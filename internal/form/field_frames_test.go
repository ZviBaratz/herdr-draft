package form

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// field_frames_test.go pins ONE field at a time, focused, so its row and
// its panel can be reviewed without the other seven competing for the
// same frame. The assembled form is pinned separately, in
// internal/app/frames_test.go.
//
// Every fixture here renders through v2's row stack, which is the only
// path production takes: a form of one migrated field plus the internal
// Create section satisfies compose's allRowSections gate on its own. The
// v1Only wrapper these fixtures used to carry -- which hid a field's
// Label/Row/Panel/PanelRows behind the bare Section interface to keep
// them on the legacy path -- went with the compose flip.

// fieldFrame builds a one-field form with the same header the real form
// has, focused on that field so its panel renders.
//
// InitialFocusID names the field explicitly rather than relying on the
// ring's first-enabled walk, because two of the fixtures below are
// deliberately INERT (a non-git worktree, an inert placement) and the
// walk would skip straight past them to the always-enabled Create
// section -- pinning the panel of the wrong section entirely.
func fieldFrame(palette theme.Palette, s Section) Model {
	m := New(Setup{
		Palette:        palette,
		Sections:       []Section{s},
		Name:           "new session",
		InitialFocusID: s.ID(),
	})
	m.SetContext("herdr-draft · main")
	m.Init()
	return m
}

// buildDirPanelForm puts DirField into focused path-browse mode over an
// app-supplied candidate set, with a home directory installed so both the
// row and the panel's candidate rows collapse to "~".
func buildDirPanelForm(palette theme.Palette) Model {
	d := NewDirField(palette)
	d.SetHomeDir("/home/zvi")
	d.SetCandidates(1, []string{
		"/home/zvi/Projects/herdr",
		"/home/zvi/Projects/herdr-draft",
		"/home/zvi/Projects/atrium",
	})
	d.Focus() // an unfocused lineInput ignores keystrokes -- see lineinput.go
	for _, r := range "~/Projects/h" {
		d.Update(rn(r))
	}
	return fieldFrame(palette, d)
}

func TestFrames_DirPanel(t *testing.T) {
	assertFrame(t, "dir-panel-80x24", buildDirPanelForm(theme.Default()), 80, 24)
}

// buildDirNotesForm is v2 spec §11's ignored-key report, on the panel that
// carries it (DirField.SetNotes): the project row is what decides which
// repository's `.herdr-draft.toml` applies, so its panel is where the
// refusals from that file are read. The three notes are verbatim
// config.LoadRepoConfig output -- a forbidden table, a forbidden key with
// its own reason, and the branch_prefix rejection that falls back to the
// user's own prefix.
func buildDirNotesForm(palette theme.Palette) Model {
	d := NewDirField(palette)
	d.SetHomeDir("/home/zvi")
	d.SetCandidates(1, []string{
		"/home/zvi/Projects/herdr-draft",
		"/home/zvi/Projects/herdr",
		"/home/zvi/Projects/atrium",
	})
	d.SetNotes([]string{
		"ignoring agents.extra_args: it becomes part of a launched agent's command line",
		"ignoring linear.prompt_template: it would become the agent's first instruction",
		`ignoring branch_prefix "a b/": a branch prefix may not contain a space; using your own configured prefix`,
	})
	d.Focus()
	return fieldFrame(palette, d)
}

func TestFrames_DirNotes(t *testing.T) {
	assertFrame(t, "dir-notes-80x24", buildDirNotesForm(theme.Default()), 80, 24)
}

// buildWorktreeProvenanceForm is v2 spec §11's provenance line, on the
// panel of a value a committed `.herdr-draft.toml` chose: the same live
// worktree the mockup frame draws, with `from .herdr-draft.toml` under the
// three parts and above the base list. The ROW is deliberately identical to
// buildWorktreePanelForm's -- provenance never appears there.
func buildWorktreeProvenanceForm(palette theme.Palette) Model {
	w := NewWorktreeField(palette)
	w.SetGitTarget(true)
	w.SetOn(true)
	w.SetBranch("zvi/fix-login-redirect-loop", false)
	w.SetHeadBranch("main")
	w.SetBaseItems(1, []string{"main", "release/1.4"})
	w.SetProvenance(".herdr-draft.toml")
	return fieldFrame(palette, w)
}

func TestFrames_WorktreeProvenance(t *testing.T) {
	assertFrame(t, "worktree-provenance-80x24", buildWorktreeProvenanceForm(theme.Default()), 80, 24)
}

// buildTitlePanelForm types a title into TitleField and sets a matching
// SetVerdict message -- the app layer's own resting note, verbatim
// (internal/app's titleNote: "branch will be <branch>", v2 spec §4's own
// mockup), and 42 cells of it, longer than v1's retired 21-cell clamp,
// so the frame shows the verdict whole.
func buildTitlePanelForm(palette theme.Palette) Model {
	f := NewTitleField(palette)
	f.Focus()
	for _, r := range "fix login redirect loop" {
		f.Update(rn(r))
	}
	f.SetVerdict(f.Value(), "branch will be zvi/fix-login-redirect-loop")
	return fieldFrame(palette, f)
}

