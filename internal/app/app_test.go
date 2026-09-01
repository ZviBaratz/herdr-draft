package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ZviBaratz/herdr-draft/internal/clauth"
	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/form"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
	"github.com/ZviBaratz/herdr-draft/internal/linear"
	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// --- test doubles ---------------------------------------------------------
//
// Every collaborator Model needs is a fake here -- no real herdr/git/
// network call is ever made by this package's own tests.

// fakeRunner implements herdrc.Runner. Only WorkspaceList is exercised by
// Task 20 (the reachability probe plus the candidate/dup-check source);
// every other method is a trivial stub satisfying the interface.
type fakeRunner struct {
	workspaces []herdrc.WorkspaceInfo
	listErr    error
	listCalls  int
}

var _ herdrc.Runner = (*fakeRunner)(nil)

func (r *fakeRunner) WorkspaceList(context.Context) ([]herdrc.WorkspaceInfo, error) {
	r.listCalls++
	if r.listErr != nil {
		return nil, r.listErr
	}
	return r.workspaces, nil
}

func (r *fakeRunner) WorktreeCreate(context.Context, herdrc.WorktreeCreateReq) (herdrc.CreatedTopology, error) {
	return herdrc.CreatedTopology{}, nil
}
func (r *fakeRunner) WorkspaceCreate(context.Context, herdrc.WorkspaceCreateReq) (herdrc.CreatedTopology, error) {
	return herdrc.CreatedTopology{}, nil
}
func (r *fakeRunner) TabCreate(context.Context, herdrc.TabCreateReq) (herdrc.CreatedTopology, error) {
	return herdrc.CreatedTopology{}, nil
}
func (r *fakeRunner) PaneSplit(context.Context, herdrc.PaneSplitReq) (herdrc.CreatedTopology, error) {
	return herdrc.CreatedTopology{}, nil
}
func (r *fakeRunner) AgentStart(context.Context, herdrc.AgentStartReq) error      { return nil }
func (r *fakeRunner) AgentPrompt(context.Context, herdrc.AgentPromptReq) error    { return nil }
func (r *fakeRunner) AgentRead(context.Context, string) (string, error)           { return "", nil }
func (r *fakeRunner) AwaitDetection(context.Context, string, time.Duration) error { return nil }
func (r *fakeRunner) PaneRun(context.Context, string, []string) error             { return nil }
func (r *fakeRunner) WorktreeRemove(context.Context, string) error                { return nil }
func (r *fakeRunner) WorkspaceClose(context.Context, string) error                { return nil }

// fakeGit implements gitSource, defaulting to "a usable git repo with no
// duplicate branch" so most tests only need to override what they care
// about.
type fakeGit struct {
	dirExists, isGitRepo, branchExists bool
	listBranchesResult                 []string
	listBranchesErr                    error
	currentBranchResult                string
	currentBranchErr                   error
	fetchPruneErr                      error

	dirExistsCalls, isGitRepoCalls, listBranchesCalls, branchExistsCalls int
	currentBranchCalls                                                   int
	fetchPruneCalls                                                      []string
	// dirsSeen records every directory path handed to a git/filesystem
	// call, so a test can assert what the app layer actually passed
	// through (e.g. that a "~/..." project directory was expanded first).
	dirsSeen []string
}

var _ gitSource = (*fakeGit)(nil)

func newFakeGit() *fakeGit {
	return &fakeGit{dirExists: true, isGitRepo: true}
}

func (g *fakeGit) DirExists(dir string) bool {
	g.dirExistsCalls++
	g.dirsSeen = append(g.dirsSeen, dir)
	return g.dirExists
}
func (g *fakeGit) IsGitRepo(dir string) bool {
	g.isGitRepoCalls++
	g.dirsSeen = append(g.dirsSeen, dir)
	return g.isGitRepo
}
func (g *fakeGit) ListBranches(_ context.Context, dir string, _ int) ([]string, error) {
	g.listBranchesCalls++
	g.dirsSeen = append(g.dirsSeen, dir)
	return g.listBranchesResult, g.listBranchesErr
}
func (g *fakeGit) BranchExists(_ context.Context, dir, _ string) (bool, error) {
	g.branchExistsCalls++
	g.dirsSeen = append(g.dirsSeen, dir)
	return g.branchExists, nil
}
func (g *fakeGit) CurrentBranch(_ context.Context, dir string) (string, error) {
	g.currentBranchCalls++
	g.dirsSeen = append(g.dirsSeen, dir)
	return g.currentBranchResult, g.currentBranchErr
}
func (g *fakeGit) FetchPrune(_ context.Context, dir string) error {
	g.fetchPruneCalls = append(g.fetchPruneCalls, dir)
	return g.fetchPruneErr
}

type fakeLinear struct {
	issues []linear.Issue
	err    error
	calls  int
}

var _ linearSource = (*fakeLinear)(nil)

func (f *fakeLinear) AssignedIssues(context.Context) ([]linear.Issue, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.issues, nil
}

type fakeClauth struct {
	status clauth.Status
	err    error
	calls  int
}

var _ clauthSource = (*fakeClauth)(nil)

func (f *fakeClauth) Status(context.Context) (clauth.Status, error) {
	f.calls++
	if f.err != nil {
		return clauth.Status{}, f.err
	}
	return f.status, nil
}

// noSleep is the injectable-clock fake every test uses: Sleep is a no-op,
// so the 150ms debounce window never actually elapses in a test run.
var noSleep = Clock{Sleep: func(time.Duration) {}}

// testSetup is newTestModel's own input -- a thin, test-friendly wrapper
// over Setup that fills in sane defaults (theme.Default palette, a no-op
// Clock, a fresh fakeRunner over Workspaces) for whatever a given test
// doesn't care about.
type testSetup struct {
	Git          *fakeGit
	Linear       *fakeLinear
	Clauth       *fakeClauth
	Ctx          herdrc.Context
	Config       config.Config
	State        config.State
	Workspaces   []herdrc.WorkspaceInfo
	ClauthStatus clauth.Status
	LinearCache  []linear.Issue
}

func newTestModel(t *testing.T, s testSetup) Model {
	t.Helper()

	git := s.Git
	if git == nil {
		git = newFakeGit()
	}
	var linSrc linearSource
	if s.Linear != nil {
		linSrc = s.Linear
	}
	var clSrc clauthSource
	if s.Clauth != nil {
		clSrc = s.Clauth
	}

	cfg := s.Config
	if cfg.Agents.Favorites == nil {
		cfg.Agents.Favorites = []string{"claude"}
	}

	return New(Setup{
		Deps: Deps{
			Runner: &fakeRunner{workspaces: s.Workspaces},
			Linear: linSrc,
			Clauth: clSrc,
			Git:    git,
			Clock:  noSleep,
		},
		Ctx:          s.Ctx,
		Config:       cfg,
		State:        s.State,
		Palette:      theme.Default(),
		StateDir:     t.TempDir(),
		Workspaces:   s.Workspaces,
		ClauthStatus: s.ClauthStatus,
		LinearCache:  s.LinearCache,
	})
}

func key(code rune, mod tea.KeyMod) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code, Mod: mod} }
func rn(r rune) tea.KeyPressMsg                     { return tea.KeyPressMsg{Code: r, Text: string(r)} }

var keyTab = key(tea.KeyTab, 0)

// --- Step 1's four required scenarios --------------------------------

