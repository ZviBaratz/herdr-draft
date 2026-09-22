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
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/ZviBaratz/herdr-draft/internal/agentopts"
	"github.com/ZviBaratz/herdr-draft/internal/clauth"
	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/defaults"
	"github.com/ZviBaratz/herdr-draft/internal/form"
	"github.com/ZviBaratz/herdr-draft/internal/gitx"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
	"github.com/ZviBaratz/herdr-draft/internal/linear"
	"github.com/ZviBaratz/herdr-draft/internal/pathx"
	"github.com/ZviBaratz/herdr-draft/internal/picker"
	"github.com/ZviBaratz/herdr-draft/internal/plan"
	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// claudeKind is the only AgentField value for which clauth account pinning
// applies (spec §6 field 7), mirrored from internal/plan/build.go's own
// unexported claudeAgentKind -- kept as this package's own small constant
// rather than exporting plan's, since app has no other reason to import
// plan in Task 20 (submit/plan.Build wiring is Task 20b's job).
const claudeKind = "claude"

// formName is the header's left-hand text (v2 spec §4). Lowercase, like
// every other label in the form and like herdr's own modal headers (v2
// spec §7).
const formName = "new session"

// knownAgentKinds is herdr's full 23-kind agent list (spec §6 field 6:
// "full kind list (herdr's 23)"), translated from
// https://github.com/herdrdev/herdr/blob/b1ff4582/src/detect/mod.rs's `Agent::ALL` /
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

// Clock groups the time primitives the app layer needs from outside
// itself, so tests can run the 150ms debounce window without a real sleep
// and pin a wall clock without a real one. The zero value is
// production-ready (each nil field defaults to its time package
// equivalent).
type Clock struct {
	Sleep func(time.Duration)
	// Now is the wall clock. It exists for AccountField, whose panel shows
	// how long until each clauth usage window resets (v3 spec §10.2) --
	// internal/form performs no I/O, so the app layer supplies the "now"
	// those relative times are measured against, alongside the status they
	// come from. Injectable for the same reason Sleep is: a golden frame
	// showing `in 2h11m` has to be the same bytes tomorrow.
	Now func() time.Time
}

func (c Clock) sleep(d time.Duration) {
	if c.Sleep != nil {
		c.Sleep(d)
		return
	}
	time.Sleep(d)
}

func (c Clock) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// linearSource is the subset of Linear access Model needs -- satisfied by
// *linear.Client in production and a fake in tests.
//
// nil no longer means "Linear isn't configured": since #293 it means no key
// has RESOLVED, which with an api_key_cmd is the ordinary state of the
// first frame (Model.linearResolving). Whether Linear is configured is a
// separate, pure question answered before the draw
// (linear.KeySourceConfigured), and it is the one that decides whether the
// issue row exists at all.
type linearSource interface {
	AssignedIssues(ctx context.Context) ([]linear.Issue, error)
}

// clauthSource is the subset of clauth access Model needs -- satisfied by
// clauthLoader (wrapping clauth.Load, see NewClauthSource) in production
// and a fake in tests.
// pickerSource is the account-picker access Model needs -- satisfied by
// picker.CLI in production (see NewPickerSource) and by a fake in tests, so no
// test in this package ever runs a subprocess.
type pickerSource interface {
	Pick(ctx context.Context, dir string, opts picker.Options) (picker.Result, error)
}

type clauthSource interface {
	Status(ctx context.Context) (clauth.Status, error)
	// Peek is Status' fast half: clauth's on-disk status file, and, when
	// that cannot answer, whether there is a clauth to ask at all. It runs
	// no subprocess and takes no context, which is the whole reason it
	// exists -- Bootstrap calls it before the first frame and leaves the
	// CLI run to Init (draw-first spec §3.2).
	//
	// internal/create's own one-method ClauthSource deliberately does NOT
	// gain it: `create` has no draw to get in front of, so it keeps
	// calling Status.
	Peek() (clauth.Status, clauth.Cached)
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
	// RepoRoot resolves the ORIGIN repository root behind dir -- the key
	// v2 spec §10's per-project memory is stored under, so every worktree of
	// one repository shares a single entry. ("", nil) for a plain
	// directory; see gitx.RepoRoot.
	RepoRoot(ctx context.Context, dir string) (string, error)
	// PrimaryCheckout is the repository's primary checkout when dir is a
	// linked worktree checkout, "" otherwise (gitx.PrimaryCheckout), and
	// ResolveCommit the commit ref names in dir (gitx.ResolveRef): together,
	// what a worktree session from a lane needs (#171).
	PrimaryCheckout(ctx context.Context, dir string) (string, error)
	ResolveCommit(ctx context.Context, dir, ref string) (string, error)
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
	// Linear is nil until an API key has resolved -- which, with a
	// configured api_key_cmd, is after the first frame (draw-first spec
	// §5.1). So nil no longer means "Linear isn't configured": it means no
	// key has resolved YET, and the three Setup/Model flags beside it say
	// which of the three reasons that is. See Bootstrap.
	Linear linearSource
	Clauth clauthSource
	// Picker is nil when `[clauth] picker` names no executable -- the normal
	// case -- OR when the one it named failed its probe. Non-nil means an
	// executable was named and has not (yet) failed the protocol check;
	// only then does the account row grow an `auto` selection.
	//
	// Since #293 it starts non-nil and UNVETTED: the probe is merged into
	// the opening `--dry-run` preview, so the verdict lands after the first
	// frame and can set this back to nil (draw-first spec §3.3, §5.4).
	// Model.pickerProbePending is what says the answer is still out, and
	// a submit resting on `auto` waits for it (§7.1). See Bootstrap.
	Picker pickerSource
	Git    gitSource
	Clock  Clock
	// Lifetime bounds the off-model work this Model starts against the
	// PROCESS's own life: the account picks and the lane's base run on its
	// context, so quitting the popup kills a picker mid-pick rather than
	// leaving it to write a ledger entry for a session nobody created
	// (#211). nil -- every Model a test builds without one -- means
	// context.Background(), exactly as this behaved before.
	Lifetime *Lifetime
	// RepoConfig reads v2 spec §11's committed .herdr-draft.toml from a
	// repository root. nil means config.LoadRepoConfig, the production
	// reader -- it is a func rather than an interface for the same reason
	// Clock.Sleep is: one call, no state, and a test that needs a
	// deterministic answer should not have to put a real file on disk to
	// get one.
	//
	// It is grouped here, with the other I/O collaborators, rather than
	// called directly from async.go, because this package's own rule is
	// that no test in it performs real I/O against something it did not
	// create.
	RepoConfig func(repoRoot string) config.RepoConfig
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
	Deps    Deps
	Ctx     herdrc.Context
	Config  config.Config
	State   config.State
	Palette theme.Palette
	// Projects is projects.json (v2 spec §10's per-project memory), loaded
	// once at startup. Its zero value means "no memory yet", which is what
	// a first run, an unreadable file and an unknown schema version all
	// look like -- see config.LoadProjects.
	Projects     config.Projects
	StateDir     string
	Workspaces   []herdrc.WorkspaceInfo
	ClauthStatus clauth.Status
	LinearCache  []linear.Issue
	// HomeDir is the string DirField collapses to "~" when it displays a
	// path (v2 spec §4). It is passed in rather than read here for the
	// same reason every other tier is -- New performs no I/O -- and, more
	// pointedly, because a golden frame that consulted os.UserHomeDir
	// would render differently on every machine whose home is not the
	// author's. Bootstrap fills it from pathx.Home; "" disables
	// collapsing.
	HomeDir string
	// LinearUnavailable is non-empty when Linear is CONFIGURED but its API
	// key could not be resolved (a broken [linear] api_key_cmd, an
	// api_key_cmd that printed nothing with nothing behind it, or an
	// inline api_key in a config.toml wider than 0600). Distinct from
	// Deps.Linear == nil AND LinearResolving false, which together mean
	// Linear is not configured at all: the first renders spec §6 field 1's
	// own field carrying this reason -- present-but-inert with no cache,
	// and a pickable cached list with one (#292) -- and the second renders
	// no field at all. See Bootstrap.
	LinearUnavailable string

	// LinearResolving says a [linear] api_key_cmd is configured, so the key
	// lands after the first frame (draw-first spec §4.1). The command is
	// the first source ResolveAPIKey tries, so a configured one makes the
	// whole resolve asynchronous whatever else is set.
	//
	// It is the issue row's third reason to exist, beside a resolved source
	// and a failure: the row is live, the cached list is pickable, and the
	// panel says what is being waited for.
	LinearResolving bool

	// ClauthLoading says clauth's status file was not fresh and there is a
	// clauth on PATH to ask (clauth.Peek's CachedAsk). The account row is
	// drawn `loading…` and Init carries the CLI read (draw-first spec
	// §3.2, §5.3).
	ClauthLoading bool

	// PickerProbePending says Deps.Picker is the executable `[clauth]
	// picker` named, not yet vetted: the probe rides on the opening
	// preview and its verdict lands after the first frame (draw-first spec
	// §3.3, §5.4).
	PickerProbePending bool
	// ClauthUnavailable is non-empty when clauth is INSTALLED but could
	// not be read -- it exited non-zero, or its --json output did not
	// parse. Distinct from clauth being absent, which is the common case
	// and renders no account row at all (clauth.Peek's CachedAbsent, which
	// reads exec.ErrNotFound off a LookPath the way Bootstrap used to read
	// it off a failed run).
	//
	// When set, the account row renders present-but-inert carrying this
	// reason, exactly as LinearUnavailable does for the issue row.
	//
	// Since #293 Bootstrap never sets it: it runs no clauth, so the reason
	// can only arrive after the first frame, through recoverAccountRow.
	// It stays on Setup because a ⌃R⌃R rebuild reconstructs the row from
	// the Model's mirror of it -- and it is also where a reload's "fewer
	// than two profiles" lands, which used to be a state only a reload
	// could produce and is now an ordinary open-time answer (decision 2).
	ClauthUnavailable string

	// PickerUnavailable is non-empty when `[clauth] picker` NAMED an
	// executable that could not be trusted: it would not run, or its one probe
	// answer did not implement the protocol. Distinct from no picker being
	// configured, which is the common case and renders no auto row.
	//
	// When set and the account row renders, the row carries this reason as its
	// first note, which is the whole point: a picker that was configured and
	// is not working must not look identical to one that was never
	// configured. With no working account row (clauth absent or unreadable, or
	// fewer than two profiles) it is shown nowhere, deliberately: the popup can
	// pin no account there, so no picker could have been used.
	PickerUnavailable string

	// reqs is where every request counter resumes. Only a ⌃R⌃R rebuild
	// sets it (handleClearRequested, through reqVersions.superseded), past
	// the discarded form's own counters: a check that form still has in
	// flight lands after the rebuild, and a counter it can still meet
	// passes another value's answer off as the fresh form's own, releasing
	// a submit held for it (#195, #201). See reqVersions.
	reqs reqVersions
}

// Bootstrap performs spec §9's pre-open refusal plus every other piece of
// startup work that must finish before the form can render: parsing the
// plugin invocation context, probing herdr reachability (a single `herdr
// workspace list` call doubles as both the reachability probe and the
// source of the workspace label list/candidate repo roots New needs),
// loading config/state/palette, reading the Linear cache and clauth's
// status file -- then hands all of it to New.
//
// BEFORE THE DRAW IT RUNS NO SUBPROCESS BUT `herdr workspace list`
// (draw-first spec §3.1). That is the whole of #293. It used to run three
// more -- a [linear] api_key_cmd at up to 60s, `clauth status --json` at
// 30s when clauth's status file was stale, and an account picker's probe
// at 30s -- each of them synchronously, with the popup pane blank and
// silent for however long they took. #141 bounded them, which decided when
// to give up and nothing else; a person whose `op read` takes fifteen
// seconds to approve still watched an empty pane for fifteen seconds on
// every open.
//
// So the three move into Init, and what stays here is what a local read
// can answer: whether a key SOURCE is configured (pure), the cached issue
// list, and clauth's status file plus a PATH check (clauth.Peek). Each
// leaves a flag on Setup saying an answer is still coming, and New draws a
// row that says so. The row SET is still decided here, before the first
// frame -- only what a row says changes afterwards (§4).
//
// Only three conditions refuse outright (spec §9 names two: "herdr socket
// unreachable -> plain-text error and exit", "on missing/invalid context"):
// an invalid $HERDR_PLUGIN_CONTEXT_JSON, an unreachable herdr, or a
// config.toml that fails to parse (extended from the spec's literal two
// cases: proceeding
// with config.Load's own zero-value Config on a parse error -- as opposed
// to its documented defaults() -- would silently corrupt every config-
// derived default the form has, which a clear stderr message is far
// preferable to). Every other failure below (a broken Linear api_key_cmd,
// a clauth status load failure, cache load failures) degrades gracefully
// per spec §13 ("Linear/clauth/network failures degrade the respective
// field to inert with a reason; they never block manual-mode creation")
// rather than refusing.
//
// lt is the process's Lifetime (#211), threaded into Deps so the commands
// the form starts later run on a context quitting cancels. Bootstrap's own
// startup calls deliberately do not: nothing can quit before the form is on
// screen, and a refusal here is reported and returned rather than waited on.
// nil is accepted and means uncancellable, which is what this package's own
// tests want.
func Bootstrap(env Env, runner herdrc.Runner, clauthSrc clauthSource, gitSrc gitSource, clock Clock, lt *Lifetime) (Model, error) {
	ctx, err := herdrc.ParseContext(env.ContextJSON)
	if err != nil {
		return Model{}, refusalContext(err)
	}

	bg := context.Background()
	workspaces, err := runner.WorkspaceList(bg)
	if err != nil {
		return Model{}, refusalHerdrUnreachable(err)
	}

	cfg, err := config.Load(env.ConfigDir)
	if err != nil {
		return Model{}, err
	}

	// Neither loader ever returns a non-nil error (spec §12: state is
	// entirely loss-tolerant) -- the error returns only exist for API
	// symmetry.
	state, _ := config.LoadState(env.StateDir)
	projects, _ := config.LoadProjects(env.StateDir)

	palette := theme.LoadHerdrPalette(cfg.Palette)

	// Linear, in two halves since #293. The question "is a key source
	// configured" is pure -- linear.KeySourceConfigured reads the same
	// three sources ResolveAPIKey does, without running any of them -- and
	// it alone decides whether the issue row exists (draw-first spec §3.1).
	//
	// The CACHE is loaded for every configured user, not only for one whose
	// key already resolved. That is decision 4 and it is #292: the cached
	// list used to be reached inside `case key != ""` alone, so a key that
	// failed threw away a perfectly pickable list sitting two lines from
	// where it was read.
	//
	// Then which of the two paths the key itself takes. An api_key_cmd is
	// the first source ResolveAPIKey tries, so a configured one makes the
	// whole resolve a subprocess and it moves into Init (resolveKeyCmd,
	// appended below). With NO api_key_cmd the resolve is $LINEAR_API_KEY,
	// or the inline key plus one stat of config.toml -- no process at all
	// -- so it stays here and those users' first frame is exactly today's,
	// apart from ruling 10's `fetching…` phase.
	//
	// linear.ResolveAPIKey still distinguishes three outcomes and so does
	// this (finding I5): a key (Linear works), no key and no error (not
	// configured at all), or an ERROR raised deliberately when the user's
	// own chosen key source fails. Either way the plugin still opens -- an
	// optional integration's misconfiguration never blocks manual-mode
	// creation.
	var linearSrc linearSource
	var linearCache []linear.Issue
	var linearUnavailable string
	var linearResolving bool
	if linear.KeySourceConfigured(cfg.Linear.APIKeyCmd, cfg.Linear.APIKey) {
		if cached, _, cerr := linear.LoadCache(env.StateDir); cerr == nil {
			linearCache = cached
		}
		if len(cfg.Linear.APIKeyCmd) > 0 {
			linearResolving = true
		} else {
			key, kerr := linear.ResolveAPIKey(bg, cfg.Linear.APIKeyCmd, cfg.Linear.APIKey, env.ConfigDir)
			switch {
			case kerr != nil:
				linearUnavailable = linearUnavailableReason(kerr)
			case key != "":
				linearSrc = &linear.Client{APIKey: key}
			}
		}
	}

	clauthEnabled := true
	if cfg.Clauth.Enabled != nil {
		clauthEnabled = *cfg.Clauth.Enabled
	}
	// clauth's error used to be dropped here, which made four different
	// situations render identically -- as nothing at all, with no text
	// anywhere saying why. "clauth is not installed", "clauth has one
	// profile", "clauth crashed" and "clauth returned unparseable JSON"
	// all produced an absent account row.
	//
	// The first two SHOULD produce an absent row: that rule is deliberate
	// and documented (docs/troubleshooting.md: "Without them the form has
	// seven rows, which is by design"), and most people who install this
	// plugin have never heard of clauth. Announcing a missing optional
	// dependency to them would be noise.
	//
	// The other two are a broken integration, which spec §13 says must
	// degrade "to inert with a reason". exec.ErrNotFound is what separates
	// them: it means the binary is not there, everything else means it was
	// there and did not work.
	//
	// Since #293 this is the status FILE and a PATH check, never the CLI
	// (clauth.Peek, draw-first spec §3.2). A fresh file decides the row
	// exactly as the full read did. A stale one with clauth on PATH leaves
	// the row drawn `loading…` while Init asks the CLI, and #200's
	// recoverAccountRow -- which already turns that answer into live /
	// "too few profiles" / "failed with a reason" -- is what lands it. A
	// stale one with no clauth on PATH is the "not installed" case above,
	// and still produces no row: exec.ErrNotFound is what separated the
	// four states before, and LookPath asks the same question without
	// starting anything.
	//
	// So clauthUnavailable is no longer reachable from here at all. It
	// stays on Setup because a ⌃R⌃R rebuild reconstructs the row from the
	// Model's own mirror of it, and because this package's own tests build
	// that state directly.
	var clauthStatus clauth.Status
	var clauthLoading bool
	if clauthEnabled && clauthSrc != nil {
		st, cached := clauthSrc.Peek()
		switch cached {
		case clauth.CachedFresh:
			clauthStatus = st
		case clauth.CachedAsk:
			clauthLoading = true
		}
	}

	// A named picker is PROBED before it is trusted (#122; the protocol
	// itself is docs/account-picker.md's "The account picker protocol"). No
	// spec section carries this: the brief it was written from cited a
	// "§5.6" that exists in no document in docs/specs.
	// Naming one is the user's explicit consent to run it; the probe is what
	// checks that the thing under that name implements the protocol, because
	// a same-named stranger routing account credentials is worse than no
	// picker at all. herdr-draft never goes looking for one on PATH, which is
	// why there is no discovery step here to read.
	//
	// One --dry-run, against the directory the form will open on: by contract
	// it writes no ledger entry and builds no account directory for a session
	// nobody has asked for.
	var pickerSrc pickerSource
	var pickerProbePending bool
	if bin := strings.TrimSpace(cfg.Clauth.Picker); bin != "" && clauthEnabled {
		pickerSrc = picker.CLI{Bin: bin}
		pickerProbePending = true
	}

	deps := Deps{Runner: runner, Linear: linearSrc, Clauth: clauthSrc, Picker: pickerSrc, Git: gitSrc, Clock: clock, Lifetime: lt}
	m := New(Setup{
		Deps:               deps,
		Ctx:                ctx,
		Config:             cfg,
		State:              state,
		Projects:           projects,
		Palette:            palette,
		StateDir:           env.StateDir,
		Workspaces:         workspaces,
		ClauthStatus:       clauthStatus,
		LinearCache:        linearCache,
		LinearUnavailable:  linearUnavailable,
		LinearResolving:    linearResolving,
		ClauthLoading:      clauthLoading,
		PickerProbePending: pickerProbePending,
		HomeDir:            pathx.Home(),
	})

	// The key resolve is BOOTSTRAP'S, not New's, and that is deliberate
	// (draw-first spec §4.4). A ⌃R⌃R rebuild goes through New and never
	// through here, so `api_key_cmd` is run exactly once per popup: a
	// second `op read` is a second approval prompt, and the first one's
	// answer lands on the rebuilt form anyway (the message carries no
	// version -- §5.1).
	//
	// The resolver is a closure over the call Bootstrap used to make
	// inline rather than a new interface on Deps: it has one caller and
	// one implementation, and resolveKeyCmd takes it as a parameter so its
	// own tests can pass a fake.
	if linearResolving {
		m.initCmds = append(m.initCmds, resolveKeyCmd(lt, func(ctx context.Context) (string, error) {
			return linear.ResolveAPIKey(ctx, cfg.Linear.APIKeyCmd, cfg.Linear.APIKey, env.ConfigDir)
		}))
	}
	return m, nil
}

