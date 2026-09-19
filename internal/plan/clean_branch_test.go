package plan

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ZviBaratz/herdr-draft/internal/gitx"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
)

// #173: a cleaned worktree create used to leave its branch behind, so a
// retry with the same title derived the same branch name and was refused.
// These pin the three conditions Clean now deletes that branch under --
// this run made it, it holds nothing the base does not, and the checkout is
// already gone -- and the evidence Execute records for the first one.

// worktreeInput is a worktree plan whose project is repo, cut from main.
func worktreeInput(repo, branch string) Input {
	in := validInput()
	in.UseWorktree = true
	in.ProjectDir = repo
	in.Branch = branch
	in.BaseRef = "main"
	return in
}

// herdrWorktreeAdd does what herdr's `worktree create` does in repo
// (herdr:src/worktree.rs at v0.9.0, run_worktree_add_command): check an
// existing local branch out, or make a new one from the base.
func herdrWorktreeAdd(t *testing.T, repo, path string) func(herdrc.WorktreeCreateReq) {
	return func(req herdrc.WorktreeCreateReq) {
		if exists, err := gitx.LocalBranchExists(context.Background(), repo, req.Branch); err == nil && exists {
			gitIn(t, repo, "worktree", "add", "-q", path, req.Branch)
			return
		}
		gitIn(t, repo, "worktree", "add", "-q", "-b", req.Branch, path, req.Base)
	}
}

func branchExists(t *testing.T, repo, branch string) bool {
	t.Helper()
	ok, err := gitx.LocalBranchExists(context.Background(), repo, branch)
	if err != nil {
		t.Fatalf("LocalBranchExists(%s): %v", branch, err)
	}
	return ok
}

func executeWorktree(t *testing.T, in Input, m *mockRunner) ExecResult {
	t.Helper()
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return Execute(context.Background(), m, ops, ExecOpts{}, nil)
}

