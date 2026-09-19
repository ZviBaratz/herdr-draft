package app

import (
	"context"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/defaults"
	"github.com/ZviBaratz/herdr-draft/internal/form"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
)

// This file is #194 on the popup's side: a base a tier supplies -- remembered
// in projects.json, or committed as .herdr-draft.toml's default_base -- that
// the base picker's branch list does not name. The rule is the same on both
// paths: keep one that resolves, map HEAD and @ to the HEAD row
// (defaults.NormalizeBase), and fall back to HEAD from one that does not,
// saying so. internal/create's equivalence test holds the two paths to it.

// TestSettleBase is the rule itself, which both paths call: a base that
// names a commit in the project stands, one that does not becomes the HEAD
// row with a note naming the ref and the tier it came from, and no base
// asks git nothing.
func TestSettleBase(t *testing.T) {
	git := newFakeGit()
	git.commits = map[string]string{"/repo old-branch": "3d4e5f6"}
	resolve := func(base string) defaults.Resolved {
		return defaults.Resolve(defaults.Sources{
			Project:     config.ProjectDefaults{Base: base},
			HaveProject: base != "",
		})
	}

	kept, note := SettleBase(context.Background(), git, "/repo", resolve("old-branch"))
	if kept.BaseRef != "old-branch" || kept.From[defaults.FieldBaseRef] != defaults.TierProjectMemory || note != "" {
		t.Errorf("a base that resolves: %q from %v, note %q; want it kept as it came, and no note",
			kept.BaseRef, kept.From[defaults.FieldBaseRef], note)
	}

	gone, note := SettleBase(context.Background(), git, "/repo", resolve("gone-branch"))
	if gone.BaseRef != "" || gone.From[defaults.FieldBaseRef] != defaults.TierBuiltin {
		t.Errorf("a base that does not resolve: %q from %v, want the HEAD row from the built-in",
			gone.BaseRef, gone.From[defaults.FieldBaseRef])
	}
	for _, want := range []string{`"gone-branch"`, "projects.json", "HEAD"} {
		if !strings.Contains(note, want) {
			t.Errorf("note = %q, want it to name %s", note, want)
		}
	}

	git.resolveCalls = nil
	head, note := SettleBase(context.Background(), git, "/repo", resolve(""))
	if head.BaseRef != "" || note != "" || len(git.resolveCalls) != 0 {
		t.Errorf("no base: %q, note %q, git asked %v; want nothing asked and nothing said", head.BaseRef, note, git.resolveCalls)
	}

	// Asked in the directory it was handed, which both callers make the
	// project: a lane is the project when the popup opens there, so a lane's
	// base is checked in the lane, where #171 resolves it.
	git.resolveCalls = nil
	SettleBase(context.Background(), git, "/repo", resolve("old-branch"))
	if want := []string{"/repo old-branch"}; !slices.Equal(git.resolveCalls, want) {
		t.Errorf("git asked %v, want %v", git.resolveCalls, want)
	}
}

