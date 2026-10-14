package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/form"
	"github.com/ZviBaratz/herdr-draft/internal/gitx"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
	"github.com/ZviBaratz/herdr-draft/internal/machine"
	"github.com/ZviBaratz/herdr-draft/internal/plan"
	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

var savedDev = herdrc.MachineInfo{ID: "af09", Label: "dev (EC2)", Target: "dev", Session: "default", Enabled: true}

// machineRunner is fakeRunner with saved machines and a toast that is
// recorded rather than posted.
type machineRunner struct {
	*fakeRunner
	saved   []herdrc.MachineInfo
	toasts  []string
	scoped  []string
	listErr error
}

func (r *machineRunner) On(id string) herdrc.Runner {
	r.scoped = append(r.scoped, id)
	return r
}
func (r *machineRunner) MachineList(context.Context) ([]herdrc.MachineInfo, error) {
	return r.saved, r.listErr
}
func (r *machineRunner) NotificationShow(_ context.Context, title, _ string) error {
	r.toasts = append(r.toasts, title)
	return nil
}

// fakeMachineProber answers the probe with what a test set, and records
// what it was asked.
type fakeMachineProber struct {
	report machine.Report
	err    error
	hold   chan struct{}
	asked  []string
}

func (p *fakeMachineProber) Probe(ctx context.Context, c machine.Choice, rel string) (machine.Report, error) {
	p.asked = append(p.asked, c.Info.ID+" "+rel)
	if p.hold != nil {
		select {
		case <-p.hold:
		case <-ctx.Done():
			return machine.Report{}, ctx.Err()
		}
	}
	return p.report, p.err
}

func devReport() machine.Report {
	return machine.Report{Clone: gitx.CloneProbe{
		Home: "/home/ubuntu", Dir: "/home/ubuntu/quantivly/hub", Exists: true, IsRepo: true,
		Branches: []string{"main"},
	}}
}

// machineModel is a popup opened in ~/quantivly/hub, with config.toml
// sending ~/quantivly to dev and herdr knowing dev -- the owner's own shape.
func machineModel(t *testing.T) (Model, *machineRunner, *fakeMachineProber, *fakeGit) {
	t.Helper()
	runner := &machineRunner{fakeRunner: &fakeRunner{}, saved: []herdrc.MachineInfo{savedDev}}
	prober := &fakeMachineProber{report: devReport()}
	git := newFakeGit()
	git.dirExists, git.isGitRepo = true, true
	git.originHead = "origin/main"
	git.originBranches = map[string]bool{"main": true}
	cfg := config.Config{
		DefaultWorktree: true,
		Agents:          config.AgentsConfig{Favorites: []string{"claude"}},
		Machine: config.MachineConfig{
			Configured: true,
			Rules:      []config.MachineRule{{Dir: "/home/zvi/quantivly", Selector: "dev"}},
		},
	}
	m := New(Setup{
		Deps: Deps{
			Runner: runner,
			Git:    git,
			Clock:  noSleep,
			Prober: prober,
		},
		Ctx:      herdrc.Context{WorkspaceCwd: "/home/zvi/quantivly/hub"},
		Config:   cfg,
		Palette:  theme.Default(),
		StateDir: t.TempDir(),
		HomeDir:  testHomeDir,
	})
	return m, runner, prober, git
}

// TestMachineRowOnlyWithATable: no [machine] table, no row -- the popup
// everyone else sees is the one they saw before (machine spec §4.2).
func TestMachineRowOnlyWithATable(t *testing.T) {
	m := newTestModel(t, testSetup{})
	if m.machine != nil {
		t.Fatal("a machine row was built with no [machine] table")
	}
	mm, _, _, _ := machineModel(t)
	if mm.machine == nil {
		t.Fatal("no machine row with a [machine] table")
	}
}

// TestMachineRuleOpensOnDevAndProbesIt is the end state machine spec §1
// is measured against, in the popup: the project's rule selects dev before
// anyone touches anything, the probe runs once the project and the machine
// list are known, and the plan the popup would build goes to dev's clone,
// in a new space there, under dev's own login.
func TestMachineRuleOpensOnDevAndProbesIt(t *testing.T) {
	m, _, prober, _ := machineModel(t)
	if got := m.machine.Value(); got != "dev" {
		t.Fatalf("machine = %q before the first check, want dev from the rule", got)
	}
	m = settle(t, m)
	if strings.Join(prober.asked, ",") != "af09 quantivly/hub" {
		t.Errorf("probe asked %q, want dev about quantivly/hub", prober.asked)
	}
	if m.machineCheckPending() {
		t.Fatal("the probe never landed")
	}
	if row := fieldText(m.machine, 60); !strings.Contains(row, "dev (EC2)") || !strings.Contains(row, "ready") {
		t.Errorf("machine row and panel = %q, want dev (EC2) ready", row)
	}
	if m.placement.Enabled() || (m.account != nil && m.account.Enabled()) {
		t.Error("placement or account is choosable for a session on dev")
	}
	// The worktree alone makes placement inert, so the row's own words are
	// what show it knows where the space is.
	if row := fieldText(m.placement, 60); !strings.Contains(row, "on dev (EC2)") {
		t.Errorf("placement row = %q, want it to say the space is on dev", row)
	}
	in := m.buildPlanInput()
	if in.Machine.ID != "af09" || in.Machine.Dir != "/home/ubuntu/quantivly/hub" || in.Machine.Target != "dev" {
		t.Errorf("plan machine = %+v", in.Machine)
	}
	if in.Placement != plan.PlacementNewSpace || in.AccountPin != "" {
		t.Errorf("placement, account = %v, %q; want a new space and no pin", in.Placement, in.AccountPin)
	}
}

