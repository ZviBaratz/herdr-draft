package gitx

import (
	"context"
	"fmt"
	"strings"
)

// Upstream returns the full name of the ref branch tracks --
// refs/remotes/origin/develop, or refs/heads/main for a local upstream --
// and "" when it tracks nothing (#221). It reads `%(upstream)` rather than
// resolving `<branch>@{upstream}`, because git answers "no upstream" to that
// with a failure, and a failure is not how "nothing to do" should be learned.
// A branch that does not exist tracks nothing too.
func Upstream(ctx context.Context, repoDir, branch string) (string, error) {
	out, err := runGit(ctx, repoDir, "for-each-ref", "--format=%(upstream)", "refs/heads/"+branch)
	if err != nil {
		return "", fmt.Errorf("upstream of %s: %w", branch, err)
	}
	return out, nil
}

// FullRefName returns the full name of the ref name resolves to in repoDir,
// by the rules git applies to a worktree's start point: origin/develop and
// refs/remotes/origin/develop are both refs/remotes/origin/develop, and so is
// a remote whose own name holds a slash. A commit id names no ref, which is
// "" rather than an error; a name that resolves to nothing is an error.
func FullRefName(ctx context.Context, repoDir, name string) (string, error) {
	// Read as an option by rev-parse, which has no --end-of-options of its
	// own to put before it.
	if name == "" || strings.HasPrefix(name, "-") {
		return "", fmt.Errorf("full ref name: %q is not a ref", name)
	}
	out, err := runGit(ctx, repoDir, "rev-parse", "--verify", "--quiet", "--symbolic-full-name", name)
	if err != nil {
		return "", fmt.Errorf("full ref name of %s: %w", name, err)
	}
	return out, nil
}

// BaseBehind reports how far a worktree base that is a local branch trails
// the remote-tracking branch it tracks: the short name of that upstream
// (origin/main) and the commits it has that the branch does not (#354).
// Anything else is ("", 0, nil), which means "nothing to say" rather than
// "up to date": a remote-tracking ref, a tag or a commit is its own
// answer, a branch that tracks nothing has nothing to trail, and one that
// tracks another local branch (remote ".") is not the stale copy of a
// remote this exists to catch -- the same refs/remotes/ rule plan's
// untrackBase applies to the upstream a new branch was given.
//
// Why a bare name needs asking about at all: git resolves `main` as
// refs/heads/main, so `--base main` cuts from wherever the local branch was
// last left. On a machine that works only in worktrees nobody moves it,
// and every lane cut from it starts behind without a word.
//
// It reads local refs only and fetches nothing, so the count is as of the
// last fetch: it can under-count and cannot over-count. It goes through
// runGit like ResolveRef and CommitsAhead rather than IsGitRepo's fast path,
// because it runs once per create and once per base change in the form --
// nowhere near the hottest calls that path exists for.
func BaseBehind(ctx context.Context, repoDir, ref string) (upstream string, behind int, err error) {
	full, err := FullRefName(ctx, repoDir, ref)
	if err != nil {
		return "", 0, err
	}
	branch, ok := strings.CutPrefix(full, "refs/heads/")
	if !ok {
		return "", 0, nil
	}
	tracked, err := Upstream(ctx, repoDir, branch)
	if err != nil {
		return "", 0, err
	}
	short, ok := strings.CutPrefix(tracked, "refs/remotes/")
	if !ok {
		return "", 0, nil
	}
	// A configured upstream whose ref is gone -- pruned, or never fetched --
	// is "[gone]" to git and nothing to count against here. Asked with
	// show-ref, which tells "no such ref" from a failure, so a git that did
	// not answer is reported rather than read as "gone".
	if exists, err := anyRefExists(ctx, repoDir, tracked); err != nil {
		return "", 0, err
	} else if !exists {
		return "", 0, nil
	}
	behind, err = CommitsAhead(ctx, repoDir, tracked, full)
	if err != nil {
		return "", 0, err
	}
	return short, behind, nil
}

// AutoSetupMerge returns branch.autoSetupMerge as git would read it in
// repoDir, from whichever config file sets it, and "" when none does.
// git compares its value exactly (`strcmp`, git_default_branch_config in
// environment.c at v2.53.0), so it is returned as written.
func AutoSetupMerge(ctx context.Context, repoDir string) (string, error) {
	out, err := runGit(ctx, repoDir, "config", "--default=", "--get", "branch.autoSetupMerge")
	if err != nil {
		return "", fmt.Errorf("branch.autoSetupMerge: %w", err)
	}
	return out, nil
}

// UnsetUpstream removes what branch tracks (`git branch --unset-upstream`).
// git refuses a branch that tracks nothing, so a caller asks Upstream first.
func UnsetUpstream(ctx context.Context, repoDir, branch string) error {
	if _, err := runGit(ctx, repoDir, "branch", "--unset-upstream", "--", branch); err != nil {
		return fmt.Errorf("unset upstream of %s: %w", branch, err)
	}
	return nil
}
