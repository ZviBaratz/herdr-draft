// field_worktree.go's base-ref picker is an INDEPENDENT IMPLEMENTATION,
// NOT derived from atrium (github.com/ZviBaratz/atrium). Atrium's own
// worktree-branch picker lives in ui/overlay/branchPicker.go, which is
// AGPL-encumbered and was explicitly never opened for this task (per the
// task-17 brief's own provenance guardrail). This file's base picker is
// built entirely on widgets.Picker (Task 14, itself ported from Atrium's
// unencumbered ui/overlay/picker.go mixin -- a DIFFERENT, clean-listed
// file), per v2 spec §6's worktree row ("a three-part editor", v1 spec
// §6 field 4 before it), with no reference to branchPicker.go's own
// shape, naming, or behavior. The chip row (on/off) and branch text
// input are likewise independent implementations, built on
// widgets.ChipRow (Task 14) and this package's own lineInput
// (lineinput.go) respectively.
//
// Section shape: v1 made spec §6 field 4 THREE focus stops -- the on/off
// toggle (ZoneWorktree), the branch text field (ZoneBranch) and the
// base-ref picker (ZoneBase) -- each an independently tabbable Section
// adapter over one shared WorktreeField. v2 spec §6 collapses all three
// into ONE Section with one row (`on · <branch> ← <base>`) and a
// three-part panel, because three rows spend three of the stack's eight
// lines restating one decision.
//
// The concern that drove the v1 split was a second focus level, where ↑↓
// would have to mean both "move between the toggle, branch and base" and
// "move the base list". That concern is answered by precedent, not by
// avoidance: AgentField already resolves exactly this ambiguity by
// letting ↑ at the top of its list move the outer cursor back
// (field_agent.go's Update). The worktreePart sub-cursor below is the
// same pattern -- away from the base list ↑↓ move the part, clamped; on
// the base list they drive the picker, except that ↑ on its top row
// hands the part cursor back to the branch.
//
// Every public setter and getter kept its v1 signature through the
// collapse, so internal/app's reads and writes are untouched apart from
// the section list itself (which loses two entries). SetBase is the one
// addition: internal/defaults resolves and persists a remembered base per
// project, and until this field had a setter for it that memory was
// written by the app layer and read by nobody.
package form

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/ZviBaratz/herdr-draft/internal/form/widgets"
	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

const (
	// worktreeRowLabel is v2's row label (v2 spec §6's field table). The
	// panel's own first part reuses it, which is what makes the panel
	// read as an expansion of the row rather than a different control.
	worktreeRowLabel = "worktree"
	// worktreeBranchLabel and worktreeBaseLabel are the panel's other two
	// part labels, rendered into the SAME label column as the row stack
	// above (v2 spec §4's mockup).
	worktreeBranchLabel = "branch"
	worktreeBaseLabel   = "base"

	// worktreeNonGitPlaceholder and worktreeOffPlaceholder are the two
	// distinct inert states: a non-git target and a git-repo-but-toggled-
	// off target are both "no worktree", but for different reasons, and a
	// user must be able to tell which from the text alone.
	worktreeNonGitPlaceholder = "not a git repository"
	worktreeOffPlaceholder    = "off"
	// worktreeOnPrefix opens the row's live state (v2 spec §6:
	// `on · <branch> ← <base>`).
	worktreeOnPrefix = "on"
	// worktreeRowSep separates the row's `on` from the branch, and
	// worktreeBaseArrow introduces the base ref it will branch from.
	worktreeRowSep    = " · "
	worktreeBaseArrow = " ← "
	// worktreeFromPrefix is what the row says INSTEAD of a branch name
	// while no branch has been derived yet: `on · from main`. See
	// rowFromBase.
	worktreeFromPrefix = "from "
	// worktreeBranchUnset is the branch editor's PLACEHOLDER -- the
	// lineInput's own prompt, and the dim stand-in the branch part shows
	// while the cursor rests elsewhere. It is deliberately NOT available
	// to Row: a placeholder is an instruction to the person typing, and
	// the row states a value. Leaking it there rendered
	// `on · branch name ← main` -- a row naming a branch called "branch
	// name" -- in the state the form OPENS in, every single time, since an
	// empty title has nothing for the app layer to derive a branch from.
	// Found by running the binary; see rowFromBase for what the row says
	// instead.
	worktreeBranchUnset = "branch name"

	// worktreeMinBaseCells is the fewest cells the row will spend on an
	// elided base ref before dropping the whole `← <base>` clause
	// instead. Below it the clause is all marker and no information.
	worktreeMinBaseCells = 4
	// worktreeBaseIndent indents the base picker's rows past the panel's
	// value column, so the list reads as belonging to the `base` part
	// above it (v2 spec §4's mockup).
	worktreeBaseIndent = 2
	// worktreePanelParts is the panel's fixed head: the chips, the branch
	// and the base selection, one line each, before a single candidate
	// row is drawn.
	worktreePanelParts = 3
	// worktreePanelMaxRows caps PanelRows. A repository can have hundreds
	// of branches; the panel should not claim more of the form than the
	// three parts plus a usable window onto the list.
	worktreePanelMaxRows = 10

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

	// worktreeBaseZonePrefix keeps the base picker's own click zones
	// spelled "row:base:<n>" rather than "row:worktree:<n>". The list is
	// one of three parts inside this field's panel, and naming its rows
	// after the part they belong to is what keeps a zone ID readable on
	// its own.
	worktreeBaseZonePrefix = "row:base:"
)

