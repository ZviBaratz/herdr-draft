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

func TestIssueField_HeightIsConstant(t *testing.T) {
	f := NewIssueField(theme.Default())
	base := f.Height(24)

	f.SetIssues(1, sampleIssues())
	f.Focus()
	f.Update(key(tea.KeyDown, 0))
	if got := f.Height(24); got != base {
		t.Errorf("Height(24) after selecting = %d, want %d", got, base)
	}
	if got := strings.Count(f.View(60, f.Height(24)), "\n") + 1; got != base {
		t.Errorf("View(60) rendered %d physical lines, want Height()'s own %d", got, base)
	}

	f.Blur()
	if got := f.Height(24); got != base {
		t.Errorf("Height(24) while blurred = %d, want %d", got, base)
	}
	if got := strings.Count(f.View(60, f.Height(24)), "\n") + 1; got != base {
		t.Errorf("View(60) while blurred rendered %d physical lines, want Height()'s own %d", got, base)
	}
}

func TestIssueField_RowShowsStatusAndEstimateHint(t *testing.T) {
	f := NewIssueField(theme.Default())
	f.Focus()
	f.SetIssues(1, sampleIssues())

	frame := ansi.Strip(f.View(60, f.Height(24)))
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
	_ = f.View(0, f.Height(24))
	_ = f.View(-3, f.Height(24))
}

func TestIssueField_NoPanicBeforeSetIssues(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("IssueField panicked before SetIssues: %v", r)
		}
	}()
	f := NewIssueField(theme.Default())
	f.Focus()
	_ = f.View(60, f.Height(24))
	f.Update(key(tea.KeyDown, 0))
	f.Update(key(tea.KeyUp, 0))
	_ = f.Selected()
}
