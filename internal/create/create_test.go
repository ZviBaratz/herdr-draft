package create

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
	"github.com/ZviBaratz/herdr-draft/internal/linear"
	"github.com/ZviBaratz/herdr-draft/internal/plan"
)

// --- test doubles ---------------------------------------------------------
//
// Nothing in this package's tests touches the real herdr, git, Linear or
// clauth. The one exception is deliberate and inert: TestBranchLeadingDash
// uses a REAL herdrc.CLIRunner to prove internal/herdrc's own argv refusal
// reaches this command's exit code -- that refusal happens while the argv
// is still being assembled, so no process is ever started.

// fakeRunner implements herdrc.Runner, recording every call and failing on
// demand at one named method.
type fakeRunner struct {
	calls []string

	// t is only for reporting a test's own mistake (splitting a pane this
	// fake never handed out); nil is fine for a runner no test drives
	// through PaneSplit.
	t *testing.T

	topo    herdrc.CreatedTopology
	listErr error
	// created counts the creation calls this fake has answered, and panes
	// records which workspace/tab each pane it handed out belongs to.
	// Both exist so a SECOND creation in one run gets ids of its own:
	// herdr allocates a fresh workspace/tab/pane per creation call, and a
	// fake that answers every one of them with the same three ids cannot
	// produce the divergence between the SPACE and the AGENT'S PANE at
	// all (#99) -- the shape CLAUDE.md's "a fake that cannot fail the way
	// the real thing does" convention is about. internal/plan's own
	// mockRunner learned the same lesson earlier, as tabTopo/splitTopo.
	created int
	panes   map[string]herdrc.CreatedTopology
	// workspaces is what WorkspaceList reports as already open -- nil
	// (the zero value) for every test that does not care, so a
	// WorktreeCreate that returns r.topo's own WorkspaceID is never read
	// as a reuse unless a test deliberately puts that id in here first
	// (placement spec §5.2's before/after comparison, exec.go).
	workspaces []herdrc.WorkspaceInfo
	failAt     string
	readText   string

	// failErr, when non-nil, is what failAt fails with instead of the
	// generic error below -- needed only where the error's own TEXT is
	// what the test is about, since herdrc.CLIRunner surfaces herdr's
	// error codes as plain text and internal/plan matches on them by
	// substring.
	failErr error
}

var _ herdrc.Runner = (*fakeRunner)(nil)

func newFakeRunner() *fakeRunner {
	return &fakeRunner{
		topo:  herdrc.CreatedTopology{WorkspaceID: "wS1", TabID: "tT1", PaneID: "pP1"},
		panes: map[string]herdrc.CreatedTopology{},
	}
}

// registerPane teaches the fake that paneID already exists in ws/tab, so a
// PaneSplit of it can answer with that tab rather than inventing one. The
// harness seeds the INVOKING pane this way before every run; creations
// register their own.
func (r *fakeRunner) registerPane(ws, tab, pane string) {
	if pane == "" {
		return
	}
	r.panes[pane] = herdrc.CreatedTopology{WorkspaceID: ws, TabID: tab, PaneID: pane}
}

// nextSpace is one whole new workspace: what worktree create and workspace
// create return. The FIRST one is r.topo, so every single-creation test
// keeps reading wS1/tT1/pP1; later ones are numbered from there.
func (r *fakeRunner) nextSpace() herdrc.CreatedTopology {
	r.created++
	t := r.topo
	if r.created > 1 {
		n := strconv.Itoa(r.created)
		t = herdrc.CreatedTopology{WorkspaceID: "wS" + n, TabID: "tT" + n, PaneID: "pP" + n}
	}
	r.registerPane(t.WorkspaceID, t.TabID, t.PaneID)
	return t
}

// nextTabIn is a new tab inside an EXISTING workspace: the workspace id is
// the caller's, the tab and pane are fresh. That asymmetry is the whole
// point -- it is what makes a placement op's pane sit in a workspace the
// space op never named.
func (r *fakeRunner) nextTabIn(ws string) herdrc.CreatedTopology {
	t := r.nextSpace()
	if ws != "" {
		t.WorkspaceID = ws
		t.CheckoutPath = ""
		r.registerPane(t.WorkspaceID, t.TabID, t.PaneID)
	}
	return t
}

// nextPaneBeside is a split: same workspace and same TAB as the pane it
// splits, a new pane. An unregistered pane means a test split something
// this fake never handed out, which is a test bug rather than a herdr
// behaviour to model.
func (r *fakeRunner) nextPaneBeside(paneID string) herdrc.CreatedTopology {
	host, ok := r.panes[paneID]
	if !ok && r.t != nil {
		r.t.Helper()
		r.t.Fatalf("fakeRunner: PaneSplit(%q): no such pane -- register it with registerPane first", paneID)
	}
	fresh := r.nextSpace()
	out := herdrc.CreatedTopology{WorkspaceID: host.WorkspaceID, TabID: host.TabID, PaneID: fresh.PaneID}
	r.registerPane(out.WorkspaceID, out.TabID, out.PaneID)
	return out
}

func (r *fakeRunner) record(name string, args ...string) error {
	r.calls = append(r.calls, name+"("+strings.Join(args, ",")+")")
	if name == r.failAt {
		if r.failErr != nil {
			return r.failErr
		}
		return errors.New("the pane was busy")
	}
	return nil
}

func (r *fakeRunner) called(name string) bool {
	for _, c := range r.calls {
		if strings.HasPrefix(c, name+"(") {
			return true
		}
	}
	return false
}

func (r *fakeRunner) WorkspaceList(context.Context) ([]herdrc.WorkspaceInfo, error) {
	_ = r.record("WorkspaceList")
	return r.workspaces, r.listErr
}

func (r *fakeRunner) WorktreeCreate(_ context.Context, req herdrc.WorktreeCreateReq) (herdrc.CreatedTopology, error) {
	if err := r.record("WorktreeCreate", req.Cwd, req.Branch, req.Base); err != nil {
		return herdrc.CreatedTopology{}, err
	}
	topo := r.nextSpace()
	topo.CheckoutPath = "/checkouts/" + req.Branch
	return topo, nil
}

func (r *fakeRunner) WorkspaceCreate(_ context.Context, req herdrc.WorkspaceCreateReq) (herdrc.CreatedTopology, error) {
	if err := r.record("WorkspaceCreate", req.Cwd, req.Label); err != nil {
		return herdrc.CreatedTopology{}, err
	}
	return r.nextSpace(), nil
}

func (r *fakeRunner) TabCreate(_ context.Context, req herdrc.TabCreateReq) (herdrc.CreatedTopology, error) {
	if err := r.record("TabCreate", req.Workspace, req.Cwd); err != nil {
		return herdrc.CreatedTopology{}, err
	}
	return r.nextTabIn(req.Workspace), nil
}

func (r *fakeRunner) PaneSplit(_ context.Context, req herdrc.PaneSplitReq) (herdrc.CreatedTopology, error) {
	if err := r.record("PaneSplit", req.PaneID, req.Direction); err != nil {
		return herdrc.CreatedTopology{}, err
	}
	return r.nextPaneBeside(req.PaneID), nil
}

func (r *fakeRunner) AgentStart(_ context.Context, req herdrc.AgentStartReq) error {
	return r.record("AgentStart", req.Name, req.Kind, req.PaneID)
}

func (r *fakeRunner) AgentPrompt(_ context.Context, req herdrc.AgentPromptReq) error {
	return r.record("AgentPrompt", req.Target, req.Text)
}

func (r *fakeRunner) AgentRead(_ context.Context, target string) (string, error) {
	if err := r.record("AgentRead", target); err != nil {
		return "", err
	}
	return r.readText, nil
}

