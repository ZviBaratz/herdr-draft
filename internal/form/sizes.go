// Derived from atrium (github.com/ZviBaratz/atrium) ui/overlay/textInput_size.go
// and ui/overlay/textInput_render.go, © Zvi Baratz, relicensed by the author.
//
// Adaptations from the source: Atrium's SetSize/fitRows shrink the
// create-form's own picker/prompt row counts (defaultPickerRows,
// defaultPromptRows, ...) to fit a short terminal, sharing one height
// budget across every section the overlay itself knows the concrete
// shape of (variantPicker, modelField, modeField, effortField,
// accountPicker, depsField -- each with its own named *SectionLines
// constant). herdr-draft's Section is opaque to this package -- there is
// no fixed, named set of field kinds to hold one shared constant per, and
// no per-kind SetVisibleRows/SetHeight setter this package is allowed to
// call (the Section interface has none; see form.go's doc comment) -- so
// the round-robin, shrink-the-shared-budget-in-priority-order algorithm
// itself is NOT ported: each Section decides its own preferred/degraded
// height as a pure function of the window height it is handed
// (Section.Height(winH)), independently of every other Section, with no
// coordination from this package. What this file ports instead is
// Atrium's *second* line of defence -- fitOverlay's post-hoc drop-lines
// cascade, for when even every Section's own best-effort degradation
// still doesn't fit (too many enabled sections, or a terminal shorter
// than every Section's own floor):
//
//   - dropLinesToFit is ported near-verbatim from fitOverlay's own helper
//     of the same name (textInput_render.go): remove interior lines
//     matching a droppable predicate, preserving the first and last line
//     unconditionally, until budget is met or nothing droppable remains.
//   - The blank-then-divider two-stage application of it (see fitToHeight
//     below) is ported from fitOverlay's own two calls to the same
//     helper with different predicates.
//   - fitOverlay's THIRD stage -- drop the default overlay heading, kept
//     only when a caller overrode Title (fork-from-checkpoint) -- has no
//     equivalent here: herdr-draft's form renders no heading line of its
//     own at all (spec §7: herdr's own popup chrome already draws the
//     title natively, outside this PTY; a second, redundant "New
//     session" line inside the form's own content would just repeat what
//     the surrounding popup frame already says). fitToHeight below keeps
//     the *stage* (a droppable "heading" line, identified by index) for
//     port fidelity and in case a future task adds one, but form.go never
//     supplies one, so in practice this stage is presently a no-op.
//   - The final clip-tail stage (fitOverlay's last block: keep the
//     content's own last line no matter what, since it's always the
//     submit control) is ported near-verbatim (clipTail below) --
//     preserved because herdr-draft's own Create section is, by
//     construction (form.go always appends it last), always that last
//     line, and spec §6 field 9 requires it "never clipped".
//   - Atrium's bordered-box arithmetic (`budget := t.height - 4`,
//     subtracting the box's own border+padding rows) is dropped outright:
//     herdr draws the popup's outer chrome, this package draws none of
//     its own (spec §7), so the budget fitToHeight is handed is the full
//     window height minus whatever fixed vertical padding form.go itself
//     reserves, not a border allowance.
package form

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Layout constants for the popup's own fixed chrome (form.go's compose):
// the left focus-marker gutter, the right margin, and one blank padding
// row top and bottom. These are herdr-draft's own numbers, not ported --
// Atrium's formChromeLines counts a wholly different, larger set of
// fixed rows (a bordered box, an overlay title, per-claude-field
// dividers) that has no equivalent in this form's flatter, borderless
// layout.
const (
	// gutterWidth is the left-margin column count reserved for the
	// focused-section accent marker (see form.go's decorateFocus):
	// one marker-glyph cell plus one separating space.
	gutterWidth = 2
	// rightMargin is the column count of blank space kept between the
	// widest content column and the popup's own right edge.
	rightMargin = 1
	// verticalPadding is the number of blank rows reserved top and
	// bottom of the composed content, before the degradation ladder in
	// fitToHeight considers dropping them again on a short terminal.
	verticalPadding = 1
)

// innerWidth returns the width available to a Section's own View(inner)
// call for a popup w columns wide: w minus the focus-marker gutter and
// the right margin, floored at 1 so a pathologically narrow window still
// asks for a renderable (if useless) width rather than 0 or negative --
// matching Picker/ChipRow/PromptArea's own "width <= 0 degrades instead
// of panicking" contract (see widgets/picker.go's widthStyle doc), which
// this package relies on rather than re-implementing.
func innerWidth(w int) int {
	inner := w - gutterWidth - rightMargin
	if inner < 1 {
		return 1
	}
	return inner
}

// dropLinesToFit removes interior lines matching droppable -- leading,
// trailing, and non-matching lines are always preserved -- until the
// slice is at most budget lines long or no droppable lines remain. Ported
// near-verbatim from Atrium's textInput_render.go dropLinesToFit.
func dropLinesToFit(lines []string, budget int, droppable func(string) bool) []string {
	excess := len(lines) - budget
	if excess <= 0 {
		return lines
	}
	out := make([]string, 0, len(lines))
	for i, l := range lines {
		if excess > 0 && i > 0 && i < len(lines)-1 && droppable(l) {
			excess--
			continue
		}
		out = append(out, l)
	}
	return out
}

