// Package widgets provides herdr-draft's form UI primitives: a filterable
// single-select list (Picker) and a horizontal single-select chip row
// (ChipRow). Both are pure view + input state -- no I/O, no owned
// bubbletea.Program, no global renderer state -- rendered on demand via an
// explicit width (and, for Picker, height), per the project's runtime/client
// boundary guardrail. Colors come from an injected theme.Palette rather than
// any hardcoded style, since herdr-draft's own palette is itself derived
// from the surrounding herdr session's theme (see internal/theme's package
// doc).
//
// Derived from atrium (github.com/ZviBaratz/atrium) ui/overlay/picker.go,
// © Zvi Baratz, relicensed by the author.
//
// Adaptations from the source: Atrium's Picker is a key-handling *mixin* --
// it owns filter text, cursor position, focus, width/row-count, and a
// sync/async distinction, but no item list and no renderer. Rendering and
// item ownership live in Atrium's project/branch picker types, which embed
// this mixin and are NOT on the audited clean list (branchPicker.go is
// AGPL-encumbered and was never opened for this port). herdr-draft's Picker
// is instead a complete, self-contained widget that owns its item list
// directly, so the public API is reshaped around that:
//
//   - SetItems(version, items) replaces Atrium's implicit contract (the
//     owner tracks a filterVersion bumped on every filter edit and discards
//     async results that arrive for an older version) with an explicit
//     guard on the method itself: a version lower than the highest one
//     already accepted is dropped outright. This is the same monotonic
//     -version-gate mechanism as Atrium's onEdit/filterVersion (picker.go
//     "async" branch), just moved onto the data-arrival path instead of the
//     query-edit path, because this Picker's SetItems is the thing that can
//     race, not its SetQuery.
//   - The clamp-into-range helper is ported near-verbatim from Atrium's
//     Picker.clampCursor.
//   - CursorNext/CursorPrev clamp rather than wrap, ported from Atrium's
//     handleKey KeyUp/KeyDown branches (no wraparound) -- unlike ChipRow's
//     Next/Prev, which do wrap (see chiprow.go).
//   - Atrium's filter-key-handling (handleKey/handlePaste, building up filter
//     text keystroke by keystroke) is dropped: that belongs to whatever text
//     -entry widget/key-grammar layer feeds this Picker's SetQuery in the
//     surrounding form, not to the Picker itself.
//   - Atrium's fuzzy ranking (rankCandidates, backed by internal/fuzzy) is
//     not on the audited clean list, so SetQuery here is a plain
//     case-insensitive substring filter over an item's cells and badge,
//     not a port of Atrium's subsequence matcher.
//   - Atrium's Focus/Blur/IsFocused, SetWidth/SetVisibleRows, and preview
//     hook are dropped: View takes explicit width/height per call (no owned
//     renderer state), and there is no preview-hook consumer in scope here.
//   - Atrium's theme/style plumbing (ppSelectedStyle, mfDimStyle, defined
//     outside the clean file set) is replaced by styles built from an
//     injected theme.Palette.
//
// See the task-14 report for the full port-vs-reimplement breakdown.
package widgets

