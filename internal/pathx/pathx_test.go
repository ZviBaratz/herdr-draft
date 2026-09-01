package pathx

import (
	"path/filepath"
	"testing"
)

func TestExpandTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cases := []struct {
		in   string
		want string
	}{
		{"~", home},
		{"~/Projects/herdr-draft", filepath.Join(home, "Projects", "herdr-draft")},
		{"~/", home},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
		{"", ""},
		// "~user" is deliberately left alone: resolving another user's
		// home needs a name-service lookup this plugin does not do.
		{"~other/Projects", "~other/Projects"},
		// A "~" that is not the first character is an ordinary character.
		{"/tmp/~/x", "/tmp/~/x"},
	}

	for _, tc := range cases {
		if got := ExpandTilde(tc.in); got != tc.want {
			t.Errorf("ExpandTilde(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestExpandTildeWithoutAHomeLeavesThePathAlone pins the degradation: an
// environment with no resolvable home must not substitute a guess. The
// caller's own directory-validity check is where the user learns the path
// does not resolve.
func TestExpandTildeWithoutAHomeLeavesThePathAlone(t *testing.T) {
	t.Setenv("HOME", "")
	if got := ExpandTilde("~/Projects/x"); got != "~/Projects/x" {
		t.Errorf("ExpandTilde with no HOME = %q, want the input unchanged", got)
	}
}
