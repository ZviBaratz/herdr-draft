package plan

import (
	"context"
	"fmt"
	"time"

	"github.com/ZviBaratz/herdr-draft/internal/gitx"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
)

// TrustOutcome is what became of a new worktree's environment files
// (Input.InheritTrust): gitx.Host.ExtendTrust's verdicts on its .envrc and
// its mise config, or the error that stopped it giving them.
type TrustOutcome struct {
	Trust gitx.EnvTrust
	Err   error
	// Interrupted reports that the .envrc this run allowed was still
	// loading after envLoadBusyBudget, and was interrupted before the agent
	// was launched, so it started anyway -- without it (settleEnv).
	Interrupted bool
}

// Note is the one line both front ends say about it -- create on the
// worktree step's progress line, the popup in that step's row -- or ""
// when there is nothing to say. Silent are: no .envrc or mise config, or
// no direnv or mise, which is most repositories; a main clone that does
// not allow or trust its own, alone, which is the state before this
// existed; and a checkout the operator denied, which they know. Said are what was extended, and a file the branch changed or a
// mise config that kept the .envrc blocked, because the operator has
// something to review or to trust.
func (o TrustOutcome) Note() string {
	e, m := o.Trust.Envrc, o.Trust.Mise
	switch {
	case o.Err != nil && e == gitx.EnvrcAllowing:
		return ".envrc may be allowed, but the step failed: " + o.Err.Error()
	case o.Err != nil:
		return "trust not extended: " + o.Err.Error()
	case e == gitx.EnvrcAllowed && m == gitx.MiseTrusted:
		return ".envrc allowed and mise trusted: same as the main clone's"
	case e == gitx.EnvrcAllowed:
		return ".envrc allowed: same as the main clone's"
	case e == gitx.EnvrcHeld && m == gitx.MiseDiffers:
		return ".envrc left blocked: its mise config differs from the main clone's"
	case e == gitx.EnvrcHeld && m == gitx.MiseUnknown:
		return ".envrc left blocked: it has a mise config and no mise was found to trust it"
	case e == gitx.EnvrcHeld:
		return ".envrc left blocked: mise does not trust the main clone's config"
	case e == gitx.EnvrcDiffers && m == gitx.MiseTrusted:
		return ".envrc left blocked: differs from the main clone's; mise trusted"
	case e == gitx.EnvrcDiffers:
		return ".envrc left blocked: differs from the main clone's"
	case m == gitx.MiseTrusted:
		return "mise trusted: same as the main clone's"
	case m == gitx.MiseDiffers:
		return "mise config left untrusted: differs from the main clone's"
	}
	return ""
}

// envLoaded reports whether the new pane's shell may load an environment
// on its first prompt -- an .envrc this run allowed, or began allowing
// before the step failed -- which can take far longer than a shell's
// start: hub's builds a virtualenv in a fresh checkout.
func (o TrustOutcome) envLoaded() bool {
	return o.Trust.Envrc == gitx.EnvrcAllowed || o.Trust.Envrc == gitx.EnvrcAllowing
}

// trustTimeout bounds the trust step. It is best-effort and sits between
// herdr cutting the worktree and the agent starting, so a tool or an ssh
// that hangs must cost a create seconds, not the session. Wide enough for a
// new ssh connection over SSM (4-5 s, machine spec §3.4) and for gitx's own
// ConnectTimeout; a constant for remoteFetchTimeout's reason.
const trustTimeout = 20 * time.Second

// extendTrust runs the trust step for one worktree op on its machine:
// source is the clone the worktree was cut from, checkout the path herdr
// reported.
func extendTrust(ctx context.Context, op Op, checkout string) TrustOutcome {
	if !op.InheritTrust || op.Worktree == nil || checkout == "" {
		return TrustOutcome{}
	}
	ctx, cancel := context.WithTimeout(ctx, trustTimeout)
	defer cancel()
	tr, err := op.Machine.Git().ExtendTrust(ctx, op.Worktree.Cwd, checkout)
	return TrustOutcome{Trust: tr, Err: err}
}

// maxEnvInterrupts bounds how many times settleEnv presses ctrl+c in a
// pane whose environment load outlasted envLoadBusyBudget. One is direnv's
// own way out; a second covers an .envrc whose first command shrugs one off.
// Past that, the agent is launched anyway, as it was before any of this.
const maxEnvInterrupts = 2

