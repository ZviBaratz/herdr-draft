package create

import (
	"context"
	"errors"
	"testing"

	"github.com/ZviBaratz/herdr-draft/internal/picker"
)

// #252's seam, from both sides. `create` is killed by a signal rather than
// quit, and main turns SIGINT/SIGTERM into a cancel of the context it passes
// here. What that cancel may and may not reach is the whole decision.

// The pre-flight side: the account pick runs on the CALLER's context, so a
// cancel kills the picker with it. It is the one picker call that is not a
// dry run, so it is the one that writes the ledger -- and a pick nobody is
// waiting for any more must not reach it.
func TestTheAccountPickRunsOnTheCallersContext(t *testing.T) {
	h := newHarness(t)
	p := &fakePicker{res: picker.Result{Profile: "alpha-1", ConfigDir: "/dirs/alpha-1"}, obeyCtx: true}
	h.deps.Picker = p

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	code := h.runCtx(ctx, "--title", "fix login", "--account", "auto", "--no-worktree")

	if code == ExitOK {
		t.Fatal("a create whose context was cancelled created a session anyway")
	}
	if h.createdAnything() {
		t.Fatalf("a cancelled pre-flight created something: %v", h.runner.calls)
	}
	if len(p.calls) != 1 {
		t.Fatalf("picker calls = %d, want the one commit pick", len(p.calls))
	}
	if !errors.Is(p.sawErr, context.Canceled) {
		t.Fatalf("the pick ran on a context reporting %v, want context.Canceled -- it is not the caller's, so a signal cannot reach the picker", p.sawErr)
	}
}

// The same cancel, with a pick that ANSWERS anyway -- which picker.CLI does
// for a picker that exited 0 before the signal and was only draining a
// child's pipe (#291). The test above cannot see this: its fake refuses, so
// the refusal stops the run before the seam is reached, with or without a
// check there. Here nothing stops it except the seam asking the context
// itself, and without that a signalled create would start a plan that main's
// re-raise kills mid-creation.
func TestAnAnsweredPickDoesNotCarryACancelledCreateAcrossTheSeam(t *testing.T) {
	h := newHarness(t)
	p := &fakePicker{res: picker.Result{Profile: "alpha-1", ConfigDir: "/dirs/alpha-1"}}
	h.deps.Picker = p

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	code := h.runCtx(ctx, "--title", "fix login", "--account", "auto", "--no-worktree")

	if len(p.calls) != 1 {
		t.Fatalf("picker calls = %d, want the one commit pick -- this test is about a pick that answered", len(p.calls))
	}
	if code == ExitOK {
		t.Fatal("a create whose context was cancelled before the seam created a session anyway")
	}
	if h.createdAnything() {
		t.Fatalf("a create cancelled before the seam created something: %v", h.runner.calls)
	}
	if !errors.Is(p.sawErr, context.Canceled) {
		t.Fatalf("the pick saw %v, want context.Canceled -- the premise is a pick that answered on a cancelled context", p.sawErr)
	}
}

// And the other side, which is the half that keeps this safe to do at all:
// plan.Execute does NOT inherit that cancellation. A pre-flight abort has
// created nothing, so it is free; an Execute abort would tear the pipeline
// out mid-creation and leave a half-built session that the keep-or-clean
// gate never sees. `--dry-run` stops at this same boundary, and the popup's
// own app.Lifetime covers the picks and not runSubmitCmd, for the same
// reason.
func TestPlanExecuteDoesNotInheritTheCancellation(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// A signal arriving in the middle of the pipeline: the first op is
	// done, and the rest must still run.
	h.runner.onOp = func(op string) {
		if op == "WorkspaceCreate" {
			cancel()
		}
	}

	code := h.runCtx(ctx, "--title", "fix login", "--no-worktree", "--placement", "new-space")

	if code != ExitOK {
		t.Fatalf("exit = %d, want %d -- a cancel during the plan abandoned it\nstderr: %s", code, ExitOK, h.stderr)
	}
	if err, ok := h.runner.ctxErrs["WorkspaceCreate"]; !ok || err != nil {
		t.Fatalf("the first op saw ctx.Err() = %v (recorded: %v), want a live context", err, ok)
	}
	if err, ok := h.runner.ctxErrs["AgentStart"]; !ok {
		t.Fatal("AgentStart never ran -- the cancelled plan stopped early")
	} else if err != nil {
		t.Fatalf("AgentStart ran on a context reporting %v, want a live one: plan.Execute must not inherit the caller's cancellation", err)
	}
}
