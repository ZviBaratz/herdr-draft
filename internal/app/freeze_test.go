package app

import (
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/form"
	"github.com/ZviBaratz/herdr-draft/internal/form/widgets"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
	"github.com/ZviBaratz/herdr-draft/internal/linear"
	"github.com/ZviBaratz/herdr-draft/internal/picker"
)

// These pin #136: once a submit has passed validation it may still make a
// round trip before it builds the plan -- the `auto` account pick, or the
// lane's commit (#171) -- and the form is frozen for it. What the user did
// meanwhile used to reach the plan that validation had already passed: a
// second ⌃S spent a second pick, a ⌃R⌃R submitted the rebuilt form, and an
// edit was built from the previous value's answers (#195's shape). esc still
// cancels.

// autoPickForm is a settled form with a title and an `auto` account row
// whose picker is p, and the ⌃S, sent from the title row, that has sent it
// to the pick.
func autoPickForm(t *testing.T, p *fakePicker) (Model, tea.Cmd) {
	t.Helper()
	return autoPickFormFrom(t, p, "title")
}

// autoPickFormFrom is autoPickForm with ⌃S sent from the row focusID. The
// project row offers a recent project, /elsewhere, after /repo.
func autoPickFormFrom(t *testing.T, p *fakePicker, focusID string) (Model, tea.Cmd) {
	t.Helper()
	cfg := config.Config{}
	cfg.Clauth.Picker = "stub"
	m := settle(t, newTestModel(t, testSetup{
		Ctx:          herdrc.Context{WorkspaceCwd: "/repo"},
		State:        config.State{Recents: []string{"/elsewhere"}},
		Clauth:       &fakeClauth{status: twoProfileStatus()},
		ClauthStatus: twoProfileStatus(),
		Picker:       p,
		Config:       cfg,
	}))
	m.title.SetTitle("Fix pagination", false)
	if m.account == nil || !m.account.IsAuto() {
		t.Fatal("test setup: the account row is not auto")
	}
	// Sized, so it renders and its mouse zones resolve. The reaction that
	// follows notices the title and checks it, and a submit waits for that
	// check (#137), so it lands first.
	next, sized := m.Update(tea.WindowSizeMsg{Width: 104, Height: 32})
	m = landTitle(t, next.(Model), []tea.Cmd{sized})
	m.form.FocusByID(focusID)
	next, pick := m.Update(form.SubmitMsg{})
	m = next.(Model)
	if m.submitting || pick == nil {
		t.Fatal("test setup: the submit did not stop for the commit-time pick")
	}
	return m, pick
}

var pickedAlpha = picker.Result{Profile: "alpha-1", ConfigDir: "/dirs/alpha-1"}

// syncZones waits for bubblezone to record the zones a render marked, which
// it does off the render call -- internal/form's mouse tests wait the same.
func syncZones() { time.Sleep(50 * time.Millisecond) }

// TestSubmit_ASecondSubmitDuringThePickSpendsNoSecondPick is #136's first
// scenario: a double ⌃S ran two commit-time picks, and each writes a ledger
// entry, for one session.
func TestSubmit_ASecondSubmitDuringThePickSpendsNoSecondPick(t *testing.T) {
	p := &fakePicker{res: pickedAlpha}
	m, pick := autoPickForm(t, p)

	next, again := m.Update(form.SubmitMsg{})
	m = next.(Model)
	for _, cmd := range []tea.Cmd{pick, again} {
		flatten(cmd)
	}
	if len(p.calls) != 1 {
		t.Fatalf("commit-time picks = %d, want 1: a second ⌃S during the pick was not ignored", len(p.calls))
	}
}

// TestSubmit_AClickDuringThePickDoesNotReachThePlan: a click selects a value
// as a keystroke does -- here the project row's second candidate, from the
// row's own panel -- so the mouse is frozen too.
func TestSubmit_AClickDuringThePickDoesNotReachThePlan(t *testing.T) {
	m, pick := autoPickFormFrom(t, &fakePicker{res: pickedAlpha}, "dir")
	if got := m.dir.Value(); got != "/repo" {
		t.Fatalf("test setup: project = %q, want /repo", got)
	}
	_ = m.View()
	syncZones()
	zi := widgets.Zones.Get("row:dir:1")
	if zi.IsZero() {
		t.Fatal("test setup: the project panel's second row never resolved")
	}

	next, _ := m.Update(tea.MouseClickMsg{X: zi.StartX, Y: zi.StartY, Button: tea.MouseLeft})
	m = next.(Model)
	next, _ = m.Update(pick())
	m = next.(Model)
	if !m.submitting {
		t.Fatal("the pick landed and the submit did not go on")
	}
	if got := m.submitInput.ProjectDir; got != "/repo" {
		t.Errorf("submitted project %q, want /repo, the project validation passed", got)
	}
}

