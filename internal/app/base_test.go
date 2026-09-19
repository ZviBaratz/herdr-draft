package app

import (
	"context"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/defaults"
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

// baseModel is a form opened on /repo-a -- whose branch list is main and
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
// test. It names a branch that exists only on a remote as its bare name
// (gitx.ListBranches strips origin/), which rev-parse does not resolve, and
// which `git worktree add -b <branch> <path> <name>` answers by making a
// branch of that name instead of the one asked for. `create` has no list to
// consult, so the form must not treat membership as an answer either.
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
