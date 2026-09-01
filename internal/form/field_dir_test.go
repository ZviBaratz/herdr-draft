package form

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

func TestDirField_IDAndEnabled(t *testing.T) {
	d := NewDirField(theme.Default())
	if d.ID() != "dir" {
		t.Errorf("ID() = %q, want %q", d.ID(), "dir")
	}
	if !d.Enabled() {
		t.Errorf("Enabled() = false, want true (Project is always present)")
	}
}

// TestDirField_ImplementsCompleter pins form.go's completer capability
// wiring: ZoneDir.isPicker() == true, so MapKey's ActionComplete only
// makes sense if the focused Section can try to complete.
func TestDirField_ImplementsCompleter(t *testing.T) {
	var s Section = NewDirField(theme.Default())
	if _, ok := s.(completer); !ok {
		t.Fatalf("*DirField does not implement completer")
	}
}

func typeInto(d *DirField, s string) {
	for _, r := range s {
		d.Update(rn(r))
	}
}

// TestDirField_FragmentModeRanksByFuzzyMatch exercises the brief's own
// "fresh subsequence ranker plugged into DirField's filtering" -- typing a
// non-path fragment must narrow AND reorder candidates by fuzzyRank, not
// merely a plain substring filter (widgets.Picker.SetQuery's own weaker
// contract, deliberately bypassed here -- see field_dir.go's own doc
// comment).
func TestDirField_FragmentModeRanksByFuzzyMatch(t *testing.T) {
	d := NewDirField(theme.Default())
	d.Focus()
	d.SetCandidates(1, []string{"/home/z/other-project", "/home/z/herdr-draft"})

	typeInto(d, "hd")

	if got := d.Value(); got != "/home/z/herdr-draft" {
		t.Fatalf("Value() after typing %q = %q, want the fuzzy-ranked top match %q", "hd", got, "/home/z/herdr-draft")
	}
}

// TestDirField_PathModeBrowsesSuppliedChildren pins the dual fragment/path
// mode switch: typing a "/"-prefixed string switches to path mode, whose
// candidate set is whatever the app layer most recently supplied via
// SetCandidates for that browse context (this package performs no
// filesystem I/O itself).
func TestDirField_PathModeBrowsesSuppliedChildren(t *testing.T) {
	d := NewDirField(theme.Default())
	d.Focus()
	d.SetCandidates(1, []string{"/home/zvi/Projects/herdr", "/home/zvi/Projects/herdr-draft", "/home/zvi/Projects/atrium"})

	typeInto(d, "/home/zvi/Projects/herdr-d")

	if got := d.Value(); got != "/home/zvi/Projects/herdr-draft" {
		t.Fatalf("Value() = %q, want the matching child %q", got, "/home/zvi/Projects/herdr-draft")
	}
}

// TestDirField_PathModeLiteralFallback pins the "typed-but-not-yet-listed
// path stays selectable" contract: a path that matches nothing in the
// current candidate set must still appear (and be selectable) as a
// literal fallback item.
func TestDirField_PathModeLiteralFallback(t *testing.T) {
	d := NewDirField(theme.Default())
	d.Focus()
	d.SetCandidates(1, []string{"/home/zvi/Projects/herdr"})

	typeInto(d, "/home/zvi/Projects/brand-new-repo")

	if got := d.Value(); got != "/home/zvi/Projects/brand-new-repo" {
		t.Fatalf("Value() = %q, want the literal typed path as a fallback selection", got)
	}
}