// TestDebounceCoalesces pins the brief's own literal requirement: two rapid
// directory changes within the 150ms debounce window must coalesce into
// exactly one real git check, not two -- the first (now-stale) debounce
// firing must be dropped without ever calling into gitSource.
func TestDebounceCoalesces(t *testing.T) {
	git := newFakeGit()
	m := newTestModel(t, testSetup{Git: git})

	cmd1 := m.scheduleDirCheck("path1")
	cmd2 := m.scheduleDirCheck("path2")

	msg1 := cmd1().(dirDebounceMsg)
	msg2 := cmd2().(dirDebounceMsg)
	if msg1.req.version == msg2.req.version {
		t.Fatalf("two schedule calls produced the same version %d, want distinct", msg1.req.version)
	}

	// The stale (first) debounce firing must be dropped: no cmd, no git
	// call.
	m2, cmd := m.handleDirDebounce(msg1)
	m = m2
	if cmd != nil {
		if got := cmd(); got != nil {
			t.Fatalf("stale debounce produced a real check instead of being dropped: %#v", got)
		}
	}
	if git.dirExistsCalls != 0 {
		t.Fatalf("dirExistsCalls = %d after the stale debounce, want 0 (coalesced away)", git.dirExistsCalls)
	}

	// The fresh (second) debounce firing must trigger exactly one check.
	m2, cmd = m.handleDirDebounce(msg2)
	m = m2
	if cmd == nil {
		t.Fatalf("fresh debounce produced a nil cmd, want the real check")
	}
	cmd()
	if git.dirExistsCalls != 1 {
		t.Fatalf("dirExistsCalls = %d after the fresh debounce, want exactly 1", git.dirExistsCalls)
	}
	_ = m
}

// TestStaleVersionDroppedEndToEnd pins the brief's own literal requirement:
// a v1 result that resolves AFTER v2 (an out-of-order slow subprocess) must
// be discarded, never overwriting the already-applied v2 state.
func TestStaleVersionDroppedEndToEnd(t *testing.T) {
	m := newTestModel(t, testSetup{})

	// New itself already scheduled the very first dir-validity request
	// (see initCmds' own doc comment), so the two schedule calls below are
	// the 2nd/3rd requests, not necessarily versions 1/2 -- read the real
	// versions back from the returned cmds rather than assuming.
	cmd1 := m.scheduleDirCheck("old")
	cmd2 := m.scheduleDirCheck("new")
	v1 := cmd1().(dirDebounceMsg).req.version
	v2 := cmd2().(dirDebounceMsg).req.version

	// v2 ("new") resolves FIRST, reporting a usable git repo.
	m2, _ := m.handleDirResult(dirResultMsg{req: request{version: v2, key: "new"}, dirExists: true, isGitRepo: true})
	m = m2
	if !m.worktree.Enabled() {
		t.Fatalf("fresh result (v2) was not applied: WorktreeField.Enabled() = false, want true")
	}

	// v1 ("old") resolves SECOND (out of order), reporting a non-repo --
	// this must be dropped, not silently reverting v2's already-applied
	// state.
	m2, _ = m.handleDirResult(dirResultMsg{req: request{version: v1, key: "old"}, dirExists: false, isGitRepo: false})
	m = m2
	if !m.worktree.Enabled() {
		t.Fatalf("stale result (v1) was wrongly applied, overwriting the fresher v2 state")
	}
}

// TestDirResult_AppliesWorktreeDefaultOnceAndReChecksTitle pins two things
// together: the config-derived worktree on/off default (spec §6 field 4)
// applies exactly once, the first time a git-repo target is observed, and
// applying it re-runs the title-duplicate check (so the branch-exists half,
// which depends on worktree being on, doesn't silently stay stale --
// see handleDirResult's own doc comment on why this can't just rely on
// reactToChanges' usual diff).
func TestDirResult_AppliesWorktreeDefaultOnceAndReChecksTitle(t *testing.T) {
	git := newFakeGit()
	git.branchExists = true
	m := newTestModel(t, testSetup{Git: git, Config: config.Config{DefaultWorktree: true}})
	m.title.SetTitle("t", false)
	m.worktree.SetBranch("zvi/t", false)

	req := request{version: m.dirReqVersion, key: "/repo"}
	m2, cmd := m.handleDirResult(dirResultMsg{req: req, dirExists: true, isGitRepo: true})
	m = m2
	if !m.worktree.On() {
		t.Fatalf("WorktreeField.On() = false after a git-repo result with DefaultWorktree=true, want true")
	}
	if cmd == nil {
		t.Fatalf("handleDirResult returned a nil cmd after flipping the worktree default, want a re-triggered title check")
	}
	debounce := cmd().(titleDebounceMsg)
	if !debounce.worktreeOn {
		t.Fatalf("re-triggered titleDebounceMsg.worktreeOn = false, want true (the state that just changed)")
	}

	// A SECOND git-repo result for a different path must NOT re-apply the
	// default (worktreeDefaultApplied is one-shot) or produce another
	// re-check cmd.
	req2 := request{version: m.dirReqVersion, key: "/other-repo"}
	m2, cmd = m.handleDirResult(dirResultMsg{req: req2, dirExists: true, isGitRepo: true})
	m = m2
	if cmd != nil {
		t.Fatalf("a second git-repo result produced a re-check cmd, want nil (default already applied once)")
	}
}

// TestDupVerdictsPushedToTitleField pins the brief's own literal
// requirement: both duplicate checks (branch exists via gitx, workspace
// label taken via Runner.WorkspaceList) are computed and pushed to
// TitleField via SetVerdict.
func TestDupVerdictsPushedToTitleField(t *testing.T) {
	git := newFakeGit()
	git.branchExists = true
	m := newTestModel(t, testSetup{
		Git:        git,
		Workspaces: []herdrc.WorkspaceInfo{{Label: "taken"}},
	})
	m.title.SetTitle("taken", false)

	cmd := m.scheduleTitleCheck("taken", "zvi/taken", "/repo", true)
	debounce := cmd().(titleDebounceMsg)

	m2, cmd2 := m.handleTitleDebounce(debounce)
	m = m2
	result := cmd2().(titleResultMsg)
	if !result.branchExists || !result.labelTaken {
		t.Fatalf("titleResultMsg = %+v, want both branchExists and labelTaken true", result)
	}

	m2, _ = m.handleTitleResult(result)
	m = m2

	frame := ansi.Strip(m.title.View(60, m.title.Height(24)))
	if !strings.Contains(frame, "branch & label in use") {
		t.Fatalf("TitleField.View(60) = %q, want it to contain the composed dup verdict", frame)
	}
}

// TestDupVerdicts_BranchExistsSkippedWhenWorktreeOff pins that the branch-
// exists check is skipped when worktree is off (a non-worktree session
// never creates a branch at all), while the label check still runs.
func TestDupVerdicts_BranchExistsSkippedWhenWorktreeOff(t *testing.T) {
	git := newFakeGit()
	git.branchExists = true // would report a collision if the check ran at all
	m := newTestModel(t, testSetup{Git: git})

	cmd := m.scheduleTitleCheck("new title", "zvi/new-title", "/repo", false)
	debounce := cmd().(titleDebounceMsg)
	_, cmd2 := m.handleTitleDebounce(debounce)
	result := cmd2().(titleResultMsg)

	if result.branchExists {
		t.Fatalf("branchExists = true with worktree off, want false (skipped)")
	}
	if git.branchExistsCalls != 0 {
		t.Fatalf("BranchExists was called %d times with worktree off, want 0", git.branchExistsCalls)
	}
}

