// Package create implements `herdr-draft create` (v2 spec §13): the
// non-interactive path a script -- or an agent already running inside a
// herdr session -- uses to create the next session without the popup.
//
// The contract that shapes every decision here is spec §13's own sentence:
// "unset flags resolve through §10's resolver, so the command and the form
// produce the same session from the same inputs". This package therefore
// owns no precedence of its own. It loads the same tiers internal/app
// loads, hands them to the same internal/defaults.Resolve, and assembles
// the same plan.Input internal/app's own buildPlanInput assembles --
// equivalence_test.go pins that sameness by driving both and comparing the
// two plan.Inputs, because a promise like this one decays the moment only
// one side is exercised.
//
// Everything below the plan is unchanged (spec §15): plan.Build stays pure
// and receives every fact already resolved by this caller, plan.Execute
// runs the ops, and internal/plan/dialog.go's screen guard still decides
// whether a queued prompt may be sent. A prompt that guard withholds is
// reported rather than assumed delivered -- and under --json it is
// reproduced in the output, which is the only place a headless caller
// could recover it from (there is no TTY to paste it into).
//
// plan.Execute runs synchronously on the calling goroutine and reports
// progress through an unbuffered callback, so it is never abandoned
// mid-pipeline -- the same hazard spec §12 names for the form's Esc
// handling, with no TUI around it.
package create

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/defaults"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
	"github.com/ZviBaratz/herdr-draft/internal/linear"
	"github.com/ZviBaratz/herdr-draft/internal/plan"
)

// Exit codes, spec §13's table verbatim.
//
// ExitUsage covers every pre-flight refusal, not only a malformed command
// line: a config.toml that will not parse, a project directory that does
// not exist, a placement whose herdr context is missing, and a plan.Build
// rejection all land here. What they have in common is the half of the
// table that matters -- nothing has been created, so there is nothing to
// keep or clean, and re-running with a corrected invocation is the whole
// remedy.
//
// ExitFailed is the other half: the plan started. It is returned whether
// or not the topology op itself succeeded, since both leave the caller
// with "the session was not created" -- --on-failure only has something to
// act on in the first case, and the JSON report says which happened.
const (
	ExitOK          = 0
	ExitFailed      = 1
	ExitUsage       = 2
	ExitUnreachable = 3
)

// Env is the process environment `create` reads, passed in as a struct
// rather than read from os.Getenv here so Run stays testable with an
// explicit, deterministic input -- the same reason app.Env exists.
//
// ContextJSON is normally EMPTY for this command: run from a plain shell
// inside a herdr pane there is no plugin invocation, so the three
// HERDR_*_ID variables herdr exports into every pane are the only context
// there is (spec §13). It is still read, and still wins where it carries a
// value, so that `herdr-draft create` invoked as a plugin -- with a real
// $HERDR_PLUGIN_CONTEXT_JSON -- uses the richer context rather than
// ignoring it.
type Env struct {
	// ConfigDir is $HERDR_PLUGIN_CONFIG_DIR.
	ConfigDir string
	// StateDir is $HERDR_PLUGIN_STATE_DIR.
	StateDir string
	// ContextJSON is $HERDR_PLUGIN_CONTEXT_JSON, usually "".
	ContextJSON string
	// WorkspaceID/TabID/PaneID are $HERDR_WORKSPACE_ID/$HERDR_TAB_ID/
	// $HERDR_PANE_ID.
	WorkspaceID string
	TabID       string
	PaneID      string
}

// GitSource is the git/filesystem access this command needs: whether the
// project directory exists, whether it is a repository, and which
// repository root it belongs to (the key spec §10's per-project memory
// hangs on). It is a deliberate SUBSET of internal/app's own gitSource, so
// app.NewGitSource satisfies it directly and production has one
// implementation rather than two.
type GitSource interface {
	DirExists(path string) bool
	IsGitRepo(dir string) bool
	RepoRoot(ctx context.Context, dir string) (string, error)
}

// IssueSource is the Linear access --issue needs -- the same one-method
// interface internal/app depends on, so a test fakes it the same way and
// no test in this package ever reaches the real API.
type IssueSource interface {
	AssignedIssues(ctx context.Context) ([]linear.Issue, error)
}

