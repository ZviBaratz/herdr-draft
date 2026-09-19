package form

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ZviBaratz/herdr-draft/internal/plan"
	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// assertSubmitFrame mirrors form_test.go's own assertFrame (same *update
// flag, same testdata/frames path convention), adapted for *SubmitView
// rather than Model since SubmitView is deliberately not a form.Section/
// Model (see submitview.go's own file doc comment).
func assertSubmitFrame(t *testing.T, name string, v *SubmitView, w, h int) {
	t.Helper()
	got := v.ViewAt(w, h)
	path := filepath.Join("testdata", "frames", name+".txt")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil || string(want) != got {
		t.Errorf("frame %s mismatch (run with -update to regenerate)\n%s", name, got)
	}
}

// newSubmitTestView builds a SubmitView carrying the same header the form
// does, so every frame below exercises v2 spec §12's "same header, rule,
// label column and button row as the form".
func newSubmitTestView() *SubmitView {
	v := NewSubmitView(theme.Default())
	v.SetHeader("new session", "herdr-draft · main")
	return v
}

// sampleStepsRunning is v2 spec §12's own progress mockup: a finished
// worktree and workspace, a running agent, a queued prompt.
func sampleStepsRunning() []Step {
	return []Step{
		{Label: "worktree", Detail: "zvi/fix-login-redirect-loop from main", State: plan.StepDone},
		{Label: "workspace", Detail: "fix login redirect loop", State: plan.StepDone},
		{Label: "claude", Detail: "starting under clauth alpha-2", State: plan.StepRunning},
		{Label: "prompt", State: plan.StepPending},
	}
}

// sampleStepsWaiting is the same stack with the agent step waiting on the
// person at the keyboard -- #115's new state, where the launch found the
// agent on a dialog only they can answer.
func sampleStepsWaiting() []Step {
	steps := sampleStepsRunning()
	steps[2] = Step{Label: "claude", Detail: "fix-login-redirect-loop", State: plan.StepWaiting}
	return steps
}

// sampleStepsFailed is the same stack with the agent step failed, which
// is the state both failure frames below are taken from.
func sampleStepsFailed() []Step {
	steps := sampleStepsRunning()
	steps[2] = Step{Label: "claude", Detail: "agent_pane_busy after 5s", State: plan.StepFailed}
	return steps
}

func strippedFrame(v *SubmitView, w, h int) string {
	return ansi.Strip(v.ViewAt(w, h))
}

func strippedFrameLines(v *SubmitView, w, h int) []string {
	return strings.Split(strippedFrame(v, w, h), "\n")
}

// cellColumnOf is the TERMINAL COLUMN sub starts at within line, or -1
// when it is absent. Byte offsets are useless for this: a "✓" glyph is
// three bytes and one cell, so strings.Index alone reports a submit row's
// label two positions to the right of an identically aligned form row's.
func cellColumnOf(line, sub string) int {
	i := strings.Index(line, sub)
	if i < 0 {
		return -1
	}
	return ansi.StringWidth(line[:i])
}

// --- golden frames ---------------------------------------------------------

func TestFrames_Progress(t *testing.T) {
	v := newSubmitTestView()
	v.SetSteps(sampleStepsRunning())
	assertSubmitFrame(t, "progress-80x24", v, 80, 24)
}

// TestFrames_FailureCleanOffered is v2 spec §12's failure screen with
// both buttons live: the stack stays, the failed row carries the reason,
// and the choice is a button row on the footer.
//
// The stack is a worktree plan's, so the decision says what CleanCheck says
// for one this run made the branch of: remove deletes that branch too (#173).
func TestFrames_FailureCleanOffered(t *testing.T) {
	v := newSubmitTestView()
	v.SetSteps(sampleStepsFailed())
	v.SetFailure(plan.ExecResult{FailedIndex: 2}, plan.CleanDecision{Allowed: true, Branch: plan.BranchDeleted})
	assertSubmitFrame(t, "failure-keep-clean-80x24", v, 80, 24)
}

// TestFrames_FailureCleanDenied is the other half: remove is disabled,
// its key glyph is gone with it, and the reason sits directly above the
// buttons.
func TestFrames_FailureCleanDenied(t *testing.T) {
	v := newSubmitTestView()
	v.SetSteps(sampleStepsFailed())
	v.SetFailure(plan.ExecResult{FailedIndex: 2}, plan.CleanDecision{Allowed: false, Reason: "uncommitted changes"})
	assertSubmitFrame(t, "failure-clean-denied-80x24", v, 80, 24)
}

// TestFrames_CleanedKeepingABranch is a remove that finished without the
// branch it said it would delete (#173): the checkout and its workspace are
// gone, the branch holds something, and nothing is left to decide, so the
// footer offers only `esc close`.
func TestFrames_CleanedKeepingABranch(t *testing.T) {
	v := newSubmitTestView()
	v.SetSteps(sampleStepsFailed())
	v.SetFailure(plan.ExecResult{FailedIndex: 2}, plan.CleanDecision{Allowed: true, Branch: plan.BranchDeleted})
	v.SetCleanedKeepingBranch("zvi/fix-login-redirect-loop", "it holds 1 commit(s) nothing else does")
	assertSubmitFrame(t, "failure-cleaned-branch-kept-80x24", v, 80, 24)
}

// TestFrames_DeadEndWithUnsentPrompt is the failure with nothing to decide
// about: step 1 itself failed and reported no space, so there is nothing
// for keep or remove to act on and the footer offers only `esc close`.
//
// It carries an unsent prompt, which is new. While
// plan.ExecResult.PromptText meant "the prompt op failed", this state could
// never have one -- reaching the prompt op meant step 1 had already
// succeeded -- so the dead-end body was a single line and the recovery path
// had nowhere to appear. #90 generalised PromptText to "the prompt did not
// land", and this is the state where losing it hurts most: `esc` is the
// only way out and it closes the popup.
//
// It is #208's case too: git made the branch and then refused the checkout
// path, and herdr's error is all the plan has, which is no evidence that
// nothing was made. So the dead-end line is the longer one naming what may
// be left, wrapped rather than clipped, since the branch is what the user
// goes to look for.
//
// The frame pins the ORDER. regionLines clips this stack from the top, so
// the lines explaining the one available button have to be last.
func TestFrames_DeadEndWithUnsentPrompt(t *testing.T) {
	v := newSubmitTestView()
	v.SetSteps([]Step{
		{Label: "worktree", Detail: "fatal: '/wt/herdr-draft/zvi-fix-login-redirect-loop' already exists", State: plan.StepFailed},
		{Label: "claude", State: plan.StepPending},
		{Label: "prompt", State: plan.StepPending},
	})
	v.SetDeadEnd(plan.ExecResult{FailedIndex: 0, PromptText: "Work on ENG-101: Fix login redirect loop"}, true, "zvi/fix-login-redirect-loop")
	v.SetUnsentPrompt("/state/herdr/zvibaratz.draft/unsent-prompt.txt", nil)
	assertSubmitFrame(t, "failure-dead-end-prompt-80x24", v, 80, 24)
}

// TestFrames_DeadEndNothingCreated is the dead end with evidence
// (ExecResult.NothingCreated): herdr refused the worktree before running
// git, so "nothing was created" is a fact and the only dead end that may
// say it (#208). No frame pinned the line on its own before; the frame
// above carried it only beside an unsent prompt.
func TestFrames_DeadEndNothingCreated(t *testing.T) {
	v := newSubmitTestView()
	v.SetSteps([]Step{
		{Label: "worktree", Detail: "linked_worktree_source: New and open worktree actions start from the repo parent workspace.", State: plan.StepFailed},
		{Label: "tab", State: plan.StepPending},
		{Label: "claude", State: plan.StepPending},
	})
	v.SetDeadEnd(plan.ExecResult{FailedIndex: 0, NothingCreated: true}, true, "zvi/fix-login-redirect-loop")
	assertSubmitFrame(t, "failure-dead-end-nothing-created-80x24", v, 80, 24)
}

