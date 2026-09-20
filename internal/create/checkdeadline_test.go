package create

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ZviBaratz/herdr-draft/internal/picker"
)

// These pin #272: every question `create`'s pre-flight asks the filesystem
// or git answers within a bounded time, and a question that does not
// answer refuses the create rather than being read as its own negative.
//
// Before, nothing in the pre-flight could time out -- DirExists is
// os.Stat, IsGitRepo runs git with no context, and the rest were handed
// the caller's context, which since #252 a signal can cancel but nothing
// can expire. So a create driven by the spawn skill, a script or a cron
// entry sat on a stalled mount for as long as the mount stayed stalled:
// no output, no exit code, and the lane that started it waiting for good.

// hangingGit is a GitSource whose filesystem and git calls never answer --
// a stalled mount, in a test.
//
// Each call is stalled INDIVIDUALLY rather than all at once, because the
// pre-flight asks its questions in order and only the first stalled one is
// ever reached: a test that stalled everything would pin the first
// question's bound six times over and the other five not at all.
//
// The blocked goroutine touches none of fakeGit's counters, so a test that
// asserts on those while a call is still hung is not reading them as they
// are written.
type hangingGit struct {
	*fakeGit
	release chan struct{}
	stall   string
	// after is which occurrence of stall to block on, counting from one.
	// It exists for NamesHead, whose own `git rev-parse HEAD` is the
	// SECOND ResolveCommit a chosen base makes: stalling the call outright
	// would stall withBase's and never reach the question under test.
	after int
	seen  int
}

// newHangingGit returns a git that answers everything except call, which
// blocks until the test ends.
func newHangingGit(t *testing.T, call string) *hangingGit {
	t.Helper()
	g := &hangingGit{fakeGit: newFakeGit(), release: make(chan struct{}), stall: call, after: 1}
	g.commits = repoCommitsIn("/projects/thing")
	t.Cleanup(func() { close(g.release) })
	return g
}

func (g *hangingGit) hang(call string) {
	if call != g.stall {
		return
	}
	g.seen++
	if g.seen >= g.after {
		<-g.release
	}
}

func (g *hangingGit) DirExists(path string) bool {
	g.hang("DirExists")
	return g.fakeGit.DirExists(path)
}

func (g *hangingGit) IsGitRepo(dir string) bool {
	g.hang("IsGitRepo")
	return g.fakeGit.IsGitRepo(dir)
}

func (g *hangingGit) RepoRoot(ctx context.Context, dir string) (string, error) {
	g.hang("RepoRoot")
	return g.fakeGit.RepoRoot(ctx, dir)
}

func (g *hangingGit) PrimaryCheckout(ctx context.Context, dir string) (string, error) {
	g.hang("PrimaryCheckout")
	return g.fakeGit.PrimaryCheckout(ctx, dir)
}

func (g *hangingGit) BranchExists(ctx context.Context, dir, name string) (bool, error) {
	g.hang("BranchExists")
	return g.fakeGit.BranchExists(ctx, dir, name)
}

func (g *hangingGit) ResolveCommit(ctx context.Context, dir, ref string) (string, error) {
	g.hang("ResolveCommit")
	return g.fakeGit.ResolveCommit(ctx, dir, ref)
}

// hungHarness is a harness whose git stalls on one named call and whose
// deadline is short enough for a test to wait it out.
func hungHarness(t *testing.T, call string) *harness {
	t.Helper()
	h, _ := hungHarnessGit(t, call)
	return h
}

// hungHarnessGit is hungHarness with the git handed back, for the one test
// that has to reach past the call name into which occurrence stalls.
func hungHarnessGit(t *testing.T, call string) (*harness, *hangingGit) {
	t.Helper()
	h := newHarness(t)
	g := newHangingGit(t, call)
	// One object behind both fields: h.git is what a test reads counters
	// from, h.deps.Git is what the run calls.
	h.git = g.fakeGit
	h.deps.Git = g
	h.deps.CheckDeadline = 20 * time.Millisecond
	return h, g
}

// runWithin runs one `create` and returns its exit code, failing the test
// if it does not come back. Every test here runs against a git that never
// answers, so a create that does not return is the defect itself and must
// not hang the suite.
func runWithin(t *testing.T, h *harness, args ...string) int {
	t.Helper()
	got := make(chan int, 1)
	go func() { got <- h.run(args...) }()
	select {
	case code := <-got:
		return code
	case <-time.After(5 * time.Second):
		t.Fatal("the create never came back: its pre-flight is not bounded by anything")
		return 0
	}
}

