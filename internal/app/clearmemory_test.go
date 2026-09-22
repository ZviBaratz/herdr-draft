package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/defaults"
	"github.com/ZviBaratz/herdr-draft/internal/form"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
	"github.com/ZviBaratz/herdr-draft/internal/plan"
)

// #135: v2 spec §10 says "⌃R ⌃R clears back to the repository default" --
// back past projects.json, the one tier above .herdr-draft.toml, and no
// further. The rebuild used to mark the four fields projects.json remembers
// as touched, which stopped per-project memory re-applying as intended but
// stopped the REPOSITORY tier as well, and for every later project in the
// popup: a repository whose file said split-here cleared to new space while
// the resolution still credited .herdr-draft.toml, and so did the next
// repository.
//
// Every test here but one drives the clear through Update and settles the
// rebuilt form's own dir check, because that check is the whole subject:
// New resolves without either per-project tier, and it is the check's
// answer that decides which of them come back. The exception is the test
// about that check never landing.

// clearFavorites makes every kind a memory fixture names selectable, as
// memoryModel does.
var clearFavorites = config.AgentsConfig{Favorites: []string{"claude", "codex", "gemini"}}

// splitHereTrunk is the committed file both fixture repositories carry:
// a placement and a base the built-in chain would never pick, and the
// worktree off so the placement row is live and its panel can be read
// (placement spec §14).
var splitHereTrunk = config.RepoConfig{
	DefaultWorktree:  ptrBool(false),
	DefaultPlacement: "split-here",
	DefaultBase:      "trunk",
}

// clearModel opens the form on /repo-a with both repositories committing
// splitHereTrunk and memory as projects.json, the first dir check settled.
// Every ref either fixture can name resolves, so #194's settle keeps them.
func clearModel(t *testing.T, memory map[string]config.ProjectDefaults) Model {
	t.Helper()
	git := newFakeGit()
	git.repoRoots = map[string]string{
		"/repo-a":                 "/repo-a",
		"/repo-a/.worktrees/lane": "/repo-a",
		"/repo-b":                 "/repo-b",
	}
	git.commits = map[string]string{
		"/repo-a trunk": "4e5f607", "/repo-a main": "0a1b2c3",
		"/repo-b trunk": "4e5f607", "/repo-b main": "0a1b2c3",
		"/repo-a/.worktrees/lane trunk": "4e5f607",
	}
	m, _ := repoConfigModel(t, "/repo-a", testSetup{
		Git:      git,
		Config:   config.Config{Agents: clearFavorites},
		Projects: memoryFor(memory),
	}, map[string]config.RepoConfig{"/repo-a": splitHereTrunk, "/repo-b": splitHereTrunk})
	return m
}

// clearAndSettle is ⌃R⌃R as the app receives it, followed by everything the
// rebuilt form schedules -- its own dir check above all.
func clearAndSettle(t *testing.T, m Model) Model {
	t.Helper()
	next, cmd := m.Update(form.ClearRequestedMsg{})
	fresh := pumpAsync(t, next.(Model), []tea.Cmd{cmd})
	// Without this the assertions after a clear could pass because the
	// rebuilt form never asked about its project at all.
	if got := fresh.projectKey; got != "/repo-a" {
		t.Fatalf("after the clear, projectKey = %q, want the rebuilt form's dir check to have resolved %q", got, "/repo-a")
	}
	return fresh
}

// assertRepoPlacement is the issue's own observation turned round: the
// value, its tier and the line on the panel, which the defect had
// disagreeing -- new space on the row, `.herdr-draft.toml` in the
// resolution.
func assertRepoPlacement(t *testing.T, m Model, where string) {
	t.Helper()
	if got := m.placement.Value(); got != plan.PlacementSplitHere {
		t.Errorf("%s: placement = %v, want the repository's split-here", where, got)
	}
	if got := m.resolved.From[defaults.FieldPlacement]; got != defaults.TierRepoConfig {
		t.Errorf("%s: From[placement] = %v, want %v", where, got, defaults.TierRepoConfig)
	}
	if frame := focusedFrame(t, m, "placement"); !strings.Contains(frame, provenanceLine) {
		t.Errorf("%s: the placement panel does not say %q:\n%s", where, provenanceLine, frame)
	}
}

// TestClear_KeepsTheRepositoryDefault is the issue's scenario end to end:
// open on a repository whose file says split-here, clear, then move to
// another repository whose file says the same.
func TestClear_KeepsTheRepositoryDefault(t *testing.T) {
	m := clearModel(t, nil)
	assertRepoPlacement(t, m, "setup")

	m = clearAndSettle(t, m)
	assertRepoPlacement(t, m, "after the clear")

	m = switchProject(t, m, "", "/repo-b")
	assertRepoPlacement(t, m, "in the next repository")
}

