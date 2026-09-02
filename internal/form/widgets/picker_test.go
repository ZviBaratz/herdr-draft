package widgets

import (
	"strconv"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

func testPalette() theme.Palette { return theme.Default() }

// TestPicker_FilterNarrows covers the brief's "filter narrows" case: SetQuery
// must shrink the visible/navigable set to items whose cells match, in
// input order, without touching the underlying item set SetItems stored.
func TestPicker_FilterNarrows(t *testing.T) {
	p := NewPicker(testPalette())
	p.SetItems(1, []PickerItem{
		{ID: "1", Cells: []string{"Alpha"}},
		{ID: "2", Cells: []string{"Beta"}},
		{ID: "3", Cells: []string{"Alphabet"}},
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
		{ID: "1", Cells: []string{"A"}},
		{ID: "2", Cells: []string{"B"}},
		{ID: "3", Cells: []string{"C"}},
	})
	p.CursorNext() // cursor -> row 1 ("B")

	// Same version 1, refreshed content.
	p.SetItems(1, []PickerItem{
		{ID: "1", Cells: []string{"A2"}},
		{ID: "2", Cells: []string{"B2"}},
		{ID: "3", Cells: []string{"C2"}},
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
		{ID: "1", Cells: []string{"A"}},
		{ID: "2", Cells: []string{"B"}},
		{ID: "3", Cells: []string{"C"}},
	})
	p.CursorNext() // cursor -> row 1, selects ID "2"

	// Same version 1, refreshed AND reordered: "2" is now first, "3" now
	// sits at the row "2" used to occupy.
	p.SetItems(1, []PickerItem{
		{ID: "2", Cells: []string{"B2"}},
		{ID: "3", Cells: []string{"C2"}},
		{ID: "1", Cells: []string{"A2"}},
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
		{ID: "1", Cells: []string{"A"}},
		{ID: "2", Cells: []string{"B"}},
		{ID: "3", Cells: []string{"C"}},
	})
	p.CursorNext() // cursor -> row 1, selects ID "2"

	// Same version 1, "2" is gone; only 2 items remain, so the old row-1
	// position is still in range and should be kept (now "y").
	p.SetItems(1, []PickerItem{
		{ID: "x", Cells: []string{"X"}},
		{ID: "y", Cells: []string{"Y"}},
	})

	got, ok := p.Selected()
	if !ok || got.ID != "y" {
		t.Fatalf("Selected() = %+v, ok=%v, want ID \"y\" (old row 1, clamped into range) when the selected ID vanished", got, ok)
	}

	// Now the old row-1 position is out of range entirely (only 1 item
	// left): the fallback must clamp, not panic or leave a stale index.
	p.SetItems(1, []PickerItem{
		{ID: "solo", Cells: []string{"Solo"}},
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
	p.SetItems(2, []PickerItem{{ID: "fresh", Cells: []string{"Fresh"}}})

	p.SetItems(1, []PickerItem{{ID: "stale", Cells: []string{"Stale"}}})

	got, ok := p.Selected()
	if !ok || got.ID != "fresh" {
		t.Fatalf("Selected() = %+v, ok=%v, want the version-2 item unchanged by the stale version-1 call", got, ok)
	}
	if len(p.filtered) != 1 {
		t.Fatalf("filtered has %d items, want 1 (the stale call must not append or replace)", len(p.filtered))
	}
}

// TestPicker_EmptyResultSaysNothing covers the empty-result state: when
// the query matches nothing, Selected reports no selection and View
// renders its full row count BLANK -- no text of its own.
//
// This inverts the assertion it replaces, which required a bare
// "no matches" row. v2 spec §6.1 forbids exactly that string: an empty
// list must speak "in the field's own terms (`no branches yet`,
// `no assigned issues`), never a bare `no matches`". Since every field
// that can empty this list already carries such a line one row below it,
// the widget's own sentence was both wrong and second -- see the note
// above View.
func TestPicker_EmptyResultSaysNothing(t *testing.T) {
	p := NewPicker(testPalette())
	p.SetItems(1, []PickerItem{{ID: "1", Cells: []string{"Alpha"}}})
	p.SetQuery("no-such-item")

	if _, ok := p.Selected(); ok {
		t.Fatalf("Selected() ok = true with an empty filtered set, want false")
	}

	view := p.View(24, 3)
	if got := strings.TrimSpace(ansi.Strip(view)); got != "" {
		t.Errorf("View(24,3) = %q, want every row blank -- the FIELD owns the empty-list sentence", got)
	}
	if got := strings.Count(view, "\n"); got != 2 {
		t.Errorf("View(24,3) has %d newlines, want 2 (a 3-row view)", got)
	}
	for i, line := range strings.Split(view, "\n") {
		if got := lipgloss.Width(line); got != 24 {
			t.Errorf("View(24,3) line %d is %d cells wide, want 24", i, got)
		}
	}
}

// TestPicker_ViewDoesNotPanicOnDegenerateDimensions guards the "no panics"
// requirement for boundary width/height values a caller could pass while a
// popup is being resized.
func TestPicker_ViewDoesNotPanicOnDegenerateDimensions(t *testing.T) {
	p := NewPicker(testPalette())
	p.SetItems(1, []PickerItem{{ID: "1", Cells: []string{"Alpha"}}})
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
			Cells: []string{"this label is far longer than the ten-cell width given to View"},
			Badge: "and this hint is also much longer than ten cells",
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
		{ID: "1", Cells: []string{"first label is much longer than the given width for sure"}},
		{ID: "2", Cells: []string{"second label is also much longer than the given width"}},
		{ID: "3", Cells: []string{"third"}},
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
		{ID: "1", Cells: []string{"Alpha"}},
		{ID: "2", Cells: []string{"Beta"}},
		{ID: "3", Cells: []string{"Alphabet"}},
	})
	if got := p.FilteredLen(); got != 3 {
		t.Errorf("FilteredLen() = %d, want 3", got)
	}

	p.SetQuery("alpha")
	if got := p.FilteredLen(); got != 2 {
		t.Errorf("FilteredLen() under a query = %d, want 2 (the filtered set, not the item set)", got)
	}
}

// TestPicker_Len pins the other half of v3 spec §8.5's `3/24` readout: Len
// must keep reporting the whole item set while a query narrows
// FilteredLen, and must not be affected by a query that matches nothing.
func TestPicker_Len(t *testing.T) {
	p := NewPicker(testPalette())
	if got := p.Len(); got != 0 {
		t.Errorf("Len() on a fresh Picker = %d, want 0", got)
	}

	p.SetItems(1, []PickerItem{
		{ID: "1", Cells: []string{"Alpha"}},
		{ID: "2", Cells: []string{"Beta"}},
		{ID: "3", Cells: []string{"Alphabet"}},
	})
	if got := p.Len(); got != 3 {
		t.Errorf("Len() = %d, want 3", got)
	}

	p.SetQuery("alpha")
	if got, want := p.Len(), 3; got != want {
		t.Errorf("Len() under a query = %d, want %d (the item set, not the filtered set)", got, want)
	}

	p.SetQuery("no-such-item")
	if got := p.Len(); got != 3 {
		t.Errorf("Len() with everything filtered out = %d, want 3", got)
	}
	if got := p.FilteredLen(); got != 0 {
		t.Errorf("FilteredLen() with everything filtered out = %d, want 0", got)
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
		items[i] = PickerItem{ID: string(rune('a' + i)), Cells: []string{string(rune('a' + i))}}
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

	p.SetItems(1, []PickerItem{{ID: "1", Cells: []string{"Alpha"}}})
	p.SetQuery("no such thing")
	if got := p.CursorRow(4); got != -1 {
		t.Errorf("CursorRow(4) with everything filtered out = %d, want -1", got)
	}

	p.SetQuery("")
	if got := p.CursorRow(0); got != 0 {
		t.Errorf("CursorRow(0) = %d, want 0 -- a degenerate height is floored at 1, matching View", got)
	}
}

// rowsOf renders a picker and returns its plain-text rows, color and
// zone markers stripped -- the alignment claims below are about which
// CELL a character lands in, which is exactly what the escape sequences
// hide.
func rowsOf(p *Picker, width, height int) []string {
	return strings.Split(ansi.Strip(p.View(width, height)), "\n")
}

// cellIndexOf reports which CELL sub starts at within line, or -1 when it
// is absent. strings.Index answers in bytes, and the mark column's own
// glyphs are three bytes each, so a byte offset is the wrong unit for
// every alignment claim in this file.
func cellIndexOf(line, sub string) int {
	at := strings.Index(line, sub)
	if at < 0 {
		return -1
	}
	return ansi.StringWidth(line[:at])
}

// cellsOf slices the first n cells off line.
func cellsOf(line string, n int) string {
	return string([]rune(line)[:n])
}

// TestPicker_ColumnWidthsAreStableWhileScrolling is v3 spec §8.1's stated
// reason for measuring over the whole filtered set rather than over the
// visible window: with a window measurement, scrolling a wider row into
// view widens its column and shifts every other row's content sideways
// mid-scroll. The fixture is built so a window measurement CANNOT pass --
// the widest first cell is at the bottom of the list and out of the
// window until the cursor reaches it.
func TestPicker_ColumnWidthsAreStableWhileScrolling(t *testing.T) {
	p := NewPicker(testPalette())
	p.SetColumns(PickerColumn{}, PickerColumn{Flex: true})
	items := []PickerItem{
		{ID: "1", Cells: []string{"a", "first"}},
		{ID: "2", Cells: []string{"bb", "second"}},
		{ID: "3", Cells: []string{"ccc", "third"}},
		{ID: "4", Cells: []string{"dddd", "fourth"}},
		{ID: "5", Cells: []string{"eeeeeeeeeeee", "fifth"}},
	}
	p.SetItems(1, items)

	const height = 2
	// Column 1 starts wherever column 0 ends plus the gap, so the index of
	// the second cell's first character IS the measurement, read back off
	// the render.
	want := cellIndexOf(rowsOf(p, 40, height)[0], "first")
	if want <= 0 {
		t.Fatalf("could not locate the second column in the first row: %q", rowsOf(p, 40, height)[0])
	}

	for step := 1; step < len(items); step++ {
		p.CursorNext()
		for row, line := range rowsOf(p, 40, height) {
			text := strings.TrimRight(line, " ")
			if text == "" {
				continue
			}
			second := strings.Fields(text)[1]
			if got := cellIndexOf(line, second); got != want {
				t.Errorf("after %d CursorNext, row %d starts its second column at cell %d, want %d (columns must not jitter as the list scrolls): %q",
					step, row, got, want, line)
			}
		}
	}
}

// TestPicker_MarkColumnKeepsCellsAligned pins v3 spec §8.2's fixed-width
// mark column against the defect it replaces: before v3 the Marker was a
// bare prefix, so a marked row's content started two cells right of every
// unmarked row's and nothing in the list lined up.
//
// It also pins the priority rule in the same breath -- a row that is both
// marked and current shows the Marker, because a profile that is both
// current and auth-failed has to shout the failure.
func TestPicker_MarkColumnKeepsCellsAligned(t *testing.T) {
	p := NewPicker(testPalette())
	p.SetItems(1, []PickerItem{
		{ID: "plain", Cells: []string{"plain"}},
		{ID: "current", Cells: []string{"current"}, Current: true},
		{ID: "marked", Cells: []string{"marked"}, Marker: "!"},
		{ID: "both", Cells: []string{"both"}, Marker: "!", Current: true},
	})

	rows := rowsOf(p, 30, 4)
	for i, want := range []string{"plain", "current", "marked", "both"} {
		if got := cellIndexOf(rows[i], want); got != markColumnWidth {
			t.Errorf("row %d (%q) starts its first cell at %d, want %d -- the mark column is FIXED width: %q",
				i, want, got, markColumnWidth, rows[i])
		}
	}
	if got := cellsOf(rows[1], markColumnWidth); got != markerCurrent+" " {
		t.Errorf("the current row's mark column = %q, want %q", got, markerCurrent+" ")
	}
	if got := cellsOf(rows[3], markColumnWidth); got != "! " {
		t.Errorf("the marked-AND-current row's mark column = %q, want %q -- the marker wins", got, "! ")
	}
}

// TestPicker_NoMarkColumnWhenNothingUsesIt is the other half of the
// fixed-width rule: a picker whose items carry neither a marker nor a
// current flag must not pay two cells for a column it will never draw in
// -- the base-ref and issue lists are exactly that.
func TestPicker_NoMarkColumnWhenNothingUsesIt(t *testing.T) {
	p := NewPicker(testPalette())
	p.SetItems(1, []PickerItem{
		{ID: "1", Cells: []string{"alpha"}},
		{ID: "2", Cells: []string{"beta"}},
	})
	for i, want := range []string{"alpha", "beta"} {
		line := rowsOf(p, 20, 2)[i]
		if got := cellIndexOf(line, want); got != 0 {
			t.Errorf("row %d = %q, want its content flush left at cell 0 (got %d): no item uses the mark column", i, line, got)
		}
	}
}

// TestPicker_BadgeIsFlushRightInItsOwnColumn pins the two properties v3
// spec §8.1 gives the badge: every badge ends at the row's last cell, and
// the cells of EVERY row -- badge or not -- stop short of the widest
// badge, so a long value can never run into a neighbour's status word.
func TestPicker_BadgeIsFlushRightInItsOwnColumn(t *testing.T) {
	p := NewPicker(testPalette())
	p.SetItems(1, []PickerItem{
		{ID: "1", Cells: []string{"short"}, Badge: "In Progress"},
		{ID: "2", Cells: []string{"a much longer value than the first"}},
		{ID: "3", Cells: []string{"third"}, Badge: "Todo"},
	})

	const width = 60
	rows := rowsOf(p, width, 3)
	for i, badge := range []string{"In Progress", "", "Todo"} {
		if badge == "" {
			continue
		}
		if got, want := cellIndexOf(rows[i], badge)+ansi.StringWidth(badge), width; got != want {
			t.Errorf("row %d's badge %q ends at cell %d, want %d (flush right): %q", i, badge, got, want, rows[i])
		}
	}
	// The badge-less row's own content must still clear the reserved
	// column: "In Progress" is 11 cells, plus the two-cell gap.
	if got := ansi.StringWidth(strings.TrimRight(rows[1], " ")); got > width-ansi.StringWidth("In Progress")-cellGap {
		t.Errorf("the badge-less row runs to cell %d, want it clear of the reserved badge column: %q", got, rows[1])
	}
}

// TestPicker_ColumnBoundsAndElideMode pins the three knobs PickerColumn
// adds that nothing else in this file exercises: Max caps a column
// however long its content is, Min holds one open however short, and
// Elide decides which END of an over-long cell survives -- the difference
// between a path a reader can still identify and one they cannot.
func TestPicker_ColumnBoundsAndElideMode(t *testing.T) {
	p := NewPicker(testPalette())
	p.SetColumns(
		PickerColumn{Max: 6},
		PickerColumn{Min: 8},
		PickerColumn{Elide: ElideHead, Max: 12},
	)
	p.SetItems(1, []PickerItem{
		{ID: "1", Cells: []string{"an overlong first cell", "x", "/home/zvi/Projects/herdr-draft"}},
	})

	row := rowsOf(p, 60, 1)[0]
	if want := "an ov…"; cellIndexOf(row, want) != 0 {
		t.Errorf("row = %q, want it to open with %q -- Max 6 caps the first column", row, want)
	}
	// Min 8 on a one-cell column pushes the third column to 6+2+8+2 == 18,
	// and ElideHead keeps the path's TAIL: the segments that identify it.
	if got, want := cellIndexOf(row, Ellipsis+"herdr-draft"), 18; got != want {
		t.Errorf("the third column starts at cell %d, want %d holding the path's tail: %q", got, want, row)
	}
}

// TestPicker_EmptyColumnCostsNothing pins the collapse rule rowLayout.left
// documents: a column no row in the set has anything in takes neither
// width nor its two-cell gap. It is reachable rather than theoretical --
// clauth's AuthStatus is unvalidated JSON and can be empty for every
// profile at once -- and a two-cell hole around an invisible column reads
// as a rendering fault, not as a column nobody filled in.
func TestPicker_EmptyColumnCostsNothing(t *testing.T) {
	p := NewPicker(testPalette())
	p.SetItems(1, []PickerItem{
		{ID: "1", Cells: []string{"alpha", "", "one"}},
		{ID: "2", Cells: []string{"beta", "", "two"}},
	})

	row := rowsOf(p, 40, 1)[0]
	if got, want := cellIndexOf(row, "one"), len("alpha")+cellGap; got != want {
		t.Errorf("the third column starts at cell %d, want %d -- the empty middle column costs nothing at all: %q", got, want, row)
	}
}

// TestPicker_QueryOwnsMatchOnlyWhileItIsSet pins v3 spec §8.4's ownership
// rule, the half of it this widget implements: with a query set,
// applyFilter computes every kept item's Match and overwrites whatever
// the caller supplied; with no query, the caller's own span is preserved
// verbatim, which is how a field that ranks its own candidates supplies
// spans this widget could not have computed.
func TestPicker_QueryOwnsMatchOnlyWhileItIsSet(t *testing.T) {
	caller := PickerMatch{Col: 0, Start: 1, End: 3}
	p := NewPicker(testPalette())
	p.SetItems(1, []PickerItem{
		{ID: "1", Cells: []string{"alpha", "beta"}, Match: caller},
	})

	if got, _ := p.Selected(); got.Match != caller {
		t.Errorf("Match with no query = %+v, want the caller's own %+v preserved verbatim", got.Match, caller)
	}

	p.SetQuery("et")
	got, ok := p.Selected()
	if !ok {
		t.Fatalf("Selected() ok = false, want the item kept: %q matches cell 1", "et")
	}
	if want := (PickerMatch{Col: 1, Start: 1, End: 3}); got.Match != want {
		t.Errorf("Match under a query = %+v, want %+v (cell 1, runes [1,3))", got.Match, want)
	}

	// A row kept only because its BADGE matched has nothing to paint: the
	// span would have to point at a column, and the badge is not one.
	p.SetQuery("")
	p.SetItems(2, []PickerItem{{ID: "1", Cells: []string{"alpha"}, Badge: "Todo"}})
	p.SetQuery("odo")
	got, ok = p.Selected()
	if !ok {
		t.Fatalf("Selected() ok = false, want the badge match to keep the row")
	}
	if !got.Match.empty() {
		t.Errorf("Match for a badge-only match = %+v, want nothing to paint", got.Match)
	}
}

// lastCellsOf renders a picker and returns the last CELL of each row,
// color and zone markers stripped -- the scrollbar column, when there is
// one.
func lastCellsOf(p *Picker, width, height int) []string {
	rows := rowsOf(p, width, height)
	out := make([]string, len(rows))
	for i, line := range rows {
		runes := []rune(line)
		if len(runes) == 0 {
			continue
		}
		out[i] = string(runes[len(runes)-1])
	}
	return out
}

// scrollbarItems is n numbered rows, wide enough to be worth eliding and
// short enough to read back off a 20-cell render.
func scrollbarItems(n int) []PickerItem {
	items := make([]PickerItem, n)
	for i := range items {
		items[i] = PickerItem{ID: strconv.Itoa(i), Cells: []string{"row" + strconv.Itoa(i)}}
	}
	return items
}

// TestPicker_ScrollbarAppearsOnlyWhenTheListOutgrowsTheWindow is v3 spec
// §8.5's conditional reservation, both halves of it: a list that fits
// spends no cell at all on a scrollbar, and one row more than fits costs
// the last column of EVERY row -- content included, which is the part the
// spec accepts out loud ("content narrows by one cell the moment the list
// outgrows the window").
func TestPicker_ScrollbarAppearsOnlyWhenTheListOutgrowsTheWindow(t *testing.T) {
	p := NewPicker(testPalette())
	p.SetItems(1, scrollbarItems(4))

	for i, cell := range lastCellsOf(p, 20, 4) {
		if cell == scrollTrackGlyph || cell == scrollThumbGlyph {
			t.Errorf("row %d ends in %q at a height that fits all 4 items, want no scrollbar at all", i, cell)
		}
	}

	p.SetItems(2, scrollbarItems(5))
	for i, cell := range lastCellsOf(p, 20, 4) {
		if cell != scrollTrackGlyph && cell != scrollThumbGlyph {
			t.Errorf("row %d ends in %q with 5 items in a 4-row window, want the track %q or the thumb %q",
				i, cell, scrollTrackGlyph, scrollThumbGlyph)
		}
	}
}

// TestPicker_ScrollbarTakesItsCellFromTheContent pins what the reserved
// column costs: the badge stops one cell short of the render width, in
// the column the bar now owns, rather than being overwritten by it.
func TestPicker_ScrollbarTakesItsCellFromTheContent(t *testing.T) {
	p := NewPicker(testPalette())
	items := scrollbarItems(5)
	for i := range items {
		items[i].Badge = "Todo"
	}
	p.SetItems(1, items)

	const width = 20
	row := rowsOf(p, width, 4)[0]
	if got, want := cellIndexOf(row, "Todo")+ansi.StringWidth("Todo"), width-scrollbarWidth; got != want {
		t.Errorf("the badge ends at cell %d, want %d -- the scrollbar owns the last cell now: %q", got, want, row)
	}
	if got := ansi.StringWidth(row); got != width {
		t.Errorf("row width = %d, want exactly %d: %q", got, width, row)
	}
}

// TestPicker_ScrollbarThumbFollowsTheCursor walks the cursor down a long
// list and checks the drawn thumb against scrollThumb's own answer at the
// offset the render used. The point is the WIRING -- that the column is
// painted from the same geometry the rows are scrolled by -- since the
// geometry itself is tabled below.
func TestPicker_ScrollbarThumbFollowsTheCursor(t *testing.T) {
	const total, height = 24, 6
	p := NewPicker(testPalette())
	p.SetItems(1, scrollbarItems(total))

	seenMidTrack := false
	for step := 0; step < total; step++ {
		wantTop, wantLength := scrollThumb(total, height, scrollOffset(step, total, height))
		if wantTop > 0 && wantTop+wantLength < height {
			seenMidTrack = true
		}
		for row, cell := range lastCellsOf(p, 20, height) {
			want := scrollTrackGlyph
			if row >= wantTop && row < wantTop+wantLength {
				want = scrollThumbGlyph
			}
			if cell != want {
				t.Fatalf("with the cursor on item %d, row %d of the bar = %q, want %q (thumb rows [%d,%d))",
					step, row, cell, want, wantTop, wantTop+wantLength)
			}
		}
		p.CursorNext()
	}
	if !seenMidTrack {
		t.Error("the sweep never produced a thumb clear of both ends -- the fixture no longer exercises a mid-track thumb")
	}
}

// TestScrollThumb tables v3 spec §8.5's thumb geometry. It is worth this
// much table for one reason: the arithmetic is integer, the interesting
// answers are the rounded ones, and every one of them is far cheaper to
// pin here than to read back out of a rendered column later.
func TestScrollThumb(t *testing.T) {
	cases := []struct {
		name             string
		total, rows      int
		offset           int
		wantTop, wantLen int
	}{
		// (0, 0) is "draw no scrollbar": the list fits, so §8.5 reserves
		// no column for one.
		{"whole list fits", 5, 10, 0, 0, 0},
		{"list exactly fills the window", 10, 10, 0, 0, 0},

		// One item over is the shortest scroll there is, and the case
		// where an off-by-one at either end is most visible: two
		// positions, and the thumb must occupy 9 of 10 rows in both.
		{"one item over, at the top", 11, 10, 0, 0, 9},
		{"one item over, at the bottom", 11, 10, 1, 1, 9},

		{"offset 0 pins the thumb to the top", 20, 10, 0, 0, 5},
		// 5*(10-5)/(20-10) == 2.5, which must round UP, not truncate.
		{"mid-scroll rounds half up", 20, 10, 5, 3, 5},
		// The bottom must be reached EXACTLY: top+length == rows.
		{"max offset reaches the bottom exactly", 20, 10, 10, 5, 5},

		// A window 1/10th of the list: length rounds to exactly 1
		// without needing the clamp, and the thumb still spans the full
		// track from top to bottom.
		{"long list, at the top", 100, 10, 0, 0, 1},
		{"long list, mid-scroll", 100, 10, 45, 5, 1},
		{"long list, at the bottom", 100, 10, 90, 9, 1},

		// rows*rows/total rounds to ZERO here; the clamp is what keeps
		// the scrollbar visible rather than absent.
		{"enormous list clamps length to 1, not 0", 10000, 10, 0, 0, 1},
		{"enormous list at the bottom still reaches it", 10000, 10, 9990, 9, 1},

		// A one-row window has one thumb position, and (rows-length) is
		// 0, so top must stay 0 without dividing by anything degenerate.
		{"single row window, at the top", 5, 1, 0, 0, 1},
		{"single row window, at the bottom", 5, 1, 4, 0, 1},

		// Degenerate inputs answer (0, 0) rather than panicking or
		// dividing by zero -- a picker mid-resize can hand over any of
		// these.
		{"zero rows", 100, 0, 0, 0, 0},
		{"negative rows", 100, -3, 5, 0, 0},
		{"zero total", 0, 10, 0, 0, 0},
		{"negative total", -5, 10, 0, 0, 0},
		{"zero total and zero rows", 0, 0, 0, 0, 0},

		// An out-of-range offset clamps into [0, total-rows] the way
		// scrollOffset's own bounds do, rather than sliding the thumb
		// off either end of the track.
		{"negative offset clamps to the top", 20, 10, -7, 0, 5},
		{"offset past the end clamps to the bottom", 20, 10, 99, 5, 5},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			top, length := scrollThumb(c.total, c.rows, c.offset)
			if top != c.wantTop || length != c.wantLen {
				t.Errorf("scrollThumb(total=%d, rows=%d, offset=%d) = (top=%d, len=%d), want (top=%d, len=%d)",
					c.total, c.rows, c.offset, top, length, c.wantTop, c.wantLen)
			}
		})
	}
}

