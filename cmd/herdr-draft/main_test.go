package main

import (
	"path/filepath"
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
