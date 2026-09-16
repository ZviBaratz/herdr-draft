package skill

import (
	"errors"
	"strings"
	"testing"
)

func TestRenderSubstitutesBothPlaceholders(t *testing.T) {
	out := Render("/opt/plugins/draft/bin/herdr-draft", "1.2.3")

	if !strings.Contains(out, "/opt/plugins/draft/bin/herdr-draft") {
		t.Error("rendered skill does not name the binary path it was given")
	}
	if !strings.Contains(out, "1.2.3") {
		t.Error("rendered skill does not carry the version it was given")
	}
	if strings.Contains(out, "{{") {
		t.Errorf("rendered skill still contains an unsubstituted placeholder:\n%s", out)
	}
}

func TestRunWritesTheDocumentToStdout(t *testing.T) {
	var stdout, stderr strings.Builder
	exe := func() (string, error) { return "/opt/bin/herdr-draft", nil }

	if code := Run(&stdout, &stderr, exe, "1.2.3"); code != 0 {
		t.Errorf("Run = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "/opt/bin/herdr-draft") {
		t.Error("stdout does not carry the resolved binary path")
	}
	if stderr.String() != "" {
		t.Errorf("stderr = %q, want empty on the happy path", stderr.String())
	}
}

func TestRunKeepsStdoutCleanWhenThePathCannotBeResolved(t *testing.T) {
	var stdout, stderr strings.Builder
	exe := func() (string, error) { return "", errors.New("no /proc") }

	if code := Run(&stdout, &stderr, exe, "1.2.3"); code != 0 {
		t.Errorf("Run = %d, want 0 -- an unresolvable path degrades, it does not fail", code)
	}
	// The whole point: `herdr-draft skill > SKILL.md` must not write a
	// warning into the skill file.
	//
	// Matched on the warning's own "<verb>: " prefix rather than on a
	// phrase from its body. The document is prose about prompt delivery
	// and says "could not be confirmed" in it quite legitimately, so a
	// looser needle asserts something about the skill's wording instead
	// of something about the writer it went to.
	if strings.Contains(stdout.String(), "herdr-draft skill:") {
		t.Error("the warning reached stdout, which would corrupt a redirected skill file")
	}
	if !strings.HasPrefix(stdout.String(), "---") {
		t.Error("stdout does not begin with the frontmatter fence")
	}
	if !strings.Contains(stderr.String(), "herdr-draft skill:") {
		t.Errorf("stderr does not explain the fallback: %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), fallbackBinName) {
		t.Error("stdout does not fall back to the plain command name")
	}
}
