package picker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// stubPicker writes an executable shell script that prints body on stdout and
// note on stderr, exits with code, and records its own argv one argument per
// line in <dir>/argv so a test can assert the exact invocation.
func stubPicker(t *testing.T, body, note string, code int) (bin, argvFile string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("stub picker is a POSIX shell script")
	}
	dir := t.TempDir()
	bin = filepath.Join(dir, "stub-picker")
	argvFile = filepath.Join(dir, "argv")
	script := "#!/bin/sh\n" +
		": > " + argvFile + "\n" +
		"for a in \"$@\"; do printf '%s\\n' \"$a\" >> " + argvFile + "; done\n" +
		"cat <<'STUBOUT'\n" + body + "\nSTUBOUT\n" +
		"printf '%s' " + shQuote(note) + " >&2\n" +
		"exit " + fmt.Sprint(code) + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return bin, argvFile
}

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

const stubPicked = `{"profile":"alpha-1","tier":"Max","config_dir":"/tmp/dirs/alpha-1",
"reason":null,"usage":{"five_hour":7,"weekly":12,"cache_age_s":30},
"resets_at":{"five_hour":"22:49","weekly":"Sun"},"skipped":[],"warnings":[]}`

const stubRefused = `{"profile":null,"config_dir":null,"reason":"pool exhausted (resets 22:49)",
"usage":{"five_hour":null,"weekly":null,"cache_age_s":null},
"resets_at":{"five_hour":null,"weekly":null},"skipped":[],"warnings":[]}`

func TestArgvIsTheDocumentedInvocation(t *testing.T) {
	if got, want := strings.Join(Argv("claude-pick", "/p/thing", Options{}), " "),
		"claude-pick --dir /p/thing --json"; got != want {
		t.Fatalf("Argv = %q, want %q", got, want)
	}
	if got, want := strings.Join(Argv("claude-pick", "/p/thing", Options{Strict: true, DryRun: true}), " "),
		"claude-pick --dir /p/thing --json --strict --dry-run"; got != want {
		t.Fatalf("Argv = %q, want %q", got, want)
	}
}

func TestPickPassesTheDocumentedFlagsAndDecodes(t *testing.T) {
	bin, argvFile := stubPicker(t, stubPicked, "", 0)
	got, err := CLI{Bin: bin}.Pick(context.Background(), "/p/thing", Options{DryRun: true})
	if err != nil {
		t.Fatalf("Pick: %v", err)
	}
	if got.Profile != "alpha-1" || got.ConfigDir != "/tmp/dirs/alpha-1" {
		t.Fatalf("Pick = %+v", got)
	}
	argv, _ := os.ReadFile(argvFile)
	if want := "--dir\n/p/thing\n--json\n--dry-run\n"; string(argv) != want {
		t.Fatalf("argv = %q, want %q", argv, want)
	}
}

// A refusal is a decision the caller branches on, so it must arrive as a
// *RefusalError carrying the picker's OWN reason -- not as "exit status 2".
func TestPickReportsARefusalWithThePickersReason(t *testing.T) {
	bin, _ := stubPicker(t, stubRefused, "", ExitExhausted)
	_, err := CLI{Bin: bin}.Pick(context.Background(), "/p/thing", Options{})
	var refusal *RefusalError
	if !errors.As(err, &refusal) {
		t.Fatalf("Pick error = %v, want *RefusalError", err)
	}
	if refusal.Code != ExitExhausted {
		t.Fatalf("Code = %d", refusal.Code)
	}
	if got, want := refusal.Short(), "pool exhausted (resets 22:49)"; got != want {
		t.Fatalf("Short() = %q, want %q", got, want)
	}
}

