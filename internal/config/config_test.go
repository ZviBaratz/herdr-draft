package config

import (
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// fullConfigTOML is the spec §12 example config, reproduced verbatim
// (including its commented-out `api_key` and `[palette]` keys) so this test
// proves the package parses the exact documented fixture, not a paraphrase
// of it.
const fullConfigTOML = `branch_prefix = "zvi/"          # default: lowercased $USER + "/"
default_worktree = true          # for git targets
default_placement = "new-space"  # when worktree is off

[linear]
api_key_cmd = ["pass", "show", "linear-api-key"]
# api_key = "lin_api_..."       # discouraged; file perms checked (0600)
prompt_template = ""             # empty = built-in default (§10)

[clauth]
enabled = true                   # auto-detected if omitted
default = "active"               # or a profile name

[agents]
favorites = ["claude", "codex"]
default = "claude"

[agents.extra_args]
claude = []
codex = []

[timeouts]
detection_ms = 30000
prompt_wait_ms = 120000

[palette]  # optional escape hatch when herdr theme detection is wrong (§7)
# accent = "#89b4fa"
# panel_bg = "#1e1e2e"
`

func boolPtr(b bool) *bool { return &b }

func defaultBranchPrefixForTest(t *testing.T) string {
	t.Helper()
	u, err := user.Current()
	if err != nil {
		t.Fatalf("user.Current: %v", err)
	}
	return strings.ToLower(u.Username) + "/"
}

func TestLoad_MissingFile_Defaults(t *testing.T) {
	dir := t.TempDir()

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	wantPrefix := defaultBranchPrefixForTest(t)
	if cfg.BranchPrefix != wantPrefix {
		t.Errorf("BranchPrefix = %q, want %q", cfg.BranchPrefix, wantPrefix)
	}
	if !cfg.DefaultWorktree {
		t.Errorf("DefaultWorktree = false, want true")
	}
	if cfg.DefaultPlacement != "new-space" {
		t.Errorf("DefaultPlacement = %q, want %q", cfg.DefaultPlacement, "new-space")
	}
	if !reflect.DeepEqual(cfg.Agents.Favorites, []string{"claude"}) {
		t.Errorf("Agents.Favorites = %v, want [claude]", cfg.Agents.Favorites)
	}
	if cfg.Timeouts.DetectionMS != 30000 {
		t.Errorf("Timeouts.DetectionMS = %d, want 30000", cfg.Timeouts.DetectionMS)
	}
	if cfg.Timeouts.PromptWaitMS != 120000 {
		t.Errorf("Timeouts.PromptWaitMS = %d, want 120000", cfg.Timeouts.PromptWaitMS)
	}
}

func TestLoad_FullConfig_ParsesEveryField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(fullConfigTOML), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.BranchPrefix != "zvi/" {
		t.Errorf("BranchPrefix = %q, want %q", cfg.BranchPrefix, "zvi/")
	}
	if !cfg.DefaultWorktree {
		t.Errorf("DefaultWorktree = false, want true")
	}
	if cfg.DefaultPlacement != "new-space" {
		t.Errorf("DefaultPlacement = %q, want %q", cfg.DefaultPlacement, "new-space")
	}

	if !reflect.DeepEqual(cfg.Linear.APIKeyCmd, []string{"pass", "show", "linear-api-key"}) {
		t.Errorf("Linear.APIKeyCmd = %v, want [pass show linear-api-key]", cfg.Linear.APIKeyCmd)
	}
	if cfg.Linear.APIKey != "" {
		t.Errorf("Linear.APIKey = %q, want empty (commented out in fixture)", cfg.Linear.APIKey)
	}
	if cfg.Linear.PromptTemplate != "" {
		t.Errorf("Linear.PromptTemplate = %q, want empty", cfg.Linear.PromptTemplate)
	}

	if cfg.Clauth.Enabled == nil || !*cfg.Clauth.Enabled {
		t.Errorf("Clauth.Enabled = %v, want *true", cfg.Clauth.Enabled)
	}
	if cfg.Clauth.Default != "active" {
		t.Errorf("Clauth.Default = %q, want %q", cfg.Clauth.Default, "active")
	}

	if !reflect.DeepEqual(cfg.Agents.Favorites, []string{"claude", "codex"}) {
		t.Errorf("Agents.Favorites = %v, want [claude codex]", cfg.Agents.Favorites)
	}
	if cfg.Agents.Default != "claude" {
		t.Errorf("Agents.Default = %q, want %q", cfg.Agents.Default, "claude")
	}
	wantExtraArgs := map[string][]string{"claude": {}, "codex": {}}
	if !reflect.DeepEqual(cfg.Agents.ExtraArgs, wantExtraArgs) {
		t.Errorf("Agents.ExtraArgs = %v, want %v", cfg.Agents.ExtraArgs, wantExtraArgs)
	}

	if cfg.Timeouts.DetectionMS != 30000 {
		t.Errorf("Timeouts.DetectionMS = %d, want 30000", cfg.Timeouts.DetectionMS)
	}
	if cfg.Timeouts.PromptWaitMS != 120000 {
		t.Errorf("Timeouts.PromptWaitMS = %d, want 120000", cfg.Timeouts.PromptWaitMS)
	}

	if cfg.Palette == nil {
		t.Errorf("Palette = nil, want non-nil empty map (fixture declares [palette] with all keys commented out)")
	}
	if len(cfg.Palette) != 0 {
		t.Errorf("Palette = %v, want empty", cfg.Palette)
	}
}

