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
// (/home/zvi/Projects/herdr/src/api/schema/workspaces.rs, ~line 59), as
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
	AwaitDetection(ctx context.Context, paneID string, timeout time.Duration) error
	PaneRun(ctx context.Context, paneID string, argv []string) error
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
// /home/zvi/Projects/herdr/src/api/schema/panes.rs's PaneInfo and confirmed
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
// success -- herdr's own CLI has two distinct success-reporting shapes
// (herdr:src/cli.rs): `print_response(&send_request(...))`, which every
// method runJSON is used for goes through, and `send_ok_request(...)`,
// which reports success by exit code ALONE and prints nothing at all on
// stdout (`pane run`'s contract -- see runOK's own doc comment, and
// task-19-report.md's live checkpoint, which found this the hard way:
// PaneRun used to route through runJSON and therefore failed every single
// real invocation with "parse response: unexpected end of JSON input",
// even though the underlying `pane run` had already succeeded).
func (r *CLIRunner) runJSON(ctx context.Context, args ...string) (json.RawMessage, error) {
	verb := strings.Join(args, " ")

	cmd := exec.CommandContext(ctx, r.Bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("herdr %s: %w: %s", verb, err, strings.TrimSpace(stderr.String()))
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
		return fmt.Errorf("herdr %s: %w: %s", verb, err, strings.TrimSpace(stderr.String()))
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
		return "", fmt.Errorf("herdr %s: %w: %s", verb, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

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
	_, err = r.runJSON(ctx, args...)
	return err
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

// pollDetection runs `herdr agent get <paneID>` and reports only whether it
// exited zero. AwaitDetection only needs a detected/not-yet boolean signal
// -- any status counts as detected -- so this bypasses runJSON's response
// parsing entirely and discards stdout/stderr.
func (r *CLIRunner) pollDetection(ctx context.Context, paneID string) error {
	cmd := exec.CommandContext(ctx, r.Bin, "agent", "get", paneID)
	return cmd.Run()
}

// AwaitDetection polls `herdr agent get <paneID>` every PollInterval until
// it exits zero (an agent was detected, in any status) or timeout elapses
// since AwaitDetection was called. The returned error names the pane id and
// the elapsed wait when it times out, or wraps ctx's error if ctx is
// cancelled first.
//
// Every poll runs against a deadline-bound child context so a single hung
// `herdr agent get` (e.g. an unresponsive server) is killed at the deadline
// rather than being allowed to run past timeout, and the deadline is
// checked before issuing each poll -- not only after one returns -- so no
// poll is ever started once the deadline has already passed.
func (r *CLIRunner) AwaitDetection(ctx context.Context, paneID string, timeout time.Duration) error {
	start := time.Now()
	deadline := start.Add(timeout)
	interval := r.pollInterval()

	deadlineCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("await detection for pane %s: %w", paneID, err)
		}
		now := time.Now()
		if !now.Before(deadline) {
			return fmt.Errorf("await detection for pane %s: timed out after %s", paneID, now.Sub(start).Round(time.Millisecond))
		}

		if err := r.pollDetection(deadlineCtx, paneID); err == nil {
			return nil
		}

		now = time.Now()
		if !now.Before(deadline) {
			return fmt.Errorf("await detection for pane %s: timed out after %s", paneID, now.Sub(start).Round(time.Millisecond))
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

// PaneRun runs `herdr pane run <paneID> <argv...>`, which types argv into
// the pane's shell and submits it atomically (send-text + Enter; spec §9
// Path B's launch primitive -- no raw socket use anywhere in the plugin).
// This goes through runOK, not runJSON: `pane run` prints nothing on
// success (see runOK's own doc comment) -- routing it through runJSON was
// task 19's live-checkpoint defect, unconditionally failing every real
// Path B launch.
func (r *CLIRunner) PaneRun(ctx context.Context, paneID string, argv []string) error {
	args := append([]string{"pane", "run", paneID}, argv...)
	return r.runOK(ctx, args...)
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
