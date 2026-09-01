package form

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

func TestWorktreeField_SectionIDs(t *testing.T) {
	w := NewWorktreeField(theme.Default())
	if got := w.ChipsSection().ID(); got != "worktree" {
		t.Errorf("ChipsSection().ID() = %q, want %q", got, "worktree")
	}
	if got := w.BranchSection().ID(); got != "branch" {
		t.Errorf("BranchSection().ID() = %q, want %q", got, "branch")
	}
	if got := w.BaseSection().ID(); got != "base" {
		t.Errorf("BaseSection().ID() = %q, want %q", got, "base")
	}
}

func TestWorktreeField_DefaultsToOffAndInert(t *testing.T) {
	w := NewWorktreeField(theme.Default())
	if w.On() {
		t.Errorf("On() = true on a fresh field, want false")
	}
	if w.Enabled() {
		t.Errorf("Enabled() = true before any SetGitTarget call, want false (safe default)")
	}
	if w.ChipsSection().Enabled() {
		t.Errorf("ChipsSection().Enabled() = true before SetGitTarget, want false")
	}
}

// TestWorktreeField_GitTargetGatesEverything pins SetGitTarget's own
// contract: a non-git target makes chips/branch/base all present-but-
// inert regardless of the on/off toggle; a git target re-enables the
// chips row (branch/base still gated by On(), separately).
func TestWorktreeField_GitTargetGatesEverything(t *testing.T) {
	w := NewWorktreeField(theme.Default())
	w.SetGitTarget(true)

	if !w.Enabled() {
		t.Errorf("Enabled() = false after SetGitTarget(true), want true")
	}
	if !w.ChipsSection().Enabled() {
		t.Errorf("ChipsSection().Enabled() = false after SetGitTarget(true), want true")
	}
	// Worktree is still off -- branch/base stay inert.
	if w.BranchSection().Enabled() {
		t.Errorf("BranchSection().Enabled() = true while off, want false")
	}
	if w.BaseSection().Enabled() {
		t.Errorf("BaseSection().Enabled() = true while off, want false")
	}

	turnOn(w)
	if !w.BranchSection().Enabled() {
		t.Errorf("BranchSection().Enabled() = false once On() and git, want true")
	}
	if !w.BaseSection().Enabled() {
		t.Errorf("BaseSection().Enabled() = false once On() and git, want true")
	}

	// Flipping back to a non-git target must force branch/base inert
	// again even though the toggle is still "on".
	w.SetGitTarget(false)
	if w.BranchSection().Enabled() {
		t.Errorf("BranchSection().Enabled() = true for a non-git target even while on, want false")
	}
	if w.BaseSection().Enabled() {
		t.Errorf("BaseSection().Enabled() = true for a non-git target even while on, want false")
	}
}

// turnOn toggles the chips row from its default "off" to "on" via a
// single Right arrow press, through the real Section.Update path.
func turnOn(w *WorktreeField) {
	w.ChipsSection().Update(key(tea.KeyRight, 0))
}

func TestWorktreeField_ChipsToggleOnOff(t *testing.T) {
	w := NewWorktreeField(theme.Default())
	w.SetGitTarget(true)

	turnOn(w)
	if !w.On() {
		t.Fatalf("On() = false after one Right on the chips zone, want true")
	}

	w.ChipsSection().Update(key(tea.KeyLeft, 0))
	if w.On() {
		t.Fatalf("On() = true after Left back, want false")
	}
}

// TestWorktreeField_SetOn pins the app-layer setter Task 20 added (see
// SetOn's own doc comment): it must apply the requested state regardless of
// starting position, and be a no-op when already there.
func TestWorktreeField_SetOn(t *testing.T) {
	w := NewWorktreeField(theme.Default())
	w.SetGitTarget(true) // ChipRow.Next()/Prev() -- what SetOn uses -- are no-ops while inert.
	if w.On() {
		t.Fatalf("On() = true on a fresh WorktreeField, want false (defaults off)")
	}

	w.SetOn(true)
	if !w.On() {
		t.Fatalf("On() = false after SetOn(true), want true")
	}

	// Already on: a further SetOn(true) must be a no-op, not toggle back off.
	w.SetOn(true)
	if !w.On() {
		t.Fatalf("On() = false after a redundant SetOn(true), want true (no-op)")
	}

	w.SetOn(false)
	if w.On() {
		t.Fatalf("On() = true after SetOn(false), want false")
	}
	w.SetOn(false)
	if w.On() {
		t.Fatalf("On() = true after a redundant SetOn(false), want false (no-op)")
	}
}

