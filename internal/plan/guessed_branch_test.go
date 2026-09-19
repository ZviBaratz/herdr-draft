package plan

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
)

// #198: herdr hands the base to `git worktree add -b <branch> <path>
// <base>`, and git 2.53 given a base that names a branch only a remote has,
// by its bare name, checks out a new local branch named after the base
// instead -- develop, tracking origin/develop -- and never makes the one
// asked for. herdr reports success, and its reply names the branch git
// checked out. The base list no longer offers such a name, but the reply
// is the only evidence of what the worktree is actually on, so Execute
// holds it to the request.

// guessedClone is a repository whose develop only origin has, as in a
// fresh clone, and the fake's worktree create does to it what git 2.53
// did in the issue's measurement: the new local branch is develop, cut
// from origin/develop. It is made explicitly rather than left to git's
// guess, so the test does not depend on which git runs it.
func guessedClone(t *testing.T) (repo, path string, m *mockRunner) {
	t.Helper()
	upstream := mkRepo(t)
	gitIn(t, upstream, "branch", "develop")
	repo = filepath.Join(t.TempDir(), "clone")
	gitIn(t, upstream, "clone", "-q", upstream, repo)
	path = filepath.Join(t.TempDir(), "smoke-remote-only")
	m = &mockRunner{
		topo: createdTopo(path, "develop"),
		onWorktreeCreate: func(herdrc.WorktreeCreateReq) {
			gitIn(t, repo, "worktree", "add", "-q", "-b", "develop", path, "origin/develop")
		},
		onWorktreeRemove: func(string) { gitIn(t, repo, "worktree", "remove", path) },
	}
	return repo, path, m
}

func TestExecuteFailsAWorktreeOnABranchItDidNotAskFor(t *testing.T) {
	repo, path, m := guessedClone(t)
	in := worktreeInput(repo, "smoke/remote-only")
	in.BaseRef = "develop"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	var failed *Progress
	result := Execute(context.Background(), m, ops, ExecOpts{}, func(p Progress) {
		if p.State == StepFailed {
			failed = &p
		}
	})

	wt := indexOfKind(ops, OpWorktreeCreate)
	if result.FailedIndex != wt {
		t.Fatalf("FailedIndex = %d, want the worktree step, %d -- the worktree is on develop, not smoke/remote-only", result.FailedIndex, wt)
	}
	if failed == nil || failed.Err == nil {
		t.Fatal("no StepFailed progress with an error")
	}
	for _, name := range []string{`"develop"`, `"smoke/remote-only"`} {
		if !strings.Contains(failed.Err.Error(), name) {
			t.Errorf("error = %q, want it to name %s", failed.Err, name)
		}
	}
	for _, c := range m.calls {
		if strings.HasPrefix(c, "AgentStart(") || strings.HasPrefix(c, "TabRename(") {
			t.Errorf("calls = %v: the plan went on past a worktree on the wrong branch", m.calls)
		}
	}
	// herdr made a checkout, so the failure has one to keep or clean.
	if result.Created == nil || result.Created.CheckoutPath != path {
		t.Fatalf("Created = %+v, want the checkout at %s, for the keep-or-clean gate", result.Created, path)
	}
	if result.NothingCreated {
		t.Error("NothingCreated = true, but herdr made a checkout")
	}
	if result.CreatedBranch != "" {
		t.Errorf("CreatedBranch = %q, want empty -- the request was not for develop", result.CreatedBranch)
	}
}

