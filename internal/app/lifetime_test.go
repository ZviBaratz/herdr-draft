package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/picker"
)

// The whole point of the type: quitting cancels the context the off-model
// work is running on, which is what kills a picker mid-pick (#211).
func TestShutdownCancelsWorkInFlight(t *testing.T) {
	lt := NewLifetime()
	ctx, done := lt.Begin()
	defer done()

	if err := ctx.Err(); err != nil {
		t.Fatalf("Begin handed out an already-cancelled context: %v", err)
	}
	lt.Shutdown(50 * time.Millisecond)
	if err := ctx.Err(); !errors.Is(err, context.Canceled) {
		t.Fatalf("after Shutdown the work's context reported %v, want context.Canceled", err)
	}
}

// And it stays up long enough for that cancellation to land.
//
// exec.CommandContext kills the child from a watcher goroutine, so a process
// that cancels and exits in the same breath can exit FIRST and leave the
// picker running anyway -- the cancellation would be delivered to nobody.
// Shutdown waits for the work itself to come back.
func TestShutdownWaitsForCancelledWorkToComeBack(t *testing.T) {
	lt := NewLifetime()
	returned := make(chan struct{})
	started := make(chan struct{})
	go func() {
		ctx, done := lt.Begin()
		defer done()
		close(started)
		<-ctx.Done()
		// Stand in for the kill a real picker's watcher goroutine has to be
		// scheduled to deliver: work that happens AFTER the cancellation and
		// before the call returns.
		time.Sleep(80 * time.Millisecond)
		close(returned)
	}()
	<-started

	lt.Shutdown(5 * time.Second)

	select {
	case <-returned:
	default:
		t.Fatal("Shutdown returned while the cancelled work was still running -- the process would exit before the kill landed")
	}
}

// The wait is BOUNDED, though, and the bound is not a formality: a picker
// whose own children hold its pipes open keeps exec.Cmd.Wait busy for the
// whole of picker.pickerWaitDelay after the kill has already landed (measured:
// ~2s). Holding the popup open for that would be waiting on a corpse.
func TestShutdownGivesUpAtTheGrace(t *testing.T) {
	lt := NewLifetime()
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	go func() {
		_, done := lt.Begin()
		defer done()
		close(started)
		<-release
	}()
	<-started

	// Shutdown runs in a goroutine of its own, and the assertion is made
	// from here. Calling it inline instead would leave the failure it
	// claims unreachable: the worker cannot be released until this function
	// returns, so an unbounded Shutdown would hang rather than take too
	// long, and the test could only fail as the package-wide timeout panic
	// ten minutes later, with none of its own words.
	returned := make(chan struct{})
	go func() {
		lt.Shutdown(100 * time.Millisecond)
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown has not returned on work that never returns -- the grace does not bound it")
	}
}

// A Begin may arrive after Shutdown has already started waiting, and it must
// not take the process down with it.
//
// bubbletea does not wait for its Cmd goroutines before Run returns -- v2.0.8's
// tea.go says so in as many words ("Don't wait on these goroutines... we'll
// have to leak the goroutine until Cmd returns") -- so a Cmd launched just
// before the quit can reach Begin after runProgram has reached Shutdown. A
// bare sync.WaitGroup cannot take that: an Add that lifts the counter off zero
// concurrently with Wait is misuse, and misuse is a panic, so the tidy exit
// this type exists to provide would have ended in a stack dump over the pane
// (measured: this loop panicked within a few hundred iterations).
func TestBeginRacingShutdownDoesNotPanic(t *testing.T) {
	for range 20000 {
		lt := NewLifetime()
		_, first := lt.Begin()
		var wg sync.WaitGroup
		wg.Add(3)
		go func() { defer wg.Done(); first() }()
		go func() { defer wg.Done(); _, done := lt.Begin(); done() }()
		go func() { defer wg.Done(); lt.Shutdown(time.Second) }()
		wg.Wait()
	}
}

// A late Begin is handed the cancelled context and is not waited for, which is
// the right answer both ways round: exec.Cmd.Start refuses an already-cancelled
// context before forking, so a picker begun here is never started and there is
// nothing left to kill.
func TestABeginAfterShutdownIsCancelledAndUntracked(t *testing.T) {
	lt := NewLifetime()
	lt.Shutdown(time.Second)

	ctx, done := lt.Begin()
	if err := ctx.Err(); !errors.Is(err, context.Canceled) {
		t.Fatalf("a Begin after Shutdown got a context reporting %v, want context.Canceled", err)
	}
	done()

	// And Shutdown is callable again without waiting on that work or
	// panicking -- only runProgram calls it today, but a second caller must
	// not be a landmine.
	start := time.Now()
	lt.Shutdown(5 * time.Second)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("a second Shutdown took %s, want it to find nothing to wait for", elapsed)
	}
}

