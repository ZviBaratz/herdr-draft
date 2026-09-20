package app

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/ZviBaratz/herdr-draft/internal/clauth"
	"github.com/ZviBaratz/herdr-draft/internal/form/widgets"
)

// #200: a clauth that failed as the popup opened, and works on the reload.
//
// The row is built in its unavailable state carrying openTimeReason, and
// nothing ever cleared it: SetProfiles does not, and SetUnavailable -- the
// only writer -- is called by New alone. So the row read `unavailable
// <reason>` for the rest of the popup, and since #191 it ignores input
// too, so none of the profiles the reload had just loaded could be pinned.
const openTimeReason = "exit status 1: could not read ~/.clauth/state"

// unavailableAccountSetup is that state: clauth installed, broken at open,
// and answering `reload` afterwards. frameSetup(true) is the base so these
// tests render the same assembled form every frame in this package does.
func unavailableAccountSetup(reload *fakeClauth) testSetup {
	setup := frameSetup(true)
	setup.Clauth = reload
	setup.ClauthStatus = clauth.Status{}
	setup.ClauthUnavailable = openTimeReason
	return setup
}

// clickAccountRow focuses the account row the only way a user can while it
// is unavailable, and runs whatever that focus change scheduled.
//
// The mouse is not a stylistic choice here. Enabled() is false while the
// row is unavailable, so the focus ring skips it (focus.go's nextEnabled)
// and no amount of ⇥ ever lands there; form.Model's click path focuses a
// row "regardless of that section's Enabled() state" (form.go's
// handleMouseClick), which is what makes spec §11's reload-on-account-focus
// reachable at all for the one row that needs it. A test that reached for
// FocusByID instead would pass over a form no user can drive.
func clickAccountRow(t *testing.T, m Model) (Model, clauthResultMsg) {
	t.Helper()
	_ = m.form.ViewAt(framePopupW, framePopupH)
	syncZones()
	zi := widgets.Zones.Get("field:account")
	if zi.IsZero() {
		t.Fatal("the account row's own mouse zone never resolved after a render")
	}
	next, cmd := m.Update(tea.MouseClickMsg{X: zi.StartX, Y: zi.StartY, Button: tea.MouseLeft})
	m = next.(Model)
	if got := m.form.FocusedID(); got != "account" {
		t.Fatalf("a click on the account row left focus on %q", got)
	}
	for _, msg := range flatten(cmd) {
		if res, ok := msg.(clauthResultMsg); ok {
			return m, res
		}
	}
	t.Fatal("clicking the unavailable account row scheduled no clauth reload")
	return m, clauthResultMsg{}
}

// accountRow is the one line the account section renders in the stack --
// the text the whole of #200 is about.
func accountRow(t *testing.T, m Model) string {
	t.Helper()
	for _, line := range strings.Split(focusedFrame(t, m, "account"), "\n") {
		if strings.Contains(line, "account") {
			return line
		}
	}
	t.Fatalf("no account row in the rendered form (sections: %v)", m.form.SectionIDs())
	return ""
}

// TestClauthReload_TwoProfilesMakeTheRowLive is #200's own repro, end to
// end through the real Update loop: click the unavailable row, let the
// reload it schedules run against a clauth that now answers, and pin a
// profile with ↓ ↓ ↵ -- the exact gesture the independent review of #191
// drove, and the one that did nothing at all before this.
func TestClauthReload_TwoProfilesMakeTheRowLive(t *testing.T) {
	m := resolveDirCheck(t, newTestModel(t, unavailableAccountSetup(&fakeClauth{status: frameClauthStatus()})))
	if m.account == nil {
		t.Fatal("setup: an installed-but-broken clauth built no account row")
	}

	m, res := clickAccountRow(t, m)
	next, _ := m.Update(res)
	m = next.(Model)

	if !m.account.Enabled() {
		t.Errorf("the account row is still inert after a reload that found %d profiles", len(frameClauthStatus().Profiles))
	}
	if row := accountRow(t, m); strings.Contains(row, "unavailable") {
		t.Errorf("the account row still reads unavailable after a good reload:\n%s", row)
	}

	press := func(k tea.KeyPressMsg) {
		next, _ := m.Update(k)
		m = next.(Model)
	}
	m.form.FocusByID("account")
	press(key(tea.KeyDown, 0))
	press(key(tea.KeyDown, 0))
	press(key(tea.KeyEnter, 0))
	if got, want := m.PlanInput().AccountPin, "work"; got != want {
		t.Errorf("AccountPin after ↓ ↓ ↵ on the recovered row = %q, want %q", got, want)
	}
}

