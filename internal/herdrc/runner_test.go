package herdrc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeHerdr writes a disposable shell script to t.TempDir() that logs its
// argv (one line per invocation) to argvLog and echoes stdout verbatim.
func fakeHerdr(t *testing.T, stdout string) (bin, argvLog string) {
	t.Helper()
	dir := t.TempDir()
	argvLog = filepath.Join(dir, "argv")
	bin = filepath.Join(dir, "herdr")
	script := "#!/bin/sh\necho \"$@\" >> " + argvLog + "\ncat <<'EOF'\n" + stdout + "\nEOF\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, argvLog
}

// fakeHerdrFail writes a disposable shell script that always fails with the
// given stderr message and exit code 1.
func fakeHerdrFail(t *testing.T, stderr string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "herdr")
	script := "#!/bin/sh\necho \"" + stderr + "\" 1>&2\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// fakeHerdrOK writes a disposable shell script that logs its argv and
// exits 0 printing NOTHING at all -- neither stdout nor stderr -- matching
// herdr's real `send_ok_request`-backed subcommands (herdr:src/cli.rs;
// `pane run` is the one CLIRunner.PaneRun uses, herdr:src/cli/pane.rs
// pane_run): success is reported by exit code alone, with no JSON
// envelope. Task 19's live checkpoint found the original fakeHerdr-based
// PaneRun test modeled the wrong contract (a canned JSON stdout the real
// `pane run` never actually prints), which is exactly why a PaneRun that
// unconditionally failed against the real CLI still passed its own unit
// test -- this fake exists so that class of gap can't recur silently.
func fakeHerdrOK(t *testing.T) (bin, argvLog string) {
	t.Helper()
	dir := t.TempDir()
	argvLog = filepath.Join(dir, "argv")
	bin = filepath.Join(dir, "herdr")
	script := "#!/bin/sh\necho \"$@\" >> " + argvLog + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, argvLog
}

// fakeHerdrHang writes a disposable shell script that ignores its arguments
// and sleeps for sleepSeconds before ever exiting -- used to prove that a
// poll bound to a deadline-limited context gets killed at the deadline
// rather than being waited out to completion.
func fakeHerdrHang(t *testing.T, sleepSeconds int) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "herdr")
	script := "#!/bin/sh\nsleep " + strconv.Itoa(sleepSeconds) + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// fakeHerdrFlaky writes a disposable shell script that fails (exit 1,
// stderr "not found") for the first failCount invocations, using a counter
// file to track how many times it has run, then succeeds and echoes
// successStdout on every invocation after that. Every invocation's argv is
// logged to argvLog (one line per call, success or failure) so tests can
// assert how many times a poller called it.
func fakeHerdrFlaky(t *testing.T, failCount int, successStdout string) (bin, argvLog string) {
	t.Helper()
	dir := t.TempDir()
	argvLog = filepath.Join(dir, "argv")
	counter := filepath.Join(dir, "count")
	bin = filepath.Join(dir, "herdr")
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> " + argvLog + "\n" +
		"n=$(cat " + counter + " 2>/dev/null || echo 0)\n" +
		"n=$((n + 1))\n" +
		"echo $n > " + counter + "\n" +
		"if [ \"$n\" -le " + strconv.Itoa(failCount) + " ]; then\n" +
		"  echo \"not found\" 1>&2\n" +
		"  exit 1\n" +
		"fi\n" +
		"cat <<'EOF'\n" + successStdout + "\nEOF\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, argvLog
}

func readArgvLog(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read argv log: %v", err)
	}
	return strings.TrimRight(string(b), "\n")
}

func readFixture(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return string(b)
}

