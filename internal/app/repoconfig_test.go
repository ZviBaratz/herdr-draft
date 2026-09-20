package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/defaults"
	"github.com/ZviBaratz/herdr-draft/internal/form"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
	"github.com/ZviBaratz/herdr-draft/internal/linear"
	"github.com/ZviBaratz/herdr-draft/internal/plan"
)

// This file covers v2 spec §11's repo-level shared config as the app layer
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

// provenanceLine is v2 spec §11's own wording, and the string every
// display assertion below looks for.
const provenanceLine = "from .herdr-draft.toml"

// focusedFrame renders the real form with section id focused, ANSI
// stripped -- the panel assertions below go through the SAME path a user
// sees rather than reaching into a field, since "the value is reachable"
// is precisely what #8 already had and #15 exists to finish.
func focusedFrame(t *testing.T, m Model, id string) string {
	t.Helper()
	m.form.FocusByID(id)
	if got := m.form.FocusedID(); got != id {
		t.Fatalf("focus is on %q, want %q (sections: %v)", got, id, m.form.SectionIDs())
	}
	return ansi.Strip(m.form.ViewAt(80, 24))
}

// TestRepoConfig_EachAttributedKeyEarnsItsOwnPanelsLine is v2 spec §11's
// display half: a value the repository's committed file chose says so, in
// the panel of the field that shows it, and nowhere else.
//
// ONE KEY PER CASE, and that is the whole difference from what this used
// to be. It set default_worktree, default_placement and default_base at
// once and asserted the line on both panels, which three keys can satisfy
// between them: the worktree toggle's attribution alone earned the
// worktree panel's line, so deleting the base from repoProvenance's
// arguments left the test green. That is #93's exact symptom, and this
// test was the one that should have caught it.
//
// The two unattributed keys are cases here too, because "nowhere else" is
// a claim about them specifically: branch_prefix and linear_branch_name
// shape the branch this package DERIVES rather than a value of their own,
// and showRepoConfig's doc comment says why neither may be credited.
func TestRepoConfig_EachAttributedKeyEarnsItsOwnPanelsLine(t *testing.T) {
	// Every case keeps the worktree OFF, which is what keeps PlacementField
	// live and therefore focusable (placement spec §14). A default_worktree
	// of false is also a value the file supplied, so the one case that sets
	// it is not a special arrangement -- it is that case's subject.
	for _, tc := range []struct {
		key string
		rc  config.RepoConfig
		// panel is the section id whose panel must carry the line. "" is
		// "no panel may", which is the claim for the two derived keys.
		panel string
	}{
		{key: "default_worktree", rc: config.RepoConfig{DefaultWorktree: ptrBool(false)}, panel: "worktree"},
		{key: "default_base", rc: config.RepoConfig{DefaultBase: "trunk"}, panel: "worktree"},
		{key: "default_placement", rc: config.RepoConfig{DefaultPlacement: "split-here"}, panel: "placement"},
		{key: "branch_prefix", rc: config.RepoConfig{BranchPrefix: "team/"}},
		{key: "linear_branch_name", rc: config.RepoConfig{LinearBranchName: ptrBool(false)}},
	} {
		t.Run(tc.key, func(t *testing.T) {
			// #194's settle keeps a tier's base only when git names a commit
			// for it. Harmless for the cases that set no base.
			git := newFakeGit()
			git.commits = map[string]string{"/repo-a trunk": "4e5f607"}
			m, _ := repoConfigModel(t, "/repo-a", testSetup{Git: git},
				map[string]config.RepoConfig{"/repo-a": tc.rc})

			// Never only the panel under test: a "render it whenever there
			// is a repo config" implementation passes the positive half and
			// fails here. The title panel shows nothing the file can set and
			// stands in for every row that is not one of the three.
			for _, id := range []string{"worktree", "placement", "title"} {
				frame := focusedFrame(t, m, id)
				switch {
				case id == tc.panel && !strings.Contains(frame, provenanceLine):
					t.Errorf("%s: with %q focused the frame does not say %q:\n%s",
						tc.key, id, provenanceLine, frame)
				case id != tc.panel && strings.Contains(frame, provenanceLine):
					t.Errorf("%s: with %q focused the frame claims %q, which belongs to %q:\n%s",
						tc.key, id, provenanceLine, tc.panel, frame)
				}
			}
		})
	}
}

