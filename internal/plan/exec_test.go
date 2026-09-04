package plan

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ZviBaratz/herdr-draft/internal/gitx"
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

	// workspacesBeforeCreate, when non-nil, is what WorkspaceList returns --
	// the "what already exists" snapshot Execute's reuse check takes
	// immediately before a worktree create (placement spec §5.2/§3).
	workspacesBeforeCreate []herdrc.WorkspaceInfo

	// tabTopo, when non-nil, is what TabCreate returns instead of the
	// shared topo -- so a test can tell a pane that came from the reuse
	// CLAIM (placement spec §5.2) apart from the one WorktreeCreate handed
	// back. With every *Create method answering the same topo, "the agent
	// went to the claimed pane" and "the agent went to the stranger's pane"
	// are the same assertion and neither can fail.
	tabTopo *herdrc.CreatedTopology

	// splitTopo, when non-nil, is what PaneSplit returns instead of the
	// shared topo -- the same trick tabTopo plays, and for the same reason:
	// with every *Create answering one topo, "Created still holds the
	// WORKTREE's checkout" and "Created holds the SPLIT's checkout" are the
	// same assertion, so the §9 clobber regression cannot be pinned at all.
	splitTopo *herdrc.CreatedTopology
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
	return m.workspacesBeforeCreate, nil
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
	if m.tabTopo != nil {
		return *m.tabTopo, nil
	}
	return m.topo, nil
}

