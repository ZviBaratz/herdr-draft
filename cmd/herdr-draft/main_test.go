package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/ZviBaratz/herdr-draft/internal/app"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
	"testing"
)

// TestClauthStatusFilePath pins the one piece of pure, testable logic in
// this file: the resolved path always ends in ".clauth/status.json" under
// whatever home directory os.UserHomeDir() reports on this machine (a real
// call -- there is no portable way to fake it without an env var this
// function doesn't accept -- but the assertion itself never depends on the
// actual value, only the suffix shape).
func TestClauthStatusFilePath(t *testing.T) {
	got := clauthStatusFilePath()
	if got == "" {
		t.Skip("os.UserHomeDir() could not be determined in this environment")
	}
	want := filepath.Join(".clauth", "status.json")
	if !strings.HasSuffix(got, want) {
		t.Errorf("clauthStatusFilePath() = %q, want a path ending in %q", got, want)
	}
}

// TestDispatch pins v2 spec §13's routing rule -- "absent means the popup,
// exactly as today; an unknown verb prints usage and exits 2" -- without
// starting a tea.Program or talking to herdr, which is the whole reason
// dispatch takes its two entry points as arguments.
func TestDispatch(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string

		wantCode       int
		wantPopup      bool
		wantCreateArgs []string
		wantStdout     string
		wantStderr     string
	}{
		{
			name:      "no arguments opens the popup",
			args:      nil,
			wantCode:  0,
			wantPopup: true,
		},
		{
			name:           "create forwards the rest of the command line",
			args:           []string{"create", "--title", "t", "--json"},
			wantCode:       0,
			wantCreateArgs: []string{"--title", "t", "--json"},
		},
		{
			name:           "create with no flags still reaches the verb",
			args:           []string{"create"},
			wantCode:       0,
			wantCreateArgs: []string{},
		},
		{
			name:       "an unknown verb exits 2 with the usage",
			args:       []string{"summon"},
			wantCode:   2,
			wantStderr: "unknown command \"summon\"",
		},
		{
			name:       "help exits 0 on stdout",
			args:       []string{"help"},
			wantCode:   0,
			wantStdout: "usage:",
		},
		{
			name:       "--help is not an unknown verb",
			args:       []string{"--help"},
			wantCode:   0,
			wantStdout: "usage:",
		},
		// All three spellings, for the same reason help takes three:
		// asking a program what it is should not require guessing which
		// convention its author picked. A user who guesses wrong on a
		// binary that has no --version at all gets `unknown command` and
		// exit 2, which reads as "this program has no version" rather
		// than "try the other spelling".
		{
			name:       "version exits 0 on stdout",
			args:       []string{"version"},
			wantCode:   0,
			wantStdout: "herdr-draft " + herdrc.Version,
		},
		{
			name:       "--version is not an unknown verb",
			args:       []string{"--version"},
			wantCode:   0,
			wantStdout: "herdr-draft " + herdrc.Version,
		},
		{
			name:       "-V is not an unknown verb",
			args:       []string{"-V"},
			wantCode:   0,
			wantStdout: "herdr-draft " + herdrc.Version,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr strings.Builder
			popupRan := false
			var createArgs []string
			createRan := false

			code := dispatch(tc.args, &stdout, &stderr,
				func() int { popupRan = true; return 0 },
				func(args []string) int { createRan, createArgs = true, args; return 0 },
				func() int { t.Fatal("the skill verb ran"); return 0 },
			)

			if code != tc.wantCode {
				t.Errorf("exit = %d, want %d", code, tc.wantCode)
			}
			if popupRan != tc.wantPopup {
				t.Errorf("popup ran = %v, want %v", popupRan, tc.wantPopup)
			}
			if wantCreate := tc.wantCreateArgs != nil; createRan != wantCreate {
				t.Errorf("create ran = %v, want %v", createRan, wantCreate)
			}
			if tc.wantCreateArgs != nil && !reflect.DeepEqual(createArgs, tc.wantCreateArgs) {
				t.Errorf("create args = %v, want %v", createArgs, tc.wantCreateArgs)
			}
			if tc.wantStdout != "" && !strings.Contains(stdout.String(), tc.wantStdout) {
				t.Errorf("stdout = %q, want it to contain %q", stdout.String(), tc.wantStdout)
			}
			if tc.wantStderr != "" && !strings.Contains(stderr.String(), tc.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tc.wantStderr)
			}
			if tc.wantCode == 2 && !strings.Contains(stderr.String(), "usage:") {
				t.Errorf("stderr = %q, want the usage alongside the refusal", stderr.String())
			}
		})
	}
}