func TestParseContext(t *testing.T) {
	raw := readFixture(t, filepath.Join("testdata", "context.json"))

	ctx, err := ParseContext(raw)
	if err != nil {
		t.Fatalf("ParseContext: %v", err)
	}

	if ctx.WorkspaceID != "w6" {
		t.Errorf("WorkspaceID = %q, want w6", ctx.WorkspaceID)
	}
	if ctx.WorkspaceLabel != "probe-ctx" {
		t.Errorf("WorkspaceLabel = %q, want probe-ctx", ctx.WorkspaceLabel)
	}
	if ctx.WorkspaceCwd != "/home/user/.herdr/worktrees/throwaway-repo/probe-ctx" {
		t.Errorf("WorkspaceCwd = %q", ctx.WorkspaceCwd)
	}
	if ctx.TabID != "w6:t1" {
		t.Errorf("TabID = %q, want w6:t1", ctx.TabID)
	}
	if ctx.FocusedPaneID != "w6:p1" {
		t.Errorf("FocusedPaneID = %q, want w6:p1", ctx.FocusedPaneID)
	}
	if ctx.FocusedPaneCwd != "/home/user/.herdr/worktrees/throwaway-repo/probe-ctx" {
		t.Errorf("FocusedPaneCwd = %q", ctx.FocusedPaneCwd)
	}
	if ctx.FocusedPaneAgent != "claude" {
		t.Errorf("FocusedPaneAgent = %q, want claude", ctx.FocusedPaneAgent)
	}
	if ctx.Worktree == nil {
		t.Fatal("Worktree = nil, want non-nil")
	}
	if ctx.Worktree.RepoKey != "/var/tmp/throwaway-repo/.git" {
		t.Errorf("Worktree.RepoKey = %q", ctx.Worktree.RepoKey)
	}
	if ctx.Worktree.RepoName != "throwaway-repo" {
		t.Errorf("Worktree.RepoName = %q", ctx.Worktree.RepoName)
	}
	if ctx.Worktree.RepoRoot != "/var/tmp/throwaway-repo" {
		t.Errorf("Worktree.RepoRoot = %q", ctx.Worktree.RepoRoot)
	}
	if ctx.Worktree.CheckoutPath != "/home/user/.herdr/worktrees/throwaway-repo/probe-ctx" {
		t.Errorf("Worktree.CheckoutPath = %q", ctx.Worktree.CheckoutPath)
	}
	if !ctx.Worktree.IsLinkedWorktree {
		t.Error("Worktree.IsLinkedWorktree = false, want true")
	}
}

func TestParseContextAbsentFieldsAreZeroValue(t *testing.T) {
	// worktree, focused_pane_agent, and every other optional field are
	// legitimately absent for a non-worktree, no-focused-agent context --
	// PluginInvocationContext marks every field Optional on the Rust side.
	ctx, err := ParseContext(`{"workspace_id":"w1"}`)
	if err != nil {
		t.Fatalf("ParseContext: %v", err)
	}
	if ctx.WorkspaceID != "w1" {
		t.Errorf("WorkspaceID = %q, want w1", ctx.WorkspaceID)
	}
	if ctx.Worktree != nil {
		t.Errorf("Worktree = %+v, want nil", ctx.Worktree)
	}
	if ctx.FocusedPaneAgent != "" {
		t.Errorf("FocusedPaneAgent = %q, want empty", ctx.FocusedPaneAgent)
	}
}

func TestParseContextInvalidJSON(t *testing.T) {
	if _, err := ParseContext("not json"); err == nil {
		t.Fatal("expected an error for invalid JSON, got nil")
	}
}

func TestCLIRunnerWorktreeCreate(t *testing.T) {
	stdout := readFixture(t, filepath.Join("testdata", "live", "worktree_create.json"))
	bin, argvLog := fakeHerdr(t, stdout)
	r := &CLIRunner{Bin: bin}

	req := WorktreeCreateReq{
		Cwd:    "/var/tmp/throwaway-repo",
		Branch: "probe/x",
		Base:   "main",
		Label:  "probe-x",
		Focus:  true,
	}
	topo, err := r.WorktreeCreate(context.Background(), req)
	if err != nil {
		t.Fatalf("WorktreeCreate: %v", err)
	}

	wantArgv := "worktree create --cwd /var/tmp/throwaway-repo --branch probe/x --base main --label probe-x --focus"
	if got := readArgvLog(t, argvLog); got != wantArgv {
		t.Errorf("argv = %q, want %q", got, wantArgv)
	}

	want := CreatedTopology{
		WorkspaceID:  "w3",
		TabID:        "w3:t1",
		PaneID:       "w3:p1",
		CheckoutPath: "/home/user/.herdr/worktrees/throwaway-repo/probe-x",
	}
	if topo != want {
		t.Errorf("topo = %+v, want %+v", topo, want)
	}
}

func TestCLIRunnerWorktreeCreateNoFocus(t *testing.T) {
	stdout := readFixture(t, filepath.Join("testdata", "live", "worktree_create.json"))
	bin, argvLog := fakeHerdr(t, stdout)
	r := &CLIRunner{Bin: bin}

	req := WorktreeCreateReq{Cwd: "/var/tmp/repo", Branch: "probe/x", Focus: false}
	if _, err := r.WorktreeCreate(context.Background(), req); err != nil {
		t.Fatalf("WorktreeCreate: %v", err)
	}

	wantArgv := "worktree create --cwd /var/tmp/repo --branch probe/x --no-focus"
	if got := readArgvLog(t, argvLog); got != wantArgv {
		t.Errorf("argv = %q, want %q", got, wantArgv)
	}
}

func TestCLIRunnerWorktreeCreateNonZeroExit(t *testing.T) {
	bin := fakeHerdrFail(t, "fatal: not a git repository")
	r := &CLIRunner{Bin: bin}

	_, err := r.WorktreeCreate(context.Background(), WorktreeCreateReq{Cwd: "/nope"})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "fatal: not a git repository") {
		t.Errorf("error %q does not contain stderr content", err.Error())
	}
}

