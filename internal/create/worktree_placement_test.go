package create

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ZviBaratz/herdr-draft/internal/config"
)

// These pin placement spec §14 on the command side: a worktree session
// runs in the worktree's own space, so `--placement` and `--workspace`
// describe something only a session WITHOUT a worktree can have.

// TestWorktree_RefusesAPlacementItCannotHonour is #145-147's rule ("refuse
// what the form refuses") for the one combination the form no longer
// offers. Each request is refused before herdr is asked to create
// anything, and the remedy is named, whether the worktree came from the
// flag or from a remembered default.
func TestWorktree_RefusesAPlacementItCannotHonour(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		// repoSpace opens the repository's own space first, which is what
		// makes tab-in and --workspace resolvable at all.
		repoSpace bool
		// fromDefault is set where the worktree was not passed on the
		// command line: the refusal must then say where it came from.
		fromDefault bool
	}{
		{name: "tab-here", args: []string{"--title", "t", "--worktree", "--placement", "tab-here"}},
		{name: "split-here", args: []string{"--title", "t", "--worktree", "--placement", "split-here"}},
		{name: "tab-in", args: []string{"--title", "t", "--worktree", "--placement", "tab-in"}, repoSpace: true},
		{name: "--workspace", args: []string{"--title", "t", "--worktree", "--workspace", "wG"}, repoSpace: true},
		// Refused for the worktree first: fixing an id that could never be
		// used anyway would be a wasted round.
		{name: "--workspace with an unknown id", args: []string{"--title", "t", "--worktree", "--workspace", "w9"}},
		// No --worktree: config.Load's built-in default_worktree turns it on.
		{name: "tab-here under the default worktree", args: []string{"--title", "t", "--placement", "tab-here"}, fromDefault: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.env.WorkspaceID, h.env.TabID, h.env.PaneID = "wS9", "tT9", "pP9"
			if tc.repoSpace {
				openRepoSpace(h)
			}

			if code := h.run(tc.args...); code != ExitUsage {
				t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitUsage, h.stderr)
			}
			if !strings.Contains(h.stderr.String(), "--no-worktree") {
				t.Errorf("stderr = %q, want the remedy (--no-worktree) named", h.stderr)
			}
			if said := strings.Contains(h.stderr.String(), "the worktree is on here from config.toml"); said != tc.fromDefault {
				t.Errorf("stderr = %q; naming the worktree's tier = %v, want %v", h.stderr, said, tc.fromDefault)
			}
			if h.createdAnything() {
				t.Errorf("herdr was asked to create something on a usage error: %v", h.runner.calls)
			}
		})
	}
}

// TestWorktree_NewSpaceIsStillAcceptedExplicitly: the one placement a
// worktree session has may still be spelled out.
func TestWorktree_NewSpaceIsStillAcceptedExplicitly(t *testing.T) {
	h := newHarness(t)
	if code := h.run("--title", "t", "--worktree", "--placement", "new-space"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	if !h.runner.called("WorktreeCreate") {
		t.Errorf("calls = %v, want a worktree", h.runner.calls)
	}
}

// TestWorktree_TheRepositorysSpaceDoesNotDrawTheAgentOut is the defect
// itself. With the repository's own space open, #128 used to default the
// agent into a tab there and leave the worktree's space holding an idle
// shell. Now nothing is placed outside the worktree's space: no tab, one
// set of ids, and a provenance that says why the placement is what it is.
func TestWorktree_TheRepositorysSpaceDoesNotDrawTheAgentOut(t *testing.T) {
	h := newHarness(t)
	openRepoSpace(h)

	if code := h.run("--title", "t", "--worktree", "--json"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	if h.runner.called("TabCreate") || h.runner.called("PaneSplit") {
		t.Fatalf("calls = %v, want the agent in the worktree's own space and nothing placed elsewhere", h.runner.calls)
	}
	var out jsonReport
	if err := json.Unmarshal([]byte(h.stdout.String()), &out); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, h.stdout)
	}
	if out.Placement != "new-space" {
		t.Errorf("placement = %q, want new-space", out.Placement)
	}
	if out.Provenance["placement"] != provenanceWorktree {
		t.Errorf("provenance[placement] = %q, want %q", out.Provenance["placement"], provenanceWorktree)
	}
	if out.WorkspaceID == "" || out.WorkspaceID != out.SpaceWorkspaceID || out.PaneID != out.SpacePaneID {
		t.Errorf("agent ids %q/%q vs space ids %q/%q, want one location",
			out.WorkspaceID, out.PaneID, out.SpaceWorkspaceID, out.SpacePaneID)
	}
}

// TestWorktree_ARememberedHerePlacementDoesNotApply: memory written by a
// worktree-off session must not pull a worktree session out of its space,
// and a placement nobody passed on this command line is no reason to
// refuse -- nor to demand the pane environment it would have needed.
func TestWorktree_ARememberedHerePlacementDoesNotApply(t *testing.T) {
	h := newHarness(t)
	writeFile(t, filepath.Join(h.env.StateDir, "last-used.json"),
		`{"kind":"claude","placement":"split-here","worktree":true}`)

	if code := h.run("--title", "t", "--json"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	if h.runner.called("PaneSplit") {
		t.Fatalf("calls = %v, want no split", h.runner.calls)
	}
	var out jsonReport
	if err := json.Unmarshal([]byte(h.stdout.String()), &out); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, h.stdout)
	}
	if out.Placement != "new-space" || out.Provenance["placement"] != provenanceWorktree {
		t.Errorf("placement = %q from %q, want new-space from %q",
			out.Placement, out.Provenance["placement"], provenanceWorktree)
	}
}

// TestWorktree_ASubmitLeavesThePlacementMemoryAlone is the command side of
// placement spec §14.2's memory rule, with the same files the form reads:
// a worktree create records the worktree and keeps the placement.
func TestWorktree_ASubmitLeavesThePlacementMemoryAlone(t *testing.T) {
	h := newHarness(t)
	key := projectMemoryKey("/projects/thing", "/projects/thing")
	writeFile(t, filepath.Join(h.env.StateDir, "last-used.json"),
		`{"kind":"claude","placement":"split-here","worktree":false}`)
	writeFile(t, filepath.Join(h.env.StateDir, "projects.json"),
		`{"version":1,"entries":{"`+key+`":{"kind":"claude","worktree":false,"placement":"tab-here","seen":"2026-09-01T00:00:00Z"}}}`)

	h.env.WorkspaceID, h.env.TabID, h.env.PaneID = "wS9", "tT9", "pP9"
	if code := h.run("--title", "t", "--worktree"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}

	state, _ := config.LoadState(h.env.StateDir)
	if state.LastPlacement != "split-here" {
		t.Errorf("last-used placement = %q, want split-here kept", state.LastPlacement)
	}
	if state.LastWorktree == nil || !*state.LastWorktree {
		t.Errorf("last-used worktree = %v, want a recorded true", state.LastWorktree)
	}
	projects, _ := config.LoadProjects(h.env.StateDir)
	entry, ok := projects.Get(key)
	if !ok {
		t.Fatalf("projects.json has no entry for %q", key)
	}
	if entry.Placement != "tab-here" {
		t.Errorf("project placement = %q, want tab-here kept", entry.Placement)
	}
	if entry.Worktree == nil || !*entry.Worktree {
		t.Errorf("project worktree = %v, want a recorded true", entry.Worktree)
	}
}
