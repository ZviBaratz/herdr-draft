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

func TestBuildWorktreeTabHere(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	in.Placement = PlacementTabHere

	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	want := []OpKind{OpWorktreeCreate, OpTabCreate, OpAgentStart}
	if got := kindsOf(ops); !reflect.DeepEqual(got, want) {
		t.Fatalf("op kinds = %v, want %v", got, want)
	}

	wt := ops[0].Worktree
	if wt == nil {
		t.Fatal("ops[0].Worktree is nil")
	}
	if wt.Focus {
		t.Error("worktree op Focus = true, want false: a placement op follows and owns focus")
	}

	tab := ops[1].Tab
	if tab == nil {
		t.Fatal("ops[1].Tab is nil")
	}
	if tab.Workspace != in.Ctx.WorkspaceID {
		t.Errorf("Tab.Workspace = %q, want %q (the INVOKING workspace, not the worktree's)", tab.Workspace, in.Ctx.WorkspaceID)
	}
	if tab.Cwd != "" {
		t.Errorf("Tab.Cwd = %q, want empty -- Execute fills it from the worktree's CheckoutPath at runtime", tab.Cwd)
	}
	if tab.Label != in.Title {
		t.Errorf("Tab.Label = %q, want %q", tab.Label, in.Title)
	}
	if !tab.Focus {
		t.Error("Tab.Focus = false, want true: the placement op is what owns focus now")
	}
	if !ops[1].CwdFromCheckout {
		t.Error("ops[1].CwdFromCheckout = false, want true")
	}
}

func TestBuildWorktreeSplitHere(t *testing.T) {
	in := validInput()
	in.UseWorktree = true
	in.Placement = PlacementSplitHere

	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	want := []OpKind{OpWorktreeCreate, OpPaneSplit, OpAgentStart}
	if got := kindsOf(ops); !reflect.DeepEqual(got, want) {
		t.Fatalf("op kinds = %v, want %v", got, want)
	}
	if ops[0].Worktree.Focus {
		t.Error("worktree op Focus = true, want false")
	}
	split := ops[1].Split
	if split == nil {
		t.Fatal("ops[1].Split is nil")
	}
	if split.PaneID != in.Ctx.FocusedPaneID {
		t.Errorf("Split.PaneID = %q, want %q (the INVOKING pane)", split.PaneID, in.Ctx.FocusedPaneID)
	}
	if split.Direction != defaultSplitDirection {
		t.Errorf("Split.Direction = %q, want %q", split.Direction, defaultSplitDirection)
	}
	if split.Cwd != "" {
		t.Errorf("Split.Cwd = %q, want empty", split.Cwd)
	}
	if !split.Focus || !ops[1].CwdFromCheckout {
		t.Error("Split.Focus/CwdFromCheckout: want true/true")
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
		t.Error("worktree op Focus = false, want true: no placement op follows, so the worktree owns focus exactly as before")
	}
	if ops[0].CwdFromCheckout {
		t.Error("the worktree op itself must never carry CwdFromCheckout -- it is the SOURCE of the checkout path, not a consumer of it")
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
// mode that can honour it. Build is pure and cannot say so out loud; the
// caller reports it.
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
