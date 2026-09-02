// field_agent.go is written fresh for this task -- there is no Atrium
// agent-kind field at all (Atrium is Claude-only; spec §16 non-goal 3
// explicitly calls out not re-shipping "the Claude-shaped form" Atrium's
// own retrospective flagged as a mistake). It builds on widgets.ChipRow
// (the favorites row) and widgets.Picker (the full kind list "behind" it),
// both Task 14 ports, the same "compose two existing widgets, own the
// glue" shape every other Task 17-18 field already uses.
//
// Design note on the single SetKinds setter: the field takes exactly one
// data setter, SetKinds([]string) (populated by the app layer from config
// plus the spec's known 23), with no separate favorites-vs-full-list
// split at the API boundary. AgentField resolves that by treating
// SetKinds' own ORDER as the signal: the app layer orders its favorites
// first (config's own `[agents] favorites` list, spec §12), followed by
// the remaining known kinds -- the same "index 0 is the default" contract
// field_placement.go's PlacementField and field_worktree.go's HEAD-first
// base list already rely on callers to honor. The leading
// agentFavoriteChips entries become chips (a config with fewer favorites
// than that just gets fewer chips) and the ENTIRE list, favorites
// included, is in the panel's picker below them -- so a favorite is
// always pickable both ways, and there is no "how do I get back to a
// favorite" dead end to design around.
package form

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/ZviBaratz/herdr-draft/internal/form/widgets"
	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// agentFavoriteChips is the max number of SetKinds' own leading entries
// shown directly as chips before a "more…" chip takes over for the rest --
// see the file doc's design note. Sized to comfortably fit spec §12's own
// example config (`favorites = ["claude", "codex"]`) plus a little room to
// grow before the row needs horizontal scrolling ChipRow doesn't have.
const agentFavoriteChips = 3

const (
	// agentRowLabel is v2's row label. v1 rendered this field with no
	// label at all (its View is a bare chip row); v2 spec §6 gives every
	// row one.
	agentRowLabel = "agent"
	// agentRowUnset is what the row reads before SetKinds has supplied
	// anything -- v2 spec §6's table has no unset state for this field
	// because production always populates it, but a zero-value field must
	// still render something honest rather than an empty cell.
	agentRowUnset = rowValueNone
	// agentPanelMaxRows caps PanelRows: the chip row plus a full kind
	// list, which spec §6 sizes at the known 23 kinds, is more than a
	// panel should claim from the rest of the form.
	agentPanelMaxRows = 10
	// agentPanelEmpty speaks in the field's own terms when there is
	// nothing to pick (v2 spec §6.1's "nothing to choose", never a bare
	// "no matches").
	agentPanelEmpty = "no agent kinds configured"
)

// AgentField is the form's Agent Section (spec §6 field 6): a one-line
// row naming the selected kind, over a panel holding the favorite chips
// and the full kind list at once -- see the file doc's design note.
type AgentField struct {
	chips  *widgets.ChipRow
	picker *widgets.Picker

	palette theme.Palette

	focused bool

	kinds         []string
	lastConfirmed string

	// pickerRowsShown is how many kind rows the last Panel render drew.
	// widgets.Picker.SelectAt needs the SAME height MarkedView was called
	// with to map a click back to an item, and the panel's list height
	// varies with the window.
	pickerRowsShown int

	// pickerVersion is bumped on every setPickerItems call so
	// widgets.Picker.SetItems always sees a strictly newer version --
	// deliberately never relying on its same-version preserve-by-ID
	// behavior here (see seedPickerCursor: this field always wants
	// explicit, deterministic control over where the cursor lands, not
	// whatever the picker's own last-remembered selection happened to
	// be).
	pickerVersion int
}

// NewAgentField returns an empty AgentField (no kinds yet -- see
// SetKinds) styled from palette.
func NewAgentField(palette theme.Palette) *AgentField {
	return &AgentField{
		chips:   widgets.NewChipRow(palette),
		picker:  widgets.NewPicker(palette),
		palette: palette,
	}
}

