package plan

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
)

// mockRunner implements herdrc.Runner for exec_test.go: every method
// appends its name and args to calls, and fails with failErr the first
// failCount times it is the method named failAt.
type mockRunner struct {
	calls     []string
	failAt    string
	failErr   error
	failCount int

	failedSoFar int
	topo        herdrc.CreatedTopology

	// readText is what AgentRead returns on success (default "": no
	// dialog present, matching the pre-existing test scenarios' implicit
	// assumption that the pane is a normal ready state).
	readText string
}

var _ herdrc.Runner = (*mockRunner)(nil)

func (m *mockRunner) record(name string, args ...string) {
	m.calls = append(m.calls, name+"("+strings.Join(args, ",")+")")
}

// shouldFail reports whether this call (for method name) should fail,
// consuming one of failCount allotted failures when it does.
func (m *mockRunner) shouldFail(name string) bool {
	if name != m.failAt || m.failedSoFar >= m.failCount {
		return false
	}
	m.failedSoFar++
	return true
}

func (m *mockRunner) WorkspaceList(ctx context.Context) ([]herdrc.WorkspaceInfo, error) {
	m.record("WorkspaceList")
	if m.shouldFail("WorkspaceList") {
		return nil, m.failErr
	}
	return nil, nil
}

func (m *mockRunner) WorktreeCreate(ctx context.Context, req herdrc.WorktreeCreateReq) (herdrc.CreatedTopology, error) {
	m.record("WorktreeCreate", req.Cwd, req.Branch, req.Base)
	if m.shouldFail("WorktreeCreate") {
		return herdrc.CreatedTopology{}, m.failErr
	}
	return m.topo, nil
}

func (m *mockRunner) WorkspaceCreate(ctx context.Context, req herdrc.WorkspaceCreateReq) (herdrc.CreatedTopology, error) {
	m.record("WorkspaceCreate", req.Cwd, req.Label)
	if m.shouldFail("WorkspaceCreate") {
		return herdrc.CreatedTopology{}, m.failErr
	}
	return m.topo, nil
}

func (m *mockRunner) TabCreate(ctx context.Context, req herdrc.TabCreateReq) (herdrc.CreatedTopology, error) {
	m.record("TabCreate", req.Workspace, req.Cwd)
	if m.shouldFail("TabCreate") {
		return herdrc.CreatedTopology{}, m.failErr
	}
	return m.topo, nil
}

func (m *mockRunner) PaneSplit(ctx context.Context, req herdrc.PaneSplitReq) (herdrc.CreatedTopology, error) {
	m.record("PaneSplit", req.PaneID, req.Direction)
	if m.shouldFail("PaneSplit") {
		return herdrc.CreatedTopology{}, m.failErr
	}
	return m.topo, nil
}

func (m *mockRunner) AgentStart(ctx context.Context, req herdrc.AgentStartReq) error {
	m.record("AgentStart", req.Name, req.PaneID)
	if m.shouldFail("AgentStart") {
		return m.failErr
	}
	return nil
}

func (m *mockRunner) AgentPrompt(ctx context.Context, req herdrc.AgentPromptReq) error {
	m.record("AgentPrompt", req.Target, req.Text)
	if m.shouldFail("AgentPrompt") {
		return m.failErr
	}
	return nil
}

func (m *mockRunner) AgentRead(ctx context.Context, target string) (string, error) {
	m.record("AgentRead", target)
	if m.shouldFail("AgentRead") {
		return "", m.failErr
	}
	return m.readText, nil
}

func (m *mockRunner) AwaitDetection(ctx context.Context, paneID string, timeout time.Duration) error {
	m.record("AwaitDetection", paneID, timeout.String())
	if m.shouldFail("AwaitDetection") {
		return m.failErr
	}
	return nil
}

func (m *mockRunner) PaneRun(ctx context.Context, paneID string, argv []string) error {
	m.record("PaneRun", append([]string{paneID}, argv...)...)
	if m.shouldFail("PaneRun") {
		return m.failErr
	}
	return nil
}

