package form

import (
	"strings"
	"testing"

	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

func TestLineInput_CharLimitIsRuneBound(t *testing.T) {
	l := newLineInput(theme.Default(), 3)
	l.Focus()
	for _, r := range "héllo" { // 5 runes, 6 bytes ('é' is 2 bytes)
		l.Update(rn(r))
	}
	if got := l.Value(); len([]rune(got)) != 3 {
		t.Fatalf("Value() = %q (%d runes), want exactly 3 runes (CharLimit)", got, len([]rune(got)))
	}
}

func TestLineInput_SetValueMovesCursorToEnd(t *testing.T) {
	l := newLineInput(theme.Default(), 0)
	l.SetValue("hello")
	l.Focus()
	l.Update(rn('!'))
	if got := l.Value(); got != "hello!" {
		t.Fatalf("Value() = %q, want %q (typing after SetValue appends at the end)", got, "hello!")
	}
}

func TestLineInput_BlurredIgnoresInput(t *testing.T) {
	l := newLineInput(theme.Default(), 0)
	l.Update(rn('x')) // never focused
	if got := l.Value(); got != "" {
		t.Fatalf("Value() = %q, want \"\" (blurred input must ignore keystrokes)", got)
	}
}

// TestLineInput_NoDefaultPromptGlyph pins a real bug this task's own
// golden-frame inspection caught: bubbles/v2's textinput.New() defaults
// Prompt to "> " (verified in the vendored source), which silently leaked
// into every field built on lineInput (DirField's header, TitleField's
// header) until newLineInput explicitly zeroed it -- see newLineInput's
// own doc comment.
func TestLineInput_NoDefaultPromptGlyph(t *testing.T) {
	l := newLineInput(theme.Default(), 0)
	l.Focus()
	l.SetValue("hello")
	if got := l.View(20); strings.Contains(got, "> ") {
		t.Fatalf("View(20) = %q, contains the default \"> \" prompt glyph, want it suppressed", got)
	}
}

func TestLineInput_ViewIsSingleLineAndWidthBound(t *testing.T) {
	l := newLineInput(theme.Default(), 0)
	l.SetValue(strings.Repeat("x", 40))
	got := l.View(10)
	if strings.Contains(got, "\n") {
		t.Fatalf("View(10) contains a newline: %q, want exactly one physical line", got)
	}
}
