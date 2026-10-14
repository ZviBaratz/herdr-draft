package form

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ZviBaratz/herdr-draft/internal/plan"
	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

func machineChoices() []MachineChoice {
	return []MachineChoice{{Selector: "local", Label: "local"}, {Selector: "dev", Label: "dev (EC2)"}}
}

// TestMachineField_OffersWhatItIsGiven: the row's chips are the offered
// set the app pushes in (machine spec §4.2), local first; the selection is
// a selector, and the row names it by its label.
func TestMachineField_OffersWhatItIsGiven(t *testing.T) {
	f := NewMachineField(theme.Default())
	f.SetChoices(machineChoices())
	if f.Value() != "local" {
		t.Errorf("Value() = %q before anything was set, want local", f.Value())
	}
	f.SetValue("dev")
	if f.Value() != "dev" {
		t.Errorf("Value() = %q after SetValue(dev)", f.Value())
	}
	if row := ansi.Strip(f.Row(40)); !strings.HasPrefix(row, "dev (EC2)") {
		t.Errorf("row = %q, want it to name the machine by its label", row)
	}
	// An unknown selector is not a chip: the selection stays put.
	f.SetValue("nanoclaw")
	if f.Value() != "dev" {
		t.Errorf("Value() = %q after SetValue(nanoclaw), want dev kept", f.Value())
	}
}

// TestMachineField_KeysCycleTheChoice: ←→ move it, as on every chip row,
// and local is one key away from the machine a rule picked.
func TestMachineField_KeysCycleTheChoice(t *testing.T) {
	f := NewMachineField(theme.Default())
	f.SetChoices(machineChoices())
	f.SetValue("dev")
	f.Update(key(tea.KeyLeft, 0))
	if f.Value() != "local" {
		t.Errorf("Value() = %q after ←, want local", f.Value())
	}
}

// TestMachineField_RowAndPanelSayWhatTheProbeFound: the row carries the
// probe's verdict in a word, the panel in its own words (machine spec
// §9.1) -- and a local choice has no probe to report.
func TestMachineField_RowAndPanelSayWhatTheProbeFound(t *testing.T) {
	f := NewMachineField(theme.Default())
	f.SetChoices(machineChoices())
	f.SetValue("dev")
	f.SetProvenance("config.toml: ~/quantivly → dev")

	f.SetStatus(MachineChecking, "checking…", "checking dev (EC2)…")
	if row := ansi.Strip(f.Row(40)); !strings.Contains(row, "dev (EC2) · checking…") {
		t.Errorf("checking row = %q", row)
	}
	panel := ansi.Strip(f.Panel(60, f.PanelRows()))
	for _, want := range []string{"local", "dev (EC2)", "checking dev (EC2)…", "from config.toml: ~/quantivly → dev"} {
		if !strings.Contains(panel, want) {
			t.Errorf("panel lacks %q:\n%s", want, panel)
		}
	}

	f.SetStatus(MachineRefused, "unreachable", "dev (EC2) is unreachable: Token for dev is expired")
	if row := ansi.Strip(f.Row(40)); !strings.Contains(row, "· unreachable") {
		t.Errorf("refused row = %q", row)
	}
	if panel := ansi.Strip(f.Panel(60, f.PanelRows())); !strings.Contains(panel, "Token for dev is expired") {
		t.Errorf("refused panel lacks the reason:\n%s", panel)
	}

	f.SetValue("local")
	if row := ansi.Strip(f.Row(40)); strings.TrimSpace(row) != "local" {
		t.Errorf("a local row reports a remote probe: %q", row)
	}
}

// TestPlacementField_InertOnAnotherMachine: a session on another machine
// opens in a new space there whatever is chosen (machine spec §6.2), so the
// row says where it goes and takes no keys -- with or without a worktree.
func TestPlacementField_InertOnAnotherMachine(t *testing.T) {
	f := NewPlacementField(theme.Default())
	f.SetValue(plan.PlacementTabHere)
	f.SetRemote("dev (EC2)")
	if f.Enabled() {
		t.Error("placement is choosable for a session on another machine")
	}
	if row := ansi.Strip(f.Row(40)); !strings.HasPrefix(row, "a new space on dev (EC2)") {
		t.Errorf("row = %q", row)
	}
	f.Update(key(tea.KeyRight, 0))
	f.SetRemote("")
	if got := placementChipID(f.Value()); got != "tab-here" {
		t.Errorf("going back to local lost the chosen placement: %q", got)
	}
	f.SetRemote("dev (EC2)")
	f.SetWorktreeOn(true)
	if row := ansi.Strip(f.Row(50)); !strings.HasPrefix(row, "the worktree's own space on dev (EC2)") {
		t.Errorf("row with a worktree = %q", row)
	}
}

// TestAccountField_InertOnAnotherMachine: another machine uses its own
// login (machine spec §6.3) -- the row says so, takes no stop, and asks
// for no retry even when clauth failed to answer.
func TestAccountField_InertOnAnotherMachine(t *testing.T) {
	f := NewAccountField(theme.Default())
	f.SetAgentIsClaude(true)
	f.SetProfiles(sampleStatus(), sampleNow())
	f.SetRemote("dev (EC2)")
	if f.Enabled() {
		t.Error("the account row is choosable for a session on another machine")
	}
	if row := ansi.Strip(f.Row(40)); !strings.HasPrefix(row, "dev (EC2)'s own login") {
		t.Errorf("row = %q", row)
	}
	if panel := ansi.Strip(f.Panel(60, f.PanelRows())); !strings.Contains(panel, "uses that machine's own login") {
		t.Errorf("panel = %q", panel)
	}
	f.SetUnavailable("clauth status --json failed")
	if f.RetryOnFocus() {
		t.Error("a remote account row asks for a clauth retry")
	}
}

// TestMachineField_ARefusalWrapsSoItsRemedyShows: herdr's refusal of a
// too-old server ends with what to do about it (machine spec §3.4), so the
// panel wraps it rather than cutting the remedy off.
func TestMachineField_ARefusalWrapsSoItsRemedyShows(t *testing.T) {
	f := NewMachineField(theme.Default())
	f.SetChoices(machineChoices())
	f.SetValue("dev")
	f.SetStatus(MachineRefused, "unreachable",
		"dev (EC2) is unreachable: machine 'dev (EC2)': remote Herdr does not support machine API forwarding; update Herdr on this machine")
	panel := ansi.Strip(f.Panel(101, f.PanelRows()))
	if !strings.Contains(panel, "update Herdr on this machine") {
		t.Errorf("the remedy was cut off:\n%s", panel)
	}
}

// TestMachineField_ShowsWhatConfigToml_GotWrong: a rule config.toml could
// not use is reported on this row's panel, the row it would have
// configured, as [agents.options]' refusals are on the options row.
func TestMachineField_ShowsWhatConfigTomlGotWrong(t *testing.T) {
	f := NewMachineField(theme.Default())
	f.SetNotes([]string{`ignoring [machine.paths] "quantivly": not an absolute path`})
	if got := f.PanelRows(); got != 3 {
		t.Errorf("PanelRows() = %d, want the chips, their explanation and the note", got)
	}
	if panel := ansi.Strip(f.Panel(100, f.PanelRows())); !strings.Contains(panel, `ignoring [machine.paths] "quantivly"`) {
		t.Errorf("panel lacks the note:\n%s", panel)
	}
}
