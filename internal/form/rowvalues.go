// rowvalues.go holds the rendering helpers Section's Label/Row/Panel/
// PanelRows share across field_*.go -- the elision rule for a one-line
// value cell, and the two-cell gutter every panel line is composed
// into.
//
// It is the row-stack counterpart of layout.go's width-and-height
// primitives, kept in its own file because the two are used at different
// levels: layout.go fits a line or a block to a size, rowvalues.go decides
// what a row or a panel line SAYS at that size.
//
// Written fresh for v2; nothing here is derived from atrium
// (github.com/ZviBaratz/atrium), whose own overlay has neither a
// one-line-per-field row stack nor a single shared panel region.
package form

import (
	"math"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/ZviBaratz/herdr-draft/internal/form/widgets"
	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// rowValueNone is the em dash v2 spec §6's table uses for a cell with
// nothing in it -- the prompt's unset row, the agent's, and the
// worktree panel's branch and base parts while there is no worktree to
// name one for. One spelling, in one place, for the same idea.
const rowValueNone = "—"

// unavailableReasonSep joins a state word to the reason for it:
// `unavailable  no API key`, `remove unavailable  uncommitted changes`.
// Two spaces, no dash -- v2 spec §6's table and §6.1's worked example
// both spell it that way, and it is the whole reason this lives in one
// place rather than in each field: the form's own row and the submit
// screen's own explanation each had a spelling, and they were not the
// same one.
const unavailableReasonSep = "  "

// rowEllipsis marks an elided row value. sizes.go's own file doc records
// the v1 decision this reverses and why: v1 clipped silently because
// running out of room on a HINT line costs the reader a suggestion they
// can live without, while a one-line VALUE cell that loses an end
// unmarked is not incomplete but MISREAD -- "~/Projects/herdr-dra" and
// "zvi/fix-login-redir" both read as real values.
const rowEllipsis = widgets.Ellipsis

// keepHead clips s to exactly width cells KEEPING ITS HEAD, marking the
// cut with rowEllipsis: the rule for titles, branches, prose and
// identifiers, whose informative end is the one you read first (v2 spec
// §7, restated in sizes.go's file doc).
//
// The body moved to widgets.KeepHead for v3 spec §8.1, alongside
// keepTail below and for the same reason paintLine's body moved: a
// PickerColumn carries an ElideMode, so the picker applies these two
// rules to its own cells, and widgets cannot import form. This is now
// the form-side name for it, kept because the form's own call sites all
// read keepHead.
func keepHead(s string, width int) string { return widgets.KeepHead(s, width) }

// keepTail clips s to exactly width cells KEEPING ITS TAIL, marking the
// cut with a leading rowEllipsis: the rule for PATHS, where the last
// segments are what distinguish "~/Projects/herdr" from
// "~/Projects/herdr-draft" and the shared prefix is what everything on
// screen already has in common.
//
// Delegates to widgets.KeepTail -- see keepHead above.
func keepTail(s string, width int) string { return widgets.KeepTail(s, width) }

// gaugeWidth is how many cells a utilization gauge occupies (v3 spec
// §8.6): ten, enough for a reader to judge a fraction at a glance and few
// enough that the account panel can afford one per usage window beside
// the profile name, the plan and the reset time.
const gaugeWidth = 10

const (
	// gaugeFilledBlock and gaugeEmptyBlock are the gauge's only two runes.
	// WHOLE blocks, v3 spec §8.6: the eighth-block partials a smoother bar
	// would use render inconsistently across fonts and buy nothing at ten
	// cells.
	gaugeFilledBlock = "█"
	gaugeEmptyBlock  = "░"
)

// gaugeBar renders fraction as exactly width cells of filled and empty
// blocks (v3 spec §8.6). fraction is CLAMPED into 0..1 rather than
// trusted -- it is derived from a utilization percentage clauth's own
// unvalidated feed supplies, so a value past 100 (or a NaN) is a shape
// this has to survive rather than one it can rule out. width < 1 renders
// "", the same degenerate-size contract every other helper in this file
// keeps.
//
// The filled count is ROUNDED rather than truncated, and the two rules
// disagree on real data: v3 spec §10.2's own mockup draws 57% as five
// blocks (truncation) and 98% as ten (rounding), so it cannot be read as
// a specification of either. Rounding is chosen because the alternative
// leaves a profile at 98% one block short of full, and a bar that will
// not fill until 100% says least exactly where it matters most -- past
// field_account.go's accountWarnThreshold, where the full bar and the
// warning word land together.
//
// It returns PLAIN text, deliberately: the picker tones, pads and elides
// it like any other cell, and §8.6 colors the WORD beside a gauge rather
// than the gauge itself -- a themed bar next to a themed badge is two
// things shouting once.
func gaugeBar(fraction float64, width int) string {
	if width < 1 {
		return ""
	}
	switch {
	case math.IsNaN(fraction), fraction < 0:
		fraction = 0
	case fraction > 1:
		fraction = 1
	}
	filled := int(math.Round(fraction * float64(width)))
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}
	return strings.Repeat(gaugeFilledBlock, filled) + strings.Repeat(gaugeEmptyBlock, width-filled)
}

