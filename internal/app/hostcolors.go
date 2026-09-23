package app

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// This file is internal/app's half of the host-colours design (#347,
// host-colours spec §7.2): ask the terminal what colours it actually
// draws, and repaint once when it answers.
//
// It runs for exactly one palette. theme.NeedsHostColors is true only when
// PanelBG is unmeasurable, which by construction is the `terminal` entry
// and nothing else (see its doc), so on the other seventeen builtins
// nothing here sends a byte, allocates a timer or reaches Update --
// initCmds is byte-identical to what it was before this existed.
//
// The measurements behind every number here were taken in a real herdr
// pane on 2026-09-23 and are recorded in host-colours spec §3. The short
// version: herdr answers OSC 10, OSC 11 and OSC 4 with the HOST terminal's
// own colours, and the answers arrive 18.8-23.3ms after process start --
// which is ~18ms after the first frame, and within 0.2ms of the frame that
// flushed the queries, because the round trip through herdr's emulator is
// sub-millisecond and the wait is Bubble Tea's own first flush.

// hostColorBudget is how long a palette waits for the terminal before it
// gives up and keeps the one it opened with (host-colours spec §4.2,
// decision 5).
//
// It is a GIVE-UP, not a wait. Nothing blocks on it: the form is drawn and
// fully usable from the first frame, this timer only decides how late an
// answer may be and still be worth applying.
//
// 250ms is chosen from both ends. It is ten times the worst measured
// arrival (23.3ms over five runs), so a terminal that is going to answer
// answers inside it with an order of magnitude to spare on a machine far
// slower than the one measured. And it is below the floor of how fast a
// person reacts to the popup appearing, which is what the ceiling is for:
// applying a late answer would repaint the screen -- on some themes six
// words changing colour at once -- while somebody is already typing into
// it. A repaint nobody can perceive is the whole point, so an answer that
// can no longer be imperceptible is not worth having.
//
// There is no retry. A terminal that did not answer one OSC query will not
// answer a second, and the fallback (host-colours spec §6) is a screen
// this project already ships and already guards.
const hostColorBudget = 250 * time.Millisecond

// hostColorDeadlineMsg is the budget firing. It carries no version: unlike
// every other timer in this package it is armed exactly once, from New, and
// a ⌃R⌃R rebuild deliberately does NOT re-arm it -- see initHostColorCmds.
type hostColorDeadlineMsg struct{}

// initHostColorCmds returns the queries and the timer, or nil when the
// palette has nothing to learn.
//
// Three kinds of query, because Bubble Tea types two of them and not the
// third (host-colours spec §3.3). Background and foreground have commands
// of their own, and their answers come back as tea.BackgroundColorMsg and
// tea.ForegroundColorMsg. A palette entry has no command, so it goes out as
// a raw OSC 4 through tea.Raw -- which is not a back door but the supported
// one: tea.Raw reaches p.execute, the same output buffer under the same
// mutex that tea.RequestBackgroundColor itself writes to
// (bubbletea/v2@v2.0.9 raw.go and tea.go:866). The reply arrives untyped,
// as uv.UnknownOscEvent, and handleHostOsc parses it.
//
// The indices asked for are the palette's own (theme.QueryIndices): six on
// the terminal entry, against the 256 herdr's client probes the host for.
func (m Model) initHostColorCmds() []tea.Cmd {
	if !theme.NeedsHostColors(m.palette) {
		return nil
	}
	indices := theme.QueryIndices(m.palette)
	if len(indices) == 0 {
		return nil
	}
	cmds := make([]tea.Cmd, 0, len(indices)+3)
	cmds = append(cmds, tea.RequestBackgroundColor, tea.RequestForegroundColor)
	for _, idx := range indices {
		cmds = append(cmds, tea.Raw(fmt.Sprintf("\x1b]4;%d;?\x1b\\", idx)))
	}
	clock := m.deps.Clock
	cmds = append(cmds, func() tea.Msg {
		clock.sleep(hostColorBudget)
		return hostColorDeadlineMsg{}
	})
	return cmds
}