// TestSettleBase_ASpellingOfHeadIsTheHeadRow: HEAD and @ are mapped without
// asking git (defaults.NormalizeBase), and every other spelling of HEAD
// itself -- HEAD^0, @~0, HEAD@{0}, anything spelled from HEAD or @ that names
// HEAD's own commit -- is found here, where git is already being asked.
// These are the refs that opened #193's hole while the clean gate still
// counted a base inside the new worktree, where they name the worktree
// itself. The popup used to drop them to "" silently; rule 1 alone would
// have kept them as spelled rather than as the HEAD row they mean.
//
// Only HEAD-relative spellings: a branch that happens to be at HEAD's commit
// is a branch, and HEAD~1 is another commit.
func TestSettleBase_ASpellingOfHeadIsTheHeadRow(t *testing.T) {
	git := newFakeGit()
	// The six spellings measured letting the clean gate remove work, which
	// gitx's TestResolveRefAnswersTheBaseRule holds real git to.
	spellings := []string{"HEAD^0", "@~0", "HEAD~0", "HEAD^{commit}", "HEAD@{0}", "@{0}"}
	git.commits = map[string]string{
		"/repo HEAD": "0a1b2c3", "/repo main": "0a1b2c3", "/repo HEADroom": "0a1b2c3", "/repo HEAD~1": "1b2c3d4",
	}
	cases := []struct{ ref, want string }{{"HEAD~1", "HEAD~1"}, {"main", "main"}, {"HEADroom", "HEADroom"}}
	for _, ref := range spellings {
		git.commits["/repo "+ref] = "0a1b2c3"
		cases = append(cases, struct{ ref, want string }{ref, ""})
	}
	resolve := func(base string) defaults.Resolved {
		return defaults.Resolve(defaults.Sources{Project: config.ProjectDefaults{Base: base}, HaveProject: true})
	}
	for _, tc := range cases {
		got, note := SettleBase(context.Background(), git, "/repo", resolve(tc.ref))
		if got.BaseRef != tc.want || note != "" {
			t.Errorf("SettleBase(%q) = %q, note %q; want %q and no note", tc.ref, got.BaseRef, note, tc.want)
		}
		// Chosen by the tier, as HEAD is: it is the tier's value, spelled
		// another way, and nothing was dropped.
		if got.From[defaults.FieldBaseRef] != defaults.TierProjectMemory {
			t.Errorf("SettleBase(%q) attributed to %v, want %v", tc.ref, got.From[defaults.FieldBaseRef], defaults.TierProjectMemory)
		}
	}
}

// baseModel is a form opened on /repo-a// baseModel is a form opened on /repo-a -- whose branch list is main and
// develop, and in which commits are the refs git can resolve -- with
// projects.json remembering base there, settled: the dir check, the branch
// list and the check SettleBase runs for the popup have all landed.
func baseModel(t *testing.T, base string, commits map[string]string) Model {
	t.Helper()
	git := newFakeGit()
	git.listBranchesResult = []string{"main", "develop"}
	git.currentBranchResult = "main"
	git.commits = commits
	m := memoryModel(t, "/repo-a", memoryFor(map[string]config.ProjectDefaults{
		"/repo-a": {Worktree: ptrBool(true), Base: base},
	}), git)
	return m
}

// worktreePanel is the real form with the worktree row focused, ANSI
// stripped: where a note about the base is shown, and what a user sees.
func worktreePanel(t *testing.T, m Model) string {
	t.Helper()
	return focusedFrame(t, m, "worktree")
}

// TestPopup_ABaseTheListDoesNotNameIsKept is rule 1: the branch list is the
// 50 most recently committed branches, so an older one -- or a tag, or
// HEAD~1 -- used to fall back to the HEAD row without a word. Once git says
// it resolves it is offered, and selected, and nothing needs saying.
func TestPopup_ABaseTheListDoesNotNameIsKept(t *testing.T) {
	m := baseModel(t, "old-branch", map[string]string{"/repo-a old-branch": "3d4e5f6"})

	if got := m.worktree.Base(); got != "old-branch" {
		t.Fatalf("Base() = %q, want the remembered %q", got, "old-branch")
	}
	if got := m.PlanInput().BaseRef; got != "old-branch" {
		t.Errorf("plan.Input.BaseRef = %q, want %q", got, "old-branch")
	}
	if panel := worktreePanel(t, m); strings.Contains(panel, "ignoring base") {
		t.Errorf("the worktree panel reports a base that stands:\n%s", panel)
	}
}

// TestPopup_ABaseThatDoesNotResolveFallsBackAndSaysSo is rule 3: a remembered
// branch deleted since becomes the HEAD row, and the worktree panel says
// which base was dropped and why -- the form's old fall-back was the same
// HEAD, silently.
func TestPopup_ABaseThatDoesNotResolveFallsBackAndSaysSo(t *testing.T) {
	m := baseModel(t, "gone-branch", nil)

	if got := m.worktree.Base(); got != "" {
		t.Fatalf("Base() = %q, want the HEAD row", got)
	}
	if got := m.resolved.From[defaults.FieldBaseRef]; got != defaults.TierBuiltin {
		t.Errorf("From[base] = %v, want %v: projects.json no longer supplies what is used", got, defaults.TierBuiltin)
	}
	if panel := worktreePanel(t, m); !strings.Contains(panel, `ignoring base "gone-branch" from projects.json`) {
		t.Errorf("the worktree panel does not say the base was dropped:\n%s", panel)
	}
}

