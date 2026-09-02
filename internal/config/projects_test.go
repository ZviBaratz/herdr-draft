package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func pbool(b bool) *bool { return &b }

func TestProjectsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	seen := time.Date(2026, 9, 2, 10, 14, 0, 0, time.UTC)

	p := Projects{}.Touched("/home/zvi/Projects/herdr-draft", ProjectDefaults{
		Kind:      "claude",
		Worktree:  pbool(true),
		Placement: "new-space",
		Base:      "main",
	}, seen)

	if err := SaveProjects(dir, p); err != nil {
		t.Fatalf("SaveProjects: %v", err)
	}

	got, err := LoadProjects(dir)
	if err != nil {
		t.Fatalf("LoadProjects: %v", err)
	}
	entry, ok := got.Get("/home/zvi/Projects/herdr-draft")
	if !ok {
		t.Fatalf("no entry round-tripped: %+v", got)
	}
	if entry.Kind != "claude" || entry.Placement != "new-space" || entry.Base != "main" {
		t.Errorf("entry = %+v, want the recorded kind/placement/base", entry)
	}
	if entry.Worktree == nil || !*entry.Worktree {
		t.Errorf("entry.Worktree = %v, want a recorded true", entry.Worktree)
	}
	if !entry.Seen.Equal(seen) {
		t.Errorf("entry.Seen = %v, want %v", entry.Seen, seen)
	}
}