// The clean removes the checkout and keeps develop: nothing shows this run
// asked for it, which is the rule for every branch it did not claim.
func TestCleanKeepsABranchTheCreateDidNotAskFor(t *testing.T) {
	repo, path, m := guessedClone(t)
	in := worktreeInput(repo, "smoke/remote-only")
	in.BaseRef = "develop"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

	d := CleanCheck(context.Background(), in, result)
	if !d.Allowed || d.Branch != BranchKept {
		t.Fatalf("CleanCheck = %+v, want allowed, keeping the branch", d)
	}
	outcome, err := Clean(context.Background(), m, in, result)
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if outcome.KeptBranch != "develop" || outcome.DeletedBranch != "" {
		t.Errorf("CleanOutcome = %+v, want develop kept and nothing deleted", outcome)
	}
	if !branchExists(t, repo, "develop") {
		t.Error("develop was deleted")
	}
	if !slices.Contains(m.calls, "WorktreeRemove(ws-1)") {
		t.Errorf("calls = %v, want the checkout at %s removed", m.calls, path)
	}
}

// herdr omits worktree.branch for a detached checkout, and a request that
// named no branch gets one herdr invented. Neither is a reply that
// contradicts the request, and a create does not fail on a field herdr did
// not send or a name nobody asked for.
func TestExecuteAcceptsABranchItCannotHoldToTheRequest(t *testing.T) {
	for _, tc := range []struct {
		name, asked, answered string
	}{
		{"no branch in the reply", "zvi/new", ""},
		{"no branch asked for", "", "brave-river-1234"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := validInput()
			in.UseWorktree = true
			in.Branch = tc.asked
			m := &mockRunner{topo: createdTopo("/tmp/wt", tc.answered)}

			result := executeWorktree(t, in, m)

			if result.FailedIndex != -1 {
				t.Fatalf("FailedIndex = %d, want -1", result.FailedIndex)
			}
		})
	}
}

// A reused space is refused a clean outright, because it is a workspace the
// user opened (placement spec §5.2). That verdict has to survive this
// failure too, and the session gets no tab claimed in the user's workspace
// for an agent that will not be started.
func TestExecuteKeepsTheReuseVerdictOnABranchItDidNotAskFor(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	m := &mockRunner{
		workspacesBeforeCreate: []herdrc.WorkspaceInfo{{WorkspaceID: "ws-1", Label: "somebody-else"}},
		topo:                   createdTopo("/tmp/wt", "develop"),
	}

	result := executeWorktree(t, in, m)

	if result.FailedIndex == -1 {
		t.Fatal("FailedIndex = -1, want the worktree step failed")
	}
	if !result.SpaceReused || result.SpaceLabel != "somebody-else" {
		t.Errorf("SpaceReused, SpaceLabel = %v, %q, want true, somebody-else", result.SpaceReused, result.SpaceLabel)
	}
	if d := CleanCheck(context.Background(), in, result); d.Allowed {
		t.Errorf("CleanCheck allowed a clean of a workspace the user opened: %+v", d)
	}
	for _, c := range m.calls {
		if strings.HasPrefix(c, "TabCreate(") {
			t.Errorf("calls = %v: claimed a tab in the user's workspace for a session that failed", m.calls)
		}
	}
}

// The rule in one place: the request as herdr reads it, trimmed, against
// the branch its reply names, and nothing held against a name nobody asked
// for or a field herdr did not send.
func TestWrongBranch(t *testing.T) {
	for _, tc := range []struct {
		name, asked, got string
		fails            bool
	}{
		{name: "the branch asked for", asked: "zvi/x", got: "zvi/x"},
		{name: "herdr's trim", asked: "zvi/x ", got: "zvi/x"},
		{name: "none asked for", asked: "", got: "brave-river-1234"},
		{name: "none in the reply", asked: "zvi/x", got: ""},
		{name: "another branch", asked: "zvi/x", got: "develop", fails: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := wrongBranch(tc.asked, tc.got)
			if !tc.fails {
				if err != nil {
					t.Fatalf("wrongBranch = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("wrongBranch = nil, want an error")
			}
			for _, name := range []string{`"develop"`, `"zvi/x"`} {
				if !strings.Contains(err.Error(), name) {
					t.Errorf("wrongBranch = %q, want it to name %s", err, name)
				}
			}
		})
	}
}
