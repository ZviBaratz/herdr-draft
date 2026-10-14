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
func (h Host) ExtendTrust(ctx context.Context, source, checkout string) (EnvTrust, error) {
	out, err := h.sh(ctx, extendTrustScript(source, checkout))
	if err != nil {
		return EnvTrust{}, fmt.Errorf("extending trust to %s: %w", checkout, err)
	}
	var tr EnvTrust
	for _, line := range strings.Split(out, "\n") {
		key, value, _ := strings.Cut(line, " ")
		switch {
		case key == "mise" && validMise(Mise(value)):
			tr.Mise = Mise(value)
		case key == "envrc" && validEnvrc(Envrc(value)):
			tr.Envrc = Envrc(value)
		default:
			return EnvTrust{}, fmt.Errorf("extending trust to %s: unexpected answer %q", checkout, out)
		}
	}
	return tr, nil
}

func validMise(m Mise) bool {
	switch m {
	case MiseAbsent, MiseTrusted, MiseDiffers, MiseNotTrusted:
		return true
	}
	return false
}

func validEnvrc(e Envrc) bool {
	switch e {
	case EnvrcAbsent, EnvrcAllowed, EnvrcDiffers, EnvrcNotAllowed, EnvrcHeld:
		return true
	}
	return false
}

// miseConfigFiles are the config files mise reads from a project directory,
// relative to it: its documented names, including the local and per-env
// variants (mise.local.toml, mise.<env>.toml) and the conf.d directories.
// `mise trust` trusts the directory, so anything here the checkout has is
// trusted with it, and each one has to be the main clone's.
var miseConfigFiles = []string{
	"mise.toml", "mise.*.toml", ".mise.toml", ".mise.*.toml",
	"mise/config.toml", ".mise/config.toml", ".config/mise.toml", ".config/mise/config.toml",
	"mise/conf.d/*.toml", ".mise/conf.d/*.toml", ".config/mise/conf.d/*.toml",
}

// extendTrustScript is ExtendTrust's script. POSIX sh, and nothing outside
// it but git, cmp, direnv and mise, because it runs on whatever the remote
// has. Its stdout is two lines, "mise <verdict>" and "envrc <verdict>".
//
// $HOME/.local/bin is appended to PATH, after everything already on it:
// mise's installer puts it there, and a non-interactive ssh's PATH does not
// include it (dev, 2026-10-14). Not finding it would read as "no mise" and
// allow an .envrc into the prompt EnvrcHeld exists to avoid.
//
// `mise trust --show` prints one "<dir>: trusted|untrusted" line per
// directory from the current one up, with $HOME spelled "~"; the main
// clone's own line is the one read. `direnv status` has two text formats in
// use: "Found RC allowed true" up to 2.32 (dev runs 2.25.2, which also
// ignores --json), and the AllowStatus number since 2.33, where 0 is
// allowed and 1 and 2 are not-allowed and denied (the laptop runs 2.37.1).
// Anything else reads as not trusted or not allowed.
func extendTrustScript(source, checkout string) string {
	return strings.Join([]string{
		"unset " + strings.Join(localRepoEnvVars, " "),
		`PATH=$PATH:$HOME/.local/bin`,
		`wt=` + shellQuote(checkout),
		`src=` + shellQuote(source),
		`top=$(GIT_TERMINAL_PROMPT=0 git -C "$src" rev-parse --show-toplevel 2>/dev/null) || top=$src`,
		// mise
		`mfiles() { (cd -- "$1" 2>/dev/null && for f in ` + strings.Join(miseConfigFiles, " ") + `; do [ -f "$f" ] && printf '%s\n' "$f"; done); }`,
		`mtrusted() { d=$1; case $d in "$HOME"/*) d="~${d#"$HOME"}" ;; esac; (cd -- "$1" && mise trust --show 2>/dev/null) | while IFS= read -r l; do [ "$l" = "$d: trusted" ] && echo yes; done; }`,
		`m=`,
		`if command -v mise >/dev/null 2>&1; then`,
		`  wl=$(mfiles "$wt")`,
		`  if [ -n "$wl" ]; then`,
		`    if [ -n "$(printf '%s\n' "$wl" | while IFS= read -r f; do cmp -s -- "$top/$f" "$wt/$f" || echo x; done)" ]; then m=differs`,
		`    elif [ "$(mtrusted "$top")" != yes ]; then m=not_trusted`,
		`    else`,
		`      mise trust "$wt" >&2 || exit 3`,
		`      [ "$(mtrusted "$wt")" = yes ] || { echo "mise does not report $wt trusted after trusting it" >&2; exit 3; }`,
		`      m=trusted`,
		`    fi`,
		`  fi`,
		`fi`,
		// direnv
		`allowed() { (cd -- "$1" && direnv status 2>/dev/null) | while IFS= read -r l; do case "$l" in "Found RC allowed true"|"Found RC allowed 0") echo yes ;; esac; done; }`,
		`e=`,
		`if command -v direnv >/dev/null 2>&1 && [ -f "$wt/.envrc" ]; then`,
		`  if ! cmp -s -- "$top/.envrc" "$wt/.envrc"; then e=differs`,
		`  elif [ "$(allowed "$top")" != yes ]; then e=not_allowed`,
		`  elif [ "$m" = differs ] || [ "$m" = not_trusted ]; then e=held`,
		`  else`,
		`    direnv allow "$wt" >&2 || exit 3`,
		`    [ "$(allowed "$wt")" = yes ] || { echo "direnv does not report $wt/.envrc allowed after allowing it" >&2; exit 3; }`,
		`    e=allowed`,
		`  fi`,
		`fi`,
		`echo "mise $m"`,
		`echo "envrc $e"`,
	}, "\n")
}

// sh runs script under /bin/sh on h's machine and returns its trimmed
// stdout: h.ssh for another machine, and the same shell here.
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
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}