// TestTitleResult_StaleVersionDropped pins the title-duplicate pipeline's
// own version staleness gate at the RESULT level (fix round 1: the review
// found only the dir-validity pipeline had an equivalent test, despite the
// report's own "Plus: stale-drop coverage for the base-list and
// title-check pipelines individually" claim) -- a v1 result reporting a
// collision that resolves AFTER a v2 result reporting none must be
// dropped, not overwrite the fresher, clean verdict.
func TestTitleResult_StaleVersionDropped(t *testing.T) {
	m := newTestModel(t, testSetup{})
	m.title.SetTitle("t", false)

	cmd1 := m.scheduleTitleCheck("t", "b1", "/repo", true)
	cmd2 := m.scheduleTitleCheck("t", "b2", "/repo", true)
	v1 := cmd1().(titleDebounceMsg).req.version
	v2 := cmd2().(titleDebounceMsg).req.version

	// v2 (fresher) resolves first: no duplicates.
	m2, _ := m.handleTitleResult(titleResultMsg{req: request{version: v2, key: "t"}})
	m = m2
	frame := ansi.Strip(m.title.View(60, m.title.Height(24)))
	if strings.Contains(frame, "exists") || strings.Contains(frame, "in use") {
		t.Fatalf("TitleField.View(60) = %q, fresh v2's clean verdict was not applied", frame)
	}

	// v1 (stale) resolves second, reporting a collision -- must be dropped,
	// not overwriting v2's already-applied clean verdict.
	m2, _ = m.handleTitleResult(titleResultMsg{req: request{version: v1, key: "t"}, branchExists: true, labelTaken: true})
	m = m2
	frame = ansi.Strip(m.title.View(60, m.title.Height(24)))
	if strings.Contains(frame, "in use") {
		t.Fatalf("TitleField.View(60) = %q, stale v1 result was wrongly applied", frame)
	}
}

// TestIssueSelectionSeedsAndRespectsTouchedBranch pins the brief's own
// literal requirement: choosing a Linear issue seeds Title/Branch/Prompt,
// but a Branch the user has already typed into is left unclobbered.
func TestIssueSelectionSeedsAndRespectsTouchedBranch(t *testing.T) {
	m := newTestModel(t, testSetup{})

	// The user types into Branch directly (bypassing the focus ring, which
	// is fine -- worktreeBranchSection.Update applies regardless of the
	// field's own inert/enabled state, matching a real keypress reaching
	// whichever section currently holds focus). The wrapped lineInput
	// ignores key input while blurred (bubbles/v2's own textinput.Model.
	// Update: "if !m.focus { return m, nil }"), so Focus() is required
	// first, exactly as form.go's own focus ring would have already done
	// for whichever section is current.
	m.worktree.BranchSection().Focus()
	m.worktree.BranchSection().Update(rn('x'))
	if got := m.worktree.Branch(); got != "x" {
		t.Fatalf("Branch() after typing = %q, want %q", got, "x")
	}

	desc := "some description"
	issue := &linear.Issue{
		Identifier: "ENG-1", Title: "Fix login bug",
		BranchName: "eng-1-fix-login", URL: "https://example.com/ENG-1",
		Description: desc,
	}
	m2, _ := m.handleIssueChosen(form.IssueChosenMsg{Issue: issue})
	m = m2

	if got := m.title.Value(); got != "Fix login bug" {
		t.Fatalf("Title.Value() = %q, want the issue's own title", got)
	}
	if got := m.worktree.Branch(); got != "x" {
		t.Fatalf("Branch() after issue selection = %q, want the user's own typed %q left unclobbered", got, "x")
	}
	if got := m.prompt.Value(); !strings.Contains(got, "ENG-1") || !strings.Contains(got, desc) {
		t.Fatalf("Prompt.Value() = %q, want it to contain the issue identifier and description", got)
	}
}

// TestIssueSelectionSeedsBranchWhenUntouched confirms the positive case
// the touched test above doesn't cover: with no prior user edit, Branch IS
// seeded from the issue's own branchName.
func TestIssueSelectionSeedsBranchWhenUntouched(t *testing.T) {
	m := newTestModel(t, testSetup{})
	issue := &linear.Issue{Identifier: "ENG-2", Title: "Add dark mode", BranchName: "eng-2-dark-mode"}
	m2, _ := m.handleIssueChosen(form.IssueChosenMsg{Issue: issue})
	m = m2
	if got := m.worktree.Branch(); got != "eng-2-dark-mode" {
		t.Fatalf("Branch() = %q, want the issue's own branchName %q", got, "eng-2-dark-mode")
	}
}

// TestIssueDeselection_ReturnsToManualMode confirms selecting "none" (a nil
// Issue) flips back to manual mode, so a subsequent title edit resumes
// deriving the branch slug.
func TestIssueDeselection_ReturnsToManualMode(t *testing.T) {
	m := newTestModel(t, testSetup{Config: config.Config{BranchPrefix: "zvi/"}})
	iss := &linear.Issue{Identifier: "ENG-3", Title: "seeded", BranchName: "from-linear"}
	m2, _ := m.handleIssueChosen(form.IssueChosenMsg{Issue: iss})
	m = m2
	if m.linearIssueSelected != true {
		t.Fatalf("linearIssueSelected = false after choosing an issue, want true")
	}

	m2, _ = m.handleIssueChosen(form.IssueChosenMsg{Issue: nil})
	m = m2
	if m.linearIssueSelected {
		t.Fatalf("linearIssueSelected = true after choosing \"none\", want false")
	}

	m.title.SetTitle("Fix login bug", false)
	cmds := m.reactToChanges()
	if len(cmds) == 0 {
		t.Fatalf("reactToChanges() returned no cmds after a manual-mode title edit")
	}
	if got := m.worktree.Branch(); got != "zvi/fix-login-bug" {
		t.Fatalf("Branch() = %q, want the derived slug %q", got, "zvi/fix-login-bug")
	}
}

// --- base-ref list -------------------------------------------------------

// TestBaseResult_ErrorSetsCouldNotList pins the base-list failure path
// (spec §8: "every remote fetch has a visible ... 'couldn't list' header
// state"): a ListBranches error must not touch WorktreeField's item list,
// only its status text, and must not fire the once-per-repo fetch.
func TestBaseResult_ErrorSetsCouldNotList(t *testing.T) {
	git := newFakeGit()
	m := newTestModel(t, testSetup{Git: git})
	// renderBase only shows the status line while the field is a usable,
	// toggled-on git target -- see field_worktree.go's own renderBase.
	m.worktree.SetGitTarget(true)
	m.worktree.SetOn(true)

	req := request{version: 1, key: "/repo"}
	m.baseReqVersion = 1

	m2, cmd := m.handleBaseResult(baseResultMsg{req: req, err: true})
	m = m2
	if cmd != nil {
		t.Fatalf("a failed base-list result produced a fetch cmd, want nil")
	}
	if len(git.fetchPruneCalls) != 0 {
		t.Fatalf("fetchPruneCalls = %v after a failed result, want none", git.fetchPruneCalls)
	}

	frame := ansi.Strip(m.worktree.BaseSection().View(60, m.worktree.BaseSection().Height(24)))
	if !strings.Contains(frame, "couldn't list") {
		t.Fatalf("BaseSection View = %q, want it to contain the \"couldn't list\" status", frame)
	}
}

