package app

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// The measured replies from the author's alacritty, in a real herdr pane on
// 2026-09-23 (host-colours spec §3.1). Kept as the raw bytes rather than as
// parsed colours, so these tests exercise the same parse the program does.
const (
	hostBgReply = "\x1b]11;rgb:1818/1818/1818\x1b\\"
	hostFgReply = "\x1b]10;rgb:d8d8/d8d8/d8d8\x1b\\"
)

var hostPaletteReplies = []string{
	"\x1b]4;2;rgb:9090/a9a9/5959\x1b\\",
	"\x1b]4;3;rgb:f4f4/bfbf/7575\x1b\\",
	"\x1b]4;4;rgb:6a6a/9f9f/b5b5\x1b\\",
	"\x1b]4;7;rgb:d8d8/d8d8/d8d8\x1b\\",
	"\x1b]4;8;rgb:6b6b/6b6b/6b6b\x1b\\",
	"\x1b]4;9;rgb:c5c5/5555/5555\x1b\\",
}

// terminalModel is a Model on the `terminal` palette -- the only palette
// any of this runs for.
func terminalModel(t *testing.T) Model {
	t.Helper()
	p, ok := theme.Builtin("terminal")
	if !ok {
		t.Fatal(`theme.Builtin("terminal") is not a known builtin`)
	}
	m := newTestModel(t, testSetup{})
	m.palette = p
	m.form.SetPalette(p)
	// Re-run New's arming with the palette now in place: newTestModel
	// builds on theme.Default(), which asks the terminal nothing.
	m.initCmds = nil
	if host := m.initHostColorCmds(); len(host) > 0 {
		m.initCmds = host
		m.hostWanted = map[uint8]bool{}
		for _, idx := range theme.QueryIndices(p) {
			m.hostWanted[idx] = true
		}
	}
	return m
}

// TestInitHostColors_AsksOnlyForWhatThePaletteDraws pins both halves of the
// gate: the six sequences the terminal palette needs, and silence on every
// other palette. The second half is what keeps seventeen builtins' golden
// frames provably still -- a query that went out on catppuccin would put a
// repaint in front of every user of every theme.
func TestInitHostColors_AsksOnlyForWhatThePaletteDraws(t *testing.T) {
	var sent []string
	for _, cmd := range terminalModel(t).initCmds {
		if raw, ok := cmd().(tea.RawMsg); ok {
			sent = append(sent, fmt.Sprint(raw.Msg))
		}
	}
	want := []string{
		"\x1b]4;2;?\x1b\\", "\x1b]4;3;?\x1b\\", "\x1b]4;4;?\x1b\\",
		"\x1b]4;7;?\x1b\\", "\x1b]4;8;?\x1b\\", "\x1b]4;9;?\x1b\\",
	}
	if len(sent) != len(want) {
		t.Fatalf("sent %d OSC 4 queries %q, want %d %q", len(sent), sent, len(want), want)
	}
	for i := range want {
		if sent[i] != want[i] {
			t.Errorf("query %d = %q, want %q", i, sent[i], want[i])
		}
	}

	// A measured palette asks for nothing, and arms no timer either.
	if cmds := newTestModel(t, testSetup{}).initHostColorCmds(); cmds != nil {
		t.Errorf("the default palette produced %d host-colour commands, want none", len(cmds))
	}

	// And the discriminating case, which is the only one that tests the
	// GATE rather than the index list. catppuccin holds no ANSI index, so
	// it would be refused by `len(indices) == 0` even with
	// NeedsHostColors deleted -- two guards, each masking the other,
	// found by mutation. This palette has indices AND a measurable
	// PanelBG: nothing about it is unknowable, so nothing may be asked.
	measured := newTestModel(t, testSetup{})
	p, _ := theme.Builtin("terminal")
	measured.palette = theme.Resolve(p, map[string]string{"panel_bg": "#181818"})
	if len(theme.QueryIndices(measured.palette)) == 0 {
		t.Fatal("the fixture lost its ANSI indices, so it no longer discriminates")
	}
	if cmds := measured.initHostColorCmds(); cmds != nil {
		t.Errorf("a palette with a known PanelBG produced %d host-colour commands, want none -- there is nothing left for the host to tell us", len(cmds))
	}
}

// TestHostColors_TheReplyReachesTheFormAsAFill is the end-to-end one: feed
// the Model the exact bytes a herdr pane answered with, and the focused row
// -- which has had no fill since #346 -- comes back with one.
func TestHostColors_TheReplyReachesTheFormAsAFill(t *testing.T) {
	m := terminalModel(t)

	if strings.Contains(m.form.ViewAt(80, 24), "48;2;") {
		t.Fatal("the unresolved terminal screen already paints a truecolor background -- this test's premise (#304, #346) is gone")
	}

	m = deliverHostColors(t, m)

	screen := m.form.ViewAt(80, 24)
	if !strings.Contains(screen, "48;2;43;43;43m") {
		t.Errorf("the focused row has no #2b2b2b fill after the host answered.\nThe band is what #346 removed for want of a ground and what decision 3 brings back; if this moved, say which measurement moved it.\n%q", screen)
	}
}

