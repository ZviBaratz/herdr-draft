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

// FetchPrune runs `git fetch --prune` in repoDir -- spec §6 field 4's
// background "fires once per repo per form-open" refresh of remote-tracking
// branches, ported in spirit from Atrium's own git.FetchBranches
// (app/app_branchsearch.go, on this task's clean list): best-effort, so a
// remoteless or offline repo simply keeps its local view rather than
// failing the caller. Unlike Atrium's FetchBranches (which swallows the
// error itself, returning nothing), FetchPrune returns it so the app layer
// can log it -- see the app package's own doc on why: it wraps the call in
// a tea.Cmd that reports completion regardless of outcome, mirroring
// Atrium's own branchFetchDoneMsg's "completion always re-triggers a
// search" contract, which needs no separate success/failure signal.
func FetchPrune(ctx context.Context, repoDir string) error {
	_, err := runGit(ctx, repoDir, "fetch", "--prune")
	if err != nil {
		return fmt.Errorf("fetch --prune: %w", err)
	}
	return nil
}

// ResolveRef resolves ref to a full commit id in repoDir (`git rev-parse
// --verify <ref>^{commit}`). An unknown ref, or a ref naming something
// that is not a commit, is an error rather than an empty result.
//
// This exists for plan.CleanCheck: the form's base picker reports "" for
// its HEAD row, and passing that straight through to Disposable produced
// `git rev-list --count ..HEAD`, which git reads as HEAD..HEAD and
// therefore counts 0 for every worktree, however many commits it carries.
// Callers resolve the sentinel against the ORIGIN repo first (resolving it
// inside the worktree would be equally useless -- its own HEAD is exactly
// the thing being counted), so the count runs against a commit the
// worktree can actually be ahead of.
func ResolveRef(ctx context.Context, repoDir, ref string) (string, error) {
	if strings.TrimSpace(ref) == "" {
		return "", fmt.Errorf("resolve ref: empty ref in %s", repoDir)
	}
	out, err := runGit(ctx, repoDir, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("resolve ref %q: %w", ref, err)
	}
	if out == "" {
		return "", fmt.Errorf("resolve ref %q: no such commit in %s", ref, repoDir)
	}
	return out, nil
}

// Disposable reports whether the worktree at worktreeDir can be safely
// discarded relative to baseRef: it must have no uncommitted changes and
// no commits that baseRef doesn't already have. When ok is false, reason
// explains which check failed.
//
// baseRef must name something git can resolve. An empty baseRef is
// rejected outright rather than quietly becoming `git rev-list --count
// ..HEAD` -- see ResolveRef's own doc comment for why that shape made this
// function's second check unfailable for every caller holding nothing but
// the form's own "" == HEAD sentinel.
func Disposable(ctx context.Context, worktreeDir, baseRef string) (ok bool, reason string, err error) {
	if strings.TrimSpace(baseRef) == "" {
		return false, "", fmt.Errorf("disposable: empty base ref for worktree %s", worktreeDir)
	}

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
