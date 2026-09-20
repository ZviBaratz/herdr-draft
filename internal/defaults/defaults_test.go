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
func TestResolve_Precedence(t *testing.T) {
	cases := []struct {
		name string
		src  Sources

		wantBranchPrefix string
		wantWorktree     bool
		wantPlacement    plan.Placement
		wantAgentKind    string
		wantBaseRef      string
		// wantLinearBranchNameOff is stated as the NEGATIVE so the zero
		// value is the built-in answer (true): only a repo config can turn
		// it off, so all but a couple of these cases expect it on.
		wantLinearBranchNameOff bool
		wantFrom                map[string]Tier
	}{
		{
			name: "everything falls through to the built-in",
			src:  Sources{KnownAgentKinds: knownKinds},

			wantBranchPrefix: "",
			wantWorktree:     false,
			wantPlacement:    plan.PlacementNewSpace,
			wantAgentKind:    "",
			wantFrom: map[string]Tier{
				// config.Config.DefaultWorktree is a plain bool, so a
				// zero-value Config still SUPPLIES a value (false) rather
				// than falling through -- see Resolve's own comment. Its
				// empty BranchPrefix is supplied the same way: nothing is
				// Defaulted, so "" is config.toml asking for no prefix (#220).
				FieldBranchPrefix:     TierUserConfig,
				FieldWorktree:         TierUserConfig,
				FieldPlacement:        TierBuiltin,
				FieldAgentKind:        TierBuiltin,
				FieldBaseRef:          TierBuiltin,
				FieldLinearBranchName: TierBuiltin,
				FieldTrustRepository:  TierBuiltin,
				FieldMarkReady:        TierBuiltin,
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
				FieldTrustRepository:  TierBuiltin,
				FieldMarkReady:        TierBuiltin,
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
				FieldTrustRepository:  TierBuiltin,
				FieldMarkReady:        TierBuiltin,
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
				FieldBranchPrefix:     TierUserConfig,
				FieldWorktree:         TierUserConfig,
				FieldPlacement:        TierGlobalMemory,
				FieldAgentKind:        TierBuiltin,
				FieldBaseRef:          TierBuiltin,
				FieldLinearBranchName: TierBuiltin,
				FieldTrustRepository:  TierBuiltin,
				FieldMarkReady:        TierBuiltin,
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
				FieldBranchPrefix:     TierUserConfig,
				FieldWorktree:         TierUserConfig,
				FieldPlacement:        TierUserConfig,
				FieldAgentKind:        TierBuiltin,
				FieldBaseRef:          TierBuiltin,
				FieldLinearBranchName: TierBuiltin,
				FieldTrustRepository:  TierBuiltin,
				FieldMarkReady:        TierBuiltin,
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
				FieldBranchPrefix:     TierUserConfig,
				FieldWorktree:         TierUserConfig,
				FieldPlacement:        TierBuiltin,
				FieldAgentKind:        TierUserConfig,
				FieldBaseRef:          TierBuiltin,
				FieldLinearBranchName: TierBuiltin,
				FieldTrustRepository:  TierBuiltin,
				FieldMarkReady:        TierBuiltin,
			},
		},
		{
			name: "no kind list means no validation",
			src: Sources{
				Global: config.State{LastKind: "anything-at-all"},
			},

			wantAgentKind: "anything-at-all",
			wantFrom: map[string]Tier{
				FieldBranchPrefix:     TierUserConfig,
				FieldWorktree:         TierUserConfig,
				FieldPlacement:        TierBuiltin,
				FieldAgentKind:        TierGlobalMemory,
				FieldBaseRef:          TierBuiltin,
				FieldLinearBranchName: TierBuiltin,
				FieldTrustRepository:  TierBuiltin,
				FieldMarkReady:        TierBuiltin,
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
				FieldBranchPrefix:     TierUserConfig,
				FieldWorktree:         TierGlobalMemory,
				FieldPlacement:        TierBuiltin,
				FieldAgentKind:        TierBuiltin,
				FieldBaseRef:          TierBuiltin,
				FieldLinearBranchName: TierBuiltin,
				FieldTrustRepository:  TierBuiltin,
				FieldMarkReady:        TierBuiltin,
			},
		},
		{
			// Spec §11's whole allowed surface arriving at once, with
			// every tier below it stacked differently, so nothing here can
			// pass by accident. branch_prefix is the one the repo tier
			// takes from config.toml, which no other tier can.
			name: ".herdr-draft.toml beats last-used.json and config.toml",
			src: Sources{
				Config: config.Config{
					BranchPrefix:     "zvi/",
					DefaultWorktree:  false,
					DefaultPlacement: "tab-here",
					Agents:           config.AgentsConfig{Default: "codex"},
				},
				Global: config.State{
					LastKind:      "gemini",
					LastPlacement: "new-space",
					LastWorktree:  boolp(false),
				},
				Repo: config.RepoConfig{
					BranchPrefix:     "team/",
					DefaultWorktree:  boolp(true),
					DefaultPlacement: "split-here",
					DefaultBase:      "trunk",
					LinearBranchName: boolp(false),
				},
				KnownAgentKinds: knownKinds,
			},

			wantBranchPrefix: "team/",
			wantWorktree:     true,
			wantPlacement:    plan.PlacementSplitHere,
			// A repository does not choose which agent runs on your
			// machine (v2 spec §11's forbidden list), so the agent kind stays
			// last-used.json's.
			wantAgentKind:           "gemini",
			wantBaseRef:             "trunk",
			wantLinearBranchNameOff: true,
			wantFrom: map[string]Tier{
				FieldBranchPrefix:     TierRepoConfig,
				FieldWorktree:         TierRepoConfig,
				FieldPlacement:        TierRepoConfig,
				FieldAgentKind:        TierGlobalMemory,
				FieldBaseRef:          TierRepoConfig,
				FieldLinearBranchName: TierRepoConfig,
				FieldTrustRepository:  TierBuiltin,
				FieldMarkReady:        TierBuiltin,
			},
		},
		{
			// The other side of spec §10's tier 1-vs-2 ordering: what you
			// last did in THIS repository outranks what the repository
			// itself ships, because it is both deliberate and recent while
			// the committed default is what a NEW checkout starts from.
			name: "projects.json beats .herdr-draft.toml",
			src: Sources{
				Repo: config.RepoConfig{
					DefaultWorktree:  boolp(false),
					DefaultPlacement: "tab-here",
					DefaultBase:      "trunk",
				},
				Project: config.ProjectDefaults{
					Worktree:  boolp(true),
					Placement: "split-here",
					Base:      "develop",
				},
				HaveProject:     true,
				KnownAgentKinds: knownKinds,
			},

			wantWorktree:  true,
			wantPlacement: plan.PlacementSplitHere,
			wantBaseRef:   "develop",
			wantFrom: map[string]Tier{
				// branch_prefix and linear_branch_name have no per-project
				// tier at all, so the repo config keeps them.
				FieldBranchPrefix:     TierUserConfig,
				FieldWorktree:         TierProjectMemory,
				FieldPlacement:        TierProjectMemory,
				FieldAgentKind:        TierBuiltin,
				FieldBaseRef:          TierProjectMemory,
				FieldLinearBranchName: TierBuiltin,
				FieldTrustRepository:  TierBuiltin,
				FieldMarkReady:        TierBuiltin,
			},
		},
		{
			// A repo config that names only some keys must not zero the
			// others -- the same partial-entry rule projects.json has, and
			// the reason every field on RepoConfig is a pointer or "".
			name: "a partial .herdr-draft.toml leaves the tiers below alone",
			src: Sources{
				Config:          config.Config{BranchPrefix: "zvi/", DefaultWorktree: true},
				Global:          config.State{LastPlacement: "tab-here", LastKind: "gemini"},
				Repo:            config.RepoConfig{DefaultBase: "trunk"},
				KnownAgentKinds: knownKinds,
			},

			wantBranchPrefix: "zvi/",
			wantWorktree:     true,
			wantPlacement:    plan.PlacementTabHere,
			wantAgentKind:    "gemini",
			wantBaseRef:      "trunk",
			wantFrom: map[string]Tier{
				FieldBranchPrefix:     TierUserConfig,
				FieldWorktree:         TierUserConfig,
				FieldPlacement:        TierGlobalMemory,
				FieldAgentKind:        TierGlobalMemory,
				FieldBaseRef:          TierRepoConfig,
				FieldLinearBranchName: TierBuiltin,
				FieldTrustRepository:  TierBuiltin,
				FieldMarkReady:        TierBuiltin,
			},
		},
		{
			// config.LoadRepoConfig drops a prefix gitx.ValidateBranchPrefix
			// rejects to "", which is how the fallback lands on the USER's
			// own configured value rather than on the built-in -- the
			// difference from config.Load's handling of the same key.
			name: "a rejected repo branch_prefix arrives as nothing and the user's applies",
			src: Sources{
				Config:          config.Config{BranchPrefix: "zvi/"},
				Repo:            config.RepoConfig{BranchPrefix: ""},
				KnownAgentKinds: knownKinds,
			},

			wantBranchPrefix: "zvi/",
			wantFrom: map[string]Tier{
				FieldBranchPrefix:     TierUserConfig,
				FieldWorktree:         TierUserConfig,
				FieldPlacement:        TierBuiltin,
				FieldAgentKind:        TierBuiltin,
				FieldBaseRef:          TierBuiltin,
				FieldLinearBranchName: TierBuiltin,
				FieldTrustRepository:  TierBuiltin,
				FieldMarkReady:        TierBuiltin,
			},
		},
		{
			// A repo config is allowed to say true explicitly, which must
			// stay distinguishable from saying nothing.
			name: "an explicit repo linear_branch_name = true is still the repo's answer",
			src: Sources{
				Repo:            config.RepoConfig{LinearBranchName: boolp(true)},
				KnownAgentKinds: knownKinds,
			},

			wantFrom: map[string]Tier{
				FieldBranchPrefix:     TierUserConfig,
				FieldWorktree:         TierUserConfig,
				FieldPlacement:        TierBuiltin,
				FieldAgentKind:        TierBuiltin,
				FieldBaseRef:          TierBuiltin,
				FieldLinearBranchName: TierRepoConfig,
				FieldTrustRepository:  TierBuiltin,
				FieldMarkReady:        TierBuiltin,
			},
		},
		{
			name: "projects.json beats every tier below it",
			src: Sources{
				Config: config.Config{
					BranchPrefix:     "zvi/",
					DefaultWorktree:  false,
					DefaultPlacement: "tab-here",
					Agents:           config.AgentsConfig{Default: "codex"},
				},
				Global: config.State{
					LastKind:      "gemini",
					LastPlacement: "new-space",
					LastWorktree:  boolp(false),
				},
				Repo: config.RepoConfig{
					DefaultWorktree:  boolp(false),
					DefaultPlacement: "new-space",
					DefaultBase:      "trunk",
				},
				Project: config.ProjectDefaults{
					Kind:      "claude",
					Worktree:  boolp(true),
					Placement: "split-here",
					Base:      "develop",
				},
				HaveProject:     true,
				KnownAgentKinds: knownKinds,
			},

			// branch_prefix has no per-project tier, so it stays the
			// highest tier that HAS one -- config.toml here, since this
			// repo config sets no prefix.
			wantBranchPrefix: "zvi/",
			wantWorktree:     true,
			wantPlacement:    plan.PlacementSplitHere,
			wantAgentKind:    "claude",
			wantBaseRef:      "develop",
			wantFrom: map[string]Tier{
				FieldBranchPrefix:     TierUserConfig,
				FieldWorktree:         TierProjectMemory,
				FieldPlacement:        TierProjectMemory,
				FieldAgentKind:        TierProjectMemory,
				FieldBaseRef:          TierProjectMemory,
				FieldLinearBranchName: TierBuiltin,
				FieldTrustRepository:  TierBuiltin,
				FieldMarkReady:        TierBuiltin,
			},
		},
		{
			name: "an entry with no value for a field leaves the tier below alone",
			src: Sources{
				Config: config.Config{DefaultWorktree: true, Agents: config.AgentsConfig{Default: "codex"}},
				Global: config.State{LastPlacement: "tab-here"},
				// A partial entry -- a hand-written one, or a future
				// herdr-draft's -- must not zero out what it does not name.
				Project:         config.ProjectDefaults{Base: "release"},
				HaveProject:     true,
				KnownAgentKinds: knownKinds,
			},

			wantWorktree:  true,
			wantPlacement: plan.PlacementTabHere,
			wantAgentKind: "codex",
			wantBaseRef:   "release",
			wantFrom: map[string]Tier{
				FieldBranchPrefix:     TierUserConfig,
				FieldWorktree:         TierUserConfig,
				FieldPlacement:        TierGlobalMemory,
				FieldAgentKind:        TierUserConfig,
				FieldBaseRef:          TierProjectMemory,
				FieldLinearBranchName: TierBuiltin,
				FieldTrustRepository:  TierBuiltin,
				FieldMarkReady:        TierBuiltin,
			},
		},
		{
			name: "an entry present but not flagged supplies nothing",
			src: Sources{
				Config: config.Config{Agents: config.AgentsConfig{Default: "codex"}},
				// HaveProject false: the caller found no entry, and the
				// zero ProjectDefaults it passed anyway must be ignored
				// rather than read as a recorded set of choices.
				Project:         config.ProjectDefaults{Kind: "claude", Worktree: boolp(true)},
				KnownAgentKinds: knownKinds,
			},

			wantWorktree:  false,
			wantAgentKind: "codex",
			wantFrom: map[string]Tier{
				FieldBranchPrefix:     TierUserConfig,
				FieldWorktree:         TierUserConfig,
				FieldPlacement:        TierBuiltin,
				FieldAgentKind:        TierUserConfig,
				FieldBaseRef:          TierBuiltin,
				FieldLinearBranchName: TierBuiltin,
				FieldTrustRepository:  TierBuiltin,
				FieldMarkReady:        TierBuiltin,
			},
		},
		{
			name: "a remembered kind this binary no longer ships falls through",
			src: Sources{
				Config:          config.Config{Agents: config.AgentsConfig{Default: "codex"}},
				Project:         config.ProjectDefaults{Kind: "retired-kind"},
				HaveProject:     true,
				KnownAgentKinds: knownKinds,
			},

			wantAgentKind: "codex",
			wantFrom: map[string]Tier{
				FieldBranchPrefix:     TierUserConfig,
				FieldWorktree:         TierUserConfig,
				FieldPlacement:        TierBuiltin,
				FieldAgentKind:        TierUserConfig,
				FieldBaseRef:          TierBuiltin,
				FieldLinearBranchName: TierBuiltin,
				FieldTrustRepository:  TierBuiltin,
				FieldMarkReady:        TierBuiltin,
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
			if got.BaseRef != c.wantBaseRef {
				t.Errorf("BaseRef = %q, want %q", got.BaseRef, c.wantBaseRef)
			}
			if got.LinearBranchName == c.wantLinearBranchNameOff {
				t.Errorf("LinearBranchName = %v, want %v", got.LinearBranchName, !c.wantLinearBranchNameOff)
			}
			assertFrom(t, got.From, c.wantFrom)
		})
	}
}

