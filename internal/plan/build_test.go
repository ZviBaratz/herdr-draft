package plan

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
)

// validInput returns a valid baseline Input (in-place, new workspace,
// active/unpinned claude, no prompt); each test overrides only the fields
// it cares about.
func validInput() Input {
	return Input{
		ProjectDir:       "/repo",
		Title:            "Fix pagination",
		Branch:           "zvi/fix-pagination",
		BaseRef:          "main",
		UseWorktree:      false,
		IsGitRepo:        true,
		Placement:        PlacementNewSpace,
		AgentKind:        "claude",
		ExtraArgs:        nil,
		AccountPin:       "",
		Prompt:           "",
		Ctx:              herdrc.Context{WorkspaceID: "w1", FocusedPaneID: "w1:p2"},
		DetectionTimeout: 5 * time.Second,
		PromptTimeout:    30 * time.Second,
		TrustRepository:  false,
	}
}

func kindsOf(ops []Op) []OpKind {
	out := make([]OpKind, len(ops))
	for i, op := range ops {
		out[i] = op.Kind
	}
	return out
}

// TestEffectivePlacement_AWorktreeSessionRunsInItsOwnSpace pins placement
// spec §14's rule at the one place both callers take it from: with a
// worktree, whatever placement was chosen or remembered, the session runs
// in the worktree's own space; without one, the choice stands untouched.
func TestEffectivePlacement_AWorktreeSessionRunsInItsOwnSpace(t *testing.T) {
	for _, p := range []Placement{PlacementNewSpace, PlacementTabHere, PlacementSplitHere, PlacementTabIn} {
		if got := EffectivePlacement(true, p); got != PlacementNewSpace {
			t.Errorf("EffectivePlacement(worktree, %v) = %v, want %v", p, got, PlacementNewSpace)
		}
		if got := EffectivePlacement(false, p); got != p {
			t.Errorf("EffectivePlacement(no worktree, %v) = %v, want it unchanged", p, got)
		}
	}
}

func TestBuildWorktreePinPrompt(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	in.AccountPin = "work"
	in.Prompt = "implement the fix"
	in.ExtraArgs = []string{"--model", "opus"}

	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	want := []OpKind{OpWorktreeCreate, OpClauthLaunch, OpAwaitDetection, OpAgentPrompt}
	if got := kindsOf(ops); !reflect.DeepEqual(got, want) {
		t.Fatalf("op kinds = %v, want %v", got, want)
	}

	wt := ops[0].Worktree
	if wt == nil {
		t.Fatal("ops[0].Worktree is nil")
	}
	if wt.Cwd != in.ProjectDir {
		t.Errorf("Worktree.Cwd = %q, want %q", wt.Cwd, in.ProjectDir)
	}
	if wt.Branch != in.Branch {
		t.Errorf("Worktree.Branch = %q, want %q", wt.Branch, in.Branch)
	}
	if wt.Base != in.BaseRef {
		t.Errorf("Worktree.Base = %q, want %q", wt.Base, in.BaseRef)
	}
	if wt.TrustRepository != in.TrustRepository {
		t.Errorf("Worktree.TrustRepository = %v, want %v", wt.TrustRepository, in.TrustRepository)
	}
	if wt.Label != in.Title {
		t.Errorf("Worktree.Label = %q, want %q", wt.Label, in.Title)
	}
	if !wt.Focus {
		t.Error("Worktree.Focus = false, want true")
	}

	wantArgv := []string{"clauth", "start", "work", "--", "--model", "opus"}
	if got := ops[1].RunArgv; !reflect.DeepEqual(got, wantArgv) {
		t.Fatalf("RunArgv = %v, want %v", got, wantArgv)
	}
	if ops[1].Agent != nil || ops[1].Worktree != nil || ops[1].Prompt != nil {
		t.Errorf("OpClauthLaunch has an unexpected populated request field: %+v", ops[1])
	}

	if ops[2].Timeout != in.DetectionTimeout {
		t.Errorf("AwaitDetection timeout = %v, want %v", ops[2].Timeout, in.DetectionTimeout)
	}

	pr := ops[3].Prompt
	if pr == nil {
		t.Fatal("ops[3].Prompt is nil")
	}
	if pr.Text != in.Prompt {
		t.Errorf("Prompt.Text = %q, want %q", pr.Text, in.Prompt)
	}
	if pr.WaitTimeout != in.PromptTimeout {
		t.Errorf("Prompt.WaitTimeout = %v, want %v", pr.WaitTimeout, in.PromptTimeout)
	}
}

