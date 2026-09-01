package form

import "testing"

func TestFuzzyMatch_Basic(t *testing.T) {
	cases := []struct {
		name      string
		candidate string
		query     string
		wantOK    bool
	}{
		{"exact", "herdr", "herdr", true},
		{"subsequence", "herdr-draft", "hdd", true},
		{"case insensitive", "Herdr-Draft", "HD", true},
		{"not a subsequence (out of order)", "herdr", "rhe", false},
		{"missing rune", "herdr", "hz", false},
		{"empty candidate, non-empty query", "", "h", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ok, _, _ := fuzzyMatch(c.candidate, c.query)
			if ok != c.wantOK {
				t.Errorf("fuzzyMatch(%q, %q) ok = %v, want %v", c.candidate, c.query, ok, c.wantOK)
			}
		})
	}
}

// TestFuzzyMatch_TightensWindow pins the two-pass algorithm's own reason
// for existing: a naive forward-only greedy match would anchor "el" in
// "xexlxlxo" at the FIRST "e" (index 1) and the FIRST "l" after it (index
// 3), a span of 3. The backward tightening pass must not change that in
// this case (there's only one "e"), but in a candidate with more than one
// occurrence of the first query rune, the backward pass re-anchors the
// window's start to the latest "e" that still permits reaching the fixed
// end -- proven by TestFuzzyRank_PrefersTighterMatch below via ranking,
// and directly here via the returned span.
func TestFuzzyMatch_TightensWindow(t *testing.T) {
	// "aXaYb": query "ab". Forward pass matches 'a' at 0, then 'b' at 4
	// (end=4). Backward pass re-anchors 'a' to the LATEST 'a' at or before
	// end=4, which is index 2 -- tightening the window from [0,4] (span 5)
	// to [2,4] (span 3).
	ok, start, end := fuzzyMatch("aXaYb", "ab")
	if !ok {
		t.Fatalf("fuzzyMatch(%q, %q) ok = false, want true", "aXaYb", "ab")
	}
	if start != 2 || end != 4 {
		t.Fatalf("fuzzyMatch(%q, %q) = (start=%d, end=%d), want (2, 4) -- backward pass should tighten to the second 'a'", "aXaYb", "ab", start, end)
	}
}

func TestFuzzyRank_ExcludesNonMatches(t *testing.T) {
	candidates := []string{"herdr-draft", "atrium", "other-project"}
	got := fuzzyRank(candidates, "hd")
	if len(got) != 1 || got[0] != "herdr-draft" {
		t.Fatalf("fuzzyRank(%v, %q) = %v, want [herdr-draft]", candidates, "hd", got)
	}
}

// TestFuzzyRank_PrefersTighterMatch is the brief's own ranking
// requirement ("rank by match tightness/earliness"): "hello" matches "hel"
// with a tight span of exactly 3 ("hel"), while "xhxexlx" matches with a
// loose span of 5 ("h.e.l" spread out) -- the tighter match must sort
// first regardless of original list order.
func TestFuzzyRank_PrefersTighterMatch(t *testing.T) {
	candidates := []string{"xhxexlx", "hello"}
	got := fuzzyRank(candidates, "hel")
	want := []string{"hello", "xhxexlx"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("fuzzyRank(%v, %q) = %v, want %v (tighter match ranks first)", candidates, "hel", got, want)
	}
}

// TestFuzzyRank_TieBreaksOnEarliness: two candidates with the SAME
// tightness (span 3, "hel" contiguous in both) but the match starts
// earlier in one than the other -- the earlier start must rank first.
func TestFuzzyRank_TieBreaksOnEarliness(t *testing.T) {
	candidates := []string{"zzhel", "hel"}
	got := fuzzyRank(candidates, "hel")
	want := []string{"hel", "zzhel"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("fuzzyRank(%v, %q) = %v, want %v (earlier match start ranks first on a tightness tie)", candidates, "hel", got, want)
	}
}

// TestFuzzyRank_TieBreaksOnOriginalOrder: two fully-identical candidates
// (same span, same start) must keep their original relative order --
// pinning the explicit index tiebreak, not just an assumption that
// sort.SliceStable alone would happen to produce this.
func TestFuzzyRank_TieBreaksOnOriginalOrder(t *testing.T) {
	candidates := []string{"proj-hel-a", "proj-hel-b"}
	got := fuzzyRank(candidates, "hel")
	if len(got) != 2 || got[0] != "proj-hel-a" || got[1] != "proj-hel-b" {
		t.Fatalf("fuzzyRank(%v, %q) = %v, want original order preserved for an exact tie", candidates, "hel", got)
	}
}

func TestFuzzyRank_EmptyQueryReturnsAllUnranked(t *testing.T) {
	candidates := []string{"c", "a", "b"}
	got := fuzzyRank(candidates, "")
	for i, c := range candidates {
		if got[i] != c {
			t.Fatalf("fuzzyRank(%v, \"\") = %v, want the original order unchanged", candidates, got)
		}
	}
}

func TestFuzzyRank_UnicodeSafe(t *testing.T) {
	// Multi-byte runes must not corrupt indexing (a byte-indexed
	// implementation would panic or mis-slice on café/héllo).
	candidates := []string{"café-project", "other"}
	got := fuzzyRank(candidates, "café")
	if len(got) != 1 || got[0] != "café-project" {
		t.Fatalf("fuzzyRank(%v, %q) = %v, want [café-project]", candidates, "café", got)
	}
}

func TestFuzzyRank_DoesNotMutateInput(t *testing.T) {
	candidates := []string{"c", "a", "b"}
	original := append([]string(nil), candidates...)
	fuzzyRank(candidates, "")
	for i := range candidates {
		if candidates[i] != original[i] {
			t.Fatalf("fuzzyRank mutated its input slice: got %v, want %v", candidates, original)
		}
	}
}
