package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
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
	"github.com/ZviBaratz/herdr-draft/internal/picker"
	"github.com/ZviBaratz/herdr-draft/internal/plan"
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
func (r *fakeRunner) AgentStart(context.Context, herdrc.AgentStartReq) error   { return nil }
func (r *fakeRunner) AgentPrompt(context.Context, herdrc.AgentPromptReq) error { return nil }
func (r *fakeRunner) AgentRead(context.Context, string) (string, error) {
	// A painted, dialog-free pane. Not "" -- after #116 that is a refusal
	// rather than a ready state, and this fake exists to be uninteresting.
	return paintedIdleScreen, nil
}
func (r *fakeRunner) AwaitDetection(context.Context, string, time.Duration, time.Duration) error {
	return nil
}
func (r *fakeRunner) PaneRun(context.Context, string, []herdrc.EnvVar, []string) error { return nil }
func (r *fakeRunner) PaneClose(context.Context, string) error                          { return nil }
func (r *fakeRunner) WorktreeRemove(context.Context, string) error                     { return nil }
func (r *fakeRunner) WorkspaceClose(context.Context, string) error                     { return nil }

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

	// subdirs answers ListSubdirs, keyed by the RESOLVED directory a
	// browse asked for (see fakeGit.ResolvePath's own fixed "~" -> /home
	// substitution); a directory with no entry here lists as empty, which
	// is what an unreadable or empty real directory does too.
	subdirs map[string][]string

	// repoRoots answers RepoRoot, keyed by the directory queried -- see
	// fakeGit.RepoRoot.
	repoRoots     map[string]string
	repoRootErr   error
	repoRootCalls []string

	dirExistsCalls, isGitRepoCalls, listBranchesCalls, branchExistsCalls int
	currentBranchCalls                                                   int
	fetchPruneCalls                                                      []string
	// listSubdirsCalls records every directory actually read, in order, so
	// a test can assert that typing WITHIN one directory re-ranks what is
	// already on hand instead of re-reading it.
	listSubdirsCalls []string
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

// ListSubdirs and ResolvePath stand in for internal/pathx so this
// package's own tests never read the real filesystem. ResolvePath applies
// a FIXED, home-independent expansion ("~" -> "/home/test") plus a
// working-directory-independent one for "." -- the real pathx.Resolve is
// tested against t.TempDir in its own package; what matters here is that
// the app layer resolves before reading, and does so exactly once.
func (g *fakeGit) ListSubdirs(dir string, limit int) []string {
	g.listSubdirsCalls = append(g.listSubdirsCalls, dir)
	entries := g.subdirs[dir]
	if len(entries) > limit {
		return entries[:limit]
	}
	return entries
}

func (g *fakeGit) ResolvePath(path string) string {
	if path == "/" {
		return "/" // filepath.Abs keeps the root; TrimSuffix below would eat it
	}
	switch {
	case path == "~" || strings.HasPrefix(path, "~/"):
		return strings.TrimSuffix("/home/test"+strings.TrimPrefix(path, "~"), "/")
	case path == "." || strings.HasPrefix(path, "./"):
		return strings.TrimSuffix("/cwd"+strings.TrimPrefix(path, "."), "/")
	default:
		return strings.TrimSuffix(path, "/")
	}
}

func (g *fakeGit) IsGitRepo(dir string) bool {
	g.isGitRepoCalls++
	g.dirsSeen = append(g.dirsSeen, dir)
	return g.isGitRepo
}

// RepoRoot stands in for gitx.RepoRoot: repoRoots maps a queried directory
// to the ORIGIN repository root it belongs to, so a test can model a linked
// worktree and its origin resolving to one root without a real `git
// worktree add` (gitx's own tests do that against a real repository). A
// directory with no entry answers with itself, which is what a plain
// single-checkout repository does.
func (g *fakeGit) RepoRoot(_ context.Context, dir string) (string, error) {
	g.repoRootCalls = append(g.repoRootCalls, dir)
	g.dirsSeen = append(g.dirsSeen, dir)
	if g.repoRootErr != nil {
		return "", g.repoRootErr
	}
	if root, ok := g.repoRoots[dir]; ok {
		return root, nil
	}
	if !g.isGitRepo {
		return "", nil
	}
	return dir, nil
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
// testClockNow is the fixed instant every test model's Clock.Now answers
// with. Fixed and never time.Now for the reason testHomeDir is fixed: the
// account panel's reset times are relative to it (v3 spec §10.2), so a
// golden frame carrying one has to be the same bytes tomorrow.
var testClockNow = time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

var noSleep = Clock{
	Sleep: func(time.Duration) {},
	Now:   func() time.Time { return testClockNow },
}

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
	Projects     config.Projects
	Workspaces   []herdrc.WorkspaceInfo
	ClauthStatus clauth.Status
	// ClauthUnavailable stands in for Bootstrap's own "clauth is installed
	// and unreadable" outcome, so a test reaches the inert account row
	// through New's real construction path rather than by reaching past it
	// and poking the field.
	ClauthUnavailable string
	LinearCache       []linear.Issue
	// RepoConfig stands in for config.LoadRepoConfig (spec §11), so a test
	// gets a deterministic .herdr-draft.toml without putting one on disk.
	// nil leaves Deps.RepoConfig nil, which is the production reader --
	// see repoConfigModel and TestRepoConfig_ProductionLoaderReadsTheFile
	// in repoconfig_test.go.
	RepoConfig func(repoRoot string) config.RepoConfig
	// Picker stands in for a configured-and-probed account picker, so a test
	// reaches the auto row through New's real construction path rather than
	// by poking the field. nil is "no picker configured", the normal case.
	Picker pickerSource
	// PickerUnavailable stands in for Bootstrap's own "a picker was named and
	// could not be trusted" outcome, the same way ClauthUnavailable does for
	// clauth.
	PickerUnavailable string
}

// testHomeDir is the home every test model collapses paths against. It is
// a FIXED string, never os.UserHomeDir: the golden frames render project
// paths with the home prefix replaced by "~", and reading the real home
// here would make them differ on every machine whose home is not the
// author's.
const testHomeDir = "/home/zvi"

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
			Runner:     &fakeRunner{workspaces: s.Workspaces},
			Linear:     linSrc,
			Clauth:     clSrc,
			Git:        git,
			Clock:      noSleep,
			RepoConfig: s.RepoConfig,
			Picker:     s.Picker,
		},
		Ctx:               s.Ctx,
		Config:            cfg,
		State:             s.State,
		Projects:          s.Projects,
		Palette:           theme.Default(),
		StateDir:          t.TempDir(),
		Workspaces:        s.Workspaces,
		ClauthStatus:      s.ClauthStatus,
		ClauthUnavailable: s.ClauthUnavailable,
		PickerUnavailable: s.PickerUnavailable,
		LinearCache:       s.LinearCache,
		HomeDir:           testHomeDir,
	})
}