// panelCursorGlyph is the marker v2 spec §4's mockups draw beside the
// selected row of a focused panel's list. It lands in the panel's own
// two-cell gutter -- the same column the row stack indents past -- which
// is why Panel is handed gutterWidth+inner rather than the value column
// alone (form.go's rowSection.Panel).
const panelCursorGlyph = "▸"

// focusBarGlyph is the accent edge marking the focused STACK row (v3 spec
// §5.4). It lands in the row's own copy of that same two-cell gutter, so
// the bar and a panel's cursor glyph sit in one column.
//
// This REVERSES v2 deliberately, and the reason is the point of recording
// it here rather than in a commit message. v2 removed v1's `▎` bar
// because the full-width ActiveRowBG fill was going to replace it -- and
// the fill turned out to be invisible, 1.07:1 against its own panel on
// the default palette, which is the defect v3 spec §5.3 repaired. Even at
// the repaired 1.40:1 a fill alone is a weak cursor, so the focused row
// now carries THREE simultaneous signals (the fill, this bar, and bold on
// the value) instead of one. That is herdr's own grammar, which marks a
// selected row with four at once -- marker glyph, brighter foreground,
// surface fill and bold (`herdr:src/ui/dialogs.rs:487-500`).
//
// The bar costs nothing: v2 deleted the glyph but kept the two-cell
// gutterWidth it used to live in (sizes.go), so no column moves and no
// row shifts.
//
// `▌` rather than v1's `▎`: a half block reads as the leading edge of the
// fill it stands against, where a quarter block reads as a hairline
// beside it.
const focusBarGlyph = "▌"

// rowGutter returns a stack row's two-cell gutter: the accent focus bar
// plus a space when focused, two blanks otherwise. Always exactly
// gutterWidth cells wide -- the row-stack counterpart of panelGutter
// above, and deliberately the same shape, because the two glyphs share a
// column.
func rowGutter(focused bool, p theme.Palette) string {
	if !focused {
		return strings.Repeat(" ", gutterWidth)
	}
	return lipgloss.NewStyle().Foreground(p.Accent).Render(focusBarGlyph) + " "
}

// panelGutter returns a panel line's two-cell gutter: the accent-colored
// cursor glyph plus a space when cursor is true, two blanks otherwise.
// Always exactly gutterWidth cells wide.
func panelGutter(cursor bool, p theme.Palette) string {
	if !cursor {
		return strings.Repeat(" ", gutterWidth)
	}
	return lipgloss.NewStyle().Foreground(p.Accent).Render(panelCursorGlyph) + " "
}

// panelInner is the width left for a panel line's content once the gutter
// is paid for, floored at 1.
func panelInner(w int) int {
	if inner := w - gutterWidth; inner > 0 {
		return inner
	}
	return 1
}

