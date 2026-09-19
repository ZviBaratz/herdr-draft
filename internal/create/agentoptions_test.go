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

// #209: --json says what the agent's command line really carries for each
// declared option, [agents.extra_args] included. The #208 session ran on a
// model and an effort extra_args set, while agent_options was absent and
// provenance said built-in for both -- a report nobody could have read the
// session's configuration from.
func TestAgentOptions_LaunchOptionsCarryExtraArgs(t *testing.T) {
	const extra = "[agents.extra_args]\nclaude = [\"--model\", \"claude-opus-5[1m]\", \"--effort=xhigh\", \"--verbose\"]\n"
	for _, tc := range []struct {
		name       string
		config     string
		args       []string
		wantChosen agentopts.Values
		wantLaunch agentopts.Values
		wantProv   map[string]string
	}{
		{
			name:       "extra_args alone",
			config:     extra,
			wantLaunch: agentopts.Values{"model": "claude-opus-5[1m]", "effort": "xhigh"},
			wantProv: map[string]string{
				"option.model":           "extra_args",
				"option.effort":          "extra_args",
				"option.permission_mode": "built-in",
			},
		},
		{
			name:       "a flag displaces extra_args's own",
			config:     extra,
			args:       []string{"--effort", "high"},
			wantChosen: agentopts.Values{"effort": "high"},
			wantLaunch: agentopts.Values{"model": "claude-opus-5[1m]", "effort": "high"},
			wantProv: map[string]string{
				"option.model":  "extra_args",
				"option.effort": "flag",
			},
		},
		{
			name:       "inherit sends nothing of its own, so extra_args still decides",
			config:     extra,
			args:       []string{"--model", "inherit"},
			wantLaunch: agentopts.Values{"model": "claude-opus-5[1m]", "effort": "xhigh"},
			wantProv:   map[string]string{"option.model": "extra_args"},
		},
		{
			name:       "a configured option displaces extra_args's too",
			config:     extra + "[agents.options.claude]\nmodel = \"sonnet\"\npermission_mode = \"plan\"\n",
			wantChosen: agentopts.Values{"model": "sonnet", "permission_mode": "plan"},
			wantLaunch: agentopts.Values{"model": "sonnet", "effort": "xhigh", "permission_mode": "plan"},
			wantProv: map[string]string{
				"option.model":           "config.toml",
				"option.effort":          "extra_args",
				"option.permission_mode": "config.toml",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			writeConfig(t, h.env.ConfigDir, tc.config)
			args := append([]string{"--title", "t", "--no-worktree", "--json"}, tc.args...)
			if code := h.run(args...); code != ExitOK {
				t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
			}
			var out jsonReport
			if err := json.Unmarshal([]byte(h.stdout.String()), &out); err != nil {
				t.Fatalf("stdout is not JSON: %v\n%s", err, h.stdout)
			}
			// agent_options stays the CHOSEN ones -- plan.Input's -- so a
			// consumer already reading it is not handed extra_args's values
			// as if somebody had picked them.
			if !reflect.DeepEqual(out.AgentOptions, tc.wantChosen) {
				t.Errorf("agent_options = %v, want %v", out.AgentOptions, tc.wantChosen)
			}
			if !reflect.DeepEqual(out.LaunchOptions, tc.wantLaunch) {
				t.Errorf("launch_options = %v, want %v", out.LaunchOptions, tc.wantLaunch)
			}
			for field, want := range tc.wantProv {
				if got := out.Provenance[field]; got != want {
					t.Errorf("provenance[%s] = %q, want %q", field, got, want)
				}
			}
		})
	}
}

// With nothing set and nothing in extra_args, every option is claude's own
// settings' to decide, and launch_options says so by being absent. So does
// agent_args: the agent starts with no arguments at all.
func TestAgentOptions_LaunchOptionsAbsentWhenNothingIsPassed(t *testing.T) {
	h := newHarness(t)
	if code := h.run("--title", "t", "--no-worktree", "--json"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	for _, key := range []string{`"launch_options"`, `"agent_args"`} {
		if strings.Contains(h.stdout.String(), key) {
			t.Errorf("%s present with no option passed anywhere:\n%s", key, h.stdout)
		}
	}
}

// #219: --json reports the agent's whole argument list, not only the three
// declared flags launch_options reads back. Any other argument in
// [agents.extra_args] reached the agent unreported, so the report could say
// plan mode for a session that some other argument had told to skip every
// permission prompt.
//
// Held to what the fake runner was HANDED, on both launch paths, because
// the claim is that the report says what the launch did. The pinned path
// types the list after the launcher's own words, so it is the tail there.
func TestAgentArgs_ReportTheWholeArgumentList(t *testing.T) {
	const extra = "[agents.extra_args]\nclaude = [\"--verbose\", \"--model\", \"claude-opus-5[1m]\", \"--effort=xhigh\"]\n"
	chosen := []string{"--effort", "high", "--permission-mode", "plan"}
	// extra_args less the effort a flag displaced, then the chosen options
	// in declaration order (agent-options spec §5).
	withChosen := []string{"--verbose", "--model", "claude-opus-5[1m]", "--effort", "high", "--permission-mode", "plan"}
	// With nothing chosen, extra_args as it stands. launch_options still
	// reports the model and the effort here; agent_args is the only key that
	// shows --verbose, which is why this case is not the same as the others.
	asConfigured := []string{"--verbose", "--model", "claude-opus-5[1m]", "--effort=xhigh"}
	agentStart := func(_ *testing.T, r *fakeRunner) []string { return r.startArgs }

	for _, tc := range []struct {
		name   string
		config string
		flags  []string
		dryRun bool
		want   []string
		// launched is what the runner was handed, nil for a dry run.
		launched func(t *testing.T, r *fakeRunner) []string
	}{
		{name: "a dry run", config: extra, flags: chosen, dryRun: true, want: withChosen},
		{name: "herdr agent start", config: extra, flags: chosen, want: withChosen, launched: agentStart},
		{name: "a dry run with no option chosen", config: extra, dryRun: true, want: asConfigured},
		{name: "herdr agent start with no option chosen", config: extra, want: asConfigured, launched: agentStart},
		{
			name:   "a pinned account's launcher",
			config: extra + "[clauth]\ndefault = \"alpha-1\"\n",
			flags:  chosen,
			want:   withChosen,
			launched: func(t *testing.T, r *fakeRunner) []string {
				launcher := []string{"clauth", "start", "alpha-1", "--"}
				if !slices.Equal(r.runArgv[:min(len(launcher), len(r.runArgv))], launcher) {
					t.Fatalf("pane run typed %q, which does not start with the launcher %q", r.runArgv, launcher)
				}
				return r.runArgv[len(launcher):]
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			writeConfig(t, h.env.ConfigDir, tc.config)
			args := append([]string{"--title", "t", "--no-worktree", "--json"}, tc.flags...)
			if tc.dryRun {
				args = append(args, "--dry-run")
			}
			if code := h.run(args...); code != ExitOK {
				t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
			}
			out := decodeReport(t, h.stdout.String())
			if !slices.Equal(out.AgentArgs, tc.want) {
				t.Errorf("agent_args = %q, want %q", out.AgentArgs, tc.want)
			}
			if tc.launched == nil {
				return
			}
			if got := tc.launched(t, h.runner); !slices.Equal(got, out.AgentArgs) {
				t.Errorf("the agent was launched with %q, but agent_args says %q", got, out.AgentArgs)
			}
		})
	}
}
