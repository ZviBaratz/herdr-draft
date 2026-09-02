// field_prompt.go is written fresh for this task -- it wraps
// widgets.PromptArea (Task 15, an independent implementation against
// charm.land/bubbles/v2's textarea, not derived from atrium; see
// widgets/textarea.go's own file doc), the same "thin Section adapter
// around an existing widget" shape field_placement.go's PlacementField
// applies to widgets.ChipRow.
package form

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/ZviBaratz/herdr-draft/internal/form/widgets"
	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

const (
	// promptRowLabel is v2's row label (v2 spec §6).
	promptRowLabel = "prompt"
	// promptRowEmpty is v2 spec §6's Unset cell for this row: an em dash,
	// not the placeholder ladder, because the ladder teaches you what to
	// TYPE and the row is not where you type (the panel is).
	promptRowEmpty = "—"

	// promptPanelMinRows / promptPanelMaxRows bound the textarea's own
	// panel: enough rows to be worth focusing even for a one-line prompt,
	// capped so a very tall window does not hand a 40-row textarea to a
	// field most sessions leave empty.
	promptPanelMinRows = 6
	promptPanelMaxRows = 20
)

// promptPlaceholderLadder is spec §6 item 8's own placeholder ladder,
// forked (per the brief's own "fork of placeholder per spec §6.8"
// wording) from the spec's two named endpoints -- the most descriptive
// entry and the bare fallback -- with two intermediate steps added so
// widgets.PromptArea's own selectPlaceholder (picking the widest entry
// that still fits) has more than a single all-or-nothing jump to choose
// between as the field's available width shrinks.
var promptPlaceholderLadder = []string{
	"Optional — sent to the agent once it starts (Enter or Tab to skip)",
	"Optional — sent once the agent starts",
	"Optional prompt",
	"Optional",
}

// PromptField is the form's Prompt Section (spec §6 field 8): an optional
// multi-line textarea, delivered post-launch via `herdr agent prompt
// --wait` (spec §9 step 3) -- this package has no opinion on delivery,
// only on collecting the text.
//
// PromptField renders a one-line label header plus the wrapped
// PromptArea's own rows: spec §6 field 8's "4 rows preferred, 1 floor",
// scaled to whatever the panel's own region can afford (Panel's h).
// Height stays independent of focus and content, per Section.Height's own
// contract -- PromptArea.View already renders exactly its configured row
// count regardless of what has been typed into it (see its own doc
// comment), so that holds here trivially.
//
// This is where PromptArea.SetRows -- the shrink hook Task 15 built and
// nothing ever called -- is finally driven: View applies it per render,
// from the height it was allocated.
type PromptField struct {
	palette theme.Palette
	area    *widgets.PromptArea
	focused bool
	touched bool
}

// NewPromptField returns an empty, blurred PromptField at
// widgets.PromptAreaPreferredRows, styled from palette, with
// promptPlaceholderLadder already installed.
func NewPromptField(palette theme.Palette) *PromptField {
	area := widgets.NewPromptArea(palette)
	area.SetPlaceholderLadder(promptPlaceholderLadder)
	return &PromptField{palette: palette, area: area}
}

// ID identifies this Section for form.go's zoneFor.
func (f *PromptField) ID() string { return "prompt" }

// Enabled reports that Prompt is always present -- spec §6 field 8 has no
// precondition that could ever make it unavailable (it is optional
// CONTENT, not an optional FIELD).
func (f *PromptField) Enabled() bool { return true }

// Focus gives the field input focus, returning the wrapped PromptArea's
// own cursor-blink tea.Cmd -- form.go's own documented deviation exists
// specifically for this widget (see form.go's Section doc comment: "A
// Section wrapping PromptArea ... has no other channel to hand that Cmd
// back to this package's focus ring").
func (f *PromptField) Focus() tea.Cmd {
	f.focused = true
	return f.area.Focus()
}

// Blur removes input focus.
func (f *PromptField) Blur() {
	f.focused = false
	f.area.Blur()
}

// Update forwards msg to the wrapped PromptArea -- see keys.go's own
// grammar boundary: MapKey already intercepts Tab/Shift+Tab/Enter/
// Ctrl+J/Shift+Enter/Alt+Enter/Esc/Ctrl+C/Ctrl+S/Ctrl+R for ZonePrompt
// before this is ever called (including, critically, a bare Enter --
// widgets/textarea.go's own doc comment warns that forwarding a raw Enter
// to Update would let bubbles' own DefaultKeyMap swallow it as a newline,
// defeating "bare Enter in the prompt zone advances"), so only genuine
// text-editing messages reach here. touched is set to true only when this
// call actually CHANGES the value -- the same before/after Value()
// discipline field_title.go's TitleField.Update and
// field_worktree.go's worktreeBranchSection.Update use, so a non-edit
// message (e.g. a cursor-blink tick) never spuriously flips Touched().
// A tea.MouseWheelMsg (task 21: "scroll the focused picker or the
// prompt") is intercepted first and never reaches f.area.Update at all:
// it scrolls via PromptArea's own ScrollUp/ScrollDown (bubbles/v2's
// textarea.Model.CursorUp/CursorDown, which moves the cursor's LINE, not
// the text content) rather than editing anything, so it must never flip
// Touched() the way a genuine edit does.
func (f *PromptField) Update(msg tea.Msg) tea.Cmd {
	if wheel, ok := msg.(tea.MouseWheelMsg); ok {
		switch wheelDelta(wheel) {
		case -1:
			f.area.ScrollUp()
		case 1:
			f.area.ScrollDown()
		}
		return nil
	}

	before := f.area.Value()
	cmd := f.area.Update(msg)
	if f.area.Value() != before {
		f.touched = true
	}
	return cmd
}

