package form

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ZviBaratz/herdr-draft/internal/clauth"
	"github.com/ZviBaratz/herdr-draft/internal/linear"
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
// ring's first-enabled walk, because one of the fixtures below is
// deliberately INERT (a non-git worktree) and the walk would skip
// straight past it to the always-enabled Create section -- pinning the
// panel of the wrong section entirely. (Placement under a worktree is
// another -- placement spec §14 -- and its frame depends on this too.)
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

// buildDirFilteredForm is v3 spec §8.4's match highlighting, on the field
// that has to compute its own spans. It is FRAGMENT mode ("dr" starts with
// none of "/", "~", "."), which buildDirPanelForm's path-mode frame does
// not cover and which is where §8.4's "match the string that will be
// DISPLAYED" actually bites: fragment mode ranks the FULL path and draws
// it home-collapsed, so a span carried out of the ranker would be nine
// runes adrift and would paint nothing at all on these rows.
//
// The candidate set is buildDirPanelForm's, so the two frames diff
// cleanly, plus one deliberate outlier. "~/Downloads/reports" matches "dr"
// across "ds/r" -- a longer window, in a different column, on a row that
// ranks below the tight ones -- which is what makes this frame evidence
// that the span is computed PER ROW rather than once for the list.
// "atrium" carries no "d" at all and is filtered out, so the frame also
// pins §8.5's readout at 3/4.
func buildDirFilteredForm(palette theme.Palette) Model {
	d := NewDirField(palette)
	d.SetHomeDir("/home/zvi")
	d.SetCandidates(1, []string{
		"/home/zvi/Projects/herdr",
		"/home/zvi/Projects/herdr-draft",
		"/home/zvi/Projects/atrium",
		"/home/zvi/Downloads/reports",
	})
	d.Focus() // an unfocused lineInput ignores keystrokes -- see lineinput.go
	for _, r := range "dr" {
		d.Update(rn(r))
	}
	return fieldFrame(palette, d)
}

func TestFrames_DirFiltered(t *testing.T) {
	assertFrame(t, "dir-filtered-80x24", buildDirFilteredForm(theme.Default()), 80, 24)
}

// buildDirNotesForm is v2 spec §11's ignored-key report, on the panel that
// carries it (DirField.SetNotes): the project row is what decides which
// repository's `.herdr-draft.toml` applies, so its panel is where the
// refusals from that file are read. The three notes are verbatim
// config.LoadRepoConfig output -- a forbidden table, a forbidden key with
// its own reason, and the branch_prefix rejection that falls back to the
// user's own prefix.
//
// Verbatim is the whole point, and this fixture failed it once: the
// branch_prefix note read "a branch prefix may not contain a space",
// which is nobody's string -- gitx.ValidateBranchPrefix returns
// "contains a space" and config/repo.go interpolates that. Because the
// fixture writes its own note text rather than calling the loader, the
// invention rendered and pinned cleanly, and a frame that invents its
// copy cannot catch a copy regression. Anything quoted here must be a
// string production actually emits.
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
		`ignoring branch_prefix "a b/": contains a space; using your own configured prefix`,
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

