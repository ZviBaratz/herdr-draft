// Package defaults resolves herdr-draft's layered creation defaults
// (v2 spec §10). It is pure: every tier is passed in already loaded, so the
// form (internal/app) and the headless command resolve identically and
// neither can drift from the other. This is the ONLY place the precedence
// chain exists.
//
// Before this package the chain lived inline in app.New -- three separate
// two-call ladders (placement, agent kind, worktree toggle), each
// expressing "config.toml, then last-used.json" in its own idiom, with no
// way for anything but the form to reach them. Extracting it is v2 spec §10's
// first piece of work, and its acceptance criterion was that
// internal/app's four `assembled-*` golden frames stay byte-identical.
package defaults

import (
	"maps"

	"github.com/ZviBaratz/herdr-draft/internal/agentopts"
	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
	"github.com/ZviBaratz/herdr-draft/internal/plan"
)

// Tier names where a resolved value came from, so callers can attribute it
// (the focused row's panel renders "from .herdr-draft.toml"; `create
// --json` prints its provenance). The constants are ordered by precedence:
// a LATER tier overrides an earlier one, which is v2 spec §10's "precedence,
// highest first" list read bottom-up.
type Tier int

const (
	// TierBuiltin is the value used when no configured or remembered tier
	// supplies one: this package's own fallback, or a default config.Load
	// filled in for a key config.toml left out (#220).
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
	// THIS project. Highest of the configured and remembered tiers,
	// because it is both deliberate and recent, where the repo default is
	// what a NEW checkout should start from.
	TierProjectMemory
	// TierOpenWorkspace is `herdr workspace list`: the one tier that is a
	// fact about the machine right now rather than a file. It decides ONE
	// field, placement, and only in two shapes (Resolve's last block):
	// `new-space` from the built-in, config.toml, last-used.json or
	// projects.json yields
	// to `tab in <space>` when the project already has a space open, and a
	// remembered `tab-in` falls back to `new-space` when it no longer does.
	// A `here` placement, and a `.herdr-draft.toml` that says new-space,
	// both stand -- one is a choice about this pane, the other a team's
	// committed statement. The three it overrides could only ever have
	// said new-space because that was the sole sensible answer before this
	// placement existed; honouring them would leave the default inert on
	// any machine that had used herdr-draft before (rabota-v2 C10).
	TierOpenWorkspace
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
	case TierOpenWorkspace:
		return "herdr workspace list"
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
	FieldTrustRepository  = "trust_repository"
	FieldMarkReady        = "mark_ready"
)

// FieldAgentOption is the provenance key for one agent session option
// (agent-options spec §8.2): "option.model", "option.effort". A function
// rather than more constants because the set of options is the
// declaration's (internal/agentopts), not this package's.
func FieldAgentOption(name string) string { return "option." + name }