// TestClear_DropsThisProjectsMemoryAndNothingUnderIt is the other half of
// v2 spec §10's sentence, field by field: each of the four values
// projects.json remembers goes back to what the tiers under it say, which
// for the three the repository's file sets is the file, and for the agent
// kind -- which a repository cannot set -- is config.toml's favourite.
func TestClear_DropsThisProjectsMemoryAndNothingUnderIt(t *testing.T) {
	m := clearModel(t, map[string]config.ProjectDefaults{
		"/repo-a": {Kind: "codex", Worktree: ptrBool(true), Placement: "tab-here", Base: "main"},
	})
	for _, field := range []string{defaults.FieldAgentKind, defaults.FieldWorktree, defaults.FieldPlacement, defaults.FieldBaseRef} {
		if got := m.resolved.From[field]; got != defaults.TierProjectMemory {
			t.Fatalf("setup: From[%q] = %v, want %v -- the clear would have nothing to drop", field, got, defaults.TierProjectMemory)
		}
	}

	m = clearAndSettle(t, m)

	if got := m.agent.Value(); got != "claude" {
		t.Errorf("agent kind = %q, want the first favourite %q rather than the remembered codex", got, "claude")
	}
	if m.worktree.On() {
		t.Error("worktree is on, want the repository's off rather than the remembered on")
	}
	if got := m.placement.Value(); got != plan.PlacementSplitHere {
		t.Errorf("placement = %v, want the repository's split-here rather than the remembered tab-here", got)
	}
	if got := m.worktree.RequestedBase(); got != "trunk" {
		t.Errorf("base = %q, want the repository's %q rather than the remembered main", got, "trunk")
	}
	for _, field := range []string{defaults.FieldWorktree, defaults.FieldPlacement, defaults.FieldBaseRef} {
		if got := m.resolved.From[field]; got != defaults.TierRepoConfig {
			t.Errorf("From[%q] = %v, want %v", field, got, defaults.TierRepoConfig)
		}
	}
	if got := m.resolved.From[defaults.FieldAgentKind]; got == defaults.TierProjectMemory {
		t.Errorf("From[agent_kind] = %v after the clear", got)
	}
}

// TestClear_KeepsTheTiersUnderTheRepository: back to the repository
// default and no further means every tier under it still applies too. The
// worktree toggle is where the touched flags hid that: New never sets it --
// only a dir check can, once the project is known to be a repository -- so
// a clear left it off under a config.toml that says on, whatever the
// repository and whatever the project.
func TestClear_KeepsTheTiersUnderTheRepository(t *testing.T) {
	m := settle(t, newTestModel(t, testSetup{
		Ctx:      herdrc.Context{WorkspaceCwd: "/repo-a"},
		Config:   config.Config{DefaultWorktree: true, Agents: clearFavorites},
		Projects: memoryFor(map[string]config.ProjectDefaults{"/repo-a": {Worktree: ptrBool(false)}}),
	}))
	if m.worktree.On() {
		t.Fatal("setup: the worktree is on, want projects.json's off over config.toml's on")
	}

	m = clearAndSettle(t, m)

	if !m.worktree.On() {
		t.Error("the worktree is off after the clear, want config.toml's on")
	}
	if got := m.resolved.From[defaults.FieldWorktree]; got != defaults.TierUserConfig {
		t.Errorf("From[worktree] = %v, want %v", got, defaults.TierUserConfig)
	}
}

// TestClear_ReturningToTheClearedProjectReAppliesItsMemory pins the choice
// clearedMemory's comment argues for: a clear is a statement about
// the form it rebuilt, and once the project row has left that project the
// statement is over. Coming back is a project change like any other, and
// v2 spec §10 re-applies memory on every one.
//
// /repo-b remembers nothing, so the middle step also shows the clear
// lifting without borrowing a memory to prove it.
func TestClear_ReturningToTheClearedProjectReAppliesItsMemory(t *testing.T) {
	m := clearModel(t, map[string]config.ProjectDefaults{
		"/repo-a": {Kind: "codex", Placement: "tab-here"},
	})
	m = clearAndSettle(t, m)
	if got := m.agent.Value(); got != "claude" {
		t.Fatalf("setup: agent kind = %q after the clear, want %q", got, "claude")
	}

	m = switchProject(t, m, "", "/repo-b")
	assertRepoPlacement(t, m, "in /repo-b")

	m = switchProject(t, m, "/repo-b", "/repo-a")
	if got := m.agent.Value(); got != "codex" {
		t.Errorf("back in /repo-a, agent kind = %q, want the remembered %q", got, "codex")
	}
	if got := m.placement.Value(); got != plan.PlacementTabHere {
		t.Errorf("back in /repo-a, placement = %v, want the remembered tab-here", got)
	}
	if got := m.resolved.From[defaults.FieldPlacement]; got != defaults.TierProjectMemory {
		t.Errorf("back in /repo-a, From[placement] = %v, want %v", got, defaults.TierProjectMemory)
	}
}

