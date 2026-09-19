package create

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ZviBaratz/herdr-draft/internal/clauth"
	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
	"github.com/ZviBaratz/herdr-draft/internal/linear"
	"github.com/ZviBaratz/herdr-draft/internal/picker"
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

	// readTextShowsAfter delays readText until after that many AgentRead
	// calls, answering with a painted, dialog-free pane until then. The
	// ORDER is what it exists for: the guard's first read passes, the send
	// goes out and stalls, and the dialog has painted by the time the
	// retry's guard looks (#132's rider).
	readTextShowsAfter int
	readCalls          int

	// postPromptErr is what AgentRead fails with once AgentPrompt has
	// succeeded -- the pane AFTER a send, as internal/plan's mockRunner and
	// internal/app's submitFakeRunner model it under the same name. A
	// failAt of AgentRead cannot express it: that also fails the guard's
	// read BEFORE the send, so nothing would ever be typed (#154).
	postPromptErr error
	promptSent    bool

	// failTimes bounds how many calls to failAt actually fail. Zero means
	// all of them, which is what every test that leaves it alone means by
	// failAt; a positive N fails the first N and lets the rest through,
	// which is the only way to model a failure a RETRY recovers from.
	// failed counts the ones that did.
	failTimes int
	failed    int

	// failErr, when non-nil, is what failAt fails with instead of the
	// generic error below -- needed only where the error's own TEXT is
	// what the test is about, since herdrc.CLIRunner surfaces herdr's
	// error codes as plain text and internal/plan matches on them by
	// substring.
	failErr error

	// worktreeAdd and worktreeRemove, when non-nil, do on disk what herdr's
	// `worktree create` and `worktree remove` do, so a clean can act on a
	// real repository (#173): internal/plan asks git itself whether the
	// branch was there before and whether the checkout is safe to remove,
	// and a path this fake invented answers neither. worktreeAdd returns
	// the checkout it made, which replaces the invented one.
	worktreeAdd    func(herdrc.WorktreeCreateReq) string
	worktreeRemove func(workspaceID string)

	// replyBranch, when set, is the branch the worktree create's reply
	// names whatever was asked for: the reply git's guess produced (#198),
	// which internal/plan refuses.
	replyBranch string
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
	if name == r.failAt && (r.failTimes == 0 || r.failed < r.failTimes) {
		r.failed++
		if r.failErr != nil {
			return r.failErr
		}
		return errors.New("the pane was busy")
	}
	return nil
}

func (r *fakeRunner) called(name string) bool { return r.countCalls(name) > 0 }

func (r *fakeRunner) countCalls(name string) int {
	n := 0
	for _, c := range r.calls {
		if strings.HasPrefix(c, name+"(") {
			n++
		}
	}
	return n
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
	// herdr's reply names the branch it used: the request's, trimmed, or
	// one it invented for a request that named none (#173).
	topo.Branch = strings.TrimSpace(req.Branch)
	if topo.Branch == "" {
		topo.Branch = "worktree/brave-river-0000"
	}
	if r.replyBranch != "" {
		topo.Branch = r.replyBranch
	}
	if r.worktreeAdd != nil {
		topo.CheckoutPath = r.worktreeAdd(req)
	}
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

func (r *fakeRunner) TabRename(_ context.Context, req herdrc.TabRenameReq) error {
	return r.record("TabRename", req.TabID, req.Label)
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
	if err := r.record("AgentPrompt", req.Target, req.Text); err != nil {
		return err
	}
	r.promptSent = true
	return nil
}

func (r *fakeRunner) AgentRead(_ context.Context, target string) (string, error) {
	if err := r.record("AgentRead", target); err != nil {
		return "", err
	}
	if r.promptSent && r.postPromptErr != nil {
		return "", r.postPromptErr
	}
	r.readCalls++
	if r.readText == "" || r.readCalls <= r.readTextShowsAfter {
		// A painted, dialog-free pane. The zero value cannot be the empty
		// screen any more: after #116 that is what a pane looks like
		// before it has drawn the dialog that eats the prompt, and
		// promptIfReady refuses it (headless `create` refuses outright,
		// having no one at the keyboard to wait for).
		return paintedIdleScreen, nil
	}
	return r.readText, nil
}

const paintedIdleScreen = "> Sonnet 5 · claude-code\n  Type your message...\n"

func (r *fakeRunner) AwaitDetection(_ context.Context, paneID string, _, blockedTimeout time.Duration) error {
	// The blocked budget is part of the record on purpose: headless
	// `create` must never wait for a dialog nobody is there to answer
	// (#115's decision 2), and a zero here is what proves it.
	return r.record("AwaitDetection", paneID+" blocked="+blockedTimeout.String())
}

func (r *fakeRunner) PaneRun(_ context.Context, paneID string, env []herdrc.EnvVar, argv []string) error {
	rec := []string{paneID}
	for _, e := range env {
		rec = append(rec, e.Name+"="+e.Value)
	}
	return r.record("PaneRun", append(rec, argv...)...)
}

func (r *fakeRunner) PaneClose(_ context.Context, paneID string) error {
	return r.record("PaneClose", paneID)
}

func (r *fakeRunner) TabClose(_ context.Context, tabID string) error {
	return r.record("TabClose", tabID)
}

func (r *fakeRunner) WorktreeRemove(_ context.Context, workspaceID string) error {
	if err := r.record("WorktreeRemove", workspaceID); err != nil {
		return err
	}
	if r.worktreeRemove != nil {
		r.worktreeRemove(workspaceID)
	}
	return nil
}

func (r *fakeRunner) WorkspaceClose(_ context.Context, workspaceID string) error {
	return r.record("WorkspaceClose", workspaceID)
}

// fakeGit implements GitSource: an existing git repository whose root is
// itself, unless a test says otherwise.
type fakeGit struct {
	exists bool
	isRepo bool
	// branches names the branches BranchExists reports as already there,
	// locally or on a remote -- nil for every test that does not care.
	branches map[string]bool
	// roots overrides RepoRoot's answer for a directory, which is otherwise
	// the directory itself; primary maps a linked worktree checkout to its
	// repository's primary checkout. Both nil for every test that is not
	// about one.
	roots   map[string]string
	primary map[string]string
	// commits answers ResolveCommit, keyed "<dir> <ref>": a ref asked about
	// anywhere else is an error, so asking the wrong checkout cannot pass
	// for asking the right one. resolveCalls records every question in that
	// form, and resolveErr fails them all.
	commits      map[string]string
	resolveErr   error
	resolveCalls []string
	// branchAsks records every name BranchExists was asked about.
	branchAsks []string
}

var _ GitSource = (*fakeGit)(nil)

func newFakeGit() *fakeGit { return &fakeGit{exists: true, isRepo: true} }

func (g *fakeGit) DirExists(string) bool { return g.exists }
func (g *fakeGit) IsGitRepo(string) bool { return g.isRepo }
func (g *fakeGit) BranchExists(_ context.Context, _ string, name string) (bool, error) {
	g.branchAsks = append(g.branchAsks, name)
	return g.branches[name], nil
}
func (g *fakeGit) RepoRoot(_ context.Context, dir string) (string, error) {
	if !g.isRepo {
		return "", nil
	}
	if root, ok := g.roots[dir]; ok {
		return root, nil
	}
	return dir, nil
}
func (g *fakeGit) PrimaryCheckout(_ context.Context, dir string) (string, error) {
	return g.primary[dir], nil
}
func (g *fakeGit) ResolveCommit(_ context.Context, dir, ref string) (string, error) {
	key := dir + " " + ref
	g.resolveCalls = append(g.resolveCalls, key)
	if g.resolveErr != nil {
		return "", g.resolveErr
	}
	if c, ok := g.commits[key]; ok {
		return c, nil
	}
	return "", fmt.Errorf("fakeGit: no commit for %q in %s", ref, dir)
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
	// The project is a repository with commits in it, so an explicit --base
	// has something to name (#194 refuses one that names nothing). A lane
	// replaces these with its own (inLane).
	git.commits = repoCommitsIn("/projects/thing")
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
	if got := strings.Count(h.stderr.String(), "... ok"); got != 3 {
		t.Errorf("stderr = %q, want one progress line per op", h.stderr)
	}
	// The workspace's own tab carries the title, not herdr's `1`.
	if !slices.Contains(h.runner.calls, "TabRename(tT1,fix login redirect)") {
		t.Errorf("calls = %v, want the new workspace's tab named for the session", h.runner.calls)
	}
}

// TestExitZero_ATabThatKeptHerdrsNameIsStillACreate: naming the tab is
// cosmetic, so its failure is a progress line and nothing more. Exit 0,
// the created line on stdout, and no failure line -- a caller that reads
// the exit code or the JSON `ok` must see the session it got, and one that
// reads stderr sees why its tab is still called `1`.
func TestExitZero_ATabThatKeptHerdrsNameIsStillACreate(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		t.Run(fmt.Sprintf("json=%v", asJSON), func(t *testing.T) {
			h := newHarness(t)
			h.runner.failAt = "TabRename"
			h.runner.failErr = errors.New(`{"error":{"code":"tab_not_found"}}`)
			args := []string{"--title", "fix login redirect", "--no-worktree"}
			if asJSON {
				args = append(args, "--json")
			}

			if code := h.run(args...); code != ExitOK {
				t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
			}
			if !h.runner.called("AgentStart") {
				t.Fatalf("calls = %v, want the run to have gone on to start the agent", h.runner.calls)
			}
			stderr := h.stderr.String()
			if !strings.Contains(stderr, "[2/3] naming tab ... failed, continuing: ") || !strings.Contains(stderr, "tab_not_found") {
				t.Errorf("stderr = %q, want the rename's own line to say it failed, that the run went on, and why", stderr)
			}
			if strings.Contains(stderr, "herdr-draft create: failed") {
				t.Errorf("stderr = %q, want no failure line for a run that created its session", stderr)
			}
			out := h.stdout.String()
			if !asJSON {
				if !strings.HasPrefix(out, "created ") {
					t.Errorf("stdout = %q, want the created line", out)
				}
				return
			}
			var got map[string]any
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatalf("stdout is not one JSON object: %v\n%s", err, out)
			}
			if got["ok"] != true {
				t.Errorf("ok = %v, want true", got["ok"])
			}
			for _, k := range []string{"error", "failed_step"} {
				if _, present := got[k]; present {
					t.Errorf("--json carries %q = %v, want it absent on a created session", k, got[k])
				}
			}
		})
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

// TestExitOne_OnFailureCleanRemovesOnlyTheHerePlacement is the same defect
// at the headless verb, where it needs no keypress at all. With
// --no-worktree and a `here` placement the plan has no space op of its own
// -- the placement op IS the topology op, made inside the invoking
// workspace -- so the clean used to run `herdr workspace close` on the
// workspace the command was run FROM, killing the caller along with
// everything else in it.
//
// The ids are the harness fake's own arithmetic: the invoking pane is
// pUSER in wUSER/tUSER, and the single creation is numbered 1, so a split
// beside pUSER answers pP1 in the invoking tab and a tab in wUSER answers
// tT1. Those are what the clean must close, and wUSER is what it must not.
func TestExitOne_OnFailureCleanRemovesOnlyTheHerePlacement(t *testing.T) {
	for _, tc := range []struct {
		placement string
		want      string
	}{
		{"split-here", "PaneClose(pP1)"},
		{"tab-here", "TabClose(tT1)"},
	} {
		t.Run(tc.placement, func(t *testing.T) {
			h := newHarness(t)
			h.env.WorkspaceID, h.env.TabID, h.env.PaneID = "wUSER", "tUSER", "pUSER"
			h.runner.failAt = "AgentStart"

			code := h.run("--title", "t", "--no-worktree", "--placement", tc.placement,
				"--on-failure", "clean", "--json")
			if code != ExitFailed {
				t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitFailed, h.stderr)
			}

			var out jsonReport
			if err := json.Unmarshal([]byte(h.stdout.String()), &out); err != nil {
				t.Fatalf("stdout is not JSON: %v\n%s", err, h.stdout)
			}
			if !out.Cleaned {
				t.Errorf("cleaned = false -- the clean is safe now and must still happen (clean_refused = %q)", out.CleanRefused)
			}
			if h.runner.called("WorkspaceClose") {
				t.Fatalf("the clean closed the invoking workspace: %v", h.runner.calls)
			}
			if got := h.runner.calls[len(h.runner.calls)-1]; got != tc.want {
				t.Fatalf("calls = %v, want them to end in %s", h.runner.calls, tc.want)
			}
		})
	}
}

