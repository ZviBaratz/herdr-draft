package create

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/gitx"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
	"github.com/ZviBaratz/herdr-draft/internal/machine"
)

var (
	savedDev      = herdrc.MachineInfo{ID: "af09", Label: "dev (EC2)", Target: "dev", Session: "default", Enabled: true}
	savedNanoclaw = herdrc.MachineInfo{ID: "fb47", Label: "nanoclaw", Target: "nanoclaw", Session: "default", Enabled: true}
)

// fakeProber stands in for machine.CLIProber: it records what it was
// asked and answers what the test set, so no ssh and no herdr runs.
type fakeProber struct {
	report machine.Report
	err    error
	hang   bool
	asked  []string
}

func (p *fakeProber) Probe(ctx context.Context, c machine.Choice, rel string) (machine.Report, error) {
	p.asked = append(p.asked, c.Info.ID+" "+rel)
	if p.hang {
		<-ctx.Done()
		return machine.Report{}, ctx.Err()
	}
	return p.report, p.err
}

// devClone is dev's clone of hub as the clone probe reports it.
func devClone() gitx.CloneProbe {
	return gitx.CloneProbe{
		Home: "/home/ubuntu", Dir: "/home/ubuntu/quantivly/hub", Exists: true, IsRepo: true,
		Origin: "https://github.com/quantivly/hub.git", Branches: []string{"main"},
	}
}

// machineHarness is a harness whose project is ~/quantivly/hub under the
// home /home/u, whose config.toml sends ~/quantivly to dev, and whose
// herdr has dev and nanoclaw saved -- the owner's own shape (machine spec
// §3.4).
func machineHarness(t *testing.T) (*harness, *fakeProber) {
	t.Helper()
	h := newHarness(t)
	writeConfig(t, h.env.ConfigDir, "[machine.paths]\n\"/home/u/quantivly\" = \"dev\"\n")
	h.deps.Workdir = func() (string, error) { return "/home/u/quantivly/hub", nil }
	h.deps.Home = func() string { return "/home/u" }
	h.git.commits = repoCommitsIn("/home/u/quantivly/hub")
	h.git.originHead = "origin/main"
	h.git.originURL = "git@github.com:quantivly/hub.git"
	h.git.originBranches = map[string]bool{"main": true}
	h.runner.machines = []herdrc.MachineInfo{savedDev, savedNanoclaw}
	p := &fakeProber{report: machine.Report{Clone: devClone()}}
	h.deps.Prober = p
	return h, p
}

func dryRunJSON(t *testing.T, h *harness) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal([]byte(h.stdout.String()), &got); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, h.stdout)
	}
	return got
}

// TestMachineRuleSendsTheSessionToDev is the end state machine spec §1 is
// measured against, headless: a project under ~/quantivly opens on dev with
// nothing on the command line saying so, every herdr call is routed there,
// and the space opens in the project's directory THERE.
func TestMachineRuleSendsTheSessionToDev(t *testing.T) {
	h, p := machineHarness(t)

	if code := h.run("--title", "fix it", "--no-worktree"); code != ExitOK {
		t.Fatalf("exit = %d\nstderr: %s", code, h.stderr)
	}
	if !slices.Equal(p.asked, []string{"af09 quantivly/hub"}) {
		t.Errorf("probe asked %q, want dev about quantivly/hub", p.asked)
	}
	// The only call before the scoping is the open-workspace tier's local
	// snapshot; nothing that creates may precede it.
	on := slices.Index(h.runner.calls, "On(af09)")
	if on < 0 || slices.ContainsFunc(h.runner.calls[:on], func(c string) bool { return c != "WorkspaceList()" }) {
		t.Fatalf("calls = %v, want everything but the local snapshot scoped to dev", h.runner.calls)
	}
	if !slices.Contains(h.runner.calls, "WorkspaceCreate(/home/ubuntu/quantivly/hub,fix it)") {
		t.Errorf("calls = %v, want the space opened in dev's clone", h.runner.calls)
	}
	if !strings.Contains(h.stdout.String(), `machine="dev (EC2)"`) {
		t.Errorf("stdout = %q, want the created line to say where", h.stdout)
	}
}