func (m *mockRunner) PaneSplit(ctx context.Context, req herdrc.PaneSplitReq) (herdrc.CreatedTopology, error) {
	m.record("PaneSplit", req.PaneID, req.Direction, req.Cwd)
	if m.shouldFail("PaneSplit") {
		return herdrc.CreatedTopology{}, m.failErr
	}
	if m.splitTopo != nil {
		return *m.splitTopo, nil
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

func (m *mockRunner) PaneClose(ctx context.Context, paneID string) error {
	m.record("PaneClose", paneID)
	if m.shouldFail("PaneClose") {
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
		"WorkspaceList()",
		"WorktreeCreate(/repo,zvi/fix-pagination,main)",
		"AgentStart(" + AgentName(in.Title) + ",pane-1)",
		"AgentRead(pane-1)",
		"AgentPrompt(pane-1,start work)",
	}
	if !reflect.DeepEqual(m.calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", m.calls, wantCalls)
	}
}

// --- placement spec §5.1/§5.2/§5.3: the space vs. the agent's pane --------

func TestExecute_WorktreeNotReused(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		workspacesBeforeCreate: []herdrc.WorkspaceInfo{{WorkspaceID: "w1"}, {WorkspaceID: "w2"}},
		topo:                   herdrc.CreatedTopology{WorkspaceID: "w9", TabID: "t1", PaneID: "pane-1", CheckoutPath: "/tmp/wt"},
	}
	result := Execute(context.Background(), m, ops, nil)

	if result.FailedIndex != -1 {
		t.Fatalf("FailedIndex = %d, want -1", result.FailedIndex)
	}
	if result.SpaceReused {
		t.Error("SpaceReused = true, want false -- w9 was not in the before-list")
	}
	if result.AgentPane != "pane-1" {
		t.Errorf("AgentPane = %q, want %q", result.AgentPane, "pane-1")
	}
	if result.Created == nil || result.Created.PaneID != "pane-1" {
		t.Fatalf("Created = %+v, want PaneID pane-1", result.Created)
	}
	want := []string{
		"WorkspaceList()",
		"WorktreeCreate(/repo,zvi/fix-pagination,main)",
		"AgentStart(" + AgentName(in.Title) + ",pane-1)",
	}
	if !reflect.DeepEqual(m.calls, want) {
		t.Fatalf("calls = %v, want %v", m.calls, want)
	}
}

func TestExecute_WorktreeReusedClaimsAFreshTab(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// w9 is already in the before-list: the create returned a workspace
	// that was open before Execute ever ran. tabTopo is set to a DIFFERENT
	// pane than topo's, so the claim call (TabCreate) is distinguishable
	// from a bug that simply trusted WorktreeCreate's own response --
	// with a single shared topo, "went to the claimed pane" and "went to
	// the stranger's pane" would be the same assertion and neither could
	// fail (fix round 1's finding).
	m := &mockRunner{
		workspacesBeforeCreate: []herdrc.WorkspaceInfo{{WorkspaceID: "w9", Label: "somebody-else"}},
		topo:                   herdrc.CreatedTopology{WorkspaceID: "w9", TabID: "t1", PaneID: "stranger-pane", CheckoutPath: "/tmp/wt"},
		tabTopo:                &herdrc.CreatedTopology{WorkspaceID: "w9", TabID: "t2", PaneID: "claimed-pane", CheckoutPath: "/tmp/wt"},
	}
	result := Execute(context.Background(), m, ops, nil)

	if result.FailedIndex != -1 {
		t.Fatalf("FailedIndex = %d, want -1", result.FailedIndex)
	}
	if !result.SpaceReused {
		t.Fatal("SpaceReused = false, want true")
	}
	if result.SpaceLabel != "somebody-else" {
		t.Errorf("SpaceLabel = %q, want %q", result.SpaceLabel, "somebody-else")
	}
	if result.Created == nil || result.Created.WorkspaceID != "w9" {
		t.Fatalf("Created = %+v, want the reused workspace w9 -- CleanCheck/Clean still need to act on the real space", result.Created)
	}
	if result.AgentPane == "stranger-pane" {
		t.Fatal("AgentPane == the pane WorktreeCreate returned -- the whole point of the correction is to NOT trust it")
	}
	if result.AgentPane != "claimed-pane" {
		t.Errorf("AgentPane = %q, want %q (the claimed tab's own pane)", result.AgentPane, "claimed-pane")
	}
	want := []string{
		"WorkspaceList()",
		"WorktreeCreate(/repo,zvi/fix-pagination,main)",
		"TabCreate(w9,/tmp/wt)",
		"AgentStart(" + AgentName(in.Title) + ",claimed-pane)",
	}
	if !reflect.DeepEqual(m.calls, want) {
		t.Fatalf("calls = %v, want %v", m.calls, want)
	}
}

func TestExecute_WorktreeReusedWithAPlacementOpClaimsNoTab(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	in.Placement = PlacementTabHere
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		workspacesBeforeCreate: []herdrc.WorkspaceInfo{{WorkspaceID: "w9", Label: "somebody-else"}},
		topo:                   herdrc.CreatedTopology{WorkspaceID: "w9", TabID: "t1", PaneID: "stranger-pane", CheckoutPath: "/tmp/wt"},
	}
	result := Execute(context.Background(), m, ops, nil)

	if result.FailedIndex != -1 {
		t.Fatalf("FailedIndex = %d, want -1", result.FailedIndex)
	}
	if !result.SpaceReused {
		t.Fatal("SpaceReused = false, want true -- the space fact does not depend on where the agent ends up")
	}
	// No extra TabCreate into w9: the placement op (index 1) is the
	// agent-pane op, and it is the one that decides where the agent runs.
	for _, c := range m.calls {
		if strings.HasPrefix(c, "TabCreate(w9,") {
			t.Fatalf("an extra TabCreate claimed a pane in the reused workspace, want none: %v", m.calls)
		}
	}
	want := []string{
		"WorkspaceList()",
		"WorktreeCreate(/repo,zvi/fix-pagination,main)",
		"TabCreate(" + in.Ctx.WorkspaceID + ",/tmp/wt)", // the PLACEMENT op, Cwd filled from CheckoutPath
		"AgentStart(" + AgentName(in.Title) + "," + m.topo.PaneID + ")",
	}
	if !reflect.DeepEqual(m.calls, want) {
		t.Fatalf("calls = %v, want %v", m.calls, want)
	}
	if result.AgentPane != m.topo.PaneID {
		t.Errorf("AgentPane = %q, want %q (the placement op's own pane)", result.AgentPane, m.topo.PaneID)
	}
}

