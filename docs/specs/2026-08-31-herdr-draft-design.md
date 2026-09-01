# herdr-draft — new session creation dialog for herdr

- **Date:** 2026-08-31
- **Status:** draft for review
- **Repo:** `~/Projects/herdr-draft` (this repo)
- **License:** MIT (see §14 — Licensing & code provenance)

## 1. Summary

herdr-draft is a herdr plugin that provides an Atrium-style "new session" dialog:
a single-screen form, opened in a herdr popup, that creates a fully configured
agent session in one submit — Linear issue, project, git worktree (branch +
base), placement (space / tab / split), agent kind, Claude account (via clauth),
and an initial prompt.

It is a standalone Go + Bubble Tea binary. herdr neither knows nor cares about
the implementation language; the plugin drives herdr exclusively through the
public CLI (`$HERDR_BIN_PATH`; the CLI itself reaches the server over the
socket at `$HERDR_SOCKET_PATH`).

## 2. Background & ecosystem context

Atrium (the author's previous TUI, now abandoned in favor of herdr) had a
new-session creation form whose UX this plugin reproduces and adapts. An
ecosystem survey (2026-08-31, amended after adversarial review) found no
existing tool that combines agent kind + account + worktree + placement in one
creation form, though several own pieces of it:

- `cloudmanic/herdr-plus` (Go) — workspace templates + fuzzy picker + worktree
  open with branch prompt. No agent form, no accounts.
- `steig/worktender` (Go) — GitHub issue → worktree → workspace → briefed
  agent, non-interactive.
- `andrewchng/herdr-sessionizer` (TS) — fzf-style project/worktree launcher.
- `tdi/herdr-worktree-from-linear` (JS) — picker over assigned Linear issues →
  worktree on the issue's `branchName`, opened as a workspace. The closest
  Linear-flow prior art.
- `talent-factory/herdr-linear` (Rust) — Linear issues panel with
  implement-on-Enter.
- `JLighter/herdr-spawn` (shell) — popup form (prompt, branch, agent kind,
  worktree-per-agent); proves the form shape is wanted, far from Atrium-grade.
- clauth ships its own herdr plugin (project wiki) — account dashboard popup
  and a per-pane `$clauth` account tag via pane metadata; deliberately no
  launch-time picker.
- Other account-adjacent plugins display usage only.

The honest composition alternative: `tdi/herdr-worktree-from-linear` plus a
herdr-plus auto-layout (whose command can be `clauth start <profile>`) plus
clauth's own dashboard delivers roughly 60–70% of this plugin's value with no
new code. What no composition delivers — one form with editable
title/branch/base, a prompt briefing composed from the issue, and per-launch
account choice — is exactly herdr-draft's scope.

Upstream herdr has demand signals but no plans: discussions #2755 (richer
worktree dialog), #2542/#3224 (placement), #3375 (cleanup after failed agent
starts), and #3228 (custom resume command, filed by clauth's author) — the
last one is the known gap this plugin inherits (§16).

herdr's own creation surfaces are primitives-only: the TUI's worktree dialog
collects a branch name; `agent start` requires an existing shell pane. All the
data ingredients exist in the CLI/API; nothing composes them. Plugin popups are
the sanctioned extension path ("the entire Herdr CLI is the plugin API";
runtime action registration and native plugin UI are explicitly not in plugin
v1).

## 3. Goals / non-goals

**Goals (v1)**

1. One keybinding opens the form; one submit produces a running, briefed agent
   in the right place.
2. Linear-seeded flow: pick an assigned issue → title, branch
   (`branchName`), and prompt pre-filled. Manual flow equally first-class.
3. clauth-backed account selection for Claude sessions.
4. herdr-native look: match the user's herdr palette and dialog conventions
   (§7). Full mouse support — herdr is mouse-first.
5. Atrium's interaction quality: focus ring, debounced+versioned async,
   inline verdicts, present-but-inert fields, width-budgeted strings.
6. Config-driven; nothing Quantivly- or user-specific hardcoded.