// worktreeChips are the on/off toggle's two chips. Lowercase, per v2
// spec §3 rule 5 ("copy is plain, lowercase and active") -- v1
// capitalized them only because its chip row was the field's whole
// rendering and read as a heading of its own.
var worktreeChips = []widgets.Chip{
	{ID: "off", Label: worktreeOffPlaceholder},
	{ID: "on", Label: worktreeOnPrefix},
}

// worktreePart is the panel's sub-focus cursor: which of the three parts
// ↑↓ and ←→ currently address. It exists ONLY inside the panel -- the row
// stack knows nothing about it and Row never consults it, which is what
// keeps "row i is always at row i" true while the user moves around
// inside the panel.
type worktreePart int

const (
	partChips worktreePart = iota
	partBranch
	partBase
)

// WorktreeField is v2 spec §6's `worktree` row as one Section (see the
// file doc comment): the on/off toggle, the branch name and the base
// ref, shown as a single consequence row and edited in a three-part
// panel.
type WorktreeField struct {
	palette theme.Palette

	chips  *widgets.ChipRow
	branch *lineInput
	base   *widgets.Picker

	focused bool
	part    worktreePart

	branchTouched bool

	isGitRepo bool

	haveBaseVersion   bool
	baseVersion       int
	baseRefs          []string
	basePickerVersion int
	baseStatus        string

	// pendingBase is SetBase's own deferred selection: the app layer
	// resolves a remembered base ref (internal/defaults) long before the
	// async branch list naming it has landed, so a SelectID that misses is
	// retried on every subsequent list refresh rather than dropped. It is
	// cleared the moment it lands, so a later refresh cannot re-apply it
	// over a selection the user has since moved.
	pendingBase     string
	havePendingBase bool

	// baseRowsShown is how many candidate rows the last Panel render drew.
	// widgets.Picker.SelectAt needs the SAME height MarkedView was called
	// with to map a click back to an item, and unlike v1's fixed
	// basePickerRows the v2 panel's list height varies with the window.
	baseRowsShown int

	// provenance is SetProvenance's config-file name, "" for a panel whose
	// values no config file chose. See SetProvenance.
	provenance string

	// headBranch is the branch currently checked out in the target repo,
	// shown alongside the base picker's HEAD row -- spec §6 field 4's own
	// "row 0 `HEAD (<current branch>)`". "" (a detached HEAD, or before
	// the app layer has resolved one) renders the bare "HEAD" the row
	// always had. See SetHeadBranch.
	headBranch string
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
	w.branch.SetPlaceholder(worktreeBranchUnset)
	// One column of ref names, drawn in herdr's own branch color (v3 spec
	// §8.1's Tone) so the base list matches the branch half of the row it
	// feeds -- Row already renders the branch in Palette.Branch.
	w.base.SetColumns(widgets.PickerColumn{Tone: widgets.ToneBranch})
	w.refreshBaseItems(true) // seed the HEAD sentinel row
	return w
}

// ID identifies this Section for form.go's zoneFor. It stays "worktree",
// v1's chip-row ID, so keys.go's ZoneWorktree mapping and every
// "chip:worktree:<id>" zone survive the collapse unchanged -- what went
// away are the "branch" and "base" IDs, and with them their entries in
// form.go's zoneKindByID.
func (w *WorktreeField) ID() string { return "worktree" }

