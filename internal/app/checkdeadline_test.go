package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/form"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
)

// These pin #202: every check a submit waits for answers within a bounded
// time, and a check that does not answer reports UNKNOWN rather than a
// verdict. Before, nothing in the project row's check could time out --
// DirExists is os.Stat, IsGitRepo runs git with no context, RepoRoot and
// PrimaryCheckout got context.Background() -- so on a stalled mount ⌃S
// waited silently and for good, with nothing on screen to say why.

// hangingGit is a gitSource whose filesystem and git calls never answer --
// a stalled mount, in a test. Every one of them blocks until release
// closes, which newHangingGit registers for the end of the test so no
// goroutine outlives it.
//
// It overrides the four calls the blocking checks make rather than
// fakeGit's whole surface, and touches none of fakeGit's own counters from
// the blocked goroutine: a test that asserts on those while a check is
// still hung would otherwise be reading them as they are written.
type hangingGit struct {
	*fakeGit
	release chan struct{}
}

func newHangingGit(t *testing.T) *hangingGit {
	t.Helper()
	g := &hangingGit{fakeGit: newFakeGit(), release: make(chan struct{})}
	t.Cleanup(func() { close(g.release) })
	return g
}

func (g *hangingGit) DirExists(string) bool { <-g.release; return false }
func (g *hangingGit) IsGitRepo(string) bool { <-g.release; return false }

func (g *hangingGit) BranchExists(context.Context, string, string) (bool, error) {
	<-g.release
	return false, nil
}

func (g *hangingGit) ResolveCommit(context.Context, string, string) (string, error) {
	<-g.release
	return "", nil
}

// hangingResolveGit answers everything except `git rev-parse <ref>`, the one
// call SettleBase and the lane's base read make. It is the stalled mount
// seen from the base check alone, with the project's own check still able to
// land -- which is what lets a test drive the base path end to end.
type hangingResolveGit struct {
	*fakeGit
	release chan struct{}
	once    sync.Once
}

func newHangingResolveGit(t *testing.T, base *fakeGit) *hangingResolveGit {
	t.Helper()
	g := &hangingResolveGit{fakeGit: base, release: make(chan struct{})}
	// sync.Once because one test releases it itself, to watch what happens
	// after; every other one leaves it to the end so no goroutine outlives
	// the test.
	t.Cleanup(func() { g.releaseOnce() })
	return g
}

func (g *hangingResolveGit) releaseOnce() { g.once.Do(func() { close(g.release) }) }

func (g *hangingResolveGit) ResolveCommit(context.Context, string, string) (string, error) {
	<-g.release
	return "", nil
}

// hungModel is a model whose git never answers and whose deadline is short
// enough for a test to wait it out.
func hungModel(t *testing.T) Model {
	t.Helper()
	m := newTestModel(t, testSetup{Ctx: herdrc.Context{WorkspaceCwd: "/repo"}})
	m.deps.Git = newHangingGit(t)
	m.checkDeadline = 20 * time.Millisecond
	return m
}

// runWithin runs cmd on its own goroutine and returns the message it
// produced, failing the test if it does not produce one in time. Every
// check under test here is running against a git that never answers, so a
// cmd that does not come back is the defect itself and must not hang the
// suite.
func runWithin(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	got := make(chan tea.Msg, 1)
	go func() { got <- cmd() }()
	select {
	case msg := <-got:
		return msg
	case <-time.After(5 * time.Second):
		t.Fatal("the check never answered: it is not bounded by anything")
		return nil
	}
}

// TestDirCheck_AnswersUnknownWhenTheFilesystemDoesNot is #202's own
// scenario at the check itself: a project whose directory never answers
// produces a result all the same, marked as no answer rather than as a
// directory that is not there.
func TestDirCheck_AnswersUnknownWhenTheFilesystemDoesNot(t *testing.T) {
	m := hungModel(t)

	msg := runWithin(t, m.runDirCheck(request{version: m.reqs.dir, key: "/stalled"}))

	res, ok := msg.(dirResultMsg)
	if !ok {
		t.Fatalf("the check answered with %T, want a dirResultMsg", msg)
	}
	if !res.timedOut {
		t.Error("the check answered without timedOut set: a hung mount would read as a settled verdict")
	}
	if res.dirExists {
		t.Error("a check that never ran reported the directory exists")
	}
}

