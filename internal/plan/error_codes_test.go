package plan

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
)

// #144: every herdr error this package branches on is classified by the
// code herdr put in its envelope, which herdrc exposes as a sentinel, and
// never by the error's text. The text holds the argv, and the argv holds
// the user's prompt, title and branch.

// herdrErr is a failed herdr call the way herdrc.CLIRunner reports one:
// the CLI's text, verbatim, and the sentinel for the envelope's code. A
// nil code is a failure whose code none of the sentinels name.
type herdrErr struct {
	msg  string
	code error
}

func (e herdrErr) Error() string        { return e.msg }
func (e herdrErr) Is(target error) bool { return e.code != nil && target == e.code }

// busyErr is `agent start` refused with agent_pane_busy.
var busyErr = herdrErr{
	msg:  `herdr agent start fix-pagination --kind claude --pane pane-1: exit status 1: {"error":{"code":"agent_pane_busy","message":"agent target pane pane-1 is not an available shell"},"id":"cli:agent:start"}`,
	code: herdrc.ErrPaneBusy,
}

// A prompt about #71 fails as a timeout, which says the agent may already
// be working on it. The prompt names agent_pane_busy; the code does not.
// A resend is the #108 injury.
func TestExecuteDoesNotResendAPromptThatNamesBusy(t *testing.T) {
	withBusyRetryOverrides(t, 0, fakeAdvancingClock(time.Second))
	in := validInput()
	in.Prompt = "why is agent_pane_busy never seen live?"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	m := &mockRunner{
		failAt: "AgentPrompt",
		failErr: fmt.Errorf("%w: %w", herdrc.ErrPromptWaitTimeout, herdrErr{
			msg: `herdr agent prompt pane-1 why is agent_pane_busy never seen live? --wait --timeout 30000: exit status 1: {"error":{"code":"timeout","message":"timed out waiting for agent status"},"id":"cli:agent:prompt"}`,
		}),
		failCount: 1000,
		topo:      herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
	}

	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

	if n := countCallsWithPrefix(m.calls, "AgentPrompt("); n != 1 {
		t.Fatalf("AgentPrompt calls = %d, want 1 -- a timed-out prompt must never be sent again\ncalls: %v", n, m.calls)
	}
	if !result.PromptUnconfirmed {
		t.Error("PromptUnconfirmed = false, want the timeout's unconfirmed posture")
	}
}

// The title reaches the worktree create's argv as --label, and herdr may
// fail that call after git made the checkout (worktree_open_failed). A
// title naming agent_pane_busy must not buy a second attempt.
func TestExecuteDoesNotRetryAWorktreeWhoseTitleNamesBusy(t *testing.T) {
	withBusyRetryOverrides(t, 0, fakeAdvancingClock(time.Second))
	in := validInput()
	in.UseWorktree = true
	in.Title = "Fix agent_pane_busy"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	m := &mockRunner{
		failAt: "WorktreeCreate",
		failErr: herdrErr{
			msg: `herdr worktree create --cwd /repo --branch zvi/fix-pagination --base main --label Fix agent_pane_busy --no-focus: exit status 1: {"error":{"code":"worktree_open_failed"}}`,
		},
		failCount: 1000,
	}

	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

	if n := countCallsWithPrefix(m.calls, "WorktreeCreate("); n != 1 {
		t.Fatalf("WorktreeCreate calls = %d, want 1\ncalls: %v", n, m.calls)
	}
	if result.FailedIndex != indexOfKind(ops, OpWorktreeCreate) {
		t.Fatalf("FailedIndex = %d, want the worktree step", result.FailedIndex)
	}
}

// #218's route: the worktree step's own wrongBranch error names both
// branches, so a typed branch naming agent_pane_busy used to re-run the
// step after herdr had made the checkout -- and the checkout dropped out
// of the result, with no keep-or-clean offered for it.
func TestExecuteDoesNotRetryAWrongBranchThatNamesBusy(t *testing.T) {
	withBusyRetryOverrides(t, 0, fakeAdvancingClock(time.Second))
	repo, path, m := guessedClone(t)
	add := m.onWorktreeCreate
	m.onWorktreeCreate = func(req herdrc.WorktreeCreateReq) {
		if countCallsWithPrefix(m.calls, "WorktreeCreate(") == 1 {
			add(req)
		}
	}
	in := worktreeInput(repo, "smoke/agent_pane_busy")
	in.BaseRef = "develop"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

	if n := countCallsWithPrefix(m.calls, "WorktreeCreate("); n != 1 {
		t.Fatalf("WorktreeCreate calls = %d, want 1\ncalls: %v", n, m.calls)
	}
	if result.Created == nil || result.Created.CheckoutPath != filepath.Clean(path) {
		t.Fatalf("Created = %+v, want the checkout at %s, for the keep-or-clean gate", result.Created, path)
	}
}

// And the other side of the same rule: herdr's real busy code is still
// retried, and the name-taken and not-ready codes still reach their own
// handling, whatever their text says.
func TestExecuteClassifiesByCodeNotText(t *testing.T) {
	withBusyRetryOverrides(t, 0, nil)
	in := validInput()
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	m := &mockRunner{
		failAt:    "AgentStart",
		failErr:   herdrErr{msg: "herdr agent start: exit status 1", code: herdrc.ErrPaneBusy},
		failCount: 2,
		topo:      herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
	}

	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

	if result.FailedIndex != -1 {
		t.Fatalf("FailedIndex = %d, want -1 -- a busy code is retried however its text reads", result.FailedIndex)
	}
	if n := countCallsWithPrefix(m.calls, "AgentStart("); n != 3 {
		t.Fatalf("AgentStart calls = %d, want 3", n)
	}

	taken := &mockRunner{
		failAt:    "AgentStart",
		failErr:   errors.New(`herdr agent start fix-agent_name_taken --kind claude: exit status 1: {"error":{"code":"agent_start_failed"}}`),
		failCount: 1000,
	}
	if err := startAgentWithDedupe(context.Background(), taken, herdrc.AgentStartReq{Name: "fix-agent_name_taken"}); err == nil {
		t.Fatal("startAgentWithDedupe = nil, want the start's own failure")
	}
	if n := countCallsWithPrefix(taken.calls, "AgentStart("); n != 1 {
		t.Errorf("AgentStart calls = %d, want 1 -- a name that names agent_name_taken is not a taken name\ncalls: %v", n, taken.calls)
	}

	if isBlockedAgentError(errors.New(`herdr agent start x --kind claude: {"error":{"code":"agent_not_ready"}}`)) {
		t.Error("isBlockedAgentError = true for text that merely names agent_not_ready")
	}
	if !isBlockedAgentError(fmt.Errorf("wrapped: %w", herdrErr{msg: "refused", code: herdrc.ErrAgentNotReady})) {
		t.Error("isBlockedAgentError = false for herdr's agent_not_ready code")
	}
}
