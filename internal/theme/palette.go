// Package theme resolves a herdr-draft color palette from herdr's own theme
// configuration.
//
// herdr-draft has no interest in inventing its own color scheme: it lives
// inside herdr's popup PTY, so its palette should feel like a natural
// extension of whatever theme the surrounding herdr session is using. This
// package translates herdr's built-in named palettes and reads herdr's own
// config.toml to figure out which one (plus overrides) is active, then lets
// herdr-draft's own config layer add one more override on top.
//
// Attribution: the color constants in builtinPalettes below are translated
// from herdr's Palette constructors
// (/home/zvi/Projects/herdr/src/app/state.rs, `impl Palette`, ~line 110
// onward; `Palette::from_name`, ~line 562), at herdr commit b1ff4582
// (b1ff4582e9688f52ffb943cfa8bee4871ae122e4). herdr is licensed under the
// Apache License, Version 2.0; see this repository's NOTICE file.
//
// Field mapping (herdr's 19-field Palette -> this package's 12-field
// Palette; herdr:src/app/state.rs:69-108 has the full struct and doc
// comments for context):
//
//	herdr field   draft field   why
//	accent     -> Accent        primary accent / highlight, 1:1.
//	panel_bg   -> PanelBG       background for panels/overlays, 1:1.
//	text       -> Text          main text color, 1:1.
//	subtext0   -> DimText       "subdued text (workspace numbers, dim
//	                            labels)" is exactly what DimText is for.
//	overlay0   -> Overlay0      herdr's "muted text (secondary info,
//	                            numbers)". It is the rule stroke and the
//	                            middle text tier (v3 spec §5.2); see the
//	                            contrast note below for why the rule is
//	                            drawn with it rather than with Border.
//	red        -> Danger        herdr's "needs attention / blocked" color.
//	green      -> Success       herdr's "done / idle" color.
//	surface_dim -> Border       herdr documents surface_dim as "very dim
//	                            surface for separators", and draws its own
//	                            scrollbar TRACK with it
//	                            (herdr:src/ui/scrollbar.rs:155-157) -- a
//	                            deliberately near-invisible fill, which is
//	                            what Border is for here too.
//	surface0   -> Surface       herdr's "subtle surface background for
//	                            selected/focused items"; the v2 form fills
//	                            its secondary button and selected panel row
//	                            with it (v2 spec §7).
//	selection_bg -> ActiveRowBG
//	                            herdr's "background for the Navigate-mode
//	                            cursor row in the sidebar" -- our focused
//	                            row is exactly that keyboard cursor. v2
//	                            used active_row_bg, which marks herdr's
//	                            *active workspace* against sidebar_bg, a
//	                            different thing (v3 spec §5.1).
//	peach      -> Warning       herdr's "interrupted / warning states"
//	                            color, used for rate-limited and degraded
//	                            markers.
//	mauve      -> Branch        herdr's own "branch name / special label
//	                            color", used for exactly that here.
//
// herdr's other seven fields (sidebar_bg, active_row_bg, surface1, overlay1,
// yellow, blue, teal) have no field of their own in this package's smaller
// Palette; herdr-draft does not replicate herdr's full theming model.
//
// Contrast, and why the mapping is not just "whatever herdr calls it"
// (v3 spec §5.3, enforced by contrast_test.go):
//
//   - The rule stroke needs to be visible on every builtin. surface1 is
//     what herdr draws its own dialog separator with
//     (herdr:src/ui/dialogs.rs:473), which makes it the obvious candidate,
//     but measured across all seventeen RGB builtins it bottoms out at
//     1.05:1 against panel_bg (rose-pine-dawn) and lands under 1.6:1 in
//     nine of them -- no better than the surface_dim it would replace.
//     overlay0's worst case is 1.69:1 (nord). So the rule is Overlay0's
//     job, and Border keeps surface_dim for the fill it is actually right
//     for. That also mirrors herdr's scrollbar exactly: track surface_dim,
//     thumb overlay0.
//
//   - The focused row has no such field. Every candidate fails somewhere:
//     selection_bg is under 1.25:1 on gruvbox-light (1.21), kanagawa-lotus
//     (1.24), rose-pine-dawn (1.10) and vesper (1.11). Rather than
//     hand-editing four table entries -- which would leave a user's own
//     override unfloored -- ActiveRowBG is translated straight and then
//     raised to the floor at load time by ensureContrast. See its doc
//     comment.
//
// Resolution order (see Builtin, Resolve, LoadHerdrPalette,
// LoadHerdrPaletteFrom below): herdr's `[theme] name` selects a Builtin
// palette; herdr's `[theme.custom]` table overrides individual fields on
// top of it; herdr-draft's own draftOverrides (its `[palette]` config
// table, spec §12) is applied last and wins any conflict. Any stage that
// fails -- an unknown name, a missing or unparsable config file, an invalid
// override color -- falls back to the previous stage's result, ultimately
// Default(). `auto_switch` and the `terminal` theme both depend on
// information this package can't see from a static config file alone
// (live host light/dark appearance, and the user's own terminal ANSI
// palette, respectively); both resolve best-effort to the configured dark
// variant (`theme.dark_name`, defaulting to "catppuccin") instead. Pixel
// parity with herdr is explicitly not a goal (spec §7).
package theme

