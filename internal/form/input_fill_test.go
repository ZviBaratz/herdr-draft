package form

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/ZviBaratz/herdr-draft/internal/form/widgets"
	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// input_fill_test.go pins v3 spec §8.7. Like picker_fill_test.go beside it,
// every claim here is about BYTES rather than characters: a rendered input
// looks identical in a stripped golden frame whether or not its fill is
// there, so a frame diff can neither confirm nor refute any of it. The
// helpers -- backgroundPerCell, rgbKey, lineWithBackground -- live in
// picker_fill_test.go; the two files are asking the same question of two
// different painters.
//
// There is a second, larger reason these tests exist. §8.7 specifies the
// fill as a flat palette.Surface, and on catppuccin -- the default theme --
// Surface and ActiveRowBG are the byte-identical #313244, so a flat Surface
// fill inside a focused row would have been 1.000:1: a correct
// implementation, green frames, and not one pixel of change on the screen
// most users see. theme.Palette.InputFill is the correction, and the floor
// it guarantees is asserted in internal/theme/contrast_test.go. What is
// asserted here is that the guaranteed color actually reaches the terminal.

// fillRun returns the [first,last] cell range over which bg is the active
// background on line, and reports whether it is unbroken across that range.
// The range rather than a "the line ends painted" check is the point: a
// fill that dropped out after the typed text and came back for the trailing
// pad would satisfy "the last cell is painted" while looking exactly like
// the defect on screen.
func fillRun(line, bg string) (first, last int, unbroken bool) {
	cells := backgroundPerCell(line)
	first, last = -1, -1
	for i, c := range cells {
		if c != bg {
			continue
		}
		if first < 0 {
			first = i
		}
		last = i
	}
	if first < 0 {
		return -1, -1, false
	}
	for i := first; i <= last; i++ {
		if cells[i] != bg {
			return first, last, false
		}
	}
	return first, last, true
}

// TestLineInput_FillSurvivesEveryEmbeddedReset is the reset hazard in
// isolation. textinput.View emits several independently styled spans on one
// line -- the typed text, the placeholder, the cursor -- and each closes
// with lipgloss's unconditional trailing reset, which clears an enclosing
// background along with its own foreground. paintLine reasserts the fill
// after each of them; nothing else would.
//
// Both states are checked because they embed different span sets: an empty
// input renders the italic placeholder span, a typed one renders the text
// span, and the failure mode is per-span.
func TestLineInput_FillSurvivesEveryEmbeddedReset(t *testing.T) {
	palette := theme.Default()
	fill := rgbKey(palette.InputFill(palette.ActiveRowBG))

	for _, tc := range []struct {
		name  string
		value string
	}{
		{"empty, showing its placeholder", ""},
		{"with typed text", "fix login redirect loop"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := newLineInput(palette, 0, palette.ActiveRowBG)
			l.SetPlaceholder("untitled")
			l.Focus()
			l.SetValue(tc.value)

			const width = 40
			line := l.View(width)
			if strings.Count(line, "\x1b[m") < 2 {
				t.Fatalf("View emits fewer than two resets (%q) -- this fixture is not exercising the hazard it exists for", line)
			}

			first, last, unbroken := fillRun(line, fill)
			if first < 0 {
				t.Fatalf("View(%d) carries no fill at all:\n%q", width, line)
			}
			if !unbroken {
				t.Errorf("the fill breaks somewhere inside [%d,%d] -- a reset cleared it and nothing put it back:\n%q", first, last, line)
			}
			// The whole width, not merely most of it: the fill IS the
			// affordance, so an input that stops being painted where its
			// text stops has not marked the editable region.
			if first != 0 || last != width-1 {
				t.Errorf("the fill covers cells [%d,%d], want [0,%d] -- the input's full width:\n%q", first, last, width-1, line)
			}
		})
	}
}

// TestFrame_FocusedInputFillWinsTheRowRepaint pins the composition, which
// is the half no unit test on lineInput can reach: composeRows paints the
// whole focused stack row ActiveRowBG AFTER the input has already painted
// itself, by inserting its own background code after every reset in a
// string the input's paint has already annotated at those same resets. The
// input's fill therefore lands last and wins -- a byte-ordering fact that
// falls out of ReplaceAll running over an already-edited string, stated
// nowhere in either function, so pinned rather than reasoned about.
//
// It also pins the thing §8.7 got wrong: the fill has to be a DIFFERENT
// color from the row it sits in. On catppuccin a flat Surface fill would
// satisfy every other assertion in this test and be invisible.
func TestFrame_FocusedInputFillWinsTheRowRepaint(t *testing.T) {
	palette := theme.Default()
	fill := rgbKey(palette.InputFill(palette.ActiveRowBG))
	activeRow := rgbKey(palette.ActiveRowBG)

	if fill == activeRow {
		t.Fatalf("the input fill %s is the focused row's own background -- the input is invisible on the row it only ever renders on", fill)
	}

	frame := buildTitlePanelForm(palette).ViewAt(80, 24)
	line, ok := lineWithBackground(frame, fill)
	if !ok {
		t.Fatalf("no line in the frame carries the input fill %s:\n%s", fill, frame)
	}

	first, last, unbroken := fillRun(line, fill)
	if !unbroken {
		t.Errorf("the input fill breaks inside [%d,%d] of the composed row:\n%q", first, last, line)
	}

	// The run is the row's VALUE cell exactly: renderStackRow spends the
	// gutter and the label column before handing what is left to Row.
	// Derived from the layout rather than written out, so a change to the
	// form's horizontal arithmetic moves this assertion with it.
	_, inner := contentBox(80)
	_, wantWidth := labelCol(inner)
	if got := last - first + 1; got != wantWidth {
		t.Errorf("the input fill is %d cells wide, want %d (the row's whole value column):\n%q", got, wantWidth, line)
	}
	// Everything to the left of the input on that row is still the focused
	// row's own fill: the input marks the editable region, not the row.
	cells := backgroundPerCell(line)
	if first == 0 || cells[first-1] != activeRow {
		t.Errorf("the cell left of the input is %q, want the focused row's %q -- the fill has escaped its value cell:\n%q",
			cells[max(first-1, 0)], activeRow, line)
	}
}