// TestMachineCycledToLocalStaysLocal: one ← keeps a session here, and a
// re-resolution of the rules never moves it back (machine spec §9.3).
func TestMachineCycledToLocalStaysLocal(t *testing.T) {
	m, _, _, _ := machineModel(t)
	m = settle(t, m)
	_ = m.form.FocusByID("machine")
	next, _ := m.Update(key(tea.KeyLeft, 0))
	m = next.(Model)
	if m.machine.Value() != config.LocalMachine {
		t.Fatalf("machine = %q after ←", m.machine.Value())
	}
	m.applyProjectDefaults(m.dir.Value(), "/home/zvi/quantivly/hub", true, config.RepoConfig{})
	if m.machine.Value() != config.LocalMachine {
		t.Errorf("the rule moved a machine the user chose: %q", m.machine.Value())
	}
	if in := m.buildPlanInput(); in.Machine.Remote() {
		t.Errorf("a local choice built a remote plan: %+v", in.Machine)
	}
}

// TestMachineUnreachableRefusesTheSubmit is decision 4 in the popup: the
// row says why in herdr's or ssh's words, ⌃S refuses, and the focus lands
// on the row where local is one keystroke away.
func TestMachineUnreachableRefusesTheSubmit(t *testing.T) {
	m, _, prober, _ := machineModel(t)
	prober.err = errors.Join(machine.ErrUnreachable, errors.New("dev (EC2) is unreachable: Token for dev is expired"))
	m = settle(t, m)
	m.title.SetTitle("fix it", false)
	m.worktree.SetBranch("zvi/fix-it", false)
	m = landTitle(t, m, []tea.Cmd{m.scheduleTitleCheck(m.title.Value(), m.worktree.Branch(), m.dir.Value(), m.worktree.On(), m.remoteSelected())})
	m = submitAndLandTheReask(t, m)
	if m.submitView != nil {
		t.Fatal("a submit to an unreachable machine started")
	}
	if got := m.form.FocusedID(); got != "machine" {
		t.Errorf("focus = %q, want the machine row", got)
	}
	// Fields, because the panel wraps the sentence (machine spec §3.4).
	if text := strings.Join(strings.Fields(fieldText(m.machine, 80)), " "); !strings.Contains(text, "Token for dev is expired") || !strings.Contains(text, "unreachable") {
		t.Errorf("machine row and panel = %q", text)
	}
}

// TestMachineSubmitWaitsForTheProbe: a ⌃S while the probe is out is held,
// and goes on by itself when it lands (machine spec §9.4). The probe is
// sent out again by cycling the row away and back, and its command kept
// unrun until the submit has been held.
func TestMachineSubmitWaitsForTheProbe(t *testing.T) {
	m, _, _, _ := machineModel(t)
	m = settle(t, m)
	m.title.SetTitle("fix it", false)
	_ = m.form.FocusByID("machine")
	next, out := m.Update(key(tea.KeyLeft, 0))
	m = landMachineTitleCheck(t, next.(Model), out)
	next, out = m.Update(key(tea.KeyRight, 0))
	m = landMachineTitleCheck(t, next.(Model), out)
	if m.machine.Value() != "dev" || !m.machineCheckPending() {
		t.Fatalf("machine = %q, pending = %v; want dev with its probe out", m.machine.Value(), m.machineCheckPending())
	}

	next, _ = m.Update(form.SubmitMsg{})
	m = next.(Model)
	if !m.submitHeld {
		t.Fatal("a submit with the probe out was not held")
	}
	result, ok := findMsg[machineResultMsg](t, out)
	if !ok {
		t.Fatal("cycling back to dev scheduled no probe")
	}
	next, _ = m.Update(result)
	m = next.(Model)
	if m.submitHeld || m.machineCheckPending() {
		t.Error("the submit stayed held after the probe landed")
	}
}

// landMachineTitleCheck lands the title-duplicate check a routed message
// scheduled -- the machine row is part of its question (machine spec §7) --
// so a submit after it is held for the probe alone.
func landMachineTitleCheck(t *testing.T, m Model, out tea.Cmd) Model {
	t.Helper()
	debounce, ok := findMsg[titleDebounceMsg](t, out)
	if !ok {
		return m
	}
	next, run := m.Update(debounce)
	m = next.(Model)
	if result, ok := findMsg[titleResultMsg](t, run); ok {
		next, _ = m.Update(result)
		m = next.(Model)
	}
	return m
}

