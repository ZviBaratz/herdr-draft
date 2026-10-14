package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/defaults"
	"github.com/ZviBaratz/herdr-draft/internal/form"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
	"github.com/ZviBaratz/herdr-draft/internal/machine"
	"github.com/ZviBaratz/herdr-draft/internal/pathx"
	"github.com/ZviBaratz/herdr-draft/internal/plan"
)

// machineCheckDeadline bounds the probe of another machine (machine spec
// §7): preflightCheckDeadline's size rather than blockingCheckDeadline's,
// because one `herdr --machine` call is about three ssh sessions and a new
// SSM connection alone takes 4-5s (§3.4). A constant, not a [timeouts]
// key, for blockingCheckDeadline's reason.
const machineCheckDeadline = 30 * time.Second

// notifyDeadline bounds the toast a remote create posts (machine spec
// §9.5). Best-effort: a toast that does not land costs a session nothing.
const notifyDeadline = 2 * time.Second

// machineState is everything the app knows about the machine row beyond
// what the field shows (machine spec §9).
type machineState struct {
	// saved is `herdr machine list --json`, read once from Init; listed is
	// whether it has answered, and listErr how it failed.
	saved   []herdrc.MachineInfo
	listed  bool
	listErr error

	// key is the project key the rules last resolved against: lexical
	// from New, then the dir check's canonical one (§9.2).
	key string
	// touched is v2 spec §10's touched-versus-preselected flag: once the
	// user has cycled the row, a re-resolution never moves it.
	touched bool

	// asked is the selector-and-key the last probe was scheduled for, so a
	// change schedules one and a repeat does not; landed is whether its
	// answer is in.
	asked  string
	landed bool
	// verdict is that answer.
	verdict machineVerdict

	// remoteBase is the base a remote worktree is cut from, resolved at
	// submit (machine spec §6.4) the way a lane's commit is.
	remoteBase string
	// shownFor is the machine label the base row's note was last written
	// for, so a change of machine rewrites it (refreshBaseStatus).
	shownFor string
}

// machineVerdict is one probe's answer.
type machineVerdict struct {
	choice machine.Choice
	rel    string
	report machine.Report
	// origin is this clone's origin URL, read with the probe so Judge can
	// compare it at submit without I/O.
	origin string
	// err is a refusal: unreachable, no saved machine, nowhere to carry
	// the project. unknown is a probe that did not answer, and unanswered
	// names what did not answer when that was not the machine itself.
	err        error
	unknown    bool
	unanswered string
}

// machinesListedMsg is `herdr machine list`'s answer.
type machinesListedMsg struct {
	saved []herdrc.MachineInfo
	err   error
}

// machineResultMsg is a probe's answer, for the selector-and-key it was
// asked about.
type machineResultMsg struct {
	asked   string
	verdict machineVerdict
}

// remoteBaseMsg carries the base a remote worktree is cut from back to the
// submit it was blocking.
type remoteBaseMsg struct {
	base string
	err  error
}

// newMachineRow builds the machine row when config.toml has a [machine]
// table (machine spec §9.1), resolving the rules against the project the
// form opens on -- lexically, since nothing may run before the first frame
// (§9.2); the dir check re-resolves with the canonical key.
func (m *Model) newMachineRow() {
	if !m.cfg.Machine.Configured {
		return
	}
	m.machine = form.NewMachineField(m.palette)
	m.machine.SetChoices(m.machineChoices())
	// The rules config.toml could not use, on the row they would have
	// configured (machine spec §5.1).
	m.machine.SetNotes(m.cfg.Machine.Warnings)
	m.mach.key = filepath.Clean(pathx.ExpandTilde(m.dir.Value()))
	m.applyMachineRule()
}

// machineChoices is the offered set (machine spec §4.2), labelled by the
// saved profiles once `herdr machine list` has answered.
func (m Model) machineChoices() []form.MachineChoice {
	offered := m.cfg.Machine.Offered()
	out := make([]form.MachineChoice, 0, len(offered))
	for _, sel := range offered {
		label := sel
		if c, err := machine.Select(m.mach.saved, sel); err == nil && c.Remote() {
			label = c.Label()
		}
		out = append(out, form.MachineChoice{Selector: sel, Label: label})
	}
	return out
}

