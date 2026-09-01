// Package app is herdr-draft's app layer (spec §4's architecture diagram):
// the tea.Model that owns form.Model plus every data source behind it,
// wiring startup (spec §9's pre-open refusal, config/state/palette
// loading, and constructing only the statically-applicable form.Section),
// the debounced/versioned reactions that keep the form's inline verdicts
// and candidate lists current (spec §8 -- see async.go), and Linear-issue
// seeding (form.IssueChosenMsg routing, spec §6 field 1) -- everything
// form.go's own package doc assigns to "the app layer" rather than the
// form itself, which stays a dumb view with no I/O of its own (spec §4).
//
// Task 20b wires spec §9's submit pipeline on top of this: validation,
// then plan.Build/Execute in a tea.Cmd, streaming plan.Progress into a
// form.SubmitView (see the handleSubmit/updateSubmitting doc comments in
// this file and async.go).
package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/ZviBaratz/herdr-draft/internal/clauth"
	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/form"
	"github.com/ZviBaratz/herdr-draft/internal/gitx"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
	"github.com/ZviBaratz/herdr-draft/internal/linear"
	"github.com/ZviBaratz/herdr-draft/internal/pathx"
	"github.com/ZviBaratz/herdr-draft/internal/plan"
	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// claudeKind is the only AgentField value for which clauth account pinning
// applies (spec §6 field 7), mirrored from internal/plan/build.go's own
// unexported claudeAgentKind -- kept as this package's own small constant
// rather than exporting plan's, since app has no other reason to import
// plan in Task 20 (submit/plan.Build wiring is Task 20b's job).
const claudeKind = "claude"

// knownAgentKinds is herdr's full 23-kind agent list (spec §6 field 6:
// "full kind list (herdr's 23)"), translated from
// /home/zvi/Projects/herdr/src/detect/mod.rs's `Agent::ALL` /
// `agent_label` (herdr commit b1ff4582, the same pinned commit
// internal/theme/palette.go's own translated constants cite) -- this is
// the ONLY source of the value herdr-draft ships for "the known 23"; spec
// §12's config.toml example never enumerates them, only `[agents]
// favorites`/`default`. AgentField.SetKinds' own "index 0 is the
// configured default" contract, plus this task's carried "favorites-first"
// requirement, both work over an ORDERED list -- see orderedAgentKinds.
var knownAgentKinds = []string{
	"pi", "claude", "codex", "gemini", "cursor", "devin", "agy", "cline",
	"omp", "mastracode", "opencode", "copilot", "kimi", "kiro", "droid",
	"amp", "grok", "hermes", "kilo", "qodercli", "qwen", "maki", "muse",
}

// defaultPromptTemplate is spec §10's built-in seeding template, used
// whenever config.Config.Linear.PromptTemplate is empty.
const defaultPromptTemplate = "Work on {identifier}: {title}\n\n{url}\n\n{description}"

// Clock groups the sleep primitive every debounced source in async.go
// needs, so tests can run the 150ms debounce window without a real sleep.
// The zero value is production-ready (Sleep nil defaults to time.Sleep).
type Clock struct {
	Sleep func(time.Duration)
}

func (c Clock) sleep(d time.Duration) {
	if c.Sleep != nil {
		c.Sleep(d)
		return
	}
	time.Sleep(d)
}

// linearSource is the subset of Linear access Model needs -- satisfied by
// *linear.Client in production (constructed internally by Bootstrap once
// an API key resolves) and a fake in tests. nil means Linear isn't
// configured at all (spec §6 field 1's static precondition).
type linearSource interface {
	AssignedIssues(ctx context.Context) ([]linear.Issue, error)
}

// clauthSource is the subset of clauth access Model needs -- satisfied by
// clauthLoader (wrapping clauth.Load, see NewClauthSource) in production
// and a fake in tests.
type clauthSource interface {
	Status(ctx context.Context) (clauth.Status, error)
}

// gitSource is the subset of git/filesystem access Model needs -- satisfied
// by gitxSource (see NewGitSource) in production and a fake in tests. Every
// method here is a thin wrapper over internal/gitx, plus os.Stat for
// DirExists (gitx itself has no directory-existence helper) and
// internal/pathx for the Project field's own path-mode browsing
// (ListSubdirs/ResolvePath, spec §6 field 2) -- filesystem rather than
// git, but here for the same reason DirExists is: it is I/O this package's
// own tests must be able to answer deterministically.
type gitSource interface {
	DirExists(path string) bool
	IsGitRepo(dir string) bool
	ListSubdirs(dir string, limit int) []string
	ResolvePath(path string) string
	ListBranches(ctx context.Context, dir string, limit int) ([]string, error)
	BranchExists(ctx context.Context, dir, name string) (bool, error)
	CurrentBranch(ctx context.Context, dir string) (string, error)
	FetchPrune(ctx context.Context, dir string) error
}

// Deps groups every I/O-capable collaborator Model needs, each behind a
// small interface so tests substitute fakes -- no real herdr/git/network
// call ever happens in this package's own tests.
type Deps struct {
	Runner herdrc.Runner
	// Linear is nil when Linear isn't configured (no resolved API key) --
	// see Bootstrap.
	Linear linearSource
	Clauth clauthSource
	Git    gitSource
	Clock  Clock
}

// Env is the plugin-invocation environment Bootstrap needs, read from the
// process's own environment variables by main.go (spec §5) -- kept as a
// plain struct, rather than Bootstrap reading os.Getenv itself, so
// Bootstrap stays testable with a deterministic, explicit input.
type Env struct {
	// ContextJSON is $HERDR_PLUGIN_CONTEXT_JSON.
	ContextJSON string
	// ConfigDir is $HERDR_PLUGIN_CONFIG_DIR.
	ConfigDir string
	// StateDir is $HERDR_PLUGIN_STATE_DIR.
	StateDir string
}

// Setup is New's own input: everything needed to construct a Model,
// already resolved by Bootstrap (or a test) -- New itself performs no I/O,
// which is what makes it directly unit-testable with fakes (see
// app_test.go).
type Setup struct {
	Deps         Deps
	Ctx          herdrc.Context
	Config       config.Config
	State        config.State
	Palette      theme.Palette
	StateDir     string
	Workspaces   []herdrc.WorkspaceInfo
	ClauthStatus clauth.Status
	LinearCache  []linear.Issue
	// LinearUnavailable is non-empty when Linear is CONFIGURED but its API
	// key could not be resolved (a broken [linear] api_key_cmd, or an
	// inline api_key in a config.toml wider than 0600). Distinct from
	// Deps.Linear == nil, which means Linear is not configured at all: the
	// first renders spec §6 field 1's own field present-but-inert carrying
	// this reason (spec §13, "degrade ... with a reason"), the second
	// renders no field at all. See Bootstrap.
	LinearUnavailable string
}

