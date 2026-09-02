// submit_test.go is Task 20b's own test file: the submit orchestration
// this task adds on top of Task 20's startup/data-source/seeding layer --
// spec §9's validation-then-plan.Build-then-plan.Execute pipeline,
// streamed plan.Progress, and the keep-or-clean gate. Every scenario uses
// fakes (submitFakeRunner below, plus app_test.go's own fakeGit/
// fakeClauth) -- no real herdr/git/network/sleep, matching this package's
// established convention.
//
// plan.CleanCheck performs REAL git I/O for a worktree space
// (gitx.Disposable) -- internal/plan's own exec_test.go already covers
// its allow/deny/error paths thoroughly against a real temp git repo
// (that package's own established convention, TestCleanCheckAllowsCleanWorktree
// et al.). This file deliberately never exercises that path: every
// failed-step scenario here uses a NON-worktree Input, for which
// CleanCheck is a pure, I/O-free `{Allowed: true}` (exec.go's own
// TestCleanCheckAlwaysAllowsNonWorktree covers this at the plan layer) --
// so what this file actually proves is that plan.CleanCheck's real
// return value (whatever it is) gets threaded through to SubmitView
// unmodified, not that CleanCheck itself is correct.
package app

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ZviBaratz/herdr-draft/internal/clauth"
	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/form"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
	"github.com/ZviBaratz/herdr-draft/internal/plan"
	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// --- test doubles ----------------------------------------------------------

// submitFakeRunner implements herdrc.Runner with configurable results --
// unlike app_test.go's own fakeRunner (every method a fixed no-op stub,
// enough for Task 20's own WorkspaceList-only needs), submit_test.go's
// scenarios need to control which topology a creation op returns and
// which specific op fails.
type submitFakeRunner struct {
	topo herdrc.CreatedTopology

	// failAt names the one Runner method (by its herdrc.Runner method
	// name, e.g. "AgentStart") that should fail; "" means nothing fails.
	failAt  string
	failErr error

	// readText is what AgentRead returns on success (default "": no
	// dialog present -- the pre-existing happy-path scenario's implicit
	// assumption, that the pane is a normal ready state, still holds).
	readText string

	calls []string
}

var _ herdrc.Runner = (*submitFakeRunner)(nil)

func (r *submitFakeRunner) shouldFail(name string) bool {
	r.calls = append(r.calls, name)
	return r.failAt == name
}

func (r *submitFakeRunner) WorkspaceList(context.Context) ([]herdrc.WorkspaceInfo, error) {
	if r.shouldFail("WorkspaceList") {
		return nil, r.failErr
	}
	return nil, nil
}

func (r *submitFakeRunner) WorktreeCreate(context.Context, herdrc.WorktreeCreateReq) (herdrc.CreatedTopology, error) {
	if r.shouldFail("WorktreeCreate") {
		return herdrc.CreatedTopology{}, r.failErr
	}
	return r.topo, nil
}

func (r *submitFakeRunner) WorkspaceCreate(context.Context, herdrc.WorkspaceCreateReq) (herdrc.CreatedTopology, error) {
	if r.shouldFail("WorkspaceCreate") {
		return herdrc.CreatedTopology{}, r.failErr
	}
	return r.topo, nil
}

func (r *submitFakeRunner) TabCreate(context.Context, herdrc.TabCreateReq) (herdrc.CreatedTopology, error) {
	if r.shouldFail("TabCreate") {
		return herdrc.CreatedTopology{}, r.failErr
	}
	return r.topo, nil
}

func (r *submitFakeRunner) PaneSplit(context.Context, herdrc.PaneSplitReq) (herdrc.CreatedTopology, error) {
	if r.shouldFail("PaneSplit") {
		return herdrc.CreatedTopology{}, r.failErr
	}
	return r.topo, nil
}

func (r *submitFakeRunner) AgentStart(context.Context, herdrc.AgentStartReq) error {
	if r.shouldFail("AgentStart") {
		return r.failErr
	}
	return nil
}

func (r *submitFakeRunner) AgentPrompt(context.Context, herdrc.AgentPromptReq) error {
	if r.shouldFail("AgentPrompt") {
		return r.failErr
	}
	return nil
}

func (r *submitFakeRunner) AgentRead(context.Context, string) (string, error) {
	if r.shouldFail("AgentRead") {
		return "", r.failErr
	}
	return r.readText, nil
}

func (r *submitFakeRunner) AwaitDetection(context.Context, string, time.Duration) error {
	if r.shouldFail("AwaitDetection") {
		return r.failErr
	}
	return nil
}

func (r *submitFakeRunner) PaneRun(context.Context, string, []string) error {
	if r.shouldFail("PaneRun") {
		return r.failErr
	}
	return nil
}

func (r *submitFakeRunner) WorktreeRemove(context.Context, string) error {
	if r.shouldFail("WorktreeRemove") {
		return r.failErr
	}
	return nil
}

func (r *submitFakeRunner) WorkspaceClose(context.Context, string) error {
	if r.shouldFail("WorkspaceClose") {
		return r.failErr
	}
	return nil
}