// panelText composes one plain (never zone-marked) panel line: the blank
// gutter, then content fitted to the remaining width.
func panelText(content string, w int) string {
	return strings.Repeat(" ", gutterWidth) + fitLine(content, panelInner(w))
}

// panelStatusLine composes a panel's LAST line, the one v3 spec §8.5
// spends on the filter count: the field's own status message on the left
// and the count flush right, in the same two-cell-gutter column every
// other panel line is composed into.
//
// msg arrives already styled, because each field's status has its own
// tone (AccountField's live verdict is Danger, everyone else's is a dim
// hint) -- and spreadLine measures rendered width, so a pre-styled left
// half costs nothing here. The COUNT is styled in this one place instead:
// it means the same thing on all three lines that carry it, and a field
// choosing its own color for it is a field that could get it wrong.
//
// The count is DROPPED WHOLE rather than squeezed when the two halves
// cannot both fit with statusCountGap between them -- the same judgement
// widgets.Picker's layout makes for a badge, and for the same reason: a
// count that has eaten the sentence beside it costs more than it says.
// The 44-cell account panel is where this actually bites, since
// spreadLine truncates its left half flush against the right one with no
// gap at all, giving `use whatever profile i3 profiles`.
//
// With no count -- because there is none, or because it did not fit -- it
// delegates to panelText rather than composing an equivalent line, so the
// panels of the fields that never show one are byte-identical by
// construction and not merely on inspection (fitLine is a lipgloss
// Render, so applying it twice to already-styled text is not obviously a
// no-op). rowvalues_test.go pins the equivalence anyway.
func panelStatusLine(msg, count string, w int, p theme.Palette) string {
	inner := panelInner(w)
	if count == "" || lipgloss.Width(msg)+statusCountGap+lipgloss.Width(count) > inner {
		return panelText(msg, w)
	}
	return strings.Repeat(" ", gutterWidth) + spreadLine(msg, dimHint(p).Render(count), inner)
}

// statusCountGap is the least air v3 spec §8.5's filter count keeps
// between itself and the message it shares a line with -- the same two
// cells widgets.Picker puts between two columns of a panel row.
const statusCountGap = 2

// filterCount is v3 spec §8.5's readout: `24 issues` with nothing
// filtered out and `3/24 issues` otherwise. It switches on shown != total
// rather than on herdr's own "is a query set" -- one less piece of state,
// and it reads identically.
//
// The noun is the FIELD's own copy, the way issuePanelEmpty is: a list of
// issues and a list of directories are counted in different words, and
// the field is the only thing that knows which.
//
// It takes BOTH grammatical numbers, which §8.5's sketched signature
// (one `noun string`) does not. A one-word version renders `1 issues` for
// a user with exactly one assigned issue -- an ordinary state, not a
// corner -- and the ratio form cannot cover it, since the noun there
// agrees with the total, not the match count. Two constants per field is
// the cheapest thing that is never wrong.
//
// An EMPTY list counts nothing at all. The left half of this very line
// already says so in the field's own terms (`no assigned issues`), and
// `0 issues` beside it is the same fact twice, in the less useful order.
//
// The full-set branch tests shown >= total rather than ==. No caller can
// reach the inequality today -- each one subtracts the rows its picker
// holds that are not things to count (IssueField's `none` sentinel,
// DirField's literal typed path) before calling -- and the point is that
// if one ever stops, the line degrades to a plain count instead of
// rendering an upside-down ratio like `4/3 issues`.
func filterCount(shown, total int, singular, plural string) string {
	if total < 1 {
		return ""
	}
	if shown >= total {
		return countOf(shown, singular, plural)
	}
	return strconv.Itoa(shown) + "/" + countOf(total, singular, plural)
}

// countOf is `1 issue` / `24 issues`.
func countOf(n int, singular, plural string) string {
	noun := plural
	if n == 1 {
		noun = singular
	}
	return strconv.Itoa(n) + " " + noun
}

