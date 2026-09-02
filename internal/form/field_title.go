// field_title.go is written fresh for this task, per the task-17 brief's
// own provenance note ("TitleField and PlacementField are written fresh")
// -- it is NOT derived from atrium (github.com/ZviBaratz/atrium).
// Atrium's own title field lives in ui/overlay/textInput.go, which is not
// on the audited clean-file list and was never opened for this task; only
// this package's own lineInput (lineinput.go, itself written directly
// against bubbles/v2's textinput, not ported from Atrium) is reused here.
package form

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

const (
	// titleCharLimit is spec §6 field 3's "32-rune cap".
	titleCharLimit = 32
	// titleRowLabel is v2's row label (v2 spec §6): lowercase, no colon,
	// no padding -- the form pads it into the label column
	// (rowlayout.go's labelColWidth).
	titleRowLabel = "title"
	// titleRowUnset is v2 spec §6's Unset cell for this row. It matches
	// the v1 placeholder the wrapped lineInput already carries, so the
	// empty field reads the same whether the row is showing the editor or
	// the resting value.
	titleRowUnset = "untitled"
)

// TitleField is the form's Title Section (spec §6 field 3): a single-line,
// 32-rune-capped text field whose typed value doubles as the session's
// branch/title (spec §6's "quick-create" contract, wired through
// form.go's own titleValuer capability so an Enter from a non-empty Title
// submits the form -- see form.go's Section doc comment).
//
// The row is the editor (a title's editing surface is one line, so it
// needs no panel to type in) and the panel is the verdict.
type TitleField struct {
	palette theme.Palette
	input   *lineInput
	focused bool
	touched bool

	// verdictKey/verdictText are SetVerdict's own staleness guard, the
	// same "clears the moment the underlying value changes" discipline
	// field_dir.go's DirField.validityPath uses for its inline marker: a
	// verdict computed for a title the user has since edited away from
	// must stop rendering, without SetVerdict's caller needing a separate
	// Clear call to make that happen -- verdictLine (below) only shows
	// verdictText when verdictKey still equals the CURRENT Value().
	verdictKey  string
	verdictText string
}

// NewTitleField returns an empty, blurred TitleField styled from palette.
func NewTitleField(palette theme.Palette) *TitleField {
	input := newLineInput(palette, titleCharLimit)
	input.SetPlaceholder("untitled")
	return &TitleField{palette: palette, input: input}
}

// ID identifies this Section for form.go's zoneFor (see form.go's Section
// doc comment's ID() convention).
func (f *TitleField) ID() string { return "title" }

// Enabled reports that Title is always present -- spec §6 field 3 has no
// precondition that could ever make it unavailable.
func (f *TitleField) Enabled() bool { return true }

// Focus gives the field input focus, returning the wrapped lineInput's own
// blink tea.Cmd.
func (f *TitleField) Focus() tea.Cmd {
	f.focused = true
	return f.input.Focus()
}

// Blur removes input focus.
func (f *TitleField) Blur() {
	f.focused = false
	f.input.Blur()
}

// Update forwards msg to the wrapped lineInput -- see keys.go's own
// grammar boundary: MapKey already intercepts Tab/Enter/Esc/Ctrl+S/Ctrl+R
// for ZoneTitle before this is ever called, so only genuine text-editing
// messages (typed runes, backspace, arrow/word movement, paste) reach
// here. touched is set to true only when this call actually CHANGES the
// value -- comparing Value() before and after, rather than assuming every
// Update call is an edit -- so a message that moves the cursor without
// changing content (Left/Right/Home/End), or a non-edit message forwarded
// here for some other reason (e.g. a cursor-blink tick), does not
// spuriously flip Touched() -- see field_dir.go's DirField.Update for the
// same discipline applied to picker cursor-reset avoidance.
func (f *TitleField) Update(msg tea.Msg) tea.Cmd {
	before := f.input.Value()
	cmd := f.input.Update(msg)
	if f.input.Value() != before {
		f.touched = true
	}
	return cmd
}

// Value returns the field's current typed text -- also TitleField's
// titleValuer implementation (form.go's optional capability interface),
// consulted by zoneFor for FocusZone.TitleEmpty.
func (f *TitleField) Value() string { return f.input.Value() }

