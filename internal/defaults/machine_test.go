package defaults

import (
	"testing"

	"github.com/ZviBaratz/herdr-draft/internal/config"
)

func machineCfg(def string, rules ...config.MachineRule) config.Config {
	return config.Config{Machine: config.MachineConfig{Configured: true, Default: def, Rules: rules}}
}

// TestMachineWithNoTableIsLocal: the builtin is this machine, credited to
// the builtin tier, and no rule is named.
func TestMachineWithNoTableIsLocal(t *testing.T) {
	sel, from, rule := Machine(config.Config{}, "/home/u/quantivly/hub")
	if sel != config.LocalMachine || from != TierBuiltin || rule != "" {
		t.Errorf("Machine = %q, %v, %q; want local, builtin, no rule", sel, from, rule)
	}
}

// TestMachineDefaultAppliesWhereNoRuleMatches: `default` is config.toml's,
// and so is the credit.
func TestMachineDefaultAppliesWhereNoRuleMatches(t *testing.T) {
	cfg := machineCfg("dev", config.MachineRule{Dir: "/home/u/quantivly", Selector: "local"})
	sel, from, rule := Machine(cfg, "/home/u/Projects/toy")
	if sel != "dev" || from != TierUserConfig || rule != "" {
		t.Errorf("Machine = %q, %v, %q; want dev, config.toml, no rule", sel, from, rule)
	}
	// An empty default is not a value: the builtin stands, and says so.
	sel, from, _ = Machine(machineCfg(""), "/home/u/Projects/toy")
	if sel != config.LocalMachine || from != TierBuiltin {
		t.Errorf("an empty default: %q, %v; want local, builtin", sel, from)
	}
}

// TestMachineLongestRuleWinsByWholeComponents is machine spec §5.2: a rule
// matches its own directory and anything under it, compared component by
// component, and the most specific rule wins.
func TestMachineLongestRuleWinsByWholeComponents(t *testing.T) {
	cfg := machineCfg("",
		config.MachineRule{Dir: "/home/u/quantivly", Selector: "dev"},
		config.MachineRule{Dir: "/home/u/quantivly/browser-repo", Selector: "local"},
	)
	tests := []struct {
		key, want, rule string
		from            Tier
	}{
		{"/home/u/quantivly", "dev", "/home/u/quantivly", TierUserConfig},
		{"/home/u/quantivly/hub", "dev", "/home/u/quantivly", TierUserConfig},
		{"/home/u/quantivly/browser-repo", "local", "/home/u/quantivly/browser-repo", TierUserConfig},
		{"/home/u/quantivly/browser-repo/sub", "local", "/home/u/quantivly/browser-repo", TierUserConfig},
		// A sibling whose name merely starts the same is not under it.
		{"/home/u/quantivly-old", config.LocalMachine, "", TierBuiltin},
		{"/home/u", config.LocalMachine, "", TierBuiltin},
		{"", config.LocalMachine, "", TierBuiltin},
	}
	for _, tt := range tests {
		sel, from, rule := Machine(cfg, tt.key)
		if sel != tt.want || from != tt.from || rule != tt.rule {
			t.Errorf("Machine(%q) = %q, %v, %q; want %q, %v, %q", tt.key, sel, from, rule, tt.want, tt.from, tt.rule)
		}
	}
}

// TestResolveNeverSuppliesAMachine is decision 2's other half: every tier
// Resolve reads is full here, and none of them carries a machine, because
// none of them can -- the machine resolves through Machine alone, from
// config.toml alone.
func TestResolveNeverSuppliesAMachine(t *testing.T) {
	ws := true
	r := Resolve(Sources{
		Config:      machineCfg("dev", config.MachineRule{Dir: "/r", Selector: "dev"}),
		Global:      config.State{LastKind: "claude", LastPlacement: "tab-here", LastWorktree: &ws},
		Repo:        config.RepoConfig{DefaultPlacement: "split-here"},
		Project:     config.ProjectDefaults{Kind: "claude", Placement: "tab-here", Worktree: &ws},
		HaveProject: true,
	})
	if _, ok := r.From[FieldMachine]; ok {
		t.Errorf("Resolve attributed a machine: %v", r.From)
	}
}