// The three refusals below are, for most people who ever see one, their
// FIRST contact with this binary -- the message is the entire product at
// that moment. spec §9 settles that these three conditions refuse rather
// than open broken; what they say is a separate question, and the answer
// used to be a bare wrapped error:
//
//	herdr-draft: parse plugin context: unexpected end of JSON input
//
// That is what running the binary from a shell printed. It does not say
// the program expects to be launched by herdr, does not name the variable
// it wanted, and does not say what to run instead. The word "plugin" is
// the only clue.
//
// The headless verb already had the better instinct -- usablePluginEnv's
// stderr lines name the variable, name the command that sets it, and say
// what is lost without it -- so these follow its example rather than
// inventing a style. Each names what was wrong, then what to do.

// refusalContext explains an unusable $HERDR_PLUGIN_CONTEXT_JSON.
//
// The unset case gets the long answer because it is not really an error at
// all: it is a person running the binary directly to see what it does, and
// what they need is orientation. A MALFORMED context is a different
// situation -- something did set the variable, so the reader is already
// inside a herdr context and does not need to be told what herdr is.
func refusalContext(err error) error {
	if !errors.Is(err, herdrc.ErrContextUnset) {
		return err
	}
	return fmt.Errorf(`%w

herdr-draft is a herdr plugin: herdr launches it in a popup pane and passes
the invocation context in that variable. There is nothing to show without
it, so this is not a program to run directly.

  open the popup:      herdr %s
  or skip the popup:   herdr-draft create --title "..."
  the flags:           herdr-draft create --help

See %s`,
		err,
		"plugin pane open --plugin "+herdrc.PluginID+" --entrypoint open",
		readmeURL)
}

// refusalHerdrUnreachable explains a herdr that did not answer.
//
// It deliberately does NOT tell the user to start herdr: the overwhelmingly
// likely reader is sitting inside a running herdr, which is the only place
// the popup opens from. A socket pointing somewhere else, or a server that
// has been restarted under a stale environment, fits the evidence far
// better -- and is the case that produces a bare non-zero exit with no
// stderr at all, which is why cmdError stopped printing a dangling colon
// for it.
func refusalHerdrUnreachable(err error) error {
	return fmt.Errorf(`herdr unreachable: %w

herdr-draft drives herdr through its CLI, so it cannot open without one
answering. $HERDR_BIN_PATH is the binary it runs and $HERDR_SOCKET_PATH is
the server it expects to reach; a plugin launched by herdr gets both.

A protocol mismatch reports itself here too: an upgraded herdr binary
talking to a server still running the old one needs the server restarted
before either will work.`, err)
}

// readmeURL is where a refusal sends someone who wants the whole picture.
const readmeURL = "https://github.com/ZviBaratz/herdr-draft#readme"

// populateAccountRow fills a LIVE account row from what this Model
// currently holds -- the clauth status, the picker, `[clauth] default` and
// the standing notes -- in the one order they may be applied in.
//
// It has two callers and they must not drift: New, for a row built live at
// open, and async.go's recoverAccountRow, for a row whose unavailable OR
// LOADING state a successful read has just cleared (#200, #293). "The row
// becomes live and can be pinned, as it would have been had clauth
// answered at open" is the whole of that decision, and one sequence in one
// place is what makes it true of both -- everything here beyond the
// profile list (the auto row, the configured default, the notes) is a
// piece a fix that only re-fed the profiles would have left behind.
//
// Since #293 the second caller is the ordinary path rather than a
// recovery: a stale status file is how most machines without a running
// clauth daemon open, so the row most users see go live goes live here.
//
// Note what that costs: with both callers sharing this body, no test can
// catch a change made INSIDE it by comparing the two rows -- they move
// together. The golden frames are what pin the contents; the comparison
// pins the caller. See TestClauthReload_TheRecoveredRowIsTheOneOpenWouldHaveBuilt.
//
// It reads Model rather than Setup deliberately, since only one of the two
// callers has a Setup; New has already copied every field it needs
// (m.cfg/m.deps/m.clauthStatus/m.pickerUnavailable) into the Model by the
// time it calls this.
//
// NOT called for a reload of an already-live row: SetPin would put the
// configured default back over whatever the user had pinned, and that
// reload happens every single time the row takes focus.
func (m *Model) populateAccountRow() {
	// The clock rides along with the status: the panel's reset times
	// are relative (v3 spec §10.2) and internal/form has no clock of
	// its own -- see Clock.Now.
	m.account.SetProfiles(m.clauthStatus, m.deps.Clock.now())
	// The auto row: SetPickerAvailable makes auto the resting selection
	// when nothing else is pinned, since someone who configured a picker
	// configured it to be used. A `[clauth] default` naming a real profile
	// beats it, and that precedence is SetPin's own (it clears auto) rather
	// than this order's -- SetPickerAvailable only selects auto while
	// nothing is pinned, so the two lines commute. This comment used to
	// claim swapping them "silently makes the configured default lose to
	// the picker"; swapping them changes nothing, which is measured -- the
	// whole suite is green either way (independent review of #200).
	if m.deps.Picker != nil {
		m.account.SetPickerAvailable(true)
	}
	// [clauth] default (spec §12), when set to a real profile name --
	// "" and the config's own documented "active" sentinel are both
	// no-ops (AccountField.SetPin's own doc comment): the picker
	// already starts on the "active" row by construction.
	m.account.SetPin(m.cfg.Clauth.Default)
	// Everything this row has to report about how an account would be
	// launched, as standing notes rather than a verdict: a verdict is
	// keyed on one pin and hidden once the pin moves, and these matter
	// most with a profile pinned -- see AccountField.SetNotes.
	//
	// First, why there is no auto row when a picker was named and failed
	// its probe. Not SetUnavailable: clauth itself is fine and its profile
	// rows still work; only the auto row is missing. This used to be a
	// verdict keyed on the unpinned "", which a `[clauth] default` naming
	// a profile hid -- leaving a configured, broken picker looking exactly
	// like no picker at all. Then config.toml's refused [clauth] keys
	// (#123). Only a live row carries any of them because only it can
	// pin: with no working account row, nothing launches through what
	// they are about.
	m.setAccountNotes()
}

// setAccountNotes is populateAccountRow's last step on its own, because the
// probe's verdict now arrives after the first frame and has to rebuild the
// notes without rebuilding the row (draw-first spec §5.4).
//
// Only a LIVE row carries any of them, which is why populateAccountRow is
// the only other caller: with no working account row nothing launches
// through what they are about, and a loading or unavailable row draws no
// notes at all (AccountField.notesShown). Such a row gets them when it goes
// live, through populateAccountRow.
func (m *Model) setAccountNotes() {
	var notes []string
	// Only when no picker works: a working auto row beside a failed probe
	// would be two answers to one question. The verdict this replaced got
	// that from an else-branch.
	if m.pickerUnavailable != "" && m.deps.Picker == nil {
		notes = append(notes, m.pickerUnavailable)
	}
	m.account.SetNotes(append(notes, m.cfg.ClauthWarnings()...))
}

// linearUnavailableReason turns a linear.ResolveAPIKey error into the
// short line IssueField.SetUnavailable renders on its hint row. The
// package's own "resolve linear api key: " prefix is dropped -- the field
// is already labeled "Issue:" and the user is looking at the Linear field;
// repeating it costs cells the actual cause needs. It flattens, as
// clauthUnavailableReason does, because runKeyCmd puts api_key_cmd's
// stderr in the error, that stderr can be multi-line, and this is a single
// row (#323).
func linearUnavailableReason(err error) string {
	return flattenReason(strings.TrimPrefix(err.Error(), "resolve linear api key: "))
}

// clauthUnavailableReason turns a clauth load failure into the short line
// AccountField.SetUnavailable renders on its row. It drops the package's
// own "clauth status --json: " / "parse clauth status: " prefixes for the
// reason linearUnavailableReason drops its own -- the row is labeled
// `account` and the reason is competing for the same cells -- and
// flattens, because a failing clauth's stderr can be multi-line and this
// is a single row.
func clauthUnavailableReason(err error) string {
	msg := err.Error()
	for _, prefix := range []string{"clauth status --json: ", "parse clauth status: ", "clauth status: "} {
		msg = strings.TrimPrefix(msg, prefix)
	}
	return flattenReason(msg)
}

// accountTooFewProfilesReason is the account row's reason for clauth
// answering with fewer than the two profiles this row exists to choose
// between (#200).
//
// A fresh STATUS FILE saying so still produces no row at all, deliberately
// -- most people who install this plugin have never heard of clauth, and a
// row saying it has one profile is noise to every one of them (see
// Bootstrap's own comment on the four states). This is for the answer that
// arrives once the row is already on screen, which until #293 only a focus
// reload could produce and is now also the ordinary open-time read of a
// stale status file (draw-first spec §5.3, decision 2's "the row stays").
// A row that exists cannot be taken away, and the reason it was drawn with
// -- `loading…`, or a failure at open -- is false by then: clauth works,
// there is just nothing here to choose. Which of the two it is goes in the
// text because they are different situations -- profiles that went
// missing, against a clauth that only ever had one -- and this row is the
// only place either is reported.
func accountTooFewProfilesReason(n int) string {
	if n == 1 {
		return "clauth reports one profile; this row needs two"
	}
	return "clauth reports no profiles; this row needs two"
}

// flattenReason collapses a multi-line failure to one line, and drops any
// control character in it.
//
// clauthUnavailableReason above also strips that package's own error prefixes;
// this one does not, because a picker is somebody else's program and its
// message is its own -- there is no prefix herdr-draft is entitled to assume.
// Both exist for the same reason: every row this lands on has exactly one
// line, and a picker's stderr can be a paragraph.
//
// The control characters are #151's. strings.Fields already split on a CR,
// which is the one that overwrites a row, but kept an ESC or a BEL inside a
// word. form.DisplayText is the rule the form draws its own external text
// by, so a reason and a Linear title are cleaned the same way; it runs
// first, and Fields then collapses the spaces it leaves.
//
// Every reason Linear, clauth or the account picker gives a row comes
// through here, by way of the helpers around it and previewFrom, so this
// is the one place to change. A failed step's error on the submit view
// does not: that is herdr's and git's text, and #151 did not cover it.
func flattenReason(s string) string { return strings.Join(strings.Fields(form.DisplayText(s)), " ") }

// linearRefreshReason turns an AssignedIssues error into the single line
// IssueField.SetRefreshError puts on the panel's status row. Same reasoning
// as linearUnavailableReason for dropping the package's own prefix, and
// both flatten -- that sibling has since had to (#323), because a failing
// api_key_cmd's stderr can be a paragraph and the row is one line.
//
// A Linear 401 arrives as `unexpected status 401: <response body>`, and
// that body is JSON with newlines in it. The panel builds a fixed number
// of lines and a multi-line status would push the row count past the
// height Panel was asked for -- an error message that breaks the form's
// layout is a worse bug than the one being reported. flattenReason also
// collapses the runs of spaces indented JSON is full of, which matters
// when the line is about to be elided to the panel's width.
func linearRefreshReason(err error) string {
	return flattenReason(strings.TrimPrefix(err.Error(), "linear assigned issues: "))
}

// reqVersions is every request-version counter the form's async sources
// share: one monotonic number per source, compared against by that
// source's own handle* to drop the answer to a question that has been
// superseded (async.go's request type). A bump is what retires a request
// already in flight, which is the property the whole of #201 rests on.
//
// A struct rather than seven fields on Model because of the one thing
// that is done to all seven at once. ⌃R⌃R rebuilds the whole form through
// New (handleClearRequested), and a check the discarded form still has in
// flight lands afterwards -- against a counter that must already have
// moved past it. #196 and #203 did that for the project row's and the
// title check's counters one Setup field at a time, because those two hold
// a submit; #201 is the rest, as one value, so that what a rebuild does to
// a counter is decided in one place rather than per counter.
//
// What is deliberately NOT in here is the pair of landed-version fields
// beside it on Model (dirLandedVersion, titleLandedVersion). Those say
// which answer has been APPLIED to the form on screen, which a rebuilt
// form has none of -- they belong to the fields, not to the questions.
// baseSettleLanded is the exception and New says why.
type reqVersions struct {
	dir   int
	title int
	base  int
	// baseSettle is #197's, shared by the two questions about a base --
	// the one a tier supplied (scheduleBaseSettle) and the one the user
	// picked (keepChosenBaseAcrossRelist, #212). It reached this struct
	// late, in #201's own review, on the strength of the second one: the
	// issue left it out because a rebuilt form marks its base touched and
	// scheduleBaseSettle then declines to ask -- which is true, and is not
	// the whole of it, since keepChosenBaseAcrossRelist's precondition IS
	// baseTouched, so a rebuilt form runs a real check here as soon as the
	// user picks a base and the once-per-repo fetch re-lists. A pre-clear
	// answer matching that version sets baseSettleLanded before
	// handleBaseSettled's switch ever looks at it, which releases a submit
	// held for a base nothing checked -- #195's own shape.
	baseSettle int
	browse     int
	// picker is the account picker's preview counter: a preview for a
	// project the user has navigated away from must not overwrite the
	// current one.
	picker int
	// clauth is bumped directly by reloadClauthCmd, with no separate
	// debounce phase -- dir, title, base, browse and picker each have one
	// -- and compared against by handleClauthResult (fix round 1: closes a
	// rapid-refocus staleness gap, see clauthResultMsg's own doc comment
	// in async.go).
	clauth int
	// accountWait is the account holds' one timer, armed at most once per
	// ⌃S (draw-first spec §7.1). It is a counter here for the reason every
	// other one is: a timer armed by a submit that has since been released
	// -- or by a form a ⌃R⌃R discarded -- must not release the next hold,
	// and the version it carries is what says so.
	accountWait int
}

