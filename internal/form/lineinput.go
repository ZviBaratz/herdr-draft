// lineInput is an independent implementation, written directly against
// charm.land/bubbles/v2's textinput package (its exported API, read from
// the vendored v2.1.1 module source under
// $GOPATH/pkg/mod/charm.land/bubbles/v2@v2.1.1/textinput/textinput.go) --
// it is NOT derived from atrium (github.com/ZviBaratz/atrium). Atrium's
// own single-line text field lives in ui/overlay/textInput.go, which is
// explicitly NOT on the audited clean-file list (per task 15's own
// provenance note on widgets/textarea.go, the same file this task
// inherits the guardrail from) and was never opened while writing this
// file.
//
// Three of this task's four fields need single-line, in-place-editable
// text: TitleField's own title, and WorktreeField's branch text input; a
// fourth, DirField's fragment/path filter, needs it too. Rather than wire
// bubbles/v2's textinput.Model into each of those three call sites by
// hand (repeating the same width-budget/Inline(true) rendering discipline
// three times), this file factors the wrapping into one small type,
// mirroring widgets/textarea.go's own PromptArea shape one level down (a
// single-line sibling of that multi-line wrapper) -- same "owns its
// business state, takes width fresh on every View call, colors from an
// injected theme.Palette" convention as Picker/ChipRow/PromptArea.
//
// Grammar boundary (mirroring PromptArea's own doc comment): this type has
// no opinion on what a keypress *means* -- keys.go's MapKey already
// intercepts Tab/Shift+Tab/Enter/Alt+Enter/Esc/Ctrl+C/Ctrl+S/Ctrl+R for
// EVERY zone (ZoneTitle, ZoneBranch, and a picker zone's own filter text)
// before a caller's Section.Update is ever reached -- so the only messages
// this type's own Update ever sees are exactly what
// bubbles/v2's DefaultKeyMap expects to handle itself (character
// entry, arrow/word movement, backspace/delete, Ctrl+A/E/K/U, paste).
// Verified directly against textinput's own DefaultKeyMap: Up/Down ARE
// bound (to cycle suggestions), unconditionally, regardless of whether
// ShowSuggestions is set -- a caller that wants Up/Down to mean something
// else in the SAME zone (e.g. DirField/WorktreeField's base picker moving
// its own cursor) must intercept those two keys itself, BEFORE calling
// this type's Update, exactly as field_dir.go and field_worktree.go do.
package form

