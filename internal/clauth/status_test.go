package clauth

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeClauth writes a disposable shell script to t.TempDir() that logs its
// argv (one line per invocation) to argvLog and echoes stdout verbatim --
// the same idiom as internal/herdrc/runner_test.go's fakeHerdr, adapted for
// clauth.
func fakeClauth(t *testing.T, stdout string) (bin, argvLog string) {
	t.Helper()
	dir := t.TempDir()
	argvLog = filepath.Join(dir, "argv")
	bin = filepath.Join(dir, "clauth")
	script := "#!/bin/sh\necho \"$@\" >> " + argvLog + "\ncat <<'EOF'\n" + stdout + "\nEOF\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, argvLog
}

// fakeClauthFail writes a disposable shell script that always fails with
// the given stderr message and exit code 1.
func fakeClauthFail(t *testing.T, stderr string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "clauth")
	script := "#!/bin/sh\necho \"" + stderr + "\" 1>&2\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func readFixture(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return b
}

func fixtureBytes(t *testing.T) []byte {
	t.Helper()
	return readFixture(t, filepath.Join("testdata", "status.json"))
}

func TestParseStatusFixture(t *testing.T) {
	st, err := ParseStatus(fixtureBytes(t))
	if err != nil {
		t.Fatalf("ParseStatus: %v", err)
	}
	if st.Degraded {
		t.Error("Degraded = true, want false for schema 1")
	}
	if st.Schema != 1 {
		t.Errorf("Schema = %d, want 1", st.Schema)
	}
	if st.ActiveProfile != "gamma" {
		t.Errorf("ActiveProfile = %q, want gamma", st.ActiveProfile)
	}
	if st.RefreshIntervalMS != 90000 {
		t.Errorf("RefreshIntervalMS = %d, want 90000", st.RefreshIntervalMS)
	}
	wantGeneratedAt := time.Date(2026, 8, 31, 20, 48, 55, 0, time.UTC)
	if !st.GeneratedAt.Equal(wantGeneratedAt) {
		t.Errorf("GeneratedAt = %v, want %v", st.GeneratedAt, wantGeneratedAt)
	}

	if len(st.Profiles) != 3 {
		t.Fatalf("len(Profiles) = %d, want 3", len(st.Profiles))
	}
	wantNames := []string{"alpha", "beta", "gamma"}
	for i, name := range wantNames {
		if st.Profiles[i].Name != name {
			t.Errorf("Profiles[%d].Name = %q, want %q", i, st.Profiles[i].Name, name)
		}
	}

	alpha := st.Profiles[0]
	if alpha.AuthStatus != "ok" {
		t.Errorf("alpha.AuthStatus = %q, want ok", alpha.AuthStatus)
	}
	if alpha.Tier != "Team" {
		t.Errorf("alpha.Tier = %q, want Team", alpha.Tier)
	}
	if alpha.Active {
		t.Error("alpha.Active = true, want false")
	}
	if len(alpha.Windows) != 3 {
		t.Fatalf("len(alpha.Windows) = %d, want 3", len(alpha.Windows))
	}
	w0 := alpha.Windows[0]
	if w0.Label != "5h" {
		t.Errorf("alpha.Windows[0].Label = %q, want 5h", w0.Label)
	}
	if w0.UtilizationPct != 95.0 {
		t.Errorf("alpha.Windows[0].UtilizationPct = %v, want 95.0", w0.UtilizationPct)
	}
	wantResetsAt := time.Date(2026, 8, 31, 20, 50, 0, 410263000, time.UTC)
	if !w0.ResetsAt.Equal(wantResetsAt) {
		t.Errorf("alpha.Windows[0].ResetsAt = %v, want %v", w0.ResetsAt, wantResetsAt)
	}

	gamma := st.Profiles[2]
	if !gamma.Active {
		t.Error("gamma.Active = false, want true")
	}
}

func TestParseStatusUnknownSchemaDegrades(t *testing.T) {
	raw := fixtureBytes(t)
	mutated := bytes.Replace(raw, []byte(`"schema": 1,`), []byte(`"schema": 99,`), 1)
	if bytes.Equal(mutated, raw) {
		t.Fatal("fixture does not contain the expected schema field; test setup is broken")
	}

	st, err := ParseStatus(mutated)
	if err != nil {
		t.Fatalf("ParseStatus: %v", err)
	}
	if !st.Degraded {
		t.Error("Degraded = false, want true for schema 99")
	}
	if len(st.Profiles) != 3 {
		t.Fatalf("len(Profiles) = %d, want 3", len(st.Profiles))
	}
	wantNames := []string{"alpha", "beta", "gamma"}
	for i, name := range wantNames {
		if st.Profiles[i].Name != name {
			t.Errorf("Profiles[%d].Name = %q, want %q", i, st.Profiles[i].Name, name)
		}
	}
}