// TestSubmit_KeystrokesDuringThePickDoNotReachThePlan: the submit builds what
// the form held when it passed validation. Typing or pasting during the pick
// used to change it, unvalidated.
func TestSubmit_KeystrokesDuringThePickDoNotReachThePlan(t *testing.T) {
	m, pick := autoPickForm(t, &fakePicker{res: pickedAlpha})
	m.form.FocusByID("title")
	for _, r := range " twice" {
		next, _ := m.Update(rn(r))
		m = next.(Model)
	}
	next, _ := m.Update(tea.PasteMsg{Content: " pasted"})
	m = next.(Model)

	next, _ = m.Update(pick())
	m = next.(Model)
	if !m.submitting {
		t.Fatal("the pick landed and the submit did not go on")
	}
	if got := m.submitInput.Title; got != "Fix pagination" {
		t.Errorf("submitted title %q, want %q, the title validation passed", got, "Fix pagination")
	}
}

// TestSubmit_AClearOrAnIssueDuringThePickIsIgnored is #136's third scenario:
// a ⌃R⌃R during the pick rebuilt the form, and the pick then submitted the
// rebuilt one, which validation had never seen. A Linear issue chosen then
// is the same: it seeds the title.
func TestSubmit_AClearOrAnIssueDuringThePickIsIgnored(t *testing.T) {
	for name, msg := range map[string]tea.Msg{
		"a clear":  form.ClearRequestedMsg{},
		"an issue": form.IssueChosenMsg{Issue: &linear.Issue{Identifier: "ENG-9", Title: "another task"}},
	} {
		t.Run(name, func(t *testing.T) {
			m, pick := autoPickForm(t, &fakePicker{res: pickedAlpha})
			next, _ := m.Update(msg)
			m = next.(Model)
			next, _ = m.Update(pick())
			m = next.(Model)
			if !m.submitting {
				t.Fatal("the pick landed and the submit did not go on")
			}
			if got := m.submitInput.Title; got != "Fix pagination" {
				t.Errorf("submitted title %q, want %q: %s reached the form during the pick", got, "Fix pagination", name)
			}
		})
	}
}

// TestSubmit_EscDuringThePickStillCancels: the freeze keeps the way out.
func TestSubmit_EscDuringThePickStillCancels(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{{Code: tea.KeyEscape}, {Code: 'c', Mod: tea.ModCtrl}} {
		m, _ := autoPickForm(t, &fakePicker{res: pickedAlpha})
		next, cmd := m.Update(key)
		m = next.(Model)
		quit := false
		for _, msg := range flatten(cmd) {
			if msg == (form.CancelMsg{}) {
				_, cmd = m.Update(msg)
				msg = cmd()
			}
			if _, ok := msg.(tea.QuitMsg); ok {
				quit = true
			}
		}
		if !quit {
			t.Errorf("%s during the pick did not quit", key.String())
		}
	}
}

// TestSubmit_ARefusedPickUnfreezesTheForm: a pick that refuses sends the user
// back to the account row, and the form is theirs again.
func TestSubmit_ARefusedPickUnfreezesTheForm(t *testing.T) {
	p := &fakePicker{err: errors.New("no account fits")}
	m, pick := autoPickForm(t, p)

	next, _ := m.Update(pick())
	m = next.(Model)
	if m.submitting {
		t.Fatal("test setup: a refused pick went on")
	}
	m.form.FocusByID("title")
	next, typed := m.Update(rn('!'))
	m = next.(Model)
	if got := m.title.Value(); got != "Fix pagination!" {
		t.Fatalf("title after typing = %q: the form stayed frozen after the pick refused", got)
	}
	m = landTitle(t, m, []tea.Cmd{typed})
	p.err = nil
	p.res = pickedAlpha
	next, again := m.Update(form.SubmitMsg{})
	m = next.(Model)
	if again == nil {
		t.Fatal("a ⌃S after the refusal started no pick: the form stayed frozen")
	}
}

// TestSubmit_KeystrokesDuringTheLaneCommitDoNotReachThePlan: the lane's commit
// is the other round trip (#171), frozen the same way. Typing a project
// during it used to build a worktree from the lane's answers for a path
// nothing had checked (#136's comment, #195's shape).
func TestSubmit_KeystrokesDuringTheLaneCommitDoNotReachThePlan(t *testing.T) {
	git := laneGit()
	m := laneModel(t, &submitFakeRunner{}, git, true)
	m.form.FocusByID("dir")

	next, readCommit := m.Update(form.SubmitMsg{})
	m = next.(Model)
	if m.submitting || readCommit == nil {
		t.Fatal("test setup: the submit did not stop to read the lane's commit")
	}
	for _, r := range "/nowhere" {
		next, _ = m.Update(rn(r))
		m = next.(Model)
	}

	next, _ = m.Update(readCommit())
	m = next.(Model)
	if !m.submitting {
		t.Fatal("the submit did not go on once the lane's commit was read")
	}
	if got := m.submitInput.ProjectDir; got != laneDir {
		t.Errorf("submitted project %q, want the lane %s that validation passed", got, laneDir)
	}
}
