package gitx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// gitTestEnv is the environment every test git command here runs with: the
// process's own, minus every GIT_* variable, plus a fixed identity. The
// strip is the point. GIT_DIR, GIT_WORK_TREE and GIT_INDEX_FILE all beat
// cmd.Dir, and `git rebase --exec` exports GIT_DIR in a linked worktree, so
// inheriting them sent this helper's `git init`/`add`/`commit` into the
// repository running the tests (TestHelpersIgnoreAnInheritedGitDir).
func gitTestEnv(extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			env = append(env, kv)
		}
	}
	env = append(env,
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	return append(env, extra...)
}

func mkRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gitTestEnv()
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

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitTestEnv()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitTestEnv()
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
	cmd.Env = gitTestEnv("GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
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

// cloneOf clones upstream the way a first user's clone is made: origin/HEAD
// set, the default branch local, and every other branch remote-only.
func cloneOf(t *testing.T, upstream string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "clone")
	gitRun(t, upstream, "clone", "-q", upstream, dir)
	return dir
}

// #198: the list stripped origin/ from every remote-tracking name, so a
// branch only origin has was offered by its bare name. That name is no ref
// in the clone, and git handed it as a worktree's start point checks out a
// new local branch named after it instead of the one asked for. The list
// now offers such a branch by the name git resolves as written, and a
// branch with a local copy stays one row, under its local name.
func TestListBranchesOffersARemoteOnlyBranchByANameThatResolves(t *testing.T) {
	upstream := mkRepo(t)
	gitRun(t, upstream, "checkout", "-qb", "develop")
	gitCommitAt(t, upstream, "develop", time.Now().Add(time.Hour))
	gitRun(t, upstream, "checkout", "-q", "main")
	clone := cloneOf(t, upstream)
	ctx := context.Background()

	got, err := ListBranches(ctx, clone, 10)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if want := []string{"origin/develop", "main"}; !slices.Equal(got, want) {
		t.Fatalf("ListBranches in a fresh clone = %q, want %q", got, want)
	}
	for _, name := range got {
		if _, err := ResolveRef(ctx, clone, name); err != nil {
			t.Errorf("ListBranches offered %q, which names nothing in the clone: %v", name, err)
		}
	}

	gitRun(t, clone, "branch", "-q", "develop", "origin/develop")
	got, err = ListBranches(ctx, clone, 10)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if want := []string{"develop", "main"}; !slices.Equal(got, want) {
		t.Fatalf("ListBranches with a local develop = %q, want %q -- one row, under the local name", got, want)
	}
}

// #148: `origin` is %(refname:short) for refs/remotes/origin/HEAD, a
// symref naming the remote's default branch rather than a branch of its
// own. The old check compared the short name with "HEAD", which it never
// is, and its test had no remote, so it could not fail. This one has a
// real origin/HEAD, and a second remote with its HEAD set too.
//
// It is a symref that is dropped, not a name ending in HEAD: a branch
// called release/HEAD is a branch, and git lets anyone make one. git's
// short name for origin's copy of it is origin/release, by the same rule
// that makes `origin` of origin/HEAD, and that name resolves to it.
func TestListBranchesDropsARemoteHEAD(t *testing.T) {
	upstream := mkRepo(t)
	gitRun(t, upstream, "checkout", "-qb", "release/HEAD")
	gitRun(t, upstream, "commit", "-q", "--allow-empty", "-m", "release")
	gitRun(t, upstream, "checkout", "-q", "main")
	clone := cloneOf(t, upstream)
	if got := strings.TrimSpace(gitOut(t, clone, "symbolic-ref", "refs/remotes/origin/HEAD")); got != "refs/remotes/origin/main" {
		t.Fatalf("setup: origin/HEAD is %q, want refs/remotes/origin/main", got)
	}
	gitRun(t, clone, "remote", "add", "fork", upstream)
	gitRun(t, clone, "fetch", "-q", "fork")
	gitRun(t, clone, "remote", "set-head", "fork", "main")
	ctx := context.Background()

	got, err := ListBranches(ctx, clone, 10)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	for _, name := range got {
		if name == "origin" || name == "fork" {
			t.Errorf("ListBranches = %q, which offers %q: a remote's HEAD, not a branch", got, name)
		}
	}
	for _, want := range []string{"main", "origin/release", "fork/main"} {
		if !slices.Contains(got, want) {
			t.Errorf("ListBranches = %q, missing %q", got, want)
		}
	}
	if got, want := revOf(t, clone, "origin/release"), revOf(t, upstream, "release/HEAD"); got != want {
		t.Errorf("origin/release resolves to %s, want release/HEAD's own commit, %s", got, want)
	}
}

func revOf(t *testing.T, dir, rev string) string {
	t.Helper()
	return strings.TrimSpace(gitOut(t, dir, "rev-parse", "--verify", "--quiet", rev))
}

// `git branch -a` lists a detached HEAD as a row of its own, "(HEAD
// detached at <sha>)", and the list offered it as a base, which is no ref
// at all. The HEAD row is already the base that means "where HEAD is".
func TestListBranchesOffersNoDetachedHEADRow(t *testing.T) {
	repo := mkRepo(t)
	gitRun(t, repo, "checkout", "-q", "--detach")

	got, err := ListBranches(context.Background(), repo, 10)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if want := []string{"main"}; !slices.Equal(got, want) {
		t.Fatalf("ListBranches with HEAD detached = %q, want %q", got, want)
	}
}