import (
	"image/color"
	"math"
	"os"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/BurntSushi/toml"
)

// Color is the color type used by Palette's fields: the standard library
// interface lipgloss v2's Style setters (Foreground, Background, ...)
// accept, and that lipgloss.Color(s) constructs. lipgloss v2 replaced v1's
// `type Color string` with a `func Color(s string) color.Color`
// constructor, so "lipgloss.Color" can no longer name a type; this alias is
// the closest equivalent and stays interchangeable with lipgloss.Color(...)
// and lipgloss Style methods without any conversion.
type Color = color.Color

// Palette is herdr-draft's small color palette, translated from a herdr
// theme. See the package doc for the herdr-field-to-draft-field mapping.
type Palette struct {
	Accent  Color
	PanelBG Color
	Text    Color
	DimText Color
	// Overlay0 draws the rules and is the middle text tier -- panel column
	// headings, badges, the scrollbar thumb. Without it the palette jumps
	// straight from Surface (1.40:1 in catppuccin) to DimText (7.89:1),
	// which is why a panel row reads as one flat wall of same-weight text
	// (v3 spec §5.2). It is the only Palette field with a guaranteed
	// contrast floor against PanelBG that holds for every builtin as
	// written, which is what makes it the rule stroke.
	Overlay0 Color
	Danger   Color
	Success  Color
	// Border is a deliberately near-invisible fill, herdr's surface_dim:
	// the scrollbar track, not the rules. Reach for Overlay0 to draw a line
	// somebody has to see.
	Border Color
	// Surface fills the secondary button and the selected panel row.
	Surface Color
	// ActiveRowBG fills the focused row, full width (v2 spec §7) -- one of
	// the three signals v3 spec §5.4 gives it. Every palette handed out by
	// this package has had it raised to the v3 spec §5.3 contrast floor by
	// ensureContrast; the builtinPalettes table below holds the unfloored
	// translation.
	ActiveRowBG Color
	// Warning marks rate-limited and degraded states -- distinct from
	// Danger, which is reserved for outright failure.
	Warning Color
	// Branch colors branch names, which herdr colors distinctly from
	// ordinary text.
	Branch Color
}

// hex builds a Color from a "#RGB" or "#RRGGBB" literal. It is only used for
// this file's own builtin color tables, which are known-good at compile
// time; user-supplied strings go through parseHexColor instead, which
// reports success/failure rather than trusting the input.
func hex(s string) Color { return lipgloss.Color(s) }

