package form

import (
	"errors"
	"os"
	"path/filepath"
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
		{Label: "claude", Detail: "starting under clauth quantivly-2", State: plan.StepRunning},
		{Label: "prompt", State: plan.StepPending},
	}
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
func TestFrames_FailureCleanOffered(t *testing.T) {
	v := newSubmitTestView()
	v.SetSteps(sampleStepsFailed())
	v.SetFailure(plan.ExecResult{FailedIndex: 2}, plan.CleanDecision{Allowed: true})
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
		"running":  func(v *SubmitView) { v.SetSteps(sampleStepsRunning()) },
		"dead end": func(v *SubmitView) { v.SetSteps(sampleStepsFailed()); v.SetDeadEnd() },
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
// from (the app layer's own scoping): nothing was created, so there is
// nothing to keep or remove and the footer says so.
func TestSubmitView_DeadEndOffersOnlyClose(t *testing.T) {
	v := newSubmitTestView()
	v.SetSteps([]Step{{Label: "workspace", Detail: "herdr: boom", State: plan.StepFailed}})
	v.SetDeadEnd()

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

// TestSubmitView_CleanReasonSitsAboveTheButtons pins the placement v2
// spec §12's mockup shows: the reason is on the line immediately above
// the button row, not floating a screen away under the rule.
func TestSubmitView_CleanReasonSitsAboveTheButtons(t *testing.T) {
	v := newSubmitTestView()
	v.SetSteps(sampleStepsFailed())
	v.SetFailure(plan.ExecResult{FailedIndex: 2}, plan.CleanDecision{Allowed: false, Reason: "uncommitted changes"})

	lines := strippedFrameLines(v, 80, 24)
	if got := lines[len(lines)-2]; !strings.Contains(got, "uncommitted changes") {
		t.Errorf("line above the footer = %q, want the denial reason", got)
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
func TestSubmitView_SecondRuleOnlyWhereThereIsSomethingToRule(t *testing.T) {
	const w, h = 80, 24

	running := newSubmitTestView()
	running.SetSteps(sampleStepsRunning())
	lines := strippedFrameLines(running, w, h)
	// Line 1 is the rule under the header; nothing below the step rows
	// may be one while the pipeline is still going.
	for i, line := range lines[2:] {
		if strings.Contains(line, "──") {
			t.Errorf("progress screen draws a second rule at line %d with nothing under it:\n%s",
				i+2, strings.Join(lines, "\n"))
			break
		}
	}

	failed := newSubmitTestView()
	failed.SetSteps(sampleStepsFailed())
	failed.SetFailure(plan.ExecResult{FailedIndex: 2}, plan.CleanDecision{Allowed: true})
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
	if reason != len(lines)-2 {
		t.Errorf("the explanation is at line %d of %d, want it anchored to the buttons", reason, len(lines))
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
