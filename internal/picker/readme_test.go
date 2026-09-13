package picker

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// readmeExamplePicker extracts the shell script the README publishes under
// "Writing one" and makes it executable in a temp dir.
//
// Extracting the LIVE document rather than keeping a copy here is the whole
// point of the test below: a copy is one more thing to keep in sync, and the
// defect this exists to catch is precisely the two drifting apart.
func readmeExamplePicker(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the README's example picker is a POSIX shell script")
	}

	const (
		heading    = "### Writing one"
		fenceOpen  = "```sh\n"
		fenceClose = "```"
	)
	readme := filepath.Join("..", "..", "README.md")
	src, err := os.ReadFile(readme)
	if err != nil {
		t.Fatalf("read README: %v", err)
	}

	rest := string(src)
	i := strings.Index(rest, heading)
	if i < 0 {
		t.Fatalf("README has no %q section -- if it moved, move this test with it", heading)
	}
	rest = rest[i:]
	j := strings.Index(rest, fenceOpen)
	if j < 0 {
		t.Fatalf("README's %q section has no ```sh block", heading)
	}
	rest = rest[j+len(fenceOpen):]
	k := strings.Index(rest, fenceClose)
	if k < 0 {
		t.Fatal("README's example picker block is unterminated")
	}
	script := rest[:k]
	if !strings.HasPrefix(script, "#!/bin/sh") {
		t.Fatalf("README's example picker does not start with a shebang:\n%s", script)
	}

	bin := filepath.Join(t.TempDir(), "readme-example-picker")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write example picker: %v", err)
	}
	return bin
}

// The README IS the contract: it is the only thing a stranger implementing a
// picker has to work from, so an example that does not satisfy the protocol is
// not a documentation nit but a wrong contract shipped to everyone who copies
// it.
//
// The version this replaces said it "ignores every flag" and emitted a real
// config_dir unconditionally, contradicting the --dry-run clause two sections
// above it. It passed the startup probe, so nothing complained -- and then
// every preview, one per project change, claimed to have built an account
// directory that was never built.
func TestTheREADMEsExamplePickerIsConformant(t *testing.T) {
	c := CLI{Bin: readmeExamplePicker(t)}
	ctx := context.Background()

	if err := c.Probe(ctx, "/p/thing"); err != nil {
		t.Fatalf("the README's example picker does not pass the startup probe: %v", err)
	}

	dry, err := c.Pick(ctx, "/p/thing", Options{DryRun: true})
	if err != nil {
		t.Fatalf("Pick(--dry-run): %v", err)
	}
	if dry.Profile != "my-account" {
		t.Errorf("Pick(--dry-run).Profile = %q, want the example's own account", dry.Profile)
	}
	if dry.ConfigDir != "" {
		t.Errorf("--dry-run must not build an account dir, got config_dir=%q", dry.ConfigDir)
	}

	// And the commit-time call, which is the one that must answer a real
	// config_dir -- an example honouring --dry-run by never emitting one at
	// all would be just as wrong, since `[clauth] launch = "wrapper"` has
	// nothing to isolate with.
	live, err := c.Pick(ctx, "/p/thing", Options{})
	if err != nil {
		t.Fatalf("Pick(): %v", err)
	}
	if live.ConfigDir == "" {
		t.Error("a non-dry-run pick must answer a config_dir")
	}
}