func TestCLIRunnerWorkspaceCreate(t *testing.T) {
	stdout := readFixture(t, filepath.Join("testdata", "live", "workspace_create.json"))
	bin, argvLog := fakeHerdr(t, stdout)
	r := &CLIRunner{Bin: bin}

	req := WorkspaceCreateReq{Cwd: "/tmp", Label: "probe", Focus: false}
	topo, err := r.WorkspaceCreate(context.Background(), req)
	if err != nil {
		t.Fatalf("WorkspaceCreate: %v", err)
	}

	wantArgv := "workspace create --cwd /tmp --label probe --no-focus"
	if got := readArgvLog(t, argvLog); got != wantArgv {
		t.Errorf("argv = %q, want %q", got, wantArgv)
	}

	want := CreatedTopology{WorkspaceID: "w4", TabID: "w4:t1", PaneID: "w4:p1"}
	if topo != want {
		t.Errorf("topo = %+v, want %+v", topo, want)
	}
}

func TestCLIRunnerTabCreate(t *testing.T) {
	stdout := readFixture(t, filepath.Join("testdata", "live", "tab_create.json"))
	bin, argvLog := fakeHerdr(t, stdout)
	r := &CLIRunner{Bin: bin}

	req := TabCreateReq{Workspace: "w4", Cwd: "/tmp", Focus: false}
	topo, err := r.TabCreate(context.Background(), req)
	if err != nil {
		t.Fatalf("TabCreate: %v", err)
	}

	wantArgv := "tab create --workspace w4 --cwd /tmp --no-focus"
	if got := readArgvLog(t, argvLog); got != wantArgv {
		t.Errorf("argv = %q, want %q", got, wantArgv)
	}

	// tab_create.json's response has no top-level "workspace" object --
	// workspace id must come from root_pane instead.
	want := CreatedTopology{WorkspaceID: "w4", TabID: "w4:t2", PaneID: "w4:p2"}
	if topo != want {
		t.Errorf("topo = %+v, want %+v", topo, want)
	}
}

func TestCLIRunnerPaneSplit(t *testing.T) {
	stdout := readFixture(t, filepath.Join("testdata", "live", "pane_split.json"))
	bin, argvLog := fakeHerdr(t, stdout)
	r := &CLIRunner{Bin: bin}

	req := PaneSplitReq{PaneID: "w4:p2", Direction: "right", Focus: false}
	topo, err := r.PaneSplit(context.Background(), req)
	if err != nil {
		t.Fatalf("PaneSplit: %v", err)
	}

	wantArgv := "pane split --pane w4:p2 --direction right --no-focus"
	if got := readArgvLog(t, argvLog); got != wantArgv {
		t.Errorf("argv = %q, want %q", got, wantArgv)
	}

	want := CreatedTopology{WorkspaceID: "w4", TabID: "w4:t2", PaneID: "w4:p3"}
	if topo != want {
		t.Errorf("topo = %+v, want %+v", topo, want)
	}
}

func TestCLIRunnerWorkspaceList(t *testing.T) {
	// No live fixture was captured for `workspace list`; this canned
	// response is hand-built from the confirmed WorkspaceInfo schema
	// (/home/zvi/Projects/herdr/src/api/schema/workspaces.rs:59), not
	// live-captured.
	stdout := `{"id":"cli:workspace:list","result":{"type":"workspace_list","workspaces":[` +
		`{"workspace_id":"w1","number":1,"label":"main","focused":true,"pane_count":1,"tab_count":1,"active_tab_id":"w1:t1","agent_status":"idle"},` +
		`{"workspace_id":"w2","number":2,"label":"probe","focused":false,"pane_count":2,"tab_count":1,"active_tab_id":"w2:t1","agent_status":"unknown","worktree":{"repo_key":"/r/.git","repo_name":"r","repo_root":"/r","checkout_path":"/r","is_linked_worktree":false}}` +
		`]}}`
	bin, argvLog := fakeHerdr(t, stdout)
	r := &CLIRunner{Bin: bin}

	got, err := r.WorkspaceList(context.Background())
	if err != nil {
		t.Fatalf("WorkspaceList: %v", err)
	}

	wantArgv := "workspace list"
	if got := readArgvLog(t, argvLog); got != wantArgv {
		t.Errorf("argv = %q, want %q", got, wantArgv)
	}

	if len(got) != 2 {
		t.Fatalf("len(workspaces) = %d, want 2", len(got))
	}
	if got[0].WorkspaceID != "w1" || !got[0].Focused {
		t.Errorf("workspaces[0] = %+v", got[0])
	}
	if got[1].Worktree == nil || got[1].Worktree.RepoRoot != "/r" {
		t.Errorf("workspaces[1].Worktree = %+v", got[1].Worktree)
	}
}