// fieldText is a form field's whole visible surface as the reader sees
// it: its one row plus its full panel, ANSI stripped. internal/form's own
// tests carry an identical helper -- v1's Section had one rendering to
// assert against and v2 has two, and a fact this package cares about (a
// verdict, a freshly applied issue) can be in either.
func fieldText(s form.Section, w int) string {
	return ansi.Strip(s.Row(w) + "\n" + s.Panel(w, s.PanelRows()))
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

	// A SECOND git-repo result for a different path resolves the same
	// default (no per-project memory here), so the toggle does not move and
	// there is nothing to re-check. Before spec §10's per-project memory
	// this was enforced by a one-shot worktreeDefaultApplied flag; now it
	// falls out of the value being unchanged, which is the behavior that
	// actually mattered -- see
	// TestDirResult_MemoryReAppliesAcrossASecondProjectChange for the case
	// where a second project SHOULD move it.
	req2 := request{version: m.dirReqVersion, key: "/other-repo"}
	m2, cmd = m.handleDirResult(dirResultMsg{req: req2, dirExists: true, isGitRepo: true})
	m = m2
	if cmd != nil {
		t.Fatalf("a second git-repo result produced a re-check cmd, want nil (the toggle did not move)")
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

	frame := fieldText(m.title, 60)
	if !strings.Contains(frame, "branch & label in use") {
		t.Fatalf("the title panel = %q, want it to contain the composed dup verdict", frame)
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
	frame := fieldText(m.title, 60)
	if strings.Contains(frame, "exists") || strings.Contains(frame, "in use") {
		t.Fatalf("the title panel = %q, fresh v2's clean verdict was not applied", frame)
	}

	// v1 (stale) resolves second, reporting a collision -- must be dropped,
	// not overwriting v2's already-applied clean verdict.
	m2, _ = m.handleTitleResult(titleResultMsg{req: request{version: v1, key: "t"}, branchExists: true, labelTaken: true})
	m = m2
	frame = fieldText(m.title, 60)
	if strings.Contains(frame, "in use") {
		t.Fatalf("the title panel = %q, stale v1 result was wrongly applied", frame)
	}
}

// TestIssueSelectionSeedsAndRespectsTouchedBranch pins the brief's own
// literal requirement: choosing a Linear issue seeds Title/Branch/Prompt,
// but a Branch the user has already typed into is left unclobbered.
func TestIssueSelectionSeedsAndRespectsTouchedBranch(t *testing.T) {
	m := newTestModel(t, testSetup{})

	// The user types into the branch directly, bypassing the focus ring
	// but reproducing the state it would have put the field in: a usable
	// git target with the worktree on (otherwise the branch part is not
	// reachable at all), focused, and one ↓ from the chips onto the
	// branch. The wrapped lineInput ignores key input while blurred
	// (bubbles/v2's own textinput.Model.Update: "if !m.focus { return m,
	// nil }"), which is what WorktreeField.Focus plus that ↓ arrange.
	m.worktree.SetGitTarget(true)
	m.worktree.SetOn(true)
	m.worktree.Focus()
	m.worktree.Update(key(tea.KeyDown, 0))
	m.worktree.Update(rn('x'))
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

	frame := ansi.Strip(m.worktree.Panel(60, m.worktree.PanelRows()))
	if !strings.Contains(frame, "couldn't list") {
		t.Fatalf("worktree panel = %q, want it to contain the \"couldn't list\" status", frame)
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
	frame := ansi.Strip(m.worktree.Panel(60, m.worktree.PanelRows()))
	if strings.Contains(frame, "couldn't list") {
		t.Fatalf("worktree panel = %q, fresh v2's success was not applied", frame)
	}

	// v1 (stale) resolves second, with an error -- must be dropped, not
	// clobbering v2's already-applied success with "couldn't list".
	m2, _ = m.handleBaseResult(baseResultMsg{req: request{version: v1, key: "old"}, err: true})
	m = m2
	frame = ansi.Strip(m.worktree.Panel(60, m.worktree.PanelRows()))
	if strings.Contains(frame, "couldn't list") {
		t.Fatalf("worktree panel = %q, stale v1 result was wrongly applied", frame)
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
//
// This test used to also assert `m.account == nil`, i.e. that a broken
// clauth showed nothing. That half WAS the defect: it made a clauth that
// crashed indistinguishable from one that is not installed, and the error
// explaining which was discarded at the point of failure. §13's own
// wording is "degrade ... to inert WITH A REASON", and there was no
// reason. The no-refusal half, which is what the test is named for, still
// holds and is still asserted.
func TestBootstrap_ClauthFailureDegrades(t *testing.T) {
	env := Env{ContextJSON: validContextJSON(), ConfigDir: t.TempDir(), StateDir: t.TempDir()}
	runner := &fakeRunner{}
	cl := &fakeClauth{err: context.DeadlineExceeded}
	m, err := Bootstrap(env, runner, cl, newFakeGit(), noSleep)
	if err != nil {
		t.Fatalf("Bootstrap with a failing clauth source returned an error, want it to degrade: %v", err)
	}
	if m.account == nil {
		t.Fatal("Model.account is nil after a clauth that was there and failed, want an inert row carrying the reason")
	}
	if m.account.Enabled() {
		t.Error("AccountField.Enabled() = true for an unreadable clauth, want inert (skipped by the focus ring)")
	}
	if got := fieldText(m.account, 80); !strings.Contains(got, "unavailable") {
		t.Errorf("account row = %q, want it to say it is unavailable", got)
	}
}

// TestBootstrap_ClauthNotInstalledShowsNothing is the other side of the
// same distinction, and the one that keeps the fix from becoming noise.
//
// Most people who install this plugin have never heard of clauth. It is an
// optional integration, and its absence is the documented, deliberate
// reason the account row does not render ("a static, by-design check, not
// a bug"). Announcing a missing optional dependency to everyone who does
// not have it would be worse than the silence this issue is about.
//
// exec.ErrNotFound is what separates the two: the binary is not there,
// versus it was there and did not work.
func TestBootstrap_ClauthNotInstalledShowsNothing(t *testing.T) {
	env := Env{ContextJSON: validContextJSON(), ConfigDir: t.TempDir(), StateDir: t.TempDir()}
	cl := &fakeClauth{err: fmt.Errorf("clauth status --json: %w", &exec.Error{Name: "clauth", Err: exec.ErrNotFound})}

	m, err := Bootstrap(env, &fakeRunner{}, cl, newFakeGit(), noSleep)
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if m.account != nil {
		t.Error("Model.account is non-nil with clauth not installed, want no row at all")
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

// TestNew_SectionOrder pins v2 spec §6's row order at the ONE place it is
// declared -- internal/app's own section slice -- including the two
// fields whose presence is a static precondition (issue, account) and the
// internal Create section New always appends last.
//
// It replaces TestNew_WorktreeSectionsAreAdjacent, whose carried
// requirement ("the three worktree zones must still read as one visual
// group") the collapse satisfies by construction: there is one worktree
// section now, so there is nothing left to separate. Note the test that
// preceded it also had to survive a Tab-walk trap -- Tab navigation SKIPS
// disabled sections (focus.go's nextEnabled), so an inserted-in-between
// regression on Placement went undetected until the assertion moved to
// SectionIDs(), which reports the real construction order INCLUDING
// disabled sections. This one is built on the same accessor for the same
// reason.
func TestNew_SectionOrder(t *testing.T) {
	full := newTestModel(t, testSetup{
		Linear:       &fakeLinear{},
		Clauth:       &fakeClauth{},
		ClauthStatus: clauth.Status{Schema: 1, Profiles: []clauth.Profile{{Name: "a"}, {Name: "b"}}},
	})
	want := []string{"issue", "title", "prompt", "dir", "worktree", "placement", "agent", "account", "create"}
	if got := full.form.SectionIDs(); !equalStrings(got, want) {
		t.Errorf("SectionIDs() for the widest configuration = %v, want %v.\nA field ADDED here also needs an entry in internal/form's panelRowsCases (field_rows_test.go), which states the panel rows each field books: that table's own coverage guard iterates the form package's hand-maintained fixtures, so it cannot see a section added only in this layer.",
			got, want)
	}

	// Linear unconfigured and fewer than two clauth profiles: those two
	// rows are absent entirely (v2 spec §6.1's "absent by design"), and
	// the rest keep their order.
	minimal := newTestModel(t, testSetup{})
	wantMinimal := []string{"title", "prompt", "dir", "worktree", "placement", "agent", "create"}
	if got := minimal.form.SectionIDs(); !equalStrings(got, wantMinimal) {
		t.Errorf("SectionIDs() for the minimal configuration = %v, want %v", got, wantMinimal)
	}
}

// TestNew_FocusOpensOnTitle pins v2 spec §8's opening state: the whole
// redesign is "open, type a title, Enter", which is only true if focus
// starts on the title rather than on the first enabled section.
//
// Both configurations, because the section list is what a first-enabled
// walk would key off and the two differ in exactly that: the full one
// puts Issue above Title, the minimal one (Linear unconfigured, fewer
// than two clauth profiles -- v2 spec §6.1's "absent by design") starts
// the stack at Title itself, so a regression to first-enabled would look
// correct there and only there.
func TestNew_FocusOpensOnTitle(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup testSetup
	}{
		{"full-config", testSetup{
			Linear:       &fakeLinear{issues: frameIssues()},
			LinearCache:  frameIssues(),
			Clauth:       &fakeClauth{status: frameClauthStatus()},
			ClauthStatus: frameClauthStatus(),
		}},
		{"minimal-config", testSetup{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel(t, tc.setup)
			m.Init()
			if got := m.form.FocusedID(); got != "title" {
				t.Errorf("FocusedID() after Init = %q, want %q (sections: %v)", got, "title", m.form.SectionIDs())
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// --- agent kinds: favorites-first (carried requirement) --------------------

func TestOrderedAgentKinds_FavoritesFirst(t *testing.T) {
	got := OrderedAgentKinds([]string{"codex", "claude"})
	if len(got) < 2 || got[0] != "codex" || got[1] != "claude" {
		t.Fatalf("OrderedAgentKinds favorites prefix = %v, want [codex claude ...]", got[:2])
	}
	// Every known kind must still be reachable exactly once.
	seen := map[string]int{}
	for _, k := range got {
		seen[k]++
	}
	for _, k := range knownAgentKinds {
		if seen[k] != 1 {
			t.Fatalf("known kind %q appears %d times in OrderedAgentKinds output, want exactly 1", k, seen[k])
		}
	}
	if len(got) != len(knownAgentKinds) {
		t.Fatalf("OrderedAgentKinds returned %d kinds, want %d (favorites are already known kinds here)", len(got), len(knownAgentKinds))
	}
}

func TestOrderedAgentKinds_DropsEmptyAndDuplicates(t *testing.T) {
	got := OrderedAgentKinds([]string{"claude", "", "claude"})
	if got[0] != "claude" {
		t.Fatalf("OrderedAgentKinds[0] = %q, want %q", got[0], "claude")
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
	if result.err == nil {
		t.Fatalf("linearResultMsg.err = nil after a failing AssignedIssues call, want the error")
	}
	// The ERROR, not merely that there was one: carrying a bool here is
	// what left the panel with nothing to say.
	if !errors.Is(result.err, context.DeadlineExceeded) {
		t.Errorf("linearResultMsg.err = %v, want it to wrap the source's own error", result.err)
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
	frame := fieldText(m.issue, 80)
	if !strings.Contains(frame, "ENG-5") {
		t.Fatalf("the issue panel = %q, want it to contain the freshly applied issue", frame)
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

	m2, _ := m.handleLinearResult(linearResultMsg{err: context.DeadlineExceeded})
	m = m2

	m.issue.Focus()
	frame := fieldText(m.issue, 80)
	if !strings.Contains(frame, "ENG-6") {
		t.Fatalf("the issue panel = %q, want the cache-rendered issue still present after a failed refresh", frame)
	}
	// ... and still PICKABLE. Reporting the failure by marking the field
	// inert would have removed the very list this test exists to protect.
	if !m.issue.Enabled() {
		t.Error("IssueField.Enabled() = false after a failed refresh, want the stale list to stay usable")
	}
}

// TestLinearResult_ErrorReachesThePanel is the defect this pair was
// missing. A fetch failure with nothing cached used to render the panel's
// "no assigned issues" -- a statement about the user's Linear queue, made
// when nobody had successfully looked at the queue. A revoked or mistyped
// API key was therefore byte-identical to genuinely having nothing
// assigned.
func TestLinearResult_ErrorReachesThePanel(t *testing.T) {
	m := newTestModel(t, testSetup{Linear: &fakeLinear{}}) // no cache

	m2, _ := m.handleLinearResult(linearResultMsg{
		err: errors.New("linear assigned issues: unexpected status 401: {\n  \"error\": \"authentication failed\"\n}"),
	})
	m = m2

	m.issue.Focus()
	frame := fieldText(m.issue, 80)
	if strings.Contains(frame, "no assigned issues") {
		t.Errorf("the issue panel = %q, want the failure reason rather than a claim about the queue", frame)
	}
	if !strings.Contains(frame, "401") {
		t.Errorf("the issue panel = %q, want it to name the failure", frame)
	}
}

// TestLinearResult_SuccessClearsAPreviousError covers the other direction:
// a transient failure followed by a working refresh must not leave a stale
// explanation sitting under a fresh list.
func TestLinearResult_SuccessClearsAPreviousError(t *testing.T) {
	m := newTestModel(t, testSetup{Linear: &fakeLinear{}})

	m2, _ := m.handleLinearResult(linearResultMsg{err: errors.New("linear assigned issues: request: dial tcp: timeout")})
	m3, _ := m2.handleLinearResult(linearResultMsg{issues: []linear.Issue{{Identifier: "ENG-7", Title: "recovered"}}})
	m = m3

	m.issue.Focus()
	frame := fieldText(m.issue, 80)
	if strings.Contains(frame, "dial tcp") {
		t.Errorf("the issue panel = %q, want the stale failure reason cleared by the successful refresh", frame)
	}
	if !strings.Contains(frame, "ENG-7") {
		t.Errorf("the issue panel = %q, want the freshly fetched issue", frame)
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
	if !strings.Contains(fieldText(m.account, 80), "a") {
		t.Fatalf("the account panel does not show the reloaded profile")
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

// --- the header's project context (v2 spec §4) ---------------------------

// TestHeaderContextNamesTheSelectedProjectAndItsBranch pins v2 spec §4's
// header: "live context for the SELECTED project: repository name and its
// current branch, not the invoking workspace." The distinction is the
// whole point -- the popup is launched from wherever the user happens to
// be, and the header must describe the session about to be created.
func TestHeaderContextNamesTheSelectedProjectAndItsBranch(t *testing.T) {
	git := newFakeGit()
	git.listBranchesResult = []string{"main"}
	git.currentBranchResult = "zvi/some-branch"
	m := newTestModel(t, testSetup{
		Git: git,
		Ctx: herdrc.Context{WorkspaceCwd: "/home/zvi/Projects/herdr-draft"},
	})

	req := request{version: m.baseReqVersion, key: m.dir.Value()}
	m2, _ := m.handleBaseResult(m.runBaseCheck(req)().(baseResultMsg))
	m = m2

	header := ansi.Strip(strings.SplitN(m.form.ViewAt(80, 24), "\n", 2)[0])
	if !strings.Contains(header, "herdr-draft · zvi/some-branch") {
		t.Errorf("header = %q, want the selected project's name and branch", header)
	}
}

// TestHeaderContextOnANonRepositoryDropsTheBranchHalf states what the
// spec does not settle: a project that is not a git repository shows its
// NAME ALONE, with no separator and no branch.
//
// The alternative -- "myproject · not a repository" -- would put a
// verdict in a place reserved for context, and repeat what the project
// row's own inert cell says one line further down. It also has to hold
// when the previous project WAS a repository: the branch must go, or the
// header quietly attributes one project's branch to another.
func TestHeaderContextOnANonRepositoryDropsTheBranchHalf(t *testing.T) {
	git := newFakeGit()
	git.listBranchesResult = []string{"main"}
	git.currentBranchResult = "main"
	m := newTestModel(t, testSetup{
		Git: git,
		Ctx: herdrc.Context{WorkspaceCwd: "/home/zvi/Projects/herdr-draft"},
	})

	req := request{version: m.baseReqVersion, key: m.dir.Value()}
	m2, _ := m.handleBaseResult(m.runBaseCheck(req)().(baseResultMsg))
	m = m2
	if header := ansi.Strip(strings.SplitN(m.form.ViewAt(80, 24), "\n", 2)[0]); !strings.Contains(header, "· main") {
		t.Fatalf("setup: header = %q, want a branch to lose", header)
	}

	// The project moves to something git cannot list refs for.
	m2, _ = m.handleBaseResult(baseResultMsg{req: request{version: m.baseReqVersion, key: "/tmp/plain"}, err: true})
	m = m2

	header := ansi.Strip(strings.SplitN(m.form.ViewAt(80, 24), "\n", 2)[0])
	if strings.Contains(header, "· main") {
		t.Errorf("header = %q, still carries the previous project's branch", header)
	}
	if !strings.Contains(header, "herdr-draft") {
		t.Errorf("header = %q, want the project name to survive", header)
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

	frame := ansi.Strip(m.worktree.Panel(60, m.worktree.PanelRows()))
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

	frame := ansi.Strip(m.worktree.Panel(60, m.worktree.PanelRows()))
	if strings.Contains(frame, "HEAD (") {
		t.Errorf("base picker = %q, want a bare HEAD row on a detached HEAD", frame)
	}
	if !strings.Contains(frame, "HEAD") {
		t.Errorf("base picker = %q, want the HEAD row still present", frame)
	}
}

// --- path-mode directory browsing (spec §6 field 2) ----------------------

// typeDir types into the focused Project field, the way a user reaches
// path mode: the app layer reacts to DirField.Typed(), never to a setter
// of its own.
func typeDir(m *Model, s string) {
	m.dir.Focus()
	for _, r := range s {
		m.dir.Update(rn(r))
	}
}

// backspaceDir deletes n runes from the Project field.
func backspaceDir(m *Model, n int) {
	m.dir.Focus()
	for range n {
		m.dir.Update(key(tea.KeyBackspace, 0))
	}
}

// browseModel returns a Model whose fragment-mode pool is two recents and
// whose fake filesystem holds ~/Projects/{atrium,herdr-draft}.
func browseModel(t *testing.T) (Model, *fakeGit) {
	t.Helper()
	git := newFakeGit()
	git.subdirs = map[string][]string{
		"/home/test/Projects": {"/home/test/Projects/atrium", "/home/test/Projects/herdr-draft"},
	}
	m := newTestModel(t, testSetup{
		Git:   git,
		State: config.State{Recents: []string{"/repo-alpha", "/repo-beta"}},
	})
	return m, git
}

// dirRows renders the Project field the way the popup does, so a test can
// assert on what the user would actually see -- DirField's own item list
// is private to the form package, and the rendered rows are the honest
// end of this pipeline anyway.
//
// It STRIPS, and that is load-bearing rather than tidy: v3 spec §8.4's
// match highlighting splits a candidate row at the matched run, so
// "/repo-alpha" filtered by "repo" renders as "/" then an accent-styled
// "repo" then "-alpha", and every strings.Contains below it went red on a
// screen that was in fact correct. A caller here asks which DIRECTORIES
// are on offer; the escapes that answer a different question have no
// business in the comparison.
func dirRows(m Model) string {
	return ansi.Strip(m.dir.Row(60) + "\n" + m.dir.Panel(60, m.dir.PanelRows()))
}

// runBrowseRound drives one full browse: the debounce firing, then the
// listing, then the result -- each through Model.Update, so the real
// message dispatch (not just the handlers) is what the test exercises.
func runBrowseRound(t *testing.T, m Model, cmds []tea.Cmd) Model {
	t.Helper()
	for _, cmd := range cmds {
		if cmd == nil {
			continue
		}
		msg := cmd()
		debounce, ok := msg.(browseDebounceMsg)
		if !ok {
			continue
		}
		next, listCmd := m.Update(debounce)
		m = next.(Model)
		if listCmd == nil {
			t.Fatalf("browse debounce produced no listing cmd")
		}
		next, _ = m.Update(listCmd())
		m = next.(Model)
	}
	return m
}

// TestTypingAPathBrowsesTheDirectory is the feature: a "~"-prefixed entry
// lists that directory's subdirectories instead of ranking the project
// pool by basename.
func TestTypingAPathBrowsesTheDirectory(t *testing.T) {
	m, git := browseModel(t)

	if rows := dirRows(m); !strings.Contains(rows, "/repo-alpha") {
		t.Fatalf("fragment mode does not show the project pool:\n%s", rows)
	}

	typeDir(&m, "~/Projects/")
	cmds := m.reactToChanges()

	// Entering path mode drops the project pool immediately -- the rows
	// must not show unrelated projects while the listing is in flight.
	if rows := dirRows(m); strings.Contains(rows, "/repo-alpha") {
		t.Errorf("project pool still on screen while the first listing is in flight:\n%s", rows)
	}
	if got := m.dir.Value(); got != "/home/test/Projects" {
		t.Errorf("selection while browsing = %q, want the expanded literal-path fallback", got)
	}

	m = runBrowseRound(t, m, cmds)

	if want := []string{"/home/test/Projects"}; !slicesEqual(git.listSubdirsCalls, want) {
		t.Errorf("ListSubdirs calls = %v, want %v (resolved exactly once)", git.listSubdirsCalls, want)
	}
	rows := dirRows(m)
	for _, want := range []string{"atrium", "herdr-draft"} {
		if !strings.Contains(rows, want) {
			t.Errorf("browsed rows missing %q:\n%s", want, rows)
		}
	}
}

// TestTypingWithinOneDirectoryDoesNotRelist pins atrium's own
// per-directory memoization, expressed here as a diff on browseDir: "he"
// after "~/Projects/" re-ranks the listing already on hand.
func TestTypingWithinOneDirectoryDoesNotRelist(t *testing.T) {
	m, git := browseModel(t)

	typeDir(&m, "~/Projects/")
	m = runBrowseRound(t, m, m.reactToChanges())

	typeDir(&m, "he")
	cmds := m.reactToChanges()
	m = runBrowseRound(t, m, cmds)

	if len(git.listSubdirsCalls) != 1 {
		t.Fatalf("ListSubdirs calls = %v, want exactly one (same parent directory)", git.listSubdirsCalls)
	}
	rows := dirRows(m)
	if !strings.Contains(rows, "herdr-draft") {
		t.Errorf("filtered rows lost the match:\n%s", rows)
	}
	if strings.Contains(rows, "atrium") {
		t.Errorf("filtered rows still show a non-match:\n%s", rows)
	}
}

// TestDescendingListsTheNewParent pins the other half: a further "/" IS a
// new parent, so it does re-list.
func TestDescendingListsTheNewParent(t *testing.T) {
	m, git := browseModel(t)
	git.subdirs["/home/test/Projects/herdr-draft"] = []string{"/home/test/Projects/herdr-draft/internal"}

	typeDir(&m, "~/Projects/")
	m = runBrowseRound(t, m, m.reactToChanges())
	typeDir(&m, "herdr-draft/")
	m = runBrowseRound(t, m, m.reactToChanges())

	want := []string{"/home/test/Projects", "/home/test/Projects/herdr-draft"}
	if !slicesEqual(git.listSubdirsCalls, want) {
		t.Fatalf("ListSubdirs calls = %v, want %v", git.listSubdirsCalls, want)
	}
	if rows := dirRows(m); !strings.Contains(rows, "internal") {
		t.Errorf("rows do not show the descended listing:\n%s", rows)
	}
}

// TestLeavingPathModeRestoresTheProjectPool pins the return trip: the
// pool DirField holds is whichever was supplied last, so fragment mode
// has to put the project list back explicitly.
func TestLeavingPathModeRestoresTheProjectPool(t *testing.T) {
	m, _ := browseModel(t)

	typeDir(&m, "~/Projects/")
	m = runBrowseRound(t, m, m.reactToChanges())
	if rows := dirRows(m); !strings.Contains(rows, "atrium") {
		t.Fatalf("browse did not take effect:\n%s", rows)
	}

	backspaceDir(&m, len("~/Projects/"))
	typeDir(&m, "repo")
	m.reactToChanges()

	rows := dirRows(m)
	if !strings.Contains(rows, "/repo-alpha") {
		t.Errorf("project pool was not restored on leaving path mode:\n%s", rows)
	}
	if strings.Contains(rows, "atrium") {
		t.Errorf("browsed rows survived the return to fragment mode:\n%s", rows)
	}
}

// TestStaleBrowseResultIsDropped pins the staleness guard at the result
// end: a slow listing for a directory the user has already typed past
// must not replace the fresher one.
func TestStaleBrowseResultIsDropped(t *testing.T) {
	m, _ := browseModel(t)

	stale := m.scheduleBrowse("~/old/")().(browseDebounceMsg).req
	typeDir(&m, "~/Projects/")
	m = runBrowseRound(t, m, m.reactToChanges())

	next, _ := m.Update(browseResultMsg{req: stale, entries: []string{"/home/test/old/ghost"}})
	m = next.(Model)

	rows := dirRows(m)
	if strings.Contains(rows, "ghost") {
		t.Errorf("stale listing was applied over the fresher one:\n%s", rows)
	}
	if !strings.Contains(rows, "atrium") {
		t.Errorf("fresh listing was lost:\n%s", rows)
	}
}

// TestLeavingPathModeInvalidatesAnInFlightListing pins the other half of
// that guard: leaving path mode has no result of its own to outrank a
// listing already in flight, so it bumps the counter itself.
func TestLeavingPathModeInvalidatesAnInFlightListing(t *testing.T) {
	m, _ := browseModel(t)

	typeDir(&m, "~/Projects/")
	cmds := m.reactToChanges()
	var inFlight request
	for _, cmd := range cmds {
		if cmd == nil {
			continue
		}
		if debounce, ok := cmd().(browseDebounceMsg); ok {
			inFlight = debounce.req
		}
	}
	if inFlight.version == 0 {
		t.Fatalf("entering path mode scheduled no browse")
	}

	backspaceDir(&m, len("~/Projects/"))
	typeDir(&m, "repo")
	m.reactToChanges()

	next, _ := m.Update(browseResultMsg{req: inFlight, entries: []string{"/home/test/Projects/atrium"}})
	m = next.(Model)

	rows := dirRows(m)
	if strings.Contains(rows, "atrium") {
		t.Errorf("an in-flight listing landed after the user left path mode:\n%s", rows)
	}
	if !strings.Contains(rows, "/repo-alpha") {
		t.Errorf("project pool did not survive the late listing:\n%s", rows)
	}
}

// TestAnEmptyListingClearsThePreviousDirectorysChildren pins
// handleBrowseResult's own "install an empty listing too": an unreadable
// directory must not leave the previous one's children on screen under a
// path they do not belong to.
func TestAnEmptyListingClearsThePreviousDirectorysChildren(t *testing.T) {
	m, _ := browseModel(t)

	typeDir(&m, "~/Projects/")
	m = runBrowseRound(t, m, m.reactToChanges())
	if rows := dirRows(m); !strings.Contains(rows, "atrium") {
		t.Fatalf("browse did not take effect, so this test would pass vacuously:\n%s", rows)
	}

	typeDir(&m, "nowhere/")
	m = runBrowseRound(t, m, m.reactToChanges())

	rows := dirRows(m)
	if strings.Contains(rows, "atrium") {
		t.Errorf("the previous directory's children survived an empty listing:\n%s", rows)
	}
	if !strings.Contains(rows, "/home/test/Projects/nowhere") {
		t.Errorf("the literal-path fallback is not what remains:\n%s", rows)
	}
}

func slicesEqual(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestBrowsingIsReachableByTypingThroughUpdate closes the gap every other
// test in this section leaves open: they drive DirField directly and then
// call reactToChanges by hand. This one sends real keystrokes through
// Model.Update -- the only path a user has -- and asserts a browse
// actually gets scheduled at the end of it.
func TestBrowsingIsReachableByTypingThroughUpdate(t *testing.T) {
	m, _ := browseModel(t)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	m = next.(Model)
	m.Init()
	// v2 opens focus on the title (spec §8), so reaching the project row
	// is now a deliberate move -- exactly as it is for a real user.
	if cmd := m.form.FocusByID("dir"); cmd != nil {
		cmd()
	}

	// Only the LAST keystroke's cmd is flattened: every keystroke also
	// batches the text input's own blink timer, which really does sleep.
	var last tea.Cmd
	versionBefore := m.browseReqVersion
	for _, r := range "~/Projects/" {
		n, cmd := m.Update(rn(r))
		m, last = n.(Model), cmd
	}
	if m.browseReqVersion == versionBefore {
		t.Fatalf("typing a path through Update never bumped the browse counter (Typed() = %q)", m.dir.Typed())
	}

	var scheduled bool
	for _, msg := range flatten(last) {
		if _, ok := msg.(browseDebounceMsg); ok {
			scheduled = true
		}
	}
	if !scheduled {
		t.Fatalf("typing a path through Update scheduled no browse (Typed() = %q)", m.dir.Typed())
	}
	if m.browseDir != "~/Projects/" {
		t.Errorf("browseDir = %q, want the raw typed parent", m.browseDir)
	}
}

// flatten runs cmd and returns every message it produced, descending into
// tea.BatchMsg (which is itself a []tea.Cmd, not a message a Model would
// ever see).
func flatten(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}
	var msgs []tea.Msg
	for _, c := range batch {
		msgs = append(msgs, flatten(c)...)
	}
	return msgs
}

// TestAgentKindSeedingPrecedence pins the three-layer default for the
// Agent field: favorites[0], then `[agents] default` (spec §12, which
// nothing read until 2026-09-01), then the last kind actually launched.
func TestAgentKindSeedingPrecedence(t *testing.T) {
	favorites := []string{"claude", "codex"}

	cases := []struct {
		name    string
		cfg     config.AgentsConfig
		state   config.State
		want    string
		wantWhy string
	}{
		{
			name:    "favorites[0] when nothing else is set",
			cfg:     config.AgentsConfig{Favorites: favorites},
			want:    "claude",
			wantWhy: "SetKinds' own index-0 default",
		},
		{
			name:    "[agents] default overrides favorites[0]",
			cfg:     config.AgentsConfig{Favorites: favorites, Default: "codex"},
			want:    "codex",
			wantWhy: "the configured default",
		},
		{
			name:    "[agents] default may name a kind outside the favorites row",
			cfg:     config.AgentsConfig{Favorites: favorites, Default: "gemini"},
			want:    "gemini",
			wantWhy: "a default reachable only through more…",
		},
		{
			name:    "last-used wins over the configured default",
			cfg:     config.AgentsConfig{Favorites: favorites, Default: "codex"},
			state:   config.State{LastKind: "claude"},
			want:    "claude",
			wantWhy: "last-used.json",
		},
		{
			name:    "an unknown configured default is ignored, not guessed at",
			cfg:     config.AgentsConfig{Favorites: favorites, Default: "not-an-agent"},
			want:    "claude",
			wantWhy: "SetKind's own unknown-kind no-op",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel(t, testSetup{
				Config: config.Config{Agents: tc.cfg},
				State:  tc.state,
			})
			if got := m.agent.Value(); got != tc.want {
				t.Errorf("agent kind = %q, want %q (%s)", got, tc.want, tc.wantWhy)
			}
		})
	}
}

// TestAgentSeedingLeavesTheFieldCoherent covers the combination the
// precedence table above misses: a `[agents] default` outside the
// favorites row followed by a favorite last-used kind. The two SetKind
// calls used to leave the "more…" list expanded on the default while the
// chip row showed the last-used kind, so the chip keys were inert and the
// first Down jumped to an unrelated kind.
func TestAgentSeedingLeavesTheFieldCoherent(t *testing.T) {
	m := newTestModel(t, testSetup{
		Config: config.Config{Agents: config.AgentsConfig{
			Favorites: []string{"claude", "codex"},
			Default:   "gemini",
		}},
		State: config.State{LastKind: "claude"},
	})

	if got := m.agent.Value(); got != "claude" {
		t.Fatalf("agent kind = %q, want %q (last-used wins)", got, "claude")
	}

	m.agent.Focus()
	m.agent.Update(key(tea.KeyRight, 0))
	if got := m.agent.Value(); got != "codex" {
		t.Errorf("Right moved to %q, want %q -- the chip row is inert while the more… list is expanded", got, "codex")
	}
}

// pumpAsync runs cmds the app itself produced and feeds every async
// message they yield back through Update, until the pipelines settle.
// Nothing is fabricated: a check the app never scheduled never runs, which
// is the whole point when the assertion is "did the app notice?". Blink
// timers and other unrelated cmds are dropped rather than run.
func pumpAsync(t *testing.T, m Model, cmds []tea.Cmd) Model {
	t.Helper()
	queue := append([]tea.Cmd(nil), cmds...)
	for range 32 {
		if len(queue) == 0 {
			return m
		}
		cmd := queue[0]
		queue = queue[1:]
		if cmd == nil {
			continue
		}
		switch msg := cmd().(type) {
		case dirDebounceMsg, dirResultMsg, baseDebounceMsg, baseResultMsg,
			browseDebounceMsg, browseResultMsg:
			next, out := m.Update(msg)
			m = next.(Model)
			queue = append(queue, out)
		case tea.BatchMsg:
			queue = append(queue, msg...)
		}
	}
	t.Fatalf("async pipelines did not settle")
	return m
}

// TestSelectionStaysValidatedAcrossPoolChanges pins the consequence of
// the browse source being the first one that moves DirField's SELECTION
// on its own: every pool swap goes through widgets.Picker.SetItems, which
// re-anchors the cursor by ID and falls back to the numeric position when
// the previous selection is gone -- so Value() can change with no
// keystroke behind it. reactToChanges is the only thing in this package
// that notices a value change, and message handlers that bypass
// routeToForm never run it. Without that, the validity marker, the
// worktree git-target gate, the base-ref list and the submit-blocking
// dirInvalid flag all keep describing a directory the user has left, and
// submit can hand herdr a path nothing ever checked.
func TestSelectionStaysValidatedAcrossPoolChanges(t *testing.T) {
	m, git := browseModel(t)

	assertValidated := func(step string) {
		t.Helper()
		selected := m.dir.Value()
		if m.lastDir != selected {
			t.Fatalf("%s: lastDir = %q but the selection is %q -- the app never noticed it moved", step, m.lastDir, selected)
		}
		for _, dir := range git.dirsSeen {
			if dir == selected {
				return
			}
		}
		t.Errorf("%s: %q was never handed to DirExists/IsGitRepo; dirs seen: %v", step, selected, git.dirsSeen)
	}

	typeDir(&m, "~/Projects/")
	m = pumpAsync(t, m, m.reactToChanges())
	assertValidated("after the first listing")

	// Move onto a real browsed child, then leave path mode: restoring the
	// project pool drops that child, so the picker re-anchors onto a
	// project the validity pipeline has never been asked about.
	m.dir.Update(key(tea.KeyDown, 0))
	m = pumpAsync(t, m, m.reactToChanges())
	assertValidated("after selecting a browsed child")

	backspaceDir(&m, len("~/Projects/"))
	typeDir(&m, "repo")
	m = pumpAsync(t, m, m.reactToChanges())
	assertValidated("after leaving path mode")

	// A listing that DISPLACES the selection: the picker cannot re-anchor
	// by ID, falls back to the numeric position, and Value() moves with no
	// keystroke behind it. Only handleBrowseResult can notice.
	backspaceDir(&m, len("repo"))
	typeDir(&m, "~/Projects/")
	m = pumpAsync(t, m, m.reactToChanges())
	m.dir.Update(key(tea.KeyUp, 0))
	m.dir.Update(key(tea.KeyUp, 0))
	m = pumpAsync(t, m, m.reactToChanges())
	if m.dir.Value() != "/home/test/Projects/atrium" {
		t.Fatalf("setup: selection is %q, want a real browsed child", m.dir.Value())
	}

	req := m.scheduleBrowse("~/Projects/")().(browseDebounceMsg).req
	next, cmd := m.Update(browseResultMsg{req: req, entries: []string{"/home/test/Projects/zeta"}})
	m = next.(Model)
	m = pumpAsync(t, m, []tea.Cmd{cmd})
	assertValidated("after a re-listing dropped the selected entry")
}

// TestChangingParentDropsThePreviousDirectorysChildren pins the fourth
// path-mode transition (path -> path, a DIFFERENT parent). Leaving the
// old listing on offer for the debounce window means the picker shows,
// and can SELECT, a sibling of the directory the user typed past -- so a
// submit in that window creates the session in the wrong directory, and
// Tab completes a path assembled from a directory that was never listed.
func TestChangingParentDropsThePreviousDirectorysChildren(t *testing.T) {
	m, _ := browseModel(t)

	typeDir(&m, "~/Projects/")
	m = pumpAsync(t, m, m.reactToChanges())
	if rows := dirRows(m); !strings.Contains(rows, "atrium") {
		t.Fatalf("setup: the first listing did not land:\n%s", rows)
	}

	// Descend: the new parent has not been listed yet.
	typeDir(&m, "herdr-draft/")
	m.reactToChanges()

	if got := m.dir.Value(); got != "/home/test/Projects/herdr-draft" {
		t.Errorf("while the listing is in flight the selection is %q, want the typed path itself", got)
	}
	if rows := dirRows(m); strings.Contains(rows, "atrium") {
		t.Errorf("the previous parent's children are still on offer:\n%s", rows)
	}

	// Tab must not complete from a directory that was never listed.
	typeDir(&m, "at")
	m.reactToChanges()
	if m.dir.Complete() {
		t.Errorf("Complete() built %q out of the previous directory's children", m.dir.Typed())
	}
}

// TestAccountPin_BrowsingDoesNotReachThePlan is v3 spec §10.3 asserted
// where it actually matters: on plan.Input, the struct that becomes
// `clauth <profile>` on a real command line.
//
// Before the fix, AccountField.Pin() returned the picker's cursor
// position, so tabbing onto the account row and pressing Down once was
// enough to launch the session under an account the user never chose.
// This drives the REAL model -- the same Update path a keypress takes --
// rather than the field in isolation, because the field-level test cannot
// see accountPin's own claude-kind gate or buildPlanInput at all.
func TestAccountPin_BrowsingDoesNotReachThePlan(t *testing.T) {
	m := newTestModel(t, testSetup{
		Ctx:          herdrc.Context{WorkspaceCwd: "/home/zvi/Projects/herdr-draft"},
		Clauth:       &fakeClauth{status: frameClauthStatus()},
		ClauthStatus: frameClauthStatus(),
	})
	if m.account == nil {
		t.Fatal("setup: no account field was constructed")
	}
	m.form.FocusByID("account")

	press := func(k tea.KeyPressMsg) {
		next, _ := m.Update(k)
		m = next.(Model)
	}

	// Browse the whole list and leave.
	for i := 0; i < 3; i++ {
		press(key(tea.KeyDown, 0))
	}
	press(key(tea.KeyTab, 0))
	if got := m.PlanInput().AccountPin; got != "" {
		t.Errorf("AccountPin after browsing the account list and tabbing away = %q, want \"\"", got)
	}

	// Commit deliberately, and it does reach the plan. The cursor is
	// still where the browsing above left it -- clamped on the last
	// profile -- which is the distinction stated as an assertion: the
	// CURSOR survived tabbing away and back, and the pin did not.
	m.form.FocusByID("account")
	press(key(tea.KeyEnter, 0))
	if got, want := m.PlanInput().AccountPin, "work"; got != want {
		t.Errorf("AccountPin after committing the row the cursor rests on = %q, want %q", got, want)
	}
}

// TestTitleSessions_DropsTheInvokingWorkspace pins the app-side half of
// v3 spec §9: the mapping down to internal/form's own value type (the
// package must have "no knowledge of herdr"), and the one workspace that
// does not belong on the list.
//
// The invoking workspace is the one the popup is open in. It is on screen
// behind the form already, and listing it would spend a row of a fifteen-
// row panel saying "you are here".
func TestTitleSessions_DropsTheInvokingWorkspace(t *testing.T) {
	got := titleSessions([]herdrc.WorkspaceInfo{
		{WorkspaceID: "ws-1", Label: "here", AgentStatus: "working", PaneCount: 1},
		{WorkspaceID: "ws-2", Label: "elsewhere", AgentStatus: "idle", PaneCount: 3,
			Worktree: &herdrc.ContextWorktree{RepoName: "quantivly"}},
		{WorkspaceID: "ws-3", Label: "plain-shell", AgentStatus: "idle", PaneCount: 1},
	}, "ws-1")

	want := []form.Session{
		{Label: "elsewhere", Status: "idle", Panes: 3, Repo: "quantivly"},
		// herdr omits the worktree key entirely for a workspace it has no
		// repository context for, so Repo is empty rather than the mapping
		// panicking on a nil pointer.
		{Label: "plain-shell", Status: "idle", Panes: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("titleSessions() = %+v, want %+v", got, want)
	}
}

// TestTitleSessions_KeepsEverythingWhenNothingIsInvoking covers the
// headless and degraded paths: a plugin context with no workspace id
// (internal/herdrc's Context makes every field optional) must not drop a
// real workspace by matching "" against "".
func TestTitleSessions_KeepsEverythingWhenNothingIsInvoking(t *testing.T) {
	got := titleSessions([]herdrc.WorkspaceInfo{
		{WorkspaceID: "", Label: "unidentified", PaneCount: 1},
		{WorkspaceID: "ws-2", Label: "elsewhere", PaneCount: 1},
	}, "")
	if len(got) != 2 {
		t.Errorf("titleSessions() kept %d of 2 workspaces with no invoking id: %+v", len(got), got)
	}
}

// TestBootstrap_UnsetPluginDirsIgnoreTheWorkingDirectory is the popup-path
// half of the guard the headless path had documented for itself all along.
//
// herdr exports $HERDR_PLUGIN_CONFIG_DIR and $HERDR_PLUGIN_STATE_DIR only
// to a launched PLUGIN, so an Env with both empty is not a strange case:
// it is `just smoke`, and it is anyone running the binary by hand to see
// what it does. Bootstrap passed them straight to config.Load /
// LoadState / LoadProjects / linear.LoadCache, each of which joined "" into
// a RELATIVE filename -- so the plugin read config.toml, recents.json,
// last-used.json and projects.json out of whatever repository the person
// happened to be standing in.
//
// The decoy config.toml here is the sharp end of it: unparseable TOML in
// the working directory used to make Bootstrap REFUSE TO OPEN, blaming a
// file that has nothing to do with this plugin.
func TestBootstrap_UnsetPluginDirsIgnoreTheWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	for name, body := range map[string]string{
		"config.toml":   "this is not toml = = =",
		"recents.json":  `["/decoy"]`,
		"projects.json": `{"version":1,"entries":{"/decoy":{"kind":"codex"}}}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write decoy %s: %v", name, err)
		}
	}

	env := Env{ContextJSON: validContextJSON()} // ConfigDir and StateDir both ""
	runner := &fakeRunner{workspaces: []herdrc.WorkspaceInfo{{WorkspaceID: "w1", Label: "main"}}}

	m, err := Bootstrap(env, runner, nil, newFakeGit(), noSleep)
	if err != nil {
		t.Fatalf("Bootstrap with unset plugin dirs = %v, want it to open on built-in defaults", err)
	}
	if got := m.cfg.BranchPrefix; got == "" {
		t.Error("cfg.BranchPrefix is empty, want the built-in default")
	}
	if len(m.state.Recents) != 0 {
		t.Errorf("state.Recents = %v, want none -- it read the working directory", m.state.Recents)
	}
	if len(m.projects.Entries) != 0 {
		t.Errorf("projects.Entries = %v, want none -- it read the working directory", m.projects.Entries)
	}
}

// TestBootstrap_UnsetContextRefusalIsInstructive covers the message a
// person actually meets first. The refusal itself was already correct and
// already tested; what it SAID was `parse plugin context: unexpected end
// of JSON input`, which names none of the things the reader needs.
//
// Asserting on message content is usually brittle and usually not worth
// it. It is worth it here because the message is the entire product at
// that moment -- this is someone's first contact with the binary, and
// there is no UI behind it to make up for a bad one. Each assertion is a
// question the old message left unanswered, not a spelling.
func TestBootstrap_UnsetContextRefusalIsInstructive(t *testing.T) {
	env := Env{ContextJSON: "", ConfigDir: t.TempDir(), StateDir: t.TempDir()}
	_, err := Bootstrap(env, &fakeRunner{}, nil, nil, noSleep)
	if err == nil {
		t.Fatal("Bootstrap with an unset context returned nil, want the pre-open refusal")
	}
	got := err.Error()

	for _, want := range []struct{ needle, why string }{
		{"HERDR_PLUGIN_CONTEXT_JSON", "name the variable that was missing"},
		{"herdr plugin", "say this is a herdr plugin rather than a program to run directly"},
		{herdrc.PluginID, "name this plugin, so the command can be copied as written"},
		{"--entrypoint open", "give the command that actually opens the popup"},
		{"herdr-draft create", "point at the entry point that works without a popup"},
		{"github.com/ZviBaratz/herdr-draft", "point somewhere with the whole picture"},
	} {
		if !strings.Contains(got, want.needle) {
			t.Errorf("refusal does not %s (looking for %q):\n%s", want.why, want.needle, got)
		}
	}
}

// TestBootstrap_MalformedContextStaysShort is the other half, and the
// reason the two cases are distinguished at all. A context that was SET
// but unreadable means the reader is already inside a herdr context;
// explaining what herdr is would be answering a question they did not ask,
// and burying the actual parse failure under it.
func TestBootstrap_MalformedContextStaysShort(t *testing.T) {
	env := Env{ContextJSON: `{"workspace_id":`, ConfigDir: t.TempDir(), StateDir: t.TempDir()}
	_, err := Bootstrap(env, &fakeRunner{}, nil, nil, noSleep)
	if err == nil {
		t.Fatal("Bootstrap with a malformed context returned nil, want the pre-open refusal")
	}
	got := err.Error()

	if strings.Contains(got, "--entrypoint open") {
		t.Errorf("a malformed context got the unset case's orientation text:\n%s", got)
	}
	if !strings.Contains(got, "parse plugin context") {
		t.Errorf("refusal = %q, want it to name the parse failure", got)
	}
}

// TestBootstrap_UnreachableHerdrRefusalNamesWhatToCheck pins the second
// refusal's content. It deliberately does not tell the reader to start
// herdr -- they are almost certainly sitting inside a running one, since
// that is the only place the popup opens from -- so it names the two
// variables that decide WHICH herdr is being talked to, plus the
// protocol-mismatch case, which produces this refusal and is fixed by
// restarting the server rather than by anything in this plugin.
func TestBootstrap_UnreachableHerdrRefusalNamesWhatToCheck(t *testing.T) {
	env := Env{ContextJSON: validContextJSON(), ConfigDir: t.TempDir(), StateDir: t.TempDir()}
	_, err := Bootstrap(env, &fakeRunner{listErr: context.DeadlineExceeded}, nil, nil, noSleep)
	if err == nil {
		t.Fatal("Bootstrap with an unreachable herdr returned nil, want the pre-open refusal")
	}
	got := err.Error()

	for _, needle := range []string{"HERDR_BIN_PATH", "HERDR_SOCKET_PATH", "protocol mismatch"} {
		if !strings.Contains(got, needle) {
			t.Errorf("refusal does not mention %q:\n%s", needle, got)
		}
	}
}

// TestClauthUnavailableReason keeps the row's one line readable. A failing
// clauth's stderr can be multi-line -- a Rust panic with a goroutine dump
// is the realistic case -- and the panel builds a fixed number of lines,
// so an unflattened reason would push the layout past the height it was
// asked for. An error message that breaks the form it is explaining is a
// worse bug than the silence it replaced.
func TestClauthUnavailableReason(t *testing.T) {
	got := clauthUnavailableReason(fmt.Errorf("clauth status --json: exit status 1: panic: something\n\n  goroutine 1"))

	if strings.ContainsAny(got, "\n\r") {
		t.Errorf("clauthUnavailableReason = %q, want a single line", got)
	}
	if strings.HasPrefix(got, "clauth status --json: ") {
		t.Errorf("clauthUnavailableReason = %q, want the package's own prefix dropped", got)
	}
	if !strings.Contains(got, "exit status 1") {
		t.Errorf("clauthUnavailableReason = %q, want it to keep the actual failure", got)
	}
}

// --- account picker --------------------------------------------------------

// fakePicker records the options it was called with, because the two calls a
// session makes differ in exactly one flag and that flag is what keeps a
// preview from writing a ledger entry for a session nobody has created.
type fakePicker struct {
	res   picker.Result
	err   error
	calls []picker.Options
	dirs  []string
}

func (p *fakePicker) Pick(_ context.Context, dir string, opts picker.Options) (picker.Result, error) {
	p.calls = append(p.calls, opts)
	p.dirs = append(p.dirs, dir)
	return p.res, p.err
}

// twoProfileStatus is the minimum clauth feed that makes an account row exist
// at all (New's own >= 2 profiles gate).
func twoProfileStatus() clauth.Status {
	return clauth.Status{Schema: 1, ActiveProfile: "alpha-1", Profiles: []clauth.Profile{
		{Name: "alpha-1", Tier: "Max", AuthStatus: "ok"},
		{Name: "alpha-2", Tier: "Max", AuthStatus: "ok"},
	}}
}

// modelWithPicker is a settled model whose account row carries a working
// picker, built through New rather than by poking the field.
func modelWithPicker(t *testing.T, p *fakePicker, cfg config.Config) Model {
	t.Helper()
	cfg.Clauth.Picker = "stub"
	return newTestModel(t, testSetup{
		Clauth:       &fakeClauth{status: twoProfileStatus()},
		ClauthStatus: twoProfileStatus(),
		Picker:       p,
		Config:       cfg,
	})
}

// runPreview drives one debounced preview end to end, the same way this
// package's dir-check tests drive theirs.
func runPreview(t *testing.T, m Model, path string) Model {
	t.Helper()
	cmd := m.schedulePickerPreview(path)
	if cmd == nil {
		t.Fatal("schedulePickerPreview returned no command")
	}
	msg, ok := cmd().(pickerDebounceMsg)
	if !ok {
		t.Fatalf("schedulePickerPreview produced %T, want pickerDebounceMsg", cmd())
	}
	m, run := m.handlePickerDebounce(msg)
	if run == nil {
		t.Fatal("handlePickerDebounce returned no command")
	}
	res, ok := run().(pickerPreviewMsg)
	if !ok {
		t.Fatalf("runPickerPreview produced %T, want pickerPreviewMsg", run())
	}
	m, _ = m.handlePickerPreview(res)
	return m
}

// A picker that is configured but does not answer the protocol degrades
// VISIBLY -- the account row is there, saying why -- rather than silently.
// This is the whole difference between "no picker configured" (the common
// case, which shows nothing) and "your picker is broken".
func TestBootstrapDegradesAnUnprobeablePicker(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-a-picker")
	configDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"),
		[]byte("[clauth]\npicker = "+strconv.Quote(missing)+"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	env := Env{ConfigDir: configDir, StateDir: t.TempDir(), ContextJSON: `{"workspace_cwd":"/repo"}`}
	cl := &fakeClauth{status: twoProfileStatus()}
	m, err := Bootstrap(env, &fakeRunner{}, cl, newFakeGit(), noSleep)
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if m.account == nil {
		t.Fatal("a broken picker must still leave an account row to say so on")
	}
	if m.account.IsAuto() {
		t.Fatal("an unprobeable picker must not produce an auto selection")
	}
	if got := fieldText(m.account, 100); !strings.Contains(got, missing) {
		t.Fatalf("the account row should name the picker that failed:\n%s", got)
	}
}

// The preview is a --dry-run and the commit is not. One ledger entry per
// session created, not one per keystroke in the project field.
func TestPreviewIsDryRunAndCommitIsNot(t *testing.T) {
	p := &fakePicker{res: picker.Result{Profile: "alpha-1", ConfigDir: "/dirs/alpha-1"}}
	m := modelWithPicker(t, p, config.Config{})

	m = runPreview(t, m, "/p/thing")
	if len(p.calls) != 1 || !p.calls[0].DryRun {
		t.Fatalf("the preview call must be a --dry-run, got %+v", p.calls)
	}
	if p.dirs[0] != "/p/thing" {
		t.Fatalf("preview dir = %q", p.dirs[0])
	}

	res, err := m.ResolveAccount(context.Background())
	if err != nil {
		t.Fatalf("ResolveAccount: %v", err)
	}
	if len(p.calls) != 2 || p.calls[1].DryRun {
		t.Fatalf("the commit call must NOT be a --dry-run: it is what writes the ledger; got %+v", p.calls)
	}
	if res.Profile != "alpha-1" {
		t.Fatalf("ResolveAccount = %+v", res)
	}
}

// The picked profile and its config dir reach plan.Input, and the launch mode
// comes from config -- start by default.
func TestWithAccountFeedsPlanInput(t *testing.T) {
	p := &fakePicker{res: picker.Result{Profile: "alpha-1", ConfigDir: "/dirs/alpha-1"}}
	m := modelWithPicker(t, p, config.Config{})
	m = m.WithAccount(picker.Result{Profile: "alpha-1", ConfigDir: "/dirs/alpha-1"})

	in := m.PlanInput()
	if in.AccountPin != "alpha-1" {
		t.Fatalf("AccountPin = %q", in.AccountPin)
	}
	if in.AccountConfigDir != "/dirs/alpha-1" {
		t.Fatalf("AccountConfigDir = %q", in.AccountConfigDir)
	}
	if in.AccountLaunch != plan.LaunchClauthStart {
		t.Fatalf("AccountLaunch = %v, want the default clauth start", in.AccountLaunch)
	}
}

func TestWrapperLaunchReachesPlanInput(t *testing.T) {
	p := &fakePicker{res: picker.Result{Profile: "alpha-1", ConfigDir: "/dirs/alpha-1"}}
	cfg := config.Config{}
	cfg.Clauth.Launch = config.ClauthLaunchWrapper
	m := modelWithPicker(t, p, cfg)
	m = m.WithAccount(picker.Result{Profile: "alpha-1", ConfigDir: "/dirs/alpha-1"})

	if got := m.PlanInput().AccountLaunch; got != plan.LaunchWrapper {
		t.Fatalf("AccountLaunch = %v, want LaunchWrapper", got)
	}
}

// A refusal must not silently fall back to an unpinned launch. Auto means the
// user asked to be picked for; launching under whatever is live instead, with
// no word said, is the silent degradation this design was amended to avoid.
func TestResolveAccountSurfacesARefusal(t *testing.T) {
	p := &fakePicker{err: &picker.RefusalError{Code: picker.ExitExhausted, Reason: "pool exhausted (resets 22:49)"}}
	m := modelWithPicker(t, p, config.Config{})
	if _, err := m.ResolveAccount(context.Background()); err == nil {
		t.Fatal("a refusal must reach the caller")
	}
}

// The refusal reaches the ROW too, in the picker's own words.
func TestPreviewRefusalReachesTheRow(t *testing.T) {
	p := &fakePicker{err: &picker.RefusalError{Code: picker.ExitExhausted, Reason: "pool exhausted (resets 22:49)"}}
	m := modelWithPicker(t, p, config.Config{})
	m = runPreview(t, m, "/p/thing")
	if got := fieldText(m.account, 100); !strings.Contains(got, "pool exhausted (resets 22:49)") {
		t.Fatalf("the account row should carry the picker's own reason:\n%s", got)
	}
}

// Not auto: no picker call at all. Someone who pinned a profile by hand has
// already answered the question the picker exists to answer.
func TestResolveAccountIsANoOpWhenNotAuto(t *testing.T) {
	p := &fakePicker{res: picker.Result{Profile: "alpha-1"}}
	m := modelWithPicker(t, p, config.Config{})
	m.account.SetPin("alpha-2")

	res, err := m.ResolveAccount(context.Background())
	if err != nil || res.Profile != "" {
		t.Fatalf("ResolveAccount = %+v, %v; want a no-op", res, err)
	}
	if len(p.calls) != 0 {
		t.Fatal("a hand-pinned account must not consult the picker")
	}
}

// A preview for a project the user has since navigated away from must not
// overwrite the current one -- the same staleness rule every other async
// source in async.go follows.
func TestStalePreviewIsDiscarded(t *testing.T) {
	p := &fakePicker{res: picker.Result{Profile: "alpha-1"}}
	m := modelWithPicker(t, p, config.Config{})

	cmd1 := m.schedulePickerPreview("old")
	cmd2 := m.schedulePickerPreview("new")
	v1 := cmd1().(pickerDebounceMsg).req.version
	v2 := cmd2().(pickerDebounceMsg).req.version
	if v1 == v2 {
		t.Fatalf("two schedule calls produced the same version %d", v1)
	}

	// The current request lands first.
	m, _ = m.handlePickerPreview(pickerPreviewMsg{
		req: request{version: v2, key: "new"}, res: picker.Result{Profile: "current"}})
	// Then the stale one, which must change nothing.
	m, _ = m.handlePickerPreview(pickerPreviewMsg{
		req: request{version: v1, key: "old"}, res: picker.Result{Profile: "stale"}})

	got := fieldText(m.account, 100)
	if !strings.Contains(got, "current") {
		t.Fatalf("the current preview should still be on the row:\n%s", got)
	}
	if strings.Contains(got, "stale") {
		t.Fatalf("a superseded preview must not overwrite the current one:\n%s", got)
	}
}
