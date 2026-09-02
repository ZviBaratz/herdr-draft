package form

import (
	"fmt"
	"image/color"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// picker_fill_test.go pins v3 spec §8.3, the one part of the panel-table
// work whose correctness is a claim about BYTES rather than about
// characters -- and which the spec itself refuses to take on argument:
// "that is a byte-level argument, not a proof. Render a real example and
// read the bytes."
//
// The hazard, in full at sizes.go:103-157 and widgets/paint.go: an outer
// Background(...).Render over content holding any inner styled span loses
// its fill after that span, because Style.Render's trailing reset is
// unconditional and clears the outer background with it. Two independent
// passes now paint the same physical line -- the picker's own
// widgets.PaintLine, asserting Surface across the selected row, and then
// composeRows' paintLine, asserting PanelBG across the whole frame line
// the row sits inside -- and the second one inserts its background after
// every reset the first one has already annotated. The claim is that the
// picker's Surface lands AFTER the frame's PanelBG at every one of those
// resets and therefore wins. Nothing in either function states that
// ordering; it falls out of ReplaceAll running over a string the earlier
// pass already edited. So it is pinned here rather than reasoned about.

// sgrSetBG matches the two SGR forms lipgloss and charm.land/x/ansi emit
// for a background: the truecolor "48;2;r;g;b" this project's palettes
// always resolve to, and a bare reset. Anything else in an escape
// sequence is a foreground, a bold, or a zone marker, none of which move
// the background.
var sgrSetBG = regexp.MustCompile(`\x1b\[([0-9;]*)m`)

// backgroundPerCell walks one rendered line and reports the active
// background color at each printable cell, as the "r;g;b" of the last
// truecolor background SGR still in force ("" for none, i.e. after a
// reset with nothing re-asserted). It is the only way to ask the question
// this file exists to ask: a rendered line LOOKS right in a diff whether
// or not its fill survives, because the fill is invisible in stripped
// text and a trailing pad of spaces on the terminal's own background is
// indistinguishable from one on Surface until you run it.
func backgroundPerCell(line string) []string {
	var out []string
	bg := ""
	rest := line
	for {
		loc := sgrSetBG.FindStringSubmatchIndex(rest)
		if loc == nil {
			break
		}
		for range ansi.StringWidth(rest[:loc[0]]) {
			out = append(out, bg)
		}
		bg = applySGR(bg, rest[loc[2]:loc[3]])
		rest = rest[loc[1]:]
	}
	for range ansi.StringWidth(rest) {
		out = append(out, bg)
	}
	return out
}

// applySGR folds one escape sequence's parameters into the running
// background: "48;2;r;g;b" sets it, a reset ("" or "0") clears it, and
// every other parameter run leaves it alone.
func applySGR(bg, params string) string {
	if params == "" || params == "0" {
		return ""
	}
	parts := strings.Split(params, ";")
	for i := 0; i+4 < len(parts); i++ {
		if parts[i] == "48" && parts[i+1] == "2" {
			return strings.Join(parts[i+2:i+5], ";")
		}
	}
	return bg
}

// rgbKey spells a palette color the way backgroundPerCell reports one.
func rgbKey(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("%d;%d;%d", r>>8, g>>8, b>>8)
}

// TestFrame_SelectedRowFillSurvivesTheFrameRepaint is v3 spec §8.3's
// required proof. The account panel is the fixture with the most styled
// spans on one row -- four toned cells and a toned badge, so five
// embedded resets the fill has to survive -- and its cursor row sits
// inside a frame line the form then repaints in PanelBG end to end.
//
// The assertion is deliberately about the WHOLE run rather than about its
// last cell: a fill that dropped out after the second column and came
// back for the padding would satisfy "the row ends in Surface" while
// looking exactly like the defect on screen.
//
// The palette is catppuccin-latte rather than this package's usual
// theme.Default(). In catppuccin, Surface and ActiveRowBG are the SAME
// #313244, so the focused STACK row -- painted ActiveRowBG, higher up the
// same frame -- is indistinguishable from the panel row this test is
// about, and the search below would answer with the wrong line. Latte
// separates all three, which is the only property this fixture needs of
// it.
func TestFrame_SelectedRowFillSurvivesTheFrameRepaint(t *testing.T) {
	palette, ok := theme.Builtin("catppuccin-latte")
	if !ok {
		t.Fatal("theme.Builtin(\"catppuccin-latte\") is not a known builtin")
	}
	surface, panelBG, activeRow := rgbKey(palette.Surface), rgbKey(palette.PanelBG), rgbKey(palette.ActiveRowBG)
	if surface == panelBG || surface == activeRow {
		t.Fatalf("Surface %s must differ from PanelBG %s and ActiveRowBG %s, or this fixture cannot tell the passes apart",
			surface, panelBG, activeRow)
	}

	frame := buildAccountPanelForm(palette).ViewAt(80, 24)
	line, ok := lineWithBackground(frame, surface)
	if !ok {
		t.Fatalf("no line in the frame carries the Surface fill at all -- the selected row is unpainted:\n%s", frame)
	}

	cells := backgroundPerCell(line)
	first, last := -1, -1
	for i, bg := range cells {
		if bg != surface {
			continue
		}
		if first < 0 {
			first = i
		}
		last = i
	}
	for i := first; i <= last; i++ {
		if cells[i] != surface {
			t.Fatalf("cell %d of the selected row is on background %q, want the Surface fill %q unbroken across [%d,%d]:\n%q",
				i, cells[i], surface, first, last, line)
		}
	}

	// The fill has to be the ROW, not a fragment of one. composeRows hands
	// each panel line gutterWidth+inner cells and panelPickerLines spends
	// the gutter on the ▸ before handing the rest to the picker, so the
	// picker's own row -- and therefore the fill -- is exactly `inner`
	// cells. Derived rather than written out, so a change to the frame's
	// horizontal arithmetic moves this with it.
	_, inner := contentBox(80)
	if got := last - first + 1; got != inner {
		t.Errorf("the Surface run is %d cells wide, want %d (the picker's whole row):\n%q",
			got, inner, line)
	}
}

// lineWithBackground returns the first line of frame on which bg is ever
// the active background.
func lineWithBackground(frame, bg string) (string, bool) {
	for _, line := range strings.Split(frame, "\n") {
		for _, cell := range backgroundPerCell(line) {
			if cell == bg {
				return line, true
			}
		}
	}
	return "", false
}
