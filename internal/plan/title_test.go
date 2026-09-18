package plan

import (
	"strings"
	"testing"
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
