// checks.go bounds every question the pre-flight asks the filesystem
// or git, so a stalled mount refuses the create instead of swallowing it
// (#272).
//
// #202 bounded the four checks that hold a submit in the popup. The same
// questions in headless `create` were still unbounded, and `create` is the
// path with nobody at the keyboard: since #252 a signal can cancel the
// pre-flight, so a person at a terminal can get out, but nothing expires
// on its own. A create driven by the spawn skill, a script or a cron entry
// therefore sat on a stalled mount for as long as the mount stayed
// stalled -- no output, no exit code, and the lane that started it waiting
// for good.
//
// Wrapping the caller in timeout(1) is not the same answer: that sends
// SIGTERM, which main.watchSignals re-raises as 128+n, so the caller
// learns the run died and never which question it died on.
//
// THE RULE, and the only one to keep in mind when adding a call here:
// every question is bounded exactly once, at the outermost point where a
// timeout can still be told apart from an answer. The six GitSource
// methods are bounded at the method, because each call site sees the error
// and can refuse. app.SettleBase and app.NamesHead are bounded as a UNIT,
// over the RAW source, because they swallow ResolveCommit's error into a
// verdict of their own -- "no such commit here; using HEAD", and false --
// so a bound any deeper would hand them a timeout dressed as an answer.
// Passing a boundedGit to either would also bound the same call twice.
package create

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ZviBaratz/herdr-draft/internal/app"
	"github.com/ZviBaratz/herdr-draft/internal/defaults"
)

// preflightCheckDeadline bounds every question the pre-flight asks the
// filesystem or git (#272).
//
// Thirty seconds, and deliberately NOT the popup's five
// (app.blockingCheckDeadline): that number is sized by its own comment for
// "the time a person will hold a key combination and wonder", and a script
// has no patience to run out. What a headless run has instead is a higher
// price for a false refusal -- a whole invocation rather than a keystroke.
// The other half of the popup's reasoning does carry over, and is what
// makes any number above a few seconds safe: these are an os.Stat and one
// or two `git rev-parse` calls, single-digit milliseconds on a filesystem
// that is working and seconds only on one that is not.
//
// It costs at most ONE budget, not six. The pre-flight asks its questions
// in order and a timeout refuses the create outright, so the first stalled
// question is the only one ever reached -- unlike the popup, where four
// independent checks can each spend their own.
//
// A constant rather than a [timeouts] key, for fetchPruneTimeout's and
// blockingCheckDeadline's reason: it is a safety bound, not a tuning knob,
// and nothing a user could set it to would make a hung mount answer. A key
// would also inherit #143 -- [timeouts] values are not validated at all --
// which would let one typo turn the bound into "immediately". Nor a flag:
// every flag on this command has a form control behind it, and no row
// offers this.
const preflightCheckDeadline = 30 * time.Second

// errCheckTimedOut is what every timed-out question reports through, so
// run() can tell one from the usage errors it travels among with a single
// errors.Is. It says the check did not finish, never that the thing
// checked was bad -- which is the whole distinction #202 named "unknown is
// not invalid", and the reason this does not land on ExitUsage.
var errCheckTimedOut = errors.New("the check did not answer in time")

// checkTimeout is one question that did not answer: the sentence a caller
// reads, and errCheckTimedOut to the code that has to branch.
//
// A type with its own Is rather than an fmt.Errorf wrap of the sentinel,
// so the message stays one clause -- "timed out after 30s checking whether
// /srv/x is a git repository" -- instead of ending in the sentinel's own
// text repeating what the clause already said. herdrc.cliError does the
// same for herdr's error codes.
type checkTimeout struct {
	what     string
	deadline time.Duration
}

func (e checkTimeout) Error() string {
	return fmt.Sprintf("timed out after %s %s", e.deadline, e.what)
}

func (checkTimeout) Is(target error) bool { return target == errCheckTimedOut }

// bounded runs one question under the deadline and returns what it
// answered, or a checkTimeout naming what was asked.
//
// It bounds the ANSWER, not the call -- app.AwaitCheck's own doc comment
// has the measured reason, and it is the half that matters here too: two
// of the six take no context at all, and on the stalled mount this is
// about, a cancelled context returns nothing sooner than no context would.
// The price is the same one the popup pays, and it is cheaper here: the
// goroutine left on a call that may never come back holds it only until
// the process exits, which a refused create is about to do.
//
// A nil release, because `create` has no Lifetime to hold a region on --
// it has no tea.Program to outlive, and main spends its shutdown grace in
// full rather than waiting on a WaitGroup.
//
// TWO CONTEXTS, and the split is the point: the deadline is ours, the
// cancellation is the caller's, and only the first of them is a timeout.
//
// The CALL gets the caller's context with the deadline on top, so a signal
// still reaches git (#252) and the deadline still reaches the four
// questions that take a context at all.
//
// The WAIT gets the deadline and nothing else. A caller whose context was
// cancelled is not a check that did not answer: reporting "timed out after
// 30s" about a signal that arrived in the first second would be false, and
// giving up on the pre-flight there would take the account pick with it --
// which #252 deliberately lets happen, because that pick is the one call
// that writes the picker's ledger and the cancel is what kills it. On a
// cancellation the call comes back by itself, carrying its own error,
// exactly as it did before this bound existed.
func bounded[T any](ctx context.Context, deadline time.Duration, what string, ask func(context.Context) T) (T, error) {
	callCtx, cancelCall := context.WithTimeout(ctx, deadline)
	defer cancelCall()
	v, ok := app.AwaitCheck(context.WithoutCancel(ctx), nil, deadline, func(context.Context) T {
		return ask(callCtx)
	})
	if !ok {
		var zero T
		return zero, checkTimeout{what: what, deadline: deadline}
	}
	return v, nil
}

