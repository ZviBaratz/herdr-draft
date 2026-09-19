package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/ZviBaratz/herdr-draft/internal/form"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
)

// These pin #195: a submit that arrives while the project row's check is in
// flight waits for the check of the value the row now holds, then runs
// validation on its answers and goes on -- with no second ⌃S. Before, it was
// built from the previous project's answers: whether the directory exists,
// whether it is a repository, its .herdr-draft.toml, its memory, and whether
// it is a lane.

// settledRepoForm is #195's starting point: a form settled on a repository,
// with a title and the worktree on.
func settledRepoForm(t *testing.T, git *fakeGit) Model {
	t.Helper()
	runner := &submitFakeRunner{topo: herdrc.CreatedTopology{WorkspaceID: "ws-1", TabID: "t-1", PaneID: "pane-1"}}
	m := settle(t, newSubmitTestModel(t, runner, testSetup{Git: git, Ctx: herdrc.Context{WorkspaceCwd: "/repo"}}))
	m.title.SetTitle("Fix pagination", false)
	m.worktree.SetOn(true)
	m.worktree.SetBranch("zvi/fix-pagination", false)
	if !m.worktree.Enabled() || !m.worktree.On() {
		t.Fatal("test setup: the worktree is not on for the repository")
	}
	return m
}

// retypeProject types path into the project row and runs the reaction a
// keystroke through Update runs after it, returning the checks it scheduled.
// Typed a key at a time through Update, every keystroke would also batch the
// input's blink timer, which really sleeps.
func retypeProject(m *Model, path string) []tea.Cmd {
	m.form.FocusByID("dir")
	typeDir(m, path)
	return m.reactToChanges()
}

// fireDirDebounce runs the scheduled checks and delivers the project row's
// debounce, returning the directory check it starts -- in flight, not yet
// landed.
func fireDirDebounce(t *testing.T, m *Model, cmds []tea.Cmd) tea.Cmd {
	t.Helper()
	for _, c := range cmds {
		for _, msg := range flatten(c) {
			if d, ok := msg.(dirDebounceMsg); ok {
				next, check := m.Update(d)
				*m = next.(Model)
				if check == nil {
					t.Fatal("the project row's debounce started no check")
				}
				return check
			}
		}
	}
	t.Fatal("no project check was scheduled")
	return nil
}

// landDirCheck delivers the directory check's answer, returning what the
// model did with it.
func landDirCheck(t *testing.T, m Model, check tea.Cmd) (Model, tea.Cmd) {
	t.Helper()
	res, ok := check().(dirResultMsg)
	if !ok {
		t.Fatal("the project check did not answer with a dirResultMsg")
	}
	next, out := m.Update(res)
	return next.(Model), out
}

// submitChain is the submit pipeline among what a landing check returned. A
// check whose answer moves the worktree toggle or the branch re-runs the
// title check too (handleDirResult), and batches it beside the submit.
func submitChain(t *testing.T, out tea.Cmd) tea.Cmd {
	t.Helper()
	for _, msg := range flatten(out) {
		if p, ok := msg.(submitProgressMsg); ok {
			return func() tea.Msg { return p }
		}
	}
	t.Fatal("the landing check returned no submit pipeline")
	return nil
}

// TestSubmit_WaitsForTheProjectCheckThenRefusesAMissingDirectory is #195's
// own scenario: the project row retyped as a path that is not there, and
// submitted before its check lands. The submit waits for the check, and the
// check's answer refuses it on the project row -- rather than a worktree
// being sent to a repository the form has left.
func TestSubmit_WaitsForTheProjectCheckThenRefusesAMissingDirectory(t *testing.T) {
	git := newFakeGit()
	m := settledRepoForm(t, git)

	git.dirExists, git.isGitRepo = false, false
	cmds := retypeProject(&m, "/nowhere")
	if got := m.dir.Value(); got != "/nowhere" {
		t.Fatalf("test setup: project = %q, want /nowhere", got)
	}
	// ⌃S submits from any row. Sent from the title, where the refusal
	// visibly moves focus away from.
	m.form.FocusByID("title")

	next, _ := m.Update(form.SubmitMsg{})
	m = next.(Model)
	if m.submitting {
		t.Fatalf("the submit started before /nowhere was checked: project %q, git repo %v, worktree %v",
			m.submitInput.ProjectDir, m.submitInput.IsGitRepo, m.submitInput.UseWorktree)
	}

	m, _ = landDirCheck(t, m, fireDirDebounce(t, &m, cmds))
	if m.submitting {
		t.Fatal("the submit went ahead for a directory that is not there")
	}
	if got := m.form.FocusedID(); got != "dir" {
		t.Errorf("focus ended on %q, want the project row, which says why", got)
	}
}

