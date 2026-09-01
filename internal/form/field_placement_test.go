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

	frame := ansi.Strip(f.View(60))
	if !strings.Contains(frame, "worktree opens as its own space") {
		t.Errorf("View(60) = %q, want it to contain the inert hint text", frame)
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

func TestPlacementField_HeightIsConstant(t *testing.T) {
	f := NewPlacementField(theme.Default())
	base := f.Height(24)

	f.Update(key(tea.KeyRight, 0))
	if got := f.Height(24); got != base {
		t.Errorf("Height(24) after moving selection = %d, want %d", got, base)
	}

	f.SetWorktreeOn(true)
	if got := f.Height(24); got != base {
		t.Errorf("Height(24) while inert = %d, want %d", got, base)
	}
	if got := strings.Count(f.View(60), "\n") + 1; got != base {
		t.Errorf("View(60) rendered %d physical lines, want Height()'s own %d", got, base)
	}
}

func TestPlacementField_NoPanicOnDegenerateWidth(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("PlacementField panicked: %v", r)
		}
	}()
	f := NewPlacementField(theme.Default())
	_ = f.View(0)
	_ = f.View(-3)
}