// findMsg runs cmd's tree, each command with a short bound so a blink that
// never returns is skipped, and returns the first message of type T.
func findMsg[T tea.Msg](t *testing.T, cmd tea.Cmd) (T, bool) {
	t.Helper()
	var zero T
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		done := make(chan tea.Msg, 1)
		go func() { done <- c() }()
		select {
		case msg := <-done:
			if batch, ok := msg.(tea.BatchMsg); ok {
				queue = append(queue, batch...)
				continue
			}
			if v, ok := msg.(T); ok {
				return v, true
			}
		case <-time.After(200 * time.Millisecond):
		}
	}
	return zero, false
}

// TestMachineRemoteBaseIsOneDevCanFetch: a remote worktree's base is
// mapped at submit (machine spec §6.4) -- HEAD becomes origin's default
// branch -- and memory keeps the row's own base.
func TestMachineRemoteBaseIsOneDevCanFetch(t *testing.T) {
	m, _, _, _ := machineModel(t)
	m = settle(t, m)
	if !m.needsRemoteBase() {
		t.Fatal("a remote worktree session does not map its base")
	}
	base, err := m.ResolveRemoteBase(context.Background())
	if err != nil || base != "origin/main" {
		t.Fatalf("ResolveRemoteBase = %q, %v; want origin/main", base, err)
	}
	if in := m.WithRemoteBase(base).buildPlanInput(); in.BaseRef != "origin/main" || in.Linked != (plan.LinkedCheckout{}) {
		t.Errorf("plan base, linked = %q, %+v", in.BaseRef, in.Linked)
	}
}

// TestMachineBranchOnlyDevHasIsRefused: herdr checks out an existing local
// branch and ignores the base, so a name dev's clone already has is refused
// at submit, on the worktree row (machine spec §7).
func TestMachineBranchOnlyDevHasIsRefused(t *testing.T) {
	m, _, prober, _ := machineModel(t)
	prober.report.Clone.Branches = []string{"main", "zvi/fix-it"}
	m = settle(t, m)
	m.title.SetTitle("fix it", false)
	m.worktree.SetBranch("zvi/fix-it", false)
	if where := m.machineRefusal(); where != "worktree" {
		t.Errorf("machineRefusal = %q, want worktree", where)
	}
}

// TestMachineToastSaysWhereTheSessionWent: herdr cannot switch the
// client's view to dev, so a remote create posts a toast here before the
// popup closes (machine spec §9.5); a local one does not.
func TestMachineToastSaysWhereTheSessionWent(t *testing.T) {
	m, runner, _, _ := machineModel(t)
	m.submitInput = plan.Input{Title: "fix it", Machine: plan.Machine{ID: "af09", Label: "dev (EC2)", Dir: "/x"}}
	m.persistStateCmd()()
	if strings.Join(runner.toasts, ",") != "fix it opened on dev (EC2)" {
		t.Errorf("toasts = %q", runner.toasts)
	}
	runner.toasts = nil
	m.submitInput = plan.Input{Title: "fix it"}
	m.persistStateCmd()()
	if len(runner.toasts) != 0 {
		t.Errorf("a local create posted %q", runner.toasts)
	}
}

// TestMachineWaitingHintSaysWhereTheDialogIs: the trust dialog of a remote
// session is on dev's screen (machine spec §8.5).
func TestMachineWaitingHintSaysWhereTheDialogIs(t *testing.T) {
	in := plan.Input{Prompt: "go", Machine: plan.Machine{ID: "af09", Label: "dev (EC2)"}}
	if got := submitWaitingHint(in); !strings.HasPrefix(got, "answer the dialog on dev (EC2)") {
		t.Errorf("hint = %q", got)
	}
}

// machineFrameModel is the full frame fixture (issue and account rows
// too) opened in ~/quantivly/hub under a rule sending it to dev, settled:
// the project checked, the machine list read, the probe answered. Ten rows
// at the popup's own 101x30 (machine spec §9.6).
func machineFrameModel(t *testing.T, prober *fakeMachineProber) Model {
	t.Helper()
	return machineFrameModelWith(t, prober)
}

// refusingForwarding is a herdr whose machine-scoped calls fail the way
// 0.9.1 fails against dev's 0.9.0 server (machine spec §3.4).
type refusingForwarding struct{ fakeRunner }

func (r *refusingForwarding) On(string) herdrc.Runner { return r }
func (r *refusingForwarding) WorkspaceList(context.Context) ([]herdrc.WorkspaceInfo, error) {
	return nil, errors.New("machine 'dev (EC2)': remote Herdr does not support machine API forwarding; update Herdr on this machine")
}

func machineFrameModelWith(t *testing.T, prober machine.Prober) Model {
	t.Helper()
	setup := frameSetup(true)
	setup.Ctx.WorkspaceCwd = "/home/zvi/quantivly/hub"
	setup.Config.DefaultWorktree = true
	setup.Config.Machine = config.MachineConfig{
		Configured: true,
		Rules:      []config.MachineRule{{Dir: "/home/zvi/quantivly", Selector: "dev"}},
	}
	setup.Machines = []herdrc.MachineInfo{savedDev}
	setup.Prober = prober
	git := newFakeGit()
	git.dirExists, git.isGitRepo = true, true
	git.originHead = "origin/main"
	setup.Git = git
	m := settle(t, newTestModel(t, setup))
	m.title.SetTitle("fix login redirect", false)
	m.reactToChanges()
	return m
}

