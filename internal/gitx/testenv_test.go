package gitx

import (
	"os"
	"path/filepath"
	"testing"
)

// TestHelpersIgnoreAnInheritedGitDir pins the one thing the git helpers in
// this file's package must never do: act on a repository other than the
// temp one they were handed. A GIT_DIR in the environment redirects every
// git command away from cmd.Dir, and `git rebase --exec` exports one in a
// linked worktree -- running this suite that way once committed `init`
// into the worktree running it and set the shared repository's core.bare,
// because `git init` guesses "bare" for a GIT_DIR not ending in /.git.
func TestHelpersIgnoreAnInheritedGitDir(t *testing.T) {
	decoy := filepath.Join(t.TempDir(), "decoy.git")
	t.Setenv("GIT_DIR", decoy)
	t.Setenv("GIT_WORK_TREE", t.TempDir())

	repo := mkRepo(t)
	gitRun(t, repo, "branch", "side")

	if _, err := os.Stat(decoy); !os.IsNotExist(err) {
		t.Fatalf("a test helper acted on the inherited GIT_DIR %s (stat: %v)", decoy, err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		t.Fatalf("mkRepo did not make its own repository at %s: %v", repo, err)
	}
}
