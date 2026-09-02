package form

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

func TestTitleField_IDAndEnabled(t *testing.T) {
	f := NewTitleField(theme.Default())
	if f.ID() != "title" {
		t.Errorf("ID() = %q, want %q", f.ID(), "title")
	}
	if !f.Enabled() {
		t.Errorf("Enabled() = false, want true (Title is always present)")
	}
}

// TestTitleField_ImplementsTitleValuer pins the capability wiring form.go's
// zoneFor relies on: a Section whose ID() == "title" must also implement
// titleValuer so FocusZone.TitleEmpty reflects the real typed value.
func TestTitleField_ImplementsTitleValuer(t *testing.T) {
	var s Section = NewTitleField(theme.Default())
	tv, ok := s.(titleValuer)
	if !ok {
		t.Fatalf("*TitleField does not implement titleValuer")
	}
	if got := tv.Value(); got != "" {
		t.Errorf("Value() on a fresh TitleField = %q, want \"\"", got)
	}
}

func TestTitleField_TypingSetsValueAndTouched(t *testing.T) {
	f := NewTitleField(theme.Default())
	f.Focus()
	if f.Touched() {
		t.Fatalf("Touched() = true before any input, want false")
	}
	for _, r := range "fix login" {
		f.Update(rn(r))
	}
	if got := f.Value(); got != "fix login" {
		t.Fatalf("Value() = %q, want %q", got, "fix login")
	}
	if !f.Touched() {
		t.Fatalf("Touched() = false after typing, want true")
	}
}

// TestTitleField_ThirtyTwoRuneCap pins the brief's literal "32-rune cap"
// requirement end to end through TitleField, not just at the lineInput
// layer (already covered by TestLineInput_CharLimitIsRuneBound).
func TestTitleField_ThirtyTwoRuneCap(t *testing.T) {
	f := NewTitleField(theme.Default())
	f.Focus()
	for range 40 {
		f.Update(rn('a'))
	}
	if got := len([]rune(f.Value())); got != 32 {
		t.Fatalf("Value() has %d runes after typing 40, want exactly 32 (cap)", got)
	}
}

// TestTitleField_VerdictShownOnlyForCurrentTitle pins SetVerdict's
// key-vs-current-value staleness rule (this task's own "no separate Clear
// method" design, mirrored from DirField's validity marker): a verdict
// computed for a title the user has since edited away from must not
// render.
func TestTitleField_VerdictShownOnlyForCurrentTitle(t *testing.T) {
	f := NewTitleField(theme.Default())
	f.Focus()
	for _, r := range "fix login" {
		f.Update(rn(r))
	}
	f.SetVerdict("fix login", "branch: zvi/fix-login")

	frame := fieldText(f, 60)
	if !strings.Contains(frame, "branch: zvi/fix-login") {
		t.Fatalf("View(60) = %q, want it to contain the current verdict", frame)
	}

	// Now the title changes -- the verdict, computed for the OLD title,
	// must stop showing (a stale verdict must never be asserted for the
	// new title).
	f.Update(rn('!'))
	frame = fieldText(f, 60)
	if strings.Contains(frame, "branch: zvi/fix-login") {
		t.Fatalf("View(60) = %q, still shows the stale verdict after the title changed", frame)
	}
}

// TestTitleField_SetTitleTouchedRule pins SetTitle's touched-vs-preselected
// discipline (added in Task 20, mirroring field_worktree.go's
// WorktreeField.SetBranch): a seeded set applies only until the user
// types, after which further seeded sets are ignored, but a hard
// (seeded == false) set always applies and clears Touched().
func TestTitleField_SetTitleTouchedRule(t *testing.T) {
	f := NewTitleField(theme.Default())

	f.SetTitle("Fix login bug", true)
	if got := f.Value(); got != "Fix login bug" {
		t.Fatalf("Value() after first seed = %q, want %q", got, "Fix login bug")
	}
	if f.Touched() {
		t.Fatalf("Touched() = true after a seeded set, want false")
	}

	f.Focus()
	f.Update(rn('!'))
	if !f.Touched() {
		t.Fatalf("Touched() = false after typing, want true")
	}

	f.SetTitle("Fix login bug v2", true)
	if got := f.Value(); got != "Fix login bug!" {
		t.Fatalf("Value() after a seeded call post-touch = %q, want the user's own edit %q unclobbered", got, "Fix login bug!")
	}

	f.SetTitle("hard reset", false)
	if got := f.Value(); got != "hard reset" {
		t.Fatalf("Value() after a hard set = %q, want %q", got, "hard reset")
	}
	if f.Touched() {
		t.Fatalf("Touched() = true after a hard set, want false (cleared)")
	}
	f.SetTitle("seeded again", true)
	if got := f.Value(); got != "seeded again" {
		t.Fatalf("Value() after re-seeding post-hard-reset = %q, want %q", got, "seeded again")
	}
}

