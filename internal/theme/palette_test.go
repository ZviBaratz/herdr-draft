package theme

import (
	"image/color"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// colorEqual compares two color.Color values by their resolved RGBA output,
// which is the only thing color.Color guarantees -- Palette fields are built
// from a mix of lipgloss.Color("#hex") (RGBColor) and lipgloss.NoColor{}, so
// comparing concrete types directly would be brittle.
func colorEqual(a, b color.Color) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}

func TestResolve_MergesValidOverrideAndIgnoresInvalid(t *testing.T) {
	base := Default()
	overrides := map[string]string{
		"accent": "#123456",
		"danger": "not-a-color",
	}

	got := Resolve(base, overrides)

	want := parseHexColorForTest(t, "#123456")
	if !colorEqual(got.Accent, want) {
		t.Errorf("Accent = %v, want %v (from valid override)", got.Accent, want)
	}
	if !colorEqual(got.Danger, base.Danger) {
		t.Errorf("Danger = %v, want unchanged base %v (invalid override must be ignored)", got.Danger, base.Danger)
	}
}

func TestResolve_UnknownKeysAreIgnored(t *testing.T) {
	base := Default()
	got := Resolve(base, map[string]string{"not_a_field": "#ffffff"})
	if !samePalette(got, base) {
		t.Errorf("Resolve with unknown key changed the palette: got %+v, want unchanged %+v", got, base)
	}
}

// TestResolve_V2PaletteKeys covers the four fields the v2 form added
// (spec §7): each must be reachable from a `[palette]` override, and
// active_row_bg must accept the underscore-optional spelling the rest of
// applyOverrideKey's multi-word keys do.
func TestResolve_V2PaletteKeys(t *testing.T) {
	cases := []struct {
		key string
		get func(Palette) Color
	}{
		{"surface", func(p Palette) Color { return p.Surface }},
		{"active_row_bg", func(p Palette) Color { return p.ActiveRowBG }},
		{"activerowbg", func(p Palette) Color { return p.ActiveRowBG }},
		{"warning", func(p Palette) Color { return p.Warning }},
		{"branch", func(p Palette) Color { return p.Branch }},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			base := Default()
			want := parseHexColorForTest(t, "#123456")

			got := Resolve(base, map[string]string{tc.key: "#123456"})

			if !colorEqual(tc.get(got), want) {
				t.Errorf("Resolve with %q override = %v, want %v", tc.key, tc.get(got), want)
			}
			if colorEqual(tc.get(base), want) {
				t.Fatalf("test is vacuous: base palette already has %v for %q", want, tc.key)
			}
		})
	}
}

// TestResolve_V3PaletteKeys covers the keys v3 spec §5.1-§5.2 added to
// applyOverrideKey: Overlay0, and the herdr source name ActiveRowBG now also
// answers to now that it is sourced from selection_bg.
func TestResolve_V3PaletteKeys(t *testing.T) {
	cases := []struct {
		key string
		get func(Palette) Color
	}{
		{"overlay0", func(p Palette) Color { return p.Overlay0 }},
		{"selection_bg", func(p Palette) Color { return p.ActiveRowBG }},
		{"selectionbg", func(p Palette) Color { return p.ActiveRowBG }},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			base := Default()
			want := parseHexColorForTest(t, "#123456")

			got := Resolve(base, map[string]string{tc.key: "#123456"})

			if !colorEqual(tc.get(got), want) {
				t.Errorf("Resolve with %q override = %v, want %v", tc.key, tc.get(got), want)
			}
			if colorEqual(tc.get(base), want) {
				t.Fatalf("test is vacuous: base palette already has %v for %q", want, tc.key)
			}
		})
	}
}

// TestBuiltinPalettes_EveryFieldIsSet walks Palette's fields by reflection
// rather than naming them, so a field added later is covered here
// automatically. lipgloss.NoColor{} (herdr's Color::Reset, used by the
// terminal palette) is a legitimate value; only a nil field is a hole.
func TestBuiltinPalettes_EveryFieldIsSet(t *testing.T) {
	if len(builtinPalettes) != 18 {
		t.Fatalf("builtinPalettes has %d entries, want 18 (one per herdr Palette::from_name arm)", len(builtinPalettes))
	}
	paletteType := reflect.TypeOf(Palette{})
	for name, palette := range builtinPalettes {
		value := reflect.ValueOf(palette)
		for i := range value.NumField() {
			if value.Field(i).IsNil() {
				t.Errorf("builtinPalettes[%q].%s is nil, want a translated color", name, paletteType.Field(i).Name)
			}
		}
	}
}

