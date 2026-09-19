package app

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/form"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
	"github.com/ZviBaratz/herdr-draft/internal/plan"
)

// These pin #171 on the popup's side: a form opened from a linked worktree's
// own space -- a lane -- or pointed at one in the project row. herdr refuses
// a linked checkout as a worktree source, so the worktree is created from the
// repository root, cut from the commit its base names in the lane, as
// `create` does.

const (
	laneDir  = "/home/zvi/wt/thing-lane"
	laneRoot = "/home/zvi/projects/thing"
	// laneHead and laneMain are the commits HEAD and main name in the lane.
	laneHead = "3f2a9c1e5b7d4a608e1f2b3c4d5e6f708192a3b4"
	laneMain = "9b8a7c6d5e4f3a2b1c0d9e8f7a6b5c4d3e2f1a0b"
)

// laneContext is the plugin context herdr hands a popup opened in the lane's
// space: the space's own checkout, and the repository behind it.
func laneContext() herdrc.Context {
	return herdrc.Context{
		WorkspaceID: "wL", WorkspaceCwd: laneDir, TabID: "tL", FocusedPaneID: "pL",
		Worktree: &herdrc.ContextWorktree{
			RepoKey: laneRoot + "/.git", RepoName: "thing",
			RepoRoot: laneRoot, CheckoutPath: laneDir, IsLinkedWorktree: true,
		},
	}
}

// laneGit is a repository in which laneDir is a linked checkout of laneRoot,
// on branch lane/one at laneHead.
func laneGit() *fakeGit {
	g := newFakeGit()
	g.repoRoots = map[string]string{laneDir: laneRoot}
	g.primary = map[string]string{laneDir: laneRoot}
	g.commits = map[string]string{laneDir + " HEAD": laneHead, laneDir + " main": laneMain}
	g.currentBranchResult = "lane/one"
	g.listBranchesResult = []string{"lane/one", "main"}
	return g
}

// laneModel is a settled form opened from the lane, with a title typed and
// the worktree set as asked.
func laneModel(t *testing.T, runner herdrc.Runner, git *fakeGit, worktree bool) Model {
	t.Helper()
	m := newSubmitTestModel(t, runner, testSetup{Git: git, Ctx: laneContext()})
	m = pumpAsync(t, m, m.initCmds)
	m.title.SetTitle("Fix pagination", false)
	m.worktree.SetOn(worktree)
	m.worktree.SetBranch("zvi/fix-pagination", false)
	return m
}

