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

Three design documents, and the citation convention distinguishes them.
`docs/specs/2026-08-31-herdr-draft-design.md` is v1 and is what a bare
"spec §N" in a code comment means. `docs/specs/2026-09-02-herdr-draft-v2-design.md`
supersedes v1's §6 (the form) and §7 (skin & mouse) and is cited as
"v2 spec §N". `docs/specs/2026-09-02-herdr-draft-v3-design.md` supersedes
v2's §4 (the screen), §7 (skin, palette, mouse), §9 (degradation) and §6's
`account` row, and is cited as "v3 spec §N"; its §13 itemises every
superseded sentence, so a reader who lands on a v2 citation can find out in
one place whether it still holds. Everything each document did not replace
stays authoritative, which is why plenty of live code still cites v2 §7 and
v2 §9 correctly. Most non-trivial doc comments cite a spec section, a "fix
round" finding, or a live-checkpoint discovery — read them; they usually
explain a non-obvious constraint rather than restating the code.

## Commands

```bash
just build   # go build -o bin/herdr-draft ./cmd/herdr-draft
just test    # go test ./...
just unused  # staticcheck -checks U1000 -tests=false ./...
just check   # gofmt -l . (empty) && go vet ./... && just unused && go test ./...
```

Single package/test: `go test ./internal/plan/...` or
`go test ./internal/app/ -run TestHandleSubmit`.

`just unused` needs **staticcheck**, the one dev-tool dependency outside
`go.mod`. Its version is pinned in exactly one place — the justfile's
`staticcheck_version` — and both the recipe and CI read it from there
(`just --evaluate staticcheck_version`), so don't repeat the number
anywhere else, including here. Run `just unused` with it missing and the
recipe prints both spellings (`go install`, or `go run` without
installing) already carrying the pinned version; an installed version that
differs is reported as a note and not enforced, because a newer release
refining `unused` is a finding to read rather than a break to work around.

It is deliberately **not** a `tool` directive in `go.mod`, which is
otherwise how you would pin one today: the pinned staticcheck needs Go
1.26, so `go get -tool` rewrites this module's own `go` line 1.25 → 1.26 — and
herdr runs `[[build]]`'s `go build` on the **user's** machine at install
time, so pinning a linter that way would raise the floor for everyone
installing the plugin. A dev tool must not narrow who can install.

