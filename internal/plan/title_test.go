package plan

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// TestCutTitle pins spec §6 field 3's cap as the form applies it: the first
// TitleMaxRunes runes, counted as runes rather than bytes, cut wherever the
// last one falls. There is no word-boundary rule, because the title row has
// none (#176's decision 2).
func TestCutTitle(t *testing.T) {
	if TitleMaxRunes != 32 {
		t.Fatalf("TitleMaxRunes = %d, want spec §6 field 3's 32", TitleMaxRunes)
	}
	for _, tc := range []struct {
		name, title, want string
	}{
		{"empty", "", ""},
		{"short is untouched", "fix login redirect loop", "fix login redirect loop"},
		{"exactly the cap is untouched", strings.Repeat("a", 32), strings.Repeat("a", 32)},
		{"one over loses one", strings.Repeat("a", 33), strings.Repeat("a", 32)},
		{"cut mid-word, not back to a space",
			"fix login redirect loop whereupon the cookie expires",
			"fix login redirect loop whereupo"},
		{"runes, not bytes",
			"Fix café login redirect loop when the cookie expires",
			"Fix café login redirect loop whe"},
		{"whitespace is the title's own, and kept", "  spaced  ", "  spaced  "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := CutTitle(tc.title)
			if got != tc.want {
				t.Errorf("CutTitle(%q) = %q, want %q", tc.title, got, tc.want)
			}
			if n := utf8.RuneCountInString(got); n > TitleMaxRunes {
				t.Errorf("CutTitle(%q) is %d runes, over the cap", tc.title, n)
			}
		})
	}
}

// TestSanitizeTitle pins the form's title-row rule (#178): bubbles'
// textinput turns a tab and each CR or LF into one space, and drops every
// other control character along with U+FFFD, which is what an invalid
// UTF-8 byte decodes to. Everything else is kept, including characters
// that are invisible but not controls.
func TestSanitizeTitle(t *testing.T) {
	for _, tc := range []struct {
		name, title, want string
	}{
		{"empty", "", ""},
		{"plain is untouched", "fix login redirect loop", "fix login redirect loop"},
		{"a tab becomes a space", "fix\tlogin", "fix login"},
		{"a line break becomes a space", "line one\nline two", "line one line two"},
		{"CR and LF are a space each", "fix\r\nlogin", "fix  login"},
		{"BEL is dropped", "fix\alogin", "fixlogin"},
		{"ESC is dropped and its sequence's text kept", "fix \x1b[7mlogin\x1b[0m", "fix [7mlogin[0m"},
		{"DEL is dropped", "fix\x7flogin", "fixlogin"},
		{"a C1 control is dropped, NEL included", "fix\u0085login", "fixlogin"},
		{"an invalid UTF-8 byte is dropped", "fix\xfflogin", "fixlogin"},
		{"a literal U+FFFD is dropped too", "fix\ufffdlogin", "fixlogin"},
		{"a format character is not a control, and is kept", "fix\u200dlogin", "fix\u200dlogin"},
		{"non-ASCII is kept", "Fix café login", "Fix café login"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := SanitizeTitle(tc.title); got != tc.want {
				t.Errorf("SanitizeTitle(%q) = %q, want %q", tc.title, got, tc.want)
			}
		})
	}
}

// TestSanitizePrompt pins #183's rule for what reaches `herdr agent
// prompt`: nothing herdr would type as a key survives, and nothing a
// prompt is written with is lost. The tab row is there so a later parity
// fix for #183's other half changes it on purpose rather than by accident.
func TestSanitizePrompt(t *testing.T) {
	for _, tc := range []struct {
		name, prompt, want string
	}{
		{"empty", "", ""},
		{"plain is untouched", "fix the login redirect loop", "fix the login redirect loop"},
		{"a line break is kept", "line one\nline two", "line one\nline two"},
		{"a blank line is kept", "para one\n\npara two", "para one\n\npara two"},
		{"a tab is kept", "func main() {\n\treturn\n}", "func main() {\n\treturn\n}"},
		{"a CRLF is one line break", "line one\r\nline two\r\n", "line one\nline two\n"},
		{"a lone CR is a line break", "line one\rline two", "line one\nline two"},
		{"CR CR LF is two", "line one\r\r\nline two", "line one\n\nline two"},
		{"the paste terminator loses its ESC", "look\x1b[201~ here", "look[201~ here"},
		{"shift+tab loses its ESC", "look\x1b[Z here", "look[Z here"},
		{"BEL is dropped", "fix\alogin", "fixlogin"},
		{"DEL is dropped", "fix\x7flogin", "fixlogin"},
		{"a C1 control is dropped, NEL and CSI included", "fix\u0085\u009blogin", "fixlogin"},
		{"an invalid UTF-8 byte is dropped", "fix\xfflogin", "fixlogin"},
		{"a literal U+FFFD is dropped too", "fix�login", "fixlogin"},
		{"a line separator is not a control, and is kept", "fix login", "fix login"},
		{"non-ASCII is kept", "Fix café login", "Fix café login"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := SanitizePrompt(tc.prompt); got != tc.want {
				t.Errorf("SanitizePrompt(%q) = %q, want %q", tc.prompt, got, tc.want)
			}
		})
	}
}

// TestSanitizePromptKeepsNoOtherControlForEveryRune takes the table above
// to all of Unicode: whatever a prompt holds, the only control characters
// left are LF and TAB.
func TestSanitizePromptKeepsNoOtherControlForEveryRune(t *testing.T) {
	var b strings.Builder
	for r := rune(0); r <= utf8.MaxRune; r++ {
		b.WriteRune(r)
	}
	for _, r := range SanitizePrompt(b.String()) {
		if r == '\n' || r == '\t' {
			continue
		}
		if unicode.IsControl(r) || r == utf8.RuneError {
			t.Fatalf("SanitizePrompt kept %U", r)
		}
	}
}
