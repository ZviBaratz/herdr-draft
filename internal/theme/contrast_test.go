package theme

import (
	"image/color"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
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

// TestBuiltinPalettes_SurfaceFillIsVisibleOnThePanel is #149, and it is a
// REGION floor rather than a word floor -- the sibling of
// ActiveRowContrastFloor and InputFillContrastFloor, not of
// SemanticTextContrastFloor. Both regions it covers are a fill whose edge
// the eye has to catch, and both carry a second signal that survives the
// fill going missing: the picker's cursor row keeps its marker glyph and
// its bold text, and the cancel button keeps its label. The words drawn on
// them are Text, DimText and the picker's tones, which have floors of their
// own. 1.25:1, for the reason InputFillContrastFloor gives, and its own
// constant for the reason InputFillContrastFloor gives too.
//
// What it guards is v2's invisible-rule defect in a third field. Surface on
// PanelBG is under 1.25:1 on TWELVE of the seventeen measurable builtins --
// one-dark and rose-pine at 1.07, one-light 1.09, rose-pine-dawn 1.10,
// vesper 1.11, solarized-light 1.14, solarized 1.15, kanagawa 1.16,
// tokyo-night 1.17, gruvbox-light 1.21, kanagawa-lotus and nord 1.24 -- and
// the default theme is not one of them (catppuccin is 1.40), which is
// exactly how twelve themes shipped a cursor row with no band and a cancel
// button that reads as plain text through a green suite.
func TestBuiltinPalettes_SurfaceFillIsVisibleOnThePanel(t *testing.T) {
	for name := range builtinPalettes {
		t.Run(name, func(t *testing.T) {
			palette, ok := Builtin(name)
			if !ok {
				t.Fatalf("Builtin(%q) not found", name)
			}
			// The same missing-data exemption as everywhere else in this
			// file: terminal's Surface is herdr's Color::Reset, and
			// widgets.PaintLine declines to paint a NoColor at all, so
			// there is no fill on screen to measure.
			if _, inherit := palette.Surface.(lipgloss.NoColor); inherit {
				if name != "terminal" {
					t.Fatalf("Surface is NoColor: only the terminal palette may be exempt")
				}
				return
			}

			got, ok := contrastRatio(palette.SurfaceFill(palette.PanelBG), palette.PanelBG)
			if !ok {
				t.Fatalf("the surface fill is unmeasurable against PanelBG")
			}
			if got < SurfaceFillContrastFloor-contrastAssertionEpsilon {
				t.Errorf("a Surface-filled region on the panel is %.3f:1 against it, want >= %.2f:1 -- a band nobody can see is not a band (#149)",
					got, SurfaceFillContrastFloor)
			}
		})
	}
}

// TestSurfaceFill_KeepsSurfaceWhereItIsAlreadyLegible is the fidelity half,
// and the reason #149 is a floor and not a repaint: herdr fills its own
// secondary button and selected row with surface0, so where surface0 is
// visible against the panel we use surface0 and the screen matches herdr's.
// catppuccin is the case that matters -- it is the default and what every
// golden frame renders on, at 1.40:1 against its own PanelBG.
func TestSurfaceFill_KeepsSurfaceWhereItIsAlreadyLegible(t *testing.T) {
	palette := Default()
	if ratio, _ := contrastRatio(palette.Surface, palette.PanelBG); ratio < SurfaceFillContrastFloor {
		t.Fatalf("catppuccin's Surface is %.3f:1 against its panel -- this fixture needs a theme that clears the floor unaided", ratio)
	}
	if got := palette.SurfaceFill(palette.PanelBG); !colorEqual(got, palette.Surface) {
		t.Errorf("SurfaceFill = %v, want Surface %v unchanged where it is already legible", got, palette.Surface)
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
// (form.go's composeRows), and a picker's cursor row is repainted inside
// the panel (widgets/picker.go). ActiveRowBG is the one a REFUSAL produces,
// because a refused submit moves focus to the row carrying the word -- but
// it is not always the worst: dracula clears 3:1 on both of the other two
// and measures 2.91:1 on the cursor row.
//
// The third ground is SurfaceFill(PanelBG) rather than the Surface field,
// and since #149 that is what the picker actually paints. Measuring against
// raw Surface would be measuring against a value twelve builtins never
// draw, which is the same defect
// TestFloorContrast_MeasuresAgainstTheRaisedActiveRowBG pins one field
// over.
//
// #277 added three more fields, and each is a different argument for the
// same floor. Success carries field_account.go's picker badge and
// submitview.go's done glyph. Accent carries the focus gutter and the
// running step's glyph, where "distinguishable" would be bar enough, but it
// also carries words: widgets/picker.go's matchStyle repaints the runes a
// query matched, and widgets/chiprow.go renders the active chip's label in
// it. Accent is a BACKGROUND as well -- the `create` button's fill, under a
// label knocked out in panelContrastFG, which is PanelBG. That is this same
// pair reversed, so flooring Accent against PanelBG floors the button's own
// label at the same time: the two uses pull together rather than against
// each other.
//
// Branch is the one that asked for a different number and got this one. It
// is not a marker -- a branch name is content, read character by character
// to check it, which is the case WCAG's 3:1 large-text figure is least
// defensible for. What decided it is the theme's OWN body text, measured on
// these same three grounds: Text bottoms out at 3.14:1 (solarized-light, on
// its focused row), with tokyo-night-day at 3.51:1 and solarized at 4.29:1
// -- three of the seventeen draw ordinary words below 4.5:1, and one-dark
// at 4.56:1 and kanagawa-lotus at 4.65:1 sit just over it. A 4.5:1 floor on
// Branch would hold a
// branch name to a higher standard than the title beside it, and charge 11
// of the 17 their branch hue to do it: solarized's #d33682 walks 96 units in
// sRGB to #e586b4, rose-pine-dawn's 76, nord's 63. At 3:1 it is three
// builtins and at most 36 units, and the floor lands just under every
// builtin's own worst body text -- which is where a floor belongs, catching
// the outliers rather than redesigning the theme.
func TestBuiltinPalettes_SemanticTextIsLegibleOnEveryGround(t *testing.T) {
	for name := range builtinPalettes {
		t.Run(name, func(t *testing.T) {
			palette, ok := Builtin(name)
			if !ok {
				t.Fatalf("Builtin(%q) not found", name)
			}

			// The exemption is missing data, as everywhere else in this
			// file, and since #304 none of it is left: terminal's panel_bg
			// and surface0 are herdr's Color::Reset -- "inherit the host
			// terminal's background" -- and so is its focused-row fill
			// (#276), while all five of these fields are ANSI indices,
			// which the terminal resolves from its own palette. So every
			// word is the terminal's own colour on the terminal's own
			// background, which is the pairing a terminal palette is
			// designed for, and TestTerminalPalette_EmitsTheTerminalsOwnColours
			// asserts that those are the colours it is sent.
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
				{"Success", palette.Success},
				{"Branch", palette.Branch},
				{"Accent", palette.Accent},
			} {
				for _, ground := range []struct {
					where string
					value Color
				}{
					{"an unfocused row or the panel", palette.PanelBG},
					{"a focused stack row", palette.ActiveRowBG},
					{"a picker's cursor row", palette.SurfaceFill(palette.PanelBG)},
				} {
					got, ok := contrastRatio(field.value, ground.value)
					if !ok {
						t.Errorf("%s on %s is unmeasurable", field.name, ground.where)
						continue
					}
					if got < SemanticTextContrastFloor-contrastAssertionEpsilon {
						t.Errorf("%s on %s = %.3f:1, want >= %.2f:1 -- a word nobody can read says nothing (#273, #277)",
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
//
// All five are asserted, not the two #273 floored, because that is what
// makes "#277 moved no frames" a measurement rather than an observation
// about the day it was run: catppuccin's Success, Branch and Accent measure
// 8.46:1, 6.19:1 and 5.97:1 on their worst ground, so extending the clamp
// to them could not have moved a frame, and this is where that stops being
// true if a future edit lowers one of the three.
func TestFloorContrast_KeepsLegibleSemanticsUnchanged(t *testing.T) {
	raw, ok := builtinPalettes["catppuccin"]
	if !ok {
		t.Fatal("catppuccin missing from builtinPalettes")
	}
	got := Default()

	for _, tc := range []struct {
		name     string
		was, now Color
	}{
		{"Danger", raw.Danger, got.Danger},
		{"Warning", raw.Warning, got.Warning},
		{"Success", raw.Success, got.Success},
		{"Branch", raw.Branch, got.Branch},
		{"Accent", raw.Accent, got.Accent},
	} {
		if !colorEqual(tc.now, tc.was) {
			t.Errorf("%s = %v, want catppuccin's own %v unchanged", tc.name, tc.now, tc.was)
		}
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
	for _, ground := range []Color{palette.PanelBG, palette.ActiveRowBG, palette.SurfaceFill(palette.PanelBG)} {
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
// palette's case from both sides: that theme's panel_bg and surface0 are
// herdr's Color::Reset, and its words are ANSI indices (#304). The indexed
// half has a test of its own, TestClamps_HandAnIndexBackAsAnIndex, because
// colorEqual cannot tell an index from the RGB its library stand-in reports.
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
// the floor on the raw #242424 (3.226:1) and fails on the #363636 that
// ensureContrast raises that to (2.512:1).
func TestFloorContrast_MeasuresAgainstTheRaisedActiveRowBG(t *testing.T) {
	raw := Palette{
		PanelBG:     lipgloss.Color("#202020"),
		Text:        lipgloss.Color("#ffffff"),
		Surface:     lipgloss.Color("#000000"), // 1.29:1 on the panel: clears SurfaceFillContrastFloor unaided
		ActiveRowBG: lipgloss.Color("#242424"), // 1.05:1, well under ActiveRowContrastFloor
		Danger:      lipgloss.Color("#e60000"),
		Warning:     lipgloss.Color("#ffcc00"), // comfortably legible; not the subject
	}

	// The fixture only means anything if the two orders disagree about
	// Danger, so assert that before asserting which one floorContrast took.
	if ratio, _ := contrastRatio(raw.Danger, raw.ActiveRowBG); ratio < SemanticTextContrastFloor {
		t.Fatalf("fixture Danger is %.3f:1 on the RAW fill -- it must pass there for the wrong order to be silent", ratio)
	}
	// And ActiveRowBG has to be the ONLY ground that can raise it, or the
	// test would pass under the wrong order for the wrong reason. This
	// guard is here because that is exactly what #149 did to the earlier
	// fixture: its Surface was its PanelBG, so SurfaceFill raised it to
	// #282828, on which the same Danger measured 2.505:1 -- a second
	// reason, masking the first.
	for _, ground := range []Color{raw.PanelBG, raw.SurfaceFill(raw.PanelBG)} {
		if ratio, _ := contrastRatio(raw.Danger, ground); ratio < SemanticTextContrastFloor {
			t.Fatalf("fixture Danger is %.3f:1 on %v -- it must clear every ground but the focused fill, or this test is not about ordering", ratio, ground)
		}
	}

	got := floorContrast(raw)

	if ratio, _ := contrastRatio(raw.Danger, got.ActiveRowBG); ratio >= SemanticTextContrastFloor {
		t.Fatalf("fixture Danger is %.3f:1 on the FLOORED fill -- it must fail there for this test to measure anything", ratio)
	}
	if colorEqual(got.Danger, raw.Danger) {
		t.Errorf("Danger came back unraised: floorContrast measured it against the raw selection_bg, not the fill it floored")
	}
}

// TestFloorContrast_MeasuresAgainstThePaintedCursorFill is the test above's
// twin, one ground over, and #149 is what made it necessary: the third
// ground a word is measured against is the fill widgets/picker.go repaints
// its cursor row with, and since #149 that is SurfaceFill(PanelBG) rather
// than the Surface field. Name the field instead and a word is measured
// against a value twelve of the seventeen builtins never draw -- the same
// defect as measuring against a raw selection_bg, and it went unguarded in
// #149's first draft: reverting that one expression left the whole suite
// green, because over the eighteen builtins the two ground lists happen to
// agree on every outcome.
//
// They only happen to. floorContrast also runs on a [palette] override and
// a custom herdr theme, where nothing constrains Surface to sit near the
// value its raise lands on. This fixture is such a palette, and it isolates
// the simpler half exactly as the test above does -- a value skipped
// outright rather than raised short:
//
//	Surface      #343434  1.060:1 on the panel, so the fill IS raised
//	SurfaceFill  #454545  what a cursor row is actually painted
//	Danger       #868686  3.419:1 on the raw Surface, 2.633:1 on the fill
//
// Its focused row is DARKER than its panel, which no builtin is, and that
// is the point rather than a curiosity: a raised fill is the panel walked
// toward Text, and so is a floored ActiveRowBG, so whenever ActiveRowBG
// needs flooring the two grounds coincide and ActiveRowBG alone forces the
// raise. Separating them takes an ActiveRowBG that clears its own floor
// without being walked -- here #000000 at 1.591:1 -- and is gentler on the
// word than the fill is.
func TestFloorContrast_MeasuresAgainstThePaintedCursorFill(t *testing.T) {
	raw := Palette{
		PanelBG:     lipgloss.Color("#303030"),
		Text:        lipgloss.Color("#ffffff"),
		Surface:     lipgloss.Color("#343434"), // 1.06:1 on the panel, under SurfaceFillContrastFloor
		ActiveRowBG: lipgloss.Color("#000000"), // 1.59:1: clears its own floor untouched
		Danger:      lipgloss.Color("#868686"),
		Warning:     lipgloss.Color("#ffcc00"), // comfortably legible; not the subject
	}
	fill := raw.SurfaceFill(raw.PanelBG)
	if colorEqual(fill, raw.Surface) {
		t.Fatalf("the fixture's Surface is not raised -- it must be, or both ground lists name the same value")
	}

	// The cursor fill has to be the ONLY ground that can raise this Danger,
	// or the test would pass without measuring the thing it is named for.
	for _, ground := range []Color{raw.PanelBG, raw.Surface, raw.ActiveRowBG} {
		if ratio, _ := contrastRatio(raw.Danger, ground); ratio < SemanticTextContrastFloor {
			t.Fatalf("fixture Danger is %.3f:1 on %v -- it must clear every ground but the painted fill, or this test is not about the fill", ratio, ground)
		}
	}
	if ratio, _ := contrastRatio(raw.Danger, fill); ratio >= SemanticTextContrastFloor {
		t.Fatalf("fixture Danger is %.3f:1 on the PAINTED fill -- it must fail there for this test to measure anything", ratio)
	}

	got := floorContrast(raw)

	if colorEqual(got.Danger, raw.Danger) {
		t.Errorf("Danger came back unraised: floorContrast measured it against the Surface field, not the fill a picker paints its cursor row with (#149)")
	}
	if ratio, _ := contrastRatio(got.Danger, fill); ratio < SemanticTextContrastFloor-contrastAssertionEpsilon {
		t.Errorf("raised Danger is %.3f:1 on the painted fill, want >= %.2f:1", ratio, SemanticTextContrastFloor)
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
	for _, ground := range []Color{got.PanelBG, got.ActiveRowBG, got.SurfaceFill(got.PanelBG)} {
		ratio, ok := contrastRatio(got.Danger, ground)
		if !ok {
			t.Fatalf("floored Danger is unmeasurable against %v", ground)
		}
		if ratio < SemanticTextContrastFloor-contrastAssertionEpsilon {
			t.Errorf("floored override is %.3f:1 against %v, want >= %.2f:1", ratio, ground, SemanticTextContrastFloor)
		}
	}
}

// TestTerminalPalette_EmitsTheTerminalsOwnColours is #304, and it asserts
// BYTES rather than a ratio because a ratio is the one thing this palette
// cannot have. Every coloured field is an ANSI palette index, which the
// host terminal resolves from its own palette, and the three herdr gives
// Color::Reset emit nothing at all. Before #304 the nine indexed fields
// were xterm's RGB defaults and went out as truecolor -- `\x1b[38;2;255;0;0m`
// for Danger -- so a terminal that redefines red still got xterm's.
//
// It asserts what Builtin hands out, after floorContrast, and the three
// backgrounds are rendered as backgrounds, because a foreground and a
// background SGR are different bytes for the same colour. It does NOT pin that a clamp
// hands an index back untouched, and cannot: on this palette every clamp
// is exempted first by its NoColor grounds, so the bytes come out right
// whether or not rgb8 measures an index. Measured: with rgb8 measuring
// indices again, this test passes. That half is
// TestClamps_HandAnIndexBackAsAnIndex's and the override test's.
func TestTerminalPalette_EmitsTheTerminalsOwnColours(t *testing.T) {
	palette, ok := Builtin("terminal")
	if !ok {
		t.Fatal("Builtin(\"terminal\") not found")
	}
	fg := func(c Color) string { return lipgloss.NewStyle().Foreground(c).Render("x") }
	bg := func(c Color) string { return lipgloss.NewStyle().Background(c).Render("x") }

	for _, tc := range []struct {
		field     string
		got, want string
	}{
		{"Accent", fg(palette.Accent), "\x1b[34mx\x1b[m"},     // Color::Blue
		{"Success", fg(palette.Success), "\x1b[32mx\x1b[m"},   // Color::Green
		{"Warning", fg(palette.Warning), "\x1b[33mx\x1b[m"},   // Color::Yellow
		{"DimText", fg(palette.DimText), "\x1b[37mx\x1b[m"},   // Color::Gray
		{"Overlay0", fg(palette.Overlay0), "\x1b[37mx\x1b[m"}, // Color::Gray
		{"Branch", fg(palette.Branch), "\x1b[37mx\x1b[m"},     // Color::Gray
		{"Border", fg(palette.Border), "\x1b[90mx\x1b[m"},     // Color::DarkGray
		{"Danger", fg(palette.Danger), "\x1b[91mx\x1b[m"},     // Color::LightRed
		// Color::Reset: no SGR at all, the terminal's own colour stays.
		// ActiveRowBG is one of them since #276: the focused row is marked
		// by its gutter glyph and bold, and its words sit on the terminal's
		// own background.
		{"ActiveRowBG", bg(palette.ActiveRowBG), "x"},
		{"PanelBG", bg(palette.PanelBG), "x"},
		{"Text", fg(palette.Text), "x"},
		{"Surface", bg(palette.Surface), "x"},
	} {
		if tc.got != tc.want {
			t.Errorf("terminal's %s renders %q, want %q -- the terminal palette must send the terminal's own colours, not an RGB guess at them (#304)",
				tc.field, tc.got, tc.want)
		}
	}
}

// TestBuiltinPalettes_OnlyTerminalEmitsIndexedColours is the other side of
// the test above: an index is correct for exactly one palette. Every other
// builtin is herdr's RGB table and has to reach the screen as the RGB it
// was written in, and every one of them is measured by the floors in this
// file -- which is only true while none of their fields is an index, since
// rgb8 reports an index as unmeasurable and every floor would quietly exempt
// it.
//
// Each field is rendered and the bytes are checked, on top of the type,
// because the bytes are what the terminal receives.
func TestBuiltinPalettes_OnlyTerminalEmitsIndexedColours(t *testing.T) {
	paletteType := reflect.TypeOf(Palette{})
	for name := range builtinPalettes {
		if name == "terminal" {
			continue
		}
		t.Run(name, func(t *testing.T) {
			palette, ok := Builtin(name)
			if !ok {
				t.Fatalf("Builtin(%q) not found", name)
			}
			value := reflect.ValueOf(palette)
			for i := range value.NumField() {
				field := paletteType.Field(i).Name
				c, _ := value.Field(i).Interface().(Color)
				switch c.(type) {
				case ansi.BasicColor, ansi.IndexedColor:
					t.Errorf("%s is the ANSI index %v -- only the terminal palette may hand its colours to the terminal's own palette", field, c)
					continue
				}
				if _, _, _, ok := rgb8(c); !ok {
					t.Errorf("%s = %v is unmeasurable, so every floor would exempt it", field, c)
				}
				if got := lipgloss.NewStyle().Foreground(c).Render("x"); !strings.HasPrefix(got, "\x1b[38;2;") {
					t.Errorf("%s renders %q, want a truecolor sequence", field, got)
				}
			}
		})
	}
}

// TestRGB8_AnIndexedColourIsUnmeasurable pins the decision #304 had to
// make about what the contrast machinery does with an index, and why a
// number is the wrong answer. ansi.BasicColor(9).RGBA() does not decline:
// it reports the VGA palette's #ff0000, and ANSI 4 its #000080, which is
// neither xterm's #0000ee nor anything the user's terminal draws. A ratio
// computed from that looks like a measurement and is not one, which is the
// failure this whole file exists to prevent. So an index is unmeasurable,
// exactly as NoColor is, for the same reason: this process does not know
// the colour on the screen.
func TestRGB8_AnIndexedColourIsUnmeasurable(t *testing.T) {
	for _, c := range []Color{ansi.BasicColor(9), ansi.BasicColor(0), ansi.IndexedColor(208), lipgloss.Color("9"), lipgloss.Color("208")} {
		if r, g, b, ok := rgb8(c); ok {
			t.Errorf("rgb8(%T %v) = #%02x%02x%02x, want unmeasurable -- that is the library's stand-in, not the terminal's colour", c, c, r, g, b)
		}
		if ratio, ok := contrastRatio(c, lipgloss.Color("#ffffff")); ok {
			t.Errorf("contrastRatio(%T %v, white) = %.3f:1, want unmeasurable", c, c, ratio)
		}
	}
}

// TestClamps_HandAnIndexBackAsAnIndex is the invariant #304 names: every
// clamp in this package returns a MIXED RGBA whenever it raises a colour,
// so an index let through one would come back as truecolor and quietly undo
// the change. Over the builtins that cannot happen -- terminal's grounds are
// all unmeasurable and every clamp exempts it on those alone -- which is
// exactly why it needs its own test. A `[palette]` override can give that
// palette measurable grounds, and then the index itself is the only thing
// standing between the clamp and a mix.
//
// So the grounds here are measurable and chosen so that the VGA stand-in
// for each index FAILS its floor on them: an rgb8 that measured an index
// would walk it. And the comparison is by identity, not colorEqual, which
// compares RGBA and cannot tell ANSI 9 from a hex #ff0000 at all.
func TestClamps_HandAnIndexBackAsAnIndex(t *testing.T) {
	grey := lipgloss.Color("#7f7f7f") // VGA #ff0000 is 1.00:1 on it, #808080 1.01:1, #c0c0c0 2.20:1
	grounds := []Color{grey, grey, grey}
	text := lipgloss.Color("#000000")

	for _, tc := range []struct {
		name  string
		index Color
		got   Color
	}{
		{"ensureContrast", ansiIndex(8), ensureContrast(grey, ansiIndex(8), text, ActiveRowContrastFloor)},
		{"raiseSemanticText", ansiIndex(9), raiseSemanticText(ansiIndex(9), grounds, SemanticTextContrastFloor)},
		{"raiseDimText", ansiIndex(7), raiseDimText(ansiIndex(7), grounds, text, DimTextContrastFloor)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.index {
				t.Errorf("%s turned the ANSI index %v into %T %v -- an index is the terminal's own colour, and a mix of it is not (#304)",
					tc.name, tc.index, tc.got, tc.got)
			}
		})
	}
}

// TestLoadHerdrPalette_HexOverridesOnTheTerminalPalette is the override
// path. A `[palette]` key replaces one field with hex and leaves the rest as
// the table has them, so an override on this palette always produces the
// mixed case -- some fields indexed, some hex -- and it has to work in both
// directions: the hex override is honoured as written and floored where its
// grounds allow, and every field it did not touch still reaches the screen
// as the terminal's own colour.
//
// It goes through LoadHerdrPaletteFrom with herdr's own `name =
// "terminal"`, the way a user reaches this palette.
//
// Two cases, because the grounds decide whether any clamp can run. With
// only `danger` overridden, every ground is still unmeasurable -- a NoColor
// panel, surface and focused row -- so the hex red stays exactly
// as written. With the three grounds overridden too, a word CAN be measured,
// and an illegible hex `danger` is raised while the indexed Warning beside it
// is still ANSI 3: the clamp ran, and the index was not in its path.
//
// Only Warning, Success and Accent are checked there, and the guard in that
// case asserts why: they are the indices whose library stand-in (VGA
// #808000, #008000, #000080) fails the floor on these grounds, so an rgb8
// that measured them would walk them. ANSI 7's stand-in, #c0c0c0, already
// clears 3:1 here, so Branch and DimText would come back unchanged with or
// without the exemption and could not fail for the reason this test gives.
// raiseDimText keeping an index is TestClamps_HandAnIndexBackAsAnIndex's.
func TestLoadHerdrPalette_HexOverridesOnTheTerminalPalette(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeFile(t, path, `
[theme]
name = "terminal"
`)

	t.Run("a hex field on unmeasurable grounds is kept as written", func(t *testing.T) {
		got := LoadHerdrPaletteFrom(path, map[string]string{"danger": "#ff5555"})
		if r := lipgloss.NewStyle().Foreground(got.Danger).Render("x"); r != "\x1b[38;2;255;85;85mx\x1b[m" {
			t.Errorf("the overridden Danger renders %q, want the user's #ff5555 as truecolor", r)
		}
		if got.Warning != ansiIndex(3) || got.ActiveRowBG != (lipgloss.NoColor{}) {
			t.Errorf("fields the override did not name moved: Warning %T %v, ActiveRowBG %T %v", got.Warning, got.Warning, got.ActiveRowBG, got.ActiveRowBG)
		}
	})

	t.Run("a hex field on measurable grounds is floored and the indices are not", func(t *testing.T) {
		illegible := "#303030"
		got := LoadHerdrPaletteFrom(path, map[string]string{
			"panel_bg":      "#202020",
			"surface":       "#000000",
			"text":          "#ffffff",
			"active_row_bg": "#404040",
			"danger":        illegible,
		})
		grounds := []Color{got.PanelBG, got.ActiveRowBG, got.SurfaceFill(got.PanelBG)}
		if !clearsFloor(lipgloss.Color("#ffffff"), grounds, SemanticTextContrastFloor) {
			t.Fatalf("fixture grounds %v leave no room for a word -- the clamp would have nowhere to go", grounds)
		}
		if colorEqual(got.Danger, lipgloss.Color(illegible)) {
			t.Errorf("the illegible hex Danger %s survived unfloored on grounds that are all measurable", illegible)
		}
		if !clearsFloor(got.Danger, grounds, SemanticTextContrastFloor) {
			t.Errorf("the floored Danger %v does not clear %.1f:1 on %v", got.Danger, SemanticTextContrastFloor, grounds)
		}
		for _, tc := range []struct {
			field     string
			got, want Color
		}{
			{"Warning", got.Warning, ansiIndex(3)},
			{"Success", got.Success, ansiIndex(2)},
			{"Accent", got.Accent, ansiIndex(4)},
		} {
			// The premise: the library's stand-in for this index fails the
			// floor here, so a clamp that measured it would have walked it.
			// Without that, the row passes for a second reason.
			r, g, b, _ := tc.want.RGBA()
			standIn := color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: 0xff}
			if clearsFloor(standIn, grounds, SemanticTextContrastFloor) {
				t.Fatalf("%s's stand-in %v already clears the floor on %v -- this row cannot tell an exempt index from a measured one", tc.field, standIn, grounds)
			}
			if tc.got != tc.want {
				t.Errorf("%s = %T %v, want the ANSI index %v it was -- a clamp that ran on this palette mixed an index (#304)", tc.field, tc.got, tc.got, tc.want)
			}
		}
	})
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

// TestBuiltinPalettes_ClampDoesNotCloseAGapItDidNotCreate is the test above
// generalised to the five fields #277 left floored, and it is a different
// assertion rather than three more rows because the absolute one cannot be
// made over all ten pairs: rose-pine draws Branch and Accent in the same
// #c4a7e7 and vesper draws Warning and Accent in the same #ffc799, by their
// own choice and before this package touches anything. A floor over every
// pair would fail on those two themes for a defect they do not have.
//
// So the promise here is relative: a pair the theme itself kept apart must
// still be apart after the clamp. That is exactly #273's convergence
// objection, scoped to the part of it this package is answerable for --
// five colours walking toward one end of one ramp is five chances to
// collide, and the Danger/Warning pair above is only one of them.
//
// Measured across the eighteen, over all ten pairs: the clamp narrows 73 of
// them, by up to 49.9 units (catppuccin-latte's Warning and Success, 201.8
// to 151.9), so "it barely moves anything" is not the defence -- the
// relative rule is. The closest it leaves a pair it narrowed is
// rose-pine-dawn's Danger and Warning at 23.0, three units over the floor,
// with nord's next at 24.5; both are pairs semanticSeparationFloor's own
// doc already records.
//
// The closest non-identical pair of all is vesper's, at 18.0 -- Branch
// beside Accent and Warning beside Branch, untouched by the clamp and
// under the floor before it ran. That is the second reason this assertion
// has to be relative rather than absolute, and it is not the same reason as
// the byte-identical pairs above: a theme may draw two semantics close
// together as well as identically, and neither is a defect this package
// introduced.
func TestBuiltinPalettes_ClampDoesNotCloseAGapItDidNotCreate(t *testing.T) {
	for name := range builtinPalettes {
		t.Run(name, func(t *testing.T) {
			raw := builtinPalettes[name]
			palette, ok := Builtin(name)
			if !ok {
				t.Fatalf("Builtin(%q) not found", name)
			}
			fields := []struct {
				name     string
				was, now Color
			}{
				{"Danger", raw.Danger, palette.Danger},
				{"Warning", raw.Warning, palette.Warning},
				{"Success", raw.Success, palette.Success},
				{"Branch", raw.Branch, palette.Branch},
				{"Accent", raw.Accent, palette.Accent},
			}
			for i := range fields {
				for j := i + 1; j < len(fields); j++ {
					a, b := fields[i], fields[j]
					before, ok := srgbDistance(a.was, b.was)
					if !ok || before < semanticSeparationFloor {
						continue // unmeasurable, or the theme's own choice
					}
					after, ok := srgbDistance(a.now, b.now)
					if !ok {
						t.Errorf("%s or %s became unmeasurable", a.name, b.name)
						continue
					}
					if after < semanticSeparationFloor {
						t.Errorf("%s %v and %s %v started %.1f apart in sRGB and the clamp left them %.1f (%v, %v), want >= %.1f -- a clamp may not close a gap it did not create (#277)",
							a.name, a.was, b.name, b.was, before, after, a.now, b.now, semanticSeparationFloor)
					}
				}
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
		// The mid grey that makes the question real, and the fill terminal's
		// focused row had until #304: #7f7f7f has 4.00:1 to white and 5.24:1
		// to black, so down is the answer even though nothing about it reads
		// as a "light" theme.
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
//
// All five floored fields are checked since #277, and the second promise is
// why it matters that they are: five colours giving up at one end of one
// ramp is five chances to collide, not one. catppuccin is the right base
// for it because its five are all distinct to begin with, which is not true
// of every builtin -- rose-pine's Branch and Accent are the same #c4a7e7 by
// the theme's own choice, and vesper's Warning and Accent the same #ffc799.
//
// Note how weak the second promise is, and that the weakness is honest
// rather than an oversight: it asserts the five are not BYTE-identical, not
// that they are still told apart. On the #c0c0c0 fixture all five do give
// up, and they land on five near-whites within about 7 units of each other
// in sRGB -- distinct, and indistinguishable. bestAlong cannot promise more
// than it does: the give-up path is reached exactly when no point on any of
// the five segments clears the floor, and on that tier there is nothing
// left to spend. semanticSeparationFloor is the assertion that a clamp
// which SUCCEEDS keeps its colours apart; this one is only that a clamp
// which fails does not answer for two fields with one value.
func TestRaiseSemanticText_AGiveUpIsNeverWorseThanDoingNothing(t *testing.T) {
	// Chosen by measurement, not by taste: a mid grey Surface that no walk
	// from either semantic can clear 3:1 against while still clearing the
	// two catppuccin grounds it sits between.
	for _, surface := range []string{"#e0e0e0", "#c0c0c0", "#a0a0a0"} {
		t.Run(surface, func(t *testing.T) {
			raw := Resolve(Default(), map[string]string{"surface": surface})
			got := floorContrast(raw)
			grounds := []Color{got.PanelBG, got.ActiveRowBG, got.SurfaceFill(got.PanelBG)}

			if clearsFloor(got.Danger, grounds, SemanticTextContrastFloor) {
				t.Skipf("this fixture no longer gives up -- it needs a Surface no walk can clear")
			}
			fields := []struct {
				name     string
				was, now Color
			}{
				{"Danger", raw.Danger, got.Danger},
				{"Warning", raw.Warning, got.Warning},
				{"Success", raw.Success, got.Success},
				{"Branch", raw.Branch, got.Branch},
				{"Accent", raw.Accent, got.Accent},
			}
			for _, tc := range fields {
				before, after := worstRatio(tc.was, grounds), worstRatio(tc.now, grounds)
				if after < before {
					t.Errorf("%s went from %.3f:1 to %.3f:1 (%v -> %v): a clamp that cannot help must not hurt",
						tc.name, before, after, tc.was, tc.now)
				}
			}
			for i := range fields {
				for j := i + 1; j < len(fields); j++ {
					if colorEqual(fields[i].now, fields[j].now) {
						t.Errorf("%s and %s both gave up at %v -- two semantics that are the same colour say less than one",
							fields[i].name, fields[j].name, fields[i].now)
					}
				}
			}
		})
	}
}

// dimTextGrounds is every ground a dim word is measured on: the three the
// semantic floor uses, for the reason floorContrast gives.
func dimTextGrounds(p Palette) []struct {
	where string
	value Color
} {
	return []struct {
		where string
		value Color
	}{
		{"an unfocused row or the panel", p.PanelBG},
		{"a focused stack row", p.ActiveRowBG},
		{"a picker's cursor row", p.SurfaceFill(p.PanelBG)},
	}
}

// TestBuiltinPalettes_DimTextIsLegibleOnEveryGround is #299. DimText draws
// every row's label -- the only way to tell which row the cursor is on --
// and most of the rest of what it draws is the only rendering of something
// somebody reads. Before this floor it was 2.23:1 on solarized-light's
// focused row and 2.77:1 on tokyo-night-day's, and the default theme was
// never near it (5.65:1), so no frame could have shown either.
//
// The exemption is terminal's, for the reason every other floor in this
// file gives.
func TestBuiltinPalettes_DimTextIsLegibleOnEveryGround(t *testing.T) {
	for name := range builtinPalettes {
		t.Run(name, func(t *testing.T) {
			palette, ok := Builtin(name)
			if !ok {
				t.Fatalf("Builtin(%q) not found", name)
			}
			if _, unknown := palette.PanelBG.(lipgloss.NoColor); unknown {
				if name != "terminal" {
					t.Fatalf("PanelBG is NoColor: only the terminal palette may be exempt from the dim-text floor")
				}
				return
			}
			for _, ground := range dimTextGrounds(palette) {
				got, ok := contrastRatio(palette.DimText, ground.value)
				if !ok {
					t.Errorf("DimText on %s is unmeasurable", ground.where)
					continue
				}
				if got < DimTextContrastFloor-contrastAssertionEpsilon {
					t.Errorf("DimText on %s = %.3f:1, want >= %.2f:1 -- a label nobody can read does not say which row this is (#299)",
						ground.where, got, DimTextContrastFloor)
				}
			}
		})
	}
}

// TestBuiltinPalettes_DimTextClampNeverPassesText is the ceiling, over the
// builtins: wherever the clamp moved DimText, the dim tier is still the
// dimmer of the two on every ground. A label raised past its value has not
// been fixed, it has been swapped with the value.
//
// It is relative -- asserted only where the clamp MOVED the colour -- and
// the reason is a theme rather than caution: kanagawa-lotus ships a
// DimText with more contrast than its Text in herdr's own values (#309),
// and the clamp only ever raises, so an absolute "DimText is never past
// Text" would fail on a colour this package never touched. That is the same
// shape as TestBuiltinPalettes_ClampDoesNotCloseAGapItDidNotCreate, for the
// same kind of reason.
//
// Over the builtins this does not pin the ceiling on its own: at
// dimTextMixStep, solarized-light's walk reaches the floor one step before
// it would reach Text, so the ceiling never has to fire.
// TestRaiseDimText_StopsShortOfText is the test that does. What this one
// catches is a coarser step, which jumps that theme straight past its Text
// (2.974:1, then 3.313:1 against a Text of 3.137:1).
func TestBuiltinPalettes_DimTextClampNeverPassesText(t *testing.T) {
	for name := range builtinPalettes {
		t.Run(name, func(t *testing.T) {
			raw := builtinPalettes[name]
			palette, ok := Builtin(name)
			if !ok {
				t.Fatalf("Builtin(%q) not found", name)
			}
			if colorEqual(palette.DimText, raw.DimText) {
				return // the theme's own choice, inverted or not
			}
			for _, ground := range dimTextGrounds(palette) {
				dim, dimOK := contrastRatio(palette.DimText, ground.value)
				text, textOK := contrastRatio(palette.Text, ground.value)
				if !dimOK || !textOK {
					t.Errorf("DimText or Text on %s is unmeasurable", ground.where)
					continue
				}
				if dim > text {
					t.Errorf("the clamp moved DimText %v -> %v, which is %.3f:1 on %s against Text's %.3f:1 -- the dim tier is now the louder one (#299)",
						raw.DimText, palette.DimText, dim, ground.where, text)
				}
			}
		})
	}
}

// TestRaiseDimText_StopsShortOfText pins the ceiling by itself, on a ground
// where the floor is out of reach: this Text is under 3:1 on it, so every
// colour that clears the floor out-contrasts Text. A clamp with no ceiling
// walks straight past it and reports success.
//
// What it must do instead is take the best point short of Text and stop --
// better than it was handed, never louder than Text, and not claiming the
// floor. A second case with room under Text pins the ordinary path next to
// it, so the ceiling cannot be satisfied by never raising at all.
func TestRaiseDimText_StopsShortOfText(t *testing.T) {
	ground := lipgloss.Color("#202020")
	grounds := []Color{ground}
	cases := []struct {
		name         string
		text, dim    Color
		floorInReach bool
	}{
		{"Text under the floor", lipgloss.Color("#666666"), lipgloss.Color("#404040"), false},
		{"room under Text", lipgloss.Color("#b0b0b0"), lipgloss.Color("#404040"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			textRatio, _ := contrastRatio(tc.text, ground)
			if (textRatio >= DimTextContrastFloor) != tc.floorInReach {
				t.Fatalf("fixture Text is %.3f:1 on the ground -- the case needs it %s the floor", textRatio, map[bool]string{true: "above", false: "under"}[tc.floorInReach])
			}
			if clearsFloor(tc.dim, grounds, DimTextContrastFloor) {
				t.Fatalf("fixture DimText already clears the floor -- nothing would be walked")
			}

			got := raiseDimText(tc.dim, grounds, tc.text, DimTextContrastFloor)

			if outContrasts(got, tc.text, grounds) {
				r, _ := contrastRatio(got, ground)
				t.Errorf("raiseDimText = %v at %.3f:1, past Text's %.3f:1 -- the ceiling did not hold", got, r, textRatio)
			}
			if colorEqual(got, tc.dim) {
				t.Errorf("raiseDimText left %v where it was, with room to raise it short of Text", tc.dim)
			}
			if before, after := worstRatio(tc.dim, grounds), worstRatio(got, grounds); after < before {
				t.Errorf("raiseDimText went from %.3f:1 to %.3f:1 -- a clamp that cannot help must not hurt", before, after)
			}
			if clears := clearsFloor(got, grounds, DimTextContrastFloor); clears != tc.floorInReach {
				r, _ := contrastRatio(got, ground)
				t.Errorf("raiseDimText = %v at %.3f:1: clears the floor = %v, want %v", got, r, clears, tc.floorInReach)
			}
		})
	}
}

// TestFloorContrast_KeepsLegibleDimTextUnchanged is the fidelity half. herdr
// picked these colours and fifteen of the seventeen measurable builtins
// picked them legibly, so every one that already clears the floor comes
// back byte-identical. catppuccin is asserted by name as well, because it
// is what every golden frame renders on: this is where "#299 moved no
// frames" stops being true if a future edit lowers its DimText.
func TestFloorContrast_KeepsLegibleDimTextUnchanged(t *testing.T) {
	if _, ok := builtinPalettes["catppuccin"]; !ok {
		t.Fatal("catppuccin missing from builtinPalettes")
	}
	for name, raw := range builtinPalettes {
		palette, ok := Builtin(name)
		if !ok {
			t.Fatalf("Builtin(%q) not found", name)
		}
		grounds := []Color{palette.PanelBG, palette.ActiveRowBG, palette.SurfaceFill(palette.PanelBG)}
		legible := clearsFloor(raw.DimText, grounds, DimTextContrastFloor)
		if name == "catppuccin" && !legible {
			t.Fatalf("catppuccin's own DimText no longer clears the floor -- this test's premise, and every golden frame, has moved")
		}
		if legible && !colorEqual(palette.DimText, raw.DimText) {
			t.Errorf("%s: DimText = %v, want the theme's own %v unchanged -- it already clears the floor", name, palette.DimText, raw.DimText)
		}
	}
}

// TestLoadHerdrPalette_FloorsAnIllegibleDimTextOverride is the case a table
// edit could not have reached, as its semantic twin above is: a `dim_text`
// in herdr-draft's own `[palette]`, or `subtext0` in herdr's
// `[theme.custom]`, goes through the same clamp a builtin does.
func TestLoadHerdrPalette_FloorsAnIllegibleDimTextOverride(t *testing.T) {
	illegible := "#191927" // a hair off catppuccin's #181825 PanelBG

	got := floorContrast(Resolve(Default(), map[string]string{"dim_text": illegible}))

	if colorEqual(got.DimText, lipgloss.Color(illegible)) {
		t.Fatalf("an illegible [palette] dim_text survived unfloored: %v", got.DimText)
	}
	for _, ground := range dimTextGrounds(got) {
		ratio, ok := contrastRatio(got.DimText, ground.value)
		if !ok {
			t.Fatalf("floored DimText is unmeasurable on %s", ground.where)
		}
		if ratio < DimTextContrastFloor-contrastAssertionEpsilon {
			t.Errorf("floored override is %.3f:1 on %s, want >= %.2f:1", ratio, ground.where, DimTextContrastFloor)
		}
	}
	if outContrasts(got.DimText, got.Text, []Color{got.PanelBG, got.ActiveRowBG, got.SurfaceFill(got.PanelBG)}) {
		t.Errorf("floored override %v is louder than Text %v", got.DimText, got.Text)
	}
}

// TestFloorContrast_MeasuresDimTextAgainstTheRaisedActiveRowBG is
// TestFloorContrast_MeasuresAgainstTheRaisedActiveRowBG for the dim tier:
// DimText is measured on the focused row's fill as floorContrast RAISES it,
// not on the selection_bg it started as. The fixture is that test's, with a
// grey dim tier in place of its red: #777777 clears the floor on the raw
// #242424 and fails on the #363636 it is raised to.
func TestFloorContrast_MeasuresDimTextAgainstTheRaisedActiveRowBG(t *testing.T) {
	raw := Palette{
		PanelBG:     lipgloss.Color("#202020"),
		Text:        lipgloss.Color("#ffffff"),
		Surface:     lipgloss.Color("#000000"), // clears SurfaceFillContrastFloor unaided
		ActiveRowBG: lipgloss.Color("#242424"), // 1.05:1, well under ActiveRowContrastFloor
		DimText:     lipgloss.Color("#777777"),
	}

	if ratio, _ := contrastRatio(raw.DimText, raw.ActiveRowBG); ratio < DimTextContrastFloor {
		t.Fatalf("fixture DimText is %.3f:1 on the RAW fill -- it must pass there for the wrong order to be silent", ratio)
	}
	// The raised fill has to be the ONLY ground that can raise it -- the
	// guard #149 taught the semantic version of this test to carry.
	for _, ground := range []Color{raw.PanelBG, raw.SurfaceFill(raw.PanelBG)} {
		if ratio, _ := contrastRatio(raw.DimText, ground); ratio < DimTextContrastFloor {
			t.Fatalf("fixture DimText is %.3f:1 on %v -- it must clear every ground but the focused fill, or this test is not about ordering", ratio, ground)
		}
	}

	got := floorContrast(raw)

	if ratio, _ := contrastRatio(raw.DimText, got.ActiveRowBG); ratio >= DimTextContrastFloor {
		t.Fatalf("fixture DimText is %.3f:1 on the FLOORED fill -- it must fail there for this test to measure anything", ratio)
	}
	if colorEqual(got.DimText, raw.DimText) {
		t.Errorf("DimText came back unraised: floorContrast measured it against the raw selection_bg, not the fill it floored")
	}
	if ratio, _ := contrastRatio(got.DimText, got.ActiveRowBG); ratio < DimTextContrastFloor-contrastAssertionEpsilon {
		t.Errorf("raised DimText is %.3f:1 on the floored fill, want >= %.2f:1", ratio, DimTextContrastFloor)
	}
}