// superseded is what a ⌃R⌃R rebuild starts from: every counter one past
// the discarded form's, so every request that form has in flight is
// already retired when the rebuilt one is constructed.
//
// Carrying the counters unchanged is not enough, and that is #201's own
// review finding rather than a refinement: a carried counter EQUALS the
// discarded form's until the rebuilt form issues a request of that kind,
// so an answer landing inside that window matches the guard and is
// applied. New re-asks dir, base and picker itself, which closes the
// window for those three; browse and clauth are re-asked only when the
// user types a path or focuses the account row, so the window is in
// practice the whole life of the form -- and a check in flight answers in
// milliseconds. Starting one past leaves no window at all: the value the
// rebuilt form starts on is one no request ever carried, and its own
// first request is higher again.
func (v reqVersions) superseded() reqVersions {
	return reqVersions{
		dir:         v.dir + 1,
		title:       v.title + 1,
		base:        v.base + 1,
		baseSettle:  v.baseSettle + 1,
		browse:      v.browse + 1,
		picker:      v.picker + 1,
		clauth:      v.clauth + 1,
		accountWait: v.accountWait + 1,
	}
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
	// homeDir is retained purely so handleClearRequested's rebuild through
	// New can hand it back (see Setup.HomeDir); nothing else reads it.
	homeDir string

	deps Deps

	form form.Model

	// issue and account are nil when their own precondition isn't met, and
	// since #293 both preconditions are answerable without running
	// anything: no Linear key SOURCE is configured, and clauth's status
	// file either reported fewer than two profiles or there is no clauth
	// on PATH to ask (draw-first spec §3). New simply never constructs or
	// appends them, and the row set is fixed before the first frame -- only
	// what a row SAYS changes afterwards.
	issue     *form.IssueField
	dir       *form.DirField
	title     *form.TitleField
	worktree  *form.WorktreeField
	placement *form.PlacementField
	agent     *form.AgentField
	options   *form.OptionsField
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

	// autoPick is the picker's commit-time answer for an `auto` account,
	// recorded by WithAccount just before the plan is built. It lives on the
	// model rather than being threaded through buildPlanInput's arguments so
	// that PlanInput() -- which internal/create's equivalence test compares
	// against the headless command's -- stays a pure read of state.
	autoPick picker.Result

	// linked is the dir check's answer about whether the project is a lane
	// -- a linked worktree checkout -- and linkedCommit the commit the base
	// names in the lane, read at submit (#171). See linkedCheckout for how
	// buildPlanInput uses them.
	linked       linkedProject
	linkedCommit string

	// relistAfterFetch is the base-request version (reqs.base) of the check the
	// once-per-repo `git fetch --prune` scheduled when it finished, and 0
	// when none is out: the one re-list on which a base the user chose is
	// kept on offer and settled (#212, keepChosenBaseAcrossRelist). Every
	// other base check leaves a chosen base alone, a project change's
	// above all (#197).
	relistAfterFetch int

	// baseSettleLanded is the version of the last base-settle answer
	// landed -- reqs.baseSettle is the counter it trails, and the two
	// differ exactly while a base check is out (baseSettlePending).
	// baseNote is the note the last answer carried: why a tier's base was
	// dropped, shown on the worktree panel while the HEAD row it fell back to
	// still stands (#194). "" when nothing was dropped.
	baseSettleLanded int
	baseNote         string

	// checkDeadline overrides blockingCheckDeadline, the bound on every
	// check a submit waits for (#202). Zero -- which is what production
	// leaves it, since there is nothing to tune -- means the constant. See
	// checkBudget, the one place either is read.
	checkDeadline time.Duration

	// submitHeld is a submit waiting for something. Three of the five are
	// #202's, ahead of validation because validation reads their verdicts:
	// the project row's check (#195), the base check (#194) and the
	// title-duplicate check (#137). The handler that lands one --
	// handleDirResult, handleBaseSettled or handleTitleResult -- re-enters
	// handleSubmit, which re-checks all three and holds again if another is
	// still out.
	//
	// The other two are #293's account holds, which sit at the point where
	// the account is READ rather than ahead of validation: the clauth hold
	// inside checkSubmitValidation, and the probe hold in continueSubmit.
	// accountHeld says which, because they resume at different steps, and
	// both are bounded by one timer rather than by awaitCheck (ruling 15).
	submitHeld bool

	// submitResolving is a submit that has passed validation and is out on a
	// round trip before it builds the plan: the lane's commit (#171) or the
	// `auto` account pick. The form is frozen for it (updateResolving, #136).
	// Set where either round trip starts, and cleared where its answer lands
	// (handleLinkedCommit, handlePickerCommit), which is on every path into
	// beginSubmit that set it.
	submitResolving bool

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

	// linearResolving, clauthLoading and pickerProbePending mirror Setup's
	// three new fields for the reason every mirror above exists: a ⌃R⌃R
	// rebuild goes through New from this Model's own state, so a state that
	// lived only in a field would be replaced by the open-time one on the
	// first clear (draw-first spec §7.4).
	//
	// Each transition writes the mirror FIRST and derives the field from
	// it, which is recoverAccountRow's rule applied to all three: a rebuild
	// then cannot put back a state that has since moved on.
	linearResolving    bool
	clauthLoading      bool
	pickerProbePending bool

	// accountHeld is which of the two account holds is holding this submit
	// (draw-first spec §7.1), and accountHoldNone when none is. It decides
	// where a landing re-enters: the clauth hold sits inside validation, so
	// it resumes at handleSubmit, and the probe hold sits after it, so it
	// resumes at continueSubmit -- re-entering the top from there would
	// spend a lane's commit read a second time.
	accountHeld accountHold

	// accountWaitArmed is whether this ⌃S has already armed its one timer.
	// Re-entries from #202's landings and from the other account hold must
	// not re-arm it, or each landing would push the deadline back and the
	// budget would never be spent. Cleared by the next form.SubmitMsg, not
	// by a re-entry.
	accountWaitArmed bool

	// accountWaitExpired is that timer having fired: the account holds are
	// over for this ⌃S whatever they were waiting for. The clauth hold then
	// proceeds WITHOUT the #243 dead-credential check, which is clauth's
	// advisory posture (§7.2); the probe hold refuses, because launching
	// unpinned under `auto` is what handlePickerCommit exists to prevent
	// (§7.3).
	accountWaitExpired bool

	// accountFromConfig is §7.2's answer: which account the row WOULD have
	// selected, from config alone, recorded when a spent budget lets a
	// submit past the clauth hold. The row has no profile list then, so
	// AccountField.SetPin cannot apply `[clauth] default` and the field's
	// own Pin() is empty.
	//
	// It lives here rather than being threaded through buildPlanInput's
	// arguments for autoPick's reason: PlanInput() stays a pure read of
	// state, which is what internal/create's equivalence test compares. The
	// next ⌃S, or a clear, discards it.
	accountFromConfig configAccount

	// clauthUnavailable and pickerUnavailable mirror Setup's fields of the
	// same names, for the reason linearUnavailable does. The rebuild used to
	// drop both, and each clear erased one: New builds no account row at all
	// for a clauth with no profiles unless it is told that clauth is
	// unreadable, and gives no reason for a missing auto row unless it is
	// told the picker failed its probe.
	clauthUnavailable string
	pickerUnavailable string

	// linearIssueSelected tracks IssueField's own "none" vs a real
	// selection (spec §6 field 1: "In Linear mode branchName owns the
	// branch and the title is free text") -- reactToChanges only derives a
	// branch suggestion from the typed title while this is false, OR while
	// v2 spec §11's linear_branch_name has been turned off for this
	// repository.
	linearIssueSelected bool

	// selectedIssueBranch is the chosen Linear issue's own branchName,
	// kept because the answer to "what should the branch be" can be
	// re-asked after the selection was made: switching to a project whose
	// .herdr-draft.toml turns linear_branch_name off (or sets a different
	// branch_prefix) has to re-derive it, and IssueField exposes the
	// chosen issue only through the one-shot form.IssueChosenMsg.
	selectedIssueBranch string

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
	// text whose listing DirField currently holds -- or is waiting for,
	// during the debounce-and-read window, when it holds no listing at all
	// (reactToTypedDir empties the pool on every parent change) -- or "" when
	// the field is in fragment mode. It is the memoization atrium's DirectoryPicker got
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

	// reqs is every request-version counter this Model issues -- see
	// async.go's request type and the schedule*/handle* pair for each
	// source. One struct rather than six fields because of what it is
	// handed to: New takes it whole across a ⌃R⌃R rebuild (#201).
	reqs reqVersions

	// dirLandedVersion is the version of the last directory check whose
	// answer was applied (handleDirResult). It trails reqs.dir exactly
	// while a check is in flight -- debouncing or running -- which is what
	// dirCheckPending compares (#195).
	dirLandedVersion int
	// titleLandedVersion is dirLandedVersion for the title-duplicate check:
	// the version of the last one whose verdict was applied
	// (handleTitleResult), trailing reqs.title exactly while a check is
	// in flight (titleCheckPending, #137).
	titleLandedVersion int

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

	// resolved is v2 spec §10's layered default resolution -- every tier
	// (config.toml, last-used.json, .herdr-draft.toml, projects.json)
	// collapsed into one value per field, plus the tier each came from.
	// Recomputed by applyProjectDefaults whenever the project row changes,
	// since the top two tiers are per-project. See internal/defaults.
	resolved defaults.Resolved

	// repoConfig is the SELECTED project's committed .herdr-draft.toml
	// (v2 spec §11), re-read by the debounced dir check on every project
	// change and kept here for two reasons: applyProjectDefaults feeds it
	// back into defaults.Resolve, and its Notes are the visible report of
	// everything in that file the trust model refused -- pushed onto the
	// project row's panel by showRepoConfig.
	repoConfig config.RepoConfig

	// projects is projects.json, loaded once at Bootstrap: nothing but this
	// process writes it, and it only writes on a successful submit, one
	// message before quitting.
	projects config.Projects

	// projectKey is the projects.json key for the CURRENTLY selected
	// project directory -- the repo root when it is a repo, its canonical
	// path otherwise -- resolved off the update loop by the debounced dir
	// check (see async.go's projectMemoryKey) and kept here so
	// persistStateCmd knows where to record the submit. "" until the first
	// dir check lands, and for a directory that does not exist.
	projectKey string

	// clearedDir and clearedKey are what a ⌃R⌃R says about v2 spec §10's
	// top tier, and all it says about any tier (#135): the project the
	// rebuilt form opens on is resolved without its projects.json entry,
	// and everything under that entry -- the repository's
	// .herdr-draft.toml first -- applies exactly as it does on any
	// form-open. handleClearRequested says why this is not the touched
	// flags.
	//
	// Two fields, because the rebuild does not know the key. New puts the
	// project row back on the popup's opening project, which need not be
	// the one the discarded form was on, and a key is resolved off the
	// update loop by the dir check. So the rebuild records the row's value
	// (clearedDir), and the first answer about that value turns it into
	// the key (clearedKey). An answer about any other value ends the clear
	// instead: that project was chosen after it, before the opening one
	// had resolved.
	//
	// Once the key is known the clear holds while the form stays on that
	// project -- a linked checkout of it included, since projects.json
	// keys the two together -- and ends at the first answer about a
	// different one. clearedMemory is the only reader and the only writer
	// after the rebuild.
	clearedDir string
	clearedKey string

	// agentKinds is the ordered kind list AgentField was built with, kept
	// because defaults.Resolve needs it to skip a remembered kind this
	// binary does not ship (defaults.Sources.KnownAgentKinds) -- and
	// applyProjectDefaults re-resolves long after New has returned.
	agentKinds []string

	// worktreeTouched/placementTouched/agentTouched implement v2 spec §10's
	// "per-project memory re-applies when the project row changes, unless
	// the user has already touched that field" -- the same
	// touched-versus-preselected rule the Linear seeding uses, expressed
	// here rather than inside the fields because none of these three
	// carries a touched flag of its own (WorktreeField has one only for its
	// branch INPUT, not for the on/off chips).
	//
	// These REPLACE the old one-shot worktreeDefaultApplied flag rather
	// than sitting beside it. Two mechanisms deciding whether a default may
	// still be applied is how a field ends up honoring neither: the
	// one-shot gate would have blocked the second project's remembered
	// toggle outright, and removing it while keeping a second gate for the
	// same question would have put the two back in competition.
	// baseTouched joins them for the fourth field projects.json remembers
	// (config.Projects.Entry.Base). It could not exist until WorktreeField
	// had a setter for its base picker's selection: #7 resolved and
	// persisted the remembered base and then had nowhere to put it, so
	// "remember the base" worked in every layer beneath the view and was
	// invisible on screen. WorktreeField.SetBase closed that, and this is
	// the flag that keeps it from fighting the user.
	worktreeTouched  bool
	placementTouched bool
	agentTouched     bool
	baseTouched      bool

	// appliedWorktreeOn/appliedPlacement/appliedAgentKind are what the APP
	// itself last put in those three fields (snapshotAppliedDefaults). The
	// touched flags above are set by comparing the field's current value
	// against these in reactToChanges: a value that moved without the app
	// moving it was moved by the user.
	//
	// These are deliberately NOT lastWorktreeOn. That field belongs to
	// syncDerivedInertness, which resyncs it on every call -- including
	// calls this package might add later, and including the one inside
	// applyProjectDefaults -- so its value tracks "the last time inertness
	// was recomputed", not "the last value the app itself chose". A touched
	// test built on it is correct only for as long as those two happen to
	// coincide, and consulting it directly from a handler (where it always
	// already equals the field) answers the same thing every time.
	//
	// The failure that matters is silent and late: any path that applies a
	// default WITHOUT refreshing the snapshot makes the app's own
	// application look like a user edit on the next reactToChanges pass, at
	// which point per-project memory stops re-applying -- and not on the
	// first project change, only from the second on.
	// TestDirResult_MemoryReAppliesAcrossASecondProjectChange is the test
	// that catches it; it was confirmed to fail when the
	// snapshotAppliedDefaults call at the end of applyProjectDefaults was
	// removed.
	appliedWorktreeOn bool
	appliedPlacement  plan.Placement
	appliedAgentKind  string
	appliedBaseRef    string

	// dirInvalid/titleDupBlocked mirror the last dir-validity/title-dup
	// verdict each already pushed into DirField/TitleField (handleDirResult/
	// handleTitleResult), kept as plain booleans on Model too because
	// neither field exposes a getter back for its own pushed verdict --
	// only a rendered marker/text. checkSubmitValidation (submit orchestration,
	// spec §9) reads these directly rather than re-deriving them, since
	// they're already exactly what the form is currently SHOWING the user.
	dirInvalid      bool
	titleDupBlocked bool

	// dirUnknown/titleDupUnknown/baseUnknown are the same three verdicts'
	// absence: the check did not answer inside blockingCheckDeadline, so
	// there is no verdict at all (#202). checkSubmitValidation refuses on
	// each of them exactly as it refuses on the verdict beside it, and
	// deliberately NOT by folding them into it -- "we could not check" and
	// "we checked, and no" are different things to be told, only one of
	// which the user fixes by typing something else. Each field says which
	// on its own surface, and each is cleared by the next answer that does
	// land.
	dirUnknown      bool
	titleDupUnknown bool

	// uncheckedBaseRef is the base ref whose check gave up, and "" when
	// none did -- baseUnknown reads it. See that method on why the base's
	// unknown is a ref rather than a flag.
	//
	// baseListNote is what the base LIST check last had to say about the
	// worktree panel's status line ("couldn't list", or "" for a list that
	// came back), and lastBaseShown the base that line was last composed
	// for. Both exist because that one line has more than one source and
	// only refreshBaseStatus may write it -- see there.
	uncheckedBaseRef string
	baseListNote     string
	lastBaseShown    string

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
	// submitDeadEnd is true only in the submitting states that have no
	// other way out: step 1 (topology creation) itself failed and reported
	// no space, so SubmitView never gets a SetFailure call at all (spec §9
	// scopes the keep-or-clean prompt to "after step 1 succeeded") -- its
	// own k/c grammar stays permanently inert, having no space to act on,
	// even when the failure left a branch behind (#208) -- or a remove ran
	// and kept a branch it had said it would delete, which leaves nothing
	// to keep or remove either (handleCleanDone, #173). updateSubmitting's
	// Esc/Ctrl+C escape hatch is scoped to this state and to submitWarned,
	// a finished create held on screen for its warnings (#230), which has
	// nothing running either (see its own doc comment): at every OTHER point in the submitting lifecycle --
	// actively streaming, waiting on plan.CleanCheck, or showing a real
	// keep-or-clean prompt -- Esc/Ctrl+C must NOT quit, or it would either
	// strand plan.Execute's own background goroutine forever blocked on
	// an unbuffered channel send nobody is left to drain (fix round 1,
	// reviewer-measured: a permanently elevated goroutine count), or let
	// the user bypass an intentional keep/clean choice silently.
	submitDeadEnd bool
	// submitWarned is a create that succeeded, persisted its state, and
	// finished a step with a warning to read (#230): the popup stays up
	// until esc, enter or ctrl+c closes it, rather than quitting with the
	// warning unread. The plan has finished, so nothing is stranded.
	submitWarned bool
	// submitInput/submitResult/submitCleanDecision are the running
	// submit attempt's own state, threaded across the several async Cmds
	// spec §9's staged pipeline needs (plan.Execute's own streamed
	// progress, then plan.CleanCheck, then -- on a CleanMsg -- plan.Clean)
	// -- see async.go's runSubmitCmd/runCleanCheckCmd/runCleanCmd.
	// submitResult carries plan.ExecResult (placement spec §5.1/§5.4)
	// rather than a bare herdrc.CreatedTopology, because a reused space's
	// Clean/CleanCheck now need SpaceReused/SpaceLabel/AgentAt too, not
	// just the space's own workspace id.
	submitInput         plan.Input
	submitResult        plan.ExecResult
	submitCleanDecision plan.CleanDecision
	// submitSteps is the full, one-per-op working row list startSubmit
	// seeds at StepPending (submitSteps, async.go) and
	// handleSubmitProgress updates in place by Index as each streamed
	// plan.Progress event arrives -- SubmitView.SetSteps REPLACES its own
	// displayed stack on every call, so this is what makes "every step,
	// including ones not yet started, visible from the first frame"
	// possible (plan.Execute's own onProgress never itself emits a
	// StepPending event for a step that hasn't started yet).
	//
	// It holds form.Steps rather than plan.Progresses because the label
	// and detail v2 spec §12's rows show are this layer's to write: see
	// form.Step's own doc comment for why internal/form must not derive
	// them from an op's verb-phrase label itself.
	submitSteps []form.Step
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
		projects:     s.Projects,
		ctx:          s.Ctx,
		stateDir:     s.StateDir,
		homeDir:      s.HomeDir,
		deps:         s.Deps,
		workspaces:   s.Workspaces,
		clauthStatus: s.ClauthStatus,
		linearIssues: s.LinearCache,

		linearUnavailable:  s.LinearUnavailable,
		clauthUnavailable:  s.ClauthUnavailable,
		pickerUnavailable:  s.PickerUnavailable,
		linearResolving:    s.LinearResolving,
		clauthLoading:      s.ClauthLoading,
		pickerProbePending: s.PickerProbePending,

		fetchedRepos: map[string]bool{},

		reqs: s.reqs,
		// Nothing is outstanding for this one source at construction, and
		// only carrying its counter makes that need saying: New schedules
		// no base settle -- applyProjectDefaults does, off the first dir
		// check -- so a landed version left at zero beside a carried
		// counter reports a check out that nobody started
		// (baseSettlePending). Every other landed version is zero here on
		// purpose, because New really does schedule those checks.
		//
		// Consistency rather than a defect fixed: every window that state
		// is wrong in is one where the submit is held or refused for
		// another reason anyway -- the dir check is out, or it timed out
		// and dirUnknown refuses. Said here because the next reader should
		// not have to re-derive it, and pinned by
		// TestClear_EveryCounterStartsPastTheDiscardedForms.
		baseSettleLanded: s.reqs.baseSettle,
	}

	m.dir = form.NewDirField(palette)
	m.title = form.NewTitleField(palette)
	// v3 spec §9's resting panel, from the WorkspaceList Bootstrap
	// already made and already retains -- no new I/O, no new subprocess,
	// no new state file. Pushed once at construction because that is when
	// the data exists; it is never re-fetched (see Model.workspaces).
	m.title.SetSessions(titleSessions(s.Workspaces, s.Ctx.WorkspaceID))
	m.worktree = form.NewWorktreeField(palette)
	m.placement = form.NewPlacementField(palette)
	m.agent = form.NewAgentField(palette)
	m.options = form.NewOptionsField(palette)
	// config.toml's refused [agents.options] entries (agent-options spec
	// §6.2), on the panel of the row they would have configured. They are
	// about the file rather than a kind, so they are set once and shown
	// whichever kind the row is on.
	m.options.SetNotes(s.Config.Agents.OptionWarnings)
	m.prompt = form.NewPromptField(palette)

	// v2 spec §10's layered defaults, resolved here rather than as three
	// separate inline ladders (placement, agent kind, worktree toggle) each
	// re-expressing "config.toml, then last-used.json" in its own idiom.
	// The kind list is resolved first because the resolver needs it: a tier
	// naming an agent kind this binary doesn't ship supplies nothing, so
	// the next tier down applies (see defaults.Sources.KnownAgentKinds).
	//
	// The per-project tier is deliberately absent here: which project the
	// form is pointed at is not known until the first debounced dir check
	// resolves its repository root, so applyProjectDefaults re-resolves
	// with it (and on every later project change) once it is.
	m.agentKinds = OrderedAgentKinds(s.Config.Agents.Favorites)
	m.resolved = defaults.Resolve(defaults.Sources{
		Config:          s.Config,
		Global:          s.State,
		KnownAgentKinds: m.agentKinds,
		// The open-workspace tier (#128) reads the same snapshot the
		// title panel and the dir candidates do. The repository root is
		// not known yet -- the dir check resolves it -- so only an exact
		// checkout match can fire here; applyProjectDefaults re-resolves
		// with the root once the check lands.
		Workspaces: s.Workspaces,
		ProjectDir: pathx.ExpandTilde(m.dir.Value()),
	})
	// The pane-reaper toggle's default (reap spec §6.1). Applied once, here:
	// its chain is built-in then config.toml, neither of which changes with
	// the project row, so applyProjectDefaults deliberately never touches it
	// and a user's ⌃X survives a project change. ⌃R ⌃R comes back through
	// this line.
	m.prompt.SetMarkReady(m.resolved.MarkReady)
	// config.toml's refused branch_prefix (#123), on the panel of the branch
	// it would have shaped. It waits for the resolution above because whether
	// it applies is a question about the resolved prefix -- see
	// BranchPrefixWarning -- and showRepoConfig asks it again for every
	// project the form moves to.
	m.worktree.SetNotes(m.branchPrefixNotes())

	// [default_placement] (spec §12), resolved across config.toml and
	// last-used.json: the two folded-in Task 20 gaps this task's brief
	// names ("[clauth] default and a non-default [default_placement] have
	// no pre-selection path"). Applying it here, before resolved.UseWorktree
	// is known to have landed on or off, needs no care about that ordering:
	// a worktree makes the row inert without moving the chip (placement
	// spec §14), so there is no "snap back to New space" for a later
	// worktree state to undo. SetValue is also a no-op when the
	// chip cursor already sits on the resolved value, which is what makes
	// the unconditional call safe on its own terms too. SetSpace first:
	// the `tab in <space>` chip has to exist before SetValue can land on
	// it.
	m.placement.SetSpace(m.resolved.Space.Label)
	m.placement.SetValue(m.resolved.Placement)

	// Linear (spec §6 field 1): rendered when Linear is configured, which
	// since #293 means a key SOURCE is set rather than a key resolved
	// (draw-first spec §4.2). Four states, first match wins, and three of
	// them are live -- because a cached list is pickable whatever the key
	// is doing, which is decision 4 and #292.
	switch {
	case s.Deps.Linear != nil:
		// A key resolved before the draw, or a rebuild carried the source
		// a landed one installed. The opening fetch is scheduled below and
		// the panel says so meanwhile (ruling 10).
		m.issue = form.NewIssueField(palette)
		m.seedIssueCache(s.LinearCache)
		m.issue.SetPhase(form.IssuePhaseFetching)
	case s.LinearResolving:
		// api_key_cmd is out. Live, with the cache pickable, and the panel
		// naming the key the user wrote (draw-first spec §5.1, §6).
		m.issue = form.NewIssueField(palette)
		m.seedIssueCache(s.LinearCache)
		m.issue.SetPhase(form.IssuePhaseWaitingKey)
	case s.LinearUnavailable != "" && len(s.LinearCache) > 0:
		// #292: the key failed and there is still a perfectly usable list
		// on disk. The reason goes where a failed REFRESH's already goes,
		// because it is the same situation -- a cached list that could not
		// be refreshed -- and SetRefreshError deliberately does not make
		// the field inert.
		m.issue = form.NewIssueField(palette)
		m.seedIssueCache(s.LinearCache)
		m.issue.SetRefreshError(s.LinearUnavailable)
	case s.LinearUnavailable != "":
		// Configured but broken, with nothing to pick (finding I5):
		// present-but-inert with the reason, rather than silently absent
		// -- see Setup.LinearUnavailable and IssueField.SetUnavailable. No
		// linearSource exists, so nothing ever schedules a refresh for it
		// (initCmds below gates on m.issue != nil AND Deps.Linear != nil).
		m.issue = form.NewIssueField(palette)
		m.issue.SetUnavailable(s.LinearUnavailable)
	}

	// Account (spec §6 field 7): rendered when clauth is enabled AND its
	// STATUS FILE reported >= 2 profiles. Bootstrap folds "enabled" into ClauthStatus.
	// Profiles (a disabled clauth simply never populates it), but a
	// caller constructing Setup directly (as this package's own tests do)
	// could still hand in a non-empty ClauthStatus with Deps.Clauth ==
	// nil -- gating on BOTH, mirroring the Deps.Linear != nil gate above,
	// is what keeps reloadClauthCmd (spec §8: "form-open + on account
	// focus") from ever being scheduled against a nil clauthSource in the
	// first place (reloadClauthCmd/handleClauthResult are also
	// defensively guarded on their own -- see async.go -- but this is the
	// gate that matters: with it, m.account is simply never non-nil when
	// Deps.Clauth is nil). A broken clauth gets a row even though it has
	// no profiles to offer, which is the whole point: without one there
	// is nowhere to say that clauth is installed and unreadable, and the
	// user sees exactly what they would see if they had never installed
	// it.
	//
	// That row is not stuck in that state for the life of the popup:
	// focusing it asks clauth again, and a reload that works clears the
	// state and populates the row from here (async.go's
	// recoverAccountRow, #200). This comment used to say "the row
	// disappears again as soon as clauth works", which was true of the
	// NEXT open and, until #200, of nothing else -- the reload loaded the
	// profiles into a row that went on reading `unavailable` and, since
	// #191, went on ignoring input.
	//
	// Since #293 there is a third state between those two, and it is the
	// one that made the row's precondition stop being static: clauth's
	// status file was stale and its CLI is being asked, so the row is drawn
	// `loading…` and inert, and is not a focus stop (ruling 8). There is
	// nothing to retry -- the read is already out -- and nothing to choose
	// between yet, so a choice made there would be silently overridden when
	// the profiles land. recoverAccountRow turns that answer into live,
	// "too few profiles", or a reason.
	switch {
	case s.ClauthUnavailable != "":
		m.account = form.NewAccountField(palette)
		m.account.SetUnavailable(s.ClauthUnavailable)
	case s.ClauthLoading:
		m.account = form.NewAccountField(palette)
		m.account.SetLoading(true)
	case s.Deps.Clauth != nil && len(s.ClauthStatus.Profiles) >= 2:
		m.account = form.NewAccountField(palette)
		m.populateAccountRow()
	}

	// Agent (spec §6 field 6, carried requirement): favorites first, then
	// every remaining known kind, so a favorite is always a chip AND the
	// full list stays reachable behind "more…" (AgentField's own doc: it
	// derives its favorite chips from THIS list's leading entries).
	m.agent.SetKinds(m.agentKinds)
	// ... then the resolved kind: `[agents] default` (spec §12, which
	// SetKinds' own "index 0 is the default" contract could only ever
	// express as favorites[0]) overridden by the kind the user actually
	// launched with last time, when a previous successful submit recorded
	// one (last-used.json). A no-op for an empty kind -- see
	// AgentField.SetKind.
	m.agent.SetKind(m.resolved.AgentKind)

	// Project (spec §6 field 2): current space's repo root, then the
	// current workspace cwd, then every open workspace's own worktree
	// root, then recents -- DirField.SetCandidates selects candidates[0]
	// as the initial selection, so ordering IS the default. The mechanism
	// is widgets/picker.go's SetItems: this first call reaches a picker
	// that has neither a prior version nor a prior selection, so both the
	// isNewVersion branch and the no-selection fallback land the cursor on
	// index 0. (Later same-version refreshes instead re-anchor by item ID,
	// which is what keeps a user's pick from jumping under them -- see
	// DirField.pickerVersion's own doc comment.)
	m.projectCandidates = buildDirCandidates(s.Ctx, s.Workspaces, s.State.Recents)
	m.supplyDirCandidates(m.projectCandidates)
	m.lastDir = m.dir.Value()
	// Path mode's literal-fallback row is expanded the same way the
	// browsed rows around it are (see DirField.SetPathExpander): the app
	// layer owns every path resolution, the form package none.
	m.dir.SetPathExpander(s.Deps.Git.ResolvePath)
	// The reverse mapping, for display only: v2's project row and its
	// panel show "~/Projects/herdr-draft", never the expanded home (v2
	// spec §4). internal/form performs no I/O, so it has to be told where
	// home is; an undeterminable home ("") simply disables collapsing.
	m.dir.SetHomeDir(s.HomeDir)

	// v2 spec §6's row order, declared HERE and nowhere else: issue,
	// title, prompt, project, worktree, placement, agent, options,
	// account. Issue sits directly above title so one ⇧⇥ reaches it, and
	// title/prompt -- the two fields the fast path is about -- lead, with
	// the machinery that usually needs no attention below them. `options`
	// (agent-options spec §7.1) sits directly under the agent whose
	// declaration it renders.
	sections := make([]form.Section, 0, 9)
	if m.issue != nil {
		sections = append(sections, m.issue)
	}
	sections = append(sections, m.title, m.prompt, m.dir, m.worktree, m.placement, m.agent, m.options)
	if m.account != nil {
		sections = append(sections, m.account)
	}

	m.form = form.New(form.Setup{
		Palette:  palette,
		Sections: sections,
		Name:     formName,
		// v2 spec §8: "focus opens on title, not on the first enabled
		// section" -- the whole point of the redesign is that the common
		// session is open, type a title, Enter.
		InitialFocusID: "title",
	})
	m.refreshFormContext()
	m.syncDerivedInertness()
	m.snapshotAppliedDefaults()

	// The picker preview is scheduled for the OPENING directory here, beside
	// the dir and base checks, and not only from reactToChanges: that one
	// fires on a project CHANGE, and the form's first project has not changed
	// from anything. Without this line the account row opens reading a bare
	// `auto` and stays that way until the user touches the project row --
	// found by running the real form rather than by any test, which is the
	// opening-state trap this repository has fallen into before.
	//
	// Appended only when non-nil: schedulePickerPreview returns nil with no
	// picker configured, which is the common case, and initCmds is a slice
	// tests iterate and INVOKE -- a nil in it is a panic, not a no-op, even
	// though tea.Batch itself would drop one.
	m.initCmds = []tea.Cmd{m.scheduleDirCheck(m.lastDir), m.scheduleBaseCheck(m.lastDir)}
	if preview := m.schedulePickerPreview(m.lastDir); preview != nil {
		m.initCmds = append(m.initCmds, preview)
	}
	if m.issue != nil && s.Deps.Linear != nil {
		m.initCmds = append(m.initCmds, m.refreshLinearCmd())
	}
	// clauth's CLI, for a status file that could not answer (draw-first
	// spec §4.4). It is New's rather than Bootstrap's, unlike the key
	// resolve: a ⌃R⌃R rebuild SHOULD ask again -- superseded() has already
	// retired the discarded form's read (#201) -- and the cost is one more
	// `clauth status --json`, which nobody has to approve.
	if m.clauthLoading {
		if reload := m.reloadClauthCmd(); reload != nil {
			m.initCmds = append(m.initCmds, reload)
		}
	}

	return m
}