// newSubmitTestModel mirrors app_test.go's own newTestModel, but takes an
// explicit herdrc.Runner -- newTestModel's own fakeRunner has no
// configurable results at all, which every scenario below needs.
func newSubmitTestModel(t *testing.T, runner herdrc.Runner, s testSetup) Model {
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
			Runner: runner,
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

// drainSubmitProgress runs a submit-pipeline Cmd chain (as returned by
// Update(form.SubmitMsg{}) once startSubmit has begun) until it produces
// a submitDoneMsg, applying every intermediate submitProgressMsg via m's
// own handleSubmitProgress -- exactly the sequence the real bubbletea
// event loop would run, just driven synchronously here -- and returns the
// final Model, the ordered progress log, and the terminal submitDoneMsg.
func drainSubmitProgress(t *testing.T, m Model, cmd tea.Cmd) (Model, []plan.Progress, submitDoneMsg) {
	t.Helper()
	if cmd == nil {
		t.Fatal("drainSubmitProgress: nil cmd")
	}
	var log []plan.Progress
	msg := cmd()
	for {
		switch pm := msg.(type) {
		case submitProgressMsg:
			log = append(log, pm.progress)
			var next tea.Cmd
			m, next = m.handleSubmitProgress(pm)
			msg = next()
		case submitDoneMsg:
			return m, log, pm
		default:
			t.Fatalf("unexpected message in submit progress chain: %#v", msg)
		}
	}
}

// --- Step 1's five required scenarios --------------------------------

// TestSubmit_HappyPathMatchesTask12FirstMatrixCase pins the brief's own
// literal requirement: a fully valid worktree+pin+prompt submission
// produces EXACTLY Task 12's first matrix case op list
// (TestBuildWorktreePinPrompt: [OpWorktreeCreate, OpClauthLaunch,
// OpAwaitDetection, OpAgentPrompt]), and every plan.Progress it reports
// is forwarded, in order, to SubmitView (via the streamed
// submitProgressMsg chain) -- not just the final state.
func TestSubmit_HappyPathMatchesTask12FirstMatrixCase(t *testing.T) {
	runner := &submitFakeRunner{topo: herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"}}
	m := newSubmitTestModel(t, runner, testSetup{
		Ctx: herdrc.Context{WorkspaceCwd: "/repo"},
		ClauthStatus: clauth.Status{Profiles: []clauth.Profile{
			{Name: "active-ish", AuthStatus: "ok"},
			{Name: "work", AuthStatus: "ok"},
		}},
		Clauth: &fakeClauth{},
	})

	m.title.SetTitle("Fix pagination", false)
	m.worktree.SetGitTarget(true) // must precede SetOn: the chip row starts inert (see WorktreeField's own doc).
	m.worktree.SetOn(true)
	m.worktree.SetBranch("zvi/fix-pagination", false)
	m.account.SetPin("work")
	m.account.SetAgentIsClaude(true)
	m.prompt.SetValue("implement the fix", false)

	next, cmd := m.Update(form.SubmitMsg{})
	m = next.(Model)
	if !m.submitting {
		t.Fatalf("Update(SubmitMsg{}) with a fully valid form did not start submitting")
	}
	if cmd == nil {
		t.Fatal("Update(SubmitMsg{}) returned a nil cmd, want the submit-pipeline chain")
	}

	// Seeded progress (before any real event arrives) must already show
	// every op, matching Task 12's first matrix case exactly.
	wantLabels := []string{"creating worktree", "launching claude via clauth", "waiting for agent detection", "sending prompt"}
	if len(m.submitProgress) != len(wantLabels) {
		t.Fatalf("seeded submitProgress = %d entries, want %d: %+v", len(m.submitProgress), len(wantLabels), m.submitProgress)
	}
	for i, label := range wantLabels {
		p := m.submitProgress[i]
		if p.Label != label || p.Index != i || p.Total != len(wantLabels) || p.State != plan.StepPending {
			t.Fatalf("seeded submitProgress[%d] = %+v, want Label=%q Index=%d Total=%d State=StepPending", i, p, label, i, len(wantLabels))
		}
	}

	m, log, done := drainSubmitProgress(t, m, cmd)

	if len(log) != 2*len(wantLabels) {
		t.Fatalf("progress events forwarded = %d, want %d ([Running,Done] per op, in order)", len(log), 2*len(wantLabels))
	}
	for i, label := range wantLabels {
		running, doneEvt := log[2*i], log[2*i+1]
		if running.Index != i || running.Label != label || running.State != plan.StepRunning {
			t.Errorf("progress[%d] = %+v, want Index=%d Label=%q State=StepRunning", 2*i, running, i, label)
		}
		if doneEvt.Index != i || doneEvt.Label != label || doneEvt.State != plan.StepDone {
			t.Errorf("progress[%d] = %+v, want Index=%d Label=%q State=StepDone", 2*i+1, doneEvt, i, label)
		}
	}

	if done.result.FailedIndex != -1 {
		t.Fatalf("ExecResult.FailedIndex = %d, want -1 (success): %+v", done.result.FailedIndex, done.result)
	}

	wantCalls := []string{"WorktreeCreate", "PaneRun", "AwaitDetection", "AgentRead", "AgentPrompt"}
	if !reflect.DeepEqual(runner.calls, wantCalls) {
		t.Fatalf("runner.calls = %v, want %v", runner.calls, wantCalls)
	}

	// A successful submit now persists spec §12's state before it quits
	// (finding I2), so the quit arrives one message later: handleSubmitDone
	// returns the write, and statePersistedMsg is what ends the program.
	m2, finalCmd := m.handleSubmitDone(done)
	if _, ok := finalCmd().(statePersistedMsg); !ok {
		t.Fatalf("handleSubmitDone on full success did not persist state first")
	}
	_, quitCmd := m2.updateSubmitting(statePersistedMsg{})
	if _, ok := quitCmd().(tea.QuitMsg); !ok {
		t.Fatalf("statePersistedMsg did not quit after a fully successful submit")
	}
}

// TestSubmit_DuplicateVerdictBlocksAndRefocusesTitle pins spec §9's
// second named validation: a branch/label duplicate blocks submission
// outright (no plan.Build, no plan.Execute, m.submitting stays false) and
// re-focuses Title.
func TestSubmit_DuplicateVerdictBlocksAndRefocusesTitle(t *testing.T) {
	runner := &submitFakeRunner{}
	m := newSubmitTestModel(t, runner, testSetup{Ctx: herdrc.Context{WorkspaceCwd: "/repo"}})
	m.title.SetTitle("Fix pagination", false)
	m.titleDupBlocked = true // simulates handleTitleResult's own already-computed, already-shown verdict.

	m.form.FocusByID("dir") // focus something else first, so the re-focus below is actually observable.

	next, cmd := m.Update(form.SubmitMsg{})
	m = next.(Model)
	if m.submitting {
		t.Fatal("Update(SubmitMsg{}) started submitting despite a blocking title-dup verdict")
	}
	if cmd == nil {
		t.Fatal("Update(SubmitMsg{}) with a blocking dup returned a nil cmd, want the re-focus")
	}
	if got := m.form.FocusedID(); got != "title" {
		t.Fatalf("FocusedID() after a dup-blocked submit = %q, want %q", got, "title")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner.calls = %v after a blocked submit, want none (nothing created)", runner.calls)
	}
}

// TestSubmit_PinnedProfileAuthFailedBlocksWithAccountVerdict pins spec
// §9's third named validation: a pinned clauth profile whose auth_status
// isn't "ok" blocks submission and re-focuses Account -- the field
// already showing that profile's own "auth failed" row marker (Task 18).
func TestSubmit_PinnedProfileAuthFailedBlocksWithAccountVerdict(t *testing.T) {
	runner := &submitFakeRunner{}
	m := newSubmitTestModel(t, runner, testSetup{
		Ctx: herdrc.Context{WorkspaceCwd: "/repo"},
		ClauthStatus: clauth.Status{Profiles: []clauth.Profile{
			{Name: "active-ish", AuthStatus: "ok"},
			{Name: "work", AuthStatus: "expired"},
		}},
		Clauth: &fakeClauth{},
	})
	m.title.SetTitle("Fix pagination", false)
	m.account.SetPin("work")
	m.account.SetAgentIsClaude(true)

	next, _ := m.Update(form.SubmitMsg{})
	m = next.(Model)
	// Unlike Title (checked below in the dup/empty-title tests),
	// AccountField.Focus() always returns nil -- widgets.Picker owns no
	// blink cmd of its own (field_account.go's own Focus() doc comment)
	// -- so the returned cmd being nil here is expected and NOT itself a
	// sign submission proceeded; m.submitting and FocusedID() are the
	// real signals.
	if m.submitting {
		t.Fatal("Update(SubmitMsg{}) started submitting despite a pinned profile with auth_status \"expired\"")
	}
	if got := m.form.FocusedID(); got != "account" {
		t.Fatalf("FocusedID() after an account-blocked submit = %q, want %q", got, "account")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner.calls = %v after a blocked submit, want none (nothing created)", runner.calls)
	}

	// Fix round 1 (reviewer finding -- silent failure): before this fix,
	// the only cue was the picker row's own marker/label -- which already
	// mentions "expired" regardless (accountRow's own "name · tier ·
	// auth_status" format) and was ALREADY visible before the click, so
	// checking for "expired" alone would pass even without the fix.
	// AccountField.SetVerdict pushes a NEW hint-row message with a
	// "blocked" prefix nothing else in the view ever renders -- that's
	// the actual signal this fix adds.
	frame := ansi.Strip(m.account.View(60, m.account.Height(24)))
	wantVerdict := "blocked — auth: expired"
	if !strings.Contains(frame, wantVerdict) {
		t.Fatalf("AccountField.View(60) after a blocked submit = %q, want it to contain the new blocking verdict %q", frame, wantVerdict)
	}
}

// TestSubmit_FailedStepShowsFailureWithCleanCheckReasonThreaded pins the
// brief's own fourth scenario: a failed step (after step 1's topology
// creation succeeded) puts SubmitView in its failure state, with
// plan.CleanCheck's own real CleanDecision -- not a placeholder --
// threaded through. Uses a non-worktree Input (see this file's own doc
// comment) so CleanCheck is pure/I-O-free and deterministically allows.
func TestSubmit_FailedStepShowsFailureWithCleanCheckReasonThreaded(t *testing.T) {
	failErr := errors.New("boom")
	runner := &submitFakeRunner{
		topo:    herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
		failAt:  "AgentStart",
		failErr: failErr,
	}
	m := newSubmitTestModel(t, runner, testSetup{Ctx: herdrc.Context{WorkspaceCwd: "/repo"}})
	m.title.SetTitle("Fix pagination", false)
	// worktree/account untouched: worktree off (no git target confirmed),
	// no account configured -- plan.Build's own topologyOp default is
	// PlacementNewSpace -> OpWorkspaceCreate, then OpAgentStart (agent
	// defaults to "claude" via testSetup's own Agents.Favorites default,
	// no pin, no prompt) -- exactly the non-worktree shape this file's own
	// doc comment describes.

	next, cmd := m.Update(form.SubmitMsg{})
	m = next.(Model)
	if !m.submitting {
		t.Fatal("Update(SubmitMsg{}) did not start submitting")
	}

	m, _, done := drainSubmitProgress(t, m, cmd)
	if done.result.FailedIndex == -1 {
		t.Fatalf("ExecResult.FailedIndex = -1, want a failure at OpAgentStart: %+v", done.result)
	}
	if done.result.Created == nil {
		t.Fatal("ExecResult.Created is nil, want step 1 (OpWorkspaceCreate) to have succeeded before the failure")
	}

	var cleanCmd tea.Cmd
	m, cleanCmd = m.handleSubmitDone(done)
	if cleanCmd == nil {
		t.Fatal("handleSubmitDone after step 1 succeeded returned a nil cmd, want the CleanCheck cmd")
	}
	msg := cleanCmd()
	ccMsg, ok := msg.(cleanCheckMsg)
	if !ok {
		t.Fatalf("handleSubmitDone's cmd produced %#v, want cleanCheckMsg", msg)
	}

	m, _ = m.handleCleanCheckResult(ccMsg)

	if !m.submitCleanDecision.Allowed {
		t.Fatalf("submitCleanDecision = %+v, want Allowed (non-worktree space, spec §9: always allowed)", m.submitCleanDecision)
	}
	if m.submitView == nil {
		t.Fatal("submitView is nil after handleCleanCheckResult")
	}

	frame := ansi.Strip(m.submitView.ViewAt(80, 24))
	if !containsAll(frame, "starting agent", "k keep", "c clean") {
		t.Fatalf("SubmitView.ViewAt(80,24) = %q, want it to show the failed step plus an allowed keep-or-clean choice", frame)
	}
}

// TestSubmit_CleanMsgOnDeniedCheckDoesNothing pins the brief's own fifth
// scenario: CleanMsg must not call plan.Clean at all when the app's own
// recorded CleanDecision denies it -- even though SubmitView's own k/c
// grammar already refuses to emit CleanMsg in that state (submitview_test.go's
// TestSubmitView_CPressNoOpWhenCleanDenied), this package checks its own
// copy too (defense in depth, matching reloadClauthCmd's nil-source
// guard elsewhere in this package) rather than trusting that gate alone.
func TestSubmit_CleanMsgOnDeniedCheckDoesNothing(t *testing.T) {
	runner := &submitFakeRunner{}
	m := newSubmitTestModel(t, runner, testSetup{})
	m.submitting = true
	m.submitCleanDecision = plan.CleanDecision{Allowed: false, Reason: "uncommitted changes"}
	m.submitInput = plan.Input{UseWorktree: true, BaseRef: "main"}
	m.submitCreated = herdrc.CreatedTopology{WorkspaceID: "ws-1", CheckoutPath: "/does/not/matter"}

	next, cmd := m.Update(form.CleanMsg{})
	if cmd != nil {
		t.Fatal("Update(CleanMsg{}) on a denied CleanDecision returned a non-nil cmd, want nil (no plan.Clean call)")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner.calls = %v after a denied CleanMsg, want none (plan.Clean never called)", runner.calls)
	}
	_ = next.(Model)
}

// --- additional coverage: validations beyond the required five ---------

// TestSubmit_EmptyTitleBlocksAndRefocusesTitle pins the guard this
// package adds beyond spec §9's own three named validations (see
// checkSubmitValidation's own doc comment): Ctrl+S can submit from ANY
// zone regardless of Title's own content (keys.go's own grammar only
// blocks a bare Enter from an empty Title), so this package must reject
// an empty title itself rather than ever calling plan.Build with one.
func TestSubmit_EmptyTitleBlocksAndRefocusesTitle(t *testing.T) {
	runner := &submitFakeRunner{}
	m := newSubmitTestModel(t, runner, testSetup{Ctx: herdrc.Context{WorkspaceCwd: "/repo"}})

	next, cmd := m.Update(form.SubmitMsg{})
	m = next.(Model)
	if m.submitting {
		t.Fatal("Update(SubmitMsg{}) with an empty title started submitting")
	}
	if cmd == nil {
		t.Fatal("Update(SubmitMsg{}) with an empty title returned a nil cmd, want the re-focus")
	}
	if got := m.form.FocusedID(); got != "title" {
		t.Fatalf("FocusedID() after an empty-title-blocked submit = %q, want %q", got, "title")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner.calls = %v after a blocked submit, want none", runner.calls)
	}
}

// TestSubmit_InvalidDirectoryBlocksSubmit pins spec §9's first named
// validation directly (the happy-path test above only exercises it
// implicitly, via never triggering it).
func TestSubmit_InvalidDirectoryBlocksSubmit(t *testing.T) {
	runner := &submitFakeRunner{}
	m := newSubmitTestModel(t, runner, testSetup{Ctx: herdrc.Context{WorkspaceCwd: "/repo"}})
	m.title.SetTitle("Fix pagination", false)
	m.dirInvalid = true // simulates handleDirResult's own already-computed verdict.

	next, cmd := m.Update(form.SubmitMsg{})
	m = next.(Model)
	if m.submitting {
		t.Fatal("Update(SubmitMsg{}) started submitting despite an invalid directory")
	}
	if cmd == nil {
		t.Fatal("Update(SubmitMsg{}) with an invalid directory returned a nil cmd, want the re-focus")
	}
	if got := m.form.FocusedID(); got != "dir" {
		t.Fatalf("FocusedID() after a dir-blocked submit = %q, want %q", got, "dir")
	}
}

// TestSubmit_StepOneFailureHasNoKeepOrCleanPrompt pins spec §9's own
// scoping of the keep-or-clean gate to "after step 1 succeeded": a
// failure AT step 1 (topology creation itself) must not run CleanCheck or
// show any keep/clean prompt at all -- Esc/Ctrl+C (updateSubmitting) is
// this state's only way out.
func TestSubmit_StepOneFailureHasNoKeepOrCleanPrompt(t *testing.T) {
	failErr := errors.New("boom")
	runner := &submitFakeRunner{failAt: "WorkspaceCreate", failErr: failErr}
	m := newSubmitTestModel(t, runner, testSetup{Ctx: herdrc.Context{WorkspaceCwd: "/repo"}})
	m.title.SetTitle("Fix pagination", false)

	next, cmd := m.Update(form.SubmitMsg{})
	m = next.(Model)

	m, _, done := drainSubmitProgress(t, m, cmd)
	if done.result.FailedIndex != 0 {
		t.Fatalf("FailedIndex = %d, want 0 (the very first op)", done.result.FailedIndex)
	}
	if done.result.Created != nil {
		t.Fatalf("Created = %+v, want nil (step 1 itself failed)", done.result.Created)
	}

	var cleanCmd tea.Cmd
	m, cleanCmd = m.handleSubmitDone(done)
	if cleanCmd != nil {
		t.Fatalf("handleSubmitDone after a step-1 failure returned a non-nil cmd, want nil (no CleanCheck)")
	}

	// Esc must still quit even in this dead-end state.
	_, quitCmd := m.updateSubmitting(tea.KeyPressMsg{Code: tea.KeyEscape})
	if quitCmd == nil {
		t.Fatal("Esc during a step-1-failure dead end returned a nil cmd, want tea.Quit")
	}
	if _, ok := quitCmd().(tea.QuitMsg); !ok {
		t.Fatal("Esc during a step-1-failure dead end did not return tea.Quit")
	}
}

// TestUpdateSubmitting_EscDuringActiveStreamingDoesNotQuit is fix round
// 1's own regression test (reviewer finding): an earlier version of
// updateSubmitting quit on Esc/Ctrl+C for the WHOLE m.submitting
// lifetime, not just the step-1-failure dead end that was actually
// approved. Since runSubmitCmd's progress channel is unbuffered and only
// re-armed by a fresh Update call, quitting mid-stream permanently
// strands plan.Execute's own background goroutine (still blocked
// mid-run, trying to send its next progress event to nobody) and
// abandons whatever it already created with no CleanCheck/Clean/prompt
// at all -- the reviewer measured a permanently elevated goroutine count
// from exactly this. Esc/Ctrl+C while a submit is actively streaming (well
// before submitDoneMsg, let alone the dead end) must now be a no-op, and
// the pipeline must still be able to run to completion afterward.
func TestUpdateSubmitting_EscDuringActiveStreamingDoesNotQuit(t *testing.T) {
	runner := &submitFakeRunner{topo: herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"}}
	m := newSubmitTestModel(t, runner, testSetup{Ctx: herdrc.Context{WorkspaceCwd: "/repo"}})
	m.title.SetTitle("Fix pagination", false)
	m.worktree.SetGitTarget(true)
	m.worktree.SetOn(true)
	m.worktree.SetBranch("zvi/fix-pagination", false)
	m.prompt.SetValue("do it", false) // more than one op, so there's real "mid-stream" room.

	next, cmd := m.Update(form.SubmitMsg{})
	m = next.(Model)
	if !m.submitting {
		t.Fatal("submit did not start")
	}

	// Apply exactly the FIRST streamed event -- plan.Execute's own
	// background goroutine is genuinely still running now, nowhere near
	// done, exactly the state the original bug quit out from under.
	msg := cmd()
	pm, ok := msg.(submitProgressMsg)
	if !ok {
		t.Fatalf("first submit message = %#v, want submitProgressMsg", msg)
	}
	var nextCmd tea.Cmd
	m, nextCmd = m.handleSubmitProgress(pm)
	if m.submitDeadEnd {
		t.Fatal("submitDeadEnd is true mid-stream, want false (nothing has failed yet)")
	}

	for _, escKey := range []tea.KeyPressMsg{{Code: tea.KeyEscape}, key('c', tea.ModCtrl)} {
		_, escCmd := m.updateSubmitting(escKey)
		if escCmd == nil {
			continue
		}
		if _, isQuit := escCmd().(tea.QuitMsg); isQuit {
			t.Fatalf("updateSubmitting(%v) mid-stream returned tea.Quit, want it ignored (would strand plan.Execute's own goroutine)", escKey)
		}
	}

	// The pipeline must still run to completion afterward -- nothing was
	// actually interrupted by the ignored Esc/Ctrl+C above.
	_, _, done := drainSubmitProgress(t, m, nextCmd)
	if done.result.FailedIndex != -1 {
		t.Fatalf("unexpected failure after ignored Esc/Ctrl+C mid-stream: %+v", done.result)
	}
}

// TestSubmit_CleanAllowedCallsPlanCleanAndQuits covers the ALLOWED half of
// handleCleanRequested (the denied half is TestSubmit_CleanMsgOnDeniedCheckDoesNothing
// above): a CleanMsg with an allowing decision must actually call
// plan.Clean against the recorded submitInput/submitCreated, then quit
// once it completes.
func TestSubmit_CleanAllowedCallsPlanCleanAndQuits(t *testing.T) {
	runner := &submitFakeRunner{}
	m := newSubmitTestModel(t, runner, testSetup{})
	m.submitting = true
	m.submitCleanDecision = plan.CleanDecision{Allowed: true}
	m.submitInput = plan.Input{UseWorktree: false}
	m.submitCreated = herdrc.CreatedTopology{WorkspaceID: "ws-1"}

	next, cmd := m.Update(form.CleanMsg{})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("Update(CleanMsg{}) with an allowed CleanDecision returned a nil cmd, want the plan.Clean cmd")
	}
	msg := cmd()
	cdMsg, ok := msg.(cleanDoneMsg)
	if !ok {
		t.Fatalf("Update(CleanMsg{})'s cmd produced %#v, want cleanDoneMsg", msg)
	}
	if len(runner.calls) != 1 || runner.calls[0] != "WorkspaceClose" {
		t.Fatalf("runner.calls = %v, want exactly [WorkspaceClose] (non-worktree Clean, spec §9)", runner.calls)
	}

	_, quitCmd := m.updateSubmitting(cdMsg)
	if quitCmd == nil {
		t.Fatal("updateSubmitting(cleanDoneMsg) returned a nil cmd, want tea.Quit")
	}
	if _, ok := quitCmd().(tea.QuitMsg); !ok {
		t.Fatal("updateSubmitting(cleanDoneMsg) did not return tea.Quit")
	}
}

// TestSubmit_CleanFailureSurfacesErrorAndStaysInPrompt is fix round 1's
// own regression test (reviewer finding -- silent failure): a failed
// plan.Clean call must NOT quit -- quitting either way made a failed
// clean indistinguishable from a successful one. It must instead surface
// the error on SubmitView (SetCleanFailed) and stay on the failure
// prompt, so the k/c choice ("c" retry, "k" give up and keep) is still
// available.
func TestSubmit_CleanFailureSurfacesErrorAndStaysInPrompt(t *testing.T) {
	cleanErr := errors.New("herdr: workspace not found")
	runner := &submitFakeRunner{failAt: "WorkspaceClose", failErr: cleanErr}
	m := newSubmitTestModel(t, runner, testSetup{})
	m.submitting = true
	m.submitCleanDecision = plan.CleanDecision{Allowed: true}
	m.submitInput = plan.Input{UseWorktree: false}
	m.submitCreated = herdrc.CreatedTopology{WorkspaceID: "ws-1"}
	m.submitView = form.NewSubmitView(m.palette)
	m.submitView.SetFailure(plan.ExecResult{FailedIndex: 0}, m.submitCleanDecision)

	next, cmd := m.Update(form.CleanMsg{})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("Update(CleanMsg{}) returned a nil cmd, want the plan.Clean cmd")
	}
	msg := cmd()
	cdMsg, ok := msg.(cleanDoneMsg)
	if !ok {
		t.Fatalf("Update(CleanMsg{})'s cmd produced %#v, want cleanDoneMsg", msg)
	}
	if cdMsg.err == nil {
		t.Fatal("cleanDoneMsg.err = nil, want the configured failure")
	}

	m, doneCmd := m.updateSubmitting(cdMsg)
	if doneCmd != nil {
		if _, isQuit := doneCmd().(tea.QuitMsg); isQuit {
			t.Fatal("updateSubmitting(cleanDoneMsg) after a Clean failure returned tea.Quit, want it to stay on the failure prompt")
		}
	}

	frame := ansi.Strip(m.submitView.ViewAt(80, 24))
	if !strings.Contains(frame, "clean failed") || !strings.Contains(frame, "herdr: workspace not found") {
		t.Fatalf("SubmitView.ViewAt(80,24) after a Clean failure = %q, want the error surfaced", frame)
	}
	if !strings.Contains(frame, "k keep") || !strings.Contains(frame, "c clean") {
		t.Fatalf("SubmitView.ViewAt(80,24) after a Clean failure = %q, want the k/c choice still available", frame)
	}
}

// TestUpdateSubmitting_KeepMsgQuits covers form.KeepMsg reaching
// tea.Quit through the real top-level Update dispatch (not just
// updateSubmitting called directly).
func TestUpdateSubmitting_KeepMsgQuits(t *testing.T) {
	m := newSubmitTestModel(t, &submitFakeRunner{}, testSetup{})
	m.submitting = true

	next, cmd := m.Update(form.KeepMsg{})
	_ = next.(Model)
	if cmd == nil {
		t.Fatal("Update(KeepMsg{}) while submitting returned a nil cmd, want tea.Quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("Update(KeepMsg{}) while submitting did not return tea.Quit")
	}
}

// TestUpdateSubmitting_KeyForwardsToSubmitView covers a non-Esc/Ctrl+C
// key (here, "k" on a failed SubmitView) reaching SubmitView's own
// Update, end to end through Update(form.KeepMsg{}) firing.
func TestUpdateSubmitting_KeyForwardsToSubmitView(t *testing.T) {
	m := newSubmitTestModel(t, &submitFakeRunner{}, testSetup{})
	m.submitting = true
	m.submitView = form.NewSubmitView(m.palette)
	m.submitView.SetFailure(plan.ExecResult{FailedIndex: 0}, plan.CleanDecision{Allowed: true})

	m, cmd := m.updateSubmitting(tea.KeyPressMsg{Code: 'k', Text: "k"})
	if cmd == nil {
		t.Fatal("updateSubmitting('k') on a failed SubmitView returned a nil cmd, want KeepMsg")
	}
	if _, ok := cmd().(form.KeepMsg); !ok {
		t.Fatalf("updateSubmitting('k')'s cmd produced %#v, want form.KeepMsg", cmd())
	}
}

// TestNew_AppliesConfigDefaultPlacement pins gap 2's PlacementField half
// at the app layer (field_placement_test.go's own TestPlacementField_SetValue
// covers the field in isolation): New must apply a non-default
// `default_placement` config value to the assembled Placement section.
func TestNew_AppliesConfigDefaultPlacement(t *testing.T) {
	m := newSubmitTestModel(t, &submitFakeRunner{}, testSetup{
		Config: config.Config{DefaultPlacement: "split-here"},
	})
	if got := m.placement.Value(); got != plan.PlacementSplitHere {
		t.Fatalf("placement.Value() after New with default_placement=%q = %v, want %v", "split-here", got, plan.PlacementSplitHere)
	}
}

// TestNew_DefaultPlacementNewSpaceNeedsNoCall pins the "no call needed"
// half of placementFromConfigValue: the config's own documented default
// ("new-space") and an omitted key must both leave Placement at its
// already-correct starting default.
func TestNew_DefaultPlacementNewSpaceNeedsNoCall(t *testing.T) {
	for _, cfgValue := range []string{"", "new-space"} {
		m := newSubmitTestModel(t, &submitFakeRunner{}, testSetup{
			Config: config.Config{DefaultPlacement: cfgValue},
		})
		if got := m.placement.Value(); got != plan.PlacementNewSpace {
			t.Errorf("placement.Value() after New with default_placement=%q = %v, want %v", cfgValue, got, plan.PlacementNewSpace)
		}
	}
}

// TestNew_AppliesConfigDefaultClauthPin pins gap 2's AccountField half at
// the app layer (field_account_test.go's own TestAccountField_SetPin
// covers the field in isolation): New must apply a `[clauth] default`
// profile name to the assembled Account section.
func TestNew_AppliesConfigDefaultClauthPin(t *testing.T) {
	m := newSubmitTestModel(t, &submitFakeRunner{}, testSetup{
		Config: config.Config{Clauth: config.ClauthConfig{Default: "work"}},
		ClauthStatus: clauth.Status{Profiles: []clauth.Profile{
			{Name: "active-ish", AuthStatus: "ok"},
			{Name: "work", AuthStatus: "ok"},
		}},
		Clauth: &fakeClauth{},
	})
	if m.account == nil {
		t.Fatal("m.account is nil, want it constructed (>=2 profiles + Clauth configured)")
	}
	if got := m.account.Pin(); got != "work" {
		t.Fatalf("account.Pin() after New with [clauth] default=%q = %q, want %q", "work", got, "work")
	}
}

// TestHandleClearRequested_ViewNonEmptyAfterClear is fix round 1's own
// regression test (reviewer finding, empirically confirmed against a
// running Model: a 4496-byte View() before Clear, 0 bytes after): New's
// own Model -- and, critically, the form.Model NESTED inside it -- both
// start with a zero-value 0x0 width/height, set only by a real
// tea.WindowSizeMsg reaching Update. A real terminal only ever sends one
// on startup/resize, never on a Clear, so the ORIGINAL handleClearRequested
// (just `return fresh, fresh.Init()`) rebuilt a Model whose nested
// form.Model had never seen a size at all -- View() rendered "" until the
// next real resize. Field values reset correctly underneath; the screen
// just went blank, defeating the whole feature. This pins that View()
// stays non-empty AND keeps showing the (re-seeded) context-derived
// project directory across a Clear, once a real size has already been
// established.
func TestHandleClearRequested_ViewNonEmptyAfterClear(t *testing.T) {
	m := newSubmitTestModel(t, &submitFakeRunner{}, testSetup{
		Ctx: herdrc.Context{WorkspaceCwd: "/repo"},
	})

	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = next.(Model)

	before := m.View().Content
	if before == "" {
		t.Fatal("test setup: View() is empty even BEFORE Clear -- fix the test, it can't distinguish the bug from a broken setup")
	}

	next, cmd := m.Update(form.ClearRequestedMsg{})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("Update(ClearRequestedMsg{}) returned a nil cmd, want the rebuilt form's own Init cmd")
	}

	after := m.View().Content
	if after == "" {
		t.Fatalf("View() is empty after Clear (was %d bytes before), want it to stay rendered at the last-known %dx%d size", len(before), 120, 40)
	}
	if got := ansi.Strip(after); !strings.Contains(got, "/repo") {
		t.Fatalf("View() after Clear = %q, want it to contain the re-seeded, context-derived project dir %q", got, "/repo")
	}
}

// TestHandleClearRequested_RebuildsToSeededStateNotZeroValues pins gap
// 1's own core promise (this task's brief: "reset the fields to their
// startup/seeded state (config defaults + context-derived values), not
// to empty zero values"): after a user has typed into Title (diverging
// from the seeded/default state) and Ctrl+R Ctrl+R fires,
// form.ClearRequestedMsg must rebuild the form back to what New would
// have produced from the SAME config/context/already-fetched data --
// project directory back to its context-derived default, Placement back
// to its configured default -- not to each field's own bare zero value.
func TestHandleClearRequested_RebuildsToSeededStateNotZeroValues(t *testing.T) {
	m := newSubmitTestModel(t, &submitFakeRunner{}, testSetup{
		Ctx:    herdrc.Context{WorkspaceCwd: "/repo"},
		Config: config.Config{DefaultPlacement: "tab-here"},
	})
	wantDir := m.dir.Value()
	wantPlacement := m.placement.Value()
	if wantDir == "" {
		t.Fatal("test setup produced an empty default project dir -- fix the test, it can't distinguish seeded from zero-value with this")
	}

	m.title.SetTitle("something the user typed", false)
	m.placement.Update(key(tea.KeyRight, 0)) // diverge Placement from its configured default too.
	if got := m.placement.Value(); got == wantPlacement {
		t.Fatalf("test setup: diverging Placement via a key press had no effect, fix the test")
	}

	next, cmd := m.Update(form.ClearRequestedMsg{})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("Update(ClearRequestedMsg{}) returned a nil cmd, want the rebuilt form's own Init cmd")
	}

	if got := m.title.Value(); got != "" {
		t.Fatalf("title.Value() after clear = %q, want \"\" (fresh field, no config-derived title default exists)", got)
	}
	if got := m.dir.Value(); got != wantDir {
		t.Fatalf("dir.Value() after clear = %q, want the context-derived default %q, not a bare zero value", got, wantDir)
	}
	if got := m.placement.Value(); got != wantPlacement {
		t.Fatalf("placement.Value() after clear = %v, want the config-derived default %v, not PlacementNewSpace's bare zero value", got, wantPlacement)
	}
}

