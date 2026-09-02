// rowlayout.go is v2's layout arithmetic (v2 spec §5 and §9): the
// horizontal content box -- a width-capped, horizontally centered
// label+value grid -- and the vertical frame -- header, two rules, the
// row stack, the panel region, the footer. It is deliberately PURE: it
// measures, it never renders, it never touches a Section, and it
// performs no I/O, so every number below can be pinned by a table test
// (rowlayout_test.go) rather than inferred from a rendered frame.
//
// This file is written fresh for v2, not derived from atrium
// (github.com/ZviBaratz/atrium): Atrium's textInput_size.go spends one
// shared row budget across a closed enum of variable-height field kinds
// (the shape sizes.go's own deleted allocateHeights ported), which is
// exactly the design v2 replaces -- v2's stack rows are one line each,
// always, and the only variable-height region is the single panel.
//
// contentBox/labelCol are now the package's ONLY horizontal measurement.
// sizes.go used to carry a second one, innerWidth, which measured
// submitview.go alone; the two were deliberately kept apart while v1's
// compose branch existed, because unifying them would have moved the two
// submit golden frames for no reason. Both of those reasons expired
// together: v2 spec §12 requires the submit screen to share the form's
// label column (which is what moved those frames on purpose), and the v1
// branch is gone, so innerWidth lost its last caller and was deleted.
package form

// Horizontal metrics of v2's content box.
const (
	// labelColWidth is the fixed width of the row stack's label column:
	// the widest label v2 spec §6 uses ("placement", 9 cells) plus the
	// two spaces separating it from the value. Every row's label is
	// padded into this column BY THE FORM, never by the section, which
	// is what makes the column aligned by construction rather than by
	// convention (v2 spec §5).
	labelColWidth = 11
	// maxContentWidth caps the content box. v2 spec §7: "width is capped
	// and the content centered horizontally, so a 200-column terminal
	// does not stretch rows into ribbons."
	maxContentWidth = 88
	// minValueWidth is the fewest cells the value column is allowed. On
	// a window too narrow for both columns the LABEL gives up cells
	// first (labelCol below): a row whose value is gone says nothing at
	// all, while a row whose label is clipped still shows the value in
	// the position the eye has already learned.
	minValueWidth = 8
	// panelFloor is v2 spec §9's promise, verbatim: "panel content never
	// falls below three rows while a picker is focused." Three is the
	// useful floor for a picker -- the cursor row plus one neighbour
	// each way.
	panelFloor = 3
)

// contentBox returns the left padding and the inner (label+value) width
// of v2's content box in a popup w columns wide.
//
// The box is laid out as
//
//	padLeft | gutterWidth | inner (= labelCol + valueCol) | right slack
//
// with rightMargin (sizes.go) held back on the right, so `padLeft +
// gutterWidth + inner + rightMargin <= w` always. inner is capped at
// maxContentWidth and the slack that cap leaves over is split evenly
// between the two sides, favouring the left on an odd remainder -- that
// is the "centered" half of v2 spec §7's width cap.
//
// The gutterWidth column is a MARKER column again: v2 emptied it when it
// replaced the `▎` focus bar with a full-width ActiveRowBG fill, and v3
// spec §5.4 puts a bar back in it because that fill turned out to be
// invisible (rowvalues.go's focusBarGlyph has the whole reversal). It
// doubles as the two-cell inset §4's mockups show between the row stack
// or the panel and the full-width header, rules and footer, and a
// picker's own cursor glyph lands in exactly the same cell.
//
// Degenerate widths degrade rather than panic (the same contract
// widgets/picker.go's widthStyle documents): inner is floored at 1 and
// padLeft at 0, so a one-column window still asks for a renderable, if
// useless, box.
func contentBox(w int) (padLeft, inner int) {
	inner = w - gutterWidth - rightMargin
	if inner > maxContentWidth {
		inner = maxContentWidth
	}
	if inner < 1 {
		inner = 1
	}
	padLeft = (w - gutterWidth - rightMargin - inner) / 2
	if padLeft < 0 {
		padLeft = 0
	}
	return padLeft, inner
}

// labelCol splits a content box inner cells wide into its label and
// value columns. The label column is labelColWidth wherever that leaves
// the value at least minValueWidth cells; below that the LABEL shrinks,
// down to nothing, before the value column is touched (see
// minValueWidth's own doc). A box narrower than minValueWidth is all
// value: label == 0, value == inner.
func labelCol(inner int) (label, value int) {
	if inner < 1 {
		return 0, 1
	}
	label = labelColWidth
	if room := inner - minValueWidth; label > room {
		label = room
	}
	if label < 0 {
		label = 0
	}
	return label, inner - label
}

// frame is the vertical layout of one render: which of v2 spec §9's six
// components this window height affords, and how many lines the two
// variable ones (the row stack and the panel region) get. Its components
// always sum to exactly the height layoutFrame was asked for.
//
// There are exactly six components and none of them is a blank spacer
// row: v2 spec §9's fixed cost is "header, rule, rows, rule, panel,
// footer" and every §4 mockup shows no blank chrome at all. Reserved
// blank rows are half of the visual defect v2 exists to remove (v1's own
// assembled-full-80x24 frame spends eight of twenty-four lines on them),
// so they are not reintroduced here as layout.
type frame struct {
	// Header is the form-name/context line at the very top.
	Header bool
	// Rows is how many stack rows are drawn. Below the floor the stack
	// scrolls (compose keeps the focused row visible) rather than
	// shrinking each row.
	Rows int
	// Rule1 is the rule under the header.
	Rule1 bool
	// Rule2 is the rule above the panel.
	Rule2 bool
	// Region is the panel's height. 0 means the window afforded no panel
	// at all -- only reachable below h == 5.
	Region int
	// Footer is the key ladder plus the Create button. Never dropped.
	Footer bool
}

