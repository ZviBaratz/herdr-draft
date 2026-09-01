// Derived from atrium (github.com/ZviBaratz/atrium) ui/overlay/chiprow.go,
// © Zvi Baratz, relicensed by the author.
//
// Adaptations from the source: Atrium's chipRow is a plain []string of
// options bound to a domain-specific convention -- index 0 is always the
// no-op "inherit" choice, so selected() returns "" for it -- and its
// focused-chip hint (noOverrideHint) is business logic about where a claude
// flag's effective value comes from across selected programs/profiles.
// herdr-draft's ChipRow generalizes both away, per the task-14 brief:
//
//   - Chips are (ID, Label, FocusHint) values with no reserved index;
//     Selected returns whatever chip the cursor is on, full stop.
//   - FocusHint is plain data the caller sets per chip; View displays the
//     FocusHint of the chip under the cursor when it is non-empty. There is
//     no override/pin concept here -- Atrium's noOverrideHint (chiprow.go
//     :107-138) is not ported, only the idea it exists to serve
//     (a focused chip can carry an explanatory hint) generalizes.
//   - SetDisabled/Disabled become SetInert(inert, placeholder): inert mode
//     now carries its own placeholder text and (per the brief) also refuses
//     Next/Prev, not just contributing "" to Selected.
//
// What carries over near-verbatim: the wraparound cursor arithmetic
// (wrapIndex) and the row styling loop from chipRow.render -- iterate
// chips, pad each label with a leading/trailing space, join with a dim "·"
// separator -- with Atrium's internal ppSelectedStyle/mfDimStyle helpers
// (defined outside the audited clean file set) replaced by styles built
// from an injected theme.Palette. Atrium's three-way styling (cursor+focused
// highlighted / cursor-alone plain / everything else dim) collapses to two
// -way (cursor highlighted / everything else dim) because this ChipRow has
// no Focus/Blur of its own -- see the widgets package doc.
package widgets

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// Chip is one option in a ChipRow.
type Chip struct {
	ID        string
	Label     string
	FocusHint string
}

// ChipRow is a horizontal single-select: a row of chips with a wrapping
// cursor, plus an inert mode that renders a placeholder in place of the
// chips and refuses navigation (e.g. while the field it drives doesn't
// apply to the current selection).
type ChipRow struct {
	palette theme.Palette

	chips  []Chip
	cursor int

	inert       bool
	placeholder string
}

// NewChipRow returns an empty ChipRow rendered with palette.
func NewChipRow(palette theme.Palette) *ChipRow {
	return &ChipRow{palette: palette}
}

// SetChips replaces the row's chips and resets the cursor to the first
// chip.
func (c *ChipRow) SetChips(chips []Chip) {
	c.chips = chips
	c.cursor = 0
}

// Next moves the cursor to the next chip, wrapping past the last chip back
// to the first -- ported from Atrium's wrapIndex/moveCursor. A no-op while
// inert.
func (c *ChipRow) Next() {
	if c.inert {
		return
	}
	c.cursor = wrapIndex(c.cursor, 1, len(c.chips))
}

// Prev moves the cursor to the previous chip, wrapping past the first chip
// back to the last -- ported from Atrium's wrapIndex/moveCursor. A no-op
// while inert.
func (c *ChipRow) Prev() {
	if c.inert {
		return
	}
	c.cursor = wrapIndex(c.cursor, -1, len(c.chips))
}

// wrapIndex moves cur by delta within [0,n), wrapping at both ends --
// ported verbatim from Atrium's chiprow.go wrapIndex. A non-positive n (no
// chips) returns 0, keeping callers panic-free since "% 0" would panic
// where the old clamp checks were silently safe (comment preserved from the
// source: the same hazard applies here).
func wrapIndex(cur, delta, n int) int {
	if n <= 0 {
		return 0
	}
	return ((cur+delta)%n + n) % n
}

// SetInert toggles the row's inert mode. While inert, View renders
// placeholder in place of the chips and Next/Prev refuse to move the
// cursor. Passing inert=false clears placeholder's effect (View goes back
// to rendering the chips) whether or not placeholder is also cleared.
func (c *ChipRow) SetInert(inert bool, placeholder string) {
	c.inert = inert
	c.placeholder = placeholder
}

// Selected returns the chip under the cursor, or the zero Chip when the row
// has no chips.
func (c *ChipRow) Selected() Chip {
	if c.cursor < 0 || c.cursor >= len(c.chips) {
		return Chip{}
	}
	return c.chips[c.cursor]
}