// TestAssembledForm_MachineReady: dev chosen by the rule and ready, with
// the placement and account rows saying what they are on dev, at the
// popup's size -- the ten-row stack taking the card's inset (§9.6).
func TestAssembledForm_MachineReady(t *testing.T) {
	m := machineFrameModel(t, &fakeMachineProber{report: devReport()})
	m.form.FocusByID("machine")
	m.reactToChanges()
	assertAppFrame(t, "assembled-machine-ready-101x30", m, framePopupW, framePopupH)
}

// TestAssembledForm_MachineChecking: the probe still out.
func TestAssembledForm_MachineChecking(t *testing.T) {
	m := machineFrameModel(t, &fakeMachineProber{report: devReport()})
	m.form.FocusByID("machine")
	m.mach.asked, m.mach.landed = "", false
	m.machine.SetStatus(form.MachineChecking, "checking…", "checking dev (EC2)…")
	assertAppFrame(t, "assembled-machine-checking-101x30", m, framePopupW, framePopupH)
}

// TestAssembledForm_MachineUnreachable: herdr's own refusal of dev's 0.9.0
// server, verbatim (machine spec §3.4), after a ⌃S it refused.
func TestAssembledForm_MachineUnreachable(t *testing.T) {
	// Through machine.CLIProber, so the sentence is the one production
	// composes from herdr's own words.
	m := machineFrameModelWith(t, machine.CLIProber{
		Runner: &refusingForwarding{},
		Clone: func(context.Context, string, string) (gitx.CloneProbe, error) {
			return devReport().Clone, nil
		},
	})
	m = landTitle(t, m, []tea.Cmd{m.scheduleTitleCheck(m.title.Value(), m.worktree.Branch(), m.dir.Value(), m.worktree.On(), m.remoteSelected())})
	m = submitAndLandTheReask(t, m)
	if m.form.FocusedID() != "machine" {
		t.Fatalf("focus = %q, want the machine row the refusal sends it to", m.form.FocusedID())
	}
	assertAppFrame(t, "assembled-machine-unreachable-101x30", m, framePopupW, framePopupH)
}

// TestAssembledForm_MachineCycledToLocal: one ← from dev, and placement
// and account are choosable again.
func TestAssembledForm_MachineCycledToLocal(t *testing.T) {
	m := machineFrameModel(t, &fakeMachineProber{report: devReport()})
	m.form.FocusByID("machine")
	next, _ := m.Update(key(tea.KeyLeft, 0))
	m = next.(Model)
	assertAppFrame(t, "assembled-machine-local-101x30", m, framePopupW, framePopupH)
}

// TestAssembledForm_MachineWorktreeBase: the worktree panel on dev says
// what the row's `← HEAD` means there -- origin's default branch, cut after
// a fetch there (machine spec §6.4).
func TestAssembledForm_MachineWorktreeBase(t *testing.T) {
	m := machineFrameModel(t, &fakeMachineProber{report: devReport()})
	m.form.FocusByID("worktree")
	m.reactToChanges()
	if text := strings.Join(strings.Fields(fieldText(m.worktree, 100)), " "); !strings.Contains(text, "on dev (EC2): cut from origin's default branch, after a fetch there") {
		t.Errorf("worktree panel = %q", text)
	}
	assertAppFrame(t, "assembled-machine-worktree-101x30", m, framePopupW, framePopupH)
}

// TestMachineRowShowsTheRulesItDropped: the popup's half of §5.1's "never
// silently".
func TestMachineRowShowsTheRulesItDropped(t *testing.T) {
	setup := testSetup{Config: config.Config{Machine: config.MachineConfig{
		Configured: true, Warnings: []string{`ignoring [machine.paths] "quantivly": not an absolute path`},
	}}}
	m := newTestModel(t, setup)
	if text := fieldText(m.machine, 100); !strings.Contains(text, `ignoring [machine.paths] "quantivly"`) {
		t.Errorf("machine panel = %q", text)
	}
}

// TestMachinePlanFailsClosed: with another machine selected, a verdict that
// is a refusal or an unknown must never build a LOCAL plan -- plan.Build
// refuses a remote plan with no directory, which is decision 4's floor.
func TestMachinePlanFailsClosed(t *testing.T) {
	m, _, _, _ := machineModel(t)
	m = settle(t, m)
	m.mach.verdict = machineVerdict{unknown: true}
	in := m.buildPlanInput()
	if !in.Machine.Remote() {
		t.Fatalf("a remote selection with an unknown verdict built a local plan: %+v", in.Machine)
	}
	if _, err := plan.Build(in); err == nil {
		t.Error("plan.Build accepted a remote plan with no directory")
	}
}

// TestMachineProbeDeadlineIsUnknown: a probe the deadline cut off reads as
// "no answer", not as a refusal in context's words (#202).
func TestMachineProbeDeadlineIsUnknown(t *testing.T) {
	m, _, prober, _ := machineModel(t)
	prober.err = context.DeadlineExceeded
	m = settle(t, m)
	if !m.mach.verdict.unknown {
		t.Errorf("verdict = %+v, want unknown", m.mach.verdict)
	}
	if text := fieldText(m.machine, 80); !strings.Contains(text, "no answer") || strings.Contains(text, "deadline") {
		t.Errorf("machine row = %q", text)
	}
}

