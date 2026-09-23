package theme

import (
	"image/color"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// hostColorsFor is the terminal palette's own indices answered with the
// values the author's alacritty reported on 2026-09-23 (host-colours spec
// §3.1). Every word on it clears its floor, which makes it the fixture for
// "nothing moves but the fills".
func measuredHost() HostColors {
	h := HostColors{Palette: map[uint8]Color{}}
	h.Background, _ = parseHexColor("#181818")
	h.Foreground, _ = parseHexColor("#d8d8d8")
	for idx, hex := range map[uint8]string{
		2: "#90a959", 3: "#f4bf75", 4: "#6a9fb5",
		7: "#d8d8d8", 8: "#6b6b6b", 9: "#c55555",
	} {
		h.Palette[idx], _ = parseHexColor(hex)
	}
	return h
}

func terminalPalette(t *testing.T) Palette {
	t.Helper()
	p, ok := Builtin("terminal")
	if !ok {
		t.Fatal(`Builtin("terminal") is not a known builtin`)
	}
	return p
}

func TestNeedsHostColors_OnlyTheTerminalPalette(t *testing.T) {
	for name := range builtinPalettes {
		p, ok := Builtin(name)
		if !ok {
			t.Fatalf("Builtin(%q) not found", name)
		}
		want := name == "terminal"
		if got := NeedsHostColors(p); got != want {
			t.Errorf("NeedsHostColors(%q) = %v, want %v -- the query is gated on this, so a true here puts OSC sequences on the wire for a palette that is already fully measured", name, got, want)
		}
	}
}

func TestQueryIndices_AreTheOnesThePaletteDraws(t *testing.T) {
	got := QueryIndices(terminalPalette(t))
	want := []uint8{2, 3, 4, 7, 8, 9}
	if len(got) != len(want) {
		t.Fatalf("QueryIndices = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("QueryIndices = %v, want %v (ascending, deduped)", got, want)
		}
	}
	// A measured palette asks for nothing at all.
	catppuccin, _ := Builtin("catppuccin")
	if idx := QueryIndices(catppuccin); len(idx) != 0 {
		t.Errorf("QueryIndices(catppuccin) = %v, want none", idx)
	}
}

// TestResolveHost_KeepsAnIndexThatClearsItsFloor is decision 2: the whole
// point of #304 is that the terminal draws its own colours, and a colour
// the measurement says is legible has no reason to stop following the
// user's terminal.
func TestResolveHost_KeepsAnIndexThatClearsItsFloor(t *testing.T) {
	p := terminalPalette(t)
	got := ResolveHost(p, measuredHost())

	for _, f := range []struct {
		name string
		c    Color
		want uint8
	}{
		{"Accent", got.Accent, 4},
		{"Success", got.Success, 2},
		{"Warning", got.Warning, 3},
		{"Danger", got.Danger, 9},
		{"DimText", got.DimText, 7},
		{"Branch", got.Branch, 7},
		{"Overlay0", got.Overlay0, 7},
		{"Border", got.Border, 8},
	} {
		idx, ok := f.c.(ansi.BasicColor)
		if !ok {
			t.Errorf("%s = %T %v, want ANSI %d -- it clears its floor on this host, so it must stay an index (#304)", f.name, f.c, f.c, f.want)
			continue
		}
		if uint8(idx) != f.want {
			t.Errorf("%s = ANSI %d, want ANSI %d", f.name, idx, f.want)
		}
	}
}

// TestResolveHost_NeverEmitsTheInheritedFields pins the half of step 3 that
// is unconditional. PanelBG and Text are "inherit the terminal's own", and
// emitting either as RGB would paint over the user's background -- possibly
// a transparent one -- on precisely the themes that needed no repair.
func TestResolveHost_NeverEmitsTheInheritedFields(t *testing.T) {
	got := ResolveHost(terminalPalette(t), measuredHost())
	if _, ok := got.PanelBG.(lipgloss.NoColor); !ok {
		t.Errorf("PanelBG = %T %v, want lipgloss.NoColor -- the host background is a MEASUREMENT, never something to send", got.PanelBG, got.PanelBG)
	}
	if _, ok := got.Text.(lipgloss.NoColor); !ok {
		t.Errorf("Text = %T %v, want lipgloss.NoColor", got.Text, got.Text)
	}
}

// TestResolveHost_TheFocusedRowGetsItsFillBack is decision 3, and the one
// visible outcome: #346 left this palette's focused row unfilled because
// there was no ground to raise a fill against. There is one now.
func TestResolveHost_TheFocusedRowGetsItsFillBack(t *testing.T) {
	before := terminalPalette(t)
	if _, _, _, ok := rgb8(before.ActiveRowBG); ok {
		t.Fatal("the unresolved terminal palette has a measurable ActiveRowBG -- this test's premise (#346) is gone")
	}
	got := ResolveHost(before, measuredHost())

	ratio, ok := contrastRatio(got.ActiveRowBG, mustHex(t, "#181818"))
	if !ok {
		t.Fatalf("ActiveRowBG = %T %v, want a measurable fill", got.ActiveRowBG, got.ActiveRowBG)
	}
	if ratio < ActiveRowContrastFloor {
		t.Errorf("ActiveRowBG is %.3f:1 against the host background, want >= %.2f:1", ratio, ActiveRowContrastFloor)
	}
	if _, _, _, ok := rgb8(got.SurfaceFill(mustHex(t, "#181818"))); !ok {
		t.Error("SurfaceFill is still unmeasurable -- the picker's cursor row and the cancel button's face stay invisible (#149)")
	}
}

// TestResolveHost_RaisesAWordItCannotRead is the other side of decision 2.
// The fixture is a light host, where ANSI 7 -- the label tier, and the
// branch colour -- is a pale grey on a pale background.
func TestResolveHost_RaisesAWordItCannotRead(t *testing.T) {
	h := HostColors{Palette: map[uint8]Color{}}
	h.Background, _ = parseHexColor("#ffffff")
	h.Foreground, _ = parseHexColor("#000000")
	for idx, hex := range map[uint8]string{
		2: "#90a959", 3: "#f4bf75", 4: "#6a9fb5",
		7: "#d8d8d8", 8: "#6b6b6b", 9: "#c55555",
	} {
		h.Palette[idx], _ = parseHexColor(hex)
	}

	got := ResolveHost(terminalPalette(t), h)
	if _, ok := got.DimText.(ansi.BasicColor); ok {
		t.Fatal("DimText is still ANSI 7 -- #d8d8d8 on #ffffff is 1.32:1 and unreadable, so the clamp must have replaced it")
	}
	grounds := []Color{h.Background, got.ActiveRowBG, got.SurfaceFill(h.Background)}
	if !clearsFloor(got.DimText, grounds, DimTextContrastFloor) {
		t.Errorf("DimText = %v, worst ratio %.3f:1, want >= %.1f:1", got.DimText, worstRatio(got.DimText, grounds), DimTextContrastFloor)
	}
}

// effective is the colour the terminal will really draw for c. A field that
// survived resolution as an ANSI index is drawn from the HOST's palette, and
// rgb8 refuses to measure an index precisely so that nobody measures the VGA
// stand-in by accident -- so a test that asserted a floor on the index
// itself would be asserting on nothing. This is the substitution the
// terminal makes, made here so the assertion is about the screen.
func effective(c Color, h HostColors) Color {
	if idx, ok := basicIndex(c); ok {
		if hc, found := h.Palette[idx]; found {
			return hc
		}
	}
	return c
}

// TestResolveHost_WithoutABackgroundChangesNothing is the required answer
// from host-colours spec §6: with no ground there is nothing to measure
// against, and a palette raised against a guess would be worse than one
// left alone.
func TestResolveHost_WithoutABackgroundChangesNothing(t *testing.T) {
	p := terminalPalette(t)
	h := measuredHost()
	h.Background = nil

	got := ResolveHost(p, h)
	assertSamePalette(t, got, p, "a HostColors with no background")

	// And the same for a palette that never needed it.
	catppuccin, _ := Builtin("catppuccin")
	assertSamePalette(t, ResolveHost(catppuccin, measuredHost()), catppuccin, "a fully measured palette")
}

// TestResolveHost_WithoutAForegroundStillFloorsTheWords is the degradation
// the code gets for free rather than by branching: ensureContrast and
// raiseDimText decline what they cannot measure, while raiseSemanticText
// walks toward black or white and needs only the grounds.
func TestResolveHost_WithoutAForegroundStillFloorsTheWords(t *testing.T) {
	h := HostColors{Palette: map[uint8]Color{}}
	h.Background, _ = parseHexColor("#ffffff")
	for idx, hex := range map[uint8]string{4: "#6a9fb5", 9: "#c55555", 7: "#d8d8d8", 2: "#90a959", 3: "#f4bf75", 8: "#6b6b6b"} {
		h.Palette[idx], _ = parseHexColor(hex)
	}

	got := ResolveHost(terminalPalette(t), h)

	// No foreground means no `toward`, so the band cannot be raised -- and
	// the seeded background must NOT be left behind as a flat fill.
	if _, ok := got.ActiveRowBG.(lipgloss.NoColor); !ok {
		t.Errorf("ActiveRowBG = %T %v, want lipgloss.NoColor -- with no foreground the clamp cannot raise it, so the row stays unfilled rather than being painted a copy of the background it sits on", got.ActiveRowBG, got.ActiveRowBG)
	}
	if _, ok := got.Surface.(lipgloss.NoColor); !ok {
		t.Errorf("Surface = %T %v, want lipgloss.NoColor, for ActiveRowBG's reason", got.Surface, got.Surface)
	}
	// The words are still floored, against the one ground there is.
	grounds := []Color{h.Background}
	if !clearsFloor(got.Warning, grounds, SemanticTextContrastFloor) {
		t.Errorf("Warning = %v, worst %.3f:1 on the background -- the semantic clamp needs no foreground and must still have run", got.Warning, worstRatio(got.Warning, grounds))
	}
}

// TestResolveHost_AMissingIndexStaysAnIndex: a host that answered some OSC
// 4 queries and not others degrades per field, which is exactly today's
// behaviour for the fields it could not answer.
func TestResolveHost_AMissingIndexStaysAnIndex(t *testing.T) {
	h := measuredHost()
	delete(h.Palette, 9) // Danger never answered

	got := ResolveHost(terminalPalette(t), h)
	if idx, ok := got.Danger.(ansi.BasicColor); !ok || uint8(idx) != 9 {
		t.Errorf("Danger = %T %v, want ANSI 9 -- an index nobody answered for is unmeasured, and unmeasured is unfloored", got.Danger, got.Danger)
	}
	if _, _, _, ok := rgb8(got.ActiveRowBG); !ok {
		t.Error("the band should still be there: it needs the background, not index 9")
	}
}

// TestResolveHost_RespectsAHexOverride guards the interaction with
// `[palette]`, which is the one way a terminal-palette field is measurable
// before the host answers. Such a field is not seeded (there is nothing to
// seed) but IS floored, which is new: until the background was known its
// grounds were unmeasurable and every floor skipped it.
func TestResolveHost_RespectsAHexOverride(t *testing.T) {
	base := terminalPalette(t)
	// A red that cannot be read on a white host.
	overridden := Resolve(base, map[string]string{"danger": "#ff9999"})
	h := HostColors{Palette: map[uint8]Color{}}
	h.Background, _ = parseHexColor("#ffffff")
	h.Foreground, _ = parseHexColor("#000000")

	got := ResolveHost(overridden, h)
	grounds := []Color{h.Background, got.ActiveRowBG, got.SurfaceFill(h.Background)}
	if !clearsFloor(got.Danger, grounds, SemanticTextContrastFloor) {
		t.Errorf("an overridden Danger is %v at %.3f:1, want >= %.1f:1 -- a hex override on this palette used to be unfloored only because its grounds were unknowable", got.Danger, worstRatio(got.Danger, grounds), SemanticTextContrastFloor)
	}
}

// TestResolveHost_HoldsOnEveryRealScheme is the measurement host-colours
// spec §5.4 reports, as a test: over 43 real terminal schemes, the band
// clears its floor, every raised word clears its own, and no clamp gives
// up.
func TestResolveHost_HoldsOnEveryRealScheme(t *testing.T) {
	if len(hostSchemes) != 43 {
		t.Fatalf("hostSchemes has %d entries, want 43 -- §5.4's figures are quoted against this set", len(hostSchemes))
	}
	base := terminalPalette(t)
	untouched := 0

	for _, s := range hostSchemes {
		h := s.colors()
		got := ResolveHost(base, h)
		grounds := []Color{h.Background, got.ActiveRowBG, got.SurfaceFill(h.Background)}

		ratio, ok := contrastRatio(got.ActiveRowBG, h.Background)
		if !ok {
			t.Errorf("%s: no focused-row fill at all", s.name)
			continue
		}
		if ratio < ActiveRowContrastFloor {
			t.Errorf("%s: band %.3f:1, want >= %.2f:1", s.name, ratio, ActiveRowContrastFloor)
		}

		for name, c := range map[string]Color{
			"Danger": got.Danger, "Warning": got.Warning, "Success": got.Success,
			"Accent": got.Accent, "Branch": got.Branch,
		} {
			drawn := effective(c, h)
			if !clearsFloor(drawn, grounds, SemanticTextContrastFloor) {
				t.Errorf("%s: %s draws as %v, worst %.3f:1, want >= %.1f:1", s.name, name, drawn, worstRatio(drawn, grounds), SemanticTextContrastFloor)
			}
		}
		if dim := effective(got.DimText, h); !clearsFloor(dim, grounds, DimTextContrastFloor) {
			t.Errorf("%s: DimText draws as %v, worst %.3f:1, want >= %.1f:1", s.name, dim, worstRatio(dim, grounds), DimTextContrastFloor)
		}

		allIndices := true
		for _, c := range []Color{got.Danger, got.Warning, got.Success, got.Accent, got.Branch, got.DimText} {
			if _, ok := c.(ansi.BasicColor); !ok {
				allIndices = false
			}
		}
		if allIndices {
			untouched++
		}
	}

	// §5.4's headline: most schemes need a raise somewhere, and a
	// meaningful minority need none at all. Asserted as a range rather
	// than as 13 exactly, so that a clamp improving by one scheme is not
	// a test failure -- what must not happen is this silently becoming
	// "every scheme keeps everything" (the clamp stopped running) or
	// "no scheme keeps anything" (#304 was thrown away).
	if untouched < 5 || untouched > 25 {
		t.Errorf("%d of 43 schemes keep every index; §5.4 measured 13. Outside 5..25 means the clamp or the restore has changed behaviour, not improved", untouched)
	}
}

func TestParsePaletteReply(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      string
		wantIdx uint8
		wantHex string
	}{
		{"herdr's own shape, ST-terminated", "\x1b]4;9;rgb:c5c5/5555/5555\x1b\\", 9, "#c55555"},
		{"BEL-terminated, which other terminals send", "\x1b]4;2;rgb:9090/a9a9/5959\x07", 2, "#90a959"},
		{"two-digit channels", "\x1b]4;4;rgb:6a/9f/b5\x07", 4, "#6a9fb5"},
		{"index 0", "\x1b]4;0;rgb:1818/1818/1818\x1b\\", 0, "#181818"},
		{"index 255", "\x1b]4;255;rgb:0000/0000/0000\x1b\\", 255, "#000000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			idx, c, ok := ParsePaletteReply(tc.in)
			if !ok {
				t.Fatalf("ParsePaletteReply(%q) declined", tc.in)
			}
			if idx != tc.wantIdx {
				t.Errorf("index = %d, want %d", idx, tc.wantIdx)
			}
			r, g, b, mok := rgb8(c)
			if !mok {
				t.Fatalf("colour %T %v is unmeasurable -- every floor in this package would skip it", c, c)
			}
			want := mustHex(t, tc.wantHex)
			wr, wg, wb, _ := rgb8(want)
			if r != wr || g != wg || b != wb {
				t.Errorf("colour = #%02x%02x%02x, want %s", r, g, b, tc.wantHex)
			}
		})
	}

	// Everything that is not an OSC 4 reply must report false rather than
	// a guess: this runs over every unrecognised OSC the program sees.
	for _, tc := range []struct{ name, in string }{
		{"empty", ""},
		{"a different OSC", "\x1b]11;rgb:1818/1818/1818\x1b\\"},
		{"a title", "\x1b]0;some window title\x07"},
		{"no terminator", "\x1b]4;9;rgb:c5c5/5555/5555"},
		{"no index separator", "\x1b]4;rgb:c5c5/5555/5555\x07"},
		{"a non-numeric index", "\x1b]4;x;rgb:c5c5/5555/5555\x07"},
		{"an out-of-range index", "\x1b]4;256;rgb:c5c5/5555/5555\x07"},
		{"a colour XParseColor declines", "\x1b]4;9;chartreuse\x07"},
		{"a set rather than a report", "\x1b]4;9;?\x07"},
		{"truncated after the prefix", "\x1b]4;"},
	} {
		t.Run("declines "+tc.name, func(t *testing.T) {
			if idx, c, ok := ParsePaletteReply(tc.in); ok {
				t.Errorf("ParsePaletteReply(%q) = %d, %v, true -- want a refusal, not a guess", tc.in, idx, c)
			}
		})
	}
}

