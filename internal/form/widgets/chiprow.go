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
	ID    string
	Label string
	// Badge is drawn dim straight after Label, inside the chip's padding,
	// on the cursor chip as on every other: a mark about the chip rather
	// than part of its name -- the options panel's `•` on a value
	// [agents.extra_args] supplies. It is part of the chip everywhere the
	// row measures one, so the window, the cut markers and the click zone
	// all count it.
	Badge     string
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

// SetPalette stores a new palette for the next render (host-colours spec
// §7.3, #347). This widget derives every style from c.palette inside
// View, so storing the new value is the whole of the repaint -- unlike
// widgets.PromptArea, whose styles are baked into its textarea once and
// have to be rebuilt. Nothing here is cached, so there is nothing to
// invalidate.
func (c *ChipRow) SetPalette(palette theme.Palette) { c.palette = palette }

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

// View renders the chip row into width cells, space-padded. A row too wide
// for width SCROLLS rather than clipping: see chipWindow (#300). width <= 0
// renders "" rather than panicking or leaving content unclipped (see
// widthStyle in picker.go). It is MarkedView with an empty
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
//
// Only the chips chipWindow puts on screen are drawn, so only they are
// marked. A chip scrolled out of view therefore has no zone, and one that
// WAS on screen last render loses its zone too: bubblezone drops every
// zone a Scan did not see (manager.go's zoneWorker at v2.0.0), so a click
// where a hidden chip used to be selects nothing.
//
// The FocusHint line is a HINT in sizes.go's sense and still clips
// silently; only the chip line scrolls.
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

	widths := make([]int, len(c.chips))
	for i, chip := range c.chips {
		widths[i] = lipgloss.Width(chipLabel(chip))
	}
	lo, hi, leftCut, rightCut := chipWindow(widths, c.cursor, width)

	var row strings.Builder
	if leftCut {
		row.WriteString(dim.Render(chipCut))
		row.WriteString(dim.Render("·"))
	}
	for i := lo; i <= hi; i++ {
		chip := c.chips[i]
		face := plain
		if i == c.cursor {
			face = active
		}
		rendered := renderChip(chip, face, dim)
		zoneID := ""
		if zonePrefix != "" {
			zoneID = zonePrefix + chip.ID
		}
		row.WriteString(Zones.Mark(zoneID, rendered))
		if i < hi {
			row.WriteString(dim.Render("·"))
		}
	}
	if rightCut {
		row.WriteString(dim.Render("·"))
		row.WriteString(dim.Render(chipCut))
	}
	line := rowStyle.Render(row.String())

	if hint := c.Selected().FocusHint; hint != "" {
		hintStyle := dim.Italic(true)
		return line + "\n" + rowStyle.Render(hintStyle.Render(hint))
	}
	return line
}

// chipLabel is a chip as the row draws it: its label and badge padded by
// one space each side, which is what the "·" separator sits between. It is
// what every width the row computes is measured on, so a badge can never
// be drawn in a cell the window did not budget for.
func chipLabel(chip Chip) string {
	return " " + chip.Label + chip.Badge + " "
}

// renderChip draws chipLabel's text: the label and its padding in face,
// the badge between them dim. A chip with no badge is face over the whole
// of chipLabel, byte for byte what it was before badges existed, so no
// frame of an unbadged row moves.
func renderChip(chip Chip, face, dim lipgloss.Style) string {
	if chip.Badge == "" {
		return face.Render(chipLabel(chip))
	}
	return face.Render(" "+chip.Label) + dim.Render(chip.Badge) + face.Render(" ")
}

// chipCut stands where the chips a scrolled row does not show would be:
// Ellipsis padded exactly like a chip, so a cut end reads as one more slot
// in the row rather than as a label that lost its tail.
var chipCut = " " + Ellipsis + " "

