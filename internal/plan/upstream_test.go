package plan

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ZviBaratz/herdr-draft/internal/gitx"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
)

// #221: herdr cuts a session's worktree with `git worktree add -b <branch>
// <path> <base>` and no --no-track (build_worktree_add_new_branch_command,
// herdr:src/worktree.rs at v0.9.0), so under git's default
// branch.autoSetupMerge a base that is a remote-tracking branch becomes the
// new branch's upstream. A plain `git push` from the session then fails
// under push.default=simple, and under push.default=upstream lands the
// session's commits on the shared branch. Execute removes that upstream
// once the worktree step has succeeded: only for a branch this run made,
// only when what it tracks is the remote-tracking ref it was cut from, and
// not when the user asked for tracking everywhere.

// upstreamClone is a clone whose origin has main and a develop with a
// commit of its own, plus a second remote whose name holds a slash. The
// global and system config are isolated, so branch.autoSetupMerge is unset
// unless a test sets it.
func upstreamClone(t *testing.T) string {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	upstream := mkRepo(t)
	gitIn(t, upstream, "checkout", "-qb", "develop")
	gitIn(t, upstream, "commit", "-q", "--allow-empty", "-m", "develop's own")
	gitIn(t, upstream, "checkout", "-q", "main")
	repo := filepath.Join(t.TempDir(), "clone")
	gitIn(t, upstream, "clone", "-q", upstream, repo)
	gitIn(t, repo, "remote", "add", "team/x", upstream)
	gitIn(t, repo, "fetch", "-q", "team/x")
	return repo
}

// trackingWorktreeAdd does what herdr's worktree create does, and then what
// git's default autoSetupMerge does to a branch cut from a remote-tracking
// ref -- made explicit with --set-upstream-to, to tracks, rather than left to
// the default of whichever git runs the test. tracks "" sets no upstream.
func trackingWorktreeAdd(t *testing.T, repo, path, tracks string) func(herdrc.WorktreeCreateReq) {
	return func(req herdrc.WorktreeCreateReq) {
		branch := strings.TrimSpace(req.Branch)
		if exists, err := gitx.LocalBranchExists(context.Background(), repo, branch); err == nil && exists {
			gitIn(t, repo, "worktree", "add", "-q", path, branch)
			return
		}
		gitIn(t, repo, "worktree", "add", "-q", "--no-track", "-b", branch, path, req.Base)
		if tracks != "" {
			gitIn(t, repo, "branch", "--set-upstream-to="+tracks, branch)
		}
	}
}

func upstreamOf(t *testing.T, repo, branch string) string {
	t.Helper()
	return strings.TrimSpace(gitOut(t, repo, "for-each-ref", "--format=%(upstream:short)", "refs/heads/"+branch))
}

// executeTracked runs a worktree plan from base whose create leaves the new
// branch tracking tracks, and returns the result and the worktree step's
// last progress.
func executeTracked(t *testing.T, repo, branch, base, tracks string) (ExecResult, Progress) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wt")
	m := &mockRunner{topo: createdTopo(path, branch), onWorktreeCreate: trackingWorktreeAdd(t, repo, path, tracks)}
	in := worktreeInput(repo, branch)
	in.BaseRef = base
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	wt := indexOfKind(ops, OpWorktreeCreate)
	var last Progress
	result := Execute(context.Background(), m, ops, ExecOpts{}, func(p Progress) {
		if p.Index == wt {
			last = p
		}
	})
	return result, last
}

func TestExecuteUnsetsTheUpstreamItsBaseGaveTheBranch(t *testing.T) {
	for _, tc := range []struct{ base, tracks string }{
		{"origin/develop", "origin/develop"},
		{"refs/remotes/origin/develop", "origin/develop"},
		// A remote whose name holds a slash: git's branch.<b>.remote is
		// team/x, which no reading of the base's spelling could split.
		{"team/x/develop", "team/x/develop"},
	} {
		t.Run(tc.base, func(t *testing.T) {
			repo := upstreamClone(t)
			result, step := executeTracked(t, repo, "zvi/session", tc.base, tc.tracks)

			if result.FailedIndex != -1 {
				t.Fatalf("FailedIndex = %d, want -1", result.FailedIndex)
			}
			if got := upstreamOf(t, repo, "zvi/session"); got != "" {
				t.Errorf("zvi/session tracks %q after the create, want nothing: a plain push would go to the base", got)
			}
			if step.State != StepDone {
				t.Errorf("worktree step's last progress = %+v, want StepDone", step)
			}
		})
	}
}