// TestCheckDeadline_IsGitRepoThatNeverAnswers is the issue's own shape:
// `git rev-parse --is-inside-work-tree` against a stalled mount. It is the
// second question the pre-flight asks and the first that could stall for
// real, which is why #202 used it as its live lever too.
func TestCheckDeadline_IsGitRepoThatNeverAnswers(t *testing.T) {
	h := hungHarness(t, "IsGitRepo")

	code := runWithin(t, h, "--title", "fix login redirect")

	if code != ExitCheckTimedOut {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitCheckTimedOut, h.stderr)
	}
	if got := h.stderr.String(); !strings.Contains(got, "timed out") || !strings.Contains(got, "git repository") {
		t.Errorf("stderr = %q, want it to say the check timed out and which check it was", got)
	}
	if len(h.runner.calls) != 0 {
		t.Errorf("calls = %v, want nothing created", h.runner.calls)
	}
}

// TestCheckDeadline_DirExistsThatNeverAnswers: the first question the
// pre-flight asks, and an os.Stat, so a context could not reach it even in
// principle -- which is why the bound is on the answer.
func TestCheckDeadline_DirExistsThatNeverAnswers(t *testing.T) {
	h := hungHarness(t, "DirExists")

	code := runWithin(t, h, "--title", "fix login redirect")

	if code != ExitCheckTimedOut {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitCheckTimedOut, h.stderr)
	}
	if got := h.stderr.String(); !strings.Contains(got, "/projects/thing exists") {
		t.Errorf("stderr = %q, want it to name the question that timed out", got)
	}
}

// TestCheckDeadline_RepoRootThatNeverAnswers. loadTiers treats a RepoRoot
// error as not fatal on purpose; a timeout is the exception, because
// carrying on re-keys the per-project memory to a path and drops the
// repository's own .herdr-draft.toml.
func TestCheckDeadline_RepoRootThatNeverAnswers(t *testing.T) {
	h := hungHarness(t, "RepoRoot")

	code := runWithin(t, h, "--title", "fix login redirect")

	if code != ExitCheckTimedOut {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitCheckTimedOut, h.stderr)
	}
	if got := h.stderr.String(); !strings.Contains(got, "repository root") {
		t.Errorf("stderr = %q, want it to name the question that timed out", got)
	}
	if h.createdAnything() {
		t.Errorf("calls = %v, want nothing created", h.runner.calls)
	}
}

// TestCheckDeadline_PrimaryCheckoutThatNeverAnswers: the same exception,
// for the question #171 added.
func TestCheckDeadline_PrimaryCheckoutThatNeverAnswers(t *testing.T) {
	h := hungHarness(t, "PrimaryCheckout")

	code := runWithin(t, h, "--title", "fix login redirect")

	if code != ExitCheckTimedOut {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitCheckTimedOut, h.stderr)
	}
	if got := h.stderr.String(); !strings.Contains(got, "primary checkout") {
		t.Errorf("stderr = %q, want it to name the question that timed out", got)
	}
}

// TestCheckDeadline_BranchExistsThatNeverAnswers: the duplicate-branch
// check, which runs after the reachability probe -- so this is also the one
// that proves a timeout AFTER herdr has been reached still creates nothing.
func TestCheckDeadline_BranchExistsThatNeverAnswers(t *testing.T) {
	h := hungHarness(t, "BranchExists")

	code := runWithin(t, h, "--title", "fix login redirect", "--worktree")

	if code != ExitCheckTimedOut {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitCheckTimedOut, h.stderr)
	}
	if got := h.stderr.String(); !strings.Contains(got, "already exists") {
		t.Errorf("stderr = %q, want it to name the question that timed out", got)
	}
	if h.createdAnything() {
		t.Errorf("calls = %v, want nothing created", h.runner.calls)
	}
}

// TestCheckDeadline_ResolveCommitThatNeverAnswers: the base a chosen
// --base names, resolved in the project (#194).
func TestCheckDeadline_ResolveCommitThatNeverAnswers(t *testing.T) {
	h := hungHarness(t, "ResolveCommit")

	code := runWithin(t, h, "--title", "fix login redirect", "--worktree", "--base", "main")

	if code != ExitCheckTimedOut {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitCheckTimedOut, h.stderr)
	}
	if got := h.stderr.String(); !strings.Contains(got, `resolving base "main"`) {
		t.Errorf("stderr = %q, want it to name the question that timed out", got)
	}
}

