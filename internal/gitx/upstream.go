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
