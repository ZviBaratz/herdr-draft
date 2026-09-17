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

// TestPlacementField_InertUnderWorktree pins placement spec §14: with a
// worktree, the session runs in the worktree's own space, so the field is
// not a focus stop and ignores input -- but it keeps the chip that was
// chosen, so turning the worktree off shows that choice again rather than
// a reset.
func TestPlacementField_InertUnderWorktree(t *testing.T) {
	f := NewPlacementField(theme.Default())
	f.Update(key(tea.KeyRight, 0)) // select "tab here"
	if got := f.Value(); got != plan.PlacementTabHere {
		t.Fatalf("setup: Value() = %v, want %v", got, plan.PlacementTabHere)
	}

	f.SetWorktreeOn(true)
	if f.Enabled() {
		t.Error("Enabled() = true under a worktree, want false -- there is no placement to choose")
	}
	f.Update(key(tea.KeyRight, 0))
	if got := f.Value(); got != plan.PlacementTabHere {
		t.Errorf("Value() after a Right under a worktree = %v, want %v -- the arrows must do nothing", got, plan.PlacementTabHere)
	}

	f.SetWorktreeOn(false)
	if !f.Enabled() {
		t.Error("Enabled() = false with the worktree off again, want true")
	}
	if got := f.Value(); got != plan.PlacementTabHere {
		t.Errorf("Value() with the worktree off again = %v, want the kept %v", got, plan.PlacementTabHere)
	}
}

// TestPlacementField_SettersStillApplyUnderWorktree: the app applies a
// remembered or configured placement whether or not a worktree is on
// (applyProjectDefaults), so the field must take it while inert. The inert
// state therefore lives in this field and not in widgets.ChipRow, whose own
// inert mode makes SelectID and Next no-ops and would drop the value.
func TestPlacementField_SettersStillApplyUnderWorktree(t *testing.T) {
	f := NewPlacementField(theme.Default())
	f.SetWorktreeOn(true)

	f.SetValue(plan.PlacementSplitHere)
	if got := f.Value(); got != plan.PlacementSplitHere {
		t.Fatalf("Value() after SetValue(SplitHere) under a worktree = %v, want %v", got, plan.PlacementSplitHere)
	}
	f.SetSpace("herdr-draft")
	f.SetValue(plan.PlacementTabIn)
	if got := f.Value(); got != plan.PlacementTabIn {
		t.Fatalf("Value() after SetValue(TabIn) under a worktree = %v, want %v", got, plan.PlacementTabIn)
	}
	f.SetSpace("herdr-draft") // the app re-runs this on every project change
	if got := f.Value(); got != plan.PlacementTabIn {
		t.Errorf("Value() after SetSpace again under a worktree = %v, want %v kept", got, plan.PlacementTabIn)
	}
}

// TestPlacementField_RowUnderWorktreeStatesTheConsequence pins the row half
// of placement spec §14: with a worktree the row says where the session
// goes (v3 spec §3 rule 1), not which chip is parked underneath.
func TestPlacementField_RowUnderWorktreeStatesTheConsequence(t *testing.T) {
	f := NewPlacementField(theme.Default())
	f.Update(key(tea.KeyRight, 0)) // "tab here"
	f.SetWorktreeOn(true)

	got := ansi.Strip(f.Row(60))
	if !strings.Contains(got, placementInertHint) {
		t.Errorf("Row(60) = %q, want it to state %q", got, placementInertHint)
	}
	if strings.Contains(got, "tab here") {
		t.Errorf("Row(60) = %q, names a placement that does not apply", got)
	}
}

// TestPlacementField_PanelUnderWorktreeSaysHowToChoose: the panel of an
// inert row (reachable by a click, form.go's FocusByID) shows no chips to
// pick from, only what gives the reader the choice back.
func TestPlacementField_PanelUnderWorktreeSaysHowToChoose(t *testing.T) {
	f := NewPlacementField(theme.Default())
	f.SetWorktreeOn(true)

	panel := ansi.Strip(f.Panel(80, f.PanelRows()))
	if !strings.Contains(panel, placementInertPanelHint) {
		t.Errorf("Panel under a worktree = %q, want %q", panel, placementInertPanelHint)
	}
	for _, chip := range placementChips {
		if strings.Contains(panel, chip.Label) {
			t.Errorf("Panel under a worktree = %q, still offers the %q chip", panel, chip.Label)
		}
	}
}

