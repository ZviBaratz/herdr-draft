package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/defaults"
	"github.com/ZviBaratz/herdr-draft/internal/form"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
	"github.com/ZviBaratz/herdr-draft/internal/linear"
	"github.com/ZviBaratz/herdr-draft/internal/plan"
)

// This file covers spec §11's repo-level shared config as the app layer
// sees it: .herdr-draft.toml reaching the form through the SAME debounced,
// versioned dir check the validity and memory-key checks ride, re-read on
// every project change, and slotting between projects.json and
// last-used.json.
//
// internal/config owns the file and its trust model (repo_test.go there,
// one case per forbidden key); internal/defaults owns the precedence
// arithmetic (defaults_test.go there). What is left for this package is
// the wiring, and the two behaviors only it can express: the branch that
// linear_branch_name changes, and staleness.

// fakeRepoConfigs is a config.LoadRepoConfig stand-in: a fixed answer per
// repository root, plus the roots it was actually asked about, which is
// how the linked-worktree and re-read cases assert WHERE the file was
// looked for rather than only what came back.
type fakeRepoConfigs struct {
	byRoot map[string]config.RepoConfig
	roots  []string
}

func (f *fakeRepoConfigs) load(root string) config.RepoConfig {
	f.roots = append(f.roots, root)
	return f.byRoot[root]
}

// repoConfigModel builds a Model pointed at cwd whose repo configs come
// from a fake loader, with the initial dir check already settled -- the
// state a real form-open reaches once its first debounce round completes.
func repoConfigModel(t *testing.T, cwd string, s testSetup, byRoot map[string]config.RepoConfig) (Model, *fakeRepoConfigs) {
	t.Helper()
	repo := &fakeRepoConfigs{byRoot: byRoot}
	s.RepoConfig = repo.load
	s.Ctx = herdrc.Context{WorkspaceCwd: cwd}
	if s.Git == nil {
		s.Git = newFakeGit()
	}
	m := newTestModel(t, s)
	return pumpAsync(t, m, m.initCmds), repo
}

// TestRepoConfig_ReachesTheFormThroughTheDirCheck is the basic wiring: a
// committed file changes what the form opens on, and every value is
// attributed to the repo tier so a panel can say where it came from.
func TestRepoConfig_ReachesTheFormThroughTheDirCheck(t *testing.T) {
	m, repo := repoConfigModel(t, "/repo-a", testSetup{
		State: config.State{LastWorktree: ptrBool(true), LastPlacement: "new-space"},
	}, map[string]config.RepoConfig{
		"/repo-a": {
			BranchPrefix: "team/",
			// Off, so PlacementField stays live: a worktree makes it inert
			// and snaps it back to New space (spec §6 field 5), which
			// would hide the placement assertion below behind an unrelated
			// rule. That it beats last-used.json's true is the worktree
			// half of this case.
			DefaultWorktree:  ptrBool(false),
			DefaultPlacement: "split-here",
			DefaultBase:      "trunk",
		},
	})

	if len(repo.roots) == 0 || repo.roots[0] != "/repo-a" {
		t.Fatalf("repo config was looked for at %v, want the repository root %q", repo.roots, "/repo-a")
	}
	if m.worktree.On() {
		t.Error("worktree toggle is on, want the repository's committed false")
	}
	if got := m.placement.Value(); got != plan.PlacementSplitHere {
		t.Errorf("placement = %v, want the repository's split-here", got)
	}
	if got := m.resolved.BranchPrefix; got != "team/" {
		t.Errorf("resolved BranchPrefix = %q, want the repository's %q", got, "team/")
	}
	if got := m.resolved.BaseRef; got != "trunk" {
		t.Errorf("resolved BaseRef = %q, want the repository's %q", got, "trunk")
	}
	for _, field := range []string{
		defaults.FieldBranchPrefix, defaults.FieldWorktree,
		defaults.FieldPlacement, defaults.FieldBaseRef,
	} {
		if got := m.resolved.From[field]; got != defaults.TierRepoConfig {
			t.Errorf("From[%q] = %v, want %v", field, got, defaults.TierRepoConfig)
		}
	}
}