// builtinPalettes holds every palette herdr's Palette::from_name accepts,
// translated per the package doc's field mapping. Keys are canonical theme
// names (see canonicalThemeName); use Builtin to look one up by any name or
// alias herdr accepts -- not this map directly, since ActiveRowBG here is
// the raw translation, before ensureContrast raises it to its floor.
//
// One value is not a straight translation: terminal's ActiveRowBG. See the
// note on that entry.
var builtinPalettes = map[string]Palette{
	"catppuccin": {
		Accent: hex("#89b4fa"), PanelBG: hex("#181825"), Text: hex("#cdd6f4"),
		DimText: hex("#a6adc8"), Overlay0: hex("#6c7086"), Danger: hex("#f38ba8"),
		Success: hex("#a6e3a1"), Border: hex("#1e1e2e"), Surface: hex("#313244"),
		ActiveRowBG: hex("#313244"), Warning: hex("#fab387"), Branch: hex("#cba6f7"),
	},
	"catppuccin-latte": {
		Accent: hex("#1e66f5"), PanelBG: hex("#eff1f5"), Text: hex("#4c4f69"),
		DimText: hex("#6c6f85"), Overlay0: hex("#9ca0b0"), Danger: hex("#d20f39"),
		Success: hex("#40a02b"), Border: hex("#e6e9ef"), Surface: hex("#ccd0da"),
		ActiveRowBG: hex("#bdd0f5"), Warning: hex("#fe640b"), Branch: hex("#8839ef"),
	},
	// herdr's terminal() palette uses the host terminal's own ANSI colors
	// (ratatui Color::Blue/Reset/Gray/... ) rather than fixed RGB, so there
	// is no single correct hex translation -- these are the standard xterm
	// 16-color defaults, a best-effort approximation: Color::Blue #0000ee
	// (ANSI 4), Color::Green #00cd00 (2), Color::Yellow #cdcd00 (3),
	// Color::Gray #e5e5e5 (7), Color::DarkGray #7f7f7f (8), Color::LightRed
	// #ff0000 (9). Overlay0 is overlay0's Color::Gray and Border is
	// surface_dim's Color::DarkGray. panel_bg, text and surface0 use herdr's
	// Color::Reset, translated to lipgloss.NoColor{} (no foreground/
	// background override), the closest analog to "inherit the terminal's
	// own color".
	//
	// ActiveRowBG is the one hand-picked value in this table. herdr's
	// terminal selection_bg is Color::Reset, and a Reset *background* is
	// not a color at all -- it is the absence of a fill, so it cannot mark
	// a row, and ensureContrast cannot raise it either (there is no known
	// panel_bg here to raise it against). It therefore keeps
	// active_row_bg's Color::DarkGray, which is what this palette drew the
	// focused row with before v3 spec §5.1 repointed the field.
	"terminal": {
		Accent: hex("#0000ee"), PanelBG: lipgloss.NoColor{}, Text: lipgloss.NoColor{},
		DimText: hex("#e5e5e5"), Overlay0: hex("#e5e5e5"), Danger: hex("#ff0000"),
		Success: hex("#00cd00"), Border: hex("#7f7f7f"), Surface: lipgloss.NoColor{},
		ActiveRowBG: hex("#7f7f7f"), Warning: hex("#cdcd00"), Branch: hex("#e5e5e5"),
	},
	"tokyo-night": {
		Accent: hex("#7aa2f7"), PanelBG: hex("#1a1b26"), Text: hex("#c0caf5"),
		DimText: hex("#a9b1d6"), Overlay0: hex("#565f89"), Danger: hex("#f7768e"),
		Success: hex("#9ece6a"), Border: hex("#1a1b26"), Surface: hex("#24283b"),
		ActiveRowBG: hex("#2d3650"), Warning: hex("#ff9e64"), Branch: hex("#bb9af7"),
	},
	"tokyo-night-day": {
		Accent: hex("#2e7de9"), PanelBG: hex("#e1e2e7"), Text: hex("#3760bf"),
		DimText: hex("#6172b0"), Overlay0: hex("#8990b3"), Danger: hex("#f52a65"),
		Success: hex("#587539"), Border: hex("#d2d3da"), Surface: hex("#c4c8da"),
		ActiveRowBG: hex("#b6cae7"), Warning: hex("#b15c00"), Branch: hex("#7847bd"),
	},
	"dracula": {
		Accent: hex("#bd93f9"), PanelBG: hex("#282a36"), Text: hex("#f8f8f2"),
		DimText: hex("#d2d2dc"), Overlay0: hex("#6272a4"), Danger: hex("#ff5555"),
		Success: hex("#50fa7b"), Border: hex("#282a36"), Surface: hex("#44475a"),
		ActiveRowBG: hex("#463f5d"), Warning: hex("#ffb86c"), Branch: hex("#ff79c6"),
	},
	"nord": {
		Accent: hex("#88c0d0"), PanelBG: hex("#2e3440"), Text: hex("#eceff4"),
		DimText: hex("#d8dee9"), Overlay0: hex("#4c566a"), Danger: hex("#bf616a"),
		Success: hex("#a3be8c"), Border: hex("#2e3440"), Surface: hex("#3b4252"),
		ActiveRowBG: hex("#40505d"), Warning: hex("#d08770"), Branch: hex("#b48ead"),
	},
	"gruvbox": {
		Accent: hex("#d79921"), PanelBG: hex("#282828"), Text: hex("#ebdbb2"),
		DimText: hex("#d5c4a1"), Overlay0: hex("#928374"), Danger: hex("#fb4934"),
		Success: hex("#b8bb26"), Border: hex("#282828"), Surface: hex("#3c3836"),
		ActiveRowBG: hex("#4b3f27"), Warning: hex("#fe8019"), Branch: hex("#d3869b"),
	},
	"gruvbox-light": {
		Accent: hex("#076678"), PanelBG: hex("#fbf1c7"), Text: hex("#3c3836"),
		DimText: hex("#504945"), Overlay0: hex("#928374"), Danger: hex("#9d0006"),
		Success: hex("#79740e"), Border: hex("#f2e5bc"), Surface: hex("#ebdbb2"),
		ActiveRowBG: hex("#ebdbb2"), Warning: hex("#af3a03"), Branch: hex("#8f3f71"),
	},
	"one-dark": {
		Accent: hex("#61afef"), PanelBG: hex("#282c34"), Text: hex("#abb2bf"),
		DimText: hex("#969ca8"), Overlay0: hex("#5c6370"), Danger: hex("#e06c75"),
		Success: hex("#98c379"), Border: hex("#282c34"), Surface: hex("#2c313a"),
		ActiveRowBG: hex("#334659"), Warning: hex("#d19a66"), Branch: hex("#c678dd"),
	},
	"one-light": {
		Accent: hex("#4078f2"), PanelBG: hex("#fafafa"), Text: hex("#383a42"),
		DimText: hex("#686b77"), Overlay0: hex("#a0a1a7"), Danger: hex("#e45649"),
		Success: hex("#50a14f"), Border: hex("#f5f5f6"), Surface: hex("#f0f0f1"),
		ActiveRowBG: hex("#cddbf8"), Warning: hex("#986801"), Branch: hex("#a626a4"),
	},
	"solarized": {
		Accent: hex("#268bd2"), PanelBG: hex("#002b36"), Text: hex("#93a1a1"),
		DimText: hex("#839496"), Overlay0: hex("#586e75"), Danger: hex("#dc322f"),
		Success: hex("#859900"), Border: hex("#002b36"), Surface: hex("#073642"),
		ActiveRowBG: hex("#083e55"), Warning: hex("#cb4b16"), Branch: hex("#d33682"),
	},
	"solarized-light": {
		Accent: hex("#268bd2"), PanelBG: hex("#fdf6e3"), Text: hex("#657b83"),
		DimText: hex("#839496"), Overlay0: hex("#93a1a1"), Danger: hex("#dc322f"),
		Success: hex("#859900"), Border: hex("#eee8d5"), Surface: hex("#eee8d5"),
		ActiveRowBG: hex("#c9dcdf"), Warning: hex("#cb4b16"), Branch: hex("#d33682"),
	},
	"kanagawa": {
		Accent: hex("#7e9cd8"), PanelBG: hex("#1f1f28"), Text: hex("#dcd7ba"),
		DimText: hex("#c8c3aa"), Overlay0: hex("#727169"), Danger: hex("#c34043"),
		Success: hex("#76946a"), Border: hex("#1f1f28"), Surface: hex("#2a2a37"),
		ActiveRowBG: hex("#32384b"), Warning: hex("#ffa066"), Branch: hex("#957fb8"),
	},
	"kanagawa-lotus": {
		Accent: hex("#4d699b"), PanelBG: hex("#f2ecbc"), Text: hex("#545464"),
		DimText: hex("#43436c"), Overlay0: hex("#a09cac"), Danger: hex("#c84053"),
		Success: hex("#6f894e"), Border: hex("#d5cea3"), Surface: hex("#dcd5ac"),
		ActiveRowBG: hex("#dcd5ac"), Warning: hex("#cc6d00"), Branch: hex("#624c83"),
	},
	"rose-pine": {
		Accent: hex("#c4a7e7"), PanelBG: hex("#191724"), Text: hex("#e0def4"),
		DimText: hex("#c8c5dc"), Overlay0: hex("#6e6a86"), Danger: hex("#eb6f92"),
		Success: hex("#31748f"), Border: hex("#26233a"), Surface: hex("#1f1d2e"),
		ActiveRowBG: hex("#3b344b"), Warning: hex("#ea9a97"), Branch: hex("#c4a7e7"),
	},
	"rose-pine-dawn": {
		Accent: hex("#907aa9"), PanelBG: hex("#faf4ed"), Text: hex("#464261"),
		DimText: hex("#797593"), Overlay0: hex("#9893a5"), Danger: hex("#b4637a"),
		Success: hex("#286983"), Border: hex("#f2e9e1"), Surface: hex("#f2e9e1"),
		ActiveRowBG: hex("#f2e9e1"), Warning: hex("#d7827e"), Branch: hex("#907aa9"),
	},
	"vesper": {
		Accent: hex("#ffc799"), PanelBG: hex("#1a1a1a"), Text: hex("#ffffff"),
		DimText: hex("#a0a0a0"), Overlay0: hex("#5c5c5c"), Danger: hex("#ff8080"),
		Success: hex("#99ffe4"), Border: hex("#101010"), Surface: hex("#232323"),
		ActiveRowBG: hex("#232323"), Warning: hex("#ffc799"), Branch: hex("#ffd1a8"),
	},
}