func (r *fakeRunner) AwaitDetection(_ context.Context, paneID string, _ time.Duration) error {
	return r.record("AwaitDetection", paneID)
}

func (r *fakeRunner) PaneRun(_ context.Context, paneID string, argv []string) error {
	return r.record("PaneRun", append([]string{paneID}, argv...)...)
}

func (r *fakeRunner) PaneClose(_ context.Context, paneID string) error {
	return r.record("PaneClose", paneID)
}

func (r *fakeRunner) WorktreeRemove(_ context.Context, workspaceID string) error {
	return r.record("WorktreeRemove", workspaceID)
}

func (r *fakeRunner) WorkspaceClose(_ context.Context, workspaceID string) error {
	return r.record("WorkspaceClose", workspaceID)
}

// fakeGit implements GitSource: an existing git repository whose root is
// itself, unless a test says otherwise.
type fakeGit struct {
	exists bool
	isRepo bool
}

var _ GitSource = (*fakeGit)(nil)

func newFakeGit() *fakeGit { return &fakeGit{exists: true, isRepo: true} }

func (g *fakeGit) DirExists(string) bool { return g.exists }
func (g *fakeGit) IsGitRepo(string) bool { return g.isRepo }
func (g *fakeGit) RepoRoot(_ context.Context, dir string) (string, error) {
	if !g.isRepo {
		return "", nil
	}
	return dir, nil
}

// fakeLinear implements IssueSource.
type fakeLinear struct {
	issues []linear.Issue
}

func (f *fakeLinear) AssignedIssues(context.Context) ([]linear.Issue, error) {
	return f.issues, nil
}

// harness is one `create` invocation's environment: temp config/state
// directories, fakes for everything else, and the captured streams.
type harness struct {
	env    Env
	deps   Deps
	runner *fakeRunner
	git    *fakeGit
	stdout *strings.Builder
	stderr *strings.Builder
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	// linear.ResolveAPIKey reads $LINEAR_API_KEY, so a developer who has
	// one exported would otherwise make `--issue` reach the real API from
	// a unit test. Cleared for every test in this package, not only the
	// Linear ones: the guarantee worth having is that nothing here can
	// reach the network by accident.
	t.Setenv("LINEAR_API_KEY", "")
	runner := newFakeRunner()
	runner.t = t
	git := newFakeGit()
	h := &harness{
		env: Env{
			ConfigDir: t.TempDir(),
			StateDir:  t.TempDir(),
			// PluginID is set for the same reason herdr sets it: it is
			// what makes the two directories above OURS (#91). Leaving it
			// empty here modelled an environment herdr never produces --
			// plugin_path_env's only two callers both export the id in the
			// same function -- and it is why usablePluginEnv's hole was
			// invisible to this package for its whole life: every test ran
			// in the exact shape the guard failed to refuse.
			PluginID: pluginID,
		},
		runner: runner,
		git:    git,
		stdout: &strings.Builder{},
		stderr: &strings.Builder{},
	}
	h.deps = Deps{
		Runner: runner,
		Git:    git,
		// No repository config unless a test supplies one -- never the
		// production loader, which would read whatever .herdr-draft.toml
		// happens to sit above the test's working directory.
		RepoConfig: func(string) config.RepoConfig { return config.RepoConfig{} },
		Stdin:      strings.NewReader(""),
		Stdout:     h.stdout,
		Stderr:     h.stderr,
		Workdir:    func() (string, error) { return "/projects/thing", nil },
		Now:        func() time.Time { return time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC) },
	}
	return h
}

func (h *harness) run(args ...string) int {
	// The invoking pane exists before this command runs, so the fake has
	// to know it: a split-here placement splits it, and herdr answers
	// that with a pane in the invoking pane's own tab.
	h.runner.registerPane(h.env.WorkspaceID, h.env.TabID, h.env.PaneID)
	return Run(context.Background(), args, h.env, h.deps)
}

// --- exit codes -----------------------------------------------------------

// TestExitZero_CreatesASession is spec §13's exit 0: a session created
// from nothing but a title, with the project directory defaulting to the
// working directory.
func TestExitZero_CreatesASession(t *testing.T) {
	h := newHarness(t)

	if code := h.run("--title", "fix login redirect", "--no-worktree"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	if !h.runner.called("WorkspaceCreate") || !h.runner.called("AgentStart") {
		t.Fatalf("calls = %v, want a workspace create and an agent start", h.runner.calls)
	}
	out := h.stdout.String()
	for _, want := range []string{"created", "workspace=wS1", "pane=pP1", "agent=claude"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout = %q, want it to contain %q", out, want)
		}
	}
	// Progress is one line per step, on stderr.
	if got := strings.Count(h.stderr.String(), "... ok"); got != 2 {
		t.Errorf("stderr = %q, want one progress line per op", h.stderr)
	}
}

// TestExitOne_PlanFailsAfterTheTopologyExists is spec §13's exit 1: the
// agent could not start, so the workspace that was already created is
// reported and -- with the default --on-failure keep -- left alone.
func TestExitOne_PlanFailsAfterTheTopologyExists(t *testing.T) {
	h := newHarness(t)
	h.runner.failAt = "AgentStart"

	if code := h.run("--title", "t", "--no-worktree"); code != ExitFailed {
		t.Fatalf("exit = %d, want %d", code, ExitFailed)
	}
	if h.runner.called("WorkspaceClose") {
		t.Errorf("--on-failure defaults to keep, but the workspace was closed: %v", h.runner.calls)
	}
	if !strings.Contains(h.stderr.String(), "kept the session it had created") {
		t.Errorf("stderr = %q, want it to say the session was kept", h.stderr)
	}
}

// TestExitOne_OnFailureCleanRemovesTheTopology pins the other half of the
// gate: --on-failure clean closes the workspace the failed run created.
func TestExitOne_OnFailureCleanRemovesTheTopology(t *testing.T) {
	h := newHarness(t)
	h.runner.failAt = "AgentStart"

	if code := h.run("--title", "t", "--no-worktree", "--on-failure", "clean"); code != ExitFailed {
		t.Fatalf("exit = %d, want %d", code, ExitFailed)
	}
	if !h.runner.called("WorkspaceClose") {
		t.Fatalf("--on-failure clean did not close the workspace: %v", h.runner.calls)
	}
	if !strings.Contains(h.stderr.String(), "was removed") {
		t.Errorf("stderr = %q, want it to say the session was removed", h.stderr)
	}
}

// TestExitOne_TopologyItselfFails covers the failure with nothing to keep
// or clean: it is still exit 1, and it says so rather than claiming a
// session exists.
func TestExitOne_TopologyItselfFails(t *testing.T) {
	h := newHarness(t)
	h.runner.failAt = "WorkspaceCreate"

	if code := h.run("--title", "t", "--no-worktree", "--on-failure", "clean"); code != ExitFailed {
		t.Fatalf("exit = %d, want %d", code, ExitFailed)
	}
	if h.runner.called("WorkspaceClose") || h.runner.called("WorktreeRemove") {
		t.Errorf("nothing was created, but a clean was attempted: %v", h.runner.calls)
	}
	if !strings.Contains(h.stderr.String(), "nothing was created") {
		t.Errorf("stderr = %q, want it to say nothing was created", h.stderr)
	}
}