// TestScrollThumb_StaysInsideTheTrack sweeps every offset of a spread of
// list/window sizes for the two invariants a renderer depends on and no
// single table row can state: the thumb never leaves the track, and its
// ends are reached exactly -- top == 0 at the first offset, top+length ==
// rows at the last. The second is the one that would otherwise go unnoticed
// as a scrollbar that stops one row short of the bottom of a long list.
func TestScrollThumb_StaysInsideTheTrack(t *testing.T) {
	for _, rows := range []int{1, 2, 3, 7, 10, 24} {
		for _, total := range []int{rows + 1, rows + 2, rows * 2, rows*3 + 1, rows * 97} {
			maxOffset := total - rows
			for offset := 0; offset <= maxOffset; offset++ {
				top, length := scrollThumb(total, rows, offset)
				if length < 1 || length > rows {
					t.Fatalf("scrollThumb(%d, %d, %d) length = %d, want within [1,%d]",
						total, rows, offset, length, rows)
				}
				if top < 0 || top+length > rows {
					t.Fatalf("scrollThumb(%d, %d, %d) = (top=%d, len=%d), want the thumb inside a %d-row track",
						total, rows, offset, top, length, rows)
				}
				switch offset {
				case 0:
					if top != 0 {
						t.Errorf("scrollThumb(%d, %d, 0) top = %d, want 0 (flush with the top)",
							total, rows, top)
					}
				case maxOffset:
					if top+length != rows {
						t.Errorf("scrollThumb(%d, %d, %d) = (top=%d, len=%d), want top+len == %d (flush with the bottom)",
							total, rows, offset, top, length, rows)
					}
				}
			}
		}
	}
}

