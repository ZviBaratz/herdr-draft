package plan

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ZviBaratz/herdr-draft/internal/gitx"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
)

// #173: a cleaned worktree create used to leave its branch behind, so a
// retry with the same title derived the same branch name and was refused.
// These pin the conditions Clean now deletes that branch under -- this run
// made it, it holds nothing the base does not before anything is removed,
// and nothing else holds its commits once the checkout is gone -- and the
// evidence Execute records for them.

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
// (herdr:src/worktree.rs at v0.9.0, run_worktree_add_command, after
// start_api_worktree_create's trim): check an existing local branch out, or
// make a new one from the base.
func herdrWorktreeAdd(t *testing.T, repo, path string) func(herdrc.WorktreeCreateReq) {
	return func(req herdrc.WorktreeCreateReq) {
		branch := strings.TrimSpace(req.Branch)
		if exists, err := gitx.LocalBranchExists(context.Background(), repo, branch); err == nil && exists {
			gitIn(t, repo, "worktree", "add", "-q", path, branch)
			return
		}
		base := req.Base
		if base == "" {
			base = "HEAD" // herdr's own default
		}
		gitIn(t, repo, "worktree", "add", "-q", "-b", branch, path, base)
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

func revOf(t *testing.T, dir, rev string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", rev)
	cmd.Dir = dir
	cmd.Env = gitTestEnv()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse %s: %v", rev, err)
	}
	return strings.TrimSpace(string(out))
}

func executeWorktree(t *testing.T, in Input, m *mockRunner) ExecResult {
	t.Helper()
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return Execute(context.Background(), m, ops, ExecOpts{}, nil)
}

// createdTopo is what herdr answers a worktree create with, naming the
// branch it checked out.
func createdTopo(path, branch string) herdrc.CreatedTopology {
	return herdrc.CreatedTopology{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1", CheckoutPath: path, Branch: branch}
}

// The fake makes the branch the way herdr does, so a check taken AFTER the
// create would find it and call it pre-existing: this is what pins "before".
func TestExecuteClaimsTheBranchItsWorktreeCreateMade(t *testing.T) {
	repo := mkRepo(t)
	path := filepath.Join(t.TempDir(), "wt")
	m := &mockRunner{topo: createdTopo(path, "zvi/new"), onWorktreeCreate: herdrWorktreeAdd(t, repo, path)}

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
	m := &mockRunner{topo: createdTopo(path, "zvi/old"), onWorktreeCreate: herdrWorktreeAdd(t, repo, path)}

	result := executeWorktree(t, worktreeInput(repo, "zvi/old"), m)

	if result.FailedIndex != -1 {
		t.Fatalf("FailedIndex = %d, want -1", result.FailedIndex)
	}
	if result.CreatedBranch != "" {
		t.Fatalf("CreatedBranch = %q, want empty -- the user made this branch, not this create", result.CreatedBranch)
	}
}

// A name git cannot hold -- here, trailing whitespace -- reads as absent,
// because `git show-ref` answers "no such ref" for it rather than failing.
// herdr trims it and checks the user's own branch out. The evidence has to
// be about the branch herdr says it used, not the one that was asked for.
func TestExecuteClaimsOnlyTheBranchHerdrSaysItUsed(t *testing.T) {
	repo := mkRepo(t)
	gitIn(t, repo, "branch", "zvi/old")
	path := filepath.Join(t.TempDir(), "wt")
	m := &mockRunner{topo: createdTopo(path, "zvi/old"), onWorktreeCreate: herdrWorktreeAdd(t, repo, path)}

	result := executeWorktree(t, worktreeInput(repo, "zvi/old "), m)

	if result.FailedIndex != -1 {
		t.Fatalf("FailedIndex = %d, want -1", result.FailedIndex)
	}
	if result.CreatedBranch != "" {
		t.Fatalf("CreatedBranch = %q, want empty -- herdr checked out the user's zvi/old", result.CreatedBranch)
	}
}

// The base the clean counts from is recorded as the create uses it: the
// commit it names in the directory herdr cuts the worktree in, at that
// moment. An unset base is herdr's HEAD.
func TestExecuteRecordsTheCommitItCutFrom(t *testing.T) {
	repo := mkRepo(t)
	path := filepath.Join(t.TempDir(), "wt")
	m := &mockRunner{topo: createdTopo(path, "zvi/new"), onWorktreeCreate: herdrWorktreeAdd(t, repo, path)}
	in := worktreeInput(repo, "zvi/new")
	in.BaseRef = ""

	result := executeWorktree(t, in, m)

	if want := revOf(t, repo, "HEAD"); result.BaseCommit != want {
		t.Fatalf("BaseCommit = %q, want HEAD at create time, %q", result.BaseCommit, want)
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
			m := &mockRunner{topo: createdTopo("", "zvi/new")}

			result := executeWorktree(t, worktreeInput(tc.dir, "zvi/new"), m)

			if result.FailedIndex != -1 {
				t.Fatalf("FailedIndex = %d, want -1 -- an unreadable branch is no reason to fail the create", result.FailedIndex)
			}
			if result.CreatedBranch != "" || result.BaseCommit != "" {
				t.Fatalf("CreatedBranch, BaseCommit = %q, %q, want both empty -- nothing was read", result.CreatedBranch, result.BaseCommit)
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
	tip := revOf(t, repo, "refs/heads/zvi/new")
	m := &mockRunner{onWorktreeRemove: func(string) { gitIn(t, repo, "worktree", "remove", wt) }}
	created := createdTopo(wt, "zvi/new")
	result := ExecResult{Created: &created, AgentAt: &created, CreatedBranch: "zvi/new"}

	out, err := Clean(context.Background(), m, worktreeInput(repo, "zvi/new"), result)
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if branchExists(t, repo, "zvi/new") {
		t.Fatal("the branch this create made survived the clean, so a retry with the same title is refused (#173)")
	}
	if want := (CleanOutcome{DeletedBranch: "zvi/new", DeletedTip: tip}); out != want {
		t.Fatalf("outcome = %+v, want %+v -- the tip is the way back", out, want)
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
	created := createdTopo(wt, "zvi/old")
	result := ExecResult{Created: &created, AgentAt: &created}

	out, err := Clean(context.Background(), m, worktreeInput(repo, "zvi/old"), result)
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if !branchExists(t, repo, "zvi/old") {
		t.Fatal("Clean deleted a branch nothing shows this create made")
	}
	if out.KeptBranch != "zvi/old" || out.KeptReason == "" || out.DeletedBranch != "" {
		t.Fatalf("outcome = %+v, want zvi/old kept, with a reason", out)
	}
}

// With no --branch, herdr invents the name, and only its reply knows it. A
// kept branch is named by that reply, not by the empty request.
func TestCleanNamesTheBranchHerdrInvented(t *testing.T) {
	repo := mkRepo(t)
	wt := mkWorktree(t, repo, "worktree/brave-river-0000")
	m := &mockRunner{onWorktreeRemove: func(string) { gitIn(t, repo, "worktree", "remove", wt) }}
	created := createdTopo(wt, "worktree/brave-river-0000")
	result := ExecResult{Created: &created, AgentAt: &created}

	out, err := Clean(context.Background(), m, worktreeInput(repo, ""), result)
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if out.KeptBranch != "worktree/brave-river-0000" {
		t.Fatalf("KeptBranch = %q, want the name herdr gave it", out.KeptBranch)
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
	created := createdTopo(wt, "zvi/new")
	result := ExecResult{Created: &created, AgentAt: &created, CreatedBranch: "zvi/new"}

	_, err := Clean(context.Background(), m, worktreeInput(repo, "zvi/new"), result)
	var refusal *CleanRefusal
	if !errors.As(err, &refusal) {
		t.Fatalf("Clean error = %v, want a *CleanRefusal -- a refusal, not a failure: nothing was touched", err)
	}
	for _, want := range []string{"zvi/new", "1 commit"} {
		if !strings.Contains(refusal.Reason, want) {
			t.Errorf("Reason = %q, want it to say %q", refusal.Reason, want)
		}
	}
	if !strings.Contains(err.Error(), "nothing was removed") {
		t.Errorf("error = %q, want it to say nothing was removed", err)
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
	created := createdTopo(filepath.Join(notRepo, "wt"), "zvi/new")
	result := ExecResult{Created: &created, AgentAt: &created, CreatedBranch: "zvi/new"}

	_, err := Clean(context.Background(), m, worktreeInput(notRepo, "zvi/new"), result)
	var refusal *CleanRefusal
	if !errors.As(err, &refusal) {
		t.Fatalf("Clean error = %v, want a *CleanRefusal -- it went ahead without being able to read the branch", err)
	}
	if len(m.calls) != 0 {
		t.Errorf("calls = %v, want none", m.calls)
	}
}

// The race the check before the removal cannot close (#190's review): the
// agent is still running while herdr removes its checkout -- herdr stops a
// workspace's panes first only for a forced remove (herdr:src/app/worktrees.rs
// at v0.9.0, should_shutdown_workspace_terminal_runtimes_for_worktree_remove)
// -- so a commit can land after the check and before the delete. Once the
// checkout is gone nothing can commit to the branch, so it is asked again
// there, and a branch holding a commit nothing else holds is kept: the
// checkout is gone, the work is not.
func TestCleanKeepsABranchThatGainedACommitDuringTheRemoval(t *testing.T) {
	repo := mkRepo(t)
	wt := mkWorktree(t, repo, "zvi/new")
	m := &mockRunner{onWorktreeRemove: func(string) {
		gitIn(t, wt, "commit", "-q", "--allow-empty", "-m", "the agent's, mid-removal")
		gitIn(t, repo, "worktree", "remove", wt)
	}}
	created := createdTopo(wt, "zvi/new")
	result := ExecResult{Created: &created, AgentAt: &created, CreatedBranch: "zvi/new"}

	out, err := Clean(context.Background(), m, worktreeInput(repo, "zvi/new"), result)
	if err != nil {
		t.Fatalf("Clean: %v -- the checkout is gone, so this is a clean with something to report, not a failure", err)
	}
	if !branchExists(t, repo, "zvi/new") {
		t.Fatal("the branch was deleted with a commit nothing else holds")
	}
	if out.KeptBranch != "zvi/new" || !strings.Contains(out.KeptReason, "1 commit") || out.DeletedBranch != "" {
		t.Fatalf("outcome = %+v, want zvi/new kept, naming the commit it holds", out)
	}
}

// If the delete itself fails once the checkout is gone, the clean is half
// done: the outcome says the branch was kept, and what finishes the job.
func TestCleanNamesTheBranchItCouldNotDelete(t *testing.T) {
	repo := mkRepo(t)
	wt := mkWorktree(t, repo, "zvi/new")
	// A remove that leaves the checkout in place: git then refuses to
	// delete a branch that is still checked out.
	m := &mockRunner{}
	created := createdTopo(wt, "zvi/new")
	result := ExecResult{Created: &created, AgentAt: &created, CreatedBranch: "zvi/new"}

	out, err := Clean(context.Background(), m, worktreeInput(repo, "zvi/new"), result)
	if err != nil {
		t.Fatalf("Clean: %v -- a failed delete after the removal is a kept branch, not a failed clean", err)
	}
	if out.KeptBranch != "zvi/new" || !strings.Contains(out.KeptReason, "git branch -D zvi/new") {
		t.Errorf("outcome = %+v, want zvi/new kept, with the command that finishes the job", out)
	}
	if _, statErr := os.Stat(wt); statErr != nil {
		t.Fatalf("fixture: the checkout should still exist: %v", statErr)
	}
}

// A branch that is already gone is nothing to delete, not a reason to
// refuse removing the worktree, and its name is free for a retry.
func TestCleanToleratesABranchAlreadyGone(t *testing.T) {
	repo := mkRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	gitIn(t, repo, "worktree", "add", "-q", "--detach", wt)
	m := &mockRunner{onWorktreeRemove: func(string) { gitIn(t, repo, "worktree", "remove", wt) }}
	created := createdTopo(wt, "zvi/gone")
	result := ExecResult{Created: &created, AgentAt: &created, CreatedBranch: "zvi/gone"}

	out, err := Clean(context.Background(), m, worktreeInput(repo, "zvi/gone"), result)
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if want := []string{"WorktreeRemove(ws-1)"}; !reflect.DeepEqual(m.calls, want) {
		t.Fatalf("calls = %v, want %v", m.calls, want)
	}
	if out.DeletedBranch != "zvi/gone" || out.DeletedTip != "" {
		t.Fatalf("outcome = %+v, want the name reported free, with no tip since nothing was deleted", out)
	}
}

// The check before the removal counts from the base Execute recorded, not
// from whatever an unset base resolves to now: the popup's failure screen
// can stay up for minutes, and the primary checkout can move in between.
// Counted from its new HEAD, a branch with nothing of its own read as
// "1 commit(s) not on <sha>" and was refused (#190's review).
func TestCleanCountsFromTheBaseRecordedAtCreate(t *testing.T) {
	repo := mkRepo(t)
	gitIn(t, repo, "branch", "older")
	gitIn(t, repo, "commit", "-q", "--allow-empty", "-m", "newer")
	base := revOf(t, repo, "HEAD")
	wt := mkWorktree(t, repo, "zvi/new")
	gitIn(t, repo, "checkout", "-q", "older") // the user, while the failure screen is up
	m := &mockRunner{onWorktreeRemove: func(string) { gitIn(t, repo, "worktree", "remove", wt) }}
	in := worktreeInput(repo, "zvi/new")
	in.BaseRef = ""
	created := createdTopo(wt, "zvi/new")
	result := ExecResult{Created: &created, AgentAt: &created, CreatedBranch: "zvi/new", BaseCommit: base}

	if decision := CleanCheck(context.Background(), in, result); !decision.Allowed {
		t.Fatalf("CleanCheck refused a pristine worktree: %s", decision.Reason)
	}
	if _, err := Clean(context.Background(), m, in, result); err != nil {
		t.Fatalf("Clean: %v -- the branch holds nothing its recorded base does not", err)
	}
	if branchExists(t, repo, "zvi/new") {
		t.Fatal("the branch survived the clean")
	}
}

// From a linked checkout (#171) the worktree is created from the primary
// checkout, and that is where its branch is looked for and deleted: the
// lane itself may be gone by the time the clean runs.
func TestCleanActsWhereTheWorktreeWasCreatedFrom(t *testing.T) {
	repo := mkRepo(t)
	base := revOf(t, repo, "HEAD")
	wt := mkWorktree(t, repo, "zvi/new")
	m := &mockRunner{onWorktreeRemove: func(string) { gitIn(t, repo, "worktree", "remove", wt) }}
	in := worktreeInput(filepath.Join(t.TempDir(), "lane-already-gone"), "zvi/new")
	in.Linked = LinkedCheckout{RepoRoot: repo, Commit: base}
	created := createdTopo(wt, "zvi/new")
	result := ExecResult{Created: &created, AgentAt: &created, CreatedBranch: "zvi/new", BaseCommit: base}

	out, err := Clean(context.Background(), m, in, result)
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if branchExists(t, repo, "zvi/new") || out.DeletedBranch != "zvi/new" {
		t.Fatalf("outcome = %+v, want the branch deleted from the primary checkout", out)
	}
}

// CleanCheck says what an allowed clean will do with the branch, from the
// same evidence Clean acts on, so the popup line and create's report
// cannot promise something Clean then does not do.
func TestCleanCheckSaysWhatHappensToTheBranch(t *testing.T) {
	repo := mkRepo(t)
	wt := mkWorktree(t, repo, "zvi/new")
	created := createdTopo(wt, "zvi/new")

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

// gitMessage keeps what git said and drops what gitx wraps it in for a
// log: the command, the directory and the exit status, and git's hints on
// the lines after.
func TestGitMessage(t *testing.T) {
	for _, tc := range []struct{ err, want string }{
		{
			"delete branch zvi/x: git branch -D -- zvi/x (in /repo): exit status 1: error: cannot delete branch 'zvi/x' used by worktree at '/wt'\nhint: whatever",
			"error: cannot delete branch 'zvi/x' used by worktree at '/wt'",
		},
		{"no exit status in this one", "no exit status in this one"},
	} {
		if got := gitMessage(errors.New(tc.err)); got != tc.want {
			t.Errorf("gitMessage(%q) = %q, want %q", tc.err, got, tc.want)
		}
	}
}
