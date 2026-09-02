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
	"charm.land/lipgloss/v2"

	"github.com/ZviBaratz/herdr-draft/internal/form/widgets"
	"github.com/ZviBaratz/herdr-draft/internal/plan"
	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// placementInertHint is SetWorktreeOn(true)'s own explanatory placeholder
// -- plan/build.go's own doc comment on Placement: "Placement is ignored
// when Input.UseWorktree is set -- worktree creation always opens a new
// workspace regardless of Placement."
const placementInertHint = "worktree opens as its own space"

// placementInertPanelHint is what the PANEL says in the same state.
// It differs from the row's own sentence deliberately: the row states the
// consequence (v2 spec §3 rule 1) and the panel, being the chooser, says
// what would give the reader a choice back. Repeating one sentence twice,
// three lines apart, would have said neither.
const placementInertPanelHint = "turn the worktree off to choose"

// placementChips are spec §6 field 5's three options, in order; "new"
// (index 0, plan.PlacementNewSpace, the zero value) is what
// SetWorktreeOn(true) snaps the selection back to -- see SetWorktreeOn's
// own doc comment. Chip IDs match config.toml's own `default_placement`
// vocabulary ("tab-here"/"split-here", see placementFromConfigValue in
// internal/app/app.go) rather than a shorter internal-only spelling --
// unified in task 21 so this field's own task-21 mouse zone IDs
// ("chip:placement:tab-here", the "click on chip:placement:tab-here
// selects it" scenario the task brief names explicitly) read the same
// vocabulary a config author already uses, instead of a second,
// internal-only ID space for the exact same three concepts.
//
// Labels are lowercase and FocusHint carries each choice's one-line
// explanation (v2 spec §7's one widget-adjacent change: "populating
// Chip.FocusHint on the placement chips -- plain data, no new code").
// v1 could not have either: it capitalized the labels because its chip
// row was the field's whole rendering, and a populated FocusHint would
// have made ChipRow.View two lines tall in a fixed-height section.
var placementChips = []widgets.Chip{
	{ID: "new", Label: "new space", FocusHint: "opens a new workspace of its own"},
	{ID: "tab-here", Label: "tab here", FocusHint: "opens a tab beside this pane's tab"},
	{ID: "split-here", Label: "split here", FocusHint: "splits this pane in two"},
}

// placementRowLabel is v2's row label (v2 spec §6). It is also the widest
// label in the stack, and therefore what rowlayout.go's labelColWidth is
// sized against.
const placementRowLabel = "placement"

// PlacementField is the form's Placement Section (spec §6 field 5): a
// one-line row naming where a non-worktree creation will attach relative
// to the invoking pane (internal/plan.Placement), over a two-line panel
// holding the three chips and the selected chip's own explanation.
type PlacementField struct {
	chips      *widgets.ChipRow
	focused    bool
	palette    theme.Palette
	worktreeOn bool

	// provenance is SetProvenance's config-file name, "" for a selection no
	// config file chose. See SetProvenance.
	provenance string
}