// TestDirField_CompleteExtendsToLongestCommonPrefix pins Tab's
// "shell-completion-then-advance" contract in path mode: typing a base
// prefix that matches multiple children extends the filter to their
// longest common prefix and reports true (consumed); MapKey only falls
// back to a plain advance when Complete returns false.
func TestDirField_CompleteExtendsToLongestCommonPrefix(t *testing.T) {
	d := NewDirField(theme.Default())
	d.Focus()
	d.SetCandidates(1, []string{
		"/home/zvi/Projects/herdr",
		"/home/zvi/Projects/herdr-draft",
	})
	typeInto(d, "/home/zvi/Projects/he")

	if !d.Complete() {
		t.Fatalf("Complete() = false, want true (both children share the \"herdr\" prefix)")
	}
	if got := d.input.Value(); got != "/home/zvi/Projects/herdr" {
		t.Fatalf("filter after Complete() = %q, want the longest common prefix %q", got, "/home/zvi/Projects/herdr")
	}

	// A second Complete() call, once already at the longest common
	// prefix, has nothing further to extend -- MapKey must fall through
	// to a plain advance.
	if d.Complete() {
		t.Fatalf("Complete() = true on a second call at the already-extended prefix, want false (nothing left to complete)")
	}
}

// TestDirField_CompleteFalseInFragmentMode pins "a zone whose widget has
// nothing to complete should treat this the same as ActionAdvance"
// (keys.go's own MapKey doc): fragment mode (no leading /, ~, or .) has no
// shell-style completion concept at all.
func TestDirField_CompleteFalseInFragmentMode(t *testing.T) {
	d := NewDirField(theme.Default())
	d.Focus()
	d.SetCandidates(1, []string{"/home/z/herdr-draft"})
	typeInto(d, "hd")

	if d.Complete() {
		t.Fatalf("Complete() = true in fragment mode, want false")
	}
}

// TestDirField_ValidityMarkerRendersOnlyForCurrentSelection pins
// SetValidity's staleness-by-comparison contract (mirroring TitleField's
// SetVerdict): a verdict applies only while its path is still the current
// selection.
func TestDirField_ValidityMarkerRendersOnlyForCurrentSelection(t *testing.T) {
	d := NewDirField(theme.Default())
	d.Focus()
	d.SetCandidates(1, []string{"/home/z/repo-a", "/home/z/repo-b"})

	d.SetValidity("/home/z/repo-a", ValidityInvalid)
	if got := ansi.Strip(d.View(60)); !strings.Contains(got, "(invalid)") {
		t.Fatalf("View(60) = %q, want it to contain \"(invalid)\" for the current selection", got)
	}

	d.picker.CursorNext() // selection moves to repo-b, which has no verdict yet
	if got := ansi.Strip(d.View(60)); strings.Contains(got, "(invalid)") {
		t.Fatalf("View(60) = %q, still shows the stale (invalid) marker after selection moved", got)
	}

	d.SetValidity("/home/z/repo-b", ValidityDirect)
	if got := ansi.Strip(d.View(60)); !strings.Contains(got, "(direct)") {
		t.Fatalf("View(60) = %q, want it to contain \"(direct)\" for repo-b", got)
	}
}

// TestDirField_SetCandidatesStalenessGate pins SetCandidates' own
// monotonic-version guard, mirroring widgets.Picker.SetItems' documented
// contract (this task's own "verified fact": "candidates arrive... via
// setters", staleness-gated the same way async delivery already is
// elsewhere in this package).
func TestDirField_SetCandidatesStalenessGate(t *testing.T) {
	d := NewDirField(theme.Default())
	d.SetCandidates(2, []string{"/home/z/fresh"})
	d.SetCandidates(1, []string{"/home/z/stale"}) // older version, must be dropped

	if got := d.Value(); got != "/home/z/fresh" {
		t.Fatalf("Value() = %q after a stale SetCandidates call, want the fresher %q to survive", got, "/home/z/fresh")
	}
}

