package clauth

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// scriptClauth writes body as an executable /bin/sh script and returns its
// path, for the tests that need a clauth doing something other than printing
// a fixed answer.
func scriptClauth(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("stub clauth is a POSIX shell script")
	}
	bin := filepath.Join(t.TempDir(), "stub-clauth")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return bin
}

// setCLIBudgets shortens the CLI deadlines for one test and restores them
// afterwards. TWO parameters rather than one, because the cancellation test
// needs them apart: the deadline must sit out of reach so that the only
// thing which can end the call is the caller's own cancel, while the pipe
// grace must still be short or a grandchild holding stdout keeps cmd.Wait
// blocked for the whole of it.
func setCLIBudgets(t *testing.T, deadline, waitDelay time.Duration) {
	t.Helper()
	prevTimeout, prevDelay := cliTimeout, cliWaitDelay
	cliTimeout, cliWaitDelay = deadline, waitDelay
	t.Cleanup(func() { cliTimeout, cliWaitDelay = prevTimeout, prevDelay })
}

// shrinkCLITimeout is the ordinary case: one short number for both.
func shrinkCLITimeout(t *testing.T, d time.Duration) {
	t.Helper()
	setCLIBudgets(t, d, d)
}

// within runs call on its own goroutine and fails the test if it has not
// come back inside budget, rather than letting an unbounded call hang the
// suite. The message is the point: a test that times out here is reporting
// the defect, not a flake.
func within(t *testing.T, budget time.Duration, what string, call func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		call()
	}()
	select {
	case <-done:
	case <-time.After(budget):
		t.Fatalf("%s never came back within %s: it is not bounded by anything", what, budget)
	}
}

// A clauth that never answers must not hang the caller (#141). Load ran
// under context.Background() from both of its callers, and app.Bootstrap's
// call is synchronous and BEFORE the popup is drawn, so the form never
// appeared and never said why.
func TestAHangingCLIIsBounded(t *testing.T) {
	bin := scriptClauth(t, "sleep 30\n")
	shrinkCLITimeout(t, 150*time.Millisecond)

	var err error
	within(t, 5*time.Second, "Load", func() {
		_, err = Load(context.Background(), LoadOpts{CLIBin: bin, Now: time.Now})
	})

	if err == nil {
		t.Fatal("Load of a hanging clauth = nil error")
	}
	if !strings.Contains(err.Error(), "no answer within 150ms") {
		t.Errorf("Load error = %v, want one naming what ran out and its budget", err)
	}
}

// And a hang is a MALFUNCTION, never a decision clauth made.
// exec.CommandContext kills the process and cmd.Run then reports an ordinary
// *exec.ExitError, so reading the exit code before the deadline would put
// `clauth status --json: signal: killed` on the account row -- a deadlock
// presented as a verdict about the user's credentials.
func TestAHangingCLIIsNotReportedAsAFailedCommand(t *testing.T) {
	bin := scriptClauth(t, "sleep 30\n")
	shrinkCLITimeout(t, 150*time.Millisecond)

	var err error
	within(t, 5*time.Second, "Load", func() {
		_, err = Load(context.Background(), LoadOpts{CLIBin: bin, Now: time.Now})
	})

	if err == nil {
		t.Fatal("Load of a hanging clauth = nil error")
	}
	if strings.Contains(err.Error(), "signal: killed") {
		t.Errorf("Load error = %v, want one naming the deadline rather than the kill", err)
	}
}

// A clauth whose CHILD outlives it must still return on time: the kill goes
// to the process herdr-draft started, and a grandchild holding the inherited
// write end of the stdout pipe keeps cmd.Wait's copier blocked, so without
// cliWaitDelay the deadline bounds nothing that matters.
func TestACLIWhoseChildOutlivesItStillReturnsOnTime(t *testing.T) {
	bin := scriptClauth(t, "sleep 30 &\nsleep 30\n")
	shrinkCLITimeout(t, 150*time.Millisecond)

	var err error
	within(t, 5*time.Second, "Load", func() {
		_, err = Load(context.Background(), LoadOpts{CLIBin: bin, Now: time.Now})
	})

	if err == nil {
		t.Fatal("Load of a hanging clauth = nil error")
	}
}

// The other half, and the one a bare `if err := cmd.Run(); err != nil` gets
// wrong: a clauth that WORKED, whose grandchild happens to hold the pipe
// open past the grace. exec.ErrWaitDelay is returned only when no Cancel
// call has occurred and the command exited successfully, so reading it as a
// failure would make the account row say clauth is broken when it is not.
//
// The two budgets are kept in their production RELATIONSHIP rather than both
// shrunk: they measure different things -- the deadline from the start of
// the command, the grace from the moment Wait observes it exited -- so with
// both at the same value an instantly-exiting command expires the deadline
// first and is correctly reported as a timeout about nothing.
func TestAWorkingCLIWhoseChildHoldsThePipeStillParses(t *testing.T) {
	payload := `{"schema":1,"active_profile":"alpha","generated_at":"2026-08-31T21:29:00+00:00","refresh_interval_ms":90000,"profiles":[{"name":"alpha","active":true,"tier":"Team","auth_status":"ok","windows":[]}]}`
	bin := scriptClauth(t, "sleep 30 &\ncat <<'JSON'\n"+payload+"\nJSON\n")
	setCLIBudgets(t, 5*time.Second, 150*time.Millisecond)

	var st Status
	var err error
	within(t, 10*time.Second, "Load", func() {
		st, err = Load(context.Background(), LoadOpts{CLIBin: bin, Now: time.Now})
	})

	if err != nil {
		t.Fatalf("Load of a working clauth = %v, want the status", err)
	}
	if st.ActiveProfile != "alpha" {
		t.Errorf("ActiveProfile = %q, want alpha", st.ActiveProfile)
	}
}

