package app

import (
	"testing"

	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/plan"
)

// TestPlacementRow_InertExactlyWhenThePlanHasAWorktree pins placement spec
// §14 at the seam between the row and the plan. The row goes inert on the
// SAME condition buildPlanInput uses to decide UseWorktree -- a git project
// with the toggle on -- not on the toggle alone: a non-git project keeps
// a remembered "on" underneath its inert worktree row, and a placement row
// that went inert on that would say "the worktree's own space" over a plan
// that has no worktree and does honour the placement.
func TestPlacementRow_InertExactlyWhenThePlanHasAWorktree(t *testing.T) {
	m := newTestModel(t, testSetup{Config: config.Config{DefaultWorktree: true}})

	req := m.scheduleDirCheck("/repo")().(dirDebounceMsg).req
	m, _ = m.handleDirResult(dirResultMsg{req: req, dirExists: true, isGitRepo: true})
	m.reactToChanges()
	if !m.PlanInput().UseWorktree {
		t.Fatal("setup: the plan has no worktree for a git project with the toggle on")
	}
	if m.placement.Enabled() {
		t.Error("placement row is live under a worktree, whose session runs in its own space")
	}
	if got := m.PlanInput().Placement; got != plan.PlacementNewSpace {
		t.Errorf("PlanInput().Placement = %v, want %v under a worktree", got, plan.PlacementNewSpace)
	}

	// The project moves to a directory that is not a repository: the
	// worktree row goes inert but keeps its "on" underneath.
	req = m.scheduleDirCheck("/not-a-repo")().(dirDebounceMsg).req
	m, _ = m.handleDirResult(dirResultMsg{req: req, dirExists: true, isGitRepo: false})
	m.reactToChanges()
	if !m.worktree.On() || m.worktree.Enabled() || m.PlanInput().UseWorktree {
		t.Fatalf("setup: worktree On/Enabled/UseWorktree = %v/%v/%v, want a kept on, inert, and no worktree in the plan",
			m.worktree.On(), m.worktree.Enabled(), m.PlanInput().UseWorktree)
	}
	if !m.placement.Enabled() {
		t.Error("placement row is inert for a non-git project, whose plan has no worktree and honours the placement")
	}
}