// TestExitOne_OnFailureCleanRefusesAReusedSpace reaches placement spec
// §5.4's reuse refusal from the headless verb -- previously unreachable
// from the command line, since nothing in this package's tests exercised
// SpaceReused before this task. It also exercises Task 3's own extension
// (exec.go's TestExecute_ReuseClaimFailureStillReportsTheSpaceItCreated,
// mirrored here at this package's level): a step failing AFTER its own
// worktree op already succeeded -- here, the reuse claim's TabCreate --
// still reports the space in ExecResult.Created rather than leaving it
// nil, so applyOnFailure runs at all where it previously would not have.
//
// The fixture is built so the refusal can ONLY come from CleanCheck's
// SpaceReused branch, never from gitx.Disposable failing on the fake,
// nonexistent checkout path: WorktreeCreate's own return value (r.topo's
// WorkspaceID) is seeded into WorkspaceList's before-list, so Execute's
// before/after comparison (exec.go) reads it as reused and CleanCheck's
// SpaceReused check short-circuits before ever calling gitx.Disposable
// (exec.go's CleanCheck orders the two checks -- SpaceReused first,
// unconditionally). A git-error refusal would say "could not determine
// whether the worktree is safe to remove"; this one names the reused
// workspace by label instead, which is what the assertions below check
// for -- not merely that SOME refusal happened.
func TestExitOne_OnFailureCleanRefusesAReusedSpace(t *testing.T) {
	h := newHarness(t)
	// r.topo (newFakeRunner's default) carries WorkspaceID "wS1" --
	// seeding that same id into the before-list is what makes Execute's
	// reuse comparison fire for WorktreeCreate's own return value.
	h.runner.workspaces = []herdrc.WorkspaceInfo{{WorkspaceID: "wS1", Label: "somebody-else"}}
	// The worktree op is both the space op and the agent-pane op here (no
	// separate placement -- "a worktree with tab-here still needs the
	// workspace id", per TestLazyContext_OnlyTheHerePlacementsNeedIt
	// above), so the reuse claim's TabCreate runs inside this same step
	// and its failure is what Task 3 taught Execute to still report the
	// space for.
	h.runner.failAt = "TabCreate"

	code := h.run("--title", "t", "--worktree", "--on-failure", "clean")
	if code != ExitFailed {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitFailed, h.stderr)
	}
	if h.runner.called("WorktreeRemove") {
		t.Errorf("a reused space was removed: %v", h.runner.calls)
	}
	stderr := h.stderr.String()
	if !strings.Contains(stderr, "somebody-else") {
		t.Errorf("stderr = %q, want the reused workspace's own label named in the refusal", stderr)
	}
	if !strings.Contains(stderr, "was already open") {
		t.Errorf("stderr = %q, want CleanCheck's SpaceReused wording, not a git-error refusal", stderr)
	}
}

// TestExitTwo_UnknownFlag is spec §13's exit 2 for a malformed command
// line, and pins that the usage comes with it.
func TestExitTwo_UnknownFlag(t *testing.T) {
	h := newHarness(t)

	if code := h.run("--title", "t", "--nonesuch"); code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if h.runner.called("WorkspaceList") {
		t.Errorf("a bad flag reached herdr: %v", h.runner.calls)
	}
	if !strings.Contains(h.stderr.String(), "usage: herdr-draft create") {
		t.Errorf("stderr = %q, want the usage", h.stderr)
	}
}

// TestExitTwo_BadEnumValues covers the two closed vocabularies.
func TestExitTwo_BadEnumValues(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"placement", []string{"--title", "t", "--placement", "sideways"}, "unknown --placement"},
		{"on-failure", []string{"--title", "t", "--on-failure", "burn"}, "unknown --on-failure"},
		{"contradiction", []string{"--title", "t", "--worktree", "--no-worktree"}, "contradict"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			if code := h.run(tc.args...); code != ExitUsage {
				t.Fatalf("exit = %d, want %d", code, ExitUsage)
			}
			if !strings.Contains(h.stderr.String(), tc.want) {
				t.Errorf("stderr = %q, want it to contain %q", h.stderr, tc.want)
			}
		})
	}
}

// TestExitTwo_NoTitle: the one value nothing can default.
func TestExitTwo_NoTitle(t *testing.T) {
	h := newHarness(t)

	if code := h.run("--no-worktree"); code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(h.stderr.String(), "a title is required") {
		t.Errorf("stderr = %q, want it to ask for a title", h.stderr)
	}
}

// TestExitTwo_MissingProjectDirectory refuses before the probe rather than
// letting herdr create a workspace rooted somewhere that does not exist.
func TestExitTwo_MissingProjectDirectory(t *testing.T) {
	h := newHarness(t)
	h.git.exists = false

	if code := h.run("--title", "t", "--project", "/nope"); code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(h.stderr.String(), "does not exist") {
		t.Errorf("stderr = %q, want it to name the missing directory", h.stderr)
	}
}

// TestExitTwo_WorktreeOutsideARepository: plan.Build's own refusal, which
// is a bad request rather than a failed creation -- nothing ran.
func TestExitTwo_WorktreeOutsideARepository(t *testing.T) {
	h := newHarness(t)
	h.git.isRepo = false

	if code := h.run("--title", "t", "--worktree"); code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(h.stderr.String(), "requires a git repository") {
		t.Errorf("stderr = %q, want plan.Build's reason", h.stderr)
	}
}

// TestExitThree_HerdrUnreachable is spec §13's exit 3, and pins that the
// probe is what produces it.
func TestExitThree_HerdrUnreachable(t *testing.T) {
	h := newHarness(t)
	h.runner.listErr = errors.New("dial unix /run/herdr.sock: connect: no such file or directory")

	if code := h.run("--title", "t", "--no-worktree"); code != ExitUnreachable {
		t.Fatalf("exit = %d, want %d", code, ExitUnreachable)
	}
	if h.runner.called("WorkspaceCreate") {
		t.Errorf("an unreachable herdr still got a create call: %v", h.runner.calls)
	}
	if !strings.Contains(h.stderr.String(), "herdr unreachable") {
		t.Errorf("stderr = %q, want it to say herdr is unreachable", h.stderr)
	}
}

// --- lazy context (spec §13) ----------------------------------------------

// TestLazyContext_OnlyTheHerePlacementsNeedIt is the requirement in one
// table: only tab-here and split-here need the herdr environment, and that
// holds regardless of UseWorktree (placement spec §6.3) -- a new space
// creates fine with no herdr environment at all, whether or not a worktree
// is involved, while tab-here and split-here refuse and name the exact
// variable they are missing, worktree or not.
func TestLazyContext_OnlyTheHerePlacementsNeedIt(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		env      Env
		wantCode int
		wantErr  string
	}{
		{
			name:     "new space needs nothing",
			args:     []string{"--title", "t", "--no-worktree", "--placement", "new-space"},
			wantCode: ExitOK,
		},
		{
			name:     "a worktree with tab-here still needs the workspace id",
			args:     []string{"--title", "t", "--worktree", "--placement", "tab-here"},
			wantCode: ExitUsage,
			wantErr:  "HERDR_WORKSPACE_ID is not set",
		},
		{
			name:     "tab-here without the workspace id",
			args:     []string{"--title", "t", "--no-worktree", "--placement", "tab-here"},
			env:      Env{TabID: "tT9"},
			wantCode: ExitUsage,
			wantErr:  "HERDR_WORKSPACE_ID is not set",
		},
		{
			name:     "tab-here without the tab id",
			args:     []string{"--title", "t", "--no-worktree", "--placement", "tab-here"},
			env:      Env{WorkspaceID: "wS9"},
			wantCode: ExitUsage,
			wantErr:  "HERDR_TAB_ID is not set",
		},
		{
			name:     "tab-here with both",
			args:     []string{"--title", "t", "--no-worktree", "--placement", "tab-here"},
			env:      Env{WorkspaceID: "wS9", TabID: "tT9"},
			wantCode: ExitOK,
		},
		{
			name:     "split-here without the pane id",
			args:     []string{"--title", "t", "--no-worktree", "--placement", "split-here"},
			wantCode: ExitUsage,
			wantErr:  "HERDR_PANE_ID is not set",
		},
		{
			name:     "split-here with it",
			args:     []string{"--title", "t", "--no-worktree", "--placement", "split-here"},
			env:      Env{PaneID: "pP9"},
			wantCode: ExitOK,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.env.WorkspaceID, h.env.TabID, h.env.PaneID = tc.env.WorkspaceID, tc.env.TabID, tc.env.PaneID

			code := h.run(tc.args...)
			if code != tc.wantCode {
				t.Fatalf("exit = %d, want %d\nstderr: %s", code, tc.wantCode, h.stderr)
			}
			if tc.wantErr != "" && !strings.Contains(h.stderr.String(), tc.wantErr) {
				t.Errorf("stderr = %q, want it to contain %q", h.stderr, tc.wantErr)
			}
		})
	}
}

