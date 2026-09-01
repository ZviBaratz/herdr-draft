// fuzzy.go is an independent implementation, NOT derived from atrium
// (github.com/ZviBaratz/atrium). Atrium's own directory-picker fragment
// matching (ui/overlay/directoryPicker.go's rankCandidates, and the
// path-mode branch's own fuzzy.Rank call) is backed by
// github.com/ZviBaratz/atrium/internal/fuzzy, a package that is NOT on
// this task's audited clean-file list and was never opened while writing
// this file. Task 17's own brief calls this out explicitly: "Write a
// fresh subsequence ranker (fzf-style: subsequence match, rank by match
// tightness/earliness) and plug it into DirField's filtering."
//
// The algorithm below is the well-known two-pass fzf "v1" approach
// (described in fzf's own README/source, not any Atrium file): a forward
// greedy pass finds *an* occurrence of query as a subsequence of
// candidate, fixing the match's rightmost (last-matched) rune; a backward
// pass then re-anchors each query rune, walking right to left from that
// fixed end, to the LATEST possible position at or before the
// previously-found one, which tightens the match window's start without
// changing its end. This gives a deterministic, reasonably tight (though
// not globally minimal -- true minimality needs the fuller DP fzf v2
// uses) match span cheaply in O(len(candidate) * len(query)) time, with
// no allocation beyond the two rune slices. Ranking then prefers a
// tighter (shorter) span, breaking ties by an earlier start column, and
// finally by the candidate's original position in the input slice (a
// stable sort's own natural tiebreak, made explicit here rather than
// relied on implicitly) -- "rank by match tightness/earliness" per the
// brief.
package form

import (
	"sort"
	"strings"
)

// fuzzyMatch reports whether query occurs as a case-insensitive
// subsequence of candidate, and if so the rune-index span [start, end]
// (inclusive) of the tightened match found by the two-pass algorithm
// described in the package doc. An empty query always matches at [0, -1]
// (a zero-width match at the very start -- see fuzzyRank, which never
// calls this for an empty query in the first place, so the exact span
// returned here is not otherwise observable).
func fuzzyMatch(candidate, query string) (ok bool, start, end int) {
	if query == "" {
		return true, 0, -1
	}

	cand := []rune(strings.ToLower(candidate))
	q := []rune(strings.ToLower(query))

	// Forward greedy pass: the earliest occurrence of each query rune, in
	// turn, at or after the previous match -- fixes `end`, the last
	// matched rune's index.
	from := 0
	end = -1
	for _, qr := range q {
		found := indexOfRuneFrom(cand, qr, from)
		if found < 0 {
			return false, 0, 0
		}
		end = found
		from = found + 1
	}

	// Backward tightening pass: starting from `end`, walk the query in
	// reverse, re-anchoring each rune to the LATEST position at or before
	// the previous (rightward) anchor -- this can only move `start`
	// rightward relative to the forward pass's own first match, never
	// leftward, so it strictly tightens (or leaves unchanged) the window
	// without invalidating the fixed `end`.
	upto := end
	start = end
	for i := len(q) - 1; i >= 0; i-- {
		found := lastIndexOfRuneUpto(cand, q[i], upto)
		start = found
		upto = found - 1
	}

	return true, start, end
}

// indexOfRuneFrom returns the index of the first occurrence of r in s at
// or after from, or -1 if none.
func indexOfRuneFrom(s []rune, r rune, from int) int {
	for i := from; i < len(s); i++ {
		if s[i] == r {
			return i
		}
	}
	return -1
}

// lastIndexOfRuneUpto returns the index of the last occurrence of r in s
// at or before upto, or -1 if none.
func lastIndexOfRuneUpto(s []rune, r rune, upto int) int {
	if upto >= len(s) {
		upto = len(s) - 1
	}
	for i := upto; i >= 0; i-- {
		if s[i] == r {
			return i
		}
	}
	return -1
}

// fuzzyHit pairs a matched candidate with the ranking key fuzzyRank sorts
// on: span (end-start+1, tightness), start (earliness), and the
// candidate's original index (an explicit, documented tiebreak rather
// than an implicit reliance on sort.SliceStable alone).
type fuzzyHit struct {
	text  string
	index int
	span  int
	start int
}

// fuzzyRank returns the subset of candidates that match query as a
// case-insensitive subsequence (see fuzzyMatch), ordered by tightness
// (shorter matched span first), then earliness (earlier match start
// first), then original input order. A non-matching candidate is dropped
// entirely, never merely sorted to the end.
//
// An empty query returns every candidate, unranked, in its original
// order -- the same "no filter" convention widgets.Picker.SetQuery and
// widgets.ChipRow use, so a caller does not need to special-case the
// empty-query call itself.
func fuzzyRank(candidates []string, query string) []string {
	if query == "" {
		return append([]string(nil), candidates...)
	}

	hits := make([]fuzzyHit, 0, len(candidates))
	for i, c := range candidates {
		ok, start, end := fuzzyMatch(c, query)
		if !ok {
			continue
		}
		hits = append(hits, fuzzyHit{text: c, index: i, span: end - start + 1, start: start})
	}

	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].span != hits[j].span {
			return hits[i].span < hits[j].span
		}
		if hits[i].start != hits[j].start {
			return hits[i].start < hits[j].start
		}
		return hits[i].index < hits[j].index
	})

	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.text
	}
	return out
}
