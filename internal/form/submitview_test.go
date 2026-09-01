package form

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

func sampleProgressDone() []plan.Progress {
	return []plan.Progress{
		{Index: 0, Total: 2, Label: "creating worktree", State: plan.StepDone},
		{Index: 1, Total: 2, Label: "starting agent", State: plan.StepRunning},
	}
}

func sampleProgressFailed() []plan.Progress {
	return []plan.Progress{
		{Index: 0, Total: 2, Label: "creating worktree", State: plan.StepDone},
		{Index: 1, Total: 2, Label: "starting claude", State: plan.StepFailed, Err: errors.New("agent_pane_busy")},
	}
}

func TestFrames_Progress(t *testing.T) {
	v := NewSubmitView(theme.Default())
	v.SetProgress(sampleProgressDone())
	assertSubmitFrame(t, "progress-80x24", v, 80, 24)
}

func TestFrames_FailureCleanDenied(t *testing.T) {
	v := NewSubmitView(theme.Default())
	v.SetProgress(sampleProgressFailed())
	v.SetFailure(plan.ExecResult{FailedIndex: 1}, plan.CleanDecision{Allowed: false, Reason: "uncommitted changes"})
	assertSubmitFrame(t, "failure-clean-denied-80x24", v, 80, 24)
}

func TestSubmitView_NoFailureBeforeSetFailure(t *testing.T) {
	v := NewSubmitView(theme.Default())
	v.SetProgress(sampleProgressDone())
	frame := ansi.Strip(v.ViewAt(80, 24))
	if strings.Contains(frame, "keep") || strings.Contains(frame, "clean") {
		t.Errorf("ViewAt(80,24) before SetFailure = %q, want no keep/clean prompt", frame)
	}
}

func TestSubmitView_KPressEmitsKeepMsg(t *testing.T) {
	v := NewSubmitView(theme.Default())
	v.SetProgress(sampleProgressFailed())
	v.SetFailure(plan.ExecResult{FailedIndex: 1}, plan.CleanDecision{Allowed: true})

	cmd := v.Update(key('k', 0))
	if cmd == nil {
		t.Fatalf("Update('k') returned nil, want a Cmd emitting KeepMsg")
	}
	if _, ok := cmd().(KeepMsg); !ok {
		t.Fatalf("Update('k')() = %T, want KeepMsg", cmd())
	}
}

func TestSubmitView_CPressEmitsCleanMsgWhenAllowed(t *testing.T) {
	v := NewSubmitView(theme.Default())
	v.SetProgress(sampleProgressFailed())
	v.SetFailure(plan.ExecResult{FailedIndex: 1}, plan.CleanDecision{Allowed: true})

	cmd := v.Update(key('c', 0))
	if cmd == nil {
		t.Fatalf("Update('c') returned nil, want a Cmd emitting CleanMsg")
	}
	if _, ok := cmd().(CleanMsg); !ok {
		t.Fatalf("Update('c')() = %T, want CleanMsg", cmd())
	}
}

// TestSubmitView_CPressNoOpWhenCleanDenied pins spec §9's "the clean
// option is disabled with the reason shown" -- "c" must not emit CleanMsg
// when CleanDecision.Allowed is false.
func TestSubmitView_CPressNoOpWhenCleanDenied(t *testing.T) {
	v := NewSubmitView(theme.Default())
	v.SetProgress(sampleProgressFailed())
	v.SetFailure(plan.ExecResult{FailedIndex: 1}, plan.CleanDecision{Allowed: false, Reason: "uncommitted changes"})

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
	v := NewSubmitView(theme.Default())
	v.SetProgress(sampleProgressDone())
	if cmd := v.Update(key('k', 0)); cmd != nil {
		t.Errorf("Update('k') before SetFailure returned a non-nil Cmd, want nil")
	}
	if cmd := v.Update(key('c', 0)); cmd != nil {
		t.Errorf("Update('c') before SetFailure returned a non-nil Cmd, want nil")
	}
}