// TestPicker_FilteredHasID covers the one thing FilteredHasID exists for:
// telling a field whose picker carries a sentinel row whether that row is
// still in the filtered set, so v3 spec §8.5's count can leave it out.
func TestPicker_FilteredHasID(t *testing.T) {
	p := NewPicker(testPalette())
	p.SetItems(1, []PickerItem{
		{ID: "\x00none", Cells: []string{"none"}},
		{ID: "ENG-1", Cells: []string{"ENG-1", "Fix login bug"}},
		{ID: "ENG-2", Cells: []string{"ENG-2", "Add dark mode"}},
	})

	for _, id := range []string{"\x00none", "ENG-1", "ENG-2"} {
		if !p.FilteredHasID(id) {
			t.Errorf("FilteredHasID(%q) = false with no query, want true", id)
		}
	}
	if p.FilteredHasID("ENG-9") {
		t.Error(`FilteredHasID("ENG-9") = true, want false -- no such item`)
	}
	// An empty id matches nothing rather than every item that forgot one.
	if p.FilteredHasID("") {
		t.Error(`FilteredHasID("") = true, want false`)
	}

	p.SetQuery("ne")
	if !p.FilteredHasID("\x00none") {
		t.Error(`the "none" row did not survive "ne", so this case proves nothing`)
	}
	if p.FilteredHasID("ENG-1") {
		t.Error(`FilteredHasID("ENG-1") = true under a query it does not match, want false`)
	}
}

