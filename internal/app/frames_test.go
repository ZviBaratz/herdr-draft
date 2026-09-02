package app

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/ZviBaratz/herdr-draft/internal/clauth"
	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
	"github.com/ZviBaratz/herdr-draft/internal/linear"
)

// This file exists because internal/form's own eleven golden frames all
// render a form with ONE section in it. Every one of them therefore passed
// while the REAL, assembled form -- ten field Sections plus the Create
// button, which only internal/app ever builds -- did not fit any popup a
// normal terminal produces: measured before the fix, the Prompt field's own
// label first appeared at a window height of 43 rows and the footer at 48,
// against the 38 interior rows herdr's `height = "80%"` popup gives on a
// 50-row terminal. At 80x24 with Prompt focused, the string "Prompt:"
// rendered nowhere at all: the user typed into a field that was not on
// screen.
//
// The frames and assertions below are deliberately in THIS package, not in
// internal/form: the assembled section list, its order, and which fields
// are present at all are app-layer facts (New's own construction, spec §6),
// and internal/form cannot import internal/app to reach them.

// updateAppFrames regenerates the golden frames under testdata/frames
// instead of comparing against them, mirroring internal/form's own
// harness contract ("run with -update to regenerate"). It is a separate
// flag variable from that package's own, since flag names are per-binary
// and these are two different test binaries.
var updateAppFrames = flag.Bool("update", false, "regenerate golden frames")