// TestFrames_DeadEndWithoutEvidence is #208's line at the width the popup
// ships at, with no unsent prompt above it: the wrap falls somewhere else
// at 101 cells, and the branch must still come out whole on one line.
func TestFrames_DeadEndWithoutEvidence(t *testing.T) {
	v := newSubmitTestView()
	v.SetSteps([]Step{
		{Label: "worktree", Detail: "fatal: '/wt/herdr-draft/zvi-fix-login-redirect-loop' already exists", State: plan.StepFailed},
		{Label: "tab", State: plan.StepPending},
		{Label: "claude", State: plan.StepPending},
	})
	v.SetDeadEnd(plan.ExecResult{FailedIndex: 0}, true, "zvi/fix-login-redirect-loop")
	assertSubmitFrame(t, "failure-dead-end-101x30", v, 101, 30)
}

// TestFrames_DeadEndAtTheSmallestPopup is the tallest dead-end body -- an
// unsent prompt over #208's wrapped line -- at 57x18, the popup herdr
// leaves on a 60x20 terminal. It is the size where regionLines' clipping
// from the top would start to matter, so the frame pins what survives.
func TestFrames_DeadEndAtTheSmallestPopup(t *testing.T) {
	v := newSubmitTestView()
	v.SetSteps([]Step{
		{Label: "worktree", Detail: "fatal: '/wt/herdr-draft/zvi-fix-login-redirect-loop' already exists", State: plan.StepFailed},
		{Label: "tab", State: plan.StepPending},
		{Label: "claude", State: plan.StepPending},
		{Label: "prompt", State: plan.StepPending},
	})
	v.SetDeadEnd(plan.ExecResult{FailedIndex: 0, PromptText: "Work on ENG-101: Fix login redirect loop"}, true, "zvi/fix-login-redirect-loop")
	v.SetUnsentPrompt("/state/herdr/zvibaratz.draft/unsent-prompt.txt", nil)
	assertSubmitFrame(t, "failure-dead-end-prompt-57x18", v, 57, 18)
}

// TestFrames_FailureUnconfirmedPrompt is #108's state, and it exists
// because a green suite proves only the states someone thought to
// fixture: the two frames that pinned this region both carried the
// not-sent wording, so the whole unconfirmed branch would have rendered
// unseen.
//
// What the frame is actually for is the two things a screenshot can check
// and an assertion cannot. The lead must not read "prompt not sent" -- the
// claim #108 is about -- and the instruction that replaces "paste this"
// has to survive the width: these lines are indented inside a bordered
// panel, so a lead that fits in isolation can still push "read the pane"
// off the end, which is the half the user acts on.
//
// SetFailure rather than SetDeadEnd: a prompt-wait timeout happens at the
// LAST step, so a session exists and the keep-or-remove choice is live --
// and `clean` is the choice CleanCheck now refuses, whose reason shows
// here.
func TestFrames_FailureUnconfirmedPrompt(t *testing.T) {
	v := newSubmitTestView()
	v.SetSteps(sampleStepsFailed())
	v.SetFailure(
		plan.ExecResult{
			FailedIndex:       2,
			PromptText:        "Work on ENG-101: Fix login redirect loop",
			PromptUnconfirmed: true,
		},
		plan.CleanDecision{
			Allowed: false,
			Reason:  "the prompt may already have been delivered",
		},
	)
	v.SetUnsentPrompt("/state/herdr/zvibaratz.draft/unsent-prompt.txt", nil)
	assertSubmitFrame(t, "failure-unconfirmed-prompt-80x24", v, 80, 24)
	// And at the width the popup actually ships at. Every failure frame in
	// this package was pinned at 80x24 only, while every form frame was
	// pinned at the shipped 101x30 -- so the screen whose whole risk is
	// "the message is truncated to the column" had no fixture at the
	// column it ships in. 80x24 stays as the pessimistic clamp.
	assertSubmitFrame(t, "failure-unconfirmed-prompt-101x30", v, 101, 30)
}

// --- v2 spec §12: the same chrome as the form ------------------------------

// TestSubmitView_LabelColumnMatchesTheForm is the whole point of v2 spec
// §12 ("same header, rule, label column and button row as the form, so
// the pipeline does not read as a different program"), pinned rather than
// eyeballed: a step row's label and value must start at exactly the same
// columns a form row's do, at every width, so a submit that replaces the
// form on screen does not shift the grid sideways under the user.
func TestSubmitView_LabelColumnMatchesTheForm(t *testing.T) {
	palette := theme.Default()

	steps := []Step{
		{Label: "worktree", Detail: "WWWWWWWW", State: plan.StepDone},
		{Label: "prompt", Detail: "WWWWWWWW", State: plan.StepPending},
	}

	for _, w := range []int{80, 120, 200, 64, 40} {
		m, stubs := buildRowForm(palette, "worktree", "prompt")
		stubs[0].value = "WWWWWWWW"
		stubs[1].value = "WWWWWWWW"

		v := NewSubmitView(palette)
		v.SetHeader("new session", "herdr-draft · main")
		v.SetSteps(steps)

		formLines := strippedLines(m, w, 24)
		submitLines := strippedFrameLines(v, w, 24)

		// Row 0 of each stack: the form's first field row and the submit
		// view's first step row both sit two lines below the header.
		const stackTop = 2
		for i := range steps {
			formRow, submitRow := formLines[stackTop+i], submitLines[stackTop+i]
			if got, want := cellColumnOf(submitRow, steps[i].Label), cellColumnOf(formRow, steps[i].Label); got != want {
				t.Errorf("w=%d row %d: submit label %q starts at column %d, the form's at %d\n form:   %q\n submit: %q",
					w, i, steps[i].Label, got, want, formRow, submitRow)
			}
			if got, want := cellColumnOf(submitRow, "WWWWWWWW"), cellColumnOf(formRow, "WWWWWWWW"); got != want {
				t.Errorf("w=%d row %d: submit value starts at column %d, the form's at %d\n form:   %q\n submit: %q",
					w, i, got, want, formRow, submitRow)
			}
		}
	}
}

// TestSubmitView_HeaderMatchesTheForm pins submitHeaderLine against
// form.go's own renderHeaderLine -- the copy submitview.go's doc comment
// declares. If one grows a third element the other does not, this fails
// instead of the two quietly diverging.
func TestSubmitView_HeaderMatchesTheForm(t *testing.T) {
	palette := theme.Default()
	m, _ := buildRowForm(palette, "title")
	m.SetContext("herdr-draft · main")

	v := NewSubmitView(palette)
	v.SetHeader("new session", "herdr-draft · main")
	v.SetSteps([]Step{{Label: "title", State: plan.StepPending}})

	for _, w := range []int{80, 120, 40} {
		formHeader := strippedLines(m, w, 24)[0]
		submitHeader := strippedFrameLines(v, w, 24)[0]
		if formHeader != submitHeader {
			t.Errorf("w=%d header mismatch\n form:   %q\n submit: %q", w, formHeader, submitHeader)
		}
	}
}

// TestSubmitView_RuleMatchesTheForm pins the other piece of shared
// chrome: the rule under the header is the same run of the same rune, in
// the same columns.
func TestSubmitView_RuleMatchesTheForm(t *testing.T) {
	palette := theme.Default()
	m, _ := buildRowForm(palette, "title")

	v := NewSubmitView(palette)
	v.SetHeader("new session", "")
	v.SetSteps([]Step{{Label: "title", State: plan.StepPending}})

	for _, w := range []int{80, 120, 200} {
		formRule := strippedLines(m, w, 24)[1]
		submitRule := strippedFrameLines(v, w, 24)[1]
		if formRule != submitRule {
			t.Errorf("w=%d rule mismatch\n form:   %q\n submit: %q", w, formRule, submitRule)
		}
	}
}