func (m *mockRunner) WorktreeRemove(ctx context.Context, workspaceID string) error {
	m.record("WorktreeRemove", workspaceID)
	if m.shouldFail("WorktreeRemove") {
		return m.failErr
	}
	return nil
}

func (m *mockRunner) WorkspaceClose(ctx context.Context, workspaceID string) error {
	m.record("WorkspaceClose", workspaceID)
	if m.shouldFail("WorkspaceClose") {
		return m.failErr
	}
	return nil
}

// withBusyRetryOverrides replaces busyRetryInterval/busyRetryNow for the
// life of the calling test (restored via t.Cleanup), so Execute's busy
// retry loop runs instantly instead of waiting out real sleeps. now == nil
// leaves busyRetryNow at its current value (real time.Now is fine when a
// test's interval is already 0 -- see time.Sleep's documented "zero or
// negative duration returns immediately").
func withBusyRetryOverrides(t *testing.T, interval time.Duration, now func() time.Time) {
	t.Helper()
	origInterval, origNow := busyRetryInterval, busyRetryNow
	busyRetryInterval = interval
	if now != nil {
		busyRetryNow = now
	}
	t.Cleanup(func() {
		busyRetryInterval = origInterval
		busyRetryNow = origNow
	})
}

// fakeAdvancingClock returns a clock func that starts at a fixed instant
// and advances by step on every call, so a test can exercise Execute's
// busyRetryBudget cutoff without depending on wall-clock speed.
func fakeAdvancingClock(step time.Duration) func() time.Time {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	n := 0
	return func() time.Time {
		t := base.Add(time.Duration(n) * step)
		n++
		return t
	}
}

func countCallsWithPrefix(calls []string, prefix string) int {
	n := 0
	for _, c := range calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// mkRepo creates a temp git repo with one commit on "main" (mirroring
// internal/gitx/repo_test.go's helper of the same name, Task 4) for
// CleanCheck's worktree tests, which need a real gitx.Disposable check.
func mkRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	run("add", ".")
	run("commit", "-qm", "init")
	return dir
}

func TestExecuteHappyPathThreadsPaneID(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	in.Prompt = "start work"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		topo: herdrc.CreatedTopology{
			WorkspaceID:  "ws-1",
			TabID:        "tab-1",
			PaneID:       "pane-1",
			CheckoutPath: "/tmp/checkout",
		},
	}

	result := Execute(context.Background(), m, ops, nil)

	if result.FailedIndex != -1 {
		t.Fatalf("FailedIndex = %d, want -1", result.FailedIndex)
	}
	if result.Created == nil || *result.Created != m.topo {
		t.Fatalf("Created = %+v, want %+v", result.Created, m.topo)
	}

	wantCalls := []string{
		"WorktreeCreate(/repo,zvi/fix-pagination,main)",
		"AgentStart(" + AgentName(in.Title) + ",pane-1)",
		"AgentRead(pane-1)",
		"AgentPrompt(pane-1,start work)",
	}
	if !reflect.DeepEqual(m.calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", m.calls, wantCalls)
	}
}

func TestExecuteFailureAtAgentStart(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		failAt:    "AgentStart",
		failErr:   errors.New("boom"),
		failCount: 1,
		topo:      herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
	}

	var progressed []Progress
	result := Execute(context.Background(), m, ops, func(p Progress) { progressed = append(progressed, p) })

	if result.FailedIndex != 1 {
		t.Fatalf("FailedIndex = %d, want 1", result.FailedIndex)
	}
	if result.Created == nil {
		t.Fatal("Created is nil, want non-nil: the topology op succeeded before AgentStart failed")
	}

	wantCalls := []string{
		"WorktreeCreate(/repo,zvi/fix-pagination,main)",
		"AgentStart(" + AgentName(in.Title) + ",pane-1)",
	}
	if !reflect.DeepEqual(m.calls, wantCalls) {
		t.Fatalf("calls = %v, want %v (no further calls after the failure)", m.calls, wantCalls)
	}

	if len(progressed) == 0 {
		t.Fatal("no progress reported")
	}
	last := progressed[len(progressed)-1]
	if last.State != StepFailed || last.Index != 1 {
		t.Fatalf("last progress = %+v, want State=StepFailed Index=1", last)
	}
	if !errors.Is(last.Err, m.failErr) {
		t.Fatalf("last progress Err = %v, want it to wrap %v", last.Err, m.failErr)
	}
}

