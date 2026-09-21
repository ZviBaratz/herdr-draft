package widgets

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// TestChipRow_WrapAroundNav covers the brief's wrap-around nav case, ported
// from Atrium's chiprow.go wrapIndex: Prev at the first chip wraps to the
// last, and Next past the last chip wraps back to the first.
func TestChipRow_WrapAroundNav(t *testing.T) {
	c := NewChipRow(testPalette())
	c.SetChips([]Chip{{ID: "a"}, {ID: "b"}, {ID: "c"}})

	c.Prev()
	if got := c.Selected().ID; got != "c" {
		t.Fatalf("Selected().ID after Prev() from the first chip = %q, want %q (wrap to the last chip)", got, "c")
	}

	c.Next()
	if got := c.Selected().ID; got != "a" {
		t.Fatalf("Selected().ID after Next() = %q, want %q (wrap back to the first chip)", got, "a")
	}

	c.Next()
	c.Next()
	c.Next() // b -> c -> wrap to a
	if got := c.Selected().ID; got != "a" {
		t.Fatalf("Selected().ID after wrapping past the last chip = %q, want %q", got, "a")
	}
}

// TestChipRow_InertRendersPlaceholderAndRefusesNav covers the brief's inert
// mode: View must render only the placeholder (no chip labels), and
// Next/Prev must not move the cursor while inert.
func TestChipRow_InertRendersPlaceholderAndRefusesNav(t *testing.T) {
	c := NewChipRow(testPalette())
	c.SetChips([]Chip{{ID: "a", Label: "Apple"}, {ID: "b", Label: "Banana"}})
	c.Next() // cursor -> b

	c.SetInert(true, "not applicable")

	view := c.View(40)
	if !strings.Contains(view, "not applicable") {
		t.Errorf("View(40) = %q, want it to contain the inert placeholder", view)
	}
	if strings.Contains(view, "Apple") || strings.Contains(view, "Banana") {
		t.Errorf("View(40) = %q, an inert row must not render its chips", view)
	}

	c.Next()
	c.Prev()
	if got := c.Selected().ID; got != "b" {
		t.Errorf("Selected().ID after Next/Prev while inert = %q, want %q (nav refused)", got, "b")
	}

	c.SetInert(false, "")
	view = c.View(40)
	if !strings.Contains(view, "Apple") {
		t.Errorf("View(40) after clearing inert = %q, want the chips back", view)
	}
}

// TestChipRow_FocusHintRendersForSelectedChip covers the brief's
// generalized focused-hint mechanism (chiprow.go:107-138 in Atrium): the
// FocusHint of the chip under the cursor is shown, and it disappears once
// the cursor moves off that chip.
func TestChipRow_FocusHintRendersForSelectedChip(t *testing.T) {
	c := NewChipRow(testPalette())
	c.SetChips([]Chip{
		{ID: "a", Label: "A", FocusHint: "hint for a"},
		{ID: "b", Label: "B"},
	})

	if view := c.View(40); !strings.Contains(view, "hint for a") {
		t.Errorf("View(40) = %q, want the selected chip's FocusHint rendered", view)
	}

	c.Next() // -> b, no FocusHint
	if view := c.View(40); strings.Contains(view, "hint for a") {
		t.Errorf("View(40) = %q, want no leftover hint once the selection moves off the chip that had one", view)
	}
}

// TestChipRow_ViewTruncatesOverflowingRowInsteadOfWrapping is the
// controller-ruled regression test for the Critical finding: a chip label
// far wider than the given width must clip onto exactly one line, exactly
// width cells wide -- not word-wrap onto several (see widthStyle's doc
// comment in picker.go for why Inline(true) is required).
func TestChipRow_ViewTruncatesOverflowingRowInsteadOfWrapping(t *testing.T) {
	c := NewChipRow(testPalette())
	c.SetChips([]Chip{{ID: "a", Label: "a chip label far longer than the ten-cell width given to View"}})

	view := c.View(10)

	if got := strings.Count(view, "\n"); got != 0 {
		t.Fatalf("View(10) has %d newlines, want 0 (exactly 1 line) -- overflow must truncate, not wrap:\n%q", got, view)
	}
	if got := lipgloss.Width(view); got != 10 {
		t.Errorf("View(10) rendered width = %d, want exactly 10", got)
	}
}

