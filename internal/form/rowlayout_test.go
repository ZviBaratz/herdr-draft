package form

import (
	"fmt"
	"testing"
)

// --- layoutFrame: the worked table -----------------------------------------

// TestLayoutFrame_SpecNineLadder pins v2 spec §9's degradation ladder
// against the worked numbers in the issue and in rowlayout.go's own doc
// comment, n = 8 (the assembled form's eight stack rows once Create is
// on the footer). These are the values a reviewer can check by eye
// against the spec, so they are asserted literally rather than derived.
func TestLayoutFrame_SpecNineLadder(t *testing.T) {
	const n = 8
	cases := []struct {
		h    int
		want frame
	}{
		{40, frame{Header: true, Rule1: true, Rows: 8, Rule2: true, Region: 28, Footer: true}},
		{24, frame{Header: true, Rule1: true, Rows: 8, Rule2: true, Region: 12, Footer: true}},
		// The real popup floor: a 64x19 interior on an 80x24 terminal.
		{19, frame{Header: true, Rule1: true, Rows: 8, Rule2: true, Region: 7, Footer: true}},
		// The panel reaches its floor; the chrome is still whole.
		{15, frame{Header: true, Rule1: true, Rows: 8, Rule2: true, Region: 3, Footer: true}},
		{14, frame{Header: true, Rule1: true, Rows: 8, Region: 3, Footer: true}},
		{13, frame{Rule1: true, Rows: 8, Region: 3, Footer: true}},
		{12, frame{Rows: 8, Region: 3, Footer: true}},
		// The stack starts scrolling: the panel floor outranks the
		// eighth row.
		{11, frame{Rows: 7, Region: 3, Footer: true}},
		{10, frame{Rows: 6, Region: 3, Footer: true}},
		{5, frame{Rows: 1, Region: 3, Footer: true}},
		// Below h = 5 the panel gives up its floor a row at a time and
		// the footer is the last line standing.
		{4, frame{Rows: 1, Region: 2, Footer: true}},
		{3, frame{Rows: 1, Region: 1, Footer: true}},
		{2, frame{Rows: 1, Footer: true}},
		{1, frame{Footer: true}},
		{0, frame{}},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("h=%d", c.h), func(t *testing.T) {
			if got := layoutFrame(c.h, n); got != c.want {
				t.Errorf("layoutFrame(%d, %d) = %+v, want %+v", c.h, n, got, c.want)
			}
		})
	}
}

// --- layoutFrame: the invariants -------------------------------------------

// layoutRange is the (h, n) grid every invariant below is checked over:
// every height from the degenerate up through a tall terminal, against
// section counts from none at all to more than any real form has.
func layoutRange(fn func(h, n int, f frame)) {
	for n := 0; n <= 14; n++ {
		for h := 1; h <= 60; h++ {
			fn(h, n, layoutFrame(h, n))
		}
	}
}

// TestLayoutFrame_ComponentsSumToTheHeight is the invariant everything
// else rests on: the six components account for every row of the window,
// with nothing left over and nothing double-spent. It is what lets
// composeRows emit its lines with no degradation ladder behind it -- if
// this held only approximately, the footer would drift off the bottom of
// short windows.
func TestLayoutFrame_ComponentsSumToTheHeight(t *testing.T) {
	layoutRange(func(h, n int, f frame) {
		if got := f.lines(); got != h {
			t.Fatalf("layoutFrame(%d, %d) = %+v sums to %d lines, want %d", h, n, f, got, h)
		}
	})
}

// TestLayoutFrame_FooterSurvivesEveryHeight pins v2 spec §9's one
// absolute: "the footer and its buttons are never dropped." Create lives
// on that line, so this is also what replaces v1's "never clip the last
// line" contract.
func TestLayoutFrame_FooterSurvivesEveryHeight(t *testing.T) {
	layoutRange(func(h, n int, f frame) {
		if !f.Footer {
			t.Fatalf("layoutFrame(%d, %d) dropped the footer", h, n)
		}
	})
	if layoutFrame(1, 8) != (frame{Footer: true}) {
		t.Fatalf("at h = 1 the footer must be the only line standing, got %+v", layoutFrame(1, 8))
	}
}