// TestResolveRefAnswersTheBaseRule holds real git to the two facts #194's
// base rule stands on (app.SettleBase, app.NamesHead), which every other
// test of that rule takes from a fake:
//
//   - A branch that exists only on a remote resolves by its remote-tracking
//     name, origin/develop, which is how the list offers it (#198), and not
//     by its bare name. So a remembered or configured `develop` in a fresh
//     clone falls back to HEAD, while `origin/develop` is kept.
//   - HEAD^0, @~0, HEAD~0, HEAD^{commit}, HEAD@{0} and @{0} are HEAD itself
//     -- the six spellings measured letting #193's clean gate remove work --
//     and HEAD~1 is not.
func TestResolveRefAnswersTheBaseRule(t *testing.T) {
	repo := mkRepo(t)
	gitRun(t, repo, "commit", "-q", "--allow-empty", "-m", "second")
	ctx := context.Background()

	remoteDir := t.TempDir()
	gitRun(t, remoteDir, "init", "-q", "--bare")
	gitRun(t, repo, "remote", "add", "origin", remoteDir)
	gitRun(t, repo, "branch", "develop")
	gitRun(t, repo, "push", "-q", "origin", "main", "develop")
	gitRun(t, repo, "branch", "-q", "-D", "develop")

	branches, err := ListBranches(ctx, repo, 10)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if !slices.Contains(branches, "origin/develop") {
		t.Fatalf("setup: ListBranches = %q, want the remote-only develop listed as origin/develop", branches)
	}
	if got, err := ResolveRef(ctx, repo, "develop"); err == nil {
		t.Errorf("ResolveRef(develop) = %s with only origin/develop present, want an error", got)
	}
	if _, err := ResolveRef(ctx, repo, "origin/develop"); err != nil {
		t.Errorf("ResolveRef(origin/develop): %v", err)
	}

	head, err := ResolveRef(ctx, repo, "HEAD")
	if err != nil {
		t.Fatalf("ResolveRef(HEAD): %v", err)
	}
	for _, ref := range []string{"HEAD^0", "@~0", "HEAD~0", "HEAD^{commit}", "HEAD@{0}", "@{0}"} {
		if got, err := ResolveRef(ctx, repo, ref); err != nil || got != head {
			t.Errorf("ResolveRef(%s) = %s, %v; want HEAD's own %s", ref, got, err, head)
		}
	}
	if got, err := ResolveRef(ctx, repo, "HEAD~1"); err != nil || got == head {
		t.Errorf("ResolveRef(HEAD~1) = %s, %v; want a commit other than HEAD's", got, err)
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

// TestLocalBranchExists pins the one way it differs from BranchExists: a
// branch that only origin has is NOT a local branch. herdr makes a new
// local branch in that case, so a caller asking "did this run make the
// branch?" must not be told it was already there.
func TestLocalBranchExists(t *testing.T) {
	repo := mkRepo(t)
	ctx := context.Background()

	remoteDir := t.TempDir()
	gitRun(t, remoteDir, "init", "-q", "--bare")
	gitRun(t, repo, "remote", "add", "origin", remoteDir)
	gitRun(t, repo, "checkout", "-qb", "remote-only")
	gitRun(t, repo, "push", "-q", "origin", "remote-only")
	gitRun(t, repo, "checkout", "-q", "main")
	gitRun(t, repo, "branch", "-qD", "remote-only")

	for _, tc := range []struct {
		name string
		want bool
	}{
		{"main", true},
		{"remote-only", false},
		{"does-not-exist", false},
	} {
		got, err := LocalBranchExists(ctx, repo, tc.name)
		if err != nil {
			t.Fatalf("LocalBranchExists(%s): %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("LocalBranchExists(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}

	if _, err := LocalBranchExists(ctx, t.TempDir(), "main"); err == nil {
		t.Error("LocalBranchExists in a non-repository returned no error; that is not the same answer as \"no such branch\"")
	}
}

func TestCommitsAhead(t *testing.T) {
	repo := mkRepo(t)
	ctx := context.Background()

	gitRun(t, repo, "branch", "feature")
	if n, err := CommitsAhead(ctx, repo, "refs/heads/feature", "main"); err != nil || n != 0 {
		t.Fatalf("CommitsAhead(feature at main) = %d, %v, want 0", n, err)
	}

	gitRun(t, repo, "checkout", "-q", "feature")
	gitRun(t, repo, "commit", "-qm", "work", "--allow-empty")
	gitRun(t, repo, "checkout", "-q", "main")
	if n, err := CommitsAhead(ctx, repo, "refs/heads/feature", "main"); err != nil || n != 1 {
		t.Fatalf("CommitsAhead(feature one commit past main) = %d, %v, want 1", n, err)
	}

	// Either side empty is `git rev-list --count ..X` or `X..`, which git
	// reads as HEAD: an answer about the wrong commit, never an error.
	if _, err := CommitsAhead(ctx, repo, "refs/heads/feature", ""); err == nil {
		t.Error("CommitsAhead with an empty base returned no error")
	}
	if _, err := CommitsAhead(ctx, repo, "", "main"); err == nil {
		t.Error("CommitsAhead with an empty ref returned no error")
	}
	if _, err := CommitsAhead(ctx, repo, "refs/heads/no-such-branch", "main"); err == nil {
		t.Error("CommitsAhead of a missing ref returned no error")
	}
}

// TestCommitsOnlyOn pins the question a delete actually has to ask: which
// commits would become unreachable. "Ahead of the base" is a proxy for it,
// and the two differ when the base is a commit no ref holds any more.
func TestCommitsOnlyOn(t *testing.T) {
	repo := mkRepo(t)
	ctx := context.Background()

	gitRun(t, repo, "branch", "feature")
	if n, err := CommitsOnlyOn(ctx, repo, "feature"); err != nil || n != 0 {
		t.Fatalf("CommitsOnlyOn(feature at main) = %d, %v, want 0", n, err)
	}

	gitRun(t, repo, "checkout", "-q", "feature")
	gitRun(t, repo, "commit", "-qm", "work", "--allow-empty")
	gitRun(t, repo, "checkout", "-q", "main")
	if n, err := CommitsOnlyOn(ctx, repo, "feature"); err != nil || n != 1 {
		t.Fatalf("CommitsOnlyOn(feature with a commit of its own) = %d, %v, want 1", n, err)
	}

	// The same commit held by another ref is not lost by deleting this one.
	gitRun(t, repo, "branch", "keeper", "feature")
	if n, err := CommitsOnlyOn(ctx, repo, "feature"); err != nil || n != 0 {
		t.Fatalf("CommitsOnlyOn(feature, also held by keeper) = %d, %v, want 0", n, err)
	}

	if _, err := CommitsOnlyOn(ctx, repo, "no-such-branch"); err == nil {
		t.Error("CommitsOnlyOn of a missing branch returned no error")
	}
}

func TestDeleteBranch(t *testing.T) {
	repo := mkRepo(t)
	ctx := context.Background()

	gitRun(t, repo, "branch", "gone")
	want := strings.TrimSpace(gitOut(t, repo, "rev-parse", "refs/heads/gone"))
	tip, err := DeleteBranch(ctx, repo, "gone")
	if err != nil {
		t.Fatalf("DeleteBranch: %v", err)
	}
	if tip != want {
		t.Errorf("DeleteBranch returned tip %q, want %q -- the one-line way back is `git branch gone <tip>`", tip, want)
	}
	if ok, err := LocalBranchExists(ctx, repo, "gone"); err != nil || ok {
		t.Fatalf("after DeleteBranch, LocalBranchExists(gone) = %v, %v, want false", ok, err)
	}

	// Why -D and not -d: a branch cut from main's tip, with no commits of
	// its own, while this checkout sits on an older branch. `git branch -d`
	// calls that "not fully merged" -- it asks about this checkout's HEAD,
	// not about the base the branch came from.
	gitRun(t, repo, "branch", "older")
	gitRun(t, repo, "commit", "-qm", "newer", "--allow-empty")
	gitRun(t, repo, "branch", "cut-from-main")
	gitRun(t, repo, "checkout", "-q", "older")
	if _, err := DeleteBranch(ctx, repo, "cut-from-main"); err != nil {
		t.Fatalf("DeleteBranch refused a branch with no commits beyond main: %v", err)
	}
	gitRun(t, repo, "checkout", "-q", "main")

	// git's own guard is kept on purpose: a branch some checkout still has
	// out is refused, whatever the caller believed.
	linked := filepath.Join(t.TempDir(), "lane")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "lane", linked)
	if _, err := DeleteBranch(ctx, repo, "lane"); err == nil {
		t.Fatal("DeleteBranch deleted a branch checked out in a linked worktree")
	}
	if ok, err := LocalBranchExists(ctx, repo, "lane"); err != nil || !ok {
		t.Fatalf("after a refused DeleteBranch, LocalBranchExists(lane) = %v, %v, want true", ok, err)
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
	base := revParse(t, repo, "main")

	// Pristine worktree at its base: disposable.
	ok, reason, err := Disposable(ctx, repo, base)
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
	ok, reason, err = Disposable(ctx, repo, base)
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
	ok, reason, err = Disposable(ctx, repo, base)
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

// Disposable counts inside the worktree, where a ref that means something
// per checkout names the worktree itself: from its own HEAD, a worktree
// carrying work counts none (#193). No list of the spellings that mean
// something per checkout stays complete (HEAD~1, @{-1}, @{upstream},
// ORIG_HEAD, :/text, refs/worktree/...), so anything but a full commit id is
// refused rather than counted -- a branch name too, though it would count
// right. Every caller resolves its base where the worktree was created from
// first, so a ref arriving here is the caller's bug.
//
// A full id is 64 digits in a SHA-256 repository, and refusing one there
// would refuse every clean in it.
func TestDisposableCountsOnlyFromACommit(t *testing.T) {
	repo := mkRepo(t)
	ctx := context.Background()
	base := revParse(t, repo, "HEAD")
	gitRun(t, repo, "commit", "-q", "--allow-empty", "-m", "real work")

	for _, ref := range []string{"", "HEAD", "@", "HEAD~1", "HEAD@{0}", "main", base[:12]} {
		if ok, reason, err := Disposable(ctx, repo, ref); err == nil || ok {
			t.Errorf("Disposable(%q) = %v, %q, %v, want an error -- it counted from something other than a commit", ref, ok, reason, err)
		}
	}

	ok, reason, err := Disposable(ctx, repo, base)
	if err != nil || ok || !strings.Contains(reason, "1 commit") {
		t.Errorf("Disposable(<base commit>) = %v, %q, %v, want the one commit beyond it counted", ok, reason, err)
	}

	sha256 := t.TempDir()
	gitRun(t, sha256, "init", "-q", "--object-format=sha256", "-b", "main")
	gitRun(t, sha256, "commit", "-q", "--allow-empty", "-m", "init")
	base = revParse(t, sha256, "HEAD")
	if len(base) != 64 {
		t.Fatalf("fixture: HEAD = %q, want a SHA-256 id", base)
	}
	gitRun(t, sha256, "commit", "-q", "--allow-empty", "-m", "real work")
	ok, reason, err = Disposable(ctx, sha256, base)
	if err != nil || ok || !strings.Contains(reason, "1 commit") {
		t.Errorf("Disposable(<SHA-256 base commit>) = %v, %q, %v, want the one commit beyond it counted", ok, reason, err)
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

// TestRepoRoot_LinkedWorktreeSharesTheOriginRoot is the required test for
// spec §10's per-project memory key: a linked worktree and its origin must
// resolve to ONE root, or every worktree of a repository gets its own
// projects.json entry and the feature does nothing for exactly the people
// most likely to use it.
//
// It is also the test that fails if RepoRoot is ever reimplemented on
// `rev-parse --show-toplevel`, which names the CHECKOUT.
func TestRepoRoot_LinkedWorktreeSharesTheOriginRoot(t *testing.T) {
	ctx := context.Background()
	origin := mkRepo(t)

	linked := filepath.Join(t.TempDir(), "feature")
	gitRun(t, origin, "worktree", "add", "-q", "-b", "feature", linked)

	originRoot, err := RepoRoot(ctx, origin)
	if err != nil {
		t.Fatalf("RepoRoot(origin): %v", err)
	}
	linkedRoot, err := RepoRoot(ctx, linked)
	if err != nil {
		t.Fatalf("RepoRoot(linked worktree): %v", err)
	}

	if originRoot != linkedRoot {
		t.Errorf("RepoRoot(origin) = %q but RepoRoot(linked worktree) = %q; want one shared root",
			originRoot, linkedRoot)
	}
	// Sanity: the shared answer really is the origin checkout, not the
	// linked one. Compared through EvalSymlinks because t.TempDir can sit
	// behind a symlink while git reports the resolved path.
	wantOrigin, evalErr := filepath.EvalSymlinks(origin)
	if evalErr != nil {
		t.Fatalf("EvalSymlinks(%s): %v", origin, evalErr)
	}
	gotOrigin, evalErr := filepath.EvalSymlinks(originRoot)
	if evalErr != nil {
		t.Fatalf("EvalSymlinks(%s): %v", originRoot, evalErr)
	}
	if gotOrigin != wantOrigin {
		t.Errorf("RepoRoot = %q, want the origin checkout %q", gotOrigin, wantOrigin)
	}
	if linkedRoot == filepath.Clean(linked) {
		t.Errorf("RepoRoot(linked worktree) = %q, the worktree's own checkout -- "+
			"this is the --show-toplevel answer, not the --git-common-dir one", linkedRoot)
	}
}

// TestPrimaryCheckout is the question #171 turns on: herdr refuses a linked
// worktree as the source of a new one, so from a linked checkout the new
// worktree is created from the repository's primary checkout instead -- and
// that has to BE the primary checkout, or herdr is handed some other
// directory.
//
// The layouts after the first three are the ones where the parent of the
// common git directory (RepoRoot's answer, a fine memory key) is not a
// checkout at all. git itself cannot name the primary checkout of a
// separate-git-dir repository from one of its linked worktrees, and a bare
// repository has none, so both answer "" -- no substitution, and herdr's own
// refusal stands.
func TestPrimaryCheckout(t *testing.T) {
	ctx := context.Background()
	origin := mkRepo(t)
	linked := filepath.Join(t.TempDir(), "feature")
	gitRun(t, origin, "worktree", "add", "-q", "-b", "feature", linked)
	sub := filepath.Join(linked, "internal")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", sub, err)
	}

	// A separate git directory, then a linked worktree of it.
	gitDirs := t.TempDir()
	separate := t.TempDir()
	gitRun(t, separate, "init", "-q", "-b", "main", "--separate-git-dir", filepath.Join(gitDirs, "repo.git"))
	gitRun(t, separate, "commit", "-q", "--allow-empty", "-m", "init")
	separateLane := filepath.Join(t.TempDir(), "lane")
	gitRun(t, separate, "worktree", "add", "-q", "-b", "lane", separateLane)

	// The same, with the git directory kept inside ANOTHER repository's work
	// tree: RepoRoot's answer for the lane is then a directory in that other
	// repository, and a worktree created there would be created in it.
	host := mkRepo(t)
	hosted := t.TempDir()
	if err := os.MkdirAll(filepath.Join(host, "gitdirs"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	gitRun(t, hosted, "init", "-q", "-b", "main", "--separate-git-dir", filepath.Join(host, "gitdirs", "repo.git"))
	gitRun(t, hosted, "commit", "-q", "--allow-empty", "-m", "init")
	hostedLane := filepath.Join(t.TempDir(), "lane")
	gitRun(t, hosted, "worktree", "add", "-q", "-b", "lane", hostedLane)

	// A bare repository with its checkouts beside it, and the `.git` file
	// that layout usually keeps at the top.
	bareRoot := t.TempDir()
	gitRun(t, bareRoot, "init", "-q", "--bare", "-b", "main", ".bare")
	if err := os.WriteFile(filepath.Join(bareRoot, ".git"), []byte("gitdir: ./.bare\n"), 0o644); err != nil {
		t.Fatalf("write .git: %v", err)
	}
	bareMain := filepath.Join(bareRoot, "main")
	gitRun(t, filepath.Join(bareRoot, ".bare"), "worktree", "add", "-q", "--orphan", bareMain)
	gitRun(t, bareMain, "commit", "-q", "--allow-empty", "-m", "init")
	bareLane := filepath.Join(bareRoot, "lane")
	gitRun(t, bareMain, "worktree", "add", "-q", "-b", "lane", bareLane)

	for _, tc := range []struct {
		name string
		dir  string
		want string
	}{
		{"the primary checkout itself", origin, ""},
		{"a linked worktree", linked, origin},
		{"a subdirectory of a linked worktree", sub, origin},
		{"a primary checkout whose git directory lives elsewhere", separate, ""},
		{"a linked worktree of that repository", separateLane, ""},
		{"a linked worktree whose git directory is inside another repository", hostedLane, ""},
		{"a linked worktree of a bare repository", bareLane, ""},
		{"a plain directory", t.TempDir(), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := PrimaryCheckout(ctx, tc.dir)
			if err != nil {
				t.Fatalf("PrimaryCheckout(%s): %v", tc.dir, err)
			}
			if (got == "") != (tc.want == "") || (got != "" && !sameDir(t, got, tc.want)) {
				t.Errorf("PrimaryCheckout(%s) = %q, want %q", tc.dir, got, tc.want)
			}
		})
	}
}

// TestRepoRoot_FromASubdirectory pins that the root is the REPOSITORY's,
// not the directory asked about: the project field can hold any path
// inside the repo and must still key on one entry.
func TestRepoRoot_FromASubdirectory(t *testing.T) {
	ctx := context.Background()
	repo := mkRepo(t)
	sub := filepath.Join(repo, "internal", "app")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", sub, err)
	}

	top, err := RepoRoot(ctx, repo)
	if err != nil {
		t.Fatalf("RepoRoot(repo): %v", err)
	}
	fromSub, err := RepoRoot(ctx, sub)
	if err != nil {
		t.Fatalf("RepoRoot(subdir): %v", err)
	}
	if top != fromSub {
		t.Errorf("RepoRoot(repo) = %q, RepoRoot(subdir) = %q; want one root", top, fromSub)
	}
}

// TestRepoRoot_NonRepositoryIsEmptyNotAnError pins the documented
// degradation: a plain directory has no repository root, and the caller
// keys on its canonical path instead. That is a normal outcome, not a
// failure to report.
func TestRepoRoot_NonRepositoryIsEmptyNotAnError(t *testing.T) {
	root, err := RepoRoot(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("RepoRoot on a non-repository = error %v, want (\"\", nil)", err)
	}
	if root != "" {
		t.Errorf("RepoRoot on a non-repository = %q, want %q", root, "")
	}
}

// TestRepoRootFallback covers the git-older-than-2.31 path directly -- the
// installed git always takes RepoRoot's --path-format branch, so the
// fallback would otherwise ship untested and break silently on the one
// setup that needs it.
func TestRepoRootFallback(t *testing.T) {
	ctx := context.Background()

	t.Run("main checkout", func(t *testing.T) {
		repo := mkRepo(t)
		got, err := repoRootFallback(ctx, repo)
		if err != nil {
			t.Fatalf("repoRootFallback: %v", err)
		}
		want, err := filepath.EvalSymlinks(repo)
		if err != nil {
			t.Fatalf("EvalSymlinks(%s): %v", repo, err)
		}
		gotResolved, err := filepath.EvalSymlinks(got)
		if err != nil {
			t.Fatalf("EvalSymlinks(%s): %v", got, err)
		}
		if gotResolved != want {
			t.Errorf("repoRootFallback = %q, want the checkout %q", gotResolved, want)
		}
	})

	t.Run("non-repository", func(t *testing.T) {
		got, err := repoRootFallback(ctx, t.TempDir())
		if err != nil {
			t.Fatalf("repoRootFallback on a non-repository = error %v, want (\"\", nil)", err)
		}
		if got != "" {
			t.Errorf("repoRootFallback on a non-repository = %q, want %q", got, "")
		}
	})
}

// isolateGitConfig points git's global and system config at /dev/null for
// the duration of a test, so a developer's own config (an insteadOf
// rewrite, a credential helper, a core.sshCommand of their own) cannot
// change the result. runGit builds its environment from the process's, so
// the process environment is where this has to go.
func isolateGitConfig(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	for _, key := range []string{"GIT_SSH_COMMAND", "GIT_SSH", "GIT_ASKPASS", "SSH_ASKPASS"} {
		// t.Setenv registers the restore; os.Unsetenv then makes it
		// genuinely absent rather than present-but-empty, which git
		// treats as a set value for some of these.
		if v, ok := os.LookupEnv(key); ok {
			t.Setenv(key, v)
			os.Unsetenv(key)
		}
	}
}

// sshStub writes an executable that appends its own argv to a log file and
// then fails, and returns its path and that log's. It stands in for ssh so
// a test can see WHICH ssh git chose and WHAT options it was handed,
// without a network or a key anywhere.
func sshStub(t *testing.T, dir string) (stub, log string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	stub = filepath.Join(dir, "ssh-stub")
	log = filepath.Join(dir, "argv.log")
	script := strings.Join([]string{
		"#!/bin/sh",
		`printf '%s\n' "$*" >> ` + shellQuote(log),
		"exit 1",
		"",
	}, "\n")
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatalf("write ssh stub: %v", err)
	}
	return stub, log
}

// sshRepo builds a throwaway repo whose only remote is an ssh URL, so any
// fetch has to pick an ssh command to run. The host is .invalid (RFC 2606)
// and never resolved -- the stub replaces ssh before that could matter.
func sshRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitRun(t, repo, "init", "-q", "-b", "main")
	gitRun(t, repo, "remote", "add", "origin", "ssh://git@example.invalid/x.git")
	return repo
}

// TestFetchPruneUsesTheConfiguredSSHCommand is the regression pin for the
// review's finding 1. git picks its ssh in a three-step order --
// GIT_SSH_COMMAND, then core.sshCommand, then GIT_SSH (connect.c at
// v2.53.0; see effectiveSSHCommand) -- and setting GIT_SSH_COMMAND
// unconditionally outranks and silently discards the other two. A user
// with a per-repo `core.sshCommand = ssh -i ~/.ssh/work_key` would have
// had the popup's fetch fall back to the default key and fail, silently,
// because the fetch is best-effort.
//
// Each case asserts BOTH halves: the user's own command still ran (the
// stub logged something), and it ran non-interactively (BatchMode=yes
// reached it).
func TestFetchPruneUsesTheConfiguredSSHCommand(t *testing.T) {
	check := func(t *testing.T, log string) {
		t.Helper()
		body, err := os.ReadFile(log)
		if err != nil {
			t.Fatalf("the configured ssh command was never run -- git fell back to a plain ssh, discarding the user's own: %v", err)
		}
		if !strings.Contains(string(body), "BatchMode=yes") {
			t.Errorf("the configured ssh command ran without BatchMode=yes; argv log:\n%s", body)
		}
	}

	t.Run("core.sshCommand", func(t *testing.T) {
		isolateGitConfig(t)
		stub, log := sshStub(t, filepath.Join(t.TempDir(), "stubs"))
		repo := sshRepo(t)
		gitRun(t, repo, "config", "core.sshCommand", stub)

		if err := FetchPrune(context.Background(), repo); err == nil {
			t.Fatalf("FetchPrune with a failing ssh stub = nil error, want the stub's failure")
		}
		check(t, log)
	})

	t.Run("GIT_SSH, whose path is not shell-parsed by git and so must be quoted", func(t *testing.T) {
		isolateGitConfig(t)
		// A space in the directory name is the point: git treats GIT_SSH
		// as a bare program path and never splits it, while the
		// GIT_SSH_COMMAND this folds it into IS shell-parsed.
		stub, log := sshStub(t, filepath.Join(t.TempDir(), "ssh stubs"))
		t.Setenv("GIT_SSH", stub)
		repo := sshRepo(t)

		if err := FetchPrune(context.Background(), repo); err == nil {
			t.Fatalf("FetchPrune with a failing ssh stub = nil error, want the stub's failure")
		}
		check(t, log)
	})

	t.Run("core.sshCommand outranks GIT_SSH", func(t *testing.T) {
		isolateGitConfig(t)
		winner, winnerLog := sshStub(t, filepath.Join(t.TempDir(), "winner"))
		loser, loserLog := sshStub(t, filepath.Join(t.TempDir(), "loser"))
		t.Setenv("GIT_SSH", loser)
		repo := sshRepo(t)
		gitRun(t, repo, "config", "core.sshCommand", winner)

		if err := FetchPrune(context.Background(), repo); err == nil {
			t.Fatalf("FetchPrune with a failing ssh stub = nil error, want the stub's failure")
		}
		// Before check, and fatal: were GIT_SSH to run instead, check's own
		// "fell back to a plain ssh" would misdescribe what happened.
		if _, err := os.Stat(loserLog); err == nil {
			t.Fatalf("GIT_SSH ran instead of core.sshCommand; git itself only reads GIT_SSH when core.sshCommand is unset")
		}
		check(t, winnerLog)
	})

	t.Run("GIT_SSH_COMMAND still wins over both", func(t *testing.T) {
		isolateGitConfig(t)
		winner, winnerLog := sshStub(t, filepath.Join(t.TempDir(), "winner"))
		loser, loserLog := sshStub(t, filepath.Join(t.TempDir(), "loser"))
		t.Setenv("GIT_SSH_COMMAND", winner)
		t.Setenv("GIT_SSH", loser)
		repo := sshRepo(t)
		gitRun(t, repo, "config", "core.sshCommand", loser)

		if err := FetchPrune(context.Background(), repo); err == nil {
			t.Fatalf("FetchPrune with a failing ssh stub = nil error, want the stub's failure")
		}
		check(t, winnerLog)
		if _, err := os.Stat(loserLog); err == nil {
			t.Errorf("the lower-precedence ssh command ran; GIT_SSH_COMMAND must win, as it does for git itself")
		}
	})
}

// TestRunGitAppliesTheNonInteractiveEnv pins the one line that actually
// fixes S4 -- runGit assigning the environment. The helper's own unit test
// cannot see that line, so without this the wiring could be deleted with
// the whole suite still green (the review's finding 3, found by deleting
// it).
//
// No pty is needed to tell wired from unwired: with the environment
// applied git reports `terminal prompts disabled`, and without it git
// reaches for /dev/tty and reports something else entirely.
func TestRunGitAppliesTheNonInteractiveEnv(t *testing.T) {
	isolateGitConfig(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	repo := t.TempDir()
	gitRun(t, repo, "init", "-q", "-b", "main")
	gitRun(t, repo, "remote", "add", "origin", srv.URL+"/repo.git")

	err := FetchPrune(context.Background(), repo)
	if err == nil {
		t.Fatalf("FetchPrune against a server demanding credentials = nil error, want a refusal")
	}
	if !strings.Contains(err.Error(), "terminal prompts disabled") {
		t.Errorf("FetchPrune error = %v,\nwant it to carry git's `terminal prompts disabled` -- without that, git went looking for a terminal to ask on, which inside the popup is the pty the form is drawn on", err)
	}
}

// TestNonInteractiveEnv pins what the helper does with a command it has
// already been handed: git must never be able to prompt (it writes
// `Username for '…':` to /dev/tty, which is the popup's own pty, not to
// the stdin exec gave it), that command must end up in batch mode, and
// none of it may cost the caller the rest of its environment -- cmd.Env =
// nil means "inherit", so the helper has to build on the base it is given
// rather than replace it. WHICH command that is, is resolved a step
// earlier and pinned by TestEffectiveSSHCommand.
func TestNonInteractiveEnv(t *testing.T) {
	get := func(env []string, key string) (string, int) {
		var val string
		var n int
		for _, kv := range env {
			if v, ok := strings.CutPrefix(kv, key+"="); ok {
				val, n = v, n+1
			}
		}
		return val, n
	}

	t.Run("prompts disabled and the given ssh put in batch mode", func(t *testing.T) {
		env := nonInteractiveEnv([]string{"PATH=/usr/bin"}, "my-ssh -v")

		if v, n := get(env, "GIT_TERMINAL_PROMPT"); v != "0" || n != 1 {
			t.Errorf("GIT_TERMINAL_PROMPT = %q (%d entries), want exactly one %q", v, n, "0")
		}
		v, n := get(env, "GIT_SSH_COMMAND")
		if n != 1 {
			t.Fatalf("GIT_SSH_COMMAND appears %d times, want exactly one (a duplicate leaves which one wins up to exec)", n)
		}
		if !strings.Contains(v, "my-ssh -v") {
			t.Errorf("GIT_SSH_COMMAND = %q, want the resolved command %q kept", v, "my-ssh -v")
		}
		if !strings.Contains(v, "BatchMode=yes") {
			t.Errorf("GIT_SSH_COMMAND = %q, want BatchMode=yes appended", v)
		}
		if got, _ := get(env, "PATH"); got != "/usr/bin" {
			t.Errorf("PATH = %q, want the base environment's %q to survive", got, "/usr/bin")
		}
	})

	t.Run("no resolved command falls back to plain ssh", func(t *testing.T) {
		env := nonInteractiveEnv([]string{"PATH=/usr/bin"}, "")
		v, _ := get(env, "GIT_SSH_COMMAND")
		if v != "ssh -oBatchMode=yes" {
			t.Errorf("GIT_SSH_COMMAND = %q, want %q", v, "ssh -oBatchMode=yes")
		}
	})

	t.Run("a stale GIT_SSH_COMMAND in the base does not survive alongside", func(t *testing.T) {
		env := nonInteractiveEnv([]string{"GIT_SSH_COMMAND=ignored", "GIT_TERMINAL_PROMPT=1"}, "chosen")
		if v, n := get(env, "GIT_SSH_COMMAND"); n != 1 || !strings.HasPrefix(v, "chosen") {
			t.Errorf("GIT_SSH_COMMAND = %q (%d entries), want exactly one built from the resolved command", v, n)
		}
		if v, n := get(env, "GIT_TERMINAL_PROMPT"); n != 1 || v != "0" {
			t.Errorf("GIT_TERMINAL_PROMPT = %q (%d entries), want exactly one %q", v, n, "0")
		}
	})
}

// TestEffectiveSSHCommand pins git's own three-step order for choosing an
// ssh command. Getting this wrong is not a cosmetic bug: whatever comes
// back here is what gets written into GIT_SSH_COMMAND, which outranks
// everything below it, so a wrong answer does not merely fail to honour a
// user's setting -- it overrides it.
func TestEffectiveSSHCommand(t *testing.T) {
	ctx := context.Background()

	t.Run("GIT_SSH_COMMAND is used as-is", func(t *testing.T) {
		got := effectiveSSHCommand(ctx, []string{"GIT_SSH_COMMAND=ssh -i /k/one"}, "")
		if got != "ssh -i /k/one" {
			t.Errorf("effectiveSSHCommand = %q, want the environment's own value unchanged (git shell-parses it, so it must not be re-quoted)", got)
		}
	})

	t.Run("GIT_SSH_COMMAND outranks core.sshCommand and GIT_SSH", func(t *testing.T) {
		isolateGitConfig(t)
		repo := mkRepo(t)
		gitRun(t, repo, "config", "core.sshCommand", "ssh -i /k/from-config")
		base := append(os.Environ(), "GIT_SSH=/opt/git-ssh", "GIT_SSH_COMMAND=ssh -i /k/from-env")

		if got := effectiveSSHCommand(ctx, base, repo); got != "ssh -i /k/from-env" {
			t.Errorf("effectiveSSHCommand = %q, want GIT_SSH_COMMAND's value", got)
		}
	})

	// The case #130 shipped wrong: GIT_SSH was returned before the config
	// was ever read, so a per-repo core.sshCommand lost to a global
	// GIT_SSH wrapper -- the opposite of what plain git does.
	t.Run("core.sshCommand outranks GIT_SSH", func(t *testing.T) {
		isolateGitConfig(t)
		repo := mkRepo(t)
		gitRun(t, repo, "config", "core.sshCommand", "ssh -i /k/from-config")
		base := append(os.Environ(), "GIT_SSH=/opt/git-ssh")

		if got := effectiveSSHCommand(ctx, base, repo); got != "ssh -i /k/from-config" {
			t.Errorf("effectiveSSHCommand = %q, want the repository's core.sshCommand -- git reads GIT_SSH only when core.sshCommand is unset", got)
		}
	})

	t.Run("GIT_SSH when core.sshCommand is unset, quoted", func(t *testing.T) {
		isolateGitConfig(t)
		// A real repository with no core.sshCommand, not "": the config is
		// now read before GIT_SSH is consulted, and "" would read whatever
		// repository the test happens to run inside.
		repo := mkRepo(t)
		base := append(os.Environ(), "GIT_SSH=/opt/my ssh/bin/ssh")

		if got := effectiveSSHCommand(ctx, base, repo); got != `'/opt/my ssh/bin/ssh'` {
			t.Errorf("effectiveSSHCommand = %q, want the path quoted -- git never splits GIT_SSH, but it does shell-parse the GIT_SSH_COMMAND this becomes", got)
		}
	})

	t.Run("core.sshCommand when neither variable is set", func(t *testing.T) {
		isolateGitConfig(t)
		repo := mkRepo(t)
		gitRun(t, repo, "config", "core.sshCommand", "ssh -i /k/from-config")

		if got := effectiveSSHCommand(ctx, os.Environ(), repo); got != "ssh -i /k/from-config" {
			t.Errorf("effectiveSSHCommand = %q, want the repository's core.sshCommand", got)
		}
	})

	t.Run("plain ssh when nothing is configured", func(t *testing.T) {
		isolateGitConfig(t)
		repo := mkRepo(t)

		if got := effectiveSSHCommand(ctx, os.Environ(), repo); got != "ssh" {
			t.Errorf("effectiveSSHCommand = %q, want %q", got, "ssh")
		}
	})
}

// TestFetchPruneHonoursContext pins runGit's WaitDelay, and it is a
// failing-first test rather than the regression pin it was first written
// as. exec.CommandContext killing git was expected to be enough; measured,
// it is not. Without WaitDelay this does not merely return late, it never
// returns at all (15s, the test's own escape hatch) -- stdout/stderr are
// bytes.Buffers, so exec pipes them and cmd.Wait blocks on its copier
// goroutines until every writer closes, and git-remote-http outlives the
// kill still holding the write end.
//
// The server accepts the connection and never answers, which is the shape
// that hangs: a refused connection fails fast on its own and would pin
// nothing.
//
// What this does NOT pin, because exec cannot do it: killing git does not
// kill git's own children. That helper is orphaned, not reaped -- see
// runGit.
func TestFetchPruneHonoursContext(t *testing.T) {
	// A developer's own global git config must not be able to change this
	// (an insteadOf rewrite, a credential helper, a proxy); runGit builds
	// its environment from the process's, so this is where to disable it.
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)

	blocked := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-blocked
	}))
	defer srv.Close()
	defer close(blocked)

	repo := t.TempDir()
	gitRun(t, repo, "init", "-q", "-b", "main")
	gitRun(t, repo, "remote", "add", "origin", srv.URL+"/repo.git")

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- FetchPrune(ctx, repo) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("FetchPrune against a server that never answers = nil error, want the cancelled context's failure")
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Errorf("FetchPrune returned after %s, want well inside the deadline plus WaitDelay", elapsed)
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("FetchPrune never returned: the context killed git but cmd.Wait is still blocked")
	}
}
