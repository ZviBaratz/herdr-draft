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

	// readText is what AgentRead returns on success. Its zero value is
	// paintedIdleScreen, NOT the empty string: after #116 an empty
	// detection buffer is a refusal rather than the "ordinary ready pane"
	// the pre-existing scenarios meant by it, so the default has to be a
	// screen that has actually painted. blankReadsFirst is how a test asks
	// for the empty one on purpose.
	readText string

	// blankReadsFirst makes the first n AgentRead calls come back
	// SUCCESSFULLY with nothing in them -- #116's own mechanism, and the
	// state the fake could not express before it: herdr answers the read,
	// and the pane it answers about has not drawn anything yet. Distinct
	// from readErrToGive beside it, which is a read that fails.
	blankReadsFirst int

	// postPromptText/postPromptErr are what AgentRead returns once
	// AgentPrompt has been called -- the pane AFTER a send, which is the
	// only thing confirmPromptLanded looks at. A call-count dial cannot
	// express it, because how many reads precede the send depends on
	// whether a wait ran.
	postPromptText string
	postPromptErr  error
	promptSent     bool

	// readErr, when non-nil, is what AgentRead fails with -- independent of
	// failAt, which names a single method and so cannot express "this op
	// failed AND reading the pane afterwards also failed". That pair is
	// exactly explainBlockedStart's fail-safe branch (an agent_not_ready
	// start whose pane cannot be read), so it needs a second dial.
	readErr error

	// readTextClearsAfter, when > 0, makes AgentRead return readText for
	// that many calls and "" from then on -- the dialog as the person at
	// the keyboard answers it, several polls into a wait. A screen that
	// never changes cannot express the thing #115's prompt-step wait is
	// about.
	readTextClearsAfter int
	readCalls           int

	// readErrAfter/readErrToGive make AgentRead succeed for that many
	// calls and then fail with readErrToGive from then on -- the pane as
	// it looks once the agent behind it has exited, which is what a
	// declined dialog leaves behind.
	readErrAfter  int
	readErrToGive error

	// readErrCount bounds that run of failures: 0 means "from readErrAfter
	// on, forever" (an agent that has gone), a positive n means exactly n
	// failed reads and then normal service (a pane briefly unreadable
	// mid-repaint). The two must not be confused, which is why a test can
	// ask for either.
	readErrCount int

	// readTextClearsOnReady makes AgentRead stop returning readText once
	// AwaitDetection has succeeded: the pane as it looks AFTER the person
	// answered the dialog. A mock whose screen never changes is not the
	// world #115 exists for -- the wait would end in a ready agent whose
	// pane still shows a trust prompt, and promptIfReady's guard would
	// correctly refuse the very prompt the wait was for.
	readTextClearsOnReady bool
	dialogAnswered        bool

	// awaitErr, when non-nil, is what AwaitDetection fails with on EVERY
	// call -- independent of failAt, which names a single method and so
	// cannot express "the start was refused AND the wait that follows it
	// also ended badly". That pair is the whole of #115's failure half:
	// the launch is blocked, herdr-draft waits, and the user never answers
	// (or answers "No, exit").
	awaitErr error

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
	m.promptSent = true
	return nil
}

func (m *mockRunner) AgentRead(ctx context.Context, target string) (string, error) {
	m.record("AgentRead", target)
	if m.readErr != nil {
		return "", m.readErr
	}
	if m.shouldFail("AgentRead") {
		return "", m.failErr
	}
	m.readCalls++
	if m.promptSent {
		if m.postPromptErr != nil {
			return "", m.postPromptErr
		}
		if m.postPromptText != "" {
			return m.postPromptText, nil
		}
	}
	if m.readErrToGive != nil && m.readCalls > m.readErrAfter &&
		(m.readErrCount == 0 || m.readCalls <= m.readErrAfter+m.readErrCount) {
		return "", m.readErrToGive
	}
	if m.readCalls <= m.blankReadsFirst {
		return "", nil
	}
	if m.dialogAnswered {
		return paintedIdleScreen, nil
	}
	if m.readTextClearsAfter > 0 && m.readCalls > m.readTextClearsAfter {
		return paintedIdleScreen, nil
	}
	if m.readText == "" {
		return paintedIdleScreen, nil
	}
	return m.readText, nil
}

// paintedIdleScreen is an ordinary, painted, dialog-free pane -- what
// mockRunner.AgentRead returns whenever no dial asks for something else.
//
// It exists because the fake's old default was the EMPTY string, which is
// to say every happy-path test in this file used to assert that a prompt
// is sent into a pane with nothing on it. That was #116 encoded as a
// fixture: the defect and the test agreed with each other, so the suite
// stayed green through the whole live run that found it.
const paintedIdleScreen = "> Sonnet 5 · claude-code\n  Type your message...\n"