// TestMachineLocalKeepsTheSessionHere is the way to keep a session local
// (machine spec §1): --machine local, for a browser or a USB device.
func TestMachineLocalKeepsTheSessionHere(t *testing.T) {
	h, p := machineHarness(t)

	if code := h.run("--title", "fix it", "--no-worktree", "--machine", "local"); code != ExitOK {
		t.Fatalf("exit = %d\nstderr: %s", code, h.stderr)
	}
	if len(p.asked) != 0 {
		t.Errorf("a local session probed a machine: %q", p.asked)
	}
	for _, c := range h.runner.calls {
		if strings.HasPrefix(c, "On(") {
			t.Errorf("a local session scoped the runner: %v", h.runner.calls)
		}
	}
	if !slices.Contains(h.runner.calls, "WorkspaceCreate(/home/u/quantivly/hub,fix it)") {
		t.Errorf("calls = %v, want the space opened here", h.runner.calls)
	}
}

// TestMachineOnlyWhatConfigOffersIsAccepted is machine spec §4.2: a saved
// machine config.toml never names -- a production host -- is not reachable
// from the command line either, because every flag has a form control
// behind it and the form's row does not offer it.
func TestMachineOnlyWhatConfigOffersIsAccepted(t *testing.T) {
	h, _ := machineHarness(t)
	if code := h.run("--title", "fix it", "--machine", "nanoclaw"); code != ExitUsage {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitUsage, h.stderr)
	}
	if s := h.stderr.String(); !strings.Contains(s, `"nanoclaw"`) || !strings.Contains(s, "local, dev") {
		t.Errorf("stderr = %q, want the refusal to name what is offered", s)
	}
}

// TestMachineNamedButNotSavedIsRefused: a rule naming a machine herdr has
// no profile for is a usage error naming it, before anything is probed.
func TestMachineNamedButNotSavedIsRefused(t *testing.T) {
	h, p := machineHarness(t)
	h.runner.machines = []herdrc.MachineInfo{savedNanoclaw}
	if code := h.run("--title", "fix it"); code != ExitUsage {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitUsage, h.stderr)
	}
	if !strings.Contains(h.stderr.String(), `no saved herdr machine "dev"`) {
		t.Errorf("stderr = %q", h.stderr)
	}
	if len(p.asked) != 0 {
		t.Error("an unresolvable machine was probed")
	}
}

// TestMachineUnreachableIsExitThree is decision 4: refused with the reason,
// naming the way to keep the session here, and nothing falls back.
func TestMachineUnreachableIsExitThree(t *testing.T) {
	h, p := machineHarness(t)
	p.err = errors.Join(machine.ErrUnreachable, errors.New("dev (EC2) is unreachable: Token for dev is expired"))
	if code := h.run("--title", "fix it", "--no-worktree"); code != ExitUnreachable {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitUnreachable, h.stderr)
	}
	s := h.stderr.String()
	if !strings.Contains(s, "Token for dev is expired") || !strings.Contains(s, "--machine local") {
		t.Errorf("stderr = %q, want the reason and --machine local", s)
	}
	if h.runner.called("WorkspaceCreate") {
		t.Error("an unreachable machine's session was created somewhere")
	}
}

// TestMachineProbeThatNeverAnswersIsExitFive: unknown, not unreachable.
func TestMachineProbeThatNeverAnswersIsExitFive(t *testing.T) {
	h, p := machineHarness(t)
	p.hang = true
	h.deps.CheckDeadline = 20 * time.Millisecond
	if code := h.run("--title", "fix it", "--no-worktree"); code != ExitCheckTimedOut {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitCheckTimedOut, h.stderr)
	}
}

