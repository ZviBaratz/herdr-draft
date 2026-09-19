package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/form"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
)

// These pin #199's form half. A branch git cannot hold passed the duplicate
// refusal (#147), because `git show-ref --verify` answers "absent" for one,
// and then herdr either trimmed it into another name -- "zvi/old " into the
// user's existing zvi/old, checked out over the base -- or failed the create
// at its first step. The form now refuses it where the branch is shown.

// settledBranchForm is a form settled on a repository with the worktree on
// -- so a branch will be made -- and a title typed and checked, so a submit
// has nothing left to refuse but the branch.
func settledBranchForm(t *testing.T, git *fakeGit) Model {
	t.Helper()
	runner := &submitFakeRunner{topo: herdrc.CreatedTopology{WorkspaceID: "ws-1", TabID: "t-1", PaneID: "pane-1"}}
	m := settle(t, newSubmitTestModel(t, runner, testSetup{
		Git:    git,
		Ctx:    herdrc.Context{WorkspaceCwd: "/repo"},
		Config: config.Config{DefaultWorktree: true},
	}))
	m.width, m.height = 104, 32
	m = landTitle(t, m, retypeTitle(&m, "Fresh"))
	if !m.worktree.Enabled() || !m.worktree.On() {
		t.Fatal("test setup: the worktree should be on, or no branch is made")
	}
	return m
}

// retypeBranch types branch into the worktree's branch input and runs the
// reaction a keystroke through Update runs after it, returning the checks it
// scheduled.
func retypeBranch(m *Model, branch string) []tea.Cmd {
	m.worktree.SetBranch(branch, false)
	return m.reactToChanges()
}

// branchPanel is the worktree field's panel as it draws, whatever holds focus.
func branchPanel(m Model) string {
	return ansi.Strip(m.worktree.Panel(80, m.worktree.PanelRows()))
}

// TestSubmit_RefusesABranchGitCannotHold is the issue's own table, typed
// into the branch input and submitted before the check of it lands -- the
// fast path #137 made wait. The submit is refused on the worktree row, the
// cursor lands in the branch input the fix is typed into, and the panel says
// what is wrong with the name.
func TestSubmit_RefusesABranchGitCannotHold(t *testing.T) {
	for _, tc := range []struct{ branch, reason string }{
		{"zvi/old ", "ends with a space"},
		{" zvi/old", "begins with a space"},
		{"zvi/a..b", `contains ".."`},
		{"zvi/a~1", "contains '~'"},
		{"zvi/a:b", "contains ':'"},
		{"zvi/a b", "contains a space"},
		{"zvi/old.lock", `ends with ".lock"`},
		{"zvi/old" + string(rune(0xa0)), "ends with whitespace (U+00A0)"},
	} {
		t.Run(tc.branch, func(t *testing.T) {
			m := settledBranchForm(t, newFakeGit())
			cmds := retypeBranch(&m, tc.branch)
			// ⌃S from the project row, which the refusal visibly moves away
			// from.
			m.form.FocusByID("dir")

			next, _ := m.Update(form.SubmitMsg{})
			m = next.(Model)
			m, _ = landTitleCheck(t, m, fireTitleDebounce(t, &m, cmds))
			if m.submitting {
				t.Fatalf("the submit went ahead onto branch %q", m.submitInput.Branch)
			}
			if got := m.form.FocusedID(); got != "worktree" {
				t.Errorf("focus ended on %q, want the worktree row, which says why", got)
			}
			if rung := m.worktree.FooterRungs()[0]; !strings.Contains(rung, "type to edit") {
				t.Errorf("footer rung %q: want the cursor in the branch input, where the fix is typed", rung)
			}
			if panel := branchPanel(m); !strings.Contains(panel, "invalid branch name  ") || !strings.Contains(panel, tc.reason) {
				t.Errorf("worktree panel does not say what is wrong (%s):\n%s", tc.reason, panel)
			}
		})
	}
}