// TestHostColors_KeepTheIndicesOnScreen is decision 2 at the byte level:
// every word on the author's own host clears its floor, so the screen still
// sends ANSI indices for them and goes on following the terminal.
func TestHostColors_KeepTheIndicesOnScreen(t *testing.T) {
	m := deliverHostColors(t, terminalModel(t))
	screen := m.form.ViewAt(80, 24)

	if !strings.Contains(screen, "\x1b[94m") && !strings.Contains(screen, "\x1b[34m") {
		t.Errorf("no ANSI 4 sequence on screen -- on this host Accent measures 4.88:1 and must still be drawn as the terminal's own blue (#304).\n%q", screen)
	}
	// The only truecolor on this screen is the two fills, never a word.
	for _, fg := range []string{"38;2;"} {
		if i := strings.Index(screen, fg); i >= 0 {
			t.Errorf("a truecolor FOREGROUND at byte %d (%q) -- every word that clears its floor stays an index", i, screen[max(i-8, 0):min(i+24, len(screen))])
		}
	}
}

// TestHostColors_NoAnswerChangesNothing is host-colours spec §6: an older
// herdr, a host that never answered herdr's own probe, a terminal that is
// not herdr. The budget expires and today's screen stands.
func TestHostColors_NoAnswerChangesNothing(t *testing.T) {
	m := terminalModel(t)
	before := m.form.ViewAt(80, 24)

	next, _ := m.Update(hostColorDeadlineMsg{})
	m = next.(Model)

	if got := m.form.ViewAt(80, 24); got != before {
		t.Errorf("the screen moved when nobody answered.\nbefore: %q\nafter:  %q", before, got)
	}
	if !m.hostDone {
		t.Error("the deadline did not close the collection -- a late answer could still repaint mid-sentence")
	}
}

// TestHostColors_ABackgroundWithoutAForegroundStillFloorsTheWords is the
// partial answer the budget is really for, and the assertion is split
// because the two halves of a resolve need different things.
//
// The words need only GROUNDS, so a background alone floors them. The band
// needs a direction to walk in, which is the foreground, so without one it
// stays unfilled -- and it must stay unfilled rather than being emitted as
// a flat copy of the background it sits on. Writing this test as "a
// background alone is enough" got both halves wrong and passed anyway,
// because lipgloss.NoColor is neither nil nor an index and the assertion
// was on the field's shape rather than on the screen.
func TestHostColors_ABackgroundWithoutAForegroundStillFloorsTheWords(t *testing.T) {
	m := terminalModel(t)
	// A white host: ANSI 7, the label tier, is #d8d8d8 on it -- 1.32:1.
	m = deliver(t, m, uv.BackgroundColorEvent{Color: mustParseColor(t, "\x1b]11;rgb:ffff/ffff/ffff\x1b\\")})
	for _, reply := range hostPaletteReplies {
		m = deliver(t, m, uv.UnknownOscEvent(reply))
	}

	if m.hostDone {
		t.Fatal("resolved before the deadline with no foreground -- an incomplete set must wait, or a word gets floored against grounds that do not exist yet")
	}
	next, _ := m.Update(hostColorDeadlineMsg{})
	m = next.(Model)

	// The SEMANTIC tier is floored: raiseSemanticText walks toward black or
	// white, so it needs the grounds and nothing else.
	if isIndex(m.palette.Accent) {
		t.Errorf("Accent is still ANSI 4: #6a9fb5 on white is 2.77:1, and the semantic walk needs only the grounds, which the background supplies")
	}
	// The DIM tier is not, and that is correct rather than a gap: a dim
	// tier must stay dimmer than Text, so raiseDimText's ceiling is the
	// foreground -- with none, there is no ceiling, and a clamp that
	// cannot tell whether it has inverted the two tiers declines.
	if !isIndex(m.palette.DimText) {
		t.Errorf("DimText = %T %v, want ANSI 7 -- raiseDimText has no ceiling without a foreground and must leave the tier alone", m.palette.DimText, m.palette.DimText)
	}
	// And no band. Asserted on the field, because the create button paints
	// its own face with Accent and matches a naive grep for a truecolor
	// background; and asserted against NoColor by name, because that is
	// the one value meaning "no fill" and it is neither nil nor an index.
	if _, ok := m.palette.ActiveRowBG.(lipgloss.NoColor); !ok {
		t.Errorf("ActiveRowBG = %T %v, want lipgloss.NoColor -- with no foreground to walk toward the row stays unfilled rather than taking a flat copy of its own background", m.palette.ActiveRowBG, m.palette.ActiveRowBG)
	}
}

// TestHostColors_ALateAnswerIsDropped is decision 5. The budget is a
// give-up, and what it gives up on is repainting a form somebody has
// already started typing into.
func TestHostColors_ALateAnswerIsDropped(t *testing.T) {
	m := terminalModel(t)
	next, _ := m.Update(hostColorDeadlineMsg{})
	m = next.(Model)
	before := m.form.ViewAt(80, 24)

	m = deliverHostColorsAllowingLate(t, m)

	if got := m.form.ViewAt(80, 24); got != before {
		t.Errorf("an answer that arrived after the budget repainted the screen anyway")
	}
}

