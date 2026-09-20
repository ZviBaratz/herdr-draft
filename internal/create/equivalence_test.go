package create

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ZviBaratz/herdr-draft/internal/agentopts"
	"github.com/ZviBaratz/herdr-draft/internal/app"
	"github.com/ZviBaratz/herdr-draft/internal/clauth"
	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/form"
	"github.com/ZviBaratz/herdr-draft/internal/gitx"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
	"github.com/ZviBaratz/herdr-draft/internal/linear"
	"github.com/ZviBaratz/herdr-draft/internal/picker"
	"github.com/ZviBaratz/herdr-draft/internal/plan"
	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// TestFormAndCommandProduceTheSamePlan is v2 spec §13's whole promise, stated
// as an assertion instead of a comment: "unset flags resolve through §10's
// resolver, so the command and the form produce the same session from the
// same inputs".
//
// It is a real double-drive, not a comparison of two calls into the same
// function. The command side goes through parseArgs and resolveRequest --
// the actual code `herdr-draft create` runs. The form side constructs a
// real app.Model over the SAME config.toml, last-used.json, projects.json,
// .herdr-draft.toml and plugin context, settles it by running the debounced
// dir/base checks its own Init schedules, types the title a user would
// type (or picks the issue that seeds it), and reads back the plan.Input
// its submit would build. The two plan.Inputs must be identical, field for
// field.
//
// That is the only test in this repository that can catch the failure this
// feature is most exposed to: a resolution rule the form applies AFTER the
// resolver (a worktree only where one is possible, a placement snapped
// back by a worktree, an agent kind falling through to the first favorite,
// a base ref remembered before the branch list naming it exists) being
// implemented once and not twice.
func TestFormAndCommandProduceTheSamePlan(t *testing.T) {
	const projectDir = "/projects/thing"
	const title = "fix login redirect loop"

	// One plugin context for both sides, so plan.Input.Ctx is comparable
	// too rather than being excluded from the comparison.
	contextJSON := `{"workspace_id":"wS0","workspace_cwd":"` + projectDir +
		`","tab_id":"tT0","focused_pane_id":"pP0"}`
	// The same pane in a lane's space instead: what herdr hands a popup
	// opened there, and what a `create` run there inherits (#171). herdr's
	// repo_root names the primary checkout; the lane is checkout_path.
	laneContextJSON := `{"workspace_id":"wS0","workspace_cwd":"` + laneDir +
		`","worktree":{"repo_key":"` + projectDir + `/.git","repo_name":"thing","repo_root":"` + projectDir +
		`","checkout_path":"` + laneDir + `","is_linked_worktree":true},"tab_id":"tT0","focused_pane_id":"pP0"}`

	for _, tc := range []struct {
		name string
		// configTOML, lastUsed, projects and repo are the four tiers under
		// the built-in default; each scenario shifts which one wins.
		configTOML string
		lastUsed   string
		projects   string
		repo       config.RepoConfig
		// workspaces is what `herdr workspace list` reports to both sides:
		// the tier (TierOpenWorkspace) that is a fact about the machine
		// rather than a file.
		workspaces []herdrc.WorkspaceInfo
		// title is what a user types into the title row, and what args
		// hands --title. "" is the short title every other scenario shares.
		title string
		// paste enters the title as one paste instead of keystrokes, which
		// is the only way a user's own text with a tab or a line break in
		// it reaches the title row: the keyboard's tab moves focus. (An
		// issue's title reaches it seeded, through SetTitle.)
		paste bool
		// lane runs the command, and opens the popup, in laneDir: a linked
		// worktree checkout of the project (#171) and the directory the
		// spawn skill's own agents run in. Both sides then take the lane as
		// the project, left unset.
		lane bool
		// issue is the Linear issue both sides can see. args names it with
		// --issue; the form is sent the message IssueField sends when a
		// user picks it, and nothing is typed, because the issue seeds the
		// title.
		issue *linear.Issue
		// args are the command's flags; the form is driven with the
		// equivalent user input (the title, or the issue) and nothing else.
		args []string
		// want is what the tiers above are supposed to resolve to. Equality
		// between the two sides is the point of the test, but two sides
		// that agreed on nothing in particular would satisfy it just as
		// well -- this is what keeps each scenario about a real tier
		// arrangement.
		want plan.Input
	}{
		{
			name: "worktree from the repository's committed default",
			configTOML: `
branch_prefix = "zvi/"
default_worktree = false
default_placement = "tab-here"
[agents]
favorites = ["claude", "codex"]
default = "claude"
[agents.extra_args]
codex = ["--full-auto"]
[timeouts]
detection_ms = 11000
prompt_wait_ms = 22000
`,
			lastUsed: `{"kind":"codex","placement":"split-here","worktree":false}`,
			projects: `{"version":1,"entries":{"` + projectDir +
				`":{"kind":"codex","worktree":true,"placement":"tab-here","base":"main","seen":"2026-09-01T00:00:00Z"}}}`,
			repo: config.RepoConfig{BranchPrefix: "team/"},
			args: []string{"--title", title},
			// projects.json wins the toggle, the kind and the base;
			// .herdr-draft.toml wins the prefix over config.toml. Its
			// remembered tab-here does NOT apply: a worktree session runs
			// in the worktree's own space (placement spec §14), so both
			// paths must turn a remembered placement into new-space, not
			// just one of them.
			want: plan.Input{
				Branch: "team/fix-login-redirect-loop", BaseRef: "main",
				UseWorktree: true, Placement: plan.PlacementNewSpace,
				AgentKind: "codex", ExtraArgs: []string{"--full-auto"},
				DetectionTimeout: 11 * time.Second, PromptTimeout: 22 * time.Second,
			},
		},
		{
			name: "no worktree, so the remembered placement stands",
			configTOML: `
branch_prefix = "zvi/"
default_worktree = true
[agents]
favorites = ["claude"]
[timeouts]
detection_ms = 30000
prompt_wait_ms = 120000
`,
			lastUsed: `{"kind":"claude","placement":"tab-here","worktree":true}`,
			repo:     config.RepoConfig{DefaultWorktree: boolp(false)},
			args:     []string{"--title", title},
			// The repository turns the worktree off over both tiers below
			// it, which is what leaves last-used.json's tab-here standing
			// as a real placement rather than being overridden.
			want: plan.Input{
				Branch:    "zvi/fix-login-redirect-loop",
				Placement: plan.PlacementTabHere, AgentKind: "claude",
				DetectionTimeout: 30 * time.Second, PromptTimeout: 120 * time.Second,
			},
		},
		{
			name: "nothing configured at all: the built-in defaults",
			configTOML: `
branch_prefix = "zvi/"
[agents]
favorites = ["claude"]
`,
			args: []string{"--title", title},
			// config.Load's own defaults: a worktree, and -- with no
			// [agents] default -- the first favorite, which AgentField
			// reaches by construction and the command reaches explicitly.
			want: plan.Input{
				Branch: "zvi/fix-login-redirect-loop", UseWorktree: true,
				Placement: plan.PlacementNewSpace, AgentKind: "claude",
				DetectionTimeout: 30 * time.Second, PromptTimeout: 120 * time.Second,
			},
		},
		{
			// `[clauth] launcher` has no flag on the command and no row in
			// the form either: config.toml is its only source, and both
			// paths must carry it into plan.Input or one config puts them
			// on different accounts.
			//
			// This scenario exists because BOTH halves of the feature were
			// pinned and the SEAM between them was not: internal/config
			// tests stop at cfg.Clauth.Launcher and internal/plan tests
			// inject Input.Launcher by hand, so replacing either
			// construction site with the built-in default left the key
			// entirely inert under a 14/14 green `go test ./...` -- a user
			// setting `launcher` would still get `clauth start`, which is
			// the mis-billing this key exists to remove. Found by
			// independent review, not by the suite.
			name: "the clauth launcher reaches both paths from config.toml",
			configTOML: `
branch_prefix = "zvi/"
[clauth]
launcher = ["claude-as", "{account}"]
`,
			args: []string{"--title", title},
			want: plan.Input{
				Branch: "zvi/fix-login-redirect-loop", UseWorktree: true,
				Placement: plan.PlacementNewSpace, AgentKind: "claude",
				Launcher:         []string{"claude-as", "{account}"},
				DetectionTimeout: 30 * time.Second, PromptTimeout: 120 * time.Second,
			},
		},
		{
			// `[worktree] trust_repository` has no flag on the command and
			// no row in the form: config.toml is its only source, so this
			// is the one scenario where the two paths agree by BOTH
			// reading the resolver rather than by one of them being told.
			// That makes it the case most likely to rot -- a future
			// `--trust-repository` flag, or a form row, would break the
			// equality here and nowhere else.
			name: "trust_repository comes off config.toml for both paths",
			configTOML: `
branch_prefix = "zvi/"
[agents]
favorites = ["claude"]
[worktree]
trust_repository = true
`,
			args: []string{"--title", title},
			want: plan.Input{
				Branch: "zvi/fix-login-redirect-loop", UseWorktree: true,
				Placement: plan.PlacementNewSpace, AgentKind: "claude",
				TrustRepository:  true,
				DetectionTimeout: 30 * time.Second, PromptTimeout: 120 * time.Second,
			},
		},
		{
			// [reaper] mark_ready has a form control (the prompt panel's
			// keep · reap chips) AND a flag, but with neither touched both
			// paths must land on config.toml's answer (reap spec §6.1).
			name: "mark_ready comes off config.toml for both paths",
			configTOML: `
branch_prefix = "zvi/"
[agents]
favorites = ["claude"]
[reaper]
mark_ready = true
`,
			args: []string{"--title", title},
			want: plan.Input{
				Branch: "zvi/fix-login-redirect-loop", UseWorktree: true,
				Placement: plan.PlacementNewSpace, AgentKind: "claude",
				MarkReady:        true,
				DetectionTimeout: 30 * time.Second, PromptTimeout: 120 * time.Second,
			},
		},
		{
			// The agent-options spec's config.toml tier (§6.1), with
			// extra_args beside it: both paths carry the options as
			// AgentOptions and extra_args exactly as configured -- the
			// displacement between them is plan.Build's, once, for both.
			name: "agent options come off config.toml for both paths",
			configTOML: `
branch_prefix = "zvi/"
[agents]
favorites = ["claude"]
[agents.extra_args]
claude = ["--verbose", "--model", "opus"]
[agents.options.claude]
model = "claude-opus-5[1m]"
effort = "xhigh"
`,
			args: []string{"--title", title},
			want: plan.Input{
				Branch: "zvi/fix-login-redirect-loop", UseWorktree: true,
				Placement: plan.PlacementNewSpace, AgentKind: "claude",
				ExtraArgs:        []string{"--verbose", "--model", "opus"},
				AgentOptions:     agentopts.Values{"model": "claude-opus-5[1m]", "effort": "xhigh"},
				DetectionTimeout: 30 * time.Second, PromptTimeout: 120 * time.Second,
			},
		},
		{
			// Options are per kind: claude's never reach a codex session,
			// on either path.
			name: "another kind's options do not ride along",
			configTOML: `
branch_prefix = "zvi/"
[agents]
favorites = ["claude", "codex"]
default = "codex"
[agents.options.claude]
effort = "low"
`,
			args: []string{"--title", title},
			want: plan.Input{
				Branch: "zvi/fix-login-redirect-loop", UseWorktree: true,
				Placement: plan.PlacementNewSpace, AgentKind: "codex",
				DetectionTimeout: 30 * time.Second, PromptTimeout: 120 * time.Second,
			},
		},
		{
			// #128's default, unchanged where it belongs: no worktree, and
			// the repository's own space is open, so the agent's tab goes
			// there.
			name: "no worktree and the repository's space is open: a tab in it",
			configTOML: `
branch_prefix = "zvi/"
default_worktree = false
[agents]
favorites = ["claude"]
`,
			workspaces: repoSpaceOpen(projectDir),
			args:       []string{"--title", title},
			want: plan.Input{
				Branch:    "zvi/fix-login-redirect-loop",
				Placement: plan.PlacementTabIn, Space: plan.Space{WorkspaceID: "wR", Label: "thing"},
				AgentKind:        "claude",
				DetectionTimeout: 30 * time.Second, PromptTimeout: 120 * time.Second,
			},
		},
		{
			// The defect the review measured (placement spec §14): with a
			// worktree, the open repository space used to default the
			// agent into a tab there and leave the worktree's own space
			// holding an idle shell. Both paths must now keep the agent in
			// the worktree's space.
			name: "a worktree and the repository's space is open: still its own space",
			configTOML: `
branch_prefix = "zvi/"
[agents]
favorites = ["claude"]
`,
			workspaces: repoSpaceOpen(projectDir),
			args:       []string{"--title", title},
			want: plan.Input{
				Branch: "zvi/fix-login-redirect-loop", UseWorktree: true,
				Placement: plan.PlacementNewSpace, Space: plan.Space{WorkspaceID: "wR", Label: "thing"},
				AgentKind:        "claude",
				DetectionTimeout: 30 * time.Second, PromptTimeout: 120 * time.Second,
			},
		},
		{
			// #171: the spawn skill's first example, run from a lane. herdr
			// refuses the lane as a worktree source, so both paths name the
			// repository root for that and cut the branch from the lane's
			// commit -- the popup because it now opens on the lane, as
			// `create` always did. This is the fixture that would have caught
			// it: before it, no scenario was invoked from a linked worktree.
			name: "from a lane: a worktree from the repository root, cut from the lane's commit",
			configTOML: `
branch_prefix = "zvi/"
[agents]
favorites = ["claude"]
`,
			lane: true,
			args: []string{"--title", title},
			want: plan.Input{
				Branch: "zvi/fix-login-redirect-loop", UseWorktree: true,
				Placement: plan.PlacementNewSpace, AgentKind: "claude",
				Linked:           plan.LinkedCheckout{RepoRoot: projectDir, Commit: laneHead},
				DetectionTimeout: 30 * time.Second, PromptTimeout: 120 * time.Second,
			},
		},
		{
			// A base remembered for the repository (projects.json keys on the
			// origin root, so the lane shares its entry) wins on both paths,
			// and both resolve it IN the lane: from the repository root a
			// per-checkout ref would name the primary checkout's commit.
			name: "from a lane, a remembered base is resolved in the lane",
			configTOML: `
branch_prefix = "zvi/"
[agents]
favorites = ["claude"]
`,
			projects: `{"version":1,"entries":{"` + projectDir +
				`":{"base":"main","seen":"2026-09-01T00:00:00Z"}}}`,
			lane: true,
			args: []string{"--title", title},
			want: plan.Input{
				Branch: "zvi/fix-login-redirect-loop", BaseRef: "main", UseWorktree: true,
				Placement: plan.PlacementNewSpace, AgentKind: "claude",
				Linked:           plan.LinkedCheckout{RepoRoot: projectDir, Commit: laneMain},
				DetectionTimeout: 30 * time.Second, PromptTimeout: 120 * time.Second,
			},
		},
		{
			// #171's second decision: without a worktree the session is the
			// lane's, in the lane's own checkout. With its space open, #128's
			// default puts the tab there -- not in the repository's space.
			name: "from a lane with no worktree: a tab in the lane's own space",
			configTOML: `
branch_prefix = "zvi/"
default_worktree = false
[agents]
favorites = ["claude"]
`,
			lane: true,
			workspaces: []herdrc.WorkspaceInfo{{WorkspaceID: "wL", Label: "thing-lane", Worktree: &herdrc.ContextWorktree{
				RepoRoot: projectDir, CheckoutPath: laneDir, RepoName: "thing", IsLinkedWorktree: true}}},
			args: []string{"--title", title},
			want: plan.Input{
				Branch:    "zvi/fix-login-redirect-loop",
				Placement: plan.PlacementTabIn, Space: plan.Space{WorkspaceID: "wL", Label: "thing-lane"},
				AgentKind:        "claude",
				DetectionTimeout: 30 * time.Second, PromptTimeout: 120 * time.Second,
			},
		},
		// #194: a base a tier supplies and the form's branch list does not
		// name. The form's picker held one only if its list named it, so
		// every row below except the last two used to build two different
		// plans: `create` passed the ref on as it was, and the form fell
		// back to its HEAD row without a word. The rule is now the same on
		// both paths -- keep a base that resolves, map HEAD and @ to the
		// HEAD row, and fall back to HEAD from one that does not. The rule
		// is the same, the note that says so differs: stderr for `create`
		// (TestTierBase*), the worktree panel for the form.
		{
			name:       "a remembered HEAD is the HEAD row",
			configTOML: baseScenarioConfig,
			projects:   rememberedBase(projectDir, "HEAD"),
			args:       []string{"--title", title},
			want:       baseScenarioWant(""),
		},
		{
			name:       "the repository's default_base HEAD is the HEAD row",
			configTOML: baseScenarioConfig,
			repo:       config.RepoConfig{DefaultBase: "HEAD"},
			args:       []string{"--title", title},
			want:       baseScenarioWant(""),
		},
		{
			name:       "a remembered @ is the HEAD row",
			configTOML: baseScenarioConfig,
			projects:   rememberedBase(projectDir, "@"),
			args:       []string{"--title", title},
			want:       baseScenarioWant(""),
		},
		{
			name:       "a remembered branch that was deleted falls back to HEAD",
			configTOML: baseScenarioConfig,
			projects:   rememberedBase(projectDir, "gone-branch"),
			args:       []string{"--title", title},
			want:       baseScenarioWant(""),
		},
		{
			// formGit lists main and dev only, which is what an existing
			// branch older than the 50 newest looks like to the picker.
			name:       "a remembered branch the list does not name is kept",
			configTOML: baseScenarioConfig,
			projects:   rememberedBase(projectDir, "old-branch"),
			args:       []string{"--title", title},
			want:       baseScenarioWant("old-branch"),
		},
		{
			// #198: the list offers a branch only a remote has by the name
			// git resolves, and projects.json then remembers that name. It
			// needs no special case on either path: it names a commit, so
			// it is kept as written.
			name:       "a remembered origin/develop, a branch only a remote has, is kept",
			configTOML: baseScenarioConfig,
			projects:   rememberedBase(projectDir, "origin/develop"),
			args:       []string{"--title", title},
			want:       baseScenarioWant("origin/develop"),
		},
		{
			// Another spelling of HEAD itself is the HEAD row too, found by
			// git. The form used to drop it to "", and while #193's hole was
			// open -- the clean gate counting from it inside the new worktree,
			// where it names the worktree -- keeping it under rule 1 would
			// have let the popup reach that hole.
			name:       "a remembered HEAD^0 names HEAD's commit, so it is the HEAD row",
			configTOML: baseScenarioConfig,
			projects:   rememberedBase(projectDir, "HEAD^0"),
			args:       []string{"--title", title},
			want:       baseScenarioWant(""),
		},
		{
			// A per-checkout ref that is another commit resolves, so rule 1
			// keeps it as written, and the clean gate counts from the commit
			// it names (#193).
			name:       "a remembered HEAD~1 resolves, so it is kept",
			configTOML: baseScenarioConfig,
			projects:   rememberedBase(projectDir, "HEAD~1"),
			args:       []string{"--title", title},
			want:       baseScenarioWant("HEAD~1"),
		},
		{
			// The lane gets the same fall-back rather than #171's refusal: the
			// base is checked in the project, which is the lane, and from HEAD
			// it is cut from the lane's commit as an unset base always was.
			name:       "from a lane, a remembered branch that was deleted falls back to the lane's HEAD",
			configTOML: baseScenarioConfig,
			projects:   rememberedBase(projectDir, "gone-branch"),
			lane:       true,
			args:       []string{"--title", title},
			want: plan.Input{
				Branch: "zvi/fix-login-redirect-loop", UseWorktree: true,
				Placement: plan.PlacementNewSpace, AgentKind: "claude",
				Linked:           plan.LinkedCheckout{RepoRoot: projectDir, Commit: laneHead},
				DetectionTimeout: 30 * time.Second, PromptTimeout: 120 * time.Second,
			},
		},
		{
			// #176: the title row holds 32 runes (spec §6 field 3) and
			// --title used to hold anything, so one long title built two
			// sessions. The title labels the space and the tab, and here the
			// branch differs as well, because it is derived from the title.
			// (It names the agent too, but plan.AgentName's 30-rune clamp
			// hides the cut for this title.)
			name: "a --title over 32 runes is cut as the form cuts it",
			configTOML: `
branch_prefix = "zvi/"
[agents]
favorites = ["claude"]
`,
			title: "fix login redirect loop when the cookie expires",
			args:  []string{"--title", "fix login redirect loop when the cookie expires"},
			want: plan.Input{
				Title:  "fix login redirect loop when the",
				Branch: "zvi/fix-login-redirect-loop-when-the", UseWorktree: true,
				Placement: plan.PlacementNewSpace, AgentKind: "claude",
				DetectionTimeout: 30 * time.Second, PromptTimeout: 120 * time.Second,
			},
		},
		{
			// #176's other half: an issue's title is cut on the way into
			// the title row too. The branch agrees either way, since both
			// paths take the issue's own, so the title is all that
			// diverged.
			//
			// The é puts a two-byte rune before the cut, and rune 32 falls
			// inside "when": a cut by bytes, or back to a word boundary,
			// lands somewhere the form's does not.
			name: "an issue title over 32 runes is cut as the form cuts it",
			configTOML: `
branch_prefix = "zvi/"
[agents]
favorites = ["claude"]
`,
			issue: &linear.Issue{
				Identifier: "ENG-42", Title: "Fix café login redirect loop when the cookie expires",
				BranchName: "zvi/eng-42-fix-cafe-login-redirect-loop",
				URL:        "https://linear.app/x/ENG-42", Description: "it loops",
			},
			args: []string{"--issue", "ENG-42"},
			// The prompt quotes the issue's title whole: the cap is the
			// title row's, and the template is not rendered into it.
			want: plan.Input{
				Title:  "Fix café login redirect loop whe",
				Branch: "zvi/eng-42-fix-cafe-login-redirect-loop", UseWorktree: true,
				Placement: plan.PlacementNewSpace, AgentKind: "claude",
				Prompt:           "Work on ENG-42: Fix café login redirect loop when the cookie expires\n\nhttps://linear.app/x/ENG-42\n\nit loops",
				DetectionTimeout: 30 * time.Second, PromptTimeout: 120 * time.Second,
			},
		},
		{
			// #178: the title row turns a tab and a line break into a space
			// and drops any other control character, and `create` kept them
			// all. Here the dropped BEL also changes the branch, which is
			// derived from the title: "abell", not "a-bell".
			name: "a --title with a tab, a line break and a BEL is cleaned as the form cleans it",
			configTOML: `
branch_prefix = "zvi/"
[agents]
favorites = ["claude"]
`,
			title: "line one\nline two\twith a\abell",
			paste: true,
			args:  []string{"--title", "line one\nline two\twith a\abell"},
			want: plan.Input{
				Title:  "line one line two with abell",
				Branch: "zvi/line-one-line-two-with-abell", UseWorktree: true,
				Placement: plan.PlacementNewSpace, AgentKind: "claude",
				DetectionTimeout: 30 * time.Second, PromptTimeout: 120 * time.Second,
			},
		},
		{
			// The same rule for an issue's title, and its order against
			// #176's cut: clean first, then cut, as the row does. Cutting
			// the raw title first would end at "...when th", one rune
			// short, because the BEL counted.
			//
			// The template leaves the title out of the prompt on purpose.
			// The form's prompt is a textarea with a sanitizer of its own
			// (a tab becomes four spaces), so a title with a tab in the
			// prompt would compare that drift too, and it is not this one.
			name: "an issue title with control characters is cleaned, then cut, as the form does",
			configTOML: `
branch_prefix = "zvi/"
[agents]
favorites = ["claude"]
[linear]
prompt_template = "Work on {identifier}"
`,
			issue: &linear.Issue{
				Identifier: "ENG-43", Title: "Fix\a login\tredirect loop when the cookie expires",
				BranchName: "zvi/eng-43-fix-login-redirect-loop",
			},
			args: []string{"--issue", "ENG-43"},
			want: plan.Input{
				Title:  "Fix login redirect loop when the",
				Branch: "zvi/eng-43-fix-login-redirect-loop", UseWorktree: true,
				Placement: plan.PlacementNewSpace, AgentKind: "claude",
				Prompt:           "Work on ENG-43",
				DetectionTimeout: 30 * time.Second, PromptTimeout: 120 * time.Second,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configDir, stateDir := t.TempDir(), t.TempDir()
			writeConfig(t, configDir, tc.configTOML)
			if tc.lastUsed != "" {
				writeFile(t, filepath.Join(stateDir, "last-used.json"), tc.lastUsed)
			}
			if tc.projects != "" {
				writeFile(t, filepath.Join(stateDir, "projects.json"), tc.projects)
			}
			repoConfig := func(string) config.RepoConfig { return tc.repo }

			typed := cmp.Or(tc.title, title)
			if tc.issue != nil {
				typed = ""
			}
			invokedIn, ctxJSON := projectDir, contextJSON
			if tc.lane {
				invokedIn, ctxJSON = laneDir, laneContextJSON
			}

			fromCommand := commandPlanInput(t, commandCase{
				configDir:   configDir,
				stateDir:    stateDir,
				contextJSON: ctxJSON,
				projectDir:  invokedIn,
				lane:        tc.lane,
				repoConfig:  repoConfig,
				workspaces:  tc.workspaces,
				issue:       tc.issue,
				args:        tc.args,
			})
			fromForm := formPlanInput(t, formCase{
				configDir:   configDir,
				stateDir:    stateDir,
				contextJSON: ctxJSON,
				lane:        tc.lane,
				repoConfig:  repoConfig,
				workspaces:  tc.workspaces,
				title:       typed,
				paste:       tc.paste,
				issue:       tc.issue,
			})

			if !reflect.DeepEqual(fromCommand, fromForm) {
				t.Fatalf("the command and the form disagree.\ncommand: %s\nform:    %s",
					showInput(fromCommand), showInput(fromForm))
			}

			// ... and both agree on the RIGHT thing.
			want := tc.want
			want.ProjectDir, want.IsGitRepo = invokedIn, true
			if want.Title == "" {
				want.Title = title
			}
			want.Ctx = mustContext(t, ctxJSON)
			// No scenario configures a [clauth] launcher, so both paths
			// resolve the built-in one. Filled here rather than repeated in
			// every literal, and only when the scenario left it unset, so a
			// future scenario can still pin its own.
			if want.Launcher == nil {
				want.Launcher = config.DefaultClauthLauncher()
			}
			if !reflect.DeepEqual(fromCommand, want) {
				t.Fatalf("the tiers did not resolve as the scenario describes.\ngot:  %s\nwant: %s",
					showInput(fromCommand), showInput(want))
			}
		})
	}
}

