package plan

import (
	"strings"
	"testing"
)

// TestReapInstruction_IsHspawnsSentence pins reap spec §2.2: the sentence is
// byte-identical to the dotfiles' _hspawn_reap_instruction, verified live on
// 2026-09-17. The literal is repeated here rather than read from the
// dotfiles, which this repository cannot reach, so an edit to the constant
// has to change this test too and a reviewer sees both (reap spec §4.3).
func TestReapInstruction_IsHspawnsSentence(t *testing.T) {
	const hspawn = `When your task is completely finished and its results are saved (committed, pushed, or written to a file), run this as the last command of your final turn so this pane closes itself: herdr pane report-metadata "$HERDR_PANE_ID" --source pane-reaper --token pane_reaper=ready — never while you are still waiting on a background job, a review, or a reply.`
	if ReapInstruction != hspawn {
		t.Errorf("ReapInstruction drifted from hspawn's sentence:\ngot:  %q\nwant: %q", ReapInstruction, hspawn)
	}
	// The part pane-reaper actually reads. Pinned on its own so a rewording
	// of the prose around it cannot quietly break the contract.
	const contract = `herdr pane report-metadata "$HERDR_PANE_ID" --source pane-reaper --token pane_reaper=ready`
	if !strings.Contains(ReapInstruction, contract) {
		t.Errorf("ReapInstruction does not carry the contract command %q", contract)
	}
	if strings.ContainsAny(ReapInstruction, "\r\n") {
		t.Error("ReapInstruction spans lines; hspawn's is one line")
	}
}

func TestReapApplies(t *testing.T) {
	for _, tc := range []struct {
		name   string
		prompt string
		want   bool
	}{
		{"empty", "", false},
		{"whitespace only", " \n\t ", false},
		{"slash command", "/loop 5m check CI", false},
		{"slash command after leading whitespace", "  \n/loop 5m", false},
		{"ordinary prompt", "fix the login redirect loop", true},
		{"a slash mid-text is not a command", "look at src/app/state.rs", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ReapApplies(tc.prompt); got != tc.want {
				t.Errorf("ReapApplies(%q) = %v, want %v", tc.prompt, got, tc.want)
			}
		})
	}
}

func TestPromptText(t *testing.T) {
	for _, tc := range []struct {
		name      string
		prompt    string
		markReady bool
		want      string
	}{
		{"off leaves the prompt verbatim", "fix it\n", false, "fix it\n"},
		{"on appends after one blank line", "fix it", true, "fix it\n\n" + ReapInstruction},
		{"on trims the prompt's own trailing whitespace first", "fix it\n\n  \n", true, "fix it\n\n" + ReapInstruction},
		{"on never touches a slash command", "/loop 5m", true, "/loop 5m"},
		{"on with no prompt sends no prompt", "", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := PromptText(Input{Prompt: tc.prompt, MarkReady: tc.markReady}); got != tc.want {
				t.Errorf("PromptText = %q, want %q", got, tc.want)
			}
		})
	}
}