// Enabled reports whether the current directory target can host a
// worktree at all -- false both before the app layer's first
// SetGitTarget call (a deliberately conservative default: nothing here
// performs its own I/O to find out) and whenever the target isn't a git
// repository. A non-git target makes the WHOLE field a present-but-inert
// row, which is exactly what v2 spec §6's `not a git repository` cell
// says.
func (w *WorktreeField) Enabled() bool { return w.isGitRepo }

// Focus gives the field input focus, parking the part cursor back on the
// chips: arriving at a field should always start at its top, so ↑↓ mean
// the same thing every time the ring lands here.
func (w *WorktreeField) Focus() tea.Cmd {
	w.focused = true
	w.part = partChips
	return w.syncBranchFocus()
}

// Blur removes input focus.
func (w *WorktreeField) Blur() {
	w.focused = false
	w.branch.Blur()
}

// Update implements v2's sub-focus grammar (see the file doc comment):
//
//   - ↑↓ move the part cursor, clamped -- except on the base list, where
//     ↓ is CursorNext and ↑ is CursorPrev, or, at the top row, a handoff
//     back to the branch part.
//   - ←→ drive the on/off chips ONLY from the chips part. They used to
//     reach the toggle from the base part too, on the reasoning that
//     "the toggle keeps one meaning throughout the field" -- which meant
//     a → while choosing a base ref silently turned the worktree off and
//     threw the choice away, three lines below a list the user was
//     visibly working in. A key that destroys the thing you are editing
//     is worse than a key that does nothing, so on the base part they
//     now do nothing at all; ↑ still walks the part cursor up to the
//     chips, and FooterRungs stops advertising a toggle there.
//   - On the branch part everything except ↑↓ forwards to the lineInput,
//     so ←→/Home/End move the text cursor rather than the toggle.
//   - A click on a "chip:worktree:<id>" or "row:base:<n>" zone selects
//     AND moves the part cursor there, so the keyboard picks up where the
//     mouse left off.
//   - The wheel scrolls the base list, the panel's only scrollable part.
func (w *WorktreeField) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.MouseClickMsg:
		return w.handleClick(msg)
	case tea.MouseWheelMsg:
		if w.part == partBase {
			switch wheelDelta(msg) {
			case -1:
				w.base.CursorPrev()
			case 1:
				w.base.CursorNext()
			}
		}
		return nil
	case tea.KeyPressMsg:
		switch msg.String() {
		case "up":
			if w.part == partBase && !w.baseAtTop() {
				w.base.CursorPrev()
				return nil
			}
			return w.setPart(w.part - 1)
		case "down":
			if w.part == partBase {
				w.base.CursorNext()
				return nil
			}
			return w.setPart(w.part + 1)
		case "left":
			if w.part == partChips {
				w.chips.Prev()
				return w.clampPart()
			}
			if w.part == partBase {
				return nil
			}
		case "right":
			if w.part == partChips {
				w.chips.Next()
				return w.clampPart()
			}
			if w.part == partBase {
				return nil
			}
		}
	}
	if w.part == partBranch {
		return w.updateBranch(msg)
	}
	return nil
}

// handleClick routes a left-button click to whichever of the panel's own
// nested zones it landed on, moving the part cursor with it.
func (w *WorktreeField) handleClick(msg tea.MouseClickMsg) tea.Cmd {
	if _, ok := w.chips.SelectAt(msg, "chip:"+w.ID()+":"); ok {
		w.setPart(partChips)
		// The same click may have turned the worktree off, stranding
		// nothing here but leaving the clamp to assert itself.
		return w.clampPart()
	}
	if w.baseRowsShown > 0 {
		if _, ok := w.base.SelectAt(msg, w.baseRowsShown, worktreeBaseZonePrefix); ok {
			return w.setPart(partBase)
		}
	}
	return nil
}

// updateBranch forwards msg to the branch lineInput, setting
// branchTouched only when the forwarded message actually changed the
// value -- the same before/after Value() comparison field_dir.go's
// DirField.Update and field_title.go's TitleField.Update use, so a
// non-edit message (a cursor-blink tick, a plain arrow key) never
// spuriously marks the field touched.
func (w *WorktreeField) updateBranch(msg tea.Msg) tea.Cmd {
	before := w.branch.Value()
	cmd := w.branch.Update(msg)
	if w.branch.Value() != before {
		w.branchTouched = true
	}
	return cmd
}