// lines is the frame's total height: the invariant every layoutFrame
// result satisfies is lines() == h.
func (f frame) lines() int {
	n := f.Rows + f.Region
	for _, present := range []bool{f.Header, f.Rule1, f.Rule2, f.Footer} {
		if present {
			n++
		}
	}
	return n
}

// layoutFrame lays out a popup h rows tall holding a stack of n rows.
//
// v2 spec §9 gives a DROP order ("panel shrinks to three rows, drop the
// second rule, drop the header, drop the first rule, scroll the row
// stack"); read backwards it is a survival priority, highest first, in
// which each item is kept only once every higher-priority item is
// already afforded:
//
//  1. the footer -- never dropped, and it carries the Create button
//  2. the n stack rows -- scrolled, focused row kept visible, only
//     below the floor
//  3. the panel's first panelFloor rows
//  4. rule 1, under the header
//  5. the header
//  6. rule 2, above the panel
//  7. every remaining row grows Region
//
// Rule 1 outranks the header because §9 drops rule 2 first, THEN the
// header, and only then rule 1.
//
// Step 7 is the load-bearing one: slack lands in Region and nowhere
// else, so the y of every line above the panel is a function of (h, n)
// alone and never of which field holds focus. That is what makes "row i
// is always at row i" true while focus travels -- and it is why a
// focused field whose PanelRows() is smaller than Region does not shrink
// the region: compose calls Panel(w, min(PanelRows(), Region)) and
// blank-fills the remainder itself, so the footer never moves either.
//
// Worked numbers for n = 8 (header, rule1, rows, rule2, panel, footer):
//
//	h  | hdr | r1 | rows | r2 | panel | ftr
//	40 |  1  |  1 |   8  |  1 |   28  |  1
//	24 |  1  |  1 |   8  |  1 |   12  |  1
//	19 |  1  |  1 |   8  |  1 |    7  |  1   <- the real popup floor, 64x19
//	15 |  1  |  1 |   8  |  1 |    3  |  1   <- panel at its floor
//	14 |  1  |  1 |   8  |  0 |    3  |  1   <- rule 2 gone
//	13 |  0  |  1 |   8  |  0 |    3  |  1   <- header gone
//	12 |  0  |  0 |   8  |  0 |    3  |  1   <- rule 1 gone
//	11 |  0  |  0 |   7  |  0 |    3  |  1   <- the stack starts scrolling
//	 5 |  0  |  0 |   1  |  0 |    3  |  1   <- the focused row only
//
// Below h == 5 the panel gives up its floor one row at a time (3, 2, 1,
// 0) and the footer is the last line standing. The whole ladder is
// MONOTONE: no component ever grows -- and so certainly never reappears
// -- as h decreases. An earlier draft of this ladder paired the panel
// and its rule as one two-row unit ranked above the header, which made
// the header vanish at h = 11 and come BACK at h = 10; that is the
// specific bug this shape exists to avoid, and rowlayout_test.go pins
// its absence directly.
//
// h < 1 returns the zero frame (nothing fits, not even the footer);
// n < 0 is treated as 0.
func layoutFrame(h, n int) frame {
	if h < 1 {
		return frame{}
	}
	if n < 0 {
		n = 0
	}

	f := frame{Footer: true}
	rem := h - 1

	// The stack: every row whenever the panel floor still fits above the
	// footer, never more than n, and never fewer than one row while any
	// line at all is left over.
	rows := rem - panelFloor
	if rows < 1 {
		rows = 1
	}
	if rows > n {
		rows = n
	}
	if rows > rem {
		rows = rem
	}
	f.Rows = rows
	rem -= rows

	// The panel floor.
	region := panelFloor
	if region > rem {
		region = rem
	}
	rem -= region

	// The chrome, in survival order: rule 1, then the header, then
	// rule 2.
	if rem > 0 {
		f.Rule1 = true
		rem--
	}
	if rem > 0 {
		f.Header = true
		rem--
	}
	if rem > 0 {
		f.Rule2 = true
		rem--
	}

	// Everything still unspent grows the panel, never the chrome.
	f.Region = region + rem
	return f
}

// stackWindow returns the index of the first stack row compose draws
// when a window of `visible` rows must show `total` rows and keep the
// one at `focus` visible (v2 spec §9's last degradation step, "scroll
// the row stack, keeping the focused row visible").
//
// The window is derived fresh from (total, visible, focus) on every
// render rather than carried as scroll state: with one line per row
// there is nothing a remembered offset could express that this does not,
// and a derived offset cannot drift out of sync with a section list the
// app layer rebuilt underneath it.
func stackWindow(total, visible, focus int) int {
	if visible >= total || visible < 1 {
		return 0
	}
	start := 0
	if focus >= visible {
		start = focus - visible + 1
	}
	if max := total - visible; start > max {
		start = max
	}
	if start < 0 {
		start = 0
	}
	return start
}