// containsAll reports whether s contains every one of subs.
func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

// --- finding I2: the state layer is finally called ------------------------

// TestSubmit_PersistsStateAndFeedsItBackIntoTheNextFormOpen pins the hook
// the whole state layer was missing: config.SaveState and
// State.TouchRecent were built and unit-tested in Task 10 and then called
// from nowhere, so recents.json and last-used.json were never written --
// spec §6 field 2's recents candidate source was permanently empty, and
// State.LastKind/LastPlacement/LastWorktree were dead on both sides.
//
// The test drives both halves: a successful submit writes the state, and a
// fresh form-open reading that state back defaults to it.
func TestSubmit_PersistsStateAndFeedsItBackIntoTheNextFormOpen(t *testing.T) {
	m := newSubmitTestModel(t, &submitFakeRunner{}, testSetup{
		Ctx:    herdrc.Context{WorkspaceCwd: "/repo"},
		Config: config.Config{Agents: config.AgentsConfig{Favorites: []string{"claude", "codex"}}},
	})
	stateDir := m.stateDir

	// Nothing on disk before a submit succeeds.
	if before, _ := config.LoadState(stateDir); len(before.Recents) != 0 || before.LastKind != "" {
		t.Fatalf("state dir was not empty before the submit: %+v", before)
	}

	m.submitInput = plan.Input{
		ProjectDir:  "/repo/project",
		Title:       "Fix pagination",
		AgentKind:   "codex",
		Placement:   plan.PlacementTabHere,
		UseWorktree: true,
	}
	_, cmd := m.handleSubmitDone(submitDoneMsg{result: plan.ExecResult{FailedIndex: -1}})
	if _, ok := cmd().(statePersistedMsg); !ok {
		t.Fatal("handleSubmitDone on success did not run the state write")
	}

	saved, _ := config.LoadState(stateDir)
	if len(saved.Recents) != 1 || saved.Recents[0] != "/repo/project" {
		t.Fatalf("Recents = %v, want the submitted project directory first", saved.Recents)
	}
	if saved.LastKind != "codex" {
		t.Errorf("LastKind = %q, want %q", saved.LastKind, "codex")
	}
	if saved.LastPlacement != "tab-here" {
		t.Errorf("LastPlacement = %q, want %q", saved.LastPlacement, "tab-here")
	}
	if saved.LastWorktree == nil || !*saved.LastWorktree {
		t.Errorf("LastWorktree = %v, want a recorded true", saved.LastWorktree)
	}

	// The read side: a fresh form-open defaults to what was just recorded.
	next := newTestModel(t, testSetup{
		Ctx:    herdrc.Context{WorkspaceCwd: "/repo"},
		Config: config.Config{Agents: config.AgentsConfig{Favorites: []string{"claude", "codex"}}},
		State:  saved,
	})
	if got := next.agent.Value(); got != "codex" {
		t.Errorf("a fresh form's agent kind = %q, want the last-used %q", got, "codex")
	}
	if got := next.placement.Value(); got != plan.PlacementTabHere {
		t.Errorf("a fresh form's placement = %v, want the last-used tab-here", got)
	}
	if !next.resolved.UseWorktree {
		t.Error("a fresh form's worktree default = false, want the last-used true")
	}
	// The recents' own destination: New feeds State.Recents into
	// buildDirCandidates, which is DirField's entire candidate pool (spec
	// §6 field 2's third candidate source). That source was permanently
	// empty for as long as nothing wrote recents.json.
	candidates := buildDirCandidates(next.ctx, next.workspaces, next.state.Recents)
	if !containsString(candidates, "/repo/project") {
		t.Errorf("a fresh form's project candidates = %v, want the recent %q among them",
			candidates, "/repo/project")
	}
}

