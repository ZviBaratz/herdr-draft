package theme

import (
	"image/color"
	"sort"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// This file is the host-colours design (host-colours spec §5 and §7.1): the
// pure half of #347. It performs no I/O and knows nothing about terminals,
// Bubble Tea or herdr -- it is handed whatever the host answered and returns
// a palette. internal/app owns the asking.
//
// The premise, measured live on 2026-09-23 and recorded in that spec's §3: a
// program inside a herdr pane that queries OSC 11, OSC 10 and OSC 4 gets the
// HOST terminal's own colours back, not ghostty's stand-ins. herdr answers
// OSC 10/11 only from a host theme its own probe actually obtained
// (`default_color_query_response`,
// https://github.com/herdrdev/herdr/blob/v0.9.0/src/pane/terminal.rs#L3253,
// which returns no reply at all when it has none), and seeds the pane's
// palette from the same object (`apply_host_terminal_theme`, #L1177).
//
// What that buys is the one palette this package could not measure. Until
// #347 the terminal entry's twelve fields were either an ANSI index or
// lipgloss.NoColor{}, rgb8 reported both unmeasurable, and so every floor in
// this package skipped that theme -- correctly, because a ratio computed
// against a value nobody knows is not a measurement. With the host's real
// background in hand there is a ground, and the existing clamps apply
// unchanged.

// HostColors is what the terminal answered. Every field is optional,
// because a host may answer some queries and not others, and the three are
// answered by three different code paths in herdr.
//
// Background is the one that matters: without it there is no ground, so
// there is nothing to measure anything against and ResolveHost returns the
// palette untouched. A missing Foreground is survivable and needs no special
// case -- see ResolveHost.
//
// Palette is keyed by ANSI palette index. Only the indices a palette
// actually uses are ever asked for (QueryIndices), and an index that did not
// answer is simply absent, which leaves that field an index exactly as it is
// today.
type HostColors struct {
	Background Color
	Foreground Color
	Palette    map[uint8]Color
}

// NeedsHostColors reports whether p has anything to gain from the host's
// colours: an unmeasurable PanelBG, which is the ground every floor in this
// package measures against.
//
// By construction that is the terminal palette and nothing else. Every other
// builtin holds hex literals, and both override tiers go through
// parseHexColor, which accepts hex and nothing else -- so no `[palette]`
// table, no herdr `[theme.custom]` table and no custom herdr theme can
// produce a palette this reports true for. That is what keeps the query off
// the wire on seventeen of the eighteen builtins, and what keeps their
// golden frames provably still.
func NeedsHostColors(p Palette) bool {
	_, _, _, ok := rgb8(p.PanelBG)
	return !ok
}

// QueryIndices returns the ANSI palette indices p actually draws with, in
// ascending order, with no duplicates. It is what internal/app asks the
// terminal about, so the query is exactly as wide as the palette needs: on
// the terminal entry that is 2, 3, 4, 7, 8 and 9 -- six sequences, not the
// 256 herdr's own client probes for.
//
// It is derived from the palette rather than written out, so a field that
// gains, loses or changes its index needs no second edit here. An override
// that replaced one with hex drops out of the list automatically, because it
// is no longer an index.
func QueryIndices(p Palette) []uint8 {
	seen := map[uint8]bool{}
	for _, c := range fields(&p) {
		if idx, ok := basicIndex(*c); ok {
			seen[idx] = true
		}
	}
	out := make([]uint8, 0, len(seen))
	for idx := range seen {
		out = append(out, idx)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ResolveHost applies host-colours spec §5.1's seed/floor/restore to p and
// returns the result. It is pure, and it is the only function in this
// package that knows a host colour from any other colour.
//
//  1. SEED. Every unmeasurable field is replaced by the colour the host
//     would have drawn for it: an ANSI index by that index's real RGB, and
//     each of the four NoColor fields by the background or the foreground it
//     inherits. After this step the palette is ordinary and measurable.
//  2. FLOOR. floorContrast runs exactly as it does for every other palette.
//     Not a copy of it, not a variant: the same function, so v3 spec §5.3's
//     clamp, §5.3a's floors, the three-ground list and the load-bearing
//     ordering rule (ActiveRowBG is raised BEFORE it is used as a ground)
//     all apply here by construction rather than by remembering to.
//  3. RESTORE. Every field the floor did not move goes back to the
//     unmeasurable value it came in as.
//
// Step 3 is what keeps #304's guarantee, and it is load-bearing rather than
// tidy. Without it a seeded PanelBG that no clamp touched would be EMITTED,
// as truecolor, and the form would paint over the user's own terminal
// background -- possibly a transparent one -- on exactly the themes that
// needed no repair at all. The rule it implements is host-colours spec §5.3:
// an index that clears its floor stays an index and goes on following the
// user's terminal; one that does not is replaced, because "it follows your
// terminal" is not a property worth keeping on a word the measurement says
// cannot be read.
//
// PanelBG and Text are restored unconditionally, whatever the floor did,
// because they are the two "inherit the terminal's own" fields and are only
// ever used here as grounds. floorContrast never writes them, so this is
// belt and braces; it is spelled out because a future floor that did write
// one would otherwise start emitting it.
//
// ActiveRowBG needs no help: it is seeded from the background, which puts
// it at 1.00:1 against it, so the floor always moves it and step 3 keeps the
// raised value. With no Foreground the floor cannot move it -- ensureContrast
// has no `toward` and returns its input -- and step 3 then restores NoColor,
// so the row stays unfilled rather than being painted a flat copy of the
// background it sits on. Both outcomes fall out of the general rule.
//
// SURFACE IS THE ONE FIELD THE GENERAL RULE CANNOT REACH, and the reason is
// worth reading before touching it. floorContrast never writes Surface: the
// painted value is derived per call site, by InputFill or SurfaceFill, from
// the ground it is composited onto -- which is exactly what Surface's own
// doc says to reach it through. Both of those derive by calling
// ensureContrast with Text as the target, and on this palette Text goes back
// to NoColor, so at render time they can derive nothing and hand their input
// straight back. Whatever is in this field IS the fill, at every call site.
//
// So it is computed here, once, as SurfaceFill of the background -- the same
// expression floorContrast used as the third ground a word is measured on,
// so the fill a picker paints its cursor row with and the fill the words
// were floored against are the same value by construction rather than by
// coincidence. That is #149's rule, and this is the one place on this
// palette where it has to be asserted rather than derived.
//
// What one value cannot do is serve two grounds, and the cost lands in one
// place: an input rendered on a FOCUSED row. Every other theme paints that
// with InputFill(ActiveRowBG), raised away from the band; here it comes out
// as the same Surface, which is raised away from the panel by the same walk
// and the same 1.25 floor that raised ActiveRowBG -- so the two are equal
// and the input does not read as a chip on the band. That is not a
// regression: on this palette an input has had NO fill at all since #304,
// and this leaves that one region exactly where it was while fixing the
// cursor row and the cancel button. Raising Surface against ActiveRowBG
// instead was tried and rejected -- it fixes the input, and it makes the
// cursor fill brighter than any other theme draws it, which pushed
// solarized-light's dim tier under DimTextContrastFloor with nowhere left
// to walk (raiseDimText's ceiling). One region unfilled beats one theme
// illegible.
//
// A missing Foreground is otherwise survivable and needs no branch, because
// the existing clamps already decline what they cannot measure: the band and
// raiseDimText return their input untouched (Text is their `toward` and
// their ceiling), while raiseSemanticText walks toward black or white and
// needs only the grounds, so the words are still floored. That falls out of
// ensureContrast's and raiseDimText's own contracts rather than being
// arranged here.
func ResolveHost(p Palette, host HostColors) Palette {
	if !NeedsHostColors(p) {
		return p
	}
	if _, _, _, ok := rgb8(host.Background); !ok {
		return p
	}

	seeded := p
	seedFields(&seeded, p, host)

	// The fill a Surface-filled region gets, computed while the background
	// and foreground are still in hand. It is the expression floorContrast
	// measures words against, not the Surface field, for #149's reason.
	surface := p.Surface
	if fill := seeded.SurfaceFill(seeded.PanelBG); !sameColor(fill, seeded.Surface) {
		surface = fill
	}

	floored := floorContrast(seeded)

	// Restore: anything the floor left where the seed put it goes back to
	// the value it arrived as. sameColor compares the measured channels,
	// not the interface value, because floorContrast hands back a
	// color.RGBA where the seed put one in and the two need not be the
	// identical dynamic type.
	out := floored
	outF, inF, seedF := fields(&out), fields(&p), fields(&seeded)
	for i := range outF {
		if sameColor(*outF[i], *seedF[i]) {
			*outF[i] = *inF[i]
		}
	}
	out.PanelBG = p.PanelBG
	out.Text = p.Text
	// Surface is set rather than restored: it is a computed fill, not an
	// inherited value, and the restore loop above would have reverted it
	// because floorContrast never moves that field. Where the walk had
	// nowhere to go -- no foreground, so no target -- it keeps p's own
	// value, which leaves every fill exactly as unfilled as it is today.
	out.Surface = surface
	return out
}

// ParsePaletteReply parses one OSC 4 colour report -- the terminal's answer
// to `ESC ] 4 ; n ; ? ST` -- into its index and colour.
//
// The shape herdr emits is `\x1b]4;{n};rgb:{r:04x}/{g:04x}/{b:04x}\x1b\\`
// (osc_rgb_response,
// https://github.com/herdrdev/herdr/blob/v0.9.0/src/pane/terminal.rs#L3312),
// with 16-bit channels at 8-bit x 257. Both terminators are accepted: ST is
// what herdr sends, BEL is what plenty of other terminals send, and
// ultraviolet's own parser stops at either, so a program that only handled
// one would work in a herdr pane and nowhere else.
//
// It parses `rgb:` ITSELF rather than calling ansi.XParseColor, and that is
// the #348 review's S7. XParseColor is deliberately lenient: its rgb:
// branch discards every strconv error, so `rgb:zz/zz/zz` comes back as
// opaque BLACK with no complaint -- a colour this package would then
// measure, floor and quite possibly emit. It also scales every channel by
// the same `>>8` regardless of width, so the X11 spellings `rgb:f/f/f` and
// `rgb:fff/fff/fff`, both of which mean white, arrive as #0f0f0f, and a
// five-digit channel wraps. Inside a herdr pane none of that is reachable,
// because herdr always sends four digits; this parser is what makes the
// sentence "anything else reports ok=false" true outside one as well.
//
// So: one to four hex digits per channel, scaled the way X11 scales them
// (v * 255 / (16^n - 1), which sends `f`, `ff`, `fff` and `ffff` all to
// 255), and a refusal for everything else. That includes the `#rrggbb`
// form, which is a SET rather than a report: a terminal never sends it to
// us, and accepting it would make the doc below false in the one direction
// that matters -- silently taking a value nobody answered with.
//
// Anything that is not an OSC 4 report reports ok=false rather than a
// guess: a different OSC, a truncated sequence, a non-numeric or
// out-of-range index, or a malformed colour. This runs over every
// unrecognised OSC event the program receives, so "not for us" is the
// common case and has to be cheap and exact.
//
// The result is an explicit 8-bit color.RGBA. That is for exactness and
// for a stable concrete type, NOT -- as an earlier draft of this comment
// claimed -- because rgb8 "requires each byte replicated". rgb8 tells
// values apart by their TYPE and their alpha and never looks at
// replication (see it in palette.go), and go-colorful's RGBA() is opaque
// with channels of exactly v*257 anyway, so nothing here was ever at risk
// of being silently skipped by a floor. The claim was wrong; the
// normalisation is still worth having.
func ParsePaletteReply(s string) (index uint8, c Color, ok bool) {
	body, found := strings.CutPrefix(s, "\x1b]4;")
	if !found {
		return 0, nil, false
	}
	switch {
	case strings.HasSuffix(body, "\x1b\\"):
		body = strings.TrimSuffix(body, "\x1b\\")
	case strings.HasSuffix(body, "\x07"):
		body = strings.TrimSuffix(body, "\x07")
	default:
		return 0, nil, false
	}

	num, value, found := strings.Cut(body, ";")
	if !found {
		return 0, nil, false
	}
	n, err := strconv.ParseUint(num, 10, 8)
	if err != nil {
		return 0, nil, false
	}
	r, g, b, ok := parseXParseRGB(value)
	if !ok {
		return 0, nil, false
	}
	return uint8(n), color.RGBA{r, g, b, 0xff}, true
}

// parseXParseRGB parses X11's `rgb:R/G/B`, where each channel is one to
// four hex digits, and scales each to eight bits the way X11 does: the
// digits are a fraction of that width's maximum, so `f`, `ff`, `fff` and
// `ffff` all mean full intensity. Anything else -- a missing prefix, the
// wrong number of channels, an empty or over-long channel, a non-hex digit
// -- reports false.
func parseXParseRGB(v string) (r, g, b uint8, ok bool) {
	body, found := strings.CutPrefix(v, "rgb:")
	if !found {
		return 0, 0, 0, false
	}
	parts := strings.Split(body, "/")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	var out [3]uint8
	for i, part := range parts {
		if len(part) == 0 || len(part) > 4 {
			return 0, 0, 0, false
		}
		raw, err := strconv.ParseUint(part, 16, 32)
		if err != nil {
			return 0, 0, 0, false
		}
		max := uint64(1)<<(4*len(part)) - 1
		// Rounded rather than truncated, so a 1-digit `8` lands on 0x88
		// rather than 0x87 -- which is what replicating the digit gives,
		// and what every other implementation produces.
		out[i] = uint8((raw*255 + max/2) / max)
	}
	return out[0], out[1], out[2], true
}

// ghosttyDefaults is the palette libghostty starts every pane with, from
// ghostty's own src/terminal/color.zig (`Name.default`). It is here to be
// RECOGNISED and refused, not to be used.
//
// The reason is #348's review, S1, and it is the one place where reading
// herdr's source changed the design rather than confirming it. Inside a
// herdr pane, OSC 4 NEVER goes unanswered: palette_color_query_response
// always replies, from the pane's own palette, and apply_host_terminal_theme
// builds that palette by starting from these defaults and overwriting only
// the entries the host actually supplied
// (https://github.com/herdrdev/herdr/blob/v0.9.0/src/pane/terminal.rs#L1177).
// On WSL the host palette is never probed at all --
// should_query_host_terminal_palette is `!running_inside_wsl()`
// (https://github.com/herdrdev/herdr/blob/v0.9.0/src/platform/linux.rs#L45)
// -- so OSC 10 and 11 carry the host's real foreground and background while
// OSC 4 carries these. The same happens anywhere the host answered herdr's
// 256-entry probe only partially.
//
// Left alone, that is worse than doing nothing: the palette would be
// floored against colours the screen never draws, and a word could be
// REPLACED on the strength of a measurement of ghostty's stand-in. Today's
// behaviour on those hosts is indices, unmeasured, which is at least
// honest. So an OSC 4 answer equal to the default for its index is dropped,
// and that index falls back to staying an index -- host-colours spec §6's
// documented partial-answer row, which S1 showed was unreachable as
// written.
//
// The cost is a false positive: a user whose scheme really does use one of
// these values loses the floor on that entry. Measured over the 43 schemes
// in hostschemes_test.go, that is **zero of 43** on any of the six indices
// this palette draws with, and a false positive errs toward today's screen
// rather than away from it.
//
// If ghostty changes these, this table stops matching and the WSL case
// returns silently. That is the maintenance hazard, and it is why
// TestGhosttyDefaults_AreTheOnesHerdrStartsFrom exists to state the values
// in one place rather than leaving them implicit in a comparison.
var ghosttyDefaults = map[uint8]string{
	0: "#1d1f21", 1: "#cc6666", 2: "#b5bd68", 3: "#f0c674",
	4: "#81a2be", 5: "#b294bb", 6: "#8abeb7", 7: "#c5c8c6",
	8: "#666666", 9: "#d54e53", 10: "#b9ca4a", 11: "#e7c547",
	12: "#7aa6da", 13: "#c397d8", 14: "#70c0b1", 15: "#eaeaea",
}

// isGhosttyStandIn reports whether c is exactly what libghostty would have
// answered for idx with no host palette behind it. See ghosttyDefaults.
func isGhosttyStandIn(idx uint8, c Color) bool {
	hex, known := ghosttyDefaults[idx]
	if !known {
		return false
	}
	want, ok := parseHexColor(hex)
	if !ok {
		return false
	}
	return sameColor(c, want)
}

// seedFields performs ResolveHost's step 1 on dst, reading the original
// palette from src. An unmeasurable field the host said nothing about is
// left alone, which is what leaves an index that did not answer an index.
func seedFields(dst *Palette, src Palette, host HostColors) {
	srcF, dstF := fields(&src), fields(dst)
	for i := range srcF {
		if _, _, _, ok := rgb8(*srcF[i]); ok {
			continue // already measurable: an override, and nothing to seed
		}
		if idx, isIndex := basicIndex(*srcF[i]); isIndex {
			if c, found := host.Palette[idx]; found {
				// An answer equal to libghostty's own default for this
				// index is a stand-in, not the host's colour -- see
				// ghosttyDefaults. Leaving the field an index is exactly
				// what "this index did not answer" already does.
				if _, _, _, ok := rgb8(c); ok && !isGhosttyStandIn(idx, c) {
					*dstF[i] = c
				}
			}
			continue
		}
		// Not an index, so lipgloss.NoColor: "inherit the terminal's own".
		// Which one it inherits is the field's job, and the four that do it
		// are named rather than guessed.
		switch dstF[i] {
		case &dst.Text:
			if _, _, _, ok := rgb8(host.Foreground); ok {
				dst.Text = host.Foreground
			}
		case &dst.PanelBG, &dst.Surface, &dst.ActiveRowBG:
			*dstF[i] = host.Background
		}
	}
}

// fields returns a pointer to each of Palette's twelve colour fields, in a
// fixed order. Two callers walk a palette field by field -- the seed and the
// restore -- and both have to visit the same fields in the same order as
// each other, which a hand-written pair of switches would only do until
// somebody added a thirteenth field to one of them.
//
// It mirrors applyOverrideKey's list, and a new Palette field belongs in
// both. The compiler cannot enforce that; the test that renders every field
// as a distinct sentinel can, and does.
func fields(p *Palette) []*Color {
	return []*Color{
		&p.Accent, &p.PanelBG, &p.Text, &p.DimText, &p.Overlay0, &p.Danger,
		&p.Success, &p.Border, &p.Surface, &p.ActiveRowBG, &p.Warning, &p.Branch,
	}
}

// basicIndex reports c's ANSI palette index, for the one Color type that
// carries one. It is deliberately narrower than rgb8's unmeasurable set:
// lipgloss.NoColor is unmeasurable and has no index, and ansi.IndexedColor
// (the 256-colour form) is excluded because no palette in this package
// produces one -- ansiIndex builds ansi.BasicColor and nothing else. Widening
// it would mean seeding a field from a query nothing sent.
func basicIndex(c Color) (uint8, bool) {
	if b, ok := c.(ansi.BasicColor); ok {
		return uint8(b), true
	}
	return 0, false
}

// sameColor reports whether two Colors render identically, by measured
// channels where both are measurable and by identity otherwise. The restore
// step compares a floorContrast result against what it was handed, and those
// are equal-but-not-identical whenever the floor declined to move a value:
// it returns its input, but through a color.Color interface that may hold a
// different dynamic type than the one that went in.
func sameColor(a, b Color) bool {
	ar, ag, ab, aok := rgb8(a)
	br, bg, bb, bok := rgb8(b)
	if aok && bok {
		return ar == br && ag == bg && ab == bb
	}
	if aok != bok {
		return false
	}
	// Neither is measurable: NoColor and an index, which compare by value.
	_, aNo := a.(lipgloss.NoColor)
	_, bNo := b.(lipgloss.NoColor)
	if aNo || bNo {
		return aNo && bNo
	}
	ai, aIdx := basicIndex(a)
	bi, bIdx := basicIndex(b)
	return aIdx && bIdx && ai == bi
}