func TestSubmitView_FailureReasonShownWhenCleanDenied(t *testing.T) {
	v := NewSubmitView(theme.Default())
	v.SetProgress(sampleProgressFailed())
	v.SetFailure(plan.ExecResult{FailedIndex: 1}, plan.CleanDecision{Allowed: false, Reason: "uncommitted changes"})

	frame := ansi.Strip(v.ViewAt(80, 24))
	if !strings.Contains(frame, "uncommitted changes") {
		t.Errorf("ViewAt(80,24) = %q, want it to contain the denial reason", frame)
	}
	if strings.Contains(frame, "c clean") {
		t.Errorf("ViewAt(80,24) = %q, want no active clean choice when denied", frame)
	}
}

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

// TestSubmitView_UnsentPromptSurfacedAsARecoverablePath pins spec §9 step
// 3's "prompt text surfaced back to the user for manual paste" as
// something that actually survives the popup (finding I6): the failure
// view names the file the prompt was saved to, and deliberately does NOT
// render the prompt itself.
func TestSubmitView_UnsentPromptSurfacedAsARecoverablePath(t *testing.T) {
	v := NewSubmitView(theme.Default())
	v.SetProgress(sampleProgressFailed())
	v.SetFailure(plan.ExecResult{FailedIndex: 1, PromptText: multiParagraphPrompt}, plan.CleanDecision{Allowed: true})

	// Before the save lands, the view still says what happened.
	if frame := ansi.Strip(v.ViewAt(80, 24)); !strings.Contains(frame, "prompt not sent") {
		t.Errorf("ViewAt(80,24) before the save landed = %q, want it to say the prompt was not sent", frame)
	}

	v.SetUnsentPrompt("/state/unsent-prompt.txt", nil)

	frame := ansi.Strip(v.ViewAt(80, 24))
	if !strings.Contains(frame, "/state/unsent-prompt.txt") {
		t.Errorf("ViewAt(80,24) = %q, want the path the unsent prompt was saved to", frame)
	}
	if !strings.Contains(frame, "prompt not sent") {
		t.Errorf("ViewAt(80,24) = %q, want it to say the prompt was not sent", frame)
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
	v := NewSubmitView(theme.Default())
	v.SetProgress(sampleProgressFailed())
	v.SetFailure(plan.ExecResult{FailedIndex: 1, PromptText: "Work on ENG-1"}, plan.CleanDecision{Allowed: true})
	v.SetUnsentPrompt("", errors.New("permission denied"))

	frame := ansi.Strip(v.ViewAt(80, 24))
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
	v := NewSubmitView(theme.Default())
	v.SetProgress(sampleProgressFailed())
	v.SetFailure(plan.ExecResult{FailedIndex: 1}, plan.CleanDecision{Allowed: true})

	before := ansi.Strip(v.ViewAt(80, 24))
	if strings.Contains(before, "clean failed") {
		t.Errorf("ViewAt(80,24) before SetCleanFailed already mentions a clean failure: %q", before)
	}

	v.SetCleanFailed(errors.New("herdr: workspace not found"))
	after := ansi.Strip(v.ViewAt(80, 24))
	if !strings.Contains(after, "clean failed") || !strings.Contains(after, "herdr: workspace not found") {
		t.Errorf("ViewAt(80,24) after SetCleanFailed = %q, want it to surface the clean error", after)
	}
	if !strings.Contains(after, "k keep") || !strings.Contains(after, "c clean") {
		t.Errorf("ViewAt(80,24) after SetCleanFailed = %q, want the k/c choice to stay available (retry)", after)
	}
}

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
	v := NewSubmitView(theme.Default())
	_ = v.ViewAt(0, 0)
	_ = v.ViewAt(-3, -3)
	_ = v.ViewAt(80, 0)
}

func TestSubmitView_NoPanicBeforeSetProgress(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("SubmitView panicked before SetProgress: %v", r)
		}
	}()
	v := NewSubmitView(theme.Default())
	_ = v.ViewAt(80, 24)
	_ = v.Update(key('k', 0))
}
