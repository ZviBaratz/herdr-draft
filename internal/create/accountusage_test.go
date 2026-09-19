package create

import (
	"errors"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ZviBaratz/herdr-draft/internal/clauth"
	"github.com/ZviBaratz/herdr-draft/internal/picker"
)

// #215: a dry run's --json reports the usage windows of the account the
// session would bill, so the spawn skill can weigh a model and an effort
// against how full that account is. These tests pin whose windows they
// are, when clauth is read at all, and what an unreadable status does.

var (
	usageReset5h = time.Date(2026, 9, 19, 20, 30, 0, 0, time.UTC)
	usageReset7d = time.Date(2026, 9, 25, 1, 0, 0, 0, time.UTC)
)

// usageStatus has three profiles with different windows, so a report that
// names the wrong one shows it. beta is the active one; alpha-1 carries a
// window with no reset time, which clauth reports as null.
func usageStatus() clauth.Status {
	return clauth.Status{Schema: 1, ActiveProfile: "beta", Profiles: []clauth.Profile{
		{Name: "alpha-1", AuthStatus: "ok", Windows: []clauth.Window{
			{Label: "5h", UtilizationPct: 38, ResetsAt: &usageReset5h},
			{Label: "7d", UtilizationPct: 96, ResetsAt: &usageReset7d},
			{Label: "7d fable", UtilizationPct: 0},
		}},
		{Name: "beta", Active: true, AuthStatus: "ok", Windows: []clauth.Window{
			{Label: "5h", UtilizationPct: 5, ResetsAt: &usageReset5h},
		}},
		{Name: "alpha-2", AuthStatus: "ok", Windows: []clauth.Window{
			{Label: "7d", UtilizationPct: 12, ResetsAt: &usageReset7d},
		}},
		{Name: "idle", AuthStatus: "ok"},
	}}
}

// usageRun runs create with --json and the given args against usageStatus,
// or against what setup leaves in the harness.
func usageRun(t *testing.T, setup func(h *harness), args ...string) (*harness, *fakeClauth, int) {
	t.Helper()
	h := newHarness(t)
	c := &fakeClauth{status: usageStatus()}
	h.deps.Clauth = c
	if setup != nil {
		setup(h)
	}
	code := h.run(append([]string{"--title", "t", "--no-worktree", "--json"}, args...)...)
	return h, c, code
}

func TestAccountUsage_WhoseWindows(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		setup func(h *harness)
		want  *accountUsage
	}{
		{
			name: "a pinned profile's, every window clauth reports",
			args: []string{"--account", "alpha-1"},
			want: &accountUsage{Profile: "alpha-1", Windows: []usageWindow{
				{Label: "5h", UtilizationPct: 38, ResetsAt: &usageReset5h},
				{Label: "7d", UtilizationPct: 96, ResetsAt: &usageReset7d},
				{Label: "7d fable", UtilizationPct: 0},
			}},
		},
		{
			name: "with nothing pinned, clauth's active profile's",
			want: &accountUsage{Profile: "beta", Windows: []usageWindow{
				{Label: "5h", UtilizationPct: 5, ResetsAt: &usageReset5h},
			}},
		},
		{
			name: "for auto, the dry pick's",
			args: []string{"--account", "auto"},
			setup: func(h *harness) {
				h.deps.Picker = &fakePicker{res: picker.Result{Profile: "alpha-2"}}
			},
			want: &accountUsage{Profile: "alpha-2", Windows: []usageWindow{
				{Label: "7d", UtilizationPct: 12, ResetsAt: &usageReset7d},
			}},
		},
		{
			name: "none, for a picked profile clauth does not report",
			args: []string{"--account", "auto"},
			setup: func(h *harness) {
				h.deps.Picker = &fakePicker{res: picker.Result{Profile: "gamma"}}
			},
		},
		{
			name: "none, for a profile that reports no windows",
			args: []string{"--account", "idle"},
		},
		{
			name: "none, for a degraded status: its windows are not to be trusted",
			setup: func(h *harness) {
				st := usageStatus()
				st.Degraded = true
				h.deps.Clauth = &fakeClauth{status: st}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _, code := usageRun(t, tc.setup, append(tc.args, "--dry-run")...)
			if code != ExitOK {
				t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
			}
			if got := decodeReport(t, h.stdout.String()).AccountUsage; !reflect.DeepEqual(got, tc.want) {
				t.Errorf("account_usage = %+v, want %+v\nstdout: %s", got, tc.want, h.stdout)
			}
			if tc.want == nil && strings.Contains(h.stdout.String(), `"account_usage"`) {
				t.Errorf("account_usage present, want it absent:\n%s", h.stdout)
			}
		})
	}
}

