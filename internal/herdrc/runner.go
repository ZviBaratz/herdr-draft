package herdrc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// CreatedTopology identifies the workspace/tab/pane a creation call (worktree
// create, workspace create, tab create, pane split) opened, plus -- for a
// worktree creation -- the checkout path on disk. CheckoutPath is "" for
// non-worktree creations.
type CreatedTopology struct {
	WorkspaceID  string
	TabID        string
	PaneID       string
	CheckoutPath string
}

// WorkspaceInfo mirrors herdr's WorkspaceInfo response shape
// (https://github.com/herdrdev/herdr/blob/b1ff4582/src/api/schema/workspaces.rs#L59),
// as
// returned by `herdr workspace list`.
type WorkspaceInfo struct {
	WorkspaceID string           `json:"workspace_id"`
	Number      int              `json:"number"`
	Label       string           `json:"label"`
	Focused     bool             `json:"focused"`
	PaneCount   int              `json:"pane_count"`
	TabCount    int              `json:"tab_count"`
	ActiveTabID string           `json:"active_tab_id"`
	AgentStatus string           `json:"agent_status"`
	Worktree    *ContextWorktree `json:"worktree"`
}

// WorktreeCreateReq is the request shape for `herdr worktree create`
// (spec §9).
type WorktreeCreateReq struct {
	Cwd             string
	Branch          string
	Base            string
	Label           string
	Focus           bool
	TrustRepository bool
}

// WorkspaceCreateReq is the request shape for `herdr workspace create`
// (spec §9).
type WorkspaceCreateReq struct {
	Cwd   string
	Label string
	Focus bool
}

// TabCreateReq is the request shape for `herdr tab create` (spec §9).
type TabCreateReq struct {
	Workspace string
	Cwd       string
	Label     string
	Focus     bool
}

// PaneSplitReq is the request shape for `herdr pane split` (spec §9).
type PaneSplitReq struct {
	PaneID    string
	Direction string
	Cwd       string
	Focus     bool
}

// AgentStartReq is the request shape for `herdr agent start` (spec §9 Path A).
type AgentStartReq struct {
	Name      string
	Kind      string
	PaneID    string
	ExtraArgs []string
}

// AgentPromptReq is the request shape for `herdr agent prompt --wait`
// (spec §9 step 3).
type AgentPromptReq struct {
	Target      string
	Text        string
	WaitTimeout time.Duration
}

// EnvVar is one environment assignment prefixed to a PaneRun command line:
// `NAME=value cmd …`, the shell's own way of setting a variable for one
// command.
//
// It is a separate parameter from argv rather than a leading argv element
// because the two are quoted DIFFERENTLY, and that difference is the whole
// reason this type exists. PaneRun shell-quotes every argv element (#72),
// which is correct for a word and wrong for an assignment: quoting the whole
// `NAME=value` gives the shell a command name.
//
// It is also not `env NAME=value cmd`. env(1) execs its target, which
// bypasses shell functions -- and the only reason to want an environment
// prefix here at all is a `claude` the user's login shell resolves to a
// function of their own (`[clauth] launch = "wrapper"`).
type EnvVar struct{ Name, Value string }

// Runner performs herdr operations needed to create and control agent
// sessions from the plugin. Every implementation drives the herdr CLI
// executable rather than the socket API directly.
type Runner interface {
	WorkspaceList(ctx context.Context) ([]WorkspaceInfo, error)
	WorktreeCreate(ctx context.Context, req WorktreeCreateReq) (CreatedTopology, error)
	WorkspaceCreate(ctx context.Context, req WorkspaceCreateReq) (CreatedTopology, error)
	TabCreate(ctx context.Context, req TabCreateReq) (CreatedTopology, error)
	PaneSplit(ctx context.Context, req PaneSplitReq) (CreatedTopology, error)
	AgentStart(ctx context.Context, req AgentStartReq) error
	AgentPrompt(ctx context.Context, req AgentPromptReq) error
	AgentRead(ctx context.Context, target string) (string, error)
	// AwaitDetection polls until the agent in paneID is ready. timeout is
	// the ordinary detection budget; blockedTimeout, when > 0, is the
	// separate budget a blocked agent gets (#115) -- pass 0 to keep
	// blocked terminal.
	AwaitDetection(ctx context.Context, paneID string, timeout, blockedTimeout time.Duration) error
	// PaneRun types a command into a pane's shell. Pass a plain argv:
	// the implementation shell-quotes each element, because herdr's own
	// `pane run` joins and types rather than execs (#72). env, when
	// non-empty, is emitted ahead of argv as shell assignments -- see
	// EnvVar for why it cannot simply be more argv.
	PaneRun(ctx context.Context, paneID string, env []EnvVar, argv []string) error
	// PaneClose runs `herdr pane close <pane_id>` -- placement spec §5.4:
	// when Execute claimed a fresh pane for the agent in a workspace it did
	// not create (a reuse correction, §5.2), Clean must close THAT pane
	// before removing the space, so nothing is left running an agent in a
	// directory about to be deleted. Never called when AgentAt.PaneID ==
	// Created.PaneID -- the common case, where there is nothing extra to
	// close.
	PaneClose(ctx context.Context, paneID string) error
	WorktreeRemove(ctx context.Context, workspaceID string) error
	WorkspaceClose(ctx context.Context, workspaceID string) error
}

// defaultPollInterval is CLIRunner's polling cadence for AwaitDetection when
// PollInterval is left at its zero value.
const defaultPollInterval = 500 * time.Millisecond