// themeAliases maps every name and alias herdr's canonical_theme_name
// accepts (/home/zvi/Projects/herdr/src/config/theme.rs:25-47) to its
// canonical builtinPalettes key.
var themeAliases = map[string]string{
	"catppuccin": "catppuccin", "catppuccin-mocha": "catppuccin",
	"catppuccin-latte": "catppuccin-latte", "latte": "catppuccin-latte", "light": "catppuccin-latte",
	"terminal":    "terminal",
	"tokyo-night": "tokyo-night", "tokyonight": "tokyo-night",
	"tokyo-night-day": "tokyo-night-day", "tokyo-day": "tokyo-night-day", "tokyonight-day": "tokyo-night-day",
	"dracula": "dracula",
	"nord":    "nord",
	"gruvbox": "gruvbox", "gruvbox-dark": "gruvbox",
	"gruvbox-light": "gruvbox-light",
	"one-dark":      "one-dark", "onedark": "one-dark",
	"one-light": "one-light", "onelight": "one-light",
	"solarized": "solarized", "solarized-dark": "solarized",
	"solarized-light": "solarized-light",
	"kanagawa":        "kanagawa",
	"kanagawa-lotus":  "kanagawa-lotus", "lotus": "kanagawa-lotus",
	"rose-pine": "rose-pine", "rosepine": "rose-pine",
	"rose-pine-dawn": "rose-pine-dawn", "rosepine-dawn": "rose-pine-dawn", "dawn": "rose-pine-dawn",
	"vesper": "vesper",
}

// canonicalThemeName normalizes name the way herdr does (lowercase, spaces
// and underscores folded to hyphens; theme.rs:26) and looks it up in
// themeAliases.
func canonicalThemeName(name string) (string, bool) {
	normalized := strings.NewReplacer(" ", "-", "_", "-").Replace(strings.ToLower(strings.TrimSpace(name)))
	canonical, ok := themeAliases[normalized]
	return canonical, ok
}