// panelMarked composes one panel line whose content the caller has
// ALREADY rendered to the content column's exact width (panelInner) and
// which may carry bubblezone markers -- a
// widgets.Picker or widgets.ChipRow render. It deliberately does not run
// content back through fitLine: every other zone-marked render in this
// package (field_issue.go, field_dir.go, field_agent.go,
// field_placement.go) composes marked widget output by concatenation for
// the same reason, and the frame's own paintLine normalizes the finished
// line's width anyway.
func panelMarked(content string, cursor bool, p theme.Palette) string {
	return panelGutter(cursor, p) + content
}

// panelChipRow composes one panel line whose content is a
// widgets.ChipRow render, already fitted to panelChipWidth(w).
//
// It pays for only ONE of the gutter's two cells and lets the chip row's
// own leading space stand in for the other: ChipRow renders every chip as
// " label ", so a chip row composed through panelMarked like any other
// content sits one column right of the picker rows and part labels
// beside it. One cell is a small thing to be wrong about and a very
// visible one in a panel whose whole job is an aligned column.
//
// An INERT chip row has no such padding (widgets.ChipRow renders its
// placeholder bare), so a caller in that state must use panelMarked
// instead -- see field_worktree.go's Panel, which switches on exactly
// that.
func panelChipRow(content string) string {
	return strings.Repeat(" ", gutterWidth-1) + content
}

// panelChipWidth is the width a chip row is rendered at to land in the
// column panelChipRow then places it in.
func panelChipWidth(w int) int { return panelInner(w) + 1 }

// panelPickerLines renders pk into exactly h panel lines w cells wide:
// the picker itself in the content column, its cursor row marked with
// panelCursorGlyph in the gutter. zonePrefix is passed straight through
// to widgets.Picker.MarkedView, so a click inside the panel still
// resolves to a row (form.go's zonePanel forwards it to the focused
// section).
func panelPickerLines(pk *widgets.Picker, w, h int, zonePrefix string, p theme.Palette) []string {
	if h < 1 {
		h = 1
	}
	cursor := pk.CursorRow(h)
	rendered := strings.Split(pk.MarkedView(panelInner(w), h, zonePrefix), "\n")
	lines := make([]string, h)
	for i := range lines {
		content := ""
		if i < len(rendered) {
			content = rendered[i]
		}
		lines[i] = panelMarked(content, i == cursor, p)
	}
	return lines
}

// panelBlock joins already-composed panel lines into Panel's own
// exactly-h-lines contract, padding with blank gutter rows and dropping
// any overflow from the bottom.
func panelBlock(w, h int, lines ...string) string {
	if h < 1 {
		h = 1
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, panelText("", w))
	}
	return strings.Join(lines, "\n")
}

// provenanceFrom opens v2 spec §11's provenance line, whose whole wording
// the spec fixes: `from .herdr-draft.toml`. The FILE is data the app layer
// pushes in (a form field knows nothing about config files); the sentence
// around it is copy, and lives here so two fields cannot spell it two ways.
const provenanceFrom = "from "

// provenanceRows is how many panel lines a source name costs: one, or none
// at all when no config file supplied the value. Every PanelRows() that can
// carry provenance adds this rather than a literal 1 -- a field that
// reserved the line unconditionally would leave a permanent blank row in
// the panel of every form that has no repo config, which is the majority.
func provenanceRows(source string) int {
	if source == "" {
		return 0
	}
	return 1
}

// provenanceLine composes the line itself: dim, in the panel's own text
// column, never on the row (v2 spec §11 -- "provenance appears in the
// focused row's panel, not in the row", which is v2 spec §3's rule 1, rows
// stay quiet).
func provenanceLine(source string, w int, p theme.Palette) string {
	return panelText(dimHint(p).Render(provenanceFrom+source), w)
}

// capRows clamps a field's PanelRows() to its own ceiling, never below 1:
// a Section that reports 0 tells the form it has no panel at all, which
// is a different statement from "a small one" (form.go's
// rowSection.PanelRows).
func capRows(want, ceiling int) int {
	if want > ceiling {
		want = ceiling
	}
	if want < 1 {
		want = 1
	}
	return want
}
