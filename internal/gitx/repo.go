package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// IsGitRepo reports whether dir is inside a git working tree. It never
// panics; any failure to invoke git (missing binary, non-repo dir, etc.)
// is treated as "not a git repo".
//
// One of two calls in this package that deliberately do NOT go through
// runGit (the other is BranchExists), and the rule for both is the same:
// `rev-parse` and `show-ref` read local refs, so there is no remote to
// authenticate to and nothing that could prompt. They skip runGit because
// they are the package's hottest calls -- this one runs on directory
// validity for a path the user is still typing -- and runGit now resolves
// core.sshCommand, a second git process each time (effectiveSSHCommand).
// Anything added here that could reach a remote must move to runGit
// instead; that is where the protections are.
func IsGitRepo(dir string) bool {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

// waitDelay bounds how long cmd.Wait may block after the context has
// killed git -- see runGit, where the reason it is needed at all is.
const waitDelay = 2 * time.Second

// runGit runs git with the given args in repoDir and returns trimmed
// stdout. On failure it returns an error wrapped with the command and
// repo directory for context.
func runGit(ctx context.Context, repoDir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoDir
	cmd.Env = nonInteractiveEnv(os.Environ(), effectiveSSHCommand(ctx, os.Environ(), repoDir))
	// exec.CommandContext kills git when ctx is done, but stdout/stderr
	// are bytes.Buffers, so exec pipes them and cmd.Wait blocks on its
	// copier goroutines until every writer closes -- a grandchild that
	// outlives the kill (an ssh or git-remote-http holding the write end)
	// keeps Wait blocked with it. WaitDelay is what makes the kill
	// actually return. It bounds THIS call, not that grandchild: the kill
	// goes to git alone, and a helper git spawned is orphaned rather than
	// killed.
	cmd.WaitDelay = waitDelay
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s (in %s): %w: %s", strings.Join(args, " "), repoDir, err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// nonInteractiveEnv returns base plus the two settings that stop git
// asking a human anything. It is applied in runGit rather than at the one
// call site that reaches the network because a per-call opt-in is the
// shape that rots: no caller of this package should ever prompt.
//
// GIT_TERMINAL_PROMPT=0 is the load-bearing one. git does not ask for
// credentials on the stdin it was given -- it opens /dev/tty and writes
// `Username for '…':` there, which inside the popup is the pty Bubble Tea
// is drawing the form on, so a null stdin buys nothing. BatchMode=yes is
// the same refusal one layer down, for a passphrase-protected ssh key
// with no agent behind it.
//
// ssh is the command to put in batch mode -- already resolved, by
// effectiveSSHCommand, in git's own precedence order; "" means plain ssh.
// Resolving first is not a nicety: GIT_SSH_COMMAND outranks both the
// GIT_SSH variable and core.sshCommand, so setting it unconditionally
// would not merely fail to honour a user's own ssh -- it would silently
// override it, which is a worse bug than the prompt this prevents.
//
// Neither setting stops a configured GIT_ASKPASS/SSH_ASKPASS helper, which
// git still calls and which may be a GUI dialog that never returns; that
// hazard is the caller's deadline's, not this function's.
//
// base is a parameter so a test can supply one instead of mutating the
// process environment; production passes os.Environ(). Building on it is
// mandatory rather than stylistic: cmd.Env = nil means "inherit", so
// assigning a two-element slice would strip PATH and everything else.
func nonInteractiveEnv(base []string, ssh string) []string {
	if strings.TrimSpace(ssh) == "" {
		ssh = "ssh"
	}
	env := make([]string, 0, len(base)+2)
	for _, kv := range base {
		// Dropped rather than shadowed: a duplicate key leaves which one
		// wins up to exec, and the tests assert exactly one of each.
		if strings.HasPrefix(kv, "GIT_TERMINAL_PROMPT=") || strings.HasPrefix(kv, "GIT_SSH_COMMAND=") {
			continue
		}
		env = append(env, kv)
	}
	return append(env,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_SSH_COMMAND="+ssh+" -oBatchMode=yes",
	)
}

// effectiveSSHCommand answers which ssh git would run for repoDir if we
// changed nothing, in git's own order: the GIT_SSH_COMMAND variable, then
// core.sshCommand, then the GIT_SSH variable, then plain ssh. That order is
// git's connect.c at v2.53.0: get_ssh_command() returns GIT_SSH_COMMAND and
// then core.sshcommand, and only when both are unset does git_connect()
// fall back to getenv("GIT_SSH")
// (https://github.com/git/git/blob/v2.53.0/connect.c#L1152-L1163 and
// #L1379-L1391). GIT_SSH ranking above the config was #130's mistake: a
// global GIT_SSH wrapper then overrode a per-repo core.sshCommand, which
// plain git never does. "" is never returned; the caller gets something it
// can append an option to.
//
// GIT_SSH is quoted and the other two are not, and that asymmetry is git's
// rather than a choice here: git treats GIT_SSH as one bare program path
// and never splits it, while GIT_SSH_COMMAND and core.sshCommand are
// shell-parsed. Folding an unquoted GIT_SSH into GIT_SSH_COMMAND would
// split `/opt/my ssh/bin/ssh` into a program and an argument.
//
// The config read is a plain local `git config` -- no network, nothing to
// prompt for -- and is deliberately NOT routed through runGit, which calls
// this: that would recurse. It is skipped only when GIT_SSH_COMMAND is
// set -- a GIT_SSH no longer skips it, because the config outranks it --
// so it is the price of every other case, and that price is not nothing:
// measured here at a median of 19ms (p90 26ms), it roughly doubles the
// wall time of every runGit call. It is paid because the alternative is
// overriding the user's ssh, it is paid on a tea.Cmd goroutine rather than
// the UI thread, and spec §8's debounce window is 150ms -- so it buys
// correctness inside a budget that was already there.
//
// Caveat, stated because it cannot be fixed here: -oBatchMode=yes is
// OpenSSH's spelling, and a non-OpenSSH wrapper such as PuTTY's plink
// rejects -o. herdr-draft installs on Linux and macOS only (the manifest's
// platforms), where plink is not the ssh anyone has, and the blast radius
// if someone does use such a wrapper is one best-effort background fetch:
// FetchPrune is the only call in this package that reaches ssh at all, and
// its failure is already swallowed by design.
func effectiveSSHCommand(ctx context.Context, base []string, repoDir string) string {
	var gitSSH string
	for _, kv := range base {
		if v, ok := strings.CutPrefix(kv, "GIT_SSH_COMMAND="); ok && strings.TrimSpace(v) != "" {
			return v
		}
		if v, ok := strings.CutPrefix(kv, "GIT_SSH="); ok && strings.TrimSpace(v) != "" {
			gitSSH = v
		}
	}
	if v := configValue(ctx, base, repoDir, "core.sshCommand"); v != "" {
		return v
	}
	if gitSSH != "" {
		return shellQuote(gitSSH)
	}
	return "ssh"
}

// configValue reads one git config key in repoDir, answering "" for an
// unset key or any failure -- every caller has a usable default, and a
// repoDir that is not a repository at all is an ordinary case here.
func configValue(ctx context.Context, base []string, repoDir, key string) string {
	cmd := exec.CommandContext(ctx, "git", "config", "--get", key)
	cmd.Dir = repoDir
	// "" rather than a resolved command: the whole point of this call is
	// to find that out, and `git config` reaches no ssh of any kind.
	cmd.Env = nonInteractiveEnv(base, "")
	cmd.WaitDelay = waitDelay
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// shellQuote wraps s so a shell reads it as exactly one word, for the one
// value here that git will shell-parse but was not written to be (see
// effectiveSSHCommand on GIT_SSH).
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// RepoRoot returns the root of the repository containing dir -- the ORIGIN
// repository's root, not a linked worktree's own checkout, so every
// worktree of one repository shares a single per-project memory entry
// (spec §10).
//
// The distinction is the whole point of this function. `rev-parse
// --show-toplevel` names the CHECKOUT: inside ~/repo/.worktrees/feature it
// answers ~/repo/.worktrees/feature, which would give every worktree of one
// repository its own projects.json entry and defeat the feature for exactly
// the users most likely to want it. `--git-common-dir` instead names the
// SHARED .git directory (~/repo/.git from either place), whose parent is
// the origin root.
//
// --git-common-dir has been able to return a relative path ("." inside a
// main worktree's .git, or a relative path from the cwd) for most of its
// life; `--path-format=absolute`, added in git 2.31, is what makes the
// answer usable without knowing what it was relative to. On an older git
// that flag is an error, so this falls back to --show-toplevel: the wrong
// answer for a linked worktree, but a correct and stable one for the main
// checkout, which is better than no memory at all.
//
// A dir that is not a git repository returns ("", nil) -- the caller keys
// on the canonical path instead (pathx.CanonicalKey), which is not a
// failure. A real failure (git missing, an unreadable repository) returns
// an error.
//
// Known limitation, deliberately not handled: a repository whose git
// directory lives outside the working tree (`git init --separate-git-dir`,
// or a GIT_DIR pointing elsewhere) has no "parent of the common dir" that
// names its checkout. Such a repository gets a key derived from the git
// directory's parent instead -- still stable, still shared between that
// repository's worktrees, just not equal to the checkout path.
func RepoRoot(ctx context.Context, dir string) (string, error) {
	if out, err := runGit(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir"); err == nil && out != "" {
		return filepath.Dir(filepath.Clean(out)), nil
	}
	// Either git predates --path-format (< 2.31) or dir is not a
	// repository at all -- repoRootFallback separates the two.
	return repoRootFallback(ctx, dir)
}

// repoRootFallback is RepoRoot's git-older-than-2.31 path, split out so it
// is directly testable: no supported way exists to make a modern git reject
// --path-format, and an untested fallback is a fallback that will be broken
// the day someone needs it.
//
// --show-toplevel is in every git this plugin could run against, and fails
// only for a non-repository -- which is how a missing --path-format is told
// apart from "dir is not a repo". Its answer is the CHECKOUT, so on an old
// git a linked worktree keys separately from its origin; that is the known
// cost of the fallback, and better than no memory at all.
func repoRootFallback(ctx context.Context, dir string) (string, error) {
	top, err := runGit(ctx, dir, "rev-parse", "--show-toplevel")
	if err == nil {
		return filepath.Clean(top), nil
	}
	if !IsGitRepo(dir) {
		return "", nil
	}
	return "", fmt.Errorf("repo root: %w", err)
}

// ListBranches returns at most limit branch names in repoDir, newest by
// committer date first. Local and remote-tracking names for the same
// branch are deduped (the "origin/" prefix is stripped), and
// "origin/HEAD" is dropped. A limit of 0 (or negative) yields no results.
func ListBranches(ctx context.Context, repoDir string, limit int) ([]string, error) {
	out, err := runGit(ctx, repoDir, "branch", "-a", "--sort=-committerdate", "--format=%(refname:short)")
	if err != nil {
		return nil, fmt.Errorf("list branches: %w", err)
	}

	var result []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(out, "\n") {
		if len(result) >= limit {
			break
		}
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		name = strings.TrimPrefix(name, "origin/")
		if name == "HEAD" {
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		result = append(result, name)
	}
	return result, nil
}

// BranchExists reports whether name exists as a local branch or as an
// origin remote-tracking branch in repoDir. Note "remote-tracking": it
// reads refs git has already fetched and contacts no remote itself.
//
// Bypasses runGit for the reason IsGitRepo's doc comment sets out, plus
// one of its own: it needs show-ref's exit code 1 ("no such ref")
// separated from a real failure, which is a distinction runGit's single
// wrapped error does not offer its callers.
func BranchExists(ctx context.Context, repoDir, name string) (bool, error) {
	for _, ref := range []string{"refs/heads/" + name, "refs/remotes/origin/" + name} {
		cmd := exec.CommandContext(ctx, "git", "show-ref", "--verify", "--quiet", ref)
		cmd.Dir = repoDir
		err := cmd.Run()
		if err == nil {
			return true, nil
		}
		// show-ref exits 1 when the ref is not found; that is a normal
		// negative result, not a failure worth reporting. Any other exit
		// code (e.g. 128 for "not a git repository") is a real error.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			continue
		}
		return false, fmt.Errorf("git show-ref --verify --quiet %s (in %s): %w", ref, repoDir, err)
	}
	return false, nil
}

// CurrentBranch returns the name of the branch checked out in repoDir,
// or "" when HEAD is detached. A detached HEAD is not an error.
func CurrentBranch(ctx context.Context, repoDir string) (string, error) {
	out, err := runGit(ctx, repoDir, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil {
		// symbolic-ref exits non-zero (without any other failure) when
		// HEAD is detached; that is expected and not an error condition.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return "", nil
		}
		return "", fmt.Errorf("current branch: %w", err)
	}
	return out, nil
}

// FetchPrune runs `git fetch --prune` in repoDir -- spec §6 field 4's
// background "fires once per repo per form-open" refresh of remote-tracking
// branches, ported in spirit from Atrium's own git.FetchBranches
// (app/app_branchsearch.go, on this task's clean list): best-effort, so a
// remoteless or offline repo simply keeps its local view rather than
// failing the caller. Unlike Atrium's FetchBranches (which swallows the
// error itself, returning nothing), FetchPrune returns it so the app layer
// can log it -- see the app package's own doc on why: it wraps the call in
// a tea.Cmd that reports completion regardless of outcome, mirroring
// Atrium's own branchFetchDoneMsg's "completion always re-triggers a
// search" contract, which needs no separate success/failure signal.
func FetchPrune(ctx context.Context, repoDir string) error {
	_, err := runGit(ctx, repoDir, "fetch", "--prune")
	if err != nil {
		return fmt.Errorf("fetch --prune: %w", err)
	}
	return nil
}

// ResolveRef resolves ref to a full commit id in repoDir (`git rev-parse
// --verify <ref>^{commit}`). An unknown ref, or a ref naming something
// that is not a commit, is an error rather than an empty result.
//
// This exists for plan.CleanCheck: the form's base picker reports "" for
// its HEAD row, and passing that straight through to Disposable produced
// `git rev-list --count ..HEAD`, which git reads as HEAD..HEAD and
// therefore counts 0 for every worktree, however many commits it carries.
// Callers resolve the sentinel against the ORIGIN repo first (resolving it
// inside the worktree would be equally useless -- its own HEAD is exactly
// the thing being counted), so the count runs against a commit the
// worktree can actually be ahead of.
func ResolveRef(ctx context.Context, repoDir, ref string) (string, error) {
	if strings.TrimSpace(ref) == "" {
		return "", fmt.Errorf("resolve ref: empty ref in %s", repoDir)
	}
	out, err := runGit(ctx, repoDir, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("resolve ref %q: %w", ref, err)
	}
	if out == "" {
		return "", fmt.Errorf("resolve ref %q: no such commit in %s", ref, repoDir)
	}
	return out, nil
}

// Disposable reports whether the worktree at worktreeDir can be safely
// discarded relative to baseRef: it must have no uncommitted changes and
// no commits that baseRef doesn't already have. When ok is false, reason
// explains which check failed.
//
// baseRef must name something git can resolve. An empty baseRef is
// rejected outright rather than quietly becoming `git rev-list --count
// ..HEAD` -- see ResolveRef's own doc comment for why that shape made this
// function's second check unfailable for every caller holding nothing but
// the form's own "" == HEAD sentinel.
func Disposable(ctx context.Context, worktreeDir, baseRef string) (ok bool, reason string, err error) {
	if strings.TrimSpace(baseRef) == "" {
		return false, "", fmt.Errorf("disposable: empty base ref for worktree %s", worktreeDir)
	}

	status, err := runGit(ctx, worktreeDir, "status", "--porcelain")
	if err != nil {
		return false, "", fmt.Errorf("check worktree status: %w", err)
	}
	if status != "" {
		return false, "worktree has uncommitted changes", nil
	}

	countOut, err := runGit(ctx, worktreeDir, "rev-list", "--count", baseRef+"..HEAD")
	if err != nil {
		return false, "", fmt.Errorf("count commits ahead of %s: %w", baseRef, err)
	}
	count, convErr := strconv.Atoi(countOut)
	if convErr != nil {
		return false, "", fmt.Errorf("parse rev-list --count output %q: %w", countOut, convErr)
	}
	if count != 0 {
		return false, fmt.Sprintf("worktree has %d commit(s) not on %s", count, baseRef), nil
	}

	return true, "", nil
}
