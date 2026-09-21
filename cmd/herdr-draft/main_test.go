package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ZviBaratz/herdr-draft/internal/app"
	"github.com/ZviBaratz/herdr-draft/internal/create"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
	"github.com/ZviBaratz/herdr-draft/internal/skill"
	"testing"
)

// TestClauthStatusFilePath pins the one piece of pure, testable logic in
// this file: the resolved path always ends in ".clauth/status.json" under
// whatever home directory os.UserHomeDir() reports on this machine (a real
// call -- there is no portable way to fake it without an env var this
// function doesn't accept -- but the assertion itself never depends on the
// actual value, only the suffix shape).
// reraiseProbeEnv marks the child this package re-executes to watch reraise
// kill something for real; see TestReraiseKillsThisProcessWithTheSignal.
const reraiseProbeEnv = "HERDR_DRAFT_RERAISE_PROBE"

func TestClauthStatusFilePath(t *testing.T) {
	got := clauthStatusFilePath()
	if got == "" {
		t.Skip("os.UserHomeDir() could not be determined in this environment")
	}
	want := filepath.Join(".clauth", "status.json")
	if !strings.HasSuffix(got, want) {
		t.Errorf("clauthStatusFilePath() = %q, want a path ending in %q", got, want)
	}
}

// TestDispatch pins v2 spec §13's routing rule -- "absent means the popup,
// exactly as today; an unknown verb prints usage and exits 2" -- without
// starting a tea.Program or talking to herdr, which is the whole reason
// dispatch takes its two entry points as arguments.
func TestDispatch(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string

		wantCode       int
		wantPopup      bool
		wantCreateArgs []string
		wantStdout     string
		wantStderr     string
	}{
		{
			name:      "no arguments opens the popup",
			args:      nil,
			wantCode:  0,
			wantPopup: true,
		},
		{
			name:           "create forwards the rest of the command line",
			args:           []string{"create", "--title", "t", "--json"},
			wantCode:       0,
			wantCreateArgs: []string{"--title", "t", "--json"},
		},
		{
			name:           "create with no flags still reaches the verb",
			args:           []string{"create"},
			wantCode:       0,
			wantCreateArgs: []string{},
		},
		{
			name:       "an unknown verb exits 2 with the usage",
			args:       []string{"summon"},
			wantCode:   2,
			wantStderr: "unknown command \"summon\"",
		},
		{
			name:       "help exits 0 on stdout",
			args:       []string{"help"},
			wantCode:   0,
			wantStdout: "usage:",
		},
		{
			name:       "--help is not an unknown verb",
			args:       []string{"--help"},
			wantCode:   0,
			wantStdout: "usage:",
		},
		// All three spellings, for the same reason help takes three:
		// asking a program what it is should not require guessing which
		// convention its author picked. A user who guesses wrong on a
		// binary that has no --version at all gets `unknown command` and
		// exit 2, which reads as "this program has no version" rather
		// than "try the other spelling".
		{
			name:       "version exits 0 on stdout",
			args:       []string{"version"},
			wantCode:   0,
			wantStdout: "herdr-draft " + herdrc.Version,
		},
		{
			name:       "--version is not an unknown verb",
			args:       []string{"--version"},
			wantCode:   0,
			wantStdout: "herdr-draft " + herdrc.Version,
		},
		{
			name:       "-V is not an unknown verb",
			args:       []string{"-V"},
			wantCode:   0,
			wantStdout: "herdr-draft " + herdrc.Version,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr strings.Builder
			popupRan := false
			var createArgs []string
			createRan := false

			code := dispatch(tc.args, &stdout, &stderr,
				func() int { popupRan = true; return 0 },
				func(args []string) int { createRan, createArgs = true, args; return 0 },
				func([]string) int { t.Fatal("the skill verb ran"); return 0 },
			)

			if code != tc.wantCode {
				t.Errorf("exit = %d, want %d", code, tc.wantCode)
			}
			if popupRan != tc.wantPopup {
				t.Errorf("popup ran = %v, want %v", popupRan, tc.wantPopup)
			}
			if wantCreate := tc.wantCreateArgs != nil; createRan != wantCreate {
				t.Errorf("create ran = %v, want %v", createRan, wantCreate)
			}
			if tc.wantCreateArgs != nil && !reflect.DeepEqual(createArgs, tc.wantCreateArgs) {
				t.Errorf("create args = %v, want %v", createArgs, tc.wantCreateArgs)
			}
			if tc.wantStdout != "" && !strings.Contains(stdout.String(), tc.wantStdout) {
				t.Errorf("stdout = %q, want it to contain %q", stdout.String(), tc.wantStdout)
			}
			if tc.wantStderr != "" && !strings.Contains(stderr.String(), tc.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tc.wantStderr)
			}
			if tc.wantCode == 2 && !strings.Contains(stderr.String(), "usage:") {
				t.Errorf("stderr = %q, want the usage alongside the refusal", stderr.String())
			}
		})
	}
}

