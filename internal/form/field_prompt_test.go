package form

import (
	"strings"
	"testing"

	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

func TestPromptField_IDAndEnabled(t *testing.T) {
	f := NewPromptField(theme.Default())
	if f.ID() != "prompt" {
		t.Errorf("ID() = %q, want %q", f.ID(), "prompt")
	}
	if !f.Enabled() {
		t.Errorf("Enabled() = false, want true (Prompt is always present)")
	}
}

// TestPromptField_ImplementsNewliner pins form.go's newliner capability
// wiring: ZonePrompt is the only zone MapKey ever returns ActionNewline
// for, and form.go's handleKey type-asserts the focused Section for it.
func TestPromptField_ImplementsNewliner(t *testing.T) {
	var s Section = NewPromptField(theme.Default())
	if _, ok := s.(newliner); !ok {
		t.Fatalf("*PromptField does not implement newliner")
	}
}

func TestPromptField_ValueRoundTrips(t *testing.T) {
	f := NewPromptField(theme.Default())
	f.SetValue("Work on ENG-1: Fix login bug", false)
	if got := f.Value(); got != "Work on ENG-1: Fix login bug" {
		t.Errorf("Value() = %q, want the seeded text", got)
	}
}

// TestPromptField_TouchedBlocksFurtherSeeding pins SetValue's
// touched-vs-preselected rule: once the user has edited the field
// (Touched() == true), a further seeded == true call must be silently
// ignored, matching field_worktree.go's identical WorktreeField.SetBranch
// contract.
func TestPromptField_TouchedBlocksFurtherSeeding(t *testing.T) {
	f := NewPromptField(theme.Default())
	if f.Touched() {
		t.Fatalf("Touched() = true before any input, want false")
	}

	f.SetValue("seed one", true)
	if got := f.Value(); got != "seed one" {
		t.Fatalf("Value() after first seed = %q, want %q", got, "seed one")
	}

	f.Focus()
	f.Update(rn('!'))
	if !f.Touched() {
		t.Fatalf("Touched() = false after typing, want true")
	}

	f.SetValue("seed two", true)
	if got := f.Value(); got != "seed one!" {
		t.Fatalf("Value() after a seeded call post-touch = %q, want the user's own edit %q unclobbered", got, "seed one!")
	}

	// A hard (seeded == false) set always applies and clears touched, so a
	// later seed can apply again.
	f.SetValue("hard reset", false)
	if got := f.Value(); got != "hard reset" {
		t.Fatalf("Value() after a hard set = %q, want %q", got, "hard reset")
	}
	if f.Touched() {
		t.Fatalf("Touched() = true after a hard set, want false (cleared)")
	}
	f.SetValue("seed three", true)
	if got := f.Value(); got != "seed three" {
		t.Fatalf("Value() after re-seeding post-hard-reset = %q, want %q", got, "seed three")
	}
}

// TestPromptField_InsertNewlineTouches pins that InsertNewline -- which
// bypasses Update entirely (see its own doc comment) -- still marks the
// field touched, so a newline-only edit blocks further seeding too.
func TestPromptField_InsertNewlineTouches(t *testing.T) {
	f := NewPromptField(theme.Default())
	f.InsertNewline()
	if !f.Touched() {
		t.Fatalf("Touched() = false after InsertNewline, want true")
	}
}

// TestPromptField_InsertNewlineAddsALiteralNewline pins InsertNewline's
// own contract (widgets.PromptArea.InsertNewline: "inserts a literal
// newline ... without going through Update").
func TestPromptField_InsertNewlineAddsALiteralNewline(t *testing.T) {
	f := NewPromptField(theme.Default())
	f.SetValue("first line", false)
	f.InsertNewline()
	if got := f.Value(); !strings.Contains(got, "\n") {
		t.Errorf("Value() after InsertNewline = %q, want it to contain a newline", got)
	}
}

func TestPromptField_FocusReturnsBlinkCmd(t *testing.T) {
	f := NewPromptField(theme.Default())
	if cmd := f.Focus(); cmd == nil {
		t.Errorf("Focus() returned a nil tea.Cmd, want the wrapped PromptArea's own cursor-blink Cmd")
	}
}

func TestPromptField_ViewShowsPlaceholderLadderEntry(t *testing.T) {
	f := NewPromptField(theme.Default())
	frame := fieldText(f, 80)
	if !strings.Contains(frame, "Optional") {
		t.Errorf("View(80) = %q, want it to contain a placeholder ladder entry", frame)
	}
}

func TestPromptField_NoPanicOnDegenerateWidth(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("PromptField panicked: %v", r)
		}
	}()
	f := NewPromptField(theme.Default())
	_ = f.Row(0)
	_ = f.Panel(0, f.PanelRows())
	_ = f.Row(-3)
	_ = f.Panel(-3, f.PanelRows())
}