// Sources is every tier Resolve consults, each already loaded by the
// caller -- this package performs no I/O of its own.
type Sources struct {
	// Config is the user's own config.toml (TierUserConfig), already
	// through config.Load's own defaults -- which are credited to
	// TierBuiltin instead, for each key Config.Defaulted names (#220).
	Config config.Config
	// Global is last-used.json (TierGlobalMemory).
	Global config.State
	// Repo is the selected project's committed .herdr-draft.toml
	// (TierRepoConfig), already through config.LoadRepoConfig's own
	// allow-list -- v2 spec §11's trust model is enforced THERE, not here.
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

	// Workspaces is the `herdr workspace list` snapshot TierOpenWorkspace
	// reads, with ProjectDir (the resolved project directory) and RepoRoot
	// (its repository root, "" when unknown or not a repository) as the
	// two paths plan.FindSpace matches against it. Nil Workspaces, or a
	// project with no open space, leaves every other tier's answer exactly
	// as it was; both the form (Bootstrap's own WorkspaceList) and headless
	// `create` supply the same snapshot, which is what keeps the two
	// choosing the same space.
	Workspaces []herdrc.WorkspaceInfo
	ProjectDir string
	RepoRoot   string
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
	// Placement is the default for where the agent's pane lands when there
	// is no worktree. It is resolved either way, so the form's chip can
	// hold it while a worktree is on; with a worktree the session runs in
	// its own space regardless (placement spec §14, plan.EffectivePlacement,
	// applied by both callers after the worktree toggle is final).
	Placement plan.Placement
	// Space is the open workspace already holding the project's checkout
	// (plan.FindSpace over Sources.Workspaces), or the zero value when there
	// is none. Reported whenever it exists, not only when Placement is
	// PlacementTabIn: the form offers the `tab in <space>` chip off it even
	// when a remembered `here` placement kept the cursor elsewhere.
	Space plan.Space
	// AgentKind is the default agent kind. "" means "no tier supplied one",
	// which leaves the form on its own first favorite.
	AgentKind string
	// BaseRef is the default base ref for a worktree branch. "" means HEAD
	// (form.WorktreeField.Base()'s own sentinel), and a tier's HEAD or @ is
	// resolved to it (NormalizeBase). Whether any other ref still names a
	// commit is a question for git, which this package does not ask: both
	// callers put the answer through app.SettleBase before using it. The form
	// applies it with WorktreeField.SetBase, which holds the selection until
	// a candidate list naming it has landed.
	BaseRef string
	// LinearBranchName reports whether a chosen Linear issue's own
	// branchName owns the branch (v2 spec §11's repo-config key of the same
	// name). True unless a repo config says otherwise, and false means the
	// branch is derived from the TITLE with BranchPrefix, exactly as it is
	// in manual mode -- the app layer's reading of a key the spec names
	// without defining its alternative, so that a repository whose branch
	// naming is its own can keep it while still seeding title and prompt
	// from Linear.
	LinearBranchName bool
	// TrustRepository adds `--trust-repository` to `herdr worktree create`
	// (herdr 0.9.0, #3044): trust a verified repository for that one
	// request instead of editing the user's global git configuration.
	//
	// This is the one resolved value with a DELIBERATELY SHORT tier chain:
	// built-in false, then config.toml, and nothing above it. The three
	// tiers it skips are each wrong for it for their own reason. A
	// repository must never claim its own trustworthiness, which is
	// repo.go's deny-list. last-used.json and projects.json remember what
	// the user last CHOSE IN THE FORM, and there is no row to choose this
	// on -- so "remembering" it would mean inventing a memory for a value
	// nobody set, and a security-relaxing one at that.
	TrustRepository bool

	// MarkReady is the pane-reaper toggle's default position (reap spec
	// §6.1): built-in false, then config.toml's [reaper] mark_ready, and
	// nothing above. It has a form control, unlike TrustRepository, and
	// skips memory anyway: v2 spec §10 remembers every submit, so one
	// `--reap` would make every later session reap -- orchestrators
	// included, which pane-reaper's contract says must never mark
	// themselves. .herdr-draft.toml refuses it (repo.go).
	MarkReady bool

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
		// Built-ins. There is deliberately no built-in branch prefix here:
		// config.Load already substitutes "$USER/" for an absent one (which
		// the user-config block below credits to TierBuiltin), so a ""
		// arriving here means the caller genuinely has no prefix (as
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
			FieldTrustRepository:  TierBuiltin,
			FieldMarkReady:        TierBuiltin,
		},
	}

	// --- TierUserConfig: the user's own config.toml ----------------------
	// These three always arrive with a value, because config.Load fills in
	// its own default for a key the file omits: "$USER/", true, new-space.
	// The value applies either way. Only the attribution depends on whether
	// the file set it (#220): a default Load filled in is the built-in's,
	// or `create --json` prints config.toml for a value no config.toml
	// contains -- the outcome trust_repository's comment below exists to
	// avoid, and which this block used to produce for all three. A Config
	// built in code has nothing defaulted, so its values stay config.toml's.
	cfg := s.Config
	if cfg.Defaulted.BranchPrefix {
		r.setString(FieldBranchPrefix, &r.BranchPrefix, cfg.BranchPrefix, TierBuiltin)
	} else {
		// Not setString, which reads "" as "this tier supplies nothing":
		// here "" is config.toml asking for no prefix at all
		// (TestLoad_EmptyBranchPrefix_MeansNoPrefix), so config.toml is what
		// supplied it. A Config built in code with no prefix is credited
		// the same way, as its plain-bool DefaultWorktree already is.
		r.BranchPrefix, r.From[FieldBranchPrefix] = cfg.BranchPrefix, TierUserConfig
	}
	r.setBool(FieldWorktree, &r.UseWorktree, &cfg.DefaultWorktree, userConfigTier(cfg.Defaulted.DefaultWorktree))
	r.setPlacement(&r.Placement, cfg.DefaultPlacement, userConfigTier(cfg.Defaulted.DefaultPlacement))
	r.setAgentKind(&r.AgentKind, s.Config.Agents.Default, TierUserConfig, s.KnownAgentKinds)
	// A *bool rather than a Defaulted flag: nothing supplies a default for
	// this key, so a nil means the file never mentioned [worktree], which
	// leaves the built-in false attributed to TierBuiltin, where it
	// belongs. Getting this wrong would make `create --json` print `from
	// config.toml` for a value no config.toml has ever contained.
	r.setBool(FieldTrustRepository, &r.TrustRepository, s.Config.Worktree.TrustRepository, TierUserConfig)
	// Pointer-attributed like trust_repository above, and for the same
	// reason: nothing supplies a default, so nil must stay built-in.
	r.setBool(FieldMarkReady, &r.MarkReady, s.Config.Reaper.MarkReady, TierUserConfig)

	// --- TierGlobalMemory: last-used.json --------------------------------
	r.setBool(FieldWorktree, &r.UseWorktree, s.Global.LastWorktree, TierGlobalMemory)
	r.setPlacement(&r.Placement, s.Global.LastPlacement, TierGlobalMemory)
	r.setAgentKind(&r.AgentKind, s.Global.LastKind, TierGlobalMemory, s.KnownAgentKinds)

	// --- TierRepoConfig: .herdr-draft.toml -------------------------------
	// The repository's committed default (v2 spec §11). It sits here, above
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
	// After every tier rather than inside setString, because "" there means
	// "this tier supplies nothing": a remembered HEAD has to win over a
	// repository's default_base first, and only then become the HEAD row.
	r.BaseRef = NormalizeBase(r.BaseRef)

	// --- TierOpenWorkspace: `herdr workspace list` -----------------------
	// See the Tier's own comment for what yields and what stands.
	if sp, ok := plan.FindSpace(s.Workspaces, s.ProjectDir, s.RepoRoot); ok {
		r.Space = sp
		if r.Placement == plan.PlacementNewSpace && r.From[FieldPlacement] != TierRepoConfig {
			r.Placement = plan.PlacementTabIn
			r.From[FieldPlacement] = TierOpenWorkspace
		}
	} else if r.Placement == plan.PlacementTabIn {
		r.Placement = plan.PlacementNewSpace
		r.From[FieldPlacement] = TierOpenWorkspace
	}

	return r
}