// TestExitOne_TopologyItselfFails covers a failed first step with no space
// to keep or clean and no evidence either way about what herdr did: this
// fake's error is not one herdrc marks herdrc.ErrNothingCreated. It is still
// exit 1, and it claims neither a session nor the absence of one (#192).
// TestExitNothingCreated_FirstStepRefused is the same step WITH the
// evidence.
func TestExitOne_TopologyItselfFails(t *testing.T) {
	h := newHarness(t)
	h.runner.failAt = "WorkspaceCreate"

	if code := h.run("--title", "t", "--no-worktree", "--on-failure", "clean"); code != ExitFailed {
		t.Fatalf("exit = %d, want %d", code, ExitFailed)
	}
	if h.runner.called("WorkspaceClose") || h.runner.called("WorktreeRemove") {
		t.Errorf("no space was reported, but a clean was attempted: %v", h.runner.calls)
	}
	if want := "\nherdr may have made part of it before failing; look before retrying"; !strings.Contains(h.stderr.String(), want) {
		t.Errorf("stderr = %q\nwant it to contain %q", h.stderr, want)
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
	// The worktree op is both the space op and the agent-pane op (since
	// placement spec §14 a worktree session never has a separate
	// placement op), so the reuse claim's TabCreate runs inside this same
	// step and its failure is what Task 3 taught Execute to still report
	// the space for.
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
		{"reap contradiction", []string{"--title", "t", "--reap", "--no-reap"}, "--reap and --no-reap contradict"},
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
// table: only tab-here and split-here need the herdr environment -- a new
// space creates fine with no herdr environment at all, while tab-here and
// split-here refuse and name the exact variable they are missing. A
// worktree never gets that far with a `here` placement: it is refused
// first, for a reason no environment could fix (placement spec §14).
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
			name:     "a worktree refuses tab-here before asking for any context",
			args:     []string{"--title", "t", "--worktree", "--placement", "tab-here"},
			wantCode: ExitUsage,
			wantErr:  "--no-worktree",
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

// --- a stalled prompt -----------------------------------------------------
//
// These three close a coverage gap: nothing in this package drove a stall at
// all, so the popup-only retry and the "unsent" label a surviving stall wore
// were both invisible from here. They cost about two seconds each in real
// time: plan.promptRetrySettle is that package's own unexported var, and the
// settle is deliberately not an ExecOpts knob, since the whole point of the
// fix is that both paths retry identically.

// stalledPromptErr is herdr's agent_prompt_stalled as internal/herdrc
// classifies it: `agent prompt` observed no working and no blocked state at
// all within AGENT_PROMPT_EFFECT_TIMEOUT_MS. The sentinel is what
// internal/plan matches on, so the JSON tail is here only because that is
// the shape a real failure arrives in.
func stalledPromptErr() error {
	return fmt.Errorf("%w: herdr agent prompt wS1:pP1 ...: exit status 1: "+
		`{"error":{"code":"agent_prompt_stalled","message":"agent prompt produced no observed working or blocked state within 5000 ms; current status is idle"},"id":"cli:agent:prompt"}`,
		herdrc.ErrPromptStalled)
}

// TestStalledPromptIsRetriedThenReportedUnconfirmed is the whole fix end to
// end. Two facts, and before it `create` got neither: the retry was gated on
// `[timeouts] trust_wait_ms`, which headless `create` passes as zero on
// purpose, and the single stall that resulted was then labelled "unsent" --
// which README documents as "resend it; it never arrived".
//
// herdr writes the prompt text and Enter before it starts waiting
// (herdr v0.9.0, src/api/wait.rs), so after two sends "it never arrived" is
// not something this command knows. The posture is therefore the same one
// #108 established for the other unknown-delivery sentinel.
func TestStalledPromptIsRetriedThenReportedUnconfirmed(t *testing.T) {
	const prompt = "implement the fix"

	h := newHarness(t)
	h.runner.failAt = "AgentPrompt"
	h.runner.failErr = stalledPromptErr()

	code := h.run("--title", "t", "--no-worktree", "--prompt", prompt, "--json")
	if code != ExitFailed {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitFailed, h.stderr)
	}
	if n := h.runner.countCalls("AgentPrompt"); n != 2 {
		t.Errorf("AgentPrompt called %d times, want 2 -- a stall is retried once in `create` too: %v",
			n, h.runner.calls)
	}

	var out jsonReport
	if err := json.Unmarshal([]byte(h.stdout.String()), &out); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, h.stdout)
	}
	if out.OK {
		t.Errorf("ok = true, want false")
	}
	if out.PromptStatus != promptStatusUnconfirmed {
		t.Errorf("prompt_status = %q, want %q", out.PromptStatus, promptStatusUnconfirmed)
	}
	// Absent, not false: neither value is a statement this command can make
	// after two sends, and a consumer branching on true/false must be made
	// to notice rather than quietly read the zero value.
	if out.PromptSent != nil {
		t.Errorf("prompt_sent = %v, want it absent", *out.PromptSent)
	}
	if strings.Contains(h.stdout.String(), "prompt_sent") {
		t.Errorf("stdout carries a prompt_sent key:\n%s", h.stdout)
	}
	if out.UnconfirmedPrompt != prompt {
		t.Errorf("unconfirmed_prompt = %q, want the prompt text back", out.UnconfirmedPrompt)
	}
	if out.UnsentPrompt != "" {
		t.Errorf("unsent_prompt = %q, want it absent -- that key is what tells a script to resend",
			out.UnsentPrompt)
	}
	// The `error` field is the loudest surface -- what a human reads first
	// and what a script logs -- and it carries herdrc's own sentinel text
	// wrapped inside plan's. Three of the four surfaces were corrected
	// first and this one was left asserting the opposite, so the same
	// object said "nothing was delivered" and "delivery is unconfirmed" at
	// once. Asserted on the retracted claim itself, not on the word
	// "delivered", which CleanCheck's timeout reason uses correctly.
	for _, retracted := range []string{"nothing was delivered", "not delivered", "was not sent"} {
		if strings.Contains(out.Error, retracted) {
			t.Errorf("error field claims %q after two sends:\n%s", retracted, out.Error)
		}
	}

	// The same run without --json, because the prompt text and its
	// instruction are written to stderr only in human mode.
	h2 := newHarness(t)
	h2.runner.failAt = "AgentPrompt"
	h2.runner.failErr = stalledPromptErr()
	if code := h2.run("--title", "t", "--no-worktree", "--prompt", prompt); code != ExitFailed {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitFailed, h2.stderr)
	}
	stderr := h2.stderr.String()
	if strings.Contains(stderr, "was not sent") {
		t.Errorf("stderr claims the prompt was not sent after two sends:\n%s", stderr)
	}
	if !strings.Contains(stderr, "read the pane") {
		t.Errorf("stderr = %q, want it to send the caller to the pane before resending", stderr)
	}
}