// TestBaseResult_StaleVersionDropped pins the base-list pipeline's own
// version staleness gate at the RESULT level (fix round 1 -- see
// TestTitleResult_StaleVersionDropped's own doc comment for why this test
// exists now and didn't before): a v1 result reporting failure that
// resolves AFTER a v2 result reporting success must be dropped, not
// clobber the fresher, successful state with "couldn't list".
func TestBaseResult_StaleVersionDropped(t *testing.T) {
	git := newFakeGit()
	m := newTestModel(t, testSetup{Git: git})
	m.worktree.SetGitTarget(true)
	m.worktree.SetOn(true)

	cmd1 := m.scheduleBaseCheck("old")
	cmd2 := m.scheduleBaseCheck("new")
	v1 := cmd1().(baseDebounceMsg).req.version
	v2 := cmd2().(baseDebounceMsg).req.version

	// v2 (fresher) resolves first, successfully.
	m2, _ := m.handleBaseResult(baseResultMsg{req: request{version: v2, key: "new"}, refs: []string{"main"}})
	m = m2
	frame := ansi.Strip(m.worktree.BaseSection().View(60, m.worktree.BaseSection().Height(24)))
	if strings.Contains(frame, "couldn't list") {
		t.Fatalf("BaseSection View = %q, fresh v2's success was not applied", frame)
	}

	// v1 (stale) resolves second, with an error -- must be dropped, not
	// clobbering v2's already-applied success with "couldn't list".
	m2, _ = m.handleBaseResult(baseResultMsg{req: request{version: v1, key: "old"}, err: true})
	m = m2
	frame = ansi.Strip(m.worktree.BaseSection().View(60, m.worktree.BaseSection().Height(24)))
	if strings.Contains(frame, "couldn't list") {
		t.Fatalf("BaseSection View = %q, stale v1 result was wrongly applied", frame)
	}
}

// TestBaseDebounce_TriggersRun pins the debounce-fired -> real-check
// transition for the base-ref list source, the base-list analogue of
// TestDebounceCoalesces' own dir-validity coverage.
func TestBaseDebounce_TriggersRun(t *testing.T) {
	git := newFakeGit()
	git.listBranchesResult = []string{"main", "feature"}
	m := newTestModel(t, testSetup{Git: git})

	cmd := m.scheduleBaseCheck("/repo")
	debounce := cmd().(baseDebounceMsg)

	m2, runCmd := m.handleBaseDebounce(debounce)
	m = m2
	if runCmd == nil {
		t.Fatalf("handleBaseDebounce returned a nil cmd for the current version")
	}
	result := runCmd().(baseResultMsg)
	if len(result.refs) != 2 || git.listBranchesCalls != 1 {
		t.Fatalf("baseResultMsg = %+v (listBranchesCalls=%d), want the fake's two refs from exactly one call", result, git.listBranchesCalls)
	}
}

// --- titleVerdictText: pure function, every combination --------------------

func TestTitleVerdictText_AllCombinations(t *testing.T) {
	cases := []struct {
		branchExists, labelTaken bool
		want                     string
	}{
		{false, false, ""},
		{true, false, "branch exists"},
		{false, true, "label in use"},
		{true, true, "branch & label in use"},
	}
	for _, c := range cases {
		if got := titleVerdictText(c.branchExists, c.labelTaken); got != c.want {
			t.Errorf("titleVerdictText(%v, %v) = %q, want %q", c.branchExists, c.labelTaken, got, c.want)
		}
	}
}

// --- once-per-repo git fetch --prune -------------------------------------

// TestFetchPruneFiresOncePerRepoPerFormOpen pins spec §6 field 4's own
// "fires once per repo per form-open": two successful base-list checks for
// the SAME path must fire the background fetch only once.
func TestFetchPruneFiresOncePerRepoPerFormOpen(t *testing.T) {
	git := newFakeGit()
	m := newTestModel(t, testSetup{Git: git})

	req := request{version: 1, key: "/repo"}
	m.baseReqVersion = 1

	m2, cmd := m.handleBaseResult(baseResultMsg{req: req, refs: []string{"main"}})
	m = m2
	if cmd == nil {
		t.Fatalf("first successful base-list result for a new repo produced no fetch cmd")
	}
	cmd()
	if len(git.fetchPruneCalls) != 1 {
		t.Fatalf("fetchPruneCalls = %v after the first result, want exactly one call", git.fetchPruneCalls)
	}

	// A second, later result for the SAME path must not fetch again.
	m2, cmd = m.handleBaseResult(baseResultMsg{req: req, refs: []string{"main", "feature"}})
	m = m2
	if cmd != nil {
		t.Fatalf("second base-list result for an already-fetched repo produced a fetch cmd, want nil")
	}
	if len(git.fetchPruneCalls) != 1 {
		t.Fatalf("fetchPruneCalls = %v after the second result, want still exactly one", git.fetchPruneCalls)
	}
}

// TestFetchPruneDone_ReRunsForCurrentPath_IgnoresStale pins
// handleFetchPruneDone's own "re-list the same path; ignore a path the
// user has since navigated away from" contract.
func TestFetchPruneDone_ReRunsForCurrentPath_IgnoresStale(t *testing.T) {
	git := newFakeGit()
	m := newTestModel(t, testSetup{Git: git})
	m.dir.SetCandidates(99, []string{"/repo"})

	m2, cmd := m.handleFetchPruneDone(fetchPruneDoneMsg{path: "/repo"})
	m = m2
	if cmd == nil {
		t.Fatalf("fetchPruneDoneMsg for the CURRENT path produced a nil cmd, want a re-run")
	}
	msg := cmd()
	if _, ok := msg.(baseResultMsg); !ok {
		t.Fatalf("fetchPruneDoneMsg re-run produced %T, want baseResultMsg", msg)
	}

	m2, cmd = m.handleFetchPruneDone(fetchPruneDoneMsg{path: "/some/other/repo"})
	if cmd != nil {
		t.Fatalf("fetchPruneDoneMsg for a path the user navigated away from produced a non-nil cmd")
	}
	_ = m2
}

// --- startup: Bootstrap's pre-open refusal (spec §9) ----------------------

func validContextJSON() string {
	return `{"workspace_id":"w1","workspace_cwd":"/repo","focused_pane_id":"p1"}`
}

func TestBootstrap_InvalidContext_Refuses(t *testing.T) {
	env := Env{ContextJSON: "", ConfigDir: t.TempDir(), StateDir: t.TempDir()}
	runner := &fakeRunner{}
	_, err := Bootstrap(env, runner, nil, nil, noSleep)
	if err == nil {
		t.Fatalf("Bootstrap with an empty $HERDR_PLUGIN_CONTEXT_JSON returned a nil error, want the pre-open refusal")
	}
	if runner.listCalls != 0 {
		t.Fatalf("WorkspaceList was called %d times despite an invalid context, want 0 (refuse before probing herdr)", runner.listCalls)
	}
}

func TestBootstrap_UnreachableHerdr_Refuses(t *testing.T) {
	env := Env{ContextJSON: validContextJSON(), ConfigDir: t.TempDir(), StateDir: t.TempDir()}
	runner := &fakeRunner{listErr: context.DeadlineExceeded}
	_, err := Bootstrap(env, runner, nil, nil, noSleep)
	if err == nil {
		t.Fatalf("Bootstrap with an unreachable herdr returned a nil error, want the pre-open refusal")
	}
}

