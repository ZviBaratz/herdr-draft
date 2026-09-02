// field_dir.go PORTS github.com/ZviBaratz/atrium's
// ui/overlay/directoryPicker.go (© Zvi Baratz, relicensed by the author),
// which is on this task's audited clean-file list.
//
// Adaptations from the source, per this project's own "no I/O in
// internal/form" rule (every candidate list, validity verdict, and branch
// list arrives via setters from the app layer -- CLAUDE.md's Runtime/
// client boundary guardrail) and this package's existing widget/grammar
// conventions:
//
//   - dedupePaths is ported near-verbatim (drops empty and duplicate
//     candidates, preserving first-seen order), applied by SetCandidates
//     -- unlike Atrium's own NewDirectoryPicker/UpdateCandidates call
//     sites, which apply it once at construction/refresh time over a
//     picker whose PickerItem has no ID concept at all, this port applies
//     it for a second, load-bearing reason too: widgets.Picker's own
//     carried fact requires unique, non-empty PickerItem.IDs (see
//     refreshItems' own doc comment), and a duplicate or empty candidate
//     reaching visibleItems would otherwise violate that.
//   - Atrium's DirectoryPicker performs real filesystem I/O directly
//     (os.Open/ReadDir in listSubdirs, os.UserHomeDir/filepath.Abs in
//     expandPath) to browse the filesystem in path mode. DirField instead
//     relies ENTIRELY on the app layer for both fragment-mode's candidate
//     pool AND path-mode's browse listing, via the single
//     SetCandidates(version, []string) setter the brief specifies: the
//     app layer watches DirField's own Typed() text and re-supplies the
//     appropriate candidate set (a fixed project list for fragment mode,
//     or the browsed directory's children for path mode -- see the app
//     package's own reactToTypedDir and its debounced browse source) as
//     the user types. This package performs no os/filepath calls of any
//     kind; the one place it needs a path EXPANDED, path mode's
//     literal-fallback row, goes through the app-supplied mapper
//     SetPathExpander installs.
//   - Atrium's fragment-mode ranking is Atrium's own internal/fuzzy
//     package, which is NOT on the clean-file list and was never opened
//     for this task; fragment mode here uses this package's own fresh
//     fuzzyRank (fuzzy.go) instead, per the task-17 brief's own
//     instruction. Atrium's path-mode ranking (`fuzzy.Rank(names, base)`)
//     is likewise replaced by fuzzyRank, applied to candidate basenames.
//   - Atrium's DirectoryPicker embeds Atrium's own Picker mixin
//     (ui/overlay/picker.go), already ported generically as this
//     package's widgets.Picker in Task 14 -- DirField holds a
//     *widgets.Picker rather than re-porting that mixin a second time.
//     widgets.Picker's own SetQuery (a plain substring filter) is
//     deliberately never called here, since DirField computes its own
//     filtered/ranked item list (visibleItems) and feeds it straight to
//     SetItems -- see the package doc on widgets.Picker for why SetQuery
//     exists at all (a plainer fallback for callers that don't need
//     fuzzy ranking).
//   - Atrium's Picker mixin also owns raw filter-text KEY HANDLING
//     (handleKey/handlePaste), which widgets.Picker deliberately dropped
//     (see its own package doc: "that belongs to whatever text-entry
//     widget... feeds this Picker's SetQuery"). DirField supplies that
//     itself via this package's own lineInput (lineinput.go, an
//     independent implementation over bubbles/v2's textinput, NOT a port
//     of Atrium's textInput.go), rather than hand-rolling filter-text
//     editing a second time.
//   - CompletePrefix becomes Complete() (the completer capability
//     form.go's handleKey type-asserts for), same longest-common-prefix
//     algorithm, ported near-verbatim (commonPrefix/longestCommonPrefix
//     below) but operating over app-supplied candidate basenames instead
//     of a fresh os.ReadDir call.
//   - SetSelectionState/ClearSelectionState/selectionMarker become
//     SetValidity(path, v) plus marker(), collapsed into ONE call with no
//     separate Clear: marker() only renders when validityPath still
//     equals the CURRENT Value() (the same "clears the moment the
//     selection moves" timing Atrium's own doc comment describes, applied
//     via comparison instead of an explicit reset call) -- mirrored by
//     field_title.go's SetVerdict for the same reason.
//   - GetSelectedPath/SelectPath become Value() (no SelectPath equivalent
//     exists -- the brief's own setter list has none; SetCandidates'
//     version-refresh-preserves-by-ID behavior, inherited from
//     widgets.Picker.SetItems, covers the "keep the app's chosen default
//     selected across a refresh" case Atrium used SelectPath for).
//   - displayPath (the "~" collapsing helper) is dropped: it calls
//     os.UserHomeDir, which this package's own "no I/O" rule excludes:
//     display formatting of candidate strings is the app layer's job now
//     (it may pre-shorten paths before calling SetCandidates).
package form

