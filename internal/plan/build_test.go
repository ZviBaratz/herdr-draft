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
