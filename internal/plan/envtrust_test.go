package plan

import (
	"context"
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
	if step.State != StepDone || !strings.HasPrefix(step.Note, "trust not extended: ") || !strings.Contains(step.Note, "permission denied") {
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

// TestExecuteStartsTheAgentWhenTheEnvironmentNeverFinishesLoading is the
// operator's rule for the environment: it never fails a create. A shell
// still loading the .envrc this run allowed when envLoadBusyBudget runs out
// is interrupted -- direnv's own "Use CTRL-C to give up" -- which leaves it
// at its prompt without the environment, the state before any of this,
// and the agent starts there. The agent's step says so.
func TestExecuteStartsTheAgentWhenTheEnvironmentNeverFinishesLoading(t *testing.T) {
	for _, interruptsNeeded := range []int{1, 2} {
		t.Run(fmt.Sprintf("freed by interrupt %d", interruptsNeeded), func(t *testing.T) {
			fakeDirenv(t)
			withBusyRetryOverrides(t, 0, fakeAdvancingClock(2*time.Second))
			repo := envrcRepo(t)
			path := filepath.Join(t.TempDir(), "wt")
			m := &mockRunner{
				topo: createdTopo(path, "zvi/new"), onWorktreeCreate: herdrWorktreeAdd(t, repo, path),
				failAt: "AgentStart", failErr: busyErr, failCount: 1000,
			}
			interrupts := 0
			m.onSendKeys = func(string, []string) {
				if interrupts++; interrupts == interruptsNeeded {
					m.failCount = 0 // the shell is at its prompt
				}
			}
			in := worktreeInput(repo, "zvi/new")
			in.InheritTrust = true
			ops, err := Build(in)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			agentIdx := indexOfKind(ops, OpAgentStart)
			var agentStep Progress
			result := Execute(context.Background(), m, ops, ExecOpts{}, func(p Progress) {
				if p.Index == agentIdx && p.State != StepRunning {
					agentStep = p
				}
			})

			if result.FailedIndex != -1 {
				t.Fatalf("FailedIndex = %d: the environment failed the create (%v)", result.FailedIndex, agentStep.Err)
			}
			if !result.EnvTrust.Interrupted {
				t.Error("EnvTrust.Interrupted = false")
			}
			for _, c := range m.calls {
				if strings.HasPrefix(c, "PaneSendKeys(") && c != "PaneSendKeys(pane-1,ctrl+c)" {
					t.Errorf("sent %s, want ctrl+c to the agent's pane", c)
				}
			}
			if n := countCallsWithPrefix(m.calls, "PaneSendKeys("); n != interruptsNeeded {
				t.Errorf("%d interrupts sent, want %d", n, interruptsNeeded)
			}
			if want := "the environment was still loading after 1m0s; interrupted it to start the agent"; agentStep.State != StepDone || agentStep.Note != want {
				t.Errorf("agent step = %v %q, want done and %q", agentStep.State, agentStep.Note, want)
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
