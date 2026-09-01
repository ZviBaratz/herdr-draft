// Package plan builds an ordered list of herdr operations from a completed
// creation form (spec §9). Build is pure logic: it performs no I/O, runs no
// subprocess, and never calls herdr itself -- internal/plan/exec.go (Task
// 13) is the executor that actually runs the returned ops and threads
// step-1 output (workspace/tab/pane ids) into the ops that need it.
package plan

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ZviBaratz/herdr-draft/internal/gitx"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
)

// Placement selects where a non-worktree creation attaches relative to the
// invoking pane (spec §9). It is ignored when Input.UseWorktree is set --
// worktree creation always opens a new workspace regardless of Placement.
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

	ops := []Op{topologyOp(in)}
	ops = append(ops, launchOps(in)...)
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

// topologyOp returns the first op: the one that creates the
// workspace/tab/pane the agent will run in. Worktree creation always wins
// over Placement (spec §9): a worktree is always a new workspace,
// regardless of where the form said to place it.
func topologyOp(in Input) Op {
	switch {
	case in.UseWorktree:
		return Op{
			Kind:  OpWorktreeCreate,
			Label: "creating worktree",
			Worktree: &herdrc.WorktreeCreateReq{
				Cwd:             in.ProjectDir,
				Branch:          in.Branch,
				Base:            in.BaseRef,
				Label:           in.Title,
				Focus:           true,
				TrustRepository: in.TrustRepository,
			},
		}
	case in.Placement == PlacementTabHere:
		return Op{
			Kind:  OpTabCreate,
			Label: "creating tab",
			Tab: &herdrc.TabCreateReq{
				Workspace: in.Ctx.WorkspaceID,
				Cwd:       in.ProjectDir,
				Label:     in.Title,
				Focus:     true,
			},
		}
	case in.Placement == PlacementSplitHere:
		return Op{
			Kind:  OpPaneSplit,
			Label: "splitting pane",
			Split: &herdrc.PaneSplitReq{
				PaneID:    in.Ctx.FocusedPaneID,
				Direction: defaultSplitDirection,
				Cwd:       in.ProjectDir,
				Focus:     true,
			},
		}
	default: // PlacementNewSpace
		return Op{
			Kind:  OpWorkspaceCreate,
			Label: "creating workspace",
			Workspace: &herdrc.WorkspaceCreateReq{
				Cwd:   in.ProjectDir,
				Label: in.Title,
				Focus: true,
			},
		}
	}
}

// launchOps returns the op(s) that start the agent. A pinned claude
// account launches through clauth (a plain shell command herdr types into
// the pane) and, unlike `herdr agent start`, does not itself wait for
// detection -- so it is followed by an explicit OpAwaitDetection. Every
// other case starts the agent directly via `herdr agent start`, which
// performs its own detection wait server-side.
func launchOps(in Input) []Op {
	if in.AccountPin != "" && in.AgentKind == claudeAgentKind {
		runArgv := append([]string{"clauth", "start", in.AccountPin, "--"}, in.ExtraArgs...)
		return []Op{
			{
				Kind:    OpClauthLaunch,
				Label:   "launching claude via clauth",
				RunArgv: runArgv,
			},
			{
				Kind:    OpAwaitDetection,
				Label:   "waiting for agent detection",
				Timeout: in.DetectionTimeout,
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
