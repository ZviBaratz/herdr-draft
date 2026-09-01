package form

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ZviBaratz/herdr-draft/internal/clauth"
	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

func sampleStatus() clauth.Status {
	return clauth.Status{
		Schema:        1,
		ActiveProfile: "alpha",
		Profiles: []clauth.Profile{
			{
				Name: "alpha", Active: true, Tier: "Team", AuthStatus: "ok",
				Windows: []clauth.Window{{Label: "5h", UtilizationPct: 12}, {Label: "7d", UtilizationPct: 40}},
			},
			{
				Name: "beta", Active: false, Tier: "Max 20x", AuthStatus: "expired",
				Windows: []clauth.Window{{Label: "5h", UtilizationPct: 0}},
			},
			{
				Name: "gamma", Active: false, Tier: "Team", AuthStatus: "ok",
				Windows: []clauth.Window{{Label: "5h", UtilizationPct: 100}},
			},
		},
	}
}

func TestAccountField_IDAndDefaultInert(t *testing.T) {
	f := NewAccountField(theme.Default())
	if f.ID() != "account" {
		t.Errorf("ID() = %q, want %q", f.ID(), "account")
	}
	if f.Enabled() {
		t.Errorf("Enabled() = true on a fresh field, want false (inert until SetAgentIsClaude, same conservative default as WorktreeField.Enabled)")
	}
	if got := f.Pin(); got != "" {
		t.Errorf("Pin() on a fresh field = %q, want \"\" (active)", got)
	}
}

// TestAccountField_InertFlipsWithAgentKind pins the brief's own literal
// requirement: "account inert flips with agent kind".
func TestAccountField_InertFlipsWithAgentKind(t *testing.T) {
	f := NewAccountField(theme.Default())
	f.SetProfiles(sampleStatus())

	f.SetAgentIsClaude(false)
	if f.Enabled() {
		t.Errorf("Enabled() = true after SetAgentIsClaude(false), want false")
	}
	inertFrame := ansi.Strip(f.View(60))
	if !strings.Contains(inertFrame, accountInertPlaceholder) {
		t.Errorf("View(60) while inert = %q, want it to contain %q", inertFrame, accountInertPlaceholder)
	}

	f.SetAgentIsClaude(true)
	if !f.Enabled() {
		t.Errorf("Enabled() = false after SetAgentIsClaude(true), want true")
	}
	claudeFrame := ansi.Strip(f.View(60))
	if strings.Contains(claudeFrame, accountInertPlaceholder) {
		t.Errorf("View(60) after re-enabling still contains the inert placeholder: %q", claudeFrame)
	}

	f.SetAgentIsClaude(false)
	if f.Enabled() {
		t.Errorf("Enabled() = true after flipping back to false, want false")
	}
}

func TestAccountField_PinCyclesThroughProfiles(t *testing.T) {
	f := NewAccountField(theme.Default())
	f.SetAgentIsClaude(true)
	f.SetProfiles(sampleStatus())

	f.Update(key(tea.KeyDown, 0)) // active -> alpha
	if got := f.Pin(); got != "alpha" {
		t.Fatalf("Pin() after one Down = %q, want %q", got, "alpha")
	}
	f.Update(key(tea.KeyDown, 0)) // alpha -> beta
	if got := f.Pin(); got != "beta" {
		t.Fatalf("Pin() after two Downs = %q, want %q", got, "beta")
	}
	f.Update(key(tea.KeyUp, 0))
	f.Update(key(tea.KeyUp, 0))
	if got := f.Pin(); got != "" {
		t.Fatalf("Pin() back at the top = %q, want \"\" (active)", got)
	}
}

// TestAccountField_SetProfilesRefreshPreservesPinByName mirrors
// field_worktree.go's identical same-version-refresh test: a later
// SetProfiles call (e.g. a re-poll on account focus, spec §8) must not
// silently bounce the user's pin back to "active".
func TestAccountField_SetProfilesRefreshPreservesPinByName(t *testing.T) {
	f := NewAccountField(theme.Default())
	f.SetAgentIsClaude(true)
	f.SetProfiles(sampleStatus())
	f.Update(key(tea.KeyDown, 0))
	f.Update(key(tea.KeyDown, 0))
	if got := f.Pin(); got != "beta" {
		t.Fatalf("setup: Pin() = %q, want %q", got, "beta")
	}

	// A re-poll with reordered profiles.
	refreshed := sampleStatus()
	refreshed.Profiles[0], refreshed.Profiles[1] = refreshed.Profiles[1], refreshed.Profiles[0]
	f.SetProfiles(refreshed)

	if got := f.Pin(); got != "beta" {
		t.Errorf("Pin() after a reordering SetProfiles refresh = %q, want %q (preserved by name)", got, "beta")
	}
}