// TestPicker_DropBelowDropsRatherThanElides pins PickerColumn.DropBelow:
// a column squeezed past the point where it can say anything goes
// entirely -- taking its gap with it -- instead of showing a lone "…".
// That is the badge's own rule, offered to a cell column.
func TestPicker_DropBelowDropsRatherThanElides(t *testing.T) {
	items := []PickerItem{
		{ID: "a", Cells: []string{"alpha", "Team", "in 2h11m"}},
		{ID: "b", Cells: []string{"bravo", "Max 20x", "in 45m"}},
	}
	newP := func() *Picker {
		p := NewPicker(testPalette())
		p.SetColumns(
			PickerColumn{},
			PickerColumn{},
			PickerColumn{DropBelow: 6},
		)
		p.SetItems(1, items)
		return p
	}

	// Wide enough for everything: the column is there.
	if row := rowsOf(newP(), 40, 2)[0]; !strings.Contains(row, "in 2h11m") {
		t.Errorf("at 40 cells the row = %q, want the whole third column", row)
	}
	// Narrow enough that shrinking the column would put it under its
	// DropBelow, so it goes instead -- and nothing is elided. The natural
	// row is 24 cells (5 + 2 + 7 + 2 + 8), so 20 is four over and the
	// column could only keep four of its eight.
	narrow := rowsOf(newP(), 20, 2)[0]
	if strings.Contains(narrow, "…") {
		t.Errorf("at 20 cells the row = %q, want the third column dropped rather than elided", narrow)
	}
	if strings.Contains(narrow, "in ") {
		t.Errorf("at 20 cells the row = %q, want the third column gone entirely", narrow)
	}
	if !strings.Contains(narrow, "alpha") || !strings.Contains(narrow, "Team") {
		t.Errorf("at 20 cells the row = %q, want the two columns it made room for intact", narrow)
	}

	// A column that can absorb the overflow while staying at or above its
	// DropBelow is SHRUNK, not dropped: the drop is a last resort, not a
	// cliff at the first missing cell.
	if row := rowsOf(newP(), 23, 2)[0]; !strings.Contains(row, "in 2h1") {
		t.Errorf("at 23 cells the row = %q, want the third column shrunk rather than dropped", row)
	}
}

