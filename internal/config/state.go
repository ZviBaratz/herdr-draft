package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// recentsFileName and lastUsedFileName are the two on-disk state files
// under $HERDR_PLUGIN_STATE_DIR that together back State (spec §12:
// "recents.json (recent project paths) ... last-used.json (last agent
// kind, placement, worktree toggle)"). linear-cache.json, the third file
// spec §12 lists in that directory, is owned by internal/linear, not this
// package.
const (
	recentsFileName  = "recents.json"
	lastUsedFileName = "last-used.json"
)

// State is herdr-draft's small amount of persisted UI state: recently used
// project paths, and the last agent kind/placement/worktree choice, so the
// form can default to what the user picked last time.
//
// Per spec §12, state is entirely loss-tolerant: a missing or corrupt state
// file is never a hard error to the rest of the app -- LoadState discards
// it silently and returns the zero value instead.
type State struct {
	// Recents is recently used project paths, most-recently-touched first,
	// capped at 20 entries (see TouchRecent).
	Recents []string
	// LastKind is the last agent kind chosen (e.g. "claude", "codex").
	LastKind string
	// LastPlacement is the last placement chosen (e.g. "new-space").
	LastPlacement string
	// LastWorktree is the last worktree toggle chosen. Nil means "never
	// recorded", distinct from an explicit false.
	LastWorktree *bool
}

// lastUsedOnDisk is last-used.json's shape.
type lastUsedOnDisk struct {
	Kind      string `json:"kind"`
	Placement string `json:"placement"`
	Worktree  *bool  `json:"worktree"`
}

// TouchRecent records path as the most recently used project path: it is
// moved to (or inserted at) the front of Recents, any earlier occurrence of
// the same path is removed (dedupe), and the list is capped at 20 entries,
// evicting the oldest as needed.
func (s *State) TouchRecent(path string) {
	recents := make([]string, 0, len(s.Recents)+1)
	recents = append(recents, path)
	for _, p := range s.Recents {
		if p == path {
			continue
		}
		recents = append(recents, p)
	}
	if len(recents) > 20 {
		recents = recents[:20]
	}
	s.Recents = recents
}

// LoadState reads State back from $stateDir/recents.json and
// $stateDir/last-used.json. Each file is loaded independently and loss-
// tolerantly: a missing or corrupt file contributes its zero value (empty
// Recents, or empty LastKind/LastPlacement/nil LastWorktree) rather than
// causing LoadState to return an error. LoadState never returns a non-nil
// error -- the error return exists for API symmetry with Load/SaveState and
// to leave room for a future source of state that can fail.
func LoadState(stateDir string) (State, error) {
	var st State

	if recents, ok := loadRecents(filepath.Join(stateDir, recentsFileName)); ok {
		st.Recents = recents
	}

	if lu, ok := loadLastUsed(filepath.Join(stateDir, lastUsedFileName)); ok {
		st.LastKind = lu.Kind
		st.LastPlacement = lu.Placement
		st.LastWorktree = lu.Worktree
	}

	return st, nil
}

// loadRecents reads and parses path as a JSON array of strings, returning
// (recents, true) only on success. Any failure (missing file, read error,
// parse error) returns (nil, false).
func loadRecents(path string) ([]string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var recents []string
	if err := json.Unmarshal(b, &recents); err != nil {
		return nil, false
	}
	return recents, true
}

// loadLastUsed reads and parses path as lastUsedOnDisk, returning
// (lastUsed, true) only on success. Any failure (missing file, read error,
// parse error) returns (lastUsedOnDisk{}, false).
func loadLastUsed(path string) (lastUsedOnDisk, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return lastUsedOnDisk{}, false
	}
	var lu lastUsedOnDisk
	if err := json.Unmarshal(b, &lu); err != nil {
		return lastUsedOnDisk{}, false
	}
	return lu, true
}

// SaveState writes st to $stateDir/recents.json and
// $stateDir/last-used.json, overwriting whatever was there. Unlike
// LoadState, a write failure here is a real error -- there is no loss-
// tolerant fallback for a save the caller explicitly asked for.
func SaveState(stateDir string, st State) error {
	recents := st.Recents
	if recents == nil {
		recents = []string{}
	}
	b, err := json.Marshal(recents)
	if err != nil {
		return fmt.Errorf("save state: encode recents: %w", err)
	}
	path := filepath.Join(stateDir, recentsFileName)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("save state: write %s: %w", path, err)
	}

	lu := lastUsedOnDisk{
		Kind:      st.LastKind,
		Placement: st.LastPlacement,
		Worktree:  st.LastWorktree,
	}
	b, err = json.Marshal(lu)
	if err != nil {
		return fmt.Errorf("save state: encode last-used: %w", err)
	}
	path = filepath.Join(stateDir, lastUsedFileName)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("save state: write %s: %w", path, err)
	}

	return nil
}