func TestBuildWorktreeActiveClaude(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	in.Prompt = "start work"
	// AccountPin left "" (active/unpinned); AgentKind left "claude".

	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	want := []OpKind{OpWorktreeCreate, OpAgentStart, OpAgentPrompt}
	if got := kindsOf(ops); !reflect.DeepEqual(got, want) {
		t.Fatalf("op kinds = %v, want %v", got, want)
	}

	ag := ops[1].Agent
	if ag == nil {
		t.Fatal("ops[1].Agent is nil")
	}
	if ag.Name != AgentName(in.Title) {
		t.Errorf("Agent.Name = %q, want %q", ag.Name, AgentName(in.Title))
	}
	if ag.Kind != "claude" {
		t.Errorf("Agent.Kind = %q, want claude", ag.Kind)
	}
	if ag.PaneID != "" {
		t.Errorf("Agent.PaneID = %q, want empty (executor fills it)", ag.PaneID)
	}
}

// TestBuild_RefusesAWorktreeWithAnyOtherPlacement: a worktree session runs
// in its own space (placement spec §14), and both callers route their
// placement through EffectivePlacement before building. An Input that
// skipped it is refused, not quietly repaired: repairing it here would let
// the caller's plan.Input -- what `create --json` reports and what the
// memory files record -- disagree with what was built.
func TestBuild_RefusesAWorktreeWithAnyOtherPlacement(t *testing.T) {
	for _, p := range []Placement{PlacementTabHere, PlacementSplitHere, PlacementTabIn} {
		in := validInput()
		in.UseWorktree = true
		in.Placement = p
		in.Space = Space{WorkspaceID: "wG", Label: "herdr-draft"} // so tab-in is refused for the worktree, not the missing space
		ops, err := Build(in)
		if err == nil {
			t.Errorf("Build(worktree, %v) = %v ops, want an error", p, kindsOf(ops))
			continue
		}
		if !strings.Contains(err.Error(), "worktree") {
			t.Errorf("Build(worktree, %v) error = %q, want it to name the worktree", p, err)
		}
	}
}

func TestBuildWorktreeNewSpaceUnchanged(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	in.Placement = PlacementNewSpace // the explicit zero value, stated for clarity

	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	want := []OpKind{OpWorktreeCreate, OpAgentStart}
	if got := kindsOf(ops); !reflect.DeepEqual(got, want) {
		t.Fatalf("op kinds = %v, want %v -- new-space placement must not append anything", got, want)
	}
	if !ops[0].Worktree.Focus {
		t.Error("worktree op Focus = false, want true: the worktree's space is where the session runs, so it owns focus")
	}
}

func TestBuildTabHereCodexNoPrompt(t *testing.T) {
	in := validInput()
	in.Placement = PlacementTabHere
	in.AgentKind = "codex"
	// Prompt left "" -- no trailing OpAgentPrompt.

	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	want := []OpKind{OpTabCreate, OpAgentStart}
	if got := kindsOf(ops); !reflect.DeepEqual(got, want) {
		t.Fatalf("op kinds = %v, want %v", got, want)
	}

	tab := ops[0].Tab
	if tab == nil {
		t.Fatal("ops[0].Tab is nil")
	}
	if tab.Workspace != in.Ctx.WorkspaceID {
		t.Errorf("Tab.Workspace = %q, want %q", tab.Workspace, in.Ctx.WorkspaceID)
	}
	if tab.Cwd != in.ProjectDir {
		t.Errorf("Tab.Cwd = %q, want %q", tab.Cwd, in.ProjectDir)
	}
	if !tab.Focus {
		t.Error("Tab.Focus = false, want true")
	}

	if ops[1].Agent == nil || ops[1].Agent.Kind != "codex" {
		t.Errorf("Agent op wrong: %+v", ops[1].Agent)
	}
}

func TestBuildSplitHere(t *testing.T) {
	in := validInput()
	in.Placement = PlacementSplitHere

	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(ops) == 0 || ops[0].Kind != OpPaneSplit {
		t.Fatalf("ops[0].Kind = %v, want OpPaneSplit", kindsOf(ops))
	}

	split := ops[0].Split
	if split == nil {
		t.Fatal("ops[0].Split is nil")
	}
	if split.PaneID != in.Ctx.FocusedPaneID {
		t.Errorf("Split.PaneID = %q, want %q", split.PaneID, in.Ctx.FocusedPaneID)
	}
	if !split.Focus {
		t.Error("Split.Focus = false, want true")
	}
}

