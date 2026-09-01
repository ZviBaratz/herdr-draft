package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func mkRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644)
	run("add", ".")
	run("commit", "-qm", "init")
	return dir
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// gitCommitAt makes an empty commit with an explicit author/committer date
// so committerdate-based ordering is deterministic in tests: commits made
// within the same wall-clock second otherwise get identical committerdate
// values and their relative order is not guaranteed by git.
func gitCommitAt(t *testing.T, dir, msg string, at time.Time) {
	t.Helper()
	date := at.Format(time.RFC3339)
	cmd := exec.Command("git", "commit", "-qm", msg, "--allow-empty")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		"GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit at %s: %v\n%s", date, err, out)
	}
}

func TestIsGitRepo(t *testing.T) {
	repo := mkRepo(t)
	if !IsGitRepo(repo) {
		t.Errorf("expected %s to be recognized as a git repo", repo)
	}

	notRepo := t.TempDir()
	if IsGitRepo(notRepo) {
		t.Errorf("expected %s to not be recognized as a git repo", notRepo)
	}
}

func TestListBranches(t *testing.T) {
	repo := mkRepo(t)
	ctx := context.Background()

	// main only so far.
	branches, err := ListBranches(ctx, repo, 10)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if len(branches) != 1 || branches[0] != "main" {
		t.Fatalf("expected [main], got %v", branches)
	}

	// Create branches with distinct, well-separated committer dates (both
	// after mkRepo's init commit) so ordering is deterministic regardless
	// of how fast the commits run.
	future := time.Now().Add(time.Hour)
	gitRun(t, repo, "checkout", "-qb", "feature-a")
	gitCommitAt(t, repo, "a", future)
	gitRun(t, repo, "checkout", "-qb", "feature-b")
	gitCommitAt(t, repo, "b", future.Add(time.Hour))
	gitRun(t, repo, "checkout", "-q", "main")

	branches, err = ListBranches(ctx, repo, 10)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if len(branches) != 3 {
		t.Fatalf("expected 3 branches, got %v", branches)
	}
	// Newest committed branch first.
	if branches[0] != "feature-b" {
		t.Errorf("expected feature-b first (newest), got %v", branches)
	}
	for _, b := range branches {
		if b == "origin/HEAD" {
			t.Errorf("origin/HEAD should be dropped, got %v", branches)
		}
	}

	// Cap at limit.
	branches, err = ListBranches(ctx, repo, 2)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if len(branches) != 2 {
		t.Fatalf("expected capped to 2, got %v", branches)
	}

	// A limit of 0 yields no results.
	branches, err = ListBranches(ctx, repo, 0)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if len(branches) != 0 {
		t.Fatalf("expected 0 branches for limit=0, got %v", branches)
	}
}

func TestListBranchesDedupesLocalAndRemote(t *testing.T) {
	repo := mkRepo(t)
	ctx := context.Background()

	// Set up a bare "remote" and push main to it as origin, so that
	// `git branch -a` lists both `main` and `remotes/origin/main`.
	remoteDir := t.TempDir()
	gitRun(t, remoteDir, "init", "-q", "--bare")
	gitRun(t, repo, "remote", "add", "origin", remoteDir)
	gitRun(t, repo, "push", "-q", "origin", "main")

	branches, err := ListBranches(ctx, repo, 10)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	count := 0
	for _, b := range branches {
		if b == "main" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected main deduped to a single entry, got %v", branches)
	}
}

func TestIsGitRepoNonRepoDir(t *testing.T) {
	dir := t.TempDir()
	if IsGitRepo(dir) {
		t.Errorf("expected non-repo dir to report false")
	}
}

func TestBranchExistsNonRepoDirReturnsError(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	// A genuine failure (not a git repository at all, exit code 128) must
	// surface as an error, not be conflated with "ref not found" (exit 1).
	_, err := BranchExists(ctx, dir, "main")
	if err == nil {
		t.Errorf("expected an error for a non-repo dir, got nil")
	}
}

func TestBranchExists(t *testing.T) {
	repo := mkRepo(t)
	ctx := context.Background()

	ok, err := BranchExists(ctx, repo, "main")
	if err != nil {
		t.Fatalf("BranchExists: %v", err)
	}
	if !ok {
		t.Errorf("expected main to exist")
	}

	ok, err = BranchExists(ctx, repo, "does-not-exist")
	if err != nil {
		t.Fatalf("BranchExists: %v", err)
	}
	if ok {
		t.Errorf("expected does-not-exist to not exist")
	}
}

