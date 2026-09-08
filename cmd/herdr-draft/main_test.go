package main

import (
	"path/filepath"
	"reflect"
	"runtime"
	"strings"

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