// setPart moves the sub-focus cursor, clamped to the parts that currently
// mean anything (maxPart), and syncs the branch input's own focus so an
// unfocused text input never silently swallows keystrokes.
func (w *WorktreeField) setPart(p worktreePart) tea.Cmd {
	if p < partChips {
		p = partChips
	}
	if limit := w.maxPart(); p > limit {
		p = limit
	}
	if p == w.part {
		return nil
	}
	w.part = p
	return w.syncBranchFocus()
}

// clampPart re-applies setPart's clamp after something OTHER than a part
// move changed which parts are live -- toggling the worktree off, or a
// project change making the target non-git, both strand a cursor parked
// on the branch or the base.
func (w *WorktreeField) clampPart() tea.Cmd { return w.setPart(w.part) }

// maxPart is the last part the cursor may sit on: only the toggle, unless
// there is both a usable git target and a worktree to configure.
func (w *WorktreeField) maxPart() worktreePart {
	if !w.isGitRepo || !w.On() {
		return partChips
	}
	return partBase
}

// syncBranchFocus keeps the wrapped lineInput focused exactly while the
// part cursor rests on it (and this Section itself holds focus), which is
// what makes its cursor blink there and nowhere else.
func (w *WorktreeField) syncBranchFocus() tea.Cmd {
	if w.focused && w.part == partBranch {
		return w.branch.Focus()
	}
	w.branch.Blur()
	return nil
}

// baseAtTop reports whether the base picker's cursor is on its first row
// (the HEAD sentinel), or the list is empty -- ↑ from here hands the part
// cursor back to the branch instead of moving the picker.
func (w *WorktreeField) baseAtTop() bool {
	sel, ok := w.base.Selected()
	return !ok || sel.ID == baseHeadID
}

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
// toggle programmatically at all (only the user's own arrow keys could
// move it) -- see field_title.go's SetTitle doc comment for the fuller
// writeup of this class of gap.
func (w *WorktreeField) SetOn(on bool) {
	if w.On() != on {
		w.chips.Next()
	}
	w.clampPart()
}

