package form

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/ZviBaratz/herdr-draft/internal/form/widgets"
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
	f.SetValue("Work on ENG-1: Fix login bug")
	if got := f.Value(); got != "Work on ENG-1: Fix login bug" {
		t.Errorf("Value() = %q, want the seeded text", got)
	}
}

// TestPromptField_InsertNewlineAddsALiteralNewline pins InsertNewline's
// own contract (widgets.PromptArea.InsertNewline: "inserts a literal
// newline ... without going through Update").
func TestPromptField_InsertNewlineAddsALiteralNewline(t *testing.T) {
	f := NewPromptField(theme.Default())
	f.SetValue("first line")
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

func TestPromptField_HeightIsConstant(t *testing.T) {
	f := NewPromptField(theme.Default())
	base := f.Height(24)
	if base != 1+widgets.PromptAreaPreferredRows {
		t.Errorf("Height(24) = %d, want %d", base, 1+widgets.PromptAreaPreferredRows)
	}

	f.SetValue("some text\nacross multiple lines\nof content")
	if got := f.Height(24); got != base {
		t.Errorf("Height(24) after SetValue = %d, want %d", got, base)
	}
	if got := strings.Count(f.View(60), "\n") + 1; got != base {
		t.Errorf("View(60) rendered %d physical lines, want Height()'s own %d", got, base)
	}
}

func TestPromptField_ViewShowsPlaceholderLadderEntry(t *testing.T) {
	f := NewPromptField(theme.Default())
	frame := ansi.Strip(f.View(80))
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
	_ = f.View(0)
	_ = f.View(-3)
}
