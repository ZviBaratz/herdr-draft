// resolve.go turns a parsed command line plus the layered defaults into
// the plan.Input the form would have produced from the same inputs (spec
// §13). It is the half of this package the equivalence test pins.
package create

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ZviBaratz/herdr-draft/internal/agentopts"
	"github.com/ZviBaratz/herdr-draft/internal/app"
	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/defaults"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
	"github.com/ZviBaratz/herdr-draft/internal/linear"
	"github.com/ZviBaratz/herdr-draft/internal/pathx"
	"github.com/ZviBaratz/herdr-draft/internal/picker"
	"github.com/ZviBaratz/herdr-draft/internal/plan"
)

// claudeKind is the only agent kind clauth account pinning applies to
// (spec §6 field 7) -- the same constant internal/app and internal/plan
// each keep for their own layer.
const claudeKind = "claude"

// clauthActive is config.toml's documented `[clauth] default` sentinel for
// "whatever account is active", which is not a pin at all. AccountField
// treats it as a no-op; so does this.
const clauthActive = "active"

// clauthAuto is the `[clauth] default` / `--account` sentinel meaning "ask the
// configured account picker". Unlike clauthActive it is not a no-op: it names
// a resolution strategy, and an install with no `[clauth] picker` does not
// have it.
const clauthAuto = "auto"

// tiers is every source spec §10's resolver consumes, already loaded, plus
// the two facts about the project directory that decide which of them
// apply and where a successful create records itself.
type tiers struct {
	cfg       config.Config
	state     config.State
	projects  config.Projects
	repo      config.RepoConfig
	entry     config.ProjectDefaults
	haveEntry bool

	// projectDir is the resolved, absolute project directory every tier
	// above was loaded for.
	projectDir string
	// projectKey is spec §10's per-project memory key: the repository root
	// when the project is a repo, its canonical path otherwise. "" when the
	// directory has no identity to remember.
	projectKey string
	isGitRepo  bool
	// repoRoot is the repository root behind projectDir, "" when it is not
	// a repository or the root could not be resolved -- plan.FindSpace's
	// second path, so a project inside a repository still finds the
	// repository's own space.
	repoRoot string
	// primary is the repository's primary checkout when projectDir is inside
	// a linked worktree checkout -- a lane (#171) -- and "" otherwise. A
	// worktree session from a lane is created from it, since herdr refuses
	// the lane as a source.
	primary string
	// workspaces is the `herdr workspace list` snapshot the open-workspace
	// tier reads (defaults.Sources.Workspaces). nil when herdr could not be
	// asked: that leaves every other tier's answer standing, and run()'s
	// reachability probe -- after every check that needs no herdr, so a flag
	// typo needs no running herdr to be reported -- is what turns an
	// unreachable herdr into exit 3.
	workspaces []herdrc.WorkspaceInfo
}

// resolution is everything run() needs after the request has met the
// defaults: the plan input itself, the tiers (for the post-success state
// write) and where each resolved value came from.
type resolution struct {
	input      plan.Input
	tiers      tiers
	provenance map[string]string
	// env is the environment the tiers were actually loaded from --
	// usablePluginEnv's answer, not the raw one. The post-success state
	// write must use THIS one, or a create that refused another plugin's
	// state directory as a source would still write to it as a sink.
	env Env
}

