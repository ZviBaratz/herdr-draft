// field_prompt.go is written fresh for this task -- it wraps
// widgets.PromptArea (Task 15, an independent implementation against
// charm.land/bubbles/v2's textarea, not derived from atrium; see
// widgets/textarea.go's own file doc), the same "thin Section adapter
// around an existing widget" shape field_placement.go's PlacementField
// applies to widgets.ChipRow.
package form

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/ZviBaratz/herdr-draft/internal/form/widgets"
	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

const promptLabel = "Prompt: "

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
// PromptField renders at a CONSTANT 1+widgets.PromptAreaPreferredRows
// physical lines regardless of focus or content (this task's own
// "verified fact": Section.Height must be hint-independent, though here
// it holds trivially -- widgets.PromptArea.View already always renders
// exactly its own configured row count, see its own doc comment) -- a
// one-line label header, then the wrapped PromptArea's own rows. Unlike
// field_dir.go's DirField/field_issue.go's IssueField, PromptField never
// calls PromptArea.SetRows: every other Task 17-18 field keeps its own
// Height(winH) constant regardless of winH too (sizes.go's own doc
// comment: the form's ACTUAL degradation mechanism is fitToHeight's
// post-hoc line-dropping cascade over the composed content, not a
// per-Section winH-driven shrink -- see its file doc's "Adaptations"
// section), so there is nothing for a per-winH SetRows call here to do
// that the shared cascade doesn't already handle.
type PromptField struct {
	palette theme.Palette
	area    *widgets.PromptArea
	focused bool
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
// text-editing messages reach here.
func (f *PromptField) Update(msg tea.Msg) tea.Cmd {
	return f.area.Update(msg)
}

// InsertNewline implements form.go's newliner capability (ZonePrompt is
// the only zone MapKey ever returns ActionNewline for): inserts a literal
// newline at the cursor via the wrapped PromptArea's own InsertNewline,
// which -- per its own doc comment -- deliberately bypasses Update.
func (f *PromptField) InsertNewline() { f.area.InsertNewline() }

// Value returns the textarea's current text.
func (f *PromptField) Value() string { return f.area.Value() }

// SetValue replaces the textarea's text, e.g. to seed the prompt from a
// chosen Linear issue's template (spec §10) once the app layer has
// composed it from an IssueChosenMsg -- see field_issue.go's own doc
// comment on why IssueField never calls this itself.
func (f *PromptField) SetValue(s string) { f.area.SetValue(s) }

// Height reports PromptField's constant footprint -- independent of winH
// or content (see the type doc comment).
func (f *PromptField) Height(int) int { return 1 + widgets.PromptAreaPreferredRows }

// View renders the field at exactly Height's own physical line count: a
// one-line label header, then the wrapped PromptArea's own rows.
func (f *PromptField) View(inner int) string {
	if inner < 1 {
		inner = 1
	}
	header := fitLine(lipgloss.NewStyle().Foreground(f.palette.Text).Render(promptLabel), inner)
	return header + "\n" + f.area.View(inner)
}
