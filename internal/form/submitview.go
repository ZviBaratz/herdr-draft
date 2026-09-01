// submitview.go is written fresh for this task -- Atrium has no
// equivalent staged-progress/keep-or-clean view at all (its own creation
// flow is fire-and-forget, no failure gate); this is new UI for
// herdr-draft's own spec §9 submit pipeline. It reuses sizes.go's
// paintLine/fitToHeight/dividerLine/decorateFocus and layout.go's
// fitLine/dimHint rather than re-deriving chrome rendering a second time,
// the same package-private helpers form.go's own compose uses.
//
// SubmitView is deliberately NOT a form.Section: it has no ID/Enabled and
// takes no part in the focus ring (there is nothing to Tab between --
// spec §9's staged-progress and keep-or-clean views are read-only except
// for the single k/c choice, which this file's own Update handles
// directly rather than through keys.go's MapKey grammar, since neither
// "k" nor "c" appears anywhere in that grammar). Per the brief, it is "a
// PURE VIEW over SetProgress/SetFailure": no herdr CLI call, no
// filesystem/network access, and no side effect beyond returning a
// tea.Cmd that emits KeepMsg/CleanMsg for the app layer's own Update to
// act on -- matching form.go's own "the form is a dumb view" posture
// (spec §4), extended here to the view that follows it.
//
// A note on the brief's own "emits SubmitMsg/KeepMsg/CleanMsg" wording,
// flagged for controller review: form.go already declares `type SubmitMsg
// struct{}` in this same package, so a second declaration of that name
// here would not even compile. Read literally, "emits SubmitMsg" can only
// mean form.go's EXISTING SubmitMsg -- the message that is what routes
// control INTO this view in the first place (the app layer's own Update
// sees form.SubmitMsg, runs spec §9's validation, and only then starts
// driving a SubmitView via SetProgress/SetFailure); SubmitView's own
// Update never re-emits it, since spec §9 describes no "resubmit" gesture
// once staged creation is under way. What this file actually defines and
// emits is exactly the other two names the brief lists: KeepMsg and
// CleanMsg, from the k/c keys spec §9's own "keep-or-clean choice in the
// popup" describes.
package form

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/ZviBaratz/herdr-draft/internal/plan"
	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// KeepMsg is emitted (as a tea.Cmd's result) when the user presses "k"
// while a failure is showing -- spec §9: "Keep leaves the created space
// as a plain shell." The app layer is expected to leave the created
// topology as-is and close the popup.
type KeepMsg struct{}

// CleanMsg is emitted when the user presses "c" while a failure is
// showing AND the CleanDecision passed to SetFailure has Allowed == true
// -- spec §9: "Clean runs `herdr worktree remove` / `herdr workspace
// close`." A denied clean decision makes "c" a no-op (see Update): the
// reason line SetFailure's caller supplied is the only feedback, matching
// spec §9's "the clean option is disabled with the reason shown" (never a
// blocking key that silently does nothing with no explanation on screen).
type CleanMsg struct{}

// SubmitView renders spec §9's staged-progress display and, once a step
// fails, its keep-or-clean failure prompt -- a pure view over
// SetProgress/SetFailure (see the file doc comment for its "form is a
// dumb view" posture and the deliberate SubmitMsg wording note).
type SubmitView struct {
	palette theme.Palette

	progress []plan.Progress

	haveFailure bool
	result      plan.ExecResult
	clean       plan.CleanDecision

	// cleanErr is SetCleanFailed's own recorded error, or nil before that
	// setter is ever called (the common case: keep succeeds silently, or
	// no clean was ever attempted). Rendered as a short, always-visible
	// line in the failure prompt -- see failureLines.
	cleanErr error
}

// NewSubmitView returns an empty SubmitView (no progress, no failure)
// styled from palette.
func NewSubmitView(palette theme.Palette) *SubmitView {
	return &SubmitView{palette: palette}
}

// SetProgress replaces the staged-progress list rendered above the
// (optional) failure prompt -- the app layer is expected to call this on
// every plan.Progress callback plan.Execute reports (spec §9: "per-step
// progress lines rendered in the popup").
func (v *SubmitView) SetProgress(p []plan.Progress) {
	v.progress = append([]plan.Progress(nil), p...)
}