// TestPicker_DropBelowZeroNeverDrops is the default every other column in
// the project relies on.
func TestPicker_DropBelowZeroNeverDrops(t *testing.T) {
	p := NewPicker(testPalette())
	p.SetColumns(PickerColumn{}, PickerColumn{})
	p.SetItems(1, []PickerItem{{ID: "a", Cells: []string{"alpha", "in 2h11m"}}})

	if row := rowsOf(p, 12, 1)[0]; !strings.Contains(row, "…") {
		t.Errorf("row = %q, want the undeclared column elided rather than dropped", row)
	}
}

// --- v3 spec §8.4, match highlighting -------------------------------------

// accentOpen is the exact escape sequence matchStyle emits before its
// text, derived from the style itself rather than written out, so a
// lipgloss change to SGR parameter ORDER moves these tests with it
// instead of silently making every one of them assert nothing.
func accentOpen(p *Picker) string {
	open, _, _ := strings.Cut(p.matchStyle().Render("\x00"), "\x00")
	return open
}

// accentRun returns the plain text of the first run in line painted in
// matchStyle, or "" when there is none. It is the only assertion that
// distinguishes "the highlight is on the right characters" from "the
// highlight is somewhere on the row" -- rowsOf strips every escape, and a
// stripped row reads identically whether the accent landed on the matched
// run or on the two characters beside it.
func accentRun(p *Picker, line string) string {
	_, after, ok := strings.Cut(line, accentOpen(p))
	if !ok {
		return ""
	}
	run, _, _ := strings.Cut(after, "\x1b")
	return run
}

