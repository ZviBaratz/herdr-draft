package form

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// --- the drop-blank-lines stage, which used to be dead ---------------------

// TestFitToHeight_DropsInteriorBlankLines pins the degradation ladder's
// SECOND rung, which had never once fired in production: the stage tested
// `l == ""`, but every composed line goes through decorateFocus (which
// prefixes a two-cell gutter) and every reserved-but-empty row is padded
// out to the full inner width, so no line reaching it was ever the empty
// string. Every overlong render therefore skipped straight to dropping
// dividers and then clipping real content. submitview.go is the caller
// that still depends on it.
//
// The three shapes below are exactly the ones compose actually produces --
// a gutter-prefixed empty line, a width-padded blank row, and a blank row
// carrying a styled-but-empty span (a dim hint with no text) -- so a
// regression to a plain equality test fails here rather than silently
// going quiet again.
func TestFitToHeight_DropsInteriorBlankLines(t *testing.T) {
	styledBlank := decorateFocus(fitLine(dimHint(theme.Default()).Render(""), 20), false, theme.Default())
	lines := []string{
		"first",
		"content A",
		decorateFocus("", false, theme.Default()), // gutter only
		"content B",
		fitLine("", 20), // width-padded blank
		"content C",
		styledBlank,
		"last",
	}

	got := fitToHeight(lines, nil, 5, "\u2500\u2500", -1)

	if len(got) != 5 {
		t.Fatalf("fitToHeight produced %d lines, want 5:\n%q", len(got), got)
	}
	for _, want := range []string{"first", "content A", "content B", "content C", "last"} {
		if !containsLine(got, want) {
			t.Errorf("fitToHeight dropped %q, which is real content -- only blank lines should have gone:\n%q", want, got)
		}
	}
}

func containsLine(lines []string, want string) bool {
	for _, l := range lines {
		if ansi.Strip(l) == want {
			return true
		}
	}
	return false
}

// TestFitToHeight_NeverDropsProtectedLines pins the focus-protection the
// ladder gained alongside the allocator: a composed form too short even for
// every section's floor must lose SOMETHING, and the one thing it must
// never lose is the section the user is currently editing.
func TestFitToHeight_NeverDropsProtectedLines(t *testing.T) {
	lines := []string{"pad", "a", "b", "c", "FOCUSED", "d", "e", "Create"}
	protect := []bool{false, false, false, false, true, false, false, true}

	got := fitToHeight(lines, protect, 3, "---", -1)

	if len(got) != 3 {
		t.Fatalf("fitToHeight produced %d lines, want 3: %q", len(got), got)
	}
	if !containsLine(got, "FOCUSED") {
		t.Errorf("the focused section's line was dropped: %q", got)
	}
	if got[len(got)-1] != "Create" {
		t.Errorf("last line = %q, want the Create button preserved unconditionally", got[len(got)-1])
	}
}

// TestClipKeeping_WithoutProtectionIsTheOldTailClip pins that the clip
// stage's replacement is behavior-preserving where nothing is protected --
// the first budget-1 lines plus the last one, exactly what Atrium's own
// fitOverlay tail clip (and this package's previous clipTail) did.
// submitview.go is what still relies on it.
func TestClipKeeping_WithoutProtectionIsTheOldTailClip(t *testing.T) {
	lines := []string{"0", "1", "2", "3", "4", "5", "6"}

	got := clipKeeping(lines, nil, 4)

	want := []string{"0", "1", "2", "6"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("clipKeeping(nil protect) = %v, want %v", got, want)
	}
}

// TestModel_ComposeFitsExactlyTheWindow pins the composed render's own
// shape at a spread of window heights, including ones far below anything
// the form's rows and panel can collectively fit into: exactly h lines
// out, with the Create button always on the last one.
func TestModel_ComposeFitsExactlyTheWindow(t *testing.T) {
	palette := theme.Default()
	sections := make([]Section, 0, 10)
	for i := 0; i < 10; i++ {
		sections = append(sections, newStub(fmt.Sprintf("s-%d", i)).withPanel(6))
	}
	m := New(Setup{Palette: palette, Sections: sections})
	m.Init()

	for _, h := range []int{6, 10, 14, 20, 24, 40, 60} {
		got := m.ViewAt(80, h)
		if lines := strings.Count(got, "\n") + 1; lines != h {
			t.Errorf("ViewAt(80, %d) produced %d lines, want exactly %d", h, lines, h)
		}
		rows := strings.Split(got, "\n")
		if last := ansi.Strip(rows[len(rows)-1]); !strings.Contains(last, "↵ create") {
			t.Errorf("ViewAt(80, %d) last row = %q, want the Create button", h, last)
		}
	}
}
