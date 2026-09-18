package defaults

import (
	"reflect"
	"testing"

	"github.com/ZviBaratz/herdr-draft/internal/agentopts"
	"github.com/ZviBaratz/herdr-draft/internal/config"
)

// Agent-options spec §6.1: built-in inherit, then config.toml, and nothing
// above. Every option the kind declares is attributed, the way Resolve
// attributes every field, so `create --json` can say "built-in" for an
// option nobody set rather than saying nothing.
func TestAgentOptions_ConfigOverBuiltin(t *testing.T) {
	cfg := config.Config{Agents: config.AgentsConfig{Options: map[string]agentopts.Values{
		"claude": {"effort": "xhigh"},
	}}}
	vals, from := AgentOptions(cfg, "claude")
	if want := (agentopts.Values{"effort": "xhigh"}); !reflect.DeepEqual(vals, want) {
		t.Fatalf("values = %v, want %v", vals, want)
	}
	want := map[string]Tier{
		"option.model":           TierBuiltin,
		"option.effort":          TierUserConfig,
		"option.permission_mode": TierBuiltin,
	}
	if !reflect.DeepEqual(from, want) {
		t.Fatalf("from = %v, want %v", from, want)
	}
}

// Nothing configured is nil, not an empty map: plan.Input carries it, and
// equivalence_test.go compares the two paths with reflect.DeepEqual.
func TestAgentOptions_NothingConfiguredIsNil(t *testing.T) {
	vals, _ := AgentOptions(config.Config{}, "claude")
	if vals != nil {
		t.Fatalf("values = %#v, want nil", vals)
	}
}

// Another kind's table never leaks across, and a kind that declares
// nothing has nothing to attribute.
func TestAgentOptions_AnotherKind(t *testing.T) {
	cfg := config.Config{Agents: config.AgentsConfig{Options: map[string]agentopts.Values{
		"claude": {"model": "opus"},
	}}}
	vals, from := AgentOptions(cfg, "codex")
	if vals != nil || len(from) != 0 {
		t.Fatalf("codex: values = %v, from = %v, want neither", vals, from)
	}
}

// The answer is the caller's to keep: editing it must not reach back into
// the loaded config, which every later resolution reads.
func TestAgentOptions_ReturnsACopy(t *testing.T) {
	cfg := config.Config{Agents: config.AgentsConfig{Options: map[string]agentopts.Values{
		"claude": {"model": "opus"},
	}}}
	vals, _ := AgentOptions(cfg, "claude")
	vals["model"] = "haiku"
	if cfg.Agents.Options["claude"]["model"] != "opus" {
		t.Fatalf("AgentOptions handed out the config's own map")
	}
}