// TestVersionStringCarriesWhatABugReportNeeds pins the content, not the
// layout. The point of adding a version at all was that a user could
// answer "what are you running?" and a bug report could state it, so the
// two facts nobody can reconstruct from the version alone have to be
// there: which plugin id this binary answers to -- forks install side by
// side under different ids -- and what compiled it.
func TestVersionStringCarriesWhatABugReportNeeds(t *testing.T) {
	got := versionString()

	for _, want := range []string{
		"herdr-draft " + herdrc.Version,
		herdrc.PluginID,
		runtime.Version(),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("versionString() does not contain %q:\n%s", want, got)
		}
	}
}

// TestVersionStringOmitsAnUnstampedBuild covers the case an INSTALLED
// plugin is always in. herdr's own [[build]] runs a plain argv with no
// shell, so it cannot compute a `git describe` and main.build stays empty
// -- an empty "build" line would be a field that looks broken rather than
// absent.
func TestVersionStringOmitsAnUnstampedBuild(t *testing.T) {
	saved := build
	t.Cleanup(func() { build = saved })

	build = ""
	if got := versionString(); strings.Contains(got, "build") {
		t.Errorf("versionString() with no stamp mentions a build:\n%s", got)
	}

	build = "v0.1.0-3-gabc1234"
	if got := versionString(); !strings.Contains(got, "v0.1.0-3-gabc1234") {
		t.Errorf("versionString() dropped the stamped build:\n%s", got)
	}
}

// TestDispatchRoutesSkill covers the third verb. It is a separate function
// rather than a row in TestDispatch's table because the table's other rows
// assert the skill verb does NOT run, and a case that inverts that reads
// better on its own than as a fourth "want" column.
func TestDispatchRoutesSkill(t *testing.T) {
	var stdout, stderr strings.Builder
	var got []string
	called := 0
	skillVerb := func(args []string) int { called++; got = args; return 0 }

	code := dispatch([]string{"skill", "--check", "/p/SKILL.md"}, &stdout, &stderr,
		func() int { t.Fatal("popup ran for `skill`"); return 0 },
		func([]string) int { t.Fatal("create ran for `skill`"); return 0 },
		skillVerb)

	if code != 0 {
		t.Errorf("dispatch(skill) = %d, want 0", code)
	}
	if called != 1 {
		t.Error("dispatch did not route `skill` to the skill verb")
	}
	// The verb's own arguments, and only those (#311).
	if want := []string{"--check", "/p/SKILL.md"}; !slices.Equal(got, want) {
		t.Errorf("the skill verb got %q, want %q", got, want)
	}
}