// TestParsePaletteReply_NormalisesToReplicatedBytes is the detail that
// would otherwise be invisible: rgb8 requires an opaque value whose bytes
// are replicated, and a colour that fails that check is silently skipped by
// every floor in this package rather than reported.
func TestParsePaletteReply_NormalisesToReplicatedBytes(t *testing.T) {
	_, c, ok := ParsePaletteReply("\x1b]4;9;rgb:c5c5/5555/5555\x1b\\")
	if !ok {
		t.Fatal("declined a well-formed reply")
	}
	if _, isRGBA := c.(color.RGBA); !isRGBA {
		t.Errorf("%T, want color.RGBA -- an ansi.XParseColor result is not guaranteed to replicate its bytes, and rgb8 checks", c)
	}
	r, g, b, a := c.RGBA()
	for _, ch := range []struct {
		name string
		v    uint32
		want uint32
	}{{"r", r, 0xc5c5}, {"g", g, 0x5555}, {"b", b, 0x5555}, {"a", a, 0xffff}} {
		if ch.v != ch.want {
			t.Errorf("%s = %#04x, want %#04x", ch.name, ch.v, ch.want)
		}
	}
}

func mustHex(t *testing.T, s string) Color {
	t.Helper()
	c, ok := parseHexColor(s)
	if !ok {
		t.Fatalf("parseHexColor(%q) failed", s)
	}
	return c
}

