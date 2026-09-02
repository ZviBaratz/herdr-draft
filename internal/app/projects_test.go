package app

import (
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/defaults"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
	"github.com/ZviBaratz/herdr-draft/internal/plan"
)

// This file covers spec §10's per-project defaults memory as the app layer
// sees it: the projects.json tier reaching the form through the debounced
// dir check, re-applying on every project change, and stopping at a field
// the user has touched.
//
// internal/config owns the file itself (projects_test.go there), and
// internal/gitx owns the key's git half (RepoRoot against a real linked
// worktree) -- what is left for this package is the wiring.

func ptrBool(b bool) *bool { return &b }

// memoryFor builds a Projects fixture with one entry per key.
func memoryFor(entries map[string]config.ProjectDefaults) config.Projects {
	p := config.Projects{Version: 1, Entries: map[string]config.ProjectDefaults{}}
	for k, v := range entries {
		v.Seen = time.Now()
		p.Entries[k] = v
	}
	return p
}

// memoryModel builds a Model pointed at cwd, with per-project memory
// loaded and the initial dir check already settled -- the state a real
// form-open reaches once its first debounce round completes.
func memoryModel(t *testing.T, cwd string, projects config.Projects, git *fakeGit) Model {
	t.Helper()
	if git == nil {
		git = newFakeGit()
	}
	m := newTestModel(t, testSetup{
		Git:      git,
		Ctx:      herdrc.Context{WorkspaceCwd: cwd},
		Config:   config.Config{Agents: config.AgentsConfig{Favorites: []string{"claude", "codex", "gemini"}}},
		Projects: projects,
	})
	return pumpAsync(t, m, m.initCmds)
}

// switchProject types a new project directory into the Project field and
// drives the resulting debounce round to completion -- the real path a
// project change takes (spec §6 field 2's path mode), not a synthesized
// dirResultMsg.
func switchProject(t *testing.T, m Model, from, to string) Model {
	t.Helper()
	backspaceDir(&m, len(from))
	typeDir(&m, to)
	m = pumpAsync(t, m, m.reactToChanges())
	if got := m.dir.Value(); got != to {
		t.Fatalf("after switching, Project = %q, want %q", got, to)
	}
	return m
}

// TestDirResult_AppliesProjectMemory is the read side of spec §10: what
// you last chose in THIS project is what the form opens on.
func TestDirResult_AppliesProjectMemory(t *testing.T) {
	m := memoryModel(t, "/repo-a", memoryFor(map[string]config.ProjectDefaults{
		"/repo-a": {Kind: "codex", Worktree: ptrBool(false), Placement: "tab-here", Base: "main"},
	}), nil)

	if got := m.projectKey; got != "/repo-a" {
		t.Fatalf("projectKey = %q, want the repo root %q", got, "/repo-a")
	}
	if got := m.agent.Value(); got != "codex" {
		t.Errorf("agent kind = %q, want the remembered %q", got, "codex")
	}
	if got := m.placement.Value(); got != plan.PlacementTabHere {
		t.Errorf("placement = %v, want the remembered tab-here", got)
	}
	if m.worktree.On() {
		t.Error("worktree toggle is on, want the remembered off")
	}
	if got := m.resolved.BaseRef; got != "main" {
		t.Errorf("resolved BaseRef = %q, want the remembered %q", got, "main")
	}
	// ...and it reaches the field, which is the half #7 could not build:
	// it resolved and persisted a base with no setter to apply it to, so
	// "remember the base" worked everywhere except on screen.
	//
	// It is applied BEFORE the branch list exists (this model has settled
	// only the dir check), which is the ordering that makes SetBase hold a
	// ref rather than drop it -- so the assertion below is on the field's
	// own state once its list arrives, not on a call having been made.
	m.worktree.SetBaseItems(1, []string{"main", "develop"})
	if got := m.worktree.Base(); got != "main" {
		t.Errorf("WorktreeField.Base() = %q, want the remembered %q", got, "main")
	}
	for _, field := range []string{
		defaults.FieldAgentKind, defaults.FieldPlacement,
		defaults.FieldWorktree, defaults.FieldBaseRef,
	} {
		if got := m.resolved.From[field]; got != defaults.TierProjectMemory {
			t.Errorf("From[%q] = %v, want %v", field, got, defaults.TierProjectMemory)
		}
	}
}

