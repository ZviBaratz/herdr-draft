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
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/ZviBaratz/herdr-draft/internal/app"
	"github.com/ZviBaratz/herdr-draft/internal/clauth"
	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/defaults"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
	"github.com/ZviBaratz/herdr-draft/internal/linear"
	"github.com/ZviBaratz/herdr-draft/internal/picker"
	"github.com/ZviBaratz/herdr-draft/internal/plan"
)

// Exit codes, spec §13's table as #192 amended it.
//
// ExitUsage covers every pre-flight refusal, not only a malformed command
// line: a config.toml that will not parse, a project directory that does
// not exist, a placement whose herdr context is missing, and a plan.Build
// rejection all land here. What they have in common is the half of the
// table that matters -- nothing has been created, so there is nothing to
// keep or clean, and re-running with a corrected invocation is the whole
// remedy.
//
// ExitUnreachable is the other refusal that comes before the plan: the
// reachability probe, run after every check that needs no herdr.
//
// ExitCheckTimedOut is the third, and the one that is NOT the caller's to
// fix (#272): a question the pre-flight asked the filesystem or git that
// did not answer inside its budget -- a stalled mount, in practice.
// Deliberately not ExitUsage, whose remedy is "fix the command and
// re-run", and whose existing occupant is "project directory does not
// exist": a directory nobody could CHECK is the other thing, and #202
// named the distinction "unknown is not invalid". Deliberately not
// ExitUnreachable either, which says herdr is down and would send a caller
// to look at a herdr that is fine. Nothing was created, and nothing on
// stdout, as for the two above.
//
// ExitFailed and ExitNothingCreated are the plan's own: it started, and it
// failed. They split on what the failure can be shown to have left behind
// (#192). ExitNothingCreated is a first step that failed before anything
// existed -- plan.ExecResult.NothingCreated, which needs evidence and not
// just an absent space -- so there is nothing to look at or clean up, and
// --on-failure has nothing to act on. ExitFailed is everything else: a
// session that may exist in part, which --on-failure acts on when a space
// was reported, and a first step that failed with no evidence either way.
// That second kind is why an absent space is not enough: herdr's worktree
// create can fail after git has made the checkout and its branch, and a
// caller told "nothing exists" would retry into them.
const (
	ExitOK             = 0
	ExitFailed         = 1
	ExitUsage          = 2
	ExitUnreachable    = 3
	ExitNothingCreated = 4
	ExitCheckTimedOut  = 5
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
	// PluginID is $HERDR_PLUGIN_ID: which plugin the three variables above
	// were exported for, and the only one of the four that says so. herdr
	// sets it alongside them for every plugin it launches itself, and a
	// shell started inside such a plugin's pane can inherit the set --
	// without reliably inheriting the id. So the three are used only when
	// this MATCHES: an absent id is not a matching one (#91). See
	// usablePluginEnv, which is why this is read at all.
	PluginID string
	// WorkspaceID/TabID/PaneID are $HERDR_WORKSPACE_ID/$HERDR_TAB_ID/
	// $HERDR_PANE_ID.
	WorkspaceID string
	TabID       string
	PaneID      string
}

// pluginID is this plugin's own id, the value $HERDR_PLUGIN_ID carries
// when the surrounding plugin environment is ours.
//
// An alias rather than a second declaration: internal/app needs the same
// string to print the command that opens the popup, and two copies of the
// one value that names the install directory, the config dir, the state
// dir and the `--plugin` argument would be two things to keep in step.
// See herdrc.PluginID, which is held to herdr-plugin.toml by a test.
const pluginID = herdrc.PluginID

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
	// BranchExists is the form's duplicate-branch check (#147): whether a
	// branch of that name already exists locally or on a remote.
	BranchExists(ctx context.Context, dir, name string) (bool, error)
	// PrimaryCheckout and ResolveCommit are what a worktree session from
	// inside a linked checkout needs (#171): the primary checkout to create
	// it from ("" when the project is not a linked checkout), and the commit
	// its base names in the linked checkout.
	PrimaryCheckout(ctx context.Context, dir string) (string, error)
	ResolveCommit(ctx context.Context, dir, ref string) (string, error)
}