// TestRepoConfig_ALowerTierIsNotAttributedToTheRepo is the other half of
// the same requirement, and the one a "render it whenever there is a repo
// config" implementation would fail: the SAME placement, resolved from
// last-used.json instead, is nobody's committed default.
func TestRepoConfig_ALowerTierIsNotAttributedToTheRepo(t *testing.T) {
	m, _ := repoConfigModel(t, "/repo-a", testSetup{
		State: config.State{LastPlacement: "split-here", LastWorktree: ptrBool(false)},
	}, map[string]config.RepoConfig{"/repo-a": {}})

	if got := m.placement.Value(); got != plan.PlacementSplitHere {
		t.Fatalf("placement = %v, want last-used.json's split-here (the value under test)", got)
	}
	if got := m.resolved.From[defaults.FieldPlacement]; got != defaults.TierGlobalMemory {
		t.Fatalf("From[placement] = %v, want %v", got, defaults.TierGlobalMemory)
	}
	for _, id := range []string{"placement", "worktree"} {
		if frame := focusedFrame(t, m, id); strings.Contains(frame, provenanceLine) {
			t.Errorf("with %q focused the frame claims %q for a last-used.json value:\n%s", id, provenanceLine, frame)
		}
	}
}

// TestRepoConfig_TouchingAValueRetiresItsProvenance: once the user moves a
// chip, what the panel shows is theirs, and a line still crediting the
// repository would be false. The touched flags v2 spec §10 already keeps are
// what this rides -- the same ones that stop per-project memory re-applying.
func TestRepoConfig_TouchingAValueRetiresItsProvenance(t *testing.T) {
	m, _ := repoConfigModel(t, "/repo-a", testSetup{}, map[string]config.RepoConfig{
		"/repo-a": {DefaultWorktree: ptrBool(false), DefaultPlacement: "split-here"},
	})
	if frame := focusedFrame(t, m, "placement"); !strings.Contains(frame, provenanceLine) {
		t.Fatalf("the repository's placement is not attributed to start with:\n%s", frame)
	}

	m.form.FocusByID("placement")
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m = next.(Model)

	if !m.placementTouched {
		t.Fatalf("placement is not marked touched after a Right; this test cannot say anything")
	}
	if frame := focusedFrame(t, m, "placement"); strings.Contains(frame, provenanceLine) {
		t.Errorf("the panel still credits the repository for a value the user moved:\n%s", frame)
	}
}