import (
	"strconv"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// Tone names the palette ROLE a picker column, badge or mark glyph is
// drawn in. It is a CLOSED enum, deliberately: v2 spec §7 declined a
// per-item style hook and v3 spec §8.1 keeps that judgement -- no
// caller-supplied lipgloss.Style reaches this widget, which resolves
// every color itself from the palette it was constructed with. A field
// says what a column MEANS; the picker decides what that looks like.
//
// (v2's stated reason for declining the hook -- "rows are rendered by the
// field, not by the picker" -- was simply false, as §8.1 notes:
// renderRow below has always rendered them. The conclusion survives its
// reasoning.)
type Tone int

const (
	// ToneDefault is Palette.Text, the primary tier: the one column in a
	// row a reader is actually scanning. It is the zero value, so an
	// undeclared column is a plain-text column.
	ToneDefault Tone = iota
	// ToneDim is Palette.DimText, for text that is present but secondary.
	ToneDim
	// ToneMuted is Palette.Overlay0, v3 spec §5.2's middle tier -- panel
	// column headings, badges, identifiers. Without it a panel row reads
	// as one flat wall of same-weight text.
	ToneMuted
	// ToneAccent is Palette.Accent.
	ToneAccent
	// ToneBranch is Palette.Branch, herdr's own branch-name color.
	ToneBranch
	// ToneSuccess is Palette.Success.
	ToneSuccess
	// ToneWarning is Palette.Warning: rate-limited and degraded states.
	ToneWarning
	// ToneDanger is Palette.Danger: outright failure.
	ToneDanger
)

// color resolves the tone against a palette. An unknown Tone answers
// Text rather than panicking or rendering colorless, matching this
// package's degrade-rather-than-fail posture everywhere else.
func (t Tone) color(p theme.Palette) theme.Color {
	switch t {
	case ToneDim:
		return p.DimText
	case ToneMuted:
		return p.Overlay0
	case ToneAccent:
		return p.Accent
	case ToneBranch:
		return p.Branch
	case ToneSuccess:
		return p.Success
	case ToneWarning:
		return p.Warning
	case ToneDanger:
		return p.Danger
	default:
		return p.Text
	}
}

// ElideMode says which END of an over-long cell is worth keeping, the
// same two rules internal/form's row stack already applies to its value
// cells (KeepHead/KeepTail, and sizes.go's file doc for why an unmarked
// clip is a MISREAD rather than an incomplete value).
type ElideMode int

const (
	// ElideTail keeps the head and marks the cut at the end: the rule for
	// titles, branches, prose and identifiers. The zero value, since it
	// is the right answer for every column that is not a path.
	ElideTail ElideMode = iota
	// ElideHead keeps the tail and marks the cut at the start: the rule
	// for PATHS, whose last segments are what distinguish them.
	ElideHead
)

// PickerMatch locates the run of characters inside one cell that a query
// matched, for v3 spec §8.4's match highlighting. Col indexes Cells;
// Start and End are RUNE indices into that cell, HALF-OPEN [Start, End).
//
// Two things make the zero value inert -- Col < 0 is §8.1's stated
// "nothing to paint" signal, and an empty span says the same thing --
// which matters because every one of the five fields feeding this widget
// leaves Match unset on most of its items. A zero value that painted the
// first character of the first column would be a defect in four fields
// at once.
//
// The half-open End is a deliberate DIVERGENCE from internal/form's
// fuzzySpan, whose End is inclusive (and whose empty-query answer is the
// End < Start of fuzzyMatch). Inclusive coordinates have no inert zero
// value, which is the whole reason for the difference; a caller bridging
// the two adds one to End at the boundary.
type PickerMatch struct {
	Col        int
	Start, End int
}

// empty reports that there is nothing to paint -- either no column was
// named or the span covers no runes. It is the guard v3 spec §8.4's
// renderer reads before touching a cell, and until that renderer lands it
// is what applyFilter's own contract is asserted against.
func (m PickerMatch) empty() bool { return m.Col < 0 || m.End <= m.Start }

// PickerColumn declares how one cell column is sized and colored.
// SetColumns installs them; a column a caller never declared falls back
// to this struct's zero value (no bounds, no flex, ToneDefault,
// ElideTail), so a single-column picker needs no declaration at all.
//
// Min and Max bound the measured width: 0 means "no bound" for either.
// Max caps a column whose content is arbitrarily long (an issue title);
// Min holds a column open so short values still line up under a heading.
//
// Flex marks a column as the one that gives up width FIRST when the row
// is too narrow for everything -- the title in an issue list, whose tail
// can be elided without making the row unreadable, as against the
// identifier beside it, which must stay whole to be worth anything. It
// does not grow a column: the badge column is flush right regardless, so
// widening a cell column changes nothing visible.
//
// DropBelow is the fewest cells the column is worth drawing in. Squeezed
// past it the column is dropped OUTRIGHT -- width 0, and left() takes its
// gap with it -- rather than elided to a lone "…". That is v3 spec §8.1's
// badge rule ("a status word with one cell left for it says nothing, and
// the cells it would crowd out say something") offered to a cell column,
// and the account panel is what needs it: its seven columns do not fit an
// 80-column terminal, and the shrink loop's floor of one cell turned
// `in 2h11m` into three cells of nothing on every row.
//
// 0 means never drop, which is the right default and what every other
// column in this project uses. Declare it only for a column that is
// genuinely optional, and only for one that can be lost ALONE: the drop
// pass has no notion of columns that must go together, so a gauge and the
// labelled percentage beside it -- which are meaningless apart -- both
// leave it at 0.
type PickerColumn struct {
	Min, Max  int
	Flex      bool
	DropBelow int
	Tone      Tone
	Elide     ElideMode
}

// PickerItem is one selectable row in a Picker, as a TABLE row rather
// than a pre-composed string (v3 spec §8.1).
//
// Cells must be PLAIN text, left to right. The picker measures them for
// alignment, truncates them per column, tones them, and (v3 spec §8.4)
// paints a match span inside one -- none of which it can do to a string
// a caller has already styled. The old Label/Hint pair is what this
// replaces: two pre-joined strings meant every list rendered as a wall of
// text, with `ENG-1` and `ENG-101` starting their titles at different
// columns.
type PickerItem struct {
	ID string
	// Cells is the row's content, one string per column, left to right.
	// A row may supply fewer cells than the widest row in the set; the
	// missing ones render blank and still hold their column open.
	Cells []string
	// Badge is a short status word rendered in its own column, flush with
	// the row's right edge. Every row's cells are held clear of the
	// WIDEST badge in the set, so a badge never collides with content.
	Badge     string
	BadgeTone Tone
	// Marker is a one-cell attention glyph ("!") in the fixed two-cell
	// mark column before cell 0 -- v3 spec §8.2. It shares that column
	// with Current's own glyph and WINS: a profile that is both current
	// and auth-failed must shout the failure, and its currency is already
	// stated on the stack row above.
	Marker string
	// Current marks the field's current VALUE, which is not the cursor.
	// The cursor is where the user is browsing and carries three signals
	// of its own (fill, bold, and the panel's ▸ gutter glyph); Current is
	// what the field would submit. AgentField is where the two visibly
	// diverge -- ←→ move the chip row and the value without moving the
	// picker cursor -- and where leaving it unset was a live defect: the
	// panel highlighted one kind while the row above named another.
	Current bool
	// Match is the span to highlight, for v3 spec §8.4. Ownership, since
	// two parties can fill it: applyFilter computes it for every item it
	// keeps whenever a query is active, overwriting whatever the caller
	// set; with no query the caller's own value is preserved verbatim,
	// which is how a field that ranks its own items supplies its own
	// spans.
	Match PickerMatch
}

// markColumnWidth is the fixed width of the mark column: one cell for the
// glyph and one separating space. FIXED is the point (v3 spec §8.2) --
// before v3 the marker was a bare prefix, so a marked row shifted two
// cells right and its cells stopped lining up with every unmarked row
// above it.
const markColumnWidth = 2

// cellGap is the blank run between two adjacent columns, and between the
// last cell column and the badge. Two cells, matching the separator the
// pre-v3 row already put between its label and its hint.
const cellGap = 2

// markerCurrent is the glyph PickerItem.Current draws in the mark
// column. Marker's own glyph is the caller's ("!" everywhere it is used
// today) because a field knows what it is warning about; this one is the
// widget's, because "the current value" means the same thing in every
// list.
const markerCurrent = "✓"

// scrollbarWidth is what v3 spec §8.5's scrollbar costs: one COLUMN --
// the last cell of every row -- and not a line. A line would have to come
// out of the panel's row budget, which is the one thing §8.5 refuses to
// spend ("no PanelRows() grows anywhere").
const scrollbarWidth = 1

// The scrollbar's two glyphs, herdr's own (src/ui/scrollbar.rs:135-162).
// They are drawn in Border and Overlay0 respectively, which is exactly
// what herdr's surface_dim/overlay0 pair translates to here -- see
// internal/theme's Palette, whose Border and Overlay0 doc comments name
// this widget's track and thumb as their reason for existing.
const (
	scrollTrackGlyph = "▕"
	scrollThumbGlyph = "█"
)

// pickerMetrics is the natural (unbounded) size of every column, measured
// over the WHOLE filtered set. Measuring over the visible window instead
// is the defect v3 spec §8.1 calls out by name: the columns would jitter
// as the list scrolls, because a wider row scrolling into view would
// widen a column and shift every other row's content sideways.
//
// It is cached because a render is per-frame and a filter change is
// per-keystroke; applyFilter invalidates it, which covers both places
// §8.1 names (SetItems and SetQuery, the two callers that rebuild
// p.filtered) without either having to remember.
type pickerMetrics struct {
	// mark is whether ANY filtered item carries a Marker or is Current,
	// i.e. whether the mark column is reserved at all. A picker whose
	// items use neither keeps its two cells for content.
	mark bool
	// cells is each column's widest cell across the filtered set.
	cells []int
	// badge is the widest Badge across the filtered set, 0 when no item
	// has one.
	badge int
}

// Picker is a filterable single-select list. It owns its full item set, the
// active text query, the derived filtered/ranked view, and the cursor.
type Picker struct {
	palette theme.Palette

	haveVersion bool
	version     int
	items       []PickerItem

	query    string
	filtered []PickerItem
	cursor   int

	columns []PickerColumn
	// metrics is the measurement cache, nil when it needs recomputing.
	metrics *pickerMetrics
}

// NewPicker returns an empty Picker rendered with palette.
func NewPicker(palette theme.Palette) *Picker {
	return &Picker{palette: palette}
}

// SetItems replaces the picker's item set, tagged with a caller-assigned
// monotonic version. A call whose version is lower than the highest version
// already accepted is ignored outright -- the stale-result guard ported in
// spirit from Atrium's picker.go (see the package doc's Adaptations
// section) -- so an out-of-order async source can never clobber a fresher
// result with a stale one.
//
// A call at the same version as the one already held (e.g. a background
// refresh of the candidates for the query already on screen) preserves the
// user's selection **by item ID**, not by index: if the item that was
// selected before the call is still present afterward (however its
// position moved), it stays selected. Only when that ID is no longer in the
// refreshed set does the cursor fall back to its old numeric position,
// clamped into the new range. This matters because a same-version refresh
// can reorder items (e.g. a directory picker's candidates re-sorted by
// freshness) -- preserving by index alone would silently move the user's
// selection onto a different, unrelated item that happens to now sit at the
// same row. A call at a strictly higher version resets the cursor to the
// top, matching a genuinely new result set arriving.
func (p *Picker) SetItems(version int, items []PickerItem) {
	if p.haveVersion && version < p.version {
		return
	}
	isNewVersion := !p.haveVersion || version > p.version
	p.haveVersion = true
	p.version = version

	var previousID string
	var hadSelection bool
	if !isNewVersion {
		previousID, hadSelection = p.selectedID()
	}

	p.items = items
	p.applyFilter()

	switch {
	case isNewVersion:
		p.cursor = 0
	case hadSelection:
		p.cursor = p.indexOfIDOrFallback(previousID, p.cursor)
	default:
		p.cursor = clampCursor(p.cursor, len(p.filtered))
	}
}

// selectedID returns the ID of the currently selected item, or ("", false)
// when nothing is selected -- a thin wrapper over Selected used by SetItems
// to capture identity before the item set underneath it changes.
func (p *Picker) selectedID() (string, bool) {
	sel, ok := p.Selected()
	if !ok {
		return "", false
	}
	return sel.ID, true
}

// indexOfIDOrFallback returns the index of the filtered item whose ID
// matches id, or -- when no filtered item has that ID (it was dropped by
// the refresh) -- fallbackCursor clamped into the current filtered range.
// This is the identity-with-index-fallback rule SetItems documents: prefer
// keeping the same item selected across a same-version refresh; only fall
// back to a raw position when that item is gone.
func (p *Picker) indexOfIDOrFallback(id string, fallbackCursor int) int {
	for i, it := range p.filtered {
		if it.ID == id {
			return i
		}
	}
	return clampCursor(fallbackCursor, len(p.filtered))
}

// SetColumns declares how this picker's cell columns are sized and
// colored (v3 spec §8.1). Columns beyond the ones declared here fall back
// to the zero PickerColumn, so a one-column picker may skip the call
// entirely, and a caller need only declare the leading columns it has an
// opinion about.
//
// It changes no measurement -- the cache holds each column's NATURAL
// width and Min/Max are applied when a row is fitted to a render width --
// so it does not invalidate anything.
func (p *Picker) SetColumns(cols ...PickerColumn) {
	p.columns = append([]PickerColumn(nil), cols...)
}

// column returns the declared column i, or the zero PickerColumn for one
// the caller never described. See SetColumns.
func (p *Picker) column(i int) PickerColumn {
	if i < 0 || i >= len(p.columns) {
		return PickerColumn{}
	}
	return p.columns[i]
}

// SetQuery replaces the active filter text and re-applies it against the
// current item set, resetting the cursor to the top of the freshly filtered
// list -- matching Atrium's sync-picker onEdit behavior (picker.go).
//
// Matching is a plain case-insensitive substring test against every cell
// and the badge. Atrium's own ranked subsequence matcher (rankCandidates,
// backed by internal/fuzzy) is not on the audited clean list, so it is
// not ported here; see the package doc.
func (p *Picker) SetQuery(query string) {
	p.query = query
	p.applyFilter()
	p.cursor = 0
}

// applyFilter rebuilds p.filtered from p.items and the active query, and
// is the single funnel for both places v3 spec §8.1 requires the column
// measurements invalidated (SetItems and SetQuery). Invalidating here
// rather than in each of them is the same set of call sites and one
// fewer thing for a third caller to forget.
//
// It also owns PickerItem.Match while a query is active, per §8.4: every
// kept item's Match is computed from that query, overwriting whatever
// the caller supplied. With no query the items -- and their callers' own
// spans -- are copied through untouched, which is how a field that ranks
// its own candidates (DirField's fuzzy path) gets to supply spans this
// widget could not have computed.
func (p *Picker) applyFilter() {
	p.metrics = nil
	if p.query == "" {
		p.filtered = append([]PickerItem(nil), p.items...)
		return
	}
	q := []rune(strings.ToLower(p.query))
	filtered := make([]PickerItem, 0, len(p.items))
	for _, it := range p.items {
		match, ok := matchItem(it, q)
		if !ok {
			continue
		}
		it.Match = match
		filtered = append(filtered, it)
	}
	p.filtered = filtered
}

// matchItem finds the first cell (left to right) holding q and reports
// the span, or -- when only the BADGE matches -- a kept row with nothing
// to paint (Col -1). A badge match still keeps the row because the
// pre-v3 filter tested the hint, which is where the status word an issue
// list is filtered by used to live.
func matchItem(it PickerItem, q []rune) (PickerMatch, bool) {
	for i, cell := range it.Cells {
		if start, end, ok := matchRunes(cell, q); ok {
			return PickerMatch{Col: i, Start: start, End: end}, true
		}
	}
	if _, _, ok := matchRunes(it.Badge, q); ok {
		return PickerMatch{Col: -1}, true
	}
	return PickerMatch{Col: -1}, false
}

// matchRunes reports the half-open RUNE span of the first
// case-insensitive occurrence of q (already lowercased by the caller, once
// per query rather than once per cell) in s.
//
// It walks runes and lowercases them one at a time rather than taking
// strings.Index over strings.ToLower(s), because the byte offset that
// would return points into the LOWERCASED string, and converting it back
// to a rune index into s assumes ToLower preserves rune count -- which it
// does not for every input (U+0130 lowercases to two runes). The span is
// a coordinate into the string a caller will slice, so it has to be exact
// there rather than nearly exact. The cost is the naive O(len(s)*len(q))
// scan, the same order as internal/form's own fuzzy matcher over the same
// list sizes.
func matchRunes(s string, q []rune) (start, end int, ok bool) {
	if len(q) == 0 || s == "" {
		return 0, 0, false
	}
	cand := []rune(s)
	for i := 0; i+len(q) <= len(cand); i++ {
		hit := true
		for j, qr := range q {
			if unicode.ToLower(cand[i+j]) != qr {
				hit = false
				break
			}
		}
		if hit {
			return i, i + len(q), true
		}
	}
	return 0, 0, false
}

// measure returns the cached column measurements, computing them over the
// whole filtered set on a miss. See pickerMetrics for why the whole set
// and not the visible window.
func (p *Picker) measure() pickerMetrics {
	if p.metrics != nil {
		return *p.metrics
	}
	m := pickerMetrics{}
	for _, it := range p.filtered {
		if it.Marker != "" || it.Current {
			m.mark = true
		}
		for len(m.cells) < len(it.Cells) {
			m.cells = append(m.cells, 0)
		}
		for i, c := range it.Cells {
			if w := ansi.StringWidth(c); w > m.cells[i] {
				m.cells[i] = w
			}
		}
		if w := ansi.StringWidth(it.Badge); w > m.badge {
			m.badge = w
		}
	}
	p.metrics = &m
	return m
}

// rowLayout is one render width's worth of column geometry: the same
// numbers for every row in the frame, which is what makes the columns
// line up.
type rowLayout struct {
	mark  int
	cells []int
	badge int
	// bar is the scrollbar column: scrollbarWidth while the list outgrows
	// the window, 0 otherwise (v3 spec §8.5). It is settled HERE rather
	// than subtracted at the render site so the cell columns are measured
	// against the width they will actually get -- a scrollbar taken off
	// the end of an already-fitted row would silently clip whatever
	// column happened to be last.
	bar int
}

// left is the width of everything before the badge: the mark column, the
// cell columns, and the gaps between them.
//
// A zero-width column contributes neither width nor gap. That is not a
// degenerate case: a column is zero wide when NO row in the filtered set
// has anything in it -- clauth reporting an empty AuthStatus for every
// profile, say, which its own unvalidated-JSON shape allows -- and a
// two-cell gap around an invisible column reads as a rendering fault
// rather than as a column nobody filled in.
func (l rowLayout) left() int {
	w, shown := l.mark, 0
	for _, c := range l.cells {
		if c == 0 {
			continue
		}
		if shown > 0 {
			w += cellGap
		}
		w += c
		shown++
	}
	return w
}

// layout fits the measured columns into a width by height render: the
// mark column when the set uses one, the scrollbar column when the list
// outgrows the window, the badge column when it has room for one, and the
// cell columns bounded by their own Min/Max and then shrunk, Flex first,
// until they fit.
//
// height is here for the scrollbar alone, and only to answer "does the
// list fit" -- no column's width depends on it, which is what keeps
// MarkedView's rows identical at every height that shows the same items.
//
// If everything is at its floor and the row STILL does not fit, the row
// simply overflows and MarkedView's per-row style clips it -- the same
// hard-clip-rather-than-fail contract widthStyle documents. A popup
// narrow enough to reach that has bigger problems than a column.
func (p *Picker) layout(width, height int) rowLayout {
	m := p.measure()

	var lay rowLayout
	if m.mark {
		lay.mark = markColumnWidth
	}
	// v3 spec §8.5: reserved only while the list outgrows the window, so
	// content narrows by one cell exactly when the bar appears -- accepted
	// there, because it coincides with a state change the user can already
	// see. The width guard is the degenerate end of the same rule: at one
	// cell of render width a scrollbar would be the whole row, and a row
	// with nothing in it says less than a row with no scrollbar.
	if len(p.filtered) > height && width > scrollbarWidth {
		lay.bar = scrollbarWidth
	}
	avail := width - lay.mark - lay.bar
	// The badge is dropped outright rather than squeezed: a status word
	// with one cell left for it says nothing, and the cells it would
	// crowd out say something.
	if m.badge > 0 && avail-cellGap-m.badge >= 1 {
		lay.badge = m.badge
		avail -= cellGap + m.badge
	}
	if len(m.cells) == 0 {
		return lay
	}

	lay.cells = make([]int, len(m.cells))
	for i, natural := range m.cells {
		col := p.column(i)
		w := natural
		if col.Max > 0 && w > col.Max {
			w = col.Max
		}
		if w < col.Min {
			w = col.Min
		}
		lay.cells[i] = w
	}

	// Columns worth less than their DropBelow go before anything is
	// squeezed, in shrink order, so the column the caller ranked most
	// expendable goes first -- and only as far as the row actually needs.
	// The walk stops at the first column that can absorb everything still
	// over while staying at or above its own DropBelow, because the
	// shrink loop below takes from exactly that column next.
	//
	// over is recomputed from left() each pass rather than adjusted by
	// hand: a dropped column costs its width AND its gap, except when it
	// was the only column left, and left() is the one place that
	// arithmetic lives.
	for _, i := range p.shrinkOrder(len(lay.cells)) {
		over := lay.left() - lay.mark - avail
		if over <= 0 {
			break
		}
		col := p.column(i)
		if col.DropBelow < 1 || lay.cells[i] < 1 {
			continue
		}
		if lay.cells[i]-over >= col.DropBelow {
			break
		}
		lay.cells[i] = 0
	}

	// Shrinking floors a non-empty column at 1, so which columns are zero
	// wide -- and therefore which gaps exist at all, see left() -- is
	// settled here and cannot change underneath the loop below.
	over := lay.left() - lay.mark - avail
	for _, i := range p.shrinkOrder(len(lay.cells)) {
		if over <= 0 {
			break
		}
		floor := p.column(i).Min
		if floor < 1 && lay.cells[i] > 0 {
			// A column shrunk to nothing takes its content with it and
			// leaves a gap where a column used to be, which reads as a
			// rendering fault rather than as a narrow terminal.
			floor = 1
		}
		if give := lay.cells[i] - floor; give > 0 {
			if give > over {
				give = over
			}
			lay.cells[i] -= give
			over -= give
		}
	}
	return lay
}

// shrinkOrder is the order columns give up width in: every Flex column
// left to right, then the rest RIGHT to left. Taking from the right first
// among the inflexible ones keeps the leading columns -- the identifier
// you are scanning for -- intact longest.
func (p *Picker) shrinkOrder(n int) []int {
	order := make([]int, 0, n)
	for i := 0; i < n; i++ {
		if p.column(i).Flex {
			order = append(order, i)
		}
	}
	for i := n - 1; i >= 0; i-- {
		if !p.column(i).Flex {
			order = append(order, i)
		}
	}
	return order
}

// Selected returns the item under the cursor, or (PickerItem{}, false) when
// the filtered list is empty.
func (p *Picker) Selected() (PickerItem, bool) {
	if p.cursor < 0 || p.cursor >= len(p.filtered) {
		return PickerItem{}, false
	}
	return p.filtered[p.cursor], true
}

// SelectID moves the cursor directly to the filtered item whose ID == id,
// leaving the cursor unchanged (and returning false) when no such item is
// present. Added in Task 20b for a caller that needs to select an item by
// IDENTITY rather than by position -- e.g. AccountField.SetPin applying
// spec §12's config.toml `[clauth] default` profile name at form
// construction, before the user has navigated the picker at all. This is
// the same first-match linear scan SetItems' own indexOfIDOrFallback
// already performs internally for a same-version refresh; SelectID is
// just a direct, caller-invoked entry point to it, for a caller that
// already knows the target ID up front rather than discovering it via a
// refresh.
func (p *Picker) SelectID(id string) bool {
	for i, it := range p.filtered {
		if it.ID == id {
			p.cursor = i
			return true
		}
	}
	return false
}

// FilteredLen reports how many rows this picker currently has to show --
// the item count AFTER SetQuery's filter, which is what a caller sizing a
// panel around the list actually needs (v2 spec §5: a Section's
// PanelRows() is "the greatest number of rows this field can put to good
// use", derived from its own item count).
//
// Pure read, added for v2's panel sizing: it renders nothing and changes
// nothing, so it cannot move a frame on its own.
func (p *Picker) FilteredLen() int { return len(p.filtered) }

// Len reports how many items this picker HOLDS, before any query -- the
// denominator of v3 spec §8.5's filter readout (`3/24 issues`), whose
// numerator is FilteredLen. The two differ only while a query is active,
// which is exactly when the readout is shown, so a caller wanting "how
// many rows are there to draw" still wants FilteredLen.
//
// Pure read, like FilteredLen: it renders nothing and changes nothing, so
// it cannot move a frame on its own.
func (p *Picker) Len() int { return len(p.items) }

// FilteredHasID reports whether the item with this ID survives the
// current query. It exists for one job: a field whose picker carries a
// SENTINEL row -- IssueField's `none`, AccountField's `active` -- cannot
// use FilteredLen as v3 spec §8.5's numerator directly, because that row
// is not one of the things the readout counts. Asking here is cheaper
// and less brittle than re-running the picker's own filter outside it.
//
// An empty id is never held (SetItems' IDs are non-empty by contract), so
// it answers false rather than matching the zero value of every item that
// forgot one.
//
// Pure read, like FilteredLen and Len: it renders nothing and changes
// nothing, so it cannot move a frame on its own.
func (p *Picker) FilteredHasID(id string) bool {
	if id == "" {
		return false
	}
	for _, it := range p.filtered {
		if it.ID == id {
			return true
		}
	}
	return false
}

// CursorRow reports the PHYSICAL row (0-based) the cursor lands on inside
// a View/MarkedView render height rows tall, or -1 when nothing is
// selected or the cursor falls outside that window. It is the exact
// inverse of SelectVisibleRow, recomputing the same scrollOffset the
// render itself uses.
//
// v2's panels draw their own `▸` cursor glyph in the two-cell gutter
// column BESIDE the list (v2 spec §4's mockups), not inside a row, so the
// field composing the panel has to know which physical line the cursor
// is on. Putting the glyph in PickerItem/renderRow instead would move
// every committed v1 golden frame that shows a picker; this accessor
// keeps the marker a property of the PANEL, where v2 puts it, and left
// this widget's own rendering byte-identical at the time.
//
// v3 spec §8.2 re-ratifies the placement on its own merits rather than on
// that frame argument, which has since expired: the cursor row now
// carries three signals inside the row (a Surface fill, bold, and a
// brighter foreground), and a fourth glyph in the row as well -- beside
// the ✓ the mark column may already be drawing -- would be one pointer
// too many. So the row's own mark column is for PickerItem.Current and
// PickerItem.Marker, and the cursor stays out here.
func (p *Picker) CursorRow(height int) int {
	if height < 1 {
		height = 1
	}
	if p.cursor < 0 || p.cursor >= len(p.filtered) {
		return -1
	}
	row := p.cursor - scrollOffset(p.cursor, len(p.filtered), height)
	if row < 0 || row >= height {
		return -1
	}
	return row
}

// CursorNext moves the cursor down one row, clamping at the last row --
// ported from Atrium's handleKey KeyDown branch (no wraparound).
func (p *Picker) CursorNext() {
	if p.cursor < len(p.filtered)-1 {
		p.cursor++
	}
}

// CursorPrev moves the cursor up one row, clamping at the first row --
// ported from Atrium's handleKey KeyUp branch (no wraparound).
func (p *Picker) CursorPrev() {
	if p.cursor > 0 {
		p.cursor--
	}
}

// SelectVisibleRow moves the cursor to the item most recently rendered at
// physical row (0-based, within a MarkedView/View call at the given
// height), recomputing the same scrollOffset that render used -- the
// mouse-click counterpart to CursorNext/CursorPrev/SelectID (task 21: a
// click on one of this picker's own "row:<sectionID>:<n>" zones, usually
// reached via SelectAt below rather than called directly). Returns false
// (a no-op, cursor unchanged) when row is out of range for the CURRENT
// filtered list at that height, or the filtered list is empty -- e.g. a
// stale click after the list changed underneath it, or a click on a
// blank/placeholder row that was never marked with a zone at all (see
// MarkedView).
func (p *Picker) SelectVisibleRow(row, height int) bool {
	if row < 0 || len(p.filtered) == 0 {
		return false
	}
	offset := scrollOffset(p.cursor, len(p.filtered), height)
	idx := offset + row
	if idx < 0 || idx >= len(p.filtered) {
		return false
	}
	p.cursor = idx
	return true
}

// SelectAt attempts to move the cursor to whichever of this picker's own
// rows msg's coordinates land on, via that row's own
// zonePrefix+strconv.Itoa(n) zone (task 21's "row:<sectionID>:<n>"
// scheme, n being the row's PHYSICAL position) registered by the most
// recent MarkedView(width, height, zonePrefix) call, returning the
// matched item and true on a hit. height and zonePrefix must be the SAME
// values passed to that MarkedView call, or the lookup will never match
// anything. A no-op (returns the zero PickerItem and false) when msg
// does not land on any of this picker's row zones.
func (p *Picker) SelectAt(msg tea.MouseMsg, height int, zonePrefix string) (PickerItem, bool) {
	for row := 0; row < height; row++ {
		if !Zones.Get(zonePrefix + strconv.Itoa(row)).InBounds(msg) {
			continue
		}
		if p.SelectVisibleRow(row, height) {
			return p.filtered[p.cursor], true
		}
		return PickerItem{}, false
	}
	return PickerItem{}, false
}

// clampCursor keeps cursor within [0, itemCount). The itemCount upper-bound
// clamp (cursor >= itemCount) is ported near-verbatim from Atrium's
// Picker.clampCursor; the cursor < 0 lower-bound guard is a defensive
// addition not present in the source -- Atrium's own cursor can't go
// negative through its handleKey (KeyUp only decrements while cursor > 0),
// but this widget also derives cursor from indexOfIDOrFallback and other
// call sites, so the guard is kept here rather than relied on implicitly.
func clampCursor(cursor, itemCount int) int {
	if cursor < 0 {
		return 0
	}
	if cursor >= itemCount {
		if itemCount > 0 {
			return itemCount - 1
		}
		return 0
	}
	return cursor
}

// NOTE: an empty list renders as blank rows and says NOTHING of its own.
// This widget used to write a bare "no matches" into row 0, which v2 spec
// §6.1 forbids outright: an empty panel list must speak "in the field's
// own terms (`no branches yet`, `no assigned issues`), never a bare
// `no matches`". That row was believed unreachable ("every picker pins a
// sentinel row 0") -- two of the five callers pin none. DirField filters
// its own candidate pool and IssueField filters even its `none` row, so
// either can empty the list, and both already carry a status line of
// their own directly beneath it: the panel printed "no matches" and
// "no matching directories" one above the other, two sentences for one
// fact, the wrong one first.
//
// The placeholder is deleted rather than made configurable, because a
// widget with an empty-text hook is a widget someone eventually leaves at
// its default. With no default to leave, the field's own line is the only
// thing that can speak -- which is where §6.1 puts the sentence anyway.

// View renders the picker into exactly height rows (floored at 1), each
// width cells wide: space-padded when short, clipped when long. width <= 0
// renders height empty rows rather than panicking or leaving content
// unclipped -- lipgloss's Width/MaxWidth style keys both treat 0 as
// "unset", so callers must not rely on a zero-width Style to blank content.
// It is MarkedView with an empty zone prefix (Zones.Mark's own empty-id
// no-op, see its doc comment) -- the zero-dependency rendering path every
// existing widget-level test and every raw (non-field-Section)
// golden-frame fixture in this package continues to use unmodified.
func (p *Picker) View(width, height int) string {
	return p.MarkedView(width, height, "")
}

// MarkedView renders exactly like View, additionally wrapping each row
// that corresponds to a real filtered item (not a blank trailing row) in
// a bubblezone/v2 zone marker ID'd zonePrefix+strconv.Itoa(row) via this
// package's shared Zones manager (zones.go) -- task 21's
// "row:<sectionID>:<n>" scheme, n being
// the row's PHYSICAL position (0-based, within this call's own height),
// NOT the item's absolute filtered index or ID, so a caller resolving a
// click back to an item must go through SelectAt/SelectVisibleRow above,
// which recompute the same scrollOffset this method used. zonePrefix ==
// "" marks nothing at all (Zones.Mark's own empty-id no-op) -- see
// View's own doc comment.
//
// v3 spec §8.5's scrollbar lives here too: the last cell of every row,
// reserved only while the list outgrows height -- see layout for the
// reservation and scrollbarCell for the two glyphs.
func (p *Picker) MarkedView(width, height int, zonePrefix string) string {
	if height < 1 {
		height = 1
	}
	if width <= 0 {
		return strings.Join(make([]string, height), "\n")
	}
	// The row style is PER ROW, not hoisted: v3 spec §8.2 fills the
	// cursor row with Surface edge to edge, and §8.3 is why that cannot
	// be an outer Background(...).Render over a row holding any styled
	// span at all -- Style.Render's trailing reset is unconditional, so
	// the fill would drop out after the first toned cell. PaintLine
	// reasserts the background after every embedded reset; see its own
	// doc comment, and the frame-level test in internal/form that pins
	// this surviving composeRows' second, PanelBG-colored pass over the
	// same line.
	lay := p.layout(width, height)
	// Everything but the scrollbar column, and what every row below is
	// composed at: the bar is appended OUTSIDE the row's own fill, so the
	// track reads as one continuous stroke down the side of the list
	// rather than being interrupted by the cursor row's Surface (herdr
	// draws its own scrollbar in a rect beside the list, for the same
	// reason). It is outside the row's zone marker too, so a click on the
	// bar is inert rather than selecting the row behind it -- the bar is
	// not draggable either (v3 spec §8.5's stated non-goal), so there is
	// nothing for a press on it to mean.
	inner := width - lay.bar
	lines := make([]string, height)

	offset := scrollOffset(p.cursor, len(p.filtered), height)
	thumbTop, thumbLength := scrollThumb(len(p.filtered), height, offset)
	for row := 0; row < height; row++ {
		bar := p.scrollbarCell(lay, row, thumbTop, thumbLength)
		idx := offset + row
		if idx >= len(p.filtered) {
			lines[row] = widthStyle(inner).Render("") + bar
			continue
		}
		cursor := idx == p.cursor
		rendered := p.renderRow(p.filtered[idx], lay, inner, cursor)
		zoneID := ""
		if zonePrefix != "" {
			zoneID = zonePrefix + strconv.Itoa(row)
		}
		marked := Zones.Mark(zoneID, rendered)
		if cursor {
			lines[row] = PaintLine(marked, inner, p.palette.Surface) + bar
			continue
		}
		lines[row] = widthStyle(inner).Render(marked) + bar
	}
	return strings.Join(lines, "\n")
}

// scrollbarCell renders one physical row's scrollbar cell -- the thumb
// where scrollThumb put it, the track everywhere else, and nothing at all
// (not even a blank) when no column was reserved, so a list that fits
// spends exactly zero cells on a scrollbar it isn't drawing.
//
// The colors are read off the palette directly rather than through Tone:
// Tone is the closed enum a CALLER picks a column's meaning from (see its
// doc comment), and the track's near-invisible Border has no place in a
// list of foreground roles a field would ever choose.
func (p *Picker) scrollbarCell(lay rowLayout, row, thumbTop, thumbLength int) string {
	if lay.bar < 1 {
		return ""
	}
	glyph, fg := scrollTrackGlyph, p.palette.Border
	if row >= thumbTop && row < thumbTop+thumbLength {
		glyph, fg = scrollThumbGlyph, p.palette.Overlay0
	}
	return lipgloss.NewStyle().Foreground(fg).Render(glyph)
}

// renderRow renders one item's content at the shared column geometry:
// the fixed mark column, each cell padded (or elided) to its column's
// width, and the badge flush with the row's right edge. width is the
// CONTENT width -- the render width less lay.bar, since the scrollbar's
// own cell is appended by MarkedView after the row is composed -- so the
// badge is flush with the last cell the row itself owns. cursor denotes
// the CURSOR row, not focus (this widget doesn't track focus; see the
// package doc's Adaptations section) and not PickerItem.Current, which
// is a different fact -- see that field's own doc comment.
//
// The cursor row's four signals are v3 spec §8.2's, taken from herdr's
// own selected row (dialogs.rs:487-500): the Surface fill MarkedView
// applies around this, bold cells, a brighter foreground, and the ▸
// glyph the PANEL draws in its gutter (there is no second cursor glyph
// inside the row -- CursorRow's doc comment records why it lives out
// there). The brighter foreground is Text and NOT Accent: with an accent
// gutter glyph and §8.4's accent match span, a third accent on the same
// row is noise.
//
// One cell per row may be routed through highlightCell instead of
// fitCell: the one PickerItem.Match names, whose matched run is repainted
// in matchStyle. Every other cell renders byte for byte as it did before
// §8.4, which is most cells on most rows -- Match is unset except on a
// filtered list.
//
// The badge keeps its own tone on the cursor row -- bolded like the
// cells, but not repainted in Text -- because that tone IS the row's
// state: a Danger badge flattened to plain text would hide a failure
// exactly when the user has scrolled onto it.
func (p *Picker) renderRow(item PickerItem, lay rowLayout, width int, cursor bool) string {
	cellStyle := func(t Tone) lipgloss.Style {
		if cursor {
			return lipgloss.NewStyle().Foreground(p.palette.Text).Bold(true)
		}
		return lipgloss.NewStyle().Foreground(t.color(p.palette))
	}

	var b strings.Builder
	if lay.mark > 0 {
		b.WriteString(p.renderMark(item, lay.mark))
	}
	shown := 0
	for i, w := range lay.cells {
		if w == 0 {
			continue // an empty column costs nothing, not even its gap -- see rowLayout.left
		}
		if shown > 0 {
			b.WriteString(strings.Repeat(" ", cellGap))
		}
		shown++
		text := ""
		if i < len(item.Cells) {
			text = item.Cells[i]
		}
		col := p.column(i)
		base := cellStyle(col.Tone)
		if m := item.Match; m.Col == i && !m.empty() {
			b.WriteString(highlightCell(text, w, m.Start, m.End, col.Elide, base, p.matchStyle()))
			continue
		}
		b.WriteString(base.Render(fitCell(text, w, col.Elide)))
	}
	if lay.badge > 0 {
		// Right-flush: pad out to the badge column and right-align the
		// word inside it, so every badge ends at the same cell -- the
		// row's last -- however wide the cells before it turned out.
		gap := width - lay.badge - lay.left()
		if gap < 1 {
			gap = 1
		}
		badge := KeepHead(item.Badge, lay.badge)
		b.WriteString(strings.Repeat(" ", gap))
		b.WriteString(strings.Repeat(" ", lay.badge-ansi.StringWidth(badge)))
		badgeStyle := lipgloss.NewStyle().Foreground(item.BadgeTone.color(p.palette))
		if cursor {
			badgeStyle = badgeStyle.Bold(true)
		}
		b.WriteString(badgeStyle.Render(badge))
	}
	return b.String()
}

// renderMark renders the fixed mark column: Marker's own glyph, else
// Current's, else blanks. Marker wins when a row is both -- v3 spec
// §8.2's stated priority, because a profile that is both current and
// auth-failed must shout the failure.
//
// The two glyphs are toned apart rather than colored alike: `!` is
// Warning because it is always an attention marker (an auth failure or a
// rate limit), and `✓` is Accent because it names the one row on the
// panel that is the field's actual value.
func (p *Picker) renderMark(item PickerItem, width int) string {
	glyph, tone := "", ToneDefault
	switch {
	case item.Marker != "":
		glyph, tone = item.Marker, ToneWarning
	case item.Current:
		glyph, tone = markerCurrent, ToneAccent
	default:
		return strings.Repeat(" ", width)
	}
	glyph = KeepHead(glyph, width)
	pad := width - ansi.StringWidth(glyph)
	if pad < 0 {
		pad = 0
	}
	return lipgloss.NewStyle().Foreground(tone.color(p.palette)).Render(glyph) + strings.Repeat(" ", pad)
}

// matchStyle is the one style v3 spec §8.4 paints a matched run in:
// Accent, bold, and the SAME on the cursor row as off it.
//
// Same-on-both is the deliberate half. The cursor row already carries
// three signals of its own (renderRow's doc comment lists them), so a
// match span that changed colour under the cursor would be a fourth
// signal saying nothing new -- and it would break the one thing the
// highlight is for, which is letting the eye run DOWN the list comparing
// where each row matched. A span that shifts hue on one row of the
// column is exactly the noise §8.2 dropped Accent from the cursor row to
// avoid.
//
// It is not a Tone: Tone is the closed enum a CALLER picks a column's
// meaning from, and "the characters your query matched" is not a
// property of the column at all.
func (p *Picker) matchStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(p.palette.Accent).Bold(true)
}

