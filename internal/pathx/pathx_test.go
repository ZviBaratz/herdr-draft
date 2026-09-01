package pathx

import (
	"fmt"
	"os"
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

func TestResolve(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}

	cases := []struct {
		in   string
		want string
	}{
		{"~/Projects/x", filepath.Join(home, "Projects", "x")},
		{"~", home},
		{"/already/absolute", "/already/absolute"},
		// Relative input is what Resolve exists for: a "./foo" reaching
		// herdr's server would resolve against the SERVER's cwd.
		{"./foo", filepath.Join(cwd, "foo")},
		{"foo/bar", filepath.Join(cwd, "foo", "bar")},
		// filepath.Abs cleans, so a trailing slash does not survive --
		// callers keep the raw typed text for display and use this only
		// for the filesystem.
		{"/tmp/x/", "/tmp/x"},
	}
	for _, tc := range cases {
		if got := Resolve(tc.in); got != tc.want {
			t.Errorf("Resolve(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestListSubdirsReturnsSortedAbsoluteDirectoriesOnly(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"zeta", "alpha", ".hidden", "middle"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatalf("Mkdir %s: %v", name, err)
		}
	}
	for _, name := range []string{"a-file.txt", "another"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}

	got := ListSubdirs(root, 50)
	want := []string{
		filepath.Join(root, ".hidden"),
		filepath.Join(root, "alpha"),
		filepath.Join(root, "middle"),
		filepath.Join(root, "zeta"),
	}
	if len(got) != len(want) {
		t.Fatalf("ListSubdirs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ListSubdirs = %v, want %v", got, want)
		}
	}
}

// TestListSubdirsFollowsSymlinkedDirectories pins the deviation from
// atrium's own listSubdirs: a symlinked project root must be listable.
func TestListSubdirsFollowsSymlinkedDirectories(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Join(target, "nope"), filepath.Join(root, "broken")); err != nil {
		t.Fatalf("Symlink broken: %v", err)
	}
	if err := os.WriteFile(filepath.Join(target, "f"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Symlink(filepath.Join(target, "f"), filepath.Join(root, "to-file")); err != nil {
		t.Fatalf("Symlink to-file: %v", err)
	}

	got := ListSubdirs(root, 50)
	if len(got) != 1 || got[0] != filepath.Join(root, "linked") {
		t.Errorf("ListSubdirs = %v, want only the directory symlink", got)
	}
}

// TestListSubdirsBoundsTheResultNotTheRead pins the other deviation: a
// directory whose files vastly outnumber its subdirectories must still
// report the subdirectories (atrium's read-bounded version reported none).
func TestListSubdirsBoundsTheResultNotTheRead(t *testing.T) {
	root := t.TempDir()
	for i := range 700 {
		name := filepath.Join(root, fmt.Sprintf("file-%04d", i))
		if err := os.WriteFile(name, nil, 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "zzz-dir"), 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	got := ListSubdirs(root, 500)
	if len(got) != 1 || got[0] != filepath.Join(root, "zzz-dir") {
		t.Errorf("ListSubdirs = %v, want the one subdirectory behind 700 files", got)
	}
}

func TestListSubdirsHonorsTheLimit(t *testing.T) {
	root := t.TempDir()
	for i := range 10 {
		if err := os.Mkdir(filepath.Join(root, fmt.Sprintf("d%02d", i)), 0o755); err != nil {
			t.Fatalf("Mkdir: %v", err)
		}
	}
	if got := ListSubdirs(root, 4); len(got) != 4 {
		t.Errorf("ListSubdirs with limit 4 returned %d entries: %v", len(got), got)
	}
	if got := ListSubdirs(root, 0); got != nil {
		t.Errorf("ListSubdirs with limit 0 = %v, want nil", got)
	}
}

// TestListSubdirsOnAnUnreadablePathIsEmpty pins the degradation every
// caller relies on: no entries, no error, so the literal-path fallback
// row is what the user sees.
func TestListSubdirsOnAnUnreadablePathIsEmpty(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if got := ListSubdirs(filepath.Join(root, "does-not-exist"), 50); len(got) != 0 {
		t.Errorf("ListSubdirs(missing) = %v, want empty", got)
	}
	if got := ListSubdirs(file, 50); len(got) != 0 {
		t.Errorf("ListSubdirs(a file) = %v, want empty", got)
	}
}
