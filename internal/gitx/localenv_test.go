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
	// So the decoy's branch.autoSetupMerge is unset whatever the
	// developer's own global config says.
	isolateGitConfig(t)
	ctx := context.Background()

	repo := mkRepo(t)
	gitRun(t, repo, "branch", "feature")
	gitRun(t, repo, "branch", "--set-upstream-to=main", "feature")
	gitRun(t, repo, "config", "branch.autoSetupMerge", "always")
	linked := filepath.Join(t.TempDir(), "lane")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "lane", linked)

	decoy := mkRepo(t)
	gitRun(t, decoy, "branch", "decoy-only")
	// A commit of its own, so its HEAD cannot equal this repository's: two
	// mkRepo commits made in the same second are byte-identical.
	gitRun(t, decoy, "commit", "-q", "--allow-empty", "-m", "decoy")
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

	// A lane's worktree is created from the repository root, from the
	// lane's commit (#171): answered about the decoy, a primary checkout,
	// the lane would be handed to herdr, and its commit would be the
	// decoy's.
	if primary, err := PrimaryCheckout(ctx, linked); err != nil || !sameDir(t, primary, repo) {
		t.Errorf("PrimaryCheckout(%s) = %q, %v: it answered about the inherited repository", linked, primary, err)
	}
	head, err := ResolveRef(ctx, linked, "HEAD")
	if err != nil {
		t.Fatalf("ResolveRef: %v", err)
	}
	if want := revParse(t, repo, "HEAD"); head != want {
		t.Errorf("ResolveRef(%s, HEAD) = %s, want this repository's %s", linked, head, want)
	}

	// The one that matters most: Disposable is the clean gate before a
	// failed session's worktree is removed. Judging the wrong repository
	// is how a worktree with work in it gets called safe to delete -- or,
	// as here, a clean one gets refused.
	ok, reason, err := Disposable(ctx, repo, revParse(t, repo, "main"))
	if err != nil || !ok {
		t.Errorf("Disposable(%s) = %v, %q, %v: it judged the inherited repository", repo, ok, reason, err)
	}

	// What a new branch tracks (#221): read, compared against its base, and
	// removed, all in this repository. The decoy has no feature branch and
	// no autoSetupMerge, so each call that leaks answers visibly wrong.
	if up, err := Upstream(ctx, repo, "feature"); err != nil || up != "refs/heads/main" {
		t.Errorf("Upstream(feature) = %q, %v: it read the inherited repository's branch", up, err)
	}
	if name, err := FullRefName(ctx, repo, "feature"); err != nil || name != "refs/heads/feature" {
		t.Errorf("FullRefName(feature) = %q, %v: it resolved in the inherited repository", name, err)
	}
	if v, err := AutoSetupMerge(ctx, repo); err != nil || v != "always" {
		t.Errorf("AutoSetupMerge = %q, %v: it read the inherited repository's config", v, err)
	}
	if m, err := UpstreamMerge(ctx, repo, "feature"); err != nil || m != "refs/heads/main" {
		t.Errorf("UpstreamMerge(feature) = %q, %v: it read the inherited repository's config", m, err)
	}
	if err := UnsetUpstream(ctx, repo, "feature"); err != nil {
		t.Errorf("UnsetUpstream(feature): %v -- it looked for the branch in the inherited repository", err)
	}
	if up := strings.TrimSpace(gitOut(t, repo, "for-each-ref", "--format=%(upstream)", "refs/heads/feature")); up != "" {
		t.Errorf("after UnsetUpstream, feature still tracks %q", up)
	}

	// The branch half of the same clean (#173): whether this run made the
	// branch, whether it holds work, and the delete itself. Asked about
	// the decoy, a branch this repository has would be "not there" and
	// the delete would fail -- or, worse, succeed over there.
	if ok, err := LocalBranchExists(ctx, repo, "decoy-only"); err != nil || ok {
		t.Errorf("LocalBranchExists(decoy-only) = %v, %v: it found the inherited repository's branch", ok, err)
	}
	if n, err := CommitsAhead(ctx, repo, "refs/heads/feature", "main"); err != nil || n != 0 {
		t.Errorf("CommitsAhead(feature, main) = %d, %v: it counted in the inherited repository", n, err)
	}
	if n, err := CommitsOnlyOn(ctx, repo, "feature"); err != nil || n != 0 {
		t.Errorf("CommitsOnlyOn(feature) = %d, %v: it counted in the inherited repository", n, err)
	}
	if _, err := DeleteBranch(ctx, repo, "feature"); err != nil {
		t.Errorf("DeleteBranch(feature): %v -- it looked for the branch in the inherited repository", err)
	}
	if ok, err := LocalBranchExists(ctx, repo, "feature"); err != nil || ok {
		t.Errorf("after DeleteBranch, LocalBranchExists(feature) = %v, %v, want false", ok, err)
	}
}

// revParse is `git rev-parse <rev>` in dir, run with the test environment
// rather than through this package, so it cannot share a leak with the
// call it checks.
func revParse(t *testing.T, dir, rev string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", rev)
	cmd.Dir = dir
	cmd.Env = gitTestEnv()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse %s in %s: %v", rev, dir, err)
	}
	return strings.TrimSpace(string(out))
}

// sameDir compares two paths after resolving symlinks, since a temp dir
// can be reached through one (macOS's /var -> /private/var).
func sameDir(t *testing.T, a, b string) bool {
	t.Helper()
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}