// highlightCell is fitCell with one run of runes repainted in accent --
// the span a query matched inside this cell, which is the whole of v3
// spec §8.4's visible output. Everything outside the run renders in base
// exactly as fitCell's own output does, and the result is still exactly
// width cells wide.
//
// start and end are HALF-OPEN rune indices [start, end) into text:
// PickerMatch's convention, NOT internal/form's fuzzySpan, whose End is
// inclusive. Taking the widget-side one here is deliberate. This
// function's only caller reads the span straight out of
// PickerItem.Match, so accepting the inclusive convention instead would
// bury a +1 inside the renderer -- and §8.4's boxed warning names that
// bridge as the likeliest silent bug in the whole redesign. It belongs at
// the FIELD that owns an inclusive span (form.dirMatch is the only one),
// where a test can point at it.
//
// Order of operations is §8.4's and is not interchangeable: truncate on
// RUNES first, then intersect the span with whatever survived. Slicing
// the cell into head/span/tail and eliding each piece would spend an
// ellipsis per piece and overshoot width -- and under ElideHead it would
// elide the head of a piece that is not the head of the cell.
//
// The intersection is why a span that scrolled off the end degrades to a
// plain cell rather than to a clamped one-rune highlight sitting against
// the ellipsis: a highlight pointing at a character the reader cannot see
// is worse than none.
func highlightCell(text string, width, start, end int, mode ElideMode, base, accent lipgloss.Style) string {
	if width <= 0 {
		return ""
	}
	shown := KeepHead(text, width)
	if mode == ElideHead {
		shown = KeepTail(text, width)
	}

	// Which of text's runes survived, and where they sit in shown:
	// text[from:to) is drawn starting at shown's rune index at. An
	// untruncated cell is the identity. Both elide rules spend exactly one
	// rune on Ellipsis (verified against KeepHead/KeepTail for wide runes
	// too, where a cut lands on a cell boundary and not a rune one), so
	// len(shown)-1 counts the survivors; ElideHead additionally shifts
	// them one rune right, past its LEADING ellipsis, which is the offset
	// §8.4 means by "carrying the offset ElideHead introduces".
	sr := []rune(shown)
	n := len([]rune(text))
	from, to, at := 0, n, 0
	if shown != text {
		kept := len(sr) - 1
		if kept < 0 {
			kept = 0
		}
		if mode == ElideHead {
			from, to, at = n-kept, n, 1
		} else {
			from, to, at = 0, kept, 0
		}
	}

	lo, hi := start, end
	if lo < from {
		lo = from
	}
	if hi > to {
		hi = to
	}
	if lo >= hi {
		return base.Render(fitCell(text, width, mode))
	}
	lo, hi = lo-from+at, hi-from+at

	// The pad is fitCell's, and it goes inside the trailing base.Render
	// for the same reason fitCell's does: the blanks that hold the next
	// column's start position have to carry the row's own styling, not
	// the accent's.
	tail := string(sr[hi:])
	if pad := width - ansi.StringWidth(shown); pad > 0 {
		tail += strings.Repeat(" ", pad)
	}

	var b strings.Builder
	// Empty pieces are skipped rather than rendered: lipgloss emits the
	// style's full SGR pair around an empty string, which would put two
	// resets into the row for nothing -- and every reset is one more the
	// cursor row's PaintLine has to repair (v3 spec §8.3).
	if head := string(sr[:lo]); head != "" {
		b.WriteString(base.Render(head))
	}
	b.WriteString(accent.Render(string(sr[lo:hi])))
	if tail != "" {
		b.WriteString(base.Render(tail))
	}
	return b.String()
}

