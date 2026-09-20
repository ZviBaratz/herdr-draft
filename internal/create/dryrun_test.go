package create

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/ZviBaratz/herdr-draft/internal/clauth"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
	"github.com/ZviBaratz/herdr-draft/internal/picker"
)

// `--dry-run` (#209) is how the spawn skill learns what a command will make
// before it asks the user to approve it: the whole pre-flight, then a
// report, and nothing else. These tests hold it to both halves of that --
// it makes nothing, and what it reports is what the real run would.

// dryRunConfig exercises every source the report has to get right: a
// branch prefix, a configured session option, options [agents.extra_args]
// passes on its own, a pinned account, and the reaper toggle.
const dryRunConfig = `branch_prefix = "zvi/"
[agents]
favorites = ["claude"]
[agents.extra_args]
claude = ["--model", "claude-opus-5[1m]", "--effort", "xhigh"]
[agents.options.claude]
permission_mode = "plan"
[clauth]
default = "alpha-1"
[reaper]
mark_ready = true
`

// decodeReport parses --json's object from stdout, failing the test when
// there is none.
func decodeReport(t *testing.T, stdout string) jsonReport {
	t.Helper()
	var out jsonReport
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, stdout)
	}
	return out
}

// resolvedOnly keeps the part of a report both runs can know: everything
// the request resolved to. The ids, the checkout and the prompt's fate
// exist only once something ran, and account_usage only in a dry run
// (#215): it is there to be weighed before approval, and a real create
// does not read clauth for it. dry_run is the other field that is SUPPOSED
// to differ.
func resolvedOnly(r jsonReport) jsonReport {
	r.WorkspaceID, r.TabID, r.PaneID, r.CheckoutPath = "", "", "", ""
	r.SpaceWorkspaceID, r.SpaceTabID, r.SpacePaneID = "", "", ""
	r.PromptSent, r.PromptStatus, r.UnsentPrompt, r.UnconfirmedPrompt = nil, "", "", ""
	r.AccountUsage = nil
	r.DryRun = false
	return r
}

// stateFiles lists what a run left in the plugin state directory.
func stateFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read state dir: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// A dry run reaches herdr only to read the workspace list, writes no
// memory, and exits 0 with a report that says it was a dry run.
func TestDryRun_CreatesNothing(t *testing.T) {
	h := newHarness(t)
	writeConfig(t, h.env.ConfigDir, dryRunConfig)
	h.deps.Stdin = strings.NewReader("do the thing\n")

	code := h.run("--title", "fix login", "--worktree", "--prompt", "-", "--dry-run", "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	if h.createdAnything() {
		t.Errorf("a dry run created something: %v", h.runner.calls)
	}
	for _, c := range h.runner.calls {
		if !strings.HasPrefix(c, "WorkspaceList(") {
			t.Errorf("a dry run made a call other than the workspace list: %s", c)
		}
	}
	if got := stateFiles(t, h.env.StateDir); len(got) != 0 {
		t.Errorf("a dry run wrote state: %v", got)
	}

	out := decodeReport(t, h.stdout.String())
	if !out.OK || !out.DryRun {
		t.Errorf("ok = %v, dry_run = %v; want both true", out.OK, out.DryRun)
	}
	// Nothing ran, so there is no pane to name and no prompt fate to report
	// -- and a prompt_status of "unsent" here would tell the caller to
	// resend a prompt it never needed to.
	if out.PaneID != "" || out.SpacePaneID != "" || out.PromptStatus != "" || out.PromptSent != nil {
		t.Errorf("a dry run reported execution it did not do:\n%s", h.stdout)
	}
}

// The point of a dry run is that its report is the real run's, so a
// spawner can show the user what they are approving. Each case runs the
// same request twice, in two identical harnesses, once with --dry-run.
func TestDryRun_ReportsWhatTheRunWould(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		setup func(h *harness)
	}{
		{
			name: "a worktree from a base, with a prompt",
			args: []string{"--title", "fix login", "--worktree", "--base", "main", "--prompt", "-", "--effort", "high"},
		},
		{
			name:  "no worktree, into the project's open space",
			args:  []string{"--title", "review the parser", "--no-worktree", "--prompt", "-", "--no-reap", "--model", "sonnet"},
			setup: openRepoSpace,
		},
		{
			name: "an account the picker chooses, with clauth to read",
			args: []string{"--title", "fix login", "--worktree", "--account", "auto"},
			setup: func(h *harness) {
				h.deps.Picker = &fakePicker{res: picker.Result{Profile: "alpha-2"}}
				h.deps.Clauth = &fakeClauth{status: usageStatus()}
			},
		},
		{
			name:  "a worktree from inside a linked checkout, base unset",
			args:  []string{"--title", "fix login", "--worktree"},
			setup: inLane,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reports := make([]jsonReport, 2)
			for i, dry := range []bool{false, true} {
				h := newHarness(t)
				writeConfig(t, h.env.ConfigDir, dryRunConfig)
				h.deps.Stdin = strings.NewReader("do the thing\n")
				if tc.setup != nil {
					tc.setup(h)
				}
				args := append([]string{"--json"}, tc.args...)
				if dry {
					args = append(args, "--dry-run")
				}
				if code := h.run(args...); code != ExitOK {
					t.Fatalf("dry=%v: exit = %d, want %d\nstderr: %s", dry, code, ExitOK, h.stderr)
				}
				reports[i] = decodeReport(t, h.stdout.String())
			}
			real, dry := reports[0], reports[1]
			if real.DryRun {
				t.Error("the real run reported dry_run")
			}
			// Guards the comparison below against passing on two reports
			// that are equal because both are empty.
			if dry.Account == "" || len(dry.LaunchOptions) == 0 || len(dry.Provenance) == 0 {
				t.Fatalf("the dry run's report is missing what it exists to show:\n%+v", dry)
			}
			if !reflect.DeepEqual(resolvedOnly(real), resolvedOnly(dry)) {
				t.Errorf("the dry run reports something the run does not.\nrun: %+v\ndry: %+v", resolvedOnly(real), resolvedOnly(dry))
			}
		})
	}
}

