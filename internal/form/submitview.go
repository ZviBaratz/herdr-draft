// submitview.go renders v2 spec §12's submit pipeline: the staged
// progress stack and, once a step fails, the keep-or-clean gate.
//
// v2's premise for this screen is one sentence long -- "same header,
// rule, label column and button row as the form, so the pipeline does
// not read as a different program" -- and it is what this file is
// arranged around: the frame comes from rowlayout.go's layoutFrame, the
// content box and label column from contentBox/labelCol, the header/
// rule/footer from the same helpers form.go's composeRows uses, and the
// keep/clean choice from the same herdr action-button face the Create
// button wears (actionButtonText/panelContrastFG). Nothing here measures
// itself independently of the form any more; v1's submit view did (it
// used sizes.go's innerWidth and its own ad-hoc line list), which is
// exactly why it looked like a different program.
//
// SubmitView is deliberately NOT a form.Section: it has no ID/Enabled and
// takes no part in the focus ring (there is nothing to Tab between --
// the staged-progress and keep-or-clean views are read-only except for
// the single k/c choice, which this file's own Update handles directly
// rather than through keys.go's MapKey grammar, since neither "k" nor
// "c" appears anywhere in that grammar). It is a PURE VIEW over its
// setters: no herdr CLI call, no filesystem/network access, and no side
// effect beyond returning a tea.Cmd that emits KeepMsg/CleanMsg for the
// app layer's own Update to act on -- matching form.go's own "the form
// is a dumb view" posture (v2 spec §4), extended here to the view that
// follows it. In particular this file knows nothing about plan.Op: the
// app layer maps its ops to Steps (label, detail) and pushes them in,
// the same way it pushes every other verdict into a field.
//
// Deliberate, disclosed deviation from v2 spec §12's mockup: the mockup
// draws the state glyph two cells to the RIGHT of the two-cell gutter,
// which would push the submit view's labels two cells off the form's
// label column -- the one thing §12 asks for by name. The glyph goes IN
// the gutter instead, which is what §4's own panel mockup does with a
// picker's cursor glyph ("a picker's own cursor glyph lands in exactly
// that column", rowlayout.go's contentBox) and what the v1 compose path
// does with the focus marker (decorateFocus). Labels then land on the
// form's label column exactly, which
// TestSubmitView_LabelColumnMatchesTheForm pins.
package form

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

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
// remove button is drawn disabled and its reason is on screen, matching
// v2 spec §12's "clean stays disabled with its reason shown" (never a
// live-looking key that silently does nothing with no explanation).
type CleanMsg struct{}

// Step is one row of the submit view's stack (v2 spec §12): a short noun
// for the label column ("worktree", "claude", "prompt"), a detail for
// the value column, and the state its glyph is drawn from.
//
// Label and Detail are the APP layer's to write, not this package's.
// plan.Progress carries only an op's verb-phrase Label ("creating
// worktree") and its error, which is neither short enough for an
// eleven-cell label column nor informative enough for the value column
// v2 spec §12 draws ("zvi/fix-login-redirect-loop from main"); deriving
// either here would mean internal/form parsing internal/plan's op
// labels, which is precisely the knowledge this package is not allowed
// to have. internal/app owns that mapping (async.go's submitSteps), keyed
// on plan.OpKind rather than on any string.
//
// An empty Detail is normal and renders as the state's own word --
// "queued" for a step that has not started, which is exactly what §12's
// own mockup shows for the prompt row.
type Step struct {
	Label  string
	Detail string
	State  plan.StepState
}

// SubmitView renders v2 spec §12's staged-progress display and, once a
// step fails, its keep-or-clean failure prompt -- a pure view over its
// setters (see the file doc comment).
type SubmitView struct {
	palette theme.Palette

	// name/context are the header line's two halves, the same pair
	// form.Model carries (Setup.Name / SetContext). They are pushed in
	// rather than recomputed so the pipeline's header is literally the
	// form's header, v2 spec §12's first requirement.
	name    string
	context string

	steps []Step

	haveFailure bool
	// deadEnd marks the one failure with no keep-or-clean choice at all:
	// step 1 (topology creation) itself failed, so nothing was created
	// and there is nothing to keep or remove. It is also the ONLY
	// submitting state Esc/Ctrl+C may quit from (see the app layer's
	// updateSubmitting), so it is the only one whose footer offers a
	// close button -- a footer that advertised "esc close" at any other
	// point would be advertising a key the app deliberately ignores.
	deadEnd bool
	result  plan.ExecResult
	clean   plan.CleanDecision

	// cleanErr is SetCleanFailed's own recorded error, or nil before that
	// setter is ever called (the common case: keep succeeds silently, or
	// no clean was ever attempted).
	cleanErr error

	// unsentPromptPath/unsentPromptErr are SetUnsentPrompt's own recorded
	// recovery location for a prompt that never reached the agent, and the
	// error if it could not be written at all -- see unsentPromptLines.
	unsentPromptPath string
	unsentPromptErr  error
}