// CLIRunner implements Runner by invoking the herdr CLI executable. It
// reads HERDR_SOCKET_PATH from its own process environment exactly like any
// other herdr CLI invocation -- CLIRunner never opens the socket itself.
type CLIRunner struct {
	// Bin is the herdr executable to invoke (e.g. "herdr", or an absolute
	// path).
	Bin string
	// PollInterval is how often AwaitDetection polls `herdr agent get`.
	// Zero means defaultPollInterval (500ms).
	PollInterval time.Duration
}

var _ Runner = (*CLIRunner)(nil)

// pollInterval returns r.PollInterval, defaulting to defaultPollInterval
// when it is zero.
func (r *CLIRunner) pollInterval() time.Duration {
	if r.PollInterval > 0 {
		return r.PollInterval
	}
	return defaultPollInterval
}

// paneRef is the pane/tab/workspace identity embedded in herdr's pane
// response objects (root_pane, pane): see the "pane_id"/"tab_id"/
// "workspace_id" fields consistently present across
// https://github.com/herdrdev/herdr/blob/b1ff4582/src/api/schema/panes.rs
// (PaneInfo) and confirmed
// in the live-captured fixtures under testdata/live/.
type paneRef struct {
	PaneID      string `json:"pane_id"`
	TabID       string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
}

// runJSON runs `herdr <args...>`, decodes its JSON envelope
// (`{"id":...,"result":{...}}`) on success, and returns the raw `result`
// payload for the caller to unmarshal further. A non-zero exit is reported
// as an error naming the herdr subcommand invoked and including its stderr
// output.
//
// Only use this for a subcommand that actually prints a JSON envelope on
// success -- herdr's own CLI has three distinct success-reporting shapes
// (herdr:src/cli.rs): `print_response(&send_request(...))`, which every
// method runJSON is used for goes through; `send_ok_request(...)`, which
// reports success by exit code ALONE and prints nothing at all on stdout
// (`pane run`'s contract -- see runOK); and `print_read_response(...)`,
// which prints a bare text payload with no envelope (`agent read`'s
// contract -- see runText).
//
// The v1 close-out live checkpoint found this the hard way: PaneRun used
// to route through runJSON and therefore failed every single real
// invocation with "parse response: unexpected end of JSON input", even
// though the underlying `pane run` had already succeeded -- and its unit
// test passed throughout, because that test's fake printed a JSON stdout
// fixture `pane run` never actually produces. Every other Runner-wrapped
// subcommand was then read directly in herdr's own source
// (`workspace_list`/`workspace_create`/`workspace_close`/`tab_create`/
// `worktree_create`/`worktree_remove`/`pane_split` in src/cli/runtime.rs,
// `agent_start`/`agent_get`/`agent_prompt` in src/cli/agent.rs) and every
// one of them prints an envelope: `pane run` is the only silent one. So
// check which shape a NEW subcommand uses before wiring it up, and give
// its fake the same shape the real CLI has -- a fake with the wrong
// stdout contract is exactly why a PaneRun that failed against every real
// invocation still passed its own unit test.
func (r *CLIRunner) runJSON(ctx context.Context, args ...string) (json.RawMessage, error) {
	verb := strings.Join(args, " ")

	cmd := exec.CommandContext(ctx, r.Bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, cmdError(verb, err, stderr.String())
	}

	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		return nil, fmt.Errorf("herdr %s: parse response: %w", verb, err)
	}
	return envelope.Result, nil
}

// runOK runs `herdr <args...>` for a subcommand whose success contract is
// exit code alone, with no JSON envelope (or any other output) printed on
// stdout -- herdr's own `send_ok_request` helper (herdr:src/cli.rs), which
// `pane run` (herdr:src/cli/pane.rs `pane_run`) is built on. Unlike
// runJSON, this never touches stdout at all (any bytes a future herdr
// version happens to print there are simply discarded, not required to be
// empty or valid JSON -- the contract this function relies on is exit code
// only). A non-zero exit is reported the same way runJSON reports one:
// naming the subcommand invoked and including its stderr output (a failed
// send_ok_request call writes its JSON error envelope to stderr, not
// stdout -- see send_ok_request's own body -- so that text still reaches
// the caller here exactly as it does for every runJSON-backed method).
func (r *CLIRunner) runOK(ctx context.Context, args ...string) error {
	verb := strings.Join(args, " ")

	cmd := exec.CommandContext(ctx, r.Bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return cmdError(verb, err, stderr.String())
	}
	return nil
}

// runText runs `herdr <args...>` for a subcommand that prints its own
// plain-text payload directly to stdout on success -- no JSON envelope --
// herdr's own `print_read_response` helper (herdr:src/cli.rs), which
// `agent read`/`pane read` (with `--format text`) are built on. Returns
// stdout verbatim (including empty). A non-zero exit is reported the same
// way runJSON/runOK report one: naming the subcommand invoked and
// including its stderr output (print_read_response writes its JSON error
// envelope to stderr on failure, not stdout, exactly like send_ok_request).
func (r *CLIRunner) runText(ctx context.Context, args ...string) (string, error) {
	verb := strings.Join(args, " ")

	cmd := exec.CommandContext(ctx, r.Bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", cmdError(verb, err, stderr.String())
	}
	return stdout.String(), nil
}

// cmdError formats a failed `herdr <verb>` uniformly for all three run
// helpers: the subcommand invoked, the process error, and herdr's own
// stderr -- but only when there IS stderr.
//
// The conditional is the point. All three helpers used to append `: %s`
// unconditionally, so a subcommand that failed silently (a bare non-zero
// exit, which is exactly what an unreachable server can produce) produced
// a message ending in a dangling colon-space:
//
//	herdr unreachable: herdr workspace list: exit status 1:
//
// That trailing separator reads as truncation -- as though the actual
// reason were there and got lost -- which is the opposite of what it
// means. There is no reason; herdr said nothing.
func cmdError(verb string, err error, stderr string) error {
	return &cliError{verb: verb, stderr: stderr, code: herdrEnvelopeCode(stderr), err: err}
}

