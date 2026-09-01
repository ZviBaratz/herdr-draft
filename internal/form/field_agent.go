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

// AgentField is the form's Agent Section (spec §6 field 6): a row of
// favorite chips plus a "more…" chip that expands into a full-kind-list
// picker. Value() always returns a real kind name, never the internal
// "more…" sentinel -- see the file doc's design note.
//
// AgentField renders at a CONSTANT 2+agentPickerRows physical lines
// regardless of focus, kind set, or expanded state (this task's own
// "verified fact": Section.Height must be hint-independent) -- the chip
// row, an always-reserved second line (field_placement.go's own
// hint-independent-Height pattern, applied here even though no chip
// currently sets a FocusHint -- reserved defensively in case one later
// does), then agentPickerRows candidate rows (blank while collapsed).
type AgentField struct {
	chips  *widgets.ChipRow
	picker *widgets.Picker

	focused  bool
	expanded bool

	kinds         []string
	lastConfirmed string

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
		chips:  widgets.NewChipRow(palette),
		picker: widgets.NewPicker(palette),
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

// Update handles Up/Down/Left/Right: while collapsed, Left/Up moves the
// chip cursor back and Right/Down moves it forward, EXCEPT that Down on
// the "more…" chip specifically expands the field instead of wrapping
// past it (widgets.ChipRow's own Next() would otherwise wrap straight
// back to the first favorite, defeating the whole point of the "more…"
// chip); while expanded, Up/Down move the full-list picker's own cursor,
// with Up at the top row collapsing back to the chip row (parked on
// "more…", where it already sits -- expand() never moves the chip
// cursor). Left/Right are no-ops while expanded: the user's attention is
// inside the vertical list, and there is nothing left/right to navigate
// to there.
func (f *AgentField) Update(msg tea.Msg) tea.Cmd {
	if click, ok := msg.(tea.MouseClickMsg); ok {
		f.handleClick(click)
		return nil
	}
	if wheel, ok := msg.(tea.MouseWheelMsg); ok {
		// Wheel only has meaning over the expanded full-list picker (spec
		// §7: "scroll the focused picker") -- the favorite chip row is a
		// small, always-fully-visible set, nothing to scroll.
		if f.expanded {
			switch wheelDelta(wheel) {
			case -1:
				f.picker.CursorPrev()
				f.syncConfirmedFromPicker()
			case 1:
				f.picker.CursorNext()
				f.syncConfirmedFromPicker()
			}
		}
		return nil
	}
	km, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	switch km.String() {
	case "up":
		if f.expanded {
			if f.pickerAtTop() {
				f.expanded = false
			} else {
				f.picker.CursorPrev()
				f.syncConfirmedFromPicker()
			}
		} else {
			f.chips.Prev()
			f.syncConfirmedFromChip()
		}
	case "down":
		switch {
		case f.expanded:
			f.picker.CursorNext()
			f.syncConfirmedFromPicker()
		case f.chips.Selected().ID == agentMoreChipID:
			f.expand()
		default:
			f.chips.Next()
			f.syncConfirmedFromChip()
		}
	case "left":
		if !f.expanded {
			f.chips.Prev()
			f.syncConfirmedFromChip()
		}
	case "right":
		if !f.expanded {
			f.chips.Next()
			f.syncConfirmedFromChip()
		}
	}
	return nil
}

// handleClick implements task 21's mouse click over AgentField: while
// expanded, a click on one of the full-list picker's own
// "row:agent:<n>" zones selects that row (SelectAt), the click-driven
// counterpart to Up/Down's own CursorPrev/CursorNext+
// syncConfirmedFromPicker pairing above; while collapsed, a click on one
// of the favorite chip row's own "chip:agent:<chipID>" zones either
// expands the field (clicking the synthetic "more…" chip -- the
// click-driven counterpart to Down on "more…" in Update above) or
// selects that favorite (syncConfirmedFromChip, mirroring Left/Right).
func (f *AgentField) handleClick(msg tea.MouseClickMsg) {
	if f.expanded {
		if _, ok := f.picker.SelectAt(msg, agentPickerRows, "row:"+f.ID()+":"); ok {
			f.syncConfirmedFromPicker()
		}
		return
	}
	chip, ok := f.chips.SelectAt(msg, "chip:"+f.ID()+":")
	if !ok {
		return
	}
	if chip.ID == agentMoreChipID {
		f.expand()
		return
	}
	f.syncConfirmedFromChip()
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
// §12's own configured default) and collapses the full-list picker.
func (f *AgentField) SetKinds(kinds []string) {
	f.kinds = append([]string(nil), kinds...)
	f.expanded = false

	chips := make([]widgets.Chip, 0, agentFavoriteChips+1)
	for i, k := range f.kinds {
		if i >= agentFavoriteChips {
			break
		}
		chips = append(chips, widgets.Chip{ID: k, Label: k})
	}
	if len(f.kinds) > agentFavoriteChips {
		chips = append(chips, widgets.Chip{ID: agentMoreChipID, Label: agentMoreLabel})
	}
	f.chips.SetChips(chips)

	f.lastConfirmed = ""
	if len(f.kinds) > 0 {
		f.lastConfirmed = f.kinds[0]
	}
	f.setPickerItems(f.fullListItems())
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

// Height reports AgentField's constant footprint -- independent of winH,
// focus, kind set, or expanded state (see the type doc comment).
func (f *AgentField) Height(int) int { return 2 + agentPickerRows }

// View renders the field at exactly Height's own physical line count.
func (f *AgentField) View(inner int) string {
	if inner < 1 {
		inner = 1
	}
	chipView := f.chips.MarkedView(inner, "chip:"+f.ID()+":")
	if !strings.Contains(chipView, "\n") {
		chipView += "\n" + fitLine("", inner)
	}

	var rows string
	if f.expanded {
		rows = f.picker.MarkedView(inner, agentPickerRows, "row:"+f.ID()+":")
	} else {
		blanks := make([]string, agentPickerRows)
		for i := range blanks {
			blanks[i] = fitLine("", inner)
		}
		rows = strings.Join(blanks, "\n")
	}

	return chipView + "\n" + rows
}
