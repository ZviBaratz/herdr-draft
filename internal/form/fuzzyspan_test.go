// fuzzyspan_test.go covers fuzzyRankSpans' own contribution -- the SPANS.
// The ranking rules it shares with fuzzyRank are pinned in fuzzy_test.go,
// which deliberately did not move when the spans were added: fuzzyRank is
// now expressed over fuzzyRankSpans, and that file passing untouched is
// the evidence the re-expression preserved its behavior.
package form

import "testing"

func TestFuzzyRankSpans_Spans(t *testing.T) {
	cases := []struct {
		name        string
		candidate   string
		query       string
		wantStart   int
		wantEnd     int
		wantMatch   bool
		wantMatched string // the runes [Start,End] name, as a readability check
	}{
		{
			name: "exact prefix", candidate: "herdr-draft", query: "herdr",
			wantStart: 0, wantEnd: 4, wantMatch: true, wantMatched: "herdr",
		},
		{
			name: "whole candidate", candidate: "herdr", query: "herdr",
			wantStart: 0, wantEnd: 4, wantMatch: true, wantMatched: "herdr",
		},
		{
			name: "scattered subsequence", candidate: "herdr-draft", query: "hdd",
			wantStart: 0, wantEnd: 6, wantMatch: true, wantMatched: "herdr-d",
		},
		{
			name: "match starts past the head", candidate: "zzz-herdr", query: "hd",
			wantStart: 4, wantEnd: 7, wantMatch: true, wantMatched: "herd",
		},
		{
			name: "case-insensitive match, span indexes the ORIGINAL", candidate: "Herdr-Draft", query: "hd",
			wantStart: 0, wantEnd: 3, wantMatch: true, wantMatched: "Herd",
		},
		{
			name: "no match is dropped, not spanned", candidate: "herdr", query: "zz",
			wantMatch: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := fuzzyRankSpans([]string{c.candidate}, c.query)
			if !c.wantMatch {
				if len(got) != 0 {
					t.Fatalf("fuzzyRankSpans([%q], %q) = %+v, want no hits", c.candidate, c.query, got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("fuzzyRankSpans([%q], %q) returned %d hits, want 1", c.candidate, c.query, len(got))
			}
			if got[0].Text != c.candidate {
				t.Errorf("Text = %q, want %q", got[0].Text, c.candidate)
			}
			if got[0].Start != c.wantStart || got[0].End != c.wantEnd {
				t.Fatalf("fuzzyRankSpans([%q], %q) span = [%d,%d], want [%d,%d] (%q)",
					c.candidate, c.query, got[0].Start, got[0].End, c.wantStart, c.wantEnd, c.wantMatched)
			}
			if matched := string([]rune(c.candidate)[c.wantStart : c.wantEnd+1]); matched != c.wantMatched {
				t.Errorf("runes [%d,%d] of %q are %q, want %q -- the case is mis-stated, not the code",
					c.wantStart, c.wantEnd, c.candidate, matched, c.wantMatched)
			}
		})
	}
}

// TestFuzzyRankSpans_EmptyQuery pins the empty-span convention a renderer
// keys off: every candidate survives in its original order, and each
// carries End < Start so "no query, nothing to paint" cannot be confused
// with a genuine one-rune match at index 0.
func TestFuzzyRankSpans_EmptyQuery(t *testing.T) {
	candidates := []string{"c", "a", "b"}
	got := fuzzyRankSpans(candidates, "")
	if len(got) != len(candidates) {
		t.Fatalf("fuzzyRankSpans(%v, \"\") returned %d hits, want all %d", candidates, len(got), len(candidates))
	}
	for i, c := range candidates {
		if got[i].Text != c {
			t.Fatalf("fuzzyRankSpans(%v, \"\") = %+v, want the original order unchanged", candidates, got)
		}
		if got[i].End >= got[i].Start {
			t.Errorf("candidate %q span = [%d,%d], want End < Start (an empty span)", c, got[i].Start, got[i].End)
		}
	}
}

// TestFuzzyRankSpans_IndicesAreRunesNotBytes is the whole reason the field
// documents rune indices: "café-project" has a two-byte 'é' at rune 3, so a
// byte-offset implementation reports an end one past the rune index and a
// caller slicing []rune with it either paints the wrong character or panics
// off the end of a short candidate.
func TestFuzzyRankSpans_IndicesAreRunesNotBytes(t *testing.T) {
	const candidate = "café-project"
	got := fuzzyRankSpans([]string{candidate}, "café")
	if len(got) != 1 {
		t.Fatalf("fuzzyRankSpans([%q], %q) returned %d hits, want 1", candidate, "café", len(got))
	}
	if got[0].Start != 0 || got[0].End != 3 {
		t.Fatalf("span = [%d,%d], want [0,3] -- rune indices, not the byte offsets [0,4]",
			got[0].Start, got[0].End)
	}
	if matched := string([]rune(candidate)[got[0].Start : got[0].End+1]); matched != "café" {
		t.Errorf("runes [%d,%d] of %q = %q, want %q", got[0].Start, got[0].End, candidate, matched, "café")
	}

	// A match that STARTS after the multi-byte rune is the other half:
	// both ends are one lower than the byte offsets would be, so a
	// byte-indexed implementation lands off by one at each end rather
	// than merely at the tail.
	got = fuzzyRankSpans([]string{candidate}, "pj")
	if len(got) != 1 {
		t.Fatalf("fuzzyRankSpans([%q], %q) returned %d hits, want 1", candidate, "pj", len(got))
	}
	if got[0].Start != 5 || got[0].End != 8 {
		t.Fatalf("span = [%d,%d], want [5,8] (the \"proj\" of \"project\", counted in runes -- byte offsets would be [6,9])",
			got[0].Start, got[0].End)
	}
	if matched := string([]rune(candidate)[got[0].Start : got[0].End+1]); matched != "proj" {
		t.Errorf("runes [%d,%d] of %q = %q, want %q", got[0].Start, got[0].End, candidate, matched, "proj")
	}
}

// TestFuzzyRankSpans_SpansSurviveRanking guards the pairing rather than
// either half of it: the sort reorders hits, so each surviving span must
// still belong to the candidate it is returned beside. Ranking puts the
// tight "hello" ahead of the loose "xhxexlx", and their spans (3 wide vs
// 5) must travel with them.
func TestFuzzyRankSpans_SpansSurviveRanking(t *testing.T) {
	got := fuzzyRankSpans([]string{"xhxexlx", "hello"}, "hel")
	want := []fuzzySpan{
		{Text: "hello", Start: 0, End: 2},
		{Text: "xhxexlx", Start: 1, End: 5},
	}
	if len(got) != len(want) {
		t.Fatalf("fuzzyRankSpans returned %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("hit %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestFuzzyRank_IsFuzzyRankSpansWithSpansDropped is the re-expression's own
// invariant, stated once here rather than left implicit in fuzzy_test.go
// continuing to pass: for the same inputs the two must agree element for
// element, empty query included.
func TestFuzzyRank_IsFuzzyRankSpansWithSpansDropped(t *testing.T) {
	candidates := []string{"herdr-draft", "atrium", "café-project", "hello", "xhxexlx", "zzhel", "hel"}
	for _, query := range []string{"", "h", "hel", "hd", "café", "zz-no-such-thing"} {
		ranked := fuzzyRank(candidates, query)
		spans := fuzzyRankSpans(candidates, query)
		if len(ranked) != len(spans) {
			t.Fatalf("query %q: fuzzyRank returned %d, fuzzyRankSpans %d", query, len(ranked), len(spans))
		}
		for i := range ranked {
			if ranked[i] != spans[i].Text {
				t.Errorf("query %q: element %d is %q via fuzzyRank but %q via fuzzyRankSpans",
					query, i, ranked[i], spans[i].Text)
			}
		}
	}
}
