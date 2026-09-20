package app

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/form"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
)

// This file is #212: the form's once-per-repo background `git fetch --prune`
// re-lists the branches when it finishes, and the base the user picked from
// the previous list may not be in the new one. widgets.Picker keeps a
// vanished row's INDEX, so the selection became whichever branch now sat
// there -- silently, with the worktree row naming it and the plan built from
// it. The rule is #194's, asked of the user's own base rather than a tier's:
// keep it on offer across the re-list, then settle it. Still names a commit
// (it merely fell out of the 50-branch window): kept. Names none (it was
// pruned): the HEAD row, with a note saying which base went.

// chosenBaseModel is a form opened on /repo-a -- branch list main,
// origin/gone and origin/next, worktree on -- with the user's own cursor
// moved to origin/gone. The state #212's window opens in: a base picked from
// the list while the fetch is still out.
func chosenBaseModel(t *testing.T, git *fakeGit) Model {
	t.Helper()
	git.listBranchesResult = []string{"main", "origin/gone", "origin/next"}
	git.currentBranchResult = "main"
	return chooseGoneBase(t, memoryModel(t, "/repo-a", memoryFor(map[string]config.ProjectDefaults{
		"/repo-a": {Worktree: ptrBool(true)},
	}), git))
}

// chooseGoneBase drives the real keys that move the base picker's cursor
// onto origin/gone -- the user deciding, which is what sets baseTouched.
func chooseGoneBase(t *testing.T, m Model) Model {
	t.Helper()
	m.form.FocusByID("worktree")
	for _, k := range []tea.KeyPressMsg{
		{Code: tea.KeyDown}, {Code: tea.KeyDown}, // chips -> branch -> base
		{Code: tea.KeyDown}, {Code: tea.KeyDown}, // HEAD -> main -> origin/gone
	} {
		next, _ := m.Update(k)
		m = next.(Model)
	}
	if !m.baseTouched || m.worktree.Base() != "origin/gone" {
		t.Fatalf("setup: touched=%v, Base() = %q, want the user's own origin/gone", m.baseTouched, m.worktree.Base())
	}
	return m
}

// prune re-lists /repo-a's branches as refs and delivers the fetch's
// completion, which is what runFetchPrune's own Cmd reports.
func prune(t *testing.T, m Model, git *fakeGit, refs []string) Model {
	t.Helper()
	git.listBranchesResult = refs
	next, cmd := m.Update(fetchPruneDoneMsg{path: "/repo-a"})
	return pumpAsync(t, next.(Model), []tea.Cmd{cmd})
}

// TestPopup_APrunedChosenBaseFallsBackAndSaysSo is the defect: the fetch
// deleted the remote-tracking branch the user chose, so it names no commit
// any more. The old behaviour handed them origin/next -- the branch that
// moved into origin/gone's row -- and said nothing.
func TestPopup_APrunedChosenBaseFallsBackAndSaysSo(t *testing.T) {
	git := newFakeGit()
	git.commits = map[string]string{"/repo-a main": "0a1b2c3", "/repo-a origin/next": "1b2c3d4"}
	m := chosenBaseModel(t, git)

	m = prune(t, m, git, []string{"main", "origin/next"})

	if got := m.worktree.Base(); got != "" {
		t.Errorf("Base() after the prune = %q, want the HEAD row", got)
	}
	if got := m.PlanInput().BaseRef; got != "" {
		t.Errorf("plan.Input.BaseRef = %q, want the HEAD row", got)
	}
	if panel := worktreePanel(t, m); !strings.Contains(panel, `ignoring base "origin/gone"`) {
		t.Errorf("the worktree panel does not say which base went:\n%s", panel)
	}
}