func TestBuildNewSpacePlacement(t *testing.T) {
	in := validInput()
	// Placement left at its zero value, PlacementNewSpace; UseWorktree left
	// false. This is the default, most common real-world path (a brand new
	// workspace, in-place).

	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	want := []OpKind{OpWorkspaceCreate, OpAgentStart}
	if got := kindsOf(ops); !reflect.DeepEqual(got, want) {
		t.Fatalf("op kinds = %v, want %v", got, want)
	}

	ws := ops[0].Workspace
	if ws == nil {
		t.Fatal("ops[0].Workspace is nil")
	}
	if ws.Cwd != in.ProjectDir {
		t.Errorf("Workspace.Cwd = %q, want %q", ws.Cwd, in.ProjectDir)
	}
	if ws.Label != in.Title {
		t.Errorf("Workspace.Label = %q, want %q", ws.Label, in.Title)
	}
	if !ws.Focus {
		t.Error("Workspace.Focus = false, want true")
	}
}

func TestBuildPinNonClaudeKindIsError(t *testing.T) {
	in := validInput()
	in.AccountPin = "work"
	in.AgentKind = "codex"

	if _, err := Build(in); err == nil {
		t.Fatal("Build: want error for account pin with non-claude kind, got nil")
	}
}

func TestBuildWorktreeWithoutGitRepoIsError(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	in.IsGitRepo = false

	if _, err := Build(in); err == nil {
		t.Fatal("Build: want error for UseWorktree without IsGitRepo, got nil")
	}
}

func TestBuildEmptyTitleIsError(t *testing.T) {
	in := validInput()
	in.Title = ""

	if _, err := Build(in); err == nil {
		t.Fatal("Build: want error for empty title, got nil")
	}
}

func TestBuildWhitespaceTitleIsError(t *testing.T) {
	in := validInput()
	in.Title = "   "

	if _, err := Build(in); err == nil {
		t.Fatal("Build: want error for whitespace-only title, got nil")
	}
}

func TestBuildEveryOpHasALabel(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	in.AccountPin = "work"
	in.Prompt = "go"

	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for i, op := range ops {
		if strings.TrimSpace(op.Label) == "" {
			t.Errorf("ops[%d] (%v) has an empty Label", i, op.Kind)
		}
	}
}

func TestAgentName(t *testing.T) {
	cases := []struct{ name, title, want string }{
		{"digit start gets s- prefix", "42 fix pagination", "s-42-fix-pagination"},
		{"lowercase start is untouched", "Fix pane focus", "fix-pane-focus"},
	}
	for _, c := range cases {
		if got := AgentName(c.title); got != c.want {
			t.Errorf("%s: AgentName(%q) = %q, want %q", c.name, c.title, got, c.want)
		}
	}
}

func TestAgentNameClampsLongTitles(t *testing.T) {
	title := strings.Repeat("a", 40)
	got := AgentName(title)
	if n := len([]rune(got)); n > 30 {
		t.Fatalf("AgentName(40-rune title) = %q (%d runes), want <= 30 runes", got, n)
	}
}

func TestAgentNameTrimsTrailingSeparatorAtClampBoundary(t *testing.T) {
	// SanitizeBranch("a-" * 40) is "a-a-a-...-a" (79 runes, alternating
	// a/-), and the 30-rune clamp lands exactly on a '-' (index 29): the
	// clamp alone would produce "...a-a-" with a dangling trailing hyphen.
	title := strings.Repeat("a-", 40)
	got := AgentName(title)

	if strings.HasSuffix(got, "-") || strings.HasSuffix(got, "_") {
		t.Fatalf("AgentName(%q) = %q, ends with a separator", title, got)
	}
	if got == "" {
		t.Fatalf("AgentName(%q) = %q, want non-empty", title, got)
	}
	if n := len([]rune(got)); n > 30 {
		t.Fatalf("AgentName(%q) = %q (%d runes), want <= 30 runes", title, got, n)
	}
	if !agentNamePattern.MatchString(got) {
		t.Fatalf("AgentName(%q) = %q, does not match %s", title, got, agentNamePattern.String())
	}
}

var agentNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

func TestAgentNameAlwaysMatchesHerdrPattern(t *testing.T) {
	titles := []string{
		"42 fix pagination",
		"Fix pane focus",
		strings.Repeat("a", 40),
		strings.Repeat("Z9 ", 20),
		"",
		"   ",
		"???",
		"héllo world",
		"-leading-dash",
		"UPPER CASE TITLE",
	}
	for _, title := range titles {
		got := AgentName(title)
		if !agentNamePattern.MatchString(got) {
			t.Errorf("AgentName(%q) = %q, does not match %s", title, got, agentNamePattern.String())
		}
	}
}

