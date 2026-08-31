package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// IsGitRepo reports whether dir is inside a git working tree. It never
// panics; any failure to invoke git (missing binary, non-repo dir, etc.)
// is treated as "not a git repo".
func IsGitRepo(dir string) bool {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

// runGit runs git with the given args in repoDir and returns trimmed
// stdout. On failure it returns an error wrapped with the command and
// repo directory for context.
func runGit(ctx context.Context, repoDir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s (in %s): %w: %s", strings.Join(args, " "), repoDir, err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// ListBranches returns at most limit branch names in repoDir, newest by
// committer date first. Local and remote-tracking names for the same
// branch are deduped (the "origin/" prefix is stripped), and
// "origin/HEAD" is dropped. A limit of 0 (or negative) yields no results.
func ListBranches(ctx context.Context, repoDir string, limit int) ([]string, error) {
	out, err := runGit(ctx, repoDir, "branch", "-a", "--sort=-committerdate", "--format=%(refname:short)")
	if err != nil {
		return nil, fmt.Errorf("list branches: %w", err)
	}

	var result []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(out, "\n") {
		if len(result) >= limit {
			break
		}
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		name = strings.TrimPrefix(name, "origin/")
		if name == "HEAD" {
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		result = append(result, name)
	}
	return result, nil
}

// BranchExists reports whether name exists as a local branch or as an
// origin remote-tracking branch in repoDir.
func BranchExists(ctx context.Context, repoDir, name string) (bool, error) {
	for _, ref := range []string{"refs/heads/" + name, "refs/remotes/origin/" + name} {
		cmd := exec.CommandContext(ctx, "git", "show-ref", "--verify", "--quiet", ref)
		cmd.Dir = repoDir
		err := cmd.Run()
		if err == nil {
			return true, nil
		}
		// show-ref exits 1 when the ref is not found; that is a normal
		// negative result, not a failure worth reporting. Any other exit
		// code (e.g. 128 for "not a git repository") is a real error.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			continue
		}
		return false, fmt.Errorf("git show-ref --verify --quiet %s (in %s): %w", ref, repoDir, err)
	}
	return false, nil
}

// CurrentBranch returns the name of the branch checked out in repoDir,
// or "" when HEAD is detached. A detached HEAD is not an error.
func CurrentBranch(ctx context.Context, repoDir string) (string, error) {
	out, err := runGit(ctx, repoDir, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil {
		// symbolic-ref exits non-zero (without any other failure) when
		// HEAD is detached; that is expected and not an error condition.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return "", nil
		}
		return "", fmt.Errorf("current branch: %w", err)
	}
	return out, nil
}

// Disposable reports whether the worktree at worktreeDir can be safely
// discarded relative to baseRef: it must have no uncommitted changes and
// no commits that baseRef doesn't already have. When ok is false, reason
// explains which check failed.
func Disposable(ctx context.Context, worktreeDir, baseRef string) (ok bool, reason string, err error) {
	status, err := runGit(ctx, worktreeDir, "status", "--porcelain")
	if err != nil {
		return false, "", fmt.Errorf("check worktree status: %w", err)
	}
	if status != "" {
		return false, "worktree has uncommitted changes", nil
	}

	countOut, err := runGit(ctx, worktreeDir, "rev-list", "--count", baseRef+"..HEAD")
	if err != nil {
		return false, "", fmt.Errorf("count commits ahead of %s: %w", baseRef, err)
	}
	count, convErr := strconv.Atoi(countOut)
	if convErr != nil {
		return false, "", fmt.Errorf("parse rev-list --count output %q: %w", countOut, convErr)
	}
	if count != 0 {
		return false, fmt.Sprintf("worktree has %d commit(s) not on %s", count, baseRef), nil
	}

	return true, "", nil
}
