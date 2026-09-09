# Changelog

Notable changes to herdr-draft, newest first. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the versions
are [semantic](https://semver.org/spec/v2.0.0.html).

Four things carry the version and move together: `herdr-plugin.toml`'s
`version`, `herdrc.Version`, this file's newest release heading, and the
`v`-prefixed git tag. The first three are held to each other by tests; the
tag is the one no test can reach. See
[CONTRIBUTING.md](CONTRIBUTING.md#releases) for the order they move in, and
for why a heading here may read `unreleased`.

## 0.1.0 — unreleased

First release. There is no predecessor to diff against, so this entry says
what 0.1.0 *is* rather than what changed.

herdr-draft is a [herdr](https://github.com/herdrdev/herdr) plugin: a
single-screen dialog, opened in a herdr popup, that creates a fully
configured agent session in one submit. The same binary also creates one
without the popup. It drives herdr exclusively through the public CLI
(`$HERDR_BIN_PATH`), never the raw socket API.

### The dialog

- **Eight rows, one line each**, in a fixed order — `issue`, `title`,
  `prompt`, `project`, `worktree`, `placement`, `agent`, `account` — over a
  single detail panel belonging to the focused row. A row states what the
  session will be rather than which knob is set, and its position does not
  move with focus or window height.
- `issue` is present only when Linear is configured, and `account` only when
  clauth has at least two profiles. With neither, the form is six rows.
- **A selected Linear issue seeds title, branch and prompt**, unless you
  have already typed over them.
- Candidate lists are tables: aligned columns, a right-flush status word, a
  live match count, a scrollbar once the list outgrows the window, and the
  matched run drawn in the accent colour.
- The `title` panel shows the sessions that were open when the form opened
  and marks the one a colliding title would collide with.
- The `worktree` panel is a three-part `off · on` / `branch` / `base`
  editor, so a form with a worktree is the same height as one without.
- **Create is a real focus stop**, the ring's last, on the footer rather
  than in the row stack. `⌃S` submits from anywhere; `⌃R ⌃R` clears back to
  the resolved defaults.
- Mouse: click a row to focus it, click a panel line to select it, wheel to
  scroll the panel.
- The popup is a fixed **104×32 cells**, a manifest value rather than a
  proportion of the terminal. herdr clamps it down to the terminal and
  never up.

### Headless `create`

- `herdr-draft create` builds the same session without the popup, for
  scripts and for agents already running inside one.
- Flags mirror the form's fields: `--project`, `--title`, `--prompt` (`-`
  reads stdin), `--branch`, `--base`, `--worktree` / `--no-worktree`,
  `--placement`, `--agent`, `--account`, `--issue`, `--json`,
  `--on-failure keep|clean`.
- **Anything left unset resolves exactly as the form resolves it** — one
  resolver, and a test drives a real form and a real `create` request over
  the same files to keep the two from drifting.
- Progress goes to stderr one line per step; the result to stdout, or a
  single JSON object under `--json` carrying a `provenance` map naming the
  tier each value came from.
- Exit codes: `0` created, `1` the plan started and failed (`--on-failure`
  applied), `2` bad usage or an unresolvable request, `3` herdr unreachable.
- **A prompt has three fates, not two**: `prompt_status` is `sent`,
  `unsent` or `unconfirmed`. The third is `agent prompt --wait` giving up
  before the agent's status changed, which is not proof the prompt failed
  to arrive — so the text comes back as `unconfirmed_prompt` rather than
  `unsent_prompt`, and `--on-failure clean` is refused, because the session
  may have an agent working in it right now.
- **The reported ids name the agent, not the worktree.** Under `--worktree`
  with `tab-here` or `split-here` the checkout gets a space the agent does
  not run in, so `--json` carries the space's triple alongside the agent's.
- `create` reads the plugin environment only when `HERDR_PLUGIN_ID` says it
  is ours, so a shell inside another plugin's pane cannot have its config
  read as this plugin's or its state written into.

### Where values come from

- Five tiers, applied lowest-first so the last writer wins: built-in →
  `config.toml` → `last-used.json` → `.herdr-draft.toml` →
  `projects.json`. A team's committed default beats what you last did in
  some *other* repository and loses to what you last did in this one.
- Per-project memory re-applies when you change the project row, for every
  field you have not touched yourself, and is keyed by the **git repository
  root** so a linked worktree and its origin share one memory.
- The resolver is pure and is the only place the precedence chain exists.

### Repository-level config (`.herdr-draft.toml`)

- A repository can commit creation defaults so a team shares them:
  `branch_prefix`, `default_worktree`, `default_placement`, `default_base`,
  `linear_branch_name`.
- **That five-key allow-list is a trust boundary, not a convenience.** The
  file arrives with `git clone`, so it may only choose among values you
  could already have picked in the form yourself, and may never name a
  command, a path outside the repository, or a credential. Anything
  rejected is reported rather than silently dropped.
- Read from the *origin* repository root and re-read when the project row
  changes.

### Configuration and appearance

- `config.toml`, all keys optional and unknown keys ignored, with
  `[linear]`, `[clauth]`, `[agents]`, `[timeouts]`, `[worktree]` and
  `[palette]` sections.
- `[worktree] trust_repository` is deliberately `config.toml`-only: it
  waives git's ownership check for one request, so a cloned repository must
  not be able to assert its own trustworthiness.
- `[palette]` overrides individual theme fields when herdr's own theme
  cannot be resolved from a static config file. Every region that marks
  something — rules, the focused row's fill, input backgrounds — has a
  **measured** contrast floor against the ground it is actually drawn on,
  over all eighteen builtin herdr themes.

### Robustness

- **Degradation over refusal.** Only an unparseable plugin context, an
  unreachable herdr or a broken `config.toml` refuse to open. A broken
  Linear key or an unloadable clauth degrades that one row to "unavailable,
  with a reason".
- **A report is not evidence; the pane is.** Neither direction of the
  prompt-delivery question is answered by what herdr reports: a wait that
  times out has not proved a failure, and an agent's `idle` status has not
  proved one either way. Where the answer cannot be known, the output says
  so and names the pane to look at, rather than asserting the tidier
  answer.
- **Screen detection is evidence-based.** Before sending a queued prompt
  the executor reads the pane and checks it against known blocking-dialog
  signatures rather than trusting an "idle" report, and turns herdr's
  "blocked during startup" into an instruction the user can act on.
- **A first-run trust dialog is a pause, not a failure.** Every worktree is
  a directory the agent has never been trusted in, so its first launch there
  meets a confirmation screen and herdr refuses to call the launch ready.
  The popup waits through it — the launch row shows `…`, the footer says
  what to answer and where, and the pipeline delivers the queued prompt and
  closes by itself once the dialog clears. No keypress in the popup. How
  long it waits is `[timeouts] trust_wait_ms`, five minutes by default; when
  that runs out, or when the agent stops running because the dialog was
  declined, it falls back to the failure screen with the prompt saved.
  Headless `create` deliberately keeps failing fast, because a script has
  nobody at the keyboard.
- `herdr pane run` types its argv into a shell rather than exec'ing it, so
  the runner shell-quotes every element — and the argv path that has no
  shell deliberately does not.

### Packaging

- Requires **herdr ≥ 0.9.0**; `platforms = ["linux", "macos"]`, declared
  rather than omitted, because an absent list is an active claim to run on
  Windows, which this cannot.
- `herdr-draft version` prints the version, the plugin id it answers to,
  and the build it was compiled from when the toolchain stamped one.
- The plugin id is namespaced: `zvibaratz.draft`.
- MIT licensed. The form's UI code is ported and adapted from
  [Atrium](https://github.com/ZviBaratz/atrium) — see `NOTICE`.
