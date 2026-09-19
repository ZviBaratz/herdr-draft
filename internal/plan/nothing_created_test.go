package plan

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
)

// markedErr is a failure marked herdrc.ErrNothingCreated the way
// herdrc.CLIRunner marks one: errors.Is finds the mark, and the text is
// herdr's alone.
type markedErr struct{ msg string }

func (e markedErr) Error() string        { return e.msg }
func (e markedErr) Is(target error) bool { return target == herdrc.ErrNothingCreated }

// refusedUnchanged is a failure herdr refused before changing anything
// (#192), as herdrc.CLIRunner reports it.
func refusedUnchanged(code string) error {
	return markedErr{msg: fmt.Sprintf(`herdr: exit status 1: {"error":{"code":%q}}`, code)}
}

// TestExecute_NothingCreatedWhenHerdrRefusesTheSpace is #192's own case: the
// first op, the one that makes the space, refused by herdr before anything
// existed -- linked_worktree_source for a worktree, and the same fact for a
// workspace.
func TestExecute_NothingCreatedWhenHerdrRefusesTheSpace(t *testing.T) {
	for _, tt := range []struct {
		name     string
		worktree bool
		failAt   string
	}{
		{"worktree create", true, "WorktreeCreate"},
		{"workspace create", false, "WorkspaceCreate"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			in := validInput()
			in.UseWorktree = tt.worktree
			ops, err := Build(in)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			m := &mockRunner{failAt: tt.failAt, failErr: refusedUnchanged("no"), failCount: 1}

			result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

			if result.FailedIndex != 0 {
				t.Fatalf("FailedIndex = %d, want 0", result.FailedIndex)
			}
			if !result.NothingCreated {
				t.Error("NothingCreated = false, want true: herdr refused the first op before making anything")
			}
		})
	}
}

// TestExecute_NothingCreatedNeedsTheEvidence: a failed first op with
// Created still nil is NOT enough. herdr 0.9.0's worktree_open_failed comes
// after `git worktree add` has made the checkout and its branch, and
// herdrc leaves such a failure unmarked.
func TestExecute_NothingCreatedNeedsTheEvidence(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	m := &mockRunner{failAt: "WorktreeCreate", failErr: errors.New("worktree_open_failed"), failCount: 1}

	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

	if result.FailedIndex != 0 || result.Created != nil {
		t.Fatalf("FailedIndex = %d, Created = %+v; want 0 and nil", result.FailedIndex, result.Created)
	}
	if result.NothingCreated {
		t.Error("NothingCreated = true on a failure that carries no evidence nothing was made")
	}
}

// TestExecute_NothingCreatedWhenTheReuseCheckFails: the list call before a
// worktree create fails the step before the create is ever made, which is
// its own evidence -- whatever the list call's error was.
func TestExecute_NothingCreatedWhenTheReuseCheckFails(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	m := &mockRunner{failAt: "WorkspaceList", failErr: errors.New("exit status 1"), failCount: 1}

	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

	if result.FailedIndex != 0 {
		t.Fatalf("FailedIndex = %d, want 0", result.FailedIndex)
	}
	if !result.NothingCreated {
		t.Error("NothingCreated = false, want true: worktree create was never called")
	}
}

// TestExecute_NotNothingCreatedWhenTheReuseClaimIsRefused: the claim's own
// `tab create` changes nothing when herdr refuses it, and herdrc marks it
// so -- but it runs after worktree create has already made a checkout and
// found its workspace (placement spec §5.2). The claim's evidence is about
// the claim.
func TestExecute_NotNothingCreatedWhenTheReuseClaimIsRefused(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	m := &mockRunner{
		workspacesBeforeCreate: []herdrc.WorkspaceInfo{{WorkspaceID: "w9", Label: "somebody-else"}},
		topo:                   herdrc.CreatedTopology{WorkspaceID: "w9", PaneID: "stranger-pane", CheckoutPath: "/tmp/wt"},
		failAt:                 "TabCreate",
		failErr:                refusedUnchanged("workspace_not_found"),
		failCount:              1,
	}

	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

	if result.FailedIndex != 0 || result.Created == nil {
		t.Fatalf("FailedIndex = %d, Created = %+v; want 0 and the reused space", result.FailedIndex, result.Created)
	}
	if result.NothingCreated {
		t.Error("NothingCreated = true, but worktree create had made a checkout before the claim was refused")
	}
}

