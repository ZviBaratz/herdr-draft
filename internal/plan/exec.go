// exec.go executes a creation plan (Task 12's Build output) against a
// herdrc.Runner, threading each op's step-1 output into later ops, and
// implements the keep-or-clean gate that runs after a plan finishes
// (spec §9).
package plan

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ZviBaratz/herdr-draft/internal/gitx"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
)

// StepState is one op's progress state, reported to Execute's onProgress
// callback before and after the op runs.
type StepState int

const (
	StepPending StepState = iota
	StepRunning
	StepDone
	StepFailed
)

// String names a StepState for progress lines and test failure output.
func (s StepState) String() string {
	switch s {
	case StepPending:
		return "StepPending"
	case StepRunning:
		return "StepRunning"
	case StepDone:
		return "StepDone"
	case StepFailed:
		return "StepFailed"
	default:
		return fmt.Sprintf("StepState(%d)", int(s))
	}
}

// Progress reports one op's state transition to Execute's caller. Index is
// the op's position among Total ops, Label is that op's Op.Label, and Err
// is only ever non-nil when State is StepFailed.
type Progress struct {
	Index, Total int
	Label        string
	State        StepState
	Err          error
}

// ExecResult is Execute's outcome. Created is the step-1 topology and is
// nil until the topology op succeeds. FailedIndex is the index of the
// first op that failed, or -1 on success. PromptText is only populated
// when the OpAgentPrompt op fails, so the caller can surface the prompt
// text that was never sent back to the user for manual paste (spec §9
// step 3).
type ExecResult struct {
	Created     *herdrc.CreatedTopology
	FailedIndex int
	PromptText  string
}

// busyRetryInterval, busyRetryBudget, and busyRetryNow implement the busy
// retry spec §9 describes for both launch paths, generalized here to any
// op Execute runs: an op failing with an agent_pane_busy error (the
// target pane's shell still starting right after topology creation --
// herdr:src/app/agents.rs:255, upstream #3375's race) is retried every
// busyRetryInterval until busyRetryBudget has elapsed since the first
// attempt, judged by busyRetryNow. These are package vars rather than
// Execute parameters -- Execute's signature is fixed by contract -- so
// tests can shrink the interval to zero and/or fake the clock to run the
// retry loop instantly instead of waiting out real sleeps. See
// withBusyRetryOverrides in exec_test.go.
var (
	busyRetryInterval = 500 * time.Millisecond
	busyRetryBudget   = 5 * time.Second
	busyRetryNow      = time.Now
)

// busyPaneErrorCode is the herdr error code (herdr:src/app/agents.rs:255)
// that marks an op rejected only because its target pane's shell is still
// starting right after topology creation -- worth retrying, unlike any
// other failure.
const busyPaneErrorCode = "agent_pane_busy"

// malformedOpError reports an Op of the given Kind whose request field
// (Worktree/Workspace/Tab/Split/Agent/Prompt) is nil. Build (build.go)
// always populates the right field for each Kind it emits, but Execute
// must not simply trust that contract: a hand-constructed or corrupted Op
// has to fail gracefully here instead of panicking on a nil dereference.
func malformedOpError(kind OpKind) error {
	return fmt.Errorf("malformed op: %s missing its request", kind)
}

// isBusyPaneError reports whether err's text contains busyPaneErrorCode.
// herdrc.CLIRunner surfaces herdr CLI failures as plain text (the
// subcommand's stderr wrapped into the error message), not as a typed
// error code, so a substring match is the only signal available here.
func isBusyPaneError(err error) bool {
	return err != nil && strings.Contains(err.Error(), busyPaneErrorCode)
}

// retryBusy runs op once and, while it keeps failing with an
// agent_pane_busy error, retries it every busyRetryInterval until
// busyRetryBudget has elapsed since the first attempt or ctx is done. Any
// non-busy error is returned immediately, with no retry.
func retryBusy(ctx context.Context, op func() error) error {
	deadline := busyRetryNow().Add(busyRetryBudget)
	for {
		err := op()
		if err == nil || !isBusyPaneError(err) {
			return err
		}
		if ctx.Err() != nil || !busyRetryNow().Before(deadline) {
			return err
		}
		// A zero or negative duration returns immediately (time.Sleep's
		// documented behavior), so tests that want an instant retry loop
		// only need to set busyRetryInterval to 0 -- no separate sleep
		// hook is needed.
		time.Sleep(busyRetryInterval)
	}
}

