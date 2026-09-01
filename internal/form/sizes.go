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
// call. Atrium's shared-budget shrink IS reproduced here, in
// allocateHeights below, but over the opaque Section interface rather than
// over a closed enum of field kinds: every Section reports a preferred
// height for the current window (Section.Height(winH)) and a floor
// (Section.MinHeight()), and allocateHeights spends the popup's rows across
// them -- preferences when they fit, floors plus a focus-first refill when
// they do not. form.go's compose then hands each Section the height it was
// allocated (Section.View(inner, h)).
//
// An earlier version of this file did NOT do that: each Section returned a
// fixed constant from Height(winH), ignoring winH entirely, and compose
// never consulted Height at all -- it split each Section's View output on
// newlines and left the cascade below to absorb whatever overflowed. The
// result was a form that did not fit its own popup (the Prompt field first
// appeared at a window height of 43 rows, the footer at 48), which is what
// allocateHeights exists to fix. The cascade below is now what it was
// always meant to be: Atrium's *second* line of defence -- fitOverlay's
// post-hoc drop-lines cascade, for when even the allocation cannot fit
// (a terminal shorter than every Section's own floor put together):
//
//   - fitOverlay's OWN first stage -- run unconditionally, before its
//     height budget check even starts -- truncates each individual
//     overlong line to innerWidth with a "…" tail
//     (`truncate.StringWithTail(l, uint(innerWidth), "…")`,
//     textInput_render.go:259-263, via github.com/muesli/reflow/truncate)
//     has NO equivalent here, and is deliberately not ported: this
//     package adds no dependency on reflow/truncate, and no line in a
//     composed form silently loses its tail with an ellipsis. Two things
//     cover the same underlying need without it -- a Section's own
//     View(inner) is responsible for staying within the inner width it's
//     handed (the same width-discipline convention
//     widgets/picker.go's widthStyle doc already establishes:
//     Inline(true) plus MaxWidth, a hard clip, no ellipsis), and
//     paintLine (this file, below) applies its own `.MaxWidth(w)` as a
//     last-resort backstop over the fully composed line (gutter +
//     content + margin) regardless of what produced it. So overlong
//     content is still bounded to the available width -- just clipped
//     silently rather than marked with "…" -- a real, disclosed
//     behavioral difference from Atrium, not an oversight.
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
//     submit control) is ported near-verbatim (clipKeeping below) --
//     preserved because herdr-draft's own Create section is, by
//     construction (form.go always appends it last), always that last
//     line, and spec §6 field 9 requires it "never clipped". clipKeeping
//     adds one thing Atrium's own has no equivalent of: a protect mask,
//     so the FOCUSED section's lines are kept alongside the last one. With
//     an empty mask it is byte-for-byte the same clip.
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
	// footerRows is the number of rows form.go's compose reserves for
	// footer.go's key ladder, always exactly one line (fitFooter picks a
	// rung that fits the width; it never wraps).
	footerRows = 1
	// formChromeRows is the total number of rows compose spends on
	// something other than a caller-supplied Section's own content: the
	// top padding row, the footer, and the internal Create section (spec
	// §6 field 9, "never clipped", so its one row is never available to
	// anything else). Field Sections subtract it when sizing themselves
	// against a window height -- see promptRowsAt/pickerRowsAt.
	formChromeRows = verticalPadding + footerRows + 1
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

// pickerChromeRows is the row count every picker-backed field Section
// spends on its own fixed chrome before a single candidate row is drawn:
// the label/selection header, plus the always-reserved hint/verdict row
// underneath it.
const pickerChromeRows = 2

// pickerRowsAt returns the candidate-row count a picker-backed field
// renders at its PREFERRED height in a popup winH rows tall (spec §6's
// "constant-height sections for a given window size" -- the constant is a
// function of the window, which is exactly what this makes real): its own
// preferred count while the window can hold the form's fixed chrome plus a
// full-size picker, then one row fewer per row the window is short of
// that, floored at one. A picker showing no candidates at all is not a
// picker, so this never returns 0 -- collapsing an UNFOCUSED picker's rows
// away entirely is allocateHeights' job (see its own doc comment), decided
// per render from the whole form's budget, not by a field sizing itself in
// isolation.
func pickerRowsAt(preferred, winH int) int {
	if preferred < 1 {
		return 1
	}
	room := winH - formChromeRows - pickerChromeRows
	if room >= preferred {
		return preferred
	}
	if room < 1 {
		return 1
	}
	return room
}

