package app

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/defaults"
	"github.com/ZviBaratz/herdr-draft/internal/form"
	"github.com/ZviBaratz/herdr-draft/internal/picker"
)

// #201: ⌃R⌃R rebuilds the whole form through New (handleClearRequested),
// and every request-version counter the rebuilt form uses used to start
// again from zero. A check the discarded form still had in flight then
// landed carrying the same version number the rebuilt form's first check
// of that kind issues, so `msg.req.version != m.reqs.<source>` -- the only
// staleness guard these sources have -- could not tell the two apart and
// both answers were applied, in arrival order.
//
// #196 and #203 carried the project row's and the title check's counters
// across the rebuild because both hold a submit. The four below hold
// nothing, which is why they waited, not why they are safe: each of them
// writes something the user then reads.
//
// There are two orderings, and the fix has to survive both. The first four
// tests drive the SAME number of requests on both sides of the clear, which
// is the issue's own scenario: New schedules the base list and the picker
// preview itself, a browse follows the first typed path, and a clauth
// reload follows the first focus of the account row -- so version 1 meets
// version 1. The two after them are the ordering the independent review
// found, where the pre-clear answer arrives BEFORE the rebuilt form has
// asked anything of that source at all -- which is why the counters are
// carried one PAST the discarded form's rather than equal to them
// (reqVersions.superseded), and why the invariant is asserted on its own at
// the bottom.
//
// The assertion is always the same: the answer to a question the discarded
// form asked moves nothing in the rebuilt one.

// typeProjectPath types raw into the project row through the real Update
// loop -- the only path a user has into path mode, and so into a browse.
// It returns the model with a browse in flight.
func typeProjectPath(t *testing.T, m Model, raw string) Model {
	t.Helper()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	m = next.(Model)
	if cmd := m.form.FocusByID("dir"); cmd != nil {
		cmd()
	}
	for _, r := range raw {
		n, _ := m.Update(rn(r))
		m = n.(Model)
	}
	if m.reqs.browse == 0 {
		t.Fatalf("typing %q scheduled no browse (Typed() = %q)", raw, m.dir.Typed())
	}
	return m
}

// turnWorktreeOn drives the worktree toggle the way a user does, so the
// base picker -- which is what a base list actually writes -- is on screen
// to compare. After a ⌃R⌃R the tiers put it back where a form-open does,
// which for frameSetup is off: its config.toml asks for no worktree and no
// tier above it says otherwise. (This used to rest on the clear marking
// the worktree touched, which #135 removed.)
func turnWorktreeOn(t *testing.T, m Model) Model {
	t.Helper()
	if cmd := m.form.FocusByID("worktree"); cmd != nil {
		cmd()
	}
	next, _ := m.Update(key(tea.KeyRight, 0))
	m = next.(Model)
	if !m.PlanInput().UseWorktree {
		t.Fatal("setup: the worktree row did not turn on")
	}
	return m
}

// TestClear_AStaleBaseListMovesNothing: the base list is the one of the
// four that rewrites two things at once -- WorktreeField's ref picker and
// the header's context line, which is the only place the checked-out
// branch is ever learned (handleBaseResult). A pre-clear list would name
// the discarded form's branches under the rebuilt form's project.
func TestClear_AStaleBaseListMovesNothing(t *testing.T) {
	git := newFakeGit()
	git.listBranchesResult = []string{"main", "release-1"}
	git.currentBranchResult = "main"
	setup := frameSetup(false)
	setup.Git = git
	m := resolveDirCheck(t, newTestModel(t, setup))

	// The list the discarded form asked for, still out when ⌃R⌃R lands.
	stale := baseResultMsg{
		req:  request{version: m.reqs.base, key: m.dir.Value()},
		refs: []string{"only-the-discarded-form-listed-this"},
		head: "discarded-head",
	}

	fresh := turnWorktreeOn(t, cleared(t, m))
	fresh, _ = fresh.handleBaseResult(baseResultMsg{
		req:  request{version: fresh.reqs.base, key: fresh.dir.Value()},
		refs: git.listBranchesResult,
		head: git.currentBranchResult,
	})
	before := focusedFrame(t, fresh, "worktree")

	next, _ := fresh.Update(stale)
	if got := focusedFrame(t, next.(Model), "worktree"); got != before {
		t.Errorf("a base list from before the clear moved the rebuilt form:\n got:\n%s\nwant:\n%s", got, before)
	}
}