// A cancelled caller is not a timeout and must not claim to be one.
func TestACancelledCallerIsNotACLITimeout(t *testing.T) {
	bin := scriptClauth(t, "sleep 30\n")
	setCLIBudgets(t, time.Hour, 150*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	defer cancel()

	var err error
	within(t, 5*time.Second, "Load", func() {
		_, err = Load(ctx, LoadOpts{CLIBin: bin, Now: time.Now})
	})

	if err == nil {
		t.Fatal("Load with a cancelled caller = nil error")
	}
	if strings.Contains(err.Error(), "no answer within") {
		t.Errorf("Load error = %v, want one that does not claim a deadline ran out", err)
	}
}

// The budget is spent only when the CLI is actually reached. A fresh status
// file answers without a subprocess at all, which is the common case every
// time the popup opens -- so the bound must not put a deadline in front of
// it, nor a stub clauth's behaviour in front of the file's contents.
func TestAFreshStatusFileSpendsNoBudget(t *testing.T) {
	dir := t.TempDir()
	statusFile := filepath.Join(dir, "status.json")
	if err := os.WriteFile(statusFile, fixtureBytes(t), 0o644); err != nil {
		t.Fatal(err)
	}
	// generated_at + 2*refresh_interval_ms; see TestLoadPrefersFreshStatusFile.
	now := time.Date(2026, 8, 31, 20, 57, 0, 0, time.UTC)
	bin := scriptClauth(t, "sleep 30\n")
	shrinkCLITimeout(t, 150*time.Millisecond)

	var st Status
	var err error
	within(t, 5*time.Second, "Load", func() {
		st, err = Load(context.Background(), LoadOpts{
			StatusFile: statusFile,
			CLIBin:     bin,
			Now:        func() time.Time { return now },
		})
	})

	if err != nil {
		t.Fatalf("Load with a fresh status file = %v, want the file's status", err)
	}
	if st.ActiveProfile != "alpha" {
		t.Errorf("ActiveProfile = %q, want alpha (from the file, with no CLI run)", st.ActiveProfile)
	}
}

// The timeout message carries this package's own error prefix, because
// app.clauthUnavailableReason strips exactly that before putting the reason
// on the one-line account row. A message without it renders with the prefix
// still on it, where the cells are scarcest. Matched on the other side by
// app's TestTimeoutReasonsRenderAsOneCleanLine.
func TestTheCLITimeoutMessageCarriesThePrefixTheRowStrips(t *testing.T) {
	bin := scriptClauth(t, "sleep 30\n")
	shrinkCLITimeout(t, 150*time.Millisecond)

	var err error
	within(t, 5*time.Second, "Load", func() {
		_, err = Load(context.Background(), LoadOpts{CLIBin: bin, Now: time.Now})
	})

	if err == nil {
		t.Fatal("Load of a hanging clauth = nil error")
	}
	if !strings.HasPrefix(err.Error(), "clauth status --json: ") {
		t.Errorf("Load error = %v, want it to start with the prefix app.clauthUnavailableReason strips", err)
	}
	if strings.ContainsAny(err.Error(), "\n\r") {
		t.Errorf("Load error = %v, want one line", err)
	}
}

// The budget is a fact in several documents and was held to this constant
// by nothing -- see linear.TestTheBudgetsAreWhatTheDocumentsSay, whose
// reasoning this shares and whose location it shares the reason for.
func TestTheBudgetIsWhatTheDocumentsSay(t *testing.T) {
	if cliTimeout != 30*time.Second {
		t.Errorf("the clauth budget is now %s. Three documents name it and no test reads them: "+
			"README.md's [clauth] and Troubleshooting sections, the CHANGELOG entry, and the v2 "+
			"spec's #141 amendment", cliTimeout)
	}
}

// clauth that ANSWERED must keep its answer even when the deadline expired
// while its grandchild was still draining the pipe -- the case the first
// arm of loadFromCLI's switch exists for, and the one whose comment used to
// claim it could not arise.
//
// See linear.TestAnAPIKeyCmdThatAnsweredJustInsideItsBudgetKeepsItsAnswer
// for why it does: cmd.Wait takes Process.Wait first, so a process that
// exits 0 before the deadline records no watcher error and no Cancel, and
// only then does awaitGoroutines start the WaitDelay timer -- which can
// expire after the deadline has passed, leaving runErr == ErrWaitDelay and
// ctx.Err() == DeadlineExceeded true together with the payload in the
// buffer. The two arms are not mutually exclusive, and the order between
// them is what decides whether a working clauth is reported as broken.
// Found in review.
func TestACLIThatAnsweredJustInsideItsBudgetKeepsItsAnswer(t *testing.T) {
	payload := `{"schema":1,"active_profile":"alpha","generated_at":"2026-08-31T21:29:00+00:00","refresh_interval_ms":90000,"profiles":[{"name":"alpha","active":true,"tier":"Team","auth_status":"ok","windows":[]}]}`
	bin := scriptClauth(t, "sleep 30 &\nsleep 0.98\ncat <<'JSON'\n"+payload+"\nJSON\n")
	setCLIBudgets(t, time.Second, 33*time.Millisecond)

	var st Status
	var err error
	within(t, 10*time.Second, "Load", func() {
		st, err = Load(context.Background(), LoadOpts{CLIBin: bin, Now: time.Now})
	})

	if err != nil {
		t.Fatalf("Load = %v, want the status clauth printed just inside the budget", err)
	}
	if st.ActiveProfile != "alpha" {
		t.Errorf("ActiveProfile = %q, want alpha", st.ActiveProfile)
	}
}