// commandCase/commandPlanInput drive the real command path up to (not
// including) plan.Build.
type commandCase struct {
	configDir, stateDir string
	contextJSON         string
	projectDir          string
	// lane makes laneDir a linked checkout of laneRoot at laneHead, as
	// formCase.lane does for the form's git.
	lane       bool
	repoConfig func(string) config.RepoConfig
	workspaces []herdrc.WorkspaceInfo
	// issue is the one assigned Linear issue --issue can find, or nil for
	// no Linear at all.
	issue  *linear.Issue
	args   []string
	picker picker.Source
}

func commandPlanInput(t *testing.T, c commandCase) plan.Input {
	t.Helper()
	resolved, err := commandResolve(t, c)
	if err != nil {
		t.Fatalf("resolveRequest: %v", err)
	}
	// run() resolves an `auto` account after the rest of the pre-flight
	// (#145), so the comparison has to take that step too -- the command's
	// counterpart of formPlanInputAuto's ResolveAccount. Without it the
	// command side would hand back the sentinel and the auto test would be
	// comparing the pick against nothing.
	in, _, err := resolveAccount(context.Background(), resolved.input, accountPicker(resolved.tiers.cfg, Deps{Picker: c.picker}), false)
	if err != nil {
		t.Fatalf("resolveAccount: %v", err)
	}
	return in
}