// TestMachineListTimeoutIsUnknown: a machine list that did not answer is
// not a herdr too old for machines.
func TestMachineListTimeoutIsUnknown(t *testing.T) {
	m, runner, _, _ := machineModel(t)
	runner.listErr = errCheckTimedOut
	m = settle(t, m)
	if !m.mach.verdict.unknown {
		t.Errorf("verdict = %+v, want unknown", m.mach.verdict)
	}
	if text := fieldText(m.machine, 80); strings.Contains(text, "0.9.1") {
		t.Errorf("a timeout is blamed on herdr's version: %q", text)
	}
}

// TestMachineSubmitResumesWhenTheListLands: a ⌃S pressed before herdr's
// machine list answered is held, and the list landing -- even with a
// verdict it settles synchronously -- resumes it rather than dropping it.
func TestMachineSubmitResumesWhenTheListLands(t *testing.T) {
	m, runner, _, _ := machineModel(t)
	runner.listErr = errors.New("unknown command: machine")
	m.title.SetTitle("fix it", false)
	m.worktree.SetBranch("zvi/fix-it", false)
	// Land everything but the machine list.
	var list machinesListedMsg
	for _, cmd := range m.initCmds {
		if cmd == nil {
			continue
		}
		if msg, ok := cmd().(machinesListedMsg); ok {
			list = msg
			continue
		}
	}
	m = pumpAsync(t, m, initCmdsExcept[machinesListedMsg](m))
	m = landTitle(t, m, []tea.Cmd{m.scheduleTitleCheck(m.title.Value(), m.worktree.Branch(), m.dir.Value(), m.worktree.On(), m.remoteSelected())})
	next, _ := m.Update(form.SubmitMsg{})
	m = next.(Model)
	if !m.submitHeld {
		t.Fatal("a submit before the machine list was not held")
	}
	next, _ = m.Update(list)
	m = next.(Model)
	if m.submitHeld {
		t.Error("the list landing left the submit held")
	}
	if got := m.form.FocusedID(); got != "machine" {
		t.Errorf("focus = %q, want the machine row the refusal sends it to", got)
	}
}

// initCmdsExcept is m's Init commands without the one answering T.
func initCmdsExcept[T tea.Msg](m Model) []tea.Cmd {
	var out []tea.Cmd
	for _, cmd := range m.initCmds {
		if cmd == nil {
			continue
		}
		c := cmd
		msg := c()
		if _, skip := msg.(T); skip {
			continue
		}
		out = append(out, func() tea.Msg { return msg })
	}
	return out
}

// TestMachineVerdictFollowsTheWorktree: "can't host it" for a worktree in
// a plain directory stops being true when the worktree goes off.
func TestMachineVerdictFollowsTheWorktree(t *testing.T) {
	m, _, prober, _ := machineModel(t)
	prober.report.Clone.IsRepo = false
	m = settle(t, m)
	if text := fieldText(m.machine, 80); !strings.Contains(text, "can't host it") {
		t.Fatalf("machine row = %q, want can't host it", text)
	}
	m.worktree.SetOn(false)
	m.reactToChanges()
	if text := fieldText(m.machine, 80); strings.Contains(text, "can't host it") {
		t.Errorf("machine row still refuses after the worktree went off: %q", text)
	}
}

// TestMachineSkipsWhatOnlyThisMachineNeeds: on another machine, the
// account picker's preview (no account applies there) and this clone's
// base-behind note (the cut is made there, after a fetch) are not asked --
// with every other precondition for both in place, which the local half
// of the test proves: there, both are.
func TestMachineSkipsWhatOnlyThisMachineNeeds(t *testing.T) {
	m, _, _, _ := machineModel(t)
	m = settle(t, m)
	m.deps.Picker = &fakePicker{}
	m.account = form.NewAccountField(theme.Default())
	m.worktree.SetBaseItems(1, []string{"main"})
	m.worktree.SetBase("main")
	if m.worktree.Base() != "main" || !m.worktree.On() {
		t.Fatalf("fixture: base %q, worktree %v", m.worktree.Base(), m.worktree.On())
	}
	if cmd := m.schedulePickerPreview(m.dir.Value()); cmd != nil {
		t.Error("the account picker's preview was scheduled for a session on dev")
	}
	if cmd := m.scheduleBaseBehind(true); cmd != nil {
		t.Error("this clone's base-behind note was scheduled for a session on dev")
	}

	_ = m.form.FocusByID("machine")
	next, out := m.Update(key(tea.KeyLeft, 0))
	m = next.(Model)
	if m.remoteSelected() {
		t.Fatal("fixture: ← did not select local")
	}
	if _, ok := findMsg[pickerDebounceMsg](t, out); !ok {
		t.Error("going back to local did not ask the picker for this project again")
	}
	if cmd := m.scheduleBaseBehind(true); cmd == nil {
		t.Error("a local session's base-behind note was not scheduled")
	}
}

// TestAssembledForm_MachineNoClone: dev answered, and the project is not
// there (machine spec §7).
func TestAssembledForm_MachineNoClone(t *testing.T) {
	report := devReport()
	report.Clone.Exists, report.Clone.IsRepo = false, false
	m := machineFrameModel(t, &fakeMachineProber{report: report})
	m.form.FocusByID("machine")
	m.reactToChanges()
	assertAppFrame(t, "assembled-machine-no-clone-101x30", m, framePopupW, framePopupH)
}

