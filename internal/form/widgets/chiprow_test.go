package widgets

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
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
