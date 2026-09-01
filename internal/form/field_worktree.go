// field_worktree.go's base-ref picker is an INDEPENDENT IMPLEMENTATION,
// NOT derived from atrium (github.com/ZviBaratz/atrium). Atrium's own
// worktree-branch picker lives in ui/overlay/branchPicker.go, which is
// AGPL-encumbered and was explicitly never opened for this task (per the
// task-17 brief's own provenance guardrail). This file's base picker is
// built entirely on widgets.Picker (Task 14, itself ported from Atrium's
// unencumbered ui/overlay/picker.go mixin -- a DIFFERENT, clean-listed
// file), per spec §6.4, with no reference to branchPicker.go's own shape,
// naming, or behavior. The chip row (on/off) and branch text input are
// likewise independent implementations, built on widgets.ChipRow (Task
// 14) and this package's own lineInput (lineinput.go) respectively.
//
// Section shape: spec §6 field 4 ("Worktree") is really three focus
// stops -- the on/off toggle (ZoneWorktree), the branch text field
// (ZoneBranch), and the base-ref picker (ZoneBase), each independently
// tabbable per keys.go's own zoneKindByID map -- so WorktreeField is not
// itself a Section; it is the shared model behind three small unexported
// Section adapters (worktreeChipsSection/worktreeBranchSection/
// worktreeBaseSection), each holding a pointer back to one WorktreeField
// and exposing the matching ID(). The app layer inserts
// w.ChipsSection()/w.BranchSection()/w.BaseSection() into Setup.Sections
// at spec §6 field 4's position, in that order.
package form

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/ZviBaratz/herdr-draft/internal/form/widgets"
	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// basePickerRows is the base-ref picker's fixed candidate-row count
// (spec §6 field 4's own constant-height contract). Kept as its own named
// constant, distinct from field_dir.go's dirPickerRows, even though the
// value happens to match today -- the two pickers serve different fields
// and have no reason to be coupled to the same number going forward.
const basePickerRows = 4

const (
	worktreeLabel = "Branch: "
	baseLabel     = "Base: "

	// worktreeNonGitPlaceholder and worktreeOffPlaceholder are the "distinct
	// placeholders" the brief calls for: a non-git target and a
	// git-repo-but-toggled-off target are both "inert," but for different
	// reasons, and a user should be able to tell which from the text
	// alone.
	worktreeNonGitPlaceholder = "not a git repository"
	worktreeOffPlaceholder    = "off"

	// baseHeadID is the base picker's internal sentinel PickerItem.ID for
	// "use HEAD" (Base()'s own "" == HEAD contract). It must be a
	// non-empty string: widgets.Picker's own carried fact (Task 14) is
	// that callers must supply unique, non-empty IDs -- its ID-preserving
	// cursor (SetItems' same-version-refresh behavior) is a first-match
	// -wins lookup, and an empty ID here previously coincided with the
	// zero value Selected() falls back to on a miss, which is exactly the
	// ambiguity the carried fact warns against (fix, review round 1:
	// "Base-picker sentinel violates the unique-non-empty-ID invariant").
	// "HEAD" itself can never collide with a real caller-supplied ref:
	// git refuses to let a branch or tag be named "HEAD" (it's a reserved
	// symbolic-ref name), so this doubles as an always-safe sentinel
	// rather than an arbitrary unlikely-to-collide string. Base()
	// translates it back to "" at the getter boundary, so the public
	// "" == HEAD contract is unchanged.
	baseHeadID = "HEAD"
)

var worktreeChips = []widgets.Chip{
	{ID: "off", Label: "Off"},
	{ID: "on", Label: "On"},
}

// WorktreeField is the shared model behind spec §6 field 4's three focus
// stops (see the file doc comment): the on/off toggle, the branch text
// input, and the base-ref picker. It exposes the setter API the app layer
// drives (SetGitTarget/SetBranch/SetBaseItems/SetBaseStatus) and the
// getters the submit pipeline reads (On/Branch/Base), plus Enabled(),
// which is NOT a Section method -- it answers "is this git target usable
// for a worktree at all," consulted by every one of the three adapters'
// own Section.Enabled() gating.
type WorktreeField struct {
	palette theme.Palette

	chips  *widgets.ChipRow
	branch *lineInput
	base   *widgets.Picker

	branchTouched bool

	isGitRepo bool

	haveBaseVersion   bool
	baseVersion       int
	baseRefs          []string
	basePickerVersion int
	baseStatus        string
}