// ClauthSource is clauth's status feed -- the same one-method interface
// internal/app reads the account row from, so app.NewClauthSource satisfies
// it and production has one loader rather than two.
type ClauthSource interface {
	Status(ctx context.Context) (clauth.Status, error)
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
	// Picker is the account picker `--account auto` resolves through, or nil
	// for "no picker configured" -- which makes `auto` a usage error rather
	// than a silent fallback. Production is picker.CLI over `[clauth] picker`;
	// every test in this package hands in a fake, so no test here ever runs a
	// subprocess.
	Picker picker.Source
	// Clauth is clauth's status feed, the one the form's account row shows:
	// where a pinned profile's auth status is read from, and a dry run's
	// account_usage (#215). nil means there is no clauth to ask, so the
	// check is skipped and the key absent: unlike Git, the production loader
	// needs the status-file path and binary name main.go owns, so it cannot
	// be built here, and main.go passes it.
	Clauth ClauthSource

	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	// Workdir is nil for os.Getwd -- the default project directory when
	// --project is not given.
	Workdir func() (string, error)
	// Now is nil for time.Now -- only used to stamp the per-project memory
	// a successful create records.
	Now func() time.Time

	// CheckDeadline overrides preflightCheckDeadline, the bound on every
	// question the pre-flight asks the filesystem or git (#272). Zero --
	// which is what production leaves it, since there is nothing to tune --
	// means the constant. See Deps.checks, the one place either is read.
	//
	// It follows app.Model.checkDeadline's own pattern rather than becoming
	// a [timeouts] key or a flag: a test needs a seam so it does not sit
	// through thirty seconds, and nothing else needs one.
	CheckDeadline time.Duration
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
		return refuse(deps.stderr(), err)
	}

	// Built once as a CHECK, with an `auto` account still unpicked: every
	// refusal plan.Build can make is a refusal the pick below must not be
	// spent on (#145).
	if _, err := plan.Build(resolved.input); err != nil {
		return refuse(deps.stderr(), err)
	}
	// `auto` with no picker to ask is a fault in the command or the config,
	// so it is reported here, ahead of the probe, as it was when the pick ran
	// inside resolveRequest: with herdr also down, exit 3 would tell the
	// caller to stop when the remedy is to fix the invocation.
	if err := requirePicker(resolved.input, accountPicker(resolved.tiers.cfg, deps)); err != nil {
		return refuse(deps.stderr(), err)
	}

	// The reachability probe (spec §13's exit 3) comes after every check
	// that needs no herdr -- a typo in a flag should not need a running
	// herdr to be reported -- and `workspace list` is the same call
	// app.Bootstrap uses for the same purpose.
	if _, err := deps.Runner.WorkspaceList(ctx); err != nil {
		fmt.Fprintf(deps.stderr(), "herdr-draft create: herdr unreachable: %v\n", err)
		return ExitUnreachable
	}

	cl := &clauthOnce{src: deps.Clauth}
	if err := refuseWhatTheFormRefuses(ctx, resolved, deps, cl); err != nil {
		return refuse(deps.stderr(), err)
	}

	// The pick is the LAST pre-flight step, as it is in the form's
	// handleSubmit: it writes the picker's ledger, so it happens only for a
	// request nothing is left to refuse. The picker's own refusal is still
	// before anything is created -- the whole of the contract the spec
	// states for its exit 2/3/4.
	in, warnings, err := resolveAccount(ctx, resolved.input, accountPicker(resolved.tiers.cfg, deps), req.dryRun)
	if err != nil {
		return refuse(deps.stderr(), err)
	}
	for _, w := range warnings {
		// One prefixed line each, however the picker wrapped it.
		fmt.Fprintf(deps.stderr(), "herdr-draft create: account picker: %s\n", strings.Join(strings.Fields(w), " "))
	}
	resolved.input = in

	ops, err := plan.Build(resolved.input)
	if err != nil {
		return refuse(deps.stderr(), err)
	}
	if req.dryRun {
		// --dry-run stops here (#209), with every refusal above already
		// made: what it reports is what the plan would be built from, and
		// a request it passes is one the real run would not refuse before
		// starting. Nothing past this point runs -- no plan, no memory
		// write, and the pick above was the picker's own dry run.
		rep := report{input: resolved.input, provenance: resolved.provenance, json: req.json, dryRun: true}
		if req.json {
			// Only --json has somewhere to put it, so only --json reads it.
			rep.usage = dryRunUsage(ctx, resolved, deps, cl)
		}
		rep.write(deps.stdout(), deps.stderr())
		return ExitOK
	}
	// #252's seam, and the reason it is HERE rather than anywhere else.
	// main turns SIGINT/SIGTERM into a cancel of ctx, so that a signal
	// arriving during the pre-flight kills the account picker with it --
	// the commit pick is the one call that writes the picker's ledger, and
	// a pick nobody is waiting for any more must not reach it.
	//
	// plan.Execute deliberately does not inherit that. Everything above has
	// created nothing, so abandoning it is free; from here on a cancellation
	// would tear the pipeline out mid-creation and leave a half-built
	// session the keep-or-clean gate never sees -- worse than the orphaned
	// ledger entry the cancel exists to prevent. It is the boundary this
	// package already draws twice over: --dry-run stops exactly here, and
	// the popup's app.Lifetime covers the picks and not runSubmitCmd.
	//
	// A signal arriving from here on therefore does what it always did,
	// near enough: main's handler re-raises it after its grace, and the
	// process dies mid-plan rather than unwinding through a cancellation
	// nothing below is prepared for. It is the one window main's own
	// handshake cannot cover, because main is inside this call for as long
	// as the plan takes.
	return execute(context.WithoutCancel(ctx), resolved, req, deps, ops)
}

