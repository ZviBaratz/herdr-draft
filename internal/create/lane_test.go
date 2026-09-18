package create

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/ZviBaratz/herdr-draft/internal/config"
)

// These pin #171 on the command side: `create` run from inside a linked
// worktree -- a lane, which is where the spawn skill's agents run -- with
// --project left off. herdr refuses a linked checkout as a worktree source,
// so the worktree is created from the repository root, and cut from the
// lane's own commit when no base is chosen.

const (
	// laneDir is the linked checkout the command runs in, and laneRoot the
	// repository's primary checkout behind it.
	laneDir  = "/worktrees/thing-lane"
	laneRoot = "/projects/thing"
	laneHead = "3f2a9c1e5b7d4a608e1f2b3c4d5e6f708192a3b4"
)

// inLane runs h's command from laneDir.
func inLane(h *harness) {
	h.deps.Workdir = func() (string, error) { return laneDir, nil }
	h.git.roots = map[string]string{laneDir: laneRoot}
	h.git.linked = map[string]bool{laneDir: true}
	h.git.head = laneHead
}

// worktreeCreates is every `worktree create` the runner was asked for, as
// "cwd,branch,base".
func worktreeCreates(h *harness) []string {
	var out []string
	for _, c := range h.runner.calls {
		if args, ok := strings.CutPrefix(c, "WorktreeCreate("); ok {
			out = append(out, strings.TrimSuffix(args, ")"))
		}
	}
	return out
}

// TestLane_AWorktreeIsCutFromTheLanesCommitInTheRepoRoot is the issue's
// first two rows: with --worktree, and with no worktree flag at all, since
// the built-in default is a worktree. Both used to hand herdr the lane as
// --cwd and fail with linked_worktree_source.
func TestLane_AWorktreeIsCutFromTheLanesCommitInTheRepoRoot(t *testing.T) {
	for _, args := range [][]string{
		{"--title", "t", "--worktree"},
		{"--title", "t"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			h := newHarness(t)
			inLane(h)

			if code := h.run(args...); code != ExitOK {
				t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
			}
			creates := worktreeCreates(h)
			if len(creates) != 1 {
				t.Fatalf("worktree creates = %v, want exactly one", creates)
			}
			cwd, rest, _ := strings.Cut(creates[0], ",")
			if cwd != laneRoot {
				t.Errorf("worktree create --cwd = %q, want the repository root %q", cwd, laneRoot)
			}
			if !strings.HasSuffix(rest, ","+laneHead) {
				t.Errorf("worktree create branch,base = %q, want the base to be the lane's commit %s", rest, laneHead)
			}
		})
	}
}

// TestLane_NoWorktreeStaysInTheLane is the issue's third row, which already
// worked and must keep working: without a worktree the session runs in the
// lane's own checkout (spawn skill §3), and nothing asks for its commit.
func TestLane_NoWorktreeStaysInTheLane(t *testing.T) {
	h := newHarness(t)
	inLane(h)

	if code := h.run("--title", "t", "--no-worktree"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	if !slices.Contains(h.runner.calls, "WorkspaceCreate("+laneDir+",t)") {
		t.Errorf("calls = %v, want a workspace in the lane's checkout", h.runner.calls)
	}
	if h.git.headCalls != 0 {
		t.Errorf("the lane's commit was resolved %d times for a session with no worktree", h.git.headCalls)
	}
}

// TestLane_AChosenBaseStillWins: the lane's commit is only what an UNSET
// base means. The source still moves to the repository root.
func TestLane_AChosenBaseStillWins(t *testing.T) {
	h := newHarness(t)
	inLane(h)

	if code := h.run("--title", "t", "--worktree", "--base", "main"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	creates := worktreeCreates(h)
	if len(creates) != 1 || !strings.HasPrefix(creates[0], laneRoot+",") || !strings.HasSuffix(creates[0], ",main") {
		t.Errorf("worktree creates = %v, want one from %s cut from main", creates, laneRoot)
	}
	if h.git.headCalls != 0 {
		t.Errorf("the lane's commit was resolved %d times with --base given", h.git.headCalls)
	}
}

// TestLane_JSONReportsTheCommitAndWhereItCameFrom: a caller reading --json
// learns the exact commit the branch was cut from, and that it came from the
// checkout rather than from a flag or a tier. The project is still the lane.
func TestLane_JSONReportsTheCommitAndWhereItCameFrom(t *testing.T) {
	h := newHarness(t)
	inLane(h)

	if code := h.run("--title", "t", "--worktree", "--json"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	var out jsonReport
	if err := json.Unmarshal([]byte(h.stdout.String()), &out); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, h.stdout)
	}
	if out.Base != laneHead {
		t.Errorf("base = %q, want the lane's commit %q", out.Base, laneHead)
	}
	if got := out.Provenance["base"]; got != "checkout" {
		t.Errorf("provenance.base = %q, want %q", got, "checkout")
	}
	if out.ProjectDir != laneDir {
		t.Errorf("project_dir = %q, want the lane %q", out.ProjectDir, laneDir)
	}
}

// TestLane_TheCommitIsNotRemembered: projects.json is keyed on the origin
// root, so a remembered commit would pin every later session in the
// repository -- from any checkout -- to this one. What was chosen was no
// base at all, and that is what is remembered.
func TestLane_TheCommitIsNotRemembered(t *testing.T) {
	h := newHarness(t)
	inLane(h)

	if code := h.run("--title", "t", "--worktree"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	projects, _ := config.LoadProjects(h.env.StateDir)
	entry, ok := projects.Get(projectMemoryKey(laneDir, laneRoot))
	if !ok {
		t.Fatalf("projects.json has no entry for the repository: %+v", projects)
	}
	if entry.Base != "" {
		t.Errorf("remembered base = %q, want none", entry.Base)
	}
}

// TestLane_AnUnresolvableCommitIsRefused: with no commit to cut from, the
// only thing left to pass herdr is no base, which would cut the branch from
// the PRIMARY checkout's HEAD -- a different commit, silently. Refused
// before anything is created, naming the way out.
func TestLane_AnUnresolvableCommitIsRefused(t *testing.T) {
	h := newHarness(t)
	inLane(h)
	h.git.headErr = errors.New("ambiguous argument 'HEAD': unknown revision")

	if code := h.run("--title", "t", "--worktree"); code != ExitUsage {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitUsage, h.stderr)
	}
	if !strings.Contains(h.stderr.String(), "--base") {
		t.Errorf("stderr = %q, want the remedy (--base) named", h.stderr)
	}
	if h.createdAnything() {
		t.Errorf("herdr was asked to create something: %v", h.runner.calls)
	}
}
