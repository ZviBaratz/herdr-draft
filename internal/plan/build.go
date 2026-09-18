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

	"github.com/ZviBaratz/herdr-draft/internal/agentopts"
	"github.com/ZviBaratz/herdr-draft/internal/gitx"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
)

// Placement selects where a session WITHOUT a worktree attaches relative
// to the invoking pane (spec §9). A worktree session always runs in the
// worktree's own new space (placement spec §14): callers route the
// placement through EffectivePlacement, and Build refuses any other
// combination.
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

// EffectivePlacement is the placement a session actually gets: chosen,
// unless it has a worktree, in which case always PlacementNewSpace -- the
// worktree's own space, which herdr groups under the repository's in its
// sidebar (placement spec §14).
//
// It is the ONE statement of that rule. The form (app's buildPlanInput)
// and headless `create` (buildInput) both route their placement through it
// before building, so a remembered or configured placement can never pull
// a worktree session out of its space on one path and not the other, and
// Build refuses an Input that skipped it.
func EffectivePlacement(useWorktree bool, chosen Placement) Placement {
	if useWorktree {
		return PlacementNewSpace
	}
	return chosen
}

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
	// ExtraArgs is `[agents.extra_args]` for AgentKind, exactly as
	// configured. Build does not pass it on as it is: it goes through
	// agentopts.Launch with AgentOptions below, which is where a chosen
	// option displaces the same flag here.
	ExtraArgs []string
	// AgentOptions is the session options chosen for AgentKind -- the
	// form's `options` row, or create's --model/--effort/--permission-mode,
	// over config.toml's [agents.options.<kind>] (agent-options spec §5).
	// nil when nothing is set. Build refuses an option the kind does not
	// declare, which neither caller can produce.
	AgentOptions agentopts.Values
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

	// Linked is set when a worktree session's ProjectDir is a linked
	// checkout, and is the zero value otherwise -- including for every
	// session without a worktree, which runs in ProjectDir whatever it is.
	Linked LinkedCheckout

	Prompt string
	// MarkReady is the pane-reaper toggle's POSITION (reap spec §7.2): the
	// form's `keep · reap` chips, or `create --reap`/`--no-reap`, over the
	// resolved default. It is not "the instruction was appended" -- Build
	// decides that, once, from this and Prompt (PromptText), so the two
	// callers compare positions and cannot append differently.
	MarkReady                       bool
	Ctx                             herdrc.Context
	DetectionTimeout, PromptTimeout time.Duration
	TrustRepository                 bool
}

// LinkedCheckout is what a worktree session has to know about a project
// directory inside a LINKED worktree -- a checkout `git worktree add` made,
// like another session's own (#171). herdr refuses one as the source of a
// new worktree (linked_worktree_source, herdr:src/app/api/worktrees.rs at
// v0.9.0), so the new worktree is created from the repository root instead,
// and cut from the linked checkout's own commit.
//
// Both callers resolve it, since Build performs no I/O, and both resolve it
// the same way, which equivalence_test.go holds them to.
type LinkedCheckout struct {
	// RepoRoot is the origin repository root (gitx.RepoRoot): `worktree
	// create`'s --cwd in place of ProjectDir.
	RepoRoot string
	// Head is the linked checkout's HEAD commit, a full SHA, resolved only
	// when BaseRef is unset: it is what an unset base means from there.
	// Leaving it to herdr would mean the root's HEAD, which is the primary
	// checkout's commit and not the one the session was started from. A
	// commit rather than a branch name, so a detached HEAD works too.
	Head string
}

// WorktreeBase is the base `worktree create` is given: BaseRef when one was
// chosen, a linked checkout's own commit when it was not, and otherwise ""
// -- herdr's default, the source checkout's HEAD. It is the one statement of
// that rule, read by Build, by the clean check that counts from the base,
// and by both callers' reports of what the branch was cut from.
func WorktreeBase(in Input) string {
	if in.BaseRef != "" {
		return in.BaseRef
	}
	return in.Linked.Head
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
	// OpTabRename names the tab a space op opened. Appended so that no
	// existing kind is renumbered; it is not a topology kind, since it
	// creates nothing.
	OpTabRename
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
	case OpTabRename:
		return "OpTabRename"
	default:
		return fmt.Sprintf("OpKind(%d)", int(k))
	}
}

