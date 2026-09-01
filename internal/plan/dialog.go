// dialog.go is a small, explicit signature list for detecting when a pane
// herdr's own agent detection reports as idle/interactive_ready is
// actually showing a blocking confirmation/selection dialog rather than a
// normal chat-input state.
//
// Task 19's live checkpoint found that this herdr version's own agent
// detection reports Claude Code's first-run "Accessing workspace"
// trust/security confirmation screen as idle and interactive_ready --
// exactly like its normal ready-for-input state (no distinct
// "blocked"/"waiting" status). Before this file existed, exec.go's
// OpAgentPrompt handling trusted that signal literally and sent the
// composed prompt text + Enter straight into the pane: the dialog isn't a
// text field, so the prompt text was silently dropped, but the trailing
// Enter confirmed whatever option the dialog had highlighted (observed
// live: "No, exit"), killing the freshly launched agent with no error
// surfaced anywhere -- `agent prompt --wait` itself reports success. This
// is a candidate herdr issue (its own detection manifest not
// distinguishing this screen from a normal ready state), not something
// herdr-draft can fix from its own side -- see task-19-report.md's fix
// section. What this file adds is a defensive guard on herdr-draft's own
// side: exec.go reads the pane before ever sending a prompt (Runner.
// AgentRead) and refuses to send if the screen looks like a dialog,
// rather than trusting "herdr says detected" to mean "safe to type into".
//
// Keep this signature list SMALL and EXPLICIT: every entry is verbatim
// text observed live from Claude Code's actual trust-dialog screen
// (task-19-report.md's Step 1/Step 2 transcripts), not a broad heuristic
// that risks false-positiving on ordinary chat output and blocking a
// legitimate prompt. A heuristic here is acceptable -- silently destroying
// an agent, the alternative, is not.
package plan

import "strings"

// promptDialogSignatures are substrings that, if present anywhere in a
// pane's detection-source screen text, mark it as a blocking confirmation/
// selection dialog rather than a normal ready-for-input state. Any one
// match is sufficient; order is significant only in that the first match
// is what blockingDialogSignature reports.
var promptDialogSignatures = []string{
	// The trust-dialog screen's own footer hint -- distinctive phrasing
	// unlikely to appear in ordinary agent chat output, and present
	// regardless of exactly how the rest of the screen is worded.
	"Enter to confirm",
	// The screen's own heading -- kept as a second, independent signal in
	// case a future Claude Code version reorders or trims the footer but
	// keeps this heading.
	"Quick safety check",
}

// blockingDialogSignature reports the first promptDialogSignatures entry
// found in text, or "" if none match. text is expected to be a pane's
// `agent read --source detection --format text` output (see
// herdrc.CLIRunner.AgentRead) -- a pure function over that text, with no
// I/O of its own, so it is trivially unit-testable without a Runner.
func blockingDialogSignature(text string) string {
	for _, sig := range promptDialogSignatures {
		if strings.Contains(text, sig) {
			return sig
		}
	}
	return ""
}
