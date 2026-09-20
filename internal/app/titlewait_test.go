package app

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/form"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
)

// These pin #137: a submit waits for the title-duplicate check the way #196
// made it wait for the project row's. The check runs 150 ms after the last
// edit to the title, the branch or the project, and validation reads the
// verdict it leaves behind, so a submit inside that window used to be
// validated against the previous title's verdict -- the fast path, a title
// typed and ↵, went past the duplicate refusal.

// takenLabel is the label of a workspace open when the form opened, so a
// title of that name is a duplicate.
const takenLabel = "Taken"

// settledTitleForm is a form settled on a repository with the worktree on and
// a workspace labelled takenLabel open.
func settledTitleForm(t *testing.T, git *fakeGit) Model {
	t.Helper()
	runner := &submitFakeRunner{topo: herdrc.CreatedTopology{WorkspaceID: "ws-1", TabID: "t-1", PaneID: "pane-1"}}
	m := settle(t, newSubmitTestModel(t, runner, testSetup{
		Git:        git,
		Ctx:        herdrc.Context{WorkspaceCwd: "/repo"},
		Workspaces: []herdrc.WorkspaceInfo{{WorkspaceID: "w9", Label: takenLabel}},
	}))
	m.width, m.height = 104, 32
	return m
}

// retypeTitle types title into the title row and runs the reaction a
// keystroke through Update runs after it, returning the checks it scheduled.
func retypeTitle(m *Model, title string) []tea.Cmd {
	m.title.SetTitle(title, false)
	return m.reactToChanges()
}

// fireTitleDebounce runs the scheduled checks and delivers every title
// debounce among them, returning the check the current one starts. The
// others are superseded and start nothing.
func fireTitleDebounce(t *testing.T, m *Model, cmds []tea.Cmd) tea.Cmd {
	t.Helper()
	var check tea.Cmd
	for _, c := range cmds {
		for _, msg := range flatten(c) {
			if d, ok := msg.(titleDebounceMsg); ok {
				next, run := m.Update(d)
				*m = next.(Model)
				if run != nil {
					check = run
				}
			}
		}
	}
	if check == nil {
		t.Fatal("no current title check was scheduled")
	}
	return check
}

// landTitleCheck delivers the title check's answer, returning what the model
// did with it.
func landTitleCheck(t *testing.T, m Model, check tea.Cmd) (Model, tea.Cmd) {
	t.Helper()
	res, ok := check().(titleResultMsg)
	if !ok {
		t.Fatal("the title check did not answer with a titleResultMsg")
	}
	next, out := m.Update(res)
	return next.(Model), out
}

// landTitle fires and lands the current title check among cmds, for a test
// whose subject is some other check: a submit waits for this one too.
func landTitle(t *testing.T, m Model, cmds []tea.Cmd) Model {
	t.Helper()
	m, _ = landTitleCheck(t, m, fireTitleDebounce(t, &m, cmds))
	return m
}

// TestSubmit_WaitsForTheTitleCheckThenRefusesADuplicate is #137's own
// scenario: a duplicate title typed and submitted before its check lands.
// The submit waits for the check, and the check's answer refuses it on the
// title row.
func TestSubmit_WaitsForTheTitleCheckThenRefusesADuplicate(t *testing.T) {
	m := settledTitleForm(t, newFakeGit())
	cmds := retypeTitle(&m, takenLabel)
	// ⌃S submits from any row. Sent from the project row, where the refusal
	// visibly moves focus away from.
	m.form.FocusByID("dir")

	next, _ := m.Update(form.SubmitMsg{})
	m = next.(Model)
	if m.submitting {
		t.Fatalf("the submit started before %q was checked, as a duplicate of an open workspace's label", takenLabel)
	}

	m, _ = landTitleCheck(t, m, fireTitleDebounce(t, &m, cmds))
	if m.submitting {
		t.Fatal("the submit went ahead with a title an open workspace already carries")
	}
	if got := m.form.FocusedID(); got != "title" {
		t.Errorf("focus ended on %q, want the title row, which says why", got)
	}
}

// TestSubmit_WaitsForTheTitleCheckThenGoesOn: a title that is no duplicate
// goes on by itself once its check lands, with no second ⌃S.
func TestSubmit_WaitsForTheTitleCheckThenGoesOn(t *testing.T) {
	m := settledTitleForm(t, newFakeGit())
	cmds := retypeTitle(&m, "Fresh")

	next, _ := m.Update(form.SubmitMsg{})
	m = next.(Model)
	if m.submitting {
		t.Fatal("the submit started before its title was checked")
	}

	m, out := landTitleCheck(t, m, fireTitleDebounce(t, &m, cmds))
	if !m.submitting {
		t.Fatal("the title check landed and the held submit did not go on")
	}
	if got := m.submitInput.Title; got != "Fresh" {
		t.Errorf("submitted title %q, want Fresh", got)
	}
	drainSubmitProgress(t, m, submitChain(t, out))
}

// TestSubmit_AFixedDuplicateIsNotRefusedOnItsOldVerdict: the check has
// already called the title a duplicate, and the user edits it to one that is
// not. Scheduling the new check takes the warning off the title panel, so a
// submit refused on the old verdict was refused with nothing on screen
// saying why. It now waits for the new verdict and goes on.
func TestSubmit_AFixedDuplicateIsNotRefusedOnItsOldVerdict(t *testing.T) {
	m := settledTitleForm(t, newFakeGit())
	m = landTitle(t, m, retypeTitle(&m, takenLabel))
	if !m.titleDupBlocked {
		t.Fatalf("test setup: %q was not found to be a duplicate", takenLabel)
	}

	cmds := retypeTitle(&m, "Fresh")
	next, _ := m.Update(form.SubmitMsg{})
	m = next.(Model)
	m, out := landTitleCheck(t, m, fireTitleDebounce(t, &m, cmds))
	if !m.submitting {
		t.Fatalf("a submit of Fresh did not go on once its check found no duplicate (focus %q)", m.form.FocusedID())
	}
	drainSubmitProgress(t, m, submitChain(t, out))
}

