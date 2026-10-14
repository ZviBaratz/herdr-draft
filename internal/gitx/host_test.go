package gitx

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeSSH puts an `ssh` on PATH that stands in for the real one the way
// machine spec §8.2's tests need: it logs its argv to the returned file, one
// argument per line, drops every option up to and including `--`, drops the
// target, and runs what is left as the remote command with `sh -c` -- which
// is what OpenSSH does on the far end, joined with spaces, through the
// remote user's shell. prelude runs first, inside the fake, so a test can
// make it fail the way ssh itself fails or leave a child holding stderr.
func fakeSSH(t *testing.T, prelude string) (logPath string) {
	t.Helper()
	bin := t.TempDir()
	logPath = filepath.Join(t.TempDir(), "ssh.argv")
	script := `#!/bin/sh
for a in "$@"; do printf '%s\n' "$a" >> "` + logPath + `"; done
` + prelude + `
while [ $# -gt 0 ]; do
  case "$1" in
    -o) shift 2 ;;
    --) shift; break ;;
    -*) shift ;;
    *) break ;;
  esac
done
shift
exec sh -c "$*"
`
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func readArgv(t *testing.T, logPath string) []string {
	t.Helper()
	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("the fake ssh was never run: %v", err)
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

// hostileRepo is a repository whose path a careless quoting would split or
// expand: a space, a single quote and a dollar sign.
func hostileRepo(t *testing.T) string {
	t.Helper()
	src := mkRepo(t)
	dir := filepath.Join(t.TempDir(), "it's a $HOME dir")
	if err := os.Rename(src, dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestHereIsThisMachineAndOverIsAnother pins the zero value: a Host nobody
// set is this machine, which is what lets plan.Machine's zero value mean
// local without a constructor anyone can forget to call.
func TestHereIsThisMachineAndOverIsAnother(t *testing.T) {
	var zero Host
	if zero.Remote() || Here.Remote() {
		t.Error("the zero Host and Here must both be this machine")
	}
	if !Over("devbox").Remote() {
		t.Error("Over(target) must be remote")
	}
}

// TestOverAsksTheRemoteClone runs a Host method over the fake ssh against a
// repository whose path and branch name both need quoting, and checks the
// answer is about that repository -- and that the command took the shape
// §8.2 specifies: batch mode, no tty, the target after `--`.
func TestOverAsksTheRemoteClone(t *testing.T) {
	ctx := context.Background()
	repo := hostileRepo(t)
	branch := "q'uo$te"
	gitRun(t, repo, "branch", branch)
	logPath := fakeSSH(t, "")

	h := Over("devbox")
	got, err := h.LocalBranchExists(ctx, repo, branch)
	if err != nil || !got {
		t.Fatalf("LocalBranchExists over ssh = %v, %v; want true, nil", got, err)
	}
	got, err = h.LocalBranchExists(ctx, repo, "absent")
	if err != nil || got {
		t.Fatalf("LocalBranchExists(absent) over ssh = %v, %v; want false, nil -- show-ref's exit 1 is an answer", got, err)
	}

	argv := readArgv(t, logPath)
	joined := strings.Join(argv, " ")
	for _, want := range []string{"-o BatchMode=yes", "-T -- devbox exec /bin/sh -c "} {
		if !strings.Contains(joined, want) {
			t.Fatalf("ssh argv = %q, want it to contain %q", argv, want)
		}
	}
}

// TestOverRunsInTheNamedRepositoryWhateverTheEnvironmentSays is
// localRepoEnvVars' rule carried to the remote: the script unsets git's
// repository variables before it runs git, so a GIT_DIR the far end's
// environment happens to hold cannot redirect the question. The fake ssh
// inherits this process's environment, which is how the test plants one.
func TestOverRunsInTheNamedRepositoryWhateverTheEnvironmentSays(t *testing.T) {
	ctx := context.Background()
	repo := mkRepo(t)
	gitRun(t, repo, "branch", "only-here")
	fakeSSH(t, "")
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "decoy.git"))

	got, err := Over("devbox").LocalBranchExists(ctx, repo, "only-here")
	if err != nil || !got {
		t.Fatalf("with GIT_DIR planted, LocalBranchExists over ssh = %v, %v; want true, nil", got, err)
	}
}

// TestOverLeavesTheRemotesSSHAlone pins #130's lesson from the far side:
// runGit sets GIT_SSH_COMMAND here because it has resolved this machine's
// own choice first; on the remote nothing has resolved anything, so setting
// it would override that machine's core.sshCommand. GIT_TERMINAL_PROMPT is
// the one setting that travels.
func TestOverLeavesTheRemotesSSHAlone(t *testing.T) {
	ctx := context.Background()
	repo := mkRepo(t)
	logPath := fakeSSH(t, "")

	if _, err := Over("devbox").ResolveRef(ctx, repo, "main"); err != nil {
		t.Fatalf("ResolveRef over ssh: %v", err)
	}
	argv := readArgv(t, logPath)
	script := argv[len(argv)-1]
	if strings.Contains(script, "GIT_SSH_COMMAND") {
		t.Errorf("the remote script sets GIT_SSH_COMMAND: %s", script)
	}
	if !strings.Contains(script, "GIT_TERMINAL_PROMPT=0") {
		t.Errorf("the remote script does not refuse to prompt: %s", script)
	}
	if !strings.Contains(script, "unset ") || !strings.Contains(script, "GIT_DIR") {
		t.Errorf("the remote script does not unset git's repository variables: %s", script)
	}
}

// TestOverReadsSSHsOwnFailureAsAnError: ssh exits 255 when it could not
// run the command at all -- an expired SSM session, a refused key. That must
// never read as show-ref's "no such ref", or a branch that exists on an
// unreachable machine would be reported absent.
func TestOverReadsSSHsOwnFailureAsAnError(t *testing.T) {
	ctx := context.Background()
	repo := mkRepo(t)
	fakeSSH(t, `echo "ssh: connect to host devbox: Connection refused" >&2; exit 255`)

	got, err := Over("devbox").LocalBranchExists(ctx, repo, "main")
	if err == nil {
		t.Fatalf("LocalBranchExists over a failed ssh = %v, nil; want an error", got)
	}
	if !strings.Contains(err.Error(), "Connection refused") {
		t.Errorf("the error does not carry ssh's own words: %v", err)
	}
	if _, err := Over("devbox").ResolveRef(ctx, repo, "main"); err == nil ||
		!strings.Contains(err.Error(), "Connection refused") {
		t.Errorf("ResolveRef over a failed ssh: %v; want ssh's words", err)
	}
}

// TestOverDoesNotWaitOnAHeldStderr: the first ssh to a ControlPersist host
// starts its ProxyCommand, which inherits ssh's stderr and outlives it by
// the persist window. A stderr pipe would hold Wait for waitDelay on every
// such call; the answer must come back as soon as the command has.
func TestOverDoesNotWaitOnAHeldStderr(t *testing.T) {
	ctx := context.Background()
	repo := mkRepo(t)
	fakeSSH(t, `sleep 5 >&2 &`)

	start := time.Now()
	got, err := Over("devbox").LocalBranchExists(ctx, repo, "main")
	if err != nil || !got {
		t.Fatalf("LocalBranchExists with stderr held = %v, %v; want true, nil", got, err)
	}
	if took := time.Since(start); took >= waitDelay {
		t.Errorf("the call took %v: it waited on the held stderr", took)
	}
}

// TestOverRefusesATargetThatReadsAsAnOption: the target comes from herdr's
// machine list, and `--` already keeps ssh from parsing it as an option;
// refusing it outright says why instead of trying a host called
// "-oProxyCommand=...".
func TestOverRefusesATargetThatReadsAsAnOption(t *testing.T) {
	logPath := fakeSSH(t, "")
	_, err := Over("-oProxyCommand=sh").ResolveRef(context.Background(), mkRepo(t), "main")
	if err == nil {
		t.Fatal("a target starting with '-' was accepted")
	}
	if _, statErr := os.Stat(logPath); statErr == nil {
		t.Error("ssh was run for a target starting with '-'")
	}
}

// TestHostMethodsAnswerLocallyWithoutSSH: Here is runGit, unchanged -- no
// ssh on PATH can be involved.
func TestHostMethodsAnswerLocallyWithoutSSH(t *testing.T) {
	ctx := context.Background()
	repo := mkRepo(t)
	logPath := fakeSSH(t, "exit 255")

	if _, err := Here.ResolveRef(ctx, repo, "main"); err != nil {
		t.Fatalf("Here.ResolveRef: %v", err)
	}
	if got, err := Here.LocalBranchExists(ctx, repo, "main"); err != nil || !got {
		t.Fatalf("Here.LocalBranchExists = %v, %v", got, err)
	}
	if _, err := os.Stat(logPath); err == nil {
		t.Error("a local Host ran ssh")
	}
}

// TestOverCleansTheRemoteRepository is the clean gate's half of §8.4: the
// ref-counting and branch-deleting methods act on the repository the remote
// command names, and the local clone with the same branch is untouched.
func TestOverCleansTheRemoteRepository(t *testing.T) {
	ctx := context.Background()
	laptop := mkRepo(t)
	remote := mkRepo(t)
	gitRun(t, laptop, "branch", "x")
	gitRun(t, remote, "branch", "x")
	fakeSSH(t, "")
	h := Over("devbox")

	if n, err := h.CommitsOnlyOn(ctx, remote, "x"); err != nil || n != 0 {
		t.Fatalf("CommitsOnlyOn over ssh = %d, %v", n, err)
	}
	tip, err := h.DeleteBranch(ctx, remote, "x")
	if err != nil || !isCommitID(tip) {
		t.Fatalf("DeleteBranch over ssh = %q, %v", tip, err)
	}
	if localBranch(t, remote, "x") {
		t.Error("the remote repository still has x")
	}
	if !localBranch(t, laptop, "x") {
		t.Error("the laptop's x was deleted by a remote clean")
	}
}

// remoteHome makes a fake remote home, $HOME for the fake ssh's shell, with
// a clone at quantivly/hub whose origin is set and which has a branch of its
// own -- the shape §7's clone probe reads on dev.
func remoteHome(t *testing.T) (home, repo string) {
	t.Helper()
	home = t.TempDir()
	repo = filepath.Join(home, "quantivly", "hub")
	if err := os.MkdirAll(filepath.Dir(repo), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(mkRepo(t), repo); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "remote", "add", "origin", "https://github.com/quantivly/hub.git")
	gitRun(t, repo, "branch", "zvi/old-work")
	t.Setenv("HOME", home)
	return home, repo
}

// TestProbeCloneReadsTheRemoteClone is §7's second probe: one ssh answers
// where the remote's home is, whether the project is there, whether it is a
// repository, what its origin is and which branches it already has.
func TestProbeCloneReadsTheRemoteClone(t *testing.T) {
	home, repo := remoteHome(t)
	fakeSSH(t, "")

	got, err := Over("devbox").ProbeClone(context.Background(), "quantivly/hub")
	if err != nil {
		t.Fatalf("ProbeClone: %v", err)
	}
	if got.Home != home || got.Dir != repo {
		t.Errorf("Home, Dir = %q, %q; want %q, %q", got.Home, got.Dir, home, repo)
	}
	if !got.Exists || !got.IsRepo {
		t.Errorf("Exists, IsRepo = %v, %v; want true, true", got.Exists, got.IsRepo)
	}
	if got.Origin != "https://github.com/quantivly/hub.git" {
		t.Errorf("Origin = %q", got.Origin)
	}
	if !slices.Equal(got.Branches, []string{"main", "zvi/old-work"}) {
		t.Errorf("Branches = %q, want [main zvi/old-work]", got.Branches)
	}
}

// TestProbeCloneTellsMissingFromNotARepository: both are answers, not
// failures -- the caller words each one -- and neither may be confused with
// ssh failing, which is.
func TestProbeCloneTellsMissingFromNotARepository(t *testing.T) {
	home, _ := remoteHome(t)
	if err := os.MkdirAll(filepath.Join(home, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeSSH(t, "")
	h := Over("devbox")

	missing, err := h.ProbeClone(context.Background(), "quantivly/absent")
	if err != nil || missing.Exists || missing.IsRepo {
		t.Errorf("a missing directory: %+v, %v; want Exists=false, no error", missing, err)
	}
	if missing.Home != home {
		t.Errorf("a missing directory still reports the home: %q", missing.Home)
	}
	plain, err := h.ProbeClone(context.Background(), "notes")
	if err != nil || !plain.Exists || plain.IsRepo {
		t.Errorf("a plain directory: %+v, %v; want Exists=true IsRepo=false", plain, err)
	}
}

// TestProbeCloneQuotesTheRelativePath: the path is the user's, from the
// laptop's directory tree, so it is quoted for the remote shell while
// "$HOME" in front of it is still expanded there.
func TestProbeCloneQuotesTheRelativePath(t *testing.T) {
	home, _ := remoteHome(t)
	odd := filepath.Join(home, "it's $odd")
	if err := os.Rename(mkRepo(t), odd); err != nil {
		t.Fatal(err)
	}
	fakeSSH(t, "")

	got, err := Over("devbox").ProbeClone(context.Background(), "it's $odd")
	if err != nil || !got.IsRepo || got.Dir != odd {
		t.Fatalf("ProbeClone(%q) = %+v, %v", "it's $odd", got, err)
	}
}

// TestProbeCloneReportsSSHFailing: an unreachable machine is an error with
// ssh's own words, which is what the popup row and create's exit 3 show.
func TestProbeCloneReportsSSHFailing(t *testing.T) {
	fakeSSH(t, `echo "Error loading SSO Token: Token for dev is expired" >&2; exit 255`)
	_, err := Over("devbox").ProbeClone(context.Background(), "quantivly/hub")
	if err == nil || !strings.Contains(err.Error(), "Token for dev is expired") {
		t.Fatalf("ProbeClone over a failed ssh: %v; want ssh's words", err)
	}
}

// TestOverBoundsTheConnection: an SSM session that stalls without EOF
// would hang a multiplexed client for good, after the worktree exists.
// herdr's own bridge sends keepalives for that reason; so does this.
func TestOverBoundsTheConnection(t *testing.T) {
	logPath := fakeSSH(t, "")
	if _, err := Over("devbox").ResolveRef(context.Background(), mkRepo(t), "main"); err != nil {
		t.Fatal(err)
	}
	argv := strings.Join(readArgv(t, logPath), " ")
	for _, opt := range []string{"-o ConnectTimeout=", "-o ServerAliveInterval=", "-o ServerAliveCountMax="} {
		if !strings.Contains(argv, opt) {
			t.Errorf("ssh argv lacks %q: %s", opt, argv)
		}
	}
}

// TestOverRunsItsScriptUnderPOSIXSh: the far end hands the command to the
// remote user's LOGIN shell, which may be fish or csh; the script is
// POSIX, so it is run by /bin/sh whatever that shell is, the way herdr runs
// its own remote scripts.
func TestOverRunsItsScriptUnderPOSIXSh(t *testing.T) {
	logPath := fakeSSH(t, "")
	if _, err := Over("devbox").ProbeClone(context.Background(), "quantivly/hub"); err != nil {
		t.Fatal(err)
	}
	if _, err := Over("devbox").ResolveRef(context.Background(), mkRepo(t), "main"); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(readArgvRaw(t, logPath), "\n") {
		if strings.Contains(line, "git") && !strings.HasPrefix(line, "exec /bin/sh -c ") {
			t.Errorf("remote command not run under /bin/sh: %s", line)
		}
	}
}

func readArgvRaw(t *testing.T, logPath string) string {
	t.Helper()
	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestProbeCloneDoesNotCountAnEnclosingRepository: a plain directory that
// merely sits inside some other repository -- a home directory kept as a
// dotfiles repo, say -- is not the project's clone, and a worktree cut
// there would be a worktree of the wrong repository.
func TestProbeCloneDoesNotCountAnEnclosingRepository(t *testing.T) {
	home, _ := remoteHome(t)
	gitRun(t, home, "init", "-q", "-b", "main")
	if err := os.MkdirAll(filepath.Join(home, "quantivly", "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeSSH(t, "")
	got, err := Over("devbox").ProbeClone(context.Background(), "quantivly/notes")
	if err != nil || !got.Exists || got.IsRepo {
		t.Errorf("a plain directory inside the home repository: %+v, %v; want Exists=true IsRepo=false", got, err)
	}
	if hub, err := Over("devbox").ProbeClone(context.Background(), "quantivly/hub"); err != nil || !hub.IsRepo {
		t.Errorf("the clone itself, inside the same home repository: %+v, %v; want IsRepo=true", hub, err)
	}
}
