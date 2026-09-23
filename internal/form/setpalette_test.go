package form

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// sentinelPalette builds a palette whose twelve fields are twelve distinct,
// recognisable colours, offset by `base` so two calls produce two palettes
// that share no value at all.
//
// The colours are deliberately ugly and deliberately unique: the whole test
// below is "does any byte of the OLD palette survive a repaint", and that
// question is only answerable if every old value is findable in the rendered
// bytes and no new value can be mistaken for one.
func sentinelPalette(base int) theme.Palette {
	c := func(i int) theme.Color {
		v, ok := parseTestHex(fmt.Sprintf("#%02x%02x%02x", base+i, base+i, base+i))
		if !ok {
			panic("bad sentinel")
		}
		return v
	}
	return theme.Palette{
		Accent: c(1), PanelBG: c(2), Text: c(3), DimText: c(4),
		Overlay0: c(5), Danger: c(6), Success: c(7), Border: c(8),
		Surface: c(9), ActiveRowBG: c(10), Warning: c(11), Branch: c(12),
	}
}

// parseTestHex is theme.parseHexColor's job from outside the package. It
// goes through lipgloss the way theme's own hex() does, so the values a
// style emits are byte-identical to the ones this test greps for.
func parseTestHex(s string) (theme.Color, bool) {
	p := theme.Resolve(theme.Palette{}, map[string]string{"accent": s})
	if p.Accent == nil {
		return nil, false
	}
	return p.Accent, true
}

// sentinelSGR is the truecolor sequence a sentinel channel value renders as,
// as a substring: every sentinel is a grey, so all three channels are equal.
func sentinelSGR(base, i int) string {
	v := base + i
	return fmt.Sprintf(";%d;%d;%dm", v, v, v)
}

// TestSetPalette_ReachesEverySection is host-colours spec §9.2's last test,
// and the one that fails if somebody adds a Section, or a palette-derived
// value inside one, without a SetPalette to match.
//
// It is a byte test rather than a field test on purpose. A SetPalette that
// stored the new palette and forgot to re-bake a widget's styles -- which is
// exactly what lineInput and PromptArea require and what nothing else does
// -- would pass any assertion made against the struct and fail this one,
// because the old colours would still be on screen.
func TestSetPalette_ReachesEverySection(t *testing.T) {
	const oldBase, newBase = 0x10, 0x90

	for _, tc := range []struct {
		name string
		make func(theme.Palette) Section
	}{
		{"title", func(p theme.Palette) Section { return NewTitleField(p) }},
		{"prompt", func(p theme.Palette) Section { return NewPromptField(p) }},
		{"dir", func(p theme.Palette) Section {
			d := NewDirField(p)
			d.SetHomeDir("/home/zvi")
			d.SetCandidates(1, []string{"/home/zvi/Projects/herdr"})
			return d
		}},
		{"worktree", func(p theme.Palette) Section { return NewWorktreeField(p) }},
		{"placement", func(p theme.Palette) Section { return NewPlacementField(p) }},
		{"agent", func(p theme.Palette) Section { return NewAgentField(p) }},
		{"options", func(p theme.Palette) Section {
			f := NewOptionsField(p)
			// A kind, with a FREE-TEXT option, and it is load-bearing.
			// OptionsField builds a ChipRow per option and a lineInput
			// for a free-text one, lazily, in SetKind -- and with no kind
			// set there are no lines, so a SetPalette that reaches them
			// and one that does not render the same bytes. That is how
			// the incomplete version of this shipped past this very test
			// (B1 of the #348 review).
			f.SetKind("claude", KindOptions{Specs: []OptionSpec{{
				Name:     "model",
				Label:    "model",
				Flag:     "--model",
				Choices:  []OptionChoice{{Value: "opus", Label: "opus"}},
				FreeText: true,
			}}})
			return f
		}},
		{"account", func(p theme.Palette) Section { return NewAccountField(p) }},
		{"issue", func(p theme.Palette) Section { return NewIssueField(p) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := sentinelPalette(oldBase)
			s := tc.make(old)
			m := fieldFrame(old, s)

			before := m.ViewAt(80, 24)
			if !strings.Contains(before, sentinelSGR(oldBase, 0)) && !anySentinel(before, oldBase) {
				t.Skipf("this section renders none of the palette at 80x24, so there is nothing for a repaint to miss")
			}

			m.SetPalette(sentinelPalette(newBase))
			after := m.ViewAt(80, 24)

			for i := 1; i <= 12; i++ {
				if sgr := sentinelSGR(oldBase, i); strings.Contains(after, sgr) {
					t.Errorf("palette field %d survived the repaint: %q is still on screen.\nSetPalette either does not reach this section, or reaches it and leaves a baked style behind (see lineInput.SetPalette and widgets.PromptArea.SetPalette).\n%q", i, sgr, after)
				}
			}
			if !anySentinel(after, newBase) {
				t.Errorf("none of the NEW palette reached the screen, so the assertion above proves nothing -- the section may simply have stopped rendering.\n%q", after)
			}
		})
	}
}

