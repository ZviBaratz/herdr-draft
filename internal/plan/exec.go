// exec.go executes a creation plan (Task 12's Build output) against a
// herdrc.Runner, threading each op's step-1 output into later ops, and
// implements the keep-or-clean gate that runs after a plan finishes
// (spec §9).
package plan

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

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
	// StepWaiting is a step that has stopped making progress on its own
	// and is waiting on the PERSON, not on herdr: the launch found the
	// agent blocked on a dialog only they can answer (#115). It is a fifth
	// state rather than a variant of StepRunning because the two differ in
	// whose turn it is, which is the only thing a user watching a pipeline
	// stall actually needs to know.
	//
	// Appended rather than inserted in state order: StepPending must stay
	// the zero value, since that is what an unstarted row is seeded with.
	StepWaiting
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
	case StepWaiting:
		return "StepWaiting"
	default:
		return fmt.Sprintf("StepState(%d)", int(s))
	}
}

// ExecOpts are Execute's RUNTIME knobs -- the settings that describe the
// caller rather than the plan.
//
// Deliberately a separate parameter and not a field on Input. Input is what
// Build maps to ops, and internal/create's equivalence_test.go compares the
// form's Input against the headless command's field for field with
// reflect.DeepEqual: anything the two paths must disagree about therefore
// cannot live there. TrustWait is exactly such a thing (#115's decision 2 --
// only the popup waits), and putting it here is what lets the two paths
// differ without the drift test losing its teeth.
type ExecOpts struct {
	// TrustWait is how long to wait for a person to answer a blocking
	// dialog that stopped the launch -- `[timeouts] trust_wait_ms`, five
	// minutes by default.
	//
	// Zero means do not wait at all, which is byte-for-byte the behaviour
	// that shipped before #115: the blocked launch fails, the prompt comes
	// back for manual paste, and the keep-or-clean gate is offered. That is
	// what headless `create` passes, because a script has nobody at the
	// keyboard and a five-minute wait for a dialog nobody will answer is
	// worse than failing with the reason.
	TrustWait time.Duration
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
// AgentAt is where the launch ops actually put the agent -- workspace, tab
// and pane -- and is equal to Created unless a placement op (§5.3) or a
// reuse correction (§5.2) moved it; nil until a topology op has succeeded.
// It is a whole topology rather than a pane id because a placement op can
// move all three at once: placementOp always targets Input.Ctx, so a
// worktree plus `tab here` leaves the agent in the INVOKING workspace
// while Created names the checkout's brand-new one (#99).
// FailedIndex is the index of the first op that failed, or -1
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
	AgentAt     *herdrc.CreatedTopology
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

	// PromptUnconfirmed reports that the prompt op failed with herdrc's
	// ErrPromptWaitTimeout -- `herdr agent prompt --wait` gave up waiting
	// for the agent's status to change -- so whether the prompt arrived is
	// UNKNOWN rather than known to be false (#108).
	//
	// It never travels alone: it is only ever set on a failed prompt op,
	// which is exactly the case that also sets PromptText, so
	// PromptUnconfirmed implies PromptText != "". What it changes is what
	// callers may say and do about that text:
	//
	//   - it must not be labelled "unsent". The field means "this is the
	//     one piece of your work this failure destroyed, here it is back",
	//     and a caller that does the documented thing with it -- paste it
	//     into the pane -- sends a working agent its instructions twice.
	//   - CleanCheck refuses a clean outright. `--on-failure clean` is the
	//     documented way to avoid litter from a failed create, and on this
	//     failure it would destroy an agent that is mid-turn.
	//
	// Deliberately NOT a "did it arrive?" heuristic. Nothing available
	// here can answer that: a large prompt is wrapped and scrolled by the
	// agent's own TUI so it appears verbatim nowhere on the screen, and
	// `agent get`'s status cannot answer it in either direction because a
	// short successful turn is already idle by the time anyone looks
	// (docs/manual-smoke.md's Cell 10, learned by running it). A guess
	// here would be wrong in the direction that DESTROYS the prompt --
	// suppressing the text on a run that really failed to deliver -- so
	// this reports what is known and leaves the pane to the human.
	PromptUnconfirmed bool
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

// isBlockedAgentError reports an agent that is running but waiting on
// something interactive, however that fact reached us. The two launch paths
// learn it differently and neither spelling is available on the other:
//
//   - Path A: `herdr agent start` refuses with the agent_not_ready CODE, in
//     stderr text (isAgentNotReadyError).
//   - Path B: there is no server-side wait to refuse at all, so
//     herdrc.AwaitDetection decides it here, from herdr's own agent JSON,
//     and says so with a typed sentinel (#94).
//
// Both mean the same thing to a user, so both get the same explanation.
func isBlockedAgentError(err error) bool {
	return isAgentNotReadyError(err) || errors.Is(err, herdrc.ErrAgentBlocked)
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

// errAgentOnDialog marks the one promptIfReady refusal that is worth
// WAITING on rather than failing: the pane is showing a dialog somebody can
// answer.
//
// A typed sentinel for the reason CLAUDE.md's "a generic error code cannot
// be substring-matched" records: this error's own text embeds the matched
// signature, and the caller must be able to tell it apart from the OTHER
// refusal in promptIfReady -- a pane that cannot be read at all -- which is
// equally a refusal and not waitable, because waiting cannot make a screen
// legible.
//
// Its message is deliberately the exact prefix promptIfReady always
// produced, so the sentence a user sees is byte-for-byte what it was.
var errAgentOnDialog = errors.New("agent is waiting on a dialog")

// errPaneUnpainted reports a pane whose detection screen came back EMPTY:
// readable, answered, and carrying nothing at all.
//
// The guard used to ask only "does this screen match a dialog signature?",
// so a screen with no text on it answered no and was treated as safe to
// type into. A blank detection buffer is not evidence that a pane is ready
// -- it is evidence there is nothing yet to judge.
//
// Read the limit before relying on this, because #116's own window is
// NOT blank and this rule does not close it. Measured on 2026-09-10 by
// polling `agent read` through four real launches into an untrusted
// directory (docs/manual-smoke.md): the read fails outright for the first
// ~300-500ms, which the guard already refused, and then comes back
// SUCCESSFULLY carrying the shell's echo of the launch command --
// "\x1b[200~claude\x1b[201~", then "❯ claude", then clauth's account
// banner -- for a further second before the dialog paints at ~2s. Twenty
// non-space characters, no signature, and this check passes it. What
// actually covers that window is confirmPromptLanded, one step later.
//
// So this stays as the cheap half of a positive rule, and its own claim is
// the narrow one: a read that succeeds with nothing at all in it has told
// us nothing, and a machine or an agent that produces one must not have a
// prompt typed into it on the strength of a signature that had nothing to
// match against. See TestPromptGuardOnTheMeasuredStartupWindow, which pins
// the measured bytes so nobody re-derives the wrong conclusion from this
// sentinel's existence.
//
// A separate sentinel rather than a reuse of errAgentOnDialog, because the
// two are different claims about the world: one screen showed a dialog,
// the other showed nothing. What they share is only what a caller should
// do next, and isUnsafeScreenError is where that is said once.
var errPaneUnpainted = errors.New("the agent's screen has not painted yet, so it is not safe to type into")

// errPromptSwallowed reports a send that herdr called successful and that
// the pane says did not land: still on a dialog, with no trace of the
// prompt text anywhere on it.
//
// Deliberately NOT matched by isUnsafeScreenError, and that exclusion is
// load-bearing rather than an oversight. Every other screen refusal in
// this file happens BEFORE any text is sent, which is what makes waiting
// and trying again safe. This one happens after, so a retry would be a
// second copy of the prompt into an agent that may have taken the first --
// the injury #108 exists to prevent, arriving through the front door. The
// post-send check reports; it never resends.
var errPromptSwallowed = errors.New("the prompt did not reach the agent")

// isUnsafeScreenError reports whether err is one of promptIfReady's
// BEFORE-the-send refusals -- the two states a person at the keyboard can
// resolve, and that #115's prompt-step wait therefore waits out.
//
// One predicate rather than two errors.Is calls at the call site because
// the set is a claim in its own right: these are the refusals it is safe
// to retry, because no text has been sent. errPromptSwallowed is the
// counter-example that makes the distinction worth naming.
func isUnsafeScreenError(err error) bool {
	return errors.Is(err, errAgentOnDialog) || errors.Is(err, errPaneUnpainted)
}

// promptRetrySettle is how long to wait before sending a stalled prompt
// again. A package var so tests can zero it, exactly as busyRetryInterval
// and dialogPollInterval beside it are.
//
// Two seconds because the window it covers is short and its cause is a TUI
// finishing its first paint: measured live, `agent get` reported the agent
// ready 0.5s after the trust dialog cleared and the send stalled, while the
// same agent took prompts normally moments later. Long enough to be past it,
// short enough that a user watching the popup does not read it as a hang.
var promptRetrySettle = 2 * time.Second

// dialogPollInterval is how often the prompt-step wait re-reads the pane
// looking for the dialog to clear. A package var rather than a parameter so
// tests can shrink it to zero and run the loop instantly, exactly as
// busyRetryInterval beside it does.
var dialogPollInterval = 500 * time.Millisecond

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
	if strings.TrimSpace(screen) == "" {
		// #116: the rule is positive now. A screen with nothing on it is
		// not one that failed to match a signature -- it is one there was
		// nothing to match against.
		return fmt.Errorf("%w -- prompt not sent", errPaneUnpainted)
	}
	if sig := blockingDialogSignature(screen); sig != "" {
		return fmt.Errorf("%w (%q) -- prompt not sent", errAgentOnDialog, sig)
	}
	if err := r.AgentPrompt(ctx, req); err != nil {
		return err
	}
	return confirmPromptLanded(ctx, r, req)
}

// promptVerifyReads is how many times confirmPromptLanded will try to read
// a pane before concluding the agent behind it has gone.
//
// A small fixed count rather than awaitDialogCleared's elapsed-time budget
// because this loop only ever runs when something is already wrong: on
// every ordinary send the first read succeeds and the other two never
// happen, so the cost of being generous here is paid by nobody. Three is
// enough to ride out a repaint and few enough that a genuinely dead agent
// is reported in well under a second.
const promptVerifyReads = 3

// confirmPromptLanded is #116's post-send half: proof that a send herdr
// called successful actually reached the agent.
//
// Nothing used to check. `agent prompt --wait` returning ok was the end of
// it, and measured live on 2026-09-09 that return value survived the
// prompt being swallowed by a dialog, its Enter answering "No, exit", and
// the agent exiting -- Execute reported FailedIndex == -1 over a dead
// pane, so the popup persisted its state, wrote no unsent-prompt.txt and
// closed. Prevention alone cannot fix that half: narrowing the window the
// pre-send guard misses does not make the report honest when it is missed
// anyway, and this is the only one of #116's three directions that catches
// a race nobody predicted.
//
// It turned out to be load-bearing for a second reason nobody expected.
// The startup window measured on 2026-09-10 (see errPaneUnpainted) is a
// screen carrying the shell's echo of the launch command: not blank, no
// signature, and so it passes the pre-send guard entirely. THIS is what
// covers it, and it covers both of its outcomes -- an agent the prompt
// killed stops answering `agent read` at all (verified live: the call
// returns agent_not_found within seconds of the agent exiting), and one
// that survives with the dialog still up carries a signature and no trace
// of the prompt.
//
// It reports; it NEVER resends. See errPromptSwallowed.
//
// Two verdicts, and they rest on different strength of evidence:
//
//   - A pane that cannot be read at all, three tries running, means the
//     agent is gone. Size-independent, no way to be wrong about it, and it
//     is what the observed failure actually looks like.
//   - A pane still showing a dialog with no trace of the prompt on it
//     means the dialog ate the text. Weaker, and gated by
//     promptIsVerifiable, because the absence of a dialog is NOT what
//     separates a swallowed prompt from a delivered one: "Enter to
//     confirm" is Claude Code's own permission-prompt footer, so an agent
//     that received the prompt and immediately asked to act on it is
//     sitting on a matching screen with the prompt delivered. Only the
//     prompt's own presence tells those two panes apart.
//
// A screen that reads back blank is deliberately a PASS. Post-send it is
// no longer the unpainted-startup state errPaneUnpainted refuses -- the
// pane answered a moment ago -- and this check only ever turns a success
// into a failure, so anything short of evidence has to leave it alone.
func confirmPromptLanded(ctx context.Context, r herdrc.Runner, req herdrc.AgentPromptReq) error {
	screen, err := readAfterSend(ctx, r, req.Target)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("could not confirm the prompt reached the agent: %w", ctxErr)
		}
		return explainPromptKilledAgent(err)
	}
	sig := blockingDialogSignature(screen)
	if sig == "" {
		return nil
	}
	if !promptIsVerifiable(req.Text) || promptOnScreen(screen, req.Text) {
		return nil
	}
	return fmt.Errorf("keep this session, answer the dialog in the pane, then paste the prompt -- "+
		"the agent is showing %q and none of the prompt reached it: %w", sig, errPromptSwallowed)
}

