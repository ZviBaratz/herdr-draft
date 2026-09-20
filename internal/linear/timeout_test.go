package linear

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// scriptKeyCmd writes body as an executable /bin/sh script and returns the
// argv an `api_key_cmd` would be. A shell script rather than a bare binary
// on purpose: `pass show ...` and `op read ...` are wrappers that shell out,
// which is the shape picker.pickerWaitDelay's comment calls the normal one.
func scriptKeyCmd(t *testing.T, body string) []string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("stub api_key_cmd is a POSIX shell script")
	}
	bin := filepath.Join(t.TempDir(), "stub-key-cmd")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return []string{bin}
}

// setKeyCmdBudgets shortens the api_key_cmd deadlines for one test and
// restores them afterwards. The production numbers are sixty seconds and
// two, neither of which a test may sit through.
//
// TWO parameters rather than picker.shrinkTimeout's one, because the
// cancellation test needs them apart: it must let the deadline sit far out
// of reach, so that the only thing which can end the call is the caller's
// own cancel -- and the pipe grace must still be short, or the grandchild
// holding stdout keeps cmd.Wait blocked for the whole of it. Measured here:
// with both set to an hour, that test does not come back at all.
func setKeyCmdBudgets(t *testing.T, deadline, waitDelay time.Duration) {
	t.Helper()
	prevTimeout, prevDelay := keyCmdTimeout, keyCmdWaitDelay
	keyCmdTimeout, keyCmdWaitDelay = deadline, waitDelay
	t.Cleanup(func() { keyCmdTimeout, keyCmdWaitDelay = prevTimeout, prevDelay })
}

// shrinkKeyCmdTimeout is the ordinary case: one short number for both, the
// way picker.shrinkTimeout does it. The pipe grace has to shrink WITH the
// deadline, or a command whose children outlive it holds the test open for
// the full delay after the kill.
func shrinkKeyCmdTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	setKeyCmdBudgets(t, d, d)
}

// within runs call on its own goroutine and fails the test if it has not
// come back inside budget, rather than letting an unbounded call hang the
// whole suite for the ten-minute package timeout. The message is the point:
// a test that times out here is reporting the defect, not a flake.
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

// An api_key_cmd that never answers must not hang the caller. It runs
// synchronously in app.Bootstrap, BEFORE the popup is drawn, so with no
// deadline a credential helper waiting on a desktop approval that never
// comes means the form never appears and never says why (#141).
func TestAHangingAPIKeyCmdIsBounded(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "")
	cmd := scriptKeyCmd(t, "sleep 30\n")
	shrinkKeyCmdTimeout(t, 150*time.Millisecond)

	var key string
	var err error
	within(t, 5*time.Second, "ResolveAPIKey", func() {
		key, err = ResolveAPIKey(context.Background(), cmd, "", t.TempDir())
	})

	if err == nil {
		t.Fatalf("ResolveAPIKey of a hanging api_key_cmd = %q, nil; want a deadline error", key)
	}
	if !errors.Is(err, ErrTimeout) {
		t.Errorf("ResolveAPIKey error = %v, want one matching ErrTimeout", err)
	}
}

// And a hang is a MALFUNCTION, never a broken command. exec.CommandContext
// kills the process and cmd.Run then reports an ordinary *exec.ExitError, so
// a runKeyCmd that read the exit code before the deadline would hand the
// issue row `run api_key_cmd: signal: killed` -- a deadlock presented as a
// decision, and a reader sent to debug a command that is fine.
func TestAHangingAPIKeyCmdIsNotReportedAsAFailedCommand(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "")
	cmd := scriptKeyCmd(t, "sleep 30\n")
	shrinkKeyCmdTimeout(t, 150*time.Millisecond)

	var err error
	within(t, 5*time.Second, "ResolveAPIKey", func() {
		_, err = ResolveAPIKey(context.Background(), cmd, "", t.TempDir())
	})

	if err == nil {
		t.Fatal("ResolveAPIKey of a hanging api_key_cmd = nil error")
	}
	if strings.Contains(err.Error(), "signal: killed") {
		t.Errorf("ResolveAPIKey error = %v, want one naming the deadline rather than the kill", err)
	}
	if !strings.Contains(err.Error(), "no answer within 150ms") {
		t.Errorf("ResolveAPIKey error = %v, want one naming what ran out and its budget", err)
	}
}

