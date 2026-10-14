package plan

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ZviBaratz/herdr-draft/internal/gitx"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
)

// fakeDirenv is internal/gitx's stub of the same name, for this package: a
// `direnv` on PATH whose allow database is keyed on the directory and the
// .envrc's content, answering `status` in 2.37's format. Its `allow` exits
// with $DIRENV_STUB_ALLOW_EXIT when that is set and non-zero, for a direnv
// that fails. Every call is logged as "<cwd> <args>".
func fakeDirenv(t *testing.T) (logPath string) {
	t.Helper()
	bin := t.TempDir()
	db := t.TempDir()
	logPath = filepath.Join(t.TempDir(), "direnv.log")
	script := `#!/bin/sh
printf '%s %s\n' "$PWD" "$*" >> '` + logPath + `'
key() { printf '%s' "$1" | tr / _; }
case "$1" in
status)
  [ -f .envrc ] || exit 0
  if [ -f '` + db + `'/"$(key "$PWD")" ] && cmp -s '` + db + `'/"$(key "$PWD")" .envrc; then
    echo "Found RC allowed 0"
  else
    echo "Found RC allowed 1"
  fi
  ;;
allow)
  [ "${DIRENV_STUB_ALLOW_EXIT:-0}" = 0 ] || { echo "direnv: error permission denied" >&2; exit "$DIRENV_STUB_ALLOW_EXIT"; }
  [ -z "$DIRENV_STUB_ALLOW_NOOP" ] || exit 0
  d=${2:-$PWD}
  cp "$d/.envrc" '` + db + `'/"$(key "$d")"
  ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "direnv"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	// The trust script also looks in $HOME/.local/bin, for mise.
	t.Setenv("HOME", t.TempDir())
	return logPath
}

const repoEnvrc = "PATH_add .venv/bin\n"

// envrcRepo is a repository whose committed .envrc the operator has
// allowed in the main clone, which is what every worktree cut from it then
// carries byte for byte.
func envrcRepo(t *testing.T) string {
	t.Helper()
	repo := mkRepo(t)
	if err := os.WriteFile(filepath.Join(repo, ".envrc"), []byte(repoEnvrc), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", ".envrc")
	gitIn(t, repo, "commit", "-qm", "envrc")
	allowDirenv(t, repo)
	return repo
}

// allowDirenv is the operator's own first allow, in dir.
func allowDirenv(t *testing.T, dir string) {
	t.Helper()
	if err := runDirenvAllow(dir); err != nil {
		t.Fatal(err)
	}
}

// envrcExecute runs a worktree plan from repo with the allow switched on
// and returns the result and the worktree step's last progress event.
// edit, when set, changes the new checkout after herdr makes it -- a
// branch whose .envrc is not the main clone's.
func envrcExecute(t *testing.T, repo string, allow bool, edit func(checkout string)) (ExecResult, Progress) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wt")
	add := herdrWorktreeAdd(t, repo, path)
	m := &mockRunner{topo: createdTopo(path, "zvi/new"), onWorktreeCreate: func(req herdrc.WorktreeCreateReq) {
		add(req)
		if edit != nil {
			edit(path)
		}
	}}
	in := worktreeInput(repo, "zvi/new")
	in.InheritTrust = allow
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var last Progress
	result := Execute(context.Background(), m, ops, ExecOpts{}, func(p Progress) {
		if p.Index == 0 && p.State != StepRunning {
			last = p
		}
	})
	return result, last
}

// TestBuildCarriesTheAllowOnTheWorktreeOpOnly: the allow is part of cutting
// a worktree, so the switch rides on that op and on nothing else -- a
// session in an existing directory has nobody's .envrc to extend.
func TestBuildCarriesTheAllowOnTheWorktreeOpOnly(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	in.InheritTrust = true
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for i, op := range ops {
		if want := op.Kind == OpWorktreeCreate; op.InheritTrust != want {
			t.Errorf("op %d (%v): InheritTrust = %v, want %v", i, op.Kind, op.InheritTrust, want)
		}
	}

	in.UseWorktree = false
	ops, err = Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for i, op := range ops {
		if op.InheritTrust {
			t.Errorf("op %d (%v) carries the allow in a session without a worktree", i, op.Kind)
		}
	}
}

// TestExecuteAllowsAnEnvrcTheMainCloneAllowed is the case the feature is
// for: the new checkout's .envrc is the main clone's, already allowed, so it
// is allowed before the agent starts, and the worktree step says so.
func TestExecuteAllowsAnEnvrcTheMainCloneAllowed(t *testing.T) {
	log := fakeDirenv(t)
	repo := envrcRepo(t)

	result, step := envrcExecute(t, repo, true, nil)

	if result.FailedIndex != -1 {
		t.Fatalf("FailedIndex = %d: %v", result.FailedIndex, step.Err)
	}
	if result.EnvTrust.Trust.Envrc != gitx.EnvrcAllowed || result.EnvTrust.Err != nil {
		t.Fatalf("Envrc = %+v, want allowed\ndirenv calls:\n%s", result.EnvTrust, readFile(t, log))
	}
	if step.State != StepDone || step.Note != ".envrc allowed: same as the main clone's" {
		t.Errorf("worktree step = %v %q", step.State, step.Note)
	}
}

// TestExecuteLeavesADifferentEnvrcBlockedAndSaysSo: a branch that changed
// .envrc keeps it blocked for review, and the one line says why.
func TestExecuteLeavesADifferentEnvrcBlockedAndSaysSo(t *testing.T) {
	log := fakeDirenv(t)
	repo := envrcRepo(t)

	result, step := envrcExecute(t, repo, true, func(checkout string) {
		if err := os.WriteFile(filepath.Join(checkout, ".envrc"), []byte(repoEnvrc+"export X=1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	})

	if result.FailedIndex != -1 {
		t.Fatalf("FailedIndex = %d: %v", result.FailedIndex, step.Err)
	}
	if result.EnvTrust.Trust.Envrc != gitx.EnvrcDiffers {
		t.Errorf("Envrc = %+v, want differs", result.EnvTrust)
	}
	if step.State != StepDone || step.Note != ".envrc left blocked: differs from the main clone's" {
		t.Errorf("worktree step = %v %q", step.State, step.Note)
	}
	if n := strings.Count(readFile(t, log), " allow "); n != 1 {
		t.Errorf("direnv was asked to allow %d times, want only the operator's own:\n%s", n, readFile(t, log))
	}
}

// TestExecuteNeverFailsACreateOverTheEnvrc: the allow is best-effort. A
// direnv that fails leaves the session made, the step done, and the reason
// in one line -- an unloaded environment is the state before this existed.
func TestExecuteNeverFailsACreateOverTheEnvrc(t *testing.T) {
	fakeDirenv(t)
	repo := envrcRepo(t)
	t.Setenv("DIRENV_STUB_ALLOW_EXIT", "1") // the operator's allow above worked; this one does not

	result, step := envrcExecute(t, repo, true, nil)

	if result.FailedIndex != -1 {
		t.Fatalf("a failed direnv allow failed the create at step %d: %v", result.FailedIndex, step.Err)
	}
	if result.Created == nil {
		t.Fatal("no space recorded")
	}
	if result.EnvTrust.Err == nil {
		t.Errorf("Envrc = %+v, want the failure recorded", result.EnvTrust)
	}
	if step.State != StepDone || !strings.HasPrefix(step.Note, ".envrc may be allowed, but the step failed: ") || !strings.Contains(step.Note, "permission denied") {
		t.Errorf("worktree step = %v %q", step.State, step.Note)
	}
}

// TestExecuteLeavesDirenvAloneWhenTurnedOff: `[worktree] inherit_trust =
// false` means direnv is not asked anything at all.
func TestExecuteLeavesDirenvAloneWhenTurnedOff(t *testing.T) {
	log := fakeDirenv(t)
	repo := envrcRepo(t)
	before := readFile(t, log)

	result, step := envrcExecute(t, repo, false, nil)

	if result.EnvTrust != (TrustOutcome{}) || step.Note != "" {
		t.Errorf("Envrc = %+v, note %q; want nothing", result.EnvTrust, step.Note)
	}
	if after := readFile(t, log); after != before {
		t.Errorf("direnv was asked with the allow switched off:\n%s", strings.TrimPrefix(after, before))
	}
}

// TestExecuteSaysNothingWhenThereIsNoEnvrcToAllow: no .envrc, or one the
// main clone has not allowed, is the state before this feature, and the
// worktree step reads as it always did.
func TestExecuteSaysNothingWhenThereIsNoEnvrcToAllow(t *testing.T) {
	fakeDirenv(t)
	for name, repo := range map[string]string{
		"no .envrc":   mkRepo(t),
		"not allowed": unallowedEnvrcRepo(t),
	} {
		t.Run(name, func(t *testing.T) {
			result, step := envrcExecute(t, repo, true, nil)
			if result.FailedIndex != -1 {
				t.Fatalf("FailedIndex = %d: %v", result.FailedIndex, step.Err)
			}
			if step.State != StepDone || step.Note != "" {
				t.Errorf("worktree step = %v %q, want done and silent", step.State, step.Note)
			}
		})
	}
}

func unallowedEnvrcRepo(t *testing.T) string {
	t.Helper()
	repo := mkRepo(t)
	if err := os.WriteFile(filepath.Join(repo, ".envrc"), []byte(repoEnvrc), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", ".envrc")
	gitIn(t, repo, "commit", "-qm", "envrc")
	return repo
}

// TestExecuteAllowsTheEnvrcOnTheRemoteMachine: on another machine the
// check and the allow run there, over the same ssh target git uses, with
// that machine's main clone and that machine's checkout path.
func TestExecuteAllowsTheEnvrcOnTheRemoteMachine(t *testing.T) {
	direnvLog := fakeDirenv(t)
	_, remote := remoteClone(t)
	if err := os.WriteFile(filepath.Join(remote, ".envrc"), []byte(repoEnvrc), 0o644); err != nil {
		t.Fatal(err)
	}
	allowDirenv(t, remote)
	sshLog := fakeSSH(t, "")
	path := filepath.Join(t.TempDir(), "wt")
	add := trackingWorktreeAdd(t, remote, path, "")
	m := &mockRunner{topo: createdTopo(path, "zvi/new"), onWorktreeCreate: func(req herdrc.WorktreeCreateReq) {
		add(req)
		// Untracked on the remote, like most .envrc files: the checkout gets
		// whatever the branch carries, and here that is the same bytes.
		if err := os.WriteFile(filepath.Join(path, ".envrc"), []byte(repoEnvrc), 0o644); err != nil {
			t.Fatal(err)
		}
	}}
	in := remoteInput()
	in.UseWorktree = true
	in.Branch = "zvi/new"
	in.Machine.Dir = remote
	in.InheritTrust = true

	result, failed := executeRemote(t, in, m)

	if result.FailedIndex != -1 {
		t.Fatalf("FailedIndex = %d: %v", result.FailedIndex, failed)
	}
	if result.EnvTrust.Trust.Envrc != gitx.EnvrcAllowed {
		t.Fatalf("Envrc = %+v, want allowed\ndirenv calls:\n%s", result.EnvTrust, readFile(t, direnvLog))
	}
	if !strings.Contains(readFile(t, sshLog), "direnv allow") {
		t.Errorf("the allow did not go over ssh:\n%s", readFile(t, sshLog))
	}
}

func runDirenvAllow(dir string) error {
	if out, err := exec.Command("direnv", "allow", dir).CombinedOutput(); err != nil {
		return fmt.Errorf("direnv allow %s: %w\n%s", dir, err, out)
	}
	return nil
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// fakeMise is internal/gitx's stub of the same name, on PATH: trust by
// directory, and `mise trust --show` printing "<cwd>: trusted|untrusted".
func fakeMise(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	db := t.TempDir()
	script := `#!/bin/sh
key() { printf '%s' "$1" | tr / _; }
disp() { case $1 in "$HOME"/*) printf '~%s' "${1#"$HOME"}" ;; *) printf '%s' "$1" ;; esac; }
[ "$1" = trust ] || exit 0
if [ "$2" = --show ]; then
  if [ -e '` + db + `'/"$(key "$PWD")" ]; then echo "$(disp "$PWD"): trusted"; else echo "$(disp "$PWD"): untrusted"; fi
  exit 0
fi
d=${2:-$PWD}
[ -f "$d" ] && d=$(dirname "$d")
touch '` + db + `'/"$(key "$d")"
`
	if err := os.WriteFile(filepath.Join(bin, "mise"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// hubRepo is envrcRepo with hub's other half, a committed .mise.toml, and
// trusted says whether the operator has trusted it in the main clone.
func hubRepo(t *testing.T, trusted bool) string {
	t.Helper()
	repo := envrcRepo(t)
	if err := os.WriteFile(filepath.Join(repo, ".mise.toml"), []byte("[env]\nX = \"1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", ".mise.toml")
	gitIn(t, repo, "commit", "-qm", "mise")
	if trusted {
		if out, err := exec.Command("mise", "trust", repo).CombinedOutput(); err != nil {
			t.Fatalf("mise trust: %v\n%s", err, out)
		}
	}
	return repo
}

// TestExecuteTrustsMiseAndAllowsTheEnvrc: hub's shape once the operator has
// trusted its mise config in the main clone -- both extended, one line.
func TestExecuteTrustsMiseAndAllowsTheEnvrc(t *testing.T) {
	fakeDirenv(t)
	fakeMise(t)
	repo := hubRepo(t, true)

	result, step := envrcExecute(t, repo, true, nil)

	want := gitx.EnvTrust{Envrc: gitx.EnvrcAllowed, Mise: gitx.MiseTrusted}
	if result.FailedIndex != -1 || result.EnvTrust.Trust != want {
		t.Fatalf("FailedIndex = %d, EnvTrust = %+v; want -1 and %+v", result.FailedIndex, result.EnvTrust, want)
	}
	if step.Note != ".envrc allowed and mise trusted: same as the main clone's" {
		t.Errorf("worktree step note = %q", step.Note)
	}
}

// TestExecuteSaysWhyMiseKeptTheEnvrcBlocked is hub's shape on 2026-10-14:
// its .envrc allowed, its mise config trusted by nobody. The .envrc stays
// blocked, so the agent starts, and the line names the thing to trust.
func TestExecuteSaysWhyMiseKeptTheEnvrcBlocked(t *testing.T) {
	fakeDirenv(t)
	fakeMise(t)
	repo := hubRepo(t, false)

	result, step := envrcExecute(t, repo, true, nil)

	if result.FailedIndex != -1 || result.EnvTrust.Trust.Envrc != gitx.EnvrcHeld {
		t.Fatalf("FailedIndex = %d, EnvTrust = %+v; want -1 and a held .envrc", result.FailedIndex, result.EnvTrust)
	}
	if step.Note != ".envrc left blocked: mise does not trust the main clone's config" {
		t.Errorf("worktree step note = %q", step.Note)
	}
}

// TestExecuteWaitsLongerForAShellLoadingItsEnvironment: an allowed .envrc
// runs in the new pane's first prompt, and herdr calls that pane busy until
// it finishes -- hub's first load outlasted busyRetryBudget on dev. Twenty
// seconds of busy (a clock that moves 2s a read) fails a plan that allowed
// nothing and succeeds one that did.
func TestExecuteWaitsLongerForAShellLoadingItsEnvironment(t *testing.T) {
	fakeDirenv(t)
	for _, allowed := range []bool{false, true} {
		t.Run(fmt.Sprintf("allowed=%v", allowed), func(t *testing.T) {
			withBusyRetryOverrides(t, 0, fakeAdvancingClock(2*time.Second))
			repo := envrcRepo(t)
			path := filepath.Join(t.TempDir(), "wt")
			m := &mockRunner{
				topo: createdTopo(path, "zvi/new"), onWorktreeCreate: herdrWorktreeAdd(t, repo, path),
				failAt: "AgentStart", failErr: busyErr, failCount: 10,
			}
			in := worktreeInput(repo, "zvi/new")
			in.InheritTrust = allowed
			result := executeWorktree(t, in, m)

			if got, want := result.FailedIndex == -1, allowed; got != want {
				t.Errorf("succeeded = %v, want %v (EnvTrust %+v, %d AgentStart calls)",
					got, want, result.EnvTrust, countCallsWithPrefix(m.calls, "AgentStart("))
			}
		})
	}
}

// TestExecuteNeverInterruptsAShellItLoadedNothingInto: a pane that stays
// busy with no environment of this run's in it is somebody else's business,
// and is reported as it always was.
func TestExecuteNeverInterruptsAShellItLoadedNothingInto(t *testing.T) {
	fakeDirenv(t)
	withBusyRetryOverrides(t, 0, fakeAdvancingClock(2*time.Second))
	repo := envrcRepo(t)
	path := filepath.Join(t.TempDir(), "wt")
	m := &mockRunner{
		topo: createdTopo(path, "zvi/new"), onWorktreeCreate: herdrWorktreeAdd(t, repo, path),
		failAt: "AgentStart", failErr: busyErr, failCount: 1000,
	}
	result := executeWorktree(t, worktreeInput(repo, "zvi/new"), m) // inherit_trust off

	if result.FailedIndex == -1 {
		t.Fatal("a pane busy for good started an agent")
	}
	if n := countCallsWithPrefix(m.calls, "PaneSendKeys("); n != 0 {
		t.Errorf("%d keys sent to a pane this run loaded nothing into: %v", n, m.calls)
	}
}

// TestExecuteWaitsLongerWhenTheAllowBeganAndTheStepFailed: a trust step
// that fails AFTER starting the allow -- direnv's own check afterwards
// disagreeing, or the timeout killing a remote ssh mid-allow -- may still
// leave an allowed .envrc for the pane to load, so the agent's start gets
// the longer wait anyway. Treating every error as "nothing allowed" gave it
// a bare shell's five seconds: the failure on dev this whole step exists to
// prevent.
func TestExecuteWaitsLongerWhenTheAllowBeganAndTheStepFailed(t *testing.T) {
	fakeDirenv(t)
	withBusyRetryOverrides(t, 0, fakeAdvancingClock(2*time.Second))
	repo := envrcRepo(t)
	t.Setenv("DIRENV_STUB_ALLOW_NOOP", "1") // the allow exits 0 and records nothing
	path := filepath.Join(t.TempDir(), "wt")
	m := &mockRunner{
		topo: createdTopo(path, "zvi/new"), onWorktreeCreate: herdrWorktreeAdd(t, repo, path),
		failAt: "AgentStart", failErr: busyErr, failCount: 10,
	}
	in := worktreeInput(repo, "zvi/new")
	in.InheritTrust = true
	result := executeWorktree(t, in, m)

	if result.EnvTrust.Err == nil || result.EnvTrust.Trust.Envrc != gitx.EnvrcAllowing {
		t.Fatalf("EnvTrust = %+v, want the failure beside an allow that had begun", result.EnvTrust)
	}
	if result.FailedIndex != -1 {
		t.Errorf("FailedIndex = %d: a shell that may be loading an environment got a bare shell's wait", result.FailedIndex)
	}
}

// TestExecuteGivesAPinnedLaunchTheLongerDetectionWait: a pinned account's
// launch line is typed into the pane (OpClauthLaunch), so a load settleEnv
// could not see would hold it, and the detection that follows allows the
// same extra minute. Nothing is pressed after the line is typed: ctrl+c
// would flush it, and a detection timeout cannot tell a slow environment
// from a slow agent.
func TestExecuteGivesAPinnedLaunchTheLongerDetectionWait(t *testing.T) {
	fakeDirenv(t)
	for _, allowed := range []bool{false, true} {
		t.Run(fmt.Sprintf("allowed=%v", allowed), func(t *testing.T) {
			repo := envrcRepo(t)
			path := filepath.Join(t.TempDir(), "wt")
			m := &mockRunner{topo: createdTopo(path, "zvi/new"), onWorktreeCreate: herdrWorktreeAdd(t, repo, path)}
			in := worktreeInput(repo, "zvi/new")
			in.AccountPin = "work"
			in.InheritTrust = allowed
			result := executeWorktree(t, in, m)
			if result.FailedIndex != -1 {
				t.Fatalf("FailedIndex = %d", result.FailedIndex)
			}
			want := in.DetectionTimeout
			if allowed {
				want += envLoadBusyBudget
			}
			for _, c := range m.calls {
				if strings.HasPrefix(c, "AwaitDetection(") && !strings.Contains(c, ","+want.String()+",") {
					t.Errorf("%s, want a %s detection wait", c, want)
				}
			}
			if n := countCallsWithPrefix(m.calls, "PaneSendKeys("); n != 0 {
				t.Errorf("%d keys sent to a pane holding a typed launch line", n)
			}
		})
	}
}

// TestTrustNoteNamesWhatTheOperatorHasToDo: the lines the review asked for
// beyond the ones the Execute tests above pin.
func TestTrustNoteNamesWhatTheOperatorHasToDo(t *testing.T) {
	boom := errors.New("ssh: connection reset")
	for _, tc := range []struct {
		o    TrustOutcome
		want string
	}{
		{TrustOutcome{Trust: gitx.EnvTrust{Envrc: gitx.EnvrcHeld, Mise: gitx.MiseUnknown}},
			".envrc left blocked: it has a mise config and no mise was found to trust it"},
		{TrustOutcome{Trust: gitx.EnvTrust{Envrc: gitx.EnvrcAllowing}, Err: boom},
			".envrc may be allowed, but the step failed: ssh: connection reset"},
		{TrustOutcome{Err: boom}, "trust not extended: ssh: connection reset"},
		{TrustOutcome{Trust: gitx.EnvTrust{Envrc: gitx.EnvrcDenied}}, ""},
	} {
		if got := tc.o.Note(); got != tc.want {
			t.Errorf("%+v: Note() = %q, want %q", tc.o, got, tc.want)
		}
	}
}

// paneShell is the pid process info names a pane's shell by, in these tests.
const paneShell = 100

// idlePane is a pane at its prompt: the shell, and nothing else.
func idlePane() herdrc.ProcessInfo {
	return herdrc.ProcessInfo{ShellPID: paneShell, Foreground: []herdrc.Process{{PID: paneShell, Name: "zsh", Argv: []string{"/usr/bin/zsh"}}}}
}

// loadingPane is a pane whose shell's first prompt is running direnv's
// hook, as `herdr pane process-info` showed it on the laptop, 2026-10-14.
func loadingPane() herdrc.ProcessInfo {
	return herdrc.ProcessInfo{ShellPID: paneShell, Foreground: []herdrc.Process{
		{PID: paneShell, Name: "zsh", Argv: []string{"/usr/bin/zsh"}},
		{PID: paneShell + 1, Name: "direnv", Argv: []string{"direnv", "export", "zsh"}},
		{PID: paneShell + 2, Name: "bash", Argv: []string{"/usr/bin/bash", "-c", "eval ..."}},
		{PID: paneShell + 3, Name: "sleep", Argv: []string{"sleep", "120"}},
	}}
}

// withEnvClock runs the settle step on a clock that moves step per read
// and polls without sleeping.
func withEnvClock(t *testing.T, step time.Duration) {
	t.Helper()
	origNow, origPoll := envNow, envPollInterval
	envNow, envPollInterval = fakeAdvancingClock(step), 0
	t.Cleanup(func() { envNow, envPollInterval = origNow, origPoll })
}

// settleExecute runs a worktree plan with the allow on, whose agent's pane
// reports panes() in turn (the last one repeated), and returns the result,
// the runner, and the agent step's last progress.
func settleExecute(t *testing.T, pinned bool, panes func(call int, interrupts int) herdrc.ProcessInfo) (ExecResult, *mockRunner, Progress) {
	t.Helper()
	fakeDirenv(t)
	withEnvClock(t, 2*time.Second)
	repo := envrcRepo(t)
	path := filepath.Join(t.TempDir(), "wt")
	m := &mockRunner{topo: createdTopo(path, "zvi/new"), onWorktreeCreate: herdrWorktreeAdd(t, repo, path)}
	calls, interrupts := 0, 0
	m.processInfo = func() (herdrc.ProcessInfo, error) { calls++; return panes(calls, interrupts), nil }
	m.onSendKeys = func(string, []string) { interrupts++ }
	in := worktreeInput(repo, "zvi/new")
	in.InheritTrust = true
	if pinned {
		in.AccountPin = "work"
	}
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	launch := indexOfKind(ops, OpAgentStart)
	if pinned {
		launch = indexOfKind(ops, OpClauthLaunch)
	}
	var step Progress
	result := Execute(context.Background(), m, ops, ExecOpts{}, func(p Progress) {
		if p.Index == launch && p.State != StepRunning {
			step = p
		}
	})
	return result, m, step
}

// callIndex is the position of the first call with prefix, -1 for none.
func callIndex(calls []string, prefix string) int {
	for i, c := range calls {
		if strings.HasPrefix(c, prefix) {
			return i
		}
	}
	return -1
}

// TestExecuteWaitsForTheEnvironmentToLoadBeforeStartingTheAgent: on the
// laptop herdr typed the agent's command into a shell still running
// direnv's hook and then timed out waiting for it (2026-10-14), so the
// agent is launched only once the load it can see has finished.
func TestExecuteWaitsForTheEnvironmentToLoadBeforeStartingTheAgent(t *testing.T) {
	result, m, step := settleExecute(t, false, func(call, _ int) herdrc.ProcessInfo {
		if call >= 2 && call <= 5 {
			return loadingPane()
		}
		return idlePane()
	})
	if result.FailedIndex != -1 {
		t.Fatalf("FailedIndex = %d: %v", result.FailedIndex, step.Err)
	}
	if n := countCallsWithPrefix(m.calls, "PaneProcessInfo("); n < 6 {
		t.Errorf("%d process reads, want the load watched until it finished", n)
	}
	if last, start := lastCallIndex(m.calls, "PaneProcessInfo("), callIndex(m.calls, "AgentStart("); start < last {
		t.Errorf("the agent started (call %d) before the load was seen to finish (call %d): %v", start, last, m.calls)
	}
	if n := countCallsWithPrefix(m.calls, "PaneSendKeys("); n != 0 || result.EnvTrust.Interrupted || step.Note != "" {
		t.Errorf("a load that finished was interrupted: %d keys, note %q", n, step.Note)
	}
}

func lastCallIndex(calls []string, prefix string) int {
	last := -1
	for i, c := range calls {
		if strings.HasPrefix(c, prefix) {
			last = i
		}
	}
	return last
}

// TestExecuteStartsTheAgentWhenTheEnvironmentNeverFinishesLoading is the
// operator's rule: the environment never fails a create. A load still
// running after envLoadBusyBudget gets ctrl+c -- direnv gives up on it,
// measured on both machines' direnv despite its hook's `trap ” SIGINT`
// -- BEFORE anything is typed, and the agent starts without it.
func TestExecuteStartsTheAgentWhenTheEnvironmentNeverFinishesLoading(t *testing.T) {
	for _, needed := range []int{1, 2} {
		t.Run(fmt.Sprintf("freed by press %d", needed), func(t *testing.T) {
			result, m, step := settleExecute(t, false, func(_ int, interrupts int) herdrc.ProcessInfo {
				if interrupts >= needed {
					return idlePane()
				}
				return loadingPane()
			})
			if result.FailedIndex != -1 {
				t.Fatalf("FailedIndex = %d: the environment failed the create (%v)", result.FailedIndex, step.Err)
			}
			if n := countCallsWithPrefix(m.calls, "PaneSendKeys("); n != needed {
				t.Errorf("%d presses, want %d", n, needed)
			}
			if c := m.calls[callIndex(m.calls, "PaneSendKeys(")]; c != "PaneSendKeys(pane-1,ctrl+c)" {
				t.Errorf("sent %s, want ctrl+c to the agent's pane", c)
			}
			if callIndex(m.calls, "AgentStart(") < lastCallIndex(m.calls, "PaneSendKeys(") {
				t.Errorf("the agent's command went out before the interrupt: %v", m.calls)
			}
			if !result.EnvTrust.Interrupted {
				t.Error("EnvTrust.Interrupted = false")
			}
			if want := "the environment was still loading after 1m0s; interrupted it to start the agent"; step.State != StepDone || step.Note != want {
				t.Errorf("agent step = %v %q, want done and %q", step.State, step.Note, want)
			}
		})
	}
}

// TestExecuteInterruptsAtMostTwice: a load two presses do not stop is not
// pressed a third time; the agent is launched anyway, as before any of this.
func TestExecuteInterruptsAtMostTwice(t *testing.T) {
	result, m, _ := settleExecute(t, false, func(int, int) herdrc.ProcessInfo { return loadingPane() })
	if n := countCallsWithPrefix(m.calls, "PaneSendKeys("); n != 2 || maxEnvInterrupts != 2 {
		t.Errorf("%d presses with maxEnvInterrupts = %d, want exactly 2", n, maxEnvInterrupts)
	}
	if callIndex(m.calls, "AgentStart(") < 0 || result.FailedIndex != -1 {
		t.Errorf("the agent was not launched after the presses ran out: FailedIndex %d, %v", result.FailedIndex, m.calls)
	}
}

// TestExecuteDoesNotWaitLongOnAShellThatLoadsNothing: a shell that stays
// at its prompt is launched into after envIdleGrace -- long enough for a
// first prompt that has not started its hook yet -- not after a minute.
func TestExecuteDoesNotWaitLongOnAShellThatLoadsNothing(t *testing.T) {
	result, m, _ := settleExecute(t, false, func(int, int) herdrc.ProcessInfo { return idlePane() })
	if result.FailedIndex != -1 {
		t.Fatalf("FailedIndex = %d", result.FailedIndex)
	}
	if n := countCallsWithPrefix(m.calls, "PaneProcessInfo("); n > 3 {
		t.Errorf("%d reads of an idle pane at 2s each, want the grace and no more", n)
	}
}

// TestExecuteInterruptsBeforeTypingAPinnedLaunch: a pinned account's launch
// line is TYPED (pane run), and ctrl+c after it would flush it from the
// terminal -- so the settle runs, and presses, before the line exists.
func TestExecuteInterruptsBeforeTypingAPinnedLaunch(t *testing.T) {
	result, m, _ := settleExecute(t, true, func(_ int, interrupts int) herdrc.ProcessInfo {
		if interrupts >= 1 {
			return idlePane()
		}
		return loadingPane()
	})
	if result.FailedIndex != -1 {
		t.Fatalf("FailedIndex = %d", result.FailedIndex)
	}
	press, typed := callIndex(m.calls, "PaneSendKeys("), callIndex(m.calls, "PaneRun(")
	if press < 0 || typed < 0 || typed < press {
		t.Errorf("ctrl+c at call %d, launch typed at call %d: want the press first, %v", press, typed, m.calls)
	}
}