func TestExecute_WorkspaceListFailureFailsTheStepBeforeAnythingIsCreated(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	failErr := errors.New("herdr unreachable")
	m := &mockRunner{failAt: "WorkspaceList", failErr: failErr, failCount: 1}
	result := Execute(context.Background(), m, ops, nil)

	if result.FailedIndex != 0 {
		t.Fatalf("FailedIndex = %d, want 0", result.FailedIndex)
	}
	if result.Created != nil {
		t.Fatalf("Created = %+v, want nil -- nothing was created", result.Created)
	}
	for _, c := range m.calls {
		if strings.HasPrefix(c, "WorktreeCreate") {
			t.Fatal("WorktreeCreate was called despite the pre-check failing -- fail-closed means nothing runs after a failed WorkspaceList")
		}
	}
}

// TestExecute_CheckoutPathSurvivesAPlacementOp is design §9's mandated
// clobber-regression test: "Created.CheckoutPath is still the worktree's
// after OpPaneSplit has produced a topology. This is the regression test
// for the clobber §5.4 describes, and it is the one that would fail
// against a naive `created = topo` on every topology op." splitTopo is
// what makes this test able to fail at all: with every *Create sharing
// one topo, "Created still holds the WORKTREE's checkout" and "Created
// holds the SPLIT's checkout" would be the same assertion (fix round 2's
// finding -- the same defect class TestExecute_WorktreeReusedClaimsAFreshTab
// had before tabTopo).
func TestExecute_CheckoutPathSurvivesAPlacementOp(t *testing.T) {
	// PlacementSplitHere, not reused -- the other placement op shape.
	in := validInput()
	in.UseWorktree = true
	in.Placement = PlacementSplitHere
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		topo:      herdrc.CreatedTopology{WorkspaceID: "wH", TabID: "tH", PaneID: "worktree-pane", CheckoutPath: "/checkout/x"},
		splitTopo: &herdrc.CreatedTopology{WorkspaceID: "wG", TabID: "tG", PaneID: "split-pane", CheckoutPath: ""},
	}
	result := Execute(context.Background(), m, ops, nil)

	if result.FailedIndex != -1 {
		t.Fatalf("FailedIndex = %d, want -1", result.FailedIndex)
	}
	if result.Created == nil {
		t.Fatal("Created = nil")
	}
	// The SPACE is the worktree op's topology, not the split's.
	if result.Created.CheckoutPath != "/checkout/x" {
		t.Errorf("Created.CheckoutPath = %q, want %q -- the placement op clobbered the space, which is exactly what disables CleanCheck's commits-beyond-base half", result.Created.CheckoutPath, "/checkout/x")
	}
	if result.Created.WorkspaceID != "wH" {
		t.Errorf("Created.WorkspaceID = %q, want %q (the worktree's own space)", result.Created.WorkspaceID, "wH")
	}
	// The AGENT PANE is the split's, not the worktree's.
	if result.AgentPane != "split-pane" {
		t.Errorf("AgentPane = %q, want %q (the placement op's own pane)", result.AgentPane, "split-pane")
	}
	// And the cwd threading itself, which is a DIFFERENT mechanism from the
	// clobber above and worth keeping pinned separately.
	want := "PaneSplit(" + in.Ctx.FocusedPaneID + ",right,/checkout/x)"
	if !containsCall(m.calls, want) {
		t.Fatalf("calls = %v, want it to contain %q", m.calls, want)
	}
}

