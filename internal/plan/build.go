// Package plan builds an ordered list of herdr operations from a completed
// creation form (spec §9). Build is pure logic: it performs no I/O, runs no
// subprocess, and never calls herdr itself -- internal/plan/exec.go (Task
// 13) is the executor that actually runs the returned ops and threads
// step-1 output (workspace/tab/pane ids) into the ops that need it.
package plan

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ZviBaratz/herdr-draft/internal/gitx"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
)

// Placement selects where a session's AGENT PANE attaches relative to the
// invoking pane (spec §9, extended by placement spec §5.3): under a plain
// (non-worktree) creation it is the only op; under a worktree it is a
// second op appended after the worktree's own creation, which still always
// happens and still always gets its own new workspace -- Placement now
// decides where the AGENT runs, not whether the checkout gets a space of
// its own.
type Placement int

const (
	// PlacementNewSpace opens a new workspace.
	PlacementNewSpace Placement = iota
	// PlacementTabHere opens a new tab in the invoking workspace
	// (Input.Ctx.WorkspaceID).
	PlacementTabHere
	// PlacementSplitHere splits the invoking pane
	// (Input.Ctx.FocusedPaneID).
	PlacementSplitHere
	// PlacementTabIn opens a new tab in a workspace the caller NAMES
	// (Input.Space) rather than the invoking one -- #128: the repository's
	// own space, when it already has one, or any workspace `--workspace`
	// picks. It is PlacementTabHere with a different target and is
	// executed by the same OpTabCreate; only where the workspace id comes
	// from differs.
	PlacementTabIn
)

// LaunchMode selects how a pinned claude account reaches the pane.
//
// The zero value is LaunchClauthStart, and that is load-bearing rather than
// alphabetical: it is what every caller written before this field existed
// already means, and it is the mode that means the same thing on every
// machine that has clauth.
type LaunchMode int

const (
	// LaunchClauthStart types `clauth start <profile> -- <extra args>`.
	// clauth builds the per-profile CLAUDE_CONFIG_DIR itself and execs claude
	// under it.
	LaunchClauthStart LaunchMode = iota
	// LaunchWrapper types `CLAUDE_CONFIG_DIR=<dir> claude <extra args>`,
	// leaving the pane's own login shell to resolve `claude`.
	//
	// Opt-in only (`[clauth] launch = "wrapper"`), and the README says what it
	// is opting into: on a machine whose shell defines a `claude` FUNCTION,
	// this reaches that function -- which is what can register a session
	// holder and launch through a team-lead helper, neither of which
	// `clauth start` has ever done. On every other machine `claude` is the
	// plain binary: the credential is still isolated, and everything else the
	// wrapper was for is silently absent. That asymmetry is why this is not
	// the default.
	LaunchWrapper
)

// ClaudeConfigDirVar is the environment variable both launch modes ultimately
// set -- clauth sets it itself under LaunchClauthStart, and LaunchWrapper
// sets it as a shell assignment prefix.
const ClaudeConfigDirVar = "CLAUDE_CONFIG_DIR"

// Input is the creation form's output: everything Build needs to produce an
// ordered op list. Build performs no I/O, so every fact that would
// otherwise require a lookup (whether ProjectDir is a git repo, the
// invoking pane/workspace/tab) must already be resolved by the caller and
// supplied here.
type Input struct {
	ProjectDir, Title, Branch, BaseRef string
	UseWorktree                        bool
	// IsGitRepo reports whether ProjectDir is inside a git working tree
	// (gitx.IsGitRepo). Only consulted when UseWorktree is set: a worktree
	// cannot be created outside a git repository, so Build rejects
	// UseWorktree && !IsGitRepo.
	IsGitRepo bool
	Placement Placement
	// Space is the open workspace PlacementTabIn puts the agent's tab in
	// (FindSpace's answer, or an explicit --workspace). Consulted for that
	// placement only; Build refuses PlacementTabIn with an empty Space,
	// because guessing a destination is the failure requireContext exists
	// to refuse for the `here` placements, one layer down.
	Space     Space
	AgentKind string
	ExtraArgs []string
	// AccountPin is a clauth account pin, or "" for the active/unpinned
	// account. Pinning is only valid when AgentKind == "claude" (spec
	// §6.7); Build rejects any other combination.
	AccountPin string
	// AccountLaunch selects how AccountPin reaches the pane; the zero value is
	// LaunchClauthStart. Consulted only when AccountPin is set and AgentKind
	// is claude.
	AccountLaunch LaunchMode
	// AccountConfigDir is the picked profile's CLAUDE_CONFIG_DIR, when the
	// caller has one -- an account picker's `config_dir`. It is the only thing
	// that makes LaunchWrapper possible, and a caller with no config dir gets
	// LaunchClauthStart back regardless of what it asked for (launchOps).
	//
	// A plan.Input field rather than an ExecOpts one because it is part of
	// WHAT is built, not of how a runtime waits: the same pin and the same
	// config dir must produce the same op list from the popup and from
	// `create`, which is what equivalence_test.go asserts.
	AccountConfigDir string
	// Launcher is the argv template that starts a pinned claude account,
	// with config.AccountPlaceholder standing for AccountPin. Empty means
	// the built-in default, so a caller that does not set it keeps the
	// argv this package built before the key existed. Only consulted on
	// the pinned-claude path, the one place AccountPin is used at all.
	Launcher []string

	Prompt                          string
	Ctx                             herdrc.Context
	DetectionTimeout, PromptTimeout time.Duration
	TrustRepository                 bool
}

