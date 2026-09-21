package create

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ZviBaratz/herdr-draft/internal/clauth"
	"github.com/ZviBaratz/herdr-draft/internal/linear"
)

// linearTimeout is a Linear call that ran out of time, the way
// internal/linear reports one: a message that is one clause, and the
// sentinel to the code that has to branch. A fake whose text merely
// CONTAINED the words would prove nothing, which is the same rule
// herdrErr and codedErr follow for herdr's error codes (#144).
type linearTimeout struct{ msg string }

func (e linearTimeout) Error() string { return e.msg }

func (linearTimeout) Is(target error) bool { return target == linear.ErrTimeout }

// A Linear fetch that did not answer is exit 5, not exit 2. `--issue` has
// nothing to fall back on -- unlike the popup, which renders its cached
// list and carries on, `create` never reads linear-cache.json at all -- so
// the question is load-bearing and the run has to refuse. What it must not
// do is call it usage: exit 2's documented remedy is "fix the command and
// re-run", and there is no typo to find when Linear is not answering.
func TestATimedOutLinearFetchIsNotUsage(t *testing.T) {
	h := newHarness(t)
	h.deps.Linear = &fakeLinear{err: linearTimeout{msg: "linear assigned issues: no answer within 30s"}}

	code := h.run("--issue", "ENG-1", "--title", "t", "--no-worktree")

	if code != ExitCheckTimedOut {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitCheckTimedOut, h.stderr)
	}
	if h.createdAnything() {
		t.Error("a refused create must create nothing")
	}
	if got := h.stdout.String(); got != "" {
		t.Errorf("stdout = %q, want empty as for exit 2 and exit 3", got)
	}
	if !strings.Contains(h.stderr.String(), "--issue ENG-1") {
		t.Errorf("stderr should name the flag it was answering:\n%s", h.stderr)
	}
	if !strings.Contains(h.stderr.String(), "no answer within 30s") {
		t.Errorf("stderr should name what ran out and its budget:\n%s", h.stderr)
	}
}

// The same for the credential helper behind it. `api_key_cmd` is resolved
// only when no Deps.Linear was supplied, so this pins the SORTING rather
// than the bound -- see the boundary note on
// TestTheAPIKeyCmdBoundIsPinnedInItsOwnPackage.
func TestATimedOutAPIKeyCmdIsNotUsage(t *testing.T) {
	if got := refuse(&strings.Builder{}, linearTimeout{msg: "resolve linear api key: api_key_cmd gave no answer within 1m0s"}); got != ExitCheckTimedOut {
		t.Errorf("refuse = %d, want %d", got, ExitCheckTimedOut)
	}
}

// And a timeout is found through however the call site wrapped it, which is
// what lets refuse sort in one place rather than at each site.
func TestRefuseFindsALinearTimeoutThroughAWrap(t *testing.T) {
	wrapped := fmt.Errorf("--issue %s: %w", "ENG-1", linearTimeout{msg: "linear assigned issues: no answer within 30s"})
	if got := refuse(&strings.Builder{}, wrapped); got != ExitCheckTimedOut {
		t.Errorf("refuse of a wrapped timeout = %d, want %d", got, ExitCheckTimedOut)
	}
}

// A clauth read that did not answer does NOT refuse, and that is the
// asymmetry #141's decision turns on rather than an oversight. The pinned
// profile's dead-credential check only QUALIFIES a session that will be
// created either way -- every other clauth failure already prints a line
// and carries on (refuseUnusableProfile returns nil on any error) -- so a
// timeout takes the same posture instead of becoming the one clauth
// failure that kills a run.
func TestATimedOutClauthReadDoesNotRefuse(t *testing.T) {
	h := newHarness(t)
	h.deps.Clauth = &fakeClauth{err: errors.New("clauth status --json: no answer within 30s")}

	code := h.run("--title", "t", "--no-worktree", "--account", "alpha-1")

	if code != ExitOK {
		t.Fatalf("exit = %d, want %d: an unreadable clauth must not stop a create\nstderr: %s", code, ExitOK, h.stderr)
	}
	if !h.createdAnything() {
		t.Error("the session should still have been created")
	}
	if !strings.Contains(h.stderr.String(), "could not check whether alpha-1 can be used") {
		t.Errorf("stderr should say the check was skipped and why:\n%s", h.stderr)
	}
	if !strings.Contains(h.stderr.String(), "no answer within 30s") {
		t.Errorf("stderr should name what ran out and its budget:\n%s", h.stderr)
	}
}

// The same under --dry-run --json: the report still prints, with
// account_usage simply absent, rather than the run refusing.
func TestATimedOutClauthReadStillReportsADryRun(t *testing.T) {
	h := newHarness(t)
	h.deps.Clauth = &fakeClauth{err: errors.New("clauth status --json: no answer within 30s")}

	code := h.run("--title", "t", "--no-worktree", "--account", "alpha-1", "--dry-run", "--json")

	if code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	if !strings.Contains(h.stdout.String(), `"ok"`) {
		t.Errorf("stdout should still carry the dry-run object:\n%s", h.stdout)
	}
	if strings.Contains(h.stdout.String(), `"account_usage"`) {
		t.Errorf("account_usage present without a status to read it from:\n%s", h.stdout)
	}
}

// The boundary, stated rather than left to be assumed. Neither caller of
// linear.ResolveAPIKey or clauth.Load has a seam in front of it -- Deps.Linear
// short-circuits the key resolution entirely, and Bootstrap takes no Linear
// source at all -- and the budgets are package-private vars. So "a real
// hanging api_key_cmd exits 5 from here" is not reachable as a test, and is
// covered by internal/linear's own bound tests plus the recorded run in
// docs/manual-smoke.md. What IS pinned here is the half that lives here: the
// sorting.
func TestTheAPIKeyCmdBoundIsPinnedInItsOwnPackage(t *testing.T) {
	if !errors.Is(linearTimeout{msg: "x"}, linear.ErrTimeout) {
		t.Fatal("this package's own fake no longer names linear's sentinel")
	}
	// And clauth's has no sentinel on purpose: nothing here branches on it.
	var _ clauth.Status
}
