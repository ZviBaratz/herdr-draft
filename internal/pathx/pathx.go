// Package pathx holds the one filesystem-path normalization herdr-draft
// needs at the boundary between what the user typed and what a subprocess
// is handed.
//
// It exists because of an asymmetry in herdr's own CLI: `herdr worktree
// create --cwd` expands a leading "~" server-side
// (herdr:src/app/api/worktrees.rs:28 -> worktree::expand_tilde_path), but
// `herdr workspace create --cwd` and `herdr pane split --cwd` do not --
// both hand the string straight to PathBuf::from
// (herdr:src/app/api/workspaces.rs:44, src/app/api/panes.rs:54). A project
// directory typed as "~/Projects/foo" therefore produced a workspace
// rooted at a directory literally named "~", relative to wherever the
// server happened to be. Every path leaving this plugin for a subprocess
// -- herdr's CLI or git's -- goes through ExpandTilde first, so the plugin
// never depends on which of herdr's own entry points happens to expand.
package pathx

import (
	"os"
	"path/filepath"
	"strings"
)

// ExpandTilde replaces a leading "~" with the current user's home
// directory: "~" alone, and "~/..." (or "~\..." on Windows). Everything
// else is returned unchanged, including the "~user" form -- resolving
// another user's home needs a name-service lookup this plugin has no
// business doing, and leaving it alone is safer than guessing.
//
// A home directory that cannot be determined (os.UserHomeDir failing, e.g.
// no HOME in the environment) also returns path unchanged: the caller's
// own validity check -- DirField's inline "(invalid)" marker, spec §6
// field 2 -- is a better place to tell the user a path does not resolve
// than a silent substitution of the wrong one.
func ExpandTilde(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, `~\`) {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}