// TestSubmitView_FooterIsTheLastLine pins the button row's position: the
// form never drops its footer and neither does this, so the buttons are
// always on the bottom line where the Create button was a keystroke ago.
func TestSubmitView_FooterIsTheLastLine(t *testing.T) {
	v := newSubmitTestView()
	v.SetSteps(sampleStepsFailed())
	v.SetFailure(plan.ExecResult{FailedIndex: 2}, plan.CleanDecision{Allowed: true})

	for _, h := range []int{24, 12, 6, 3, 1} {
		lines := strippedFrameLines(v, 80, h)
		if len(lines) != h {
			t.Fatalf("h=%d rendered %d lines, want %d", h, len(lines), h)
		}
		last := lines[h-1]
		if !strings.Contains(last, "keep it") {
			t.Errorf("h=%d last line = %q, want the button row", h, last)
		}
	}
}

// --- v2 spec §12: the step rows -------------------------------------------

func TestSubmitView_StepGlyphs(t *testing.T) {
	v := newSubmitTestView()
	v.SetSteps(sampleStepsRunning())

	lines := strippedFrameLines(v, 80, 24)
	for _, want := range []struct{ glyph, label string }{
		{"✓", "worktree"},
		{"✓", "workspace"},
		{"›", "claude"},
		{"", "prompt"},
	} {
		var row string
		for _, l := range lines {
			if strings.Contains(l, want.label) {
				row = l
				break
			}
		}
		if row == "" {
			t.Fatalf("no row for %q in\n%s", want.label, strings.Join(lines, "\n"))
		}
		glyphs := strings.TrimSpace(row[:strings.Index(row, want.label)])
		if glyphs != want.glyph {
			t.Errorf("row %q carries glyph %q, want %q", want.label, glyphs, want.glyph)
		}
	}
}

// TestSubmitView_PendingStepWithNoDetailReadsQueued pins v2 spec §12's
// own mockup for the prompt row: a step that has not started and has
// nothing to say yet says "queued".
func TestSubmitView_PendingStepWithNoDetailReadsQueued(t *testing.T) {
	v := newSubmitTestView()
	v.SetSteps([]Step{{Label: "prompt", State: plan.StepPending}})
	if frame := strippedFrame(v, 80, 24); !strings.Contains(frame, "prompt     queued") {
		t.Errorf("ViewAt(80,24) = %q, want a `prompt  queued` row", frame)
	}
}

// TestSubmitView_FailedRowCarriesTheReason pins "the failed row carries
// the reason" (v2 spec §12) -- the error goes in that row's value column,
// not into a separate banner.
func TestSubmitView_FailedRowCarriesTheReason(t *testing.T) {
	v := newSubmitTestView()
	v.SetSteps(sampleStepsFailed())
	v.SetFailure(plan.ExecResult{FailedIndex: 2}, plan.CleanDecision{Allowed: true})

	var row string
	for _, l := range strippedFrameLines(v, 80, 24) {
		if strings.Contains(l, "claude") {
			row = l
			break
		}
	}
	if !strings.Contains(row, "✗") || !strings.Contains(row, "agent_pane_busy after 5s") {
		t.Errorf("failed row = %q, want the ✗ glyph and the reason on the same row", row)
	}
}

// TestSubmitView_StackStaysOnFailure pins "on failure the stack stays":
// the successful steps above the failure are still on screen.
func TestSubmitView_StackStaysOnFailure(t *testing.T) {
	v := newSubmitTestView()
	v.SetSteps(sampleStepsFailed())
	v.SetFailure(plan.ExecResult{FailedIndex: 2}, plan.CleanDecision{Allowed: true})

	frame := strippedFrame(v, 80, 24)
	for _, want := range []string{"worktree", "workspace", "claude", "prompt"} {
		if !strings.Contains(frame, want) {
			t.Errorf("ViewAt(80,24) lost the %q row on failure:\n%s", want, frame)
		}
	}
}

// --- v2 spec §12: the keep-or-clean buttons -------------------------------

func TestSubmitView_NoFailureBeforeSetFailure(t *testing.T) {
	v := newSubmitTestView()
	v.SetSteps(sampleStepsRunning())
	frame := strippedFrame(v, 80, 24)
	if strings.Contains(frame, "keep it") || strings.Contains(frame, "remove it") {
		t.Errorf("ViewAt(80,24) before SetFailure = %q, want no keep/remove buttons", frame)
	}
	// The running pipeline's footer must not advertise an exit either --
	// the app layer ignores Esc/Ctrl+C at exactly this point.
	if strings.Contains(frame, "esc") {
		t.Errorf("ViewAt(80,24) mid-pipeline = %q, want no esc hint (the key is ignored there)", frame)
	}
}

func TestSubmitView_KPressEmitsKeepMsg(t *testing.T) {
	v := newSubmitTestView()
	v.SetSteps(sampleStepsFailed())
	v.SetFailure(plan.ExecResult{FailedIndex: 2}, plan.CleanDecision{Allowed: true})

	cmd := v.Update(key('k', 0))
	if cmd == nil {
		t.Fatalf("Update('k') returned nil, want a Cmd emitting KeepMsg")
	}
	if _, ok := cmd().(KeepMsg); !ok {
		t.Fatalf("Update('k')() = %T, want KeepMsg", cmd())
	}
}

func TestSubmitView_CPressEmitsCleanMsgWhenAllowed(t *testing.T) {
	v := newSubmitTestView()
	v.SetSteps(sampleStepsFailed())
	v.SetFailure(plan.ExecResult{FailedIndex: 2}, plan.CleanDecision{Allowed: true})

	cmd := v.Update(key('c', 0))
	if cmd == nil {
		t.Fatalf("Update('c') returned nil, want a Cmd emitting CleanMsg")
	}
	if _, ok := cmd().(CleanMsg); !ok {
		t.Fatalf("Update('c')() = %T, want CleanMsg", cmd())
	}
}

// TestSubmitView_CPressNoOpWhenCleanDenied pins v2 spec §12's "clean
// stays disabled with its reason shown" -- "c" must not emit CleanMsg
// when CleanDecision.Allowed is false.
func TestSubmitView_CPressNoOpWhenCleanDenied(t *testing.T) {
	v := newSubmitTestView()
	v.SetSteps(sampleStepsFailed())
	v.SetFailure(plan.ExecResult{FailedIndex: 2}, plan.CleanDecision{Allowed: false, Reason: "uncommitted changes"})

	if cmd := v.Update(key('c', 0)); cmd != nil {
		t.Errorf("Update('c') with clean denied returned a non-nil Cmd, want nil")
	}
	// "k" must still work even when clean is denied.
	cmd := v.Update(key('k', 0))
	if cmd == nil {
		t.Fatalf("Update('k') with clean denied returned nil, want KeepMsg still available")
	}
	if _, ok := cmd().(KeepMsg); !ok {
		t.Fatalf("Update('k')() = %T, want KeepMsg", cmd())
	}
}

func TestSubmitView_KeyBeforeFailureIsNoOp(t *testing.T) {
	v := newSubmitTestView()
	v.SetSteps(sampleStepsRunning())
	if cmd := v.Update(key('k', 0)); cmd != nil {
		t.Errorf("Update('k') before SetFailure returned a non-nil Cmd, want nil")
	}
	if cmd := v.Update(key('c', 0)); cmd != nil {
		t.Errorf("Update('c') before SetFailure returned a non-nil Cmd, want nil")
	}
}

