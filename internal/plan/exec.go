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

// ExecResult is Execute's outcome. Created is the SPACE -- the step-1
// topology Clean acts on -- and is nil until that op succeeds; its PaneID
// is no longer necessarily where the agent runs (placement spec §5.1).
// AgentPane is the pane the launch ops actually targeted: equal to
// Created.PaneID unless a placement op (§5.3) or a reuse correction (§5.2)
// moved it. FailedIndex is the index of the first op that failed, or -1
// on success. PromptText carries a prompt that never reached the agent, so
// the caller can surface it for manual paste (spec §9 step 3); it is empty
// on success and on any plan that carried no prompt at all.
//
// PromptText means "the prompt did not land", not "the prompt op failed".
// It used to mean the narrower thing -- set only when OpAgentPrompt itself
// was the failing op -- which quietly destroyed the prompt whenever the
// plan stopped BEFORE reaching it. That was a rare shape until herdr 0.9.0
// started refusing `agent start` against an untrusted directory (#90,
// explainBlockedStart), which made "fails at step 2 of three" the ordinary
// outcome of a submit into a fresh worktree; the user's composed prompt
// was the one thing it took with it. It also made `create --json` report
// `prompt_sent: true` for a run that never started an agent, since that
// field is derived from this one being empty.
type ExecResult struct {
	Created     *herdrc.CreatedTopology
	AgentPane   string
	FailedIndex int
	PromptText  string

	// SpaceReused reports that the worktree op's own workspace was
	// already open BEFORE Execute ran, rather than freshly created
	// (placement spec §5.2/§3) -- an exact fact, from a before/after
	// workspace-id set comparison, not an inference. SpaceLabel is that
	// workspace's own Label, for CleanCheck's refusal message (§5.4):
	// Clean must never remove a space the user, not herdr-draft, opened.
	SpaceReused bool
	SpaceLabel  string
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

// nameTakenErrorCode is the herdr error code for
// AgentStartError::DuplicateName (herdr:src/app/agents.rs:165 raises it,
// :266 encodes it): the requested agent name is already in use by a live
// agent. Unlike busyPaneErrorCode this is not a timing race -- the name is
// taken and stays taken -- so the fix is a different NAME, not a later
// attempt with the same one. See startAgentWithDedupe.
const nameTakenErrorCode = "agent_name_taken"

// agentNotReadyErrorCode is the herdr error code `agent start` raises when
// its readiness poll finds the agent it just launched sitting at
// `agent_status: "blocked"`
// (https://github.com/herdrdev/herdr/blob/b1ff4582/src/cli/agent.rs#L607).
// `agent start`'s contract is "the expected agent was detected in the same
// terminal and is ready for input", and there is no opt-out flag --
// `--timeout` only changes how long it waits -- so a blocked agent fails
// the step however healthy the process itself is.
//
// This is NOT the code `agent prompt` uses for the same condition: that one
// answers `agent_blocked`
// (https://github.com/herdrdev/herdr/blob/b1ff4582/src/app/api/agents.rs#L85).
// Two codes for one situation, on the two calls herdr-draft makes, is why
// this constant is named for the call that raises it rather than for the
// state it describes.
const agentNotReadyErrorCode = "agent_not_ready"

// maxAgentNameAttempts bounds startAgentWithDedupe: the caller's own name
// plus eight suffixed alternatives ("-2" through "-9", the two-rune
// suffixes build.go's maxAgentNameLen already reserves room for). Nine
// live sessions started from one title is already well past any plausible
// use; beyond that, failing with herdr's own real error is more honest
// than looping against a server that keeps saying no.
const maxAgentNameAttempts = 9

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

// isNameTakenError reports whether err's text contains nameTakenErrorCode,
// by the same substring match (and for the same reason) isBusyPaneError
// uses: herdrc.CLIRunner surfaces herdr CLI failures as plain text, not as
// a typed error code.
func isNameTakenError(err error) bool {
	return err != nil && strings.Contains(err.Error(), nameTakenErrorCode)
}

// isAgentNotReadyError reports whether err's text contains
// agentNotReadyErrorCode, by the same substring match (and for the same
// reason) isBusyPaneError uses.
func isAgentNotReadyError(err error) bool {
	return err != nil && strings.Contains(err.Error(), agentNotReadyErrorCode)
}

// startAgentWithDedupe runs `herdr agent start` for req and, when herdr
// rejects the name as already taken, retries with a numeric dedupe suffix
// -- spec §9's "Name = title slug (deduped; DuplicateName retried with
// suffix)", the retry plan.AgentName's own two reserved runes were always
// for and which nothing ever performed. Without it, opening a second
// session from the same Linear issue (or the same title) failed the whole
// submit at step 2 -- AFTER the worktree or workspace had already been
// created, leaving exactly the half-built mess spec §9's keep-or-clean
// gate exists to clean up.
//
// Every other error, including agent_pane_busy, is returned unchanged on
// the first attempt: busy is retried one level up by retryBusy (with the
// same name, which is the correct response to a pane whose shell is still
// starting), and this loop must not swallow it.
func startAgentWithDedupe(ctx context.Context, r herdrc.Runner, req herdrc.AgentStartReq) error {
	base := req.Name
	var err error
	for attempt := 1; attempt <= maxAgentNameAttempts; attempt++ {
		if attempt > 1 {
			req.Name = AgentNameWithSuffix(base, attempt)
		}
		err = r.AgentStart(ctx, req)
		if err == nil || !isNameTakenError(err) {
			return err
		}
		if ctx.Err() != nil {
			return err
		}
	}
	return fmt.Errorf("agent name %q is taken, and so were %d suffixed alternatives: %w",
		base, maxAgentNameAttempts-1, err)
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

// withPaneTail appends what the pane is actually showing to a detection
// failure, or returns err unchanged when there is nothing useful to add.
//
// A detection timeout used to report only that it had waited:
//
//	waiting for agent detection ... failed: await detection for pane w9:p1:
//	timed out after 30.001s
//
// while the pane held the answer for the whole thirty seconds. The case
// that prompted this was an extra_args value containing shell glob
// characters -- herdr's `pane run` TYPES a command into the pane's
// interactive shell rather than exec'ing an argv vector, so zsh expanded
// it and refused:
//
//	❯ clauth start personal -- --model claude-opus-5[1m] --effort xhigh
//	zsh: no matches found: claude-opus-5[1m]
//
// The agent never started, and `pane run` still reported success, because
// its exit code reflects the TYPING and not the command (runOK's contract).
// So the launch step cannot tell that anything went wrong -- but this step
// waits thirty seconds next to a pane that is displaying the reason, and
// then says nothing about it.
//
// Deliberately best-effort and last: the timeout is the failure, and this
// only enriches it. A read that fails, or returns nothing, leaves the
// original error exactly as it was rather than replacing a real diagnosis
// with a complaint about not being able to fetch one.
func withPaneTail(ctx context.Context, r herdrc.Runner, paneID string, err error) error {
	if paneID == "" {
		return err
	}
	screen, readErr := r.AgentRead(ctx, paneID)
	if readErr != nil {
		return err
	}
	return withScreenTail(screen, err)
}

// withScreenTail is withPaneTail's formatting half, split out so
// explainBlockedStart -- which has already read the screen for its own
// reasons -- can append the same tail without reading the pane a second
// time. Kept as one function rather than two copies of three lines: a
// duplicated body drifts, and the two callers must keep saying "the pane
// shows" the same way, since a reader who has seen one failure quote a
// pane should recognize the next.
func withScreenTail(screen string, err error) error {
	tail := lastNonBlankLines(screen, paneTailLines)
	if tail == "" {
		return err
	}
	return fmt.Errorf("%w; the pane shows: %s", err, tail)
}

// explainBlockedStart turns herdr's own `agent_not_ready` refusal into
// something the user can act on, and returns every other error untouched.
//
// herdr 0.9.0's detection manifest learned to recognize Claude Code's
// first-run trust prompt, so it now reports that screen as
// `agent_status: "blocked"` rather than the idle/interactive_ready it
// reported on 0.8.2 (see dialog.go's own header for what that used to
// cost). `agent start` polls for readiness and refuses a blocked agent
// outright, so on 0.9.0 a submit into any FRESH WORKTREE -- a path Claude
// Code has never been trusted in, which is every worktree the first time --
// fails at the launch step. Upstream is right to refuse; what it cannot
// know is that the agent is running fine and is one keystroke from ready,
// which makes "starting agent failed" a frightening message about a session
// that is healthy.
//
// So: on that one code, read the pane and say what is actually true. A
// known dialog signature (dialog.go) gets the specific instruction; any
// other blocked-at-startup screen gets quoted verbatim, which is the same
// service withPaneTail does for a detection timeout and is strictly better
// than a bare error code. A pane that cannot be read leaves herdr's error
// exactly as it was -- never replace a real diagnosis with a complaint
// about not being able to fetch one.
//
// The instruction is deliberately FIRST in the message and herdr's own
// error last. SubmitView.stepValue truncates a step's value at the head
// (v2 spec §7, "keeping the informative end"), so at an 80-cell popup only
// the opening clause survives on screen -- while `create`'s stderr and
// `--json` carry the whole thing, herdr's error code included. The action
// belongs where the user with the narrowest window will still see it.
//
// kind is the agent kind being launched ("claude"), used only to name it;
// an empty kind falls back to "the agent" rather than opening the sentence
// with a space.
func explainBlockedStart(ctx context.Context, r herdrc.Runner, kind, paneID string, err error) error {
	if !isAgentNotReadyError(err) || paneID == "" {
		return err
	}
	screen, readErr := r.AgentRead(ctx, paneID)
	if readErr != nil {
		return err
	}
	if sig := blockingDialogSignature(screen); sig != "" {
		return fmt.Errorf("%s started; answer the dialog in the pane, then keep this session -- it is showing %q: %w",
			agentKindName(kind), sig, err)
	}
	return withScreenTail(screen, err)
}

// agentKindName names an agent kind in prose, falling back to "the agent"
// for an empty one. Build never emits an OpAgentStart with an empty Kind
// (Input.AgentKind is resolved before it), but a hand-built op can, and a
// sentence starting " started; answer the dialog" is worse than a generic
// one.
func agentKindName(kind string) string {
	if kind == "" {
		return "the agent"
	}
	return kind
}

// paneTailLines is how much of the pane a detection failure quotes. Three
// is enough for a shell rejection (the echoed command, the error, the new
// prompt) and small enough that the failure screen stays readable -- this
// text reaches a UI that surfaces it to the user, not a log.
const paneTailLines = 3

// lastNonBlankLines returns the last n non-blank lines of s joined by " | ",
// trimmed, or "" when there are none.
//
// Joined rather than kept multi-line because the caller's error string ends
// up on a failure screen that budgets its rows; a screen dump with embedded
// newlines would push that layout past the height it was asked for, which
// is the same hazard the Linear and clauth reasons flatten for.
func lastNonBlankLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	var kept []string
	for i := len(lines) - 1; i >= 0 && len(kept) < n; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		return ""
	}
	// kept was gathered bottom-up; put it back in reading order.
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	return strings.Join(kept, " | ")
}