// TestClauthReload_TheRecoveredRowIsTheOneOpenWouldHaveBuilt is the
// decision's own wording: "the row becomes live and can be pinned, as it
// would have been had clauth answered at open."
//
// Everything New does for a live row beyond loading the profiles has to
// happen here too -- the auto row, `[clauth] default` and the standing
// notes -- so the recovered row is rendered beside the row a working
// clauth would have opened on and compared byte for byte. A picker that
// failed its probe is in the fixture because its reason is a note rather
// than a profile: it is the piece most easily left behind by a fix that
// only re-feeds the profile list.
//
// Be clear about what that comparison can and cannot catch, because it
// reads stronger than it is (independent review of #200). Both sides go
// through populateAccountRow, so a change INSIDE that function moves both
// frames together and this assertion stays silent; what it catches is the
// recovery path ceasing to call it, or calling it differently. The golden
// frame below is what pins the contents, and the assertions beside it --
// the configured default, the note, and the auto row with a working
// picker -- are deliberately about the row's own state rather than about
// the two rows agreeing.
func TestClauthReload_TheRecoveredRowIsTheOneOpenWouldHaveBuilt(t *testing.T) {
	reloaded := &fakeClauth{status: frameClauthStatus()}
	recovering := unavailableAccountSetup(reloaded)
	recovering.Config.Clauth.Default = "work"
	recovering.PickerUnavailable = pickerProbeReason

	opened := recovering
	opened.ClauthStatus = frameClauthStatus()
	opened.ClauthUnavailable = ""

	m := resolveDirCheck(t, newTestModel(t, recovering))
	m, res := clickAccountRow(t, m)
	next, _ := m.Update(res)
	m = next.(Model)

	live := resolveDirCheck(t, newTestModel(t, opened))
	if got, want := focusedFrame(t, m, "account"), focusedFrame(t, live, "account"); got != want {
		t.Errorf("the recovered account row is not the row a working clauth would have opened on.\ngot:\n%s\nwant:\n%s", got, want)
	}
	if got := m.account.Pin(); got != "work" {
		t.Errorf("Pin() on the recovered row = %q, want the configured default %q", got, "work")
	}
	if frame := focusedFrame(t, m, "account"); !strings.Contains(frame, pickerProbeHead) {
		t.Errorf("the recovered row does not carry the note saying why there is no auto row:\n%s", frame)
	}

	assertAppFrame(t, "assembled-clauth-recovered-101x30", m, framePopupW, framePopupH)

	// And the auto row, which the fixture above cannot show because its
	// picker failed its probe. A working one makes auto the resting
	// selection, and a recovered row has to get that too.
	withPicker := unavailableAccountSetup(&fakeClauth{status: frameClauthStatus()})
	withPicker.Picker = &fakePicker{}
	p := resolveDirCheck(t, newTestModel(t, withPicker))
	p, pres := clickAccountRow(t, p)
	pnext, _ := p.Update(pres)
	p = pnext.(Model)
	if !p.account.IsAuto() {
		t.Error("the recovered row has no auto row, with a picker configured and probed")
	}
}