// envInterruptWait is how long settleEnv gives a pressed load to stop
// before it presses again.
const envInterruptWait = 5 * time.Second

// envPollInterval, envIdleGrace and envNow pace settleEnv. Package vars
// for busyRetryInterval's reason: tests run them on a fake clock.
//
// envIdleGrace is how long a pane must sit at a bare shell, with no load
// seen, before settleEnv takes it that none is coming. The allow usually
// lands before the shell's first prompt runs its hook, and nothing tells a
// prompt that has not started that hook from one that finished without
// it: an interactive zsh takes 0.18 s to start on the laptop and 0.55 s on
// dev (measured 2026-10-14), well inside it.
var (
	envPollInterval = 250 * time.Millisecond
	envIdleGrace    = 2 * time.Second
	envNow          = time.Now
)

// loadingEnv reports a pane whose foreground is running direnv's hook:
// `direnv export <shell>`, which is how the hook loads an .envrc.
func loadingEnv(info herdrc.ProcessInfo) bool {
	for _, p := range info.Foreground {
		if p.Name == "direnv" && len(p.Argv) >= 2 && p.Argv[1] == "export" {
			return true
		}
	}
	return false
}

// atPrompt reports a pane whose foreground is the shell alone. herdr's
// shell_pid is optional, so without it one process stands for the shell.
func atPrompt(info herdrc.ProcessInfo) bool {
	if info.ShellPID == 0 {
		return len(info.Foreground) <= 1
	}
	for _, p := range info.Foreground {
		if p.PID != info.ShellPID {
			return false
		}
	}
	return true
}

// settleEnv waits, before an agent is launched in pane, for the load of the
// .envrc this run allowed, reading the pane's foreground (herdr pane
// process-info) rather than inferring it from how a launch fails: herdr's
// `agent start` refuses a busy shell on dev and types into it on the laptop,
// which then times out waiting for an agent queued behind the load.
//
// It returns once a load has been seen and finished, or once the shell has
// sat at its prompt for envIdleGrace with none seen. A load still running
// after envLoadBusyBudget gets ctrl+c -- direnv gives up on it, measured on
// direnv 2.25.2 and 2.37.1 despite the hook's `trap -- ” SIGINT` -- at most
// maxEnvInterrupts times, and always BEFORE the launch: a pinned account's
// launch line is typed, and a press after it would flush it.
//
// It never fails anything. A pane whose foreground cannot be read is
// launched into at once, as before this existed.
func settleEnv(ctx context.Context, r herdrc.Runner, pane string) (interrupted bool) {
	start := envNow()
	deadline := start.Add(envLoadBusyBudget)
	// The whole wait is bounded too, whatever a branch below decides: every
	// press its wait, and the grace once.
	giveUp := deadline.Add(maxEnvInterrupts*envInterruptWait + envIdleGrace)
	seen, presses := false, 0
	var idleSince time.Time
	for ctx.Err() == nil {
		info, err := r.PaneProcessInfo(ctx, pane)
		if err != nil {
			return interrupted
		}
		now := envNow()
		if now.After(giveUp) {
			return interrupted
		}
		switch {
		case loadingEnv(info):
			seen, idleSince = true, time.Time{}
			if now.Before(deadline) {
				break
			}
			if presses == maxEnvInterrupts || r.PaneSendKeys(ctx, pane, "ctrl+c") != nil {
				return interrupted
			}
			presses, interrupted = presses+1, true
			deadline = now.Add(envInterruptWait)
		case atPrompt(info):
			if seen {
				return interrupted
			}
			if idleSince.IsZero() {
				idleSince = now
			} else if now.Sub(idleSince) >= envIdleGrace {
				return interrupted
			}
		default:
			// The shell is running something that is not the hook: its rc
			// still starting, most likely. Waited for, but not past the
			// budget, and never interrupted -- it is not ours.
			idleSince = time.Time{}
			if !now.Before(deadline) {
				return interrupted
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(envPollInterval):
		}
	}
	return interrupted
}

// interruptedNote is the agent step's line when it had to interrupt the
// environment load to start.
func interruptedNote() string {
	return fmt.Sprintf("the environment was still loading after %s; interrupted it to start the agent", envLoadBusyBudget)
}