func TestParseStatusInvalidJSON(t *testing.T) {
	if _, err := ParseStatus([]byte("not json")); err == nil {
		t.Fatal("expected an error for invalid JSON, got nil")
	}
}

func TestLoadPrefersFreshStatusFile(t *testing.T) {
	dir := t.TempDir()
	statusFile := filepath.Join(dir, "status.json")
	if err := os.WriteFile(statusFile, fixtureBytes(t), 0o644); err != nil {
		t.Fatal(err)
	}

	// generated_at is 2026-08-31T20:48:55Z, refresh_interval_ms is 90000
	// (90s); fresh until generated_at + 180s = 2026-08-31T20:51:55Z. Pick a
	// Now() just before that boundary.
	now := time.Date(2026, 8, 31, 20, 51, 0, 0, time.UTC)
	cliBin := fakeClauthFail(t, "CLIBin must not be invoked when the status file is fresh")

	st, err := Load(context.Background(), LoadOpts{
		StatusFile: statusFile,
		CLIBin:     cliBin,
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if st.ActiveProfile != "gamma" {
		t.Errorf("ActiveProfile = %q, want gamma (from status file)", st.ActiveProfile)
	}
}

func TestLoadFallsBackToCLIWhenStatusFileStale(t *testing.T) {
	dir := t.TempDir()
	statusFile := filepath.Join(dir, "status.json")
	if err := os.WriteFile(statusFile, fixtureBytes(t), 0o644); err != nil {
		t.Fatal(err)
	}

	// Fresh window ends at 2026-08-31T20:51:55Z; pick a Now() well past it.
	now := time.Date(2026, 8, 31, 21, 30, 0, 0, time.UTC)
	cliStdout := `{"schema":1,"active_profile":"alpha","generated_at":"2026-08-31T21:29:00+00:00","refresh_interval_ms":90000,"profiles":[{"name":"alpha","active":true,"provider":"anthropic","tier":"Team","auth_status":"ok","windows":[]}]}`
	cliBin, argvLog := fakeClauth(t, cliStdout)

	st, err := Load(context.Background(), LoadOpts{
		StatusFile: statusFile,
		CLIBin:     cliBin,
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if st.ActiveProfile != "alpha" {
		t.Errorf("ActiveProfile = %q, want alpha (from CLI, not stale status file)", st.ActiveProfile)
	}

	logged, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatalf("CLIBin was not invoked: %v", err)
	}
	if got := strings.TrimSpace(string(logged)); got != "status --json" {
		t.Errorf("argv = %q, want %q", got, "status --json")
	}
}

func TestLoadMissingStatusFileFallsBackToCLI(t *testing.T) {
	now := time.Date(2026, 8, 31, 21, 30, 0, 0, time.UTC)
	cliBin, argvLog := fakeClauth(t, string(fixtureBytes(t)))

	st, err := Load(context.Background(), LoadOpts{
		StatusFile: filepath.Join(t.TempDir(), "does-not-exist.json"),
		CLIBin:     cliBin,
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if st.ActiveProfile != "gamma" {
		t.Errorf("ActiveProfile = %q, want gamma (from CLI)", st.ActiveProfile)
	}
	if _, err := os.Stat(argvLog); err != nil {
		t.Errorf("CLIBin was not invoked: %v", err)
	}
}

func TestLoadBothMissingReturnsError(t *testing.T) {
	_, err := Load(context.Background(), LoadOpts{
		StatusFile: filepath.Join(t.TempDir(), "does-not-exist.json"),
		CLIBin:     "",
		Now:        func() time.Time { return time.Now() },
	})
	if err == nil {
		t.Fatal("expected an error when both the status file and CLIBin are missing, got nil")
	}
}

func TestLoadCLIFailurePropagatesError(t *testing.T) {
	cliBin := fakeClauthFail(t, "not logged in")
	_, err := Load(context.Background(), LoadOpts{
		CLIBin: cliBin,
		Now:    func() time.Time { return time.Now() },
	})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("error %q does not contain stderr content", err.Error())
	}
}
