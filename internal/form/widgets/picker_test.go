package widgets

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

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

// TestPicker_SameVersionRefreshPreservesSelectionByIDAcrossReorder is the
// controller-ruled regression test: selection preservation across a
// same-version refresh must follow the item's ID, not its numeric position.
// Item "2" moves from row 1 to row 0 in the refreshed list; an index-based
// implementation would leave the cursor on row 1 and silently select a
// different item ("3", now at row 1) instead.
func TestPicker_SameVersionRefreshPreservesSelectionByIDAcrossReorder(t *testing.T) {
	p := NewPicker(testPalette())
	p.SetItems(1, []PickerItem{
		{ID: "1", Label: "A"},
		{ID: "2", Label: "B"},
		{ID: "3", Label: "C"},
	})
	p.CursorNext() // cursor -> row 1, selects ID "2"

	// Same version 1, refreshed AND reordered: "2" is now first, "3" now
	// sits at the row "2" used to occupy.
	p.SetItems(1, []PickerItem{
		{ID: "2", Label: "B2"},
		{ID: "3", Label: "C2"},
		{ID: "1", Label: "A2"},
	})

	got, ok := p.Selected()
	if !ok || got.ID != "2" {
		t.Fatalf("Selected() = %+v, ok=%v, want ID \"2\" to stay selected across the reorder (identity, not index)", got, ok)
	}
}

