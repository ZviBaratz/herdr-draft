package defaults

import (
	"testing"

	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/plan"
)

func boolp(b bool) *bool { return &b }

// knownKinds is the agent-kind list every case below validates against --
// enough of herdr's real list to exercise Sources.KnownAgentKinds' own
// skip-an-unknown-kind rule without pinning the full 23.
var knownKinds = []string{"claude", "codex", "gemini"}

// TestResolve_Precedence is spec §10's precedence chain, one case per
// (field, winning tier) pair: each case stacks every tier BELOW the winner
// with a different value, so a case can only pass if the tier that is
// supposed to win actually does.
//
// The project-memory tier is absent here by construction -- this is the
// extraction's own table, carrying only the tiers that existed when the
// chain was inline in app.New. The per-project cases live in
// TestResolve_ProjectMemoryTier.
func TestResolve_Precedence(t *testing.T) {
	cases := []struct {
		name string
		src  Sources

		wantBranchPrefix string
		wantWorktree     bool
		wantPlacement    plan.Placement
		wantAgentKind    string
		wantFrom         map[string]Tier
	}{
		{
			name: "everything falls through to the built-in",
			src:  Sources{KnownAgentKinds: knownKinds},

			wantBranchPrefix: "",
			wantWorktree:     false,
			wantPlacement:    plan.PlacementNewSpace,
			wantAgentKind:    "",
			wantFrom: map[string]Tier{
				FieldBranchPrefix: TierBuiltin,
				// config.Config.DefaultWorktree is a plain bool, so a
				// zero-value Config still SUPPLIES a value (false) rather
				// than falling through -- see Resolve's own comment.
				FieldWorktree:         TierUserConfig,
				FieldPlacement:        TierBuiltin,
				FieldAgentKind:        TierBuiltin,
				FieldBaseRef:          TierBuiltin,
				FieldLinearBranchName: TierBuiltin,
			},
		},
		{
			name: "config.toml supplies every field it can",
			src: Sources{
				Config: config.Config{
					BranchPrefix:     "zvi/",
					DefaultWorktree:  true,
					DefaultPlacement: "tab-here",
					Agents:           config.AgentsConfig{Default: "codex"},
				},
				KnownAgentKinds: knownKinds,
			},

			wantBranchPrefix: "zvi/",
			wantWorktree:     true,
			wantPlacement:    plan.PlacementTabHere,
			wantAgentKind:    "codex",
			wantFrom: map[string]Tier{
				FieldBranchPrefix:     TierUserConfig,
				FieldWorktree:         TierUserConfig,
				FieldPlacement:        TierUserConfig,
				FieldAgentKind:        TierUserConfig,
				FieldBaseRef:          TierBuiltin,
				FieldLinearBranchName: TierBuiltin,
			},
		},
		{
			name: "last-used.json beats config.toml",
			src: Sources{
				Config: config.Config{
					BranchPrefix:     "zvi/",
					DefaultWorktree:  true,
					DefaultPlacement: "tab-here",
					Agents:           config.AgentsConfig{Default: "codex"},
				},
				Global: config.State{
					LastKind:      "gemini",
					LastPlacement: "split-here",
					LastWorktree:  boolp(false),
				},
				KnownAgentKinds: knownKinds,
			},

			// branch_prefix has no last-used tier, so it stays config's.
			wantBranchPrefix: "zvi/",
			wantWorktree:     false,
			wantPlacement:    plan.PlacementSplitHere,
			wantAgentKind:    "gemini",
			wantFrom: map[string]Tier{
				FieldBranchPrefix:     TierUserConfig,
				FieldWorktree:         TierGlobalMemory,
				FieldPlacement:        TierGlobalMemory,
				FieldAgentKind:        TierGlobalMemory,
				FieldBaseRef:          TierBuiltin,
				FieldLinearBranchName: TierBuiltin,
			},
		},
		{
			name: "an explicit last-used new-space beats a configured tab-here",
			src: Sources{
				Config:          config.Config{DefaultPlacement: "tab-here"},
				Global:          config.State{LastPlacement: "new-space"},
				KnownAgentKinds: knownKinds,
			},

			wantPlacement: plan.PlacementNewSpace,
			wantFrom: map[string]Tier{
				FieldBranchPrefix:     TierBuiltin,
				FieldWorktree:         TierUserConfig,
				FieldPlacement:        TierGlobalMemory,
				FieldAgentKind:        TierBuiltin,
				FieldBaseRef:          TierBuiltin,
				FieldLinearBranchName: TierBuiltin,
			},
		},
		{
			name: "an unparseable placement supplies nothing",
			src: Sources{
				Config:          config.Config{DefaultPlacement: "tab-here"},
				Global:          config.State{LastPlacement: "tab-hear"},
				KnownAgentKinds: knownKinds,
			},

			wantPlacement: plan.PlacementTabHere,
			wantFrom: map[string]Tier{
				FieldBranchPrefix:     TierBuiltin,
				FieldWorktree:         TierUserConfig,
				FieldPlacement:        TierUserConfig,
				FieldAgentKind:        TierBuiltin,
				FieldBaseRef:          TierBuiltin,
				FieldLinearBranchName: TierBuiltin,
			},
		},
		{
			name: "an unknown agent kind falls through to the tier below",
			src: Sources{
				Config: config.Config{Agents: config.AgentsConfig{Default: "codex"}},
				// A kind this binary no longer ships: form.AgentField.SetKind
				// would silently ignore it, so the resolver must too --
				// otherwise the configured default is shadowed by a value
				// that never applies.
				Global:          config.State{LastKind: "retired-kind"},
				KnownAgentKinds: knownKinds,
			},

			wantAgentKind: "codex",
			wantFrom: map[string]Tier{
				FieldBranchPrefix:     TierBuiltin,
				FieldWorktree:         TierUserConfig,
				FieldPlacement:        TierBuiltin,
				FieldAgentKind:        TierUserConfig,
				FieldBaseRef:          TierBuiltin,
				FieldLinearBranchName: TierBuiltin,
			},
		},
		{
			name: "no kind list means no validation",
			src: Sources{
				Global: config.State{LastKind: "anything-at-all"},
			},

			wantAgentKind: "anything-at-all",
			wantFrom: map[string]Tier{
				FieldBranchPrefix:     TierBuiltin,
				FieldWorktree:         TierUserConfig,
				FieldPlacement:        TierBuiltin,
				FieldAgentKind:        TierGlobalMemory,
				FieldBaseRef:          TierBuiltin,
				FieldLinearBranchName: TierBuiltin,
			},
		},
		{
			name: "a recorded false worktree beats a configured true",
			src: Sources{
				Config: config.Config{DefaultWorktree: true},
				Global: config.State{LastWorktree: boolp(false)},
			},

			wantWorktree: false,
			wantFrom: map[string]Tier{
				FieldBranchPrefix:     TierBuiltin,
				FieldWorktree:         TierGlobalMemory,
				FieldPlacement:        TierBuiltin,
				FieldAgentKind:        TierBuiltin,
				FieldBaseRef:          TierBuiltin,
				FieldLinearBranchName: TierBuiltin,
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Resolve(c.src)
			if got.BranchPrefix != c.wantBranchPrefix {
				t.Errorf("BranchPrefix = %q, want %q", got.BranchPrefix, c.wantBranchPrefix)
			}
			if got.UseWorktree != c.wantWorktree {
				t.Errorf("UseWorktree = %v, want %v", got.UseWorktree, c.wantWorktree)
			}
			if got.Placement != c.wantPlacement {
				t.Errorf("Placement = %v, want %v", got.Placement, c.wantPlacement)
			}
			if got.AgentKind != c.wantAgentKind {
				t.Errorf("AgentKind = %q, want %q", got.AgentKind, c.wantAgentKind)
			}
			assertFrom(t, got.From, c.wantFrom)
		})
	}
}

