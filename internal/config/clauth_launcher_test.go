package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeConfigBody writes a whole config.toml body, for fixtures that need
// more than writeConfig's single branch_prefix key.
func writeConfigBody(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	return dir
}

// The default has to stay byte-identical to what the plugin built before
// `launcher` existed, because every other installation keeps it: a change
// here is a behaviour change for people who never opened the config file.
func TestLoad_ClauthLauncherDefault(t *testing.T) {
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"clauth", "start", "{account}", "--"}
	if got := cfg.Clauth.Launcher; !reflect.DeepEqual(got, want) {
		t.Fatalf("Clauth.Launcher = %v, want %v", got, want)
	}
	if cfg.Clauth.LauncherWarning != "" {
		t.Errorf("the built-in default warned about itself: %q", cfg.Clauth.LauncherWarning)
	}
}

// A template is not a prefix swap: `clauth start` needs the trailing `--`
// and `claude-as` does not, so the whole argv is the unit and no element is
// appended on the caller's behalf.
func TestLoad_ClauthLauncherFromFile(t *testing.T) {
	dir := writeConfigBody(t, "[clauth]\nlauncher = [\"claude-as\", \"{account}\"]\n")
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"claude-as", "{account}"}
	if got := cfg.Clauth.Launcher; !reflect.DeepEqual(got, want) {
		t.Fatalf("Clauth.Launcher = %v, want %v", got, want)
	}
	if cfg.Clauth.LauncherWarning != "" {
		t.Errorf("a valid launcher warned: %q", cfg.Clauth.LauncherWarning)
	}
}

// Every rejected shape degrades to the default WITH a reason, the same way
// an unusable branch_prefix does (Load's own doc comment). A launcher is
// exactly the wrong key to fail the plugin over -- and exactly the wrong
// one to drop silently, because a template carrying no {account} would
// launch the session on whatever account happened to be active, which is
// the mis-billing this key exists to fix.
func TestLoad_ClauthLauncherRejectedShapesFallBackWithAReason(t *testing.T) {
	def := []string{"clauth", "start", "{account}", "--"}
	cases := []struct {
		name, body, mentions string
	}{
		{"no placeholder", "[clauth]\nlauncher = [\"claude-as\"]\n", "{account}"},
		{"two placeholders", "[clauth]\nlauncher = [\"x\", \"{account}\", \"{account}\"]\n", "{account}"},
		{"empty list", "[clauth]\nlauncher = []\n", "empty"},
		{"blank first element", "[clauth]\nlauncher = [\"\", \"{account}\"]\n", "empty"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := Load(writeConfigBody(t, c.body))
			if err != nil {
				t.Fatalf("Load: %v -- a bad launcher must never fail Load", err)
			}
			if got := cfg.Clauth.Launcher; !reflect.DeepEqual(got, def) {
				t.Fatalf("Clauth.Launcher = %v, want the default %v", got, def)
			}
			if cfg.Clauth.LauncherWarning == "" {
				t.Fatal("fell back to the default with no reason; a silent fallback is the failure mode")
			}
			if !strings.Contains(cfg.Clauth.LauncherWarning, c.mentions) {
				t.Errorf("warning %q does not mention %q", cfg.Clauth.LauncherWarning, c.mentions)
			}
		})
	}
}

// The placeholder may sit anywhere in the argv and may share its element
// with other text, so the check counts occurrences rather than looking for
// a bare element equal to "{account}".
func TestLoad_ClauthLauncherPlaceholderNeedNotBeItsOwnElement(t *testing.T) {
	dir := writeConfigBody(t, "[clauth]\nlauncher = [\"sh\", \"-c\", \"exec claude-as {account}\"]\n")
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Clauth.LauncherWarning != "" {
		t.Fatalf("rejected a launcher whose placeholder shares an element: %q", cfg.Clauth.LauncherWarning)
	}
	want := []string{"sh", "-c", "exec claude-as {account}"}
	if got := cfg.Clauth.Launcher; !reflect.DeepEqual(got, want) {
		t.Errorf("Clauth.Launcher = %v, want %v", got, want)
	}
}