// TestLazyContext_TabHereUsesTheWorkspaceItWasGiven checks the value
// actually reaches the op, not just the validation.
func TestLazyContext_TabHereUsesTheWorkspaceItWasGiven(t *testing.T) {
	h := newHarness(t)
	h.env.WorkspaceID, h.env.TabID = "wS9", "tT9"

	if code := h.run("--title", "t", "--no-worktree", "--placement", "tab-here"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	if !strings.Contains(strings.Join(h.runner.calls, " "), "TabCreate(wS9,") {
		t.Errorf("calls = %v, want a tab created in wS9", h.runner.calls)
	}
}

// TestRequireContext_WorktreeStillNeedsContextForAPlacement is placement
// spec §6.3 at the unit level: requireContext used to return nil outright
// for any UseWorktree input, before even looking at Placement. A worktree
// with tab-here now demands the same HERDR_WORKSPACE_ID a non-worktree
// tab-here already does, because Placement decides where the agent's pane
// goes regardless of whether a worktree was also created.
func TestRequireContext_WorktreeStillNeedsContextForAPlacement(t *testing.T) {
	in := plan.Input{UseWorktree: true, Placement: plan.PlacementTabHere}
	err := requireContext(herdrc.Context{}, in) // no WorkspaceID/TabID/FocusedPaneID
	if err == nil {
		t.Fatal("requireContext returned nil for worktree+tab-here with no pane context, want an error naming the missing variable")
	}
	if !strings.Contains(err.Error(), "HERDR_WORKSPACE_ID") {
		t.Errorf("error = %q, want it to name HERDR_WORKSPACE_ID", err)
	}
}

// TestRequireContext_WorktreeNewSpaceStillNeedsNoContext is the other half:
// a worktree opening its own new space needs no invoking-pane context,
// exactly as a non-worktree new space does -- UseWorktree never mattered to
// this decision, Placement always did.
func TestRequireContext_WorktreeNewSpaceStillNeedsNoContext(t *testing.T) {
	in := plan.Input{UseWorktree: true, Placement: plan.PlacementNewSpace}
	if err := requireContext(herdrc.Context{}, in); err != nil {
		t.Errorf("requireContext = %v, want nil -- a worktree's own new space needs no invoking-pane context", err)
	}
}

// --- prompt ---------------------------------------------------------------

// TestPromptFromStdin is spec §13's `--prompt -`.
func TestPromptFromStdin(t *testing.T) {
	h := newHarness(t)
	h.deps.Stdin = strings.NewReader("look at the login redirect loop\n")

	if code := h.run("--title", "t", "--no-worktree", "--prompt", "-"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	want := "AgentPrompt(pP1,look at the login redirect loop)"
	if !strings.Contains(strings.Join(h.runner.calls, " "), want) {
		t.Fatalf("calls = %v, want %q (the trailing newline trimmed)", h.runner.calls, want)
	}
}

// TestUnsentPromptIsRecoverable pins the outcome the dialog guard produces
// (internal/plan/dialog.go): the agent was showing a blocking dialog, the
// prompt was deliberately NOT typed into it, and a headless caller has no
// pane to recover it from -- so it comes back in the output.
func TestUnsentPromptIsRecoverable(t *testing.T) {
	h := newHarness(t)
	h.runner.readText = "Quick safety check\nDo you trust this folder?"

	code := h.run("--title", "t", "--no-worktree", "--prompt", "the prompt that was not sent", "--json")
	if code != ExitFailed {
		t.Fatalf("exit = %d, want %d", code, ExitFailed)
	}
	if h.runner.called("AgentPrompt") {
		t.Fatalf("the dialog guard did not hold: %v", h.runner.calls)
	}

	var out jsonReport
	if err := json.Unmarshal([]byte(h.stdout.String()), &out); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, h.stdout)
	}
	if out.OK {
		t.Errorf("ok = true, want false")
	}
	if out.PromptSent == nil || *out.PromptSent {
		t.Errorf("prompt_sent = %v, want false", out.PromptSent)
	}
	if out.UnsentPrompt != "the prompt that was not sent" {
		t.Errorf("unsent_prompt = %q, want the prompt text back", out.UnsentPrompt)
	}
	if !strings.Contains(out.Error, "waiting on a dialog") {
		t.Errorf("error = %q, want the dialog guard's reason", out.Error)
	}
	if out.WorkspaceID != "wS1" {
		t.Errorf("workspace_id = %q, want the session that WAS created", out.WorkspaceID)
	}
}

// TestUnsentPromptSurvivesAFailureBeforeTheAgentStarts is the same recovery
// one step earlier, and it used to be broken in the most misleading way
// available: `prompt_sent` is derived from PromptText being empty, and
// PromptText was only ever set when the PROMPT op failed -- so a run that
// died at `agent start`, having typed nothing anywhere, reported
// `prompt_sent: true` and dropped the text.
//
// #90 made that the ordinary outcome rather than a corner: on herdr 0.9.0 a
// fresh worktree's first-run trust prompt blocks `agent start` outright, so
// every first submit into a new checkout stops here. A headless caller has
// no pane to scroll back through, which is exactly why the text has to come
// out in the report.
func TestUnsentPromptSurvivesAFailureBeforeTheAgentStarts(t *testing.T) {
	h := newHarness(t)
	h.runner.failAt = "AgentStart"

	const prompt = "Work on ENG-101\n\nthe long body a caller would hate to retype"
	code := h.run("--title", "t", "--no-worktree", "--prompt", prompt, "--json")
	if code != ExitFailed {
		t.Fatalf("exit = %d, want %d", code, ExitFailed)
	}
	if h.runner.called("AgentPrompt") {
		t.Fatalf("the plan stopped at AgentStart, but a prompt was sent anyway: %v", h.runner.calls)
	}

	var out jsonReport
	if err := json.Unmarshal([]byte(h.stdout.String()), &out); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, h.stdout)
	}
	if out.PromptSent == nil || *out.PromptSent {
		t.Errorf("prompt_sent = %v, want false -- nothing was ever typed", out.PromptSent)
	}
	if out.UnsentPrompt != prompt {
		t.Errorf("unsent_prompt = %q, want the whole prompt back", out.UnsentPrompt)
	}
}