// TestSubmit_FailedSubmitPersistsNothing guards the other side: a failed
// submit says nothing about what the user wants next time, so it must not
// overwrite the state a previous successful one recorded.
func TestSubmit_FailedSubmitPersistsNothing(t *testing.T) {
	m := newSubmitTestModel(t, &submitFakeRunner{}, testSetup{Ctx: herdrc.Context{WorkspaceCwd: "/repo"}})
	m.submitInput = plan.Input{ProjectDir: "/repo/project", AgentKind: "codex"}

	created := herdrc.CreatedTopology{WorkspaceID: "w1", PaneID: "w1:p1"}
	_, cmd := m.handleSubmitDone(submitDoneMsg{result: plan.ExecResult{FailedIndex: 1, Created: &created}})
	if cmd != nil {
		// Whatever the failure path returns, it must not be the state write.
		if _, ok := cmd().(statePersistedMsg); ok {
			t.Fatal("a failed submit persisted last-used state")
		}
	}

	saved, _ := config.LoadState(m.stateDir)
	if len(saved.Recents) != 0 || saved.LastKind != "" {
		t.Fatalf("a failed submit wrote state: %+v", saved)
	}
}

func containsString(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// --- finding I6: an unsent prompt survives the popup ---------------------

// TestSubmit_UnsentPromptIsSavedForManualPaste pins spec §9 step 3's
// "prompt text surfaced back to the user for manual paste" as something
// that outlives the popup. It used to be rendered inline through fitLine,
// whose Inline(true) strips newlines and whose MaxWidth hard-clips to the
// popup width -- so a multi-paragraph Linear-seeded prompt became one
// glued, truncated line, and then the popup closed and it was gone.
func TestSubmit_UnsentPromptIsSavedForManualPaste(t *testing.T) {
	m := newSubmitTestModel(t, &submitFakeRunner{}, testSetup{Ctx: herdrc.Context{WorkspaceCwd: "/repo"}})
	m.submitView = form.NewSubmitView(m.palette)
	m.submitting = true

	prompt := "Work on ENG-1: Fix login\n\nhttps://linear.app/x/ENG-1\n\nLong description here."
	created := herdrc.CreatedTopology{WorkspaceID: "w1", PaneID: "w1:p1"}
	m.submitInput = plan.Input{ProjectDir: "/repo", Prompt: prompt}

	m2, cmd := m.handleSubmitDone(submitDoneMsg{result: plan.ExecResult{
		FailedIndex: 2,
		Created:     &created,
		PromptText:  prompt,
	}})
	m = m2
	if cmd == nil {
		t.Fatal("a prompt-step failure returned no follow-up Cmd at all")
	}

	saved := findPromptSavedMsg(t, cmd)
	if saved.err != nil {
		t.Fatalf("saving the unsent prompt failed: %v", saved.err)
	}
	body, err := os.ReadFile(saved.path)
	if err != nil {
		t.Fatalf("read the saved prompt at %s: %v", saved.path, err)
	}
	if string(body) != prompt {
		t.Fatalf("saved prompt = %q, want the full text including its newlines %q", string(body), prompt)
	}

	// The keep-or-clean gate is what actually puts the failure prompt on
	// screen (handleCleanCheckResult -> SubmitView.SetFailure), so drive it
	// before rendering -- the recovery line lives inside that prompt.
	result := plan.ExecResult{FailedIndex: 2, Created: &created, PromptText: prompt}
	m3, _ := m.handleCleanCheckResult(cleanCheckMsg{result: result, decision: plan.CleanDecision{Allowed: true}})
	m4, _ := m3.handlePromptSaved(saved)
	// The rendered path itself is pinned at the view layer
	// (TestSubmitView_UnsentPromptSurfacedAsARecoverablePath, with a short
	// path a popup width can hold); here it is enough that the failure view
	// tells the user the prompt was kept rather than lost, and that the
	// prompt's own body is not pasted into the frame.
	frame := ansi.Strip(m4.submitView.ViewAt(80, 24))
	if !strings.Contains(frame, "prompt not sent") {
		t.Errorf("the failure view does not mention the unsent prompt:\n%s", frame)
	}
	if strings.Contains(frame, "Long description here.") {
		t.Errorf("the failure view pasted the prompt body into the frame:\n%s", frame)
	}
}

// findPromptSavedMsg runs cmd (a Cmd or a tea.BatchMsg of them) and returns
// the promptSavedMsg it produced.
func findPromptSavedMsg(t *testing.T, cmd tea.Cmd) promptSavedMsg {
	t.Helper()
	switch msg := cmd().(type) {
	case promptSavedMsg:
		return msg
	case tea.BatchMsg:
		for _, c := range msg {
			if c == nil {
				continue
			}
			if got, ok := c().(promptSavedMsg); ok {
				return got
			}
		}
	}
	t.Fatal("no promptSavedMsg was produced for a failed prompt step")
	return promptSavedMsg{}
}
