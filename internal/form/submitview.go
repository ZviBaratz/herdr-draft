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
// that column", rowlayout.go's contentBox) and what the form's own row
// stack does with its focus bar (v3 spec §5.4). Labels then land on the
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

	// waitingHint is SetWaitingHint's own footer instruction for a step
	// waiting on the user, or "" for defaultWaitingHint.
	waitingHint string

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
//
// It takes the result anyway, and for one reason: a dead end can still
// carry an unsent prompt. Since #90 generalised ExecResult.PromptText from
// "the prompt op failed" to "the prompt did not land", a plan that never
// got past `worktree create` reports the prompt the user had composed, and
// unsentPromptLines needs to know there is one in order to say so while
// the save is still in flight. There is no CleanDecision to pass, because
// there is nothing to decide about.
func (v *SubmitView) SetDeadEnd(res plan.ExecResult) {
	v.deadEnd = true
	v.result = res
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
// content box (contentBox/labelCol): the top margin, header, rule, the
// step rows, rule, the failure region, rule 3, footer, the bottom margin.
// Every line is painted with palette.PanelBG across the full w columns
// (v2 spec §7), and the step the user should be looking at -- the running
// one, or the failed one -- is painted with ActiveRowBG, the same
// full-width fill the form marks its focused row with.
//
// The two margins and rule 3 are v3 spec §7's, and this view emits them
// for the same reason it shares layoutFrame at all: the submit screen
// replaces the form in place, so anything that moves the form's header or
// footer must move this one's identically or the swap visibly jumps.
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

	for i := 0; i < f.PadTop; i++ {
		add("", v.palette.PanelBG)
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

	// Rule 2 is the REGION's own top border here, not a fixed line under
	// the steps, so its line is spent out of the region's budget rather
	// than ahead of it -- see regionLines.
	region := f.Region
	if f.Rule2 {
		region++
	}
	for _, l := range v.regionLines(boxWidth, region, f.Rule2) {
		add(l, v.palette.PanelBG)
	}
	// Rule 3 is a PLAIN divider here, deliberately not folded into the
	// region the way rule 2 above is (v3 spec §7.4's note): rule 2 is
	// spent out of the region's budget because it travels down with the
	// bottom-anchored explanation and is omitted when there is none,
	// whereas rule 3 closes the card at a fixed line whether or not the
	// region has anything in it. Folding it in too would have moved it
	// away from the form's own rule 3 and left
	// TestSubmitView_RuleMatchesTheForm needing a story.
	if f.Rule3 {
		add(dividerLine(boxWidth, v.palette), v.palette.PanelBG)
	}
	if f.Footer {
		add(v.footerLine(boxWidth), v.palette.PanelBG)
	}
	for i := 0; i < f.PadBottom; i++ {
		add("", v.palette.PanelBG)
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
// the glyph's own cell width: "✓" done, "›" running, "✗" failed, "…"
// waiting on the user, blank for a step that has not started.
//
// The waiting glyph is deliberately not "›". A running step and a waiting
// one differ in whose turn it is, and the gutter is the first thing anyone
// reads: an accent chevron on a pipeline that has stopped for the user
// would say the machine is still working, which is the one thing that is
// not true (#115).
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
	case plan.StepWaiting:
		glyph, fg = "…", p.Warning
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
	case plan.StepWaiting:
		style = lipgloss.NewStyle().Foreground(v.palette.Warning)
		if text == "" {
			text = "waiting on you…"
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

// regionLines renders the panel region, INCLUDING rule 2 when rule is
// true (compose spends the rule's line out of region for exactly this
// reason): blank while the pipeline is running, and the failure
// explanation, under its own rule, once the pipeline has stopped.
//
// The explanation is BOTTOM-aligned, unlike the form's panel, which is
// top-aligned: every line of it exists to explain the buttons on the
// footer directly underneath (v2 spec §12's own mockup shows the reason
// line immediately above the button row), so it is anchored to them
// rather than floating a screen away under the rule. When the region is
// too short for the whole explanation it is clipped from the TOP, which
// takes the rule first and makes the clean rationale/denial -- the last
// line, and the one §12 requires to be on screen whenever remove is
// disabled -- the line that survives.
//
// The rule travels DOWN with the explanation instead of sitting under
// the step rows, and is omitted entirely when there is no explanation.
// The form draws its own rule 2 above a region that always holds the
// focused field's chooser, so slack under it is correct there; this view
// has no chooser, and a rule with nothing beneath it -- sixteen blank
// lines above a footer, on the progress screen -- was a divider drawn
// over nothing. Trailing blanks above a bottom-anchored footer are not
// the defect and are left alone.
func (v *SubmitView) regionLines(width, region int, rule bool) []string {
	if region <= 0 {
		return nil
	}
	body := v.failureBody(width)
	if rule && len(body) > 0 && len(body) < region {
		body = append([]string{dividerLine(width, v.palette)}, body...)
	}
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
		// The unsent prompt goes ABOVE the dead-end line, following this
		// stack's own least- to most-important ordering: "nothing was
		// created" is what explains the single `esc close` button, so it
		// is the line that must survive regionLines clipping from the top.
		// A dead end can carry an unsent prompt since #90 generalised
		// ExecResult.PromptText -- a plan that never got past `worktree
		// create` still had one composed.
		return append(v.unsentPromptLines(width), indentedLine(dimText(v.palette).Render(
			"nothing was created — there is nothing to keep or remove"), width))
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
		// `<state>  <reason>`, two spaces and no dash: v2 spec §6's own
		// form for "unavailable, with a reason" (§6.1's `unavailable  no
		// API key`, and the issue row that already renders it that way).
		// This line used an em dash, which made the form's two screens
		// spell the same idiom two ways.
		out = append(out, indentedLine(lipgloss.NewStyle().Foreground(v.palette.Warning).Render(
			"remove unavailable"+unavailableReasonSep+v.clean.Reason), width))
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

	w := v.promptWording()
	if v.unsentPromptErr != nil {
		return []string{
			indentedLine(lipgloss.NewStyle().Foreground(v.palette.Danger).Render(
				w.saveFailed+v.unsentPromptErr.Error()), width),
			indentedLine(dimText(v.palette).Render(w.copyLead+v.result.PromptText), width),
		}
	}
	if v.unsentPromptPath == "" {
		// The save is still in flight (its Cmd has not landed yet): say
		// what happened without promising a path that may not exist.
		return []string{indentedLine(dimText(v.palette).Render(w.saving), width)}
	}
	// The path gets a line to itself: a plugin state dir plus a filename
	// routinely runs past 60 cells, and sharing a row with the explanation
	// would clip the tail -- which is the half the user actually has to
	// type.
	return []string{
		indentedLine(dimText(v.palette).Render(w.saved), width),
		indentedLine(dimText(v.palette).Render(v.unsentPromptPath), width),
	}
}

// promptWording is the three leads unsentPromptLines can need, chosen by
// what is actually KNOWN about the prompt's fate rather than by what
// failed.
//
// The not-sent set is #90's original wording, pinned byte-for-byte by two
// golden frames, and it stays: the dialog guard's refusal really did not
// deliver anything, and offering the text back is the right answer there.
//
// The unconfirmed set exists because #108 made "prompt not sent" a claim
// the form cannot support. `herdr agent prompt --wait` completes only on
// observed working-or-blocked activity, so a large prompt into an agent
// slow to paint its status transition times out while running perfectly
// well -- in the live case, several tool calls deep. Every line therefore
// leads with what is actually known and carries the instruction that
// replaces "paste this": read the pane first. Pasting is precisely how a
// working agent gets its instructions twice.
//
// Room matters here. The popup is a fixed 104 cells and these lines are
// indented inside a bordered panel, so each lead has to fit beside its
// own suffix -- which is why "read the pane" rather than a sentence.
type promptWording struct {
	// saveFailed precedes the save error, with the text inline after it.
	saveFailed string
	// saving is shown while the save Cmd is still in flight.
	saving string
	// saved precedes the path, which gets a line of its own.
	saved string
	// copyLead precedes the prompt rendered INLINE, which only happens
	// when the save itself failed and there is no path to point at. It
	// needs its own wording rather than inheriting saveFailed's: this is
	// the one line that puts the text in front of the user and implicitly
	// tells them what to do with it, so on an unconfirmed delivery a bare
	// "copy manually:" reinstates the double-paste directly under a lead
	// that had just been corrected. #108's own mutation sweep found this
	// branch unpinned, which is why all three now assert the property
	// rather than the one that had a fixture.
	copyLead string
}

func (v *SubmitView) promptWording() promptWording {
	if v.result.PromptUnconfirmed {
		return promptWording{
			saveFailed: "delivery unconfirmed, and the prompt could not be saved: ",
			saving:     "delivery unconfirmed — read the pane; saving the prompt…",
			saved:      "delivery unconfirmed — read the pane, then paste from:",
			copyLead:   "read the pane, then copy: ",
		}
	}
	return promptWording{
		saveFailed: "prompt not sent, and could not be saved: ",
		saving:     "prompt not sent — saving it for manual paste…",
		saved:      "prompt not sent — saved for manual paste:",
		copyLead:   "copy manually: ",
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
	case v.waitingOnTheUser():
		return lipgloss.NewStyle().Foreground(v.palette.Warning).Render(v.waitingHintOrDefault()), nil
	default:
		return dimText(v.palette).Render(v.stepCounter()), nil
	}
}

// defaultWaitingHint is what the footer says while a step is waiting on the
// person at the keyboard (#115) and nobody has pushed in something more
// specific.
//
// It goes in the FOOTER rather than in the waiting row's value column for
// two reasons. The footer is the widest line on the screen and the only one
// whose job is already a hint, so the instruction survives at the 80-cell
// pessimistic width where a row value would be truncated mid-word; and the
// row keeps saying what the step IS, so the screen reads as one pipeline
// paused rather than as a row that changed its mind.
//
// It says where to look rather than what the dialog is: with a `here`
// placement herdr has already moved the user to the agent's pane, so "in
// the pane" is where they are, and the popup is what is in their way.
//
// Both this and anything SetWaitingHint pushes in must fit the 78 cells an
// 80-column popup leaves: footerLine truncates a hint with no visible
// marker at all when there are no buttons beside it, so an over-long
// instruction does not look truncated, it looks finished.
const defaultWaitingHint = "answer the dialog in the pane — this carries on by itself once you do"

// SetWaitingHint replaces the footer instruction shown while a step is
// waiting on the user. "" restores defaultWaitingHint.
//
// A setter rather than a constant because the most useful thing this line
// can say -- that the prompt the user typed will be delivered the moment
// they answer -- is only TRUE for a plan that carries a prompt op, and
// internal/form is deliberately not allowed to know which ops a plan has
// (see this file's own doc comment). internal/app resolves it from
// plan.Input, the same way it resolves every other row's words.
func (v *SubmitView) SetWaitingHint(hint string) { v.waitingHint = hint }

// waitingHintOrDefault is SetWaitingHint's value, or defaultWaitingHint
// when nothing was pushed in -- this view's usual zero-value-safe posture.
func (v *SubmitView) waitingHintOrDefault() string {
	if v.waitingHint == "" {
		return defaultWaitingHint
	}
	return v.waitingHint
}

// waitingOnTheUser reports whether any step has stopped for the person at
// the keyboard. Any, not the active one: the active row is the LAST step
// that has started, and nothing forbids a later step from having started
// too -- reading the whole stack is what keeps the footer honest without
// depending on that ordering.
func (v *SubmitView) waitingOnTheUser() bool {
	for _, s := range v.steps {
		if s.State == plan.StepWaiting {
			return true
		}
	}
	return false
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
