// field_issue.go is written fresh for this task, per the task-18 brief's
// own provenance framing (every Task 18 component "is written fresh" --
// there is no Atrium issue-picker file on the audited clean list at all;
// Atrium has no Linear integration). It builds entirely on widgets.Picker
// (Task 14, ported from Atrium's own clean-listed ui/overlay/picker.go
// mixin), the same "own the item list, feed widgets.Picker" shape
// field_dir.go's DirField and field_worktree.go's base picker already
// establish, plus this package's own lineInput (lineinput.go) for the
// type-to-filter text spec §6 field 1 calls for.
//
// Unlike DirField's dual fragment/path mode (which needed its own fresh
// fuzzy ranker, fuzzy.go, because a path has structure a plain substring
// filter can't exploit), IssueField's filtering has no structure to
// exploit beyond "does this issue's identifier/title/status mention what
// was typed" -- so it uses widgets.Picker's own built-in SetQuery
// (case-insensitive substring over Label/Hint) directly, rather than
// re-deriving fuzzyRank a second time for a field that doesn't need it.
package form

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/ZviBaratz/herdr-draft/internal/form/widgets"
	"github.com/ZviBaratz/herdr-draft/internal/linear"
	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// issueNoneID is the "none" row's internal widgets.PickerItem.ID -- a
// leading-NUL sentinel that can never collide with a real Linear
// identifier (Linear issue identifiers are always
// "<TEAM-KEY>-<number>", never containing a NUL byte), matching
// field_worktree.go's baseHeadID sentinel discipline: widgets.Picker
// requires unique, non-empty IDs (Task 14's own carried fact), and
// Selected()/the emitted IssueChosenMsg both translate this sentinel back
// to a nil *linear.Issue at the getter boundary, so the internal ID never
// leaks through either public surface.
const issueNoneID = "\x00none"

// issueNoneLabel is the picker's own first row. v2 spec §3 rule 5 names
// this exact string as the copy pass's worked example -- "`none`, not
// `None (manual entry)`" -- and it is a good one: the parenthetical
// explained a mechanism ("you will type the title yourself") that the
// title row one line up already demonstrates.
const issueNoneLabel = "none"

// issueUnavailableLabel is the header body an inert field shows in place
// of a selection (SetUnavailable) -- the reason itself goes on the hint
// row, which has the full inner width to spend on it.
const issueUnavailableLabel = "unavailable"

const (
	// issueRowLabel is v2's row label (v2 spec §6).
	issueRowLabel = "issue"
	// issueRowNone is v2 spec §6's Unset cell. It is the same word the
	// picker's own first row now uses (issueNoneLabel): the row states a
	// value and the list row offers a choice, but there is only one honest
	// word for this one and repeating it costs nothing.
	issueRowNone = "none"
	// issuePanelMaxRows caps PanelRows: spec §10 fetches up to 50 issues,
	// far more than a panel should claim from the rest of the form.
	issuePanelMaxRows = 24
	// issuePanelEmpty speaks in the field's own terms when Linear
	// returned nothing (v2 spec §6.1's "nothing to choose", never a bare
	// "no matches").
	issuePanelEmpty = "no assigned issues"
	// issuePanelNoMatch is the OTHER empty list: Linear did return
	// issues, and the filter the user typed excludes all of them (and the
	// `none` row with them -- it is a picker item like any other, so a
	// query matching nothing empties the list completely). Saying "no
	// assigned issues" there tells the user their Linear queue is empty
	// when what is actually empty is their search, and the fix for the
	// two is different: one is "go get assigned some work", the other is
	// "delete a character". Worded to match field_dir.go's own
	// dirPanelEmpty, which is the same state on the sibling field.
	issuePanelNoMatch = "no matching issues"
)

// IssueField is the form's Linear issue Section (spec §6 field 1): a
// type-to-filter picker over the viewer's assigned issues, "none" pinned
// as row 0 for manual (non-Linear-seeded) mode. Selecting an issue emits
// IssueChosenMsg -- this package deliberately does NOT itself seed
// Title/Branch/Prompt from the chosen issue (form.go's own "the form is a
// dumb view" contract, restated in this task's brief: "the app layer
// routes seeding — the form does NOT"); the app layer's own Update,
// listening for IssueChosenMsg, is expected to call
// TitleField/WorktreeField.SetBranch/PromptField's own setters itself,
// honoring the touched-vs-preselected rule those setters already
// implement.
type IssueField struct {
	palette theme.Palette
	input   *lineInput
	picker  *widgets.Picker
	focused bool

	haveVersion bool
	version     int
	issues      []linear.Issue
	byID        map[string]linear.Issue

	pickerVersion  int
	lastSelectedID string

	// pickerRowsShown is how many issue rows the last Panel render drew.
	// widgets.Picker.SelectAt needs the SAME height MarkedView was called
	// with to map a click back to an item, and v2's panel height varies
	// with the window -- so the fixed v1 constant this used to pass
	// (issuePickerRows, 6, deleted with this field) picked the wrong
	// issue on any list the panel had scrolled.
	pickerRowsShown int

	// unavailable, when non-empty, is SetUnavailable's own reason: Linear
	// is configured but could not be reached, so the field renders inert
	// carrying that reason instead of a picker (spec §13).
	unavailable string
}