// Bootstrap performs spec §9's pre-open refusal plus every other piece of
// startup work that must finish before the form can render: parsing the
// plugin invocation context, probing herdr reachability (a single `herdr
// workspace list` call doubles as both the reachability probe and the
// source of the workspace label list/candidate repo roots New needs),
// loading config/state/palette, resolving the Linear API key, and loading
// clauth's status feed -- then hands all of it to New.
//
// Only two conditions refuse outright (spec §9: "herdr socket unreachable
// -> plain-text error and exit", "on missing/invalid context"): an invalid
// $HERDR_PLUGIN_CONTEXT_JSON, an unreachable herdr, or a config.toml that
// fails to parse (extended from the spec's literal two cases: proceeding
// with config.Load's own zero-value Config on a parse error -- as opposed
// to its documented defaults() -- would silently corrupt every config-
// derived default the form has, which a clear stderr message is far
// preferable to). Every other failure below (a broken Linear api_key_cmd,
// a clauth status load failure, cache load failures) degrades gracefully
// per spec §13 ("Linear/clauth/network failures degrade the respective
// field to inert with a reason; they never block manual-mode creation")
// rather than refusing.
func Bootstrap(env Env, runner herdrc.Runner, clauthSrc clauthSource, gitSrc gitSource, clock Clock) (Model, error) {
	ctx, err := herdrc.ParseContext(env.ContextJSON)
	if err != nil {
		return Model{}, err
	}

	bg := context.Background()
	workspaces, err := runner.WorkspaceList(bg)
	if err != nil {
		return Model{}, fmt.Errorf("herdr unreachable: %w", err)
	}

	cfg, err := config.Load(env.ConfigDir)
	if err != nil {
		return Model{}, err
	}

	// LoadState never returns a non-nil error (spec §12: state is entirely
	// loss-tolerant) -- the error return only exists for API symmetry.
	state, _ := config.LoadState(env.StateDir)

	palette := theme.LoadHerdrPalette(cfg.Palette)

	// linear.ResolveAPIKey distinguishes three outcomes, and so does this
	// (finding I5): a key (Linear works), no key and no error (Linear is
	// not configured at all -- spec §6 field 1's "absent -> not rendered"),
	// or an ERROR, which it raises deliberately when the user's own chosen
	// key source fails: a broken api_key_cmd, or an inline api_key sitting
	// in a config.toml readable by anyone but its owner. The last case used
	// to be folded into the second, so a typo in api_key_cmd made the whole
	// Linear field vanish with nothing anywhere saying why; spec §13
	// requires it to degrade "with a reason" instead. Either way the plugin
	// still opens -- an optional integration's misconfiguration never
	// blocks manual-mode creation.
	var linearSrc linearSource
	var linearCache []linear.Issue
	var linearUnavailable string
	key, kerr := linear.ResolveAPIKey(cfg.Linear.APIKeyCmd, cfg.Linear.APIKey, env.ConfigDir)
	switch {
	case kerr != nil:
		linearUnavailable = linearUnavailableReason(kerr)
	case key != "":
		linearSrc = &linear.Client{APIKey: key}
		if cached, _, cerr := linear.LoadCache(env.StateDir); cerr == nil {
			linearCache = cached
		}
	}

	clauthEnabled := true
	if cfg.Clauth.Enabled != nil {
		clauthEnabled = *cfg.Clauth.Enabled
	}
	var clauthStatus clauth.Status
	if clauthEnabled && clauthSrc != nil {
		if st, serr := clauthSrc.Status(bg); serr == nil {
			clauthStatus = st
		}
	}

	deps := Deps{Runner: runner, Linear: linearSrc, Clauth: clauthSrc, Git: gitSrc, Clock: clock}
	return New(Setup{
		Deps:              deps,
		Ctx:               ctx,
		Config:            cfg,
		State:             state,
		Palette:           palette,
		StateDir:          env.StateDir,
		Workspaces:        workspaces,
		ClauthStatus:      clauthStatus,
		LinearCache:       linearCache,
		LinearUnavailable: linearUnavailable,
	}), nil
}

// linearUnavailableReason turns a linear.ResolveAPIKey error into the
// short line IssueField.SetUnavailable renders on its hint row. The
// package's own "resolve linear api key: " prefix is dropped -- the field
// is already labeled "Issue:" and the user is looking at the Linear field;
// repeating it costs cells the actual cause needs.
func linearUnavailableReason(err error) string {
	return strings.TrimPrefix(err.Error(), "resolve linear api key: ")
}