**Non-goals (v1)** — see §16 for the full future-work list: variant fan-out,
draft persistence, Linear writes, model/effort/permission chip fields,
marketplace publication, fixing the resume/account-binding gap (#3228).

## 4. Architecture overview

```
┌ herdr (popup frame: native border, title, user palette) ─┐
│  herdr-draft binary (Go + Bubble Tea, PTY)                    │
│  ┌ form (dumb view) ──────────────┐                      │
│  │ fields, focus ring, verdicts   │                      │
│  └────────────▲───────────────────┘                      │
│        setters│                                          │
│  ┌ app layer ─┴───────────────────┐                      │
│  │ async, debounced, versioned    │                      │
│  └─┬─────────┬─────────┬──────────┘                      │
└────│─────────│─────────│─────────────────────────────────┘
     │         │         │
  herdr CLI  Linear    clauth status --json / git subprocess
             GraphQL
```

Atrium's load-bearing architectural rule carries over verbatim: **the form is a
dumb view**. It never does I/O; every verdict, candidate list, and hint is
computed in the app layer and pushed in via setters. All async results carry
versions/keys and stale results are rejected.

## 5. Plugin packaging & invocation

`herdr-plugin.toml`:

- `[[build]]` — `go build -o bin/herdr-draft ./cmd/herdr-draft`.
- `[[panes]]` — id `draft`, title `New session`, `placement = "popup"`,
  `width = "80%"`, `height = "80%"`, command `["bin/herdr-draft"]`.
- `[[actions]]` — id `open`, context `global`, so
  `herdr plugin action invoke` works and a keybinding can target it.

The user binds a key in herdr config via `[[keys.command]]` with
`type = "plugin_action"`. Documented in README (plugin actions do not appear in
herdr's context/global menus — keybinding and CLI are the only discovery
surfaces in plugin v1).

At startup the binary reads:

- `HERDR_PLUGIN_CONTEXT_JSON` — current workspace id/label/cwd, worktree info,
  tab, focused pane (id/cwd/agent/status).
- `HERDR_PLUGIN_CONFIG_DIR` / `HERDR_PLUGIN_STATE_DIR` — §12.
- `HERDR_BIN_PATH`, `HERDR_SOCKET_PATH` — herdr access.

Accepted platform limits: popup returns `ui_busy` while a herdr modal is open;
popup chrome inside the frame is ours to draw (§7); Escape reaches the popup
(herdr popups receive all terminal input).

## 6. The form

Single-screen modal form (not a wizard). Interaction grammar ported from
Atrium:

- Focus ring over the sections present; `Tab` = complete-then-advance in
  pickers, plain advance elsewhere; `Shift+Tab` back; both skip inert stops
  with wrap-around.
- `↑/↓` move within the focused widget; `Enter` advances, submits from a
  non-empty Title and from Create; `⌃S` submits from anywhere; `Esc`/`⌃C`
  cancel; `⌃R ⌃R` double-tap clears; `⌃J`/`⇧↵`/`⌥↵` newline in the prompt.
- Paste routed separately from keys (a clipboard containing "esc" must not
  cancel the form).
- Each section declares a PREFERRED height and a MINIMUM height; the form
  allocates the window's row budget between them, giving the focused
  section its preferred height and shedding the others toward their
  minimum when the budget is short. Layout is therefore stable for a given
  (window size, focused section) pair — not for window size alone.

  *Amended 2026-09-01, from "constant-height sections for a given window
  size".* Atrium's form owned a full-height screen; herdr-draft's owns an
  80%-of-terminal popup, which on an 80×24 terminal is ~19 usable rows
  across up to 10 sections. A genuinely constant layout gives every
  section ~2 rows, so no picker — Project, Base, Linear issue, Agent,
  Account — could ever display a single candidate row: 5 of 10 fields
  would be unusable at the smallest supported size. Measured behavior
  after the change: moving focus shifts a section's start by 0 to −6 rows
  at h=24, always upward or unchanged, with the key-hint footer and the
  Create button pinned at h−2/h−1 so the two fixed reference points never
  move.

- Strings width-budgeted with hint ladders; graceful degradation order:
  truncate → drop blanks → drop dividers → drop heading → clip tail but
  never the Create button.
- Fields whose applicability can change **while the form is open** (non-git
  target, non-claude agent) go **present-but-inert** with an explanatory
  placeholder, never absent, so a field never vanishes from under the user
  (its rows may shrink toward its minimum when another section takes
  focus — see the height bullet above — but the field itself stays on
  screen and stays reachable in the focus ring). Fields
  whose precondition is static at startup (Linear unconfigured, fewer than two
  clauth profiles) are simply not rendered.

### Field order and behavior

1. **Linear issue** *(optional; rendered only when Linear is configured)* —
   type-to-filter picker over the viewer's assigned issues (state type
   `unstarted` + `started`), showing identifier, title, status, estimate,
   priority. Selecting seeds: Title ← issue title, Branch ← `branchName`,
   Prompt ← template (§10). `none` = manual mode. Seeding follows Atrium's
   **touched-vs-preselected** rule: programmatic seeds never mark a field
   touched; the first user edit does, and later seeding stops clobbering it.