// TestDirResult_NoMemoryLeavesTheGlobalTiers pins the other half: a project
// nothing was ever created in falls through to last-used.json, which is
// what makes per-project memory a pure addition with no migration.
func TestDirResult_NoMemoryLeavesTheGlobalTiers(t *testing.T) {
	git := newFakeGit()
	m := newTestModel(t, testSetup{
		Git:    git,
		Ctx:    herdrc.Context{WorkspaceCwd: "/unremembered"},
		Config: config.Config{Agents: config.AgentsConfig{Favorites: []string{"claude", "codex", "gemini"}}},
		State:  config.State{LastKind: "gemini", LastPlacement: "split-here", LastWorktree: ptrBool(true)},
		Projects: memoryFor(map[string]config.ProjectDefaults{
			"/somewhere-else": {Kind: "codex", Worktree: ptrBool(false), Placement: "tab-here"},
		}),
	})
	m = pumpAsync(t, m, m.initCmds)

	if got := m.agent.Value(); got != "gemini" {
		t.Errorf("agent kind = %q, want the last-used %q", got, "gemini")
	}
	if !m.worktree.On() {
		t.Error("worktree toggle is off, want the last-used true")
	}
	if got := m.resolved.From[defaults.FieldAgentKind]; got != defaults.TierGlobalMemory {
		t.Errorf("From[agent_kind] = %v, want %v", got, defaults.TierGlobalMemory)
	}
}

// TestDirResult_MemoryReAppliesAcrossASecondProjectChange is the test the
// implementation notes demanded and no existing test would have caught.
//
// Per-project memory has to re-apply on EVERY project change, not just the
// first. Whether it may apply is decided by a touched flag, and the touched
// flag is set by comparing each field against a snapshot of what the app
// itself last put there. If that snapshot is shared with lastWorktreeOn --
// which syncDerivedInertness resyncs on every call, including the call
// inside the dir-result handler -- the comparison stops distinguishing an
// app-applied value from a user edit, and the memory either overrides the
// user forever or silently stops applying. Either way the symptom appears
// only at the SECOND project change, which is why this test walks three
// projects rather than two.
func TestDirResult_MemoryReAppliesAcrossASecondProjectChange(t *testing.T) {
	m := memoryModel(t, "/repo-a", memoryFor(map[string]config.ProjectDefaults{
		"/repo-a": {Kind: "codex", Worktree: ptrBool(false), Placement: "tab-here"},
		"/repo-b": {Kind: "gemini", Worktree: ptrBool(true), Placement: "new-space"},
		"/repo-c": {Kind: "claude", Worktree: ptrBool(false), Placement: "split-here"},
	}), nil)

	assertMemory := func(where, kind string, worktreeOn bool, placement plan.Placement) {
		t.Helper()
		if got := m.agent.Value(); got != kind {
			t.Errorf("at %s: agent kind = %q, want %q", where, got, kind)
		}
		if got := m.worktree.On(); got != worktreeOn {
			t.Errorf("at %s: worktree on = %v, want %v", where, got, worktreeOn)
		}
		// Placement is only meaningful with the worktree off: turning one on
		// makes the field inert and snaps it back to New space (spec §6
		// field 5), which is what the form actually does with a worktree.
		if !worktreeOn {
			if got := m.placement.Value(); got != placement {
				t.Errorf("at %s: placement = %v, want %v", where, got, placement)
			}
		}
		if m.worktreeTouched || m.placementTouched || m.agentTouched {
			t.Errorf("at %s: a field was marked touched by the app's own application "+
				"(worktree=%v placement=%v agent=%v) -- memory will stop re-applying",
				where, m.worktreeTouched, m.placementTouched, m.agentTouched)
		}
	}

	assertMemory("the first project", "codex", false, plan.PlacementTabHere)

	m = switchProject(t, m, "", "/repo-b")
	assertMemory("the second project", "gemini", true, plan.PlacementNewSpace)

	// The one that matters: a SECOND change, after the app has already
	// applied memory twice and syncDerivedInertness has run in between.
	m = switchProject(t, m, "/repo-b", "/repo-c")
	assertMemory("the third project", "claude", false, plan.PlacementSplitHere)
}

// TestDirResult_UserEditsSurviveAProjectChange is the touched half of spec
// §10's rule: memory re-applies "unless the user has already touched that
// field". The worktree toggle is driven through the real key path (a
// right-arrow into the focused chip row) so the whole chain is exercised,
// not just the flag.
func TestDirResult_UserEditsSurviveAProjectChange(t *testing.T) {
	m := memoryModel(t, "/repo-a", memoryFor(map[string]config.ProjectDefaults{
		"/repo-a": {Kind: "codex", Worktree: ptrBool(false), Placement: "tab-here"},
		"/repo-b": {Kind: "gemini", Worktree: ptrBool(true), Placement: "new-space"},
	}), nil)
	if m.worktree.On() {
		t.Fatalf("setup: worktree is on, want the remembered off")
	}

	// The user turns the worktree on themselves.
	m.form.FocusByID("worktree")
	next, _ := m.Update(key(tea.KeyRight, 0))
	m = next.(Model)
	if !m.worktree.On() {
		t.Fatalf("setup: the right-arrow did not move the worktree chip row")
	}
	if !m.worktreeTouched {
		t.Fatal("a user keypress moved the worktree toggle but it was not marked touched")
	}

	// The user also picks an agent kind by hand.
	m.agent.SetKind("claude")
	m.reactToChanges()
	if !m.agentTouched {
		t.Fatal("the agent kind moved without the app moving it but was not marked touched")
	}

	// /repo-b remembers worktree=true (which happens to agree) and
	// kind=gemini (which does not). Neither may be applied.
	m = switchProject(t, m, "", "/repo-b")
	if got := m.agent.Value(); got != "claude" {
		t.Errorf("agent kind = %q after a project change, want the user's own %q", got, "claude")
	}
	if !m.worktree.On() {
		t.Error("worktree toggle went off, want the user's own on")
	}
}

