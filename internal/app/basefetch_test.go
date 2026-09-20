package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/form"
	"github.com/ZviBaratz/herdr-draft/internal/form/widgets"
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
	panel := worktreePanel(t, m)
	if !strings.Contains(panel, `ignoring base "origin/gone"`) {
		t.Errorf("the worktree panel does not say which base went:\n%s", panel)
	}
	// And the row goes with it. Asserted on the candidate lines rather than
	// the whole panel, because the note names the ref too.
	for line := range strings.Lines(panel) {
		if strings.TrimSpace(line) == "origin/gone" {
			t.Errorf("a ref that names no commit is still offered as a candidate:\n%s", panel)
		}
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

// TestPopup_TheBaseNeverPassesThroughTheNeighbour: the offer is applied in
// the SAME Update as the list it protects against, so there is no moment --
// not one message wide -- at which the row names a branch nobody chose. Both
// outcomes go through that moment identically and only diverge when the
// check answers, which is the point: the list is not what decides.
func TestPopup_TheBaseNeverPassesThroughTheNeighbour(t *testing.T) {
	for _, tc := range []struct{ name, resolves, want string }{
		{"a base that still resolves", "origin/gone", "origin/gone"},
		{"a base the fetch pruned", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			git := newFakeGit()
			if tc.resolves != "" {
				git.commits = map[string]string{"/repo-a " + tc.resolves: "3d4e5f6"}
			}
			m := chosenBaseModel(t, git)

			git.listBranchesResult = []string{"main", "origin/next"}
			next, cmd := m.Update(fetchPruneDoneMsg{path: "/repo-a"})
			m = next.(Model)

			listed, ok := cmd().(baseResultMsg)
			if !ok {
				t.Fatalf("the fetch's re-list produced %T, want a baseResultMsg", cmd())
			}
			next, settle := m.Update(listed)
			m = next.(Model)
			if got := m.worktree.Base(); got != "origin/gone" {
				t.Fatalf("Base() with the new list applied and the check still out = %q, want origin/gone held by the offer", got)
			}

			m = pumpAsync(t, m, []tea.Cmd{settle})
			if got := m.worktree.Base(); got != tc.want {
				t.Errorf("Base() after the check answered = %q, want %q", got, tc.want)
			}
		})
	}
}

// pickBase moves the base picker's cursor onto ref from wherever it is,
// through the real keys -- the user deciding.
func pickBase(t *testing.T, m Model, ref string) Model {
	t.Helper()
	m.form.FocusByID("worktree")
	for _, k := range []tea.KeyPressMsg{{Code: tea.KeyDown}, {Code: tea.KeyDown}} {
		next, _ := m.Update(k)
		m = next.(Model)
	}
	for range 12 {
		if m.worktree.Base() == ref {
			return m
		}
		next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		m = next.(Model)
	}
	t.Fatalf("could not reach base %q; stopped on %q", ref, m.worktree.Base())
	return m
}

// TestPopup_ABaseMovedWhileTheCheckIsOutStands: the check is a `git
// rev-parse` wide and #195's hold parks the user inside it, so a base moved
// in that window is the likely case rather than the rare one. The answer is
// about the ref they HAD; applying it would take away the ref they HAVE --
// #212's own symptom, reintroduced from the other side. This is the first
// path on which an async answer can move a touched base at all, which is
// why the tier branch's !baseTouched guard does not already cover it.
func TestPopup_ABaseMovedWhileTheCheckIsOutStands(t *testing.T) {
	git := newFakeGit()
	git.commits = map[string]string{"/repo-a origin/next": "1b2c3d4"}
	m := chosenBaseModel(t, git)

	git.listBranchesResult = []string{"main", "origin/next"}
	next, cmd := m.Update(fetchPruneDoneMsg{path: "/repo-a"})
	m, held := pumpHoldingBaseChecks(t, next.(Model), []tea.Cmd{cmd})
	if len(held) != 1 {
		t.Fatalf("base checks held = %d, want the one the re-list scheduled", len(held))
	}

	m = pickBase(t, m, "origin/next")
	next, _ = m.Update(held[0])
	m = next.(Model)

	if got := m.worktree.Base(); got != "origin/next" {
		t.Errorf("Base() = %q, want the %q the user moved to while the check was out", got, "origin/next")
	}
	if got := m.PlanInput().BaseRef; got != "origin/next" {
		t.Errorf("plan.Input.BaseRef = %q, want %q", got, "origin/next")
	}
	if panel := worktreePanel(t, m); strings.Contains(panel, "ignoring base") {
		t.Errorf("a note about the base they replaced reached the panel:\n%s", panel)
	}
}

