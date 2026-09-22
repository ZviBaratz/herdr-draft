package picker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Options are the protocol's two optional flags.
type Options struct {
	// Strict asks for headless semantics -- no interactive fallback, no
	// waiting for a person. `create` sets it; the popup does not.
	Strict bool
	// DryRun asks the picker not to write its ledger and not to build an
	// account directory, so config_dir comes back null. The popup's live
	// preview sets it; the commit-time pick must not, or two sessions opened
	// a second apart would both be handed the same account.
	DryRun bool
}

// Source is the picker access herdr-draft depends on, so internal/app and
// internal/create can be tested against a fake and never run a subprocess.
type Source interface {
	Pick(ctx context.Context, dir string, opts Options) (Result, error)
}

// Argv is the documented invocation, exported because it is the contract's
// most quotable half and docs/account-picker.md and the tests should read
// one definition of it.
func Argv(bin, dir string, opts Options) []string {
	argv := []string{bin, "--dir", dir, "--json"}
	if opts.Strict {
		argv = append(argv, "--strict")
	}
	if opts.DryRun {
		argv = append(argv, "--dry-run")
	}
	return argv
}

// CLI runs a picker executable. Bin is what `[clauth] picker` named -- a
// command name resolved on PATH, or an absolute path.
//
// There is deliberately no "find a picker on PATH" constructor. Finding *a*
// program with the name a user configured is not finding *this* one, and a
// plugin that silently adopted a same-named stranger to route account
// credentials would be worse than a plugin with no picker at all. Bin comes
// from config, and Probe checks it before anything trusts it.
type CLI struct{ Bin string }

var _ Source = CLI{}

// Pick runs one pick and returns its answer.
//
// Three outcomes, kept distinct because callers act differently on each: a
// Result; a *RefusalError (the picker ran and said no -- a decision); or a
// plain error (the picker could not be run, or answered outside its own
// protocol -- a malfunction).
func (c CLI) Pick(ctx context.Context, dir string, opts Options) (Result, error) {
	code, stdout, stderr, err := c.run(ctx, dir, opts)
	if err != nil {
		return Result{}, err
	}

	// Decode FIRST, even on a refusal: the protocol reports its `reason` in
	// the JSON body on every exit path, and that sentence is more useful than
	// anything this package could compose from an exit code.
	res, derr := Decode(stdout)

	if code != ExitPicked {
		// An exit code outside the protocol's set is a MALFUNCTION, and
		// this check is what docs/account-picker.md's "any other exit code
		// is treated as a malfunction, not a refusal" means on this path --
		// it used to be true of Probe alone, so a picker that died on exit
		// 1 or 127, or was killed by a signal, was reported to the user as
		// "picker refused: picker exited 127": a crash presented as a
		// decision. The two are not interchangeable. A refusal is the
		// picker answering; a malfunction is the picker failing to, and
		// only the first is something the user can reason about.
		if !documentedCode(code) {
			return Result{}, fmt.Errorf("%s%s", undocumentedCodeMsg(c.Bin, code), stderrNote(stderr))
		}
		reason := res.Reason
		if derr != nil || strings.TrimSpace(reason) == "" {
			reason = strings.TrimSpace(string(stderr))
		}
		return Result{}, &RefusalError{Code: code, Reason: reason}
	}
	if derr != nil {
		return Result{}, fmt.Errorf("picker %s: %w", c.Bin, derr)
	}
	if res.Profile == "" {
		// Exit 0 means picked. A picker that exits 0 with no profile has
		// broken its own contract, and passing the empty string on as a pin
		// would launch under whatever account happened to be live while the
		// row claimed the picker had chosen one.
		return Result{}, fmt.Errorf("picker %s: exit 0 (picked) but no profile in the answer", c.Bin)
	}
	return res, nil
}

// Probe runs ONE `--json --dry-run` against dir and reports whether the
// executable behaves like a picker: an exit code from the documented set, and
// an answer carrying the documented keys.
//
// It judges the SHAPE of the answer, never the answer. A machine whose pool
// is exhausted, or which has no tenant table configured, still has a
// perfectly good picker -- refusing it there would turn the feature off on
// exactly the day the user most needs to see why it said no.
//
// --dry-run is not incidental: a probe must not write a ledger entry or build
// an account directory for a session nobody has asked for yet.
func (c CLI) Probe(ctx context.Context, dir string) error {
	code, stdout, stderr, err := c.run(ctx, dir, Options{DryRun: true})
	if err != nil {
		return err
	}
	if !documentedCode(code) {
		return fmt.Errorf("%s%s", undocumentedCodeMsg(c.Bin, code), stderrNote(stderr))
	}
	if missing := MissingKeys(stdout); len(missing) > 0 {
		return fmt.Errorf("%s answered without the documented keys %s -- it does not implement the account-picker protocol",
			c.Bin, strings.Join(missing, ", "))
	}
	return nil
}