// readAfterSend reads paneID, retrying a failed read up to
// promptVerifyReads times, and returns the last error if every attempt
// failed.
//
// One failed read is not evidence of anything: a pane can be briefly
// unreadable mid-repaint, and a send is exactly when a repaint is likely.
// It is a RUN of failures that carries the conclusion, which is the same
// distinction awaitDialogCleared draws with its detection budget and
// herdrc.AwaitDetection draws from `agent get` falling silent.
func readAfterSend(ctx context.Context, r herdrc.Runner, paneID string) (string, error) {
	var err error
	for i := 0; i < promptVerifyReads; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(dialogPollInterval):
			}
		}
		var screen string
		if screen, err = r.AgentRead(ctx, paneID); err == nil {
			return screen, nil
		}
	}
	return "", err
}

// promptTraceMaxRunes and promptTraceMaxLines bound the prompts
// confirmPromptLanded is willing to look for on a screen.
//
// They exist because ExecResult.PromptUnconfirmed's doc comment already
// recorded the finding that makes an unbounded version of this check
// wrong: a large prompt is wrapped and scrolled by the agent's own TUI, so
// it appears verbatim nowhere -- measured on a 6311-byte, 120-line prompt
// (docs/manual-smoke.md's Cell 10). Searching for one of those and not
// finding it is not evidence of anything, and acting on it would tell a
// user to re-paste a prompt an agent is already working on.
//
// So the trace test applies only to a prompt small enough that a delivered
// copy would still be on the screen next to the dialog, and a bigger one
// falls back to the read-failure verdict alone. A screenful is the honest
// bound; these are its two dimensions, deliberately generous in neither.
const (
	promptTraceMaxRunes = 400
	promptTraceMaxLines = 10
)