// seedIssueCache puts the cache-rendered list into a freshly built
// IssueField at a fresh version, which is New's half of spec §10's "render
// cache first, then async refresh".
//
// Factored out because #293 gave it three callers instead of one: a
// resolved key, a key still out, and a key that failed over a list that is
// still pickable (#292).
func (m *Model) seedIssueCache(cache []linear.Issue) {
	if len(cache) == 0 {
		return
	}
	m.issueItemsVersion++
	m.issue.SetIssues(m.issueItemsVersion, cache)
}

// Init focuses the form's initial section and kicks off everything the
// first frame did not need: the debounced dir-validity and base-list
// checks, the account picker's opening preview (carrying the probe), the
// Linear async refresh, the `clauth status --json` a stale status file
// could not answer for, and -- appended by Bootstrap rather than New -- the
// api_key_cmd resolve (#293). The actual scheduling and counter-bumping
// for those already happened in New (see initCmds' own doc comment); this
// only returns the resulting Cmds.
//
// That list is the whole of draw-first spec §3: the form is already on
// screen by the time any of them runs, and each row says what it is
// waiting for.
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
	if m.submitResolving {
		if next, cmd, frozen := m.updateResolving(msg); frozen {
			return next, cmd
		}
	}
	switch msg := msg.(type) {
	case form.IssueChosenMsg:
		return m.handleIssueChosen(msg)
	case form.CancelMsg:
		return m, tea.Quit
	case form.SubmitMsg:
		return m.startSubmitRequest()
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
	case linearKeyMsg:
		return m.handleLinearKey(msg)
	case linearResultMsg:
		return m.handleLinearResult(msg)
	case clauthResultMsg:
		return m.handleClauthResult(msg)
	case accountWaitMsg:
		return m.handleAccountWait(msg)
	case pickerDebounceMsg:
		return m.handlePickerDebounce(msg)
	case pickerPreviewMsg:
		return m.handlePickerPreview(msg)
	case pickerCommitMsg:
		return m.handlePickerCommit(msg)
	case linkedCommitMsg:
		return m.handleLinkedCommit(msg)
	case baseSettledMsg:
		return m.handleBaseSettled(msg)
	default:
		return m.routeToForm(msg)
	}
}

