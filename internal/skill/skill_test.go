package skill

import (
	"errors"
	"io"
	"regexp"
	"strings"
	"testing"
)

func TestRenderSubstitutesEveryPlaceholder(t *testing.T) {
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

	if code := Run(nil, &stdout, &stderr, exe, noRead(t), "1.2.3"); code != 0 {
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

	if code := Run(nil, &stdout, &stderr, exe, noRead(t), "1.2.3"); code != 0 {
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

// --- the digest and --check (#311) -----------------------------------------

func TestDigestIsAShortHashOfTheTemplate(t *testing.T) {
	d := Digest()
	if !regexp.MustCompile(`^[0-9a-f]{12}$`).MatchString(d) {
		t.Fatalf("Digest() = %q, want twelve lowercase hex digits", d)
	}
	if d != digestOf(spawnSkillDoc) {
		t.Error("Digest() is not the digest of the embedded template")
	}
	// Not vacuous: one byte of the source moves it, which is the whole
	// reason it exists -- the version does not move within a release.
	if digestOf(spawnSkillDoc+"x") == d {
		t.Error("changing the template does not change its digest")
	}
	// And it is a digest of the SOURCE, not of a render: the path and the
	// version are what differ between two machines on the same build.
	if strings.Contains(spawnSkillDoc, d) {
		t.Error("the template contains its own digest, which no hash of it can")
	}
}

func TestRenderStampsTheDigest(t *testing.T) {
	out := Render("/opt/bin/herdr-draft", "1.2.3")
	if !strings.Contains(out, "herdr-draft 1.2.3, skill "+Digest()) {
		t.Errorf("the footer does not carry the version and the digest together:\n%s", tail(out))
	}
}

func tail(s string) string {
	if len(s) > 800 {
		return s[len(s)-800:]
	}
	return s
}

// checkRun runs `skill --check <p>` against a reader that serves installed.
func checkRun(t *testing.T, installed string, readErr error) (int, string, string) {
	t.Helper()
	var stdout, stderr strings.Builder
	exe := func() (string, error) { return "/opt/bin/herdr-draft", nil }
	read := func(p string) ([]byte, error) {
		if p != "/home/u/SKILL.md" {
			t.Errorf("read %q, want the path given to --check", p)
		}
		return []byte(installed), readErr
	}
	code := Run([]string{"--check", "/home/u/SKILL.md"}, &stdout, &stderr, exe, read, "1.2.3")
	return code, stdout.String(), stderr.String()
}

func TestCheckAcceptsACurrentCopy(t *testing.T) {
	code, stdout, stderr := checkRun(t, Render("/opt/bin/herdr-draft", "1.2.3"), nil)
	if code != 0 {
		t.Errorf("--check on a current copy = %d, want 0 (stdout %q, stderr %q)", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "current") {
		t.Errorf("stdout = %q, want a verdict saying the copy is current", stdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

// TestCheckNamesWhyACopyIsStale is the table of verdicts. Each row changes
// ONE thing about an otherwise current copy, so the reason reported is the
// reason for that row and not for some other difference riding along.
func TestCheckNamesWhyACopyIsStale(t *testing.T) {
	current := Render("/opt/bin/herdr-draft", "1.2.3")
	stamp := "herdr-draft 1.2.3, skill " + Digest()
	if !strings.Contains(current, stamp) {
		t.Fatalf("fixture premise: the render does not carry %q", stamp)
	}
	for _, tc := range []struct {
		name, installed, want string
	}{
		{"content changed", strings.Replace(current, stamp, "herdr-draft 1.2.3, skill 000000000000", 1), "different skill source"},
		{"version changed", strings.Replace(current, stamp, "herdr-draft 1.2.2, skill "+Digest(), 1), "version 1.2.2"},
		{"binary moved", Render("/old/bin/herdr-draft", "1.2.3"), "different binary path"},
		{"edited by hand", strings.Replace(current, "# Spawning", "# Spawnin", 1), "edited since"},
		{"no stamp at all", strings.Replace(current, ", skill "+Digest(), "", 1), "no skill digest"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.installed == current {
				t.Fatal("fixture premise: this row changed nothing")
			}
			code, stdout, stderr := checkRun(t, tc.installed, nil)
			if code != 1 {
				t.Errorf("--check = %d, want 1", code)
			}
			if !strings.Contains(stdout, "stale") || !strings.Contains(stdout, tc.want) {
				t.Errorf("stdout = %q, want a stale verdict naming %q", stdout, tc.want)
			}
			// The fix travels with the verdict: the reader who has it can
			// act without looking it up.
			if !strings.Contains(stdout, `'/opt/bin/herdr-draft' skill > '/home/u/SKILL.md'`) {
				t.Errorf("stdout = %q, want the regenerate command", stdout)
			}
			if stderr != "" {
				t.Errorf("stderr = %q, want empty -- a stale copy is an answer, not an error", stderr)
			}
		})
	}
}

func TestCheckRefusesWhatItCannotAnswer(t *testing.T) {
	code, stdout, stderr := checkRun(t, "", errors.New("no such file"))
	if code != 2 {
		t.Errorf("--check on an unreadable file = %d, want 2", code)
	}
	if stdout != "" || !strings.Contains(stderr, "no such file") {
		t.Errorf("stdout %q, stderr %q: want nothing on stdout and the read error on stderr", stdout, stderr)
	}

	for _, args := range [][]string{{"--check"}, {"--bogus"}, {"extra"}, {"--check", "a", "b"}} {
		var stdout, stderr strings.Builder
		exe := func() (string, error) { return "/opt/bin/herdr-draft", nil }
		read := func(string) ([]byte, error) { t.Errorf("%q read a file", args); return nil, nil }
		if code := Run(args, &stdout, &stderr, exe, read, "1.2.3"); code != 2 {
			t.Errorf("Run(%q) = %d, want 2", args, code)
		}
		if stdout.String() != "" {
			t.Errorf("Run(%q) wrote to stdout: a usage error must not look like a skill file", args)
		}
		if !strings.Contains(stderr.String(), "--check") {
			t.Errorf("Run(%q) stderr = %q, want the usage", args, stderr.String())
		}
	}
}

// noRead is the reader for a Run that must not read anything.
func noRead(t *testing.T) func(string) ([]byte, error) {
	return func(p string) ([]byte, error) { t.Errorf("read %q without --check", p); return nil, nil }
}

// TestCheckWithoutItsOwnPathHasNoVerdict: every render names the binary's
// path, so a check that could not learn it would compare a correct copy
// against the fallback name, call it stale, and offer to regenerate it
// with a bare command name in place of a working path. Unknown is not
// stale.
func TestCheckWithoutItsOwnPathHasNoVerdict(t *testing.T) {
	var stdout, stderr strings.Builder
	exe := func() (string, error) { return "", errors.New("no /proc") }
	read := func(string) ([]byte, error) { return []byte(Render("/opt/bin/herdr-draft", "1.2.3")), nil }
	if code := Run([]string{"--check", "/p"}, &stdout, &stderr, exe, read, "1.2.3"); code != 2 {
		t.Errorf("--check without the binary's path = %d, want 2", code)
	}
	if stdout.String() != "" {
		t.Errorf("stdout = %q, want no verdict", stdout.String())
	}
	if !strings.Contains(stderr.String(), "no /proc") {
		t.Errorf("stderr = %q, want the reason", stderr.String())
	}
}

// TestCheckQuotesTheRegenerateCommand: the command is printed to be
// pasted, so a path is quoted for the shell it will be pasted into rather
// than wrapped in double quotes, inside which $ and ` still expand.
func TestCheckQuotesTheRegenerateCommand(t *testing.T) {
	var stdout strings.Builder
	exe := func() (string, error) { return "/opt/it's/herdr-draft", nil }
	read := func(string) ([]byte, error) { return []byte("old"), nil }
	Run([]string{"--check", "/p/a$HOME`x`.md"}, &stdout, io.Discard, exe, read, "1.2.3")
	if want := `'/opt/it'\''s/herdr-draft' skill > '/p/a$HOME` + "`x`" + `.md'`; !strings.Contains(stdout.String(), want) {
		t.Errorf("stdout = %q, want the command %s", stdout.String(), want)
	}
}

// TestSkillHelpIsNotAUsageError: asking for help exits 0 with the usage on
// stdout, as `herdr-draft --help` does.
func TestSkillHelpIsNotAUsageError(t *testing.T) {
	for _, arg := range []string{"-h", "--help", "help"} {
		var stdout, stderr strings.Builder
		exe := func() (string, error) { return "/opt/bin/herdr-draft", nil }
		if code := Run([]string{arg}, &stdout, &stderr, exe, noRead(t), "1.2.3"); code != 0 {
			t.Errorf("skill %s = %d, want 0", arg, code)
		}
		if !strings.Contains(stdout.String(), "--check <path>") || stderr.String() != "" {
			t.Errorf("skill %s: stdout %q, stderr %q; want the usage on stdout", arg, stdout.String(), stderr.String())
		}
	}
}
