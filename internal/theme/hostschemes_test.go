package theme

// hostScheme is one real terminal colour scheme, reduced to exactly what
// the terminal palette draws with: the background, the foreground and ANSI
// 2, 3, 4, 7, 8 and 9.
//
// The 43 below are the evidence base #304 and #345 measured against, and
// host-colours spec §5.4"s table was produced from them. 42 come from
// iTerm2-Color-Schemes" ghostty/ directory at
// 12d9f63060857aaf673daae635a4c721d12eb586; "zz Canonical Solarized Dark
// (Xresources)" is hand-built from Solarized"s published Xresources
// mapping.
//
// They are COMMITTED here rather than read from the handoff directory they
// were extracted from, and that is the point: a test that reads a path
// existing on one laptop passes everywhere else by skipping, which is not
// a test. Reduced to eight colours per scheme they cost a few hundred
// lines and answer the only question this package asks of them -- does the
// clamp hold on a real terminal palette, on every real terminal palette
// anyone handed us.
type hostScheme struct {
	name   string
	bg, fg string
	// ansi is indices 2, 3, 4, 7, 8, 9 in that order -- Success, Warning,
	// Accent, the DimText/Overlay0/Branch tier, Border and Danger.
	ansi [6]string
}

// hostSchemeIndices names what hostScheme.ansi"s six entries are, so a
// reader does not have to count and a test does not have to hardcode.
var hostSchemeIndices = [6]uint8{2, 3, 4, 7, 8, 9}

// colors builds the HostColors a terminal running this scheme would answer
// with.
func (s hostScheme) colors() HostColors {
	h := HostColors{Palette: map[uint8]Color{}}
	h.Background, _ = parseHexColor(s.bg)
	h.Foreground, _ = parseHexColor(s.fg)
	for i, idx := range hostSchemeIndices {
		h.Palette[idx], _ = parseHexColor(s.ansi[i])
	}
	return h
}