func TestBuiltin_TokyoNightDiffersFromDefault(t *testing.T) {
	tokyoNight, ok := Builtin("tokyo-night")
	if !ok {
		t.Fatalf("Builtin(\"tokyo-night\") not found")
	}
	def := Default()
	if samePalette(tokyoNight, def) {
		t.Errorf("Builtin(\"tokyo-night\") == Default(), want a distinct palette")
	}
}

func TestBuiltin_UnknownNameReturnsFalse(t *testing.T) {
	if _, ok := Builtin("not-a-real-theme"); ok {
		t.Errorf("Builtin(\"not-a-real-theme\") = ok, want not found")
	}
}

// TestBuiltin_EveryNameAndAlias walks every canonical theme name and every
// alias accepted by herdr's own canonical_theme_name
// (/home/zvi/Projects/herdr/src/config/theme.rs:25), confirming Builtin
// translates all of them.
func TestBuiltin_EveryNameAndAlias(t *testing.T) {
	names := []string{
		"catppuccin", "catppuccin-mocha",
		"catppuccin-latte", "latte", "light",
		"terminal",
		"tokyo-night", "tokyonight",
		"tokyo-night-day", "tokyo-day", "tokyonight-day",
		"dracula",
		"nord",
		"gruvbox", "gruvbox-dark",
		"gruvbox-light",
		"one-dark", "onedark",
		"one-light", "onelight",
		"solarized", "solarized-dark",
		"solarized-light",
		"kanagawa",
		"kanagawa-lotus", "lotus",
		"rose-pine", "rosepine",
		"rose-pine-dawn", "rosepine-dawn", "dawn",
		"vesper",
	}
	if len(names) != 32 {
		t.Fatalf("test fixture has %d names, want 32 (18 canonical + 14 aliases)", len(names))
	}
	for _, name := range names {
		if _, ok := Builtin(name); !ok {
			t.Errorf("Builtin(%q) not found, want a translated palette", name)
		}
	}
}

func TestBuiltin_NameNormalization(t *testing.T) {
	// herdr lowercases and replaces spaces/underscores with hyphens before
	// matching (theme.rs:26).
	mixed, ok := Builtin("Catppuccin_Mocha")
	if !ok {
		t.Fatalf("Builtin(\"Catppuccin_Mocha\") not found")
	}
	canonical, ok := Builtin("catppuccin")
	if !ok {
		t.Fatalf("Builtin(\"catppuccin\") not found")
	}
	if !samePalette(mixed, canonical) {
		t.Errorf("Builtin(\"Catppuccin_Mocha\") != Builtin(\"catppuccin\"), normalization not applied")
	}
}

func TestDefault_IsCatppuccin(t *testing.T) {
	def := Default()
	catppuccin, ok := Builtin("catppuccin")
	if !ok {
		t.Fatalf("Builtin(\"catppuccin\") not found")
	}
	if !samePalette(def, catppuccin) {
		t.Errorf("Default() != Builtin(\"catppuccin\")")
	}
}

func TestLoadHerdrPaletteFrom_MissingFile_ReturnsDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.toml")

	got := LoadHerdrPaletteFrom(path, nil)
	if !samePalette(got, Default()) {
		t.Errorf("LoadHerdrPaletteFrom(missing file) = %+v, want Default()", got)
	}
}

func TestLoadHerdrPaletteFrom_NameSelectsBuiltin(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeFile(t, path, `
[theme]
name = "dracula"
`)

	got := LoadHerdrPaletteFrom(path, nil)
	dracula, ok := Builtin("dracula")
	if !ok {
		t.Fatalf("Builtin(\"dracula\") not found")
	}
	if !samePalette(got, dracula) {
		t.Errorf("LoadHerdrPaletteFrom did not select the configured builtin: got %+v, want %+v", got, dracula)
	}
}

func TestLoadHerdrPaletteFrom_CustomOverridesBuiltin(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeFile(t, path, `
[theme]
name = "dracula"

[theme.custom]
accent = "#202020"
panel_bg = "#101010"
`)

	got := LoadHerdrPaletteFrom(path, nil)
	wantAccent := parseHexColorForTest(t, "#202020")
	wantPanelBG := parseHexColorForTest(t, "#101010")
	if !colorEqual(got.Accent, wantAccent) {
		t.Errorf("Accent = %v, want %v (from [theme.custom])", got.Accent, wantAccent)
	}
	if !colorEqual(got.PanelBG, wantPanelBG) {
		t.Errorf("PanelBG = %v, want %v (from [theme.custom])", got.PanelBG, wantPanelBG)
	}
}

