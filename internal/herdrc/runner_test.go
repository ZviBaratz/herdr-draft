package herdrc

import (
	"context"
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