// TestResolve_LinearBranchNameDefaultsOn pins the one field no tier can
// currently move: until .herdr-draft.toml has a producer, a Linear issue's
// own branchName always owns the branch, which is what the form does today.
func TestResolve_LinearBranchNameDefaultsOn(t *testing.T) {
	if !Resolve(Sources{}).LinearBranchName {
		t.Error("LinearBranchName = false with no tier set, want true (today's form behavior)")
	}
}

// TestTierString pins the user-facing names, since they are what a
// provenance line ("from .herdr-draft.toml") is built out of.
func TestTierString(t *testing.T) {
	cases := map[Tier]string{
		TierBuiltin:       "built-in",
		TierUserConfig:    "config.toml",
		TierGlobalMemory:  "last-used.json",
		TierRepoConfig:    ".herdr-draft.toml",
		TierProjectMemory: "projects.json",
	}
	for tier, want := range cases {
		if got := tier.String(); got != want {
			t.Errorf("Tier(%d).String() = %q, want %q", int(tier), got, want)
		}
	}
}

// TestTierOrderIsPrecedence pins the ordering the whole package depends on:
// Resolve applies tiers in ascending constant order and lets the last
// writer win, so the constants ARE spec §10's precedence list.
func TestTierOrderIsPrecedence(t *testing.T) {
	ordered := []Tier{TierBuiltin, TierUserConfig, TierGlobalMemory, TierRepoConfig, TierProjectMemory}
	for i := 1; i < len(ordered); i++ {
		if ordered[i] <= ordered[i-1] {
			t.Fatalf("tier %v does not outrank %v", ordered[i], ordered[i-1])
		}
	}
}

// TestPlacementRoundTrip pins ParsePlacement/PlacementValue as inverses:
// last-used.json and projects.json are written with one and read with the
// other, so a disagreement would silently drop a remembered placement.
func TestPlacementRoundTrip(t *testing.T) {
	for _, p := range []plan.Placement{plan.PlacementNewSpace, plan.PlacementTabHere, plan.PlacementSplitHere} {
		s := PlacementValue(p)
		got, ok := ParsePlacement(s)
		if !ok || got != p {
			t.Errorf("ParsePlacement(PlacementValue(%v)) = (%v, %v), want (%v, true)", p, got, ok, p)
		}
	}
	for _, s := range []string{"", "tab-hear", "New space"} {
		if _, ok := ParsePlacement(s); ok {
			t.Errorf("ParsePlacement(%q) reported ok, want false", s)
		}
	}
}

func assertFrom(t *testing.T, got, want map[string]Tier) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("From has %d entries (%v), want %d (%v)", len(got), got, len(want), want)
	}
	for field, wantTier := range want {
		gotTier, ok := got[field]
		if !ok {
			t.Errorf("From[%q] missing; every field must be attributed", field)
			continue
		}
		if gotTier != wantTier {
			t.Errorf("From[%q] = %v, want %v", field, gotTier, wantTier)
		}
	}
}
