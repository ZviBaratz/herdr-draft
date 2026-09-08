package config

import (
	"os"
	"path/filepath"
	"testing"
)

// The hazard these cover is not a wrong value, it is a wrong PATH.
// filepath.Join("", "config.toml") is "config.toml" -- relative -- so an
// unset $HERDR_PLUGIN_CONFIG_DIR / $HERDR_PLUGIN_STATE_DIR used to make
// these functions read and WRITE in whatever directory the binary was
// started from. herdr exports those variables only to a launched plugin,
// so "unset" is the normal case for `just smoke`, for the headless verb
// outside a plugin pane, and for anyone running the binary by hand to see
// what it does -- which is to say, in someone's repository.
//
// Each test chdirs into a temp directory and asserts that nothing appeared
// in it. Asserting on the returned value alone would pass just as well
// against the broken version whenever the cwd happened to be clean, which
// is exactly why the bug survived.

// chdirTemp moves the process into a fresh empty directory for the test
// and returns it. t.Chdir restores the previous directory afterwards.
func chdirTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	return dir
}

// assertNothingWritten fails naming every entry that appeared in dir.
func assertNothingWritten(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read back %s: %v", dir, err)
	}
	for _, e := range entries {
		t.Errorf("wrote %q into the working directory; an empty state dir means "+
			"THERE IS NO STATE DIRECTORY, not \"write here\"", e.Name())
	}
}

func TestLoad_EmptyConfigDirDoesNotReadTheWorkingDirectory(t *testing.T) {
	dir := chdirTemp(t)
	// A config.toml that would refuse the plugin outright if it were read
	// as ours: unparseable TOML. This is the shape of the real hazard --
	// some other project's config.toml, parsed as this plugin's.
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("this is not toml = = ="), 0o600); err != nil {
		t.Fatalf("write decoy: %v", err)
	}

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\") = %v, want the built-in defaults and no error", err)
	}
	if cfg.BranchPrefix != defaults().BranchPrefix {
		t.Errorf("BranchPrefix = %q, want the built-in default %q -- Load(\"\") read something",
			cfg.BranchPrefix, defaults().BranchPrefix)
	}
}

func TestLoadState_EmptyStateDirDoesNotReadTheWorkingDirectory(t *testing.T) {
	dir := chdirTemp(t)
	if err := os.WriteFile(filepath.Join(dir, "recents.json"), []byte(`["/decoy"]`), 0o600); err != nil {
		t.Fatalf("write decoy: %v", err)
	}

	st, _ := LoadState("")
	if len(st.Recents) != 0 {
		t.Errorf("Recents = %v, want none -- LoadState(\"\") read the working directory", st.Recents)
	}
}

func TestLoadProjects_EmptyStateDirDoesNotReadTheWorkingDirectory(t *testing.T) {
	dir := chdirTemp(t)
	decoy := `{"version":1,"entries":{"/decoy":{"kind":"codex"}}}`
	if err := os.WriteFile(filepath.Join(dir, "projects.json"), []byte(decoy), 0o600); err != nil {
		t.Fatalf("write decoy: %v", err)
	}

	p, _ := LoadProjects("")
	if len(p.Entries) != 0 {
		t.Errorf("Entries = %v, want none -- LoadProjects(\"\") read the working directory", p.Entries)
	}
}

func TestSaveState_EmptyStateDirWritesNothing(t *testing.T) {
	dir := chdirTemp(t)
	if err := SaveState("", State{Recents: []string{"/projects/thing"}, LastKind: "claude"}); err != nil {
		t.Fatalf("SaveState(\"\") = %v, want nil (skipped, per spec §12's loss-tolerance)", err)
	}
	assertNothingWritten(t, dir)
}

func TestSaveProjects_EmptyStateDirWritesNothing(t *testing.T) {
	dir := chdirTemp(t)
	p := Projects{Version: projectsSchemaVersion, Entries: map[string]ProjectDefaults{
		"/projects/thing": {Kind: "claude"},
	}}
	if err := SaveProjects("", p); err != nil {
		t.Fatalf("SaveProjects(\"\") = %v, want nil (skipped)", err)
	}
	assertNothingWritten(t, dir)
}
