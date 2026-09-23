package plan

import (
	"strings"
	"testing"
)

func TestBlockingDialogSignatureMatchesTrustDialog(t *testing.T) {
	// Verbatim (trimmed) from the v1 close-out live checkpoint's capture
	// (2026-09-01, herdr 0.8.2) of Claude Code's real first-run
	// "Accessing workspace" screen. This fixture IS the surviving record
	// of that screen: dialog.go's two signatures were read off it, so
	// replacing this text with something never observed live would throw
	// away the only evidence those two strings are real.
	screen := "❯ claude\n\n" +
		"─────────────────────────────\n" +
		" Accessing workspace:\n\n" +
		" /home/user/.herdr/worktrees/repo/branch\n\n" +
		" Quick safety check: Is this\n" +
		" a project you created or\n" +
		" one you trust? (Like your\n" +
		" own code, a well-known open\n" +
		" source project, or work\n" +
		" from your team). If not,\n" +
		" take a moment to review\n" +
		" what's in this folder\n" +
		" first.\n\n" +
		" Claude Code'll be able to\n" +
		" read, edit, and execute\n" +
		" files here.\n\n" +
		" Security guide\n\n" +
		" ❯ No, exit\n" +
		"   Yes, I trust this folder\n\n" +
		" Enter to confirm · Esc to\n" +
		" cancel\n"

	if got := blockingDialogSignature(screen); got == "" {
		t.Fatal("blockingDialogSignature = \"\", want a match against the real trust-dialog screen")
	}
}

// spendLimitScreen is Claude Code's monthly-spend-limit dialog, verbatim
// from the live pane recorded in #349. It is the surviving record of that
// screen in the same way the trust dialog above is of its own: the two
// signatures dialog.go carries for it were read off these lines.
//
// Note what it does NOT contain. There is no "Quick safety check" and no
// "Enter to confirm" -- so before #352 this screen matched nothing, and
// the guard that exists to stop a prompt's Enter answering a dialog let
// it through onto an option that raises the user's spending.
const spendLimitScreen = "You've hit your monthly spend limit · your weekly limit resets Sep 25, 4am\n" +
	"What do you want to do?\n" +
	"  > Adjust monthly spend limit: Unlimited\n" +
	"    Wait for limit to reset\n" +
	"Usage credit balance: $58.63\n"

func TestBlockingDialogSignatureMatchesSpendLimitDialog(t *testing.T) {
	if got := blockingDialogSignature(spendLimitScreen); got == "" {
		t.Fatal("blockingDialogSignature = \"\", want a match against the real spend-limit screen (#349)")
	}
}

// TestSpendLimitScreenCarriesNoOlderSignature is the guard that makes the
// test above mean what it says. Both screens are dialogs, so a fixture
// that happened to contain "Enter to confirm" would match for the trust
// prompt's reason and pass while the spend entries did nothing -- the
// second-reason shape CLAUDE.md warns about. This pins that the ONLY
// reason the spend screen matches is a spend signature.
func TestSpendLimitScreenCarriesNoOlderSignature(t *testing.T) {
	for _, sig := range []string{"Quick safety check", "Enter to confirm"} {
		if strings.Contains(spendLimitScreen, sig) {
			t.Errorf("the spend-limit fixture contains %q, so a match against it proves nothing about the spend signatures", sig)
		}
	}
}

// TestSpendLimitScreenIsOneListNotTwo records the decision #352 arrived
// at the long way round: the spend screen is in the SAME list as the
// trust prompt, used at both moments, and who decides after a send is
// promptOnScreen -- see plan's
// TestExecuteSpendDialogAfterASendIsDecidedByTheProbe and the note beside
// the entries in dialog.go.
//
// Kept as an assertion rather than a comment because the intermediate
// design -- a second, narrower list for the post-send check -- looks
// obviously right until you notice it forces the success verdict on a
// prompt the dialog ate.
func TestSpendLimitScreenIsOneListNotTwo(t *testing.T) {
	if got := blockingDialogSignature(spendLimitScreen); got == "" {
		t.Error("the spend screen must be refused, and by the same list the trust prompt is in")
	}
}

func TestBlockingDialogSignatureMatchesEachSignatureIndependently(t *testing.T) {
	for _, sig := range promptDialogSignatures {
		if got := blockingDialogSignature(sig); got != sig {
			t.Errorf("blockingDialogSignature(%q) = %q, want %q", sig, got, sig)
		}
	}
}

func TestBlockingDialogSignatureNoMatchOnOrdinaryChatOutput(t *testing.T) {
	ordinary := []string{
		"",
		"claude>  I've implemented the fix in main.go and added a test.\n" +
			"Let me know if you'd like me to run the test suite.",
		"╭─ Claude Code ─────────────────╮\n│ > implement the login flow    │\n╰────────────────────────────────╯",
		"$ ls\nREADME.md  go.mod  internal/",
	}
	for _, text := range ordinary {
		if got := blockingDialogSignature(text); got != "" {
			t.Errorf("blockingDialogSignature(%q) = %q, want \"\" (no dialog)", text, got)
		}
	}
}
