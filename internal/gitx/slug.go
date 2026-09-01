package gitx

import (
	"fmt"
	"hash/fnv"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// SanitizeBranch lowercases, strips diacritics, maps every run of
// non-[a-z0-9] to a single '-', and trims leading/trailing '-'.
func SanitizeBranch(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	folded, _, err := transform.String(t, s)
	if err != nil {
		folded = s
	}
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(folded) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func BranchSlug(prefix, title string) string {
	body := SanitizeBranch(title)
	if body == "" {
		h := fnv.New64a()
		h.Write([]byte(title))
		body = fmt.Sprintf("session-%08x", uint32(h.Sum64()))
	}
	return prefix + body
}
