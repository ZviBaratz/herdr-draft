package picker

import (
	"context"
	"os"
	"os/exec"
	"testing"
)

// TestAgainstARealPicker is the one test in this package that runs a picker
// nobody in this repository wrote. It is SKIPPED unless HERDR_DRAFT_PICKER
// names an executable, so it never runs in CI or on a contributor's machine
// -- which is the point: the protocol is the contract, and no particular
// implementation may become a prerequisite for a green tree.
//
// Run it with:
//
//	HERDR_DRAFT_PICKER=$(command -v claude-pick) go test ./internal/picker/ -run Real -v
//
// What it proves that the stubs cannot: that the documented key list, the
// documented flags and the documented exit codes describe something that
// actually exists. A protocol only this repository's own stubs satisfy has
// one conformant implementation and it is fictional.
func TestAgainstARealPicker(t *testing.T) {
	bin := os.Getenv("HERDR_DRAFT_PICKER")
	if bin == "" {
		t.Skip("set HERDR_DRAFT_PICKER to an executable implementing the picker protocol")
	}
	if _, err := exec.LookPath(bin); err != nil {
		t.Skipf("HERDR_DRAFT_PICKER=%q: %v", bin, err)
	}
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	c := CLI{Bin: bin}
	if err := c.Probe(context.Background(), dir); err != nil {
		t.Fatalf("Probe(%s): %v", bin, err)
	}

	res, err := c.Pick(context.Background(), dir, Options{DryRun: true})
	if err != nil {
		// A refusal is a conformant answer, not a failure of the protocol.
		t.Logf("Pick refused (still conformant): %v", err)
		return
	}
	if res.Profile == "" {
		t.Fatal("a successful pick must name a profile")
	}
	if res.ConfigDir != "" {
		t.Errorf("--dry-run must not build an account dir, got config_dir=%q", res.ConfigDir)
	}
	t.Logf("real picker chose %q (tenant %q, tier %q)", res.Profile, res.Tenant, res.Tier)
}
