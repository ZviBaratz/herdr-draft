package plan

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
)

// A stall does not always leave an empty input box (#371).
//
// #132 measured the stall's dominant cause on 2026-09-09: for a second or so
// after a trust dialog clears, Claude Code reports ready while its TUI is not
// accepting input, herdr's typing goes nowhere, and the pane is left with an
// empty buffer. Sending again then delivers the prompt exactly once. On
// 2026-11-16 a stall left the opposite: the TEXT was accepted and herdr's
// Enter, sent 300 ms after it (AGENT_PROMPT_SUBMIT_DELAY,
// https://github.com/herdrdev/herdr/blob/v0.9.0/src/app/api/agents.rs#L13),
// was not. Typing again put a second copy behind the first, and the agent got
// one turn with the prompt in it twice. `create` reported it `sent`.
//
// So the retry reads the input box before it types. Measured live against
// Claude Code 2.1.325 in a herdr 0.9.1 pane on 2026-11-16:
//
//   - text left in the box shows there inline, after the box's marker and
//     wrapped onto indented lines, or, for a large paste, as a placeholder:
//     "[Pasted text #1 +59 lines]" for 60 lines, "[Pasted text #2]" for one
//     line of 1,963 characters;
//   - `pane send-keys <pane> Enter` submits it, and `agent wait --until
//     working --until blocked` saw the agent react 0.42 s later;
//   - Enter on an empty box starts no turn;
//   - an empty box shows ghost text ("Try \"write a test for …\"") that a
//     plain-text read cannot tell from typed text, which is why the box is
//     matched against the prompt itself and never judged by being non-empty;
//   - ctrl+c does clear the box, and also arms Claude Code's "Press Ctrl-C
//     again to exit", which is why the text is submitted and never cleared
//     and typed again.

// inputBoxMarker is the glyph Claude Code starts its input box with. Every
// submitted prompt in its transcript starts with it too, which is why the
// box is the LAST line carrying it.
const inputBoxMarker = "❯"

// inputBoxEdge is the box-drawing rune Claude Code draws its input box's top
// and bottom edges with.
const inputBoxEdge = '─'

// pastedTextPlaceholder opens what Claude Code shows in its input box in
// place of a large paste, with or without a line count after the number.
// herdr-draft is the only thing that has typed into the pane at this point,
// so a placeholder in the box is its prompt.
const pastedTextPlaceholder = "[Pasted text #"

// enterKey is herdr's name for the Enter key in `pane send-keys`.
const enterKey = "Enter"

// promptReactionWait is how long the agent gets to react once Enter is
// pressed on a prompt left in its box: the same five seconds herdr gives a
// send before it calls it a stall (AGENT_PROMPT_EFFECT_TIMEOUT_MS,
// https://github.com/herdrdev/herdr/blob/v0.9.0/src/api/wait.rs#L20). The
// reaction measured live took 0.42 s.
const promptReactionWait = 5 * time.Second

// statusNow is AgentWait's budget for asking which status the agent is in
// now, rather than waiting for one.
const statusNow time.Duration = 0

// errPromptLeftInInputBox is the retry's own verdict when Enter on a prompt
// left in the box got no reaction either. Text was typed once and is most
// likely still there, so it takes the unconfirmed posture
// (causeLeftInInputBox), and it is never answered by typing the prompt again.
var errPromptLeftInInputBox = errors.New("the prompt is most likely still in the agent's input box, unsubmitted")

