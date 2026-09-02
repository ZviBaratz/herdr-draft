package create

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
	"github.com/ZviBaratz/herdr-draft/internal/linear"
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

	topo     herdrc.CreatedTopology
	listErr  error
	failAt   string
	readText string
}

var _ herdrc.Runner = (*fakeRunner)(nil)

func newFakeRunner() *fakeRunner {
	return &fakeRunner{topo: herdrc.CreatedTopology{
		WorkspaceID: "wS1", TabID: "tT1", PaneID: "pP1",
	}}
}

func (r *fakeRunner) record(name string, args ...string) error {
	r.calls = append(r.calls, name+"("+strings.Join(args, ",")+")")
	if name == r.failAt {
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
	return nil, r.listErr
}

func (r *fakeRunner) WorktreeCreate(_ context.Context, req herdrc.WorktreeCreateReq) (herdrc.CreatedTopology, error) {
	if err := r.record("WorktreeCreate", req.Cwd, req.Branch, req.Base); err != nil {
		return herdrc.CreatedTopology{}, err
	}
	topo := r.topo
	topo.CheckoutPath = "/checkouts/" + req.Branch
	return topo, nil
}

func (r *fakeRunner) WorkspaceCreate(_ context.Context, req herdrc.WorkspaceCreateReq) (herdrc.CreatedTopology, error) {
	if err := r.record("WorkspaceCreate", req.Cwd, req.Label); err != nil {
		return herdrc.CreatedTopology{}, err
	}
	return r.topo, nil
}

func (r *fakeRunner) TabCreate(_ context.Context, req herdrc.TabCreateReq) (herdrc.CreatedTopology, error) {
	if err := r.record("TabCreate", req.Workspace, req.Cwd); err != nil {
		return herdrc.CreatedTopology{}, err
	}
	return r.topo, nil
}

func (r *fakeRunner) PaneSplit(_ context.Context, req herdrc.PaneSplitReq) (herdrc.CreatedTopology, error) {
	if err := r.record("PaneSplit", req.PaneID, req.Direction); err != nil {
		return herdrc.CreatedTopology{}, err
	}
	return r.topo, nil
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
	git := newFakeGit()
	h := &harness{
		env: Env{
			ConfigDir: t.TempDir(),
			StateDir:  t.TempDir(),
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
// table: a new space and a worktree create fine with no herdr environment
// at all, while tab-here and split-here refuse and name the exact variable
// they are missing.
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
			name:     "a worktree needs nothing",
			args:     []string{"--title", "t", "--worktree"},
			wantCode: ExitOK,
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
	if out.PaneID != "pP1" || out.WorkspaceID != "wS1" || out.TabID != "tT1" {
		t.Errorf("topology = %q/%q/%q, want the created ids", out.WorkspaceID, out.TabID, out.PaneID)
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
	h.env.ConfigDir, h.env.StateDir = "", ""

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
