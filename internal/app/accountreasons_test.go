package app

import (
	"strings"
	"testing"

	"github.com/ZviBaratz/herdr-draft/internal/clauth"
)

// pickerProbeReason is the shape picker.CLI.Probe reports for a named
// executable that does not implement the protocol, flattened the way
// Bootstrap flattens it. The executable is a neutral stand-in: fixtures in
// this repository name no one machine's tools.
const pickerProbeReason = "acct-pick answered without the documented keys config_dir, reason, usage.cache_age_s -- it does not implement the account-picker protocol"

// pickerProbeHead is the part of pickerProbeReason that names the picker and
// the failure: short enough to survive any panel these tests render, since a
// note elides at its tail.
const pickerProbeHead = "acct-pick answered without the documented keys"

// TestAccountReasons_PickerUnavailableSurvivesAPinnedDefault was found while
// fixing #123. Setup.PickerUnavailable reached the account row as a verdict
// keyed on the unpinned "", and a verdict renders only while its key matches
// Pin() -- so a `[clauth] default` naming a real profile hid the reason, and a
// picker that was configured and not working looked exactly like one that was
// never configured, which is the one thing the reason exists to prevent.
func TestAccountReasons_PickerUnavailableSurvivesAPinnedDefault(t *testing.T) {
	setup := frameSetup(true)
	setup.PickerUnavailable = pickerProbeReason
	setup.Config.Clauth.Default = "work"

	m := resolveDirCheck(t, newTestModel(t, setup))
	if got := m.account.Pin(); got != "work" {
		t.Fatalf("Pin() = %q, want the configured default %q", got, "work")
	}
	if frame := focusedFrame(t, m, "account"); !strings.Contains(frame, pickerProbeHead) {
		t.Errorf("a pinned default hid why the picker is not in use:\n%s", frame)
	}
}

// TestAccountReasons_ClearKeepsAPickerThatCouldNotBeTrusted: ⌃R⌃R rebuilds
// the form through New, and handleClearRequested did not hand New the
// picker's reason, so the first clear erased it.
func TestAccountReasons_ClearKeepsAPickerThatCouldNotBeTrusted(t *testing.T) {
	setup := frameSetup(true)
	setup.PickerUnavailable = pickerProbeReason

	m := cleared(t, resolveDirCheck(t, newTestModel(t, setup)))
	if frame := focusedFrame(t, m, "account"); !strings.Contains(frame, pickerProbeHead) {
		t.Errorf("⌃R⌃R erased why the picker is not in use:\n%s", frame)
	}
}

// TestAccountReasons_ClearKeepsAnUnreadableClauth is the worse half of the same
// gap. Without Setup.ClauthUnavailable, New builds no account row at all for a
// clauth with no profiles, so the first clear made an installed-but-broken
// clauth render exactly like no clauth -- the state spec §13 says must
// degrade "with a reason" rather than vanish.
//
// It asserts the cleared form against the frame TestAssembledForm_ClauthUnreadable
// already pins for the opened one: a clear has to put back the same screen,
// byte for byte, not merely something containing the reason.
func TestAccountReasons_ClearKeepsAnUnreadableClauth(t *testing.T) {
	setup := frameSetup(true)
	setup.ClauthStatus = clauth.Status{}
	setup.ClauthUnavailable = "exit status 1: could not read ~/.clauth/state"

	m := cleared(t, resolveDirCheck(t, newTestModel(t, setup)))
	if m.account == nil {
		t.Fatalf("⌃R⌃R removed the account row of an unreadable clauth (sections: %v)", m.form.SectionIDs())
	}
	m.form.FocusByID("agent")
	m.reactToChanges()

	assertAppFrame(t, "assembled-clauth-unreadable-101x30", m, framePopupW, framePopupH)
}

// TestAccountReasons_PickerReasonLeadsTheNotes pins the order of the account
// row's notes (review of #126). A short panel keeps only the first, and the
// picker's reason is the one that explains the list itself -- why there is no
// auto row -- so it comes ahead of config.toml's refused [clauth] keys.
func TestAccountReasons_PickerReasonLeadsTheNotes(t *testing.T) {
	loaded := loadConfigBody(t, "[clauth]\nlaunch = \"teleport\"\n")
	setup := frameSetup(true)
	setup.Config.Clauth = loaded.Clauth
	setup.Config.ClauthLaunchWarning = loaded.ClauthLaunchWarning
	setup.PickerUnavailable = pickerProbeReason

	const refusedKey = `ignoring [clauth] launch "teleport"`
	frame := focusedFrame(t, resolveDirCheck(t, newTestModel(t, setup)), "account")
	reason, refused := strings.Index(frame, pickerProbeHead), strings.Index(frame, refusedKey)
	switch {
	case reason < 0 || refused < 0:
		t.Fatalf("the account panel does not carry both %q and %q:\n%s", pickerProbeHead, refusedKey, frame)
	case reason > refused:
		t.Errorf("config.toml's refused key sits above why the picker is not in use:\n%s", frame)
	}
}

// TestAccountReasons_AWorkingPickerCarriesNoProbeFailure keeps the two picker
// states exclusive (review of #126). Bootstrap sets Deps.Picker or
// PickerUnavailable, never both, and the verdict the note replaced lived in
// the else-branch of a working picker -- so a working auto row was never
// reported beside a failed probe, and the note must not start doing it for
// the first caller of New that hands it both.
func TestAccountReasons_AWorkingPickerCarriesNoProbeFailure(t *testing.T) {
	setup := frameSetup(true)
	setup.Picker = &fakePicker{}
	setup.PickerUnavailable = pickerProbeReason

	m := resolveDirCheck(t, newTestModel(t, setup))
	if !m.account.IsAuto() {
		t.Fatal("precondition: a working picker should make auto the resting selection")
	}
	if frame := focusedFrame(t, m, "account"); strings.Contains(frame, pickerProbeHead) {
		t.Errorf("an account row with a working auto row also reports a failed probe:\n%s", frame)
	}
}
