// resolve.go turns a parsed command line plus the layered defaults into
// the plan.Input the form would have produced from the same inputs (spec
// §13). It is the half of this package the equivalence test pins.
package create

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/ZviBaratz/herdr-draft/internal/app"
	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/defaults"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
	"github.com/ZviBaratz/herdr-draft/internal/linear"
	"github.com/ZviBaratz/herdr-draft/internal/pathx"
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
}

// resolution is everything run() needs after the request has met the
// defaults: the plan input itself, the tiers (for the post-success state
// write) and where each resolved value came from.
type resolution struct {
	input      plan.Input
	tiers      tiers
	provenance map[string]string
}

// resolveRequest is the whole pre-flight: locate the project, load every
// tier, resolve the layered defaults through internal/defaults, apply the
// explicit flags on top, and refuse anything that cannot work.
//
// The order matters in one place only: the project directory has to be
// known before any tier can be loaded, because two of the five tiers
// (.herdr-draft.toml and projects.json) are per-project.
func resolveRequest(ctx context.Context, req request, env Env, deps Deps) (resolution, error) {
	warnMissingPluginDirs(env, deps.stderr())

	cfg, err := loadUserConfig(env.ConfigDir)
	if err != nil {
		return resolution{}, err
	}
	if cfg.BranchPrefixWarning != "" {
		// The form shows this in a panel; here it is a line on stderr,
		// because a create that silently used a different prefix than the
		// config file asks for is a create whose branch nobody can explain.
		fmt.Fprintf(deps.stderr(), "herdr-draft create: %s\n", cfg.BranchPrefixWarning)
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
	})

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
	if err := requireContext(hctx, in); err != nil {
		return resolution{}, err
	}
	return resolution{input: in, tiers: t, provenance: prov}, nil
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
	t.projectKey = projectMemoryKey(projectDir, repoRoot)
	t.repo = deps.repoConfig()(repoRoot)
	t.state, t.projects = loadMemory(env.StateDir)
	t.entry, t.haveEntry = t.projects.Get(t.projectKey)
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
// An empty temporary directory is the input that yields exactly
// config.Load's own defaults: it has no defaults-only entry point, and its
// argument is a directory rather than a file path.
func loadUserConfig(configDir string) (config.Config, error) {
	if configDir != "" {
		return config.Load(configDir)
	}
	empty, err := os.MkdirTemp("", "herdr-draft-no-config-")
	if err != nil {
		return config.Config{}, fmt.Errorf("no HERDR_PLUGIN_CONFIG_DIR, and no temporary directory to fall back on: %w", err)
	}
	defer func() { _ = os.RemoveAll(empty) }()
	return config.Load(empty)
}

// loadMemory reads last-used.json and projects.json, or answers with
// empties when there is no state directory -- see loadUserConfig for why
// that is the normal case, and note that the same relative-path hazard
// applies: config.LoadState("") would read (and, but for remember()'s own
// guard, later write) state files in the caller's working directory.
//
// Neither loader ever returns a non-nil error (spec §12: state is
// loss-tolerant); their error returns exist for API symmetry.
func loadMemory(stateDir string) (config.State, config.Projects) {
	if stateDir == "" {
		return config.State{}, config.Projects{}
	}
	state, _ := config.LoadState(stateDir)
	projects, _ := config.LoadProjects(stateDir)
	return state, projects
}

