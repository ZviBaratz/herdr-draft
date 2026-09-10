package plan

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ZviBaratz/herdr-draft/internal/config"
)

// clauthArgv builds the plan for a pinned claude account and returns the
// argv of its OpClauthLaunch.
func clauthArgv(t *testing.T, launcher []string, pin string, extra []string) []string {
	t.Helper()
	in := validInput()
	in.AccountPin = pin
	in.ExtraArgs = extra
	in.Launcher = launcher
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, op := range ops {
		if op.Kind == OpClauthLaunch {
			return op.RunArgv
		}
	}
	t.Fatalf("no OpClauthLaunch in %v", kindsOf(ops))
	return nil
}

// The whole point of defaulting rather than requiring: a caller that never
// heard of Launcher -- every existing one -- keeps today's argv exactly.
// Without this row the change is a silent behaviour change for everyone.
func TestLaunchOps_ZeroLauncherIsTodaysArgv(t *testing.T) {
	got := clauthArgv(t, nil, "work", []string{"--model", "opus"})
	want := []string{"clauth", "start", "work", "--", "--model", "opus"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RunArgv = %v, want %v", got, want)
	}
}

// A template, not a prefix swap. `claude-as` takes no `--`, so nothing may
// be appended for it: the argv is exactly the template plus ExtraArgs.
func TestLaunchOps_ClaudeAsLauncherAppendsNoSeparator(t *testing.T) {
	got := clauthArgv(t, []string{"claude-as", "{account}"}, "work", []string{"--model", "opus"})
	want := []string{"claude-as", "work", "--model", "opus"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RunArgv = %v, want %v", got, want)
	}
}

// Substitution is per element and textual, so a placeholder sharing its
// element with other text still resolves, and elements without one are
// passed through untouched.
func TestLaunchOps_SubstitutesInPlaceWithinAnElement(t *testing.T) {
	got := clauthArgv(t, []string{"sh", "-c", "exec claude-as {account}"}, "work", nil)
	want := []string{"sh", "-c", "exec claude-as work"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RunArgv = %v, want %v", got, want)
	}
}

// The builder must not alias the caller's slice: Build is called per submit
// and a mutated template would leak the previous account into the next
// launch.
func TestLaunchOps_DoesNotMutateTheLauncher(t *testing.T) {
	launcher := []string{"claude-as", "{account}"}
	if got := clauthArgv(t, launcher, "work", nil); got[1] != "work" {
		t.Fatalf("RunArgv[1] = %q, want %q", got[1], "work")
	}
	if launcher[1] != "{account}" {
		t.Fatalf("launcher was mutated to %v; Build must copy it", launcher)
	}
	if got := clauthArgv(t, launcher, "personal", nil); got[1] != "personal" {
		t.Fatalf("second launch argv[1] = %q, want %q", got[1], "personal")
	}
}

// The `sh -c "…{account}"` shape is accepted by the validator (one
// placeholder) but STRANDS ExtraArgs: they are appended after the whole
// template, so they become the inner shell's $0 and $1 and never reach the
// agent, while the launch step still reports ok. An earlier comment
// recommended this shape. Pinning the actual argv is what stops the
// recommendation coming back.
func TestLaunchOps_ShCLauncherStrandsExtraArgs(t *testing.T) {
	got := clauthArgv(t, []string{"sh", "-c", "exec claude-as {account}"}, "work",
		[]string{"--model", "opus"})
	want := []string{"sh", "-c", "exec claude-as work", "--model", "opus"}
	if !reflect.DeepEqual(got, want) {
		// Errorf, not Fatalf: the assertion below must stay REACHABLE. With a
		// Fatalf here it could never fail -- got[2] is fixed by the DeepEqual
		// that precedes it -- which makes it decoration by this repo's own
		// test: what single change to the code would make this fail?
		t.Errorf("RunArgv = %v, want %v", got, want)
	}
	// The point of the row: --model opus is NOT inside the -c string, so the
	// inner shell never passes it on. This is what actually fails if someone
	// "fixes" the stranding by splicing ExtraArgs into the -c string.
	if len(got) > 2 && strings.Contains(got[2], "--model") {
		t.Errorf("ExtraArgs leaked into the -c string: %q", got[2])
	}
}

// plan holds its own fallback so a zero Input keeps working, and config
// holds the value a config.toml defaults to. Two constants for one fact is
// only safe while something holds them together -- the same reason
// TestVersionMatchesManifest exists.
func TestDefaultLauncherMatchesConfigDefault(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if !reflect.DeepEqual(defaultClauthLauncher(), cfg.Clauth.Launcher) {
		t.Fatalf("plan default %v != config default %v",
			defaultClauthLauncher(), cfg.Clauth.Launcher)
	}
}