// fitCell renders text at exactly width cells: elided per the column's
// own rule when it is too long, space-padded on the right when it is too
// short. Padding is what makes the NEXT column start at the same place on
// every row, so it is applied even to the last column, whose trailing
// blanks the row style would otherwise have added anyway.
func fitCell(text string, width int, mode ElideMode) string {
	if width <= 0 {
		return ""
	}
	shown := KeepHead(text, width)
	if mode == ElideHead {
		shown = KeepTail(text, width)
	}
	if pad := width - ansi.StringWidth(shown); pad > 0 {
		return shown + strings.Repeat(" ", pad)
	}
	return shown
}

// scrollOffset picks the first visible row so cursor stays inside a window
// of size rows drawn from total items, keeping the cursor roughly centered
// once the list is taller than the window.
func scrollOffset(cursor, total, rows int) int {
	if rows <= 0 || total <= rows {
		return 0
	}
	offset := cursor - rows/2
	if offset < 0 {
		offset = 0
	}
	if offset > total-rows {
		offset = total - rows
	}
	return offset
}

// scrollThumb sizes and positions a proportional scrollbar thumb for a
// window of rows drawn from total items at offset -- the pure geometry
// behind v3 spec §8.5's last-column scrollbar, and a sibling of
// scrollOffset above rather than part of any render. Returns (0, 0) when
// the whole list fits, which doubles as the "reserve no column at all"
// signal §8.5 asks for: the scrollbar costs a column only while the list
// outgrows the window.
//
// Ported from herdr's own scrollbar (src/ui/scrollbar.rs:36-69,
// scrollbar_thumb), with its ScrollMetrics collapsed into this widget's
// terms: herdr's viewport_rows and its track height are both rows here
// (the track is exactly as tall as the list window it sits beside), and
// its max_offset_from_bottom is total-rows. That leaves both of its
// formulas intact --
//
//	length = clamp(round(rows*rows/total), 1, rows)
//	top    = round(offset*(rows-length) / (total-rows))
//
// -- including the .max(1), which exists for a case that is easy to reach
// here: a list a hundred times the window's height rounds the
// proportional length to ZERO, and a zero-length thumb is not a small
// scrollbar but an absent one.
//
// Rounding is integer half-up via roundDiv rather than herdr's f32
// .round(), which is the same answer for non-negative inputs and makes
// the bottom of the track exact rather than nearly exact: at
// offset == total-rows the two factors cancel outright, so top is
// rows-length and top+length is rows, never one row short.
func scrollThumb(total, rows, offset int) (top, length int) {
	if rows <= 0 || total <= rows {
		return 0, 0
	}
	if offset < 0 {
		offset = 0
	}
	if offset > total-rows {
		offset = total - rows
	}

	length = roundDiv(rows*rows, total)
	if length < 1 {
		length = 1
	}
	if length > rows {
		// Unreachable for total > rows (the true ratio is already below
		// rows), kept because herdr's own .min(track_height) is and a
		// thumb longer than its track is the one failure that would
		// overrun the caller's row loop.
		length = rows
	}
	return roundDiv(offset*(rows-length), total-rows), length
}

