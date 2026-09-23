// dialog.go is a small, explicit signature list for detecting when a pane
// herdr's own agent detection reports as idle/interactive_ready is
// actually showing a blocking confirmation/selection dialog rather than a
// normal chat-input state.
//
// Task 19's live checkpoint found that herdr 0.8.2's own agent detection
// reported Claude Code's first-run "Accessing workspace" trust/security
// confirmation screen as idle and interactive_ready -- exactly like its
// normal ready-for-input state (no distinct "blocked"/"waiting" status).
// Before this file existed, exec.go's OpAgentPrompt handling trusted that
// signal literally and sent the composed prompt text + Enter straight into
// the pane: the dialog isn't a text field, so the prompt text was silently
// dropped, but the trailing Enter confirmed whatever option the dialog had
// highlighted (observed live: "No, exit"), killing the freshly launched
// agent with no error surfaced anywhere -- `agent prompt --wait` itself
// reports success.
//
// That prediction came true. This file used to say herdr's detection
// manifests "can in principle learn to recognize this exact screen ...
// which would let this guard be relaxed or removed later"; herdr 0.9.0's
// manifest learned it, and re-verified live on 2026-09-08 (#90) the same
// screen now reports `agent_status: "blocked"`. Both calls herdr-draft
// makes refuse it upstream, under two different error codes: `agent start`
// with agent_not_ready
// (https://github.com/herdrdev/herdr/blob/b1ff4582/src/cli/agent.rs#L607)
// and `agent prompt` with agent_blocked
// (https://github.com/herdrdev/herdr/blob/b1ff4582/src/app/api/agents.rs#L85),
// before any input is sent.
//
// The guard STAYS, and is no longer only a guard. Two reasons.
//
// It is not redundant: herdr's manifest knows THIS ONE SCREEN, not every
// dialog. A login-method chooser, a permission prompt, a
// resume-or-start-fresh selector -- any of those still reaches an
// interactive_ready pane that is not safe to type into, and the upstream
// codes are a second line of defence rather than a replacement for the
// first. Removing a local check because one instance of the class was
// fixed upstream is how the original defect gets a second life under a
// different screen.
//
// And it is now what turns a refusal into an explanation. exec.go's
// explainBlockedStart reads the pane on an agent_not_ready start and
// matches it here, which is the difference between herdr's "agent is
// blocked during startup" and telling the user their agent is fine and
// waiting on one keystroke.
//
// So: exec.go reads the pane before ever sending a prompt (Runner.
// AgentRead) and refuses to send if the screen looks like a dialog, rather
// than trusting "herdr says detected" to mean "safe to type into".
//
// Keep this signature list SMALL and EXPLICIT: every entry is verbatim
// text observed live from a real Claude Code dialog, and each screen is
// preserved verbatim in a dialog_test.go fixture that is the surviving
// record of it. Two entries came off the first-run trust screen at the v1
// close-out live checkpoint (2026-09-01, herdr 0.8.2) and two off the
// monthly-spend-limit screen in #349 (2026-09-23). Not a broad heuristic
// that risks false-positiving on ordinary chat output and blocking a
// legitimate prompt. A heuristic here is acceptable -- silently destroying
// an agent, the alternative, is not.
package plan

import "strings"