func (m *mockRunner) AwaitDetection(ctx context.Context, paneID string, timeout, blockedTimeout time.Duration) error {
	m.record("AwaitDetection", paneID, timeout.String(), blockedTimeout.String())
	if m.shouldFail("AwaitDetection") {
		return m.failErr
	}
	if m.awaitErr != nil {
		return m.awaitErr
	}
	if m.readTextClearsOnReady {
		m.dialogAnswered = true
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
// withDialogPollInterval shrinks the prompt-step dialog wait's poll cadence
// so a test runs its loop instantly instead of sleeping out real seconds --
// the same trick withBusyRetryOverrides plays, and for the same reason.
// withPromptRetrySettle shrinks the pause before a stalled prompt is sent
// again, so a test runs the retry instantly instead of sleeping it out.
func withPromptRetrySettle(t *testing.T, d time.Duration) {
	t.Helper()
	orig := promptRetrySettle
	promptRetrySettle = d
	t.Cleanup(func() { promptRetrySettle = orig })
}

func withDialogPollInterval(t *testing.T, interval time.Duration) {
	t.Helper()
	orig := dialogPollInterval
	dialogPollInterval = interval
	t.Cleanup(func() { dialogPollInterval = orig })
}

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

	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

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
		// The read AFTER the send is #116's post-send verification, and it
		// is in the happy path's own expected sequence on purpose: a check
		// that only runs when something already looks wrong would not have
		// caught the failure it exists for, which reported success.
		"AgentRead(pane-1)",
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
	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

	if result.FailedIndex != -1 {
		t.Fatalf("FailedIndex = %d, want -1", result.FailedIndex)
	}
	if result.SpaceReused {
		t.Error("SpaceReused = true, want false -- w9 was not in the before-list")
	}
	if agentPaneID(result) != "pane-1" {
		t.Errorf("AgentAt.PaneID = %q, want %q", agentPaneID(result), "pane-1")
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

// agentPaneID is result.AgentAt.PaneID with the nil case folded in, since
// "no pane was ever claimed" and "the claimed pane is p" are the two
// answers these assertions distinguish between.
func agentPaneID(r ExecResult) string {
	if r.AgentAt == nil {
		return ""
	}
	return r.AgentAt.PaneID
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
	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

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
	if agentPaneID(result) == "stranger-pane" {
		t.Fatal("AgentAt.PaneID == the pane WorktreeCreate returned -- the whole point of the correction is to NOT trust it")
	}
	if agentPaneID(result) != "claimed-pane" {
		t.Errorf("AgentAt.PaneID = %q, want %q (the claimed tab's own pane)", agentPaneID(result), "claimed-pane")
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
	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

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
	if agentPaneID(result) != m.topo.PaneID {
		t.Errorf("AgentAt.PaneID = %q, want %q (the placement op's own pane)", agentPaneID(result), m.topo.PaneID)
	}
}

// TestExecute_AgentAtCarriesTheWholeLocationNotJustThePane is #99 at the
// layer that knows the answer. A placement op moves the agent's WORKSPACE
// and TAB as well as its pane, because placementOp always targets
// Input.Ctx: with a worktree the checkout gets a brand-new space of its
// own while `tab here` puts the agent back in the invoking workspace. A
// reader that only had the pane id could not say which tab or workspace to
// address, and the only consumer that reports an id to a caller
// (internal/create) reported the SPACE's three instead.
func TestExecute_AgentAtCarriesTheWholeLocationNotJustThePane(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	in.Placement = PlacementTabHere
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Three ids apart, all of them: the worktree's own new space versus a
	// tab in the invoking workspace. A mock sharing one topo across both
	// creations could not tell these two answers apart at all.
	m := &mockRunner{
		topo:    herdrc.CreatedTopology{WorkspaceID: "wWT", TabID: "tWT", PaneID: "pWT", CheckoutPath: "/checkout/x"},
		tabTopo: &herdrc.CreatedTopology{WorkspaceID: in.Ctx.WorkspaceID, TabID: "tHERE", PaneID: "pHERE"},
	}
	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

	if result.FailedIndex != -1 {
		t.Fatalf("FailedIndex = %d, want -1", result.FailedIndex)
	}
	if result.AgentAt == nil {
		t.Fatal("AgentAt = nil, want the placement op's own topology")
	}
	if *result.AgentAt != *m.tabTopo {
		t.Errorf("AgentAt = %+v, want %+v (the placement op's own workspace/tab/pane)", *result.AgentAt, *m.tabTopo)
	}
	// And the space is untouched by it -- the other half of the same
	// separation, which Clean depends on.
	if result.Created == nil || result.Created.WorkspaceID != "wWT" || result.Created.PaneID != "pWT" {
		t.Errorf("Created = %+v, want the worktree's own space", result.Created)
	}
}

// TestExecute_AgentAtIsTheClaimedTabWhenTheSpaceWasReused is the same
// claim for §5.2's correction, the other way the two diverge. Here the
// workspace agrees (the claim opens a tab INSIDE the reused space) and the
// tab and pane do not, which is precisely the case a pane-only AgentAt
// could half-answer and a caller could half-believe: #99's live evidence
// had the report naming another session's pane in the right workspace.
func TestExecute_AgentAtIsTheClaimedTabWhenTheSpaceWasReused(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		workspacesBeforeCreate: []herdrc.WorkspaceInfo{{WorkspaceID: "w9", Label: "somebody-else"}},
		topo:                   herdrc.CreatedTopology{WorkspaceID: "w9", TabID: "t1", PaneID: "stranger-pane", CheckoutPath: "/tmp/wt"},
		tabTopo:                &herdrc.CreatedTopology{WorkspaceID: "w9", TabID: "t2", PaneID: "claimed-pane", CheckoutPath: "/tmp/wt"},
	}
	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

	if result.FailedIndex != -1 {
		t.Fatalf("FailedIndex = %d, want -1", result.FailedIndex)
	}
	if result.AgentAt == nil {
		t.Fatal("AgentAt = nil, want the claimed tab's own topology")
	}
	if result.AgentAt.TabID != "t2" {
		t.Errorf("AgentAt.TabID = %q, want %q -- the claimed tab, not the one the reused workspace already had", result.AgentAt.TabID, "t2")
	}
	if result.AgentAt.PaneID != "claimed-pane" {
		t.Errorf("AgentAt.PaneID = %q, want %q", result.AgentAt.PaneID, "claimed-pane")
	}
	if result.AgentAt.WorkspaceID != "w9" {
		t.Errorf("AgentAt.WorkspaceID = %q, want %q -- the claim opens a tab inside the reused space", result.AgentAt.WorkspaceID, "w9")
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
	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

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
	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

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
	if agentPaneID(result) != "split-pane" {
		t.Errorf("AgentAt.PaneID = %q, want %q (the placement op's own pane)", agentPaneID(result), "split-pane")
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
	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

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
	if agentPaneID(result) != "" {
		t.Errorf("AgentAt.PaneID = %q, want empty -- no pane was ever claimed, so Clean must have nothing to close", agentPaneID(result))
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
	result := Execute(context.Background(), m, ops, ExecOpts{}, func(p Progress) { progressed = append(progressed, p) })

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

	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

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

	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

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

	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

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

	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)
	if result.FailedIndex != -1 {
		t.Fatalf("unexpected failure: %+v", result)
	}

	wantCalls := []string{
		"WorkspaceList()",
		"WorktreeCreate(/repo,zvi/fix-pagination,main)",
		"PaneRun(pane-1,clauth,start,work,--,--model,opus)",
		"AwaitDetection(pane-1," + in.DetectionTimeout.String() + ",0s)",
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
	result := Execute(context.Background(), m, ops, ExecOpts{}, func(p Progress) { progressed = append(progressed, p) })

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

	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

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

	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

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
// defect-fix scenario (the v1 close-out live checkpoint, 2026-09-01):
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

	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

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

	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

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
	result := Execute(context.Background(), m, ops, ExecOpts{}, func(p Progress) { got = append(got, p) })
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
	result := ExecResult{Created: &created, AgentAt: &created}

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
	result := ExecResult{Created: &created, AgentAt: &created}

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
	result := ExecResult{Created: &created, AgentAt: &created}

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
	result := ExecResult{Created: &created, AgentAt: &created}

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
	result := ExecResult{Created: &created, AgentAt: &created}

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
	result := ExecResult{Created: &created, AgentAt: &created}

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
	result := ExecResult{Created: &created, AgentAt: &herdrc.CreatedTopology{PaneID: "claimed-pane"}, SpaceReused: true, SpaceLabel: "somebody-else"}

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
// zero value: with PaneID == "" an implementation keying off AgentAt.PaneID !=
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
	result := ExecResult{Created: &created, AgentAt: &herdrc.CreatedTopology{PaneID: "claimed-pane"}} // != created.PaneID ("worktree-pane")

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
	result := ExecResult{Created: &created, AgentAt: &herdrc.CreatedTopology{PaneID: "p1"}} // == created.PaneID

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

	res := Execute(context.Background(), m, ops, ExecOpts{}, nil)

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

	res := Execute(context.Background(), m, ops, ExecOpts{}, nil)

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
	result := ExecResult{Created: &created, AgentAt: &created}

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

// TestExecuteDetectionTimeoutQuotesThePane is the diagnosability half of
// the live 2026-09-08 finding: an `[agents.extra_args]` value containing
// shell glob characters was rejected by zsh, the agent never started, and
// the only thing reported was that detection had timed out after 30s. The
// pane held the actual reason for the whole thirty seconds.
//
// The launch step cannot catch this on its own -- herdr's `pane run` TYPES
// into an interactive shell and reports success by exit code, which
// reflects the typing rather than the command -- so this step, already
// waiting next to the pane, is where the answer is available.
func TestExecuteDetectionTimeoutQuotesThePane(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	// Path B: build.go emits OpAwaitDetection only for an account-pinned
	// launch, which is also the path the live failure was on -- the glob
	// characters were in extra_args on a `clauth start` command line.
	in.AccountPin = "personal"
	in.ExtraArgs = []string{"--model", "claude-opus-5[1m]"}
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		failAt:    "AwaitDetection",
		failErr:   errors.New("await detection for pane pane-1: timed out after 30.001s"),
		failCount: 1,
		topo:      herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
		readText: "❯ clauth start personal -- --model claude-opus-5[1m] --effort xhigh\n" +
			"zsh: no matches found: claude-opus-5[1m]\n" +
			"❯ \n\n\n",
	}

	var progressed []Progress
	result := Execute(context.Background(), m, ops, ExecOpts{}, func(p Progress) { progressed = append(progressed, p) })

	if result.FailedIndex == -1 {
		t.Fatalf("Execute succeeded, want the detection op to fail: %+v", result)
	}
	last := progressed[len(progressed)-1]
	if last.State != StepFailed {
		t.Fatalf("last progress = %+v, want StepFailed", last)
	}
	msg := last.Err.Error()

	if !strings.Contains(msg, "no matches found") {
		t.Errorf("detection failure = %q, want it to quote the shell's rejection from the pane", msg)
	}
	// The timeout is still the failure; the pane text only enriches it.
	if !errors.Is(last.Err, m.failErr) {
		t.Errorf("detection failure = %q, want the original timeout still wrapped", msg)
	}
	// A screen dump with embedded newlines would break the failure
	// screen's own row budget.
	if strings.ContainsAny(msg, "\n\r") {
		t.Errorf("detection failure = %q, want a single line", msg)
	}
}

// TestExecuteDetectionTimeoutSurvivesAnUnreadablePane keeps the enrichment
// best-effort. The timeout is the real failure; replacing it with a
// complaint about not being able to fetch a diagnosis would be strictly
// worse than the message this set out to improve.
func TestExecuteDetectionTimeoutSurvivesAnUnreadablePane(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	in.AccountPin = "personal"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	timeout := errors.New("await detection for pane pane-1: timed out after 30.001s")

	for _, tc := range []struct {
		name string
		// readText and blankReadsFirst are two ways to spell an
		// uninformative pane, and both have to reach withPaneTail: a
		// screen of whitespace, and a read that succeeds with nothing in
		// it at all. The second cannot be spelled with readText any more,
		// since the fake's zero value is a painted screen (#116).
		readText        string
		blankReadsFirst int
	}{
		{name: "the pane is blank", readText: "\n   \n\n"},
		{name: "the pane is empty", blankReadsFirst: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &mockRunner{
				failAt:          "AwaitDetection",
				failErr:         timeout,
				failCount:       1,
				topo:            herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
				readText:        tc.readText,
				blankReadsFirst: tc.blankReadsFirst,
			}

			var progressed []Progress
			result := Execute(context.Background(), m, ops, ExecOpts{}, func(p Progress) { progressed = append(progressed, p) })
			if result.FailedIndex == -1 {
				t.Fatal("Execute succeeded, want the detection op to fail")
			}
			last := progressed[len(progressed)-1]
			if !errors.Is(last.Err, timeout) {
				t.Errorf("detection failure = %v, want the original timeout intact", last.Err)
			}
			if strings.Contains(last.Err.Error(), "the pane shows") {
				t.Errorf("detection failure = %q, want no empty pane quote appended", last.Err.Error())
			}
		})
	}
}

// --- #90: a start herdr refused because of a blocking dialog --------------

// notReadyErr is the failure `herdr agent start` produces on 0.9.0 when its
// readiness poll finds the agent blocked, as herdrc.CLIRunner surfaces it:
// the subcommand named, then herdr's own JSON error envelope, which
// print_response (herdr:src/cli.rs:738) writes to stderr before exiting 1
// and cmdError folds into the message (`herdr %s: %w: %s`).
//
// Verbatim, character for character, from a live 0.9.0 run against a fresh
// worktree on 2026-09-08 -- including the `"id"` member that trails the
// error object, which #90's own transcript had trimmed. The exactness is
// the point: isAgentNotReadyError matches this by SUBSTRING, because herdr
// error codes reach Go inside an error string rather than as a typed value,
// so a convenient `errors.New("agent_not_ready")` would satisfy the match
// while proving nothing about the shape the real CLI hands over.
func notReadyErr(agentName string) error {
	return fmt.Errorf("herdr agent start %s --kind claude --pane w2:p1: exit status 1: "+
		`{"error":{"code":"agent_not_ready","message":"agent %s is blocked during startup and is not ready for prompts"},"id":"cli:agent:start"}`,
		agentName, agentName)
}

// trustDialogScreen is Claude Code's first-run trust prompt as `agent read
// --source detection` renders it -- the same screen dialog_test.go's
// fixture preserves, re-observed live on herdr 0.9.0 while filing #90.
const trustDialogScreen = "Quick safety check: Is this a project you created or one you trust?\n\n" +
	"❯ No, exit\n  Yes, I trust this folder\n\nEnter to confirm · Esc to cancel\n"

// TestExecuteBlockedStartIsExplained is #90's core scenario. herdr 0.9.0
// detects the first-run trust prompt and reports the agent `blocked`, so
// `agent start` refuses with agent_not_ready -- and since every freshly
// created worktree is a directory Claude Code has never been trusted in,
// Path A's step 2 now fails routinely on a session that is in fact healthy
// and one keystroke from ready.
//
// What the step must say is what the user can do about it, FIRST, because
// SubmitView truncates a step's value at the head.
func TestExecuteBlockedStartIsExplained(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	in.Prompt = "implement the fix"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		failAt:    "AgentStart",
		failErr:   notReadyErr("fix-pagination"),
		failCount: 1,
		topo:      herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
		readText:  trustDialogScreen,
	}

	var progressed []Progress
	result := Execute(context.Background(), m, ops, ExecOpts{}, func(p Progress) { progressed = append(progressed, p) })

	if result.FailedIndex != 1 {
		t.Fatalf("FailedIndex = %d, want 1 (the agent start op): %+v", result.FailedIndex, result)
	}
	if !containsCall(m.calls, "AgentRead(pane-1)") {
		t.Fatalf("calls = %v, want the agent pane read to find out WHY the start was refused", m.calls)
	}

	last := progressed[len(progressed)-1]
	if last.State != StepFailed {
		t.Fatalf("last progress = %+v, want StepFailed", last)
	}
	msg := last.Err.Error()

	// The instruction, and the agent named -- not "starting agent failed".
	for _, want := range []string{"claude started", "answer the dialog in the pane", "keep this session"} {
		if !strings.Contains(msg, want) {
			t.Errorf("blocked start = %q, want it to contain %q", msg, want)
		}
	}
	// The dialog it matched, so the message is checkable against the screen.
	if !strings.Contains(msg, "Quick safety check") {
		t.Errorf("blocked start = %q, want it to name the dialog signature it matched", msg)
	}
	// herdr's own error is still in there for `create --json` and stderr.
	if !errors.Is(last.Err, m.failErr) {
		t.Errorf("blocked start = %q, want herdr's own agent_not_ready error still wrapped", msg)
	}
	if strings.ContainsAny(msg, "\n\r") {
		t.Errorf("blocked start = %q, want a single line -- the failure screen budgets its rows", msg)
	}

	// The prompt never reached the agent, so it must come back for paste.
	if result.PromptText != in.Prompt {
		t.Errorf("PromptText = %q, want %q -- the step-2 failure must not eat the prompt", result.PromptText, in.Prompt)
	}
}

// TestExplainBlockedStartLeadsWithTheAction pins the one property of this
// message that the popup can take away: SubmitView.stepValue truncates a
// step's value at the head (v2 spec §7), and at an 80-cell popup that
// value column is around sixty cells. So the instruction has to come
// before the evidence -- the natural English order ("agent X is blocked
// because ...: do Y") puts the actionable half exactly where it gets cut.
//
// Asserted here rather than through Execute because Execute wraps the
// error as `plan: execute: <label>: ...` and internal/app strips that
// prefix back off (async.go's execErrorPrefix) before rendering: measuring
// the rendered head through Execute would mean re-implementing the app's
// stripping inside a plan test, against the wrong string.
func TestExplainBlockedStartLeadsWithTheAction(t *testing.T) {
	m := &mockRunner{readText: trustDialogScreen}
	err := explainBlockedStart(context.Background(), m, "claude", "pane-1", notReadyErr("fix-pagination"))

	const rendered = 60 // roughly what an 80-cell popup gives the value column
	head := firstRunes(err.Error(), rendered)
	if !strings.Contains(head, "answer the dialog") {
		t.Errorf("first %d runes = %q, want the instruction still visible there", rendered, head)
	}
}

// TestExecuteBlockedStartQuotesAnUnrecognizedScreen covers the other reason
// `agent start` reports agent_not_ready: some screen dialog.go has never
// heard of. Quoting the pane is the same service a detection timeout gets,
// and is strictly better than handing back a bare error code -- herdr's
// detection manifest knows one dialog, not every dialog.
func TestExecuteBlockedStartQuotesAnUnrecognizedScreen(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		failAt:    "AgentStart",
		failErr:   notReadyErr("fix-pagination"),
		failCount: 1,
		topo:      herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
		readText:  "Select login method:\n\n❯ Claude account with subscription\n  Anthropic Console account\n",
	}

	var progressed []Progress
	Execute(context.Background(), m, ops, ExecOpts{}, func(p Progress) { progressed = append(progressed, p) })

	msg := progressed[len(progressed)-1].Err.Error()
	if !strings.Contains(msg, "Anthropic Console account") {
		t.Errorf("blocked start = %q, want the unrecognized screen quoted", msg)
	}
	if strings.Contains(msg, "answer the dialog in the pane") {
		t.Errorf("blocked start = %q, want NO trust-prompt instruction for a screen dialog.go did not match", msg)
	}
	if strings.ContainsAny(msg, "\n\r") {
		t.Errorf("blocked start = %q, want a single line", msg)
	}
}

// TestExecuteBlockedStartSurvivesAnUnreadablePane keeps the explanation
// best-effort, exactly as withPaneTail is: herdr's refusal is the real
// failure, and replacing it with a complaint about not being able to read
// the pane would be strictly worse than the message this set out to
// improve.
func TestExecuteBlockedStartSurvivesAnUnreadablePane(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	refused := notReadyErr("fix-pagination")

	m := &mockRunner{
		failAt:    "AgentStart",
		failErr:   refused,
		failCount: 1,
		topo:      herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
		readErr:   errors.New("agent target pane-1 not found"),
	}

	var progressed []Progress
	Execute(context.Background(), m, ops, ExecOpts{}, func(p Progress) { progressed = append(progressed, p) })

	last := progressed[len(progressed)-1]
	if !errors.Is(last.Err, refused) {
		t.Errorf("blocked start = %v, want herdr's own refusal intact", last.Err)
	}
	if strings.Contains(last.Err.Error(), "pane-1 not found") {
		t.Errorf("blocked start = %q, want the READ's failure kept out of it -- it is not the diagnosis", last.Err.Error())
	}
}

// TestExecuteAgentStartFailureLeavesOtherErrorsAlone is the scope guard.
// Only agent_not_ready means "blocked on a screen worth reading"; every
// other start failure -- a name herdr will not accept, an exhausted dedupe
// loop, a busy pane -- has its answer in the error itself, and reading the
// pane for it would add a herdr round-trip and a paragraph of irrelevant
// terminal output to a message that was already correct.
func TestExecuteAgentStartFailureLeavesOtherErrorsAlone(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	refused := errors.New(`herdr agent start x --kind claude: exit status 1: ` +
		`{"error":{"code":"agent_start_failed","message":"agent process exited before becoming interactive"}}`)

	m := &mockRunner{
		failAt:    "AgentStart",
		failErr:   refused,
		failCount: 1,
		topo:      herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
		readText:  trustDialogScreen, // present, and must go unread
	}

	var progressed []Progress
	Execute(context.Background(), m, ops, ExecOpts{}, func(p Progress) { progressed = append(progressed, p) })

	if containsCall(m.calls, "AgentRead(pane-1)") {
		t.Errorf("calls = %v, want NO pane read for a failure that is not agent_not_ready", m.calls)
	}
	msg := progressed[len(progressed)-1].Err.Error()
	if strings.Contains(msg, "Quick safety check") || strings.Contains(msg, "answer the dialog") {
		t.Errorf("start failure = %q, want herdr's own error unembellished", msg)
	}
}

// TestExecuteUnsentPromptSurvivesAFailureBeforeThePromptOp pins the
// generalisation #90 forced. PromptText used to be set only when
// OpAgentPrompt was itself the failing op, so a plan that stopped earlier
// destroyed the prompt outright: nothing saved it for paste, and
// `create --json` derived `prompt_sent: true` from it being empty. Every
// step before the prompt op is a step that can now do that.
func TestExecuteUnsentPromptSurvivesAFailureBeforeThePromptOp(t *testing.T) {
	for _, tc := range []struct {
		name   string
		failAt string
	}{
		{"the worktree is never created", "WorktreeCreate"},
		{"the agent never starts", "AgentStart"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := validInput()
			in.UseWorktree = true
			in.Prompt = "implement the fix\n\nsecond paragraph"
			ops, err := Build(in)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}

			m := &mockRunner{
				failAt:    tc.failAt,
				failErr:   errors.New("herdr said no"),
				failCount: 1,
				topo:      herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
			}

			result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

			if result.FailedIndex == -1 {
				t.Fatalf("Execute succeeded, want %s to fail", tc.failAt)
			}
			if result.PromptText != in.Prompt {
				t.Fatalf("PromptText = %q, want %q -- a prompt that never reached the agent must come back", result.PromptText, in.Prompt)
			}
		})
	}
}

// TestExecuteSuccessLeavesPromptTextEmpty is the other half of that
// contract, and the one `create --json`'s `prompt_sent` actually reads:
// PromptText means "the prompt did not land", so a plan that ran to the end
// must leave it empty however long the prompt was.
func TestExecuteSuccessLeavesPromptTextEmpty(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	in.Prompt = "implement the fix"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{topo: herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"}}
	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

	if result.FailedIndex != -1 {
		t.Fatalf("Execute failed, want success: %+v", result)
	}
	if result.PromptText != "" {
		t.Fatalf("PromptText = %q, want empty -- the prompt was sent", result.PromptText)
	}
}

// firstRunes returns the first n runes of s, for asserting on what survives
// a head-keeping truncation.
func firstRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}

// --- #94: Path B must not prompt an agent that is not ready ---------------

// TestExecutePathBBlockedDetectionNeverPrompts is #94's regression test, and
// the assertion that matters is the negative one: AgentPrompt must not be
// called.
//
// Live on 0.9.0, Path B into a fresh worktree destroyed the agent it had
// just launched. `OpAwaitDetection` was satisfied by `agent get` merely
// exiting zero, which happens ~450ms before Claude Code paints its
// first-run trust screen; promptIfReady then read a screen with no dialog
// on it yet, sent, and the prompt's trailing Enter confirmed the dialog's
// highlighted "No, exit". Detection now means READY, so the plan stops here
// with something the user can act on and the prompt op is never reached.
func TestExecutePathBBlockedDetectionNeverPrompts(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	in.AccountPin = "personal" // Path B: clauth launch + explicit detection wait
	in.Prompt = "implement the fix"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		failAt:    "AwaitDetection",
		failErr:   fmt.Errorf("await detection for pane pane-1: %w", herdrc.ErrAgentBlocked),
		failCount: 1,
		topo:      herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
		readText:  trustDialogScreen,
	}

	var progressed []Progress
	result := Execute(context.Background(), m, ops, ExecOpts{}, func(p Progress) { progressed = append(progressed, p) })

	if containsCall(m.calls, "AgentPrompt(pane-1,implement the fix)") {
		t.Fatalf("calls = %v, want NO AgentPrompt -- this is the call that killed the agent", m.calls)
	}
	if result.FailedIndex != 2 {
		t.Fatalf("FailedIndex = %d, want 2 (the detection op): %+v", result.FailedIndex, result)
	}

	msg := progressed[len(progressed)-1].Err.Error()
	for _, want := range []string{"claude started", "answer the dialog in the pane", "Quick safety check"} {
		if !strings.Contains(msg, want) {
			t.Errorf("blocked detection = %q, want it to contain %q", msg, want)
		}
	}
	// Path A and Path B must not describe the same situation two ways.
	if strings.Contains(msg, "timed out") {
		t.Errorf("blocked detection = %q, want the instruction rather than a timeout", msg)
	}
	if result.PromptText != in.Prompt {
		t.Errorf("PromptText = %q, want %q -- the prompt must survive for paste", result.PromptText, in.Prompt)
	}
}