// TestBlockedStartIsExplainedHeadlessly is #90's message reaching the
// headless verb. `create` has no pane and no keep-or-clean gate, so the one
// place it can say what happened is stderr -- and unlike the popup, it has
// the room to carry herdr's own error code alongside the instruction.
func TestBlockedStartIsExplainedHeadlessly(t *testing.T) {
	h := newHarness(t)
	h.runner.failAt = "AgentStart"
	h.runner.failErr = errors.New("herdr agent start t --kind claude: exit status 1: " +
		`{"error":{"code":"agent_not_ready","message":"agent t is blocked during startup and is not ready for prompts"},"id":"cli:agent:start"}`)
	h.runner.readText = "Quick safety check: Is this a project you created or one you trust?\n" +
		"❯ No, exit\n  Yes, I trust this folder\nEnter to confirm · Esc to cancel"

	if code := h.run("--title", "t", "--no-worktree"); code != ExitFailed {
		t.Fatalf("exit = %d, want %d", code, ExitFailed)
	}
	stderr := h.stderr.String()
	for _, want := range []string{"answer the dialog in the pane", "Quick safety check", "agent_not_ready"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want it to contain %q", stderr, want)
		}
	}
}

// --- --json ---------------------------------------------------------------

// TestJSONShape pins the success object, including spec §10's provenance.
func TestJSONShape(t *testing.T) {
	h := newHarness(t)
	writeConfig(t, h.env.ConfigDir, `
branch_prefix = "zvi/"
default_placement = "tab-here"
`)
	h.env.WorkspaceID, h.env.TabID = "wS9", "tT9"

	if code := h.run("--title", "Fix Login", "--no-worktree", "--json"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}

	var out jsonReport
	if err := json.Unmarshal([]byte(h.stdout.String()), &out); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, h.stdout)
	}
	if !out.OK {
		t.Errorf("ok = false, want true")
	}
	if out.Title != "Fix Login" || out.ProjectDir != "/projects/thing" {
		t.Errorf("title/project_dir = %q/%q", out.Title, out.ProjectDir)
	}
	// wS9, not a workspace of its own: tab-here opens a tab in the
	// INVOKING workspace, and herdr's `tab create --workspace wS9`
	// answers with wS9's own id. The tab and the pane are new.
	if out.PaneID != "pP1" || out.WorkspaceID != "wS9" || out.TabID != "tT1" {
		t.Errorf("topology = %q/%q/%q, want wS9/tT1/pP1", out.WorkspaceID, out.TabID, out.PaneID)
	}
	if out.Placement != "tab-here" {
		t.Errorf("placement = %q, want the resolved tab-here", out.Placement)
	}
	if out.PromptSent != nil {
		t.Errorf("prompt_sent = %v, want it absent when there was no prompt", *out.PromptSent)
	}
	if got := out.Provenance["placement"]; got != "config.toml" {
		t.Errorf("provenance[placement] = %q, want config.toml", got)
	}
	if got := out.Provenance["worktree"]; got != "flag" {
		t.Errorf("provenance[worktree] = %q, want flag (--no-worktree was given)", got)
	}
}

// TestReportedIDsNameTheAgentNotTheSpace is #99. A worktree plus a `here`
// placement is the one combination that separates the two -- placement
// spec §6.1's headline case, and Cell 8's. The worktree op opens a whole
// new space for the checkout; the placement op then puts the agent's pane
// in a NEW TAB OF THE INVOKING WORKSPACE (build.go's placementOp always
// targets Input.Ctx), so the agent shares none of the space's three ids.
//
// With the worktree off the placement op IS the topology op, and with
// new-space there is no placement op, so those two agree and cannot show
// this. Nor can equivalence_test.go, which compares plan.Inputs: this is
// downstream of the plan entirely.
//
// What a caller does with the answer is the reason it matters: the id it
// greps out of a create is the one it sends the next keystroke to. Before
// this, that was the worktree's own idle shell -- or, when the workspace
// was reused, another session's pane (#99's live evidence, #91's family).
func TestReportedIDsNameTheAgentNotTheSpace(t *testing.T) {
	h := newHarness(t)
	h.env.WorkspaceID, h.env.TabID, h.env.PaneID = "wS9", "tT9", "pP9"

	if code := h.run("--title", "t", "--worktree", "--branch", "zvi/x", "--base", "main",
		"--placement", "tab-here", "--json"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}

	var out jsonReport
	if err := json.Unmarshal([]byte(h.stdout.String()), &out); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, h.stdout)
	}
	// The fake's own arithmetic: creation 1 is the worktree's space
	// (wS1/tT1/pP1), creation 2 the claimed tab, which herdr opens inside
	// the invoking workspace and so answers with wS9 (tT2/pP2).
	if out.WorkspaceID != "wS9" || out.TabID != "tT2" || out.PaneID != "pP2" {
		t.Errorf("agent ids = %q/%q/%q, want wS9/tT2/pP2 -- where the agent actually is",
			out.WorkspaceID, out.TabID, out.PaneID)
	}
	if out.SpaceWorkspaceID != "wS1" || out.SpaceTabID != "tT1" || out.SpacePaneID != "pP1" {
		t.Errorf("space ids = %q/%q/%q, want wS1/tT1/pP1 -- the space Clean acts on, still reported",
			out.SpaceWorkspaceID, out.SpaceTabID, out.SpacePaneID)
	}
	if out.CheckoutPath != "/checkouts/zvi/x" {
		t.Errorf("checkout_path = %q, want the worktree's own checkout", out.CheckoutPath)
	}
	// And the pane the agent was actually launched into is the one
	// reported, which is the whole claim -- read from the calls rather
	// than from the report, so the two have to agree.
	if !h.runner.called("AgentStart") || !strings.Contains(strings.Join(h.runner.calls, " "), ",pP2)") {
		t.Errorf("calls = %v, want the agent started in pP2", h.runner.calls)
	}
}

// TestHumanLineNamesTheAgentsPane is #99 for the caller that did not ask
// for --json. The line's whole reason to be key=value is that a shell
// greps `pane=` out of it, so it is the spelling the issue's failing
// script actually reads.
func TestHumanLineNamesTheAgentsPane(t *testing.T) {
	h := newHarness(t)
	h.env.WorkspaceID, h.env.TabID, h.env.PaneID = "wS9", "tT9", "pP9"

	if code := h.run("--title", "t", "--worktree", "--branch", "zvi/x", "--base", "main",
		"--placement", "tab-here"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	got := strings.TrimSpace(h.stdout.String())
	for _, want := range []string{"workspace=wS9", "tab=tT2", "pane=pP2"} {
		if !strings.Contains(got, want) {
			t.Errorf("stdout = %q, want it to contain %q", got, want)
		}
	}
	// The space's pane must not be what `pane=` names -- the exact
	// substring a grep would pick up.
	if strings.Contains(got, "pane=pP1") {
		t.Errorf("stdout = %q, still names the SPACE's pane", got)
	}
}

// TestFailureBeforeAnyAgentPaneReportsOnlyTheSpace pins the honest answer
// for a run that died between the two: the worktree's space exists, no
// pane was ever claimed for the agent, and so the agent triple is ABSENT
// rather than quietly filled in from the space. Filling it in is the
// tempting mistake -- the object looks more complete -- and it re-creates
// exactly the confusion #99 is about, one failure mode further along:
// three ids that claim to say where the agent is when there is no agent.
//
// Found by mutation: the fallback passed every other test in the package.
func TestFailureBeforeAnyAgentPaneReportsOnlyTheSpace(t *testing.T) {
	h := newHarness(t)
	h.env.WorkspaceID, h.env.TabID, h.env.PaneID = "wS9", "tT9", "pP9"
	h.runner.failAt = "TabCreate" // the placement op, after the worktree's space exists

	if code := h.run("--title", "t", "--worktree", "--branch", "zvi/x", "--base", "main",
		"--placement", "tab-here", "--json"); code != ExitFailed {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitFailed, h.stderr)
	}
	var out jsonReport
	if err := json.Unmarshal([]byte(h.stdout.String()), &out); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, h.stdout)
	}
	if out.WorkspaceID != "" || out.TabID != "" || out.PaneID != "" {
		t.Errorf("agent ids = %q/%q/%q, want all absent -- no pane was ever claimed for an agent",
			out.WorkspaceID, out.TabID, out.PaneID)
	}
	if out.SpaceWorkspaceID != "wS1" || out.SpacePaneID != "pP1" {
		t.Errorf("space ids = %q/%q, want wS1/pP1 -- the space that DOES exist and may need cleaning",
			out.SpaceWorkspaceID, out.SpacePaneID)
	}
}