// boundedErr is bounded for a question that answers with an error of its
// own. A timeout replaces the pair; a real error comes back untouched, so
// "git said no" and "git did not say" stay two different answers all the
// way to the call site.
func boundedErr[T any](ctx context.Context, deadline time.Duration, what string, ask func(context.Context) (T, error)) (T, error) {
	type answer struct {
		v   T
		err error
	}
	res, err := bounded(ctx, deadline, what, func(ctx context.Context) answer {
		v, err := ask(ctx)
		return answer{v: v, err: err}
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return res.v, res.err
}

// boundedGit is the pre-flight's GitSource with a deadline on every
// question. It is deliberately NOT a GitSource: the two calls that
// answered with a bare bool now answer with an error too, which is what
// makes the compiler find every call site and what stops a timeout being
// read as the verdict's negative -- a directory that does not exist, a
// directory that is not a repository, a branch that is free.
type boundedGit struct {
	git      GitSource
	deadline time.Duration
}

// checks is the one place either half of the budget is read.
func (d Deps) checks() boundedGit {
	deadline := d.CheckDeadline
	if deadline <= 0 {
		deadline = preflightCheckDeadline
	}
	return boundedGit{git: d.git(), deadline: deadline}
}

func (b boundedGit) DirExists(ctx context.Context, path string) (bool, error) {
	return bounded(ctx, b.deadline, "checking whether "+path+" exists",
		func(context.Context) bool { return b.git.DirExists(path) })
}

func (b boundedGit) IsGitRepo(ctx context.Context, dir string) (bool, error) {
	return bounded(ctx, b.deadline, "checking whether "+dir+" is a git repository",
		func(context.Context) bool { return b.git.IsGitRepo(dir) })
}

func (b boundedGit) RepoRoot(ctx context.Context, dir string) (string, error) {
	return boundedErr(ctx, b.deadline, "finding the repository root of "+dir,
		func(ctx context.Context) (string, error) { return b.git.RepoRoot(ctx, dir) })
}

func (b boundedGit) PrimaryCheckout(ctx context.Context, dir string) (string, error) {
	return boundedErr(ctx, b.deadline, "finding the primary checkout of "+dir,
		func(ctx context.Context) (string, error) { return b.git.PrimaryCheckout(ctx, dir) })
}

func (b boundedGit) BranchExists(ctx context.Context, dir, name string) (bool, error) {
	return boundedErr(ctx, b.deadline, fmt.Sprintf("checking whether branch %q already exists in %s", name, dir),
		func(ctx context.Context) (bool, error) { return b.git.BranchExists(ctx, dir, name) })
}

func (b boundedGit) ResolveCommit(ctx context.Context, dir, ref string) (string, error) {
	return boundedErr(ctx, b.deadline, fmt.Sprintf("resolving base %q in %s", ref, dir),
		func(ctx context.Context) (string, error) { return b.git.ResolveCommit(ctx, dir, ref) })
}

// SettleBase is app.SettleBase under the bound, as a unit -- see THE RULE
// at the top of this file. It reports the timeout as a third return rather
// than through the note, because the note is what a create PRINTS and
// carries on from, and a question nobody answered must stop the run.
func (b boundedGit) SettleBase(ctx context.Context, dir string, res defaults.Resolved) (defaults.Resolved, string, error) {
	type settled struct {
		res  defaults.Resolved
		note string
	}
	out, err := bounded(ctx, b.deadline, fmt.Sprintf("settling base %q in %s", res.BaseRef, dir),
		func(ctx context.Context) settled {
			r, note := app.SettleBase(ctx, b.git, dir, res)
			return settled{res: r, note: note}
		})
	if err != nil {
		return defaults.Resolved{}, "", err
	}
	return out.res, out.note, nil
}

// NamesHead is app.NamesHead under the bound, as a unit, for the same
// reason: it turns its ResolveCommit's error into a plain false.
func (b boundedGit) NamesHead(ctx context.Context, dir, ref, commit string) (bool, error) {
	return bounded(ctx, b.deadline, fmt.Sprintf("checking whether %q is HEAD in %s", ref, dir),
		func(ctx context.Context) bool { return app.NamesHead(ctx, b.git, dir, ref, commit) })
}