// promptIsVerifiable reports whether text is small enough that its absence
// from a screen means something.
func promptIsVerifiable(text string) bool {
	return len([]rune(text)) <= promptTraceMaxRunes &&
		strings.Count(text, "\n") < promptTraceMaxLines
}

// promptProbeRunes is how much of a prompt has to be found for it to count
// as present. Long enough not to collide with the dialog's own prose,
// short enough to survive the agent's TUI reflowing the turn it landed in.
const promptProbeRunes = 24

// promptOnScreen reports whether screen carries a trace of text.
//
// Both sides have their whitespace REMOVED rather than normalised, which
// is what makes the match survive the pane wrapping a line: a turn broken
// across a column boundary can split a word without leaving a space
// behind, so collapsing runs to a single space would not put it back
// together and dropping them entirely does.
//
// Its errors run one way on purpose. A probe short enough to collide with
// unrelated text makes this return true for a prompt that never landed --
// a missed detection, which is where this check started. Nothing here can
// make it return false for a prompt that did.
func promptOnScreen(screen, text string) bool {
	probe := []rune(squeezeSpace(text))
	if len(probe) == 0 {
		return true
	}
	if len(probe) > promptProbeRunes {
		probe = probe[:promptProbeRunes]
	}
	return strings.Contains(squeezeSpace(screen), string(probe))
}

