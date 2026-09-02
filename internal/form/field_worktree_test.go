package form

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

func TestWorktreeField_SectionID(t *testing.T) {
	w := NewWorktreeField(theme.Default())
	if got := w.ID(); got != "worktree" {
		t.Errorf("ID() = %q, want %q", got, "worktree")
	}
	// The collapse kept this ID, which is what keeps keys.go's
	// ZoneWorktree mapping and every "chip:worktree:<id>" zone working;
	// "branch" and "base" are gone from form.go's zoneKindByID with the
	// sections that carried them.
	if _, ok := zoneKindByID["branch"]; ok {
		t.Errorf("zoneKindByID still maps %q; no Section carries that ID any more", "branch")
	}
	if _, ok := zoneKindByID["base"]; ok {
		t.Errorf("zoneKindByID still maps %q; no Section carries that ID any more", "base")
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
}

// TestWorktreeField_GitTargetGatesEverything pins SetGitTarget's own
// contract: a non-git target makes the whole field present-but-inert
// regardless of the on/off toggle, and the sub-focus cursor cannot park
// on a part that has nothing to configure.
func TestWorktreeField_GitTargetGatesEverything(t *testing.T) {
	w := NewWorktreeField(theme.Default())
	w.SetGitTarget(true)

	if !w.Enabled() {
		t.Errorf("Enabled() = false after SetGitTarget(true), want true")
	}
	// Worktree is still off -- only the toggle part is reachable.
	if got := w.maxPart(); got != partChips {
		t.Errorf("maxPart() while off = %v, want partChips", got)
	}

	turnOn(w)
	if got := w.maxPart(); got != partBase {
		t.Errorf("maxPart() once on and git = %v, want partBase", got)
	}

	// Flipping back to a non-git target must strand nothing: the cursor
	// comes back to the chips even though the toggle still reads "on".
	w.Update(key(tea.KeyDown, 0)) // chips -> branch
	if w.part != partBranch {
		t.Fatalf("setup: part = %v, want partBranch", w.part)
	}
	w.SetGitTarget(false)
	if w.part != partChips {
		t.Errorf("part = %v after the target stopped being a repository, want partChips", w.part)
	}
}

// turnOn toggles the chips row from its default "off" to "on" via a
// single Right arrow press, through the real Section.Update path.
func turnOn(w *WorktreeField) {
	w.Update(key(tea.KeyRight, 0))
}

func TestWorktreeField_ChipsToggleOnOff(t *testing.T) {
	w := NewWorktreeField(theme.Default())
	w.SetGitTarget(true)

	turnOn(w)
	if !w.On() {
		t.Fatalf("On() = false after one Right, want true")
	}

	w.Update(key(tea.KeyLeft, 0))
	if w.On() {
		t.Fatalf("On() = true after Left back, want false")
	}
}

// TestWorktreeField_SubFocusGrammar is the whole reason the three v1
// sections could become one (v2 spec §6): ↑↓ mean "move the part" away
// from the base list and "move the list" on it, with the top row handing
// the part cursor back -- the same handoff AgentField's expanded list
// already used, which is the precedent that answered the "↑↓ would mean
// two things" objection.
func TestWorktreeField_SubFocusGrammar(t *testing.T) {
	w := NewWorktreeField(theme.Default())
	w.SetGitTarget(true)
	w.SetOn(true)
	w.SetBaseItems(1, []string{"main", "release/1.4"})
	w.Focus()

	if w.part != partChips {
		t.Fatalf("Focus() left the part cursor at %v, want partChips", w.part)
	}

	// Down walks the parts, clamping at the last one.
	w.Update(key(tea.KeyDown, 0))
	if w.part != partBranch {
		t.Fatalf("part after one Down = %v, want partBranch", w.part)
	}
	w.Update(key(tea.KeyDown, 0))
	if w.part != partBase {
		t.Fatalf("part after two Downs = %v, want partBase", w.part)
	}

	// On the base list, Down is the picker's own cursor and the part
	// cursor stays put.
	w.Update(key(tea.KeyDown, 0))
	if w.part != partBase {
		t.Fatalf("part after a Down on the base list = %v, want it to stay on partBase", w.part)
	}
	if got := w.Base(); got != "main" {
		t.Fatalf("Base() after a Down on the base list = %q, want %q", got, "main")
	}
	w.Update(key(tea.KeyDown, 0))
	if got := w.Base(); got != "release/1.4" {
		t.Fatalf("Base() after a second Down = %q, want %q", got, "release/1.4")
	}

	// Up walks back UP the list first...
	w.Update(key(tea.KeyUp, 0))
	if got, part := w.Base(), w.part; got != "main" || part != partBase {
		t.Fatalf("after Up: Base() = %q part = %v, want %q / partBase", got, part, "main")
	}
	w.Update(key(tea.KeyUp, 0))
	if got, part := w.Base(), w.part; got != "" || part != partBase {
		t.Fatalf("after a second Up: Base() = %q part = %v, want HEAD / partBase", got, part)
	}
	// ...and only THEN hands the part cursor back to the branch.
	w.Update(key(tea.KeyUp, 0))
	if w.part != partBranch {
		t.Fatalf("Up at the top of the base list left part = %v, want the handoff back to partBranch", w.part)
	}
	if got := w.Base(); got != "" {
		t.Fatalf("the handoff also moved the base selection to %q, want it left on HEAD", got)
	}

	// Up again is a plain part move; the top clamps.
	w.Update(key(tea.KeyUp, 0))
	if w.part != partChips {
		t.Fatalf("part after Up from the branch = %v, want partChips", w.part)
	}
	w.Update(key(tea.KeyUp, 0))
	if w.part != partChips {
		t.Fatalf("part after Up from the chips = %v, want it clamped at partChips", w.part)
	}
}

// TestWorktreeField_ArrowsDriveTheChipsOnlyFromTheChips pins the other
// half of the grammar: ←→ mean the toggle on the CHIPS part and nowhere
// else -- the text cursor's on the branch, and nothing at all on the
// base, where they used to turn the worktree off and throw away the ref
// the user was in the middle of choosing.
func TestWorktreeField_ArrowsDriveTheChipsOnlyFromTheChips(t *testing.T) {
	w := NewWorktreeField(theme.Default())
	w.SetGitTarget(true)
	w.SetOn(true)
	w.SetBranch("zvi/keep-me", false)
	w.SetBaseItems(1, []string{"main"})
	w.Focus()

	// From the base part, ←→ do nothing: not the toggle, not the cursor.
	w.Update(key(tea.KeyDown, 0))
	w.Update(key(tea.KeyDown, 0))
	if w.part != partBase {
		t.Fatalf("setup: part = %v, want partBase", w.part)
	}
	w.Update(key(tea.KeyDown, 0)) // off the HEAD sentinel, onto "main"
	if got := w.Base(); got != "main" {
		t.Fatalf("setup: Base() = %q, want the base cursor moved off HEAD", got)
	}
	w.Update(key(tea.KeyRight, 0))
	if !w.On() {
		t.Errorf("Right from the base part turned the worktree OFF; it must be inert there")
	}
	if got := w.Base(); got != "main" {
		t.Errorf("Base() after Right on the base part = %q, want the choice untouched", got)
	}
	w.Update(key(tea.KeyLeft, 0))
	if !w.On() || w.part != partBase {
		t.Errorf("Left from the base part moved something: On() = %v, part = %v", w.On(), w.part)
	}

	// On the branch part they are the text cursor's, not the toggle's.
	w.Update(key(tea.KeyUp, 0)) // -> branch (the base cursor is off its top row)
	w.Update(key(tea.KeyUp, 0))
	if w.part != partBranch {
		t.Fatalf("setup: part = %v, want partBranch", w.part)
	}
	w.Update(key(tea.KeyLeft, 0))
	w.Update(key(tea.KeyLeft, 0))
	if !w.On() {
		t.Errorf("Left on the branch part flipped the toggle; it belongs to the text cursor there")
	}
	// A real edit still lands, and still marks the field touched.
	w.Update(rn('X'))
	if got := w.Branch(); got != "zvi/keep-X" && !strings.Contains(got, "X") {
		t.Errorf("Branch() after typing on the branch part = %q, want the edit applied", got)
	}

	// And from the chips they still work, both ways.
	w.Update(key(tea.KeyUp, 0))
	if w.part != partChips {
		t.Fatalf("setup: part = %v, want partChips", w.part)
	}
	w.Update(key(tea.KeyLeft, 0))
	if w.On() {
		t.Errorf("Left on the chips part did not reach the on/off toggle")
	}
	// ...and turning it off strands nothing.
	if w.part != partChips {
		t.Errorf("part after the toggle went off = %v, want it clamped to partChips", w.part)
	}
	w.Update(key(tea.KeyRight, 0))
	if !w.On() {
		t.Errorf("Right on the chips part did not turn the worktree back on")
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

	// The user types, taking over the field (the field must be focused AND
	// the part cursor on the branch, matching how the form's own ring
	// drives it: an unfocused text input ignores keystrokes).
	focusBranch(w)
	w.Update(rn('-'))
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

// focusBranch puts the field in the state a user typing a branch name is
// in: focused, with the part cursor on the branch input.
func focusBranch(w *WorktreeField) {
	w.Focus()
	w.Update(key(tea.KeyDown, 0))
}

// TestWorktreeField_HardSetOverridesTouched pins SetBranch's seeded=false
// path: an authoritative (non-seeded) set always applies, even after the
// field has been touched, and clears the touched flag so a later seed can
// apply again.
func TestWorktreeField_HardSetOverridesTouched(t *testing.T) {
	w := NewWorktreeField(theme.Default())
	w.SetGitTarget(true)
	turnOn(w)

	focusBranch(w)
	w.Update(rn('x')) // touch it
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

// TestWorktreeField_SetBaseRoundTrip pins the setter #7 needed and could
// not have: a remembered base ref goes in and comes back out of Base(),
// "" still means HEAD, and -- the case that actually happens in
// production -- a ref set BEFORE the async branch list arrives is applied
// when it does, rather than dropped on the floor.
func TestWorktreeField_SetBaseRoundTrip(t *testing.T) {
	w := NewWorktreeField(theme.Default())
	w.SetBaseItems(1, []string{"main", "release/1.4"})

	w.SetBase("release/1.4")
	if got := w.Base(); got != "release/1.4" {
		t.Fatalf("Base() after SetBase(%q) = %q", "release/1.4", got)
	}
	w.SetBase("")
	if got := w.Base(); got != "" {
		t.Fatalf("Base() after SetBase(\"\") = %q, want \"\" (HEAD)", got)
	}

	// The real ordering: the app layer resolves the remembered base off
	// the debounced dir check, one `git for-each-ref` round trip BEFORE
	// the list naming it exists.
	pending := NewWorktreeField(theme.Default())
	pending.SetBase("develop")
	if got := pending.Base(); got != "" {
		t.Fatalf("Base() before the list arrived = %q, want \"\" -- there is nothing to select yet", got)
	}
	pending.SetBaseItems(1, []string{"main", "develop"})
	if got := pending.Base(); got != "develop" {
		t.Fatalf("Base() after the list arrived = %q, want the remembered %q", got, "develop")
	}

	// Once it lands it is forgotten: a later refresh must not re-apply it
	// over a selection the user has since moved.
	pending.SetGitTarget(true)
	pending.SetOn(true)
	focusBase(pending)
	pending.Update(key(tea.KeyUp, 0)) // develop -> main
	if got := pending.Base(); got != "main" {
		t.Fatalf("setup: Base() = %q, want the user's own %q", got, "main")
	}
	pending.SetBaseItems(2, []string{"main", "develop"})
	if got := pending.Base(); got != "main" {
		t.Fatalf("Base() after a later refresh = %q, want the user's %q -- a landed SetBase must not re-apply", got, "main")
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
	w.SetBase("develop")
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
	w.SetOn(true)
	w.SetBaseItems(1, []string{"main", "develop"})
	focusBase(w)

	w.Update(key(tea.KeyDown, 0)) // HEAD -> main
	if got := w.Base(); got != "main" {
		t.Fatalf("Base() after one Down = %q, want %q", got, "main")
	}
	w.Update(key(tea.KeyDown, 0)) // main -> develop
	if got := w.Base(); got != "develop" {
		t.Fatalf("Base() after two Downs = %q, want %q", got, "develop")
	}
	w.Update(key(tea.KeyUp, 0))
	w.Update(key(tea.KeyUp, 0))
	if got := w.Base(); got != "" {
		t.Fatalf("Base() back at the top = %q, want \"\" (HEAD)", got)
	}
}

// focusBase puts the part cursor on the base list, the state in which ↑↓
// drive the picker.
func focusBase(w *WorktreeField) {
	w.Focus()
	w.Update(key(tea.KeyDown, 0))
	w.Update(key(tea.KeyDown, 0))
}

func TestWorktreeField_SetBaseItemsStalenessGate(t *testing.T) {
	w := NewWorktreeField(theme.Default())
	w.SetGitTarget(true)
	w.SetOn(true)
	w.SetBaseItems(2, []string{"fresh"})
	w.SetBaseItems(1, []string{"stale"}) // dropped: older version
	focusBase(w)

	w.Update(key(tea.KeyDown, 0))
	if got := w.Base(); got != "fresh" {
		t.Fatalf("Base() = %q after a stale SetBaseItems call, want the fresher %q to survive", got, "fresh")
	}
}

func TestWorktreeField_SetBaseStatusShown(t *testing.T) {
	w := NewWorktreeField(theme.Default())
	w.SetGitTarget(true)
	turnOn(w)
	w.SetBaseStatus("searching…")

	frame := ansi.Strip(w.Panel(60, w.PanelRows()))
	if !strings.Contains(frame, "searching…") {
		t.Fatalf("Panel = %q, want it to contain the base status", frame)
	}
}

// TestWorktreeField_RowVocabulary pins v2 spec §6's worktree row in each
// of its three states, and the elision order the row promises.
func TestWorktreeField_RowVocabulary(t *testing.T) {
	w := NewWorktreeField(theme.Default())

	if got := rowText(w.Row(60)); got != worktreeNonGitPlaceholder {
		t.Errorf("Row on a non-git target = %q, want %q", got, worktreeNonGitPlaceholder)
	}

	w.SetGitTarget(true)
	if got := rowText(w.Row(60)); got != worktreeOffPlaceholder {
		t.Errorf("Row while off = %q, want %q", got, worktreeOffPlaceholder)
	}

	w.SetOn(true)
	w.SetBranch("zvi/fix-login-redirect-loop", false)
	w.SetHeadBranch("main")
	// The row names the ref this worktree will branch FROM, not the
	// picker row that chose it: HEAD selected plus a checked-out `main`
	// reads `← main`, never `← HEAD (main)`.
	if got, want := rowText(w.Row(60)), "on · zvi/fix-login-redirect-loop ← main"; got != want {
		t.Errorf("Row = %q, want %q", got, want)
	}

	w.SetBaseItems(1, []string{"release/1.4"})
	w.SetBase("release/1.4")
	if got, want := rowText(w.Row(60)), "on · zvi/fix-login-redirect-loop ← release/1.4"; got != want {
		t.Errorf("Row with an explicit base = %q, want %q", got, want)
	}
	w.SetBase("")
	w.SetHeadBranch("")
	if got, want := rowText(w.Row(60)), "on · zvi/fix-login-redirect-loop ← HEAD"; got != want {
		t.Errorf("Row on a detached HEAD = %q, want %q", got, want)
	}
	w.SetHeadBranch("main")
	w.SetBase("")

	// The base gives up cells first...
	if got := rowText(w.Row(40)); !strings.Contains(got, "zvi/fix-login-redirect-loop") {
		t.Errorf("Row at 40 cells = %q, want the branch intact and the base elided", got)
	}
	// ...then the whole clause goes, rather than showing a stub...
	if got := rowText(w.Row(34)); strings.Contains(got, "←") {
		t.Errorf("Row at 34 cells = %q, want the base clause dropped entirely", got)
	}
	// ...and only then does the branch itself elide.
	if got := rowText(w.Row(20)); !strings.HasSuffix(got, rowEllipsis) {
		t.Errorf("Row at 20 cells = %q, want the branch elided last, marked with %q", got, rowEllipsis)
	}
}

// TestWorktreeField_NonGitPlaceholdersAreDistinct pins the brief's own
// "inert w/ distinct placeholders" wording: the panel must show DIFFERENT
// text for "not a git repository" than for merely "off" (git repo, toggle
// off) -- otherwise a user could not tell the two inert reasons apart.
func TestWorktreeField_NonGitPlaceholdersAreDistinct(t *testing.T) {
	w := NewWorktreeField(theme.Default())

	w.SetGitTarget(false)
	nonGit := ansi.Strip(w.Panel(60, w.PanelRows()))

	w.SetGitTarget(true) // git repo, but still off
	off := ansi.Strip(w.Panel(60, w.PanelRows()))

	if nonGit == off {
		t.Errorf("panel is identical for \"non-git\" and \"off\": %q", nonGit)
	}
	if !strings.Contains(nonGit, worktreeNonGitPlaceholder) {
		t.Errorf("non-git panel = %q, want it to name the reason", nonGit)
	}
	if !strings.Contains(off, worktreeOffPlaceholder) {
		t.Errorf("off panel = %q, want it to name the reason", off)
	}

	// Each reason is named ONCE, by the chips line that owns it. The
	// branch and base parts below carry an em dash instead: repeating a
	// six-word reason down three consecutive lines said nothing the
	// first line had not, and "branch  off" reads as a verb phrase
	// before it reads as a value.
	for name, panel := range map[string]string{"non-git": nonGit, "off": off} {
		lines := strings.Split(panel, "\n")
		if len(lines) < 3 {
			t.Fatalf("%s panel has %d lines, want the three parts", name, len(lines))
		}
		for i, part := range []string{"branch", "base"} {
			line := lines[i+1]
			if !strings.Contains(line, rowValueNone) {
				t.Errorf("%s panel's %s part = %q, want %q", name, part, line, rowValueNone)
			}
		}
	}
}

// TestWorktreeField_FooterRungsFollowThePart pins v2 spec §3 rule 4 for
// the one field whose keys mean different things in different parts: the
// footer must not promise "←→ toggle" while the user is typing a branch
// name, and must not promise "type to edit" anywhere else.
func TestWorktreeField_FooterRungsFollowThePart(t *testing.T) {
	w := NewWorktreeField(theme.Default())
	w.SetGitTarget(true)
	w.SetOn(true)
	w.SetBaseItems(1, []string{"main"})
	w.Focus()

	widest := func() string { return w.FooterRungs()[0] }

	if got := widest(); !strings.Contains(got, "←→") {
		t.Errorf("rung on the chips part = %q, want it to teach the toggle", got)
	}
	w.Update(key(tea.KeyDown, 0))
	if got := widest(); !strings.Contains(got, "type to edit") || strings.Contains(got, "←→") {
		t.Errorf("rung on the branch part = %q, want it to teach typing and NOT the toggle", got)
	}
	// On the base part the toggle is gone too: ←→ are a no-op there
	// (Update), because they used to discard the ref being chosen.
	w.Update(key(tea.KeyDown, 0))
	atTop := widest()
	if !strings.Contains(atTop, "pick a base") || strings.Contains(atTop, "←→") {
		t.Errorf("rung on the base part = %q, want it to teach the list and NOT the toggle", atTop)
	}
	// At the top of the list ↑ leaves the part; below it ↑ moves the
	// list. One wording cannot be true in both places, so it isn't one.
	if !strings.Contains(atTop, "↑ back to the branch") {
		t.Errorf("rung at the top of the base list = %q, want the way back out", atTop)
	}
	w.Update(key(tea.KeyDown, 0))
	if got := widest(); strings.Contains(got, "back to the branch") || !strings.Contains(got, "↑↓ pick a base") {
		t.Errorf("rung below the top of the base list = %q, want ↑↓ to mean the list", got)
	}

	// A non-git target must not be promised keys that do nothing -- which
	// is exactly what footer.go's own ZoneWorktree table would have said.
	nonGit := NewWorktreeField(theme.Default())
	if got := nonGit.FooterRungs()[0]; strings.ContainsAny(got, "←→↑↓") {
		t.Errorf("rung on a non-git target = %q, want no arrow key promised: none of them do anything here", got)
	}
}

func TestWorktreeField_NoPanicOnDegenerateInputs(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("WorktreeField panicked: %v", r)
		}
	}()
	w := NewWorktreeField(theme.Default())
	_ = w.Row(0)
	_ = w.Row(-2)
	_ = w.Panel(0, 0)
	_ = w.Panel(-2, 1)
	_ = w.Panel(4, 20)
	w.SetBaseItems(1, nil)
	w.SetBranch("", true)
	w.SetBase("nothing-like-this")
}

// TestWorktreeField_HeadRowNamesTheCurrentBranch pins spec §6 field 4's
// own "row 0 `HEAD (<current branch>)`" (minor M4): the row read a bare
// "HEAD" because nothing ever supplied the branch -- gitx.CurrentBranch
// existed and was tested, with no caller anywhere.
func TestWorktreeField_HeadRowNamesTheCurrentBranch(t *testing.T) {
	w := NewWorktreeField(theme.Default())
	w.SetGitTarget(true)
	w.SetOn(true)
	w.SetBaseItems(1, []string{"release/1.4"})

	if frame := ansi.Strip(w.Panel(60, w.PanelRows())); strings.Contains(frame, "HEAD (") {
		t.Fatalf("base panel = %q, want a bare HEAD before any branch is supplied", frame)
	}

	w.SetHeadBranch("main")
	if got := w.HeadBranch(); got != "main" {
		t.Errorf("HeadBranch() = %q, want %q -- the app layer reads it back for the header", got, "main")
	}

	frame := ansi.Strip(w.Panel(60, w.PanelRows()))
	if !strings.Contains(frame, "HEAD (main)") {
		t.Errorf("base panel = %q, want the HEAD row to name the current branch", frame)
	}
	// And it names it EXACTLY ONCE: the base part line yields to the
	// list below it rather than printing the same six cells one line
	// apart (see panelBase).
	if n := strings.Count(frame, "HEAD (main)"); n != 1 {
		t.Errorf("base panel names HEAD (main) %d times:\n%s", n, frame)
	}
	if part := strings.Split(frame, "\n")[2]; strings.Contains(part, "HEAD") {
		t.Errorf("base part = %q, want it left to the list's own cursor row", part)
	}
	// With no room for a list, the part line is the only place the base
	// can appear -- so there it says it.
	short := ansi.Strip(w.Panel(60, worktreePanelParts))
	if part := strings.Split(short, "\n")[2]; !strings.Contains(part, "HEAD (main)") {
		t.Errorf("base part with no list rows = %q, want the selection named there", part)
	}
	// The sentinel's own public contract is unchanged: HEAD still means "".
	if got := w.Base(); got != "" {
		t.Errorf("Base() = %q, want \"\" -- the HEAD row's label must not leak into the getter", got)
	}
}

// TestWorktreeField_HeadRowKeepsTheUsersSelection pins that supplying the
// branch name later (the base list arrives asynchronously) does not yank
// the cursor back to HEAD from a ref the user already picked.
func TestWorktreeField_HeadRowKeepsTheUsersSelection(t *testing.T) {
	w := NewWorktreeField(theme.Default())
	w.SetGitTarget(true)
	w.SetOn(true)
	w.SetBaseItems(1, []string{"release/1.4"})
	focusBase(w)
	w.Update(key(tea.KeyDown, 0)) // HEAD -> release/1.4

	if got := w.Base(); got != "release/1.4" {
		t.Fatalf("Base() = %q, want the user's own selection before the head branch lands", got)
	}
	w.SetHeadBranch("main")
	if got := w.Base(); got != "release/1.4" {
		t.Fatalf("Base() = %q after SetHeadBranch, want the user's selection preserved", got)
	}
}