func TestCLIRunnerAgentStart(t *testing.T) {
	stdout := `{"id":"cli:agent:start","result":{"type":"agent_started","agent":{"agent":"claude"},"argv":["claude"]}}`
	bin, argvLog := fakeHerdr(t, stdout)
	r := &CLIRunner{Bin: bin}

	req := AgentStartReq{Name: "probe", Kind: "claude", PaneID: "w1:p1"}
	if err := r.AgentStart(context.Background(), req); err != nil {
		t.Fatalf("AgentStart: %v", err)
	}

	wantArgv := "agent start probe --kind claude --pane w1:p1"
	if got := readArgvLog(t, argvLog); got != wantArgv {
		t.Errorf("argv = %q, want %q", got, wantArgv)
	}
}

func TestCLIRunnerAgentStartWithExtraArgs(t *testing.T) {
	stdout := `{"id":"cli:agent:start","result":{"type":"agent_started","agent":{"agent":"claude"},"argv":["claude"]}}`
	bin, argvLog := fakeHerdr(t, stdout)
	r := &CLIRunner{Bin: bin}

	req := AgentStartReq{Name: "probe", Kind: "claude", PaneID: "w1:p1", ExtraArgs: []string{"--resume"}}
	if err := r.AgentStart(context.Background(), req); err != nil {
		t.Fatalf("AgentStart: %v", err)
	}

	wantArgv := "agent start probe --kind claude --pane w1:p1 -- --resume"
	if got := readArgvLog(t, argvLog); got != wantArgv {
		t.Errorf("argv = %q, want %q", got, wantArgv)
	}
}

func TestCLIRunnerAgentPromptWait(t *testing.T) {
	stdout := `{"id":"cli:agent:prompt","result":{"type":"agent_prompted","agent":{"agent":"claude"}}}`
	bin, argvLog := fakeHerdr(t, stdout)
	r := &CLIRunner{Bin: bin}

	req := AgentPromptReq{Target: "w1:p1", Text: "hello", WaitTimeout: 5 * time.Second}
	if err := r.AgentPrompt(context.Background(), req); err != nil {
		t.Fatalf("AgentPrompt: %v", err)
	}

	wantArgv := "agent prompt w1:p1 hello --wait --timeout 5000"
	if got := readArgvLog(t, argvLog); got != wantArgv {
		t.Errorf("argv = %q, want %q", got, wantArgv)
	}
}

func TestCLIRunnerAgentPromptNonZeroExit(t *testing.T) {
	bin := fakeHerdrFail(t, "agent_blocked")
	r := &CLIRunner{Bin: bin}

	err := r.AgentPrompt(context.Background(), AgentPromptReq{Target: "w1:p1", Text: "hi"})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "agent_blocked") {
		t.Errorf("error %q does not contain stderr content", err.Error())
	}
}

// TestCLIRunnerAgentRead exercises AgentRead's runText path -- unlike
// runJSON-backed methods, AgentRead's stdout IS the payload (plain text,
// no envelope), so this test's fixture is deliberately non-JSON text, to
// prove runText doesn't try to parse it as JSON the way runJSON would.
func TestCLIRunnerAgentRead(t *testing.T) {
	screen := "some pane text\nsecond line"
	bin, argvLog := fakeHerdr(t, screen)
	r := &CLIRunner{Bin: bin}

	got, err := r.AgentRead(context.Background(), "w1:p1")
	if err != nil {
		t.Fatalf("AgentRead: %v", err)
	}
	if !strings.Contains(got, screen) {
		t.Errorf("AgentRead text = %q, want it to contain %q", got, screen)
	}

	wantArgv := "agent read w1:p1 --source detection --format text"
	if gotArgv := readArgvLog(t, argvLog); gotArgv != wantArgv {
		t.Errorf("argv = %q, want %q", gotArgv, wantArgv)
	}
}

func TestCLIRunnerAgentReadNonZeroExit(t *testing.T) {
	bin := fakeHerdrFail(t, "agent target w1:p1 not found")
	r := &CLIRunner{Bin: bin}

	_, err := r.AgentRead(context.Background(), "w1:p1")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "agent target w1:p1 not found") {
		t.Errorf("error %q does not contain stderr content", err.Error())
	}
}

