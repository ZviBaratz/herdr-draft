// PromptArea is an independent implementation, written directly against
// charm.land/bubbles/v2's textarea package (its exported API and doc
// comments, read from the actual v2.1.1 module source under
// $GOPATH/pkg/mod/charm.land/bubbles/v2@v2.1.1/textarea/textarea.go) and
// spec §6 item 8 -- it is NOT derived from atrium
// (github.com/ZviBaratz/atrium). Atrium's own textInput.go
// (ui/overlay/textInput.go), the file that would be the natural porting
// source for a prompt/title text widget, is explicitly NOT on the audited
// clean-file list (spec §14: only 118 of 172 surviving lines are
// Zvi-authored; 54 are third-party) and was never opened while writing
// this file, per the task-15 brief's provenance guardrail. Only
// textInput_keys.go (ported into ../keys.go, a different package) was read
// from Atrium for this task.
//
// PromptArea wraps a single bubbles/v2 textarea.Model: a fixed-height (4
// rows preferred, 1 row floor -- PromptAreaPreferredRows/PromptAreaMinRows,
// spec §6 item 8), width-laddered-placeholder text field. Like Picker and
// ChipRow (picker.go, chiprow.go), it owns its business state (the
// textarea's content, cursor, and focus) but takes width fresh on every
// View call rather than caching it -- the same "no global renderer state"
// convention, and colors come from an injected theme.Palette rather than
// any hardcoded style, for the same reason those two widgets do it (see
// the package doc).
//
// Grammar boundary: this file has no opinion on what a keypress *means* --
// that is ../keys.go's job (MapKey). PromptArea only exposes the primitives
// a caller needs to act on a KeyAction once decided: Update forwards
// anything the grammar layer reports as ActionNone (not part of the
// grammar) straight to the wrapped textarea.Model's own editing behavior
// (typing, arrow movement, word-delete, ...), and InsertNewline is the
// explicit hook for ActionNewline (⌃J/⇧↵/⌥↵ in the prompt zone). This
// split matters concretely for Enter: bubbles/v2 textarea's own
// DefaultKeyMap binds a bare "enter" (and "ctrl+m") to InsertNewline
// (verified directly against DefaultKeyMap in the vendored source, not
// assumed) -- so a caller MUST route every keypress through MapKey first
// and never forward a raw Enter to Update, or the grammar's "bare Enter in
// the prompt zone advances" rule (spec §6) would be silently defeated by
// the widget swallowing it as a newline first. See
// TestPromptArea_RawUpdateEnterInsertsNewline for a pinned demonstration.
package widgets

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

const (
	// PromptAreaPreferredRows is PromptArea's preferred height (spec §6
	// item 8: "optional textarea (4 rows preferred, 1 floor)"). A freshly
	// constructed PromptArea starts at this height; task 16's form
	// degradation ladder may shrink it via SetRows as the popup gets
	// tight on vertical space.
	PromptAreaPreferredRows = 4
	// PromptAreaMinRows is the shortest height SetRows will ever apply --
	// the prompt field is optional and may shrink, but per spec §6 it is
	// never fully hidden the way a field whose precondition goes false
	// mid-session goes present-but-inert instead of absent.
	PromptAreaMinRows = 1
)

// PromptArea is herdr-draft's prompt textarea field (spec §6 item 8),
// wrapping charm.land/bubbles/v2's textarea.Model. Unlike Picker/ChipRow,
// it has no stored palette field: their styles are recomputed from it on
// every View call, but PromptArea's palette-derived textarea.Styles are
// baked into ta once at construction (see NewPromptArea/paletteStyles) and
// read back out of ta itself thereafter, so there is nothing left in this
// struct that would ever consult a stored palette again.
type PromptArea struct {
	ta   textarea.Model
	rows int

	// ladder holds the placeholder candidates SetPlaceholderLadder was
	// last given, ordered by nothing in particular -- selectPlaceholder
	// picks the widest-fitting one by measured width on every View call,
	// not by ladder order, so a caller does not have to pre-sort it (see
	// selectPlaceholder's doc).
	ladder []string
}

