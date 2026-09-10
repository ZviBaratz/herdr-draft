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
		// rejected is a substring of the value the file supplied that
		// appears in NO default, so the assertion cannot be satisfied by
		// the replacement's own rendering.
		rejected string
	}{
		{"no placeholder", "[clauth]\nlauncher = [\"claude-as\"]\n", "{account}", "claude-as"},
		{"two placeholders", "[clauth]\nlauncher = [\"xyzzy\", \"{account}\", \"{account}\"]\n", "{account}", "xyzzy"},
		{"empty list", "[clauth]\nlauncher = []\n", "empty", ""},
		{"blank first element", "[clauth]\nlauncher = [\"\", \"{account}\"]\n", "empty", ""},
		// A blank element MID-argv is the case the validator's own doc
		// comment justifies specifically, and nothing fed it one: mutating
		// the check to `i == 0 && ...` survived the whole suite.
		{"blank middle element", "[clauth]\nlauncher = [\"claude-as\", \"\", \"{account}\"]\n", "empty", "claude-as"},
		// Whitespace-only is why the check trims. Mutating TrimSpace(a) to
		// a bare `a == ""` also survived.
		{"whitespace-only element", "[clauth]\nlauncher = [\" \", \"{account}\"]\n", "empty", ""},
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
			// The warning must name what it REJECTED, not only what it
			// used instead. Every probe above is satisfied by the rendered
			// default on its own, so deleting the rejected value from the
			// message survived -- while LauncherWarning's doc comment
			// claims it names that value. `config_test.go` already holds
			// BranchPrefixWarning to both halves; this carries the pair
			// across. "claude-as" appears in no default.
			if c.rejected != "" && !strings.Contains(cfg.Clauth.LauncherWarning, c.rejected) {
				t.Errorf("warning %q does not name the rejected value %q",
					cfg.Clauth.LauncherWarning, c.rejected)
			}
			if !strings.Contains(cfg.Clauth.LauncherWarning, "clauth start") {
				t.Errorf("warning %q does not name the replacement it used",
					cfg.Clauth.LauncherWarning)
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

// A wrong TYPE fails Load rather than degrading, unlike a wrong shape. That
// is this loader's behaviour for every key, but `launcher` is the first
// list-typed one and a string is the most plausible way to mistype it -- and
// a comment here once claimed a malformed optional key can never stop the
// form opening. This row holds the corrected claim to the code.
func TestLoad_ClauthLauncherWrongTypeFailsLoad(t *testing.T) {
	for _, body := range []string{
		"[clauth]\nlauncher = \"clauth start {account} --\"\n",
		"[clauth]\nlauncher = [1, 2]\n",
		"[clauth]\nlauncher = true\n",
	} {
		cfg, err := Load(writeConfigBody(t, body))
		if err == nil {
			t.Errorf("Load accepted %q; expected a parse error", body)
			continue
		}
		if cfg.Clauth.LauncherWarning != "" {
			t.Errorf("a type error also set a warning (%q); the two paths are distinct",
				cfg.Clauth.LauncherWarning)
		}
	}
}

// A launcher that is ONLY the placeholder passes validation -- one placeholder,
// no empty element -- so the account name becomes argv[0] and herdr types it
// into the pane as the program. That is almost certainly a mistake, but the
// validator's rule is about the placeholder count and not about argv[0] being a
// plausible program, and widening it at review time would be scope this key
// does not own. Pinned as ACCEPTED so the behaviour is deliberate rather than
// accidental: if a future version starts rejecting it, this row is where the
// decision gets recorded.
func TestLoad_ClauthLauncherBarePlaceholderIsAccepted(t *testing.T) {
	cfg, err := Load(writeConfigBody(t, "[clauth]\nlauncher = [\"{account}\"]\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Clauth.LauncherWarning != "" {
		t.Errorf("rejected a bare-placeholder launcher: %q", cfg.Clauth.LauncherWarning)
	}
	if want := []string{"{account}"}; !reflect.DeepEqual(cfg.Clauth.Launcher, want) {
		t.Errorf("Clauth.Launcher = %v, want %v", cfg.Clauth.Launcher, want)
	}
}