// TestDirResult_MemoryKeyIsTheRepoRootNotTheCheckout pins spec §10's key
// rule at the app layer: a linked worktree resolves to its origin's root,
// so both share ONE entry. gitx's own test proves RepoRoot really answers
// that against a real repository; this one proves the app asks it and keys
// the memory on the answer.
func TestDirResult_MemoryKeyIsTheRepoRootNotTheCheckout(t *testing.T) {
	git := newFakeGit()
	git.repoRoots = map[string]string{
		"/repo":                     "/repo",
		"/repo/.worktrees/feature":  "/repo",
		"/elsewhere/other-worktree": "/repo",
	}
	memory := memoryFor(map[string]config.ProjectDefaults{
		"/repo": {Kind: "codex", Worktree: ptrBool(false), Placement: "tab-here"},
	})

	for _, cwd := range []string{"/repo", "/repo/.worktrees/feature", "/elsewhere/other-worktree"} {
		m := memoryModel(t, cwd, memory, git)
		if got := m.projectKey; got != "/repo" {
			t.Errorf("with the project at %s, projectKey = %q, want the origin root %q", cwd, got, "/repo")
		}
		if got := m.agent.Value(); got != "codex" {
			t.Errorf("with the project at %s, agent kind = %q, want the origin's remembered %q", cwd, got, "codex")
		}
	}
}

// TestDirResult_NonRepoKeysOnTheCanonicalPath pins the other branch of the
// key rule: a plain directory has no repository root, and is remembered
// under its own canonical path instead.
func TestDirResult_NonRepoKeysOnTheCanonicalPath(t *testing.T) {
	git := newFakeGit()
	git.isGitRepo = false
	m := memoryModel(t, "/plain-dir", memoryFor(map[string]config.ProjectDefaults{
		"/plain-dir": {Kind: "codex", Placement: "tab-here"},
	}), git)

	if got := m.projectKey; got != "/plain-dir" {
		t.Fatalf("projectKey = %q, want the canonical path %q", got, "/plain-dir")
	}
	if got := m.agent.Value(); got != "codex" {
		t.Errorf("agent kind = %q, want the remembered %q", got, "codex")
	}
}

// TestDirResult_AMissingDirectoryIsNotRemembered pins that a path that is
// not there gets no key: there is nothing to remember about it, and keying
// on it would fill projects.json with typos.
func TestDirResult_AMissingDirectoryIsNotRemembered(t *testing.T) {
	git := newFakeGit()
	git.dirExists = false
	m := memoryModel(t, "/gone", config.Projects{}, git)
	if got := m.projectKey; got != "" {
		t.Errorf("projectKey = %q for a directory that does not exist, want %q", got, "")
	}
	if len(git.repoRootCalls) != 0 {
		t.Errorf("RepoRoot was called %d time(s) for a directory that does not exist, want none",
			len(git.repoRootCalls))
	}
}

// TestDirResult_AFailedRepoRootFallsBackToThePathKey pins the documented
// degradation: a repository whose root cannot be resolved still gets a
// stable key from its path, so it keeps a memory rather than losing one.
func TestDirResult_AFailedRepoRootFallsBackToThePathKey(t *testing.T) {
	git := newFakeGit()
	git.repoRootErr = errors.New("git: no such file or directory")
	m := memoryModel(t, "/repo-a", memoryFor(map[string]config.ProjectDefaults{
		"/repo-a": {Kind: "codex", Worktree: ptrBool(false), Placement: "tab-here"},
	}), git)

	if got := m.projectKey; got != "/repo-a" {
		t.Fatalf("projectKey = %q when RepoRoot failed, want the path key %q", got, "/repo-a")
	}
	if got := m.agent.Value(); got != "codex" {
		t.Errorf("agent kind = %q, want the remembered %q", got, "codex")
	}
}