// TestHighlightCell_SpanIsHalfOpen is the guard v3 spec §8.4's boxed
// warning asks for by name. widgets.PickerMatch's End is HALF-OPEN and
// internal/form's fuzzySpan's is INCLUSIVE, and the two conventions differ
// by exactly one at exactly the coordinate a renderer indexes with -- so a
// highlightCell that read its arguments as inclusive would still light up
// on nearly every fixture, one character short at the end and one
// character wide at the empty span [n, n).
//
// The first and last runes of the cell are the cases the brief singles
// out, because they are the ones a fencepost error turns into NO
// highlight rather than into a visibly wrong one.
func TestHighlightCell_SpanIsHalfOpen(t *testing.T) {
	p := NewPicker(testPalette())
	for _, tc := range []struct {
		name       string
		start, end int
		want       string
	}{
		{"the first rune alone", 0, 1, "a"},
		{"the last rune alone", 4, 5, "a"},
		{"an interior run", 1, 4, "lph"},
		{"the whole cell", 0, 5, "alpha"},
		{"an empty span paints nothing", 2, 2, ""},
		{"a span past the end is clipped to it", 3, 99, "ha"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cell := highlightCell("alpha", 5, tc.start, tc.end, ElideTail, lipgloss.NewStyle(), p.matchStyle())
			if got := accentRun(p, cell); got != tc.want {
				t.Errorf("highlightCell(%q, [%d,%d)) accents %q, want %q (rendered %q)",
					"alpha", tc.start, tc.end, got, tc.want, cell)
			}
			if got, want := ansi.StringWidth(ansi.Strip(cell)), 5; got != want {
				t.Errorf("the cell is %d cells wide, want exactly %d: %q", got, want, cell)
			}
		})
	}
}