func assertSamePalette(t *testing.T, got, want Palette, what string) {
	t.Helper()
	g, w := fields(&got), fields(&want)
	for i := range g {
		if !sameColor(*g[i], *w[i]) {
			t.Errorf("%s changed field %d: got %T %v, want %T %v", what, i, *g[i], *g[i], *w[i], *w[i])
		}
	}
}

// TestFloorContrast_DoesNotMoveTheInheritedFields pins the premise behind
// ResolveHost's unconditional `out.PanelBG = p.PanelBG` and `out.Text =
// p.Text`.
//
// Those two lines are belt and braces today: the restore loop above them
// already covers both, because floorContrast never writes either field, so
// they compare equal to the seed and go back on their own. A mutation test
// proved exactly that -- deleting them changes no result. They stay because
// a future floor that DID write one would start emitting the user's own
// background as truecolor, and nothing else would notice.
//
// This is the test that would notice. It asserts the premise rather than
// the redundancy: if floorContrast ever learns to move PanelBG or Text,
// this goes red and points at ResolveHost, where the two lines stop being
// redundant and start being the thing that saves it.
func TestFloorContrast_DoesNotMoveTheInheritedFields(t *testing.T) {
	for name := range builtinPalettes {
		raw := builtinPalettes[name]
		floored := floorContrast(raw)
		if !sameColor(floored.PanelBG, raw.PanelBG) {
			t.Errorf("%s: floorContrast moved PanelBG %v -> %v. ResolveHost restores it unconditionally and that line is now load-bearing rather than defensive -- read its comment before changing either.", name, raw.PanelBG, floored.PanelBG)
		}
		if !sameColor(floored.Text, raw.Text) {
			t.Errorf("%s: floorContrast moved Text %v -> %v, same warning", name, raw.Text, floored.Text)
		}
	}
}

