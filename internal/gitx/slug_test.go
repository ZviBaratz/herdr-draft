package gitx

import "testing"

func TestBranchSlug(t *testing.T) {
	cases := []struct{ name, prefix, title, want string }{
		{"simple", "zvi/", "Fix pane focus", "zvi/fix-pane-focus"},
		{"unicode and symbols dropped", "zvi/", "héllo?? world!", "zvi/hello-world"},
		{"collapses runs", "zvi/", "a  --  b", "zvi/a-b"},
		{"trims separators", "zvi/", "-a-", "zvi/a"},
		{"no prefix", "", "Add thing", "add-thing"},
		{"git-invalid chars", "zvi/", "a..b~c^d:e", "zvi/a-b-c-d-e"},
	}
	for _, c := range cases {
		if got := BranchSlug(c.prefix, c.title); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestBranchSlugHashFallback(t *testing.T) {
	got := BranchSlug("zvi/", "??? !!!")
	if len(got) != len("zvi/session-")+8 || got[:len("zvi/session-")] != "zvi/session-" {
		t.Errorf("fallback shape wrong: %q", got)
	}
	if got != BranchSlug("zvi/", "??? !!!") {
		t.Error("fallback must be deterministic")
	}
}

// FuzzBranchSlugIsAValidBranchName: a branch BranchSlug derives from a
// prefix ValidateBranchPrefix accepts is one ValidateBranchName accepts too,
// whatever the title. A derived name the validator refused would be the
// slug's fault, not the user's -- the form would show a verdict against a
// branch nobody typed, and `create` would exit 2 over a title (#199).
//
// The seeds are the ones that once broke it: a prefix whose last component
// is left open, so the slug can complete ".lock" on it, and a prefix that
// begins with a no-break space, which git accepts and herdr trims. `go test
// -fuzz=FuzzBranchSlugIsAValidBranchName ./internal/gitx/` looks for more.
func FuzzBranchSlugIsAValidBranchName(f *testing.F) {
	for _, seed := range []struct{ prefix, title string }{
		{"zvi/", "fix login"},
		{"", "Add thing"},
		{"zvi/", "??? !!!"},
		{"zvi.", "Lock"},
		{"zvi.l", "ock"},
		{"zvi.lo", "CK"},
		{"zvi.loc", "k"},
		{"a.b.", "lock"},
		{string(rune(0xa0)) + "zvi/", "fix"},
		{string(rune(0x3000)), "fix"},
	} {
		f.Add(seed.prefix, seed.title)
	}
	f.Fuzz(func(t *testing.T, prefix, title string) {
		if ValidateBranchPrefix(prefix) != nil {
			return
		}
		branch := BranchSlug(prefix, title)
		if err := ValidateBranchName(branch); err != nil {
			t.Errorf("BranchSlug(%q, %q) = %q, which ValidateBranchName refuses: %v", prefix, title, branch, err)
		}
		if again := BranchSlug(prefix, title); again != branch {
			t.Errorf("BranchSlug(%q, %q) is not deterministic: %q, then %q", prefix, title, branch, again)
		}
	})
}
