package form

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// v1Only pins a fixture form to compose's v1 path.
//
// Every frame in this file is a v1 frame: it renders ONE field plus the
// internal Create section, and it exists to pin what that field's
// View/Height/MinHeight draw. Since compose's gate is a capability check
// over the whole ring (form.go's allRowSections), a form of one already
// migrated field plus Create would satisfy it, flip to v2's row stack,
// and move a frame that is not about v2 at all -- so these fixtures state
// which path they mean instead of inferring it.
//
// It works by embedding the Section INTERFACE, whose method set is
// exactly v1's: the wrapper promotes ID/Enabled/Focus/Blur/Update/View/
// Height/MinHeight and nothing else, so Label/Row/Panel/PanelRows are not
// reachable through it. The optional capability interfaces (titleValuer,
// completer, newliner, footerHinter) are hidden too, which changes
// nothing here: composeLegacy's footer is legacyFooterRungs, which
// consults neither the focused zone nor footerHinter, and the other three
// only ever affect key handling, which these fixtures never exercise.
//
// This whole helper, and the six v1 frames it serves, are deleted by the
// change that flips the compose path and regenerates every frame.
type v1Only struct{ Section }

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

	m := New(Setup{Palette: palette, Sections: []Section{v1Only{d}}})
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

	m := New(Setup{Palette: palette, Sections: []Section{v1Only{f}}})
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

	m := New(Setup{Palette: palette, Sections: []Section{v1Only{f}}})
	m.Init()
	return m
}

func TestFrames_PlacementInert(t *testing.T) {
	assertFrame(t, "placement-inert-80x24", buildPlacementInertForm(theme.Default()), 80, 24)
}

// buildIssuePickerForm focuses IssueField over a small assigned-issue set,
// for the "issue-picker-120x40" golden frame the task-18 brief names
// explicitly.
func buildIssuePickerForm(palette theme.Palette) Model {
	f := NewIssueField(palette)
	f.SetIssues(1, sampleIssues())
	f.Focus()
	f.Update(key(tea.KeyDown, 0)) // none -> ENG-1

	m := New(Setup{Palette: palette, Sections: []Section{v1Only{f}}})
	m.Init()
	return m
}

func TestFrames_IssuePicker(t *testing.T) {
	assertFrame(t, "issue-picker-120x40", buildIssuePickerForm(theme.Default()), 120, 40)
}

// buildAccountForm enables AccountField (agent kind claude) over a mixed
// healthy/warned profile set, for the "account-80x24" golden frame.
func buildAccountForm(palette theme.Palette) Model {
	f := NewAccountField(palette)
	f.SetAgentIsClaude(true)
	f.SetProfiles(sampleStatus())
	f.Focus()
	f.Update(key(tea.KeyDown, 0)) // active -> alpha

	m := New(Setup{Palette: palette, Sections: []Section{v1Only{f}}})
	m.Init()
	return m
}

func TestFrames_Account(t *testing.T) {
	assertFrame(t, "account-80x24", buildAccountForm(theme.Default()), 80, 24)
}