// applyMachineRule resolves the rules for the current key and, unless the
// user has cycled the row, selects the answer (machine spec §5).
func (m *Model) applyMachineRule() {
	if m.machine == nil {
		return
	}
	sel, from, rule := defaults.Machine(m.cfg, m.mach.key)
	if !m.mach.touched {
		m.machine.SetValue(sel)
	}
	m.machine.SetProvenance(m.machineProvenance(from, rule, sel))
}

// machineProvenance is the panel's "from" line: the rule that matched, or
// config.toml's default, or nothing for the builtin.
func (m Model) machineProvenance(from defaults.Tier, rule, sel string) string {
	if m.mach.touched || from != defaults.TierUserConfig {
		return ""
	}
	if rule == "" {
		return "config.toml: [machine] default"
	}
	shown := rule
	if home := m.homeDir; home != "" && (rule == home || strings.HasPrefix(rule, home+string(filepath.Separator))) {
		shown = "~" + strings.TrimPrefix(rule, home)
	}
	return fmt.Sprintf("config.toml: %s → %s", shown, sel)
}

// remoteSelected reports whether the row has another machine selected.
func (m Model) remoteSelected() bool {
	return m.machine != nil && m.machine.Value() != config.LocalMachine
}

// machineLabel is the selected machine's label, "" for this machine.
func (m Model) machineLabel() string {
	if !m.remoteSelected() {
		return ""
	}
	sel := m.machine.Value()
	if c, err := machine.Select(m.mach.saved, sel); err == nil {
		return c.Label()
	}
	return sel
}

// machineListCmd reads the saved machines, once, from Init. Local and
// fast, and bounded anyway: a herdr that does not answer must not hold a
// submit for good.
func (m Model) machineListCmd() tea.Cmd {
	runner := m.deps.Runner
	lt, deadline := m.checkBudget()
	return func() tea.Msg {
		msg, ok := awaitCheck(lt, deadline, func(ctx context.Context) machinesListedMsg {
			saved, err := runner.MachineList(ctx)
			return machinesListedMsg{saved: saved, err: err}
		})
		if !ok {
			return machinesListedMsg{err: errCheckTimedOut}
		}
		return msg
	}
}

// handleMachinesListed labels the chips and, if another machine is
// selected, probes it.
func (m Model) handleMachinesListed(msg machinesListedMsg) (Model, tea.Cmd) {
	m.mach.saved, m.mach.listErr, m.mach.listed = msg.saved, msg.err, true
	if m.machine == nil {
		return m, nil
	}
	m.machine.SetChoices(m.machineChoices())
	m.syncDerivedInertness()
	cmd := m.maybeProbeMachine()
	// A verdict settled here, with no probe to wait for, still owes a held
	// submit its answer.
	if m.submitHeld && !m.machineCheckPending() {
		return m.handleSubmit()
	}
	return m, cmd
}

// machineAsk is what a probe is asked about: the selector and the key.
func (m Model) machineAsk() string { return m.machine.Value() + "\x00" + m.mach.key }

// maybeProbeMachine schedules a probe when the selector or the key has
// changed since the last one (machine spec §7), and says what the row is
// waiting for meanwhile. It is idempotent, so every path that can change
// either calls it: the list landing, the dir check landing, and a routed
// message (reactToChanges).
func (m *Model) maybeProbeMachine() tea.Cmd {
	if m.machine == nil {
		return nil
	}
	ask := m.machineAsk()
	if ask == m.mach.asked {
		return nil
	}
	m.mach.asked, m.mach.landed, m.mach.verdict = ask, false, machineVerdict{}
	if !m.remoteSelected() {
		m.mach.landed = true
		m.machine.SetStatus(form.MachineIdle, "", "")
		return nil
	}
	if !m.mach.listed {
		m.machine.SetStatus(form.MachineChecking, "checking…", "reading herdr's saved machines…")
		m.mach.asked = "" // the list landing asks again
		return nil
	}
	if errors.Is(m.mach.listErr, errCheckTimedOut) {
		// Unknown, not a herdr too old for machines (#202).
		m.landMachine(machineVerdict{unknown: true, unanswered: "herdr's list of saved machines"})
		return nil
	}
	if m.mach.listErr != nil {
		m.landMachine(machineVerdict{err: fmt.Errorf("could not read herdr's saved machines, which another machine needs herdr 0.9.1 for: %v", herdrc.Reason(m.mach.listErr))})
		return nil
	}
	choice, err := machine.Select(m.mach.saved, m.machine.Value())
	if err != nil {
		m.landMachine(machineVerdict{err: err})
		return nil
	}
	m.machine.SetStatus(form.MachineChecking, "checking…", "checking "+choice.Label()+"…")

	prober := m.deps.Prober
	if prober == nil {
		prober = machine.CLIProber{Runner: m.deps.Runner}
	}
	git, key, home := m.deps.Git, m.mach.key, m.homeDir
	lt := m.deps.Lifetime
	deadline := m.machineDeadline()
	return func() tea.Msg {
		v, ok := awaitCheck(lt, deadline, func(ctx context.Context) machineVerdict {
			rel, err := machine.Rel(pathx.CanonicalKey(home), key)
			if err != nil {
				return machineVerdict{choice: choice, err: err}
			}
			report, err := prober.Probe(ctx, choice, rel)
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				// The deadline answered, not the machine: unknown (#202).
				return machineVerdict{choice: choice, unknown: true}
			}
			if err != nil {
				return machineVerdict{choice: choice, rel: rel, err: err}
			}
			origin, _ := git.OriginURL(ctx, key)
			return machineVerdict{choice: choice, rel: rel, report: report, origin: origin}
		})
		if !ok {
			return machineResultMsg{asked: ask, verdict: machineVerdict{choice: choice, unknown: true}}
		}
		return machineResultMsg{asked: ask, verdict: v}
	}
}