func TestExecuteAgentStartBusyRetrySucceeds(t *testing.T) {
	withBusyRetryOverrides(t, 0, nil)

	in := validInput()
	in.UseWorktree = true
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		failAt:    "AgentStart",
		failErr:   errors.New("agent_pane_busy: pane still starting"),
		failCount: 2,
		topo:      herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
	}

	result := Execute(context.Background(), m, ops, nil)

	if result.FailedIndex != -1 {
		t.Fatalf("FailedIndex = %d, want -1 (should succeed after retries)", result.FailedIndex)
	}
	if n := countCallsWithPrefix(m.calls, "AgentStart("); n != 3 {
		t.Fatalf("AgentStart calls = %d, want 3 (2 failures + 1 success), calls: %v", n, m.calls)
	}
}

func TestExecutePersistentNonBusyErrorNoRetry(t *testing.T) {
	withBusyRetryOverrides(t, 0, nil)

	in := validInput()
	in.UseWorktree = true
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		failAt:    "AgentStart",
		failErr:   errors.New("some persistent non-busy failure"),
		failCount: 1000,
		topo:      herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
	}

	result := Execute(context.Background(), m, ops, nil)

	if result.FailedIndex != 1 {
		t.Fatalf("FailedIndex = %d, want 1 (immediate failure)", result.FailedIndex)
	}
	if n := countCallsWithPrefix(m.calls, "AgentStart("); n != 1 {
		t.Fatalf("AgentStart calls = %d, want 1 (no retry for a non-busy error)", n)
	}
}

func TestExecuteBusyRetryExhaustsBudget(t *testing.T) {
	// A clock that jumps 2s on every read blows through the 5s budget
	// after a handful of attempts, with interval=0 so no real sleep is
	// ever waited out, proving the retry loop is bounded rather than
	// depending on the busy error eventually clearing on its own.
	withBusyRetryOverrides(t, 0, fakeAdvancingClock(2*time.Second))

	in := validInput()
	in.UseWorktree = true
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		failAt:    "AgentStart",
		failErr:   errors.New("agent_pane_busy: pane still starting"),
		failCount: 1000, // never succeeds
		topo:      herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
	}

	result := Execute(context.Background(), m, ops, nil)

	if result.FailedIndex != 1 {
		t.Fatalf("FailedIndex = %d, want 1 (budget exhausted, still fails)", result.FailedIndex)
	}
	n := countCallsWithPrefix(m.calls, "AgentStart(")
	if n < 2 {
		t.Fatalf("AgentStart calls = %d, want at least 2 (at least one retry before giving up)", n)
	}
	if n > 10 {
		t.Fatalf("AgentStart calls = %d, want a small bounded number (budget cutoff, not an unbounded loop)", n)
	}
}