// routeToForm forwards msg to form.Model's own Update, then runs
// reactToChanges (which may itself schedule further async work) before
// returning both Cmds batched together. While a submit is frozen for its
// round trip, only what reachesAFrozenForm names gets that far.
func (m Model) routeToForm(msg tea.Msg) (Model, tea.Cmd) {
	if m.submitResolving && !reachesAFrozenForm(msg) {
		return m, nil
	}
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
//
// The prompt template is taken from the USER's config.toml and never from
// the repository's own .herdr-draft.toml: a repo-controlled template would
// become the agent's first instruction, which is a prompt-injection
// surface rather than a preference (v2 spec §11, which lists
// `[linear] prompt_template` as forbidden after an earlier draft allowed
// it). config.LoadRepoConfig ignores the key outright, so there is nothing
// here to guard against -- this comment exists so the absence reads as a
// decision rather than an oversight.
func (m Model) handleIssueChosen(msg form.IssueChosenMsg) (Model, tea.Cmd) {
	m.linearIssueSelected = msg.Issue != nil
	m.selectedIssueBranch = ""
	if msg.Issue != nil {
		iss := *msg.Issue
		m.selectedIssueBranch = iss.BranchName
		m.title.SetTitle(iss.Title, true)
		m.worktree.SetBranch(m.branchSuggestion(), true)
		m.prompt.SetValue(RenderPromptTemplate(m.cfg.Linear.PromptTemplate, iss), true)
	}
	cmds := m.reactToChanges()
	return m, tea.Batch(cmds...)
}

// branchSuggestion is the branch a SEEDED WorktreeField.SetBranch should
// carry for the form as it currently stands -- the chosen Linear issue's
// own branchName, or the title run through the resolved branch prefix.
//
// Which one depends on v2 spec §11's linear_branch_name, the repo-config key
// a repository sets to keep its own branch naming. The spec names the key
// and its default (true) but does not say what false DOES; this is the app
// layer's reading: false means the branch is derived from the title
// exactly as in manual mode, so a Linear selection still seeds title and
// prompt while the branch stays the repository's own shape. That is the
// only alternative that needs no further configuration to be usable.
//
// An issue with no branchName of its own falls through to the title
// derivation rather than suggesting "", which would blank a branch the
// user can see.
func (m Model) branchSuggestion() string {
	issueBranch := ""
	if m.linearIssueSelected {
		issueBranch = m.selectedIssueBranch
	}
	return BranchFor(m.resolved, issueBranch, m.title.Value())
}

// BranchFor is branchSuggestion's rule with the form's state passed in
// instead of read off a Model: the chosen Linear issue's own branchName
// while v2 spec §11's linear_branch_name leaves it in charge, and the title
// run through the resolved prefix otherwise.
//
// Exported, and extracted from the method above rather than copied, for
// v2 spec §13's sake: the headless `create` command derives its branch from
// the same resolved defaults, and "the command and the form produce the
// same session from the same inputs" is a promise a second implementation
// of this rule would quietly break -- branch_prefix and linear_branch_name
// are both per-repository, so the two would disagree exactly where a
// repository had configured something.
//
// issueBranch is "" when no issue is selected, or when the selected one
// has no branchName of its own; both fall through to the title derivation
// rather than suggesting an empty branch.
func BranchFor(res defaults.Resolved, issueBranch, title string) string {
	if res.LinearBranchName && issueBranch != "" {
		return issueBranch
	}
	return gitx.BranchSlug(res.BranchPrefix, title)
}

// BranchRefusal is why a session must not be created on branch, or nil when
// nothing stands in its way: a name git cannot hold, or one herdr would trim
// into a different name (gitx.ValidateBranchName, #199). Only a session with
// a worktree makes a branch, so without one there is nothing to refuse --
// the same condition the duplicate check has.
//
// An empty branch is refused too, as gitx.ErrBranchNameEmpty (#199's open
// question, decided 2026-09-19). Left out of herdr's argv, it had herdr
// invent a worktree/<adjective>-<noun>-NNNN branch that ignores
// branch_prefix, that the duplicate check never saw, and that the clean
// could never show this run made (plan's branchIsAbsent), so a failed create
// left it behind. Neither path offers "let herdr name it", and in `create`
// an empty --branch is far more often an unset variable than a request.
//
// Exported for internal/create, for WorkspaceLabelled's reason: the form's
// submit and the command's pre-flight refuse the same names because they ask
// the same function, and no plan.Input field records a refusal for
// equivalence_test.go to catch a drift by.
func BranchRefusal(useWorktree bool, branch string) error {
	if !useWorktree {
		return nil
	}
	return gitx.ValidateBranchName(branch)
}

// handleSubmit is form.SubmitMsg's own handler (spec §9's submit
// pipeline): runs every blocking validation FIRST (checkSubmitValidation),
// refusing to create anything at all when one fires -- no plan.Build call,
// no plan.Execute, nothing -- and only once every check clears does it
// build the plan and start the staged execution (startSubmit).
//
// Before any of that it waits for the project row's check (#195). Until the
// check of the value the row now holds lands, dirInvalid, the worktree row's
// git target, the repository's defaults and the lane answer all describe the
// previous project, and validation would pass or refuse on them. The check
// is already on its way -- reactToChanges scheduled it with the edit -- so
// holding is all there is to do here; handleDirResult comes back through
// this function when it lands, and the user presses nothing twice.
//
// It waits for the base check too (#194), which the landing dir check itself
// schedules. A base check still out means the base row may be about to
// change: a remembered base the list does not name has not been offered yet,
// and one that names no commit has not fallen back. It answers in the time a
// `git rev-parse` takes, and handleBaseSettled comes back through here.
//
// And for the title-duplicate check (#137), whose verdict checkSubmitValidation
// reads as titleDupBlocked. An edit to the title, the branch or the project
// schedules one 150 ms out, and until it lands the flag is the verdict on
// what the form held before: a duplicate typed and submitted at once went
// past it, and a duplicate fixed at once was refused with no warning left on
// screen. handleTitleResult comes back through here.
// startSubmitRequest is a NEW ⌃S, as opposed to one of the re-entries
// every held check makes into handleSubmit when its answer lands.
//
// The distinction is the whole of draw-first spec §7.1's "one budget,
// armed once per ⌃S": this is the only place the account holds' per-submit
// state is cleared, so a re-entry cannot re-arm the timer or push the
// deadline back, and a fresh ⌃S after a spent budget may hold again and
// discards §7.2's recorded account.
func (m Model) startSubmitRequest() (Model, tea.Cmd) {
	m.accountWaitArmed = false
	m.accountWaitExpired = false
	m.accountHeld = accountHoldNone
	m.accountFromConfig = configAccount{}
	return m.handleSubmit()
}

func (m Model) handleSubmit() (Model, tea.Cmd) {
	if m.dirCheckPending() || m.baseSettlePending() || m.titleCheckPending() {
		m.submitHeld = true
		return m, nil
	}
	m.submitHeld = false
	if cmd, blocked := m.checkSubmitValidation(); blocked {
		return m, cmd
	}
	// A lane's base is resolved HERE, after every blocking check and ahead
	// of the account pick: it has no side effect, and a lane whose base
	// cannot be resolved should not have spent a pick first.
	if m.needsLinkedCommit() {
		m.submitResolving = true
		return m, m.linkedCommitCmd()
	}
	return m.continueSubmit()
}

// updateResolving is Update while a submit is out on a round trip after
// validation (submitResolving, #136), and reports whether it took msg.
//
// The submit builds the plan the form held when validation passed, so
// nothing the user does may change the form until it has. That takes two
// rules, and this is the first. The form's own submit, clear and issue
// messages already on their way from a key pressed just before are dropped
// here: a second ⌃S would spend a second `auto` pick, a ⌃R⌃R would have the
// pick submit a rebuilt form validation never saw, and an issue would seed
// the title. The ways out stay open: esc and ⌃C, as the form's key grammar
// always has them, and the Cancel button, which cancels exactly as esc does.
//
// The second rule is routeToForm's: while frozen, the form hears nothing but
// a resize (reachesAFrozenForm). Every key, click and paste goes that way,
// and so does anything else an edit could ride in on.
// The round trip's own answer and the async results are the app's, and go
// through as usual.
func (m Model) updateResolving(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if s := msg.String(); s == "esc" || s == "ctrl+c" {
			return m, tea.Quit, true
		}
	case tea.MouseClickMsg:
		if form.IsCancelClick(msg) {
			return m, tea.Quit, true
		}
	case form.SubmitMsg, form.ClearRequestedMsg, form.IssueChosenMsg:
		return m, nil, true
	}
	return m, nil, false
}

// reachesAFrozenForm names the whole of what the form hears while a submit
// is out on its round trip (#136): a resize, which changes no value. It
// names what may pass, not what may not, because an edit does not only
// arrive as a key: ⌃V in a text field is bubbles' own paste, which reads the
// clipboard in a Cmd and comes back as bubbles' unexported paste message --
// so a ⌃V pressed just before ⌃S could land during the freeze and edit the
// field. The cursor's blink stops with the rest, which leaves the cursor
// steady while the form is frozen; focus starts it again.
func reachesAFrozenForm(msg tea.Msg) bool {
	_, resize := msg.(tea.WindowSizeMsg)
	return resize
}

// dirCheckPending reports whether the project row has a check in flight:
// scheduled for its current value and not yet landed. Every edit to the row
// schedules one (reactToChanges) and a stale answer is dropped rather than
// landed (handleDirResult), so the versions agree only once the answer for
// the value the row now holds is the one applied.
func (m Model) dirCheckPending() bool { return m.dirLandedVersion != m.reqs.dir }

// titleCheckPending is dirCheckPending for the title-duplicate check: one
// scheduled for what the title, branch and project now hold, and not yet
// landed.
func (m Model) titleCheckPending() bool { return m.titleLandedVersion != m.reqs.title }

// continueSubmit is handleSubmit's second step, and where a lane's commit
// resumes it (handleLinkedCommit).
func (m Model) continueSubmit() (Model, tea.Cmd) {
	// THE PROBE HOLD (draw-first spec §7.1), before the commit pick and
	// never after it: a commit pick writes the picker's ledger, and must
	// never run an executable that has not answered the probe.
	//
	// Because it sits after validation it freezes the form, exactly as the
	// commit pick's own round trip does (submitResolving, #136) -- an edit
	// made during the wait would otherwise reach a plan validation never
	// saw. And like the clauth hold it applies only to claude.
	if m.agent.Value() == claudeKind && m.account != nil && m.pickerProbePending && m.accountIsAuto() {
		if !m.accountWaitExpired {
			m.submitResolving = true
			// A STATEMENT, not an operand of the return. holdForAccount
			// mutates this m, and Go leaves the order of a plain variable
			// operand against a call in the same return unspecified -- so
			// `return m, m.holdForAccount(...)` may copy m before or after
			// the mutation, and a copy taken first would return a Model
			// that is not held at all. (The m.form.FocusByID(...) returns
			// elsewhere in this file are safe for a different reason:
			// FocusByID takes a VALUE receiver and changes nothing about
			// the struct being returned.)
			cmd := m.holdForAccount(accountHoldProbe)
			return m, cmd
		}
		// §7.3: an unknown is not a pass. Removing `auto` and launching
		// under whatever account happens to be live is what
		// handlePickerCommit exists to prevent.
		return m.refuseAccountHold(accountProbePendingReason)
	}
	// An `auto` account is resolved HERE, after every blocking check and
	// before anything is created: the pick writes a ledger entry, and spending
	// one on a submit that a duplicate title was about to refuse would hand
	// out an account to a session that never exists.
	if m.deps.Picker != nil && m.account != nil && m.accountIsAuto() && m.autoPick.Profile == "" {
		m.account.SetPickerPreview(form.AccountPickerPreview{Pending: true})
		m.submitResolving = true
		return m, m.pickerCommitCmd()
	}
	return m.beginSubmit()
}

