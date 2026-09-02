// field_agent.go is written fresh for this task -- there is no Atrium
// agent-kind field at all (Atrium is Claude-only; spec §16 non-goal 3
// explicitly calls out not re-shipping "the Claude-shaped form" Atrium's
// own retrospective flagged as a mistake). It builds on widgets.ChipRow
// (the favorites row) and widgets.Picker (the full kind list "behind" it),
// both Task 14 ports, the same "compose two existing widgets, own the
// glue" shape every other Task 17-18 field already uses.
//
// Design note on the single SetKinds setter (flagged for controller
// review): the brief's own interfaces line gives AgentField exactly one
// data setter, SetKinds([]string) ("populated by app layer from config +
// the spec's known 23"), with no separate favorites-vs-full-list split at
// the API boundary. AgentField resolves this by treating SetKinds' own
// ORDER as the signal: the app layer is expected to order its favorites
// first (config's own `[agents] favorites` list, spec §12), followed by
// the remaining known kinds -- the same "index 0 is the default" contract
// field_placement.go's PlacementField and field_worktree.go's HEAD-first
// base list already rely on callers to honor. AgentField then shows the
// leading agentFavoriteChips entries directly as chips (a config with
// fewer favorites than that just gets fewer chips -- SetChips handles a
// short slice fine), appends a synthetic "more…" chip when the full list
// is longer than that, and makes the ENTIRE SetKinds list (not just the
// excess) reachable through it -- so a favorite is always still pickable
// from the expanded list too, and there is no "how do I get back to a
// favorite after expanding" dead end to design around.
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

// agentPickerRows is the full-kind-list picker's fixed row count, reserved
// unconditionally so Height() stays constant whether or not the list is
// currently expanded -- the same "always reserve, sometimes render blank"
// discipline field_dir.go's DirField uses for focus, applied here to an
// internal expanded/collapsed toggle instead (see Height's own doc
// comment: the contract is about focus-independence specifically, not
// every kind of internal state).
const agentPickerRows = 4

// agentMoreChipID is the synthetic "more…" chip's internal Chip.ID -- a
// leading-NUL sentinel that can never collide with a real agent kind name
// (herdr kind identifiers are plain lowercase words, e.g. "claude",
// "codex"; none begin with a NUL byte), matching field_issue.go's
// issueNoneID and field_worktree.go's baseHeadID sentinel discipline.
const agentMoreChipID = "\x00more"

const agentMoreLabel = "more…"

const (
	// agentRowLabel is v2's row label. v1 rendered this field with no
	// label at all (its View is a bare chip row); v2 spec §6 gives every
	// row one.
	agentRowLabel = "agent"
	// agentRowUnset is what the row reads before SetKinds has supplied
	// anything -- v2 spec §6's table has no unset state for this field
	// because production always populates it, but a zero-value field must
	// still render something honest rather than an empty cell.
	agentRowUnset = "—"
	// agentPanelMaxRows caps PanelRows: the chip row plus a full kind
	// list, which spec §6 sizes at the known 23 kinds, is more than a
	// panel should claim from the rest of the form.
	agentPanelMaxRows = 10
	// agentPanelEmpty speaks in the field's own terms when there is
	// nothing to pick (v2 spec §6.1's "nothing to choose", never a bare
	// "no matches").
	agentPanelEmpty = "no agent kinds configured"
)