// TestSubmitView_EscIsNeverAViewLevelExit pins the view half of the
// safety property v2 spec §12 restates: Esc/Ctrl+C are not this view's
// keys in ANY state. Whether they quit is the app layer's decision alone
// (updateSubmitting scopes it to the step-one dead end), because quitting
// mid-pipeline strands plan.Execute on an unbuffered channel send. A
// SubmitView that answered them would be a second, unscoped way out.
func TestSubmitView_EscIsNeverAViewLevelExit(t *testing.T) {
	states := map[string]func(*SubmitView){
		"running": func(v *SubmitView) { v.SetSteps(sampleStepsRunning()) },
		"dead end": func(v *SubmitView) {
			v.SetSteps(sampleStepsFailed())
			v.SetDeadEnd(plan.ExecResult{FailedIndex: 0}, false, "")
		},
		"keep-or-clean": func(v *SubmitView) {
			v.SetSteps(sampleStepsFailed())
			v.SetFailure(plan.ExecResult{FailedIndex: 2}, plan.CleanDecision{Allowed: true})
		},
	}
	for name, setup := range states {
		v := newSubmitTestView()
		setup(v)
		for _, msg := range []tea.KeyPressMsg{keyEsc, keyCtrlC} {
			if cmd := v.Update(msg); cmd != nil {
				t.Errorf("%s: Update(%v) returned a non-nil Cmd, want the view to leave the decision to the app layer", name, msg)
			}
		}
	}
}

// TestSubmitView_DeadEndOffersOnlyClose pins the one state Esc DOES quit
// from (the app layer's own scoping): no space was reported, so there is
// nothing for keep or remove to act on and the footer says so. This is the
// dead end with evidence that the first step made nothing
// (ExecResult.NothingCreated), which is the only one that may say so (#208).
func TestSubmitView_DeadEndOffersOnlyClose(t *testing.T) {
	v := newSubmitTestView()
	v.SetSteps([]Step{{Label: "workspace", Detail: "herdr: boom", State: plan.StepFailed}})
	v.SetDeadEnd(plan.ExecResult{FailedIndex: 0, NothingCreated: true}, false, "")

	frame := strippedFrame(v, 80, 24)
	if !strings.Contains(frame, "esc close") {
		t.Errorf("ViewAt(80,24) in the dead end = %q, want an `esc close` button", frame)
	}
	if strings.Contains(frame, "keep it") || strings.Contains(frame, "remove it") {
		t.Errorf("ViewAt(80,24) in the dead end = %q, want no keep/remove choice", frame)
	}
	if !strings.Contains(frame, "nothing was created") {
		t.Errorf("ViewAt(80,24) in the dead end = %q, want it to say nothing was created", frame)
	}
}

// TestSubmitView_DeadEndWithoutEvidenceSaysWhatMayBeLeft is #208's view
// half. A first step that failed with no space reported and no evidence
// that it made nothing is still a dead end, with only `esc close`, because
// there is no space for keep or remove to act on. But herdr can fail a
// worktree create after git has made the branch, so the line names what
// may be left instead of claiming nothing is -- `create`'s own wording
// (internal/create/report.go, failureLine), with the popup's dash.
func TestSubmitView_DeadEndWithoutEvidenceSaysWhatMayBeLeft(t *testing.T) {
	const branch = "zvi/fix-login-redirect-loop"
	cases := []struct {
		name     string
		worktree bool
		branch   string
		want     []string
	}{
		{
			name:     "worktree",
			worktree: true,
			branch:   branch,
			want: []string{
				"herdr may have made part of it before failing — any of the branch " + branch +
					", its checkout and a workspace for it; look before retrying",
			},
		},
		{
			// A branch row cleared by hand: herdr names the branch itself.
			name:     "worktree, no branch named",
			worktree: true,
			want: []string{
				"herdr may have made part of it before failing — any of a branch, its checkout and a workspace for it; look before retrying",
			},
		},
		{
			name: "no worktree",
			want: []string{"herdr may have made part of it before failing; look before retrying"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := newSubmitTestView()
			v.SetSteps([]Step{{Label: "worktree", Detail: "worktree_create_failed", State: plan.StepFailed}})
			v.SetDeadEnd(plan.ExecResult{FailedIndex: 0}, tc.worktree, tc.branch)

			frame := strippedFrame(v, 80, 24)
			// The region's lines joined, so a sentence the view wraps is
			// still one string to look in.
			joined := strings.Join(strings.Fields(frame), " ")
			for _, s := range tc.want {
				if !strings.Contains(joined, s) {
					t.Errorf("ViewAt(80,24) in the dead end = %q, want it to say %q", frame, s)
				}
			}
			if strings.Contains(frame, "nothing was created") {
				t.Errorf("ViewAt(80,24) in the dead end = %q, claims nothing was created with no evidence of it", frame)
			}
			if !strings.Contains(frame, "esc close") {
				t.Errorf("ViewAt(80,24) in the dead end = %q, want an `esc close` button", frame)
			}
			if strings.Contains(frame, "keep it") || strings.Contains(frame, "remove it") {
				t.Errorf("ViewAt(80,24) in the dead end = %q, want no keep/remove choice", frame)
			}
		})
	}
}

// TestSubmitView_DeadEndLinesWrapAtTheSmallestPopup holds both dead-end
// lines whole at 57x18, the popup herdr leaves on a 60x20 terminal. The
// evidence line is 56 cells, so a clipped line lost the end of "remove":
// the word that says why `esc close` is the only button.
func TestSubmitView_DeadEndLinesWrapAtTheSmallestPopup(t *testing.T) {
	cases := []struct {
		name string
		res  plan.ExecResult
		want string
	}{
		{
			name: "evidence",
			res:  plan.ExecResult{FailedIndex: 0, NothingCreated: true},
			want: "nothing was created — there is nothing to keep or remove",
		},
		{
			name: "no evidence",
			res:  plan.ExecResult{FailedIndex: 0},
			want: "herdr may have made part of it before failing — any of the branch zvi/fix-login-redirect-loop, its checkout and a workspace for it; look before retrying",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := newSubmitTestView()
			v.SetSteps([]Step{{Label: "worktree", Detail: "herdr: boom", State: plan.StepFailed}})
			v.SetDeadEnd(tc.res, true, "zvi/fix-login-redirect-loop")
			frame := strippedFrame(v, 57, 18)
			if joined := strings.Join(strings.Fields(frame), " "); !strings.Contains(joined, tc.want) {
				t.Errorf("ViewAt(57,18) in the dead end = %q, want it to say %q whole", frame, tc.want)
			}
		})
	}
}

// TestSubmitView_CleanDisabledShowsItsReason pins v2 spec §12's "clean
// stays disabled with its reason shown when the checkout is dirty or
// ahead of base", for both of gitx.Disposable's own refusals: the reason
// is on screen AND the remove button has dropped its key glyph, so
// nothing on the footer advertises a key that does nothing.
func TestSubmitView_CleanDisabledShowsItsReason(t *testing.T) {
	// Both strings are gitx.Disposable's own verbatim refusals (repo.go:
	// "worktree has uncommitted changes" / "worktree has %d commit(s) not
	// on %s"), which is what plan.CleanCheck threads into
	// CleanDecision.Reason -- not invented copy.
	for _, reason := range []string{
		"worktree has uncommitted changes",
		"worktree has 3 commit(s) not on main",
	} {
		v := newSubmitTestView()
		v.SetSteps(sampleStepsFailed())
		v.SetFailure(plan.ExecResult{FailedIndex: 2}, plan.CleanDecision{Allowed: false, Reason: reason})

		frame := strippedFrame(v, 80, 24)
		if !strings.Contains(frame, reason) {
			t.Errorf("ViewAt(80,24) = %q, want the denial reason %q on screen", frame, reason)
		}
		if !strings.Contains(frame, "remove it") {
			t.Errorf("ViewAt(80,24) = %q, want the remove button still drawn (disabled), not removed", frame)
		}
		if strings.Contains(frame, "c remove it") {
			t.Errorf("ViewAt(80,24) = %q, want the disabled remove button to drop its key glyph", frame)
		}
		if !strings.Contains(frame, "k keep it") {
			t.Errorf("ViewAt(80,24) = %q, want keep still live", frame)
		}
	}
}