// unsentPromptText returns the prompt text of an OpAgentPrompt in ops that
// did NOT reach the agent, given the index of the op that failed, or "" if
// there is no prompt op or it had already succeeded.
//
// "Did not reach the agent" is every prompt op at or after failedIdx: ops
// after it never ran at all, and the one AT it is the failing op itself --
// which promptIfReady may well have refused deliberately, without sending
// a byte (dialog.go's guard). Both cases owe the user their text back.
func unsentPromptText(ops []Op, failedIdx int) string {
	for i := failedIdx; i >= 0 && i < len(ops); i++ {
		if ops[i].Kind == OpAgentPrompt && ops[i].Prompt != nil {
			return ops[i].Prompt.Text
		}
	}
	return ""
}

// isTopologyKind reports whether kind is one of the four ops that can
// produce a herdrc.CreatedTopology (i.e. carries gotTopo=true in Execute's
// loop below) -- OpAgentStart and everything after it never do.
func isTopologyKind(kind OpKind) bool {
	switch kind {
	case OpWorktreeCreate, OpWorkspaceCreate, OpTabCreate, OpPaneSplit:
		return true
	default:
		return false
	}
}

// topologyIndices returns the index of the FIRST topology-producing op in
// ops (always 0 by Build's own construction -- topologyOp is always
// first) and the LAST one (placement spec §5.1's "space" and "agent
// pane": identical, at the same index, whenever Build appended no
// placement op after a worktree create; two different indices when it
// did). ops with no topology-producing op at all (which Build never
// actually returns, but Execute must not panic against a hand-built one)
// answers (-1, -1).
func topologyIndices(ops []Op) (space, agentPane int) {
	space, agentPane = -1, -1
	for i, op := range ops {
		if !isTopologyKind(op.Kind) {
			continue
		}
		if space == -1 {
			space = i
		}
		agentPane = i
	}
	return space, agentPane
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
	spaceIdx, agentPaneIdx := topologyIndices(ops)

	var created herdrc.CreatedTopology
	haveCreated := false
	var agentPane string
	haveAgentPane := false
	total := len(ops)

	for i, op := range ops {
		emitProgress(onProgress, i, total, op.Label, StepRunning, nil)

		var topo herdrc.CreatedTopology
		gotTopo := false
		var reused bool
		var reusedLabel string
		var claimedPane string

		runErr := retryBusy(ctx, func() error {
			// Reset per ATTEMPT, not per op: retryBusy may re-run this closure,
			// and state a first attempt reached must not leak into a second one
			// that fails earlier -- a stale reused=true would give CleanCheck a
			// false SpaceReused and make it refuse to clean a space nobody else
			// owned. Unreachable today, since retryBusy only retries
			// agent_pane_busy and none of the three calls in OpWorktreeCreate
			// can return it, which is exactly why it is worth three lines rather
			// than a reader's trust.
			gotTopo, reused, claimedPane = false, false, ""
			reusedLabel = ""

			var err error
			switch op.Kind {
			case OpWorktreeCreate:
				if op.Worktree == nil {
					return malformedOpError(op.Kind)
				}
				// The reuse check runs UNCONDITIONALLY for every worktree
				// create, whether or not this op is the agent-pane op:
				// SpaceReused/SpaceLabel feed CleanCheck's refusal (§5.4)
				// regardless of where the agent ends up. The list call
				// goes BEFORE the create and its failure fails the step --
				// fail closed, before anything is half-built (§5.2).
				//
				// The before/after id-SET comparison is exact, not a
				// heuristic (design §3, §11 item 7): herdr allocates
				// workspace ids from a monotonic counter and documents
				// them as stable, never recycled (herdr:src/workspace.rs
				// ~line 179, "Stable public workspace identity, independent
				// of display order" -- cited here per the design's own
				// recommendation, so the next reader finds the guarantee
				// this relies on rather than re-deriving it). The only
				// false reading is a THIRD PARTY opening a workspace in
				// the sub-second window between this list call and the
				// create, and then that same create reusing it -- two
				// coincidences, and the failure is benign (one extra tab
				// in a workspace herdr had just made).
				before, listErr := r.WorkspaceList(ctx)
				if listErr != nil {
					return fmt.Errorf("checking which workspaces already exist: %w", listErr)
				}
				existed := make(map[string]bool, len(before))
				labelOf := make(map[string]string, len(before))
				for _, w := range before {
					existed[w.WorkspaceID] = true
					labelOf[w.WorkspaceID] = w.Label
				}

				topo, err = r.WorktreeCreate(ctx, *op.Worktree)
				if err != nil {
					return err
				}
				gotTopo = true

				if existed[topo.WorkspaceID] {
					reused = true
					reusedLabel = labelOf[topo.WorkspaceID]
					if i == agentPaneIdx {
						// The correction: a fresh TAB, not a split -- a
						// split changes the geometry of a tab the user
						// owns, where a tab only adds one entry to their
						// tab bar (§5.2). Fires only here: if a placement
						// op follows, IT decides where the agent runs, and
						// a claim in the reused workspace would be litter.
						claim, claimErr := r.TabCreate(ctx, herdrc.TabCreateReq{
							Workspace: topo.WorkspaceID,
							Cwd:       topo.CheckoutPath,
							Label:     op.Worktree.Label,
							Focus:     true,
						})
						if claimErr != nil {
							return claimErr
						}
						claimedPane = claim.PaneID
					}
				}
				return nil
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
				req := *op.Tab
				if op.CwdFromCheckout && haveCreated {
					req.Cwd = created.CheckoutPath
				}
				topo, err = r.TabCreate(ctx, req)
				gotTopo = err == nil
			case OpPaneSplit:
				if op.Split == nil {
					return malformedOpError(op.Kind)
				}
				req := *op.Split
				if op.CwdFromCheckout && haveCreated {
					req.Cwd = created.CheckoutPath
				}
				topo, err = r.PaneSplit(ctx, req)
				gotTopo = err == nil
			case OpAgentStart:
				if op.Agent == nil {
					return malformedOpError(op.Kind)
				}
				req := *op.Agent
				if req.PaneID == "" && haveAgentPane {
					req.PaneID = agentPane
				}
				err = startAgentWithDedupe(ctx, r, req)
				// Runs AFTER the dedupe loop, so it sees the error the step
				// actually failed with rather than an intermediate
				// agent_name_taken, and returns everything that is not
				// agent_not_ready untouched -- including agent_pane_busy,
				// which retryBusy one level up still has to recognize.
				err = explainBlockedStart(ctx, r, req.Kind, req.PaneID, err)
			case OpClauthLaunch:
				paneID := ""
				if haveAgentPane {
					paneID = agentPane
				}
				err = r.PaneRun(ctx, paneID, op.RunArgv)
			case OpAwaitDetection:
				paneID := ""
				if haveAgentPane {
					paneID = agentPane
				}
				err = r.AwaitDetection(ctx, paneID, op.Timeout)
				if err != nil {
					err = withPaneTail(ctx, r, paneID, err)
				}
			case OpAgentPrompt:
				if op.Prompt == nil {
					return malformedOpError(op.Kind)
				}
				req := *op.Prompt
				if req.Target == "" && haveAgentPane {
					req.Target = agentPane
				}
				err = promptIfReady(ctx, r, req)
			default:
				err = fmt.Errorf("plan: execute: unknown op kind %v", op.Kind)
			}
			return err
		})

		if runErr != nil {
			// A step that failed AFTER its own space op already succeeded
			// still leaves a space behind to account for: §5.2's reuse
			// correction runs INSIDE the worktree step, so `worktree create`
			// can have mutated real herdr state -- a checkout on disk, a
			// workspace holding it -- before the claim that follows it
			// failed. Recording the space here is what gets the
			// keep-or-clean gate offered at all: handleSubmitDone treats
			// Created == nil as "nothing to clean", which is the right
			// answer for §5.2's fail-closed list call (nothing was built
			// yet) and the wrong one for this case.
			if gotTopo && i == spaceIdx {
				c := topo
				result.Created = &c
				result.SpaceReused = reused
				result.SpaceLabel = reusedLabel
			}
			wrapped := fmt.Errorf("plan: execute: %s: %w", op.Label, runErr)
			emitProgress(onProgress, i, total, op.Label, StepFailed, wrapped)
			result.FailedIndex = i
			result.PromptText = unsentPromptText(ops, i)
			return result
		}

		if gotTopo {
			if i == spaceIdx {
				created = topo
				haveCreated = true
				c := topo
				result.Created = &c
				if reused {
					result.SpaceReused = true
					result.SpaceLabel = reusedLabel
				}
			}
			if i == agentPaneIdx {
				if reused {
					agentPane = claimedPane
				} else {
					agentPane = topo.PaneID
				}
				haveAgentPane = true
				result.AgentPane = agentPane
			}
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
// created (spec §9's keep-or-clean gate). A REUSED space (placement spec
// §5.2's SpaceReused) is refused OUTRIGHT and before anything else: it is
// a workspace the user, not herdr-draft, opened, and "clean" closing it --
// every tab, every pane -- is not a clean, whatever the checkout inside
// it looks like. Only once that is ruled out does a worktree space defer
// to gitx.Disposable, exactly as before. Non-worktree spaces (a plain
// workspace/tab/pane) have no on-disk checkout to lose and no reuse
// hazard either (only OpWorktreeCreate can silently reuse something) --
// only pane content herdr-draft itself created -- so they are always
// allowed.
func CleanCheck(ctx context.Context, in Input, result ExecResult) CleanDecision {
	if result.SpaceReused {
		checkout := ""
		if result.Created != nil {
			checkout = result.Created.CheckoutPath
		}
		// The instruction is `git worktree remove`, not `herdr worktree
		// remove`: the latter is exactly the command that would close the
		// reused workspace (design §8.2 -- herdr stamps worktree tracking
		// onto a reused space same as a created one). Design §11 item 4
		// asks for the exact command in the reason string, not just the
		// path, since it is longer than any reason this gate currently
		// shows and worth the width.
		return CleanDecision{
			Allowed: false,
			Reason: fmt.Sprintf(
				"this session joined %q, a workspace that was already open -- removing the worktree would close it and everything in it. "+
					"The checkout at %s is left in place; remove it yourself with `git worktree remove %s` from the origin repository if you no longer want it.",
				result.SpaceLabel, checkout, checkout,
			),
		}
	}
	if !in.UseWorktree {
		return CleanDecision{Allowed: true}
	}

	var checkout string
	if result.Created != nil {
		checkout = result.Created.CheckoutPath
	}

	base, err := resolveBaseRef(ctx, in)
	if err != nil {
		return CleanDecision{
			Allowed: false,
			Reason:  fmt.Sprintf("could not determine what this worktree branched from: %v", err),
		}
	}

	ok, reason, err := gitx.Disposable(ctx, checkout, base)
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

// resolveBaseRef turns Input.BaseRef into something gitx.Disposable can
// actually count against.
//
// The form's base picker reports "" for its row-0 HEAD entry
// (WorktreeField.Base()'s own documented "" == HEAD contract), and
// `herdr worktree create` is likewise called with no --base in that case,
// branching the new worktree from the ORIGIN repo's HEAD. Handing that ""
// to gitx.Disposable produced `git rev-list --count ..HEAD`, which git
// reads as HEAD..HEAD: zero, always, for every worktree -- so the
// "no commits beyond base" half of spec §9's clean gate never once
// refused a worktree carrying real work. That was the default case: HEAD
// is row 0 of the picker, so a user who never touched the base field hit
// it every time.
//
// The sentinel is resolved against Input.ProjectDir -- the repo the
// worktree was created from -- to the commit its HEAD names now.
// Disclosed approximation: "now" is when the keep-or-clean prompt is being
// prepared, seconds after creation, not the instant of creation itself, so
// a commit landing on the origin repo's HEAD in that window would shift
// the base. Capturing the commit at creation time instead would mean
// threading a resolved base through Build, Op, Execute and ExecResult for
// a race no interactive submit can realistically hit; the trade is
// deliberate. A non-empty BaseRef (any other picker row) is used as-is:
// git resolves it in the worktree, which shares the origin repo's object
// store.
func resolveBaseRef(ctx context.Context, in Input) (string, error) {
	if in.BaseRef != "" {
		return in.BaseRef, nil
	}
	if in.ProjectDir == "" {
		return "", fmt.Errorf("no project directory to resolve HEAD in")
	}
	return gitx.ResolveRef(ctx, in.ProjectDir, "HEAD")
}

// Clean removes the space Execute created for in, once CleanCheck has
// allowed it (spec §9's "clean" choice; placement spec §5.4 -- CleanCheck
// never allows a reused space, so Clean itself does not need to re-check
// that here). It removes ONLY what herdr-draft itself created: live
// probing (Task 2b) found that `herdr worktree create`, run from a repo
// with no workspace already open, also opens an implicit origin-repo
// workspace as a side effect. Clean deliberately does not touch that
// implicit workspace -- closing it is out of scope for v1 and risks
// destroying state the user, not herdr-draft, created.
//
// When Execute claimed a pane for the agent that differs from the space's
// own (a reuse correction, or a placement op moving the agent elsewhere --
// placement spec §5.1/§5.2/§5.3), that claimed pane is closed FIRST, so
// nothing is left running an agent in a directory about to be deleted.
func Clean(ctx context.Context, r herdrc.Runner, in Input, result ExecResult) error {
	if result.Created == nil {
		return fmt.Errorf("plan: clean: nothing was created")
	}
	created := *result.Created

	if result.AgentPane != "" && result.AgentPane != created.PaneID {
		if err := r.PaneClose(ctx, result.AgentPane); err != nil {
			return fmt.Errorf("plan: clean: close agent pane: %w", err)
		}
	}

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
