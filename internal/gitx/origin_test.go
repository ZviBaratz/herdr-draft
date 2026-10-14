package gitx

import (
	"context"
	"testing"
)

// TestOriginHead is §6.4's first row: a base left at HEAD becomes origin's
// default branch for a remote session, because the remote's own checkout is
// on whatever branch someone last left it. A clone knows that branch as
// refs/remotes/origin/HEAD; a repository without one answers "".
func TestOriginHead(t *testing.T) {
	ctx := context.Background()
	upstream := mkRepo(t)
	clone := cloneOf(t, upstream)
	if got, err := OriginHead(ctx, clone); err != nil || got != "origin/main" {
		t.Errorf("OriginHead(clone) = %q, %v; want origin/main", got, err)
	}
	if got, err := OriginHead(ctx, upstream); err != nil || got != "" {
		t.Errorf("OriginHead(no origin) = %q, %v; want \"\", nil", got, err)
	}
}

// TestOriginBranchExists: a local branch is carried to a remote session as
// origin's copy of it, which must exist -- as of the laptop's last fetch.
func TestOriginBranchExists(t *testing.T) {
	ctx := context.Background()
	upstream := mkRepo(t)
	gitRun(t, upstream, "branch", "develop")
	clone := cloneOf(t, upstream)
	gitRun(t, clone, "branch", "local-only")
	for name, want := range map[string]bool{"main": true, "develop": true, "local-only": false} {
		if got, err := OriginBranchExists(ctx, clone, name); err != nil || got != want {
			t.Errorf("OriginBranchExists(%q) = %v, %v; want %v", name, got, err, want)
		}
	}
}

// TestOriginURL: what the remote clone's origin is compared against
// (machine spec §7), "" with no origin rather than an error.
func TestOriginURL(t *testing.T) {
	ctx := context.Background()
	repo := mkRepo(t)
	if got, err := OriginURL(ctx, repo); err != nil || got != "" {
		t.Errorf("OriginURL(no origin) = %q, %v; want \"\", nil", got, err)
	}
	gitRun(t, repo, "remote", "add", "origin", "git@github.com:quantivly/hub.git")
	if got, err := OriginURL(ctx, repo); err != nil || got != "git@github.com:quantivly/hub.git" {
		t.Errorf("OriginURL = %q, %v", got, err)
	}
}