// TestSubmitView_RemoveLineSaysWhatHappensToTheBranch pins the line above
// the buttons to what plan.Clean will actually do (#173). It used to say
// "remove undoes everything this create made" for every plan, which was
// false twice over for a worktree: herdr's `worktree remove` keeps the
// branch, and the repository workspace herdr opens beside a first worktree
// stays open. A worktree's line therefore names what remove deletes, and
// says so when the branch is kept.
func TestSubmitView_RemoveLineSaysWhatHappensToTheBranch(t *testing.T) {
	for _, tc := range []struct {
		fate plan.BranchFate
		want string
	}{
		{plan.BranchDeleted, "remove deletes the worktree, its branch and its workspace"},
		{plan.BranchKept, "remove deletes the worktree and its workspace, but keeps the branch"},
		{plan.NoBranch, "remove undoes everything this create made"},
	} {
		v := newSubmitTestView()
		v.SetSteps(sampleStepsFailed())
		v.SetFailure(plan.ExecResult{FailedIndex: 2}, plan.CleanDecision{Allowed: true, Branch: tc.fate})

		frame := strippedFrame(v, 80, 24)
		if !strings.Contains(frame, tc.want) {
			t.Errorf("fate %v: frame does not say %q:\n%s", tc.fate, tc.want, frame)
		}
		if tc.fate != plan.NoBranch && strings.Contains(frame, "everything") {
			t.Errorf("fate %v: a worktree's remove line claims everything, and the parent workspace stays:\n%s", tc.fate, frame)
		}
	}
}

// TestSubmitView_CleanedKeepingABranchOffersNothingMore: once a remove has
// run, k and c are spent -- the space is gone -- and the view says what is
// left instead of offering a second remove that would fail against a
// workspace herdr no longer has (#190's review).
func TestSubmitView_CleanedKeepingABranchOffersNothingMore(t *testing.T) {
	v := newSubmitTestView()
	v.SetSteps(sampleStepsFailed())
	v.SetFailure(plan.ExecResult{FailedIndex: 2}, plan.CleanDecision{Allowed: true, Branch: plan.BranchDeleted})
	v.SetCleanedKeepingBranch("zvi/x", "it holds 1 commit(s) nothing else does")

	frame := strippedFrame(v, 80, 24)
	for _, want := range []string{"removed the worktree and its workspace, but not its branch", "zvi/x", "it holds 1 commit(s) nothing else does", "esc close"} {
		if !strings.Contains(frame, want) {
			t.Errorf("frame does not say %q:\n%s", want, frame)
		}
	}
	for _, gone := range []string{"keep it", "remove it", "remove deletes"} {
		if strings.Contains(frame, gone) {
			t.Errorf("frame still offers %q after the remove ran:\n%s", gone, frame)
		}
	}
	for _, k := range []rune{'k', 'c'} {
		if cmd := v.Update(key(k, 0)); cmd != nil {
			t.Errorf("%q after the remove ran produced %#v, want nothing", k, cmd())
		}
	}
}

// TestSubmitView_KeptBranchReasonWrapsWithoutSplittingTheCommand: the
// reason git refused a delete with ends in the command that finishes the
// job. It wraps at 80 cells, and the command has to arrive whole.
func TestSubmitView_KeptBranchReasonWrapsWithoutSplittingTheCommand(t *testing.T) {
	v := newSubmitTestView()
	v.SetSteps(sampleStepsFailed())
	v.SetFailure(plan.ExecResult{FailedIndex: 2}, plan.CleanDecision{Allowed: true, Branch: plan.BranchDeleted})
	// Worded so the 80-cell wrap point falls inside the command: a wrapper
	// that breaks at hyphens, or between the command's words, splits it.
	v.SetCleanedKeepingBranch("zvi/fix-login-redirect-loop",
		"git would not delete it, because another git held its lock; `git branch -D zvi/fix-login-redirect-loop` finishes the job")

	if frame := strippedFrame(v, 80, 24); !strings.Contains(frame, "`git branch -D zvi/fix-login-redirect-loop`") {
		t.Fatalf("the command was split across lines:\n%s", frame)
	}
}

// TestWrapAtSpacesKeepsNamesWhole: a reason ending in a command to copy,
// wrapped at a width that falls inside the command, keeps the command on one
// line -- ansi's own wrappers break it at the hyphen, and a plain space wrap
// between its words.
func TestWrapAtSpacesKeepsNamesWhole(t *testing.T) {
	got := wrapAtSpaces("git would not delete it; `git branch -D zvi/fix-login-redirect-loop` finishes the job", 50)
	want := []string{"git would not delete it;", "`git branch -D zvi/fix-login-redirect-loop`", "finishes the job"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wrapAtSpaces = %q, want %q", got, want)
	}
}

// regionLastLine is the y of the bottom line of the panel region: under
// v3 spec §7's frame that is no longer "two above the end", because rule
// 3 closes the card between the region and the footer and a bottom margin
// may sit under the footer in turn. Derived from the frame, in
// compose()'s own order.
func regionLastLine(f frame, h int) int {
	y := footerRow(f, h) - 1
	if f.Rule3 {
		y--
	}
	return y
}

// TestSubmitView_CleanReasonSitsAboveTheButtons pins the placement v2
// spec §12's mockup shows: the reason is at the BOTTOM of the region,
// next to the button row it explains, not floating a screen away under
// the rule.
//
// "Immediately above" became "immediately above, inside the card" when
// v3 spec §7.4 added rule 3: the card's closing rule now sits between the
// explanation and the buttons. That is one line of chrome, not the screen
// of blank the bottom-anchoring exists to remove, and the anchoring is
// still what is asserted -- the reason is the region's last line, wherever
// the region ends.
func TestSubmitView_CleanReasonSitsAboveTheButtons(t *testing.T) {
	v := newSubmitTestView()
	v.SetSteps(sampleStepsFailed())
	v.SetFailure(plan.ExecResult{FailedIndex: 2}, plan.CleanDecision{Allowed: false, Reason: "uncommitted changes"})

	const w, h = 80, 24
	last := regionLastLine(layoutFrame(h, len(sampleStepsFailed())), h)
	lines := strippedFrameLines(v, w, h)
	if got := lines[last]; !strings.Contains(got, "uncommitted changes") {
		t.Errorf("the region's last line (%d) = %q, want the denial reason", last, got)
	}
}