// TestExecuteClauthLaunchThreadsPaneID covers Path B (spec §9 step 2): a
// pinned claude account launches through clauth via Runner.PaneRun rather
// than Runner.AgentStart, followed by an explicit OpAwaitDetection --
// build.go's launchOps emits this two-op sequence only when AccountPin is
// set. Both ops target the pane the topology op created, but neither op's
// request carries a PaneID field to fill in (RunArgv is a plain argv
// slice, OpAwaitDetection only carries a Timeout) -- Execute must instead
// pass created.PaneID as PaneRun/AwaitDetection's explicit paneID
// argument. Nothing previously asserted that argument was the *threaded*
// id rather than an empty or stale string.
func TestExecuteClauthLaunchThreadsPaneID(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	in.AccountPin = "work"
	in.ExtraArgs = []string{"--model", "opus"}
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{topo: herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"}}

	result := Execute(context.Background(), m, ops, nil)
	if result.FailedIndex != -1 {
		t.Fatalf("unexpected failure: %+v", result)
	}

	wantCalls := []string{
		"WorktreeCreate(/repo,zvi/fix-pagination,main)",
		"PaneRun(pane-1,clauth,start,work,--,--model,opus)",
		"AwaitDetection(pane-1," + in.DetectionTimeout.String() + ")",
	}
	if !reflect.DeepEqual(m.calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", m.calls, wantCalls)
	}
}

// TestExecuteMalformedOpDoesNotPanic proves Execute fails gracefully,
// rather than panicking on a nil dereference, when an Op's Kind requires a
// request field (here, OpAgentStart's Agent) that is nil. Build never
// produces such an Op, but Execute's contract does not get to assume that
// of every caller.
func TestExecuteMalformedOpDoesNotPanic(t *testing.T) {
	ops := []Op{{Kind: OpAgentStart, Label: "starting agent"}} // Agent left nil

	m := &mockRunner{}
	var progressed []Progress
	result := Execute(context.Background(), m, ops, func(p Progress) { progressed = append(progressed, p) })

	if result.FailedIndex != 0 {
		t.Fatalf("FailedIndex = %d, want 0", result.FailedIndex)
	}
	if len(m.calls) != 0 {
		t.Fatalf("calls = %v, want none: a malformed op must fail before ever reaching the runner", m.calls)
	}
	if len(progressed) == 0 {
		t.Fatal("no progress reported")
	}
	last := progressed[len(progressed)-1]
	if last.State != StepFailed {
		t.Fatalf("last progress state = %v, want StepFailed", last.State)
	}
	if last.Err == nil || !strings.Contains(last.Err.Error(), "malformed op") {
		t.Fatalf("last progress Err = %v, want it to mention \"malformed op\"", last.Err)
	}
}

func TestExecuteFailureAtAgentPromptSurfacesPromptText(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	in.Prompt = "implement the thing"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		failAt:    "AgentPrompt",
		failErr:   errors.New("agent_prompt_stalled"),
		failCount: 1,
		topo:      herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
	}

	result := Execute(context.Background(), m, ops, nil)

	if result.FailedIndex != 2 {
		t.Fatalf("FailedIndex = %d, want 2", result.FailedIndex)
	}
	if result.PromptText != in.Prompt {
		t.Fatalf("PromptText = %q, want %q", result.PromptText, in.Prompt)
	}
}

// containsCall reports whether calls contains want verbatim -- a small
// helper for the promptIfReady tests below, which care about whether one
// particular call happened at all (not the full ordered sequence every
// other wantCalls-style test in this file asserts).
func containsCall(calls []string, want string) bool {
	for _, c := range calls {
		if c == want {
			return true
		}
	}
	return false
}

// TestExecutePromptSentWhenPaneIsReady is promptIfReady's (exec.go) clean
// branch: AgentRead's text carries no blockingDialogSignature match, so
// AgentPrompt is called exactly as it always was -- the task 19 fix
// round's own required "clean ready state -> prompt sent as before"
// scenario, isolated as its own named test (TestExecuteHappyPathThreadsPaneID
// already proves the same sequence via its own wantCalls, incidentally).
func TestExecutePromptSentWhenPaneIsReady(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	in.Prompt = "start work"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		topo:     herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
		readText: "> Sonnet 5 · claude-code\n  Type your message...\n",
	}

	result := Execute(context.Background(), m, ops, nil)

	if result.FailedIndex != -1 {
		t.Fatalf("FailedIndex = %d, want -1: %+v", result.FailedIndex, result)
	}
	if !containsCall(m.calls, "AgentRead(pane-1)") {
		t.Fatalf("calls = %v, want it to contain the AgentRead check", m.calls)
	}
	if !containsCall(m.calls, "AgentPrompt(pane-1,start work)") {
		t.Fatalf("calls = %v, want it to contain the AgentPrompt call", m.calls)
	}
}

