package plan

import (
	"context"
	"strings"
	"testing"
)

// laneHead is a linked checkout's HEAD commit, as a caller resolves it.
const laneHead = "3f2a9c1e5b7d4a608e1f2b3c4d5e6f708192a3b4"

// laneInput is a worktree session whose project is a linked checkout of
// /repo -- a lane (#171) -- with nothing chosen for the base.
func laneInput() Input {
	in := validInput()
	in.ProjectDir = "/wt/lane"
	in.UseWorktree = true
	in.BaseRef = ""
	in.Linked = LinkedCheckout{RepoRoot: "/repo", Head: laneHead}
	return in
}

// TestBuild_ALinkedCheckoutIsCutFromItsCommitInTheRepoRoot is #171: herdr
// refuses a linked checkout as a worktree source (linked_worktree_source),
// so `worktree create` names the repository root instead -- and, the base
// being unset, the lane's own commit, or herdr would cut the branch from the
// PRIMARY checkout's HEAD.
func TestBuild_ALinkedCheckoutIsCutFromItsCommitInTheRepoRoot(t *testing.T) {
	ops, err := Build(laneInput())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	wt := ops[0].Worktree
	if wt == nil {
		t.Fatalf("ops[0] = %v, want the worktree create", ops[0].Kind)
	}
	if wt.Cwd != "/repo" {
		t.Errorf("Worktree.Cwd = %q, want the repository root %q: herdr refuses a linked checkout as the source", wt.Cwd, "/repo")
	}
	if wt.Base != laneHead {
		t.Errorf("Worktree.Base = %q, want the lane's commit %q", wt.Base, laneHead)
	}
}

// TestBuild_AChosenBaseStillWinsFromALinkedCheckout: the lane's commit is
// what an UNSET base means there, nothing more. The source is still the
// repository root, which is the half that has nothing to do with the base.
func TestBuild_AChosenBaseStillWinsFromALinkedCheckout(t *testing.T) {
	in := laneInput()
	in.BaseRef = "main"
	in.Linked.Head = ""

	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if wt := ops[0].Worktree; wt.Cwd != "/repo" || wt.Base != "main" {
		t.Errorf("Worktree Cwd, Base = %q, %q; want %q, %q", wt.Cwd, wt.Base, "/repo", "main")
	}
}

// TestBuild_RefusesALinkedCheckoutWithNoCommitToCutFrom: with the source
// moved to the repository root, an empty base would mean the primary
// checkout's HEAD -- the wrong commit, silently. Refused instead, so a
// caller that forgot to resolve the lane's commit cannot build that plan.
func TestBuild_RefusesALinkedCheckoutWithNoCommitToCutFrom(t *testing.T) {
	in := laneInput()
	in.Linked.Head = ""

	ops, err := Build(in)
	if err == nil {
		t.Fatalf("Build = %v, want an error: no base and no resolved commit", kindsOf(ops))
	}
	if !strings.Contains(err.Error(), "/wt/lane") {
		t.Errorf("error = %q, want it to name the checkout", err)
	}
}

// TestBuild_WithoutAWorktreeTheSessionStaysInTheLinkedCheckout is #171's
// second decision: only `worktree create`'s --cwd moves to the repository
// root. A session with no worktree runs in the project directory itself --
// the lane -- which is what spawn skill §3 promises for --no-worktree.
func TestBuild_WithoutAWorktreeTheSessionStaysInTheLinkedCheckout(t *testing.T) {
	in := laneInput()
	in.UseWorktree = false

	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if ws := ops[0].Workspace; ws == nil || ws.Cwd != "/wt/lane" {
		t.Fatalf("ops[0] = %+v, want a workspace create in the linked checkout", ops[0])
	}
}

// TestWorktreeBase is the base `worktree create` is given, which the popup's
// submit rows and `create --json` report and the clean check counts from.
func TestWorktreeBase(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   Input
		want string
	}{
		{"a chosen base", Input{BaseRef: "main", Linked: LinkedCheckout{RepoRoot: "/repo", Head: laneHead}}, "main"},
		{"unset, from a linked checkout", Input{Linked: LinkedCheckout{RepoRoot: "/repo", Head: laneHead}}, laneHead},
		{"unset, from a primary checkout", Input{}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := WorktreeBase(tc.in); got != tc.want {
				t.Errorf("WorktreeBase = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestResolveBaseRef_ALinkedCheckoutCountsFromTheCommitItWasCutFrom: the
// clean gate counts the worktree's own commits from its base. From a lane
// that is the commit the plan named, which needs no git call to know --
// the empty ProjectDir here would fail one.
func TestResolveBaseRef_ALinkedCheckoutCountsFromTheCommitItWasCutFrom(t *testing.T) {
	in := laneInput()
	in.ProjectDir = ""

	got, err := resolveBaseRef(context.Background(), in)
	if err != nil {
		t.Fatalf("resolveBaseRef: %v", err)
	}
	if got != laneHead {
		t.Errorf("resolveBaseRef = %q, want the lane's commit %q", got, laneHead)
	}
}
