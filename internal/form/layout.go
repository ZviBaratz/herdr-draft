// layout.go holds small rendering helpers shared by Tasks 17-18's concrete
// field Sections (field_dir.go, field_title.go, field_worktree.go,
// field_placement.go, and, per the task-18 brief, the fields that follow
// them) -- written fresh for this task, not derived from atrium
// (github.com/ZviBaratz/atrium).
package form

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// fitLine pads/clips s to exactly width cells on exactly one physical
// output line, floored at width 1. This is the same
// Width+MaxWidth+Inline(true) composition widgets/picker.go's own
// widthStyle documents and relies on (task 14's own hard-won fact,
// restated in this task's brief: "lipgloss v2 Style.Render WORD-WRAPS
// before MaxWidth -- every fixed-height render needs .Inline(true)");
// duplicated here rather than exported from package widgets because every
// field Section in this package composes MULTIPLE styled spans (a label,
// a typed value, an inline marker) onto one row before the row as a WHOLE
// needs clipping to inner width, not just a single already-isolated
// widget's own View output.
func fitLine(s string, width int) string {
	if width < 1 {
		width = 1
	}
	return lipgloss.NewStyle().Width(width).MaxWidth(width).Inline(true).Render(s)
}

// fitBlock normalizes a Section's rendered block to exactly h physical
// lines, width cells wide: extra lines are dropped from the BOTTOM (a
// Section renders its label/value header first, per Section.View's own
// contract, so the bottom is always its least important end), and a short
// block is padded with blank rows.
//
// form.go's compose applies this to every Section's own View output before
// composing it, so the line count it books against sizes.go's own
// allocation is the line count actually emitted even if a Section's View
// and its Height/MinHeight ever disagree. h < 1 is treated as 1: every
// Section occupies at least one row.
func fitBlock(block string, h, width int) string {
	if h < 1 {
		h = 1
	}
	lines := strings.Split(block, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, fitLine("", width))
	}
	return strings.Join(lines, "\n")
}

// sectionLines assembles a field Section's own View output from an ordered
// list of already-rendered rows, most important first, truncated or padded
// to exactly h lines -- the shared shape every concrete field in this
// package uses to honor Section.View's "shed rows from the bottom as h
// shrinks" contract.
func sectionLines(h, width int, rows ...string) string {
	return fitBlock(strings.Join(rows, "\n"), h, width)
}

// blankRows returns n blank lines, width cells wide, joined -- the
// reserved-but-empty candidate rows an unfocused picker field renders when
// the form's budget still affords them.
func blankRows(n, width int) []string {
	if n < 1 {
		return nil
	}
	out := make([]string, n)
	for i := range out {
		out[i] = fitLine("", width)
	}
	return out
}

// dimText returns a plain dim-foreground style from palette, the base for
// this package's own hint/placeholder text (mirroring
// widgets.Picker/ChipRow's own DimText usage).
func dimText(p theme.Palette) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(p.DimText)
}

// dimHint returns dimText, italicized -- this package's own convention for
// a reserved hint/placeholder line (matching widgets.ChipRow's own
// FocusHint styling and widgets.Picker's empty-list placeholder styling).
func dimHint(p theme.Palette) lipgloss.Style {
	return dimText(p).Italic(true)
}