// TestClear_AStaleBrowseListingMovesNothing: a browse result replaces
// DirField's whole candidate pool (handleBrowseResult installs an empty
// listing too), and it is the one source that moves the project SELECTION
// without a keystroke behind it -- so a pre-clear listing does not merely
// show the wrong directories, it can change which one a submit would use.
func TestClear_AStaleBrowseListingMovesNothing(t *testing.T) {
	const staleEntry = "/home/test/Projects/only-the-discarded-form-listed-this"

	m, _ := browseModel(t)
	m = typeProjectPath(t, m, "~/Projects/")
	stale := browseResultMsg{
		req:     request{version: m.reqs.browse, key: m.browseDir},
		entries: []string{staleEntry},
	}

	fresh := typeProjectPath(t, cleared(t, m), "~/Projects/")
	before := dirRows(fresh)

	next, _ := fresh.Update(stale)
	fresh = next.(Model)
	if got := dirRows(fresh); got != before {
		t.Errorf("a browse listing from before the clear moved the project row:\n got:\n%s\nwant:\n%s", got, before)
	}
	if strings.Contains(fresh.dir.Value(), staleEntry) {
		t.Errorf("a browse listing from before the clear became the selected project: %q", fresh.dir.Value())
	}
}

// TestClear_AStalePickerPreviewMovesNothing: the preview is what the
// account row says the picker WOULD choose for this project, so a
// pre-clear one credits the rebuilt row with a profile no picker was asked
// about since.
func TestClear_AStalePickerPreviewMovesNothing(t *testing.T) {
	p := &fakePicker{res: picker.Result{Profile: "work", Tier: "Max"}}
	m := resolveDirCheck(t, modelWithPicker(t, p, config.Config{}))
	if m.reqs.picker == 0 {
		t.Fatal("setup: New scheduled no picker preview")
	}

	stale := pickerPreviewMsg{
		req: request{version: m.reqs.picker, key: m.dir.Value()},
		res: picker.Result{Profile: "only-the-discarded-form-asked-for-this", Tier: "Pro"},
	}

	fresh := cleared(t, m)
	before := focusedFrame(t, fresh, "account")

	next, _ := fresh.Update(stale)
	if got := focusedFrame(t, next.(Model), "account"); got != before {
		t.Errorf("a picker preview from before the clear moved the account row:\n got:\n%s\nwant:\n%s", got, before)
	}
}

// TestClear_AStaleClauthReloadMovesNothing is the one whose blast radius
// grew. Since #255 a reload applied to a row that is currently
// unavailable writes the row's STATE and its reason (recoverAccountRow),
// not just a profile list, and a ⌃R⌃R rebuild of an unavailable row leaves
// it unavailable -- so the pre-clear answer lands on exactly the state it
// can rewrite.
//
// The worst shape is already ruled out elsewhere: a stale FAILURE cannot
// un-recover a row a fresh answer has made live, because handleClauthResult
// then takes the live path and ignores failures entirely
// (TestClauthReload_ALiveRowIsLeftAloneByAFailure). What is left is this --
// a rebuilt row still unavailable, taking a reason clauth gave to a form
// that is gone.
func TestClear_AStaleClauthReloadMovesNothing(t *testing.T) {
	const staleReason = "exit status 1: only the discarded form was told this"

	cl := &fakeClauth{status: frameClauthStatus()}
	m := resolveDirCheck(t, newTestModel(t, unavailableAccountSetup(cl)))

	// The reload the discarded form asked for, answered by a clauth that
	// was failing at the time.
	cl.err = errors.New(staleReason)
	m, stale := clickAccountRow(t, m)
	cl.err = nil

	fresh := cleared(t, m)
	if fresh.account == nil || fresh.account.Enabled() {
		t.Fatal("setup: the rebuilt account row is not the unavailable one #255 recovers")
	}
	// The rebuilt form asks its own question, whose answer is not in yet.
	fresh, _ = clickAccountRow(t, fresh)
	before := accountRow(t, fresh)

	next, _ := fresh.Update(stale)
	fresh = next.(Model)
	if got := accountRow(t, fresh); got != before {
		t.Errorf("a clauth reload from before the clear moved the account row:\n got: %s\nwant: %s", got, before)
	}
	if strings.Contains(fresh.clauthUnavailable, staleReason) {
		t.Errorf("the rebuilt row's reason came from before the clear: %q", fresh.clauthUnavailable)
	}
}

// --- the second ordering: the answer lands before the rebuilt form asks ---