// TestCheckDeadline_SettleBaseThatNeverAnswers is the first of the two
// COMPOSITES, and the reason they are bounded as a unit: app.SettleBase
// turns its ResolveCommit's error into a verdict of its own. Bounding it
// any deeper would hand it a timeout dressed as "no such commit here" and
// drop a tier's base over a question git never answered.
func TestCheckDeadline_SettleBaseThatNeverAnswers(t *testing.T) {
	h := hungHarness(t, "ResolveCommit")
	rememberBase(t, h, "main")

	code := runWithin(t, h, "--title", "fix login redirect", "--worktree")

	if code != ExitCheckTimedOut {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitCheckTimedOut, h.stderr)
	}
	if got := h.stderr.String(); !strings.Contains(got, `settling base "main"`) {
		t.Errorf("stderr = %q, want it to name the question that timed out", got)
	}
	if got := h.stderr.String(); strings.Contains(got, "using HEAD") {
		t.Errorf("stderr = %q, want no base dropped: nobody established that it does not resolve", got)
	}
	if h.createdAnything() {
		t.Errorf("calls = %v, want nothing created", h.runner.calls)
	}
}

// TestCheckDeadline_NamesHeadThatNeverAnswers is the other composite, whose
// own `git rev-parse HEAD` is the second ResolveCommit a chosen base makes
// -- hence `after`. app.NamesHead turns its error into a plain false, so
// the same reasoning applies.
func TestCheckDeadline_NamesHeadThatNeverAnswers(t *testing.T) {
	h, g := hungHarnessGit(t, "ResolveCommit")
	g.after = 2

	code := runWithin(t, h, "--title", "fix login redirect", "--worktree", "--base", "HEAD^0")

	if code != ExitCheckTimedOut {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitCheckTimedOut, h.stderr)
	}
	if got := h.stderr.String(); !strings.Contains(got, "is HEAD in") {
		t.Errorf("stderr = %q, want it to name the question that timed out", got)
	}
}

// --- unknown is not the verdict's negative --------------------------------
//
// #202's other half, and the one that matters more here than in the popup:
// three of these questions currently read their own failure as an answer,
// so a timeout that was not refused would not hang a create -- it would
// hand back a DIFFERENT session and call it success.

// TestCheckDeadline_ATimedOutDirCheckIsNotAMissingDirectory. A stat that
// never answered is not a directory that does not exist, and only one of
// the two is fixed by passing a different --project.
func TestCheckDeadline_ATimedOutDirCheckIsNotAMissingDirectory(t *testing.T) {
	h := hungHarness(t, "DirExists")

	code := runWithin(t, h, "--title", "fix login redirect")

	if code == ExitUsage {
		t.Fatalf("exit = %d: a check nobody answered was reported as a bad invocation\nstderr: %s", code, h.stderr)
	}
	if code != ExitCheckTimedOut {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitCheckTimedOut, h.stderr)
	}
	if got := h.stderr.String(); strings.Contains(got, "does not exist") {
		t.Errorf("stderr = %q, want it not to claim the directory is missing", got)
	}
}

// TestCheckDeadline_ATimedOutRepoCheckDoesNotDropTheWorktree is the
// sharpest of the three. gitx.IsGitRepo answers false for every failure to
// invoke git, and loadTiers reads that as "not a repository", which turns
// `--worktree` silently OFF. A create that swallowed the timeout would
// report exit 0 over a session in the wrong place.
func TestCheckDeadline_ATimedOutRepoCheckDoesNotDropTheWorktree(t *testing.T) {
	h := hungHarness(t, "IsGitRepo")

	code := runWithin(t, h, "--title", "fix login redirect", "--worktree")

	if code == ExitOK {
		t.Fatalf("a create whose repository check never answered made a session anyway: %v", h.runner.calls)
	}
	if code != ExitCheckTimedOut {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitCheckTimedOut, h.stderr)
	}
	if h.createdAnything() {
		t.Errorf("calls = %v, want nothing created", h.runner.calls)
	}
}

// TestCheckDeadline_ATimedOutBranchCheckDoesNotReadAsFree. The error from
// this one is discarded deliberately -- a git that says "I cannot tell"
// leaves the branch to herdr, which refuses a checkout that is really
// taken. A question NOBODY answered is the other thing: reading it as "the
// branch is free" is how a create walks into old work, because herdr v0.9.0
// checks an existing local branch OUT rather than refusing it.
func TestCheckDeadline_ATimedOutBranchCheckDoesNotReadAsFree(t *testing.T) {
	h := hungHarness(t, "BranchExists")

	code := runWithin(t, h, "--title", "fix login redirect", "--worktree")

	if code == ExitOK {
		t.Fatalf("a create whose branch check never answered made a session anyway: %v", h.runner.calls)
	}
	if code != ExitCheckTimedOut {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitCheckTimedOut, h.stderr)
	}
	if h.createdAnything() {
		t.Errorf("calls = %v, want nothing created", h.runner.calls)
	}
}