// NewWorktreeField returns a WorktreeField defaulting to off, inert (no
// git target confirmed yet -- see Enabled's own doc comment on why the
// safe default is "not usable until told otherwise"), styled from
// palette.
func NewWorktreeField(palette theme.Palette) *WorktreeField {
	w := &WorktreeField{
		palette: palette,
		chips:   widgets.NewChipRow(palette),
		branch:  newLineInput(palette, 0),
		base:    widgets.NewPicker(palette),
	}
	w.chips.SetChips(worktreeChips)
	w.chips.SetInert(true, worktreeNonGitPlaceholder) // safe default: see SetGitTarget
	w.branch.SetPlaceholder("branch name")
	w.refreshBaseItems(true) // seed the HEAD sentinel row
	return w
}

// ChipsSection returns the Section for spec §6 field 4's on/off toggle
// (ID "worktree").
func (w *WorktreeField) ChipsSection() Section { return worktreeChipsSection{w} }

// BranchSection returns the Section for the branch text input (ID
// "branch").
func (w *WorktreeField) BranchSection() Section { return worktreeBranchSection{w} }

// BaseSection returns the Section for the base-ref picker (ID "base").
func (w *WorktreeField) BaseSection() Section { return worktreeBaseSection{w} }

// Enabled reports whether the current directory target can host a
// worktree at all -- false both before the app layer's first
// SetGitTarget call (a deliberately conservative default: nothing here
// performs its own I/O to find out) and whenever the target isn't a git
// repository. It is NOT a Section method; every one of the three adapters'
// own Section.Enabled() consults it (see their doc comments).
func (w *WorktreeField) Enabled() bool { return w.isGitRepo }

// On reports whether the chip row is currently toggled to "on".
func (w *WorktreeField) On() bool { return w.chips.Selected().ID == "on" }

// SetOn sets the chip row's on/off toggle to on, e.g. to apply spec §6
// field 4's "default from config" (config.Config.DefaultWorktree, or a
// state.State.LastWorktree override) at form construction -- worktreeChips
// has exactly two entries (off, on), so a single ChipRow.Next() call always
// toggles between them regardless of direction; a call that already matches
// the current state is a no-op.
//
// Added in Task 20 (the app layer): unlike SetBranch/SetBaseItems/
// SetBaseStatus, this field previously had no way to set its initial
// toggle programmatically at all (only the user's own Left/Right/Up/Down
// through worktreeChipsSection.Update could move it) -- see
// field_title.go's SetTitle doc comment for the fuller writeup of this
// class of gap.
func (w *WorktreeField) SetOn(on bool) {
	if w.On() != on {
		w.chips.Next()
	}
}

// SetGitTarget records whether the currently selected project directory
// is a git repository, gating every part of this field: the chip row
// itself goes inert (worktreeNonGitPlaceholder) when isRepo is false, and
// -- regardless of the on/off toggle's own position -- so do the branch
// and base zones (see worktreeBranchSection/worktreeBaseSection's own
// Enabled()).
func (w *WorktreeField) SetGitTarget(isRepo bool) {
	w.isGitRepo = isRepo
	w.chips.SetInert(!isRepo, worktreeNonGitPlaceholder)
}

// Branch returns the branch text input's current value.
func (w *WorktreeField) Branch() string { return w.branch.Value() }

// SetBranch sets the branch text input's value, honoring the
// touched-vs-preselected rule: when seeded is true, this is a
// SUGGESTION (e.g. derived from a chosen Linear issue's slug) that is
// applied only if the user has not yet typed into the field themselves
// (branchTouched == false) -- once touched, every further seeded call is
// silently ignored, so a later re-suggestion (e.g. a debounced re-fetch)
// never clobbers the user's own edit. seeded == false is a hard,
// authoritative set (e.g. the app layer's own Ctrl+R Ctrl+R rebuild,
// though in practice that reconstructs a fresh WorktreeField instead) that
// always applies and clears branchTouched, so a subsequent seed can apply
// again.
func (w *WorktreeField) SetBranch(v string, seeded bool) {
	if seeded && w.branchTouched {
		return
	}
	w.branch.SetValue(v)
	if !seeded {
		w.branchTouched = false
	}
}

// SetBaseItems replaces the base-ref candidate pool (branch/tag names the
// app layer has resolved for the current project), tagged with a
// caller-assigned monotonic version -- the same staleness guard
// field_dir.go's SetCandidates documents, for the same reason. A synthetic
// "HEAD" row (ID baseHeadID, a non-empty sentinel -- see its own doc
// comment) is always kept first, ahead of every caller-supplied ref --
// see Base()'s own doc comment ("" == HEAD).
func (w *WorktreeField) SetBaseItems(version int, refs []string) {
	if w.haveBaseVersion && version < w.baseVersion {
		return
	}
	w.haveBaseVersion = true
	w.baseVersion = version
	w.baseRefs = append([]string(nil), refs...)
	w.refreshBaseItems(false)
}

