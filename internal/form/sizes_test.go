package form

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// --- the drop-blank-lines stage, which used to be dead ---------------------

// TestFitToHeight_DropsInteriorBlankLines pins spec §6's degradation
// ladder's SECOND rung, which had never once fired in production: the
// stage tested `l == ""`, but form.go's compose runs every line through
// decorateFocus (which prefixes a two-cell gutter) and every field pads its
// reserved-but-empty rows out to the full inner width, so no line reaching
// it was ever the empty string. Every overlong render therefore skipped
// straight to dropping dividers and then clipping real content.
//
// The three shapes below are exactly the ones compose actually produces --
// a gutter-prefixed empty line, a width-padded blank row, and a blank row
// carrying a styled-but-empty span (a dim hint with no text, e.g.
// DirField's own reserved hint row) -- so a regression to a plain equality
// test fails here rather than silently going quiet again.
func TestFitToHeight_DropsInteriorBlankLines(t *testing.T) {
	styledBlank := decorateFocus(fitLine(dimHint(theme.Default()).Render(""), 20), false, theme.Default())
	lines := []string{
		"first",
		"content A",
		decorateFocus("", false, theme.Default()), // gutter only
		"content B",
		fitLine("", 20), // width-padded blank
		"content C",
		styledBlank,
		"last",
	}

	got := fitToHeight(lines, nil, 5, "\u2500\u2500", -1)

	if len(got) != 5 {
		t.Fatalf("fitToHeight produced %d lines, want 5:\n%q", len(got), got)
	}
	for _, want := range []string{"first", "content A", "content B", "content C", "last"} {
		if !containsLine(got, want) {
			t.Errorf("fitToHeight dropped %q, which is real content -- only blank lines should have gone:\n%q", want, got)
		}
	}
}

func containsLine(lines []string, want string) bool {
	for _, l := range lines {
		if ansi.Strip(l) == want {
			return true
		}
	}
	return false
}

// TestFitToHeight_NeverDropsProtectedLines pins the focus-protection the
// ladder gained alongside the allocator: a composed form too short even for
// every section's floor must lose SOMETHING, and the one thing it must
// never lose is the section the user is currently editing.
func TestFitToHeight_NeverDropsProtectedLines(t *testing.T) {
	lines := []string{"pad", "a", "b", "c", "FOCUSED", "d", "e", "Create"}
	protect := []bool{false, false, false, false, true, false, false, true}

	got := fitToHeight(lines, protect, 3, "---", -1)

	if len(got) != 3 {
		t.Fatalf("fitToHeight produced %d lines, want 3: %q", len(got), got)
	}
	if !containsLine(got, "FOCUSED") {
		t.Errorf("the focused section's line was dropped: %q", got)
	}
	if got[len(got)-1] != "Create" {
		t.Errorf("last line = %q, want the Create button preserved unconditionally", got[len(got)-1])
	}
}

// TestClipKeeping_WithoutProtectionIsTheOldTailClip pins that the clip
// stage's replacement is behavior-preserving where nothing is protected --
// the first budget-1 lines plus the last one, exactly what Atrium's own
// fitOverlay tail clip (and this package's previous clipTail) did. This is
// what keeps the committed degraded-80x20 golden frame byte-identical.
func TestClipKeeping_WithoutProtectionIsTheOldTailClip(t *testing.T) {
	lines := []string{"0", "1", "2", "3", "4", "5", "6"}

	got := clipKeeping(lines, nil, 4)

	want := []string{"0", "1", "2", "6"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("clipKeeping(nil protect) = %v, want %v", got, want)
	}
}

// --- the budget allocator -------------------------------------------------

// allocStub is a Section double with independently settable preferred and
// floor heights, for driving allocateHeights directly.
type allocStub struct {
	stubSection
	pref, floor int
}

func newAllocStub(id string, pref, floor int) *allocStub {
	s := &allocStub{pref: pref, floor: floor}
	s.id = id
	s.enabled = true
	s.height = func(int) int { return pref }
	s.content = func(int) string { return id }
	return s
}

func (s *allocStub) Height(int) int { return s.pref }
func (s *allocStub) MinHeight() int { return s.floor }

func allocSections(specs ...[2]int) []Section {
	out := make([]Section, len(specs))
	for i, sp := range specs {
		out[i] = newAllocStub("s", sp[0], sp[1])
	}
	return out
}

func sum(xs []int) int {
	total := 0
	for _, x := range xs {
		total += x
	}
	return total
}