// squeezeSpace returns s with every whitespace rune removed.
func squeezeSpace(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

// waitThroughDialog turns a blocked launch into a wait for the person at
// the keyboard, and is the whole of #115.
//
// Both launch paths reach it holding the same fact by different routes --
// Path A from `agent start`'s own agent_not_ready refusal, Path B from
// herdrc's ErrAgentBlocked sentinel -- and neither needs a second `agent
// start`: a blocked agent is running and addressable throughout, recorded
// live in docs/manual-smoke.md's Cell 8. herdr refuses to CALL the launch
// ready; it does not kill anything. So this is a poll.
//
// It emits StepWaiting first, because a wait nobody can see is a hang, and
// because with a `here` placement herdr has already moved the user to the
// agent's pane (placementOp's Focus: true) -- they answer the dialog there
// while the popup polls behind it and closes itself. Zero keypresses in the
// popup is the point, so nothing here asks for one.
//
// The error it returns is deliberately left for the CALLER's own explain
// switch, which already knows how to turn a blocked verdict into an
// instruction; the one case it handles itself is the agent going away,
// since "answer the dialog in the pane" would then be advice about a pane
// with nothing in it.
func waitThroughDialog(ctx context.Context, r herdrc.Runner, onProgress func(Progress),
	index, total int, label, paneID string, detection, trustWait time.Duration) error {
	emitProgress(onProgress, index, total, label, StepWaiting, nil)
	err := r.AwaitDetection(ctx, paneID, detection, trustWait)
	if errors.Is(err, herdrc.ErrAgentGone) {
		return explainAbandonedDialog(err)
	}
	return err
}

// waitThroughPromptDialog is #115's second trigger, and the one a user
// actually meets.
//
// Live on herdr 0.9.0 `agent start` frequently returns OK while the trust
// dialog is up -- herdr's readiness verdict lands before it classifies that
// screen -- so the launch step succeeds and the refusal arrives one step
// later, from promptIfReady's own guard (docs/manual-smoke.md, 2026-09-09).
// The launch-step wait never runs on that path, and without this one the
// user is back to hand-pasting a prompt they already typed, which is the
// whole complaint #115 exists for.
//
// It waits on the SCREEN, not on herdr's status, and that is the load-bearing
// difference from waitThroughDialog. herdr is the thing that was wrong about
// this pane: polling `agent get` here would return "ready" immediately and
// send the prompt straight back into the dialog, which is exactly the
// accident #94 exists to prevent. dialog.go's signatures are the evidence
// promptIfReady already trusts, so the wait trusts the same evidence.
//
// Once the screen is clear it still defers to herdr for readiness, with no
// blocked budget: the dialog is gone, so a blocked verdict now means a
// DIFFERENT dialog, which is a refusal and not another wait.
func waitThroughPromptDialog(ctx context.Context, r herdrc.Runner, onProgress func(Progress),
	index, total int, label, paneID string, detection, trustWait time.Duration) error {
	emitProgress(onProgress, index, total, label, StepWaiting, nil)
	if err := awaitDialogCleared(ctx, r, paneID, detection, trustWait); err != nil {
		return err
	}
	return r.AwaitDetection(ctx, paneID, detection, 0)
}

// awaitDialogCleared polls paneID's screen until no dialog.go signature
// matches it, and reports why it stopped otherwise.
//
// A read that FAILS is not treated as "cleared" -- that would send a prompt
// into a pane nobody can see, which is the whole hazard -- but it is not
// fatal either, since a pane can be briefly unreadable mid-repaint. Reads
// that keep failing for a full detection budget mean the agent behind them
// has gone, the same conclusion herdrc.AwaitDetection draws from `agent get`
// falling silent, and deliberately the same sentence: a user does not care
// which step noticed that their "No, exit" took effect.
func awaitDialogCleared(ctx context.Context, r herdrc.Runner, paneID string, detection, trustWait time.Duration) error {
	start := time.Now()
	deadline := start.Add(trustWait)
	// lastSeen is the most recent moment the pane could be read at all; a
	// run of unreadable polls longer than the detection budget is what
	// separates "repainting" from "gone".
	lastSeen := start
	lastSig := ""

	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("waiting for the dialog in pane %s to be answered: %w", paneID, err)
		}
		screen, err := r.AgentRead(ctx, paneID)
		switch {
		case err != nil:
			if time.Since(lastSeen) >= detection {
				return explainAbandonedDialog(fmt.Errorf("%w: %w", herdrc.ErrAgentGone, err))
			}
		default:
			lastSeen = time.Now()
			if strings.TrimSpace(screen) == "" {
				// #116: a screen with nothing on it is not a cleared one.
				// The dialog this loop waits for spends its first moments
				// looking exactly like this, so returning here would hand
				// promptIfReady back the very pane the wait was entered
				// for -- and its own refusal is what got us here, so the
				// two would agree to send the prompt into an unpainted
				// pane one retry later.
				break
			}
			sig := blockingDialogSignature(screen)
			if sig == "" {
				return nil
			}
			lastSig = sig
		}

		now := time.Now()
		if !now.Before(deadline) {
			// The instruction first, herdr's own detail last: at an 80-cell
			// popup only the opening clause survives (v2 spec §7's
			// head-keeping truncation), and the action is what the user with
			// the narrowest window still has to be told.
			if lastSig == "" {
				// Readable throughout and never once carrying anything.
				// There is no dialog to tell anyone to answer, so the
				// instruction cannot be the one below it.
				return fmt.Errorf("keep this session and paste the prompt into the pane yourself -- "+
					"the agent's screen was still blank after %s: %w", trustWait, errPaneUnpainted)
			}
			return fmt.Errorf("answer the dialog in the pane, then keep this session and paste the prompt -- "+
				"it is still showing %q after %s: %w", lastSig, trustWait, errAgentOnDialog)
		}
		wait := dialogPollInterval
		if remaining := deadline.Sub(now); wait > remaining {
			wait = remaining
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for the dialog in pane %s to be answered: %w", paneID, ctx.Err())
		case <-time.After(wait):
		}
	}
}