// refuseWhatTheFormRefuses is the form's submit-time validation that
// plan.Build does not already make (#147): checkSubmitValidation's
// duplicate-title and dead-credential blocks. Without it a request the
// popup would refuse went straight to herdr from here, and
// equivalence_test.go could not see the difference, because it compares
// plan.Inputs and a refusal is not one.
//
// Each check reads its condition the way the form's does, so the two
// refuse the same requests: the branch only when a worktree would create
// it, the label against the workspace snapshot the resolver already read,
// and the auth status as non-blocking whenever it is unknown.
func refuseWhatTheFormRefuses(ctx context.Context, resolved resolution, deps Deps, cl *clauthOnce) error {
	in := resolved.input

	// Not a formality: herdr v0.9.0 does not refuse a branch that exists
	// locally. It checks it out and ignores the base
	// (https://github.com/herdrdev/herdr/blob/v0.9.0/src/worktree.rs#L315),
	// failing only if that branch's checkout directory is already on disk,
	// so this is what stops a session starting on old work. A branch that
	// exists only on a remote is refused too, as the form refuses it: herdr
	// would make an unrelated local branch of the same name from the base.
	if in.UseWorktree && in.Branch != "" {
		exists, err := deps.checks().BranchExists(ctx, in.ProjectDir, in.Branch)
		// A real error is still discarded, deliberately and as before: a
		// git that answers "I cannot tell" leaves the branch to herdr,
		// which refuses a checkout that is genuinely taken. A question
		// NOBODY answered is the other thing (#272) -- reading it as "the
		// branch is free" is how a create walks into old work.
		if errors.Is(err, errCheckTimedOut) {
			return err
		}
		if err == nil && exists {
			return fmt.Errorf("branch %q already exists, locally or on a remote; pass --branch with a new name", in.Branch)
		}
	}

	if w, taken := app.WorkspaceLabelled(resolved.tiers.workspaces, in.Title); taken {
		return fmt.Errorf("workspace %s is already labelled %q; pass a different --title", w.WorkspaceID, in.Title)
	}

	return refuseUnusableProfile(ctx, resolved, deps, cl)
}