// TestClear_AStaleBrowseListingBeforeTheRebuiltFormAsks is the ordering an
// independent review of this fix found, and the reason the counters are
// carried one PAST the discarded form's rather than equal to them.
//
// Carried equal, a counter matches the discarded form's outstanding
// requests until the rebuilt form issues one of its own. For the three
// sources New re-asks at construction -- dir, base, picker -- that window
// does not exist. Browse is re-asked only when the user types a path, and
// clauth only when they focus the account row, so the window is in
// practice the whole life of the rebuilt form, while the check in flight
// answers in milliseconds.
func TestClear_AStaleBrowseListingBeforeTheRebuiltFormAsks(t *testing.T) {
	const staleEntry = "/home/test/Projects/only-the-discarded-form-listed-this"

	m, _ := browseModel(t)
	m = typeProjectPath(t, m, "~/Projects/")
	stale := browseResultMsg{
		req:     request{version: m.reqs.browse, key: m.browseDir},
		entries: []string{staleEntry},
	}

	fresh := cleared(t, m) // never typed into, so it has browsed nothing
	before := dirRows(fresh)

	next, _ := fresh.Update(stale)
	fresh = next.(Model)
	if got := dirRows(fresh); got != before {
		t.Errorf("a browse listing from before the clear moved the project row:\n got:\n%s\nwant:\n%s", got, before)
	}
	if strings.Contains(fresh.dir.Value(), staleEntry) {
		t.Errorf("a browse listing from before the clear became the selected project: %q", fresh.dir.Value())
	}
}

// TestClear_AStaleClauthReloadBeforeTheRebuiltFormAsks is the same ordering
// for the other source New does not re-ask, and the more pointed of the two:
// since #255 the answer rewrites an unavailable row's state and its reason,
// and the rebuilt row is unavailable from the moment it is built.
func TestClear_AStaleClauthReloadBeforeTheRebuiltFormAsks(t *testing.T) {
	const staleReason = "exit status 1: only the discarded form was told this"

	cl := &fakeClauth{status: frameClauthStatus()}
	m := resolveDirCheck(t, newTestModel(t, unavailableAccountSetup(cl)))

	cl.err = errors.New(staleReason)
	m, stale := clickAccountRow(t, m)
	cl.err = nil

	fresh := cleared(t, m) // the account row is never focused, so nothing is asked
	if fresh.account == nil || fresh.account.Enabled() {
		t.Fatal("setup: the rebuilt account row is not the unavailable one #255 recovers")
	}
	before := accountRow(t, fresh)

	next, _ := fresh.Update(stale)
	fresh = next.(Model)
	if got := accountRow(t, fresh); got != before {
		t.Errorf("a clauth reload from before the clear moved the account row:\n got: %s\nwant: %s", got, before)
	}
	if strings.Contains(fresh.clauthUnavailable, staleReason) {
		t.Errorf("the rebuilt row's reason came from before the clear: %q", fresh.clauthUnavailable)
	}
}

// --- #197's counter, which the issue left out ----------------------------