// ID identifies this Section for form.go's zoneFor.
func (f *AgentField) ID() string { return "agent" }

// Enabled reports that Agent is always present -- spec §6 field 6 has no
// precondition that could ever make it unavailable.
func (f *AgentField) Enabled() bool { return true }

// Focus gives the field input focus. Neither ChipRow nor Picker owns a
// Focus/Blur of its own (see their package doc); focused is tracked only
// for Section-interface completeness, matching field_placement.go's
// PlacementField.
func (f *AgentField) Focus() tea.Cmd {
	f.focused = true
	return nil
}

// Blur removes input focus.
func (f *AgentField) Blur() { f.focused = false }

// Update implements v2's agent grammar, which needs no modes because the
// panel shows both halves at once (v2 spec §6, "the more… list moves into
// the panel"): ←→ move the favorite chip cursor, ↑↓ move the full
// kind-list picker's, and either one confirms the kind it lands on.
//
// v1 had to arbitrate: its chip row and its list occupied the same rows,
// so ↑↓ meant "move the chips" until a "more…" chip revealed the list and
// then meant "move the list", with ↑ at the top collapsing back. Nothing
// in v2 is collapsed, so nothing has two meanings, and the "more…" chip
// that opened the list has no job left -- SetKinds stopped emitting it.
func (f *AgentField) Update(msg tea.Msg) tea.Cmd {
	if click, ok := msg.(tea.MouseClickMsg); ok {
		f.handleClick(click)
		return nil
	}
	if wheel, ok := msg.(tea.MouseWheelMsg); ok {
		// The wheel scrolls the kind list (v2 spec §7: "the wheel
		// scrolls the panel unconditionally") -- the favorite chip row is
		// a small, always-fully-visible set with nothing to scroll.
		switch wheelDelta(wheel) {
		case -1:
			f.picker.CursorPrev()
			f.syncConfirmedFromPicker()
		case 1:
			f.picker.CursorNext()
			f.syncConfirmedFromPicker()
		}
		return nil
	}
	km, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	switch km.String() {
	case "up":
		f.picker.CursorPrev()
		f.syncConfirmedFromPicker()
	case "down":
		f.picker.CursorNext()
		f.syncConfirmedFromPicker()
	case "left":
		f.chips.Prev()
		f.syncConfirmedFromChip()
	case "right":
		f.chips.Next()
		f.syncConfirmedFromChip()
	}
	return nil
}

// handleClick implements task 21's mouse click over AgentField: a click
// on one of the kind list's own "row:agent:<n>" zones selects that row
// (SelectAt, the click-driven counterpart to ↑↓), and a click on one of
// the favorite chip row's own "chip:agent:<chipID>" zones selects that
// favorite (the counterpart to ←→). Both halves are always on screen, so
// unlike v1 there is no mode deciding which of the two a click can mean.
func (f *AgentField) handleClick(msg tea.MouseClickMsg) {
	if _, ok := f.chips.SelectAt(msg, "chip:"+f.ID()+":"); ok {
		f.syncConfirmedFromChip()
		return
	}
	if f.pickerRowsShown > 0 {
		if _, ok := f.picker.SelectAt(msg, f.pickerRowsShown, "row:"+f.ID()+":"); ok {
			f.syncConfirmedFromPicker()
		}
	}
}

// seedPickerCursor moves the kind list's cursor onto lastConfirmed when
// that kind is present, so a programmatic SetKind and the panel's own
// list never disagree about what is selected. setPickerItems always
// resets the cursor to row 0 first (a fresh, strictly-newer version every
// call -- see pickerVersion's own doc comment), so the CursorNext walk
// below always starts from a known baseline.
func (f *AgentField) seedPickerCursor() {
	items := f.fullListItems()
	f.setPickerItems(items)
	for i, it := range items {
		if it.ID == f.lastConfirmed {
			for j := 0; j < i; j++ {
				f.picker.CursorNext()
			}
			return
		}
	}
}