// TestStalledPromptTheRetryRescues is the retry earning its keep, and the
// reason the fix is a retry rather than better wording: the measured cause
// of a stall is a TUI that reports itself ready about half a second before
// it accepts input, which one settle is past. Nothing is echoed back,
// because nothing was lost.
func TestStalledPromptTheRetryRescues(t *testing.T) {
	const prompt = "implement the fix"

	h := newHarness(t)
	h.runner.failAt = "AgentPrompt"
	h.runner.failErr = stalledPromptErr()
	h.runner.failTimes = 1

	code := h.run("--title", "t", "--no-worktree", "--prompt", prompt, "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	if n := h.runner.countCalls("AgentPrompt"); n != 2 {
		t.Errorf("AgentPrompt called %d times, want 2 -- one stall, one retry: %v", n, h.runner.calls)
	}

	var out jsonReport
	if err := json.Unmarshal([]byte(h.stdout.String()), &out); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, h.stdout)
	}
	if !out.OK {
		t.Errorf("ok = false, want true: %+v", out)
	}
	if out.PromptStatus != promptStatusSent {
		t.Errorf("prompt_status = %q, want %q", out.PromptStatus, promptStatusSent)
	}
	if strings.Contains(h.stdout.String(), prompt) || strings.Contains(h.stderr.String(), prompt) {
		t.Errorf("the prompt was echoed back after being delivered:\nstdout: %s\nstderr: %s",
			h.stdout, h.stderr)
	}
}

// TestOnFailureCleanRefusesASurvivingStall is the accepted cost of the
// posture, pinned so it cannot be traded away later: after two sends the
// pane may hold the prompt, and `clean` would remove a pane that might have
// a healthy agent working in it. The refusal is disclosed rather than
// silent, and the caller still gets the ids.
func TestOnFailureCleanRefusesASurvivingStall(t *testing.T) {
	h := newHarness(t)
	h.runner.failAt = "AgentPrompt"
	h.runner.failErr = stalledPromptErr()

	code := h.run("--title", "t", "--no-worktree", "--prompt", "implement the fix",
		"--on-failure", "clean", "--json")
	if code != ExitFailed {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitFailed, h.stderr)
	}
	for _, closer := range []string{"WorkspaceClose", "PaneClose", "TabClose"} {
		if h.runner.called(closer) {
			t.Errorf("--on-failure clean removed the session with %s: %v", closer, h.runner.calls)
		}
	}

	var out jsonReport
	if err := json.Unmarshal([]byte(h.stdout.String()), &out); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, h.stdout)
	}
	if out.Cleaned {
		t.Errorf("cleaned = true, want false")
	}
	if out.CleanRefused == "" {
		t.Error("clean_refused is empty -- a refusal the caller cannot see is a silent no-op")
	}
	// The timeout's reason names a wait that gave up; a stall timed nothing
	// out, and saying so would be a fabricated explanation.
	if strings.Contains(out.CleanRefused, "timed out") {
		t.Errorf("clean_refused = %q, want the stall's own evidence, not the timeout's", out.CleanRefused)
	}
	if out.SpacePaneID == "" && out.PaneID == "" {
		t.Errorf("no pane id in the report, but the caller was told to read the pane: %+v", out)
	}
}

// TestStallThenARefusedRetryIsStillUnconfirmed is #132's rider reaching the
// headless verb, and it is the shape the posture existed to prevent: a stall
// whose RETRY is refused by the guard used to be classified from the
// terminal error alone, so it reported `unsent` -- README's "resend it; it
// never arrived" -- and `--on-failure clean` removed the session, after herdr
// had already written the prompt text and Enter into that pane once.
//
// The timing is the measured one (CLAUDE.md, 2026-09-10): the guard's first
// read lands in the window where the pane carries the shell's echo and no
// signature, and the dialog paints during the two-second settle.
func TestStallThenARefusedRetryIsStillUnconfirmed(t *testing.T) {
	const prompt = "implement the fix"

	h := newHarness(t)
	h.runner.failAt = "AgentPrompt"
	h.runner.failErr = stalledPromptErr()
	h.runner.readTextShowsAfter = 1
	h.runner.readText = "Quick safety check: Is this a project you created or one you trust?\n" +
		"❯ No, exit\n  Yes, I trust this folder\nEnter to confirm · Esc to cancel"

	code := h.run("--title", "t", "--no-worktree", "--prompt", prompt,
		"--on-failure", "clean", "--json")
	if code != ExitFailed {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitFailed, h.stderr)
	}
	if n := h.runner.countCalls("AgentPrompt"); n != 1 {
		t.Fatalf("AgentPrompt called %d times, want 1 -- the retry's guard must refuse the dialog: %v",
			n, h.runner.calls)
	}
	for _, closer := range []string{"WorkspaceClose", "PaneClose", "TabClose"} {
		if h.runner.called(closer) {
			t.Errorf("clean removed a pane the prompt had already gone into (%s): %v",
				closer, h.runner.calls)
		}
	}

	var out jsonReport
	if err := json.Unmarshal([]byte(h.stdout.String()), &out); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, h.stdout)
	}
	if out.PromptStatus != promptStatusUnconfirmed {
		t.Errorf("prompt_status = %q, want %q -- one send has already gone out",
			out.PromptStatus, promptStatusUnconfirmed)
	}
	if out.PromptSent != nil {
		t.Errorf("prompt_sent = %v, want it absent", *out.PromptSent)
	}
	if out.UnconfirmedPrompt != prompt || out.UnsentPrompt != "" {
		t.Errorf("text came back under the wrong key: unconfirmed=%q unsent=%q",
			out.UnconfirmedPrompt, out.UnsentPrompt)
	}
	if out.Cleaned || out.CleanRefused == "" {
		t.Errorf("cleaned=%v clean_refused=%q, want a disclosed refusal", out.Cleaned, out.CleanRefused)
	}
	// The refusal must name its own evidence: one send, not two, and
	// nothing timed out.
	if strings.Contains(out.CleanRefused, "timed out") || strings.Contains(out.CleanRefused, "twice") {
		t.Errorf("clean_refused = %q, want the after-a-send evidence", out.CleanRefused)
	}
	// The guard's own refusal is the `error` field here, and its tail is
	// the claim this rider retracts.
	if strings.Contains(out.Error, "prompt not sent") {
		t.Errorf("error field still says the prompt was not sent:\n%s", out.Error)
	}
}