// Builtin looks up a herdr built-in palette by name, accepting any name or
// alias herdr's Palette::from_name does. It reports false for a name herdr
// itself would not recognize.
//
// The palette it returns has been through floorContrast, so callers get one
// that satisfies v3 spec §5.3 rather than the raw builtinPalettes entry.
func Builtin(name string) (Palette, bool) {
	canonical, ok := canonicalThemeName(name)
	if !ok {
		return Palette{}, false
	}
	palette, ok := builtinPalettes[canonical]
	if !ok {
		return Palette{}, false
	}
	return floorContrast(palette), true
}

// Default returns herdr's default palette: catppuccin
// (herdr:src/app/mod.rs's theme_runtime_config falls back to "catppuccin"
// when `[theme] name` is absent).
func Default() Palette {
	palette, _ := Builtin("catppuccin")
	return palette
}

// applyOverrideKey sets the Palette field named by key, matching Palette's
// field names in snake_case -- the same convention herdr-draft's own
// `[palette]` config table uses (docs/specs/2026-08-31-herdr-draft-design.md
// §12's `accent`/`panel_bg` example keys). Matching is case-insensitive and
// tolerates a missing underscore, since these maps may originate from
// hand-edited TOML. An unrecognized key is a silent no-op.
//
// ActiveRowBG additionally answers to `selection_bg`, its herdr source name
// (v3 spec §5.1) -- a hand-editing user who has herdr's palette in front of
// them is as likely to reach for that as for ours. Note for anyone adding
// another alias: Resolve iterates its override map, so no caller may ever
// emit two keys that reach the same field, or which one wins is
// map-iteration order. toOverrides is the only caller that builds a map
// programmatically, and it emits one key per field.
func applyOverrideKey(p *Palette, key string, c Color) {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "accent":
		p.Accent = c
	case "panel_bg", "panelbg":
		p.PanelBG = c
	case "text":
		p.Text = c
	case "dim_text", "dimtext":
		p.DimText = c
	case "overlay0":
		p.Overlay0 = c
	case "danger":
		p.Danger = c
	case "success":
		p.Success = c
	case "border":
		p.Border = c
	case "surface":
		p.Surface = c
	case "active_row_bg", "activerowbg", "selection_bg", "selectionbg":
		p.ActiveRowBG = c
	case "warning":
		p.Warning = c
	case "branch":
		p.Branch = c
	}
}

// Resolve applies overrides on top of base, one field per recognized key
// (see applyOverrideKey). Each value must be a "#RGB" or "#RRGGBB" hex
// literal; anything else -- an unknown key, a malformed hex string, a named
// color, an ANSI code -- is ignored and that field keeps base's value. A nil
// map is a no-op.
//
// Resolve is a pure merge and deliberately does NOT apply floorContrast: it
// reports what the override tiers actually say, and mixing that with a
// legibility rule would make its result depend on which order a caller
// composed the tiers in. Flooring belongs at the points that hand a finished
// palette to a renderer -- Builtin and LoadHerdrPaletteFrom.
func Resolve(base Palette, overrides map[string]string) Palette {
	result := base
	for key, value := range overrides {
		c, ok := parseHexColor(value)
		if !ok {
			continue
		}
		applyOverrideKey(&result, key, c)
	}
	return result
}

// parseHexColor parses a strict "#RGB" or "#RRGGBB" hex color literal,
// reporting ok=false for anything else. This is intentionally narrower than
// lipgloss.Color's own parsing (which also accepts ANSI decimal codes and
// silently maps anything else to "no color" rather than reporting failure)
// so that Resolve's "ignore invalid" contract is unambiguous.
func parseHexColor(s string) (Color, bool) {
	s = strings.TrimSpace(s)
	if len(s) != 4 && len(s) != 7 {
		return nil, false
	}
	if s[0] != '#' {
		return nil, false
	}
	for _, r := range s[1:] {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return nil, false
		}
	}
	return lipgloss.Color(s), true
}

// ActiveRowContrastFloor is v3 spec §5.3's minimum WCAG contrast ratio
// between the focused row's fill and the panel behind it. It exists because
// the defect it guards is invisible to every other form of verification this
// project has: v2 shipped a focused-row fill at 1.07:1 -- a cursor nobody
// could see -- through fifteen green commits and a full golden-frame suite,
// because frames record bytes and not perceptibility.
//
// §5.3 is explicit that a theme which cannot meet a floor gets a better
// value, not a waiver. Do not lower this to make something pass.
const ActiveRowContrastFloor = 1.25