// Deps groups every I/O-capable collaborator behind something a test can
// substitute. The nil-means-production fields (Git, RepoConfig, Linear,
// Stdin/Stdout/Stderr, Workdir, Now) follow app.Deps.RepoConfig's own
// precedent: one call each, no state, and a test that needs a
// deterministic answer should not have to put a real repository on disk to
// get one.
type Deps struct {
	// Runner is the herdr CLI. Required.
	Runner herdrc.Runner
	// Git is nil for the production source (internal/gitx via app).
	Git GitSource
	// Linear is nil for "build one from the user's configured API key",
	// which resolves to "Linear is not configured" when there is no key.
	Linear IssueSource
	// RepoConfig is nil for config.LoadRepoConfig.
	RepoConfig func(repoRoot string) config.RepoConfig

	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	// Workdir is nil for os.Getwd -- the default project directory when
	// --project is not given.
	Workdir func() (string, error)
	// Now is nil for time.Now -- only used to stamp the per-project memory
	// a successful create records.
	Now func() time.Time
}

func (d Deps) stdout() io.Writer {
	if d.Stdout != nil {
		return d.Stdout
	}
	return os.Stdout
}

func (d Deps) stderr() io.Writer {
	if d.Stderr != nil {
		return d.Stderr
	}
	return os.Stderr
}

func (d Deps) stdin() io.Reader {
	if d.Stdin != nil {
		return d.Stdin
	}
	return os.Stdin
}

func (d Deps) workdir() (string, error) {
	if d.Workdir != nil {
		return d.Workdir()
	}
	return os.Getwd()
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d Deps) repoConfig() func(string) config.RepoConfig {
	if d.RepoConfig != nil {
		return d.RepoConfig
	}
	return config.LoadRepoConfig
}

// Run executes `herdr-draft create` with args (the arguments AFTER the
// verb) and returns the process exit code. It never prompts -- stdin is
// read only for `--prompt -`, and only because the caller asked -- and
// never panics: progress goes to Stderr one line per step, the result to
// Stdout.
func Run(ctx context.Context, args []string, env Env, deps Deps) int {
	req, err := parseArgs(args)
	switch {
	case err == errHelpRequested:
		fmt.Fprint(deps.stdout(), createUsage)
		return ExitOK
	case err != nil:
		// A malformed command line is the one refusal worth reprinting the
		// whole usage for: the caller mistyped the interface itself.
		fmt.Fprintf(deps.stderr(), "herdr-draft create: %v\n\n", err)
		fmt.Fprint(deps.stderr(), createUsage)
		return ExitUsage
	}
	return run(ctx, req, env, deps)
}

// run is Run past flag parsing, split out so every step below reads as one
// pipeline: resolve the request against the layered defaults, refuse
// anything that cannot work BEFORE touching herdr, then build and execute
// the plan.
func run(ctx context.Context, req request, env Env, deps Deps) int {
	resolved, err := resolveRequest(ctx, req, env, deps)
	if err != nil {
		return usageError(deps.stderr(), err)
	}

	ops, err := plan.Build(resolved.input)
	if err != nil {
		return usageError(deps.stderr(), err)
	}

	// The reachability probe (spec §13's exit 3) is deliberately the LAST
	// pre-flight step: a typo in a flag should not need a running herdr to
	// be reported, and `workspace list` is the same call app.Bootstrap
	// uses for the same purpose.
	if _, err := deps.Runner.WorkspaceList(ctx); err != nil {
		fmt.Fprintf(deps.stderr(), "herdr-draft create: herdr unreachable: %v\n", err)
		return ExitUnreachable
	}

	return execute(ctx, resolved, req, env, deps, ops)
}