// TestPopup_AListedBaseThatDoesNotResolveStillFallsBack: the list is not the
// test. It used to name a branch that exists only on a remote by its bare
// name, which rev-parse does not resolve, and which `git worktree add -b
// <branch> <path> <name>` answers by making a branch of that name instead
// of the one asked for. gitx.ListBranches now offers such a branch as
// origin/<name> (#198), but a list is still a snapshot, and `create` has no
// list to consult, so the form must not treat membership as an answer
// either.
func TestPopup_AListedBaseThatDoesNotResolveStillFallsBack(t *testing.T) {
	m := baseModel(t, "develop", nil)

	if got := m.worktree.Base(); got != "" {
		t.Fatalf("Base() = %q, want the HEAD row: develop is listed and does not resolve", got)
	}
	if panel := worktreePanel(t, m); !strings.Contains(panel, `ignoring base "develop"`) {
		t.Errorf("the worktree panel does not say the base was dropped:\n%s", panel)
	}
}

// TestPopup_TheNoteGoesWhenTheUserPicksABase: the note says HEAD is used,
// which stops being true the moment the user picks something else, exactly
// as provenance stops being true (showRepoConfig).
func TestPopup_TheNoteGoesWhenTheUserPicksABase(t *testing.T) {
	m := baseModel(t, "gone-branch", nil)
	m.form.FocusByID("worktree")
	for _, k := range []tea.KeyPressMsg{
		{Code: tea.KeyDown}, {Code: tea.KeyDown}, // chips -> branch -> base
		{Code: tea.KeyDown}, // HEAD -> main
	} {
		next, _ := m.Update(k)
		m = next.(Model)
	}
	if got := m.worktree.Base(); got != "main" {
		t.Fatalf("setup: Base() = %q, want the user's %q", got, "main")
	}
	if panel := worktreePanel(t, m); strings.Contains(panel, "ignoring base") {
		t.Errorf("the note outlived the user's own choice:\n%s", panel)
	}
}

// TestPopup_AnAnswerForAnEarlierResolutionIsDropped: the check runs in the
// background, and a project change re-resolves the base before an answer
// about the previous one lands. That answer must move nothing.
func TestPopup_AnAnswerForAnEarlierResolutionIsDropped(t *testing.T) {
	m := baseModel(t, "old-branch", map[string]string{"/repo-a old-branch": "3d4e5f6"})

	next, _ := m.Update(baseSettledMsg{
		version:  m.baseSettleVersion - 1,
		resolved: m.resolved.WithoutBase(),
		note:     `ignoring base "old-branch" from projects.json: stale`,
	})
	m = next.(Model)
	if got := m.worktree.Base(); got != "old-branch" {
		t.Errorf("Base() = %q after a stale answer, want %q untouched", got, "old-branch")
	}
	if panel := worktreePanel(t, m); strings.Contains(panel, "stale") {
		t.Errorf("a stale answer reached the panel:\n%s", panel)
	}
}

// TestPopup_AnAnswerAfterTheUserChoseIsDropped: a base the user picked while
// the check was out is theirs, and neither the fall-back nor its note may
// replace it.
func TestPopup_AnAnswerAfterTheUserChoseIsDropped(t *testing.T) {
	m := baseModel(t, "old-branch", map[string]string{"/repo-a old-branch": "3d4e5f6"})
	m.baseTouched = true

	next, _ := m.Update(baseSettledMsg{
		version:  m.baseSettleVersion,
		resolved: m.resolved.WithoutBase(),
		note:     `ignoring base "old-branch" from projects.json`,
	})
	m = next.(Model)
	if got := m.worktree.Base(); got != "old-branch" {
		t.Errorf("Base() = %q, want the user's %q to stand", got, "old-branch")
	}
	if panel := worktreePanel(t, m); strings.Contains(panel, "ignoring base") {
		t.Errorf("a note about a base the user replaced reached the panel:\n%s", panel)
	}
}