// clipTail truncates lines to budget, keeping the FIRST budget-1 lines and
// then the very last line unconditionally -- ported near-verbatim from
// fitOverlay's own final clip step. It is the last-resort stage: whatever
// lines carried real content get dropped from the middle/end before the
// one line this form can least afford to lose (form.go's internal Create
// section, always the composed content's last line -- spec §6 field 9,
// "never clipped").
func clipTail(lines []string, budget int) []string {
	if budget < 1 || len(lines) <= budget {
		return lines
	}
	head := lines[: budget-1 : budget-1]
	return append(head, lines[len(lines)-1])
}

// fitToHeight applies spec §6's degradation ladder to composed content
// lines: drop interior blank lines, then interior divider lines, then (if
// headingIndex names one -- see the package doc's note that form.go never
// currently supplies one) that single heading line, then clip the tail --
// each stage only engaged if the previous one still leaves lines over
// budget. headingIndex < 0 means "no heading line in this render," making
// that stage a no-op; a value >= len(lines) is likewise ignored rather
// than panicking.
func fitToHeight(lines []string, budget int, divider string, headingIndex int) []string {
	if budget < 1 {
		return lines
	}
	lines = dropLinesToFit(lines, budget, func(l string) bool { return l == "" })
	lines = dropLinesToFit(lines, budget, func(l string) bool { return l == divider })
	if len(lines) > budget && headingIndex >= 0 && headingIndex < len(lines) {
		out := make([]string, 0, len(lines)-1)
		out = append(out, lines[:headingIndex]...)
		out = append(out, lines[headingIndex+1:]...)
		lines = out
	}
	return clipTail(lines, budget)
}

// paintLine explicitly paints bg as line's background across exactly
// width cells, surviving any ANSI resets already embedded in line (e.g.
// from a Section's own accent/dim-styled spans).
//
// This is NOT the same footgun Task 14 hit with word-wrap-before-truncate
// (widgets/picker.go's widthStyle doc): it is a *different* lipgloss v2
// composition hazard, verified the same way (rendering a real example and
// inspecting the raw bytes, not assumed from the docs) --
// lipgloss.Style.Render's own reset code (charm.land/x/ansi's
// ResetStyle, "\x1b[m") is unconditional: nesting an outer
// `Background(bg).Render(alreadyStyledContent)` around content that
// itself contains ANY inner styled span (e.g. an accent-colored focused
// label) does NOT paint bg behind the plain-text runs that follow that
// inner span within the same line, because the inner span's own trailing
// reset clears the outer background too, and nothing re-asserts it until
// the OUTER style's own padding step runs (confirmed by inspecting
// `lipgloss.NewStyle().Background(bg).Width(w).Inline(true).Render(...)`
// applied to a string containing one inner Foreground(...).Render(...)
// span: the text between that span and the line's padding renders with
// NO background code at all). Spec §7 requires "panel background
// painted explicitly across the full popup area (do not rely on
// terminal-default bg inside the popup PTY)" -- a background that visibly
// drops out after the first accent-colored span in a line would violate
// exactly that.
//
// The fix, verified against the same kind of real-bytes inspection:
// reassert bg's own SGR "set background" code (built via
// charm.land/x/ansi's own Style type, NOT lipgloss.Style.Render, so it
// carries no trailing reset of its own) immediately after every
// occurrence of ansi.ResetStyle already embedded in line, then prefix the
// whole thing with the same code so the line starts painted too. The
// final Width/MaxWidth/Inline(true) wrap (mirroring widgets/picker.go's
// widthStyle, and required for the same fixed-one-line reason its own doc
// comment gives) both pads/clips to width AND -- belt and suspenders,
// since the reasserted background is already the last SGR state active
// at that point -- re-paints the padding explicitly.
//
// bg == lipgloss.NoColor{} (the "terminal" builtin theme's stand-in for
// "inherit the terminal's own background," see internal/theme's package
// doc) is deliberately NOT painted at all: lipgloss.Style.Render's own
// getAsColor/Render logic special-cases that exact sentinel to mean "skip
// the Background SGR key, leave the terminal's own default" (verified in
// charm.land/lipgloss/v2@v2.0.5 style.go, `if bg != noColor`), and
// building the SGR bytes directly via ansi.Style here bypasses that
// special case -- so this function re-implements the same guard, rather
// than literally painting black (NoColor{}.RGBA() reports opaque black)
// over a user who picked the theme specifically to inherit their
// terminal's own background.
func paintLine(line string, width int, bg color.Color) string {
	if width < 1 {
		width = 1
	}
	if _, inherit := bg.(lipgloss.NoColor); inherit || bg == nil {
		return lipgloss.NewStyle().Width(width).MaxWidth(width).Inline(true).Render(line)
	}
	bgOn := ansi.Style{}.BackgroundColor(bg).String()
	painted := bgOn + strings.ReplaceAll(line, ansi.ResetStyle, ansi.ResetStyle+bgOn)
	return lipgloss.NewStyle().Background(bg).Width(width).MaxWidth(width).Inline(true).Render(painted)
}