// TestAFirstSendTheCheckRejectsIsUnconfirmed is #154 at the headless verb:
// the send is a FIRST one, herdr accepts it, and the read straight afterwards
// finds it did not land. Before #154 nothing had marked the text typed, so
// the report said `unsent` -- which tells a script to resend -- and
// `--on-failure clean` removed the one pane whose screen showed what the
// prompt's Enter had done.
//
// Both of the post-send check's verdicts, and herdr's own report of the
// agent gone, because all three come after herdr has typed the text and
// Enter. The dialog verdict's timing is the measured
// one: the guard reads the startup window's dialog-free screen, and the
// dialog has painted by the time the check looks. The other is a pane that
// stops answering, which costs about a second in real time here --
// readAfterSend's poll interval is internal/plan's own unexported var.
func TestAFirstSendTheCheckRejectsIsUnconfirmed(t *testing.T) {
	const prompt = "implement the fix"

	for _, tc := range []struct {
		name  string
		setup func(*fakeRunner)
		// evidence is what clean_refused must name; notClaimed is what the
		// `error` field used to say and must not any more (none, when it
		// used to pass herdr's own error through); instruction is what it
		// must say instead.
		evidence, notClaimed, instruction string
	}{
		{
			name: "a dialog with no trace of the prompt",
			setup: func(r *fakeRunner) {
				r.readTextShowsAfter = 1
				r.readText = "Quick safety check: Is this a project you created or one you trust?\n" +
					"❯ No, exit\n  Yes, I trust this folder\nEnter to confirm · Esc to cancel"
			},
			// Not "dialog" alone: the after-a-send sentence says "possibly
			// typed into a dialog" too, so that would pass for the wrong
			// cause.
			evidence:    "no trace of the prompt",
			notClaimed:  "reach",
			instruction: "read the pane before pasting",
		},
		{
			name: "a pane that stopped answering",
			setup: func(r *fakeRunner) {
				r.postPromptErr = errors.New("herdr agent read wS1:pP1: exit status 1: agent_not_found")
			},
			evidence:    "stopped answering",
			notClaimed:  "can be removed",
			instruction: "read the pane before removing",
		},
		{
			// herdr's own wait seeing the agent go before the read does:
			// `agent_not_running` comes only after the dispatch succeeded.
			name: "herdr reporting the agent no longer running",
			setup: func(r *fakeRunner) {
				r.failAt = "AgentPrompt"
				r.failErr = fmt.Errorf("%w: herdr agent prompt wS1:pP1 ...: exit status 1: "+
					`{"error":{"code":"agent_not_running","message":"agent is no longer running in the target pane"},"id":"cli:agent:prompt"}`,
					herdrc.ErrPromptAgentGone)
			},
			evidence:    "stopped answering",
			instruction: "read the pane before removing",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			tc.setup(h.runner)

			code := h.run("--title", "t", "--no-worktree", "--prompt", prompt,
				"--on-failure", "clean", "--json")
			if code != ExitFailed {
				t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitFailed, h.stderr)
			}
			if n := h.runner.countCalls("AgentPrompt"); n != 1 {
				t.Fatalf("AgentPrompt called %d times, want 1 -- a first send, never resent: %v",
					n, h.runner.calls)
			}
			for _, closer := range []string{"WorkspaceClose", "PaneClose", "TabClose"} {
				if h.runner.called(closer) {
					t.Errorf("clean removed a pane herdr had typed the prompt into (%s): %v",
						closer, h.runner.calls)
				}
			}

			var out jsonReport
			if err := json.Unmarshal([]byte(h.stdout.String()), &out); err != nil {
				t.Fatalf("stdout is not JSON: %v\n%s", err, h.stdout)
			}
			if out.PromptStatus != promptStatusUnconfirmed {
				t.Errorf("prompt_status = %q, want %q -- herdr accepted the send",
					out.PromptStatus, promptStatusUnconfirmed)
			}
			if out.PromptSent != nil {
				t.Errorf("prompt_sent = %v, want it absent", *out.PromptSent)
			}
			if out.UnconfirmedPrompt != prompt || out.UnsentPrompt != "" {
				t.Errorf("text came back under the wrong key: unconfirmed=%q unsent=%q",
					out.UnconfirmedPrompt, out.UnsentPrompt)
			}
			if out.Cleaned || !strings.Contains(out.CleanRefused, tc.evidence) {
				t.Errorf("cleaned=%v clean_refused=%q, want a refusal naming %q",
					out.Cleaned, out.CleanRefused, tc.evidence)
			}
			if tc.notClaimed != "" && strings.Contains(out.Error, tc.notClaimed) {
				t.Errorf("error field still says %q:\n%s", tc.notClaimed, out.Error)
			}
			if !strings.Contains(out.Error, tc.instruction) {
				t.Errorf("error field does not say %q:\n%s", tc.instruction, out.Error)
			}
			if out.PaneID == "" {
				t.Errorf("no pane id in the report, but the caller was told to read the pane: %+v", out)
			}
		})
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

