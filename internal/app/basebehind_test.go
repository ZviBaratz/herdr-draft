package app

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/form"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
)

// behindModel is a form opened on /repo-a with a worktree on, whose branch
// list is main and origin/next and in which both resolve, and in which
// behind answers BaseBehind. base is what projects.json remembers there:
// "" leaves the HEAD row selected.
func behindModel(t *testing.T, base string, behind map[string]fakeBehind) (Model, *fakeGit) {
	t.Helper()
	return behindModelListing(t, []string{"main", "origin/next"}, base, behind)
}

// behindModelListing is behindModel over a branch list of its own, drawn
// from main, origin/main and origin/next, all three of which resolve.
func behindModelListing(t *testing.T, refs []string, base string, behind map[string]fakeBehind) (Model, *fakeGit) {
	t.Helper()
	git := newFakeGit()
	git.listBranchesResult = refs
	git.currentBranchResult = "feature"
	git.commits = map[string]string{
		"/repo-a main":        "0a1b2c3",
		"/repo-a origin/main": "2c3d4e5",
		"/repo-a origin/next": "1b2c3d4",
	}
	git.behind = behind
	m := memoryModel(t, "/repo-a", memoryFor(map[string]config.ProjectDefaults{
		"/repo-a": {Worktree: ptrBool(true), Base: base},
	}), git)
	return m, git
}

// pickBaseAndPump is pickBase with the Cmds each key schedules run to
// completion, which pickBase drops: the stale-base question goes out on
// the key that moves the base, not afterwards.
func pickBaseAndPump(t *testing.T, m Model, ref string) Model {
	t.Helper()
	m, answers := pickBaseCollecting(t, m, ref)
	for _, a := range answers {
		next, _ := m.Update(a)
		m = next.(Model)
	}
	return m
}