// A command whose CHILD outlives it must still return on time. The kill goes
// to the process herdr-draft started; a grandchild holding the inherited
// write end of the stdout pipe keeps cmd.Wait's copier blocked, so without
// keyCmdWaitDelay the deadline bounds nothing that matters. `pass` shelling
// out to `gpg` and a `gpg-agent` is exactly this shape.
func TestAnAPIKeyCmdWhoseChildOutlivesItStillReturnsOnTime(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "")
	cmd := scriptKeyCmd(t, "sleep 30 &\nsleep 30\n")
	shrinkKeyCmdTimeout(t, 150*time.Millisecond)

	var err error
	within(t, 5*time.Second, "ResolveAPIKey", func() {
		_, err = ResolveAPIKey(context.Background(), cmd, "", t.TempDir())
	})

	if !errors.Is(err, ErrTimeout) {
		t.Errorf("ResolveAPIKey error = %v, want one matching ErrTimeout", err)
	}
}

// The other half of that, and the one a bare `if err := cmd.Run(); err != nil`
// gets wrong: a command that WORKED, whose grandchild happens to hold the
// pipe open past the delay. exec.ErrWaitDelay is returned only when no Cancel
// call has occurred and the command exited successfully, so reading it as a
// failure turns a working `pass show` into "your api_key_cmd is broken".
func TestAWorkingAPIKeyCmdWhoseChildHoldsThePipeStillGivesTheKey(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "")
	cmd := scriptKeyCmd(t, "sleep 30 &\nprintf 'lin_api_fromcmd\\n'\n")
	// The two budgets are kept in their production RELATIONSHIP rather than
	// both shrunk, and the first draft of this test got that wrong. They
	// measure different things: the deadline runs from the start of the
	// command, the pipe grace from the moment Wait observes it exited. With
	// both at 150ms the command exits at once, so the grace expires ~150ms
	// after the deadline already has -- measured, 4 runs in 5 -- and the
	// call is correctly reported as a timeout. Sixty to two is the shipped
	// ratio; five seconds to 150ms is the same shape, small enough to sit
	// through.
	setKeyCmdBudgets(t, 5*time.Second, 150*time.Millisecond)

	var key string
	var err error
	within(t, 5*time.Second, "ResolveAPIKey", func() {
		key, err = ResolveAPIKey(context.Background(), cmd, "", t.TempDir())
	})

	if err != nil {
		t.Fatalf("ResolveAPIKey of a working api_key_cmd = %v, want the key", err)
	}
	if key != "lin_api_fromcmd" {
		t.Errorf("ResolveAPIKey = %q, want the key its api_key_cmd printed", key)
	}
}

// A cancelled caller is NOT a timeout, and must not claim to be one. The two
// are different events with different remedies -- ⌃C during a headless
// create is the caller's own doing (main.watchSignals cancels the pre-flight
// context), and reporting "no answer within 60s" about a signal that arrived
// in the first second would be false.
func TestACancelledCallerIsNotAnAPIKeyCmdTimeout(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "")
	cmd := scriptKeyCmd(t, "sleep 30\n")
	setKeyCmdBudgets(t, time.Hour, 150*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	defer cancel()

	var err error
	within(t, 5*time.Second, "ResolveAPIKey", func() {
		_, err = ResolveAPIKey(ctx, cmd, "", t.TempDir())
	})

	if err == nil {
		t.Fatal("ResolveAPIKey of a cancelled caller = nil error")
	}
	if errors.Is(err, ErrTimeout) {
		t.Errorf("ResolveAPIKey error = %v, want one that is NOT a timeout", err)
	}
	if strings.Contains(err.Error(), "no answer within") {
		t.Errorf("ResolveAPIKey error = %v, want one that does not claim a deadline ran out", err)
	}
}

// shrinkHTTPTimeout shortens the assignedIssues deadline for one test. One
// number, not two: nothing is spawned here, so there is no pipe grace to
// shrink with it.
func shrinkHTTPTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	prev := httpTimeout
	httpTimeout = d
	t.Cleanup(func() { httpTimeout = prev })
}

// stalledServer answers by never answering, and releases when the test ends
// or the client's own context is done -- httptest.Server.Close waits for
// outstanding handlers, so a handler with no way out would hang the suite at
// cleanup instead of in the call.
func stalledServer(t *testing.T, before func(w http.ResponseWriter)) *httptest.Server {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if before != nil {
			before(w)
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() {
		close(release)
		srv.Close()
	})
	return srv
}