// TestPopup_ASubmitResumesFromTheBaseTheUserActuallyHas is the same window
// with the submit in it, which is what the hold puts there: the submit is
// held for the check, the user picks while they wait, and the answer that
// releases it must not be the thing that changed what it submits.
func TestPopup_ASubmitResumesFromTheBaseTheUserActuallyHas(t *testing.T) {
	runner := &submitFakeRunner{topo: herdrc.CreatedTopology{WorkspaceID: "ws-1", TabID: "t-1", PaneID: "pane-1"}}
	git := newFakeGit()
	git.listBranchesResult = []string{"main", "origin/gone", "origin/next"}
	git.currentBranchResult = "main"
	git.commits = map[string]string{"/repo-a origin/next": "1b2c3d4"}
	m := newSubmitTestModel(t, runner, testSetup{
		Git: git,
		Ctx: herdrc.Context{WorkspaceCwd: "/repo-a"},
		Projects: memoryFor(map[string]config.ProjectDefaults{
			"/repo-a": {Worktree: ptrBool(true)},
		}),
	})
	m = chooseGoneBase(t, pumpAsync(t, m, m.initCmds))
	m.title.SetTitle("fix the thing", false)
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
	m = pickBase(t, m, "origin/next")

	next, _ = m.Update(held[0])
	m = next.(Model)
	if !m.submitting {
		t.Fatal("the base check landing did not resume the submit")
	}
	if got := m.submitInput.BaseRef; got != "origin/next" {
		t.Errorf("submitted base = %q, want the %q the user chose while the submit was held", got, "origin/next")
	}
}

// TestPopup_ABasePickedWhileTheRelistIsInFlightIsKept: the fetch COMPLETING
// and the new list being APPLIED are two moments with three subprocess calls
// between them, and a base picked in between reaches SetBaseItems with no
// offer under it. That is #212 verbatim in a narrower window, so the offer
// has to be made where the list is replaced rather than where the fetch
// finished.
func TestPopup_ABasePickedWhileTheRelistIsInFlightIsKept(t *testing.T) {
	git := newFakeGit()
	git.listBranchesResult = []string{"main", "origin/gone", "origin/next"}
	git.currentBranchResult = "main"
	git.commits = map[string]string{"/repo-a origin/gone": "3d4e5f6"}
	m := memoryModel(t, "/repo-a", memoryFor(map[string]config.ProjectDefaults{
		"/repo-a": {Worktree: ptrBool(true)},
	}), git)

	next, cmd := m.Update(fetchPruneDoneMsg{path: "/repo-a"})
	m = next.(Model)
	// The user picks from the list still on screen, before the re-list lands.
	git.listBranchesResult = []string{"main", "origin/next"}
	m = pickBase(t, m, "origin/gone")
	m = pumpAsync(t, m, []tea.Cmd{cmd})

	if got := m.worktree.Base(); got != "origin/gone" {
		t.Errorf("Base() = %q, want the %q the user picked before the re-list landed", got, "origin/gone")
	}
}

// TestPopup_TheFallbackResnapshotsWhatTheAppPutThere: the fall-back is the
// app moving the base, so snapshotAppliedDefaults has to record HEAD as what
// the app put there. It matters in one corner, and the corner is reachable:
// the check can fail for a reason other than the ref being gone -- git
// unavailable for that moment -- and then the ref is still in the list for
// the user to pick straight back. Without the resnapshot the snapshot still
// holds that ref, so noteUserEdits sees no change, and the note saying HEAD
// is in use stays on screen beside the base they just re-picked.
func TestPopup_TheFallbackResnapshotsWhatTheAppPutThere(t *testing.T) {
	git := newFakeGit()
	git.resolveErr = errors.New("git: unavailable for a moment")
	m := chosenBaseModel(t, git)

	// The list keeps origin/gone -- nothing was pruned, the check just could
	// not answer -- so it is still there to be picked again. Directly under
	// the HEAD row, which is load-bearing: reaching it past another ref
	// would clear the note on the way through and the test would pass
	// whether or not the fall-back resnapshotted anything.
	m = prune(t, m, git, []string{"origin/gone", "main", "origin/next"})
	if got := m.worktree.Base(); got != "" {
		t.Fatalf("setup: Base() = %q, want the HEAD row the failed check fell back to", got)
	}

	// By CLICK, not by keystroke, and that is the whole test. Walking there
	// with the arrows takes at least one message to get into the picker, and
	// reactToChanges resnapshots at the end of every message -- so the
	// keyboard route repairs the stale snapshot before the base can move,
	// and passes whether or not the fall-back resnapshotted. A click moves
	// the base in the SAME message, which is the one order that reads the
	// snapshot the fall-back left behind.
	git.resolveErr = nil
	m.form.FocusByID("worktree")
	_ = m.form.ViewAt(80, 24)
	syncZones()
	zi := widgets.Zones.Get("row:base:1") // HEAD is row 0; origin/gone is row 1
	if zi.IsZero() {
		t.Fatal("setup: the base panel's second row never resolved")
	}
	next, _ := m.Update(tea.MouseClickMsg{X: zi.StartX, Y: zi.StartY, Button: tea.MouseLeft})
	m = next.(Model)
	if got := m.worktree.Base(); got != "origin/gone" {
		t.Fatalf("setup: the click selected %q, want origin/gone", got)
	}

	if panel := worktreePanel(t, m); strings.Contains(panel, "ignoring base") {
		t.Errorf("the note stayed beside the base the user picked straight back:\n%s", panel)
	}
}