// TestSetPalette_ReachesTheFormsOwnStyles guards the half a per-section test
// cannot see. Model.palette paints the rules, the header and the footer, and
// it is a plain field on a value receiver -- the exact shape that gets
// written to a copy and silently dropped. Every Section would repaint
// correctly while the frame around them did not.
func TestSetPalette_ReachesTheFormsOwnStyles(t *testing.T) {
	const oldBase, newBase = 0x10, 0x90
	old := sentinelPalette(oldBase)
	m := fieldFrame(old, NewPlacementField(old))

	m.SetPalette(sentinelPalette(newBase))
	after := m.ViewAt(80, 24)

	// Overlay0 (field 5) draws the rules, DimText (4) the footer's ladder.
	for _, i := range []int{4, 5} {
		if sgr := sentinelSGR(oldBase, i); strings.Contains(after, sgr) {
			t.Errorf("the form's own palette field %d survived: %q is still on screen -- Model.SetPalette needs a POINTER receiver, and the rules and footer are what prove it.\n%q", i, sgr, after)
		}
	}
}

func anySentinel(screen string, base int) bool {
	for i := 1; i <= 12; i++ {
		if strings.Contains(screen, sentinelSGR(base, i)) {
			return true
		}
	}
	return false
}

// TestTerminalThemeFrames pins the terminal theme's screen byte for byte,
// in both of its states: as it opens, before the host has answered, and
// after (#347).
//
// Two frames, one difference, which is the shape that makes a frame
// readable. The first is the fallback host-colours spec §6 keeps -- every
// colour an ANSI index or nothing at all, no fill on the focused row
// (#304, #346) -- and it is the screen a non-herdr terminal, an older
// herdr and a headless `create` all go on seeing. The second is what the
// author's own alacritty resolves to: the same words, still as indices,
// plus the band.
//
// Diffing the two is the cheapest description of what this feature does.
func TestTerminalThemeFrames(t *testing.T) {
	base, ok := theme.Builtin("terminal")
	if !ok {
		t.Fatal(`theme.Builtin("terminal") is not a known builtin`)
	}

	t.Run("fallback", func(t *testing.T) {
		assertFrame(t, "terminal-theme-fallback", terminalFrameForm(base), 80, 24)
	})

	t.Run("resolved", func(t *testing.T) {
		assertFrame(t, "terminal-theme-resolved", terminalFrameForm(theme.ResolveHost(base, measuredTerminalHost(t))), 80, 24)
	})
}

// terminalFrameForm is one focused field plus the Create section, which is
// enough to carry every tier the palette has: a focused row (and so the
// band), a dim label, a branch ref, a semantic marker and the two buttons.
func terminalFrameForm(p theme.Palette) Model {
	w := NewWorktreeField(p)
	w.SetGitTarget(true)
	w.SetBase("origin/main")
	w.SetBranch("zvi/host-colours", true)
	return fieldFrame(p, w)
}

// measuredTerminalHost is what the author's alacritty answered in a real
// herdr pane on 2026-09-23 (host-colours spec §3.1). Kept here as hex
// rather than as the raw OSC bytes because this file is about the screen,
// not about the parse -- internal/app's own tests drive the bytes.
func measuredTerminalHost(t *testing.T) theme.HostColors {
	t.Helper()
	h := theme.HostColors{Palette: map[uint8]theme.Color{}}
	h.Background = mustTestHex(t, "#181818")
	h.Foreground = mustTestHex(t, "#d8d8d8")
	for idx, hex := range map[uint8]string{
		2: "#90a959", 3: "#f4bf75", 4: "#6a9fb5",
		7: "#d8d8d8", 8: "#6b6b6b", 9: "#c55555",
	} {
		h.Palette[idx] = mustTestHex(t, hex)
	}
	return h
}

func mustTestHex(t *testing.T, s string) theme.Color {
	t.Helper()
	c, ok := parseTestHex(s)
	if !ok {
		t.Fatalf("parseTestHex(%q) failed", s)
	}
	return c
}