// Model is the real tea.Model herdr-draft runs: form.Model plus every
// concrete field it needs to drive via setters (form.go's own doc: "the
// app layer is expected to hold each concrete Section by its own concrete
// type"), the data sources behind them, and the small amount of app-owned
// state (request-version counters, last-observed getter snapshots) the
// debounce/seeding machinery in async.go needs.
type Model struct {
	palette  theme.Palette
	cfg      config.Config
	state    config.State
	ctx      herdrc.Context
	stateDir string

	deps Deps

	form form.Model

	// issue and account are nil when their own static precondition isn't
	// met (spec §6: Linear unconfigured, fewer than two clauth profiles) --
	// New simply never constructs or appends them.
	issue     *form.IssueField
	dir       *form.DirField
	title     *form.TitleField
	worktree  *form.WorktreeField
	placement *form.PlacementField
	agent     *form.AgentField
	account   *form.AccountField
	prompt    *form.PromptField

	// workspaces is fetched once at form-open (Bootstrap's own
	// WorkspaceList call, spec §8) and never re-fetched -- the source for
	// both DirField's candidate pool and the title-duplicate label check.
	workspaces []herdrc.WorkspaceInfo

	// clauthStatus is the last clauth status feed this Model has seen --
	// New's own Setup.ClauthStatus, refreshed by handleClauthResult
	// alongside its m.account.SetProfiles call. AccountField itself
	// exposes no per-profile AuthStatus getter (only Pin(), the selected
	// profile's NAME), so submit-time validation (accountAuthBlocked)
	// keeps its own copy to look a pinned name up against, and
	// handleClearRequested reuses it to reseed a rebuilt AccountField
	// without a real clauth reload.
	clauthStatus clauth.Status

	// linearIssues is the last Linear issue list this Model has seen --
	// New's own Setup.LinearCache, refreshed by handleLinearResult
	// alongside its m.issue.SetIssues call. Kept for the same reason as
	// clauthStatus: handleClearRequested (spec §6's ⌃R⌃R rebuild) needs
	// it to reseed a rebuilt IssueField without waiting on a fresh Linear
	// round-trip.
	linearIssues []linear.Issue

	// linearUnavailable mirrors Setup.LinearUnavailable, kept so
	// handleClearRequested (spec §6's ⌃R⌃R rebuild) reconstructs the same
	// inert Linear field rather than silently promoting it back to a
	// working one.
	linearUnavailable string

	// linearIssueSelected tracks IssueField's own "none" vs a real
	// selection (spec §6 field 1: "In Linear mode branchName owns the
	// branch and the title is free text") -- reactToChanges only derives a
	// branch suggestion from the typed title while this is false.
	linearIssueSelected bool

	// last-observed getter snapshots -- reactToChanges diffs the CURRENT
	// value against these after every message routed through form.Model,
	// the same before/after-comparison discipline every Section's own
	// Update already uses for its own value, applied here one level up
	// (form.Model exposes no "did section X change" signal of its own).
	lastDir        string
	lastDirTyped   string
	lastTitle      string
	lastDupKey     string
	lastWorktreeOn bool
	lastFocusedID  string

	// projectCandidates is the fragment-mode candidate pool built once at
	// New (buildDirCandidates), kept so leaving path mode can put it back
	// -- DirField holds only ONE pool at a time, whichever was supplied
	// last.
	projectCandidates []string

	// browseDir is the RAW (un-expanded) directory portion of the typed
	// text whose listing DirField currently holds, or "" when the field is
	// in fragment mode. It is the memoization atrium's DirectoryPicker got
	// from its own cachedDir: typing WITHIN one directory re-ranks the
	// listing already on hand instead of re-reading it, and only a change
	// of parent schedules a fresh one.
	browseDir string

	// dirCandVersion is the single monotonic counter behind every
	// DirField.SetCandidates call this Model makes -- the initial project
	// pool, each browse result, and each restore of the pool on leaving
	// path mode. One counter, because DirField's own staleness guard drops
	// anything older than the highest version it has accepted: two
	// independent counters would let a browse result outrank a newer
	// restore.
	dirCandVersion int

	// request-version counters -- see async.go's request type and the
	// schedule*/handle* pair for each source.
	dirReqVersion    int
	baseReqVersion   int
	titleReqVersion  int
	browseReqVersion int
	// clauthReqVersion is the clauth-reload source's own version counter --
	// bumped directly by reloadClauthCmd (there is no separate debounce
	// phase for this source, unlike the three above), and compared against
	// by handleClauthResult (fix round 1: closes a rapid-refocus staleness
	// gap -- see clauthResultMsg's own doc comment in async.go).
	clauthReqVersion int

	// baseItemsVersion/issueItemsVersion are the monotonic version
	// parameters WorktreeField.SetBaseItems/IssueField.SetIssues expect
	// their caller to supply -- a SEPARATE counter space from the
	// request-staleness versions above (those gate which async result to
	// apply at all; these are the version widgets.Picker.SetItems itself
	// uses to decide "reset cursor to top" vs "preserve selection by ID").
	baseItemsVersion  int
	issueItemsVersion int

	// fetchedRepos tracks which repo paths have already had their
	// once-per-form-open `git fetch --prune` fired (spec §6 field 4) --
	// never reset, so a repo already fetched this form-open never fetches
	// again even if the user navigates away and back.
	fetchedRepos map[string]bool

	// worktreeDefaultOn/worktreeDefaultApplied implement spec §6 field 4's
	// "default from config" (config.Config.DefaultWorktree, overridden by
	// config.State.LastWorktree when set) as a ONE-SHOT application: see
	// handleDirResult's own doc comment for why it can only apply once the
	// target is known to be a usable git repo, and only once ever (a later
	// directory change must never fight the user's own toggle choice by
	// re-forcing the config default back).
	worktreeDefaultOn      bool
	worktreeDefaultApplied bool

	// dirInvalid/titleDupBlocked mirror the last dir-validity/title-dup
	// verdict each already pushed into DirField/TitleField (handleDirResult/
	// handleTitleResult), kept as plain booleans on Model too because
	// neither field exposes a getter back for its own pushed verdict --
	// only a rendered marker/text. checkSubmitValidation (submit orchestration,
	// spec §9) reads these directly rather than re-deriving them, since
	// they're already exactly what the form is currently SHOWING the user.
	dirInvalid      bool
	titleDupBlocked bool

	// width/height are this Model's own copy of the last tea.WindowSizeMsg
	// (form.Model keeps its own copy internally, unreachable from here) --
	// needed so View can render form.SubmitView.ViewAt at the right size
	// once m.submitting is true and m.form.View() is no longer what's on
	// screen.
	width, height int

	// submitting is true from form.SubmitMsg's own validation passing
	// (handleSubmit/startSubmit) until the popup quits -- see
	// updateSubmitting's own doc comment for why message routing branches
	// on it entirely (SubmitView is not a form.Section and takes no part
	// in form.Model's focus ring).
	submitting bool
	// submitDeadEnd is true only in the one submitting state that has no
	// other way out: step 1 (topology creation) itself failed, so
	// SubmitView never gets a SetFailure call at all (spec §9 scopes the
	// keep-or-clean prompt to "after step 1 succeeded") -- its own k/c
	// grammar stays permanently inert. updateSubmitting's Esc/Ctrl+C
	// escape hatch is scoped to exactly this state (see its own doc
	// comment): at every OTHER point in the submitting lifecycle --
	// actively streaming, waiting on plan.CleanCheck, or showing a real
	// keep-or-clean prompt -- Esc/Ctrl+C must NOT quit, or it would either
	// strand plan.Execute's own background goroutine forever blocked on
	// an unbuffered channel send nobody is left to drain (fix round 1,
	// reviewer-measured: a permanently elevated goroutine count), or let
	// the user bypass an intentional keep/clean choice silently.
	submitDeadEnd bool
	// submitInput/submitCreated/submitCleanDecision are the running
	// submit attempt's own state, threaded across the several async Cmds
	// spec §9's staged pipeline needs (plan.Execute's own streamed
	// progress, then plan.CleanCheck, then -- on a CleanMsg -- plan.Clean)
	// -- see async.go's runSubmitCmd/runCleanCheckCmd/runCleanCmd.
	submitInput         plan.Input
	submitCreated       herdrc.CreatedTopology
	submitCleanDecision plan.CleanDecision
	// submitProgress is the full, Total-length working progress list
	// startSubmit seeds at StepPending and handleSubmitProgress updates
	// in place by Index as each streamed plan.Progress event arrives --
	// SubmitView.SetProgress REPLACES its own displayed list on every
	// call, so this is what makes "every step, including ones not yet
	// started, visible from the first frame" possible (plan.Execute's own
	// onProgress never itself emits a StepPending event for a step that
	// hasn't started yet).
	submitProgress []plan.Progress
	// submitView is constructed fresh by startSubmit for each submit
	// attempt (nil before the first one) -- a *form.SubmitView, not a
	// form.Section: it takes no part in form.Model's own focus ring (see
	// submitview.go's own file doc comment).
	submitView *form.SubmitView

	// initCmds carries the very first debounced dir-validity/base-list
	// requests (plus the Linear async refresh, when configured), scheduled
	// in New rather than Init -- see Init's own doc comment for why: Init()
	// returns only a tea.Cmd, with no way to persist a mutated Model, so
	// any request-counter bump made from there would be silently discarded
	// by bubbletea, which keeps using the pre-Init Model value for its
	// first real Update call.
	initCmds []tea.Cmd
}

