package linear

import (
	"os"
	"testing"
)

// TestSaveCache_EmptyStateDirWritesNothing covers the one write path in
// this repository that was NOT guarded, and whose lack of a guard was
// invisible because its only caller discards the error:
//
//	_ = linear.SaveCache(m.stateDir, msg.issues) // best-effort
//
// filepath.Join("", "linear-cache.json") is relative, so an unset
// $HERDR_PLUGIN_STATE_DIR dropped the file into the process's working
// directory. herdr exports that variable only to a launched plugin, so the
// people who hit this are the ones running the binary by hand or through
// `just smoke` -- standing in a repository at the time.
//
// The assertion is on the DIRECTORY, not the return value: a version that
// wrote the file would still return nil, which is what made this silent.
func TestSaveCache_EmptyStateDirWritesNothing(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	if err := SaveCache("", []Issue{{Identifier: "ENG-1", Title: "decoy"}}); err != nil {
		t.Fatalf("SaveCache(\"\") = %v, want nil (skipped, like SaveState and SaveProjects)", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read back %s: %v", dir, err)
	}
	for _, e := range entries {
		t.Errorf("wrote %q into the working directory -- that is someone's repository", e.Name())
	}
}

// TestLoadCache_EmptyStateDirReadsNothing is the read half. It must not
// stat a relative linear-cache.json, and it reports "nothing to load" as
// an error, like every other missing-cache outcome here, so the caller's
// existing discard-and-carry-on path needs no change.
func TestLoadCache_EmptyStateDirReadsNothing(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	decoy := `[{"Identifier":"ENG-9","Title":"from the working directory"}]`
	if err := os.WriteFile("linear-cache.json", []byte(decoy), 0o600); err != nil {
		t.Fatalf("write decoy: %v", err)
	}

	issues, _, err := LoadCache("")
	if err == nil {
		t.Fatalf("LoadCache(\"\") = %d issues, nil error; want an error -- it read the working directory", len(issues))
	}
	if len(issues) != 0 {
		t.Errorf("issues = %v, want none", issues)
	}
}