// roundDiv returns num/den rounded half-up, for num >= 0 and den > 0:
// floor((2*num + den) / (2*den)) is exactly floor(num/den + 1/2) without
// leaving integer arithmetic. den <= 0 answers 0 rather than panicking --
// scrollThumb's own guards already exclude it.
func roundDiv(num, den int) int {
	if den <= 0 {
		return 0
	}
	return (2*num + den) / (2 * den)
}

// widthStyle returns a Style that pads/truncates rendered content to
// exactly width cells, on exactly one output line. Callers must only invoke
// it with width > 0: lipgloss treats 0 as "unset" for both the Width and
// MaxWidth style keys, so this would pass width<=0 content through
// unclipped rather than blanking it.
//
// Inline(true) is required, not cosmetic: Style.Render runs Wrap() whenever
// width > 0 and inline is false, *before* the MaxWidth truncation step (see
// charm.land/lipgloss/v2@v2.0.5 style.go's "Word wrap" comment) -- so
// without it, content longer than width is word-wrapped onto multiple
// physical lines instead of being clipped onto one, silently breaking every
// caller here that promises a fixed row/line count (Picker.View's
// exactly-height-rows contract, ChipRow.View's single-line chip row).
// Inline(true) skips that word-wrap step but leaves the width-alignment
// padding (via alignTextHorizontal, which runs regardless of inline) and
// the MaxWidth truncation (likewise unconditional) intact, so short content
// still gets padded to width and long content still gets clipped to it.
func widthStyle(width int) lipgloss.Style {
	return lipgloss.NewStyle().Width(width).MaxWidth(width).Inline(true)
}