// userConfigTier is the tier a config.Config value is credited to: the
// user's config.toml when the file set it, the built-in when config.Load
// filled in its own default (#220).
func userConfigTier(defaulted bool) Tier {
	if defaulted {
		return TierBuiltin
	}
	return TierUserConfig
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
// vocabulary ("new-space"/"tab-here"/"split-here"/"tab-in") into internal/plan's own
// Placement enum. ok is false for "" (the key is absent) and for any
// unrecognized value (a typo must not override a lower tier).
//
// Unlike the app-layer helper this replaced, "new-space" parses as a REAL
// value rather than as "nothing to apply": a tier that says "new-space"
// explicitly is expressing a choice, and under the old shape it silently
// lost to a LOWER tier's "tab-here" -- so a config.toml default_placement
// of "tab-here" could not be overridden by a last-used.json of
// "new-space", contradicting v2 spec §10's own precedence. See
// PlacementValue for the write side.
func ParsePlacement(s string) (plan.Placement, bool) {
	switch s {
	case "new-space":
		return plan.PlacementNewSpace, true
	case "tab-here":
		return plan.PlacementTabHere, true
	case "split-here":
		return plan.PlacementSplitHere, true
	case "tab-in":
		return plan.PlacementTabIn, true
	default:
		return plan.PlacementNewSpace, false
	}
}

// RememberedPlacement is the placement a successful submit records in
// last-used.json and projects.json, given what that file held before.
//
// A worktree session has no placement -- it runs in the worktree's own space
// whatever the chip said (placement spec §14) -- so it records nothing new
// and keeps previous. Recording its "new-space" instead would overwrite the
// placement a session without a worktree remembered, and would turn
// "nothing recorded" into a remembered new-space that outranks
// config.toml's default_placement for every later session. The form
// (app.persistStateCmd) and `create` (remember) both call this, so the two
// paths cannot record differently.
func RememberedPlacement(in plan.Input, previous string) string {
	if in.UseWorktree {
		return previous
	}
	return PlacementValue(in.Placement)
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
	case plan.PlacementTabIn:
		return "tab-in"
	default:
		return "new-space"
	}
}

