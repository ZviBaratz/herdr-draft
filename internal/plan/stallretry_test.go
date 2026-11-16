package plan

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
)

// --- #371: a stall can leave the prompt in the input box -------------------
//
// Live on 2026-11-16 (docs/manual-smoke.md, "Recorded runs"), a first send
// stalled with its text still in Claude Code's input box: herdr's Enter,
// 300 ms after the text, was not acted on. The retry typed a second copy
// behind it and the pane submitted both as one turn, reported `sent`.
//
// The screens below are shaped like that session's own `agent read
// --source detection --format text`: the transcript, then the input box
// between two rules, then the footer.

const inputBoxRuleLine = "────────────────────────────────────────────────────────────"

// claudeScreen draws a Claude Code screen whose input box holds box, which
// may span lines.
func claudeScreen(transcript, box string) string {
	return " ▐▛███▛█   Claude Code v2.1.325\n▝▜██████▀  Haiku 5.5 · Claude Ultra\n\n" +
		transcript + "\n" +
		inputBoxRuleLine + "\n" +
		"❯ " + box + "\n" +
		inputBoxRuleLine + "\n" +
		"  ⏵⏵ auto mode on (shift+tab to cycle)\n"
}

func TestPromptLeftInInputBox(t *testing.T) {
	const prompt = "implement the fix for the stalled retry"
	// Numbered so that no stretch of it looks like its start: a box whose
	// start has scrolled away must be recognised by its end alone.
	var numbered []string
	for i := 1; i <= 150; i++ {
		numbered = append(numbered, fmt.Sprintf("w%d", i))
	}
	longPrompt := "Start of the handoff: " + strings.Join(numbered, " ") + " and stop there."
	wrapped := wrapForBox(longPrompt, 60)
	scrolledToEnd := "  " + strings.Join(strings.Split(wrapped, "\n  ")[3:], "\n  ")

	for _, tc := range []struct {
		name   string
		screen string
		text   string
		want   bool
	}{
		{"the prompt, inline", claudeScreen("", prompt), prompt, true},
		{"a long prompt, wrapped across the box", claudeScreen("", wrapped), longPrompt, true},
		{"a long prompt whose start has scrolled out of the box", claudeScreen("", scrolledToEnd), longPrompt, true},
		{"a large paste, collapsed with its line count", claudeScreen("", "[Pasted text #1 +59 lines]"), "line\n" + prompt, true},
		{"a long single-line paste, collapsed without one", claudeScreen("", "[Pasted text #2]"), longPrompt, true},
		// The ghost text Claude Code shows in an empty box reads, in plain
		// text, exactly like something typed.
		{"an empty box showing ghost text", claudeScreen("", `Try "write a test for create_test.go"`), prompt, false},
		{"an empty box", claudeScreen("", ""), prompt, false},
		// A submitted prompt carries the same marker in the transcript; the
		// input box is the LAST one.
		{"the prompt already submitted, box empty", claudeScreen("❯ "+prompt+"\n\n● On it.", ""), prompt, false},
		{"something else in the box", claudeScreen("", "a different request entirely"), prompt, false},
		{"no input box at all", "> Sonnet 5 · claude-code\n  Type your message...\n", prompt, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := promptLeftInInputBox(tc.screen, tc.text); got != tc.want {
				t.Errorf("promptLeftInInputBox = %v, want %v for screen:\n%s", got, tc.want, tc.screen)
			}
		})
	}
}

// wrapForBox breaks text into lines of at most width runes, indenting every
// line after the first by two spaces, the way Claude Code continues its
// input box. It splits mid-word, as a terminal wrap does.
func wrapForBox(text string, width int) string {
	runes := []rune(text)
	var lines []string
	for len(runes) > width {
		lines = append(lines, string(runes[:width]))
		runes = runes[width:]
	}
	lines = append(lines, string(runes))
	return strings.Join(lines, "\n  ")
}

// stalledOnceMock is a run whose first send stalls, and whose pane, from
// the retry's guard read on, shows afterStall.
func stalledOnceMock(afterStall string) *mockRunner {
	return &mockRunner{
		topo:               herdrc.CreatedTopology{WorkspaceID: "ws-1", PaneID: "pane-1"},
		failAt:             "AgentPrompt",
		failErr:            stalledPromptErr(),
		failCount:          1,
		readText:           afterStall,
		readTextShowsAfter: 1,
	}
}

// isReactionWait reports whether a wait is the one after an Enter: the
// statuses AgentPrompt's own `--until` names, with a real budget.
func isReactionWait(timeout time.Duration, until []herdrc.AgentStatus) bool {
	return timeout > 0 && slices.Equal(until, herdrc.PromptLandedStatuses())
}

