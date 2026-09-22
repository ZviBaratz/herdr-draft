package app

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/ZviBaratz/herdr-draft/internal/agentopts"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
	"github.com/ZviBaratz/herdr-draft/internal/linear"
)

// The showcase frames are the README's screenshots. `just screenshots`
// turns each one into docs/images/<name>.svg through hack/frames/svg.py,
// and TestShowcaseImagesAreCurrent, at the bottom of this file, fails when
// a committed image no longer matches the frame it was made from.
//
// They are ordinary golden frames on purpose. A screenshot taken by hand
// drifts the day the UI moves and nothing notices; one generated from a
// frame this suite asserts cannot, because the frame fails first and
// regenerating it (`go test ./internal/app/ -update`) is the same step that
// regenerates every other frame. What they add over the frames beside them
// is only their DATA: every name on screen is neutral -- a project, issues,
// sessions and accounts that belong to nobody -- because these are the
// frames people outside the project will read. The rest of this package's
// fixtures use real repository and organisation names, which is fine for a
// test and wrong for a README.
//
// So the setup below starts from frameSetup and replaces exactly the
// fields that carry a name. Everything else -- the favourites, the clauth
// fixture (whose profiles are already called `personal` and `work`), the
// fake runner and git -- is the same one every other frame renders over,
// so a showcase frame cannot quietly show a form the tests do not.

// showcaseProject is the repository the showcase form opens in. It sits
// under testHomeDir, so the project row renders it as `~/src/acme-api` and
// the home directory's own name never reaches the frame.
const showcaseProject = testHomeDir + "/src/acme-api"

// showcaseIssues is the Linear queue the issue picker shows: enough to
// read as a real queue, in three states, and one issue with no estimate,
// so every column the picker draws is in use and one of them is ragged.
func showcaseIssues() []linear.Issue {
	est := func(f float64) *float64 { return &f }
	return []linear.Issue{
		{Identifier: "ACME-142", Title: "Fix login redirect loop", BranchName: "you/acme-142-fix-login-redirect-loop",
			StateName: "In Progress", StateType: "started", Estimate: est(3), Priority: 2},
		{Identifier: "ACME-139", Title: "Add rate limiting to the public API", BranchName: "you/acme-139-rate-limiting",
			StateName: "Todo", StateType: "unstarted", Estimate: est(5), Priority: 2},
		{Identifier: "ACME-151", Title: "Retry webhook deliveries with backoff", BranchName: "you/acme-151-webhook-retries",
			StateName: "In Review", StateType: "started", Estimate: est(2), Priority: 1},
		{Identifier: "ACME-128", Title: "Upgrade the Postgres driver", BranchName: "you/acme-128-postgres-driver",
			StateName: "Todo", StateType: "unstarted", Priority: 3},
		{Identifier: "ACME-160", Title: "Flaky checkout test on CI", BranchName: "you/acme-160-flaky-checkout-test",
			StateName: "Todo", StateType: "unstarted", Estimate: est(1), Priority: 4},
	}
}

// showcaseWorkspaces is the session list the title panel shows. ws-1 is
// the invoking workspace, which titleSessions drops, so what is listed is
// the other three -- one per agent status word herdr reports, and one
// session with no worktree context, as frameWorkspaces covers for the same
// reasons.
func showcaseWorkspaces() []herdrc.WorkspaceInfo {
	return []herdrc.WorkspaceInfo{
		{WorkspaceID: "ws-1", Label: "acme-api", AgentStatus: "idle", PaneCount: 2,
			Worktree: &herdrc.ContextWorktree{RepoName: "acme-api"}},
		{WorkspaceID: "ws-2", Label: "docs-site", AgentStatus: "working", PaneCount: 3,
			Worktree: &herdrc.ContextWorktree{RepoName: "docs-site"}},
		{WorkspaceID: "ws-3", Label: "billing-webhooks", AgentStatus: "blocked", PaneCount: 1,
			Worktree: &herdrc.ContextWorktree{RepoName: "acme-api"}},
		{WorkspaceID: "ws-4", Label: "scratch", AgentStatus: "idle", PaneCount: 2},
	}
}

