package config

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ZviBaratz/herdr-draft/internal/agentopts"
)

// Agent-options spec §6.2: [agents.options.<kind>] is the config.toml tier
// of the options chain, validated against the declaration at load.
func TestLoad_AgentOptions(t *testing.T) {
	cfg, err := Load(writeConfigBody(t, `
[agents.extra_args]
claude = ["--verbose"]

[agents.options.claude]
model = "claude-opus-5[1m]"
effort = "xhigh"
permission_mode = "plan"
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := map[string]agentopts.Values{"claude": {"model": "claude-opus-5[1m]", "effort": "xhigh", "permission_mode": "plan"}}
	if !reflect.DeepEqual(cfg.Agents.Options, want) {
		t.Fatalf("Options = %v, want %v", cfg.Agents.Options, want)
	}
	if len(cfg.Agents.OptionWarnings) != 0 {
		t.Fatalf("unexpected warnings %q", cfg.Agents.OptionWarnings)
	}
	// The sub-table must not disturb its sibling.
	if got := cfg.Agents.ExtraArgs["claude"]; !reflect.DeepEqual(got, []string{"--verbose"}) {
		t.Fatalf("ExtraArgs = %q", got)
	}
}

func TestLoad_AgentOptionsAbsentIsNil(t *testing.T) {
	cfg, err := Load(writeConfigBody(t, "[agents]\nfavorites = [\"claude\"]\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Agents.Options != nil || cfg.Agents.OptionWarnings != nil {
		t.Fatalf("Options = %v, warnings = %q, want both nil", cfg.Agents.Options, cfg.Agents.OptionWarnings)
	}
}

// `inherit` is a legal way to write "unset", not a value and not a mistake.
func TestLoad_AgentOptionsInheritIsUnset(t *testing.T) {
	cfg, err := Load(writeConfigBody(t, "[agents.options.claude]\nmodel = \"inherit\"\neffort = \"low\"\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := (agentopts.Values{"effort": "low"}); !reflect.DeepEqual(cfg.Agents.Options["claude"], want) {
		t.Fatalf("Options[claude] = %v, want %v", cfg.Agents.Options["claude"], want)
	}
	if len(cfg.Agents.OptionWarnings) != 0 {
		t.Fatalf("unexpected warnings %q", cfg.Agents.OptionWarnings)
	}
}

// Everything the table can get wrong degrades with a reason and keeps the
// rest of the table: nothing here may stop the popup opening, and nothing
// may vanish without a word (agent-options spec §6.2).
func TestLoad_AgentOptionsDegradeWithAReason(t *testing.T) {
	cfg, err := Load(writeConfigBody(t, `
[agents.options]
gemini = "pro"

[agents.options.claude]
effort = "ultra"
model = 5
permission_mode = "bypassPermissions"
sandbox = "on"

[agents.options.codex]
model = "o3"
`))
	if err != nil {
		t.Fatalf("Load must not fail on a bad option: %v", err)
	}
	if got := cfg.Agents.Options; len(got) != 0 {
		t.Fatalf("Options = %v, want nothing to survive", got)
	}
	want := [][]string{
		{"[agents.options.claude] effort", `"ultra"`, "expected one of low, medium, high, xhigh, max, or inherit"},
		{"[agents.options.claude] model", "expected a string"},
		{"[agents.options.claude] permission_mode", `"bypassPermissions"`, "confirmation dialog"},
		{"[agents.options.claude] sandbox", "claude declares no sandbox option"},
		{"[agents.options.codex]", "codex takes no session options"},
		{"[agents.options] gemini", "expected a table"},
	}
	got := cfg.Agents.OptionWarnings
	if len(got) != len(want) {
		t.Fatalf("warnings = %q, want %d of them", got, len(want))
	}
	for i, parts := range want {
		if !strings.HasPrefix(got[i], "ignoring ") {
			t.Errorf("warning %d %q does not say what happened to the key", i, got[i])
		}
		for _, p := range parts {
			if !strings.Contains(got[i], p) {
				t.Errorf("warning %d %q should contain %q", i, got[i], p)
			}
		}
	}
}

// A bad entry costs only itself.
func TestLoad_AgentOptionsKeepTheGoodKeysBesideABadOne(t *testing.T) {
	cfg, err := Load(writeConfigBody(t, "[agents.options.claude]\nmodel = \"opus\"\neffort = \"ultra\"\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := (agentopts.Values{"model": "opus"}); !reflect.DeepEqual(cfg.Agents.Options["claude"], want) {
		t.Fatalf("Options[claude] = %v, want %v", cfg.Agents.Options["claude"], want)
	}
	if len(cfg.Agents.OptionWarnings) != 1 {
		t.Fatalf("warnings = %q, want exactly the effort one", cfg.Agents.OptionWarnings)
	}
}

// `options` written as a value rather than a table is a mistake like any
// other in it, and is reported (review of the first version: it decoded
// into an empty map and vanished).
func TestLoad_AgentOptionsNotATableIsReported(t *testing.T) {
	for _, body := range []string{"[agents]\noptions = \"opus\"\n", "[agents]\noptions = 5\n", "[agents]\noptions = [\"opus\"]\n"} {
		cfg, err := Load(writeConfigBody(t, body))
		if err != nil {
			t.Fatalf("%q: Load: %v", body, err)
		}
		if len(cfg.Agents.OptionWarnings) != 1 || !strings.Contains(cfg.Agents.OptionWarnings[0], "ignoring [agents] options") {
			t.Errorf("%q: warnings = %q, want one naming [agents] options", body, cfg.Agents.OptionWarnings)
		}
	}
}
