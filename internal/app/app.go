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
// Task 20 stops short of the submit pipeline (spec §9's staged creation):
// plan.Build/Execute wiring is Task 20b's job. app.Model is nonetheless the
// seam that work extends from -- form.SubmitMsg (emitted by form.Model on
// Enter/Ctrl+S) is already intercepted in Update below, just not yet acted
// on; see the case comment there.
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
// method here is a thin wrapper over internal/gitx plus os.Stat for
// DirExists (gitx itself has no directory-existence helper).
type gitSource interface {
	DirExists(path string) bool
	IsGitRepo(dir string) bool
	ListBranches(ctx context.Context, dir string, limit int) ([]string, error)
	BranchExists(ctx context.Context, dir, name string) (bool, error)
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

	var linearSrc linearSource
	var linearCache []linear.Issue
	if key, kerr := linear.ResolveAPIKey(cfg.Linear.APIKeyCmd, cfg.Linear.APIKey, env.ConfigDir); kerr == nil && key != "" {
		linearSrc = &linear.Client{APIKey: key}
		if cached, _, cerr := linear.LoadCache(env.StateDir); cerr == nil {
			linearCache = cached
		}
	}
	// A ResolveAPIKey error (e.g. a broken api_key_cmd) is treated the same
	// as "no key resolved": the Linear field is simply not rendered (spec
	// §6 field 1's own "absent -> not rendered" contract), rather than
	// refusing the whole plugin over an optional integration's
	// misconfiguration.

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
		Deps:         deps,
		Ctx:          ctx,
		Config:       cfg,
		State:        state,
		Palette:      palette,
		StateDir:     env.StateDir,
		Workspaces:   workspaces,
		ClauthStatus: clauthStatus,
		LinearCache:  linearCache,
	}), nil
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
	lastTitle      string
	lastDupKey     string
	lastWorktreeOn bool
	lastFocusedID  string

	// request-version counters -- see async.go's request type and the
	// schedule*/handle* pair for each source.
	dirReqVersion   int
	baseReqVersion  int
	titleReqVersion int
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
		fetchedRepos: map[string]bool{},
	}

	m.dir = form.NewDirField(palette)
	m.title = form.NewTitleField(palette)
	m.worktree = form.NewWorktreeField(palette)
	m.placement = form.NewPlacementField(palette)
	m.agent = form.NewAgentField(palette)
	m.prompt = form.NewPromptField(palette)

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
	}

	// Agent (spec §6 field 6, carried requirement): favorites first, then
	// every remaining known kind, so a favorite is always a chip AND the
	// full list stays reachable behind "more…" (AgentField's own doc: it
	// derives its favorite chips from THIS list's leading entries).
	m.agent.SetKinds(orderedAgentKinds(s.Config.Agents.Favorites))

	// Project (spec §6 field 2): current space's repo root, then the
	// current workspace cwd, then every open workspace's own worktree
	// root, then recents -- DirField.SetCandidates selects candidates[0]
	// as the initial selection (widgets.Picker's own same-version
	// preserve-by-ID/fallback-to-index-0 behavior on a picker with no
	// prior selection -- see task-20-report.md for the full trace), so
	// ordering IS the default.
	m.dir.SetCandidates(1, buildDirCandidates(s.Ctx, s.Workspaces, s.State.Recents))
	m.lastDir = m.dir.Value()

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
	if m.issue != nil {
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

// Update dispatches an incoming message: the app-level messages this
// package itself defines or intercepts (form.IssueChosenMsg seeding,
// form.CancelMsg/SubmitMsg/ClearRequestedMsg, and every async.go debounce/
// result message) are handled directly; everything else is routed through
// form.Model's own Update (routeToForm), which also runs reactToChanges
// afterward to notice any value change that needs its own debounced
// reaction or dynamic-inertness sync.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case form.IssueChosenMsg:
		return m.handleIssueChosen(msg)
	case form.CancelMsg:
		return m, tea.Quit
	case form.SubmitMsg:
		// Task 20b wires spec §9's submit pipeline (validation, then
		// plan.Build/Execute) here. Task 20's own scope stops at
		// validation STATE (the inline verdicts this package already
		// computes and pushes into TitleField/AccountField); it does not
		// act on a submit attempt.
		return m, nil
	case form.ClearRequestedMsg:
		// Rebuilding the form to its default state (form.go's own doc
		// comment: "rebuilding the form... is the app layer's job") is
		// deferred past Task 20 -- see task-20-report.md's concerns
		// section for why (rebuilding would need to safely re-apply every
		// already-fetched async result -- candidates, issues, clauth
		// profiles -- onto fresh field instances, which is real work this
		// task's explicit responsibility list doesn't name).
		return m, nil
	case dirDebounceMsg:
		return m.handleDirDebounce(msg)
	case dirResultMsg:
		return m.handleDirResult(msg)
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
func (m Model) View() tea.View {
	v := m.form.View()
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
