package plan

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ZviBaratz/herdr-draft/internal/agentopts"
)

// optionsInput is a claude plan with two options set, over an extra_args
// that passes one of the same flags and one of its own.
func optionsInput() Input {
	in := validInput()
	in.ExtraArgs = []string{"--model", "sonnet", "--verbose"}
	in.AgentOptions = agentopts.Values{"model": "claude-opus-5[1m]", "effort": "high"}
	return in
}

// Path A (agent-options spec §5.1): `herdr agent start ... -- <args>`, an
// argv vector. The chosen model displaces extra_args' own --model rather
// than following it.
func TestBuild_OptionsReachAgentStart(t *testing.T) {
	ops, err := Build(optionsInput())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got := findOp(t, ops, OpAgentStart).Agent.ExtraArgs
	want := []string{"--verbose", "--model", "claude-opus-5[1m]", "--effort", "high"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AgentStart ExtraArgs = %q, want %q", got, want)
	}
}

// Path B, the launcher template: the same argument list after the
// template. Unquoted here -- CLIRunner.PaneRun quotes for the shell (#72).
func TestBuild_OptionsReachThePinnedLauncher(t *testing.T) {
	in := optionsInput()
	in.AccountPin = "work"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got := strings.Join(findOp(t, ops, OpClauthLaunch).RunArgv, " ")
	if want := "clauth start work -- --verbose --model claude-opus-5[1m] --effort high"; got != want {
		t.Fatalf("RunArgv = %q, want %q", got, want)
	}
}

// Path B, wrapper mode: `claude <args>` under CLAUDE_CONFIG_DIR.
func TestBuild_OptionsReachTheWrapperLaunch(t *testing.T) {
	in := optionsInput()
	in.AccountPin = "work"
	in.AccountLaunch = LaunchWrapper
	in.AccountConfigDir = "/dirs/work"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got := strings.Join(findOp(t, ops, OpClauthLaunch).RunArgv, " ")
	if want := "claude --verbose --model claude-opus-5[1m] --effort high"; got != want {
		t.Fatalf("RunArgv = %q, want %q", got, want)
	}
}

// Build refuses an option the kind does not declare, the way it refuses an
// account pin on a kind that cannot be pinned: neither caller can produce
// one, so one arriving means a caller built the Input by hand.
func TestBuild_RefusesOptionsTheKindDoesNotDeclare(t *testing.T) {
	in := validInput()
	in.AgentKind = "codex"
	in.AgentOptions = agentopts.Values{"model": "o3"}
	if _, err := Build(in); err == nil || !strings.Contains(err.Error(), "codex declares no model option") {
		t.Fatalf("Build error = %v, want the undeclared option named", err)
	}

	in = validInput()
	in.AgentOptions = agentopts.Values{"permission_mode": "bypassPermissions"}
	if _, err := Build(in); err == nil || !strings.Contains(err.Error(), "permission_mode") {
		t.Fatalf("Build error = %v, want the refused value named", err)
	}
}

// The `sh -c` launcher strands options exactly as it strands extra_args
// (TestLaunchOps_ShCLauncherStrandsExtraArgs): they follow the whole
// template. Pinned so that a change here is a decision, not an accident.
func TestBuild_ShCLauncherStrandsOptionsToo(t *testing.T) {
	in := validInput()
	in.AccountPin = "work"
	in.Launcher = []string{"sh", "-c", "exec claude-as {account}"}
	in.AgentOptions = agentopts.Values{"effort": "max"}
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got := findOp(t, ops, OpClauthLaunch).RunArgv
	want := []string{"sh", "-c", "exec claude-as work", "--effort", "max"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RunArgv = %q, want %q", got, want)
	}
}
