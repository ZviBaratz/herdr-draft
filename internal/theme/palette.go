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
// Field mapping (herdr's 19-field Palette -> this package's 11-field
// Palette; herdr:src/app/state.rs:69-108 has the full struct and doc
// comments for context):
//
//	herdr field   draft field   why
//	accent     -> Accent        primary accent / highlight, 1:1.
//	panel_bg   -> PanelBG       background for panels/overlays, 1:1.
//	text       -> Text          main text color, 1:1.
//	subtext0   -> DimText       "subdued text (workspace numbers, dim
//	                            labels)" is exactly what DimText is for.
//	red        -> Danger        herdr's "needs attention / blocked" color.
//	green      -> Success       herdr's "done / idle" color.
//	surface_dim -> Border       herdr documents surface_dim as "very dim
//	                            surface for separators" -- separators are
//	                            what Border draws.
//	surface0   -> Surface       herdr's "subtle surface background for
//	                            selected/focused items"; the v2 form fills
//	                            its secondary button and selected panel row
//	                            with it (v2 spec §7).
//	active_row_bg -> ActiveRowBG
//	                            herdr's "background for the active
//	                            workspace and focused agent rows"; the v2
//	                            form's focused row uses the same fill.
//	peach      -> Warning       herdr's "interrupted / warning states"
//	                            color, used for rate-limited and degraded
//	                            markers.
//	mauve      -> Branch        herdr's own "branch name / special label
//	                            color", used for exactly that here.
//
// herdr's other eight fields (sidebar_bg, selection_bg, surface1, overlay0,
// overlay1, yellow, blue, teal) have no equivalent in this package's smaller
// Palette and are not translated; herdr-draft does not replicate herdr's
// full theming model.
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
	Danger  Color
	Success Color
	Border  Color
	// Surface fills the secondary button and the selected panel row.
	Surface Color
	// ActiveRowBG fills the focused row, full width (v2 spec §7).
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
// alias herdr accepts.
var builtinPalettes = map[string]Palette{
	"catppuccin": {
		Accent: hex("#89b4fa"), PanelBG: hex("#181825"), Text: hex("#cdd6f4"),
		DimText: hex("#a6adc8"), Danger: hex("#f38ba8"), Success: hex("#a6e3a1"),
		Border: hex("#1e1e2e"), Surface: hex("#313244"), ActiveRowBG: hex("#1e1e2e"),
		Warning: hex("#fab387"), Branch: hex("#cba6f7"),
	},
	"catppuccin-latte": {
		Accent: hex("#1e66f5"), PanelBG: hex("#eff1f5"), Text: hex("#4c4f69"),
		DimText: hex("#6c6f85"), Danger: hex("#d20f39"), Success: hex("#40a02b"),
		Border: hex("#e6e9ef"), Surface: hex("#ccd0da"), ActiveRowBG: hex("#e6e9ef"),
		Warning: hex("#fe640b"), Branch: hex("#8839ef"),
	},
	// herdr's terminal() palette uses the host terminal's own ANSI colors
	// (ratatui Color::Blue/Reset/Gray/... ) rather than fixed RGB, so there
	// is no single correct hex translation -- these are the standard xterm
	// 16-color defaults (ANSI indices 2 "green", 3 "yellow", 4 "blue", 7
	// "white", 8 "bright black", 9 "bright red"), a best-effort
	// approximation. panel_bg, text and surface0 use herdr's Color::Reset,
	// translated to lipgloss.NoColor{} (no foreground/background override),
	// the closest analog to "inherit the terminal's own color".
	"terminal": {
		Accent: hex("#0000ee"), PanelBG: lipgloss.NoColor{}, Text: lipgloss.NoColor{},
		DimText: hex("#e5e5e5"), Danger: hex("#ff0000"), Success: hex("#00cd00"),
		Border: hex("#7f7f7f"), Surface: lipgloss.NoColor{}, ActiveRowBG: hex("#7f7f7f"),
		Warning: hex("#cdcd00"), Branch: hex("#e5e5e5"),
	},
	"tokyo-night": {
		Accent: hex("#7aa2f7"), PanelBG: hex("#1a1b26"), Text: hex("#c0caf5"),
		DimText: hex("#a9b1d6"), Danger: hex("#f7768e"), Success: hex("#9ece6a"),
		Border: hex("#1a1b26"), Surface: hex("#24283b"), ActiveRowBG: hex("#232636"),
		Warning: hex("#ff9e64"), Branch: hex("#bb9af7"),
	},
	"tokyo-night-day": {
		Accent: hex("#2e7de9"), PanelBG: hex("#e1e2e7"), Text: hex("#3760bf"),
		DimText: hex("#6172b0"), Danger: hex("#f52a65"), Success: hex("#587539"),
		Border: hex("#d2d3da"), Surface: hex("#c4c8da"), ActiveRowBG: hex("#d2d3da"),
		Warning: hex("#b15c00"), Branch: hex("#7847bd"),
	},
	"dracula": {
		Accent: hex("#bd93f9"), PanelBG: hex("#282a36"), Text: hex("#f8f8f2"),
		DimText: hex("#d2d2dc"), Danger: hex("#ff5555"), Success: hex("#50fa7b"),
		Border: hex("#282a36"), Surface: hex("#44475a"), ActiveRowBG: hex("#373c52"),
		Warning: hex("#ffb86c"), Branch: hex("#ff79c6"),
	},
	"nord": {
		Accent: hex("#88c0d0"), PanelBG: hex("#2e3440"), Text: hex("#eceff4"),
		DimText: hex("#d8dee9"), Danger: hex("#bf616a"), Success: hex("#a3be8c"),
		Border: hex("#2e3440"), Surface: hex("#3b4252"), ActiveRowBG: hex("#434c5e"),
		Warning: hex("#d08770"), Branch: hex("#b48ead"),
	},
	"gruvbox": {
		Accent: hex("#d79921"), PanelBG: hex("#282828"), Text: hex("#ebdbb2"),
		DimText: hex("#d5c4a1"), Danger: hex("#fb4934"), Success: hex("#b8bb26"),
		Border: hex("#282828"), Surface: hex("#3c3836"), ActiveRowBG: hex("#323130"),
		Warning: hex("#fe8019"), Branch: hex("#d3869b"),
	},
	"gruvbox-light": {
		Accent: hex("#076678"), PanelBG: hex("#fbf1c7"), Text: hex("#3c3836"),
		DimText: hex("#504945"), Danger: hex("#9d0006"), Success: hex("#79740e"),
		Border: hex("#f2e5bc"), Surface: hex("#ebdbb2"), ActiveRowBG: hex("#f2e5bc"),
		Warning: hex("#af3a03"), Branch: hex("#8f3f71"),
	},
	"one-dark": {
		Accent: hex("#61afef"), PanelBG: hex("#282c34"), Text: hex("#abb2bf"),
		DimText: hex("#969ca8"), Danger: hex("#e06c75"), Success: hex("#98c379"),
		Border: hex("#282c34"), Surface: hex("#2c313a"), ActiveRowBG: hex("#313640"),
		Warning: hex("#d19a66"), Branch: hex("#c678dd"),
	},
	"one-light": {
		Accent: hex("#4078f2"), PanelBG: hex("#fafafa"), Text: hex("#383a42"),
		DimText: hex("#686b77"), Danger: hex("#e45649"), Success: hex("#50a14f"),
		Border: hex("#f5f5f6"), Surface: hex("#f0f0f1"), ActiveRowBG: hex("#d8dbe2"),
		Warning: hex("#986801"), Branch: hex("#a626a4"),
	},
	"solarized": {
		Accent: hex("#268bd2"), PanelBG: hex("#002b36"), Text: hex("#93a1a1"),
		DimText: hex("#839496"), Danger: hex("#dc322f"), Success: hex("#859900"),
		Border: hex("#002b36"), Surface: hex("#073642"), ActiveRowBG: hex("#164b57"),
		Warning: hex("#cb4b16"), Branch: hex("#d33682"),
	},
	"solarized-light": {
		Accent: hex("#268bd2"), PanelBG: hex("#fdf6e3"), Text: hex("#657b83"),
		DimText: hex("#839496"), Danger: hex("#dc322f"), Success: hex("#859900"),
		Border: hex("#eee8d5"), Surface: hex("#eee8d5"), ActiveRowBG: hex("#eee8d5"),
		Warning: hex("#cb4b16"), Branch: hex("#d33682"),
	},
	"kanagawa": {
		Accent: hex("#7e9cd8"), PanelBG: hex("#1f1f28"), Text: hex("#dcd7ba"),
		DimText: hex("#c8c3aa"), Danger: hex("#c34043"), Success: hex("#76946a"),
		Border: hex("#1f1f28"), Surface: hex("#2a2a37"), ActiveRowBG: hex("#363646"),
		Warning: hex("#ffa066"), Branch: hex("#957fb8"),
	},
	"kanagawa-lotus": {
		Accent: hex("#4d699b"), PanelBG: hex("#f2ecbc"), Text: hex("#545464"),
		DimText: hex("#43436c"), Danger: hex("#c84053"), Success: hex("#6f894e"),
		Border: hex("#d5cea3"), Surface: hex("#dcd5ac"), ActiveRowBG: hex("#d5cea3"),
		Warning: hex("#cc6d00"), Branch: hex("#624c83"),
	},
	"rose-pine": {
		Accent: hex("#c4a7e7"), PanelBG: hex("#191724"), Text: hex("#e0def4"),
		DimText: hex("#c8c5dc"), Danger: hex("#eb6f92"), Success: hex("#31748f"),
		Border: hex("#26233a"), Surface: hex("#1f1d2e"), ActiveRowBG: hex("#26233a"),
		Warning: hex("#ea9a97"), Branch: hex("#c4a7e7"),
	},
	"rose-pine-dawn": {
		Accent: hex("#907aa9"), PanelBG: hex("#faf4ed"), Text: hex("#464261"),
		DimText: hex("#797593"), Danger: hex("#b4637a"), Success: hex("#286983"),
		Border: hex("#f2e9e1"), Surface: hex("#f2e9e1"), ActiveRowBG: hex("#e3d9cf"),
		Warning: hex("#d7827e"), Branch: hex("#907aa9"),
	},
	"vesper": {
		Accent: hex("#ffc799"), PanelBG: hex("#1a1a1a"), Text: hex("#ffffff"),
		DimText: hex("#a0a0a0"), Danger: hex("#ff8080"), Success: hex("#99ffe4"),
		Border: hex("#101010"), Surface: hex("#232323"), ActiveRowBG: hex("#101010"),
		Warning: hex("#ffc799"), Branch: hex("#ffd1a8"),
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
func Builtin(name string) (Palette, bool) {
	canonical, ok := canonicalThemeName(name)
	if !ok {
		return Palette{}, false
	}
	palette, ok := builtinPalettes[canonical]
	return palette, ok
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
	case "danger":
		p.Danger = c
	case "success":
		p.Success = c
	case "border":
		p.Border = c
	case "surface":
		p.Surface = c
	case "active_row_bg", "activerowbg":
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

// herdrThemeCustom mirrors the subset of herdr's CustomThemeColors
// (/home/zvi/Projects/herdr/src/config/theme.rs:102-126) that maps onto this
// package's smaller Palette. herdr's other eight [theme.custom] keys
// (sidebar_bg, selection_bg, surface1, overlay0, overlay1, yellow, blue,
// teal) and the nested [theme.custom.light]/[theme.custom.dark]
// per-appearance override tables have no equivalent field here and are
// intentionally not decoded.
type herdrThemeCustom struct {
	Accent     string `toml:"accent"`
	PanelBG    string `toml:"panel_bg"`
	Text       string `toml:"text"`
	Subtext0   string `toml:"subtext0"`
	Red        string `toml:"red"`
	Green      string `toml:"green"`
	SurfaceDim string `toml:"surface_dim"`
	// Decoded since the v2 palette gained a draft field for each of these
	// (spec §7); before that they were real herdr keys with no draft
	// equivalent, so leaving them out would now mean a user who customizes
	// herdr's mauve gets herdr's color for branches and ours for the same
	// branches.
	Surface0    string `toml:"surface0"`
	ActiveRowBG string `toml:"active_row_bg"`
	Peach       string `toml:"peach"`
	Mauve       string `toml:"mauve"`
}

// toOverrides translates herdr's [theme.custom] keys into the draft field
// names Resolve understands, per the package doc's field mapping. Only keys
// actually present in the config are included, so Resolve leaves the rest
// of the base palette untouched.
func (c herdrThemeCustom) toOverrides() map[string]string {
	m := make(map[string]string, 11)
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
	if c.ActiveRowBG != "" {
		m["active_row_bg"] = c.ActiveRowBG
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
	return base
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