// SetGitTarget records whether the currently selected project directory
// is a git repository, gating every part of this field: the chip row
// itself goes inert (worktreeNonGitPlaceholder), the row reads
// `not a git repository`, and the Section stops taking a focus stop at
// all (Enabled).
func (w *WorktreeField) SetGitTarget(isRepo bool) {
	w.isGitRepo = isRepo
	w.chips.SetInert(!isRepo, worktreeNonGitPlaceholder)
	w.clampPart()
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

// SetBase selects ref in the base picker, "" meaning the HEAD row -- the
// mirror of Base(), and the setter spec §10's per-project memory needs in
// order to put a remembered base back on screen.
//
// A ref the current candidate pool does not (yet) hold is REMEMBERED and
// re-applied on the next list refresh, not dropped: the app layer
// resolves the remembered base from projects.json the moment the project
// row changes, which is one debounce plus one `git for-each-ref` before
// the list naming it exists. It is forgotten the moment it lands, so a
// later refresh cannot re-apply it over a selection the user has since
// moved.
func (w *WorktreeField) SetBase(ref string) {
	id := ref
	if id == "" {
		id = baseHeadID
	}
	if w.base.SelectID(id) {
		w.pendingBase, w.havePendingBase = "", false
		return
	}
	w.pendingBase, w.havePendingBase = id, true
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
	items = append(items, widgets.PickerItem{ID: baseHeadID, Cells: []string{w.headLabel()}})
	seen := map[string]bool{baseHeadID: true}
	for _, r := range w.baseRefs {
		if r == "" || seen[r] {
			continue
		}
		seen[r] = true
		items = append(items, widgets.PickerItem{ID: r, Cells: []string{r}})
	}
	w.base.SetItems(w.basePickerVersion, items)

	// A remembered base the previous pool did not hold gets its chance on
	// every refresh -- see SetBase.
	if w.havePendingBase && w.base.SelectID(w.pendingBase) {
		w.pendingBase, w.havePendingBase = "", false
	}
}

// SetHeadBranch records the branch currently checked out in the target
// repo, so the base picker's row 0 reads `HEAD (<current branch>)` --
// spec §6 field 4 verbatim. "" (a detached HEAD, which gitx.CurrentBranch
// reports as an empty string rather than an error, or simply "not resolved
// yet") leaves the row reading a bare "HEAD".
//
// Wired in the final fix wave (minor M4): gitx.CurrentBranch existed, was
// tested, and had no caller at all, while this row showed a bare "HEAD"
// that told the user nothing about what they were about to branch from.
func (w *WorktreeField) SetHeadBranch(name string) {
	if w.headBranch == name {
		return
	}
	w.headBranch = name
	// Same version, so widgets.Picker.SetItems takes its preserve-by-ID
	// branch and a user who has already moved off HEAD stays where they
	// were (refreshBaseItems' own bump=false contract).
	w.refreshBaseItems(false)
}

// HeadBranch returns the branch SetHeadBranch last recorded, or "" for a
// detached (or not yet resolved) HEAD. The app layer reads it back to
// build the header's own project-context line (v2 spec §4), so the branch
// shown up there and the one this field's HEAD row names cannot disagree.
func (w *WorktreeField) HeadBranch() string { return w.headBranch }

// headLabel is the base picker's row-0 text: "HEAD" on its own, or
// "HEAD (<branch>)" once SetHeadBranch has supplied one.
func (w *WorktreeField) headLabel() string {
	if w.headBranch == "" {
		return "HEAD"
	}
	return "HEAD (" + w.headBranch + ")"
}

// SetBaseStatus sets the status text shown alongside the base part (e.g.
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

// baseDisplay is the base PART's value text: the selected ref, or the
// HEAD row's own label when the cursor rests on the sentinel. The panel
// names the row you are pointing at, so "HEAD (main)" is right there.
func (w *WorktreeField) baseDisplay() string {
	if sel := w.Base(); sel != "" {
		return sel
	}
	return w.headLabel()
}

// rowBase is the same fact as baseDisplay, said the way the ROW says
// things: the ref this worktree will actually branch from. Selecting the
// HEAD row means "whatever is checked out", and what is checked out is
// `main` -- so the row reads `← main`, not `← HEAD (main)`, which would
// name the mechanism instead of the consequence (v2 spec §3 rule 1, and
// §4's own mockup). A detached or unresolved HEAD has no better name for
// itself, and falls back to the bare sentinel.
func (w *WorktreeField) rowBase() string {
	if sel := w.Base(); sel != "" {
		return sel
	}
	if w.headBranch != "" {
		return w.headBranch
	}
	return baseHeadID
}

// --- the row and its panel ------------------------------------------------

// Label is v2's row label (v2 spec §6's field table).
func (w *WorktreeField) Label() string { return worktreeRowLabel }

// Row states the consequence, not the settings (v2 spec §3 rule 1):
// `on · zvi/fix-login-redirect-loop ← main` when a worktree will be
// created, `on · from main` while the branch is still unnamed (see
// rowFromBase), a dim `off` when one will not be created, and a dim
// `not a git repository` when one cannot be.
//
// The elision order runs from least to most informative: the BASE gives
// up cells first (it is usually the repository's default branch, which
// the reader can guess), then the whole `← <base>` clause is dropped
// rather than shown as a stub, and only then is the BRANCH elided -- the
// branch name is the one part of this row naming something that does not
// exist yet.
func (w *WorktreeField) Row(width int) string {
	if width < 1 {
		width = 1
	}
	dim := dimText(w.palette)
	switch {
	case !w.isGitRepo:
		return fitLine(dim.Render(keepHead(worktreeNonGitPlaceholder, width)), width)
	case !w.On():
		return fitLine(dim.Render(keepHead(worktreeOffPlaceholder, width)), width)
	}

	baseText := w.rowBase()
	head := worktreeOnPrefix + worktreeRowSep

	branchText := w.Branch()
	if branchText == "" {
		return fitLine(w.rowFromBase(head, baseText, width), width)
	}

	fixed := lipgloss.Width(head) + lipgloss.Width(worktreeBaseArrow)

	// Stage 1: the base gives up cells.
	room := width - fixed - lipgloss.Width(branchText)
	if room >= lipgloss.Width(baseText) || room >= worktreeMinBaseCells {
		return fitLine(w.rowSpans(head, branchText, keepHead(baseText, room)), width)
	}
	// Stage 2: drop the base clause entirely.
	if lipgloss.Width(head)+lipgloss.Width(branchText) <= width {
		return fitLine(w.rowSpans(head, branchText, ""), width)
	}
	// Stage 3: elide the branch.
	return fitLine(w.rowSpans(head, keepHead(branchText, width-lipgloss.Width(head)), ""), width)
}

// rowFromBase is the row for a worktree that is on with no branch name
// derived yet: `on · from main`. That is not an edge case -- it is the
// state the form OPENS in, every single time, because the branch is
// derived from the title (internal/app) and the title starts empty.
//
// The row previously borrowed the branch editor's placeholder here and
// rendered `on · branch name ← main`, which reads as a branch literally
// called "branch name". Naming a branch that does not exist is the one
// thing this row must not do, so the unnamed state changes SHAPE instead
// of filling in a name: the `← ` arrow is a relation between a branch and
// the ref it forks from, and with nothing on its left there is no
// relation to draw. `from <base>` states the same consequence with the
// half that is actually decided -- "a worktree will be made from main" --
// and leaves the branch to the title panel, which already says
// `branch will be <slug>` the moment a title exists.
//
// The elision ladder collapses with the branch gone: the base is the only
// thing left to spend cells on, and below worktreeMinBaseCells of it the
// clause is dropped whole rather than stubbed -- leaving a bare `on`,
// which is still true and is the same word the chip carries.
func (w *WorktreeField) rowFromBase(head, baseText string, width int) string {
	dim := dimText(w.palette)
	room := width - lipgloss.Width(head) - lipgloss.Width(worktreeFromPrefix)
	if room >= lipgloss.Width(baseText) || room >= worktreeMinBaseCells {
		ref := lipgloss.NewStyle().Foreground(w.palette.Branch)
		return dim.Render(head+worktreeFromPrefix) + ref.Render(keepHead(baseText, room))
	}
	return dim.Render(keepHead(worktreeOnPrefix, width))
}

// rowSpans paints the row's three spans: `on` and the separators dim, the
// branch and base names in the palette's own Branch color, which is what
// herdr uses for a ref anywhere it shows one (v2 spec §7).
func (w *WorktreeField) rowSpans(head, branch, base string) string {
	dim := dimText(w.palette)
	ref := lipgloss.NewStyle().Foreground(w.palette.Branch)
	out := dim.Render(head) + ref.Render(branch)
	if base != "" {
		out += dim.Render(worktreeBaseArrow) + ref.Render(base)
	}
	return out
}

// Panel is v2 spec §4's three-part editor: the toggle, the branch and the
// base selection, each on its own line with its label in the SAME column
// the row stack above uses, then the base candidate list indented under
// the base part.
//
// The `▸` glyph in the gutter marks the active PART, not the selected
// base ref -- the list carries its own accent highlight for that
// (widgets.Picker's cursor row), so one glyph never has to mean two
// things at once.
func (w *WorktreeField) Panel(width, h int) string {
	if h < 1 {
		h = 1
	}
	inner := panelInner(width)
	labelW, valueW := labelCol(inner)

	// The provenance line is the FIRST thing a short panel gives up: at
	// v2 spec §9's three-row floor the three parts are the whole panel,
	// and a note about where a value came from must not cost the control
	// that changes it. It sits directly under those parts rather than
	// after the base list, because it speaks about them -- and because a
	// fixed line placed after a list whose length follows the window would
	// move every time the popup is resized.
	prov := ""
	if h > worktreePanelParts {
		prov = w.provenance
	}

	// A LIVE chip row supplies its own leading space (widgets.ChipRow
	// renders each chip as " label "), so it takes one cell off the label
	// column and one onto its own -- otherwise `off · on` would sit one
	// column right of the branch and base values beside it, which is
	// exactly the near-miss alignment a shared label column exists to
	// prevent. This is the same off-by-one panelChipRow handles for the
	// panels that have no label column. An INERT chip row renders its
	// placeholder bare and takes the ordinary columns.
	chipLabelW, chipValueW := labelW, valueW
	if w.isGitRepo && labelW > 0 {
		chipLabelW, chipValueW = labelW-1, valueW+1
	}

	// The base list's height is settled BEFORE the base part line is
	// composed, because whether the list is on screen is what decides
	// whether that line names the selection at all -- see panelBase.
	rows := h - worktreePanelParts - provenanceRows(prov)
	if rows < 0 || !w.isGitRepo || !w.On() {
		rows = 0
	}
	w.baseRowsShown = rows

	lines := make([]string, 0, h)
	lines = append(lines, w.panelPart(partChips, worktreeRowLabel, w.panelChips(chipValueW), chipLabelW))
	if h > 1 {
		lines = append(lines, w.panelPart(partBranch, worktreeBranchLabel, w.panelBranch(valueW), labelW))
	}
	if h > 2 {
		lines = append(lines, w.panelPart(partBase, worktreeBaseLabel, w.panelBase(valueW, w.baseListNamesSelection(rows)), labelW))
	}
	if prov != "" {
		lines = append(lines, provenanceLine(prov, width, w.palette))
	}

	if rows > 0 {
		lines = append(lines, w.panelBaseRows(labelW, valueW, rows)...)
	}
	return panelBlock(width, h, lines...)
}

// baseListNamesSelection reports whether the candidate list rendered
// under the base part will itself show the selected ref, given the rows
// it was allotted: the picker's own window always follows its cursor
// (widgets.Picker.CursorRow recomputes the same scrollOffset the render
// uses), so this is false only when there is no list -- no rows at all,
// an empty pool, or a worktree that is off or impossible.
func (w *WorktreeField) baseListNamesSelection(rows int) bool {
	return rows > 0 && w.base.CursorRow(rows) >= 0
}

// panelPart composes one of the three part lines: the gutter marker, the
// dim label padded into the shared label column, then the part's own
// already-fitted content.
func (w *WorktreeField) panelPart(part worktreePart, label, content string, labelW int) string {
	padded := ""
	if labelW > 0 {
		padded = dimText(w.palette).Width(labelW).MaxWidth(labelW).Inline(true).Render(label)
	}
	return panelGutter(w.part == part, w.palette) + padded + content
}

// panelChips renders the on/off toggle, taking only the chip row's first
// line: widgets.ChipRow appends a second one for a chip carrying a
// FocusHint, and this part owns exactly one line of the panel.
func (w *WorktreeField) panelChips(valueW int) string {
	v := w.chips.MarkedView(valueW, "chip:"+w.ID()+":")
	if idx := strings.IndexByte(v, '\n'); idx >= 0 {
		v = v[:idx]
	}
	return v
}

// panelBranch renders the branch part: the live editor while the part
// cursor rests on it, the value otherwise, and one of the two distinct
// inert placeholders when there is no branch to name.
func (w *WorktreeField) panelBranch(valueW int) string {
	switch {
	case !w.isGitRepo, !w.On():
		// An em dash, not the reason. The chips line one row above IS
		// the reason -- `off` selected, or the inert non-git
		// placeholder -- and repeating it here and again on the base
		// part printed "not a git repository" three times down a
		// four-line panel. "branch  off" also reads as a verb phrase
		// before it reads as a value, which is the sort of thing a copy
		// pass exists to catch.
		return fitLine(dimHint(w.palette).Render(rowValueNone), valueW)
	case w.focused && w.part == partBranch:
		return w.branch.View(valueW)
	case w.Branch() == "":
		return fitLine(dimHint(w.palette).Render(worktreeBranchUnset), valueW)
	default:
		return fitLine(lipgloss.NewStyle().Foreground(w.palette.Branch).Render(keepHead(w.Branch(), valueW)), valueW)
	}
}

// panelBase renders the base part: the current selection plus whatever
// SetBaseStatus last reported, or the matching inert placeholder.
//
// listNamesSelection is the fix for the base saying itself twice. The
// candidate list sits directly under this line with its own accent
// cursor on the selected ref, so naming that ref here as well rendered
//
//	base       HEAD (main)
//	             HEAD (main)
//	             main
//
// -- the same six cells, twice, one line apart, which v2 spec §4's own
// mockup does not do. Of the two ways to stop repeating it, dropping the
// value from THIS line is the one that leaves widgets.Picker alone: the
// alternative (hide the selected candidate from the list) would mean
// reimplementing the picker's windowed render and its click-to-row
// mapping inside this field to omit one row, and would give the list a
// membership that changes as the cursor moves through it. So the part
// line yields to the list -- but only while the list is actually there
// to say it. With no rows to spare it names the base itself, which is
// what keeps the selection on screen at a panel height too short for a
// list at all.
//
// The status text (SetBaseStatus: "searching…", "couldn't list") is not
// a repeat of anything and stays either way.
func (w *WorktreeField) panelBase(valueW int, listNamesSelection bool) string {
	if !w.isGitRepo || !w.On() {
		// See panelBranch: the reason belongs to the chips line, once.
		return fitLine(dimHint(w.palette).Render(rowValueNone), valueW)
	}
	status := ""
	if w.baseStatus != "" {
		status = "  " + dimHint(w.palette).Render(w.baseStatus)
	}
	budget := valueW - lipgloss.Width(status)
	if budget < 1 {
		budget = 1
	}
	display := ""
	if !listNamesSelection {
		display = keepHead(w.baseDisplay(), budget)
	}
	body := lipgloss.NewStyle().Foreground(w.palette.Branch).Render(display)
	return fitLine(fitLine(body, budget)+status, valueW)
}

// panelBaseRows renders the base candidate list, indented past the value
// column so it reads as belonging to the `base` part above it. The rows
// carry no gutter marker of their own: the picker's own accent-styled
// cursor row already says which ref is selected, and the gutter says
// which PART is active.
func (w *WorktreeField) panelBaseRows(labelW, valueW, rows int) []string {
	indent := labelW + worktreeBaseIndent
	listWidth := valueW - worktreeBaseIndent
	if listWidth < 1 {
		listWidth = 1
	}
	rendered := strings.Split(w.base.MarkedView(listWidth, rows, worktreeBaseZonePrefix), "\n")
	out := make([]string, rows)
	for i := range out {
		content := ""
		if i < len(rendered) {
			content = rendered[i]
		}
		out[i] = panelGutter(false, w.palette) + strings.Repeat(" ", indent) + content
	}
	return out
}

// PanelRows is the three parts, the provenance line when there is one,
// and one line per base candidate, capped at worktreePanelMaxRows. An
// inert or off worktree asks for the parts alone -- there is no list to
// show, and reserving rows for one would leave a hole where the panel says
// nothing -- but it still asks for the provenance line, since a repository
// that turned the worktree OFF is exactly the case a reader needs told.
func (w *WorktreeField) PanelRows() int {
	head := worktreePanelParts + provenanceRows(w.provenance)
	if !w.isGitRepo || !w.On() {
		return head
	}
	return capRows(head+w.base.FilteredLen(), worktreePanelMaxRows)
}

// SetProvenance names the config file this panel's values came from, so it
// can say so (v2 spec §11: `from .herdr-draft.toml`). "" -- the resting
// state -- is "nobody's file chose these", and the line is not reserved at
// all.
//
// One line covers the whole panel rather than one per part: the caller
// pushes a source only for a value this panel actually SHOWS and the user
// has not since moved, and three separate attributions would cost three of
// the ten rows the base list is competing for. See PlacementField
// .SetProvenance for why this takes a plain file name.
func (w *WorktreeField) SetProvenance(source string) { w.provenance = source }

// FooterRungs implements form.go's footerHinter: the footer teaches the
// focused field (v2 spec §3 rule 4), and this field's keys mean different
// things in each of its three parts, which footer.go's per-ZONE table
// cannot see. Widest first; the form appends the constant tail.
func (w *WorktreeField) FooterRungs() []string {
	if !w.isGitRepo {
		// No key does anything here, and the zone table's own
		// "↑↓ part · ←→ toggle" would promise two that do not. Saying so
		// costs one rung and stops the footer lying; the constant tail
		// still carries Tab and Esc.
		return []string{"nothing to set here"}
	}
	switch w.part {
	case partBranch:
		return []string{"type to edit · ↑↓ part", "↑↓ part"}
	case partBase:
		// No ←→ here: they are a no-op on this part (Update), and saying
		// otherwise is what made a stray → look like a working key right
		// up until it discarded the base you were picking.
		//
		// ↑ is position-dependent for the same reason ZoneTitle's Enter
		// is (footer.go): at the top of the list it hands the part
		// cursor back to the branch, and anywhere else it just moves the
		// list, so one wording cannot be true in both places.
		if w.baseAtTop() {
			return []string{"↓ pick a base · ↑ back to the branch", "↓ pick a base"}
		}
		return []string{"↑↓ pick a base"}
	default:
		if !w.On() {
			return []string{"←→ turn it on", "←→ toggle"}
		}
		return []string{"↑↓ part · ←→ toggle", "↑↓ part"}
	}
}