// resolveRequest is the first half of the pre-flight: locate the project,
// load every tier, resolve the layered defaults through internal/defaults,
// apply the explicit flags on top, and refuse anything that is wrong with
// the request itself. The half that needs herdr, or spends something --
// the plan check, the reachability probe, the form's duplicate and auth
// refusals, and the `auto` pick -- is run()'s, in that order (#145).
//
// The order matters in one place only: the project directory has to be
// known before any tier can be loaded, because two of the five tiers
// (.herdr-draft.toml and projects.json) are per-project.
func resolveRequest(ctx context.Context, req request, env Env, deps Deps) (resolution, error) {
	env = usablePluginEnv(env, deps.stderr())

	cfg, err := loadUserConfig(env.ConfigDir)
	if err != nil {
		return resolution{}, err
	}
	// A line on stderr for every [clauth] warning, because a create that
	// silently launched some other way than the config file asks for is a
	// create nobody can explain -- and the stakes are high: `[clauth] launch`
	// and `[clauth] launcher` are one decision (config.Load reconciles them),
	// and every way that decision can be overridden changes which account a
	// session is billed to. config.ClauthWarnings is the same list the popup's
	// account panel reads, which is what keeps a warning added to it from
	// reaching one surface and not the other.
	for _, w := range cfg.ClauthWarnings() {
		fmt.Fprintf(deps.stderr(), "herdr-draft create: %s\n", w)
	}
	// And every [agents.options] entry config.Load refused: the same list
	// the popup's options panel shows (agent-options spec §6.2), because a
	// default that silently did not apply is a session nobody can explain.
	for _, w := range cfg.Agents.OptionWarnings {
		fmt.Fprintf(deps.stderr(), "herdr-draft create: %s\n", w)
	}

	projectDir, err := resolveProjectDir(req, deps)
	if err != nil {
		return resolution{}, err
	}

	t := loadTiers(ctx, cfg, env, deps, projectDir)
	for _, note := range t.repo.Notes {
		// Spec §11 puts these in the focused row's panel. There is no panel
		// here, and a repository key that was refused has to be visible
		// somewhere or the person who committed it will conclude it works.
		fmt.Fprintf(deps.stderr(), "herdr-draft create: %s: %s\n", config.RepoConfigFileName, note)
	}

	kinds := app.OrderedAgentKinds(cfg.Agents.Favorites)
	res := defaults.Resolve(defaults.Sources{
		Config:          cfg,
		Global:          t.state,
		Repo:            t.repo,
		Project:         t.entry,
		HaveProject:     t.haveEntry,
		KnownAgentKinds: kinds,
		Workspaces:      t.workspaces,
		ProjectDir:      t.projectDir,
		RepoRoot:        t.repoRoot,
	})
	// A line on stderr, because a create that silently used a different
	// prefix than the config file asks for is a create whose branch nobody
	// can explain. It waits for the resolution and goes through the same
	// app.BranchPrefixWarning the popup's worktree panel does, because a
	// repository's own branch_prefix outranks config.toml's: there the
	// warning's `using "<fallback>"` would name a prefix this create is not
	// using, for a key that would not have applied even if it were valid.
	if w := app.BranchPrefixWarning(cfg, res); w != "" {
		fmt.Fprintf(deps.stderr(), "herdr-draft create: %s\n", w)
	}
	// A tier's base that no longer names a commit falls back to HEAD, and
	// says so, through the same app.SettleBase the popup's worktree panel
	// does (#194). Before buildInput, which turns the resolution's
	// attribution into --json's provenance: the dropped ref's tier no longer
	// supplies what is used. Not for --base, which replaces the tier's base
	// outright -- withBase checks that one, and refuses it rather than
	// swapping it for another.
	if !req.set["base"] {
		var note string
		if res, note = app.SettleBase(ctx, deps.git(), t.projectDir, res); note != "" {
			fmt.Fprintf(deps.stderr(), "herdr-draft create: %s\n", note)
		}
	}

	issue, err := findIssue(ctx, req, cfg, env, deps)
	if err != nil {
		return resolution{}, err
	}

	prompt, promptGiven, err := readPrompt(req, deps.stdin())
	if err != nil {
		return resolution{}, err
	}

	hctx, err := herdrContext(env)
	if err != nil {
		return resolution{}, err
	}

	in, prov, err := buildInput(req, t, res, kinds, issue, explicitPrompt{text: prompt, given: promptGiven}, hctx, deps)
	if err != nil {
		return resolution{}, err
	}
	in, err = withBase(ctx, in, t, prov, req.set["base"], deps.git())
	if err != nil {
		return resolution{}, err
	}
	if err := requireContext(hctx, in); err != nil {
		return resolution{}, err
	}

	if w := reapWarning(req, in); w != "" {
		fmt.Fprintf(deps.stderr(), "herdr-draft create: %s\n", w)
	}

	// An `auto` account is still the sentinel here: turning it into a profile
	// runs the picker for real and writes its ledger, so it waits for every
	// refusal that can be known without it -- see run() (#145).
	return resolution{input: in, tiers: t, provenance: prov, env: env}, nil
}

// resolveProjectDir resolves --project, defaulting to the working directory --
// the headless answer to spec §6 field 2's "current space's repo root",
// which has no meaning outside a plugin invocation. It is made absolute
// and tilde-expanded here, at the boundary where a typed path becomes an
// argument for herdr's CLI and git (pathx's own package doc explains why
// herdr cannot be relied on to expand it).
func resolveProjectDir(req request, deps Deps) (string, error) {
	raw := req.project
	if !req.set["project"] || strings.TrimSpace(raw) == "" {
		wd, err := deps.workdir()
		if err != nil {
			return "", fmt.Errorf("no --project given and the working directory could not be determined: %w", err)
		}
		raw = wd
	}
	dir := pathx.Resolve(raw)
	if !deps.git().DirExists(dir) {
		return "", fmt.Errorf("project directory does not exist: %s", dir)
	}
	return dir, nil
}

// git returns the GitSource to use -- Deps.Git when a caller supplied one,
// internal/app's production source otherwise. Going through app rather
// than wrapping internal/gitx again keeps ONE production implementation of
// these three calls, which is the same reason this command borrows app's
// agent-kind list rather than restating it.
func (d Deps) git() GitSource {
	if d.Git != nil {
		return d.Git
	}
	return app.NewGitSource()
}

// loadTiers loads every source defaults.Resolve consumes for projectDir.
// It mirrors internal/app's own debounced dir check (async.go's
// runDirCheck/projectMemoryKey): is this a repository, what is its root,
// what does its committed .herdr-draft.toml say, and what does
// projects.json remember about it.
func loadTiers(ctx context.Context, cfg config.Config, env Env, deps Deps, projectDir string) tiers {
	t := tiers{cfg: cfg, projectDir: projectDir}
	t.isGitRepo = deps.git().IsGitRepo(projectDir)

	repoRoot := ""
	if t.isGitRepo {
		// An error here is not fatal: the form treats an unresolvable root
		// as "no repo-level config and a path-keyed memory" rather than as
		// a failure, and so does this.
		if root, err := deps.git().RepoRoot(ctx, projectDir); err == nil {
			repoRoot = root
		}
	}
	t.repoRoot = repoRoot
	if repoRoot != "" {
		// Not fatal either, and for the same reason: an unanswered question,
		// or one with no answer (gitx.PrimaryCheckout), leaves the lane as
		// the source, which herdr refuses by name (linked_worktree_source) --
		// loud, and no worse than before #171.
		if primary, err := deps.git().PrimaryCheckout(ctx, projectDir); err == nil {
			t.primary = primary
		}
	}
	t.projectKey = projectMemoryKey(projectDir, repoRoot)
	t.repo = deps.repoConfig()(repoRoot)
	t.state, t.projects = loadMemory(env.StateDir)
	t.entry, t.haveEntry = t.projects.Get(t.projectKey)
	// The same snapshot app.Bootstrap takes at form-open, so the form and
	// this verb resolve `tab in <space>` from one list (equivalence_test.go
	// hands both sides one fake runner). An error is not fatal HERE -- see
	// tiers.workspaces.
	if ws, err := deps.Runner.WorkspaceList(ctx); err == nil {
		t.workspaces = ws
	}
	return t
}

