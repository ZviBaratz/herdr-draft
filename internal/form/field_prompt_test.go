package form

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
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

func TestPromptField_ImplementsToggler(t *testing.T) {
	var s Section = NewPromptField(theme.Default())
	if _, ok := s.(toggler); !ok {
		t.Fatalf("*PromptField does not implement toggler")
	}
}

// TestPromptField_ReapDefaultsToKeep is reap spec §3.1 at the field: a fresh
// field is off until the app seeds it (Task 4) or the user flips it.
func TestPromptField_ReapDefaultsToKeep(t *testing.T) {
	f := NewPromptField(theme.Default())
	if f.MarkReady() {
		t.Fatal("fresh PromptField.MarkReady() = true, want keep")
	}
	f.SetMarkReady(true)
	if !f.MarkReady() {
		t.Fatal("SetMarkReady(true) did not select reap")
	}
	f.Toggle()
	if f.MarkReady() {
		t.Fatal("Toggle() from reap did not return to keep")
	}
}

// TestPromptField_RowStatesTheReapOnlyWhenItWillBeSent is reap spec §5.3's
// table: a row states what the session will be (v2 spec §3 rule 1), so the
// suffix appears exactly when plan.PromptText will append.
func TestPromptField_RowStatesTheReapOnlyWhenItWillBeSent(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		on    bool
		want  bool
	}{
		{"keep", "fix it", false, false},
		{"reap over a prompt", "fix it\nand more", true, true},
		{"reap over nothing", "", true, false},
		{"reap over a slash command", "/loop 5m", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := NewPromptField(theme.Default())
			f.SetValue(tc.value, false)
			f.SetMarkReady(tc.on)
			row := ansi.Strip(f.Row(70))
			if got := strings.Contains(row, "reap when done"); got != tc.want {
				t.Errorf("Row(70) = %q, contains \"reap when done\" = %v, want %v", row, got, tc.want)
			}
			if tc.want && !strings.Contains(row, "+1 more") {
				t.Errorf("Row(70) = %q, want the +N more count kept beside the suffix", row)
			}
		})
	}
}

// TestPromptField_PanelLeadsWithTheReapLine pins reap spec §5.1: the chips
// are the panel's FIRST line, so they do not move as the textarea grows,
// and the text starts on the line below.
func TestPromptField_PanelLeadsWithTheReapLine(t *testing.T) {
	f := NewPromptField(theme.Default())
	f.SetValue("first line of the prompt", false)
	lines := strings.Split(ansi.Strip(f.Panel(80, f.PanelRows())), "\n")
	if len(lines) != f.PanelRows() {
		t.Fatalf("Panel produced %d lines, want PanelRows() = %d", len(lines), f.PanelRows())
	}
	if !strings.Contains(lines[0], "keep") || !strings.Contains(lines[0], "reap") {
		t.Errorf("panel line 0 = %q, want the keep · reap chips", lines[0])
	}
	if !strings.Contains(lines[1], "first line of the prompt") {
		t.Errorf("panel line 1 = %q, want the textarea's first line", lines[1])
	}
}

// TestPromptField_PanelHintSaysWhyTheReapWillNotBeSent is the panel half of
// reap spec §5.3's table.
func TestPromptField_PanelHintSaysWhyTheReapWillNotBeSent(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		on          bool
		want        string
	}{
		{"keep", "fix it", false, promptReapHintOff},
		{"reap", "fix it", true, promptReapHintOn},
		{"reap over nothing", "", true, promptReapHintEmpty},
		{"reap over a slash command", "/loop", true, promptReapHintSlash},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := NewPromptField(theme.Default())
			f.SetValue(tc.value, false)
			f.SetMarkReady(tc.on)
			first := strings.SplitN(ansi.Strip(f.Panel(200, f.PanelRows())), "\n", 2)[0]
			if !strings.Contains(first, tc.want) {
				t.Errorf("panel line 0 = %q, want it to carry %q", first, tc.want)
			}
		})
	}
}

// TestPromptField_OneRowPanelKeepsTheTextarea is reap spec §5.4: when the
// region leaves one row, it goes to the text being typed, not the toggle.
func TestPromptField_OneRowPanelKeepsTheTextarea(t *testing.T) {
	f := NewPromptField(theme.Default())
	f.SetValue("only line", false)
	panel := ansi.Strip(f.Panel(80, 1))
	if strings.Contains(panel, "reap") {
		t.Errorf("Panel(80, 1) = %q, want the textarea alone", panel)
	}
	if !strings.Contains(panel, "only line") {
		t.Errorf("Panel(80, 1) = %q, want the prompt text", panel)
	}
}

// TestModel_WordLeftInAnEmptyPromptReturns is widgets'
// TestPromptArea_WordLeftReturnsAtTheStartOfTheText along the route a
// keypress actually takes: Model.Update, MapKey, PromptField.Update and only
// then the textarea. Under charm.land/bubbles/v2 v2.1.1, ⌥← or ⌥B in an
// empty prompt never came back from the first of those, so the popup stopped
// answering every key after it, esc included; v2.2.1 fixed the loop upstream
// (charmbracelet/bubbles#1036). ⌃← is here because v2.2.0 bound it to the
// same word-left.
//
// Two guards come first, because either failing would make the bounded
// Update below return at once without the loop ever being entered, and the
// test would pass for that reason instead: the key must be ActionNone in the
// prompt, so the grammar forwards it rather than consuming it, and the
// textarea must be focused, since a blurred one ignores every key.
func TestModel_WordLeftInAnEmptyPromptReturns(t *testing.T) {
	for _, k := range []tea.KeyPressMsg{
		{Code: tea.KeyLeft, Mod: tea.ModAlt},
		{Code: 'b', Mod: tea.ModAlt},
		{Code: tea.KeyLeft, Mod: tea.ModCtrl},
	} {
		t.Run(k.String(), func(t *testing.T) {
			if action, _ := MapKey(k, FocusZone{Kind: ZonePrompt}, false); action != ActionNone {
				t.Fatalf("MapKey(%s) in the prompt = %v, want ActionNone (forwarded to the textarea)", k.String(), action)
			}
			f := NewPromptField(theme.Default())
			m := fieldFrame(theme.Default(), f)
			if !f.area.Focused() {
				t.Fatal("the prompt's textarea is not focused, so it would ignore the key")
			}
			if !returnsWithin(2*time.Second, func() { m.Update(k) }) {
				t.Fatalf("Model.Update(%s) in an empty prompt did not return within 2s", k.String())
			}
			if got := f.Value(); got != "" {
				t.Errorf("Value() = %q after %s, want the prompt still empty", got, k.String())
			}
		})
	}
}

// returnsWithin reports whether fn returns within d, running it on a
// goroutine it cannot stop -- see widgets' copy of this helper for why a
// failing case leaving that goroutine behind is the right trade.
func returnsWithin(d time.Duration, fn func()) bool {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}