func TestBootstrap_Success(t *testing.T) {
	env := Env{ContextJSON: validContextJSON(), ConfigDir: t.TempDir(), StateDir: t.TempDir()}
	runner := &fakeRunner{workspaces: []herdrc.WorkspaceInfo{{WorkspaceID: "w1", Label: "main"}}}
	m, err := Bootstrap(env, runner, nil, newFakeGit(), noSleep)
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if m.ctx.WorkspaceCwd != "/repo" {
		t.Fatalf("Model.ctx.WorkspaceCwd = %q, want %q", m.ctx.WorkspaceCwd, "/repo")
	}
	if len(m.workspaces) != 1 {
		t.Fatalf("Model.workspaces = %v, want the one fetched workspace", m.workspaces)
	}
}

// A clauth load failure must degrade, not refuse -- spec §13.
func TestBootstrap_ClauthFailureDegrades(t *testing.T) {
	env := Env{ContextJSON: validContextJSON(), ConfigDir: t.TempDir(), StateDir: t.TempDir()}
	runner := &fakeRunner{}
	cl := &fakeClauth{err: context.DeadlineExceeded}
	m, err := Bootstrap(env, runner, cl, newFakeGit(), noSleep)
	if err != nil {
		t.Fatalf("Bootstrap with a failing clauth source returned an error, want it to degrade: %v", err)
	}
	if m.account != nil {
		t.Fatalf("Model.account is non-nil despite a failed clauth load, want nil (degraded, not shown)")
	}
}

// --- static section construction (spec §6) --------------------------------

func TestNew_LinearFieldOnlyWhenConfigured(t *testing.T) {
	without := newTestModel(t, testSetup{})
	if without.issue != nil {
		t.Fatalf("Model.issue is non-nil with no linearSource configured, want nil")
	}

	with := newTestModel(t, testSetup{Linear: &fakeLinear{}})
	if with.issue == nil {
		t.Fatalf("Model.issue is nil with a linearSource configured, want non-nil")
	}
}

func TestNew_AccountFieldOnlyWithTwoOrMoreProfiles(t *testing.T) {
	zero := newTestModel(t, testSetup{Clauth: &fakeClauth{}})
	if zero.account != nil {
		t.Fatalf("Model.account is non-nil with zero clauth profiles, want nil")
	}

	one := newTestModel(t, testSetup{
		Clauth:       &fakeClauth{},
		ClauthStatus: clauth.Status{Schema: 1, Profiles: []clauth.Profile{{Name: "a"}}},
	})
	if one.account != nil {
		t.Fatalf("Model.account is non-nil with exactly one clauth profile, want nil")
	}

	two := newTestModel(t, testSetup{
		Clauth:       &fakeClauth{},
		ClauthStatus: clauth.Status{Schema: 1, Profiles: []clauth.Profile{{Name: "a"}, {Name: "b"}}},
	})
	if two.account == nil {
		t.Fatalf("Model.account is nil with two clauth profiles and a clauthSource configured, want non-nil")
	}
}

// TestNew_AccountFieldRequiresClauthSource pins fix round 1's own repro:
// a caller constructing Setup directly (bypassing Bootstrap, whose own
// clauthEnabled gate is what normally keeps ClauthStatus empty when clauth
// is disabled/absent) could still hand in >= 2 profiles with Deps.Clauth
// == nil. Before this fix, that constructed AccountField anyway; focusing
// it then panicked in reloadClauthCmd (a nil-interface method call) --
// reproduced directly by the reviewer using the same testSetup shape
// TestSyncDerivedInertness_AccountFollowsAgentKind already used. Now the
// gate itself prevents construction, so the field simply isn't there.
func TestNew_AccountFieldRequiresClauthSource(t *testing.T) {
	m := newTestModel(t, testSetup{
		ClauthStatus: clauth.Status{Schema: 1, Profiles: []clauth.Profile{{Name: "a"}, {Name: "b"}}},
		// Clauth deliberately left nil.
	})
	if m.account != nil {
		t.Fatalf("Model.account is non-nil with >= 2 profiles but no clauthSource, want nil")
	}
}

// TestReloadClauthCmd_NilSourceDoesNotPanic is the reviewer's own repro,
// one level down: even bypassing the New-time gate above and calling
// reloadClauthCmd directly against a Model whose deps.Clauth is nil (the
// exact call reactToChanges made when Account somehow ended up focused
// without a configured clauthSource) must not panic -- it must return a
// nil cmd instead. Defense in depth alongside the construction gate.
func TestReloadClauthCmd_NilSourceDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("reloadClauthCmd panicked with a nil clauthSource: %v", r)
		}
	}()
	m := newTestModel(t, testSetup{}) // Clauth left nil
	if cmd := m.reloadClauthCmd(); cmd != nil {
		t.Fatalf("reloadClauthCmd() with a nil clauthSource returned a non-nil cmd, want nil")
	}
}

// TestNew_WorktreeSectionsAreAdjacent pins the carried requirement: the
// three worktree zones must still read as ONE visual group in the
// assembled section list.
//
// Fix round 1: this used to (a) rebuild m.form from a hand-written
// Sections list, discarding the real one New() assembled, and (b) even
// after fixing that, rely on a Tab-walk (sectionOrder) -- but Tab
// navigation SKIPS disabled sections (focus.go's nextEnabled), and
// Placement is disabled whenever Worktree is on (the exact state this test
// puts the form in to make Branch/Base enabled), so an inserted-in-between
// regression on Placement specifically would have kept passing even after
// fix (a) alone -- verified directly: temporarily inserting m.placement
// between ChipsSection and BranchSection in app.go's own New still passed
// the Tab-walk version of this test. form.Model.SectionIDs() (added
// alongside this fix) returns the real construction order INCLUDING
// disabled sections, closing that gap for good.
func TestNew_WorktreeSectionsAreAdjacent(t *testing.T) {
	m := newTestModel(t, testSetup{})

	ids := m.form.SectionIDs()
	idx := func(id string) int {
		for i, v := range ids {
			if v == id {
				return i
			}
		}
		return -1
	}
	w, b, base := idx("worktree"), idx("branch"), idx("base")
	if w < 0 || b < 0 || base < 0 {
		t.Fatalf("SectionIDs() %v is missing one of worktree/branch/base", ids)
	}
	if b != w+1 || base != b+1 {
		t.Fatalf("worktree sections are not adjacent in %v (worktree=%d branch=%d base=%d)", ids, w, b, base)
	}
}

// --- agent kinds: favorites-first (carried requirement) --------------------

func TestOrderedAgentKinds_FavoritesFirst(t *testing.T) {
	got := orderedAgentKinds([]string{"codex", "claude"})
	if len(got) < 2 || got[0] != "codex" || got[1] != "claude" {
		t.Fatalf("orderedAgentKinds favorites prefix = %v, want [codex claude ...]", got[:2])
	}
	// Every known kind must still be reachable exactly once.
	seen := map[string]int{}
	for _, k := range got {
		seen[k]++
	}
	for _, k := range knownAgentKinds {
		if seen[k] != 1 {
			t.Fatalf("known kind %q appears %d times in orderedAgentKinds output, want exactly 1", k, seen[k])
		}
	}
	if len(got) != len(knownAgentKinds) {
		t.Fatalf("orderedAgentKinds returned %d kinds, want %d (favorites are already known kinds here)", len(got), len(knownAgentKinds))
	}
}

