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
// out, with the Create button always on the footer's line.
//
// That line is the LAST one only up to h = 24 here. From h = 40 v3 spec
// §7's bottom margin sits under it, so the footer's y is derived from the
// frame -- the promise being pinned is that the footer survives every
// height, not that the frame stops there.
func TestModel_ComposeFitsExactlyTheWindow(t *testing.T) {
	palette := theme.Default()
	const n = 10
	sections := make([]Section, 0, n)
	for i := 0; i < n; i++ {
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
		ftr := footerRow(layoutFrame(h, n), h)
		if line := ansi.Strip(rows[ftr]); !strings.Contains(line, "↵ create") {
			t.Errorf("ViewAt(80, %d) row %d = %q, want the Create button", h, ftr, line)
		}
	}
}
