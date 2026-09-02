// Package defaults resolves herdr-draft's layered creation defaults
// (spec §10). It is pure: every tier is passed in already loaded, so the
// form (internal/app) and the headless command resolve identically and
// neither can drift from the other. This is the ONLY place the precedence
// chain exists.
//
// Before this package the chain lived inline in app.New -- three separate
// two-call ladders (placement, agent kind, worktree toggle), each
// expressing "config.toml, then last-used.json" in its own idiom, with no
// way for anything but the form to reach them. Extracting it is spec §10's
// first piece of work, and its acceptance criterion was that
// internal/app's four `assembled-*` golden frames stay byte-identical.
package defaults

import (
	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/plan"
)

// Tier names where a resolved value came from, so callers can attribute it
// (the focused row's panel renders "from .herdr-draft.toml"; `create
// --json` prints its provenance). The constants are ordered by precedence:
// a LATER tier overrides an earlier one, which is spec §10's "precedence,
// highest first" list read bottom-up.
type Tier int

const (
	// TierBuiltin is this package's own fallback -- the value used when no
	// configured or remembered tier supplies one.
	TierBuiltin Tier = iota
	// TierUserConfig is $HERDR_PLUGIN_CONFIG_DIR/config.toml.
	TierUserConfig
	// TierGlobalMemory is last-used.json: the user's last choice ANYWHERE.
	TierGlobalMemory
	// TierRepoConfig is <repo root>/.herdr-draft.toml, the repository's
	// committed default (config.LoadRepoConfig). It outranks
	// TierGlobalMemory because a team's committed default should beat
	// whatever the user last happened to do in some OTHER repository.
	TierRepoConfig
	// TierProjectMemory is projects.json[key]: the user's last choice in
	// THIS project. Highest, because it is both deliberate and recent,
	// where the repo default is what a NEW checkout should start from.
	TierProjectMemory
)

// String names the tier in the vocabulary the panel and `create --json`
// show the user: the file it came from, not the Go constant.
func (t Tier) String() string {
	switch t {
	case TierUserConfig:
		return "config.toml"
	case TierGlobalMemory:
		return "last-used.json"
	case TierRepoConfig:
		return ".herdr-draft.toml"
	case TierProjectMemory:
		return "projects.json"
	default:
		return "built-in"
	}
}

// Field* are Resolved.From's map keys -- one per resolved value, named in
// the same vocabulary the config files use, so a provenance line reads the
// same wherever it is rendered.
const (
	FieldBranchPrefix     = "branch_prefix"
	FieldWorktree         = "worktree"
	FieldPlacement        = "placement"
	FieldAgentKind        = "agent_kind"
	FieldBaseRef          = "base"
	FieldLinearBranchName = "linear_branch_name"
)

// Sources is every tier Resolve consults, each already loaded by the
// caller -- this package performs no I/O of its own.
type Sources struct {
	// Config is the user's own config.toml (TierUserConfig), already
	// through config.Load's own defaults.
	Config config.Config
	// Global is last-used.json (TierGlobalMemory).
	Global config.State
	// Repo is the selected project's committed .herdr-draft.toml
	// (TierRepoConfig), already through config.LoadRepoConfig's own
	// allow-list -- spec §11's trust model is enforced THERE, not here.
	// Its zero value means "no repo config", which is what a non-repo
	// project, an absent file and a malformed one all look like.
	//
	// It needs no HaveRepo companion the way Project needs HaveProject:
	// every field on it is already a nil pointer or "" when unset, since
	// nothing writes this file back and so no entry can exist with
	// meaningfully-zero contents.
	Repo config.RepoConfig
	// Project is this project's projects.json entry (TierProjectMemory),
	// meaningful only when HaveProject is true.
	Project config.ProjectDefaults
	// HaveProject distinguishes "no entry for this project" from an entry
	// whose fields happen to be zero -- a ProjectDefaults zero value alone
	// cannot express the difference, and reading one as a recorded choice
	// would let an empty entry override real configuration.
	HaveProject bool

	// KnownAgentKinds, when non-empty, is the list of agent kinds the form
	// can actually select. A tier naming a kind outside it supplies
	// nothing, and the next tier down applies instead.
	//
	// This mirrors form.AgentField.SetKind, which silently no-ops for an
	// unknown kind: without the same rule here, a last-used.json naming a
	// kind this binary no longer ships would beat the user's configured
	// `[agents] default` and then fail to apply, leaving the form on
	// favorites[0] rather than on the configured default. The app layer's
	// pre-extraction two-call ladder got this right by accident (each
	// SetKind no-oped independently); a single resolved value has to get it
	// right on purpose, and the headless command needs the same answer.
	//
	// Empty means "do not validate", for a caller that has no kind list.
	KnownAgentKinds []string
}