// TestExecutePathBDetectionTimeoutStillQuotesThePane guards the branch the
// blocked case now sits beside: an ordinary timeout is NOT a blocked agent
// and must keep the pane-quoting treatment PR #89 added, rather than being
// swept into the dialog explanation.
func TestExecutePathBDetectionTimeoutStillQuotesThePane(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	in.AccountPin = "personal"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		failAt:    "AwaitDetection",
		failErr:   errors.New("await detection for pane pane-1: timed out after 30.001s"),
		failCount: 1,
		topo:      herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
		readText:  "❯ clauth start personal --\nzsh: command not found: clauth\n❯ \n",
	}

	var progressed []Progress
	Execute(context.Background(), m, ops, ExecOpts{}, func(p Progress) { progressed = append(progressed, p) })

	msg := progressed[len(progressed)-1].Err.Error()
	if !strings.Contains(msg, "command not found") {
		t.Errorf("detection timeout = %q, want the pane quoted", msg)
	}
	if strings.Contains(msg, "answer the dialog") {
		t.Errorf("detection timeout = %q, want NO dialog instruction for a plain timeout", msg)
	}
}

// TestBuildPathBCarriesTheAgentKindForMessages pins the field the blocked
// explanation names the agent from. Path B is claude-only today, so
// hardcoding "claude" in exec.go would read correctly and be right by
// coincidence; this is what makes it right on purpose.
func TestBuildPathBCarriesTheAgentKindForMessages(t *testing.T) {
	in := validInput()
	in.AccountPin = "personal"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, op := range ops {
		if op.Kind == OpAwaitDetection {
			if op.AgentKind != in.AgentKind {
				t.Fatalf("OpAwaitDetection.AgentKind = %q, want %q", op.AgentKind, in.AgentKind)
			}
			return
		}
	}
	t.Fatal("Build emitted no OpAwaitDetection for an account-pinned launch")
}