// A nil Lifetime is every Model a test builds without one, and it behaves
// exactly as this code did before #211: real work on an uncancellable
// context, and nothing to wait for.
func TestANilLifetimeRunsOnBackground(t *testing.T) {
	var lt *Lifetime

	ctx, done := lt.Begin()
	if ctx == nil {
		t.Fatal("a nil Lifetime handed out a nil context")
	}
	if err := ctx.Err(); err != nil {
		t.Fatalf("a nil Lifetime's context reported %v, want a live context", err)
	}
	done()
	lt.Shutdown(time.Second)
}

// --- what runs on it: every off-model call that starts a subprocess --------

// lastCancelled reports whether the last context handed to a fake is one the
// Lifetime cancelled. context.Background(), the value every one of these call
// sites passed before #211, reports nil here forever.
func lastCancelled(t *testing.T, ctxs []context.Context) {
	t.Helper()
	if len(ctxs) == 0 {
		t.Fatal("the call never happened, so there is no context to check")
	}
	if err := ctxs[len(ctxs)-1].Err(); !errors.Is(err, context.Canceled) {
		t.Fatalf("the call ran on a context that Shutdown left reporting %v -- it is not the app's, so quitting cannot reach it", err)
	}
}

// The commit-time pick is #211 itself: the one picker call that writes the
// ledger, and the one `esc` used to leave running.
func TestTheCommitPickRunsOnACancellableContext(t *testing.T) {
	lt := NewLifetime()
	p := &fakePicker{res: picker.Result{Profile: "alpha-1"}}
	m := modelWithPicker(t, p, config.Config{})
	m.deps.Lifetime = lt

	if _, ok := m.pickerCommitCmd()().(pickerCommitMsg); !ok {
		t.Fatal("pickerCommitCmd did not produce a pickerCommitMsg")
	}
	lt.Shutdown(time.Second)
	lastCancelled(t, p.ctxs)
}

// The preview pick writes no ledger, so nothing is recorded for a session
// nobody created -- but a picker that shells out per account is not free, and
// there is no reason to leave one running for a form that is gone (owner,
// 2026-09-20, beside #211's own decision).
func TestThePreviewPickRunsOnACancellableContext(t *testing.T) {
	lt := NewLifetime()
	p := &fakePicker{res: picker.Result{Profile: "alpha-1"}}
	m := modelWithPicker(t, p, config.Config{})
	m.deps.Lifetime = lt

	m = runPreview(t, m, "/p/thing")
	lt.Shutdown(time.Second)
	lastCancelled(t, p.ctxs)
}

// And the lane's base (owner, same decision): a `git rev-parse` that writes
// nothing either way, run at submit and blocking it. It is not the only thing
// a submit waits for -- handleSubmit also holds for the dir check, the base
// settle and the title-duplicate check, all three still on
// context.Background() -- so this is one more entry on that list rather than
// the end of it.
func TestTheLaneCommitReadRunsOnACancellableContext(t *testing.T) {
	lt := NewLifetime()
	git := laneGit()
	m := laneModel(t, &submitFakeRunner{}, git, true)
	m.deps.Lifetime = lt

	if _, ok := m.linkedCommitCmd()().(linkedCommitMsg); !ok {
		t.Fatal("linkedCommitCmd did not produce a linkedCommitMsg")
	}
	lt.Shutdown(time.Second)
	lastCancelled(t, git.resolveCtxs)
}

// And main's own Lifetime is the one the Model runs its work on. Bootstrap
// takes it rather than making one because the process, not the model, is what
// ends -- but it is Bootstrap that puts it where the commands read it.
func TestBootstrapWiresTheLifetimeIn(t *testing.T) {
	lt := NewLifetime()
	env := Env{ContextJSON: validContextJSON(), ConfigDir: t.TempDir(), StateDir: t.TempDir()}
	m, err := Bootstrap(env, &fakeRunner{}, nil, newFakeGit(), noSleep, lt)
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if m.deps.Lifetime != lt {
		t.Fatal("Bootstrap dropped the Lifetime it was handed -- quitting would reach none of the work")
	}
}