// dirRow is the project row as rendered, which is where this issue's
// "say one is out" half lands.
func dirRow(m Model) string { return ansi.Strip(m.dir.Row(60)) }

// TestSubmit_ACheckThatTimesOutRefusesRatherThanCreating is #202's own
// scenario at the submit: ⌃S is held for the project row's check (#195),
// the check times out, and the submit is refused on the row rather than
// released into a create built on answers nobody has.
func TestSubmit_ACheckThatTimesOutRefusesRatherThanCreating(t *testing.T) {
	runner := &submitFakeRunner{topo: herdrc.CreatedTopology{WorkspaceID: "ws-1", TabID: "t-1", PaneID: "pane-1"}}
	m := settle(t, newSubmitTestModel(t, runner, testSetup{Git: newFakeGit(), Ctx: herdrc.Context{WorkspaceCwd: "/repo"}}))
	m.title.SetTitle("Fix pagination", false)
	m.worktree.SetOn(true)
	m.worktree.SetBranch("zvi/fix-pagination", false)

	cmds := retypeProject(&m, "/stalled")
	m = landTitle(t, m, cmds)
	m.form.FocusByID("title")

	next, _ := m.Update(form.SubmitMsg{})
	m = next.(Model)
	if !m.submitHeld {
		t.Fatal("test setup: the submit was not held for the project row's check")
	}
	// Everything the form asked herdr while settling. Nothing may be added
	// to it by a submit that is about to be refused.
	settled := len(runner.calls)

	// The check for the value the row now holds, answering that it never
	// answered.
	next, _ = m.Update(dirResultMsg{req: request{version: m.reqs.dir, key: "/stalled"}, timedOut: true})
	m = next.(Model)

	if m.submitting {
		t.Fatal("the submit went ahead on a check that never answered")
	}
	if got := runner.calls[settled:]; len(got) > 0 {
		t.Errorf("the refused submit still called herdr: %v", got)
	}
	if got := m.form.FocusedID(); got != "dir" {
		t.Errorf("focus ended on %q, want the project row, which says why", got)
	}
	if row := dirRow(m); !strings.Contains(row, "check timed out") {
		t.Errorf("project row = %q, want it to say the check timed out", row)
	}
}

// TestDirRow_ATimedOutCheckDoesNotReadAsAMissingDirectory is the
// distinction the decision turns on: unknown is not invalid. A path that
// genuinely is not there and a check that never answered must not leave
// the row saying the same thing, because only one of them is the user's
// to fix by typing a different path.
func TestDirRow_ATimedOutCheckDoesNotReadAsAMissingDirectory(t *testing.T) {
	git := newFakeGit()
	missing := settledRepoForm(t, git)
	git.dirExists, git.isGitRepo = false, false
	cmds := retypeProject(&missing, "/nowhere")
	missing, _ = landDirCheck(t, missing, fireDirDebounce(t, &missing, cmds))

	unknown := settledRepoForm(t, newFakeGit())
	retypeProject(&unknown, "/stalled")
	next, _ := unknown.Update(dirResultMsg{req: request{version: unknown.reqs.dir, key: "/stalled"}, timedOut: true})
	unknown = next.(Model)

	missingRow, unknownRow := dirRow(missing), dirRow(unknown)
	if strings.Contains(unknownRow, "invalid") {
		t.Errorf("a timed-out check reads as invalid: %q", unknownRow)
	}
	if !strings.Contains(missingRow, "invalid") {
		t.Errorf("test setup: a missing directory no longer reads as invalid: %q", missingRow)
	}
	if missing.dirUnknown {
		t.Error("a directory that is not there was recorded as unknown")
	}
	if unknown.dirInvalid {
		t.Error("a check that never answered was recorded as an invalid directory")
	}
}

// TestDirRow_SaysItIsCheckingWhileTheCheckIsOut: the row draws the one
// state it never had -- a value scheduled for a check and not yet
// answered. Without it a held ⌃S has nothing on screen at all, which is
// what made a stalled mount look like a dead key.
func TestDirRow_SaysItIsCheckingWhileTheCheckIsOut(t *testing.T) {
	git := newFakeGit()
	m := settledRepoForm(t, git)

	cmds := retypeProject(&m, "/slow")
	if row := dirRow(m); !strings.Contains(row, "checking") {
		t.Errorf("project row with a check out = %q, want it to say it is checking", row)
	}

	m, _ = landDirCheck(t, m, fireDirDebounce(t, &m, cmds))
	if row := dirRow(m); strings.Contains(row, "checking") {
		t.Errorf("project row after its check landed = %q, want the checking note gone", row)
	}
}