// A picker that refuses without JSON on stdout still has to produce an
// actionable line: its stderr is the fallback, and the code's meaning is the
// fallback to that.
func TestPickFallsBackToStderrThenToTheCodesMeaning(t *testing.T) {
	bin, _ := stubPicker(t, "", "claude-pick: the tenant table is unreadable", ExitBadTable)
	_, err := CLI{Bin: bin}.Pick(context.Background(), "/p/thing", Options{})
	var refusal *RefusalError
	if !errors.As(err, &refusal) {
		t.Fatalf("Pick error = %v, want *RefusalError", err)
	}
	if got, want := refusal.Short(), "claude-pick: the tenant table is unreadable"; got != want {
		t.Fatalf("Short() = %q, want %q", got, want)
	}

	bin, _ = stubPicker(t, "", "", ExitNoProfiles)
	_, err = CLI{Bin: bin}.Pick(context.Background(), "/p/thing", Options{})
	if !errors.As(err, &refusal) {
		t.Fatalf("Pick error = %v, want *RefusalError", err)
	}
	if got, want := refusal.Short(), "no profiles registered"; got != want {
		t.Fatalf("Short() = %q, want %q", got, want)
	}
}

// Exit 0 with no profile is not a pick. The protocol says 0 means picked, so
// a picker answering 0 with a null profile is broken, and treating it as a
// successful empty pin would launch under whatever account happened to be
// live while the row said the picker had chosen.
func TestPickRejectsAPicklessSuccess(t *testing.T) {
	bin, _ := stubPicker(t, stubRefused, "", ExitPicked)
	c := CLI{Bin: bin}
	if _, err := c.Pick(context.Background(), "/p/thing", Options{}); err == nil {
		t.Fatal("exit 0 with a null profile must be an error")
	}
}

func TestPickReportsAnAbsentExecutable(t *testing.T) {
	_, err := CLI{Bin: filepath.Join(t.TempDir(), "nope")}.Pick(context.Background(), "/p", Options{})
	if err == nil {
		t.Fatal("a missing picker must be an error")
	}
	var refusal *RefusalError
	if errors.As(err, &refusal) {
		t.Fatal("a missing executable is not a refusal -- the picker never ran")
	}
}

// Probe is what stands between herdr-draft and a same-named stranger. A
// program on PATH called claude-pick that prints valid JSON of the wrong
// shape must be refused, naming the missing keys.
func TestProbeRejectsJSONOfTheWrongShape(t *testing.T) {
	bin, _ := stubPicker(t, `{"account":"alpha-1","ok":true}`, "", 0)
	err := CLI{Bin: bin}.Probe(context.Background(), "/p/thing")
	if err == nil {
		t.Fatal("Probe must reject an answer missing every documented key")
	}
	for _, key := range []string{"profile", "config_dir", "usage.five_hour"} {
		if !strings.Contains(err.Error(), key) {
			t.Fatalf("Probe error %q should name the missing key %q", err, key)
		}
	}
}

func TestProbeAcceptsTheDocumentedShapeAndIsADryRun(t *testing.T) {
	bin, argvFile := stubPicker(t, stubPicked, "", 0)
	c := CLI{Bin: bin}
	if err := c.Probe(context.Background(), "/p/thing"); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	argv, _ := os.ReadFile(argvFile)
	if !strings.Contains(string(argv), "--dry-run") {
		t.Fatalf("Probe must be a --dry-run, argv = %q", argv)
	}
}

// A machine with an exhausted pool, or no tenant table, still HAS a
// conformant picker. Probing must judge the shape of the answer, not the
// answer -- otherwise the feature turns itself off on exactly the day the
// user most needs to see why.
func TestProbeAcceptsAConformantRefusal(t *testing.T) {
	bin, _ := stubPicker(t, stubRefused, "", ExitExhausted)
	c := CLI{Bin: bin}
	if err := c.Probe(context.Background(), "/p/thing"); err != nil {
		t.Fatalf("Probe of a conformant refusal: %v", err)
	}
}

func TestProbeRejectsAnUndocumentedExitCode(t *testing.T) {
	bin, _ := stubPicker(t, stubPicked, "", 7)
	err := CLI{Bin: bin}.Probe(context.Background(), "/p/thing")
	if err == nil || !strings.Contains(err.Error(), "7") {
		t.Fatalf("Probe error = %v, want one naming exit 7", err)
	}
}