// SetFailure records the keep-or-clean gate's own state once a step has
// failed after step 1 (topology creation) succeeded -- spec §9: "Failure
// handling after step 1 succeeded: keep-or-clean choice in the popup."
// Calling this a second time (e.g. a later, more informed CleanCheck
// result) simply overwrites the previous one; there is no way to clear it
// back to "no failure" short of constructing a fresh SubmitView, matching
// this package's other fields' own "no separate Clear" posture where a
// setter's own new value is authoritative (field_title.go's SetVerdict,
// field_dir.go's SetValidity).
func (v *SubmitView) SetFailure(res plan.ExecResult, clean plan.CleanDecision) {
	v.haveFailure = true
	v.result = res
	v.clean = clean
}

// SetCleanFailed records that a "c" (clean) attempt itself failed -- the
// herdr CLI call plan.Clean issued (spec §9: `herdr worktree remove` /
// `herdr workspace close`) returned a non-nil error, even though
// CleanCheck had already said it was safe to try. Rendered as a short,
// always-visible error line in the failure prompt once haveFailure is
// true (a no-op before SetFailure, same zero-value safety as SetFailure
// itself; see Update's identical guard).
//
// Added in fix round 1 (Task 20b review -- silent failure): before this,
// a failed Clean was indistinguishable from a successful one -- the app
// layer captured the error but had nowhere to put it, so it quit either
// way, exactly the same "never silent" guarantee spec §9 exists to
// protect for the ORIGINAL step failure. The app layer's own
// handleCleanDone (async.go) now calls this and stays on the failure
// prompt (instead of quitting) when Clean itself fails, so the k/c
// choice stays available too -- "k" to give up and keep, or "c" to retry.
func (v *SubmitView) SetCleanFailed(err error) {
	v.cleanErr = err
}

// Update handles the keep-or-clean gate's own k/c keys -- a no-op ("no
// panic on an unconstructed value or before a failure" -- this task's own
// carried "zero-value safety" fact) until SetFailure has been called, and
// a no-op for "c" specifically when the CleanDecision denies it (see
// CleanMsg's own doc comment).
func (v *SubmitView) Update(msg tea.KeyPressMsg) tea.Cmd {
	if !v.haveFailure {
		return nil
	}
	switch msg.String() {
	case "k":
		return func() tea.Msg { return KeepMsg{} }
	case "c":
		if v.clean.Allowed {
			return func() tea.Msg { return CleanMsg{} }
		}
	}
	return nil
}

// ViewAt is SubmitView's deterministic render entry point (golden-frame
// tests call it directly), mirroring form.Model.ViewAt's own contract and
// rationale verbatim: it renders at an explicit w x h and pins lipgloss's
// output to TrueColor via colorprofile.Writer's documented pass-through
// mode, so frames are byte-identical across machines -- see
// form.Model.ViewAt's own doc comment for the full "why pin at all" case,
// which applies unchanged here (same lipgloss v2 package, same styling
// approach, no environment-sensitive call anywhere in this file either).
func (v *SubmitView) ViewAt(w, h int) string {
	content := v.compose(w, h)
	var buf strings.Builder
	cw := &colorprofile.Writer{Forward: &buf, Profile: colorprofile.TrueColor}
	_, _ = cw.WriteString(content)
	return buf.String()
}

