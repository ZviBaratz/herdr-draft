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
//     case-insensitive substring filter over Label/Hint, not a port of
//     Atrium's subsequence matcher.
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
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// PickerItem is one selectable row in a Picker.
type PickerItem struct {
	ID     string
	Label  string
	Hint   string
	Marker string
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

// SetQuery replaces the active filter text and re-applies it against the
// current item set, resetting the cursor to the top of the freshly filtered
// list -- matching Atrium's sync-picker onEdit behavior (picker.go).
//
// Matching is a plain case-insensitive substring test against Label and
// Hint. Atrium's own ranked subsequence matcher (rankCandidates, backed by
// internal/fuzzy) is not on the audited clean list, so it is not ported
// here; see the package doc.
func (p *Picker) SetQuery(query string) {
	p.query = query
	p.applyFilter()
	p.cursor = 0
}

func (p *Picker) applyFilter() {
	if p.query == "" {
		p.filtered = append([]PickerItem(nil), p.items...)
		return
	}
	q := strings.ToLower(p.query)
	filtered := make([]PickerItem, 0, len(p.items))
	for _, it := range p.items {
		if strings.Contains(strings.ToLower(it.Label), q) || strings.Contains(strings.ToLower(it.Hint), q) {
			filtered = append(filtered, it)
		}
	}
	p.filtered = filtered
}

// Selected returns the item under the cursor, or (PickerItem{}, false) when
// the filtered list is empty.
func (p *Picker) Selected() (PickerItem, bool) {
	if p.cursor < 0 || p.cursor >= len(p.filtered) {
		return PickerItem{}, false
	}
	return p.filtered[p.cursor], true
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

// pickerEmptyPlaceholder is the row View renders in place of the list when
// the current query matches nothing.
const pickerEmptyPlaceholder = "no matches"

// View renders the picker into exactly height rows (floored at 1), each
// width cells wide: space-padded when short, clipped when long. width <= 0
// renders height empty rows rather than panicking or leaving content
// unclipped -- lipgloss's Width/MaxWidth style keys both treat 0 as
// "unset", so callers must not rely on a zero-width Style to blank content.
func (p *Picker) View(width, height int) string {
	if height < 1 {
		height = 1
	}
	if width <= 0 {
		return strings.Join(make([]string, height), "\n")
	}
	rowStyle := widthStyle(width)

	lines := make([]string, height)

	if len(p.filtered) == 0 {
		placeholderStyle := lipgloss.NewStyle().Foreground(p.palette.DimText).Italic(true)
		lines[0] = rowStyle.Render(placeholderStyle.Render(pickerEmptyPlaceholder))
		for i := 1; i < height; i++ {
			lines[i] = rowStyle.Render("")
		}
		return strings.Join(lines, "\n")
	}

	offset := scrollOffset(p.cursor, len(p.filtered), height)
	for row := 0; row < height; row++ {
		idx := offset + row
		if idx >= len(p.filtered) {
			lines[row] = rowStyle.Render("")
			continue
		}
		lines[row] = rowStyle.Render(p.renderRow(p.filtered[idx], idx == p.cursor))
	}
	return strings.Join(lines, "\n")
}

// renderRow renders one item's plain (unpadded) content: an optional
// Marker, the Label, and a dim Hint, with the Label (and Marker) highlighted
// when current is true -- current denotes the cursor row, not focus (this
// widget doesn't track focus; see the package doc's Adaptations section).
func (p *Picker) renderRow(item PickerItem, current bool) string {
	labelStyle := lipgloss.NewStyle().Foreground(p.palette.Text)
	if current {
		labelStyle = lipgloss.NewStyle().Foreground(p.palette.Accent).Bold(true)
	}
	hintStyle := lipgloss.NewStyle().Foreground(p.palette.DimText)

	var b strings.Builder
	if item.Marker != "" {
		b.WriteString(labelStyle.Render(item.Marker))
		b.WriteString(" ")
	}
	b.WriteString(labelStyle.Render(item.Label))
	if item.Hint != "" {
		b.WriteString("  ")
		b.WriteString(hintStyle.Render(item.Hint))
	}
	return b.String()
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