// assertAppFrame mirrors internal/form's own assertFrame, rendering
// through the app Model's nested form.Model at an explicit size.
func assertAppFrame(t *testing.T, name string, m Model, w, h int) {
	t.Helper()
	got := m.form.ViewAt(w, h)
	path := filepath.Join("testdata", "frames", name+".txt")
	if *updateAppFrames {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil || string(want) != got {
		t.Errorf("frame %s mismatch (run with -update to regenerate)\n%s", name, got)
	}
}

// frameIssues is the assigned-issue fixture the full-config frames render
// the Issue picker over -- deliberately more issues than any allocation
// will show, so a shrunk picker is visibly a WINDOW onto a longer list.
func frameIssues() []linear.Issue {
	return []linear.Issue{
		{Identifier: "ENG-101", Title: "Fix login redirect loop", BranchName: "zvi/eng-101-login", StateName: "In Progress"},
		{Identifier: "ENG-102", Title: "Add dark mode toggle", BranchName: "zvi/eng-102-dark-mode", StateName: "Todo"},
		{Identifier: "ENG-103", Title: "Refactor auth middleware", BranchName: "zvi/eng-103-auth", StateName: "Todo"},
		{Identifier: "ENG-104", Title: "Cache workspace lookups", BranchName: "zvi/eng-104-cache", StateName: "Todo"},
	}
}

// frameClauthStatus is the two-profile clauth fixture the full-config
// frames render the Account field over (spec §6 field 7 renders the field
// only at >= 2 profiles).
func frameClauthStatus() clauth.Status {
	return clauth.Status{
		Schema:        1,
		ActiveProfile: "personal",
		Profiles: []clauth.Profile{
			{Name: "personal", Active: true, Tier: "max", AuthStatus: "ok",
				Windows: []clauth.Window{{Label: "5h", UtilizationPct: 12}}},
			{Name: "work", Tier: "team", AuthStatus: "ok",
				Windows: []clauth.Window{{Label: "5h", UtilizationPct: 71}}},
		},
	}
}

// newAssembledModel builds the REAL form the plugin runs: every Section
// internal/app's own New assembles for the given configuration, driven far
// enough through the app layer's async plumbing (the directory check) that
// the worktree zones are live rather than stuck in their conservative
// not-a-git-repo default.
//
// full selects the widest configuration spec §6 describes -- Linear
// configured (so the Issue field is rendered) and two clauth profiles (so
// the Account field is) -- versus the minimal one, where both static
// preconditions fail and neither field is constructed at all.
func newAssembledModel(t *testing.T, full bool) Model {
	t.Helper()

	setup := testSetup{
		Ctx: herdrc.Context{
			WorkspaceID:   "ws-1",
			WorkspaceCwd:  "/home/zvi/Projects/herdr-draft",
			FocusedPaneID: "pane-1",
		},
		Config: config.Config{
			BranchPrefix: "zvi/",
			Agents:       config.AgentsConfig{Favorites: []string{"claude", "codex"}},
		},
		Workspaces: []herdrc.WorkspaceInfo{{WorkspaceID: "ws-1", Label: "herdr-draft"}},
	}
	if full {
		setup.Linear = &fakeLinear{issues: frameIssues()}
		setup.LinearCache = frameIssues()
		setup.Clauth = &fakeClauth{status: frameClauthStatus()}
		setup.ClauthStatus = frameClauthStatus()
	}

	m := newTestModel(t, setup)

	// Resolve the directory check the real form-open would: without it
	// WorktreeField stays inert on its deliberately conservative "not a
	// git repo until told otherwise" default (SetGitTarget's own doc
	// comment), which is not the state a user opening this popup in a
	// repo sees.
	next, _ := m.handleDirResult(dirResultMsg{
		req:       request{version: m.dirReqVersion, key: m.dir.Value()},
		dirExists: true,
		isGitRepo: true,
	})
	return next
}

// sectionMarkers maps every Section ID New can assemble to a literal
// string that must appear in the render whenever that section holds focus.
// In v2 that is simply each field's own row label (v2 spec §6's table) --
// v1 needed a per-field rule here because three of its sections were bare
// chip rows with no label at all.
//
// It lost "branch" and "base" with the worktree collapse, and "create"
// keeps its button text: Create is the one section that renders on the
// footer rather than in the stack.
var sectionMarkers = map[string]string{
	"issue":     "issue",
	"dir":       "project",
	"title":     "title",
	"worktree":  "worktree",
	"placement": "placement",
	"agent":     "agent",
	"account":   "account",
	"prompt":    "prompt",
	"create":    "↵ create",
}

// TestAssembledForm_FocusedSectionVisibleAt80x24 is the assertion whose
// absence let C1 ship: for EVERY section in the ring, focusing it and
// rendering the real assembled form at 80x24 must show that section. This
// is the regression that mattered -- with Prompt focused, "Prompt:"
// appeared nowhere in the rendered frame at all.
//
// Both footer buttons are checked on the same renders, since spec §6
// field 9 ("never clipped") and v2 spec §4's `↵ create · esc cancel`
// pair are what a degradation ladder must never spend, whichever field
// happens to hold focus. The key ladder beside them is contextual by
// design (v2 spec §3 rule 4) and has no fixed text to assert here --
// internal/form's TestFooterRungs_PerZone pins that, per zone.
func TestAssembledForm_FocusedSectionVisibleAt80x24(t *testing.T) {
	for _, full := range []bool{true, false} {
		name := "minimal-config"
		if full {
			name = "full-config"
		}
		t.Run(name, func(t *testing.T) {
			m := newAssembledModel(t, full)
			ids := m.form.SectionIDs()
			// v2's stack is eight rows plus the Create section on the
			// footer; the minimal configuration omits issue and account
			// entirely (v2 spec §6.1's "absent by design"), leaving six
			// rows and Create. This is a floor guard against a section
			// silently vanishing -- TestNew_SectionOrder pins the exact
			// list, and its order, for both configurations.
			if len(ids) < 7 {
				t.Fatalf("assembled form has %d sections (%v), want the full v2 spec §6 field list", len(ids), ids)
			}

			for _, id := range ids {
				marker, ok := sectionMarkers[id]
				if !ok {
					t.Fatalf("section %q has no marker in sectionMarkers -- add one when adding a field", id)
				}
				m.form.FocusByID(id)
				frame := ansi.Strip(m.form.ViewAt(80, 24))

				if !strings.Contains(frame, marker) {
					t.Errorf("with %q focused, the render at 80x24 does not contain %q:\n%s", id, marker, frame)
				}
				if !strings.Contains(frame, "↵ create") {
					t.Errorf("with %q focused, the render at 80x24 lost the Create button:\n%s", id, frame)
				}
				if !strings.Contains(frame, "esc cancel") {
					t.Errorf("with %q focused, the render at 80x24 lost the cancel button:\n%s", id, frame)
				}
			}
		})
	}
}

// TestAssembledForm_EverySectionVisibleDownToItsFloor walks the whole
// range of window heights herdr's own popup can produce (a "80%"-height
// popup on terminals from 24 to 60 rows, plus everything below that down
// to a degenerate 8) and asserts the invariant the fix is built on: at
// EVERY height, every section still renders at least its own header row,
// so no field ever silently disappears from the form.
//
// It also reports (via t.Log, not an assertion -- it is a measurement, not
// a contract) the smallest height at which the full form still shows every
// section, which is the number the fix report quotes.
func TestAssembledForm_EverySectionVisibleDownToItsFloor(t *testing.T) {
	m := newAssembledModel(t, true)
	ids := m.form.SectionIDs()

	smallestFit := 0
	for h := 60; h >= 8; h-- {
		fits := true
		for _, id := range ids {
			m.form.FocusByID(id)
			frame := ansi.Strip(m.form.ViewAt(80, h))
			if !strings.Contains(frame, "↵ create") {
				t.Errorf("at 80x%d with %q focused, the Create button was clipped", h, id)
			}
			if !strings.Contains(frame, sectionMarkers[id]) {
				t.Errorf("at 80x%d, the FOCUSED section %q is not rendered", h, id)
			}
			for _, other := range ids {
				if !strings.Contains(frame, sectionMarkers[other]) {
					fits = false
				}
			}
		}
		if fits {
			smallestFit = h
		}
	}

	if smallestFit == 0 {
		t.Fatalf("the full form never showed every section at any height between 8 and 60")
	}
	t.Logf("full assembled form shows every section at 80x%d and taller", smallestFit)
}

// TestAssembledForm_Frames pins the real assembled form at the two sizes
// spec §15 names (80x24 and 120x40), for both the widest configuration
// (Linear plus two clauth profiles) and the minimal one, with focus on the
// Prompt field -- the field C1's own worst case made invisible, and the
// reason these frames exist at all.
//
// The two configurations deliberately differ in more than which fields
// exist: the full one has the worktree toggle ON (Branch and Base live,
// Placement inert -- "a worktree is always its own space", spec §6 field
// 5), the minimal one has it off (Branch and Base carry their distinct
// inert placeholders, Placement live). Between them the four frames cover
// every field's live AND inert rendering under a real budget allocation.
func TestAssembledForm_Frames(t *testing.T) {
	for _, tc := range []struct {
		name     string
		full     bool
		worktree bool
	}{
		{"full", true, true},
		{"minimal", false, false},
	} {
		m := newAssembledModel(t, tc.full)
		m.title.SetTitle("fix login redirect loop", false)
		m.worktree.SetOn(tc.worktree)
		m.worktree.SetBranch("zvi/fix-login-redirect-loop", false)
		m.worktree.SetHeadBranch("main")
		m.worktree.SetBaseItems(1, []string{"main", "release/1.4"})
		m.prompt.SetValue("Work on ENG-101: Fix login redirect loop", false)
		// Through the real reaction path rather than by hand: it is what
		// syncs Placement's inert state, the header's context line AND the
		// title panel's resting note, so the frame shows what a running
		// form shows rather than a hand-assembled subset of it. The Cmds
		// it schedules are debounces nothing in this test ever fires.
		m.reactToChanges()
		m.form.FocusByID("prompt")

		assertAppFrame(t, fmt.Sprintf("assembled-%s-80x24", tc.name), m, 80, 24)
		assertAppFrame(t, fmt.Sprintf("assembled-%s-120x40", tc.name), m, 120, 40)
	}
}

// TestAssembledForm_OpeningState pins the frame every user sees FIRST:
// the fully configured form the instant it opens, before a single
// keystroke -- no title, no prompt, focus on the title row (v2 spec §8),
// the worktree on and its base list resolved.
//
// Nothing covered this. Every other frame in this file types a title and
// a prompt first, internal/form's own fixtures each assemble ONE field,
// and `empty-80x24` over there is an issue-picker fixture with two
// sections in it. So the one state guaranteed to be rendered on every
// single popup open was the one state with no frame -- which is how the
// worktree row shipped reading `on · branch name ← main`, the branch
// editor's placeholder standing in for a branch nobody had named yet.
// Found by running the binary, not by any test.
//
// The worktree toggle, head branch and base list are set by hand for the
// same reason the frames below set them: they arrive from the app layer's
// debounced async checks, which no test ever fires. What is deliberately
// NOT set is anything the user would have had to type.
func TestAssembledForm_OpeningState(t *testing.T) {
	m := newAssembledModel(t, true)
	m.worktree.SetOn(true)
	m.worktree.SetHeadBranch("main")
	m.worktree.SetBaseItems(1, []string{"main", "release/1.4"})
	m.reactToChanges()
	m.form.FocusByID("title")

	assertAppFrame(t, "assembled-opening-80x24", m, 80, 24)
}

// TestAssembledForm_FullAt64x19 pins the size this whole rewrite is
// justified by and which nothing previously covered: the interior of
// herdr's own 80%-height popup on an 80x24 terminal.
//
// It is deliberately a SEPARATE test from the four frames above rather
// than a third size on the same loop: those render with the prompt
// focused (the field v1's allocator made invisible, which is why they
// exist), while the size that matters here should show the form as it
// actually opens -- focused on the title, v2 spec §8's opening state.
func TestAssembledForm_FullAt64x19(t *testing.T) {
	m := newAssembledModel(t, true)
	m.title.SetTitle("fix login redirect loop", false)
	m.worktree.SetOn(true)
	m.worktree.SetBranch("zvi/fix-login-redirect-loop", false)
	m.worktree.SetHeadBranch("main")
	m.worktree.SetBaseItems(1, []string{"main", "release/1.4"})
	m.prompt.SetValue("Work on ENG-101: Fix login redirect loop", false)
	m.reactToChanges()
	m.form.FocusByID("title")

	assertAppFrame(t, "assembled-full-64x19", m, 64, 19)
}
