package form

import (
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ZviBaratz/herdr-draft/internal/clauth"
	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// sampleNow is the wall clock every account fixture is measured against
// (v3 spec §10.2's reset times are relative, and internal/form takes its
// clock from the caller -- AccountField.SetProfiles). A FIXED instant,
// never time.Now: a golden frame reading `in 2h11m` has to be the same
// bytes tomorrow.
func sampleNow() time.Time {
	return time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
}

// resetIn is a *time.Time d from sampleNow, for a window that reports one.
func resetIn(d time.Duration) *time.Time {
	t := sampleNow().Add(d)
	return &t
}

func sampleStatus() clauth.Status {
	return clauth.Status{
		Schema:        1,
		ActiveProfile: "alpha",
		Profiles: []clauth.Profile{
			{
				Name: "alpha", Active: true, Tier: "Team", AuthStatus: "ok",
				Windows: []clauth.Window{
					{Label: "5h", UtilizationPct: 12, ResetsAt: resetIn(2*time.Hour + 11*time.Minute)},
					{Label: "7d", UtilizationPct: 40},
				},
			},
			{
				Name: "beta", Active: false, Tier: "Max 20x", AuthStatus: "expired",
				Windows: []clauth.Window{{Label: "5h", UtilizationPct: 0, ResetsAt: resetIn(45 * time.Minute)}},
			},
			{
				Name: "gamma", Active: false, Tier: "Team", AuthStatus: "ok",
				Windows: []clauth.Window{{Label: "5h", UtilizationPct: 100, ResetsAt: resetIn(time.Hour + 40*time.Minute)}},
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
	f.SetProfiles(sampleStatus(), sampleNow())

	f.SetAgentIsClaude(false)
	if f.Enabled() {
		t.Errorf("Enabled() = true after SetAgentIsClaude(false), want false")
	}
	inertFrame := fieldText(f, 60)
	if !strings.Contains(inertFrame, accountInertPlaceholder) {
		t.Errorf("View(60) while inert = %q, want it to contain %q", inertFrame, accountInertPlaceholder)
	}

	f.SetAgentIsClaude(true)
	if !f.Enabled() {
		t.Errorf("Enabled() = false after SetAgentIsClaude(true), want true")
	}
	claudeFrame := fieldText(f, 60)
	if strings.Contains(claudeFrame, accountInertPlaceholder) {
		t.Errorf("View(60) after re-enabling still contains the inert placeholder: %q", claudeFrame)
	}

	f.SetAgentIsClaude(false)
	if f.Enabled() {
		t.Errorf("Enabled() = true after flipping back to false, want false")
	}
}

// browseTo walks the account cursor down n rows WITHOUT committing --
// the gesture that used to pin an account by accident (v3 spec §10.3) and
// now does not.
func browseTo(f *AccountField, downs int) {
	for i := 0; i < downs; i++ {
		f.Update(key(tea.KeyDown, 0))
	}
}

// TestAccountField_BrowsingDoesNotPin is v3 spec §10.3, and it is a
// CORRECTNESS test rather than a cosmetic one: Pin() feeds
// plan.Input.AccountPin, so before this the session really did launch
// under `clauth <whatever row the cursor was resting on>`. Tabbing onto
// this row and pressing Down once was enough.
func TestAccountField_BrowsingDoesNotPin(t *testing.T) {
	f := NewAccountField(theme.Default())
	f.SetAgentIsClaude(true)
	f.SetProfiles(sampleStatus(), sampleNow())

	browseTo(f, 1) // active -> alpha, cursor only
	if got := f.Pin(); got != "" {
		t.Fatalf("Pin() after one Down = %q, want \"\" -- moving the cursor is browsing, not choosing", got)
	}
	browseTo(f, 3) // ... and off the end, still nothing committed
	if got := f.Pin(); got != "" {
		t.Fatalf("Pin() after walking the whole list = %q, want \"\"", got)
	}
}

// TestAccountField_EnterCommitsThePin is the other half: the deliberate
// gesture works, it is reversible in the same place, and a second press
// reports false so form.go's handleKey falls through to a plain advance.
func TestAccountField_EnterCommitsThePin(t *testing.T) {
	f := NewAccountField(theme.Default())
	f.SetAgentIsClaude(true)
	f.SetProfiles(sampleStatus(), sampleNow())

	browseTo(f, 1)
	if !f.Complete() {
		t.Fatal("Complete() on a row that is not the pin = false, want true (it moved the pin)")
	}
	if got := f.Pin(); got != "alpha" {
		t.Fatalf("Pin() after committing = %q, want %q", got, "alpha")
	}
	if f.Complete() {
		t.Error("a second Complete() on the same row = true, want false so the key advances instead")
	}

	browseTo(f, 1)
	if !f.Complete() {
		t.Fatal("Complete() after moving on = false, want true")
	}
	if got := f.Pin(); got != "beta" {
		t.Fatalf("Pin() after committing the next row = %q, want %q", got, "beta")
	}

	// Committing on the `active` sentinel REMOVES the pin: the same
	// gesture in the same place, which is why it is a row and not a key.
	f.Update(key(tea.KeyUp, 0))
	f.Update(key(tea.KeyUp, 0))
	if !f.Complete() {
		t.Fatal("Complete() back on the active row = false, want true")
	}
	if got := f.Pin(); got != "" {
		t.Fatalf("Pin() after committing the active row = %q, want \"\"", got)
	}
}

// TestAccountField_CommittingMarksTheRowWithoutMovingTheCursor pins what
// the ✓ is for. commitPin re-feeds the picker at the SAME version, so the
// mark moves and the cursor does not.
func TestAccountField_CommittingMarksTheRowWithoutMovingTheCursor(t *testing.T) {
	f := NewAccountField(theme.Default())
	f.SetAgentIsClaude(true)
	f.SetProfiles(sampleStatus(), sampleNow())

	browseTo(f, 2) // active -> alpha -> beta
	f.Complete()
	sel, ok := f.picker.Selected()
	if !ok || sel.ID != "beta" {
		t.Fatalf("cursor after committing = %+v, want it still on beta", sel)
	}
	if !sel.Current {
		t.Error("the committed row is not marked Current, so nothing on screen says which profile is pinned")
	}

	browseTo(f, 1) // beta -> gamma, browsing again
	if sel, _ := f.picker.Selected(); sel.Current {
		t.Error("the row under the cursor is marked Current after merely browsing to it")
	}
}

// TestAccountField_SetPin pins the new config-default pre-selection
// setter (Task 20b, spec §12's `[clauth] default`): a real profile name
// moves the pin; "" and the config's own "active" sentinel are both
// no-ops; an unknown name is also a no-op (never guess).
func TestAccountField_SetPin(t *testing.T) {
	f := NewAccountField(theme.Default())
	f.SetAgentIsClaude(true)
	f.SetProfiles(sampleStatus(), sampleNow())

	f.SetPin("")
	if got := f.Pin(); got != "" {
		t.Fatalf("Pin() after SetPin(\"\") = %q, want \"\" (no-op)", got)
	}
	f.SetPin("active")
	if got := f.Pin(); got != "" {
		t.Fatalf("Pin() after SetPin(\"active\") = %q, want \"\" (no-op)", got)
	}
	f.SetPin("does-not-exist")
	if got := f.Pin(); got != "" {
		t.Fatalf("Pin() after SetPin(\"does-not-exist\") = %q, want \"\" (no-op, never guess)", got)
	}

	f.SetPin("beta")
	if got := f.Pin(); got != "beta" {
		t.Fatalf("Pin() after SetPin(\"beta\") = %q, want %q", got, "beta")
	}
}

// TestAccountField_SetVerdict pins the new submit-time blocking verdict
// (fix round 1: an auth-blocked submit previously surfaced no NEW
// message at all -- only the picker row's own pre-existing marker, which
// was already visible before the blocked Create press, making the block
// look like it silently did nothing). The verdict must render on the
// hint row while it still matches the CURRENT pin, and stop rendering
// the moment the pin moves away -- the same staleness-by-comparison
// discipline TitleField.SetVerdict already uses, with no separate Clear
// call.
func TestAccountField_SetVerdict(t *testing.T) {
	f := NewAccountField(theme.Default())
	f.SetAgentIsClaude(true)
	f.SetProfiles(sampleStatus(), sampleNow())
	f.SetPin("beta")

	// The pre-existing `auth failed` badge on beta's own row is not the
	// verdict and never was; what must be absent is the verdict TEXT.
	frame := fieldText(f, 60)
	if strings.Contains(frame, "blocked") {
		t.Fatalf("View(60) before SetVerdict already shows a verdict: %q", frame)
	}

	f.SetVerdict(f.Pin(), "blocked — auth: expired")
	frame = fieldText(f, 60)
	if !strings.Contains(frame, "blocked — auth: expired") {
		t.Fatalf("View(60) after SetVerdict = %q, want it to contain the verdict text", frame)
	}

	// Moving the pin away must silently drop the now-stale verdict.
	f.SetPin("gamma")
	frame = fieldText(f, 60)
	if strings.Contains(frame, "blocked — auth: expired") {
		t.Fatalf("View(60) after moving the pin away still shows the stale verdict: %q", frame)
	}
}

// TestAccountField_SetProfilesRefreshPreservesPinByName mirrors
// field_worktree.go's identical same-version-refresh test: a later
// SetProfiles call (e.g. a re-poll on account focus, spec §8) must not
// silently bounce the user's pin back to "active".
func TestAccountField_SetProfilesRefreshPreservesPinByName(t *testing.T) {
	f := NewAccountField(theme.Default())
	f.SetAgentIsClaude(true)
	f.SetProfiles(sampleStatus(), sampleNow())
	browseTo(f, 2)
	f.Complete()
	if got := f.Pin(); got != "beta" {
		t.Fatalf("setup: Pin() = %q, want %q", got, "beta")
	}

	// A re-poll with reordered profiles.
	refreshed := sampleStatus()
	refreshed.Profiles[0], refreshed.Profiles[1] = refreshed.Profiles[1], refreshed.Profiles[0]
	f.SetProfiles(refreshed, sampleNow())

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
	f.SetProfiles(status, sampleNow())

	frame := fieldText(f, 60)
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
	f.SetProfiles(sampleStatus(), sampleNow())

	frame := fieldText(f, 60)
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
	f.SetProfiles(status, sampleNow())

	frame := fieldText(f, 60)
	if strings.Contains(frame, accountWarnAuthFailed) || strings.Contains(frame, accountWarnRateLimited) {
		t.Errorf("View(60) = %q, want no warning markers for a healthy profile", frame)
	}
	if !strings.Contains(frame, "5h 12%") {
		t.Errorf("View(60) = %q, want the 5h utilization shown", frame)
	}
}

// TestAccountField_EmptyProfileNameSkipped pins the review fix directly:
// a profile with an empty Name must not become its own row -- clauth's
// Profile.Name is unvalidated external JSON (internal/clauth/status.go's
// ParseStatus enforces neither non-emptiness nor uniqueness), and an
// empty-ID row would otherwise be indistinguishable from the "active"
// sentinel at the Pin() boundary ("" == no pin).
func TestAccountField_EmptyProfileNameSkipped(t *testing.T) {
	f := NewAccountField(theme.Default())
	f.SetAgentIsClaude(true)
	f.SetProfiles(clauth.Status{Profiles: []clauth.Profile{
		{Name: "", Tier: "Team", AuthStatus: "ok"},
		{Name: "real", Tier: "Team", AuthStatus: "ok"},
	}}, sampleNow())

	browseTo(f, 1) // active -> the only real row (empty-name profile skipped)
	f.Complete()
	if got := f.Pin(); got != "real" {
		t.Fatalf("Pin() after one Down = %q, want %q (the empty-name profile must not occupy a row)", got, "real")
	}

	// A further Down must clamp on the same last real row -- if the
	// empty-name profile had produced its own row, this would instead
	// leave the cursor on it and the commit below would pin "".
	browseTo(f, 1)
	f.Complete()
	if got := f.Pin(); got != "real" {
		t.Fatalf("Pin() after a second (clamped) Down = %q, want %q", got, "real")
	}
}

// TestAccountField_DuplicateProfileNamesDeduped pins the review fix's
// other half: two profiles sharing a Name must yield exactly one row
// (first-seen wins, mirroring field_worktree.go's identical seen-map
// guard for base refs), so widgets.Picker's own first-match-wins ID
// lookup never has two rows to disambiguate between.
func TestAccountField_DuplicateProfileNamesDeduped(t *testing.T) {
	f := NewAccountField(theme.Default())
	f.SetAgentIsClaude(true)
	f.SetProfiles(clauth.Status{Profiles: []clauth.Profile{
		{Name: "dup", Tier: "Team", AuthStatus: "ok"},
		{Name: "dup", Tier: "Max 20x", AuthStatus: "expired"}, // same name -- must not add a second row
		{Name: "unique", Tier: "Team", AuthStatus: "ok"},
	}}, sampleNow())

	browseTo(f, 1) // active -> dup (first-seen wins)
	f.Complete()
	if got := f.Pin(); got != "dup" {
		t.Fatalf("Pin() after one Down = %q, want %q", got, "dup")
	}
	f.Update(key(tea.KeyDown, 0)) // dup -> unique (the second "dup" must not have its own row)
	f.Complete()
	if got := f.Pin(); got != "unique" {
		t.Fatalf("Pin() after two Downs = %q, want %q (a duplicate name must yield exactly one row)", got, "unique")
	}
	// Clamped: a third Down must stay on "unique", confirming there is no
	// hidden extra row for the second "dup" profile.
	f.Update(key(tea.KeyDown, 0))
	if got := f.Pin(); got != "unique" {
		t.Fatalf("Pin() after three (clamped) Downs = %q, want %q", got, "unique")
	}
}

func TestAccountField_NoPanicOnDegenerateWidth(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("AccountField panicked: %v", r)
		}
	}()
	f := NewAccountField(theme.Default())
	_ = f.Row(0)
	_ = f.Panel(0, f.PanelRows())
	_ = f.Row(-3)
	_ = f.Panel(-3, f.PanelRows())
}

func TestAccountField_NoPanicBeforeSetProfiles(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("AccountField panicked before SetProfiles: %v", r)
		}
	}()
	f := NewAccountField(theme.Default())
	f.SetAgentIsClaude(true)
	_ = f.Row(60)
	_ = f.Panel(60, f.PanelRows())
	f.Update(key(tea.KeyDown, 0))
	f.Update(key(tea.KeyUp, 0))
	_ = f.Pin()
}

