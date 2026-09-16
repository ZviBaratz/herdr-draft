package plan

import (
	"testing"

	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
)

// snapshot is a `herdr workspace list` as it looked on the machine this
// feature was written for (2026-09-16): a plain shell workspace with no
// repository context, three repositories' primary checkouts, and linked
// worktrees grouped under two of them. Paths are the real shape herdr
// reports -- absolute, no trailing slash.
func snapshot() []herdrc.WorkspaceInfo {
	wt := func(checkout, root string, linked bool) *herdrc.ContextWorktree {
		return &herdrc.ContextWorktree{RepoKey: root + "/.git", RepoName: "x", RepoRoot: root, CheckoutPath: checkout, IsLinkedWorktree: linked}
	}
	return []herdrc.WorkspaceInfo{
		{WorkspaceID: "w4", Label: "Quantivly", PaneCount: 7},
		{WorkspaceID: "wX", Label: "Toysim", PaneCount: 1, Worktree: wt("/home/zvi/Projects/toysim", "/home/zvi/Projects/toysim", false)},
		{WorkspaceID: "wG", Label: "herdr-draft", PaneCount: 2, Worktree: wt("/home/zvi/Projects/herdr-draft", "/home/zvi/Projects/herdr-draft", false)},
		{WorkspaceID: "w62", Label: "v0.1.0 orchestrator", PaneCount: 1, Worktree: wt("/home/zvi/.herdr/worktrees/herdr-draft/zvi-v0-1-0-orchestrator", "/home/zvi/Projects/herdr-draft", true)},
		{WorkspaceID: "w5R", Label: "meeting follow-ups", PaneCount: 2, Worktree: wt("/home/zvi/.herdr/worktrees/toysim/orch-meeting-followups", "/home/zvi/Projects/toysim", true)},
	}
}

// TestFindSpace_PrimaryCheckoutIsTheRepoSpace is #128's motivating case:
// the project row names a repository whose primary checkout already has a
// workspace, so a new session belongs in THAT space as a tab, not in a new
// top-level space beside it.
func TestFindSpace_PrimaryCheckoutIsTheRepoSpace(t *testing.T) {
	sp, ok := FindSpace(snapshot(), "/home/zvi/Projects/herdr-draft", "/home/zvi/Projects/herdr-draft")
	if !ok {
		t.Fatal("FindSpace = not found, want wG (the primary checkout's own workspace)")
	}
	if sp.WorkspaceID != "wG" || sp.Label != "herdr-draft" {
		t.Errorf("FindSpace = %+v, want {wG herdr-draft}", sp)
	}
}

// TestFindSpace_LinkedWorktreeMatchesItsOwnSpace: a project row pointing at
// a linked worktree's checkout resolves to that worktree's workspace, not
// to the parent repository's -- the checkout the tab's cwd will be in is
// the one whose space it should join.
func TestFindSpace_LinkedWorktreeMatchesItsOwnSpace(t *testing.T) {
	sp, ok := FindSpace(snapshot(), "/home/zvi/.herdr/worktrees/toysim/orch-meeting-followups", "/home/zvi/Projects/toysim")
	if !ok || sp.WorkspaceID != "w5R" {
		t.Errorf("FindSpace = %+v, %v; want w5R", sp, ok)
	}
}

// TestFindSpace_SubdirectoryFallsBackToTheRepoRoot: a project row inside a
// repository (a package directory, say) has no workspace of its own; the
// repository's primary space is the answer, found through repoRoot.
func TestFindSpace_SubdirectoryFallsBackToTheRepoRoot(t *testing.T) {
	sp, ok := FindSpace(snapshot(), "/home/zvi/Projects/toysim/internal/thing", "/home/zvi/Projects/toysim")
	if !ok || sp.WorkspaceID != "wX" {
		t.Errorf("FindSpace = %+v, %v; want wX via the repo root", sp, ok)
	}
}

// TestFindSpace_NoSpaceForARepoWithNone pins the other half of the
// default: a repository with no workspace open resolves to nothing, so the
// caller stays on `new space`.
func TestFindSpace_NoSpaceForARepoWithNone(t *testing.T) {
	if sp, ok := FindSpace(snapshot(), "/home/zvi/Projects/nanoclaw", "/home/zvi/Projects/nanoclaw"); ok {
		t.Errorf("FindSpace = %+v, want not found for a repository with no open workspace", sp)
	}
	// A plain-shell workspace (no worktree context at all) never matches
	// anything, and an empty snapshot matches nothing.
	if _, ok := FindSpace(snapshot(), "", ""); ok {
		t.Error("FindSpace with an empty project dir reported a match")
	}
	if _, ok := FindSpace(nil, "/home/zvi/Projects/toysim", "/home/zvi/Projects/toysim"); ok {
		t.Error("FindSpace over no workspaces reported a match")
	}
}

