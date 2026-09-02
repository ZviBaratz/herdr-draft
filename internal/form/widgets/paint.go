package widgets

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// PaintLine explicitly paints bg as line's background across exactly width
// cells, surviving any ANSI resets already embedded in line -- e.g. from an
// accent-colored match span inside a picker row, or the several resets
// textinput.View emits inside one input line.
//
// It lives here, rather than in internal/form where it was written, because
// v3 spec §8.3 needs it on both sides of the package boundary: a picker row
// with an accent match span inside an outer Background(Surface) fill loses
// that fill after the span, and widgets cannot import form (form already
// imports widgets, so the dependency only runs one way). form.paintLine is
// now a delegator to this function, and its own doc comment carries the
// full byte-level derivation of the hazard and the fix -- read that one for
// the reasoning; what follows is the short form.
//
// lipgloss.Style.Render's trailing reset (charm.land/x/ansi's ResetStyle,
// "\x1b[m") is unconditional, so an outer Background(bg).Render over
// content that already holds ANY inner styled span leaves the runs after
// that span with no background code at all -- the inner span's own reset
// clears the outer background too, and nothing re-asserts it until the
// outer style's padding step runs. The fix is to reassert bg's own "set
// background" SGR immediately after every ResetStyle already present in
// line (built via ansi.Style, NOT lipgloss.Style.Render, so the
// reassertion carries no trailing reset of its own) and to prefix the whole
// line with the same code so it starts painted.
//
// The closing Width/MaxWidth/Inline(true) wrap is the same one widthStyle
// builds and is required for the same reason its doc comment gives:
// Inline(true) suppresses the word-wrap step Render would otherwise run
// before truncating, which is what keeps this a single line of exactly
// width cells.
//
// bg == lipgloss.NoColor{} (the "terminal" builtin theme's stand-in for
// "inherit the terminal's own background," see internal/theme's package
// doc) is deliberately NOT painted: Style.Render special-cases that exact
// sentinel to mean "skip the Background SGR key", and building the SGR
// bytes directly via ansi.Style here bypasses that special case -- so the
// guard is re-implemented rather than painting literal black
// (NoColor{}.RGBA() reports opaque black) over a user who picked the theme
// specifically to inherit their terminal's own background. A nil bg is
// treated the same way.
func PaintLine(line string, width int, bg color.Color) string {
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