// TestSubmitView_SecondRuleOnlyWhereThereIsSomethingToRule pins the
// difference between this view's panel region and the form's. The form
// always fills that region with the focused field's chooser, so a rule
// above it always has content under it; the submit view has no chooser
// at all, so a fixed rule under the step rows drew a divider over
// sixteen blank lines on the progress screen and stood sixteen lines
// above the reason it was supposed to introduce on the failure one.
//
// The rule now travels with the explanation, which is bottom-anchored to
// the buttons it explains. Trailing blanks above a bottom-anchored
// footer are not the defect and are not asserted against.
//
// v3 spec §7.4's rule 3 is exempt by construction and not by exception:
// it closes the card at a fixed line directly above the footer, which is
// chrome, where the rule this test is about is a divider that must
// introduce something. So the search runs over the step rows and the
// region only -- everything from line 2 down to the region's last line.
func TestSubmitView_SecondRuleOnlyWhereThereIsSomethingToRule(t *testing.T) {
	const w, h = 80, 24

	running := newSubmitTestView()
	running.SetSteps(sampleStepsRunning())
	runFrame := layoutFrame(h, len(sampleStepsRunning()))
	lines := strippedFrameLines(running, w, h)
	// The first rule is the one under the header; nothing below the step
	// rows may be one while the pipeline is still going. Found rather than
	// assumed to be line 1: a short step list leaves the frame room for a
	// top margin above the header (v3 spec §7.1's last rung).
	rule1 := -1
	for i, line := range lines {
		if strings.Contains(line, "──") {
			rule1 = i
			break
		}
	}
	if rule1 < 0 {
		t.Fatalf("progress screen has no rule under its header:\n%s", strings.Join(lines, "\n"))
	}
	for i, line := range lines[rule1+1 : regionLastLine(runFrame, h)+1] {
		if strings.Contains(line, "──") {
			t.Errorf("progress screen draws a second rule at line %d with nothing under it:\n%s",
				i+rule1+1, strings.Join(lines, "\n"))
			break
		}
	}
	// And rule 3 is where it should be, so the exemption above is not
	// quietly excusing a missing line.
	if r3 := footerRow(runFrame, h) - 1; !runFrame.Rule3 || !strings.Contains(lines[r3], "──") {
		t.Errorf("line %d = %q, want v3 spec §7.4's rule 3 closing the card above the footer", r3, lines[r3])
	}

	failed := newSubmitTestView()
	failed.SetSteps(sampleStepsFailed())
	failed.SetFailure(plan.ExecResult{FailedIndex: 2}, plan.CleanDecision{Allowed: true})
	failFrame := layoutFrame(h, len(sampleStepsFailed()))
	lines = strippedFrameLines(failed, w, h)
	reason := -1
	for i, line := range lines {
		if strings.Contains(line, "remove undoes everything") {
			reason = i
		}
	}
	if reason < 1 {
		t.Fatalf("failure screen never rendered its explanation:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[reason-1], "──") {
		t.Errorf("the line above the explanation = %q, want the second rule introducing it", lines[reason-1])
	}
	if want := regionLastLine(failFrame, h); reason != want {
		t.Errorf("the explanation is at line %d of %d, want it on the region's last line (%d), anchored to the buttons",
			reason, len(lines), want)
	}
}

// --- v2 spec §12: the unsent-prompt row -----------------------------------

// multiParagraphPrompt is the shape spec §10's own default seeding
// template produces -- identifier line, blank, URL, blank, description --
// and the shape the old inline rendering destroyed: fitLine's Inline(true)
// strips every newline and its MaxWidth clips the glued result to the
// popup's width, so what reached the user was one truncated line, and then
// the popup closed and took it with it.
const multiParagraphPrompt = "Work on ENG-1: Fix login redirect loop\n\n" +
	"https://linear.app/acme/issue/ENG-1\n\n" +
	"The redirect loop reproduces whenever the session cookie is refreshed " +
	"mid-request; see the attached HAR for the full sequence."

// TestSubmitView_UnsentPromptKeepsItsOwnRow pins v2 spec §12's "the
// unsent-prompt path keeps its own row" and spec §9 step 3's "prompt text
// surfaced back to the user for manual paste" as something that actually
// survives the popup (finding I6): the failure view names the file the
// prompt was saved to, on a line of its own, and deliberately does NOT
// render the prompt itself.
func TestSubmitView_UnsentPromptKeepsItsOwnRow(t *testing.T) {
	v := newSubmitTestView()
	v.SetSteps(sampleStepsFailed())
	v.SetFailure(plan.ExecResult{FailedIndex: 2, PromptText: multiParagraphPrompt}, plan.CleanDecision{Allowed: true})

	// Before the save lands, the view still says what happened.
	if frame := strippedFrame(v, 80, 24); !strings.Contains(frame, "prompt not sent") {
		t.Errorf("ViewAt(80,24) before the save landed = %q, want it to say the prompt was not sent", frame)
	}

	v.SetUnsentPrompt("/state/unsent-prompt.txt", nil)

	lines := strippedFrameLines(v, 80, 24)
	frame := strings.Join(lines, "\n")
	if !strings.Contains(frame, "prompt not sent") {
		t.Errorf("ViewAt(80,24) = %q, want it to say the prompt was not sent", frame)
	}
	// The path gets a row to itself, so a long state dir is not clipped
	// by an explanation sharing the line.
	var pathRow string
	for _, l := range lines {
		if strings.Contains(l, "/state/unsent-prompt.txt") {
			pathRow = l
			break
		}
	}
	if pathRow == "" {
		t.Fatalf("ViewAt(80,24) = %q, want the path the unsent prompt was saved to", frame)
	}
	if strings.TrimSpace(pathRow) != "/state/unsent-prompt.txt" {
		t.Errorf("the unsent-prompt path shares its row with %q, want a row of its own", strings.TrimSpace(pathRow))
	}
	// The prompt's own body must stay out of the frame: it cannot survive
	// fitLine, and a truncated copy is worse than a path.
	if strings.Contains(frame, "reproduces whenever") {
		t.Errorf("ViewAt(80,24) = %q, want the prompt BODY left out of the frame entirely", frame)
	}
}

// TestSubmitView_UnsentPromptFallsBackToInlineTextWhenTheSaveFailed pins
// the other half: when the prompt could not be written anywhere, a clipped
// inline copy is all that is left, and it is still better than silence.
func TestSubmitView_UnsentPromptFallsBackToInlineTextWhenTheSaveFailed(t *testing.T) {
	v := newSubmitTestView()
	v.SetSteps(sampleStepsFailed())
	v.SetFailure(plan.ExecResult{FailedIndex: 2, PromptText: "Work on ENG-1"}, plan.CleanDecision{Allowed: true})
	v.SetUnsentPrompt("", errors.New("permission denied"))

	frame := strippedFrame(v, 80, 24)
	if !strings.Contains(frame, "permission denied") {
		t.Errorf("ViewAt(80,24) = %q, want the save error surfaced rather than swallowed", frame)
	}
	if !strings.Contains(frame, "Work on ENG-1") {
		t.Errorf("ViewAt(80,24) = %q, want the prompt text inline as the last-resort copy", frame)
	}
}

// TestSubmitView_CleanFailedSurfacesError pins SetCleanFailed (fix round
// 1: a failed "c" attempt was previously indistinguishable from a
// successful one -- the app layer captured plan.Clean's own error but
// had nowhere to put it). Nothing renders before SetCleanFailed is
// called (zero-value safety); afterward, a short error line appears
// alongside the still-available k/c choice.
func TestSubmitView_CleanFailedSurfacesError(t *testing.T) {
	v := newSubmitTestView()
	v.SetSteps(sampleStepsFailed())
	v.SetFailure(plan.ExecResult{FailedIndex: 2}, plan.CleanDecision{Allowed: true})

	before := strippedFrame(v, 80, 24)
	if strings.Contains(before, "remove failed") {
		t.Errorf("ViewAt(80,24) before SetCleanFailed already mentions a failed remove: %q", before)
	}

	v.SetCleanFailed(errors.New("herdr: workspace not found"))
	after := strippedFrame(v, 80, 24)
	if !strings.Contains(after, "remove failed") || !strings.Contains(after, "herdr: workspace not found") {
		t.Errorf("ViewAt(80,24) after SetCleanFailed = %q, want it to surface the remove error", after)
	}
	if !strings.Contains(after, "k keep it") || !strings.Contains(after, "c remove it") {
		t.Errorf("ViewAt(80,24) after SetCleanFailed = %q, want the k/c choice to stay available (retry)", after)
	}
}

// --- degradation and zero-value safety ------------------------------------

func TestSubmitView_NoPanicBeforeSetCleanFailed(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("SubmitView panicked: %v", r)
		}
	}()
	v := NewSubmitView(theme.Default())
	_ = v.ViewAt(80, 24)
	v.SetCleanFailed(errors.New("boom"))
	_ = v.ViewAt(80, 24)
}