// TestPopup_AChosenBaseOutsideTheWindowStaysSelected is the other cause the
// re-list looks identical from: the list is the 50 most recently committed
// branches (maxBaseRefs), so a base can leave it while still naming a commit
// perfectly well. That one is kept, selected, and needs nothing said.
func TestPopup_AChosenBaseOutsideTheWindowStaysSelected(t *testing.T) {
	git := newFakeGit()
	git.commits = map[string]string{"/repo-a origin/gone": "3d4e5f6"}
	m := chosenBaseModel(t, git)

	m = prune(t, m, git, []string{"main", "origin/next"})

	if got := m.worktree.Base(); got != "origin/gone" {
		t.Errorf("Base() after the re-list = %q, want the user's own origin/gone", got)
	}
	if got := m.PlanInput().BaseRef; got != "origin/gone" {
		t.Errorf("plan.Input.BaseRef = %q, want %q", got, "origin/gone")
	}
	if panel := worktreePanel(t, m); strings.Contains(panel, "ignoring base") {
		t.Errorf("the worktree panel reports a base that stands:\n%s", panel)
	}
}

// TestSettleChosenBase is the rule this path asks, and the one thing that
// separates its note from SettleBase's: there is no tier to name, because
// the user is the source.
func TestSettleChosenBase(t *testing.T) {
	git := newFakeGit()
	git.commits = map[string]string{"/repo origin/kept": "3d4e5f6"}

	if note := settleChosenBase(context.Background(), git, "/repo", "origin/kept"); note != "" {
		t.Errorf("a base that resolves said %q, want nothing said", note)
	}

	note := settleChosenBase(context.Background(), git, "/repo", "origin/gone")
	for _, want := range []string{`"origin/gone"`, "any more", "HEAD"} {
		if !strings.Contains(note, want) {
			t.Errorf("note = %q, want it to name %s", note, want)
		}
	}
	if strings.Contains(note, " from ") {
		t.Errorf("note = %q, want no tier credited with a base the user picked", note)
	}

	git.resolveCalls = nil
	if note := settleChosenBase(context.Background(), git, "/repo", ""); note != "" || len(git.resolveCalls) != 0 {
		t.Errorf("the HEAD row: note %q, git asked %v; want nothing asked and nothing said", note, git.resolveCalls)
	}
}

// TestPopup_TheFallbackNoteGoesWhenTheUserPicksAgain: the note says HEAD is
// being used instead of origin/gone, which stops being true the moment they
// pick something else -- the same rule that retires a tier's note, now
// reached from the other side, since the note it retires is about their own
// previous choice.
func TestPopup_TheFallbackNoteGoesWhenTheUserPicksAgain(t *testing.T) {
	git := newFakeGit()
	m := prune(t, chosenBaseModel(t, git), git, []string{"main", "origin/next"})
	if panel := worktreePanel(t, m); !strings.Contains(panel, "ignoring base") {
		t.Fatalf("setup: no note to retire:\n%s", panel)
	}

	m.form.FocusByID("worktree")
	for _, k := range []tea.KeyPressMsg{
		{Code: tea.KeyDown}, {Code: tea.KeyDown}, // chips -> branch -> base
		{Code: tea.KeyDown}, // HEAD -> main
	} {
		next, _ := m.Update(k)
		m = next.(Model)
	}

	if got := m.worktree.Base(); got != "main" {
		t.Fatalf("Base() = %q, want the user's new %q", got, "main")
	}
	if panel := worktreePanel(t, m); strings.Contains(panel, "ignoring base") {
		t.Errorf("the note outlived the base it was about:\n%s", panel)
	}
}

// TestPopup_TheRelistAsksNothingAboutABaseNobodyChose: the re-list settles
// the USER's base and only theirs. A base still where the app put it raises
// no question here -- #194's own check already ran for it when the project
// row landed, and handleBaseSettled's offer is what carries it through a
// re-list -- so there is nothing to ask git and, the part that costs if it
// is wrong, no check for a submit to be held behind.
//
// Both cases the guard covers, because they fail differently: a tier's base
// is a non-empty ref that is simply not the user's, and the HEAD row is no
// ref at all.
func TestPopup_TheRelistAsksNothingAboutABaseNobodyChose(t *testing.T) {
	for _, tc := range []struct{ name, remembered string }{
		{"a base a tier supplied", "old-branch"},
		{"the HEAD row", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			git := newFakeGit()
			git.listBranchesResult = []string{"main", "origin/next"}
			git.commits = map[string]string{"/repo-a old-branch": "3d4e5f6"}
			m := memoryModel(t, "/repo-a", memoryFor(map[string]config.ProjectDefaults{
				"/repo-a": {Worktree: ptrBool(true), Base: tc.remembered},
			}), git)
			if got := m.worktree.Base(); got != tc.remembered || m.baseTouched {
				t.Fatalf("setup: Base() = %q touched=%v, want %q untouched", got, m.baseTouched, tc.remembered)
			}

			git.resolveCalls = nil
			next, cmd := m.Update(fetchPruneDoneMsg{path: "/repo-a"})
			m = pumpAsync(t, next.(Model), []tea.Cmd{cmd})

			if len(git.resolveCalls) != 0 {
				t.Errorf("the re-list asked git %v about a base nobody chose", git.resolveCalls)
			}
			if m.baseSettlePending() {
				t.Error("a submit would be held for a check nobody scheduled")
			}
			if got := m.worktree.Base(); got != tc.remembered {
				t.Errorf("Base() after the re-list = %q, want %q", got, tc.remembered)
			}
		})
	}
}