// NewSubmitView returns an empty SubmitView (no header, no steps, no
// failure) styled from palette.
func NewSubmitView(palette theme.Palette) *SubmitView {
	return &SubmitView{palette: palette}
}

// SetHeader sets the two halves of the header line -- the same pair the
// form's own header carries (v2 spec §4: the form's name on the left,
// live context for the selected project on the right). Either may be
// "", which renders that half empty; the header row itself is still part
// of the frame whenever the window affords it, because the frame is a
// function of (height, step count) alone.
func (v *SubmitView) SetHeader(name, context string) {
	v.name = name
	v.context = context
}

// SetSteps replaces the whole staged-progress stack. The app layer is
// expected to seed it with every op at plan.StepPending before the first
// event arrives -- so the user sees the full checklist immediately, not
// just whichever step happens to be running -- and to call this again on
// every plan.Progress event.
func (v *SubmitView) SetSteps(steps []Step) {
	v.steps = append([]Step(nil), steps...)
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

// SetDeadEnd records the step-1 failure that has no keep-or-clean choice
// at all (see the deadEnd field). It is deliberately separate from
// SetFailure: SetFailure means "there is a created space to decide
// about", and this means the opposite.
func (v *SubmitView) SetDeadEnd() {
	v.deadEnd = true
}

// SetCleanFailed records that a "c" (remove) attempt itself failed -- the
// herdr CLI call plan.Clean issued (spec §9: `herdr worktree remove` /
// `herdr workspace close`) returned a non-nil error, even though
// CleanCheck had already said it was safe to try. Rendered as a short
// error line above the buttons once haveFailure is true (a no-op before
// SetFailure, same zero-value safety as SetFailure itself; see Update's
// identical guard).
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

// SetUnsentPrompt records where the app layer saved a prompt that never
// reached the agent (spec §9 step 3: "prompt text surfaced back to the
// user for manual paste"), or the error that stopped it from saving.
//
// Added in the final v1 fix wave (finding I6). The failure view used to
// render the WHOLE prompt inline, through fitLine -- whose Inline(true)
// strips newlines and whose MaxWidth hard-clips to the popup width -- so
// a multi-paragraph Linear-seeded prompt (spec §10's own template is
// three paragraphs) collapsed into a single glued, truncated line, and
// then the popup closed and the text was gone for good. A path the user
// can `cat` survives the popup; the inline text never could, at any
// width.
func (v *SubmitView) SetUnsentPrompt(path string, err error) {
	v.unsentPromptPath = path
	v.unsentPromptErr = err
}

// Update handles the keep-or-clean gate's own k/c keys -- a no-op ("no
// panic on an unconstructed value or before a failure") until SetFailure
// has been called, and a no-op for "c" specifically when the
// CleanDecision denies it (see CleanMsg's own doc comment).
//
// Esc and Ctrl+C are deliberately NOT handled here at all, in any state.
// Whether they quit is the app layer's decision and only the app layer's
// (updateSubmitting): quitting mid-pipeline strands plan.Execute's own
// background goroutine on an unbuffered channel send nobody is left to
// drain, so the app scopes that exit to the step-1 dead end alone. A
// key grammar in this file that also quit would be a second, unscoped
// way out of exactly the state that must not have one.
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
//
// Unlike form.Model.ViewAt there is no bubblezone scan: this view has no
// clickable regions. The keep/clean buttons are keyboard-only, matching
// the k/c grammar Update implements -- v2 spec §7's zone list covers the
// form's rows, panel and Create/Cancel buttons and names nothing here.
func (v *SubmitView) ViewAt(w, h int) string {
	content := v.compose(w, h)
	var buf strings.Builder
	cw := &colorprofile.Writer{Forward: &buf, Profile: colorprofile.TrueColor}
	_, _ = cw.WriteString(content)
	return buf.String()
}

// compose assembles the view at exactly w x h using the FORM's own frame
// (rowlayout.go's layoutFrame over the step stack) and the form's own
// content box (contentBox/labelCol): header, rule, the step rows, rule,
// the failure region, footer. Every line is painted with palette.PanelBG
// across the full w columns (v2 spec §7), and the step the user should be
// looking at -- the running one, or the failed one -- is painted with
// ActiveRowBG, the same full-width fill the form marks its focused row
// with.
//
// There is no degradation ladder and none is needed, for exactly the
// reason composeRows needs none: layoutFrame's components sum to h by
// construction. The paint loop still clamps to h, belt and braces.
func (v *SubmitView) compose(w, h int) string {
	if h <= 0 {
		return ""
	}

	padLeft, inner := contentBox(w)
	boxWidth := gutterWidth + inner
	labelW, valueW := labelCol(inner)
	pad := strings.Repeat(" ", padLeft)

	f := layoutFrame(h, len(v.steps))
	active := v.activeStep()

	type composedLine struct {
		text string
		bg   theme.Color
	}
	lines := make([]composedLine, 0, h)
	add := func(text string, bg theme.Color) {
		lines = append(lines, composedLine{text: pad + text, bg: bg})
	}

	if f.Header {
		add(submitHeaderLine(v.name, v.context, boxWidth, v.palette), v.palette.PanelBG)
	}
	if f.Rule1 {
		add(dividerLine(boxWidth, v.palette), v.palette.PanelBG)
	}

	start := stackWindow(len(v.steps), f.Rows, active)
	for i := start; i < start+f.Rows && i < len(v.steps); i++ {
		bg := v.palette.PanelBG
		if i == active && v.steps[i].State != plan.StepPending {
			bg = v.palette.ActiveRowBG
		}
		add(v.stepRow(v.steps[i], labelW, valueW), bg)
	}

	if f.Rule2 {
		add(dividerLine(boxWidth, v.palette), v.palette.PanelBG)
	}
	for _, l := range v.regionLines(boxWidth, f.Region) {
		add(l, v.palette.PanelBG)
	}
	if f.Footer {
		add(v.footerLine(boxWidth), v.palette.PanelBG)
	}

	painted := make([]string, 0, h)
	for _, l := range lines {
		if len(painted) == h {
			break
		}
		painted = append(painted, paintLine(l.text, w, l.bg))
	}
	for len(painted) < h {
		painted = append(painted, paintLine("", w, v.palette.PanelBG))
	}
	return strings.Join(painted, "\n")
}

// activeStep is the index of the row the user should be looking at: the
// last step that has started (running, done or failed), or 0 when none
// has. It is both the row compose fills with ActiveRowBG and the row
// stackWindow keeps visible once the stack is taller than the window --
// the same job m.ring.index does for the form.
func (v *SubmitView) activeStep() int {
	active := 0
	for i, s := range v.steps {
		if s.State != plan.StepPending {
			active = i
		}
	}
	return active
}

// submitHeaderLine renders the header exactly as form.go's
// renderHeaderLine does -- the form's name bold on the left, the live
// project context dim and flush right.
//
// It is a deliberate copy of those four lines rather than a call into
// Model.renderHeaderLine, which would mean building a partial Model
// literal here and would break the day that method starts reading
// anything else off the Model. The copy is pinned against the real thing
// by TestSubmitView_HeaderMatchesTheForm, so a change to one that is not
// made to the other fails a test rather than drifting quietly.
func submitHeaderLine(name, context string, width int, p theme.Palette) string {
	if name != "" {
		name = lipgloss.NewStyle().Foreground(p.Text).Bold(true).Render(name)
	}
	if context != "" {
		context = dimText(p).Render(context)
	}
	return spreadLine(name, context, width)
}

// stepRow renders one step as a labeled row on the form's own grid: the
// state glyph in the two-cell gutter, the step's noun dim in the fixed
// label column, its detail in the value column. Deliberately identical
// in shape to form.go's renderStackRow -- same gutter, same labelW/
// valueW split, same dim-label/bright-value pairing (v2 spec §7) -- with
// the glyph standing where the form leaves blank indent.
func (v *SubmitView) stepRow(s Step, labelW, valueW int) string {
	label := ""
	if labelW > 0 {
		label = dimText(v.palette).Width(labelW).MaxWidth(labelW).Inline(true).Render(s.Label)
	}
	return stepGlyph(s.State, v.palette) + label + fitLine(v.stepValue(s, valueW), valueW)
}

// stepGlyph renders a step's state marker, padded to exactly the gutter's
// width so the label column starts where the form's does regardless of
// the glyph's own cell width: "✓" done, "›" running, "✗" failed, blank
// for a step that has not started.
func stepGlyph(state plan.StepState, p theme.Palette) string {
	var glyph string
	var fg theme.Color
	switch state {
	case plan.StepRunning:
		glyph, fg = "›", p.Accent
	case plan.StepDone:
		glyph, fg = "✓", p.Success
	case plan.StepFailed:
		glyph, fg = "✗", p.Danger
	default: // StepPending: no marker at all, just the indent.
		return strings.Repeat(" ", gutterWidth)
	}
	return fitLine(lipgloss.NewStyle().Foreground(fg).Render(glyph), gutterWidth)
}

// stepValue renders a step's value column: its Detail when the app layer
// supplied one, and otherwise the state's own word. A failed step's
// Detail is its error (the app layer writes it there), rendered in
// Danger.
//
// Long values are truncated with a visible marker (v2 spec §7's
// "row values truncate with a visible marker via ansi.Truncate ...
// keeping the informative end"), keeping the HEAD: a branch name, a
// title and an error message all say the most in their first cells.
func (v *SubmitView) stepValue(s Step, width int) string {
	text := s.Detail
	style := lipgloss.NewStyle().Foreground(v.palette.Text)
	switch s.State {
	case plan.StepFailed:
		style = lipgloss.NewStyle().Foreground(v.palette.Danger)
		if text == "" {
			text = "failed"
		}
	case plan.StepRunning:
		if text == "" {
			text = "working…"
		}
	case plan.StepDone:
		if text == "" {
			text = "done"
		}
	default: // StepPending
		style = dimText(v.palette)
		if text == "" {
			text = "queued"
		}
	}
	if width > 0 {
		text = ansi.Truncate(text, width, "…")
	}
	return style.Render(text)
}

// regionLines renders the panel region: blank while the pipeline is
// running, and the failure explanation once it has stopped.
//
// The explanation is BOTTOM-aligned, unlike the form's panel, which is
// top-aligned: every line of it exists to explain the buttons on the
// footer directly underneath (v2 spec §12's own mockup shows the reason
// line immediately above the button row), so it is anchored to them
// rather than floating a screen away under the rule. When the region is
// too short for the whole explanation it is clipped from the TOP, which
// makes the clean rationale/denial -- the last line, and the one §12
// requires to be on screen whenever remove is disabled -- the line that
// survives.
func (v *SubmitView) regionLines(width, region int) []string {
	if region <= 0 {
		return nil
	}
	body := v.failureBody(width)
	if len(body) > region {
		body = body[len(body)-region:]
	}
	out := make([]string, 0, region)
	for len(out)+len(body) < region {
		out = append(out, fitLine("", width))
	}
	return append(out, body...)
}

// failureBody is the explanation stack shown above the buttons, ordered
// least- to most-important down the screen (regionLines clips from the
// top): a failed remove attempt first, then the unsent prompt's recovery
// path, then the line explaining what remove would do or why it is
// unavailable. Empty while the pipeline is still running.
func (v *SubmitView) failureBody(width int) []string {
	if v.deadEnd {
		return []string{indentedLine(dimText(v.palette).Render(
			"nothing was created — there is nothing to keep or remove"), width)}
	}
	if !v.haveFailure {
		return nil
	}

	out := make([]string, 0, 5)
	if v.cleanErr != nil {
		out = append(out, indentedLine(lipgloss.NewStyle().Foreground(v.palette.Danger).Render(
			"remove failed: "+v.cleanErr.Error()), width))
	}
	out = append(out, v.unsentPromptLines(width)...)

	if v.clean.Allowed {
		out = append(out, indentedLine(dimText(v.palette).Render(
			"remove undoes everything this create made"), width))
	} else {
		out = append(out, indentedLine(lipgloss.NewStyle().Foreground(v.palette.Warning).Render(
			"remove unavailable — "+v.clean.Reason), width))
	}
	return out
}

// indentedLine renders one explanation line inside the content box: the
// same two-cell gutter indent the row stack's labels sit behind, so the
// explanation lines up with the labels above it rather than with the
// rule and the footer (v2 spec §4's own mockup puts the form's status
// line on exactly that column).
func indentedLine(s string, width int) string {
	indent := gutterWidth
	if indent > width {
		indent = width
	}
	return strings.Repeat(" ", indent) + fitLine(s, width-indent)
}

// unsentPromptLines renders spec §9 step 3's recovery surface for a
// prompt that never reached the agent: where it was saved, so the user
// can read it back after this popup closes. This is v2 spec §12's "the
// unsent-prompt path keeps its own row".
//
// The prompt's own text is deliberately NOT rendered here (finding I6).
// Every line in this view goes through fitLine, whose Inline(true) strips
// newlines and whose MaxWidth hard-clips to the popup's width -- so
// rendering the text inline turned a multi-paragraph, Linear-seeded
// prompt (spec §10's default template is three paragraphs) into one
// glued, truncated line, and the popup then closed and took it with it.
// Only when the save itself failed does the text appear inline, clipped,
// as a last-resort better-than-nothing: at that point a partial copy is
// all there is.
func (v *SubmitView) unsentPromptLines(width int) []string {
	if v.result.PromptText == "" {
		return nil
	}

	if v.unsentPromptErr != nil {
		return []string{
			indentedLine(lipgloss.NewStyle().Foreground(v.palette.Danger).Render(
				"prompt not sent, and could not be saved: "+v.unsentPromptErr.Error()), width),
			indentedLine(dimText(v.palette).Render("copy manually: "+v.result.PromptText), width),
		}
	}
	if v.unsentPromptPath == "" {
		// The save is still in flight (its Cmd has not landed yet): say
		// what happened without promising a path that may not exist.
		return []string{indentedLine(dimText(v.palette).Render(
			"prompt not sent — saving it for manual paste…"), width)}
	}
	// The path gets a line to itself: a plugin state dir plus a filename
	// routinely runs past 60 cells, and sharing a row with the explanation
	// would clip the tail -- which is the half the user actually has to
	// type.
	return []string{
		indentedLine(dimText(v.palette).Render("prompt not sent — saved for manual paste:"), width),
		indentedLine(dimText(v.palette).Render(v.unsentPromptPath), width),
	}
}

// footerLine renders the footer on the form's own footer grammar (v2
// spec §5/§12): a status hint on the left, action buttons flush right,
// the buttons never traded away for the hint.
func (v *SubmitView) footerLine(width int) string {
	hint, buttons := v.footerParts()
	if len(buttons) == 0 {
		return fitLine(hint, width)
	}
	right := strings.Join(buttons, strings.Repeat(" ", footerButtonGap))
	rightWidth := lipgloss.Width(right)
	if rightWidth >= width {
		return fitLine(right, width)
	}
	if avail := width - rightWidth - footerButtonGap; avail > 0 {
		hint = ansi.Truncate(hint, avail, "…")
	} else {
		hint = ""
	}
	return spreadLine(hint, right, width)
}

// footerParts is the footer's contents for each of the pipeline's three
// resting states.
//
// While the pipeline runs there are NO buttons and no key hints, because
// there are no keys: the app layer ignores every key mid-pipeline,
// Esc/Ctrl+C included (quitting there strands plan.Execute on an
// unbuffered channel send). A footer offering "esc cancel" -- which is
// what the form's own footer says one keystroke earlier -- would be
// advertising an exit that does not exist, so it shows a step counter
// instead.
func (v *SubmitView) footerParts() (hint string, buttons []string) {
	switch {
	case v.deadEnd:
		return "", []string{submitButton("esc", "close", buttonPrimary, v.palette)}
	case v.haveFailure:
		keep := submitButton("k", "keep it", buttonPrimary, v.palette)
		if v.clean.Allowed {
			return "", []string{keep, submitButton("c", "remove it", buttonSecondary, v.palette)}
		}
		// A disabled button drops its key glyph: the face is what
		// advertises a working key, and this key does nothing (Update).
		// The reason why is on the line directly above it.
		return "", []string{keep, submitButton("", "remove it", buttonDisabled, v.palette)}
	default:
		return dimText(v.palette).Render(v.stepCounter()), nil
	}
}

// stepCounter is the running pipeline's own footer hint, "step 2 of 4".
// "" when there are no steps at all.
func (v *SubmitView) stepCounter() string {
	if len(v.steps) == 0 {
		return ""
	}
	return fmt.Sprintf("step %d of %d", v.activeStep()+1, len(v.steps))
}

// buttonKind selects one of herdr's three action-button faces (v2 spec
// §7: "the primary filled with the accent color, the secondary on a
// surface background").
type buttonKind int

const (
	buttonPrimary buttonKind = iota
	buttonSecondary
	buttonDisabled
)

// submitButton renders one footer action button at its intrinsic width,
// reusing form.go's own herdr port (actionButtonText's " {hint} {label} "
// shape and panelContrastFG's knocked-out foreground) so the keep/remove
// buttons wear exactly the face the Create button does one screen
// earlier.
func submitButton(hint, label string, kind buttonKind, p theme.Palette) string {
	var style lipgloss.Style
	switch kind {
	case buttonPrimary:
		style = lipgloss.NewStyle().Foreground(panelContrastFG(p)).Background(p.Accent).Bold(true)
	case buttonSecondary:
		style = lipgloss.NewStyle().Foreground(p.Text).Background(p.Surface)
	default:
		style = lipgloss.NewStyle().Foreground(p.DimText).Background(p.Surface)
		hint = ""
	}
	return style.Inline(true).Render(actionButtonText(hint, label))
}
