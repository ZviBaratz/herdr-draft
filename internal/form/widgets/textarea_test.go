package widgets

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// TestPromptArea_DefaultHeightIsFourRowsPreferred covers the brief's "4
// rows preferred" contract: a freshly constructed PromptArea, before
// SetRows is ever called, renders exactly 4 lines.
func TestPromptArea_DefaultHeightIsFourRowsPreferred(t *testing.T) {
	p := NewPromptArea(testPalette())
	view := p.View(40)
	if got := strings.Count(view, "\n") + 1; got != PromptAreaPreferredRows {
		t.Fatalf("View(40) has %d lines, want %d (preferred rows)\n%q", got, PromptAreaPreferredRows, view)
	}
}

// TestPromptArea_SetRowsFloorsAtOne covers the brief's "1 row floor":
// SetRows must never let the textarea collapse to zero (or negative)
// height, no matter what the form's degradation ladder (task 16) asks for.
func TestPromptArea_SetRowsFloorsAtOne(t *testing.T) {
	p := NewPromptArea(testPalette())

	for _, requested := range []int{1, 0, -5} {
		p.SetRows(requested)
		view := p.View(40)
		got := strings.Count(view, "\n") + 1
		if got != PromptAreaMinRows {
			t.Errorf("SetRows(%d); View(40) has %d lines, want %d (floor)", requested, got, PromptAreaMinRows)
		}
	}
}

// TestPromptArea_SetRowsHoldsAnyValueAboveTheFloor pins that SetRows isn't
// silently clamped to some other constant besides the floor -- a
// mid-degradation height (e.g. 2, chosen by task 16's ladder between the
// preferred 4 and the 1-row floor) must render at exactly that height.
func TestPromptArea_SetRowsHoldsAnyValueAboveTheFloor(t *testing.T) {
	p := NewPromptArea(testPalette())
	p.SetRows(2)
	view := p.View(40)
	if got := strings.Count(view, "\n") + 1; got != 2 {
		t.Fatalf("SetRows(2); View(40) has %d lines, want 2", got)
	}
}

// ladderFixture returns three placeholder candidates of strictly decreasing
// rendered width, none of which is a substring of another (so a
// strings.Contains check unambiguously identifies which one is present),
// suitable for both TestSelectPlaceholder_PicksWidestFitting (exact
// boundary widths, derived from the fixtures' own measured widths -- never
// hand-guessed, since bubbles/ANSI rendering makes eyeballed cell counts
// easy to get wrong) and
// TestPromptArea_ViewUsesSelectPlaceholderForItsLadder (a lighter
// rendering-level check that View actually wires SetPlaceholderLadder's
// stored candidates into selectPlaceholder rather than ignoring it).
func ladderFixture(t *testing.T) (ladder []string, widths []int) {
	t.Helper()
	ladder = []string{
		"widest candidate for the prompt placeholder ladder",
		"medium candidate for the ladder",
		"short one",
	}
	widths = make([]int, len(ladder))
	for i, s := range ladder {
		widths[i] = lipgloss.Width(s)
	}
	for i := 1; i < len(widths); i++ {
		if widths[i] >= widths[i-1] {
			t.Fatalf("ladderFixture: widths must strictly decrease, got %v for %v", widths, ladder)
		}
	}
	for i, a := range ladder {
		for j, b := range ladder {
			if i != j && strings.Contains(a, b) {
				t.Fatalf("ladderFixture: %q must not contain %q as a substring (ambiguous for Contains-based assertions)", a, b)
			}
		}
	}
	return ladder, widths
}

// TestSelectPlaceholder_PicksWidestFitting is the brief's ladder test,
// exercised directly against selectPlaceholder (the pure selection
// function View delegates to) rather than through a rendered View() call:
// bubbles' placeholder rendering word-wraps and hard-wraps any candidate
// that doesn't fit the given width at all (see
// TestPromptArea_PlaceholderNeverBreaksFixedLineCount), which is the right
// behavior for the widget but makes the *selection* boundary hard to pin
// precisely through rendered output alone -- testing selectPlaceholder
// directly isolates "which candidate was chosen" from "how a chosen-but-
// still-too-wide candidate gets wrapped onto the fixed row budget."
func TestSelectPlaceholder_PicksWidestFitting(t *testing.T) {
	ladder, widths := ladderFixture(t)

	cases := []struct {
		name  string
		width int
		want  string
	}{
		{"everything fits; widest wins", widths[0] + 10, ladder[0]},
		{"exact fit for the widest", widths[0], ladder[0]},
		{"widest no longer fits by one cell", widths[0] - 1, ladder[1]},
		{"exact fit for the middle entry", widths[1], ladder[1]},
		{"middle no longer fits by one cell", widths[1] - 1, ladder[2]},
		{"exact fit for the narrowest", widths[2], ladder[2]},
		{"nothing fits; falls back to the narrowest anyway", widths[2] - 1, ladder[2]},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := selectPlaceholder(ladder, c.width); got != c.want {
				t.Errorf("selectPlaceholder(ladder, %d) = %q, want %q", c.width, got, c.want)
			}
		})
	}
}