// refreshBaseItems rebuilds the base picker's item list from baseRefs
// (deduped, "HEAD" sentinel first) and feeds it to the wrapped Picker,
// bumping basePickerVersion first when bump is true -- the same
// bump-only-on-a-real-change discipline field_dir.go's refreshItems
// documents, so a same-context SetBaseItems refresh preserves the current
// selection by ref name (widgets.Picker.SetItems' own same-version
// contract) rather than resetting the cursor to HEAD every time.
func (w *WorktreeField) refreshBaseItems(bump bool) {
	if bump {
		w.basePickerVersion++
	}
	items := make([]widgets.PickerItem, 0, len(w.baseRefs)+1)
	items = append(items, widgets.PickerItem{ID: baseHeadID, Label: "HEAD"})
	seen := map[string]bool{baseHeadID: true}
	for _, r := range w.baseRefs {
		if r == "" || seen[r] {
			continue
		}
		seen[r] = true
		items = append(items, widgets.PickerItem{ID: r, Label: r})
	}
	w.base.SetItems(w.basePickerVersion, items)
}

// SetBaseStatus sets the status text shown alongside the base row (e.g.
// "searching…" while an async ref list is loading, or "couldn't list" on
// failure) -- "" hides it.
func (w *WorktreeField) SetBaseStatus(s string) { w.baseStatus = s }

// Base returns the currently selected base ref, or "" to mean HEAD (the
// always-first sentinel row -- see refreshBaseItems). The sentinel's own
// PickerItem.ID is the non-empty baseHeadID (see its own doc comment for
// why it can't be "" internally); this getter is the one place that
// translates it back to "", so the public "" == HEAD contract holds
// regardless of the internal ID's own value.
func (w *WorktreeField) Base() string {
	sel, ok := w.base.Selected()
	if !ok || sel.ID == baseHeadID {
		return ""
	}
	return sel.ID
}

// worktreeChipsSection is the Section adapter for the on/off toggle (ID
// "worktree").
type worktreeChipsSection struct{ f *WorktreeField }

func (s worktreeChipsSection) ID() string { return "worktree" }

// Enabled mirrors WorktreeField.Enabled() -- the chip row itself has
// nothing to toggle for a target that can't host a worktree at all.
func (s worktreeChipsSection) Enabled() bool { return s.f.Enabled() }

func (s worktreeChipsSection) Focus() tea.Cmd { return nil }
func (s worktreeChipsSection) Blur()          {}

// Update moves the chip cursor on Left/Up (Prev) or Right/Down (Next) --
// both are no-ops while inert (widgets.ChipRow's own contract).
func (s worktreeChipsSection) Update(msg tea.Msg) tea.Cmd {
	km, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	switch km.String() {
	case "left", "up":
		s.f.chips.Prev()
	case "right", "down":
		s.f.chips.Next()
	}
	return nil
}

func (s worktreeChipsSection) Height(int) int { return 2 }

// View renders the chip row, padding to exactly two physical lines when
// ChipRow.View itself only produced one -- the same reserved-hint-line
// pattern field_placement.go's PlacementField.View uses, for the same
// hint-independent-Height reason.
func (s worktreeChipsSection) View(inner int) string {
	if inner < 1 {
		inner = 1
	}
	v := s.f.chips.View(inner)
	if !strings.Contains(v, "\n") {
		v += "\n" + fitLine("", inner)
	}
	return v
}

// worktreeBranchSection is the Section adapter for the branch text input
// (ID "branch").
type worktreeBranchSection struct{ f *WorktreeField }

func (s worktreeBranchSection) ID() string { return "branch" }

// Enabled requires BOTH a usable git target and the toggle being on --
// present-but-inert (form.go's Section doc comment) whenever either
// condition fails, with renderBranch (below) choosing which of the two
// distinct placeholders to show.
func (s worktreeBranchSection) Enabled() bool { return s.f.isGitRepo && s.f.On() }

func (s worktreeBranchSection) Focus() tea.Cmd { return s.f.branch.Focus() }
func (s worktreeBranchSection) Blur()          { s.f.branch.Blur() }

// Update forwards to the branch lineInput, setting branchTouched only
// when the forwarded message actually changed the value -- the same
// before/after Value() comparison field_dir.go's DirField.Update and
// field_title.go's TitleField.Update use, so a non-edit message (e.g. a
// cursor-blink tick) never spuriously marks the field touched.
func (s worktreeBranchSection) Update(msg tea.Msg) tea.Cmd {
	before := s.f.branch.Value()
	cmd := s.f.branch.Update(msg)
	if s.f.branch.Value() != before {
		s.f.branchTouched = true
	}
	return cmd
}

