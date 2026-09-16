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

// placementChips are spec §6 field 5's three options, in order; "new"
// (index 0, plan.PlacementNewSpace, the zero value) is the field's
// initial selection. Chip IDs match config.toml's own `default_placement`
// vocabulary ("tab-here"/"split-here", see placementFromConfigValue in
// internal/app/app.go) rather than a shorter internal-only spelling --
// unified in task 21 so this field's own task-21 mouse zone IDs
// ("chip:placement:tab-here") read the same vocabulary a config author
// already uses.
//
// FocusHint is the chip's WORKTREE-OFF explanation (placement spec §6.1's
// two-column table); placementWorktreeHint below picks the worktree-on
// wording for the same chip, since under a worktree each choice means
// something different -- "tab here" opens a tab on the WORKTREE's
// checkout, not on the plain project directory.
var placementChips = []widgets.Chip{
	{ID: "new", Label: "new space", FocusHint: "opens a new workspace of its own"},
	{ID: "tab-here", Label: "tab here", FocusHint: "opens a tab beside this pane's tab"},
	{ID: "split-here", Label: "split here", FocusHint: "splits this pane in two"},
}

// tabInChipID is the fourth chip's ID (plan.PlacementTabIn, config
// vocabulary "tab-in"). The chip itself is built per project by
// tabInChip, because its label names the space -- "tab in herdr-draft" --
// and it is only offered while such a space is open (SetSpace).
const tabInChipID = "tab-in"

// tabInLabelWidth bounds how much of a space's label the chip carries.
// Space labels are free text (a Linear title, say) and the chip row is
// one line; the row shows the same label, so a long one is truncated the
// same way in both places rather than wrapping the row.
const tabInLabelWidth = 28

func tabInChip(spaceLabel string) widgets.Chip {
	return widgets.Chip{
		ID:        tabInChipID,
		Label:     "tab in " + keepHead(spaceLabel, tabInLabelWidth),
		FocusHint: "opens a tab in the space already holding this repository",
	}
}

// placementWorktreeHint and placementWorktreeDisclosure are placement
// spec §6.1's worktree-on half of the two-column hint table.
// placementWorktreeHint replaces FocusHint's own wording per chip (the
// worktree's checkout, not the plain project directory, is what gets
// attached); placementWorktreeDisclosure is a SEPARATE second line, shown
// only for the two non-default chips, naming the fact the row does not
// restate (v2 spec §3 rule 1: the WORKTREE row, one line up, already
// says a worktree exists -- repeating that here would say the same thing
// twice, three lines apart, which v2 spec §3 rule 5's copy pass exists to
// delete).
func placementWorktreeHint(chipID string) string {
	switch chipID {
	case "tab-here":
		return "the agent opens a tab here, on the worktree's checkout"
	case tabInChipID:
		return "the agent opens a tab in that space, on the worktree's checkout"
	case "split-here":
		return "the agent splits this pane, onto the worktree's checkout"
	default: // "new"
		return "the worktree opens as its own space"
	}
}

const placementWorktreeDisclosure = "the worktree also keeps a space of its own"

// placementRowLabel is v2's row label (v2 spec §6). It is also the widest
// label in the stack, and therefore what rowlayout.go's labelColWidth is
// sized against.
const placementRowLabel = "placement"

// PlacementField is the form's Placement Section (spec §6 field 5): a
// one-line row naming where a creation will attach relative to the
// invoking pane (internal/plan.Placement) -- worktree on or off alike,
// placement spec §6.1 -- over a panel holding the three chips and the
// selected chip's own explanation.
type PlacementField struct {
	chips      *widgets.ChipRow
	focused    bool
	palette    theme.Palette
	worktreeOn bool

	// offered is the chip list currently loaded into chips: placementChips
	// alone, or placementChips plus tabInChip while SetSpace has named an
	// open space. Kept so SetValue knows how far Next() has to walk.
	offered []widgets.Chip

	// provenance is SetProvenance's config-file name, "" for a selection no
	// config file chose. See SetProvenance.
	provenance string
}

// NewPlacementField returns a PlacementField with "New space" selected,
// styled from palette.
func NewPlacementField(palette theme.Palette) *PlacementField {
	f := &PlacementField{chips: widgets.NewChipRow(palette), palette: palette}
	f.offered = placementChips
	f.chips.SetChips(f.offered)
	return f
}

// SetSpace offers -- or withdraws -- the fourth chip, `tab in <label>`,
// for the open workspace already holding the selected project (#128;
// defaults.Resolved.Space). "" means there is none: the chip goes, and a
// cursor that sat on it lands back on "new space", which is what
// Value() reads for a chip that is not there. The selection otherwise
// survives, by ID, so re-running this on every project change (the
// app's applyProjectDefaults) never moves a chip the user chose.
func (f *PlacementField) SetSpace(label string) {
	selected := f.chips.Selected().ID
	if strings.TrimSpace(label) == "" {
		f.offered = placementChips
	} else {
		f.offered = append(append([]widgets.Chip{}, placementChips...), tabInChip(label))
	}
	f.chips.SetChips(f.offered)
	f.chips.SelectID(selected)
}