// TestWorktreeField_TouchedRule is the brief's own literal requirement,
// verbatim: SetBranch("zvi/from-linear", true) seeds a value; the user
// then types; a LATER SetBranch(..., true) call (another seed attempt,
// e.g. a debounced re-fetch of the same suggestion) must be ignored,
// since the user has already taken over editing the field.
func TestWorktreeField_TouchedRule(t *testing.T) {
	w := NewWorktreeField(theme.Default())
	w.SetGitTarget(true)
	turnOn(w)

	w.SetBranch("zvi/from-linear", true)
	if got := w.Branch(); got != "zvi/from-linear" {
		t.Fatalf("Branch() after seeding = %q, want %q", got, "zvi/from-linear")
	}

	// The user types, taking over the field (a real focus stop must be
	// focused first, matching how the form's own ring drives it: an
	// unfocused text input ignores keystrokes).
	w.BranchSection().Focus()
	w.BranchSection().Update(rn('-'))
	if got := w.Branch(); got != "zvi/from-linear-" {
		t.Fatalf("Branch() after typing = %q, want %q", got, "zvi/from-linear-")
	}

	// A later seed attempt for the SAME (or a different) suggestion must
	// be ignored now that the user has touched the field.
	w.SetBranch("zvi/from-linear", true)
	if got := w.Branch(); got != "zvi/from-linear-" {
		t.Fatalf("Branch() after a post-touch seeded SetBranch = %q, want the user's edit %q preserved", got, "zvi/from-linear-")
	}
	w.SetBranch("something/else", true)
	if got := w.Branch(); got != "zvi/from-linear-" {
		t.Fatalf("Branch() after a second post-touch seeded SetBranch = %q, want the user's edit %q preserved", got, "zvi/from-linear-")
	}
}

// TestWorktreeField_HardSetOverridesTouched pins SetBranch's seeded=false
// path: an authoritative (non-seeded) set always applies, even after the
// field has been touched, and clears the touched flag so a later seed can
// apply again.
func TestWorktreeField_HardSetOverridesTouched(t *testing.T) {
	w := NewWorktreeField(theme.Default())
	w.SetGitTarget(true)
	turnOn(w)

	w.BranchSection().Focus()
	w.BranchSection().Update(rn('x')) // touch it
	if got := w.Branch(); got != "x" {
		t.Fatalf("setup: Branch() after typing 'x' = %q, want %q", got, "x")
	}
	w.SetBranch("reset-value", false)
	if got := w.Branch(); got != "reset-value" {
		t.Fatalf("Branch() after a hard SetBranch = %q, want %q", got, "reset-value")
	}

	// touched must be cleared by the hard set, so a subsequent seed
	// applies again.
	w.SetBranch("seeded-again", true)
	if got := w.Branch(); got != "seeded-again" {
		t.Fatalf("Branch() after a seed following a hard reset = %q, want %q (touched cleared by the hard set)", got, "seeded-again")
	}
}

func TestWorktreeField_BaseDefaultsToHEAD(t *testing.T) {
	w := NewWorktreeField(theme.Default())
	if got := w.Base(); got != "" {
		t.Fatalf("Base() on a fresh field = %q, want \"\" (HEAD)", got)
	}
}

// TestWorktreeField_BaseSentinelHasNonEmptyID pins review round 1's first
// Important finding directly: the HEAD row's own widgets.PickerItem.ID
// must be non-empty (widgets.Picker's own carried fact -- Task 14 --
// requires unique, non-empty IDs from every caller), even though the
// PUBLIC Base() contract still reports "" for that row.
func TestWorktreeField_BaseSentinelHasNonEmptyID(t *testing.T) {
	w := NewWorktreeField(theme.Default())
	w.SetBaseItems(1, []string{"main"})

	sel, ok := w.base.Selected() // cursor starts on the HEAD sentinel row
	if !ok {
		t.Fatalf("base picker has no selection on a freshly seeded field")
	}
	if sel.ID == "" {
		t.Fatalf("HEAD sentinel PickerItem.ID = \"\", want a non-empty internal sentinel (widgets.Picker requires unique, non-empty IDs)")
	}
	if got := w.Base(); got != "" {
		t.Fatalf("Base() = %q for the HEAD row, want \"\" (the internal sentinel ID must not leak through the public getter)", got)
	}
}

// TestWorktreeField_SetBaseItemsRefreshPreservesSelectionByID pins review
// round 1's requirement directly: a same-version SetBaseItems refresh that
// REORDERS the ref list must keep the same ref selected by ID (inherited
// from widgets.Picker.SetItems' own same-version contract -- see
// field_dir.go's identical DirField test for the analogous candidate-pool
// case), not silently move the cursor onto whatever ref now sits at the
// same row.
func TestWorktreeField_SetBaseItemsRefreshPreservesSelectionByID(t *testing.T) {
	w := NewWorktreeField(theme.Default())
	w.SetBaseItems(1, []string{"main", "develop", "release"})
	w.BaseSection().Update(key(tea.KeyDown, 0)) // HEAD -> main
	w.BaseSection().Update(key(tea.KeyDown, 0)) // main -> develop
	if got := w.Base(); got != "develop" {
		t.Fatalf("setup: Base() = %q, want %q", got, "develop")
	}

	// Same version (1), reordered -- "develop" now sits where "main" used
	// to be.
	w.SetBaseItems(1, []string{"develop", "main", "release"})

	if got := w.Base(); got != "develop" {
		t.Fatalf("Base() after a same-version reordering refresh = %q, want %q (selection preserved by ID)", got, "develop")
	}
}