// The fake makes the branch the way herdr does, so a check taken AFTER the
// create would find it and call it pre-existing: this is what pins "before".
func TestExecuteClaimsTheBranchItsWorktreeCreateMade(t *testing.T) {
	repo := mkRepo(t)
	path := filepath.Join(t.TempDir(), "wt")
	m := &mockRunner{
		topo:             herdrc.CreatedTopology{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1", CheckoutPath: path},
		onWorktreeCreate: herdrWorktreeAdd(t, repo, path),
	}

	result := executeWorktree(t, worktreeInput(repo, "zvi/new"), m)

	if result.FailedIndex != -1 {
		t.Fatalf("FailedIndex = %d, want -1", result.FailedIndex)
	}
	if result.CreatedBranch != "zvi/new" {
		t.Fatalf("CreatedBranch = %q, want %q -- the branch did not exist until this create made it", result.CreatedBranch, "zvi/new")
	}
}

// herdr checks an existing local branch out rather than refusing it, and
// answers exactly as it does for a new one. The form's and create's own
// refusals are meant to stop this reaching herdr, but neither is a
// guarantee (the form's is a debounced verdict submit does not wait for,
// and both fail open on a git error), so the evidence is taken here.
func TestExecuteDoesNotClaimABranchThatWasAlreadyThere(t *testing.T) {
	repo := mkRepo(t)
	gitIn(t, repo, "branch", "zvi/old")
	path := filepath.Join(t.TempDir(), "wt")
	m := &mockRunner{
		topo:             herdrc.CreatedTopology{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1", CheckoutPath: path},
		onWorktreeCreate: herdrWorktreeAdd(t, repo, path),
	}

	result := executeWorktree(t, worktreeInput(repo, "zvi/old"), m)

	if result.FailedIndex != -1 {
		t.Fatalf("FailedIndex = %d, want -1", result.FailedIndex)
	}
	if result.CreatedBranch != "" {
		t.Fatalf("CreatedBranch = %q, want empty -- the user made this branch, not this create", result.CreatedBranch)
	}
}

// Unreadable is not "absent". It is not a failure either: the evidence
// only decides whether a later clean may delete the branch, so a create
// that cannot gather it goes ahead and simply cannot claim the branch.
func TestExecuteDoesNotClaimABranchItCouldNotCheck(t *testing.T) {
	for _, tc := range []struct {
		name, dir string
	}{
		{"not a repository", t.TempDir()},
		// "" would run git in the test's own working directory -- this
		// repository -- which is the #186 hazard, and an answer about the
		// wrong repository in production.
		{"no directory", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &mockRunner{topo: herdrc.CreatedTopology{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"}}

			result := executeWorktree(t, worktreeInput(tc.dir, "zvi/new"), m)

			if result.FailedIndex != -1 {
				t.Fatalf("FailedIndex = %d, want -1 -- an unreadable branch is no reason to fail the create", result.FailedIndex)
			}
			if result.CreatedBranch != "" {
				t.Fatalf("CreatedBranch = %q, want empty -- nothing showed the branch was absent", result.CreatedBranch)
			}
		})
	}
}

// The fake's remove really removes the checkout, as herdr's does, so a
// Clean that deleted the branch FIRST would be refused by git: the branch
// would still be checked out. That is what pins the order.
func TestCleanDeletesTheBranchItsCreateMade(t *testing.T) {
	repo := mkRepo(t)
	wt := mkWorktree(t, repo, "zvi/new")
	m := &mockRunner{onWorktreeRemove: func(string) { gitIn(t, repo, "worktree", "remove", wt) }}
	created := herdrc.CreatedTopology{WorkspaceID: "ws-1", CheckoutPath: wt}
	result := ExecResult{Created: &created, AgentAt: &created, CreatedBranch: "zvi/new"}

	if err := Clean(context.Background(), m, worktreeInput(repo, "zvi/new"), result); err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if branchExists(t, repo, "zvi/new") {
		t.Fatal("the branch this create made survived the clean, so a retry with the same title is refused (#173)")
	}
	if want := []string{"WorktreeRemove(ws-1)"}; !reflect.DeepEqual(m.calls, want) {
		t.Fatalf("calls = %v, want %v", m.calls, want)
	}
}

// Input.Branch is the name the plan ASKED for; only ExecResult.CreatedBranch
// says this run made it.
func TestCleanKeepsABranchItHasNoEvidenceFor(t *testing.T) {
	repo := mkRepo(t)
	wt := mkWorktree(t, repo, "zvi/old")
	m := &mockRunner{onWorktreeRemove: func(string) { gitIn(t, repo, "worktree", "remove", wt) }}
	created := herdrc.CreatedTopology{WorkspaceID: "ws-1", CheckoutPath: wt}
	result := ExecResult{Created: &created, AgentAt: &created}

	if err := Clean(context.Background(), m, worktreeInput(repo, "zvi/old"), result); err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if !branchExists(t, repo, "zvi/old") {
		t.Fatal("Clean deleted a branch nothing shows this create made")
	}
}

// CleanCheck's verdict can be minutes old by the time `c` is pressed. A
// commit made since then lives only on the branch once the checkout is
// gone, so the branch is checked again before ANYTHING is removed -- and a
// clean that would have to keep it removes nothing at all.
func TestCleanRemovesNothingWhenTheBranchHasGainedCommits(t *testing.T) {
	repo := mkRepo(t)
	wt := mkWorktree(t, repo, "zvi/new")
	gitIn(t, wt, "commit", "-q", "--allow-empty", "-m", "made after the clean check")
	m := &mockRunner{onWorktreeRemove: func(string) { gitIn(t, repo, "worktree", "remove", wt) }}
	created := herdrc.CreatedTopology{WorkspaceID: "ws-1", CheckoutPath: wt}
	result := ExecResult{Created: &created, AgentAt: &created, CreatedBranch: "zvi/new"}

	err := Clean(context.Background(), m, worktreeInput(repo, "zvi/new"), result)
	if err == nil {
		t.Fatal("Clean removed a worktree whose branch holds a commit its base does not")
	}
	for _, want := range []string{"zvi/new", "1 commit", "nothing was removed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to say %q", err, want)
		}
	}
	if len(m.calls) != 0 {
		t.Errorf("calls = %v, want none -- the refusal has to come before anything is removed", m.calls)
	}
	if !branchExists(t, repo, "zvi/new") {
		t.Error("the branch holding the commit was deleted")
	}
}

// Fails closed, as CleanCheck does: a branch that cannot be judged is not
// one to delete, and deleting the checkout alone would leave the clean half
// done with no way to say which half.
func TestCleanRemovesNothingWhenItCannotJudgeTheBranch(t *testing.T) {
	notRepo := t.TempDir()
	m := &mockRunner{}
	created := herdrc.CreatedTopology{WorkspaceID: "ws-1", CheckoutPath: filepath.Join(notRepo, "wt")}
	result := ExecResult{Created: &created, AgentAt: &created, CreatedBranch: "zvi/new"}

	if err := Clean(context.Background(), m, worktreeInput(notRepo, "zvi/new"), result); err == nil {
		t.Fatal("Clean went ahead without being able to read the branch it was about to delete")
	}
	if len(m.calls) != 0 {
		t.Errorf("calls = %v, want none", m.calls)
	}
}

// A branch that is already gone is nothing to delete, not a reason to
// refuse removing the worktree.
func TestCleanToleratesABranchAlreadyGone(t *testing.T) {
	repo := mkRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	gitIn(t, repo, "worktree", "add", "-q", "--detach", wt)
	m := &mockRunner{onWorktreeRemove: func(string) { gitIn(t, repo, "worktree", "remove", wt) }}
	created := herdrc.CreatedTopology{WorkspaceID: "ws-1", CheckoutPath: wt}
	result := ExecResult{Created: &created, AgentAt: &created, CreatedBranch: "zvi/gone"}

	if err := Clean(context.Background(), m, worktreeInput(repo, "zvi/gone"), result); err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if want := []string{"WorktreeRemove(ws-1)"}; !reflect.DeepEqual(m.calls, want) {
		t.Fatalf("calls = %v, want %v", m.calls, want)
	}
}

// If the delete itself fails once the checkout is gone, the clean is half
// done, and the error has to say which half and what to run.
func TestCleanNamesTheBranchItCouldNotDelete(t *testing.T) {
	repo := mkRepo(t)
	wt := mkWorktree(t, repo, "zvi/new")
	// A remove that leaves the checkout in place: git then refuses to
	// delete a branch that is still checked out.
	m := &mockRunner{}
	created := herdrc.CreatedTopology{WorkspaceID: "ws-1", CheckoutPath: wt}
	result := ExecResult{Created: &created, AgentAt: &created, CreatedBranch: "zvi/new"}

	err := Clean(context.Background(), m, worktreeInput(repo, "zvi/new"), result)
	if err == nil {
		t.Fatal("Clean reported success with the branch still there")
	}
	if !strings.Contains(err.Error(), "git branch -D zvi/new") {
		t.Errorf("error = %q, want the command that finishes the job", err)
	}
	if _, statErr := os.Stat(wt); statErr != nil {
		t.Fatalf("fixture: the checkout should still exist: %v", statErr)
	}
}

// CleanCheck says what an allowed clean will do with the branch, from the
// same evidence Clean acts on, so the popup line and create's report
// cannot promise something Clean then does not do.
func TestCleanCheckSaysWhatHappensToTheBranch(t *testing.T) {
	repo := mkRepo(t)
	wt := mkWorktree(t, repo, "zvi/new")
	created := herdrc.CreatedTopology{WorkspaceID: "ws-1", CheckoutPath: wt}

	for _, tc := range []struct {
		name    string
		in      Input
		created string
		want    BranchFate
	}{
		{"made by this run", worktreeInput(repo, "zvi/new"), "zvi/new", BranchDeleted},
		{"no evidence", worktreeInput(repo, "zvi/new"), "", BranchKept},
		{"no worktree", validInput(), "", NoBranch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := ExecResult{Created: &created, AgentAt: &created, CreatedBranch: tc.created}
			decision := CleanCheck(context.Background(), tc.in, result)
			if !decision.Allowed {
				t.Fatalf("fixture: clean refused: %s", decision.Reason)
			}
			if decision.Branch != tc.want {
				t.Fatalf("Branch = %v, want %v", decision.Branch, tc.want)
			}
		})
	}

	// A refused clean does nothing to anything, the branch included.
	gitIn(t, wt, "commit", "-q", "--allow-empty", "-m", "work")
	result := ExecResult{Created: &created, AgentAt: &created, CreatedBranch: "zvi/new"}
	if decision := CleanCheck(context.Background(), worktreeInput(repo, "zvi/new"), result); decision.Allowed || decision.Branch != NoBranch {
		t.Fatalf("decision = %+v, want refused with Branch = NoBranch", decision)
	}
}