func TestCLIRunnerWorktreeRemove(t *testing.T) {
	stdout := `{"id":"cli:worktree:remove","result":{"type":"worktree_removed","workspace_id":"w3","path":"/x","forced":false}}`
	bin, argvLog := fakeHerdr(t, stdout)
	r := &CLIRunner{Bin: bin}

	if err := r.WorktreeRemove(context.Background(), "w3"); err != nil {
		t.Fatalf("WorktreeRemove: %v", err)
	}

	wantArgv := "worktree remove --workspace w3"
	if got := readArgvLog(t, argvLog); got != wantArgv {
		t.Errorf("argv = %q, want %q", got, wantArgv)
	}
}

func TestCLIRunnerWorkspaceClose(t *testing.T) {
	stdout := `{"id":"cli:workspace:close","result":{"type":"workspace_closed"}}`
	bin, argvLog := fakeHerdr(t, stdout)
	r := &CLIRunner{Bin: bin}

	if err := r.WorkspaceClose(context.Background(), "w4"); err != nil {
		t.Fatalf("WorkspaceClose: %v", err)
	}

	wantArgv := "workspace close w4"
	if got := readArgvLog(t, argvLog); got != wantArgv {
		t.Errorf("argv = %q, want %q", got, wantArgv)
	}
}

func TestCLIRunnerAwaitDetectionRetriesUntilDetected(t *testing.T) {
	stdout := `{"id":"cli:agent:get","result":{"type":"agent","agent":{"agent":"claude","status":"working"}}}`
	bin, argvLog := fakeHerdrFlaky(t, 2, stdout)
	r := &CLIRunner{Bin: bin, PollInterval: 5 * time.Millisecond}

	if err := r.AwaitDetection(context.Background(), "w1:p2", time.Second); err != nil {
		t.Fatalf("AwaitDetection: %v", err)
	}

	log := readArgvLog(t, argvLog)
	calls := strings.Count(log, "\n") + 1
	if calls < 3 {
		t.Errorf("calls = %d, want >= 3 (fails twice, then succeeds)", calls)
	}
	for _, line := range strings.Split(log, "\n") {
		if line != "agent get w1:p2" {
			t.Errorf("argv line = %q, want %q", line, "agent get w1:p2")
		}
	}
}

func TestCLIRunnerAwaitDetectionTimeout(t *testing.T) {
	bin := fakeHerdrFail(t, "not found")
	r := &CLIRunner{Bin: bin, PollInterval: 5 * time.Millisecond}

	start := time.Now()
	err := r.AwaitDetection(context.Background(), "w1:p2", 50*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "w1:p2") {
		t.Errorf("error %q does not name the pane", err.Error())
	}
	if elapsed < 50*time.Millisecond {
		t.Errorf("AwaitDetection returned after %v, before its 50ms timeout elapsed", elapsed)
	}
}

func TestCLIRunnerAwaitDetectionTimeoutKillsHangingPoll(t *testing.T) {
	// The fake herdr sleeps far longer than the timeout; AwaitDetection must
	// not wait for it to finish -- it must kill the poll at the deadline and
	// return promptly, not after the poll's own 5s sleep.
	bin := fakeHerdrHang(t, 5)
	r := &CLIRunner{Bin: bin, PollInterval: 5 * time.Millisecond}

	start := time.Now()
	err := r.AwaitDetection(context.Background(), "w1:p2", 50*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
	if elapsed >= 500*time.Millisecond {
		t.Errorf("AwaitDetection took %v to time out against a 50ms deadline and a hanging poll; the poll was not killed promptly", elapsed)
	}
}

// TestCLIRunnerPaneRun models the real `herdr pane run` contract
// (send_ok_request, herdr:src/cli.rs): success is reported by exit code
// alone, with NOTHING printed on stdout. This replaced a fixture that
// echoed a canned JSON envelope `pane run` never actually prints -- task
// 19's live checkpoint found that gap let a PaneRun which unconditionally
// failed against the real CLI still pass this test.
func TestCLIRunnerPaneRun(t *testing.T) {
	bin, argvLog := fakeHerdrOK(t)
	r := &CLIRunner{Bin: bin}

	err := r.PaneRun(context.Background(), "w1:p2", []string{"clauth", "start", "alpha", "--"})
	if err != nil {
		t.Fatalf("PaneRun: %v", err)
	}

	wantArgv := "pane run w1:p2 clauth start alpha --"
	if got := readArgvLog(t, argvLog); got != wantArgv {
		t.Errorf("argv = %q, want %q", got, wantArgv)
	}
}

// TestCLIRunnerPaneRunToleratesNonEmptyStdout is a defensive companion to
// TestCLIRunnerPaneRun: PaneRun's contract (via runOK) is exit-code-only,
// so it must not start failing if some future herdr version's `pane run`
// happens to print something (JSON or otherwise) on success -- runOK never
// inspects stdout at all, unlike runJSON.
func TestCLIRunnerPaneRunToleratesNonEmptyStdout(t *testing.T) {
	stdout := `{"id":"cli:pane:run","result":{"type":"pane_run"}}`
	bin, _ := fakeHerdr(t, stdout)
	r := &CLIRunner{Bin: bin}

	if err := r.PaneRun(context.Background(), "w1:p2", []string{"echo", "hi"}); err != nil {
		t.Fatalf("PaneRun: %v", err)
	}
}

func TestCLIRunnerPaneRunNonZeroExit(t *testing.T) {
	bin := fakeHerdrFail(t, "no such pane w1:p2")
	r := &CLIRunner{Bin: bin}

	err := r.PaneRun(context.Background(), "w1:p2", []string{"echo", "hi"})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "no such pane w1:p2") {
		t.Errorf("error %q does not contain stderr content", err.Error())
	}
}