var _ tea.Model = Model{}

// New constructs a Model from an already-resolved Setup -- see Bootstrap
// for the real startup sequence that gathers Setup's own fields via I/O.
// New itself performs no I/O: every section is built and populated purely
// from the arguments it's given.
func New(s Setup) Model {
	palette := s.Palette
	m := Model{
		palette:      palette,
		cfg:          s.Config,
		state:        s.State,
		ctx:          s.Ctx,
		stateDir:     s.StateDir,
		deps:         s.Deps,
		workspaces:   s.Workspaces,
		clauthStatus: s.ClauthStatus,
		linearIssues: s.LinearCache,

		linearUnavailable: s.LinearUnavailable,

		fetchedRepos: map[string]bool{},
	}

	m.dir = form.NewDirField(palette)
	m.title = form.NewTitleField(palette)
	m.worktree = form.NewWorktreeField(palette)
	m.placement = form.NewPlacementField(palette)
	m.agent = form.NewAgentField(palette)
	m.prompt = form.NewPromptField(palette)

	// [default_placement] (spec §12), when set to something other than
	// "new-space" (PlacementField's own natural starting default -- no
	// call needed for that case): the two folded-in Task 20 gaps this
	// task's brief names ("[clauth] default and a non-default
	// [default_placement] have no pre-selection path"). "off"/on-then-
	// snapped-back-to-New's own interaction with worktree defaulting on
	// is handled the same way it already was before this gap was closed
	// -- see PlacementField.SetValue's own doc comment: applying this now
	// is safe regardless of where worktreeDefaultOn will later land,
	// since a worktree turning on always snaps Placement back to New
	// space anyway (spec §12's own config.toml comment: "when worktree is
	// off").
	// config.toml's own value first, then -- when a previous successful
	// submit recorded one (spec §12's last-used.json, written by
	// persistStateCmd) -- the placement the user actually launched with
	// last time, which overrides it. State.LastPlacement was written by
	// nobody and read by nobody before the state layer was wired up
	// (finding I2); this is its read side.
	if p, ok := placementFromConfigValue(s.Config.DefaultPlacement); ok {
		m.placement.SetValue(p)
	}
	if p, ok := placementFromConfigValue(s.State.LastPlacement); ok {
		m.placement.SetValue(p)
	}

	// Linear (spec §6 field 1): rendered only when Linear is configured --
	// decided entirely by whether Bootstrap resolved an API key at all
	// (Deps.Linear != nil is the static precondition; New never itself
	// tries to resolve a key).
	if s.Deps.Linear != nil {
		m.issue = form.NewIssueField(palette)
		if len(s.LinearCache) > 0 {
			m.issueItemsVersion++
			m.issue.SetIssues(m.issueItemsVersion, s.LinearCache)
		}
	} else if s.LinearUnavailable != "" {
		// Configured but broken (finding I5): rendered present-but-inert
		// with the reason, rather than silently absent -- see
		// Setup.LinearUnavailable and IssueField.SetUnavailable. No
		// linearSource exists, so nothing ever schedules a refresh for it
		// (initCmds below gates on m.issue != nil AND Deps.Linear != nil).
		m.issue = form.NewIssueField(palette)
		m.issue.SetUnavailable(s.LinearUnavailable)
	}

	// Account (spec §6 field 7): rendered only when clauth is enabled AND
	// >= 2 profiles exist. Bootstrap folds "enabled" into ClauthStatus.
	// Profiles (a disabled clauth simply never populates it), but a caller
	// constructing Setup directly (as this package's own tests do) could
	// still hand in a non-empty ClauthStatus with Deps.Clauth == nil --
	// gating on BOTH, mirroring the Deps.Linear != nil gate above, is what
	// keeps reloadClauthCmd (spec §11: "load ... on account focus") from
	// ever being scheduled against a nil clauthSource in the first place
	// (reloadClauthCmd/handleClauthResult are also defensively guarded on
	// their own -- see async.go -- but this is the gate that matters: with
	// it, m.account is simply never non-nil when Deps.Clauth is nil).
	if s.Deps.Clauth != nil && len(s.ClauthStatus.Profiles) >= 2 {
		m.account = form.NewAccountField(palette)
		m.account.SetProfiles(s.ClauthStatus)
		// [clauth] default (spec §12), when set to a real profile name --
		// "" and the config's own documented "active" sentinel are both
		// no-ops (AccountField.SetPin's own doc comment): the picker
		// already starts on the "active" row by construction.
		m.account.SetPin(s.Config.Clauth.Default)
	}

	// Agent (spec §6 field 6, carried requirement): favorites first, then
	// every remaining known kind, so a favorite is always a chip AND the
	// full list stays reachable behind "more…" (AgentField's own doc: it
	// derives its favorite chips from THIS list's leading entries).
	m.agent.SetKinds(orderedAgentKinds(s.Config.Agents.Favorites))
	// ... then `[agents] default`, which spec §12 lists but nothing read
	// until now: SetKinds' own "index 0 is the default" contract can only
	// ever express favorites[0], so a config naming a default OUTSIDE the
	// favorites row -- or a different one within it -- had no effect at
	// all.
	m.agent.SetKind(s.Config.Agents.Default)
	// ... then the kind the user actually launched with last time, when a
	// previous successful submit recorded one (spec §12's last-used.json).
	// Last-used wins over the configured default, the same precedence
	// worktreeDefaultOn applies just below for `default_worktree` vs
	// State.LastWorktree. Either call is a no-op for an empty or unknown
	// kind -- see AgentField.SetKind.
	m.agent.SetKind(s.State.LastKind)

	// Project (spec §6 field 2): current space's repo root, then the
	// current workspace cwd, then every open workspace's own worktree
	// root, then recents -- DirField.SetCandidates selects candidates[0]
	// as the initial selection (widgets.Picker's own same-version
	// preserve-by-ID/fallback-to-index-0 behavior on a picker with no
	// prior selection -- see task-20-report.md for the full trace), so
	// ordering IS the default.
	m.projectCandidates = buildDirCandidates(s.Ctx, s.Workspaces, s.State.Recents)
	m.supplyDirCandidates(m.projectCandidates)
	m.lastDir = m.dir.Value()
	// Path mode's literal-fallback row is expanded the same way the
	// browsed rows around it are (see DirField.SetPathExpander): the app
	// layer owns every path resolution, the form package none.
	m.dir.SetPathExpander(s.Deps.Git.ResolvePath)

	m.worktreeDefaultOn = s.Config.DefaultWorktree
	if s.State.LastWorktree != nil {
		m.worktreeDefaultOn = *s.State.LastWorktree
	}

	sections := make([]form.Section, 0, 9)
	if m.issue != nil {
		sections = append(sections, m.issue)
	}
	sections = append(sections, m.dir, m.title,
		// The three worktree zones (carried requirement: "must still read
		// as ONE visual group") are inserted back-to-back, in spec §6
		// field 4's own on/off-then-branch-then-base order, with nothing
		// from another field between them.
		m.worktree.ChipsSection(), m.worktree.BranchSection(), m.worktree.BaseSection(),
		m.placement, m.agent)
	if m.account != nil {
		sections = append(sections, m.account)
	}
	sections = append(sections, m.prompt)

	m.form = form.New(form.Setup{Palette: palette, Sections: sections})
	m.syncDerivedInertness()

	m.initCmds = []tea.Cmd{m.scheduleDirCheck(m.lastDir), m.scheduleBaseCheck(m.lastDir)}
	if m.issue != nil && s.Deps.Linear != nil {
		m.initCmds = append(m.initCmds, m.refreshLinearCmd())
	}

	return m
}

