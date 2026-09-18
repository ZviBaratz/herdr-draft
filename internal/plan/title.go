// title.go is the one place the session title's rules exist: the 32-rune
// cap (spec §6 field 3, "Cap 32 runes") and which characters a title can
// hold. It is in plan for reap.go's reason: internal/form and
// internal/create both import plan already, and they read this ONE
// definition rather than keeping a copy each. The form caps its title row
// with TitleMaxRunes; `create` cleans with SanitizeTitle and cuts with
// CutTitle. Before #176 and #178 only the form did either, so one title
// could build two different sessions.
package plan

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// TitleMaxRunes is the longest session title, in runes: the title row's
// cap (spec §6 field 3). The title names the space, the tab and the agent,
// and a branch derived from it inherits the cap too.
const TitleMaxRunes = 32

// CutTitle returns title's first TitleMaxRunes runes, or title unchanged
// when it already fits. The cut is plain, mid-word if that is where rune 32
// falls, because that is what the form's title row does to a pasted or
// Linear-seeded title (bubbles' textinput CharLimit), and #176 decided the
// two paths cut the same way rather than a better way. Clean a title with
// SanitizeTitle first: the row does, so a dropped character is never
// counted against the cap.
func CutTitle(title string) string {
	runes := []rune(title)
	if len(runes) <= TitleMaxRunes {
		return title
	}
	return string(runes[:TitleMaxRunes])
}

// SanitizeTitle returns title as the form's title row would hold it: a tab
// and each CR or LF become one space, and every other control character is
// dropped, as is U+FFFD -- which is also what each byte of invalid UTF-8
// decodes to, so those go too. Everything else is kept.
//
// The rule is not this repository's. It is bubbles' textinput sanitizer
// (v2.1.1, textinput.go's san(): runeutil's ReplaceTabs(" ") and
// ReplaceNewlines(" ")), which cleans typing, pastes and SetValue alike.
// The row cannot be routed through this function, so
// TestTitleField_KeepsWhatPlanKeeps holds the two to one answer instead,
// and a bubbles upgrade that changes the rule fails there (#178).
//
// herdr would store a control character in a label verbatim and then drop
// it when drawing, so a tab or a line break between two words shows as
// the words run together, and an escape sequence's text is printed without
// its ESC -- measured on herdr 0.9.0 on 2026-09-18.
func SanitizeTitle(title string) string {
	var b strings.Builder
	b.Grow(len(title))
	for _, r := range title {
		switch {
		case r == utf8.RuneError:
			// dropped
		case r == '\t', r == '\r', r == '\n':
			b.WriteByte(' ')
		case unicode.IsControl(r):
			// dropped
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