// TestExecutePromptWaitTimeoutIsUnconfirmed is #108's core: herdr's
// `agent prompt --wait` timing out means the CONFIRMATION timed out, not
// that the prompt failed to arrive, so the result has to say "unconfirmed"
// rather than assert a delivery failure it cannot know about.
//
// The step still fails -- exit 1 and a visible failed step, because the
// prompt genuinely may not have landed -- but PromptUnconfirmed is what
// stops the two harms the issue names: text offered back as `unsent`
// (inviting a double-paste into a working agent) and `--on-failure clean`
// tearing down a session that is mid-turn.
func TestExecutePromptWaitTimeoutIsUnconfirmed(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	in.Prompt = "implement the thing"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		failAt: "AgentPrompt",
		// Shaped like the real thing: herdrc wraps its sentinel around
		// the CLI error, so callers see both.
		failErr:   fmt.Errorf("%w: herdr agent prompt w1:p1 ...", herdrc.ErrPromptWaitTimeout),
		failCount: 1,
		topo:      herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
	}

	var last Progress
	result := Execute(context.Background(), m, ops, ExecOpts{}, func(p Progress) {
		if p.State == StepFailed {
			last = p
		}
	})

	if !result.PromptUnconfirmed {
		t.Error("PromptUnconfirmed = false, want true -- a wait timeout cannot prove the prompt was not delivered")
	}
	if result.FailedIndex != 2 {
		t.Errorf("FailedIndex = %d, want 2 -- the step still failed", result.FailedIndex)
	}
	// The text is still carried: losing the caller's prompt is the worse
	// error of the two, so it survives and only its LABEL changes.
	if result.PromptText != in.Prompt {
		t.Errorf("PromptText = %q, want %q", result.PromptText, in.Prompt)
	}
	if last.Err == nil {
		t.Fatal("no failed-step progress reported")
	}
	// The message a user acts on must not claim the prompt was not sent,
	// and must say what to do instead of resending blindly.
	msg := last.Err.Error()
	if strings.Contains(msg, "not sent") {
		t.Errorf("failure message claims the prompt was not sent:\n%s", msg)
	}
	if !strings.Contains(msg, "read the pane") {
		t.Errorf("failure message does not tell the user to read the pane:\n%s", msg)
	}
	// errors.Is must survive the rewording, or nothing downstream can
	// classify it either.
	if !errors.Is(last.Err, herdrc.ErrPromptWaitTimeout) {
		t.Errorf("reworded failure no longer matches ErrPromptWaitTimeout:\n%s", msg)
	}
}

// TestExecuteOtherPromptFailureIsNotUnconfirmed is the negative half: the
// unconfirmed state is reserved for the wait timeout, and every other
// prompt failure keeps meaning exactly what it meant before -- including
// the dialog guard's deliberate refusal, where the text really was never
// typed and offering it back is the correct behaviour.
func TestExecuteOtherPromptFailureIsNotUnconfirmed(t *testing.T) {
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

	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

	if result.PromptUnconfirmed {
		t.Error("PromptUnconfirmed = true for a non-timeout prompt failure; the unconfirmed " +
			"state must not swallow failures that really did not deliver")
	}
	if result.PromptText != in.Prompt {
		t.Errorf("PromptText = %q, want %q", result.PromptText, in.Prompt)
	}
}

// TestCleanCheckRefusesWhenPromptUnconfirmed pins the second harm shut at
// the chokepoint both callers share. `--on-failure clean` and the form's
// keep-or-clean gate both go through CleanCheck, so refusing here covers
// both without either having to remember to.
//
// It is checked BEFORE the reuse and worktree branches deliberately: those
// ask "is this space safe to remove", and this asks "might something be
// running in it right now", which outranks them.
func TestCleanCheckRefusesWhenPromptUnconfirmed(t *testing.T) {
	in := validInput()
	in.UseWorktree = false // the branch that would otherwise allow a clean

	result := ExecResult{
		FailedIndex:       2,
		PromptUnconfirmed: true,
		Created:           &herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
	}

	decision := CleanCheck(context.Background(), in, result)
	if decision.Allowed {
		t.Fatal("CleanCheck allowed a clean while prompt delivery was unconfirmed -- " +
			"this is the case that tears down a healthy agent mid-turn")
	}
	if !strings.Contains(decision.Reason, "may already have been delivered") {
		t.Errorf("refusal reason does not explain why:\n%s", decision.Reason)
	}
}

