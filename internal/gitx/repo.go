package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
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

// RepoRoot returns the root of the repository containing dir -- the ORIGIN
// repository's root, not a linked worktree's own checkout, so every
// worktree of one repository shares a single per-project memory entry
// (spec §10).
//
// The distinction is the whole point of this function. `rev-parse
// --show-toplevel` names the CHECKOUT: inside ~/repo/.worktrees/feature it
// answers ~/repo/.worktrees/feature, which would give every worktree of one
// repository its own projects.json entry and defeat the feature for exactly
// the users most likely to want it. `--git-common-dir` instead names the
// SHARED .git directory (~/repo/.git from either place), whose parent is
// the origin root.
//
// --git-common-dir has been able to return a relative path ("." inside a
// main worktree's .git, or a relative path from the cwd) for most of its
// life; `--path-format=absolute`, added in git 2.31, is what makes the
// answer usable without knowing what it was relative to. On an older git
// that flag is an error, so this falls back to --show-toplevel: the wrong
// answer for a linked worktree, but a correct and stable one for the main
// checkout, which is better than no memory at all.
//
// A dir that is not a git repository returns ("", nil) -- the caller keys
// on the canonical path instead (pathx.CanonicalKey), which is not a
// failure. A real failure (git missing, an unreadable repository) returns
// an error.
//
// Known limitation, deliberately not handled: a repository whose git
// directory lives outside the working tree (`git init --separate-git-dir`,
// or a GIT_DIR pointing elsewhere) has no "parent of the common dir" that
// names its checkout. Such a repository gets a key derived from the git
// directory's parent instead -- still stable, still shared between that
// repository's worktrees, just not equal to the checkout path.
func RepoRoot(ctx context.Context, dir string) (string, error) {
	if out, err := runGit(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir"); err == nil && out != "" {
		return filepath.Dir(filepath.Clean(out)), nil
	}
	// Either git predates --path-format (< 2.31) or dir is not a
	// repository at all -- repoRootFallback separates the two.
	return repoRootFallback(ctx, dir)
}

// repoRootFallback is RepoRoot's git-older-than-2.31 path, split out so it
// is directly testable: no supported way exists to make a modern git reject
// --path-format, and an untested fallback is a fallback that will be broken
// the day someone needs it.
//
// --show-toplevel is in every git this plugin could run against, and fails
// only for a non-repository -- which is how a missing --path-format is told
// apart from "dir is not a repo". Its answer is the CHECKOUT, so on an old
// git a linked worktree keys separately from its origin; that is the known
// cost of the fallback, and better than no memory at all.
func repoRootFallback(ctx context.Context, dir string) (string, error) {
	top, err := runGit(ctx, dir, "rev-parse", "--show-toplevel")
	if err == nil {
		return filepath.Clean(top), nil
	}
	if !IsGitRepo(dir) {
		return "", nil
	}
	return "", fmt.Errorf("repo root: %w", err)
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