// warnMissingPluginDirs says, once, that this create is resolving against
// less than the form would. Silence would be worse: the whole point of
// spec §13 is that the two produce the same session, and a create that
// ignored the user's config.toml because a variable was unset would
// produce a DIFFERENT one with nothing anywhere saying why.
func warnMissingPluginDirs(env Env, stderr io.Writer) {
	var missing []string
	if env.ConfigDir == "" {
		missing = append(missing, "HERDR_PLUGIN_CONFIG_DIR")
	}
	if env.StateDir == "" {
		missing = append(missing, "HERDR_PLUGIN_STATE_DIR")
	}
	if len(missing) == 0 {
		return
	}
	fmt.Fprintf(stderr,
		"herdr-draft create: %s not set -- resolving without your config.toml and remembered defaults; export them (herdr plugin config-dir draft) to resolve exactly what the form resolves\n",
		strings.Join(missing, " and "))
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
// placement snapped to New space when a worktree wins, an agent kind
// falling through to the first favorite).
func buildInput(req request, t tiers, res defaults.Resolved, kinds []string, issue *linear.Issue, prompt explicitPrompt, hctx herdrc.Context, deps Deps) (plan.Input, map[string]string, error) {
	prov := provenanceOf(res)

	title := req.title
	if !req.set["title"] || strings.TrimSpace(title) == "" {
		if issue != nil {
			title = issue.Title
		}
	}
	if strings.TrimSpace(title) == "" {
		return plan.Input{}, nil, fmt.Errorf("a title is required: pass --title, or --issue to take one from Linear")
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
		base = req.base
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
	if useWorktree && placement != plan.PlacementNewSpace {
		// PlacementField goes inert and snaps back to New space the moment
		// a worktree turns on, because plan.Build ignores Placement
		// entirely for a worktree. Matching that here is what keeps the two
		// paths producing the same plan.Input.
		//
		// Only an EXPLICIT --placement is worth a line about it: a flag
		// quietly dropped is worse than one refused, but a REMEMBERED
		// placement being overridden by a worktree is the form's normal
		// resting behavior and nothing the caller asked for.
		if req.set["placement"] {
			fmt.Fprintf(deps.stderr(), "herdr-draft create: --placement %s ignored: a worktree always opens a new space\n",
				defaults.PlacementValue(placement))
		}
		placement = plan.PlacementNewSpace
		prov[defaults.FieldPlacement] = provenanceWorktree
	}

	kind, err := agentKind(req, res, kinds, prov)
	if err != nil {
		return plan.Input{}, nil, err
	}

	return plan.Input{
		ProjectDir:  t.projectDir,
		Title:       title,
		Branch:      branch,
		BaseRef:     base,
		UseWorktree: useWorktree,
		IsGitRepo:   t.isGitRepo,
		Placement:   placement,
		AgentKind:   kind,
		ExtraArgs:   t.cfg.Agents.ExtraArgs[kind],
		AccountPin:  accountPin(req, t.cfg, kind),
		Prompt:      promptText(prompt, issue, t.cfg),
		Ctx:         hctx,

		DetectionTimeout: time.Duration(t.cfg.Timeouts.DetectionMS) * time.Millisecond,
		PromptTimeout:    time.Duration(t.cfg.Timeouts.PromptWaitMS) * time.Millisecond,
		// Unwired, not merely unimplemented: herdr 0.8.2 (this plugin's
		// min_herdr_version) answers `unknown option: --trust-repository`
		// and fails worktree creation outright. See buildPlanInput's own
		// note; the two must be wired together or not at all.
		TrustRepository: false,
	}, prov, nil
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

// accountPin resolves the clauth pin the same way Model.accountPin does:
// only claude can be pinned (spec §6 field 7, and plan.Build refuses any
// other combination), and the configured `[clauth] default` applies only
// when clauth is enabled and names a real profile rather than the "active"
// sentinel.
//
// An explicit --account is passed through even for a non-claude kind,
// deliberately: plan.Build's own refusal names the rule, which is more
// useful than silently dropping the flag.
func accountPin(req request, cfg config.Config, kind string) string {
	if req.set["account"] {
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
// split-here need to know where "here" is, and a worktree needs neither
// because it always opens a new space (plan.Build's topologyOp checks
// UseWorktree before Placement). The message names the missing variable
// exactly, because "missing context" tells a script author nothing they
// can act on.
//
// tab-here asks for both the workspace and the tab id even though `herdr
// tab create --workspace` consumes only the first: herdr exports the three
// together for every pane, so an environment carrying one without the
// other is not a pane, and guessing a workspace for "here" is precisely
// what this check exists to prevent.
func requireContext(hctx herdrc.Context, in plan.Input) error {
	if in.UseWorktree {
		return nil
	}
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
	}
	return nil
}

// provenanceFlag and provenanceWorktree are the two provenance values
// spec §10's tier names cannot express: a value the caller gave outright,
// and the one value the plan itself decides (a worktree's placement).
const (
	provenanceFlag     = "flag"
	provenanceWorktree = "worktree"
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