func TestFrames_TitlePanel(t *testing.T) {
	assertFrame(t, "title-panel-80x24", buildTitlePanelForm(theme.Default()), 80, 24)
}

// buildPromptPanelForm focuses PromptField over a multi-line prompt, so
// the frame shows both the row's "+N more" summary and the textarea the
// panel opens onto.
func buildPromptPanelForm(palette theme.Palette) Model {
	f := NewPromptField(palette)
	f.SetValue("Work on ENG-101: Fix login redirect loop\n\nStart with the cookie the callback sets.", false)
	f.Focus()
	return fieldFrame(palette, f)
}

func TestFrames_PromptPanel(t *testing.T) {
	assertFrame(t, "prompt-panel-80x24", buildPromptPanelForm(theme.Default()), 80, 24)
}

// buildWorktreePanelForm is v2 spec §4's own worktree mockup: a live
// worktree with a branch, a base list, and the part cursor moved off the
// chips onto the branch -- the state the mockup draws.
func buildWorktreePanelForm(palette theme.Palette) Model {
	w := NewWorktreeField(palette)
	w.SetGitTarget(true)
	w.SetOn(true)
	w.SetBranch("zvi/fix-login-redirect-loop", false)
	w.SetHeadBranch("main")
	w.SetBaseItems(1, []string{"main", "release/1.4"})

	m := fieldFrame(palette, w)
	w.Update(key(tea.KeyDown, 0)) // chips -> branch
	return m
}

func TestFrames_WorktreePanel(t *testing.T) {
	assertFrame(t, "worktree-panel-80x24", buildWorktreePanelForm(theme.Default()), 80, 24)
}

// buildWorktreeNonGitForm pins the other end of the field: a target that
// cannot host a worktree at all, where the row and all three panel parts
// carry the non-git reason rather than an empty control.
func buildWorktreeNonGitForm(palette theme.Palette) Model {
	w := NewWorktreeField(palette)
	w.SetGitTarget(false)
	return fieldFrame(palette, w)
}

func TestFrames_WorktreeNonGit(t *testing.T) {
	assertFrame(t, "worktree-nongit-80x24", buildWorktreeNonGitForm(theme.Default()), 80, 24)
}

// buildPlacementPanelForm sets PlacementField inert (worktree on), the
// state where the row states the reason and the panel's chips are
// replaced by their own placeholder.
func buildPlacementPanelForm(palette theme.Palette) Model {
	f := NewPlacementField(palette)
	f.SetWorktreeOn(true)
	return fieldFrame(palette, f)
}

func TestFrames_PlacementPanel(t *testing.T) {
	assertFrame(t, "placement-panel-80x24", buildPlacementPanelForm(theme.Default()), 80, 24)
}

// buildIssuePanelForm focuses IssueField over a small assigned-issue set.
func buildIssuePanelForm(palette theme.Palette) Model {
	f := NewIssueField(palette)
	f.SetIssues(1, sampleIssues())
	f.Focus()
	f.Update(key(tea.KeyDown, 0)) // none -> ENG-1
	return fieldFrame(palette, f)
}

func TestFrames_IssuePanel(t *testing.T) {
	assertFrame(t, "issue-panel-120x40", buildIssuePanelForm(theme.Default()), 120, 40)
}

// buildAccountPanelForm enables AccountField (agent kind claude) over a
// mixed healthy/warned profile set, so the frame carries the colored
// state words that replaced v1's bare "!" marker.
func buildAccountPanelForm(palette theme.Palette) Model {
	f := NewAccountField(palette)
	f.SetAgentIsClaude(true)
	f.SetProfiles(sampleStatus())
	f.Focus()
	f.Update(key(tea.KeyDown, 0)) // active -> alpha
	return fieldFrame(palette, f)
}

func TestFrames_AccountPanel(t *testing.T) {
	assertFrame(t, "account-panel-80x24", buildAccountPanelForm(theme.Default()), 80, 24)
}

// buildAgentPanelForm focuses AgentField over more kinds than fit its
// favorites row, which in v1 hid the rest behind a "more…" chip and in v2
// simply lists them all in the panel.
func buildAgentPanelForm(palette theme.Palette) Model {
	f := NewAgentField(palette)
	f.SetKinds([]string{"claude", "codex", "pi", "gemini", "cursor", "aider"})
	f.Focus()
	return fieldFrame(palette, f)
}

func TestFrames_AgentPanel(t *testing.T) {
	assertFrame(t, "agent-panel-80x24", buildAgentPanelForm(theme.Default()), 80, 24)
}