// TestClear_AStaleBaseSettleDoesNotReleaseAHeldSubmit is the counter #201
// was told it could skip, and the reason it could not.
//
// The issue's argument was that a rebuilt form marks its base touched, so
// scheduleBaseSettle declines to ask and counts every check as landed
// without running one. Both halves are wrong in a way that only shows
// together. The decline still bumps the version on its way past, so the two
// sides collide like every other pair; and scheduleBaseSettle is not the
// only scheduler on this counter -- keepChosenBaseAcrossRelist (#212) runs
// a REAL `git rev-parse` and its own precondition IS baseTouched, so a
// rebuilt form reaches it as soon as the user picks a base and the
// once-per-repo fetch re-lists.
//
// What that costs is the one thing this counter can cost. handleBaseSettled
// sets baseSettleLanded BEFORE its switch looks at anything, so a pre-clear
// answer does not need to survive the switch to do damage: it only needs to
// match the version, and the submit held for #212's real check
// (baseSettlePending) is released over a base nothing checked. That is
// #195's own shape, arriving through the door #201 was closing.
func TestClear_AStaleBaseSettleDoesNotReleaseAHeldSubmit(t *testing.T) {
	git := newFakeGit()
	git.listBranchesResult = []string{"main", "origin/gone", "origin/next"}
	git.currentBranchResult = "main"
	git.commits = map[string]string{"/repo-a origin/gone": "3d4e5f6"}

	m := memoryModel(t, "/repo-a", memoryFor(map[string]config.ProjectDefaults{
		"/repo-a": {Worktree: ptrBool(true), Base: "main"},
	}), git)
	// One project change, which is what makes the versions meet: without
	// the carry the rebuilt form reaches #212's settle at 2 (its own
	// decline takes 1), and a discarded form that has asked twice is out
	// at 2 as well. A form left on its opening project is out at 1 and
	// would miss by one, which proves nothing either way.
	m = switchProject(t, m, "/repo-a", "/repo-b")
	preClear := m.reqs.baseSettle
	if preClear != 2 {
		t.Fatalf("setup: the discarded form is out at version %d, not the %d the rebuilt form would reach", preClear, 2)
	}

	fresh := settle(t, mustClear(t, m))
	fresh.title.SetTitle("Fix pagination", false)
	fresh.form.FocusByID("worktree")
	next, _ := fresh.Update(key(tea.KeyRight, 0))
	fresh = chooseGoneBase(t, next.(Model))

	// The once-per-repo fetch finishes and re-lists: #212's real settle,
	// and the only base check a rebuilt form ever runs.
	next, cmd := fresh.Update(fetchPruneDoneMsg{path: "/repo-a"})
	fresh = next.(Model)
	res, ok := cmd().(baseResultMsg)
	if !ok {
		t.Fatalf("the re-list produced %T, want baseResultMsg", res)
	}
	next, _ = fresh.Update(res)
	fresh = next.(Model)
	if !fresh.baseSettlePending() {
		t.Fatal("setup: no real base check is in flight after the re-list")
	}
	// The title check is an independent hold; land it so the base settle is
	// the only thing the submit is waiting for.
	fresh.titleLandedVersion = fresh.reqs.title

	next, _ = fresh.Update(form.SubmitMsg{})
	fresh = next.(Model)
	if fresh.submitting || !fresh.submitHeld {
		t.Fatalf("setup: the submit was not held (submitting %v, held %v)", fresh.submitting, fresh.submitHeld)
	}

	next, _ = fresh.Update(baseSettledMsg{
		version:  preClear,
		resolved: defaults.Resolved{BaseRef: "only-the-discarded-form-resolved-this"},
	})
	after := next.(Model)
	if !after.baseSettlePending() {
		t.Errorf("a base settle from before the clear counted as the answer to #212's own check (landed %d, counter %d)",
			after.baseSettleLanded, after.reqs.baseSettle)
	}
	if after.submitting {
		t.Errorf("a base settle from before the clear released the held submit: BaseRef %q", after.submitInput.BaseRef)
	}
}

// --- the invariant the six tests above are each one instance of ----------

// TestClear_EveryCounterStartsPastTheDiscardedForms states the property
// directly, because the behavioural tests above can only ever cover the
// sources someone thought to drive -- and #201's own defect was a counter
// nobody had thought to carry.
//
// Strictly greater, not equal: equal is the second ordering's defect, where
// the rebuilt form holds a number the discarded form's outstanding requests
// still carry. A counter added to reqVersions and left out of superseded
// fails here rather than in whichever field's test is written next.
func TestClear_EveryCounterStartsPastTheDiscardedForms(t *testing.T) {
	m := resolveDirCheck(t, newTestModel(t, frameSetup(true)))
	// Every source driven at least once, so no counter is compared at zero
	// -- where "carried" and "restarted" look the same.
	m = typeProjectPath(t, m, "~/Projects/")
	m.reqs = reqVersions{dir: 7, title: 8, base: 9, baseSettle: 10, browse: 11, picker: 12, clauth: 13}
	before := m.reqs

	fresh, _ := m.handleClearRequested()

	// The one landed version that is carried with its counter, and the
	// reason New carries it: a rebuilt form has started no base check, so
	// it must not report one outstanding. Every other landed version stays
	// at zero because New really does schedule those checks.
	if fresh.baseSettlePending() {
		t.Errorf("the rebuilt form reports a base check outstanding (landed %d, counter %d) with nothing started",
			fresh.baseSettleLanded, fresh.reqs.baseSettle)
	}

	for _, c := range []struct {
		name     string
		was, now int
	}{
		{"dir", before.dir, fresh.reqs.dir},
		{"title", before.title, fresh.reqs.title},
		{"base", before.base, fresh.reqs.base},
		{"baseSettle", before.baseSettle, fresh.reqs.baseSettle},
		{"browse", before.browse, fresh.reqs.browse},
		{"picker", before.picker, fresh.reqs.picker},
		{"clauth", before.clauth, fresh.reqs.clauth},
	} {
		if c.now <= c.was {
			t.Errorf("%s: the rebuilt form starts at %d, which the discarded form's requests can still carry (it reached %d)",
				c.name, c.now, c.was)
		}
	}
}

// mustClear is handleClearRequested without its Cmd, for the tests that go
// on to drive the rebuilt form themselves.
func mustClear(t *testing.T, m Model) Model {
	t.Helper()
	fresh, _ := m.handleClearRequested()
	return fresh
}