// Init focuses the form's initial section and kicks off the very first
// debounced dir-validity/base-list check plus (when configured) the
// Linear async refresh -- the actual scheduling/counter-bumping for those
// already happened in New (see initCmds' own doc comment); this only
// returns the resulting Cmds.
func (m Model) Init() tea.Cmd {
	return tea.Batch(append([]tea.Cmd{m.form.Init()}, m.initCmds...)...)
}

// Update dispatches an incoming message. tea.WindowSizeMsg is captured
// into this Model's own width/height copy first, unconditionally (see
// their own doc comment on Model), regardless of what else the message
// dispatch below does with it. Once m.submitting is true (see
// startSubmit), every message is routed to updateSubmitting instead --
// form.Model's own key grammar and every debounce/result source below are
// all bypassed entirely, since SubmitView has replaced the form on screen
// and takes no part in form.Model's focus ring (submitview.go's file doc
// comment). Otherwise: the app-level messages this package itself defines
// or intercepts (form.IssueChosenMsg seeding, form.CancelMsg/SubmitMsg/
// ClearRequestedMsg, and every async.go debounce/result message) are
// handled directly; everything else is routed through form.Model's own
// Update (routeToForm), which also runs reactToChanges afterward to
// notice any value change that needs its own debounced reaction or
// dynamic-inertness sync.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if wsm, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = wsm.Width, wsm.Height
	}
	if m.submitting {
		return m.updateSubmitting(msg)
	}
	switch msg := msg.(type) {
	case form.IssueChosenMsg:
		return m.handleIssueChosen(msg)
	case form.CancelMsg:
		return m, tea.Quit
	case form.SubmitMsg:
		return m.handleSubmit()
	case form.ClearRequestedMsg:
		return m.handleClearRequested()
	case dirDebounceMsg:
		return m.handleDirDebounce(msg)
	case dirResultMsg:
		return m.handleDirResult(msg)
	case browseDebounceMsg:
		return m.handleBrowseDebounce(msg)
	case browseResultMsg:
		return m.handleBrowseResult(msg)
	case baseDebounceMsg:
		return m.handleBaseDebounce(msg)
	case baseResultMsg:
		return m.handleBaseResult(msg)
	case fetchPruneDoneMsg:
		return m.handleFetchPruneDone(msg)
	case titleDebounceMsg:
		return m.handleTitleDebounce(msg)
	case titleResultMsg:
		return m.handleTitleResult(msg)
	case linearResultMsg:
		return m.handleLinearResult(msg)
	case clauthResultMsg:
		return m.handleClauthResult(msg)
	default:
		return m.routeToForm(msg)
	}
}

// routeToForm forwards msg to form.Model's own Update, then runs
// reactToChanges (which may itself schedule further async work) before
// returning both Cmds batched together.
func (m Model) routeToForm(msg tea.Msg) (Model, tea.Cmd) {
	next, cmd := m.form.Update(msg)
	m.form = next.(form.Model)
	cmds := append(m.reactToChanges(), cmd)
	return m, tea.Batch(cmds...)
}

// handleIssueChosen routes form.IssueChosenMsg (spec §6 field 1: "Selecting
// seeds: Title <- issue title, Branch <- branchName, Prompt <- template")
// into the three target fields' own touched-respecting setters -- IssueField
// itself deliberately never calls them (see its own doc comment: "the app
// layer routes seeding — the form does NOT"). A nil Issue (the "none" row,
// manual mode) seeds nothing; it only flips linearIssueSelected back to
// false so reactToChanges resumes deriving the branch from typed title text.
func (m Model) handleIssueChosen(msg form.IssueChosenMsg) (Model, tea.Cmd) {
	m.linearIssueSelected = msg.Issue != nil
	if msg.Issue != nil {
		iss := *msg.Issue
		m.title.SetTitle(iss.Title, true)
		m.worktree.SetBranch(iss.BranchName, true)
		m.prompt.SetValue(renderPromptTemplate(m.cfg.Linear.PromptTemplate, iss), true)
	}
	cmds := m.reactToChanges()
	return m, tea.Batch(cmds...)
}

// handleSubmit is form.SubmitMsg's own handler (spec §9's submit
// pipeline): runs every blocking validation FIRST (checkSubmitValidation),
// refusing to create anything at all when one fires -- no plan.Build call,
// no plan.Execute, nothing -- and only once every check clears does it
// build the plan and start the staged execution (startSubmit).
func (m Model) handleSubmit() (Model, tea.Cmd) {
	if cmd, blocked := m.checkSubmitValidation(); blocked {
		return m, cmd
	}

	in := m.buildPlanInput()
	ops, err := plan.Build(in)
	if err != nil {
		// Every precondition plan.Build itself checks (non-empty title,
		// worktree-requires-git-repo, pin-requires-claude) is already
		// enforced above or by construction (accountPin never returns a
		// pin for a non-claude kind; UseWorktree can only be true once
		// WorktreeField.Enabled() -- its own git-repo gate -- is true) --
		// this is a defensive fallback for a future Build rule this
		// package hasn't anticipated, not a path exercised today.
		m.title.SetVerdict(m.title.Value(), "could not build plan: "+err.Error())
		return m, m.form.FocusByID("title")
	}

	return m.startSubmit(ops, in)
}