// Resolved is one resolution of every layered default, plus where each
// value came from.
type Resolved struct {
	// BranchPrefix is prepended to a title-derived branch name
	// (gitx.BranchSlug). "" means no prefix.
	BranchPrefix string
	// UseWorktree is the worktree toggle's default position. The app layer
	// can only apply it once the project directory is known to be a git
	// repository (form.WorktreeField.SetOn's own precondition).
	UseWorktree bool
	// Placement is the default placement for a non-worktree creation.
	Placement plan.Placement
	// AgentKind is the default agent kind. "" means "no tier supplied one",
	// which leaves the form on its own first favorite.
	AgentKind string
	// BaseRef is the default base ref for a worktree branch. "" means HEAD
	// (form.WorktreeField.Base()'s own sentinel).
	//
	// Resolved, but not APPLIED by the form: WorktreeField exposes no
	// setter for its base picker's selection (only SetBaseItems, for the
	// candidate pool), and internal/form is being rewritten under a
	// separate issue, so adding one here would collide. The headless
	// `create` command can consume this immediately; the form picks it up
	// when the setter lands.
	BaseRef string
	// LinearBranchName reports whether a chosen Linear issue's own
	// branchName owns the branch (spec §11's repo-config key of the same
	// name). True unless a repo config says otherwise, and false means the
	// branch is derived from the TITLE with BranchPrefix, exactly as it is
	// in manual mode -- the app layer's reading of a key the spec names
	// without defining its alternative, so that a repository whose branch
	// naming is its own can keep it while still seeding title and prompt
	// from Linear.
	LinearBranchName bool

	// From maps each Field* key to the tier that supplied its value.
	// Always fully populated: a value no tier supplied is attributed to
	// TierBuiltin.
	From map[string]Tier
}

// Resolve applies every tier in Sources in precedence order, lowest first,
// so the last writer for each field wins -- see Tier's own doc comment.
// Every field is attributed, including the ones that fell through to the
// built-in.
func Resolve(s Sources) Resolved {
	r := Resolved{
		// Built-ins. There is deliberately no built-in branch prefix:
		// config.Load already substitutes "$USER/" for an absent one, so a
		// "" arriving here means the caller genuinely has no prefix (as
		// internal/app's own tests, which build config.Config directly, do)
		// and inventing one would change every branch they derive.
		Placement:        plan.PlacementNewSpace,
		LinearBranchName: true,
		From: map[string]Tier{
			FieldBranchPrefix:     TierBuiltin,
			FieldWorktree:         TierBuiltin,
			FieldPlacement:        TierBuiltin,
			FieldAgentKind:        TierBuiltin,
			FieldBaseRef:          TierBuiltin,
			FieldLinearBranchName: TierBuiltin,
		},
	}

	// --- TierUserConfig: the user's own config.toml ----------------------
	r.setString(FieldBranchPrefix, &r.BranchPrefix, s.Config.BranchPrefix, TierUserConfig)
	// DefaultWorktree is a plain bool, so config.toml always supplies one:
	// config.Load's defaults() fills in `true` for a file that omits the
	// key, and the zero value is the answer for a Config built in code.
	r.setBool(FieldWorktree, &r.UseWorktree, &s.Config.DefaultWorktree, TierUserConfig)
	r.setPlacement(&r.Placement, s.Config.DefaultPlacement, TierUserConfig)
	r.setAgentKind(&r.AgentKind, s.Config.Agents.Default, TierUserConfig, s.KnownAgentKinds)

	// --- TierGlobalMemory: last-used.json --------------------------------
	r.setBool(FieldWorktree, &r.UseWorktree, s.Global.LastWorktree, TierGlobalMemory)
	r.setPlacement(&r.Placement, s.Global.LastPlacement, TierGlobalMemory)
	r.setAgentKind(&r.AgentKind, s.Global.LastKind, TierGlobalMemory, s.KnownAgentKinds)

	// --- TierRepoConfig: .herdr-draft.toml -------------------------------
	// The repository's committed default (spec §11). It sits here, above
	// last-used.json and below projects.json, because a team's committed
	// default should beat whatever the user last did in some OTHER
	// repository and lose to what they last did in THIS one.
	//
	// Every value arriving here has already been through
	// config.LoadRepoConfig's allow-list, so this block needs no trust
	// rules of its own -- and deliberately has none: a second, partial copy
	// of the trust model in a package that performs no I/O would be the
	// one that goes stale. BranchPrefix in particular is already validated
	// there (a rejected one arrives as "", which setString reads as "this
	// tier supplies nothing", landing the fallback on the user's own
	// configured prefix one tier down).
	r.setString(FieldBranchPrefix, &r.BranchPrefix, s.Repo.BranchPrefix, TierRepoConfig)
	r.setBool(FieldWorktree, &r.UseWorktree, s.Repo.DefaultWorktree, TierRepoConfig)
	r.setPlacement(&r.Placement, s.Repo.DefaultPlacement, TierRepoConfig)
	r.setString(FieldBaseRef, &r.BaseRef, s.Repo.DefaultBase, TierRepoConfig)
	r.setBool(FieldLinearBranchName, &r.LinearBranchName, s.Repo.LinearBranchName, TierRepoConfig)

	// --- TierProjectMemory: projects.json[key] ---------------------------
	if s.HaveProject {
		r.setBool(FieldWorktree, &r.UseWorktree, s.Project.Worktree, TierProjectMemory)
		r.setPlacement(&r.Placement, s.Project.Placement, TierProjectMemory)
		r.setAgentKind(&r.AgentKind, s.Project.Kind, TierProjectMemory, s.KnownAgentKinds)
		r.setString(FieldBaseRef, &r.BaseRef, s.Project.Base, TierProjectMemory)
	}

	return r
}