// commandResolve is commandPlanInput's first half, the command's own
// resolveRequest, with its refusal handed back rather than fatal: a request
// the command refuses is what TestFormAndCommandRefuseTheSameBranches
// compares.
func commandResolve(t *testing.T, c commandCase) (resolution, error) {
	t.Helper()
	t.Setenv("LINEAR_API_KEY", "")

	req, err := parseArgs(c.args)
	if err != nil {
		t.Fatalf("parseArgs(%v): %v", c.args, err)
	}
	runner := newFakeRunner()
	runner.workspaces = c.workspaces
	git := newFakeGit()
	git.commits = repoCommitsIn(c.projectDir)
	if c.lane {
		git.roots = map[string]string{laneDir: laneRoot}
		git.primary = map[string]string{laneDir: laneRoot}
		git.commits = map[string]string{laneDir + " HEAD": laneHead, laneDir + " main": laneMain}
	}
	// Assigned only when there is an issue: a nil *fakeLinear in the
	// interface would read as Linear configured.
	var issues IssueSource
	if c.issue != nil {
		issues = &fakeLinear{issues: []linear.Issue{*c.issue}}
	}
	return resolveRequest(context.Background(), req, Env{
		ConfigDir:   c.configDir,
		StateDir:    c.stateDir,
		ContextJSON: c.contextJSON,
		// Without the id these three are not demonstrably ours and
		// usablePluginEnv drops all of them (#91), which would make this
		// test compare a form resolving from four tiers against a command
		// resolving from none -- and it did, silently, until the guard
		// learned that absent is not the same as matching.
		PluginID: pluginID,
	}, Deps{
		Runner:     runner,
		Git:        git,
		Linear:     issues,
		RepoConfig: c.repoConfig,
		Stdin:      strings.NewReader(""),
		Stdout:     &strings.Builder{},
		Stderr:     &strings.Builder{},
		Workdir:    func() (string, error) { return c.projectDir, nil },
		Picker:     c.picker,
	})
}