// promptDialogSignatures are substrings that, if present anywhere in a
// pane's detection-source screen text, mark it as a blocking confirmation/
// selection dialog rather than a normal ready-for-input state. Any one
// match is sufficient, so the order does not affect WHETHER a screen
// matches -- only which signature blockingDialogSignature reports.
//
// It is ordered most-nameable first, because that report is now quoted
// back to the user (exec.go's explainBlockedStart and promptIfReady both
// put it in their failure message) and the entries are not equally useful
// there -- each screen's heading leads, its looser signal follows. "it is showing \"Quick safety check\"" names a screen
// someone can match against their own pane; "it is showing \"Enter to
// confirm\"" describes a footer that half of every TUI has. Both are
// equally good at DETECTING -- keeping both is what makes the match
// survive a Claude Code release that rewords either one -- so leading with
// the heading costs nothing and is what the reader actually sees.
var promptDialogSignatures = []string{
	// The screen's own heading: the one line that says what is being asked.
	"Quick safety check",
	// Claude Code's monthly-spend-limit screen (#349), which is the second
	// member of the class this file's header predicted and the reason the
	// guard was kept: herdr's manifest knows the trust prompt, not the
	// class. Both lines are verbatim from a live pane, recorded in the
	// issue and in dialog_test.go's fixture.
	//
	// It is worth being explicit about what an unguarded send does here,
	// because it is worse than the founding case. That one answered "No,
	// exit" and destroyed a fresh agent; this one's highlighted option is
	// "Adjust monthly spend limit: Unlimited", so the trailing Enter
	// raises the user's own spending cap -- a decision that is theirs,
	// that costs money, and that nobody saw. The reachable path is
	// narrow but real: `agent prompt` answers agent_prompt_stalled when
	// herdr observes no working or blocked state, exec.go retries such a
	// send exactly once through promptIfReady, and without these two
	// entries that retry's guard passes.
	//
	// The heading first, for the reason the ordering comment above gives.
	"hit your monthly spend limit",
	// The highlighted option, as a second independent signal, and the one
	// that still matches if the heading is reworded. Spelled without the
	// apostrophe the heading carries ("You've") so that a straight-quote
	// to typographic-quote change upstream cannot silently unmatch both
	// at once -- the same reason the trust prompt keeps two entries.
	"Adjust monthly spend limit",
	// The trust screen's footer hint -- distinctive phrasing unlikely to
	// appear in ordinary agent chat output, and present regardless of
	// exactly how the rest of that screen is worded. Kept as an
	// independent signal in case a future Claude Code version reworks the
	// heading above. Last because it is the loosest: it is also the
	// permission-prompt footer, so it names a screen least usefully when
	// quoted back.
	"Enter to confirm",
}

// swallowingDialogSignatures is the subset of the list above that a
// dialog seen AFTER a send may be blamed on, and it is a subset for one
// reason: the two questions are not the same question.
//
// Before a send, the question is "is this screen unsafe to type into?",
// and every entry above answers yes. After a send, confirmPromptLanded
// asks the narrower "is this screen evidence the text did not land?" --
// and for the spend dialog the answer is the opposite. That screen
// appears BECAUSE the agent took the prompt and began working; #349
// recorded it arriving under a truthful `prompt_status: sent`, and this
// PR's own section 8 tells the reader so. Matching it there turned a
// delivered prompt into errPromptSwallowed: a create that exits 1 and
// tells the user a dialog ate their text, about a prompt the agent has
// (#352's review caught it before it shipped).
//
// The startup dialogs are the other way round. A trust prompt is on
// screen before the agent can process anything, so finding one after a
// send means the send went into it. "Enter to confirm" is kept with
// them, and it is the loose one -- it is also Claude Code's
// permission-prompt footer, which an agent that RECEIVED the prompt can
// raise (CLAUDE.md says so) -- but promptIsVerifiable and promptOnScreen
// already gate that case, and dropping it would lose the footer's cover
// for a reworded trust screen.
//
// So: a new signature goes in the list above always, and in this one only
// if seeing it after a send is evidence the prompt was NOT received.
var swallowingDialogSignatures = []string{
	"Quick safety check",
	"Enter to confirm",
}

// swallowingDialogSignature is blockingDialogSignature over that subset,
// for the post-send check alone.
func swallowingDialogSignature(text string) string {
	return firstSignature(text, swallowingDialogSignatures)
}

// blockingDialogSignature reports the first promptDialogSignatures entry
// found in text, or "" if none match. text is expected to be a pane's
// `agent read --source detection --format text` output (see
// herdrc.CLIRunner.AgentRead) -- a pure function over that text, with no
// I/O of its own, so it is trivially unit-testable without a Runner.
func blockingDialogSignature(text string) string {
	return firstSignature(text, promptDialogSignatures)
}

// firstSignature is the shared scan, so the two lists cannot drift in how
// they are matched -- only in what they contain.
func firstSignature(text string, signatures []string) string {
	for _, sig := range signatures {
		if strings.Contains(text, sig) {
			return sig
		}
	}
	return ""
}
