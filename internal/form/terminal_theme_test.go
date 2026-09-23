package form

import (
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
// exist, so the word `invalid` is drawn on the focused row's fill. On this
// theme that pair is now the terminal's ANSI 9 on its ANSI 8, which is the
// whole of what the fix can promise -- whether the user's red reads on the
// user's bright black is their palette's business, and #276 is open on it.
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
	if !strings.HasPrefix(row, "\x1b[100m") {
		t.Errorf("the focused row does not open on ANSI 8's background, \\x1b[100m:\n%q", row)
	}
	if !strings.Contains(row, "\x1b[91m"+dirRowInvalid) {
		t.Errorf("%q is not drawn in ANSI 9, \\x1b[91m:\n%q", dirRowInvalid, row)
	}
}
