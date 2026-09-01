package form

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

func manyKinds() []string {
	// More than agentFavoriteChips so a "more…" chip is guaranteed to
	// appear -- exercises the file doc's own design note.
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

// TestAgentField_FewerKindsThanCapShowsNoMoreChip pins the file doc's own
// "a config with fewer favorites than that just gets fewer chips" case: no
// "more…" chip, and Down simply cycles like Right.
func TestAgentField_FewerKindsThanCapShowsNoMoreChip(t *testing.T) {
	f := NewAgentField(theme.Default())
	f.SetKinds([]string{"claude", "codex"})

	f.Update(key(tea.KeyDown, 0))
	if got := f.Value(); got != "codex" {
		t.Fatalf("Value() after one Down (no more… chip present) = %q, want %q", got, "codex")
	}
	if f.expanded {
		t.Errorf("expanded = true, want false (nothing to expand into)")
	}
}

// TestAgentField_DownOnMoreChipExpandsFullList pins the field's own
// "more…" expansion design: Down while the chip cursor sits on "more…"
// opens the full-kind-list picker rather than wrapping the chip row.
func TestAgentField_DownOnMoreChipExpandsFullList(t *testing.T) {
	f := NewAgentField(theme.Default())
	f.SetKinds(manyKinds())

	// One Left from the first chip wraps straight to "more…" (ChipRow's
	// own wraparound cursor -- chiprow.go's wrapIndex) without passing
	// through any other favorite chip, so lastConfirmed stays at
	// SetKinds' own default ("claude", index 0) rather than whatever chip
	// a multi-step walk would have last landed on.
	f.Update(key(tea.KeyLeft, 0))
	f.Update(key(tea.KeyDown, 0))

	if !f.expanded {
		t.Fatalf("expanded = false after Down on the more… chip, want true")
	}
	if got := f.Value(); got != "claude" {
		t.Fatalf("Value() right after expanding = %q, want %q (seeded from lastConfirmed)", got, "claude")
	}

	f.Update(key(tea.KeyDown, 0)) // move into the full list
	if got := f.Value(); got != "codex" {
		t.Fatalf("Value() after one Down inside the expanded list = %q, want %q", got, "codex")
	}
}

// TestAgentField_UpAtTopOfListCollapses pins the reverse transition: Up at
// the expanded list's own top row collapses back to the chip row rather
// than clamping in place.
func TestAgentField_UpAtTopOfListCollapses(t *testing.T) {
	f := NewAgentField(theme.Default())
	f.SetKinds(manyKinds())
	f.Update(key(tea.KeyLeft, 0)) // wrap straight to "more…", see the sibling test's own comment
	f.Update(key(tea.KeyDown, 0)) // expand, cursor seeded at "claude" (row 0)

	f.Update(key(tea.KeyUp, 0))
	if f.expanded {
		t.Errorf("expanded = true after Up at the top row, want false (collapsed)")
	}
	if got := f.Value(); got != "claude" {
		t.Errorf("Value() after collapsing = %q, want %q (unchanged)", got, "claude")
	}
}

// TestAgentField_ExpandedListCanSelectAKindNotOnTheChipRow exercises the
// design note's core claim: a kind past agentFavoriteChips is reachable
// (and selectable) through the full list.
func TestAgentField_ExpandedListCanSelectAKindNotOnTheChipRow(t *testing.T) {
	f := NewAgentField(theme.Default())
	kinds := manyKinds() // gemini is kinds[4], past agentFavoriteChips (3)
	f.SetKinds(kinds)

	for range agentFavoriteChips {
		f.Update(key(tea.KeyRight, 0))
	}
	f.Update(key(tea.KeyDown, 0)) // expand
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

func TestAgentField_ViewShowsMoreChipWhenTruncated(t *testing.T) {
	f := NewAgentField(theme.Default())
	f.SetKinds(manyKinds())
	frame := ansi.Strip(f.View(60, f.Height(24)))
	if !strings.Contains(frame, "more…") {
		t.Errorf("View(60) = %q, want it to contain the more… chip", frame)
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

// TestAgentField_SetKindSelectsAFavoriteOrExpandsToReachIt pins the setter
// the state layer needed (finding I2): SetKinds' "index 0 is the default"
// contract could express only the CONFIGURED default, so
// config.State.LastKind had nowhere to be applied.
func TestAgentField_SetKindSelectsAFavoriteOrExpandsToReachIt(t *testing.T) {
	f := NewAgentField(theme.Default())
	f.SetKinds([]string{"claude", "codex", "pi", "gemini", "cursor"})

	if got := f.Value(); got != "claude" {
		t.Fatalf("Value() = %q, want the configured default %q", got, "claude")
	}

	// A kind with its own chip: the chip cursor moves, the list stays shut.
	f.SetKind("codex")
	if got := f.Value(); got != "codex" {
		t.Fatalf("Value() after SetKind(codex) = %q, want %q", got, "codex")
	}
	if frame := ansi.Strip(f.View(60, f.Height(40))); strings.Contains(frame, "gemini") {
		t.Errorf("View = %q, want the full list still collapsed for a favorite", frame)
	}

	// A kind only reachable behind "more…": the list opens on it, so the
	// user can see what is selected rather than a bare highlighted "more…".
	f.SetKind("gemini")
	if got := f.Value(); got != "gemini" {
		t.Fatalf("Value() after SetKind(gemini) = %q, want %q", got, "gemini")
	}
	if frame := ansi.Strip(f.View(60, f.Height(40))); !strings.Contains(frame, "gemini") {
		t.Errorf("View = %q, want the expanded list showing the selected kind", frame)
	}

	// A stale or typo'd persisted value never overrides the default.
	f.SetKind("no-such-agent")
	if got := f.Value(); got != "gemini" {
		t.Errorf("Value() after SetKind of an unknown kind = %q, want it unchanged", got)
	}
	f.SetKind("")
	if got := f.Value(); got != "gemini" {
		t.Errorf("Value() after SetKind(\"\") = %q, want it unchanged", got)
	}
}

// TestAgentFieldSetKindCollapsesWhenTheKindHasAChip pins the invariant a
// second SetKind call depends on: after SetKind, the expanded "more…"
// list must never be left highlighting a kind other than the selected
// one. Before this, seeding a non-favorite default and then a favorite
// last-used kind opened the form with the list expanded on the default,
// the chip row showing the last-used kind, and Left/Right doing nothing.
func TestAgentFieldSetKindCollapsesWhenTheKindHasAChip(t *testing.T) {
	f := NewAgentField(theme.Default())
	f.SetKinds([]string{"claude", "codex", "pi", "gemini", "cursor"})

	f.SetKind("gemini") // outside the chip row: expands the full list
	if !f.expanded {
		t.Fatalf("SetKind(non-favorite) did not expand the list, so this test proves nothing")
	}

	f.SetKind("claude") // has a chip: must collapse again
	if f.expanded {
		t.Errorf("SetKind(favorite) left the more… list expanded")
	}
	if got := f.Value(); got != "claude" {
		t.Errorf("Value() = %q, want %q", got, "claude")
	}

	// The chip row owns Left/Right only while collapsed -- the observable
	// symptom of the stale flag.
	f.Focus()
	f.Update(key(tea.KeyRight, 0))
	if got := f.Value(); got != "codex" {
		t.Errorf("Right after seeding moved to %q, want %q (chip row inert while expanded)", got, "codex")
	}
}
