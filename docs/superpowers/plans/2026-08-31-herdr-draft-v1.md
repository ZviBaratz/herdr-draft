# herdr-draft v1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A herdr plugin whose popup form creates a fully configured agent session (Linear issue, project, worktree, placement, agent kind, clauth account, initial prompt) in one submit.

**Architecture:** Standalone Go + Bubble Tea binary in a herdr popup pane. Strict dumb-view: `internal/form` renders and routes keys/mouse only; `internal/app` owns all I/O (async, debounced, versioned); `internal/plan` turns form output into an ordered op list executed against a `herdr.Runner` interface (CLI subprocess only — no raw socket use). Pure domain packages first, UI second, submit pipeline last.

**Tech Stack:** Go 1.25, `charm.land/bubbletea/v2 v2.0.8`, `charm.land/bubbles/v2 v2.1.1`, `charm.land/lipgloss/v2 v2.0.5`, `github.com/lrstanley/bubblezone/v2 v2.0.0`, `github.com/BurntSushi/toml`. External processes: `herdr`, `git`, `clauth`. HTTP: Linear GraphQL.

**Spec:** `docs/specs/2026-08-31-herdr-draft-design.md` (this repo). The spec is normative; where this plan and the spec disagree, the spec wins and the plan gets fixed.

## Global Constraints

- Module path: `github.com/ZviBaratz/herdr-draft`. Go `1.25`.
- Charm imports are the **v2 lines** (`charm.land/bubbletea/v2`, `charm.land/bubbles/v2`, `charm.land/lipgloss/v2`) — Atrium ports assume v2 APIs; do not import `github.com/charmbracelet/*` v1 paths.
- License: MIT. **Provenance gate (spec §14):** Atrium files may be ported ONLY from this clean list: `ui/overlay/textInput_create.go`, `textInput_focus.go`, `textInput_keys.go`, `textInput_render.go`, `textInput_size.go`, `directoryPicker.go`, `accountPicker.go`, `accountSelection.go`, `chiprow.go`, `picker.go`, and patterns from `app/app_session.go` / `app_branchsearch.go`. `ui/overlay/textInput.go` and `ui/overlay/branchPicker.go` are AGPL-encumbered: **reimplement, never copy**. Atrium checkout: `/home/zvi/Projects/atrium`. herdr checkout (Apache-2.0, attribution in NOTICE): `/home/zvi/Projects/herdr`.
- No panics on production paths; every error wrapped with `%w` and context. `gofmt -l` empty, `go vet ./...` clean, `go test ./...` green before every commit.
- Commits: lowercase conventional subjects (`feat:`, `fix:`, `test:`, `docs:`, `chore:`); end each commit message body with `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`.
- The form never does I/O (spec §4). All async results carry a version/key and stale results are dropped.
- Fields with static preconditions (Linear unconfigured, <2 clauth profiles) are not rendered; fields with dynamic preconditions (non-git target, non-claude agent) render present-but-inert (spec §6).
- All user-visible strings width-budgeted; degradation order per spec §6.
- Tests never touch the network, a live herdr, clauth, or Linear — fixtures and fakes only. The live checkpoints are Tasks 2b (early probes) and 19 (closeout).

## File Structure

```
herdr-draft/
  go.mod, justfile, LICENSE, NOTICE, README.md, .gitignore
  herdr-plugin.toml
  cmd/herdr-draft/main.go            — entry: env checks, deps wiring, tea.NewProgram
  internal/gitx/slug.go, repo.go     — branch derivation; git subprocess queries
  internal/herdrc/context.go         — HERDR_PLUGIN_CONTEXT_JSON types + parsing
  internal/herdrc/runner.go          — Runner interface + CLIRunner (subprocess, JSON)
  internal/clauth/status.go          — status feed: file-first, CLI fallback, degrade
  internal/linear/client.go, cache.go
  internal/config/config.go, state.go
  internal/theme/palette.go          — herdr palette defaults + user-config overrides
  internal/plan/build.go, exec.go    — form output → ops; staged execution + clean gate
  internal/form/                     — Bubble Tea models (dumb view)
    form.go, focus.go, footer.go, sizes.go
    widgets/picker.go, widgets/chiprow.go, widgets/textarea.go
    field_dir.go, field_issue.go, field_title.go, field_worktree.go,
    field_placement.go, field_agent.go, field_account.go, field_prompt.go,
    submitview.go
    testdata/frames/*.txt            — golden frames
  internal/app/app.go, async.go      — I/O orchestration, debounce, setters
  docs/specs/…, docs/superpowers/plans/…, docs/manual-smoke.md
```

Dependency direction: `form` imports nothing but `theme` and stdlib; `app` imports everything; `plan` imports `herdrc` + `gitx` types only.

---

### Task 1: Repo scaffold

**Files:**
- Create: `go.mod`, `justfile`, `.gitignore`, `LICENSE`, `NOTICE`, `cmd/herdr-draft/main.go`, `cmd/herdr-draft/main_test.go`

**Interfaces:**
- Produces: buildable module `github.com/ZviBaratz/herdr-draft`; `just test`, `just check` recipes every later task uses.

- [ ] **Step 1: Initialize module and pin toolchain**

```bash
cd ~/Projects/herdr-draft
go mod init github.com/ZviBaratz/herdr-draft
go mod edit -go=1.25
```

- [ ] **Step 2: Write `.gitignore`, `LICENSE`, `NOTICE`, `justfile`**

`.gitignore`: `bin/`, `coverage.out`. `LICENSE`: MIT text, copyright `2026 Zvi Baratz`. `NOTICE`:

```
herdr-draft
Copyright 2026 Zvi Baratz

Visual conventions and default palette values are translated from herdr
(https://github.com/herdrdev/herdr), Copyright the herdr contributors,
licensed under the Apache License, Version 2.0.
```

`justfile`:

```make
test:
    go test ./...

check:
    test -z "$(gofmt -l .)"
    go vet ./...
    go test ./...

build:
    go build -o bin/herdr-draft ./cmd/herdr-draft
```

- [ ] **Step 3: Write failing smoke test** — `cmd/herdr-draft/main_test.go`:

```go
package main

import "testing"

func TestVersionString(t *testing.T) {
	if version() == "" {
		t.Fatal("version() must be non-empty")
	}
}
```

- [ ] **Step 4: Run `go test ./cmd/...`** — expected: FAIL (`version` undefined).

- [ ] **Step 5: Minimal `main.go`**

```go
package main

import "fmt"

func version() string { return "0.1.0-dev" }

func main() { fmt.Println("herdr-draft", version()) }
```

- [ ] **Step 6: `just check`** — expected: PASS.

- [ ] **Step 7: Commit** — `chore: scaffold go module, justfile, licensing`

---

### Task 2: Plugin manifest + minimal popup binary