// setPickerItems feeds items to the wrapped Picker at a fresh, strictly
// increasing version, guaranteeing widgets.Picker.SetItems always takes
// its "new version" branch (cursor reset to row 0) rather than its
// same-version preserve-by-ID branch -- see pickerVersion's own doc
// comment for why this field never wants the latter.
func (f *AgentField) setPickerItems(items []widgets.PickerItem) {
	f.pickerVersion++
	f.picker.SetItems(f.pickerVersion, items)
}

func (f *AgentField) syncConfirmedFromChip() {
	if sel := f.chips.Selected(); sel.ID != "" {
		f.lastConfirmed = sel.ID
		f.refreshCurrent()
	}
}

func (f *AgentField) syncConfirmedFromPicker() {
	if sel, ok := f.picker.Selected(); ok {
		f.lastConfirmed = sel.ID
		f.refreshCurrent()
	}
}

// SetKinds replaces the field's ordered kind list -- see the file doc's
// design note for how favorites-vs-full-list is derived from this single
// list's own order. Resets the chip cursor to index 0 (kinds[0], spec
// §12's own configured default).
//
// The chip row carries the leading favorites and NOTHING ELSE. v1 also
// appended a synthetic "more…" chip, whose whole job was to open the full
// kind list; v2's panel shows that list permanently, one line below the
// chips, so a chip meaning "show the thing already on screen" is a
// control with no effect.
func (f *AgentField) SetKinds(kinds []string) {
	f.kinds = append([]string(nil), kinds...)

	chips := make([]widgets.Chip, 0, agentFavoriteChips)
	for i, k := range f.kinds {
		if i >= agentFavoriteChips {
			break
		}
		chips = append(chips, widgets.Chip{ID: k, Label: k})
	}
	f.chips.SetChips(chips)

	f.lastConfirmed = ""
	if len(f.kinds) > 0 {
		f.lastConfirmed = f.kinds[0]
	}
	f.setPickerItems(f.fullListItems())
}

// SetKind selects kind as the field's current value, e.g. to apply the
// last agent kind the user actually launched with (config.State.LastKind,
// spec §12's last-used.json). A kind not present in the current SetKinds
// list, and "" , are both no-ops -- never override the configured default
// with a guess at a stale or typo'd value, matching AccountField.SetPin's
// own posture for the same class of persisted-preference input.
//
// A kind that has its own chip moves the chip cursor to it; either way
// the kind list's own cursor is moved there too, so the panel's two
// halves never disagree about what is selected.
//
// Added alongside the state-persistence wiring (finding I2): SetKinds'
// "index 0 is the default" contract could express only the CONFIGURED
// default, so LastKind had nowhere to be applied and last-used.json's
// `kind` field was written by nobody and read by nobody.
func (f *AgentField) SetKind(kind string) {
	if kind == "" {
		return
	}
	known := false
	for _, k := range f.kinds {
		if k == kind {
			known = true
			break
		}
	}
	if !known {
		return
	}

	f.chips.SelectID(kind) // a no-op for a kind with no chip of its own
	// seedPickerCursor reads lastConfirmed, so set it first.
	f.lastConfirmed = kind
	f.seedPickerCursor()
}

// fullListItems builds the full-kind-list picker's item set from f.kinds
// -- every configured kind is reachable here, not just the ones excluded
// from the chip row (see the file doc's design note).
//
// Current marks lastConfirmed, and this is the field v3 spec §8.2 calls
// out by name: THE CURSOR IS NOT THE VALUE HERE. ←→ move the chip row
// and lastConfirmed with it (syncConfirmedFromChip) without touching the
// picker cursor, which only SetKind re-seeds -- so before v3 the panel
// highlighted one kind while the stack row one line above it named
// another, with nothing on screen saying which one would actually
// launch. The ✓ this puts in the mark column is that missing statement.
func (f *AgentField) fullListItems() []widgets.PickerItem {
	items := make([]widgets.PickerItem, len(f.kinds))
	for i, k := range f.kinds {
		items[i] = widgets.PickerItem{ID: k, Cells: []string{k}, Current: k == f.lastConfirmed}
	}
	return items
}

