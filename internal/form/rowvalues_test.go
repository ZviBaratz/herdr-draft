package form

import (
	"math"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// TestFilterCount tables v3 spec §8.5's readout. It is worth a table
// because the interesting answers are the boundaries -- an empty list, a
// list of one, and a filter that keeps everything -- and each of them is
// a different branch.
func TestFilterCount(t *testing.T) {
	cases := []struct {
		name         string
		shown, total int
		want         string
	}{
		// An empty list says nothing: the message half of the same line
		// already says it, in the field's own words.
		{"nothing on offer", 0, 0, ""},
		{"a negative total is still nothing", 0, -3, ""},

		{"nothing filtered out", 24, 24, "24 issues"},
		{"filtered", 3, 24, "3/24 issues"},
		{"filtered to nothing keeps the denominator", 0, 24, "0/24 issues"},

		// Singular, which one noun cannot do: a user with exactly one
		// assigned issue is an ordinary state, not a corner.
		{"one on offer", 1, 1, "1 issue"},
		// The noun agrees with the TOTAL in the ratio form, which is why
		// the plain branch cannot borrow it.
		{"one match of many", 1, 24, "1/24 issues"},

		// Unreachable today -- every caller discounts the rows its picker
		// holds that are not issues/directories before calling -- and
		// pinned so that a caller which stops doing so degrades to a
		// plain count rather than rendering `25/24 issues`.
		{"more shown than held states the plain count", 25, 24, "25 issues"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := filterCount(c.shown, c.total, "issue", "issues"); got != c.want {
				t.Errorf("filterCount(%d, %d) = %q, want %q", c.shown, c.total, got, c.want)
			}
		})
	}
}

// TestPanelStatusLine_NoCountIsPanelText pins the equivalence
// panelStatusLine's own doc comment promises: a field that shows no count
// gets the byte-identical line it got before §8.5 existed. Byte-identical
// and not merely equivalent-looking, because fitLine is a lipgloss
// Render and the message arrives already styled.
func TestPanelStatusLine_NoCountIsPanelText(t *testing.T) {
	p := theme.Default()
	msg := dimHint(p).Render("no matching issues")
	for _, w := range []int{1, 3, 20, 44, 80, 101, 150} {
		if got, want := panelStatusLine(msg, "", w, p), panelText(msg, w); got != want {
			t.Errorf("panelStatusLine(msg, \"\", %d) = %q, want panelText's own %q", w, got, want)
		}
	}
}

// TestPanelStatusLine_CountIsFlushRight pins where the count lands and
// that the line is still exactly one panel line wide.
func TestPanelStatusLine_CountIsFlushRight(t *testing.T) {
	p := theme.Default()
	const w = 80
	line := ansi.Strip(panelStatusLine(dimHint(p).Render("no matching issues"), "0/24 issues", w, p))

	if got := ansi.StringWidth(line); got != w {
		t.Fatalf("line width = %d, want exactly %d: %q", got, w, line)
	}
	if !strings.HasPrefix(line, strings.Repeat(" ", gutterWidth)+"no matching issues") {
		t.Errorf("line = %q, want the message in the content column after a %d-cell gutter", line, gutterWidth)
	}
	if !strings.HasSuffix(line, "0/24 issues") {
		t.Errorf("line = %q, want the count flush with the right edge", line)
	}
}

// TestPanelStatusLine_CountIsDroppedRatherThanSqueezed is the 44-cell
// account panel: the legend it carries is 38 cells of prose, and
// spreadLine truncates its left half FLUSH against the right one, so a
// squeezed count reads `use whatever profile i3 profiles`. §8.1 drops a
// badge outright for the same reason; this drops the count.
func TestPanelStatusLine_CountIsDroppedRatherThanSqueezed(t *testing.T) {
	p := theme.Default()
	const w, count = 44, "3 profiles"
	msg := dimHint(p).Render(accountActiveLegend)

	line := panelStatusLine(msg, count, w, p)
	if strings.Contains(ansi.Strip(line), count) {
		t.Errorf("line = %q, want the count dropped -- it does not fit beside a %d-cell message",
			ansi.Strip(line), ansi.StringWidth(accountActiveLegend))
	}
	if want := panelText(msg, w); line != want {
		t.Errorf("line = %q, want the countless line panelText composes, %q", line, want)
	}

	// One cell wider than the tightest fit and it comes back, so the
	// threshold is a real boundary rather than "never at 44".
	fits := ansi.StringWidth(accountActiveLegend) + statusCountGap + len(count) + gutterWidth
	if got := ansi.Strip(panelStatusLine(msg, count, fits, p)); !strings.HasSuffix(got, count) {
		t.Errorf("at w=%d the count is the last thing that fits, but the line is %q", fits, got)
	}
}

// TestGaugeBar tables v3 spec §8.6's gauge. The interesting answers are
// the rounding boundary and the inputs clauth's unvalidated feed can
// actually produce -- a percentage past 100, or none at all.
func TestGaugeBar(t *testing.T) {
	cases := []struct {
		name     string
		fraction float64
		width    int
		want     string
	}{
		{"empty", 0, 10, "░░░░░░░░░░"},
		{"full", 1, 10, "██████████"},
		{"half", 0.5, 10, "█████░░░░░"},

		// The three the fixtures draw side by side.
		{"0%", 0.00, 10, "░░░░░░░░░░"},
		{"12%", 0.12, 10, "█░░░░░░░░░"},
		{"100%", 1.00, 10, "██████████"},

		// Rounded, not truncated: 98% is one block short of full under
		// truncation, and a bar that will not fill until exactly 100 says
		// least where it matters most -- past the warning threshold.
		{"98% rounds up to full", 0.98, 10, "██████████"},
		{"94% does not", 0.94, 10, "█████████░"},
		{"5% rounds up to one block", 0.05, 10, "█░░░░░░░░░"},
		{"4% rounds down to none", 0.04, 10, "░░░░░░░░░░"},

		// Clamped rather than trusted: clauth's own feed is unvalidated.
		{"over 100%", 1.7, 10, "██████████"},
		{"negative", -0.3, 10, "░░░░░░░░░░"},

		{"one cell", 0.6, 1, "█"},
		{"zero width renders nothing", 0.5, 0, ""},
		{"negative width renders nothing", 0.5, -4, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := gaugeBar(c.fraction, c.width); got != c.want {
				t.Errorf("gaugeBar(%v, %d) = %q, want %q", c.fraction, c.width, got, c.want)
			}
		})
	}

	// NaN is its own case because it compares false against every bound,
	// so a naive clamp lets it through and int(math.Round(NaN)) is
	// implementation-defined.
	if got, want := gaugeBar(math.NaN(), 10), "░░░░░░░░░░"; got != want {
		t.Errorf("gaugeBar(NaN, 10) = %q, want %q", got, want)
	}

	// Every gauge in a table has to be the same number of cells or the
	// column beside it does not line up.
	for pct := 0; pct <= 100; pct++ {
		if got := ansi.StringWidth(gaugeBar(float64(pct)/100, gaugeWidth)); got != gaugeWidth {
			t.Fatalf("gaugeBar at %d%% is %d cells wide, want %d", pct, got, gaugeWidth)
		}
	}
}
