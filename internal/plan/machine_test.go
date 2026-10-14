package plan

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ZviBaratz/herdr-draft/internal/gitx"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
)

// devMachine is a remote session's machine as both callers resolve it from
// machine.Select and the clone probe: the profile id herdr is handed, the
// label messages use, the ssh target git goes over, and the project's
// absolute directory there.
var devMachine = Machine{ID: "af09", Label: "dev (EC2)", Target: "devbox", Dir: "/home/ubuntu/quantivly/hub"}

func remoteInput() Input {
	in := validInput()
	in.Machine = devMachine
	in.BaseRef = "origin/main"
	return in
}

// fakeSSH puts an `ssh` on PATH that runs the remote command locally with
// `sh -c`, dropping its options and target, and logs each command line it
// ran -- the far end of gitx.Over, so a remote plan's git reaches a temp
// repository standing in for dev's clone. prelude runs first, for a test
// that needs ssh itself to fail.
func fakeSSH(t *testing.T, prelude string) (logPath string) {
	t.Helper()
	bin := t.TempDir()
	logPath = filepath.Join(t.TempDir(), "ssh.log")
	script := `#!/bin/sh
` + prelude + `
while [ $# -gt 0 ]; do
  case "$1" in
    -o) shift 2 ;;
    --) shift; break ;;
    -*) shift ;;
    *) break ;;
  esac
done
shift
printf '%s\n' "$*" >> "` + logPath + `"
exec sh -c "$*"
`
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

// TestMachineZeroValueIsThisMachine: a plan nobody gave a machine is local
// and asks this machine's git, which is every plan before this field.
func TestMachineZeroValueIsThisMachine(t *testing.T) {
	var m Machine
	if m.Remote() || m.Git().Remote() {
		t.Error("the zero Machine is not this machine")
	}
	if !devMachine.Remote() || !devMachine.Git().Remote() {
		t.Error("a Machine with a profile id is not remote")
	}
}

// TestBuildRemoteWorktreeCutsFromTheRemoteClone: the worktree is created
// from the project's directory THERE, every op carries the machine so
// Execute can scope herdr and git to it, and the labels say where.
func TestBuildRemoteWorktreeCutsFromTheRemoteClone(t *testing.T) {
	in := remoteInput()
	in.UseWorktree = true
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got, want := kindsOf(ops), []OpKind{OpWorktreeCreate, OpTabRename, OpAgentStart}; !reflect.DeepEqual(got, want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	if got := ops[0].Worktree.Cwd; got != devMachine.Dir {
		t.Errorf("worktree --cwd = %q, want the remote clone %q", got, devMachine.Dir)
	}
	if got := ops[0].Worktree.Base; got != "origin/main" {
		t.Errorf("worktree --base = %q", got)
	}
	for i, op := range ops {
		if op.Machine != devMachine {
			t.Errorf("op %d (%v) carries machine %+v", i, op.Kind, op.Machine)
		}
	}
	if !strings.HasSuffix(ops[0].Label, " on dev (EC2)") {
		t.Errorf("label %q does not say where", ops[0].Label)
	}
}

// TestBuildRemoteSessionOpensASpaceThere: without a worktree the session's
// space is a new workspace in the remote directory -- absolute, because
// `workspace create --cwd` is not tilde-expanded by the server (machine
// spec §3.2).
func TestBuildRemoteSessionOpensASpaceThere(t *testing.T) {
	ops, err := Build(remoteInput())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if ops[0].Kind != OpWorkspaceCreate || ops[0].Workspace.Cwd != devMachine.Dir {
		t.Fatalf("op 0 = %v %+v, want a workspace create in %q", ops[0].Kind, ops[0].Workspace, devMachine.Dir)
	}
}

// TestBuildRefusesWhatARemoteSessionCannotBe: each of these needs
// something only this machine has -- the caller's pane, a local space, a
// clauth profile, a commit only this clone holds -- or something only the
// probe can supply. Both callers refuse them first; this is the floor.
func TestBuildRefusesWhatARemoteSessionCannotBe(t *testing.T) {
	tests := map[string]func(*Input){
		"tab here":   func(in *Input) { in.Placement = PlacementTabHere },
		"split here": func(in *Input) { in.Placement = PlacementSplitHere },
		"tab in": func(in *Input) {
			in.Placement = PlacementTabIn
			in.Space = Space{WorkspaceID: "w3", Label: "hub"}
		},
		"an account pin": func(in *Input) { in.AccountPin = "quantivly-4" },
		"a lane commit": func(in *Input) {
			in.UseWorktree = true
			in.Linked = LinkedCheckout{RepoRoot: "/home/zvi/quantivly/hub", Commit: strings.Repeat("a", 40)}
		},
		"a worktree with no base": func(in *Input) { in.UseWorktree = true; in.BaseRef = "" },
		"no directory there":      func(in *Input) { in.Machine.Dir = "" },
	}
	for name, mutate := range tests {
		in := remoteInput()
		mutate(&in)
		if _, err := Build(in); err == nil {
			t.Errorf("%s: Build accepted it for a session on another machine", name)
		}
	}
}

// TestEffectivePlacementOnAnotherMachineIsANewSpace is placement spec §14's
// second case (machine spec §6.2).
func TestEffectivePlacementOnAnotherMachineIsANewSpace(t *testing.T) {
	for _, p := range []Placement{PlacementTabHere, PlacementSplitHere, PlacementTabIn, PlacementNewSpace} {
		if got := EffectivePlacement(false, true, p); got != PlacementNewSpace {
			t.Errorf("EffectivePlacement(remote, %v) = %v", p, got)
		}
		if got := EffectivePlacement(false, false, p); got != p {
			t.Errorf("EffectivePlacement(local, %v) = %v, want it unchanged", p, got)
		}
	}
}

// TestExecuteScopesTheRunnerToTheMachine: Execute scopes herdr itself, so a
// caller that hands it the local runner still reaches the machine -- and
// nothing reaches herdr before the scoping.
func TestExecuteScopesTheRunnerToTheMachine(t *testing.T) {
	ops, err := Build(remoteInput())
	if err != nil {
		t.Fatal(err)
	}
	m := &mockRunner{topo: herdrc.CreatedTopology{WorkspaceID: "w1", TabID: "w1:t1", PaneID: "w1:p1"}}
	Execute(context.Background(), m, ops, ExecOpts{}, nil)
	if len(m.calls) == 0 || m.calls[0] != "On(af09)" {
		t.Fatalf("calls = %v, want On(af09) first", m.calls)
	}
}

// remoteClone is a clone standing in for dev's: cloned from upstream, then
// left behind while upstream moves on -- the shape dev's clones were in
// (last fetched 2026-09-16, machine spec §3.3).
func remoteClone(t *testing.T) (upstream, remote string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	upstream = mkRepo(t)
	remote = filepath.Join(t.TempDir(), "remote")
	gitIn(t, upstream, "clone", "-q", upstream, remote)
	gitIn(t, upstream, "commit", "-q", "--allow-empty", "-m", "merged after dev last fetched")
	return upstream, remote
}

// TestExecuteFetchesOnTheRemoteBeforeCutting is machine spec §8.3: the
// remote clone is fetched first, so the worktree is cut from origin/main as
// it is NOW rather than as dev last saw it, and the base recorded for the
// clean gate is that commit.
func TestExecuteFetchesOnTheRemoteBeforeCutting(t *testing.T) {
	upstream, remote := remoteClone(t)
	fakeSSH(t, "")
	path := filepath.Join(t.TempDir(), "wt")
	var originMainAtCreate string
	m := &mockRunner{
		topo: createdTopo(path, "zvi/new"),
		onWorktreeCreate: func(req herdrc.WorktreeCreateReq) {
			originMainAtCreate = revOf(t, remote, "refs/remotes/origin/main")
			trackingWorktreeAdd(t, remote, path, "origin/main")(req)
		},
	}
	in := remoteInput()
	in.UseWorktree = true
	in.Branch = "zvi/new"
	in.Machine.Dir = remote

	result, failed := executeRemote(t, in, m)

	if result.FailedIndex != -1 {
		t.Fatalf("FailedIndex = %d: %v", result.FailedIndex, failed)
	}
	want := revOf(t, upstream, "HEAD")
	if originMainAtCreate != want {
		t.Errorf("origin/main at create = %s, want upstream's %s: the remote was not fetched first", originMainAtCreate, want)
	}
	if result.BaseCommit != want {
		t.Errorf("BaseCommit = %s, want %s", result.BaseCommit, want)
	}
	if result.CreatedBranch != "zvi/new" {
		t.Errorf("CreatedBranch = %q: the branch's absence was not read on the remote", result.CreatedBranch)
	}
	if got := revParseUpstream(t, remote, "zvi/new"); got != "" {
		t.Errorf("zvi/new still tracks %q on the remote: #221's untrack did not run there", got)
	}
}

// executeRemote runs in's plan and returns the failed step's error with
// the result -- ExecResult carries no error; Progress does.
func executeRemote(t *testing.T, in Input, m *mockRunner) (ExecResult, error) {
	t.Helper()
	ops, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var failed error
	result := Execute(context.Background(), m, ops, ExecOpts{}, func(p Progress) {
		if p.State == StepFailed {
			failed = p.Err
		}
	})
	return result, failed
}

func revParseUpstream(t *testing.T, dir, branch string) string {
	t.Helper()
	ok, _ := gitx.Upstream(context.Background(), dir, branch)
	return ok
}

// TestExecuteRefusesToCutFromAnUnfetchedBase: a fetch that fails means the
// base might be a month stale, which is the failure the fetch exists to
// prevent -- so herdr is never asked, and the plan says nothing exists.
func TestExecuteRefusesToCutFromAnUnfetchedBase(t *testing.T) {
	_, remote := remoteClone(t)
	fakeSSH(t, `case "$*" in *fetch*) echo "fatal: unable to access 'https://github.com/': Could not resolve host" >&2; exit 128 ;; esac`)
	m := &mockRunner{topo: createdTopo(filepath.Join(t.TempDir(), "wt"), "zvi/new")}
	in := remoteInput()
	in.UseWorktree = true
	in.Branch = "zvi/new"
	in.Machine.Dir = remote

	result, failed := executeRemote(t, in, m)

	if result.FailedIndex != 0 {
		t.Fatalf("FailedIndex = %d, want 0", result.FailedIndex)
	}
	for _, c := range m.calls {
		if strings.HasPrefix(c, "WorktreeCreate") {
			t.Fatalf("herdr was asked to cut a worktree after the fetch failed: %v", m.calls)
		}
	}
	if !result.NothingCreated {
		t.Error("NothingCreated = false for a plan that never asked herdr for anything")
	}
	if failed == nil || !strings.Contains(failed.Error(), "Could not resolve host") {
		t.Errorf("error = %v, want git's own words", failed)
	}
}

// TestCleanOnARemoteSessionDeletesOnlyTheRemoteBranch is the regression
// machine spec §8.4 names: before plan carried the machine, Clean asked the
// LAPTOP's clone for the session's branch and would have deleted a laptop
// branch that merely shared its name.
func TestCleanOnARemoteSessionDeletesOnlyTheRemoteBranch(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	laptop, remote := mkRepo(t), mkRepo(t)
	gitIn(t, laptop, "branch", "x")
	wt := mkWorktree(t, remote, "x")
	fakeSSH(t, "")
	m := &mockRunner{onWorktreeRemove: func(string) { gitIn(t, remote, "worktree", "remove", wt) }}
	created := createdTopo(wt, "x")
	result := ExecResult{Created: &created, AgentAt: &created, CreatedBranch: "x"}
	in := worktreeInput(laptop, "x")
	in.Machine = devMachine
	in.Machine.Dir = remote

	if d := CleanCheck(context.Background(), in, result); !d.Allowed {
		t.Fatalf("CleanCheck refused: %s", d.Reason)
	}
	out, err := Clean(context.Background(), m, in, result)
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if out.DeletedBranch != "x" || branchExists(t, remote, "x") {
		t.Errorf("the remote's x survived: %+v", out)
	}
	if !branchExists(t, laptop, "x") {
		t.Fatal("a remote session's clean deleted the laptop's branch x")
	}
	if len(m.calls) == 0 || m.calls[0] != "On(af09)" {
		t.Errorf("calls = %v, want the clean scoped to the machine first", m.calls)
	}
}

// TestCleanClosesARemoteWorkspaceWhateverItsId: workspace ids are per
// server, so a remote w1 is not the caller's local w1, and the guard that
// refuses to close "the workspace this create was invoked from" does not
// apply to a space on another machine.
func TestCleanClosesARemoteWorkspaceWhateverItsId(t *testing.T) {
	m := &mockRunner{}
	created := herdrc.CreatedTopology{WorkspaceID: "w1", TabID: "w1:t1", PaneID: "w1:p1"}
	in := remoteInput()
	if in.Ctx.WorkspaceID != "w1" {
		t.Fatalf("fixture: the caller's workspace is %q, want w1", in.Ctx.WorkspaceID)
	}
	if _, err := Clean(context.Background(), m, in, ExecResult{Created: &created, AgentAt: &created}); err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if want := []string{"On(af09)", "WorkspaceClose(w1)"}; !reflect.DeepEqual(m.calls, want) {
		t.Errorf("calls = %v, want %v", m.calls, want)
	}
}

// TestRemoteGitFailureNamesTheMachine: a step that failed on dev says so,
// since its pane is on a screen the laptop is not showing.
func TestRemoteGitFailureNamesTheMachine(t *testing.T) {
	_, remote := remoteClone(t)
	fakeSSH(t, `echo "Token for dev is expired" >&2; exit 255`)
	in := remoteInput()
	in.UseWorktree = true
	in.Branch = "zvi/new"
	in.Machine.Dir = remote
	_, failed := executeRemote(t, in, &mockRunner{})
	if failed == nil || !strings.Contains(failed.Error(), "dev (EC2)") {
		t.Errorf("error = %v, want it to name the machine", failed)
	}
}

// TestExplainBlockedStartSaysWhereThePaneIs: a remote session's dialog is
// on a screen the laptop is not showing, so the instruction names the
// machine -- still inside what the popup's row has room for.
func TestExplainBlockedStartSaysWhereThePaneIs(t *testing.T) {
	m := &mockRunner{readText: trustDialogScreen}
	err := explainBlockedStart(context.Background(), m, devMachine, "claude", "pane-1", notReadyErr("fix-pagination"))
	head := firstRunes(err.Error(), 60)
	if !strings.Contains(head, "answer the dialog on dev (EC2)") {
		t.Errorf("first 60 runes = %q, want the instruction to name the machine", head)
	}
}

// TestExecuteRetriesARemoteFetchOnce: sessions spawned together into one
// clone fetch at once, and git refuses the second while the first holds a
// ref lock. One retry turns that into a create rather than an exit 4.
func TestExecuteRetriesARemoteFetchOnce(t *testing.T) {
	_, remote := remoteClone(t)
	counter := filepath.Join(t.TempDir(), "fetches")
	fakeSSH(t, `case "$*" in *fetch*)
  n=$(cat `+counter+` 2>/dev/null || echo 0); n=$((n+1)); echo $n > `+counter+`
  if [ "$n" -eq 1 ]; then echo "error: cannot lock ref 'refs/remotes/origin/main'" >&2; exit 1; fi ;;
esac`)
	path := filepath.Join(t.TempDir(), "wt")
	m := &mockRunner{topo: createdTopo(path, "zvi/new"), onWorktreeCreate: trackingWorktreeAdd(t, remote, path, "")}
	in := remoteInput()
	in.UseWorktree = true
	in.Branch = "zvi/new"
	in.Machine.Dir = remote

	result, failed := executeRemote(t, in, m)
	if result.FailedIndex != -1 {
		t.Fatalf("a fetch that failed once on a ref lock failed the create: %v", failed)
	}
}
