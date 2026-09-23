package app

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/ZviBaratz/herdr-draft/internal/form"
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
// any of this runs for -- built through the real New.
//
// Going through New is the point. An earlier version built on
// theme.Default() and then hand-copied New's arming into the helper, which
// meant no test ran the real thing: deleting New's hostWanted loop, or its
// append to initCmds, passed the whole suite while production rejected
// every reply and resolved at the deadline with nothing (#348 review, S4).
func terminalModel(t *testing.T) Model {
	t.Helper()
	p, ok := theme.Builtin("terminal")
	if !ok {
		t.Fatal(`theme.Builtin("terminal") is not a known builtin`)
	}
	return newTestModel(t, testSetup{Palette: &p})
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

// TestHostColors_TheOSC4SeamSurvivesADependencyBump drives a REAL
// tea.Program over the exact bytes a herdr pane answers with, and asserts
// the reply reaches Update.
//
// The feature hangs on two fall-throughs, and an earlier version of this
// test only covered one. ultraviolet's parseOsc has to return
// UnknownOscEvent for OSC 4, AND bubbletea's translateInputEvent has to
// return it unchanged rather than typing it. Driving uv.EventDecoder alone
// pins the first; a bubbletea bump adding `case uv.UnknownOscEvent:` would
// switch the feature off with that test still green, while its doc claimed
// it "goes red instead" (#348 review, S8).
//
// So this goes through the whole stack: bytes in on stdin, messages out to
// a model. Note what it deliberately does NOT do -- record every message.
// tea.EnvMsg carries the entire process environment, and a debug model that
// logged it once put this machine's API keys in a transcript.
func TestHostColors_TheOSC4SeamSurvivesADependencyBump(t *testing.T) {
	const reply = "\x1b]4;9;rgb:c5c5/5555/5555\x1b\\"

	got := make(chan string, 4)
	m := seamModel{got: got}
	p := tea.NewProgram(m,
		tea.WithInput(strings.NewReader(reply)),
		tea.WithOutput(io.Discard),
		tea.WithoutSignalHandler(),
	)
	done := make(chan error, 1)
	go func() { _, err := p.Run(); done <- err }()

	select {
	case osc := <-got:
		if osc != reply {
			t.Errorf("Update received %q, want the bytes that went in, %q", osc, reply)
		}
	case <-time.After(5 * time.Second):
		p.Kill()
		t.Fatal("no uv.UnknownOscEvent reached Update within 5s.\nThe OSC 4 reply is only visible to this program because neither ultraviolet nor bubbletea recognises it. If a bump has taught either of them that sequence, handleHostOsc must switch to whatever it now returns -- the feature is dead until it does.")
	}
	p.Quit()
	<-done
}

// seamModel reports the first uv.UnknownOscEvent it is given and nothing
// else. It is deliberately not a message recorder; see the test's doc.
type seamModel struct{ got chan string }

func (m seamModel) Init() tea.Cmd { return nil }

func (m seamModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if osc, ok := msg.(uv.UnknownOscEvent); ok {
		select {
		case m.got <- string(osc):
		default:
		}
		return m, tea.Quit
	}
	return m, nil
}

func (m seamModel) View() tea.View { return tea.NewView("") }

// TestHostColors_TheDecoderStillHandsUsOSC4 pins ultraviolet's half on its
// own, offline and without a terminal, so a failure says which half broke.
func TestHostColors_TheDecoderStillHandsUsOSC4(t *testing.T) {
	for _, in := range []string{hostPaletteReplies[0], "\x1b]4;9;rgb:c5c5/5555/5555\x07"} {
		var d uv.EventDecoder
		n, ev := d.Decode([]byte(in))
		if n != len(in) {
			t.Errorf("Decode(%q) consumed %d of %d bytes", in, n, len(in))
		}
		osc, ok := ev.(uv.UnknownOscEvent)
		if !ok {
			t.Fatalf("Decode(%q) returned %T, want uv.UnknownOscEvent", in, ev)
		}
		if string(osc) != in {
			t.Errorf("Decode(%q) carried %q -- handleHostOsc parses these bytes", in, string(osc))
		}
	}

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

// TestHostColors_TheDeadlineAfterAnAnswerIsInert pins that the budget
// timer, which always fires, changes nothing once the answers have landed.
//
// Read what it does NOT pin, because its first version claimed otherwise.
// It does not pin finishHostColors' hostDone check: remove that, and this
// still passes, because a second resolve over the measured host is exactly
// the idempotent case and the screen comes back byte-identical. The check
// is pinned by TestHostColors_ALateAnswerIsDropped, where the deadline
// comes FIRST and the answers after, so the second pass has new
// information and does change the screen (#348 review, S5).
func TestHostColors_TheDeadlineAfterAnAnswerIsInert(t *testing.T) {
	m := deliverHostColors(t, terminalModel(t))
	resolved := m.form.ViewAt(80, 24)

	next, _ := m.Update(hostColorDeadlineMsg{})
	m = next.(Model)

	if got := m.form.ViewAt(80, 24); got != resolved {
		t.Errorf("the budget firing after a completed resolve changed the screen.\nbefore: %q\nafter:  %q", resolved, got)
	}
}

// TestHostColors_AClearDoesNotAskAgain is the ⌃R⌃R path, which had no test
// at all (#348 review, S3 and S4).
//
// The rebuilt Model is handed the RESOLVED palette, whose PanelBG is back
// to NoColor, so NeedsHostColors says yes again and an earlier version
// re-sent the whole query and re-resolved. That was defensible only while
// "the resolve is idempotent" was believed unconditionally, and it is not:
// a clamp that gives up returns a value that does not clear its floor, so a
// second pass walks again from a new start. Setup.HostColorsDone carries
// the answer across instead.
func TestHostColors_AClearDoesNotAskAgain(t *testing.T) {
	m := deliverHostColors(t, terminalModel(t))

	next, _ := m.Update(form.ClearRequestedMsg{})
	fresh, ok := next.(Model)
	if !ok {
		t.Fatalf("a clear returned %T, not a Model", next)
	}

	if !fresh.hostDone {
		t.Error("the rebuilt Model has not been told the terminal already answered, so it will ask again and re-resolve an already-resolved palette")
	}
	for _, cmd := range fresh.initCmds {
		if cmd == nil {
			continue
		}
		if raw, isRaw := cmd().(tea.RawMsg); isRaw {
			t.Errorf("the rebuilt Model re-sent an OSC query: %q", fmt.Sprint(raw.Msg))
		}
	}
	// Asserted on the palette, not on the screen: ⌃R⌃R resets the form's
	// CONTENT by design, so two screens differ for reasons that have
	// nothing to do with colour. The band is the palette's own fingerprint
	// -- it exists only on a resolved one.
	if _, unfilled := fresh.palette.ActiveRowBG.(lipgloss.NoColor); unfilled {
		t.Error("the rebuilt Model's ActiveRowBG is back to NoColor: it was handed the fallback palette, not the resolved one")
	}
	if !strings.Contains(fresh.form.ViewAt(80, 24), "48;2;43;43;43m") {
		t.Errorf("the rebuilt form does not paint the resolved band.\n%q", fresh.form.ViewAt(80, 24))
	}
}
