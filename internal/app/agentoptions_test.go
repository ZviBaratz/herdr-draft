package app

import (
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ZviBaratz/herdr-draft/internal/agentopts"
	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
	"github.com/ZviBaratz/herdr-draft/internal/plan"
)

func press(t *testing.T, m Model, keys ...tea.KeyPressMsg) Model {
	t.Helper()
	for _, k := range keys {
		next, _ := m.Update(k)
		m = next.(Model)
	}
	return m
}

// Agent-options spec §6.1: config.toml's [agents.options.<kind>] is where
// the row starts, and what the plan carries if nobody touches it.
func TestOptions_ConfigSeedReachesThePlan(t *testing.T) {
	m := newTestModel(t, testSetup{Config: config.Config{Agents: config.AgentsConfig{
		Options: map[string]agentopts.Values{"claude": {"effort": "xhigh"}},
	}}})
	if got, want := m.PlanInput().AgentOptions, (agentopts.Values{"effort": "xhigh"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("AgentOptions = %v, want %v", got, want)
	}
	if row := ansi.Strip(m.options.Row(60)); !strings.Contains(row, "effort xhigh") {
		t.Fatalf("options row = %q", row)
	}
}

// Nothing configured is nil in the plan -- the shape equivalence_test.go
// compares against create's with reflect.DeepEqual.
func TestOptions_NothingSetIsNilInThePlan(t *testing.T) {
	m := newTestModel(t, testSetup{})
	if got := m.PlanInput().AgentOptions; got != nil {
		t.Fatalf("AgentOptions = %#v, want nil", got)
	}
}

// The row follows the agent row, through the real message path: options
// chosen for claude never ride along with codex, and come back with
// claude (spec §7.2).
func TestOptions_FollowTheAgentKind(t *testing.T) {
	m := newTestModel(t, testSetup{Config: config.Config{Agents: config.AgentsConfig{Favorites: []string{"claude", "codex"}}}})
	m.form.FocusByID("options")
	m = press(t, m, key(tea.KeyRight, 0)) // model: inherit -> fable
	if got, want := m.PlanInput().AgentOptions, (agentopts.Values{"model": "fable"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("after choosing: AgentOptions = %v, want %v", got, want)
	}

	m.form.FocusByID("agent")
	m = press(t, m, key(tea.KeyRight, 0)) // claude -> codex
	if m.agent.Value() != "codex" {
		t.Fatalf("setup: agent = %q", m.agent.Value())
	}
	if got := m.PlanInput().AgentOptions; got != nil {
		t.Fatalf("codex carried claude's options: %v", got)
	}
	if m.options.Enabled() {
		t.Fatalf("the options row is a focus stop for codex, which declares nothing")
	}

	m = press(t, m, key(tea.KeyLeft, 0)) // codex -> claude
	if got, want := m.PlanInput().AgentOptions, (agentopts.Values{"model": "fable"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("back on claude: AgentOptions = %v, want %v", got, want)
	}
}

// A submit that beats the sync must not send one kind's options with
// another: agentOptions refuses a row showing a kind other than the agent
// row's.
func TestOptions_AStaleRowSendsNothing(t *testing.T) {
	m := newTestModel(t, testSetup{Config: config.Config{Agents: config.AgentsConfig{
		Favorites: []string{"claude", "codex"},
		Options:   map[string]agentopts.Values{"claude": {"effort": "low"}},
	}}})
	m.agent.Update(key(tea.KeyRight, 0)) // codex, with no reactToChanges after it
	if got := m.PlanInput().AgentOptions; got != nil {
		t.Fatalf("AgentOptions = %v from a row still showing %q for agent %q", got, m.options.Kind(), m.agent.Value())
	}
}

// config.toml's refused entries are on the panel of the row they would
// have configured (spec §6.2).
func TestOptions_ConfigWarningsReachThePanel(t *testing.T) {
	const warning = `ignoring [agents.options.claude] effort = "ultra": expected one of low, medium, high, xhigh, max, or inherit`
	m := newTestModel(t, testSetup{Config: config.Config{Agents: config.AgentsConfig{OptionWarnings: []string{warning}}}})
	m.form.FocusByID("options")
	if frame := ansi.Strip(m.form.ViewAt(framePopupW, framePopupH)); !strings.Contains(frame, `ignoring [agents.options.claude] effort = "ultra"`) {
		t.Fatalf("the warning is not on the options panel:\n%s", frame)
	}
}

// The launch step says what the agent was started WITH, on both paths.
func TestSubmitStepDetail_NamesTheOptions(t *testing.T) {
	in := plan.Input{AgentKind: "claude", AgentOptions: agentopts.Values{"model": "opus", "permission_mode": "plan"}}
	start := plan.Op{Kind: plan.OpAgentStart, Agent: &herdrc.AgentStartReq{Name: "fix-login"}}
	if got, want := submitStepDetail(start, in), "fix-login · opus · plan mode"; got != want {
		t.Errorf("agent start detail = %q, want %q", got, want)
	}
	in.AccountPin = "alpha-1"
	launch := plan.Op{Kind: plan.OpClauthLaunch, RunArgv: []string{"clauth", "start", "alpha-1", "--"}}
	if got, want := submitStepDetail(launch, in), "under clauth alpha-1 · opus · plan mode"; got != want {
		t.Errorf("pinned launch detail = %q, want %q", got, want)
	}
	if got := submitStepDetail(start, plan.Input{AgentKind: "claude"}); got != "fix-login" {
		t.Errorf("with no options the detail = %q, want the name alone", got)
	}
}