// loadUserConfig reads the user's config.toml, and exists for one reason:
// the plugin directories are NOT set for this command in its own headline
// scenario.
//
// herdr exports HERDR_WORKSPACE_ID/HERDR_TAB_ID/HERDR_PANE_ID into every
// pane's shell (herdr:src/pane.rs, apply_pane_launch_env) but exports
// HERDR_PLUGIN_CONFIG_DIR/HERDR_PLUGIN_STATE_DIR only to a launched PLUGIN
// (herdr:src/app/api/plugins/env.rs) -- which is exactly the difference
// spec §13 is about. So an empty config directory is the normal case here,
// and it must mean "there is none": config.Load joins its argument with
// "config.toml", so passing "" would read a file of that name out of the
// caller's own working directory -- a project's own config.toml, parsed as
// this plugin's, and a parse failure in it would refuse the create
// outright.
//
// config.Load handles the empty case itself, returning its own defaults,
// so this is a pass-through kept for the doc comment above it. It used to
// fake that outcome with an empty temporary directory, because Load had no
// defaults-only entry point and would otherwise have joined "" into a
// relative "config.toml"; the guard now lives in Load, where every caller
// gets it -- including internal/app's Bootstrap, which never had one.
func loadUserConfig(configDir string) (config.Config, error) {
	return config.Load(configDir)
}

// loadMemory reads last-used.json and projects.json, or answers with
// empties when there is no state directory -- see loadUserConfig for why
// that is the normal case. Both loaders now refuse an empty directory
// themselves rather than joining it into a relative path, so this no
// longer needs a guard of its own.
//
// Neither loader ever returns a non-nil error (spec §12: state is
// loss-tolerant); their error returns exist for API symmetry.
func loadMemory(stateDir string) (config.State, config.Projects) {
	state, _ := config.LoadState(stateDir)
	projects, _ := config.LoadProjects(stateDir)
	return state, projects
}

// usablePluginEnv keeps the plugin-invocation half of env only when it is
// DEMONSTRABLY ours, and says, once, when this create is therefore
// resolving against less than the form would.
//
// Two ways it can be less. The plainer one is that the variables are
// simply unset, which is the normal headless case (see loadUserConfig).
// The other is that they are not ours: a shell started inside another
// plugin's pane can inherit its whole HERDR_PLUGIN_* environment. Using
// them would read that plugin's config.toml as ours, write our state
// files into its state directory, and -- worse -- take its invocation
// context, whose pane and workspace ids are not necessarily where this
// command is running. Dropping all four together is the only coherent
// answer: they are one environment, not four variables.
//
// $HERDR_PLUGIN_ID is the only variable that says whose the other three
// are, so the test is that it MATCHES -- not merely that it does not
// conflict (#91). An unset id used to pass: the guard read
// `PluginID != "" && PluginID != pluginID`, and the leak this command
// actually meets does not carry one. Observed live, with the directories
// pointing at another plugin: three of our state files written into its
// state directory, its projects.json read back into a resolution and
// reported truthfully as `"placement": "projects.json"`, and its (empty)
// config.toml standing in for the user's own, which silently dropped
// [agents.extra_args] and launched on the wrong model.
//
// Absent is not the same as matching, and treating it as ours costs
// nothing to give up: herdr never produces that shape for a plugin it
// launched itself. plugin_path_env is the only thing that exports the
// directory pair (herdr:src/app/api/plugins/env.rs), it has exactly two
// callers, and both push HERDR_PLUGIN_ID in the same function
// (https://github.com/herdrdev/herdr/blob/b1ff4582/src/app/api/plugins/panes.rs#L251
// for a plugin pane, .../runtime.rs#L39 for an action, event or link
// command). So "the directories are set" is evidence of nothing on its
// own, and the id is what makes it evidence.
//
// The one caller who has to change is the person following this project's
// own advice: exporting the two directories by hand into a plain shell
// (README, docs/manual-smoke.md's Route B) now needs HERDR_PLUGIN_ID
// beside them. Both messages below say so, because advice that does not
// survive being followed is worse than none.
//
// Silence in any case would be worse than a line on stderr: the whole
// point of spec §13 is that the command and the form produce the same
// session, and one that quietly resolved from different inputs would
// produce a different session with nothing anywhere saying why.
func usablePluginEnv(env Env, stderr io.Writer) Env {
	pluginEnvSet := env.ConfigDir != "" || env.StateDir != "" || env.ContextJSON != ""
	if pluginEnvSet && env.PluginID != pluginID {
		whose := fmt.Sprintf("belongs to plugin %q, not to %q", env.PluginID, pluginID)
		if env.PluginID == "" {
			whose = fmt.Sprintf("does not say whose it is -- HERDR_PLUGIN_ID is not set, so it is not demonstrably %q's", pluginID)
		}
		fmt.Fprintf(stderr,
			"herdr-draft create: the HERDR_PLUGIN_* environment here %s -- ignoring it and resolving from built-in defaults; export HERDR_PLUGIN_ID=%s together with HERDR_PLUGIN_CONFIG_DIR=$(herdr plugin config-dir %s) and HERDR_PLUGIN_STATE_DIR to resolve exactly what the form resolves\n",
			whose, pluginID, pluginID)
		env.PluginID, env.ConfigDir, env.StateDir, env.ContextJSON = "", "", "", ""
		return env
	}

	var missing []string
	if env.ConfigDir == "" {
		missing = append(missing, "HERDR_PLUGIN_CONFIG_DIR")
	}
	if env.StateDir == "" {
		missing = append(missing, "HERDR_PLUGIN_STATE_DIR")
	}
	if len(missing) > 0 {
		fmt.Fprintf(stderr,
			"herdr-draft create: %s not set -- resolving without your config.toml and remembered defaults; export them, and HERDR_PLUGIN_ID=%s beside them (herdr plugin config-dir %s), to resolve exactly what the form resolves\n",
			strings.Join(missing, " and "), pluginID, pluginID)
	}
	return env
}

