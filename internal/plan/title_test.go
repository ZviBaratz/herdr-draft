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
