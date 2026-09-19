package create

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ZviBaratz/herdr-draft/internal/config"
)

// These pin #194 on the command side. A base a tier supplies is kept if it
// resolves, is the HEAD row if it is HEAD or @, and otherwise falls back to
// HEAD with a line on stderr -- the same rule the form applies, which
// TestFormAndCommandProduceTheSamePlan holds the two to. An explicit --base
// that is HEAD or @ is the HEAD row too, and one that does not resolve is
// refused.

// rememberBase writes a projects.json remembering base for the harness's
// project, /projects/thing, and nothing else.
func rememberBase(t *testing.T, h *harness, base string) {
	t.Helper()
	writeFile(t, filepath.Join(h.env.StateDir, "projects.json"), rememberedBase("/projects/thing", base))
}

// createdFrom is the one `worktree create` a run asked for, as its base.
func createdFrom(t *testing.T, h *harness) string {
	t.Helper()
	creates := worktreeCreates(h)
	if len(creates) != 1 {
		t.Fatalf("worktree creates = %v, want exactly one", creates)
	}
	return creates[0][strings.LastIndex(creates[0], ",")+1:]
}

// jsonReportOf decodes a --json run's stdout.
func jsonReportOf(t *testing.T, h *harness) jsonReport {
	t.Helper()
	var out jsonReport
	if err := json.Unmarshal([]byte(h.stdout.String()), &out); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, h.stdout)
	}
	return out
}