func TestSubmitView_NoPanicOnDegenerateSize(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("SubmitView panicked: %v", r)
		}
	}()
	v := newSubmitTestView()
	v.SetSteps(sampleStepsFailed())
	v.SetFailure(plan.ExecResult{FailedIndex: 2}, plan.CleanDecision{Allowed: false, Reason: "dirty"})
	for _, size := range [][2]int{{0, 0}, {-3, -3}, {80, 0}, {1, 1}, {3, 24}, {80, 2}} {
		_ = v.ViewAt(size[0], size[1])
	}
}

func TestSubmitView_NoPanicBeforeSetSteps(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("SubmitView panicked before SetSteps: %v", r)
		}
	}()
	v := NewSubmitView(theme.Default())
	_ = v.ViewAt(80, 24)
	_ = v.Update(key('k', 0))
}

// TestSubmitView_EveryLineIsExactlyOneRow pins the Inline(true)+Width+
// MaxWidth discipline (layout.go's fitLine) across every state: lipgloss
// v2 word-wraps before applying MaxWidth, so a single long value silently
// becoming two physical lines would push the footer off the bottom.
func TestSubmitView_EveryLineIsExactlyOneRow(t *testing.T) {
	long := strings.Repeat("very-long-branch-name/", 12)
	v := newSubmitTestView()
	v.SetSteps([]Step{
		{Label: "worktree", Detail: long, State: plan.StepDone},
		{Label: "claude", Detail: long, State: plan.StepFailed},
	})
	v.SetFailure(plan.ExecResult{FailedIndex: 1, PromptText: multiParagraphPrompt},
		plan.CleanDecision{Allowed: false, Reason: long})
	v.SetCleanFailed(errors.New(long))
	v.SetUnsentPrompt("/"+long+"/unsent-prompt.txt", nil)

	for _, size := range [][2]int{{101, 30}, {77, 22}, {57, 18}, {150, 44}, {40, 10}} {
		w, h := size[0], size[1]
		lines := strippedFrameLines(v, w, h)
		if len(lines) != h {
			t.Fatalf("ViewAt(%d,%d) rendered %d lines, want exactly %d", w, h, len(lines), h)
		}
		for i, l := range lines {
			if got := ansi.StringWidth(l); got != w {
				t.Errorf("ViewAt(%d,%d) line %d is %d cells wide, want %d: %q", w, h, i, got, w, l)
			}
		}
	}
}

// TestSubmitView_UnconfirmedPromptNeverInvitesABlindPaste is the gap #108's
// own mutation sweep turned up, and it is the whole defect in miniature.
//
// unsentPromptLines has three branches -- the save still in flight, the
// save landed, the save failed -- and only the middle one was frame-pinned.
// The third renders the prompt INLINE with "copy manually:", as a
// last-resort better-than-nothing when there is no file to point at. On an
// unconfirmed delivery that line is exactly the double-paste invitation the
// issue is about, printed directly under a lead that had been corrected.
//
// So the property is asserted across all three rather than the one that
// happened to have a fixture: no branch may claim the prompt was not sent,
// and every branch must carry the instruction that replaces "paste this".
func TestSubmitView_UnconfirmedPromptNeverInvitesABlindPaste(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*SubmitView)
	}{
		{"save still in flight", func(*SubmitView) {}},
		{"save landed", func(v *SubmitView) { v.SetUnsentPrompt("/state/unsent-prompt.txt", nil) }},
		{"save failed", func(v *SubmitView) { v.SetUnsentPrompt("", errors.New("permission denied")) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := newSubmitTestView()
			v.SetSteps(sampleStepsFailed())
			v.SetFailure(
				plan.ExecResult{
					FailedIndex:       2,
					PromptText:        multiParagraphPrompt,
					PromptUnconfirmed: true,
				},
				plan.CleanDecision{Allowed: false, Reason: "the prompt may already have been delivered"},
			)
			tc.setup(v)

			frame := strippedFrame(v, 80, 24)
			if strings.Contains(frame, "prompt not sent") {
				t.Errorf("frame claims the prompt was not sent:\n%s", frame)
			}
			if !strings.Contains(frame, "read the pane") {
				t.Errorf("frame does not tell the user to read the pane before resending:\n%s", frame)
			}
		})
	}
}

// --- #115: the step that is waiting on the person at the keyboard --------

// TestSubmitView_WaitingStepHasItsOwnGlyph: a wait is neither running nor
// failed, and the gutter is the first thing anyone reads. Sharing "›" with
// a running step would say the machine is still working, which is the one
// thing that is not true.
func TestSubmitView_WaitingStepHasItsOwnGlyph(t *testing.T) {
	v := newSubmitTestView()
	v.SetSteps(sampleStepsWaiting())

	lines := strippedFrameLines(v, 80, 24)
	var row string
	for _, l := range lines {
		if strings.Contains(l, "claude") {
			row = l
			break
		}
	}
	if row == "" {
		t.Fatalf("no claude row in\n%s", strings.Join(lines, "\n"))
	}
	glyph := strings.TrimSpace(row[:strings.Index(row, "claude")])
	if glyph == "" {
		t.Fatal("waiting row has no glyph at all -- a pending row already looks like that")
	}
	for _, taken := range []string{"✓", "›", "✗"} {
		if glyph == taken {
			t.Errorf("waiting row carries %q, the glyph for another state", glyph)
		}
	}
}

// sampleStepsNonFatal is a worktree session whose tab kept herdr's name:
// the rename step failed, and the launch below it went on regardless.
func sampleStepsNonFatal() []Step {
	return []Step{
		{Label: "worktree", Detail: "zvi/fix-login-redirect-loop from main", State: plan.StepDone},
		{Label: "tab", Detail: "not named: herdr tab rename w3:t1 Fix login redirect loop: exit status 1", State: plan.StepFailedNonFatal},
		{Label: "claude", Detail: "starting under clauth alpha-2", State: plan.StepRunning},
		{Label: "prompt", State: plan.StepPending},
	}
}

// TestFrames_ProgressPastANonFatalStep pins the sixth state as drawn: its
// own glyph and colour, the reason in the value column, and the rows below
// it still running -- which is the whole difference from a failed step.
func TestFrames_ProgressPastANonFatalStep(t *testing.T) {
	v := newSubmitTestView()
	v.SetSteps(sampleStepsNonFatal())
	assertSubmitFrame(t, "progress-nonfatal-80x24", v, 80, 24)
}

// TestSubmitView_NonFatalStepHasItsOwnGlyph: a step that failed without
// stopping the plan must not look like any of the other four. Blank is a
// pending row; ✓ would claim the tab was named; ✗ says the plan stopped,
// which it did not; and … says it is waiting on the user, who has nothing
// to do.
func TestSubmitView_NonFatalStepHasItsOwnGlyph(t *testing.T) {
	v := newSubmitTestView()
	v.SetSteps(sampleStepsNonFatal())

	var row string
	for _, l := range strippedFrameLines(v, 80, 24) {
		if strings.Contains(l, "tab") && strings.Contains(l, "not named") {
			row = l
			break
		}
	}
	if row == "" {
		t.Fatalf("no tab row in\n%s", strippedFrame(v, 80, 24))
	}
	glyph := strings.TrimSpace(row[:strings.Index(row, "tab")])
	if glyph == "" {
		t.Fatal("non-fatal row has no glyph at all -- a pending row already looks like that")
	}
	for _, taken := range []string{"✓", "›", "✗", "…"} {
		if glyph == taken {
			t.Errorf("non-fatal row carries %q, the glyph for another state", glyph)
		}
	}
}