// TestLineInput_TerminalThemeGetsNoFill is the exemption that makes the
// whole feature safe to ship unconditionally. herdr's terminal palette maps
// surface0 to Color::Reset -- "inherit whatever the host terminal is using"
// -- which this package carries as lipgloss.NoColor{}. Painting it would
// mean writing opaque black over a user who chose that theme precisely so
// nothing would be written.
//
// Two guards have to hold for that, and both are asserted: InputFill must
// pass an unmeasurable Surface through instead of mixing it into a real
// color, and paintLine must then decline to emit the color it was handed.
// Either one alone would be enough today; neither alone is a contract.
func TestLineInput_TerminalThemeGetsNoFill(t *testing.T) {
	palette, ok := theme.Builtin("terminal")
	if !ok {
		t.Fatal("theme.Builtin(\"terminal\") is not a known builtin")
	}
	if _, inherit := palette.Surface.(lipgloss.NoColor); !inherit {
		t.Fatalf("the terminal palette's Surface is %v, not NoColor -- this test's premise has changed", palette.Surface)
	}
	if _, inherit := palette.InputFill(palette.ActiveRowBG).(lipgloss.NoColor); !inherit {
		t.Errorf("InputFill mixed an inherit-the-terminal Surface into a real color: %v", palette.InputFill(palette.ActiveRowBG))
	}

	l := newLineInput(palette, 0, palette.ActiveRowBG)
	l.SetPlaceholder("untitled")
	l.Focus()

	for i, bg := range backgroundPerCell(l.View(40)) {
		if bg != "" {
			t.Fatalf("cell %d of the terminal theme's input is painted %q, want the host terminal's own background:\n%q", i, bg, l.View(40))
		}
	}
}

// TestPromptArea_FillCoversEveryRowIncludingTheCursorLine is the four-row
// block's version of the same question, plus one hazard the single-line
// inputs do not have: bubbles gives the textarea's CURSOR line its own
// StyleState entry, distinct from Text, and a cursor line that ended up on
// a different background from the rows around it would read as a stripe
// across the fill rather than as a filled block.
//
// §8.7 anticipates that and prescribes a Background on CursorLine/
// EndOfBuffer in paletteStyles. It is not needed, and this test is why it
// was not added: those styles set a Foreground only and so emit no
// background code of their own, which leaves PaintLine's reasserted fill in
// force across every row. The prescription would have painted the same
// color a second time. If a future bubbles release starts emitting a
// background there, this test fails and the prescription becomes correct.
func TestPromptArea_FillCoversEveryRowIncludingTheCursorLine(t *testing.T) {
	palette := theme.Default()
	fill := rgbKey(palette.InputFill(palette.PanelBG))

	f := NewPromptField(palette)
	f.SetValue("first line\nsecond line", false)
	f.Focus()

	const width = 60
	rows := strings.Split(f.Panel(width, 4), "\n")
	if len(rows) != 4 {
		t.Fatalf("Panel returned %d rows, want 4", len(rows))
	}
	painted := 0
	for i, row := range rows {
		first, last, unbroken := fillRun(row, fill)
		if first < 0 {
			continue // a panel row outside the textarea's own block
		}
		painted++
		if !unbroken {
			t.Errorf("row %d's fill breaks inside [%d,%d] -- a reset or the cursor-line style cleared it:\n%q", i, first, last, row)
		}
	}
	if painted != 4 {
		t.Errorf("%d of 4 textarea rows carry the fill; a four-row input has to be a block, not a stripe:\n%s", painted, strings.Join(rows, "\n"))
	}
}

// TestPromptArea_UnfilledRendersExactlyAsBefore is the other half of §8.7's
// opt-in: SetFill is what turns the block on, so a PromptArea nobody calls
// it on must emit no background of its own. Without this, "opt-in" would be
// a claim about the call site rather than about the widget.
func TestPromptArea_UnfilledRendersExactlyAsBefore(t *testing.T) {
	area := widgets.NewPromptArea(theme.Default())
	area.SetValue("first line\nsecond line")
	area.Focus()

	for i, bg := range backgroundPerCell(area.View(60)) {
		if bg != "" {
			t.Fatalf("cell %d of an unfilled PromptArea is painted %q, want nothing:\n%q", i, bg, area.View(60))
		}
	}
}