// NewPromptArea returns an empty, blurred PromptArea at
// PromptAreaPreferredRows, styled from palette. Colors are applied once
// here, matching Picker/ChipRow's injected-palette idiom (no SetPalette
// setter exists on any of the three widgets); a form that wants to react
// to a live palette change (spec §16 non-goal 8: herdr-draft does not do
// this in v1) would need to reconstruct the widget.
func NewPromptArea(palette theme.Palette) *PromptArea {
	ta := textarea.New()
	// No left-hand prompt glyph and no line-number gutter: both consume
	// width computed into textarea's own reservedInner (see SetWidth in
	// the vendored source), which would make the width this widget's
	// caller passes to View not match the width available for placeholder
	// text 1:1. Zeroing both keeps View(width)'s effective typing/
	// placeholder width equal to width itself, so selectPlaceholder's
	// lipgloss.Width(candidate) <= width comparison is exact rather than
	// needing to replicate bubbles' own reserved-width arithmetic here.
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.SetStyles(paletteStyles(palette))
	ta.SetHeight(PromptAreaPreferredRows)

	return &PromptArea{
		ta:   ta,
		rows: PromptAreaPreferredRows,
	}
}

// paletteStyles builds a textarea.Styles from palette: Text uses
// palette.Text focused / palette.DimText blurred (dimming on blur mirrors
// bubbles' own DefaultStyles convention), Placeholder is always
// palette.DimText and italic, and the cursor uses palette.Accent. No style
// here sets Background or Width/Height -- backgrounds are the form root's
// job to paint across the whole popup (task 16's brief step 1: "panel bg
// painted explicitly across the full popup area"), matching Picker/
// ChipRow's own styles (Foreground only, see picker.go/chiprow.go), and
// bubbles' own StyleState.computedText()/computedPlaceholder()/etc. append
// Inline(true) unconditionally (verified in the vendored source) regardless
// of what a caller-supplied Style sets, so the Task 14 word-wrap-before-
// truncate footgun (widthStyle's doc comment in picker.go) cannot recur
// through a custom Width set here even if one were added later.
func paletteStyles(palette theme.Palette) textarea.Styles {
	placeholder := lipgloss.NewStyle().Foreground(palette.DimText).Italic(true)
	endOfBuffer := lipgloss.NewStyle().Foreground(palette.DimText)

	return textarea.Styles{
		Focused: textarea.StyleState{
			Base:        lipgloss.NewStyle(),
			CursorLine:  lipgloss.NewStyle().Foreground(palette.Text),
			EndOfBuffer: endOfBuffer,
			Placeholder: placeholder,
			Text:        lipgloss.NewStyle().Foreground(palette.Text),
		},
		Blurred: textarea.StyleState{
			Base:        lipgloss.NewStyle(),
			CursorLine:  lipgloss.NewStyle().Foreground(palette.DimText),
			EndOfBuffer: endOfBuffer,
			Placeholder: placeholder,
			Text:        lipgloss.NewStyle().Foreground(palette.DimText),
		},
		Cursor: textarea.CursorStyle{
			Color:      palette.Accent,
			Shape:      tea.CursorBlock,
			Blink:      true,
			BlinkSpeed: 500 * time.Millisecond,
		},
	}
}

// SetRows sets the textarea's height, flooring at PromptAreaMinRows -- the
// hook task 16's constant-height section budget / degradation ladder
// drives as the popup's available height changes. Never call this with a
// value below PromptAreaMinRows expecting it to stick; it is silently
// raised to the floor instead of panicking or collapsing to zero rows.
func (p *PromptArea) SetRows(rows int) {
	if rows < PromptAreaMinRows {
		rows = PromptAreaMinRows
	}
	p.rows = rows
	p.ta.SetHeight(rows)
}

// SetPlaceholderLadder stores ladder, a set of placeholder strings of
// varying descriptiveness (spec §6 item 8's example: from "Optional --
// sent to the agent once it starts (Enter or Tab to skip)" down to bare
// "Optional"). The actual choice of which one to display happens lazily
// inside View, since width is only ever known there (see the package doc);
// SetPlaceholderLadder itself does no rendering.
func (p *PromptArea) SetPlaceholderLadder(ladder []string) {
	p.ladder = ladder
}

// selectPlaceholder returns the entry in ladder with the greatest rendered
// width that is still <= width, so the caller sees the most descriptive
// placeholder the available space can hold. It does not assume ladder is
// pre-sorted by length in either direction -- every candidate's width is
// measured and compared directly -- so a caller may list entries in
// whatever order reads best in its own source. When nothing fits (width is
// narrower than even the shortest candidate), it falls back to the
// narrowest entry rather than the first or the widest: spec §6's own ladder
// example ends in a bare "Optional" specifically so there is always a
// last-resort entry meant to fit almost any width the form would actually
// budget for this field, and showing the narrowest available text
// overflows the least when even that does not fit. An empty ladder returns
// "".
func selectPlaceholder(ladder []string, width int) string {
	if len(ladder) == 0 {
		return ""
	}

	narrowest := ladder[0]
	narrowestWidth := lipgloss.Width(narrowest)

	var bestFit string
	bestFitWidth := -1 // -1: no candidate has fit yet.

	for _, candidate := range ladder {
		w := lipgloss.Width(candidate)
		if w < narrowestWidth {
			narrowest, narrowestWidth = candidate, w
		}
		if w <= width && w > bestFitWidth {
			bestFit, bestFitWidth = candidate, w
		}
	}

	if bestFitWidth == -1 {
		return narrowest
	}
	return bestFit
}