// beginSubmit is handleSubmit's second half: build the plan and start it.
//
// Separate because an `auto` account has to make a round trip through a
// tea.Cmd first, and re-running the validation list on the way back would
// re-report verdicts the user has already seen.
func (m Model) beginSubmit() (Model, tea.Cmd) {
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
		m.title.SetVerdict(m.title.Value(), "could not build plan: "+err.Error(), form.VerdictRefusal)
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
//   - A branch that cannot be made (BranchRefusal, #199), computed here for
//     the branch as it stands. It refocuses the worktree row with the
//     cursor in the branch input, and pushes the verdict the panel shows
//     there. For a wrong name that is the text handleTitleResult pushes once
//     the debounced check lands, so a submit that beat the check still says
//     why. For an empty one it is the only place the verdict comes from:
//     "branch name required" is said at submit, as "title required" is, and
//     never by the check.
//   - Branch/workspace-label duplicates (titleDupBlocked, kept live by
//     handleTitleResult) -- the SAME live verdict TitleField is already
//     showing; this does not compute a new message, only blocks.
//   - A pinned clauth profile clauth reports `broken` -- the one
//     auth_status that means the credential cannot be used at all
//     (accountAuthBlocked, #243).
//
// Returns (nil, false) when nothing blocks.
func (m *Model) checkSubmitValidation() (tea.Cmd, bool) {
	if strings.TrimSpace(m.title.Value()) == "" {
		m.title.SetVerdict(m.title.Value(), "title required", form.VerdictRefusal)
		return m.form.FocusByID("title"), true
	}
	if m.dirInvalid || m.dirUnknown {
		// One refusal, two reasons, and the row is already showing which:
		// `invalid` for a path that is not there, `check timed out` for
		// one nobody could answer for (#202). Nothing is created on the
		// second either -- an unknown is not a pass.
		return m.form.FocusByID("dir"), true
	}
	// Before the duplicate refusal, which is about whether a branch of this
	// name exists -- a question with no answer for a name git cannot hold
	// (#199). Asked here rather than read off the check's last verdict: it
	// is pure, so the answer can be had for the branch exactly as it stands.
	branch := m.worktree.Branch()
	if err := BranchRefusal(m.worktree.Enabled() && m.worktree.On(), branch); err != nil {
		m.worktree.SetBranchVerdict(branch, branchVerdictText(err))
		return tea.Batch(m.form.FocusByID("worktree"), m.worktree.FocusBranch()), true
	}
	if m.baseUnknown() {
		// The other half of the worktree row's refusals, and it lands on
		// the same row for the same reason: the base is one keystroke away
		// there, and the panel this focus opens names the ref (#202).
		return m.form.FocusByID("worktree"), true
	}
	if m.titleDupBlocked || m.titleDupUnknown {
		// As with the project row: one refusal, and the panel this focus
		// opens already says which of the two it is (#202).
		return m.form.FocusByID("title"), true
	}
	// THE CLAUTH HOLD (draw-first spec §7.1), immediately before the check
	// it exists for and after every validation that does not need an
	// account: a required title, a duplicate, or a branch git cannot use is
	// refused at once, as today, and never after a wait that could not have
	// changed the answer.
	//
	// Only for claude, the one kind an account is pinned or picked for
	// (accountPin, ResolveAccount). A codex submit never waits on clauth.
	if m.agent.Value() == claudeKind && m.account != nil && m.clauthLoading {
		if !m.accountWaitExpired {
			return m.holdForAccount(accountHoldClauth), true
		}
		// Decision 5: the budget is spent, so the create goes ahead
		// WITHOUT #243's dead-credential check -- there is no status to
		// check against, and clauth's posture here is advisory, as
		// `create` has taken it since #141. Unlike #202's held checks this
		// timeout does not refuse.
		//
		// What it launches under is recorded here, where the submit gets
		// past, and read from there by accountPin, continueSubmit and
		// ResolveAccount (§7.2).
		m.accountFromConfig = m.accountFromConfigValue()
		return nil, false
	}
	if pin, blocked := m.accountAuthBlocked(); blocked {
		// Fix round 1 (reviewer finding -- silent failure): the row
		// marker Task 18 already renders for this profile was already
		// visible BEFORE this blocked Create press, so it alone gave no
		// new signal that anything happened; AccountField.SetVerdict
		// (fix round 1) pushes a fresh, submit-time message, the same
		// "the field itself shows why" pattern Title's own dup verdict
		// already used.
		// "sign in again  <status>": the same words the account ROW
		// already shows for a bad auth status, and v2 spec §6's own
		// `<state>  <reason>` separator. It replaced
		// "blocked — auth: <status>", which led with a state the user
		// cannot act on and carried a `Label:` colon v2 spec §7 drops
		// everywhere else.
		m.account.SetVerdict(pin, fmt.Sprintf("sign in again  clauth reports %s", clauth.AuthBroken))
		return m.form.FocusByID("account"), true
	}
	return nil, false
}

// accountHold names which of draw-first spec §7.1's two holds is holding a
// submit, which is what says where a landing re-enters.
//
// Two holds rather than one because they sit at different points and wait
// for different answers: the clauth hold is inside validation, immediately
// before #243's dead-credential check, and the probe hold is after all of
// it, immediately before the commit pick. Re-entering the top from the
// second would re-run a lane's commit read, which is a round trip with a
// cost.
type accountHold int

const (
	accountHoldNone accountHold = iota
	accountHoldClauth
	accountHoldProbe
)

// configAccount is draw-first spec §7.2's answer: which account the row
// WOULD have selected, from config alone, for a submit that outlasted a
// clauth load.
//
// set is what distinguishes "unpinned, decided from config" from "nothing
// decided" -- both carry an empty pin, and only the first must beat the
// field's own Pin(). It is the same shape WithAccount records a picker's
// answer in, and for the same reason: buildPlanInput stays a pure read of
// state.
type configAccount struct {
	set bool
	// pin is a `[clauth] default` naming a profile, pinned WITHOUT
	// verification, exactly as `create` pins one. The live row would drop a
	// mistyped default ("never guess"); this path types `clauth start
	// <typo>` into the pane instead, which is the one disclosed divergence.
	pin string
	// auto is the picker's own row, chosen when no default names a profile
	// and a picker is configured whose probe has not failed. The commit
	// pick then runs once the probe passes (§7.1's probe hold); a probe
	// still pending when the budget is spent is §7.3's refusal.
	auto bool
}

// accountFromConfigValue is configAccount's own precedence, which is the
// LIVE row's precedence read off config instead of off the field:
// AccountField.SetPin has a named profile beat `auto`, and
// SetPickerAvailable makes `auto` the resting selection over `active`.
func (m Model) accountFromConfigValue() configAccount {
	if pin := m.cfg.Clauth.Default; pin != "" && pin != accountActiveSentinel {
		return configAccount{set: true, pin: pin}
	}
	if m.deps.Picker != nil {
		return configAccount{set: true, auto: true}
	}
	return configAccount{set: true}
}

// accountActiveSentinel is config.toml's documented "use whatever profile
// is live" value for `[clauth] default`, which AccountField.SetPin already
// treats as a no-op. Named here because accountFromConfigValue has to make
// the same judgement without a field to ask.
const accountActiveSentinel = "active"

// accountIsAuto is "the selection is the picker's own row", reading
// §7.2's recorded answer ahead of the field -- which is the order every
// reader of that answer takes, because the field it would otherwise ask has
// no profile list to have selected anything in.
func (m Model) accountIsAuto() bool {
	if m.accountFromConfig.set {
		return m.accountFromConfig.auto
	}
	return m.account != nil && m.account.IsAuto()
}

// holdForAccount holds the submit at the step that called it and arms the
// one timer that bounds the wait (draw-first spec §7.1).
//
// ONE BUDGET, ARMED ONCE PER ⌃S. The bound is checkBudget()'s deadline, the
// same 5s every #202 check gets, but the wait is a TIMER rather than
// awaitCheck (ruling 15, a documented exception to CLAUDE.md's "the same
// bound, from the same two helpers"): the answer is already in flight as a
// message and there is no call to hand awaitCheck. Re-entries -- from
// #202's landings, and from the other account hold -- find accountWaitArmed
// set and do not re-arm, so nothing pushes the deadline back.
func (m *Model) holdForAccount(which accountHold) tea.Cmd {
	m.submitHeld = true
	m.accountHeld = which
	if m.accountWaitArmed {
		return nil
	}
	m.accountWaitArmed = true
	m.reqs.accountWait++
	v := m.reqs.accountWait
	clock := m.deps.Clock
	_, deadline := m.checkBudget()
	return func() tea.Msg {
		clock.sleep(deadline)
		return accountWaitMsg{version: v}
	}
}

// resumeAccountHold re-enters the step a landing has just released, and is
// a no-op when no account hold is up.
func (m Model) resumeAccountHold() (Model, tea.Cmd) {
	switch m.accountHeld {
	case accountHoldClauth:
		m.accountHeld = accountHoldNone
		return m.handleSubmit()
	case accountHoldProbe:
		m.accountHeld = accountHoldNone
		return m.continueSubmit()
	}
	return m, nil
}

// refuseAccountHold ends a held submit with a reason on the account row
// (draw-first spec §7.3), which is #202's posture for a check that could
// not answer and handlePickerCommit's for an `auto` that could not be
// resolved: neither falls through to an unpinned launch.
//
// It clears submitResolving because the probe hold froze the form the way
// the commit pick's own round trip does -- an edit made during the wait
// would otherwise reach a plan validation never saw -- and the freeze has
// to end with the wait.
func (m Model) refuseAccountHold(reason string) (Model, tea.Cmd) {
	m.submitHeld = false
	m.accountHeld = accountHoldNone
	m.submitResolving = false
	if m.account == nil {
		return m, nil
	}
	m.account.SetVerdict(m.account.Pin(), reason)
	return m, m.form.FocusByID("account")
}

// accountProbePendingReason is the first of §7.3's two refusals, in #202's
// could-not-check register: the budget ran out with the probe still out.
// The second carries the probe's own reason, which is more specific than
// anything this layer could compose.
const accountProbePendingReason = "picker has not answered yet"

// titleSessions maps herdr's own workspace list down to the value type
// internal/form's TitleField takes (v3 spec §9). The mapping is the
// boundary CLAUDE.md draws -- "internal/form ... no knowledge of
// herdr/git/Linear" -- so the herdrc types stop here.
//
// The INVOKING workspace is dropped. It is the one the popup is open in,
// so it is on screen behind the form already, and it is the one workspace
// a new session can never collide with in any way the user would be
// surprised by. Listing it would spend a row of a fifteen-row panel
// saying "you are here".
//
// A workspace's Worktree is optional -- herdr returns the key only for a
// workspace it has repository context for (verified against live `herdr
// workspace list` output, where a plain shell workspace has none) -- so
// the repository column is empty for those rather than absent for all.
func titleSessions(workspaces []herdrc.WorkspaceInfo, invokingID string) []form.Session {
	out := make([]form.Session, 0, len(workspaces))
	for _, w := range workspaces {
		if w.WorkspaceID != "" && w.WorkspaceID == invokingID {
			continue
		}
		s := form.Session{Label: w.Label, Status: w.AgentStatus, Panes: w.PaneCount}
		if w.Worktree != nil {
			s.Repo = w.Worktree.RepoName
		}
		out = append(out, s)
	}
	return out
}

// accountPin returns the Input.AccountPin plan.Build should receive:
// AccountField's own Pin() only when the currently selected agent kind is
// claude (spec §6 field 7 -- pinning is meaningless, and plan.Build itself
// rejects, any other kind) -- "" (unpinned) otherwise, even if a pin
// committed before the agent kind changed is still recorded
// (AccountField's inert state, driven by SetAgentIsClaude, keeps the pin
// so it applies again once the agent is claude, and refuses changes to it
// meanwhile, #182).
// The picker's commit-time answer wins when there is one: `auto` is a promise
// to resolve at submit, and WithAccount is what fulfils it. With no answer
// recorded, this is the field's own pin exactly as before.
func (m Model) accountPin() string {
	if m.account == nil || m.agent.Value() != claudeKind {
		return ""
	}
	if m.autoPick.Profile != "" {
		return m.autoPick.Profile
	}
	// §7.2's answer ahead of the field, for a submit that outlasted a
	// clauth load: the field has no profile list, so SetPin could not have
	// applied `[clauth] default` and its own Pin() is empty.
	if m.accountFromConfig.set {
		return m.accountFromConfig.pin
	}
	return m.account.Pin()
}

// accountLaunch is `[clauth] launch`, mapped to plan's own enum.
//
// config.toml is the ONLY source: the mode is a property of the machine's
// shell, not of the session being created, so no row offers it and no memory
// tier remembers it -- the same reasoning `[worktree] trust_repository` is
// confined to config.toml by.
func (m Model) accountLaunch() plan.LaunchMode {
	if m.cfg.Clauth.Launch == config.ClauthLaunchWrapper {
		return plan.LaunchWrapper
	}
	return plan.LaunchClauthStart
}

// ResolveAccount runs the configured picker for an `auto` selection and
// returns its answer. It is a no-op returning a zero Result and a nil error
// for every other selection -- an `active` row, a hand-pinned profile, a
// non-claude agent, or no picker at all -- so callers may call it
// unconditionally.
//
// This is the COMMIT call, and it deliberately does not pass --dry-run: the
// protocol's ledger write is what stops two sessions opened a second apart
// from being handed the same account, and a preview that wrote one would
// register intent for every keystroke in the project field.
//
// It is exported because both callers of it are real: the submit pipeline
// (async.go's pickerCommitCmd) and internal/create's equivalence test, which
// has to drive the form all the way to a comparable plan.Input. Pairing it
// with WithAccount rather than mutating in place is what lets the I/O happen
// inside a tea.Cmd, which runs off-model by construction.
func (m Model) ResolveAccount(ctx context.Context) (picker.Result, error) {
	if m.deps.Picker == nil || m.account == nil || !m.accountIsAuto() || m.agent.Value() != claudeKind {
		return picker.Result{}, nil
	}
	return m.deps.Picker.Pick(ctx, pathx.ExpandTilde(m.dir.Value()), picker.Options{})
}

// linkedProject is the dir check's answer about a lane (#171): root is the
// repository's primary checkout when dir is a linked worktree checkout, ""
// when it is not. dir is the project value the answer was for.
type linkedProject struct{ dir, root string }

// linkedCheckout is the plan.Input.Linked this form would submit: the primary
// checkout the worktree is created from, and the commit the base names in
// the lane. Zero without a worktree, and zero when the dir check's answer was
// about a different project value -- one typed and submitted before its own
// check landed -- rather than send this project's worktree to another
// repository.
func (m Model) linkedCheckout(useWorktree bool) plan.LinkedCheckout {
	if !useWorktree || m.linked.root == "" || m.linked.dir != m.dir.Value() {
		return plan.LinkedCheckout{}
	}
	return plan.LinkedCheckout{RepoRoot: m.linked.root, Commit: m.linkedCommit}
}

// needsLinkedCommit reports whether a submit has to resolve its base in the
// lane first: a worktree, from a lane.
func (m Model) needsLinkedCommit() bool {
	useWorktree := m.worktree.Enabled() && m.worktree.On()
	return m.linkedCheckout(useWorktree).RepoRoot != ""
}

// linkedBaseRef is the ref a submit from a lane resolves there: the chosen
// base, or HEAD -- the base row's own row 0 -- when none is.
func (m Model) linkedBaseRef() string { return cmp.Or(m.worktree.Base(), "HEAD") }

// ResolveLinkedCommit resolves the base in the lane for a submit that needs
// it (needsLinkedCommit), and is a no-op returning "" otherwise, so callers
// may call it unconditionally.
//
// In the lane, because herdr runs `git worktree add` in the source, and from
// the primary checkout HEAD -- the base row's row 0 -- is the primary's
// commit. At submit rather than with the dir check, because a lane's own
// agent may commit while the popup is up, and HEAD means the commit the lane
// is on when the session is made. It is `create`'s withBase for
// the popup, and exported for ResolveAccount's reason: internal/create's
// equivalence test drives the form to a comparable plan.Input through it and
// WithLinkedCommit.
func (m Model) ResolveLinkedCommit(ctx context.Context) (string, error) {
	dir, ref, ask := m.linkedCommitReq()
	if !ask {
		return "", nil
	}
	return m.deps.Git.ResolveCommit(ctx, dir, ref)
}

// linkedCommitReq is the question ResolveLinkedCommit asks, read off the
// form, with ask false when there is none (needsLinkedCommit).
//
// It is split out so linkedCommitCmd can do the READING somewhere the
// answer's own goroutine is not. Since #202 that goroutine can outlive the
// answer -- the deadline can produce the message without it -- and the
// message unfreezes the form, so a field read left over there would be
// racing the next edit rather than sitting safely inside the freeze. Every
// other bounded check already captures its inputs at scheduling time
// (scheduleTitleCheck's own doc comment names the discipline); this is the
// one that read them from a Model it had carried along.
//
// The dir is expanded as buildPlanInput expands ProjectDir; the dir check
// keyed its answer on the raw project value.
func (m Model) linkedCommitReq() (dir, ref string, ask bool) {
	if !m.needsLinkedCommit() {
		return "", "", false
	}
	return pathx.ExpandTilde(m.linked.dir), m.linkedBaseRef(), true
}

// WithLinkedCommit records a ResolveLinkedCommit answer so buildPlanInput
// can stay a pure read of field state.
func (m Model) WithLinkedCommit(commit string) Model {
	m.linkedCommit = commit
	return m
}

// WithAccount records a ResolveAccount answer so buildPlanInput can stay a
// pure read of field state. A zero Result clears any previous answer, which is
// what makes a second submit after a failed first one ask again rather than
// reuse an account the picker has since ledgered elsewhere.
func (m Model) WithAccount(r picker.Result) Model {
	m.autoPick = r
	return m
}

// accountAuthBlocked reports whether the currently pinned profile (see
// accountPin) is one clauth says cannot be used at all -- spec §9's
// blocking verdict as #243 narrowed it -- consulting m.clauthStatus, the
// last clauth feed New/handleClauthResult recorded (AccountField itself
// exposes no per-profile AuthStatus getter of its own).
//
// The judgment is clauth.Status.AuthOf's, not this function's, and that is
// the point: every guard it applies is one a loop over Profiles here would
// forget. Two used to be forgotten right here.
//
//   - An EMPTY AuthStatus does not block. This was a disclosed judgment
//     call when it was written ("consistency with the row over a stricter
//     rule nobody asked for"); it is now clauth's own contract, which tells
//     readers to default an absent auth_status to "ok"
//     (https://github.com/uwuclxdy/clauth/blob/v0.15.2/wiki/Daemon.md#L165).
//   - A DEGRADED status does not block (#245). It used to: the popup drew
//     names only, saying nothing about any profile's auth state, while this
//     gate refused to launch because of that same unreadable state.
//
// And only `broken` blocks. `expired` is a token between refreshes -- the
// launch this form builds hands its refresh token to `claude` -- so it is
// marked on the row and never refused (#243). No status text is returned
// with the verdict any more: `broken` is the only value that reaches the
// caller's message, so the caller names it from the constant rather than
// from a second traversal that could disagree with this one.
func (m Model) accountAuthBlocked() (pin string, blocked bool) {
	pin = m.accountPin()
	if pin == "" {
		return "", false
	}
	return pin, m.clauthStatus.AuthOf(pin) == clauth.AuthDead
}

// PlanInput is the plan.Input this form would submit as it currently
// stands -- buildPlanInput's own answer, exported because v2 spec §13's
// headless `create` has to produce the SAME one from the same inputs and
// that equivalence is worth an assertion rather than a comment. Nothing in
// production calls it; internal/create's equivalence test does, comparing
// this form's answer against the command's field by field.
func (m Model) PlanInput() plan.Input { return m.buildPlanInput() }

// buildPlanInput composes plan.Input from the form's current field state
// (spec §9) -- called only once checkSubmitValidation has cleared every
// blocking condition.
func (m Model) buildPlanInput() plan.Input {
	useWorktree := m.worktree.Enabled() && m.worktree.On()
	return plan.Input{
		// Expanded here, at the boundary where the project directory stops
		// being text the user typed and becomes an argument for herdr's CLI
		// and git (finding I3): `herdr worktree create --cwd` expands a
		// leading "~" server-side, but `herdr workspace create --cwd` and
		// `herdr pane split --cwd` do not -- see internal/pathx's own
		// package doc.
		ProjectDir:  pathx.ExpandTilde(m.dir.Value()),
		Title:       m.title.Value(),
		Branch:      m.worktree.Branch(),
		BaseRef:     m.worktree.Base(),
		UseWorktree: useWorktree,
		IsGitRepo:   m.worktree.Enabled(), // WorktreeField.Enabled() IS "is the target a git repo" (its own doc comment).
		// The chip keeps whatever was chosen or remembered, so turning the
		// worktree off shows it again; with a worktree it does not apply
		// (placement spec §14).
		Placement:        plan.EffectivePlacement(useWorktree, m.placement.Value()),
		Space:            m.resolved.Space,
		AgentKind:        m.agent.Value(),
		ExtraArgs:        m.cfg.Agents.ExtraArgs[m.agent.Value()],
		AgentOptions:     m.agentOptions(),
		AccountPin:       m.accountPin(),
		AccountLaunch:    m.accountLaunch(),
		AccountConfigDir: m.autoPick.ConfigDir,
		Launcher:         m.cfg.Clauth.Launcher,
		Linked:           m.linkedCheckout(useWorktree),
		Prompt:           m.prompt.Value(),
		// The toggle's position, not whether the instruction is appended --
		// plan.PromptText decides that for both callers (reap spec §7.2).
		MarkReady:        m.prompt.MarkReady(),
		Ctx:              m.ctx,
		DetectionTimeout: time.Duration(m.cfg.Timeouts.DetectionMS) * time.Millisecond,
		PromptTimeout:    time.Duration(m.cfg.Timeouts.PromptWaitMS) * time.Millisecond,
		// `--trust-repository` on `herdr worktree create` (herdr 0.9.0,
		// #3044): trust a verified repository for this one request rather
		// than editing the user's global git config. Opt-in via
		// `[worktree] trust_repository`, and it comes through the resolver
		// like every other default so the form and `create` cannot
		// disagree -- but with a chain that stops at config.toml, since no
		// repository or remembered choice may supply it (see
		// defaults.Resolved.TrustRepository).
		TrustRepository: m.resolved.TrustRepository,
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
	m.submitSteps = submitSteps(ops, in)
	m.submitView = form.NewSubmitView(m.palette)
	// v2 spec §12's first requirement is that the pipeline wear the
	// form's own header. The name is the form's name; the context half
	// is the selected project, which is as much of spec §4's
	// "repository · branch" line as this layer currently resolves (the
	// form's own live context is wired separately).
	m.submitView.SetHeader(submitHeaderName, submitHeaderContext(in))
	m.submitView.SetWaitingHint(submitWaitingHint(in))
	m.submitView.SetSteps(m.submitSteps)
	// TrustWait is the popup's alone (#115's decision 2): there is a person
	// on the other side of this screen who can answer a dialog, which is
	// exactly what headless `create` does not have.
	return m, runSubmitCmd(context.Background(), m.deps.Runner, ops, plan.ExecOpts{
		TrustWait: time.Duration(m.cfg.Timeouts.TrustWaitMS) * time.Millisecond,
	})
}

// handleClearRequested implements form.ClearRequestedMsg (spec §6's ⌃R⌃R
// double-tap). Task 20 left this a deliberate, documented no-op and said
// why: form.go's own doc makes rebuilding to defaults the app layer's
// job, but doing it CORRECTLY means safely re-applying every
// already-fetched async result -- candidates, issues, clauth profiles --
// onto FRESH field instances, which is real work that task's own
// responsibility list did not name. This method is that deferred work,
// held to the brief's wording for it: "reset the fields to their
// startup/seeded state (config defaults + context-derived values), not to
// empty zero values". It rebuilds the form from scratch via
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
		Projects:     m.projects,
		Palette:      m.palette,
		StateDir:     m.stateDir,
		Workspaces:   m.workspaces,
		ClauthStatus: m.clauthStatus,
		LinearCache:  m.linearIssues,
		HomeDir:      m.homeDir,

		LinearUnavailable: m.linearUnavailable,
		ClauthUnavailable: m.clauthUnavailable,
		PickerUnavailable: m.pickerUnavailable,

		// The three waits a rebuild inherits (draw-first spec §7.4). The
		// key's own message is Bootstrap's and unversioned, so it lands on
		// the rebuilt form and api_key_cmd never runs twice; clauth is
		// re-asked by New, and superseded() has already retired the
		// discarded form's read; and the probe's verdict is unversioned
		// too, so an answer still in flight decides it.
		LinearResolving:    m.linearResolving,
		ClauthLoading:      m.clauthLoading,
		PickerProbePending: m.pickerProbePending,

		// Every counter, one past this form's (#201): a check the form
		// being discarded still has in flight must not meet a version the
		// rebuilt one holds or issues. See reqVersions.superseded.
		reqs: m.reqs.superseded(),
	})
	// v2 spec §10: "⌃R ⌃R clears back to the repository default" --
	// explicitly NOT back to what you last did in this project. New has
	// already resolved without the per-project tier (it knows no project
	// key yet), but the dir check fresh.Init() is about to schedule would
	// resolve WITH it and put the memory straight back, undoing the
	// clear. So the rebuilt form is told which project it opened on, and
	// applyProjectDefaults leaves that project's projects.json entry out
	// of its resolution: that tier alone, for that project alone (#135).
	// See clearedDir for how the project is identified and when the clear
	// ends.
	//
	// This used to set the four touched flags instead, on the argument
	// that one mechanism should decide whether a default may still be
	// applied (v2 spec §10's first wiring trap), and that a ⌃R⌃R is a
	// deliberate statement about those fields' values, which is what
	// "touched" means. The first half still holds: whether a FIELD may be
	// moved remains the touched flags' question alone, and this answers
	// none of it -- it decides what the resolution is made from, before
	// any field is asked. The second half was wrong. A touched flag says
	// "the user chose this value", and a choice outranks every tier, so
	// the flags stopped .herdr-draft.toml -- and the worktree default of
	// config.toml and last-used.json, which only a dir check ever applies
	// -- as surely as they stopped projects.json; and since a flag is
	// never cleared, they did it for every project the popup moved to
	// afterwards. A repository whose file said split-here cleared to new
	// space while the resolution still credited the file, and so did the
	// next repository. A clear chooses no value. It names a tier not to
	// consult, which is a question about the resolution rather than about
	// the fields: the old code read "don't re-apply the memory" as "don't
	// apply anything".
	//
	// The flags themselves mean what they always did. The rebuilt form
	// starts with all four false, as any form-open does, so a value the
	// user moves after the clear is theirs and outranks every tier from
	// then on.
	fresh.clearedDir = fresh.dir.Value()
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

	// Touched-versus-preselected for the three fields per-project memory
	// re-applies to (v2 spec §10). This runs FIRST, before anything below can
	// move a value itself: every one of these three getters is compared
	// against what the app last put there (snapshotAppliedDefaults), so a
	// value that moved without the app moving it moved because the user
	// did. syncDerivedInertness at the bottom of this function used to be
	// able to move Placement on its own, back when a worktree turning on
	// snapped it to New space. Placement spec §14 brought the inertness
	// back WITHOUT the snap -- PlacementField.Value() is the chip either
	// way, and plan.EffectivePlacement applies the worktree rule at build
	// time -- so that dependency is still gone; the snapshot refreshes
	// AFTER it regardless.
	m.noteUserEdits()

	if typed := m.dir.Typed(); typed != m.lastDirTyped {
		m.lastDirTyped = typed
		cmds = append(cmds, m.reactToTypedDir(typed)...)
	}

	dirVal := m.dir.Value()
	if dirVal != m.lastDir {
		m.lastDir = dirVal
		cmds = append(cmds, m.scheduleDirCheck(dirVal), m.scheduleBaseCheck(dirVal), m.schedulePickerPreview(dirVal))
	}

	titleVal := m.title.Value()
	if titleVal != m.lastTitle {
		m.lastTitle = titleVal
		// Manual mode (spec §6 field 3): "choosing a title is choosing a
		// branch" -- WorktreeField.SetBranch's own touched guard means
		// this is a no-op the moment the user has edited Branch
		// themselves, and branchSuggestion returns the chosen issue's own
		// branchName unchanged while that is what owns the branch, so the
		// call is safe to make unconditionally.
		m.worktree.SetBranch(m.branchSuggestion(), true)
	}

	branchVal := m.worktree.Branch()
	worktreeOn := m.worktree.On()
	dupKey := titleVal + "\x00" + branchVal + "\x00" + dirVal
	if dupKey != m.lastDupKey || worktreeOn != m.lastWorktreeOn {
		m.lastDupKey = dupKey
		// The resting note goes on NOW, not when the check lands, so the
		// title panel says what the title will produce while the debounce
		// is still running. Overwriting whatever verdict is there is safe
		// precisely here and nowhere else: this branch fires exactly when
		// a fresh check is being scheduled, so any duplicate warning
		// already on screen was computed for a title/branch/dir triple
		// that no longer holds and is about to be recomputed.
		m.title.SetVerdict(titleVal, m.titleNote(), form.VerdictNote)
		cmds = append(cmds, m.scheduleTitleCheck(titleVal, branchVal, dirVal, worktreeOn))
	}

	if focusedID := m.form.FocusedID(); focusedID != m.lastFocusedID {
		m.lastFocusedID = focusedID
		// Not while the row is LOADING (draw-first spec §4.2): the
		// open-time read is already out, and a second one would retire it
		// through the version guard -- turning a focus into a reason the
		// first answer never got to give.
		if focusedID == "account" && m.account != nil && !m.clauthLoading {
			cmds = append(cmds, m.reloadClauthCmd())
		}
	}

	m.syncDerivedInertness()
	// v2 spec §11's provenance follows the touched flags noteUserEdits
	// set at the top of this function: a value the user has just moved is
	// no longer the repository's, and the line saying it was has to go
	// with it. This handler is the only place those flags ever flip.
	m.showRepoConfig()
	// Cheap and synchronous, like syncDerivedInertness above: the header's
	// project name follows the project ROW, which can move on any routed
	// message (a keystroke re-ranking the candidates, a click on a
	// candidate row). Its branch half is refreshed separately, by the base
	// check that learns it (async.go's handleBaseResult).
	m.refreshFormContext()
	// The base status line follows the base, because what it says is about
	// one: an unknown whose ref the user has replaced stops applying
	// (baseUnknown), and the line saying it has to go with it, exactly as
	// v2 spec §11's provenance goes with a value the user moved. Its own
	// snapshot rather than appliedBaseRef, which syncDerivedInertness
	// resyncs for a different question -- sharing one is the shape the
	// CLAUDE.md convention warns about.
	if base := m.worktree.RequestedBase(); base != m.lastBaseShown {
		m.lastBaseShown = base
		m.refreshBaseStatus()
	}
	m.snapshotAppliedDefaults()
	return cmds
}