// TestCleanCheckStillAllowsWithoutUnconfirmedPrompt is the control: the
// new gate must not have made every clean a refusal.
func TestCleanCheckStillAllowsWithoutUnconfirmedPrompt(t *testing.T) {
	in := validInput()
	in.UseWorktree = false

	decision := CleanCheck(context.Background(), in, ExecResult{FailedIndex: 1})
	if !decision.Allowed {
		t.Fatalf("CleanCheck refused an ordinary no-worktree clean: %s", decision.Reason)
	}
}

// --- #115: waiting through the trust dialog instead of failing on it -----
//
// Every worktree is a directory Claude Code has never been trusted in, so
// its first-run trust prompt is not an edge case but the ordinary first-run
// path -- and until #115 it ended a submit at step 2 with the user's
// composed prompt handed back for manual paste. The agent survives herdr's
// refusal and stays addressable (recorded live, docs/manual-smoke.md's Cell
// 8), so the fix is a poll and never a re-launch.
//
// The two launch paths learn "blocked" differently and the tests below pin
// both, because the wait has to look identical from the user's side: Path A
// is told by `agent start`'s own agent_not_ready refusal, Path B by
// AwaitDetection's ErrAgentBlocked sentinel.

// stillBlockedErr is what herdrc.AwaitDetection returns when its blocked
// budget runs out with the agent still sitting on the dialog -- a timeout
// that still wraps ErrAgentBlocked, so plan's own classifier keeps firing
// and the failure keeps saying the thing the user can act on.
func stillBlockedErr() error {
	return fmt.Errorf("await detection for pane pane-1: timed out after 5m0s: %w", herdrc.ErrAgentBlocked)
}

// clauthInput is Path B: a pinned claude account, which launches through
// clauth and is therefore followed by an explicit OpAwaitDetection rather
// than relying on `agent start`'s own server-side wait.
func clauthInput() Input {
	in := validInput()
	in.UseWorktree = true
	in.Prompt = "implement the fix"
	in.AccountPin = "personal"
	in.AgentKind = "claude"
	in.DetectionTimeout = 30 * time.Second
	return in
}

// TestExecuteWaitsThroughABlockedStart is #115's Path A: `agent start` is
// refused with agent_not_ready, and instead of failing the submit the
// pipeline waits for the person to answer the dialog, then sends the prompt
// it was already holding. Zero keypresses in the popup.
func TestExecuteWaitsThroughABlockedStart(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	in.Prompt = "implement the fix"
	in.DetectionTimeout = 30 * time.Second
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		failAt:    "AgentStart",
		failErr:   notReadyErr("fix-pagination"),
		failCount: 1,
		topo:      herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
		readText:  trustDialogScreen,

		readTextClearsOnReady: true,
	}

	var progressed []Progress
	result := Execute(context.Background(), m, ops, ExecOpts{TrustWait: 5 * time.Minute},
		func(p Progress) { progressed = append(progressed, p) })

	if result.FailedIndex != -1 {
		t.Fatalf("FailedIndex = %d, want -1: a blocked start is a wait, not a failure: %+v", result.FailedIndex, result)
	}
	if result.PromptText != "" {
		t.Errorf("PromptText = %q, want empty -- the prompt was sent, not handed back", result.PromptText)
	}
	if !containsCall(m.calls, "AgentPrompt(pane-1,implement the fix)") {
		t.Fatalf("calls = %v, want the prompt sent once the agent came ready", m.calls)
	}
	// The wait is a poll against the SAME pane, with the trust budget --
	// never a second `agent start`.
	if !containsCall(m.calls, "AwaitDetection(pane-1,30s,5m0s)") {
		t.Fatalf("calls = %v, want a poll carrying the trust budget", m.calls)
	}
	if starts := countCallsWithPrefix(m.calls, "AgentStart"); starts != 1 {
		t.Errorf("AgentStart called %d times, want 1 -- the agent survived the refusal and must not be relaunched", starts)
	}
}

// TestExecuteReportsTheWaitToTheUser: a wait nobody can see is a hang. The
// step goes to a state of its own -- neither running nor failed -- so the
// popup can say whose turn it is.
func TestExecuteReportsTheWaitToTheUser(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	in.DetectionTimeout = 30 * time.Second
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		failAt:    "AgentStart",
		failErr:   notReadyErr("fix-pagination"),
		failCount: 1,
		topo:      herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
		readText:  trustDialogScreen,

		readTextClearsOnReady: true,
	}

	var progressed []Progress
	Execute(context.Background(), m, ops, ExecOpts{TrustWait: 5 * time.Minute},
		func(p Progress) { progressed = append(progressed, p) })

	var waiting []Progress
	for _, p := range progressed {
		if p.State == StepWaiting {
			waiting = append(waiting, p)
		}
	}
	if len(waiting) != 1 {
		t.Fatalf("got %d StepWaiting events, want exactly 1: %+v", len(waiting), progressed)
	}
	agentStep := 1
	if waiting[0].Index != agentStep {
		t.Errorf("StepWaiting reported for step %d, want the launch step %d", waiting[0].Index, agentStep)
	}
	// And it settles: the same step must end Done, or the row would sit on
	// "waiting" forever after the dialog was answered.
	last := progressed[len(progressed)-1]
	if last.State != StepDone {
		t.Errorf("last progress = %+v, want StepDone once the whole plan ran", last)
	}
}

// TestExecuteWaitsThroughABlockedDetection is Path B, where the blocked
// verdict arrives as herdrc's own sentinel rather than as `agent start`'s
// refusal. Same wait, same outcome -- the difference between the paths must
// not reach the user.
func TestExecuteWaitsThroughABlockedDetection(t *testing.T) {
	ops, err := Build(clauthInput())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		failAt:    "AwaitDetection",
		failErr:   fmt.Errorf("await detection for pane pane-1: %w", herdrc.ErrAgentBlocked),
		failCount: 1,
		topo:      herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
		readText:  trustDialogScreen,

		readTextClearsOnReady: true,
	}

	result := Execute(context.Background(), m, ops, ExecOpts{TrustWait: 5 * time.Minute}, nil)

	if result.FailedIndex != -1 {
		t.Fatalf("FailedIndex = %d, want -1: %+v", result.FailedIndex, result)
	}
	// The FIRST poll deliberately carries no blocked budget, so blocked
	// comes back as a verdict this layer can act on: that is what lets Path
	// B report StepWaiting at the same moment Path A does. Absorbing it
	// inside herdrc instead would still deliver the prompt, and would leave
	// the user watching a row that says "working…" for five minutes.
	if !containsCall(m.calls, "AwaitDetection(pane-1,30s,0s)") {
		t.Fatalf("calls = %v, want the first poll to carry NO blocked budget", m.calls)
	}
	if !containsCall(m.calls, "AwaitDetection(pane-1,30s,5m0s)") {
		t.Fatalf("calls = %v, want the second poll to carry the trust budget", m.calls)
	}
	if !containsCall(m.calls, "AgentPrompt(pane-1,implement the fix)") {
		t.Errorf("calls = %v, want the prompt sent once the agent came ready", m.calls)
	}
}

// TestExecuteWithoutATrustBudgetFailsFastOnABlockedStart is decision 2 of
// the issue, and it is what headless `create` relies on: a script has
// nobody at the keyboard, so waiting for a dialog nobody will answer is
// worse than failing with the reason. A zero budget must leave the old
// behaviour untouched, down to not polling at all.
func TestExecuteWithoutATrustBudgetFailsFastOnABlockedStart(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	in.Prompt = "implement the fix"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		failAt:    "AgentStart",
		failErr:   notReadyErr("fix-pagination"),
		failCount: 1,
		topo:      herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
		readText:  trustDialogScreen,
	}

	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

	if result.FailedIndex != 1 {
		t.Fatalf("FailedIndex = %d, want 1 (the agent start op): %+v", result.FailedIndex, result)
	}
	if countCallsWithPrefix(m.calls, "AwaitDetection") != 0 {
		t.Errorf("calls = %v, want NO poll at all: with no budget there is nothing to wait for", m.calls)
	}
	if result.PromptText != in.Prompt {
		t.Errorf("PromptText = %q, want the prompt handed back for paste", result.PromptText)
	}
}

// TestExecuteFailsWhenTheDialogIsNeverAnswered: the budget runs out with
// the agent still on the dialog. That falls back to exactly today's
// behaviour -- the failure screen, the saved prompt, the keep-or-clean gate
// -- and the message must still lead with what to do, since SubmitView
// truncates a step's value keeping the head.
func TestExecuteFailsWhenTheDialogIsNeverAnswered(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	in.Prompt = "implement the fix"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		failAt:    "AgentStart",
		failErr:   notReadyErr("fix-pagination"),
		failCount: 1,
		awaitErr:  stillBlockedErr(),
		topo:      herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
		readText:  trustDialogScreen,
	}

	var progressed []Progress
	result := Execute(context.Background(), m, ops, ExecOpts{TrustWait: 5 * time.Minute},
		func(p Progress) { progressed = append(progressed, p) })

	if result.FailedIndex != 1 {
		t.Fatalf("FailedIndex = %d, want 1 (the agent start op): %+v", result.FailedIndex, result)
	}
	if result.PromptText != in.Prompt {
		t.Errorf("PromptText = %q, want the prompt handed back for paste", result.PromptText)
	}
	last := progressed[len(progressed)-1]
	if last.State != StepFailed {
		t.Fatalf("last progress = %+v, want StepFailed", last)
	}
	msg := last.Err.Error()
	for _, want := range []string{"answer the dialog in the pane", "Quick safety check"} {
		if !strings.Contains(msg, want) {
			t.Errorf("unanswered dialog = %q, want it to contain %q", msg, want)
		}
	}
	if strings.ContainsAny(msg, "\n\r") {
		t.Errorf("unanswered dialog = %q, want a single line", msg)
	}
}