// ID identifies this Section for form.go's zoneFor.
func (f *PlacementField) ID() string { return "placement" }

// Enabled reports that Placement always takes a real focus stop (placement
// spec §6.1): it used to go present-but-inert whenever the worktree toggle
// was on, because worktree creation ignored it outright. It no longer
// does -- plan.Build now honors Placement under a worktree too, routing
// the AGENT'S pane through it while the worktree still gets its own
// space regardless (placement spec §5.3) -- so there is no state left in
// which choosing a placement is meaningless.
func (f *PlacementField) Enabled() bool { return true }

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
// handles a task 21 left-button click on one of this row's own
// "chip:placement:<chipID>" zones (SelectAt) -- and every other message
// is ignored: MapKey never forwards Tab/Enter/Esc/etc. here as
// ActionNone (ZonePlacement is a plain, non-picker, non-title,
// non-prompt zone), so nothing else is expected to reach this Update.
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

// SetWorktreeOn records whether a worktree is on, which changes what the
// SAME three chips mean (placement spec §6.1) but no longer disables them
// or resets the selection: before placement spec, a worktree ignored
// Placement outright, so SetWorktreeOn(true) snapped the cursor back to
// "New space" and refused all further input (widgets.ChipRow.SetInert).
// plan.Build now honors whatever the user picked either way, so the
// selection must survive the toggle exactly like every other field's
// state does.
func (f *PlacementField) SetWorktreeOn(on bool) {
	f.worktreeOn = on
}

// SetValue moves the chip cursor directly to the chip matching v -- e.g.
// to apply spec §12's config.toml `default_placement` value at form
// construction. SetChips (NewPlacementField) always starts the cursor at
// index 0 ("New space", plan.PlacementNewSpace's own zero value), so this
// only needs to move it for a configured value OTHER than the default;
// walking forward with ChipRow's own wrapping Next() is safe here (unlike
// a non-wrapping Picker) because placementChips is a small, fixed,
// three-entry list -- at most two Next() calls always reaches any of the
// three.
//
// Added in Task 20b (the app layer) alongside AccountField.SetPin -- see
// field_title.go's SetTitle doc comment for the fuller writeup of this
// class of gap: a config-derived default value with no way to
// pre-select the field it configures.
func (f *PlacementField) SetValue(v plan.Placement) {
	id := placementChipID(v)
	for range f.offered {
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
	case plan.PlacementTabIn:
		return tabInChipID
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
	case tabInChipID:
		return plan.PlacementTabIn
	default:
		return plan.PlacementNewSpace
	}
}

// --- the row and its panel ------------------------------------------------

// Label is v2's row label (v2 spec §6's field table).
func (f *PlacementField) Label() string { return placementRowLabel }

// Row names the selected placement (placement spec §6.1): worktree off,
// v2's own lowercase vocabulary unchanged; worktree on, the SAME chip
// label, since the row states its own consequence and "a worktree
// exists" is already stated one row up by the worktree row itself.
func (f *PlacementField) Row(w int) string {
	if w < 1 {
		w = 1
	}
	return fitLine(lipgloss.NewStyle().Foreground(f.palette.Text).Render(keepHead(f.chips.Selected().Label, w)), w)
}

// Panel is the chip row plus, beneath it, the one-line explanation of
// whichever chip is selected -- worktree off, the chip's own FocusHint
// unchanged; worktree on, placementWorktreeHint's own wording for the
// SAME chip, plus a second disclosure line for the two non-default chips
// (placement spec §6.1).
func (f *PlacementField) Panel(w, h int) string {
	v := f.chips.MarkedView(panelChipWidth(w), "chip:"+f.ID()+":")
	// MarkedView appends the selected chip's FocusHint as a second line
	// of its own, WITHOUT the panel's gutter. Taking only the first line
	// here and re-composing the hint through panelText below is what
	// keeps it aligned with every other panel line.
	if idx := strings.IndexByte(v, '\n'); idx >= 0 {
		v = v[:idx]
	}

	selected := f.chips.Selected().ID
	hint := f.chips.Selected().FocusHint
	if f.worktreeOn {
		hint = placementWorktreeHint(selected)
	}

	lines := []string{
		panelChipRow(v),
		panelText(dimHint(f.palette).Render(hint), w),
	}
	if f.worktreeOn && selected != "new" {
		lines = append(lines, panelText(dimHint(f.palette).Render(placementWorktreeDisclosure), w))
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

// PanelRows is two -- the chips and their explanation -- plus one more
// under a worktree for the two non-default chips' disclosure line
// (placement spec §6.1), plus the provenance line when a config file
// chose the selection.
func (f *PlacementField) PanelRows() int {
	rows := 2
	if f.worktreeOn && f.chips.Selected().ID != "new" {
		rows++
	}
	return rows + provenanceRows(f.provenance)
}