// TestFailureLineNamesTheSpaceNotTheAgent is the other side of #99, and
// the reason it is a test rather than a comment: when the agent's ids and
// the space's diverge, the keep-or-clean line must keep naming the SPACE.
// It is the thing --on-failure clean would have removed and the thing a
// person has to go close by hand, so an agent pane id there would send
// them after the wrong container -- and docs/manual-smoke.md's Cell 8
// reads this exact line as its evidence ("naming the SPACE (not an agent
// pane) is the same evidence read a different way").
//
// Found by mutation: switching this line to AgentAt left every other test
// green, which is precisely how the opposite change gets made by someone
// tidying #99 up later.
func TestFailureLineNamesTheSpaceNotTheAgent(t *testing.T) {
	h := newHarness(t)
	h.env.WorkspaceID, h.env.TabID, h.env.PaneID = "wS9", "tT9", "pP9"
	h.runner.failAt = "AgentStart" // after both the space and the agent's pane exist

	if code := h.run("--title", "t", "--worktree", "--branch", "zvi/x", "--base", "main",
		"--placement", "tab-here", "--on-failure", "keep"); code != ExitFailed {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitFailed, h.stderr)
	}
	stderr := h.stderr.String()
	if !strings.Contains(stderr, "workspace wS1, pane pP1") {
		t.Errorf("stderr = %q, want the SPACE's ids (wS1/pP1) in the kept-session line", stderr)
	}
	if strings.Contains(stderr, "pane pP2") {
		t.Errorf("stderr = %q, names the AGENT's pane -- Clean acts on the space, and Cell 8 reads this line", stderr)
	}
}

// --- the argv boundary (issue #14) ----------------------------------------

// TestBranchLeadingDash pins that internal/herdrc's argv refusal -- a flag
// value the herdr CLI would read as another flag -- surfaces here as a
// plain exit 1 with a readable reason, not as a panic, a confusing wrap,
// or an attempt to run herdr with a mangled command line.
//
// It runs against a REAL herdrc.CLIRunner precisely because a fake could
// only re-state the rule rather than exercise it: the refusal happens
// while the argv is being assembled, before any process is started, so the
// runner's Bin never has to exist. If the refusal were ever lost, this
// would try to execute that path and the assertion below would fail with a
// different message rather than passing.
func TestBranchLeadingDash(t *testing.T) {
	h := newHarness(t)
	h.deps.Runner = &refusingRunner{
		fakeRunner: h.runner,
		real:       herdrc.CLIRunner{Bin: filepath.Join(t.TempDir(), "herdr-does-not-exist")},
	}

	code := h.run("--title", "t", "--worktree", "--branch", "--oops")
	if code != ExitFailed {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitFailed, h.stderr)
	}
	stderr := h.stderr.String()
	if !strings.Contains(stderr, `begins with "-"`) {
		t.Errorf("stderr = %q, want internal/herdrc's own refusal reason", stderr)
	}
	if !strings.Contains(stderr, "nothing was created") {
		t.Errorf("stderr = %q, want it to say nothing was created", stderr)
	}
}

// refusingRunner is the fake runner with WorktreeCreate delegated to a
// real CLIRunner, so the reachability probe stays offline while the one
// call under test goes through internal/herdrc's real argv assembly.
type refusingRunner struct {
	*fakeRunner
	real herdrc.CLIRunner
}

func (r *refusingRunner) WorktreeCreate(ctx context.Context, req herdrc.WorktreeCreateReq) (herdrc.CreatedTopology, error) {
	return r.real.WorktreeCreate(ctx, req)
}

// --- memory ---------------------------------------------------------------