// TestMachineWithoutTheCloneIsExitTwo: the project is not on dev.
func TestMachineWithoutTheCloneIsExitTwo(t *testing.T) {
	h, p := machineHarness(t)
	p.report.Clone.Exists, p.report.Clone.IsRepo = false, false
	if code := h.run("--title", "fix it", "--no-worktree"); code != ExitUsage {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitUsage, h.stderr)
	}
	if !strings.Contains(h.stderr.String(), "~/quantivly/hub is not on dev (EC2)") {
		t.Errorf("stderr = %q", h.stderr)
	}
}

// TestMachineRefusesWhatOnlyThisMachineHas: the here placements, a named
// space and an account pin each need something dev does not have, and a
// flag that asks for one is refused rather than dropped (machine spec
// §6.2, §6.3). An account config.toml defaults to is dropped quietly: it
// was nobody's request for THIS session.
func TestMachineRefusesWhatOnlyThisMachineHas(t *testing.T) {
	for _, args := range [][]string{
		{"--placement", "tab-here"},
		{"--placement", "split-here"},
		{"--workspace", "w1"},
		{"--account", "quantivly-4"},
	} {
		h, _ := machineHarness(t)
		h.runner.workspaces = []herdrc.WorkspaceInfo{{WorkspaceID: "w1", Label: "hub"}}
		all := append([]string{"--title", "fix it", "--no-worktree"}, args...)
		if code := h.run(all...); code != ExitUsage {
			t.Errorf("%v: exit = %d, want %d\nstderr: %s", args, code, ExitUsage, h.stderr)
		}
		if !strings.Contains(h.stderr.String(), "dev (EC2)") {
			t.Errorf("%v: stderr = %q, want the refusal to name the machine", args, h.stderr)
		}
	}

	h, _ := machineHarness(t)
	writeConfig(t, h.env.ConfigDir, "[machine.paths]\n\"/home/u/quantivly\" = \"dev\"\n[clauth]\ndefault = \"quantivly-4\"\n")
	if code := h.run("--title", "fix it", "--no-worktree", "--dry-run", "--json"); code != ExitOK {
		t.Fatalf("a configured default account refused a remote session: %d\nstderr: %s", code, h.stderr)
	}
	if got := dryRunJSON(t, h); got["account"] != nil {
		t.Errorf("account = %v, want none on dev", got["account"])
	}
}

// TestMachineBaseIsOneDevCanFetch is machine spec §6.4, through create:
// HEAD becomes origin's default branch, a local branch origin also has
// becomes origin's copy, and one origin lacks is refused.
func TestMachineBaseIsOneDevCanFetch(t *testing.T) {
	h, _ := machineHarness(t)
	if code := h.run("--title", "fix it", "--worktree", "--dry-run", "--json"); code != ExitOK {
		t.Fatalf("exit = %d\nstderr: %s", code, h.stderr)
	}
	if got := dryRunJSON(t, h)["base"]; got != "origin/main" {
		t.Errorf("base = %v, want origin/main", got)
	}

	h, _ = machineHarness(t)
	h.git.fullRefs = map[string]string{"main": "refs/heads/main"}
	if code := h.run("--title", "fix it", "--worktree", "--base", "main", "--dry-run", "--json"); code != ExitOK {
		t.Fatalf("exit = %d\nstderr: %s", code, h.stderr)
	}
	if got := dryRunJSON(t, h)["base"]; got != "origin/main" {
		t.Errorf("base = %v, want origin/main", got)
	}

	h, _ = machineHarness(t)
	h.git.commits["/home/u/quantivly/hub local-only"] = strings.Repeat("c", 40)
	h.git.fullRefs = map[string]string{"local-only": "refs/heads/local-only"}
	if code := h.run("--title", "fix it", "--worktree", "--base", "local-only", "--dry-run"); code != ExitUsage {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitUsage, h.stderr)
	}
	if !strings.Contains(h.stderr.String(), "push it, or use local") {
		t.Errorf("stderr = %q", h.stderr)
	}
}