// TestChipRow_ViewHintLineAlsoTruncatesInsteadOfWrapping extends the
// truncation guard to the FocusHint line: a long hint must clip onto its
// own single line rather than wrapping the row into many, and the whole
// view must still be exactly two lines (chip row + hint row).
func TestChipRow_ViewHintLineAlsoTruncatesInsteadOfWrapping(t *testing.T) {
	c := NewChipRow(testPalette())
	c.SetChips([]Chip{{ID: "a", Label: "A", FocusHint: "this focus hint is far longer than the given width for sure"}})

	view := c.View(10)
	lines := strings.Split(view, "\n")
	if len(lines) != 2 {
		t.Fatalf("View(10) produced %d lines, want exactly 2 (chip row + hint row):\n%q", len(lines), view)
	}
	for i, line := range lines {
		if w := lipgloss.Width(line); w != 10 {
			t.Errorf("line %d width = %d, want 10: %q", i, w, line)
		}
	}
}

// TestChipRow_SelectedOnEmptyChipsDoesNotPanic guards the "no panics"
// requirement: an empty ChipRow must behave, not crash, under
// Selected/Next/Prev/View.
func TestChipRow_SelectedOnEmptyChipsDoesNotPanic(t *testing.T) {
	c := NewChipRow(testPalette())
	if got := (c.Selected()); got != (Chip{}) {
		t.Errorf("Selected() on an empty ChipRow = %+v, want the zero Chip", got)
	}
	c.Next()
	c.Prev()
	_ = c.View(10)
	_ = c.View(0)
	_ = c.View(-3)
}

// modelChips is the options row's model line WITHOUT its trailing free-text
// `other` chip -- five chips, 41 cells in full. The row #300 was measured
// on ships with six (OptionsField.newLine appends `other` to any free-text
// line); five keeps the hand-worked widths below short, and `other` adds
// nothing a sixth ordinary chip would not. At 36 columns that line read
// `inherit · fable · opu` whatever the cursor was on.
func modelChips() []Chip {
	return []Chip{
		{ID: "inherit", Label: "inherit"},
		{ID: "fable", Label: "fable"},
		{ID: "opus", Label: "opus"},
		{ID: "sonnet", Label: "sonnet"},
		{ID: "haiku", Label: "haiku"},
	}
}

// chipRowAt renders modelChips with the cursor on chip i at width w, and
// returns the one line stripped of style.
func chipRowAt(t *testing.T, i, w int) string {
	t.Helper()
	c := NewChipRow(testPalette())
	c.SetChips(modelChips())
	for n := 0; n < i; n++ {
		c.Next()
	}
	return ansi.Strip(c.View(w))
}

// TestChipRow_TheCursorChipIsAlwaysOnScreen is #300. A chip row is a
// CHOOSER, and at 36 columns the options panel's model line stayed
// byte-identical while → walked the value from fable to opus to sonnet:
// from opus onward the chip under the cursor was past the right edge, so
// the control being operated showed neither the cursor nor that there was
// anything beyond `opu`.
//
// So the row scrolls, far enough to show the cursor chip WHOLE. Run at
// every cursor position and every width from the cursor chip's own -- its
// label and leading pad, which is the least that can show it -- up past
// the row's full 41. The bottom of that range is chipWindow's last
// branch, where the cut markers yield to the chip rather than pushing it
// off the edge.
//
// "Whole" is read off the "·"-separated pieces, not as the padded
// " label " the row draws: the row's last cell is a padding space, which
// may be clipped, so the chip at the very end of a row that just fits
// has no trailing space and is still whole.
func TestChipRow_TheCursorChipIsAlwaysOnScreen(t *testing.T) {
	for i, chip := range modelChips() {
		for w := lipgloss.Width(" " + chip.Label); w <= 45; w++ {
			got := chipRowAt(t, i, w)
			if !chipsOn(got)[chip.Label] {
				t.Errorf("cursor on %q at width %d: the row is %q, want the cursor chip on screen whole", chip.Label, w, got)
			}
			if lipgloss.Width(got) != w {
				t.Errorf("cursor on %q at width %d: the row is %d cells, want exactly %d", chip.Label, w, lipgloss.Width(got), w)
			}
		}
	}
}