// TestProjectsOnDiskShape pins the file format spec §10 documents, so a
// change to it is a deliberate act rather than a struct-tag accident: this
// file is read by a future herdr-draft, and by anyone debugging one.
func TestProjectsOnDiskShape(t *testing.T) {
	dir := t.TempDir()
	seen := time.Date(2026, 9, 2, 10, 14, 0, 0, time.UTC)
	p := Projects{}.Touched("/p", ProjectDefaults{
		Kind: "claude", Worktree: pbool(true), Placement: "new-space", Base: "main",
	}, seen)
	if err := SaveProjects(dir, p); err != nil {
		t.Fatalf("SaveProjects: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(dir, "projects.json"))
	if err != nil {
		t.Fatalf("read projects.json: %v", err)
	}
	var raw struct {
		Version int `json:"version"`
		Entries map[string]struct {
			Kind      string `json:"kind"`
			Worktree  *bool  `json:"worktree"`
			Placement string `json:"placement"`
			Base      string `json:"base"`
			Seen      string `json:"seen"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("projects.json is not the documented shape: %v\n%s", err, b)
	}
	if raw.Version != 1 {
		t.Errorf("version = %d, want 1", raw.Version)
	}
	e, ok := raw.Entries["/p"]
	if !ok {
		t.Fatalf("entries has no /p: %s", b)
	}
	if e.Kind != "claude" || e.Placement != "new-space" || e.Base != "main" ||
		e.Worktree == nil || !*e.Worktree || e.Seen != "2026-09-02T10:14:00Z" {
		t.Errorf("entry = %+v, want spec §10's documented shape\n%s", e, b)
	}
}

// TestProjectsEvictsLeastRecentlySeenAtTheCap pins spec §10's "capped at 50
// entries, evicting least-recently-seen".
func TestProjectsEvictsLeastRecentlySeenAtTheCap(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p := Projects{}

	// 50 projects, oldest first: /p00 is the least recently seen.
	for i := range 50 {
		p = p.Touched(fmt.Sprintf("/p%02d", i), ProjectDefaults{Kind: "claude"}, base.Add(time.Duration(i)*time.Hour))
	}
	if len(p.Entries) != 50 {
		t.Fatalf("at the cap there are %d entries, want 50", len(p.Entries))
	}

	// The 51st evicts exactly one: the least recently seen.
	p = p.Touched("/new", ProjectDefaults{Kind: "codex"}, base.Add(100*time.Hour))
	if len(p.Entries) != 50 {
		t.Fatalf("after exceeding the cap there are %d entries, want 50", len(p.Entries))
	}
	if _, ok := p.Get("/p00"); ok {
		t.Error("/p00 (the least recently seen) survived eviction")
	}
	if _, ok := p.Get("/p01"); !ok {
		t.Error("/p01 was evicted, want only the single least-recently-seen entry gone")
	}
	if _, ok := p.Get("/new"); !ok {
		t.Error("the entry just recorded was evicted; it carries the newest Seen and can never be the victim")
	}

	// Re-touching an old entry makes it the most recent, so the NEXT
	// eviction takes someone else: "least recently seen", not "oldest
	// first written".
	p = p.Touched("/p01", ProjectDefaults{Kind: "claude"}, base.Add(200*time.Hour))
	p = p.Touched("/newer", ProjectDefaults{Kind: "codex"}, base.Add(300*time.Hour))
	if _, ok := p.Get("/p01"); !ok {
		t.Error("/p01 was evicted after being re-seen, want it kept as most-recently-seen")
	}
	if _, ok := p.Get("/p02"); ok {
		t.Error("/p02 survived, want it evicted as the new least-recently-seen")
	}
}

// TestProjectsTouchedDoesNotAliasTheCaller pins the copy Touched makes: the
// app layer snapshots state at call time and hands the snapshot to a
// background write, which a shared map would silently undermine.
func TestProjectsTouchedDoesNotAliasTheCaller(t *testing.T) {
	now := time.Now()
	original := Projects{}.Touched("/a", ProjectDefaults{Kind: "claude"}, now)
	next := original.Touched("/b", ProjectDefaults{Kind: "codex"}, now.Add(time.Hour))

	if _, ok := original.Get("/b"); ok {
		t.Error("Touched added /b to the ORIGINAL Projects; the map is shared")
	}
	if _, ok := next.Get("/a"); !ok {
		t.Error("Touched dropped /a from the copy")
	}
}

// TestProjectsTouchedIgnoresAnEmptyKey pins that a project whose identity
// could not be resolved records nothing rather than acquiring an "" entry.
func TestProjectsTouchedIgnoresAnEmptyKey(t *testing.T) {
	p := Projects{}.Touched("", ProjectDefaults{Kind: "claude"}, time.Now())
	if len(p.Entries) != 0 {
		t.Errorf("Touched with an empty key recorded %+v, want nothing", p.Entries)
	}
}

// TestLoadProjectsDiscardsUnusableFiles pins spec §10's "corrupt or missing
// file discarded silently, like every other state file": every one of these
// must yield an empty Projects and a nil error, never a failure the rest of
// the app has to handle.
func TestLoadProjectsDiscardsUnusableFiles(t *testing.T) {
	cases := []struct {
		name    string
		content string // "" means: write no file at all
	}{
		{"missing file", ""},
		{"truncated json", `{"version":1,"entries":{"/p":{"kind":"cla`},
		{"not json at all", "this is not json"},
		{"wrong root type", `["/p"]`},
		{"entries of the wrong type", `{"version":1,"entries":42}`},
		{"a version this binary does not know", `{"version":2,"entries":{"/p":{"kind":"claude"}}}`},
		{"no version at all", `{"entries":{"/p":{"kind":"claude"}}}`},
		{"empty file", " "},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if c.content != "" {
				if err := os.WriteFile(filepath.Join(dir, "projects.json"), []byte(c.content), 0o600); err != nil {
					t.Fatalf("write fixture: %v", err)
				}
			}
			got, err := LoadProjects(dir)
			if err != nil {
				t.Fatalf("LoadProjects returned an error for %s: %v", c.name, err)
			}
			if len(got.Entries) != 0 {
				t.Errorf("LoadProjects kept %+v from an unusable file", got.Entries)
			}
			if _, ok := got.Get("/p"); ok {
				t.Error("an entry survived an unusable file")
			}
		})
	}
}

// TestLoadProjectsSurvivesADiscardedFileBeingRewritten pins the loss
// tolerance end to end: a corrupt file is not just ignored on read, it is
// replaced cleanly on the next save rather than wedging the feature.
func TestLoadProjectsSurvivesADiscardedFileBeingRewritten(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "projects.json"), []byte("{{{"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	loaded, _ := LoadProjects(dir)
	loaded = loaded.Touched("/p", ProjectDefaults{Kind: "claude"}, time.Now())
	if err := SaveProjects(dir, loaded); err != nil {
		t.Fatalf("SaveProjects over a corrupt file: %v", err)
	}

	got, _ := LoadProjects(dir)
	if _, ok := got.Get("/p"); !ok {
		t.Errorf("the rewritten file did not load back: %+v", got)
	}
}

// TestGetOnAnEmptyProjects pins the zero value as usable: a first run
// carries no map at all, and every caller passes Get's ok straight through
// to defaults.Sources.HaveProject.
func TestGetOnAnEmptyProjects(t *testing.T) {
	var p Projects
	if _, ok := p.Get("/p"); ok {
		t.Error("Get on a zero Projects reported an entry")
	}
	if _, ok := p.Get(""); ok {
		t.Error("Get on an empty key reported an entry")
	}
}
