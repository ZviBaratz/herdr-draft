package form

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

func manyKinds() []string {
	// More than agentFavoriteChips, so the chip row and the full kind list
	// genuinely differ -- exercises the file doc's own design note.
	return []string{"claude", "codex", "aider", "cursor-agent", "gemini"}
}

func TestAgentField_IDAndEnabled(t *testing.T) {
	f := NewAgentField(theme.Default())
	if f.ID() != "agent" {
		t.Errorf("ID() = %q, want %q", f.ID(), "agent")
	}
	if !f.Enabled() {
		t.Errorf("Enabled() = false, want true (Agent is always present)")
	}
}

func TestAgentField_SetKindsDefaultsToFirst(t *testing.T) {
	f := NewAgentField(theme.Default())
	f.SetKinds(manyKinds())
	if got := f.Value(); got != "claude" {
		t.Errorf("Value() on a fresh SetKinds = %q, want %q (index 0, spec's own configured default)", got, "claude")
	}
}

func TestAgentField_ArrowsCycleFavoriteChips(t *testing.T) {
	f := NewAgentField(theme.Default())
	f.SetKinds(manyKinds())

	f.Update(key(tea.KeyRight, 0))
	if got := f.Value(); got != "codex" {
		t.Fatalf("Value() after one Right = %q, want %q", got, "codex")
	}
	f.Update(key(tea.KeyLeft, 0))
	if got := f.Value(); got != "claude" {
		t.Fatalf("Value() after Right,Left = %q, want %q", got, "claude")
	}
}

// TestAgentField_ChipRowHoldsOnlyFavorites pins the v2 chip row: the
// leading favorites and nothing else. v1 appended a synthetic "more…"
// chip whose job was to reveal the full kind list; v2's panel shows that
// list permanently, so the chip would be a control with no effect.
func TestAgentField_ChipRowHoldsOnlyFavorites(t *testing.T) {
	f := NewAgentField(theme.Default())
	f.SetKinds(manyKinds())

	panel := ansi.Strip(f.Panel(60, f.PanelRows()))
	if strings.Contains(panel, "more") {
		t.Errorf("Panel = %q, want no more… chip: the list it opened is already on screen", panel)
	}
	// Every kind is in the panel, favorites and non-favorites alike.
	for _, kind := range manyKinds() {
		if !strings.Contains(panel, kind) {
			t.Errorf("Panel does not list %q:\n%s", kind, panel)
		}
	}
}

// TestAgentField_ArrowsSplitTheChipsAndTheList pins v2's grammar, which
// needs no modes because both halves of the panel are always visible:
// ←→ move the favorite chips, ↑↓ move the full kind list, and each
// confirms what it lands on.
func TestAgentField_ArrowsSplitTheChipsAndTheList(t *testing.T) {
	f := NewAgentField(theme.Default())
	f.SetKinds(manyKinds())

	f.Update(key(tea.KeyDown, 0))
	if got := f.Value(); got != "codex" {
		t.Fatalf("Value() after one Down = %q, want the second kind %q", got, "codex")
	}
	f.Update(key(tea.KeyUp, 0))
	if got := f.Value(); got != "claude" {
		t.Fatalf("Value() after Down,Up = %q, want %q", got, "claude")
	}
	// Up at the top clamps rather than wrapping or handing focus anywhere:
	// the chips are a sibling control, not an outer cursor.
	f.Update(key(tea.KeyUp, 0))
	if got := f.Value(); got != "claude" {
		t.Fatalf("Value() after a third Up = %q, want it clamped at %q", got, "claude")
	}

	f.Update(key(tea.KeyRight, 0))
	if got := f.Value(); got != "codex" {
		t.Fatalf("Value() after one Right = %q, want %q", got, "codex")
	}
}

// TestAgentField_ListReachesAKindNotOnTheChipRow exercises the design
// note's core claim: a kind past agentFavoriteChips is reachable (and
// selectable) through the full list, with no gesture in between.
func TestAgentField_ListReachesAKindNotOnTheChipRow(t *testing.T) {
	f := NewAgentField(theme.Default())
	kinds := manyKinds() // gemini is kinds[4], past agentFavoriteChips (3)
	f.SetKinds(kinds)

	for range len(kinds) - 1 {
		f.Update(key(tea.KeyDown, 0))
	}

	if got := f.Value(); got != "gemini" {
		t.Fatalf("Value() after walking to the last row = %q, want %q", got, "gemini")
	}
}