// TestFrames_PromptPanelReap pins the one state no other frame reaches:
// reap selected over a real prompt, so the row carries its suffix and the
// panel's first line its "on" hint (reap spec §5.3).
func TestFrames_PromptPanelReap(t *testing.T) {
	palette := theme.Default()
	f := NewPromptField(palette)
	f.SetValue("Work on ENG-101: Fix login redirect loop\n\nStart with the cookie the callback sets.", false)
	f.SetMarkReady(true)
	f.Focus()
	assertFrame(t, "prompt-panel-reap-80x24", fieldFrame(palette, f), 80, 24)
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

// buildWorktreeBranchVerdictForm is #199's refusal as the user meets it: a
// submit refused over a branch with a trailing space, the part cursor on
// the branch input it has to be fixed in, and the verdict under the parts.
// The space itself is invisible, which is the whole reason the verdict names
// it.
func buildWorktreeBranchVerdictForm(palette theme.Palette) Model {
	w := NewWorktreeField(palette)
	w.SetGitTarget(true)
	w.SetOn(true)
	w.SetBranch("zvi/old ", false)
	w.SetHeadBranch("main")
	w.SetBaseItems(1, []string{"main", "release/1.4"})
	w.SetBranchVerdict("zvi/old ", "invalid branch name  ends with a space")

	m := fieldFrame(palette, w)
	w.FocusBranch()
	return m
}

func TestFrames_WorktreeBranchVerdict(t *testing.T) {
	assertFrame(t, "worktree-branch-verdict-80x24", buildWorktreeBranchVerdictForm(theme.Default()), 80, 24)
}

// TestFrames_WorktreeBranchRequired is the other refusal the branch part
// carries: a submit refused over a branch input cleared by hand. The editor
// shows its own dim placeholder, `branch name`, with the verdict under it.
func TestFrames_WorktreeBranchRequired(t *testing.T) {
	palette := theme.Default()
	w := NewWorktreeField(palette)
	w.SetGitTarget(true)
	w.SetOn(true)
	w.SetBranch("", false)
	w.SetHeadBranch("main")
	w.SetBaseItems(1, []string{"main", "release/1.4"})
	w.SetBranchVerdict("", "branch name required")

	m := fieldFrame(palette, w)
	w.FocusBranch()
	assertFrame(t, "worktree-branch-required-80x24", m, 80, 24)
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

// buildPlacementPanelForm focuses PlacementField with a non-default chip
// chosen, and -- with worktreeOn -- a worktree turned on afterwards:
// placement spec §14's inert state, reached by a click since Tab skips it.
// The two frames are one pair on purpose: the chosen "tab here" must show
// in the first and be absent from the second, which is the whole of what
// the worktree does to this row.
func buildPlacementPanelForm(palette theme.Palette, worktreeOn bool) Model {
	f := NewPlacementField(palette)
	f.Update(key(tea.KeyRight, 0)) // "tab here"
	f.SetWorktreeOn(worktreeOn)
	return fieldFrame(palette, f)
}

func TestFrames_PlacementPanel(t *testing.T) {
	assertFrame(t, "placement-panel-80x24", buildPlacementPanelForm(theme.Default(), false), 80, 24)
	assertFrame(t, "placement-panel-worktree-80x24", buildPlacementPanelForm(theme.Default(), true), 80, 24)
}

// buildIssuePanelForm focuses IssueField over a small assigned-issue set.
func buildIssuePanelForm(palette theme.Palette) Model {
	f := NewIssueField(palette)
	f.SetIssues(1, sampleIssues())
	f.Focus()
	f.Update(key(tea.KeyDown, 0)) // none -> ENG-1
	return fieldFrame(palette, f)
}

// 150x44 rather than the retired 120x40 (v3 spec §12): the picker wants a
// frame with room to spare, and 120x40 corresponds to nothing herdr's
// fixed-cell popup can produce.
func TestFrames_IssuePanel(t *testing.T) {
	assertFrame(t, "issue-panel-150x44", buildIssuePanelForm(theme.Default()), 150, 44)
}

// buildIssueColumnsForm is v3 spec §8.1's own motivating example, which
// the fixture above cannot show: sampleIssues' two identifiers are the
// same width and its titles both fit, so that frame renders identically
// whether or not the columns line up. This one has three identifiers of
// three different widths and a title far too long for the row, which is
// the whole complaint -- before v3, `ENG-1` and `ENG-101` started their
// titles at different cells and a long title pushed the status word off
// the end mid-word.
func buildIssueColumnsForm(palette theme.Palette) Model {
	f := NewIssueField(palette)
	f.SetIssues(1, []linear.Issue{
		{Identifier: "ENG-1", Title: "Fix login bug", StateName: "Todo", Estimate: estimate(3)},
		{Identifier: "ENG-101", Title: "revalidate and reset the candidate pool on every browse transition", StateName: "In Progress"},
		{Identifier: "PLATFORM-7", Title: "short", StateName: "Done"},
	})
	f.Focus()
	f.Update(key(tea.KeyDown, 0)) // none -> ENG-1
	return fieldFrame(palette, f)
}

func TestFrames_IssueColumns(t *testing.T) {
	assertFrame(t, "issue-columns-80x16", buildIssueColumnsForm(theme.Default()), 80, 16)
}

// buildIssueFilteredForm is the OTHER half of v3 spec §8.4, the half
// widgets.Picker computes for itself: this field hands the picker an
// unranked list and lets SetQuery narrow it, so applyFilter owns every
// span here where DirField owns its own.
//
// It exists because CLAUDE.md's standing warning applies squarely --
// a golden suite proves only the states someone thought to fixture, and
// before this frame no fixture in the project drew a MULTI-COLUMN picker
// under a query at all. "at" is chosen so the answer differs per row and
// a renderer that ignored Match.Col could not pass: it matches
// PLATFORM-7 in the IDENTIFIER and revalidate in the TITLE, and ENG-1
// not at all, which also pins §8.5's readout at 2/3.
func buildIssueFilteredForm(palette theme.Palette) Model {
	f := NewIssueField(palette)
	f.SetIssues(1, []linear.Issue{
		{Identifier: "ENG-1", Title: "Fix login bug", StateName: "Todo", Estimate: estimate(3)},
		{Identifier: "ENG-101", Title: "revalidate and reset the candidate pool on every browse transition", StateName: "In Progress"},
		{Identifier: "PLATFORM-7", Title: "short", StateName: "Done"},
	})
	f.Focus()
	for _, r := range "at" {
		f.Update(rn(r))
	}
	return fieldFrame(palette, f)
}

func TestFrames_IssueFiltered(t *testing.T) {
	assertFrame(t, "issue-filtered-80x16", buildIssueFilteredForm(theme.Default()), 80, 16)
}

// buildIssueScrollForm is #25's own acceptance fixture: a list far longer
// than the window it is drawn into, with the cursor walked far enough
// down that the thumb is clear of BOTH ends of the track. Every other
// frame in this package renders a list that fits, so before this one no
// fixture anywhere contained a scrollbar cell at all -- and the two ends
// are the positions an off-by-one still looks plausible at.
//
// Nothing is typed into it, so its count is the plain `20 issues`: the
// ratio form is pinned by dir-panel-80x24, whose typed text keeps two of
// three candidates. What this frame adds is that a count and a scrollbar
// coexist -- the count sits on the status line, one row BELOW the last
// row the bar reaches, so neither is drawn over the other.
func buildIssueScrollForm(palette theme.Palette) Model {
	issues := make([]linear.Issue, 20)
	for i := range issues {
		issues[i] = linear.Issue{
			Identifier: "ENG-" + strconv.Itoa(100+i),
			Title:      "queued work item " + strconv.Itoa(i),
			StateName:  "Todo",
		}
	}
	f := NewIssueField(palette)
	f.SetIssues(1, issues)
	f.Focus()
	for i := 0; i < 12; i++ {
		f.Update(key(tea.KeyDown, 0))
	}
	return fieldFrame(palette, f)
}

func TestFrames_IssueScroll(t *testing.T) {
	assertFrame(t, "issue-scroll-80x16", buildIssueScrollForm(theme.Default()), 80, 16)
}

// buildAccountPanelForm enables AccountField (agent kind claude) over a
// mixed healthy/warned profile set, so the frame carries the colored
// state words that replaced v1's bare "!" marker.
func buildAccountPanelForm(palette theme.Palette) Model {
	f := NewAccountField(palette)
	f.SetAgentIsClaude(true)
	f.SetProfiles(sampleStatus(), sampleNow())
	f.Focus()
	f.Update(key(tea.KeyDown, 0)) // active -> alpha
	return fieldFrame(palette, f)
}

func TestFrames_AccountPanel(t *testing.T) {
	assertFrame(t, "account-panel-80x24", buildAccountPanelForm(theme.Default()), 80, 24)
}

// buildAccountInertForm is the account row with a non-claude agent, reached
// by a click since Tab skips it (#182): the dim row, a panel of one sentence
// that gives the choice back, and a footer promising no keys.
func buildAccountInertForm(palette theme.Palette) Model {
	f := NewAccountField(palette)
	f.SetAgentIsClaude(false)
	f.SetProfiles(sampleStatus(), sampleNow())
	return fieldFrame(palette, f)
}

func TestFrames_AccountInert(t *testing.T) {
	assertFrame(t, "account-inert-80x24", buildAccountInertForm(theme.Default()), 80, 24)
}

// TestFrames_AccountPanelNarrow pins the SHRINK ladder, which no other
// frame reaches: the account panel is the widest table in the form (four
// cell columns, a mark column and a badge), so 44 cells is where v3 spec
// §8.1's Min/Flex arithmetic actually has to choose what to give up. It
// is here because the first attempt chose wrong -- flexing the profile
// NAME elided all four names to "…" while "Max 20x" kept every cell it
// asked for, and no fixture at 80 cells could have shown it.
// TestFrames_AccountGauge is #28's acceptance fixture, at the width the
// popup actually ships in (101x30, v3 spec §6.1) rather than this file's
// usual 80x24 measuring stick -- because the whole of v3 spec §10.2's
// table only exists at a real width, and it is the state the author asked
// for by name: three gauges at 0% / 12% / 100%, both windows, the `●`
// marking clauth's live profile, and a reset countdown.
//
// It is the other side of a boundary account-panel-80x24 pins: there the
// reset column cannot be given accountResetMinCells and is dropped
// outright rather than elided to a lone `…` on every row.
func TestFrames_AccountGauge(t *testing.T) {
	assertFrame(t, "account-gauge-101x30", buildAccountPanelForm(theme.Default()), 101, 30)
}

func TestFrames_AccountPanelNarrow(t *testing.T) {
	assertFrame(t, "account-panel-44x12", buildAccountPanelForm(theme.Default()), 44, 12)
}

// buildAccountExpiredForm is beta with clauth's `expired` rather than the
// `broken` sampleStatus gives it, so the third auth state reaches a frame.
//
// Its own builder rather than a fourth profile in sampleStatus: the panel is
// the widest table in the form, an extra row moves the `N profiles` legend
// and can push the 44-cell frame into a scrollbar, and re-making that layout
// decision as a side effect of an auth-vocabulary change is exactly what
// TestFrames_AccountPanelNarrow exists to catch. The seven frames built over
// sampleStatus keep pinning the state that actually blocks a submit; this
// one pins the state that must NOT.
func buildAccountExpiredForm(palette theme.Palette) Model {
	status := sampleStatus()
	status.Profiles[1].AuthStatus = clauth.AuthExpired
	f := NewAccountField(palette)
	f.SetAgentIsClaude(true)
	f.SetProfiles(status, sampleNow())
	f.SetPin("beta")
	f.Focus()
	return fieldFrame(palette, f)
}

// TestFrames_AccountExpired is #243 in bytes: `expired` reads as clauth's own
// word in the warning color, on the row and in the panel alike, where every
// non-"ok" status used to read `sign in again` in red and name a remedy that
// would not have helped.
func TestFrames_AccountExpired(t *testing.T) {
	assertFrame(t, "account-expired-101x30", buildAccountExpiredForm(theme.Default()), 101, 30)
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

// buildAccountAutoForm is the account panel on a machine that has configured
// AND probed an account picker: the `auto` row exists, sits first, and is the
// resting selection. preview is what the picker last answered.
//
// The cursor is deliberately left where it starts -- on `auto` -- rather than
// stepped down as buildAccountPanelForm does, because the whole point of these
// three frames is the row the form OPENS on. v2 shipped a defect in its
// opening state through fifteen green commits precisely because every fixture
// had already moved past it.
func buildAccountAutoForm(palette theme.Palette, preview AccountPickerPreview) Model {
	f := NewAccountField(palette)
	f.SetAgentIsClaude(true)
	f.SetProfiles(sampleStatus(), sampleNow())
	f.SetPickerAvailable(true)
	f.SetPickerPreview(preview)
	f.Focus()
	return fieldFrame(palette, f)
}

// TestFrames_AccountAutoPending is the state between a project change and the
// picker's answer -- the one a user sees most often and the one with the least
// to show, which is why it is worth a frame of its own.
func TestFrames_AccountAutoPending(t *testing.T) {
	assertFrame(t, "account-auto-pending-101x30",
		buildAccountAutoForm(theme.Default(), AccountPickerPreview{Pending: true}), 101, 30)
}

// TestFrames_AccountAutoPicked pins the answered row: `auto → alpha`, the
// picker's own gauges in the panel, and the ✓ on the auto row rather than on
// the profile it named -- two different rows, which is the distinction the
// whole `auto` design rests on.
func TestFrames_AccountAutoPicked(t *testing.T) {
	assertFrame(t, "account-auto-picked-101x30",
		buildAccountAutoForm(theme.Default(), AccountPickerPreview{
			Profile: "alpha", Tier: "Max", FiveHourPct: pct(3), WeeklyPct: pct(11),
		}), 101, 30)
}

// TestFrames_AccountAutoPickedWithMachine is the picked state as a picker that
// reports its host draws it (#165): one warning in the warning tone, and the
// machine figures dim beneath it, above the legend.
func TestFrames_AccountAutoPickedWithMachine(t *testing.T) {
	assertFrame(t, "account-auto-picked-machine-101x30",
		buildAccountAutoForm(theme.Default(), AccountPickerPreview{
			Profile: "alpha", Tier: "Max", FiveHourPct: pct(3), WeeklyPct: pct(11),
			Load1: pct(4.37), NCPU: pct(8), SwapUsedPct: pct(32),
			Warnings: []string{"alpha resets in 12m"},
		}), 101, 30)
}

// TestFrames_AccountAutoRefused is the state this row exists for: the picker
// ran, said no, and said why. Nothing here may read "unavailable".
func TestFrames_AccountAutoRefused(t *testing.T) {
	assertFrame(t, "account-auto-refused-101x30",
		buildAccountAutoForm(theme.Default(), AccountPickerPreview{
			Refusal: "pool exhausted (resets 22:49)",
		}), 101, 30)
}

// buildOptionsPanelForm focuses OptionsField on claude with a config seed,
// extra_args passing a flag of its own, and the cursor moved to effort --
// agent-options spec §7.2's panel with every line it can carry. With other,
// the model is a typed id, so the name part is open under it.
func buildOptionsPanelForm(palette theme.Palette, other bool) Model {
	f := NewOptionsField(palette)
	seed := map[string]string{"effort": "xhigh", "permission_mode": "plan"}
	if other {
		seed["model"] = "claude-opus-5[1m]"
	}
	f.SetKind("claude", KindOptions{
		Specs: claudeSpecs(), Seed: seed, SeedSource: "config.toml",
		ExtraArgs: []string{"--verbose", "--model", "opus"},
		Pinned:    map[string]string{"model": "opus"},
	})
	m := fieldFrame(palette, f)
	f.Update(key(tea.KeyDown, 0))
	return m
}

func TestFrames_OptionsPanel(t *testing.T) {
	assertFrame(t, "options-panel-80x24", buildOptionsPanelForm(theme.Default(), false), 80, 24)
	assertFrame(t, "options-panel-other-80x24", buildOptionsPanelForm(theme.Default(), true), 80, 24)
}

// buildOptionsInertForm is a kind that declares nothing, reached by a
// click since Tab skips it: agent-options spec §7.1's one dim row, and a
// panel that says why and still shows the kind's extra_args.
func buildOptionsInertForm(palette theme.Palette) Model {
	f := NewOptionsField(palette)
	f.SetKind("codex", KindOptions{ExtraArgs: []string{"--full-auto"}})
	return fieldFrame(palette, f)
}

func TestFrames_OptionsInert(t *testing.T) {
	assertFrame(t, "options-inert-80x24", buildOptionsInertForm(theme.Default()), 80, 24)
}

// narrowFloorCases is the six (field, terminal width) pairs where #281's
// per-zone create button moved the key ladder's floor one column earlier
// than the single 10-cell face that preceded it: the button grew to 11
// cells in these zones, the rung budget lost that cell, and the zone's
// narrowest lead stopped fitting.
//
// Re-measured rather than copied (2026-09-20). Replaying the whole
// ladder over terminals 24-160 against the tree at 15f3934 -- the old
// button AND the old rungs, which differ for the empty title --
// reproduces createKey's doc comment exactly: the chosen rung differs at
// 74 (zone, width) pairs, six of which are these. `lead` is what the
// footer taught at this width before #281, and teaches again one column
// wider, which is what makes each of these a cliff rather than a floor
// the zone never leaves.
//
// The widths are TERMINAL widths, two more than the boxWidth renderFooter
// measures against (contentBox's sideMargin on each side) -- which is
// the reading that makes the doc comment's numbers and the fixtures below
// the same numbers.
//
// Four of the six reach the footer through the zone table, which is what
// was measured. Two do NOT: with the part cursor where these builders
// leave it, WorktreeField and OptionsField answer for themselves
// (footerHinter), and the cliff still lands on the same column because
// their narrowest lead is the same string the table's is -- "↑↓ part"
// and "←→ value". So these two frames pin a coincidence of wording as
// well as a width, and if either override is reworded the boundary below
// will say so rather than the frame quietly moving.
//
// Neither of those two overrides ever returns an empty slice, so
// footerRungsFor never falls back for them and production never renders
// zoneRungs(ZoneWorktree) or zoneRungs(ZoneOptions) at all. Their table
// entries are duplicates of the override's strings held to nothing but
// TestFooterRungs_PerZone's rungs[0]: rewording the TABLE's narrowest
// worktree rung leaves the whole repo green, where rewording the
// override's -- or the table's for a zone with no override, such as
// issue -- is caught here within one column. Reported by the review of
// #289 and deliberately left: pinning a table to the override that
// shadows it is a different change from the three #289 asks for.
var narrowFloorCases = []struct {
	name  string
	build func(theme.Palette) Model
	width int
	lead  string
}{
	{"worktree", buildWorktreePanelForm, 35, "↑↓ part"},
	{"options", func(p theme.Palette) Model { return buildOptionsPanelForm(p, false) }, 36, "←→ value"},
	{"issue", buildIssuePanelForm, 37, "↑↓ choose"},
	{"dir", buildDirPanelForm, 37, "↑↓ choose"},
	{"placement", func(p theme.Palette) Model { return buildPlacementPanelForm(p, false) }, 37, "←→ choose"},
	{"agent", buildAgentPanelForm, 40, "↑↓ all kinds"},
}

// TestFrames_NarrowBandAtTheLaddersFloor is #289 item 3. Below 57 columns
// this suite had exactly two frames -- account-panel-44x12, and a
// degraded-40x8 built from STUB sections, which zoneFor maps onto
// ZonePlacement -- so one real field was pinned at one width in the whole
// 33-44 band, and not one of the six widths #281 moved.
//
// One frame each, at the width where the field's own footer falls to the
// constant tail. They are fixtures of the WHOLE screen, so what they pin
// is not only the footer: each panel's own clipping at 35-37 columns is
// pinned nowhere else, and the worktree row's value ellipsis gets a
// second fixture beside account-panel-44x12's.
//
// What they do NOT reach, contrary to an earlier draft of this comment:
// the label column, which holds its full labelColWidth until inner drops
// below labelColWidth+minValueWidth -- terminal width 23, far below this
// band, and pinned by no fixture in the suite. 40 columns is not new
// either; degraded-40x8 is there already, with stub sections and four
// fewer lines.
//
// The footer they show is the accepted answer, not a defect -- see
// narrowFloorCases, and createKey's doc comment for why a shorter lead
// would move the cliff rather than remove it.
func TestFrames_NarrowBandAtTheLaddersFloor(t *testing.T) {
	for _, c := range narrowFloorCases {
		assertFrame(t, fmt.Sprintf("%s-floor-%dx12", c.name, c.width), c.build(theme.Default()), c.width, 12)
	}
}

// TestFooter_TheFloorIsACliffAtTheMeasuredWidth is the other half of the
// same six pairs, and the half a golden frame cannot state: a frame at 35
// records what the worktree row's footer says at 35, and says nothing
// about whether 34 or 36 says it too. This renders each field at its
// floor width and one column wider and asserts the ladder steps back up,
// so the six widths are pinned as a BOUNDARY rather than as six points.
//
// It goes through ViewAt rather than renderFooter deliberately: the
// widths in play are terminal widths, and the two-cell margin between a
// terminal and the box the footer is measured against is exactly the
// arithmetic that made #281's measurements hard to check against a
// fixture.
func TestFooter_TheFloorIsACliffAtTheMeasuredWidth(t *testing.T) {
	floor := tailRungs(false)[len(tailRungs(false))-1]
	for _, c := range narrowFloorCases {
		t.Run(c.name, func(t *testing.T) {
			at := func(width int) string {
				lines := strings.Split(ansi.Strip(c.build(theme.Default()).ViewAt(width, 12)), "\n")
				return strings.TrimSpace(lines[len(lines)-1])
			}
			if got := at(c.width); !strings.HasPrefix(got, floor) {
				t.Errorf("at %d columns the footer = %q, want it to have fallen to %q", c.width, got, floor)
			}
			if got := at(c.width); strings.Contains(got, c.lead) {
				t.Errorf("at %d columns the footer = %q, want %q not to fit", c.width, got, c.lead)
			}
			if got := at(c.width + 1); !strings.HasPrefix(got, c.lead) {
				t.Errorf("at %d columns the footer = %q, want the zone's own %q back", c.width+1, got, c.lead)
			}
		})
	}
}