// emitProgress calls onProgress, when non-nil, with a Progress built from
// the given fields.
func emitProgress(onProgress func(Progress), index, total int, label string, state StepState, err error) {
	if onProgress == nil {
		return
	}
	onProgress(Progress{Index: index, Total: total, Label: label, State: state, Err: err})
}

// promptIfReady reads req.Target's current detection-source screen
// (Runner.AgentRead) and checks it for a blocking confirmation/selection
// dialog (blockingDialogSignature, dialog.go) before ever sending req's
// prompt text via Runner.AgentPrompt -- spec §9 step 3's own principle,
// hardened by task 19's live checkpoint finding that herdr's own agent
// detection can report a pane idle/interactive_ready while it is actually
// showing a screen like this: never send input into a state that has not
// been positively confirmed safe to type into. A pane whose screen cannot
// be read at all is treated the same as a detected dialog -- when in
// doubt, keep the session and surface the prompt text for manual paste,
// rather than assume "unreadable" means "safe".
//
// Neither failure mode calls Runner.AgentPrompt at all, so the agent is
// never sent text (and never sent the trailing Enter that, on the
// trust-dialog screen, was what actually killed it) -- Execute's existing
// OpAgentPrompt failure path (result.PromptText, the keep-or-clean gate)
// takes over exactly as it does for a "real" AgentPrompt error, since from
// Execute's point of view this is just another error from this op.
func promptIfReady(ctx context.Context, r herdrc.Runner, req herdrc.AgentPromptReq) error {
	screen, err := r.AgentRead(ctx, req.Target)
	if err != nil {
		return fmt.Errorf("could not confirm the agent is ready for a prompt: %w", err)
	}
	if sig := blockingDialogSignature(screen); sig != "" {
		return fmt.Errorf("agent is waiting on a dialog (%q) -- prompt not sent", sig)
	}
	return r.AgentPrompt(ctx, req)
}

// Execute runs ops in order against r. It threads each op's step-1 output
// (the topology op's workspace/tab/pane ids) into every later op that
// needs it -- OpAgentStart's Agent.PaneID, OpClauthLaunch's PaneRun target
// pane, OpAwaitDetection's target pane, and OpAgentPrompt's Prompt.Target
// -- since Build (build.go) leaves those fields empty for exactly this
// reason. Execute reports Progress before and after each op and stops at
// the first op that fails, after that op's busy retry budget (if any) is
// exhausted. It never panics: an Op whose Kind requires a request field
// that is nil (see malformedOpError) fails that op gracefully instead of
// dereferencing nil.
func Execute(ctx context.Context, r herdrc.Runner, ops []Op, onProgress func(Progress)) ExecResult {
	result := ExecResult{FailedIndex: -1}

	var created herdrc.CreatedTopology
	haveCreated := false
	total := len(ops)

	for i, op := range ops {
		emitProgress(onProgress, i, total, op.Label, StepRunning, nil)

		var topo herdrc.CreatedTopology
		gotTopo := false
		var promptText string

		runErr := retryBusy(ctx, func() error {
			var err error
			switch op.Kind {
			case OpWorktreeCreate:
				if op.Worktree == nil {
					return malformedOpError(op.Kind)
				}
				topo, err = r.WorktreeCreate(ctx, *op.Worktree)
				gotTopo = err == nil
			case OpWorkspaceCreate:
				if op.Workspace == nil {
					return malformedOpError(op.Kind)
				}
				topo, err = r.WorkspaceCreate(ctx, *op.Workspace)
				gotTopo = err == nil
			case OpTabCreate:
				if op.Tab == nil {
					return malformedOpError(op.Kind)
				}
				topo, err = r.TabCreate(ctx, *op.Tab)
				gotTopo = err == nil
			case OpPaneSplit:
				if op.Split == nil {
					return malformedOpError(op.Kind)
				}
				topo, err = r.PaneSplit(ctx, *op.Split)
				gotTopo = err == nil
			case OpAgentStart:
				if op.Agent == nil {
					return malformedOpError(op.Kind)
				}
				req := *op.Agent
				if req.PaneID == "" && haveCreated {
					req.PaneID = created.PaneID
				}
				err = r.AgentStart(ctx, req)
			case OpClauthLaunch:
				paneID := ""
				if haveCreated {
					paneID = created.PaneID
				}
				err = r.PaneRun(ctx, paneID, op.RunArgv)
			case OpAwaitDetection:
				paneID := ""
				if haveCreated {
					paneID = created.PaneID
				}
				err = r.AwaitDetection(ctx, paneID, op.Timeout)
			case OpAgentPrompt:
				if op.Prompt == nil {
					return malformedOpError(op.Kind)
				}
				req := *op.Prompt
				if req.Target == "" && haveCreated {
					req.Target = created.PaneID
				}
				promptText = req.Text
				err = promptIfReady(ctx, r, req)
			default:
				err = fmt.Errorf("plan: execute: unknown op kind %v", op.Kind)
			}
			return err
		})

		if runErr != nil {
			wrapped := fmt.Errorf("plan: execute: %s: %w", op.Label, runErr)
			emitProgress(onProgress, i, total, op.Label, StepFailed, wrapped)
			result.FailedIndex = i
			if op.Kind == OpAgentPrompt {
				result.PromptText = promptText
			}
			return result
		}

		if gotTopo {
			created = topo
			haveCreated = true
			c := topo
			result.Created = &c
		}
		emitProgress(onProgress, i, total, op.Label, StepDone, nil)
	}

	return result
}