// Every refusal a real create makes before anything exists, a dry run makes
// the same way: a caller that validated a command with --dry-run must not
// then see it refused.
func TestDryRun_RefusesWhatTheRunRefuses(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		setup func(h *harness)
		want  int
	}{
		{
			name:  "a branch that already exists",
			args:  []string{"--worktree", "--branch", "feature/login"},
			setup: func(h *harness) { h.git.branches = map[string]bool{"feature/login": true} },
			want:  ExitUsage,
		},
		{
			name: "a title a workspace already carries",
			args: []string{"--no-worktree"},
			setup: func(h *harness) {
				h.runner.workspaces = []herdrc.WorkspaceInfo{{WorkspaceID: "wX", Label: "fix login"}}
			},
			want: ExitUsage,
		},
		{
			name: "an effort claude does not take",
			args: []string{"--no-worktree", "--effort", "ultra"},
			want: ExitUsage,
		},
		{
			name: "auto with no picker to ask",
			args: []string{"--no-worktree", "--account", "auto"},
			want: ExitUsage,
		},
		{
			name: "the picker refuses",
			args: []string{"--no-worktree", "--account", "auto"},
			setup: func(h *harness) {
				h.deps.Picker = &fakePicker{err: &picker.RefusalError{Code: picker.ExitExhausted, Reason: "pool exhausted (resets 22:49)"}}
			},
			want: ExitUsage,
		},
		{
			// `broken` since #243 -- `expired` is a token between
			// refreshes and no longer refuses anything, so a dry run of it
			// would report a create that a real run would make.
			name: "a pinned profile whose credential clauth reports dead",
			args: []string{"--no-worktree", "--account", "alpha-2"},
			setup: func(h *harness) {
				h.deps.Clauth = &fakeClauth{status: clauth.Status{Schema: 1, Profiles: []clauth.Profile{{Name: "alpha-2", AuthStatus: clauth.AuthBroken}}}}
			},
			want: ExitUsage,
		},
		{
			name:  "herdr unreachable",
			args:  []string{"--no-worktree"},
			setup: func(h *harness) { h.runner.listErr = errors.New("connection refused") },
			want:  ExitUnreachable,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stderr := make([]string, 2)
			for i, dry := range []bool{false, true} {
				h := newHarness(t)
				if tc.setup != nil {
					tc.setup(h)
				}
				args := append([]string{"--title", "fix login", "--json"}, tc.args...)
				if dry {
					args = append(args, "--dry-run")
				}
				if code := h.run(args...); code != tc.want {
					t.Errorf("dry=%v: exit = %d, want %d\nstderr: %s", dry, code, tc.want, h.stderr)
				}
				if h.stdout.Len() != 0 {
					t.Errorf("dry=%v: a refusal printed on stdout:\n%s", dry, h.stdout)
				}
				stderr[i] = h.stderr.String()
			}
			// The same reason, not just the same code: the caller fixes the
			// command from this text, so a dry run that refused for another
			// reason would send them to fix the wrong thing.
			if stderr[0] != stderr[1] {
				t.Errorf("the dry run's refusal differs from the run's.\nrun: %s\ndry: %s", stderr[0], stderr[1])
			}
		})
	}
}

// `--account auto` under --dry-run asks the picker for its --dry-run answer,
// which writes no ledger and so spends nothing -- the popup's live preview
// does the same. Still strict: nobody is at the keyboard for this either.
func TestDryRun_AutoAccountSpendsNoPick(t *testing.T) {
	h := newHarness(t)
	p := &fakePicker{res: picker.Result{Profile: "alpha-2"}}
	h.deps.Picker = p

	if code := h.run("--title", "fix login", "--no-worktree", "--account", "auto", "--dry-run", "--json"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	if len(p.calls) != 1 {
		t.Fatalf("picker calls = %d, want exactly 1", len(p.calls))
	}
	if !p.calls[0].DryRun {
		t.Error("a dry run's pick must be the picker's own --dry-run: anything else spends an account")
	}
	if !p.calls[0].Strict {
		t.Error("a dry run's pick must still be --strict: nobody is at the keyboard")
	}
	if got := decodeReport(t, h.stdout.String()).Account; got != "alpha-2" {
		t.Errorf("account = %q, want the picker's answer alpha-2", got)
	}
}

// Without --json a dry run prints one line on stdout, which says it would
// create and names what the command line does not.
func TestDryRun_HumanLine(t *testing.T) {
	h := newHarness(t)
	writeConfig(t, h.env.ConfigDir, dryRunConfig)

	if code := h.run("--title", "fix login", "--worktree", "--dry-run"); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	line := strings.TrimRight(h.stdout.String(), "\n")
	if strings.Contains(line, "\n") {
		t.Errorf("stdout is more than one line:\n%s", line)
	}
	if !strings.HasPrefix(line, "would create ") {
		t.Errorf("stdout does not start with %q: %s", "would create ", line)
	}
	for _, want := range []string{
		`title="fix login"`,
		"agent=claude",
		"branch=zvi/fix-login",
		"base=HEAD",
		"account=alpha-1",
		"model=claude-opus-5[1m]",
		"effort=xhigh",
		"permission_mode=plan",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("stdout does not name %s:\n%s", want, line)
		}
	}
	if strings.HasPrefix(line, "created") {
		t.Errorf("a dry run printed the created line: %s", line)
	}
}
