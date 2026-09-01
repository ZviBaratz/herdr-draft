package plan

import "testing"

func TestBlockingDialogSignatureMatchesTrustDialog(t *testing.T) {
	// Verbatim (trimmed) from task-19-report.md's live transcript of
	// Claude Code's real first-run "Accessing workspace" screen.
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
