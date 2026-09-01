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
}

// newLineInput returns an empty, blurred lineInput styled from palette.
// charLimit <= 0 means no limit (textinput's own convention, passed
// through unchanged).
func newLineInput(palette theme.Palette, charLimit int) *lineInput {
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
	return &lineInput{ti: ti}
}

// lineInputStyles builds a textinput.Styles from palette, matching
// widgets/textarea.go's paletteStyles convention one level down: Text uses
// palette.Text focused / palette.DimText blurred, Placeholder is always
// palette.DimText and italic, and the cursor uses palette.Accent. No
// Background is set here, for the same reason paletteStyles doesn't set
// one -- sizes.go's paintLine paints the panel background across the
// whole composed line, not any individual widget's own style.
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
func (l *lineInput) SetValue(v string) { l.ti.SetValue(v) }

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
// width cells (floored at 1) via fitLine -- textinput.Model.View already
// keeps its own typed content on one line via internal horizontal
// scrolling (verified in the vendored source: View computes an
// offset/offsetRight window and never wraps), but fitLine's outer
// Width/MaxWidth/Inline wrap is still applied as the same defensive
// last-resort backstop sizes.go's paintLine and widgets/picker.go's
// widthStyle both use, since this value is composed alongside a caller's
// own label/marker text on the SAME row before that whole row is clipped
// to the field's inner width.
func (l *lineInput) View(width int) string {
	if width < 1 {
		width = 1
	}
	l.ti.SetWidth(width)
	return fitLine(l.ti.View(), width)
}
