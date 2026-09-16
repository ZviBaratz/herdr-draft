package create

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
)

// calledWith reports whether any recorded call starts with prefix -- the
// harness's own called() matches a method NAME, and these tests need the
// first argument too (which workspace the tab went in).
func calledWith(h *harness, prefix string) bool {
	for _, c := range h.runner.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

// openRepoSpace teaches the harness's fake herdr that the project's
// primary checkout already has a workspace open -- #128's situation.
func openRepoSpace(h *harness) {
	h.runner.workspaces = []herdrc.WorkspaceInfo{
		{WorkspaceID: "w4", Label: "Quantivly", PaneCount: 7},
		{WorkspaceID: "wG", Label: "thing", PaneCount: 2, Worktree: &herdrc.ContextWorktree{
			RepoRoot: "/projects/thing", CheckoutPath: "/projects/thing", RepoName: "thing"}},
	}
}

// TestTabIn_IsTheDefaultWhenTheRepoHasASpace is rabota-v2 C10's default:
// with nothing configured and the project's space open, `create` puts the
// agent's tab in that space rather than making a new one, and says which
// tier decided.
func TestTabIn_IsTheDefaultWhenTheRepoHasASpace(t *testing.T) {
	h := newHarness(t)
	openRepoSpace(h)

	if code := h.run("--title", "t", "--no-worktree", "--json"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	if h.runner.called("WorkspaceCreate") {
		t.Fatalf("a new workspace was created although the repository already has one: %v", h.runner.calls)
	}
	if !calledWith(h, "TabCreate(wG,") {
		t.Fatalf("calls = %v, want TabCreate in wG", h.runner.calls)
	}
	var out struct {
		Placement   string            `json:"placement"`
		WorkspaceID string            `json:"workspace_id"`
		Provenance  map[string]string `json:"provenance"`
	}
	if err := json.Unmarshal([]byte(h.stdout.String()), &out); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, h.stdout)
	}
	if out.Placement != "tab-in" || out.WorkspaceID != "wG" {
		t.Errorf("placement/workspace_id = %q/%q, want tab-in/wG", out.Placement, out.WorkspaceID)
	}
	if out.Provenance["placement"] != "herdr workspace list" {
		t.Errorf("provenance[placement] = %q, want %q", out.Provenance["placement"], "herdr workspace list")
	}
}

// TestTabIn_NewSpaceStaysForARepoWithNone pins the other half: no open
// space, and the default is exactly what it was.
func TestTabIn_NewSpaceStaysForARepoWithNone(t *testing.T) {
	h := newHarness(t)
	h.runner.workspaces = []herdrc.WorkspaceInfo{{WorkspaceID: "w4", Label: "Quantivly"}}

	if code := h.run("--title", "t", "--no-worktree"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	if !h.runner.called("WorkspaceCreate") || h.runner.called("TabCreate") {
		t.Errorf("calls = %v, want a new workspace and no tab", h.runner.calls)
	}
}

// TestTabIn_ExplicitNewSpaceStillWins: `--placement new-space` is the way
// to insist on a space of its own while the repository has one open.
func TestTabIn_ExplicitNewSpaceStillWins(t *testing.T) {
	h := newHarness(t)
	openRepoSpace(h)
	if code := h.run("--title", "t", "--no-worktree", "--placement", "new-space"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	if !h.runner.called("WorkspaceCreate") {
		t.Errorf("calls = %v, want the explicit new-space honoured", h.runner.calls)
	}
}

// TestTabIn_ExplicitWithNoSpaceIsUsageError: asking for tab-in where no
// workspace holds the project is refused before herdr is touched, with the
// remedy named.
func TestTabIn_ExplicitWithNoSpaceIsUsageError(t *testing.T) {
	h := newHarness(t)
	h.runner.workspaces = []herdrc.WorkspaceInfo{{WorkspaceID: "w4", Label: "Quantivly"}}
	if code := h.run("--title", "t", "--no-worktree", "--placement", "tab-in"); code != ExitUsage {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitUsage, h.stderr)
	}
	if !strings.Contains(h.stderr.String(), "--workspace") {
		t.Errorf("stderr = %q, want the remedy to name --workspace", h.stderr)
	}
	if h.runner.called("TabCreate") || h.runner.called("WorkspaceCreate") {
		t.Errorf("herdr was touched on a usage error: %v", h.runner.calls)
	}
}

// TestTabIn_WorkspaceFlagNamesAnyOpenSpace is #128's own proposal: a
// script in one space starting a session in another, by id, with none of
// the pane variables consulted.
func TestTabIn_WorkspaceFlagNamesAnyOpenSpace(t *testing.T) {
	h := newHarness(t)
	openRepoSpace(h)
	h.env.WorkspaceID, h.env.TabID, h.env.PaneID = "", "", ""

	if code := h.run("--title", "t", "--no-worktree", "--workspace", "w4", "--json"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	if !calledWith(h, "TabCreate(w4,") {
		t.Fatalf("calls = %v, want TabCreate in w4 (the named space, not the repo's wG)", h.runner.calls)
	}
	var out struct {
		Placement  string            `json:"placement"`
		Provenance map[string]string `json:"provenance"`
	}
	if err := json.Unmarshal([]byte(h.stdout.String()), &out); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, h.stdout)
	}
	if out.Placement != "tab-in" || out.Provenance["placement"] != "flag" {
		t.Errorf("placement %q from %q, want tab-in from flag", out.Placement, out.Provenance["placement"])
	}
}

// TestTabIn_WorkspaceFlagRefusals: an unknown id, and a --workspace beside
// a placement that cannot use it, are both usage errors.
func TestTabIn_WorkspaceFlagRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"unknown id", []string{"--title", "t", "--no-worktree", "--workspace", "w9"}, "unknown --workspace"},
		{"with split-here", []string{"--title", "t", "--no-worktree", "--workspace", "w4", "--placement", "split-here"}, "does not apply"},
		{"empty id", []string{"--title", "t", "--no-worktree", "--workspace", ""}, "needs a workspace id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			openRepoSpace(h)
			if code := h.run(tc.args...); code != ExitUsage {
				t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitUsage, h.stderr)
			}
			if !strings.Contains(h.stderr.String(), tc.want) {
				t.Errorf("stderr = %q, want it to contain %q", h.stderr, tc.want)
			}
		})
	}
}

// TestTabIn_CleanClosesOnlyTheTab: --on-failure clean after a tab-in
// failure closes the tab it made and never the workspace it borrowed.
func TestTabIn_CleanClosesOnlyTheTab(t *testing.T) {
	h := newHarness(t)
	openRepoSpace(h)
	h.runner.failAt = "AgentStart"
	if code := h.run("--title", "t", "--no-worktree", "--on-failure", "clean"); code != ExitFailed {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitFailed, h.stderr)
	}
	if !h.runner.called("TabClose") || h.runner.called("WorkspaceClose") {
		t.Errorf("calls = %v, want the tab closed and the borrowed workspace untouched", h.runner.calls)
	}
}