// TestChipRow_ACutIsMarkedAndNoChipIsCutInHalf pins the other half of
// #300: `inherit · fable · opu` loses the rest of the row with nothing to
// say it did, and cuts a chip mid-word on the way.
//
// sizes.go's file doc lets a HINT line clip silently, because running out
// of room there costs a suggestion the reader can live without, and makes
// a VALUE cell elide with a marker, because a value that loses an end is
// misread. A chooser is neither, and its tail is the list of things the
// user can pick -- so a cut is marked, with a `…` standing where the
// hidden chips would be, and a chip is either on screen whole or not at
// all.
func TestChipRow_ACutIsMarkedAndNoChipIsCutInHalf(t *testing.T) {
	whole := map[string]bool{"…": true}
	for _, c := range modelChips() {
		whole[c.Label] = true
	}
	// From 15, where every cursor position has room for its chip and every
	// marker it is owed: sonnet, cut on both sides, is the widest such
	// case at 8 + 4 + 4 - 1. Below that the markers yield (see
	// TestChipRow_BelowTheFloorTheMarkersYieldToTheChip) and "a cut is
	// always marked" is deliberately no longer promised.
	for i := range modelChips() {
		for w := 15; w < 40; w++ {
			got := chipRowAt(t, i, w)
			for _, piece := range strings.Split(got, "·") {
				if p := strings.TrimSpace(piece); p != "" && !whole[p] {
					t.Errorf("cursor on chip %d at width %d: the row is %q, and %q is a chip cut in half", i, w, got, p)
				}
			}
			if !strings.Contains(got, "…") {
				t.Errorf("cursor on chip %d at width %d: the row is %q, which does not fit in full and says nothing about what it dropped", i, w, got)
			}
		}
	}
}

// TestChipRow_ARowThatFitsIsUnchanged is what keeps every golden frame at
// an ordinary width byte-identical: the scroll only exists below the width
// the whole row needs, and a row with room to spare carries no marker.
//
// "Fits" starts at 40, not at the 41 cells the row draws, because the last
// of those is haiku's trailing padding: clipping it loses no glyph, so at
// 40 the row is still every chip, whole, in order.
func TestChipRow_ARowThatFitsIsUnchanged(t *testing.T) {
	for i := range modelChips() {
		for w := 40; w <= 60; w++ {
			got := chipRowAt(t, i, w)
			if strings.Contains(got, "…") {
				t.Errorf("cursor on chip %d at width %d: the row is %q, want no cut marker on a row that fits", i, w, got)
			}
			if want := " inherit · fable · opus · sonnet · haiku"; !strings.HasPrefix(got, want) {
				t.Errorf("cursor on chip %d at width %d: the row is %q, want it to start %q", i, w, got, want)
			}
		}
	}
}

// chipsOn is the set of whole things on a rendered row: each
// "·"-separated piece, trimmed. A chip cut in half shows up here as a
// fragment that matches no label, which is what lets a test ask "is this
// chip on screen" without caring whether its padding survived.
func chipsOn(row string) map[string]bool {
	on := map[string]bool{}
	for _, piece := range strings.Split(row, "·") {
		if p := strings.TrimSpace(piece); p != "" {
			on[p] = true
		}
	}
	return on
}