// TestSubmitView_NonFatalStepWithNoDetailSaysSo: the state's own word, for
// a row with nothing of its own to say -- the same fallback every other
// state has.
func TestSubmitView_NonFatalStepWithNoDetailSaysSo(t *testing.T) {
	v := newSubmitTestView()
	v.SetSteps([]Step{{Label: "tab", State: plan.StepFailedNonFatal}})
	if frame := strippedFrame(v, 80, 24); !strings.Contains(frame, "failed, continuing") {
		t.Errorf("ViewAt(80,24) = %q, want the row to say it failed and the run went on", frame)
	}
}

// TestSubmitView_WaitingStepWithNoDetailSaysSo mirrors the "queued" rule
// for the fifth state: a row with nothing of its own to say prints its
// state's word.
func TestSubmitView_WaitingStepWithNoDetailSaysSo(t *testing.T) {
	v := newSubmitTestView()
	v.SetSteps([]Step{{Label: "claude", State: plan.StepWaiting}})
	if frame := strippedFrame(v, 80, 24); !strings.Contains(frame, "waiting on you") {
		t.Errorf("ViewAt(80,24) = %q, want the waiting row to say whose turn it is", frame)
	}
}

// TestSubmitView_WaitingReplacesTheStepCounterWithTheInstruction: the
// footer is the widest line on the screen and the only one designed to
// carry a hint, so it is where the thing the user has to DO belongs. A
// step counter is the right footer for a pipeline that is progressing on
// its own and the wrong one for a pipeline that has stopped for them.
func TestSubmitView_WaitingReplacesTheStepCounterWithTheInstruction(t *testing.T) {
	v := newSubmitTestView()
	v.SetSteps(sampleStepsWaiting())

	frame := strippedFrame(v, 80, 24)
	if !strings.Contains(frame, "answer the dialog in the pane") {
		t.Errorf("ViewAt(80,24) = %q, want the footer to say what to do", frame)
	}
	if strings.Contains(frame, "step 3 of 4") {
		t.Errorf("ViewAt(80,24) = %q, want the instruction INSTEAD of the step counter", frame)
	}
}

// TestSubmitView_WaitingHintSurvivesTheNarrowestPopup: footerLine
// truncates a hint with no visible marker when there are no buttons beside
// it, so an instruction that overflows does not look cut off -- it looks
// like a different, shorter sentence. Both the default and anything pushed
// in have to fit the pessimistic width whole.
func TestSubmitView_WaitingHintSurvivesTheNarrowestPopup(t *testing.T) {
	for _, hint := range []string{"", appPushedWaitingHint} {
		v := newSubmitTestView()
		v.SetWaitingHint(hint)
		v.SetSteps(sampleStepsWaiting())

		want := hint
		if want == "" {
			want = defaultWaitingHint
		}
		if frame := strippedFrame(v, 80, 24); !strings.Contains(frame, want) {
			t.Errorf("ViewAt(80,24) does not carry %q whole:\n%s", want, frame)
		}
	}
}

// appPushedWaitingHint is the longest thing internal/app actually pushes
// in (async.go's submitWaitingHint). Copied rather than imported:
// internal/form cannot import internal/app, and the point of the test is
// that THIS package's footer can hold it.
const appPushedWaitingHint = "answer the dialog in the pane — your prompt goes out as soon as you do"

// TestSubmitView_PushedWaitingHintReplacesTheDefault: the setter is what
// lets the app say the true thing (there IS a prompt waiting) without this
// package knowing what a prompt op is.
func TestSubmitView_PushedWaitingHintReplacesTheDefault(t *testing.T) {
	v := newSubmitTestView()
	v.SetWaitingHint(appPushedWaitingHint)
	v.SetSteps(sampleStepsWaiting())

	frame := strippedFrame(v, 80, 24)
	if !strings.Contains(frame, appPushedWaitingHint) {
		t.Errorf("ViewAt(80,24) = %q, want the pushed-in hint", frame)
	}
	if strings.Contains(frame, defaultWaitingHint) {
		t.Errorf("ViewAt(80,24) = %q, want the default REPLACED, not shown beside it", frame)
	}
}

// TestSubmitView_StepCounterReturnsOnceTheDialogIsAnswered: the
// instruction is a property of the waiting STATE, not a latch. Once the
// step moves on, the footer goes back to the counter -- otherwise the
// screen would keep telling the user to answer a dialog they have already
// answered.
func TestSubmitView_StepCounterReturnsOnceTheDialogIsAnswered(t *testing.T) {
	v := newSubmitTestView()
	v.SetSteps(sampleStepsWaiting())
	_ = strippedFrame(v, 80, 24)
	v.SetSteps(sampleStepsRunning())

	frame := strippedFrame(v, 80, 24)
	if strings.Contains(frame, "answer the dialog in the pane") {
		t.Errorf("ViewAt(80,24) = %q, want the instruction gone once no step is waiting", frame)
	}
	if !strings.Contains(frame, "step 3 of 4") {
		t.Errorf("ViewAt(80,24) = %q, want the step counter back", frame)
	}
}

// sampleStepsDoneWithWarning is a session that was created, whose worktree
// step could not stop its branch tracking the base it was cut from (#221):
// the row's value column truncates the reason, and the reason ends in the
// command that fixes it.
func sampleStepsDoneWithWarning() []Step {
	return []Step{
		{Label: "worktree", State: plan.StepFailedNonFatal, Detail: "branch zvi/fix-login-redirect-loop still tracks origin/develop, the branch it was cut from (could not lock config file .git/config: File exists); `git branch --unset-upstream zvi/fix-login-redirect-loop` finishes the job"},
		{Label: "tab", Detail: "Fix login redirect loop", State: plan.StepDone},
		{Label: "claude", Detail: "starting under clauth alpha-2", State: plan.StepDone},
		{Label: "prompt", State: plan.StepDone},
	}
}

// TestFrames_DoneWithWarnings is a create that succeeded with a step's
// warning to read (#230): the popup stays up, says so, and offers only
// `esc close`.
func TestFrames_DoneWithWarnings(t *testing.T) {
	v := newSubmitTestView()
	v.SetSteps(sampleStepsDoneWithWarning())
	v.SetDoneWithWarnings()
	assertSubmitFrame(t, "done-with-warnings-80x24", v, 80, 24)
}

// TestSubmitView_DoneWithWarningsSaysTheWholeReason: the row truncates the
// reason, so the held screen repeats it in full below the rows, wrapped,
// with the command to copy on one line (#230).
func TestSubmitView_DoneWithWarningsSaysTheWholeReason(t *testing.T) {
	v := newSubmitTestView()
	v.SetSteps(sampleStepsDoneWithWarning())
	v.SetDoneWithWarnings()

	frame := strippedFrame(v, 80, 24)
	for _, want := range []string{"created", "`git branch --unset-upstream zvi/fix-login-redirect-loop`", "esc close"} {
		if !strings.Contains(frame, want) {
			t.Errorf("frame does not say %q:\n%s", want, frame)
		}
	}
	for _, gone := range []string{"step 4 of 4", "keep it", "remove it"} {
		if strings.Contains(frame, gone) {
			t.Errorf("frame still says %q once the create is done:\n%s", gone, frame)
		}
	}
}