// findOp returns the single op of kind in ops, failing when there is none.
func findOp(t *testing.T, ops []Op, kind OpKind) Op {
	t.Helper()
	for _, op := range ops {
		if op.Kind == kind {
			return op
		}
	}
	t.Fatalf("no %v in %v", kind, ops)
	return Op{}
}

// The zero LaunchMode must be today's behaviour, byte for byte. That is what
// lets every existing caller -- and every test written before this field
// existed -- go on meaning what it meant.
func TestBuildDefaultLaunchIsClauthStart(t *testing.T) {
	in := validInput()
	in.AccountPin = "alpha-1"
	in.ExtraArgs = []string{"--model", "opus[1m]"}
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	op := findOp(t, ops, OpClauthLaunch)
	if got, want := strings.Join(op.RunArgv, " "), "clauth start alpha-1 -- --model opus[1m]"; got != want {
		t.Fatalf("RunArgv = %q, want %q", got, want)
	}
	if len(op.RunEnv) != 0 {
		t.Fatalf("RunEnv = %v, want none", op.RunEnv)
	}
}

// Wrapper mode types a bare `claude` under a CLAUDE_CONFIG_DIR assignment, so
// the pane's login shell resolves `claude` itself -- to a function, where the
// user has one.
func TestBuildWrapperLaunchTypesClaudeUnderAConfigDir(t *testing.T) {
	in := validInput()
	in.AccountPin = "alpha-1"
	in.AccountLaunch = LaunchWrapper
	in.AccountConfigDir = "/home/a/dirs/alpha-1"
	in.ExtraArgs = []string{"--model", "opus[1m]"}
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	op := findOp(t, ops, OpClauthLaunch)
	if got, want := strings.Join(op.RunArgv, " "), "claude --model opus[1m]"; got != want {
		t.Fatalf("RunArgv = %q, want %q", got, want)
	}
	want := []herdrc.EnvVar{{Name: ClaudeConfigDirVar, Value: "/home/a/dirs/alpha-1"}}
	if !reflect.DeepEqual(op.RunEnv, want) {
		t.Fatalf("RunEnv = %v, want %v", op.RunEnv, want)
	}
}

// Wrapper mode with no config dir has nothing to isolate WITH. A bare
// `claude` there would launch under whatever credential happened to be
// machine-global -- the opposite of a pin -- so the pin falls back to the
// mode that can honour it, and SAYS SO on the step's own label, which is what
// `create` prints one line per step to stderr.
func TestBuildWrapperWithoutAConfigDirFallsBackToClauthStart(t *testing.T) {
	in := validInput()
	in.AccountPin = "alpha-1"
	in.AccountLaunch = LaunchWrapper
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	op := findOp(t, ops, OpClauthLaunch)
	if got, want := strings.Join(op.RunArgv, " "), "clauth start alpha-1 --"; got != want {
		t.Fatalf("RunArgv = %q, want %q", got, want)
	}
	if len(op.RunEnv) != 0 {
		t.Fatalf("RunEnv = %v, want none", op.RunEnv)
	}
	if !strings.Contains(op.Label, "no config dir") {
		t.Fatalf("Label = %q, want one saying wrapper mode was downgraded", op.Label)
	}
}

// And the label stays clean when nothing was substituted -- a note on every
// launch would say nothing on the one launch it exists for.
func TestBuildDoesNotClaimADowngradeThatDidNotHappen(t *testing.T) {
	for _, tc := range []struct {
		name      string
		launch    LaunchMode
		configDir string
	}{
		{"clauth start", LaunchClauthStart, ""},
		{"wrapper with a config dir", LaunchWrapper, "/home/a/dirs/alpha-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := validInput()
			in.AccountPin = "alpha-1"
			in.AccountLaunch = tc.launch
			in.AccountConfigDir = tc.configDir
			ops, err := Build(in)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if got := findOp(t, ops, OpClauthLaunch).Label; got != "typing the clauth launch" {
				t.Fatalf("Label = %q, want the plain launch label", got)
			}
		})
	}
}

// A config dir with no pin is not a launch instruction. Unpinned means
// "whatever clauth has live", and there is no clauth launch op at all.
func TestBuildIgnoresAConfigDirWithNoPin(t *testing.T) {
	in := validInput()
	in.AccountLaunch = LaunchWrapper
	in.AccountConfigDir = "/home/a/dirs/alpha-1"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, op := range ops {
		if op.Kind == OpClauthLaunch {
			t.Fatal("an unpinned account must not produce a clauth launch op")
		}
	}
}

// --- where the two launch mechanisms meet ---------------------------------