// refuseUnusableProfile is the form's accountAuthBlocked: a pinned profile
// whose credential clauth reports dead. Only a named profile is checked --
// `auto` is still the sentinel here, exactly as it is when the form runs the
// same check.
//
// Dead means clauth's `broken`, and only that (#243). It is the value that
// means "last refresh rejected as revoked/invalid", and `clauth login` --
// which this message names -- is what clears it. `expired` is a different
// thing wearing a similar word: an access token past its expiry whose
// refresh has not run, which the `clauth start` this create goes on to type
// heals by handing the stored refresh token to `claude`. Refusing it made
// the one remedy a person could act on the wrong one.
//
// Two statuses are checked on and pass, both of them said on stderr. Unlike
// the form, where the account row shows the reason itself, there is no row
// here and a check skipped in silence reads as a check passed:
//
//   - one that could not be read at all, and
//
//   - one clauth.ParseStatus marked Degraded (#245), where every field past
//     the profile's name is unreliable. The DECISION is AuthOf's and is not
//     taken again here; what this branch decides is only whether to say
//     something, which AuthOf cannot answer -- it is deliberately silent
//     about which of its reasons applied, and an unlisted profile must not
//     print this line.
//
//     It does mean the Degraded field is read in two places. What holds
//     them together is equivalence_test.go's auth table: deleting AuthOf's
//     guard leaves this one standing and breaks the popup, so the pair
//     fails as a pair rather than drifting.
//
// The degraded line does NOT set cl.said, which means something narrower:
// that the read's ERROR is already on stderr. A degraded read succeeded.
func refuseUnusableProfile(ctx context.Context, resolved resolution, deps Deps, cl *clauthOnce) error {
	pin := resolved.input.AccountPin
	if pin == "" || pin == clauthAuto || deps.Clauth == nil {
		return nil
	}
	if !clauthEnabled(resolved) {
		return nil
	}
	status, err := cl.get(ctx)
	if err != nil {
		fmt.Fprintf(deps.stderr(), "herdr-draft create: could not check whether %s can be used: %v\n", pin, err)
		cl.said = true
		return nil
	}
	if status.Degraded {
		fmt.Fprintf(deps.stderr(), "herdr-draft create: clauth status is degraded (%s); not checking whether %s can be used\n", degradedBecause(status), pin)
		return nil
	}
	if status.AuthOf(pin) == clauth.AuthDead {
		return fmt.Errorf("clauth reports profile %s as %q, which cannot be used: `clauth login %s`, or pass a different --account", pin, clauth.AuthBroken, pin)
	}
	return nil
}

// degradedBecause names why clauth.ParseStatus degraded a status, for the
// one line refuseUnusableProfile prints when it skips its check.
//
// Two causes, and the difference is worth the words: a full parse of a
// schema nobody has read clauth's reason for still carries every field,
// while a structurally incompatible payload recovered only the profile
// names. The second decodes no schema at all, so it reports Schema 0 --
// naming a number no clauth writes, next to a sentence about a schema
// someone could go and read, would describe the wrong failure.
func degradedBecause(status clauth.Status) string {
	if status.Schema == 0 {
		return "its shape is one this build cannot read"
	}
	return fmt.Sprintf("schema %d is one this build has not checked", status.Schema)
}

// clauthEnabled is `[clauth] enabled`, which defaults to on.
func clauthEnabled(resolved resolution) bool {
	enabled := resolved.tiers.cfg.Clauth.Enabled
	return enabled == nil || *enabled
}