// TestLoadHerdrPaletteFrom_V2CustomKeys pins the [theme.custom] half of the
// v2 palette addition: herdr's surface0/peach/mauve are real customization
// keys, so a user who retunes them in herdr must get the same colors here
// rather than the builtin's.
//
// active_row_bg was the fourth key here until v3 spec §5.1 moved ActiveRowBG
// onto selection_bg. It moved to the v3 test below rather than being
// repointed in place, because the key that reaches the field is now a v3
// concern and the value has to clear the contrast floor to stay a pure
// routing assertion.
func TestLoadHerdrPaletteFrom_V2CustomKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeFile(t, path, `
[theme]
name = "dracula"

[theme.custom]
surface0 = "#111111"
peach = "#333333"
mauve = "#444444"
`)

	got := LoadHerdrPaletteFrom(path, nil)

	cases := []struct {
		herdrKey  string
		draftName string
		hex       string
		got       Color
	}{
		{"surface0", "Surface", "#111111", got.Surface},
		{"peach", "Warning", "#333333", got.Warning},
		{"mauve", "Branch", "#444444", got.Branch},
	}
	for _, tc := range cases {
		want := parseHexColorForTest(t, tc.hex)
		if !colorEqual(tc.got, want) {
			t.Errorf("%s = %v, want %v (from [theme.custom] %s)", tc.draftName, tc.got, want, tc.herdrKey)
		}
	}
}

// TestLoadHerdrPaletteFrom_V3CustomKeys covers the [theme.custom] keys v3
// spec §5.1-§5.2 brought into herdrThemeCustom. Both override values are
// chosen to clear ActiveRowContrastFloor against dracula's panel_bg, so this
// stays an assertion about key routing; floorContrast raising an illegible
// one is TestLoadHerdrPalette_FloorsAnIllegibleOverride's job.
func TestLoadHerdrPaletteFrom_V3CustomKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeFile(t, path, `
[theme]
name = "dracula"

[theme.custom]
selection_bg = "#555555"
overlay0 = "#666666"
`)

	got := LoadHerdrPaletteFrom(path, nil)

	cases := []struct {
		herdrKey  string
		draftName string
		hex       string
		got       Color
	}{
		{"selection_bg", "ActiveRowBG", "#555555", got.ActiveRowBG},
		{"overlay0", "Overlay0", "#666666", got.Overlay0},
	}
	for _, tc := range cases {
		want := parseHexColorForTest(t, tc.hex)
		if !colorEqual(tc.got, want) {
			t.Errorf("%s = %v, want %v (from [theme.custom] %s)", tc.draftName, tc.got, want, tc.herdrKey)
		}
	}
}

// TestLoadHerdrPaletteFrom_ActiveRowBGCustomKeyIsIgnored pins the key v3 spec
// §5.1 took out of herdrThemeCustom. herdr's active_row_bg marks its
// *active workspace*, not its keyboard cursor; honoring it here would undo
// the remapping, and decoding it alongside selection_bg would be a
// nondeterministic double-write into one field (see herdrThemeCustom's doc).
func TestLoadHerdrPaletteFrom_ActiveRowBGCustomKeyIsIgnored(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeFile(t, path, `
[theme]
name = "dracula"

[theme.custom]
active_row_bg = "#555555"
`)

	got := LoadHerdrPaletteFrom(path, nil)
	dracula, ok := Builtin("dracula")
	if !ok {
		t.Fatalf("Builtin(\"dracula\") not found")
	}
	if !colorEqual(got.ActiveRowBG, dracula.ActiveRowBG) {
		t.Errorf("ActiveRowBG = %v, want the builtin's %v -- herdr's active_row_bg must not reach it", got.ActiveRowBG, dracula.ActiveRowBG)
	}
}

