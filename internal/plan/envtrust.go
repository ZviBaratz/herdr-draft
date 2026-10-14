package plan

import (
	"context"
	"fmt"
	"time"

	"github.com/ZviBaratz/herdr-draft/internal/gitx"
)

// TrustOutcome is what became of a new worktree's environment files
// (Input.InheritTrust): gitx.Host.ExtendTrust's verdicts on its .envrc and
// its mise config, or the error that stopped it giving them.
type TrustOutcome struct {
	Trust gitx.EnvTrust
	Err   error
	// Interrupted reports that the .envrc this run allowed was still
	// loading when the agent's start ran out of envLoadBusyBudget, and was
	// interrupted so the agent could start anyway -- without it.
	Interrupted bool
}

// Note is the one line both front ends say about it -- create on the
// worktree step's progress line, the popup in that step's row -- or ""
// when there is nothing to say. Silent are: no .envrc or mise config, or
// no direnv or mise, which is most repositories; and a main clone that
// does not allow or trust its own, alone, which is the state before this
// existed. Said are what was extended, and a file the branch changed or a
// mise config that kept the .envrc blocked, because the operator has
// something to review or to trust.
func (o TrustOutcome) Note() string {
	if o.Err != nil {
		return "trust not extended: " + o.Err.Error()
	}
	e, m := o.Trust.Envrc, o.Trust.Mise
	switch {
	case e == gitx.EnvrcAllowed && m == gitx.MiseTrusted:
		return ".envrc allowed and mise trusted: same as the main clone's"
	case e == gitx.EnvrcAllowed:
		return ".envrc allowed: same as the main clone's"
	case e == gitx.EnvrcHeld && m == gitx.MiseDiffers:
		return ".envrc left blocked: its mise config differs from the main clone's"
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

// envLoaded reports whether the new pane's shell will load an environment
// on its first prompt -- an .envrc this run allowed -- which can take far
// longer than a shell's start: hub's builds a virtualenv in a fresh
// checkout.
func (o TrustOutcome) envLoaded() bool { return o.Trust.Envrc == gitx.EnvrcAllowed }

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

// maxEnvInterrupts bounds how many times Execute presses ctrl+c in a pane
// whose environment load outlasted envLoadBusyBudget. One is direnv's own
// way out; a second covers an .envrc whose first command shrugs one off.
// Past that, the pane is busy for some reason of its own and the step fails
// as any busy pane does.
const maxEnvInterrupts = 2

// interruptedNote is the agent step's line when it had to interrupt the
// environment load to start.
func interruptedNote() string {
	return fmt.Sprintf("the environment was still loading after %s; interrupted it to start the agent", envLoadBusyBudget)
}