// assertNeverExecuted fails unless the fake herdr was never run at all.
// Every fake in this file creates its argv log on its first invocation and
// only then, so the file's absence is direct evidence that no process was
// spawned -- the property a refusal must have, and the one thing that
// distinguishes it from an invocation that ran and failed.
func assertNeverExecuted(t *testing.T, argvLog string) {
	t.Helper()
	_, err := os.Stat(argvLog)
	if err == nil {
		t.Fatalf("herdr was executed (argv %q); a refused flag value must never reach the CLI", readArgvLog(t, argvLog))
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat argv log: %v", err)
	}
}

// TestCLIRunnerRefusesFlagValueReadAsAnotherFlag covers every `--flag
// value` pair CLIRunner builds from a variable, one row each: a value
// beginning with "-" must be refused before anything is executed, because
// herdr's parser would read it as another flag (and herdr has no
// `--flag=value` form to disambiguate it with -- see appendFlag).
//
// Not in the table, for lack of a way to reach them: AgentPrompt's
// --timeout (its `WaitTimeout > 0` guard already excludes every value that
// could render with a leading "-") and AgentRead's --source/--format
// (string literals). TestAppendFlag covers the shared funnel those go
// through.
func TestCLIRunnerRefusesFlagValueReadAsAnotherFlag(t *testing.T) {
	tests := []struct {
		name  string
		flag  string
		value string
		run   func(r *CLIRunner, value string) error
	}{
		{
			name: "worktree create --cwd", flag: "--cwd", value: "-/var/tmp/repo",
			run: func(r *CLIRunner, v string) error {
				_, err := r.WorktreeCreate(context.Background(), WorktreeCreateReq{Cwd: v, Branch: "probe/x"})
				return err
			},
		},
		{
			name: "worktree create --branch", flag: "--branch", value: "--force",
			run: func(r *CLIRunner, v string) error {
				_, err := r.WorktreeCreate(context.Background(), WorktreeCreateReq{Cwd: "/var/tmp/repo", Branch: v})
				return err
			},
		},
		{
			name: "worktree create --base", flag: "--base", value: "-main",
			run: func(r *CLIRunner, v string) error {
				_, err := r.WorktreeCreate(context.Background(), WorktreeCreateReq{Cwd: "/var/tmp/repo", Branch: "probe/x", Base: v})
				return err
			},
		},
		{
			// The label is the session title, which can be seeded from a
			// Linear issue title -- the one value in this table that can
			// originate outside the machine.
			name: "worktree create --label", flag: "--label", value: "--focus",
			run: func(r *CLIRunner, v string) error {
				_, err := r.WorktreeCreate(context.Background(), WorktreeCreateReq{Cwd: "/var/tmp/repo", Branch: "probe/x", Label: v})
				return err
			},
		},
		{
			name: "workspace create --cwd", flag: "--cwd", value: "-/var/tmp/repo",
			run: func(r *CLIRunner, v string) error {
				_, err := r.WorkspaceCreate(context.Background(), WorkspaceCreateReq{Cwd: v})
				return err
			},
		},
		{
			name: "workspace create --label", flag: "--label", value: "--focus",
			run: func(r *CLIRunner, v string) error {
				_, err := r.WorkspaceCreate(context.Background(), WorkspaceCreateReq{Cwd: "/var/tmp/repo", Label: v})
				return err
			},
		},
		{
			name: "tab create --workspace", flag: "--workspace", value: "-w4",
			run: func(r *CLIRunner, v string) error {
				_, err := r.TabCreate(context.Background(), TabCreateReq{Workspace: v})
				return err
			},
		},
		{
			name: "tab create --cwd", flag: "--cwd", value: "-/var/tmp/repo",
			run: func(r *CLIRunner, v string) error {
				_, err := r.TabCreate(context.Background(), TabCreateReq{Workspace: "w4", Cwd: v})
				return err
			},
		},
		{
			name: "tab create --label", flag: "--label", value: "--focus",
			run: func(r *CLIRunner, v string) error {
				_, err := r.TabCreate(context.Background(), TabCreateReq{Workspace: "w4", Label: v})
				return err
			},
		},
		{
			name: "pane split --pane", flag: "--pane", value: "-w4:p2",
			run: func(r *CLIRunner, v string) error {
				_, err := r.PaneSplit(context.Background(), PaneSplitReq{PaneID: v, Direction: "right"})
				return err
			},
		},
		{
			name: "pane split --direction", flag: "--direction", value: "-right",
			run: func(r *CLIRunner, v string) error {
				_, err := r.PaneSplit(context.Background(), PaneSplitReq{PaneID: "w4:p2", Direction: v})
				return err
			},
		},
		{
			name: "pane split --cwd", flag: "--cwd", value: "-/var/tmp/repo",
			run: func(r *CLIRunner, v string) error {
				_, err := r.PaneSplit(context.Background(), PaneSplitReq{PaneID: "w4:p2", Direction: "right", Cwd: v})
				return err
			},
		},
		{
			name: "agent start --kind", flag: "--kind", value: "-claude",
			run: func(r *CLIRunner, v string) error {
				return r.AgentStart(context.Background(), AgentStartReq{Name: "probe", Kind: v, PaneID: "w1:p1"})
			},
		},
		{
			name: "agent start --pane", flag: "--pane", value: "-w1:p1",
			run: func(r *CLIRunner, v string) error {
				return r.AgentStart(context.Background(), AgentStartReq{Name: "probe", Kind: "claude", PaneID: v})
			},
		},
		{
			name: "worktree remove --workspace", flag: "--workspace", value: "-w3",
			run: func(r *CLIRunner, v string) error {
				return r.WorktreeRemove(context.Background(), v)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bin, argvLog := fakeHerdr(t, `{"id":"x","result":{}}`)
			r := &CLIRunner{Bin: bin}

			err := tt.run(r, tt.value)
			if err == nil {
				t.Fatalf("%s = nil, want a refusal for value %q", tt.name, tt.value)
			}
			if !errors.Is(err, errRefused) {
				t.Errorf("error %q is not a refusal to run (errors.Is errRefused = false); it must be distinguishable from a herdr-side failure", err)
			}
			if !strings.Contains(err.Error(), tt.flag) {
				t.Errorf("error %q does not name the flag %s", err, tt.flag)
			}
			if !strings.Contains(err.Error(), tt.value) {
				t.Errorf("error %q does not name the offending value %s", err, tt.value)
			}
			assertNeverExecuted(t, argvLog)
		})
	}
}

