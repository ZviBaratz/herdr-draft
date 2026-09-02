# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

herdr-draft is a [herdr](https://github.com/herdrdev/herdr) plugin: a
standalone Go + Bubble Tea (charm.land/bubbletea/v2) TUI binary that herdr
launches in a popup pane. It is a stack of one-line rows over a single
detail panel — issue, title, prompt, project, worktree, placement, agent,
account — and drives herdr exclusively through its public CLI
(`$HERDR_BIN_PATH`), never the raw socket API. The same binary also carries
a headless `create` verb that produces the same session without the popup.

Two design documents, and the citation convention distinguishes them.
`docs/specs/2026-08-31-herdr-draft-design.md` is v1 and is what a bare
"spec §N" in a code comment means. `docs/specs/2026-09-02-herdr-draft-v2-design.md`
supersedes v1's §6 (the form) and §7 (skin & mouse) and is cited as
"v2 spec §N"; every other v1 section stays authoritative. Most non-trivial
doc comments cite a spec section, a "fix round" finding, or a
live-checkpoint discovery — read them; they usually explain a non-obvious
constraint rather than restating the code.

## Commands

```bash
just build   # go build -o bin/herdr-draft ./cmd/herdr-draft
just test    # go test ./...
just check   # gofmt -l . (must be empty) && go vet ./... && go test ./...
```

Single package/test: `go test ./internal/plan/...` or
`go test ./internal/app/ -run TestHandleSubmit`.

Manual release smoke (Path A/B × worktree on/off, plus the headless `create`,
the repo config and per-project memory) is in `docs/manual-smoke.md` — not
part of CI, run it by hand before a release. Read its warning before running
`herdr-draft create` anywhere: herdr exports the pane variables into every
shell, so a `create` in your own session creates a real session there.

`go test ./...` covers unit tests plus golden-frame rendering tests
(`*frames_test.go`) with no real I/O — every package that touches herdr,
git, Linear, or clauth is tested against a small local interface + fake, never
the real subprocess/network.

## Architecture

Layering, outermost to innermost:

- **`cmd/herdr-draft`** — deliberately thin `main`, and the verb router
  (v2 spec §13): no argument opens the popup — read
  `$HERDR_PLUGIN_CONTEXT_JSON` / `$HERDR_PLUGIN_CONFIG_DIR` /
  `$HERDR_PLUGIN_STATE_DIR` / `$HERDR_BIN_PATH`, construct production
  `Deps`, call `app.Bootstrap`, run the `tea.Program`. `create` hands the
  rest of the command line to `internal/create`; `help`/`-h`/`--help`
  prints usage to stdout and exits 0; anything else prints usage to stderr
  and exits 2. `dispatch` takes both verbs as injected funcs so routing is
  testable without a `tea.Program` or a herdr.
- **`internal/app`** — the real `tea.Model`. Owns `form.Model` plus every
  data source behind it (Linear, clauth, git, herdr), and is the *only*
  package that performs I/O orchestration and routes values between fields
  (`form.IssueChosenMsg` seeding Title/Branch/Prompt, debounced
  dir-validity/base-branch/title-dup/repo-config checks in `async.go`,
  submit-pipeline wiring). It also declares the **row order**, in one
  place: the `sections` slice in `New`. `Bootstrap` does the pre-open
  refusal (spec §9): only an unparseable plugin context, an unreachable
  herdr, or a broken `config.toml` refuse outright — a broken Linear key or
  clauth load degrades that one field to "unavailable, with a reason"
  instead of blocking the plugin.
- **`internal/create`** — the headless verb (v2 spec §13). `Run(ctx, args,
  Env, Deps) int` parses the flags, resolves everything unset through
  `internal/defaults`, builds and executes the same `plan.Op` list the form
  does, and returns the process exit code (0 created, 1 the plan started
  and failed, 2 bad usage or an unresolvable request, 3 herdr unreachable).
  It owns **no precedence of its own** — `equivalence_test.go` drives a
  real `app.Model` and a real `create` request over the same files and
  asserts the two `plan.Input`s are identical field for field, which is the
  test that keeps them from drifting. Progress goes to stderr one line per
  step; stdout carries a created session and nothing else.
- **`internal/form`** — "the form is a dumb view" (spec §4): a `Section`
  interface, focus ring, key grammar (`keys.go`), mouse zones
  (`bubblezone/v2`), and rendering only. No I/O, no knowledge of herdr/git/
  Linear. **The `Section` shape is the thing to read first** (`form.go`):
  v1 asked each field how tall it wanted to be; v2 asks for `Label()`,
  `Row(w)` — exactly one line, always, focused or not — and
  `Panel(w, h)`/`PanelRows()`, rendered only for the focused section. One
  line per field and one panel for the whole form is what makes the row
  positions fixed. Four optional interfaces sit beside it (`titleValuer`,
  `completer`, `newliner`, `footerHinter`), all checked by type assertion
  on the focused section. Every verdict and candidate list is pushed in
  from `internal/app` via setters on each field's own *concrete* type
  (`field_*.go`) — the `Section` interface itself deliberately does not
  expose them.
- **`internal/plan`** — `build.go` maps a completed form (`plan.Input`) to
  an ordered `[]Op` with **no I/O at all**; `exec.go` (`Execute`) is the
  separate executor that runs those ops against a `herdrc.Runner`, threads
  step-1 topology output (workspace/tab/pane ids) into later ops, and
  implements the post-submit keep-or-clean gate. `dialog.go` recognizes a
  small list of known "blocking confirmation dialog" screen signatures.
- **`internal/defaults`** — the layered-defaults resolver (v2 spec §10),
  and **the only place the precedence chain exists**. Pure: every tier is
  handed to `Resolve(Sources) Resolved` already loaded, so the form and the
  headless command cannot disagree about what an unset field means. The
  `Tier` constants are *ordered by precedence* and applied lowest-first, so
  the last writer wins: `TierBuiltin` → `TierUserConfig` (`config.toml`) →
  `TierGlobalMemory` (`last-used.json`) → `TierRepoConfig`
  (`.herdr-draft.toml`) → `TierProjectMemory` (`projects.json`).
  `Resolved.From` records which tier supplied each field, which is what
  lets a panel say `from .herdr-draft.toml` and `create --json` print its
  provenance.
- **`internal/herdrc`** — `context.go` decodes the plugin invocation
  context; `runner.go`'s `CLIRunner` is the only place that shells out to
  the `herdr` binary (`Runner` interface, fakeable in tests).
- **`internal/config`** — loads `config.toml` (all keys optional, unknown
  keys ignored) and persists small loss-tolerant JSON state
  (`recents.json`, `last-used.json`, `projects.json` in
  `$HERDR_PLUGIN_STATE_DIR`). `repo.go` loads the repository's committed
  `.herdr-draft.toml`, whose five-key allow-list is a **trust boundary**,
  not a convenience: the file arrives with `git clone`, so it may only
  choose among values the user could already have picked in the form, and
  never name a command, a path outside the repository, or a credential.
  Both the allow-list and the deny-list are guarded by an `init()` that
  panics if they ever overlap. Anything rejected is reported, never
  silently dropped.
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
- **`Row(w)` takes no height parameter, by design.** A section renders its
  row from its own state and the value column's width, and must not consult
  the window height in any way — that is precisely what makes "row i is
  always at row i" hold as focus travels and the panel grows. It is a
  contract rather than a comment because `field_rows_test.go` renders every
  section at two window heights and compares the bytes. Reaching for the
  height inside a `Row` is the shape of the bug that convention exists to
  catch.
- **App-layer state is diffed, not event-driven.** `form.Model` exposes no
  "section X changed" signal; `Model.reactToChanges` (in `app.go`) compares
  each relevant getter against a last-observed snapshot after every routed
  message and schedules debounced async work (`async.go`) on a real change.
  New reactive behavior tied to a field's value generally belongs there,
  following the same before/after-comparison pattern each field's own
  `Update` already uses internally. Touched-versus-preselected flags need
  their **own** snapshot fields: `syncDerivedInertness` resyncs its own on
  every call, so sharing one silently stops per-project memory re-applying
  after the first project change.
- **A golden-frame suite proves only the states someone thought to
  fixture.** The v2 form shipped a defect in its *opening* state — the
  first thing every user sees, a worktree row naming a branch called
  "branch name" — through fifteen green commits, because every fixture had
  a title already typed. `go test ./...` passing is not evidence the screen
  is right. Build it and run it (`docs/manual-smoke.md`'s Route B runs the
  real form in an ordinary pane, no popup needed), and when a change moves
  a state no frame pins, add the frame.