// retryStalledPrompt is the one retry a stalled send gets, and it decides
// from the pane what that retry is.
//
//   - The prompt is still in the input box: press Enter on it and watch for
//     the reaction (submitPromptLeftInInputBox). Typing it again is #371.
//   - It is not, and the agent is working: the first send's Enter was acted
//     on after herdr stopped watching. The prompt is delivered, and a second
//     copy would be queued behind it as a second turn.
//   - Otherwise: type it again (#132's case), with retype.
//
// The guard read runs first and refuses exactly as the first send's does,
// worded for text that has already gone out (textAlreadySent). The box is
// judged from that same read.
func retryStalledPrompt(ctx context.Context, r herdrc.Runner, req herdrc.AgentPromptReq,
	textAlreadySent bool, retype func() error) error {
	screen, err := guardPromptScreen(ctx, r, req.Target, textAlreadySent)
	if err != nil {
		return err
	}
	if promptLeftInInputBox(screen, req.Text) {
		return submitPromptLeftInInputBox(ctx, r, req)
	}
	// An error here is no evidence either way, so the retry goes on as it
	// would have before the question was asked.
	if landedLate, err := r.AgentWait(ctx, req.Target, statusNow, herdrc.AgentWorking); err == nil && landedLate {
		return confirmPromptLanded(ctx, r, req)
	}
	return retype()
}

// submitPromptLeftInInputBox presses Enter on the prompt a stall left in the
// agent's input box, and watches for the same reaction AgentPrompt's own
// wait looks for (herdrc.PromptLandedStatuses). A reaction is followed by
// the post-send check every delivered prompt gets.
func submitPromptLeftInInputBox(ctx context.Context, r herdrc.Runner, req herdrc.AgentPromptReq) error {
	if err := r.PaneSendKeys(ctx, req.Target, enterKey); err != nil {
		return fmt.Errorf("could not press Enter on the prompt a stalled send left in the agent's input "+
			"box -- read the pane: %w", err)
	}
	reacted, err := r.AgentWait(ctx, req.Target, promptReactionWait, herdrc.PromptLandedStatuses()...)
	if err != nil {
		return fmt.Errorf("pressed Enter on the prompt a stalled send left in the agent's input box, "+
			"and could not watch for the agent's reaction -- read the pane: %w", err)
	}
	if !reacted {
		return fmt.Errorf("the agent did not react to the prompt or to Enter pressed on it again -- "+
			"read the pane, and if the prompt is in its input box, press Enter there rather than "+
			"pasting it: %w", errPromptLeftInInputBox)
	}
	return confirmPromptLanded(ctx, r, req)
}

// promptLeftInInputBox reports whether screen's input box holds text: the
// prompt itself, from its start or (when the start has scrolled out of the
// box) its end, or Claude Code's placeholder for a large paste.
//
// It answers only on positive evidence, and its errors run toward "no":
// that is a retype, which is what the retry always did, and never Enter
// pressed into an input box holding something else.
func promptLeftInInputBox(screen, text string) bool {
	box, found := inputBoxText(screen)
	if !found {
		return false
	}
	boxText := squeezeSpace(box)
	if strings.HasPrefix(boxText, squeezeSpace(pastedTextPlaceholder)) {
		return true
	}
	prompt := []rune(squeezeSpace(text))
	if len(prompt) == 0 {
		return false
	}
	probeLength := min(len(prompt), promptProbeRunes)
	promptStart := string(prompt[:probeLength])
	promptEnd := string(prompt[len(prompt)-probeLength:])
	return strings.HasPrefix(boxText, promptStart) || strings.HasSuffix(boxText, promptEnd)
}

// inputBoxText is what Claude Code's input box holds on screen: the text
// after the last line's inputBoxMarker, and the lines under it down to the
// box's bottom edge. found is false for a screen with no input box on it,
// which includes every agent that does not draw one this way.
func inputBoxText(screen string) (text string, found bool) {
	lines := strings.Split(screen, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		first, isBoxLine := strings.CutPrefix(strings.TrimLeft(lines[i], " "), inputBoxMarker)
		if !isBoxLine {
			continue
		}
		boxLines := []string{first}
		for _, line := range lines[i+1:] {
			if isInputBoxEdge(line) {
				break
			}
			boxLines = append(boxLines, line)
		}
		return strings.Join(boxLines, "\n"), true
	}
	return "", false
}

// isInputBoxEdge reports whether line is one of the input box's edges: a
// line of inputBoxEdge and nothing else.
func isInputBoxEdge(line string) bool {
	trimmed := strings.TrimSpace(line)
	return trimmed != "" && strings.Trim(trimmed, string(inputBoxEdge)) == ""
}