// formCase/formPlanInput build a real app.Model over the same tiers, let
// it settle, type the title, and read back what its submit would build.
// The form side is deliberately driven with the ONE input a user gives
// that this comparison needs -- the title, or the issue that seeds it --
// and nothing else. Every scenario above therefore differs only in its
// configuration tiers, which is precisely the claim under test ("unset
// flags resolve through the resolver"). Driving the form's chip rows and
// pickers by keystroke would couple this test to internal/form's key
// grammar, which is being rewritten under a separate issue; the
// flags-beat-the-tiers half is covered on the command side alone, by
// TestFlagsBeatEveryTier.
type formCase struct {
	configDir, stateDir string
	contextJSON         string
	// lane is commandCase.lane, for the form's git.
	lane       bool
	repoConfig func(string) config.RepoConfig
	workspaces []herdrc.WorkspaceInfo
	title      string
	// paste sends title as one tea.PasteMsg instead of keystrokes.
	paste bool
	// issue, when set, is offered by a Linear source and then chosen, as
	// commandCase.issue is found by --issue.
	issue *linear.Issue
	// landChecks runs what choosing the issue schedules -- the debounced
	// title and branch check among it -- instead of dropping it, so that a
	// submit sent afterwards is not held for a check that will never land.
	// Only a scenario that submits needs it.
	landChecks bool
	picker     picker.Source
	// clauthStatus is what makes the account row exist at all (app.New's own
	// ">= 2 profiles" gate). The command path never consults clauth, so this
	// has no counterpart on the other side -- it is scaffolding for the row,
	// not a tier under comparison.
	clauthStatus clauth.Status
}

func formPlanInput(t *testing.T, c formCase) plan.Input {
	t.Helper()
	return withLinkedCommit(t, formModel(t, c)).PlanInput()
}

// withLinkedCommit takes the first step a submit takes after validation:
// resolving the base in a lane when the plan needs it (#171). The command
// side takes its counterpart inside resolveRequest. For every other scenario
// it is a no-op, which is itself worth running: a form that resolved a
// commit it did not need would show up here as a Linked the command does not
// have.
func withLinkedCommit(t *testing.T, m app.Model) app.Model {
	t.Helper()
	commit, err := m.ResolveLinkedCommit(context.Background())
	if err != nil {
		t.Fatalf("ResolveLinkedCommit: %v", err)
	}
	return m.WithLinkedCommit(commit)
}