// cliError is a failed herdr CLI invocation.
//
// Its message is byte-for-byte what cmdError has always produced --
// `herdr <verb>: <exit status>: <stderr>` -- and that is a requirement,
// not an accident: isBusyPaneError, isNameTakenError and
// isAgentNotReadyError all substring-match this text, so changing a byte
// of it would retire three classifiers silently.
//
// What it adds is code, herdr's own `error.code`, parsed HERE where the
// stderr envelope is still a discrete value. That is the whole difference
// between reading herdr's verdict and grepping a string that also contains
// the caller's prompt text and the flags this package chose -- see
// ErrPromptWaitTimeout (#108).
type cliError struct {
	verb   string
	stderr string
	code   string
	err    error
}

func (e *cliError) Error() string {
	if s := strings.TrimSpace(e.stderr); s != "" {
		return fmt.Sprintf("herdr %s: %v: %s", e.verb, e.err, s)
	}
	return fmt.Sprintf("herdr %s: %v", e.verb, e.err)
}

// Unwrap keeps the underlying exec error reachable, so an errors.Is
// against something like exec.ErrNotFound still works through this type
// exactly as it did through cmdError's old %w chain.
func (e *cliError) Unwrap() error { return e.err }

// herdrEnvelopeCode returns the `error.code` from a herdr JSON error
// envelope, or "" when stderr is not one.
//
// A failed herdr request prints
// `{"error":{"code":...,"message":...},"id":...}` to STDERR
// (herdr:src/cli.rs's send_request / send_ok_request), which is how the
// code reaches this package at all. Reading stderr and only stderr is the
// point: the error's own message additionally contains the verb, and the
// verb contains whatever text the caller asked to send.
func herdrEnvelopeCode(stderr string) string {
	s := strings.TrimSpace(stderr)
	if !strings.HasPrefix(s, "{") {
		return ""
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(s), &envelope); err != nil {
		return ""
	}
	return envelope.Error.Code
}

// herdrErrorCode returns the herdr error code err carries, or "" if it
// carries none -- either because it is not a CLI failure at all, or
// because herdr wrote something other than an envelope to stderr.
func herdrErrorCode(err error) string {
	var e *cliError
	if errors.As(err, &e) {
		return e.code
	}
	return ""
}

// promptWaitTimeoutCode is herdr's `error.code` for `agent prompt --wait`
// giving up. Unlike the other codes named in this package it is a word
// generic enough to appear in ordinary prose, which is precisely why it is
// only ever compared against a PARSED envelope field and never matched
// against an error's text.
const promptWaitTimeoutCode = "timeout"

// ErrPromptWaitTimeout reports that `herdr agent prompt --wait` stopped
// waiting for the agent's status to change. It does NOT report that the
// prompt failed to arrive, and conflating the two is the #108 defect:
// herdr 0.9.0 completes this wait only on "observed working or blocked
// activity" (its own note for #3506/#3685), so a large prompt into an
// agent slow to PAINT a status transition can exceed the timeout while
// being perfectly fine. The prompt was handed to herdr; what timed out is
// the confirmation.
//
// A typed sentinel for the same reason ErrAgentBlocked is one: callers
// must be able to tell this apart from every other prompt failure without
// grepping prose. Here the reason is sharper still -- the prose in
// question embeds the caller's own prompt text.
var ErrPromptWaitTimeout = errors.New("timed out waiting for the agent's status to change, so delivery is unconfirmed")

// promptStalledCode is herdr's `error.code` for `agent prompt` observing no
// lifecycle change at all within its five-second window
// (herdr:src/app/api/agents.rs). Unlike promptWaitTimeoutCode beside it this
// one is specific enough to be unmistakable, but it is read from the parsed
// envelope for the same reason: the error's own text carries the caller's
// prompt.
const promptStalledCode = "agent_prompt_stalled"

// ErrPromptStalled reports that herdr sent the prompt and then saw the agent
// do NOTHING -- no working state, no blocked state -- within its five-second
// window.
//
// It is the opposite conclusion from ErrPromptWaitTimeout above, and keeping
// the two apart is the whole reason it exists. That one means a wait gave up
// while the agent was demonstrably busy, so delivery is UNKNOWN and must not
// be re-attempted. This one means herdr observed no reaction at all, which is
// the positive evidence that nothing was processed.
//
// Live on herdr 0.9.0 (docs/manual-smoke.md, 2026-09-09) it has one dominant
// cause: for a second or so after Claude Code's trust dialog is answered,
// `agent get` reports the agent idle and interactive_ready while its TUI is
// not yet accepting input. herdr types, nothing happens, and the pane is left
// with an empty buffer and no turn -- so a caller that waits a moment and
// sends again delivers the prompt exactly once rather than not at all.
var ErrPromptStalled = errors.New("the agent was not accepting input yet, so nothing was delivered")

// focusFlag returns "--focus" or "--no-focus": herdr's CLI models placement
// focus as two explicit mutually exclusive flags rather than a single
// toggle, so every creation call must pass exactly one.
func focusFlag(focus bool) string {
	if focus {
		return "--focus"
	}
	return "--no-focus"
}

// errRefused marks a refusal to execute -- an argv this file declines to
// hand to the herdr binary at all -- so it can be told apart from an
// execution that failed, which is what every other error here reports.
// Unexported because no caller branches on it yet: in-package tests assert
// it with errors.Is, and plan.Execute's own `%w` wrapping keeps it
// reachable if one ever does.
var errRefused = errors.New("refusing to run herdr")