// InputFillContrastFloor is the minimum WCAG contrast ratio between a text
// input's fill and whatever it is drawn on top of (v3 spec §8.7). It is the
// same number as ActiveRowContrastFloor, for the same reason -- both mark a
// background REGION whose edge the eye has to catch -- but it is its own
// constant so that moving one floor never silently moves the other.
//
// §8.7 does not say what the fill IS; issue #27 does, and it says a flat
// palette.Surface, checked as "catppuccin #1e1e2e/#313244 is clear". Both
// halves of that check are wrong. #1e1e2e is catppuccin's Border, a value
// no input is ever drawn on -- PanelBG is #181825 -- and for three of the
// four inputs the ground is neither: an input is only ever rendered while
// its field is focused, and a focused stack row is filled ActiveRowBG end
// to end before the input's own fill is composited into it.
//
// Measured against the grounds that are actually used, Surface is 1.000:1
// on catppuccin -- the DEFAULT theme, where Surface and ActiveRowBG are the
// byte-identical #313244 -- 1.002:1 on tokyo-night-day and 1.007:1 on
// catppuccin-latte, with six of seventeen builtins under 1.08:1. A flat
// Surface fill would therefore have moved every golden frame and zero
// pixels on the theme most users see, which is v2's invisible-rule defect
// reproduced one field over. See Palette.InputFill.
const InputFillContrastFloor = 1.25

// contrastMixStep is how coarsely ensureContrast walks away from the
// background. It is deliberately coarse: a fine search would land a clamped
// theme a hair over the floor, which is compliant but still marginal, and
// 5% steps are far below the resolution at which a background shift is
// visible anyway. No builtin needs more than four of them.
const contrastMixStep = 0.05

// floorContrast raises the fields v3 spec §5.3 puts a floor under. It runs on
// every palette this package hands a caller (Builtin, and so Default, plus
// LoadHerdrPaletteFrom), which is what lets Palette's own doc promise that
// ActiveRowBG is always floored.
//
// Overlay0 is not floored, and does not need to be: its worst case across
// the builtins is 1.69:1 (nord), so contrast_test.go asserts the 1.6:1 floor
// against the table directly rather than repairing it here. Nothing else has
// a floor -- Border is a deliberately near-invisible fill.
func floorContrast(p Palette) Palette {
	p.ActiveRowBG = ensureContrast(p.PanelBG, p.ActiveRowBG, p.Text, ActiveRowContrastFloor)
	return p
}

// InputFill returns the background a text input should be painted with when
// it is drawn on top of ground (v3 spec §8.7). It is Surface -- herdr's own
// choice for the inputs in its dialogs (herdr:src/ui/dialogs.rs's
// render_name_input_field) -- wherever Surface is actually distinguishable
// from that ground, and Surface raised away from the ground where it is not.
//
// It is a method taking a ground rather than a Palette field because the two
// call sites sit on DIFFERENT grounds and no single value serves both. The
// issue, dir and title inputs render only while their field is focused, so
// they sit on ActiveRowBG; the worktree branch input and the prompt textarea
// live in the panel, so they sit on PanelBG. Surface clears the floor
// against PanelBG on most builtins (it is the pairing herdr itself uses) and
// against ActiveRowBG on almost none -- see InputFillContrastFloor for the
// numbers, and note that catppuccin, the default, is exactly 1.000:1.
//
// The raise is ensureContrast, the same walk §5.3 uses for ActiveRowBG, so
// there is one clamp in this package and not two. It mixes the GROUND toward
// Text, which makes the input a slightly RAISED chip rather than an inset
// well. That direction is deliberate: a well would be painted PanelBG, and a
// PanelBG rectangle inside a focused row lines up vertically with the
// unfocused rows above and below it, so the focused band reads as having a
// hole punched through it rather than as carrying an input.
//
// A Surface or a ground this process cannot measure passes straight through
// untouched, which is the terminal palette: its Surface is herdr's
// Color::Reset, and widgets.PaintLine declines to paint a NoColor at all, so
// that theme keeps inheriting the host terminal's own background exactly as
// it does everywhere else.
func (p Palette) InputFill(ground Color) Color {
	return ensureContrast(ground, p.Surface, p.Text, InputFillContrastFloor)
}

// ensureContrast returns fg when it already meets floor against bg, and
// otherwise bg mixed toward toward until it does -- so an explicit
// selection_bg from a herdr theme, or a user's own `[palette]` override, is
// honored wherever it is legible and only raised where it is not.
//
// Why a mix and not simply a different herdr field: no single field clears
// the focused-row floor on every builtin. selection_bg, the most faithful
// source, is 1.21:1 on gruvbox-light, 1.24:1 on kanagawa-lotus, 1.10:1 on
// rose-pine-dawn and 1.11:1 on vesper. Hand-editing those four table entries
// would floor the table and leave a user's own override unfloored, which is
// the case that matters more. Walking from the panel background toward the
// theme's own text color also keeps the result inside the theme's range: the
// four raises it makes land between 1.28:1 and 1.34:1, and a 100% mix could
// only ever arrive at Text.
//
// An unmeasurable bg, fg or toward returns fg untouched rather than mixed.
// That is the terminal palette, whose panel_bg is herdr's Color::Reset:
// "inherit the host terminal's background" is a value this process cannot
// know, so there is no ratio to compute and nothing honest to raise it to.
func ensureContrast(bg, fg, toward Color, floor float64) Color {
	bgR, bgG, bgB, bgOK := rgb8(bg)
	towardR, towardG, towardB, towardOK := rgb8(toward)
	if _, _, _, fgOK := rgb8(fg); !bgOK || !towardOK || !fgOK {
		return fg
	}

	if ratio, ok := contrastRatio(fg, bg); ok && ratio >= floor {
		return fg
	}

	for f := contrastMixStep; f < 1; f += contrastMixStep {
		mixed := color.RGBA{
			R: mixChannel(bgR, towardR, f),
			G: mixChannel(bgG, towardG, f),
			B: mixChannel(bgB, towardB, f),
			A: 0xff,
		}
		if ratio, ok := contrastRatio(mixed, bg); ok && ratio >= floor {
			return mixed
		}
	}
	return toward
}