// TestExecuteStallWithThePromptLeftInTheBoxPressesEnter is #371's fix: the
// text is already in front of the agent, so the retry submits it rather
// than typing a second copy behind it.
func TestExecuteStallWithThePromptLeftInTheBoxPressesEnter(t *testing.T) {
	withPromptRetrySettle(t, 0)
	in := validInput()
	in.UseWorktree = true
	in.Prompt = "implement the fix"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := stalledOnceMock(claudeScreen("", "implement the fix"))
	m.agentWait = func(timeout time.Duration, until []herdrc.AgentStatus) (bool, error) {
		return isReactionWait(timeout, until), nil
	}

	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

	if result.FailedIndex != -1 {
		t.Fatalf("FailedIndex = %d, want -1: %+v", result.FailedIndex, result)
	}
	if n := countCallsWithPrefix(m.calls, "AgentPrompt"); n != 1 {
		t.Errorf("AgentPrompt called %d times, want 1 -- a second send types a second copy: %v", n, m.calls)
	}
	if !containsCall(m.calls, "PaneSendKeys(pane-1,Enter)") {
		t.Errorf("calls = %v, want Enter pressed on the pane", m.calls)
	}
	if !containsCall(m.calls, "AgentWait(pane-1,5s,working|blocked)") {
		t.Errorf("calls = %v, want the reaction watched for with the prompt's own statuses", m.calls)
	}
	if result.PromptUnconfirmed || result.PromptText != "" {
		t.Errorf("PromptUnconfirmed = %v, PromptText = %q; want a delivered prompt", result.PromptUnconfirmed, result.PromptText)
	}
}

// TestExecuteStallWithAPasteLeftInTheBoxPressesEnter: a large prompt is not
// on the screen at all, only Claude Code's placeholder for it.
func TestExecuteStallWithAPasteLeftInTheBoxPressesEnter(t *testing.T) {
	withPromptRetrySettle(t, 0)
	in := validInput()
	in.UseWorktree = true
	in.Prompt = strings.Repeat("a line of the handoff\n", 60)
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := stalledOnceMock(claudeScreen("", "[Pasted text #1 +59 lines]"))
	m.agentWait = func(timeout time.Duration, until []herdrc.AgentStatus) (bool, error) {
		return isReactionWait(timeout, until), nil
	}

	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

	if result.FailedIndex != -1 {
		t.Fatalf("FailedIndex = %d, want -1: %+v", result.FailedIndex, result)
	}
	if n := countCallsWithPrefix(m.calls, "AgentPrompt"); n != 1 {
		t.Errorf("AgentPrompt called %d times, want 1: %v", n, m.calls)
	}
	if !containsCall(m.calls, "PaneSendKeys(pane-1,Enter)") {
		t.Errorf("calls = %v, want Enter pressed on the pane", m.calls)
	}
}

// TestExecuteStallWithThePromptLeftInTheBoxAndNoReaction: Enter pressed and
// nothing happened either. The text is in the box, so typing it again is
// still wrong; the user is told where it is and what to press.
func TestExecuteStallWithThePromptLeftInTheBoxAndNoReaction(t *testing.T) {
	withPromptRetrySettle(t, 0)
	in := validInput()
	in.UseWorktree = true
	in.Prompt = "implement the fix"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := stalledOnceMock(claudeScreen("", "implement the fix"))

	var progressed []Progress
	result := Execute(context.Background(), m, ops, ExecOpts{},
		func(p Progress) { progressed = append(progressed, p) })

	if want := indexOfKind(ops, OpAgentPrompt); result.FailedIndex != want {
		t.Fatalf("FailedIndex = %d, want %d (the prompt op): %+v", result.FailedIndex, want, result)
	}
	if n := countCallsWithPrefix(m.calls, "AgentPrompt"); n != 1 {
		t.Errorf("AgentPrompt called %d times, want 1: %v", n, m.calls)
	}
	if !result.PromptUnconfirmed {
		t.Errorf("PromptUnconfirmed = false, want true: the text was typed")
	}
	if result.promptUnconfirmedCause != causeLeftInInputBox {
		t.Errorf("cause = %q, want %q", result.promptUnconfirmedCause, causeLeftInInputBox)
	}
	msg := progressed[len(progressed)-1].Err.Error()
	for _, want := range []string{"input box", "press Enter"} {
		if !strings.Contains(msg, want) {
			t.Errorf("step message = %q, want it to say %q", msg, want)
		}
	}
	decision := CleanCheck(context.Background(), in, result)
	if decision.Allowed {
		t.Errorf("CleanCheck allowed a clean over a prompt in the input box")
	}
	if !strings.Contains(decision.Reason, "input box") {
		t.Errorf("clean reason = %q, want it to say where the prompt is", decision.Reason)
	}
}