// appendFlag appends a `--flag value` pair to args, refusing a value the
// herdr CLI would read as another flag instead of as the value.
//
// Every `--flag`/value pair CLIRunner builds goes through this one helper
// rather than through a guard at each call site, so a subcommand wired up
// later cannot reopen the hole by forgetting to check: plain
// `args = append(args, "--base", v)` is the shape it replaces throughout
// this file. (Flag values written as string literals -- AgentRead's
// `--source detection --format text` -- are the sole exception: a constant
// cannot begin with "-".)
//
// A "-"-leading value is refused rather than escaped because herdr offers
// nothing to escape it with. The `--flag=value` form, which would make the
// leading "-" unambiguous, is not accepted: `herdr tab list
// --workspace=wG` answers `unknown option: --workspace=wG`. A `--`
// end-of-flags terminator is no help either: every creation command here
// appends --focus/--no-focus after these values, which such a terminator
// would swallow. Refusal is what is left, and it is defensible on its own
// terms: a ref, path or label
// beginning with "-" is pathological. gitx.ValidateBranchPrefix rejects
// the same shape one layer up, for the same reason -- this closes it for
// every flag rather than for that one configured value.
//
// The refusal reads "refusing to run herdr: ..." where an execution that
// failed reads "herdr <argv>: <exit status>: <stderr>", and it happens
// while the argv is still being assembled -- before runJSON, runOK or
// runText is reached, so nothing is executed.
//
// An empty value keeps this file's established meaning of "omit the flag
// entirely" and is not an error: every optional field on the *Req structs
// signals "unset" that way.
//
// Only flag *values* are covered. Positional arguments carry the same
// parsing hazard but cannot take the same answer: PaneRun's argv is a
// command line whose own flags are the point, and AgentPrompt's text is
// free prose that may legitimately open with "-". Those need a herdr-side
// `--` their subcommands honor, not a refusal here. (`agent start`'s name
// positional is already safe by construction: plan.AgentName always
// returns a [a-z]-initial slug.)
func appendFlag(args []string, flag, value string) ([]string, error) {
	if value == "" {
		return args, nil
	}
	if strings.HasPrefix(value, "-") {
		// Returns nil, not args: a caller that ignored the error would
		// then run herdr with no arguments at all rather than silently
		// with the flag dropped.
		return nil, fmt.Errorf(`%w: %s value %q begins with "-", which the herdr CLI reads as another flag`, errRefused, flag, value)
	}
	return append(args, flag, value), nil
}

// WorkspaceList runs `herdr workspace list`.
func (r *CLIRunner) WorkspaceList(ctx context.Context) ([]WorkspaceInfo, error) {
	raw, err := r.runJSON(ctx, "workspace", "list")
	if err != nil {
		return nil, err
	}

	var result struct {
		Workspaces []WorkspaceInfo `json:"workspaces"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("herdr workspace list: parse response: %w", err)
	}
	return result.Workspaces, nil
}

// WorktreeCreate runs `herdr worktree create`.
func (r *CLIRunner) WorktreeCreate(ctx context.Context, req WorktreeCreateReq) (CreatedTopology, error) {
	args := []string{"worktree", "create"}
	var err error
	if args, err = appendFlag(args, "--cwd", req.Cwd); err != nil {
		return CreatedTopology{}, err
	}
	if args, err = appendFlag(args, "--branch", req.Branch); err != nil {
		return CreatedTopology{}, err
	}
	if args, err = appendFlag(args, "--base", req.Base); err != nil {
		return CreatedTopology{}, err
	}
	if args, err = appendFlag(args, "--label", req.Label); err != nil {
		return CreatedTopology{}, err
	}
	args = append(args, focusFlag(req.Focus))
	if req.TrustRepository {
		args = append(args, "--trust-repository")
	}

	raw, err := r.runJSON(ctx, args...)
	if err != nil {
		return CreatedTopology{}, err
	}

	var result struct {
		RootPane  paneRef `json:"root_pane"`
		Workspace struct {
			WorkspaceID string           `json:"workspace_id"`
			Worktree    *ContextWorktree `json:"worktree"`
		} `json:"workspace"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return CreatedTopology{}, fmt.Errorf("herdr worktree create: parse response: %w", err)
	}

	topo := CreatedTopology{
		WorkspaceID: result.RootPane.WorkspaceID,
		TabID:       result.RootPane.TabID,
		PaneID:      result.RootPane.PaneID,
	}
	if result.Workspace.Worktree != nil {
		topo.CheckoutPath = result.Workspace.Worktree.CheckoutPath
	}
	return topo, nil
}