// pickBaseCollecting moves the base picker onto ref with real keys and
// returns the stale-base answers those keys' Cmds produced, undelivered,
// with every other Cmd pumped as usual.
func pickBaseCollecting(t *testing.T, m Model, ref string) (Model, []baseBehindMsg) {
	t.Helper()
	var answers []baseBehindMsg
	press := func(k tea.KeyPressMsg) {
		next, cmd := m.Update(k)
		m = next.(Model)
		queue := []tea.Cmd{cmd}
		for len(queue) > 0 {
			c := queue[0]
			queue = queue[1:]
			if c == nil {
				continue
			}
			switch msg := c().(type) {
			case tea.BatchMsg:
				queue = append(queue, msg...)
			case baseBehindMsg:
				answers = append(answers, msg)
			}
		}
	}
	// chips -> branch -> base. Their Cmds are dropped, as pickBase drops
	// them: focusing the branch input starts its cursor blink, a Cmd that
	// sleeps for real, and neither key moves the base.
	m.form.FocusByID("worktree")
	for range 2 {
		next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		m = next.(Model)
	}
	for range 12 {
		if m.worktree.Base() == ref {
			return m, answers
		}
		press(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	t.Fatalf("could not reach base %q; stopped on %q", ref, m.worktree.Base())
	return m, nil
}

// TestBaseBehindNote is the wording both paths share (#354), and the two
// answers that leave nothing to say.
func TestBaseBehindNote(t *testing.T) {
	for _, tc := range []struct {
		upstream string
		behind   int
		want     string
	}{
		{"origin/main", 38, "local main is 38 commits behind origin/main"},
		{"origin/main", 1, "local main is 1 commit behind origin/main"},
		{"origin/main", 0, ""},
		{"", 0, ""},
	} {
		if got := BaseBehindNote("main", tc.upstream, tc.behind); got != tc.want {
			t.Errorf("BaseBehindNote(main, %q, %d) = %q, want %q", tc.upstream, tc.behind, got, tc.want)
		}
	}
}

// TestPopup_ALocalBaseBehindItsUpstreamSaysSo is #354 in the popup: main
// is the local branch, so picking it cuts from wherever that was left. The
// panel says how far behind that is.
func TestPopup_ALocalBaseBehindItsUpstreamSaysSo(t *testing.T) {
	m, _ := behindModel(t, "", map[string]fakeBehind{"/repo-a main": {"origin/main", 38}})

	m = pickBaseAndPump(t, m, "main")

	if panel := worktreePanel(t, m); !strings.Contains(panel, "local main is 38 commits behind origin/main") {
		t.Errorf("the worktree panel does not say main is behind origin/main:\n%s", panel)
	}
}

// TestPopup_ARememberedBaseBehindItsUpstreamSaysSo: a tier's base is the
// same local branch as a picked one, and as stale.
func TestPopup_ARememberedBaseBehindItsUpstreamSaysSo(t *testing.T) {
	m, _ := behindModel(t, "main", map[string]fakeBehind{"/repo-a main": {"origin/main", 3}})

	if got := m.worktree.Base(); got != "main" {
		t.Fatalf("setup: Base() = %q, want the remembered main", got)
	}
	if panel := worktreePanel(t, m); !strings.Contains(panel, "local main is 3 commits behind origin/main") {
		t.Errorf("the worktree panel does not say the remembered main is behind:\n%s", panel)
	}
}

// TestPopup_NothingToSayAboutABaseLeavesNoNote covers every answer that is
// not a stale local branch -- which gitx decides, and the fake reports as
// nothing -- and the HEAD row, which is never asked about at all.
func TestPopup_NothingToSayAboutABaseLeavesNoNote(t *testing.T) {
	m, git := behindModel(t, "", map[string]fakeBehind{"/repo-a main": {"origin/main", 0}})

	for _, call := range git.behindCalls {
		if strings.HasSuffix(call, " ") {
			t.Errorf("the HEAD row was asked about: %q", call)
		}
	}
	for _, ref := range []string{"main", "origin/next"} {
		m = pickBaseAndPump(t, m, ref)
		if panel := worktreePanel(t, m); strings.Contains(panel, "behind") {
			t.Errorf("base %s: the panel has a stale-base note with nothing to say:\n%s", ref, panel)
		}
	}
	if len(git.behindCalls) == 0 {
		t.Error("no base was asked about, so the absence of a note proves nothing")
	}
}

// TestPopup_AFailedBaseBehindLeavesNoNote: the question is advisory, and
// "could not tell" is nothing a person can act on.
func TestPopup_AFailedBaseBehindLeavesNoNote(t *testing.T) {
	m, git := behindModel(t, "", nil)
	git.behindErr = errors.New("git exploded")

	m = pickBaseAndPump(t, m, "main")

	if len(git.behindCalls) == 0 {
		t.Fatal("setup: the base was never asked about")
	}
	if panel := worktreePanel(t, m); strings.Contains(panel, "behind") {
		t.Errorf("a failed question left a note:\n%s", panel)
	}
}

// TestPopup_TheNoteGoesWithTheBase: the note is about the base on screen,
// so moving off it -- to another ref, or the worktree off -- takes it away.
func TestPopup_TheNoteGoesWithTheBase(t *testing.T) {
	m, _ := behindModel(t, "", map[string]fakeBehind{"/repo-a main": {"origin/main", 38}})
	m = pickBaseAndPump(t, m, "main")
	if panel := worktreePanel(t, m); !strings.Contains(panel, "behind origin/main") {
		t.Fatalf("setup: no note on main:\n%s", panel)
	}

	m = pickBaseAndPump(t, m, "origin/next")
	if m.baseBehindNote != "" {
		t.Errorf("note after moving to origin/next = %q, want none", m.baseBehindNote)
	}

	m, _ = behindModel(t, "", map[string]fakeBehind{"/repo-a main": {"origin/main", 38}})
	m = pickBaseAndPump(t, m, "main")
	if m.baseBehindNote == "" {
		t.Fatal("setup: no note on main the second time")
	}
	m.worktree.SetOn(false)
	m = pumpAsync(t, m, m.reactToChanges())
	if m.baseBehindNote != "" {
		t.Errorf("note with the worktree off = %q, want none", m.baseBehindNote)
	}
}

// TestPopup_AStaleBehindAnswerIsDropped: an answer about a base the user
// has since moved off must not land on the one they moved to.
func TestPopup_AStaleBehindAnswerIsDropped(t *testing.T) {
	m, _ := behindModel(t, "", map[string]fakeBehind{"/repo-a main": {"origin/main", 38}})

	m, asked := pickBaseCollecting(t, m, "main")
	if len(asked) == 0 {
		t.Fatal("setup: no stale-base question went out for main")
	}

	m = pickBaseAndPump(t, m, "origin/next")
	next, _ := m.Update(asked[len(asked)-1])
	m = next.(Model)
	if m.baseBehindNote != "" {
		t.Errorf("an answer about main landed on origin/next: %q", m.baseBehindNote)
	}
}

// TestPopup_TheFetchReasksAboutAnUnmovedBase: the form's own fetch is what
// moves origin/main, so the re-list after it asks again even though the
// base on screen has not moved.
func TestPopup_TheFetchReasksAboutAnUnmovedBase(t *testing.T) {
	m, git := behindModel(t, "", map[string]fakeBehind{"/repo-a main": {"origin/main", 0}})
	m = pickBaseAndPump(t, m, "main")
	if m.baseBehindNote != "" {
		t.Fatalf("setup: note before the fetch = %q, want none", m.baseBehindNote)
	}

	git.behind = map[string]fakeBehind{"/repo-a main": {"origin/main", 5}}
	next, cmd := m.Update(fetchPruneDoneMsg{path: "/repo-a"})
	m = pumpAsync(t, next.(Model), []tea.Cmd{cmd})

	if got, want := m.baseBehindNote, "local main is 5 commits behind origin/main"; got != want {
		t.Errorf("note after the fetch = %q, want %q", got, want)
	}
}

// TestPopup_ASubmitDoesNotWaitForTheBehindCheck: the note is advisory, so
// a submit with the question still out goes ahead.
func TestPopup_ASubmitDoesNotWaitForTheBehindCheck(t *testing.T) {
	runner := &submitFakeRunner{topo: herdrc.CreatedTopology{WorkspaceID: "ws-1", TabID: "t-1", PaneID: "pane-1"}}
	git := newFakeGit()
	git.listBranchesResult = []string{"main", "origin/next"}
	git.currentBranchResult = "feature"
	git.commits = map[string]string{"/repo-a main": "0a1b2c3"}
	git.behind = map[string]fakeBehind{"/repo-a main": {"origin/main", 38}}
	m := newSubmitTestModel(t, runner, testSetup{
		Git: git,
		Ctx: herdrc.Context{WorkspaceCwd: "/repo-a"},
		Projects: memoryFor(map[string]config.ProjectDefaults{
			"/repo-a": {Worktree: ptrBool(true)},
		}),
	})
	m = pumpAsync(t, m, m.initCmds)
	m.title.SetTitle("fix the thing", false)
	m = landTitle(t, m, m.reactToChanges())
	// Asked, and never answered.
	m, asked := pickBaseCollecting(t, m, "main")
	if len(asked) == 0 {
		t.Fatal("setup: no stale-base question went out")
	}

	next, _ := m.Update(form.SubmitMsg{})
	m = next.(Model)
	if m.submitHeld || !m.submitting {
		t.Errorf("submit held=%v submitting=%v with only the stale-base question out, want it started", m.submitHeld, m.submitting)
	}
}

// TestPopup_ADroppedBaseTakesItsNoteWithIt: a remembered base the list
// names but git cannot resolve is dropped to the HEAD row by the settle
// (#194), and a note about how far behind it was would describe a base no
// longer in use. The settle moves the base without routing through
// reactToChanges, so it is what has to retire the note.
func TestPopup_ADroppedBaseTakesItsNoteWithIt(t *testing.T) {
	git := newFakeGit()
	git.listBranchesResult = []string{"main", "gone"}
	git.currentBranchResult = "feature"
	git.commits = map[string]string{"/repo-a main": "0a1b2c3"}
	git.behind = map[string]fakeBehind{"/repo-a gone": {"origin/gone", 4}}
	m := newTestModel(t, testSetup{
		Git: git,
		Ctx: herdrc.Context{WorkspaceCwd: "/repo-a"},
		Projects: memoryFor(map[string]config.ProjectDefaults{
			"/repo-a": {Worktree: ptrBool(true), Base: "gone"},
		}),
	})
	m = pumpAsync(t, m, m.initCmds)

	if got := m.worktree.Base(); got != "" {
		t.Fatalf("setup: Base() = %q, want the HEAD row the settle dropped gone to", got)
	}
	if m.baseBehindNote != "" {
		t.Errorf("note after gone was dropped = %q, want none", m.baseBehindNote)
	}
}

// listedDirectlyUnder reports whether the worktree panel draws the base row
// below on the line straight after the base row above.
func listedDirectlyUnder(panel, above, below string) bool {
	prev := ""
	for line := range strings.Lines(panel) {
		line = strings.TrimSpace(line)
		if prev == above && line == below {
			return true
		}
		prev = line
	}
	return false
}

// TestPopup_ABehindMainHasOriginsCopyUnderIt is #361's own story. The note
// said main was behind origin/main, and origin/main was not on the list,
// folded into main's row. gitx.ListBranches now lists it directly under a
// local main that is behind it. A remembered main stays main -- the tier
// asked for the local branch, and create keeps it too -- with the note
// naming the row one down. Picking that row is the person's move, and it
// clears the note: a remote-tracking ref trails nothing.
func TestPopup_ABehindMainHasOriginsCopyUnderIt(t *testing.T) {
	m, _ := behindModelListing(t, []string{"main", "origin/main", "origin/next"}, "main",
		map[string]fakeBehind{"/repo-a main": {"origin/main", 5}})

	if got := m.worktree.Base(); got != "main" {
		t.Fatalf("Base() = %q, want the remembered main, not origin's copy", got)
	}
	panel := worktreePanel(t, m)
	if !strings.Contains(panel, "local main is 5 commits behind origin/main") {
		t.Errorf("the worktree panel does not say main is behind origin/main:\n%s", panel)
	}
	if !listedDirectlyUnder(panel, "main", "origin/main") {
		t.Errorf("origin/main is not listed directly under main:\n%s", panel)
	}

	m = pickBaseAndPump(t, m, "origin/main")

	if m.baseBehindNote != "" {
		t.Errorf("note on origin/main = %q, want none", m.baseBehindNote)
	}
	if got := m.PlanInput().BaseRef; got != "origin/main" {
		t.Errorf("plan.Input.BaseRef = %q, want origin/main", got)
	}
}

// TestPopup_TheFetchListsOriginsCopyWithoutMovingTheBase is #212's ground.
// The form's own fetch is what moves origin/main, so it is what puts
// origin/main on the list under a main that was current a moment before: a
// row appears inside a list the person may already be pointing into. The
// base stays the ref it was, whether that is a remembered main above the
// new row or a pick below it, and a remembered one is still the tier's.
func TestPopup_TheFetchListsOriginsCopyWithoutMovingTheBase(t *testing.T) {
	for _, tc := range []struct{ name, remembered, picked string }{
		{"a remembered main above the new row", "main", ""},
		{"a picked base below it", "", "origin/next"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, git := behindModel(t, tc.remembered, map[string]fakeBehind{"/repo-a main": {"origin/main", 0}})
			want := tc.remembered
			if tc.picked != "" {
				m = pickBaseAndPump(t, m, tc.picked)
				want = tc.picked
			}
			if got := m.worktree.Base(); got != want {
				t.Fatalf("setup: Base() = %q, want %q", got, want)
			}
			touched := m.baseTouched

			git.behind = map[string]fakeBehind{"/repo-a main": {"origin/main", 5}}
			m = prune(t, m, git, []string{"main", "origin/main", "origin/next"})

			if got := m.worktree.Base(); got != want {
				t.Errorf("Base() after the re-list = %q, want %q", got, want)
			}
			if m.baseTouched != touched {
				t.Errorf("baseTouched after the re-list = %v, want %v: nobody moved the base", m.baseTouched, touched)
			}
			panel := worktreePanel(t, m)
			if !listedDirectlyUnder(panel, "main", "origin/main") {
				t.Errorf("origin/main is not listed directly under main after the fetch:\n%s", panel)
			}
			if want == "main" && m.baseBehindNote != "local main is 5 commits behind origin/main" {
				t.Errorf("note after the fetch = %q, want main's count", m.baseBehindNote)
			}
		})
	}
}

// TestPopup_AChosenOriginCopyFoldedBackStaysSelected: a picked origin/main
// can leave the list the other way. Another pane updating the local main
// while the popup is open makes the fetch's re-list fold origin/main back
// into main's row, so the ref the person chose is no longer listed. That is
// #212's shape exactly, and its answer holds: the ref is kept on offer,
// settled, and still selected, rather than the branch that moved into its
// row.
func TestPopup_AChosenOriginCopyFoldedBackStaysSelected(t *testing.T) {
	m, git := behindModelListing(t, []string{"main", "origin/main", "origin/next"}, "",
		map[string]fakeBehind{"/repo-a main": {"origin/main", 5}})
	m = pickBaseAndPump(t, m, "origin/main")

	git.behind = nil
	m = prune(t, m, git, []string{"main", "origin/next"})

	if got := m.worktree.Base(); got != "origin/main" {
		t.Errorf("Base() after the fold = %q, want the person's own origin/main", got)
	}
	if got := m.PlanInput().BaseRef; got != "origin/main" {
		t.Errorf("plan.Input.BaseRef = %q, want origin/main", got)
	}
	if panel := worktreePanel(t, m); strings.Contains(panel, "ignoring base") {
		t.Errorf("the worktree panel reports a base that stands:\n%s", panel)
	}
}