// explainAbandonedDialog says what an ErrAgentGone actually means to
// somebody watching the popup: the session they were being asked about is
// over. herdrc's own sentence describes the evidence ("stopped being
// reported"), which is the right thing for it to say and the wrong thing to
// put on a failure row.
//
// It names the likeliest cause without asserting it, because the evidence
// cannot distinguish a declined dialog from a crash -- see ErrAgentGone's
// own doc comment for why `agent get` can never tell them apart -- and the
// action ("start it again") is the same either way.
func explainAbandonedDialog(err error) error {
	return fmt.Errorf("the agent is no longer running -- if you answered \"No, exit\" that is why; "+
		"nothing was sent, and this session can be removed and started again: %w", err)
}

// explainPromptKilledAgent says what a pane that stopped being readable
// immediately after a send actually means.
//
// Deliberately not explainAbandonedDialog beside it, whose "nothing was
// sent" is the one thing that is NOT true here: a prompt went out, and it
// is the likeliest reason the agent is gone. The distinction matters to
// the user, because it is the difference between a session that failed and
// one their own composed text destroyed -- and it is the sentence that
// tells them to look at a pane the popup would otherwise have called
// clean. The action is the same either way, so it comes last.
func explainPromptKilledAgent(err error) error {
	return fmt.Errorf("the agent exited as the prompt was sent -- most likely a dialog that had not "+
		"painted yet, answered by the prompt's own Enter; nothing was delivered, and this session "+
		"can be removed and started again: %w", err)
}