func TestOrderedAgentKinds_DropsEmptyAndDuplicates(t *testing.T) {
	got := orderedAgentKinds([]string{"claude", "", "claude"})
	if got[0] != "claude" {
		t.Fatalf("orderedAgentKinds[0] = %q, want %q", got[0], "claude")
	}
	count := 0
	for _, k := range got {
		if k == "claude" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("\"claude\" appears %d times, want exactly 1", count)
	}
}

// TestNew_AgentKindsFavoritesFirst pins the SAME carried requirement at
// Model construction: AgentField.Value() (index 0) must be the configured
// favorite, not an arbitrary knownAgentKinds entry.
func TestNew_AgentKindsFavoritesFirst(t *testing.T) {
	m := newTestModel(t, testSetup{Config: config.Config{Agents: config.AgentsConfig{Favorites: []string{"codex", "claude"}}}})
	if got := m.agent.Value(); got != "codex" {
		t.Fatalf("AgentField.Value() = %q, want the first configured favorite %q", got, "codex")
	}
}

// --- dynamic inertness sync -----------------------------------------------

// TestSyncDerivedInertness_AccountFollowsAgentKind pins spec §6 field 7's
// dynamic half: AccountField goes inert the moment the agent kind isn't
// claude, and re-enables when it's claude again.
func TestSyncDerivedInertness_AccountFollowsAgentKind(t *testing.T) {
	m := newTestModel(t, testSetup{
		Config:       config.Config{Agents: config.AgentsConfig{Favorites: []string{"claude", "codex"}}},
		Clauth:       &fakeClauth{},
		ClauthStatus: clauth.Status{Schema: 1, Profiles: []clauth.Profile{{Name: "a"}, {Name: "b"}}},
	})
	if !m.account.Enabled() {
		t.Fatalf("AccountField.Enabled() = false with agent kind %q, want true", m.agent.Value())
	}

	m.agent.Update(key(tea.KeyRight, 0)) // claude -> codex
	m.syncDerivedInertness()
	if m.account.Enabled() {
		t.Fatalf("AccountField.Enabled() = true with agent kind %q, want false (inert)", m.agent.Value())
	}
}

// --- mouse/altscreen (carried requirement) ---------------------------------

// TestView_EnablesAltScreenAndMouse pins the bubbletea v2.0.8 fact this
// task verified directly against the vendored package (options.go defines
// no WithAltScreen/mouse-enabling ProgramOption at all in v2 -- both moved
// to fields on the returned tea.View): View() must set them itself.
func TestView_EnablesAltScreenAndMouse(t *testing.T) {
	m := newTestModel(t, testSetup{})
	v := m.View()
	if !v.AltScreen {
		t.Fatalf("View().AltScreen = false, want true")
	}
	if v.MouseMode != tea.MouseModeAllMotion {
		t.Fatalf("View().MouseMode = %v, want tea.MouseModeAllMotion", v.MouseMode)
	}
}

// --- Init/New split: no lost counter mutation -----------------------------

// TestInit_DoesNotLoseNewsCounterState guards the pitfall this task's own
// report documents at length: tea.Model.Init() returns only a tea.Cmd, with
// no way to persist a mutated receiver, so any request-counter bump made
// FROM Init (rather than New) would be silently discarded by bubbletea,
// which keeps using the pre-Init Model value for its first real Update.
// This pins the fix directly: New (not Init) is where the very first
// dir-validity request is scheduled, so the counter bump is already part
// of the Model bubbletea actually retains, and the Cmd Init() later
// replays from initCmds still resolves against it. (Deliberately reads
// initCmds directly rather than going through m.Init()'s own returned
// tea.Batch -- that batch also contains form.Model.Init()'s cursor-blink
// Cmd, which blocks for a real ~530ms if invoked synchronously; this test
// has nothing to do with that Cmd.)
func TestInit_DoesNotLoseNewsCounterState(t *testing.T) {
	m := newTestModel(t, testSetup{})

	if m.dirReqVersion == 0 {
		t.Fatalf("dirReqVersion = 0 right after New, want New to have already scheduled the first dir-validity request")
	}

	var found bool
	for _, cmd := range m.initCmds {
		dd, ok := cmd().(dirDebounceMsg)
		if !ok {
			continue
		}
		found = true
		if dd.req.version != m.dirReqVersion {
			t.Fatalf("dirDebounceMsg.req.version = %d, want it to match New's own dirReqVersion %d", dd.req.version, m.dirReqVersion)
		}
		next, runCmd := m.handleDirDebounce(dd)
		m = next
		if runCmd == nil {
			t.Fatalf("Init()'s own dir-validity debounce was dropped as stale on the Model New returned -- the counter bump was lost")
		}
	}
	if !found {
		t.Fatalf("initCmds never produced a dirDebounceMsg")
	}
}

// --- Update's own top-level dispatch ---------------------------------

// TestUpdate_RoutesAsyncMessages drives every async.go message THROUGH
// Model.Update itself (not the handle* method directly, unlike this file's
// other tests) -- pinning that the switch in Update actually wires each
// case to its handler, end to end.
func TestUpdate_RoutesAsyncMessages(t *testing.T) {
	git := newFakeGit()
	m := newTestModel(t, testSetup{Git: git})

	cmd := m.scheduleDirCheck("/repo")
	debounce := cmd().(dirDebounceMsg)

	next, cmd := m.Update(debounce)
	m = next.(Model)
	if cmd == nil {
		t.Fatalf("Update(dirDebounceMsg) returned a nil cmd for the current version")
	}
	result := cmd()

	next, _ = m.Update(result)
	m = next.(Model)
	if !m.worktree.Enabled() {
		t.Fatalf("Update(dirResultMsg) did not apply WorktreeField.SetGitTarget")
	}
}