// TestPopup_ADroppedRepoDefaultIsNotAttributedToTheRepo: `from
// .herdr-draft.toml` names the file as the source of what the panel shows,
// and once its default_base is dropped the panel shows HEAD, which the file
// did not choose.
func TestPopup_ADroppedRepoDefaultIsNotAttributedToTheRepo(t *testing.T) {
	git := newFakeGit()
	git.listBranchesResult = []string{"main"}
	m, _ := repoConfigModel(t, "/repo-a", testSetup{Git: git}, map[string]config.RepoConfig{
		"/repo-a": {DefaultBase: "gone-branch"},
	})

	// A line of its own: the note names the file too, as the tier the
	// dropped base came from.
	panel := worktreePanel(t, m)
	for line := range strings.Lines(panel) {
		if strings.TrimSpace(line) == provenanceLine {
			t.Errorf("the panel credits the repository with a base it no longer supplies:\n%s", panel)
		}
	}
	if !strings.Contains(panel, `ignoring base "gone-branch" from .herdr-draft.toml`) {
		t.Errorf("the panel does not say the repository's base was dropped:\n%s", panel)
	}
}

// TestPopup_AProjectChangeWithdrawsTheNoteAndTheOffer: both are about the
// previous project's base, and a ref offered there may name nothing here.
func TestPopup_AProjectChangeWithdrawsTheNoteAndTheOffer(t *testing.T) {
	gone := baseModel(t, "gone-branch", nil)
	gone = switchProject(t, gone, "/repo-a", "/repo-b")
	if panel := worktreePanel(t, gone); strings.Contains(panel, "ignoring base") {
		t.Errorf("/repo-a's note followed the form to /repo-b:\n%s", panel)
	}

	offered := baseModel(t, "old-branch", map[string]string{"/repo-a old-branch": "3d4e5f6"})
	offered = switchProject(t, offered, "/repo-a", "/repo-b")
	offered.worktree.SetBase("old-branch")
	if got := offered.worktree.Base(); got != "" {
		t.Errorf("Base() = %q in /repo-b, want /repo-a's offer withdrawn", got)
	}
}

// TestPopup_AnOfferedBaseIsNotTheUsersChoice: the check moving the base is
// the app applying a default, not the user deciding, so per-project memory
// still re-applies on the next project change. noteUserEdits compares the
// base against what the app last put there, and an offer that moved it
// without refreshing that snapshot would read as a user edit on the very
// next keystroke.
func TestPopup_AnOfferedBaseIsNotTheUsersChoice(t *testing.T) {
	git := newFakeGit()
	git.listBranchesResult = []string{"main", "develop"}
	git.commits = map[string]string{"/repo-a old-branch": "3d4e5f6", "/repo-b develop": "5f60718"}
	m := memoryModel(t, "/repo-a", memoryFor(map[string]config.ProjectDefaults{
		"/repo-a": {Base: "old-branch"},
		"/repo-b": {Base: "develop"},
	}), git)
	if got := m.worktree.Base(); got != "old-branch" {
		t.Fatalf("setup: Base() = %q, want the offered %q", got, "old-branch")
	}

	m.form.FocusByID("title")
	next, _ := m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	m = next.(Model)
	m = switchProject(t, m, "/repo-a", "/repo-b")

	if got := m.worktree.Base(); got != "develop" {
		t.Errorf("Base() in /repo-b = %q, want its remembered %q: nobody chose /repo-a's", got, "develop")
	}
}

// pumpHoldingBaseChecks is pumpAsync, except that every base check's answer
// is kept back and returned rather than delivered: the state between the dir
// check landing -- which is what schedules the base check -- and the base
// check's own answer.
func pumpHoldingBaseChecks(t *testing.T, m Model, cmds []tea.Cmd) (Model, []tea.Msg) {
	t.Helper()
	var held []tea.Msg
	queue := append([]tea.Cmd(nil), cmds...)
	for range 64 {
		if len(queue) == 0 {
			return m, held
		}
		cmd := queue[0]
		queue = queue[1:]
		if cmd == nil {
			continue
		}
		switch msg := cmd().(type) {
		case baseSettledMsg:
			held = append(held, msg)
		case dirDebounceMsg, dirResultMsg, baseDebounceMsg, baseResultMsg:
			next, out := m.Update(msg)
			m = next.(Model)
			queue = append(queue, out)
		case tea.BatchMsg:
			queue = append(queue, msg...)
		}
	}
	t.Fatalf("async pipelines did not settle")
	return m, nil
}

