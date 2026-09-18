package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestLocalRepoEnvVarsMatchGit holds localRepoEnvVars to the installed
// git's own answer. The list is hard-coded so production spends no extra
// process on it, which makes it a copy -- and a copy needs something that
// fails when the original moves. A git that adds a variable to the list
// fails here, which is the moment to add it.
func TestLocalRepoEnvVarsMatchGit(t *testing.T) {
	cmd := exec.Command("git", "rev-parse", "--local-env-vars")
	cmd.Dir = t.TempDir()
	cmd.Env = gitTestEnv()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse --local-env-vars: %v", err)
	}
	got := strings.Fields(string(out))
	want := slices.Clone(localRepoEnvVars)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("localRepoEnvVars = %v\ninstalled git says %v", want, got)
	}
}

// TestProductionCallsIgnoreAnInheritedGitDir: every call here names the
// directory it means, and an inherited GIT_DIR / GIT_WORK_TREE -- which a
// git hook exports, and `git rebase --exec` in a linked worktree -- must
// not make it answer about another repository instead.
//
// The decoy is built to disagree with the target on every question asked:
// it is dirty where the target is clean, it has a branch the target does
// not, and it is a repository where the third directory is not. Each call
// that follows the inherited variables therefore gets a visibly wrong
// answer, which is the discriminating shape: a decoy that agreed with the
// target would let a leaking call pass.
func TestProductionCallsIgnoreAnInheritedGitDir(t *testing.T) {
	ctx := context.Background()

	repo := mkRepo(t)
	gitRun(t, repo, "branch", "feature")

	decoy := mkRepo(t)
	gitRun(t, decoy, "branch", "decoy-only")
	if err := os.WriteFile(filepath.Join(decoy, "dirty"), []byte("x"), 0o644); err != nil {
		t.Fatalf("dirty the decoy: %v", err)
	}
	notRepo := t.TempDir()

	t.Setenv("GIT_DIR", filepath.Join(decoy, ".git"))
	t.Setenv("GIT_WORK_TREE", decoy)

	if IsGitRepo(notRepo) {
		t.Errorf("IsGitRepo(%s) = true: it answered about the inherited GIT_DIR", notRepo)
	}
	if !IsGitRepo(repo) {
		t.Errorf("IsGitRepo(%s) = false for a real repository", repo)
	}

	root, err := RepoRoot(ctx, repo)
	if err != nil {
		t.Fatalf("RepoRoot: %v", err)
	}
	if !sameDir(t, root, repo) {
		t.Errorf("RepoRoot(%s) = %s: the inherited GIT_DIR's repository, not this one", repo, root)
	}

	if ok, err := BranchExists(ctx, repo, "decoy-only"); err != nil || ok {
		t.Errorf("BranchExists(decoy-only) = %v, %v: it found the inherited repository's branch", ok, err)
	}
	if ok, err := BranchExists(ctx, repo, "feature"); err != nil || !ok {
		t.Errorf("BranchExists(feature) = %v, %v: it missed this repository's own branch", ok, err)
	}

	branches, err := ListBranches(ctx, repo, 10)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if slices.Contains(branches, "decoy-only") || !slices.Contains(branches, "feature") {
		t.Errorf("ListBranches = %v: not this repository's branches", branches)
	}

	// The one that matters most: Disposable is the clean gate before a
	// failed session's worktree is removed. Judging the wrong repository
	// is how a worktree with work in it gets called safe to delete -- or,
	// as here, a clean one gets refused.
	ok, reason, err := Disposable(ctx, repo, "main")
	if err != nil || !ok {
		t.Errorf("Disposable(%s) = %v, %q, %v: it judged the inherited repository", repo, ok, reason, err)
	}
}

// sameDir compares two paths after resolving symlinks, since a temp dir
// can be reached through one (macOS's /var -> /private/var).
func sameDir(t *testing.T, a, b string) bool {
	t.Helper()
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}
