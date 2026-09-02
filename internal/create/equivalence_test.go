package create

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/ZviBaratz/herdr-draft/internal/app"
	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
	"github.com/ZviBaratz/herdr-draft/internal/plan"
	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// TestFormAndCommandProduceTheSamePlan is spec §13's whole promise, stated
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
// type, and reads back the plan.Input its submit would build. The two
// plan.Inputs must be identical, field for field.
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

	for _, tc := range []struct {
		name string
		// configTOML, lastUsed, projects and repo are the four tiers under
		// the built-in default; each scenario shifts which one wins.
		configTOML string
		lastUsed   string
		projects   string
		repo       config.RepoConfig
		// args are the command's flags; the form is driven with the
		// equivalent user input (the title, always) and nothing else.
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
			// .herdr-draft.toml wins the prefix over config.toml; and the
			// worktree overrides the tab-here placement every tier agrees
			// on, because a worktree is always a new space.
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

			fromCommand := commandPlanInput(t, commandCase{
				configDir:   configDir,
				stateDir:    stateDir,
				contextJSON: contextJSON,
				projectDir:  projectDir,
				repoConfig:  repoConfig,
				args:        tc.args,
			})
			fromForm := formPlanInput(t, formCase{
				configDir:   configDir,
				stateDir:    stateDir,
				contextJSON: contextJSON,
				repoConfig:  repoConfig,
				title:       title,
			})

			if !reflect.DeepEqual(fromCommand, fromForm) {
				t.Fatalf("the command and the form disagree.\ncommand: %s\nform:    %s",
					showInput(fromCommand), showInput(fromForm))
			}

			// ... and both agree on the RIGHT thing.
			want := tc.want
			want.ProjectDir, want.Title, want.IsGitRepo = projectDir, title, true
			want.Ctx = mustContext(t, contextJSON)
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
	repoConfig          func(string) config.RepoConfig
	args                []string
}

func commandPlanInput(t *testing.T, c commandCase) plan.Input {
	t.Helper()
	t.Setenv("LINEAR_API_KEY", "")

	req, err := parseArgs(c.args)
	if err != nil {
		t.Fatalf("parseArgs(%v): %v", c.args, err)
	}
	resolved, err := resolveRequest(context.Background(), req, Env{
		ConfigDir:   c.configDir,
		StateDir:    c.stateDir,
		ContextJSON: c.contextJSON,
	}, Deps{
		Runner:     newFakeRunner(),
		Git:        newFakeGit(),
		RepoConfig: c.repoConfig,
		Stdin:      strings.NewReader(""),
		Stdout:     &strings.Builder{},
		Stderr:     &strings.Builder{},
		Workdir:    func() (string, error) { return c.projectDir, nil },
	})
	if err != nil {
		t.Fatalf("resolveRequest: %v", err)
	}
	return resolved.input
}

// formCase/formPlanInput build a real app.Model over the same tiers, let
// it settle, type the title, and read back what its submit would build.
// The form side is deliberately driven with the ONE input a user gives
// that this comparison needs -- the title -- and nothing else. Every
// scenario above therefore differs only in its configuration tiers, which
// is precisely the claim under test ("unset flags resolve through the
// resolver"). Driving the form's chip rows and pickers by keystroke would
// couple this test to internal/form's key grammar, which is being
// rewritten under a separate issue; the flags-beat-the-tiers half is
// covered on the command side alone, by TestFlagsBeatEveryTier.
type formCase struct {
	configDir, stateDir string
	contextJSON         string
	repoConfig          func(string) config.RepoConfig
	title               string
}

func formPlanInput(t *testing.T, c formCase) plan.Input {
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

	m := app.New(app.Setup{
		Deps: app.Deps{
			Runner:     newFakeRunner(),
			Git:        &formGit{},
			Clock:      app.Clock{Sleep: func(time.Duration) {}},
			RepoConfig: c.repoConfig,
		},
		Ctx:      hctx,
		Config:   cfg,
		State:    state,
		Projects: projects,
		Palette:  theme.Default(),
		StateDir: c.stateDir,
	})

	// A real terminal size first: several fields only lay out (and the
	// pickers only page) once they have one.
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	// Then the form's own startup work -- the debounced directory check
	// that resolves the project's repository root, reads its
	// .herdr-draft.toml and applies spec §10's per-project memory, and the
	// base-branch listing the remembered base needs in order to land.
	m = pump(t, m, m.Init())
	// Then the one thing a user types. The commands a keystroke returns
	// are deliberately dropped rather than pumped: they are the cursor's
	// blink and the debounced title-duplicate check, and neither touches
	// any field plan.Input reads (the branch derivation a title change
	// drives is synchronous, inside reactToChanges). Pumping them would
	// mean waiting out one blocked blink command per character.
	for _, r := range c.title {
		m = send(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return m.PlanInput()
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

// TestFlagsBeatEveryTier is the other half of spec §13's precedence: an
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
// whose root is itself, with two branches.
type formGit struct{}

func (formGit) DirExists(string) bool { return true }
func (formGit) IsGitRepo(string) bool { return true }
func (formGit) RepoRoot(_ context.Context, dir string) (string, error) {
	return dir, nil
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