// handleHostBackground, handleHostForeground and handleHostOsc collect the
// three answers. Each one tries to finish, because any of them may be the
// last to arrive and the order is not ours to choose -- measured, the OSC 4
// replies and the background landed in a different order in one of five
// runs.
//
// NONE OF THEM CHECKS WHETHER THE COLLECTION IS CLOSED, and that is
// deliberate. There is exactly one hostDone check, in finishHostColors,
// because it is the only place a decision is made; recording into
// m.hostColors afterwards costs nothing and nothing reads it again. An
// earlier draft checked here as well, and mutation testing showed what that
// buys: with two checks, breaking either one left every test green, because
// each covered for the other. One guard that a test can actually pin beats
// two that alibi each other.
//
// The two typed ones route their message on to the form afterwards, so the
// form sees exactly what it saw before this file existed.
func (m Model) handleHostBackground(msg tea.BackgroundColorMsg) (Model, tea.Cmd) {
	m.hostColors.Background = msg.Color
	m, _ = m.finishHostColors(false)
	return m.routeToForm(msg)
}

func (m Model) handleHostForeground(msg tea.ForegroundColorMsg) (Model, tea.Cmd) {
	m.hostColors.Foreground = msg.Color
	m, _ = m.finishHostColors(false)
	return m.routeToForm(msg)
}

// handleHostOsc takes the OSC replies Bubble Tea does not type.
// uv.UnknownOscEvent is the library's catch-all for every OSC it does not
// recognise, not a channel of ours, so most of what arrives here is not
// ours and the common path is the one that hands it straight back.
//
// Only a reply this palette actually asked for is consumed. Everything else
// keeps today's routing -- the default case's routeToForm -- because this
// change must not alter what any other message does on any other palette,
// and "swallow every unknown OSC" would be exactly that kind of quiet
// alteration.
func (m Model) handleHostOsc(msg uv.UnknownOscEvent) (Model, tea.Cmd) {
	idx, c, ok := theme.ParsePaletteReply(string(msg))
	if !ok || !m.hostWanted[idx] {
		return m.routeToForm(msg)
	}
	if m.hostColors.Palette == nil {
		m.hostColors.Palette = map[uint8]theme.Color{}
	}
	m.hostColors.Palette[idx] = c
	return m.finishHostColors(false)
}

// handleHostColorDeadline is the budget expiring: resolve with whatever
// arrived, which may be nothing.
func (m Model) handleHostColorDeadline(hostColorDeadlineMsg) (Model, tea.Cmd) {
	return m.finishHostColors(true)
}

// finishHostColors applies the answers once, and only once. It holds the
// single hostDone check for the reason the collectors' own doc gives.
//
// Before the deadline it waits for a COMPLETE set -- background, foreground
// and every index asked for -- rather than repainting on each arrival. Two
// reasons, and the second is the one that would bite. Repainting per answer
// would mean up to eight repaints where one will do, each of them a
// palette the floors have only half the information for. And the
// intermediate palettes are not merely incomplete but wrong in a specific
// way: theme.ResolveHost measures words against a focused-row fill derived
// from the background, so a word floored before the background arrived
// would be floored against a ground that does not exist yet, and then kept,
// because the next pass would find it already clearing its floor.
//
// At the deadline it resolves with whatever it has. theme.ResolveHost
// itself decides what a partial answer is worth: no background and it
// returns the palette untouched, a missing index and that field stays an
// index. There is no threshold here, deliberately -- "enough of an answer"
// is a question about colours, and it belongs in the package that measures
// them.
func (m Model) finishHostColors(deadline bool) (Model, tea.Cmd) {
	if m.hostDone {
		return m, nil
	}
	if !deadline && !m.hostColorsComplete() {
		return m, nil
	}
	m.hostDone = true

	if m.hostColors.Background == nil {
		// Nothing was learned, so there is nothing to repaint.
		// theme.ResolveHost would return the palette untouched anyway;
		// this is the cheap check that says so without pretending to
		// compare two Palettes. Comparing them with == would be a
		// runtime panic waiting for the first colour type that is not
		// comparable, which is not a bet worth taking to skip a repaint
		// nobody can see.
		return m, nil
	}
	resolved := theme.ResolveHost(m.palette, m.hostColors)
	m.palette = resolved
	m.form.SetPalette(resolved)
	return m, nil
}

// hostColorsComplete reports whether every answer asked for has arrived.
func (m Model) hostColorsComplete() bool {
	if m.hostColors.Background == nil || m.hostColors.Foreground == nil {
		return false
	}
	for idx, wanted := range m.hostWanted {
		if !wanted {
			continue
		}
		if _, ok := m.hostColors.Palette[idx]; !ok {
			return false
		}
	}
	return true
}
