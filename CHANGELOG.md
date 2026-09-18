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
- **`placement` offers `tab in <space>` and defaults to it** whenever the
  project's checkout already has a workspace open (#128), so a repository's
  own space collects its sessions as tabs instead of a new top-level space
  appearing per task. `new space` stays for a repository with none.
- **A worktree session always runs in the worktree's own space**, which
  herdr's sidebar nests under the repository's. While the worktree is on,
  the `placement` row is inert and reads `the worktree's own space`. It
  keeps its value for when the worktree is turned off. Placing a worktree
  session's agent elsewhere left the worktree's space holding only an idle
  shell, and once `tab in` became the default it did so for every worktree
  session after the first in a repository.
- **Every tab a session opens carries its title**, from the popup and from
  `create` alike. herdr's `workspace create` and `worktree create` label
  only the space, which left its first tab called `1`, so a `new space` or
  worktree session now renames that tab. The rename is cosmetic, and a
  failed one never fails the create. `create` reports the step on stderr as
  `failed, continuing` and the run goes on. The popup marks the row `!`,
  but a successful submit still closes the popup at once, so the row stays
  on screen only when a later step fails. `split here` renames nothing,
  because the tab it lands in is yours.
- **A selected Linear issue seeds title, branch and prompt**, unless you
  have already typed over them.
- **The prompt panel can end the prompt with pane-reaper's instruction.**
  A `keep · reap` line over the textarea, flipped with `⌃X` or a click,
  asks the agent to mark its own pane ready once its work is saved, so the
  pane-reaper plugin can close it. Off by default. It is never added to an
  empty prompt or a slash command, and the row reads `· reap when done`
  only when it will really be sent.
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
  `--reap` / `--no-reap`, `--placement`, `--workspace`, `--agent`,
  `--account`, `--issue`, `--json`, `--on-failure keep|clean`.