// promptRowsAt returns the textarea row count PromptField renders at its
// preferred height in a popup winH rows tall -- spec §6 field 8 verbatim:
// "4 rows preferred, 1 floor" (widgets.PromptAreaPreferredRows /
// PromptAreaMinRows), shrinking between the two as the window gets short.
// promptLabelRows is the field's own one-line label header.
const promptLabelRows = 1

func promptRowsAt(preferred, minimum, winH int) int {
	if preferred < minimum {
		preferred = minimum
	}
	room := winH - formChromeRows - promptLabelRows
	if room >= preferred {
		return preferred
	}
	if room < minimum {
		return minimum
	}
	return room
}

// allocateHeights distributes budget rows of composed content across
// sections, returning one rendered height per section (the value compose
// passes to each Section.View) plus whether the composed content can still
// afford a divider row after every body section.
//
// This is the piece spec §6's "constant-height sections for a given window
// size" always needed and never had: Section.Height(winH) alone is one
// field's opinion of its own PREFERRED size, computed with no knowledge of
// how many other fields share the popup, and summing those preferences for
// the real, assembled form (ten field Sections, five of them picker-backed)
// overflows any popup a 24-to-50-row terminal can produce. The ladder in
// fitToHeight below is a last-resort line-dropper, not a layout: left to
// itself it amputates whatever did not fit, which is how the Prompt field
// and the footer came to be invisible at every height under 43 rows.
//
// The allocation, in order:
//
//   - Every preference plus a divider per body section fits: everyone gets
//     what they asked for, dividers included. This is the case every
//     single-section golden frame in this package is in, which is why they
//     render byte-identically to before this function existed.
//   - Every preference fits without dividers: dividers go first, matching
//     spec §6's own degradation order ("drop dividers" ranks ahead of
//     "clip tail"; content outranks separators).
//   - Otherwise every section starts at its own MinHeight() floor and the
//     spare rows are handed out: the FOCUSED section fills to its full
//     preference first (the user can only interact with one field at a
//     time, and a picker collapsed to its floor while focused is a field
//     the user cannot use), then every other section grows one row at a
//     time in ring order until the budget runs out. This is what collapses
//     the ~22 permanently-blank reserved picker rows an unfocused
//     Issue/Project/Base/Agent/Account field would otherwise hold onto.
//   - When even the floors do not fit (a popup shorter than roughly 14
//     rows), the floors are returned anyway and fitToHeight's own ladder
//     takes over -- with the focused section's lines marked protected, so
//     the one field the user is actually editing is the last thing dropped
//     rather than an arbitrary victim of the tail clip.
//
// The cost of this is the one thing spec §6's "constant-height" wording
// promised and this cannot deliver: at a fixed window size, moving focus
// now reflows the form, because the focused field grows and the field it
// left shrinks. That trade is deliberate. At 80x24 the ten-field form has
// roughly two rows per field; a layout that keeps every section a fixed
// height at that size is a layout in which no picker ever shows a single
// candidate.
func allocateHeights(sections []Section, focusIdx, winH, budget int) ([]int, bool) {
	n := len(sections)
	if n == 0 {
		return nil, false
	}

	pref := make([]int, n)
	floor := make([]int, n)
	prefTotal, floorTotal := 0, 0
	for i, s := range sections {
		p := s.Height(winH)
		if p < 1 {
			p = 1
		}
		f := s.MinHeight()
		if f < 1 {
			f = 1
		}
		if f > p {
			f = p
		}
		pref[i], floor[i] = p, f
		prefTotal += p
		floorTotal += f
	}

	// One divider follows every body section (compose appends the Create
	// section last and never draws a divider after it).
	dividers := n - 1

	if prefTotal+dividers <= budget {
		return pref, true
	}
	if prefTotal <= budget {
		return pref, false
	}
	if floorTotal >= budget {
		return floor, false
	}

	heights := append([]int(nil), floor...)
	spare := budget - floorTotal

	if focusIdx >= 0 && focusIdx < n {
		want := pref[focusIdx] - heights[focusIdx]
		if want > spare {
			want = spare
		}
		heights[focusIdx] += want
		spare -= want
	}

	for spare > 0 {
		grew := false
		for i := 0; i < n && spare > 0; i++ {
			if heights[i] >= pref[i] {
				continue
			}
			heights[i]++
			spare--
			grew = true
		}
		if !grew {
			break
		}
	}
	return heights, false
}