// compose assembles the view's content at exactly w x h: one blank
// padding row top and bottom (matching form.Model.compose's own
// verticalPadding convention), each progress line (progressLine), then --
// only once SetFailure has been called -- a divider and the failure
// prompt's own lines (failureLines). sizes.go's fitToHeight then applies
// the same drop-blanks/drop-dividers/clip-tail degradation cascade
// form.Model.compose uses for a short window, and every line is finally
// painted with palette.PanelBG across the full w columns (spec §7).
func (v *SubmitView) compose(w, h int) string {
	if h <= 0 {
		return ""
	}

	inner := innerWidth(w)
	dividerDecorated := decorateFocus(dividerLine(inner, v.palette), false, v.palette)
	blank := decorateFocus("", false, v.palette)

	lines := make([]string, 0, h)
	for i := 0; i < verticalPadding; i++ {
		lines = append(lines, blank)
	}

	for _, p := range v.progress {
		lines = append(lines, decorateFocus(fitLine(progressLine(p, v.palette), inner), false, v.palette))
	}

	if v.haveFailure {
		lines = append(lines, dividerDecorated)
		for _, l := range v.failureLines(inner) {
			lines = append(lines, decorateFocus(l, false, v.palette))
		}
	}

	for i := 0; i < verticalPadding; i++ {
		lines = append(lines, blank)
	}

	lines = fitToHeight(lines, h, dividerDecorated, -1)

	painted := make([]string, 0, h)
	for _, l := range lines {
		if len(painted) == h {
			break
		}
		painted = append(painted, paintLine(l, w, v.palette.PanelBG))
	}
	for len(painted) < h {
		painted = append(painted, paintLine("", w, v.palette.PanelBG))
	}
	return strings.Join(painted, "\n")
}

// progressLine renders one plan.Progress row, matching spec §9's own
// literal example format ("creating worktree… ✓" / "starting claude… ✗
// <error>"): plan.Op.Label is a bare verb phrase with no trailing
// ellipsis or marker of its own (internal/plan/build.go: `Label:
// "creating worktree"`) -- this function is what supplies both, styled by
// State: dim, unadorned for StepPending (not yet started); accent with a
// trailing "…" for StepRunning (in progress); Success-colored "✓" for
// StepDone; Danger-colored "✗" plus the wrapped error text for
// StepFailed.
func progressLine(p plan.Progress, palette theme.Palette) string {
	text := lipgloss.NewStyle().Foreground(palette.Text)
	switch p.State {
	case plan.StepRunning:
		return lipgloss.NewStyle().Foreground(palette.Accent).Render(p.Label + "…")
	case plan.StepDone:
		return text.Render(p.Label+"… ") + lipgloss.NewStyle().Foreground(palette.Success).Render("✓")
	case plan.StepFailed:
		line := text.Render(p.Label+"… ") + lipgloss.NewStyle().Foreground(palette.Danger).Render("✗")
		if p.Err != nil {
			line += "  " + lipgloss.NewStyle().Foreground(palette.Danger).Render(p.Err.Error())
		}
		return line
	default: // StepPending
		return dimHint(palette).Render(p.Label)
	}
}

// failureLines renders the keep-or-clean gate's own lines: a header, the
// k/c choice (or, when CleanDecision.Allowed is false, "k keep" alone plus
// the denial reason on its own line -- spec §9: "the clean option is
// disabled with the reason shown"), a short error line when SetCleanFailed
// has recorded a Clean attempt that itself failed, and, when
// ExecResult.PromptText is non-empty (only set on an OpAgentPrompt
// failure -- exec.go's own doc comment), a note surfacing it for manual
// paste (spec §9 step 3: "timeout ... prompt text surfaced back to the
// user for manual paste").
func (v *SubmitView) failureLines(inner int) []string {
	out := make([]string, 0, 5)

	header := lipgloss.NewStyle().Foreground(v.palette.Danger).Bold(true).Render("Step failed — choose how to proceed:")
	out = append(out, fitLine(header, inner))

	if v.clean.Allowed {
		choice := lipgloss.NewStyle().Foreground(v.palette.Text).Render("k keep    ·    c clean")
		out = append(out, fitLine(choice, inner))
	} else {
		keep := lipgloss.NewStyle().Foreground(v.palette.Text).Render("k keep")
		out = append(out, fitLine(keep, inner))
		reason := dimHint(v.palette).Render("clean disabled: " + v.clean.Reason)
		out = append(out, fitLine(reason, inner))
	}

	if v.cleanErr != nil {
		errLine := lipgloss.NewStyle().Foreground(v.palette.Danger).Render("clean failed: " + v.cleanErr.Error())
		out = append(out, fitLine(errLine, inner))
	}

	if v.result.PromptText != "" {
		note := dimHint(v.palette).Render("prompt not sent — copy manually: " + v.result.PromptText)
		out = append(out, fitLine(note, inner))
	}

	return out
}