// mixChannel interpolates one sRGB channel, in sRGB space rather than linear
// light. That is the wrong space for blending photographs and the right one
// here: the point is to step along the ramp a theme author would have picked
// values from, not to model physical light.
func mixChannel(from, to uint8, f float64) uint8 {
	return uint8(math.Round(float64(from) + f*(float64(to)-float64(from))))
}

// contrastRatio is WCAG's (Lmax+0.05)/(Lmin+0.05) -- symmetric, and never
// below 1. It reports ok=false when either color is unmeasurable (see rgb8),
// because there is no defensible number to return in that case.
func contrastRatio(a, b Color) (float64, bool) {
	ar, ag, ab, aOK := rgb8(a)
	br, bg, bb, bOK := rgb8(b)
	if !aOK || !bOK {
		return 0, false
	}
	la, lb := relativeLuminance(ar, ag, ab), relativeLuminance(br, bg, bb)
	return (math.Max(la, lb) + 0.05) / (math.Min(la, lb) + 0.05), true
}

// relativeLuminance is the WCAG 2.x definition: linearize each sRGB channel,
// then weight them for the eye's sensitivity
// (https://www.w3.org/TR/WCAG22/#dfn-relative-luminance).
func relativeLuminance(r, g, b uint8) float64 {
	return 0.2126*linearizeChannel(r) +
		0.7152*linearizeChannel(g) +
		0.0722*linearizeChannel(b)
}

func linearizeChannel(v uint8) float64 {
	c := float64(v) / 255
	if c <= 0.03928 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

// rgb8 recovers a Color's 8-bit sRGB channels, reporting ok=false for a value
// there is nothing to measure. color.Color.RGBA reports 16-bit
// alpha-premultiplied channels, and every measurable Palette value is opaque
// with each byte replicated (0x89 becomes 0x8989), which makes >>8 exact.
//
// The unmeasurable case is lipgloss.NoColor{} -- herdr's Color::Reset,
// "inherit the terminal's own color" -- and it has to be recognized by type,
// because its RGBA reports opaque black, which no arithmetic can tell from a
// real #000000. Nothing else reaches a Palette field here: builtinPalettes
// holds hex literals and NoColor, and every override goes through
// parseHexColor, which accepts hex and nothing else. nil and a translucent
// value are rejected too, so a future source of either surfaces as a
// contrast check that declines to answer rather than one that answers wrong.
func rgb8(c Color) (r, g, b uint8, ok bool) {
	if c == nil {
		return 0, 0, 0, false
	}
	if _, isNoColor := c.(lipgloss.NoColor); isNoColor {
		return 0, 0, 0, false
	}
	cr, cg, cb, ca := c.RGBA()
	if ca != 0xffff {
		return 0, 0, 0, false
	}
	return uint8(cr >> 8), uint8(cg >> 8), uint8(cb >> 8), true
}

// herdrThemeCustom mirrors the subset of herdr's CustomThemeColors
// (/home/zvi/Projects/herdr/src/config/theme.rs:102-126) that maps onto this
// package's smaller Palette. herdr's other seven [theme.custom] keys
// (sidebar_bg, active_row_bg, surface1, overlay1, yellow, blue, teal) and
// the nested [theme.custom.light]/[theme.custom.dark] per-appearance
// override tables have no equivalent field here and are intentionally not
// decoded.
//
// active_row_bg was decoded until v3 spec §5.1 moved ActiveRowBG onto
// selection_bg. It is deliberately gone rather than kept alongside its
// replacement: both would write the same field, and Resolve iterates a map,
// so a user who set both keys would get whichever the runtime happened to
// visit last. Honoring it would also undo the remapping -- it would repaint
// our keyboard cursor with herdr's active-workspace color.
type herdrThemeCustom struct {
	Accent     string `toml:"accent"`
	PanelBG    string `toml:"panel_bg"`
	Text       string `toml:"text"`
	Subtext0   string `toml:"subtext0"`
	Red        string `toml:"red"`
	Green      string `toml:"green"`
	SurfaceDim string `toml:"surface_dim"`
	// Decoded since the v2 palette gained a draft field for each of these
	// (v2 spec §7's "four palette fields are added" table: surface0,
	// active_row_bg, peach, mauve); before that they were real herdr keys
	// with no draft equivalent, so leaving them out would now mean a user who customizes
	// herdr's mauve gets herdr's color for branches and ours for the same
	// branches.
	Surface0 string `toml:"surface0"`
	Peach    string `toml:"peach"`
	Mauve    string `toml:"mauve"`
	// Decoded for the same reason one round later: v3 spec §5.1-§5.2 pointed
	// ActiveRowBG at selection_bg and added Overlay0.
	SelectionBG string `toml:"selection_bg"`
	Overlay0    string `toml:"overlay0"`
}

// toOverrides translates herdr's [theme.custom] keys into the draft field
// names Resolve understands, per the package doc's field mapping. Only keys
// actually present in the config are included, so Resolve leaves the rest
// of the base palette untouched.
func (c herdrThemeCustom) toOverrides() map[string]string {
	m := make(map[string]string, 12)
	if c.Accent != "" {
		m["accent"] = c.Accent
	}
	if c.PanelBG != "" {
		m["panel_bg"] = c.PanelBG
	}
	if c.Text != "" {
		m["text"] = c.Text
	}
	if c.Subtext0 != "" {
		m["dim_text"] = c.Subtext0
	}
	if c.Overlay0 != "" {
		m["overlay0"] = c.Overlay0
	}
	if c.Red != "" {
		m["danger"] = c.Red
	}
	if c.Green != "" {
		m["success"] = c.Green
	}
	if c.SurfaceDim != "" {
		m["border"] = c.SurfaceDim
	}
	if c.Surface0 != "" {
		m["surface"] = c.Surface0
	}
	if c.SelectionBG != "" {
		m["active_row_bg"] = c.SelectionBG
	}
	if c.Peach != "" {
		m["warning"] = c.Peach
	}
	if c.Mauve != "" {
		m["branch"] = c.Mauve
	}
	return m
}

// herdrThemeConfig mirrors the subset of herdr's ThemeConfig
// (/home/zvi/Projects/herdr/src/config/theme.rs:61-72) this package reads
// from herdr's own config.toml.
type herdrThemeConfig struct {
	Name       string `toml:"name"`
	AutoSwitch bool   `toml:"auto_switch"`
	DarkName   string `toml:"dark_name"`
	// LightName is decoded for completeness (it's a real herdr config key)
	// but unused: this package's auto_switch handling always resolves to
	// the dark variant, best-effort, per the package doc.
	LightName string           `toml:"light_name"`
	Custom    herdrThemeCustom `toml:"custom"`
}

type herdrConfig struct {
	Theme herdrThemeConfig `toml:"theme"`
}

// herdrConfigPath resolves herdr's own config.toml path on this machine:
// ${XDG_CONFIG_HOME:-~/.config}/herdr/config.toml
// (/home/zvi/Projects/herdr/src/config/io.rs:30-35,62-68,173). It returns
// "" when neither XDG_CONFIG_HOME nor the user's home directory can be
// determined, in which case LoadHerdrPalette falls back to Default().
func herdrConfigPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "herdr", "config.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "herdr", "config.toml")
}