// TestAllocateHeights_PreferencesWinWhenTheyFit pins the case every
// single-section golden frame in this package is in -- and therefore why
// those eleven frames render byte-identically to before the allocator
// existed: when every section's preference plus a divider per body section
// fits the budget, everyone gets exactly what they asked for, dividers
// included.
func TestAllocateHeights_PreferencesWinWhenTheyFit(t *testing.T) {
	sections := allocSections([2]int{6, 1}, [2]int{2, 1}, [2]int{1, 1})

	got, dividers := allocateHeights(sections, 0, 24, 22)

	if !dividers {
		t.Errorf("dividers = false, want true -- the preferred layout fits with them")
	}
	want := []int{6, 2, 1}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("allocateHeights = %v, want %v", got, want)
		}
	}
}

// TestAllocateHeights_DividersGoBeforeContent pins spec §6's own
// degradation order at the allocation layer: separators are given up
// before any section is asked to shrink.
func TestAllocateHeights_DividersGoBeforeContent(t *testing.T) {
	// Preferences total 9; with 2 dividers that is 11, over the budget of
	// 10, but 9 alone fits.
	sections := allocSections([2]int{6, 1}, [2]int{2, 1}, [2]int{1, 1})

	got, dividers := allocateHeights(sections, 0, 24, 10)

	if dividers {
		t.Errorf("dividers = true, want false -- they no longer fit")
	}
	if sum(got) != 9 {
		t.Fatalf("allocateHeights = %v (sum %d), want every preference intact (sum 9)", got, sum(got))
	}
}

// TestAllocateHeights_FocusedSectionFillsFirst pins the rule that makes the
// assembled form usable at 80x24: when preferences do not fit, every
// section drops to its floor and the FOCUSED one is refilled to its full
// preference before anything else grows. A picker collapsed to its floor
// while focused is a field the user cannot use.
func TestAllocateHeights_FocusedSectionFillsFirst(t *testing.T) {
	sections := allocSections([2]int{6, 1}, [2]int{6, 1}, [2]int{6, 1}, [2]int{1, 1})

	got, dividers := allocateHeights(sections, 1, 24, 10)

	if dividers {
		t.Errorf("dividers = true, want false")
	}
	if got[1] != 6 {
		t.Errorf("focused section got %d rows, want its full preference of 6 (all: %v)", got[1], got)
	}
	if sum(got) != 10 {
		t.Errorf("allocateHeights spent %d of a 10-row budget (%v), want all of it", sum(got), got)
	}
	for i, h := range got {
		if h < 1 {
			t.Errorf("section %d got %d rows -- every section must keep at least its own header row (%v)", i, h, got)
		}
	}
}

// TestAllocateHeights_FloorsSurviveAnImpossibleBudget pins the degenerate
// case: a budget too small for even the floors returns the floors anyway
// (never zero or negative heights), leaving fitToHeight's own protected
// ladder to decide what actually reaches the screen.
func TestAllocateHeights_FloorsSurviveAnImpossibleBudget(t *testing.T) {
	sections := allocSections([2]int{6, 2}, [2]int{6, 2}, [2]int{6, 2})

	got, dividers := allocateHeights(sections, 0, 24, 3)

	if dividers {
		t.Errorf("dividers = true, want false")
	}
	for i, h := range got {
		if h != 2 {
			t.Errorf("section %d got %d rows, want its floor of 2 (%v)", i, h, got)
		}
	}
}

// --- per-field winH ladders ----------------------------------------------

// TestPickerRowsAt_ShrinksWithTheWindow pins that Section.Height(winH) is
// now a real function of winH rather than a constant that ignored it: a
// picker asks for its full row count only while the window can hold the
// form's own chrome plus a full-size picker, and never asks for zero rows.
func TestPickerRowsAt_ShrinksWithTheWindow(t *testing.T) {
	if got := pickerRowsAt(6, 40); got != 6 {
		t.Errorf("pickerRowsAt(6, 40) = %d, want 6 (a tall window affords the full picker)", got)
	}
	if got := pickerRowsAt(6, 24); got != 6 {
		t.Errorf("pickerRowsAt(6, 24) = %d, want 6 -- shrinking at 24 would shift the committed golden frames", got)
	}
	prev := pickerRowsAt(6, 12)
	for winH := 11; winH >= 1; winH-- {
		got := pickerRowsAt(6, winH)
		if got > prev {
			t.Errorf("pickerRowsAt(6, %d) = %d grew as the window shrank (previous %d)", winH, got, prev)
		}
		if got < 1 {
			t.Errorf("pickerRowsAt(6, %d) = %d, want at least 1 row", winH, got)
		}
		prev = got
	}
	if got := pickerRowsAt(6, 8); got >= 6 {
		t.Errorf("pickerRowsAt(6, 8) = %d, want fewer than the preferred 6 in an 8-row popup", got)
	}
}