// TestTitleField_FocusBlurWiring pins that Focus()/Blur() actually reach
// the wrapped lineInput, through the one observable difference they make:
// bubbles' textinput only accepts keystrokes while focused, so typing is
// what proves the wiring -- not the tea.Cmd Focus happens to return, which
// may legitimately be nil depending on cursor mode.
//
// Rewritten in the final fix wave (minor M6): the previous version called
// Focus() and Blur() and asserted nothing whatsoever, deferring in its own
// comment to "the focused-state assertion below" -- which did not exist.
func TestTitleField_FocusBlurWiring(t *testing.T) {
	f := NewTitleField(theme.Default())

	// Blurred by construction: keystrokes must not reach the input.
	f.Update(rn('a'))
	if got := f.Value(); got != "" {
		t.Fatalf("Value() after typing while blurred = %q, want %q", got, "")
	}
	if f.Touched() {
		t.Fatal("Touched() = true after a keystroke that never reached the input")
	}

	f.Focus()
	f.Update(rn('a'))
	f.Update(rn('b'))
	if got := f.Value(); got != "ab" {
		t.Fatalf("Value() after typing while focused = %q, want %q", got, "ab")
	}
	if !f.Touched() {
		t.Fatal("Touched() = false after the user typed into a focused field")
	}

	f.Blur()
	f.Update(rn('c'))
	if got := f.Value(); got != "ab" {
		t.Fatalf("Value() after typing post-Blur = %q, want %q (Blur did not reach the input)", got, "ab")
	}
}

// sampleSessions is v3 spec §9's list in the shape the app pushes it:
// three other workspaces, one with no repository context at all (herdr
// omits the worktree key for a workspace it has none for).
func sampleSessions() []Session {
	return []Session{
		{Label: "report-studio", Status: "idle", Panes: 4, Repo: "quantivly"},
		{Label: "qspace-tls", Status: "blocked", Panes: 1, Repo: "quantivly"},
		{Label: "scratch", Status: "idle", Panes: 2},
	}
}

// TestTitleField_PanelRowsGrowsWithTheSessionList pins the number v3 spec
// §9 exists to change. It was a flat 1 -- one verdict line against
// issuePanelMaxRows' 24 -- and because the region is fixed so the footer
// does not move as focus travels, that meant no popup height served both:
// sized for the picker, the opening screen was a fifteen-row hole.
func TestTitleField_PanelRowsGrowsWithTheSessionList(t *testing.T) {
	f := NewTitleField(theme.Default())
	if got := f.PanelRows(); got != 1 {
		t.Errorf("PanelRows() before any session list = %d, want 1 (the verdict line, reserved unconditionally)", got)
	}

	f.SetSessions(sampleSessions())
	// The verdict line, a blank, the heading, and one row per session.
	if got, want := f.PanelRows(), 3+len(sampleSessions()); got != want {
		t.Errorf("PanelRows() with %d sessions = %d, want %d", len(sampleSessions()), got, want)
	}

	// An empty list is the pre-v3 panel exactly, not a heading over
	// nothing: a form opened in a fresh herdr with one workspace (the one
	// the popup is in, which the app drops) has nothing to say here.
	f.SetSessions(nil)
	if got := f.PanelRows(); got != 1 {
		t.Errorf("PanelRows() after the list emptied = %d, want 1", got)
	}
}