It is a real gate, not advice — `just check` fails on a
hit — because `go vet` does not detect unused unexported code **at all**,
and that blind spot is the one this project keeps falling into: it is how
the whole v1 drop-lines cascade survived the compose path's deletion, and
how half of #25's filter-count work sat in a green tree defined and called
by nothing. `-tests=false` is the load-bearing flag; without it a symbol
kept alive only by the test that exists to call it counts as used, which is
the exact shape both defects had.

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
- **`herdr pane run` types, it does not exec — so `PaneRun` quotes.**
  `pane run` joins its argv with single spaces and sends the string as pane
  input plus Enter (`herdr:src/cli/pane.rs`), so the argv structure is gone
  before anything runs and the pane's own shell re-parses the result.
  `CLIRunner.PaneRun` therefore shell-quotes every element, and callers
  (`plan.Build`'s `RunArgv`) pass a plain argv. This is the third distinct
  way `pane run` has misled this codebase, after `runJSON` vs `runOK` and
  the exit code that covers the typing rather than the command: an unquoted
  `[agents.extra_args]` model id became `zsh: no matches found` with the
  launch step still reporting ok (#72). Note where the quoting may NOT go:
  `AgentStart` hands the same values to herdr as an argv vector with no
  shell anywhere, so quoting them there corrupts them — which is why the
  old config-side workaround could not be right for both paths at once.
- **Screen detection is evidence-based, not trusted blindly.** herdr's own
  agent detection can report a pane "idle"/ready while it is actually
  showing a blocking dialog. `plan.Execute` always calls `Runner.AgentRead`
  (`--source detection`) and checks it against `internal/plan/dialog.go`'s
  known signatures *before* sending a queued prompt — never send on
  "detected" alone. The founding example, Claude Code's first-run trust
  prompt, is no longer one: herdr 0.9.0's manifest learned that screen and
  reports it `blocked`, refusing it upstream under two codes
  (`agent start` → `agent_not_ready`, `agent prompt` → `agent_blocked`).
  That is why the guard is worth reading rather than deleting — the
  manifest knows *that screen*, not the class, so a login chooser or a
  permission prompt still arrives as "ready". Reading the pane also now
  earns its second keep: `explainBlockedStart` matches the same signature
  list to turn herdr's "blocked during startup" into an instruction the
  user can act on (#90).
- **Citations into herdr's source name a pinned commit, never a local
  path.** Two spellings, both anchored to
  `b1ff4582e9688f52ffb943cfa8bee4871ae122e4`: the `herdr:src/cli.rs`
  shorthand for a passing reference, and a full
  `https://github.com/herdrdev/herdr/blob/b1ff4582/...#L123` URL where a
  reader will actually want to go look. Eleven comments used to cite
  `/home/zvi/Projects/herdr/...`, a tree nobody else has — the claims were
  correct and unverifiable at the same time, which is the worst of both.
  A citation that only resolves on one laptop is not a citation.
- **`plan.Build` stays pure.** It performs no I/O and never touches a
  `herdrc.Runner`; anything Build needs to decide (is this a git repo, what
  is the invoking pane id) must already be resolved by the caller
  (`internal/app`) and passed in via `plan.Input`.
- **`[worktree] trust_repository` is `config.toml`-only, and that is a
  trust boundary rather than an omission.** The key adds
  `--trust-repository` to `herdr worktree create` (herdr 0.9.0, #3044),
  which waives git's ownership check for one request. It resolves through
  `internal/defaults` like everything else but with a chain that stops at
  `TierUserConfig`: `.herdr-draft.toml` is refused because a repository
  arriving via `git clone` must never assert its own trustworthiness (it is
  on `repo.go`'s deny-list, and the allow-list being flat means the table
  form is rejected structurally, not by anyone remembering to), and the two
  memory tiers are refused because they remember what the user *chose in
  the form* and no row offers this. There is deliberately no
  `--trust-repository` flag on `create` either: every flag there has a form
  row behind it, and `equivalence_test.go` is what would break first.
  (Until 2026-09-08 this convention said the opposite — leave the key
  unwired, because the 0.8.2 floor predated the flag. herdr 0.9.0 and the
  floor bump retired that.)
- **The version is one fact in four files.** `herdr-plugin.toml`'s
  `version`, `herdrc.Version`, `CHANGELOG.md`'s newest release heading and
  the git tag `vX.Y.Z` move together. Three of them are a gate
  (`TestVersionMatchesManifest`, `TestChangelogMatchesVersion`); **the tag
  is the one no test can reach**, because a correct checkout can be tagless
  — a tarball, a shallow CI clone, a fresh worktree — so nothing may assert
  one exists. That is precisely what makes it the member to forget, and it
  is not ceremony: herdr has no `plugin update` command, so `herdr plugin
  install owner/repo --ref <tag>` is the documented way to pin or refresh
  an install and needs something to point at, and `just build` stamps
  `git describe --tags --always --dirty` into the binary, which degrades to
  a bare hash — no version at all in `herdr-draft version`'s build line —
  when no tag is in reach. The release date goes on **with** the tag, in
  its own commit, never earlier: the heading must carry the date and the
  tag must name a commit that already contains it, which only works in that
  order. `CONTRIBUTING.md`'s "Releases" is the procedure; a heading reading
  `unreleased` on `main` is a normal state, meaning the version is settled
  and the release is held.
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
- **Frames record bytes, not perceptibility**, which is the same lesson one
  layer down and the one v3 exists for: v2 drew its rules and its focused-row
  fill at a contrast ratio of **1.07:1**, correctly, at the right width, and
  invisibly. A green fixture says nothing about whether a colour can be seen.
  Anything that marks a region — a rule, a fill, an input background — needs
  a **measured** floor against the ground it is actually drawn on, over all
  eighteen builtins, in `internal/theme/contrast_test.go` beside
  `ActiveRowContrastFloor` and `InputFillContrastFloor`. Note "the ground it
  is actually drawn on": an input is only rendered while its field is
  focused, so it sits on `ActiveRowBG`, not `PanelBG` — and on the default
  theme those two happen to be byte-identical to `Surface`, which is how a
  flat-`Surface` input fill would have shipped invisible a second time.
- **A test-only symbol is kept by a `//lint:ignore`, not by hope.**
  `just unused` runs with `-tests=false`, so anything only a test calls
  reads as dead. Two symbols are kept that way on purpose and each carries
  a `//lint:ignore U1000 <reason>` as the last line of its doc comment
  (`rowlayout.go`'s `frame.lines`, which states the `lines() == h`
  invariant the tests assert, and `rowvalues.go`'s `rowEllipsis`, the
  form-side name the row tests and the neighbouring prose read in). That
  directive is the *only* accepted suppression — there is no allow-list
  file — because the reason has to sit where the next sweep will read it.
  Two things follow. Reach for it only when the symbol is genuinely a
  statement rather than a leftover: #16 collapsed the duplicated
  `footerRungsFor` body instead of annotating it, precisely because a
  duplicated body can drift and an alias or an invariant cannot. And
  staticcheck does **not** report a directive that has stopped matching
  anything, so a suppression left on a symbol that later gains a real
  caller, or on one that should have gone, rots silently.
- **`go test ./... -update` FAILS.** The `-update` flag is registered
  per-binary, in `internal/form/form_test.go` and `internal/app/frames_test.go`
  only, so a bare `./...` run errors on every other package. Regenerate per
  package: `go test ./internal/form/ -update` then
  `go test ./internal/app/ -update`.
