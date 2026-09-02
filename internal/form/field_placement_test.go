package form

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ZviBaratz/herdr-draft/internal/plan"
	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

func TestPlacementField_IDAndDefault(t *testing.T) {
	f := NewPlacementField(theme.Default())
	if f.ID() != "placement" {
		t.Errorf("ID() = %q, want %q", f.ID(), "placement")
	}
	if !f.Enabled() {
		t.Errorf("Enabled() = false, want true before SetWorktreeOn(true)")
	}
	if got := f.Value(); got != plan.PlacementNewSpace {
		t.Errorf("Value() on a fresh field = %v, want %v", got, plan.PlacementNewSpace)
	}
}

// TestPlacementField_SetValue pins the new config-default pre-selection
// setter (Task 20b, spec §12's `default_placement`): each of the three
// values moves the cursor to the matching chip.
func TestPlacementField_SetValue(t *testing.T) {
	f := NewPlacementField(theme.Default())

	f.SetValue(plan.PlacementSplitHere)
	if got := f.Value(); got != plan.PlacementSplitHere {
		t.Fatalf("Value() after SetValue(SplitHere) = %v, want %v", got, plan.PlacementSplitHere)
	}

	f.SetValue(plan.PlacementTabHere)
	if got := f.Value(); got != plan.PlacementTabHere {
		t.Fatalf("Value() after SetValue(TabHere) = %v, want %v", got, plan.PlacementTabHere)
	}

	f.SetValue(plan.PlacementNewSpace)
	if got := f.Value(); got != plan.PlacementNewSpace {
		t.Fatalf("Value() after SetValue(NewSpace) = %v, want %v", got, plan.PlacementNewSpace)
	}
}

func TestPlacementField_ArrowsCycleChipsAndValue(t *testing.T) {
	f := NewPlacementField(theme.Default())

	f.Update(key(tea.KeyRight, 0))
	if got := f.Value(); got != plan.PlacementTabHere {
		t.Fatalf("Value() after one Right = %v, want %v", got, plan.PlacementTabHere)
	}

	f.Update(key(tea.KeyRight, 0))
	if got := f.Value(); got != plan.PlacementSplitHere {
		t.Fatalf("Value() after two Rights = %v, want %v", got, plan.PlacementSplitHere)
	}

	f.Update(key(tea.KeyLeft, 0))
	if got := f.Value(); got != plan.PlacementTabHere {
		t.Fatalf("Value() after Right,Right,Left = %v, want %v", got, plan.PlacementTabHere)
	}
}

// TestPlacementField_WorktreeOnGoesInert pins the brief's own literal
// requirement: SetWorktreeOn(true) makes the field present-but-inert (per
// form.go's Section doc comment, Enabled() == false) with the exact
// explanatory hint text, and forces Value() back to PlacementNewSpace
// (plan/build.go: "worktree creation always opens a new workspace
// regardless of Placement") even if the user had previously chosen a
// different chip.
func TestPlacementField_WorktreeOnGoesInert(t *testing.T) {
	f := NewPlacementField(theme.Default())
	f.Update(key(tea.KeyRight, 0)) // select "Tab here"
	if got := f.Value(); got != plan.PlacementTabHere {
		t.Fatalf("setup: Value() = %v, want %v", got, plan.PlacementTabHere)
	}

	f.SetWorktreeOn(true)

	if f.Enabled() {
		t.Errorf("Enabled() = true after SetWorktreeOn(true), want false (present-but-inert)")
	}
	if got := f.Value(); got != plan.PlacementNewSpace {
		t.Errorf("Value() after SetWorktreeOn(true) = %v, want %v (forced back to new space)", got, plan.PlacementNewSpace)
	}

	// The ROW states the consequence and the PANEL says how to get the
	// choice back -- two different sentences on purpose (see
	// placementInertPanelHint).
	if got := ansi.Strip(f.Row(60)); !strings.Contains(got, placementInertHint) {
		t.Errorf("Row(60) = %q, want it to contain %q", got, placementInertHint)
	}
	panel := ansi.Strip(f.Panel(60, f.PanelRows()))
	if !strings.Contains(panel, placementInertPanelHint) {
		t.Errorf("Panel(60) = %q, want it to contain %q", panel, placementInertPanelHint)
	}
}

func TestPlacementField_WorktreeOffReenables(t *testing.T) {
	f := NewPlacementField(theme.Default())
	f.SetWorktreeOn(true)
	f.SetWorktreeOn(false)

	if !f.Enabled() {
		t.Errorf("Enabled() = false after SetWorktreeOn(false), want true")
	}
	// Chips must be navigable again (inert no longer refuses Next/Prev).
	f.Update(key(tea.KeyRight, 0))
	if got := f.Value(); got != plan.PlacementTabHere {
		t.Errorf("Value() after re-enabling and one Right = %v, want %v", got, plan.PlacementTabHere)
	}
}

// TestPlacementField_ProvenanceIsAPanelLineTheRowNeverShows is v2 spec
// §11's placement half: a value a config file chose says so in the PANEL,
// and the row is not touched at all (v2 spec §3 rule 1 -- rows stay quiet,
// and a row that grew a suffix when a repo config appeared would move text
// under the cursor).
func TestPlacementField_ProvenanceIsAPanelLineTheRowNeverShows(t *testing.T) {
	f := NewPlacementField(theme.Default())
	rowBefore := f.Row(60)
	if rows := f.PanelRows(); rows != 2 {
		t.Fatalf("PanelRows() with no provenance = %d, want 2", rows)
	}
	if panel := ansi.Strip(f.Panel(60, f.PanelRows())); strings.Contains(panel, "from ") {
		t.Errorf("Panel() with no provenance = %q, want no provenance line", panel)
	}

	f.SetProvenance(".herdr-draft.toml")

	if rows := f.PanelRows(); rows != 3 {
		t.Errorf("PanelRows() with provenance = %d, want 3 (the line must be booked, or the form blank-fills over it)", rows)
	}
	if got, want := f.Row(60), rowBefore; got != want {
		t.Errorf("Row(60) changed when provenance was set:\n before: %q\n  after: %q", rowText(want), rowText(got))
	}
	if got := panelLineAt(f.Panel(60, f.PanelRows()), 2); got != "from .herdr-draft.toml" {
		t.Errorf("last panel line = %q, want %q", got, "from .herdr-draft.toml")
	}

	f.SetProvenance("")
	if rows := f.PanelRows(); rows != 2 {
		t.Errorf("PanelRows() after clearing provenance = %d, want 2 back", rows)
	}
}

func TestPlacementField_NoPanicOnDegenerateWidth(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("PlacementField panicked: %v", r)
		}
	}()
	f := NewPlacementField(theme.Default())
	_ = f.Row(0)
	_ = f.Panel(0, f.PanelRows())
	_ = f.Row(-3)
	_ = f.Panel(-3, f.PanelRows())
}
