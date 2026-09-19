package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// trackingClone is a clone whose origin has develop, plus a second remote
// whose name holds a slash, the way `git remote add team/x` spells one. Its
// branches' upstreams are set explicitly in each test (--set-upstream-to)
// rather than left to branch.autoSetupMerge, whose default is not this
// package's to rely on across the gits CI runs.
func trackingClone(t *testing.T) string {
	t.Helper()
	upstream := mkRepo(t)
	gitRun(t, upstream, "branch", "develop")
	clone := cloneOf(t, upstream)
	gitRun(t, clone, "remote", "add", "team/x", upstream)
	gitRun(t, clone, "fetch", "-q", "team/x")
	return clone
}

// TestUpstream reads what a branch tracks as git names it in full, which is
// what a base is compared against (#221). No upstream is an empty answer,
// not an error: `git branch --unset-upstream` exits 128 on such a branch,
// and "nothing to do" must not be learned from a failure.
func TestUpstream(t *testing.T) {
	isolateGitConfig(t)
	ctx := context.Background()
	repo := trackingClone(t)

	gitRun(t, repo, "branch", "--no-track", "cut", "origin/develop")
	if got, err := Upstream(ctx, repo, "cut"); err != nil || got != "" {
		t.Fatalf("Upstream(cut) with none set = %q, %v, want \"\", nil", got, err)
	}

	for _, tc := range []struct{ upstream, want string }{
		{"origin/develop", "refs/remotes/origin/develop"},
		{"team/x/develop", "refs/remotes/team/x/develop"},
		// A local upstream (remote "."), which autoSetupMerge=inherit can
		// copy from a local base: it names a branch, not a remote's.
		{"main", "refs/heads/main"},
	} {
		gitRun(t, repo, "branch", "--set-upstream-to="+tc.upstream, "cut")
		if got, err := Upstream(ctx, repo, "cut"); err != nil || got != tc.want {
			t.Errorf("Upstream(cut) tracking %s = %q, %v, want %q", tc.upstream, got, err, tc.want)
		}
	}

	if _, err := Upstream(ctx, t.TempDir(), "cut"); err == nil {
		t.Error("Upstream in a non-repository returned no error; that is not the same answer as \"tracks nothing\"")
	}
}

// TestFullRefName resolves a base the way git resolved it as a start point,
// so every spelling of one remote-tracking branch compares equal to the
// upstream it gave, and a commit -- a lane's base (#171) -- names no ref.
func TestFullRefName(t *testing.T) {
	isolateGitConfig(t)
	ctx := context.Background()
	repo := trackingClone(t)

	for _, tc := range []struct{ name, want string }{
		{"origin/develop", "refs/remotes/origin/develop"},
		{"refs/remotes/origin/develop", "refs/remotes/origin/develop"},
		{"team/x/develop", "refs/remotes/team/x/develop"},
		{"main", "refs/heads/main"},
		{revParse(t, repo, "HEAD"), ""},
	} {
		if got, err := FullRefName(ctx, repo, tc.name); err != nil || got != tc.want {
			t.Errorf("FullRefName(%s) = %q, %v, want %q", tc.name, got, err, tc.want)
		}
	}

	if _, err := FullRefName(ctx, repo, "no-such-ref"); err == nil {
		t.Error("FullRefName of a name that resolves to nothing returned no error")
	}
}

// TestAutoSetupMerge reads the setting as git reads it: the effective value
// in the repository, and "" when nothing sets it.
func TestAutoSetupMerge(t *testing.T) {
	isolateGitConfig(t)
	ctx := context.Background()
	repo := mkRepo(t)

	if got, err := AutoSetupMerge(ctx, repo); err != nil || got != "" {
		t.Fatalf("AutoSetupMerge unset = %q, %v, want \"\", nil", got, err)
	}
	gitRun(t, repo, "config", "branch.autoSetupMerge", "always")
	if got, err := AutoSetupMerge(ctx, repo); err != nil || got != "always" {
		t.Fatalf("AutoSetupMerge = %q, %v, want always", got, err)
	}

	// The user's own global config counts: it is where the setting lives.
	global := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(global, []byte("[branch]\n\tautoSetupMerge = always\n"), 0o644); err != nil {
		t.Fatalf("write global config: %v", err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	other := mkRepo(t)
	if got, err := AutoSetupMerge(ctx, other); err != nil || got != "always" {
		t.Fatalf("AutoSetupMerge from the global config = %q, %v, want always", got, err)
	}
}

// TestUnsetUpstream removes what a branch tracks and nothing else.
func TestUnsetUpstream(t *testing.T) {
	isolateGitConfig(t)
	ctx := context.Background()
	repo := trackingClone(t)

	gitRun(t, repo, "branch", "--no-track", "cut", "origin/develop")
	gitRun(t, repo, "branch", "--set-upstream-to=origin/develop", "cut")
	if err := UnsetUpstream(ctx, repo, "cut"); err != nil {
		t.Fatalf("UnsetUpstream: %v", err)
	}
	if got := revParseQuiet(t, repo, "cut@{upstream}"); got != "" {
		t.Errorf("after UnsetUpstream, cut@{upstream} = %q, want none", got)
	}
	if !localBranch(t, repo, "cut") {
		t.Error("UnsetUpstream removed the branch itself")
	}
}

// revParseQuiet is `git rev-parse --symbolic-full-name <rev>` in dir, "" when
// git cannot resolve it -- run with the test environment, so it cannot share
// a leak with the call it checks.
func revParseQuiet(t *testing.T, dir, rev string) string {
	t.Helper()
	cmd := execGit(dir, "rev-parse", "--symbolic-full-name", rev)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
}

func localBranch(t *testing.T, dir, branch string) bool {
	t.Helper()
	return execGit(dir, "show-ref", "--verify", "--quiet", "refs/heads/"+branch).Run() == nil
}

// execGit is a git command in dir with the test environment.
func execGit(dir string, args ...string) *exec.Cmd {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitTestEnv()
	return cmd
}