// SelectID moves the cursor directly to the chip whose ID matches id --
// task 21's mouse-click counterpart to Next/Prev, e.g. via SelectAt
// below. A no-op (returns false), including while inert (Next/Prev's own
// contract), when no chip matches id -- mirroring widgets.Picker.SelectID's
// identical "leaves the cursor unchanged" contract for an unresolvable
// target.
func (c *ChipRow) SelectID(id string) bool {
	if c.inert {
		return false
	}
	for i, chip := range c.chips {
		if chip.ID == id {
			c.cursor = i
			return true
		}
	}
	return false
}

// SelectAt attempts to move the cursor to whichever of this row's own
// chips msg's coordinates land on, via that chip's own
// zonePrefix+chip.ID zone (task 21's "chip:<sectionID>:<chipID>" scheme)
// registered by the most recent MarkedView(width, zonePrefix) call,
// returning the matched chip and true on a hit. zonePrefix must be the
// SAME prefix passed to that MarkedView call, or the lookup will never
// match anything. A no-op (returns the zero Chip and false), including
// while inert (SelectID's own contract), when msg does not land on any
// of this row's zones.
func (c *ChipRow) SelectAt(msg tea.MouseMsg, zonePrefix string) (Chip, bool) {
	for _, chip := range c.chips {
		if Zones.Get(zonePrefix + chip.ID).InBounds(msg) {
			if c.SelectID(chip.ID) {
				return chip, true
			}
			return Chip{}, false
		}
	}
	return Chip{}, false
}

// View renders the chip row into width cells (space-padded, clipped on
// overflow). width <= 0 renders "" rather than panicking or leaving content
// unclipped (see widthStyle in picker.go). It is MarkedView with an empty
// zone prefix (Zones.Mark's own empty-id no-op, see its doc comment) --
// the zero-dependency rendering path every existing widget-level test and
// every raw (non-field-Section) golden-frame fixture in this package
// continues to use unmodified.
func (c *ChipRow) View(width int) string {
	return c.MarkedView(width, "")
}

// MarkedView renders exactly like View, additionally wrapping each
// chip's own rendered span in a bubblezone/v2 zone marker ID'd
// zonePrefix+chip.ID (task 21's "chip:<sectionID>:<chipID>" scheme) via
// this package's shared Zones manager (zones.go), so a caller's own
// mouse-click handling can later resolve a click back to a specific chip
// via SelectAt above. zonePrefix == "" marks nothing at all (Zones.Mark's
// own empty-id no-op) -- see View's own doc comment.
//
// While inert, MarkedView renders only placeholder, dimmed, and nothing
// else -- no chips exist to mark. Otherwise it renders the chips
// separated by a dim "·" with the cursor chip highlighted, followed -- on
// a second line, only when non-empty -- by the FocusHint of the chip
// under the cursor: the generalized focused-hint mechanism ported from
// Atrium's chiprow.go:107-138 (see the file header).
func (c *ChipRow) MarkedView(width int, zonePrefix string) string {
	if width <= 0 {
		return ""
	}
	rowStyle := widthStyle(width)

	if c.inert {
		placeholderStyle := lipgloss.NewStyle().Foreground(c.palette.DimText).Italic(true)
		return rowStyle.Render(placeholderStyle.Render(c.placeholder))
	}

	dim := lipgloss.NewStyle().Foreground(c.palette.DimText)
	plain := lipgloss.NewStyle().Foreground(c.palette.Text)
	active := lipgloss.NewStyle().Foreground(c.palette.Accent).Bold(true)

	var row strings.Builder
	for i, chip := range c.chips {
		label := " " + chip.Label + " "
		var rendered string
		if i == c.cursor {
			rendered = active.Render(label)
		} else {
			rendered = plain.Render(label)
		}
		zoneID := ""
		if zonePrefix != "" {
			zoneID = zonePrefix + chip.ID
		}
		row.WriteString(Zones.Mark(zoneID, rendered))
		if i < len(c.chips)-1 {
			row.WriteString(dim.Render("·"))
		}
	}
	line := rowStyle.Render(row.String())

	if hint := c.Selected().FocusHint; hint != "" {
		hintStyle := dim.Italic(true)
		return line + "\n" + rowStyle.Render(hintStyle.Render(hint))
	}
	return line
}
