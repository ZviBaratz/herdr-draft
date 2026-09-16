package plan

import (
	"path/filepath"
	"strings"

	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
)

// Space names an open herdr workspace a session's pane can be placed into
// -- PlacementTabIn's target (Input.Space). Label is what the placement
// row shows ("tab in <Label>"); it falls back to the id when herdr reports
// no label, so the row can always name where the tab goes.
type Space struct {
	WorkspaceID string
	Label       string
}

// FindSpace answers #128's question for the common case: which open
// workspace already holds the repository the project row names? It is
// pure -- a `herdr workspace list` snapshot in, an answer out -- so the
// form and headless `create` resolve it identically (defaults.Resolve is
// the one caller), and it performs no I/O, so a symlinked path is compared
// as written: callers hand it the paths they have, cleaned here.
//
// Two passes, in order. First, a workspace whose checkout IS projectDir:
// the repository's primary checkout, or a linked worktree's, whichever the
// row points at. Second, when repoRoot is known and no checkout matched,
// the PRIMARY checkout of that repository (is_linked_worktree false) -- the
// parent space every worktree of it is grouped under -- so a project row
// pointing inside a repository still lands in the repository's own space
// rather than a new one. A linked worktree's space is deliberately not a
// fallback for its parent: its checkout is a different branch, and a tab
// whose cwd is the parent's checkout would sit in a space labelled for
// another piece of work.
//
// A workspace with no worktree context (a plain shell) never matches; it
// has no checkout to be equal to anything.
func FindSpace(workspaces []herdrc.WorkspaceInfo, projectDir, repoRoot string) (Space, bool) {
	dir := cleanPath(projectDir)
	if dir == "" {
		return Space{}, false
	}
	for _, w := range workspaces {
		if w.Worktree != nil && cleanPath(w.Worktree.CheckoutPath) == dir {
			return spaceOf(w), true
		}
	}
	root := cleanPath(repoRoot)
	if root == "" {
		return Space{}, false
	}
	for _, w := range workspaces {
		if w.Worktree != nil && !w.Worktree.IsLinkedWorktree && cleanPath(w.Worktree.CheckoutPath) == root {
			return spaceOf(w), true
		}
	}
	return Space{}, false
}

func spaceOf(w herdrc.WorkspaceInfo) Space {
	label := strings.TrimSpace(w.Label)
	if label == "" {
		label = w.WorkspaceID
	}
	return Space{WorkspaceID: w.WorkspaceID, Label: label}
}

// cleanPath normalises a path for equality: "" stays "", everything else
// is filepath.Clean'd, which folds `.`/`..` segments and a trailing slash.
func cleanPath(p string) string {
	if strings.TrimSpace(p) == "" {
		return ""
	}
	return filepath.Clean(p)
}
