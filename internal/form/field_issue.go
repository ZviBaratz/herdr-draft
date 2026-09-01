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

// issuePickerRows is IssueField's fixed candidate-row count (spec §6
// field 1's own constant-height contract), picked larger than
// field_dir.go's dirPickerRows/field_worktree.go's basePickerRows (4)
// since an assigned-issue list is the field most likely to have enough
// rows to make a taller window worth showing (spec §10: up to 50 issues
// per fetch) -- most visible at the "issue-picker-120x40" golden frame
// the brief names explicitly.
const issuePickerRows = 6

const issueLabel = "Issue: "

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

const issueNoneLabel = "None (manual entry)"

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
//
// IssueField renders at a CONSTANT 2+issuePickerRows physical lines
// regardless of focus, issue set, or filter text (this task's own
// "verified fact": Section.Height must be hint-independent) -- a header
// row (label + typed filter/selection), an always-reserved hint row, then
// issuePickerRows candidate rows (blank while unfocused, matching
// field_dir.go's DirField.View convention).
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

	hint string
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

// Enabled reports that, once constructed, Issue always takes a focus stop
// -- the "rendered only when Linear is configured" gate (spec §6 field 1)
// is a STATIC precondition the app layer applies by simply not
// constructing/including this Section at all (matching form.go's own
// Section doc comment: "Fields whose precondition is static at startup
// ... are simply not rendered"), not something IssueField itself decides
// dynamically.
func (f *IssueField) Enabled() bool { return true }

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

// Hint sets the text shown on the always-reserved hint row.
func (f *IssueField) Hint(s string) { f.hint = s }

// Height reports IssueField's constant footprint -- independent of winH,
// focus, issue set, or filter text (see the type doc comment).
func (f *IssueField) Height(int) int { return 2 + issuePickerRows }

// View renders the field at exactly Height's own physical line count.
func (f *IssueField) View(inner int) string {
	if inner < 1 {
		inner = 1
	}
	header := f.renderHeader(inner)
	hintLine := fitLine(dimHint(f.palette).Render(f.hint), inner)

	var rows string
	if f.focused {
		rows = f.picker.View(inner, issuePickerRows)
	} else {
		blanks := make([]string, issuePickerRows)
		for i := range blanks {
			blanks[i] = fitLine("", inner)
		}
		rows = strings.Join(blanks, "\n")
	}

	return header + "\n" + hintLine + "\n" + rows
}

// renderHeader renders the label plus, while focused, the raw typed
// filter text, or, while unfocused, the resolved current selection --
// matching field_dir.go's DirField.renderHeader convention.
func (f *IssueField) renderHeader(inner int) string {
	labelStyled := lipgloss.NewStyle().Foreground(f.palette.Text).Render(issueLabel)
	budget := inner - lipgloss.Width(issueLabel)
	if budget < 1 {
		budget = 1
	}

	var body string
	switch {
	case f.focused:
		body = f.input.View(budget)
	case f.Selected() == nil:
		body = fitLine(dimHint(f.palette).Render(issueNoneLabel), budget)
	default:
		sel := f.Selected()
		body = fitLine(lipgloss.NewStyle().Foreground(f.palette.Text).Render(sel.Identifier+" · "+sel.Title), budget)
	}

	return fitLine(labelStyled+body, inner)
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
