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

// The heads of the two launcher warnings as the account panel shows them:
// the reason, which is what tells the two apart, before anything that can
// run long.
const (
	launcherIgnoredHead   = `ignoring [clauth] launcher: [clauth] launch = "wrapper"`
	launcherMalformedHead = `ignoring [clauth] launcher: it contains {account} 0 times`
)

// TestConfigWarnings_ClauthOnesReachTheAccountPanel is #123's defect: the popup
// read none of config.Load's [clauth] warnings, and a TUI in a herdr pane has
// no stderr anyone sees, so an ignored or malformed launcher was reported to
// nobody.
//
// `default` names a profile on purpose. A launcher is only ever used for a
// PINNED account, so that is the state these warnings matter in -- and the
// state a verdict keyed on the unpinned "" would have filtered out as stale,
// which is how Setup.PickerUnavailable's reason used to vanish.
func TestConfigWarnings_ClauthOnesReachTheAccountPanel(t *testing.T) {
	m := configWarningsModel(t, "[clauth]\ndefault = \"work\"\nlaunch = \"wrapper\"\nlauncher = [\"claude-as\"]\n")
	if got := m.account.Pin(); got != "work" {
		t.Fatalf("Pin() = %q, want the configured default %q", got, "work")
	}

	for name, model := range map[string]Model{"opened": m, "cleared": cleared(t, m)} {
		frame := focusedFrame(t, model, "account")
		// Both facts, ignored first. Only the heads are asserted: a note wider
		// than the panel elides at its tail, visibly.
		ignored, malformed := strings.Index(frame, launcherIgnoredHead), strings.Index(frame, launcherMalformedHead)
		switch {
		case ignored < 0 || malformed < 0:
			t.Errorf("%s: the account panel does not carry both %q and %q:\n%s",
				name, launcherIgnoredHead, launcherMalformedHead, frame)
		case ignored > malformed:
			t.Errorf("%s: the malformed-launcher note is above the ignored-launcher one:\n%s", name, frame)
		}
	}
}

// TestConfigWarnings_ALongLauncherStillShowsBothReasons comes from the review
// of #125. Each note is one line cut to the panel's width, and a launcher is an
// argv that can run to real paths. While the rejected value led each warning, a
// long one pushed both reasons past the edge and the panel showed the same
// truncated line twice -- two warnings, neither saying why.
func TestConfigWarnings_ALongLauncherStillShowsBothReasons(t *testing.T) {
	m := configWarningsModel(t, "[clauth]\ndefault = \"work\"\nlaunch = \"wrapper\"\n"+
		"launcher = [\"/home/someone/.local/bin/claude-as\", \"--config-dir\", \"/home/someone/.config/claude-profiles\"]\n")

	frame := focusedFrame(t, m, "account")
	for _, want := range []string{launcherIgnoredHead, launcherMalformedHead} {
		if !strings.Contains(frame, want) {
			t.Errorf("the account panel lost %q to a long launcher:\n%s", want, frame)
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

// TestConfigWarnings_BranchPrefixNoteFollowsThePrefixInEffect comes from the
// review of #125. The warning ends `using "<fallback>"`, but a repository's
// .herdr-draft.toml outranks config.toml for branch_prefix, so in such a
// repository the branch part directly above the note named a different prefix
// from the one the note said was in use. There, the refused key would not have
// applied even if it were valid, so the note goes -- and it comes back in the
// next project that has no prefix of its own.
func TestConfigWarnings_BranchPrefixNoteFollowsThePrefixInEffect(t *testing.T) {
	const head = `ignoring branch_prefix "a b/"`
	m, _ := repoConfigModel(t, "/repo-a", testSetup{Config: loadConfigBody(t, "branch_prefix = \"a b/\"\n")},
		map[string]config.RepoConfig{
			"/repo-a": {BranchPrefix: "team/"},
			"/repo-b": {},
		})

	if frame := focusedFrame(t, m, "worktree"); strings.Contains(frame, head) {
		t.Errorf("a repository with its own branch_prefix still shows config.toml's refused one:\n%s", frame)
	}
	m = switchProject(t, m, "/repo-a", "/repo-b")
	if frame := focusedFrame(t, m, "worktree"); !strings.Contains(frame, head) {
		t.Errorf("a repository with no branch_prefix of its own lost the note:\n%s", frame)
	}
}
