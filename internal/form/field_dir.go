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
//   - Atrium's DirectoryPicker performs real filesystem I/O directly
//     (os.Open/ReadDir in listSubdirs, os.UserHomeDir/filepath.Abs in
//     expandPath) to browse the filesystem in path mode. DirField instead
//     relies ENTIRELY on the app layer for both fragment-mode's candidate
//     pool AND path-mode's browse listing, via the single
//     SetCandidates(version, []string) setter the brief specifies: the
//     app layer is expected to watch DirField's own Value()/typed text
//     (via whatever polling or event mechanism it uses outside this
//     package) and re-supply the appropriate candidate set (a fixed
//     project list for fragment mode, or the current directory's children
//     for path mode) as the user types. This package performs no
//     os/filepath calls of any kind.
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

// dirPickerRows is DirField's fixed candidate-row count (spec §6 field 2's
// own constant-height contract) -- chosen to match
// widgets.PromptArea.PromptAreaPreferredRows so the form's two "list-
// shaped" fields default to the same visual weight.
const dirPickerRows = 4

const dirLabel = "Project: "

// DirField is the form's Project directory Section (spec §6 field 2): a
// dual-mode picker over app-supplied candidates -- fragment mode (fuzzy-
// ranked filtering of the full candidate pool) when the typed text
// doesn't look like a path, path mode (browsing whatever children the app
// layer has most recently supplied for the current prefix) when it does.
//
// DirField renders at a constant 2+dirPickerRows physical lines regardless
// of focus, candidates, or validity state -- a header row (label + typed
// text/selection + inline validity marker), an always-reserved hint row,
// then dirPickerRows candidate rows (blank while unfocused, matching
// Atrium's own DirectoryPicker.Render contract).
type DirField struct {
	palette theme.Palette
	input   *lineInput
	picker  *widgets.Picker
	focused bool

	haveCandVersion bool
	candVersion     int
	candidates      []string

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

	hint string
}

// NewDirField returns an empty, blurred DirField styled from palette.
func NewDirField(palette theme.Palette) *DirField {
	d := &DirField{
		palette: palette,
		input:   newLineInput(palette, 0),
		picker:  widgets.NewPicker(palette),
	}
	d.input.SetPlaceholder("type to search, or / ~ . to browse")
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
	if !looksLikePath(raw) {
		return false
	}
	dir, base := splitPath(raw)

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
	d.candidates = append([]string(nil), candidates...)
	d.refreshItems(false)
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

// Hint sets the text shown on the always-reserved hint row.
func (d *DirField) Hint(s string) { d.hint = s }

// Height reports DirField's constant footprint -- independent of winH,
// focus, candidates, or validity state (see the type doc comment).
func (d *DirField) Height(int) int { return 2 + dirPickerRows }

// View renders the field at exactly Height's own physical line count.
func (d *DirField) View(inner int) string {
	if inner < 1 {
		inner = 1
	}
	header := d.renderHeader(inner)
	hintLine := fitLine(dimHint(d.palette).Render(d.hint), inner)

	var rows string
	if d.focused {
		rows = d.picker.View(inner, dirPickerRows)
	} else {
		blanks := make([]string, dirPickerRows)
		for i := range blanks {
			blanks[i] = fitLine("", inner)
		}
		rows = strings.Join(blanks, "\n")
	}

	return header + "\n" + hintLine + "\n" + rows
}

// renderHeader renders the label plus, while focused, the raw editable
// filter/path text, or, while unfocused, the resolved current selection
// (matching Atrium's own Render: "when unfocused it shows the chosen
// project on the header line") -- followed in both cases by the inline
// validity marker.
func (d *DirField) renderHeader(inner int) string {
	labelStyled := lipgloss.NewStyle().Foreground(d.palette.Text).Render(dirLabel)
	marker := d.marker()
	chrome := lipgloss.Width(dirLabel) + lipgloss.Width(marker)
	budget := inner - chrome
	if budget < 1 {
		budget = 1
	}

	var body string
	if d.focused {
		body = d.input.View(budget)
	} else if val := d.Value(); val == "" {
		body = fitLine(dimHint(d.palette).Render("(none)"), budget)
	} else {
		body = fitLine(lipgloss.NewStyle().Foreground(d.palette.Text).Render(val), budget)
	}

	return fitLine(labelStyled+body+marker, inner)
}

// marker renders SetValidity's own inline indicator, or "" when no
// verdict currently applies to the CURRENT selection (see SetValidity's
// doc comment).
func (d *DirField) marker() string {
	if !d.validityKnown || d.validityPath != d.Value() {
		return ""
	}
	switch d.validity {
	case ValidityInvalid:
		return "  " + lipgloss.NewStyle().Foreground(d.palette.Danger).Render("(invalid)")
	case ValidityDirect:
		return "  " + dimHint(d.palette).Render("(direct)")
	default:
		return ""
	}
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
		// string as ID (this task's own "verified fact") guarantees
		// uniqueness here.
		pickerItems[i] = widgets.PickerItem{ID: it, Label: it}
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
	if !looksLikePath(raw) {
		return fuzzyRank(d.candidates, raw)
	}
	return d.pathModeItems(raw)
}

// pathModeItems ranks the candidate pool's basenames against splitPath's
// own base component (fuzzyRank, not a plain prefix match -- matching
// Atrium's own path-mode ranking, `fuzzy.Rank(names, base)`), maps each
// ranked basename back to its full candidate path, then appends the
// literal typed path as a fallback if it isn't already present -- so a
// complete or not-yet-listed path stays selectable even when nothing on
// offer matches it (ported in spirit from Atrium's own literal-fallback
// comment in visibleItems).
func (d *DirField) pathModeItems(raw string) []string {
	_, base := splitPath(raw)

	names := make([]string, len(d.candidates))
	byName := make(map[string]string, len(d.candidates))
	for i, c := range d.candidates {
		n := basename(c)
		names[i] = n
		if _, exists := byName[n]; !exists {
			byName[n] = c
		}
	}

	ranked := fuzzyRank(names, base)
	items := make([]string, 0, len(ranked)+1)
	seen := make(map[string]bool, len(ranked)+1)
	for _, n := range ranked {
		p := byName[n]
		if !seen[p] {
			seen[p] = true
			items = append(items, p)
		}
	}
	if !seen[raw] {
		items = append(items, raw)
	}
	return items
}

// looksLikePath reports whether s should be treated as a path to browse
// rather than a fragment to fuzzy-match against the candidate pool --
// ported verbatim from Atrium's own looksLikePath.
func looksLikePath(s string) bool {
	return strings.HasPrefix(s, "/") || strings.HasPrefix(s, "~") || strings.HasPrefix(s, ".")
}

// splitPath splits raw into the directory portion to browse and the base
// prefix to match within it -- ported from Atrium's own splitRawPath
// (operating on the raw, un-expanded string; this package performs no
// path expansion at all, see the file doc's Adaptations section).
func splitPath(raw string) (dir, base string) {
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
