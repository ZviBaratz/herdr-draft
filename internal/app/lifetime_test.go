package app

import (
	"context"
	"errors"
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

	start := time.Now()
	lt.Shutdown(100 * time.Millisecond)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Shutdown took %s on work that never returns -- the grace does not bound it", elapsed)
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

// And the lane's base, the other call a submit blocks on (owner, same
// decision): a `git rev-parse` that writes nothing either way, on the same
// context so that "everything the submit is waiting for dies with the popup"
// is a rule rather than a list.
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