// TestDefaultProjectDir_ALaneOpensOnItsOwnCheckout is the owner's call on
// #171 for the popup: opened from a lane it defaults to the lane, which is
// what `create` run there defaults to, so the two paths agree. herdr's
// repo_root for that space names the PRIMARY checkout.
func TestDefaultProjectDir_ALaneOpensOnItsOwnCheckout(t *testing.T) {
	primary := &herdrc.ContextWorktree{RepoRoot: laneRoot, CheckoutPath: laneRoot}
	for _, tc := range []struct {
		name string
		ctx  herdrc.Context
		want string
	}{
		{"a lane's space", laneContext(), laneDir},
		{"the repository's own space", herdrc.Context{WorkspaceCwd: laneRoot + "/sub", Worktree: primary}, laneRoot},
		{"a space with no repository", herdrc.Context{WorkspaceCwd: "/tmp/scratch"}, "/tmp/scratch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := defaultProjectDir(tc.ctx); got != tc.want {
				t.Errorf("defaultProjectDir = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDirCandidates_ALaneOffersTheRepositoryNext: the primary checkout the
// popup used to open on stays one keystroke away.
func TestDirCandidates_ALaneOffersTheRepositoryNext(t *testing.T) {
	got := buildDirCandidates(laneContext(), nil, nil)
	if len(got) < 2 || !reflect.DeepEqual(got[:2], []string{laneDir, laneRoot}) {
		t.Errorf("candidates = %v, want the lane then the repository root first", got)
	}
}

// TestSubmit_FromALaneTheWorktreeIsCutFromItsCommitInTheRepoRoot is the
// popup's half of #171, through the real submit: the commit is read at
// submit, not when the form opened, because a lane's agent may commit while
// the popup is up and "HEAD" means the commit it is on now.
func TestSubmit_FromALaneTheWorktreeIsCutFromItsCommitInTheRepoRoot(t *testing.T) {
	runner := &submitFakeRunner{topo: herdrc.CreatedTopology{WorkspaceID: "ws-1", TabID: "t-1", PaneID: "pane-1"}}
	git := laneGit()
	m := laneModel(t, runner, git, true)
	if got := m.dir.Value(); got != laneDir {
		t.Fatalf("project = %q, want the lane", got)
	}
	if len(git.resolveCalls) != 0 {
		t.Fatalf("the lane's commit was read before submit: %v", git.resolveCalls)
	}

	next, cmd := m.Update(form.SubmitMsg{})
	m = next.(Model)
	if m.submitting {
		t.Fatal("the submit started before the lane's commit was read")
	}
	next, cmd = m.Update(cmd())
	m = next.(Model)
	if !m.submitting {
		t.Fatal("reading the lane's commit did not resume the submit")
	}
	if !reflect.DeepEqual(git.resolveCalls, []string{laneDir + " HEAD"}) {
		t.Errorf("commits read: %v, want HEAD once, in the lane", git.resolveCalls)
	}

	drainSubmitProgress(t, m, cmd)
	if len(runner.worktreeReqs) != 1 {
		t.Fatalf("worktree creates = %+v, want one", runner.worktreeReqs)
	}
	if req := runner.worktreeReqs[0]; req.Cwd != laneRoot || req.Base != laneHead {
		t.Errorf("worktree create --cwd %q --base %q, want --cwd %q --base %q", req.Cwd, req.Base, laneRoot, laneHead)
	}
}

// TestSubmit_FromALaneAChosenBaseIsResolvedInTheLane: herdr runs `git
// worktree add` in the source, so a base handed to it from the repository
// root would be resolved there. A chosen base is therefore read in the lane
// at submit too -- for a branch the same commit, but the rule is the one
// that keeps HEAD-relative refs honest on the command side. The source still
// moves to the repository root.
func TestSubmit_FromALaneAChosenBaseIsResolvedInTheLane(t *testing.T) {
	runner := &submitFakeRunner{topo: herdrc.CreatedTopology{WorkspaceID: "ws-1", TabID: "t-1", PaneID: "pane-1"}}
	git := laneGit()
	m := laneModel(t, runner, git, true)
	m.worktree.SetBase("main")
	if got := m.worktree.Base(); got != "main" {
		t.Fatalf("test setup: base = %q, want main", got)
	}

	next, cmd := m.Update(form.SubmitMsg{})
	m = next.(Model)
	next, cmd = m.Update(cmd())
	m = next.(Model)
	if !m.submitting {
		t.Fatal("the submit did not resume once the base was read")
	}
	drainSubmitProgress(t, m, cmd)
	if !reflect.DeepEqual(git.resolveCalls, []string{laneDir + " main"}) {
		t.Errorf("commits read: %v, want main once, in the lane", git.resolveCalls)
	}
	if req := runner.worktreeReqs[0]; req.Cwd != laneRoot || req.Base != laneMain {
		t.Errorf("worktree create --cwd %q --base %q, want --cwd %q --base %q", req.Cwd, req.Base, laneRoot, laneMain)
	}
}

// TestSubmit_FromALaneWithNoWorktreeStaysInTheLane: without a worktree the
// session runs in the lane itself, and nothing needs its commit.
func TestSubmit_FromALaneWithNoWorktreeStaysInTheLane(t *testing.T) {
	git := laneGit()
	m := laneModel(t, &submitFakeRunner{}, git, false)

	next, _ := m.Update(form.SubmitMsg{})
	m = next.(Model)
	if !m.submitting {
		t.Fatal("a submit with no worktree waited for something")
	}
	if m.submitInput.ProjectDir != laneDir || m.submitInput.Linked != (plan.LinkedCheckout{}) {
		t.Errorf("project %q, linked %+v; want the lane and nothing linked", m.submitInput.ProjectDir, m.submitInput.Linked)
	}
	if len(git.resolveCalls) != 0 {
		t.Errorf("commits read for a session with no worktree: %v", git.resolveCalls)
	}
}

// TestSubmit_AnUnreadableLaneCommitStopsTheSubmit: with no commit to cut
// from, what is left is the primary checkout's HEAD -- the wrong commit,
// silently. The submit stops on the worktree row instead, where a base can
// be chosen.
func TestSubmit_AnUnreadableLaneCommitStopsTheSubmit(t *testing.T) {
	git := laneGit()
	git.resolveErr = errors.New("ambiguous argument 'HEAD'")
	m := laneModel(t, &submitFakeRunner{}, git, true)

	next, cmd := m.Update(form.SubmitMsg{})
	m = next.(Model)
	next, _ = m.Update(cmd())
	m = next.(Model)

	if m.submitting {
		t.Fatal("the submit went ahead without the lane's commit")
	}
	if got := m.form.FocusedID(); got != "worktree" {
		t.Errorf("focus ended on %q, want the worktree row, where a base can be chosen", got)
	}
	if got := fieldText(m.worktree, 100); !strings.Contains(got, "pick a base") {
		t.Errorf("the worktree row should say why the submit stopped:\n%s", got)
	}
}

// TestSubmit_ALaneAnswerForAnotherDirectoryIsNotUsed: the dir check's answer
// belongs to the directory it was asked about, and linkedCheckout uses it only
// while the project row still holds that directory.
//
// Neither window a user had into that still exists. A submit waits for the
// check of the current value before it validates (#195), and the form is
// frozen while the submit reads the lane's commit (#136), so no keystroke
// reaches the project row in between. What can still move the row then is
// not a keystroke: a directory listing that lands late swaps the candidates
// under the selection, and widgets.Picker may re-anchor it
// (TestSelectionStaysValidatedAcrossPoolChanges). retypeProject edits the
// field directly, past Update and so past the freeze, which is how this test
// stands in for that. What is pinned is that the lane's repository does not
// receive another project's worktree.
func TestSubmit_ALaneAnswerForAnotherDirectoryIsNotUsed(t *testing.T) {
	git := laneGit()
	m := laneModel(t, &submitFakeRunner{}, git, true)

	next, readCommit := m.Update(form.SubmitMsg{})
	m = next.(Model)
	if m.submitting || readCommit == nil {
		t.Fatal("test setup: the submit did not stop to read the lane's commit")
	}
	retypeProject(&m, "/elsewhere")
	if got := m.dir.Value(); got != "/elsewhere" {
		t.Fatalf("test setup: project = %q, want /elsewhere", got)
	}

	next, _ = m.Update(readCommit())
	m = next.(Model)
	if !m.submitting {
		t.Fatal("the submit did not go on once the lane's commit was read")
	}
	if m.submitInput.Linked != (plan.LinkedCheckout{}) {
		t.Errorf("linked = %+v, want nothing: that answer was about %s", m.submitInput.Linked, laneDir)
	}
}

// TestSubmit_FromALaneTheCommitIsNotRemembered: projects.json is keyed on the
// origin root, so a remembered commit would pin every later session in the
// repository to this one. No base was chosen, and that is what is kept.
func TestSubmit_FromALaneTheCommitIsNotRemembered(t *testing.T) {
	m := newSubmitTestModel(t, &submitFakeRunner{}, testSetup{Ctx: laneContext()})
	m.projectKey = laneRoot
	m.submitInput = plan.Input{
		ProjectDir: laneDir, Title: "t", AgentKind: "claude", UseWorktree: true,
		Linked: plan.LinkedCheckout{RepoRoot: laneRoot, Commit: laneHead},
	}

	_, cmd := m.handleSubmitDone(submitDoneMsg{result: plan.ExecResult{FailedIndex: -1}})
	if _, ok := cmd().(statePersistedMsg); !ok {
		t.Fatal("handleSubmitDone on success did not run the state write")
	}
	projects, _ := config.LoadProjects(m.stateDir)
	entry, ok := projects.Get(laneRoot)
	if !ok {
		t.Fatal("projects.json has no entry for the repository")
	}
	if entry.Base != "" {
		t.Errorf("remembered base = %q, want none", entry.Base)
	}
}

// TestSubmitStepDetail_ALaneNamesTheCommit: the submit screen's worktree row
// says what the branch is cut from. From a lane with no base chosen that is
// the commit, not "HEAD" -- which from the repository root would name the
// wrong one. A chosen base is named as chosen.
func TestSubmitStepDetail_ALaneNamesTheCommit(t *testing.T) {
	op := plan.Op{Kind: plan.OpWorktreeCreate}
	for _, tc := range []struct {
		name string
		in   plan.Input
		want string
	}{
		{"no base chosen", plan.Input{Branch: "zvi/x", Linked: plan.LinkedCheckout{RepoRoot: laneRoot, Commit: laneHead}}, "zvi/x from 3f2a9c1"},
		{"a chosen base", plan.Input{Branch: "zvi/x", BaseRef: "main", Linked: plan.LinkedCheckout{RepoRoot: laneRoot, Commit: laneMain}}, "zvi/x from main"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := submitStepDetail(op, tc.in); got != tc.want {
				t.Errorf("detail = %q, want %q", got, tc.want)
			}
		})
	}
}