import (
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/ZviBaratz/herdr-draft/internal/form/widgets"
	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// Validity is the app layer's own verdict on the currently selected
// directory (spec §6 field 2), reported via SetValidity.
type Validity int

const (
	// ValidityRepo is a normal git repository -- no inline marker shown.
	ValidityRepo Validity = iota
	// ValidityDirect is a valid, existing directory that is not a git
	// repository -- shown as a dim "(direct)" marker (a direct, non-
	// worktree-isolated session).
	ValidityDirect
	// ValidityInvalid is a path that does not exist (or is not a
	// directory) -- shown as a danger-colored "(invalid)" marker.
	ValidityInvalid
)

const (
	// dirRowLabel is v2's row label. Note it is NOT this Section's ID()
	// ("dir", which zoneKindByID and every mouse zone already spell that
	// way): v2 spec §6's field table calls the row "project", and the two
	// names have no reason to be the same one.
	dirRowLabel = "project"
	// dirRowNone is what the row reads when no candidate is selected --
	// v2 spec §6's table has no unset cell for this field because
	// production always has a directory, but a form whose candidate list
	// is empty must still say something.
	dirRowNone = "none"
	// dirRowInvalid and dirRowNotRepo are v2 spec §6's Inert cells for
	// this row, replacing v1's parenthesized "(invalid)"/"(direct)"
	// markers. The row-stack rewrite plan kept v1's wording; the spec's
	// table is normative and says `invalid` / `not a repository`, so that
	// is what the row says.
	dirRowInvalid   = "invalid"
	dirRowNotRepo   = "not a repository"
	dirRowMarkerGap = "  " // separates a path from its marker

	// dirPanelMaxRows caps PanelRows: a project list can be long, and the
	// panel should not claim more of the form than it can fill.
	dirPanelMaxRows = 12
	// dirPanelEmpty speaks in the field's own terms when nothing matches
	// (v2 spec §6.1's "nothing to choose", never a bare "no matches").
	dirPanelEmpty = "no matching directories"
)

// DirField is the form's Project directory Section (spec §6 field 2): a
// dual-mode picker over app-supplied candidates -- fragment mode (fuzzy-
// ranked filtering of the full candidate pool) when the typed text
// doesn't look like a path, path mode (browsing whatever children the app
// layer has most recently supplied for the current prefix) when it does.
type DirField struct {
	palette theme.Palette
	input   *lineInput
	picker  *widgets.Picker
	focused bool

	haveCandVersion bool
	candVersion     int
	candidates      []string

	// pickerRowsShown is how many candidate rows the last Panel render
	// drew. widgets.Picker.SelectAt needs the SAME height MarkedView was
	// called with to map a click back to an item, and v2's panel height
	// varies with the window -- so the fixed v1 constant this used to
	// pass (dirPickerRows, 4, deleted with this field) picked the wrong
	// candidate on any list the panel had scrolled.
	pickerRowsShown int

	// pickerVersion is bumped only when the FILTER TEXT changes (a fresh
	// result set the user is now looking at, which should reset the
	// cursor to the top -- widgets.Picker.SetItems' own "new version"
	// behavior) and left UNCHANGED across a SetCandidates call under an
	// otherwise-unmodified filter (a same-context candidate refresh,
	// which should instead preserve the user's selection by item ID --
	// widgets.Picker.SetItems' own "same version" behavior). This is what
	// gives DirField Atrium's UpdateCandidates re-anchor-by-path behavior
	// "for free," inherited from Task 14's own Picker contract rather
	// than re-implemented here.
	pickerVersion int

	validityKnown bool
	validityPath  string
	validity      Validity

	// pathExpander is SetPathExpander's app-supplied raw->absolute mapper,
	// nil until installed (identity, this field's own pre-browse
	// behavior).
	pathExpander func(string) string

	// notes is SetNotes' app-supplied report about the SELECTED project --
	// v2 spec §11's "ignored and reported" lines. See SetNotes.
	notes []string

	// homeDir is SetHomeDir's app-supplied home directory, used ONLY to
	// collapse a leading home prefix to "~" where a path is DISPLAYED
	// (v2 spec §6: "path, ~-shortened"). Purely cosmetic: Value() and
	// every PickerItem.ID stay the real path.
	homeDir string
}

// NewDirField returns an empty, blurred DirField styled from palette.
func NewDirField(palette theme.Palette) *DirField {
	d := &DirField{
		palette: palette,
		input:   newLineInput(palette, 0),
		picker:  widgets.NewPicker(palette),
	}
	d.input.SetPlaceholder("type to search, or / ~ . to browse")
	// One column, and the one place in the form where a picker cell
	// elides at its HEAD (v3 spec §8.1's ElideMode): these are paths, and
	// the last segments are what distinguish "~/Projects/herdr" from
	// "~/Projects/herdr-draft" while the shared prefix is what every row
	// on screen already has in common. Before v3 an over-long candidate
	// was clipped silently at its tail by the row style, which is exactly
	// the MISREAD sizes.go's file doc warns about.
	d.picker.SetColumns(widgets.PickerColumn{Elide: widgets.ElideHead})
	d.refreshItems(true)
	return d
}

// ID identifies this Section for form.go's zoneFor.
func (d *DirField) ID() string { return "dir" }

// Enabled reports that Project is always present -- spec §6 field 2 has no
// precondition that could ever make it unavailable.
func (d *DirField) Enabled() bool { return true }

// Focus gives the field input focus.
func (d *DirField) Focus() tea.Cmd {
	d.focused = true
	return d.input.Focus()
}

// Blur removes input focus.
func (d *DirField) Blur() {
	d.focused = false
	d.input.Blur()
}

// Update handles Up/Down as picker cursor movement (see lineinput.go's own
// doc comment on why this must be intercepted BEFORE reaching the wrapped
// lineInput's Update -- bubbles/v2's textinput unconditionally binds
// Up/Down to its own, here-irrelevant suggestion-cycling); every other
// message is forwarded to the filter/path text input. items are only
// re-ranked (and the picker's cursor only reset to the top) when the
// forwarded message actually CHANGED the typed text -- comparing Value()
// before and after, rather than assuming every Update call is an edit --
// so a non-edit message (e.g. a cursor-blink tick, forwarded here because
// this Section has focus, or a Left/Right/Home/End keypress that only
// moves the input cursor) never spuriously resets a candidate row the
// user has already selected with the arrow keys back to the top.
func (d *DirField) Update(msg tea.Msg) tea.Cmd {
	if click, ok := msg.(tea.MouseClickMsg); ok {
		if d.pickerRowsShown > 0 {
			d.picker.SelectAt(click, d.pickerRowsShown, "row:"+d.ID()+":")
		}
		return nil
	}
	if wheel, ok := msg.(tea.MouseWheelMsg); ok {
		switch wheelDelta(wheel) {
		case -1:
			d.picker.CursorPrev()
		case 1:
			d.picker.CursorNext()
		}
		return nil
	}
	if km, ok := msg.(tea.KeyPressMsg); ok {
		switch km.String() {
		case "up":
			d.picker.CursorPrev()
			return nil
		case "down":
			d.picker.CursorNext()
			return nil
		}
	}

	before := d.input.Value()
	cmd := d.input.Update(msg)
	if d.input.Value() != before {
		d.refreshItems(true)
	}
	return cmd
}

// Complete implements form.go's completer capability (ZoneDir.isPicker()
// == true): shell-style "extend to the longest common prefix, then
// advance" Tab-completion in path mode, ported from Atrium's
// CompletePrefix (see the file doc's Adaptations section). Fragment mode
// has no completion concept and always returns false, letting MapKey fall
// back to a plain advance -- keys.go's own documented contract for "a
// zone whose widget has nothing to complete."
func (d *DirField) Complete() bool {
	raw := d.input.Value()
	if !LooksLikePath(raw) {
		return false
	}
	dir, base := SplitPath(raw)

	lower := strings.ToLower(base)
	var names []string
	for _, c := range d.candidates {
		n := basename(c)
		if strings.HasPrefix(strings.ToLower(n), lower) {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return false
	}

	lcp := longestCommonPrefix(names)
	newFilter := dir + lcp
	if lcp == "" || newFilter == raw {
		return false
	}

	d.input.SetValue(newFilter)
	d.refreshItems(true)
	return true
}

// SetCandidates replaces the candidate pool the app layer currently has
// on offer (a fixed project list for fragment mode, or the current
// directory's children for path mode), tagged with a caller-assigned
// monotonic version -- a call whose version is lower than the highest one
// already accepted is dropped outright, the same staleness guard
// widgets.Picker.SetItems documents for the identical "an out-of-order
// async source must never clobber a fresher result" reason.
//
// This does NOT reset the picker's own cursor to the top -- see
// pickerVersion's own doc comment: a same-filter refresh preserves the
// current selection by item ID wherever possible, matching Atrium's own
// UpdateCandidates.
func (d *DirField) SetCandidates(version int, candidates []string) {
	if d.haveCandVersion && version < d.candVersion {
		return
	}
	d.haveCandVersion = true
	d.candVersion = version
	d.candidates = dedupePaths(candidates)
	d.refreshItems(false)
}

// dedupePaths drops empty and duplicate entries, preserving first-seen
// order -- ported near-verbatim from Atrium's own dedupePaths
// (directoryPicker.go; the file doc's Adaptations list originally omitted
// this helper -- added in review round 1 to keep the port record
// accurate). SetCandidates applies it so a caller-supplied duplicate (or
// empty-string) candidate can never reach visibleItems/refreshItems and
// produce two widgets.PickerItem rows sharing the same ID -- the same
// unique-non-empty-ID invariant field_worktree.go's baseHeadID fix
// addresses on the base-picker side.
func dedupePaths(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	deduped := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		deduped = append(deduped, p)
	}
	return deduped
}

// SetValidity records the app layer's own verdict for path, shown as an
// inline marker on the header row only while path still equals the
// CURRENT selection (Value()) -- see the file doc's Adaptations section
// on why there is no separate Clear method.
func (d *DirField) SetValidity(path string, v Validity) {
	d.validityKnown = true
	d.validityPath = path
	d.validity = v
}

// Value returns the currently selected candidate/path, or "" if nothing
// is selected (an empty filtered list).
func (d *DirField) Value() string {
	sel, ok := d.picker.Selected()
	if !ok {
		return ""
	}
	return sel.ID
}

// Typed returns the RAW text the user has entered, as opposed to
// Value()'s resolved selection: "~/Pro" while Value() is whatever
// candidate row that currently highlights.
//
// The app layer watches this (not Value()) to drive path mode, because
// path mode is a property of what is being TYPED -- which directory to
// list, and whether the candidate pool should be the project list or a
// directory's children -- and every candidate row it then supplies is a
// consequence of it. Watching Value() instead would be circular.
func (d *DirField) Typed() string { return d.input.Value() }

// SetPathExpander installs the app layer's raw->absolute path mapper
// (pathx.Resolve in production), used for path mode's literal-fallback
// row -- so the fallback names the same directory, in the same notation,
// as the browsed rows around it, which the app layer supplies already
// expanded. Without one the fallback stays the raw typed text, which is
// this field's own behavior in every test that installs none.
//
// The function is the app layer's; this package still performs no I/O of
// its own (see the file doc's Adaptations section).
func (d *DirField) SetPathExpander(f func(string) string) {
	d.pathExpander = f
	d.refreshItems(false)
}

// expand applies SetPathExpander's mapper, falling back to raw when none
// is installed or the mapper returns an empty string -- refreshItems'
// PickerItem IDs must stay non-empty.
func (d *DirField) expand(raw string) string {
	if d.pathExpander == nil {
		return raw
	}
	if expanded := d.pathExpander(raw); expanded != "" {
		return expanded
	}
	return raw
}

// SetHomeDir installs the app layer's own home-directory string (os.
// UserHomeDir in production), used to collapse a leading home prefix to
// "~" wherever this field DISPLAYS a path -- v2's row and the panel's own
// candidate rows (v2 spec §4's mockup shows both collapsed) -- wired
// exactly like SetPathExpander, and for the same reason: this package
// performs no I/O and cannot ask the OS where home is.
//
// DISPLAY ONLY, and the split runs through widgets.PickerItem: the ID
// stays the real path and the Label is what collapses, so Value() and
// every path this field hands the app layer are untouched. That is what
// makes it safe to install at construction -- a "~" that leaked into a
// value would be a path no filesystem call could resolve.
//
// It refreshes the item list rather than only recording the string,
// because the Labels built by the last refreshItems call are already
// rendered from the PREVIOUS home. Same version, so the picker's
// preserve-by-ID branch keeps the current selection.
//
// "" (and "/", which would collapse every absolute path to "~") disable
// collapsing entirely.
func (d *DirField) SetHomeDir(home string) {
	if d.homeDir == home {
		return
	}
	d.homeDir = home
	d.refreshItems(false)
}

// collapseHome renders p with a leading homeDir replaced by "~" -- a pure
// string operation, deliberately not filepath.Rel (see basename's own doc
// comment: this package performs no path/OS calls of any kind).
//
// The prefix must end at a segment boundary: "/home/zvi" must not turn
// "/home/zvirus/x" into "~us/x".
func (d *DirField) collapseHome(p string) string {
	home := strings.TrimSuffix(d.homeDir, "/")
	if home == "" || p == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}
	return p
}