// documentedCode reports whether code is one the protocol defines. Anything
// else is a malfunction on BOTH paths -- see Pick and Probe, which share this
// so the two cannot drift again.
func documentedCode(code int) bool {
	switch code {
	case ExitPicked, ExitExhausted, ExitBackpressure, ExitBadTable, ExitNoProfiles, ExitUsage:
		return true
	}
	return false
}

func undocumentedCodeMsg(bin string, code int) string {
	return fmt.Sprintf("%s exited %d, which is not one of the protocol's codes (0, 2, 3, 4, 5, 64)", bin, code)
}

// stderrNote appends a picker's own stderr to a message, flattened, when it
// said anything. A program dying outside the protocol usually explains itself
// there and nowhere else.
func stderrNote(stderr []byte) string {
	note := strings.TrimSpace(string(stderr))
	if note == "" {
		return ""
	}
	return ": " + flatten(note)
}

// pickerTimeout bounds EVERY picker invocation -- probe, preview and the
// commit-time pick alike, because they all reach run.
//
// It is a deadlock breaker, not a responsiveness budget, and the distinction
// is what sets the number. The invocation this protects is a third-party
// executable named in config.toml and run before the popup is drawn: with no
// deadline, a picker that hangs means the form never appears and never says
// why, which is the one degradation mode this feature's design forbids
// outright because it is neither visible nor recoverable. Every other failure
// here already reaches a row or a stderr line.
//
// Thirty seconds, then, is chosen to sit ABOVE the slowest legitimate answer
// rather than near a comfortable one. A conformant picker may hold N accounts'
// usage behind a cache (the protocol reports `usage.cache_age_s` precisely
// because it has one), and a cold cache is N network round-trips before it can
// answer at all. Cutting that off at a snappier three or five seconds would
// report a WORKING picker as unusable, which is a worse error than a slow
// start: it is wrong, it is permanent until someone re-reads the config, and
// the user has no way to tell it from a genuinely broken executable.
//
// A var, not a const, only so the tests can shrink it -- the same shape
// plan.exec's promptRetrySettle uses.
var pickerTimeout = 30 * time.Second

// pickerWaitDelay is how long Wait may keep waiting on the picker's PIPES
// after the picker itself has exited -- killed by the deadline or not -- and
// without it the deadline above bounds nothing that matters.
//
// Measured, not reasoned about: with pickerTimeout alone, a stub picker that
// is `#!/bin/sh` + `sleep 30` returns from CLI.run after the full thirty
// seconds. exec.CommandContext kills the SHELL on time, but `sleep` is a
// separate child holding the inherited write end of the stdout pipe open, and
// cmd.Wait waits for the pipe-copying goroutine rather than for the process.
// A real picker is exactly this shape -- it sources a framework and shells out
// per account -- so the case is the normal one, not a contrived one.
//
// Two seconds of grace, so a picker that is merely being killed still gets to
// flush whatever it had written; after that Wait closes the pipes and returns
// regardless. Nothing is lost by it on that path: the kill means the run has
// already produced no usable answer.
//
// The grace also runs on the OTHER path, and that one is not a failure
// (#291). It starts whenever the picker exits, killed or not, so a picker
// that answers and exits 0 while a child still holds the pipe comes back
// from cmd.Run as exec.ErrWaitDelay two seconds later -- with its answer in
// the buffer. classifyRun reads that as the answer it is.
var pickerWaitDelay = 2 * time.Second