func TestBranchExistsRemoteOnly(t *testing.T) {
	repo := mkRepo(t)
	ctx := context.Background()

	remoteDir := t.TempDir()
	gitRun(t, remoteDir, "init", "-q", "--bare")
	gitRun(t, repo, "remote", "add", "origin", remoteDir)
	gitRun(t, repo, "checkout", "-qb", "remote-only")
	gitRun(t, repo, "push", "-q", "origin", "remote-only")
	gitRun(t, repo, "checkout", "-q", "main")
	gitRun(t, repo, "branch", "-qD", "remote-only")

	ok, err := BranchExists(ctx, repo, "remote-only")
	if err != nil {
		t.Fatalf("BranchExists: %v", err)
	}
	if !ok {
		t.Errorf("expected remote-only branch to be found via origin ref")
	}
}

func TestCurrentBranch(t *testing.T) {
	repo := mkRepo(t)
	ctx := context.Background()

	name, err := CurrentBranch(ctx, repo)
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	if name != "main" {
		t.Errorf("expected main, got %q", name)
	}

	// Detached HEAD.
	gitRun(t, repo, "checkout", "-q", "--detach", "HEAD")
	name, err = CurrentBranch(ctx, repo)
	if err != nil {
		t.Fatalf("CurrentBranch on detached HEAD should not error: %v", err)
	}
	if name != "" {
		t.Errorf("expected empty string when detached, got %q", name)
	}
}

func TestDisposable(t *testing.T) {
	repo := mkRepo(t)
	ctx := context.Background()

	// Pristine worktree at base ref: disposable.
	ok, reason, err := Disposable(ctx, repo, "main")
	if err != nil {
		t.Fatalf("Disposable: %v", err)
	}
	if !ok {
		t.Errorf("expected pristine worktree to be disposable, reason: %q", reason)
	}
	if reason != "" {
		t.Errorf("expected empty reason when disposable, got %q", reason)
	}

	// Dirty worktree: not disposable.
	if err := os.WriteFile(filepath.Join(repo, "f"), []byte("changed"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	ok, reason, err = Disposable(ctx, repo, "main")
	if err != nil {
		t.Fatalf("Disposable: %v", err)
	}
	if ok {
		t.Errorf("expected dirty worktree to not be disposable")
	}
	if reason == "" {
		t.Errorf("expected a reason when not disposable")
	}
	gitRun(t, repo, "checkout", "--", "f")

	// Ahead of base: not disposable. Branch off main and add a commit so
	// HEAD has diverged from the base ref.
	gitRun(t, repo, "checkout", "-qb", "feature")
	gitRun(t, repo, "commit", "-qm", "extra", "--allow-empty")
	ok, reason, err = Disposable(ctx, repo, "main")
	if err != nil {
		t.Fatalf("Disposable: %v", err)
	}
	if ok {
		t.Errorf("expected worktree ahead of base to not be disposable")
	}
	if reason == "" {
		t.Errorf("expected a reason when not disposable")
	}
}

// TestFetchPrune pins the happy path (a repo with a remote configured, even
// a local-path one; also a repo with no remote at all -- git itself treats
// "nothing to fetch" as success, not an error) and the "error surfaced not
// swallowed" contract for a remote git genuinely can't reach -- see
// FetchPrune's own doc comment on why it returns the error rather than
// discarding it like Atrium's own FetchBranches does.
func TestFetchPrune(t *testing.T) {
	ctx := context.Background()

	t.Run("no remote configured", func(t *testing.T) {
		repo := mkRepo(t)
		if err := FetchPrune(ctx, repo); err != nil {
			t.Errorf("FetchPrune with no remote configured: %v, want nil (git treats this as a no-op success)", err)
		}
	})

	t.Run("local remote", func(t *testing.T) {
		remote := mkRepo(t)
		repo := t.TempDir()
		gitRun(t, repo, "init", "-q", "-b", "main")
		gitRun(t, repo, "remote", "add", "origin", remote)
		if err := FetchPrune(ctx, repo); err != nil {
			t.Fatalf("FetchPrune with a real remote configured: %v", err)
		}
	})

	t.Run("unreachable remote", func(t *testing.T) {
		repo := t.TempDir()
		gitRun(t, repo, "init", "-q", "-b", "main")
		gitRun(t, repo, "remote", "add", "origin", filepath.Join(t.TempDir(), "does-not-exist"))
		if err := FetchPrune(ctx, repo); err == nil {
			t.Errorf("FetchPrune with an unreachable remote = nil error, want a wrapped git error")
		}
	})
}