// TestTitleCheck_AnswersUnknownWhenGitDoesNot: the title-duplicate check
// asks git whether the branch already exists, and that question holds a
// submit too (#137). A git that never answers must not hold it for good.
func TestTitleCheck_AnswersUnknownWhenGitDoesNot(t *testing.T) {
	m := hungModel(t)

	msg := runWithin(t, m.runTitleCheck(titleDebounceMsg{
		req:        request{version: m.reqs.title, key: "Fix pagination"},
		branch:     "zvi/fix-pagination",
		dir:        "/repo",
		worktreeOn: true,
	}))

	res, ok := msg.(titleResultMsg)
	if !ok {
		t.Fatalf("the check answered with %T, want a titleResultMsg", msg)
	}
	if !res.timedOut {
		t.Error("the check answered without timedOut set: a hung git would read as 'no such branch'")
	}
	if res.branchExists {
		t.Error("a check that never ran reported the branch exists")
	}
	// The two halves the check answers without asking git stay answered:
	// they are pure, so a git that hangs costs them nothing.
	if res.branch != "zvi/fix-pagination" {
		t.Errorf("branch = %q, want the one the check was scheduled for", res.branch)
	}
}

// TestSubmit_ATimedOutTitleCheckRefusesRatherThanCreating: the same
// refusal one row up. A branch that may or may not already exist is not a
// branch to create a worktree on.
func TestSubmit_ATimedOutTitleCheckRefusesRatherThanCreating(t *testing.T) {
	m := settledRepoForm(t, newFakeGit())
	m.reactToChanges()
	m.form.FocusByID("dir")

	next, _ := m.Update(form.SubmitMsg{})
	m = next.(Model)
	if !m.submitHeld {
		t.Fatal("test setup: the submit was not held for the title check")
	}

	next, _ = m.Update(titleResultMsg{
		req:      request{version: m.reqs.title, key: m.title.Value()},
		branch:   m.worktree.Branch(),
		timedOut: true,
	})
	m = next.(Model)

	if m.submitting {
		t.Fatal("the submit went ahead on a duplicate check that never answered")
	}
	if got := m.form.FocusedID(); got != "title" {
		t.Errorf("focus ended on %q, want the title row, which says why", got)
	}
	panel := ansi.Strip(m.title.Panel(80, m.title.PanelRows()))
	if !strings.Contains(panel, "couldn't check") {
		t.Errorf("title panel = %q, want it to say the check could not be made", panel)
	}
}

// TestBaseSettle_AnswersUnknownWhenGitDoesNot: the base check is one `git
// rev-parse` (#194, SettleBase) and it holds a submit like the other two.
func TestBaseSettle_AnswersUnknownWhenGitDoesNot(t *testing.T) {
	m := hungModel(t)
	m.resolved.BaseRef = "old-branch"
	m.baseTouched = false

	cmd := m.scheduleBaseSettle()
	if cmd == nil {
		t.Fatal("test setup: no base check was scheduled for a tier's base")
	}
	msg := runWithin(t, cmd)

	res, ok := msg.(baseSettledMsg)
	if !ok {
		t.Fatalf("the check answered with %T, want a baseSettledMsg", msg)
	}
	if !res.timedOut {
		t.Error("the check answered without timedOut set: a hung git would read as 'no such commit'")
	}
	if res.note != "" {
		t.Errorf("a check that never ran produced the note %q, which claims the base was dropped", res.note)
	}
}