// TestRepoConfig_SitsBetweenProjectMemoryAndLastUsed is spec §10's
// ordering, exercised through the real form rather than through Resolve:
// the team's committed default outranks what you last did in some OTHER
// repository, and loses to what you last did in THIS one.
func TestRepoConfig_SitsBetweenProjectMemoryAndLastUsed(t *testing.T) {
	m, _ := repoConfigModel(t, "/repo-a", testSetup{
		// Tier 3: your last choice anywhere. Both of these must lose.
		State: config.State{LastPlacement: "new-space", LastWorktree: ptrBool(true)},
		// Tier 1: your last choice HERE -- placement only, so the repo's
		// worktree default still applies and the two tiers are told apart
		// within one run.
		Projects: memoryFor(map[string]config.ProjectDefaults{
			"/repo-a": {Placement: "tab-here"},
		}),
	}, map[string]config.RepoConfig{
		// Worktree off for the reason the case above records: PlacementField
		// is inert while a worktree is on, and this case is about placement.
		"/repo-a": {DefaultWorktree: ptrBool(false), DefaultPlacement: "split-here"},
	})

	if got := m.placement.Value(); got != plan.PlacementTabHere {
		t.Errorf("placement = %v, want projects.json's tab-here to beat the repo's split-here", got)
	}
	if got := m.resolved.From[defaults.FieldPlacement]; got != defaults.TierProjectMemory {
		t.Errorf("From[placement] = %v, want %v", got, defaults.TierProjectMemory)
	}
	if m.worktree.On() {
		t.Error("worktree toggle is on, want the repo's false to beat last-used.json's true")
	}
	if got := m.resolved.From[defaults.FieldWorktree]; got != defaults.TierRepoConfig {
		t.Errorf("From[worktree] = %v, want %v", got, defaults.TierRepoConfig)
	}
}

// TestRepoConfig_ReReadWhenTheProjectChanges: the file is a property of
// the project, so changing the project row asks again -- at the new
// project's own root -- and the answer replaces the previous one wholesale
// rather than merging with it.
func TestRepoConfig_ReReadWhenTheProjectChanges(t *testing.T) {
	m, repo := repoConfigModel(t, "/repo-a", testSetup{}, map[string]config.RepoConfig{
		"/repo-a": {DefaultPlacement: "split-here", DefaultBase: "trunk"},
		"/repo-b": {DefaultPlacement: "tab-here"},
	})
	if got := m.placement.Value(); got != plan.PlacementSplitHere {
		t.Fatalf("placement in /repo-a = %v, want split-here", got)
	}

	m = switchProject(t, m, "/repo-a", "/repo-b")

	if got := repo.roots[len(repo.roots)-1]; got != "/repo-b" {
		t.Errorf("last repo-config read was for %q, want the new project's root %q", got, "/repo-b")
	}
	if got := m.placement.Value(); got != plan.PlacementTabHere {
		t.Errorf("placement in /repo-b = %v, want the new repository's tab-here", got)
	}
	if got := m.resolved.BaseRef; got != "" {
		t.Errorf("resolved BaseRef = %q, want /repo-a's trunk gone rather than carried over", got)
	}
}

// TestRepoConfig_StaleResultIsDropped is the versioned half of the
// pattern. A result for a project the user has already left must apply
// nothing: it rides the dir check's own request/version guard, so the test
// synthesizes a result carrying a superseded version and asserts the form
// did not move.
func TestRepoConfig_StaleResultIsDropped(t *testing.T) {
	m, _ := repoConfigModel(t, "/repo-a", testSetup{}, map[string]config.RepoConfig{
		"/repo-a": {DefaultPlacement: "split-here"},
	})
	before := m.placement.Value()

	stale := dirResultMsg{
		req:        request{version: m.dirReqVersion - 1, key: "/repo-old"},
		dirExists:  true,
		isGitRepo:  true,
		memoryKey:  "/repo-old",
		repoConfig: config.RepoConfig{DefaultPlacement: "tab-here", Notes: []string{"ignoring palette: nope"}},
	}
	next, _ := m.Update(stale)
	m = next.(Model)

	if got := m.placement.Value(); got != before {
		t.Errorf("placement = %v after a stale result, want it left at %v", got, before)
	}
	if got := m.projectKey; got == "/repo-old" {
		t.Error("projectKey took the stale result's key")
	}
	if len(m.repoConfigNotes()) != 0 {
		t.Errorf("notes = %q, want a stale result's notes dropped too", m.repoConfigNotes())
	}

	// ...and the CURRENT version still applies, so the guard is rejecting
	// staleness rather than everything.
	fresh := stale
	fresh.req.version = m.dirReqVersion
	next, _ = m.Update(fresh)
	m = next.(Model)
	if got := m.placement.Value(); got != plan.PlacementTabHere {
		t.Errorf("placement = %v after a current result, want tab-here", got)
	}
}