func TestLoad_UnknownKeysIgnored(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	toml := `branch_prefix = "zvi/"
totally_unknown_key = "should be ignored"

[linear]
prompt_template = "hi"
made_up_field = 42

[bogus_section]
whatever = true
`
	if err := os.WriteFile(path, []byte(toml), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.BranchPrefix != "zvi/" {
		t.Errorf("BranchPrefix = %q, want %q", cfg.BranchPrefix, "zvi/")
	}
	if cfg.Linear.PromptTemplate != "hi" {
		t.Errorf("Linear.PromptTemplate = %q, want %q", cfg.Linear.PromptTemplate, "hi")
	}
	// DefaultPlacement wasn't set in this fixture, so it should still carry
	// its default rather than being clobbered by the unknown keys.
	if cfg.DefaultPlacement != "new-space" {
		t.Errorf("DefaultPlacement = %q, want default %q preserved", cfg.DefaultPlacement, "new-space")
	}
}

func TestLoadState_MissingFile_ZeroValue(t *testing.T) {
	dir := t.TempDir()

	st, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(st.Recents) != 0 {
		t.Errorf("Recents = %v, want empty", st.Recents)
	}
	if st.LastKind != "" || st.LastPlacement != "" {
		t.Errorf("LastKind/LastPlacement = %q/%q, want empty/empty", st.LastKind, st.LastPlacement)
	}
	if st.LastWorktree != nil {
		t.Errorf("LastWorktree = %v, want nil", st.LastWorktree)
	}
}

func TestLoadState_CorruptFile_ZeroValue(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "recents.json"), []byte("{not valid json"), 0o600); err != nil {
		t.Fatalf("write corrupt recents.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "last-used.json"), []byte("also not json{{{"), 0o600); err != nil {
		t.Fatalf("write corrupt last-used.json: %v", err)
	}

	st, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v, want no error even on corrupt state", err)
	}
	if len(st.Recents) != 0 {
		t.Errorf("Recents = %v, want empty", st.Recents)
	}
	if st.LastKind != "" || st.LastPlacement != "" || st.LastWorktree != nil {
		t.Errorf("last-used fields not zero-valued: kind=%q placement=%q worktree=%v",
			st.LastKind, st.LastPlacement, st.LastWorktree)
	}
}

func TestState_SaveLoad_RoundTrip(t *testing.T) {
	dir := t.TempDir()

	want := State{
		Recents:       []string{"/a", "/b", "/c"},
		LastKind:      "claude",
		LastPlacement: "new-space",
		LastWorktree:  boolPtr(true),
	}
	if err := SaveState(dir, want); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	got, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if !reflect.DeepEqual(got.Recents, want.Recents) {
		t.Errorf("Recents = %v, want %v", got.Recents, want.Recents)
	}
	if got.LastKind != want.LastKind {
		t.Errorf("LastKind = %q, want %q", got.LastKind, want.LastKind)
	}
	if got.LastPlacement != want.LastPlacement {
		t.Errorf("LastPlacement = %q, want %q", got.LastPlacement, want.LastPlacement)
	}
	if got.LastWorktree == nil || *got.LastWorktree != *want.LastWorktree {
		t.Errorf("LastWorktree = %v, want *%v", got.LastWorktree, *want.LastWorktree)
	}
}

func TestState_TouchRecent_MostRecentFirstDedupeCappedAt20(t *testing.T) {
	var st State

	// Fill with 20 distinct paths, oldest first.
	for i := 0; i < 20; i++ {
		st.TouchRecent("/path/" + strconv.Itoa(i))
	}
	if len(st.Recents) != 20 {
		t.Fatalf("len(Recents) = %d, want 20", len(st.Recents))
	}
	if st.Recents[0] != "/path/19" {
		t.Errorf("Recents[0] = %q, want most-recently-touched /path/19", st.Recents[0])
	}

	// Touching a path already present should move it to the front without
	// growing the list (dedupe), and touching a new 21st path should evict
	// the oldest entry (cap at 20).
	st.TouchRecent("/path/5")
	if len(st.Recents) != 20 {
		t.Fatalf("len(Recents) after re-touch = %d, want still 20 (dedupe)", len(st.Recents))
	}
	if st.Recents[0] != "/path/5" {
		t.Errorf("Recents[0] after re-touch = %q, want /path/5", st.Recents[0])
	}
	count5 := 0
	for _, p := range st.Recents {
		if p == "/path/5" {
			count5++
		}
	}
	if count5 != 1 {
		t.Errorf("/path/5 appears %d times, want 1 (deduped)", count5)
	}

	st.TouchRecent("/path/new")
	if len(st.Recents) != 20 {
		t.Fatalf("len(Recents) after cap-exceeding touch = %d, want still 20", len(st.Recents))
	}
	if st.Recents[0] != "/path/new" {
		t.Errorf("Recents[0] = %q, want /path/new", st.Recents[0])
	}
	for _, p := range st.Recents {
		if p == "/path/0" {
			t.Errorf("oldest entry /path/0 should have been evicted, but is still present: %v", st.Recents)
		}
	}
}