// TestTierBase_ThatDoesNotResolveFallsBackAndSaysSo is rule 3: a remembered
// branch deleted since is not handed to herdr, the worktree is cut from
// HEAD, and one line says which base was dropped and where it came from.
// The provenance moves with it: the tier that named the ref no longer
// supplies what was used.
func TestTierBase_ThatDoesNotResolveFallsBackAndSaysSo(t *testing.T) {
	h := newHarness(t)
	rememberBase(t, h, "gone-branch")

	if code := h.run("--title", "t", "--worktree", "--json"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	if base := createdFrom(t, h); base != "" {
		t.Errorf("worktree create base = %q, want none: the HEAD row", base)
	}
	var note string
	for line := range strings.Lines(h.stderr.String()) {
		if strings.Contains(line, "gone-branch") {
			note = line
		}
	}
	if !strings.HasPrefix(note, "herdr-draft create: ") || !strings.Contains(note, "projects.json") || !strings.Contains(note, "HEAD") {
		t.Errorf("stderr = %q, want one `herdr-draft create:` line naming gone-branch, projects.json and HEAD", h.stderr)
	}
	out := jsonReportOf(t, h)
	if out.Base != "" || out.Provenance["base"] != "built-in" {
		t.Errorf("--json base = %q from %q, want none from built-in", out.Base, out.Provenance["base"])
	}
}

// TestTierBase_ThatResolvesIsKeptAsItCame is rule 1 on this path: the
// command has no list to fall outside of, so a base that resolves reaches
// herdr as it was written, and nothing is said about it.
func TestTierBase_ThatResolvesIsKeptAsItCame(t *testing.T) {
	h := newHarness(t)
	rememberBase(t, h, "old-branch")

	if code := h.run("--title", "t", "--worktree"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	if base := createdFrom(t, h); base != "old-branch" {
		t.Errorf("worktree create base = %q, want old-branch", base)
	}
	if strings.Contains(h.stderr.String(), "old-branch") {
		t.Errorf("stderr = %q, want nothing said about a base that stands", h.stderr)
	}
}

// TestTierBase_HeadIsTheHeadRowAndIsRememberedAsTheFormRemembersIt is rule
// 2, and the memory half of the issue: each path used to write its own
// spelling of HEAD back to projects.json, the form "" and `create` "HEAD".
// Nothing is said, because nothing was dropped.
func TestTierBase_HeadIsTheHeadRowAndIsRememberedAsTheFormRemembersIt(t *testing.T) {
	for _, ref := range []string{"HEAD", "@"} {
		t.Run(ref, func(t *testing.T) {
			h := newHarness(t)
			rememberBase(t, h, ref)

			if code := h.run("--title", "t", "--worktree", "--json"); code != ExitOK {
				t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
			}
			if base := createdFrom(t, h); base != "" {
				t.Errorf("worktree create base = %q, want none: the HEAD row", base)
			}
			if len(h.git.resolveCalls) != 0 || strings.Contains(h.stderr.String(), "ignoring base") {
				t.Errorf("git asked %v, stderr %q: HEAD needs neither", h.git.resolveCalls, h.stderr)
			}
			if out := jsonReportOf(t, h); out.Base != "" || out.Provenance["base"] != "projects.json" {
				t.Errorf("--json base = %q from %q, want none, still from projects.json", out.Base, out.Provenance["base"])
			}
			projects, _ := config.LoadProjects(h.env.StateDir)
			if entry, _ := projects.Get("/projects/thing"); entry.Base != "" {
				t.Errorf("remembered base = %q, want \"\" -- what the form writes for its HEAD row", entry.Base)
			}
		})
	}
}

// TestFlagBase_HeadIsTheHeadRow: the form has one HEAD row, so --base HEAD
// and --base @ mean what leaving it there means. It is still the caller's
// choice, and --json says so.
func TestFlagBase_HeadIsTheHeadRow(t *testing.T) {
	for _, ref := range []string{"HEAD", "@"} {
		t.Run(ref, func(t *testing.T) {
			h := newHarness(t)
			rememberBase(t, h, "main")

			if code := h.run("--title", "t", "--worktree", "--base", ref, "--json"); code != ExitOK {
				t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
			}
			if base := createdFrom(t, h); base != "" {
				t.Errorf("worktree create base = %q, want none: the HEAD row", base)
			}
			if out := jsonReportOf(t, h); out.Base != "" || out.Provenance["base"] != "flag" {
				t.Errorf("--json base = %q from %q, want none from flag", out.Base, out.Provenance["base"])
			}
		})
	}
}

// TestFlagBase_ThatDoesNotResolveIsRefused: a base asked for by name is not
// swapped for another one. Refused before anything is created, as a lane
// already refused it (#171), where outside a lane it used to reach herdr and
// fail at the worktree step.
func TestFlagBase_ThatDoesNotResolveIsRefused(t *testing.T) {
	h := newHarness(t)

	if code := h.run("--title", "t", "--worktree", "--base", "gone-branch"); code != ExitUsage {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitUsage, h.stderr)
	}
	if msg := h.stderr.String(); !strings.Contains(msg, `"gone-branch"`) || !strings.Contains(msg, "--base") {
		t.Errorf("stderr = %q, want the ref and the flag named", msg)
	}
	if h.createdAnything() {
		t.Errorf("herdr was asked to create something: %v", h.runner.calls)
	}
	if want := []string{"/projects/thing gone-branch"}; !slices.Equal(h.git.resolveCalls, want) {
		t.Errorf("git asked %v, want %v: in the project", h.git.resolveCalls, want)
	}
}

// TestFlagBase_IsNotSettled: an explicit --base replaces the tier's, so the
// tier's is never checked and never reported on, even when it would have
// been dropped.
func TestFlagBase_IsNotSettled(t *testing.T) {
	h := newHarness(t)
	rememberBase(t, h, "gone-branch")

	if code := h.run("--title", "t", "--worktree", "--base", "main"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	if base := createdFrom(t, h); base != "main" {
		t.Errorf("worktree create base = %q, want main", base)
	}
	if strings.Contains(h.stderr.String(), "gone-branch") {
		t.Errorf("stderr = %q, want nothing about a base the flag replaced", h.stderr)
	}
}

// TestLane_ATierBaseThatDoesNotResolveFallsBackToTheLanesHead: #171 refused a
// lane's base that did not resolve, which is right for one the caller named
// and wrong for one a tier remembered -- that is rule 3, and the lane gets it
// too. From HEAD a lane's worktree is cut from the lane's commit, as an unset
// base always was, and --json says the checkout supplied it.
func TestLane_ATierBaseThatDoesNotResolveFallsBackToTheLanesHead(t *testing.T) {
	h := newHarness(t)
	inLane(h)
	writeFile(t, filepath.Join(h.env.StateDir, "projects.json"), rememberedBase(laneRoot, "gone-branch"))

	if code := h.run("--title", "t", "--worktree", "--json"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	if base := createdFrom(t, h); base != laneHead {
		t.Errorf("worktree create base = %q, want the lane's commit %s", base, laneHead)
	}
	if !strings.Contains(h.stderr.String(), `ignoring base "gone-branch"`) {
		t.Errorf("stderr = %q, want the dropped base named", h.stderr)
	}
	if out := jsonReportOf(t, h); out.Provenance["base"] != "checkout" {
		t.Errorf("provenance.base = %q, want checkout", out.Provenance["base"])
	}
}

// TestLane_AFlagBaseThatDoesNotResolveIsStillRefused is the other half: an
// explicit base keeps #171's refusal, from a lane as from anywhere else.
func TestLane_AFlagBaseThatDoesNotResolveIsStillRefused(t *testing.T) {
	h := newHarness(t)
	inLane(h)

	if code := h.run("--title", "t", "--worktree", "--base", "gone-branch"); code != ExitUsage {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitUsage, h.stderr)
	}
	if h.createdAnything() {
		t.Errorf("herdr was asked to create something: %v", h.runner.calls)
	}
}

// TestLane_AFlagHeadIsTheLanesCommitAndStillTheFlags: --base HEAD from a lane
// is cut from the lane's commit, as no base is, and --json reports that
// commit -- but as the caller's choice, not as one the checkout made for
// them.
func TestLane_AFlagHeadIsTheLanesCommitAndStillTheFlags(t *testing.T) {
	h := newHarness(t)
	inLane(h)

	if code := h.run("--title", "t", "--worktree", "--base", "HEAD", "--json"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	out := jsonReportOf(t, h)
	if out.Base != laneHead || out.Provenance["base"] != "flag" {
		t.Errorf("--json base = %q from %q, want the lane's commit %s from flag", out.Base, out.Provenance["base"], laneHead)
	}
}