func TestLoadHerdrPaletteFrom_DraftOverrideWinsOverCustomAndBuiltin(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeFile(t, path, `
[theme]
name = "dracula"

[theme.custom]
accent = "#202020"
panel_bg = "#101010"
`)

	draftOverrides := map[string]string{"accent": "#303030"}
	got := LoadHerdrPaletteFrom(path, draftOverrides)

	wantAccent := parseHexColorForTest(t, "#303030")
	if !colorEqual(got.Accent, wantAccent) {
		t.Errorf("Accent = %v, want %v (draft override must win)", got.Accent, wantAccent)
	}
	// panel_bg was not in draftOverrides, so the [theme.custom] value from
	// the previous stage must survive untouched.
	wantPanelBG := parseHexColorForTest(t, "#101010")
	if !colorEqual(got.PanelBG, wantPanelBG) {
		t.Errorf("PanelBG = %v, want %v (unrelated draft key must not disturb it)", got.PanelBG, wantPanelBG)
	}
}

func TestLoadHerdrPaletteFrom_UnknownNameFallsBackToDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeFile(t, path, `
[theme]
name = "not-a-real-theme"
`)

	got := LoadHerdrPaletteFrom(path, nil)
	if !samePalette(got, Default()) {
		t.Errorf("LoadHerdrPaletteFrom(unknown theme) = %+v, want Default() fallback", got)
	}
}

func TestLoadHerdrPaletteFrom_InvalidTOMLFallsBackToDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeFile(t, path, `not valid toml [[[`)

	got := LoadHerdrPaletteFrom(path, nil)
	if !samePalette(got, Default()) {
		t.Errorf("LoadHerdrPaletteFrom(invalid TOML) = %+v, want Default() fallback", got)
	}
}

func TestLoadHerdrPaletteFrom_AutoSwitchResolvesToDarkVariant(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeFile(t, path, `
[theme]
name = "catppuccin-latte"
auto_switch = true
dark_name = "nord"
`)

	got := LoadHerdrPaletteFrom(path, nil)
	nord, ok := Builtin("nord")
	if !ok {
		t.Fatalf("Builtin(\"nord\") not found")
	}
	if !samePalette(got, nord) {
		t.Errorf("auto_switch did not resolve to the configured dark variant: got %+v, want %+v", got, nord)
	}
}

func TestLoadHerdrPaletteFrom_AutoSwitchWithoutDarkNameDefaultsToCatppuccin(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeFile(t, path, `
[theme]
auto_switch = true
`)

	got := LoadHerdrPaletteFrom(path, nil)
	if !samePalette(got, Default()) {
		t.Errorf("auto_switch without dark_name = %+v, want Default() (catppuccin)", got)
	}
}

func TestLoadHerdrPaletteFrom_TerminalNameResolvesToDarkVariant(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeFile(t, path, `
[theme]
name = "terminal"
dark_name = "gruvbox"
`)

	got := LoadHerdrPaletteFrom(path, nil)
	gruvbox, ok := Builtin("gruvbox")
	if !ok {
		t.Fatalf("Builtin(\"gruvbox\") not found")
	}
	if !samePalette(got, gruvbox) {
		t.Errorf("name = \"terminal\" did not resolve to the configured dark variant: got %+v, want %+v", got, gruvbox)
	}
}

func TestLoadHerdrPalette_UsesXDGConfigHome(t *testing.T) {
	dir := t.TempDir()
	herdrDir := filepath.Join(dir, "herdr")
	if err := os.MkdirAll(herdrDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeFile(t, filepath.Join(herdrDir, "config.toml"), `
[theme]
name = "nord"
`)

	t.Setenv("XDG_CONFIG_HOME", dir)

	got := LoadHerdrPalette(nil)
	nord, ok := Builtin("nord")
	if !ok {
		t.Fatalf("Builtin(\"nord\") not found")
	}
	if !samePalette(got, nord) {
		t.Errorf("LoadHerdrPalette did not honor XDG_CONFIG_HOME: got %+v, want %+v", got, nord)
	}
}

// samePalette compares every Palette field by reflection rather than naming
// them, so a field added to Palette later is compared here without anyone
// having to remember to extend this helper.
func samePalette(a, b Palette) bool {
	av, bv := reflect.ValueOf(a), reflect.ValueOf(b)
	for i := range av.NumField() {
		ac, _ := av.Field(i).Interface().(color.Color)
		bc, _ := bv.Field(i).Interface().(color.Color)
		if !colorEqual(ac, bc) {
			return false
		}
	}
	return true
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write fixture %s: %v", path, err)
	}
}

func parseHexColorForTest(t *testing.T, hex string) color.Color {
	t.Helper()
	c, ok := parseHexColor(hex)
	if !ok {
		t.Fatalf("parseHexColor(%q) failed", hex)
	}
	return c
}