// --- the row and its panel ------------------------------------------------

// Label is v2's row label -- "project", not this Section's ID (see
// dirRowLabel's own doc comment).
func (d *DirField) Label() string { return dirRowLabel }

// Row is the selected directory with "~" collapsed, plus the validity
// marker v2 spec §6 spells `invalid` / `not a repository`, or the live
// dual-mode input while focused.
//
// The path elides at its HEAD, keeping the TAIL: the last segments are
// what tell "~/Projects/herdr" from "~/Projects/herdr-draft", and the
// shared prefix is what every other candidate has too. The marker's width
// is subtracted first, so a verdict is never the thing that gets cut.
func (d *DirField) Row(w int) string {
	if w < 1 {
		w = 1
	}
	marker := d.rowMarker()
	budget := w - lipgloss.Width(marker)
	if budget < 1 {
		budget = 1
	}
	if d.focused {
		return fitLine(d.input.View(budget)+marker, w)
	}
	val := d.Value()
	if val == "" {
		return fitLine(dimText(d.palette).Render(keepHead(dirRowNone, budget))+marker, w)
	}
	shown := keepTail(d.collapseHome(val), budget)
	return fitLine(lipgloss.NewStyle().Foreground(d.palette.Text).Render(shown)+marker, w)
}

// rowMarker renders v2's validity word for the CURRENT selection, or ""
// when the app layer's last verdict was for some other path (the same
// staleness-by-comparison guard v1's marker() uses).
func (d *DirField) rowMarker() string {
	if !d.validityKnown || d.validityPath != d.Value() {
		return ""
	}
	switch d.validity {
	case ValidityInvalid:
		return dirRowMarkerGap + lipgloss.NewStyle().Foreground(d.palette.Danger).Render(dirRowInvalid)
	case ValidityDirect:
		return dirRowMarkerGap + dimText(d.palette).Render(dirRowNotRepo)
	default:
		return ""
	}
}