**Files:**
- Create: `herdr-plugin.toml`
- Modify: `cmd/herdr-draft/main.go`

**Interfaces:**
- Produces: linkable herdr plugin; popup opens, draws a placeholder line, `q`/`Esc`/`⌃C` exit 0.

- [ ] **Step 1: Read the manifest reference** — `/home/zvi/Projects/herdr/docs/next/website/src/content/docs/plugins.mdx` (manifest section, ~lines 55–130, and the `[[panes]]` placement section ~line 300). Copy the exact metadata field names from the documented example — do not invent keys.

- [ ] **Step 2: Write `herdr-plugin.toml`** with: plugin id/metadata per the doc example (id `draft`, name `herdr-draft`, description "new session creation dialog"); `[[build]]` running `go build -o bin/herdr-draft ./cmd/herdr-draft`; `[[panes]]` entrypoint id `open`, title `New session`, `placement = "popup"`, `width = "80%"`, `height = "80%"`, command `["bin/herdr-draft"]`; `[[actions]]` id `open`, context `global`, invoking the same binary via the pane entrypoint if the schema supports it, else a command that opens the pane. **Three gotchas:** (a) `herdr plugin pane open --placement` does NOT accept `popup` (CLI parser: overlay/split/tab/zoomed only) — popup placement must come from the manifest entrypoint, so never pass `--placement` when opening. (b) Manifest command argv arrays run with NO shell expansion (plugins.mdx:118-122), so `["$HERDR_BIN_PATH", …]` will not expand — use `["sh","-c","\"$HERDR_BIN_PATH\" plugin pane open --plugin draft --entrypoint open"]` or a checked-in script. (c) Set `min_herdr_version` to the current herdr release (popup panes need ≥0.7.4) — do not copy the doc example's value.

- [ ] **Step 3: Replace main with a minimal Bubble Tea program**

```go
package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
)

func version() string { return "0.1.0-dev" }

type smokeModel struct{}

func (m smokeModel) Init() tea.Cmd { return nil }

func (m smokeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m smokeModel) View() string {
	return "herdr-draft " + version() + " — press q to close"
}

func main() {
	if _, err := tea.NewProgram(smokeModel{}, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "herdr-draft:", err)
		os.Exit(1)
	}
}
```