// A Linear API that accepts the connection and never answers must not hang
// the caller (#141). http.DefaultClient has no Timeout of its own, so before
// the bound a proxy that swallowed the request left `create --issue` with no
// output and no exit code, and the popup's background refresh waiting for
// the life of the process.
//
// Deliberately over the DEFAULT client -- no Client.HTTP -- because that is
// the one the production callers build (app.Bootstrap and create's
// Deps.linear both construct &linear.Client{APIKey: key}).
func TestAHangingLinearAPIIsBounded(t *testing.T) {
	srv := stalledServer(t, nil)
	shrinkHTTPTimeout(t, 150*time.Millisecond)

	c := &Client{APIKey: "lin_api_test", Endpoint: srv.URL}
	var err error
	within(t, 5*time.Second, "AssignedIssues", func() {
		_, err = c.AssignedIssues(context.Background())
	})

	if err == nil {
		t.Fatal("AssignedIssues against a server that never answers = nil error")
	}
	if !errors.Is(err, ErrTimeout) {
		t.Errorf("AssignedIssues error = %v, want one matching ErrTimeout", err)
	}
	if !strings.Contains(err.Error(), "no answer within 150ms") {
		t.Errorf("AssignedIssues error = %v, want one naming its budget", err)
	}
}

// The half a transport-only bound would miss: headers arrive, then the body
// stops. This is what pins that the deadline belongs on the REQUEST rather
// than beside it -- the request context "controls the entire lifetime of a
// request and its response: obtaining a connection, sending the request, and
// reading the response headers and body" (go1.26.4 src/net/http/request.go),
// so io.ReadAll is covered and http.Client.Timeout is not needed. Setting
// that as well would be two timers on one deadline, which is the defect
// #272's review caught arriving through another door.
func TestALinearResponseBodyThatNeverFinishesIsBounded(t *testing.T) {
	srv := stalledServer(t, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "4096")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":`))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	})
	shrinkHTTPTimeout(t, 150*time.Millisecond)

	c := &Client{APIKey: "lin_api_test", Endpoint: srv.URL}
	var err error
	within(t, 5*time.Second, "AssignedIssues", func() {
		_, err = c.AssignedIssues(context.Background())
	})

	if err == nil {
		t.Fatal("AssignedIssues against a stalled response body = nil error")
	}
	if !errors.Is(err, ErrTimeout) {
		t.Errorf("AssignedIssues error = %v, want one matching ErrTimeout", err)
	}
}

// And the bound is not a property of the default client, so a caller that
// supplies its own does not escape it. Nothing in the tree injects one in
// production today, but the tests do (client_test.go's srv.Client()), and a
// bound that a field can switch off is not a bound.
func TestTheLinearDeadlineHoldsForAnInjectedClient(t *testing.T) {
	srv := stalledServer(t, nil)
	shrinkHTTPTimeout(t, 150*time.Millisecond)

	c := &Client{HTTP: srv.Client(), APIKey: "lin_api_test", Endpoint: srv.URL}
	var err error
	within(t, 5*time.Second, "AssignedIssues", func() {
		_, err = c.AssignedIssues(context.Background())
	})

	if !errors.Is(err, ErrTimeout) {
		t.Errorf("AssignedIssues error = %v, want one matching ErrTimeout", err)
	}
}

// A cancelled caller is not a timeout here either, for the reason it is not
// one for api_key_cmd: the remedies differ and only one of them is the
// caller's own doing.
func TestACancelledCallerIsNotALinearTimeout(t *testing.T) {
	srv := stalledServer(t, nil)
	shrinkHTTPTimeout(t, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	defer cancel()

	c := &Client{APIKey: "lin_api_test", Endpoint: srv.URL}
	var err error
	within(t, 5*time.Second, "AssignedIssues", func() {
		_, err = c.AssignedIssues(ctx)
	})

	if err == nil {
		t.Fatal("AssignedIssues with a cancelled caller = nil error")
	}
	if errors.Is(err, ErrTimeout) {
		t.Errorf("AssignedIssues error = %v, want one that is NOT a timeout", err)
	}
	if strings.Contains(err.Error(), "no answer within") {
		t.Errorf("AssignedIssues error = %v, want one that does not claim a deadline ran out", err)
	}
}

// Both timeout messages carry this package's own error prefix, because the
// app layer strips exactly those before putting a reason on a one-line row:
// app.linearUnavailableReason drops "resolve linear api key: " and
// app.linearRefreshReason drops "linear assigned issues: ". A message
// without one is not wrong, it just renders with the prefix still on it, in
// the row where the cells are scarcest -- which is invisible from here and
// from there, so it is pinned on this side and matched on that one
// (app's TestTimeoutReasonsRenderAsOneCleanLine).
func TestTimeoutMessagesCarryThePrefixesTheRowStrips(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		prefix string
		reader string
	}{
		{"api_key_cmd", timeout{prefix: keyCmdPrefix, what: "api_key_cmd gave no answer", deadline: keyCmdTimeout}, "resolve linear api key: ", "app.linearUnavailableReason"},
		{"assignedIssues", timeout{prefix: assignedIssuesPrefix, what: "no answer", deadline: httpTimeout}, "linear assigned issues: ", "app.linearRefreshReason"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.HasPrefix(tc.err.Error(), tc.prefix) {
				t.Errorf("%v does not start with %q, which %s strips", tc.err, tc.prefix, tc.reader)
			}
			if strings.ContainsAny(tc.err.Error(), "\n\r") {
				t.Errorf("%v is not one line", tc.err)
			}
		})
	}
}