// TestExecuteFailsWhenTheAgentIsGoneFromTheDialog is the declined dialog:
// "No, exit" ends Claude Code, and herdrc reports the transition as
// ErrAgentGone rather than burning the whole trust budget on a session that
// no longer exists. What the step must say is that the agent is gone --
// "answer the dialog in the pane" would be an instruction about a pane with
// nothing in it.
func TestExecuteFailsWhenTheAgentIsGoneFromTheDialog(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	in.Prompt = "implement the fix"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		failAt:    "AgentStart",
		failErr:   notReadyErr("fix-pagination"),
		failCount: 1,
		awaitErr:  fmt.Errorf("await detection for pane pane-1: %w", herdrc.ErrAgentGone),
		topo:      herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
		readText:  trustDialogScreen,
	}

	var progressed []Progress
	result := Execute(context.Background(), m, ops, ExecOpts{TrustWait: 5 * time.Minute},
		func(p Progress) { progressed = append(progressed, p) })

	if result.FailedIndex != 1 {
		t.Fatalf("FailedIndex = %d, want 1: %+v", result.FailedIndex, result)
	}
	msg := progressed[len(progressed)-1].Err.Error()
	if !strings.Contains(msg, "no longer running") {
		t.Errorf("gone agent = %q, want it to say the agent is no longer running", msg)
	}
	if strings.Contains(msg, "answer the dialog in the pane") {
		t.Errorf("gone agent = %q, want no instruction to answer a dialog that is not there", msg)
	}
	if result.PromptText != in.Prompt {
		t.Errorf("PromptText = %q, want the prompt handed back for paste", result.PromptText)
	}
}

// TestExecuteGoneAgentReadsTheSameOnBothPaths: the two launch paths learn
// that the agent went away by different routes and must not say so
// differently. Path B's ordinary detection failure gets the pane appended
// (withPaneTail, which is how an unexplained timeout earns its diagnosis),
// and a gone agent must be excluded from that: the pane it would quote is a
// shell prompt, and the message already says what happened.
func TestExecuteGoneAgentReadsTheSameOnBothPaths(t *testing.T) {
	ops, err := Build(clauthInput())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		failAt:    "AwaitDetection",
		failErr:   fmt.Errorf("await detection for pane pane-1: %w", herdrc.ErrAgentBlocked),
		failCount: 1,
		awaitErr:  fmt.Errorf("await detection for pane pane-1: %w", herdrc.ErrAgentGone),
		topo:      herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
		readText:  "❯ ",
	}

	var progressed []Progress
	result := Execute(context.Background(), m, ops, ExecOpts{TrustWait: 5 * time.Minute},
		func(p Progress) { progressed = append(progressed, p) })

	if result.FailedIndex != 2 {
		t.Fatalf("FailedIndex = %d, want 2 (the detection op): %+v", result.FailedIndex, result)
	}
	msg := progressed[len(progressed)-1].Err.Error()
	if !strings.Contains(msg, "no longer running") {
		t.Errorf("gone agent = %q, want it to say the agent is no longer running", msg)
	}
	if strings.Contains(msg, "the pane shows") {
		t.Errorf("gone agent = %q, want no pane quoted: it is a shell prompt, and Path A quotes nothing here either", msg)
	}
}

// --- #115, second trigger: the dialog that stops the PROMPT step ---------
//
// Live on herdr 0.9.0, `agent start` frequently returns ok while the trust
// dialog is up -- herdr's readiness verdict lands before it classifies that
// screen -- so the popup's refusal arrives one step later, from
// promptIfReady's own guard, and the launch-step wait above never runs.
// That is the path a user actually meets (docs/manual-smoke.md, 2026-09-09),
// so it waits too. The signal here is the SCREEN rather than herdr's status:
// herdr is the thing that was wrong about this pane, and dialog.go's
// signatures are the evidence promptIfReady already trusts.

// TestExecuteWaitsForThePromptDialogToClear: the guard refuses, the person
// answers, and the prompt goes out by itself.
func TestExecuteWaitsForThePromptDialogToClear(t *testing.T) {
	withDialogPollInterval(t, 0)
	in := validInput()
	in.UseWorktree = true
	in.Prompt = "implement the fix"
	in.DetectionTimeout = 30 * time.Second
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		topo:                herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
		readText:            trustDialogScreen,
		readTextClearsAfter: 3,
	}

	var progressed []Progress
	result := Execute(context.Background(), m, ops, ExecOpts{TrustWait: 5 * time.Minute},
		func(p Progress) { progressed = append(progressed, p) })

	if result.FailedIndex != -1 {
		t.Fatalf("FailedIndex = %d, want -1: the dialog cleared and the prompt should have gone out: %+v",
			result.FailedIndex, result)
	}
	if !containsCall(m.calls, "AgentPrompt(pane-1,implement the fix)") {
		t.Fatalf("calls = %v, want the prompt sent once the dialog cleared", m.calls)
	}
	// Once the screen is clear it defers to herdr for readiness, under the
	// real detection budget and with NO blocked budget: the dialog is gone,
	// so a blocked verdict now means a different dialog and a real refusal.
	if !containsCall(m.calls, "AwaitDetection(pane-1,30s,0s)") {
		t.Fatalf("calls = %v, want a readiness check carrying the detection budget", m.calls)
	}
	if result.PromptText != "" {
		t.Errorf("PromptText = %q, want empty", result.PromptText)
	}
	var waiting int
	for _, p := range progressed {
		if p.State == StepWaiting {
			waiting++
		}
	}
	if waiting != 1 {
		t.Errorf("got %d StepWaiting events, want exactly 1: %+v", waiting, progressed)
	}
}

// TestExecuteWithoutATrustBudgetDoesNotWaitForThePromptDialog: decision 2
// again, one step further down. Headless `create` must still get today's
// refusal the moment the guard matches.
func TestExecuteWithoutATrustBudgetDoesNotWaitForThePromptDialog(t *testing.T) {
	withDialogPollInterval(t, 0)
	in := validInput()
	in.UseWorktree = true
	in.Prompt = "implement the fix"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		topo:                herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
		readText:            trustDialogScreen,
		readTextClearsAfter: 1,
	}

	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

	if result.FailedIndex != 2 {
		t.Fatalf("FailedIndex = %d, want 2 (the prompt op): %+v", result.FailedIndex, result)
	}
	if countCallsWithPrefix(m.calls, "AgentPrompt") != 0 {
		t.Errorf("calls = %v, want no prompt sent at all", m.calls)
	}
	if result.PromptText != in.Prompt {
		t.Errorf("PromptText = %q, want the prompt handed back for paste", result.PromptText)
	}
}

// TestExecuteFailsWhenThePromptDialogIsNeverAnswered: the budget runs out
// with the dialog still up. Today's failure, and the message still leads
// with what to do, since SubmitView truncates a step's value keeping the
// head.
func TestExecuteFailsWhenThePromptDialogIsNeverAnswered(t *testing.T) {
	withDialogPollInterval(t, 0)
	in := validInput()
	in.UseWorktree = true
	in.Prompt = "implement the fix"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		topo:     herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
		readText: trustDialogScreen,
	}

	var progressed []Progress
	result := Execute(context.Background(), m, ops, ExecOpts{TrustWait: 20 * time.Millisecond},
		func(p Progress) { progressed = append(progressed, p) })

	if result.FailedIndex != 2 {
		t.Fatalf("FailedIndex = %d, want 2 (the prompt op): %+v", result.FailedIndex, result)
	}
	if countCallsWithPrefix(m.calls, "AgentPrompt") != 0 {
		t.Errorf("calls = %v, want nothing ever sent into the dialog", m.calls)
	}
	if result.PromptText != in.Prompt {
		t.Errorf("PromptText = %q, want the prompt handed back for paste", result.PromptText)
	}
	msg := progressed[len(progressed)-1].Err.Error()
	for _, want := range []string{"answer the dialog in the pane", "Quick safety check"} {
		if !strings.Contains(msg, want) {
			t.Errorf("unanswered prompt dialog = %q, want it to contain %q", msg, want)
		}
	}
	if strings.ContainsAny(msg, "\n\r") {
		t.Errorf("unanswered prompt dialog = %q, want a single line", msg)
	}
}

// TestExecuteReportsAGoneAgentFromThePromptDialog: "No, exit" ends Claude
// Code, and `agent read` then has no agent to read. Same conclusion as the
// launch-step wait, and deliberately the same sentence -- the user does not
// care which step noticed.
func TestExecuteReportsAGoneAgentFromThePromptDialog(t *testing.T) {
	withDialogPollInterval(t, 0)
	in := validInput()
	in.UseWorktree = true
	in.Prompt = "implement the fix"
	in.DetectionTimeout = 10 * time.Millisecond
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		topo:          herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
		readText:      trustDialogScreen,
		readErrAfter:  1,
		readErrToGive: errors.New("herdr agent read pane-1: exit status 1: no agent in pane"),
	}

	var progressed []Progress
	result := Execute(context.Background(), m, ops, ExecOpts{TrustWait: time.Minute},
		func(p Progress) { progressed = append(progressed, p) })

	if result.FailedIndex != 2 {
		t.Fatalf("FailedIndex = %d, want 2: %+v", result.FailedIndex, result)
	}
	msg := progressed[len(progressed)-1].Err.Error()
	if !strings.Contains(msg, "no longer running") {
		t.Errorf("gone agent = %q, want it to say the agent is no longer running", msg)
	}
	if result.PromptText != in.Prompt {
		t.Errorf("PromptText = %q, want the prompt handed back for paste", result.PromptText)
	}
}