func TestAgentField_HeightIsConstant(t *testing.T) {
	f := NewAgentField(theme.Default())
	base := f.Height(24)

	f.SetKinds(manyKinds())
	if got := f.Height(24); got != base {
		t.Errorf("Height(24) after SetKinds = %d, want %d", got, base)
	}
	for range agentFavoriteChips {
		f.Update(key(tea.KeyRight, 0))
	}
	f.Update(key(tea.KeyDown, 0)) // expand
	if got := f.Height(24); got != base {
		t.Errorf("Height(24) while expanded = %d, want %d", got, base)
	}
	if got := strings.Count(f.View(60, f.Height(24)), "\n") + 1; got != base {
		t.Errorf("View(60) rendered %d physical lines, want Height()'s own %d", got, base)
	}
}

func TestAgentField_NoPanicOnDegenerateWidth(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("AgentField panicked: %v", r)
		}
	}()
	f := NewAgentField(theme.Default())
	_ = f.View(0, f.Height(24))
	_ = f.View(-3, f.Height(24))
}

func TestAgentField_NoPanicBeforeSetKinds(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("AgentField panicked before SetKinds: %v", r)
		}
	}()
	f := NewAgentField(theme.Default())
	_ = f.View(60, f.Height(24))
	f.Update(key(tea.KeyRight, 0))
	f.Update(key(tea.KeyDown, 0))
	if got := f.Value(); got != "" {
		t.Errorf("Value() before SetKinds = %q, want \"\"", got)
	}
}

// TestAgentField_SetKind pins the setter the state layer needed (finding
// I2): SetKinds' "index 0 is the default" contract could express only the
// CONFIGURED default, so config.State.LastKind had nowhere to be applied.
func TestAgentField_SetKind(t *testing.T) {
	f := NewAgentField(theme.Default())
	f.SetKinds([]string{"claude", "codex", "pi", "gemini", "cursor"})

	if got := f.Value(); got != "claude" {
		t.Fatalf("Value() = %q, want the configured default %q", got, "claude")
	}

	// A kind with its own chip.
	f.SetKind("codex")
	if got := f.Value(); got != "codex" {
		t.Fatalf("Value() after SetKind(codex) = %q, want %q", got, "codex")
	}

	// A kind reachable only through the list. Both halves of the panel
	// must agree on it: the list's cursor moves there, and ←→ still walk
	// the chip row from wherever it was left.
	f.SetKind("gemini")
	if got := f.Value(); got != "gemini" {
		t.Fatalf("Value() after SetKind(gemini) = %q, want %q", got, "gemini")
	}
	if sel, ok := f.picker.Selected(); !ok || sel.ID != "gemini" {
		t.Errorf("the kind list's own cursor = %+v, want it moved to gemini too", sel)
	}
	f.Update(key(tea.KeyDown, 0))
	if got := f.Value(); got != "cursor" {
		t.Errorf("Down after SetKind(gemini) = %q, want %q -- the list cursor was seeded there", got, "cursor")
	}

	// A stale or typo'd persisted value never overrides the default.
	f.SetKind("no-such-agent")
	if got := f.Value(); got != "cursor" {
		t.Errorf("Value() after SetKind of an unknown kind = %q, want it unchanged", got)
	}
	f.SetKind("")
	if got := f.Value(); got != "cursor" {
		t.Errorf("Value() after SetKind(\"\") = %q, want it unchanged", got)
	}
}

// TestAgentField_SetKindTwiceLeavesTheChipsUsable pins the invariant a
// second SetKind call depends on: seeding a non-favorite default and then
// a favorite last-used kind (app.New does exactly that -- `[agents]
// default`, then last-used.json) must leave the chip row working. In v1
// it did not: the first call left the list "expanded", which made the
// chip row swallow ←→ entirely.
func TestAgentField_SetKindTwiceLeavesTheChipsUsable(t *testing.T) {
	f := NewAgentField(theme.Default())
	f.SetKinds([]string{"claude", "codex", "pi", "gemini", "cursor"})

	f.SetKind("gemini") // outside the chip row
	f.SetKind("claude") // has a chip
	if got := f.Value(); got != "claude" {
		t.Errorf("Value() = %q, want %q", got, "claude")
	}

	f.Focus()
	f.Update(key(tea.KeyRight, 0))
	if got := f.Value(); got != "codex" {
		t.Errorf("Right after seeding moved to %q, want %q", got, "codex")
	}
}
