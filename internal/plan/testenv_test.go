package plan

import (
	"os"
	"path/filepath"
	"testing"
)

// TestGitHelpersIgnoreAnInheritedGitDir is internal/gitx's test of the same
// name, for this package's own copies of the helpers: a GIT_DIR in the
// environment (`git rebase --exec` exports one in a linked worktree) must
// not redirect them from the temp repository they were handed.
func TestGitHelpersIgnoreAnInheritedGitDir(t *testing.T) {
	decoy := filepath.Join(t.TempDir(), "decoy.git")
	t.Setenv("GIT_DIR", decoy)
	t.Setenv("GIT_WORK_TREE", t.TempDir())

	repo := mkRepo(t)
	gitIn(t, repo, "branch", "side")

	if _, err := os.Stat(decoy); !os.IsNotExist(err) {
		t.Fatalf("a test helper acted on the inherited GIT_DIR %s (stat: %v)", decoy, err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		t.Fatalf("mkRepo did not make its own repository at %s: %v", repo, err)
	}
}