// TestSubmit_ATimedOutBaseCheckRefusesRatherThanCreating: a base that may
// or may not name a commit is not a base to branch from. The refusal lands
// on the worktree row, beside the base it is about.
//
// Driven end to end through the real path -- a project whose memory names a
// base, its dir check landing, the settle that schedules, and a `git
// rev-parse` that never answers -- rather than from a hand-built
// baseSettledMsg. That matters here and not for the other three: the base's
// unknown is read as a comparison against the base in play (baseUnknown),
// so a test that sets m.resolved.BaseRef without putting the ref in the row
// builds a state production never reaches, and would keep passing if the
// comparison stopped matching. applyProjectDefaults calls SetBase
// immediately before scheduleBaseSettle, under the same !baseTouched guard,
// which is the invariant this asserts by exercising it.
func TestSubmit_ATimedOutBaseCheckRefusesRatherThanCreating(t *testing.T) {
	runner := &submitFakeRunner{topo: herdrc.CreatedTopology{WorkspaceID: "ws-1", TabID: "t-1", PaneID: "pane-1"}}
	git := newHangingResolveGit(t, newFakeGit())
	git.listBranchesResult = []string{"main"}
	m := settle(t, newSubmitTestModel(t, runner, testSetup{
		Git: git.fakeGit,
		Ctx: herdrc.Context{WorkspaceCwd: "/repo-a"},
		Projects: memoryFor(map[string]config.ProjectDefaults{
			"/repo-b": {Worktree: ptrBool(true), Base: "old-branch"},
		}),
	}))
	m.deps.Git = git
	m.checkDeadline = 20 * time.Millisecond
	m.title.SetTitle("Fix pagination", false)

	cmds := retypeProject(&m, "/repo-b")
	m = landTitle(t, m, cmds)
	next, _ := m.Update(form.SubmitMsg{})
	m = next.(Model)

	// The dir check lands, applies /repo-b's remembered base and schedules
	// the settle that asks whether it names a commit. The submit holds for
	// that one.
	m, out := landDirCheck(t, m, fireDirDebounce(t, &m, cmds))
	if m.submitting {
		t.Fatalf("test setup: the submit went on with /repo-b's base check still out, from base %q", m.submitInput.BaseRef)
	}
	if got := m.worktree.RequestedBase(); got != "old-branch" {
		t.Fatalf("test setup: the base in play is %q, want the remembered old-branch", got)
	}

	// What the landing check handed back: /repo-b's base check, and the
	// title check its remembered worktree scheduled by turning the worktree
	// on. The submit waits for that one too, so it is landed first and the
	// hold under test is the base check's alone.
	var settled *baseSettledMsg
	var titleCheck tea.Cmd
	for _, msg := range flatten(out) {
		switch msg := msg.(type) {
		case baseSettledMsg:
			settled = &msg
		case titleDebounceMsg:
			next, run := m.Update(msg)
			m = next.(Model)
			titleCheck = run
		}
	}
	if settled == nil || titleCheck == nil {
		t.Fatalf("test setup: the landing dir check handed back base=%v title=%v", settled != nil, titleCheck != nil)
	}
	if !settled.timedOut {
		t.Fatalf("the base check answered %+v, want it to report the deadline", *settled)
	}
	m, _ = landTitleCheck(t, m, titleCheck)
	next, _ = m.Update(*settled)
	m = next.(Model)

	if m.submitting {
		t.Fatal("the submit went ahead on a base check that never answered")
	}
	if m.submitHeld {
		t.Fatalf("test setup: the submit is still held (dir %v, base %v, title %v)",
			m.dirCheckPending(), m.baseSettlePending(), m.titleCheckPending())
	}
	if got := m.form.FocusedID(); got != "worktree" {
		t.Errorf("focus ended on %q, want the worktree row, which says why", got)
	}
	panel := ansi.Strip(m.worktree.Panel(80, m.worktree.PanelRows()))
	if !strings.Contains(panel, "couldn't check old-branch") {
		t.Errorf("worktree panel = %q, want it to name the base it could not check", panel)
	}
}

// TestLinkedCommit_AnswersUnknownWhenGitDoesNot: the lane's base is
// resolved at submit, after every blocking check and before the account
// pick (#171). The submit is stopped dead waiting for it, and the form is
// frozen while it waits (#136), so this is the one of the four where a
// hang takes the ways out with it.
func TestLinkedCommit_AnswersUnknownWhenGitDoesNot(t *testing.T) {
	m := laneModel(t, &submitFakeRunner{}, laneGit(), true)
	m.deps.Git = newHangingGit(t)
	m.checkDeadline = 20 * time.Millisecond

	msg := runWithin(t, m.linkedCommitCmd())

	res, ok := msg.(linkedCommitMsg)
	if !ok {
		t.Fatalf("the read answered with %T, want a linkedCommitMsg", msg)
	}
	if !errors.Is(res.err, errCheckTimedOut) {
		t.Errorf("err = %v, want it to report the deadline", res.err)
	}
	if res.commit != "" {
		t.Errorf("commit = %q, want none from a read that never ran", res.commit)
	}
}

