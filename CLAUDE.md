# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

herdr-draft is a [herdr](https://github.com/herdrdev/herdr) plugin: a
standalone Go + Bubble Tea (charm.land/bubbletea/v2) TUI binary that herdr
launches in a popup pane. It reproduces Atrium's "new session" creation form
— Linear issue, project directory, git worktree (branch + base), placement,
agent kind, clauth account, initial prompt — and drives herdr exclusively
through its public CLI (`$HERDR_BIN_PATH`), never the raw socket API.

The authoritative design document is `docs/specs/2026-08-31-herdr-draft-design.md`
("spec §N" in code comments refers to it). Most non-trivial doc comments in
this codebase cite a spec section, a "fix round" finding, or a live-checkpoint
discovery — read them; they usually explain a non-obvious constraint rather
than restating the code.

## Commands

```bash
just build   # go build -o bin/herdr-draft ./cmd/herdr-draft
just test    # go test ./...
just check   # gofmt -l . (must be empty) && go vet ./... && go test ./...
```

Single package/test: `go test ./internal/plan/...` or
`go test ./internal/app/ -run TestHandleSubmit`.

Manual release smoke (throwaway herdr session, Path A/B × worktree on/off) is
in `docs/manual-smoke.md` — not part of CI, run it by hand before a release.

`go test ./...` covers unit tests plus golden-frame rendering tests
(`*_frames_test.go`) with no real I/O — every package that touches herdr,
git, Linear, or clauth is tested against a small local interface + fake, never
the real subprocess/network.

## Architecture

Layering, outermost to innermost:

- **`cmd/herdr-draft`** — deliberately thin `main`: reads
  `$HERDR_PLUGIN_CONTEXT_JSON` / `$HERDR_PLUGIN_CONFIG_DIR` /
  `$HERDR_PLUGIN_STATE_DIR` / `$HERDR_BIN_PATH` from the environment,
  constructs production `Deps`, calls `app.Bootstrap`, runs the `tea.Program`.
- **`internal/app`** — the real `tea.Model`. Owns `form.Model` plus every
  data source behind it (Linear, clauth, git, herdr), and is the *only*
  package that performs I/O orchestration and routes values between fields
  (`form.IssueChosenMsg` seeding Title/Branch/Prompt, debounced
  dir-validity/base-branch/title-dup checks in `async.go`, submit-pipeline
  wiring). `Bootstrap` does the pre-open refusal (spec §9): only an
  unparseable plugin context, an unreachable herdr, or a broken
  `config.toml` refuse outright — a broken Linear key or clauth load
  degrades that one field to "unavailable, with a reason" instead of
  blocking the plugin.
- **`internal/form`** — "the form is a dumb view" (spec §4): a `Section`
  interface, focus ring, key grammar (`keys.go`), mouse zones
  (`bubblezone/v2`), and rendering only. No I/O, no knowledge of herdr/git/
  Linear. Every verdict and candidate list is pushed in from `internal/app`
  via setters on each field's own *concrete* type (`field_*.go`) — the
  `Section` interface itself deliberately does not expose them.
- **`internal/plan`** — `build.go` maps a completed form (`plan.Input`) to
  an ordered `[]Op` with **no I/O at all**; `exec.go` (`Execute`) is the
  separate executor that runs those ops against a `herdrc.Runner`, threads
  step-1 topology output (workspace/tab/pane ids) into later ops, and
  implements the post-submit keep-or-clean gate. `dialog.go` recognizes a
  small list of known "blocking confirmation dialog" screen signatures.
- **`internal/herdrc`** — `context.go` decodes the plugin invocation
  context; `runner.go`'s `CLIRunner` is the only place that shells out to
  the `herdr` binary (`Runner` interface, fakeable in tests).
- **`internal/config`** — loads `config.toml` (all keys optional, unknown
  keys ignored) and persists small loss-tolerant JSON state
  (`recents.json`, `last-used.json`, etc. in `$HERDR_PLUGIN_STATE_DIR`).
- **`internal/clauth`, `internal/linear`, `internal/gitx`, `internal/pathx`,
  `internal/theme`** — thin, single-purpose clients/helpers, each behind a
  small interface `internal/app` depends on (`clauthSource`, `linearSource`,
  `gitSource`) so it can be tested with fakes.

### Load-bearing conventions

- **herdr CLI has two success shapes, not one.** Most subcommands print a
  JSON envelope (`runJSON`); a few (`pane run`) report success by exit code
  alone with nothing on stdout (`runOK`). Routing one through the other's
  parser is a real bug this codebase hit once already (`PaneRun` via
  `runJSON` failed every real invocation) — check which shape a new
  subcommand uses before wiring it up.
- **Screen detection is evidence-based, not trusted blindly.** herdr's own
  agent detection can report a pane "idle"/ready while it is actually
  showing a blocking dialog (e.g. Claude Code's first-run trust prompt).
  `plan.Execute` always calls `Runner.AgentRead` (`--source detection`) and
  checks it against `internal/plan/dialog.go`'s known signatures *before*
  sending a queued prompt — never send on "detected" alone.
- **`plan.Build` stays pure.** It performs no I/O and never touches a
  `herdrc.Runner`; anything Build needs to decide (is this a git repo, what
  is the invoking pane id) must already be resolved by the caller
  (`internal/app`) and passed in via `plan.Input`.
- **`--trust-repository` is deliberately unwired**, not just unimplemented:
  herdr's minimum supported version (0.8.2, `min_herdr_version` in
  `herdr-plugin.toml`) predates the flag and would fail worktree creation
  outright if it were passed. Don't add a config key for it until
  `min_herdr_version` is bumped past the herdr release that carries it.
- **App-layer state is diffed, not event-driven.** `form.Model` exposes no
  "section X changed" signal; `Model.reactToChanges` (in `app.go`) compares
  each relevant getter against a last-observed snapshot after every routed
  message and schedules debounced async work (`async.go`) on a real change.
  New reactive behavior tied to a field's value generally belongs there,
  following the same before/after-comparison pattern each field's own
  `Update` already uses internally.