// TestSuccessRecordsPerProjectMemory: a headless create feeds the same
// tiers the form reads, so the next form-open defaults to what actually
// ran (spec §10).
func TestSuccessRecordsPerProjectMemory(t *testing.T) {
	h := newHarness(t)

	if code := h.run("--title", "t", "--no-worktree", "--placement", "split-here", "--agent", "codex"); code != ExitUsage {
		// split-here needs a pane; this is only here to prove the guard
		// runs before anything is recorded.
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if _, err := os.Stat(filepath.Join(h.env.StateDir, "projects.json")); !os.IsNotExist(err) {
		t.Fatalf("a refused create wrote projects.json")
	}

	h = newHarness(t)
	if code := h.run("--title", "t", "--no-worktree", "--agent", "codex"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}

	projects, _ := config.LoadProjects(h.env.StateDir)
	entry, ok := projects.Get(projectMemoryKey("/projects/thing", "/projects/thing"))
	if !ok {
		t.Fatalf("projects.json has no entry for the project: %+v", projects)
	}
	if entry.Kind != "codex" {
		t.Errorf("remembered kind = %q, want codex", entry.Kind)
	}
	if entry.Worktree == nil || *entry.Worktree {
		t.Errorf("remembered worktree = %v, want false", entry.Worktree)
	}

	state, _ := config.LoadState(h.env.StateDir)
	if state.LastKind != "codex" || len(state.Recents) != 1 {
		t.Errorf("last-used/recents = %q/%v, want codex and one recent", state.LastKind, state.Recents)
	}
}

// TestNoPluginDirectoriesNeverReadsTheWorkingDirectory covers the normal
// headless environment: herdr exports the pane ids into a shell but not
// HERDR_PLUGIN_CONFIG_DIR/HERDR_PLUGIN_STATE_DIR, so both are empty here.
//
// The hazard is that config.Load/LoadState join their argument with a file
// name, making "" a RELATIVE path: a project with its own config.toml at
// the root would have had it parsed as this plugin's -- and a project
// whose config.toml is not even TOML (the fixture below) would have had
// the create refused outright. Nothing may be read from, or written to,
// the working directory.
func TestNoPluginDirectoriesNeverReadsTheWorkingDirectory(t *testing.T) {
	h := newHarness(t)
	// The whole plugin environment is absent, id included: herdr exports
	// HERDR_PLUGIN_* only to a launched plugin, never to a pane's shell
	// (herdr:src/app/api/plugins/env.rs), so this is the shape production
	// actually meets. Clearing only the directories would leave an id
	// with nothing to own and pin neither of usablePluginEnv's two
	// messages to the case it belongs to (#91).
	h.env.PluginID, h.env.ConfigDir, h.env.StateDir = "", "", ""

	wd := t.TempDir()
	t.Chdir(wd)
	writeConfig(t, wd, "this is not toml at all ][\n")

	if code := h.run("--title", "t", "--no-worktree"); code != ExitOK {
		t.Fatalf("exit = %d, want %d -- the working directory's own config.toml was read\nstderr: %s",
			code, ExitOK, h.stderr)
	}

	entries, err := os.ReadDir(wd)
	if err != nil {
		t.Fatalf("read working directory: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "config.toml" {
			t.Errorf("create left %q in the working directory", e.Name())
		}
	}
	if !strings.Contains(h.stderr.String(), "HERDR_PLUGIN_CONFIG_DIR and HERDR_PLUGIN_STATE_DIR not set") {
		t.Errorf("stderr = %q, want it to name the unset variables", h.stderr)
	}
	// An environment with nothing in it is not a foreign one. Saying it is
	// not demonstrably ours would be true and useless: there is nothing to
	// refuse, and the advice for the two cases is different.
	if strings.Contains(h.stderr.String(), "ignoring it") {
		t.Errorf("stderr = %q, want the unset message, not the refusal", h.stderr)
	}
}

// TestAnotherPluginsEnvironmentIsIgnored covers the leak this command can
// actually meet: a shell started inside ANOTHER plugin's pane inherits its
// HERDR_PLUGIN_* environment, so the directories point at that plugin and
// the context JSON describes its invocation, not this pane. All of it is
// dropped together, and the pane ids -- which are this pane's -- are kept.
func TestAnotherPluginsEnvironmentIsIgnored(t *testing.T) {
	h := newHarness(t)
	otherState := t.TempDir()
	h.env.PluginID = "someone.else"
	h.env.StateDir = otherState
	h.env.ContextJSON = `{"workspace_id":"wOTHER","tab_id":"tOTHER"}`
	h.env.WorkspaceID, h.env.TabID = "wS9", "tT9"

	if code := h.run("--title", "t", "--no-worktree", "--placement", "tab-here"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	if !strings.Contains(strings.Join(h.runner.calls, " "), "TabCreate(wS9,") {
		t.Errorf("calls = %v, want the tab in THIS pane's workspace, not the other plugin's", h.runner.calls)
	}
	if entries, _ := os.ReadDir(otherState); len(entries) != 0 {
		t.Errorf("wrote %d file(s) into another plugin's state directory", len(entries))
	}
	if !strings.Contains(h.stderr.String(), `belongs to plugin "someone.else"`) {
		t.Errorf("stderr = %q, want it to name the plugin whose environment was ignored", h.stderr)
	}
}

// TestPluginEnvironmentWithoutAnIdIsIgnored is #91: the guard above used
// to require a NON-EMPTY $HERDR_PLUGIN_ID before it would refuse an
// environment, and the leak this command actually meets does not carry
// one. Observed live: HERDR_PLUGIN_CONFIG_DIR and HERDR_PLUGIN_STATE_DIR
// pointing at another plugin, HERDR_PLUGIN_ID unset. Absent is not the
// same as matching -- an environment that does not say whose it is is not
// demonstrably ours -- so it takes the same path as a foreign id.
//
// herdr never produces that shape for a plugin it launched itself:
// plugin_path_env, which is the only thing that exports the directory
// pair (herdr:src/app/api/plugins/env.rs), has exactly two callers and
// both push HERDR_PLUGIN_ID in the same function
// (https://github.com/herdrdev/herdr/blob/b1ff4582/src/app/api/plugins/panes.rs#L251
// and .../runtime.rs#L39). "The directories are set" is therefore not
// evidence of anything on its own.
//
// All three harms #91 recorded live are asserted in every case below:
// our state files written into the other plugin's directory, its
// config.toml read as ours, and its invocation context taken over this
// pane's own ids. Not every assertion discriminates in every case -- with
// no state directory there is nowhere to write wrongly -- but each case
// discriminates on at least one, and running all four everywhere is what
// keeps a subset from looking safe.
func TestPluginEnvironmentWithoutAnIdIsIgnored(t *testing.T) {
	// The three variables the id owns, in the subsets an inherited
	// environment can actually arrive in. The first case is the live
	// capture verbatim -- both directories, NO context JSON -- and it is
	// the one that matters: a guard keyed on the context JSON alone would
	// pass a fixture that set all three while leaving the observed leak
	// wide open, which is this repo's most expensive recurring defect
	// (see CONTRIBUTING.md, "Make a test able to fail") seen from the
	// fixture side rather than the fake's.
	cases := []struct {
		name        string
		dirs        bool
		contextJSON bool
	}{
		{name: "the shape observed live: both directories, no context JSON", dirs: true},
		{name: "the whole inherited environment", dirs: true, contextJSON: true},
		{name: "the context JSON alone", contextJSON: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			otherConfig, otherState := t.TempDir(), t.TempDir()
			h.env.PluginID = ""
			h.env.ConfigDir, h.env.StateDir = "", ""
			if c.dirs {
				h.env.ConfigDir, h.env.StateDir = otherConfig, otherState
				// A foreign config.toml that changes what this run does if
				// it is read as ours: the first favorite is the kind an
				// unspecified --agent falls through to.
				writeConfig(t, otherConfig, "[agents]\nfavorites = [\"codex\"]\n")
			}
			if c.contextJSON {
				h.env.ContextJSON = `{"workspace_id":"wOTHER","tab_id":"tOTHER"}`
			}
			h.env.WorkspaceID, h.env.TabID = "wS9", "tT9"

			if code := h.run("--title", "t", "--no-worktree", "--placement", "tab-here"); code != ExitOK {
				t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
			}
			if !strings.Contains(strings.Join(h.runner.calls, " "), "TabCreate(wS9,") {
				t.Errorf("calls = %v, want the tab in THIS pane's workspace, not the other plugin's", h.runner.calls)
			}
			if !strings.Contains(h.stdout.String(), "agent=claude") {
				t.Errorf("stdout = %q, want the built-in agent kind, not the other plugin's configured favorite", h.stdout)
			}
			if entries, _ := os.ReadDir(otherState); len(entries) != 0 {
				t.Errorf("wrote %d file(s) into another plugin's state directory", len(entries))
			}
			// The diagnosis, not the advice that follows it: the advice
			// names HERDR_PLUGIN_ID too, so a bare substring check on the
			// name passes even with the reason removed.
			if !strings.Contains(h.stderr.String(), "HERDR_PLUGIN_ID is not set") {
				t.Errorf("stderr = %q, want it to say which variable was missing and why that decided it", h.stderr)
			}
		})
	}
}

// --- --issue --------------------------------------------------------------

// TestIssueSeedsTitleBranchAndPrompt: the same three seedings the form
// does on an issue selection (spec §6 field 1), through the same template.
func TestIssueSeedsTitleBranchAndPrompt(t *testing.T) {
	h := newHarness(t)
	h.deps.Linear = &fakeLinear{issues: []linear.Issue{{
		Identifier: "LIN-42", Title: "Fix login redirect loop",
		BranchName: "zvi/lin-42-fix-login", URL: "https://linear.app/x/LIN-42",
		Description: "it loops",
	}}}

	if code := h.run("--issue", "lin-42", "--worktree"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	joined := strings.Join(h.runner.calls, " ")
	if !strings.Contains(joined, "zvi/lin-42-fix-login") {
		t.Errorf("calls = %v, want the issue's own branchName", h.runner.calls)
	}
	if !strings.Contains(joined, "Work on LIN-42: Fix login redirect loop") {
		t.Errorf("calls = %v, want the seeded prompt", h.runner.calls)
	}
}

// TestIssueWithoutLinear refuses rather than silently creating a session
// with none of the seeding that was asked for.
func TestIssueWithoutLinear(t *testing.T) {
	h := newHarness(t)

	if code := h.run("--issue", "LIN-1", "--no-worktree"); code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(h.stderr.String(), "Linear is not configured") {
		t.Errorf("stderr = %q, want it to say Linear is unconfigured", h.stderr)
	}
}

// --- usage ----------------------------------------------------------------

// flagNames is every flag this command accepts, in the order the usage
// text lists them.
//
// It lives here rather than beside createUsage in flags.go (#16 item 3):
// it has never had a production reader, and its old doc comment named a
// usage_test.go that does not exist, which is what a list nobody outside
// one test consults looks like after a while. In a _test.go file it is
// what the rest of this package's helpers are -- newHarness, writeConfig
// -- a test's own data, held where the assertion that needs it is.
//
// Note what it does NOT protect: the list is a third hand-maintained
// copy beside parseArgs' registrations and createUsage's prose, so a flag
// added to the first two and not to this one still passes. Deriving it
// from parseArgs' own flag.FlagSet would close that, and would mean
// splitting the FlagSet construction out of parseArgs for a test's
// benefit; left undone deliberately, not overlooked.
var flagNames = []string{
	"project", "title", "prompt", "branch", "base",
	"worktree", "no-worktree", "placement", "agent", "account",
	"issue", "json", "on-failure",
}

// TestUsageListsEveryFlag keeps the hand-written usage block honest: a
// flag added to parseArgs and forgotten here is a flag nobody can
// discover.
func TestUsageListsEveryFlag(t *testing.T) {
	for _, name := range flagNames {
		if !strings.Contains(createUsage, "--"+name) {
			t.Errorf("createUsage does not mention --%s", name)
		}
	}
}

// TestHelpExitsZero: asking for help is not a usage error.
func TestHelpExitsZero(t *testing.T) {
	h := newHarness(t)
	if code := h.run("--help"); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	if !strings.Contains(h.stdout.String(), "usage: herdr-draft create") {
		t.Errorf("stdout = %q, want the usage on stdout", h.stdout)
	}
}

// writeConfig puts a config.toml in dir.
func writeConfig(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
}

// TestPromptWaitTimeoutReportsUnconfirmed is #108 as a headless caller
// sees it. The wait timed out; the prompt may well have been delivered.
//
// Three things this pins, one per harm the issue names:
//
//   - `prompt_sent` is ABSENT rather than false. It is a *bool, and false
//     is an assertion this run cannot make. A consumer that branches on
//     true/false keeps working; one that wants the third state reads
//     prompt_status.
//   - the text comes back under `unconfirmed_prompt`, never
//     `unsent_prompt`. The name is the whole trap: `unsent_prompt` means
//     "this failure destroyed your work, here it is back", and pasting it
//     into a working agent sends the instructions twice.
//   - `clean_refused` is set even though the caller asked for `clean`, and
//     nothing was removed.
func TestPromptWaitTimeoutReportsUnconfirmed(t *testing.T) {
	h := newHarness(t)
	h.runner.failAt = "AgentPrompt"
	h.runner.failErr = fmt.Errorf("%w: herdr agent prompt pP1 ...: exit status 1: "+
		`{"error":{"code":"timeout","message":"timed out waiting for agent status"},"id":"cli:agent:prompt"}`,
		herdrc.ErrPromptWaitTimeout)

	code := h.run("--title", "t", "--no-worktree", "--prompt", "a large handoff prompt",
		"--on-failure", "clean", "--json")
	if code != ExitFailed {
		t.Fatalf("exit = %d, want %d", code, ExitFailed)
	}

	var out jsonReport
	if err := json.Unmarshal([]byte(h.stdout.String()), &out); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, h.stdout)
	}
	if out.PromptSent != nil {
		t.Errorf("prompt_sent = %v, want it absent -- neither true nor false is knowable here", *out.PromptSent)
	}
	if out.PromptStatus != "unconfirmed" {
		t.Errorf("prompt_status = %q, want %q", out.PromptStatus, "unconfirmed")
	}
	if out.UnsentPrompt != "" {
		t.Errorf("unsent_prompt = %q, want it absent -- the prompt may have been delivered", out.UnsentPrompt)
	}
	if out.UnconfirmedPrompt != "a large handoff prompt" {
		t.Errorf("unconfirmed_prompt = %q, want the prompt text back", out.UnconfirmedPrompt)
	}
	if out.Cleaned {
		t.Error("cleaned = true -- --on-failure clean tore down a session that may be mid-turn")
	}
	if out.CleanRefused == "" {
		t.Error("clean_refused is empty; a refused clean has to say why")
	}
	if h.runner.called("WorkspaceClose") || h.runner.called("WorktreeRemove") {
		t.Errorf("something was removed anyway: %v", h.runner.calls)
	}
}