// TestAccountResetText pins the panel's reset cell. The zero-padding is
// the load-bearing part: `2h5m` and `2h11m` are different widths, and a
// column that changes width re-lays the whole table on a list that
// re-renders every keystroke.
func TestAccountResetText(t *testing.T) {
	now := sampleNow()
	cases := []struct {
		name string
		in   time.Duration
		want string
	}{
		// The day tier, which the live panel is what asked for: clauth's
		// second window is SEVEN days, so `in 110h07m` was an ordinary
		// reading rather than a corner.
		{"days and hours", 4*24*time.Hour + 14*time.Hour, "in 4d14h"},
		{"exactly a day", 24 * time.Hour, "in 1d00h"},
		{"one minute short of a day stays in hours", 24*time.Hour - time.Minute, "in 23h59m"},
		{"a full seven-day window", 7 * 24 * time.Hour, "in 7d00h"},

		{"hours and minutes", 2*time.Hour + 11*time.Minute, "in 2h11m"},
		{"minutes are zero padded past the first hour", 2*time.Hour + 5*time.Minute, "in 2h05m"},
		{"exactly an hour", time.Hour, "in 1h00m"},
		{"under an hour", 45 * time.Minute, "in 45m"},
		{"a single minute", time.Minute, "in 1m"},

		// clauth's feed can be minutes behind the clock, and a negative
		// countdown says nothing a reader can use.
		{"already due", 0, "due"},
		{"overdue", -30 * time.Minute, "due"},
		// Rounded to the minute, so the last half-minute reads `due`
		// rather than `in 0m`.
		{"under half a minute", 20 * time.Second, "due"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := accountResetText(now.Add(c.in), now); got != c.want {
				t.Errorf("accountResetText(now+%v, now) = %q, want %q", c.in, got, c.want)
			}
		})
	}

	// The padding's real promise, swept rather than sampled: the LOWER
	// unit is always two digits, so `2h5m` never appears beside `2h11m`
	// and the readings sort by eye.
	//
	// It is not that every reading is the same width -- `in 10h00m` is a
	// cell wider than `in 2h47m`, and a seven-day window reaches both.
	// The column absorbs that, because it is measured over the whole
	// filtered set: the widths differ per PROFILE, which is stable, not
	// per keystroke.
	lower := regexp.MustCompile(`^in \d+[dh]\d\d[hm]$`)
	for m := 60; m < 9*24*60; m++ {
		got := accountResetText(now.Add(time.Duration(m)*time.Minute), now)
		if !lower.MatchString(got) {
			t.Fatalf("at %d minutes the reset cell = %q, want a two-digit lower unit", m, got)
		}
	}
}