// TestPlacementField_PanelRowsMatchesPanelAcrossCombinations crosses the
// three axes this field's rows could plausibly branch on -- worktree on and
// off, times all three chips, times provenance set and unset -- and asserts
// the row count each combination is worth: 2 base rows (the chips and
// their explanation, or under a worktree the inert line and its blank),
// +1 when a config file chose the value. The +1 for a worktree disclosure
// line that placement spec §6.1 added went with §14.
//
// field_rows_test.go's panelRowsCases carries this field's BRANCHES, one
// case per side of the conjunction. Three things are left for this test,
// and an earlier version of this comment named a fourth that was simply
// false -- that only this test moves the chip by real keypresses, when
// panelRowsCases' own chipsRight helper sends the identical
// key(tea.KeyRight, 0) through Update. Corrected here rather than
// quietly, because an unsupported rationale in a comment is the shape of
// defect that lets a future reader delete real coverage believing it is
// duplicated.
//
// What it actually adds: the full PRODUCT, where the table has one case
// per branch -- interactions like (worktree on, split here, provenance)
// that no single branch case reaches; the only exercise of the THIRD
// chip anywhere in either test's row arithmetic, since the table needs
// just one non-default chip and uses tab-here; and the
// placementChipID(f.Value()) setup assertion below, which pins that
// Value() agrees with the chip the keypresses actually landed on -- a
// fact the table takes on trust.
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
				// Chips first: an inert field ignores the arrows.
				for i := 0; i < chip.right; i++ {
					f.Update(key(tea.KeyRight, 0))
				}
				f.SetWorktreeOn(worktreeOn)
				f.SetProvenance(provenance)

				if got := f.Value(); placementChipID(got) != chip.id {
					t.Fatalf("setup: worktreeOn=%v chip=%q: Value() resolved to chip %q, want %q",
						worktreeOn, chip.id, placementChipID(got), chip.id)
				}

				// The direct arithmetic check, computed independently of
				// both PlacementField.PanelRows() and Panel(): 2 base rows,
				// +1 when a config file chose the value -- and only while
				// the row shows that value, so not under a worktree.
				wantRows := 2
				if provenance != "" && !worktreeOn {
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

// TestPlacementField_InertPanelAttributesNothing: provenance says which file
// chose the value a panel shows (v2 spec §11). The inert panel shows no
// value -- the plan runs in the worktree's own space whatever
// .herdr-draft.toml said -- so it must not claim the file chose anything;
// `create --json` attributes the same state to "worktree".
func TestPlacementField_InertPanelAttributesNothing(t *testing.T) {
	f := NewPlacementField(theme.Default())
	f.SetProvenance(".herdr-draft.toml")
	f.SetWorktreeOn(true)
	if panel := ansi.Strip(f.Panel(80, 3)); strings.Contains(panel, "from ") {
		t.Errorf("inert Panel = %q, still attributes a value it does not show", panel)
	}
	f.SetWorktreeOn(false)
	if panel := ansi.Strip(f.Panel(80, f.PanelRows())); !strings.Contains(panel, "from .herdr-draft.toml") {
		t.Errorf("live Panel = %q, want the provenance back", panel)
	}
}

// TestPlacementField_FooterUnderWorktreeHasNothingToSet: the zone table's
// "←→ choose" would promise a key that does nothing on an inert row, which
// is the worktree's resting state (config.Load defaults it on). Same
// sentence as field_worktree.go's non-git rung. Asserted through the
// footerHinter interface, since that structural check is how footer.go
// finds the override at all.
func TestPlacementField_FooterUnderWorktreeHasNothingToSet(t *testing.T) {
	f := NewPlacementField(theme.Default())
	h, ok := any(f).(footerHinter)
	if !ok {
		t.Fatal("PlacementField does not implement footerHinter, want an override for its inert state")
	}
	if got := h.FooterRungs(); got != nil {
		t.Errorf("FooterRungs() with the worktree off = %v, want nil (the zone table's own rung)", got)
	}
	f.SetWorktreeOn(true)
	if got := h.FooterRungs(); len(got) != 1 || got[0] != "nothing to set here" {
		t.Errorf("FooterRungs() under a worktree = %v, want [nothing to set here]", got)
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