// TestExecute_NotNothingCreatedAfterTheFirstStep: a marked failure of a later
// op says nothing about the space the first op made.
func TestExecute_NotNothingCreatedAfterTheFirstStep(t *testing.T) {
	in := validInput()
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	m := &mockRunner{
		topo:      herdrc.CreatedTopology{WorkspaceID: "w4", TabID: "w4:t1", PaneID: "w4:p1"},
		failAt:    "AgentStart",
		failErr:   refusedUnchanged("agent_start_failed"),
		failCount: 1,
	}

	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

	if want := indexOfKind(ops, OpAgentStart); result.FailedIndex != want {
		t.Fatalf("FailedIndex = %d, want %d", result.FailedIndex, want)
	}
	if result.NothingCreated {
		t.Error("NothingCreated = true, but the workspace was created at step 1")
	}
}

// TestExecute_NotNothingCreatedWhenAnOpRanFirst: the evidence speaks for the
// op that was refused, and "nothing exists" needs it to be the FIRST op.
// Build never puts anything ahead of the space, but Execute runs whatever it
// is handed, and an op that ran before the refusal has already acted.
func TestExecute_NotNothingCreatedWhenAnOpRanFirst(t *testing.T) {
	in := validInput()
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	first := Op{Kind: OpAgentStart, Label: "starting agent", Agent: &herdrc.AgentStartReq{Name: "x", Kind: "claude", PaneID: "w1:p1"}}
	ops = append([]Op{first}, ops...)
	m := &mockRunner{failAt: "WorkspaceCreate", failErr: refusedUnchanged("workspace_create_failed"), failCount: 1}

	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

	if result.FailedIndex != 1 {
		t.Fatalf("FailedIndex = %d, want 1", result.FailedIndex)
	}
	if result.NothingCreated {
		t.Error("NothingCreated = true, but an agent was started before the space was refused")
	}
}

// worktreeAttempts answers each WorktreeCreate with the next error in errs.
type worktreeAttempts struct {
	mockRunner
	errs []error
}

func (m *worktreeAttempts) WorktreeCreate(_ context.Context, req herdrc.WorktreeCreateReq) (herdrc.CreatedTopology, error) {
	m.calls = append(m.calls, "WorktreeCreate("+req.Branch+")")
	err := m.errs[0]
	m.errs = m.errs[1:]
	return herdrc.CreatedTopology{}, err
}

// TestExecute_NothingCreatedNeedsEveryAttemptsEvidence: an attempt that
// made a checkout and was then retried leaves a retry whose refusal is
// evidence only about the retry. Found by the independent review, when
// retryBusy read an error's TEXT and a title in --label could buy the retry.
// Since #144 it reads herdr's code, and herdr answers no worktree create with
// agent_pane_busy, so this is unreachable today -- which is why it is pinned
// rather than left to a reader's trust.
func TestExecute_NothingCreatedNeedsEveryAttemptsEvidence(t *testing.T) {
	withBusyRetryOverrides(t, 0, nil)
	in := validInput()
	in.UseWorktree = true
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	m := &worktreeAttempts{errs: []error{
		// herdr made the checkout and its branch, then failed busy.
		herdrErr{msg: `herdr worktree create --label fix: exit status 1`, code: herdrc.ErrPaneBusy},
		refusedUnchanged("worktree_operation_in_progress"),
	}}

	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

	if len(m.errs) != 0 {
		t.Fatalf("WorktreeCreate ran %d time(s), want 2 -- the scenario needs the retry", 2-len(m.errs))
	}
	if result.FailedIndex != 0 {
		t.Fatalf("FailedIndex = %d, want 0", result.FailedIndex)
	}
	if result.NothingCreated {
		t.Error("NothingCreated = true, but the first attempt left a checkout behind")
	}
}