// OpKind identifies which herdr operation an Op performs.
type OpKind int

const (
	OpWorktreeCreate OpKind = iota
	OpWorkspaceCreate
	OpTabCreate
	OpPaneSplit
	OpAgentStart
	OpClauthLaunch
	OpAwaitDetection
	OpAgentPrompt
)

// String names an OpKind for progress lines and test failure output.
func (k OpKind) String() string {
	switch k {
	case OpWorktreeCreate:
		return "OpWorktreeCreate"
	case OpWorkspaceCreate:
		return "OpWorkspaceCreate"
	case OpTabCreate:
		return "OpTabCreate"
	case OpPaneSplit:
		return "OpPaneSplit"
	case OpAgentStart:
		return "OpAgentStart"
	case OpClauthLaunch:
		return "OpClauthLaunch"
	case OpAwaitDetection:
		return "OpAwaitDetection"
	case OpAgentPrompt:
		return "OpAgentPrompt"
	default:
		return fmt.Sprintf("OpKind(%d)", int(k))
	}
}

// Op is one step of a creation plan. Exactly one request field is populated
// per Kind: Worktree for OpWorktreeCreate, Workspace for
// OpWorkspaceCreate, Tab for OpTabCreate, Split for OpPaneSplit, Agent for
// OpAgentStart, RunArgv for OpClauthLaunch, Prompt for OpAgentPrompt.
// OpAwaitDetection populates only Timeout, which OpAgentStart also carries
// for the trust-dialog wait (#115). Requests that depend on step-1
// output (pane/workspace ids the topology-creation op returns) are left
// with those fields empty -- the executor (Task 13) fills them in from the
// step-1 result before running each op.
type Op struct {
	Kind  OpKind
	Label string // progress line, e.g. "creating worktree"

	Worktree  *herdrc.WorktreeCreateReq  // OpWorktreeCreate
	Workspace *herdrc.WorkspaceCreateReq // OpWorkspaceCreate
	Tab       *herdrc.TabCreateReq       // OpTabCreate
	Split     *herdrc.PaneSplitReq       // OpPaneSplit
	Agent     *herdrc.AgentStartReq      // OpAgentStart
	RunArgv   []string                   // OpClauthLaunch: argv for Runner.PaneRun
	RunEnv    []herdrc.EnvVar            // OpClauthLaunch: shell assignment prefix for Runner.PaneRun
	Prompt    *herdrc.AgentPromptReq     // OpAgentPrompt
	Timeout   time.Duration              // OpAwaitDetection, and OpAgentStart's #115 fallback

	// AgentKind is OpAwaitDetection's other field: the kind being waited
	// for, carried for MESSAGES only and never for behaviour. Path B is
	// claude-only today (launchOps gates on it), so exec.go could have
	// written "claude" into the blocked-agent explanation and been right;
	// it would have been right by coincidence, and silently wrong the day
	// another kind gains an account-pinned launch.
	AgentKind string

	// CwdFromCheckout tells Execute to fill this op's Cwd from the SPACE
	// op's CheckoutPath, which Build cannot know: only OpTabCreate and
	// OpPaneSplit ever set it, and only as placement spec §5.3's
	// build-time placement op appended after a worktree create. Build
	// performs no I/O and never touches a Runner (CLAUDE.md), so it
	// cannot resolve a checkout path itself -- it states the INTENT and
	// Execute resolves it once the worktree op's own response is in hand.
	CwdFromCheckout bool
}

