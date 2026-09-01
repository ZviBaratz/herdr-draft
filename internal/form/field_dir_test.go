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

// TestDirField_PathModeKeepsDistinctCandidatesSharingABasename pins review
// round 1's second Important finding directly: two DIFFERENT candidates
// that happen to share a basename (different parent directories, same
// leaf name -- e.g. two separate checkouts both named "api") must both
// stay selectable in path mode. An earlier version of pathModeItems
// deduped by basename BEFORE ranking, which silently dropped every
// same-named candidate but the first.
func TestDirField_PathModeKeepsDistinctCandidatesSharingABasename(t *testing.T) {
	d := NewDirField(theme.Default())
	d.Focus()
	d.SetCandidates(1, []string{"/home/zvi/work/api", "/home/zvi/oss/api"})

	typeInto(d, "/a")

	// Walk every visible row from the top (Down clamps at the last row --
	// widgets.Picker's own documented contract -- so a repeated Value()
	// means the walk has reached the end), collecting each row's Value().
	seen := map[string]bool{d.Value(): true}
	prev := d.Value()
	for range 5 { // generous bound: only a handful of rows are possible here
		d.Update(key(tea.KeyDown, 0))
		cur := d.Value()
		if cur == prev {
			break
		}
		seen[cur] = true
		prev = cur
	}

	for _, want := range []string{"/home/zvi/work/api", "/home/zvi/oss/api"} {
		if !seen[want] {
			t.Fatalf("visible rows = %v, want both same-basename candidates present (missing %q)", seen, want)
		}
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
	if got := ansi.Strip(d.View(60, d.Height(24))); !strings.Contains(got, "(invalid)") {
		t.Fatalf("View(60) = %q, want it to contain \"(invalid)\" for the current selection", got)
	}

	d.picker.CursorNext() // selection moves to repo-b, which has no verdict yet
	if got := ansi.Strip(d.View(60, d.Height(24))); strings.Contains(got, "(invalid)") {
		t.Fatalf("View(60) = %q, still shows the stale (invalid) marker after selection moved", got)
	}

	d.SetValidity("/home/z/repo-b", ValidityDirect)
	if got := ansi.Strip(d.View(60, d.Height(24))); !strings.Contains(got, "(direct)") {
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
	if got := strings.Count(d.View(60, d.Height(24)), "\n") + 1; got != base {
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
	_ = d.View(0, d.Height(24))
	_ = d.Value()
	_ = d.Complete()
	d.Focus()
	_ = d.View(-5, d.Height(24))
	d.SetCandidates(1, nil)
	_ = d.View(1, d.Height(24))
}

// TestDirFieldTypedIsTheRawTextNotTheSelection pins the distinction the
// app layer's browse source depends on: Typed() is what the user has
// entered, Value() is the row that entry currently highlights.
func TestDirFieldTypedIsTheRawTextNotTheSelection(t *testing.T) {
	d := NewDirField(theme.Default())
	d.SetCandidates(1, []string{"/home/z/Projects/herdr", "/home/z/Projects/atrium"})

	if got := d.Typed(); got != "" {
		t.Errorf("Typed() on a fresh field = %q, want empty", got)
	}
	if got := d.Value(); got != "/home/z/Projects/herdr" {
		t.Errorf("Value() = %q, want the first candidate", got)
	}

	d.Focus()
	typeInto(d, "atr")
	if got := d.Typed(); got != "atr" {
		t.Errorf("Typed() = %q, want %q", got, "atr")
	}
	if got := d.Value(); got != "/home/z/Projects/atrium" {
		t.Errorf("Value() = %q, want the ranked selection", got)
	}
}

// TestDirFieldLiteralFallbackUsesTheInstalledExpander pins why
// SetPathExpander exists: without it the fallback row is the raw "~/x"
// while every browsed row around it is absolute, so the same directory
// can appear twice and a "~" selection can reach a herdr CLI that does
// not expand it.
func TestDirFieldLiteralFallbackUsesTheInstalledExpander(t *testing.T) {
	d := NewDirField(theme.Default())
	d.SetPathExpander(func(raw string) string {
		return strings.Replace(raw, "~", "/home/z", 1)
	})
	// What a browse of "~/Projects/" supplies: absolute child paths.
	d.SetCandidates(1, []string{"/home/z/Projects/herdr", "/home/z/Projects/atrium"})

	d.Focus()
	typeInto(d, "~/Projects/nope")
	items := d.visibleItems()
	last := items[len(items)-1]
	if last != "/home/z/Projects/nope" {
		t.Errorf("literal fallback = %q, want the expanded path", last)
	}

	// A fully typed path that IS on offer must not appear twice, once
	// browsed and once as its own literal fallback.
	d.input.SetValue("")
	typeInto(d, "~/Projects/herdr")
	seen := 0
	for _, it := range d.visibleItems() {
		if it == "/home/z/Projects/herdr" {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("%q appears %d times in %v, want exactly once", "/home/z/Projects/herdr", seen, d.visibleItems())
	}
}

// TestDirFieldWithoutAnExpanderKeepsTheRawFallback pins the default: no
// expander installed means no expansion, which is what every other test
// in this file (and every golden frame) observes.
func TestDirFieldWithoutAnExpanderKeepsTheRawFallback(t *testing.T) {
	d := NewDirField(theme.Default())
	d.SetCandidates(1, []string{"/srv/a"})
	d.Focus()
	typeInto(d, "~/b")

	items := d.visibleItems()
	if last := items[len(items)-1]; last != "~/b" {
		t.Errorf("literal fallback = %q, want the raw typed text", last)
	}
}

// TestLooksLikePathAndSplitPathAreTheSharedGrammar pins the exported
// pair the app layer's browse source reads -- one definition of path
// mode, not two.
func TestLooksLikePathAndSplitPathAreTheSharedGrammar(t *testing.T) {
	for _, tc := range []struct {
		in   string
		path bool
	}{
		{"/srv/x", true}, {"~/x", true}, {"./x", true}, {"../x", true},
		{"~", true}, {".", true},
		{"herdr", false}, {"", false}, {"my-project", false},
	} {
		if got := LooksLikePath(tc.in); got != tc.path {
			t.Errorf("LooksLikePath(%q) = %v, want %v", tc.in, got, tc.path)
		}
	}

	for _, tc := range []struct{ in, dir, base string }{
		{"~/Projects/he", "~/Projects/", "he"},
		{"~/Projects/", "~/Projects/", ""},
		{"~", "~", ""},
		{".", ".", ""},
		// A single leading separator: the root IS the directory to browse.
		{"/srv", "/", "srv"},
		{"/srv/", "/srv/", ""},
	} {
		dir, base := SplitPath(tc.in)
		if dir != tc.dir || base != tc.base {
			t.Errorf("SplitPath(%q) = (%q, %q), want (%q, %q)", tc.in, dir, base, tc.dir, tc.base)
		}
	}
}

// TestDirFieldCompleteLeavesTheCursorAtTheEnd pins a bug live validation
// found: bubbles' textinput.SetValue only repositions a cursor that would
// be out of bounds, so completing "/srv/gam" to "/srv/gamma-tools" left
// the cursor after "gam" and the next keystroke landed in the middle of
// the path.
func TestDirFieldCompleteLeavesTheCursorAtTheEnd(t *testing.T) {
	d := NewDirField(theme.Default())
	d.SetCandidates(1, []string{"/srv/gamma-tools", "/srv/alpha"})
	d.Focus()
	typeInto(d, "/srv/gam")

	if !d.Complete() {
		t.Fatalf("Complete() = false, want a completion to %q", "/srv/gamma-tools")
	}
	if got := d.Typed(); got != "/srv/gamma-tools" {
		t.Fatalf("after Complete(), Typed() = %q", got)
	}

	typeInto(d, "/")
	if got := d.Typed(); got != "/srv/gamma-tools/" {
		t.Errorf("after typing past a completion, Typed() = %q, want the keystroke at the END", got)
	}
}