// TestResolve_LinearBranchNameDefaultsOn pins the built-in: with no repo
// config -- which is every project that has not committed one -- a Linear
// issue's own branchName owns the branch, which is what the form has
// always done. Only .herdr-draft.toml can turn it off.
func TestResolve_LinearBranchNameDefaultsOn(t *testing.T) {
	if !Resolve(Sources{}).LinearBranchName {
		t.Error("LinearBranchName = false with no tier set, want true (today's form behavior)")
	}
	// A repo config that omits the key is not a repo config that turns it
	// off: nil has to stay distinct from a recorded false.
	if !Resolve(Sources{Repo: config.RepoConfig{DefaultBase: "trunk"}}).LinearBranchName {
		t.Error("LinearBranchName = false for a repo config that omits the key, want true")
	}
}

// TestResolve_HeadBasesAreTheHeadRow is #194's rule 2: HEAD and @ are what
// the form's HEAD row already means, and that row's value is "". A tier that
// names either supplies "" -- still attributed to that tier, since it is the
// one that chose HEAD over whatever sits beneath it.
func TestResolve_HeadBasesAreTheHeadRow(t *testing.T) {
	for _, ref := range []string{"HEAD", "@"} {
		t.Run(ref, func(t *testing.T) {
			fromRepo := Resolve(Sources{Repo: config.RepoConfig{DefaultBase: ref}})
			if fromRepo.BaseRef != "" || fromRepo.From[FieldBaseRef] != TierRepoConfig {
				t.Errorf("default_base %q resolved to %q from %v, want \"\" from %v",
					ref, fromRepo.BaseRef, fromRepo.From[FieldBaseRef], TierRepoConfig)
			}
			// Above a repository default, so a remembered HEAD has to beat it
			// rather than be read as "this tier supplies nothing".
			remembered := Resolve(Sources{
				Repo:        config.RepoConfig{DefaultBase: "trunk"},
				Project:     config.ProjectDefaults{Base: ref},
				HaveProject: true,
			})
			if remembered.BaseRef != "" || remembered.From[FieldBaseRef] != TierProjectMemory {
				t.Errorf("remembered %q over default_base trunk resolved to %q from %v, want \"\" from %v",
					ref, remembered.BaseRef, remembered.From[FieldBaseRef], TierProjectMemory)
			}
		})
	}
	// Everything else is a ref git has to answer for, and passes through.
	for _, ref := range []string{"HEAD~1", "HEAD@{1}", "@{u}", "head", "main"} {
		if got := NormalizeBase(ref); got != ref {
			t.Errorf("NormalizeBase(%q) = %q, want it unchanged", ref, got)
		}
	}
}