// defaultSplitDirection is the direction Build requests for
// PlacementSplitHere. herdr's own pane-split probes (Task 2/2b's live
// checkpoints) consistently used "right"; Build follows that precedent as
// its default.
const defaultSplitDirection = "right"

// claudeAgentKind is the only AgentKind for which account pinning
// (Input.AccountPin) is meaningful (spec §6.7).
const claudeAgentKind = "claude"

// Build maps a completed creation form to an ordered list of herdr
// operations. It performs no I/O and never touches a herdr Runner.
func Build(in Input) ([]Op, error) {
	if strings.TrimSpace(in.Title) == "" {
		return nil, fmt.Errorf("plan: build: title is required")
	}
	if in.UseWorktree && !in.IsGitRepo {
		return nil, fmt.Errorf("plan: build: worktree creation requires a git repository at %q", in.ProjectDir)
	}
	if in.AccountPin != "" && in.AgentKind != claudeAgentKind {
		return nil, fmt.Errorf("plan: build: account pinning is only supported for the %q agent kind, got %q", claudeAgentKind, in.AgentKind)
	}
	if in.Placement == PlacementTabIn && in.Space.WorkspaceID == "" {
		return nil, fmt.Errorf("plan: build: placement tab-in needs an open workspace to place the tab in, and none was named")
	}

	ops := append(topologyOp(in), launchOps(in)...)
	if in.Prompt != "" {
		ops = append(ops, Op{
			Kind:  OpAgentPrompt,
			Label: "sending prompt",
			Prompt: &herdrc.AgentPromptReq{
				Text:        in.Prompt,
				WaitTimeout: in.PromptTimeout,
			},
			// Not the prompt's own wait (that is WaitTimeout above): this is
			// the DETECTION budget, used only when the send is refused
			// because the pane is showing a dialog and Execute waits for it
			// to clear (#115). It is how long an unreadable pane may stay
			// unreadable before the agent behind it counts as gone, and how
			// long readiness gets once the screen is clear.
			Timeout: in.DetectionTimeout,
		})
	}
	return ops, nil
}

// topologyOp returns the op or ops that establish where a session lives:
// the SPACE (always first, and always what makes the checkout exist when
// UseWorktree is set) and, when Placement asks for something other than
// a worktree's own new workspace, a second op that places the AGENT'S
// pane relative to the invoking pane instead (placement spec §5.1/§5.3).
//
// Before placement spec: worktree creation always won over Placement
// outright, and a worktree was always a new workspace regardless of where
// the form said to place it. That is superseded -- see the design doc's
// §12 for the exact sentence and where it lived.
func topologyOp(in Input) []Op {
	if in.UseWorktree {
		// Empty Cwd with CwdFromCheckout set: the checkout does not exist
		// yet, so only Execute can fill it in.
		placement, hasPlacement := placementOp(in, "", true)
		worktree := Op{
			Kind:  OpWorktreeCreate,
			Label: "creating worktree",
			Worktree: &herdrc.WorktreeCreateReq{
				Cwd:             in.ProjectDir,
				Branch:          in.Branch,
				Base:            in.BaseRef,
				Label:           in.Title,
				Focus:           !hasPlacement,
				TrustRepository: in.TrustRepository,
			},
		}
		if !hasPlacement {
			return []Op{worktree}
		}
		return []Op{worktree, placement}
	}

	if placement, ok := placementOp(in, in.ProjectDir, false); ok {
		return []Op{placement}
	}
	return []Op{{
		Kind:  OpWorkspaceCreate,
		Label: "creating workspace",
		Workspace: &herdrc.WorkspaceCreateReq{
			Cwd:   in.ProjectDir,
			Label: in.Title,
			Focus: true,
		},
	}}
}