// Panel is the candidate list, then whatever notes the app layer has about
// the selected project, then one dim status line.
//
// The notes come BETWEEN the list and the status line rather than after it
// so they sit against the list they belong beside: the status line is
// usually empty (panelStatus speaks only when nothing matches), and a note
// separated from the list by a blank row reads as belonging to neither.
// Nothing is lost by putting the status last -- it has something to say
// only when the list is EMPTY, and notesShown reserves the list no rows in
// that case, so the status always fits.
func (d *DirField) Panel(w, h int) string {
	if h < 1 {
		h = 1
	}
	notes := d.notesShown(h)

	lines := make([]string, 0, h)
	d.pickerRowsShown = 0
	if rows := h - 1 - len(notes); rows > 0 {
		d.pickerRowsShown = rows
		lines = append(lines, panelPickerLines(d.picker, w, rows, "row:"+d.ID()+":", d.palette)...)
	}
	// Warning, not dim: every other line in this panel is a thing the user
	// can pick, and these are the one thing here that says something they
	// wrote was refused. A note styled like a hint is a note nobody reads,
	// which is the whole defect v2 spec §11's "with a visible note" names.
	// The text is prose, so it elides at its TAIL (keepHead) rather than
	// being clipped silently by panelText's own fit.
	note := lipgloss.NewStyle().Foreground(d.palette.Warning)
	for _, n := range notes {
		lines = append(lines, panelText(note.Render(keepHead(n, panelInner(w))), w))
	}
	lines = append(lines, panelText(dimHint(d.palette).Render(d.panelStatus()), w))
	return panelBlock(w, h, lines...)
}