// projectMemoryKey mirrors internal/app's function of the same name: the
// ORIGIN repository root when one resolved -- so every worktree of one
// repository shares a single entry -- and the canonical path otherwise,
// both normalized through pathx.CanonicalKey so one directory cannot
// acquire two keys.
func projectMemoryKey(projectDir, repoRoot string) string {
	if repoRoot != "" {
		return pathx.CanonicalKey(repoRoot)
	}
	return pathx.CanonicalKey(projectDir)
}

// buildInput assembles plan.Input from the explicit flags and the resolved
// defaults -- the headless counterpart of internal/app's buildPlanInput,
// and the function equivalence_test.go compares against it.
//
// Every field follows the same rule: an explicitly given flag wins;
// otherwise the resolver's answer applies, in the same shape the form
// would have applied it (a worktree only where one is possible, a
// placement that only applies without one -- placement spec §14 -- an
// agent kind falling through to the first favorite).
func buildInput(req request, t tiers, res defaults.Resolved, kinds []string, issue *linear.Issue, prompt explicitPrompt, hctx herdrc.Context, deps Deps) (plan.Input, map[string]string, error) {
	prov := provenanceOf(res)

	// Blank is judged on the cleaned text, as the form judges it: a paste
	// of nothing but control characters leaves the title row empty, so a
	// Linear issue picked afterwards still seeds it.
	raw, from := req.title, "--title"
	if !req.set["title"] || strings.TrimSpace(plan.SanitizeTitle(raw)) == "" {
		if issue != nil {
			raw, from = issue.Title, issue.Identifier+"'s title"
			// A --title that was given and is not used is said, or the
			// caller finds a different title on the session and no reason.
			if req.set["title"] {
				fmt.Fprintf(deps.stderr(), "herdr-draft create: --title is blank as the form's title row would hold it, so %s is used instead\n", from)
			}
		}
	}
	// The title row's rules, from the definitions the row itself is held
	// to, so one title builds one session: cleaned (#178) and then cut to
	// spec §6 field 3's cap (#176), in the row's order, before anything
	// reads it. The branch below is derived from it, and the title-in-use
	// refusal compares it. The form changes a title without a word because
	// its row shows the result. Nothing shows anything here, so a caller
	// whose title came back changed is told, once.
	clean := plan.SanitizeTitle(raw)
	if strings.TrimSpace(clean) == "" {
		// Refused, as the form refuses an empty title row. When cleaning is
		// what emptied it, the reason names the title that was given:
		// "a title is required: pass --title" would tell a caller who did
		// pass one to do it again.
		if strings.TrimSpace(raw) != "" {
			return plan.Input{}, nil, fmt.Errorf("%s has nothing the form's title row keeps; pass a --title with text in it", from)
		}
		return plan.Input{}, nil, fmt.Errorf("a title is required: pass --title, or --issue to take one from Linear")
	}
	title := plan.CutTitle(clean)
	if note := titleNote(from, raw, clean, title); note != "" {
		fmt.Fprintf(deps.stderr(), "herdr-draft create: %s\n", note)
	}

	issueBranch := ""
	if issue != nil {
		issueBranch = issue.BranchName
	}
	branch := app.BranchFor(res, issueBranch, title)
	if req.set["branch"] {
		branch = req.branch
		// A branch given outright never consults the prefix, so the prefix
		// stops being a resolved value: reporting the tier that would have
		// supplied it would attribute a value nothing used.
		delete(prov, defaults.FieldBranchPrefix)
		prov["branch"] = provenanceFlag
	}

	base := res.BaseRef
	if req.set["base"] {
		// HEAD and @ are the form's one HEAD row, as they are from a tier
		// (#194): "" to herdr, to projects.json and to --json alike, with the
		// provenance still the caller's.
		base = defaults.NormalizeBase(req.base)
		prov[defaults.FieldBaseRef] = provenanceFlag
	}

	// The resolved toggle can only apply where a worktree is possible --
	// WorktreeField.SetOn's own precondition, expressed here as the same
	// conjunction buildPlanInput makes (Enabled() && On()). An EXPLICIT
	// --worktree is deliberately not filtered that way: plan.Build refuses
	// it with a message naming the directory, which is a better answer to
	// "create a worktree here" than silently not creating one.
	useWorktree := res.UseWorktree && t.isGitRepo
	if req.worktree != nil {
		useWorktree = *req.worktree
		prov[defaults.FieldWorktree] = provenanceFlag
	}

	placement := res.Placement
	if req.set["placement"] {
		p, _ := defaults.ParsePlacement(req.placement) // already validated
		placement = p
		prov[defaults.FieldPlacement] = provenanceFlag
	}
	// The space tab-in aims at: the resolver's (the workspace already
	// holding this project), or the one --workspace names outright. The
	// flag implies the placement -- naming a destination and then not
	// using it is not a request anyone means -- and validate() has already
	// refused it alongside any OTHER explicit placement. #128 asked for
	// exactly this flag; it is the first that names a workspace other than
	// the invoking one, and equivalence_test.go treats it as a flag
	// (provenance "flag"), not as context.
	// A worktree session runs in the worktree's own space (placement spec
	// §14), so a placement or a destination given outright asks for
	// something it cannot have. Refused rather than dropped: the form
	// offers no such combination, and #145-147's rule is that `create`
	// refuses what the form refuses. A REMEMBERED placement is different --
	// nobody asked for it on this command line -- and simply does not apply
	// (below). Checked before --workspace's id is looked up, so a caller is
	// not sent to fix an id that could never be used.
	if useWorktree {
		if err := refuseWorktreePlacement(req, placement, prov[defaults.FieldWorktree]); err != nil {
			return plan.Input{}, nil, err
		}
	}
	space := res.Space
	if req.set["workspace"] {
		named, ok := workspaceByID(t.workspaces, req.workspace)
		if !ok {
			return plan.Input{}, nil, fmt.Errorf("unknown --workspace %q: no open workspace has that id (herdr workspace list)", req.workspace)
		}
		space = named
		placement = plan.PlacementTabIn
		prov[defaults.FieldPlacement] = provenanceFlag
	}

	if useWorktree && !req.set["placement"] {
		prov[defaults.FieldPlacement] = provenanceWorktree
	}
	placement = plan.EffectivePlacement(useWorktree, placement)

	kind, err := agentKind(req, res, kinds, prov)
	if err != nil {
		return plan.Input{}, nil, err
	}

	// The pane-reaper toggle (reap spec §7.2): a flag wins, else the
	// resolver's config.toml-or-built-in answer. Its position, not whether
	// the instruction is appended -- plan.PromptText decides that.
	markReady := res.MarkReady
	if req.reap != nil {
		markReady = *req.reap
		prov[defaults.FieldMarkReady] = provenanceFlag
	}

	options, err := agentOptions(req, t.cfg, kind, prov)
	if err != nil {
		return plan.Input{}, nil, err
	}

	return plan.Input{
		ProjectDir:    t.projectDir,
		Title:         title,
		Branch:        branch,
		BaseRef:       base,
		UseWorktree:   useWorktree,
		IsGitRepo:     t.isGitRepo,
		Placement:     placement,
		Space:         space,
		AgentKind:     kind,
		ExtraArgs:     t.cfg.Agents.ExtraArgs[kind],
		AgentOptions:  options,
		AccountPin:    accountPin(req, t.cfg, kind),
		AccountLaunch: accountLaunch(t.cfg),
		// Off the same config as the form's buildPlanInput. Both paths must
		// carry both of these or TestFormAndCommandProduceTheSamePlan fails --
		// which is the invariant working: a launcher (or a launch mode) the
		// popup honours and this verb ignores would put the two on different
		// accounts for one config.
		Launcher:  t.cfg.Clauth.Launcher,
		Prompt:    promptText(prompt, issue, t.cfg),
		MarkReady: markReady,
		Ctx:       hctx,

		DetectionTimeout: time.Duration(t.cfg.Timeouts.DetectionMS) * time.Millisecond,
		PromptTimeout:    time.Duration(t.cfg.Timeouts.PromptWaitMS) * time.Millisecond,
		// `[worktree] trust_repository`, straight off the resolver -- see
		// buildPlanInput's own note. There is deliberately no
		// `--trust-repository` FLAG on this verb: every flag here has a
		// form row behind it, this value has none, and equivalence_test.go
		// exists to prove the two paths build the same plan.Input. A flag
		// only `create` could set would be the first thing to break that.
		TrustRepository: res.TrustRepository,
	}, prov, nil
}