// placementOp returns the op that attaches the agent's pane relative to the
// invoking pane -- (Op{}, false) for PlacementNewSpace, which asks for a
// space of its own rather than a position beside anything.
//
// The two callers differ only in where the new pane's cwd comes from, which
// is the whole reason this is one function rather than two: a plain creation
// knows the directory outright (in.ProjectDir), while under a worktree only
// Execute can know it -- the checkout does not exist until herdr makes it --
// so the op carries an empty Cwd plus CwdFromCheckout and Execute fills it
// from the space op's own CheckoutPath (placement spec §5.1/§5.3). Build
// performs no I/O and never touches a Runner (CLAUDE.md); it states the
// INTENT and Execute resolves it.
//
// Workspace/PaneID name the INVOKING pane's own workspace/pane (Input.Ctx)
// for the two `here` placements, and the NAMED workspace (Input.Space) for
// PlacementTabIn -- never a worktree's: placing the agent somewhere other
// than the worktree's own space is exactly what this op is for.
func placementOp(in Input, cwd string, cwdFromCheckout bool) (Op, bool) {
	tab := func(workspace string) (Op, bool) {
		return Op{
			Kind:  OpTabCreate,
			Label: "creating tab",
			Tab: &herdrc.TabCreateReq{
				Workspace: workspace,
				Cwd:       cwd,
				Label:     in.Title,
				Focus:     true,
			},
			CwdFromCheckout: cwdFromCheckout,
		}, true
	}
	switch in.Placement {
	case PlacementTabHere:
		return tab(in.Ctx.WorkspaceID)
	case PlacementTabIn:
		return tab(in.Space.WorkspaceID)
	case PlacementSplitHere:
		return Op{
			Kind:  OpPaneSplit,
			Label: "splitting pane",
			Split: &herdrc.PaneSplitReq{
				PaneID:    in.Ctx.FocusedPaneID,
				Direction: defaultSplitDirection,
				Cwd:       cwd,
				Focus:     true,
			},
			CwdFromCheckout: cwdFromCheckout,
		}, true
	default:
		return Op{}, false
	}
}

// defaultClauthLauncher returns the launcher argv used when Input.Launcher
// is empty. config.DefaultClauthLauncher holds the same value for a
// config.toml that omits the key; TestDefaultLauncherMatchesConfigDefault
// holds the two together, because this package is a pure builder and does
// not import config to ask.
func defaultClauthLauncher() []string {
	return []string{"clauth", "start", accountPlaceholder, "--"}
}

// accountPlaceholder mirrors config.AccountPlaceholder. See
// defaultClauthLauncher for why it is duplicated rather than imported.
const accountPlaceholder = "{account}"

// resolveLauncher substitutes account into a launcher template, returning a
// FRESH slice.
//
// The copy is not defensive habit: Build runs once per submit against a
// template the caller holds, and substituting in place would leave the
// previous account baked into it -- so the second launch of a session would
// silently reuse the first one's account. Substitution is textual and
// per element so a placeholder may share an element with other text
// (`sh -c "exec claude-as {account}"` is accepted).
//
// That shape is accepted, not recommended, and the reason is worth stating
// because an earlier version of this comment recommended it: ExtraArgs are
// appended AFTER the whole template, so with `["sh","-c","exec claude-as
// {account}"]` and `[agents.extra_args]` of `--model opus`, the argv becomes
// `sh -c "exec claude-as work" --model opus` and those two words land as the
// inner shell's $0 and $1 -- silently never reaching the agent, while the
// launch step still reports ok (#72's shape again: the step covers the
// typing, not the command). Pinned by
// TestLaunchOps_ShCLauncherStrandsExtraArgs. A wrapper that must receive
// ExtraArgs has to be a plain program or a function name, not an `sh -c`
// string.
//
// A template reaching here has been validated by config.Load, which
// guarantees exactly one placeholder. An UNVALIDATED one -- a caller
// building Input by hand -- is still substituted rather than rejected:
// Build is a pure builder and has no channel for a reason, and refusing
// here would turn a config problem into a failed launch with no
// explanation. Zero placeholders therefore yields the template unchanged,
// which the config-load warning is what prevents reaching a real pane.
func resolveLauncher(template []string, account string) []string {
	if len(template) == 0 {
		template = defaultClauthLauncher()
	}
	out := make([]string, len(template))
	for i, a := range template {
		out[i] = strings.ReplaceAll(a, accountPlaceholder, account)
	}
	return out
}

