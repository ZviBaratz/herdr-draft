package form

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ZviBaratz/herdr-draft/internal/form/widgets"
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

// mustItems is visibleItems' item list alone, for the assertions that
// care about WHICH directories are on offer rather than about the query
// v3 spec §8.4's highlight is computed from.
func mustItems(d *DirField) []string {
	items, _ := d.visibleItems()
	return items
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

	// The marker lives on the ROW, and v1's parenthesized "(invalid)"/
	// "(direct)" became v2 spec §6's own words.
	d.SetValidity("/home/z/repo-a", ValidityInvalid)
	if got := rowText(d.Row(60)); !strings.Contains(got, dirRowInvalid) {
		t.Fatalf("Row(60) = %q, want it to contain %q for the current selection", got, dirRowInvalid)
	}

	d.picker.CursorNext() // selection moves to repo-b, which has no verdict yet
	if got := rowText(d.Row(60)); strings.Contains(got, dirRowInvalid) {
		t.Fatalf("Row(60) = %q, still shows the stale marker after the selection moved", got)
	}

	d.SetValidity("/home/z/repo-b", ValidityDirect)
	if got := rowText(d.Row(60)); !strings.Contains(got, dirRowNotRepo) {
		t.Fatalf("Row(60) = %q, want it to contain %q for repo-b", got, dirRowNotRepo)
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

// TestDirField_NotesAreBookedRenderedAndOffTheRow is v2 spec §11's
// ignored-key report, which lands on THIS panel because the file it
// describes is a property of the selected project (SetNotes). It pins the
// same three things every panel line has to get right: PanelRows books it,
// the panel renders it, and the row says nothing about it.
func TestDirField_NotesAreBookedRenderedAndOffTheRow(t *testing.T) {
	d := NewDirField(theme.Default())
	d.SetCandidates(1, []string{"/home/z/a", "/home/z/b"})
	rowBefore := d.Row(60)
	bare := d.PanelRows()

	notes := []string{
		"ignoring agents.extra_args: it becomes part of a launched agent's command line",
		"ignoring clauth: a repository does not configure your clauth accounts",
	}
	d.SetNotes(notes)

	if got := d.PanelRows(); got != bare+len(notes) {
		t.Fatalf("PanelRows() with %d notes = %d, want %d", len(notes), got, bare+len(notes))
	}
	if got := d.Row(60); got != rowBefore {
		t.Errorf("Row(60) changed when notes were set:\n before: %q\n  after: %q", rowText(rowBefore), rowText(got))
	}

	panel := ansi.Strip(d.Panel(80, d.PanelRows()))
	for _, n := range notes {
		if !strings.Contains(panel, n) {
			t.Errorf("Panel = %q, want it to carry %q", panel, n)
		}
	}
	// Both candidates are still on offer: the report was paid for, not
	// taken out of the chooser.
	for _, c := range []string{"/home/z/a", "/home/z/b"} {
		if !strings.Contains(panel, c) {
			t.Errorf("Panel = %q, want candidate %q still listed beside the notes", panel, c)
		}
	}

	d.SetNotes(nil)
	if got := d.PanelRows(); got != bare {
		t.Errorf("PanelRows() after clearing the notes = %d, want %d back", got, bare)
	}
}

// TestDirField_NotesNeverEmptyTheChooser is the panel-floor rule: a
// repository with a long report must not cost the project row the one
// candidate line that makes it a chooser at all (notesShown).
func TestDirField_NotesNeverEmptyTheChooser(t *testing.T) {
	d := NewDirField(theme.Default())
	d.SetCandidates(1, []string{"/home/z/a", "/home/z/b", "/home/z/c"})
	d.SetNotes([]string{"ignoring palette: a repository does not set your colors",
		"ignoring timeouts: a repository does not set your timeouts",
		"ignoring linear.api_key: it is a credential"})

	for _, h := range []int{1, 2, panelFloor, 4, 8} {
		panel := ansi.Strip(d.Panel(80, h))
		lines := strings.Split(panel, "\n")
		if len(lines) != h {
			t.Fatalf("Panel(80, %d) produced %d lines, want %d", h, len(lines), h)
		}
		if h >= panelFloor && !strings.Contains(panel, "/home/z/a") {
			t.Errorf("Panel(80, %d) = %q, want the chooser to keep at least its cursor row", h, panel)
		}
	}

	// The report is truncated from the BACK, so the first note is the one
	// that survives a tight region.
	if got := ansi.Strip(d.Panel(80, panelFloor)); !strings.Contains(got, "ignoring palette") {
		t.Errorf("Panel at the floor = %q, want the first note kept", got)
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
	_ = d.Row(0)
	_ = d.Panel(0, d.PanelRows())
	_ = d.Value()
	_ = d.Complete()
	d.Focus()
	_ = d.Row(-5)
	_ = d.Panel(-5, d.PanelRows())
	d.SetCandidates(1, nil)
	_ = d.Row(1)
	_ = d.Panel(1, d.PanelRows())
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
	items, _ := d.visibleItems()
	last := items[len(items)-1]
	if last != "/home/z/Projects/nope" {
		t.Errorf("literal fallback = %q, want the expanded path", last)
	}

	// A fully typed path that IS on offer must not appear twice, once
	// browsed and once as its own literal fallback.
	d.input.SetValue("")
	typeInto(d, "~/Projects/herdr")
	seen := 0
	for _, it := range mustItems(d) {
		if it == "/home/z/Projects/herdr" {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("%q appears %d times in %v, want exactly once", "/home/z/Projects/herdr", seen, mustItems(d))
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

	items, _ := d.visibleItems()
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

// TestDirField_FilterCountDiscountsTheLiteralRow pins the DirField half
// of v3 spec §8.5's numerator. Path mode appends the literal typed path
// (pathModeItems) so a not-yet-listed directory stays selectable, and
// that row is not one of the candidates on offer: counting it reads
// `3/3 directories` for a query two of the three match, i.e. "nothing was
// filtered out" about a filtered list.
func TestDirField_FilterCountDiscountsTheLiteralRow(t *testing.T) {
	d := NewDirField(theme.Default())
	d.SetHomeDir("/home/zvi")
	d.SetCandidates(1, []string{
		"/home/zvi/Projects/herdr",
		"/home/zvi/Projects/herdr-draft",
		"/home/zvi/Projects/atrium",
	})
	d.Focus()

	if got, want := d.filterCount(), "3 directories"; got != want {
		t.Errorf("filterCount with nothing typed = %q, want %q", got, want)
	}

	for _, r := range "~/Projects/h" {
		d.Update(rn(r))
	}
	if got, want := d.picker.FilteredLen(), 3; got != want {
		t.Fatalf("the picker holds %d rows, want %d (two candidates plus the literal) -- this test is about the difference", got, want)
	}
	if got, want := d.filterCount(), "2/3 directories"; got != want {
		t.Errorf("filterCount in path mode = %q, want %q", got, want)
	}
}

// --- v3 spec §8.4, the spans this field supplies itself -------------------

// TestDirMatch_BridgesTheInclusiveSpanToTheHalfOpenOne is the fencepost
// guard for the ONE place in this codebase where the project's two span
// conventions meet. fuzzyMatch's End is inclusive; widgets.PickerMatch's
// is half-open, so that its zero value can be inert on the great majority
// of items that carry no match. dirMatch is the +1, and §8.4's boxed
// warning calls it the likeliest silent bug left in the redesign.
//
// The two cases that matter are the ends: a span on the FIRST rune with
// the +1 dropped collapses to [0,0), which PickerMatch.empty() reads as
// "nothing to paint", and a span on the LAST rune does the same at [n,n).
// Both would render as a plainly unhighlighted row rather than as a
// visibly misplaced highlight, which is why they are asserted on the
// coordinates and not only on the pixels.
func TestDirMatch_BridgesTheInclusiveSpanToTheHalfOpenOne(t *testing.T) {
	const display = "~/Projects/atrium" // 17 runes: "~" at 0, "m" at 16
	for _, tc := range []struct {
		name    string
		display string
		query   string
		want    widgets.PickerMatch
	}{
		{"the first rune of the cell", display, "~", widgets.PickerMatch{Col: 0, Start: 0, End: 1}},
		{"the last rune of the cell", display, "m", widgets.PickerMatch{Col: 0, Start: 16, End: 17}},
		{"a run in the middle", display, "atr", widgets.PickerMatch{Col: 0, Start: 11, End: 14}},
		{"the whole cell", display, display, widgets.PickerMatch{Col: 0, Start: 0, End: 17}},
		{"no filter typed", display, "", widgets.PickerMatch{Col: -1}},
		{"a candidate the display cannot show the match in", display, "zzz", widgets.PickerMatch{Col: -1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := dirMatch(tc.display, tc.query); got != tc.want {
				t.Errorf("dirMatch(%q, %q) = %+v, want %+v", tc.display, tc.query, got, tc.want)
			}
		})
	}
}

// TestDirField_HighlightsWhatIsDrawnNotWhatWasRanked is v3 spec §8.4's
// "match the string that will be DISPLAYED" on the field that has to obey
// it. Fragment mode ranks the FULL path and draws it home-collapsed, so
// the ranker's own span is nine runes adrift of the cell -- far enough
// past the end of "~/Projects/herdr-draft" that carrying it through would
// paint nothing at all rather than paint the wrong characters.
//
// This field is also the only one of the five that must supply spans at
// all: it ranks its own candidates and never calls SetQuery, so
// widgets.Picker.applyFilter copies its Match through untouched and
// nothing else would ever fill it in.
func TestDirField_HighlightsWhatIsDrawnNotWhatWasRanked(t *testing.T) {
	d := NewDirField(theme.Default())
	d.SetHomeDir("/home/zvi")
	d.SetCandidates(1, []string{"/home/zvi/Projects/herdr-draft"})
	d.Focus()
	typeInto(d, "draft") // no "/", "~" or "." -- fragment mode

	item, ok := d.picker.Selected()
	if !ok {
		t.Fatal("nothing selected after filtering to the one matching candidate")
	}
	// "~/Projects/herdr-draft": the second "draft" starts at rune 17. The
	// span against the RANKED "/home/zvi/Projects/herdr-draft" is [25,30),
	// which is off the end of a 22-rune cell.
	if want := (widgets.PickerMatch{Col: 0, Start: 17, End: 22}); item.Match != want {
		t.Errorf("Match = %+v, want %+v -- the span must be computed against %q, not against %q",
			item.Match, want, item.Cells[0], item.ID)
	}
	if got, want := accentedRun(theme.Default(), d.Panel(60, 4)), "draft"; got != want {
		t.Errorf("the panel accents %q, want %q", got, want)
	}
}

// TestDirField_PathModeHighlightsInsideTheFullPath is the other transform
// §8.4 names: path mode ranks each candidate's BASENAME and draws the
// whole path, so a span carried out of the ranker would be an index into
// a string the panel never shows.
func TestDirField_PathModeHighlightsInsideTheFullPath(t *testing.T) {
	d := NewDirField(theme.Default())
	d.SetHomeDir("/home/zvi")
	d.SetCandidates(1, []string{"/home/zvi/Projects/herdr-draft"})
	d.Focus()
	typeInto(d, "~/Projects/hd")

	item, ok := d.picker.Selected()
	if !ok {
		t.Fatal("nothing selected after filtering to the one matching candidate")
	}
	// The query the ranker used is SplitPath's base, "hd", not the whole
	// raw text -- which does not occur in the cell at all.
	//
	// The painted run is the match WINDOW, not the matched characters:
	// PickerMatch is one contiguous span by construction, and fuzzyMatch's
	// backward pass tightens that window to "herd" (the "h" of "herdr"
	// through the "d" of "herdr", not the later "d" of "draft"). Both
	// halves are worth pinning -- a span that widened to the last "d"
	// would mean the tightening pass had stopped running.
	if want := (widgets.PickerMatch{Col: 0, Start: 11, End: 15}); item.Match != want {
		t.Errorf("Match = %+v, want %+v for cell %q", item.Match, want, item.Cells[0])
	}
	if got, want := accentedRun(theme.Default(), d.Panel(60, 4)), "herd"; got != want {
		t.Errorf("the panel accents %q, want %q", got, want)
	}
}

// TestDirField_RestingPanelHighlightsNothing pins the state the golden
// suite let through once before (CLAUDE.md's "a golden-frame suite proves
// only the states someone thought to fixture"): the panel as it OPENS,
// with nothing typed. fuzzyMatch answers an empty query with [0, -1],
// which is End < Start and therefore inert -- but a bridge that added its
// +1 unconditionally would turn that into the half-open [0, 0)... and a
// bridge that also clamped a negative End would turn it into [0, 1) and
// light the first character of every directory on the list.
func TestDirField_RestingPanelHighlightsNothing(t *testing.T) {
	d := NewDirField(theme.Default())
	d.SetHomeDir("/home/zvi")
	d.SetCandidates(1, []string{
		"/home/zvi/Projects/herdr",
		"/home/zvi/Projects/atrium",
	})
	d.Focus()

	if got := accentedRun(theme.Default(), d.Panel(60, 4)); got != "" {
		t.Errorf("the resting panel accents %q, want nothing at all", got)
	}
}

// accentedRun returns the plain text of the first run in s painted in the
// palette's Accent AND bold -- widgets.Picker.matchStyle, and nothing else
// this form draws. The opening escape is derived from the style rather
// than written out, so a lipgloss change to SGR parameter order moves
// this with it instead of quietly making every caller assert nothing.
func accentedRun(p theme.Palette, s string) string {
	open, _, _ := strings.Cut(lipgloss.NewStyle().Foreground(p.Accent).Bold(true).Render("\x00"), "\x00")
	_, after, ok := strings.Cut(s, open)
	if !ok {
		return ""
	}
	run, _, _ := strings.Cut(after, "\x1b")
	return run
}