// TestExecute_ReuseClaimFailureStillReportsTheSpaceItCreated is fix round
// 1's regression for a gap Q2 of the task-3 report raised: §5.2's
// fail-closed reasoning ("failing before WorktreeCreate runs means nothing
// is half-built") only covers the WorkspaceList pre-check, which runs
// BEFORE anything mutates herdr's state. The reuse claim's TabCreate runs
// AFTER WorktreeCreate has already succeeded -- a checkout exists on disk
// and herdr holds a workspace for it -- so a claim failure must still
// report that space, or handleSubmitDone reads Created == nil as "nothing
// to clean" about something that very much exists, and the worktree is
// orphaned with no keep-or-clean gate ever offered.
func TestExecute_ReuseClaimFailureStillReportsTheSpaceItCreated(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Reuse detected, so Execute makes the claim call -- and the claim
	// fails. worktree create has ALREADY succeeded by then: a checkout
	// exists on disk and herdr holds a workspace for it.
	m := &mockRunner{
		workspacesBeforeCreate: []herdrc.WorkspaceInfo{{WorkspaceID: "w9", Label: "somebody-else"}},
		topo:                   herdrc.CreatedTopology{WorkspaceID: "w9", PaneID: "stranger-pane", CheckoutPath: "/tmp/wt"},
		failAt:                 "TabCreate",
		failErr:                errors.New("herdr said no"),
		failCount:              1,
	}
	result := Execute(context.Background(), m, ops, nil)

	if result.FailedIndex != 0 {
		t.Fatalf("FailedIndex = %d, want 0", result.FailedIndex)
	}
	if result.Created == nil {
		t.Fatal("Created = nil, but worktree create succeeded -- the keep-or-clean gate would never be offered and the checkout would be orphaned")
	}
	if result.Created.CheckoutPath != "/tmp/wt" {
		t.Errorf("Created.CheckoutPath = %q, want %q", result.Created.CheckoutPath, "/tmp/wt")
	}
	if !result.SpaceReused || result.SpaceLabel != "somebody-else" {
		t.Errorf("SpaceReused/SpaceLabel = %v/%q, want true/%q -- CleanCheck must still refuse to remove a workspace the user owns", result.SpaceReused, result.SpaceLabel, "somebody-else")
	}
	if result.AgentPane != "" {
		t.Errorf("AgentPane = %q, want empty -- no pane was ever claimed, so Clean must have nothing to close", result.AgentPane)
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
		"WorkspaceList()",
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
		"WorkspaceList()",
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
	result := ExecResult{Created: &created, AgentPane: created.PaneID}

	if err := Clean(context.Background(), m, in, result); err != nil {
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
	result := ExecResult{Created: &created, AgentPane: created.PaneID}

	if err := Clean(context.Background(), m, in, result); err != nil {
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
	result := ExecResult{Created: &created, AgentPane: created.PaneID}

	err := Clean(context.Background(), m, in, result)
	if !errors.Is(err, m.failErr) {
		t.Fatalf("Clean error = %v, want it to wrap %v", err, m.failErr)
	}
}

func TestCleanCheckAlwaysAllowsNonWorktree(t *testing.T) {
	in := validInput()
	in.UseWorktree = false

	decision := CleanCheck(context.Background(), in, ExecResult{Created: &herdrc.CreatedTopology{}})
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
	result := ExecResult{Created: &created, AgentPane: created.PaneID}

	decision := CleanCheck(context.Background(), in, result)
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
	result := ExecResult{Created: &created, AgentPane: created.PaneID}

	decision := CleanCheck(context.Background(), in, result)
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
	result := ExecResult{Created: &created, AgentPane: created.PaneID}

	decision := CleanCheck(context.Background(), in, result)
	if decision.Allowed {
		t.Fatal("expected a Disposable error (invalid base ref) to deny cleanup, not silently allow it")
	}
	if strings.TrimSpace(decision.Reason) == "" {
		t.Fatal("expected a human-readable Reason describing the error")
	}
}

func TestCleanCheckRefusesAReusedSpace(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	created := herdrc.CreatedTopology{WorkspaceID: "w9", CheckoutPath: "/tmp/wt"}
	result := ExecResult{Created: &created, AgentPane: "claimed-pane", SpaceReused: true, SpaceLabel: "somebody-else"}

	decision := CleanCheck(context.Background(), in, result)
	if decision.Allowed {
		t.Fatal("CleanCheck allowed cleaning a reused space, want refused")
	}
	if !strings.Contains(decision.Reason, "somebody-else") {
		t.Errorf("Reason = %q, want it to name the reused workspace's label", decision.Reason)
	}
	if !strings.Contains(decision.Reason, "/tmp/wt") {
		t.Errorf("Reason = %q, want it to name the checkout path so the user can act manually", decision.Reason)
	}
	if !strings.Contains(decision.Reason, "git worktree remove /tmp/wt") {
		t.Errorf("Reason = %q, want the exact recovery command (design §11 item 4) -- `herdr worktree remove` would close the very workspace this refusal is protecting", decision.Reason)
	}
}

func TestCleanCheckRefusesAReusedSpaceEvenWhenTheWorktreeItselfIsPristine(t *testing.T) {
	// SpaceReused must short-circuit BEFORE gitx.Disposable ever runs --
	// a pristine checkout is irrelevant, the workspace it sits in is not
	// herdr-draft's to remove regardless.
	repo := mkRepo(t)
	in := validInput()
	in.UseWorktree = true
	in.BaseRef = "main"
	created := herdrc.CreatedTopology{WorkspaceID: "w9", CheckoutPath: repo}
	result := ExecResult{Created: &created, SpaceReused: true, SpaceLabel: "somebody-else"}

	decision := CleanCheck(context.Background(), in, result)
	if decision.Allowed {
		t.Fatal("a reused space must never be allowed, however clean the checkout looks")
	}
}

// TestClean_ClosesTheClaimedPaneBeforeRemovingTheWorktree gives created a
// real, different PaneID ("worktree-pane") rather than leaving it at its
// zero value: with PaneID == "" an implementation keying off AgentPane !=
// "" alone (or almost anything else) would also make this assertion pass,
// which is exactly the "same assertion for two different mechanisms" shape
// this plan has been bitten by three times already (see mockRunner's
// tabTopo/splitTopo doc comments). Genuinely populated and genuinely
// different is what makes the inequality mean something.
func TestClean_ClosesTheClaimedPaneBeforeRemovingTheWorktree(t *testing.T) {
	m := &mockRunner{}
	in := validInput()
	in.UseWorktree = true
	created := herdrc.CreatedTopology{WorkspaceID: "w9", PaneID: "worktree-pane"}
	result := ExecResult{Created: &created, AgentPane: "claimed-pane"} // != created.PaneID ("worktree-pane")

	if err := Clean(context.Background(), m, in, result); err != nil {
		t.Fatalf("Clean: %v", err)
	}
	want := []string{"PaneClose(claimed-pane)", "WorktreeRemove(w9)"}
	if !reflect.DeepEqual(m.calls, want) {
		t.Fatalf("calls = %v, want %v (pane closed BEFORE the space is removed)", m.calls, want)
	}
}

func TestClean_NoPaneCloseWhenAgentPaneMatchesTheSpace(t *testing.T) {
	m := &mockRunner{}
	in := validInput()
	in.UseWorktree = true
	created := herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "p1"}
	result := ExecResult{Created: &created, AgentPane: "p1"} // == created.PaneID

	if err := Clean(context.Background(), m, in, result); err != nil {
		t.Fatalf("Clean: %v", err)
	}
	want := []string{"WorktreeRemove(ws-1)"}
	if !reflect.DeepEqual(m.calls, want) {
		t.Fatalf("calls = %v, want %v -- the common case must not call PaneClose at all", m.calls, want)
	}
}

// --- agent_name_taken dedupe retry (spec §9, finding I1) ------------------

// nameTakenRunner is a mockRunner that answers AgentStart with herdr's own
// agent_name_taken error for every name in taken, and records every name it
// was asked for.
type nameTakenRunner struct {
	mockRunner
	taken     map[string]bool
	namesSeen []string
}

func (m *nameTakenRunner) AgentStart(_ context.Context, req herdrc.AgentStartReq) error {
	m.namesSeen = append(m.namesSeen, req.Name)
	if m.taken[req.Name] {
		// The exact shape herdrc.CLIRunner surfaces: herdr's stderr error
		// envelope wrapped into a plain Go error (there is no typed code).
		return fmt.Errorf("herdr agent start: exit status 1: {\"error\":{\"code\":\"%s\",\"message\":\"agent name %s is already used\"}}",
			nameTakenErrorCode, req.Name)
	}
	return nil
}

// TestExecuteRetriesTakenAgentName pins spec §9's "DuplicateName retried
// with suffix", which had no implementation at all: herdr's
// agent_name_taken is not agent_pane_busy, so retryBusy passed it through
// and the whole submit failed at step 2 -- after the worktree already
// existed. Opening a second session from the same Linear issue was enough
// to hit it.
func TestExecuteRetriesTakenAgentName(t *testing.T) {
	m := &nameTakenRunner{taken: map[string]bool{"fix-pagination": true, "fix-pagination-2": true}}
	m.topo = herdrc.CreatedTopology{WorkspaceID: "w9", PaneID: "w9:p1"}

	ops := []Op{
		{Kind: OpWorkspaceCreate, Label: "creating workspace", Workspace: &herdrc.WorkspaceCreateReq{}},
		{Kind: OpAgentStart, Label: "starting agent", Agent: &herdrc.AgentStartReq{Name: "fix-pagination", Kind: "claude"}},
	}

	res := Execute(context.Background(), m, ops, nil)

	if res.FailedIndex != -1 {
		t.Fatalf("FailedIndex = %d, want -1 -- a taken name must be retried with a suffix, not fail the submit", res.FailedIndex)
	}
	want := []string{"fix-pagination", "fix-pagination-2", "fix-pagination-3"}
	if !reflect.DeepEqual(m.namesSeen, want) {
		t.Fatalf("names attempted = %v, want %v", m.namesSeen, want)
	}
}

// TestExecuteGivesUpAfterBoundedNameAttempts pins the retry's bound: a
// server that rejects every candidate must surface a real failure, not
// loop forever.
func TestExecuteGivesUpAfterBoundedNameAttempts(t *testing.T) {
	m := &nameTakenRunner{taken: nil}
	m.topo = herdrc.CreatedTopology{WorkspaceID: "w9", PaneID: "w9:p1"}
	// A nil map answers false for every key, so make one that says yes to
	// everything instead.
	m.taken = map[string]bool{}
	for i := 1; i <= maxAgentNameAttempts+3; i++ {
		m.taken[AgentNameWithSuffix("fix-pagination", i)] = true
	}
	m.taken["fix-pagination"] = true

	ops := []Op{
		{Kind: OpWorkspaceCreate, Label: "creating workspace", Workspace: &herdrc.WorkspaceCreateReq{}},
		{Kind: OpAgentStart, Label: "starting agent", Agent: &herdrc.AgentStartReq{Name: "fix-pagination", Kind: "claude"}},
	}

	res := Execute(context.Background(), m, ops, nil)

	if res.FailedIndex != 1 {
		t.Fatalf("FailedIndex = %d, want 1 -- every candidate name was refused", res.FailedIndex)
	}
	if len(m.namesSeen) != maxAgentNameAttempts {
		t.Fatalf("attempted %d names (%v), want exactly maxAgentNameAttempts=%d",
			len(m.namesSeen), m.namesSeen, maxAgentNameAttempts)
	}
}

// TestAgentNameWithSuffixStaysInsideHerdrsPattern pins that every name the
// retry can produce is still one herdr will accept
// ([a-z][a-z0-9_-]{0,31}), including for a base name already at
// AgentName's own 30-rune cap with a suffix wider than the two runes that
// cap reserves.
func TestAgentNameWithSuffixStaysInsideHerdrsPattern(t *testing.T) {
	pattern := regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

	bases := []string{
		AgentName("Fix pagination"),
		AgentName(strings.Repeat("a very long session title ", 4)),
		AgentName("---"),
	}
	for _, base := range bases {
		for n := 2; n <= 120; n++ {
			got := AgentNameWithSuffix(base, n)
			if !pattern.MatchString(got) {
				t.Errorf("AgentNameWithSuffix(%q, %d) = %q, which herdr's own agent-name pattern rejects", base, n, got)
			}
		}
	}
}

// --- the clean gate's "no commits beyond base" check (finding I4) ---------

// mkWorktree adds a linked worktree on a new branch to repo, mirroring what
// `herdr worktree create` does, and returns its checkout path.
func mkWorktree(t *testing.T, repo, branch string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wt")
	gitIn(t, repo, "worktree", "add", "-q", "-b", branch, path)
	return path
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v (in %s): %v\n%s", args, dir, err, out)
	}
}

// TestCleanCheckDeniesCommitsBeyondTheDefaultHeadBase is the regression for
// I4: WorktreeField.Base() reports "" for its default HEAD row, and
// CleanCheck passed that straight to gitx.Disposable, producing `git
// rev-list --count ..HEAD` -- HEAD..HEAD, which counts 0 for every
// worktree no matter what is in it. The "no commits beyond base" half of
// spec §9's clean gate was therefore a no-op in the DEFAULT case, offering
// to destroy a worktree carrying real commits.
func TestCleanCheckDeniesCommitsBeyondTheDefaultHeadBase(t *testing.T) {
	repo := mkRepo(t)
	wt := mkWorktree(t, repo, "feature")

	in := validInput()
	in.UseWorktree = true
	in.BaseRef = "" // WorktreeField.Base()'s own "" == HEAD sentinel
	in.ProjectDir = repo
	created := herdrc.CreatedTopology{CheckoutPath: wt}
	result := ExecResult{Created: &created, AgentPane: created.PaneID}

	if decision := CleanCheck(context.Background(), in, result); !decision.Allowed {
		t.Fatalf("a pristine worktree at the default HEAD base was denied: %q", decision.Reason)
	}

	gitIn(t, wt, "commit", "-q", "--allow-empty", "-m", "real work")

	decision := CleanCheck(context.Background(), in, result)
	if decision.Allowed {
		t.Fatal("a worktree with a commit beyond its base was allowed to be destroyed")
	}
	if !strings.Contains(decision.Reason, "commit") {
		t.Fatalf("Reason = %q, want it to name the commits that block cleanup", decision.Reason)
	}
}

// TestDisposableRejectsAnEmptyBaseRef pins the hardening underneath that
// fix at its own layer: gitx.Disposable must refuse an empty base rather
// than silently answering "0 commits ahead" for every worktree it is ever
// handed.
func TestDisposableRejectsAnEmptyBaseRef(t *testing.T) {
	repo := mkRepo(t)
	wt := mkWorktree(t, repo, "feature")
	gitIn(t, wt, "commit", "-q", "--allow-empty", "-m", "real work")

	ok, _, err := gitx.Disposable(context.Background(), wt, "")
	if err == nil {
		t.Fatalf("Disposable(worktree, \"\") returned ok=%v with no error, want a refusal", ok)
	}
	if ok {
		t.Fatal("Disposable(worktree, \"\") reported a worktree with an extra commit as disposable")
	}
}
