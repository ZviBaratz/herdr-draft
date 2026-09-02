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
	// panelCapRows is the tallest the panel region is allowed to grow
	// (v3 spec §7.2). Every row a window has spare beyond it becomes
	// margin above and below the card instead of more blank panel.
	//
	// DERIVED FROM THE MANIFEST, and re-derive it if that changes:
	// herdr-plugin.toml asks for a 32-row popup, which hands this form 30
	// rows; the eight stack rows and the six chrome lines (header, three
	// rules, the footer -- and the one row of inset each end) leave
	// sixteen, and holding one back top and bottom as the card's inset
	// gives the panel fifteen.
	//
	// Two things it is deliberately NOT, both of which an earlier draft
	// got wrong:
	//
	//   - Not max(PanelRows()). That is issuePanelMaxRows, 24, and the
	//     natural region at h = 30 is 17, so a cap of 24 would never bind
	//     -- the whole change would be a no-op at the only size that
	//     ships.
	//   - Not a parameter to layoutFrame. PromptField.PanelRows() grows
	//     with the text you type and IssueField.PanelRows() shrinks as you
	//     filter, so any cap derived from the focused field's appetite is
	//     data-dependent, and the footer would jump while the user types.
	//     A constant cannot do that.
	//
	// Only the two widgets that window their own content are clipped by
	// it: issue (24) and prompt (20). Every other field's panel fits
	// whole.
	panelCapRows = 15
)

