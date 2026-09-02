// layout.go holds the width-and-height primitives every rendering in this
// package is built on (see rowvalues.go for the row/panel-specific ones)
// -- written fresh, not derived from atrium
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

// fitBlock normalizes a rendered block to exactly h physical lines, width
// cells wide: extra lines are dropped from the BOTTOM and a short block is
// padded with blank rows.
//
// form.go's renderPanelRegion applies it to the focused Section's own
// Panel output, so the line count the layout booked is the line count
// actually emitted even if a Panel and its PanelRows ever disagree. h < 1
// is treated as 1.
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

// sectionLines assembles a block from an ordered list of already-rendered
// rows, most important first, truncated or padded to exactly h lines.
func sectionLines(h, width int, rows ...string) string {
	return fitBlock(strings.Join(rows, "\n"), h, width)
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