// TestPopup_ASubmitWaitsForTheBaseCheck: the base check is scheduled by the
// dir check landing and answers a moment later, and a submit in between
// would build its plan from a base nobody had answered for yet -- for a base
// the list does not name, the HEAD row, which is the drift #194 removes. The
// window is a `git rev-parse` wide, and #195's hold makes it certain rather
// than rare: that hold releases a submit the instant the dir check lands. So
// the submit waits for this check too, and goes on by itself when it lands.
func TestPopup_ASubmitWaitsForTheBaseCheck(t *testing.T) {
	runner := &submitFakeRunner{topo: herdrc.CreatedTopology{WorkspaceID: "ws-1", TabID: "t-1", PaneID: "pane-1"}}
	git := newFakeGit()
	git.listBranchesResult = []string{"main"}
	git.commits = map[string]string{"/repo-a old-branch": "3d4e5f6"}
	m := newSubmitTestModel(t, runner, testSetup{
		Git: git,
		Ctx: herdrc.Context{WorkspaceCwd: "/repo-a"},
		Projects: memoryFor(map[string]config.ProjectDefaults{
			"/repo-a": {Worktree: ptrBool(true), Base: "old-branch"},
		}),
	})
	m, held := pumpHoldingBaseChecks(t, m, m.initCmds)
	if len(held) != 1 {
		t.Fatalf("base checks held = %d, want the one the dir check scheduled", len(held))
	}
	m.title.SetTitle("fix the thing", false)
	// Its title check lands first: a submit waits for that one too (#137),
	// and this test is about the base check.
	m = landTitle(t, m, m.reactToChanges())

	next, _ := m.Update(form.SubmitMsg{})
	m = next.(Model)
	if m.submitting {
		t.Fatalf("the submit started with the base check still out, from base %q", m.submitInput.BaseRef)
	}

	next, _ = m.Update(held[0])
	m = next.(Model)
	if !m.submitting {
		t.Fatal("the base check landing did not resume the submit")
	}
	if got := m.submitInput.BaseRef; got != "old-branch" {
		t.Errorf("submitted base = %q, want the remembered %q the check confirmed", got, "old-branch")
	}
}

// TestPopup_AStaleBaseCheckDoesNotReleaseASubmit: the answer a submit waits
// for is the one about the project the row holds now. An answer about the
// project before it is dropped, and must not count as that one having landed
// -- so a submit pressed after it is still held, whichever order the two
// arrive in.
func TestPopup_AStaleBaseCheckDoesNotReleaseASubmit(t *testing.T) {
	runner := &submitFakeRunner{topo: herdrc.CreatedTopology{WorkspaceID: "ws-1", TabID: "t-1", PaneID: "pane-1"}}
	git := newFakeGit()
	git.listBranchesResult = []string{"main"}
	git.commits = map[string]string{"/repo-a old-a": "3d4e5f6", "/repo-b old-b": "4e5f607"}
	m := newSubmitTestModel(t, runner, testSetup{
		Git: git,
		Ctx: herdrc.Context{WorkspaceCwd: "/repo-a"},
		Projects: memoryFor(map[string]config.ProjectDefaults{
			"/repo-a": {Worktree: ptrBool(true), Base: "old-a"},
			"/repo-b": {Worktree: ptrBool(true), Base: "old-b"},
		}),
	})
	m, fromA := pumpHoldingBaseChecks(t, m, m.initCmds)
	backspaceDir(&m, len("/repo-a"))
	typeDir(&m, "/repo-b")
	m, fromB := pumpHoldingBaseChecks(t, m, m.reactToChanges())
	if len(fromA) != 1 || len(fromB) != 1 {
		t.Fatalf("base checks held: %d for /repo-a and %d for /repo-b, want one each", len(fromA), len(fromB))
	}
	m.title.SetTitle("fix the thing", false)
	// Its title check lands first: a submit waits for that one too (#137),
	// and this test is about the base check.
	m = landTitle(t, m, m.reactToChanges())

	next, _ := m.Update(fromA[0])
	next, _ = next.(Model).Update(form.SubmitMsg{})
	m = next.(Model)
	if m.submitting {
		t.Fatalf("a submit after /repo-a's answer went on with /repo-b's still out, from base %q", m.submitInput.BaseRef)
	}

	next, _ = m.Update(fromB[0])
	m = next.(Model)
	if !m.submitting || m.submitInput.BaseRef != "old-b" {
		t.Errorf("after /repo-b's answer: submitting=%v from %q, want a submit from old-b", m.submitting, m.submitInput.BaseRef)
	}
}