// InsertNewline implements form.go's newliner capability (ZonePrompt is
// the only zone MapKey ever returns ActionNewline for): inserts a literal
// newline at the cursor via the wrapped PromptArea's own InsertNewline,
// which -- per its own doc comment -- deliberately bypasses Update. touched
// is set directly here too (Update's own before/after comparison never
// runs for this path, since MapKey's ActionNewline calls this instead of
// forwarding to Update -- see form.go's handleKey), otherwise a
// newline-only edit would leave Touched() incorrectly false.
func (f *PromptField) InsertNewline() {
	f.area.InsertNewline()
	f.touched = true
}

// Value returns the textarea's current text.
func (f *PromptField) Value() string { return f.area.Value() }

// Touched reports whether the user has edited this field (typed or
// inserted a newline) since construction -- never reset once true, mirroring
// field_title.go's TitleField.Touched().
func (f *PromptField) Touched() bool { return f.touched }

// SetValue replaces the textarea's text, honoring the same
// touched-vs-preselected rule field_worktree.go's WorktreeField.SetBranch
// and field_title.go's TitleField.SetTitle document: when seeded is true,
// this is a SUGGESTION (e.g. a chosen Linear issue's own prompt template,
// spec §10) applied only if the user has not yet edited the field
// themselves (Touched() == false); seeded == false is a hard, authoritative
// set that always applies and clears touched.
//
// The seeded parameter is added in Task 20 (the app layer) -- see
// field_title.go's SetTitle doc comment for the fuller writeup of why this
// field, like Title, needed touched-respecting seeding it didn't originally
// have.
func (f *PromptField) SetValue(s string, seeded bool) {
	if seeded && f.touched {
		return
	}
	f.area.SetValue(s)
	if !seeded {
		f.touched = false
	}
}

// --- the row and its panel ------------------------------------------------

// Label is v2's row label (v2 spec §6's field table).
func (f *PromptField) Label() string { return promptRowLabel }

// Row summarizes a multi-line value on one line: the first non-blank
// line, elided at its TAIL (prose reads left to right), followed by a dim
// " +N more" naming how many further non-blank lines the panel holds. An
// empty prompt reads as a dim em dash.
//
// N counts non-blank lines only, deliberately: a value ending in a
// newline would otherwise claim "+1 more" for a line with nothing on it.
//
// The suffix's width is subtracted BEFORE the first line is elided, so
// the count is never the thing that gets cut -- losing it would turn a
// summary into what looks like the whole value.
func (f *PromptField) Row(w int) string {
	if w < 1 {
		w = 1
	}
	first, more := promptSummary(f.area.Value())
	if first == "" {
		return fitLine(dimText(f.palette).Render(promptRowEmpty), w)
	}

	suffix := ""
	if more > 0 {
		suffix = dimText(f.palette).Render(" +" + strconv.Itoa(more) + " more")
	}
	body := lipgloss.NewStyle().Foreground(f.palette.Text).
		Render(keepHead(first, w-lipgloss.Width(suffix)))
	return fitLine(body+suffix, w)
}

// promptSummary splits value into the first non-blank line and the count
// of non-blank lines after it -- Row's whole content model, factored out
// so it can be pinned directly by a table test.
func promptSummary(value string) (first string, more int) {
	for _, line := range strings.Split(value, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if first == "" {
			first = line
			continue
		}
		more++
	}
	return first, more
}

// Panel is the textarea itself, indented into the panel's own gutter and
// sized to exactly the h rows the layout kept for it. SetRows is applied
// here per render for the same reason View applies it: the widget caches
// no geometry of its own.
func (f *PromptField) Panel(w, h int) string {
	if h < 1 {
		h = 1
	}
	f.area.SetRows(h)
	inner := panelInner(w)
	rendered := strings.Split(f.area.View(inner), "\n")
	lines := make([]string, 0, h)
	for i := 0; i < h && i < len(rendered); i++ {
		lines = append(lines, panelText(rendered[i], w))
	}
	return panelBlock(w, h, lines...)
}

// PanelRows grows with the text: enough rows for the whole prompt plus
// one to type the next line into, never fewer than promptPanelMinRows and
// never more than promptPanelMaxRows.
func (f *PromptField) PanelRows() int {
	lines := strings.Count(f.area.Value(), "\n") + 1
	want := lines + 1
	if want < promptPanelMinRows {
		want = promptPanelMinRows
	}
	return capRows(want, promptPanelMaxRows)
}
