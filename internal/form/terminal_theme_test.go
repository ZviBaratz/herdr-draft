package form

import (
	"regexp"
	"strings"
	"testing"

	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// TestTerminalTheme_TheScreenSendsOnlyTheTerminalsOwnColours is #304 at the
// level the user sees: the assembled screen, through every painter between
// a Palette field and the bytes. internal/theme pins what the palette holds.
// This pins that nothing on the way -- PaintLine's reasserted fills, the
// row stack's focused band, a lipgloss style -- turns an index back into an
// RGB value, which is the one way the theme could hold the right thing and
// the screen still draw xterm's red.
//
// The screen is #276's: the project row focused over a path that does not
// exist. Any fill on that row is a ground the terminal's palette was not
// designed for, and every fill this theme tried made `invalid` unreadable
// on most real schemes (see internal/theme's terminal entry). So the row
// is unfilled, and must stay so: the word is the terminal's ANSI 9 on the
// terminal's own background, and the row is marked by its blue gutter glyph
// and bold, the other two of v3 spec §5.4's signals.
//
// Any `38;2;` or `48;2;` is a truecolor sequence, and this theme has no RGB
// value left to send.
func TestTerminalTheme_TheScreenSendsOnlyTheTerminalsOwnColours(t *testing.T) {
	palette, ok := theme.Builtin("terminal")
	if !ok {
		t.Fatal("theme.Builtin(\"terminal\") is not a known builtin")
	}
	const path = "/home/zvi/Projects/gone"
	d := NewDirField(palette)
	d.SetHomeDir("/home/zvi")
	d.SetCandidates(1, []string{path})
	d.Focus()
	d.SetValidity(path, ValidityInvalid)

	screen := fieldFrame(palette, d).ViewAt(80, 24)

	for _, truecolor := range []string{"38;2;", "48;2;"} {
		if i := strings.Index(screen, truecolor); i >= 0 {
			t.Errorf("the terminal theme's screen sends a truecolor sequence at byte %d (%q) -- every colour on it must be the terminal's own (#304):\n%q",
				i, screen[max(i-8, 0):min(i+24, len(screen))], screen)
		}
	}

	var row string
	for _, line := range strings.Split(screen, "\n") {
		if strings.Contains(line, dirRowInvalid) {
			row = line
			break
		}
	}
	if row == "" {
		t.Fatalf("no line carries %q -- the fixture is not #276's screen:\n%s", dirRowInvalid, screen)
	}
	for _, sgr := range regexp.MustCompile(`\x1b\[([0-9;]*)m`).FindAllStringSubmatch(row, -1) {
		for _, param := range strings.Split(sgr[1], ";") {
			// 40-48 and 100-107 set a background; 49 resets it to the
			// terminal's own, which is the one this row may use.
			if len(param) == 2 && param[0] == '4' && param != "49" || len(param) == 3 && strings.HasPrefix(param, "10") {
				t.Errorf("the focused row paints a background (%q) -- on this theme it has none, so its words stay on the terminal's own (#276):\n%q", sgr[0], row)
			}
		}
	}
	if !strings.Contains(row, "\x1b[34m"+focusBarGlyph) {
		t.Errorf("the focused row has no ANSI 4 gutter glyph %q -- without a fill it is one of the two signals left:\n%q", focusBarGlyph, row)
	}
	if !strings.Contains(row, "\x1b[1m") {
		t.Errorf("the focused row's value is not bold -- without a fill that is the other signal left:\n%q", row)
	}
	if !strings.Contains(row, "\x1b[91m"+dirRowInvalid) {
		t.Errorf("%q is not drawn in ANSI 9, \\x1b[91m:\n%q", dirRowInvalid, row)
	}
}
