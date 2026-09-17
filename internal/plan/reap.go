// reap.go is the one place herdr-draft's pane-reaper instruction exists
// (reap spec §4): the sentence, the rule for when a prompt can carry it,
// and the composition Build sends. It is in plan because Build composes
// the text and must stay pure, and because internal/form and
// internal/create ask the SAME predicate rather than keeping copies --
// one rule is what stops the row saying "reap when done" over a prompt
// the plan sends bare.
package plan

import "strings"

// ReapInstruction asks an agent to mark its own pane ready for pane-reaper
// once its work is saved. It is byte-identical to the dotfiles'
// _hspawn_reap_instruction (reap spec §2.2), and
// TestReapInstruction_IsHspawnsSentence holds that; change the two
// together. The embedded "$HERDR_PANE_ID" is literal on purpose: `herdr
// agent prompt` receives this as argv with no shell in between
// (herdrc.CLIRunner.AgentPrompt), so the AGENT's shell expands it, in the
// pane it names.
const ReapInstruction = `When your task is completely finished and its results are saved (committed, pushed, or written to a file), run this as the last command of your final turn so this pane closes itself: herdr pane report-metadata "$HERDR_PANE_ID" --source pane-reaper --token pane_reaper=ready — never while you are still waiting on a background job, a review, or a reply.`

// reapSeparator puts the instruction in a paragraph of its own. hspawn
// joins with a single space because its prompts are one line; a
// herdr-draft prompt is usually a multi-paragraph handoff, where a sentence
// glued to the last line reads as part of that line's point (reap spec
// §4.2).
const reapSeparator = "\n\n"

// ReapApplies reports whether prompt can carry ReapInstruction: it has
// content, and it is not a slash command. A slash command takes the rest of
// its line as arguments, so the sentence would reach it as arguments.
// Leading whitespace is trimmed before the "/" test, which is stricter than
// hspawn's raw test on purpose: wrongly skipping costs a reap, wrongly
// appending corrupts a command (reap spec §4.2).
func ReapApplies(prompt string) bool {
	t := strings.TrimSpace(prompt)
	return t != "" && !strings.HasPrefix(t, "/")
}

// PromptText is the text OpAgentPrompt sends for in: in.Prompt verbatim,
// or -- when in.MarkReady is set and ReapApplies -- the prompt with its own
// trailing whitespace trimmed, a blank line, and ReapInstruction.
func PromptText(in Input) string {
	if !in.MarkReady || !ReapApplies(in.Prompt) {
		return in.Prompt
	}
	return strings.TrimRight(in.Prompt, " \t\r\n") + reapSeparator + ReapInstruction
}
