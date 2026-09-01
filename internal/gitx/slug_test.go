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