// withBase checks the base a worktree is cut from against the checkout it
// will be cut in, which is the project: it fills in plan.Input.Linked for a
// worktree session from a linked checkout (#171), refuses an explicit --base
// that names no commit (#194), and maps one that is HEAD itself spelled
// another way to the HEAD row (app.NamesHead). A tier's base has already
// been through app.SettleBase, so the only one that can fail here is one the
// caller named -- or HEAD itself, in a repository with no commit yet.
//
// --base is asked about only where a worktree will use it: in a repository,
// with a worktree. Nothing else reads it, a session without a worktree was
// always created whatever --base said, and a non-git --worktree has a better
// reason to be refused, which plan.Build gives.
//
// From a lane, Linked is the primary checkout herdr will accept as the
// source, and the commit the base names IN the linked checkout -- HEAD's when
// no base was chosen, which is what spawn skill §3 promises an unset --base
// means. herdr runs `git worktree add` in the source, so a base left for it
// to resolve would be resolved in the primary checkout: an unset one, or any
// per-checkout ref (HEAD~1, HEAD@{1}), as the primary's commit. For a branch
// the commit is the same either way. A commit, so a detached HEAD works too,
// and so --json can say exactly what an unset base turned out to be.
//
// Outside a lane only an explicit --base is asked about, one `rev-parse`
// before anything exists: herdr would otherwise refuse it at the worktree
// step, after the create had started. The lane's own question is that same
// check, so it is asked once, in the lane.
//
// Resolved here rather than in loadTiers because only these cases need it,
// and after buildInput because only buildInput knows whether there is a
// worktree and which base. It is the command's half of what the form does at
// submit (app's ResolveLinkedCommit).
//
// The commit is not remembered. BaseRef stays what was chosen, and
// projects.json records that, since it is keyed on the origin root and a
// remembered commit would pin every later session in the repository to it.
func withBase(ctx context.Context, in plan.Input, t tiers, prov map[string]string, explicit bool, git GitSource) (plan.Input, error) {
	lane := in.UseWorktree && t.primary != ""
	chosen := explicit && in.BaseRef != "" && in.UseWorktree && t.isGitRepo
	if !lane && !chosen {
		return in, nil
	}
	ref := cmp.Or(in.BaseRef, "HEAD")
	commit, err := git.ResolveCommit(ctx, in.ProjectDir, ref)
	switch {
	case err != nil && lane:
		return plan.Input{}, fmt.Errorf("%s is a linked worktree checkout, so the base is resolved there, and %s could not be: %v -- pass --base to choose another", in.ProjectDir, ref, err)
	case err != nil:
		return plan.Input{}, fmt.Errorf("--base %q names no commit in %s -- pass a branch, tag or commit that exists there, or leave --base off", ref, in.ProjectDir)
	}
	if chosen && app.NamesHead(ctx, git, in.ProjectDir, ref, commit) {
		in.BaseRef = ""
	}
	if !lane {
		return in, nil
	}
	in.Linked = plan.LinkedCheckout{RepoRoot: t.primary, Commit: commit}
	// The checkout supplied the base only when nobody chose one: a --base
	// HEAD, or a remembered one, is still the caller's or the tier's, and
	// only reads as "" because HEAD is the HEAD row.
	if in.BaseRef == "" && prov[defaults.FieldBaseRef] == defaults.TierBuiltin.String() {
		prov[defaults.FieldBaseRef] = provenanceCheckout
	}
	return in, nil
}