// contentBox returns the left padding and the inner (label+value) width
// of the content box in a popup w columns wide.
//
// The box is laid out as
//
//	sideMargin | gutterWidth | inner (= labelCol + valueCol) | sideMargin
//
// and it FILLS the pane: `padLeft + gutterWidth + inner + sideMargin == w`
// exactly, at every width above the degenerate ones (v3 spec §6.2).
//
// v2 capped inner at 88 and centered the slack, on the reasoning that "a
// 200-column terminal does not stretch rows into ribbons". v3 spec §6.2
// deletes the cap for two reasons. The pane width is now fixed by our own
// manifest (v3 spec §6.1, 101 columns), so any cap is either dead code at
// the one size that ships or reintroduces dead columns there; and the
// ribbon worry assumed a naked line of text, where a focused row is now a
// full-width band with its text at the left (v3 spec §5.4). The surface
// that genuinely wanted the columns was the panel, and the cap was
// costing it: at 190 columns it truncated issue titles mid-word with
// eighty columns empty on either side.
//
// The margin is SYMMETRIC now. v2 held back a rightMargin only, so the
// header, both rules and the footer -- which are drawn at the full box
// width, gutter included -- sat flush against herdr's own `│` popup
// glyph on the left.
//
// The gutterWidth column is a MARKER column again: v2 emptied it when it
// replaced the `▎` focus bar with a full-width ActiveRowBG fill, and v3
// spec §5.4 puts a bar back in it because that fill turned out to be
// invisible (rowvalues.go's focusBarGlyph has the whole reversal). It is
// not a margin -- it is the row stack's indent and the panel's cursor
// column (rowvalues.go's panelGutter), which is why it stays two cells
// wide while the margins around it change.
//
// Degenerate widths degrade rather than panic (the same contract
// widgets/picker.go's widthStyle documents), and the branch below is
// shaped by one requirement: inner must be NONDECREASING in w. Shedding
// the two margins one at a time as the window narrows would make inner
// jump from 2 at w=4 to 1 at w=5 -- wider window, narrower box -- so the
// degenerate case drops both margins and the content floor together, in
// one step.
func contentBox(w int) (padLeft, inner int) {
	inner = w - gutterWidth - 2*sideMargin
	if inner < 1 {
		return 0, 1
	}
	return sideMargin, inner
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

// frame is the vertical layout of one render: which of v3 spec §7.1's
// components this window height affords, and how many lines the variable
// ones (the row stack, the panel region and the two pads) get. Its
// components always sum to exactly the height layoutFrame was asked for.
//
// v2 had six components and NO blank spacer rows at all: §9's fixed cost
// was "header, rule, rows, rule, panel, footer", and reserved blank rows
// were half of the visual defect v2 existed to remove (v1's own
// assembled-full-80x24 frame spent eight of twenty-four lines on them).
// v3 spec §7 adds PadTop/PadBottom, which look like the same thing and
// are not: v1's spacers were reserved AHEAD of the content, so a short
// window paid for them out of the panel; these are the LEFTOVER of a
// window too tall for the card, which v2 spent on yet more blank panel
// (`rowlayout.go:252-253`). The rows they occupy were blank either way --
// the change is only where the blank sits, and therefore whether the
// footer is nailed to the bottom edge of the pane or floats one row above
// it.
//
// The pads are (h, n)-determined and so focus-independent, which is what
// keeps them legal. `Region - PanelRows(focused)` is the OTHER kind of
// leftover (v3 spec §7's opening paragraph): it cannot become margin
// without moving the footer as focus travels, and it is not addressed
// here.
type frame struct {
	// PadTop is the blank margin above the header: the taller half of
	// whatever a window has spare once the region has reached
	// panelCapRows. Nonzero only when Region == panelCapRows.
	PadTop int
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
	// Region is the panel's height, at most panelCapRows. 0 means the
	// window afforded no panel at all -- only reachable below h == 5.
	Region int
	// Rule3 is the rule BELOW the panel, closing the card (v3 spec §7.4).
	// Last in the survival ladder: it goes before rule 2, because rule 2
	// separates two kinds of content while rule 3 only closes the card.
	Rule3 bool
	// Footer is the key ladder plus the Create button. Never dropped.
	Footer bool
	// PadBottom is the blank margin below the footer: the shorter half of
	// the same remainder. Nonzero only when Region == panelCapRows.
	PadBottom int
}

// lines is the frame's total height: the invariant every layoutFrame
// result satisfies is lines() == h.
func (f frame) lines() int {
	n := f.Rows + f.Region + f.PadTop + f.PadBottom
	for _, present := range []bool{f.Header, f.Rule1, f.Rule2, f.Rule3, f.Footer} {
		if present {
			n++
		}
	}
	return n
}

// layoutFrame lays out a popup h rows tall holding a stack of n rows.
//
// v2 spec §9 gave a DROP order ("panel shrinks to three rows, drop the
// second rule, drop the header, drop the first rule, scroll the row
// stack"); read backwards it is a survival priority, highest first, in
// which each item is kept only once every higher-priority item is
// already afforded. v3 spec §7.1 keeps that order verbatim and appends
// three rungs -- rule 3, then the CAPPED region, then the margin:
//
//  1. the footer -- never dropped, and it carries the Create button
//  2. the n stack rows -- scrolled, focused row kept visible, only
//     below the floor
//  3. the panel's first panelFloor rows
//  4. rule 1, under the header
//  5. the header
//  6. rule 2, above the panel
//  7. rule 3, below the panel                              (v3 §7.4)
//  8. the region, grown to at most panelCapRows            (v3 §7.2)
//  9. the remainder: PadBottom = rem/2, PadTop = rem-rem/2 (v3 §7.1)
//
// Rule 1 outranks the header because §9 drops rule 2 first, THEN the
// header, and only then rule 1. Rule 3 is ranked last of the three
// because rule 2 separates two kinds of content while rule 3 only closes
// the card.
//
// Steps 8 and 9 are the load-bearing pair, and they are what v2's single
// "everything left over grows Region" became. Slack still lands nowhere
// above the panel, so the y of every line from the header down is a
// function of (h, n) alone and never of which field holds focus -- that
// is what makes "row i is always at row i" true while focus travels, and
// it is why a focused field whose PanelRows() is smaller than Region does
// not shrink the region: compose calls Panel(w, min(PanelRows(), Region))
// and blank-fills the remainder itself. What changed is that beyond
// panelCapRows the slack stops being blank PANEL and becomes blank
// MARGIN, split between the two ends, so a tall window shows a card
// rather than a footer nailed to the bottom edge under twenty-two empty
// rows.
//
// Worked numbers for n = 8, v3 spec §7.3's table verbatim:
//
//	h  | pad | hdr | r1 | rows | r2 | region | r3 | ftr | pad
//	40 |  6  |  1  |  1 |   8  |  1 |   15   |  1 |  1  |  6
//	30 |  1  |  1  |  1 |   8  |  1 |   15   |  1 |  1  |  1   <- the popup
//	29 |  1  |  1  |  1 |   8  |  1 |   15   |  1 |  1  |  0
//	28 |  0  |  1  |  1 |   8  |  1 |   15   |  1 |  1  |  0   <- cap binds
//	22 |  0  |  1  |  1 |   8  |  1 |    9   |  1 |  1  |  0   <- 80x24
//	19 |  0  |  1  |  1 |   8  |  1 |    6   |  1 |  1  |  0
//	16 |  0  |  1  |  1 |   8  |  1 |    3   |  1 |  1  |  0   <- frame whole
//	15 |  0  |  1  |  1 |   8  |  1 |    3   |  0 |  1  |  0   <- rule 3 gone
//	14 |  0  |  0  |  1 |   8  |  0 |    3   |  0 |  1  |  0   <- rule 2 gone
//	13 |  0  |  0  |  1 |   8  |  0 |    3   |  0 |  1  |  0   <- header gone
//	12 |  0  |  0  |  0 |   8  |  0 |    3   |  0 |  1  |  0   <- rule 1 gone
//	11 |  0  |  0  |  0 |   7  |  0 |    3   |  0 |  1  |  0   <- stack scrolls
//	 5 |  0  |  0  |  0 |   1  |  0 |    3   |  0 |  1  |  0   <- focused row
//
// At n = 8 everything from h = 15 down is byte-identical to v2: the whole
// change lives at h >= 16, where rule 3 becomes the first thing a taller
// window buys. (That is a property of THIS n, not of the ladder -- a
// shorter stack reaches h >= 16's territory sooner, and n = 0 first
// differs at h = 8.)
//
// Below h == 5 the panel gives up its floor one row at a time (3, 2, 1,
// 0) and the footer is the last line standing. The whole ladder is
// MONOTONE: no component ever grows -- and so certainly never reappears
// -- as h decreases. An earlier draft of this ladder paired the panel
// and its rule as one two-row unit ranked above the header, which made
// the header vanish at h = 11 and come BACK at h = 10; that is the
// specific bug this shape exists to avoid, and rowlayout_test.go pins
// its absence directly. Region survives that test because it is a min of
// a nondecreasing quantity against a constant, and both pads because
// they are halves of a nondecreasing remainder.
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
	// rule 2, then rule 3.
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
	if rem > 0 {
		f.Rule3 = true
		rem--
	}

	// The panel grows next, but only to the cap: past that the slack is
	// the card's margin rather than more empty panel (v3 spec §7.2).
	grow := panelCapRows - region
	if grow > rem {
		grow = rem
	}
	if grow < 0 {
		grow = 0
	}
	f.Region = region + grow
	rem -= grow

	// Whatever is still unspent is margin, the taller half on top: the
	// card reads better sitting slightly high, and it makes the split
	// deterministic on an odd remainder.
	f.PadBottom = rem / 2
	f.PadTop = rem - f.PadBottom
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