// notesShown is how many of the notes this panel height can afford, from
// the front.
//
// Two rows are spoken for before the first note: the status line, and one
// candidate row whenever there IS a candidate. The chooser is what this
// panel is for -- a report about a config file must never be the thing that
// empties it -- so at v2 spec §9's three-row floor a long report shows its
// first line and the list keeps its cursor row. The rest of the report
// comes back as soon as the window does.
func (d *DirField) notesShown(h int) []string {
	room := h - 1
	if d.picker.FilteredLen() > 0 {
		room--
	}
	if room > len(d.notes) {
		room = len(d.notes)
	}
	if room < 0 {
		room = 0
	}
	return d.notes[:room]
}

// SetNotes records the app layer's report about the SELECTED project --
// v2 spec §11's one line per key in that repository's committed
// `.herdr-draft.toml` that the trust model refused, and the reason, plus
// the single line a malformed file reports instead.
//
// They live on THIS field because they are a property of the FILE, not of
// any one value: the project row is what decides which repository the form
// points at, and therefore which file this is. A reader who wonders why a
// key they committed did nothing looks at the row that chose the repository.
//
// It takes plain strings, already worded: this package performs no I/O and
// knows nothing of internal/config, so the classification and the reasons
// are decided over there and only the finished lines arrive here. nil (the
// resting state, and what a repository with no such file yields) reserves
// no rows at all.
func (d *DirField) SetNotes(notes []string) { d.notes = notes }