// TestPicker_SameVersionRefreshFallsBackToClampedIndexWhenSelectedIDIsGone
// covers the fallback half of the controller ruling: when the previously
// selected item's ID is no longer present after a same-version refresh
// (e.g. a candidate dropped out), the cursor falls back to its old numeric
// position clamped into the new range, rather than jumping back to the top.
func TestPicker_SameVersionRefreshFallsBackToClampedIndexWhenSelectedIDIsGone(t *testing.T) {
	p := NewPicker(testPalette())
	p.SetItems(1, []PickerItem{
		{ID: "1", Label: "A"},
		{ID: "2", Label: "B"},
		{ID: "3", Label: "C"},
	})
	p.CursorNext() // cursor -> row 1, selects ID "2"

	// Same version 1, "2" is gone; only 2 items remain, so the old row-1
	// position is still in range and should be kept (now "y").
	p.SetItems(1, []PickerItem{
		{ID: "x", Label: "X"},
		{ID: "y", Label: "Y"},
	})

	got, ok := p.Selected()
	if !ok || got.ID != "y" {
		t.Fatalf("Selected() = %+v, ok=%v, want ID \"y\" (old row 1, clamped into range) when the selected ID vanished", got, ok)
	}

	// Now the old row-1 position is out of range entirely (only 1 item
	// left): the fallback must clamp, not panic or leave a stale index.
	p.SetItems(1, []PickerItem{
		{ID: "solo", Label: "Solo"},
	})
	got, ok = p.Selected()
	if !ok || got.ID != "solo" {
		t.Fatalf("Selected() = %+v, ok=%v, want the sole remaining item (fallback clamped into range)", got, ok)
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

// TestPicker_ViewTruncatesOverflowingRowInsteadOfWrapping is the
// controller-ruled regression test for the Critical finding: lipgloss's
// Style.Render word-wraps whenever width > 0 unless Inline(true) is set, and
// that wrap step runs *before* MaxWidth truncation -- so a Style built as
// plain Width(w).MaxWidth(w) silently turns one overflowing row into many
// physical lines instead of clipping it onto one. A single row far wider
// than the given width must still produce exactly one output line, exactly
// width cells wide.
func TestPicker_ViewTruncatesOverflowingRowInsteadOfWrapping(t *testing.T) {
	p := NewPicker(testPalette())
	p.SetItems(1, []PickerItem{
		{
			ID:    "1",
			Label: "this label is far longer than the ten-cell width given to View",
			Hint:  "and this hint is also much longer than ten cells",
		},
	})

	view := p.View(10, 1)

	if got := strings.Count(view, "\n"); got != 0 {
		t.Fatalf("View(10,1) has %d newlines, want 0 (exactly 1 line) -- overflow must truncate, not wrap:\n%q", got, view)
	}
	if got := lipgloss.Width(view); got != 10 {
		t.Errorf("View(10,1) rendered width = %d, want exactly 10", got)
	}
}

// TestPicker_ViewOverflowingRowsHoldExactHeightAcrossMultipleRows extends
// the truncation guard to a multi-row view: every row must independently
// clip to width, and the total output must still be exactly height lines
// (not height-plus-however-many-words-wrapped).
func TestPicker_ViewOverflowingRowsHoldExactHeightAcrossMultipleRows(t *testing.T) {
	p := NewPicker(testPalette())
	p.SetItems(1, []PickerItem{
		{ID: "1", Label: "first label is much longer than the given width for sure"},
		{ID: "2", Label: "second label is also much longer than the given width"},
		{ID: "3", Label: "third"},
	})

	view := p.View(8, 3)
	lines := strings.Split(view, "\n")
	if len(lines) != 3 {
		t.Fatalf("View(8,3) produced %d lines, want exactly 3:\n%q", len(lines), view)
	}
	for i, line := range lines {
		if w := lipgloss.Width(line); w != 8 {
			t.Errorf("line %d width = %d, want 8: %q", i, w, line)
		}
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

// TestPicker_SelectID pins SelectID (added in Task 20b for
// AccountField.SetPin's own config-default pre-selection, spec §12's
// `[clauth] default`): it must move the cursor to the matching item
// regardless of the item's own position, report true on a match, and
// leave the cursor exactly where it was (reporting false) for an id that
// isn't present.
func TestPicker_SelectID(t *testing.T) {
	p := NewPicker(testPalette())
	p.SetItems(1, []PickerItem{{ID: "active"}, {ID: "alpha"}, {ID: "beta"}, {ID: "gamma"}})

	if !p.SelectID("gamma") {
		t.Fatalf("SelectID(%q) = false, want true", "gamma")
	}
	if got, _ := p.Selected(); got.ID != "gamma" {
		t.Fatalf("Selected().ID after SelectID(%q) = %q, want %q", "gamma", got.ID, "gamma")
	}

	if p.SelectID("does-not-exist") {
		t.Fatalf("SelectID(%q) = true, want false (not present)", "does-not-exist")
	}
	if got, _ := p.Selected(); got.ID != "gamma" {
		t.Fatalf("Selected().ID after a missed SelectID = %q, want unchanged %q", got.ID, "gamma")
	}
}

// TestPicker_FilteredLen pins the count v2's panel sizing reads: the
// items AFTER the query, not the whole set.
func TestPicker_FilteredLen(t *testing.T) {
	p := NewPicker(testPalette())
	if got := p.FilteredLen(); got != 0 {
		t.Errorf("FilteredLen() on a fresh Picker = %d, want 0", got)
	}

	p.SetItems(1, []PickerItem{
		{ID: "1", Label: "Alpha"},
		{ID: "2", Label: "Beta"},
		{ID: "3", Label: "Alphabet"},
	})
	if got := p.FilteredLen(); got != 3 {
		t.Errorf("FilteredLen() = %d, want 3", got)
	}

	p.SetQuery("alpha")
	if got := p.FilteredLen(); got != 2 {
		t.Errorf("FilteredLen() under a query = %d, want 2 (the filtered set, not the item set)", got)
	}
}

// TestPicker_CursorRowIsTheInverseOfSelectVisibleRow pins the accessor
// v2's panels use to draw their own ▸ marker in the gutter column beside
// the list: for every cursor position and window height, the physical row
// it reports must be the row SelectVisibleRow maps back to that same
// item. Deriving one from the other rather than duplicating scrollOffset
// is what keeps the marker on the highlighted row once the list is taller
// than the window and starts scrolling.
func TestPicker_CursorRowIsTheInverseOfSelectVisibleRow(t *testing.T) {
	items := make([]PickerItem, 9)
	for i := range items {
		items[i] = PickerItem{ID: string(rune('a' + i)), Label: string(rune('a' + i))}
	}

	for _, height := range []int{1, 2, 3, 5, 9, 20} {
		for cursor := 0; cursor < len(items); cursor++ {
			p := NewPicker(testPalette())
			p.SetItems(1, items)
			for i := 0; i < cursor; i++ {
				p.CursorNext()
			}
			want, _ := p.Selected()

			row := p.CursorRow(height)
			if row < 0 || row >= height {
				t.Errorf("CursorRow(%d) with the cursor on item %d = %d, want a row inside [0,%d)",
					height, cursor, row, height)
				continue
			}
			if !p.SelectVisibleRow(row, height) {
				t.Errorf("SelectVisibleRow(CursorRow(%d)=%d, %d) = false for item %d", height, row, height, cursor)
				continue
			}
			if got, _ := p.Selected(); got.ID != want.ID {
				t.Errorf("CursorRow(%d) reported row %d for item %d (%q), but that row holds %q",
					height, row, cursor, want.ID, got.ID)
			}
		}
	}
}

// TestPicker_CursorRowWithoutASelection pins the degenerate answers: an
// empty list has no cursor row at all, and a caller must be able to tell
// that apart from row 0.
func TestPicker_CursorRowWithoutASelection(t *testing.T) {
	p := NewPicker(testPalette())
	if got := p.CursorRow(4); got != -1 {
		t.Errorf("CursorRow(4) on an empty Picker = %d, want -1", got)
	}

	p.SetItems(1, []PickerItem{{ID: "1", Label: "Alpha"}})
	p.SetQuery("no such thing")
	if got := p.CursorRow(4); got != -1 {
		t.Errorf("CursorRow(4) with everything filtered out = %d, want -1", got)
	}

	p.SetQuery("")
	if got := p.CursorRow(0); got != 0 {
		t.Errorf("CursorRow(0) = %d, want 0 -- a degenerate height is floored at 1, matching View", got)
	}
}
