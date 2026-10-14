package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// EnvTrust is what ExtendTrust found, and so what it did, for a new
// checkout's environment files: its .envrc, which direnv allows, and its
// mise config, which mise trusts. Neither is a git question; they are here
// because Host is, and the answer has to come from the machine the session
// runs on.
type EnvTrust struct {
	Envrc Envrc
	Mise  Mise
}

// Envrc is ExtendTrust's verdict on the checkout's .envrc.
type Envrc string

const (
	// EnvrcAbsent is nothing to say: the checkout has no .envrc, or the
	// machine has no direnv. Most repositories have neither, and a line
	// saying so on every session would be noise.
	EnvrcAbsent Envrc = ""
	// EnvrcAllowed: the checkout's .envrc is byte-identical to the main
	// clone's, which direnv already allowed, and direnv now allows the
	// checkout's too.
	EnvrcAllowed Envrc = "allowed"
	// EnvrcDiffers: the checkout's .envrc is not the main clone's -- the
	// branch changed it, or added one the main clone does not have -- so it
	// was left blocked for the operator to review.
	EnvrcDiffers Envrc = "differs"
	// EnvrcNotAllowed: the two are identical, but direnv does not allow the
	// main clone's either, so there is no allow to extend.
	EnvrcNotAllowed Envrc = "not_allowed"
	// EnvrcAllowing: the allow was started and the step then failed, so the
	// .envrc may well be allowed. Only ever returned beside an error -- and
	// it is why ExtendTrust returns what it saw even then: a shell that may
	// be loading an environment needs the longer wait (plan's envLoaded).
	EnvrcAllowing Envrc = "allowing"
	// EnvrcDenied: direnv records an explicit deny for the checkout (status
	// 2, direnv 2.33+), which the operator made and this does not override.
	EnvrcDenied Envrc = "denied"
	// EnvrcHeld: the .envrc would have been allowed, but the checkout has a
	// mise config mise does not trust (Mise says why), so it was left
	// blocked. An allowed .envrc that runs `mise activate` over an
	// untrusted config stops the pane's first prompt on mise's own
	// question, and the agent cannot start in a shell that is busy asking
	// it -- measured on dev, 2026-10-14. Blocked, the session starts as it
	// did before any of this.
	EnvrcHeld Envrc = "held"
)

// Mise is ExtendTrust's verdict on the checkout's mise config.
type Mise string

const (
	// MiseAbsent is nothing to say: the checkout has no mise config, or the
	// machine has no mise.
	MiseAbsent Mise = ""
	// MiseTrusted: every mise config file the checkout has is byte-identical
	// to the main clone's, which mise already trusts, and mise now trusts
	// the checkout's too.
	MiseTrusted Mise = "trusted"
	// MiseDiffers: a mise config file in the checkout is not the main
	// clone's, or the main clone has no such file.
	MiseDiffers Mise = "differs"
	// MiseNotTrusted: the files are the main clone's, but mise does not
	// trust the main clone's either.
	MiseNotTrusted Mise = "not_trusted"
	// MiseUnknown: the checkout has a mise config and no mise was found to
	// ask about it -- not on PATH, nor where its installers put it. Counted
	// as untrusted, so it holds the .envrc: a mise the shell finds and this
	// did not would otherwise meet the .envrc's `mise activate` with a
	// prompt.
	MiseUnknown Mise = "unknown"
)