// clauthOnce is clauth's status, read at most once per create and only when
// something asks for it: the signed-out check for a pinned profile, and a
// dry run's account_usage (#215). The read can be a subprocess -- a stale
// status file falls back to `clauth status --json` -- so a real create that
// pins nothing never makes it, and the two askers share the one read rather
// than risk two answers.
type clauthOnce struct {
	src    ClauthSource
	read   bool
	status clauth.Status
	err    error
	// said records that the read's error is already on stderr, so the second
	// asker does not print it again.
	said bool
}

func (c *clauthOnce) get(ctx context.Context) (clauth.Status, error) {
	if !c.read {
		c.status, c.err = c.src.Status(ctx)
		c.read = true
	}
	return c.status, c.err
}

// dryRunUsage is a dry run's account_usage (#215): the windows of the
// account the session would bill, from the status the form's account row
// reads. It is called after the pick, so an `auto` pin is already the dry
// pick's profile. nil -- the key absent -- whenever it cannot be said: no
// clauth to ask, clauth switched off, an agent other than claude (the
// account applies to claude only), or a status that could not be read.
//
// A status that could not be read is said on stderr, as the dead-credential
// check says it, unless the check already did, or clauth is simply not
// installed:
// most people never installed it, and the popup does not mention it either
// (#88). A report whose key is absent because clauth broke, with nothing to
// say so, would read as an account nobody could see into for no reason.
func dryRunUsage(ctx context.Context, resolved resolution, deps Deps, cl *clauthOnce) *accountUsage {
	in := resolved.input
	if deps.Clauth == nil || in.AgentKind != claudeKind || !clauthEnabled(resolved) {
		return nil
	}
	status, err := cl.get(ctx)
	if err != nil {
		if !cl.said && !errors.Is(err, exec.ErrNotFound) {
			fmt.Fprintf(deps.stderr(), "herdr-draft create: could not read clauth's usage windows: %v\n", err)
			cl.said = true
		}
		return nil
	}
	return usageFor(status, in.AccountPin)
}

// execute runs the plan and reports it. It is the only part of this
// package that can leave anything behind, which is why the keep-or-clean
// gate and the state write both live here rather than in a caller.
func execute(ctx context.Context, resolved resolution, req request, deps Deps, ops []plan.Op) int {
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
		case plan.StepFailedNonFatal:
			// Said, and deliberately NOT recorded as rep's failure: the
			// run goes on, and whether it fails is for a later step to
			// decide. Only stderr hears about it -- a tab still called
			// by its number, or a branch still tracking its base (#221),
			// changes nothing a caller of stdout or --json addresses
			// next, and the line names the command that fixes the second.
			fmt.Fprintf(deps.stderr(), "[%d/%d] %s ... failed, continuing: %v\n", p.Index+1, total, p.Label, p.Err)
		}
	}

	// No trust budget, deliberately (#115's decision 2). A script has
	// nobody at the keyboard, so waiting five minutes for a dialog nobody
	// will answer is worse than failing with the reason -- and there is no
	// flag for it either, since every flag here has a form row behind it
	// and no row offers this.
	rep.result = plan.Execute(ctx, deps.Runner, ops, plan.ExecOpts{}, onProgress)

	if rep.result.FailedIndex == -1 {
		// Spec §10/§12's memory is written only by a successful create, the
		// same rule app.persistStateCmd follows -- a failed one says nothing
		// about what the user wants next.
		remember(resolved, deps.now())
		rep.write(deps.stdout(), deps.stderr())
		return ExitOK
	}

	switch {
	case rep.result.Created != nil:
		applyOnFailure(ctx, deps, &rep)
	case rep.onFailure == onFailureClean && !rep.result.NothingCreated:
		// No space to act on, and no evidence nothing was made (#192).
		// Said, because under --json an absent `cleaned` beside an absent
		// `clean_refused` read as nothing left to clean.
		rep.cleanRefused = "no space was reported, so there was nothing to remove"
	}
	rep.write(deps.stdout(), deps.stderr())
	if rep.result.NothingCreated {
		return ExitNothingCreated
	}
	return ExitFailed
}