// TestFindSpace_TrailingSlashAndDotSegments: the project row is typed by a
// person; herdr's paths are not. A trailing slash or a `.` segment must not
// hide the match.
func TestFindSpace_TrailingSlashAndDotSegments(t *testing.T) {
	for _, dir := range []string{"/home/zvi/Projects/herdr-draft/", "/home/zvi/Projects/./herdr-draft", "/home/zvi/Projects/toysim/../herdr-draft"} {
		if sp, ok := FindSpace(snapshot(), dir, ""); !ok || sp.WorkspaceID != "wG" {
			t.Errorf("FindSpace(%q) = %+v, %v; want wG", dir, sp, ok)
		}
	}
}

// TestFindSpace_LabelFallsBackToTheID: a workspace herdr reports with no
// label still has to be nameable on the placement row.
func TestFindSpace_LabelFallsBackToTheID(t *testing.T) {
	ws := []herdrc.WorkspaceInfo{{WorkspaceID: "w9", Worktree: &herdrc.ContextWorktree{CheckoutPath: "/r"}}}
	sp, ok := FindSpace(ws, "/r", "/r")
	if !ok || sp.Label != "w9" {
		t.Errorf("FindSpace = %+v, %v; want Label to fall back to the id", sp, ok)
	}
}

// TestBuild_TabInPlacesTheTabInTheNamedSpace: PlacementTabIn is
// PlacementTabHere aimed at a workspace the caller names (Input.Space)
// rather than the invoking pane's own -- one OpTabCreate whose Workspace
// is that id, cwd the project directory.
func TestBuild_TabInPlacesTheTabInTheNamedSpace(t *testing.T) {
	in := validInput()
	in.Placement = PlacementTabIn
	in.Space = Space{WorkspaceID: "wG", Label: "herdr-draft"}
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if ops[0].Kind != OpTabCreate || ops[0].Tab == nil {
		t.Fatalf("ops[0] = %v, want OpTabCreate with a Tab request", ops[0].Kind)
	}
	if got := ops[0].Tab.Workspace; got != "wG" {
		t.Errorf("Tab.Workspace = %q, want the named space wG, not the invoking %q", got, in.Ctx.WorkspaceID)
	}
	if got := ops[0].Tab.Cwd; got != in.ProjectDir {
		t.Errorf("Tab.Cwd = %q, want %q", got, in.ProjectDir)
	}
}

// TestBuild_TabInUnderAWorktreeKeepsTheCheckoutsSpace: placement spec
// §5.3 unchanged -- the worktree still opens its own grouped space, and the
// agent's tab lands in the named space on the checkout's path.
func TestBuild_TabInUnderAWorktreeKeepsTheCheckoutsSpace(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	in.Placement = PlacementTabIn
	in.Space = Space{WorkspaceID: "wG", Label: "herdr-draft"}
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(ops) < 2 || ops[0].Kind != OpWorktreeCreate || ops[1].Kind != OpTabCreate {
		t.Fatalf("op kinds = %v, want [OpWorktreeCreate OpTabCreate ...]", kindsOf(ops))
	}
	if ops[1].Tab.Workspace != "wG" || !ops[1].CwdFromCheckout {
		t.Errorf("placement op = %+v (CwdFromCheckout=%v), want Workspace wG with the cwd taken from the checkout", ops[1].Tab, ops[1].CwdFromCheckout)
	}
}

// TestBuild_TabInWithoutASpaceIsRefused: a tab-in with no workspace to
// aim at cannot be built. Guessing "here" is exactly what requireContext
// exists to prevent for tab-here, and this is the same refusal one layer
// down.
func TestBuild_TabInWithoutASpaceIsRefused(t *testing.T) {
	in := validInput()
	in.Placement = PlacementTabIn
	if _, err := Build(in); err == nil {
		t.Fatal("Build accepted PlacementTabIn with an empty Input.Space, want an error")
	}
}