// WorkspaceCreate runs `herdr workspace create`.
func (r *CLIRunner) WorkspaceCreate(ctx context.Context, req WorkspaceCreateReq) (CreatedTopology, error) {
	args := []string{"workspace", "create"}
	var err error
	if args, err = appendFlag(args, "--cwd", req.Cwd); err != nil {
		return CreatedTopology{}, err
	}
	if args, err = appendFlag(args, "--label", req.Label); err != nil {
		return CreatedTopology{}, err
	}
	args = append(args, focusFlag(req.Focus))

	raw, err := r.runJSON(ctx, args...)
	if err != nil {
		return CreatedTopology{}, err
	}

	var result struct {
		RootPane paneRef `json:"root_pane"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return CreatedTopology{}, fmt.Errorf("herdr workspace create: parse response: %w", err)
	}
	return CreatedTopology{
		WorkspaceID: result.RootPane.WorkspaceID,
		TabID:       result.RootPane.TabID,
		PaneID:      result.RootPane.PaneID,
	}, nil
}

// TabCreate runs `herdr tab create`.
func (r *CLIRunner) TabCreate(ctx context.Context, req TabCreateReq) (CreatedTopology, error) {
	args := []string{"tab", "create"}
	var err error
	if args, err = appendFlag(args, "--workspace", req.Workspace); err != nil {
		return CreatedTopology{}, err
	}
	if args, err = appendFlag(args, "--cwd", req.Cwd); err != nil {
		return CreatedTopology{}, err
	}
	if args, err = appendFlag(args, "--label", req.Label); err != nil {
		return CreatedTopology{}, err
	}
	args = append(args, focusFlag(req.Focus))

	raw, err := r.runJSON(ctx, args...)
	if err != nil {
		return CreatedTopology{}, err
	}

	// Unlike worktree/workspace create, tab create's response has no
	// top-level "workspace" object -- only root_pane and tab (see
	// testdata/live/tab_create.json) -- so workspace id comes from root_pane.
	var result struct {
		RootPane paneRef `json:"root_pane"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return CreatedTopology{}, fmt.Errorf("herdr tab create: parse response: %w", err)
	}
	return CreatedTopology{
		WorkspaceID: result.RootPane.WorkspaceID,
		TabID:       result.RootPane.TabID,
		PaneID:      result.RootPane.PaneID,
	}, nil
}