// TestSubmit_ATimedOutLaneCommitSaysSoRatherThanBlamingTheRef: a read that
// never answered is not a ref that does not resolve. The refusal both
// paths share is right -- stop on the worktree row, where another base is
// one keystroke away -- but the reason has to be the true one, since only
// one of the two is fixed by picking a different base.
func TestSubmit_ATimedOutLaneCommitSaysSoRatherThanBlamingTheRef(t *testing.T) {
	m := laneModel(t, &submitFakeRunner{}, laneGit(), true)
	m.worktree.SetBase("main")

	next, _ := m.Update(linkedCommitMsg{err: errCheckTimedOut})
	m = next.(Model)

	if m.submitting {
		t.Fatal("the submit went ahead on a base read that never answered")
	}
	panel := ansi.Strip(m.worktree.Panel(80, m.worktree.PanelRows()))
	if !strings.Contains(panel, "couldn't check main") {
		t.Errorf("worktree panel = %q, want it to say the read could not be made", panel)
	}
	if strings.Contains(panel, "couldn't resolve") {
		t.Errorf("worktree panel = %q, want it not to blame the ref for a read that never ran", panel)
	}
}

// TestBaseSettle_ATimedOutTierAnswerIsDroppedOnceTheUserHasChosen: a
// tier's base check is dropped when the user has picked a base of their
// own, and that has to hold for the timed-out answer too. Reporting an
// unknown about a ref the form no longer offers would refuse every submit
// over a base nobody can see or replace.
func TestBaseSettle_ATimedOutTierAnswerIsDroppedOnceTheUserHasChosen(t *testing.T) {
	m := settledRepoForm(t, newFakeGit())
	m.resolved.BaseRef = "old-branch"
	m.baseTouched = true
	m.baseSettleVersion++

	next, _ := m.Update(baseSettledMsg{version: m.baseSettleVersion, timedOut: true})
	m = next.(Model)

	if m.baseUnknown() {
		t.Error("a tier's timed-out check refused a submit over a base the user had already replaced")
	}
	panel := ansi.Strip(m.worktree.Panel(80, m.worktree.PanelRows()))
	if strings.Contains(panel, "couldn't check") {
		t.Errorf("worktree panel = %q, want nothing said about a dropped question", panel)
	}
}

// TestSubmit_ACheckThatAnswersAfterATimeoutReleasesTheSubmit: the unknown
// is the last answer, not a latch. A stalled mount that comes back, or a
// user who retypes the path, leaves a form that can be submitted again --
// otherwise the refusal would be permanent and `esc` the only way out,
// which is the defect with an extra step.
func TestSubmit_ACheckThatAnswersAfterATimeoutReleasesTheSubmit(t *testing.T) {
	git := newFakeGit()
	m := settledRepoForm(t, git)

	cmds := retypeProject(&m, "/slow")
	m = landTitle(t, m, cmds)
	next, _ := m.Update(dirResultMsg{req: request{version: m.reqs.dir, key: "/slow"}, timedOut: true})
	m = next.(Model)
	if !m.dirUnknown {
		t.Fatal("test setup: the timed-out check left no unknown to clear")
	}

	// The same value, checked again and answered this time.
	m, _ = landDirCheck(t, m, fireDirDebounce(t, &m, cmds))
	if m.dirUnknown {
		t.Fatal("an answered check left the unknown from the one before it standing")
	}
	if row := dirRow(m); strings.Contains(row, "check timed out") {
		t.Errorf("project row = %q, want the timed-out note gone once a check answered", row)
	}

	next, cmd := m.Update(form.SubmitMsg{})
	m = next.(Model)
	if !m.submitting {
		t.Fatal("the submit was still refused after a check answered")
	}
	drainSubmitProgress(t, m, submitChain(t, cmd))
}