// TestResolved_WithoutBase is what a base that does not resolve falls back
// to (#194's rule 3): the HEAD row, attributed to the built-in, because the
// tier that named the ref no longer supplies what is used. The original is
// left alone -- the form hands its resolution to a background check, and a
// shared From map written from there would be a data race.
func TestResolved_WithoutBase(t *testing.T) {
	res := Resolve(Sources{Repo: config.RepoConfig{DefaultBase: "gone"}})

	got := res.WithoutBase()
	if got.BaseRef != "" || got.From[FieldBaseRef] != TierBuiltin {
		t.Errorf("WithoutBase = %q from %v, want \"\" from %v", got.BaseRef, got.From[FieldBaseRef], TierBuiltin)
	}
	if res.BaseRef != "gone" || res.From[FieldBaseRef] != TierRepoConfig {
		t.Errorf("the original became %q from %v, want it left at gone from %v",
			res.BaseRef, res.From[FieldBaseRef], TierRepoConfig)
	}
	if got.From[FieldPlacement] != res.From[FieldPlacement] {
		t.Errorf("From[placement] = %v, want every other field's attribution kept (%v)",
			got.From[FieldPlacement], res.From[FieldPlacement])
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

// TestResolve_TrustRepositoryComesOnlyFromConfigToml pins the deliberately
// short tier chain. Every other resolved value walks all five tiers; this
// one stops at config.toml, and each tier it skips is wrong for its own
// reason:
//
//   - .herdr-draft.toml, because a repository asserting its own
//     trustworthiness is the trust boundary inverted (config/repo.go's
//     deny-list refuses it at load, so nothing can even arrive here).
//   - last-used.json and projects.json, because those remember what the
//     user CHOSE IN THE FORM, and no row offers this. "Remembering" it
//     would be inventing a memory of a choice nobody made.
//
// The subtests stack a value into each higher tier's own source anyway --
// as far as those types allow one to be expressed at all -- so that a
// future edit wiring this into another tier fails here rather than
// silently widening who can waive a git safety check.
func TestResolve_TrustRepositoryComesOnlyFromConfigToml(t *testing.T) {
	t.Run("absent everywhere is the built-in false", func(t *testing.T) {
		r := Resolve(Sources{})
		if r.TrustRepository {
			t.Error("TrustRepository = true with no tier set, want false")
		}
		if got := r.From[FieldTrustRepository]; got != TierBuiltin {
			t.Errorf("From[trust_repository] = %v, want %v", got, TierBuiltin)
		}
	})

	t.Run("config.toml supplies it and is attributed", func(t *testing.T) {
		r := Resolve(Sources{
			Config: config.Config{Worktree: config.WorktreeConfig{TrustRepository: boolp(true)}},
		})
		if !r.TrustRepository {
			t.Error("TrustRepository = false, want true from config.toml")
		}
		if got := r.From[FieldTrustRepository]; got != TierUserConfig {
			t.Errorf("From[trust_repository] = %v, want %v", got, TierUserConfig)
		}
	})

	t.Run("an explicit config false stays false and attributed", func(t *testing.T) {
		r := Resolve(Sources{
			Config: config.Config{Worktree: config.WorktreeConfig{TrustRepository: boolp(false)}},
		})
		if r.TrustRepository {
			t.Error("TrustRepository = true, want false")
		}
		if got := r.From[FieldTrustRepository]; got != TierUserConfig {
			t.Errorf("From[trust_repository] = %v, want %v", got, TierUserConfig)
		}
	})

	t.Run("no higher tier can turn it on", func(t *testing.T) {
		// Everything the higher tiers CAN carry, all set to their most
		// permissive value at once. None of them has a trust_repository of
		// its own -- which is the point -- so the assertion is that a
		// fully-populated stack still leaves the config.toml answer alone.
		r := Resolve(Sources{
			Config: config.Config{
				DefaultWorktree: true,
				Worktree:        config.WorktreeConfig{TrustRepository: boolp(false)},
			},
			Global:      config.State{LastWorktree: boolp(true), LastPlacement: "tab-here"},
			Repo:        config.RepoConfig{DefaultWorktree: boolp(true), DefaultPlacement: "new-space"},
			Project:     config.ProjectDefaults{Worktree: boolp(true), Placement: "tab-here"},
			HaveProject: true,
		})
		if r.TrustRepository {
			t.Error("TrustRepository = true: a tier above config.toml supplied it, which none may")
		}
		if got := r.From[FieldTrustRepository]; got != TierUserConfig {
			t.Errorf("From[trust_repository] = %v, want %v (config.toml's own answer must stand)", got, TierUserConfig)
		}
	})
}

// TestResolve_MarkReadyComesOnlyFromConfigToml is reap spec §6.1's chain:
// built-in false, then config.toml, and nothing above. Unlike
// trust_repository there IS a form control for it; memory still may not
// supply it, because one use would silently make every later session reap.
func TestResolve_MarkReadyComesOnlyFromConfigToml(t *testing.T) {
	t.Run("absent everywhere is the built-in false", func(t *testing.T) {
		r := Resolve(Sources{})
		if r.MarkReady {
			t.Error("MarkReady = true with no tier set, want false")
		}
		if got := r.From[FieldMarkReady]; got != TierBuiltin {
			t.Errorf("From[mark_ready] = %v, want %v", got, TierBuiltin)
		}
	})

	t.Run("config.toml supplies it and is attributed", func(t *testing.T) {
		r := Resolve(Sources{Config: config.Config{Reaper: config.ReaperConfig{MarkReady: boolp(true)}}})
		if !r.MarkReady {
			t.Error("MarkReady = false, want true from config.toml")
		}
		if got := r.From[FieldMarkReady]; got != TierUserConfig {
			t.Errorf("From[mark_ready] = %v, want %v", got, TierUserConfig)
		}
	})

	t.Run("a fully populated stack leaves config.toml's answer alone", func(t *testing.T) {
		r := Resolve(Sources{
			Config:      config.Config{DefaultWorktree: true, Reaper: config.ReaperConfig{MarkReady: boolp(false)}},
			Global:      config.State{LastWorktree: boolp(true), LastPlacement: "tab-here"},
			Repo:        config.RepoConfig{DefaultWorktree: boolp(true), DefaultPlacement: "new-space"},
			Project:     config.ProjectDefaults{Worktree: boolp(true), Placement: "tab-here"},
			HaveProject: true,
		})
		if r.MarkReady {
			t.Error("MarkReady = true: a tier above config.toml supplied it, which none may")
		}
		if got := r.From[FieldMarkReady]; got != TierUserConfig {
			t.Errorf("From[mark_ready] = %v, want %v", got, TierUserConfig)
		}
	})
}

// TestRememberedPlacement_AWorktreeSubmitKeepsWhatWasRecorded pins placement
// spec §14.2's memory rule. A worktree session has no placement -- nobody
// chose one -- so its submit must not overwrite the placement a session
// without a worktree recorded, nor turn "nothing recorded" into
// "new-space", which would then outrank config.toml's default_placement.
func TestRememberedPlacement_AWorktreeSubmitKeepsWhatWasRecorded(t *testing.T) {
	worktree := plan.Input{UseWorktree: true, Placement: plan.PlacementNewSpace}
	if got := RememberedPlacement(worktree, "split-here"); got != "split-here" {
		t.Errorf("worktree submit over split-here records %q, want split-here kept", got)
	}
	if got := RememberedPlacement(worktree, ""); got != "" {
		t.Errorf("worktree submit over nothing records %q, want nothing", got)
	}
	noWorktree := plan.Input{Placement: plan.PlacementTabIn}
	if got := RememberedPlacement(noWorktree, "split-here"); got != "tab-in" {
		t.Errorf("submit without a worktree records %q, want its own tab-in", got)
	}
}

// #220: a value config.Load filled in from its own defaults is the
// built-in's, not config.toml's -- the value applies, and the attribution
// says where it really came from. The same Config with nothing defaulted
// (every Config built in code, as TestResolve_Precedence's are) keeps the
// config.toml attribution.
func TestResolve_CreditsLoadsOwnDefaultsToTheBuiltin(t *testing.T) {
	cfg := config.Config{BranchPrefix: "me/", DefaultWorktree: true, DefaultPlacement: "new-space"}
	for _, tc := range []struct {
		name      string
		defaulted config.DefaultedKeys
		want      Tier
	}{
		{"Load filled all three in", config.DefaultedKeys{BranchPrefix: true, DefaultWorktree: true, DefaultPlacement: true}, TierBuiltin},
		{"the file set all three", config.DefaultedKeys{}, TierUserConfig},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := cfg
			c.Defaulted = tc.defaulted
			res := Resolve(Sources{Config: c, KnownAgentKinds: knownKinds})
			if res.BranchPrefix != "me/" || !res.UseWorktree || res.Placement != plan.PlacementNewSpace {
				t.Errorf("the values changed: prefix %q, worktree %v, placement %v", res.BranchPrefix, res.UseWorktree, res.Placement)
			}
			for _, f := range []string{FieldBranchPrefix, FieldWorktree, FieldPlacement} {
				if got := res.From[f]; got != tc.want {
					t.Errorf("From[%s] = %v, want %v", f, got, tc.want)
				}
			}
		})
	}
}
