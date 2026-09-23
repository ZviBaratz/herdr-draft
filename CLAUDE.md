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

Eleven documents in `docs/specs/`, and the citation convention distinguishes
them. A bare "spec §N" in a code comment means v1, the 2026-08-31 design;
every other document that code cites by section carries a prefix:

| citation | document (`docs/specs/`) | what it replaces |
|---|---|---|
| bare `spec §N` | `2026-08-31-herdr-draft-design.md` (v1) | — |
| `v2 spec §N` | `2026-09-02-herdr-draft-v2-design.md` | v1's §6 (the form) and §7 (skin & mouse) |
| `v3 spec §N` | `2026-09-02-herdr-draft-v3-design.md` | v2's §4 (the screen), §7 (skin, palette, mouse), §9 (degradation) and §6's `account` row; its §13 itemises every superseded sentence |
| `placement spec §N` | `2026-09-03-placement-and-pane-ownership-design.md` | parts of v1, v2 and v3, itemised in its §12; its own §14 then reverses its §5.3 and §6.1, and §15 names the tab a session opens |
| `spawn-skill spec §N` | `2026-09-16-spawn-skill-design.md` | — |
| `reap spec §N` | `2026-09-17-pane-reaper-ready-design.md` | amends v2's §6, §8, §10, §11 and §13 and v1's §12; its §12 itemises |
| `agent-options spec §N` | `2026-09-18-agent-options-design.md` | reverses v1's §3 and §16 item 3 and v2's §16, and amends v3's §4 and §7.2, v2's §8, §11 and §13 and v1's §12; its §12 itemises |
| `draw-first spec §N` | `2026-09-21-draw-first-design.md` | amends v1's §6, §8, §9, §10 and §13, v2's §6.1 and §13 and v3's §12; implemented 2026-09-22 (#293, #292); its §12 itemises |
| `host-colours spec §N` | `2026-09-23-host-colours-design.md` | amends v3's §5.3a (the `terminal` exemption becomes conditional) and §5.4 (the focused row's fill returns); implemented 2026-09-23 (#347); its §13 itemises |

Two more carry no section prefix because nothing cites them by section, so
name them by filename: `2026-09-07-herdr-membership-assessment.md` is the
evidence base for `2026-09-08-herdr-8.1-8.2-correction.md`, which retracts
the placement spec's §2.4 and §8.1. The supersession chain is therefore
v1 → v2 → v3 → placement → correction, four links rather than the one
v1 → v2 → v3 this paragraph described until 2026-09-20.

Everything each document did not replace stays authoritative, which is why
plenty of live code still cites v2 §7 and v2 §9 correctly. Most non-trivial
doc comments cite a spec section, a "fix round" finding, or a
live-checkpoint discovery — read them; they usually explain a non-obvious
constraint rather than restating the code.

**Every section number is reused, so a bare `spec §N` is only ever as good
as the check that produced it** (#287). v1 runs §1–§17, v2 §1–§16 and v3
§1–§14, over largely the same ground: §9 is v1's submit pipeline, v2's
degradation and v3's resting panel; §12 is v1's config and state, v2's
submit and failure and v3's testing. There is no subset of numbers where
the bare form is structurally safe, and the tree shows it working in both
directions. §6, §9 and §12 carry 141, 77 and 50 bare citations that are
all correct, and beside them 16 `v2 spec §9` and 34 `v2 spec §12` for the
other meaning — one number, both meanings, told apart every time, because
whoever wrote them checked. §10, §11 and §13 are the same shape with the
check missing: 56, 47 and 30 sites said v1 while meaning v2's defaults
resolution, repo-config trust boundary and headless `create`. Each already
had correctly prefixed neighbours, so the tree disagreed with itself
rather than being uniformly wrong.

So the rule is not "avoid a bare citation where a later document reused
the number" — that would condemn all 356 correct ones. It is: **open v1
§N and confirm it says what your comment claims.** Three things that check
catches and nothing else does. A citation can name the right document and
the wrong *section*: seven comments quoted a clauth reload cadence as §11
when it is §8's data-source table, with the quotation marks around a
paraphrase that appears in no document. A prefix may sit on the *previous*
comment line, so a line-based grep reports 36 correct citations in this
tree as bare — join the comment paragraph before counting. And a
sentence-initial citation is spelled `Spec §N`: a case-sensitive grep for
`spec §` misses all 14 of those, which is how 11 wrong ones survived an
entire pass of this work, including the `repoAllowedKeys` comment #287
had named as the most important in the tree.

## Commands

```bash
just build   # go build -o bin/herdr-draft ./cmd/herdr-draft
just test    # go test ./...
just unused  # staticcheck -checks U1000 -tests=false ./...
just check   # gofmt -l . (empty) && go vet ./... && just unused && go test ./...
just live    # the real form under a pty, every input stubbed, screen to stdout
just live-selftest  # that driver's terminal emulator alone, fed bytes; <1s
just screenshots    # docs/images/*.svg from the showcase golden frames
just frames-selftest  # the frame-to-SVG converter's own tests
```

`live` and `live-selftest` need `python3` and sit outside `just check` and
CI on purpose — `hack/live/README.md` has why, the usage, and the ways a
pty reading can still mislead you. `screenshots` and `frames-selftest` need
`python3` too; the README's images are drawn from the `showcase-*` golden
frames in `internal/app`, and `TestShowcaseImagesAreCurrent` fails `just
check` when an image is older than its frame or the converter. So a change
that moves a showcase frame is finished only after `go test ./internal/app/
-update` AND `just screenshots`.

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
  rest of the command line to `internal/create`; `skill` prints the
  agent-facing document from `internal/skill`, or with `--check <path>`
  compares an installed copy against it; `help`/`-h`/`--help`
  prints usage and `version`/`--version`/`-V` the build line, both to
  stdout with exit 0; anything else prints usage to stderr and exits 2.
  `dispatch` takes the popup and both verbs as injected funcs so routing is
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
  **Before the first frame it runs no subprocess but `herdr workspace
  list`** (draw-first spec §3.1). The three that used to run there — the
  Linear `api_key_cmd` at up to 60s, `clauth status --json` at 30s when
  clauth's status file is stale, and an account picker's probe at 30s —
  run from `Init` instead, and the rows say what they are waiting for. What
  stays is what a local read answers: whether a key *source* is configured
  (`linear.KeySourceConfigured`, pure), `linear.LoadCache`, and clauth's
  status file plus a `PATH` check (`clauth.Peek`). The **row set is still
  fixed before the first frame** — only what a row says changes afterwards
  — which is why those three questions have to be answerable without
  running anything.
- **`internal/create`** — the headless verb (v2 spec §13). `Run(ctx, args,
  Env, Deps) int` parses the flags, resolves everything unset through
  `internal/defaults`, builds and executes the same `plan.Op` list the form
  does, and returns the process exit code (0 created, 1 the plan started
  and failed, 2 bad usage or an unresolvable request, 3 herdr unreachable,
  4 the plan's first step failed before anything existed).
  It owns **no precedence of its own** — `equivalence_test.go` drives a
  real `app.Model` and a real `create` request over the same files and
  asserts the two `plan.Input`s are identical field for field, which is the
  test that keeps them from drifting. Progress goes to stderr one line per
  step; stdout carries a created session and nothing else. `--dry-run`
  (#209) stops after the whole pre-flight, before `plan.Execute`: it
  reports what would be created, spends no pick and writes no memory. It
  adds nothing to `plan.Input`, so it does not break the rule that every
  flag has a form control; the form's counterpart is the form itself.
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
- **`internal/agentopts`** — the per-agent-kind declaration of session
  options (agent-options spec §4): Claude's model, effort and permission
  mode, their offered values, their flags, and the argv a choice becomes
  (`Launch`, which also displaces the same flag in `[agents.extra_args]`).
  Pure, and it imports nothing from this module, so `plan` can build from
  it and `config` can validate against it. `internal/form` does **not** import it: the app turns each
  `Option` into a `form.OptionSpec`. The form's `options` row, `create`'s
  flags and `config.toml`'s `[agents.options.<kind>]` are all rendered from
  or validated against it, which is why declaring another kind's options
  is a change to this package alone.
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
- **`internal/clauth`, `internal/linear`, `internal/gitx`, `internal/picker`,
  `internal/pathx`, `internal/theme`** — thin, single-purpose clients/helpers,
  each behind a small interface `internal/app` depends on (`clauthSource`,
  `linearSource`, `gitSource`, `pickerSource`) so it can be tested with
  fakes. `internal/picker` speaks the documented account-picker protocol
  (`<picker> --dir <path> --json [--strict] [--dry-run]`) to whatever
  executable is configured, rather than depending on any one tool.
- **`internal/skill`** — not a client: the embedded `spawn_skill.md` that
  `herdr-draft skill` prints, the renderer that stamps the binary path,
  version and content digest into it, and `--check` (#311). No interface
  and no I/O beyond what `Run` is handed (the writers, `os.Executable`,
  and the `os.ReadFile` that `--check` reads through), and it imports
  nothing else from this module, because prose about the CLI must not
  depend on the code the CLI drives; the tests holding that prose to the
  code live in `internal/create`.

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
  `AgentStart` hands the same values to herdr as an argv vector, and herdr
  quotes them itself — at v0.9.0 `agent start` quotes each element for the
  pane's shell and types the line, exactly as `pane run` does
  (`herdr:src/app/agents.rs` and `src/platform/mod.rs` at v0.9.0), so the
  agent's command is resolved by that shell on both paths. Quoting them
  here would quote them twice, which is why the old config-side workaround
  could not be right for both paths at once. What differs between the two
  is who quotes, not whether a shell is involved: this side of `pane run`,
  and herdr's side of `agent start`.
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
- **A green launch step is not proof the pane is safe to type into.** The
  convention above assumes herdr tells us when it sees a dialog. It does,
  *sometimes*. Measured live on 0.9.0 (docs/manual-smoke.md, 2026-09-10):
  `herdr agent start` returned **ok** on three consecutive popup submits
  into one throwaway repo while the trust prompt was on screen — and a
  headless `create` into **that same repo**, minutes later, refused the same
  screen with `agent_not_ready`. Its readiness verdict and its manifest's
  classification of that screen are two clocks, and which one lands first is
  not ours to control. So the two upstream codes are a race rather than a
  guarantee, and `dialog.go`'s guard is the only thing standing between a
  queued prompt and a confirmation dialog. It can lose that race too, which
  was **#116**: the guard read a screen that had not painted, the prompt's
  Enter answered the preselected "No, exit", and the pipeline reported a
  clean create over a dead agent. Anything new that trusts a successful
  launch step is trusting a coin flip.
- **The prompt guard's rule is positive, and it brackets the send.** #116's
  fix is two rules because its defect had two halves, and neither subsumes
  the other. Before the send, a read must come back with *something* on it:
  "no dialog signature matched" was the old rule and an unpainted screen
  satisfies it, so `promptIfReady` refuses an empty read as
  `errPaneUnpainted` and `awaitDialogCleared` no longer counts a blank poll
  as a cleared one. **That half does not close #116's own window**, and the
  measurement is the reason to read this rather than infer it: polled live
  on 2026-09-10, `agent read` fails for ~300-500ms after `agent start`, then
  succeeds for a further ~1.4s carrying the shell's echo of the launch
  command and clauth's banner — real text, no signature — and only then
  paints the dialog. `TestPromptGuardOnTheMeasuredStartupWindow` pins those
  bytes. After the send, `confirmPromptLanded` reads the pane again, and it
  is what actually covers that window: prevention can only narrow one — it
  cannot make the report honest when the window is missed anyway, and a
  create that lies is the half that costs the most. It **reports and never
  resends**, which is
  what `isUnsafeScreenError` exists to say in one place: every other screen
  refusal happens before any text is sent, which is what makes waiting and
  retrying safe, and `errPromptSwallowed` is deliberately not in that set.
  Its two verdicts rest on different evidence and are worth keeping
  straight — a pane that stops answering most likely means the agent is
  gone, which is size-independent but inferred, since `agent read` fails
  the same way whatever went wrong; a dialog still up with no trace of the
  prompt is weaker again and is gated by `promptIsVerifiable`, because
  `ExecResult.PromptUnconfirmed`'s own doc comment already recorded that a
  large prompt is reflowed and scrolled by the agent's TUI and appears
  verbatim nowhere. Note also what does *not* separate the two panes: a
  dialog. "Enter to confirm" is Claude Code's own permission-prompt footer,
  so an agent that received the prompt and immediately asked to act on it
  is sitting on a matching screen with the prompt delivered. What the two
  verdicts share is the posture: both come after herdr accepted the send,
  so both are **unconfirmed**, never `unsent`, and `CleanCheck` refuses
  the clean (#154).
- **Only the popup waits for a person; `create` never does — and that
  difference may not live in `plan.Input`.** `internal/create`'s
  `equivalence_test.go` compares the form's `plan.Input` against the headless
  command's field for field with `reflect.DeepEqual`, so a field the two
  paths must disagree about would break the one test that keeps them from
  drifting. Runtime differences go in `plan.ExecOpts`, passed to `Execute`
  beside the ops. `TrustWait` (#115) is the first: five minutes of waiting
  for a human to answer a dialog is right for a popup and wrong for a script
  with nobody at the keyboard. Zero means "behave exactly as this did before
  the option existed", which is what lets `create` opt out by passing an
  empty `ExecOpts` rather than by carrying a branch of its own. `NoFocus`
  is the second, and the other way round: `create` sets it, so nothing it
  opens pulls the person's view away from what they are doing, while the
  popup, whose person just asked for the session, keeps herdr's `--focus`.
- **A prompt has four fates, and "it failed" is only one of them.**
  `herdr agent prompt` can succeed; time out its confirmation
  (`ErrPromptWaitTimeout`, herdr's `timeout`); stall
  (`ErrPromptStalled`, herdr's `agent_prompt_stalled`); or fail outright.
  The middle two look alike and mean opposite things, which is why each is a
  typed sentinel rather than a substring match. A **timeout** means the wait
  gave up while the agent was demonstrably busy, so delivery is UNKNOWN: the
  text must never be relabelled "unsent", never resent (#108), and
  `CleanCheck` refuses the clean. A **stall** means herdr observed no
  working or blocked state at all, which is positive evidence nothing was
  processed — so it is the one failure worth sending again, exactly once,
  through `promptIfReady` so the guard gates the retry. Its dominant cause is
  measured: for about a second after a trust dialog clears, `agent get`
  reports `idle` and `interactive_ready` while the agent's TUI is not yet
  accepting input. Collapsing the two would either strand a delivered prompt
  or double-paste into a working agent. That retry is **not** gated on
  `trust_wait_ms` and must not be re-gated on it (#132): the budget is for
  waiting on a *person*, and a two-second settle waits for nobody, so both
  the popup and headless `create` take it. Note also where "nothing was
  processed" stops: herdr writes the text and Enter *before* it starts
  watching (herdr v0.9.0, `src/api/wait.rs`), so a stall that survives the
  retry takes the **unconfirmed** posture — two sends have been made, the
  pane may hold two copies. And the posture is **sticky**: once herdr has
  typed the text, no later failure in the same op may take it back to
  "unsent", which is what stops the retry's own guard — correctly refusing a
  dialog that painted during the settle — from re-permitting the clean it
  had just refused. "Typed" is **recorded where herdr accepts the send**
  (`promptIfReady`'s `typed` result), not inferred afterwards from which
  error came back: #154 was a first send that the post-send check found
  swallowed, reported `unsent` because the list of errors meaning "the text
  went out" did not include it. When `agent prompt` itself fails, an error
  counts if herdr *may* have raised it after typing began
  (`promptTextMayBeTyped`): the stall, the timeout and `agent_not_running`,
  which herdr's wait raises only after its dispatch succeeded, and
  `agent_prompt_failed` (#228), which proves nothing either way. herdr
  raises that one before anything is queued and when a write fails partway
  through the text alike, and neither the code nor the message separates
  them — "PTY actor closed during input submission" covers a submission
  that had typed nothing as well as one halfway through — so it counts
  because "unsent" is the claim that costs, not because typing is likely.
  Don't try to split it by herdr's message: `herdrc.ErrPromptSendFailed`'s
  doc cites every path at v0.9.0. `ExecResult.promptUnconfirmedCause` carries
  which of the six shapes it was, purely so `CleanCheck` names the right
  evidence; the exported flag stays the posture every other package reads.
- **A herdr error is classified by its code, never by its text (#144).**
  `cliError`'s message embeds the argv, and the argv carries the user's
  prompt, title and branch, so text that mentions a code is not that code:
  a prompt about `agent_pane_busy` once bought itself a resend. `herdrc`
  parses `error.code` from the stderr envelope and exposes each code a
  caller branches on as a sentinel (`ErrPaneBusy`, `ErrAgentNameTaken`,
  `ErrAgentNotReady`) that `cliError.Is` matches, so `errors.Is` finds it
  through any `%w` wrap and an error that is not herdr's never matches.
  A new code worth branching on gets a sentinel in `codeSentinels`, not a
  `strings.Contains`. Test fakes return a small type whose `Is` names the
  sentinel (`herdrErr` in `plan`, `codedErr` in `app` and `create`) —
  a fake whose text merely contains the code now proves nothing.
- **Citations into herdr's source name a pinned ref, never a local
  path.** The pin is the herdr release this plugin's floor names
  (`min_herdr_version` in `herdr-plugin.toml`), tagged `v<version>` — the
  key has no `v` and herdr's tags do, so `blob/X.Y.Z/` is a 404. It is
  written down — tag, commit SHA, distance from the first pin — in exactly
  one place: `CONTRIBUTING.md`'s "Spec citations". Read it there and bump
  it there; do not restate it here. Two spellings: the
  `herdr:src/cli.rs at <pin>` shorthand for a passing reference, and a
  full `https://github.com/herdrdev/herdr/blob/<pin>/...#L123` URL where a
  reader will actually want to go look. A citation anchored to **any ref
  other than the current pin** is **legacy** — still a valid statement
  about *that* ref, not necessarily about the herdr this plugin runs
  against — and every citation of the current pin becomes one the moment
  the floor moves. The first and most common example is
  `b1ff4582e9688f52ffb943cfa8bee4871ae122e4` (`herdr:src/cli.rs` with no
  ref, or `blob/b1ff4582/`): the first pin, which differs from the pin
  that replaced it in the prompt-wait semantics several comments depend
  on. Migrate a legacy citation to the current pin, re-reading the cited
  lines at the new ref, when you touch the code around it; do not write a
  new one. Until 2026-09-16 this bullet named `b1ff4582` as the only pin,
  which by then was false about the tree (24 lines in `*.go` citing the
  legacy commit against 15 citing `v0.9.0` — Go files only; a repo-wide
  grep also counts the prose, this bullet included). Eleven comments used
  to cite `/home/zvi/Projects/herdr/...`, a tree nobody else has — the
  claims were correct and unverifiable at the same time, which is the
  worst of both. A citation that only resolves on one laptop is not a
  citation.
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
  control behind it — a row, or a control in a row's panel like the prompt's
  `keep · reap` (reap spec) — and `equivalence_test.go` is what would break
  first.
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
- **Quitting the popup kills the work it started, and cancelling is only
  half of that.** The commit-time account pick is the one picker call that
  is not a dry run, so it is the one that writes the picker's ledger, and
  `esc` used to quit while leaving it running — an account recorded for a
  session that never existed (#211). It, the `--dry-run` preview and the
  lane's `git rev-parse` therefore run on `app.Lifetime`'s context
  (`lt.Begin()`, not `context.Background()`), which `cmd/herdr-draft`'s
  `runProgram` cancels once the `tea.Program` has returned. The half that
  is easy to leave out is the wait after it: `exec.CommandContext` kills
  the child from a watcher goroutine, so a process that cancels and exits
  in the same breath can exit first and leave the picker running anyway.
  `Lifetime.Shutdown` waits, bounded, and the bound is sized for the KILL
  rather than for the call — measured 2026-09-20, cancel to a dead picker
  is single-digit milliseconds (worst of fifty runs 3.8ms, and 4.8ms on a
  second pass, so read it as an order of magnitude rather than a number),
  while cancel to `exec.Cmd.Wait` returning is `picker.pickerWaitDelay`
  almost exactly, ~2s, because the picker's orphaned children inherit its
  stdout pipe. `Begin` and `Shutdown` are mutex-guarded, and that is not
  tidiness: bubbletea does not wait for its `Cmd` goroutines before `Run`
  returns, so a late `Begin` is ordinary, and a `sync.WaitGroup` `Add`
  overlapping a `Wait` is misuse — it panicked the quit path in review.
  Anything new that shells out from a `tea.Cmd` belongs on the same
  context — which since #293 includes the Linear key resolve
  (`resolveKeyCmd`, so `esc` during an `op read` approval kills the helper)
  and `reloadClauthCmd`, both of which this bullet used to list among the
  "reads nobody has needed cancelled". `refreshLinearCmd` stays on
  `context.Background()`: it is an in-process HTTP request, and nothing of
  it outlives the process. Since #202 the four checks a submit waits for are on it too —
  the dir check's `RepoRoot`/`PrimaryCheckout`, the base settle, the
  title-dup `BranchExists`, and the lane's commit read, which was already
  there — through `awaitCheck`, which runs each one under the Lifetime's
  context with the deadline on top, so that a quit and a check giving up
  are the same cancellation from the model's side. Until 2026-09-20 this bullet
  listed the first three as "reads nobody has needed cancelled"; #202 is
  what needed them cancelled, and the sentence was true only until
  something did. What is still on `context.Background()` in `internal/app`
  is not one kind: Bootstrap's own startup reads (nothing has started that
  a quit could cancel yet), `fetch --prune` (which carries
  `fetchPruneTimeout` instead), the base list and the Linear fetch are reads
  nobody has needed cancelled; `runSubmitCmd`, `plan.CleanCheck` and
  `plan.Clean` are the creation and teardown pipeline, and abandoning one
  of those halfway is a worse outcome than letting it finish — which is the
  same line `create` draws (#252): its pre-flight runs on the caller's
  context, `plan.Execute` gets `context.WithoutCancel` of it, and
  `--dry-run` has always stopped at that exact boundary.
- **`create` answers a signal; the popup does not have to.** bubbletea
  already notifies on `SIGINT`/`SIGTERM` and turns them into
  `InterruptMsg`/`QuitMsg` (v2.0.9 `tea.go`), so the popup reaches
  `runProgram`'s teardown the ordinary way — measured, and closing the
  pane mid-pick kills both the popup and the picker too. `create` had no
  such thing and left the picker running (#252). `main.watchSignals` is
  the answer: announce, cancel, wait `shutdownGrace` for the kill to
  land, publish, then **re-raise the same signal**. Re-raising is what
  keeps `create`'s documented exit codes honest — 2 means "fix your
  invocation", and a create killed by a supervisor that reported 2 would
  be lying to it.
  **The half that is easy to leave out is the handshake**, and it was
  left out first time round: cancelling makes the very next pre-flight
  step fail in *microseconds*, so `create` returns its own code and main
  reaches `os.Exit` about a millisecond later — before the grace is
  spent and before the re-raise runs. Measured at 1ms; the first
  smoke run missed it only because its stub picker forked a `sleep` that
  held the exit open past the grace, so the re-raise won by accident. A
  picker that is a single process gave exit **2** for a `kill`.
  `exitAfterSignal` is the fix: main waits on `signalWatch.begun`/`acted`
  and reports 128+n. The `die` still matters for the window a handshake
  cannot reach — inside `plan.Execute`, main is in `create.Run` for as
  long as the plan takes.
  Two things this rules out. A handler that simply stayed out of
  `plan.Execute`'s way would make a create mid-creation immune to
  `SIGTERM` until the plan finished, which is worse than the bug. And
  `signal.Notify` **overrides an inherited `SIG_IGN`** — a POSIX shell
  sets that for a background job's `SIGINT` — so the signals are filtered
  through `notIgnored` first, or `create &` would start answering a
  ctrl+c it had been told to sit out.
- **A check that holds a submit is bounded, and unknown is not invalid
  (#202).** `handleSubmit` refuses to build a plan while the project row's
  check, the base settle or the title-duplicate check is out, and the
  lane's commit read stops the submit dead after validation. None of the
  four could time out, so a stalled mount made `⌃S` a dead key: it waited,
  silently, for good. All four now run through `checkBudget` and
  `awaitCheck`, which bound **the answer** rather than the calls —
  deliberately, because `os.Stat` and `gitx.IsGitRepo` take no context at
  all, and because a process blocked on a stalled network mount is in
  uninterruptible sleep, where a cancelled context returns nothing sooner
  than no context would. The price is a goroutine left on a call that may
  never come back; it answers into a buffered channel nobody reads, so it
  costs no late message and the existing version guards never see one. That
  goroutine, not the `tea.Cmd`, is what holds the `Lifetime` region, and
  that is load-bearing rather than tidy: `Shutdown`'s wait exists so a
  cancelled child is dead before the process exits, and `awaitCheck`
  returns the moment the deadline fires with the child still running.
  What that goroutine must **not** hold is the `cancel`. `select` picks
  uniformly at random among the cases that are ready, so cancelling from
  the answering goroutine's own defer makes every answer expire its own
  context and turns "the answer and the deadline are both ready" from a
  boundary case into the common one — a coin flip, per check, between the
  answer and a refusal. It shipped once, and the thing to learn from how
  it was caught is what could not catch it: `go test -race` is blind to
  it, because two ready channel operations are not unsynchronised memory.
  One CI runner went red and the other stayed green. The
  `ctx.Done()` branch also peeks at the answer before giving up, for the
  boundary that remains.
  Two rules follow for anything new that holds a submit. It gets the same
  bound — and since #293 "from the same two helpers" is a default rather
  than the rule. The two **account holds** (draw-first spec §7.1, ruling 15)
  take `checkBudget()`'s deadline and a plain timer, because there is no
  call to hand `awaitCheck`: the answer they wait for is already in flight
  as a message, the clauth landing or the probed preview. They are the
  documented exception, and the exception is narrow — a hold with a call
  behind it still goes through `awaitCheck`. The other half of that
  convention does NOT bend: those two are armed **once per ⌃S**
  (`accountWaitArmed`, `reqs.accountWait`), because a timer re-armed by
  every re-entry pushes the deadline back and the budget is never spent.
  And what a spent budget *means* is per hold rather than universal.
  #202's three refuse. The **clauth** hold proceeds, without #243's
  dead-credential check, under what the row would have selected from config
  alone (`accountFromConfig`) — clauth is advisory, as `create` has taken
  it since #141, and a create refused because a status file was stale would
  be refusing on no evidence at all. The **probe** hold refuses, because
  the thing it could not check is whether an executable may be run, and
  launching unpinned under whatever account is live is exactly what
  `handlePickerCommit` exists to prevent. And its timeout is reported as
  **unknown**, never as the verdict's negative: `dirUnknown`,
  `titleDupUnknown` and `baseUnknown` refuse the submit exactly as
  `dirInvalid` and `titleDupBlocked` do, and say something else while doing
  it, because "we could not check" and "we checked, and no" are only the
  same sentence to whoever wrote the code. Note which of the three is not a
  flag. `baseUnknown` compares a stored REF against the base in play, the
  way `DirField.SetValidity` compares its `validityPath` against `Value()`,
  because the base check is the one that can decline to ask: with the
  resolution supplying no base, or the user having picked their own,
  `scheduleBaseSettle` returns no message at all. A flag cleared only where
  an answer lands therefore latched — and the line it writes says "pick a
  base", which sets `baseTouched`, which is exactly the condition under
  which no answer is coming, so the submit was refused for the life of the
  popup. A verdict about a value belongs to that value; the other two are
  flags only because their pipelines always answer.
- **App-layer state is diffed, not event-driven.** `form.Model` exposes no
  "section X changed" signal; `Model.reactToChanges` (in `app.go`) compares
  each relevant getter against a last-observed snapshot after every routed
  message and schedules debounced async work (`async.go`) on a real change.
  New reactive behavior tied to a field's value generally belongs there,
  following the same before/after-comparison pattern each field's own
  `Update` already uses internally. Touched-versus-preselected flags need
  their **own** snapshot fields: `syncDerivedInertness` resyncs its own on
  every call, so sharing one silently stops per-project memory re-applying
  after the first project change. And a touched-versus-preselected snapshot
  records what the app **asked for**, not what the field is showing — #248,
  which is the same failure from the other side. A field may defer what it
  was told: `WorktreeField.SetBase` holds a ref no candidate list names yet
  and reads as the `HEAD` row meanwhile, so `snapshotAppliedDefaults`
  recording `Base()` recorded `""` for a base the app had already applied,
  and the list landing it a moment later read as the user moving it. Hence
  the pair `Base()` (what is on screen, for the row, the panel and the plan)
  and `RequestedBase()` (what the app put there, for the diff alone). A new
  deferring setter needs the same pair, and note that the getter has to come
  off the field's own **concrete** type — the `Section` interface
  deliberately exposes none of this.
- **A golden-frame suite proves only the states someone thought to
  fixture.** The v2 form shipped a defect in its *opening* state — the
  first thing every user sees, a worktree row naming a branch called
  "branch name" — through fifteen green commits, because every fixture had
  a title already typed. `go test ./...` passing is not evidence the screen
  is right. Build it and run it (`docs/manual-smoke.md`'s Route B runs the
  real form in an ordinary pane, no popup needed; `just live` runs the form
  under a pty with nothing real behind it and prints the screen at any
  size you name), and when a change moves a state no frame pins, add the
  frame.
- **Frames record bytes, not perceptibility**, which is the same lesson one
  layer down and the one v3 exists for: v2 drew its rules and its focused-row
  fill at a contrast ratio of **1.07:1**, correctly, at the right width, and
  invisibly. A green fixture says nothing about whether a colour can be seen.
  Anything that marks a region — a rule, a fill, an input background, a
  picker's cursor row, a secondary button's face — needs a **measured** floor
  against the ground it is actually drawn on, over all eighteen builtins, in
  `internal/theme/contrast_test.go` beside `ActiveRowContrastFloor`,
  `InputFillContrastFloor` and `SurfaceFillContrastFloor`. Note "the ground
  it is actually drawn on": an input is only rendered while its field is
  focused, so it sits on `ActiveRowBG`, not `PanelBG` — and on the default
  theme those two happen to be byte-identical to `Surface`, which is how a
  flat-`Surface` input fill would have shipped invisible a second time. The
  same trap took the last two regions one field over (#149): `Surface` on
  `PanelBG` is under 1.25:1 on **twelve of the seventeen** measurable
  builtins and 1.40:1 on catppuccin, so twelve themes shipped a highlighted
  picker row with no band and a cancel button with no face.
  **A colour that carries a word needs the same thing, and it is a second
  class rather than the same one** (#273, #277). Those four floors are 1.25:1
  and 1.6:1, which is right for an edge the eye has to catch and nowhere
  near legible; `SemanticTextContrastFloor` is 3:1 and covers five fields —
  `Danger`, `Warning`, `Success`, `Branch` and `Accent` — each the *only*
  rendering of some word, so "the words already say what they mean" is true
  and does not help. **3:1 rather than WCAG's 4.5:1 body-text figure is a
  measurement rather than a concession**, and `Branch` is where that was
  settled, since a branch name is content read character by character and so
  the hardest case for a large-text figure: no builtin's own `Text` falls
  below 3:1 on any ground, while three fall below 4.5:1 — solarized-light at
  3.14:1 on its focused row, tokyo-night-day 3.51, solarized 4.29. 3:1
  therefore sits under every theme's own body text without reaching any of
  it, which is where a floor belongs, while 4.5:1 would hold a branch name to
  a higher standard than the title beside it and walk 11 of the 17 branch
  colours, one of them 96 units in sRGB. A word reaches **three** grounds,
  not two — `PanelBG`, `ActiveRowBG`, and the `SurfaceFill(PanelBG)` a picker
  repaints its cursor row with — and which of them is worst is the theme's
  business, not the form's: `ActiveRowBG` is worst on most builtins and
  dracula clears 3:1 on both of the others while measuring 2.91:1 on the
  cursor row. Name that third ground by the expression and not by `Surface`:
  dracula is one of the five builtins whose `Surface` is *not* raised, so
  the figure is right under a name that stopped being right. Two things
  follow for a clamp on a foreground. It walks toward **black or white**,
  not toward `Text` the way `ensureContrast` does: a theme's `Text` is
  often a desaturated grey, and walking solarized's red and orange toward
  its `#839496` arrived at two browns 24 apart, which is the convergence
  such a clamp is accused of.
  And `floorContrast` raises `ActiveRowBG` **before** it uses it as a
  ground, because a word measured against the raw `selection_bg` is
  measured against a fill four builtins never draw — and it computes the
  cursor fill rather than reading `Surface` for the same reason, because raw
  `Surface` is a fill twelve of them never draw.
  **The dim tier is the same class with a ceiling** (#299).
  `DimTextContrastFloor` is 3:1 on the same three grounds, and
  `raiseDimText` never walks `DimText` past `Text`: a dim tier raised above
  the bright one has not been fixed, it has been swapped with it. Measure
  that as signed luminance, not sRGB distance, which has no direction —
  the semantic clamp at 4.5:1 moves solarized-light's labels *further* from
  its `Text` (43.4 → 45.5) by walking them past it. The most a dim tier can
  differ from `Text` while clearing a floor is `Text`'s worst ratio divided
  by the floor. On solarized-light that is 1.046:1, which is why that walk
  steps at 1%: a 5% step jumps the whole window. The ceiling is relative
  (it stops the walk crossing `Text`, and lowers nothing), because
  kanagawa-lotus ships a `DimText` louder than its `Text` (#309).
- **A change that moves a shared value can hand another test a SECOND
  reason to pass, and none of the signals you normally trust will say so.**
  #149 repointed the third ground the semantic-text floor measures words on,
  from the `Surface` field to the fill a picker actually paints.
  `TestFloorContrast_MeasuresAgainstTheRaisedActiveRowBG` stayed green and
  should not have: its fixture's `Surface` *was* its `PanelBG`, so the new
  expression raised it to a value on which that fixture's own `Danger`
  measured 2.505:1. The raise the test attributes to `ActiveRowBG` now had a
  second cause, so it would have passed under exactly the wrong ordering it
  exists to catch. A green suite conceals this, `-race` is silent about it,
  and the diff of the file containing the test is empty. So after moving a
  value other tests measure against, re-derive their **premises** rather than
  re-running them, and where a test says "X is the reason", give it a guard
  asserting X is the **only** reason — a loop over the other inputs
  asserting each one passes. That guard is three lines and it fails loudly
  the next time somebody moves a ground.
  The other half is that the moved value needs a fixture of its own.
  Reverting that one expression left all seventeen packages green, because
  over the eighteen builtins the old and new ground lists happen to agree on
  every outcome — and they only happen to, since `floorContrast` also runs
  on a `[palette]` override and a custom herdr theme, where nothing
  constrains them to. Expect the discriminating fixture to be awkward: both
  candidate grounds here are "the panel walked toward `Text`", so they
  coincide whenever `ActiveRowBG` needs raising at all, and separating them
  took a palette whose focused row is *darker* than its panel, which no real
  theme is (`TestFloorContrast_MeasuresAgainstThePaintedCursorFill`).
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