// TestLayoutFrame_WholeStackAboveTheFloor pins where the stack stops
// being whole. Every row is drawn as long as the rows, the panel floor
// and the footer all fit -- h >= n + panelFloor + 1 -- and never more
// rows than there are sections.
//
// NOTE, flagged for review: the issue's test plan asks for "Rows == n for
// every h >= n + 1". That is the SUPERSEDED ladder's floor, in which the
// panel and its rule were dropped as a unit before the stack scrolled.
// The correction that the issue itself closes with -- spec §9's "panel
// content never falls below three rows" -- ranks the panel floor above
// the stack's completeness, which moves the floor to n + 4 and is what
// its own worked table shows (n = 8: eight rows at h = 12, seven at
// h = 11). n + 4 is asserted here; h >= n + 1 would fail against the
// table the same issue supplies.
func TestLayoutFrame_WholeStackAboveTheFloor(t *testing.T) {
	layoutRange(func(h, n int, f frame) {
		switch {
		case h >= n+panelFloor+1:
			if f.Rows != n {
				t.Fatalf("layoutFrame(%d, %d).Rows = %d, want the whole stack (%d)", h, n, f.Rows, n)
			}
		case f.Rows > n:
			t.Fatalf("layoutFrame(%d, %d).Rows = %d, want at most %d", h, n, f.Rows, n)
		}
	})
}

// TestLayoutFrame_PanelFloor pins the other half of the correction: v2
// spec §9's "panel content never falls below three rows while a picker
// is focused". The region is only allowed under three rows when the
// window is too short for three rows plus a row and a footer at all --
// h < 5 -- and it is never negative.
func TestLayoutFrame_PanelFloor(t *testing.T) {
	layoutRange(func(h, n int, f frame) {
		if f.Region < 0 {
			t.Fatalf("layoutFrame(%d, %d).Region = %d, want >= 0", h, n, f.Region)
		}
		if h >= panelFloor+2 && f.Region < panelFloor {
			t.Fatalf("layoutFrame(%d, %d).Region = %d, want >= the %d-row floor", h, n, f.Region, panelFloor)
		}
		// The issue's own phrasing of the same property.
		if f.Region > 0 && h >= n+4 && f.Region < panelFloor {
			t.Fatalf("layoutFrame(%d, %d).Region = %d, want >= %d", h, n, f.Region, panelFloor)
		}
	})
}

// TestLayoutFrame_SlackLandsInTheRegion pins the property that makes
// "rows never move" possible at all: every row a window has spare goes
// to the panel and to nothing above it. Chrome is a fixed cost, the
// stack is a fixed cost, and the region absorbs the difference -- so the
// y of every line above the panel is a function of (h, n) alone, never
// of which field holds focus or of how much that field has to show.
func TestLayoutFrame_SlackLandsInTheRegion(t *testing.T) {
	for n := 0; n <= 12; n++ {
		full := layoutFrame(n+panelFloor+4, n) // every component afforded
		for h := n + panelFloor + 4; h <= 80; h++ {
			f := layoutFrame(h, n)
			if f.Header != full.Header || f.Rule1 != full.Rule1 || f.Rule2 != full.Rule2 || f.Rows != full.Rows {
				t.Fatalf("layoutFrame(%d, %d) = %+v: everything above the panel must match %+v once the frame is whole", h, n, f, full)
			}
			if want := h - (n + 4); f.Region != want {
				t.Fatalf("layoutFrame(%d, %d).Region = %d, want %d (all slack)", h, n, f.Region, want)
			}
		}
	}
}

