package create

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ZviBaratz/herdr-draft/internal/agentopts"
)

// Agent-options spec §8.1: each flag is registered from the declaration, so
// every option any kind declares has one. createUsage and the spawn skill
// are held to the flag set by their own tests; this holds the flag set to
// the declaration.
func TestEveryDeclaredOptionHasAFlag(t *testing.T) {
	flags := flagNames()
	for _, name := range agentopts.Names() {
		if !slices.Contains(flags, agentopts.FlagName(name)) {
			t.Errorf("agentopts declares %q but create has no --%s", name, agentopts.FlagName(name))
		}
	}
}

// Everything the options can get wrong is a usage error, reported before
// anything is created: a bad value, an option the kind does not declare,
// a mode that is refused rather than unknown, and a blank flag.
func TestAgentOptionFlags_Refusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"unknown effort", []string{"--effort", "ultra"}, []string{`invalid --effort "ultra"`, "expected one of low, medium, high, xhigh, max, or inherit"}},
		{"a model id that reads as a flag", []string{"--model", "-x"}, []string{`invalid --model "-x"`, "starts with a letter or digit"}},
		{"refused mode, with its reason", []string{"--permission-mode", "bypassPermissions"}, []string{`invalid --permission-mode "bypassPermissions"`, "confirmation dialog", "[agents.extra_args]"}},
		{"a kind that declares no such option", []string{"--agent", "codex", "--effort", "high"}, []string{"--effort does not apply to agent kind codex", "codex declares no effort option"}},
		{"blank", []string{"--model", " "}, []string{"--model needs a value", `"inherit"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			writeConfig(t, h.env.ConfigDir, "[agents]\nfavorites = [\"claude\", \"codex\"]\n")
			args := append([]string{"--title", "t", "--no-worktree"}, tc.args...)
			if code := h.run(args...); code != ExitUsage {
				t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitUsage, h.stderr)
			}
			for _, w := range tc.want {
				if !strings.Contains(h.stderr.String(), w) {
					t.Errorf("stderr does not contain %q:\n%s", w, h.stderr)
				}
			}
			if h.createdAnything() {
				t.Errorf("a refused request created something: %v", h.runner.calls)
			}
		})
	}
}

// §8.2: --json reports the options launched with, and provenance says
// where each came from -- the flag, config.toml, or built-in inherit.
func TestAgentOptions_JSONAndProvenance(t *testing.T) {
	h := newHarness(t)
	writeConfig(t, h.env.ConfigDir, "[agents.options.claude]\neffort = \"high\"\n")
	if code := h.run("--title", "t", "--no-worktree", "--json", "--model", "claude-opus-5[1m]"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	var out jsonReport
	if err := json.Unmarshal([]byte(h.stdout.String()), &out); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, h.stdout)
	}
	if want := (agentopts.Values{"model": "claude-opus-5[1m]", "effort": "high"}); !reflect.DeepEqual(out.AgentOptions, want) {
		t.Errorf("agent_options = %v, want %v", out.AgentOptions, want)
	}
	for field, want := range map[string]string{
		"option.model":           "flag",
		"option.effort":          "config.toml",
		"option.permission_mode": "built-in",
	} {
		if got := out.Provenance[field]; got != want {
			t.Errorf("provenance[%s] = %q, want %q", field, got, want)
		}
	}
}

// `inherit` clears a configured default for one session: nothing is sent,
// agent_options is absent, and provenance says the flag decided it.
func TestAgentOptions_InheritClearsAConfiguredDefault(t *testing.T) {
	h := newHarness(t)
	writeConfig(t, h.env.ConfigDir, "[agents.options.claude]\neffort = \"high\"\n")
	if code := h.run("--title", "t", "--no-worktree", "--json", "--effort", "inherit"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	if strings.Contains(h.stdout.String(), `"agent_options"`) {
		t.Errorf("agent_options present after --effort inherit:\n%s", h.stdout)
	}
	var out jsonReport
	if err := json.Unmarshal([]byte(h.stdout.String()), &out); err != nil {
		t.Fatalf("stdout is not JSON: %v", err)
	}
	if got := out.Provenance["option.effort"]; got != "flag" {
		t.Errorf("provenance[option.effort] = %q, want flag", got)
	}
}

// config.toml's refused [agents.options] entries reach stderr, the same
// list the popup's panel shows (§6.2), and do not stop the create.
func TestAgentOptions_ConfigWarningsReachStderr(t *testing.T) {
	h := newHarness(t)
	writeConfig(t, h.env.ConfigDir, "[agents.options.claude]\neffort = \"ultra\"\n")
	if code := h.run("--title", "t", "--no-worktree"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	if !strings.Contains(h.stderr.String(), `herdr-draft create: ignoring [agents.options.claude] effort = "ultra"`) {
		t.Errorf("stderr does not carry the warning:\n%s", h.stderr)
	}
}