// TestRepoConfig_ALinkedWorktreeAndItsOriginReadOneFile is why the root
// comes from gitx.RepoRoot rather than from the selected directory:
// RepoRoot derives from --git-common-dir, so every worktree of one
// repository resolves to the origin root and therefore reads the origin's
// committed file. A per-checkout read would give each worktree of a
// repository a different team default.
func TestRepoConfig_ALinkedWorktreeAndItsOriginReadOneFile(t *testing.T) {
	git := newFakeGit()
	git.repoRoots = map[string]string{
		"/origin":     "/origin",
		"/wt/feature": "/origin",
	}
	m, repo := repoConfigModel(t, "/origin", testSetup{Git: git}, map[string]config.RepoConfig{
		"/origin": {DefaultPlacement: "split-here"},
	})
	if got := m.placement.Value(); got != plan.PlacementSplitHere {
		t.Fatalf("placement in the origin = %v, want split-here", got)
	}

	m = switchProject(t, m, "/origin", "/wt/feature")

	if got := repo.roots[len(repo.roots)-1]; got != "/origin" {
		t.Errorf("the linked worktree read its config from %q, want the origin root %q", got, "/origin")
	}
	if got := m.placement.Value(); got != plan.PlacementSplitHere {
		t.Errorf("placement in the linked worktree = %v, want the origin's split-here", got)
	}
}

// TestRepoConfig_NonRepoProjectAsksForNothing: the file lives at a
// repository root, so a plain directory has nowhere for one to be. The
// loader is still called -- with "" -- rather than the call being skipped,
// so there is exactly one code path.
func TestRepoConfig_NonRepoProjectAsksForNothing(t *testing.T) {
	git := newFakeGit()
	git.isGitRepo = false
	_, repo := repoConfigModel(t, "/plain-dir", testSetup{Git: git}, map[string]config.RepoConfig{
		"/plain-dir": {DefaultPlacement: "split-here"},
	})

	for _, root := range repo.roots {
		if root != "" {
			t.Errorf("repo config was looked for at %q for a non-repository, want no root", root)
		}
	}
}

// --- linear_branch_name ---------------------------------------------------

// chosenIssue is the Linear selection the two cases below share.
var chosenIssue = &linear.Issue{
	Identifier: "ENG-9", Title: "Fix login redirect loop", BranchName: "eng-9-fix-login-redirect-loop",
}

// TestRepoConfig_LinearBranchNameOffDerivesTheBranchFromTheTitle pins this
// package's reading of spec §11's newest key. The spec names it and its
// default but not what FALSE does; the answer implemented here is "the
// branch is derived from the title with the resolved prefix, exactly as in
// manual mode", so a repository with its own branch naming can keep it
// while still seeding title and prompt from Linear.
func TestRepoConfig_LinearBranchNameOffDerivesTheBranchFromTheTitle(t *testing.T) {
	m, _ := repoConfigModel(t, "/repo-a", testSetup{
		Linear: &fakeLinear{},
		Config: config.Config{BranchPrefix: "zvi/"},
	}, map[string]config.RepoConfig{
		"/repo-a": {BranchPrefix: "team/", LinearBranchName: ptrBool(false)},
	})

	next, _ := m.Update(form.IssueChosenMsg{Issue: chosenIssue})
	m = next.(Model)

	if got := m.title.Value(); got != chosenIssue.Title {
		t.Errorf("Title = %q, want the issue's own title -- only the BRANCH is repo-controlled", got)
	}
	want := "team/fix-login-redirect-loop"
	if got := m.worktree.Branch(); got != want {
		t.Errorf("Branch = %q, want the title-derived %q", got, want)
	}
}