// TestMachineJSONSaysWhereAndWhy: --json names the machine and where the
// choice came from, so a caller can tell a rule's dev from its own flag.
func TestMachineJSONSaysWhereAndWhy(t *testing.T) {
	h, _ := machineHarness(t)
	if code := h.run("--title", "fix it", "--no-worktree", "--dry-run", "--json"); code != ExitOK {
		t.Fatalf("exit = %d\nstderr: %s", code, h.stderr)
	}
	got := dryRunJSON(t, h)
	m, _ := got["machine"].(map[string]any)
	if m["selector"] != "dev" || m["id"] != "af09" || m["label"] != "dev (EC2)" {
		t.Errorf("machine = %v", got["machine"])
	}
	if prov, _ := got["provenance"].(map[string]any); prov["machine"] != "config.toml" {
		t.Errorf("provenance.machine = %v, want config.toml", prov["machine"])
	}

	h, _ = machineHarness(t)
	if code := h.run("--title", "fix it", "--no-worktree", "--dry-run", "--json", "--machine", "local"); code != ExitOK {
		t.Fatalf("exit = %d\nstderr: %s", code, h.stderr)
	}
	got = dryRunJSON(t, h)
	if m, _ := got["machine"].(map[string]any); m["selector"] != "local" || m["id"] != nil {
		t.Errorf("machine = %v, want local with no id", got["machine"])
	}
	if prov, _ := got["provenance"].(map[string]any); prov["machine"] != "flag" {
		t.Errorf("provenance.machine = %v, want flag", prov["machine"])
	}
}

// TestMachineBranchOnlyDevHasIsTaken: herdr checks out an existing local
// branch and ignores the base, so a name dev's clone already has is
// refused like one the laptop has (machine spec §7).
func TestMachineBranchOnlyDevHasIsTaken(t *testing.T) {
	h, p := machineHarness(t)
	p.report.Clone.Branches = []string{"main", "zvi/fix-it"}
	h.git.commits = repoCommitsIn("/home/u/quantivly/hub")
	if code := h.run("--title", "fix it", "--worktree", "--branch", "zvi/fix-it"); code != ExitUsage {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitUsage, h.stderr)
	}
	if !strings.Contains(h.stderr.String(), "on dev (EC2)") {
		t.Errorf("stderr = %q, want the refusal to say where the branch is", h.stderr)
	}
}

// TestMachineLaneFollowsItsRepository: a lane under ~/.herdr/worktrees is
// matched by its repository's key (machine spec §5.2), and carried to dev
// as that repository's primary checkout.
func TestMachineLaneFollowsItsRepository(t *testing.T) {
	h, p := machineHarness(t)
	lane := "/home/u/.herdr/worktrees/hub/zvi-x"
	h.deps.Workdir = func() (string, error) { return lane, nil }
	h.git.roots = map[string]string{lane: "/home/u/quantivly/hub"}
	h.git.primary = map[string]string{lane: "/home/u/quantivly/hub"}
	h.git.commits = repoCommitsIn(lane)
	if code := h.run("--title", "fix it", "--worktree", "--dry-run", "--json"); code != ExitOK {
		t.Fatalf("exit = %d\nstderr: %s", code, h.stderr)
	}
	if !slices.Equal(p.asked, []string{"af09 quantivly/hub"}) {
		t.Errorf("probe asked %q, want the lane's repository", p.asked)
	}
}