// Op is one step of a creation plan. Exactly one request field is populated
// per Kind: Worktree for OpWorktreeCreate, Workspace for
// OpWorkspaceCreate, Tab for OpTabCreate, Split for OpPaneSplit, Agent for
// OpAgentStart, RunArgv for OpClauthLaunch, Prompt for OpAgentPrompt,
// Rename for OpTabRename.
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
	Rename    *herdrc.TabRenameReq       // OpTabRename
	Timeout   time.Duration              // OpAwaitDetection, and OpAgentStart's #115 fallback

	// AgentKind is OpAwaitDetection's other field: the kind being waited
	// for, carried for MESSAGES only and never for behaviour. Path B is
	// claude-only today (launchOps gates on it), so exec.go could have
	// written "claude" into the blocked-agent explanation and been right;
	// it would have been right by coincidence, and silently wrong the day
	// another kind gains an account-pinned launch.
	AgentKind string
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
	if in.UseWorktree && in.Linked.RepoRoot != "" && WorktreeBase(in) == "" {
		return nil, fmt.Errorf("plan: build: %q is a linked worktree checkout and its commit was not resolved, so the branch would be cut from the primary checkout's HEAD instead", in.ProjectDir)
	}
	if in.AccountPin != "" && in.AgentKind != claudeAgentKind {
		return nil, fmt.Errorf("plan: build: account pinning is only supported for the %q agent kind, got %q", claudeAgentKind, in.AgentKind)
	}
	if err := agentopts.Validate(in.AgentKind, in.AgentOptions); err != nil {
		return nil, fmt.Errorf("plan: build: agent options: %w", err)
	}
	if in.UseWorktree && in.Placement != PlacementNewSpace {
		return nil, fmt.Errorf("plan: build: a worktree session runs in the worktree's own space, so placement %s does not apply -- route it through EffectivePlacement first", placementName(in.Placement))
	}
	if in.Placement == PlacementTabIn && in.Space.WorkspaceID == "" {
		return nil, fmt.Errorf("plan: build: placement tab-in needs an open workspace to place the tab in, and none was named")
	}

	topology := topologyOp(in)
	ops := append(topology, nameTabOp(in, topology)...)
	ops = append(ops, launchOps(in)...)
	if in.Prompt != "" {
		ops = append(ops, Op{
			Kind:  OpAgentPrompt,
			Label: "sending prompt",
			Prompt: &herdrc.AgentPromptReq{
				Text:        PromptText(in),
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

// topologyOp returns the op that establishes where a session lives: the
// worktree's own new space when UseWorktree is set, the placement op when
// a placement asks for a position beside something, and a new workspace
// otherwise.
//
// Placement spec §5.3 once appended a second op after a worktree create to
// put the agent somewhere else; §14 reversed that, and Build refuses the
// combination before this runs. Execute still treats the first topology op
// as the space and the last as the agent's pane (topologyIndices), which
// with one op is the same op.
//
// From a linked checkout the worktree is created from the repository root
// (LinkedCheckout); every other op here runs in ProjectDir, the checkout
// itself.
func topologyOp(in Input) []Op {
	if in.UseWorktree {
		source := in.ProjectDir
		if in.Linked.RepoRoot != "" {
			source = in.Linked.RepoRoot
		}
		return []Op{{
			Kind:  OpWorktreeCreate,
			Label: "creating worktree",
			Worktree: &herdrc.WorktreeCreateReq{
				Cwd:             source,
				Branch:          in.Branch,
				Base:            WorktreeBase(in),
				Label:           in.Title,
				Focus:           true,
				TrustRepository: in.TrustRepository,
			},
		}}
	}

	if placement, ok := placementOp(in); ok {
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

// nameTabOp returns the op that names the tab a space op opened, after the
// session title -- or nothing, when the topology op already named its tab
// or opened none.
//
// Only the two space ops need it, and the reason is herdr's rather than
// ours: `workspace create` and `worktree create` take --label for the SPACE
// alone (herdr:src/cli/workspace.rs and src/cli/worktree.rs at v0.9.0), so
// the tab either one opens is named by herdr's default, its number. The
// `tab` placements name theirs at creation (`tab create --label`, in
// placementOp), and `split here` opens a pane inside a tab the user owns,
// whose name is not ours to change.
//
// Keyed on the topology op's KIND rather than on Input's placement and
// worktree fields, because what needs a rename is a fact about what each
// herdr call leaves behind, and the kind is that call.
//
// The op carries no tab id: which tab to name is only known once the space
// op has run, and Execute fills it in from where the agent's pane ended up.
func nameTabOp(in Input, topology []Op) []Op {
	if len(topology) == 0 {
		return nil
	}
	switch topology[len(topology)-1].Kind {
	case OpWorkspaceCreate, OpWorktreeCreate:
		return []Op{{
			Kind:   OpTabRename,
			Label:  "naming tab",
			Rename: &herdrc.TabRenameReq{Label: in.Title},
		}}
	default:
		return nil
	}
}

// placementOp returns the op that attaches the session's pane relative to
// the invoking pane, in the project directory -- (Op{}, false) for
// PlacementNewSpace, which asks for a space of its own rather than a
// position beside anything.
//
// Workspace/PaneID name the INVOKING pane's own workspace/pane (Input.Ctx)
// for the two `here` placements, and the NAMED workspace (Input.Space) for
// PlacementTabIn.
func placementOp(in Input) (Op, bool) {
	tab := func(workspace string) (Op, bool) {
		return Op{
			Kind:  OpTabCreate,
			Label: "creating tab",
			Tab: &herdrc.TabCreateReq{
				Workspace: workspace,
				Cwd:       in.ProjectDir,
				Label:     in.Title,
				Focus:     true,
			},
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
				Cwd:       in.ProjectDir,
				Focus:     true,
			},
		}, true
	default:
		return Op{}, false
	}
}

// placementName names p for an error message, in config.toml's own
// vocabulary (defaults.PlacementValue's, which this pure package cannot
// import).
func placementName(p Placement) string {
	switch p {
	case PlacementTabHere:
		return "tab-here"
	case PlacementSplitHere:
		return "split-here"
	case PlacementTabIn:
		return "tab-in"
	default:
		return "new-space"
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
				ExtraArgs: agentArgs(in),
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
	args := agentArgs(in)
	if in.AccountLaunch == LaunchWrapper && in.AccountConfigDir != "" {
		return append([]string{"claude"}, args...),
			[]herdrc.EnvVar{{Name: ClaudeConfigDirVar, Value: in.AccountConfigDir}}, false
	}
	return append(resolveLauncher(in.Launcher, in.AccountPin), args...),
		nil, in.AccountLaunch == LaunchWrapper
}

// agentArgs is what the agent is started with after its own command:
// extra_args with the chosen options applied (agent-options spec §5.1).
// Both launch paths take it from here and nowhere else, so an option can
// never reach one path and not the other.
func agentArgs(in Input) []string {
	return agentopts.Launch(in.AgentKind, in.ExtraArgs, in.AgentOptions)
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