// TestBaseSettle_AnAnswerAfterATimeoutTakesTheRefusalBack is the base
// row's half of the same rule: the unknown is the last answer, not a
// latch. Both halves of it have to go -- the flag that refuses the submit
// and the line that says why -- or the row goes on naming a base it could
// not check after a check that could.
func TestBaseSettle_AnAnswerAfterATimeoutTakesTheRefusalBack(t *testing.T) {
	m := timedOutBaseSettle(t, "old-branch")

	// The same question, asked again and answered: the base still names a
	// commit, so nothing is dropped and nothing is left to say.
	m.baseSettleVersion++
	next, _ := m.Update(baseSettledMsg{version: m.baseSettleVersion, resolved: m.resolved})
	m = next.(Model)

	if m.baseUnknown() {
		t.Error("an answered check left the unknown from the one before it standing")
	}
	panel := ansi.Strip(m.worktree.Panel(80, m.worktree.PanelRows()))
	if strings.Contains(panel, "couldn't check") {
		t.Errorf("worktree panel = %q, want the timed-out line gone once a check answered", panel)
	}
}

// timedOutBaseSettle is a form whose base check for ref gave up: the setup
// the three tests below start from.
func timedOutBaseSettle(t *testing.T, ref string) Model {
	t.Helper()
	m := settledRepoForm(t, newFakeGit())
	m = landTitle(t, m, m.reactToChanges())
	m.resolved.BaseRef = ref
	m.worktree.OfferBase(ref)
	m.worktree.SetBase(ref)
	m.baseSettleVersion++
	next, _ := m.Update(baseSettledMsg{version: m.baseSettleVersion, timedOut: true})
	m = next.(Model)
	if !m.baseUnknown() {
		t.Fatalf("test setup: the timed-out base check for %s recorded no unknown", ref)
	}
	return m
}

// TestBaseSettle_TheUnknownGoesWithTheRefItWasAbout is the review's first
// blocking finding. The base is the one check of the four that can decline
// to ask: scheduleBaseSettle returns no message at all when the resolution
// supplies no base or the user has chosen their own. So an unknown cleared
// only where an ANSWER lands latched -- and the line it writes says "pick a
// base", which sets baseTouched, which is exactly the condition under which
// no answer is ever coming. The submit was then refused for the life of the
// popup: #202's own defect, one layer up.
func TestBaseSettle_TheUnknownGoesWithTheRefItWasAbout(t *testing.T) {
	m := timedOutBaseSettle(t, "old-branch")

	// Doing what the line says.
	m.worktree.OfferBase("feature")
	m.worktree.SetBase("feature")
	m.reactToChanges()

	if m.baseUnknown() {
		t.Error("the unknown outlived the base it was about: no answer about old-branch is ever coming now")
	}
	panel := ansi.Strip(m.worktree.Panel(80, m.worktree.PanelRows()))
	if strings.Contains(panel, "couldn't check old-branch") {
		t.Errorf("worktree panel = %q, want nothing about a base the user has replaced", panel)
	}

	next, _ := m.Update(form.SubmitMsg{})
	m = next.(Model)
	if m.submitHeld {
		t.Fatalf("test setup: the submit was held (dir %v, base %v, title %v)",
			m.dirCheckPending(), m.baseSettlePending(), m.titleCheckPending())
	}
	if !m.submitting {
		t.Errorf("the submit is still refused after the base moved: focus %q", m.form.FocusedID())
	}
}

// TestBaseSettle_TheUnknownDoesNotSurviveAProjectChange is the same finding
// from the side where the user changes nothing about the base: a project
// whose resolution supplies no base makes scheduleBaseSettle decline too, so
// walking away from the project did not rescue the form either.
func TestBaseSettle_TheUnknownDoesNotSurviveAProjectChange(t *testing.T) {
	m := timedOutBaseSettle(t, "old-branch")

	cmds := retypeProject(&m, "/other")
	m = landTitle(t, m, cmds)
	m, _ = landDirCheck(t, m, fireDirDebounce(t, &m, cmds))

	if m.baseUnknown() {
		t.Errorf("the unknown survived a whole project change: resolved base %q", m.resolved.BaseRef)
	}
}

