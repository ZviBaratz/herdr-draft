package form

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ZviBaratz/herdr-draft/internal/linear"
	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

func estimate(v float64) *float64 { return &v }

func sampleIssues() []linear.Issue {
	return []linear.Issue{
		{Identifier: "ENG-1", Title: "Fix login bug", StateName: "Todo", Estimate: estimate(3)},
		{Identifier: "ENG-2", Title: "Add dark mode", StateName: "In Progress"},
	}
}

func TestIssueField_IDAndDefault(t *testing.T) {
	f := NewIssueField(theme.Default())
	if f.ID() != "issue" {
		t.Errorf("ID() = %q, want %q", f.ID(), "issue")
	}
	if !f.Enabled() {
		t.Errorf("Enabled() = false, want true (static gate lives in the app layer)")
	}
	if got := f.Selected(); got != nil {
		t.Errorf("Selected() on a fresh field = %+v, want nil (the none row)", got)
	}
}

func TestIssueField_SetIssuesAndSelectByDown(t *testing.T) {
	f := NewIssueField(theme.Default())
	f.SetIssues(1, sampleIssues())

	f.Update(key(tea.KeyDown, 0)) // none -> ENG-1
	sel := f.Selected()
	if sel == nil || sel.Identifier != "ENG-1" {
		t.Fatalf("Selected() after one Down = %+v, want ENG-1", sel)
	}

	f.Update(key(tea.KeyDown, 0)) // ENG-1 -> ENG-2
	sel = f.Selected()
	if sel == nil || sel.Identifier != "ENG-2" {
		t.Fatalf("Selected() after two Downs = %+v, want ENG-2", sel)
	}

	f.Update(key(tea.KeyUp, 0))
	f.Update(key(tea.KeyUp, 0))
	if got := f.Selected(); got != nil {
		t.Fatalf("Selected() back at the top = %+v, want nil (none)", got)
	}
}

// TestIssueField_SelectingEmitsIssueChosenMsg pins the brief's own
// literal requirement: selecting an issue emits IssueChosenMsg{Issue}.
func TestIssueField_SelectingEmitsIssueChosenMsg(t *testing.T) {
	f := NewIssueField(theme.Default())
	f.SetIssues(1, sampleIssues())

	cmd := f.Update(key(tea.KeyDown, 0)) // none -> ENG-1
	if cmd == nil {
		t.Fatalf("Update() returned a nil tea.Cmd, want one emitting IssueChosenMsg")
	}
	msg, ok := cmd().(IssueChosenMsg)
	if !ok {
		t.Fatalf("Update()() = %T, want IssueChosenMsg", cmd())
	}
	if msg.Issue == nil || msg.Issue.Identifier != "ENG-1" {
		t.Fatalf("IssueChosenMsg.Issue = %+v, want ENG-1", msg.Issue)
	}
}

// TestIssueField_SelectingNoneEmitsNilIssue pins the "back to none"
// direction of the same contract: IssueChosenMsg.Issue is nil for manual
// mode, not omitted.
func TestIssueField_SelectingNoneEmitsNilIssue(t *testing.T) {
	f := NewIssueField(theme.Default())
	f.SetIssues(1, sampleIssues())
	f.Update(key(tea.KeyDown, 0)) // none -> ENG-1, consumes the first emission

	cmd := f.Update(key(tea.KeyUp, 0)) // ENG-1 -> none
	if cmd == nil {
		t.Fatalf("Update() returned a nil tea.Cmd, want one emitting IssueChosenMsg{nil}")
	}
	msg, ok := cmd().(IssueChosenMsg)
	if !ok {
		t.Fatalf("Update()() = %T, want IssueChosenMsg", cmd())
	}
	if msg.Issue != nil {
		t.Errorf("IssueChosenMsg.Issue = %+v, want nil", msg.Issue)
	}
}