// TestPromptRowsAt_HonorsSpecFieldEightsBounds pins spec §6 field 8
// verbatim: "4 rows preferred, 1 floor".
func TestPromptRowsAt_HonorsSpecFieldEightsBounds(t *testing.T) {
	if got := promptRowsAt(4, 1, 40); got != 4 {
		t.Errorf("promptRowsAt(4, 1, 40) = %d, want the preferred 4", got)
	}
	if got := promptRowsAt(4, 1, 5); got != 1 {
		t.Errorf("promptRowsAt(4, 1, 5) = %d, want the floor of 1 in a 5-row popup", got)
	}
	if got := promptRowsAt(4, 1, 1); got != 1 {
		t.Errorf("promptRowsAt(4, 1, 1) = %d, want the floor of 1, never less", got)
	}
}

// --- Section.View renders exactly the height it is allocated -------------

// TestFieldSections_RenderExactlyTheirAllocatedHeight pins Section.View's
// own contract across every concrete field in this package, at every height
// from its floor up past its preference: the line count compose books
// against the allocation must be the line count actually emitted, or the
// whole layout desynchronizes.
func TestFieldSections_RenderExactlyTheirAllocatedHeight(t *testing.T) {
	palette := theme.Default()
	w := NewWorktreeField(palette)
	w.SetGitTarget(true)
	w.SetOn(true)

	sections := map[string]Section{
		"issue":     NewIssueField(palette),
		"dir":       NewDirField(palette),
		"title":     NewTitleField(palette),
		"worktree":  w.ChipsSection(),
		"branch":    w.BranchSection(),
		"base":      w.BaseSection(),
		"placement": NewPlacementField(palette),
		"agent":     NewAgentField(palette),
		"account":   NewAccountField(palette),
		"prompt":    NewPromptField(palette),
	}

	for id, s := range sections {
		for h := s.MinHeight(); h <= s.Height(40)+2; h++ {
			got := strings.Count(s.View(60, h), "\n") + 1
			if got != h {
				t.Errorf("%s.View(60, %d) rendered %d physical lines, want exactly %d", id, h, got, h)
			}
		}
	}
}

// TestFieldSections_ShowTheirLabelAtTheirFloor pins the reason MinHeight is
// what it is: a field squeezed down to its floor must still tell the user
// which field it is. This is the per-field half of the app-layer assertion
// that the focused section is always visible at 80x24.
func TestFieldSections_ShowTheirLabelAtTheirFloor(t *testing.T) {
	palette := theme.Default()
	w := NewWorktreeField(palette)
	w.SetGitTarget(true)
	w.SetOn(true)

	cases := []struct {
		section Section
		marker  string
	}{
		{NewIssueField(palette), "Issue:"},
		{NewDirField(palette), "Project:"},
		{NewTitleField(palette), "Title:"},
		{w.ChipsSection(), "Off"},
		{w.BranchSection(), "Branch:"},
		{w.BaseSection(), "Base:"},
		{NewPlacementField(palette), "New space"},
		{NewAccountField(palette), "Account:"},
		{NewPromptField(palette), "Prompt:"},
	}

	for _, tc := range cases {
		got := ansi.Strip(tc.section.View(60, tc.section.MinHeight()))
		if !strings.Contains(got, tc.marker) {
			t.Errorf("%s at its floor of %d lines does not show %q:\n%q",
				tc.section.ID(), tc.section.MinHeight(), tc.marker, got)
		}
	}
}

// TestModel_ComposeFitsExactlyTheWindow pins the composed render's own
// shape at a spread of window heights, including ones far below anything
// the form's sections can collectively fit into: exactly h lines out, with
// the Create button always on the last one.
func TestModel_ComposeFitsExactlyTheWindow(t *testing.T) {
	palette := theme.Default()
	sections := make([]Section, 0, 10)
	for i := 0; i < 10; i++ {
		sections = append(sections, newAllocStub("s", 6, 1))
	}
	m := New(Setup{Palette: palette, Sections: sections})
	m.Init()

	for _, h := range []int{6, 10, 14, 20, 24, 40, 60} {
		got := m.ViewAt(80, h)
		if lines := strings.Count(got, "\n") + 1; lines != h {
			t.Errorf("ViewAt(80, %d) produced %d lines, want exactly %d", h, lines, h)
		}
		rows := strings.Split(got, "\n")
		if last := ansi.Strip(rows[len(rows)-1]); !strings.Contains(last, "Create") {
			t.Errorf("ViewAt(80, %d) last row = %q, want the Create button", h, last)
		}
	}
}