var hostSchemes = []hostScheme{
	{"Apple System Colors Light", "#feffff", "#000000", [6]string{"#26a439", "#cdac08", "#0869cb", "#98989d", "#464646", "#ff453a"}},
	{"Apple System Colors", "#1e1e1e", "#ffffff", [6]string{"#26a439", "#cdac08", "#0869cb", "#98989d", "#464646", "#ff453a"}},
	{"Atom One Dark", "#21252b", "#abb2bf", [6]string{"#98c379", "#e5c07b", "#61afef", "#abb2bf", "#767676", "#e06c75"}},
	{"Atom One Light", "#f9f9f9", "#2a2c33", [6]string{"#3f953a", "#d2b67c", "#2f5af3", "#bbbbbb", "#000000", "#de3e35"}},
	{"Ayu Light", "#f8f9fa", "#5c6166", [6]string{"#6cbf43", "#eca944", "#3199e1", "#bababa", "#686868", "#f07171"}},
	{"Ayu", "#0b0e14", "#bfbdb6", [6]string{"#7fd962", "#f9af4f", "#53bdfa", "#c7c7c7", "#686868", "#f07178"}},
	{"Breeze", "#31363b", "#eff0f1", [6]string{"#11d116", "#f67400", "#1d99f3", "#eff0f1", "#7f8c8d", "#c0392b"}},
	{"Builtin Dark", "#000000", "#bbbbbb", [6]string{"#00bb00", "#bbbb00", "#0d0dc8", "#bbbbbb", "#555555", "#ff5555"}},
	{"Builtin Light", "#ffffff", "#000000", [6]string{"#00bb00", "#bbbb00", "#0000bb", "#bbbbbb", "#555555", "#ff5555"}},
	{"Builtin Tango Dark", "#000000", "#ffffff", [6]string{"#4e9a06", "#c4a000", "#3465a4", "#d3d7cf", "#555753", "#ef2929"}},
	{"Builtin Tango Light", "#ffffff", "#000000", [6]string{"#4e9a06", "#c4a000", "#3465a4", "#b9bdb5", "#555753", "#ef2929"}},
	{"Catppuccin Latte", "#eff1f5", "#4c4f69", [6]string{"#40a02b", "#df8e1d", "#1e66f5", "#5c5f77", "#acb0be", "#e7103f"}},
	{"Catppuccin Mocha", "#1e1e2e", "#cdd6f4", [6]string{"#a6e3a1", "#f9e2af", "#89b4fa", "#bac2de", "#585b70", "#f7aec2"}},
	{"Dracula", "#282a36", "#f8f8f2", [6]string{"#50fa7b", "#f1fa8c", "#bd93f9", "#f8f8f2", "#6272a4", "#ff6e6e"}},
	{"Everforest Dark Med", "#232a2e", "#d3c6aa", [6]string{"#a7c080", "#dbbc7f", "#7fbbb3", "#f2efdf", "#a6b0a0", "#f85552"}},
	{"Everforest Light Med", "#efebd4", "#5c6a72", [6]string{"#9ab373", "#c1a266", "#7fbbb3", "#b2af9f", "#a6b0a0", "#f85552"}},
	{"GitHub Dark Default", "#0d1117", "#e6edf3", [6]string{"#3fb950", "#d29922", "#58a6ff", "#b1bac4", "#6e7681", "#ffa198"}},
	{"GitHub Light Default", "#ffffff", "#1f2328", [6]string{"#116329", "#4d2d00", "#0969da", "#6e7781", "#57606a", "#a40e26"}},
	{"Gruvbox Dark", "#282828", "#ebdbb2", [6]string{"#98971a", "#d79921", "#458588", "#a89984", "#928374", "#fb4934"}},
	{"Gruvbox Light", "#fbf1c7", "#3c3836", [6]string{"#98971a", "#d79921", "#458588", "#7c6f64", "#928374", "#9d0006"}},
	{"Kanagawa Lotus", "#f2ecbc", "#545464", [6]string{"#6f894e", "#77713f", "#4d699b", "#545464", "#8a8980", "#d7474b"}},
	{"Kanagawa Wave", "#1f1f28", "#dcd7ba", [6]string{"#76946a", "#c0a36e", "#7e9cd8", "#c8c093", "#727169", "#e82424"}},
	{"Kitty Default", "#000000", "#dddddd", [6]string{"#19cb00", "#cecb00", "#0d73cc", "#dddddd", "#767676", "#f2201f"}},
	{"Material", "#eaeaea", "#232322", [6]string{"#457b24", "#f6981e", "#134eb2", "#afafaf", "#424242", "#e83b3f"}},
	{"Monokai Pro", "#2d2a2e", "#fcfcfa", [6]string{"#a9dc76", "#ffd866", "#fc9867", "#fcfcfa", "#727072", "#ff6188"}},
	{"Night Owl", "#011627", "#d6deeb", [6]string{"#22da6e", "#addb67", "#82aaff", "#ffffff", "#575656", "#ef5350"}},
	{"Nord Light", "#e5e9f0", "#414858", [6]string{"#96b17f", "#c5a565", "#81a1c1", "#a5abb6", "#4c566a", "#bf616a"}},
	{"Nord", "#2e3440", "#d8dee9", [6]string{"#a3be8c", "#ebcb8b", "#81a1c1", "#e5e9f0", "#596377", "#bf616a"}},
	{"One Half Dark", "#282c34", "#dcdfe4", [6]string{"#98c379", "#e5c07b", "#61afef", "#dcdfe4", "#5d677a", "#e06c75"}},
	{"One Half Light", "#fafafa", "#383a42", [6]string{"#50a14f", "#c18401", "#0184bc", "#bababa", "#4f525e", "#e06c75"}},
	{"Rose Pine Dawn", "#faf4ed", "#575279", [6]string{"#286983", "#ea9d34", "#56949f", "#575279", "#9893a5", "#b4637a"}},
	{"Rose Pine", "#191724", "#e0def4", [6]string{"#31748f", "#f6c177", "#9ccfd8", "#e0def4", "#6e6a86", "#eb6f92"}},
	{"Selenized Black", "#181818", "#b9b9b9", [6]string{"#70b433", "#dbb32d", "#368aeb", "#b9b9b9", "#777777", "#ff5e56"}},
	{"Solarized Dark Patched", "#001e27", "#708284", [6]string{"#738a05", "#a57706", "#2176c7", "#eae3cb", "#475b62", "#bd3613"}},
	{"TokyoNight Day", "#e1e2e7", "#3760bf", [6]string{"#587539", "#8c6c3e", "#2e7de9", "#6172b0", "#a1a6c5", "#f52a65"}},
	{"TokyoNight", "#1a1b26", "#c0caf5", [6]string{"#9ece6a", "#e0af68", "#7aa2f7", "#a9b1d6", "#414868", "#f7768e"}},
	{"Ubuntu", "#300a24", "#eeeeec", [6]string{"#4e9a06", "#c4a000", "#3465a4", "#d3d7cf", "#555753", "#ef2929"}},
	{"Wez", "#000000", "#b3b3b3", [6]string{"#55cc55", "#cdcd55", "#5555cc", "#cccccc", "#555555", "#ff5555"}},
	{"iTerm2 Default", "#000000", "#ffffff", [6]string{"#00c200", "#c7c400", "#2225c4", "#ffffff", "#686868", "#ff6e67"}},
	{"iTerm2 Light Background", "#ffffff", "#000000", [6]string{"#00c200", "#c7c400", "#0225c7", "#bababa", "#686868", "#ff6e67"}},
	{"iTerm2 Solarized Dark", "#002b36", "#839496", [6]string{"#859900", "#b58900", "#268bd2", "#eee8d5", "#335e69", "#cb4b16"}},
	{"iTerm2 Solarized Light", "#fdf6e3", "#657b83", [6]string{"#859900", "#b58900", "#268bd2", "#bbb5a2", "#002b36", "#cb4b16"}},
	{"zz Canonical Solarized Dark (Xresources)", "#002b36", "#839496", [6]string{"#859900", "#b58900", "#268bd2", "#eee8d5", "#002b36", "#cb4b16"}},
}