// TestHighlightCell_PadsAndTonesLikeFitCell pins the two properties that
// make this a drop-in for fitCell rather than a second cell renderer: the
// cell is still exactly width cells however short its text, and the pad
// carries the ROW's own style, not the accent's -- a padded run in Accent
// would smear the highlight across the gap to the next column.
func TestHighlightCell_PadsAndTonesLikeFitCell(t *testing.T) {
	p := NewPicker(testPalette())
	cell := highlightCell("ab", 8, 0, 1, ElideTail, lipgloss.NewStyle(), p.matchStyle())
	if got, want := ansi.Strip(cell), "ab      "; got != want {
		t.Errorf("highlightCell padded to %q, want %q", got, want)
	}
	if got, want := accentRun(p, cell), "a"; got != want {
		t.Errorf("the accented run is %q, want %q -- the pad must not join it: %q", got, want, cell)
	}
}

// TestHighlightCell_TruncatesFirstThenIntersects pins v3 spec §8.4's
// stated ORDER of operations, which is not interchangeable with the
// obvious alternative: slicing the cell into head/span/tail and eliding
// each piece spends an ellipsis per piece and overshoots the width, where
// doing it in this order spends one and lands on it.
//
// ElideHead is the case that carries an offset -- its ellipsis is a
// LEADING rune, so every surviving character sits one rune right of where
// the span was computed, on top of the runes the cut removed. Getting
// that offset wrong shifts the highlight by a fixed amount on every path
// in the project's most-filtered panel.
func TestHighlightCell_TruncatesFirstThenIntersects(t *testing.T) {
	p := NewPicker(testPalette())
	base := lipgloss.NewStyle()

	// ElideTail at 4 keeps "alp" and spends the fourth cell on the
	// ellipsis. A span on the surviving "l" paints; a span on the "b"
	// that was cut away paints nothing at all rather than clamping onto
	// the ellipsis -- a highlight pointing at a character the reader
	// cannot see is worse than none.
	tail := highlightCell("alphabet", 4, 1, 2, ElideTail, base, p.matchStyle())
	if got, want := ansi.Strip(tail), "alp"+Ellipsis; got != want {
		t.Fatalf("elided cell = %q, want %q", got, want)
	}
	if got, want := accentRun(p, tail), "l"; got != want {
		t.Errorf("the accented run is %q, want %q: %q", got, want, tail)
	}
	if got := accentRun(p, highlightCell("alphabet", 4, 5, 6, ElideTail, base, p.matchStyle())); got != "" {
		t.Errorf("a span on a truncated-away rune accents %q, want nothing", got)
	}

	// ElideHead at 5 keeps "/bcd" behind a leading ellipsis. The "c" is
	// rune 4 of the ORIGINAL and must land on rune 3 of what is drawn.
	head := highlightCell("/a/bcd", 5, 4, 5, ElideHead, base, p.matchStyle())
	if got, want := ansi.Strip(head), Ellipsis+"/bcd"; got != want {
		t.Fatalf("elided cell = %q, want %q", got, want)
	}
	if got, want := accentRun(p, head), "c"; got != want {
		t.Errorf("the accented run is %q, want %q -- the leading ellipsis's offset was not carried: %q", got, want, head)
	}
}

