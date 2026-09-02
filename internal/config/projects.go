package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// projectsFileName is spec §10's per-project memory file, alongside
// recents.json and last-used.json under $HERDR_PLUGIN_STATE_DIR.
const projectsFileName = "projects.json"

// projectsSchemaVersion is the only `version` LoadProjects accepts. A file
// carrying anything else -- a newer binary's format, or a 0 from a file
// that never had the key -- is discarded like any other unreadable state
// file rather than guessed at: reading a shape this binary does not know
// would silently apply the wrong defaults, which is worse than starting
// from the tiers below.
const projectsSchemaVersion = 1

// maxProjectEntries is spec §10's cap. Beyond it, the least recently SEEN
// entry is evicted -- seen, not written: a project you keep opening keeps
// its memory even if you have not created a session in it lately.
const maxProjectEntries = 50

// ProjectDefaults is one projects.json entry: what the user last chose in
// one project, and when that project was last recorded.
//
// Worktree is a pointer for the same reason State.LastWorktree is: nil
// ("the file omits the key") has to stay distinct from a recorded false,
// or a hand-written entry would silently force the toggle off.
type ProjectDefaults struct {
	Kind      string    `json:"kind,omitempty"`
	Worktree  *bool     `json:"worktree,omitempty"`
	Placement string    `json:"placement,omitempty"`
	Base      string    `json:"base,omitempty"`
	Seen      time.Time `json:"seen"`
}

// Projects is projects.json: per-project creation defaults, keyed by the
// git repository root when the project is a repo -- so a linked worktree
// and its origin share one memory (gitx.RepoRoot) -- and by the canonical
// absolute path otherwise (pathx.CanonicalKey).
//
// Like every other state file in this package it is loss-tolerant: a
// missing, unreadable, malformed or unknown-version file simply means "no
// per-project memory", never an error the rest of the app has to handle.
type Projects struct {
	Version int                        `json:"version"`
	Entries map[string]ProjectDefaults `json:"entries"`
}

// Get returns the entry for key. ok is false for an empty key, for a
// Projects that loaded nothing, and for a project with no entry yet --
// callers pass ok straight through to defaults.Sources.HaveProject, which
// is what keeps "no entry" distinct from "an entry whose fields are zero".
func (p Projects) Get(key string) (ProjectDefaults, bool) {
	if key == "" || p.Entries == nil {
		return ProjectDefaults{}, false
	}
	d, ok := p.Entries[key]
	return d, ok
}

// Touched returns a copy of p with d recorded for key, stamped seen at
// now, and the cap enforced. It copies rather than mutating in place
// because Projects carries a map: the caller's own value would otherwise
// change under it through the shared map, which is exactly the kind of
// aliasing surprise a "snapshot the state at call time" discipline exists
// to avoid (see app.persistStateCmd).
//
// An empty key records nothing: a project directory whose identity could
// not be resolved has nowhere to be remembered.
func (p Projects) Touched(key string, d ProjectDefaults, now time.Time) Projects {
	if key == "" {
		return p
	}
	next := Projects{Version: projectsSchemaVersion, Entries: make(map[string]ProjectDefaults, len(p.Entries)+1)}
	for k, v := range p.Entries {
		next.Entries[k] = v
	}
	d.Seen = now.UTC()
	next.Entries[key] = d
	next.evict()
	return next
}

// evict drops least-recently-seen entries until the cap holds. Ties on
// Seen (two entries recorded within the same instant, which the tests do
// deliberately) are broken by key, so eviction is deterministic rather
// than dependent on Go's randomized map iteration order.
//
// The entry just recorded always carries the newest Seen, so it can never
// be the one evicted.
func (p *Projects) evict() {
	if len(p.Entries) <= maxProjectEntries {
		return
	}
	keys := make([]string, 0, len(p.Entries))
	for k := range p.Entries {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := p.Entries[keys[i]].Seen, p.Entries[keys[j]].Seen
		if a.Equal(b) {
			return keys[i] < keys[j]
		}
		return a.Before(b)
	})
	for _, k := range keys[:len(keys)-maxProjectEntries] {
		delete(p.Entries, k)
	}
}

// LoadProjects reads $stateDir/projects.json. Any failure -- a missing
// file, a read error, malformed JSON, or a `version` this binary does not
// know -- yields an empty Projects rather than an error, matching
// LoadState's own loss-tolerance (spec §12). The error return exists for
// symmetry with the rest of this package's loaders and is always nil.
func LoadProjects(stateDir string) (Projects, error) {
	b, err := os.ReadFile(filepath.Join(stateDir, projectsFileName))
	if err != nil {
		return Projects{}, nil
	}
	var p Projects
	if err := json.Unmarshal(b, &p); err != nil {
		return Projects{}, nil
	}
	if p.Version != projectsSchemaVersion {
		return Projects{}, nil
	}
	if p.Entries == nil {
		p.Entries = map[string]ProjectDefaults{}
	}
	return p, nil
}

// SaveProjects writes p to $stateDir/projects.json, stamping the schema
// version this binary writes. Atomic (temp file plus rename, see
// writeFileAtomic) for the same reason SaveState is: LoadProjects would
// discard a half-written file silently, so the user would lose their whole
// per-project memory to a crash mid-write with nothing anywhere to explain
// it.
//
// Unlike LoadProjects, a write failure is a real error -- there is no
// loss-tolerant fallback for a save the caller explicitly asked for.
func SaveProjects(stateDir string, p Projects) error {
	p.Version = projectsSchemaVersion
	if p.Entries == nil {
		p.Entries = map[string]ProjectDefaults{}
	}
	b, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("save projects: encode: %w", err)
	}
	if err := writeFileAtomic(filepath.Join(stateDir, projectsFileName), b); err != nil {
		return fmt.Errorf("save projects: %w", err)
	}
	return nil
}