// TestAccountField_DegradedRendersNameOnly pins spec §11's own contract:
// schema != 1 must degrade every row beyond Name.
func TestAccountField_DegradedRendersNameOnly(t *testing.T) {
	f := NewAccountField(theme.Default())
	f.SetAgentIsClaude(true)
	status := sampleStatus()
	status.Degraded = true
	f.SetProfiles(status)

	frame := ansi.Strip(f.View(60))
	if strings.Contains(frame, "Team") || strings.Contains(frame, "expired") {
		t.Errorf("View(60) while degraded = %q, want no tier/auth_status text", frame)
	}
	if !strings.Contains(frame, "alpha") {
		t.Errorf("View(60) while degraded = %q, want the profile name still shown", frame)
	}
}

// TestAccountField_WarnsOnAuthFailedAndRateLimited pins spec §6 field 7's
// "visibly marked" contract for both conditions the brief names.
func TestAccountField_WarnsOnAuthFailedAndRateLimited(t *testing.T) {
	f := NewAccountField(theme.Default())
	f.SetAgentIsClaude(true)
	f.SetProfiles(sampleStatus())

	frame := ansi.Strip(f.View(60))
	if !strings.Contains(frame, accountWarnAuthFailed) {
		t.Errorf("View(60) = %q, want it to contain the auth-failed marker for beta", frame)
	}
	if !strings.Contains(frame, accountWarnRateLimited) {
		t.Errorf("View(60) = %q, want it to contain the rate-limited marker for gamma", frame)
	}
}

// TestAccountField_HealthyProfileCarriesNoWarning is the negative case:
// alpha (auth ok, well under 100%) must not be marked.
func TestAccountField_HealthyProfileCarriesNoWarning(t *testing.T) {
	f := NewAccountField(theme.Default())
	status := clauth.Status{Profiles: []clauth.Profile{
		{Name: "solo", Tier: "Team", AuthStatus: "ok", Windows: []clauth.Window{{Label: "5h", UtilizationPct: 12}}},
	}}
	f.SetAgentIsClaude(true)
	f.SetProfiles(status)

	frame := ansi.Strip(f.View(60))
	if strings.Contains(frame, accountWarnAuthFailed) || strings.Contains(frame, accountWarnRateLimited) {
		t.Errorf("View(60) = %q, want no warning markers for a healthy profile", frame)
	}
	if !strings.Contains(frame, "5h 12%") {
		t.Errorf("View(60) = %q, want the 5h utilization shown", frame)
	}
}

func TestAccountField_HeightIsConstant(t *testing.T) {
	f := NewAccountField(theme.Default())
	base := f.Height(24)

	f.SetProfiles(sampleStatus())
	f.SetAgentIsClaude(true)
	if got := f.Height(24); got != base {
		t.Errorf("Height(24) after enabling = %d, want %d", got, base)
	}
	if got := strings.Count(f.View(60), "\n") + 1; got != base {
		t.Errorf("View(60) rendered %d physical lines, want Height()'s own %d", got, base)
	}

	f.SetAgentIsClaude(false)
	if got := f.Height(24); got != base {
		t.Errorf("Height(24) while inert = %d, want %d", got, base)
	}
	if got := strings.Count(f.View(60), "\n") + 1; got != base {
		t.Errorf("View(60) while inert rendered %d physical lines, want Height()'s own %d", got, base)
	}
}

func TestAccountField_NoPanicOnDegenerateWidth(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("AccountField panicked: %v", r)
		}
	}()
	f := NewAccountField(theme.Default())
	_ = f.View(0)
	_ = f.View(-3)
}

func TestAccountField_NoPanicBeforeSetProfiles(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("AccountField panicked before SetProfiles: %v", r)
		}
	}()
	f := NewAccountField(theme.Default())
	f.SetAgentIsClaude(true)
	_ = f.View(60)
	f.Update(key(tea.KeyDown, 0))
	f.Update(key(tea.KeyUp, 0))
	_ = f.Pin()
}
