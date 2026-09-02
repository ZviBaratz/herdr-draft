package form

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

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
