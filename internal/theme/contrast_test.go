package theme

import (
	"image/color"
	"math"
	"testing"

	"charm.land/lipgloss/v2"
)

// overlay0ContrastFloor is v3 spec §5.3's floor for the rule stroke. Unlike
// ActiveRowContrastFloor there is no clamp behind it: every builtin's
// overlay0 clears it as translated (worst case nord, 1.69:1), so this is a
// pure assertion on the table. Its job is to stop a theme added later from
// reintroducing the invisible-rule defect.
const overlay0ContrastFloor = 1.6

// contrastAssertionEpsilon absorbs float noise so a value sitting exactly on
// a floor is not failed by a last-bit rounding difference.
const contrastAssertionEpsilon = 1e-9

// TestContrastRatio_KnownValues keeps the floor tests below from passing
// vacuously: a broken luminance formula would satisfy any floor while
// measuring nothing. The references are WCAG's own extremes plus #767676,
// the darkest grey that clears 4.5:1 against white.
func TestContrastRatio_KnownValues(t *testing.T) {
	cases := []struct {
		a, b string
		want float64
	}{
		{"#ffffff", "#000000", 21.0},
		{"#000000", "#ffffff", 21.0}, // symmetric
		{"#ffffff", "#ffffff", 1.0},
		{"#767676", "#ffffff", 4.5422},
		{"#6c7086", "#181825", 3.5941}, // catppuccin Overlay0 vs PanelBG
	}
	for _, tc := range cases {
		got, ok := contrastRatio(lipgloss.Color(tc.a), lipgloss.Color(tc.b))
		if !ok {
			t.Errorf("contrastRatio(%s, %s) reported unmeasurable", tc.a, tc.b)
			continue
		}
		if math.Abs(got-tc.want) > 5e-4 {
			t.Errorf("contrastRatio(%s, %s) = %.4f, want %.4f", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestContrastRatio_UnmeasurableColorIsReported(t *testing.T) {
	if _, ok := contrastRatio(lipgloss.NoColor{}, lipgloss.Color("#ffffff")); ok {
		t.Error("contrastRatio(NoColor, white) reported a ratio; NoColor means \"inherit\", there is nothing to measure")
	}
	if _, ok := contrastRatio(lipgloss.Color("#ffffff"), nil); ok {
		t.Error("contrastRatio(white, nil) reported a ratio, want unmeasurable")
	}
}

// TestEnsureContrast_LeavesLegibleValuesAlone is the half of ensureContrast's
// contract that matters for fidelity: a theme's or a user's own color must
// survive untouched wherever it is already legible. Thirteen of the
// seventeen RGB builtins are in this case.
func TestEnsureContrast_LeavesLegibleValuesAlone(t *testing.T) {
	bg := lipgloss.Color("#181825") // catppuccin PanelBG
	text := lipgloss.Color("#cdd6f4")
	fg := lipgloss.Color("#313244") // its selection_bg, 1.40:1

	got := ensureContrast(bg, fg, text, ActiveRowContrastFloor)

	if !colorEqual(got, fg) {
		t.Errorf("ensureContrast raised an already-legible value: got %v, want %v unchanged", got, fg)
	}
}

func TestEnsureContrast_RaisesIllegibleValuesToTheFloor(t *testing.T) {
	bg := lipgloss.Color("#1a1a1a") // vesper PanelBG
	text := lipgloss.Color("#ffffff")
	fg := lipgloss.Color("#232323") // its selection_bg, 1.11:1

	got := ensureContrast(bg, fg, text, ActiveRowContrastFloor)

	if colorEqual(got, fg) {
		t.Fatalf("ensureContrast left an illegible value alone: %v", got)
	}
	ratio, ok := contrastRatio(got, bg)
	if !ok {
		t.Fatalf("ensureContrast returned an unmeasurable color: %v", got)
	}
	if ratio < ActiveRowContrastFloor-contrastAssertionEpsilon {
		t.Errorf("raised value is %.3f:1 against the background, want >= %.2f:1", ratio, ActiveRowContrastFloor)
	}
	// It must stop at the floor rather than march off to Text: a focused-row
	// fill that reaches the text color is a solid bar, not a cursor.
	if ratio > 2 {
		t.Errorf("raised value is %.3f:1, overshooting -- ensureContrast should stop just past the floor", ratio)
	}
}

// TestEnsureContrast_UnmeasurableInputsPassThrough pins the terminal
// palette's case. herdr gives it Color::Reset for panel_bg, so there is no
// known ground to raise anything against; inventing one would make the
// result a fiction.
func TestEnsureContrast_UnmeasurableInputsPassThrough(t *testing.T) {
	fg := lipgloss.Color("#7f7f7f")
	cases := []struct {
		name           string
		bg, fg, toward Color
		want           Color
	}{
		{"NoColor background", lipgloss.NoColor{}, fg, lipgloss.Color("#ffffff"), fg},
		{"NoColor toward", lipgloss.Color("#1a1a1a"), fg, lipgloss.NoColor{}, fg},
		{"NoColor value", lipgloss.Color("#1a1a1a"), lipgloss.NoColor{}, lipgloss.Color("#ffffff"), lipgloss.NoColor{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ensureContrast(tc.bg, tc.fg, tc.toward, ActiveRowContrastFloor)
			if !colorEqual(got, tc.want) {
				t.Errorf("ensureContrast = %v, want %v passed through untouched", got, tc.want)
			}
		})
	}
}

// TestBuiltinPalettes_MeetContrastFloors is the assertion v3 spec §5.3 calls
// the most valuable test in v3: every builtin's rule stroke and focused-row
// fill has to be perceptible against the panel it is drawn on.
//
// It measures what Builtin hands out, not the raw table, because ActiveRowBG
// is floored on the way out (floorContrast) -- so this covers the clamp being
// wired in as well as the values themselves. Overlay0 is asserted against
// the table as translated, with no clamp behind it.
//
// There is exactly one exemption, and it is missing data rather than a
// waiver -- see the terminal case below. Every other palette is measured.
func TestBuiltinPalettes_MeetContrastFloors(t *testing.T) {
	if len(builtinPalettes) != 18 {
		t.Fatalf("builtinPalettes has %d entries, want 18 -- a new theme must be measured too", len(builtinPalettes))
	}
	for name := range builtinPalettes {
		t.Run(name, func(t *testing.T) {
			palette, ok := Builtin(name)
			if !ok {
				t.Fatalf("Builtin(%q) not found", name)
			}

			// herdr's terminal palette sets panel_bg to Color::Reset, i.e.
			// "inherit whatever the host terminal's background is", so there
			// is no value here to measure a ratio against, and a floor
			// asserted against an invented background would be a fiction.
			if _, unknown := palette.PanelBG.(lipgloss.NoColor); unknown {
				if name != "terminal" {
					t.Fatalf("PanelBG is NoColor: only the terminal palette may be exempt from the contrast floors")
				}
				return
			}

			for _, tc := range []struct {
				field string
				value Color
				floor float64
			}{
				{"Overlay0", palette.Overlay0, overlay0ContrastFloor},
				{"ActiveRowBG", palette.ActiveRowBG, ActiveRowContrastFloor},
			} {
				got, ok := contrastRatio(tc.value, palette.PanelBG)
				if !ok {
					t.Errorf("%s vs PanelBG is unmeasurable (%v vs %v)", tc.field, tc.value, palette.PanelBG)
					continue
				}
				if got < tc.floor-contrastAssertionEpsilon {
					t.Errorf("%s vs PanelBG = %.3f:1, want >= %.2f:1 -- fix the value or the clamp, do not lower the floor (v3 spec §5.3)",
						tc.field, got, tc.floor)
				}
			}
		})
	}
}

// TestLoadHerdrPalette_FloorsAnIllegibleOverride is the case the clamp exists
// for that a table edit could not have covered: a user who picks a
// focused-row fill too close to their panel background still gets a visible
// cursor.
func TestLoadHerdrPalette_FloorsAnIllegibleOverride(t *testing.T) {
	base := Default()
	illegible := "#191927" // a hair off catppuccin's #181825 PanelBG

	got := floorContrast(Resolve(base, map[string]string{"active_row_bg": illegible}))

	if colorEqual(got.ActiveRowBG, lipgloss.Color(illegible)) {
		t.Fatalf("an illegible [palette] override survived unfloored: %v", got.ActiveRowBG)
	}
	ratio, ok := contrastRatio(got.ActiveRowBG, got.PanelBG)
	if !ok {
		t.Fatalf("floored ActiveRowBG is unmeasurable: %v", got.ActiveRowBG)
	}
	if ratio < ActiveRowContrastFloor-contrastAssertionEpsilon {
		t.Errorf("floored override is %.3f:1, want >= %.2f:1", ratio, ActiveRowContrastFloor)
	}
}

// TestBuiltinPalettes_InputFillIsVisibleOnBothGrounds is v3 spec §8.7's own
// version of the floor above, and it exists because §8.7 specified a value
// that fails it. §8.7 asks for a flat palette.Surface, justified by
// "catppuccin #1e1e2e/#313244 is clear" -- which is Surface against PanelBG.
// Three of the form's four inputs never render on PanelBG: they are drawn
// only while their field is focused, and a focused stack row is filled
// ActiveRowBG first. Against THAT ground, measured rather than assumed:
//
//	catppuccin       1.000:1  (Surface and ActiveRowBG are both #313244)
//	tokyo-night-day  1.002:1
//	catppuccin-latte 1.007:1
//	gruvbox-light    1.074:1
//	kanagawa-lotus   1.077:1
//	dracula          1.078:1
//
// Six of seventeen below 1.08:1, the default theme at dead level. That is
// v2's 1.07:1 rule again, in a different field, and it would have shipped
// green: the frames would have changed bytes and not one pixel of screen.
//
// Both grounds are asserted, because InputFill takes the ground as an
// argument precisely so the two call sites can differ.
func TestBuiltinPalettes_InputFillIsVisibleOnBothGrounds(t *testing.T) {
	for name := range builtinPalettes {
		t.Run(name, func(t *testing.T) {
			palette, ok := Builtin(name)
			if !ok {
				t.Fatalf("Builtin(%q) not found", name)
			}
			// The terminal palette's Surface is herdr's Color::Reset and
			// nothing paints it at all, so there is no fill to measure --
			// the same missing-data exemption the floors above take, and
			// it is asserted rather than assumed in the form package's
			// TestLineInput_TerminalThemeGetsNoFill.
			if _, inherit := palette.Surface.(lipgloss.NoColor); inherit {
				if name != "terminal" {
					t.Fatalf("Surface is NoColor: only the terminal palette may be exempt")
				}
				return
			}

			for _, tc := range []struct {
				where  string
				ground Color
			}{
				{"a focused stack row", palette.ActiveRowBG},
				{"the detail panel", palette.PanelBG},
			} {
				got, ok := contrastRatio(palette.InputFill(tc.ground), tc.ground)
				if !ok {
					t.Errorf("InputFill on %s is unmeasurable", tc.where)
					continue
				}
				if got < InputFillContrastFloor-contrastAssertionEpsilon {
					t.Errorf("an input on %s is %.3f:1 against it, want >= %.2f:1 -- an input nobody can see is not an input (v3 spec §8.7)",
						tc.where, got, InputFillContrastFloor)
				}
			}
		})
	}
}

// TestInputFill_KeepsSurfaceWhereItIsAlreadyLegible is the fidelity half of
// InputFill's contract, and the reason it is not simply "always mix": herdr
// fills its own inputs with surface0, so where surface0 is visible against
// the ground we use surface0 and the screen matches herdr's. rose-pine is
// the case -- its Surface is 1.397:1 against its own focused row.
func TestInputFill_KeepsSurfaceWhereItIsAlreadyLegible(t *testing.T) {
	palette, ok := Builtin("rose-pine")
	if !ok {
		t.Fatal("Builtin(\"rose-pine\") not found")
	}
	if ratio, _ := contrastRatio(palette.Surface, palette.ActiveRowBG); ratio < InputFillContrastFloor {
		t.Fatalf("rose-pine's Surface is %.3f:1 against its focused row -- this fixture needs a theme that clears the floor unaided", ratio)
	}
	if got := palette.InputFill(palette.ActiveRowBG); !colorEqual(got, palette.Surface) {
		t.Errorf("InputFill = %v, want Surface %v unchanged where it is already legible", got, palette.Surface)
	}
}

// TestRGB8_RecoversChannelsExactly pins the >>8 recovery relativeLuminance
// depends on: a hex literal must come back as the bytes it was written with.
func TestRGB8_RecoversChannelsExactly(t *testing.T) {
	r, g, b, ok := rgb8(lipgloss.Color("#89b4fa"))
	if !ok {
		t.Fatal("rgb8 reported a hex literal as unmeasurable")
	}
	if r != 0x89 || g != 0xb4 || b != 0xfa {
		t.Errorf("rgb8(#89b4fa) = %#x %#x %#x, want 0x89 0xb4 0xfa", r, g, b)
	}
	if _, _, _, ok := rgb8(color.RGBA{R: 0x80, G: 0, B: 0, A: 0x80}); ok {
		t.Error("rgb8 accepted a translucent color; premultiplied channels would measure wrong")
	}
}

// TestBuiltinPalettes_SemanticTextIsLegibleOnEveryGround is the floor above,
// one class over: every floor this file had before #273 measured a background
// REGION -- the focused row's fill, an input's fill, the rule stroke -- and
// none of them measured a foreground anybody has to read.
//
// Danger and Warning are not emphasis on a word that is legible anyway. They
// ARE the word: field_dir.go's rowMarker renders `invalid` in Danger and
// `check timed out` in Warning, field_account.go renders `sign in again`,
// `expired` and the picker's refusal badges, and rowvalues.go's noteLine
// draws every repo-config refusal. There is no second, legible copy.
//
// All three grounds are asserted because a word reaches all three: a stack
// row is filled PanelBG, or ActiveRowBG while its field is focused
// (form.go's composeRows), and a picker's cursor row is repainted Surface
// inside the panel (widgets/picker.go). ActiveRowBG is the one a REFUSAL
// produces, because a refused submit moves focus to the row carrying the
// word -- but it is not always the worst: dracula clears 3:1 on both of the
// other two and measures 2.91:1 on Surface.
func TestBuiltinPalettes_SemanticTextIsLegibleOnEveryGround(t *testing.T) {
	for name := range builtinPalettes {
		t.Run(name, func(t *testing.T) {
			palette, ok := Builtin(name)
			if !ok {
				t.Fatalf("Builtin(%q) not found", name)
			}

			// The exemption is missing data, as everywhere else in this
			// file, but it is worth saying what is missing: terminal's
			// panel_bg and surface0 are herdr's Color::Reset -- "inherit
			// the host terminal's background" -- while its active_row_bg is
			// a plain #7f7f7f. So a word there IS drawn on one ground this
			// process can measure, and on that ground terminal's #ff0000
			// Danger is 1.00:1. It is exempt anyway because one value
			// serves all three grounds and raising it for the ground we can
			// see is a bet about the two we cannot; see raiseSemanticText,
			// and note that this leaves a real defect in that theme's own
			// values rather than in this clamp.
			if _, unknown := palette.PanelBG.(lipgloss.NoColor); unknown {
				if name != "terminal" {
					t.Fatalf("PanelBG is NoColor: only the terminal palette may be exempt from the semantic-text floor")
				}
				return
			}

			for _, field := range []struct {
				name  string
				value Color
			}{
				{"Danger", palette.Danger},
				{"Warning", palette.Warning},
			} {
				for _, ground := range []struct {
					where string
					value Color
				}{
					{"an unfocused row or the panel", palette.PanelBG},
					{"a focused stack row", palette.ActiveRowBG},
					{"a picker's cursor row", palette.Surface},
				} {
					got, ok := contrastRatio(field.value, ground.value)
					if !ok {
						t.Errorf("%s on %s is unmeasurable", field.name, ground.where)
						continue
					}
					if got < SemanticTextContrastFloor-contrastAssertionEpsilon {
						t.Errorf("%s on %s = %.3f:1, want >= %.2f:1 -- a word nobody can read is not a refusal (#273)",
							field.name, ground.where, got, SemanticTextContrastFloor)
					}
				}
			}
		})
	}
}

// TestFloorContrast_KeepsLegibleSemanticsUnchanged is the fidelity half of
// the clamp, and the reason #273 is a floor and not a repaint: herdr picked
// these colors and most of its themes picked them legibly, so a palette that
// already clears the floor has to come back byte-identical. catppuccin is
// the case that matters most -- it is the default, and it is what every
// golden frame in this repository renders on, so a raise here would move
// frames in two packages and change the screen most users see.
func TestFloorContrast_KeepsLegibleSemanticsUnchanged(t *testing.T) {
	raw, ok := builtinPalettes["catppuccin"]
	if !ok {
		t.Fatal("catppuccin missing from builtinPalettes")
	}
	got := Default()

	if !colorEqual(got.Danger, raw.Danger) {
		t.Errorf("Danger = %v, want catppuccin's own %v unchanged", got.Danger, raw.Danger)
	}
	if !colorEqual(got.Warning, raw.Warning) {
		t.Errorf("Warning = %v, want catppuccin's own %v unchanged", got.Warning, raw.Warning)
	}
}

// TestRaiseSemanticText_RaisesAnIllegibleValue is the other half, on the
// worst builtin: catppuccin-latte's peach is 1.918:1 on its own focused row,
// the lowest of the eighteen. It also pins that the walk STOPS near the
// floor rather than marching to the end of the ramp -- the same no-overshoot
// check TestEnsureContrast_RaisesIllegibleValuesToTheFloor makes, and for
// the same reason one layer up: a Danger dragged to white is not a warmer
// red, it is white.
func TestRaiseSemanticText_RaisesAnIllegibleValue(t *testing.T) {
	raw, ok := builtinPalettes["catppuccin-latte"]
	if !ok {
		t.Fatal("catppuccin-latte missing from builtinPalettes")
	}
	palette, ok := Builtin("catppuccin-latte")
	if !ok {
		t.Fatal("Builtin(\"catppuccin-latte\") not found")
	}

	if colorEqual(palette.Warning, raw.Warning) {
		t.Fatalf("raiseSemanticText left an illegible value alone: %v", palette.Warning)
	}
	for _, ground := range []Color{palette.PanelBG, palette.ActiveRowBG, palette.Surface} {
		ratio, ok := contrastRatio(palette.Warning, ground)
		if !ok {
			t.Fatalf("raised Warning is unmeasurable against %v", ground)
		}
		if ratio < SemanticTextContrastFloor-contrastAssertionEpsilon {
			t.Errorf("raised Warning is %.3f:1 against %v, want >= %.2f:1", ratio, ground, SemanticTextContrastFloor)
		}
		// Measured across the eighteen, the highest ratio any raised value
		// reaches on any ground is 4.76:1 (dracula's Danger on PanelBG).
		if ratio > 6 {
			t.Errorf("raised Warning is %.3f:1, overshooting -- the walk should stop near the floor", ratio)
		}
	}
}

// TestRaiseSemanticText_UnmeasurableInputsPassThrough pins the terminal
// palette's case from both sides. An unmeasurable ground is the one that
// matters: that theme's panel_bg and surface0 are herdr's Color::Reset, and
// one raised value would have to serve them and its real #7f7f7f
// active_row_bg at once.
func TestRaiseSemanticText_UnmeasurableInputsPassThrough(t *testing.T) {
	fg := lipgloss.Color("#805050") // illegible on every ground below
	dark := lipgloss.Color("#1a1a1a")
	cases := []struct {
		name    string
		fg      Color
		grounds []Color
		want    Color
	}{
		{"one unmeasurable ground", fg, []Color{dark, lipgloss.NoColor{}, dark}, fg},
		{"unmeasurable value", lipgloss.NoColor{}, []Color{dark}, lipgloss.NoColor{}},
		{"no grounds at all", fg, nil, fg},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := raiseSemanticText(tc.fg, tc.grounds, SemanticTextContrastFloor)
			if !colorEqual(got, tc.want) {
				t.Errorf("raiseSemanticText = %v, want %v passed through untouched", got, tc.want)
			}
		})
	}
}

// TestFloorContrast_MeasuresAgainstTheRaisedActiveRowBG pins the one
// ordering constraint inside floorContrast: ActiveRowBG is both a field with
// a floor of its own and a GROUND the semantic floor measures against, so it
// has to be raised before it is used as one. Get it backwards and a word is
// measured against a selection_bg that four builtins never actually draw,
// and passes while being illegible on the fill the user really sees.
//
// The defect the wrong order produces is usually an UNDER-raise rather than
// a skipped one, and rose-pine-dawn is the live example: with the two blocks
// swapped its peach is walked just far enough to clear 3:1 against the raw
// #f2e9e1 selection_bg, stops at #b76f6b, and arrives at 2.737:1 on the
// #dfd9d8 the form actually fills -- under the floor, and 19.4 from its own
// Danger, which is close enough to trip
// TestBuiltinPalettes_ClampedSemanticsStayDistinct too.
//
// The fixture here is synthetic anyway, because it isolates the simpler half
// -- a value skipped outright rather than raised short -- and states it
// without depending on any theme's numbers staying put. Its Danger clears
// the floor on the raw #141414 and fails on the #282828 that ensureContrast
// raises that to.
func TestFloorContrast_MeasuresAgainstTheRaisedActiveRowBG(t *testing.T) {
	raw := Palette{
		PanelBG:     lipgloss.Color("#101010"),
		Text:        lipgloss.Color("#ffffff"),
		Surface:     lipgloss.Color("#101010"),
		ActiveRowBG: lipgloss.Color("#141414"), // 1.05:1, well under ActiveRowContrastFloor
		Danger:      lipgloss.Color("#cc0000"),
		Warning:     lipgloss.Color("#ffcc00"), // comfortably legible; not the subject
	}

	// The fixture only means anything if the two orders disagree about
	// Danger, so assert that before asserting which one floorContrast took.
	if ratio, _ := contrastRatio(raw.Danger, raw.ActiveRowBG); ratio < SemanticTextContrastFloor {
		t.Fatalf("fixture Danger is %.3f:1 on the RAW fill -- it must pass there for the wrong order to be silent", ratio)
	}

	got := floorContrast(raw)

	if ratio, _ := contrastRatio(raw.Danger, got.ActiveRowBG); ratio >= SemanticTextContrastFloor {
		t.Fatalf("fixture Danger is %.3f:1 on the FLOORED fill -- it must fail there for this test to measure anything", ratio)
	}
	if colorEqual(got.Danger, raw.Danger) {
		t.Errorf("Danger came back unraised: floorContrast measured it against the raw selection_bg, not the fill it floored")
	}
}

// TestLoadHerdrPalette_FloorsAnIllegibleSemanticOverride is the case the
// clamp exists for that an assertion over builtinPalettes could not have
// covered, and it is the same argument ensureContrast's doc makes for
// ActiveRowBG: a user can set `danger` in herdr-draft's own `[palette]`, or
// `red` in herdr's `[theme.custom]`, and a table edit reaches neither.
func TestLoadHerdrPalette_FloorsAnIllegibleSemanticOverride(t *testing.T) {
	base := Default()
	illegible := "#191927" // a hair off catppuccin's #181825 PanelBG

	got := floorContrast(Resolve(base, map[string]string{"danger": illegible}))

	if colorEqual(got.Danger, lipgloss.Color(illegible)) {
		t.Fatalf("an illegible [palette] override survived unfloored: %v", got.Danger)
	}
	for _, ground := range []Color{got.PanelBG, got.ActiveRowBG, got.Surface} {
		ratio, ok := contrastRatio(got.Danger, ground)
		if !ok {
			t.Fatalf("floored Danger is unmeasurable against %v", ground)
		}
		if ratio < SemanticTextContrastFloor-contrastAssertionEpsilon {
			t.Errorf("floored override is %.3f:1 against %v, want >= %.2f:1", ratio, ground, SemanticTextContrastFloor)
		}
	}
}

// semanticSeparationFloor is a tripwire, not a perceptual metric: plain
// Euclidean distance in sRGB, which is a crude stand-in for "these two still
// look like different colors". It is here because #273 named convergence as
// the risk of clamping two semantic colors to one floor against one ground,
// and a measured tripwire is a better answer to that than an argument.
//
// The number is the measured worst across the eighteen with a little
// headroom: rose-pine-dawn lands at 23.0, its own dusty #b4637a red beside a
// #d7827e peach that the clamp darkens to #ac6865. nord is next at 24.5.
// Both are themes whose red and orange started close -- 47 and 42 apart
// untouched -- so the clamp halves a gap it did not create. What would trip
// this is a future theme, or a change to the walk, that closes one further.
const semanticSeparationFloor = 20.0

// TestBuiltinPalettes_ClampedSemanticsStayDistinct answers #273's own
// objection to this fix -- "a Warning and a Danger clamped to the same floor
// against the same ground converge into each other" -- with a measurement.
//
// They do move together, and the reason they do not merge is the direction
// the walk takes: toward black or white rather than toward Text. Walking
// toward a theme's Text drains hue, and the evidence is solarized, which
// under an earlier draft of raiseSemanticText arrived at #b07573 and
// #af765b -- 24 apart, from 39, and both brown. Toward the ramp's end it
// arrives at #e35b59 and #d36639, 37 apart and still a red and an orange.
func TestBuiltinPalettes_ClampedSemanticsStayDistinct(t *testing.T) {
	for name := range builtinPalettes {
		t.Run(name, func(t *testing.T) {
			palette, ok := Builtin(name)
			if !ok {
				t.Fatalf("Builtin(%q) not found", name)
			}
			d, ok := srgbDistance(palette.Danger, palette.Warning)
			if !ok {
				t.Skipf("Danger or Warning is unmeasurable in %s", name)
			}
			if d < semanticSeparationFloor {
				t.Errorf("Danger %v and Warning %v are %.1f apart in sRGB, want >= %.1f -- two refusals that look alike say less than one (#273)",
					palette.Danger, palette.Warning, d, semanticSeparationFloor)
			}
		})
	}
}

func srgbDistance(a, b Color) (float64, bool) {
	ar, ag, ab, aOK := rgb8(a)
	br, bg, bb, bOK := rgb8(b)
	if !aOK || !bOK {
		return 0, false
	}
	dr, dg, db := float64(ar)-float64(br), float64(ag)-float64(bg), float64(ab)-float64(bb)
	return math.Sqrt(dr*dr + dg*dg + db*db), true
}

// TestFarthestEnd_PicksTheDirectionWithRoomLeft pins the choice the walk
// depends on. It is measured against the grounds rather than inferred from
// the theme's name or from a luminance threshold, which is what lets one
// rule serve a light theme and a dark one.
func TestFarthestEnd_PicksTheDirectionWithRoomLeft(t *testing.T) {
	white, black := lipgloss.Color("#ffffff"), lipgloss.Color("#000000")
	cases := []struct {
		name    string
		grounds []Color
		want    Color
	}{
		{"dark grounds leave room upward", []Color{lipgloss.Color("#1e1e2e"), lipgloss.Color("#313244")}, white},
		{"light grounds leave room downward", []Color{lipgloss.Color("#eff1f5"), lipgloss.Color("#ccd0da")}, black},
		// The mid grey that makes the question real: terminal's #7f7f7f has
		// 3.95:1 to white and 5.32:1 to black, so down is the answer even
		// though nothing about it reads as a "light" theme.
		{"a mid grey still has a better side", []Color{lipgloss.Color("#7f7f7f")}, black},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := farthestEnd(tc.grounds); !colorEqual(got, tc.want) {
				t.Errorf("farthestEnd = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestRaiseSemanticText_AGiveUpIsNeverWorseThanDoingNothing covers the case
// the walk cannot solve, on the tier that is the whole argument for clamping
// rather than asserting. No builtin reaches it -- the grounds in a real
// theme sit close together, so a line from a colour to one end of the ramp
// clears all three or none -- but a `[palette]` or `[theme.custom]` override
// can put a ground anywhere, including between Danger and both ends.
//
// Two promises, and the second is the one that bites. A clamp that gives up
// must not return something WORSE than the colour it was handed: walking
// catppuccin's #f38ba8 to plain white against a #e0e0e0 Surface takes its
// worst ground from 1.75:1 to 1.32:1. And it must not answer for two
// semantics with the same value: both would give up at the same end of the
// same ramp, so Danger and Warning came back byte-identical white -- #273's
// own convergence objection, arriving on the tier
// TestBuiltinPalettes_ClampedSemanticsStayDistinct cannot see.
func TestRaiseSemanticText_AGiveUpIsNeverWorseThanDoingNothing(t *testing.T) {
	// Chosen by measurement, not by taste: a mid grey Surface that no walk
	// from either semantic can clear 3:1 against while still clearing the
	// two catppuccin grounds it sits between.
	for _, surface := range []string{"#e0e0e0", "#c0c0c0", "#a0a0a0"} {
		t.Run(surface, func(t *testing.T) {
			raw := Resolve(Default(), map[string]string{"surface": surface})
			got := floorContrast(raw)
			grounds := []Color{got.PanelBG, got.ActiveRowBG, got.Surface}

			if clearsFloor(got.Danger, grounds, SemanticTextContrastFloor) {
				t.Skipf("this fixture no longer gives up -- it needs a Surface no walk can clear")
			}
			for _, tc := range []struct {
				name     string
				was, now Color
			}{
				{"Danger", raw.Danger, got.Danger},
				{"Warning", raw.Warning, got.Warning},
			} {
				before, after := worstRatio(tc.was, grounds), worstRatio(tc.now, grounds)
				if after < before {
					t.Errorf("%s went from %.3f:1 to %.3f:1 (%v -> %v): a clamp that cannot help must not hurt",
						tc.name, before, after, tc.was, tc.now)
				}
			}
			if colorEqual(got.Danger, got.Warning) {
				t.Errorf("Danger and Warning both gave up at %v -- two refusals that are the same colour say less than one", got.Danger)
			}
		})
	}
}