// TestPopup_AProjectChangeRetiresAChosenBaseCheck: both schedulers share
// baseSettleVersion, so the question the newer project raises replaces the
// one the re-list raised. The older answer is about a project the form has
// left, and moving the base -- or putting its note on the panel -- from
// there would be #194's stale-answer hole reopened on this path.
func TestPopup_AProjectChangeRetiresAChosenBaseCheck(t *testing.T) {
	git := newFakeGit()
	m := chosenBaseModel(t, git)

	git.listBranchesResult = []string{"main", "origin/next"}
	next, cmd := m.Update(fetchPruneDoneMsg{path: "/repo-a"})
	m, held := pumpHoldingBaseChecks(t, next.(Model), []tea.Cmd{cmd})
	if len(held) != 1 {
		t.Fatalf("base checks held = %d, want the one the re-list scheduled", len(held))
	}

	m = switchProject(t, m, "/repo-a", "/repo-b")
	next, _ = m.Update(held[0])
	m = next.(Model)

	if got := m.worktree.Base(); got != "origin/gone" {
		t.Errorf("Base() in /repo-b = %q, want the user's own base, untouched by /repo-a's answer", got)
	}
	if panel := worktreePanel(t, m); strings.Contains(panel, "ignoring base") {
		t.Errorf("an answer about /repo-a reached /repo-b's panel:\n%s", panel)
	}
}

// TestPopup_ASubmitWaitsForThePrunedBaseCheck: the check is scheduled by the
// fetch landing and answers a `git rev-parse` later. A submit in between
// would build its plan from a base being re-checked -- and for the pruned
// case, from a ref herdr would refuse. So the submit waits for this check
// the way it already waits for the one a project change schedules (#197),
// and goes on by itself when it lands.
func TestPopup_ASubmitWaitsForThePrunedBaseCheck(t *testing.T) {
	runner := &submitFakeRunner{topo: herdrc.CreatedTopology{WorkspaceID: "ws-1", TabID: "t-1", PaneID: "pane-1"}}
	git := newFakeGit()
	git.listBranchesResult = []string{"main", "origin/gone", "origin/next"}
	git.currentBranchResult = "main"
	m := newSubmitTestModel(t, runner, testSetup{
		Git: git,
		Ctx: herdrc.Context{WorkspaceCwd: "/repo-a"},
		Projects: memoryFor(map[string]config.ProjectDefaults{
			"/repo-a": {Worktree: ptrBool(true)},
		}),
	})
	m = chooseGoneBase(t, pumpAsync(t, m, m.initCmds))
	m.title.SetTitle("fix the thing", false)
	// Its title check lands first: a submit waits for that one too (#137),
	// and this test is about the base check.
	m = landTitle(t, m, m.reactToChanges())

	git.listBranchesResult = []string{"main", "origin/next"}
	next, cmd := m.Update(fetchPruneDoneMsg{path: "/repo-a"})
	m, held := pumpHoldingBaseChecks(t, next.(Model), []tea.Cmd{cmd})
	if len(held) != 1 {
		t.Fatalf("base checks held = %d, want the one the re-list scheduled", len(held))
	}

	next, _ = m.Update(form.SubmitMsg{})
	m = next.(Model)
	if m.submitting {
		t.Fatalf("the submit started with the base check still out, from base %q", m.submitInput.BaseRef)
	}

	next, _ = m.Update(held[0])
	m = next.(Model)
	if !m.submitting {
		t.Fatal("the base check landing did not resume the submit")
	}
	if got := m.submitInput.BaseRef; got != "" {
		t.Errorf("submitted base = %q, want the HEAD row the check fell back to", got)
	}
}

