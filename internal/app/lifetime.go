package app

import (
	"context"
	"sync"
	"time"
)

// Lifetime is the popup process's own bound on the off-model work it starts:
// one context every cancellable call runs on, cancelled when the program
// quits, plus the bounded wait that keeps the process alive long enough for
// that cancellation to be delivered (#211).
//
// It exists because of what the commit-time account pick does. It is the one
// picker call that is not a dry run -- it writes the picker's ledger, which is
// what stops two sessions being handed the same account -- and `esc` mid-pick
// used to quit the popup and leave the picker running, so a pick the user
// cancelled could still record an account for a session that never exists.
// Running it on a context cancelled at quit is what kills the picker with its
// caller, because picker.CLI runs it through exec.CommandContext.
//
// The nil Lifetime is usable and does exactly what this code did before: every
// call runs on context.Background(), uncancellable, with nothing to wait for.
// That is what a Model built by a test that does not care about any of this
// has, and it is why Deps.Lifetime may be left unset.
type Lifetime struct {
	ctx    context.Context
	cancel context.CancelFunc
	// inFlight counts the Begin/done regions Shutdown waits for.
	inFlight sync.WaitGroup
}

// NewLifetime returns a Lifetime nothing has cancelled yet. Production has
// exactly one, built in main alongside the tea.Program it bounds.
func NewLifetime() *Lifetime {
	ctx, cancel := context.WithCancel(context.Background())
	return &Lifetime{ctx: ctx, cancel: cancel}
}

// Begin registers one piece of cancellable off-model work and returns the
// context to run it on together with the func that marks it finished:
//
//	ctx, done := lt.Begin()
//	defer done()
//
// Forgetting done() costs the whole grace at quit and nothing else -- the wait
// is bounded on purpose (see Shutdown).
//
// A Begin that arrives AFTER Shutdown has cancelled gets the cancelled context
// and is not waited for, which is the right answer both ways round: a picker
// started on an already-cancelled context is never started at all
// (exec.Cmd.Start refuses it), so there is nothing left to kill.
func (l *Lifetime) Begin() (context.Context, func()) {
	if l == nil {
		return context.Background(), func() {}
	}
	l.inFlight.Add(1)
	return l.ctx, sync.OnceFunc(func() { l.inFlight.Done() })
}

// Shutdown cancels every call in flight and then waits, up to grace, for those
// calls to come back. Main calls it once, after the tea.Program has returned
// and before the process exits.
//
// The wait is the half that is easy to leave out and does not work without.
// exec.CommandContext does not kill the child on the cancelling goroutine: it
// kills it from a watcher goroutine that has to be scheduled first, so a
// process that cancels and exits immediately can exit first and leave the
// picker running -- the bug, with an extra step.
//
// grace bounds it because what remains after the kill is not ours. Measured on
// 2026-09-20 over twenty runs of a `#!/bin/sh` + `sleep` stub: cancel to a
// dead child, 1.4ms worst case; cancel to exec.Cmd.Wait returning, 2.05s --
// the picker's orphaned `sleep` inherits the stdout pipe and holds Wait open
// for the whole of picker.pickerWaitDelay. A real picker is exactly that shape
// (it shells out per account), so the slow case is the normal one, and waiting
// it out would hold the popup on screen for two seconds after the form is gone
// and after the process it was waiting for was already dead. The grace is
// therefore sized for the kill, not for the return.
func (l *Lifetime) Shutdown(grace time.Duration) {
	if l == nil {
		return
	}
	l.cancel()
	returned := make(chan struct{})
	go func() {
		l.inFlight.Wait()
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(grace):
	}
}