// applyOnFailure runs spec §13's `--on-failure` decision over the topology
// the failed run did create. `keep` does nothing, deliberately and
// visibly: it is the default because a half-built session a human can look
// at is worth more than a tidy machine.
//
// `clean` goes through plan.CleanCheck first, exactly as the form's own
// keep-or-clean gate does, so a worktree carrying uncommitted work or
// commits of its own is refused with the reason rather than removed --
// and so is a run that reused an already-open workspace instead of
// creating its own: CleanCheck refuses that case by name too, rather
// than closing a workspace the user, not this command, owns. There is no
// way to override that refusal from the command line, which is the
// point: a non-interactive caller is the one least able to notice what it
// would be destroying.
//
// A worktree's clean also deletes the branch the run made (#173). What
// became of it is recorded for the report, because a caller about to retry
// with the same title needs to know whether the name is free again. A clean
// that removed the checkout and then kept the branch still happened: it is
// `cleaned`, with the branch beside it, never clean_refused -- which says
// the session is still there. A refusal Clean itself makes before removing
// anything is clean_refused, with CleanCheck's kind of reason.
func applyOnFailure(ctx context.Context, deps Deps, rep *report) {
	if rep.onFailure != onFailureClean {
		return
	}
	decision := plan.CleanCheck(ctx, rep.input, rep.result)
	if !decision.Allowed {
		rep.cleanRefused = decision.Reason
		return
	}
	outcome, err := plan.Clean(ctx, deps.Runner, rep.input, rep.result)
	var refusal *plan.CleanRefusal
	switch {
	case errors.As(err, &refusal):
		rep.cleanRefused = refusal.Reason
		return
	case err != nil:
		rep.cleanRefused = err.Error()
		return
	}
	rep.cleaned = true
	rep.outcome = outcome
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
func remember(resolved resolution, now time.Time) {
	stateDir := resolved.env.StateDir
	if stateDir == "" {
		return
	}
	in := resolved.input

	st := resolved.tiers.state
	if in.ProjectDir != "" {
		st.TouchRecent(in.ProjectDir)
	}
	st.LastKind = in.AgentKind
	st.LastPlacement = defaults.RememberedPlacement(in, resolved.tiers.state.LastPlacement)
	useWorktree := in.UseWorktree
	st.LastWorktree = &useWorktree
	_ = config.SaveState(stateDir, st)

	if resolved.tiers.projectKey == "" {
		return
	}
	previous, _ := resolved.tiers.projects.Get(resolved.tiers.projectKey)
	projects := resolved.tiers.projects.Touched(resolved.tiers.projectKey, config.ProjectDefaults{
		Kind:      in.AgentKind,
		Worktree:  &useWorktree,
		Placement: defaults.RememberedPlacement(in, previous.Placement),
		Base:      in.BaseRef,
	}, now)
	_ = config.SaveProjects(stateDir, projects)
}

// refuse reports a pre-flight refusal and returns its exit code, so every
// such site is one `return refuse(...)` and they all read identically.
//
// Two codes, because a pre-flight refusal has two kinds and only one of
// them is the caller's to fix. ExitUsage is the ordinary one, whose
// documented remedy is "fix the command and re-run". A question the
// filesystem or git never answered is ExitCheckTimedOut: nothing in the
// command would change the answer, and calling it usage sends the caller
// looking for a typo that is not there (#272).
//
// Sorted HERE rather than at each site, so a check added later cannot
// forget: there is one place to get it right, and errors.Is finds the
// timeout through however the site wrapped it.
func refuse(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "herdr-draft create: %v\n", err)
	if errors.Is(err, errCheckTimedOut) {
		return ExitCheckTimedOut
	}
	return ExitUsage
}