// TestRepoConfig_LinearBranchNameDefaultsToTheIssueBranch is the
// regression guard on the other side: with no repo config, or one that
// leaves the key alone, the issue's own branchName still owns the branch,
// which is what the form has always done.
func TestRepoConfig_LinearBranchNameDefaultsToTheIssueBranch(t *testing.T) {
	m, _ := repoConfigModel(t, "/repo-a", testSetup{
		Linear: &fakeLinear{},
		Config: config.Config{BranchPrefix: "zvi/"},
	}, map[string]config.RepoConfig{
		"/repo-a": {BranchPrefix: "team/"},
	})

	next, _ := m.Update(form.IssueChosenMsg{Issue: chosenIssue})
	m = next.(Model)

	if got := m.worktree.Branch(); got != chosenIssue.BranchName {
		t.Errorf("Branch = %q, want the issue's own %q", got, chosenIssue.BranchName)
	}
}

// TestRepoConfig_SwitchingProjectsReDerivesTheBranch: linear_branch_name
// and branch_prefix are both per-repository now, so the branch a chosen
// issue produces is a question the project row can re-open. It is applied
// SEEDED, so a branch the user typed themselves still stands.
func TestRepoConfig_SwitchingProjectsReDerivesTheBranch(t *testing.T) {
	m, _ := repoConfigModel(t, "/repo-a", testSetup{
		Linear: &fakeLinear{},
		Config: config.Config{BranchPrefix: "zvi/"},
	}, map[string]config.RepoConfig{
		"/repo-a": {},
		"/repo-b": {BranchPrefix: "team/", LinearBranchName: ptrBool(false)},
	})

	next, _ := m.Update(form.IssueChosenMsg{Issue: chosenIssue})
	m = next.(Model)
	if got := m.worktree.Branch(); got != chosenIssue.BranchName {
		t.Fatalf("Branch in /repo-a = %q, want the issue's own %q", got, chosenIssue.BranchName)
	}

	m = switchProject(t, m, "/repo-a", "/repo-b")

	want := "team/fix-login-redirect-loop"
	if got := m.worktree.Branch(); got != want {
		t.Errorf("Branch in /repo-b = %q, want it re-derived as %q", got, want)
	}
}

// TestRepoConfig_AnEmptyTitleStillProducesNoBranch guards the trap in
// re-deriving the branch on a project change: gitx.BranchSlug answers an
// empty title with a deterministic "session-xxxxxxxx" rather than with
// nothing, so an unguarded re-derivation would fill the branch input with
// a hash the moment the form opened -- before the user had typed a
// character.
func TestRepoConfig_AnEmptyTitleStillProducesNoBranch(t *testing.T) {
	m, _ := repoConfigModel(t, "/repo-a", testSetup{}, map[string]config.RepoConfig{
		"/repo-a": {BranchPrefix: "team/"},
	})

	if got := m.worktree.Branch(); got != "" {
		t.Errorf("Branch = %q on a freshly opened form, want empty", got)
	}
}

// TestRepoConfig_ATypedBranchSurvivesAProjectChange is the other half of
// that: re-deriving must never overwrite what the user typed.
func TestRepoConfig_ATypedBranchSurvivesAProjectChange(t *testing.T) {
	m, _ := repoConfigModel(t, "/repo-a", testSetup{
		Config: config.Config{BranchPrefix: "zvi/"},
	}, map[string]config.RepoConfig{
		"/repo-b": {BranchPrefix: "team/"},
	})
	m.title.SetTitle("Fix login redirect loop", false)
	m.reactToChanges()

	// Typed, not SetBranch(_, false): a hard set CLEARS branchTouched, so
	// it would prove the opposite of what this case is about. See
	// TestIssueSelectionSeedsAndRespectsTouchedBranch on the ↓ that puts
	// the focus on the branch input.
	m.worktree.SetOn(true)
	m.worktree.Focus()
	m.worktree.Update(key(tea.KeyDown, 0))
	m.worktree.Update(rn('!'))
	typed := m.worktree.Branch()

	m = switchProject(t, m, "/repo-a", "/repo-b")

	if got := m.worktree.Branch(); got != typed {
		t.Errorf("Branch = %q, want the user's own %q left alone", got, typed)
	}
}

