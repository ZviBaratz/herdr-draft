package form

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

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
	if !strings.Contains(frame, "optional") {
		t.Errorf("View(80) = %q, want it to contain a placeholder ladder entry", frame)
	}
	// v2 spec §8 made ↵ submit from this field, so no rung may still
	// offer it as a way past the prompt.
	if strings.Contains(frame, "Enter") || strings.Contains(frame, "skip") {
		t.Errorf("View(80) = %q, want no placeholder naming Enter as a way to skip the prompt", frame)
	}
}

// TestPromptField_PanelWrapsAtItsOwnMaxWidth pins v3 spec §6.3: with the
// kernel's width cap gone, the ONE surface that still wants a reading
// measure keeps its own. The prose wraps at promptPanelMaxWidth however
// wide the panel gets, while the panel LINE is still padded to the full
// width, so the region's background stays painted behind it.
//
// It is checked at a width no shipped popup produces on purpose: the
// clamp is inert at the 101-column pane (v3 spec §6.1) and exists for the
// oversized terminal, which is exactly the case a frame at the shipped
// size cannot cover.
func TestPromptField_PanelWrapsAtItsOwnMaxWidth(t *testing.T) {
	f := NewPromptField(theme.Default())
	f.SetValue(strings.Repeat("word ", 60), false)

	const w = 200
	lines := strings.Split(f.Panel(w, f.PanelRows()), "\n")
	if len(lines) < 2 {
		t.Fatalf("Panel(%d) produced %d lines, want the prose wrapped over several", w, len(lines))
	}
	for i, l := range lines {
		if got := ansi.StringWidth(l); got != w {
			t.Errorf("Panel(%d) line %d is %d cells wide, want the panel's full %d", w, i, got, w)
		}
		if got := ansi.StringWidth(strings.TrimRight(ansi.Strip(l), " ")); got > gutterWidth+promptPanelMaxWidth {
			t.Errorf("Panel(%d) line %d has %d cells of text, want at most the gutter plus %d", w, i, got, promptPanelMaxWidth)
		}
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