// TestSubmitValidation_ARefusedBranchSaysWhy: the refusal does not depend on
// the debounced check having landed for the branch as it stands -- it asks
// BranchRefusal itself -- and neither does its explanation. A branch moved
// by anything that does not reschedule the check is refused with the verdict
// on screen, not with the cursor parked beside nothing.
func TestSubmitValidation_ARefusedBranchSaysWhy(t *testing.T) {
	m := settledBranchForm(t, newFakeGit())
	m.worktree.SetBranch("zvi/old ", false)

	if _, blocked := m.checkSubmitValidation(); !blocked {
		t.Fatal("validation passed a branch with a trailing space")
	}
	if panel := branchPanel(m); !strings.Contains(panel, "invalid branch name  ends with a space") {
		t.Errorf("the refusal left no verdict on the worktree panel:\n%s", panel)
	}
}

// TestSubmit_ARefusedBranchSaysWhyInAShortWindow: the popup is 32 rows, but
// herdr clamps it to the terminal and Route B runs in any pane. However short
// the window, a submit refused over the branch leaves the reason on screen --
// found by the #199 review, where below 16 rows the panel's floor dropped the
// verdict and kept the refusal.
func TestSubmit_ARefusedBranchSaysWhyInAShortWindow(t *testing.T) {
	for _, h := range []int{32, 24, 18, 16, 15, 14, 12} {
		m := settledBranchForm(t, newFakeGit())
		next, _ := m.Update(tea.WindowSizeMsg{Width: 104, Height: h})
		m = next.(Model)
		m = landTitle(t, m, retypeBranch(&m, "zvi/old "))
		m.form.FocusByID("agent")

		next, _ = m.Update(form.SubmitMsg{})
		m = next.(Model)
		if m.submitting {
			t.Fatalf("height %d: the submit went ahead", h)
		}
		if view := ansi.Strip(m.View().Content); !strings.Contains(view, "invalid branch name  ends with a space") {
			t.Errorf("height %d: the submit was refused (focus %q) with no reason on screen:\n%s", h, m.form.FocusedID(), view)
		}
	}
}

// TestBranchVerdict_LandsWithTheDebouncedCheck: the verdict is shown when the
// title/branch check lands, not on each keystroke -- every branch typed with
// a prefix passes through "zvi/", which is not a branch name, and a verdict
// that flashed at each "/" would be noise. An edit takes it down at once.
func TestBranchVerdict_LandsWithTheDebouncedCheck(t *testing.T) {
	m := settledBranchForm(t, newFakeGit())

	cmds := retypeBranch(&m, "zvi/")
	if panel := branchPanel(m); strings.Contains(panel, "invalid branch name") {
		t.Fatalf("the verdict was drawn before the check landed:\n%s", panel)
	}
	m = landTitle(t, m, cmds)
	if panel := branchPanel(m); !strings.Contains(panel, "empty path component") {
		t.Fatalf("the check landed and the panel has no verdict:\n%s", panel)
	}

	retypeBranch(&m, "zvi/x")
	if panel := branchPanel(m); strings.Contains(panel, "invalid branch name") {
		t.Errorf("the verdict about %q is still drawn over %q:\n%s", "zvi/", m.worktree.Branch(), panel)
	}
}

// TestTitleCheck_DecidesValidityBeforeAskingGit: the check does not ask git
// whether a name it cannot hold exists. `show-ref` would say "absent", and a
// duplicate verdict built on that answer -- either way -- is about a branch
// nobody could make.
func TestTitleCheck_DecidesValidityBeforeAskingGit(t *testing.T) {
	git := newFakeGit()
	git.branchExists = true
	m := settledBranchForm(t, git)
	asked := git.branchExistsCalls

	m = landTitle(t, m, retypeBranch(&m, "zvi/old "))
	if git.branchExistsCalls != asked {
		t.Errorf("git was asked whether %q exists (%d calls)", "zvi/old ", git.branchExistsCalls-asked)
	}
	if m.titleDupBlocked {
		t.Error("a duplicate verdict stands for a branch whose existence was never the question")
	}
}

// TestTitleNote_NamesOnlyABranchThatCanBeMade: the title panel's resting note
// says what the title will produce -- `branch will be <branch>` -- and must
// not promise a branch the submit is about to refuse. With a trailing space
// that promise would read exactly like the user's existing branch.
func TestTitleNote_NamesOnlyABranchThatCanBeMade(t *testing.T) {
	m := settledBranchForm(t, newFakeGit())
	titlePanel := func() string { return ansi.Strip(m.title.Panel(80, m.title.PanelRows())) }

	m = landTitle(t, m, retypeBranch(&m, "zvi/fresh"))
	if !strings.Contains(titlePanel(), "branch will be zvi/fresh") {
		t.Fatalf("test setup: the title panel should name a valid branch:\n%s", titlePanel())
	}
	m = landTitle(t, m, retypeBranch(&m, "zvi/old "))
	if got := titlePanel(); strings.Contains(got, "branch will be") {
		t.Errorf("the title panel promises a branch the submit refuses:\n%s", got)
	}
}

