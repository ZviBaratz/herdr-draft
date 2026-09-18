package plan

import (
	"context"
	"strings"
	"testing"
)

// laneHead is a linked checkout's HEAD commit, and laneMain the commit its
// `main` names, as a caller resolves them in that checkout.
const (
	laneHead = "3f2a9c1e5b7d4a608e1f2b3c4d5e6f708192a3b4"
	laneMain = "9b8a7c6d5e4f3a2b1c0d9e8f7a6b5c4d3e2f1a0b"
)

// laneInput is a worktree session whose project is a linked checkout of
// /repo -- a lane (#171) -- with nothing chosen for the base.
func laneInput() Input {
	in := validInput()
	in.ProjectDir = "/wt/lane"
	in.UseWorktree = true
	in.BaseRef = ""
	in.Linked = LinkedCheckout{RepoRoot: "/repo", Commit: laneHead}
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

// TestBuild_AChosenBaseIsTheCommitItNamesInTheLinkedCheckout: herdr runs
// `git worktree add` in the source, so from the repository root a ref that
// means something per checkout -- HEAD above all -- would name the PRIMARY
// checkout's commit. The caller resolves the chosen base in the lane, and the
// commit is what herdr is given. For a branch that is the same commit either
// way.
func TestBuild_AChosenBaseIsTheCommitItNamesInTheLinkedCheckout(t *testing.T) {
	in := laneInput()
	in.BaseRef = "main"
	in.Linked.Commit = laneMain

	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if wt := ops[0].Worktree; wt.Cwd != "/repo" || wt.Base != laneMain {
		t.Errorf("Worktree Cwd, Base = %q, %q; want %q, %q", wt.Cwd, wt.Base, "/repo", laneMain)
	}
}

// TestBuild_RefusesALinkedCheckoutWithNoCommitToCutFrom: with the source
// moved to the repository root, a base handed over unresolved would be
// resolved there -- an empty one, or HEAD, as the primary checkout's commit,
// silently. Refused instead, chosen base or not, so a caller that forgot to
// resolve it in the lane cannot build that plan.
func TestBuild_RefusesALinkedCheckoutWithNoCommitToCutFrom(t *testing.T) {
	for _, base := range []string{"", "HEAD", "main"} {
		in := laneInput()
		in.BaseRef = base
		in.Linked.Commit = ""

		ops, err := Build(in)
		if err == nil {
			t.Errorf("base %q: Build = %v, want an error: nothing resolved in the lane", base, kindsOf(ops))
			continue
		}
		if !strings.Contains(err.Error(), "/wt/lane") {
			t.Errorf("base %q: error = %q, want it to name the checkout", base, err)
		}
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
		{"a chosen base, from a linked checkout", Input{BaseRef: "main", Linked: LinkedCheckout{RepoRoot: "/repo", Commit: laneMain}}, laneMain},
		{"unset, from a linked checkout", Input{Linked: LinkedCheckout{RepoRoot: "/repo", Commit: laneHead}}, laneHead},
		{"a chosen base, from a primary checkout", Input{BaseRef: "main"}, "main"},
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
