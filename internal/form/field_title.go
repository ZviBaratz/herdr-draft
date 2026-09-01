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
	// titleVerdictMaxCells bounds SetVerdict's displayed text, independent
	// of whatever inner width View is otherwise handed -- the same
	// "reserve the message's columns up front" discipline
	// directoryPicker.go's own selectionMarker doc describes Atrium
	// hitting a real bug over (#545: an unbounded verdict sentence
	// silently truncated by the row's own overflow edge).
	titleVerdictMaxCells = 21

	titleLabel = "Title: "
)

// TitleField is the form's Title Section (spec §6 field 3): a single-line,
// 32-rune-capped text field whose typed value doubles as the session's
// branch/title (spec §6's "quick-create" contract, wired through
// form.go's own titleValuer capability so an Enter from a non-empty Title
// submits the form -- see form.go's Section doc comment).
//
// TitleField renders at a CONSTANT two physical lines regardless of focus
// or whether a verdict is currently set (this task's own "verified fact":
// Section.Height must be hint-independent) -- one line for the label and
// typed text, one always-reserved line for SetVerdict's own message.
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

// View renders the field at exactly Height's own two physical lines: the
// label/typed-text row, then the always-reserved verdict row.
func (f *TitleField) View(inner int) string {
	if inner < 1 {
		inner = 1
	}
	labelStyled := lipgloss.NewStyle().Foreground(f.palette.Text).Render(titleLabel)
	budget := inner - lipgloss.Width(titleLabel)
	if budget < 1 {
		budget = 1
	}
	header := fitLine(labelStyled+f.input.View(budget), inner)
	return header + "\n" + f.verdictLine(inner)
}

// verdictLine renders SetVerdict's own message, bounded to
// titleVerdictMaxCells (or inner, whichever is narrower), or a blank line
// when no verdict currently applies to Value() -- see verdictKey's own doc
// comment. Always exactly one physical line, so Height stays constant
// whether or not a verdict is set.
func (f *TitleField) verdictLine(inner int) string {
	budget := titleVerdictMaxCells
	if inner < budget {
		budget = inner
	}
	text := ""
	if f.verdictKey == f.Value() {
		text = f.verdictText
	}
	clipped := fitLine(dimHint(f.palette).Render(text), budget)
	return fitLine(clipped, inner)
}

// Height reports TitleField's constant two-line footprint -- independent
// of winH, focus, or verdict state (see the type doc comment).
func (f *TitleField) Height(int) int { return 2 }

// Value returns the field's current typed text -- also TitleField's
// titleValuer implementation (form.go's optional capability interface),
// consulted by zoneFor for FocusZone.TitleEmpty.
func (f *TitleField) Value() string { return f.input.Value() }

// Touched reports whether the user has typed into this field since
// construction (never reset once true -- there is no untouch operation;
// a caller that rebuilds the form for spec §6's Ctrl+R Ctrl+R clear
// gesture is expected to construct a fresh TitleField instead).
func (f *TitleField) Touched() bool { return f.touched }

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