2. **Project** — Atrium's dual-mode directory picker: fragment = fuzzy filter
   over candidates; `/`, `~`, `.` prefix = filesystem browsing with a
   literal-path fallback entry. Candidates: current context cwd → open herdr
   workspace cwds/repo roots (`herdr workspace list`) → recents (state dir).
   Inline validity marker: `(invalid)` non-directory, `(direct)` non-git-repo,
   computed async + debounced (150 ms). Default: current space's repo root.
3. **Title** — required. Cap 32 runes. In manual mode, **choosing a title is
   choosing a branch**: branch = sanitize(`branch_prefix` + title), hash
   fallback when the slug sanitizes to nothing. In Linear mode `branchName`
   owns the branch and the title is free text. Live duplicate verdicts
   (bounded width): existing local/remote branch (`git show-ref`), herdr
   workspace label collision. Verdicts never disable submit; a failing submit
   re-focuses Title.
4. **Worktree** — chips `worktree` / `in place` (default from config; inert
   for non-git targets with distinct placeholders). When on: editable Branch
   field (seeded per above) and **Base picker** — row 0 `HEAD (<current
   branch>)`, then `git branch -a --sort=-committerdate`, deduped across
   `origin/`, capped at 50, debounced 150 ms, versioned. A background
   `git fetch --prune` fires once per repo per form-open.