// TestCheckDeadline_DryRunRefusesTheSameWay: --dry-run does the whole
// pre-flight and stops before plan.Execute, so it hung identically before
// this and must refuse identically now. It also spends no pick, because the
// refusal comes before the picker is asked at all.
func TestCheckDeadline_DryRunRefusesTheSameWay(t *testing.T) {
	h := hungHarness(t, "DirExists")
	p := &fakePicker{res: picker.Result{Profile: "alpha-1", ConfigDir: "/dirs/alpha-1"}}
	h.deps.Picker = p

	code := runWithin(t, h, "--title", "fix login redirect", "--account", "auto", "--dry-run", "--json")

	if code != ExitCheckTimedOut {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitCheckTimedOut, h.stderr)
	}
	if len(p.calls) != 0 {
		t.Errorf("picker calls = %d, want none: the refusal comes first", len(p.calls))
	}
	if got := h.stdout.String(); got != "" {
		t.Errorf("stdout = %q, want nothing -- as for exit 2 and exit 3", got)
	}
}

// TestCheckDeadline_ACancelledCallerIsNotATimeout. The deadline is ours,
// the cancellation is the caller's, and only the first is a timeout.
// Reporting "timed out after 30s" about a signal that arrived in the first
// second would be false -- and giving up on the pre-flight there would take
// the account pick with it, which #252 deliberately lets happen so the
// cancel can kill the picker before it writes its ledger.
func TestCheckDeadline_ACancelledCallerIsNotATimeout(t *testing.T) {
	h := newHarness(t)
	p := &fakePicker{res: picker.Result{Profile: "alpha-1", ConfigDir: "/dirs/alpha-1"}, obeyCtx: true}
	h.deps.Picker = p

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	code := h.runCtx(ctx, "--title", "fix login", "--account", "auto", "--no-worktree")

	if code == ExitCheckTimedOut {
		t.Fatalf("a cancelled create reported a timeout\nstderr: %s", h.stderr)
	}
	if got := h.stderr.String(); strings.Contains(got, "timed out after") {
		t.Errorf("stderr = %q, want no timeout claimed: nothing timed out", got)
	}
	if len(p.calls) != 1 {
		t.Fatalf("picker calls = %d, want the one commit pick still reached (#252)", len(p.calls))
	}
}

// cancelAwareGit answers the moment its context is done, with that
// context's own error -- a git that honours cancellation promptly, which
// gitx.anyRefExists behind BranchExists very nearly is: it sets no
// WaitDelay and gives exec no pipes to drain, so its call comes back as
// soon as the kill is reaped.
type cancelAwareGit struct{ *fakeGit }

func (g *cancelAwareGit) ResolveCommit(ctx context.Context, _, _ string) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

// TestCheckDeadline_ACallTheDeadlineKilledIsStillATimeout.
//
// The deadline must exist in exactly ONE place. Give the call its own
// timer as well and the two fire together, so which of them is observed
// first decides whether the run reports "the check did not answer" or the
// killed call's own error -- and this repository has been bitten twice by
// deciding anything on two things being ready at once. The second reading
// is the damaging one: a cancelled ResolveCommit becomes "--base names no
// commit in <dir>", which blames a ref that may be perfectly fine, and the
// same shape elsewhere becomes "no root" and "the branch is free".
//
// So the call is cancelled by RETURNING rather than by a timer of its own:
// bounded's deferred cancel fires after the verdict, which is an ordering
// and not a race. This fake makes the difference deterministic -- it comes
// back the instant its context is done, so a call-side timer that fired
// first would win every time instead of almost never.
func TestCheckDeadline_ACallTheDeadlineKilledIsStillATimeout(t *testing.T) {
	h := newHarness(t)
	h.deps.Git = &cancelAwareGit{fakeGit: h.git}
	h.deps.CheckDeadline = 20 * time.Millisecond

	code := runWithin(t, h, "--title", "fix login redirect", "--worktree", "--base", "main")

	if code == ExitUsage {
		t.Fatalf("exit = %d: the deadline's own kill was reported as a bad --base\nstderr: %s", code, h.stderr)
	}
	if code != ExitCheckTimedOut {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitCheckTimedOut, h.stderr)
	}
	if got := h.stderr.String(); strings.Contains(got, "names no commit") {
		t.Errorf("stderr = %q, want no claim about the ref: nobody established anything about it", got)
	}
}
