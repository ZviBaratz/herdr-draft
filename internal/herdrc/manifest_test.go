package herdrc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// manifest is the subset of herdr-plugin.toml these tests assert on.
type manifest struct {
	ID        string   `toml:"id"`
	Version   string   `toml:"version"`
	Platforms []string `toml:"platforms"`
}

// readManifest parses the REAL herdr-plugin.toml. Reading the real file is
// the point of every test here: a fixture copy would drift from the
// shipped manifest with exactly the silence these tests exist to break.
func readManifest(t *testing.T) manifest {
	t.Helper()
	path := filepath.Join("..", "..", "herdr-plugin.toml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var m manifest
	if err := toml.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return m
}

// TestPluginIDMatchesManifest makes PluginID's "keep it byte-identical to
// herdr-plugin.toml's id" a check rather than a request.
//
// The drift it guards is silent in the worst way. usablePluginEnv compares
// $HERDR_PLUGIN_ID -- which herdr sets from the MANIFEST -- against this
// constant, and treats a mismatch as "this environment belongs to some
// other plugin". So renaming the plugin in the manifest without renaming
// the constant makes `create`, running inside its own plugin's pane,
// decide the environment is a stranger's: it drops the config dir, the
// state dir and the invocation context, prints a line about a foreign
// plugin naming itself on both sides, and resolves from built-in defaults.
// The session still gets created, just not the one the form would have
// built -- which is precisely the equivalence spec §13 exists to hold.
func TestPluginIDMatchesManifest(t *testing.T) {
	m := readManifest(t)
	if m.ID == "" {
		t.Fatal("herdr-plugin.toml declares no id")
	}
	if m.ID != PluginID {
		t.Errorf("herdr-plugin.toml id = %q, PluginID = %q; they must match exactly -- "+
			"usablePluginEnv compares $HERDR_PLUGIN_ID against PluginID, so a drift makes "+
			"`create` mistake its own plugin environment for another plugin's", m.ID, PluginID)
	}
}

// TestPluginIDIsVendorQualified pins the decision behind the name rather
// than just the name. herdr plugin ids share one global namespace -- the id
// is the install directory, the config dir, the state dir and the
// `--plugin` argument -- so a bare generic word is a claim on a name any
// other author could reasonably want. Renaming is cheapest before the first
// public install and breaks strangers after it, which is why this is worth
// a guard rather than a convention.
func TestPluginIDIsVendorQualified(t *testing.T) {
	before, after, found := strings.Cut(PluginID, ".")
	if !found || before == "" || after == "" {
		t.Errorf("PluginID = %q, want a vendor-qualified <vendor>.<name> id "+
			"(herdr's own documented example is example.layout)", PluginID)
	}
}

// TestManifestDeclaresOnlySupportedPlatforms guards a key whose ABSENCE is
// an active claim. herdr's build_platform_supported reads a missing
// platforms list with is_none_or, so it treats "unspecified" as "every
// platform" -- meaning a manifest that says nothing has claimed Windows,
// where the `sh -c` action entrypoint and the extensionless build output
// cannot run. Deleting this key would therefore not relax a constraint; it
// would re-assert support that was never built, and the only symptom would
// be a stranger's install registering and then failing to launch.
//
// "windows" is rejected by name rather than by an allow-list alone, so the
// failure message can say what would actually have to change first.
func TestManifestDeclaresOnlySupportedPlatforms(t *testing.T) {
	m := readManifest(t)

	if len(m.Platforms) == 0 {
		t.Fatal("herdr-plugin.toml declares no platforms; herdr reads that as ALL platforms, " +
			"which claims Windows support this plugin does not have")
	}

	known := map[string]bool{"linux": true, "macos": true, "windows": true}
	for _, p := range m.Platforms {
		if !known[p] {
			t.Errorf("platform %q is not one herdr recognizes (linux, macos, windows)", p)
		}
		if p == "windows" {
			t.Error("platforms declares windows: the [[actions]] entrypoint is `sh -c` and " +
				"[[build]] writes an extensionless binary, so a Windows install would register " +
				"and then fail to launch. Make those two real before adding it back")
		}
	}
}

// TestVersionMatchesManifest holds the Version constant to the manifest.
//
// herdr shows the manifest's version for an installed plugin, so a drift
// makes `herdr-draft version` and `herdr plugin list` disagree about what
// is running -- which is worse than having no version at all, since the
// whole reason to add one was so a bug report could state it.
//
// The git tag is the third member of this set and the one no test can
// reach: a tag lives in the repository, not in the tree being tested, and
// asserting on `git describe` would fail in every shallow CI checkout and
// every `go install` from a module cache. Keeping the tag in step is the
// release process's job; keeping these two in step is this test's.
func TestVersionMatchesManifest(t *testing.T) {
	m := readManifest(t)
	if m.Version == "" {
		t.Fatal("herdr-plugin.toml declares no version")
	}
	if m.Version != Version {
		t.Errorf("herdr-plugin.toml version = %q, herdrc.Version = %q; bump them together -- "+
			"herdr shows the manifest's value for an installed plugin, so a drift makes the "+
			"binary and herdr disagree about what is running", m.Version, Version)
	}
}