// TestPopup_TheRelistKeepsAnOfferTheUserMovedOffOf is the other half of the
// same guard, and it is about the OFFER rather than the check. A user who
// picked a base and then went back to the HEAD row has a touched base and no
// ref: offering that would withdraw the row a tier's base is drawn on
// (OfferBase(""), which is how applyProjectDefaults retires one), taking a
// candidate off the list mid-form for someone who might scroll back to it.
// Nothing about the re-list justifies that, so the re-list leaves it alone.
func TestPopup_TheRelistKeepsAnOfferTheUserMovedOffOf(t *testing.T) {
	git := newFakeGit()
	git.listBranchesResult = []string{"main", "origin/next"}
	git.currentBranchResult = "main"
	git.commits = map[string]string{"/repo-a old-branch": "3d4e5f6"}
	m := memoryModel(t, "/repo-a", memoryFor(map[string]config.ProjectDefaults{
		"/repo-a": {Worktree: ptrBool(true), Base: "old-branch"},
	}), git)

	m.form.FocusByID("worktree")
	for _, k := range []tea.KeyPressMsg{
		{Code: tea.KeyDown}, {Code: tea.KeyDown}, // chips -> branch -> base
		{Code: tea.KeyDown},                  // old-branch -> main: theirs now
		{Code: tea.KeyUp}, {Code: tea.KeyUp}, // main -> old-branch -> HEAD
	} {
		next, _ := m.Update(k)
		m = next.(Model)
	}
	if !m.baseTouched || m.worktree.Base() != "" {
		t.Fatalf("setup: touched=%v, Base() = %q, want a touched base back on the HEAD row", m.baseTouched, m.worktree.Base())
	}

	m = prune(t, m, git, []string{"main", "origin/next"})

	if panel := worktreePanel(t, m); !strings.Contains(panel, "old-branch") {
		t.Errorf("the re-list withdrew a candidate the user had only moved off:\n%s", panel)
	}
}

// TestPopup_TheAnswerAndTheNewListLandInEitherOrder: handleFetchPruneDone
// batches the check with the re-list, and nothing decides which of the two
// Cmds answers first. Both orders have to end in the same place, and they
// reach it by different means -- the offer is what holds the selection when
// the new list lands first, and the HEAD row is what the refresh preserves
// by ID when the check's answer lands first -- so one order passing says
// nothing about the other.
func TestPopup_TheAnswerAndTheNewListLandInEitherOrder(t *testing.T) {
	for _, tc := range []struct {
		name      string
		listFirst bool
	}{
		{"the new list first", true},
		{"the check's answer first", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			git := newFakeGit()
			m := chosenBaseModel(t, git)
			git.listBranchesResult = []string{"main", "origin/next"}

			next, cmd := m.Update(fetchPruneDoneMsg{path: "/repo-a"})
			m = next.(Model)
			var settled, listed tea.Msg
			for _, c := range cmd().(tea.BatchMsg) {
				switch msg := c().(type) {
				case baseSettledMsg:
					settled = msg
				case baseResultMsg:
					listed = msg
				}
			}
			if settled == nil || listed == nil {
				t.Fatalf("the re-list produced settled=%v listed=%v, want one of each", settled, listed)
			}

			order := []tea.Msg{settled, listed}
			if tc.listFirst {
				order = []tea.Msg{listed, settled}
			}
			for _, msg := range order {
				next, _ := m.Update(msg)
				m = next.(Model)
			}

			if got := m.worktree.Base(); got != "" {
				t.Errorf("Base() = %q, want the HEAD row", got)
			}
			if panel := worktreePanel(t, m); !strings.Contains(panel, `ignoring base "origin/gone"`) {
				t.Errorf("the worktree panel does not say which base went:\n%s", panel)
			}
		})
	}
}
