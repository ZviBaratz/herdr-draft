package defaults

import (
	"testing"

	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
	"github.com/ZviBaratz/herdr-draft/internal/plan"
)

func openSpaces() []herdrc.WorkspaceInfo {
	return []herdrc.WorkspaceInfo{
		{WorkspaceID: "w4", Label: "Quantivly"},
		{WorkspaceID: "wG", Label: "herdr-draft", Worktree: &herdrc.ContextWorktree{RepoRoot: "/p/herdr-draft", CheckoutPath: "/p/herdr-draft"}},
	}
}

// TestResolve_OpenWorkspaceBeatsRememberedNewSpace is the rabota-v2 C10
// default: when the project already has a space, `new space` from
// config.toml, last-used.json AND projects.json all yield to `tab in
// <space>`. Those three tiers can only ever say "new-space" because it was
// the sole sensible answer before this placement existed, so honoring them
// would make the feature inert on exactly the machine it was written for
// (both memory files there said new-space).
func TestResolve_OpenWorkspaceBeatsRememberedNewSpace(t *testing.T) {
	cfg := config.Config{DefaultPlacement: "new-space"}
	r := Resolve(Sources{
		Config:      cfg,
		Global:      config.State{LastPlacement: "new-space"},
		Project:     config.ProjectDefaults{Placement: "new-space"},
		HaveProject: true,
		Workspaces:  openSpaces(),
		ProjectDir:  "/p/herdr-draft",
		RepoRoot:    "/p/herdr-draft",
	})
	if r.Placement != plan.PlacementTabIn {
		t.Fatalf("Placement = %v, want PlacementTabIn over three remembered new-space tiers", r.Placement)
	}
	if r.Space.WorkspaceID != "wG" || r.Space.Label != "herdr-draft" {
		t.Errorf("Space = %+v, want {wG herdr-draft}", r.Space)
	}
	if got := r.From[FieldPlacement]; got != TierOpenWorkspace {
		t.Errorf("From[placement] = %v, want TierOpenWorkspace", got)
	}
	if got := TierOpenWorkspace.String(); got != "herdr workspace list" {
		t.Errorf("TierOpenWorkspace.String() = %q, want the command the value came from", got)
	}
}

// TestResolve_HerePlacementsAreNotOverridden: a remembered or configured
// tab-here / split-here is a real choice about THIS pane and stands.
func TestResolve_HerePlacementsAreNotOverridden(t *testing.T) {
	for _, raw := range []string{"tab-here", "split-here"} {
		r := Resolve(Sources{
			Config:     config.Config{DefaultPlacement: "new-space"},
			Global:     config.State{LastPlacement: raw},
			Workspaces: openSpaces(), ProjectDir: "/p/herdr-draft", RepoRoot: "/p/herdr-draft",
		})
		want, _ := ParsePlacement(raw)
		if r.Placement != want {
			t.Errorf("last-used %q with an open space: Placement = %v, want %v", raw, r.Placement, want)
		}
		if r.Space.WorkspaceID != "wG" {
			t.Errorf("last-used %q: Space = %+v, want the open space still reported so the form can offer it", raw, r.Space)
		}
	}
}

// TestResolve_RepoConfigNewSpaceStillWins: a repository's committed
// `default_placement = "new-space"` is a team statement, not a leftover,
// and it is the one tier the open-workspace rule does not override.
func TestResolve_RepoConfigNewSpaceStillWins(t *testing.T) {
	r := Resolve(Sources{
		Config:     config.Config{DefaultPlacement: "new-space"},
		Repo:       config.RepoConfig{DefaultPlacement: "new-space"},
		Workspaces: openSpaces(), ProjectDir: "/p/herdr-draft", RepoRoot: "/p/herdr-draft",
	})
	if r.Placement != plan.PlacementNewSpace || r.From[FieldPlacement] != TierRepoConfig {
		t.Errorf("Placement = %v from %v, want new-space from .herdr-draft.toml", r.Placement, r.From[FieldPlacement])
	}
}

// TestResolve_RememberedTabInWithNoSpaceFallsBackToNewSpace: a
// projects.json that remembers tab-in for a repository whose space has
// since been closed must not leave the form on a chip that cannot be
// offered. It falls back to new space, attributed to the workspace list
// that decided it.
func TestResolve_RememberedTabInWithNoSpaceFallsBackToNewSpace(t *testing.T) {
	r := Resolve(Sources{
		Config:      config.Config{DefaultPlacement: "new-space"},
		Project:     config.ProjectDefaults{Placement: "tab-in"},
		HaveProject: true,
		Workspaces:  openSpaces(), ProjectDir: "/p/nanoclaw", RepoRoot: "/p/nanoclaw",
	})
	if r.Placement != plan.PlacementNewSpace {
		t.Errorf("Placement = %v, want new-space when the remembered space is gone", r.Placement)
	}
	if r.Space != (plan.Space{}) {
		t.Errorf("Space = %+v, want empty", r.Space)
	}
	if got := r.From[FieldPlacement]; got != TierOpenWorkspace {
		t.Errorf("From[placement] = %v, want TierOpenWorkspace (the list is what decided)", got)
	}
}

// TestResolve_NoWorkspacesIsTheOldBehaviour: a caller that supplies no
// snapshot (or a project with no space) gets exactly the pre-C10 answer.
func TestResolve_NoWorkspacesIsTheOldBehaviour(t *testing.T) {
	r := Resolve(Sources{Config: config.Config{DefaultPlacement: "new-space"}, Global: config.State{LastPlacement: "new-space"}})
	if r.Placement != plan.PlacementNewSpace || r.From[FieldPlacement] != TierGlobalMemory {
		t.Errorf("Placement = %v from %v, want new-space from last-used.json", r.Placement, r.From[FieldPlacement])
	}
}

// TestPlacementVocabularyRoundTripsTabIn: last-used.json and projects.json
// are written with PlacementValue and read with ParsePlacement, so the new
// word has to survive the trip.
func TestPlacementVocabularyRoundTripsTabIn(t *testing.T) {
	if got := PlacementValue(plan.PlacementTabIn); got != "tab-in" {
		t.Errorf("PlacementValue(TabIn) = %q, want %q", got, "tab-in")
	}
	if p, ok := ParsePlacement("tab-in"); !ok || p != plan.PlacementTabIn {
		t.Errorf("ParsePlacement(%q) = %v, %v; want PlacementTabIn, true", "tab-in", p, ok)
	}
}