// TestUpdate_CancelQuits pins form.CancelMsg -> tea.Quit.
func TestUpdate_CancelQuits(t *testing.T) {
	m := newTestModel(t, testSetup{})
	_, cmd := m.Update(form.CancelMsg{})
	if cmd == nil {
		t.Fatalf("Update(CancelMsg{}) returned a nil cmd, want tea.Quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("Update(CancelMsg{})'s cmd did not produce tea.QuitMsg{}")
	}
}

// TestUpdate_SubmitAndClearNowActInsteadOfNoOp supersedes this task's own
// prior TestUpdate_SubmitAndClearAreNoOpsInTask20: Task 20's own declared
// scope boundary (form.SubmitMsg deferred to Task 20b, form.ClearRequestedMsg
// deferred entirely) is closed by this task -- see submit_test.go for the
// full submit-pipeline/validation/clear coverage; this only pins that both
// messages reach real handling via Update's own top-level dispatch, not
// just when called directly. A zero-value form (empty title, nothing else
// configured) is BLOCKED by checkSubmitValidation's own empty-title guard
// -- still a non-nil cmd (the blocking re-focus), just not the submit
// pipeline starting.
func TestUpdate_SubmitAndClearNowActInsteadOfNoOp(t *testing.T) {
	m := newTestModel(t, testSetup{})

	next, cmd := m.Update(form.SubmitMsg{})
	if cmd == nil {
		t.Fatalf("Update(SubmitMsg{}) with an empty title returned a nil cmd, want the blocking re-focus")
	}
	if m2 := next.(Model); m2.submitting {
		t.Fatalf("Update(SubmitMsg{}) with an empty title started submitting, want it blocked")
	}

	next, cmd = m.Update(form.ClearRequestedMsg{})
	if cmd == nil {
		t.Fatalf("Update(ClearRequestedMsg{}) returned a nil cmd, want the rebuilt form's own Init cmd")
	}
	_ = next.(Model)
}

// TestUpdate_IssueChosenRoutesThroughTopLevelDispatch pins that
// form.IssueChosenMsg reaches handleIssueChosen via Update's own switch,
// not just when called directly (see the earlier direct-call seeding
// tests).
func TestUpdate_IssueChosenRoutesThroughTopLevelDispatch(t *testing.T) {
	m := newTestModel(t, testSetup{})
	issue := &linear.Issue{Identifier: "ENG-9", Title: "via Update", BranchName: "eng-9"}
	next, _ := m.Update(form.IssueChosenMsg{Issue: issue})
	m = next.(Model)
	if got := m.title.Value(); got != "via Update" {
		t.Fatalf("Title.Value() after Update(IssueChosenMsg) = %q, want %q", got, "via Update")
	}
}

// TestUpdate_DefaultRoutesToFormAndReactsToChanges pins the fallback path:
// an ordinary message (here, a WindowSizeMsg) reaches form.Model.Update,
// and reactToChanges runs afterward -- exercised here via a directory
// change made directly on the field, then a no-op message pumped through
// Update to trigger the diff.
func TestUpdate_DefaultRoutesToFormAndReactsToChanges(t *testing.T) {
	m := newTestModel(t, testSetup{})
	m.dir.SetCandidates(99, []string{"/repo-a", "/repo-b"})
	versionBefore := m.dirReqVersion

	next, cmd := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)
	if m.dirReqVersion == versionBefore {
		t.Fatalf("dirReqVersion unchanged after a directory-candidate change routed through Update, want reactToChanges to have scheduled a fresh check")
	}
	_ = cmd
}

// --- linear async refresh + clauth reload-on-focus -------------------------

// TestRefreshLinearCmd_ProducesResultFromTheSource pins refreshLinearCmd's
// own construction directly (New already schedules it into initCmds when
// Linear is configured, but nothing else invokes the returned closure).
func TestRefreshLinearCmd_ProducesResultFromTheSource(t *testing.T) {
	fl := &fakeLinear{issues: []linear.Issue{{Identifier: "ENG-7"}}}
	m := newTestModel(t, testSetup{Linear: fl})

	msg := m.refreshLinearCmd()()
	result, ok := msg.(linearResultMsg)
	if !ok {
		t.Fatalf("refreshLinearCmd() produced %T, want linearResultMsg", msg)
	}
	if len(result.issues) != 1 || result.issues[0].Identifier != "ENG-7" {
		t.Fatalf("linearResultMsg.issues = %+v, want the fake's one issue", result.issues)
	}
	if fl.calls != 1 {
		t.Fatalf("fakeLinear.calls = %d, want exactly 1", fl.calls)
	}
}

func TestRefreshLinearCmd_ErrorReported(t *testing.T) {
	fl := &fakeLinear{err: context.DeadlineExceeded}
	m := newTestModel(t, testSetup{Linear: fl})
	result := m.refreshLinearCmd()().(linearResultMsg)
	if !result.err {
		t.Fatalf("linearResultMsg.err = false after a failing AssignedIssues call, want true")
	}
}

// TestLinearResult_AppliesIssuesAndSavesCache pins handleLinearResult's own
// contract: a successful refresh replaces IssueField's item list and
// persists the new cache.
func TestLinearResult_AppliesIssuesAndSavesCache(t *testing.T) {
	m := newTestModel(t, testSetup{Linear: &fakeLinear{}})
	issues := []linear.Issue{{Identifier: "ENG-5", Title: "fresh from the network"}}

	m2, _ := m.handleLinearResult(linearResultMsg{issues: issues})
	m = m2

	if got := m.issue.Selected(); got != nil {
		t.Fatalf("IssueField.Selected() = %+v right after SetIssues (cursor stays on \"none\"), want nil", got)
	}
	// SetIssues applied -- confirmed indirectly via the rendered picker
	// containing the fresh issue's own identifier once focused.
	m.issue.Focus()
	frame := ansi.Strip(m.issue.View(80, m.issue.Height(24)))
	if !strings.Contains(frame, "ENG-5") {
		t.Fatalf("IssueField.View(80) = %q, want it to contain the freshly applied issue", frame)
	}

	cached, _, err := linear.LoadCache(m.stateDir)
	if err != nil {
		t.Fatalf("LoadCache after handleLinearResult: %v", err)
	}
	if len(cached) != 1 || cached[0].Identifier != "ENG-5" {
		t.Fatalf("LoadCache() = %+v, want the freshly saved issue", cached)
	}
}

// TestLinearResult_ErrorLeavesIssuesUntouched pins spec §13's "network
// failures degrade ... never block" for Linear: a failed refresh must not
// clobber whatever the cache-rendered list already showed.
func TestLinearResult_ErrorLeavesIssuesUntouched(t *testing.T) {
	cached := []linear.Issue{{Identifier: "ENG-6", Title: "from cache"}}
	m := newTestModel(t, testSetup{Linear: &fakeLinear{}, LinearCache: cached})

	m2, _ := m.handleLinearResult(linearResultMsg{err: true})
	m = m2

	m.issue.Focus()
	frame := ansi.Strip(m.issue.View(80, m.issue.Height(24)))
	if !strings.Contains(frame, "ENG-6") {
		t.Fatalf("IssueField.View(80) = %q, want the cache-rendered issue still present after a failed refresh", frame)
	}
}

// TestReactToChanges_AccountFocusReloadsClauth pins spec §11's own "load
// at open and on account focus": focusing AccountField must schedule a
// fresh clauth reload, and its result must reach AccountField.SetProfiles.
func TestReactToChanges_AccountFocusReloadsClauth(t *testing.T) {
	initial := clauth.Status{Schema: 1, Profiles: []clauth.Profile{{Name: "a"}, {Name: "b"}}}
	cl := &fakeClauth{status: initial}
	m := newTestModel(t, testSetup{Clauth: cl, ClauthStatus: initial})
	if cl.calls != 0 {
		t.Fatalf("clauthSource.Status was called %d times before construction's own synchronous load, want the fake untouched by New (Bootstrap owns that load in production)", cl.calls)
	}

	m.form.Init()
	// Advance focus until AccountField itself is current.
	for i := 0; i < 16 && m.form.FocusedID() != "account"; i++ {
		next, _ := m.form.Update(keyTab)
		m.form = next.(form.Model)
	}
	if m.form.FocusedID() != "account" {
		t.Fatalf("could not reach the account section by tabbing")
	}

	cmds := m.reactToChanges()
	var reloadCmd tea.Cmd
	for _, c := range cmds {
		if c == nil {
			continue
		}
		reloadCmd = c
	}
	if reloadCmd == nil {
		t.Fatalf("reactToChanges() produced no cmd after focusing account, want a clauth reload")
	}
	msg := reloadCmd()
	result, ok := msg.(clauthResultMsg)
	if !ok {
		t.Fatalf("reload cmd produced %T, want clauthResultMsg", msg)
	}
	if cl.calls != 1 {
		t.Fatalf("clauthSource.Status was called %d times after focusing account, want exactly 1", cl.calls)
	}

	m2, _ := m.handleClauthResult(result)
	m = m2
	if !strings.Contains(ansi.Strip(m.account.View(80, m.account.Height(24))), "a") {
		t.Fatalf("AccountField.View(80) does not show the reloaded profile")
	}

	// Re-running reactToChanges without a further focus change must not
	// schedule ANOTHER reload.
	cmds = m.reactToChanges()
	for _, c := range cmds {
		if c == nil {
			continue
		}
		if _, ok := c().(clauthResultMsg); ok {
			t.Fatalf("a second reactToChanges pass with no new focus change scheduled another clauth reload")
		}
	}
}