// TestRepoConfig_ReachesTheFormThroughTheDirCheck is the basic wiring: a
// committed file changes what the form opens on, and every value is
// attributed to the repo tier so a panel can say where it came from.
func TestRepoConfig_ReachesTheFormThroughTheDirCheck(t *testing.T) {
	// trunk exists, or #194's check would drop it back to HEAD.
	git := newFakeGit()
	git.commits = map[string]string{"/repo-a trunk": "4e5f607"}
	m, repo := repoConfigModel(t, "/repo-a", testSetup{
		Git:   git,
		State: config.State{LastWorktree: ptrBool(true), LastPlacement: "new-space"},
	}, map[string]config.RepoConfig{
		"/repo-a": {
			BranchPrefix: "team/",
			// Off, asserted below in its own right as the worktree half of
			// this case (that it beats last-used.json's true). It is also
			// what keeps the placement row live for the provenance
			// assertion: under a worktree the row is inert (placement spec
			// §14), though the chip underneath still holds the value.
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

// TestRepoConfig_SitsBetweenProjectMemoryAndLastUsed is v2 spec §10's
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
		// Worktree off for the reason the case above records: the chip
		// holds the value either way, but only a live row shows it
		// (placement spec §14).
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
// package's reading of v2 spec §11's newest key. The spec names it and its
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

// TestRepoConfig_NotesReachTheViewAndFollowTheProject: the report is
// v2 spec §11's "ignored with a visible note", and it is per-project like
// everything else in the file, so leaving a repository must take its notes
// with it rather than leave them attributed to the next one.
//
// The note lands on the PROJECT panel (showRepoConfig): the file is a
// property of the repository the project row chose, not of any one value.
// This asserts both what the model holds and what the user actually reads,
// because the model half alone is what shipped in #8 and it was invisible.
func TestRepoConfig_NotesReachTheViewAndFollowTheProject(t *testing.T) {
	m, _ := repoConfigModel(t, "/repo-a", testSetup{}, map[string]config.RepoConfig{
		"/repo-a": {Notes: []string{"ignoring agents.extra_args: it becomes part of a launched agent's command line"}},
		"/repo-b": {},
	})

	notes := m.repoConfigNotes()
	if len(notes) != 1 || !strings.Contains(notes[0], "agents.extra_args") {
		t.Fatalf("notes = %q, want one naming agents.extra_args", notes)
	}
	frame := focusedFrame(t, m, "dir")
	if !strings.Contains(frame, "ignoring agents.extra_args") {
		t.Errorf("the project panel does not name the ignored key:\n%s", frame)
	}
	// The reason travels with the key. Only its head is asserted: a note
	// longer than the panel is wide elides at its tail with a visible
	// marker (form's keepHead), which is the honest rendering rather than a
	// silent clip.
	if !strings.Contains(frame, "ignoring agents.extra_args: it becomes part of") {
		t.Errorf("the project panel names the key without the reason:\n%s", frame)
	}

	m = switchProject(t, m, "/repo-a", "/repo-b")
	if got := m.repoConfigNotes(); len(got) != 0 {
		t.Errorf("notes = %q after switching to a repository with none, want empty", got)
	}
	if frame := focusedFrame(t, m, "dir"); strings.Contains(frame, "ignoring agents.extra_args") {
		t.Errorf("the previous repository's note followed the form to the next one:\n%s", frame)
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
	// "Never blocks" is not the same as "never mentioned": a file someone
	// wrote and expects to work reports the same way an ignored key does,
	// on the project panel (v2 spec §11).
	if frame := focusedFrame(t, m, "dir"); !strings.Contains(frame, "ignoring .herdr-draft.toml: expected a value") {
		t.Errorf("the project panel does not report the malformed file:\n%s", frame)
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

// issue93RepoConfig is the committed file #93 was found against, byte for
// byte, and keeping it verbatim is this test's whole point. The probe that
// nearly closed the issue injected a config.RepoConfig struct directly, so
// it exercised neither of the two things the live file also did: a
// branch_prefix gitx.ValidateBranchPrefix REFUSES, which reaches the form
// only through LoadRepoConfig, and a forbidden table. Both produce a note,
// and an unexercised path that puts text on a panel was a live reason not
// to close the issue on a probe that skipped it.
//
// What the check ESTABLISHED, rather than assumed, is that neither can
// reach this panel: both notes go to the PROJECT panel (showRepoConfig ->
// m.dir.SetNotes), and the worktree panel's own notes are
// branchPrefixNotes -- about the user's config.toml, not this file -- plus
// baseNote. Read the two halves in that order: the fixture stays verbatim
// because the path had not been run, not because the mechanism was ever
// likely.
const issue93RepoConfig = `default_base = "develop"
branch_prefix = "a b/"

[agents.extra_args]
claude = ["--dangerously-skip-permissions"]
`

// TestRepoConfig_OnlyTheBaseEarnsTheWorktreePanelsLine is #93: a committed
// file whose ONLY applied value is default_base still says so, on the
// worktree panel, through the production loader.
//
// Three things it does that the existing coverage does not, each of which
// is a way the issue survived:
//
//   - It goes through config.LoadRepoConfig on a real file rather than a
//     fake, so the refused branch_prefix and the forbidden table travel
//     with the base the way they did live; a struct injected straight into
//     Deps.RepoConfig carries neither.
//   - The base is the only value the file supplies, so no other
//     attribution can satisfy the assertion on its behalf -- which is what
//     TestRepoConfig_EachAttributedKeyEarnsItsOwnPanelsLine's predecessor,
//     with its three keys at once, could not rule out.
//   - It reads the panel one routed message AFTER the pipelines settle,
//     because noteUserEdits runs on the next message rather than at the end
//     of a pipeline. Live, the line was there at open and gone one
//     keystroke later.
//
// The mechanism it guards is #248's: a held base landing from the branch
// list read as a user edit, setting baseTouched with nobody having touched
// anything, which is exactly what fromRepoConfig withdraws the line on.
//
// FOUR ORDERINGS, and only the third can fail. They are not decoration:
// which of them a real form takes is not this package's choice, and three
// of the four were measured rather than reasoned about, because the first
// draft of this table described one of them wrongly. Every state below was
// printed from the real Model, message by message.
func TestRepoConfig_OnlyTheBaseEarnsTheWorktreePanelsLine(t *testing.T) {
	for _, tc := range []struct {
		name     string
		branches []string
		// holdSettle keeps #194's base settle OUT, which is the window #248
		// lived in: the settle is the OTHER path that calls
		// snapshotAppliedDefaults, so letting it answer re-records the base
		// and hides the defect. With it held, the snapshot taken at the end
		// of applyProjectDefaults is the only one there is -- and whether
		// that one recorded the ref the app asked for or the HEAD row the
		// picker was showing is the whole of the fix.
		holdSettle bool
		// viaSwitch opens the form on a DIFFERENT repository and moves the
		// project row to this one, which is the only way to reach
		// applyProjectDefaults with a branch list already installed.
		viaSwitch bool
	}{
		// At OPEN the picker is empty, so SetBase holds the ref whatever
		// the list will say, and the list landing is what selects it.
		// Measured: after dirResultMsg, Base()="" with
		// RequestedBase()="develop"; after baseResultMsg, both "develop".
		// The settle then re-records it, which is what makes this case
		// unable to catch #248 and makes the third case necessary.
		{name: "the branch list lands the held base", branches: []string{"main", "develop"}},
		// No list at all, so nothing lands it there and #194's SETTLE does
		// instead -- handleBaseSettled's tier branch puts it on offer and
		// selects it. A genuinely different landing path, and the one a
		// base that is real but older than the 50 refs the picker lists
		// arrives by. Measured: Base() stays "" until the settle answers.
		{name: "the settle offers a base the list never names"},
		// #248's own window, and the ordering this test would otherwise be
		// unable to fail in: the list lands the held ref with the settle,
		// the other snapshotter, still out. The list has to name the base.
		// With the case above's empty list the settle is the only thing
		// that could land the ref, so holding it holds the landing too and
		// there is nothing to misread.
		{
			name:       "the list lands the held base while the settle is still out",
			branches:   []string{"main", "develop"},
			holdSettle: true,
		},
		// The one ordering where the ref is never held: a project SWITCH
		// runs applyProjectDefaults while the previous project's list is
		// still in the picker, so SetBase selects outright. Measured:
		// Base()="develop" already at dirResultMsg. Worth its own case
		// because it is the path #248's defect lived on, and because "the
		// ref is always held at first" is true only of a form OPEN -- which
		// is exactly the assumption that made this table's first draft
		// describe the case above wrongly.
		{
			name:      "a project switch selects the base outright",
			branches:  []string{"main", "develop"},
			viaSwitch: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, config.RepoConfigFileName),
				[]byte(issue93RepoConfig), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}

			git := newFakeGit()
			git.listBranchesResult = tc.branches
			git.currentBranchResult = "main"
			// #194's settle keeps a tier's base only when git names a commit
			// for it; without this the base falls back to HEAD and there is
			// no attribution left to assert. The switch case is asked the
			// same question about the repository it starts in.
			git.commits = map[string]string{root + " develop": "9f8e7d6"}

			// Where the form OPENS. For the switch case that is a second
			// repository with no committed file of its own, so the base
			// under test can only have come from this one.
			opensOn := root
			if tc.viaSwitch {
				opensOn = t.TempDir()
				git.commits[opensOn+" develop"] = "9f8e7d6"
			}

			// RepoConfig left nil, so Deps falls through to the production
			// config.LoadRepoConfig -- the whole reason this fixture is a
			// file rather than a struct.
			m := newTestModel(t, testSetup{
				Git: git,
				Ctx: herdrc.Context{WorkspaceCwd: opensOn},
				// The user's own prefix, so the refused one has somewhere to
				// fall back TO that is not the built-in; and the worktree on,
				// which config.defaults() gives every real config.toml and a
				// literal here does not. Without it the panel renders its
				// off shape, which is not the screen #93 reported.
				Config: config.Config{BranchPrefix: "zvi/", DefaultWorktree: true},
			})
			switch {
			case tc.holdSettle:
				var held []tea.Msg
				m, held = pumpHoldingBaseChecks(t, m, m.initCmds)
				if len(held) != 1 {
					t.Fatalf("base settles held = %d, want this project's one", len(held))
				}
			case tc.viaSwitch:
				m = pumpAsync(t, m, m.initCmds)
				if m.worktree.Base() != "" {
					t.Fatalf("setup: the form opened with Base() = %q, want the other repository to supply none",
						m.worktree.Base())
				}
				m = switchProject(t, m, opensOn, root)
			default:
				m = pumpAsync(t, m, m.initCmds)
			}

			// One real keystroke through the real routing, which is where
			// noteUserEdits runs. The title is focused at open.
			next, cmd := m.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
			m = pumpAsync(t, next.(Model), []tea.Cmd{cmd})

			if got := m.resolved.BaseRef; got != "develop" {
				t.Fatalf("resolved BaseRef = %q, want the committed %q", got, "develop")
			}
			if got := m.resolved.From[defaults.FieldBaseRef]; got != defaults.TierRepoConfig {
				t.Fatalf("From[base_ref] = %v, want %v", got, defaults.TierRepoConfig)
			}
			if got := m.worktree.Base(); got != "develop" {
				t.Errorf("worktree Base() = %q, want the committed %q on the row", got, "develop")
			}
			// The flag the line hangs off, asserted in its own right: a
			// failure here names the mechanism, where the frame assertion
			// below only names the symptom.
			if m.baseTouched {
				t.Error("baseTouched is set with nobody having touched the base (#248's shape)")
			}
			if frame := focusedFrame(t, m, "worktree"); !strings.Contains(frame, provenanceLine) {
				t.Errorf("the worktree panel does not say %q for a base only the committed file supplied:\n%s",
					provenanceLine, frame)
			}

			// The rest of the live file, so a future change that keeps the
			// line by dropping one of its neighbours is not green here. The
			// refused prefix falls back to the USER's own rather than to the
			// built-in, and both rejections are reported on the project
			// panel.
			if got := m.resolved.BranchPrefix; got != "zvi/" {
				t.Errorf("resolved BranchPrefix = %q, want the user's own %q", got, "zvi/")
			}
			if got := m.resolved.From[defaults.FieldBranchPrefix]; got != defaults.TierUserConfig {
				t.Errorf("From[branch_prefix] = %v, want %v", got, defaults.TierUserConfig)
			}
			dirFrame := focusedFrame(t, m, "dir")
			for _, want := range []string{
				"ignoring agents.extra_args",
				`ignoring branch_prefix "a b/"`,
			} {
				if !strings.Contains(dirFrame, want) {
					t.Errorf("the project panel does not report %q:\n%s", want, dirFrame)
				}
			}
		})
	}
}