// TestCLIRunnerWorktreeRemoveRefusesEmptyWorkspace guards the one place
// where appendFlag's "an empty value omits the flag" rule would change what
// herdr acts on rather than making it fail: `worktree remove`'s --workspace
// is optional to herdr's own parser, so a bare `herdr worktree remove` is a
// destructive command against a default target, not a usage error.
func TestCLIRunnerWorktreeRemoveRefusesEmptyWorkspace(t *testing.T) {
	bin, argvLog := fakeHerdr(t, `{"id":"x","result":{}}`)
	r := &CLIRunner{Bin: bin}

	err := r.WorktreeRemove(context.Background(), "")
	if err == nil {
		t.Fatal("WorktreeRemove(\"\") = nil, want a refusal")
	}
	if !errors.Is(err, errRefused) {
		t.Errorf("error %q is not a refusal to run (errors.Is errRefused = false)", err)
	}
	assertNeverExecuted(t, argvLog)
}

// TestCLIRunnerHyphenatedValuesAreUnaffected is the other half of the
// refusal: only a value that *begins* with "-" is pathological. Branch
// names, base refs and labels that merely contain hyphens are the normal
// case and must reach herdr byte for byte.
func TestCLIRunnerHyphenatedValuesAreUnaffected(t *testing.T) {
	stdout := readFixture(t, filepath.Join("testdata", "live", "worktree_create.json"))
	bin, argvLog := fakeHerdr(t, stdout)
	r := &CLIRunner{Bin: bin}

	req := WorktreeCreateReq{
		Cwd:    "/var/tmp/my-throwaway-repo",
		Branch: "zvi/fix-login-redirect-loop",
		Base:   "release/1.4-rc",
		Label:  "fix-login-redirect-loop",
		Focus:  true,
	}
	if _, err := r.WorktreeCreate(context.Background(), req); err != nil {
		t.Fatalf("WorktreeCreate: %v", err)
	}

	wantArgv := "worktree create --cwd /var/tmp/my-throwaway-repo --branch zvi/fix-login-redirect-loop --base release/1.4-rc --label fix-login-redirect-loop --focus"
	if got := readArgvLog(t, argvLog); got != wantArgv {
		t.Errorf("argv = %q, want %q", got, wantArgv)
	}
}