func TestWorktreeField_SetBaseItemsAndSelect(t *testing.T) {
	w := NewWorktreeField(theme.Default())
	w.SetGitTarget(true)
	turnOn(w)
	w.SetBaseItems(1, []string{"main", "develop"})

	w.BaseSection().Update(key(tea.KeyDown, 0)) // HEAD -> main
	if got := w.Base(); got != "main" {
		t.Fatalf("Base() after one Down = %q, want %q", got, "main")
	}
	w.BaseSection().Update(key(tea.KeyDown, 0)) // main -> develop
	if got := w.Base(); got != "develop" {
		t.Fatalf("Base() after two Downs = %q, want %q", got, "develop")
	}
	w.BaseSection().Update(key(tea.KeyUp, 0))
	w.BaseSection().Update(key(tea.KeyUp, 0))
	if got := w.Base(); got != "" {
		t.Fatalf("Base() back at the top = %q, want \"\" (HEAD)", got)
	}
}

func TestWorktreeField_SetBaseItemsStalenessGate(t *testing.T) {
	w := NewWorktreeField(theme.Default())
	w.SetBaseItems(2, []string{"fresh"})
	w.SetBaseItems(1, []string{"stale"}) // dropped: older version

	w.BaseSection().Update(key(tea.KeyDown, 0))
	if got := w.Base(); got != "fresh" {
		t.Fatalf("Base() = %q after a stale SetBaseItems call, want the fresher %q to survive", got, "fresh")
	}
}

func TestWorktreeField_SetBaseStatusShown(t *testing.T) {
	w := NewWorktreeField(theme.Default())
	w.SetGitTarget(true)
	turnOn(w)
	w.SetBaseStatus("searching…")

	frame := ansi.Strip(w.BaseSection().View(60))
	if !strings.Contains(frame, "searching…") {
		t.Fatalf("BaseSection().View(60) = %q, want it to contain the base status", frame)
	}
}

// TestWorktreeField_NonGitPlaceholdersAreDistinct pins the brief's own
// "inert w/ distinct placeholders" wording: the branch and base rows must
// show DIFFERENT placeholder text for "not a git repository" than for
// merely "off" (git repo, toggle off) -- otherwise a user couldn't tell
// the two inert reasons apart.
func TestWorktreeField_NonGitPlaceholdersAreDistinct(t *testing.T) {
	w := NewWorktreeField(theme.Default())

	w.SetGitTarget(false)
	nonGitBranch := ansi.Strip(w.BranchSection().View(60))
	nonGitBase := ansi.Strip(w.BaseSection().View(60))

	w.SetGitTarget(true) // git repo, but still off
	offBranch := ansi.Strip(w.BranchSection().View(60))
	offBase := ansi.Strip(w.BaseSection().View(60))

	if nonGitBranch == offBranch {
		t.Errorf("branch row placeholder is identical for \"non-git\" and \"off\": %q", nonGitBranch)
	}
	if nonGitBase == offBase {
		t.Errorf("base row placeholder is identical for \"non-git\" and \"off\": %q", nonGitBase)
	}
}

func TestWorktreeField_HeightIsConstantAcrossStates(t *testing.T) {
	w := NewWorktreeField(theme.Default())
	chipsBase := w.ChipsSection().Height(24)
	branchBase := w.BranchSection().Height(24)
	baseBase := w.BaseSection().Height(24)

	w.SetGitTarget(true)
	turnOn(w)
	w.SetBranch("some/branch", false)
	w.SetBaseItems(1, []string{"main", "develop", "release"})
	w.SetBaseStatus("searching…")

	if got := w.ChipsSection().Height(24); got != chipsBase {
		t.Errorf("ChipsSection().Height(24) changed: got %d, want %d", got, chipsBase)
	}
	if got := w.BranchSection().Height(24); got != branchBase {
		t.Errorf("BranchSection().Height(24) changed: got %d, want %d", got, branchBase)
	}
	if got := w.BaseSection().Height(24); got != baseBase {
		t.Errorf("BaseSection().Height(24) changed: got %d, want %d", got, baseBase)
	}

	if got := strings.Count(w.ChipsSection().View(60), "\n") + 1; got != chipsBase {
		t.Errorf("ChipsSection().View(60) rendered %d lines, want %d", got, chipsBase)
	}
	if got := strings.Count(w.BranchSection().View(60), "\n") + 1; got != branchBase {
		t.Errorf("BranchSection().View(60) rendered %d lines, want %d", got, branchBase)
	}
	if got := strings.Count(w.BaseSection().View(60), "\n") + 1; got != baseBase {
		t.Errorf("BaseSection().View(60) rendered %d lines, want %d", got, baseBase)
	}
}

func TestWorktreeField_NoPanicOnDegenerateInputs(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("WorktreeField panicked: %v", r)
		}
	}()
	w := NewWorktreeField(theme.Default())
	_ = w.ChipsSection().View(0)
	_ = w.BranchSection().View(-2)
	_ = w.BaseSection().View(0)
	w.SetBaseItems(1, nil)
	w.SetBranch("", true)
}