- **`--placement tab-in` and `--workspace <id>`** place the agent's tab in a
  workspace other than the invoking one — the one already holding the
  project (the default when there is one), or any open workspace by id —
  with none of the three pane variables required (#128). They need
  `--no-worktree`: with a worktree, `--workspace` and any `--placement` but
  `new-space` are refused, and a remembered placement does not apply.
- **Anything left unset resolves exactly as the form resolves it** — one
  resolver, and a test drives a real form and a real `create` request over
  the same files to keep the two from drifting.
- **A title is cut to 32 runes, as the form's `title` row cuts it**,
  whether it came from `--title` or from `--issue`'s Linear issue, and a
  line on stderr gives the title used. Uncut, one long title built two
  different sessions: the space and tab labels and a title-derived branch
  differed from the popup's, and so could the agent's name (#176).
- Progress goes to stderr one line per step; the result to stdout, or a
  single JSON object under `--json` carrying a `provenance` map naming the
  tier each value came from.
- Exit codes: `0` created, `1` the plan started and failed (`--on-failure`
  applied), `2` bad usage or an unresolvable request, `3` herdr unreachable.
- **It refuses what the form refuses**, before anything is created (exit
  `2`): a branch that already exists when a worktree would create it, a
  title an open workspace already carries, and a pinned account clauth
  reports as signed out (#147). The branch check matters most: herdr checks
  an existing branch out rather than refusing it, and ignores `--base` when
  it does. `--account auto`'s real pick runs after every other check, so a
  refused request spends no pick (#145), and `--account active` means no
  pin, as it does in `config.toml` (#146).
- **A prompt has three fates, not two**: `prompt_status` is `sent`,
  `unsent` or `unconfirmed`. The third is `agent prompt --wait` giving up
  before the agent's status changed, which is not proof the prompt failed
  to arrive — so the text comes back as `unconfirmed_prompt` rather than
  `unsent_prompt`, and `--on-failure clean` is refused, because the session
  may have an agent working in it right now.
- **The reported ids name the agent, not the space.** `--json` carries the
  space's triple alongside the agent's. The two differ when herdr answers a
  worktree create with a workspace that was already open, and the agent is
  given a fresh tab in it instead of whatever pane herdr handed back.
- `create` reads the plugin environment only when `HERDR_PLUGIN_ID` says it
  is ours, so a shell inside another plugin's pane cannot have its config
  read as this plugin's or its state written into.
- **`herdr-draft skill` prints an agent skill for driving all of the
  above.** Redirect it into `~/.claude/skills/spawn/SKILL.md` — the README
  has the recipe, including the `herdr plugin list --json` lookup that
  finds the binary, which is not on `PATH` (#167) — and
  an agent asked to hand work off creates a session instead of writing a
  handoff document and stopping. It carries the `HERDR_PLUGIN_*` exports
  and why they must share one command, the placement choice, the prompt,
  and how to read `prompt_status` and the pane afterwards. It also carries
  the absolute path of the binary that printed it, which is why the emitted
  file is machine-specific and is regenerated after an upgrade.

### Where values come from

- Five tiers, applied lowest-first so the last writer wins: built-in →
  `config.toml` → `last-used.json` → `.herdr-draft.toml` →
  `projects.json`. A team's committed default beats what you last did in
  some *other* repository and loses to what you last did in this one.
- One more tier is a fact about the machine, not a file: `herdr workspace
  list`. It decides placement only, for a session without a worktree:
  `new-space` from the three memory and user-config tiers yields to
  `tab-in` when the project's space is open, a remembered `tab-in` falls
  back once it is gone, and a `here` placement, a `.herdr-draft.toml`, and
  `--placement` all stand.
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
  rejected is reported rather than silently dropped. `[reaper]` is refused
  with its own reason: it would add an instruction to your agent's prompt.
- Read from the *origin* repository root and re-read when the project row
  changes.

### Configuration and appearance

- `config.toml`, all keys optional and unknown keys ignored, with
  `[linear]`, `[clauth]`, `[agents]`, `[timeouts]`, `[worktree]`, `[reaper]`
  and `[palette]` sections.
- `[worktree] trust_repository` is deliberately `config.toml`-only: it
  waives git's ownership check for one request, so a cloned repository must
  not be able to assert its own trustworthiness.
- `[reaper] mark_ready` turns the prompt's `reap` on by default for the
  popup and `create` alike. It is `config.toml`-only, and never remembered
  from a submit: one reaped session must not make the next one reap, and
  orchestrator sessions must never close themselves.
- An optional **account picker protocol**: herdr-draft can hand "which Claude
  account should this session use?" to any executable implementing a small
  documented contract (`--dir <path> --json [--strict] [--dry-run]`, one JSON
  object out, six documented exit codes). It ships no picker and hard-codes
  none, which is what keeps this feature from making a public plugin depend
  on one machine's private toolchain. `[clauth] picker` names one —
  explicitly; a picker is never discovered on `PATH` — and it is probed once
  before it is trusted. Every call is bounded by a 30-second deadline, a
  refusal is always shown rather than silently downgraded to an unpinned
  launch, and an exit code outside the documented set is reported as a
  malfunction rather than obeyed as a decision. With it configured, the
  `account` row grows an `auto` selection and `create` accepts
  `--account auto`. The picker's `warnings` and `machine` figures (load,
  cpus, swap) are shown on the `account` panel, with an unmeasured figure
  written as such rather than as zero, and `create` prints the warnings to
  stderr; neither ever refuses a session (#165).
- `[clauth] launch` selects how a pinned account is launched, defaulting to
  `clauth start <profile> --`. The opt-in `"wrapper"` mode types
  `CLAUDE_CONFIG_DIR=<dir> claude` instead, which is only worth setting on a
  machine whose shell defines `claude` as a function; everywhere else it
  silently means less, which is why it is not the default.

  It pairs with `[clauth] launcher` as **one decision**: `launch` picks the
  mechanism and `launcher` configures the argv one. The two cannot be a single
  key — `launcher` conveys the account as a word in a command line and
  requires an `{account}` placeholder, while wrapper mode conveys it as a
  credential directory in the environment and names the account nowhere in its
  argv, and a quoted `NAME=value` is not a shell assignment, so the
  environment half cannot travel inside the template.

  `launcher` takes exactly one `{account}`, standing for the profile name;
  any other shape falls back to the default rather than launching on an
  unintended account. The reason to set it at all is that `clauth start`
  costs the session its herdmates team lead, so a wrapper of your own that
  keeps both — `["claude-as", "{account}"]` — goes here.

  Only one mechanism is ever in effect: a `launcher` set alongside
  `launch = "wrapper"` is ignored. That, a malformed `launcher` and an
  unrecognised `launch` are each reported, on the `account` row's panel in
  the popup and on stderr by `herdr-draft create`; a malformed `launcher`
  beside `"wrapper"` is reported as both malformed and ignored. The popup
  also reports the launch it performed, including when wrapper mode fell
  back for want of a `config_dir`.
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
- **A pane with nothing on it is not a ready one, and a send that reported
  success is not a delivery.** The read before a prompt has to come back
  with something on it: a screen that has not painted matches no dialog
  signature, and treating that as safe is how a prompt's own Enter used to
  answer a trust dialog that drew a moment later — killing the agent while
  the run reported a clean create. A pane can also carry a shell prompt and
  nothing else for a second or so before an agent's own screen appears,
  which no signature will ever match, so the pane is read again *after* the
  send: a create is only called clean once the pane says the prompt landed. An agent that has stopped answering, or a dialog still up with no
  trace of the prompt on it, is a failure with the prompt saved. This check
  never resends: text has already gone out, so a second copy is the injury
  it exists to report. The one failure that does get another send is a
  *stall* — see below — and it is the only one.
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
  nobody at the keyboard. That budget covers waiting on a *person* and
  nothing else.

  The wait covers the dialog wherever it stops the run. herdr may recognise
  the screen and refuse the launch, or it may call the agent ready and leave
  the dialog for herdr-draft's own prompt guard to catch one step later;
  live, the second is what the popup meets. The launch-step wait polls
  herdr's status, the prompt-step wait polls the pane's own screen — herdr
  being wrong about that pane is the reason the second wait exists, so it
  does not ask herdr again.

  A prompt that herdr reports as *stalled* — it saw the agent do nothing at
  all — is sent once more after a short pause, through the same dialog
  guard. That is the one prompt failure which is positive evidence nothing
  was *processed*, and for about a second after a trust dialog clears it is
  the ordinary outcome: herdr calls the agent ready while its interface is
  still coming up. The pause waits for a terminal to paint rather than for
  a person, so this retry is not on `trust_wait_ms` and both the popup and
  headless `create` take it. It is deliberately not the same as a prompt
  whose confirmation merely timed out, which means the agent *was* working
  and is never resent.

  A stall that survives the retry is reported as **unconfirmed**, not
  unsent: herdr writes the prompt text and Enter before it starts watching,
  so after two sends the pane may hold two copies and "it never arrived" is
  not something this command knows. `--on-failure clean` is refused, and
  both the popup and `create` say to read the pane before resending.

  That posture is **sticky**. Once the text has gone out, nothing later in
  the same step reports the prompt as unsent or permits the clean — which
  matters most when the retry itself is refused, the first send having
  stalled against a screen that had not painted and the dialog being up by
  the time the retry looks.
- `herdr pane run` types its argv into a shell rather than exec'ing it, so
  the runner shell-quotes every element — and the argv path that has no
  shell deliberately does not.
- **Besides Linear — which a configured key is your consent for — one
  thing reaches the network without being asked, and it is bounded.**
  Landing the form on a git repository runs a background `git fetch
  --prune` there — once per repository per form open — so the base picker
  knows about remote branches that arrived since you last fetched. It
  contacts that repository's default remote with the credentials git would
  normally use, *including* a `core.sshCommand` or `GIT_SSH` of your own,
  which it resolves and appends to rather than overrides. Every git call
  that can reach a remote runs with terminal prompts and ssh interaction
  disabled, because git asks for a password on `/dev/tty` rather than on
  the stdin it was handed, and inside the popup that is the terminal the
  form is drawn on. The fetch is additionally bounded at 30 seconds, since
  disabling prompts does not stop a configured askpass helper from opening
  a dialog that never answers. [`SECURITY.md`](SECURITY.md) states the same
  at greater length.

### Packaging

- Requires **herdr ≥ 0.9.0**; `platforms = ["linux", "macos"]`, declared
  rather than omitted, because an absent list is an active claim to run on
  Windows, which this cannot.
- `herdr-draft version` prints the version, the plugin id it answers to,
  and the build it was compiled from when the toolchain stamped one.
- The plugin id is namespaced: `zvibaratz.draft`.
- MIT licensed. The form's UI code is ported and adapted from
  [Atrium](https://github.com/ZviBaratz/atrium) — see `NOTICE`.
