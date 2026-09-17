package form

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/ZviBaratz/herdr-draft/internal/plan"
	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// TestPlacementField_SetSpaceOffersTabIn pins #128's fourth chip: once an
// open space is named, the row can read `tab in <label>` and Value()
// reports PlacementTabIn; with no space named there are still three chips
// and SetValue(TabIn) cannot land anywhere but "new space".
func TestPlacementField_SetSpaceOffersTabIn(t *testing.T) {
	f := NewPlacementField(theme.Default())

	f.SetValue(plan.PlacementTabIn)
	if got := f.Value(); got != plan.PlacementNewSpace {
		t.Fatalf("Value() after SetValue(TabIn) with no space = %v, want new space (no chip to land on)", got)
	}

	f.SetSpace("herdr-draft")
	f.SetValue(plan.PlacementTabIn)
	if got := f.Value(); got != plan.PlacementTabIn {
		t.Fatalf("Value() after SetSpace+SetValue(TabIn) = %v, want PlacementTabIn", got)
	}
	if got := rowText(f.Row(60)); got != "tab in herdr-draft" {
		t.Errorf("Row(60) = %q, want %q", got, "tab in herdr-draft")
	}
	if hint := panelLineAt(f.Panel(80, f.PanelRows()), 1); !strings.Contains(hint, "already holding") {
		t.Errorf("panel hint = %q, want it to explain the space already holds the repository", hint)
	}

	// The fourth chip is reachable by arrow, after "split here".
	g := NewPlacementField(theme.Default())
	g.SetSpace("toysim")
	for i := 0; i < 3; i++ {
		g.Update(key(tea.KeyRight, 0))
	}
	if got := g.Value(); got != plan.PlacementTabIn {
		t.Errorf("Value() after three Rights with a space = %v, want PlacementTabIn as the fourth chip", got)
	}
}

// TestPlacementField_SetSpaceWithdrawsAndKeepsOtherChoices: a project
// change to a repository with no open space removes the chip; a cursor on
// it falls back to new space, a cursor anywhere else stays put.
func TestPlacementField_SetSpaceWithdrawsAndKeepsOtherChoices(t *testing.T) {
	f := NewPlacementField(theme.Default())
	f.SetSpace("herdr-draft")
	f.SetValue(plan.PlacementTabIn)
	f.SetSpace("")
	if got := f.Value(); got != plan.PlacementNewSpace {
		t.Errorf("Value() after the space went away = %v, want new space", got)
	}
	if got := rowText(f.Row(60)); strings.Contains(got, "tab in") {
		t.Errorf("Row(60) = %q still names a space that is gone", got)
	}

	g := NewPlacementField(theme.Default())
	g.SetValue(plan.PlacementSplitHere)
	g.SetSpace("herdr-draft")
	if got := g.Value(); got != plan.PlacementSplitHere {
		t.Errorf("Value() after SetSpace = %v, want the user's split-here kept", got)
	}
	g.SetSpace("other")
	if got := g.Value(); got != plan.PlacementSplitHere {
		t.Errorf("Value() after a second SetSpace = %v, want split-here kept", got)
	}
}

// TestPlacementField_TabInIsKeptUnderAWorktreeButNotShown: a worktree
// session never takes a tab in the repository's space (placement spec
// §14), so the row stops naming it; the chosen chip survives underneath and
// comes back when the worktree is turned off.
func TestPlacementField_TabInIsKeptUnderAWorktreeButNotShown(t *testing.T) {
	f := NewPlacementField(theme.Default())
	f.SetSpace("herdr-draft")
	f.SetValue(plan.PlacementTabIn)
	f.SetWorktreeOn(true)
	if got := rowText(f.Row(60)); strings.Contains(got, "tab in") {
		t.Errorf("Row(60) under a worktree = %q, still names the repository's space", got)
	}
	if got := f.Value(); got != plan.PlacementTabIn {
		t.Errorf("Value() under a worktree = %v, want the chosen %v kept", got, plan.PlacementTabIn)
	}
	f.SetWorktreeOn(false)
	if got := rowText(f.Row(60)); got != "tab in herdr-draft" {
		t.Errorf("Row(60) with the worktree off again = %q, want %q", got, "tab in herdr-draft")
	}
}

// TestPlacementField_LongSpaceLabelIsTruncated: a space label is free
// text, and the chip row is one line.
func TestPlacementField_LongSpaceLabelIsTruncated(t *testing.T) {
	f := NewPlacementField(theme.Default())
	f.SetSpace("Toysim lane BITS - weight, the SKU gate and the watermark reason")
	f.SetValue(plan.PlacementTabIn)
	row := rowText(f.Row(80))
	if !strings.HasPrefix(row, "tab in Toysim lane BITS") {
		t.Errorf("Row = %q, want it to start with the label's head", row)
	}
	if len([]rune(row)) > len("tab in ")+tabInLabelWidth {
		t.Errorf("Row = %q (%d runes), want the label bounded at %d", row, len([]rune(row)), tabInLabelWidth)
	}
}