// TestHostColors_TheDecoderStillHandsUsOSC4 is the library seam, and the
// reason it is a test rather than a comment: OSC 4 reaches Update only
// because ultraviolet does NOT recognise it and bubbletea's
// translateInputEvent falls through for what it cannot type. The day a
// dependency bump learns that sequence, our uv.UnknownOscEvent branch stops
// matching and the whole feature goes quiet -- no build error, no failing
// assertion anywhere else, just a palette that silently stops resolving.
// This goes red instead.
func TestHostColors_TheDecoderStillHandsUsOSC4(t *testing.T) {
	for _, in := range []string{hostPaletteReplies[0], "\x1b]4;9;rgb:c5c5/5555/5555\x07"} {
		var d uv.EventDecoder
		n, ev := d.Decode([]byte(in))
		if n != len(in) {
			t.Errorf("Decode(%q) consumed %d of %d bytes", in, n, len(in))
		}
		osc, ok := ev.(uv.UnknownOscEvent)
		if !ok {
			t.Fatalf("Decode(%q) returned %T, want uv.UnknownOscEvent.\nIf the library has learned OSC 4, handleHostOsc must switch to whatever it returns now -- the feature is dead until it does.", in, ev)
		}
		if string(osc) != in {
			t.Errorf("Decode(%q) carried %q -- handleHostOsc parses these bytes", in, string(osc))
		}
	}

	// And the two bubbletea DOES type, so a bump that changed those would
	// be caught here too rather than in a screenshot.
	var d uv.EventDecoder
	if _, ev := d.Decode([]byte(hostBgReply)); !isBackgroundEvent(ev) {
		t.Errorf("Decode(%q) returned %T, want a background colour event", hostBgReply, ev)
	}
}

// --- helpers --------------------------------------------------------

func deliverHostColors(t *testing.T, m Model) Model {
	t.Helper()
	m = deliverHostColorsAllowingLate(t, m)
	if !m.hostDone {
		t.Fatal("the full set of answers did not close the collection")
	}
	return m
}

func deliverHostColorsAllowingLate(t *testing.T, m Model) Model {
	t.Helper()
	m = deliver(t, m, uv.BackgroundColorEvent{Color: mustParseColor(t, hostBgReply)})
	m = deliver(t, m, uv.ForegroundColorEvent{Color: mustParseColor(t, hostFgReply)})
	for _, reply := range hostPaletteReplies {
		m = deliver(t, m, uv.UnknownOscEvent(reply))
	}
	return m
}

// deliver routes one event through the real Update, translated the way
// bubbletea translates it, so these tests go through the same switch the
// program does rather than calling the handlers directly.
func deliver(t *testing.T, m Model, ev uv.Event) Model {
	t.Helper()
	var msg tea.Msg
	switch e := ev.(type) {
	case uv.BackgroundColorEvent:
		msg = tea.BackgroundColorMsg(e)
	case uv.ForegroundColorEvent:
		msg = tea.ForegroundColorMsg(e)
	default:
		msg = ev
	}
	next, _ := m.Update(msg)
	return next.(Model)
}

func isBackgroundEvent(ev uv.Event) bool {
	_, ok := ev.(uv.BackgroundColorEvent)
	return ok
}

// mustParseColor pulls the colour out of an OSC 10/11 reply the way
// ultraviolet does, so the fixtures stay raw bytes.
func mustParseColor(t *testing.T, reply string) theme.Color {
	t.Helper()
	var d uv.EventDecoder
	_, ev := d.Decode([]byte(reply))
	switch e := ev.(type) {
	case uv.BackgroundColorEvent:
		return e.Color
	case uv.ForegroundColorEvent:
		return e.Color
	}
	t.Fatalf("Decode(%q) returned %T, not a colour event", reply, ev)
	return nil
}

// isIndex reports whether a palette field is still an ANSI index -- which
// is the only question these tests ask of a colour, and the one thing that
// distinguishes "kept, and still following the terminal" from "replaced".
func isIndex(c theme.Color) bool {
	_, ok := c.(ansi.BasicColor)
	return ok
}

// TestHostColors_TheDeadlineAfterAnAnswerIsInert is the other half of "once,
// and only once", and it is a real hazard rather than tidiness. The budget
// timer always fires, including after the answers have already landed and
// been applied. Without the guard that second pass would run ResolveHost on
// an ALREADY-resolved palette -- whose PanelBG is back to NoColor, so
// NeedsHostColors says yes again -- and re-seed from values the clamps had
// already moved.
func TestHostColors_TheDeadlineAfterAnAnswerIsInert(t *testing.T) {
	m := deliverHostColors(t, terminalModel(t))
	resolved := m.form.ViewAt(80, 24)

	next, _ := m.Update(hostColorDeadlineMsg{})
	m = next.(Model)

	if got := m.form.ViewAt(80, 24); got != resolved {
		t.Errorf("the budget firing after a completed resolve changed the screen -- it re-resolved an already-resolved palette.\nbefore: %q\nafter:  %q", resolved, got)
	}
}