// launchOps returns the op(s) that start the agent. A pinned claude
// account launches through Input.Launcher (a plain shell command herdr
// types into the pane) and, unlike `herdr agent start`, does not itself
// wait for detection -- so it is followed by an explicit OpAwaitDetection.
//
// RunArgv is a plain, UNQUOTED argv, exactly like AgentStartReq.ExtraArgs
// beside it: CLIRunner.PaneRun quotes for the pane's shell, since it is
// the layer that knows herdr types this rather than exec'ing it (#72).
// Quoting here instead would put transport knowledge in a pure builder and
// would corrupt the value for the other path. Every
// other case starts the agent directly via `herdr agent start`, which
// performs its own detection wait server-side.
func launchOps(in Input) []Op {
	if in.AccountPin != "" && in.AgentKind == claudeAgentKind {
		runArgv, runEnv, downgraded := clauthLaunchCommand(in)
		label := "typing the clauth launch"
		if downgraded {
			// The substitution said out loud, on the step where it happens.
			// This is `create`'s half of it -- the verb prints op labels one
			// per line to stderr; the popup's half is async.go's
			// submitStepDetail, which has its own value column to say it in.
			label += " (wrapper mode had no config dir)"
		}
		return []Op{
			{
				Kind: OpClauthLaunch,
				// "typing", not "launching", and that is the whole of #72's
				// first item: this op only puts a command line into the
				// pane's shell, and its success means the text was sent
				// (runOK covers the typing, not the command). A shell can
				// still refuse what it was handed -- an unquoted glob used
				// to do exactly that -- so a step labelled "launching
				// claude" reporting ok was claiming something it cannot
				// know. The OpAwaitDetection that always follows is what
				// confirms an agent; read the two rows as one launch.
				Label:   label,
				RunArgv: runArgv,
				RunEnv:  runEnv,
			},
			{
				Kind:      OpAwaitDetection,
				Label:     "waiting for agent detection",
				Timeout:   in.DetectionTimeout,
				AgentKind: in.AgentKind,
			},
		}
	}
	return []Op{
		{
			Kind:  OpAgentStart,
			Label: "starting agent",
			Agent: &herdrc.AgentStartReq{
				Name:      AgentName(in.Title),
				Kind:      in.AgentKind,
				ExtraArgs: in.ExtraArgs,
			},
			// Path A does its detection waiting server-side, so this
			// budget is unused unless the start comes back BLOCKED and
			// Execute waits through the dialog (#115) -- where it is the
			// ordinary detection budget the wait falls back on once the
			// dialog stops being reported. Set here rather than defaulted
			// in Execute so both launch paths take their detection number
			// from the same Input field.
			Timeout: in.DetectionTimeout,
		},
	}
}

// clauthLaunchCommand is the pinned-account command line and its environment
// prefix, for the mode in.AccountLaunch asks for.
//
// The two mechanisms meet here, and the branch is the whole of it: wrapper
// mode conveys the account as a credential directory in the environment, and
// everything else conveys it as a word in the argv, through Input.Launcher's
// template (`[clauth] launcher`). config.ClauthConfig.Launch's doc comment
// explains why those cannot be one key.
//
// LaunchWrapper WITHOUT a config dir becomes the argv mechanism, and that is
// the honest fallback rather than an omission: a bare `claude` with no
// CLAUDE_CONFIG_DIR launches under whatever credential is machine-global,
// which is the exact opposite of the pin the user asked for. Note that the
// fallback goes through resolveLauncher rather than a second hardcoded `clauth
// start`: a hardcoded one here is exactly how `[clauth] launcher` would have
// become inert for every pinned launch, which is what the first draft of this
// merge did.
//
// It returns whether it substituted, which is the third result's whole job.
// This comment used to end "Build is pure and cannot report the substitution;
// the caller does, on the launch step" -- and the caller did not. The step
// named the command line typed and never said a requested wrapper launch had
// been downgraded, so the one mode the user had to opt into could turn itself
// off in silence. Purity was never the obstacle: a bool travelling out of a
// function that performs no I/O is still no I/O, and both callers (launchOps'
// own Label for `create`, app's submitStepDetail for the popup) now say it.
//
// Both spellings pass ExtraArgs UNQUOTED, exactly as before: CLIRunner.PaneRun
// is the layer that knows herdr types this rather than execing it, and it
// quotes there (#72).
func clauthLaunchCommand(in Input) (argv []string, env []herdrc.EnvVar, downgraded bool) {
	if in.AccountLaunch == LaunchWrapper && in.AccountConfigDir != "" {
		return append([]string{"claude"}, in.ExtraArgs...),
			[]herdrc.EnvVar{{Name: ClaudeConfigDirVar, Value: in.AccountConfigDir}}, false
	}
	return append(resolveLauncher(in.Launcher, in.AccountPin), in.ExtraArgs...),
		nil, in.AccountLaunch == LaunchWrapper
}

