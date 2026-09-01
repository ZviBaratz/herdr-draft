package form

import (
	"github.com/ZviBaratz/herdr-draft/internal/theme"
	"testing"
)

// buildDirBrowseForm puts DirField into focused path-browse mode over an
// app-supplied candidate set, for the "dir-browse-80x24" golden frame the
// task-17 brief names explicitly.
func buildDirBrowseForm(palette theme.Palette) Model {
	d := NewDirField(palette)
	d.SetCandidates(1, []string{
		"/home/zvi/Projects/herdr",
		"/home/zvi/Projects/herdr-draft",
		"/home/zvi/Projects/atrium",
	})
	d.Focus() // an unfocused lineInput ignores keystrokes -- see lineinput.go
	for _, r := range "/home/zvi/Projects/h" {
		d.Update(rn(r))
	}

	m := New(Setup{Palette: palette, Sections: []Section{d}})
	m.Init()
	return m
}

func TestFrames_DirBrowse(t *testing.T) {
	assertFrame(t, "dir-browse-80x24", buildDirBrowseForm(theme.Default()), 80, 24)
}

// buildTitleVerdictForm types a title into TitleField and sets a matching
// SetVerdict message, for the "title-verdict-80x24" golden frame.
func buildTitleVerdictForm(palette theme.Palette) Model {
	f := NewTitleField(palette)
	f.Focus()
	for _, r := range "fix login bug" {
		f.Update(rn(r))
	}
	f.SetVerdict(f.Value(), "branch: zvi/fix-login-bug")

	m := New(Setup{Palette: palette, Sections: []Section{f}})
	m.Init()
	return m
}

func TestFrames_TitleVerdict(t *testing.T) {
	assertFrame(t, "title-verdict-80x24", buildTitleVerdictForm(theme.Default()), 80, 24)
}

// buildWorktreeNonGitForm puts all three WorktreeField zones into the
// non-git present-but-inert state, for the "worktree-nongit-80x24" golden
// frame -- exercising the "distinct placeholders" contract
// TestWorktreeField_NonGitPlaceholdersAreDistinct already pins
// behaviorally.
func buildWorktreeNonGitForm(palette theme.Palette) Model {
	w := NewWorktreeField(palette)
	w.SetGitTarget(false)

	m := New(Setup{Palette: palette, Sections: []Section{
		w.ChipsSection(), w.BranchSection(), w.BaseSection(),
	}})
	m.Init()
	return m
}

func TestFrames_WorktreeNonGit(t *testing.T) {
	assertFrame(t, "worktree-nongit-80x24", buildWorktreeNonGitForm(theme.Default()), 80, 24)
}

// buildPlacementInertForm sets PlacementField inert (worktree on), for the
// "placement-inert-80x24" golden frame.
func buildPlacementInertForm(palette theme.Palette) Model {
	f := NewPlacementField(palette)
	f.SetWorktreeOn(true)

	m := New(Setup{Palette: palette, Sections: []Section{f}})
	m.Init()
	return m
}

func TestFrames_PlacementInert(t *testing.T) {
	assertFrame(t, "placement-inert-80x24", buildPlacementInertForm(theme.Default()), 80, 24)
}
