// Derived from atrium (github.com/ZviBaratz/atrium) ui/overlay/textInput_size.go
// and ui/overlay/textInput_render.go, © Zvi Baratz, relicensed by the author.
//
// What survives of that port is paintLine -- whose body has since moved
// to widgets.PaintLine, leaving the name here as a delegator, see its own
// doc comment -- plus two of this form's own layout constants. Everything
// else is gone:
//
//   - Atrium's shared height BUDGET -- reproduced here as allocateHeights
//     over the opaque Section interface -- went with v1's variable-height
//     sections. v2's stack rows are one line each, always, its only
//     variable-height region is the single panel, and rowlayout.go's
//     layoutFrame computes that arithmetically instead of arbitrating it
//     between fields.
//   - Atrium's DROP-LINES CASCADE -- fitOverlay's post-hoc degradation
//     pass, for when composed content simply does not fit, ported here as
//     fitToHeight over dropLinesToFit, clipKeeping and isBlankLine --
//     went one round later, and its own history is the cautionary part.
//     v2 spec §5 listed it under "Kept" on the assumption submitview.go
//     would still need a post-hoc pass; §9's priority ladder, settled two
//     sections later, made that false. layoutFrame's six components sum
//     to exactly the height it was asked for BY CONSTRUCTION, which is
//     why both composeRows and SubmitView.compose say in their own doc
//     comments that no degradation ladder runs and none is needed --
//     leaving the cascade with nothing to degrade. It then sat here
//     unreachable for most of the v2 program, still documenting v1's `▎`
//     gutter bar (decorateFocus, unreachable alongside it and deleted
//     with it) long after v2 had replaced that affordance with a
//     full-width ActiveRowBG fill -- which is exactly how dead code
//     misinforms: a reader greps for the bar, finds the function that
//     draws it, and believes the screen still has one.
//   - Atrium's bordered-box arithmetic (`budget := t.height - 4`,
//     subtracting the box's own border+padding rows) never applied at
//     all: herdr draws the popup's outer chrome and this package draws
//     none of its own (spec §7), so a render here is handed the full
//     window height, not a border allowance.
//
// One deliberate, disclosed behavioral difference from Atrium outlives
// the cascade, and paintLine is what still enforces this package's side
// of it. fitOverlay's OWN first stage -- unconditional, before its
// height budget check even started -- truncated each individual overlong
// line to innerWidth with a "…" tail
// (`truncate.StringWithTail(l, uint(innerWidth), "…")`,
// textInput_render.go:259-263, via github.com/muesli/reflow/truncate).
// It has no equivalent here and is deliberately not ported: this package
// adds no dependency on reflow/truncate, and no COMPOSED line silently
// loses its tail with an ellipsis. Two things cover the same underlying
// need without it -- every renderer here stays within the width it is
// handed (the same width-discipline convention widgets/picker.go's
// widthStyle doc establishes: Inline(true) plus MaxWidth, a hard clip,
// no ellipsis), and paintLine (below) applies its own `.MaxWidth(w)` as
// a last-resort backstop over the fully composed line (gutter + content
// + margin) regardless of what produced it. So overlong content is still
// bounded to the available width -- just clipped silently rather than
// marked with "…", a real difference from Atrium and not an oversight.
//
// CORRECTION for v2 (v2 spec §7), to the paragraph above: the
// no-ellipsis rule is REVERSED for the row stack's VALUE cells
// (rowvalues.go's keepHead/keepTail), which elide with a visible marker,
// keeping the informative end -- the tail for paths, the head for titles
// and branches. The v1 reasoning was about a dependency this package did
// not want, and about HINT lines, where running out of room costs the
// reader a suggestion they can live without. A one-line value cell is a
// different failure: a path or a branch name that loses its tail
// unmarked is not merely incomplete, it is MISREAD -- "~/Projects/
// herdr-dra" and "zvi/fix-login-redir" both read as real values. The
// dependency objection has lapsed too: charmbracelet/x/ansi is already
// imported by this very file. Everything else above still holds -- the
// silent clip remains the backstop for a composed line as a whole, and
// paintLine still runs last.
package form

import (
	"image/color"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/ZviBaratz/herdr-draft/internal/form/widgets"
)

// Layout constants for the popup's own fixed chrome: the marker gutter
// and the side margins. These are herdr-draft's own numbers, not ported --
// Atrium's formChromeLines counts a wholly different, larger set of fixed
// rows (a bordered box, an overlay title, per-claude-field dividers) that
// has no equivalent in this form's flatter, borderless layout.
//
// Both serve the FORM: rowlayout.go's contentBox measures against both,
// and the gutter is a marker column again -- v2 emptied it, on the
// premise that a full-width fill would carry focus by itself, and v3 spec
// §5.4 puts rowvalues.go's focus bar back in it because the fill turned
// out to be invisible (see focusBarGlyph for the whole reversal). There is
// no VERTICAL constant left here. v1 kept a verticalPadding = 1 for the blank row
// submitview.go reserved above and below its content; v2's frame has no
// padding rows at all (v2 spec §9's six components, none of them a
// spacer), so it went with the cascade that used to drop it again on a
// short terminal.
const (
	// gutterWidth is the marker column plus its separating space: a panel
	// picker's own cursor glyph, or a stack row's focus bar, and one
	// blank. It is content, not margin -- see sideMargin.
	gutterWidth = 2
	// sideMargin is the blank column kept on EACH side of the card, so
	// the header, the rules and the footer -- all drawn at the full box
	// width -- do not sit flush against herdr's own popup border (v3 spec
	// §6.2). v2 held back a rightMargin only, which is exactly the defect:
	// one column of air on the right and none on the left.
	sideMargin = 1
)

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
//
// The body moved to widgets.PaintLine for v3 spec §8.3: the picker hits
// the identical hazard painting its own selected-row fill around an accent
// match span, and widgets cannot import form (the dependency runs the
// other way). Everything above is the reasoning for both; this is now the
// form-side name for it, kept because the form's own call sites and the
// citations pointing here all read paintLine.
func paintLine(line string, width int, bg color.Color) string {
	return widgets.PaintLine(line, width, bg)
}

// boldSpan renders s bold, surviving the ANSI resets a Section's own
// accent- and dim-styled spans have already embedded in it. It is the
// third of v3 spec §5.4's focus signals, applied by form.go's
// renderStackRow to the focused row's VALUE cell only -- the label stays
// dim, and the row's other two signals are the fill and rowvalues.go's
// focus bar.
//
// This is paintLine's hazard in a second SGR key, so it takes paintLine's
// fix: lipgloss.Style.Render's trailing reset is unconditional, so an
// outer Bold(true).Render over content holding ANY inner styled span
// leaves every run after that span un-bolded, exactly as it leaves them
// unpainted. Reassert the attribute immediately after every reset already
// present, prefix the whole run so it starts set, and close it so nothing
// leaks into the padding. Read paintLine's doc above for the byte-level
// derivation; this is the same argument with Bold in place of a
// background color.
//
// It composes with paintLine rather than fighting it: paintLine runs last
// over the finished line and inserts its own background code after these
// same resets, leaving `reset + bg + bold` where this function left
// `reset + bold`. So a focused row comes out both painted and bold.
func boldSpan(s string) string {
	on := ansi.Style{}.Bold().String()
	return on + strings.ReplaceAll(s, ansi.ResetStyle, ansi.ResetStyle+on) + ansi.ResetStyle
}
