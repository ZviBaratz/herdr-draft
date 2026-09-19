package create

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
)

// #198 from the headless verb. git 2.53, handed a base that names a branch
// only a remote has by its bare name, checks out a new local branch of the
// base's name, and herdr's reply says so. `create` refuses such a --base
// before anything is made (#194), so the reply here comes from a fake: the
// test is about what the report says once internal/plan refuses it.
//
// The worktree step failed after herdr made a checkout, so this is exit 1,
// not 4, and there is a checkout to name and to clean. The clean removes it
// and keeps the branch git made, which nothing shows this run asked for.
func TestCreateReportsAWorktreeOnABranchItDidNotAskFor(t *testing.T) {
	repo := gitRepo(t)
	checkout := filepath.Join(t.TempDir(), "wt")
	h := newHarness(t)
	h.runner.replyBranch = "develop"
	h.runner.worktreeAdd = func(herdrc.WorktreeCreateReq) string {
		gitDo(t, repo, "worktree", "add", "-q", "-b", "develop", checkout)
		return checkout
	}
	h.runner.worktreeRemove = func(string) { gitDo(t, repo, "worktree", "remove", checkout) }

	code := h.run("--title", "remote only", "--worktree", "--project", repo,
		"--branch", "smoke/remote-only", "--on-failure", "clean", "--json")

	if code != ExitFailed {
		t.Fatalf("exit = %d, want %d -- herdr made a checkout\nstderr: %s", code, ExitFailed, h.stderr)
	}
	out := cleanReport(t, h)
	if out.OK || out.FailedStep != "creating worktree" {
		t.Errorf("ok, failed_step = %v, %q, want false, the worktree step", out.OK, out.FailedStep)
	}
	for _, name := range []string{`"develop"`, `"smoke/remote-only"`} {
		if !strings.Contains(out.Error, name) {
			t.Errorf("error = %q, want it to name %s", out.Error, name)
		}
	}
	if out.CheckoutPath != checkout {
		t.Errorf("checkout_path = %q, want %q", out.CheckoutPath, checkout)
	}
	if out.PaneID != "" {
		t.Errorf("pane_id = %q, want none: no agent was placed", out.PaneID)
	}
	if !out.Cleaned || out.KeptBranch != "develop" || out.DeletedBranch != "" {
		t.Errorf("cleaned, kept_branch, deleted_branch = %v, %q, %q; want true, develop, none",
			out.Cleaned, out.KeptBranch, out.DeletedBranch)
	}
	if !localBranch(t, repo, "develop") {
		t.Error("develop is gone; the clean deleted a branch this run did not ask for")
	}
}
