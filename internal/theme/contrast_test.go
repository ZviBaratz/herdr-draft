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