// NewPlacementField returns a PlacementField with "New space" selected,
// styled from palette.
func NewPlacementField(palette theme.Palette) *PlacementField {
	f := &PlacementField{chips: widgets.NewChipRow(palette), palette: palette}
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
// Section-interface completeness.
func (f *PlacementField) Focus() tea.Cmd {
	f.focused = true
	return nil
}

// Blur removes input focus.
func (f *PlacementField) Blur() { f.focused = false }

// Update moves the chip cursor on Left/Up (Prev) or Right/Down (Next) --
// both are no-ops while the row is inert (widgets.ChipRow.Next/Prev's own
// contract) -- handles a task 21 left-button click on one of this row's
// own "chip:placement:<chipID>" zones (SelectAt, also a no-op while
// inert) -- and every other message is ignored: MapKey never forwards
// Tab/Enter/Esc/etc. here as ActionNone (ZonePlacement is a plain,
// non-picker, non-title, non-prompt zone), so nothing else is expected to
// reach this Update.
func (f *PlacementField) Update(msg tea.Msg) tea.Cmd {
	if click, ok := msg.(tea.MouseClickMsg); ok {
		f.chips.SelectAt(click, "chip:"+f.ID()+":")
		return nil
	}
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
	f.chips.SetInert(on, placementInertPanelHint)
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

// SetProvenance names the config file the current selection came from, so
// the panel can say so (v2 spec §11: `from .herdr-draft.toml`). "" -- the
// resting state -- is "nobody's file chose this", and the line is not
// reserved at all.
//
// It takes a plain FILE NAME, not a config value: this package performs no
// I/O and knows nothing of internal/config or internal/defaults, so which
// tier won is decided over there (defaults.Resolved.From) and only its
// answer is pushed in here. That is the same setter convention every other
// verdict in this package arrives by.
func (f *PlacementField) SetProvenance(source string) { f.provenance = source }

// placementChipID translates plan's own Placement enum to this field's
// internal placementChips IDs -- the inverse of Value()'s own switch.
func placementChipID(v plan.Placement) string {
	switch v {
	case plan.PlacementTabHere:
		return "tab-here"
	case plan.PlacementSplitHere:
		return "split-here"
	default:
		return "new"
	}
}

// Value returns the chip currently under the cursor, translated to
// internal/plan's own Placement enum. An unrecognized (or absent) chip ID
// defaults to PlacementNewSpace, the safe/zero-value choice.
func (f *PlacementField) Value() plan.Placement {
	switch f.chips.Selected().ID {
	case "tab-here":
		return plan.PlacementTabHere
	case "split-here":
		return plan.PlacementSplitHere
	default:
		return plan.PlacementNewSpace
	}
}

// --- the row and its panel ------------------------------------------------

// Label is v2's row label (v2 spec §6's field table).
func (f *PlacementField) Label() string { return placementRowLabel }

// Row names the selected placement in v2's own lowercase vocabulary, or
// -- while a worktree is on, which makes the choice meaningless -- states
// why in dim italic (v2 spec §6's Inert cell, the same sentence v1's
// inert chip row already carries).
func (f *PlacementField) Row(w int) string {
	if w < 1 {
		w = 1
	}
	if f.worktreeOn {
		return fitLine(dimHint(f.palette).Render(keepHead(placementInertHint, w)), w)
	}
	value := f.chips.Selected().Label
	return fitLine(lipgloss.NewStyle().Foreground(f.palette.Text).Render(keepHead(value, w)), w)
}

// Panel is the chip row plus, beneath it, the one-line explanation of
// whichever chip is selected (v2 spec §6). While inert the chip row
// renders its own placeholder and there is no choice to explain, so the
// second line stays blank rather than repeating the sentence already on
// the row.
func (f *PlacementField) Panel(w, h int) string {
	// A LIVE chip row pays for one of the gutter's two cells itself
	// (panelChipRow); the INERT placeholder does not, so it is composed
	// like any other panel text.
	chipLine := func() string {
		if f.worktreeOn {
			return panelMarked(f.chips.MarkedView(panelInner(w), "chip:"+f.ID()+":"), false, f.palette)
		}
		v := f.chips.MarkedView(panelChipWidth(w), "chip:"+f.ID()+":")
		// MarkedView appends the selected chip's FocusHint as a second
		// line of its own, WITHOUT the panel's gutter. Taking only the
		// first line here and re-composing the hint through panelText
		// below is what keeps it aligned with every other panel line.
		if idx := strings.IndexByte(v, '\n'); idx >= 0 {
			v = v[:idx]
		}
		return panelChipRow(v)
	}

	hint := ""
	if !f.worktreeOn {
		hint = f.chips.Selected().FocusHint
	}
	lines := []string{
		chipLine(),
		panelText(dimHint(f.palette).Render(hint), w),
	}
	// Last, so a region too short for it drops the provenance rather than
	// the chooser: panelBlock truncates from the bottom, and a note about
	// where a value came from is worth less than the control that changes
	// it.
	if f.provenance != "" {
		lines = append(lines, provenanceLine(f.provenance, w, f.palette))
	}
	return panelBlock(w, h, lines...)
}

// PanelRows is two -- the chips and their explanation -- plus the
// provenance line when a config file chose the selection.
func (f *PlacementField) PanelRows() int { return 2 + provenanceRows(f.provenance) }

// FooterRungs implements form.go's footerHinter for the one state
// footer.go's per-ZONE table cannot see: with the worktree on, this
// field's chips are inert (SetWorktreeOn -> widgets.ChipRow.SetInert,
// whose Next/Prev are then no-ops), so the table's "←→ choose" promises
// a key that does nothing -- and this is the DEFAULT configuration's
// resting state, not an edge case. Same judgement, and the same
// sentence, as field_worktree.go's non-git rung.
func (f *PlacementField) FooterRungs() []string {
	if f.worktreeOn {
		return []string{"nothing to set here"}
	}
	return nil
}