// TestCLIRunnerEmptyFlagValueOmitsFlag pins the behavior appendFlag
// inherited from the `if req.X != ""` guards it replaced: an unset optional
// field drops its flag entirely rather than sending an empty argv element.
func TestCLIRunnerEmptyFlagValueOmitsFlag(t *testing.T) {
	t.Run("worktree create", func(t *testing.T) {
		stdout := readFixture(t, filepath.Join("testdata", "live", "worktree_create.json"))
		bin, argvLog := fakeHerdr(t, stdout)
		r := &CLIRunner{Bin: bin}

		req := WorktreeCreateReq{Cwd: "/var/tmp/repo", Branch: "probe/x", Base: "", Label: ""}
		if _, err := r.WorktreeCreate(context.Background(), req); err != nil {
			t.Fatalf("WorktreeCreate: %v", err)
		}

		wantArgv := "worktree create --cwd /var/tmp/repo --branch probe/x --no-focus"
		if got := readArgvLog(t, argvLog); got != wantArgv {
			t.Errorf("argv = %q, want %q", got, wantArgv)
		}
	})

	t.Run("tab create", func(t *testing.T) {
		stdout := readFixture(t, filepath.Join("testdata", "live", "tab_create.json"))
		bin, argvLog := fakeHerdr(t, stdout)
		r := &CLIRunner{Bin: bin}

		if _, err := r.TabCreate(context.Background(), TabCreateReq{Cwd: "/tmp"}); err != nil {
			t.Fatalf("TabCreate: %v", err)
		}

		wantArgv := "tab create --cwd /tmp --no-focus"
		if got := readArgvLog(t, argvLog); got != wantArgv {
			t.Errorf("argv = %q, want %q", got, wantArgv)
		}
	})
}

// TestAppendFlag exercises the funnel itself, including the two flags no
// CLIRunner method can route a hostile value to (--timeout, --source).
func TestAppendFlag(t *testing.T) {
	base := []string{"worktree", "create"}

	t.Run("ordinary value is appended", func(t *testing.T) {
		got, err := appendFlag(base, "--branch", "zvi/fix-login-redirect-loop")
		if err != nil {
			t.Fatalf("appendFlag: %v", err)
		}
		want := "worktree create --branch zvi/fix-login-redirect-loop"
		if joined := strings.Join(got, " "); joined != want {
			t.Errorf("args = %q, want %q", joined, want)
		}
	})

	t.Run("interior hyphens are fine", func(t *testing.T) {
		for _, v := range []string{"fix-login-redirect-loop", "release/1.4-rc", "a-", "x-y"} {
			if _, err := appendFlag(base, "--base", v); err != nil {
				t.Errorf("appendFlag(%q) = %v, want nil", v, err)
			}
		}
	})

	t.Run("empty value omits the flag", func(t *testing.T) {
		got, err := appendFlag(base, "--label", "")
		if err != nil {
			t.Fatalf("appendFlag: %v", err)
		}
		if joined := strings.Join(got, " "); joined != "worktree create" {
			t.Errorf("args = %q, want the flag omitted", joined)
		}
	})

	t.Run("leading hyphen is refused", func(t *testing.T) {
		for _, v := range []string{"-", "--", "-x", "--force", "-rf"} {
			got, err := appendFlag(base, "--label", v)
			if err == nil {
				t.Errorf("appendFlag(%q) = nil, want a refusal", v)
				continue
			}
			if !errors.Is(err, errRefused) {
				t.Errorf("appendFlag(%q) error %q is not a refusal to run", v, err)
			}
			if !strings.Contains(err.Error(), "--label") || !strings.Contains(err.Error(), v) {
				t.Errorf("appendFlag(%q) error %q must name both the flag and the value", v, err)
			}
			if got != nil {
				// A caller that ignored the error must not be handed a
				// usable-looking argv with the flag quietly dropped.
				t.Errorf("appendFlag(%q) args = %q, want nil", v, got)
			}
		}
	})

	t.Run("base args are not mutated", func(t *testing.T) {
		if _, err := appendFlag(base, "--branch", "x"); err != nil {
			t.Fatalf("appendFlag: %v", err)
		}
		if joined := strings.Join(base, " "); joined != "worktree create" {
			t.Errorf("base args = %q, want them untouched", joined)
		}
	})
}

func TestCLIRunnerDefaultPollInterval(t *testing.T) {
	r := &CLIRunner{Bin: "herdr"}
	if got := r.pollInterval(); got != defaultPollInterval {
		t.Errorf("pollInterval() = %v, want %v", got, defaultPollInterval)
	}

	r.PollInterval = 250 * time.Millisecond
	if got := r.pollInterval(); got != 250*time.Millisecond {
		t.Errorf("pollInterval() = %v, want 250ms", got)
	}
}