// TestChipRow_AScrolledRowShowsAsMuchAsFits pins the two promises
// chipWindow's doc makes beyond "the cursor is on screen": a scrolled row
// shows as much of its START as it can, and then as many chips as still
// fit. Neither of the property tests above can tell a generous window from
// a stingy one -- a row showing only the cursor chip between two markers
// satisfies both -- so these are worked by hand, not by calling the code.
//
// modelChips draw 9, 7, 6, 8 and 7 cells (" inherit " … " haiku "), a
// separator is 1, a cut marker is " … " plus its separator (4), and the
// row's final padding space costs nothing. At 30 cells:
//
//	cursor on inherit: no left cut. inherit·fable·opus + right cut is
//	9+7+6+2+4-1 = 27, and adding sonnet makes it 36. So three chips.
//	cursor on haiku: no right cut. From opus it is 6+8+7+2+4-1 = 26;
//	from fable, 34. So the window starts at opus.
func TestChipRow_AScrolledRowShowsAsMuchAsFits(t *testing.T) {
	for _, tc := range []struct {
		cursor, width int
		want          string
	}{
		{0, 30, "inherit · fable · opus · …"},
		{4, 30, "… · opus · sonnet · haiku"},
		// The boundary itself, which neither case above is near:
		// inherit·fable·opus plus a right cut is exactly 27, so it fits
		// at 27 and does not at 26. A window that stops one chip short
		// of what fits wastes a chip exactly as the pad miscount did.
		{0, 27, "inherit · fable · opus · …"},
		{0, 26, "inherit · fable · …"},
	} {
		got := strings.TrimSpace(chipRowAt(t, tc.cursor, tc.width))
		if got != tc.want {
			t.Errorf("cursor on chip %d at width %d: the row is %q, want %q", tc.cursor, tc.width, got, tc.want)
		}
	}
}

// modeChips is a permission-mode line, whose `accept-edits` is the widest
// chip in the form (14 cells drawn): the one #303's review found cut in
// half below the floor, reading `… · accept-edit` at 30 columns.
func modeChips() []Chip {
	return []Chip{
		{ID: "inherit", Label: "inherit"},
		{ID: "manual", Label: "manual"},
		{ID: "plan", Label: "plan"},
		{ID: "accept-edits", Label: "accept-edits"},
		{ID: "bypass", Label: "bypass"},
	}
}

// TestChipRow_BelowTheFloorTheMarkersYieldToTheChip pins chipWindow's last
// branch. Below the width the cursor chip needs with every cut marker it
// is owed, the first version still drew both markers, and the left one
// pushed the chip off the edge: the defect #300 exists to remove, back
// again one regime down. The markers now go first -- right, then left --
// so the chip is whole down to its own width.
//
// With the cursor on accept-edits (index 3, cut on both sides) the widths
// are worked by hand: the chip needs 13 cells without its final pad, a
// cut marker 4, so both markers need 21, the left one alone 17, and none
// 13.
func TestChipRow_BelowTheFloorTheMarkersYieldToTheChip(t *testing.T) {
	render := func(w int) string {
		c := NewChipRow(testPalette())
		c.SetChips(modeChips())
		for n := 0; n < 3; n++ {
			c.Next()
		}
		return ansi.Strip(c.View(w))
	}
	for _, tc := range []struct {
		from, to int
		want     string
	}{
		{21, 21, "… · accept-edits · …"},
		{17, 20, "… · accept-edits"},
		{13, 16, "accept-edits"},
	} {
		for w := tc.from; w <= tc.to; w++ {
			if got := strings.TrimSpace(render(w)); got != tc.want {
				t.Errorf("cursor on accept-edits at width %d: the row is %q, want %q", w, got, tc.want)
			}
		}
	}
}
