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

// pickerProbeHead is as much of pickerProbeReason as an 80-cell panel keeps.
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