// TestPromptWaitTimeoutHumanOutputDoesNotClaimItWasNotSent is the same run
// without --json, because the human wording is what actually caused the
// double-paste: a caller reads "the prompt was not sent -- reproduced here
// so it is not lost" and does the obvious thing with it.
func TestPromptWaitTimeoutHumanOutputDoesNotClaimItWasNotSent(t *testing.T) {
	h := newHarness(t)
	h.runner.failAt = "AgentPrompt"
	h.runner.failErr = fmt.Errorf("%w: exit status 1", herdrc.ErrPromptWaitTimeout)

	if code := h.run("--title", "t", "--no-worktree", "--prompt", "the handoff"); code != ExitFailed {
		t.Fatalf("exit = %d, want %d", code, ExitFailed)
	}

	all := h.stdout.String() + h.stderr.String()
	if strings.Contains(all, "the prompt was not sent") {
		t.Errorf("output asserts the prompt was not sent:\n%s", all)
	}
	if !strings.Contains(all, "read the pane") {
		t.Errorf("output does not tell the caller to read the pane:\n%s", all)
	}
	// The text still has to be recoverable -- losing it is the worse error.
	if !strings.Contains(all, "the handoff") {
		t.Errorf("output dropped the prompt text entirely:\n%s", all)
	}
}

// TestOrdinaryUnsentPromptStillSaysUnsent is the control. The dialog
// guard's refusal really did not deliver the prompt, so that path must
// keep its old wording, its `prompt_sent: false` and its `unsent_prompt`
// -- the new state must not have swallowed the honest failure.
func TestOrdinaryUnsentPromptStillSaysUnsent(t *testing.T) {
	h := newHarness(t)
	h.runner.readText = "Quick safety check\nDo you trust this folder?"

	if code := h.run("--title", "t", "--no-worktree", "--prompt", "p", "--json"); code != ExitFailed {
		t.Fatalf("exit = %d, want %d", code, ExitFailed)
	}

	var out jsonReport
	if err := json.Unmarshal([]byte(h.stdout.String()), &out); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, h.stdout)
	}
	if out.PromptSent == nil || *out.PromptSent {
		t.Errorf("prompt_sent = %v, want false", out.PromptSent)
	}
	if out.PromptStatus != "unsent" {
		t.Errorf("prompt_status = %q, want %q", out.PromptStatus, "unsent")
	}
	if out.UnsentPrompt != "p" {
		t.Errorf("unsent_prompt = %q, want the prompt back", out.UnsentPrompt)
	}
	if out.UnconfirmedPrompt != "" {
		t.Errorf("unconfirmed_prompt = %q, want it absent", out.UnconfirmedPrompt)
	}
}

// TestSuccessfulPromptStatusIsSent keeps the happy path honest: three
// states means the successful one is named too, not left to be inferred
// from a field's absence.
func TestSuccessfulPromptStatusIsSent(t *testing.T) {
	h := newHarness(t)

	if code := h.run("--title", "t", "--no-worktree", "--prompt", "p", "--json"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitOK, h.stderr)
	}

	var out jsonReport
	if err := json.Unmarshal([]byte(h.stdout.String()), &out); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, h.stdout)
	}
	if out.PromptSent == nil || !*out.PromptSent {
		t.Errorf("prompt_sent = %v, want true", out.PromptSent)
	}
	if out.PromptStatus != "sent" {
		t.Errorf("prompt_status = %q, want %q", out.PromptStatus, "sent")
	}
}