// panelStatus renders the panel's last line: the field's own empty-list
// sentence, or nothing.
//
// v1 also had a Hint(string) setter feeding this line. It was deleted
// with the v1 path: nothing in internal/app ever called it, so the row it
// reserved was permanently blank in production -- one of the two defects
// v2 spec §2 names by hand.
func (d *DirField) panelStatus() string {
	if d.picker.FilteredLen() == 0 {
		return dirPanelEmpty
	}
	return ""
}

// PanelRows is one row per candidate, one per note (SetNotes) and the
// status line, capped at dirPanelMaxRows.
func (d *DirField) PanelRows() int {
	return capRows(1+len(d.notes)+d.picker.FilteredLen(), dirPanelMaxRows)
}

// refreshItems recomputes visibleItems() and feeds it to the wrapped
// Picker, bumping pickerVersion first when bump is true -- see
// pickerVersion's own doc comment for when each caller should pass which.
func (d *DirField) refreshItems(bump bool) {
	if bump {
		d.pickerVersion++
	}
	items := d.visibleItems()
	pickerItems := make([]widgets.PickerItem, len(items))
	for i, it := range items {
		// PickerItem.ID has no uniqueness contract of its own
		// (widgets/picker.go's own doc); using the full path/candidate
		// string as ID (this task's own "verified fact") is unique here
		// PROVIDED items itself holds no duplicate -- which it doesn't, by
		// construction of every visibleItems() branch: the empty-filter
		// branch and fragment mode both return a subset/reorder of
		// d.candidates, itself deduped by SetCandidates' own dedupePaths;
		// path mode (pathModeItems) explicitly dedupes by full path via
		// its own seen map, for both the ranked results and the literal
		// fallback. Softened from an earlier, unqualified "guarantees
		// uniqueness here" after review round 1 found a real duplicate-ID
		// path through pathModeItems this claim had not yet accounted for
		// -- see pathModeItems' and dedupePaths' own doc comments.
		// ID is the REAL path (Value()/SetValidity/every app-layer read go
		// through it); the cell is what the panel shows, with the home
		// prefix collapsed to "~" -- see SetHomeDir. The two differ only
		// in presentation, which is exactly the split PickerItem is for.
		pickerItems[i] = widgets.PickerItem{ID: it, Cells: []string{d.collapseHome(it)}}
	}
	d.picker.SetItems(d.pickerVersion, pickerItems)
}