// TestPopup_AChosenBaseSurvivesAProjectChange: a base the user picked is
// theirs, and a project change re-applies nothing to it. The review's
// scenario: an offered base the user has chosen for themselves, then the
// project row moved within the same repository. Withdrawing the offer took
// the row out from under the selection, and widgets.Picker keeps a vanished
// row's INDEX -- so the base became whichever branch sat there next, without
// a word, and with no check left to run for it.
func TestPopup_AChosenBaseSurvivesAProjectChange(t *testing.T) {
	git := newFakeGit()
	git.listBranchesResult = []string{"main", "develop"}
	git.repoRoots = map[string]string{"/repo-a/sub": "/repo-a"}
	git.commits = map[string]string{"/repo-a old-branch": "3d4e5f6"}
	m := memoryModel(t, "/repo-a", memoryFor(map[string]config.ProjectDefaults{
		"/repo-a": {Worktree: ptrBool(true), Base: "old-branch"},
	}), git)
	m.form.FocusByID("worktree")
	for _, k := range []tea.KeyPressMsg{
		{Code: tea.KeyDown}, {Code: tea.KeyDown}, // chips -> branch -> base
		{Code: tea.KeyUp}, {Code: tea.KeyDown}, // old-branch -> HEAD -> old-branch
	} {
		next, _ := m.Update(k)
		m = next.(Model)
	}
	if !m.baseTouched || m.worktree.Base() != "old-branch" {
		t.Fatalf("setup: touched=%v, Base() = %q, want the user's own old-branch", m.baseTouched, m.worktree.Base())
	}

	m = switchProject(t, m, "/repo-a", "/repo-a/sub")

	if got := m.worktree.Base(); got != "old-branch" {
		t.Errorf("Base() after the project change = %q, want the user's old-branch", got)
	}
}

// TestPopup_APendingBaseReadsAsTheHeadRow: between a project change and the
// check that answers for the new project's base, the row names no branch
// that nobody chose. It used to read whichever branch of the new list sat at
// the old selection's index.
func TestPopup_APendingBaseReadsAsTheHeadRow(t *testing.T) {
	git := newFakeGit()
	git.listBranchesResult = []string{"main", "develop"}
	git.commits = map[string]string{"/repo-a old-a": "3d4e5f6", "/repo-b old-b": "4e5f607"}
	m := memoryModel(t, "/repo-a", memoryFor(map[string]config.ProjectDefaults{
		"/repo-a": {Worktree: ptrBool(true), Base: "old-a"},
		"/repo-b": {Worktree: ptrBool(true), Base: "old-b"},
	}), git)
	if got := m.worktree.Base(); got != "old-a" {
		t.Fatalf("setup: Base() = %q, want the offered old-a", got)
	}

	backspaceDir(&m, len("/repo-a"))
	typeDir(&m, "/repo-b")
	m, held := pumpHoldingBaseChecks(t, m, m.reactToChanges())
	if len(held) != 1 {
		t.Fatalf("base checks held = %d, want /repo-b's", len(held))
	}
	if got := m.worktree.Base(); got != "" {
		t.Errorf("Base() with /repo-b's check still out = %q, want the HEAD row", got)
	}
}