// noteUserEdits marks the worktree toggle, placement and agent kind as
// touched when their current value differs from what the app itself last
// put there -- v2 spec §10's touched-versus-preselected rule, for the three
// fields per-project memory re-applies to. None of them carries a touched
// flag of its own, and the form deliberately exposes no "section X
// changed" signal, so this is the same one-level-up diff reactToChanges
// already does for the other getters.
//
// The base is the exception on both counts, and the exception is the
// interesting part: WorktreeField does carry a flag for it (BasePicked,
// alongside the one it has always had for its branch input), and this
// function reads it below, because a pick of the HEAD row is a decision
// that changes no value for a diff to find (#262).
//
// Once set, a flag is never cleared: only a ⌃R⌃R rebuild (which starts
// from a fresh Model) resets the form's idea of what the user has decided.
func (m *Model) noteUserEdits() {
	if on := m.worktree.On(); on != m.appliedWorktreeOn {
		m.worktreeTouched = true
	}
	if p := m.placement.Value(); p != m.appliedPlacement {
		m.placementTouched = true
	}
	if k := m.agent.Value(); k != m.appliedAgentKind {
		m.agentTouched = true
	}
	// The base needs one extra guard the other three do not: its value
	// also changes when the CANDIDATE LIST changes underneath it (an async
	// `git for-each-ref` landing, a project switch replacing the pool).
	// WorktreeField.SetBase shows the HEAD row while it holds a ref nothing
	// names yet, so the app's own moves go through HEAD. Only a move AWAY
	// from HEAD counts as a decision; a fall back to HEAD is the list
	// moving, not the user. (widgets.Picker itself keeps a vanished row's
	// index rather than going back to row 0, which is why a user's own base
	// is kept on offer across a project change -- applyProjectDefaults.)
	//
	// That rule answers only one half. The other -- a held ref LANDING,
	// which is a move away from HEAD that the app made -- is answered in
	// snapshotAppliedDefaults instead, by recording the ref the app asked
	// for rather than the row it was showing (#248). Read the two together
	// before changing either.
	if b := m.worktree.Base(); b != m.appliedBaseRef && b != "" {
		m.baseTouched = true
		// And retires the note, which said HEAD was being used instead of
		// some ref -- true of the base they just replaced, not of this one.
		// This is the whole of showRepoConfig's old baseTouched guard, moved
		// to where the move is actually observed so that a note set AFTER it,
		// about the user's own base, survives to be shown (#212).
		m.baseNote = ""
	}
	// And the base alone needs a second SOURCE as well, because the guard
	// above excludes exactly one real decision: a user who picks the HEAD
	// row (#262). Their pick changes no value -- the row is already
	// selected whenever a ref is held, and a held ref is precisely when a
	// tier's settle is out to overwrite them -- so no diff of the value can
	// see it. WorktreeField.BasePicked is that event rather than its
	// (absent) effect, reported by the one layer that receives it as a
	// click or a keystroke.
	//
	// A second source rather than a replacement, and a second source rather
	// than a second FLAG. It cannot compete with the diff the way two gates
	// would (see worktreeTouched's own note above): both only ever set the
	// one flag true, and this one is literally user input, so it is never
	// the spurious true #248 was. What it deliberately does not do is clear
	// baseNote -- that note says HEAD is in use instead of some ref, which
	// the diff's move to a real ref falsifies and a pick of HEAD does not.
	if m.worktree.BasePicked() {
		m.baseTouched = true
	}
}

// snapshotAppliedDefaults records the memory-fed fields' current values
// as "what the app put there", so noteUserEdits' next pass only reports a
// change the USER made.
//
// The base is recorded as the ref the app REQUESTED
// (WorktreeField.RequestedBase), not the one the picker is showing, and
// that distinction is #248. applyProjectDefaults resolves a remembered
// base one debounce before the `git for-each-ref` that names it, so
// SetBase holds it and the row reads HEAD meanwhile; snapshotting the row
// recorded "" for a base the app had already asked for, and the list
// landing it a moment later read as the user moving the base away from
// HEAD -- setting baseTouched with nobody having touched anything, and
// stopping per-project base memory re-applying for the rest of the
// form-open. A deferred selection IS what the app put there; it has
// simply not landed yet.
//
// The converse -- a snapshot holding a ref the picker is not showing, so
// a user who selects that same ref by hand registers as no change at all
// -- is unreachable rather than merely harmless, and the order matters:
// the row they would have to select is the one whose ABSENCE is the reason
// the ref is being held, and every path that adds a row (SetBaseItems,
// OfferBase, SetHeadBranch) goes through refreshBaseItems, which lands the
// held ref before anyone can point at it. Were it reachable it would not
// be free: baseTouched would stay false and the next project change would
// re-apply memory over a choice they had made.
//
// It is called at the end of every path that can move one of them without
// user input -- New, reactToChanges, applyProjectDefaults, and both of
// handleBaseSettled's applying branches (#212, #247) -- always
// AFTER syncDerivedInertness. That ordering used to matter for Placement
// itself: syncDerivedInertness moved it when a worktree turned on, and
// snapshotting before that call would have left the snapshot holding a
// placement the field no longer showed, reading as a user edit on the very
// next reactToChanges and permanently stopping per-project memory from
// re-applying to it. Worktree state no longer changes what Placement
// holds -- placement spec §14's inert row keeps the chip, and
// plan.EffectivePlacement applies the rule at build time -- but the
// ordering costs nothing to keep and stays, in case a future dynamic field
// reintroduces the same shape of dependency.
func (m *Model) snapshotAppliedDefaults() {
	m.appliedWorktreeOn = m.worktree.On()
	m.appliedPlacement = m.placement.Value()
	m.appliedAgentKind = m.agent.Value()
	m.appliedBaseRef = m.worktree.RequestedBase()
}

// applyProjectDefaults re-resolves v2 spec §10's layered defaults for the
// project the form now points at (key and repo, both from the debounced
// dir check) and applies each resolved value to the field that shows it --
// unless the user has already touched that field, in which case their
// choice stands. This is "per-project memory re-applies when the project
// row changes", now with the repository's own committed default (v2 spec §11)
// sitting between it and last-used.json.
//
// isGitRepo gates the worktree toggle alone: WorktreeField.SetOn is
// meaningless for a target that cannot host a worktree (the chip row is
// inert), so a remembered `true` waits for a repository rather than being
// spent on a plain directory.
//
// dir is the project row's value the check was about, which only a ⌃R⌃R
// needs: it is how the rebuilt form recognises the answer about the
// project it opened on (clearedMemory).
func (m *Model) applyProjectDefaults(dir, key string, isGitRepo bool, repo config.RepoConfig) tea.Cmd {
	m.projectKey = key
	m.repoConfig = repo
	entry, have := m.projects.Get(key)
	if m.clearedMemory(dir, key) {
		// "⌃R ⌃R clears back to the repository default" (v2 spec §10):
		// the one tier above .herdr-draft.toml is left out, as absent
		// rather than as an empty entry -- see Sources.HaveProject -- and
		// it is the only one (#135).
		entry, have = config.ProjectDefaults{}, false
	}
	// The memory key IS the repository root for a repository (projectMemoryKey:
	// canonicalised, so a symlinked checkout compares the way herdr reports
	// it), and the canonical path otherwise -- which FindSpace's second
	// pass would only misread as a root if a workspace's primary checkout
	// were that exact non-repository path, which cannot happen.
	repoRoot := ""
	if isGitRepo {
		repoRoot = key
	}
	m.resolved = defaults.Resolve(defaults.Sources{
		Config:          m.cfg,
		Global:          m.state,
		Repo:            repo,
		Project:         entry,
		HaveProject:     have,
		KnownAgentKinds: m.agentKinds,
		Workspaces:      m.workspaces,
		ProjectDir:      pathx.ExpandTilde(m.dir.Value()),
		RepoRoot:        repoRoot,
	})

	// The worktree toggle goes first, and syncDerivedInertness runs right
	// after it -- but this specific pairing is not load-bearing for
	// anything it touches. PlacementField does not need it: its setters
	// below apply whether or not it is inert (placement spec §14 keeps the
	// inert state in the field, not in its chip row, for exactly that).
	// AccountField's inert condition follows the agent KIND -- which this
	// first call reads STALE, since SetKind runs below it -- so Account's
	// correctness comes from the SECOND syncDerivedInertness call at the
	// bottom of this function (see its own comment), not this one. Nor
	// does lastWorktreeOn need this pairing: reactToChanges gates its
	// debounced title-duplicate check on `worktreeOn != m.lastWorktreeOn`,
	// but the second call resyncs it to the identical final value anyway,
	// since nothing between the two calls changes m.worktree.On(). The
	// pair is provably equivalent to the second call alone -- confirmed by
	// removing this one and re-running the full suite: still green. Left
	// in place regardless, since reordering or deleting working code for
	// a constraint that went away gains nothing.
	if isGitRepo && !m.worktreeTouched {
		m.worktree.SetOn(m.resolved.UseWorktree)
	}
	m.syncDerivedInertness()

	// The `tab in <space>` chip follows the PROJECT, not the user: it is
	// offered or withdrawn on every project change regardless of
	// placementTouched (SetSpace keeps any other chip the user chose), and
	// only the CURSOR move below is the user's to have overridden.
	m.placement.SetSpace(m.resolved.Space.Label)
	if !m.placementTouched {
		m.placement.SetValue(m.resolved.Placement)
	}
	if !m.agentTouched {
		m.agent.SetKind(m.resolved.AgentKind)
	}
	// The previous project's offer and note are about a base that is no
	// longer this form's, and a ref offered there may name nothing here --
	// unless the base is the user's own, which a project change re-applies
	// nothing to. Then it stays on offer, because that is what keeps it
	// selected: widgets.Picker keeps a vanished row's INDEX, so withdrawing
	// the row the user chose would hand them the branch that sits there next.
	offer := ""
	if m.baseTouched {
		offer = m.worktree.Base()
	}
	m.worktree.OfferBase(offer)
	m.baseNote = ""
	if !m.baseTouched {
		// The remembered base almost always arrives BEFORE the branch list
		// naming it: this runs off the debounced dir check, and the
		// `git for-each-ref` that populates the picker is a separate,
		// later round trip. SetBase holds the ref and re-applies it when
		// the list lands (see its own doc comment), which is the whole
		// reason it is not a plain SelectID here.
		m.worktree.SetBase(m.resolved.BaseRef)
	}
	// And the list is not the test of whether it can be used: it is the 50
	// newest branches. Git is, in the background -- see SettleBase, which
	// `create` calls with the same resolution (#194).
	settle := m.scheduleBaseSettle()

	// The branch follows the project too, which it did not have to before
	// v2 spec §11: branch_prefix and linear_branch_name are both per-repo now,
	// so the same title produces a different branch in a different
	// repository. Seeded, so a branch the user typed themselves still
	// stands (WorktreeField.SetBranch's own touched guard), and no touched
	// flag of this package's own is needed -- the field carries that one.
	//
	// The emptiness guard is load-bearing, not defensive:
	// gitx.BranchSlug answers an EMPTY title with a deterministic
	// "session-xxxxxxxx" rather than with nothing, so re-deriving
	// unconditionally would fill the branch input with a hash on the very
	// first dir check, before the user had typed a character.
	// reactToChanges never hits this because it only derives when the
	// title CHANGES, and a freshly opened form's title has not.
	if m.title.Value() != "" || m.linearIssueSelected {
		m.worktree.SetBranch(m.branchSuggestion(), true)
	}

	// Again, because the agent kind above drives AccountField's own inert
	// condition (spec §6 field 7, "inert while the kind is not claude").
	m.syncDerivedInertness()
	// v2 spec §11's visible half, last: it reads both the resolution
	// above and the values the calls above just applied.
	m.showRepoConfig()
	m.snapshotAppliedDefaults()
	return settle
}

// clearedMemory reports whether a dir check's answer about dir, which
// resolved to the projects.json key key, is still under a ⌃R⌃R -- to be
// resolved without that project's memory -- and moves the clear along: from
// the row's value to its key on the first answer about the value the
// rebuilt form opened on, and to nothing on the first answer about any
// other project. See clearedDir.
//
// Nothing brings an ended clear back, so returning to the cleared project
// later in the same popup re-applies its memory. That is deliberate. A
// clear is a statement about the form it rebuilt, and once the row has
// left that project the form it was about is gone; coming back is a project
// change like any other, and v2 spec §10 re-applies memory on every one.
// Keeping it cleared would have one project resolve two ways in one popup,
// depending on the path the row took to reach it, under a rule nothing on
// screen shows. Nor is it needed to protect anything the user decided: a
// value they moved after the clear is touched, and stands through every
// return (noteUserEdits). And a clear whose effect outlives the project it
// was about is #135's own shape.
func (m *Model) clearedMemory(dir, key string) bool {
	if m.clearedDir != "" {
		opened := dir == m.clearedDir
		m.clearedDir = ""
		if opened {
			m.clearedKey = key
		}
		return opened
	}
	if m.clearedKey != "" && key == m.clearedKey {
		return true
	}
	m.clearedKey = ""
	return false
}

// repoConfigLoader returns the .herdr-draft.toml reader to use --
// Deps.RepoConfig when a caller supplied one, config.LoadRepoConfig
// otherwise. Snapshotted by runDirCheck at scheduling time, like every
// other dependency a background Cmd closes over.
func (m Model) repoConfigLoader() func(string) config.RepoConfig {
	if m.deps.RepoConfig != nil {
		return m.deps.RepoConfig
	}
	return config.LoadRepoConfig
}

// repoConfigNotes is v2 spec §11's visible report: one line per key in the
// selected repository's .herdr-draft.toml that the trust model refused,
// plus the reason. Empty when there is no such file, or when everything in
// it was allowed. showRepoConfig puts these on the project row's panel.
func (m Model) repoConfigNotes() []string { return m.repoConfig.Notes }

// showRepoConfig pushes v2 spec §11's two visible pieces of the selected
// repository's own .herdr-draft.toml into the form. It is the ONLY place
// this package renders anything about that file, and it is called from the
// two paths that can change what it should say: applyProjectDefaults (a
// different project, so a different file and a fresh resolution) and
// reactToChanges (the user moving a value the file had chosen).
//
// PROVENANCE (`from .herdr-draft.toml`, v2 spec §11) goes on the panel of
// the field that SHOWS a value the file supplied -- never on the row, which
// stays quiet -- and only while the app's own application of that value
// still stands. A field the user has since moved is theirs; the touched
// flags v2 spec §10 already keeps are exactly that question, so this consults
// them rather than inventing a second answer.
//
// Two fields can carry it, and that is the complete set. The file's five
// allowed keys (config.repoAllowedKeys) are branch_prefix,
// default_worktree, default_placement, default_base and
// linear_branch_name, and every value they resolve to is shown by either
// the worktree panel or the placement panel.
//
// branch_prefix and linear_branch_name are deliberately NOT attributed.
// Neither is a value of its own: both shape the branch this package
// DERIVES (branchSuggestion), and that branch is equally the user's own
// typing the moment they edit it -- WorktreeField owns that touched flag
// and does not expose it, so there is no moment at which claiming the
// repository wrote what is on screen would be reliably true. Their effect
// is visible where it belongs, in the branch itself.
//
// The IGNORED-KEY NOTES go on the PROJECT panel instead, because they are a
// property of the FILE rather than of any one value, and the project row is
// what decides which file that is -- see form.DirField.SetNotes.
func (m *Model) showRepoConfig() {
	m.worktree.SetProvenance(repoProvenance(
		m.fromRepoConfig(defaults.FieldWorktree, m.worktreeTouched),
		m.fromRepoConfig(defaults.FieldBaseRef, m.baseTouched),
	))
	m.placement.SetProvenance(repoProvenance(
		m.fromRepoConfig(defaults.FieldPlacement, m.placementTouched),
	))
	m.dir.SetNotes(m.repoConfigNotes())
	// config.toml's own refused branch_prefix follows the resolution the same
	// way, because a repository's .herdr-draft.toml can take the prefix over
	// -- see BranchPrefixWarning. A dropped base's note joins it unguarded,
	// unlike the provenance above: it is about what the row now shows and
	// why -- the base that WENT, and HEAD in its place -- and the user is
	// one of the two who can have lost one (#212). What used to
	// stand here was a baseTouched guard, for the true half of that -- a note
	// about a TIER's base says HEAD is used, which stops being true the
	// moment the user picks their own. That is now handled where the fact is
	// known, by noteUserEdits clearing the note on the move itself, which
	// leaves this able to show a note about the user's own base too.
	notes := m.branchPrefixNotes()
	if m.baseNote != "" {
		notes = append(notes, m.baseNote)
	}
	m.worktree.SetNotes(notes)
}