// TestExecuteStallWhoseFirstSendLandedLateIsNotSentAgain: herdr stops
// watching after five seconds, and an Enter acted on after that is a
// prompt delivered, not one to send again. The box is empty and the agent
// is working.
func TestExecuteStallWhoseFirstSendLandedLateIsNotSentAgain(t *testing.T) {
	withPromptRetrySettle(t, 0)
	in := validInput()
	in.UseWorktree = true
	in.Prompt = "implement the fix"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := stalledOnceMock(claudeScreen("❯ implement the fix\n\n● On it.", ""))
	m.agentWait = func(timeout time.Duration, until []herdrc.AgentStatus) (bool, error) {
		return timeout == 0 && slices.Equal(until, []herdrc.AgentStatus{herdrc.AgentWorking}), nil
	}

	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

	if result.FailedIndex != -1 {
		t.Fatalf("FailedIndex = %d, want -1: %+v", result.FailedIndex, result)
	}
	if n := countCallsWithPrefix(m.calls, "AgentPrompt"); n != 1 {
		t.Errorf("AgentPrompt called %d times, want 1 -- the agent is already on it: %v", n, m.calls)
	}
	if n := countCallsWithPrefix(m.calls, "PaneSendKeys"); n != 0 {
		t.Errorf("PaneSendKeys called %d times, want 0: %v", n, m.calls)
	}
}

// TestExecuteStallWithAnEmptyBoxTypesThePromptAgain is #132's own case, and
// it keeps its retry: nothing in the box, nothing happening, so the one
// copy that goes in is the retry's.
func TestExecuteStallWithAnEmptyBoxTypesThePromptAgain(t *testing.T) {
	withPromptRetrySettle(t, 0)
	in := validInput()
	in.UseWorktree = true
	in.Prompt = "implement the fix"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := stalledOnceMock(claudeScreen("", `Try "write a test for create_test.go"`))

	result := Execute(context.Background(), m, ops, ExecOpts{}, nil)

	if result.FailedIndex != -1 {
		t.Fatalf("FailedIndex = %d, want -1: %+v", result.FailedIndex, result)
	}
	if n := countCallsWithPrefix(m.calls, "AgentPrompt"); n != 2 {
		t.Errorf("AgentPrompt called %d times, want 2 -- one stall, one retry: %v", n, m.calls)
	}
	if n := countCallsWithPrefix(m.calls, "PaneSendKeys"); n != 0 {
		t.Errorf("PaneSendKeys called %d times, want 0: %v", n, m.calls)
	}
	// One guard read before each send and one check after the one that
	// landed: the retry's guard read is the one the box was judged from,
	// not an extra read in front of it.
	if n := countCallsWithPrefix(m.calls, "AgentRead"); n != 3 {
		t.Errorf("AgentRead called %d times, want 3: %v", n, m.calls)
	}
}

// TestExecuteStallThenEnterFailsIsUnconfirmed: pressing Enter is itself a
// herdr call that can fail. The text is in the pane either way, so the
// posture stays unconfirmed and nothing is typed again.
func TestExecuteStallThenEnterFailsIsUnconfirmed(t *testing.T) {
	withPromptRetrySettle(t, 0)
	in := validInput()
	in.UseWorktree = true
	in.Prompt = "implement the fix"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := stalledOnceMock(claudeScreen("", "implement the fix"))
	failing := &failingSendKeys{mockRunner: m}

	result := Execute(context.Background(), failing, ops, ExecOpts{}, nil)

	if want := indexOfKind(ops, OpAgentPrompt); result.FailedIndex != want {
		t.Fatalf("FailedIndex = %d, want %d: %+v", result.FailedIndex, want, result)
	}
	if !result.PromptUnconfirmed {
		t.Error("PromptUnconfirmed = false, want true")
	}
	if n := countCallsWithPrefix(m.calls, "AgentPrompt"); n != 1 {
		t.Errorf("AgentPrompt called %d times, want 1: %v", n, m.calls)
	}
}

// failingSendKeys is a mockRunner whose PaneSendKeys fails, which the
// shared failAt dial cannot say while AgentPrompt holds it.
type failingSendKeys struct{ *mockRunner }

func (f *failingSendKeys) PaneSendKeys(ctx context.Context, paneID string, keys ...string) error {
	f.record("PaneSendKeys", append([]string{paneID}, keys...)...)
	return errors.New("herdr pane send-keys pane-1 Enter: exit status 1")
}