// --- finding I5: a broken [linear] api_key_cmd must say so ---------------

// TestBootstrap_BrokenLinearKeyDegradesWithAReason pins spec §13's
// "degrade ... with a reason" for the case that had no reason anywhere:
// linear.ResolveAPIKey deliberately hard-errors when the user's own chosen
// key source fails, and Bootstrap used to discard that error and treat it
// as "Linear is not configured" -- so a typo in api_key_cmd made the whole
// Linear field vanish, silently.
func TestBootstrap_BrokenLinearKeyDegradesWithAReason(t *testing.T) {
	configDir := t.TempDir()
	cfg := "[linear]\napi_key_cmd = [\"/nonexistent/definitely-not-a-real-binary\"]\n"
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	env := Env{ContextJSON: validContextJSON(), ConfigDir: configDir, StateDir: t.TempDir()}

	m, err := Bootstrap(env, &fakeRunner{}, nil, newFakeGit(), noSleep)
	if err != nil {
		t.Fatalf("Bootstrap with a broken api_key_cmd refused outright, want it to degrade: %v", err)
	}
	if m.linearUnavailable == "" {
		t.Fatal("Model.linearUnavailable is empty after a failed api_key_cmd, want the reason recorded")
	}
	if m.deps.Linear != nil {
		t.Fatal("Deps.Linear is non-nil despite an unresolvable key")
	}
	if m.issue == nil {
		t.Fatal("Model.issue is nil after a failed api_key_cmd -- the field must be present-but-inert, not absent")
	}
	if m.issue.Enabled() {
		t.Error("the Linear field is enabled despite an unresolvable key, want present-but-inert")
	}

	// The user must be able to see WHY, on screen, not just in a field
	// that quietly stopped working.
	m.form.FocusByID("dir") // the inert Linear field cannot itself take focus
	frame := ansi.Strip(m.form.ViewAt(120, 40))
	if !strings.Contains(frame, "unavailable") {
		t.Errorf("the rendered form does not mark Linear unavailable:\n%s", frame)
	}
	if !strings.Contains(frame, "api_key_cmd") {
		t.Errorf("the rendered form does not say why Linear is unavailable:\n%s", frame)
	}
}

// TestNew_LinearUnconfiguredStillRendersNoField guards the other half of
// the same distinction: absent is still absent. Spec §6 is explicit that a
// statically-unavailable field is "simply not rendered"; only the
// configured-but-broken case became a visible inert one.
func TestNew_LinearUnconfiguredStillRendersNoField(t *testing.T) {
	m := newTestModel(t, testSetup{})
	if m.issue != nil {
		t.Fatal("Model.issue is non-nil with Linear neither configured nor broken, want nil")
	}
}

// --- finding I3: tilde expansion at the subprocess boundary --------------

// TestTildeProjectDirIsExpandedBeforeItLeavesThePlugin pins that a "~/..."
// project directory is expanded before it reaches git or herdr, while the
// field itself keeps the raw text the user typed (DirField.SetValidity
// keys its inline marker on it).
//
// `herdr worktree create --cwd` expands a leading "~" server-side, but
// `herdr workspace create --cwd` and `herdr pane split --cwd` do not: an
// unexpanded path would have produced a workspace rooted at a directory
// literally named "~".
func TestTildeProjectDirIsExpandedBeforeItLeavesThePlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	wantPath := filepath.Join(home, "Projects", "thing")

	git := newFakeGit()
	m := newTestModel(t, testSetup{Git: git})
	m.dir.SetCandidates(2, []string{"~/Projects/thing"})

	cmd := m.scheduleDirCheck(m.dir.Value())
	debounce := cmd().(dirDebounceMsg)
	if debounce.req.key != "~/Projects/thing" {
		t.Fatalf("the debounce request key = %q, want the RAW typed text (DirField keys its own marker on it)", debounce.req.key)
	}
	m2, checkCmd := m.handleDirDebounce(debounce)
	m = m2
	checkCmd()

	found := false
	for _, seen := range git.dirsSeen {
		if seen == wantPath {
			found = true
		}
		if strings.HasPrefix(seen, "~") {
			t.Errorf("an unexpanded path %q reached the git/filesystem layer", seen)
		}
	}
	if !found {
		t.Fatalf("git saw %v, want the expanded %q", git.dirsSeen, wantPath)
	}

	m.title.SetTitle("thing", false)
	if got := m.buildPlanInput().ProjectDir; got != wantPath {
		t.Fatalf("plan.Input.ProjectDir = %q, want the expanded %q -- this is the value that becomes `herdr workspace create --cwd`", got, wantPath)
	}
}

// --- minor M4: the base picker's HEAD row names the current branch ------

// TestBaseListNamesTheCurrentBranchOnTheHeadRow pins spec §6 field 4's own
// "row 0 `HEAD (<current branch>)`", which read a bare "HEAD" because
// gitx.CurrentBranch -- written and tested in Task 4 -- had no caller.
func TestBaseListNamesTheCurrentBranchOnTheHeadRow(t *testing.T) {
	git := newFakeGit()
	git.listBranchesResult = []string{"main", "release/1.4"}
	git.currentBranchResult = "main"
	m := newTestModel(t, testSetup{Git: git})
	m.worktree.SetGitTarget(true)
	m.worktree.SetOn(true)

	req := request{version: m.baseReqVersion, key: "/repo"}
	result := m.runBaseCheck(req)().(baseResultMsg)
	if result.head != "main" {
		t.Fatalf("baseResultMsg.head = %q, want the current branch %q", result.head, "main")
	}
	m2, _ := m.handleBaseResult(result)
	m = m2

	frame := ansi.Strip(m.worktree.BaseSection().View(60, m.worktree.BaseSection().Height(40)))
	if !strings.Contains(frame, "HEAD (main)") {
		t.Errorf("base picker = %q, want its HEAD row to name the current branch", frame)
	}
}

// TestBaseListFallsBackToABareHeadOnADetachedHead pins the degradation:
// gitx.CurrentBranch reports a detached HEAD as an empty name rather than
// an error, and the row must simply read "HEAD" rather than "HEAD ()".
func TestBaseListFallsBackToABareHeadOnADetachedHead(t *testing.T) {
	git := newFakeGit()
	git.listBranchesResult = []string{"main"}
	git.currentBranchResult = ""
	m := newTestModel(t, testSetup{Git: git})
	m.worktree.SetGitTarget(true)
	m.worktree.SetOn(true)

	req := request{version: m.baseReqVersion, key: "/repo"}
	m2, _ := m.handleBaseResult(m.runBaseCheck(req)().(baseResultMsg))
	m = m2

	frame := ansi.Strip(m.worktree.BaseSection().View(60, m.worktree.BaseSection().Height(40)))
	if strings.Contains(frame, "HEAD (") {
		t.Errorf("base picker = %q, want a bare HEAD row on a detached HEAD", frame)
	}
	if !strings.Contains(frame, "HEAD") {
		t.Errorf("base picker = %q, want the HEAD row still present", frame)
	}
}