// TestPopup_AHeldBaseNeverOverwritesTheUsersOwnPick is #256 end to end,
// and it is the same class as the two defects above -- a base changing
// under the user without a word -- reached from the PENDING side rather
// than the list side. Both of those start from a base picked out of a list
// that named it, where nothing is held.
//
// The remembered ref is not in the branch list, so WorktreeField.SetBase
// holds it and the picker shows the HEAD row. The user picks `main` out of
// the list they can actually see. Then the once-per-repo `git fetch
// --prune` (#212) re-lists the branches, this time naming the held ref --
// and the hold, if the user's own decision has not retired it, lands on
// top of their choice.
//
// RequestedBase() is asserted at the moment of the pick because that is
// the root cause rather than the symptom: the hold outliving the user's
// decision. The plan is asserted at the end because that is what the
// overwrite actually costs -- a session branched from a ref nobody chose.
func TestPopup_AHeldBaseNeverOverwritesTheUsersOwnPick(t *testing.T) {
	git := newFakeGit()
	// The remembered base is NOT in the branch list, so SetBase holds it.
	git.listBranchesResult = []string{"main", "develop"}
	git.currentBranchResult = "main"
	git.commits = map[string]string{"/repo-a remote-only": "3d4e5f6", "/repo-a main": "0a1b2c3"}
	m := newTestModel(t, testSetup{
		Git:    git,
		Ctx:    herdrc.Context{WorkspaceCwd: "/repo-a"},
		Config: config.Config{Agents: config.AgentsConfig{Favorites: []string{"claude"}}},
		Projects: memoryFor(map[string]config.ProjectDefaults{
			"/repo-a": {Worktree: ptrBool(true), Base: "remote-only"},
		}),
	})
	// Held: the dir check has landed and resolved the remembered base, and
	// the check that would offer it has not answered yet.
	m, _ = pumpHoldingBaseChecks(t, m, m.initCmds)
	if got := m.worktree.Base(); got != "" {
		t.Fatalf("setup: Base() = %q, want the HEAD row while the remembered ref is held", got)
	}
	// And the window is really the HELD one, not merely a base that never
	// resolved: Base() == "" is equally true of a SetBase that was dropped
	// instead of remembered, so without this the test passes with its own
	// subject deleted.
	if got := m.worktree.RequestedBase(); got != "remote-only" {
		t.Fatalf("setup: RequestedBase() = %q, want the remembered ref held", got)
	}

	// The user picks a base out of the list they can actually see.
	m.form.FocusByID("worktree")
	for _, k := range []tea.KeyPressMsg{
		{Code: tea.KeyDown}, {Code: tea.KeyDown}, // chips -> branch -> base
		{Code: tea.KeyDown}, // HEAD -> main
	} {
		next, _ := m.Update(k)
		m = next.(Model)
	}
	if got := m.worktree.Base(); got != "main" || !m.baseTouched {
		t.Fatalf("setup: Base() = %q touched=%v, want the user's own main", got, m.baseTouched)
	}
	if got := m.worktree.RequestedBase(); got != "main" {
		t.Errorf("RequestedBase() after the user picked a base = %q, want %q: the hold survived their decision", got, "main")
	}

	m = prune(t, m, git, []string{"main", "develop", "remote-only"})

	if got := m.worktree.Base(); got != "main" {
		t.Errorf("Base() after the fetch re-listed the held ref = %q, want the user's own %q", got, "main")
	}
	if got := m.PlanInput().BaseRef; got != "main" {
		t.Errorf("plan.Input.BaseRef = %q, want the user's own %q", got, "main")
	}
}