// run executes the picker and separates "did not run" from "ran and exited
// non-zero". Only the latter carries an exit code worth interpreting.
func (c CLI) run(ctx context.Context, dir string, opts Options) (code int, stdout, stderr []byte, err error) {
	if c.Bin == "" {
		return 0, nil, nil, errors.New("picker: no executable configured")
	}
	ctx, cancel := context.WithTimeout(ctx, pickerTimeout)
	defer cancel()

	argv := Argv(c.Bin, dir, opts)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.WaitDelay = pickerWaitDelay
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf

	runErr := cmd.Run()

	switch classifyRun(runErr, ctx.Err()) {
	case outcomeAnswered:
		return 0, out.Bytes(), errBuf.Bytes(), nil
	case outcomeTimedOut:
		return 0, nil, nil, fmt.Errorf("picker %s: no answer within %s%s", c.Bin, pickerTimeout, stderrNote(errBuf.Bytes()))
	case outcomeCancelled:
		return 0, nil, nil, fmt.Errorf("picker %s: %w", c.Bin, ctx.Err())
	case outcomeExited:
		// classifyRun returns this only when runErr IS an *exec.ExitError,
		// so the As below always matches and exitErr is never nil.
		var exitErr *exec.ExitError
		errors.As(runErr, &exitErr)
		return exitErr.ExitCode(), out.Bytes(), errBuf.Bytes(), nil
	default:
		// Could not start: not on PATH, not executable. Never a refusal --
		// nothing refused anything.
		if note := strings.TrimSpace(errBuf.String()); note != "" {
			return 0, nil, nil, fmt.Errorf("picker %s: %w: %s", c.Bin, runErr, flatten(note))
		}
		return 0, nil, nil, fmt.Errorf("picker %s: %w", c.Bin, runErr)
	}
}

// runOutcome is what one picker invocation amounted to.
type runOutcome int

const (
	outcomeAnswered runOutcome = iota
	outcomeTimedOut
	outcomeCancelled
	outcomeExited
	outcomeNotStarted
)

// classifyRun turns what cmd.Run returned, plus the context's own state,
// into one of five outcomes. It is a pure function of those two errors so
// that the ORDER of the cases -- which is the whole behaviour here -- can be
// asserted without arranging a real subprocess to land inside a window a
// loaded machine can miss.
//
// It is linear.classifyRun's four cases with the last one split in two,
// because only this package has an exit code worth reading: a picker that
// exited non-zero on its own is answering in the protocol's codes, and one
// that never started is not. linear's comment carries the full argument;
// what follows is the part that decides a picker's outcome.
//
// An ANSWER IN HAND BEATS A CONTEXT THAT IS DONE, so the first case is read
// before the deadline. exec.ErrWaitDelay means the picker SUCCEEDED -- it is
// returned only when "no Cancel call has occurred, and the command has
// otherwise exited with a successful status" (go1.26.4 src/os/exec/exec.go)
// -- and a nil runErr can arrive with the deadline already expired, because
// once Process.Wait has reaped the child nothing watches the context. Both
// are a real two-second band at the shipped 30s/2s: a picker that answers
// between 28s and 30s with a child on the pipe. Reading the deadline first,
// as run did until #291, reports that working picker as one that never
// answered -- and reporting a working picker as unusable is the error
// pickerTimeout's own comment ranks worse than a slow start.
//
// A run the deadline AFFECTED cannot reach the first case: killed while
// running gives an *exec.ExitError, and exited 0 before it could be reaped
// gives the context error itself, because the kill succeeds on the zombie
// and the watcher injects ctx.Err(). Neither is nil or ErrWaitDelay. A
// zombie the reap gets to first is not one of these: the kill finds it done,
// nothing reached it, and it comes back nil -- an answer, correctly.
//
// Otherwise the DEADLINE is read before the exit code, and that order is
// load-bearing rather than tidy. exec.CommandContext kills the process when
// the context is done, and cmd.Run then reports a perfectly ordinary
// *exec.ExitError carrying code -1 ("signal: killed") -- measured, not
// assumed. Read in exit-code order, a hung picker would therefore arrive at
// Pick as a refusal with an impossible code, so the timeout added to stop a
// hang would have turned it into "picker refused: picker exited -1": a
// deadlock reported as a decision. Nothing timed out here refused anything.
//
// It is read as DeadlineExceeded specifically: a caller that was CANCELLED
// reports context.Canceled, and calling that a timeout would be false
// (#272).
func classifyRun(runErr, ctxErr error) runOutcome {
	var exitErr *exec.ExitError
	switch {
	case runErr == nil || errors.Is(runErr, exec.ErrWaitDelay):
		return outcomeAnswered
	case errors.Is(ctxErr, context.DeadlineExceeded):
		return outcomeTimedOut
	case ctxErr != nil:
		return outcomeCancelled
	case errors.As(runErr, &exitErr):
		return outcomeExited
	default:
		return outcomeNotStarted
	}
}