// ExtendTrust extends the operator's own trust in the main clone's
// environment files to a worktree just cut from it -- checkout, a worktree
// of the repository at source -- for the files that are the same bytes, and
// for nothing else.
//
// mise first: every mise config file the checkout has (miseConfigFiles) must
// be byte-identical to the one at the same path in the root of source's
// repository, and mise must already trust that directory, by its own
// judgement -- `mise trust --show` there. Then `mise trust` in the checkout.
//
// Then direnv: the checkout's .envrc must be byte-identical to the main
// clone's, direnv must already allow that one by its own judgement --
// `direnv status` in the main clone -- and mise's half must not have left an
// untrusted config behind (EnvrcHeld). Then `direnv allow` in the checkout.
// Neither tool's database is ever read directly.
//
// The operator's one explicit allow or trust of the main clone is the
// anchor. direnv keys an allow on the file's path and content, and mise a
// trust on the directory, so what this extends is a path: the same bytes,
// already approved, in a second checkout of the same repository. That is
// also all either tool asks of the main clone -- its allowed .envrc goes on
// running whatever the checked-out branch's other files say, so a file it
// sources or a flake it evaluates is trusted no further here than there.
//
// The whole of it is one script on h's machine -- one ssh for another
// machine (machine spec §8.2), never the laptop's tools. One, because it
// races the new pane's shell: its first prompt runs direnv's hook, and an
// allow that lands after it prints ".envrc is blocked" anyway. A verdict is
// said only once the tool itself reports it; an error is ssh failing, or a
// trust or an allow that did not take.
//
// On an error it still returns what the script had said by then, because
// "envrc allowing" -- printed just before the allow -- means the pane's
// shell may load the .envrc whatever went wrong afterwards.
func (h Host) ExtendTrust(ctx context.Context, source, checkout string) (EnvTrust, error) {
	out, err := h.sh(ctx, extendTrustScript(source, checkout))
	var tr EnvTrust
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		key, value, _ := strings.Cut(line, " ")
		switch {
		case key == "mise" && validMise(Mise(value)):
			tr.Mise = Mise(value)
		case key == "envrc" && validEnvrc(Envrc(value)):
			tr.Envrc = Envrc(value)
		case err == nil:
			return EnvTrust{}, fmt.Errorf("extending trust to %s: unexpected answer %q", checkout, out)
		}
	}
	if err != nil {
		return tr, fmt.Errorf("extending trust to %s: %w", checkout, err)
	}
	return tr, nil
}

func validMise(m Mise) bool {
	switch m {
	case MiseAbsent, MiseTrusted, MiseDiffers, MiseNotTrusted, MiseUnknown:
		return true
	}
	return false
}

func validEnvrc(e Envrc) bool {
	switch e {
	case EnvrcAbsent, EnvrcAllowed, EnvrcAllowing, EnvrcDiffers, EnvrcNotAllowed, EnvrcDenied, EnvrcHeld:
		return true
	}
	return false
}

// miseConfigFiles are patterns, relative to a project directory, for the
// files mise reads there: the config names mise 2026.6.14's binary lists
// (mise.toml, .mise.toml, .rtx.toml, the mise/, .mise/ and .config/mise
// forms, conf.d), each pattern wide enough to take their .local and
// per-environment variants; .tool-versions; and .miserc.toml, which can
// choose which per-environment file is read.
//
// It is a comparison, not the trust boundary. `mise trust` trusts the
// checkout's whole directory, so a name missing here is trusted unread --
// but no further than the main clone's own trust already goes: the same
// file on that branch, checked out in the main clone, would be read under
// it. Matching is the extra caution that keeps a branch's changed config
// for review.
var miseConfigFiles = []string{
	"mise.toml", "mise.*.toml", ".mise.toml", ".mise.*.toml", ".rtx.toml", ".rtx.*.toml",
	"mise/*.toml", "mise/conf.d/*.toml", ".mise/*.toml", ".mise/conf.d/*.toml",
	".config/mise.toml", ".config/mise.*.toml", ".config/mise/*.toml", ".config/mise/conf.d/*.toml",
	".tool-versions", ".miserc.toml", ".config/miserc.toml",
}