// visibleItems computes the current mode's displayed item list: every
// candidate (empty filter), a fuzzy-ranked subset of the candidate pool
// (fragment mode), or a fuzzy-ranked-by-basename subset of the candidate
// pool plus a literal fallback (path mode) -- see pathModeItems.
func (d *DirField) visibleItems() []string {
	raw := d.input.Value()
	if raw == "" {
		return append([]string(nil), d.candidates...)
	}
	if !LooksLikePath(raw) {
		return fuzzyRank(d.candidates, raw)
	}
	return d.pathModeItems(raw)
}

// pathModeItems ranks the FULL candidate list against SplitPath's own base
// component, matching each candidate by its OWN basename (fuzzyRank, not a
// plain prefix match -- matching Atrium's own path-mode ranking,
// `fuzzy.Rank(names, base)`), then appends the literal typed path -- put
// through SetPathExpander's own mapper when the app layer has installed
// one -- as a fallback if it isn't already present, so a complete or
// not-yet-listed path stays selectable even when nothing on offer matches
// it (ported in spirit from Atrium's own literal-fallback comment in
// visibleItems).
//
// Ranking is done by feeding fuzzyRank every candidate's basename WITHOUT
// first collapsing duplicate basenames into one representative candidate
// (a real bug an earlier version of this function had, caught in review
// round 1: two distinct candidates sharing a basename, e.g. "~/work/api"
// and "~/oss/api", would silently collapse to whichever one a
// name->single-path map happened to keep). Instead, byName maps each
// basename to the ORDERED bucket of every candidate sharing it; since two
// candidates with an IDENTICAL basename always score identically against
// the same query (fuzzyMatch's result depends only on the string content,
// not the candidate's position), fuzzyRank's own documented tiebreak on
// original input order keeps same-named entries in their original
// relative order within its result -- so popping each bucket FIFO as
// pathModeItems walks that result always re-associates the correct
// distinct candidate with each ranked occurrence of its basename,
// regardless of how many differently-named candidates are interspersed.
func (d *DirField) pathModeItems(raw string) []string {
	_, base := SplitPath(raw)

	names := make([]string, len(d.candidates))
	byName := make(map[string][]string, len(d.candidates))
	for i, c := range d.candidates {
		n := basename(c)
		names[i] = n
		byName[n] = append(byName[n], c)
	}

	ranked := fuzzyRank(names, base)
	items := make([]string, 0, len(ranked)+1)
	seen := make(map[string]bool, len(ranked)+1)
	for _, n := range ranked {
		bucket := byName[n]
		if len(bucket) == 0 {
			continue // defensive: cannot happen, every ranked name came from names
		}
		p := bucket[0]
		byName[n] = bucket[1:]
		if !seen[p] {
			seen[p] = true
			items = append(items, p)
		}
	}
	if literal := d.expand(raw); !seen[literal] {
		items = append(items, literal)
	}
	return items
}

