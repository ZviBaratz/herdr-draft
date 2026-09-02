package form

import (
	"strings"
	"testing"

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
