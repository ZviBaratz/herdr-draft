package create

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
)

// refusedFirst is herdr's refusal of #192's own case as herdrc.CLIRunner
// reports it: a `worktree create` from a lane of a repository whose primary
// checkout could not be found, refused before git ran.
var refusedFirst = fmt.Errorf("%w: herdr worktree create: exit status 1: "+
	`{"error":{"code":"linked_worktree_source","message":"New and open worktree actions start from the repo parent workspace."}}`,
	herdrc.ErrNothingCreated)

// TestExitNothingCreated_FirstStepRefused is #192: herdr refused the op that
// makes the space, so nothing exists -- which the exit status alone now
// says, rather than the 1 that tells a caller the session may half-exist.
// --on-failure has nothing to act on, and nothing is attempted.
func TestExitNothingCreated_FirstStepRefused(t *testing.T) {
	h := newHarness(t)
	h.runner.failAt = "WorktreeCreate"
	h.runner.failErr = refusedFirst

	code := h.run("--title", "t", "--worktree", "--on-failure", "clean")
	if code != ExitNothingCreated {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitNothingCreated, h.stderr)
	}
	if h.runner.called("WorktreeRemove") || h.runner.called("WorkspaceClose") {
		t.Errorf("nothing was created, but a clean was attempted: %v", h.runner.calls)
	}
	if !strings.Contains(h.stderr.String(), "\nnothing was created") {
		t.Errorf("stderr = %q, want it to say nothing was created", h.stderr)
	}
	if h.stdout.Len() != 0 {
		t.Errorf("stdout = %q, want it empty: it carries a created session and nothing else", h.stdout)
	}
}

// TestExitNothingCreated_JSON is what --json says on exit 4: the failure and
// the step, and none of the ids a created session would carry.
func TestExitNothingCreated_JSON(t *testing.T) {
	h := newHarness(t)
	h.runner.failAt = "WorktreeCreate"
	h.runner.failErr = refusedFirst

	if code := h.run("--title", "t", "--worktree", "--json"); code != ExitNothingCreated {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitNothingCreated, h.stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(h.stdout.String()), &got); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, h.stdout)
	}
	if got["ok"] != false {
		t.Errorf("ok = %v, want false", got["ok"])
	}
	if got["failed_step"] != "creating worktree" {
		t.Errorf("failed_step = %v, want %q", got["failed_step"], "creating worktree")
	}
	if e, _ := got["error"].(string); !strings.Contains(e, "linked_worktree_source") {
		t.Errorf("error = %q, want herdr's refusal in it", e)
	}
	for _, k := range []string{"workspace_id", "tab_id", "pane_id", "checkout_path",
		"space_workspace_id", "space_tab_id", "space_pane_id"} {
		if _, present := got[k]; present {
			t.Errorf("--json carries %q = %v, want it absent: nothing was created", k, got[k])
		}
	}
}

// TestExitOne_FirstStepFailsWithoutEvidence is the other side of the rule:
// the first step failed, Created is nil, and nothing says herdr refused
// before acting. herdr 0.9.0 answers worktree_open_failed after `git
// worktree add` has made the checkout and its branch. That stays exit 1,
// and stderr stops saying nothing was created: it names what may be left.
func TestExitOne_FirstStepFailsWithoutEvidence(t *testing.T) {
	h := newHarness(t)
	h.runner.failAt = "WorktreeCreate"
	h.runner.failErr = fmt.Errorf("herdr worktree create: exit status 1: " +
		`{"error":{"code":"worktree_open_failed","message":"created worktree but failed to open workspace: no pty"}}`)

	code := h.run("--title", "t", "--worktree", "--branch", "zvi/t")
	if code != ExitFailed {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitFailed, h.stderr)
	}
	if strings.Contains(h.stderr.String(), "nothing was created") {
		t.Errorf("stderr = %q, claims nothing was created with no evidence of it", h.stderr)
	}
	if want := "herdr may have made part of it before failing -- the worktree's checkout, or its branch zvi/t; look before retrying"; !strings.Contains(h.stderr.String(), want) {
		t.Errorf("stderr = %q\nwant it to contain %q", h.stderr, want)
	}
}