// NewIssueField returns an empty, blurred IssueField (selection on the
// "none" sentinel row) styled from palette.
func NewIssueField(palette theme.Palette) *IssueField {
	f := &IssueField{
		palette: palette,
		input:   newLineInput(palette, 0),
		picker:  widgets.NewPicker(palette),
	}
	f.input.SetPlaceholder("type to filter")
	f.refreshItems(true)
	f.lastSelectedID = issueNoneID
	return f
}

// ID identifies this Section for form.go's zoneFor.
func (f *IssueField) ID() string { return "issue" }

// Enabled reports that, once constructed, Issue takes a focus stop -- the
// "rendered only when Linear is configured" gate (spec §6 field 1) is a
// STATIC precondition the app layer applies by simply not
// constructing/including this Section at all (matching form.go's own
// Section doc comment: "Fields whose precondition is static at startup
// ... are simply not rendered"), not something IssueField itself decides
// dynamically.
//
// The one exception is SetUnavailable: Linear CONFIGURED but broken is a
// different state from Linear absent, and spec §13 requires it to degrade
// "with a reason" rather than vanish. Such a field is present-but-inert --
// rendered, skipped by the focus ring.
func (f *IssueField) Enabled() bool { return f.unavailable == "" }

// SetUnavailable marks the field present-but-inert, showing reason instead
// of a picker -- spec §13: "Linear/clauth/network failures degrade the
// respective field to inert with a reason." "" restores the normal state.
//
// Added in the final fix wave (finding I5). linear.ResolveAPIKey
// deliberately hard-errors when the user's own chosen key source fails (a
// broken api_key_cmd, or an inline api_key in a config.toml wider than
// 0600), but Bootstrap discarded that error and treated it as "no key
// configured" -- so a typo in api_key_cmd made the entire Linear field
// silently disappear with nothing anywhere saying why. Absent and broken
// now render differently: absent is still not rendered at all (the static
// precondition), broken is rendered inert, carrying its reason.
func (f *IssueField) SetUnavailable(reason string) { f.unavailable = reason }

// Focus gives the field input focus, returning the wrapped lineInput's own
// blink tea.Cmd.
func (f *IssueField) Focus() tea.Cmd {
	f.focused = true
	return f.input.Focus()
}

// Blur removes input focus.
func (f *IssueField) Blur() {
	f.focused = false
	f.input.Blur()
}

// Update handles Up/Down as picker cursor movement (see field_dir.go's
// DirField.Update for why this must intercept before the wrapped
// lineInput's own Update -- bubbles/v2's textinput unconditionally binds
// Up/Down to its own, here-irrelevant suggestion-cycling) and forwards
// every other message to the filter text input, re-querying the picker
// (via widgets.Picker.SetQuery, not a fresh fuzzy rank -- see the file
// doc) only when the forwarded message actually changed the typed text.
// Whenever the resulting Selected() row's ID actually changes (from
// either branch), an IssueChosenMsg is returned as a tea.Cmd -- the field
// deliberately does not gate this on a separate "confirm" gesture: mere
// cursor movement onto a different issue (or back to "none") IS the
// selection, mirroring spec §6 field 1's plain "Selecting seeds..."
// language and Atrium's own live-preview-while-browsing interaction
// quality (spec §3 goal 5).
func (f *IssueField) Update(msg tea.Msg) tea.Cmd {
	if click, ok := msg.(tea.MouseClickMsg); ok {
		if f.pickerRowsShown > 0 {
			if _, ok := f.picker.SelectAt(click, f.pickerRowsShown, "row:"+f.ID()+":"); ok {
				return f.selectionChangedCmd()
			}
		}
		return nil
	}
	if wheel, ok := msg.(tea.MouseWheelMsg); ok {
		switch wheelDelta(wheel) {
		case -1:
			f.picker.CursorPrev()
			return f.selectionChangedCmd()
		case 1:
			f.picker.CursorNext()
			return f.selectionChangedCmd()
		}
		return nil
	}
	if km, ok := msg.(tea.KeyPressMsg); ok {
		switch km.String() {
		case "up":
			f.picker.CursorPrev()
			return f.selectionChangedCmd()
		case "down":
			f.picker.CursorNext()
			return f.selectionChangedCmd()
		}
	}

	before := f.input.Value()
	cmd := f.input.Update(msg)
	if f.input.Value() != before {
		f.picker.SetQuery(f.input.Value())
		return tea.Batch(cmd, f.selectionChangedCmd())
	}
	return cmd
}