// View renders the textarea into exactly p.rows lines (PromptAreaPreferredRows
// by default, or whatever SetRows last set), width cells wide. width <= 0
// renders p.rows empty lines rather than panicking or leaving content
// unclipped -- matching Picker.View/ChipRow.View's degenerate-dimension
// contract (see widthStyle's doc comment in picker.go). SetWidth/SetHeight
// are applied fresh on every call (no cached width field), the same
// per-call-dimensions convention Picker and ChipRow use; bubbles' own
// textarea.Model.View already produces a fixed p.rows-line block regardless
// of content length (it pads short content with blank end-of-buffer rows
// and windows longer content through its internal viewport, both verified
// in the vendored source's view()/View() -- see the package doc), so unlike
// Picker/ChipRow this widget does not need its own width/MaxWidth/Inline
// wrapper on top: see paletteStyles' doc comment for why that footgun does
// not apply here.
func (p *PromptArea) View(width int) string {
	if width <= 0 {
		return strings.Join(make([]string, p.rows), "\n")
	}

	p.ta.SetWidth(width)
	p.ta.SetHeight(p.rows)
	p.ta.Placeholder = selectPlaceholder(p.ladder, width)

	return p.ta.View()
}

// Value returns the textarea's current text.
func (p *PromptArea) Value() string {
	return p.ta.Value()
}

// SetValue replaces the textarea's text, e.g. to seed the prompt from a
// Linear issue template (spec §10) or restore a draft.
func (p *PromptArea) SetValue(s string) {
	p.ta.SetValue(s)
}

// InsertNewline inserts a literal newline at the cursor without going
// through Update -- the hook a caller invokes when keys.go's MapKey
// reports ActionNewline (⌃J/⇧↵/⌥↵ in the prompt zone). It bypasses Update
// deliberately: a caller must never forward the originating keypress to
// Update instead (see the package doc's Grammar boundary section for why
// that would be wrong for a bare Enter specifically).
func (p *PromptArea) InsertNewline() {
	p.ta.InsertRune('\n')
}

// ScrollUp moves the cursor up one visual line -- task 21's mouse-wheel
// scroll (wheel up over the focused prompt), delegating to bubbles/v2's
// own textarea.Model.CursorUp (verified exported in the vendored v2.1.1
// source: moves the cursor one visual line up, which also scrolls the
// wrapped textarea's own internal viewport once the cursor leaves the
// visible window -- there is no separate "scroll without moving the
// cursor" primitive on bubbles' own Model, so this, like InsertNewline
// above, bypasses Update rather than synthesizing a keypress).
func (p *PromptArea) ScrollUp() {
	p.ta.CursorUp()
}

// ScrollDown is ScrollUp's opposite (wheel down), delegating to bubbles/
// v2's own textarea.Model.CursorDown.
func (p *PromptArea) ScrollDown() {
	p.ta.CursorDown()
}

// Focus gives the textarea input focus (a blinking cursor, and Update
// starts accepting keystrokes instead of ignoring them) and returns the
// tea.Cmd that starts the cursor's blink loop -- bubbles' own Model.Focus
// contract, passed through unchanged. Unlike Picker/ChipRow (which own no
// bubbletea.Program-visible command at all), PromptArea wraps a widget that
// does, so the caller (task 16's focus ring, moving focus onto this zone)
// needs to fold this into whatever tea.Cmd its own Update returns -- a
// dropped blink command would still function correctly, just render a
// static cursor instead of a blinking one until the next keystroke's own
// Update call incidentally restarts it.
func (p *PromptArea) Focus() tea.Cmd {
	return p.ta.Focus()
}

// Blur removes input focus.
func (p *PromptArea) Blur() {
	p.ta.Blur()
}

// Focused reports whether the textarea currently has input focus.
func (p *PromptArea) Focused() bool {
	return p.ta.Focused()
}

// Update forwards msg to the wrapped textarea.Model, returning whatever
// tea.Cmd it produces (e.g. the cursor blink tick). Callers must only pass
// messages keys.go's MapKey has already classified as ActionNone (not part
// of the form grammar) or as a genuine bubbletea.Model.Update message class
// like tea.PasteMsg the grammar layer never intercepts -- see the package
// doc's Grammar boundary section.
func (p *PromptArea) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	p.ta, cmd = p.ta.Update(msg)
	return cmd
}