// TestClearRequested_DoesNotReApplyProjectMemory pins spec §10's "⌃R⌃R
// clears back to the repository default" -- explicitly not back to what you
// last did in this project. Without the suppression the dir check the
// rebuilt form immediately schedules would put the memory straight back and
// the clear would look like it did nothing.
func TestClearRequested_DoesNotReApplyProjectMemory(t *testing.T) {
	m := memoryModel(t, "/repo-a", memoryFor(map[string]config.ProjectDefaults{
		"/repo-a": {Kind: "codex", Worktree: ptrBool(true), Placement: "tab-here"},
	}), nil)
	if got := m.agent.Value(); got != "codex" {
		t.Fatalf("setup: agent kind = %q, want the remembered %q", got, "codex")
	}

	fresh, cmd := m.handleClearRequested()
	fresh = pumpAsync(t, fresh, []tea.Cmd{cmd})

	// The rebuilt form's own dir check really did run and really did
	// resolve the same project -- without this the assertions below would
	// pass for the wrong reason.
	if got := fresh.projectKey; got != "/repo-a" {
		t.Fatalf("after the clear, projectKey = %q, want the dir check to have resolved %q", got, "/repo-a")
	}
	if got := fresh.agent.Value(); got == "codex" {
		t.Error("⌃R⌃R left the remembered agent kind in place, want a clear back to the configured default")
	}
	if fresh.worktree.On() {
		t.Error("⌃R⌃R left the remembered worktree toggle on, want a clear back to the configured default")
	}
}

// TestSubmit_RecordsProjectMemory is the write side of spec §10: a
// successful submit records what it launched with under this project's key,
// and last-used.json keeps being written alongside it as the global
// fallback tier.
func TestSubmit_RecordsProjectMemory(t *testing.T) {
	m := newSubmitTestModel(t, &submitFakeRunner{}, testSetup{
		Ctx:    herdrc.Context{WorkspaceCwd: "/repo"},
		Config: config.Config{Agents: config.AgentsConfig{Favorites: []string{"claude", "codex"}}},
	})
	stateDir := m.stateDir
	m.projectKey = "/repo"
	m.submitInput = plan.Input{
		ProjectDir:  "/repo",
		Title:       "Fix pagination",
		AgentKind:   "codex",
		Placement:   plan.PlacementTabHere,
		BaseRef:     "develop",
		UseWorktree: true,
	}

	_, cmd := m.handleSubmitDone(submitDoneMsg{result: plan.ExecResult{FailedIndex: -1}})
	if _, ok := cmd().(statePersistedMsg); !ok {
		t.Fatal("handleSubmitDone on success did not run the state write")
	}

	saved, _ := config.LoadProjects(stateDir)
	entry, ok := saved.Get("/repo")
	if !ok {
		t.Fatalf("projects.json has no entry for /repo: %+v", saved)
	}
	if entry.Kind != "codex" {
		t.Errorf("entry.Kind = %q, want %q", entry.Kind, "codex")
	}
	if entry.Placement != "tab-here" {
		t.Errorf("entry.Placement = %q, want %q", entry.Placement, "tab-here")
	}
	if entry.Worktree == nil || !*entry.Worktree {
		t.Errorf("entry.Worktree = %v, want a recorded true", entry.Worktree)
	}
	if entry.Base != "develop" {
		t.Errorf("entry.Base = %q, want %q", entry.Base, "develop")
	}
	if entry.Seen.IsZero() {
		t.Error("entry.Seen is zero; the cap's least-recently-seen eviction depends on it")
	}

	// last-used.json is untouched by this feature: it is now the global
	// fallback tier, not a thing that got replaced.
	global, _ := config.LoadState(stateDir)
	if global.LastKind != "codex" {
		t.Errorf("last-used.json LastKind = %q, want %q -- the global tier must keep being written", global.LastKind, "codex")
	}
}

// TestSubmit_WithoutAProjectKeyRecordsNoMemory pins the documented gap: a
// submit fired before the dir check has resolved a key has nowhere to
// record itself, and must not invent one. The global tier still records it.
func TestSubmit_WithoutAProjectKeyRecordsNoMemory(t *testing.T) {
	m := newSubmitTestModel(t, &submitFakeRunner{}, testSetup{Ctx: herdrc.Context{WorkspaceCwd: "/repo"}})
	stateDir := m.stateDir
	m.submitInput = plan.Input{ProjectDir: "/repo", AgentKind: "codex"}

	_, cmd := m.handleSubmitDone(submitDoneMsg{result: plan.ExecResult{FailedIndex: -1}})
	cmd()

	saved, _ := config.LoadProjects(stateDir)
	if len(saved.Entries) != 0 {
		t.Errorf("projects.json = %+v, want no entries without a resolved key", saved.Entries)
	}
	if global, _ := config.LoadState(stateDir); global.LastKind != "codex" {
		t.Errorf("last-used.json LastKind = %q, want %q", global.LastKind, "codex")
	}
}