(Adjust `tea.KeyPressMsg` to the actual bubbletea v2 key message type — check Atrium's usage in `/home/zvi/Projects/atrium/ui/overlay/textInput_keys.go` for the v2 idiom and mirror it.)

- [ ] **Step 4: `go get` the charm deps at pinned versions, `just check`** — expected: PASS.

- [ ] **Step 5: Manual checkpoint (requires a running herdr):** `herdr plugin link ~/Projects/herdr-draft` (the path is positional — `src/cli/spec.rs:804-806`), bind nothing yet, run `herdr plugin pane open --plugin draft --entrypoint open`. Expected: centered popup with the placeholder line; `q` closes it. Record the working invocation in README later. If the id/field names were wrong, fix the manifest now.

- [ ] **Step 6: Commit** — `feat: plugin manifest and popup smoke binary`

---

### Task 2b: LIVE CHECKPOINT — early validation probes

Requires a running herdr session (use a disposable named session) and the linked plugin from Task 2. This runs BEFORE any code builds on assumed shapes; its outputs become the fixtures later tasks consume. Read spec §17 first.

- [ ] **Step 1: Capture creation-response fixtures.** In a throwaway git repo: `herdr worktree create --cwd <repo> --branch probe/x --no-focus`, `herdr workspace create --cwd /tmp --label probe --no-focus`, `herdr tab create --workspace <id> --no-focus`, `herdr pane split --pane <id> --no-focus`. Save each raw JSON response (sanitize paths) to `internal/herdrc/testdata/live/<method>.json`. Close the probe workspaces.

- [ ] **Step 2: Capture the context fixture.** Temporarily add a manifest action running `["sh","-c","printf '%s' \"$HERDR_PLUGIN_CONTEXT_JSON\" > /tmp/ctx.json"]`, invoke it, save the sanitized result as `internal/herdrc/testdata/context.json`, then remove the temporary action.

- [ ] **Step 3: Probe the wrapped launch.** In a probe pane: `herdr pane run <pane_id> clauth start <profile> --` with a safe profile; confirm `herdr agent get <pane_id>` reports a claude agent; time detection latency (informs the 30 s default). Exit the agent cleanly.

- [ ] **Step 4: Probe `agent_pane_busy`.** Immediately after a `worktree create`, run `herdr agent start probe --kind claude --pane <id>`; record whether the first call rejects with `agent_pane_busy` and how long until it succeeds.

- [ ] **Step 5: Probe popup mouse.** In the Task 2 smoke popup (temporarily enable bubbletea mouse mode and print received mouse events), click and wheel; confirm events arrive.

- [ ] **Step 6:** Tick the corresponding spec §17 boxes with one-line findings. Commit fixtures + spec — `chore: live probe fixtures and spec validation findings`

---

### Task 3: gitx — branch slug derivation

**Files:**
- Create: `internal/gitx/slug.go`, `internal/gitx/slug_test.go`

**Interfaces:**
- Produces: `func BranchSlug(prefix, title string) string` (sanitized `prefix+title`; deterministic `<prefix>session-<8hexchars-of-fnv64(title)>` fallback when the slug body sanitizes to empty); `func SanitizeBranch(s string) string`.

- [ ] **Step 1: Failing tests** — `slug_test.go`, table-driven (write fresh; spec §6.3 semantics — do NOT copy Atrium's `session/git/util.go`, its provenance is unaudited):

```go
package gitx

import "testing"

func TestBranchSlug(t *testing.T) {
	cases := []struct{ name, prefix, title, want string }{
		{"simple", "zvi/", "Fix pane focus", "zvi/fix-pane-focus"},
		{"unicode and symbols dropped", "zvi/", "héllo?? world!", "zvi/hello-world"},
		{"collapses runs", "zvi/", "a  --  b", "zvi/a-b"},
		{"trims separators", "zvi/", "-a-", "zvi/a"},
		{"no prefix", "", "Add thing", "add-thing"},
		{"git-invalid chars", "zvi/", "a..b~c^d:e", "zvi/a-b-c-d-e"},
	}
	for _, c := range cases {
		if got := BranchSlug(c.prefix, c.title); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestBranchSlugHashFallback(t *testing.T) {
	got := BranchSlug("zvi/", "??? !!!")
	if len(got) != len("zvi/session-")+8 || got[:len("zvi/session-")] != "zvi/session-" {
		t.Errorf("fallback shape wrong: %q", got)
	}
	if got != BranchSlug("zvi/", "??? !!!") {
		t.Error("fallback must be deterministic")
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/gitx/ -run TestBranchSlug -v` — expected: FAIL (undefined).

- [ ] **Step 3: Implement**

```go
package gitx

import (
	"fmt"
	"hash/fnv"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// SanitizeBranch lowercases, strips diacritics, maps every run of
// non-[a-z0-9] to a single '-', and trims leading/trailing '-'.
func SanitizeBranch(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	folded, _, err := transform.String(t, s)
	if err != nil {
		folded = s
	}
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(folded) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func BranchSlug(prefix, title string) string {
	body := SanitizeBranch(title)
	if body == "" {
		h := fnv.New64a()
		h.Write([]byte(title))
		body = fmt.Sprintf("session-%08x", uint32(h.Sum64()))
	}
	return prefix + body
}
```

- [ ] **Step 4: Run tests** — expected: PASS (fix the sanitize table expectations if the diacritic transform differs; the *behavior contract* is the test table).

- [ ] **Step 5: Commit** — `feat: branch slug derivation with hash fallback`

---

### Task 4: gitx — repository queries

**Files:**
- Create: `internal/gitx/repo.go`, `internal/gitx/repo_test.go`

**Interfaces:**
- Produces:
  - `func IsGitRepo(dir string) bool`
  - `func ListBranches(ctx context.Context, repoDir string, limit int) ([]string, error)` — `git branch -a --sort=-committerdate --format=%(refname:short)`, strip `origin/` prefix, dedupe preserving order, drop `origin/HEAD`, cap `limit`.
  - `func BranchExists(ctx context.Context, repoDir, name string) (bool, error)` — `git show-ref --verify --quiet refs/heads/<name>` OR `refs/remotes/origin/<name>`.
  - `func CurrentBranch(ctx context.Context, repoDir string) (string, error)` — empty string when detached.
  - `func Disposable(ctx context.Context, worktreeDir, baseRef string) (ok bool, reason string, err error)` — true only when `git status --porcelain` is empty AND `git rev-list --count <baseRef>..HEAD` is `0`.

- [ ] **Step 1: Failing tests against real temp repos** — helper creating a repo with `t.TempDir()` + `git init -q` + config user + commits; cases: non-repo dir → `IsGitRepo` false; branches listed newest-first, deduped, capped; `BranchExists` positive/negative; `Disposable` false with a dirty file, false with a commit past base, true when pristine.

```go
func mkRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644)
	run("add", "."); run("commit", "-qm", "init")
	return dir
}
```

- [ ] **Step 2: Run** — expected: FAIL. **Step 3: Implement** with `exec.CommandContext`, `cmd.Dir = repoDir`, output parsing per the interface contract above. **Step 4: Run** — PASS. **Step 5: Commit** — `feat: git repo queries for branch picker and clean gate`

---

### Task 5: herdrc — plugin context + CLI runner

**Files:**
- Create: `internal/herdrc/context.go`, `internal/herdrc/runner.go`, `internal/herdrc/runner_test.go`, `internal/herdrc/testdata/context.json`

**Interfaces:**
- Produces:

```go
type Context struct {
	WorkspaceID    string `json:"workspace_id"`
	WorkspaceLabel string `json:"workspace_label"`
	WorkspaceCwd   string `json:"workspace_cwd"`
	TabID          string `json:"tab_id"`
	FocusedPaneID  string `json:"focused_pane_id"`
	FocusedPaneCwd string `json:"focused_pane_cwd"`
	Worktree       *ContextWorktree `json:"worktree"`
}
func ParseContext(raw string) (Context, error)

type CreatedTopology struct{ WorkspaceID, TabID, PaneID string }

type Runner interface {
	WorkspaceList(ctx context.Context) ([]WorkspaceInfo, error)
	WorktreeCreate(ctx context.Context, req WorktreeCreateReq) (CreatedTopology, error)
	WorkspaceCreate(ctx context.Context, req WorkspaceCreateReq) (CreatedTopology, error)
	TabCreate(ctx context.Context, req TabCreateReq) (CreatedTopology, error)
	PaneSplit(ctx context.Context, req PaneSplitReq) (CreatedTopology, error)
	AgentStart(ctx context.Context, req AgentStartReq) error
	AgentPrompt(ctx context.Context, req AgentPromptReq) error
	AwaitDetection(ctx context.Context, paneID string, timeout time.Duration) error
	PaneRun(ctx context.Context, paneID string, argv []string) error
	WorktreeRemove(ctx context.Context, workspaceID string) error
	WorkspaceClose(ctx context.Context, workspaceID string) error
}

type CLIRunner struct{ Bin string; PollInterval time.Duration }  // implements Runner; the herdr CLI reads HERDR_SOCKET_PATH from the environment itself
```

  Request structs mirror spec §9 exactly: `WorktreeCreateReq{Cwd, Branch, Base, Label string; Focus, TrustRepository bool}`, `AgentStartReq{Name, Kind, PaneID string; ExtraArgs []string}`, `AgentPromptReq{Target, Text string; WaitTimeout time.Duration}`, etc.

- [ ] **Step 1: Fixtures** — `testdata/context.json` and `testdata/live/*.json` were captured from a real herdr in Task 2b; use them verbatim as the canned outputs. Cross-check the `Context` struct tags against `PluginInvocationContext` in `/home/zvi/Projects/herdr/src/api/schema/plugins.rs:363` (snake_case; every field is Optional in Rust — make each Go field a pointer or tolerate absence). The struct tags in this plan are a best current guess; the captured fixtures are normative.

- [ ] **Step 2: Failing tests** — `ParseContext` round-trips the fixture; `CLIRunner` tests use a **fake herdr**: a shell script written to `t.TempDir()` that logs its argv to a file and echoes canned JSON:

```go
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
```

  Cases: `WorktreeCreate` builds exactly `worktree create --cwd X --branch B --base R --label L --focus` and parses `workspace_id`/pane id from JSON; non-zero exit → error containing stderr; `AgentPrompt` passes `--wait --timeout N`.

- [ ] **Step 3: Run** — FAIL. **Step 4: Implement** `CLIRunner` (one private `runJSON(ctx, args...) (json.RawMessage, error)` helper; per-method arg assembly + response structs). Leave `AwaitDetection` and `PaneRun` returning `fmt.Errorf("not implemented")` — they land in Tasks 6 and 7's tests. **Step 5: Run** — PASS. **Step 6: Commit** — `feat: herdr context parsing and CLI runner`

---

### Task 6: herdrc — detection wait

**Files:**
- Modify: `internal/herdrc/runner.go`
- Test: `internal/herdrc/runner_test.go`

**Interfaces:**
- Produces: working `AwaitDetection(ctx, paneID, timeout)` — polls `herdr agent get <paneID>` every 500ms until it returns an agent whose status is any settled/working value, or times out with an error naming the pane and elapsed time.

- [ ] **Step 1: Failing test** — fake herdr script that fails (exit 1, "not found") the first two invocations then succeeds (use a counter file in the script); assert `AwaitDetection` returns nil and made ≥3 calls; second test with an always-failing script and 50ms timeout (poll interval made injectable: `CLIRunner.PollInterval time.Duration`, default 500ms) asserting a timeout error.
- [ ] **Step 2: Run** — FAIL. **Step 3: Implement.** **Step 4: Run** — PASS. **Step 5: Commit** — `feat: detection wait for wrapped agent launches`

---

### Task 7: herdrc — pane run launch

**Files:**
- Modify: `internal/herdrc/runner.go`
- Test: `internal/herdrc/runner_test.go`

**Interfaces:**
- Produces: working `PaneRun(ctx, paneID string, argv []string) error` — invokes `herdr pane run <paneID> <argv…>`, which types the command into the pane's shell and submits it atomically (send-text + Enter; `src/cli/spec.rs` `pane run`, cli-reference.mdx:217). This is Path B's launch primitive (spec §9) — no raw socket use anywhere in the plugin.

- [ ] **Step 1: Failing test** — fake herdr (Task 5 helper) asserting the exact argv `pane run w1:p2 clauth start alpha --`; non-zero exit propagates stderr in the wrapped error.

- [ ] **Step 2: Run** — FAIL. **Step 3: Implement.** **Step 4: Run** — PASS. **Step 5: Commit** — `feat: pane run launch support`

---

### Task 8: clauth status feed

**Files:**
- Create: `internal/clauth/status.go`, `internal/clauth/status_test.go`, `internal/clauth/testdata/status.json`

**Interfaces:**
- Produces:

```go
type Window struct {
	Label          string  `json:"label"`
	UtilizationPct float64 `json:"utilization_pct"`
	ResetsAt       time.Time `json:"resets_at"`
}
type Profile struct {
	Name       string   `json:"name"`
	Active     bool     `json:"active"`
	Tier       string   `json:"tier"`
	AuthStatus string   `json:"auth_status"`
	Windows    []Window `json:"windows"`
}
type Status struct {
	Schema            int       `json:"schema"` // JSON number — verified live: "schema": 1
	ActiveProfile     string    `json:"active_profile"`
	GeneratedAt       time.Time `json:"generated_at"`
	RefreshIntervalMS int       `json:"refresh_interval_ms"`
	Profiles          []Profile `json:"profiles"`
	Degraded          bool      `json:"-"` // set when schema unknown
}
func ParseStatus(b []byte) (Status, error)
func Load(ctx context.Context, opts LoadOpts) (Status, error)
type LoadOpts struct{ StatusFile, CLIBin string; Now func() time.Time }
```

- [ ] **Step 1: Fixture** — build `testdata/status.json` from the real installed clauth: run `clauth status --json` once, redact profile names to `alpha`/`beta`/`gamma`, keep the structure byte-faithful (field names verified live 2026-08-31: `schema`, `active_profile`, `generated_at`, `refresh_interval_ms`, `profiles[].{name,active,provider,tier,has_live_session,auth_status,windows[]}`).

- [ ] **Step 2: Failing tests** — parse fixture (names, auth_status, windows); **unknown schema** (mutate `schema` to `99`) → `Degraded: true`, names still populated, no error; `Load` prefers `StatusFile` when `generated_at + 2×refresh_interval` is after `Now()`, else invokes `CLIBin status --json` (fake script as in Task 5); both missing → error.

- [ ] **Step 3: Run** — FAIL. **Step 4: Implement.** Degradation rule: a parse of the required subset (`profiles[].name`) that succeeds keeps working regardless of `schema`; `Degraded` is set when `schema != 1` — downstream renders name-only entries. **Step 5: Run** — PASS. **Step 6: Commit** — `feat: clauth status feed with schema degradation`

---

### Task 9: Linear client + cache

**Files:**
- Create: `internal/linear/client.go`, `internal/linear/cache.go`, `internal/linear/client_test.go`, `internal/linear/testdata/assigned.json`

**Interfaces:**
- Produces:

```go
type Issue struct {
	Identifier, Title, BranchName, URL, Description string
	StateName, StateType string
	Estimate *float64
	Priority int
	CycleNumber *int
}
type Client struct{ HTTP *http.Client; APIKey, Endpoint string } // Endpoint default https://api.linear.app/graphql
func (c *Client) AssignedIssues(ctx context.Context) ([]Issue, error)
func ResolveAPIKey(apiKeyCmd []string, apiKeyLiteral, configDir string) (string, error) // order: cmd → $LINEAR_API_KEY → literal (reject if config file perms are wider than 0600)
func LoadCache(stateDir string) ([]Issue, time.Time, error)
func SaveCache(stateDir string, issues []Issue) error
```

- [ ] **Step 1: Fixture** — `testdata/assigned.json`: a GraphQL response for the spec §10 query (2 issues: one `unstarted` with estimate+cycle, one `started` with nulls), shaped exactly like Linear's `{"data":{"viewer":{"assignedIssues":{"nodes":[…]}}}}`.

- [ ] **Step 2: Failing tests** — `httptest.NewServer` asserting: `Authorization` header is the bare key (Linear personal keys use no `Bearer` prefix), body contains `assignedIssues` and `branchName`; response parsed into `[]Issue` with nils handled. GraphQL-level error payload → error. Cache: `SaveCache`+`LoadCache` round-trip with mtime as the timestamp; corrupt file → error (caller discards, spec §12 loss-tolerance lives in app layer).

- [ ] **Step 3: Run** — FAIL. **Step 4: Implement** with the spec §10 query verbatim as a `const`. **Step 5: Run** — PASS. **Step 6: Commit** — `feat: linear assigned-issues client and cache`

---

### Task 10: config + state

**Files:**
- Create: `internal/config/config.go`, `internal/config/state.go`, `internal/config/config_test.go`

**Interfaces:**
- Produces: `type Config` mirroring spec §12 exactly (`BranchPrefix`, `DefaultWorktree`, `DefaultPlacement`, `Linear{APIKeyCmd []string; APIKey string; PromptTemplate string}`, `Clauth{Enabled *bool; Default string}`, `Agents{Favorites []string; Default string; ExtraArgs map[string][]string}`, `Timeouts{DetectionMS, PromptWaitMS int}`, `Palette map[string]string` from the optional `[palette]` table — spec §12); `func Load(configDir string) (Config, error)` — missing file → all defaults; defaults: prefix `strings.ToLower(user.Username)+"/"`, worktree true, placement `new-space`, favorites `["claude"]`, detection 30000, prompt wait 120000. State: `type State{Recents []string; LastKind, LastPlacement string; LastWorktree *bool}`, `LoadState`/`SaveState(stateDir)` — corrupt/missing state silently zero-valued.

- [ ] **Step 1: Failing tests** — empty dir → defaults populated; full TOML (spec §12 example verbatim as the test fixture string) → every field parsed; unknown keys ignored; corrupt state → zero value, no error; recents round-trip capped at 20 with most-recent-first dedupe (`func (s *State) TouchRecent(path string)`).
- [ ] **Step 2: Run** — FAIL. **Step 3: Implement** with `github.com/BurntSushi/toml`. **Step 4: Run** — PASS. **Step 5: Commit** — `feat: plugin config and loss-tolerant state`

---

### Task 11: theme — herdr palette

**Files:**
- Create: `internal/theme/palette.go`, `internal/theme/palette_test.go`

**Interfaces:**
- Produces:

```go
type Palette struct {
	Accent, PanelBG, Text, DimText, Danger, Success, Border lipgloss.Color
}
func Builtin(name string) (Palette, bool)    // every built-in herdr palette, translated
func Default() Palette                       // herdr's default palette
func Resolve(base Palette, overrides map[string]string) Palette // parse #hex; ignore invalid
func LoadHerdrPalette(draftOverrides map[string]string) Palette
// resolution: herdr config [theme] name → Builtin (auto_switch / "terminal"
// resolve to the configured dark variant, best-effort) → [theme.custom]
// overrides → draftOverrides (herdr-draft's own [palette] table, applied
// last) — any failure at a stage falls back to the previous stage,
// ultimately Default(). Pixel parity is NOT a v1 gate (spec §7).
```

- [ ] **Step 1: Translate constants** — the actual color values live in `/home/zvi/Projects/herdr/src/app/state.rs` (palette constructors from ~line 112; `Palette::from_name` at ~line 562 — `theme.rs` holds NO color values, only the config structs). Translate every palette `from_name` accepts, including its name aliases, into `Builtin`. Read `/home/zvi/Projects/herdr/src/config/theme.rs` for the config shape: `CustomThemeColors` keys (`accent`, `panel_bg`, …), `parse_color`, `auto_switch`, and mode-specific overrides. Cite `state.rs` + the herdr commit hash in a comment and in `NOTICE`. herdr's config lives at its platform config dir + `config.toml` (`src/config/io.rs:173`); on Linux resolve `${XDG_CONFIG_HOME:-~/.config}/herdr/config.toml`.

- [ ] **Step 2: Failing tests** — `Resolve` merges a valid override and ignores `"not-a-color"`; `Builtin("tokyo-night")` differs from `Default()`; config-path-injectable `LoadHerdrPaletteFrom(path string, draftOverrides map[string]string)`: fixture TOML with `[theme] name` selects the builtin, `[theme.custom] accent` overrides it, a draft override wins over both; missing file → `Default()`.
- [ ] **Step 3: Run** — FAIL. **Step 4: Implement.** **Step 5: Run** — PASS. **Step 6: Commit** — `feat: herdr palette resolution with attribution`

---

### Task 12: plan — creation-plan builder

**Files:**
- Create: `internal/plan/build.go`, `internal/plan/build_test.go`

**Interfaces:**
- Produces:

```go
type Placement int
const (PlacementNewSpace Placement = iota; PlacementTabHere; PlacementSplitHere)

type Input struct {
	ProjectDir, Title, Branch, BaseRef string
	UseWorktree bool
	Placement   Placement
	AgentKind   string
	ExtraArgs   []string
	AccountPin  string // "" = active / unpinned
	Prompt      string
	Ctx         herdrc.Context
	DetectionTimeout, PromptTimeout time.Duration
	TrustRepository bool
}

type Op struct {
	Kind  OpKind // OpWorktreeCreate | OpWorkspaceCreate | OpTabCreate | OpPaneSplit | OpAgentStart | OpClauthLaunch | OpAwaitDetection | OpAgentPrompt
	Label string // progress line, e.g. "creating worktree"
	// exactly one populated request field per Kind:
	Worktree  *herdrc.WorktreeCreateReq
	Workspace *herdrc.WorkspaceCreateReq
	Tab       *herdrc.TabCreateReq
	Split     *herdrc.PaneSplitReq
	Agent     *herdrc.AgentStartReq
	RunArgv   []string // OpClauthLaunch: argv for Runner.PaneRun
	Prompt    *herdrc.AgentPromptReq
	Timeout   time.Duration
}
func Build(in Input) ([]Op, error)
```

  Also produces `func AgentName(title string) string` — herdr agent names must match `[a-z][a-z0-9_-]{0,31}` (agent-automation.mdx:38): sanitize like a branch slug, prefix `s-` when the first rune is not a lowercase letter, clamp to 30 runes to leave room for a 2-char dedupe suffix.

  Rules (spec §9): worktree on → `OpWorktreeCreate` regardless of placement; worktree off → op per placement. Launch: `AccountPin != "" && AgentKind == "claude"` → `OpClauthLaunch` (`RunArgv = ["clauth","start",pin,"--"]+ExtraArgs`, executed via `Runner.PaneRun`) + `OpAwaitDetection`; else `OpAgentStart` (name = `AgentName(Title)`, kind, extra args). `Prompt != ""` → final `OpAgentPrompt`. Pane/workspace ids inside requests that depend on step-1 output are left empty — the executor fills them (Task 13). Validation errors (empty title; worktree on without git repo flag in input — carry `IsGitRepo bool` in `Input`) return descriptive errors.

- [ ] **Step 1: Failing table-driven tests** — full matrix, asserting op kinds in order and key request fields:
  - worktree+pin+prompt → `[OpWorktreeCreate, OpClauthLaunch, OpAwaitDetection, OpAgentPrompt]`, `RunArgv` exact.
  - `AgentName("42 fix pagination")` → `s-42-fix-pagination`; a 40-rune title clamps to ≤30; every output matches `[a-z][a-z0-9_-]{0,31}`.
  - worktree+active+claude → `[OpWorktreeCreate, OpAgentStart, OpAgentPrompt]` (agent start does its own detection wait).
  - in-place + `PlacementTabHere` + codex + no prompt → `[OpTabCreate, OpAgentStart]`, tab req carries `Ctx.WorkspaceID`.
  - in-place + `PlacementSplitHere` → `OpPaneSplit` with `Ctx.FocusedPaneID`.
  - pin + non-claude kind → error (account pinning is claude-only, spec §6.7).
  - `UseWorktree && !IsGitRepo` → error.
  - empty title → error.
- [ ] **Step 2: Run** — FAIL. **Step 3: Implement `Build`.** **Step 4: Run** — PASS. **Step 5: Commit** — `feat: creation plan builder covering all launch paths`

---

### Task 13: plan — executor with keep/clean gate

**Files:**
- Create: `internal/plan/exec.go`, `internal/plan/exec_test.go`

**Interfaces:**
- Produces:

```go
type StepState int
const (StepPending StepState = iota; StepRunning; StepDone; StepFailed)
type Progress struct{ Index, Total int; Label string; State StepState; Err error }

type ExecResult struct {
	Created      *herdrc.CreatedTopology // nil until topology op succeeds
	FailedIndex  int                     // -1 on success
	PromptText   string                  // surfaced back on prompt failure
}
func Execute(ctx context.Context, r herdrc.Runner, ops []Op, onProgress func(Progress)) ExecResult

type CleanDecision struct{ Allowed bool; Reason string }
func CleanCheck(ctx context.Context, in Input, created herdrc.CreatedTopology) CleanDecision // gitx.Disposable for worktrees; always allowed for empty non-worktree spaces
func Clean(ctx context.Context, r herdrc.Runner, in Input, created herdrc.CreatedTopology) error // WorktreeRemove or WorkspaceClose per Input.UseWorktree
```

  Execution threads step-1 output into later ops (fills `Agent.PaneID`, the `PaneRun` target pane, `Prompt.Target` from `Created`); emits `Progress` before and after each op; stops at first failure. **Busy retry (spec §9):** an op failing with an error containing the code `agent_pane_busy` (`herdr:src/app/agents.rs:255` — the shell still starting right after topology creation, upstream #3375's race) is retried every 500 ms for up to 5 s (interval and clock injectable) before the failure is recorded.

- [ ] **Step 1: Failing tests with a mock Runner** — `type mockRunner struct{ calls []string; failAt string; failErr error; failCount int; topo herdrc.CreatedTopology }` implementing every method by appending its name+args and failing when name == failAt (up to failCount times). Cases: happy path threads pane id from `WorktreeCreate` into `AgentStart` and `AgentPrompt`; failure at `AgentStart` → `FailedIndex` correct, no further calls, `Created` non-nil; `AgentStart` failing twice with an `agent_pane_busy` error then succeeding → overall success with 3 calls (injected zero interval); persistent non-busy error → immediate failure, no retry; failure at `AgentPrompt` → `PromptText` populated; progress sequence `[Running,Done]×n` verified; `Clean` on worktree input calls `WorktreeRemove`, non-worktree calls `WorkspaceClose`; `CleanCheck` denies a dirty worktree (temp repo from Task 4 helper with an uncommitted file) with a human-readable `Reason`.
- [ ] **Step 2: Run** — FAIL. **Step 3: Implement.** **Step 4: Run** — PASS. **Step 5: Commit** — `feat: staged plan executor with keep-or-clean gate`

---

### Task 14: form widgets — picker and chiprow (ports)

**Files:**
- Create: `internal/form/widgets/picker.go`, `internal/form/widgets/chiprow.go`, tests alongside

**Interfaces:**
- Produces: `widgets.Picker` — filterable single-select list: `SetItems(version int, items []PickerItem)`, `SetQuery(string)`, `Selected() (PickerItem, bool)`, `CursorNext/Prev()`, `View(width, height int) string`, versioned so a stale `SetItems` (version < last seen) is ignored; `PickerItem{ID, Label, Hint string; Marker string}`. `widgets.ChipRow` — horizontal single-select: `SetChips([]Chip)`, `Next/Prev()`, `SetInert(bool, placeholder string)`, `Selected() Chip`, `View(width int) string`; `Chip{ID, Label, FocusHint string}`.

- [ ] **Step 1: Port** — source `/home/zvi/Projects/atrium/ui/overlay/picker.go` and `chiprow.go` (both on the clean provenance list). Adaptations: package `widgets`; drop Atrium theme/styles in favor of an injected `theme.Palette`; keep the versioned-filter logic (`picker.go:154-163` in Atrium) intact; chiprow keeps the `inherit`-style focused-hint mechanism (`chiprow.go:107-138`) generalized to `FocusHint`.
- [ ] **Step 2: Failing tests** — picker: filter narrows, cursor survives a same-version refresh, **stale version ignored**, empty-result state renders placeholder; chiprow: wrap-around nav, inert mode renders placeholder and refuses nav.
- [ ] **Step 3: Run** — FAIL (ports not yet compiling) → adapt until PASS. **Step 4: Commit** — `feat: port picker and chiprow widgets from atrium`

---

### Task 15: form widgets — textarea + key grammar (reimplement + port)

**Files:**
- Create: `internal/form/widgets/textarea.go`, `internal/form/keys.go`, tests alongside

**Interfaces:**
- Produces: `widgets.PromptArea` wrapping `charm.land/bubbles/v2` textarea (4 rows preferred, 1 floor, width-laddered placeholder via `SetPlaceholderLadder([]string)`); `form.KeyAction` enum + `func MapKey(msg tea.KeyPressMsg, zone FocusZone) KeyAction` implementing the spec §6 grammar: Tab/Shift-Tab advance/back, Enter advance-or-submit, ⌃S submit, Esc/⌃C cancel, ⌃R⌃R armed clear (arm state passed in/out), ⌃J/⇧↵/⌥↵ newline only in prompt zone, paste as data never keys.

- [ ] **Step 1: Provenance note in file header** — `textarea.go` is a **reimplementation** (Atrium's `textInput.go` is AGPL-encumbered; do not open it while writing this file — work from `bubbles/textarea` docs and the spec). `keys.go` ports from `/home/zvi/Projects/atrium/ui/overlay/textInput_keys.go` (clean list), including the paste-routing guard (`HandlePaste` separate from `HandleKeyPress`).
- [ ] **Step 2: Failing tests** — grammar table: (`tab`, zone=dir) → `ActionComplete`; (`enter`, zone=title, titleEmpty=false) → `ActionSubmit`; (`enter`, zone=prompt) → `ActionAdvance`; (`ctrl+j`, zone=prompt) → `ActionNewline`; ⌃R once → `ActionArmClear`, ⌃R again → `ActionClear`, ⌃R then `x` → disarmed. PromptArea: ladder picks widest fitting placeholder.
- [ ] **Step 3: Run** — FAIL → implement → PASS. **Step 4: Commit** — `feat: key grammar port and reimplemented prompt textarea`

---

### Task 16: form root — focus ring, layout, skin, golden frames

**Files:**
- Create: `internal/form/form.go`, `internal/form/focus.go`, `internal/form/sizes.go`, `internal/form/footer.go`, `internal/form/form_test.go`, `internal/form/testdata/frames/`

**Interfaces:**
- Produces: `form.Model` (tea.Model) — `form.New(cfg form.Setup) Model` where `Setup{Palette theme.Palette; Sections []Section}`; `type Section interface { ID() string; Enabled() bool; Focus(); Blur(); Update(tea.Msg) tea.Cmd; View(inner int) string; Height(winH int) int }`; focus ring skipping disabled sections with wrap (port `textInput_focus.go` — clean list); constant-height section budget + degradation ladder (port `textInput_size.go`, `textInput_render.go` — clean list); footer key ladder (`footer.go`). Setter methods used by the app layer land with each field task.

- [ ] **Step 1: Port + adapt** focus/render/size from the clean list; strip Atrium's three-role overlay switching (create-form role only); apply `theme.Palette` styles: panel bg painted explicitly across the full popup area, accent for the focused section marker, `✓` list markers, action-button row styled after herdr's `render_action_button` conventions (read `/home/zvi/Projects/herdr/src/ui/widgets.rs:151-210` for the shape being imitated — imitate, don't translate line-by-line).
- [ ] **Step 2: Golden-frame harness**

```go
func assertFrame(t *testing.T, name string, m form.Model, w, h int) {
	t.Helper()
	got := m.ViewAt(w, h) // deterministic render entry point added for tests
	path := filepath.Join("testdata", "frames", name+".txt")
	if *update {
		os.WriteFile(path, []byte(got), 0o644)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil || string(want) != got {
		t.Errorf("frame %s mismatch (run with -update to regenerate)\n%s", name, got)
	}
}
```

  with `var update = flag.Bool("update", false, "regenerate golden frames")`.
  **Determinism:** lipgloss v2 output varies with the detected color profile — pin it explicitly inside `ViewAt` (force truecolor; check the v2 renderer/profile API against how Atrium's frame tests keep `/home/zvi/Projects/atrium/app/testdata/frames/` stable) so frames are machine-independent.
- [ ] **Step 3: Failing tests** — with two stub sections: focus ring wraps and skips disabled; frames `empty-80x24.txt`, `empty-120x40.txt` generated then committed after visual inspection; degradation at 80×20 keeps the Create button visible (stub sections + shrink).
- [ ] **Step 4: Run/implement/PASS.** **Step 5: Commit** — `feat: form root with focus ring, herdr skin, golden frames`

---

### Task 17: fields, part 1 — directory, title, worktree, placement

**Files:**
- Create: `internal/form/field_dir.go`, `field_title.go`, `field_worktree.go`, `field_placement.go`, tests + frames

**Interfaces:**
- Produces four `Section` implementations with app-facing setters:
  - `DirField` (port `directoryPicker.go` — clean): dual fragment/path mode, Tab shell-completion-then-advance, `SetCandidates(version, []string)`, `SetValidity(path string, v Validity)` (`ValidityRepo|ValidityDirect|ValidityInvalid`), `Value() string`, `Hint(string)`.
  - `TitleField`: 32-rune cap, `SetVerdict(key, text string)` (bounded 21 cells), `Value()`, exposes `Touched()`.
  - `WorktreeField`: on/off chips + branch text input + base `widgets.Picker`; `SetGitTarget(isRepo bool)` (inert w/ distinct placeholders), `SetBranch(v string, seeded bool)` honoring touched-vs-preselected, `SetBaseItems(version, items)`, `SetBaseStatus(s string)` (`searching…`/`couldn't list`), `Base() string` ("" = HEAD), `Branch() string`, `Enabled() / On() bool`. **Reimplementation note:** base-picker behavior follows spec §6.4; Atrium's `branchPicker.go` is AGPL-encumbered — build on `widgets.Picker`, do not open the Atrium file.
  - `PlacementField`: three chips; `SetWorktreeOn(bool)` → inert with hint `worktree opens as its own space`; `Value() plan.Placement`.
- [ ] **Step 1: Failing tests per field** (behavior + a golden frame each: `dir-browse-80x24`, `title-verdict-80x24`, `worktree-nongit-80x24`, `placement-inert-80x24`). Test the touched rule explicitly: `SetBranch("zvi/from-linear", true)` then user types → later `SetBranch(…, true)` is ignored.
- [ ] **Step 2: Run/implement/PASS.** **Step 3: Commit** — `feat: directory, title, worktree, placement fields`

---

### Task 18: fields, part 2 — issue, agent, account, prompt, submit view

**Files:**
- Create: `internal/form/field_issue.go`, `field_agent.go`, `field_account.go`, `field_prompt.go`, `submitview.go`, tests + frames

**Interfaces:**
- Produces:
  - `IssueField`: `widgets.Picker` over `SetIssues(version, []linear.Issue)` with `none` row 0; rows render `identifier · title` with status/estimate hint; `Selected() *linear.Issue`.
  - `AgentField`: favorites chips + full-list picker behind a `more…` chip; `SetKinds([]string)` (populated by app layer from config + the spec's known 23), `Value() string`.
  - `AccountField`: rendered only when constructed (static gate lives in app layer); `SetProfiles(clauth.Status)`, `SetAgentIsClaude(bool)` (dynamic inert), rows `name · tier · auth · 5h N%`, `active` row 0; `Pin() string` ("" = active); degraded status → name-only rows; rate-limited or auth-failed profiles carry a warning marker (no confirm modal — deferred, spec §16).
  - `PromptField`: wraps `widgets.PromptArea`; `Value() string`; fork of placeholder per spec §6.8.
  - `SubmitView`: renders staged `plan.Progress` list + the keep/clean failure prompt (`k` keep / `c` clean when allowed, reason line when not) — pure view over `SetProgress([]plan.Progress)`, `SetFailure(res plan.ExecResult, clean plan.CleanDecision)`, emits `SubmitMsg`/`KeepMsg`/`CleanMsg`.
- [ ] **Step 1: Failing tests** — seeding: selecting an issue emits `IssueChosenMsg{Issue}` (app layer routes it to title/branch/prompt setters); account inert flips with agent kind; frames: `issue-picker-120x40`, `account-80x24`, `progress-80x24`, `failure-clean-denied-80x24`.
- [ ] **Step 2: Run/implement/PASS.** **Step 3: Commit** — `feat: issue, agent, account, prompt fields and submit view`

---

### Task 19: LIVE CHECKPOINT — closeout validation

Requires a running herdr session. Task 2b verified the raw shapes early; this pass validates the finished code paths end-to-end and closes the remaining spec §17 items.

- [ ] **Step 1:** Full Path A run via the real popup: worktree on, no pin, prompt set — verify workspace created, agent detected, prompt delivered, sidebar grouping correct. Clean up.
- [ ] **Step 2:** Full Path B run: pinned clauth profile — verify the pane ran `clauth start`, detection reports claude, prompt delivered under the pinned account. Clean up.
- [ ] **Step 3:** Popup background paint check with the real skin against the user's herdr theme (spec §17); fix palette fallout if any.
- [ ] **Step 4:** Record the minimum supported clauth version for Task 22's README; tick every remaining §17 box with one-line findings. `just check`; commit — `fix: closeout findings from live validation`

---

### Task 20: app layer — startup, data sources, seeding

**Files:**
- Create: `internal/app/app.go`, `internal/app/async.go`, `internal/app/app_test.go`
- Modify: `cmd/herdr-draft/main.go`

**Interfaces:**
- Produces: `app.Model` (the real tea.Model) owning `form.Model` + all data sources behind small interfaces (`herdrc.Runner`, `linearSource`, `clauthSource`, `gitSource` — each satisfied by the real packages and by test fakes). Responsibilities, each as a `tea.Cmd` returning a typed msg:
  - startup: parse context (missing/invalid `HERDR_PLUGIN_CONTEXT_JSON` or unreachable socket → plain-text error to stderr, exit 1 — the pre-open refusal, spec §9); load config/state/palette; construct only the statically-applicable sections (Linear? clauth ≥2 profiles?).
  - debounced (150ms, versioned) reactions ported as *patterns* from Atrium `app_branchsearch.go` (clean list): dir validity check, branch list fetch + one `git fetch --prune` per repo per form-open, title dup verdicts (branch exists / workspace label taken via `WorkspaceList`).
  - Linear: cache-render-then-refresh; clauth: load at open + on account focus.
  - `IssueChosenMsg` routing: title/branch/prompt seeding with touched-respect; issue templates from config.
- [ ] **Step 1: Failing tests** — with fakes: debounce coalesces (two `SetQuery` within 150ms → one fetch, use injectable clock `func() time.Time` + a test scheduler); stale version dropped end-to-end (fetch v1 resolves after v2 → v1 result discarded); dup verdicts computed and pushed to `TitleField` via setters; issue selection seeds title/branch/prompt and respects a touched branch.
- [ ] **Step 2: Run/implement/PASS** (keep `async.go` mechanical: one `type request{version int; key string}` guard used by every source). **Step 3: Wire `main.go`**: env checks → deps → `tea.NewProgram(app.New(deps), tea.WithAltScreen(), <mouse option>)` — verify the bubbletea v2 mouse-enable option name against Atrium's usage, same as the key-msg type. **Step 4: Commit** — `feat: app startup, data sources, seeding`

---

### Task 20b: app layer — submit pipeline wiring

**Files:**
- Modify: `internal/app/app.go`, `internal/app/async.go`
- Test: `internal/app/submit_test.go`

**Interfaces:**
- Consumes: `plan.Build`/`plan.Execute`/`plan.CleanCheck`/`plan.Clean` (Tasks 12–13), `SubmitView` msgs (Task 18), validation state from Task 20.
- Produces: submit orchestration — validations first (spec §9 list: directory validity; branch/label duplicates block and re-focus Title; pinned profile `auth_status != ok` blocks with an account verdict), then `plan.Build` + `plan.Execute` in a `tea.Cmd`, streaming `plan.Progress` msgs into `SubmitView`; `KeepMsg`/`CleanMsg` → `plan.Clean` guarded by `plan.CleanCheck`.

- [ ] **Step 1: Failing tests** — with fakes: happy-path submit produces the exact op list from Task 12's first matrix case and forwards every progress msg in order; dup verdict blocks submit and focuses Title; pinned profile with `auth_status: "expired"` blocks with an account verdict; a failed step puts `SubmitView` in failure state with the `CleanCheck` reason threaded through; `CleanMsg` on a denied check does nothing.
- [ ] **Step 2: Run/implement/PASS.** **Step 3: Commit** — `feat: submit pipeline wiring`

---

### Task 21: mouse support

**Files:**
- Modify: `internal/form/form.go`, all `field_*.go`, `internal/form/widgets/*.go`
- Test: `internal/form/mouse_test.go`

**Interfaces:**
- Produces: bubblezone/v2 integration — every focusable section, chip, picker row, and the Create button registers a zone (`zone.Mark(id, view)`); `form.Model` handles `tea.MouseClickMsg` (focus section / activate chip / select row / submit) and `tea.MouseWheelMsg` (scroll focused picker or prompt). Zone IDs: `section:<id>`, `chip:<sectionID>:<chipID>`, `row:<sectionID>:<n>`, `button:create`.

- [ ] **Step 1: Failing tests** — zone-ID map: render at 120×40, assert `zone.Get("button:create")` is in-bounds and a synthesized click at its coords produces `SubmitMsg`; click on `chip:placement:tab-here` selects it; wheel over the base picker moves its cursor. (Consult Atrium's bubblezone usage absence — this is new code — and bubblezone/v2 README for the scan/Mark API.)
- [ ] **Step 2: Run/implement/PASS.** **Step 3: Manual checkpoint in the linked popup: click every field, wheel the pickers.** **Step 4: Commit** — `feat: mouse support via bubblezone`

---

### Task 22: docs, NOTICE completion, manual smoke matrix, provenance re-audit

**Files:**
- Create: `README.md`, `docs/manual-smoke.md`
- Modify: `NOTICE`

- [ ] **Step 1: README** — what it is (one screenshot placeholder comment), install (`herdr plugin install ZviBaratz/herdr-draft` once published; `herdr plugin link --path` for now), keybinding example (`[[keys.command]]` with `type = "plugin_action"`), config reference (spec §12 table rendered as prose), the #3228 resume caveat verbatim from spec §11, minimum clauth version (from Task 19 findings), troubleshooting (`ui_busy`, socket unreachable).
- [ ] **Step 2: `docs/manual-smoke.md`** — the spec §15 release matrix as literal checklists: Path A/B × worktree on/off, each with setup commands (disposable herdr session), expected sidebar/agent outcomes, and teardown.
- [ ] **Step 3: Provenance re-audit (spec §14 gate)** — for each ported file, confirm its Atrium source is on the clean list; confirm `widgets/textarea.go` and `field_worktree.go` contain the reimplementation header note; update `NOTICE` with the final list of Atrium-derived files ("portions derived from atrium, © Zvi Baratz, relicensed by the author").
- [ ] **Step 4: `just check`; commit** — `docs: readme, manual smoke matrix, provenance audit`

---

## Self-Review (performed at write time)

- **Spec coverage:** §5→T2; §6 grammar→T15/T16, fields→T14/T17/T18; §7→T11/T16/T21; §8→T20; §9→T12/T13/T20/T20b (pre-open refusal T20; busy retry T13); §10→T9; §11→T8 (+T2b latency); §12→T10; §13 verdicts→T17/T18/T20b; §14→T1/T15/T17/T22; §15→T16 frames, T22 matrix, per-task units; §16 needs no tasks; §17→T2b (early probes), T19 (closeout).
- **Placeholder scan:** the two intentional "check the real source" steps (T5 context tags, T11 palette values) are verification steps against files the executor has locally, with the normative source named — not TBDs.
- **Type consistency:** `herdrc.CreatedTopology`, `plan.Input/Op/Progress`, `theme.Palette`, `widgets.Picker` names checked across tasks 5–21.
