package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Host is the machine a git command runs on (machine spec §8.2). Its zero
// value, Here, is this one: the package's functions as they always were.
// Over(target) is a herdr machine's clone, reached with `ssh <target>`, the
// same target herdr's own machine bridge connects to.
//
// The methods are the questions plan asks about a session's repository
// after it exists -- whether a branch is there, what a base resolves to,
// what a worktree's branch tracks, whether it is safe to clean -- because a
// session on another machine has its repository THERE. Asking the laptop's
// clone instead is not an approximation: before this type, plan.Clean on a
// remote session would have deleted a laptop branch that happened to share
// the session's name. Everything else in this package stays local-only,
// IsGitRepo and the ssh-resolving configValue above all: the form's
// directory, base list and memory key are all about the laptop's clone.
type Host struct{ target string }

// Here is this machine.
var Here Host

// Over is the machine `ssh <target>` reaches. target is a herdr machine
// profile's ssh target, as `herdr machine list --json` prints it.
func Over(target string) Host { return Host{target: target} }

// Remote reports whether h is another machine.
func (h Host) Remote() bool { return h.target != "" }

// run is runGit on h: the same git, the same arguments, asked of the
// repository dir on whichever machine h is.
func (h Host) run(ctx context.Context, dir string, args ...string) (string, error) {
	if !h.Remote() {
		return runGit(ctx, dir, args...)
	}
	out, err := h.ssh(ctx, remoteGit(dir, args...))
	if err != nil {
		return "", fmt.Errorf("git %s (on %s, in %s): %w", strings.Join(args, " "), h.target, dir, err)
	}
	return out, nil
}

// anyRefExists is the package's anyRefExists on h. Over ssh, show-ref's
// exit 1 is still "no such ref"; ssh's own 255 is a failure, never an
// absence (TestOverReadsSSHsOwnFailureAsAnError).
func (h Host) anyRefExists(ctx context.Context, dir string, refs ...string) (bool, error) {
	if !h.Remote() {
		return anyRefExists(ctx, dir, refs...)
	}
	for _, ref := range refs {
		_, err := h.ssh(ctx, remoteGit(dir, "show-ref", "--verify", "--quiet", ref))
		if err == nil {
			return true, nil
		}
		if exitCode(err) == 1 {
			continue
		}
		return false, fmt.Errorf("git show-ref --verify --quiet %s (on %s, in %s): %w", ref, h.target, dir, err)
	}
	return false, nil
}

// remoteGit is the command line ssh hands the remote shell for one git
// invocation: every word quoted, because OpenSSH joins its arguments with
// spaces and the far end's shell re-parses them -- the same reason
// herdrc.CLIRunner.PaneRun quotes for `herdr pane run`.
//
// The repository variables are unset first, for localRepoEnvVars' reason:
// the directory named is the repository asked about. GIT_TERMINAL_PROMPT=0
// is runGit's refusal to ask a person anything; `-T` and BatchMode already
// leave nothing to ask on, and this stops git trying. GIT_SSH_COMMAND is
// deliberately NOT set: runGit sets it only after resolving this machine's
// own choice (effectiveSSHCommand), and nothing has resolved the remote's,
// so setting it would override that machine's core.sshCommand -- #130's
// bug, from the other side.
func remoteGit(dir string, args ...string) string {
	words := make([]string, 0, len(args)+3)
	words = append(words, "git", "-C", shellQuote(dir))
	for _, a := range args {
		words = append(words, shellQuote(a))
	}
	return "unset " + strings.Join(localRepoEnvVars, " ") + "; GIT_TERMINAL_PROMPT=0 exec " + strings.Join(words, " ")
}