// AgentOptions resolves one agent kind's session options (agent-options
// spec §6.1) and attributes each one the kind declares: built-in `inherit`,
// then config.toml's `[agents.options.<kind>]`, and nothing above.
//
// A function of its own rather than more fields on Resolved, because it
// is a function of the kind -- and the kind is not settled when Resolve
// runs: `create --agent` overrides the resolved kind, and the form's agent
// row can move to any kind while the popup is open. Both callers ask this
// with their FINAL kind, which keeps the chain in this package without
// resolving options for a kind nobody launches.
//
// It skips memory for mark_ready's reason (reap spec §6.1): whether a
// session should plan first or think hard is a property of the task, and a
// remembered `plan mode` would silently carry into the next one. A
// repository cannot set it either -- repo.go refuses `agents.options` --
// so there is no repository tier to skip.
//
// The values are a fresh map, nil when nothing is set.
func AgentOptions(cfg config.Config, kind string) (agentopts.Values, map[string]Tier) {
	opts := agentopts.For(kind)
	if len(opts) == 0 {
		return nil, nil
	}
	configured := cfg.Agents.Options[kind]
	var vals agentopts.Values
	from := make(map[string]Tier, len(opts))
	for _, o := range opts {
		key := FieldAgentOption(o.Name)
		from[key] = TierBuiltin
		if v := configured[o.Name]; v != "" {
			if vals == nil {
				vals = agentopts.Values{}
			}
			vals[o.Name] = v
			from[key] = TierUserConfig
		}
	}
	return vals, from
}

// NormalizeBase is #194's rule 2: HEAD and @ name the base picker's HEAD
// row, whose value is "" (form.WorktreeField.Base()), and every other ref
// passes through for git to answer for. Both paths apply it -- Resolve to a
// tier's base, internal/create to --base -- so one base cannot be "" in the
// form and "HEAD" in `create`, which is what each wrote to projects.json
// before, and what turned off the clean gate's commit check for `create`
// alone (#193).
//
// Only these two spellings, because only these two need no git to
// recognise. Every other spelling of HEAD itself (HEAD^0, @{0}) is found by
// app.NamesHead, in the check both callers already make, and HEAD~1 or
// HEAD@{1} are other commits, which the HEAD row does not stand for.
func NormalizeBase(ref string) string {
	if ref == "HEAD" || ref == "@" {
		return ""
	}
	return ref
}

// WithoutBase is r with its base dropped to the built-in HEAD: what a base
// that does not resolve falls back to (#194's rule 3, app.SettleBase). It is
// attributed to the built-in rather than to the tier that named the ref,
// because that tier no longer supplies what is used, so neither a
// `from .herdr-draft.toml` line nor `create --json`'s provenance may claim
// it does.
//
// r is not modified: its From map is copied, since the form hands its
// resolution to a background check and a write to a shared map from there
// would race with the model.
func (r Resolved) WithoutBase() Resolved {
	r.From = maps.Clone(r.From)
	r.BaseRef = ""
	r.From[FieldBaseRef] = TierBuiltin
	return r
}
