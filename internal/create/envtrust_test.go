package create

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ZviBaratz/herdr-draft/internal/gitx"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
	"github.com/ZviBaratz/herdr-draft/internal/plan"
)

// What `create` says about a new worktree's .envrc: one line beside the
// worktree step's progress, and `envrc` under --json. It never changes the
// exit code -- the allow is best-effort, and an unloaded environment is the
// state before it existed.

// stubDirenv is internal/gitx's fakeDirenv, for this package: an allow
// database keyed on directory and content, `status` in 2.37's format, and
// an `allow` that exits $DIRENV_STUB_ALLOW_EXIT when that is non-zero.
func stubDirenv(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	db := t.TempDir()
	script := `#!/bin/sh
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
  cp "${2:-$PWD}/.envrc" '` + db + `'/"$(key "${2:-$PWD}")"
  ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "direnv"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	// The trust script also looks in $HOME/.local/bin, for mise.
	t.Setenv("HOME", t.TempDir())
}

// envrcCreate runs a successful worktree create from a repository whose
// committed .envrc the operator has allowed in the main clone. edit, when
// set, changes the checkout after herdr makes it.
func envrcCreate(t *testing.T, edit func(checkout string), args ...string) (*harness, int) {
	t.Helper()
	stubDirenv(t)
	repo := gitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, ".envrc"), []byte("PATH_add .venv/bin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitDo(t, repo, "add", ".envrc")
	gitDo(t, repo, "commit", "-q", "-m", "envrc")
	if out, err := exec.Command("direnv", "allow", repo).CombinedOutput(); err != nil {
		t.Fatalf("direnv allow: %v\n%s", err, out)
	}

	h := newHarness(t)
	checkout := filepath.Join(t.TempDir(), "wt")
	h.runner.worktreeAdd = func(req herdrc.WorktreeCreateReq) string {
		gitDo(t, repo, "worktree", "add", "-q", "-b", req.Branch, checkout, "HEAD")
		if edit != nil {
			edit(checkout)
		}
		return checkout
	}
	return h, h.run(append([]string{"--title", "envrc", "--worktree", "--project", repo}, args...)...)
}

func TestCreateSaysItAllowedTheEnvrc(t *testing.T) {
	t.Run("human", func(t *testing.T) {
		h, code := envrcCreate(t, nil)
		if code != ExitOK {
			t.Fatalf("exit = %d\nstderr: %s", code, h.stderr)
		}
		if want := "creating worktree ... ok (.envrc allowed: same as the main clone's)\n"; !strings.Contains(h.stderr.String(), want) {
			t.Errorf("stderr = %q, want a line ending %q", h.stderr, want)
		}
	})
	t.Run("json", func(t *testing.T) {
		h, code := envrcCreate(t, nil, "--json")
		if code != ExitOK {
			t.Fatalf("exit = %d\nstderr: %s", code, h.stderr)
		}
		if out := cleanReport(t, h); out.Envrc != "allowed" || out.TrustError != "" {
			t.Errorf("envrc, trust_error = %q, %q; want allowed", out.Envrc, out.TrustError)
		}
	})
}

func TestCreateSaysItLeftADifferentEnvrcBlocked(t *testing.T) {
	changed := func(checkout string) {
		if err := os.WriteFile(filepath.Join(checkout, ".envrc"), []byte("export CHANGED=1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h, code := envrcCreate(t, changed, "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d\nstderr: %s", code, h.stderr)
	}
	if out := cleanReport(t, h); out.Envrc != "differs" {
		t.Errorf("envrc = %q, want differs", out.Envrc)
	}
	if want := "(.envrc left blocked: differs from the main clone's)"; !strings.Contains(h.stderr.String(), want) {
		t.Errorf("stderr = %q, want %q", h.stderr, want)
	}
}

// TestCreateSucceedsWhenTheAllowFails: exit 0, the session reported on
// stdout as always, and the failure under trust_error.
func TestCreateSucceedsWhenTheAllowFails(t *testing.T) {
	// The operator's own allow of the main clone works; the one after the
	// worktree is cut does not.
	failAllow := func(string) { t.Setenv("DIRENV_STUB_ALLOW_EXIT", "1") }
	h, code := envrcCreate(t, failAllow, "--json")
	if code != ExitOK {
		t.Fatalf("a failed direnv allow failed the create: exit = %d\nstderr: %s", code, h.stderr)
	}
	out := cleanReport(t, h)
	if !out.OK || out.Envrc != "allowing" || !strings.Contains(out.TrustError, "permission denied") {
		t.Errorf("ok, envrc, trust_error = %v, %q, %q; want true, allowing and direnv's words", out.OK, out.Envrc, out.TrustError)
	}
}

// TestCreateIsSilentWithTheAllowOff: `[worktree] inherit_trust = false`
// leaves the .envrc to the operator, and nothing is said.
func TestCreateIsSilentWithTheAllowOff(t *testing.T) {
	stubDirenv(t)
	repo := gitRepo(t)
	h := newHarness(t)
	writeFile(t, filepath.Join(h.env.ConfigDir, "config.toml"), "[worktree]\ninherit_trust = false\n")
	checkout := filepath.Join(t.TempDir(), "wt")
	h.runner.worktreeAdd = func(req herdrc.WorktreeCreateReq) string {
		gitDo(t, repo, "worktree", "add", "-q", "-b", req.Branch, checkout, "HEAD")
		if err := os.WriteFile(filepath.Join(checkout, ".envrc"), []byte("export X=1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return checkout
	}
	if code := h.run("--title", "envrc", "--worktree", "--project", repo, "--json"); code != ExitOK {
		t.Fatalf("exit = %d\nstderr: %s", code, h.stderr)
	}
	if out := cleanReport(t, h); out.Envrc != "" {
		t.Errorf("envrc = %q with the allow switched off, want nothing", out.Envrc)
	}
	if strings.Contains(h.stderr.String(), ".envrc") {
		t.Errorf("stderr mentions .envrc with the allow switched off: %q", h.stderr)
	}
}

// TestCreateReportsAnEnvrcHeldForMise: hub as it was on 2026-10-14, its
// .envrc allowed and its mise config trusted by nobody. The create succeeds
// with the .envrc left blocked, and --json says which tool held it.
func TestCreateReportsAnEnvrcHeldForMise(t *testing.T) {
	bin := t.TempDir()
	untrusting := "#!/bin/sh\n[ \"$1 $2\" = \"trust --show\" ] && echo \"$PWD: untrusted\"\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "mise"), []byte(untrusting), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	withMise := func(checkout string) {
		if err := os.WriteFile(filepath.Join(checkout, ".mise.toml"), []byte("[env]\nX = \"1\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h, code := envrcCreate(t, withMise, "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d\nstderr: %s", code, h.stderr)
	}
	if out := cleanReport(t, h); out.Envrc != "held" || out.Mise != "differs" {
		t.Errorf("envrc, mise = %q, %q; want held, differs", out.Envrc, out.Mise)
	}
}

// TestJSONReportsAnInterruptedEnvironmentLoad: a create whose agent started
// only after its environment load was interrupted is still a create -- ok,
// with the agent's environment missing said under env_load_interrupted.
func TestJSONReportsAnInterruptedEnvironmentLoad(t *testing.T) {
	var buf strings.Builder
	r := report{
		input:  plan.Input{Title: "envrc", AgentKind: "claude", UseWorktree: true},
		result: plan.ExecResult{FailedIndex: -1, EnvTrust: plan.TrustOutcome{Trust: gitx.EnvTrust{Envrc: gitx.EnvrcAllowed}, Interrupted: true}},
		json:   true,
	}
	r.writeJSON(&buf)
	var out jsonReport
	if err := json.Unmarshal([]byte(buf.String()), &out); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, buf.String())
	}
	if !out.OK || out.Envrc != "allowed" || !out.EnvLoadInterrupted {
		t.Errorf("ok, envrc, env_load_interrupted = %v, %q, %v; want true, allowed, true", out.OK, out.Envrc, out.EnvLoadInterrupted)
	}
}