// LooksLikePath reports whether s should be treated as a path to browse
// rather than a fragment to fuzzy-match against the candidate pool --
// ported verbatim from Atrium's own looksLikePath.
//
// Exported (with SplitPath) because the app layer decides from the SAME
// grammar which directory to list and when to swap the candidate pool
// (spec §6 field 2). A second copy of the rule over there would be a
// second rule: the pool would swap on inputs this field does not treat as
// paths, or fail to swap on inputs it does.
func LooksLikePath(s string) bool {
	return strings.HasPrefix(s, "/") || strings.HasPrefix(s, "~") || strings.HasPrefix(s, ".")
}

// SplitPath splits raw into the directory portion to browse and the base
// prefix to match within it -- ported from Atrium's own splitRawPath
// (operating on the raw, un-expanded string; this package performs no
// path expansion at all, see the file doc's Adaptations section).
func SplitPath(raw string) (dir, base string) {
	if strings.HasSuffix(raw, "/") {
		return raw, ""
	}
	if idx := strings.LastIndex(raw, "/"); idx >= 0 {
		return raw[:idx+1], raw[idx+1:]
	}
	return raw, ""
}

// basename returns the final path segment of p (a pure string operation,
// deliberately not filepath.Base -- this package performs no path/OS
// calls of any kind, and a plain "/"-based split keeps this file's
// behavior self-contained and independently testable without touching the
// filesystem).
func basename(p string) string {
	p = strings.TrimSuffix(p, "/")
	if idx := strings.LastIndex(p, "/"); idx >= 0 {
		return p[idx+1:]
	}
	return p
}

// longestCommonPrefix returns the longest common prefix of names,
// comparing case-insensitively but emitting the casing of the first name
// -- ported near-verbatim from Atrium's own longestCommonPrefix.
func longestCommonPrefix(names []string) string {
	if len(names) == 0 {
		return ""
	}
	prefix := names[0]
	for _, n := range names[1:] {
		prefix = commonPrefix(prefix, n)
		if prefix == "" {
			break
		}
	}
	return prefix
}

// commonPrefix returns the longest common (case-insensitive) prefix of a
// and b -- ported verbatim from Atrium's own commonPrefix.
func commonPrefix(a, b string) string {
	ra, rb := []rune(a), []rune(b)
	n := 0
	for n < len(ra) && n < len(rb) && unicode.ToLower(ra[n]) == unicode.ToLower(rb[n]) {
		n++
	}
	return string(ra[:n])
}