// TestVersionStringCarriesWhatABugReportNeeds pins the content, not the
// layout. The point of adding a version at all was that a user could
// answer "what are you running?" and a bug report could state it, so the
// two facts nobody can reconstruct from the version alone have to be
// there: which plugin id this binary answers to -- forks install side by
// side under different ids -- and what compiled it.
func TestVersionStringCarriesWhatABugReportNeeds(t *testing.T) {
	got := versionString()

	for _, want := range []string{
		"herdr-draft " + herdrc.Version,
		herdrc.PluginID,
		runtime.Version(),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("versionString() does not contain %q:\n%s", want, got)
		}
	}
}

// TestVersionStringOmitsAnUnstampedBuild covers the case an INSTALLED
// plugin is always in. herdr's own [[build]] runs a plain argv with no
// shell, so it cannot compute a `git describe` and main.build stays empty
// -- an empty "build" line would be a field that looks broken rather than
// absent.
func TestVersionStringOmitsAnUnstampedBuild(t *testing.T) {
	saved := build
	t.Cleanup(func() { build = saved })

	build = ""
	if got := versionString(); strings.Contains(got, "build") {
		t.Errorf("versionString() with no stamp mentions a build:\n%s", got)
	}

	build = "v0.1.0-3-gabc1234"
	if got := versionString(); !strings.Contains(got, "v0.1.0-3-gabc1234") {
		t.Errorf("versionString() dropped the stamped build:\n%s", got)
	}
}

// TestDispatchRoutesSkill covers the third verb. It is a separate function
// rather than a row in TestDispatch's table because the table's other rows
// assert the skill verb does NOT run, and a case that inverts that reads
// better on its own than as a fourth "want" column.
func TestDispatchRoutesSkill(t *testing.T) {
	var stdout, stderr strings.Builder
	called := 0
	skillVerb := func() int { called = 1; return 0 }

	code := dispatch([]string{"skill"}, &stdout, &stderr,
		func() int { t.Fatal("popup ran for `skill`"); return 0 },
		func([]string) int { t.Fatal("create ran for `skill`"); return 0 },
		skillVerb)

	if code != 0 {
		t.Errorf("dispatch(skill) = %d, want 0", code)
	}
	if called != 1 {
		t.Error("dispatch did not route `skill` to the skill verb")
	}
}

// TestRunSkillStampsTheBuildersVersion pins the one thing skill.Run itself
// cannot: that this call site hands it herdrc.Version and not some other
// string. Every test in package skill passes with a call site supplying
// "dev", and the stamp is the only thing that tells a user their installed
// copy is stale.
func TestRunSkillStampsTheBuildersVersion(t *testing.T) {
	var stdout, stderr strings.Builder

	if code := runSkill(&stdout, &stderr); code != 0 {
		t.Fatalf("runSkill = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), herdrc.Version) {
		t.Errorf("the rendered skill does not carry herdrc.Version (%q) -- "+
			"an installed copy would never look stale", herdrc.Version)
	}
}

// --- quitting takes the picker with it (#211) -------------------------------

// The popup's LAST act is to cancel the work it started and wait for that
// cancellation to be delivered. Cancelling alone is not enough and this is the
// test that says so: exec.CommandContext kills the picker from a watcher
// goroutine, so a process that cancels and exits in the same breath can exit
// first -- and a picker that survives its caller goes on to write the ledger
// entry #211 is about.
func TestQuittingWaitsForTheCancellationToLand(t *testing.T) {
	lt := app.NewLifetime()
	started, returned := make(chan struct{}), make(chan struct{})
	go func() {
		ctx, done := lt.Begin()
		defer done()
		close(started)
		<-ctx.Done()
		// Stands in for the kill: work that happens after the cancellation
		// and before the call returns. Well inside the shutdown grace.
		time.Sleep(80 * time.Millisecond)
		close(returned)
	}()
	<-started

	var stderr bytes.Buffer
	if code := runProgram(&stderr, lt, func() error { return nil }); code != 0 {
		t.Fatalf("runProgram of a clean popup returned %d, want 0 (stderr: %s)", code, stderr.String())
	}
	select {
	case <-returned:
	default:
		t.Fatal("the process was ready to exit while the cancelled pick was still running -- the picker would outlive the popup")
	}
}

// And a popup that fails is still a popup that quit: the exit code is the
// program's, but the work it started is cancelled either way.
func TestAFailedRunStillCancelsTheWork(t *testing.T) {
	lt := app.NewLifetime()
	ctx, done := lt.Begin()
	defer done()

	var stderr bytes.Buffer
	if code := runProgram(&stderr, lt, func() error { return errors.New("terminal is not a terminal") }); code != 1 {
		t.Fatalf("runProgram of a failed popup returned %d, want 1", code)
	}
	if err := ctx.Err(); !errors.Is(err, context.Canceled) {
		t.Fatalf("after a failed run the work's context reported %v, want context.Canceled", err)
	}
	if !strings.Contains(stderr.String(), "terminal is not a terminal") {
		t.Fatalf("stderr = %q, want the program's own error", stderr.String())
	}
}