// showcaseSetup is frameSetup(true) with every name replaced by a neutral
// one: the project, the branch prefix, the sessions and the issue queue.
func showcaseSetup() testSetup {
	setup := frameSetup(true)
	setup.Ctx.WorkspaceCwd = showcaseProject
	setup.Config.BranchPrefix = "you/"
	setup.Workspaces = showcaseWorkspaces()
	setup.Linear = &fakeLinear{issues: showcaseIssues()}
	setup.LinearCache = showcaseIssues()
	return setup
}

// showcaseModel is the showcase form as it stands the moment it opens: the
// dir check landed, the worktree on and the base list resolved, applied by
// hand for TestAssembledForm_OpeningState's reason -- they arrive from
// debounced async checks no test fires. setup lets a frame change what
// only New can apply (a config.toml option, say) before that.
func showcaseModel(t *testing.T, setup testSetup) Model {
	t.Helper()
	m := resolveDirCheck(t, newTestModel(t, setup))
	m.worktree.SetOn(true)
	m.worktree.SetHeadBranch("main")
	m.worktree.SetBaseItems(1, []string{"main", "release/1.4"})
	m.reactToChanges()
	return m
}

// showcaseFrameName is the golden file a showcase frame is pinned in. The
// `showcase-` prefix is what TestShowcaseImagesAreCurrent looks for, and
// `just screenshots` names each frame explicitly.
func showcaseFrameName(state string) string {
	return fmt.Sprintf("showcase-%s-%dx%d", state, framePopupW, framePopupH)
}

// TestShowcaseFrames pins the five screens the README shows, at the size
// the popup ships at. Each is taken the way a person would reach it: the
// title is typed key by key through Update, and the picker's cursor is
// moved the same way, so the frame is what the running form draws rather
// than a state assembled field by field.
func TestShowcaseFrames(t *testing.T) {
	// The first screen anyone sees: focus on an empty title, the worktree
	// on, the sessions already open listed underneath.
	t.Run("opening", func(t *testing.T) {
		m := showcaseModel(t, showcaseSetup())
		m.form.FocusByID("title")
		assertAppFrame(t, showcaseFrameName("opening"), m, framePopupW, framePopupH)
	})

	// The same screen after typing a title: the branch derived from it on
	// the worktree row and in the title panel's note, which is the whole
	// "type a title, press enter" workflow in one frame.
	t.Run("titled", func(t *testing.T) {
		m := showcaseModel(t, showcaseSetup())
		m.form.FocusByID("title")
		m = typeInto(t, m, "fix flaky checkout test")
		assertAppFrame(t, showcaseFrameName("titled"), m, framePopupW, framePopupH)
	})

	// The issue row focused, with the picker listing the queue and the
	// cursor moved off its `none` row onto the first issue, which is where
	// a person picking one lands.
	t.Run("issue-picker", func(t *testing.T) {
		m := showcaseModel(t, showcaseSetup())
		// The opening fetch lands first. Every other showcase frame is
		// the form at rest, and this one is a picture of the picker
		// rather than of the wait in front of it: left out, the panel's
		// status line reads `fetching assigned issues…` for good, since
		// no fixture ever answers the fetch (draw-first spec §6).
		m, _ = m.handleLinearResult(linearResultMsg{issues: showcaseIssues()})
		m.form.FocusByID("issue")
		m = press(t, m, key(tea.KeyDown, 0))
		assertAppFrame(t, showcaseFrameName("issue-picker"), m, framePopupW, framePopupH)
	})

	// The options row focused, with both places an option's value can come
	// from on screen at once: the model pinned by `[agents.extra_args]`,
	// which the first chip names as what the session gets, and effort and
	// mode chosen in `[agents.options.claude]`. The cursor is on the model,
	// so the panel's lines explain the pin and name config.toml.
	t.Run("options", func(t *testing.T) {
		setup := showcaseSetup()
		setup.Config.Agents.ExtraArgs = map[string][]string{"claude": {"--model", "opus"}}
		setup.Config.Agents.Options = map[string]agentopts.Values{
			"claude": {"effort": "high", "permission_mode": "plan"},
		}
		m := showcaseModel(t, setup)
		m.form.FocusByID("options")
		m.reactToChanges()
		assertAppFrame(t, showcaseFrameName("options"), m, framePopupW, framePopupH)
	})

	// The account row focused: both clauth profiles with their usage
	// gauges and reset times.
	t.Run("account", func(t *testing.T) {
		m := showcaseModel(t, showcaseSetup())
		m.form.FocusByID("account")
		m.reactToChanges()
		assertAppFrame(t, showcaseFrameName("account"), m, framePopupW, framePopupH)
	})
}