// refreshCurrent re-feeds the kind list at the SAME picker version so
// each item's Current flag catches up with lastConfirmed. Same version is
// the point: widgets.Picker.SetItems preserves the selection by ID on a
// same-version refresh, so the cursor stays exactly where the user left
// it while the ✓ moves -- which is the whole distinction Current draws.
//
// A flag on an item has to be re-supplied to change, so every path that
// moves lastConfirmed without rebuilding the list has to come through
// here; seedPickerCursor and SetKinds rebuild it themselves.
func (f *AgentField) refreshCurrent() {
	f.picker.SetItems(f.pickerVersion, f.fullListItems())
}

// Value returns the currently effective agent kind -- never the internal
// "more…" sentinel (see the file doc's design note) and never "" once
// SetKinds has been given a non-empty list; "" only for a freshly
// constructed field SetKinds has not yet populated (zero-value safety: no
// panic, just an unresolved kind the app layer's own submit-time
// validation is expected to catch, the same "verdicts never disable
// submit" posture spec §6 field 3 documents for Title).
func (f *AgentField) Value() string { return f.lastConfirmed }

// --- the row and its panel ------------------------------------------------

// Label is v2's row label (v2 spec §6's field table).
func (f *AgentField) Label() string { return agentRowLabel }

// Row is the selected kind, nothing else (v2 spec §6: `claude`). The
// favorites and the full list both live in the panel, so the row does not
// have to choose between showing the value and showing the choices.
func (f *AgentField) Row(w int) string {
	if w < 1 {
		w = 1
	}
	if v := f.Value(); v != "" {
		return fitLine(lipgloss.NewStyle().Foreground(f.palette.Text).Render(keepHead(v, w)), w)
	}
	return fitLine(dimText(f.palette).Render(agentRowUnset), w)
}

// Panel is the favorites chip row on line 0 and the full kind list
// beneath it, BOTH always visible (v2 spec §6: "the more… list moves into
// the panel"). ←→ drive the chips and ↑↓ the list, which is the whole
// reason the two can share a panel without a second focus level.
func (f *AgentField) Panel(w, h int) string {
	if h < 1 {
		h = 1
	}
	chips := f.chips.MarkedView(panelChipWidth(w), "chip:"+f.ID()+":")
	if idx := strings.IndexByte(chips, '\n'); idx >= 0 {
		chips = chips[:idx]
	}
	lines := []string{panelChipRow(chips)}

	f.pickerRowsShown = 0
	if h > 1 {
		if len(f.kinds) == 0 {
			lines = append(lines, panelText(dimHint(f.palette).Render(agentPanelEmpty), w))
		} else {
			f.pickerRowsShown = h - 1
			lines = append(lines, panelPickerLines(f.picker, w, h-1, "row:"+f.ID()+":", f.palette)...)
		}
	}
	return panelBlock(w, h, lines...)
}

// PanelRows is the chip row plus one line per known kind, capped at
// agentPanelMaxRows. An empty kind set still asks for two, so the "no
// agent kinds configured" line has somewhere to land.
func (f *AgentField) PanelRows() int {
	if len(f.kinds) == 0 {
		return 2
	}
	return capRows(1+len(f.kinds), agentPanelMaxRows)
}

// FooterRungs implements form.go's footerHinter for the state
// footer.go's per-ZONE table cannot see: with no kinds configured there
// are no chips and no list, so the table's "←→ favorites · ↑↓ all kinds"
// names two controls that are not on the screen. Same judgement, and the
// same sentence, as field_worktree.go's non-git rung.
func (f *AgentField) FooterRungs() []string {
	if len(f.kinds) == 0 {
		return []string{"nothing to set here"}
	}
	return nil
}