// TestAccountField_LiveProfileIsMarked is the question the author asked
// that the screen could not answer: *what is the active account*. clauth
// reports it, the app has always read it as a lookup key, and nothing
// ever displayed it (v3 spec §10.2).
func TestAccountField_LiveProfileIsMarked(t *testing.T) {
	f := NewAccountField(theme.Default())
	f.SetAgentIsClaude(true)
	f.SetProfiles(sampleStatus(), sampleNow()) // ActiveProfile: "alpha"

	panel := ansi.Strip(f.Panel(101, f.PanelRows()))
	if !strings.Contains(panel, "alpha "+accountLiveGlyph) {
		t.Errorf("panel does not mark the live profile:\n%s", panel)
	}
	for _, other := range []string{"beta " + accountLiveGlyph, "gamma " + accountLiveGlyph} {
		if strings.Contains(panel, other) {
			t.Errorf("panel marks %q as live too:\n%s", other, panel)
		}
	}
	if !strings.Contains(panel, accountLiveLegend) {
		t.Errorf("panel does not explain the glyph it just drew:\n%s", panel)
	}

	// With no live profile among the rows there is no glyph to explain,
	// so the line goes back to saying what the `active` row means.
	orphaned := sampleStatus()
	orphaned.ActiveProfile = "vanished"
	f.SetProfiles(orphaned, sampleNow())
	panel = ansi.Strip(f.Panel(101, f.PanelRows()))
	if strings.Contains(panel, accountLiveGlyph) {
		t.Errorf("panel draws the live glyph with no live profile on it:\n%s", panel)
	}
	if !strings.Contains(panel, accountActiveHint) {
		t.Errorf("panel explains a glyph that is not there instead of the `active` row:\n%s", panel)
	}
}