// SettleBase is #194's rule 3, and the one place it is written: a base some
// tier supplied is kept if it names a commit in dir, the project, and
// otherwise falls back to the HEAD row (defaults.Resolved.WithoutBase), with
// a note saying so. The note is "" when nothing was dropped. HEAD and @
// arrive here already mapped to "" by defaults.Resolve (rule 2), and ""
// asks git nothing. Any other spelling of HEAD itself is mapped here, where
// git is already being asked (NamesHead).
//
// It exists because the base picker's branch list was the only test a
// remembered base ever met, and the list is not a test of anything: it is
// the 50 most recently committed branches (maxBaseRefs). So a branch deleted
// since, an older one, a tag, or HEAD itself all fell back to row 0 in the
// form without a word, while `create` handed the same ref to herdr as it was.
// ResolveCommit is git's own answer, `rev-parse --verify <ref>^{commit}` --
// the same question `git worktree add` asks of its start point.
//
// Both paths call it with their resolution before anything else reads the
// base: `create` at once, the form from the background check its dir check
// schedules (async.go's scheduleBaseSettle), since a `git rev-parse` does not
// belong in Update. dir is the project, which from a lane is the lane --
// where #171 resolves a base -- so a lane falls back here too instead of
// meeting that refusal. An explicit --base is not a tier's and is not
// settled: `create` refuses one that does not resolve.
func SettleBase(ctx context.Context, git commitResolver, dir string, res defaults.Resolved) (defaults.Resolved, string) {
	if res.BaseRef == "" {
		return res, ""
	}
	commit, err := git.ResolveCommit(ctx, dir, res.BaseRef)
	if err != nil {
		return res.WithoutBase(), fmt.Sprintf("ignoring base %q from %s: no such commit here; using HEAD",
			res.BaseRef, res.From[defaults.FieldBaseRef])
	}
	if NamesHead(ctx, git, dir, res.BaseRef, commit) {
		// Still the tier's choice, as a tier's HEAD is: it chose HEAD,
		// spelled another way, and nothing was dropped.
		res.BaseRef = ""
	}
	return res, ""
}

// settleChosenBase is the same question asked of a base the USER picked
// rather than one a tier supplied (#212), and it returns only the note: ""
// when ref still names a commit in dir and the caller is to keep it, the
// reason to fall back to the HEAD row otherwise.
//
// Two things separate it from SettleBase, and both are why it is a function
// of its own rather than a synthesised defaults.Resolved put through that
// one. There is no tier to name, so the note stops after the ref -- the
// user is the source, and telling them a file supplied what they chose
// themselves would be false. And "any more" is the whole point: this ref
// was in the branch list when they picked it out of it, which is what makes
// its disappearance worth a line rather than a silent fall-back.
//
// NamesHead is not asked either. The base picker offers exactly one HEAD,
// its own sentinel row, and Base() reports that as ""; every other row is a
// ref, so a ref that arrives here spelled from HEAD got there as a tier's
// offer the user moved back onto, and keeping it as spelled is right.
func settleChosenBase(ctx context.Context, git commitResolver, dir, ref string) string {
	if ref == "" {
		return ""
	}
	if _, err := git.ResolveCommit(ctx, dir, ref); err != nil {
		return fmt.Sprintf("ignoring base %q: no such commit here any more; using HEAD", ref)
	}
	return ""
}

// NamesHead reports whether ref, which names commit in dir, is HEAD itself
// spelled another way -- HEAD^0, @~0, HEAD@{0}, @{0}, anything spelled from
// HEAD or @ that names HEAD's own commit -- and so means the base picker's
// HEAD row, "", as HEAD and @ do (defaults.NormalizeBase). `create` asks it
// of --base too.
//
// It used to matter beyond tidiness. The clean gate counted a base inside
// the worktree, where each of these names the worktree itself, so a worktree
// holding work counted as having none; the popup used to drop them all to ""
// without asking, and keeping them under rule 1 would have opened that hole
// on the popup's path for the first time. The gate now counts from a commit
// resolved where the worktree was cut from (#193), so what is left is that
// these mean the HEAD row and both paths say so. Git decides rather than a
// list of spellings, because no list is complete (HEAD@{now} is one more).
//
// Only a ref spelled from HEAD or @: a branch that happens to be at HEAD's
// commit is still a branch, and HEAD~1 is another commit.
func NamesHead(ctx context.Context, git commitResolver, dir, ref, commit string) bool {
	if !headRelative(ref) {
		return false
	}
	head, err := git.ResolveCommit(ctx, dir, "HEAD")
	return err == nil && head == commit
}

// headRelative reports whether ref is spelled from HEAD or @: either alone,
// or followed by a revision suffix (~ ^ @ {). A branch called HEADroom is
// not.
func headRelative(ref string) bool {
	for _, p := range []string{"HEAD", "@"} {
		if rest, ok := strings.CutPrefix(ref, p); ok && (rest == "" || strings.ContainsRune("~^@{", rune(rest[0]))) {
			return true
		}
	}
	return false
}

// commitResolver is the one git question SettleBase asks -- a subset of both
// this package's gitSource and internal/create's GitSource, so either path
// hands over the source it already has.
type commitResolver interface {
	ResolveCommit(ctx context.Context, dir, ref string) (string, error)
}

// BranchPrefixWarning is config.toml's refused-branch_prefix warning when it
// describes res, and "" otherwise: when there is none, or when a tier above
// config.toml -- a repository's .herdr-draft.toml -- supplied the prefix
// (review of #125). The warning ends `using "<fallback>"`, and in such a
// repository that fallback is not the prefix in use, so a note saying it is
// would contradict the branch printed directly above it; the refused key
// would not have applied there even had it been valid.
//
// Exported for internal/create, which reports the same warning on stderr and
// has to decide the same way -- the reason it borrows BranchFor rather than
// restating it.
func BranchPrefixWarning(cfg config.Config, res defaults.Resolved) string {
	if res.From[defaults.FieldBranchPrefix] > defaults.TierUserConfig {
		return ""
	}
	return cfg.BranchPrefixWarning
}

// branchPrefixNotes is BranchPrefixWarning as the worktree panel's note list:
// nil, which reserves no rows, when there is nothing to say.
func (m Model) branchPrefixNotes() []string {
	if w := BranchPrefixWarning(m.cfg, m.resolved); w != "" {
		return []string{w}
	}
	return nil
}

// fromRepoConfig reports whether field's resolved value came from the
// repository's own .herdr-draft.toml AND still stands -- untouched, so what
// the form shows is what that file chose. The attribution itself is not
// recomputed here: defaults.Resolve already decided it (Resolved.From).
func (m Model) fromRepoConfig(field string, touched bool) bool {
	return !touched && m.resolved.From[field] == defaults.TierRepoConfig
}

// repoProvenance is the source name a field's SetProvenance renders
// `from <source>` out of: the repo config file when any of the values that
// field's panel shows came from it, "" otherwise. One line covers a panel,
// so one attributed value is enough to earn it.
func repoProvenance(attributed ...bool) string {
	for _, ok := range attributed {
		if ok {
			return defaults.TierRepoConfig.String()
		}
	}
	return ""
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
//   - path -> path, a DIFFERENT parent: drop the pool for the same reason,
//     and this one matters more. The previous directory's children look
//     like plausible children of what was just typed, and DirField would
//     not merely display them: it would SELECT one (Value() feeds the
//     submitted project directory) and Tab-complete from them, assembling
//     a path out of a directory that was never listed. atrium cannot
//     exhibit this -- its read is synchronous and its memo is keyed on the
//     current directory -- so the window is a consequence of making the
//     listing asynchronous, and it closes here.
//   - path -> path, the SAME parent: nothing. Typing "he" after
//     "~/Projects/" re-ranks the listing DirField already holds --
//     atrium's own per-directory memoization, expressed here as a diff on
//     browseDir rather than a cache inside the field.
//   - path -> fragment: put the project pool back, and invalidate any
//     listing still in flight (bumping the browse counter) so a slow
//     os.ReadDir cannot land afterwards and clobber it.
func (m *Model) reactToTypedDir(typed string) []tea.Cmd {
	if !form.LooksLikePath(typed) {
		if m.browseDir == "" {
			return nil // already in fragment mode; the pool is already right
		}
		m.browseDir = ""
		m.reqs.browse++
		m.supplyDirCandidates(m.projectCandidates)
		return nil
	}

	dir, _ := form.SplitPath(typed)
	if m.browseDir == dir {
		return nil
	}
	m.browseDir = dir
	m.supplyDirCandidates(nil)
	return []tea.Cmd{m.scheduleBrowse(dir)}
}

// supplyDirCandidates hands DirField a new candidate pool under the next
// version of the single monotonic counter every such call shares -- see
// dirCandVersion's own doc comment for why there is only one.
func (m *Model) supplyDirCandidates(candidates []string) {
	m.dirCandVersion++
	m.dir.SetCandidates(m.dirCandVersion, candidates)
}

// syncDerivedInertness re-applies the two DYNAMIC inert conditions (spec
// §6 field 7 -- "inert while X", checked continuously, unlike the STATIC
// preconditions that gate whether a field is constructed at all):
// AccountField's, from AgentField's current state, and PlacementField's,
// from whether the plan will have a worktree (placement spec §14). Cheap
// and synchronous (no I/O), so it is safe to call unconditionally on every
// reactToChanges pass and from the async dirResultMsg handler (which
// moves WorktreeField.On() outside of reactToChanges' own diff, via
// SetOn -- see handleDirResult), rather than needing its own
// diff-gating.
//
// Placement follows Enabled() && On(), the exact conjunction
// buildPlanInput uses for UseWorktree, and not On() alone: a project that
// is not a repository keeps the toggle's "on" underneath its inert
// worktree row, and its plan still honours the placement.
func (m *Model) syncDerivedInertness() {
	m.placement.SetWorktreeOn(m.worktree.Enabled() && m.worktree.On())
	m.lastWorktreeOn = m.worktree.On()
	if m.account != nil {
		m.account.SetAgentIsClaude(m.agent.Value() == claudeKind)
	}
	// The options row follows the agent row (agent-options spec §7): a
	// kind it has not shown yet is built from the declaration and seeded
	// from config.toml, and one it has shown gets back what it held.
	// OptionsField.SetKind is a no-op for the kind already showing, so
	// this is cheap on every routed message.
	if kind := m.agent.Value(); kind != m.options.Kind() {
		m.options.SetKind(kind, kindOptions(m.cfg, kind))
	}
}

// kindOptions is everything the options row needs about one agent kind:
// the declaration as the form's own specs, the config.toml seed and its
// provenance -- both from defaults.AgentOptions, the one place the chain
// exists -- and extra_args with what it already pins.
func kindOptions(cfg config.Config, kind string) form.KindOptions {
	seed, from := defaults.AgentOptions(cfg, kind)
	source := ""
	for _, t := range from {
		if t != defaults.TierBuiltin {
			source = t.String()
		}
	}
	extra := cfg.Agents.ExtraArgs[kind]
	return form.KindOptions{
		Specs:      optionSpecs(kind),
		Seed:       seed,
		SeedSource: source,
		ExtraArgs:  extra,
		Pinned:     agentopts.Pinned(kind, extra),
	}
}

// optionSpecs translates a kind's agentopts declaration into the form's
// own OptionSpec values, which is how the form renders options without
// importing the package that declares them.
func optionSpecs(kind string) []form.OptionSpec {
	var out []form.OptionSpec
	for _, o := range agentopts.For(kind) {
		spec := form.OptionSpec{
			Name:     o.Name,
			Label:    o.Label,
			Flag:     o.Flag,
			FreeText: o.FreeText,
			Valid:    o.ValidFree,
			Row:      o.Row,
		}
		for _, c := range o.Choices {
			spec.Choices = append(spec.Choices, form.OptionChoice{Value: c.Value, Label: c.Label})
		}
		out = append(out, spec)
	}
	return out
}

// agentOptions is the options row's values for the plan, but only while
// the row is showing the kind the agent row will launch. syncDerivedInertness
// keeps the two together after every routed message, so a mismatch means a
// submit arrived before the sync did. Options chosen for another kind must
// never ride along with this one; the launched kind's own config.toml
// default is what the row would have shown, and what `create` would send.
func (m Model) agentOptions() agentopts.Values {
	if kind := m.agent.Value(); m.options.Kind() != kind {
		vals, _ := defaults.AgentOptions(m.cfg, kind)
		return vals
	}
	return m.options.Values()
}

// refreshFormContext sets the header's right-hand text (v2 spec §4): live
// context for the SELECTED project -- its directory name and the branch
// currently checked out there -- deliberately NOT the invoking workspace,
// which is what the popup was launched from rather than what the session
// being created will run in.
//
// The name is the selected path's own last segment. For a repository
// checkout that is the repository name; for a linked worktree it is the
// worktree's name, which is the more useful of the two to see here (it is
// the directory the new session will actually sit beside).
//
// NON-GIT PROJECTS SHOW THE NAME ALONE, with no separator and no branch
// half. The spec does not settle this, and the alternative --
// "myproject · not a repository" -- says in the header what the project
// row's own inert cell already says one line down, in a place reserved
// for context rather than verdicts. The branch half exists when there is
// a branch; otherwise there is simply less to say.
//
// A detached HEAD is the same case: gitx.CurrentBranch reports "" rather
// than an error, so the header quietly drops to the name alone rather
// than inventing a ref.
func (m *Model) refreshFormContext() {
	name := lastPathSegment(m.dir.Value())
	if name == "" {
		m.form.SetContext("")
		return
	}
	if branch := m.worktree.HeadBranch(); branch != "" {
		name += " · " + branch
	}
	m.form.SetContext(name)
}

// lastPathSegment is the final component of a slash-separated path, with
// any trailing separator ignored -- filepath.Base's answer for the shapes
// this form deals in, without importing path/filepath for one call whose
// input is already a cleaned absolute path.
func lastPathSegment(p string) string {
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// View renders the form and enables the popup chrome bubbletea v2 controls
// entirely through the returned tea.View (v2.0.9 has no
// tea.WithAltScreen()/mouse-enabling tea.NewProgram option at all --
// verified directly against charm.land/bubbletea/v2@v2.0.9's options.go,
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

// OrderedAgentKinds builds AgentField.SetKinds' own input list: favorites
// first (deduped, empty entries dropped), then every knownAgentKinds entry
// not already present -- the carried requirement's exact wording ("config
// [agents] favorites, then the remaining kinds"). AgentField itself treats
// index 0 as spec §12's own configured default, so favorites[0] (when
// favorites is non-empty) IS the default; config.Config's own defaults()
// already sets Agents.Favorites to ["claude"] when the user's config omits
// it entirely, so this is never called with an empty list in practice.
//
// Exported for the headless `create` command (v2 spec §13), which needs the
// IDENTICAL list rather than a similar one: it is what
// defaults.Sources.KnownAgentKinds validates each tier's remembered kind
// against, so a command resolving against a different list would silently
// pick a different agent than the form does from the same config.
func OrderedAgentKinds(favorites []string) []string {
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
//
// A LINKED worktree's space -- a lane -- opens on its own checkout instead
// (#171). herdr's repo_root for that space names the primary checkout, and
// `create` run from the lane defaults to the lane, its working directory, so
// following repo_root here would have the two paths build different
// sessions from one place: another project, another base, and without a
// worktree another directory.
func defaultProjectDir(ctx herdrc.Context) string {
	if w := ctx.Worktree; w != nil && w.IsLinkedWorktree && w.CheckoutPath != "" {
		return w.CheckoutPath
	}
	if ctx.Worktree != nil && ctx.Worktree.RepoRoot != "" {
		return ctx.Worktree.RepoRoot
	}
	return ctx.WorkspaceCwd
}

// buildDirCandidates builds DirField's initial candidate pool (spec §6
// field 2): current context cwd/repo root, then open herdr workspace
// cwds/repo roots (only worktree-backed workspaces carry a resolvable cwd
// at all -- herdrc.WorkspaceInfo has no plain Cwd field for a non-worktree
// workspace, a real data-shape limitation designed around rather than an
// oversight: a plain, non-worktree open workspace therefore contributes
// nothing here beyond whatever the current context's own cwd already
// supplies), then recents (state dir). DirField.SetCandidates
// applies its own dedupePaths, so duplicates across these three sources
// (e.g. the current workspace appearing both as defaultProjectDir and in
// Workspaces) collapse to their first occurrence, preserving order.
func buildDirCandidates(ctx herdrc.Context, workspaces []herdrc.WorkspaceInfo, recents []string) []string {
	var out []string
	if d := defaultProjectDir(ctx); d != "" {
		out = append(out, d)
	}
	// From a lane, the repository it belongs to comes next: the default
	// before #171, one keystroke away. Anywhere else it is the default
	// itself, and the dedupe drops it.
	if ctx.Worktree != nil && ctx.Worktree.RepoRoot != "" {
		out = append(out, ctx.Worktree.RepoRoot)
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

// RenderPromptTemplate composes a chosen Linear issue's seeded prompt text
// (spec §10), using tmpl (config.Config.Linear.PromptTemplate) when
// non-empty, or defaultPromptTemplate otherwise.
//
// Exported for v2 spec §13's headless `create`: `create --issue` seeds its
// prompt from the same template through the same substitutions, and a
// second copy of them would be a second answer to "what does a
// Linear-seeded session start with".
//
// The result goes through plan.SanitizePrompt, here and not in each
// caller, because an issue's title and description are somebody else's
// text on their way to being typed into an agent's pane (#183). The
// popup's textarea would drop the escape sequences anyway; `create` has
// no textarea, and a rule applied where the text is made cannot be
// forgotten by the next thing to use it. Rendering it clean also means
// the textarea is handed one line break per CRLF rather than two.
func RenderPromptTemplate(tmpl string, iss linear.Issue) string {
	if tmpl == "" {
		tmpl = defaultPromptTemplate
	}
	r := strings.NewReplacer(
		"{identifier}", iss.Identifier,
		"{title}", iss.Title,
		"{url}", iss.URL,
		"{description}", iss.Description,
	)
	return plan.SanitizePrompt(r.Replace(tmpl))
}
