// rowvalues.go holds the rendering helpers Section's Label/Row/Panel/
// PanelRows share across field_*.go -- the elision rule for a one-line
// value cell, and the two-cell gutter every panel line is composed
// into.
//
// It is the row-stack counterpart of layout.go's width-and-height
// primitives, kept in its own file because the two are used at different
// levels: layout.go fits a line or a block to a size, rowvalues.go decides
// what a row or a panel line SAYS at that size.
//
// Written fresh for v2; nothing here is derived from atrium
// (github.com/ZviBaratz/atrium), whose own overlay has neither a
// one-line-per-field row stack nor a single shared panel region.
package form

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ZviBaratz/herdr-draft/internal/form/widgets"
	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// rowEllipsis marks an elided row value. sizes.go's own file doc records
// the v1 decision this reverses and why: v1 clipped silently because
// running out of room on a HINT line costs the reader a suggestion they
// can live without, while a one-line VALUE cell that loses an end
// unmarked is not incomplete but MISREAD -- "~/Projects/herdr-dra" and
// "zvi/fix-login-redir" both read as real values.
const rowEllipsis = "…"

// keepHead clips s to exactly width cells KEEPING ITS HEAD, marking the
// cut with rowEllipsis: the rule for titles, branches, prose and
// identifiers, whose informative end is the one you read first (v2 spec
// §7, restated in sizes.go's file doc).
//
// width < 1 renders nothing; a width of 1 that cannot hold s renders the
// ellipsis alone, which is still an honest "there is more here than fits"
// -- the same degrade-rather-than-panic contract widgets/picker.go's
// widthStyle documents for its own degenerate dimensions.
func keepHead(s string, width int) string {
	if width < 1 {
		return ""
	}
	if ansi.StringWidth(s) <= width {
		return s
	}
	return ansi.Truncate(s, width, rowEllipsis)
}

// keepTail clips s to exactly width cells KEEPING ITS TAIL, marking the
// cut with a leading rowEllipsis: the rule for PATHS, where the last
// segments are what distinguish "~/Projects/herdr" from
// "~/Projects/herdr-draft" and the shared prefix is what everything on
// screen already has in common.
//
// ansi.TruncateLeft removes n cells and prepends the marker, so the n
// that leaves exactly width cells behind is (rendered width - width + 1),
// the +1 paying for the ellipsis itself. At width 1 that n would consume
// the whole string and TruncateLeft would emit nothing at all, so the
// degenerate widths short-circuit to the bare marker.
func keepTail(s string, width int) string {
	if width < 1 {
		return ""
	}
	w := ansi.StringWidth(s)
	if w <= width {
		return s
	}
	if width == 1 {
		return rowEllipsis
	}
	return ansi.TruncateLeft(s, w-width+1, rowEllipsis)
}

// panelCursorGlyph is the marker v2 spec §4's mockups draw beside the
// selected row of a focused panel's list. It lands in the panel's own
// two-cell gutter -- the same column the row stack indents past -- which
// is why Panel is handed gutterWidth+inner rather than the value column
// alone (form.go's rowSection.Panel).
const panelCursorGlyph = "▸"

// panelGutter returns a panel line's two-cell gutter: the accent-colored
// cursor glyph plus a space when cursor is true, two blanks otherwise.
// Always exactly gutterWidth cells wide.
func panelGutter(cursor bool, p theme.Palette) string {
	if !cursor {
		return strings.Repeat(" ", gutterWidth)
	}
	return lipgloss.NewStyle().Foreground(p.Accent).Render(panelCursorGlyph) + " "
}

// panelInner is the width left for a panel line's content once the gutter
// is paid for, floored at 1.
func panelInner(w int) int {
	if inner := w - gutterWidth; inner > 0 {
		return inner
	}
	return 1
}

// panelText composes one plain (never zone-marked) panel line: the blank
// gutter, then content fitted to the remaining width.
func panelText(content string, w int) string {
	return strings.Repeat(" ", gutterWidth) + fitLine(content, panelInner(w))
}

// panelMarked composes one panel line whose content the caller has
// ALREADY rendered to the content column's exact width (panelInner) and
// which may carry bubblezone markers -- a
// widgets.Picker or widgets.ChipRow render. It deliberately does not run
// content back through fitLine: every other zone-marked render in this
// package (field_issue.go, field_dir.go, field_agent.go,
// field_placement.go) composes marked widget output by concatenation for
// the same reason, and the frame's own paintLine normalizes the finished
// line's width anyway.
func panelMarked(content string, cursor bool, p theme.Palette) string {
	return panelGutter(cursor, p) + content
}

// panelChipRow composes one panel line whose content is a
// widgets.ChipRow render, already fitted to panelChipWidth(w).
//
// It pays for only ONE of the gutter's two cells and lets the chip row's
// own leading space stand in for the other: ChipRow renders every chip as
// " label ", so a chip row composed through panelMarked like any other
// content sits one column right of the picker rows and part labels
// beside it. One cell is a small thing to be wrong about and a very
// visible one in a panel whose whole job is an aligned column.
//
// An INERT chip row has no such padding (widgets.ChipRow renders its
// placeholder bare), so a caller in that state must use panelMarked
// instead -- see field_worktree.go's Panel, which switches on exactly
// that.
func panelChipRow(content string) string {
	return strings.Repeat(" ", gutterWidth-1) + content
}

// panelChipWidth is the width a chip row is rendered at to land in the
// column panelChipRow then places it in.
func panelChipWidth(w int) int { return panelInner(w) + 1 }

// panelPickerLines renders pk into exactly h panel lines w cells wide:
// the picker itself in the content column, its cursor row marked with
// panelCursorGlyph in the gutter. zonePrefix is passed straight through
// to widgets.Picker.MarkedView, so a click inside the panel still
// resolves to a row (form.go's zonePanel forwards it to the focused
// section).
func panelPickerLines(pk *widgets.Picker, w, h int, zonePrefix string, p theme.Palette) []string {
	if h < 1 {
		h = 1
	}
	cursor := pk.CursorRow(h)
	rendered := strings.Split(pk.MarkedView(panelInner(w), h, zonePrefix), "\n")
	lines := make([]string, h)
	for i := range lines {
		content := ""
		if i < len(rendered) {
			content = rendered[i]
		}
		lines[i] = panelMarked(content, i == cursor, p)
	}
	return lines
}

// panelBlock joins already-composed panel lines into Panel's own
// exactly-h-lines contract, padding with blank gutter rows and dropping
// any overflow from the bottom.
func panelBlock(w, h int, lines ...string) string {
	if h < 1 {
		h = 1
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, panelText("", w))
	}
	return strings.Join(lines, "\n")
}

// capRows clamps a field's PanelRows() to its own ceiling, never below 1:
// a Section that reports 0 tells the form it has no panel at all, which
// is a different statement from "a small one" (form.go's
// rowSection.PanelRows).
func capRows(want, ceiling int) int {
	if want > ceiling {
		want = ceiling
	}
	if want < 1 {
		want = 1
	}
	return want
}