import (
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// lineInput wraps a single bubbles/v2 textinput.Model.
type lineInput struct {
	ti textinput.Model
	// fill is the background painted across the input's whole width, so
	// an empty input is a visible affordance rather than a stretch of
	// the row it sits on (v3 spec §8.7). It is theme.Palette.InputFill
	// of the GROUND the caller passed to newLineInput, not a flat
	// palette.Surface -- read InputFill's own doc for why the ground has
	// to be named at the call site.
	fill theme.Color
}

// newLineInput returns an empty, blurred lineInput styled from palette,
// filled for the ground it will be drawn on. charLimit <= 0 means no
// limit (textinput's own convention, passed through unchanged).
//
// ground is the background the composed row this input lands in is
// painted with: palette.ActiveRowBG for an input rendered in a stack
// row's value cell (which is every input that is only rendered while its
// field is focused), palette.PanelBG for one rendered inside the detail
// panel. It is a required argument rather than a default because getting
// it wrong is invisible -- a fill that matches its ground renders
// byte-different frames and an identical screen, which is exactly the
// class of defect v3 exists to fix.
func newLineInput(palette theme.Palette, charLimit int, ground theme.Color) *lineInput {
	ti := textinput.New()
	// textinput.New() defaults Prompt to "> " (verified in the vendored
	// source), a shell-style leading glyph that would silently consume
	// width from this caller's own budget -- the identical footgun
	// widgets/textarea.go's own PromptArea.NewPromptArea zeroes
	// ta.Prompt for (see its doc comment: "consume width computed into
	// textarea's own reservedInner... Zeroing... keeps View(width)'s
	// effective typing/placeholder width equal to width itself").
	ti.Prompt = ""
	ti.CharLimit = charLimit
	ti.SetStyles(lineInputStyles(palette))
	return &lineInput{ti: ti, fill: palette.InputFill(ground)}
}

// lineInputStyles builds a textinput.Styles from palette, matching
// widgets/textarea.go's paletteStyles convention one level down: Text uses
// palette.Text focused / palette.DimText blurred, Placeholder is always
// palette.DimText and italic, and the cursor uses palette.Accent.
//
// Still no Background on any span, even now that View paints one. v3 spec
// §8.7 asks for it here too, "belt and braces", and it is not needed:
// PaintLine reasserts the fill after every reset a span leaves behind, and
// a Foreground-only span emits no background SGR of its own to overwrite
// it in between, so the fill is already unbroken across the whole line --
// pinned cell by cell in input_fill_test.go rather than argued. Setting it
// twice would only make every frame carry a second copy of the same color.
func lineInputStyles(palette theme.Palette) textinput.Styles {
	placeholder := lipgloss.NewStyle().Foreground(palette.DimText).Italic(true)
	return textinput.Styles{
		Focused: textinput.StyleState{
			Text:        lipgloss.NewStyle().Foreground(palette.Text),
			Placeholder: placeholder,
		},
		Blurred: textinput.StyleState{
			Text:        lipgloss.NewStyle().Foreground(palette.DimText),
			Placeholder: placeholder,
		},
		Cursor: textinput.CursorStyle{
			Color:      palette.Accent,
			Shape:      tea.CursorBlock,
			Blink:      true,
			BlinkSpeed: 500 * time.Millisecond,
		},
	}
}

// Value returns the input's current text.
func (l *lineInput) Value() string { return l.ti.Value() }

// SetValue replaces the input's text and moves the cursor to the end --
// matching textinput.Model.SetValue's own documented behavior (verified in
// the vendored source: SetValue calls SetCursor(len(m.value)) internally).
// SetValue replaces the text. The cursor is moved to the END, which
// bubbles' own SetValue does NOT do on its own: it only repositions a
// cursor that would be out of bounds (setValueInternal), so replacing
// text with a LONGER string leaves the cursor wherever it was. Every
// caller here is replacing the whole value rather than editing around a
// cursor -- DirField.Complete's Tab-completion most visibly, where a
// stranded cursor made the next keystroke insert into the middle of the
// path just completed (found in live validation, 2026-09-01).
func (l *lineInput) SetValue(v string) {
	l.ti.SetValue(v)
	l.ti.CursorEnd()
}

// SetPlaceholder sets the text shown when Value() == "".
func (l *lineInput) SetPlaceholder(s string) { l.ti.Placeholder = s }

// Focus gives the input focus and returns the cursor's blink tea.Cmd --
// bubbles' own textinput.Model.Focus contract, passed through unchanged
// (see widgets/textarea.go's own PromptArea.Focus doc for why this Cmd
// must not be silently dropped).
func (l *lineInput) Focus() tea.Cmd { return l.ti.Focus() }

// Blur removes input focus.
func (l *lineInput) Blur() { l.ti.Blur() }

// Update forwards msg to the wrapped textinput.Model, returning whatever
// tea.Cmd it produces. See the package doc's Grammar boundary section for
// which messages a caller may pass here.
func (l *lineInput) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	l.ti, cmd = l.ti.Update(msg)
	return cmd
}

// View renders the input into exactly one physical line, clipped/padded to
// width cells (floored at 1) and painted in l.fill -- textinput.Model.View
// already keeps its own typed content on one line via internal horizontal
// scrolling (verified in the vendored source: View computes an
// offset/offsetRight window and never wraps), but paintLine's outer
// Width/MaxWidth/Inline wrap is still applied as the same defensive
// last-resort backstop sizes.go's paintLine and widgets/picker.go's
// widthStyle both use, since this value is composed alongside a caller's
// own label/marker text on the SAME row before that whole row is clipped
// to the field's inner width.
//
// paintLine rather than fitLine is the whole of v3 spec §8.7: it pads and
// clips to exactly width as fitLine did, and additionally reasserts the
// fill after each of the several resets textinput.View embeds (the text
// span, the placeholder span, the cursor span). It also declines to paint
// a lipgloss.NoColor{} at all, which is what leaves the `terminal` theme's
// inputs inheriting the host terminal's own background rather than being
// filled with a fiction -- see paintLine's own doc, and
// TestLineInput_TerminalThemeGetsNoFill.
//
// The composition with the caller's own paint is the same one
// picker_fill_test.go proves for the picker: this fill goes in first, and
// composeRows' outer paintLine then inserts ActiveRowBG (or PanelBG) after
// every reset in a string that already carries this fill after those same
// resets -- so this one is last and wins.
func (l *lineInput) View(width int) string {
	if width < 1 {
		width = 1
	}
	l.ti.SetWidth(width)
	return paintLine(l.ti.View(), width, l.fill)
}