// machineDeadline is machineCheckDeadline, or a test's own checkDeadline.
func (m Model) machineDeadline() time.Duration {
	if m.checkDeadline > 0 {
		return m.checkDeadline
	}
	return machineCheckDeadline
}

// handleMachineResult lands a probe's answer, if it is still about what the
// row holds, and resumes a submit it was holding.
func (m Model) handleMachineResult(msg machineResultMsg) (Model, tea.Cmd) {
	if m.machine == nil || msg.asked != m.mach.asked {
		return m, nil
	}
	m.landMachine(msg.verdict)
	if m.submitHeld {
		return m.handleSubmit()
	}
	return m, nil
}

// landMachine records a verdict and shows it.
func (m *Model) landMachine(v machineVerdict) {
	m.mach.verdict, m.mach.landed = v, true
	m.showMachineVerdict()
}

// showMachineVerdict puts the landed verdict on the row. Separate from
// landMachine because one part of it is about the form rather than the
// machine -- a worktree needs a repository there -- so syncDerivedInertness
// re-shows it when the worktree toggles.
func (m *Model) showMachineVerdict() {
	if m.machine == nil || !m.remoteSelected() || !m.mach.landed {
		return
	}
	v := m.mach.verdict
	label := m.machineLabel()
	switch {
	case v.unknown:
		what := cmp.Or(v.unanswered, label)
		m.machine.SetStatus(form.MachineRefused, "no answer",
			fmt.Sprintf("%s did not answer in %s: try again, or choose local", what, m.machineDeadline()))
	case v.err != nil:
		word := "refused"
		if errors.Is(v.err, machine.ErrUnreachable) {
			word = "unreachable"
		}
		m.machine.SetStatus(form.MachineRefused, word, v.err.Error())
	default:
		if _, err := m.judgeMachine(); err != nil {
			m.machine.SetStatus(form.MachineRefused, "can't host it", err.Error())
			return
		}
		m.machine.SetStatus(form.MachineReady, "", "ready: ~/"+v.rel+" on "+label)
	}
}

// judgeMachine is machine.Judge on the landed probe, with the worktree as
// it is now -- the one part of the verdict that depends on the form.
func (m Model) judgeMachine() (string, error) {
	v := m.mach.verdict
	useWorktree := m.worktree.Enabled() && m.worktree.On()
	return machine.Judge(v.report, v.choice, v.rel, useWorktree, v.origin)
}

// machineCheckPending reports whether a submit must wait for the probe of
// the selected machine (machine spec §9.4).
func (m Model) machineCheckPending() bool {
	return m.remoteSelected() && !m.mach.landed
}

// machineRefusal is the submit's machine refusal, or "" when the selected
// machine can host the session. Checked after the other validations, with
// the row focused, where local is one keystroke away.
func (m Model) machineRefusal() string {
	if !m.remoteSelected() {
		return ""
	}
	v := m.mach.verdict
	switch {
	case v.unknown || v.err != nil:
		return "machine"
	}
	if _, err := m.judgeMachine(); err != nil {
		m.machine.SetStatus(form.MachineRefused, "can't host it", err.Error())
		return "machine"
	}
	branch := m.worktree.Branch()
	if m.worktree.Enabled() && m.worktree.On() && branch != "" && v.report.HasBranch(branch) {
		m.worktree.SetBranchVerdict(branch, fmt.Sprintf("%s already has a branch %s", v.choice.Label(), branch))
		return "worktree"
	}
	if w, taken := WorkspaceLabelled(v.report.Workspaces, m.title.Value()); taken {
		m.title.SetVerdict(m.title.Value(), fmt.Sprintf("%s on %s is already labelled this", w.WorkspaceID, v.choice.Label()), form.VerdictRefusal)
		return "title"
	}
	return ""
}