// TestSubmit_AStaleTitleAnswerDoesNotEndTheWait: a check that was running for
// a title the user has typed past lands and is dropped, and a ⌃S sent after
// it is still held -- the stale verdict did not count as the current one.
func TestSubmit_AStaleTitleAnswerDoesNotEndTheWait(t *testing.T) {
	m := settledTitleForm(t, newFakeGit())
	first := fireTitleDebounce(t, &m, retypeTitle(&m, "Fresh"))
	retypeTitle(&m, takenLabel)
	m, _ = landTitleCheck(t, m, first)

	next, _ := m.Update(form.SubmitMsg{})
	m = next.(Model)
	if m.submitting {
		t.Fatalf("a submit of %q went ahead after only Fresh's check landed", m.submitInput.Title)
	}
}

// TestSubmit_AProjectChangeWaitsForTheTitleCheckItsDefaultsStart: the title
// check's answer depends on the project -- the branch is looked up there, and
// only while a worktree is on. When the new project's defaults turn the
// worktree on, the dir check that releases a held submit also schedules a
// fresh title check, and the submit waits for that one too. It used to read
// the verdict computed with the worktree off: the #196 review's finding 2.
func TestSubmit_AProjectChangeWaitsForTheTitleCheckItsDefaultsStart(t *testing.T) {
	git := newFakeGit()
	git.isGitRepo = false
	runner := &submitFakeRunner{topo: herdrc.CreatedTopology{WorkspaceID: "ws-1", TabID: "t-1", PaneID: "pane-1"}}
	m := settle(t, newSubmitTestModel(t, runner, testSetup{
		Git:    git,
		Ctx:    herdrc.Context{WorkspaceCwd: "/scratch"},
		Config: config.Config{DefaultWorktree: true},
	}))
	m = landTitle(t, m, retypeTitle(&m, "Fix pagination"))
	if m.worktree.Enabled() || m.worktree.On() {
		t.Fatal("test setup: /scratch should have no worktree")
	}

	git.isGitRepo, git.branchExists = true, true
	cmds := retypeProject(&m, "/repo")
	next, _ := m.Update(form.SubmitMsg{})
	m = next.(Model)
	// The title check the edit scheduled lands first: it is one git call to
	// the dir check's four. It was asked with the worktree off.
	m, _ = landTitleCheck(t, m, fireTitleDebounce(t, &m, cmds))
	m, out := landDirCheck(t, m, fireDirDebounce(t, &m, cmds))
	if !m.worktree.On() {
		t.Fatal("test setup: /repo's default did not turn the worktree on")
	}
	if m.submitting {
		t.Fatalf("the submit went on with a title verdict computed with the worktree off: branch %q exists", m.submitInput.Branch)
	}

	m, _ = landTitleCheck(t, m, fireTitleDebounce(t, &m, []tea.Cmd{out}))
	if m.submitting {
		t.Fatal("the submit went ahead onto a branch that already exists")
	}
	if got := m.form.FocusedID(); got != "title" {
		t.Errorf("focus ended on %q, want the title row, which says why", got)
	}
}

// TestSubmit_ATitleCheckFromBeforeAClearDoesNotReleaseIt is #201 for the
// title check: ⌃R⌃R rebuilds the form, and a title check the discarded form
// had in flight lands afterwards. It must not pass for the rebuilt form's own
// check -- which it did while the rebuild started the counter from zero and
// the two versions could meet, and which now would also release a submit
// held for it, onto the old title's verdict.
//
// The discarded form's check is issued a few edits in, so that without the
// carry the rebuilt form's check for Fresh reaches the same version: the
// rebuilt form is edited up to one short of it first. With the carry it is
// already past, and nothing needs editing.
func TestSubmit_ATitleCheckFromBeforeAClearDoesNotReleaseIt(t *testing.T) {
	m := settledTitleForm(t, newFakeGit())
	for _, title := range []string{"a", "ab", "abc", "abcd", "abcde"} {
		retypeTitle(&m, title)
	}
	before, ok := fireTitleDebounce(t, &m, retypeTitle(&m, takenLabel))().(titleResultMsg)
	if !ok || !before.labelTaken {
		t.Fatal("test setup: the discarded form's check did not find the duplicate")
	}

	next, _ := m.Update(form.ClearRequestedMsg{})
	m = settle(t, next.(Model))
	for i := 0; m.reqs.title < before.req.version-1; i++ {
		retypeTitle(&m, fmt.Sprintf("draft %d", i))
	}
	cmds := retypeTitle(&m, "Fresh")
	next, _ = m.Update(form.SubmitMsg{})
	m = next.(Model)
	if m.submitting {
		t.Fatal("test setup: the submit was not held for Fresh's check")
	}

	next, _ = m.Update(before)
	m = next.(Model)
	if m.submitting || m.titleDupBlocked || !m.submitHeld {
		t.Fatalf("the check for %q, from before the clear, landed in the rebuilt form: submitting %v, duplicate %v, still held %v",
			before.req.key, m.submitting, m.titleDupBlocked, m.submitHeld)
	}
	m, out := landTitleCheck(t, m, fireTitleDebounce(t, &m, cmds))
	if !m.submitting {
		t.Fatalf("a submit of Fresh did not go on once its own check landed (focus %q)", m.form.FocusedID())
	}
	drainSubmitProgress(t, m, submitChain(t, out))
}