// maxAgentNameLen is AgentName's own output cap (spec: "clamp to 30 runes
// to leave room for a 2-char dedupe suffix"). herdr's agent-name pattern
// itself allows one more rune ([a-z][a-z0-9_-]{0,31}, 32 total) -- the
// 2-rune gap below that is reserved for a caller-appended dedupe suffix
// (e.g. "-2") on a naming conflict, which is outside Build's scope.
const maxAgentNameLen = 30

// AgentName derives a herdr agent name from a creation form's Title. herdr
// agent names must match [a-z][a-z0-9_-]{0,31} (agent-automation.mdx:38):
// AgentName sanitizes title like a branch slug (gitx.SanitizeBranch),
// prefixes "s-" when the result's first rune is not a lowercase letter
// (SanitizeBranch only ever emits [a-z0-9-], so this covers a leading
// digit or an empty/all-symbol title), clamps to maxAgentNameLen runes,
// then trims any trailing "-"/"_" the clamp exposed -- names surface in the
// UI, and a hyphen dangling off a hard truncation reads as broken rather
// than intentional.
func AgentName(title string) string {
	slug := withAgentNamePrefix(gitx.SanitizeBranch(title))

	runes := []rune(slug)
	if len(runes) > maxAgentNameLen {
		runes = runes[:maxAgentNameLen]
	}

	clamped := strings.TrimRight(string(runes), "-_")
	if clamped == "" {
		// The prefixed slug always starts with a lowercase letter (see
		// withAgentNamePrefix), which TrimRight never removes, so this is
		// unreachable with the current maxAgentNameLen -- kept as a
		// defensive fallback (e.g. against a future maxAgentNameLen of 0)
		// rather than ever returning an empty, regex-invalid name.
		return withAgentNamePrefix("")
	}
	return clamped
}

// maxHerdrAgentNameLen is the longest name herdr's own agent-name pattern
// accepts: [a-z][a-z0-9_-]{0,31}, 32 runes in total
// (agent-automation.mdx:38).
const maxHerdrAgentNameLen = 32

// AgentNameWithSuffix returns name with a numeric dedupe suffix appended
// -- spec §9's "Name = title slug (deduped; DuplicateName retried with
// suffix)". maxAgentNameLen above has always reserved two runes for
// exactly this, but nothing ever appended one: herdr answers a colliding
// name with error code agent_name_taken
// (herdr:src/app/agents.rs:165,266), which is NOT agent_pane_busy, so
// exec.go's busy retry passed it straight through and the whole submit
// failed at step 2 -- after the worktree/workspace had already been
// created. exec.go's startAgentWithDedupe is the caller this function
// exists for.
//
// The result always stays inside herdr's own [a-z][a-z0-9_-]{0,31}: the
// base name is trimmed (and any trailing "-"/"_" that trim exposes
// removed, matching AgentName's own rule) whenever the suffix would push
// it past 32 runes, so even a suffix wider than the two runes
// maxAgentNameLen reserved stays legal.
func AgentNameWithSuffix(name string, n int) string {
	suffix := "-" + strconv.Itoa(n)

	runes := []rune(name)
	if room := maxHerdrAgentNameLen - len([]rune(suffix)); len(runes) > room {
		if room < 1 {
			room = 1
		}
		runes = runes[:room]
	}

	trimmed := strings.TrimRight(string(runes), "-_")
	if trimmed == "" {
		// Unreachable for any AgentName output (which always begins with a
		// lowercase letter TrimRight never removes) -- a defensive fallback
		// rather than ever returning a bare, regex-invalid suffix.
		trimmed = withAgentNamePrefix("")
	}
	return trimmed + suffix
}

// withAgentNamePrefix prefixes slug with "s-" when its first rune is not a
// lowercase letter -- gitx.SanitizeBranch only ever emits [a-z0-9-], so
// this covers a leading digit or an empty/all-symbol slug. The prefixed
// result always starts with a lowercase letter.
func withAgentNamePrefix(slug string) string {
	first, _ := utf8.DecodeRuneInString(slug)
	if first < 'a' || first > 'z' {
		return "s-" + slug
	}
	return slug
}