// TestClear_MovingToAnotherProjectResolvesItInFull: the clear is about one
// project, so the next one gets its memory at once -- the half the old
// touched flags got wrong from the other direction, since they stopped
// memory for every project for the rest of the popup.
func TestClear_MovingToAnotherProjectResolvesItInFull(t *testing.T) {
	m := clearModel(t, map[string]config.ProjectDefaults{
		"/repo-a": {Kind: "codex", Placement: "tab-here"},
		"/repo-b": {Kind: "gemini", Placement: "new-space"},
	})
	m = switchProject(t, clearAndSettle(t, m), "", "/repo-b")

	if got := m.agent.Value(); got != "gemini" {
		t.Errorf("agent kind = %q, want /repo-b's remembered %q", got, "gemini")
	}
	if got := m.placement.Value(); got != plan.PlacementNewSpace {
		t.Errorf("placement = %v, want /repo-b's remembered new space", got)
	}
}

// TestClear_TheClearedProjectIsTheOneTheRebuiltFormOpenedOn: a rebuild puts
// the project row back on the popup's opening project, and the clear is
// about THAT one. A project the user picks before its check has even
// answered was chosen after the clear, so it resolves in full -- which it
// would not if the clear simply claimed the first answer to land.
func TestClear_TheClearedProjectIsTheOneTheRebuiltFormOpenedOn(t *testing.T) {
	m := clearModel(t, map[string]config.ProjectDefaults{
		"/repo-b": {Kind: "gemini", Placement: "new-space"},
	})
	// The rebuilt form's own dir check is never delivered: the project
	// change below supersedes it, exactly as it would inside the debounce.
	fresh, _ := m.handleClearRequested()
	fresh = switchProject(t, fresh, "", "/repo-b")

	if got := fresh.projectKey; got != "/repo-b" {
		t.Fatalf("setup: projectKey = %q, want %q", got, "/repo-b")
	}
	if got := fresh.agent.Value(); got != "gemini" {
		t.Errorf("agent kind = %q, want /repo-b's remembered %q", got, "gemini")
	}
	if got := fresh.resolved.From[defaults.FieldPlacement]; got != defaults.TierProjectMemory {
		t.Errorf("From[placement] = %v, want %v", got, defaults.TierProjectMemory)
	}
}

// TestClear_HoldsAcrossALinkedCheckoutOfTheSameProject: the clear is keyed
// the way projects.json is (v2 spec §10's key rule), so a linked worktree
// of the cleared repository is the same project, and its memory stays
// cleared. Moving there is a move of the row, not of the project the
// memory belongs to.
func TestClear_HoldsAcrossALinkedCheckoutOfTheSameProject(t *testing.T) {
	m := clearModel(t, map[string]config.ProjectDefaults{
		"/repo-a": {Kind: "codex", Placement: "tab-here"},
	})
	m = switchProject(t, clearAndSettle(t, m), "", "/repo-a/.worktrees/lane")

	if got := m.projectKey; got != "/repo-a" {
		t.Fatalf("setup: projectKey = %q, want the lane keyed on its repository %q", got, "/repo-a")
	}
	if got := m.agent.Value(); got == "codex" {
		t.Error("the cleared memory came back in a linked checkout of the same repository")
	}
	if got := m.placement.Value(); got != plan.PlacementSplitHere {
		t.Errorf("placement = %v, want the repository's split-here", got)
	}
}

// TestClear_AFieldTouchedAfterTheClearStaysTheUsers: the clear gives up the
// touched flags it used to set, and the flags go back to meaning what the
// user did. A placement they move after the clear beats every tier on
// every later dir check: the next repository's file, and the cleared
// project's own memory when they come back to it.
func TestClear_AFieldTouchedAfterTheClearStaysTheUsers(t *testing.T) {
	m := clearModel(t, map[string]config.ProjectDefaults{
		"/repo-a": {Placement: "new-space"},
	})
	m = clearAndSettle(t, m)

	m.form.FocusByID("placement")
	next, _ := m.Update(key(tea.KeyLeft, 0)) // split here -> tab here
	m = next.(Model)
	if got := m.placement.Value(); got != plan.PlacementTabHere || !m.placementTouched {
		t.Fatalf("setup: placement = %v touched=%v, want the user's own tab-here", got, m.placementTouched)
	}

	m = switchProject(t, m, "", "/repo-b")
	if got := m.placement.Value(); got != plan.PlacementTabHere {
		t.Errorf("in /repo-b, placement = %v, want the user's own tab-here over the repository's split-here", got)
	}
	m = switchProject(t, m, "/repo-b", "/repo-a")
	if got := m.placement.Value(); got != plan.PlacementTabHere {
		t.Errorf("back in /repo-a, placement = %v, want the user's own tab-here over the remembered new space", got)
	}
}
