package herdrc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// forwardingRefused is herdr 0.9.1's stderr, verbatim, for `herdr --machine
// <id> status server` against dev's 0.9.0 server, captured 2026-10-13
// (machine spec §3.4): a Rust Debug of an io::Error, with no JSON envelope.
const forwardingRefused = `Error: Custom { kind: Unsupported, error: "machine 'dev (EC2)': remote Herdr does not support machine API forwarding; update Herdr on this machine" }`

// machineListJSON is `herdr machine list --json` as herdr 0.9.1 printed it
// on the owner's laptop on 2026-10-13: a bare array, no envelope.
const machineListJSON = `[
  {
    "id": "af0915ce314d4345e77e3d31e22e8679",
    "label": "dev (EC2)",
    "target": "dev",
    "session": "default",
    "enabled": true,
    "selected": false
  },
  {
    "id": "fb473b9d019c8b09c2ec6da8f4acdaa0",
    "label": "nanoclaw",
    "target": "nanoclaw",
    "session": "default",
    "enabled": true,
    "selected": false
  }
]`

// fakeHerdrRouted writes a fake herdr that logs every argv and answers by
// what was asked, so one binary can stand behind all four of CLIRunner's
// exec sites in a single test.
func fakeHerdrRouted(t *testing.T) (bin, argvLog string) {
	t.Helper()
	dir := t.TempDir()
	argvLog = filepath.Join(dir, "argv")
	bin = filepath.Join(dir, "herdr")
	script := `#!/bin/sh
echo "$@" >> ` + argvLog + `
case "$*" in
  *"agent get"*) cat <<'EOF'
` + agentGetJSON(`"agent_status":"idle","interactive_ready":true`) + `
EOF
  ;;
  *"agent read"*) echo "screen text" ;;
  *"pane run"*) ;;
  *) echo '{"id":"x","result":{"workspaces":[]}}' ;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, argvLog
}

// TestCLIRunnerOnPrefixesEveryExecSite: a machine-scoped runner must put
// `--machine <id>` before the subcommand on every path that runs herdr --
// runJSON, runOK, runText, and pollDetection's own `agent get` -- because a
// single unprefixed call would act on the LOCAL server with an id that
// means something else there (machine spec §8.1).
func TestCLIRunnerOnPrefixesEveryExecSite(t *testing.T) {
	ctx := context.Background()
	bin, argvLog := fakeHerdrRouted(t)
	r := (&CLIRunner{Bin: bin, PollInterval: time.Millisecond}).On("af0915ce")

	if _, err := r.WorkspaceList(ctx); err != nil {
		t.Fatalf("WorkspaceList: %v", err)
	}
	if err := r.PaneRun(ctx, "w1:p1", nil, []string{"true"}); err != nil {
		t.Fatalf("PaneRun: %v", err)
	}
	if _, err := r.AgentRead(ctx, "w1:p1"); err != nil {
		t.Fatalf("AgentRead: %v", err)
	}
	if err := r.AwaitDetection(ctx, "w1:p1", time.Second, 0); err != nil {
		t.Fatalf("AwaitDetection: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(readArgvLog(t, argvLog)), "\n")
	if len(lines) < 4 {
		t.Fatalf("got %d invocations, want at least 4:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "--machine af0915ce ") {
			t.Errorf("invocation %q is not routed to the machine", l)
		}
	}
}

// TestCLIRunnerOnEmptyIsLocal: "" is this machine, whatever the runner it
// was scoped from -- which is what lets plan call On(machine.ID) on every op
// without a branch for local.
func TestCLIRunnerOnEmptyIsLocal(t *testing.T) {
	bin, argvLog := fakeHerdrRouted(t)
	r := (&CLIRunner{Bin: bin}).On("af0915ce").On("")
	if _, err := r.WorkspaceList(context.Background()); err != nil {
		t.Fatalf("WorkspaceList: %v", err)
	}
	if got := strings.TrimSpace(readArgvLog(t, argvLog)); got != "workspace list" {
		t.Errorf("argv = %q, want an unprefixed workspace list", got)
	}
}

// TestCLIRunnerOnDoesNotScopeTheOriginal: On returns a scoped copy. The
// popup and create share one runner, and scoping it in place would send
// the next local call to the machine.
func TestCLIRunnerOnDoesNotScopeTheOriginal(t *testing.T) {
	bin, argvLog := fakeHerdrRouted(t)
	local := &CLIRunner{Bin: bin}
	_ = local.On("af0915ce")
	if _, err := local.WorkspaceList(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(readArgvLog(t, argvLog)); got != "workspace list" {
		t.Errorf("argv = %q: On scoped the runner it was called on", got)
	}
}

// TestCLIRunnerMachineListDecodesTheBareArray: `machine list --json` prints
// an array, not runJSON's envelope -- a third success shape -- and it is a
// local command that herdr refuses to forward, so a scoped runner must
// still ask it locally.
func TestCLIRunnerMachineListDecodesTheBareArray(t *testing.T) {
	bin, argvLog := fakeHerdr(t, machineListJSON)
	got, err := (&CLIRunner{Bin: bin}).On("af0915ce").MachineList(context.Background())
	if err != nil {
		t.Fatalf("MachineList: %v", err)
	}
	want := []MachineInfo{
		{ID: "af0915ce314d4345e77e3d31e22e8679", Label: "dev (EC2)", Target: "dev", Session: "default", Enabled: true},
		{ID: "fb473b9d019c8b09c2ec6da8f4acdaa0", Label: "nanoclaw", Target: "nanoclaw", Session: "default", Enabled: true},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d machines, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("machine %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if argv := strings.TrimSpace(readArgvLog(t, argvLog)); argv != "machine list --json" {
		t.Errorf("argv = %q, want a local `machine list --json`", argv)
	}
}

// TestCLIRunnerNotificationShow: `notification show` prints an envelope
// (print_response, herdr:src/cli/notification.rs at v0.9.0), so it goes
// through runJSON; the title and body are argv, never interpolated.
func TestCLIRunnerNotificationShow(t *testing.T) {
	bin, argvLog := fakeHerdr(t, `{"id":"cli:notification:show","result":{"type":"ok"}}`)
	err := (&CLIRunner{Bin: bin}).NotificationShow(context.Background(), "fix it opened on dev (EC2)", "")
	if err != nil {
		t.Fatalf("NotificationShow: %v", err)
	}
	if got := strings.TrimSpace(readArgvLog(t, argvLog)); got != "notification show fix it opened on dev (EC2)" {
		t.Errorf("argv = %q", got)
	}
}

// TestCLIErrorShowsAWrappedTransportFailurePlainly: herdr reports a machine
// it could not reach as a Debug-printed io::Error. The message underneath is
// what a person should read; the wrapper is noise. Display only: it carries
// no code, so it classifies as nothing (#144), and it is not marked
// "nothing created" -- a bridge that dropped after sending may have made
// the space (machine spec §8.1).
func TestCLIErrorShowsAWrappedTransportFailurePlainly(t *testing.T) {
	bin := fakeHerdrFail(t, strings.ReplaceAll(forwardingRefused, `"`, `\"`))
	_, err := (&CLIRunner{Bin: bin}).On("af0915ce").WorkspaceCreate(context.Background(), WorkspaceCreateReq{Cwd: "/r", Label: "t"})
	if err == nil {
		t.Fatal("expected an error")
	}
	want := "machine 'dev (EC2)': remote Herdr does not support machine API forwarding; update Herdr on this machine"
	if !strings.HasSuffix(err.Error(), ": "+want) {
		t.Errorf("error = %q\nwant it to end with herdr's own message, unwrapped", err)
	}
	if strings.Contains(err.Error(), "Custom {") {
		t.Errorf("error still carries the Debug wrapper: %q", err)
	}
	if herdrErrorCode(err) != "" {
		t.Errorf("a transport failure was classified as code %q", herdrErrorCode(err))
	}
	if errors.Is(err, ErrNothingCreated) {
		t.Error("a transport failure was marked nothing-created")
	}
}

// TestReasonIsHerdrsOwnWords: a refusal row has room for herdr's sentence,
// not the argv in front of it -- which, on a machine call, is a 32-hex
// profile id. Anything that is not a herdr failure is its own text.
func TestReasonIsHerdrsOwnWords(t *testing.T) {
	bin := fakeHerdrFail(t, strings.ReplaceAll(forwardingRefused, `"`, `\"`))
	_, err := (&CLIRunner{Bin: bin}).On("af0915ce").WorkspaceList(context.Background())
	want := "machine 'dev (EC2)': remote Herdr does not support machine API forwarding; update Herdr on this machine"
	if got := Reason(err); got != want {
		t.Errorf("Reason = %q\nwant     %q", got, want)
	}
	silent := fakeHerdrFail(t, "")
	_, err = (&CLIRunner{Bin: silent}).WorkspaceList(context.Background())
	if got := Reason(err); got != "herdr workspace list: exit status 1" {
		t.Errorf("Reason of a silent failure = %q, want the whole error", got)
	}
	if got := Reason(errors.New("plain")); got != "plain" {
		t.Errorf("Reason(plain) = %q", got)
	}
}