// TestExecuteAgentPromptSkippedWhenDialogDetected is promptIfReady's core
// defect-fix scenario (task 19's live checkpoint, task-19-report.md):
// AgentRead's text contains a known blocking-dialog signature (Claude
// Code's real first-run trust-confirmation screen, verbatim from the live
// transcript), so AgentPrompt must never be called at all -- the agent is
// never sent the trailing Enter that, on the real screen, was what
// actually confirmed "No, exit" and killed it. The op still fails exactly
// like any other OpAgentPrompt failure: FailedIndex points at it and
// PromptText surfaces the prompt for manual paste (spec §9 step 3).
func TestExecuteAgentPromptSkippedWhenDialogDetected(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	in.Prompt = "implement the fix"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		topo: herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
		readText: "Quick safety check: Is this a project you created or one you trust?\n\n" +
			"❯ No, exit\n  Yes, I trust this folder\n\nEnter to confirm · Esc to cancel\n",
	}

	result := Execute(context.Background(), m, ops, nil)

	wantFailedIndex := len(ops) - 1 // Build always appends OpAgentPrompt last when Prompt != ""
	if result.FailedIndex != wantFailedIndex {
		t.Fatalf("FailedIndex = %d, want %d (the prompt op): %+v", result.FailedIndex, wantFailedIndex, result)
	}
	if result.PromptText != in.Prompt {
		t.Fatalf("PromptText = %q, want %q (surfaced for manual paste)", result.PromptText, in.Prompt)
	}
	if !containsCall(m.calls, "AgentRead(pane-1)") {
		t.Fatalf("calls = %v, want it to contain the AgentRead check", m.calls)
	}
	if containsCall(m.calls, "AgentPrompt(pane-1,implement the fix)") {
		t.Fatalf("calls = %v, want NO AgentPrompt call -- a detected dialog must block it entirely", m.calls)
	}
}

// TestExecuteAgentPromptSkippedWhenAgentReadFails is promptIfReady's
// fail-safe branch: a pane whose screen cannot even be read is treated the
// same as a detected dialog -- "when in doubt, keep the session and
// surface the text" -- rather than assumed safe to prompt.
func TestExecuteAgentPromptSkippedWhenAgentReadFails(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	in.Prompt = "implement the fix"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		failAt:    "AgentRead",
		failErr:   errors.New("agent target pane-1 not found"),
		failCount: 1,
		topo:      herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
	}

	result := Execute(context.Background(), m, ops, nil)

	if result.PromptText != in.Prompt {
		t.Fatalf("PromptText = %q, want %q (surfaced for manual paste)", result.PromptText, in.Prompt)
	}
	if containsCall(m.calls, "AgentPrompt(pane-1,implement the fix)") {
		t.Fatalf("calls = %v, want NO AgentPrompt call -- an unreadable pane must block it too", m.calls)
	}
}

