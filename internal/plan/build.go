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
)

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
	AgentKind string
	ExtraArgs []string
	// AccountPin is a clauth account pin, or "" for the active/unpinned
	// account. Pinning is only valid when AgentKind == "claude" (spec
	// §6.7); Build rejects any other combination.
	AccountPin                      string
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
// OpAwaitDetection populates only Timeout. Requests that depend on step-1
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
	Prompt    *herdrc.AgentPromptReq     // OpAgentPrompt
	Timeout   time.Duration              // OpAwaitDetection

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

	ops := append(topologyOp(in), launchOps(in)...)
	if in.Prompt != "" {
		ops = append(ops, Op{
			Kind:  OpAgentPrompt,
			Label: "sending prompt",
			Prompt: &herdrc.AgentPromptReq{
				Text:        in.Prompt,
				WaitTimeout: in.PromptTimeout,
			},
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
// Workspace/PaneID always name the INVOKING pane's own workspace/pane
// (Input.Ctx), never a worktree's -- placing the agent somewhere other than
// the worktree's own space is exactly what this op is for.
func placementOp(in Input, cwd string, cwdFromCheckout bool) (Op, bool) {
	switch in.Placement {
	case PlacementTabHere:
		return Op{
			Kind:  OpTabCreate,
			Label: "creating tab",
			Tab: &herdrc.TabCreateReq{
				Workspace: in.Ctx.WorkspaceID,
				Cwd:       cwd,
				Label:     in.Title,
				Focus:     true,
			},
			CwdFromCheckout: cwdFromCheckout,
		}, true
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

// launchOps returns the op(s) that start the agent. A pinned claude
// account launches through clauth (a plain shell command herdr types into
// the pane) and, unlike `herdr agent start`, does not itself wait for
// detection -- so it is followed by an explicit OpAwaitDetection.
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
		runArgv := append([]string{"clauth", "start", in.AccountPin, "--"}, in.ExtraArgs...)
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
				Label:   "typing the clauth launch",
				RunArgv: runArgv,
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
		},
	}
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