// LoadHerdrPalette resolves the active palette from herdr's own config.toml
// (found via herdrConfigPath) with draftOverrides -- herdr-draft's own
// `[palette]` config table -- applied last. See the package doc for the
// full resolution order and fallback rules.
func LoadHerdrPalette(draftOverrides map[string]string) Palette {
	return LoadHerdrPaletteFrom(herdrConfigPath(), draftOverrides)
}

// LoadHerdrPaletteFrom is LoadHerdrPalette with an explicit config path,
// for tests. A missing file, an unreadable file, or a file that fails to
// parse as TOML all fall back to Default() for the builtin-selection stage;
// an unknown `[theme] name` does likewise. See the package doc for the full
// resolution order.
func LoadHerdrPaletteFrom(path string, draftOverrides map[string]string) Palette {
	base := Default()

	var cfg herdrConfig
	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			if _, err := toml.Decode(string(data), &cfg); err != nil {
				cfg = herdrConfig{}
			} else if palette, ok := resolveBuiltinFromConfig(cfg.Theme); ok {
				base = palette
			}
		}
	}

	base = Resolve(base, cfg.Theme.Custom.toOverrides())
	base = Resolve(base, draftOverrides)
	// Floor last, after every tier has had its say, so a user's own
	// active_row_bg goes through the same check a builtin's does (v3 spec
	// §5.3). Builtin already floored the base; doing it again is a no-op on
	// a value that already clears the floor.
	return floorContrast(base)
}

// resolveBuiltinFromConfig picks the Builtin palette selected by a herdr
// [theme] table. auto_switch and name = "terminal" both depend on
// information a static config file can't provide (live host appearance,
// and the user's own terminal ANSI colors); both resolve best-effort to
// theme.dark_name (defaulting to "catppuccin") instead -- see the package
// doc's resolution-order section.
func resolveBuiltinFromConfig(theme herdrThemeConfig) (Palette, bool) {
	name := theme.Name
	if name == "" {
		name = "catppuccin"
	}

	canonical, known := canonicalThemeName(name)
	needsDarkFallback := theme.AutoSwitch || (known && canonical == "terminal")
	if needsDarkFallback {
		name = theme.DarkName
		if name == "" {
			name = "catppuccin"
		}
	}

	return Builtin(name)
}