// TestReportedIDsNameTheAgentNotTheSpace is #99. The two triples separate
// in exactly one shape since placement spec §14 kept a worktree session in
// its own space: `worktree create` REUSES a workspace that was already
// open for the checkout, and Execute claims a fresh tab in it for the
// agent (placement spec §5.2) rather than typing into whatever pane herdr
// handed back. The agent then shares the space's workspace but not its
// tab or pane.
//
// reuseTheWorktreesSpace stages that: the workspace the fake's worktree
// create returns is already in `workspace list`. Nor can
// equivalence_test.go show this, since it compares plan.Inputs: this is
// downstream of the plan entirely.
//
// What a caller does with the answer is the reason it matters: the id it
// greps out of a create is the one it sends the next keystroke to. Before
// this, that was the worktree's own idle shell -- or, when the workspace
// was reused, another session's pane (#99's live evidence, #91's family).
func TestReportedIDsNameTheAgentNotTheSpace(t *testing.T) {
	h := newHarness(t)
	reuseTheWorktreesSpace(h)

	if code := h.run("--title", "t", "--worktree", "--branch", "zvi/x", "--base", "main",
		"--json"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}

	var out jsonReport
	if err := json.Unmarshal([]byte(h.stdout.String()), &out); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, h.stdout)
	}
	// The fake's own arithmetic: creation 1 is the worktree's (reused)
	// space wS1/tT1/pP1, creation 2 the tab claimed inside it (tT2/pP2).
	if out.WorkspaceID != "wS1" || out.TabID != "tT2" || out.PaneID != "pP2" {
		t.Errorf("agent ids = %q/%q/%q, want wS1/tT2/pP2 -- where the agent actually is",
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
	reuseTheWorktreesSpace(h)

	if code := h.run("--title", "t", "--worktree", "--branch", "zvi/x", "--base", "main"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	got := strings.TrimSpace(h.stdout.String())
	for _, want := range []string{"workspace=wS1", "tab=tT2", "pane=pP2"} {
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
	reuseTheWorktreesSpace(h)
	h.runner.failAt = "TabCreate" // the reuse claim, after the worktree's space exists

	if code := h.run("--title", "t", "--worktree", "--branch", "zvi/x", "--base", "main",
		"--json"); code != ExitFailed {
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
// them after the wrong container.
//
// Found by mutation: switching this line to AgentAt left every other test
// green, which is precisely how the opposite change gets made by someone
// tidying #99 up later.
func TestFailureLineNamesTheSpaceNotTheAgent(t *testing.T) {
	h := newHarness(t)
	reuseTheWorktreesSpace(h)
	h.runner.failAt = "AgentStart" // after both the space and the agent's pane exist

	if code := h.run("--title", "t", "--worktree", "--branch", "zvi/x", "--base", "main",
		"--on-failure", "keep"); code != ExitFailed {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitFailed, h.stderr)
	}
	stderr := h.stderr.String()
	if !strings.Contains(stderr, "workspace wS1, pane pP1") {
		t.Errorf("stderr = %q, want the SPACE's ids (wS1/pP1) in the kept-session line", stderr)
	}
	if strings.Contains(stderr, "pane pP2") {
		t.Errorf("stderr = %q, names the AGENT's pane -- Clean acts on the space", stderr)
	}
}

// --- the argv boundary (issue #14) ----------------------------------------

// TestBranchLeadingDash: a --branch git would read as an option is a usage
// error, refused with the other names git cannot hold (#199) before herdr
// is asked anything. It used to reach internal/herdrc's argv refusal
// (appendFlag), which still stands one layer down and is pinned there
// (TestCLIRunnerCreateCallRefusedHereChangedNothing). The real CLIRunner
// stays wired in here so that a regression in the first refusal is caught
// by the second's exit 4 instead of passing silently.
func TestBranchLeadingDash(t *testing.T) {
	h := newHarness(t)
	h.deps.Runner = &refusingRunner{
		fakeRunner: h.runner,
		real:       herdrc.CLIRunner{Bin: filepath.Join(t.TempDir(), "herdr-does-not-exist")},
	}

	code := h.run("--title", "t", "--worktree", "--branch", "--oops")
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitUsage, h.stderr)
	}
	stderr := h.stderr.String()
	if !strings.Contains(stderr, `--branch "--oops"`) || !strings.Contains(stderr, `begins with "-"`) {
		t.Errorf("stderr = %q, want it to name the flag and the reason", stderr)
	}
	if h.createdAnything() {
		t.Fatalf("a refused create must create nothing, got %v", h.runner.calls)
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

// TestTitleIsKeptToWhatTheFormKeeps is #176 and #178 as the caller sees
// them. The session is titled with what the form's title row would hold --
// tabs and line breaks made spaces, other control characters dropped, then
// the first 32 runes -- and one line on stderr says so: an explicit --title
// would otherwise come back changed with nothing anywhere saying why.
// --json reports the title that was used.
func TestTitleIsKeptToWhatTheFormKeeps(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		wantUsed string
		// wantLines is every stderr line about the title, whole, in order.
		wantLines []string
	}{
		{
			name:      "--title",
			args:      []string{"--title", "fix login redirect loop when the cookie expires"},
			wantUsed:  "fix login redirect loop when the",
			wantLines: []string{`herdr-draft create: --title is 47 runes and a title is capped at 32, as in the form; using "fix login redirect loop when the"`},
		},
		{
			name:      "--issue",
			args:      []string{"--issue", "eng-42"},
			wantUsed:  "Fix café login redirect loop whe",
			wantLines: []string{`herdr-draft create: ENG-42's title is 52 runes and a title is capped at 32, as in the form; using "Fix café login redirect loop whe"`},
		},
		{
			// --title wins over the issue's, so the line names --title:
			// blaming the issue for a title the caller typed would send
			// them to the wrong place to shorten it.
			name:      "--title beside --issue",
			args:      []string{"--title", "fix login redirect loop when the cookie expires", "--issue", "eng-42"},
			wantUsed:  "fix login redirect loop when the",
			wantLines: []string{`herdr-draft create: --title is 47 runes and a title is capped at 32, as in the form; using "fix login redirect loop when the"`},
		},
		{
			name:      "--title with a line break",
			args:      []string{"--title", "line one\nline two"},
			wantUsed:  "line one line two",
			wantLines: []string{`herdr-draft create: --title has characters the form's title row does not keep (a tab, CR or LF becomes a space, and the rest are dropped); using "line one line two"`},
		},
		{
			// One line for both changes, naming the title actually used:
			// two lines would have the first give a title that was then
			// cut.
			name:      "--issue with control characters, over the cap",
			args:      []string{"--issue", "eng-43"},
			wantUsed:  "Fix login redirect loop when the",
			wantLines: []string{`herdr-draft create: ENG-43's title has characters the form's title row does not keep (a tab, CR or LF becomes a space, and the rest are dropped), and a title is capped at 32 runes; using "Fix login redirect loop when the"`},
		},
		{
			// Nothing of this --title survives cleaning, so it is blank
			// and the issue's title applies -- as in the form, where a
			// paste of control characters leaves the row empty and a
			// picked issue still seeds it.
			name:     "--title of control characters only, beside --issue",
			args:     []string{"--title", "\a\x1b", "--issue", "eng-42"},
			wantUsed: "Fix café login redirect loop whe",
			wantLines: []string{
				`herdr-draft create: --title is blank as the form's title row would hold it, so ENG-42's title is used instead`,
				`herdr-draft create: ENG-42's title is 52 runes and a title is capped at 32, as in the form; using "Fix café login redirect loop whe"`,
			},
		},
		{
			name:     "exactly the cap is not cut",
			args:     []string{"--title", "fix login redirect loop when the"},
			wantUsed: "fix login redirect loop when the",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.deps.Linear = &fakeLinear{issues: []linear.Issue{{
				Identifier: "ENG-42", Title: "Fix café login redirect loop when the cookie expires",
				BranchName: "zvi/eng-42-fix-cafe-login-redirect-loop",
			}, {
				Identifier: "ENG-43", Title: "Fix\a login\tredirect loop when the cookie expires",
				BranchName: "zvi/eng-43-fix-login-redirect-loop",
			}}}

			if code := h.run(append(tc.args, "--no-worktree", "--json")...); code != ExitOK {
				t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
			}

			var lines []string
			for _, line := range strings.Split(h.stderr.String(), "\n") {
				if strings.HasPrefix(line, "herdr-draft create: ") && strings.Contains(line, "title") {
					lines = append(lines, line)
				}
			}
			switch {
			case len(tc.wantLines) == 0 && len(lines) > 0:
				t.Errorf("stderr says %q, want nothing about the title", lines)
			case len(tc.wantLines) > 0 && !slices.Equal(lines, tc.wantLines):
				t.Errorf("stderr lines about the title = %q, want exactly\n%q", lines, tc.wantLines)
			}

			// The space is labelled with what was used, not what was asked.
			wantCall := "WorkspaceCreate(/projects/thing," + tc.wantUsed + ")"
			if !slices.Contains(h.runner.calls, wantCall) {
				t.Errorf("calls = %v, want %s", h.runner.calls, wantCall)
			}
			var out jsonReport
			if err := json.Unmarshal([]byte(h.stdout.String()), &out); err != nil {
				t.Fatalf("stdout is not JSON: %v\n%s", err, h.stdout)
			}
			if out.Title != tc.wantUsed {
				t.Errorf("--json title = %q, want %q", out.Title, tc.wantUsed)
			}
		})
	}
}

// TestTitleWithNothingTheRowKeepsIsRefusedByName: a title that cleans to
// blank is refused, as the form refuses an empty title row, and the reason
// names the title that was given. "a title is required: pass --title"
// would tell a caller who did pass one to do it again.
func TestTitleWithNothingTheRowKeepsIsRefusedByName(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		args       []string
	}{
		{
			name: "--title",
			args: []string{"--title", "\a\x1b"},
			want: "herdr-draft create: --title has nothing the form's title row keeps; pass a --title with text in it",
		},
		{
			name: "--issue",
			args: []string{"--issue", "eng-44"},
			want: "herdr-draft create: ENG-44's title has nothing the form's title row keeps; pass a --title with text in it",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.deps.Linear = &fakeLinear{issues: []linear.Issue{{Identifier: "ENG-44", Title: "\a\t"}}}

			if code := h.run(append(tc.args, "--no-worktree")...); code != ExitUsage {
				t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitUsage, h.stderr)
			}
			if !strings.Contains(h.stderr.String(), tc.want+"\n") {
				t.Errorf("stderr = %q, want the line %q", h.stderr, tc.want)
			}
		})
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

// flagNames is every flag this command accepts, derived from the one
// place that actually decides -- parseArgs' own flag.FlagSet, via
// registerFlags.
//
// It used to be a hand-written slice here, with a doc comment recording
// that deriving it would mean splitting the FlagSet construction out of
// parseArgs "for a test's benefit; left undone deliberately". The split
// has since happened for a reason outside this package: the spawn
// skill's document is held to create's flag set from
// spawnskill_contract_test.go, and a flag the document never names is a
// flag an agent never uses. With registerFlags in hand, keeping a third
// hand-maintained copy beside it and createUsage would be the only
// remaining way for a new flag to slip past both.
func flagNames() []string {
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	var req request
	var worktreeOn, worktreeOff bool
	registerFlags(fs, &req, &worktreeOn, &worktreeOff)

	var names []string
	fs.VisitAll(func(f *flag.Flag) { names = append(names, f.Name) })
	return names
}

// TestUsageListsEveryFlag keeps the hand-written usage block honest: a
// flag added to parseArgs and forgotten here is a flag nobody can
// discover.
func TestUsageListsEveryFlag(t *testing.T) {
	names := flagNames()
	if len(names) == 0 {
		t.Fatal("registerFlags registered nothing -- this test would pass vacuously")
	}
	for _, name := range names {
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

// --- the account picker ---------------------------------------------------

// fakePicker is this package's own, declared locally rather than shared with
// internal/app's identical one: exporting a fake from a production package to
// a sibling's tests is a worse trade than eight duplicated lines.
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

// `--account auto` resolves through the picker, with --strict and WITHOUT
// --dry-run: there is nobody at the keyboard to answer an interactive
// fallback, and this is the real launch, so the ledger write must happen.
func TestCreateResolvesAutoThroughThePickerStrictly(t *testing.T) {
	h := newHarness(t)
	p := &fakePicker{res: picker.Result{Profile: "alpha-1", ConfigDir: "/dirs/alpha-1"}}
	h.deps.Picker = p

	if code := h.run("--title", "fix login", "--account", "auto", "--no-worktree"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	if len(p.calls) != 1 {
		t.Fatalf("picker calls = %d, want exactly 1", len(p.calls))
	}
	if !p.calls[0].Strict {
		t.Error("the headless verb must ask for --strict: nobody is at the keyboard")
	}
	if p.calls[0].DryRun {
		t.Error("the commit pick must NOT be a --dry-run: it is what writes the ledger")
	}
	if p.dirs[0] != "/projects/thing" {
		t.Errorf("picker dir = %q, want the project directory", p.dirs[0])
	}
	if got := strings.Join(h.runner.calls, "\n"); !strings.Contains(got, "alpha-1") {
		t.Fatalf("the picked profile should reach the launch:\n%s", got)
	}
}

// The picker's warnings go to stderr (#165): `create` has no account panel to
// draw them on, and a warning nobody sees is a warning not given.
func TestCreatePrintsThePickersWarnings(t *testing.T) {
	h := newHarness(t)
	h.deps.Picker = &fakePicker{res: picker.Result{
		Profile: "alpha-1", Warnings: []string{"alpha-1 resets in 12m"},
	}}

	if code := h.run("--title", "fix login", "--account", "auto", "--no-worktree"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	if !strings.Contains(h.stderr.String(), "alpha-1 resets in 12m") {
		t.Fatalf("stderr should carry the picker's warning:\n%s", h.stderr)
	}
}

// ... one prefixed line per warning, however the picker wrapped it.
func TestCreatePrintsAMultiLineWarningOnOneLine(t *testing.T) {
	h := newHarness(t)
	h.deps.Picker = &fakePicker{res: picker.Result{Profile: "alpha-1", Warnings: []string{"first half\n  second half"}}}

	if code := h.run("--title", "fix login", "--account", "auto", "--no-worktree"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	if !strings.Contains(h.stderr.String(), "herdr-draft create: account picker: first half second half\n") {
		t.Fatalf("stderr should carry the warning on one prefixed line:\n%s", h.stderr)
	}
}

// A refusal fails the request BEFORE any worktree or pane exists, with the
// picker's own reason on stderr, and exits 2 -- this verb's existing contract
// for a request that cannot be satisfied.
func TestCreateRefusesBeforeCreatingAnythingWhenThePickerRefuses(t *testing.T) {
	h := newHarness(t)
	h.deps.Picker = &fakePicker{
		err: &picker.RefusalError{Code: picker.ExitExhausted, Reason: "pool exhausted (resets 22:49)"},
	}

	code := h.run("--title", "fix login", "--account", "auto", "--no-worktree")
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(h.stderr.String(), "pool exhausted (resets 22:49)") {
		t.Fatalf("stderr should carry the picker's own reason:\n%s", h.stderr)
	}
	// registerPane is the harness's own pre-existing pane, not a created
	// one; nothing the plan would have run may have happened.
	for _, call := range h.runner.calls {
		if strings.HasPrefix(call, "WorktreeCreate") || strings.HasPrefix(call, "WorkspaceCreate") ||
			strings.HasPrefix(call, "PaneRun") || strings.HasPrefix(call, "AgentStart") {
			t.Fatalf("a picker refusal must create nothing, got %v", h.runner.calls)
		}
	}
}

// ... and it is a usage error even with herdr down. It is a fault in the
// command or the config, which needs no pick to find, so it must not wait for
// the reachability probe: exit 3 tells an agent "nothing was created; stop",
// when what it needs to hear is "fix the command".
func TestCreateRejectsAutoWithNoPickerBeforeTheReachabilityProbe(t *testing.T) {
	h := newHarness(t)
	h.runner.listErr = errors.New("connection refused")

	if code := h.run("--title", "fix login", "--account", "auto", "--no-worktree"); code != ExitUsage {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitUsage, h.stderr)
	}
}

// `--account auto` with no picker configured is a usage error, not a silent
// fallback to the active account: the user named a resolution strategy this
// install does not have.
func TestCreateRejectsAutoWithNoPicker(t *testing.T) {
	h := newHarness(t)

	if code := h.run("--title", "fix login", "--account", "auto", "--no-worktree"); code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(h.stderr.String(), "[clauth] picker") {
		t.Fatalf("stderr should name the config key that is missing:\n%s", h.stderr)
	}
}

// A named profile never consults the picker: the user has already answered
// the question it exists to answer.
func TestCreateNamedAccountDoesNotConsultThePicker(t *testing.T) {
	h := newHarness(t)
	p := &fakePicker{res: picker.Result{Profile: "alpha-1"}}
	h.deps.Picker = p

	if code := h.run("--title", "fix login", "--account", "alpha-2", "--no-worktree"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	if len(p.calls) != 0 {
		t.Fatalf("a named account must not consult the picker, got %d calls", len(p.calls))
	}
}

// A launcher that fell back has to SAY so on stderr. The fallback itself is
// safe -- the default pins the account correctly -- but silence would leave
// the user believing their own template ran, and the whole reason the
// template is validated is that one with no {account} launches on whichever
// profile clauth has live. `create` has no panel to put a reason in, so
// this line is the only place it can appear.
func TestCreate_ReportsARejectedClauthLauncher(t *testing.T) {
	h := newHarness(t)
	body := "[clauth]\nlauncher = [\"claude-as\"]\n"
	if err := os.WriteFile(filepath.Join(h.env.ConfigDir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}

	if code := h.run("--title", "fix login redirect", "--no-worktree"); code != ExitOK {
		t.Fatalf("exit = %d, want %d -- a bad launcher must not fail the create\nstderr: %s",
			code, ExitOK, h.stderr)
	}
	got := h.stderr.String()
	for _, want := range []string{"launcher", "{account}", "clauth"} {
		if !strings.Contains(got, want) {
			t.Errorf("stderr does not mention %q; got:\n%s", want, got)
		}
	}
}

// The mirror image, and the one that keeps the line from becoming noise: a
// config that names no launcher at all is the overwhelmingly common case
// and must produce no warning.
func TestCreate_SaysNothingAboutAnAbsentClauthLauncher(t *testing.T) {
	h := newHarness(t)
	if code := h.run("--title", "fix login redirect", "--no-worktree"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	if strings.Contains(h.stderr.String(), "launcher") {
		t.Errorf("warned about a launcher nobody configured:\n%s", h.stderr)
	}
}

// The other two ways the launch decision can be overridden, on the same
// stderr and for the same reason as the rejected-launcher line above.
//
// `[clauth] launch` is the one that was NOWHERE: #122 set ClauthLaunchWarning,
// unit tested it in internal/config, and surfaced it on no screen at all, so
// an unrecognised launch mode silently became `start`. An exported field
// written and never read is invisible to staticcheck, which is why a green
// `just check` said nothing about it for the length of that PR.
func TestCreate_ReportsAnUnknownClauthLaunchMode(t *testing.T) {
	h := newHarness(t)
	body := "[clauth]\nlaunch = \"teleport\"\n"
	if err := os.WriteFile(filepath.Join(h.env.ConfigDir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}

	if code := h.run("--title", "fix login redirect", "--no-worktree"); code != ExitOK {
		t.Fatalf("exit = %d, want %d -- an unknown launch mode must not fail the create\nstderr: %s",
			code, ExitOK, h.stderr)
	}
	got := h.stderr.String()
	for _, want := range []string{"launch", "teleport", "start"} {
		if !strings.Contains(got, want) {
			t.Errorf("stderr does not mention %q; got:\n%s", want, got)
		}
	}
}

// And the seam between the two keys: wrapper mode takes no argv template, so
// a launcher set beside it is reported rather than silently discarded. This is
// the line that keeps "two keys, one decision" from being two keys where one
// quietly wins.
func TestCreate_ReportsALauncherIgnoredByWrapperMode(t *testing.T) {
	h := newHarness(t)
	body := "[clauth]\nlaunch = \"wrapper\"\nlauncher = [\"claude-as\", \"{account}\"]\n"
	if err := os.WriteFile(filepath.Join(h.env.ConfigDir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}

	if code := h.run("--title", "fix login redirect", "--no-worktree"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	got := h.stderr.String()
	for _, want := range []string{"launcher", "claude-as", "wrapper"} {
		if !strings.Contains(got, want) {
			t.Errorf("stderr does not mention %q; got:\n%s", want, got)
		}
	}
}

// Both facts at once (#123). A malformed launcher beside wrapper mode used to
// report only the malformedness, which told the user to fix a template that
// wrapper mode would never have used -- and not that it would not.
func TestCreate_ReportsAMalformedLauncherUnderWrapperModeAsIgnoredToo(t *testing.T) {
	h := newHarness(t)
	body := "[clauth]\nlaunch = \"wrapper\"\nlauncher = [\"claude-as\"]\n"
	if err := os.WriteFile(filepath.Join(h.env.ConfigDir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}

	if code := h.run("--title", "fix login redirect", "--no-worktree"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	got := h.stderr.String()
	for _, want := range []string{"it contains {account} 0 times", "isolated credential directory"} {
		if !strings.Contains(got, want) {
			t.Errorf("stderr does not mention %q; got:\n%s", want, got)
		}
	}
}

// A repository's own branch_prefix outranks config.toml's, so a refused
// config.toml prefix is not the one such a create falls back to, and a line
// ending `using "<fallback>"` would contradict the branch it derives (review
// of #125). Both directions: silent where the repository supplies a prefix,
// reported where it does not -- the same rule the popup's worktree panel
// follows, through the same app.BranchPrefixWarning.
func TestCreate_ReportsARefusedBranchPrefixOnlyWhereItsFallbackApplies(t *testing.T) {
	for _, tc := range []struct {
		name       string
		repoPrefix string
		wantLine   bool
	}{
		{"the repository supplies a prefix", "team/", false},
		{"the repository supplies none", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			writeConfig(t, h.env.ConfigDir, "branch_prefix = \"a b/\"\n")
			h.deps.RepoConfig = func(string) config.RepoConfig { return config.RepoConfig{BranchPrefix: tc.repoPrefix} }

			if code := h.run("--title", "fix login redirect", "--no-worktree"); code != ExitOK {
				t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
			}
			if got := strings.Contains(h.stderr.String(), `ignoring branch_prefix "a b/"`); got != tc.wantLine {
				t.Errorf("stderr reports the refused prefix = %v, want %v\nstderr: %s", got, tc.wantLine, h.stderr)
			}
		})
	}
}

// The mirror image for both, so neither line becomes noise: the common config
// names no launch mode and no launcher, and must produce neither warning.
func TestCreate_SaysNothingAboutAnAbsentLaunchMode(t *testing.T) {
	h := newHarness(t)
	if code := h.run("--title", "fix login redirect", "--no-worktree"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	if got := h.stderr.String(); strings.Contains(got, "launch ") || strings.Contains(got, "ignoring") {
		t.Errorf("warned about a launch mode nobody configured:\n%s", got)
	}
}

// --- the form's submit-time refusals (#145, #146, #147) --------------------

// fakeClauth implements ClauthSource.
type fakeClauth struct {
	status clauth.Status
	err    error
}

func (c *fakeClauth) Status(context.Context) (clauth.Status, error) {
	return c.status, c.err
}

// reuseTheWorktreesSpace makes the fake's first `worktree create` answer
// with a workspace `workspace list` already reported -- herdr's reuse
// branch -- so Execute claims a tab in it for the agent (placement spec
// §5.2). Since §14 that is the only way the agent's ids and the space's
// can differ, which is what the #99 tests need.
func reuseTheWorktreesSpace(h *harness) {
	h.runner.workspaces = []herdrc.WorkspaceInfo{{WorkspaceID: "wS1", Label: "an older session"}}
}

// createdAnything reports whether any call that makes a session -- a
// worktree, a workspace, a tab, a split, or a launch -- reached the runner.
func (h *harness) createdAnything() bool {
	for _, name := range []string{"WorktreeCreate", "WorkspaceCreate", "TabCreate", "PaneSplit", "PaneRun", "AgentStart"} {
		if h.runner.called(name) {
			return true
		}
	}
	return false
}

// #147: herdr v0.9.0 does not refuse a worktree on a branch that already
// exists -- it checks that branch out and ignores the base
// (https://github.com/herdrdev/herdr/blob/v0.9.0/src/worktree.rs#L315) -- so
// a create that let this through would start the agent on someone's old
// work. The form refuses it; so must the command.
func TestCreateRefusesAWorktreeOnAnExistingBranch(t *testing.T) {
	h := newHarness(t)
	h.git.branches = map[string]bool{"feature/login": true}

	code := h.run("--title", "fix login", "--branch", "feature/login", "--worktree")
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitUsage, h.stderr)
	}
	if !strings.Contains(h.stderr.String(), `"feature/login"`) {
		t.Errorf("stderr should name the branch:\n%s", h.stderr)
	}
	if h.createdAnything() {
		t.Fatalf("a refused create must create nothing, got %v", h.runner.calls)
	}
}

// The branch only matters when a worktree would create it: without one no
// branch is made at all, which is the same condition the form's check has.
func TestCreateIgnoresAnExistingBranchWithoutAWorktree(t *testing.T) {
	h := newHarness(t)
	h.git.branches = map[string]bool{"feature/login": true}

	if code := h.run("--title", "fix login", "--branch", "feature/login", "--no-worktree"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
}

// #199: a branch git cannot hold is refused before anything is made, naming
// the flag and what is wrong with it. The duplicate refusal above cannot see
// these names: `git show-ref --verify` answers "absent" for a name git could
// never hold, so "zvi/old " went through it -- and herdr trims it into
// zvi/old, the user's existing branch, and checks that out over the base.
//
// It is decided before the duplicate check asks git about the name, which
// would only answer "absent".
func TestCreateRefusesABranchGitCannotHold(t *testing.T) {
	for _, tc := range []struct{ branch, reason string }{
		// The issue's own table.
		{"zvi/old ", "ends with a space"},
		{" zvi/old", "begins with a space"},
		{"zvi/a..b", `contains ".."`},
		{"zvi/a~1", `contains '~'`},
		{"zvi/a:b", `contains ':'`},
		{"zvi/a b", "contains a space"},
		{"zvi/old.lock", `ends with ".lock"`},
		// git would take this one; herdr's trim would not leave it alone.
		{"zvi/old" + string(rune(0xa0)), "ends with whitespace (U+00A0)"},
	} {
		t.Run(tc.branch, func(t *testing.T) {
			h := newHarness(t)
			h.git.branches = map[string]bool{"zvi/old": true}

			code := h.run("--title", "fix login", "--branch", tc.branch, "--worktree")
			if code != ExitUsage {
				t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitUsage, h.stderr)
			}
			stderr := h.stderr.String()
			if want := "--branch " + strconv.Quote(tc.branch); !strings.Contains(stderr, want) {
				t.Errorf("stderr should name the flag and its value as %s:\n%s", want, stderr)
			}
			if !strings.Contains(stderr, tc.reason) {
				t.Errorf("stderr should say what is wrong (%s):\n%s", tc.reason, stderr)
			}
			if h.createdAnything() {
				t.Fatalf("a refused create must create nothing, got %v", h.runner.calls)
			}
			if len(h.git.branchAsks) > 0 {
				t.Errorf("BranchExists was asked about %q: validity is decided before the duplicate check", h.git.branchAsks)
			}
		})
	}
}

// And without herdr: a request wrong on its face is a usage error, not
// "herdr unreachable", as every other fault in the request itself is.
func TestCreateRefusesABranchGitCannotHoldWithHerdrDown(t *testing.T) {
	h := newHarness(t)
	h.runner.listErr = errors.New("connection refused")

	if code := h.run("--title", "fix login", "--branch", "zvi/old ", "--worktree"); code != ExitUsage {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitUsage, h.stderr)
	}
}

// Outside a repository no worktree can be made, so its branch is not what is
// wrong: the refusal is plan.Build's, which names the directory. Refusing the
// branch first sent the caller to fix a name and then refused them again for
// the directory (#199's review). It is also the form's condition, whose
// worktree row is inert outside a repository.
func TestCreateOutsideARepositoryRefusesTheWorktreeNotTheBranch(t *testing.T) {
	h := newHarness(t)
	h.git.isRepo = false

	code := h.run("--title", "fix login", "--branch", "zvi/a b", "--worktree")
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitUsage, h.stderr)
	}
	stderr := h.stderr.String()
	if !strings.Contains(stderr, "requires a git repository") || strings.Contains(stderr, "cannot be used as a branch name") {
		t.Errorf("stderr should refuse the worktree for want of a repository, not the branch:\n%s", stderr)
	}
}

// A branch named by the Linear issue is held to the same rule, and the
// refusal says where the name came from: the caller never typed it.
func TestCreateRefusesAnIssueBranchGitCannotHold(t *testing.T) {
	h := newHarness(t)
	h.deps.Linear = &fakeLinear{issues: []linear.Issue{{
		Identifier: "LIN-42", Title: "Fix login redirect loop", BranchName: "zvi/old ",
	}}}

	code := h.run("--issue", "lin-42", "--worktree")
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitUsage, h.stderr)
	}
	stderr := h.stderr.String()
	for _, want := range []string{"LIN-42", `"zvi/old "`, "ends with a space", "--branch"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr should mention %s:\n%s", want, stderr)
		}
	}
	if h.createdAnything() {
		t.Fatalf("a refused create must create nothing, got %v", h.runner.calls)
	}
}

// Without a worktree no branch is made, so there is nothing to refuse: the
// condition the duplicate check has, and the form's (#199).
func TestCreateIgnoresAnInvalidBranchWithoutAWorktree(t *testing.T) {
	h := newHarness(t)

	if code := h.run("--title", "fix login", "--branch", "zvi/a b", "--no-worktree"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
}

// NOT DECIDED (#199): an empty --branch is not refused. It is left out of
// herdr's argv, and herdr names the branch itself
// (worktree/<adjective>-<noun>-NNNN). Whether it should be refused instead
// was left to the owner; this pins today's behaviour so a change to it is a
// decision rather than a side effect.
func TestCreateEmptyBranchStillLetsHerdrNameIt(t *testing.T) {
	h := newHarness(t)

	if code := h.run("--title", "fix login", "--branch", "", "--worktree"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	if want := "WorktreeCreate(/projects/thing,,"; !strings.Contains(strings.Join(h.runner.calls, " "), want) {
		t.Errorf("calls = %v, want a worktree create naming no branch (%s...)", h.runner.calls, want)
	}
}

// #147's other half of the duplicate check: an open workspace already
// labelled with the title.
func TestCreateRefusesATitleAnOpenWorkspaceAlreadyCarries(t *testing.T) {
	h := newHarness(t)
	h.runner.workspaces = []herdrc.WorkspaceInfo{{WorkspaceID: "wOld", Label: "fix login"}}

	code := h.run("--title", "fix login", "--no-worktree", "--placement", "new-space")
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitUsage, h.stderr)
	}
	if !strings.Contains(h.stderr.String(), "wOld") {
		t.Errorf("stderr should name the workspace holding the label:\n%s", h.stderr)
	}
	if h.createdAnything() {
		t.Fatalf("a refused create must create nothing, got %v", h.runner.calls)
	}
}

// #147: a pinned profile clauth reports as not signed in is refused, as the
// form refuses it, rather than typed into a pane where it fails.
func TestCreateRefusesAProfileClauthReportsSignedOut(t *testing.T) {
	h := newHarness(t)
	h.deps.Clauth = &fakeClauth{status: clauth.Status{Schema: 1, Profiles: []clauth.Profile{
		{Name: "alpha-1", AuthStatus: "ok"},
		{Name: "alpha-2", AuthStatus: "expired"},
	}}}

	code := h.run("--title", "fix login", "--account", "alpha-2", "--no-worktree")
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitUsage, h.stderr)
	}
	for _, want := range []string{"alpha-2", "expired"} {
		if !strings.Contains(h.stderr.String(), want) {
			t.Errorf("stderr should mention %q:\n%s", want, h.stderr)
		}
	}
	if h.createdAnything() {
		t.Fatalf("a refused create must create nothing, got %v", h.runner.calls)
	}
}

// The mirror images, which keep the check from becoming a new way to fail:
// a signed-in profile passes, and so does one clauth could not report on --
// the form treats an unknown status as non-blocking, and a create that
// refused whenever clauth hiccupped would be stricter than the popup.
func TestCreateLaunchesAProfileClauthReportsSignedIn(t *testing.T) {
	h := newHarness(t)
	h.deps.Clauth = &fakeClauth{status: clauth.Status{Schema: 1, Profiles: []clauth.Profile{
		{Name: "alpha-2", AuthStatus: "ok"},
	}}}

	if code := h.run("--title", "fix login", "--account", "alpha-2", "--no-worktree"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
}

func TestCreateLaunchesWhenClauthCannotReport(t *testing.T) {
	h := newHarness(t)
	h.deps.Clauth = &fakeClauth{err: errors.New("clauth status --json: exit status 1")}

	if code := h.run("--title", "fix login", "--account", "alpha-2", "--no-worktree"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	if !strings.Contains(h.stderr.String(), "exit status 1") {
		t.Errorf("an unchecked auth status should be said on stderr, not skipped silently:\n%s", h.stderr)
	}
}

// #146: `active` is the "no pin" sentinel on the command line too, not a
// profile called "active".
func TestCreateAccountActiveIsNoPin(t *testing.T) {
	h := newHarness(t)

	if code := h.run("--title", "fix login", "--account", "active", "--no-worktree"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	if h.runner.called("PaneRun") {
		t.Fatalf("--account active must not launch through clauth, got %v", h.runner.calls)
	}
	if !h.runner.called("AgentStart") {
		t.Fatalf("--account active should start the agent unpinned, got %v", h.runner.calls)
	}
}

// ... including for an agent that cannot be pinned: no pin is not a pin, so
// there is nothing for plan.Build to refuse.
func TestCreateAccountActiveWithANonClaudeAgent(t *testing.T) {
	h := newHarness(t)

	if code := h.run("--title", "fix login", "--account", "active", "--agent", "codex", "--no-worktree"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
}

// #145: the commit pick writes the picker's ledger, so every refusal that
// can be known without it has to come first. One test per refusal class,
// because a pick that moved past one of them and not another is exactly
// the partial fix this is here to catch.
func TestCreateAutoPicksNothingForARequestItThenRefuses(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(h *harness)
		args  []string
		want  int
	}{
		{
			name: "plan.Build refuses the pin",
			args: []string{"--title", "fix login", "--account", "auto", "--agent", "codex", "--no-worktree"},
			want: ExitUsage,
		},
		{
			name:  "herdr is unreachable",
			setup: func(h *harness) { h.runner.listErr = errors.New("connection refused") },
			args:  []string{"--title", "fix login", "--account", "auto", "--no-worktree"},
			want:  ExitUnreachable,
		},
		{
			name:  "the branch already exists",
			setup: func(h *harness) { h.git.branches = map[string]bool{"feature/login": true} },
			args:  []string{"--title", "fix login", "--account", "auto", "--branch", "feature/login", "--worktree"},
			want:  ExitUsage,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			p := &fakePicker{res: picker.Result{Profile: "alpha-1", ConfigDir: "/dirs/alpha-1"}}
			h.deps.Picker = p
			if tc.setup != nil {
				tc.setup(h)
			}

			if code := h.run(tc.args...); code != tc.want {
				t.Fatalf("exit = %d, want %d\nstderr: %s", code, tc.want, h.stderr)
			}
			if len(p.calls) != 0 {
				t.Fatalf("the picker was called %d time(s) for a create that was then refused", len(p.calls))
			}
		})
	}
}

// TestParseArgs_ReapIsATriState is reap spec §7.1: the --worktree pair's
// shape, including the inversion of --no-reap=false.
func TestParseArgs_ReapIsATriState(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want *bool
	}{
		{"neither", []string{"--title", "t"}, nil},
		{"--reap", []string{"--title", "t", "--reap"}, boolp(true)},
		{"--no-reap", []string{"--title", "t", "--no-reap"}, boolp(false)},
		{"--reap=false", []string{"--title", "t", "--reap=false"}, boolp(false)},
		{"--no-reap=false", []string{"--title", "t", "--no-reap=false"}, boolp(true)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := parseArgs(tc.args)
			if err != nil {
				t.Fatalf("parseArgs: %v", err)
			}
			switch {
			case tc.want == nil && req.reap != nil:
				t.Errorf("reap = %v, want unset", *req.reap)
			case tc.want != nil && (req.reap == nil || *req.reap != *tc.want):
				t.Errorf("reap = %v, want %v", req.reap, *tc.want)
			}
		})
	}
}

// TestReap_AppendsTheInstructionAndReportsIt: the sent text carries the
// sentence, and --json says the pane will mark itself, attributed to the flag.
func TestReap_AppendsTheInstructionAndReportsIt(t *testing.T) {
	h := newHarness(t)
	code := h.run("--title", "t", "--no-worktree", "--prompt", "look at the login redirect loop", "--reap", "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	if calls := strings.Join(h.runner.calls, "\n"); !strings.Contains(calls, "look at the login redirect loop\n\n"+plan.ReapInstruction) {
		t.Errorf("calls = %v, want the prompt followed by the reap instruction", h.runner.calls)
	}
	var out jsonReport
	if err := json.Unmarshal([]byte(h.stdout.String()), &out); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, h.stdout)
	}
	if !out.MarkReady {
		t.Error("mark_ready = false, want true: the instruction was appended")
	}
	if got := out.Provenance["mark_ready"]; got != provenanceFlag {
		t.Errorf("provenance.mark_ready = %q, want %q", got, provenanceFlag)
	}
}

// TestReap_AFlagThatCannotApplySaysSo is reap spec §7.3: not refused, since
// the form does not refuse it, but not silent either.
func TestReap_AFlagThatCannotApplySaysSo(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"no prompt", []string{"--title", "t", "--no-worktree", "--reap"}, "there is no prompt"},
		{"slash command", []string{"--title", "t", "--no-worktree", "--reap", "--prompt", "/loop 5m"}, "slash command"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			if code := h.run(tc.args...); code != ExitOK {
				t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
			}
			if !strings.Contains(h.stderr.String(), "--reap has no effect: ") || !strings.Contains(h.stderr.String(), tc.want) {
				t.Errorf("stderr = %q, want a --reap has no effect note naming %q", h.stderr, tc.want)
			}
		})
	}
}

// TestReap_ConfigDefaultThatCannotApplyIsSilent: nobody asked on this command
// line, so its not applying is not news (reap spec §7.3). It also pins the
// provenance side of the same case: with neither `--reap` nor `--no-reap`
// given, `mark_ready` is attributed to config.toml (the tier this test's
// own config sets it from), never to `flag` (the other tests here only pin
// the flag case).
func TestReap_ConfigDefaultThatCannotApplyIsSilent(t *testing.T) {
	h := newHarness(t)
	writeConfig(t, h.env.ConfigDir, "[reaper]\nmark_ready = true\n")
	if code := h.run("--title", "t", "--no-worktree", "--json"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	if strings.Contains(h.stderr.String(), "--reap") {
		t.Errorf("stderr = %q, want no --reap note for a config default", h.stderr)
	}
	var out jsonReport
	if err := json.Unmarshal([]byte(h.stdout.String()), &out); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, h.stdout)
	}
	if got := out.Provenance["mark_ready"]; got != "config.toml" {
		t.Errorf("provenance.mark_ready = %q, want config.toml (no flag was given)", got)
	}
}