// TestBaseSettle_ASuccessfulListDoesNotWipeTheUnknownsReason is the
// review's second: the base LIST check owns the same status line and
// clears it on every success, so a list landing after a timeout left the
// refusal with nothing on screen behind it -- ⌃S moved focus to the
// worktree row and said nothing at all. The interleaving is the ordinary
// one: the fetch's re-list arrives seconds after the settle's deadline.
func TestBaseSettle_ASuccessfulListDoesNotWipeTheUnknownsReason(t *testing.T) {
	m := timedOutBaseSettle(t, "main")

	next, _ := m.Update(baseResultMsg{
		req:  request{version: m.reqs.base, key: m.dir.Value()},
		refs: []string{"main", "feature"},
		head: "main",
	})
	m = next.(Model)

	if !m.baseUnknown() {
		t.Fatal("test setup: the list landing cleared the unknown itself")
	}
	panel := ansi.Strip(m.worktree.Panel(80, m.worktree.PanelRows()))
	if !strings.Contains(panel, "couldn't check main") {
		t.Errorf("worktree panel = %q, want the refusal's reason still on it", panel)
	}
}

// TestShutdown_WaitsForACheckStillInFlight: the popup's bounded wait at
// quit exists so a cancelled child is actually dead before the process
// exits (#211) -- exec.CommandContext kills from a watcher goroutine that
// has to be scheduled first. A deadline that returned the answer's whole
// Lifetime region the moment it fired would hand Shutdown nothing to wait
// for and bring that bug back with an extra step, for all four checks at
// once. Found in review.
func TestShutdown_WaitsForACheckStillInFlight(t *testing.T) {
	lt := NewLifetime()
	git := newHangingResolveGit(t, laneGit())
	m := laneModel(t, &submitFakeRunner{}, git.fakeGit, true)
	m.deps.Git = git
	m.deps.Lifetime = lt
	m.checkDeadline = 20 * time.Millisecond

	answered := make(chan tea.Msg, 1)
	cmd := m.linkedCommitCmd()
	go func() { answered <- cmd() }()
	// The deadline fires long before the call comes back, so the message is
	// out and the form has moved on while git is still held.
	if _, ok := (<-answered).(linkedCommitMsg); !ok {
		t.Fatal("the lane's base read did not answer")
	}

	done := make(chan struct{})
	go func() { lt.Shutdown(2 * time.Second); close(done) }()
	select {
	case <-done:
		t.Fatal("Shutdown returned with the call it cancelled still running: nothing waits for the kill")
	case <-time.After(100 * time.Millisecond):
	}

	git.releaseOnce()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown did not return once the call it was waiting for came back")
	}
}

// TestAwaitCheck_AnAnswerThatArrivedIsNeverCalledATimeout is the hazard the
// Lifetime fix walked into and CI caught. `select` chooses uniformly at
// random among the cases that are ready, so an answer sitting in the
// channel while the context is also done is a coin flip -- and the first
// draft made that the COMMON case, because it cancelled the context from
// the answering goroutine's own defer, right after the send. A check that
// reports a deadline over an answer it already has refuses a submit for
// nothing, and a project check that does it leaves the worktree row never
// learning it is in a repository: three unrelated tests failed in their
// SETUP, on the Linux runner and not the macOS one, at a rate of about one
// run in four.
//
// Run from sixty-four goroutines at once, and that number is measured
// rather than picked: the window is a scheduling accident, not a
// certainty. With one caller the send hands straight to the parked
// receiver and the race is essentially never entered; at eight callers
// the defect is caught about one run in three, and at sixty-four every
// run of ten. What actually HOLDS the property is structural -- the
// context is no longer cancelled by the answer, so it can only be done at
// the real deadline, and the peek prefers the answer even then -- but a
// structural argument is not a regression test, and this is the shape
// that fails when the structure goes.
func TestAwaitCheck_AnAnswerThatArrivedIsNeverCalledATimeout(t *testing.T) {
	var wg sync.WaitGroup
	bad := make(chan string, 1)
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 1000 {
				got, ok := awaitCheck(nil, time.Minute, func(context.Context) int { return 7 })
				if !ok || got != 7 {
					select {
					case bad <- fmt.Sprintf("iteration %d answered (%d, %v), want (7, true)", i, got, ok):
					default:
					}
					return
				}
			}
		}()
	}
	wg.Wait()
	select {
	case msg := <-bad:
		t.Fatalf("%s: an answer in hand was reported as a deadline", msg)
	default:
	}
}