// selectionChangedCmd compares the picker's current Selected() ID against
// lastSelectedID and, when it differs, records the new ID and returns a
// tea.Cmd emitting IssueChosenMsg for it -- nil otherwise, so a keypress
// that moves the cursor without actually changing which row is selected
// (e.g. Up clamped at the top row) never spuriously re-fires seeding.
func (f *IssueField) selectionChangedCmd() tea.Cmd {
	sel, ok := f.picker.Selected()
	id := issueNoneID
	if ok {
		id = sel.ID
	}
	if id == f.lastSelectedID {
		return nil
	}
	f.lastSelectedID = id
	issue := f.issueByID(id)
	return func() tea.Msg { return IssueChosenMsg{Issue: issue} }
}

// issueByID translates an internal widgets.PickerItem.ID back to a
// *linear.Issue, or nil for the "none" sentinel (or an ID that no longer
// exists in the current issue set) -- the getter-boundary translation
// field_worktree.go's baseHeadID doc comment describes for the identical
// sentinel reason.
func (f *IssueField) issueByID(id string) *linear.Issue {
	if id == issueNoneID {
		return nil
	}
	issue, ok := f.byID[id]
	if !ok {
		return nil
	}
	return &issue
}

// SetIssues replaces the assigned-issue list the app layer currently has
// on offer, tagged with a caller-assigned monotonic version -- the same
// staleness guard field_dir.go's SetCandidates and field_worktree.go's
// SetBaseItems document, for the same "an out-of-order async source must
// never clobber a fresher result" reason (spec §8: cache-first render,
// then an async TTL refresh). A same-version call (e.g. the async refresh
// landing over the cache-first render at an unchanged version) preserves
// the current selection by issue identifier, inherited from
// widgets.Picker.SetItems' own same-version contract; a strictly newer
// version resets the cursor to the "none" row.
func (f *IssueField) SetIssues(version int, issues []linear.Issue) {
	if f.haveVersion && version < f.version {
		return
	}
	isNew := !f.haveVersion || version > f.version
	f.haveVersion = true
	f.version = version
	f.issues = issues
	f.refreshItems(isNew)
}

// refreshItems rebuilds the picker's item list from f.issues (the "none"
// sentinel row always first) and feeds it to the wrapped Picker, bumping
// pickerVersion first when bump is true -- see SetIssues' own doc comment
// for when each caller should pass which.
func (f *IssueField) refreshItems(bump bool) {
	if bump {
		f.pickerVersion++
	}
	items := make([]widgets.PickerItem, 0, len(f.issues)+1)
	items = append(items, widgets.PickerItem{ID: issueNoneID, Label: issueNoneLabel})
	byID := make(map[string]linear.Issue, len(f.issues))
	for _, iss := range f.issues {
		if iss.Identifier == "" {
			continue // defensive: an unidentified issue can't be a stable PickerItem.ID
		}
		byID[iss.Identifier] = iss
		items = append(items, widgets.PickerItem{
			ID:    iss.Identifier,
			Label: iss.Identifier + " · " + iss.Title,
			Hint:  issueHint(iss),
		})
	}
	f.byID = byID
	f.picker.SetItems(f.pickerVersion, items)
}

// issueHint composes the row's status/estimate hint text (spec §6 field
// 1: "showing identifier, title, status, estimate, priority" -- priority
// is deliberately left out of the fixed hint budget here, matching the
// brief's own narrower "identifier · title with status/estimate hint"
// wording; a caller wanting priority surfaced can still read it off
// Selected()/issueByID's returned *linear.Issue).
func issueHint(iss linear.Issue) string {
	var parts []string
	if iss.StateName != "" {
		parts = append(parts, iss.StateName)
	}
	if iss.Estimate != nil {
		parts = append(parts, "est "+strconv.FormatFloat(*iss.Estimate, 'g', -1, 64))
	}
	return strings.Join(parts, " · ")
}