// Touched reports whether the user has typed into this field since
// construction (never reset once true -- there is no untouch operation;
// a caller that rebuilds the form for spec §6's Ctrl+R Ctrl+R clear
// gesture is expected to construct a fresh TitleField instead).
func (f *TitleField) Touched() bool { return f.touched }

// SetTitle sets the input's value, honoring the same touched-vs-preselected
// rule field_worktree.go's WorktreeField.SetBranch documents: when seeded
// is true, this is a SUGGESTION (e.g. a chosen Linear issue's own title)
// applied only if the user has not yet typed into the field themselves
// (Touched() == false) -- once touched, every further seeded call is
// silently ignored, so a later re-suggestion never clobbers the user's own
// edit. seeded == false is a hard, authoritative set that always applies
// and clears touched, so a subsequent seed can apply again.
//
// Added in Task 20 (the app layer) alongside WorktreeField.SetBranch and
// PromptField.SetValue -- IssueChosenMsg's own doc comment already
// documents the app layer calling "TitleField/WorktreeField/PromptField's
// own setters", but Title had no programmatic setter at all until this one;
// see task-20-report.md for the full write-up of this gap and why the fix
// mirrors SetBranch's existing, already-reviewed discipline rather than
// inventing a new one.
func (f *TitleField) SetTitle(v string, seeded bool) {
	if seeded && f.touched {
		return
	}
	f.input.SetValue(v)
	if !seeded {
		f.touched = false
	}
}

// --- the row and its panel ------------------------------------------------
//
// Label is v2's row label (v2 spec §6's field table).
func (f *TitleField) Label() string { return titleRowLabel }

// Row renders the title's value cell: the live editor while focused (v2
// spec §5 -- "the row is the EDITOR for a field whose editing surface is
// a single line"), the typed value otherwise, and a dim "untitled" when
// there is nothing to show. A too-long title elides at its TAIL, keeping
// the head: a title is read left to right and its first words are what
// identify it.
//
// No verdict is appended here, deliberately: v2 spec §6 puts verdicts in
// the panel precisely so a recomputing verdict cannot shift text under
// the cursor.
func (f *TitleField) Row(w int) string {
	if w < 1 {
		w = 1
	}
	if f.focused {
		return f.input.View(w)
	}
	if v := f.Value(); v != "" {
		return fitLine(lipgloss.NewStyle().Foreground(f.palette.Text).Render(keepHead(v, w)), w)
	}
	return fitLine(dimText(f.palette).Render(keepHead(titleRowUnset, w)), w)
}

// Panel renders the verdict line at FULL width -- the whole point of
// moving it off the row. v1 clamped it to 21 cells (titleVerdictMaxCells,
// now deleted) so it could not collide with the typed text sharing its
// section; a full-width line of its own has nothing to collide with, and
// "branch: zvi/fix-login-redirect-loop" survives whole.
//
// A verdict computed for a title the user has since edited away from is
// not rendered at all, the same staleness-by-comparison guard
// verdictKey's own doc comment describes for v1.
func (f *TitleField) Panel(w, h int) string {
	text := ""
	if f.verdictKey == f.Value() {
		text = f.verdictText
	}
	return panelBlock(w, h, panelText(dimHint(f.palette).Render(text), w))
}

// PanelRows is one: the verdict line, whether or not a verdict currently
// applies. Reserving it unconditionally is what keeps the panel's height
// -- and therefore the footer's position -- independent of whether the
// app layer has answered yet.
func (f *TitleField) PanelRows() int { return 1 }

// SetVerdict records the app layer's own live-validation message for the
// title text that was current when it was computed (key): a short note
// (e.g. the branch name a title would produce, or a "title already in
// use" warning) shown on the reserved verdict line. A later call whose key
// no longer matches Value() (the title has since changed) is stored but
// never rendered -- see verdictKey's own doc comment; there is no separate
// Clear method, matching DirField's SetValidity's identical
// staleness-by-comparison design.
func (f *TitleField) SetVerdict(key, text string) {
	f.verdictKey = key
	f.verdictText = text
}