// TestRunSkillStampsTheBuildersVersion pins the one thing skill.Run itself
// cannot: that this call site hands it herdrc.Version and not some other
// string. Every test in package skill passes with a call site supplying
// "dev", and the stamp is the only thing that tells a user their installed
// copy is stale.
func TestRunSkillStampsTheBuildersVersion(t *testing.T) {
	var stdout, stderr strings.Builder

	if code := runSkill(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("runSkill = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), herdrc.Version) {
		t.Errorf("the rendered skill does not carry herdrc.Version (%q) -- "+
			"an installed copy would never look stale", herdrc.Version)
	}
}

// TestRunSkillChecksTheRealFile pins what skill.Run cannot: that this call
// site hands --check a reader that reads the file (#311). Every test in
// package skill passes with a reader that serves a string.
func TestRunSkillChecksTheRealFile(t *testing.T) {
	var doc strings.Builder
	if code := runSkill(nil, &doc, io.Discard); code != 0 {
		t.Fatalf("runSkill = %d, want 0", code)
	}
	path := filepath.Join(t.TempDir(), "SKILL.md")
	if err := os.WriteFile(path, []byte(doc.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr strings.Builder
	if code := runSkill([]string{"--check", path}, &stdout, &stderr); code != 0 {
		t.Errorf("runSkill --check on what it just printed = %d, want 0\nstdout: %s\nstderr: %s", code, stdout.String(), stderr.String())
	}
	if err := os.WriteFile(path, []byte(doc.String()+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if code := runSkill([]string{"--check", path}, &stdout, io.Discard); code != 1 {
		t.Errorf("runSkill --check on an edited copy = %d, want 1: %s", code, stdout.String())
	}
}

// TestVersionPrintsTheSkillDigest is the other half of the footer's claim
// that `version` prints the skill value it carries (#311); package create's
// TestSkillFooterNamesTheDigest holds the footer to skill.Digest().
func TestVersionPrintsTheSkillDigest(t *testing.T) {
	if want := "  skill      " + skill.Digest() + "\n"; !strings.Contains(versionString(), want) {
		t.Errorf("versionString() = %q, want a line %q", versionString(), want)
	}
}

// --- quitting takes the picker with it (#211) -------------------------------

// The popup's LAST act is to cancel the work it started and wait for that
// cancellation to be delivered. Cancelling alone is not enough and this is the
// test that says so: exec.CommandContext kills the picker from a watcher
// goroutine, so a process that cancels and exits in the same breath can exit
// first -- and a picker that survives its caller goes on to write the ledger
// entry #211 is about.
func TestQuittingWaitsForTheCancellationToLand(t *testing.T) {
	lt := app.NewLifetime()
	started, returned := make(chan struct{}), make(chan struct{})
	go func() {
		ctx, done := lt.Begin()
		defer done()
		close(started)
		<-ctx.Done()
		// Stands in for the kill: work that happens after the cancellation
		// and before the call returns. Well inside the shutdown grace.
		time.Sleep(80 * time.Millisecond)
		close(returned)
	}()
	<-started

	var stderr bytes.Buffer
	if code := runProgram(&stderr, lt, func() error { return nil }); code != 0 {
		t.Fatalf("runProgram of a clean popup returned %d, want 0 (stderr: %s)", code, stderr.String())
	}
	select {
	case <-returned:
	default:
		t.Fatal("the process was ready to exit while the cancelled pick was still running -- the picker would outlive the popup")
	}
}

// And a popup that fails is still a popup that quit: the exit code is the
// program's, but the work it started is cancelled either way.
func TestAFailedRunStillCancelsTheWork(t *testing.T) {
	lt := app.NewLifetime()
	ctx, done := lt.Begin()
	defer done()

	var stderr bytes.Buffer
	if code := runProgram(&stderr, lt, func() error { return errors.New("terminal is not a terminal") }); code != 1 {
		t.Fatalf("runProgram of a failed popup returned %d, want 1", code)
	}
	if err := ctx.Err(); !errors.Is(err, context.Canceled) {
		t.Fatalf("after a failed run the work's context reported %v, want context.Canceled", err)
	}
	if !strings.Contains(stderr.String(), "terminal is not a terminal") {
		t.Fatalf("stderr = %q, want the program's own error", stderr.String())
	}
}

// --- a signalled create takes the picker with it (#252) ---------------------

// The policy, in order: announce that a signal arrived, cancel the pre-flight
// so the account picker is killed with it, wait long enough for that kill to
// land, publish the signal for the main goroutine, and only then die.
func TestSignalWatchCancelsThenDiesWithTheSameSignal(t *testing.T) {
	const grace = 80 * time.Millisecond
	sigs := make(chan os.Signal, 1)
	var mu sync.Mutex
	var order []string
	var died os.Signal
	var cancelledAt, diedAt time.Time
	dead := make(chan struct{})

	w := watchSignals(sigs,
		func() { mu.Lock(); defer mu.Unlock(); order = append(order, "cancel"); cancelledAt = time.Now() },
		grace,
		func(s os.Signal) {
			mu.Lock()
			defer mu.Unlock()
			order = append(order, "die")
			died, diedAt = s, time.Now()
			close(dead)
		})

	sigs <- syscall.SIGTERM

	// begun must close as soon as the signal is seen, well before the grace
	// is spent: it is what stops the main goroutine exiting underneath the
	// teardown, which is the whole of #252's review finding.
	select {
	case <-w.begun:
	case <-time.After(5 * time.Second):
		t.Fatal("begun never closed -- main has no way to know a signal is being acted on")
	}

	select {
	case <-dead:
	case <-time.After(5 * time.Second):
		t.Fatal("the teardown never reached the dying half")
	}

	mu.Lock()
	defer mu.Unlock()
	if want := []string{"cancel", "die"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v -- dying first would leave the picker running, which is the bug", order, want)
	}
	if died != syscall.SIGTERM {
		t.Fatalf("died with %v, want the signal that was sent", died)
	}
	if waited := diedAt.Sub(cancelledAt); waited < grace {
		t.Fatalf("waited %s between the cancel and the death, want at least the %s grace -- exec.CommandContext kills the picker from a watcher goroutine that has to be scheduled first", waited, grace)
	}
}

// A closed channel is not a signal. Nothing is announced, nothing is
// cancelled and nothing dies -- the alternative is a create that kills itself
// on the way out of an ordinary successful run.
func TestSignalWatchIgnoresAClosedChannel(t *testing.T) {
	sigs := make(chan os.Signal)
	close(sigs)
	cancelled, died := false, false

	w := watchSignals(sigs, func() { cancelled = true }, time.Second, func(os.Signal) { died = true })
	time.Sleep(20 * time.Millisecond)

	if cancelled || died {
		t.Fatalf("a closed channel produced cancel=%v die=%v, want neither", cancelled, died)
	}
	select {
	case <-w.begun:
		t.Fatal("a closed channel announced a signal that never arrived")
	default:
	}
}

// And the half the review of #257 found missing. Cancelling the pre-flight
// makes the very next step fail in microseconds, so `create` returns its own
// exit code and main reaches os.Exit long before the teardown's grace is up:
// the picker's kill is never waited for, and the run reports 2 -- "fix your
// invocation" -- or 3, "herdr is unreachable", for a kill. Measured at 1ms
// against a picker that is a single process.
func TestASignalledRunReportsTheSignalAndWaitsForTheTeardown(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
		want int
	}{
		{"a cancelled pre-flight", create.ExitUsage, 128 + int(syscall.SIGTERM)},
		{"a cancelled probe", create.ExitUnreachable, 128 + int(syscall.SIGTERM)},
		// A create that finished its work says so. The session exists; a
		// signal that arrived as it was being reported does not unmake it.
		{"a create that succeeded anyway", create.ExitOK, create.ExitOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &signalWatch{begun: make(chan struct{}), acted: make(chan os.Signal, 1)}
			close(w.begun)
			w.acted <- syscall.SIGTERM
			close(w.acted)

			if got := exitAfterSignal(tc.code, w); got != tc.want {
				t.Fatalf("exitAfterSignal(%d) = %d, want %d", tc.code, got, tc.want)
			}
		})
	}
}

// With no signal in flight it is the create's own answer, immediately: a
// create that waited for a teardown that is not coming would hang on every
// ordinary run.
func TestAnUnsignalledRunKeepsItsOwnExitCode(t *testing.T) {
	w := watchSignals(make(chan os.Signal), func() {}, time.Hour, func(os.Signal) {})

	done := make(chan int, 1)
	go func() { done <- exitAfterSignal(create.ExitUsage, w) }()
	select {
	case got := <-done:
		if got != create.ExitUsage {
			t.Fatalf("exitAfterSignal = %d, want the create's own %d", got, create.ExitUsage)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("exitAfterSignal blocked with no signal in flight")
	}
}

// reraise is the one piece of this that can only be proved by a process that
// actually dies of it, so it is proved in a child. Its own evidence used to
// be a smoke run whose stub picker held the exit open long enough for the
// re-raise to win by accident -- which is to say, no evidence at all.
func TestReraiseKillsThisProcessWithTheSignal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signal death is a POSIX wait status")
	}
	if os.Getenv(reraiseProbeEnv) == "1" {
		reraise(syscall.SIGTERM)
		// Only reached if the re-raise did nothing, which is the failure
		// this test exists to catch: a create that cannot die of the
		// signal it was sent.
		time.Sleep(10 * time.Second)
		os.Exit(99)
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestReraiseKillsThisProcessWithTheSignal", "-test.timeout=60s")
	cmd.Env = append(os.Environ(), reraiseProbeEnv+"=1")
	err := cmd.Run()

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("the probe exited with %v, want a signal death", err)
	}
	// An anonymous interface rather than syscall.WaitStatus, which has a
	// different shape on Windows and would not compile there.
	type signaled interface {
		Signaled() bool
		Signal() syscall.Signal
	}
	ws, ok := exitErr.Sys().(signaled)
	if !ok {
		t.Skipf("no POSIX wait status on %s", runtime.GOOS)
	}
	if !ws.Signaled() {
		t.Fatalf("the probe exited with code %d, want death by signal", exitErr.ExitCode())
	}
	if ws.Signal() != syscall.SIGTERM {
		t.Fatalf("the probe died of %v, want SIGTERM", ws.Signal())
	}
}

// A signal this process was started with instructions to ignore stays
// ignored. signal.Notify would OVERRIDE an inherited SIG_IGN, so without this
// filter `create &` in a POSIX shell -- which ignores SIGINT for background
// jobs -- would start answering a ctrl+c it had been told to sit out, and
// answer it by abandoning its pre-flight.
func TestNotIgnoredDropsAnIgnoredSignal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIG_IGN inheritance is a POSIX story")
	}
	// SIGHUP rather than one the test binary's own runner leans on.
	signal.Ignore(syscall.SIGHUP)
	t.Cleanup(func() { signal.Reset(syscall.SIGHUP) })

	got := notIgnored(syscall.SIGHUP, syscall.SIGTERM)

	if want := []os.Signal{syscall.SIGTERM}; !reflect.DeepEqual(got, want) {
		t.Fatalf("notIgnored = %v, want %v -- an ignored signal must not be caught", got, want)
	}
}
