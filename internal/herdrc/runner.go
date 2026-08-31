package herdrc

import (
	"bytes"
	"context"
	"encoding/json"
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

// focusFlag returns "--focus" or "--no-focus": herdr's CLI models placement
// focus as two explicit mutually exclusive flags rather than a single
// toggle, so every creation call must pass exactly one.
func focusFlag(focus bool) string {
	if focus {
		return "--focus"
	}
	return "--no-focus"
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
	if req.Cwd != "" {
		args = append(args, "--cwd", req.Cwd)
	}
	if req.Branch != "" {
		args = append(args, "--branch", req.Branch)
	}
	if req.Base != "" {
		args = append(args, "--base", req.Base)
	}
	if req.Label != "" {
		args = append(args, "--label", req.Label)
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
	if req.Cwd != "" {
		args = append(args, "--cwd", req.Cwd)
	}
	if req.Label != "" {
		args = append(args, "--label", req.Label)
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
	if req.Workspace != "" {
		args = append(args, "--workspace", req.Workspace)
	}
	if req.Cwd != "" {
		args = append(args, "--cwd", req.Cwd)
	}
	if req.Label != "" {
		args = append(args, "--label", req.Label)
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
	if req.PaneID != "" {
		args = append(args, "--pane", req.PaneID)
	}
	if req.Direction != "" {
		args = append(args, "--direction", req.Direction)
	}
	if req.Cwd != "" {
		args = append(args, "--cwd", req.Cwd)
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
	args := []string{"agent", "start", req.Name, "--kind", req.Kind, "--pane", req.PaneID}
	if len(req.ExtraArgs) > 0 {
		args = append(args, "--")
		args = append(args, req.ExtraArgs...)
	}
	_, err := r.runJSON(ctx, args...)
	return err
}

// AgentPrompt runs `herdr agent prompt --wait` (spec §9 step 3).
func (r *CLIRunner) AgentPrompt(ctx context.Context, req AgentPromptReq) error {
	args := []string{"agent", "prompt", req.Target, req.Text, "--wait"}
	if req.WaitTimeout > 0 {
		args = append(args, "--timeout", strconv.FormatInt(req.WaitTimeout.Milliseconds(), 10))
	}
	_, err := r.runJSON(ctx, args...)
	return err
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
func (r *CLIRunner) AwaitDetection(ctx context.Context, paneID string, timeout time.Duration) error {
	start := time.Now()
	deadline := start.Add(timeout)
	interval := r.pollInterval()

	for {
		if err := r.pollDetection(ctx, paneID); err == nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("await detection for pane %s: %w", paneID, err)
		}

		now := time.Now()
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

// PaneRun runs `herdr pane run <paneID> <argv...>`. Implemented in Task 7.
func (r *CLIRunner) PaneRun(ctx context.Context, paneID string, argv []string) error {
	return fmt.Errorf("not implemented")
}

// WorktreeRemove runs `herdr worktree remove --workspace <workspaceID>`.
func (r *CLIRunner) WorktreeRemove(ctx context.Context, workspaceID string) error {
	_, err := r.runJSON(ctx, "worktree", "remove", "--workspace", workspaceID)
	return err
}

// WorkspaceClose runs `herdr workspace close <workspaceID>`.
func (r *CLIRunner) WorkspaceClose(ctx context.Context, workspaceID string) error {
	_, err := r.runJSON(ctx, "workspace", "close", workspaceID)
	return err
}