// typeInto types s into the focused field one key at a time, through the
// Model's own Update, which runs the same reaction a real keystroke does.
func typeInto(t *testing.T, m Model, s string) Model {
	t.Helper()
	for _, r := range s {
		m = press(t, m, rn(r))
	}
	return m
}

// showcaseImages and showcaseConverter are where `just screenshots` writes
// and what it runs, from this package's directory, which is where `go test`
// runs it.
const (
	showcaseImages    = "../../docs/images"
	showcaseConverter = "../../hack/frames/svg.py"
)

// The provenance svg.py writes into every image it draws: the frame, by file
// name, and a digest of each of the frame and the converter itself.
var (
	showcaseFrameComment     = regexp.MustCompile(`<!-- frame (\S+) sha256=([0-9a-f]{64}) -->`)
	showcaseGeneratorComment = regexp.MustCompile(`<!-- generator hack/frames/svg\.py sha256=([0-9a-f]{64}) -->`)
)

// TestShowcaseImagesAreCurrent is what makes the README's screenshots
// unable to drift. The showcase frames above fail when the UI moves; this
// fails when a committed image was drawn from an older frame, or by an
// older converter, than the ones in the tree -- so after either, CI is red
// until someone runs `just screenshots`.
//
// It compares digests rather than re-drawing, because re-drawing needs
// Python and `just check` must not. The digests are written by svg.py
// itself, so a hand-edited image keeps passing -- the one thing this does
// not catch, and not a mistake anyone makes by accident, since each image
// opens by saying it is generated.
//
// It also fails for a showcase frame no image was drawn from, which is how
// a frame added above without a line in the recipe would otherwise sit
// unused, and for an image whose frame is gone.
func TestShowcaseImagesAreCurrent(t *testing.T) {
	converter, err := os.ReadFile(showcaseConverter)
	if err != nil {
		t.Fatalf("read the converter: %v", err)
	}
	converterSum := sha256Hex(converter)

	images, err := filepath.Glob(filepath.Join(showcaseImages, "*.svg"))
	if err != nil {
		t.Fatal(err)
	}
	drawn := map[string]bool{}
	for _, path := range images {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		img := "docs/images/" + filepath.Base(path) // as the repository root spells it
		frameM, genM := showcaseFrameComment.FindSubmatch(b), showcaseGeneratorComment.FindSubmatch(b)
		if frameM == nil && genM == nil {
			continue // not drawn by svg.py: a hand capture is not this test's business
		}
		if frameM == nil || genM == nil {
			t.Errorf("%s carries half of svg.py's provenance -- run `just screenshots`", img)
			continue
		}
		name := string(frameM[1])
		if filepath.Base(name) != name {
			t.Errorf("%s names its frame %q, which is not a file name", img, name)
			continue
		}
		drawn[name] = true
		frame, err := os.ReadFile(filepath.Join("testdata", "frames", name))
		if err != nil {
			t.Errorf("%s was drawn from %s, which is gone -- delete the image, or restore the frame and run `just screenshots`", img, name)
			continue
		}
		if sha256Hex(frame) != string(frameM[2]) {
			t.Errorf("%s was drawn from an older %s -- run `just screenshots`", img, name)
		}
		if string(genM[1]) != converterSum {
			t.Errorf("%s was drawn by an older hack/frames/svg.py -- run `just screenshots`", img)
		}
	}

	frames, err := filepath.Glob(filepath.Join("testdata", "frames", "showcase-*.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) == 0 {
		t.Fatal("no showcase frames under testdata/frames -- the glob, or the prefix, has moved")
	}
	for _, f := range frames {
		if !drawn[filepath.Base(f)] {
			t.Errorf("no image under docs/images was drawn from %s -- add it to `just screenshots` and run it", filepath.Base(f))
		}
	}
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
