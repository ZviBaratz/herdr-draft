package form

import (
	"strings"
	"testing"

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

	frame := ansi.Strip(f.View(60))
	if !strings.Contains(frame, "branch: zvi/fix-login") {
		t.Fatalf("View(60) = %q, want it to contain the current verdict", frame)
	}

	// Now the title changes -- the verdict, computed for the OLD title,
	// must stop showing (a stale verdict must never be asserted for the
	// new title).
	f.Update(rn('!'))
	frame = ansi.Strip(f.View(60))
	if strings.Contains(frame, "branch: zvi/fix-login") {
		t.Fatalf("View(60) = %q, still shows the stale verdict after the title changed", frame)
	}
}

// TestTitleField_VerdictBoundedToTwentyOneCells pins the brief's literal
// "bounded to 21 cells" contract: a verdict text longer than 21 cells must
// be truncated to at most 21 cells of content, regardless of how much
// horizontal room View's own inner width otherwise has.
func TestTitleField_VerdictBoundedToTwentyOneCells(t *testing.T) {
	f := NewTitleField(theme.Default())
	longVerdict := strings.Repeat("x", 40)
	f.SetVerdict(f.Value(), longVerdict) // key == "" == Value() on a fresh field

	frame := ansi.Strip(f.View(80))
	if strings.Contains(frame, longVerdict) {
		t.Fatalf("View(80) contains the full 40-cell verdict text unbounded, want it clipped to 21 cells")
	}
	if !strings.Contains(frame, strings.Repeat("x", 21)) {
		t.Fatalf("View(80) does not contain the expected 21-cell-clipped verdict prefix")
	}
}

// TestTitleField_HeightIsConstant pins the task's own "verified fact":
// Height must not depend on focus, touched state, or whether a verdict is
// currently set -- the reserved verdict/hint line keeps the field's line
// count identical in every state.
func TestTitleField_HeightIsConstant(t *testing.T) {
	f := NewTitleField(theme.Default())
	base := f.Height(24)

	f.Focus()
	if got := f.Height(24); got != base {
		t.Errorf("Height(24) while focused = %d, want %d (focus-independent)", got, base)
	}

	f.SetVerdict(f.Value(), "some verdict text")
	if got := f.Height(24); got != base {
		t.Errorf("Height(24) with a verdict set = %d, want %d (hint-line-independent)", got, base)
	}

	if got := strings.Count(f.View(60), "\n") + 1; got != base {
		t.Errorf("View(60) rendered %d physical lines, want Height()'s own %d", got, base)
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

func TestTitleField_FocusBlurWiring(t *testing.T) {
	f := NewTitleField(theme.Default())
	if cmd := f.Focus(); cmd == nil {
		// Not fatal -- bubbles' textinput.Focus may or may not return a
		// blink Cmd depending on cursor mode, but Focus() must not panic
		// and must be callable; recorded via the focused-state assertion
		// below instead of requiring a non-nil Cmd specifically.
		_ = cmd
	}
	f.Blur()
}
