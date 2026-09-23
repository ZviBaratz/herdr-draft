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
// Anything that is not an OSC 4 REPLY reports ok=false rather than a guess:
// a different OSC, a set rather than a report (`...;#rrggbb` with no `?`
// asked for), a truncated sequence, a non-numeric or out-of-range index, or
// a colour ansi.XParseColor declines. This runs over every unrecognised OSC
// event the program receives, so "not for us" is the common case and has to
// be cheap and exact.
//
// The result is normalised to an explicit 8-bit color.RGBA. That is not
// cosmetic: rgb8 requires an opaque value with each byte replicated, which
// is how it tells a real colour from lipgloss.NoColor and from an ANSI
// index. XParseColor's rgb: branch already returns exactly that, but its
// "#"-prefixed branch returns a colorful.Color, whose RGBA() is computed
// from floats and does not replicate -- so a terminal that answered in hex
// would produce a palette entry every floor in this package quietly skipped.
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
	parsed := ansi.XParseColor(value)
	if parsed == nil {
		return 0, nil, false
	}
	r, g, b, a := parsed.RGBA()
	if a != 0xffff {
		return 0, nil, false
	}
	return uint8(n), color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), 0xff}, true
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
				if _, _, _, ok := rgb8(c); ok {
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