// TestSelectPlaceholder_EmptyLadderReturnsEmpty guards the degenerate input
// PromptArea's own TestPromptArea_EmptyLadderLeavesPlaceholderBlank relies
// on: no candidates means no placeholder, not a panic.
func TestSelectPlaceholder_EmptyLadderReturnsEmpty(t *testing.T) {
	if got := selectPlaceholder(nil, 40); got != "" {
		t.Errorf("selectPlaceholder(nil, 40) = %q, want \"\"", got)
	}
}

// TestSelectPlaceholder_DoesNotAssumeLadderIsPreSorted covers
// selectPlaceholder's doc claim that it measures every candidate rather
// than trusting caller ordering: the widest-fitting candidate must be found
// regardless of where it sits in the slice.
func TestSelectPlaceholder_DoesNotAssumeLadderIsPreSorted(t *testing.T) {
	ladder, widths := ladderFixture(t)
	scrambled := []string{ladder[2], ladder[0], ladder[1]} // narrowest first, widest in the middle

	if got := selectPlaceholder(scrambled, widths[0]+10); got != ladder[0] {
		t.Errorf("selectPlaceholder(scrambled, %d) = %q, want the widest candidate %q regardless of slice order", widths[0]+10, got, ladder[0])
	}
}

// TestPromptArea_ViewUsesSelectPlaceholderForItsLadder is the
// rendering-level half of the ladder contract: SetPlaceholderLadder's
// stored candidates must actually reach the wrapped textarea's Placeholder
// field through View, not just sit unused in a field. It checks the
// ANSI-stripped view for the expected text (see the comment inline below
// for why stripping is required) at two widths wide enough that the
// selected candidate always fits without wrapping, so this test only
// exercises "was the right candidate wired in," leaving the exhaustive
// boundary math to TestSelectPlaceholder_PicksWidestFitting above.
func TestPromptArea_ViewUsesSelectPlaceholderForItsLadder(t *testing.T) {
	ladder, widths := ladderFixture(t)

	cases := []struct {
		width int
		want  string
	}{
		{widths[0] + 10, ladder[0]}, // wide enough for the widest candidate
		{widths[1], ladder[1]},      // too narrow for the widest, exact fit for the middle
	}
	for _, c := range cases {
		p := NewPromptArea(testPalette())
		p.SetPlaceholderLadder(ladder)
		// bubbles' placeholderView renders the empty textarea's first
		// character through its virtual cursor's own Style.Render call,
		// separately from the rest of the placeholder text (see
		// textarea.go's placeholderView in the vendored source) -- so the
		// raw View() output never contains a candidate as one
		// ANSI-code-free contiguous substring, even the selected one.
		// Strip the ANSI codes first so the plain characters (which do
		// stay in original order across that split) read back as the
		// literal placeholder text.
		plain := ansi.Strip(p.View(c.width))
		if !strings.Contains(plain, c.want) {
			t.Errorf("width=%d: View (ANSI-stripped) does not contain expected placeholder %q\n%s", c.width, c.want, plain)
		}
	}
}

// TestPromptArea_PlaceholderNeverBreaksFixedLineCount is the
// controller-ruled regression style guard from Task 14's Critical finding
// (lipgloss Style.Render word-wraps before MaxWidth truncation unless
// Inline(true) is set): a placeholder far wider than the given width, at
// the 1-row floor, must still render as exactly PromptAreaMinRows lines,
// never more.
func TestPromptArea_PlaceholderNeverBreaksFixedLineCount(t *testing.T) {
	p := NewPromptArea(testPalette())
	p.SetRows(PromptAreaMinRows)
	p.SetPlaceholderLadder([]string{
		"this placeholder sentence is far longer than any width this test gives View, on purpose",
	})

	view := p.View(10)
	if got := strings.Count(view, "\n") + 1; got != PromptAreaMinRows {
		t.Fatalf("View(10) has %d lines, want %d (floor) even with an overlong placeholder\n%q", got, PromptAreaMinRows, view)
	}
}

// TestPromptArea_EmptyLadderLeavesPlaceholderBlank guards against a panic
// or a stale placeholder when SetPlaceholderLadder is never called (or
// called with an empty slice): PromptArea must still render cleanly.
func TestPromptArea_EmptyLadderLeavesPlaceholderBlank(t *testing.T) {
	p := NewPromptArea(testPalette())
	view := p.View(40) // no SetPlaceholderLadder call at all
	if got := strings.Count(view, "\n") + 1; got != PromptAreaPreferredRows {
		t.Fatalf("View(40) has %d lines, want %d", got, PromptAreaPreferredRows)
	}
}

