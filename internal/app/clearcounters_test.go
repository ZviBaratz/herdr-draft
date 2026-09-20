package app

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/defaults"
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
// Each test drives the SAME number of requests on both sides of the clear,
// which is the collision's own shape: New schedules the base list and the
// picker preview itself, a browse follows the first typed path, and a
// clauth reload follows the first focus of the account row -- so version 1
// meets version 1. The assertion is always the same: the answer to a
// question the discarded form asked moves nothing in the rebuilt one.

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
// to compare. A ⌃R⌃R marks the worktree touched (handleClearRequested), so
// the config tier will not turn it on again by itself.
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
	// Worktree on, because the base picker is what the list writes and an
	// off worktree renders none of it.
	setup.Config.DefaultWorktree = true
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

// TestClear_AStaleBaseSettleMovesNothing is the counter #201 deliberately
// leaves alone, pinned where its protection actually is.
//
// baseSettleVersion (#197) is not carried across the rebuild, and the
// issue's reason for leaving it -- the rebuilt form marks its base touched,
// so scheduleBaseSettle counts every check as landed without running one --
// is right about the check and not about the counter: the decline bumps
// the version on its way past, so a rebuilt form reaches 1 exactly as the
// discarded one did and the two answers collide like every other pair
// above (measured, 2026-09-20). What stops the pre-clear one is
// handleBaseSettled's own `msg.chosen == "" && m.baseTouched` case, which a
// rebuilt form satisfies by construction.
//
// So this asserts the outcome rather than the version, and it is here
// rather than beside #197 because it is the same collision: the day that
// case goes, this counter needs carrying too.
func TestClear_AStaleBaseSettleMovesNothing(t *testing.T) {
	git := newFakeGit()
	git.listBranchesResult = []string{"main", "release-1"}
	git.currentBranchResult = "main"
	setup := frameSetup(false)
	setup.Git = git
	m := resolveDirCheck(t, newTestModel(t, setup))

	fresh := turnWorktreeOn(t, cleared(t, m))
	stale := baseSettledMsg{
		version:  fresh.baseSettleVersion,
		resolved: defaults.Resolved{BaseRef: "only-the-discarded-form-resolved-this"},
	}
	before := focusedFrame(t, fresh, "worktree")

	next, _ := fresh.Update(stale)
	if got := focusedFrame(t, next.(Model), "worktree"); got != before {
		t.Errorf("a base settle from before the clear moved the rebuilt form:\n got:\n%s\nwant:\n%s", got, before)
	}
}
