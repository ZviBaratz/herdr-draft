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

// BranchSlug derives a branch name from a title: SanitizeBranch's slug (or
// a deterministic "session-xxxxxxxx" when the title sanitizes away to
// nothing) with prefix prepended verbatim.
//
// The prefix is NOT sanitized here -- it is a configured value whose "/"
// separators are meaningful, and mangling it would be worse than refusing
// it. Callers must have run it through ValidateBranchPrefix first: the
// result reaches `herdr worktree create --branch <value>` as an argv
// element, so an unchecked prefix is an argument-injection surface.
func BranchSlug(prefix, title string) string {
	body := SanitizeBranch(title)
	if body == "" {
		h := fnv.New64a()
		h.Write([]byte(title))
		body = fmt.Sprintf("session-%08x", uint32(h.Sum64()))
	}
	return prefix + body
}