// TestPromptArea_ViewDoesNotPanicOnDegenerateWidth mirrors Picker's
// no-panic guard for boundary width values a caller could pass while a
// popup is being resized.
func TestPromptArea_ViewDoesNotPanicOnDegenerateWidth(t *testing.T) {
	p := NewPromptArea(testPalette())
	p.SetPlaceholderLadder([]string{"Optional"})
	for _, w := range []int{0, -5, 1} {
		_ = p.View(w)
	}
}

// TestPromptArea_ValueRoundTrips covers the basic SetValue/Value contract
// the form needs to seed and read back the prompt's text.
func TestPromptArea_ValueRoundTrips(t *testing.T) {
	p := NewPromptArea(testPalette())
	p.SetValue("hello there")
	if got := p.Value(); got != "hello there" {
		t.Fatalf("Value() = %q, want %q", got, "hello there")
	}
}

// TestPromptArea_InsertNewlineAddsALine covers the hook form.go (task 16)
// calls when MapKey returns ActionNewline (⌃J/⇧↵/⌥↵ in the prompt zone) --
// InsertNewline must add a literal newline to the value without going
// through Update (bare Enter must never reach the wrapped bubbles textarea
// as a keypress, since its own default keymap binds "enter" to insert a
// newline, which would defeat the grammar's advance-on-Enter rule; see the
// package doc).
func TestPromptArea_InsertNewlineAddsALine(t *testing.T) {
	p := NewPromptArea(testPalette())
	p.SetValue("first")
	p.InsertNewline()
	if got, want := p.Value(), "first\n"; got != want {
		t.Fatalf("Value() after InsertNewline() = %q, want %q", got, want)
	}
}

// TestPromptArea_FocusBlur covers the Focus/Blur/Focused surface task 16's
// focus ring needs to drive.
func TestPromptArea_FocusBlur(t *testing.T) {
	p := NewPromptArea(testPalette())
	if p.Focused() {
		t.Fatalf("Focused() = true on a freshly constructed PromptArea, want false")
	}
	p.Focus()
	if !p.Focused() {
		t.Fatalf("Focused() = false after Focus(), want true")
	}
	p.Blur()
	if p.Focused() {
		t.Fatalf("Focused() = true after Blur(), want false")
	}
}

// TestPromptArea_UpdateInsertsTypedRunesWhileFocused covers PromptArea's
// pass-through of ordinary typing to the wrapped bubbles textarea -- the
// editing behavior (arrow movement, backspace, word-delete, ...) this
// widget does not reimplement, per the brief's "wraps charm.land/bubbles/v2
// textarea." bubbles' Model.Update ignores all input while blurred, so
// this must Focus() first, matching real form usage where only the
// focused zone's widget ever receives keypresses.
func TestPromptArea_UpdateInsertsTypedRunesWhileFocused(t *testing.T) {
	p := NewPromptArea(testPalette())
	p.Focus()
	p.Update(tea.KeyPressMsg{Code: 'h', Text: "h"})
	p.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	if got := p.Value(); got != "hi" {
		t.Fatalf("Value() after typing \"hi\" = %q, want %q", got, "hi")
	}
}

// TestPromptArea_UpdateIgnoresInputWhileBlurred pins that a keypress
// delivered to a blurred PromptArea (e.g. a stray Update call routed to
// the wrong zone) is inert -- bubbles' own Model.Update behavior, but
// worth pinning since PromptArea forwards to it directly.
func TestPromptArea_UpdateIgnoresInputWhileBlurred(t *testing.T) {
	p := NewPromptArea(testPalette())
	p.Update(tea.KeyPressMsg{Code: 'h', Text: "h"})
	if got := p.Value(); got != "" {
		t.Fatalf("Value() after typing while blurred = %q, want \"\" (blurred textarea ignores input)", got)
	}
}

// TestPromptArea_RawUpdateEnterInsertsNewline documents the exact footgun
// the package doc warns callers about: bubbles/v2 textarea's DefaultKeyMap
// binds "enter" (and "ctrl+m") to InsertNewline (verified directly against
// charm.land/bubbles/v2@v2.1.1/textarea/textarea.go's DefaultKeyMap, not
// assumed). If PromptArea's caller (task 16's form.go) ever forwarded a
// bare Enter keypress to Update unfiltered instead of routing it through
// keys.go's MapKey first, it would silently insert a newline instead of
// advancing -- defeating "bare Enter in the prompt zone advances" (spec
// §6). PromptArea itself does not rebind or filter this; the grammar layer
// is responsible for intercepting Enter before it ever reaches Update. This
// test pins the wrapped widget's raw behavior so a future bubbles upgrade
// that changes it is caught here rather than silently downstream.
func TestPromptArea_RawUpdateEnterInsertsNewline(t *testing.T) {
	p := NewPromptArea(testPalette())
	p.Focus()
	p.SetValue("x")
	p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if got, want := p.Value(), "x\n"; got != want {
		t.Fatalf("Value() after a raw Enter Update = %q, want %q (bubbles' own InsertNewline default binding)", got, want)
	}
}
