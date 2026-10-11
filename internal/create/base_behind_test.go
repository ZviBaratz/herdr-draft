package create

import (
	"errors"
	"strings"
	"testing"
)

// These pin #354 on the command side: a base that is a local branch behind
// the remote-tracking branch it tracks gets one `herdr-draft create:` line
// saying so, and the create goes ahead from it as asked. Which bases are
// such a branch is gitx.BaseBehind's question and is tested there against
// real git; here the fake answers, and what is pinned is when the command
// asks, what it prints, and that the answer never stops a create.

const behindLine = "herdr-draft create: local main is 38 commits behind origin/main as of the last fetch -- pass --base origin/main to start from it, or update main\n"

// behindHarness is a harness whose main is 38 commits behind origin/main.
func behindHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.git.behind = map[string]fakeBehind{"/projects/thing main": {"origin/main", 38}}
	return h
}

func TestBaseBehind_AnExplicitLocalBaseSaysSo(t *testing.T) {
	h := behindHarness(t)

	if code := h.run("--title", "t", "--worktree", "--base", "main"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	if !strings.Contains(h.stderr.String(), behindLine) {
		t.Errorf("stderr = %q, want the line %q", h.stderr, behindLine)
	}
	// Advice, not a substitution: the base asked for is the base used.
	if base := createdFrom(t, h); base != "main" {
		t.Errorf("worktree create base = %q, want main as asked", base)
	}
}

// TestBaseBehind_ADryRunSaysSo: the dry run is the cheapest place to catch
// it, and the line comes from the resolution a dry run shares.
func TestBaseBehind_ADryRunSaysSo(t *testing.T) {
	h := behindHarness(t)

	if code := h.run("--title", "t", "--worktree", "--base", "main", "--dry-run", "--json"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	if !strings.Contains(h.stderr.String(), behindLine) {
		t.Errorf("stderr = %q, want the line %q", h.stderr, behindLine)
	}
	if creates := worktreeCreates(h); len(creates) != 0 {
		t.Errorf("a dry run created worktrees: %v", creates)
	}
}

// TestBaseBehind_ARememberedBaseSaysSo: a tier's main is as stale as a
// typed one.
func TestBaseBehind_ARememberedBaseSaysSo(t *testing.T) {
	h := behindHarness(t)
	rememberBase(t, h, "main")

	if code := h.run("--title", "t", "--worktree"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	if !strings.Contains(h.stderr.String(), behindLine) {
		t.Errorf("stderr = %q, want the line %q", h.stderr, behindLine)
	}
}

// TestBaseBehind_NothingToSayPrintsNothing: a base the fake has nothing to
// say about is asked about and prints nothing, and a run with no base or
// no worktree does not ask at all -- main is behind in every case, so a
// question asked where it should not be would print.
func TestBaseBehind_NothingToSayPrintsNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		ask  bool
	}{
		{"a remote-tracking base", []string{"--worktree", "--base", "origin/develop"}, true},
		{"a branch with nothing behind", []string{"--worktree", "--base", "dev"}, true},
		{"a commit spelled from HEAD", []string{"--worktree", "--base", "HEAD~1"}, true},
		{"no base", []string{"--worktree"}, false},
		{"HEAD", []string{"--worktree", "--base", "HEAD"}, false},
		{"no worktree", []string{"--no-worktree", "--base", "main"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := behindHarness(t)
			if code := h.run(append([]string{"--title", "t"}, tc.args...)...); code != ExitOK {
				t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
			}
			if strings.Contains(h.stderr.String(), "behind") {
				t.Errorf("stderr = %q, want no stale-base line", h.stderr)
			}
			if asked := len(h.git.behindCalls) > 0; asked != tc.ask {
				t.Errorf("asked = %v (%v), want %v", asked, h.git.behindCalls, tc.ask)
			}
		})
	}
}

// TestBaseBehind_AFailedQuestionGoesAhead: the question only advises, so a
// git that could not answer it is a line saying so and a create all the
// same.
func TestBaseBehind_AFailedQuestionGoesAhead(t *testing.T) {
	h := behindHarness(t)
	h.git.behindErr = errors.New("git exploded")

	if code := h.run("--title", "t", "--worktree", "--base", "main"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	if want := `herdr-draft create: could not tell whether base "main" is behind its upstream (git exploded); going ahead`; !strings.Contains(h.stderr.String(), want) {
		t.Errorf("stderr = %q, want %q", h.stderr, want)
	}
	if base := createdFrom(t, h); base != "main" {
		t.Errorf("worktree create base = %q, want main", base)
	}
}

// TestBaseBehind_AHungQuestionGoesAhead: bounded like every pre-flight
// question (#272), and unlike the others its timeout refuses nothing.
func TestBaseBehind_AHungQuestionGoesAhead(t *testing.T) {
	h, _ := hungHarnessGit(t, "BaseBehind")

	if code := runWithin(t, h, "--title", "t", "--worktree", "--base", "main"); code != ExitOK {
		t.Fatalf("exit = %d, want %d: a stale-base question that did not answer refused the create\nstderr: %s", code, ExitOK, h.stderr)
	}
	stderr := h.stderr.String()
	if !strings.Contains(stderr, `could not tell whether base "main" is behind its upstream`) || !strings.Contains(stderr, "timed out") {
		t.Errorf("stderr = %q, want a could-not-tell line naming the timeout", stderr)
	}
	if base := createdFrom(t, h); base != "main" {
		t.Errorf("worktree create base = %q, want main", base)
	}
}