// Selected returns the currently selected issue, or nil when the cursor
// is on the "none" row (manual mode) -- the getter a submit-time reader
// uses to learn the FINAL chosen issue, distinct from the live
// IssueChosenMsg stream Update emits as the user browses.
func (f *IssueField) Selected() *linear.Issue {
	sel, ok := f.picker.Selected()
	if !ok {
		return nil
	}
	return f.issueByID(sel.ID)
}

// --- the row and its panel ------------------------------------------------

// Label is v2's row label (v2 spec §6's field table).
func (f *IssueField) Label() string { return issueRowLabel }

// Row is the chosen issue as `ENG-101 · fix login redirect loop`, a dim
// `none` in manual mode, the live filter input while focused, and -- when
// Linear is configured but unreachable -- `unavailable  <reason>` in dim
// italic (v2 spec §6/§6.1).
//
// An over-long issue elides at its TAIL, keeping the head: the identifier
// leads, and it is the half that stays useful when the title is cut.
func (f *IssueField) Row(w int) string {
	if w < 1 {
		w = 1
	}
	switch {
	case f.unavailable != "":
		text := issueUnavailableLabel + unavailableReasonSep + f.unavailable
		return fitLine(dimHint(f.palette).Render(keepHead(text, w)), w)
	case f.focused:
		return f.input.View(w)
	default:
		sel := f.Selected()
		if sel == nil {
			return fitLine(dimText(f.palette).Render(keepHead(issueRowNone, w)), w)
		}
		text := sel.Identifier + " · " + sel.Title
		return fitLine(lipgloss.NewStyle().Foreground(f.palette.Text).Render(keepHead(text, w)), w)
	}
}

// Panel is the issue list plus one dim status line beneath it. An inert
// field shows no list at all -- there is nothing to pick -- only the
// reason it is inert, where the full panel width is available for it.
func (f *IssueField) Panel(w, h int) string {
	if h < 1 {
		h = 1
	}
	lines := make([]string, 0, h)
	f.pickerRowsShown = 0
	if f.unavailable == "" && h > 1 {
		f.pickerRowsShown = h - 1
		lines = append(lines, panelPickerLines(f.picker, w, h-1, "row:"+f.ID()+":", f.palette)...)
	}
	for len(lines) < h-1 {
		lines = append(lines, panelText("", w))
	}
	lines = append(lines, panelText(dimHint(f.palette).Render(f.panelStatus()), w))
	return panelBlock(w, h, lines...)
}

// panelStatus renders the panel's last line: the unavailable reason, one
// of the field's two distinct empty-list sentences, or nothing.
//
// The empty-Linear check comes FIRST because it is the more fundamental
// fact: with no assigned issues at all, a filter that also matches
// nothing is not what the user needs told about.
//
// v1 also had a Hint(string) setter feeding this line. It was deleted
// with the v1 path: nothing in internal/app ever called it, so the row it
// reserved was permanently blank in production -- one of the two defects
// v2 spec §2 names by hand.
func (f *IssueField) panelStatus() string {
	switch {
	case f.unavailable != "":
		return f.unavailable
	case len(f.issues) == 0:
		return issuePanelEmpty
	case f.picker.FilteredLen() == 0:
		return issuePanelNoMatch
	default:
		return ""
	}
}

// PanelRows is one row per offered issue (the "none" sentinel included)
// plus the status line, capped at issuePanelMaxRows. An inert field wants
// only the status line, which is all Panel draws for it.
func (f *IssueField) PanelRows() int {
	if f.unavailable != "" {
		return 1
	}
	return capRows(1+f.picker.FilteredLen(), issuePanelMaxRows)
}

// FooterRungs implements form.go's footerHinter for the state
// footer.go's per-ZONE table cannot see: an unavailable field draws no
// picker and no filter input at all (Panel/Row), so the table's
// "type to filter · ↑↓ choose · ⇥ complete" would advertise three keys
// with nothing to act on. Same judgement, and the same sentence, as
// field_worktree.go's non-git rung.
func (f *IssueField) FooterRungs() []string {
	if f.unavailable != "" {
		return []string{"nothing to set here"}
	}
	return nil
}

// IssueChosenMsg is emitted (as a tea.Cmd's result) whenever IssueField's
// own selection changes -- Issue is nil for the "none" row (manual mode),
// or a pointer to the newly selected issue otherwise. Per this task's own
// brief ("the app layer routes seeding — the form does NOT"), IssueField
// never itself calls TitleField/WorktreeField/PromptField's own setters;
// the app layer's own Update, wrapping form.Model, is expected to listen
// for this message and drive that seeding itself, honoring each target
// field's own touched-vs-preselected rule.
type IssueChosenMsg struct {
	Issue *linear.Issue
}