// isBlankLine reports whether l carries no visible content -- whitespace
// only, once every ANSI escape sequence is stripped out of it.
//
// This is the fix for a stage of the ladder below that had been dead since
// the day it was written: fitToHeight's drop-blank-lines stage tested
// `l == ""`, but form.go's compose runs every composed line through
// decorateFocus first, which prefixes a two-cell focus-marker gutter, and
// every field Section pads its own reserved-but-empty rows out to the full
// inner width. No line reaching fitToHeight has EVER been the empty
// string, so the stage the spec's degradation ladder puts SECOND never
// once fired -- every overlong render skipped straight from "drop
// dividers" to the tail clip. submitview.go's compose had the identical
// dead test, fixed the same way.
func isBlankLine(l string) bool {
	return strings.TrimSpace(ansi.Strip(l)) == ""
}

// dropLinesToFit removes interior lines matching droppable -- leading,
// trailing, protected, and non-matching lines are always preserved --
// until the slice is at most budget lines long or no droppable lines
// remain. Ported near-verbatim from Atrium's textInput_render.go
// dropLinesToFit; the protect mask (nil, or one entry per line; see
// fitToHeight) is this package's own addition.
func dropLinesToFit(lines []string, protect []bool, budget int, droppable func(string) bool) ([]string, []bool) {
	excess := len(lines) - budget
	if excess <= 0 {
		return lines, protect
	}
	outLines := make([]string, 0, len(lines))
	outProtect := make([]bool, 0, len(lines))
	for i, l := range lines {
		if excess > 0 && i > 0 && i < len(lines)-1 && !isProtected(protect, i) && droppable(l) {
			excess--
			continue
		}
		outLines = append(outLines, l)
		outProtect = append(outProtect, isProtected(protect, i))
	}
	return outLines, outProtect
}

// isProtected reports whether index i is marked in the protect mask -- a
// nil or short mask means "not protected", so every caller can pass nil
// for "nothing to protect".
func isProtected(protect []bool, i int) bool {
	return i >= 0 && i < len(protect) && protect[i]
}

// clipKeeping truncates lines to budget, keeping the very last line
// unconditionally (form.go's internal Create section -- spec §6 field 9,
// "never clipped"), then every protected line (the focused Section's own
// rows), then as much of the rest as still fits, taken from the top.
//
// With an empty protect mask this is exactly Atrium's own fitOverlay tail
// clip, which it replaces: the first budget-1 lines plus the last one. The
// protect mask is what makes spec §6's other promise hold -- the field the
// user is currently editing is never the line this stage throws away. A
// protected block larger than the whole budget still degrades gracefully
// (its first budget-1 lines survive) rather than overflowing.
func clipKeeping(lines []string, protect []bool, budget int) []string {
	if budget < 1 || len(lines) <= budget {
		return lines
	}

	keep := make([]bool, len(lines))
	keep[len(lines)-1] = true
	kept := 1

	for i := range lines {
		if kept >= budget {
			break
		}
		if !keep[i] && isProtected(protect, i) {
			keep[i] = true
			kept++
		}
	}
	for i := range lines {
		if kept >= budget {
			break
		}
		if !keep[i] {
			keep[i] = true
			kept++
		}
	}

	out := make([]string, 0, budget)
	for i, l := range lines {
		if keep[i] {
			out = append(out, l)
		}
	}
	return out
}

// fitToHeight applies spec §6's degradation ladder to composed content
// lines: drop interior blank lines, then interior divider lines, then (if
// headingIndex names one -- see the package doc's note that form.go never
// currently supplies one) that single heading line, then clip what is
// left -- each stage only engaged if the previous one still leaves lines
// over budget. headingIndex < 0 means "no heading line in this render,"
// making that stage a no-op; a value >= len(lines) is likewise ignored
// rather than panicking. protect, when non-nil, carries one entry per
// input line marking the lines no stage may drop (form.go's compose marks
// the focused Section's own rows and the Create button's); nil protects
// nothing beyond the first and last lines every stage already preserves.
func fitToHeight(lines []string, protect []bool, budget int, divider string, headingIndex int) []string {
	if budget < 1 {
		return lines
	}
	lines, protect = dropLinesToFit(lines, protect, budget, isBlankLine)
	lines, protect = dropLinesToFit(lines, protect, budget, func(l string) bool { return l == divider })
	if len(lines) > budget && headingIndex >= 0 && headingIndex < len(lines) {
		outLines := make([]string, 0, len(lines)-1)
		outProtect := make([]bool, 0, len(lines)-1)
		for i, l := range lines {
			if i == headingIndex {
				continue
			}
			outLines = append(outLines, l)
			outProtect = append(outProtect, isProtected(protect, i))
		}
		lines, protect = outLines, outProtect
	}
	return clipKeeping(lines, protect, budget)
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
