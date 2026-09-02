package main

import (
	"path/filepath"
	"reflect"
	"strings"
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