// TestClauthReload_FewerThanTwoKeepsItUnavailableWithANewReason: a reload
// that works but finds fewer than two profiles is the one state open never
// produces -- with one profile there is no row at all, by design. The row
// is already on screen, so it stays, and what it says has to change: the
// open-time reason is no longer true, and "clauth works, there is just
// nothing to choose between" is what the user can act on.
func TestClauthReload_FewerThanTwoKeepsItUnavailableWithANewReason(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status clauth.Status
		want   string
	}{
		{"one profile", clauth.Status{Schema: 1, Profiles: []clauth.Profile{{Name: "personal"}}}, "clauth reports one profile; this row needs two"},
		{"no profiles", clauth.Status{Schema: 1}, "clauth reports no profiles; this row needs two"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := resolveDirCheck(t, newTestModel(t, unavailableAccountSetup(&fakeClauth{status: tc.status})))
			m, res := clickAccountRow(t, m)
			next, _ := m.Update(res)
			m = next.(Model)

			if m.account == nil {
				t.Fatal("the account row vanished on a reload that found too few profiles")
			}
			if m.account.Enabled() {
				t.Error("the account row went live on a reload that found fewer than two profiles")
			}
			row := accountRow(t, m)
			if !strings.Contains(row, tc.want) {
				t.Errorf("the account row = %q, want the new reason %q", row, tc.want)
			}
			if strings.Contains(row, openTimeReason) {
				t.Errorf("the account row still carries the open-time reason:\n%s", row)
			}
		})
	}
}

// TestClauthReload_TooFewProfilesFrame fixtures what that row draws, panel
// and all -- CLAUDE.md: "a golden-frame suite proves only the states
// someone thought to fixture."
func TestClauthReload_TooFewProfilesFrame(t *testing.T) {
	one := clauth.Status{Schema: 1, Profiles: []clauth.Profile{{Name: "personal", Active: true, Tier: "max", AuthStatus: "ok"}}}
	m := resolveDirCheck(t, newTestModel(t, unavailableAccountSetup(&fakeClauth{status: one})))
	m, res := clickAccountRow(t, m)
	next, _ := m.Update(res)
	m = next.(Model)
	m.form.FocusByID("account")

	assertAppFrame(t, "assembled-clauth-too-few-101x30", m, framePopupW, framePopupH)
}

// TestClauthReload_AFailedReloadReplacesTheReason: the two failures can
// differ -- crashed at open, missing afterwards, or the reverse -- and the
// newer one is the one the user can act on. Before this the row kept
// saying why clauth failed when the popup opened, however long ago that
// was and whatever clauth was doing now.
func TestClauthReload_AFailedReloadReplacesTheReason(t *testing.T) {
	gone := errors.New(`clauth status --json: exec: "clauth": executable file not found in $PATH`)
	m := resolveDirCheck(t, newTestModel(t, unavailableAccountSetup(&fakeClauth{err: gone})))
	m, res := clickAccountRow(t, m)
	next, _ := m.Update(res)
	m = next.(Model)

	row := accountRow(t, m)
	if !strings.Contains(row, `executable file not found in $PATH`) {
		t.Errorf("the account row = %q, want the reason the reload failed with", row)
	}
	if strings.Contains(row, openTimeReason) {
		t.Errorf("the account row still carries the open-time reason:\n%s", row)
	}
	if m.account.Enabled() {
		t.Error("the account row went live on a failed reload")
	}

	assertAppFrame(t, "assembled-clauth-reload-failed-101x30", m, framePopupW, framePopupH)
}

// TestClauthReload_AFailedReloadSurvivesAClear: ⌃R⌃R rebuilds the form
// through New from the Model's own state, so a reason written only into
// the field would be replaced by the open-time one on the first clear --
// the same gap TestAccountReasons_ClearKeepsAnUnreadableClauth closed for
// the picker's reason.
func TestClauthReload_AFailedReloadSurvivesAClear(t *testing.T) {
	const now = "exit status 2: ~/.clauth/state is not valid JSON"
	m := resolveDirCheck(t, newTestModel(t, unavailableAccountSetup(&fakeClauth{err: errors.New("clauth status --json: " + now)})))
	m, res := clickAccountRow(t, m)
	next, _ := m.Update(res)
	m = next.(Model)

	row := accountRow(t, cleared(t, m))
	if !strings.Contains(row, now) {
		t.Errorf("after ⌃R⌃R the account row = %q, want the reason the reload failed with", row)
	}
}