// TestTitleField_PanelListsTheSessions pins what the resting panel draws.
func TestTitleField_PanelListsTheSessions(t *testing.T) {
	f := NewTitleField(theme.Default())
	f.SetSessions(sampleSessions())

	panel := ansi.Strip(f.Panel(101, f.PanelRows()))
	for _, want := range []string{
		titleSessionsHeading, "3 sessions",
		"report-studio", "idle", "4 panes", "quantivly",
		"qspace-tls", "blocked", "1 pane",
		"scratch", "2 panes",
	} {
		if !strings.Contains(panel, want) {
			t.Errorf("panel does not carry %q:\n%s", want, panel)
		}
	}

	// The heading names WHEN the list was read, because §9 requires it:
	// the data is fetched once at Bootstrap and never refreshed, so a
	// panel claiming the sessions "exist" would state what it cannot know.
	if strings.Contains(panel, "already exist") {
		t.Errorf("the heading claims the list is live:\n%s", panel)
	}
}

// TestTitleField_SessionListHasNoCursor is the author's own decision made
// into an assertion: the list is informational, so no row is drawn as
// chosen. The picker's cursor-row treatment is three signals (Surface
// fill, bold, and the panel's ▸ gutter glyph) all saying "this is the one
// you picked", and on a list nothing can act on that is a promise the
// form cannot keep.
func TestTitleField_SessionListHasNoCursor(t *testing.T) {
	f := NewTitleField(theme.Default())
	f.SetSessions(sampleSessions())

	panel := f.Panel(101, f.PanelRows())
	if strings.Contains(ansi.Strip(panel), panelCursorGlyph) {
		t.Errorf("the session list draws a cursor glyph:\n%s", ansi.Strip(panel))
	}
	// And no row carries the cursor row's Surface fill either -- the
	// glyph is the panel's, the fill is the picker's, and both have to go.
	if strings.Contains(panel, ansiBackground(theme.Default().Surface)) {
		t.Errorf("a session row is painted with the cursor row's fill:\n%q", panel)
	}
}

// TestTitleField_CollisionMarkFollowsTheTypedTitle is the half of §9's
// panel that is not just a list. The app already computes this fact
// (async.go's workspaceLabelTaken, off the same data) and only ever
// surfaces it as a verdict AFTER the collision; the mark shows it coming,
// and shows which session it is with.
func TestTitleField_CollisionMarkFollowsTheTypedTitle(t *testing.T) {
	f := NewTitleField(theme.Default())
	f.SetSessions(sampleSessions())
	f.Focus()

	if got := ansi.Strip(f.Panel(101, f.PanelRows())); strings.Contains(got, markerWarning) {
		t.Fatalf("a mark before anything was typed:\n%s", got)
	}

	for _, r := range "qspace-tls" {
		f.Update(rn(r))
	}
	marked := markedSessionRows(f)
	if len(marked) != 1 || !strings.Contains(marked[0], "qspace-tls") {
		t.Errorf("marked rows = %v, want exactly the colliding one", marked)
	}

	// One character short of the label is not a collision: the comparison
	// is workspaceLabelTaken's own exact equality, so the mark and the
	// submit-time refusal cannot disagree about what a collision is.
	f.Update(key(tea.KeyBackspace, 0))
	if got := markedSessionRows(f); len(got) != 0 {
		t.Errorf("marked rows after backspacing one character = %v, want none", got)
	}
}

// markedSessionRows returns the stripped session rows carrying the
// warning marker.
func markedSessionRows(f *TitleField) []string {
	var out []string
	for _, line := range strings.Split(ansi.Strip(f.Panel(101, f.PanelRows())), "\n") {
		if strings.HasPrefix(strings.TrimLeft(line, " "), markerWarning) {
			out = append(out, strings.TrimSpace(line))
		}
	}
	return out
}

// TestTitleField_UnlabelledSessionIsSkipped guards widgets.Picker's
// unique-non-empty-ID requirement the same way every other field's
// refresh does. herdr's own label is unvalidated as far as this process
// is concerned, and an unlabelled workspace names nothing and collides
// with nothing.
func TestTitleField_UnlabelledSessionIsSkipped(t *testing.T) {
	f := NewTitleField(theme.Default())
	f.SetSessions([]Session{{Label: "", Status: "idle", Panes: 1}, {Label: "real", Status: "idle", Panes: 1}})

	rows := 0
	for _, line := range strings.Split(ansi.Strip(f.Panel(101, f.PanelRows())), "\n") {
		if strings.Contains(line, "1 pane") {
			rows++
		}
	}
	if rows != 1 {
		t.Errorf("the panel drew %d session rows, want 1 -- the unlabelled workspace must not occupy one", rows)
	}
}