func TestExecuteProgressSequence(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	in.Prompt = "go"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{topo: herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"}}

	var got []Progress
	result := Execute(context.Background(), m, ops, func(p Progress) { got = append(got, p) })
	if result.FailedIndex != -1 {
		t.Fatalf("unexpected failure: %+v", result)
	}

	if len(got) != 2*len(ops) {
		t.Fatalf("progress events = %d, want %d ([Running,Done] per op)", len(got), 2*len(ops))
	}
	for i, op := range ops {
		running, done := got[2*i], got[2*i+1]
		if running.Index != i || running.Total != len(ops) || running.Label != op.Label || running.State != StepRunning {
			t.Errorf("progress[%d] = %+v, want Index=%d Total=%d Label=%q State=StepRunning", 2*i, running, i, len(ops), op.Label)
		}
		if done.Index != i || done.Total != len(ops) || done.Label != op.Label || done.State != StepDone {
			t.Errorf("progress[%d] = %+v, want Index=%d Total=%d Label=%q State=StepDone", 2*i+1, done, i, len(ops), op.Label)
		}
	}
}

func TestCleanWorktreeCallsWorktreeRemove(t *testing.T) {
	m := &mockRunner{}
	in := validInput()
	in.UseWorktree = true
	created := herdrc.CreatedTopology{WorkspaceID: "ws-1"}

	if err := Clean(context.Background(), m, in, created); err != nil {
		t.Fatalf("Clean: %v", err)
	}
	want := []string{"WorktreeRemove(ws-1)"}
	if !reflect.DeepEqual(m.calls, want) {
		t.Fatalf("calls = %v, want %v", m.calls, want)
	}
}

func TestCleanNonWorktreeCallsWorkspaceClose(t *testing.T) {
	m := &mockRunner{}
	in := validInput()
	in.UseWorktree = false
	created := herdrc.CreatedTopology{WorkspaceID: "ws-2"}

	if err := Clean(context.Background(), m, in, created); err != nil {
		t.Fatalf("Clean: %v", err)
	}
	want := []string{"WorkspaceClose(ws-2)"}
	if !reflect.DeepEqual(m.calls, want) {
		t.Fatalf("calls = %v, want %v", m.calls, want)
	}
}

func TestCleanPropagatesRunnerError(t *testing.T) {
	m := &mockRunner{failAt: "WorktreeRemove", failErr: errors.New("herdr: workspace not found"), failCount: 1}
	in := validInput()
	in.UseWorktree = true
	created := herdrc.CreatedTopology{WorkspaceID: "ws-1"}

	err := Clean(context.Background(), m, in, created)
	if !errors.Is(err, m.failErr) {
		t.Fatalf("Clean error = %v, want it to wrap %v", err, m.failErr)
	}
}

func TestCleanCheckAlwaysAllowsNonWorktree(t *testing.T) {
	in := validInput()
	in.UseWorktree = false

	decision := CleanCheck(context.Background(), in, herdrc.CreatedTopology{})
	if !decision.Allowed {
		t.Fatalf("expected a non-worktree space to always be allowed, reason: %q", decision.Reason)
	}
}

func TestCleanCheckAllowsCleanWorktree(t *testing.T) {
	repo := mkRepo(t)

	in := validInput()
	in.UseWorktree = true
	in.BaseRef = "main"
	created := herdrc.CreatedTopology{CheckoutPath: repo}

	decision := CleanCheck(context.Background(), in, created)
	if !decision.Allowed {
		t.Fatalf("expected a pristine worktree to be allowed, reason: %q", decision.Reason)
	}
}

func TestCleanCheckDeniesDirtyWorktree(t *testing.T) {
	repo := mkRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "f"), []byte("changed"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	in := validInput()
	in.UseWorktree = true
	in.BaseRef = "main"
	created := herdrc.CreatedTopology{CheckoutPath: repo}

	decision := CleanCheck(context.Background(), in, created)
	if decision.Allowed {
		t.Fatal("expected a dirty worktree to be denied")
	}
	if strings.TrimSpace(decision.Reason) == "" {
		t.Fatal("expected a human-readable Reason when denied")
	}
}

func TestCleanCheckDeniesOnDisposableError(t *testing.T) {
	// An invalid BaseRef makes gitx.Disposable itself fail (not just
	// return ok=false) -- that must surface as denied with the error's
	// context in Reason, never as a silent "allowed".
	repo := mkRepo(t)

	in := validInput()
	in.UseWorktree = true
	in.BaseRef = "this-ref-does-not-exist"
	created := herdrc.CreatedTopology{CheckoutPath: repo}

	decision := CleanCheck(context.Background(), in, created)
	if decision.Allowed {
		t.Fatal("expected a Disposable error (invalid base ref) to deny cleanup, not silently allow it")
	}
	if strings.TrimSpace(decision.Reason) == "" {
		t.Fatal("expected a human-readable Reason describing the error")
	}
}