// TestDirField_SetCandidatesRefreshPreservesSelectionByID pins the
// same-context candidate refresh contract DirectoryPicker.UpdateCandidates
// documents (atrium's own directoryPicker.go): a background refresh under
// the SAME typed filter should not silently move the user's selection
// onto a different item that happens to now sit at the same row.
func TestDirField_SetCandidatesRefreshPreservesSelectionByID(t *testing.T) {
	d := NewDirField(theme.Default())
	d.SetCandidates(1, []string{"/home/z/a", "/home/z/b", "/home/z/c"})
	d.picker.CursorNext() // select "/home/z/b"

	// A refresh under an unchanged filter ("") that reorders the list.
	d.SetCandidates(1, []string{"/home/z/b", "/home/z/a", "/home/z/c"})

	if got := d.Value(); got != "/home/z/b" {
		t.Fatalf("Value() after a same-context reordering refresh = %q, want the same item %q to stay selected by ID", got, "/home/z/b")
	}
}

// TestDirField_NonEditUpdateDoesNotResetSelection pins a correctness
// concern this task's own design had to actively guard against: a message
// forwarded to Update that does NOT change the typed filter text (a
// cursor-blink tick, or a key that moves the input cursor without editing
// content) must not spuriously bump the picker's internal version and
// reset the user's row selection back to the top.
func TestDirField_NonEditUpdateDoesNotResetSelection(t *testing.T) {
	d := NewDirField(theme.Default())
	d.Focus()
	d.SetCandidates(1, []string{"/home/z/a", "/home/z/b"})
	d.picker.CursorNext() // select "/home/z/b"

	d.Update(struct{ unrelated bool }{}) // not a KeyPressMsg at all

	if got := d.Value(); got != "/home/z/b" {
		t.Fatalf("Value() after an unrelated Update message = %q, want selection preserved at %q", got, "/home/z/b")
	}
}

// TestDirField_ArrowKeysMoveCursorNotText pins the split this task's own
// lineinput.go doc comment calls out: Up/Down must move the picker's
// cursor, not be swallowed as textinput's own (irrelevant here)
// suggestion-cycling keys, and must not be inserted as literal text.
func TestDirField_ArrowKeysMoveCursorNotText(t *testing.T) {
	d := NewDirField(theme.Default())
	d.Focus()
	d.SetCandidates(1, []string{"/home/z/a", "/home/z/b", "/home/z/c"})

	d.Update(key(tea.KeyDown, 0))
	if got := d.Value(); got != "/home/z/b" {
		t.Fatalf("Value() after Down = %q, want %q", got, "/home/z/b")
	}
	if got := d.input.Value(); got != "" {
		t.Fatalf("filter text after Down = %q, want unchanged \"\"", got)
	}

	d.Update(key(tea.KeyUp, 0))
	if got := d.Value(); got != "/home/z/a" {
		t.Fatalf("Value() after Up = %q, want %q", got, "/home/z/a")
	}
}

func TestDirField_HeightIsConstant(t *testing.T) {
	d := NewDirField(theme.Default())
	base := d.Height(24)

	d.Focus()
	if got := d.Height(24); got != base {
		t.Errorf("Height(24) while focused = %d, want %d", got, base)
	}
	d.SetCandidates(1, []string{"/a", "/b"})
	d.SetValidity("/a", ValidityInvalid)
	d.Hint("some hint")
	if got := d.Height(24); got != base {
		t.Errorf("Height(24) with candidates/validity/hint set = %d, want %d", got, base)
	}
	if got := strings.Count(d.View(60), "\n") + 1; got != base {
		t.Errorf("View(60) rendered %d physical lines, want Height()'s own %d", got, base)
	}
}

// TestDirField_NoPanicOnDegenerateInputs is this project's own "no panics
// in production code" rule, exercised directly against a fresh field with
// no candidates and a pathologically narrow render width.
func TestDirField_NoPanicOnDegenerateInputs(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("DirField panicked: %v", r)
		}
	}()
	d := NewDirField(theme.Default())
	_ = d.View(0)
	_ = d.Value()
	_ = d.Complete()
	d.Focus()
	_ = d.View(-5)
	d.SetCandidates(1, nil)
	_ = d.View(1)
}