// CleanDecision is CleanCheck's verdict: whether Clean is safe to run for
// the space Execute created, and -- when it isn't -- a human-readable
// reason to show the user (spec §9: "the clean option is disabled with
// the reason shown").
type CleanDecision struct {
	Allowed bool
	Reason  string
}

// CleanCheck reports whether Clean is safe to run for the space Execute
// created (spec §9's keep-or-clean gate). Worktree spaces defer to
// gitx.Disposable -- which refuses when the checkout has uncommitted
// changes or commits ahead of in.BaseRef -- using created.CheckoutPath,
// the field herdrc.CreatedTopology carries precisely for this. Any error
// from Disposable itself (e.g. an invalid BaseRef) is surfaced as a
// denied decision with the error folded into Reason, never as a silent
// "allowed". Non-worktree spaces (a plain workspace/tab/pane) have no
// on-disk checkout to lose -- only pane content herdr-draft itself
// created -- so they are always allowed.
func CleanCheck(ctx context.Context, in Input, created herdrc.CreatedTopology) CleanDecision {
	if !in.UseWorktree {
		return CleanDecision{Allowed: true}
	}

	ok, reason, err := gitx.Disposable(ctx, created.CheckoutPath, in.BaseRef)
	if err != nil {
		return CleanDecision{
			Allowed: false,
			Reason:  fmt.Sprintf("could not determine whether the worktree is safe to remove: %v", err),
		}
	}
	if !ok {
		return CleanDecision{Allowed: false, Reason: reason}
	}
	return CleanDecision{Allowed: true}
}

// Clean removes the space Execute created for in, once CleanCheck has
// allowed it (spec §9's "clean" choice). It removes ONLY what herdr-draft
// itself created: live probing (Task 2b) found that `herdr worktree
// create`, run from a repo with no workspace already open, also opens an
// implicit origin-repo workspace as a side effect. Clean deliberately does
// not touch that implicit workspace -- closing it is out of scope for v1
// and risks destroying state the user, not herdr-draft, created.
func Clean(ctx context.Context, r herdrc.Runner, in Input, created herdrc.CreatedTopology) error {
	if in.UseWorktree {
		if err := r.WorktreeRemove(ctx, created.WorkspaceID); err != nil {
			return fmt.Errorf("plan: clean: remove worktree: %w", err)
		}
		return nil
	}
	if err := r.WorkspaceClose(ctx, created.WorkspaceID); err != nil {
		return fmt.Errorf("plan: clean: close workspace: %w", err)
	}
	return nil
}