// execute runs the plan and reports it. It is the only part of this
// package that can leave anything behind, which is why the keep-or-clean
// gate and the state write both live here rather than in a caller.
func execute(ctx context.Context, resolved resolution, req request, env Env, deps Deps, ops []plan.Op) int {
	rep := report{
		input:      resolved.input,
		provenance: resolved.provenance,
		onFailure:  req.onFailure,
		json:       req.json,
	}

	// plan.Execute reports through this callback synchronously, so the
	// progress lines interleave with nothing and the failing step's error
	// -- which ExecResult itself does not carry -- is captured here.
	total := len(ops)
	onProgress := func(p plan.Progress) {
		switch p.State {
		case plan.StepDone:
			fmt.Fprintf(deps.stderr(), "[%d/%d] %s ... ok\n", p.Index+1, total, p.Label)
		case plan.StepFailed:
			fmt.Fprintf(deps.stderr(), "[%d/%d] %s ... failed: %v\n", p.Index+1, total, p.Label, p.Err)
			rep.failedLabel = p.Label
			rep.err = p.Err
		}
	}

	rep.result = plan.Execute(ctx, deps.Runner, ops, onProgress)

	if rep.result.FailedIndex == -1 {
		// Spec §10/§12's memory is written only by a successful create, the
		// same rule app.persistStateCmd follows -- a failed one says nothing
		// about what the user wants next.
		remember(env, resolved, deps.now())
		rep.write(deps.stdout(), deps.stderr())
		return ExitOK
	}

	if rep.result.Created != nil {
		applyOnFailure(ctx, deps, &rep)
	}
	rep.write(deps.stdout(), deps.stderr())
	return ExitFailed
}

// applyOnFailure runs spec §13's `--on-failure` decision over the topology
// the failed run did create. `keep` does nothing, deliberately and
// visibly: it is the default because a half-built session a human can look
// at is worth more than a tidy machine.
//
// `clean` goes through plan.CleanCheck first, exactly as the form's own
// keep-or-clean gate does, so a worktree carrying uncommitted work or
// commits of its own is refused with the reason rather than removed. There
// is no way to override that refusal from the command line, which is the
// point: a non-interactive caller is the one least able to notice what it
// would be destroying.
func applyOnFailure(ctx context.Context, deps Deps, rep *report) {
	if rep.onFailure != onFailureClean {
		return
	}
	created := *rep.result.Created
	if decision := plan.CleanCheck(ctx, rep.input, created); !decision.Allowed {
		rep.cleanRefused = decision.Reason
		return
	}
	if err := plan.Clean(ctx, deps.Runner, rep.input, created); err != nil {
		rep.cleanRefused = err.Error()
		return
	}
	rep.cleaned = true
}

// remember writes the choices this create was made with back to the plugin
// state directory: recents.json, last-used.json and (spec §10) this
// project's projects.json entry -- the same three app.persistStateCmd
// writes, with the same values, because the tiers they feed are shared.
//
// A headless create that read the memory but never wrote it would make the
// two paths diverge in the other direction: the form's per-project default
// would keep pointing at whatever was last done IN THE FORM, silently
// disagreeing with what actually ran. Errors are dropped for spec §12's
// reason -- state is loss-tolerant, and a session that just launched is
// not worth failing over a recents file.
func remember(env Env, resolved resolution, now time.Time) {
	if env.StateDir == "" {
		return
	}
	in := resolved.input

	st := resolved.tiers.state
	if in.ProjectDir != "" {
		st.TouchRecent(in.ProjectDir)
	}
	st.LastKind = in.AgentKind
	st.LastPlacement = defaults.PlacementValue(in.Placement)
	useWorktree := in.UseWorktree
	st.LastWorktree = &useWorktree
	_ = config.SaveState(env.StateDir, st)

	if resolved.tiers.projectKey == "" {
		return
	}
	projects := resolved.tiers.projects.Touched(resolved.tiers.projectKey, config.ProjectDefaults{
		Kind:      in.AgentKind,
		Worktree:  &useWorktree,
		Placement: defaults.PlacementValue(in.Placement),
		Base:      in.BaseRef,
	}, now)
	_ = config.SaveProjects(env.StateDir, projects)
}

// usageError reports a pre-flight refusal and returns ExitUsage, so every
// such site is one `return usageError(...)` and they all read identically.
func usageError(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "herdr-draft create: %v\n", err)
	return ExitUsage
}
