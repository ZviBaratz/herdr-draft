// Package pathx holds the filesystem-path work herdr-draft does at the
// boundary between what the user typed and what a subprocess -- or the
// Project field's own browse listing -- acts on.
//
// ListSubdirs and Resolve PORT github.com/ZviBaratz/atrium's
// ui/overlay/directoryPicker.go (listSubdirs and expandPath; (c) Zvi
// Baratz, relicensed by the author), which is on this project's audited
// clean-file list. They live here rather than in internal/form because
// that package performs no I/O of its own: DirField renders whatever
// candidates the app layer supplies, and the app layer browses the
// filesystem through this package (see field_dir.go's own file doc).
//
// ExpandTilde exists because of an asymmetry in herdr's own CLI: `herdr
// worktree create --cwd` expands a leading "~" server-side
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
	"io/fs"
	"os"
	"path/filepath"
	"sort"
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

// readChunk is how many directory entries ListSubdirs pulls per ReadDir
// call. It only bounds allocation per iteration, not the total scan.
const readChunk = 512

// maxEntriesScanned is ListSubdirs' own defense against a pathological
// directory (a build cache with a million files): once this many entries
// have been examined, the walk stops with whatever subdirectories it
// found. The literal-path fallback row keeps an unlisted target reachable
// by typing it out in full.
const maxEntriesScanned = 20000

// Resolve turns what the user typed into the absolute path the filesystem
// -- and, on submit, herdr's own server -- will act on: a leading "~"
// expanded per ExpandTilde, then made absolute against this process's
// working directory. Ported from atrium's expandPath.
//
// Making relative input absolute HERE, at browse time, is what keeps a
// "./foo" selection from reaching `herdr workspace create --cwd`, which
// would resolve it against the SERVER's working directory rather than the
// user's. A path that cannot be made absolute (filepath.Abs failing, e.g.
// an unreadable working directory) is returned tilde-expanded but
// otherwise unchanged, for ExpandTilde's own stated reason: the caller's
// validity check is a better place to say a path does not resolve than a
// silent substitution of the wrong one.
//
// Resolve("") returns the working directory, inherited from filepath.Abs.
// Path mode never asks it for one (form.LooksLikePath requires a leading "/",
// "~" or ".").
func Resolve(path string) string {
	expanded := ExpandTilde(path)
	if abs, err := filepath.Abs(expanded); err == nil {
		return abs
	}
	return expanded
}

// ListSubdirs returns the absolute paths of dir's immediate
// subdirectories, sorted by name, at most limit of them. dir is used as
// given -- callers pass a Resolve'd path.
//
// Any error (missing, permission denied, not a directory) yields no
// entries rather than a reported failure: the Project field's own
// literal-path fallback row is the answer to "nothing here matches", and
// DirField's inline (invalid) marker -- driven separately by the
// directory-validity source -- is where the user learns a path does not
// resolve. A partially read directory keeps what it read.
//
// Two deliberate deviations from atrium's listSubdirs:
//
//   - atrium bounds the READ (`f.ReadDir(maxDirEntries)`), so a directory
//     whose first 500 entries are files reports no subdirectories at all.
//     This bounds the RESULT instead, reading on until limit
//     subdirectories are found (or maxEntriesScanned entries have been
//     examined).
//   - atrium tests entries with DirEntry.IsDir alone, which is false for a
//     symlink pointing AT a directory -- a symlinked project root
//     (~/work -> /mnt/data/work) was therefore invisible. Symlinks, and
//     only symlinks, pay one os.Stat here to find out. A broken or
//     circular link fails that Stat and is skipped, which is the intended
//     outcome either way.
func ListSubdirs(dir string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	f, err := os.Open(dir)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }() // read-only handle; a close error is not actionable

	names := make([]string, 0, limit)
	scanned := 0
	for len(names) < limit && scanned < maxEntriesScanned {
		// ReadDir(n) returns up to n entries, reporting io.EOF once the
		// directory is exhausted; a short read without an error simply
		// means "no more right now", which for a directory means done.
		entries, readErr := f.ReadDir(readChunk)
		for _, e := range entries {
			scanned++
			if !isDir(dir, e) {
				continue
			}
			names = append(names, e.Name())
			if len(names) == limit {
				break
			}
		}
		if readErr != nil || len(entries) == 0 {
			break
		}
	}

	sort.Strings(names)
	paths := make([]string, len(names))
	for i, n := range names {
		paths[i] = filepath.Join(dir, n)
	}
	return paths
}

// isDir reports whether e names a directory, following a symlink (and
// only a symlink) with one os.Stat -- see ListSubdirs' own doc comment.
func isDir(dir string, e fs.DirEntry) bool {
	if e.IsDir() {
		return true
	}
	if e.Type()&fs.ModeSymlink == 0 {
		return false
	}
	info, err := os.Stat(filepath.Join(dir, e.Name()))
	return err == nil && info.IsDir()
}