// checkSubmitValidation runs spec §9's submit-time validation list, in
// order, stopping at the first blocking condition and re-focusing the
// offending section -- form.Model.FocusByID's own doc comment names
// exactly this rule ("a failing submit re-focuses Title"), generalized
// here to the other two blocking fields for the same "point at the
// problem" reason (a controller judgment call beyond the brief's literal
// wording, which names re-focus only for the title-duplicate case --
// flagged in this task's own report):
//
//   - An empty title: not one of spec §9's own three named validations,
//     but a real gap this package must still guard -- MapKey's own
//     grammar only blocks a bare Enter from submitting an empty Title
//     (form.go's titleValuer/TitleEmpty), while ⌃S submits from ANY zone
//     regardless (keys.go), and plan.Build itself rejects an empty title
//     outright. Disclosed judgment call (flagged by review): this
//     re-orders spec §9's own literal validation list, which names
//     directory validity FIRST -- empty-title is checked before it here,
//     deliberately, because every check below it either doesn't apply
//     (workspace-label duplicate is defined as false for an empty title)
//     or doesn't matter (a valid-but-unused directory) without a title
//     at all; there is no scenario where showing a directory-invalid
//     verdict ahead of "title required" would be more useful to the
//     user.
//   - Directory validity (dirInvalid, kept live by handleDirResult).
//   - Branch/workspace-label duplicates (titleDupBlocked, kept live by
//     handleTitleResult) -- the SAME live verdict TitleField is already
//     showing; this does not compute a new message, only blocks.
//   - A pinned clauth profile whose auth_status isn't "ok"
//     (accountAuthBlocked).
//
// Returns (nil, false) when nothing blocks.
func (m Model) checkSubmitValidation() (tea.Cmd, bool) {
	if strings.TrimSpace(m.title.Value()) == "" {
		m.title.SetVerdict(m.title.Value(), "title required")
		return m.form.FocusByID("title"), true
	}
	if m.dirInvalid {
		return m.form.FocusByID("dir"), true
	}
	if m.titleDupBlocked {
		return m.form.FocusByID("title"), true
	}
	if pin, status, blocked := m.accountAuthBlocked(); blocked {
		// Fix round 1 (reviewer finding -- silent failure): the row
		// marker Task 18 already renders for this profile was already
		// visible BEFORE this blocked Create press, so it alone gave no
		// new signal that anything happened; AccountField.SetVerdict
		// (fix round 1) pushes a fresh, submit-time message, the same
		// "the field itself shows why" pattern Title's own dup verdict
		// already used.
		m.account.SetVerdict(pin, fmt.Sprintf("blocked — auth: %s", status))
		return m.form.FocusByID("account"), true
	}
	return nil, false
}

// accountPin returns the Input.AccountPin plan.Build should receive:
// AccountField's own Pin() only when the currently selected agent kind is
// claude (spec §6 field 7 -- pinning is meaningless, and plan.Build itself
// rejects, any other kind) -- "" (unpinned) otherwise, even if the
// picker's own cursor still sits on a stale pin from before the agent
// kind changed (AccountField's present-but-inert state, driven by
// SetAgentIsClaude, does not reset the underlying picker selection, only
// its own visibility).
func (m Model) accountPin() string {
	if m.account == nil || m.agent.Value() != claudeKind {
		return ""
	}
	return m.account.Pin()
}

// accountAuthBlocked reports whether the currently pinned profile (see
// accountPin) has a known, non-"ok" auth_status (spec §9: "pinned account
// auth_status != ok -> blocking verdict"), and that raw status text when
// it does (fix round 1: checkSubmitValidation threads it into
// AccountField.SetVerdict's own new blocking message) -- consulting
// m.clauthStatus, the last clauth feed New/handleClauthResult recorded
// (AccountField itself exposes no per-profile AuthStatus getter of its
// own). A pin naming a profile clauth's own feed doesn't currently list
// (e.g. one removed since the last reload) is not blocked here -- there
// is nothing to judge it against.
//
// Disclosed judgment call (flagged by review, not explicitly specified
// by spec §9): an EMPTY AuthStatus is treated as non-blocking, same as
// "ok" -- mirroring accountRow's own accountWarning helper (Task 18),
// which already treats "" the same way for the picker row's own inline
// marker. Consistency with that already-shipped, already-reviewed
// behavior was chosen over inventing a stricter "unknown status blocks"
// rule this task was never asked to add; an empty AuthStatus would
// otherwise disagree with what the row itself is already showing (an
// unmarked, unwarned row) about the very same profile.
func (m Model) accountAuthBlocked() (pin, status string, blocked bool) {
	pin = m.accountPin()
	if pin == "" {
		return "", "", false
	}
	for _, p := range m.clauthStatus.Profiles {
		if p.Name == pin {
			if p.AuthStatus != "" && p.AuthStatus != "ok" {
				return pin, p.AuthStatus, true
			}
			return pin, "", false
		}
	}
	return pin, "", false
}

// buildPlanInput composes plan.Input from the form's current field state
// (spec §9) -- called only once checkSubmitValidation has cleared every
// blocking condition.
func (m Model) buildPlanInput() plan.Input {
	return plan.Input{
		// Expanded here, at the boundary where the project directory stops
		// being text the user typed and becomes an argument for herdr's CLI
		// and git (finding I3): `herdr worktree create --cwd` expands a
		// leading "~" server-side, but `herdr workspace create --cwd` and
		// `herdr pane split --cwd` do not -- see internal/pathx's own
		// package doc.
		ProjectDir:       pathx.ExpandTilde(m.dir.Value()),
		Title:            m.title.Value(),
		Branch:           m.worktree.Branch(),
		BaseRef:          m.worktree.Base(),
		UseWorktree:      m.worktree.Enabled() && m.worktree.On(),
		IsGitRepo:        m.worktree.Enabled(), // WorktreeField.Enabled() IS "is the target a git repo" (its own doc comment).
		Placement:        m.placement.Value(),
		AgentKind:        m.agent.Value(),
		ExtraArgs:        m.cfg.Agents.ExtraArgs[m.agent.Value()],
		AccountPin:       m.accountPin(),
		Prompt:           m.prompt.Value(),
		Ctx:              m.ctx,
		DetectionTimeout: time.Duration(m.cfg.Timeouts.DetectionMS) * time.Millisecond,
		PromptTimeout:    time.Duration(m.cfg.Timeouts.PromptWaitMS) * time.Millisecond,
		// TrustRepository has no config.toml key today: spec §9 mentions
		// "--trust-repository per config" but internal/config.Config never
		// gained a matching field across Tasks 1-20 (config.go's own
		// struct has no trust_repository/[worktree] table at all). Always
		// false until that config gap is filled -- flagged in this task's
		// own report as a real, disclosed limitation, not invented here.
		TrustRepository: false,
	}
}

// startSubmit begins the staged plan.Execute run for ops (already built
// from in): seeds SubmitView with one StepPending row per op -- so the
// user sees the full staged checklist immediately, not just whichever
// step happens to be running, since plan.Execute's own onProgress never
// itself emits a StepPending event for a step that hasn't started yet --
// then hands off to runSubmitCmd (async.go) for the actual streamed
// execution.
func (m Model) startSubmit(ops []plan.Op, in plan.Input) (Model, tea.Cmd) {
	m.submitting = true
	m.submitInput = in
	m.submitProgress = make([]plan.Progress, len(ops))
	for i, op := range ops {
		m.submitProgress[i] = plan.Progress{Index: i, Total: len(ops), Label: op.Label, State: plan.StepPending}
	}
	m.submitView = form.NewSubmitView(m.palette)
	m.submitView.SetProgress(m.submitProgress)
	return m, runSubmitCmd(context.Background(), m.deps.Runner, ops)
}

