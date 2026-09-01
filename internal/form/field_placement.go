// field_placement.go is written fresh for this task, per the task-17
// brief's own provenance note ("TitleField and PlacementField are written
// fresh") -- it is NOT derived from atrium (github.com/ZviBaratz/atrium).
// It builds entirely on widgets.ChipRow (Task 14, ported from Atrium's own
// ui/overlay/chiprow.go), which already supplies the wrapping cursor and
// inert/placeholder mechanics this field needs.
package form

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/ZviBaratz/herdr-draft/internal/form/widgets"
	"github.com/ZviBaratz/herdr-draft/internal/plan"
	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// placementInertHint is SetWorktreeOn(true)'s own explanatory placeholder
// -- plan/build.go's own doc comment on Placement: "Placement is ignored
// when Input.UseWorktree is set -- worktree creation always opens a new
// workspace regardless of Placement."
const placementInertHint = "worktree opens as its own space"

// placementChips are spec §6 field 5's three options, in order; "new"
// (index 0, plan.PlacementNewSpace, the zero value) is what
// SetWorktreeOn(true) snaps the selection back to -- see SetWorktreeOn's
// own doc comment.
var placementChips = []widgets.Chip{
	{ID: "new", Label: "New space"},
	{ID: "tab", Label: "Tab here"},
	{ID: "split", Label: "Split here"},
}

// PlacementField is the form's Placement Section (spec §6 field 5): a
// three-chip row selecting where a non-worktree creation attaches
// relative to the invoking pane (internal/plan.Placement). It renders at a
// constant 2 physical lines (the chip row, plus an always-reserved second
// line -- ChipRow.View's own line count varies with whether the selected
// chip carries a FocusHint or the row is inert, so this field's own
// wrapper unconditionally pads to two lines, matching this task's
// "verified fact": "your Section.Height() must be hint-independent").
type PlacementField struct {
	chips      *widgets.ChipRow
	focused    bool
	worktreeOn bool
}

// NewPlacementField returns a PlacementField with "New space" selected,
// styled from palette.
func NewPlacementField(palette theme.Palette) *PlacementField {
	f := &PlacementField{chips: widgets.NewChipRow(palette)}
	f.chips.SetChips(placementChips)
	return f
}

// ID identifies this Section for form.go's zoneFor.
func (f *PlacementField) ID() string { return "placement" }

// Enabled reports whether Placement currently takes a real focus stop:
// false while worktree is on -- present-but-inert (form.go's Section doc
// comment), since Placement is meaningless once worktree creation always
// opens a new workspace regardless of it.
func (f *PlacementField) Enabled() bool { return !f.worktreeOn }

// Focus gives the field input focus. ChipRow has no Focus/Blur of its own
// (widgets/chiprow.go's own package doc) -- focused is tracked only for
// Section-interface completeness, matching form_test.go's own
// chipRowSection stub.
func (f *PlacementField) Focus() tea.Cmd {
	f.focused = true
	return nil
}

// Blur removes input focus.
func (f *PlacementField) Blur() { f.focused = false }

// Update moves the chip cursor on Left/Up (Prev) or Right/Down (Next) --
// both are no-ops while the row is inert (widgets.ChipRow.Next/Prev's own
// contract), and every other message is ignored: MapKey never forwards
// Tab/Enter/Esc/etc. here as ActionNone (ZonePlacement is a plain,
// non-picker, non-title, non-prompt zone), so nothing else is expected to
// reach this Update.
func (f *PlacementField) Update(msg tea.Msg) tea.Cmd {
	km, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	switch km.String() {
	case "left", "up":
		f.chips.Prev()
	case "right", "down":
		f.chips.Next()
	}
	return nil
}

// SetWorktreeOn toggles Placement's inert state: while on is true, the
// chip row shows placementInertHint in place of the chips (widgets.ChipRow
// .SetInert) and Enabled() reports false (present-but-inert). It also
// snaps the selection back to "New space" -- via SetChips, which resets
// the cursor to index 0 BEFORE SetInert engages (Next/Prev alone could not
// do this: ChipRow refuses to move its cursor while inert) -- so Value()
// reads PlacementNewSpace for as long as the field stays inert, matching
// what the surrounding plan actually does with a worktree creation
// (ignores Placement outright).
func (f *PlacementField) SetWorktreeOn(on bool) {
	f.worktreeOn = on
	if on {
		f.chips.SetChips(placementChips)
	}
	f.chips.SetInert(on, placementInertHint)
}

// SetValue moves the chip cursor directly to the chip matching v -- e.g.
// to apply spec §12's config.toml `default_placement` value at form
// construction. SetChips (NewPlacementField) always starts the cursor at
// index 0 ("New space", plan.PlacementNewSpace's own zero value), so this
// only needs to move it for a configured value OTHER than the default;
// walking forward with ChipRow's own wrapping Next() is safe here (unlike
// a non-wrapping Picker) because placementChips is a small, fixed,
// three-entry list -- at most two Next() calls always reaches any of the
// three, and a no-op while inert (widgets.ChipRow.Next()'s own contract)
// simply leaves the cursor wherever it already was, matching this
// field's other setters' "present but not yet meaningful" posture.
//
// Added in Task 20b (the app layer) alongside AccountField.SetPin -- see
// field_title.go's SetTitle doc comment for the fuller writeup of this
// class of gap: a config-derived default value with no way to
// pre-select the field it configures.
func (f *PlacementField) SetValue(v plan.Placement) {
	id := placementChipID(v)
	for range placementChips {
		if f.chips.Selected().ID == id {
			return
		}
		f.chips.Next()
	}
}

// placementChipID translates plan's own Placement enum to this field's
// internal placementChips IDs -- the inverse of Value()'s own switch.
func placementChipID(v plan.Placement) string {
	switch v {
	case plan.PlacementTabHere:
		return "tab"
	case plan.PlacementSplitHere:
		return "split"
	default:
		return "new"
	}
}

// Value returns the chip currently under the cursor, translated to
// internal/plan's own Placement enum. An unrecognized (or absent) chip ID
// defaults to PlacementNewSpace, the safe/zero-value choice.
func (f *PlacementField) Value() plan.Placement {
	switch f.chips.Selected().ID {
	case "tab":
		return plan.PlacementTabHere
	case "split":
		return plan.PlacementSplitHere
	default:
		return plan.PlacementNewSpace
	}
}

// Height reports PlacementField's constant two-line footprint --
// independent of winH, focus, selection, or inert state.
func (f *PlacementField) Height(int) int { return 2 }

// View renders the chip row, padding to exactly two physical lines when
// ChipRow.View itself only produced one (no FocusHint on the selected
// chip, and not inert) -- see the type doc comment.
func (f *PlacementField) View(inner int) string {
	if inner < 1 {
		inner = 1
	}
	v := f.chips.View(inner)
	if !strings.Contains(v, "\n") {
		v += "\n" + fitLine("", inner)
	}
	return v
}
