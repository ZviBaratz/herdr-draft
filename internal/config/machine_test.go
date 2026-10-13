package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestLoad_NoMachineTableIsUnconfigured: the popup's machine row exists only
// when the table does (machine spec §9.1), so its absence must be readable
// as such -- and nothing else about the config may change for it.
func TestLoad_NoMachineTableIsUnconfigured(t *testing.T) {
	cfg, err := Load(writeConfigBody(t, "branch_prefix = \"zvi/\"\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Machine.Configured || cfg.Machine.Default != "" || cfg.Machine.Rules != nil || cfg.Machine.Warnings != nil {
		t.Errorf("Machine = %+v, want the zero value", cfg.Machine)
	}
}

// TestLoad_AnEmptyMachineTableIsConfigured: `[machine]` with nothing in it
// still opts in -- the row appears, offering only local.
func TestLoad_AnEmptyMachineTableIsConfigured(t *testing.T) {
	cfg, err := Load(writeConfigBody(t, "[machine]\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Machine.Configured {
		t.Error("an empty [machine] table did not count as configured")
	}
}

// TestLoad_MachineRulesAreExpandedCanonicalAndOrdered: rule keys are the
// user's spelling (`~/quantivly`) and the project key they are matched
// against is canonical (pathx.CanonicalKey), so Load canonicalises the
// keys once; the order is by directory, so nothing depends on a map's.
func TestLoad_MachineRulesAreExpandedCanonicalAndOrdered(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, d := range []string{"quantivly/browser-repo", "work"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(home, "work"), filepath.Join(home, "quantivly-link")); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(writeConfigBody(t, `[machine]
default = "local"
[machine.paths]
"~/quantivly" = "dev"
"~/quantivly/browser-repo" = "local"
"~/quantivly-link" = "dev (EC2)"
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []MachineRule{
		{Dir: filepath.Join(home, "quantivly"), Selector: "dev"},
		{Dir: filepath.Join(home, "quantivly", "browser-repo"), Selector: "local"},
		{Dir: filepath.Join(home, "work"), Selector: "dev (EC2)"},
	}
	if !reflect.DeepEqual(cfg.Machine.Rules, want) {
		t.Errorf("Rules = %+v\nwant  %+v", cfg.Machine.Rules, want)
	}
	if cfg.Machine.Default != "local" || !cfg.Machine.Configured || cfg.Machine.Warnings != nil {
		t.Errorf("Machine = %+v", cfg.Machine)
	}
}

// TestLoad_MachineRulesItCannotUseAreReportedNotDropped: a relative key,
// an empty selector, and two keys naming one directory each lose their
// entry -- with a warning, never silently (machine spec §5.1).
func TestLoad_MachineRulesItCannotUseAreReportedNotDropped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg, err := Load(writeConfigBody(t, `[machine.paths]
"quantivly" = "dev"
"~/empty" = ""
"~/q" = "dev"
"~/q/" = "local"
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []MachineRule{{Dir: filepath.Join(home, "q"), Selector: "dev"}}
	if !reflect.DeepEqual(cfg.Machine.Rules, want) {
		t.Errorf("Rules = %+v, want %+v", cfg.Machine.Rules, want)
	}
	joined := strings.Join(cfg.Machine.Warnings, "\n")
	for _, frag := range []string{`"quantivly"`, "not an absolute path", `"~/empty"`, "names no machine", `"~/q/"`, "same directory as"} {
		if !strings.Contains(joined, frag) {
			t.Errorf("warnings do not mention %q:\n%s", frag, joined)
		}
	}
}

// TestMachineOfferedIsLocalAndWhatTheConfigNames: the offered set (machine
// spec §4.2) is local first, then every machine the table names, once each,
// in the order a reader meets them -- default, then the rules. A saved
// machine the table never names is not in it: that is what keeps a
// production host one cycle away from nothing.
func TestMachineOfferedIsLocalAndWhatTheConfigNames(t *testing.T) {
	m := MachineConfig{
		Configured: true,
		Default:    "dev",
		Rules: []MachineRule{
			{Dir: "/h/a", Selector: "local"},
			{Dir: "/h/b", Selector: "dev"},
			{Dir: "/h/c", Selector: "build box"},
		},
	}
	if got, want := m.Offered(), []string{"local", "dev", "build box"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Offered() = %q, want %q", got, want)
	}
	if got := (MachineConfig{}).Offered(); !reflect.DeepEqual(got, []string{"local"}) {
		t.Errorf("an unconfigured table offers %q, want [local]", got)
	}
}

// TestRepoConfigRefusesAMachineTable: a repository arriving with git clone
// must not choose which host runs your code (machine spec §5.3; v2 spec
// §11's trust model), and the note says so.
func TestRepoConfigRefusesAMachineTable(t *testing.T) {
	dir := t.TempDir()
	body := "[machine]\ndefault = \"dev\"\n[machine.paths]\n\"/x\" = \"dev\"\n"
	if err := os.WriteFile(filepath.Join(dir, RepoConfigFileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	rc := LoadRepoConfig(dir)
	notes := strings.Join(rc.Notes, "\n")
	if !strings.Contains(notes, "machine") || !strings.Contains(notes, "which host runs your code") {
		t.Errorf("the repo config's notes do not refuse [machine] with its reason:\n%s", notes)
	}
}