// TestMachineIsNeverRemembered is decision 2: a remote session leaves the
// project's remembered placement as it was, and neither memory file learns
// a machine.
func TestMachineIsNeverRemembered(t *testing.T) {
	h, _ := machineHarness(t)
	prior := config.Projects{Version: 1, Entries: map[string]config.ProjectDefaults{
		"/home/u/quantivly/hub": {Kind: "claude", Placement: "tab-here"},
	}}
	if err := config.SaveProjects(h.env.StateDir, prior); err != nil {
		t.Fatal(err)
	}
	if code := h.run("--title", "fix it", "--no-worktree"); code != ExitOK {
		t.Fatalf("exit = %d\nstderr: %s", code, h.stderr)
	}
	projects, err := config.LoadProjects(h.env.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := projects.Get("/home/u/quantivly/hub"); got.Placement != "tab-here" {
		t.Errorf("remembered placement = %q, want tab-here left alone", got.Placement)
	}
	for _, f := range []string{"projects.json", "last-used.json"} {
		b, _ := os.ReadFile(filepath.Join(h.env.StateDir, f))
		if strings.Contains(string(b), "machine") || strings.Contains(string(b), "dev") {
			t.Errorf("%s learned the machine: %s", f, b)
		}
	}
}

// TestMachineAcceptsAnyNameForTheOfferedMachine: `create --json` hands back
// the machine's label and id, and an agent that passes either back must
// reach the same machine config.toml's `dev` names.
func TestMachineAcceptsAnyNameForTheOfferedMachine(t *testing.T) {
	for _, sel := range []string{"dev (EC2)", "af09"} {
		h, _ := machineHarness(t)
		if code := h.run("--title", "fix it", "--no-worktree", "--dry-run", "--json", "--machine", sel); code != ExitOK {
			t.Fatalf("--machine %q: exit %d\nstderr: %s", sel, code, h.stderr)
		}
		if m, _ := dryRunJSON(t, h)["machine"].(map[string]any); m["id"] != "af09" {
			t.Errorf("--machine %q: machine = %v", sel, m)
		}
	}
}

// TestMachineRuleItCannotUseIsSaidOnStderr: config.Load drops a rule it
// cannot use with a warning, and the warning has to reach someone -- a
// silently dropped `quantivly = "dev"` would send every work session to
// this machine with nothing saying why (machine spec §5.1).
func TestMachineRuleItCannotUseIsSaidOnStderr(t *testing.T) {
	h, _ := machineHarness(t)
	writeConfig(t, h.env.ConfigDir, "[machine.paths]\n\"quantivly\" = \"dev\"\n")
	if code := h.run("--title", "fix it", "--no-worktree", "--dry-run"); code != ExitOK {
		t.Fatalf("exit = %d\nstderr: %s", code, h.stderr)
	}
	if !strings.Contains(h.stderr.String(), `ignoring [machine.paths] "quantivly": not an absolute path`) {
		t.Errorf("stderr = %q, want the dropped rule named", h.stderr)
	}
}

// TestMachineDryRunReportsNoLaptopAccountUsage: account_usage is how full
// the account the session would bill is, and a session on another machine
// bills that machine's own login -- so the laptop's clauth windows would be
// a number about the wrong account, which the spawn skill tells agents to
// act on (independent review #3).
func TestMachineDryRunReportsNoLaptopAccountUsage(t *testing.T) {
	h, _ := machineHarness(t)
	h.deps.Clauth = &fakeClauth{status: usageStatus()}
	if code := h.run("--title", "fix it", "--no-worktree", "--dry-run", "--json"); code != ExitOK {
		t.Fatalf("exit = %d\nstderr: %s", code, h.stderr)
	}
	if got := dryRunJSON(t, h); got["account_usage"] != nil {
		t.Errorf("account_usage = %v for a session on dev", got["account_usage"])
	}
	// The control: the same dry run kept here reports the laptop's.
	h, _ = machineHarness(t)
	h.deps.Clauth = &fakeClauth{status: usageStatus()}
	if code := h.run("--title", "fix it", "--no-worktree", "--dry-run", "--json", "--machine", "local"); code != ExitOK {
		t.Fatalf("exit = %d\nstderr: %s", code, h.stderr)
	}
	if got := dryRunJSON(t, h); got["account_usage"] == nil {
		t.Error("the local control reports no account_usage, so the test proves nothing")
	}
}
