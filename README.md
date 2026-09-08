# herdr-draft

New-session creation dialog for [herdr](https://github.com/herdrdev/herdr).

```
new session                                                  herdr-draft · main
───────────────────────────────────────────────────────────────────────────────
  issue      none
  title      fix login redirect loop
  prompt     Work on ENG-101: Fix login redirect loop
  project    ~/Projects/herdr-draft
  worktree   on · zvi/fix-login-redirect-loop ← main
  placement  new space
  agent      claude
  account    active · max · 12%
───────────────────────────────────────────────────────────────────────────────
  branch will be zvi/fix-login-redirect-loop


⌃S create now · ⇥ for the prompt · ⌃R clear             ↵ create    esc cancel
```

(an 80×24 render with the title row focused, blank panel rows trimmed. The
focused row is painted in the theme's `active_row_bg`, not marked with a
glyph, so plain text cannot show which one it is — here, `title`.)

## The fast path

Open it, type a title, press Enter.

That is the whole common case. Focus opens on **title**, every other row is
already resolved to something correct, and `↵` from a non-empty title
creates the session — a worktree on a branch derived from what you typed, a
new space to hold it, and your agent running in it.

```
new session                                                  herdr-draft · main
───────────────────────────────────────────────────────────────────────────────
  issue      none
  title      untitled
  prompt     —
  project    ~/Projects/herdr-draft
  worktree   on · from main
  placement  new space
  agent      claude
  account    active · max · 12%
───────────────────────────────────────────────────────────────────────────────


name it to create · ⇥ for the prompt · ⌃R clear         ↵ create    esc cancel
```

Everything else is there for the session that is not the common one. Nothing
below the title needs your attention unless you want to change it.

## What it is

A herdr plugin: a single-screen dialog, opened in a herdr popup, that
creates a fully configured agent session in one submit — git worktree,
placement, agent kind, Claude account (via
[clauth](https://github.com/clauth/clauth)), an initial prompt, and
optionally a Linear issue to seed all three from. The same binary also
creates one [without the popup](#headless-create).

It is a standalone Go + Bubble Tea binary. herdr neither knows nor cares
about the implementation language — the plugin drives herdr exclusively
through the public CLI (`$HERDR_BIN_PATH`, itself reaching the herdr server
over the socket at `$HERDR_SOCKET_PATH`).

Three design documents: `docs/specs/2026-08-31-herdr-draft-design.md` is the
original; `docs/specs/2026-09-02-herdr-draft-v2-design.md` supersedes its
§6 (the form) and §7 (skin and mouse); and
`docs/specs/2026-09-02-herdr-draft-v3-design.md` supersedes v2's §4, §7 and
§9 with the dialog described here. Each one's own header itemises what it
replaced. The
form's UI code is ported and adapted from
[Atrium](https://github.com/ZviBaratz/atrium), the author's previous TUI —
see [License & provenance](#license--provenance).

## The form

**Eight rows, one line each**, in this order. A row states what the session
will be, not which knob is set, and it never moves: the row positions are
fixed whatever has focus and whatever the window height is.

| Row | Reads | When it can't be set |
|---|---|---|
| `issue` | `ENG-101 · fix login redirect loop`, or `none` | `unavailable  <reason>` |
| `title` | what you typed, or a dim `untitled` | — |
| `prompt` | the first line, plus a dim ` +N more` | — |
| `project` | the path, `~`-shortened | `invalid` / `not a repository` |
| `worktree` | `on · <branch> ← <base>`, `on · from <base>` before a title exists to derive a branch from, or `off` | `not a git repository` |
| `placement` | `new space` / `tab here` / `split here` | — |
| `agent` | `claude` | — |
| `account` | `personal · Max 20x · 5h 12% · 7d 40%`, or `active · …` when nothing is pinned | `account pinning only applies to claude` |

`issue` is absent unless Linear is configured, and `account` unless clauth is
configured with at least two profiles — both static, startup-time checks
(see [Configuration](#configuration)). With neither, the form is six rows.
That is the shape most people will see.

**The popup is a fixed 104×32 cells.** That is a manifest value, not a
proportion of your terminal, and it is a visible change from the old
percentage-sized popup: on a large terminal the dialog is now a card rather
than a wall. herdr clamps it down to the terminal and never up, so an 80×24
terminal gets a full-screen popup rather than an overflow.

**One panel**, under the second rule, belonging to the focused row and to no
other: the candidate list for `issue`, `project`, `agent` and `account`; the
textarea for `prompt`; the verdict line (`branch will be …`) plus the
sessions that were open when the form opened, for `title`; a three-part
`off · on` / `branch` / `base` editor for `worktree`. Branch and base are not
rows of their own — they are parts of the worktree row's panel, so a form
with a worktree is the same height as one without.

Every candidate list is a table: aligned columns, a right-flush status word,
a scrollbar in the last column while the list outgrows the window, a live
`3/24 issues` count, and the run your filter matched drawn in the accent
colour. The `title` panel's session list is the one exception to "candidate"
— it is informational, has no cursor, and marks the row a colliding title
would collide with.

**The footer** teaches the focused row on the left and carries the actions on
the right. Create is a real focus stop, the ring's last, reached by `⇥` past
`account` — it just lives on the footer rather than in the stack, so nothing
in the row order is spent on a button.

| Key | What it does |
|---|---|
| `⇥` / `⇧⇥` | next / previous row (`⇧⇥` from title reaches `issue`) |
| `↑` `↓` | move within the focused row's panel |
| `←` `→` | chips: worktree on/off, placement, agent favorites |
| `⇥` in a picker | complete, then advance |
| `↵` | create, from a non-empty title, from the prompt, or from the button; advance from anywhere else |
| `⌃S` | create, from anywhere |
| `⌃J` (or `⇧↵`, `⌥↵`) | newline in the prompt |
| `⌃R` `⌃R` | clear back to the resolved defaults |
| `esc` / `⌃C` | cancel |

The mouse works too: click a row to focus it, click a panel line to select
it, and the wheel scrolls the panel.

### The project row

The project row has two modes, and switches between them on what you type:

- **Fragment** (anything not starting with `/`, `~` or `.`) fuzzy-filters a
  fixed pool of candidates: the current space's repo root first, then the
  current workspace cwd, then every open herdr workspace's own worktree
  root, then your recents.
- **Path** (`/`, `~` or `.`) browses the filesystem — the subdirectories of
  the parent you have typed so far, re-read only when that parent changes.
  `⇥` completes to the longest common prefix, shell-style, and deliberately
  stops at an exact directory rather than diving into it; typing `/` is how
  you descend. The fully typed path is always the last row, so a directory
  that does not exist yet (or one buried past the 500-entry listing cap)
  stays selectable.

Browsed rows are absolute — `~/Projects/x` resolves at browse time, since
`herdr workspace create --cwd` would otherwise resolve a relative path
against the *server's* working directory. A directory that is not a git
repository is allowed; the row says `not a repository` and the worktree row
goes inert, which is the only consequence.

Changing the project re-reads that repository: its branches, its
`.herdr-draft.toml`, and what you last chose there. The header's right half
follows it — it names the **selected** project and the branch checked out
there, not the workspace the popup was opened from.

## Requirements

- herdr ≥ 0.9.0 (`min_herdr_version` in `herdr-plugin.toml`). herdr refuses
  to install or link a plugin whose minimum is newer than the running binary,
  so an older herdr gets a clean refusal rather than a plugin that loads and
  then misbehaves.
- [clauth](https://github.com/clauth/clauth) ≥ 0.14.1 for account-pinned
  (Path B) launches — see [clauth integration](#clauth-integration) below.
  clauth is entirely optional: without it, herdr-draft still works for
  manual/Linear-seeded session creation (Path A), it just has no `account`
  row to pin a profile on.
- Go 1.25+ to build from source (`herdr-plugin.toml`'s `[[build]]` runs
  `go build -o bin/herdr-draft ./cmd/herdr-draft` for you).
- Linux or macOS. The manifest declares `platforms = ["linux", "macos"]`,
  so herdr refuses the install on Windows rather than registering a plugin
  that cannot launch: the action entrypoint is `sh -c`, and the build
  produces an extensionless binary. Windows support is a feature nobody
  has built or tested, not an oversight in packaging.

## Install

Not yet published to a registry. For now, link a local checkout:

```bash
herdr plugin link <path-to-this-repo>
```

`<path-to-this-repo>` is a positional argument (a plugin directory
containing `herdr-plugin.toml`, or a direct manifest path) — not `--path`.
herdr runs the manifest's build command and registers the plugin globally
for the current user; every herdr session sees it immediately.

Once published, the intended route is:

```bash
herdr plugin install ZviBaratz/herdr-draft
```

(this is aspirational — herdr-draft has not been published yet; see the
v1 design spec §16, "marketplace publication," for why it's deferred until the
tool has proven itself in daily use).

## Keybinding

Plugin actions don't appear in herdr's context/global menus in plugin v1 —
keybinding and CLI are the only discovery surfaces (see
[Troubleshooting](#troubleshooting)). Bind a key in herdr's own
`config.toml`:

```toml
[[keys.command]]
key = "prefix+n"
type = "plugin_action"
command = "draft.open"
description = "new session"
```

`draft.open` is herdr's qualified id for herdr-draft's one action (plugin
id `draft`, action id `open`, from `herdr-plugin.toml`). You can also
invoke it directly from a shell:

```bash
herdr plugin pane open --plugin zvibaratz.draft --entrypoint open
```

## Headless `create`

The same binary creates a session without the popup, for scripts and for
agents already running inside a session. It is the plugin's own
`bin/herdr-draft` (herdr builds it from the manifest; put it on `PATH` or
call it by path):

```bash
herdr-draft create --title "fix login redirect loop" --worktree
herdr-draft create --title "triage" --no-worktree --placement split-here
git log -1 --format=%B | herdr-draft create --title "revert" --prompt -
```

Flags mirror the form's fields: `--project`, `--title`, `--prompt` (`-`
reads stdin), `--branch`, `--base`, `--worktree` / `--no-worktree`,
`--placement`, `--agent`, `--account`, `--issue`, `--json`, `--on-failure
keep|clean`. `herdr-draft create --help` lists them.

**Anything you don't pass resolves exactly the way the form resolves it** —
see [Where defaults come from](#where-defaults-come-from); the command and
the form go through the same resolver, and a test drives both over the same
files to keep them from drifting. The project directory defaults to the
working directory. A successful create records what it used, so the form's
next open defaults to it too.

Progress goes to stderr, one line per step; the result to stdout, or a
single JSON object with `--json` (which also carries a prompt the dialog
guard withheld, since a headless caller has no pane to recover it from, and
a `provenance` map naming the tier each value came from — `flag` for the
ones you passed). It never prompts. Exit codes:

| Code | Meaning |
|---|---|
| 0 | created |
| 1 | the plan started and failed (`--on-failure` applied) |
| 2 | bad usage, or a request that cannot be resolved |
| 3 | herdr is unreachable |

`--placement tab-here` and `split-here` need to know where "here" is, and
read `HERDR_WORKSPACE_ID` / `HERDR_TAB_ID` / `HERDR_PANE_ID`, which herdr
sets in every pane's shell — regardless of `--worktree`, since placement
now decides where the AGENT'S pane lands even when the worktree gets its
own new space alongside it. Only `new-space` needs none of them. A missing
one is named exactly.

**Export the plugin environment.** herdr sets those three pane variables
but not `HERDR_PLUGIN_CONFIG_DIR` / `HERDR_PLUGIN_STATE_DIR`, which it
gives only to a launched plugin — so without them `create` resolves from
built-in defaults instead of your own configuration, and says so. All
three lines, in your shell rc:

```bash
export HERDR_PLUGIN_ID="zvibaratz.draft"
export HERDR_PLUGIN_CONFIG_DIR="$(herdr plugin config-dir zvibaratz.draft)"
export HERDR_PLUGIN_STATE_DIR="${XDG_STATE_HOME:-$HOME/.local/state}/herdr/plugins/zvibaratz.draft"
```

herdr has a CLI for the config directory but not for the state one, whose
layout is `<herdr state dir>/plugins/zvibaratz.draft`
(`herdr:src/plugin_paths.rs`). Point both at the same directories the popup
gets and the two paths share one memory.

`HERDR_PLUGIN_ID` is not decoration: it is the only one of the four that
says *whose* the other three are, and `create` uses them only when it
matches. A shell started inside *another* plugin's pane can inherit that
plugin's whole `HERDR_PLUGIN_*` environment, and an inherited one does not
reliably carry the id at all — so a different id and a missing id are
treated the same way. Either has `create` ignore all four, including the
context JSON, whose pane and workspace ids are not necessarily where you
are, rather than read one plugin's config as another's or write your state
into its directory. It tells you which case it was, and what to export.

## Configuration

herdr-draft reads `$HERDR_PLUGIN_CONFIG_DIR/config.toml`. Every key is
optional; a missing file or omitted key falls back to the default shown
below. Unknown keys are ignored, so an older herdr-draft binary tolerates a
newer config file.

Get the config directory for your herdr install with:

```bash
herdr plugin config-dir zvibaratz.draft
```

### Top level

- `branch_prefix` (default: your lowercased OS username plus `/`, e.g.
  `zvi/`, or `user/` if the username can't be determined) — prefix
  prepended to the sanitized title when herdr-draft derives a branch name
  in manual mode. It must be usable as the leading part of a git ref
  (`git check-ref-format`'s rules, minus the ones a prefix is exempt from:
  a trailing `/` is fine, and an explicit empty value means "no prefix"),
  and it may not start with `-`, which the herdr CLI would read as a flag.
  An unusable value is ignored with a reason and the default takes over;
  it never stops herdr-draft from opening.
- `default_worktree` (default: `true`) — whether the worktree row starts on
  or off for a git target.
- `default_placement` (default: `"new-space"`) — where the agent's own pane
  lands: `new-space`, `tab-here`, or `split-here`. This applies whether or
  not the worktree row is on: a worktree always gets its own new space for
  the checkout regardless, and placement separately decides where the
  agent runs, which can be that same new space or a tab/split on the
  invoking pane instead.

Both are *defaults*, and a later tier can override them — see
[Where defaults come from](#where-defaults-come-from).

### `[linear]`

- `api_key_cmd` — argv (no shell) whose stdout is your Linear API key, e.g.
  `["pass", "show", "linear-api-key"]`. Checked before `LINEAR_API_KEY` and
  before `api_key` below.
- `api_key` — the API key given directly in the config file. Discouraged;
  herdr-draft checks the file's permissions (0600) before trusting a value
  read this way.
- `prompt_template` (default: empty, meaning the built-in template) —
  overrides the prompt seeded from a selected Linear issue. The built-in
  default is:

  ```
  Work on {identifier}: {title}

  {url}

  {description}
  ```

If no Linear API key resolves from any of the three sources, the `issue` row
is simply not rendered — the form is seven rows and everything else works
unchanged.

If a key source is *configured but fails* — `api_key_cmd` exits non-zero or
isn't on `$PATH`, or `api_key` sits in a `config.toml` readable by anyone
but you — the row is rendered and reads `unavailable  <reason>`, with the
same reason on its panel's own line. It can't be focused, and everything
else in the form still works.

### `[clauth]`

- `enabled` (default: auto-detected) — explicitly force clauth integration
  on or off. Omit to let herdr-draft detect it.
- `default` (default: unset, meaning "don't pin") — which clauth profile the
  `account` row starts on: a profile name, or the sentinel `"active"` —
  unset and `"active"` behave identically ("don't pin, use whichever profile
  clauth currently has live").

The `account` row only renders when clauth is configured **and** at least
two profiles exist (a static, startup-time check).

### `[agents]`

- `favorites` (default: `["claude"]`) — the chips at the top of the agent
  row's panel. herdr's full kind list (23 as of this writing) is in the same
  panel, under them, regardless of what's listed here.
- `default` — which kind the `agent` row starts on. It may name a kind
  outside `favorites` (it will be selected in the full list); an unknown
  kind is ignored rather than guessed at. It is one tier among several —
  see [Where defaults come from](#where-defaults-come-from), where
  `favorites[0]` is the built-in floor beneath all of them.
- `[agents.extra_args]` — a sub-table mapping agent kind to extra CLI args
  appended at launch, e.g. `claude = ["--model", "sonnet"]`. Empty by
  default for every kind.

### `[timeouts]`

- `detection_ms` (default: `30000`) — how long herdr-draft polls
  `herdr agent get` after launch before giving up on detecting the agent.
- `prompt_wait_ms` (default: `120000`) — timeout passed to
  `herdr agent prompt --wait` for step 3 of the submit pipeline.

### `[worktree]`

- `trust_repository` (default: `false`) — adds `--trust-repository` to
  `herdr worktree create`, which tells herdr to trust a verified repository
  **for that one request** instead of you editing your global git
  configuration. Needs herdr ≥ 0.9.0, which is this plugin's minimum.

  Turn it on if worktree creation fails with git's "dubious ownership"
  error — typically a repository owned by a different user than the one
  running herdr. The alternative is the permanent, machine-wide
  `git config --global --add safe.directory <path>`, which this key exists
  to let you avoid.

  It is `config.toml`-only, deliberately: a repository can never set it
  (see [Repo-level shared config](#repo-level-shared-config-herdr-drafttoml)),
  and neither `last-used.json` nor `projects.json` remembers it, because
  those remember what you *chose in the form* and there is no row for this.

### `[palette]`

Optional escape hatch when herdr's own theme can't be read correctly (see
the v1 design spec §7 for why: `auto_switch` and `name = "terminal"` aren't
resolvable from a static config file, so herdr-draft falls back to the
configured dark variant for both). Override individual fields, e.g.:

```toml
[palette]
accent = "#89b4fa"
panel_bg = "#1e1e2e"
```

Recognized keys mirror `internal/theme.Palette`'s eleven fields: `accent`,
`panel_bg`, `text`, `dim_text`, `danger`, `success`, `border`, `surface`,
`active_row_bg`, `warning`, `branch` (matching case-insensitively,
underscore-optional). Values must be a strict `#RGB` or
`#RRGGBB` hex literal — anything else (a named color, an ANSI code, a
malformed string) is silently ignored and that field keeps its previous
value.

The last four are the ones the row-stack layout needs, and they are the
ones most worth overriding if your theme reads wrong: `surface` fills the
secondary button and the selected panel row, `active_row_bg` is the focused
row's fill (there is no marker glyph, so if this reads as the background
nothing looks focused), `warning` marks a rate-limited or degraded account,
and `branch` colors branch names, as herdr does.

## Where defaults come from

Every row the form opens on, and every flag you leave off `create`, is
resolved through the same five tiers. **Highest first:**

| Tier | What it is |
|---|---|
| `projects.json` | your last choice in *this* project |
| `.herdr-draft.toml` | the repository's committed default |
| `last-used.json` | your last choice anywhere |
| `config.toml` | your own configuration |
| built-in | what herdr-draft ships with |

A team's committed default beats whatever you last did in some *other*
repository, and loses to what you last did in this one. `⌃R ⌃R` clears back
to the repository default.

Not every tier can supply every value. `.herdr-draft.toml` never chooses
your agent (that is a machine decision, not a repository one), and
`projects.json` and `last-used.json` only remember things a submit
actually settled — the worktree toggle, the placement, the agent kind, and
the base ref for the project you were in. A selected Linear issue overrides
title, branch and prompt on top of all of it, unless you have already typed
over them.

Per-project memory re-applies when you change the project row, for every
field you have not touched yourself. It is keyed by the **git repository
root**, so a linked worktree and its origin share one memory rather than
accumulating one entry each.

`create --json` prints a `provenance` map naming the tier each value came
from. In the form, only the repository tier is attributed — `from
.herdr-draft.toml`, on its own line in the panel of the row showing the
value.

## Repo-level shared config (`.herdr-draft.toml`)

A repository can commit a `.herdr-draft.toml` at its root so a team shares
creation defaults instead of each person configuring their own machine. It
is read from the *origin* repository root, so a linked worktree and its
origin see the same file, and re-read whenever you change the project row.

```toml
branch_prefix = "team/"
default_worktree = true
default_placement = "new-space"
default_base = "develop"
linear_branch_name = false
```

**Trust model.** This file arrives with `git clone`, so it may only choose
among values you could already have picked in the form yourself. It may
never name a command to run, a path outside the repository, or a
credential — anything else would make checking out a repository a
code-execution vector.

The list above is therefore the *complete* set of keys it may set:

- `branch_prefix` — same rules and same validation as the `config.toml`
  key. An unusable value is ignored with a reason, and *your own*
  configured prefix applies instead (not the built-in default).
- `default_worktree`, `default_placement`, `default_base` — as above.
- `linear_branch_name` (default: `true`) — whether a selected Linear
  issue's own `branchName` owns the branch. Set it to `false` in a
  repository with its own branch naming: the branch is then derived from
  the title and `branch_prefix` exactly as in manual mode, while the issue
  still seeds the title and the prompt.

**Everything else in the file is ignored and reported**, including every
other key `config.toml` accepts — `[agents.extra_args]` (it becomes part
of a launched agent's command line), `[agents] favorites`/`default` (a
repository doesn't choose which agent runs on your machine), `[linear]
prompt_template` (it would become the agent's first instruction),
`[linear] api_key`/`api_key_cmd` (a credential, and a command), and all of
`[clauth]`, `[timeouts]`, `[palette]` and `[worktree]` (a repository does
not decide that it is trusted — `trust_repository` waives a git safety
check, and the answer to "is this repository safe?" can never come from the
repository). So is a key that is simply misspelled. A malformed file is ignored too — it never blocks the form.

**Where you see all this.** The report — one line per ignored key, with the
reason — is in the **project** row's panel, since the project row is what
decides which repository's file applies:

```
  ignoring agents.extra_args: it becomes part of a launched agent's command li…
  ignoring linear.prompt_template: it would become the agent's first instructi…
```

A value the file *did* supply is marked `from .herdr-draft.toml` in the
panel of the row showing it (worktree, placement); the rows themselves stay
quiet, and the mark goes away once you change that value yourself.

For where this file sits among your own settings, see
[Where defaults come from](#where-defaults-come-from).

## clauth integration

When clauth is configured and ≥2 profiles exist, the `account` row lets you
pin a profile for the launched Claude session (`clauth start <profile> --`,
routed through `herdr pane run`). Pinning is optional — leaving the row on
`active` lets clauth use whatever profile is currently live, with no wrapper
involved. The row carries each profile's own state: `<name> · <plan> · ok`,
a rate-limited profile's percentage in the warning color, an expired one
reading `sign in again` in red.

**Tested with clauth 0.14.1+.** This is the empirically-confirmed floor
from this plugin's own live validation (`clauth status --json` schema `1`
parsing, and `clauth start <profile> --` launching correctly under
`herdr pane run`) — no older clauth 0.x release was available to test, so
treat it as "known to work at 0.14.1," not a verified absolute minimum.

**Known gap, accepted for v1**: on herdr session restore, herdr resumes
`claude --resume <id>` without the clauth wrapper, dropping the account
binding. Upstream discussion #3228 proposes per-agent resume templates
(`clauth info <id>` already prints the exact resume command). Documented
in README; nothing herdr-draft can fix locally.

## Known limitations

These are real, live-verified rough edges, not deferred features — read
them before filing a bug against something documented here:

- **A brand-new worktree's first launch stops on Claude Code's trust
  prompt.** Claude Code asks "Is this a project you created or one you
  trust?" the first time it runs in any directory, and every worktree
  herdr-draft creates is a directory it has never seen. herdr 0.9.0
  recognizes that screen and reports the agent as `blocked`, so `herdr agent
  start` refuses to call the launch a success — which means your submit
  stops at the launch step even though the agent is running fine.

  Nothing is lost. herdr-draft reads the pane, recognizes the dialog and
  says so: *claude started; answer the dialog in the pane, then keep this
  session*. Press `k` to keep, answer the prompt in the pane, and the
  session is a normal one from there. Any prompt you had composed is saved
  to a file, named on the failure screen, so you can paste it in once the
  agent is ready. It happens once per directory.

- **Prompt-delivery dialog guard.** More generally, herdr-draft never sends
  a queued prompt into a pane it has not positively confirmed is safe to
  type into. Before sending it reads the pane and checks it against a small,
  explicit list of known dialog signatures (`internal/plan/dialog.go`); on a
  match — or if the pane cannot be read at all — it stops, saves the prompt
  text and names the file on the failure screen.

  This exists because on herdr 0.8.2 the trust prompt above was reported as
  idle/ready-for-input, indistinguishable from the agent actually being
  ready: trusting that signal sent the prompt text into a screen that is not
  a text field, and the trailing Enter confirmed the highlighted option
  ("No, exit"), killing the freshly launched agent with no error shown
  anywhere. herdr's detection has since learned that one screen, but it
  knows that screen and not the class — a login-method chooser or a
  permission prompt is still a dialog it will report as ready — so the guard
  stays.
- **Popup panes are only reachable via keybinding or CLI.** Plugin actions
  do not appear in herdr's own context/global menus in plugin v1 — see
  [Keybinding](#keybinding) for both routes.

## Troubleshooting

**Popup won't open / `ui_busy` error.** herdr's own API returns `ui_busy`
("popup panes can only open from the normal workspace view") when another
herdr modal is already open. Close whatever herdr dialog is currently up
and try again.

**Which version am I running?**

```bash
herdr-draft version     # --version and -V work too
```

```
herdr-draft 0.1.0
  plugin id  zvibaratz.draft
  build      v0.1.0-3-gabc1234
  built with go1.26.4
```

The first line is the version `herdr-plugin.toml` declares, which is what
herdr shows for the install. `build` is the exact commit, and appears only
when there was one to record — herdr's own build step runs a plain argv
with no shell, so an installed plugin has no `git describe` to stamp and
the line is simply absent. Quote all of it in a bug report.

**"herdr-draft: ..." plain-text error, nothing renders.** herdr-draft
refuses to open the form at all — rather than opening it broken — when it
can't reach the herdr socket, when `$HERDR_PLUGIN_CONTEXT_JSON` is missing
or malformed, or when `config.toml` fails to parse. The first two say what
to check and what to run; the config one names the file and the line.

**"$HERDR_PLUGIN_CONTEXT_JSON is not set" when you run the binary
yourself.** Expected, and not a fault: herdr sets that variable when it
launches the plugin, so there is no invocation context — no workspace, no
tab, no pane — when you start the binary from a shell. Use the popup, or
`herdr-draft create`, both of which the message names. `just smoke` runs
the real form in an ordinary pane by building a context by hand.

**Worktree creation fails with git's "dubious ownership".** git refuses to
operate on a repository owned by another user, and worktree creation is
where you meet it. Set `trust_repository = true`
under [`[worktree]`](#worktree) — herdr then trusts the repository for that
one request, which is narrower than the machine-wide
`git config --global --add safe.directory <path>` you would otherwise need.
Requires herdr ≥ 0.9.0.

**Plugin actions not appearing in herdr's menus.** Expected in plugin v1 —
see [Known limitations](#known-limitations). Use the keybinding or the CLI
route from [Keybinding](#keybinding).

**There is no `account` row.** It only renders when clauth is configured
*and* at least two profiles exist, checked once at form startup. Fewer than
two profiles, or clauth not detected at all, means the row is simply absent
— a static, by-design check, not a bug. Same for the `issue` row and
Linear.

**The `account` row says `unavailable`.** Different situation: clauth *is*
installed, and herdr-draft could not read it — it exited non-zero, or its
`--json` output did not parse. The reason is on the row. The row is inert
and the focus ring skips it; it goes back to normal, or back to being
absent, once clauth works.

**The `issue` row says `unavailable`.** Your key source is configured but
failed; the reason is on the row itself and in its panel. See
[`[linear]`](#linear).

**"prompt not sent" after a submit.** The session was created and the agent
started, but the prompt couldn't be delivered (the agent was showing a
dialog, or `agent prompt --wait` timed out). The failure screen says
`prompt not sent — saved for manual paste:` with the full path on the line
under it — `unsent-prompt.txt` in the plugin state directory — so you can
paste it into the agent by hand. The session itself is fine: press `k`
(keep it).

**A row shows a value you didn't choose.** Something above your own
`config.toml` supplied it. Check the row's panel for
`from .herdr-draft.toml`, and see
[Where defaults come from](#where-defaults-come-from) for the rest of the
chain. `⌃R ⌃R` clears the form back to those resolved defaults.

## State

herdr-draft keeps a small amount of loss-tolerant state in
`$HERDR_PLUGIN_STATE_DIR`, all of it safe to delete:

- `recents.json` — recently used project directories, offered as candidates
  on the project row. Written after each successful submit.
- `last-used.json` — the agent kind, placement, and worktree toggle from
  your last successful submit, anywhere; the form defaults to them next
  time.
- `projects.json` — the same, but per project: what you last chose in each
  repository, keyed by its git root so a linked worktree and its origin
  share one entry. Capped at 50, evicting least-recently-seen. Beats
  `last-used.json` when you return to a project you have used before.
- `linear-cache.json` — the last Linear issue list, rendered instantly at
  form-open while a fresh one loads.
- `unsent-prompt.txt` — a prompt that couldn't be delivered, kept for
  manual paste (see Troubleshooting above). Overwritten by the next one.

## Testing

The gate is `just check`, and it is **four** steps:

```bash
test -z "$(gofmt -l .)"   # formatting
go vet ./...
just unused               # staticcheck -checks U1000 -tests=false ./...
go test ./...             # unit + golden-frame tests, no I/O
```

It runs on every push and pull request
([`.github/workflows/check.yml`](.github/workflows/check.yml)), on Linux and
macOS.

**`just unused` is a real gate, not advice.** `go vet` does not detect
unused unexported code *at all*, and that blind spot has shipped two
defects here: a whole cascade of drop-line handling survived the deletion
of the only path that called it, and half of a filter-count feature sat in
a green tree defined and called by nothing. `-tests=false` is the
load-bearing flag — without it, a symbol kept alive only by the test that
exists to call it counts as used, which is the exact shape both defects
had. A symbol that is deliberately test-only carries a
`//lint:ignore U1000 <reason>` at its declaration instead.

staticcheck is the one dev tool not carried in `go.mod`. Its version is
pinned in the justfile as `staticcheck_version`, which the workflow reads
too, so run `just unused` with it missing and the error names the exact
install line — `go install honnef.co/go/tools/cmd/staticcheck@<pinned>`, or
the `go run` spelling if you would rather not install it. It is not a
`go.mod` tool directive on purpose: that would drag this module's own `go`
line up to whatever the linter needs, and herdr builds the plugin with
`go build` on the *user's* machine, so a dev tool must not narrow who can
install.

The golden frames under `internal/*/testdata/` are the most exact
description of the shipped screens there is — read one before trusting a
description of the UI, this file's included. They also prove only the
states someone thought to fixture: this project shipped a defect in the
form's *opening* state through fifteen green commits.

Regenerating them is **per package**, not repo-wide:

```bash
go test ./internal/form/ -update
go test ./internal/app/ -update
```

`go test ./... -update` fails. The flag is registered per test binary, in
those two packages only, so a repo-wide run errors on every other package.

Manual release smoke (Path A/B × worktree on/off, plus the headless
`create`, the repo config and per-project memory) is documented separately
in [`docs/manual-smoke.md`](docs/manual-smoke.md) — deliberately not part
of CI, since every scenario creates real herdr sessions. Read its warning
before running `herdr-draft create` in a real session.

## Contributing & security

Patches are welcome — [`CONTRIBUTING.md`](CONTRIBUTING.md) covers the gate,
the golden-frame workflow, the spec-citation convention, and the features
that were explicitly declined. herdr does not sandbox plugin code, so
[`SECURITY.md`](SECURITY.md) states this plugin's actual surface (the
command it runs for you, the credential it handles, and the trust boundary
around a repository-committed `.herdr-draft.toml`) and how to report
something privately.

## License & provenance

herdr-draft is MIT licensed (see `LICENSE`). It ports and adapts UI code
from [Atrium](https://github.com/ZviBaratz/atrium) (AGPL-3.0) under a
license grant from Atrium's own author, and translates visual conventions
and default palette values from herdr (Apache-2.0). Full attribution and
the audited file-by-file provenance are in `NOTICE` and the v1 design
spec's §14.