// TestResolveHost_SurfaceIsThePanelFill pins WHICH ground Surface is derived
// from, which is the one decision in ResolveHost that has no other guard.
//
// Both candidates are plausible and they are not equally good: the panel
// fill is the value floorContrast measures words against (#149's rule), and
// the band fill would draw a visible input chip at the cost of a brighter
// cursor row than any theme ships -- measured, that pushes
// solarized-light's dim tier under its floor with nowhere left to walk.
// The choice is invisible in every other assertion here, so it gets its own.
func TestResolveHost_SurfaceIsThePanelFill(t *testing.T) {
	h := HostColors{Palette: map[uint8]Color{}}
	h.Background, _ = parseHexColor("#181818")
	h.Foreground, _ = parseHexColor("#d8d8d8")
	for idx, hex := range map[uint8]string{2: "#90a959", 3: "#f4bf75", 4: "#6a9fb5", 7: "#d8d8d8", 8: "#6b6b6b", 9: "#c55555"} {
		h.Palette[idx], _ = parseHexColor(hex)
	}
	got := ResolveHost(terminalPalette(t), h)

	// What the two candidate grounds produce, computed here rather than
	// assumed, so this test states the difference instead of a constant.
	seeded := terminalPalette(t)
	seeded.PanelBG, seeded.Text, seeded.Surface = h.Background, h.Foreground, h.Background
	fromPanel := seeded.SurfaceFill(h.Background)
	fromBand := ensureContrast(got.ActiveRowBG, got.ActiveRowBG, h.Foreground, InputFillContrastFloor)
	if sameColor(fromPanel, fromBand) {
		t.Skip("this host cannot tell the two grounds apart, so there is nothing to pin")
	}
	if !sameColor(got.Surface, fromPanel) {
		t.Errorf("Surface = %v, want the PANEL fill %v (got the band fill %v?). floorContrast measures words against SurfaceFill(PanelBG); a Surface derived from anything else would floor them against a fill nothing paints.", got.Surface, fromPanel, fromBand)
	}
}
