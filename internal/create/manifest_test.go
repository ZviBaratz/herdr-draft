package create

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// TestPluginIDMatchesManifest makes pluginID's "keep it byte-identical to
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
//
// Reading the real manifest is the point. A fixture copy would drift with
// exactly the same silence.
func TestPluginIDMatchesManifest(t *testing.T) {
	path := filepath.Join("..", "..", "herdr-plugin.toml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	var manifest struct {
		ID string `toml:"id"`
	}
	if err := toml.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	if manifest.ID == "" {
		t.Fatalf("%s declares no id", path)
	}
	if manifest.ID != pluginID {
		t.Errorf("herdr-plugin.toml id = %q, pluginID = %q; they must match exactly -- "+
			"usablePluginEnv compares $HERDR_PLUGIN_ID against pluginID, so a drift makes "+
			"`create` mistake its own plugin environment for another plugin's", manifest.ID, pluginID)
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
	before, after, found := strings.Cut(pluginID, ".")
	if !found || before == "" || after == "" {
		t.Errorf("pluginID = %q, want a vendor-qualified <vendor>.<name> id "+
			"(herdr's own documented example is example.layout)", pluginID)
	}
}