// TestPicker_MatchIsPaintedOnTheColumnItNames walks the widget end to
// end: applyFilter computes the span, renderRow routes that one cell
// through highlightCell, and every other cell renders as it always did.
// The SECOND column is the interesting one -- a renderer that ignored
// Match.Col would light up the first.
func TestPicker_MatchIsPaintedOnTheColumnItNames(t *testing.T) {
	p := NewPicker(testPalette())
	p.SetItems(1, []PickerItem{
		{ID: "1", Cells: []string{"alpha", "beta"}},
		{ID: "2", Cells: []string{"gamma", "delta"}},
	})
	p.SetQuery("elt")

	line := strings.Split(p.View(40, 1), "\n")[0]
	if got, want := accentRun(p, line), "elt"; got != want {
		t.Errorf("the accented run is %q, want %q from cell 1 of %q: %q", got, want, "delta", line)
	}
	if !strings.Contains(ansi.Strip(line), "gamma") {
		t.Errorf("row = %q, want the unmatched first cell still rendered", ansi.Strip(line))
	}
}

// TestPicker_MatchIsTheSameAccentOnTheCursorRow pins v3 spec §8.4's "on
// both cursor and non-cursor rows". The cursor row already carries three
// signals of its own, and a span that changed colour under the cursor
// would stop the eye running DOWN the column comparing where each row
// matched -- which is the whole job of the highlight.
func TestPicker_MatchIsTheSameAccentOnTheCursorRow(t *testing.T) {
	p := NewPicker(testPalette())
	p.SetItems(1, []PickerItem{
		{ID: "1", Cells: []string{"herdr"}},
		{ID: "2", Cells: []string{"herdr-draft"}},
	})
	p.SetQuery("erd")

	rows := strings.Split(p.View(40, 2), "\n")
	cursorRun, plainRun := accentRun(p, rows[0]), accentRun(p, rows[1])
	if cursorRun != "erd" || plainRun != "erd" {
		t.Fatalf("accented runs are %q (cursor row) and %q (plain row), want %q on both", cursorRun, plainRun, "erd")
	}
	if got, want := strings.Count(rows[0], accentOpen(p)), 1; got != want {
		t.Errorf("the cursor row opens the accent %d times, want %d: %q", got, want, rows[0])
	}
}

// TestPicker_NoMatchRendersExactlyAsBefore is the regression half. Match
// is unset on most items of every list in the project, so those rows must
// come out byte for byte as they did before §8.4 -- which is what makes a
// golden-frame diff after this change name only the rows that really
// gained a highlight.
func TestPicker_NoMatchRendersExactlyAsBefore(t *testing.T) {
	p := NewPicker(testPalette())
	items := []PickerItem{{ID: "1", Cells: []string{"alpha", "beta"}}}
	p.SetItems(1, items)
	plain := p.View(40, 1)

	// Col -1 is §8.1's "nothing to paint" and an empty span says the same
	// thing; both must be as inert as the zero value.
	for _, m := range []PickerMatch{{Col: -1}, {Col: -1, Start: 1, End: 3}, {Col: 0, Start: 2, End: 2}} {
		marked := append([]PickerItem(nil), items...)
		marked[0].Match = m
		p.SetItems(2, marked)
		if got := p.View(40, 1); got != plain {
			t.Errorf("Match %+v changed the rendering:\n got %q\nwant %q", m, got, plain)
		}
	}
}

// TestPicker_CursorlessDrawsNoCursorRow pins v3 spec §9's read-only list:
// no row takes the cursor row's treatment and CursorRow answers -1, so
// the panel's own gutter draws no ▸ either. Everything else -- columns,
// marks, badges, the scrollbar -- is untouched, which is what makes the
// session list "not a new widget".
func TestPicker_CursorlessDrawsNoCursorRow(t *testing.T) {
	items := []PickerItem{
		{ID: "a", Cells: []string{"alpha"}},
		{ID: "b", Cells: []string{"bravo"}},
	}
	withCursor := NewPicker(testPalette())
	withCursor.SetItems(1, items)
	cursorless := NewPicker(testPalette())
	cursorless.SetCursorless(true)
	cursorless.SetItems(1, items)

	if got := withCursor.CursorRow(4); got != 0 {
		t.Fatalf("CursorRow on a normal picker = %d, want 0 -- this test is about the difference", got)
	}
	if got := cursorless.CursorRow(4); got != -1 {
		t.Errorf("CursorRow on a cursorless picker = %d, want -1 (the panel draws its ▸ from this)", got)
	}

	// The RAW renders differ (row 0 loses its fill and its bold) while the
	// stripped text does not: suppressing the cursor must not move a cell.
	rawWith, rawWithout := withCursor.MarkedView(20, 4, ""), cursorless.MarkedView(20, 4, "")
	if rawWith == rawWithout {
		t.Error("the cursorless render is byte-identical to the cursored one; nothing was suppressed")
	}
	if !strings.Contains(rawWith, ansiBg(testPalette().Surface)) {
		t.Fatal("the cursored picker paints no Surface row at all; this test cannot detect its absence")
	}
	if strings.Contains(rawWithout, ansiBg(testPalette().Surface)) {
		t.Error("a cursorless row is painted with the cursor row's Surface fill")
	}
	if got, want := ansi.Strip(rawWithout), ansi.Strip(rawWith); got != want {
		t.Errorf("the cursorless text differs from the cursored one:\n got %q\nwant %q", got, want)
	}
}

// ansiBg renders c's background SGR the way lipgloss emits it.
func ansiBg(c theme.Color) string {
	rendered := lipgloss.NewStyle().Background(c).Render("x")
	return rendered[:strings.Index(rendered, "x")]
}