// The wrapper-mode FALLBACK goes through the launcher template, not through a
// second hardcoded `clauth start`.
//
// This is the merge of #121 and #122 in one row. Both PRs replaced the same
// line of launchOps: #121 with resolveLauncher, #122 with clauthLaunchCommand.
// Taking #122's version wholesale -- which is what a conflict resolution
// naturally does -- left a hardcoded `clauth start` on the non-wrapper branch
// and made `[clauth] launcher` inert for every pinned launch, with the whole
// suite still green, because #121's plan rows only ever exercise the path
// where AccountLaunch is the zero value.
func TestWrapperFallbackStillHonoursTheLauncher(t *testing.T) {
	in := validInput()
	in.AccountPin = "alpha-1"
	in.AccountLaunch = LaunchWrapper // asked for...
	in.AccountConfigDir = ""         // ...but no config dir, so it falls back
	in.Launcher = []string{"claude-as", accountPlaceholder}

	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	op := findOp(t, ops, OpClauthLaunch)
	if got, want := strings.Join(op.RunArgv, " "), "claude-as alpha-1"; got != want {
		t.Fatalf("RunArgv = %q, want %q -- the fallback must use the configured launcher", got, want)
	}
	if len(op.RunEnv) != 0 {
		t.Fatalf("RunEnv = %v, want none on the argv mechanism", op.RunEnv)
	}
	if !strings.Contains(op.Label, "no config dir") {
		t.Fatalf("Label = %q, want the downgrade reported", op.Label)
	}
}

// And wrapper mode that DOES run ignores the launcher entirely: its argv is a
// bare `claude` and the account travels in the environment. config.Load resets
// a launcher set alongside wrapper mode, so this combination should not reach
// a real pane -- pinned here anyway, because plan is a pure builder that
// accepts whatever Input it is handed and must not splice the two mechanisms.
func TestWrapperModeDoesNotUseTheLauncher(t *testing.T) {
	in := validInput()
	in.AccountPin = "alpha-1"
	in.AccountLaunch = LaunchWrapper
	in.AccountConfigDir = "/dirs/alpha-1"
	in.Launcher = []string{"claude-as", accountPlaceholder}

	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	op := findOp(t, ops, OpClauthLaunch)
	if got, want := strings.Join(op.RunArgv, " "), "claude"; got != want {
		t.Fatalf("RunArgv = %q, want %q", got, want)
	}
	want := []herdrc.EnvVar{{Name: ClaudeConfigDirVar, Value: "/dirs/alpha-1"}}
	if !reflect.DeepEqual(op.RunEnv, want) {
		t.Fatalf("RunEnv = %v, want %v", op.RunEnv, want)
	}
}

// promptOp returns Build's OpAgentPrompt, or nil when the plan has none.
func promptOp(t *testing.T, in Input) *Op {
	t.Helper()
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for i := range ops {
		if ops[i].Kind == OpAgentPrompt {
			return &ops[i]
		}
	}
	return nil
}

// TestBuild_MarkReadyAppendsToThePromptOp is reap spec §4.2 at the one place
// it takes effect: the text OpAgentPrompt carries is what every guard in
// Execute reads and what herdr receives (reap spec §9.2).
func TestBuild_MarkReadyAppendsToThePromptOp(t *testing.T) {
	in := validInput()
	in.Prompt = "fix the redirect loop\n"
	in.MarkReady = true
	op := promptOp(t, in)
	if op == nil {
		t.Fatal("no OpAgentPrompt")
	}
	if want := "fix the redirect loop\n\n" + ReapInstruction; op.Prompt.Text != want {
		t.Errorf("prompt text = %q, want %q", op.Prompt.Text, want)
	}
}

func TestBuild_MarkReadyOffSendsThePromptVerbatim(t *testing.T) {
	in := validInput()
	in.Prompt = "fix the redirect loop\n"
	op := promptOp(t, in)
	if op == nil || op.Prompt.Text != "fix the redirect loop\n" {
		t.Fatalf("prompt op = %+v, want the prompt byte for byte", op)
	}
}

func TestBuild_MarkReadyNeverAppendsToASlashCommand(t *testing.T) {
	in := validInput()
	in.Prompt = "/loop 5m check CI"
	in.MarkReady = true
	op := promptOp(t, in)
	if op == nil || op.Prompt.Text != "/loop 5m check CI" {
		t.Fatalf("prompt op = %+v, want the slash command untouched", op)
	}
}

func TestBuild_MarkReadyWithNoPromptAddsNoPromptOp(t *testing.T) {
	in := validInput()
	in.MarkReady = true
	if op := promptOp(t, in); op != nil {
		t.Fatalf("prompt op = %+v, want none: there is nothing to append to", op)
	}
}