// agentKind resolves the agent kind: --agent when given (rejected unless
// it is a kind this binary can actually start, which is the same list the
// form's own picker offers), then the resolver's answer, then the first
// favorite -- AgentField's own "index 0 is the configured default"
// contract, which SetKind's no-op-for-"" behavior leaves standing.
func agentKind(req request, res defaults.Resolved, kinds []string, prov map[string]string) (string, error) {
	if req.set["agent"] {
		for _, k := range kinds {
			if k == req.agent {
				prov[defaults.FieldAgentKind] = provenanceFlag
				return k, nil
			}
		}
		return "", fmt.Errorf("unknown --agent %q: not one of the agent kinds this plugin can start", req.agent)
	}
	if res.AgentKind != "" {
		return res.AgentKind, nil
	}
	if len(kinds) > 0 {
		return kinds[0], nil
	}
	return "", fmt.Errorf("no agent kind to start: set --agent, or [agents] favorites in config.toml")
}

// agentOptions resolves the session options for the kind this create will
// launch (agent-options spec §8.1): defaults.AgentOptions for that kind --
// config.toml over built-in inherit, the one chain the form reads too --
// then every option flag given, each normalised against the kind's own
// declaration.
//
// An option the kind does not declare is refused rather than dropped. The
// form cannot produce one -- its row only offers what the kind declares --
// and #145-147's rule is that create refuses what the form refuses; a
// silently ignored `--effort` on a codex session is a script that thinks it
// asked for something.
func agentOptions(req request, cfg config.Config, kind string, prov map[string]string) (agentopts.Values, error) {
	vals, from := defaults.AgentOptions(cfg, kind)
	for field, tier := range from {
		prov[field] = tier.String()
	}
	for _, name := range agentopts.Names() {
		flag := agentopts.FlagName(name)
		if !req.set[flag] {
			continue
		}
		raw := *req.options[name]
		// Normalize("") succeeds for every option the kind declares, so an
		// error here means the kind declares no such option at all -- which
		// deserves its own sentence rather than an "invalid value" one.
		if _, err := agentopts.Normalize(kind, name, ""); err != nil {
			return nil, fmt.Errorf("--%s does not apply to agent kind %s: %v", flag, kind, err)
		}
		v, err := agentopts.Normalize(kind, name, raw)
		if err != nil {
			return nil, fmt.Errorf("invalid --%s %q: %v", flag, raw, err)
		}
		if v == "" {
			delete(vals, name)
		} else {
			if vals == nil {
				vals = agentopts.Values{}
			}
			vals[name] = v
		}
		prov[defaults.FieldAgentOption(name)] = provenanceFlag
	}
	if len(vals) == 0 {
		return nil, nil
	}
	return vals, nil
}

// accountPin resolves the clauth pin the same way Model.accountPin does:
// only claude can be pinned (spec §6 field 7, and plan.Build refuses any
// other combination), and the configured `[clauth] default` applies only
// when clauth is enabled and names a real profile rather than the "active"
// sentinel.
//
// An explicit --account naming a profile is passed through even for a
// non-claude kind, deliberately: plan.Build's own refusal names the rule,
// which is more useful than silently dropping the flag. `active` names no
// profile, so it is no pin for any kind (#146).
//
// `auto` is returned UNRESOLVED, as the sentinel: resolving it runs a
// subprocess, and this function is pure so that every precedence rule stays
// readable in one place. resolveAccount below is what turns it into a profile
// name, once, at the point the request is otherwise complete.
func accountPin(req request, cfg config.Config, kind string) string {
	if req.set["account"] {
		// `active` means no pin wherever it is written, the flag included:
		// passed through, it typed `clauth start active` into the pane (#146).
		if req.account == clauthActive {
			return ""
		}
		return req.account
	}
	if kind != claudeKind {
		return ""
	}
	if cfg.Clauth.Enabled != nil && !*cfg.Clauth.Enabled {
		return ""
	}
	if cfg.Clauth.Default == clauthActive {
		return ""
	}
	return cfg.Clauth.Default
}

// accountPicker is the picker `--account auto` resolves through: whatever a
// test injected, else one built from `[clauth] picker`, else nil.
//
// The nil-means-production shape every other collaborator in Deps has (Git,
// Linear, RepoConfig), and for the same reason: this package's tests must
// never run a subprocess, and production must never need a caller to have
// loaded the config twice to hand it one.
//
// Note what is NOT here: any search of PATH. A picker is named or there is
// none -- see config.ClauthConfig.Picker.
//
// Unlike the popup, this does not PROBE the named executable first. The popup
// probes because it is about to offer a row that has to work later, silently,
// on a submit; here the very next thing that happens is the real call, and its
// failure is reported in full to a person reading stderr. A probe would only
// double the work and halve the error message.
func accountPicker(cfg config.Config, deps Deps) picker.Source {
	if deps.Picker != nil {
		return deps.Picker
	}
	if bin := strings.TrimSpace(cfg.Clauth.Picker); bin != "" {
		return picker.CLI{Bin: bin}
	}
	return nil
}