// A degraded status leaves the key out and says nothing: the popup's panel
// already says the status is degraded, and a status clauth answered is not
// a failure worth a line on every dry run.
func TestAccountUsage_DegradedIsQuiet(t *testing.T) {
	h, _, code := usageRun(t, func(h *harness) {
		st := usageStatus()
		st.Degraded = true
		h.deps.Clauth = &fakeClauth{status: st}
	}, "--dry-run")
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	if h.stderr.Len() != 0 {
		t.Errorf("stderr for a degraded status, want nothing:\n%s", h.stderr)
	}
}

// A window clauth reports with no reset time says nothing about one, rather
// than "resets_at": null: absent means absent throughout the report.
func TestAccountUsage_NullResetIsOmitted(t *testing.T) {
	h, _, code := usageRun(t, nil, "--account", "alpha-1", "--dry-run")
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
	}
	if strings.Contains(h.stdout.String(), "null") {
		t.Errorf("the report carries a null:\n%s", h.stdout)
	}
	if got := strings.Count(h.stdout.String(), `"resets_at"`); got != 2 {
		t.Errorf("resets_at appears %d times, want 2 (alpha-1's two windows that have one):\n%s", got, h.stdout)
	}
}

// When clauth is read, and how often. The status can be a subprocess, so
// it is read only where the report or a refusal needs it, and once.
func TestAccountUsage_WhenClauthIsRead(t *testing.T) {
	for _, tc := range []struct {
		name      string
		args      []string
		config    string
		human     bool // without --json
		wantReads int
		wantUsage bool
	}{
		{name: "a dry run with nothing pinned", args: []string{"--dry-run"}, wantReads: 1, wantUsage: true},
		{name: "a pinned dry run reads once for the sign-in check and the report", args: []string{"--account", "alpha-1", "--dry-run"}, wantReads: 1, wantUsage: true},
		{name: "a real create with nothing pinned does not read it", wantReads: 0},
		{name: "a pinned real create reads it for the sign-in check alone", args: []string{"--account", "alpha-1"}, wantReads: 1},
		{name: "a dry run without --json has nowhere to report it", args: []string{"--dry-run"}, human: true, wantReads: 0},
		{name: "another agent kind has no account", args: []string{"--agent", "codex", "--dry-run"}, config: "[agents]\nfavorites = [\"claude\", \"codex\"]\n", wantReads: 0},
		{name: "clauth switched off", args: []string{"--dry-run"}, config: "[clauth]\nenabled = false\n", wantReads: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			c := &fakeClauth{status: usageStatus()}
			h.deps.Clauth = c
			if tc.config != "" {
				writeConfig(t, h.env.ConfigDir, tc.config)
			}
			args := append([]string{"--title", "t", "--no-worktree"}, tc.args...)
			if !tc.human {
				args = append(args, "--json")
			}
			if code := h.run(args...); code != ExitOK {
				t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
			}
			if c.calls != tc.wantReads {
				t.Errorf("clauth read %d times, want %d", c.calls, tc.wantReads)
			}
			if got := strings.Contains(h.stdout.String(), `"account_usage"`); got != tc.wantUsage {
				t.Errorf("account_usage present = %v, want %v:\n%s", got, tc.wantUsage, h.stdout)
			}
		})
	}
}

// An unreadable status leaves the key out, and says why on stderr -- unless
// clauth is not installed, which is most people, and which the popup does
// not mention either (#88). A pinned profile's own "could not check" line
// already says it, so it is not said twice.
func TestAccountUsage_UnreadableStatus(t *testing.T) {
	broken := errors.New("clauth status --json: exit status 1: daemon not running")
	missing := fmt.Errorf("clauth status --json: %w", exec.ErrNotFound)
	for _, tc := range []struct {
		name      string
		err       error
		args      []string
		wantLines int
	}{
		{name: "a broken clauth is said once", err: broken, wantLines: 1},
		{name: "a missing clauth is not said", err: missing, wantLines: 0},
		{name: "a pinned profile's check already said it", err: broken, args: []string{"--account", "alpha-1"}, wantLines: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _, code := usageRun(t, func(h *harness) {
				h.deps.Clauth = &fakeClauth{err: tc.err}
			}, append(tc.args, "--dry-run")...)
			if code != ExitOK {
				t.Fatalf("exit = %d, want %d: an unreadable clauth must not stop a dry run\nstderr: %s", code, ExitOK, h.stderr)
			}
			if strings.Contains(h.stdout.String(), `"account_usage"`) {
				t.Errorf("account_usage present without a status to read it from:\n%s", h.stdout)
			}
			if got := strings.Count(h.stderr.String(), "daemon not running") + strings.Count(h.stderr.String(), "executable file not found"); got != tc.wantLines {
				t.Errorf("stderr says why %d times, want %d:\n%s", got, tc.wantLines, h.stderr)
			}
		})
	}
}