// TestAccountField_PanelDropsTheResetColumnRatherThanEliding pins the
// widths on either side of PickerColumn.DropBelow. Before it, an
// 80-column terminal -- which is a real popup size (v3 spec §6.1 clamps
// the 104-cell card to the terminal) -- ended every profile row in a lone
// `…` where `in 2h11m` should be: three cells spent saying nothing.
func TestAccountField_PanelDropsTheResetColumnRatherThanEliding(t *testing.T) {
	f := NewAccountField(theme.Default())
	f.SetAgentIsClaude(true)
	f.SetProfiles(sampleStatus(), sampleNow())

	wide := ansi.Strip(f.Panel(101, f.PanelRows()))
	if !strings.Contains(wide, "in 2h11m") {
		t.Errorf("the shipped width does not carry the reset column:\n%s", wide)
	}

	narrow := ansi.Strip(f.Panel(80, f.PanelRows()))
	if strings.Contains(narrow, "in 2h11m") {
		t.Errorf("80 cells fits the reset column after all -- this test no longer covers the drop:\n%s", narrow)
	}
	if strings.Contains(narrow, "…") {
		t.Errorf("a column was elided to nothing instead of being dropped:\n%s", narrow)
	}
	// The columns the reset time made room for are all still whole.
	for _, want := range []string{"█░░░░░░░░░", "5h 12%", "████░░░░░░", "7d 40%"} {
		if !strings.Contains(narrow, want) {
			t.Errorf("dropping the reset column cost %q too:\n%s", want, narrow)
		}
	}
}