// TestAssembledForm_MachineTimedOut: the probe did not answer -- unknown,
// in its own words, never a refusal's (#202).
func TestAssembledForm_MachineTimedOut(t *testing.T) {
	m := machineFrameModel(t, &fakeMachineProber{err: context.DeadlineExceeded})
	m.form.FocusByID("machine")
	m.reactToChanges()
	assertAppFrame(t, "assembled-machine-timed-out-101x30", m, framePopupW, framePopupH)
}

// localProjectResult is the dir check landing on a project no rule sends
// anywhere: the row's key moves to /home/zvi/Projects/toy, so the rules
// re-resolve to local.
func localProjectResult(m Model) dirResultMsg {
	return dirResultMsg{
		req:       request{version: m.reqs.dir, key: m.dir.Value()},
		dirExists: true, isGitRepo: true,
		memoryKey: "/home/zvi/Projects/toy",
	}
}

// TestMachineProjectCheckMovingToLocalAsksThePickerAgain: the account
// picker's preview is skipped while dev is selected, and the dir check is
// what moves the row to local when the project changes -- so it must ask
// for the preview again, or a submit resting on `auto` holds for a probe
// nothing will ever answer (independent review #1a).
func TestMachineProjectCheckMovingToLocalAsksThePickerAgain(t *testing.T) {
	m, _, _, _ := machineModel(t)
	m = settle(t, m)
	m.deps.Picker = &fakePicker{}
	m.account = form.NewAccountField(theme.Default())
	if !m.remoteSelected() {
		t.Fatal("fixture: dev is not selected")
	}
	next, cmd := m.handleDirResult(localProjectResult(m))
	if next.remoteSelected() {
		t.Fatal("fixture: the project check did not move the row to local")
	}
	if _, ok := findMsg[pickerDebounceMsg](t, cmd); !ok {
		t.Error("the move to local scheduled no picker preview")
	}
}

// TestMachineProjectCheckMovingToLocalReChecksTheTitle: the title check in
// flight was asked about dev (origin's branches, no local labels); once the
// project check moves the row to local, it has to be asked again about this
// machine, or a laptop branch of the same name slips through (independent
// review #1b).
func TestMachineProjectCheckMovingToLocalReChecksTheTitle(t *testing.T) {
	m, _, _, _ := machineModel(t)
	m = settle(t, m)
	_, cmd := m.handleDirResult(localProjectResult(m))
	debounce, ok := findMsg[titleDebounceMsg](t, cmd)
	if !ok {
		t.Fatal("the move to local scheduled no title check")
	}
	if debounce.remote {
		t.Error("the title check scheduled after the move to local still asks about dev")
	}
}

// TestMachineSubmitAsksAgainAfterAnUnreachableVerdict: "try again" has to
// mean it -- a ⌃S after an unreachable or unanswered verdict asks the
// machine again, rather than refusing on the old answer until the row is
// cycled (independent review #2).
func TestMachineSubmitAsksAgainAfterAnUnreachableVerdict(t *testing.T) {
	m, _, prober, _ := machineModel(t)
	prober.err = errors.Join(machine.ErrUnreachable, errors.New("dev (EC2) is unreachable: Token for dev is expired"))
	m = settle(t, m)
	m.title.SetTitle("fix it", false)
	m.worktree.SetBranch("zvi/fix-it", false)
	m = landTitle(t, m, []tea.Cmd{m.scheduleTitleCheck(m.title.Value(), m.worktree.Branch(), m.dir.Value(), m.worktree.On(), m.remoteSelected())})

	prober.err = nil // the login was renewed
	next, out := m.Update(form.SubmitMsg{})
	m = next.(Model)
	if !m.submitHeld || !m.machineCheckPending() {
		t.Fatalf("a ⌃S after an unreachable verdict did not ask again (held=%v pending=%v)", m.submitHeld, m.machineCheckPending())
	}
	result, ok := findMsg[machineResultMsg](t, out)
	if !ok {
		t.Fatal("no probe was sent")
	}
	next, _ = m.Update(result)
	m = next.(Model)
	if m.mach.verdict.err != nil || len(prober.asked) != 2 {
		t.Errorf("verdict = %+v after %d probes; want ready after the second", m.mach.verdict, len(prober.asked))
	}
}

// TestMachineSubmitReadsTheListAgainAfterItFailed: a machine list that
// failed is read again on the next ⌃S, not held against the machine for
// the life of the popup.
func TestMachineSubmitReadsTheListAgainAfterItFailed(t *testing.T) {
	m, runner, _, _ := machineModel(t)
	runner.listErr = errCheckTimedOut
	m = settle(t, m)
	runner.listErr = nil
	m.title.SetTitle("fix it", false)
	m.worktree.SetBranch("zvi/fix-it", false)
	m = landTitle(t, m, []tea.Cmd{m.scheduleTitleCheck(m.title.Value(), m.worktree.Branch(), m.dir.Value(), m.worktree.On(), m.remoteSelected())})
	next, out := m.Update(form.SubmitMsg{})
	m = next.(Model)
	if _, ok := findMsg[machinesListedMsg](t, out); !ok {
		t.Error("a ⌃S after a failed machine list did not read it again")
	}
}