// formModel is formPlanInput's first half: a real, settled app.Model with the
// title typed. Split out so a scenario that has to do something MORE with the
// model before reading its plan.Input -- resolving an `auto` account, which
// the submit pipeline does between validation and plan.Build -- can, without
// every other scenario growing a step it does not use.
func formModel(t *testing.T, c formCase) app.Model {
	t.Helper()

	cfg, err := config.Load(c.configDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	state, _ := config.LoadState(c.stateDir)
	projects, _ := config.LoadProjects(c.stateDir)
	hctx, err := herdrc.ParseContext(c.contextJSON)
	if err != nil {
		t.Fatalf("ParseContext: %v", err)
	}

	deps := app.Deps{
		Runner:     newFakeRunner(),
		Git:        &formGit{lane: c.lane},
		Clock:      app.Clock{Sleep: func(time.Duration) {}},
		RepoConfig: c.repoConfig,
		Picker:     c.picker,
		// Non-nil purely to satisfy app.New's construction gate; nothing
		// in this test focuses the account row, which is the only thing
		// that would ever call it.
		Clauth: app.NewClauthSource(clauth.LoadOpts{}),
	}
	if c.issue != nil {
		deps.Linear = &fakeLinear{issues: []linear.Issue{*c.issue}}
	}

	m := app.New(app.Setup{
		Deps:         deps,
		Ctx:          hctx,
		Workspaces:   c.workspaces,
		Config:       cfg,
		State:        state,
		Projects:     projects,
		Palette:      theme.Default(),
		StateDir:     c.stateDir,
		ClauthStatus: c.clauthStatus,
	})

	// A real terminal size first: several fields only lay out (and the
	// pickers only page) once they have one.
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	// Then the form's own startup work -- the debounced directory check
	// that resolves the project's repository root, reads its
	// .herdr-draft.toml and applies v2 spec §10's per-project memory, and the
	// base-branch listing the remembered base needs in order to land.
	m = pump(t, m, m.Init())
	// A chosen issue arrives as the message IssueField sends on a pick,
	// not by keystroke -- the same line against the key grammar that
	// formCase draws. What it schedules is dropped for the reason given
	// below: the seeding plan.Input reads is synchronous.
	if c.issue != nil {
		next, cmd := m.Update(form.IssueChosenMsg{Issue: c.issue})
		m = next.(app.Model)
		if c.landChecks {
			m = pump(t, m, cmd)
		}
	}
	// Then the one thing a user types. The commands a keystroke returns
	// are deliberately dropped rather than pumped: they are the cursor's
	// blink and the debounced title-duplicate check, and neither touches
	// any field plan.Input reads (the branch derivation a title change
	// drives is synchronous, inside reactToChanges). Pumping them would
	// mean waiting out one blocked blink command per character.
	if c.paste {
		return send(m, tea.PasteMsg{Content: c.title})
	}
	for _, r := range c.title {
		m = send(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return m
}

// send routes one message and discards whatever it scheduled.
func send(m app.Model, msg tea.Msg) app.Model {
	next, _ := m.Update(msg)
	return next.(app.Model)
}

// pump runs a tea.Cmd tree the way bubbletea's own loop does -- each
// command on its own goroutine, tea.Batch unwrapped, every resulting
// message fed back through Update on this one goroutine -- until the
// commands stop producing messages. It is what lets a test outside package
// app drive the debounced async chain (schedule -> debounce -> result)
// through the model's public Update alone.
//
// It ends on an idle window rather than on a count of outstanding
// commands, because one command genuinely never returns: the text
// cursor's blink parks on a channel until the next blink, which in a real
// program is exactly what it should do. Every command this test cares
// about completes in microseconds against fakes, so an idle window three
// orders of magnitude longer than that is a safe stopping point.
func pump(t *testing.T, m app.Model, cmd tea.Cmd) app.Model {
	t.Helper()
	const idle = 100 * time.Millisecond

	msgs := make(chan tea.Msg, 256)
	outstanding := 0
	start := func(c tea.Cmd) {
		if c == nil {
			return
		}
		outstanding++
		go func() {
			if msg := c(); msg != nil {
				msgs <- msg
			}
		}()
	}

	start(cmd)
	for steps := 0; outstanding > 0; steps++ {
		if steps > 500 {
			t.Fatal("the form never settled: still scheduling work after 500 messages")
		}
		select {
		case msg := <-msgs:
			outstanding--
			if batch, ok := msg.(tea.BatchMsg); ok {
				for _, c := range batch {
					start(c)
				}
				continue
			}
			updated, next := m.Update(msg)
			m = updated.(app.Model)
			start(next)
		case <-time.After(idle):
			return m
		}
	}
	return m
}

// TestFlagsBeatEveryTier is the other half of v2 spec §13's precedence: an
// explicitly given flag wins over every configured and remembered value,
// and says so in --json's provenance. It runs on the command side alone --
// see formCase's own comment.
func TestFlagsBeatEveryTier(t *testing.T) {
	configDir, stateDir := t.TempDir(), t.TempDir()
	writeConfig(t, configDir, `
branch_prefix = "zvi/"
default_worktree = true
default_placement = "new-space"
[agents]
favorites = ["claude", "codex"]
`)
	writeFile(t, filepath.Join(stateDir, "last-used.json"),
		`{"kind":"claude","placement":"new-space","worktree":true}`)

	in := commandPlanInput(t, commandCase{
		configDir:   configDir,
		stateDir:    stateDir,
		contextJSON: `{"workspace_id":"wS0","tab_id":"tT0","focused_pane_id":"pP0"}`,
		projectDir:  "/projects/thing",
		repoConfig:  func(string) config.RepoConfig { return config.RepoConfig{DefaultBase: "release"} },
		args: []string{
			"--title", "t", "--no-worktree", "--placement", "split-here",
			"--agent", "codex", "--base", "main", "--branch", "explicit-branch",
		},
	})

	if in.UseWorktree {
		t.Errorf("UseWorktree = true, want --no-worktree to win over default_worktree")
	}
	if in.Placement != plan.PlacementSplitHere {
		t.Errorf("Placement = %v, want --placement split-here to win", in.Placement)
	}
	if in.AgentKind != "codex" {
		t.Errorf("AgentKind = %q, want --agent codex to win over last-used.json", in.AgentKind)
	}
	if in.BaseRef != "main" {
		t.Errorf("BaseRef = %q, want --base main to win over the repo's default_base", in.BaseRef)
	}
	if in.Branch != "explicit-branch" {
		t.Errorf("Branch = %q, want --branch to win over the title derivation", in.Branch)
	}
}

// formGit is app's own gitSource for these tests: an existing repository
// whose root is itself, with two branches -- and, with lane set, laneDir a
// linked checkout of laneRoot at laneHead, as commandCase.lane makes it for
// the command.
type formGit struct{ lane bool }

func (formGit) DirExists(string) bool { return true }
func (formGit) IsGitRepo(string) bool { return true }
func (g formGit) RepoRoot(_ context.Context, dir string) (string, error) {
	if g.lane && dir == laneDir {
		return laneRoot, nil
	}
	return dir, nil
}
func (g formGit) PrimaryCheckout(_ context.Context, dir string) (string, error) {
	if g.lane && dir == laneDir {
		return laneRoot, nil
	}
	return "", nil
}

// ResolveCommit answers in the checkout the project is, as commandCase's
// fake does: in the lane when there is one, and in the repository only when
// there is not, so a commit read in any other checkout -- the primary's HEAD
// from a lane, above all -- is an error rather than an answer that happens
// to match.
func (g formGit) ResolveCommit(_ context.Context, dir, ref string) (string, error) {
	if g.lane && dir == laneDir {
		switch ref {
		case "HEAD":
			return laneHead, nil
		case "main":
			return laneMain, nil
		}
	}
	if c, ok := repoCommits[ref]; ok && !g.lane && dir == laneRoot {
		return c, nil
	}
	return "", fmt.Errorf("formGit: no commit for %q in %s", ref, dir)
}
func (formGit) ListSubdirs(string, int) []string { return nil }
func (formGit) ResolvePath(p string) string      { return p }
func (formGit) ListBranches(context.Context, string, int) ([]string, error) {
	return []string{"main", "dev"}, nil
}
func (formGit) BranchExists(context.Context, string, string) (bool, error) { return false, nil }
func (formGit) CurrentBranch(context.Context, string) (string, error)      { return "main", nil }
func (formGit) FetchPrune(context.Context, string) error                   { return nil }

func boolp(b bool) *bool { return &b }

// baseScenarioConfig is the configuration #194's rows share: nothing that
// touches the base, so the tier each row names is the only one that does.
const baseScenarioConfig = `
branch_prefix = "zvi/"
[agents]
favorites = ["claude"]
`

// rememberedBase is a projects.json remembering base for projectDir and
// nothing else.
func rememberedBase(projectDir, base string) string {
	return `{"version":1,"entries":{"` + projectDir + `":{"base":"` + base + `","seen":"2026-09-01T00:00:00Z"}}}`
}

// baseScenarioWant is what baseScenarioConfig resolves to with base as the
// plan's base.
func baseScenarioWant(base string) plan.Input {
	return plan.Input{
		Branch: "zvi/fix-login-redirect-loop", BaseRef: base, UseWorktree: true,
		Placement: plan.PlacementNewSpace, AgentKind: "claude",
		DetectionTimeout: 30 * time.Second, PromptTimeout: 120 * time.Second,
	}
}

// repoCommits is what ResolveCommit answers in the project's own checkout,
// laneRoot, for both sides' fakes, keyed by ref: formGit's two listed
// branches, a branch older than any list the picker is given, and the
// per-checkout refs a tier can name. gone-branch is deliberately absent.
//
// HEAD and @ resolve, as they do in git, so the rows naming them are about
// the mapping to the HEAD row: were they missing here, those rows would reach
// HEAD by falling back instead, and pass without it.
var repoCommits = map[string]string{
	"HEAD":       "0a1b2c3d4e5f60718293a4b5c6d7e8f901234567",
	"@":          "0a1b2c3d4e5f60718293a4b5c6d7e8f901234567",
	"HEAD^0":     "0a1b2c3d4e5f60718293a4b5c6d7e8f901234567",
	"HEAD~1":     "1b2c3d4e5f60718293a4b5c6d7e8f9012345678a",
	"main":       "0a1b2c3d4e5f60718293a4b5c6d7e8f901234567",
	"dev":        "2c3d4e5f60718293a4b5c6d7e8f9012345678ab1",
	"old-branch": "3d4e5f60718293a4b5c6d7e8f9012345678ab12c",
	// A remote-tracking branch: only its qualified name resolves, and the
	// bare develop is deliberately absent, as it is from a fresh clone.
	"origin/develop": "4e5f60718293a4b5c6d7e8f9012345678ab12c3d",
}

// repoCommitsIn is repoCommits keyed as fakeGit.commits is, "<dir> <ref>".
func repoCommitsIn(dir string) map[string]string {
	out := make(map[string]string, len(repoCommits))
	for ref, commit := range repoCommits {
		out[dir+" "+ref] = commit
	}
	return out
}

// repoSpaceOpen is a `herdr workspace list` in which the project's primary
// checkout already has a space herdr has marked as the repository's own --
// the only kind plan.FindSpace can see.
func repoSpaceOpen(projectDir string) []herdrc.WorkspaceInfo {
	return []herdrc.WorkspaceInfo{{WorkspaceID: "wR", Label: "thing", Worktree: &herdrc.ContextWorktree{
		RepoRoot: projectDir, CheckoutPath: projectDir, RepoName: "thing"}}}
}

func mustContext(t *testing.T, raw string) herdrc.Context {
	t.Helper()
	ctx, err := herdrc.ParseContext(raw)
	if err != nil {
		t.Fatalf("ParseContext: %v", err)
	}
	return ctx
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// showInput renders a plan.Input for a failure message: JSON, because the
// struct is wide and a field-by-field diff of two %+v lines is unreadable.
func showInput(in plan.Input) string {
	b, err := json.Marshal(in)
	if err != nil {
		return "<unencodable>"
	}
	return string(b)
}

// TestFormAndCommandRefuseTheSameBranches is #199's half of v2 spec §13's
// promise. A refusal is not a plan.Input field, so the table above cannot
// see the two paths disagree about one; this drives both to the point of
// creating a session and compares which branches each refuses.
//
// The branch is the Linear issue's own: the one source of a name that
// reaches both paths through the same rule (app.BranchFor) with nothing
// typed. The command must refuse in resolveRequest, before anything else is
// asked. The form is submitted, and must stay on the form with the worktree
// panel saying why -- in the same words, since both ask one function. The
// last name is a control: neither refuses it, and both build one plan.Input.
func TestFormAndCommandRefuseTheSameBranches(t *testing.T) {
	const projectDir = "/projects/thing"
	contextJSON := `{"workspace_id":"wS0","workspace_cwd":"` + projectDir +
		`","tab_id":"tT0","focused_pane_id":"pP0"}`
	repoConfig := func(string) config.RepoConfig { return config.RepoConfig{} }

	for _, tc := range []struct {
		branch  string
		refused bool
	}{
		{"zvi/old ", true},
		{" zvi/old", true},
		{"zvi/a..b", true},
		{"zvi/a~1", true},
		{"zvi/a:b", true},
		{"zvi/a b", true},
		{"zvi/old.lock", true},
		{"zvi/old" + string(rune(0xa0)), true},
		{"zvi/lin-42-fix-login", false},
	} {
		t.Run(tc.branch, func(t *testing.T) {
			configDir, stateDir := t.TempDir(), t.TempDir()
			writeConfig(t, configDir, "default_worktree = true\n[agents]\nfavorites = [\"claude\"]\n")
			issue := &linear.Issue{Identifier: "LIN-42", Title: "Fix login redirect loop", BranchName: tc.branch}
			command := commandCase{
				configDir: configDir, stateDir: stateDir, contextJSON: contextJSON,
				projectDir: projectDir, repoConfig: repoConfig, issue: issue,
				args: []string{"--issue", "LIN-42"},
			}

			_, commandErr := commandResolve(t, command)

			m := formModel(t, formCase{
				configDir: configDir, stateDir: stateDir, contextJSON: contextJSON,
				repoConfig: repoConfig, issue: issue, landChecks: true,
			})
			if got := m.PlanInput(); !got.UseWorktree || got.Branch != tc.branch {
				t.Fatalf("test setup: the form holds worktree %v, branch %q; want the issue's branch, with a worktree", got.UseWorktree, got.Branch)
			}
			fromForm := withLinkedCommit(t, m).PlanInput()
			next, _ := m.Update(form.SubmitMsg{})
			view := ansi.Strip(next.(app.Model).View().Content)
			formRefused := strings.Contains(view, "invalid branch name")

			if (commandErr != nil) != tc.refused || formRefused != tc.refused {
				t.Fatalf("want refused = %v from both; the command's error is %v, and the form's view after submit is:\n%s",
					tc.refused, commandErr, view)
			}
			if tc.refused {
				reason := gitx.ValidateBranchName(tc.branch).Error()
				if !strings.Contains(commandErr.Error(), reason) || !strings.Contains(view, reason) {
					t.Errorf("both should give the reason %q\ncommand: %v\nform:\n%s", reason, commandErr, view)
				}
				return
			}
			if fromCommand := commandPlanInput(t, command); !reflect.DeepEqual(fromCommand, fromForm) {
				t.Fatalf("the command and the form disagree.\ncommand: %s\nform:    %s",
					showInput(fromCommand), showInput(fromForm))
			}
		})
	}
}

// TestFormAndCommandRefuseAnEmptyBranch is TestFormAndCommandRefuseTheSameBranches
// for the one name no issue can supply: an empty one, which BranchFor never
// derives. The command is given --branch "". The form is driven by keystroke
// -- ⇥ ⇥ ⇥ from the title to the worktree row, ↓ to the branch input, and
// one backspace per character -- because clearing the input is the only way
// a person reaches an empty branch. That couples to the key grammar, for
// TestFormAndCommandMarkReadyTheSameWay's reason: the claim under test is
// that the control a person would use is refused.
func TestFormAndCommandRefuseAnEmptyBranch(t *testing.T) {
	const projectDir = "/projects/thing"
	const title = "fix login redirect loop"
	contextJSON := `{"workspace_id":"wS0","workspace_cwd":"` + projectDir +
		`","tab_id":"tT0","focused_pane_id":"pP0"}`
	repoConfig := func(string) config.RepoConfig { return config.RepoConfig{} }
	configDir, stateDir := t.TempDir(), t.TempDir()
	writeConfig(t, configDir, "branch_prefix = \"zvi/\"\ndefault_worktree = true\n[agents]\nfavorites = [\"claude\"]\n")

	_, commandErr := commandResolve(t, commandCase{
		configDir: configDir, stateDir: stateDir, contextJSON: contextJSON,
		projectDir: projectDir, repoConfig: repoConfig,
		args: []string{"--title", title, "--branch", ""},
	})
	if commandErr == nil || !strings.Contains(commandErr.Error(), "--branch is empty") {
		t.Fatalf("the command's error is %v, want it to refuse the empty --branch", commandErr)
	}

	m := formModel(t, formCase{
		configDir: configDir, stateDir: stateDir, contextJSON: contextJSON,
		repoConfig: repoConfig, title: title,
	})
	derived := m.PlanInput().Branch
	if derived == "" || !m.PlanInput().UseWorktree {
		t.Fatalf("test setup: want a worktree and a derived branch, got worktree %v, branch %q", m.PlanInput().UseWorktree, derived)
	}
	for range 3 {
		m = send(m, tea.KeyPressMsg{Code: tea.KeyTab})
	}
	m = send(m, tea.KeyPressMsg{Code: tea.KeyDown})
	var cmd tea.Cmd
	for range derived {
		var next tea.Model
		next, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
		m = next.(app.Model)
	}
	if got := m.PlanInput().Branch; got != "" {
		t.Fatalf("test setup: the branch input holds %q after the backspaces, want it empty", got)
	}
	// The last edit's checks: a submit waits for the check of the form as
	// it now stands.
	m = pump(t, m, cmd)

	next, _ := m.Update(form.SubmitMsg{})
	if view := ansi.Strip(next.(app.Model).View().Content); !strings.Contains(view, "branch name required") {
		t.Fatalf("the form did not refuse the empty branch; its view after submit is:\n%s", view)
	}
}

// TestFormAndCommandResolveAutoTheSameWay extends the promise above to the
// account picker. A plan.Input field only ONE path can fill is exactly the
// drift TestFormAndCommandProduceTheSamePlan exists to catch, and `auto` adds
// two of them: the picked profile and its config dir.
//
// Both sides are driven over one stub picker. The form side reaches its answer
// through the same exported pair the submit pipeline uses -- ResolveAccount
// then WithAccount -- rather than through a test-only hook, which is what
// makes the comparison meaningful: a divergence here is a divergence in
// production code.
//
// It is a separate test from the table above rather than another row in it
// because the form side needs two things no other scenario does: a clauth feed
// (or there is no account row to select `auto` on) and a settled picker
// preview. Bolting those onto every row would make the common case harder to
// read in order to spare fifteen lines here.
func TestFormAndCommandResolveAutoTheSameWay(t *testing.T) {
	const projectDir = "/projects/thing"
	const title = "fix login redirect loop"
	const configTOML = `
branch_prefix = "zvi/"
[agents]
favorites = ["claude"]
[clauth]
picker = "stub"
default = "auto"
launch = "wrapper"
`
	contextJSON := `{"workspace_id":"wS0","workspace_cwd":"` + projectDir +
		`","tab_id":"tT0","focused_pane_id":"pP0"}`

	newStub := func() picker.Source {
		return &fakePicker{res: picker.Result{Profile: "alpha-1", Tier: "Max", ConfigDir: "/dirs/alpha-1"}}
	}

	configDir, stateDir := t.TempDir(), t.TempDir()
	writeConfig(t, configDir, configTOML)
	repoConfig := func(string) config.RepoConfig { return config.RepoConfig{} }

	fromCommand := commandPlanInput(t, commandCase{
		configDir:   configDir,
		stateDir:    stateDir,
		contextJSON: contextJSON,
		projectDir:  projectDir,
		repoConfig:  repoConfig,
		args:        []string{"--title", title},
		picker:      newStub(),
	})

	fromForm := formPlanInputAuto(t, formCase{
		configDir:   configDir,
		stateDir:    stateDir,
		contextJSON: contextJSON,
		repoConfig:  repoConfig,
		title:       title,
		picker:      newStub(),
		clauthStatus: clauth.Status{Schema: 1, ActiveProfile: "alpha-1", Profiles: []clauth.Profile{
			{Name: "alpha-1", Tier: "Max", AuthStatus: "ok"},
			{Name: "alpha-2", Tier: "Max", AuthStatus: "ok"},
		}},
	})

	if !reflect.DeepEqual(fromCommand, fromForm) {
		t.Fatalf("the command and the form disagree.\ncommand: %s\nform:    %s",
			showInput(fromCommand), showInput(fromForm))
	}

	// ... and both agree on the RIGHT thing. Two sides that agreed on nothing
	// in particular would satisfy DeepEqual just as well.
	if fromCommand.AccountPin != "alpha-1" {
		t.Errorf("AccountPin = %q, want the picker's answer", fromCommand.AccountPin)
	}
	if fromCommand.AccountConfigDir != "/dirs/alpha-1" {
		t.Errorf("AccountConfigDir = %q, want the picker's answer", fromCommand.AccountConfigDir)
	}
	if fromCommand.AccountLaunch != plan.LaunchWrapper {
		t.Errorf("AccountLaunch = %v, want LaunchWrapper from config.toml", fromCommand.AccountLaunch)
	}
}

// formPlanInputAuto is formPlanInput plus the commit-time account pick the
// submit pipeline performs. Kept separate rather than folded in, because every
// other scenario has no picker and calling ResolveAccount there would assert
// nothing while making the common path harder to read.
func formPlanInputAuto(t *testing.T, c formCase) plan.Input {
	t.Helper()
	m := withLinkedCommit(t, formModel(t, c))
	res, err := m.ResolveAccount(context.Background())
	if err != nil {
		t.Fatalf("ResolveAccount: %v", err)
	}
	return m.WithAccount(res).PlanInput()
}

// TestFormAndCommandMarkReadyTheSameWay drives the toggle from BOTH sides:
// the command by flag, the form by keystroke (⇥ to the prompt, type it, ⌃X).
// The table above only ever compares resolved defaults; this is the case
// where each side is TOLD, which is where a flag without a working form
// control -- or a form control nothing reads -- would show up.
//
// It couples to the form's key grammar on purpose, against formCase's own
// advice, because the claim under test is that the control exists and
// reaches plan.Input (reap spec §3.2). The two keys it uses are the grammar's
// most stable: ⇥ from a filled title, and the toggle's own chord.
func TestFormAndCommandMarkReadyTheSameWay(t *testing.T) {
	const projectDir = "/projects/thing"
	const title = "fix login redirect loop"
	const prompt = "look at the login redirect loop"
	contextJSON := `{"workspace_id":"wS0","workspace_cwd":"` + projectDir +
		`","tab_id":"tT0","focused_pane_id":"pP0"}`
	repoConfig := func(string) config.RepoConfig { return config.RepoConfig{} }

	for _, tc := range []struct {
		name       string
		configTOML string
		flag       string
		want       bool
	}{
		{"off by default, turned on", "branch_prefix = \"zvi/\"\n[agents]\nfavorites = [\"claude\"]\n", "--reap", true},
		{"on from config.toml, turned off", "branch_prefix = \"zvi/\"\n[agents]\nfavorites = [\"claude\"]\n[reaper]\nmark_ready = true\n", "--no-reap", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configDir, stateDir := t.TempDir(), t.TempDir()
			writeConfig(t, configDir, tc.configTOML)

			fromCommand := commandPlanInput(t, commandCase{
				configDir: configDir, stateDir: stateDir, contextJSON: contextJSON,
				projectDir: projectDir, repoConfig: repoConfig,
				args: []string{"--title", title, "--prompt", prompt, tc.flag},
			})

			m := formModel(t, formCase{
				configDir: configDir, stateDir: stateDir, contextJSON: contextJSON,
				repoConfig: repoConfig, title: title,
			})
			if got := m.PlanInput().MarkReady; got == tc.want {
				t.Fatalf("test setup: the form already resolved MarkReady = %v before ⌃X, so the keystroke proves nothing", got)
			}
			m = send(m, tea.KeyPressMsg{Code: tea.KeyTab})
			for _, r := range prompt {
				m = send(m, tea.KeyPressMsg{Code: r, Text: string(r)})
			}
			m = send(m, tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
			fromForm := m.PlanInput()

			if !reflect.DeepEqual(fromCommand, fromForm) {
				t.Fatalf("the command and the form disagree.\ncommand: %s\nform:    %s",
					showInput(fromCommand), showInput(fromForm))
			}
			if fromCommand.MarkReady != tc.want || fromCommand.Prompt != prompt {
				t.Errorf("MarkReady = %v, Prompt = %q; want %v and the typed prompt", fromCommand.MarkReady, fromCommand.Prompt, tc.want)
			}
		})
	}
}

// TestFormAndCommandAgentOptionsTheSameWay is the options row's half of
// "every create flag has a form control behind it" (agent-options spec
// §8.1): a flag and the keystrokes a person would make in the row must build
// the same plan.Input, over a config.toml default the two both move off.
//
// It couples to the key grammar for TestFormAndCommandMarkReadyTheSameWay's
// reason. ⇧⇥ ⇧⇥ from the title reaches the row because this form has no
// issue or account row: the ring wraps to Create, then to `options`.
func TestFormAndCommandAgentOptionsTheSameWay(t *testing.T) {
	const projectDir = "/projects/thing"
	const title = "fix login redirect loop"
	contextJSON := `{"workspace_id":"wS0","workspace_cwd":"` + projectDir +
		`","tab_id":"tT0","focused_pane_id":"pP0"}`
	repoConfig := func(string) config.RepoConfig { return config.RepoConfig{} }
	back := tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}

	for _, tc := range []struct {
		name    string
		options string
		flags   []string
		keys    []tea.KeyPressMsg
		want    agentopts.Values
	}{
		{
			name:    "a configured effort, moved one level up",
			options: "effort = \"high\"\n",
			flags:   []string{"--effort", "xhigh"},
			keys:    []tea.KeyPressMsg{back, back, {Code: tea.KeyDown}, {Code: tea.KeyRight}},
			want:    agentopts.Values{"effort": "xhigh"},
		},
		{
			name:    "a configured model, cleared back to inherit",
			options: "model = \"opus\"\npermission_mode = \"plan\"\n",
			flags:   []string{"--model", "inherit"},
			keys:    []tea.KeyPressMsg{back, back, {Code: tea.KeyLeft}, {Code: tea.KeyLeft}},
			want:    agentopts.Values{"permission_mode": "plan"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configDir, stateDir := t.TempDir(), t.TempDir()
			writeConfig(t, configDir, "branch_prefix = \"zvi/\"\n[agents]\nfavorites = [\"claude\"]\n[agents.options.claude]\n"+tc.options)

			fromCommand := commandPlanInput(t, commandCase{
				configDir: configDir, stateDir: stateDir, contextJSON: contextJSON,
				projectDir: projectDir, repoConfig: repoConfig,
				args: append([]string{"--title", title}, tc.flags...),
			})

			m := formModel(t, formCase{
				configDir: configDir, stateDir: stateDir, contextJSON: contextJSON,
				repoConfig: repoConfig, title: title,
			})
			if got := m.PlanInput().AgentOptions; reflect.DeepEqual(got, tc.want) {
				t.Fatalf("test setup: the form already resolved %v before any keystroke, so the keystrokes prove nothing", got)
			}
			for _, k := range tc.keys {
				m = send(m, k)
			}
			fromForm := m.PlanInput()

			if !reflect.DeepEqual(fromCommand, fromForm) {
				t.Fatalf("the command and the form disagree.\ncommand: %s\nform:    %s",
					showInput(fromCommand), showInput(fromForm))
			}
			if !reflect.DeepEqual(fromCommand.AgentOptions, tc.want) {
				t.Errorf("AgentOptions = %v, want %v", fromCommand.AgentOptions, tc.want)
			}
		})
	}
}

// pinAccount tabs to the account row and commits a pin on the profile in
// `row` (1-based among the profiles, below the `active` sentinel), which is
// the only way a pin reaches the form from outside package app: no config or
// memory tier carries an account, AccountField.SetPin is unexported, and
// Model.WithAccount -- the commit-time picker answer -- lands AFTER
// checkSubmitValidation has already run and so cannot stand in for a pin the
// validation is supposed to see.
//
// The tab count is discovered rather than written down, so a change to the
// row order (which internal/app declares in one place) moves this with it
// instead of silently pinning the wrong row.
func pinAccount(t *testing.T, m app.Model, row int, want string) app.Model {
	t.Helper()
	focused := false
	for i := 0; i < 20 && !focused; i++ {
		for _, ln := range strings.Split(ansi.Strip(m.View().Content), "\n") {
			if strings.Contains(ln, "\u258c") && strings.Contains(ln, "account") {
				focused = true
				break
			}
		}
		if !focused {
			m = send(m, tea.KeyPressMsg{Code: tea.KeyTab})
		}
	}
	if !focused {
		t.Fatalf("never reached the account row by tabbing; the form is:\n%s", ansi.Strip(m.View().Content))
	}
	for i := 0; i < row; i++ {
		m = send(m, tea.KeyPressMsg{Code: tea.KeyDown})
	}
	m = send(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := m.PlanInput().AccountPin; got != want {
		t.Fatalf("test setup: the form pinned %q, want %q", got, want)
	}
	return m
}

// TestFormAndCommandRefuseTheSameAuthStatuses is the auth half of v2 spec
// §13's promise, and it did not exist until #243/#245 -- which is how the
// two paths came to be compared on branches, labels and every plan.Input
// field while nothing at all held them to the same answer about a clauth
// profile. A refusal is not a plan.Input field, so the equivalence table
// cannot see this kind of disagreement; the two functions that decide it
// (app.Model.accountAuthBlocked and refuseUnusableProfile) live in different
// packages, were written at different times, and each carries a doc comment
// claiming to be the other.
//
// The table is every auth_status clauth is known to write -- across three
// of its versions, because the set is not closed: `expiring` is schema 1's
// spelling of `expired`, and `unknown` joined additively under schema 2
// without a bump. `revoked` stands in for the next such arrival, which is a
// thing that happens rather than a thing the schema rule prevents. Only
// `broken` is refused. The degraded column is #245: a schema nobody has
// read clauth's reason for makes every field past the profile's name
// unreliable, so neither path may refuse on one, `broken` included.
//
// Each path is driven through its own real entry point rather than through
// its refusal function, so the ORDER each applies the check in is under test
// too: a check the command ran after the account pick, or the form ran after
// the plan was built, would pass a direct call and fail here.
func TestFormAndCommandRefuseTheSameAuthStatuses(t *testing.T) {
	const projectDir = "/projects/thing"
	contextJSON := `{"workspace_id":"wS0","workspace_cwd":"` + projectDir +
		`","tab_id":"tT0","focused_pane_id":"pP0"}`

	for _, tc := range []struct {
		name     string
		status   string
		degraded bool
		refused  bool
	}{
		{name: "absent", status: "", refused: false},
		{name: "ok", status: clauth.AuthOK, refused: false},
		{name: "expired", status: clauth.AuthExpired, refused: false},
		{name: "expiring", status: clauth.AuthExpiring, refused: false},
		{name: "unknown", status: clauth.AuthUnknown, refused: false},
		{name: "broken", status: clauth.AuthBroken, refused: true},
		{name: "unrecognized", status: "revoked", refused: false},

		{name: "absent degraded", status: "", degraded: true, refused: false},
		{name: "ok degraded", status: clauth.AuthOK, degraded: true, refused: false},
		{name: "expired degraded", status: clauth.AuthExpired, degraded: true, refused: false},
		{name: "expiring degraded", status: clauth.AuthExpiring, degraded: true, refused: false},
		{name: "unknown degraded", status: clauth.AuthUnknown, degraded: true, refused: false},
		{name: "broken degraded", status: clauth.AuthBroken, degraded: true, refused: false},
		{name: "unrecognized degraded", status: "revoked", degraded: true, refused: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status := clauth.Status{
				Schema:        1,
				ActiveProfile: "alpha-1",
				Degraded:      tc.degraded,
				Profiles: []clauth.Profile{
					{Name: "alpha-1", Tier: "Max", AuthStatus: clauth.AuthOK},
					{Name: "alpha-2", Tier: "Max", AuthStatus: tc.status},
				},
			}
			if tc.degraded {
				status.Schema = 7
			}

			// The command, through Run: every pre-flight step in its real
			// order, exit 2 for a request it will not resolve.
			h := newHarness(t)
			h.deps.Clauth = &fakeClauth{status: status}
			code := h.run("--title", "fix login redirect loop", "--account", "alpha-2", "--no-worktree")
			commandRefused := code == ExitUsage
			if !commandRefused && code != ExitOK {
				t.Fatalf("test setup: the command exited %d, which is neither a refusal nor a create\nstderr: %s", code, h.stderr)
			}

			// The form, through a submit on a pin a person committed.
			configDir, stateDir := t.TempDir(), t.TempDir()
			writeConfig(t, configDir, "[agents]\nfavorites = [\"claude\"]\n")
			issue := &linear.Issue{Identifier: "LIN-42", Title: "Fix login redirect loop", BranchName: "zvi/lin-42-fix-login"}
			m := pinAccount(t, formModel(t, formCase{
				configDir: configDir, stateDir: stateDir, contextJSON: contextJSON,
				repoConfig:   func(string) config.RepoConfig { return config.RepoConfig{} },
				issue:        issue,
				landChecks:   true,
				clauthStatus: status,
			}), 2, "alpha-2")

			next, _ := m.Update(form.SubmitMsg{})
			view := ansi.Strip(next.(app.Model).View().Content)
			formRefused := strings.Contains(view, "clauth reports")

			if commandRefused != tc.refused || formRefused != tc.refused {
				t.Fatalf("want refused = %v from both; the command exited %d (stderr: %s) and the form's view after submit is:\n%s",
					tc.refused, code, h.stderr, view)
			}
			if !tc.refused {
				return
			}
			// Both refusals name the value, so neither can be read as a
			// generic "clauth said no" that a future widening would hide in.
			if !strings.Contains(h.stderr.String(), clauth.AuthBroken) || !strings.Contains(view, clauth.AuthBroken) {
				t.Errorf("both refusals should name %q\ncommand: %s\nform:\n%s", clauth.AuthBroken, h.stderr, view)
			}
		})
	}
}