// TestLayoutFrame_IsMonotone is the property the superseded ladder
// violated, and the reason it is worth its own test: as the window
// shrinks, no component may GROW -- and so certainly none may vanish and
// then reappear. The old ladder made the header disappear at h = 11 and
// come back at h = 10, because it ranked a two-row panel-plus-rule unit
// above the header and that unit became unaffordable one row further
// down. A reader watching a terminal shrink would see chrome flicker
// back into existence.
func TestLayoutFrame_IsMonotone(t *testing.T) {
	boolToInt := func(b bool) int {
		if b {
			return 1
		}
		return 0
	}
	for n := 0; n <= 14; n++ {
		for h := 1; h < 60; h++ {
			lo, hi := layoutFrame(h, n), layoutFrame(h+1, n)
			components := []struct {
				name   string
				lo, hi int
			}{
				{"Header", boolToInt(lo.Header), boolToInt(hi.Header)},
				{"Rule1", boolToInt(lo.Rule1), boolToInt(hi.Rule1)},
				{"Rule2", boolToInt(lo.Rule2), boolToInt(hi.Rule2)},
				{"Rows", lo.Rows, hi.Rows},
				{"Region", lo.Region, hi.Region},
				{"Footer", boolToInt(lo.Footer), boolToInt(hi.Footer)},
			}
			for _, c := range components {
				if c.lo > c.hi {
					t.Fatalf("%s grew as the window shrank: layoutFrame(%d, %d).%s = %d but layoutFrame(%d, %d).%s = %d",
						c.name, h, n, c.name, c.lo, h+1, n, c.name, c.hi)
				}
			}
		}
	}
}

// TestLayoutFrame_DropOrder pins v2 spec §9's order itself: rule 2 goes
// first, then the header, then rule 1. Stated as implications, so it
// holds at every (h, n) rather than only at the sampled heights of the
// worked table.
func TestLayoutFrame_DropOrder(t *testing.T) {
	layoutRange(func(h, n int, f frame) {
		if f.Rule2 && !f.Header {
			t.Fatalf("layoutFrame(%d, %d) = %+v kept rule 2 without the header (spec §9 drops rule 2 FIRST)", h, n, f)
		}
		if f.Header && !f.Rule1 {
			t.Fatalf("layoutFrame(%d, %d) = %+v kept the header without rule 1 (spec §9 drops the header BEFORE rule 1)", h, n, f)
		}
		if f.Rule1 && f.Region < panelFloor {
			t.Fatalf("layoutFrame(%d, %d) = %+v kept chrome while the panel was below its floor", h, n, f)
		}
	})
}

// --- stackWindow -----------------------------------------------------------

// TestStackWindow_KeepsTheFocusedRowVisible pins v2 spec §9's last
// degradation step. The window never runs off either end of the stack,
// and the focused row is inside it at every height -- including the
// pathological ones where only a single row fits.
func TestStackWindow_KeepsTheFocusedRowVisible(t *testing.T) {
	const total = 8
	for visible := 1; visible <= total; visible++ {
		for focus := 0; focus < total; focus++ {
			start := stackWindow(total, visible, focus)
			if start < 0 || start+visible > total {
				t.Fatalf("stackWindow(%d, %d, %d) = %d: window [%d,%d) runs off the stack", total, visible, focus, start, start, start+visible)
			}
			if focus < start || focus >= start+visible {
				t.Fatalf("stackWindow(%d, %d, %d) = %d: the focused row is not inside [%d,%d)", total, visible, focus, start, start, start+visible)
			}
		}
	}
	if got := stackWindow(3, 8, 2); got != 0 {
		t.Errorf("stackWindow with more room than rows = %d, want 0", got)
	}
	// The focus index the ring reports can be the Create section's, one
	// past the last stack row; the window must still be well formed.
	if got := stackWindow(8, 3, 8); got != 5 {
		t.Errorf("stackWindow(8, 3, 8) = %d, want 5 (clamped to the stack's end)", got)
	}
}

// --- contentBox / labelCol -------------------------------------------------