// TestRepoConfig_RejectedBranchPrefixFallsBackToTheUsersOwn is the
// end-to-end of the fallback config.Load does NOT do: an unusable
// repo-supplied prefix lands on the user's configured value, not on the
// built-in default, because the repo tier simply supplies nothing and the
// tier below is config.toml.
func TestRepoConfig_RejectedBranchPrefixFallsBackToTheUsersOwn(t *testing.T) {
	// What config.LoadRepoConfig hands back for a rejected prefix: the
	// value dropped, the reason on Notes.
	m, _ := repoConfigModel(t, "/repo-a", testSetup{
		Config: config.Config{BranchPrefix: "zvi/"},
	}, map[string]config.RepoConfig{
		"/repo-a": {Notes: []string{`ignoring branch_prefix "-x": starts with "-"; using your own configured prefix`}},
	})

	m.title.SetTitle("Fix login redirect loop", false)
	m.reactToChanges()

	if got := m.worktree.Branch(); got != "zvi/fix-login-redirect-loop" {
		t.Errorf("Branch = %q, want the USER's prefix %q applied", got, "zvi/")
	}
	if got := m.resolved.From[defaults.FieldBranchPrefix]; got != defaults.TierUserConfig {
		t.Errorf("From[branch_prefix] = %v, want %v", got, defaults.TierUserConfig)
	}
	if len(m.repoConfigNotes()) != 1 {
		t.Errorf("notes = %q, want the rejection reported", m.repoConfigNotes())
	}
}

// TestRepoConfig_NotesReachTheModelAndFollowTheProject: the report is
// spec §11's "ignored with a visible note", and it is per-project like
// everything else in the file, so leaving a repository must take its notes
// with it rather than leave them attributed to the next one.
//
// The notes are NOT yet rendered -- see Model.repoConfig's own doc comment
// -- so this asserts the value a view will read.
func TestRepoConfig_NotesReachTheModelAndFollowTheProject(t *testing.T) {
	m, _ := repoConfigModel(t, "/repo-a", testSetup{}, map[string]config.RepoConfig{
		"/repo-a": {Notes: []string{"ignoring agents.extra_args: it becomes part of a launched agent's command line"}},
		"/repo-b": {},
	})

	notes := m.repoConfigNotes()
	if len(notes) != 1 || !strings.Contains(notes[0], "agents.extra_args") {
		t.Fatalf("notes = %q, want one naming agents.extra_args", notes)
	}

	m = switchProject(t, m, "/repo-a", "/repo-b")
	if got := m.repoConfigNotes(); len(got) != 0 {
		t.Errorf("notes = %q after switching to a repository with none, want empty", got)
	}
}

// TestRepoConfig_MalformedFileDoesNotBlockTheForm: config.LoadRepoConfig
// answers a broken file with notes and no values, and the form must go on
// working off the tiers below -- a teammate can break this file for
// everyone at once, so it must never be able to stop the plugin.
func TestRepoConfig_MalformedFileDoesNotBlockTheForm(t *testing.T) {
	m, _ := repoConfigModel(t, "/repo-a", testSetup{
		State: config.State{LastPlacement: "tab-here"},
	}, map[string]config.RepoConfig{
		"/repo-a": {Notes: []string{"ignoring .herdr-draft.toml: expected a value"}},
	})

	if got := m.placement.Value(); got != plan.PlacementTabHere {
		t.Errorf("placement = %v, want last-used.json's tab-here to still apply", got)
	}
	if m.dirInvalid {
		t.Error("dirInvalid is set, want a malformed repo config to leave the project valid")
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if got := next.(Model).View().Content; got == "" {
		t.Error("the form renders nothing, want a malformed repo config never to block it")
	}
}

// TestRepoConfig_ProductionLoaderReadsTheFile closes the one gap the fake
// loader leaves: that Deps.RepoConfig defaulting to config.LoadRepoConfig
// is actually wired, at the actual path, off the actual repository root.
// It is the only test in this package that puts a real file on disk.
func TestRepoConfig_ProductionLoaderReadsTheFile(t *testing.T) {
	root := t.TempDir()
	body := "default_placement = \"split-here\"\n\n[palette]\naccent = \"#ff0000\"\n"
	if err := os.WriteFile(filepath.Join(root, config.RepoConfigFileName), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// RepoConfig left nil, so Deps falls through to config.LoadRepoConfig.
	m := newTestModel(t, testSetup{Ctx: herdrc.Context{WorkspaceCwd: root}})
	m = pumpAsync(t, m, m.initCmds)

	if got := m.placement.Value(); got != plan.PlacementSplitHere {
		t.Errorf("placement = %v, want the committed split-here", got)
	}
	notes := m.repoConfigNotes()
	if len(notes) != 1 || !strings.Contains(notes[0], "palette") {
		t.Errorf("notes = %q, want the forbidden [palette] table reported", notes)
	}
}