5. **Placement** — chips `new space` / `tab here` / `split here`. Active only
   when Worktree is off (a worktree is always its own linked-worktree space in
   herdr's model); inert with hint `worktree opens as its own space` otherwise.
6. **Agent** — chip row of configured favorites (default `claude`); full kind
   list (herdr's 23) reachable behind the row. Per-kind extra args from config
   are appended at launch.
7. **Account** — rendered only when clauth is configured and ≥2 profiles
   exist (static); when rendered, inert unless the selected agent kind is
   `claude` (dynamic). Entries
   from `clauth status --json`: `active` (don't pin; use whatever profile is
   live) plus one entry per profile showing name, tier, `auth_status`, and
   5h/7d window utilization. Rate-limited or auth-failed profiles are
   selectable but visibly marked; the exhausted-confirm modal is deferred to
   future work (§16).
8. **Prompt** — optional textarea (4 rows preferred, 1 floor). Placeholder
   ladder: `Optional — sent to the agent once it starts (Enter or Tab to
   skip)` down to `Optional`. Delivered post-launch via
   `herdr agent prompt --wait` (§9).
9. **Create** — last focus stop, always enabled, never clipped.

## 7. herdr-native skin & mouse

herdr draws the popup's outer chrome natively — accent border, title, panel
background in the user's palette (`herdr:src/ui/panes.rs` `render_popup_pane`).
Inside it, the form must not look foreign:

- **Palette**: replicate herdr's theme resolution best-effort. The built-in
  palettes live in `herdr:src/app/state.rs` (`Palette::from_name`, ~line 562;
  `theme.rs` holds only the config structs and color parsing) — translate all
  of them, plus name aliases. Selection: `[theme] name` from herdr's own
  config, then `[theme.custom]` overrides, then herdr-draft's own `[palette]`
  config table as the user-facing escape hatch. `auto_switch` and
  `name = "terminal"` are unknowable from config alone: resolve to the
  configured dark variant and document the limitation. Pixel parity is NOT a
  v1 gate. If nothing can be read, fall back to herdr's default palette,
  never to an invented scheme.
- **Conventions** imitated from herdr's dialog code (`src/ui/dialogs.rs`,
  `src/ui/widgets.rs`): header treatment, `✓` selection markers in choice
  lists, action-button row styling, accent-colored focus indication, panel
  background fill (explicitly painted — do not rely on terminal-default bg
  mapping inside the popup PTY).
- **Mouse (v1, not deferred)**: click to focus any field; click to activate
  chips, list rows, and the Create button; wheel scrolls pickers and the
  prompt textarea. Implemented with bubblezone hit-testing. Keyboard remains
  fully sufficient (Atrium grammar unchanged).

## 8. Data sources & async model

| Source | Access | When |
|---|---|---|
| herdr context | `HERDR_PLUGIN_CONTEXT_JSON` | startup |
| herdr workspaces/panes | `herdr workspace list`, `herdr pane list` (JSON) | form-open |
| Linear issues | GraphQL over HTTPS (§10) | form-open, async; cache-first |
| clauth profiles | `clauth status --json`; prefer fresh `~/.clauth/status.json` (daemon feed, supported integration surface) | form-open + on account focus |
| git branches / show-ref / repo checks | `git` subprocess in target repo | debounced per keystroke class |

All request/response pairs carry keys (filter version, `(title,path)`,
`path`, cache generation); stale responses are dropped. Nothing blocks the
render loop; every remote fetch has a visible `searching…` / `couldn't
list` header state in its widget.

## 9. Submit pipeline

Pre-open refusal (before the form renders): herdr socket unreachable →
plain-text error and exit.

Submit-time validation (inline verdicts, submit blocked, focus moved):
directory validity; branch/label duplicates; pinned account
`auth_status != ok` → blocking verdict on the account field; prompt required
only if config demands it (default: optional).

Staged creation, with per-step progress lines rendered in the popup
(`creating worktree… ✓` / `starting claude… ✗ <error>`):

**Step 1 — topology** (herdr CLI; all creation output is JSON, IDs parsed
from it):

> *Note, 2026-09-01:* `--trust-repository` is **blocked upstream**, not
> merely unimplemented. herdr added the flag to `worktree create` in
> commit `095f1337` ("fix: trust worktree repositories per request",
> #3344, 2026-08-28), which is on herdr `master` and in no release —
> herdr 0.8.2, this plugin's `min_herdr_version`, answers
> `unknown option: --trust-repository`. Passing it today would break
> worktree creation outright. The `[worktree] trust_repository` config key
> is therefore deliberately absent rather than inert; wire it when a herdr
> release contains `095f1337`, and raise `min_herdr_version` in the same
> change.

- Worktree on: `herdr worktree create --cwd <project> --branch <b>
  --base <ref> --label <title> --focus [--trust-repository per config]` →
  new workspace id + its initial tab/pane.
- Worktree off: per placement — `herdr workspace create --cwd --label
  --focus`, `herdr tab create --workspace <ctx> --cwd --label --focus`, or
  `herdr pane split --cwd --focus` (env omitted; unused in v1 paths).

**Step 2 — launch** (two paths):

- *Path A — no account pin, or non-claude kind:*
  `herdr agent start <name> --kind <k> --pane <pane_id> -- <extra args>`.
  Name = title slug (deduped; `DuplicateName` retried with suffix).
  `agent start` types the composed command into the shell pane and waits for
  detection (its own timeout honored).
- *Path B — pinned clauth profile (claude only):*
  `herdr pane run <pane_id> clauth start <profile> -- <extra args…>` types the
  wrapper command into the step-1 shell pane and submits it atomically
  (send-text + Enter; `herdr:src/cli/spec.rs` `pane run`). herdr's screen
  detection recognizes Claude Code through the wrapper — detection scans the
  entire foreground job (`herdr:src/detect/mod.rs` `identify_agent_in_job`).
  herdr-draft then waits for detection by polling `herdr agent get <pane_id>`
  with a configurable timeout (default 30 s).

Both paths retry a launch rejected with error code `agent_pane_busy` (the
shell still starting right after step 1 — the exact failure mode behind
upstream #3375; `herdr:src/app/agents.rs:255`) every 500 ms for up to 5 s
before surfacing failure.

**Step 3 — prompt** (when non-empty): wait for a detected settled state, then
`herdr agent prompt <target> <text> --wait --timeout <ms>`. Handle documented
failure modes explicitly: `agent_blocked` (report, leave session running),
`agent_prompt_stalled`, `timeout` (report; session stays up, prompt text
surfaced back to the user for manual paste).

**Failure handling after step 1 succeeded**: keep-or-clean choice in the
popup. *Keep* leaves the created space as a plain shell. *Clean* runs
`herdr worktree remove` / `herdr workspace close` — worktree removal only
when the checkout is clean and has no commits beyond base; otherwise the
clean option is disabled with the reason shown. Never silent, never `--force`.
(Upstream #3375 documents exactly this failure mess; herdr-draft must not add to
it.)

## 10. Linear integration (read-only)

- **Auth**: personal API key, resolved in order: `api_key_cmd` (e.g.
  `pass show linear`), `LINEAR_API_KEY` env, `api_key` config value. Absent →
  the Linear field is not rendered; everything else works.
- **Query** (single request, form-open):

  ```graphql
  { viewer { assignedIssues(
      filter: { state: { type: { in: ["unstarted", "started"] } } },
      orderBy: updatedAt, first: 50
    ) { nodes {
        identifier title branchName url description
        state { name type } estimate priority cycle { number }
  } } } }
  ```

- **Cache**: last response in state dir, rendered instantly at form-open with
  an async refresh (TTL ~5 min); filtering is client-side.
- **Seeding template** (config-overridable), default:

  ```
  Work on {identifier}: {title}

  {url}

  {description}
  ```

- **Deliberately no writes.** No comments, no status changes: the GitHub
  integration flips In Progress on branch push and In Review/Done on PR
  events; manual flips would fight it. This is a design decision, not a
  deferral.

## 11. clauth integration

- **Read**: `clauth status --json` (integer `schema` field, currently `1`;
  per-profile
  `name`, `active`, `provider`, `tier`, `has_live_session`, `auth_status`,
  `fallback`, usage `windows[]` with `label`/`utilization_pct`/`resets_at`).
  Prefer reading the daemon's `~/.clauth/status.json` when fresh (clauth
  documents it as a feed for other apps); fall back to invoking the CLI.
  `schema != 1` → degrade to name-only entries, never crash.
- **Launch**: `clauth start <profile> -- <claude args>` via `pane run`
  (§9 Path B). clauth owns the per-profile `CLAUDE_CONFIG_DIR` mirror; herdr-draft
  never touches `~/.clauth` internals beyond the documented status feed.
  `--with-fallback` / `--isolated` are not v1 form options (config may append
  them via per-kind extra args at the user's own risk).
- **Interop**: clauth's own herdr plugin owns the account dashboard and the
  per-pane `$clauth` metadata tag; herdr-draft must not publish competing pane
  metadata under that token. Launch-time pinning and the dashboard are
  complementary.
- **Known gap, accepted for v1**: on herdr session restore, herdr resumes
  `claude --resume <id>` without the clauth wrapper, dropping the account
  binding. Upstream discussion #3228 proposes per-agent resume templates
  (`clauth info <id>` already prints the exact resume command). Documented in
  README; nothing herdr-draft can fix locally.

## 12. Configuration & state

`$HERDR_PLUGIN_CONFIG_DIR/config.toml` (all keys optional; sane defaults):

```toml
branch_prefix = "zvi/"          # default: lowercased $USER + "/"
default_worktree = true          # for git targets
default_placement = "new-space"  # when worktree is off

[linear]
api_key_cmd = ["pass", "show", "linear-api-key"]
# api_key = "lin_api_..."       # discouraged; file perms checked (0600)
prompt_template = ""             # empty = built-in default (§10)

[clauth]
enabled = true                   # auto-detected if omitted
default = "active"               # or a profile name

[agents]
favorites = ["claude", "codex"]
default = "claude"

[agents.extra_args]
claude = []
codex = []

[timeouts]
detection_ms = 30000
prompt_wait_ms = 120000

[palette]  # optional escape hatch when herdr theme detection is wrong (§7)
# accent = "#89b4fa"
# panel_bg = "#1e1e2e"
```

`$HERDR_PLUGIN_STATE_DIR/`: `recents.json` (recent project paths),
`linear-cache.json`, `last-used.json` (last agent kind, placement, worktree
toggle). All state loss-tolerant: corrupt/missing state files are discarded
silently.

## 13. Error handling

- Every widget owns an inline verdict/status line (bounded width); toasts
  don't exist (they'd hide behind the popup — Atrium lesson).
- All herdr CLI invocations check exit code and parse stderr; errors surface
  in the staged-progress view with the failing command name.
- Linear/clauth/network failures degrade the respective field to inert with a
  reason; they never block manual-mode creation.
- The submit pipeline is the only place with side effects; everything before
  it is read-only.

## 14. Licensing & code provenance

- **herdr-draft license: MIT** (ecosystem norm; adoption-friendly).
- **Atrium is AGPL-3.0 with multiple copyright holders.** The port is gated on
  per-file provenance, verified by `git blame --line-porcelain` (2026-08-31):
  - Clean to port (100% Zvi-authored surviving lines, relicensable by the
    author): `textInput_create.go`, `textInput_focus.go`, `textInput_keys.go`,
    `textInput_render.go`, `textInput_size.go`, `directoryPicker.go`,
    `accountPicker.go`, `accountSelection.go`, `chiprow.go`, `picker.go`, and
    the app-side patterns in `app_session.go` / `app_branchsearch.go`.
  - **Not clean**: `textInput.go` (54 surviving third-party lines of 172) and
    `branchPicker.go` (72 of 396). These two are **reimplemented, not
    ported**: the third-party lines are generic textarea/list plumbing,
    re-derived from the `bubbles` primitives; a blame re-audit of the final
    ported files is a pre-publication checklist item.
- **herdr styling reference**: herdr is Apache-2.0; palette constants and
  dialog conventions translated from its source are attributed in `NOTICE`.

## 15. Testing

- **Unit (pure, no I/O)**: branch derivation/sanitization + hash fallback;
  form-state → creation plan (ordered list of herdr CLI invocations) for
  every path × placement × worktree combination; Linear and clauth
  JSON parsing incl. unknown-schema degradation; verdict logic; width-budget
  ladders.
- **Golden frames**: rendered form at 80×24 and 120×40 (Atrium's
  `testdata/frames` pattern) for: empty form, Linear-seeded, non-git target
  (inert fields), account field with marked profiles, staged-progress,
  failure keep/clean.
- **Fixtures** for all network/subprocess JSON; tests never hit Linear,
  clauth, or a live herdr.
- **Manual smoke** (documented in README, not CI): throwaway named herdr
  session; run the full matrix of Path A/B × worktree on/off once per
  release.
- CI: `gofmt -l`, `go vet`, `go test ./...`.

## 16. Out of scope (v1) / future work

1. Variant fan-out (N sessions per profile) — Atrium's `variantPicker` ports
   cleanly when wanted.
2. Draft persistence across popup close (Atrium's stash-on-Esc).
3. Model / effort / permission-mode chip fields — covered by per-kind extra
   args; Atrium's own retrospective flagged the "Claude-shaped form" as a
   mistake (adapter-declared schemas were its intended fix). Do not re-ship
   that mistake.
4. Linear writes of any kind.
5. Resume/account binding across herdr restore — upstream #3228; support it
   in herdr-draft's README and revisit when herdr grows resume templates.
6. Marketplace publication (manifest metadata, docs) — after the tool has
   proven itself in daily personal use.
7. Prompt-history reuse picker (`↑` on empty prompt).
8. Reading herdr theme changes live (v1 reads at startup only).
9. Account exhausted-confirm modal (Atrium's gate); v1 ships the inline
   rate-limit marker plus a blocking verdict on `auth_status != ok` only.

## 17. Implementation-time validation checklist

Facts assumed above that must be verified against a live herdr before the
relevant milestone is declared done:

Resolved during adversarial review (2026-08-31): `layout.apply` is no longer
used (Path B uses `pane run`; layout.apply with `tab_id` REPLACES the tab —
socket-api.mdx — making it wrong for this use anyway); pane-id targeting of
detected agents is documented (cli-reference.mdx:308); clauth `schema` is the
integer `1`.

- [x] `pane run` launch of `clauth start <profile>` in a fresh worktree pane:
      detection recognizes claude through the wrapper; measure latency
      (affects the 30 s default). **Live-probed 2026-08-31 (task 2b):**
      `herdr pane run <pane> clauth start quantivly-2 --` in a worktree pane
      was detected as `agent: "claude"` within ≤5 s of launch (one poll
      window; single-shot probe, see task-2b-report.md); comfortably inside
      the 30 s default.
      **Fixed and re-validated live 2026-09-01 (task 19 fix round):** task
      2b's probe called the herdr CLI's `pane run` directly, bypassing
      `CLIRunner.PaneRun`; task 19's own first closeout pass (before this
      fix) found `PaneRun` routed through `runJSON`, which requires a JSON
      envelope on stdout that `herdr pane run` never actually prints,
      making every real Path B submission fail at the launch step. Fixed
      by giving `PaneRun` its own exit-code-only run path (`runOK`); a
      full real Path B run afterward showed `launching claude via
      clauth… ✓` and `waiting for agent detection… ✓` in the popup, with
      `herdr pane list` confirming `agent: "claude"`,
      `tokens.clauth: "quantivly-2"` on the launched pane — see
      task-19-report.md's fix section for the full transcript.
- [x] Exact JSON field names in creation responses (`workspace_id`, pane ids)
      across `worktree create` / `workspace create` / `tab create` /
      `pane split`. **Live-probed 2026-08-31 (task 2b):** captured verbatim
      (sanitized) in `internal/herdrc/testdata/live/{worktree_create,
      workspace_create,tab_create,pane_split}.json`; confirmed
      `worktree create` also opens a second, non-worktree workspace for the
      origin repo alongside the linked-worktree workspace (both need
      cleanup).
- [x] herdr user-config location and theme resolution for palette parsing
      (§7); translate the built-in palettes from `src/app/state.rs`
      (`Palette::from_name`, ~line 562) at the pinned herdr version.
      **Live-probed 2026-09-01 (task 19):** the user's real
      `~/.config/herdr/config.toml` has `[theme] name = "tokyo-night"`;
      `herdr pane read <popup-host> --format ansi` on a real popup shows
      `\x1b[48;2;26;27;38m`/`\x1b[38;2;122;162;247m` throughout the form's
      content, matching `internal/theme/palette.go`'s `tokyo-night` entry
      (`PanelBG #1a1b26`, `Accent #7aa2f7`) exactly, byte-for-byte the same
      RGB values herdr's own native border chrome uses in the same capture
      — confirms config location, name resolution, and color translation
      all correct end to end.
- [x] Popup PTY background behavior: whether terminal-default-bg cells render
      as `panel_bg` (paint explicitly regardless).
      **Live-probed 2026-09-01 (task 19):** in the same ANSI capture, every
      interior row — including the blank vertical-padding row directly
      under the top border — carries an explicit `48;2;26;27;38`
      reassertion after each embedded reset, with no gap where a
      terminal-default background would show through; no palette fallout
      found, nothing to fix.
- [x] Popup mouse forwarding on the user's terminal (source says yes:
      `handle_popup_mouse`, `src/app/input/mod.rs:482`) — one manual probe.
      **Live-probed 2026-08-31 (task 2b):** could not drive a real physical
      mouse in this environment, so injected raw SGR sequences
      (`\x1b[<0;51;21M`/`m` for a click, `\x1b[<65;51;21M` for wheel-down)
      via `herdr pane send-text` into the popup's host pane (the pane
      running the nested herdr TUI client) while the smoke binary was
      temporarily patched to enable `tea.MouseModeAllMotion` and print
      received events. Both a click and a wheel event were received and
      rendered inside the popup (`mouse: left`, `mouse: wheeldown`),
      confirming `handle_popup_mouse` correctly forwards translated,
      coordinate-relative mouse bytes into the popup PTY. This exercises
      Herdr's own SGR-decode-and-forward path end to end, not a physical
      terminal's mouse reporting, so a from-hardware click is still worth a
      spot-check at Task 19, but the code path itself is confirmed working.
- [ ] `agent_pane_busy` retry: start an agent immediately after
      `worktree create` and confirm the bounded retry rides it out.
      **Probed 2026-08-31 (task 2b):** busy state did not reproduce at
      ~90 ms creation-to-start gap; retry path itself remains unconfirmed
      live — recheck opportunistically at Task 19.
      **Re-probed 2026-09-01 (task 19):** 5 back-to-back `worktree create`
      → `agent start` pairs with no artificial delay at all (the pane id
      read straight from `worktree create`'s own JSON response and used
      immediately) still did not reproduce `agent_pane_busy` in this
      environment; still leaving this unticked per the task brief ("if not,
      reproduced, leave it and say so") — the retry path remains
      unconfirmed live, covered only by Task 9's mock-runner unit tests.
- [x] Pin the minimum supported clauth version in README. **Recorded
      2026-09-01 (task 19) for Task 22:** clauth 0.14.1 (`clauth --version`)
      is the version installed and exercised throughout this live
      checkpoint — `clauth status --json` schema `1` parsed correctly by
      `internal/clauth`, and `clauth start <profile> --` launches
      correctly under `pane run`. No older clauth version was available to
      test in this environment, so 0.14.1 is the empirically-confirmed
      floor, not a verified absolute minimum — Task 22 should phrase the
      README accordingly ("tested with clauth 0.14.1+") rather than
      implying earlier 0.x releases were checked and rejected.
