package herdrc

import (
	"context"
	"errors"
	"fmt"
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

// TestCLIRunnerWorktreeCreateTrustRepository pins the ONE argv difference
// `[worktree] trust_repository` makes. The flag is positional-last and
// takes no value, so a regression here is silent: herdr would simply
// create the worktree without trusting the repository, and the failure
// surfaces later as a git "dubious ownership" error from a subprocess
// nobody is watching.
func TestCLIRunnerWorktreeCreateTrustRepository(t *testing.T) {
	stdout := readFixture(t, filepath.Join("testdata", "live", "worktree_create.json"))
	bin, argvLog := fakeHerdr(t, stdout)
	r := &CLIRunner{Bin: bin}

	req := WorktreeCreateReq{Cwd: "/var/tmp/repo", Branch: "probe/x", Focus: true, TrustRepository: true}
	if _, err := r.WorktreeCreate(context.Background(), req); err != nil {
		t.Fatalf("WorktreeCreate: %v", err)
	}

	wantArgv := "worktree create --cwd /var/tmp/repo --branch probe/x --focus --trust-repository"
	if got := readArgvLog(t, argvLog); got != wantArgv {
		t.Errorf("argv = %q, want %q", got, wantArgv)
	}
}

// TestCLIRunnerWorktreeCreateNoTrustRepositoryByDefault is the other half,
// and the one that matters for anyone who never set the key: the flag must
// be ABSENT, not present-and-false. herdr 0.9.0 takes `--trust-repository`
// as a bare switch, so there is no false to pass -- sending it always
// would silently opt every user into relaxing a git safety check.
func TestCLIRunnerWorktreeCreateNoTrustRepositoryByDefault(t *testing.T) {
	stdout := readFixture(t, filepath.Join("testdata", "live", "worktree_create.json"))
	bin, argvLog := fakeHerdr(t, stdout)
	r := &CLIRunner{Bin: bin}

	req := WorktreeCreateReq{Cwd: "/var/tmp/repo", Branch: "probe/x", Focus: true}
	if _, err := r.WorktreeCreate(context.Background(), req); err != nil {
		t.Fatalf("WorktreeCreate: %v", err)
	}

	if got := readArgvLog(t, argvLog); strings.Contains(got, "--trust-repository") {
		t.Errorf("argv = %q, want no --trust-repository", got)
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
	// (https://github.com/herdrdev/herdr/blob/b1ff4582/src/api/schema/workspaces.rs#L59),
	// not
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

func TestCLIRunnerPaneClose(t *testing.T) {
	stdout := `{"id":"cli:pane:close","result":{"type":"ok"}}`
	bin, argvLog := fakeHerdr(t, stdout)
	r := &CLIRunner{Bin: bin}

	if err := r.PaneClose(context.Background(), "p3"); err != nil {
		t.Fatalf("PaneClose: %v", err)
	}

	wantArgv := "pane close p3"
	if got := readArgvLog(t, argvLog); got != wantArgv {
		t.Errorf("argv = %q, want %q", got, wantArgv)
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
	// The shape here is `herdr agent get`'s real one, captured live from
	// 0.9.0 on 2026-09-08. The fixture this replaces said
	// `{"agent":"claude","status":"working"}` -- a key herdr does not emit
	// (it is `agent_status`), carrying a value that would not mean ready
	// even if it did. It passed only because AwaitDetection used to ignore
	// stdout completely and read the exit code alone, which is the bug #94
	// is about: a fake with the wrong contract cannot fail the way the real
	// CLI does.
	stdout := agentGetJSON(`"agent_status":"idle","interactive_ready":true`)
	bin, argvLog := fakeHerdrFlaky(t, 2, stdout)
	r := &CLIRunner{Bin: bin, PollInterval: 5 * time.Millisecond}

	if err := r.AwaitDetection(context.Background(), "w1:p2", time.Second, 0); err != nil {
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
	err := r.AwaitDetection(context.Background(), "w1:p2", 50*time.Millisecond, 0)
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
	err := r.AwaitDetection(context.Background(), "w1:p2", 50*time.Millisecond, 0)
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

	err := r.PaneRun(context.Background(), "w1:p2", nil, []string{"clauth", "start", "alpha", "--"})
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

	if err := r.PaneRun(context.Background(), "w1:p2", nil, []string{"echo", "hi"}); err != nil {
		t.Fatalf("PaneRun: %v", err)
	}
}

// TestCLIRunnerPaneRunQuotesForTheShell is #72. `herdr pane run` does not
// exec an argv vector: it joins args[1..] with single spaces and TYPES the
// result into the pane's interactive shell, then sends Enter
// (https://github.com/herdrdev/herdr/blob/b1ff4582/src/cli/pane.rs#L1047).
// So every element is re-parsed by that shell, and an element carrying a
// glob, a space or a quote no longer means what the caller said.
//
// Found live: an `[agents.extra_args]` model id containing brackets became
// `zsh: no matches found: claude-opus-5[1m]`, the agent never started, and
// `pane run` still reported success because its exit code covers the typing
// (runOK). PaneRun therefore quotes, so a caller can hand it a plain argv
// and mean it -- and so that the same argv can go to AgentStart unaltered,
// which is the half the old config-side workaround could not satisfy at the
// same time.
func TestCLIRunnerPaneRunQuotesForTheShell(t *testing.T) {
	bin, argvLog := fakeHerdrOK(t)
	r := &CLIRunner{Bin: bin}

	argv := []string{"clauth", "start", "personal", "--", "--model", "claude-opus-5[1m]", "--effort", "xhigh"}
	if err := r.PaneRun(context.Background(), "w9:p1", nil, argv); err != nil {
		t.Fatalf("PaneRun: %v", err)
	}

	// What the pane's shell will see, which is what this test is really
	// about: herdr space-joins whatever we pass, so the logged argv line is
	// the command line verbatim.
	wantArgv := "pane run w9:p1 clauth start personal -- --model 'claude-opus-5[1m]' --effort xhigh"
	if got := readArgvLog(t, argvLog); got != wantArgv {
		t.Errorf("argv  = %q\nwant   = %q", got, wantArgv)
	}
}

// TestCLIRunnerPaneRunQuotesSpacesAndQuotes covers the two metacharacters
// the glob case does not: an element containing a space must stay ONE word,
// and an element containing a single quote must not end the quoting early.
func TestCLIRunnerPaneRunQuotesSpacesAndQuotes(t *testing.T) {
	bin, argvLog := fakeHerdrOK(t)
	r := &CLIRunner{Bin: bin}

	argv := []string{"clauth", "start", "p", "--", "--append-system-prompt", "be terse", "--tag", "it's"}
	if err := r.PaneRun(context.Background(), "w9:p1", nil, argv); err != nil {
		t.Fatalf("PaneRun: %v", err)
	}

	wantArgv := `pane run w9:p1 clauth start p -- --append-system-prompt 'be terse' --tag 'it'\''s'`
	if got := readArgvLog(t, argvLog); got != wantArgv {
		t.Errorf("argv  = %q\nwant   = %q", got, wantArgv)
	}
}

// TestCLIRunnerAgentStartPassesExtraArgsRaw is the other half of #72, and
// the reason the quoting lives in PaneRun rather than in the value the
// caller supplies: `herdr agent start` hands its post-`--` arguments to the
// agent as an argv VECTOR, with no shell anywhere, so the same value must
// arrive unquoted here. Quoting it -- which is what the retired
// config.toml workaround did -- made claude receive a model name that
// literally began with an apostrophe.
func TestCLIRunnerAgentStartPassesExtraArgsRaw(t *testing.T) {
	stdout := `{"id":"cli:agent:start","result":{"type":"agent_started","agent":{"agent":"claude"},"argv":["claude"]}}`
	bin, argvLog := fakeHerdr(t, stdout)
	r := &CLIRunner{Bin: bin}

	req := AgentStartReq{
		Name: "probe", Kind: "claude", PaneID: "w1:p1",
		ExtraArgs: []string{"--model", "claude-opus-5[1m]", "--effort", "xhigh"},
	}
	if err := r.AgentStart(context.Background(), req); err != nil {
		t.Fatalf("AgentStart: %v", err)
	}

	wantArgv := "agent start probe --kind claude --pane w1:p1 -- --model claude-opus-5[1m] --effort xhigh"
	if got := readArgvLog(t, argvLog); got != wantArgv {
		t.Errorf("argv  = %q\nwant   = %q", got, wantArgv)
	}
}

// TestShellQuote pins the quoting rule itself: leave alone what a POSIX
// shell cannot misread, and wrap everything else in single quotes, closing
// and reopening around an embedded one. The safe set is deliberately
// conservative -- it is cheaper to over-quote a rare character than to
// reason about every shell's expansions -- but it does cover the ordinary
// flag/value shapes, so the command a user sees typed into their own pane
// stays readable.
func TestShellQuote(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "''"},
		{"clauth", "clauth"},
		{"--effort", "--effort"},
		{"a_b.c-d/e:f@g%h+i,j=k", "a_b.c-d/e:f@g%h+i,j=k"},
		{"claude-opus-5[1m]", "'claude-opus-5[1m]'"},
		{"two words", "'two words'"},
		{"it's", `'it'\''s'`},
		{"$HOME", "'$HOME'"},
		{"a;b", "'a;b'"},
		{"*", "'*'"},
		{"~/x", "'~/x'"},
	}
	for _, c := range cases {
		if got := shellQuote(c.in); got != c.want {
			t.Errorf("shellQuote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCLIRunnerPaneRunNonZeroExit(t *testing.T) {
	bin := fakeHerdrFail(t, "no such pane w1:p2")
	r := &CLIRunner{Bin: bin}

	err := r.PaneRun(context.Background(), "w1:p2", nil, []string{"echo", "hi"})
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

// TestCLIRunnerPaneCloseRefusesEmptyPaneID mirrors
// TestCLIRunnerWorktreeRemoveRefusesEmptyWorkspace: a caller holding no pane
// id must not silently close whatever pane herdr would pick by default.
func TestCLIRunnerPaneCloseRefusesEmptyPaneID(t *testing.T) {
	bin, argvLog := fakeHerdr(t, `{"id":"x","result":{}}`)
	r := &CLIRunner{Bin: bin}

	err := r.PaneClose(context.Background(), "")
	if err == nil {
		t.Fatal("PaneClose(\"\") = nil, want a refusal")
	}
	if !errors.Is(err, errRefused) {
		t.Errorf("error %q is not a refusal to run (errors.Is errRefused = false)", err)
	}
	assertNeverExecuted(t, argvLog)
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

// --- #94: detection must mean READY, not merely present -------------------

// agentGetJSON wraps agent fields in `herdr agent get`'s real envelope. The
// surrounding shape (and the field names inside) are verbatim from a live
// 0.9.0 capture on 2026-09-08; only the fields a test varies are passed in,
// so no test has to restate the parts that never change.
func agentGetJSON(fields string) string {
	return `{"id":"cli:agent:get","result":{"type":"agent_info","agent":{` +
		`"agent":"claude","name":"probe","pane_id":"w1:p2","workspace_id":"w1",` +
		fields + `}}}`
}

// TestClassifyAgent pins herdr's own readiness rule
// (herdr:src/cli/agent.rs:607-625), which pollDetection reproduces so that
// Path B means the same thing by "detected" as Path A does.
func TestClassifyAgent(t *testing.T) {
	for _, tc := range []struct {
		name  string
		agent agentInfo
		want  detectionState
	}{
		// The one that cost an agent its life: herdr registers Claude Code
		// before it paints its first-run screen, and "unknown" was being
		// read as "go ahead and type".
		{"unknown is not ready", agentInfo{AgentStatus: "unknown"}, detectionPending},
		{"working is not ready", agentInfo{AgentStatus: "working"}, detectionPending},
		{"blocked stops the wait", agentInfo{AgentStatus: "blocked", LaunchPending: true}, detectionBlocked},
		{"idle and interactive is ready", agentInfo{AgentStatus: "idle", InteractiveReady: true}, detectionReady},
		{"done and interactive is ready", agentInfo{AgentStatus: "done", InteractiveReady: true}, detectionReady},
		{"idle, still launching, keeps waiting", agentInfo{AgentStatus: "idle", LaunchPending: true}, detectionPending},
		// The ordinary ready state. `herdr agent get` omits BOTH flags once
		// the launch bookkeeping is done -- verified live on 0.9.0 against a
		// healthy idle claude -- so reading their absence as "never became
		// interactive" would fail every successful Path B launch. This is
		// the assertion that caught it.
		{"idle with neither flag is the ready state", agentInfo{AgentStatus: "idle"}, detectionReady},
		{"done with neither flag is the ready state", agentInfo{AgentStatus: "done"}, detectionReady},
		// A status this herdr does not have must not be promoted to ready
		// by a later release; waiting is the safe default.
		{"an unrecognised status keeps waiting", agentInfo{AgentStatus: "brand-new"}, detectionPending},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyAgent(tc.agent); got != tc.want {
				t.Errorf("classifyAgent(%+v) = %v, want %v", tc.agent, got, tc.want)
			}
		})
	}
}

// TestCLIRunnerAwaitDetectionWaitsThroughUnknown is #94 at the herdrc level:
// a detected-but-not-ready agent must not end the wait. Before this,
// `agent get` merely exiting zero was the whole test, so this poll returned
// success against the exact JSON below.
func TestCLIRunnerAwaitDetectionWaitsThroughUnknown(t *testing.T) {
	bin, _ := fakeHerdr(t, agentGetJSON(`"agent_status":"unknown","launch_pending":true`))
	r := &CLIRunner{Bin: bin, PollInterval: 5 * time.Millisecond}

	err := r.AwaitDetection(context.Background(), "w1:p2", 60*time.Millisecond, 0)
	if err == nil {
		t.Fatal("AwaitDetection returned success for an agent that never became ready")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %q, want a timeout -- unknown is not a terminal state", err.Error())
	}
}

// TestCLIRunnerAwaitDetectionReportsBlocked: an agent sitting on a dialog
// will never become ready on its own, so waiting out the full timeout tells
// the user nothing they can act on. It ends the wait immediately with a
// sentinel internal/plan turns into the instruction that answers it.
func TestCLIRunnerAwaitDetectionReportsBlocked(t *testing.T) {
	bin, _ := fakeHerdr(t, agentGetJSON(`"agent_status":"blocked","launch_pending":true`))
	r := &CLIRunner{Bin: bin, PollInterval: 5 * time.Millisecond}

	start := time.Now()
	err := r.AwaitDetection(context.Background(), "w1:p2", 10*time.Second, 0)
	elapsed := time.Since(start)

	if !errors.Is(err, ErrAgentBlocked) {
		t.Fatalf("AwaitDetection error = %v, want it to wrap ErrAgentBlocked", err)
	}
	if !strings.Contains(err.Error(), "w1:p2") {
		t.Errorf("error = %q, does not name the pane", err.Error())
	}
	if elapsed > time.Second {
		t.Errorf("took %v to report a blocked agent against a 10s timeout; it must not wait out the deadline", elapsed)
	}
}

// TestCLIRunnerAwaitDetectionAcceptsASettledAgent is the happy path this
// change nearly broke. A ready claude answers `agent get` with
// `agent_status: "idle"` and NO interactive_ready and NO launch_pending --
// both are Options on the Rust side, omitted once the launch is done. An
// earlier draft transcribed `agent start`'s rule literally and read that as
// "settled but never interactive", which would have failed every successful
// Path B launch rather than only the blocked ones.
func TestCLIRunnerAwaitDetectionAcceptsASettledAgent(t *testing.T) {
	bin, _ := fakeHerdr(t, agentGetJSON(`"agent_status":"idle"`))
	r := &CLIRunner{Bin: bin, PollInterval: 5 * time.Millisecond}

	if err := r.AwaitDetection(context.Background(), "w1:p2", time.Second, 0); err != nil {
		t.Fatalf("AwaitDetection: %v, want success for a settled idle agent", err)
	}
}

// TestCLIRunnerAwaitDetectionUnparseableResponseKeepsWaiting: a detected
// agent whose response cannot be read is not evidence of readiness. The
// timeout is the backstop, and it quotes the pane.
func TestCLIRunnerAwaitDetectionUnparseableResponseKeepsWaiting(t *testing.T) {
	bin, _ := fakeHerdr(t, "this is not json")
	r := &CLIRunner{Bin: bin, PollInterval: 5 * time.Millisecond}

	err := r.AwaitDetection(context.Background(), "w1:p2", 60*time.Millisecond, 0)
	if err == nil {
		t.Fatal("AwaitDetection returned success against an unreadable response")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %q, want a timeout", err.Error())
	}
}

// fakeHerdrFailEnvelope writes a disposable herdr that fails the way the
// REAL binary fails: a compact JSON error envelope on stderr, exit 1.
//
// fakeHerdrFail cannot do this. It interpolates its stderr straight into a
// double-quoted shell `echo`, so any `"` in the text breaks the script --
// which is why the only failure fixture in this file until now was the
// bare word `agent_blocked`, a string herdr never prints on its own. That
// was harmless while every consumer substring-matched the whole error
// text, and stopped being harmless the moment one needed the error CODE:
// a fixture that models the wrong stderr shape cannot fail the way the
// real thing does (CLAUDE.md's standing convention, hit here for the
// seventh time). The heredoc is what makes the quotes survive.
func fakeHerdrFailEnvelope(t *testing.T, code, message string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "herdr")
	envelope := fmt.Sprintf(`{"error":{"code":%q,"message":%q},"id":"cli:agent:prompt"}`, code, message)
	script := "#!/bin/sh\ncat 1>&2 <<'HERDR_EOF'\n" + envelope + "\nHERDR_EOF\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// TestCLIRunnerAgentPromptWaitTimeout pins the #108 conclusion: herdr's
// `timeout` code on `agent prompt --wait` means the WAIT gave up, not that
// the prompt failed to arrive, so it reaches callers as a typed sentinel
// they can tell apart from every other prompt failure.
func TestCLIRunnerAgentPromptWaitTimeout(t *testing.T) {
	bin := fakeHerdrFailEnvelope(t, "timeout", "timed out waiting for agent status")
	r := &CLIRunner{Bin: bin}

	err := r.AgentPrompt(context.Background(), AgentPromptReq{
		Target: "w1:p1", Text: "hi", WaitTimeout: 120 * time.Second,
	})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !errors.Is(err, ErrPromptWaitTimeout) {
		t.Errorf("error %q is not ErrPromptWaitTimeout", err.Error())
	}
	// The underlying detail has to survive: the message is what a user
	// reads, and "timed out waiting for agent status" is herdr's own.
	if !strings.Contains(err.Error(), "timed out waiting for agent status") {
		t.Errorf("error %q dropped herdr's own message", err.Error())
	}
}

// TestCLIRunnerAgentPromptTimeoutIsNotSubstringMatched is the test that
// rules out the obvious implementation of the above.
//
// AgentPrompt puts the prompt TEXT in its argv, and cmdError formats
// `herdr <joined argv>: <exit>: <stderr>` -- so the error string for ANY
// prompt failure contains both the echoed `--timeout 120000` flag and the
// user's entire prompt, verbatim. A `strings.Contains(err, "timeout")`
// classifier therefore fires on a prompt that merely mentions the word,
// and #108's own issue text is such a prompt: it quotes the envelope this
// package is matching on. Only herdr's structured stderr may decide it.
func TestCLIRunnerAgentPromptTimeoutIsNotSubstringMatched(t *testing.T) {
	// A real failure that is NOT a wait timeout, for a prompt whose text
	// discusses timeouts and even quotes the envelope.
	bin := fakeHerdrFailEnvelope(t, "agent_blocked", "agent is blocked")
	r := &CLIRunner{Bin: bin}

	err := r.AgentPrompt(context.Background(), AgentPromptReq{
		Target: "w1:p1",
		Text:   `investigate: {"error":{"code":"timeout","message":"timed out waiting for agent status"}}`,
		// A set WaitTimeout also puts `--timeout 120000` in the argv.
		WaitTimeout: 120 * time.Second,
	})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if errors.Is(err, ErrPromptWaitTimeout) {
		t.Errorf("a prompt whose TEXT mentions the timeout envelope was classified as a wait "+
			"timeout; only herdr's own stderr code may decide this\nerror: %s", err.Error())
	}
}

// fakeHerdrStaged writes a disposable herdr that answers the first n
// invocations with firstStdout and every later one with secondStdout --
// or, when secondStdout is "", fails the way `agent get` fails once there
// is no agent in the pane at all (exit 1, "not found" on stderr).
//
// It is fakeHerdrFlaky's mirror image. That one fails and then recovers,
// which is what a poll waiting for something to APPEAR needs; this one
// changes a settled answer, which is what a poll watching a state
// TRANSITION needs -- an agent that is blocked and then becomes ready, or
// blocked and then stops being reported at all (#115).
func fakeHerdrStaged(t *testing.T, n int, firstStdout, secondStdout string) (bin, argvLog string) {
	t.Helper()
	dir := t.TempDir()
	argvLog = filepath.Join(dir, "argv")
	counter := filepath.Join(dir, "count")
	bin = filepath.Join(dir, "herdr")
	second := "cat <<'EOF'\n" + secondStdout + "\nEOF\n"
	if secondStdout == "" {
		second = "echo \"not found\" 1>&2\nexit 1\n"
	}
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> " + argvLog + "\n" +
		"n=$(cat " + counter + " 2>/dev/null || echo 0)\n" +
		"n=$((n + 1))\n" +
		"echo $n > " + counter + "\n" +
		"if [ \"$n\" -le " + strconv.Itoa(n) + " ]; then\n" +
		"  cat <<'EOF'\n" + firstStdout + "\nEOF\n" +
		"  exit 0\n" +
		"fi\n" +
		second
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, argvLog
}

// blockedAgentJSON/readyAgentJSON are the two `agent get` answers the
// trust-dialog wait travels between: a claude sitting on its first-run
// trust prompt, and the same claude once the user has answered it.
func blockedAgentJSON() string { return agentGetJSON(`"agent_status":"blocked","launch_pending":true`) }
func readyAgentJSON() string   { return agentGetJSON(`"agent_status":"idle"`) }

// TestCLIRunnerAwaitDetectionWaitsThroughBlocked: given a blocked budget,
// `blocked` is a WAIT rather than a verdict (#115). The agent is alive and
// one keystroke from ready; the person at the keyboard is the thing being
// waited on, and when they answer, the poll must simply carry on and
// succeed.
func TestCLIRunnerAwaitDetectionWaitsThroughBlocked(t *testing.T) {
	bin, _ := fakeHerdrStaged(t, 3, blockedAgentJSON(), readyAgentJSON())
	r := &CLIRunner{Bin: bin, PollInterval: 5 * time.Millisecond}

	if err := r.AwaitDetection(context.Background(), "w1:p2", time.Second, 10*time.Second); err != nil {
		t.Fatalf("AwaitDetection: %v, want success once the dialog is answered", err)
	}
}

// TestCLIRunnerAwaitDetectionBlockedBudgetOutlivesTheBase: the blocked
// budget REPLACES the base one for the rest of the wait, rather than
// merely being consulted alongside it. A human reading a security prompt
// is a different order of magnitude from a machine painting a status
// transition, which is the whole reason there are two numbers.
//
// Asserted as a lower bound on elapsed time, never an upper one: the point
// is that the wait outlived the base deadline, and pinning how far past it
// went would be pinning the load on the machine running the test. The base
// budget is likewise generous enough for one `agent get` to actually
// COMPLETE -- an earlier draft used 20ms, which killed the first poll at
// its own deadline and read as pending, so the test failed for a reason
// that had nothing to do with what it is about.
func TestCLIRunnerAwaitDetectionBlockedBudgetOutlivesTheBase(t *testing.T) {
	bin, argvLog := fakeHerdr(t, blockedAgentJSON())
	r := &CLIRunner{Bin: bin, PollInterval: 5 * time.Millisecond}

	start := time.Now()
	err := r.AwaitDetection(context.Background(), "w1:p2", 100*time.Millisecond, 600*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("AwaitDetection returned success for an agent that stayed blocked")
	}
	if errors.Is(err, ErrAgentBlocked) {
		t.Errorf("error = %v, want a timeout: with a blocked budget, blocked is pending and not a verdict", err)
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %q, want a timeout", err.Error())
	}
	if elapsed < 400*time.Millisecond {
		t.Errorf("gave up after %v, want it to outlive the 100ms base deadline and run out the 600ms blocked budget", elapsed)
	}
	if polls := strings.Count(readArgvLog(t, argvLog), "\n"); polls < 2 {
		t.Errorf("polled %d time(s); a blocked agent under a blocked budget is pending, so the poll must carry on", polls)
	}
}

// TestCLIRunnerAwaitDetectionGivesUpWhenTheAgentStopsBeingReported is the
// declined dialog: the user answers "No, exit", Claude Code exits, and
// `agent get` stops reporting an agent in that pane at all -- which reads
// as "not yet" (classifyAgent has deliberately no "exited" verdict, since
// the call simply fails). Left alone that would burn the whole blocked
// budget on a session that is already gone, so an agent that WAS blocked
// and has since stopped being reported for a full base timeout ends the
// wait with a sentinel of its own.
func TestCLIRunnerAwaitDetectionGivesUpWhenTheAgentStopsBeingReported(t *testing.T) {
	bin, _ := fakeHerdrStaged(t, 2, blockedAgentJSON(), "")
	r := &CLIRunner{Bin: bin, PollInterval: 5 * time.Millisecond}

	start := time.Now()
	err := r.AwaitDetection(context.Background(), "w1:p2", 150*time.Millisecond, 30*time.Second)
	elapsed := time.Since(start)

	if !errors.Is(err, ErrAgentGone) {
		t.Fatalf("AwaitDetection error = %v, want it to wrap ErrAgentGone", err)
	}
	if !strings.Contains(err.Error(), "w1:p2") {
		t.Errorf("error = %q, does not name the pane", err.Error())
	}
	if elapsed > 5*time.Second {
		t.Errorf("took %v to notice the agent had gone, against a 30s blocked budget", elapsed)
	}
}

// TestCLIRunnerAwaitDetectionNeverReportsGoneWithoutHavingSeenBlocked: an
// agent that is simply slow to appear must still time out normally. The
// gone verdict is evidence that something WAS there and stopped being
// reported, so it may never fire for a pane that was never blocked at all
// -- otherwise every ordinary detection timeout would rename itself.
func TestCLIRunnerAwaitDetectionNeverReportsGoneWithoutHavingSeenBlocked(t *testing.T) {
	bin := fakeHerdrFail(t, "not found")
	r := &CLIRunner{Bin: bin, PollInterval: 5 * time.Millisecond}

	err := r.AwaitDetection(context.Background(), "w1:p2", 150*time.Millisecond, 30*time.Second)

	if errors.Is(err, ErrAgentGone) {
		t.Fatalf("error = %v, want a plain timeout: nothing was ever detected in that pane", err)
	}
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error = %v, want a timeout", err)
	}
}

// TestCLIRunnerAgentPromptStalled: herdr's `agent_prompt_stalled` is a
// different fact from its `timeout`, and conflating them would repeat #108
// with the signs reversed.
//
// `timeout` means the wait gave up while something WAS happening -- delivery
// unconfirmed. `agent_prompt_stalled` means herdr observed no working or
// blocked state at all within five seconds, which is positive evidence that
// nothing was processed. Recorded live on 2026-09-09: for the ~seconds after
// a trust dialog is answered, `agent get` reports idle and interactive_ready
// while Claude Code is not yet accepting input, and the pane afterwards holds
// an empty buffer and no turn.
func TestCLIRunnerAgentPromptStalled(t *testing.T) {
	bin := fakeHerdrFailEnvelope(t, "agent_prompt_stalled",
		"agent prompt produced no observed working or blocked state within 5000 ms; current status is idle")
	r := &CLIRunner{Bin: bin}

	err := r.AgentPrompt(context.Background(), AgentPromptReq{Target: "w1:p2", Text: "hi", WaitTimeout: time.Second})

	if !errors.Is(err, ErrPromptStalled) {
		t.Fatalf("error %q is not ErrPromptStalled", err)
	}
	if errors.Is(err, ErrPromptWaitTimeout) {
		t.Errorf("error %q is ALSO ErrPromptWaitTimeout; the two mean opposite things about delivery", err)
	}
	if !strings.Contains(err.Error(), "agent_prompt_stalled") {
		t.Errorf("error %q drops herdr's own code", err)
	}
}

// TestCLIRunnerPaneRunEmitsAnEnvPrefixAsAShellAssignment: an environment
// prefix must reach the pane as a SHELL ASSIGNMENT -- unquoted name, `=`,
// quoted value. This is the one place #72's quote-everything rule cannot
// apply verbatim: `'CLAUDE_CONFIG_DIR=/x' claude` makes the pane's shell look
// for a command called `CLAUDE_CONFIG_DIR=/x`.
//
// It also may not go through env(1). `env VAR=x claude` execs the binary,
// which is precisely what `[clauth] launch = "wrapper"` exists NOT to do: the
// mode is for a login shell whose `claude` is a function, and env would step
// over it.
func TestCLIRunnerPaneRunEmitsAnEnvPrefixAsAShellAssignment(t *testing.T) {
	bin, argvLog := fakeHerdrOK(t)
	r := &CLIRunner{Bin: bin}

	err := r.PaneRun(context.Background(), "w1:p2",
		[]EnvVar{{Name: "CLAUDE_CONFIG_DIR", Value: "/home/a b/dirs/alpha-1"}},
		[]string{"claude", "--model", "opus[1m]"})
	if err != nil {
		t.Fatalf("PaneRun: %v", err)
	}

	wantArgv := `pane run w1:p2 CLAUDE_CONFIG_DIR='/home/a b/dirs/alpha-1' claude --model 'opus[1m]'`
	if got := readArgvLog(t, argvLog); got != wantArgv {
		t.Errorf("argv:\n got %s\nwant %s", got, wantArgv)
	}
}

// No prefix, no change: the default launch path must produce byte-for-byte
// what it produced before this parameter existed.
func TestCLIRunnerPaneRunWithNoEnvIsUnchanged(t *testing.T) {
	bin, argvLog := fakeHerdrOK(t)
	r := &CLIRunner{Bin: bin}

	if err := r.PaneRun(context.Background(), "w1:p2", nil, []string{"clauth", "start", "alpha", "--"}); err != nil {
		t.Fatalf("PaneRun: %v", err)
	}
	if got, want := readArgvLog(t, argvLog), "pane run w1:p2 clauth start alpha --"; got != want {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

// A name that is not a shell identifier is refused rather than typed. The
// value is quoted and therefore inert; the NAME is not, so it is the half
// that has to be validated instead.
func TestCLIRunnerPaneRunRefusesAnUnusableEnvName(t *testing.T) {
	bin, argvLog := fakeHerdrOK(t)
	r := &CLIRunner{Bin: bin}

	for _, bad := range []string{"", "2FOO", "FOO BAR", "FOO=BAR", "FOO;rm -rf /"} {
		err := r.PaneRun(context.Background(), "w1:p2", []EnvVar{{Name: bad, Value: "x"}}, []string{"claude"})
		if err == nil {
			t.Fatalf("PaneRun with env name %q should have been refused", bad)
		}
	}
	// The log file is only created by the fake herdr's first run, so its
	// absence is the assertion: nothing was typed into any pane.
	if _, err := os.Stat(argvLog); err == nil {
		t.Fatalf("a refused env name must type nothing, got %q", readArgvLog(t, argvLog))
	}
}
