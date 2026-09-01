// layout.go holds small rendering helpers shared by Tasks 17-18's concrete
// field Sections (field_dir.go, field_title.go, field_worktree.go,
// field_placement.go, and, per the task-18 brief, the fields that follow
// them) -- written fresh for this task, not derived from atrium
// (github.com/ZviBaratz/atrium).
package form

import (
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