// chipWindow picks the chips a row of the given rendered widths shows at
// width, first and last inclusive, with the cursor chip always among them
// (#300).
//
// A chip row is a CHOOSER, which is the case internal/form's sizes.go file
// doc does not name. That doc lets a HINT line clip silently, because
// running out of room there costs a suggestion the reader can live
// without, and makes a VALUE cell elide with a marker, because a value
// that loses an end is misread. A chooser's tail is neither: it is the
// list of things the user can pick, and it holds the cursor. Hard-clipped
// the way hints are, the options panel's model line read `inherit · fable
// · opu` at 36 columns whatever the cursor was on, so from opus onward
// the user was changing a value with the control they were operating
// showing no cursor at all.
//
// So a row that fits is drawn whole and unchanged -- every golden frame
// at an ordinary width is byte-identical -- and a row that does not
// SCROLLS: it shows as much of its start as it can while still showing the
// cursor chip whole, marks each cut end with chipCut, and never draws a
// chip in half.
//
// It is stateless on purpose. The window is a pure function of the
// widths, the cursor and the width, so there is no scroll offset to keep
// in step with SetChips, SelectID or a resize, and nothing a render at one
// width can leave behind for a render at another. The price is that
// moving LEFT from the far end snaps back to the start as soon as the
// start fits again, rather than easing back a chip at a time; the longest
// rows in the form are six chips, so that is a jump of a few at most, and
// a predictable one. A click can re-window the row under the pointer for
// the same reason: the window follows the cursor wherever it lands.
//
// leftCut and rightCut say which ends carry a marker, and they are not
// simply lo > 0 and hi < n-1. Below the width the cursor chip needs with
// every marker it is owed, the markers go BEFORE the chip does: first the
// right one, then the left, because a marker that pushes the cursor chip
// off the edge brings back the very defect this exists to remove -- the
// first version drew both regardless, and at 30 columns the options
// panel's mode line read `… · accept-edit`. So down to the cursor chip's
// own width the chip is whole and a cut may go unmarked; below that there
// is no honest answer left and widthStyle's hard clip is the backstop, as
// sizes.go says it is for any composed line.
func chipWindow(widths []int, cursor, width int) (lo, hi int, leftCut, rightCut bool) {
	n := len(widths)
	if n == 0 {
		return 0, -1, false, false
	}
	cutWidth := lipgloss.Width(chipCut) + 1 // the marker and its separator
	// cost is what showing chips lo..hi needs, which is one cell LESS
	// than they draw: whatever ends the row -- the last chip or the cut
	// marker -- ends in its padding space, and clipping that loses no
	// glyph. Counting it made a row that fits with a cell to spare look
	// one cell too wide, and placement-floor-37x12 traded its whole
	// `split here` chip for a marker to save a space nobody could see.
	// That rests on a chip having no background of its own -- the cursor
	// chip is foreground and bold only -- so a clipped pad hides nothing.
	cost := func(lo, hi int) int {
		total := hi - lo - 1 // separators between the chips shown, less the final pad
		for i := lo; i <= hi; i++ {
			total += widths[i]
		}
		if lo > 0 {
			total += cutWidth
		}
		if hi < n-1 {
			total += cutWidth
		}
		return total
	}
	if cost(0, n-1) <= width {
		return 0, n - 1, false, false
	}
	lo = 0
	for lo < cursor && cost(lo, cursor) > width {
		lo++
	}
	hi = cursor
	for hi+1 < n && cost(lo, hi+1) <= width {
		hi++
	}
	leftCut, rightCut = lo > 0, hi < n-1
	if cost(lo, hi) <= width {
		return lo, hi, leftCut, rightCut
	}
	// No window fits with its markers: lo == hi == cursor here, and the
	// markers yield to the chip, right one first.
	alone := widths[cursor] - 1
	if rightCut && alone+cutWidth+boolWidth(leftCut, cutWidth) > width {
		rightCut = false
	}
	if leftCut && alone+cutWidth > width {
		leftCut = false
	}
	return lo, hi, leftCut, rightCut
}

// boolWidth is w when on, else nothing.
func boolWidth(on bool, w int) int {
	if on {
		return w
	}
	return 0
}