// The budgets are a fact in several documents and were held to these
// constants by nothing. Changing either leaves the whole suite green while
// README, SECURITY.md, the CHANGELOG, the agent-facing spawn skill and the
// v2 spec's #141 amendment all go on naming the old number -- and the skill
// is the one that matters, because an agent reads it to decide how long to
// wait before concluding a create is stuck.
//
// The guard lives here rather than beside create's
// TestSkillNamesTheCheckBudget for the plain reason that only this package
// can read these two.
func TestTheBudgetsAreWhatTheDocumentsSay(t *testing.T) {
	if keyCmdTimeout != 60*time.Second {
		t.Errorf("api_key_cmd's budget is now %s. Five documents name it and no test reads them: "+
			"internal/skill/spawn_skill.md's exit-5 paragraph, README.md's [linear] and exit-code "+
			"sections, SECURITY.md's api_key_cmd bullet, the CHANGELOG entry, and the v2 spec's "+
			"#141 amendment", keyCmdTimeout)
	}
	if httpTimeout != 30*time.Second {
		t.Errorf("the assignedIssues budget is now %s. The same documents name it", httpTimeout)
	}
}

// A command that ANSWERED must keep its answer even when the deadline
// expired while its grandchild was still draining the pipe.
//
// This is the case the first arm of runKeyCmd's switch exists for, and the
// comment there used to claim it could not arise -- that a run the deadline
// ended has had its Cancel called, so it could never come back as
// ErrWaitDelay. That is false, and this test is the measurement that says
// so. The shape is `pass show`'s: a grandchild on the stdout pipe, and a
// command that answers just inside its budget.
//
// exec's own ordering is why. cmd.Wait takes Process.Wait first, and the
// process really did exit 0 before the deadline, so the watcher records no
// error and no Cancel occurs. Only then does awaitGoroutines start the
// WaitDelay timer, which expires AFTER the deadline has passed -- leaving
// runErr == ErrWaitDelay and ctx.Err() == DeadlineExceeded true at the same
// time, with the key sitting in the buffer. Measured at the shipped 60:2
// ratio scaled to 1s:33ms: 20 runs out of 20, deterministic rather than a
// race.
//
// The numbers here are NOT that ratio, deliberately. Entering the window
// needs two orderings to hold -- the command must exit before the deadline,
// and the pipe grace must outlast it -- and at 1s:33ms each had about 20ms
// of margin. That held 30 runs here under eight spinners and at -cpu=1, and
// 20ms is still the shape of a test that is green on one CI runner and red
// on the other, which is #202's own story. 2s with a 1s grace and an exit
// at 1.5s gives 500ms either side for the same property, and costs two and
// a half seconds.
//
// So the two arms are NOT mutually exclusive and the order between them is
// what decides the outcome. Reading the deadline first throws a working
// helper's key away and reports a timeout; reading the answer first is what
// this pins. Found in review.
func TestAnAPIKeyCmdThatAnsweredJustInsideItsBudgetKeepsItsAnswer(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "")
	cmd := scriptKeyCmd(t, "sleep 30 &\nsleep 1.5\nprintf 'lin_api_justintime\\n'\n")
	setKeyCmdBudgets(t, 2*time.Second, time.Second)

	var key string
	var err error
	within(t, 10*time.Second, "ResolveAPIKey", func() {
		key, err = ResolveAPIKey(context.Background(), cmd, "", t.TempDir())
	})

	if err != nil {
		t.Fatalf("ResolveAPIKey = %v, want the key its api_key_cmd printed just inside the budget", err)
	}
	if key != "lin_api_justintime" {
		t.Errorf("ResolveAPIKey = %q, want the key", key)
	}
}