// setString applies a string tier value, treating "" as "this tier does
// not supply one" -- both string fields here read the empty string as
// unset (no prefix; HEAD).
func (r *Resolved) setString(field string, dst *string, v string, t Tier) {
	if v == "" {
		return
	}
	*dst = v
	r.From[field] = t
}

// setBool applies a *bool tier value: nil is "this tier does not supply
// one", which is exactly what distinguishes an unrecorded worktree toggle
// from a recorded false.
func (r *Resolved) setBool(field string, dst *bool, v *bool, t Tier) {
	if v == nil {
		return
	}
	*dst = *v
	r.From[field] = t
}

// setPlacement applies a placement tier value written in the config
// vocabulary. An empty or unrecognized string supplies nothing: never
// override a lower tier with a guess at a typo.
func (r *Resolved) setPlacement(dst *plan.Placement, raw string, t Tier) {
	p, ok := ParsePlacement(raw)
	if !ok {
		return
	}
	*dst = p
	r.From[FieldPlacement] = t
}

// setAgentKind applies an agent-kind tier value, skipping a kind that is
// not in known (when known is non-empty) -- see Sources.KnownAgentKinds.
func (r *Resolved) setAgentKind(dst *string, kind string, t Tier, known []string) {
	if kind == "" || !kindKnown(known, kind) {
		return
	}
	*dst = kind
	r.From[FieldAgentKind] = t
}

// kindKnown reports whether kind is selectable. An empty known list means
// the caller supplied no list to check against, so every kind passes.
func kindKnown(known []string, kind string) bool {
	if len(known) == 0 {
		return true
	}
	for _, k := range known {
		if k == kind {
			return true
		}
	}
	return false
}

// ParsePlacement reads spec §12's config.toml `default_placement`
// vocabulary ("new-space"/"tab-here"/"split-here") into internal/plan's own
// Placement enum. ok is false for "" (the key is absent) and for any
// unrecognized value (a typo must not override a lower tier).
//
// Unlike the app-layer helper this replaced, "new-space" parses as a REAL
// value rather than as "nothing to apply": a tier that says "new-space"
// explicitly is expressing a choice, and under the old shape it silently
// lost to a LOWER tier's "tab-here" -- so a config.toml default_placement of
// "tab-here" could not be overridden by a last-used.json of "new-space",
// contradicting spec §10's own precedence. See PlacementValue for the write
// side.
func ParsePlacement(s string) (plan.Placement, bool) {
	switch s {
	case "new-space":
		return plan.PlacementNewSpace, true
	case "tab-here":
		return plan.PlacementTabHere, true
	case "split-here":
		return plan.PlacementSplitHere, true
	default:
		return plan.PlacementNewSpace, false
	}
}

// PlacementValue is ParsePlacement's inverse: it names p in the same
// config.toml vocabulary, for writing into last-used.json and
// projects.json. Round-tripping through the SAME vocabulary the config file
// uses is what lets one parser serve all three sources.
func PlacementValue(p plan.Placement) string {
	switch p {
	case plan.PlacementTabHere:
		return "tab-here"
	case plan.PlacementSplitHere:
		return "split-here"
	default:
		return "new-space"
	}
}