// planMachine is plan.Input's Machine: the landed verdict's profile and the
// project's directory there, or the zero value for this machine.
//
// It fails closed: with another machine selected it is always remote, and
// a verdict that is not a pass leaves Dir empty, which plan.Build refuses
// -- so no answer, however it arrives, can turn a session meant for that
// machine into one built here (decision 4).
func (m Model) planMachine() plan.Machine {
	if !m.remoteSelected() {
		return plan.Machine{}
	}
	v := m.mach.verdict
	out := plan.Machine{ID: v.choice.Info.ID, Label: v.choice.Label(), Target: v.choice.Info.Target}
	if out.ID == "" {
		c, err := machine.Select(m.mach.saved, m.machine.Value())
		if err != nil || !c.Remote() {
			// Nothing to name it by: an id no server has, so Build's
			// remote refusal still fires on the empty directory.
			return plan.Machine{ID: m.machine.Value(), Label: m.machine.Value()}
		}
		out = plan.Machine{ID: c.Info.ID, Label: c.Label(), Target: c.Info.Target}
	}
	if m.mach.landed && v.err == nil && !v.unknown {
		out.Dir = v.report.Clone.Dir
	}
	return out
}

// needsRemoteBase reports whether a submit has to map its base for another
// machine first (machine spec §6.4): a worktree there.
func (m Model) needsRemoteBase() bool {
	return m.remoteSelected() && m.worktree.Enabled() && m.worktree.On()
}

// ResolveRemoteBase is machine.RemoteBase for the form's base, a no-op
// returning "" when needsRemoteBase does not hold. Exported for
// ResolveLinkedCommit's reason: internal/create's equivalence test drives
// the form to a comparable plan.Input through it and WithRemoteBase.
func (m Model) ResolveRemoteBase(ctx context.Context) (string, error) {
	if !m.needsRemoteBase() {
		return "", nil
	}
	return machine.RemoteBase(ctx, m.deps.Git, pathx.ExpandTilde(m.dir.Value()), m.worktree.Base(), m.machineLabel())
}

// WithRemoteBase is m with the remote base resolved.
func (m Model) WithRemoteBase(base string) Model {
	m.mach.remoteBase = base
	return m
}

// remoteBaseCmd runs ResolveRemoteBase off-model, bounded like the lane's
// commit: laptop-side git, on the app's context.
func (m Model) remoteBaseCmd() tea.Cmd {
	src := m
	lt, deadline := m.checkBudget()
	return func() tea.Msg {
		msg, ok := awaitCheck(lt, deadline, func(ctx context.Context) remoteBaseMsg {
			base, err := src.ResolveRemoteBase(ctx)
			return remoteBaseMsg{base: base, err: err}
		})
		if !ok {
			return remoteBaseMsg{err: errCheckTimedOut}
		}
		return msg
	}
}

// handleRemoteBase resumes -- or refuses, on the worktree row -- the submit
// the remote base was blocking.
func (m Model) handleRemoteBase(msg remoteBaseMsg) (Model, tea.Cmd) {
	m.submitResolving = false
	if msg.err != nil {
		reason := msg.err.Error()
		if errors.Is(msg.err, errCheckTimedOut) {
			reason = "couldn't check the base for " + m.machineLabel()
		}
		m.worktree.SetBaseStatus(reason, true)
		return m, m.form.FocusByID("worktree")
	}
	return m.WithRemoteBase(msg.base).continueSubmit()
}

// notifyRemoteCmd posts this machine's toast for a session created on
// another (machine spec §9.5): herdr cannot switch the client's view there,
// so the popup closing must not read as nothing having happened.
func notifyRemoteCmd(runner herdrc.Runner, in plan.Input) func() {
	if !in.Machine.Remote() {
		return func() {}
	}
	title := in.Title + " opened on " + in.Machine.Label
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), notifyDeadline)
		defer cancel()
		_ = runner.NotificationShow(ctx, title, "")
	}
}