// TestIssueField_NoSpuriousMsgWhenSelectionUnchanged pins the
// "clamped, not actually moved" guard: Up from the top row (already on
// none) must not re-emit IssueChosenMsg.
func TestIssueField_NoSpuriousMsgWhenSelectionUnchanged(t *testing.T) {
	f := NewIssueField(theme.Default())
	f.SetIssues(1, sampleIssues())

	if cmd := f.Update(key(tea.KeyUp, 0)); cmd != nil {
		t.Errorf("Update(Up) at the top row returned a non-nil Cmd, want nil (selection did not change)")
	}
}

func TestIssueField_TypingFiltersByFuzzySubstring(t *testing.T) {
	f := NewIssueField(theme.Default())
	f.Focus()
	f.SetIssues(1, sampleIssues())

	for _, r := range "dark" {
		f.Update(rn(r))
	}
	f.Update(key(tea.KeyDown, 0)) // none -> the only remaining match

	sel := f.Selected()
	if sel == nil || sel.Identifier != "ENG-2" {
		t.Fatalf("Selected() after filtering to %q = %+v, want ENG-2", "dark", sel)
	}
}

// TestIssueField_SetIssuesStalenessGate mirrors
// field_worktree.go's TestWorktreeField_SetBaseItemsStalenessGate: an
// older-versioned SetIssues call must not clobber a fresher one.
func TestIssueField_SetIssuesStalenessGate(t *testing.T) {
	f := NewIssueField(theme.Default())
	f.SetIssues(2, []linear.Issue{{Identifier: "ENG-9", Title: "fresh"}})
	f.SetIssues(1, []linear.Issue{{Identifier: "ENG-0", Title: "stale"}})

	f.Update(key(tea.KeyDown, 0))
	sel := f.Selected()
	if sel == nil || sel.Identifier != "ENG-9" {
		t.Fatalf("Selected() after a stale SetIssues call = %+v, want the fresher ENG-9 to survive", sel)
	}
}

// TestIssueField_SetIssuesRefreshPreservesSelectionByID mirrors
// field_worktree.go's identical base-picker test: a same-version refresh
// that reorders the issue list must keep the same issue selected by
// identifier.
func TestIssueField_SetIssuesRefreshPreservesSelectionByID(t *testing.T) {
	f := NewIssueField(theme.Default())
	f.SetIssues(1, sampleIssues())
	f.Update(key(tea.KeyDown, 0)) // none -> ENG-1
	f.Update(key(tea.KeyDown, 0)) // ENG-1 -> ENG-2
	if got := f.Selected(); got == nil || got.Identifier != "ENG-2" {
		t.Fatalf("setup: Selected() = %+v, want ENG-2", got)
	}

	reordered := []linear.Issue{sampleIssues()[1], sampleIssues()[0]}
	f.SetIssues(1, reordered)

	if got := f.Selected(); got == nil || got.Identifier != "ENG-2" {
		t.Fatalf("Selected() after a same-version reordering refresh = %+v, want ENG-2 (selection preserved by ID)", got)
	}
}

func TestIssueField_RowShowsStatusAndEstimateHint(t *testing.T) {
	f := NewIssueField(theme.Default())
	f.Focus()
	f.SetIssues(1, sampleIssues())

	frame := fieldText(f, 60)
	if !strings.Contains(frame, "Todo") || !strings.Contains(frame, "est 3") {
		t.Errorf("View(60) = %q, want it to contain the status/estimate hint", frame)
	}
}

func TestIssueField_NoPanicOnDegenerateWidth(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("IssueField panicked: %v", r)
		}
	}()
	f := NewIssueField(theme.Default())
	_ = f.Row(0)
	_ = f.Panel(0, f.PanelRows())
	_ = f.Row(-3)
	_ = f.Panel(-3, f.PanelRows())
}

func TestIssueField_NoPanicBeforeSetIssues(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("IssueField panicked before SetIssues: %v", r)
		}
	}()
	f := NewIssueField(theme.Default())
	f.Focus()
	_ = f.Row(60)
	_ = f.Panel(60, f.PanelRows())
	f.Update(key(tea.KeyDown, 0))
	f.Update(key(tea.KeyUp, 0))
	_ = f.Selected()
}