// ssh runs script on h's machine and returns its trimmed stdout. A failure
// carries the remote's stderr -- ssh's own words when ssh itself failed --
// and keeps the *exec.ExitError reachable for exitCode.
//
// stderr goes to a file, not a pipe. The first ssh to a host with
// ControlPersist starts that host's ProxyCommand, which inherits ssh's
// stderr and lives for the persist window; with a pipe, Wait would sit on
// the copier until waitDelay on every such call and then report
// exec.ErrWaitDelay for a command that succeeded
// (TestOverDoesNotWaitOnAHeldStderr). A file has no copier to wait on.
// ErrWaitDelay after a zero exit is still read as success, for a held
// stdout nobody has seen yet.
func (h Host) ssh(ctx context.Context, script string) (string, error) {
	if strings.HasPrefix(h.target, "-") {
		return "", fmt.Errorf("ssh: %q is not a host", h.target)
	}
	errFile, err := os.CreateTemp("", "herdr-draft-ssh-*")
	if err != nil {
		return "", fmt.Errorf("ssh %s: %w", h.target, err)
	}
	defer os.Remove(errFile.Name())
	defer errFile.Close()

	cmd := exec.CommandContext(ctx, "ssh", sshArgs(h.target, script)...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = errFile
	cmd.WaitDelay = waitDelay
	err = cmd.Run()
	if errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil && cmd.ProcessState.Success() {
		err = nil
	}
	if err != nil {
		var stderr []byte
		if _, seekErr := errFile.Seek(0, io.SeekStart); seekErr == nil {
			stderr, _ = io.ReadAll(errFile)
		}
		if exitCode(err) == 255 {
			return "", fmt.Errorf("ssh %s: %w: %s", h.target, err, strings.TrimSpace(string(stderr)))
		}
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(stderr)))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// sshArgs is ssh's argv for running script on target.
//
// Batch mode and no tty: nothing may prompt (remoteGit's reason). A connect
// timeout and keepalives, because these calls run while a session is being
// made and cleaned, mostly with no deadline of their own, and an SSM link
// that stalls without an EOF would otherwise hang a multiplexed client for
// good -- herdr's own machine bridge sets keepalives for the same reason.
// ssh takes the first value it is given for an option and reads the command
// line first, so these override the user's ssh config for those three
// options. They are bounds rather than preferences, and none is shorter than
// a person would wait anyway.
//
// And the script runs under /bin/sh, not the remote user's login shell:
// OpenSSH hands the command to that shell, which may be fish or csh, and the
// scripts here are POSIX (`d="$HOME"/...`, `VAR=val exec`). herdr runs its
// own remote scripts through /bin/sh for the same reason.
func sshArgs(target, script string) []string {
	return []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=15",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=4",
		"-T", "--", target,
		"exec /bin/sh -c " + shellQuote(script),
	}
}

// exitCode is the process exit code err carries, or -1 when it carries
// none (a start failure, a context kill).
func exitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

// CloneProbe is what the remote machine says about a project directory
// (machine spec §7): the second of the two probes a remote session waits
// for before anything is created.
type CloneProbe struct {
	// Home is the remote $HOME, and Dir the project directory under it, as
	// the remote spells them -- absolute, because `herdr workspace create
	// --cwd` is not tilde-expanded by the server (machine spec §3.2).
	Home, Dir string
	// Exists and IsRepo are answers, not failures: the caller words each.
	Exists, IsRepo bool
	// Origin is remote.origin.url as configured there, "" when unset.
	Origin string
	// Branches are the clone's local branch names, the remote half of the
	// duplicate-branch check (herdr checks out an existing local branch and
	// ignores the base, so a name only the remote has must be caught here).
	Branches []string
}

// ProbeClone asks h about the directory rel, relative to its $HOME, in one
// ssh. rel is quoted for the remote shell and "$HOME" in front of it is
// left for that shell to expand, which is how a laptop path under /home/zvi
// becomes the same project under dev's /home/ubuntu. Every git question is
// asked inside the directory with the repository variables unset, for
// remoteGit's reason. Only ssh failing is an error.
func (h Host) ProbeClone(ctx context.Context, rel string) (CloneProbe, error) {
	if !h.Remote() {
		return CloneProbe{}, errors.New("probe clone: this machine is not a remote")
	}
	script := strings.Join([]string{
		"unset " + strings.Join(localRepoEnvVars, " "),
		`printf 'home\t%s\n' "$HOME"`,
		`d="$HOME"/` + shellQuote(rel),
		`printf 'dir\t%s\n' "$d"`,
		`cd -- "$d" 2>/dev/null || exit 0`,
		`printf 'exists\n'`,
		`[ "$(GIT_TERMINAL_PROMPT=0 git rev-parse --is-inside-work-tree 2>/dev/null)" = true ] || exit 0`,
		`printf 'repo\n'`,
		`printf 'origin\t%s\n' "$(git config --get remote.origin.url)"`,
		`git for-each-ref --format='branch%09%(refname:short)' refs/heads`,
	}, "; ")
	out, err := h.ssh(ctx, script)
	if err != nil {
		return CloneProbe{}, fmt.Errorf("probe %s on %s: %w", rel, h.target, err)
	}
	var p CloneProbe
	for _, line := range strings.Split(out, "\n") {
		key, value, _ := strings.Cut(line, "\t")
		switch key {
		case "home":
			p.Home = value
		case "dir":
			p.Dir = value
		case "exists":
			p.Exists = true
		case "repo":
			p.IsRepo = true
		case "origin":
			p.Origin = value
		case "branch":
			p.Branches = append(p.Branches, value)
		}
	}
	return p, nil
}