// TestSubmit_WaitsForTheProjectCheckThenGoesOnWithItsAnswers: when the new
// project is fine, the held submit goes on by itself once its check lands,
// built from the new project's answers -- here a plain directory, so no
// worktree.
func TestSubmit_WaitsForTheProjectCheckThenGoesOnWithItsAnswers(t *testing.T) {
	git := newFakeGit()
	m := settledRepoForm(t, git)

	git.isGitRepo = false
	cmds := retypeProject(&m, "/scratch")

	next, _ := m.Update(form.SubmitMsg{})
	m = next.(Model)
	if m.submitting {
		t.Fatalf("the submit started before /scratch was checked: git repo %v, worktree %v",
			m.submitInput.IsGitRepo, m.submitInput.UseWorktree)
	}

	m, out := landDirCheck(t, m, fireDirDebounce(t, &m, cmds))
	if !m.submitting {
		t.Fatal("the check landed and the held submit did not go on")
	}
	in := m.submitInput
	if in.ProjectDir != "/scratch" || in.IsGitRepo || in.UseWorktree {
		t.Errorf("submitted project %q, git repo %v, worktree %v; want /scratch, a plain directory, no worktree",
			in.ProjectDir, in.IsGitRepo, in.UseWorktree)
	}
	drainSubmitProgress(t, m, submitChain(t, out))
}

// TestSubmit_OnlyTheCheckOfTheCurrentValueReleasesIt: the user may keep
// typing while a submit is held. A check that was already running for the
// previous value and lands afterwards is not the answer the submit is
// waiting for; the one for the value the row now holds is.
func TestSubmit_OnlyTheCheckOfTheCurrentValueReleasesIt(t *testing.T) {
	git := newFakeGit()
	m := settledRepoForm(t, git)

	git.isGitRepo = false
	first := fireDirDebounce(t, &m, retypeProject(&m, "/scratch"))
	next, _ := m.Update(form.SubmitMsg{})
	m = next.(Model)
	if m.submitting {
		t.Fatal("the submit started before /scratch was checked")
	}

	// /scratch's check is running when the user types on.
	cmds := retypeProject(&m, "-two")
	if got := m.dir.Value(); got != "/scratch-two" {
		t.Fatalf("test setup: project = %q, want /scratch-two", got)
	}
	m, _ = landDirCheck(t, m, first)
	if m.submitting {
		t.Fatal("the check for /scratch released a submit of /scratch-two")
	}

	m, out := landDirCheck(t, m, fireDirDebounce(t, &m, cmds))
	if !m.submitting {
		t.Fatal("the check for the current value landed and the held submit did not go on")
	}
	if got := m.submitInput.ProjectDir; got != "/scratch-two" {
		t.Errorf("submitted project %q, want /scratch-two", got)
	}
	drainSubmitProgress(t, m, submitChain(t, out))
}

// TestSubmit_AFreshFormWaitsForItsOpeningCheck: the form opens with its
// project's check scheduled (New's initCmds) and nothing landed yet, which
// is also what gives the worktree row its repository and the project its
// defaults. A submit that quick is held like any other.
func TestSubmit_AFreshFormWaitsForItsOpeningCheck(t *testing.T) {
	runner := &submitFakeRunner{topo: herdrc.CreatedTopology{WorkspaceID: "ws-1", TabID: "t-1", PaneID: "pane-1"}}
	m := newSubmitTestModel(t, runner, testSetup{Ctx: herdrc.Context{WorkspaceCwd: "/repo"}})
	m.title.SetTitle("Fix pagination", false)

	next, _ := m.Update(form.SubmitMsg{})
	m = next.(Model)
	if m.submitting {
		t.Fatal("the submit started before the opening project was checked")
	}

	m, out := landDirCheck(t, m, fireDirDebounce(t, &m, m.initCmds))
	if !m.submitting {
		t.Fatal("the opening check landed and the held submit did not go on")
	}
	if in := m.submitInput; in.ProjectDir != "/repo" || !in.IsGitRepo {
		t.Errorf("submitted project %q, git repo %v; want /repo, the repository its check found", in.ProjectDir, in.IsGitRepo)
	}
	drainSubmitProgress(t, m, submitChain(t, out))
}