// PaneSplit runs `herdr pane split`.
func (r *CLIRunner) PaneSplit(ctx context.Context, req PaneSplitReq) (CreatedTopology, error) {
	args := []string{"pane", "split"}
	var err error
	if args, err = appendFlag(args, "--pane", req.PaneID); err != nil {
		return CreatedTopology{}, err
	}
	if args, err = appendFlag(args, "--direction", req.Direction); err != nil {
		return CreatedTopology{}, err
	}
	if args, err = appendFlag(args, "--cwd", req.Cwd); err != nil {
		return CreatedTopology{}, err
	}
	args = append(args, focusFlag(req.Focus))

	raw, err := r.runJSON(ctx, args...)
	if err != nil {
		return CreatedTopology{}, err
	}

	// pane split's response is a bare "pane" object, not wrapped in
	// root_pane/tab/workspace (see testdata/live/pane_split.json).
	var result struct {
		Pane paneRef `json:"pane"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return CreatedTopology{}, fmt.Errorf("herdr pane split: parse response: %w", err)
	}
	return CreatedTopology{
		WorkspaceID: result.Pane.WorkspaceID,
		TabID:       result.Pane.TabID,
		PaneID:      result.Pane.PaneID,
	}, nil
}

// AgentStart runs `herdr agent start` (spec §9 Path A).
func (r *CLIRunner) AgentStart(ctx context.Context, req AgentStartReq) error {
	// --kind and --pane used to be emitted unconditionally; routing them
	// through appendFlag gives them the same "an empty value omits the
	// flag" rule every other flag here follows. Neither is ever empty in
	// practice (plan.Build always sets a kind, plan.Execute always threads
	// a pane id in), and omitting either is safe in a way omitting
	// `worktree remove --workspace` is not: herdr requires both (`herdr
	// agent start <NAME> --kind <KIND> --pane <ID>`), so a missing one is
	// a parse error rather than a fallback to some default target.
	args, err := appendFlag([]string{"agent", "start", req.Name}, "--kind", req.Kind)
	if err != nil {
		return err
	}
	if args, err = appendFlag(args, "--pane", req.PaneID); err != nil {
		return err
	}
	if len(req.ExtraArgs) > 0 {
		// ExtraArgs is a pass-through command line whose own flags are the
		// whole point ("--resume"), which is why it goes after herdr's
		// end-of-flags "--" and not through appendFlag.
		args = append(args, "--")
		args = append(args, req.ExtraArgs...)
	}
	_, err = r.runJSON(ctx, args...)
	return err
}

// AgentPrompt runs `herdr agent prompt --wait` (spec §9 step 3).
func (r *CLIRunner) AgentPrompt(ctx context.Context, req AgentPromptReq) error {
	args := []string{"agent", "prompt", req.Target, req.Text, "--wait"}
	var err error
	if req.WaitTimeout > 0 {
		// The > 0 guard already rules out a "-"-leading duration; the flag
		// still goes through appendFlag so the funnel has no exceptions to
		// reason about.
		if args, err = appendFlag(args, "--timeout", strconv.FormatInt(req.WaitTimeout.Milliseconds(), 10)); err != nil {
			return err
		}
	}
	if _, err = r.runJSON(ctx, args...); err != nil {
		switch herdrErrorCode(err) {
		case promptWaitTimeoutCode:
			return fmt.Errorf("%w: %w", ErrPromptWaitTimeout, err)
		case promptStalledCode:
			return fmt.Errorf("%w: %w", ErrPromptStalled, err)
		}
		return err
	}
	return nil
}

// AgentRead runs `herdr agent read <target> --source detection --format
// text`, returning the pane's current detection-source screen as plain
// text. Used by internal/plan's Execute to check for a blocking
// confirmation/selection dialog before ever calling AgentPrompt (spec §9
// step 3's own principle, hardened by task 19's live checkpoint: herdr's
// own agent detection can report a pane idle/interactive_ready while it is
// actually showing a screen like Claude Code's first-run trust
// confirmation, so "detected" alone is not proof a prompt is safe to
// send). `--source detection` matches this project's own established
// convention for screen-state evidence (see CLAUDE.md's "Screen detection
// is evidence-based").
func (r *CLIRunner) AgentRead(ctx context.Context, target string) (string, error) {
	return r.runText(ctx, "agent", "read", target, "--source", "detection", "--format", "text")
}

// ErrAgentBlocked reports an agent that herdr has detected and is running,
// but which is sitting on something requiring interactive input -- in
// practice Claude Code's first-run trust prompt in a directory the
// launching account has not been trusted in yet.
//
// A typed sentinel rather than the substring match every OTHER herdr error
// in this package reaches Go through, and deliberately so: those arrive as
// the CLI's stderr wrapped into an error string, with no structure to test.
// This one is decided HERE, by AwaitDetection reading herdr's own JSON, so
// there is a real value to return and callers should not be reduced to
// grepping prose for it.
var ErrAgentBlocked = errors.New("agent is blocked and needs interactive input")

// ErrAgentGone reports an agent that WAS observed blocked and has since
// stopped being reported at all -- in practice a first-run trust prompt
// answered "No, exit".
//
// It is inference from the only evidence there is, and the doc comment
// says so rather than pretending otherwise: classifyAgent deliberately
// has no "exited" verdict, because `herdr agent get` does not report a
// dead agent -- the call simply fails, which is indistinguishable from
// "not detected yet". What makes the inference worth drawing here is the
// TRANSITION: something was being reported in that pane, and now nothing
// is. Waiting is what a blocked budget is for, but waiting five minutes
// for a session the user has already declined is not.
//
// Only ever returned when a blocked budget is in force AND blocked was
// actually observed first, so an ordinary slow launch still times out as
// a timeout rather than renaming itself.
var ErrAgentGone = errors.New("agent stopped being reported after waiting on a dialog")

// pollWaitDelay bounds how long a killed `agent get` may keep
// AwaitDetection waiting on its stdout pipe after the deadline has passed
// (see pollDetection). Short enough that the timeout stays honest, long
// enough that a healthy poll finishing normally is never truncated -- it
// only ever applies once the process has already been killed.
const pollWaitDelay = 100 * time.Millisecond

// detectionState is one pollDetection verdict.
type detectionState int

const (
	// detectionPending means keep waiting: either no agent yet, or one
	// whose status cannot yet decide the question.
	detectionPending detectionState = iota
	// detectionReady means the agent is detected AND ready for input.
	detectionReady
	// detectionBlocked means detected, running, and waiting on a dialog.
	detectionBlocked
)

// agentInfo is the subset of `herdr agent get`'s `result.agent` object that
// decides readiness. Everything else herdr reports there is deliberately
// not modelled: this type exists to answer one question.
type agentInfo struct {
	AgentStatus      string `json:"agent_status"`
	InteractiveReady bool   `json:"interactive_ready"`
	LaunchPending    bool   `json:"launch_pending"`
}

// pollDetection runs `herdr agent get <paneID>` once and classifies the
// answer.
//
// It used to report only whether the command exited zero -- "any status
// counts as detected" -- and that was the bug behind #94. herdr registers
// Claude Code as detected before it has painted its first-run trust screen:
// measured live, `agent get` succeeded at 0.48s with `agent_status:
// "unknown"` while the dialog only appeared at 0.93s. A caller that treated
// the 0.48s answer as "ready" then typed a queued prompt into a dialog that
// was about to appear, and the trailing Enter confirmed its highlighted
// "No, exit" -- destroying the agent herdr-draft had just launched.
//
// The classification below is herdr's OWN readiness rule, the one `agent
// start` applies while polling
// (https://github.com/herdrdev/herdr/blob/b1ff4582/src/cli/agent.rs#L607-L625),
// reproduced here on purpose: Path A gets its readiness guarantee from
// `agent start` server-side, and Path B -- a `clauth start` typed into a
// shell by `pane run`, which returns as soon as the TYPING succeeds -- has
// no server-side wait at all. Mirroring the rule is what makes the two
// paths mean the same thing by "detected", rather than leaving Path B with
// a weaker promise nobody wrote down.
func (r *CLIRunner) pollDetection(ctx context.Context, paneID string) detectionState {
	cmd := exec.CommandContext(ctx, r.Bin, "agent", "get", paneID)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	// WaitDelay is load-bearing here, not tidiness. This poll used to
	// discard stdout entirely, so a ctx deadline killing the child returned
	// from Run immediately. Capturing stdout into a non-*os.File makes
	// os/exec create a pipe and wait for the copying goroutine, and that
	// goroutine blocks until EVERY writer closes the write end -- including
	// a grandchild that inherited the fd and outlived the kill. Without
	// this, a hung `agent get` whose child spawned anything holds
	// AwaitDetection well past its own deadline; the existing
	// TestCLIRunnerAwaitDetectionTimeoutKillsHangingPoll caught exactly
	// that, taking 5s against a 50ms timeout.
	cmd.WaitDelay = pollWaitDelay
	if err := cmd.Run(); err != nil {
		// No agent detected in that pane yet -- the ordinary "not yet"
		// answer, and the only one this call had before.
		return detectionPending
	}

	var envelope struct {
		Result struct {
			Agent agentInfo `json:"agent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		// A detected agent whose response cannot be read is not evidence
		// of readiness. Keep waiting rather than promote an unparseable
		// answer to "ready" -- the timeout is the backstop, and it quotes
		// the pane.
		return detectionPending
	}
	return classifyAgent(envelope.Result.Agent)
}

// classifyAgent applies herdr's readiness rule to one agent record. Split
// from pollDetection so the rule is testable without a subprocess -- it is
// the whole of what this change is, and it deserves to be pinned directly
// rather than only through a fake CLI.
func classifyAgent(a agentInfo) detectionState {
	switch a.AgentStatus {
	case "blocked":
		return detectionBlocked
	case "idle", "done":
		if a.InteractiveReady {
			return detectionReady
		}
		if a.LaunchPending {
			// Settled-looking but still coming up: keep waiting.
			return detectionPending
		}
		// Settled, with NEITHER flag present. This is the ordinary ready
		// state, verified live on 0.9.0: `herdr agent get` against a
		// perfectly healthy idle claude returns the keys
		//
		//   agent, agent_session, agent_status, cwd, focused,
		//   foreground_cwd, pane_id, revision, state_change_seq, tab_id,
		//   terminal_id, terminal_title, terminal_title_stripped, tokens,
		//   workspace_id
		//
		// and no interactive_ready or launch_pending at all -- both are
		// Options on the Rust side and omitted once the launch bookkeeping
		// is done. `agent start`'s own rule reads the API's richer agent
		// record while a launch is in flight, where those fields do exist;
		// transcribing it literally against the CLI's output would classify
		// every ready Path B agent as one that had exited, which is the
		// opposite of the truth and would fail every successful launch.
		//
		// There is deliberately no "exited" verdict here: a dead agent is
		// not reported by `agent get` at all (the call fails, which reads
		// as pending and then times out with the pane quoted), so this
		// package has no way to observe that state and should not pretend
		// to.
		return detectionReady
	default:
		// "working", "unknown", and anything a later herdr adds: not an
		// answer yet. Unknown is the state #94 leaked through.
		return detectionPending
	}
}

// AwaitDetection polls `herdr agent get <paneID>` every PollInterval until
// the agent there is READY for input, until it reaches a state that will
// never become ready, or until its deadline elapses. The returned error
// names the pane id and the elapsed wait when it times out, or wraps ctx's
// error if ctx is cancelled first.
//
// "Ready", not "present": see pollDetection for why the difference cost an
// agent its life (#94).
//
// # Two budgets, because two different things are being waited on
//
// timeout is the ordinary detection budget, tuned for a machine painting a
// status transition in tens of seconds. blockedTimeout is what a BLOCKED
// agent gets instead, and it is tuned for a person reading a security
// prompt -- one number cannot serve both, which is why the caller passes
// two (#115, `[timeouts] trust_wait_ms`).
//
// blockedTimeout == 0 keeps blocked terminal: the first blocked poll ends
// the wait with ErrAgentBlocked, which internal/plan turns into the
// instruction that answers it, exactly as it does for Path A's
// `agent_not_ready`. That is the whole behaviour headless `create` wants,
// since a script has nobody at the keyboard to answer anything.
//
// blockedTimeout > 0 makes blocked PENDING and hands the wait over to the
// user: on the first blocked poll the deadline moves to blockedTimeout
// from that moment, and the poll simply carries on until they answer. The
// enabling fact is that a blocked agent is alive and addressable
// throughout -- herdr refuses to call the launch ready, it does not kill
// anything -- so this is a poll and never a re-launch.
//
// The one thing that must not happen is waiting out that whole budget for
// a session the user has already declined. An agent that has died is not
// reported by `agent get` at all, so it reads as "not yet"; what gives it
// away is the TRANSITION, so once blocked has been observed, a full
// timeout with nothing reported at all ends the wait with ErrAgentGone.
// Never for a pane that was never blocked: there, nothing was ever there
// to go, and an ordinary slow launch must still time out as a timeout.
//
// Every poll runs against a deadline-bound child context so a single hung
// `herdr agent get` (e.g. an unresponsive server) is killed at the deadline
// rather than being allowed to run past it, and the deadline is checked
// before issuing each poll -- not only after one returns -- so no poll is
// ever started once the deadline has already passed. The context is built
// per poll rather than once, because the deadline moves.
func (r *CLIRunner) AwaitDetection(ctx context.Context, paneID string, timeout, blockedTimeout time.Duration) error {
	start := time.Now()
	deadline := start.Add(timeout)
	interval := r.pollInterval()
	// lastBlocked is when the agent was most recently seen blocked, and
	// zero until it ever is -- which is also what makes ErrAgentGone
	// unreachable for a pane that never showed a dialog.
	var lastBlocked time.Time

	timedOut := func(now time.Time) error {
		return fmt.Errorf("await detection for pane %s: timed out after %s", paneID, now.Sub(start).Round(time.Millisecond))
	}

	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("await detection for pane %s: %w", paneID, err)
		}
		now := time.Now()
		if !now.Before(deadline) {
			return timedOut(now)
		}

		pollCtx, cancel := context.WithDeadline(ctx, deadline)
		state := r.pollDetection(pollCtx, paneID)
		cancel()

		switch state {
		case detectionReady:
			return nil
		case detectionBlocked:
			if blockedTimeout <= 0 {
				return fmt.Errorf("await detection for pane %s: %w", paneID, ErrAgentBlocked)
			}
			// Moved once, on the FIRST sighting, rather than pushed
			// forward on every later one: the user gets blockedTimeout to
			// answer the dialog, not blockedTimeout after they last
			// happened to be looking at it, so the total wait stays a
			// number the caller chose.
			if lastBlocked.IsZero() {
				deadline = time.Now().Add(blockedTimeout)
			}
			lastBlocked = time.Now()
		default:
			if !lastBlocked.IsZero() && time.Since(lastBlocked) >= timeout {
				return fmt.Errorf("await detection for pane %s: %w", paneID, ErrAgentGone)
			}
		}

		now = time.Now()
		if !now.Before(deadline) {
			return timedOut(now)
		}

		wait := interval
		if remaining := deadline.Sub(now); wait > remaining {
			wait = remaining
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("await detection for pane %s: %w", paneID, ctx.Err())
		case <-time.After(wait):
		}
	}
}

// shellSafeChars is the set a POSIX shell cannot misread: no expansion, no
// word splitting, no quoting significance. Deliberately conservative --
// over-quoting a rare character costs nothing, while reasoning about every
// shell's expansions costs correctness -- but wide enough that ordinary
// flags, paths, and values pass through untouched, which is what keeps the
// command herdr types into the user's own pane readable.
const shellSafeChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_@%+=:,./-"

// shellQuote returns s as exactly one word for a POSIX shell.
//
// Single quotes rather than backslashes because inside them nothing is
// special at all; the one thing they cannot hold is a single quote, so an
// embedded one has to close the quoting, escape itself with a backslash,
// and reopen. The empty string has to be quoted too, or it would vanish
// from the command line entirely.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	for _, r := range s {
		if !strings.ContainsRune(shellSafeChars, r) {
			return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
		}
	}
	return s
}

// PaneRun runs `herdr pane run <paneID> <argv...>`, which types argv into
// the pane's shell and submits it atomically (send-text + Enter; spec §9
// Path B's launch primitive -- no raw socket use anywhere in the plugin).
// This goes through runOK, not runJSON: `pane run` prints nothing on
// success (see runOK's own doc comment) -- routing it through runJSON was
// task 19's live-checkpoint defect, unconditionally failing every real
// Path B launch.
//
// Each element is shell-quoted here, and that is load-bearing rather than
// defensive (#72). "Types argv into the pane's shell" is literal: herdr
// joins args[1..] with single spaces and sends the string as input
// (https://github.com/herdrdev/herdr/blob/b1ff4582/src/cli/pane.rs#L1047),
// so the argv structure is gone by the time anything runs and the pane's
// own shell re-parses the result. Passing values through unquoted let a
// model id containing brackets become `zsh: no matches found`, with the
// agent never starting and this call still reporting success -- its exit
// code covers the typing, not the command.
//
// Quoting belongs HERE, not in the value a caller supplies, because the
// same value also goes to AgentStart, which herdr execs as an argv vector
// with no shell at all: one spelling has to serve both, and only the
// unquoted one can. A config.toml that pre-quoted its `[agents.extra_args]`
// to survive this path was the documented workaround until this fix, and it
// broke the other one -- claude received a model name beginning with an
// apostrophe.
//
// The env prefix is the one thing here that is NOT quoted whole, and EnvVar's
// own doc comment says why: `'NAME=value' cmd` asks the shell for a command
// called `NAME=value`. Name and value are quoted separately -- the name
// against a whitelist (envNameOK), the value with the same shellQuote every
// argv element gets.
func (r *CLIRunner) PaneRun(ctx context.Context, paneID string, env []EnvVar, argv []string) error {
	args := make([]string, 0, len(argv)+len(env)+3)
	args = append(args, "pane", "run", paneID)
	for _, e := range env {
		if !envNameOK(e.Name) {
			return fmt.Errorf("%w pane run: %q is not a usable environment variable name", errRefused, e.Name)
		}
		args = append(args, e.Name+"="+shellQuote(e.Value))
	}
	for _, a := range argv {
		args = append(args, shellQuote(a))
	}
	return r.runOK(ctx, args...)
}

// envNameOK reports whether name is a portable shell variable name --
// [A-Za-z_][A-Za-z0-9_]* -- and therefore safe to type UNQUOTED, which is the
// only way an assignment prefix can be spelled.
//
// The value beside it is quoted and so cannot escape; the name is the half
// with no protection, so it gets a whitelist rather than an escaping rule.
// Every name herdr-draft itself passes is a compile-time constant, so this
// guard is for a future caller, and for the reader who needs to see that the
// unquoted half was thought about rather than overlooked.
func envNameOK(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r == '_':
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

// PaneClose runs `herdr pane close <pane_id>`. herdr's own JSON success
// envelope is {"type":"ok"} with nothing else to parse -- runJSON's raw
// Result is discarded rather than unmarshalled into anything, matching
// WorkspaceClose's own handling of the same shape.
//
// The empty-id guard mirrors WorktreeRemove's own reasoning: a caller
// holding no pane id must not silently close whatever pane herdr would
// pick by default.
func (r *CLIRunner) PaneClose(ctx context.Context, paneID string) error {
	if paneID == "" {
		return fmt.Errorf("%w pane close: no pane id to close", errRefused)
	}
	_, err := r.runJSON(ctx, "pane", "close", paneID)
	return err
}

// WorktreeRemove runs `herdr worktree remove --workspace <workspaceID>`.
//
// The empty workspace id is refused rather than passed on, because this is
// the one flag here whose omission changes what herdr acts on instead of
// making it fail: herdr's own parser treats --workspace as optional
// (`herdr worktree remove [OPTIONS]`), and appendFlag omits a flag whose
// value is empty. Without this guard, a caller holding no workspace id --
// a Clean after a submit that failed before step 1 returned a topology --
// would issue a bare `herdr worktree remove` against whatever default
// target herdr picks. Passing the empty id through as an empty argv
// element, which is what this did before appendFlag, left the rejection to
// herdr; refusing locally is the same outcome without the round trip.
func (r *CLIRunner) WorktreeRemove(ctx context.Context, workspaceID string) error {
	if workspaceID == "" {
		return fmt.Errorf("%w worktree remove: no workspace id to remove", errRefused)
	}
	args, err := appendFlag([]string{"worktree", "remove"}, "--workspace", workspaceID)
	if err != nil {
		return err
	}
	_, err = r.runJSON(ctx, args...)
	return err
}

// WorkspaceClose runs `herdr workspace close <workspaceID>`.
func (r *CLIRunner) WorkspaceClose(ctx context.Context, workspaceID string) error {
	_, err := r.runJSON(ctx, "workspace", "close", workspaceID)
	return err
}