// TestContentBox_FitsAndCentersAndCaps pins v2 spec §7's width cap and
// centering: the box never overruns the window, is capped at
// maxContentWidth however wide the terminal gets, and the slack that cap
// leaves is split between the two margins rather than all falling on one
// side.
func TestContentBox_FitsAndCentersAndCaps(t *testing.T) {
	for w := 1; w <= 240; w++ {
		padLeft, inner := contentBox(w)
		if padLeft < 0 {
			t.Fatalf("contentBox(%d) padLeft = %d, want >= 0", w, padLeft)
		}
		if inner < 1 {
			t.Fatalf("contentBox(%d) inner = %d, want >= 1", w, inner)
		}
		if padLeft+inner > w {
			t.Fatalf("contentBox(%d) = (%d, %d): the box overruns the window", w, padLeft, inner)
		}
		if inner > maxContentWidth {
			t.Fatalf("contentBox(%d) inner = %d, want at most the %d cap", w, inner, maxContentWidth)
		}
		// The whole line, gutter and right margin included, still fits.
		if w > gutterWidth+rightMargin+1 && padLeft+gutterWidth+inner+rightMargin > w {
			t.Fatalf("contentBox(%d) = (%d, %d): gutter + box + right margin overruns the window", w, padLeft, inner)
		}
	}

	// The two sizes the design is argued from: the popup floor uses the
	// full width, a wide terminal centers a capped box.
	if padLeft, inner := contentBox(64); padLeft != 0 || inner != 61 {
		t.Errorf("contentBox(64) = (%d, %d), want (0, 61)", padLeft, inner)
	}
	if padLeft, inner := contentBox(120); padLeft != 14 || inner != maxContentWidth {
		t.Errorf("contentBox(120) = (%d, %d), want (14, %d)", padLeft, inner, maxContentWidth)
	}

	// Centering: past the cap, the left pad grows with the window.
	prev, _ := contentBox(100)
	for w := 101; w <= 200; w++ {
		padLeft, _ := contentBox(w)
		if padLeft < prev {
			t.Fatalf("contentBox(%d) padLeft = %d shrank below contentBox(%d)'s %d", w, padLeft, w-1, prev)
		}
		prev = padLeft
	}
}

// TestLabelCol_LabelShrinksFirst pins which column pays for a narrow
// window: the label gives up cells, down to nothing, before the value
// column is allowed below minValueWidth. A row whose value is gone says
// nothing at all; a row whose label is clipped still shows the value in
// the position the eye has learned.
func TestLabelCol_LabelShrinksFirst(t *testing.T) {
	for inner := 1; inner <= 200; inner++ {
		label, value := labelCol(inner)
		if label < 0 || value < 0 {
			t.Fatalf("labelCol(%d) = (%d, %d), want both >= 0", inner, label, value)
		}
		if label+value != inner {
			t.Fatalf("labelCol(%d) = (%d, %d): the two columns must fill the box exactly", inner, label, value)
		}
		if label > labelColWidth {
			t.Fatalf("labelCol(%d) label = %d, want at most %d", inner, label, labelColWidth)
		}
		switch {
		case inner >= labelColWidth+minValueWidth:
			if label != labelColWidth {
				t.Fatalf("labelCol(%d) label = %d, want the full %d", inner, label, labelColWidth)
			}
		case inner > minValueWidth:
			if value != minValueWidth {
				t.Fatalf("labelCol(%d) = (%d, %d), want the value held at %d while the label shrinks", inner, label, value, minValueWidth)
			}
		default:
			if label != 0 {
				t.Fatalf("labelCol(%d) label = %d, want 0 (a box this narrow is all value)", inner, label)
			}
		}
	}

	if label, value := labelCol(61); label != 11 || value != 50 {
		t.Errorf("labelCol(61) = (%d, %d), want (11, 50) -- the 64-column popup floor", label, value)
	}
	if label, value := labelCol(maxContentWidth); label != 11 || value != 77 {
		t.Errorf("labelCol(%d) = (%d, %d), want (11, 77) -- a wide terminal's capped box", maxContentWidth, label, value)
	}
}