// explainStalledPrompt says what is left after a prompt stalled TWICE.
//
// One stall is evidence the agent was not accepting input yet, and Execute
// answers it by trying again. Two is where that evidence stops carrying the
// conclusion: something is wrong that another attempt will not fix, and two
// sends have now been made, so "the prompt was not sent" is a stronger claim
// than what is known. The instruction is therefore the same one #108
// established for the opposite sentinel -- read the pane before pasting --
// because the failure mode that costs the user most here is a double-paste
// into an agent that did eventually receive it.
func explainStalledPrompt(err error) error {
	return fmt.Errorf("the agent was still not accepting input, so the prompt was probably not delivered -- "+
		"read the pane before pasting it, in case a copy arrived late: %w", err)
}

// explainUnconfirmedPrompt rewrites a prompt-wait timeout into the only
// conclusion the evidence supports, and into an instruction.
//
// herdr's own message is "timed out waiting for agent status", which is
// true and reads as "the prompt failed". The step's old wording then
// compounded it -- "the prompt was not sent" -- for a prompt that in the
// live #108 case had been delivered and was already several tool calls
// deep. So the text says three things: what actually timed out, that
// delivery is unknown rather than failed, and what to do instead of
// resending (read the pane). The sentinel is wrapped rather than replaced
// so `errors.Is` still classifies it downstream.
func explainUnconfirmedPrompt(err error) error {
	return fmt.Errorf("the prompt was handed to herdr, but the wait for the agent to change "+
		"status timed out, so delivery could not be confirmed -- read the pane before "+
		"resending it, since the agent may already be working on it: %w", err)
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
	if !isBlockedAgentError(err) || paneID == "" {
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
func Execute(ctx context.Context, r herdrc.Runner, ops []Op, opts ExecOpts, onProgress func(Progress)) ExecResult {
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
		var claimed *herdrc.CreatedTopology

		runErr := retryBusy(ctx, func() error {
			// Reset per ATTEMPT, not per op: retryBusy may re-run this closure,
			// and state a first attempt reached must not leak into a second one
			// that fails earlier -- a stale reused=true would give CleanCheck a
			// false SpaceReused and make it refuse to clean a space nobody else
			// owned. Unreachable today, since retryBusy only retries
			// agent_pane_busy and none of the three calls in OpWorktreeCreate
			// can return it, which is exactly why it is worth three lines rather
			// than a reader's trust.
			gotTopo, reused, claimed = false, false, nil
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
						claimed = &claim
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
				if opts.TrustWait > 0 && isBlockedAgentError(err) {
					// Not a failure but a wait (#115). If it comes back
					// blocked anyway -- the budget ran out with the dialog
					// still up -- explainBlockedStart below turns THAT
					// error into the instruction instead, and herdr's own
					// agent_not_ready envelope drops out of the message: by
					// then the interesting fact is that five minutes passed
					// unanswered, not that the launch was refused.
					err = waitThroughDialog(ctx, r, onProgress, i, total, op.Label, req.PaneID, op.Timeout, opts.TrustWait)
				}
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
				err = r.PaneRun(ctx, paneID, op.RunEnv, op.RunArgv)
			case OpAwaitDetection:
				paneID := ""
				if haveAgentPane {
					paneID = agentPane
				}
				// The first poll deliberately runs with NO blocked budget,
				// so blocked still comes back as a verdict rather than
				// being absorbed silently inside herdrc. That is what lets
				// this path emit StepWaiting at the same moment Path A does
				// -- herdrc has no way to report "I have started waiting"
				// mid-call, and one extra `agent get` is a cheap price for
				// both paths running the same three lines.
				err = r.AwaitDetection(ctx, paneID, op.Timeout, 0)
				if opts.TrustWait > 0 && isBlockedAgentError(err) {
					err = waitThroughDialog(ctx, r, onProgress, i, total, op.Label, paneID, op.Timeout, opts.TrustWait)
				}
				switch {
				case err == nil:
				case isBlockedAgentError(err):
					// Path B's equivalent of #90's refused start: the agent
					// is up and waiting on a dialog, which is an instruction
					// to give rather than a timeout to report.
					err = explainBlockedStart(ctx, r, op.AgentKind, paneID, err)
				case errors.Is(err, herdrc.ErrAgentGone):
					// Already explained by waitThroughDialog, and the pane
					// tail below would only quote a shell prompt.
				default:
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
				if opts.TrustWait > 0 && isUnsafeScreenError(err) {
					// One retry, never a loop: the wait already ran until
					// the screen was clear, so a second refusal is a
					// different dialog and a real refusal.
					//
					// isUnsafeScreenError rather than errAgentOnDialog
					// alone because #116 gave the wait a second thing to
					// wait out -- a pane that has not painted -- and
					// deliberately excludes errPromptSwallowed, which is
					// the one refusal that arrives with text already sent.
					if waitErr := waitThroughPromptDialog(ctx, r, onProgress, i, total,
						op.Label, req.Target, op.Timeout, opts.TrustWait); waitErr != nil {
						err = waitErr
					} else {
						err = promptIfReady(ctx, r, req)
					}
				}
				// A stalled send means herdr saw the agent do NOTHING, which
				// is the one prompt failure that is positive evidence
				// nothing was delivered -- so it is the one worth sending
				// again. Deliberately not ErrPromptWaitTimeout beside it:
				// that one means the agent WAS working, and resending would
				// give a busy agent its instructions twice (#108).
				//
				// Exactly one extra attempt, and through promptIfReady, so
				// the guard gates the retry as it gates the first send and a
				// TUI that buffered the keystrokes it was handed while
				// starting cannot be fed the same prompt repeatedly.
				if opts.TrustWait > 0 && errors.Is(err, herdrc.ErrPromptStalled) {
					select {
					case <-ctx.Done():
					case <-time.After(promptRetrySettle):
					}
					err = promptIfReady(ctx, r, req)
				}
				switch {
				case errors.Is(err, herdrc.ErrPromptWaitTimeout):
					err = explainUnconfirmedPrompt(err)
				case errors.Is(err, herdrc.ErrPromptStalled):
					err = explainStalledPrompt(err)
				}
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
			// Set here rather than inside the retried closure above: a
			// busy-retry that eventually succeeds must not leave the flag
			// behind, and by this point runErr is the error that actually
			// ended the run.
			result.PromptUnconfirmed = errors.Is(runErr, herdrc.ErrPromptWaitTimeout)
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
				// The whole topology, not just the pane: a placement op
				// can move the agent's workspace and tab too, and a
				// caller that is handed a pane id alone cannot tell which
				// tab to address (#99).
				at := topo
				if claimed != nil {
					at = *claimed
				}
				agentPane = at.PaneID
				haveAgentPane = true
				result.AgentAt = &at
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
	// Checked before every other branch on purpose. The branches below ask
	// whether this SPACE is safe to remove -- uncommitted work, a workspace
	// somebody else opened. This one asks whether something is running in
	// it right now, which outranks all of them: on a prompt-wait timeout
	// the agent may be mid-turn on the very prompt that appeared to fail,
	// and `clean` would kill it (#108).
	if result.PromptUnconfirmed {
		return CleanDecision{
			Allowed: false,
			Reason: "the prompt may already have been delivered -- the wait for the agent's status " +
				"timed out, which is not proof it failed -- so this session may have an agent " +
				"working in it right now. Read the pane and remove it yourself if it really is idle.",
		}
	}
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

	if result.AgentAt != nil && result.AgentAt.PaneID != "" && result.AgentAt.PaneID != created.PaneID {
		if err := r.PaneClose(ctx, result.AgentAt.PaneID); err != nil {
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
