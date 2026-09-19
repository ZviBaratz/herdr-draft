package create

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ZviBaratz/herdr-draft/internal/gitx"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
)

// #173 from the headless verb: `--on-failure clean` removes the branch its
// run made, so a retry with the same title is not refused, and says so --
// `deleted_branch` under --json, and the branch named on stderr -- because
// "cleaned" alone no longer tells a caller whether the name is free again.

// gitRepo makes a repository with one commit on main. The environment is
// stripped of GIT_* for the reason internal/gitx's gitTestEnv gives: `git
// rebase --exec` exports GIT_DIR in a linked worktree, and a helper that
// inherits it commits into the repository running the tests (#186).
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitDo(t, dir, "init", "-q", "-b", "main")
	gitDo(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
	return dir
}

func gitDo(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env,
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v (in %s): %v\n%s", args, dir, err, out)
	}
}

func localBranch(t *testing.T, repo, name string) bool {
	t.Helper()
	ok, err := gitx.LocalBranchExists(context.Background(), repo, name)
	if err != nil {
		t.Fatalf("LocalBranchExists(%s): %v", name, err)
	}
	return ok
}

// failingWorktreeCreate is a harness whose worktree create really happens
// in repo, the way herdr's does -- an existing local branch checked out, a
// new one made from the base -- and whose run then fails at the agent, so
// --on-failure has something to clean.
func failingWorktreeCreate(t *testing.T, repo string) *harness {
	t.Helper()
	h := newHarness(t)
	h.runner.failAt = "AgentStart"
	checkout := filepath.Join(t.TempDir(), "wt")
	h.runner.worktreeAdd = func(req herdrc.WorktreeCreateReq) string {
		if localBranch(t, repo, req.Branch) {
			gitDo(t, repo, "worktree", "add", "-q", checkout, req.Branch)
			return checkout
		}
		base := req.Base
		if base == "" {
			base = "HEAD"
		}
		gitDo(t, repo, "worktree", "add", "-q", "-b", req.Branch, checkout, base)
		return checkout
	}
	h.runner.worktreeRemove = func(string) { gitDo(t, repo, "worktree", "remove", checkout) }
	return h
}

func TestOnFailureCleanDeletesTheBranchItsRunMade(t *testing.T) {
	t.Run("json", func(t *testing.T) {
		repo := gitRepo(t)
		h := failingWorktreeCreate(t, repo)

		code := h.run("--title", "clean me", "--worktree", "--project", repo, "--on-failure", "clean", "--json")
		if code != ExitFailed {
			t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitFailed, h.stderr)
		}
		var out jsonReport
		if err := json.Unmarshal([]byte(h.stdout.String()), &out); err != nil {
			t.Fatalf("stdout is not JSON: %v\n%s", err, h.stdout)
		}
		if !out.Cleaned {
			t.Fatalf("cleaned = false (clean_refused = %q)", out.CleanRefused)
		}
		if out.Branch == "" || out.DeletedBranch != out.Branch {
			t.Errorf("deleted_branch = %q, want the run's branch %q", out.DeletedBranch, out.Branch)
		}
		if localBranch(t, repo, out.Branch) {
			t.Errorf("branch %s survived the clean, so a retry with the same title is refused", out.Branch)
		}
	})

	t.Run("human", func(t *testing.T) {
		repo := gitRepo(t)
		h := failingWorktreeCreate(t, repo)

		if code := h.run("--title", "clean me", "--worktree", "--project", repo, "--branch", "zvi/clean-me", "--on-failure", "clean"); code != ExitFailed {
			t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitFailed, h.stderr)
		}
		if want := "was removed (--on-failure clean), with its branch zvi/clean-me"; !strings.Contains(h.stderr.String(), want) {
			t.Errorf("stderr = %q, want %q", h.stderr, want)
		}
	})
}

// The branch was already there, and create's own refusal did not stop the
// run -- the shape of a check that failed open, which the fake git models
// by knowing no branches. herdr checks the branch out; the clean must leave
// it, and say it did.
func TestOnFailureCleanKeepsABranchItsRunDidNotMake(t *testing.T) {
	t.Run("json", func(t *testing.T) {
		repo := gitRepo(t)
		gitDo(t, repo, "branch", "zvi/old")
		h := failingWorktreeCreate(t, repo)

		code := h.run("--title", "old", "--worktree", "--project", repo, "--branch", "zvi/old", "--on-failure", "clean", "--json")
		if code != ExitFailed {
			t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitFailed, h.stderr)
		}
		var out jsonReport
		if err := json.Unmarshal([]byte(h.stdout.String()), &out); err != nil {
			t.Fatalf("stdout is not JSON: %v\n%s", err, h.stdout)
		}
		if !out.Cleaned {
			t.Fatalf("cleaned = false (clean_refused = %q)", out.CleanRefused)
		}
		if out.DeletedBranch != "" {
			t.Errorf("deleted_branch = %q, want absent -- the user made this branch", out.DeletedBranch)
		}
		if !localBranch(t, repo, "zvi/old") {
			t.Error("the clean deleted a branch the run did not make")
		}
	})

	t.Run("human", func(t *testing.T) {
		repo := gitRepo(t)
		gitDo(t, repo, "branch", "zvi/old")
		h := failingWorktreeCreate(t, repo)

		if code := h.run("--title", "old", "--worktree", "--project", repo, "--branch", "zvi/old", "--on-failure", "clean"); code != ExitFailed {
			t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitFailed, h.stderr)
		}
		if want := "but not its branch zvi/old"; !strings.Contains(h.stderr.String(), want) {
			t.Errorf("stderr = %q, want %q", h.stderr, want)
		}
	})
}