// extendTrustScript is ExtendTrust's script. POSIX sh, and nothing outside
// it but git, cmp, direnv and mise, because it runs on whatever the remote
// has. Its stdout is "mise <verdict>", then "envrc <verdict>" -- with an
// "envrc allowing" between them once the allow has begun, which is what a
// caller reads from a script that failed after it.
//
// Where mise's installers put it is appended to PATH, after everything
// already on it: ~/.local/bin (its install script; dev, where a
// non-interactive ssh's PATH lacks it), ~/.local/share/mise/bin, cargo's
// bin and Homebrew's. A checkout with a mise config and no mise found is
// MiseUnknown, which holds the .envrc rather than allowing it into the
// prompt EnvrcHeld exists to avoid.
//
// The main clone's root is found with --show-cdup and a cd, not with
// --show-toplevel, because the latter resolves symlinks: direnv keys an
// allow on the path the operator's shell spelled, so a clone reached
// through one -- macOS's /var, or a ~/quantivly that links elsewhere --
// has to be asked about by that spelling, or it reads as not allowed.
//
// `mise trust --show` prints one "<dir>: trusted|untrusted" line per
// directory from the current one up, with $HOME spelled "~"; the main
// clone's own line is the one read, under its spelled or its resolved
// path, since mise may name either. `direnv status` has two text formats in
// use: "Found RC allowed true" up to 2.32 (dev runs 2.25.2, which also
// ignores --json), and the AllowStatus number since 2.33, where 0 is
// allowed and 1 and 2 are not-allowed and denied (the laptop runs 2.37.1).
// Anything else reads as not trusted or not allowed.
func extendTrustScript(source, checkout string) string {
	return strings.Join([]string{
		"unset " + strings.Join(localRepoEnvVars, " "),
		`PATH=$PATH:$HOME/.local/bin:$HOME/.local/share/mise/bin:$HOME/.cargo/bin:/opt/homebrew/bin`,
		`wt=` + shellQuote(checkout),
		`src=` + shellQuote(source),
		`top=$(cd -- "$src" 2>/dev/null && c=$(GIT_TERMINAL_PROMPT=0 git rev-parse --show-cdup 2>/dev/null) && cd -- "./$c" && pwd) || top=$src`,
		// mise
		`mfiles() { (cd -- "$1" 2>/dev/null && for f in ` + strings.Join(miseConfigFiles, " ") + `; do [ -f "$f" ] && printf '%s\n' "$f"; done); }`,
		`tilde() { case $1 in "$HOME"/*) printf '~%s' "${1#"$HOME"}" ;; *) printf '%s' "$1" ;; esac; }`,
		`mtrusted() { d=$(tilde "$1"); p=$(tilde "$(cd -- "$1" && pwd -P)"); (cd -- "$1" && mise trust --show 2>/dev/null) | while IFS= read -r l; do { [ "$l" = "$d: trusted" ] || [ "$l" = "$p: trusted" ]; } && echo yes; done; }`,
		`m=`,
		`wl=$(mfiles "$wt")`,
		`if [ -n "$wl" ] && ! command -v mise >/dev/null 2>&1; then m=unknown`,
		`elif [ -n "$wl" ]; then`,
		`  if [ -n "$(printf '%s\n' "$wl" | while IFS= read -r f; do cmp -s -- "$top/$f" "$wt/$f" || echo x; done)" ]; then m=differs`,
		`  elif [ "$(mtrusted "$top")" != yes ]; then m=not_trusted`,
		`  else`,
		`    mise trust "$wt" >&2 || exit 3`,
		`    [ "$(mtrusted "$wt")" = yes ] || { echo "mise does not report $wt trusted after trusting it" >&2; exit 3; }`,
		`    m=trusted`,
		`  fi`,
		// A path is reused when a branch name is: whatever trust an earlier
		// session left on it covered content nobody compared this time.
		`  if [ "$m" != trusted ] && [ "$(mtrusted "$wt")" = yes ]; then mise trust --untrust "$wt" >&2 || exit 3; fi`,
		`fi`,
		`echo "mise $m"`,
		// direnv
		`allowed() { (cd -- "$1" && direnv status 2>/dev/null) | while IFS= read -r l; do case "$l" in "Found RC allowed true"|"Found RC allowed 0") echo yes ;; "Found RC allowed 2") echo denied ;; esac; done; }`,
		`e=`,
		`if command -v direnv >/dev/null 2>&1 && [ -f "$wt/.envrc" ]; then`,
		`  if ! cmp -s -- "$top/.envrc" "$wt/.envrc"; then e=differs`,
		`  elif [ "$(allowed "$top")" != yes ]; then e=not_allowed`,
		`  elif [ "$(allowed "$wt")" = denied ]; then e=denied`,
		`  elif [ "$m" = differs ] || [ "$m" = not_trusted ] || [ "$m" = unknown ]; then e=held`,
		`  else`,
		`    echo "envrc allowing"`,
		`    direnv allow "$wt" >&2 || exit 3`,
		`    [ "$(allowed "$wt")" = yes ] || { echo "direnv does not report $wt/.envrc allowed after allowing it" >&2; exit 3; }`,
		`    e=allowed`,
		`  fi`,
		`fi`,
		`echo "envrc $e"`,
	}, "\n")
}

// sh runs script under /bin/sh on h's machine and returns its trimmed
// stdout: h.ssh for another machine, and the same shell here. On a failure
// the stdout so far comes back beside the error, as h.ssh's does.
func (h Host) sh(ctx context.Context, script string) (string, error) {
	if h.Remote() {
		return h.ssh(ctx, script)
	}
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = waitDelay
	err := cmd.Run()
	if errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil && cmd.ProcessState.Success() {
		err = nil
	}
	if err != nil {
		return strings.TrimSpace(stdout.String()), fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}
