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

// TestPlacementField_LiveUnderWorktree pins placement spec §6.1: the
// field stays a real focus stop and keeps whatever the user picked when
// the worktree toggle turns on -- the opposite of the field's own
// pre-placement-spec behavior, which snapped the selection back to New
// space and refused all input.
func TestPlacementField_LiveUnderWorktree(t *testing.T) {
	f := NewPlacementField(theme.Default())
	f.Update(key(tea.KeyRight, 0)) // select "Tab here"
	if got := f.Value(); got != plan.PlacementTabHere {
		t.Fatalf("setup: Value() = %v, want %v", got, plan.PlacementTabHere)
	}

	f.SetWorktreeOn(true)

	if !f.Enabled() {
		t.Error("Enabled() = false after SetWorktreeOn(true), want true -- placement now applies under a worktree too")
	}
	if got := f.Value(); got != plan.PlacementTabHere {
		t.Errorf("Value() after SetWorktreeOn(true) = %v, want %v (the selection must survive)", got, plan.PlacementTabHere)
	}

	// The chips must still respond to input.
	f.Update(key(tea.KeyRight, 0))
	if got := f.Value(); got != plan.PlacementSplitHere {
		t.Errorf("Value() after one more Right = %v, want %v", got, plan.PlacementSplitHere)
	}
}

// TestPlacementField_RowUnderWorktreeNamesTheChipNotTheReason pins the
// row half of §6.1's hint table: with the worktree on, the row still
// shows the chip's OWN label (e.g. "tab here"), never the old inert
// sentence, because there is no inert state left to explain.
func TestPlacementField_RowUnderWorktreeNamesTheChipNotTheReason(t *testing.T) {
	f := NewPlacementField(theme.Default())
	f.SetWorktreeOn(true)
	f.Update(key(tea.KeyRight, 0)) // "tab here"

	if got := ansi.Strip(f.Row(60)); !strings.Contains(got, "tab here") {
		t.Errorf("Row(60) = %q, want it to contain %q", got, "tab here")
	}
	if got := ansi.Strip(f.Row(60)); strings.Contains(got, "worktree opens as its own space") {
		t.Errorf("Row(60) = %q, still shows the old inert sentence", got)
	}
}

// TestPlacementField_PanelDisclosesTheWorktreesOwnSpace pins §6.1's
// panel-only disclosure: for the two NON-default chips under a worktree,
// the panel adds "the worktree also keeps a space of its own" beneath the
// chip's ordinary hint. "new space" gets no disclosure -- there is
// nothing extra to disclose when the chip's own consequence already IS a
// new space.
func TestPlacementField_PanelDisclosesTheWorktreesOwnSpace(t *testing.T) {
	f := NewPlacementField(theme.Default())
	f.SetWorktreeOn(true)

	if strings.Contains(ansi.Strip(f.Panel(80, f.PanelRows())), placementWorktreeDisclosure) {
		t.Error("the default chip (new space) under worktree shows the disclosure, want none")
	}

	f.Update(key(tea.KeyRight, 0)) // tab here
	if !strings.Contains(ansi.Strip(f.Panel(80, f.PanelRows())), placementWorktreeDisclosure) {
		t.Error("tab here under worktree does not disclose the worktree's own space")
	}

	f.Update(key(tea.KeyRight, 0)) // split here
	if !strings.Contains(ansi.Strip(f.Panel(80, f.PanelRows())), placementWorktreeDisclosure) {
		t.Error("split here under worktree does not disclose the worktree's own space")
	}

	f.SetWorktreeOn(false)
	if strings.Contains(ansi.Strip(f.Panel(80, f.PanelRows())), placementWorktreeDisclosure) {
		t.Error("worktree off still shows the disclosure")
	}
}

// TestPlacementField_PanelRowsMatchesPanelAcrossCombinations crosses all
// three axes this field's PanelRows() branches on -- worktree on and off,
// times all three chips, times provenance set and unset -- and asserts
// the row count each combination is worth under placement spec §6.1's own
// rule: 2 base rows, +1 under a worktree for a non-default chip, +1 when
// a config file chose the value.
//
// field_rows_test.go's panelRowsCases carries the same field's five
// BRANCHES, so what this adds is the product rather than a representative
// of each branch, and it adds one thing that table cannot: the chip is
// moved by real Right keypresses through Update, so a selection the
// key path resolves differently from the constructor is caught here.
//
// It was written when the shared invariant could not fail. That is fixed
// (#33): the shared test now compares PanelRows() against an expectation
// derived independently of Panel, over every field, and the shape check
// this test used to keep a copy of -- len(split(Panel(80, want))) == want
// -- is gone from both, because panelBlock pads and truncates to the
// height it is handed and the comparison therefore held whatever
// PanelRows() returned. Panel's line count is pinned properly by
// field_rows_test.go's TestFieldPanel_IsAlwaysExactlyHLines.
func TestPlacementField_PanelRowsMatchesPanelAcrossCombinations(t *testing.T) {
	chipMoves := []struct {
		id    string
		right int // number of Right presses from the fresh "new" selection
	}{
		{"new", 0},
		{"tab-here", 1},
		{"split-here", 2},
	}

	for _, worktreeOn := range []bool{false, true} {
		for _, chip := range chipMoves {
			for _, provenance := range []string{"", ".herdr-draft.toml"} {
				f := NewPlacementField(theme.Default())
				f.SetWorktreeOn(worktreeOn)
				for i := 0; i < chip.right; i++ {
					f.Update(key(tea.KeyRight, 0))
				}
				f.SetProvenance(provenance)

				if got := f.Value(); placementChipID(got) != chip.id {
					t.Fatalf("setup: worktreeOn=%v chip=%q: Value() resolved to chip %q, want %q",
						worktreeOn, chip.id, placementChipID(got), chip.id)
				}

				// The direct arithmetic check: computed independently of
				// both PlacementField.PanelRows() and Panel(), from the
				// same rule placement spec §6.1 states -- 2 base rows,
				// +1 under worktree for a non-"new" chip, +1 more when a
				// config file chose the value.
				wantRows := 2
				if worktreeOn && chip.id != "new" {
					wantRows++
				}
				if provenance != "" {
					wantRows++
				}
				if got := f.PanelRows(); got != wantRows {
					t.Errorf("worktreeOn=%v chip=%q provenance=%q: PanelRows() = %d, want %d",
						worktreeOn, chip.id, provenance, got, wantRows)
				}
			}
		}
	}
}

// TestPlacementField_FooterHasNoWorktreeOverride pins the removal of the
// "nothing to set here" rung -- there is no longer an inert state for it
// to describe.
//
// The brief's own literal version of this test called f.FooterRungs()
// and asserted it returned nil, which does not compile once the method
// is gone (dispatch's own flagged trap). The real behavior to pin is
// that PlacementField stops satisfying form.go's footerHinter interface
// at all, which is asserted structurally at footer.go's own call site
// (`s.(footerHinter)`, no separate registration step) -- so a section
// that never implements the method falls through to the zone table
// exactly the same way a section that lost the method does.
func TestPlacementField_FooterHasNoWorktreeOverride(t *testing.T) {
	f := NewPlacementField(theme.Default())
	f.SetWorktreeOn(true)
	if _, ok := any(f).(footerHinter); ok {
		t.Error("PlacementField still satisfies footerHinter, want it to fall through to footer.go's own per-zone table")
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