// Every case here leaves the upstream alone, and says nothing: the step is
// StepDone as it was before #221.
func TestExecuteLeavesAnUpstreamItDidNotCause(t *testing.T) {
	for _, tc := range []struct {
		name, base, tracks string
		setup              func(t *testing.T, repo string)
	}{
		{
			// A user who asked for tracking everywhere gets it.
			name: "autoSetupMerge always", base: "origin/develop", tracks: "origin/develop",
			setup: func(t *testing.T, repo string) { gitIn(t, repo, "config", "branch.autoSetupMerge", "always") },
		},
		{
			// Tracking something other than what it was cut from is not
			// what the base did to it.
			name: "tracks another branch", base: "origin/develop", tracks: "origin/main",
		},
		{
			// A local base, which with autoSetupMerge=always would track
			// the local branch (remote "."): not a remote's.
			name: "local base", base: "main", tracks: "main",
		},
		{
			// The branch was already there, so herdr checked it out and
			// the base made nothing: it is the user's, tracking and all.
			name: "branch already existed", base: "origin/develop", tracks: "",
			setup: func(t *testing.T, repo string) {
				gitIn(t, repo, "branch", "--no-track", "zvi/session", "origin/develop")
				gitIn(t, repo, "branch", "--set-upstream-to=origin/develop", "zvi/session")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := upstreamClone(t)
			if tc.setup != nil {
				tc.setup(t, repo)
			}
			want := tc.tracks
			if want == "" {
				want = upstreamOf(t, repo, "zvi/session")
			}
			result, step := executeTracked(t, repo, "zvi/session", tc.base, tc.tracks)

			if result.FailedIndex != -1 {
				t.Fatalf("FailedIndex = %d, want -1", result.FailedIndex)
			}
			if got := upstreamOf(t, repo, "zvi/session"); got != want {
				t.Errorf("zvi/session tracks %q after the create, want %q left as it was", got, want)
			}
			if step.State != StepDone || step.Err != nil {
				t.Errorf("worktree step's last progress = %+v, want StepDone with nothing to report", step)
			}
		})
	}
}

// A failed unset does not fail the create: the checkout exists and the
// agent is next. The worktree step says what is still wrong and the command
// that fixes it, the way a tab that kept its number is reported.
func TestExecuteReportsAnUpstreamItCouldNotUnset(t *testing.T) {
	repo := upstreamClone(t)
	path := filepath.Join(t.TempDir(), "wt")
	track := trackingWorktreeAdd(t, repo, path, "origin/develop")
	m := &mockRunner{
		topo: createdTopo(path, "zvi/session"),
		onWorktreeCreate: func(req herdrc.WorktreeCreateReq) {
			track(req)
			// Another git writing the config at the same moment: git
			// takes this lock for every config write.
			if err := os.WriteFile(filepath.Join(repo, ".git", "config.lock"), nil, 0o644); err != nil {
				t.Fatalf("lock config: %v", err)
			}
		},
	}
	in := worktreeInput(repo, "zvi/session")
	in.BaseRef = "origin/develop"
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	wt := indexOfKind(ops, OpWorktreeCreate)
	var steps []Progress
	result := Execute(context.Background(), m, ops, ExecOpts{}, func(p Progress) {
		if p.Index == wt {
			steps = append(steps, p)
		}
	})

	if result.FailedIndex != -1 {
		t.Fatalf("FailedIndex = %d, want -1: a branch left tracking its base must not fail the create", result.FailedIndex)
	}
	if result.CreatedBranch != "zvi/session" || result.Created == nil {
		t.Errorf("result = %+v, want the space and the branch recorded as for any successful worktree step", result)
	}
	last := steps[len(steps)-1]
	if last.State != StepFailedNonFatal || last.Err == nil {
		t.Fatalf("worktree step's last progress = %+v, want StepFailedNonFatal with the reason", last)
	}
	for _, want := range []string{"origin/develop", "git branch --unset-upstream zvi/session"} {
		if !strings.Contains(last.Err.Error(), want) {
			t.Errorf("error = %q, want it to name %q", last.Err, want)
		}
	}
	if last.Kind != OpWorktreeCreate {
		t.Errorf("progress Kind = %v, want OpWorktreeCreate, so a caller can word the row for it", last.Kind)
	}
	if !callsInclude(m.calls, "AgentStart(") {
		t.Errorf("calls = %v: the plan stopped after the worktree step", m.calls)
	}
}

func callsInclude(calls []string, prefix string) bool {
	for _, c := range calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitTestEnv()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v (in %s): %v", args, dir, err)
	}
	return string(out)
}