// AgentField is the form's Agent Section (spec §6 field 6): a row of
// favorite chips plus a "more…" chip that expands into a full-kind-list
// picker. Value() always returns a real kind name, never the internal
// "more…" sentinel -- see the file doc's design note.
//
// AgentField PREFERS 2+agentPickerRows physical lines -- independent of
// focus, kind set, and expanded state (this task's own "verified fact":
// Section.Height must be hint-independent) -- and renders into whatever
// compose's own budget allocation gives it (Section.View's h, sizes.go's
// allocateHeights), shedding rows from the bottom: the chip row first,
// then a reserved second line (field_placement.go's own
// hint-independent-Height pattern, applied here even though no chip
// currently sets a FocusHint -- reserved defensively in case one later
// does), then candidate rows (blank while collapsed).
type AgentField struct {
	chips  *widgets.ChipRow
	picker *widgets.Picker

	// palette is retained for v2's Row/Panel, which style dim text
	// themselves; v1's View delegates every style to ChipRow/Picker and
	// never reads it.
	palette theme.Palette

	focused  bool
	expanded bool

	kinds         []string
	lastConfirmed string

	// pickerRowsShown is how many kind rows the last Panel render drew.
	// widgets.Picker.SelectAt needs the SAME height MarkedView was called
	// with to map a click back to an item, and unlike v1's fixed
	// agentPickerRows the v2 panel's list height varies with the window.
	pickerRowsShown int

	// pickerVersion is bumped on every setPickerItems call so
	// widgets.Picker.SetItems always sees a strictly newer version --
	// deliberately never relying on its same-version preserve-by-ID
	// behavior here (see expand's own doc comment: this field always
	// wants explicit, deterministic control over where the cursor lands,
	// not whatever the picker's own last-remembered selection happened to
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
// so ↑↓ meant "move the chips" until a "more…" chip expanded the list and
// then meant "move the list", with ↑ at the top collapsing back. Nothing
// in v2 is collapsed, so nothing has two meanings, and the "more…" chip
// that opened the list has no job left -- SetKinds stopped emitting it.
func (f *AgentField) Update(msg tea.Msg) tea.Cmd {
	if click, ok := msg.(tea.MouseClickMsg); ok {
		f.handleClick(click)
		return nil
	}
	if wheel, ok := msg.(tea.MouseWheelMsg); ok {
		// The wheel scrolls the kind list (spec §7: "scroll the focused
		// picker") -- the favorite chip row is a small, always-fully-
		// visible set with nothing to scroll.
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

// pickerAtTop reports whether the full-list picker's cursor is already on
// its first row (or the list is empty) -- Up from here collapses instead
// of moving the cursor.
func (f *AgentField) pickerAtTop() bool {
	sel, ok := f.picker.Selected()
	if !ok {
		return true
	}
	items := f.fullListItems()
	return len(items) == 0 || items[0].ID == sel.ID
}

// expand switches to the full-list picker, seeding its cursor at
// lastConfirmed when that kind is present in the full list (0 otherwise)
// -- so opening the list never silently loses the currently effective
// selection. setPickerItems always resets the cursor to row 0 first
// (a fresh, strictly-newer version every call -- see pickerVersion's own
// doc comment), so the CursorNext walk below always starts from a known
// baseline rather than wherever widgets.Picker's own same-version
// preserve-by-ID logic might otherwise have left it.
func (f *AgentField) expand() {
	f.expanded = true
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
	if sel := f.chips.Selected(); sel.ID != "" && sel.ID != agentMoreChipID {
		f.lastConfirmed = sel.ID
	}
}

func (f *AgentField) syncConfirmedFromPicker() {
	if sel, ok := f.picker.Selected(); ok {
		f.lastConfirmed = sel.ID
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
	f.expanded = false

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
	// expand() seeds the kind list's cursor from lastConfirmed, so set
	// that first. Its own `expanded` flag is v1's and means nothing here:
	// v2's panel shows the list whether or not anything "expanded" it.
	f.lastConfirmed = kind
	f.expand()
}

// fullListItems builds the full-kind-list picker's item set from f.kinds
// -- every configured kind is reachable here, not just the ones excluded
// from the chip row (see the file doc's design note).
func (f *AgentField) fullListItems() []widgets.PickerItem {
	items := make([]widgets.PickerItem, len(f.kinds))
	for i, k := range f.kinds {
		items[i] = widgets.PickerItem{ID: k, Label: k}
	}
	return items
}

// Value returns the currently effective agent kind -- never the internal
// "more…" sentinel (see the file doc's design note) and never "" once
// SetKinds has been given a non-empty list; "" only for a freshly
// constructed field SetKinds has not yet populated (zero-value safety: no
// panic, just an unresolved kind the app layer's own submit-time
// validation is expected to catch, the same "verdicts never disable
// submit" posture spec §6 field 3 documents for Title).
func (f *AgentField) Value() string { return f.lastConfirmed }

// --- v2 row stack (form.go's rowSection) ---------------------------------
//
// Added ALONGSIDE View/Height/MinHeight; see field_title.go's identical
// section comment for why their arrival moves no golden frame.
//
// v2 retires the expand/collapse mechanism (expanded, expand,
// pickerAtTop, agentMoreChipID) -- Panel shows the favorites and the full
// kind list at the same time, so there is nothing to expand INTO. Those
// members survive here only because v1's View and Update still drive
// them; deleting them is part of the same change that deletes v1's path.

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

// Height reports AgentField's PREFERRED footprint in a popup winH rows
// tall -- independent of focus, kind set, and expanded state (see the type
// doc comment); the full-kind-list row count shrinks with winH via
// pickerRowsAt (sizes.go).
func (f *AgentField) Height(winH int) int {
	return pickerChromeRows + pickerRowsAt(agentPickerRows, winH)
}

// MinHeight is the chip row alone -- the selected agent kind, which is
// what a user not currently editing this field needs to see of it.
func (f *AgentField) MinHeight() int { return 1 }

// View renders the field into exactly h physical lines: the chip row
// first, then the reserved hint row, then whatever full-kind-list rows are
// left over (real rows while expanded, blanks otherwise).
func (f *AgentField) View(inner, h int) string {
	if inner < 1 {
		inner = 1
	}
	chipView := f.chips.MarkedView(inner, "chip:"+f.ID()+":")
	if !strings.Contains(chipView, "\n") {
		chipView += "\n" + fitLine("", inner)
	}

	rows := strings.Split(fitBlock(chipView, pickerChromeRows, inner), "\n")
	if candidates := h - pickerChromeRows; candidates > 0 {
		if f.expanded {
			rows = append(rows, strings.Split(f.picker.MarkedView(inner, candidates, "row:"+f.ID()+":"), "\n")...)
		} else {
			rows = append(rows, blankRows(candidates, inner)...)
		}
	}
	return sectionLines(h, inner, rows...)
}