// handleClearRequested implements form.ClearRequestedMsg (spec §6's ⌃R⌃R
// double-tap, Task 20's own documented no-op gap -- see
// task-20-report.md's Concerns #2, and this task's own brief: "reset the
// fields to their startup/seeded state (config defaults + context-derived
// values), not to empty zero values"): rebuilds the form from scratch via
// New, reusing every already-fetched async result this Model currently
// holds (workspaces, clauthStatus, linearIssues) exactly as Bootstrap's
// own first call to New would have, rather than resetting to New's own
// empty-Setup zero values. New performs no I/O, so this is safe to call
// synchronously here; the returned Model's own Init() reproduces exactly
// the same debounced dir/base checks and Linear refresh a real form-open
// schedules, which is what re-seeds candidate lists/verdicts rather than
// leaving them stale from the discarded Model.
//
// Fix round 1 (reviewer finding, empirically confirmed: a 4496-byte view
// before Clear, 0 bytes after): New's own Model -- and, more precisely,
// the form.Model nested inside it -- both start with a zero-value 0x0
// width/height, set only by a real tea.WindowSizeMsg arriving through
// Update. A real terminal only ever sends that message on startup/resize,
// never on a Clear, so the ORIGINAL version of this method (just `return
// fresh, fresh.Init()`) rebuilt a Model that would render "" until the
// next real resize -- field values reset correctly, but the screen went
// blank, defeating the whole feature. The fix replays the CURRENT size
// (m.width/m.height, this Model's own top-level copy, captured by every
// Update call -- see their own doc comment) through fresh.Update as a
// synthesized tea.WindowSizeMsg: routed through Update's own top-level
// capture (sets fresh's copy) and, since fresh isn't submitting yet,
// routeToForm's default case (forwards to fresh.form.Update, which sets
// the nested form.Model's own private width/height) -- the same two
// places a real WindowSizeMsg would reach, just synthesized instead of
// waited for.
func (m Model) handleClearRequested() (Model, tea.Cmd) {
	fresh := New(Setup{
		Deps:         m.deps,
		Ctx:          m.ctx,
		Config:       m.cfg,
		State:        m.state,
		Palette:      m.palette,
		StateDir:     m.stateDir,
		Workspaces:   m.workspaces,
		ClauthStatus: m.clauthStatus,
		LinearCache:  m.linearIssues,

		LinearUnavailable: m.linearUnavailable,
	})
	next, sizeCmd := fresh.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
	fresh = next.(Model)
	return fresh, tea.Batch(fresh.Init(), sizeCmd)
}

// reactToChanges diffs every getter this package needs to react to against
// its own last-observed snapshot, scheduling whatever debounced async work
// (async.go) a real change calls for, and unconditionally re-syncing the
// cheap, synchronous dynamic-inertness fields (syncDerivedInertness) --
// this is the one-level-up analogue of every Section's own before/after
// Value() comparison in its Update (field_dir.go's DirField.Update,
// field_title.go's TitleField.Update, ...), since form.Model itself
// exposes no "section X's value changed" signal of its own.
func (m *Model) reactToChanges() []tea.Cmd {
	var cmds []tea.Cmd

	if typed := m.dir.Typed(); typed != m.lastDirTyped {
		m.lastDirTyped = typed
		cmds = append(cmds, m.reactToTypedDir(typed)...)
	}

	dirVal := m.dir.Value()
	if dirVal != m.lastDir {
		m.lastDir = dirVal
		cmds = append(cmds, m.scheduleDirCheck(dirVal), m.scheduleBaseCheck(dirVal))
	}

	titleVal := m.title.Value()
	if titleVal != m.lastTitle {
		m.lastTitle = titleVal
		if !m.linearIssueSelected {
			// Manual mode (spec §6 field 3): "choosing a title is choosing
			// a branch" -- WorktreeField.SetBranch's own touched guard
			// means this is a no-op the moment the user has edited Branch
			// themselves.
			m.worktree.SetBranch(gitx.BranchSlug(m.cfg.BranchPrefix, titleVal), true)
		}
	}

	branchVal := m.worktree.Branch()
	worktreeOn := m.worktree.On()
	dupKey := titleVal + "\x00" + branchVal + "\x00" + dirVal
	if dupKey != m.lastDupKey || worktreeOn != m.lastWorktreeOn {
		m.lastDupKey = dupKey
		cmds = append(cmds, m.scheduleTitleCheck(titleVal, branchVal, dirVal, worktreeOn))
	}

	if focusedID := m.form.FocusedID(); focusedID != m.lastFocusedID {
		m.lastFocusedID = focusedID
		if focusedID == "account" && m.account != nil {
			cmds = append(cmds, m.reloadClauthCmd())
		}
	}

	m.syncDerivedInertness()
	return cmds
}

// reactToTypedDir keeps DirField's candidate pool in step with what the
// user is typing into the Project field (spec §6 field 2's dual mode):
// the fixed project list while the text reads as a fragment, the browsed
// directory's children while it reads as a path.
//
// DirField itself owns the RANKING in both modes and never reads the
// filesystem; this is the half that decides WHICH pool it ranks. The
// three transitions:
//
//   - fragment -> path: drop the pool immediately, so the rows never show
//     unrelated project directories ranked by basename during the ~150ms
//     before the first listing lands. What the user sees meanwhile is the
//     literal-path fallback row alone, which is the honest answer.
//   - path -> path, same parent: nothing. Typing "he" after "~/Projects/"
//     re-ranks the listing DirField already holds -- atrium's own
//     per-directory memoization, expressed here as a diff on browseDir
//     rather than a cache inside the field.
//   - path -> fragment: put the project pool back, and invalidate any
//     listing still in flight (bumping the browse counter) so a slow
//     os.ReadDir cannot land afterwards and clobber it.
func (m *Model) reactToTypedDir(typed string) []tea.Cmd {
	if !form.LooksLikePath(typed) {
		if m.browseDir == "" {
			return nil // already in fragment mode; the pool is already right
		}
		m.browseDir = ""
		m.browseReqVersion++
		m.supplyDirCandidates(m.projectCandidates)
		return nil
	}

	dir, _ := form.SplitPath(typed)
	if m.browseDir == dir {
		return nil
	}
	entering := m.browseDir == ""
	m.browseDir = dir
	if entering {
		m.supplyDirCandidates(nil)
	}
	return []tea.Cmd{m.scheduleBrowse(dir)}
}

// supplyDirCandidates hands DirField a new candidate pool under the next
// version of the single monotonic counter every such call shares -- see
// dirCandVersion's own doc comment for why there is only one.
func (m *Model) supplyDirCandidates(candidates []string) {
	m.dirCandVersion++
	m.dir.SetCandidates(m.dirCandVersion, candidates)
}

