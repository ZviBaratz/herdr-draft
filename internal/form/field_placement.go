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
// FocusHint is each chip's one-line explanation. There is no worktree-on
// wording: under a worktree no chip applies (placement spec §14), and the
// row goes inert instead.
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
		Label:     "tab in " + keepHead(DisplayText(spaceLabel), tabInLabelWidth),
		FocusHint: "opens a tab in the space already holding this repository",
	}
}

// placementInertHint is the row under a worktree: where the session goes,
// which is the consequence the row states (v3 spec §3 rule 1). Placement
// spec §14 put it back after §6.1 had removed v2's version of it.
const placementInertHint = "the worktree's own space"

// placementInertPanelHint is the panel in the same state. It differs from
// the row on purpose: the row states the consequence, and the panel, being
// the chooser, says what gives the reader the choice back -- one sentence
// twice, three lines apart, would say neither (v2 spec §3 rule 5).
const placementInertPanelHint = "turn the worktree off to choose"

// placementRowLabel is v2's row label (v2 spec §6). It is also the widest
// label in the stack, and therefore what rowlayout.go's labelColWidth is
// sized against.
const placementRowLabel = "placement"

// PlacementField is the form's Placement Section (spec §6 field 5): a
// one-line row naming where a creation without a worktree will attach
// relative to the invoking pane (internal/plan.Placement), over a panel
// holding the chips and the selected chip's own explanation. With a
// worktree it is inert (placement spec §14).
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

// SetPalette implements paletteSetter: repaint after the host terminal
// answered (host-colours spec §7.3, #347).
func (f *PlacementField) SetPalette(p theme.Palette) {
	f.palette = p
	f.chips.SetPalette(p)
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

// Enabled reports whether Placement takes a focus stop: not while a
// worktree is on, because a worktree session runs in the worktree's own
// space whatever is chosen here (placement spec §14,
// plan.EffectivePlacement). The row stays drawn, present-but-inert, and
// says so.
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
// handles a task 21 left-button click on one of this row's own
// "chip:placement:<chipID>" zones (SelectAt) -- and every other message
// is ignored: MapKey never forwards Tab/Enter/Esc/etc. here as
// ActionNone (ZonePlacement is a plain, non-picker, non-title,
// non-prompt zone), so nothing else is expected to reach this Update.
//
// Under a worktree it ignores everything. The row can still be focused by
// a click (form.go's FocusByID ignores Enabled), and a keypress there must
// not quietly move a choice that does not apply.
func (f *PlacementField) Update(msg tea.Msg) tea.Cmd {
	if f.worktreeOn {
		return nil
	}
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

// SetWorktreeOn records whether a worktree is on, which makes the field
// inert (placement spec §14) without touching the selection: the chip
// keeps what was chosen or remembered, and shows it again when the
// worktree goes off. plan.EffectivePlacement, not this field, is what
// turns it into new-space for the plan.
//
// Deliberately NOT widgets.ChipRow.SetInert, which the pre-§6.1 version of
// this method used. That mode makes SelectID and Next no-ops, and the app
// applies remembered placements (SetValue, SetSpace) whether or not a
// worktree is on; under it they would be silently dropped. The v2 version
// could live with that only because it also snapped the cursor to "new
// space", which is the reset this version exists not to do.
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

// Row names the selected placement in v2's own lowercase vocabulary, or --
// under a worktree, where no placement applies -- states where the session
// goes instead, dim (v2 spec §6's present-but-inert row; placement spec
// §14).
func (f *PlacementField) Row(w int) string {
	if w < 1 {
		w = 1
	}
	if f.worktreeOn {
		return fitLine(dimHint(f.palette).Render(keepHead(placementInertHint, w)), w)
	}
	return fitLine(lipgloss.NewStyle().Foreground(f.palette.Text).Render(keepHead(f.chips.Selected().Label, w)), w)
}

// Panel is the chip row plus, beneath it, the one-line explanation of
// whichever chip is selected -- or, under a worktree, the sentence that
// gives the reader the choice back and a blank line where the explanation
// would be, since there is no choice to explain (placement spec §14).
func (f *PlacementField) Panel(w, h int) string {
	var lines []string
	if f.worktreeOn {
		// Composed like any other panel text: only a LIVE chip row pays for
		// one of the gutter's two cells itself (panelChipRow).
		lines = []string{panelText(dimHint(f.palette).Render(placementInertPanelHint), w), ""}
	} else {
		v := f.chips.MarkedView(panelChipWidth(w), "chip:"+f.ID()+":")
		// MarkedView appends the selected chip's FocusHint as a second
		// line of its own, WITHOUT the panel's gutter. Taking only the
		// first line here and re-composing the hint through panelText
		// below is what keeps it aligned with every other panel line.
		if idx := strings.IndexByte(v, '\n'); idx >= 0 {
			v = v[:idx]
		}
		lines = []string{
			panelChipRow(v),
			panelText(dimHint(f.palette).Render(f.chips.Selected().FocusHint), w),
		}
	}
	// Last, so a region too short for it drops the provenance rather than
	// the chooser: panelBlock truncates from the bottom, and a note about
	// where a value came from is worth less than the control that changes
	// it.
	if f.showsProvenance() {
		lines = append(lines, provenanceLine(f.provenance, w, f.palette))
	}
	return panelBlock(w, h, lines...)
}

// showsProvenance reports whether the panel carries the provenance line:
// only while it shows the value a config file chose. The inert panel shows
// none -- a worktree session runs in its own space whatever the file said
// -- so attributing one there would claim a choice nothing is honouring,
// and `create --json` attributes the same state to "worktree".
func (f *PlacementField) showsProvenance() bool {
	return f.provenance != "" && !f.worktreeOn
}

// PanelRows is two -- the chips and their explanation, or under a worktree
// the inert line and its blank -- plus the provenance line when a config
// file chose the selection and the panel shows it (showsProvenance).
func (f *PlacementField) PanelRows() int {
	if f.showsProvenance() {
		return 2 + provenanceRows(f.provenance)
	}
	return 2
}

// FooterRungs implements form.go's footerHinter for the one state
// footer.go's per-ZONE table cannot see: under a worktree this row is
// inert and ignores its keys, so the table's "←→ choose" would promise a
// key that does nothing -- and a worktree is the default configuration's
// resting state, not an edge case. Same sentence as field_worktree.go's
// non-git rung.
func (f *PlacementField) FooterRungs() []string {
	if f.worktreeOn {
		return []string{"nothing to set here"}
	}
	return nil
}