// resolveAccount turns an `auto` pin into a real profile and its config dir.
//
// --strict, and not --dry-run. Strict because this verb has nobody at the
// keyboard: a picker that would have prompted must refuse instead. Not dry-run
// because this is the real launch, and the protocol's ledger write is what
// keeps two concurrent creates off one account.
//
// The picker's `warnings` come back beside the input, for run() to print:
// the popup draws them on the account panel, and this verb has no panel
// (#165).
func resolveAccount(ctx context.Context, in plan.Input, src picker.Source) (plan.Input, []string, error) {
	if in.AccountPin != clauthAuto {
		return in, nil, nil
	}
	if err := requirePicker(in, src); err != nil {
		return in, nil, err
	}
	res, err := src.Pick(ctx, in.ProjectDir, picker.Options{Strict: true})
	if err != nil {
		return in, nil, err
	}
	in.AccountPin = res.Profile
	in.AccountConfigDir = res.ConfigDir
	return in, res.Warnings, nil
}

// requirePicker refuses an `auto` account when there is no picker to resolve
// it through. Separate from resolveAccount so run() can make this refusal
// before the reachability probe without running the pick itself.
func requirePicker(in plan.Input, src picker.Source) error {
	if in.AccountPin == clauthAuto && src == nil {
		return fmt.Errorf("--account auto needs an account picker: set `[clauth] picker` in config.toml to an executable implementing the picker protocol")
	}
	return nil
}

// accountLaunch is `[clauth] launch`, mapped to plan's enum -- the same
// mapping app.Model.accountLaunch performs, and deliberately the same
// duplicated two-line body rather than a shared helper: equivalence_test.go
// compares the two paths' plan.Input, and a shared helper would make it unable
// to catch the two drifting.
func accountLaunch(cfg config.Config) plan.LaunchMode {
	if cfg.Clauth.Launch == config.ClauthLaunchWrapper {
		return plan.LaunchWrapper
	}
	return plan.LaunchClauthStart
}

// explicitPrompt is --prompt's already-resolved value: the text, and
// whether the flag was given at all. The distinction is what lets an
// explicit `--prompt ""` mean "start the agent with no prompt" while an
// absent flag still lets a chosen Linear issue seed one.
type explicitPrompt struct {
	text  string
	given bool
}

// promptText resolves the initial prompt: --prompt (with "-" already read
// from stdin by readPrompt) or, failing that, the prompt a chosen Linear
// issue seeds through the USER's own template -- never a repository's,
// which spec §11 forbids for exactly the reason it would be effective: it
// would become the agent's first instruction.
func promptText(prompt explicitPrompt, issue *linear.Issue, cfg config.Config) string {
	if prompt.given {
		return prompt.text
	}
	if issue == nil {
		return ""
	}
	return app.RenderPromptTemplate(cfg.Linear.PromptTemplate, *issue)
}

// findIssue resolves --issue to one of the caller's assigned Linear issues
// (spec §6 field 1's own source), matched on identifier, case-insensitively
// -- "lin-123" and "LIN-123" are the same issue and a shell has no reason
// to prefer either.
func findIssue(ctx context.Context, req request, cfg config.Config, env Env, deps Deps) (*linear.Issue, error) {
	if !req.set["issue"] || strings.TrimSpace(req.issue) == "" {
		return nil, nil
	}
	src, err := deps.linear(cfg, env.ConfigDir)
	if err != nil {
		return nil, fmt.Errorf("--issue %s: %w", req.issue, err)
	}
	if src == nil {
		return nil, fmt.Errorf("--issue %s: Linear is not configured (set [linear] api_key_cmd in config.toml)", req.issue)
	}
	issues, err := src.AssignedIssues(ctx)
	if err != nil {
		return nil, fmt.Errorf("--issue %s: %w", req.issue, err)
	}
	want := strings.ToLower(strings.TrimSpace(req.issue))
	for i := range issues {
		if strings.ToLower(issues[i].Identifier) == want {
			return &issues[i], nil
		}
	}
	return nil, fmt.Errorf("--issue %s: no assigned Linear issue with that identifier", req.issue)
}

// linear returns the issue source --issue searches: Deps.Linear when a
// caller supplied one, otherwise a real client built from the user's own
// configured API key. (nil, nil) means Linear is not configured at all,
// which is the same distinction app.Bootstrap draws.
func (d Deps) linear(cfg config.Config, configDir string) (IssueSource, error) {
	if d.Linear != nil {
		return d.Linear, nil
	}
	key, err := linear.ResolveAPIKey(cfg.Linear.APIKeyCmd, cfg.Linear.APIKey, configDir)
	if err != nil {
		return nil, err
	}
	if key == "" {
		return nil, nil
	}
	return &linear.Client{APIKey: key}, nil
}

// herdrContext resolves herdr's invocation context. A real
// $HERDR_PLUGIN_CONTEXT_JSON wins field by field -- `herdr-draft create`
// run as a plugin should use the richer context rather than ignore it --
// and the three per-pane variables fill in whatever it does not carry,
// which for a plain shell inside a pane is all of it (spec §13).
func herdrContext(env Env) (herdrc.Context, error) {
	var ctx herdrc.Context
	if strings.TrimSpace(env.ContextJSON) != "" {
		parsed, err := herdrc.ParseContext(env.ContextJSON)
		if err != nil {
			return herdrc.Context{}, err
		}
		ctx = parsed
	}
	if ctx.WorkspaceID == "" {
		ctx.WorkspaceID = env.WorkspaceID
	}
	if ctx.TabID == "" {
		ctx.TabID = env.TabID
	}
	if ctx.FocusedPaneID == "" {
		ctx.FocusedPaneID = env.PaneID
	}
	return ctx, nil
}