// TestIssueField_EmptyLinearAndEmptyFilterReadDifferently pins two states
// that used to share one sentence. "Linear has no issues assigned to you"
// and "the text you typed excludes every issue you have" are different
// facts with different fixes, and the panel said `no assigned issues` to
// both -- telling a user with a full queue and a typo that their queue was
// empty.
//
// It also pins the other half of the same defect: the list ABOVE the
// status line stays blank. widgets.Picker used to write its own bare
// "no matches" into row 0, so a filtered-out list printed two sentences
// for one fact, the wrong one first, against v2 spec §6.1's "never a bare
// `no matches`".
func TestIssueField_EmptyLinearAndEmptyFilterReadDifferently(t *testing.T) {
	palette := theme.Default()

	empty := NewIssueField(palette)
	empty.SetIssues(1, nil)
	if got := panelLineAt(empty.Panel(60, 4), 3); got != issuePanelEmpty {
		t.Errorf("panel status with no issues at all = %q, want %q", got, issuePanelEmpty)
	}

	f := NewIssueField(palette)
	f.SetIssues(1, sampleIssues())
	f.Focus()
	if got := panelLineAt(f.Panel(60, 4), 3); got != "" {
		t.Errorf("panel status with issues on offer = %q, want no status line at all", got)
	}

	for _, r := range "zzzz" {
		f.Update(rn(r))
	}
	panel := f.Panel(60, 4)
	if got := panelLineAt(panel, 3); got != issuePanelNoMatch {
		t.Errorf("panel status with a filter matching nothing = %q, want %q", got, issuePanelNoMatch)
	}
	if got := ansi.Strip(panel); strings.Contains(got, "no matches") {
		t.Errorf("panel = %q, want no bare \"no matches\" row (v2 spec §6.1)", got)
	}
	for i := 0; i < 3; i++ {
		if got := panelLineAt(panel, i); got != "" {
			t.Errorf("panel list row %d = %q, want it blank -- the status line owns the sentence", i, got)
		}
	}

	// Both at once: no issues AND a filter matching nothing. The empty
	// queue is the more fundamental fact and wins.
	if got := panelLineAt(empty.Panel(60, 4), 3); got != issuePanelEmpty {
		t.Errorf("panel status with neither issues nor matches = %q, want %q", got, issuePanelEmpty)
	}
}

// TestIssueField_UnavailableIsInertAndCarriesTheReason pins spec §13's
// "degrade ... with a reason" for a Linear integration that is configured
// but broken (finding I5). Absent Linear is still not rendered at all --
// that is the app layer's static precondition -- but a broken api_key_cmd
// used to take the same path, so the field silently vanished with nothing
// anywhere saying why.
func TestIssueField_UnavailableIsInertAndCarriesTheReason(t *testing.T) {
	f := NewIssueField(theme.Default())
	f.SetIssues(1, sampleIssues())

	if !f.Enabled() {
		t.Fatal("Enabled() = false for a healthy field, want true")
	}

	const reason = "run api_key_cmd: exec: \"pass\": executable file not found"
	f.SetUnavailable(reason)

	if f.Enabled() {
		t.Error("Enabled() = true while unavailable, want false (present-but-inert, skipped by the focus ring)")
	}

	frame := fieldText(f, 70)
	if !strings.Contains(frame, "unavailable") {
		t.Errorf("View = %q, want the field marked unavailable", frame)
	}
	if !strings.Contains(frame, "api_key_cmd") {
		t.Errorf("View = %q, want the reason rendered", frame)
	}
	// A field that cannot reach Linear must not offer issues to pick.
	if strings.Contains(frame, "ENG-1") {
		t.Errorf("View = %q, want no candidate rows while unavailable", frame)
	}
	// Even focused (a click can still land on an inert section), the
	// picker stays out of the way.
	f.Focus()
	if frame := fieldText(f, 70); strings.Contains(frame, "ENG-1") {
		t.Errorf("View while focused = %q, want no candidate rows while unavailable", frame)
	}

	f.SetUnavailable("")
	if !f.Enabled() {
		t.Error("Enabled() = false after clearing the reason, want true")
	}
}
