package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ZviBaratz/herdr-draft/internal/config"
)

// loadConfigBody runs config.Load itself over a config.toml holding body, so
// the tests below pin what the loader actually writes reaching the screen
// rather than a hand-typed stand-in for it. The file lives in a directory this
// test created, which is the only real I/O this package's tests allow.
func loadConfigBody(t *testing.T, body string) config.Config {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

// configWarningsModel is the assembled form over that loaded config.
func configWarningsModel(t *testing.T, body string) Model {
	t.Helper()
	setup := frameSetup(true)
	setup.Config = loadConfigBody(t, body)
	return resolveDirCheck(t, newTestModel(t, setup))
}

// cleared is the form after ⌃R⌃R, which rebuilds it through New from the
// Model's own config -- so a warning pushed anywhere but New would vanish on
// the first clear. The dir check is answered again because the rebuilt
// worktree row starts inert, like every freshly opened one.
func cleared(t *testing.T, m Model) Model {
	t.Helper()
	fresh, _ := m.handleClearRequested()
	return resolveDirCheck(t, fresh)
}

// TestConfigWarnings_ClauthOnesReachTheAccountPanel is #123's defect: the popup
// read none of config.Load's [clauth] warnings, and a TUI in a herdr pane has
// no stderr anyone sees, so an ignored or malformed launcher was reported to
// nobody.
//
// `default` names a profile on purpose. A launcher is only ever used for a
// PINNED account, so that is the state these warnings matter in -- and the
// state a verdict keyed on the unpinned "" (the shape Setup.PickerUnavailable
// is routed with) would have filtered out as stale.
func TestConfigWarnings_ClauthOnesReachTheAccountPanel(t *testing.T) {
	m := configWarningsModel(t, "[clauth]\ndefault = \"work\"\nlaunch = \"wrapper\"\nlauncher = [\"claude-as\"]\n")
	if got := m.account.Pin(); got != "work" {
		t.Fatalf("Pin() = %q, want the configured default %q", got, "work")
	}

	for name, model := range map[string]Model{"opened": m, "cleared": cleared(t, m)} {
		frame := focusedFrame(t, model, "account")
		// Both facts, ignored first. Only the heads are asserted: a note wider
		// than the panel elides at its tail, visibly.
		for _, want := range []string{
			`ignoring [clauth] launcher [claude-as]: [clauth] launch = "wrapper"`,
			`ignoring [clauth] launcher [claude-as]: it contains {account} 0 times`,
		} {
			if !strings.Contains(frame, want) {
				t.Errorf("%s: the account panel does not carry %q:\n%s", name, want, frame)
			}
		}
	}
}

// TestConfigWarnings_BranchPrefixReachesTheWorktreePanel: the other warning
// config.Load writes, on the panel of the row whose branch the refused prefix
// would have shaped.
func TestConfigWarnings_BranchPrefixReachesTheWorktreePanel(t *testing.T) {
	const want = `ignoring branch_prefix "-x": starts with "-"`
	m := configWarningsModel(t, "branch_prefix = \"-x\"\n")

	for name, model := range map[string]Model{"opened": m, "cleared": cleared(t, m)} {
		if frame := focusedFrame(t, model, "worktree"); !strings.Contains(frame, want) {
			t.Errorf("%s: the worktree panel does not carry %q:\n%s", name, want, frame)
		}
	}
}
