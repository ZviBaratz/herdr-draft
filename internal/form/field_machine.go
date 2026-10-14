// field_machine.go is the machine row (machine spec §9.1): which herdr
// machine the session runs on. Written fresh for that design, on
// widgets.ChipRow like field_placement.go.
package form

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/ZviBaratz/herdr-draft/internal/form/widgets"
	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// MachineChoice is one machine the row offers: the selector config.toml
// wrote (or "local"), and the label a person knows it by -- a saved
// profile's, once the app has read `herdr machine list`.
type MachineChoice struct {
	Selector string
	Label    string
}

// MachineState is what the app knows about the chosen machine (machine
// spec §7): nothing to know (this machine), its probe still out, ready, or
// refused -- unreachable, without the project, or not answering.
type MachineState int

const (
	MachineIdle MachineState = iota
	MachineChecking
	MachineReady
	MachineRefused
)

// machineRowLabel is the row's label. It fits rowlayout.go's
// labelColWidth, which "placement" sets.
const machineRowLabel = "machine"

// machineLocalSelector is config.LocalMachine's spelling, which this
// package cannot import: the chip the field starts on, and the one whose
// row never carries a probe's verdict.
const machineLocalSelector = "local"

// MachineField is the form's machine Section: a chip row of the machines
// config.toml offers (machine spec §4.2), and a panel saying where the
// choice came from and what its probe found. It performs no I/O; the
// choices, the provenance and the verdict are pushed in by internal/app,
// which is the only package that knows what a machine is.
type MachineField struct {
	chips   *widgets.ChipRow
	focused bool
	palette theme.Palette
	offered []widgets.Chip

	provenance string

	state            MachineState
	rowNote, panelNo string
}

// NewMachineField returns a MachineField offering only this machine, until
// SetChoices says otherwise.
func NewMachineField(palette theme.Palette) *MachineField {
	f := &MachineField{chips: widgets.NewChipRow(palette), palette: palette}
	f.SetChoices([]MachineChoice{{Selector: machineLocalSelector, Label: machineLocalSelector}})
	return f
}

// SetPalette implements paletteSetter (host-colours spec §7.3).
func (f *MachineField) SetPalette(p theme.Palette) {
	f.palette = p
	f.chips.SetPalette(p)
}

// SetChoices loads the offered machines, keeping the selection by selector
// -- the app re-loads them once `herdr machine list` has supplied the
// labels, and that must not move a chip anyone chose.
func (f *MachineField) SetChoices(choices []MachineChoice) {
	selected := f.chips.Selected().ID
	f.offered = make([]widgets.Chip, 0, len(choices))
	for _, c := range choices {
		hint := "runs on " + c.Label + ", in a new space there"
		if c.Selector == machineLocalSelector {
			hint = "runs here, as every session did before machines"
		}
		f.offered = append(f.offered, widgets.Chip{ID: c.Selector, Label: c.Label, FocusHint: hint})
	}
	f.chips.SetChips(f.offered)
	if selected != "" {
		f.chips.SelectID(selected)
	}
}

// SetValue moves the selection to selector, as the app does with what the
// rules resolved. A selector that is not offered leaves it where it is.
func (f *MachineField) SetValue(selector string) { f.chips.SelectID(selector) }

// Value is the selected selector.
func (f *MachineField) Value() string {
	if id := f.chips.Selected().ID; id != "" {
		return id
	}
	return machineLocalSelector
}

// SetProvenance is where the selection came from, e.g. "config.toml:
// ~/quantivly → dev"; "" for none.
func (f *MachineField) SetProvenance(source string) { f.provenance = source }

// SetStatus is the probe's verdict: rowNote is the word the row carries
// after the label, panelNote the sentence the panel gives it.
func (f *MachineField) SetStatus(state MachineState, rowNote, panelNote string) {
	f.state, f.rowNote, f.panelNo = state, rowNote, panelNote
}

// ID identifies this Section for form.go's zoneFor.
func (f *MachineField) ID() string { return "machine" }

// Enabled: the row is always a choice.
func (f *MachineField) Enabled() bool { return true }

// Focus gives the field input focus.
func (f *MachineField) Focus() tea.Cmd {
	f.focused = true
	return nil
}

// Blur removes input focus.
func (f *MachineField) Blur() { f.focused = false }

// Update moves the selection on ←→ (and ↑↓, as placement's row does), or
// to a clicked chip.
func (f *MachineField) Update(msg tea.Msg) tea.Cmd {
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

// Label is the row label.
func (f *MachineField) Label() string { return machineRowLabel }

// remoteVerdict reports whether the row and panel carry the probe's
// verdict: only for another machine, which is the only kind that has one.
func (f *MachineField) remoteVerdict() bool {
	return f.Value() != machineLocalSelector && f.state != MachineIdle
}

// verdictStyle colours the verdict: a refusal in Danger, the rest dim.
func (f *MachineField) verdictStyle() lipgloss.Style {
	if f.state == MachineRefused {
		return lipgloss.NewStyle().Foreground(f.palette.Danger)
	}
	return dimText(f.palette)
}

// Row names the chosen machine and, for another machine, the probe's word
// on it: `dev (EC2) · checking…`. It reads nothing but the field's own
// state, never the window height (field_rows_test.go).
func (f *MachineField) Row(w int) string {
	if w < 1 {
		w = 1
	}
	label := f.chips.Selected().Label
	text := lipgloss.NewStyle().Foreground(f.palette.Text)
	// Each part is cut as plain text before it is styled, so a cut never
	// lands inside an escape sequence; the label keeps its room first.
	const sep = " · "
	room := w - lipgloss.Width(label) - lipgloss.Width(sep)
	if !f.remoteVerdict() || f.rowNote == "" || room < 1 {
		return fitLine(text.Render(keepHead(label, w)), w)
	}
	return fitLine(text.Render(label)+dimText(f.palette).Render(sep)+f.verdictStyle().Render(keepHead(f.rowNote, room)), w)
}

// Panel is the chips and the chosen one's explanation, then the probe's
// sentence for another machine, then where the choice came from.
func (f *MachineField) Panel(w, h int) string {
	v := f.chips.MarkedView(panelChipWidth(w), "chip:"+f.ID()+":")
	if idx := strings.IndexByte(v, '\n'); idx >= 0 {
		v = v[:idx]
	}
	lines := []string{
		panelChipRow(v),
		panelText(dimHint(f.palette).Render(f.chips.Selected().FocusHint), w),
	}
	if f.remoteVerdict() && f.panelNo != "" {
		lines = append(lines, panelText(f.verdictStyle().Render(keepHead(DisplayText(f.panelNo), panelInner(w))), w))
	}
	if f.provenance != "" {
		lines = append(lines, provenanceLine(f.provenance, w, f.palette))
	}
	return panelBlock(w, h, lines...)
}

// PanelRows is the chips and their explanation, the verdict line when one
// is shown, and the provenance line when there is one.
func (f *MachineField) PanelRows() int {
	rows := 2 + provenanceRows(f.provenance)
	if f.remoteVerdict() && f.panelNo != "" {
		rows++
	}
	return rows
}