// submitAndLandTheReask presses ⌃S on a machine that was unreachable, which
// asks it again (reaskMachine), and lands that second answer -- the same
// as the first here -- so the refusal is the one the user would see.
func submitAndLandTheReask(t *testing.T, m Model) Model {
	t.Helper()
	next, out := m.Update(form.SubmitMsg{})
	m = next.(Model)
	result, ok := findMsg[machineResultMsg](t, out)
	if !ok {
		t.Fatal("the ⌃S did not ask the machine again")
	}
	next, _ = m.Update(result)
	return next.(Model)
}

// --- the adversarial review's findings (each reproduced there first) -------

// cycleAwayAndBack moves the machine row dev -> local -> dev and returns the
// probe the return scheduled, unrun, landing the title checks each cycle
// schedules so a later submit is held for the probe alone.
func cycleAwayAndBack(t *testing.T, m Model) (Model, tea.Cmd) {
	t.Helper()
	_ = m.form.FocusByID("machine")
	next, out := m.Update(key(tea.KeyLeft, 0))
	m = landMachineTitleCheck(t, next.(Model), out)
	if m.remoteSelected() {
		t.Fatal("fixture: <- did not select local")
	}
	next, out = m.Update(key(tea.KeyRight, 0))
	m = landMachineTitleCheck(t, next.(Model), out)
	if m.machine.Value() != "dev" || !m.machineCheckPending() {
		t.Fatalf("fixture: machine = %q, pending = %v", m.machine.Value(), m.machineCheckPending())
	}
	return m, out
}

// TestMachineSupersededProbeAnswerIsDropped: two probes asking the same
// question (dev, this project) can be out at once after the row is cycled
// twice; the older one's answer must not land as the verdict while the
// newer is still out.
func TestMachineSupersededProbeAnswerIsDropped(t *testing.T) {
	m, _, prober, _ := machineModel(t)
	m = settle(t, m)
	m.title.SetTitle("fix it", false)
	m.worktree.SetBranch("zvi/fix-it", false)
	m, probe1 := cycleAwayAndBack(t, m)
	m, probe2 := cycleAwayAndBack(t, m)

	prober.err = errors.Join(machine.ErrUnreachable, errors.New("dev (EC2) is unreachable: connection reset"))
	r1, ok := findMsg[machineResultMsg](t, probe1)
	if !ok {
		t.Fatal("probe #1 produced no result")
	}
	prober.err = nil
	r2, ok := findMsg[machineResultMsg](t, probe2)
	if !ok {
		t.Fatal("probe #2 produced no result")
	}
	next, _ := m.Update(r1)
	m = next.(Model)
	if m.mach.landed {
		t.Errorf("a superseded probe's answer landed as the verdict (%v) while the newer one is out", m.mach.verdict.err)
	}
	next, _ = m.Update(r2)
	m = next.(Model)
	if !m.mach.landed || m.mach.verdict.err != nil {
		t.Errorf("the current probe's answer did not land: landed=%v err=%v", m.mach.landed, m.mach.verdict.err)
	}
}

// TestMachineRefusalDuringTheRoundTripLandsOnTheMachineRow: a machine
// verdict that turns to a refusal while the remote base is being resolved
// stops the submit on the machine row, in the probe's words -- not as
// plan.Build's internal refusal on the title row.
func TestMachineRefusalDuringTheRoundTripLandsOnTheMachineRow(t *testing.T) {
	m, _, prober, _ := machineModel(t)
	m = settle(t, m)
	m.title.SetTitle("fix it", false)
	m.worktree.SetBranch("zvi/fix-it", false)
	m = landTitle(t, m, []tea.Cmd{m.scheduleTitleCheck(m.title.Value(), m.worktree.Branch(), m.dir.Value(), m.worktree.On(), m.remoteSelected())})
	next, out := m.Update(form.SubmitMsg{})
	m = next.(Model)
	base, ok := findMsg[remoteBaseMsg](t, out)
	if !ok || !m.submitResolving {
		t.Fatalf("fixture: no remote-base round trip (resolving=%v)", m.submitResolving)
	}
	// The verdict changes under the frozen form: a current answer, as a
	// re-ask that was out would give.
	prober.err = errors.Join(machine.ErrUnreachable, errors.New("dev (EC2) is unreachable: connection reset"))
	m.landMachine(machineVerdict{choice: m.mach.verdict.choice, err: prober.err})
	next, _ = m.Update(base)
	m = next.(Model)
	if m.submitting {
		t.Fatal("a plan was built after the machine was reported unreachable")
	}
	if got := m.form.FocusedID(); got != "machine" {
		t.Errorf("focus = %q; want the machine row", got)
	}
	if text := fieldText(m.title, 100); strings.Contains(text, "could not build plan") {
		t.Errorf("the title row carries plan.Build's refusal: %q", strings.Join(strings.Fields(text), " "))
	}
}