// requireContext is spec §13's lazy context requirement: only tab-here and
// split-here need to know where "here" is. The message names the missing
// variable exactly, because "missing context" tells a script author
// nothing they can act on.
//
// tab-here asks for both the workspace and the tab id even though `herdr
// tab create --workspace` consumes only the first: herdr exports the three
// together for every pane, so an environment carrying one without the
// other is not a pane, and guessing a workspace for "here" is precisely
// what this check exists to prevent.
func requireContext(hctx herdrc.Context, in plan.Input) error {
	switch in.Placement {
	case plan.PlacementTabHere:
		if hctx.WorkspaceID == "" {
			return fmt.Errorf("HERDR_WORKSPACE_ID is not set: --placement tab-here creates the tab in the workspace this pane belongs to")
		}
		if hctx.TabID == "" {
			return fmt.Errorf("HERDR_TAB_ID is not set: --placement tab-here needs the pane environment herdr exports")
		}
	case plan.PlacementSplitHere:
		if hctx.FocusedPaneID == "" {
			return fmt.Errorf("HERDR_PANE_ID is not set: --placement split-here splits the pane it is run from")
		}
	case plan.PlacementTabIn:
		// Not a context requirement -- tab-in needs none of the three
		// pane variables, which is the whole point of #128 -- but the
		// same kind of refusal: a destination this command would have to
		// guess. plan.Build refuses it too; this names the remedy.
		if in.Space.WorkspaceID == "" {
			return fmt.Errorf("no open workspace holds %s: --placement tab-in needs one; name it with --workspace <id> (herdr workspace list), or use --placement new-space", in.ProjectDir)
		}
	}
	return nil
}

// refuseWorktreePlacement names what is wrong with an explicit --workspace
// or a non-new-space --placement under a worktree, and the remedy. Where the
// worktree came from is part of the sentence when it was not the flag,
// because a caller who never typed --worktree cannot otherwise tell why a
// placement they did type is being refused.
func refuseWorktreePlacement(req request, placement plan.Placement, worktreeFrom string) error {
	why := "a worktree session runs in the worktree's own space, which herdr groups under the repository's"
	if worktreeFrom != provenanceFlag {
		why += " -- and the worktree is on here from " + worktreeFrom
	}
	switch {
	case req.set["workspace"]:
		return fmt.Errorf("--workspace needs --no-worktree: %s", why)
	case req.set["placement"] && placement != plan.PlacementNewSpace:
		return fmt.Errorf("--placement %s needs --no-worktree: %s", defaults.PlacementValue(placement), why)
	}
	return nil
}

// reapWarning names why an explicit --reap will not append anything (reap
// spec §7.3). Not a refusal, because the form does not refuse the same
// toggle, and only for the FLAG: a config.toml default nobody asked for on
// this command line is not news when it does not apply.
func reapWarning(req request, in plan.Input) string {
	if req.reap == nil || !*req.reap || plan.ReapApplies(in.Prompt) {
		return ""
	}
	if strings.TrimSpace(in.Prompt) == "" {
		return "--reap has no effect: there is no prompt to add pane-reaper's instruction to"
	}
	return "--reap has no effect: a prompt starting with / is a slash command, and the instruction would reach it as arguments"
}

// titleNote is the stderr line for a title buildInput changed, or "" for
// one it used as given: raw as the caller or the issue gave it, clean after
// plan.SanitizeTitle, and used after plan.CutTitle. One line covers both
// changes and names only the title actually used -- a line per change
// would have the first quote a title the second then cut.
func titleNote(from, raw, clean, used string) string {
	const unkept = "has characters the form's title row does not keep (a tab, CR or LF becomes a space, and the rest are dropped)"
	cleaned, cut := clean != raw, used != clean
	switch {
	case cleaned && cut:
		return fmt.Sprintf("%s %s, and a title is capped at %d runes; using %q", from, unkept, plan.TitleMaxRunes, used)
	case cleaned:
		return fmt.Sprintf("%s %s; using %q", from, unkept, used)
	case cut:
		return fmt.Sprintf("%s is %d runes and a title is capped at %d, as in the form; using %q",
			from, utf8.RuneCountInString(clean), plan.TitleMaxRunes, used)
	}
	return ""
}

// workspaceByID finds the open workspace --workspace names, so the plan
// carries its label as well as its id and the report can say where the
// tab went in the same words the form would.
func workspaceByID(workspaces []herdrc.WorkspaceInfo, id string) (plan.Space, bool) {
	for _, w := range workspaces {
		if w.WorkspaceID == id {
			label := strings.TrimSpace(w.Label)
			if label == "" {
				label = id
			}
			return plan.Space{WorkspaceID: id, Label: label}, true
		}
	}
	return plan.Space{}, false
}

// provenanceFlag, provenanceWorktree and provenanceCheckout are the
// provenance values spec §10's tier names cannot express: a value the
// caller gave outright on the command line, the one value a worktree
// decides by itself (placement spec §14: a worktree session's placement is
// its own space), and the base a linked checkout supplies when none was
// chosen -- its own commit (#171).
const (
	provenanceFlag     = "flag"
	provenanceWorktree = "worktree"
	provenanceCheckout = "checkout"
)

// provenanceOf turns the resolver's own tier attribution into the string
// map --json prints (spec §10: "the resolver reports which tier supplied
// each value, which is what lets ... `create --json` print its
// provenance"). Callers overwrite individual entries as explicit flags
// take over.
func provenanceOf(res defaults.Resolved) map[string]string {
	prov := make(map[string]string, len(res.From))
	for field, tier := range res.From {
		prov[field] = tier.String()
	}
	return prov
}