// TestExecuteToleratesAnUnreadablePaneWhileWaiting: a pane can be briefly
// unreadable mid-repaint, and a single failed read is not evidence the agent
// behind it has gone. Treating it as death would abandon a session the user
// is in the middle of rescuing -- the exact opposite of what the wait is
// for -- so only a run of failures as long as the detection budget counts.
func TestExecuteToleratesAnUnreadablePaneWhileWaiting(t *testing.T) {
	withDialogPollInterval(t, 0)
	in := validInput()
	in.UseWorktree = true
	in.Prompt = "implement the fix"
	in.DetectionTimeout = 30 * time.Second
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		topo:                herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
		readText:            trustDialogScreen,
		readTextClearsAfter: 4,
		readErrAfter:        2,
		readErrCount:        1,
		readErrToGive:       errors.New("herdr agent read pane-1: exit status 1: transient"),
	}

	result := Execute(context.Background(), m, ops, ExecOpts{TrustWait: time.Minute}, nil)

	if result.FailedIndex != -1 {
		t.Fatalf("FailedIndex = %d, want -1: one unreadable poll is not a dead agent: %+v",
			result.FailedIndex, result)
	}
	if !containsCall(m.calls, "AgentPrompt(pane-1,implement the fix)") {
		t.Errorf("calls = %v, want the prompt sent once the dialog cleared", m.calls)
	}
}

// --- #115: the prompt that arrives a second too early --------------------
//
// Live on 2026-09-09, the launch-step wait resolved correctly and then the
// prompt failed anyway: for about a second after the trust dialog is
// answered, `agent get` reports the agent idle and interactive_ready while
// Claude Code is not yet accepting input. herdr types, sees nothing happen,
// and returns agent_prompt_stalled with the pane left holding an empty
// buffer and no turn. Without a retry the wait ends exactly where it began
// -- a failure screen and a prompt to paste by hand.

// stalledPromptErr is herdr's own refusal, as herdrc types it.
func stalledPromptErr() error {
	return fmt.Errorf("%w: herdr agent prompt w1:p2 ...: exit status 1: "+
		`{"error":{"code":"agent_prompt_stalled","message":"agent prompt produced no observed working or blocked state within 5000 ms; current status is idle"},"id":"cli:agent:prompt"}`,
		herdrc.ErrPromptStalled)
}

// TestExecuteRetriesAPromptThatStalled: one more attempt, and the prompt the
// user typed is delivered instead of handed back.
func TestExecuteRetriesAPromptThatStalled(t *testing.T) {
	withPromptRetrySettle(t, 0)
	in := validInput()
	in.UseWorktree = true
	in.Prompt = "implement the fix"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		topo:      herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
		failAt:    "AgentPrompt",
		failErr:   stalledPromptErr(),
		failCount: 1,
	}

	result := Execute(context.Background(), m, ops, ExecOpts{TrustWait: 5 * time.Minute}, nil)

	if result.FailedIndex != -1 {
		t.Fatalf("FailedIndex = %d, want -1: a stalled send is the agent not being ready yet: %+v",
			result.FailedIndex, result)
	}
	if n := countCallsWithPrefix(m.calls, "AgentPrompt"); n != 2 {
		t.Errorf("AgentPrompt called %d times, want exactly 2 -- one stall, one retry", n)
	}
	// Three reads for two sends: the guard runs again before the retry, so
	// the second send can no more type into a dialog than the first could,
	// and the send that succeeds is then verified (#116). The stalled one
	// is not -- there is nothing to verify about a send herdr says it never
	// delivered.
	if n := countCallsWithPrefix(m.calls, "AgentRead"); n != 3 {
		t.Errorf("AgentRead called %d times, want 3 -- promptIfReady must gate the retry and verify the send", n)
	}
	if result.PromptText != "" {
		t.Errorf("PromptText = %q, want empty", result.PromptText)
	}
}

// TestExecuteRetriesAStalledPromptOnlyOnce: bounded, so a Claude Code that
// buffers keystrokes it received while starting cannot be handed the same
// prompt over and over. Two stalls is where the evidence stops supporting
// another attempt.
func TestExecuteRetriesAStalledPromptOnlyOnce(t *testing.T) {
	withPromptRetrySettle(t, 0)
	in := validInput()
	in.UseWorktree = true
	in.Prompt = "implement the fix"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		topo:      herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
		failAt:    "AgentPrompt",
		failErr:   stalledPromptErr(),
		failCount: 99,
	}

	var progressed []Progress
	result := Execute(context.Background(), m, ops, ExecOpts{TrustWait: 5 * time.Minute},
		func(p Progress) { progressed = append(progressed, p) })

	if result.FailedIndex != 2 {
		t.Fatalf("FailedIndex = %d, want 2 (the prompt op): %+v", result.FailedIndex, result)
	}
	if n := countCallsWithPrefix(m.calls, "AgentPrompt"); n != 2 {
		t.Errorf("AgentPrompt called %d times, want exactly 2 -- the retry is bounded", n)
	}
	if result.PromptText != in.Prompt {
		t.Errorf("PromptText = %q, want the prompt handed back for paste", result.PromptText)
	}
	// Two sends have now been attempted, so "not sent" is a stronger claim
	// than the evidence supports: the message must send the user to the pane
	// before they paste, the same posture #108 established.
	msg := progressed[len(progressed)-1].Err.Error()
	if !strings.Contains(msg, "read the pane") {
		t.Errorf("stalled twice = %q, want it to send the user to the pane before pasting", msg)
	}
	if strings.ContainsAny(msg, "\n\r") {
		t.Errorf("stalled twice = %q, want a single line", msg)
	}
}

// TestExecuteWithoutATrustBudgetDoesNotRetryAStalledPrompt: decision 2 once
// more. `create` gets herdr's own refusal, on the first attempt, unchanged.
func TestExecuteWithoutATrustBudgetDoesNotRetryAStalledPrompt(t *testing.T) {
	withPromptRetrySettle(t, 0)
	in := validInput()
	in.UseWorktree = true
	in.Prompt = "implement the fix"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		topo:      herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
		failAt:    "AgentPrompt",
		failErr:   stalledPromptErr(),
		failCount: 99,
	}

	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

	if result.FailedIndex != 2 {
		t.Fatalf("FailedIndex = %d, want 2: %+v", result.FailedIndex, result)
	}
	if n := countCallsWithPrefix(m.calls, "AgentPrompt"); n != 1 {
		t.Errorf("AgentPrompt called %d times, want exactly 1 -- no budget, no retry", n)
	}
}

// TestExecuteDoesNotRetryAnUnconfirmedPrompt is the line between the two
// sentinels, and it is the one that must not blur: #108's timeout means the
// agent WAS working, so a resend would give a busy agent its instructions
// twice. Only a stall is retried.
func TestExecuteDoesNotRetryAnUnconfirmedPrompt(t *testing.T) {
	withPromptRetrySettle(t, 0)
	in := validInput()
	in.UseWorktree = true
	in.Prompt = "implement the fix"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		topo:      herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
		failAt:    "AgentPrompt",
		failErr:   fmt.Errorf("%w: herdr agent prompt ...", herdrc.ErrPromptWaitTimeout),
		failCount: 99,
	}

	result := Execute(context.Background(), m, ops, ExecOpts{TrustWait: 5 * time.Minute}, nil)

	if n := countCallsWithPrefix(m.calls, "AgentPrompt"); n != 1 {
		t.Fatalf("AgentPrompt called %d times, want exactly 1 -- an unconfirmed prompt is never resent", n)
	}
	if !result.PromptUnconfirmed {
		t.Errorf("PromptUnconfirmed = false, want true -- #108's classification must survive the retry logic")
	}
}

// -- #116: a prompt sent into a not-yet-painted dialog ------------------
//
// The defect these pin is one race with two halves, and each half needs
// its own rule. Before the send, a screen that has not painted must not be
// read as a safe one (errPaneUnpainted). After it, a send that reported
// success must be shown to have LANDED before the pipeline calls the
// create clean (confirmPromptLanded). Neither rule subsumes the other:
// the first cannot close a window it can only narrow, and the second
// cannot stop the agent from dying -- it can only stop the popup from
// telling the user it did not.

// TestExecutePromptRefusedWhenScreenHasNotPainted is #116's pre-send half.
//
// The read SUCCEEDS and returns nothing: herdr answered, and the pane it
// answered about had not drawn its trust dialog yet. Under the old
// negative rule ("no signature matched, therefore safe") that sent the
// prompt, and its trailing Enter answered the dialog's preselected
// "No, exit". With no TrustWait -- the headless `create` shape -- the
// refusal is terminal, and it is the RIGHT terminal outcome: the agent is
// alive and the prompt is surfaced for a paste.
func TestExecutePromptRefusedWhenScreenHasNotPainted(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	in.Prompt = "implement the fix"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		topo:            herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
		blankReadsFirst: 1,
	}

	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

	wantFailedIndex := len(ops) - 1
	if result.FailedIndex != wantFailedIndex {
		t.Fatalf("FailedIndex = %d, want %d (the prompt op): %+v", result.FailedIndex, wantFailedIndex, result)
	}
	if result.PromptText != in.Prompt {
		t.Fatalf("PromptText = %q, want %q (surfaced for manual paste)", result.PromptText, in.Prompt)
	}
	if containsCall(m.calls, "AgentPrompt(pane-1,implement the fix)") {
		t.Fatalf("calls = %v, want NO AgentPrompt call -- an unpainted screen is not a ready one", m.calls)
	}
}