// syncDerivedInertness re-applies PlacementField's/AccountField's own
// DYNAMIC inert conditions (spec §6 fields 5 and 7 -- "inert while X",
// checked continuously, unlike the STATIC preconditions that gate whether
// a field is constructed at all) from WorktreeField's/AgentField's current
// state. Cheap and synchronous (no I/O), so it is safe to call
// unconditionally on every reactToChanges pass and from the async
// dirResultMsg handler (which moves WorktreeField.On() outside of
// reactToChanges' own diff, via SetOn -- see handleDirResult), rather than
// needing its own diff-gating.
func (m *Model) syncDerivedInertness() {
	m.placement.SetWorktreeOn(m.worktree.On())
	m.lastWorktreeOn = m.worktree.On()
	if m.account != nil {
		m.account.SetAgentIsClaude(m.agent.Value() == claudeKind)
	}
}

// placementConfigValue is placementFromConfigValue's inverse: it names a
// plan.Placement in spec §12's own config.toml vocabulary, for writing
// into last-used.json (persistStateCmd, async.go). Round-tripping through
// the SAME vocabulary the config file uses is what lets New apply
// State.LastPlacement through placementFromConfigValue, with no second
// serialization format for the same three values.
func placementConfigValue(p plan.Placement) string {
	switch p {
	case plan.PlacementTabHere:
		return "tab-here"
	case plan.PlacementSplitHere:
		return "split-here"
	default:
		return "new-space"
	}
}

// placementFromConfigValue translates spec §12's config.toml
// `default_placement` string ("new-space"/"tab-here"/"split-here") to
// internal/plan's own Placement enum. ok is false for "" (config omitted
// the key) and "new-space" (PlacementField's own natural starting
// default already matches it, so New has nothing to apply) alike, and
// for any unrecognized value (never override the field's default with a
// guess at a typo'd config string) -- only "tab-here"/"split-here" (the
// two placements PlacementField does NOT already start on) return true.
func placementFromConfigValue(s string) (plan.Placement, bool) {
	switch s {
	case "tab-here":
		return plan.PlacementTabHere, true
	case "split-here":
		return plan.PlacementSplitHere, true
	default:
		return plan.PlacementNewSpace, false
	}
}

// View renders the form and enables the popup chrome bubbletea v2 controls
// entirely through the returned tea.View (v2.0.8 has no
// tea.WithAltScreen()/mouse-enabling tea.NewProgram option at all --
// verified directly against charm.land/bubbletea/v2@v2.0.8's options.go,
// which defines no such option; AltScreen and MouseMode are both fields on
// tea.View instead, per tea.go's own doc comments). MouseMode is set to
// tea.MouseModeAllMotion, the exact setting task-2b's live probe already
// confirmed working end to end (a raw SGR click and a wheel event were
// both received and rendered inside a herdr popup under this mode) --
// spec §7 calls for click-to-focus/activate plus wheel-scroll, which
// MouseModeCellMotion alone would already cover, but AllMotion additionally
// reports hover/motion events with no button held, which Task 21's own
// mouse-driven hit-testing may want for focus-follows-hover or drag
// affordances; there is no discovered downside to enabling it now.
//
// Once m.submitting is true (form.SubmitMsg's own validation has passed --
// see handleSubmit), this renders m.submitView instead of the form: spec
// §9's staged-progress/keep-or-clean display replaces the form entirely
// rather than appearing alongside it, matching SubmitView's own "not a
// form.Section" design (submitview.go's file doc comment). AltScreen/
// MouseMode stay the same either way -- this is still the same popup, just
// showing a different pure view over it.
func (m Model) View() tea.View {
	var v tea.View
	if m.submitting && m.submitView != nil {
		v = tea.NewView(m.submitView.ViewAt(m.width, m.height))
	} else {
		v = m.form.View()
	}
	v.AltScreen = true
	v.MouseMode = tea.MouseModeAllMotion
	return v
}

// --- construction helpers -------------------------------------------------

// orderedAgentKinds builds AgentField.SetKinds' own input list: favorites
// first (deduped, empty entries dropped), then every knownAgentKinds entry
// not already present -- the carried requirement's exact wording ("config
// [agents] favorites, then the remaining kinds"). AgentField itself treats
// index 0 as spec §12's own configured default, so favorites[0] (when
// favorites is non-empty) IS the default; config.Config's own defaults()
// already sets Agents.Favorites to ["claude"] when the user's config omits
// it entirely, so this is never called with an empty list in practice.
func orderedAgentKinds(favorites []string) []string {
	seen := make(map[string]bool, len(favorites)+len(knownAgentKinds))
	out := make([]string, 0, len(favorites)+len(knownAgentKinds))
	for _, k := range favorites {
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	for _, k := range knownAgentKinds {
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	return out
}

// defaultProjectDir resolves spec §6 field 2's "Default: current space's
// repo root" -- the worktree's own repo root when the invoking context is
// itself a worktree, falling back to the plain workspace cwd otherwise.
func defaultProjectDir(ctx herdrc.Context) string {
	if ctx.Worktree != nil && ctx.Worktree.RepoRoot != "" {
		return ctx.Worktree.RepoRoot
	}
	return ctx.WorkspaceCwd
}

// buildDirCandidates builds DirField's initial candidate pool (spec §6
// field 2): current context cwd/repo root, then open herdr workspace
// cwds/repo roots (only worktree-backed workspaces carry a resolvable cwd
// at all -- herdrc.WorkspaceInfo has no plain Cwd field for a non-worktree
// workspace, a real data-shape limitation, not an oversight; see
// task-20-report.md), then recents (state dir). DirField.SetCandidates
// applies its own dedupePaths, so duplicates across these three sources
// (e.g. the current workspace appearing both as defaultProjectDir and in
// Workspaces) collapse to their first occurrence, preserving order.
func buildDirCandidates(ctx herdrc.Context, workspaces []herdrc.WorkspaceInfo, recents []string) []string {
	var out []string
	if d := defaultProjectDir(ctx); d != "" {
		out = append(out, d)
	}
	if ctx.WorkspaceCwd != "" {
		out = append(out, ctx.WorkspaceCwd)
	}
	for _, ws := range workspaces {
		if ws.Worktree == nil {
			continue
		}
		switch {
		case ws.Worktree.CheckoutPath != "":
			out = append(out, ws.Worktree.CheckoutPath)
		case ws.Worktree.RepoRoot != "":
			out = append(out, ws.Worktree.RepoRoot)
		}
	}
	out = append(out, recents...)
	return out
}

// renderPromptTemplate composes a chosen Linear issue's seeded prompt text
// (spec §10), using tmpl (config.Config.Linear.PromptTemplate) when
// non-empty, or defaultPromptTemplate otherwise.
func renderPromptTemplate(tmpl string, iss linear.Issue) string {
	if tmpl == "" {
		tmpl = defaultPromptTemplate
	}
	r := strings.NewReplacer(
		"{identifier}", iss.Identifier,
		"{title}", iss.Title,
		"{url}", iss.URL,
		"{description}", iss.Description,
	)
	return r.Replace(tmpl)
}