func (s worktreeBranchSection) Height(int) int { return 2 }

func (s worktreeBranchSection) View(inner int) string {
	return s.f.renderBranch(inner)
}

// renderBranch renders the branch row at exactly two physical lines: the
// label/text (or, while inert, one of the two distinct placeholders), then
// an always-reserved second line.
func (w *WorktreeField) renderBranch(inner int) string {
	if inner < 1 {
		inner = 1
	}
	labelStyled := lipgloss.NewStyle().Foreground(w.palette.Text).Render(worktreeLabel)
	budget := inner - lipgloss.Width(worktreeLabel)
	if budget < 1 {
		budget = 1
	}

	var body string
	switch {
	case !w.isGitRepo:
		body = fitLine(dimHint(w.palette).Render(worktreeNonGitPlaceholder), budget)
	case !w.On():
		body = fitLine(dimHint(w.palette).Render(worktreeOffPlaceholder), budget)
	default:
		body = w.branch.View(budget)
	}

	header := fitLine(labelStyled+body, inner)
	return header + "\n" + fitLine("", inner)
}

// worktreeBaseSection is the Section adapter for the base-ref picker (ID
// "base"). It deliberately does NOT implement form.go's completer
// capability: unlike DirField's shell-style path completion, a base-ref
// picker has nothing to "complete" (spec §6 asks for none) -- MapKey's own
// documented fallback for exactly this case ("a zone whose widget has
// nothing to complete should treat this the same as ActionAdvance")
// already produces the right behavior without this type needing to
// implement Complete() bool returning false itself.
type worktreeBaseSection struct{ f *WorktreeField }

func (s worktreeBaseSection) ID() string { return "base" }

// Enabled mirrors worktreeBranchSection.Enabled() -- the base picker is
// equally meaningless without both a usable git target and the toggle
// being on.
func (s worktreeBaseSection) Enabled() bool { return s.f.isGitRepo && s.f.On() }

func (s worktreeBaseSection) Focus() tea.Cmd { return nil }
func (s worktreeBaseSection) Blur()          {}

// Update moves the base picker's cursor on Up/Down.
func (s worktreeBaseSection) Update(msg tea.Msg) tea.Cmd {
	km, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	switch km.String() {
	case "up":
		s.f.base.CursorPrev()
	case "down":
		s.f.base.CursorNext()
	}
	return nil
}

func (s worktreeBaseSection) Height(int) int { return 2 + basePickerRows }

func (s worktreeBaseSection) View(inner int) string {
	return s.f.renderBase(inner)
}

// renderBase renders the base row at exactly 2+basePickerRows physical
// lines: the label/selection (or, while inert, one of the two distinct
// placeholders) with SetBaseStatus's own status text appended inline, an
// always-reserved second line, then basePickerRows candidate rows (always
// shown, unlike field_dir.go's DirField -- a short ref list is small
// enough that hiding it while unfocused buys nothing, a deliberate
// simplification over mechanically mirroring DirField's own
// focus-gated-rows convention).
func (w *WorktreeField) renderBase(inner int) string {
	if inner < 1 {
		inner = 1
	}
	labelStyled := lipgloss.NewStyle().Foreground(w.palette.Text).Render(baseLabel)

	if !w.isGitRepo || !w.On() {
		placeholder := worktreeOffPlaceholder
		if !w.isGitRepo {
			placeholder = worktreeNonGitPlaceholder
		}
		budget := inner - lipgloss.Width(baseLabel)
		if budget < 1 {
			budget = 1
		}
		header := fitLine(labelStyled+fitLine(dimHint(w.palette).Render(placeholder), budget), inner)
		blanks := make([]string, basePickerRows)
		for i := range blanks {
			blanks[i] = fitLine("", inner)
		}
		return header + "\n" + fitLine("", inner) + "\n" + strings.Join(blanks, "\n")
	}

	status := ""
	if w.baseStatus != "" {
		status = "  " + dimHint(w.palette).Render(w.baseStatus)
	}
	display := "HEAD"
	if sel := w.Base(); sel != "" {
		display = sel
	}
	budget := inner - lipgloss.Width(baseLabel) - lipgloss.Width(status)
	if budget < 1 {
		budget = 1
	}
	body := fitLine(lipgloss.NewStyle().Foreground(w.palette.Text).Render(display), budget)
	header := fitLine(labelStyled+body+status, inner)

	rows := w.base.View(inner, basePickerRows)
	return header + "\n" + fitLine("", inner) + "\n" + rows
}