// TestClauthReload_ARecoveredRowSurvivesAClear is that same rule the other
// way round: once clauth has answered, a clear must not put the broken row
// back. New's own gate is `>= 2 profiles`, so this holds only if the
// reload's status was recorded on the Model as well as in the field.
func TestClauthReload_ARecoveredRowSurvivesAClear(t *testing.T) {
	m := resolveDirCheck(t, newTestModel(t, unavailableAccountSetup(&fakeClauth{status: frameClauthStatus()})))
	m, res := clickAccountRow(t, m)
	next, _ := m.Update(res)
	m = next.(Model)

	m = cleared(t, m)
	if m.account == nil {
		t.Fatal("⌃R⌃R removed the recovered account row entirely")
	}
	if !m.account.Enabled() {
		t.Error("⌃R⌃R put the unavailable account row back after a good reload")
	}
	if row := accountRow(t, m); strings.Contains(row, openTimeReason) {
		t.Errorf("⌃R⌃R restored the open-time reason:\n%s", row)
	}
}

// TestClauthReload_AStaleResultMovesNothing: there is no debounce phase
// for this source, so a slow reload's answer can land after a fresher one
// was scheduled (clauthResultMsg's own doc comment). It must move nothing
// -- neither the state nor the reason -- which is the half of the version
// check a fix that writes on every result is most likely to lose.
func TestClauthReload_AStaleResultMovesNothing(t *testing.T) {
	m := resolveDirCheck(t, newTestModel(t, unavailableAccountSetup(&fakeClauth{status: frameClauthStatus()})))
	m, res := clickAccountRow(t, m)

	stale := res
	stale.version = res.version - 1
	before := accountRow(t, m)
	next, _ := m.Update(stale)
	m = next.(Model)

	if m.account.Enabled() {
		t.Error("a stale reload made the account row live")
	}
	if got := accountRow(t, m); got != before {
		t.Errorf("a stale reload moved the account row:\n got: %s\nwant: %s", got, before)
	}

	// And the current one still lands.
	next, _ = m.Update(res)
	m = next.(Model)
	if !m.account.Enabled() {
		t.Error("the current reload no longer lands after a stale one arrived first")
	}
}

// TestClauthReload_ALiveRowIsLeftAloneByAFailure is the boundary of all of
// the above. Everything #200 decided is about a row that is ALREADY
// unavailable: a row that opened live keeps today's behaviour, where a
// failed reload changes nothing at all.
//
// Making a live row unavailable on a failure would be worse than the bug:
// Pin() keeps whatever was pinned (SetUnavailable does not clear it) and
// accountPin does not consult the unavailable state, so the session would
// launch under an account the row had stopped showing.
func TestClauthReload_ALiveRowIsLeftAloneByAFailure(t *testing.T) {
	cl := &fakeClauth{status: frameClauthStatus()}
	setup := frameSetup(true)
	setup.Clauth = cl
	m := resolveDirCheck(t, newTestModel(t, setup))

	// The failure is the fake's, not a hand-built message: the reload has
	// to fail the way a real one does, through reloadClauthCmd itself.
	cl.err = errors.New("clauth status --json: exit status 1")
	m, res := clickAccountRow(t, m)
	before := accountRow(t, m)
	next, _ := m.Update(res)
	m = next.(Model)

	if !m.account.Enabled() {
		t.Error("a failed reload made a working account row inert")
	}
	if got := accountRow(t, m); got != before {
		t.Errorf("a failed reload moved a working account row:\n got: %s\nwant: %s", got, before)
	}
}