// TestMachineListTimeoutNamesTheListsOwnDeadline: the list is bounded by
// the ordinary check budget, so its "did not answer" names that, not the
// probe's 30s.
func TestMachineListTimeoutNamesTheListsOwnDeadline(t *testing.T) {
	m, runner, _, _ := machineModel(t)
	runner.listErr = errCheckTimedOut
	m = settle(t, m)
	_, listDeadline := m.checkBudget()
	text := strings.Join(strings.Fields(fieldText(m.machine, 100)), " ")
	if !strings.Contains(text, "did not answer in "+listDeadline.String()) {
		t.Errorf("the list gave up after %s, but the row says: %q", listDeadline, text)
	}
}

// refusedRemoteBase drives a popup on dev to the remote-base refusal: the
// base is a laptop-only branch origin lacks.
func refusedRemoteBase(t *testing.T) Model {
	t.Helper()
	m, _, _, git := machineModel(t)
	m = settle(t, m)
	git.fullRefs = map[string]string{"foo": "refs/heads/foo"}
	m.title.SetTitle("fix it", false)
	m.worktree.SetBranch("zvi/fix-it", false)
	m.worktree.SetBaseItems(1, []string{"main", "foo"})
	m.worktree.SetBase("foo")
	m.reactToChanges()
	m = landTitle(t, m, []tea.Cmd{m.scheduleTitleCheck(m.title.Value(), m.worktree.Branch(), m.dir.Value(), m.worktree.On(), m.remoteSelected())})
	next, out := m.Update(form.SubmitMsg{})
	m = next.(Model)
	base, ok := findMsg[remoteBaseMsg](t, out)
	if !ok {
		t.Fatalf("fixture: no remote base round trip (held=%v)", m.submitHeld)
	}
	next, _ = m.Update(base)
	m = next.(Model)
	if m.submitting || m.form.FocusedID() != "worktree" {
		t.Fatalf("fixture: submitting=%v focus=%q", m.submitting, m.form.FocusedID())
	}
	if text := fieldText(m.worktree, 120); !strings.Contains(text, "origin has no foo") {
		t.Fatalf("fixture: worktree panel = %q", strings.Join(strings.Fields(text), " "))
	}
	return m
}

// TestMachineRemoteBaseRefusalSurvivesTheRelist: the fetch's re-list
// rewrites the base status line; the refusal has to be part of what it
// composes, not wiped and replaced with a promise origin cannot keep.
func TestMachineRemoteBaseRefusalSurvivesTheRelist(t *testing.T) {
	m := refusedRemoteBase(t)
	relist := baseResultMsg{req: request{version: m.reqs.base, key: m.dir.Value()}, refs: []string{"main", "foo"}, head: "main"}
	next, _ := m.Update(relist)
	m = next.(Model)
	text := strings.Join(strings.Fields(fieldText(m.worktree, 120)), " ")
	if !strings.Contains(text, "origin has no foo") {
		t.Errorf("the refusal is gone from the worktree panel: %q", text)
	}
	if strings.Contains(text, "cut from origin's copy of foo") {
		t.Errorf("the panel promises a cut origin cannot supply: %q", text)
	}
	// And it goes with the base it was about.
	m.worktree.SetBase("main")
	m.reactToChanges()
	if text := strings.Join(strings.Fields(fieldText(m.worktree, 120)), " "); strings.Contains(text, "origin has no foo") {
		t.Errorf("the refusal outlived its base: %q", text)
	}
}

// TestMachineSecondListLandingRefreshesTheVerdict: a list that succeeds
// after one that failed asks the machine, rather than leaving the failed
// list's refusal standing.
func TestMachineSecondListLandingRefreshesTheVerdict(t *testing.T) {
	m, runner, _, _ := machineModel(t)
	m = pumpAsync(t, m, initCmdsExcept[machinesListedMsg](m))
	failed := machinesListedMsg{version: m.reqs.machineList, err: errors.New("herdr machine list --json: signal: killed")}
	next, _ := m.Update(failed)
	m = next.(Model)
	if m.mach.verdict.err == nil {
		t.Fatal("fixture: the failed list landed no refusal")
	}
	next, out := m.Update(machinesListedMsg{version: m.reqs.machineList, saved: runner.saved})
	m = next.(Model)
	if _, ok := findMsg[machineResultMsg](t, out); !ok {
		t.Error("the list succeeding scheduled no probe")
	}
}

// TestMachineDiscardedFormsListAnswerIsDropped: ⌃R⌃R's rebuilt form must
// not take the discarded form's list answer as its own.
func TestMachineDiscardedFormsListAnswerIsDropped(t *testing.T) {
	m, runner, _, _ := machineModel(t)
	var oldList tea.Cmd
	for _, cmd := range m.initCmds {
		if _, ok := cmd().(machinesListedMsg); ok {
			oldList = cmd
		}
	}
	runner.listErr = errors.New("herdr machine list --json: signal: killed")
	stale := oldList().(machinesListedMsg)
	runner.listErr = nil
	next, _ := m.Update(form.ClearRequestedMsg{})
	fresh := next.(Model)
	fresh = pumpAsync(t, fresh, initCmdsExcept[machinesListedMsg](fresh))
	next, _ = fresh.Update(stale)
	fresh = next.(Model)
	if fresh.mach.verdict.err != nil {
		t.Errorf("the rebuilt form took the discarded form's list failure: %v", fresh.mach.verdict.err)
	}
}