// TestSubmit_AnInvalidBranchWithoutAWorktreeIsNotRefused: with the worktree
// off no branch is made, so there is nothing to refuse -- the condition the
// duplicate check has, and `create`'s.
func TestSubmit_AnInvalidBranchWithoutAWorktreeIsNotRefused(t *testing.T) {
	m := settledBranchForm(t, newFakeGit())
	m.worktree.SetOn(false)
	cmds := retypeBranch(&m, "zvi/a b")

	next, _ := m.Update(form.SubmitMsg{})
	m = next.(Model)
	m, out := landTitleCheck(t, m, fireTitleDebounce(t, &m, cmds))
	if !m.submitting {
		t.Fatalf("a submit with no worktree was refused over its branch (focus %q)", m.form.FocusedID())
	}
	drainSubmitProgress(t, m, submitChain(t, out))
}

// TestSubmit_RefusesAnEmptyBranch: an emptied branch input is refused at
// submit, the way an empty title is (#199's open question, decided
// 2026-09-19). herdr would invent a worktree/<adjective>-<noun>-NNNN branch
// that ignores branch_prefix and that a clean cannot show this run made.
//
// Like "title required", it is said at submit and not while the form is
// being filled in: the debounced check does not call an empty branch wrong,
// or the worktree panel would say so before anyone had finished typing.
func TestSubmit_RefusesAnEmptyBranch(t *testing.T) {
	m := settledBranchForm(t, newFakeGit())
	m = landTitle(t, m, retypeBranch(&m, ""))
	if panel := branchPanel(m); strings.Contains(panel, "branch name required") {
		t.Errorf("the emptied branch was called wrong before any submit:\n%s", panel)
	}
	m.form.FocusByID("dir")

	next, _ := m.Update(form.SubmitMsg{})
	m = next.(Model)
	if m.submitting {
		t.Fatal("a submit with an empty branch went ahead")
	}
	if got := m.form.FocusedID(); got != "worktree" {
		t.Errorf("focus ended on %q, want the worktree row, which says why", got)
	}
	if rung := m.worktree.FooterRungs()[0]; !strings.Contains(rung, "type to edit") {
		t.Errorf("footer rung %q: want the cursor in the branch input", rung)
	}
	if panel := branchPanel(m); !strings.Contains(panel, "branch name required") {
		t.Errorf("worktree panel does not say why:\n%s", panel)
	}
}

// TestBranchVerdict_NoneInTheOpeningState: the form opens with the worktree
// on and no branch yet, because the branch is derived from a title nobody
// has typed. That state must not read as a refusal.
func TestBranchVerdict_NoneInTheOpeningState(t *testing.T) {
	runner := &submitFakeRunner{topo: herdrc.CreatedTopology{WorkspaceID: "ws-1", TabID: "t-1", PaneID: "pane-1"}}
	m := settle(t, newSubmitTestModel(t, runner, testSetup{
		Git:    newFakeGit(),
		Ctx:    herdrc.Context{WorkspaceCwd: "/repo"},
		Config: config.Config{DefaultWorktree: true},
	}))
	if !m.worktree.On() || m.worktree.Branch() != "" {
		t.Fatalf("test setup: want the worktree on and no branch, got on %v, branch %q", m.worktree.On(), m.worktree.Branch())
	}
	// The worktree turning on schedules a title check, which lands 150 ms
	// after the form opens -- with no title and no branch. settle drops title
	// checks, so this lands that one.
	m = landTitle(t, m, []tea.Cmd{m.scheduleTitleCheck(m.title.Value(), m.worktree.Branch(), m.dir.Value(), m.worktree.On())})
	if panel := branchPanel(m); strings.Contains(panel, "branch name required") || strings.Contains(panel, "invalid branch name") {
		t.Errorf("the opening state reads as a refusal:\n%s", panel)
	}
}