// TestExecutePromptWaitsThroughAnUnpaintedScreen is the same refusal in
// the popup, where somebody is at the keyboard.
//
// An unpainted screen is a wait, not a stop: #115's prompt-step wait polls
// until the pane says something, and the whole point of the fix is that
// "nothing yet" is not "clear". The wait therefore has to hold through a
// blank read exactly as it holds through a dialog -- if it treated blank
// as cleared it would resolve instantly and hand promptIfReady the very
// pane it was entered for, which is a loop that sends the prompt anyway.
func TestExecutePromptWaitsThroughAnUnpaintedScreen(t *testing.T) {
	withDialogPollInterval(t, 0)

	in := validInput()
	in.UseWorktree = true
	in.Prompt = "implement the fix"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		topo:            herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
		blankReadsFirst: 3,
	}

	result := Execute(context.Background(), m, ops, ExecOpts{TrustWait: time.Minute}, nil)

	if result.FailedIndex != -1 {
		t.Fatalf("FailedIndex = %d, want -1 (the screen painted and the prompt went out): %+v",
			result.FailedIndex, result)
	}
	if !containsCall(m.calls, "AgentPrompt(pane-1,implement the fix)") {
		t.Fatalf("calls = %v, want the prompt sent once the pane painted", m.calls)
	}
	if n := countCallsWithPrefix(m.calls, "AgentPrompt"); n != 1 {
		t.Errorf("AgentPrompt called %d times, want 1", n)
	}
}

// TestExecutePromptFailsWhenTheSendKilledTheAgent is #116's headline: the
// create that reports success over a dead agent.
//
// Everything upstream is green -- the pane painted, no signature matched,
// `agent prompt --wait` returned ok -- and the agent is gone anyway,
// because the screen that painted between the read and the send was a
// dialog and the prompt's Enter answered it. The only evidence left is the
// pane, and a pane whose agent has exited cannot be read at all
// (ErrAgentGone's own reasoning, one step later). Reporting this as a
// clean create is what makes the defect cost more than the launch: state
// is persisted, no unsent-prompt.txt is written, and nobody has a reason
// to look.
func TestExecutePromptFailsWhenTheSendKilledTheAgent(t *testing.T) {
	withDialogPollInterval(t, 0)

	in := validInput()
	in.UseWorktree = true
	in.Prompt = "implement the fix"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		topo:          herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
		postPromptErr: errors.New("herdr agent read pane-1: exit status 1: no agent in pane"),
	}

	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

	wantFailedIndex := len(ops) - 1
	if result.FailedIndex != wantFailedIndex {
		t.Fatalf("FailedIndex = %d, want %d (the prompt op): %+v", result.FailedIndex, wantFailedIndex, result)
	}
	if result.PromptText != in.Prompt {
		t.Fatalf("PromptText = %q, want %q (surfaced for manual paste)", result.PromptText, in.Prompt)
	}
	if result.PromptUnconfirmed {
		t.Errorf("PromptUnconfirmed = true, want false -- nothing was delivered, so the clean is safe")
	}
	if n := countCallsWithPrefix(m.calls, "AgentPrompt"); n != 1 {
		t.Errorf("AgentPrompt called %d times, want exactly 1 -- verification must never resend (#108)", n)
	}
}

// TestExecutePromptFailsWhenTheDialogSwallowedIt is the same race without
// the death: the dialog ate the text and the Enter chose something that
// did not exit.
//
// The pane after the send is still on a dialog and carries no trace of the
// prompt, which is two independent facts pointing the same way. Note what
// this must NOT do: having sent once already, it reports rather than
// waiting and sending again. A second copy into an agent that did receive
// the first is the injury #108 exists to prevent, and a post-send check
// that retries would deliver it by the front door.
func TestExecutePromptFailsWhenTheDialogSwallowedIt(t *testing.T) {
	withDialogPollInterval(t, 0)

	in := validInput()
	in.UseWorktree = true
	in.Prompt = "implement the fix"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		topo: herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
		postPromptText: "Quick safety check: Is this a project you created or one you trust?\n\n" +
			"❯ No, exit\n  Yes, I trust this folder\n\nEnter to confirm · Esc to cancel\n",
	}

	result := Execute(context.Background(), m, ops, ExecOpts{TrustWait: time.Minute}, nil)

	wantFailedIndex := len(ops) - 1
	if result.FailedIndex != wantFailedIndex {
		t.Fatalf("FailedIndex = %d, want %d (the prompt op): %+v", result.FailedIndex, wantFailedIndex, result)
	}
	if result.PromptText != in.Prompt {
		t.Fatalf("PromptText = %q, want %q (surfaced for manual paste)", result.PromptText, in.Prompt)
	}
	if n := countCallsWithPrefix(m.calls, "AgentPrompt"); n != 1 {
		t.Errorf("AgentPrompt called %d times, want exactly 1 -- verification reports, it never resends (#108)", n)
	}
}

// TestExecutePromptSucceedsWhenTheAgentRaisesItsOwnDialog is the
// false-positive the post-send check has to survive, and the reason it
// looks for the prompt rather than for the absence of a dialog.
//
// "Enter to confirm" is Claude Code's OWN footer: an agent that received
// the prompt and immediately asked permission to act on it is sitting on a
// screen carrying a dialog.go signature, with the prompt delivered and
// already a turn. Calling that a failure would tell the user to re-paste
// something the agent is working on -- #108's injury again, reached from
// the opposite side. What separates the two panes is not the dialog, it is
// whether the prompt is on the screen at all.
func TestExecutePromptSucceedsWhenTheAgentRaisesItsOwnDialog(t *testing.T) {
	withDialogPollInterval(t, 0)

	in := validInput()
	in.UseWorktree = true
	in.Prompt = "delete the stale fixtures under testdata"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		topo: herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
		// The turn is wrapped MID-WORD, and inside the probe rather than
		// past it: "fixt|ures" is what a pane does to a line that runs off
		// its right edge, and it is the case that decides how promptOnScreen
		// has to normalise. Collapsing whitespace runs to a single space
		// leaves "fixt ures" and finds nothing; removing it entirely puts
		// the word back together. A fixture that wraps at a space cannot
		// tell those two apart, so this one does not.
		postPromptText: "❯ delete the stale fixt\n  ures under testdata\n\n" +
			"● I'll remove those. Running:\n  rm -rf testdata/old\n\n" +
			"Do you want to proceed?\n❯ Yes\n  No\n\nEnter to confirm · Esc to cancel\n",
	}

	result := Execute(context.Background(), m, ops, ExecOpts{TrustWait: time.Minute}, nil)

	if result.FailedIndex != -1 {
		t.Fatalf("FailedIndex = %d, want -1 -- the prompt is on the screen, so it landed: %+v",
			result.FailedIndex, result)
	}
}

// TestExecutePromptSurvivesOneUnreadablePaneAfterSending keeps the
// post-send check from inventing failures.
//
// A pane can be briefly unreadable mid-repaint, and the send that just
// happened is exactly when a repaint is likeliest. One failed read is not
// evidence an agent has gone -- the same distinction awaitDialogCleared
// draws with its own detection budget -- so the check re-reads before it
// concludes anything. A verification that turns a good create into a
// failure is a worse defect than the one it replaced, because it fires on
// every run rather than on a race.
func TestExecutePromptSurvivesOneUnreadablePaneAfterSending(t *testing.T) {
	withDialogPollInterval(t, 0)

	in := validInput()
	in.UseWorktree = true
	in.Prompt = "implement the fix"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := &mockRunner{
		topo:          herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
		readErrAfter:  1,
		readErrCount:  1,
		readErrToGive: errors.New("herdr agent read pane-1: exit status 1: transient"),
	}

	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

	if result.FailedIndex != -1 {
		t.Fatalf("FailedIndex = %d, want -1 -- one unreadable poll is a repaint, not a dead agent: %+v",
			result.FailedIndex, result)
	}
}

// TestUnsafeScreenErrorsExcludeASwallowedPrompt pins the one membership
// question in this file that a scenario test cannot reach.
//
// isUnsafeScreenError names the refusals Execute is allowed to WAIT on and
// then try again, and what makes that safe is that every one of them
// happens before any text is sent. errPromptSwallowed does not: it is the
// verdict of a check that runs after a send. Putting it in this set would
// make a swallowed prompt a resend, which is #108's double-paste arriving
// through the front door.
//
// Asserted here, on the predicate itself, because driving it through
// Execute proves nothing either way -- a swallowed prompt's pane is still
// showing the dialog, so the wait that a wrong answer here would enter
// runs out its budget and fails anyway, and the run looks identical. The
// invariant is a claim about the SET, so the set is where it is tested.
func TestUnsafeScreenErrorsExcludeASwallowedPrompt(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"a dialog on the screen is waitable", fmt.Errorf("wrapped: %w", errAgentOnDialog), true},
		{"an unpainted screen is waitable", fmt.Errorf("wrapped: %w", errPaneUnpainted), true},
		{"a swallowed prompt is NOT", fmt.Errorf("wrapped: %w", errPromptSwallowed), false},
		{"nor is an unrelated failure", errors.New("herdr agent prompt: exit status 1"), false},
		{"nor is nothing at all", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isUnsafeScreenError(tc.err); got != tc.want {
				t.Errorf("isUnsafeScreenError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