// TestBaseSettle_PickingTheHeadRowAlsoRetiresTheUnknown is the seam
// between this change and #268, which landed beside it: a pick of the HEAD
// row is a decision that changes no value, so #268 gave WorktreeField a
// BasePicked flag for the event rather than its (absent) effect. This
// change reads the base a different way -- baseUnknown compares its stored
// ref against RequestedBase() -- and the two have to agree, because the
// line the timeout writes says "pick a base" and the HEAD row is one of
// the things a user can pick in answer.
//
// Driven through the real keys, as #268's own tests are, rather than by
// setting the field: the point is what the user's decision does.
func TestBaseSettle_PickingTheHeadRowAlsoRetiresTheUnknown(t *testing.T) {
	m := timedOutBaseSettle(t, "old-branch")
	m.worktree.SetBaseItems(1, []string{"old-branch", "main"})
	if got := m.worktree.RequestedBase(); got != "old-branch" {
		t.Fatalf("test setup: the base in play is %q, want the ref the check gave up on", got)
	}

	// pickBase only travels down, and the HEAD row is row 0.
	m.form.FocusByID("worktree")
	for range 2 {
		next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		m = next.(Model)
	}
	for range 12 {
		if m.worktree.Base() == "" && m.worktree.BasePicked() {
			break
		}
		next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
		m = next.(Model)
	}
	if !m.worktree.BasePicked() || m.worktree.Base() != "" {
		t.Fatalf("test setup: base %q, picked %v; want the HEAD row, chosen", m.worktree.Base(), m.worktree.BasePicked())
	}
	m.reactToChanges()

	if m.baseUnknown() {
		t.Error("the unknown outlived a base the user replaced with the HEAD row")
	}
	panel := ansi.Strip(m.worktree.Panel(80, m.worktree.PanelRows()))
	if strings.Contains(panel, "couldn't check") {
		t.Errorf("worktree panel = %q, want nothing about a base the user has left", panel)
	}
}

// TestBaseUnknown_TheFooterDoesNotOfferToKeepAnUncheckedBase is #269 meeting
// #202, and the collision is entirely in what the footer says.
//
// A base whose check timed out refuses the submit until the VALUE moves:
// baseUnknown compares the unchecked ref against the base in play, so only
// picking a different one clears it. ↵ is the one pick that does not move
// the value -- it commits the row already under the cursor -- so under a
// line reading "couldn't check <ref>: pick a base" it is an offer to do the
// one thing that cannot help. The user would press the key the footer
// taught them, watch the rung disappear, and find the refusal and the line
// exactly as they were.
//
// The at-top case is clean without this and stays so: there ↵ retires a
// hold, RequestedBase moves from the held ref to "", and the unknown clears
// on its own. This is about the other arm.
func TestBaseUnknown_TheFooterDoesNotOfferToKeepAnUncheckedBase(t *testing.T) {
	m := timedOutBaseSettle(t, "old-branch")
	m.worktree.SetOn(true)
	m.worktree.SetBaseItems(9, []string{"main", "old-branch", "develop"})
	m.worktree.SetBase("old-branch")
	m.form.FocusByID("worktree")
	for range 2 { // chips -> branch -> base
		next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		m = next.(Model)
	}
	// Genuinely the state: below the top row, nothing picked by hand, and
	// the unknown standing. Without the first two the rung would be absent
	// for a reason that has nothing to do with the refusal.
	if m.worktree.BasePicked() || !m.baseUnknown() {
		t.Fatalf("setup: BasePicked = %v, baseUnknown = %v, want an unchecked base nobody has picked",
			m.worktree.BasePicked(), m.baseUnknown())
	}
	rung := m.worktree.FooterRungs()[0]
	// Loose on purpose: every wording of this part's rung names a base, so
	// the guard holds whichever branch the field takes and the assertion
	// below is what actually fires.
	if !strings.Contains(rung, "base") {
		t.Fatalf("setup: rung = %q, want the base part's own rung", rung)
	}
	if strings.Contains(rung, "↵") {
		t.Errorf("rung under a refusal = %q, want no ↵: the panel is asking for a different base and ↵ keeps this one", rung)
	}
}
