package gitx

import (
	"context"
	"fmt"
	"strings"
)

// OriginHead is origin's default branch as this clone knows it, short
// ("origin/main"), from refs/remotes/origin/HEAD -- or "" when the clone
// has no such ref (no origin, or one added without a fetch of its HEAD). It
// is what a base left at HEAD becomes for a session on another machine
// (machine spec §6.4): there, HEAD would be whatever branch the remote
// checkout was last left on.
//
// Through runGit rather than IsGitRepo's fast path: it runs once per
// submit, nowhere near the hottest calls that path exists for.
func OriginHead(ctx context.Context, repoDir string) (string, error) {
	full, err := runGit(ctx, repoDir, "for-each-ref", "--format=%(symref)", "refs/remotes/origin/HEAD")
	if err != nil {
		return "", fmt.Errorf("origin's default branch: %w", err)
	}
	short, ok := strings.CutPrefix(full, "refs/remotes/")
	if !ok {
		return "", nil
	}
	return short, nil
}

// OriginBranchExists reports whether origin/<name> exists in repoDir, as of
// the last fetch: whether a local branch can be carried to another machine
// as origin's copy of it (machine spec §6.4).
func OriginBranchExists(ctx context.Context, repoDir, name string) (bool, error) {
	return anyRefExists(ctx, repoDir, "refs/remotes/origin/"+name)
}

// OriginURL is remote.origin.url as configured in repoDir, "" when unset:
// what another machine's clone of the project is compared against, so a
// session is not cut from a different repository that merely has the same
// path there (machine spec §7).
func OriginURL(ctx context.Context, repoDir string) (string, error) {
	out, err := runGit(ctx, repoDir, "config", "--default=", "--get", "remote.origin.url")
	if err != nil {
		return "", fmt.Errorf("origin url: %w", err)
	}
	return out, nil
}
