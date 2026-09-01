package widgets

import (
	"strings"
	"testing"

	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

func testPalette() theme.Palette { return theme.Default() }

// TestPicker_FilterNarrows covers the brief's "filter narrows" case: SetQuery
// must shrink the visible/navigable set to items whose label matches, in
// input order, without touching the underlying item set SetItems stored.
func TestPicker_FilterNarrows(t *testing.T) {
	p := NewPicker(testPalette())
	p.SetItems(1, []PickerItem{
		{ID: "1", Label: "Alpha"},
		{ID: "2", Label: "Beta"},
		{ID: "3", Label: "Alphabet"},
	})

	p.SetQuery("alpha")

	if len(p.filtered) != 2 {
		t.Fatalf("filtered has %d items, want 2 (Alpha, Alphabet); got %+v", len(p.filtered), p.filtered)
	}
	got, ok := p.Selected()
	if !ok {
		t.Fatalf("Selected() ok = false, want a match for query %q", "alpha")
	}
	if got.ID != "1" {
		t.Errorf("Selected().ID = %q, want %q (first narrowed match, cursor reset to top)", got.ID, "1")
	}

	p.SetQuery("")
	if len(p.filtered) != 3 {
		t.Errorf("filtered after clearing the query has %d items, want all 3 back", len(p.filtered))
	}
}

// TestPicker_CursorSurvivesSameVersionRefresh covers the brief's versioned
// SetItems contract: a call at the *same* version as the last accepted one
// (e.g. a refreshed fetch for the query already on screen) must not reset
// the cursor back to the top the way a filter edit or a genuinely new
// version does.
func TestPicker_CursorSurvivesSameVersionRefresh(t *testing.T) {
	p := NewPicker(testPalette())
	p.SetItems(1, []PickerItem{
		{ID: "1", Label: "A"},
		{ID: "2", Label: "B"},
		{ID: "3", Label: "C"},
	})
	p.CursorNext() // cursor -> row 1 ("B")

	// Same version 1, refreshed content.
	p.SetItems(1, []PickerItem{
		{ID: "1", Label: "A2"},
		{ID: "2", Label: "B2"},
		{ID: "3", Label: "C2"},
	})

	got, ok := p.Selected()
	if !ok || got.ID != "2" {
		t.Fatalf("Selected() = %+v, ok=%v, want row 2 (cursor preserved across a same-version refresh)", got, ok)
	}
}

// TestPicker_StaleVersionIgnored covers the brief's core versioned-filter
// requirement, ported in spirit from Atrium's picker.go stale-result guard:
// a SetItems call whose version is lower than the highest version already
// seen must be dropped outright, leaving the newer items and cursor intact.
func TestPicker_StaleVersionIgnored(t *testing.T) {
	p := NewPicker(testPalette())
	p.SetItems(2, []PickerItem{{ID: "fresh", Label: "Fresh"}})

	p.SetItems(1, []PickerItem{{ID: "stale", Label: "Stale"}})

	got, ok := p.Selected()
	if !ok || got.ID != "fresh" {
		t.Fatalf("Selected() = %+v, ok=%v, want the version-2 item unchanged by the stale version-1 call", got, ok)
	}
	if len(p.filtered) != 1 {
		t.Fatalf("filtered has %d items, want 1 (the stale call must not append or replace)", len(p.filtered))
	}
}

// TestPicker_EmptyResultPlaceholder covers the brief's empty-result state:
// when the query matches nothing, Selected reports no selection and View
// renders a placeholder row instead of leaving the list visually blank.
func TestPicker_EmptyResultPlaceholder(t *testing.T) {
	p := NewPicker(testPalette())
	p.SetItems(1, []PickerItem{{ID: "1", Label: "Alpha"}})
	p.SetQuery("no-such-item")

	if _, ok := p.Selected(); ok {
		t.Fatalf("Selected() ok = true with an empty filtered set, want false")
	}

	view := p.View(24, 3)
	if !strings.Contains(view, pickerEmptyPlaceholder) {
		t.Errorf("View(24,3) = %q, want it to contain the empty-result placeholder %q", view, pickerEmptyPlaceholder)
	}
	if got := strings.Count(view, "\n"); got != 2 {
		t.Errorf("View(24,3) has %d newlines, want 2 (a 3-row view)", got)
	}
}

// TestPicker_ViewDoesNotPanicOnDegenerateDimensions guards the "no panics"
// requirement for boundary width/height values a caller could pass while a
// popup is being resized.
func TestPicker_ViewDoesNotPanicOnDegenerateDimensions(t *testing.T) {
	p := NewPicker(testPalette())
	p.SetItems(1, []PickerItem{{ID: "1", Label: "Alpha"}})
	for _, dims := range [][2]int{{0, 0}, {-5, -5}, {0, 5}, {5, 0}, {5, -1}} {
		_ = p.View(dims[0], dims[1])
	}
}

// TestPicker_CursorNextPrevClampWithoutWrapping pins that Picker navigation
// (unlike ChipRow's) clamps at both ends rather than wrapping, matching
// Atrium's handleKey KeyUp/KeyDown branches (picker.go).
func TestPicker_CursorNextPrevClampWithoutWrapping(t *testing.T) {
	p := NewPicker(testPalette())
	p.SetItems(1, []PickerItem{{ID: "1"}, {ID: "2"}})

	p.CursorPrev() // already at 0, must stay
	if got, _ := p.Selected(); got.ID != "1" {
		t.Fatalf("Selected().ID after CursorPrev() at row 0 = %q, want %q (clamp, not wrap)", got.ID, "1")
	}

	p.CursorNext()
	p.CursorNext() // already at the last row, must stay
	if got, _ := p.Selected(); got.ID != "2" {
		t.Fatalf("Selected().ID after CursorNext() past the last row = %q, want %q (clamp, not wrap)", got.ID, "2")
	}
}
