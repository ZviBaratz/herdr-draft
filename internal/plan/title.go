// title.go holds the session title's two rules: the 32-rune cap (spec §6
// field 3, "Cap 32 runes") and which characters a title can hold. It is in
// plan for reap.go's reason: internal/form and internal/create both import
// plan already. The cap is ONE definition both read -- the form's title
// row is built with TitleMaxRunes, and `create` cuts with CutTitle. The
// character rule is not ours: it is bubbles' textinput sanitizer, which
// the row applies itself, so SanitizeTitle is a copy for `create`, held to
// the row by tests rather than shared by a call (see SanitizeTitle).
// Before #176 and #178 only the form applied either rule, so one title
// could build two different sessions.
//
// SanitizePrompt is here too, beside the rule it is modelled on. The
// prompt's is ours rather than bubbles', and its reason is herdr's rather
// than the form's (#183): see its doc comment.
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
// Only TAB, CR and LF become a space. VT, FF and NEL are control
// characters like any other and are dropped, and U+2028/U+2029 are not
// control characters at all, so they are kept -- all as the row does.
//
// The rule is not this repository's. It is bubbles' textinput sanitizer
// (v2.2.1, textinput.go's san(): runeutil's ReplaceTabs(" ") and
// ReplaceNewlines(" ")), which cleans typing, pastes and SetValue alike.
// The row cannot be routed through this function, so two tests in
// internal/form hold the row to it instead (#178):
// TestTitleField_KeepsWhatPlanKeeps over a table that includes invalid
// UTF-8, and TestTitleField_KeepsWhatPlanKeepsForEveryRune over every rune
// there is. A bubbles upgrade that changes how it treats any character
// fails one of them.
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

// SanitizePrompt returns prompt with no control character left but LF and
// TAB: a CRLF or a lone CR becomes one LF, and every other control
// character is dropped -- ESC, the rest of C0, DEL and C1 -- as is U+FFFD,
// which is also what each byte of invalid UTF-8 decodes to. Everything
// else is kept, U+2028 and U+2029 included, since neither is a control
// character.
//
// The reason is where the prompt goes (#183). `herdr agent prompt` types
// the text into the agent's pane between bracketed-paste markers and does
// not escape it, so a `\x1b[201~` inside it ends the paste early and
// whatever follows arrives as keystrokes -- `\x1b[Z` is shift+tab, which
// cycles Claude Code's permission mode. Where the pane has bracketed paste
// off, herdr sends the bytes bare and every ESC is a key. Both are
// encode_api_text,
// https://github.com/herdrdev/herdr/blob/v0.9.0/src/app/api_helpers.rs#L25-L32,
// which `agent prompt` reaches from herdr:src/app/api/agents.rs's
// queue_agent_prompt at v0.9.0. A Linear description is somebody
// else's text, and `create --issue` handed it over byte for byte. That is
// reasoned from herdr's code, not reproduced end to end. Nothing dropped
// is something a prompt could mean: to an agent's input box a control
// character is a key rather than text. LF and TAB are what a prompt is
// written with, so they stay, and a paste carries them as text.
//
// The popup was never exposed: its prompt is a bubbles textarea, whose
// sanitizer (v2.2.1, textarea.go's san()) drops the same characters from
// a paste, from typing and from SetValue. So this is `create`'s rule and
// the Linear-seeded prompt's, and the form's textarea still applies its
// own on top. Two of the textarea's choices are deliberately NOT copied,
// which is where this differs from SanitizeTitle's relationship to the
// title row:
//
//   - A TAB is kept. The textarea turns one into four spaces, which
//     flattens a pasted Go snippet, and whether `create` should follow it
//     or the form should stop is #183's other, still open, half.
//   - A CRLF is ONE line break. The textarea turns CR and LF into a line
//     break each, so a CRLF file gains a blank line under every line; no
//     one writing a prompt means that. A lone CR is a line break too
//     rather than dropped: it is one on old Macs, and dropping it would
//     join two lines into one word.
func SanitizePrompt(prompt string) string {
	var b strings.Builder
	b.Grow(len(prompt))
	for i, r := range prompt {
		switch {
		case r == '\r':
			if i+1 < len(prompt) && prompt[i+1] == '\n' {
				continue // the LF that follows is the line break
			}
			b.WriteByte('\n')
		case r == '\n', r == '\t':
			b.WriteRune(r)
		case r == utf8.RuneError:
			// dropped
		case unicode.IsControl(r):
			// dropped
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
