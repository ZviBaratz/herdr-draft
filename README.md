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
  placement  the worktree's own space
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
  placement  the worktree's own space
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
[clauth](https://github.com/uwuclxdy/clauth)), an initial prompt, and
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

**Nine rows, one line each**, in this order. A row states what the session
will be, not which knob is set, and it never moves: the row positions are
fixed whatever has focus and whatever the window height is.

| Row | Reads | When it can't be set |
|---|---|---|
| `issue` | `ENG-101 · fix login redirect loop`, or `none` | `unavailable  <reason>` |
| `title` | what you typed, or a dim `untitled` | — |
| `prompt` | the first line, plus a dim ` +N more`, and a dim ` · reap when done` when the prompt will end with pane-reaper's instruction | — |
| `project` | the path, `~`-shortened | `invalid` / `not a repository` |
| `worktree` | `on · <branch> ← <base>`, `on · from <base>` before a title exists to derive a branch from, or `off` | `not a git repository` |
| `placement` | `new space` / `tab here` / `split here` / `tab in <space>` | `the worktree's own space`, whenever the worktree is on |
| `agent` | `claude` | — |
| `options` | `opus · effort xhigh · plan mode`, the options you chose; a dim value is what `[agents.extra_args]` passes instead; `claude's own settings` when nothing is set anywhere | `none for <kind>`, for an agent kind that declares no options |
| `account` | `personal · Max 20x · 5h 12% · 7d 40%`, or `active · …` when nothing is pinned | `account pinning only applies to claude` |

`issue` is absent unless Linear is configured, and `account` unless clauth is
configured with at least two profiles — both static, startup-time checks
(see [Configuration](#configuration)). With neither, the form is seven rows.
That is the shape most people will see.

**The popup is a fixed 104×32 cells.** That is a manifest value, not a
proportion of your terminal, and it is a visible change from the old
percentage-sized popup: on a large terminal the dialog is now a card rather
than a wall. herdr clamps it down to the terminal and never up, so an 80×24
terminal gets a full-screen popup rather than an overflow.

**One panel**, under the second rule, belonging to the focused row and to no
other: the candidate list for `issue`, `project`, `agent` and `account`; a
`keep · reap` line over the textarea for `prompt` (see
[`[reaper]`](#reaper)); the verdict line (`branch will be …`) plus the
sessions that were open when the form opened, for `title`; a three-part
`off · on` / `branch` / `base` editor for `worktree`; one chip line per
option for `options` (see [`[agents.options]`](#agentsoptionskind)). Branch and base are not
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
| `←` `→` | chips: worktree on/off, placement, agent favorites, an option's value |
| `⇥` in a picker | complete, then advance |
| `↵` | create, from a non-empty title, from the prompt, or from the button; advance from anywhere else |
| `⌃S` | create, from anywhere |
| `⌃J` (or `⇧↵`, `⌥↵`) | newline in the prompt |
| `⌃X` | in the prompt: `keep` or `reap` — whether the prompt ends with pane-reaper's instruction |
| `⌃R` `⌃R` | clear back to the resolved defaults |
| `esc` / `⌃C` | cancel |

The mouse works too: click a row to focus it, click a panel line to select
it, and the wheel scrolls the panel.

### The project row

The project row has two modes, and switches between them on what you type:

- **Fragment** (anything not starting with `/`, `~` or `.`) fuzzy-filters a
  fixed pool of candidates: the current space's repo root first, then the
  current workspace cwd, then every open herdr workspace's own worktree
  root, then your recents. Opened in a linked worktree's own space, the
  first candidate, and the default, is that worktree's checkout, with the
  repository root next: the same project `create` run there resolves to.
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

The first time the form lands on a given repository it also runs a
background `git fetch --prune` there — once per repository per form open,
with no user action — so the worktree row's base picker offers remote
branches that arrived since you last fetched. A branch only the remote has
is offered as `origin/<name>`, the name git resolves; one you also have
locally is listed once, under its local name. A session cut from one
does not track it (see [Headless `create`](#headless-create)). **That contacts the
repository's default remote** — the current branch's upstream, otherwise
`origin` — with whatever credentials git would normally use, including a
`core.sshCommand` or `GIT_SSH` of your own. It runs with terminal prompts
and ssh interaction disabled and under a 30-second deadline, so a remote
that wants a password fails instead of prompting on top of the form, and
one that never answers cannot hang the popup.

## Requirements

- herdr ≥ 0.9.0 (`min_herdr_version` in `herdr-plugin.toml`). herdr refuses
  to install or link a plugin whose minimum is newer than the running binary,
  so an older herdr gets a clean refusal rather than a plugin that loads and
  then misbehaves.
- [clauth](https://github.com/uwuclxdy/clauth) ≥ 0.14.1 for account-pinned
  (Path B) launches — see [clauth integration](#clauth-integration) below.
  clauth is entirely optional: without it, herdr-draft still works for
  manual/Linear-seeded session creation (Path A), it just has no `account`
  row to pin a profile on.
- Go 1.25+ on the machine doing the installing. `herdr plugin install` runs
  `herdr-plugin.toml`'s `[[build]]` — `go build -o bin/herdr-draft
  ./cmd/herdr-draft` — there and then; `herdr plugin link` does not build at
  all, so a linked checkout needs `just build` first. See
  [Install](#install).
- Linux or macOS. The manifest declares `platforms = ["linux", "macos"]`,
  so herdr refuses the install on Windows rather than registering a plugin
  that cannot launch: the action entrypoint is `sh -c`, and the build
  produces an extensionless binary. Windows support is a feature nobody
  has built or tested, not an oversight in packaging.

## Install

```bash
herdr plugin install ZviBaratz/herdr-draft
```

herdr clones the repository into a temporary checkout, prints an install
preview, asks you to confirm, and only then runs the manifest's `[[build]]`
command — `go build -o bin/herdr-draft ./cmd/herdr-draft`, with the plugin
root as its working directory — before moving the checkout into place. The
build runs on **your** machine, which is why [Requirements](#requirements)
asks for Go 1.25+ and why this repository pins no dev tool that would raise
that floor.

`--ref <tag-or-branch>` pins a version. `-y` / `--yes` skips the
confirmation, and is **required** rather than optional when stdin is not a
terminal, so a scripted install that omits it exits 2 with a message rather
than hanging.

If the build fails, the install fails with it and nothing is registered; a
build that edits `herdr-plugin.toml` on its way past is also refused, so the
manifest you confirmed is the manifest you get.

**This route has been run from clean.** On 2026-09-09, on a freshly
provisioned Ubuntu 22.04 x86_64 machine that had never had herdr or this
plugin on it: git, then herdr 0.9.0's own release binary and Go 1.25.14, and
nothing else. `herdr plugin install ZviBaratz/herdr-draft -y` printed the
preview, ran `[[build]]`, registered the plugin and left a working binary —
`herdr plugin list` reported `zvibaratz.draft (herdr-draft) enabled`, and the
installed `herdr-draft version` reported `0.1.0` with the resolved commit.
The build wants no dev tooling: it is a plain `go build`, and it ran with no
`staticcheck` anywhere on the machine.

Three things that run did not settle:

- **The popup was not exercised.** That needs an attached TUI client, and is
  tracked separately (#45). Nor was a session created end to end, which also
  needs a running herdr server and agent credentials on the machine.
- **It was the author's own cloud instance, not a stranger's.** What it
  proves is that the install works on a machine nobody had prepared for it,
  which is the risk worth retiring; a genuine third-party first-install
  report is still welcome. If it fails for you, please open an issue — the
  `[[build]]` step is precisely the part the development route below never
  runs.
- **An installed plugin's `herdr-draft version` prints no `build` line**, and
  that is correct rather than a stamping failure: herdr runs `[[build]]` as a
  plain argv with no shell, so there is nowhere for `git describe` to run. A
  `commit` line is still there, from the Go toolchain's own VCS stamp — which
  a build from a git *worktree* would not produce either.

### Development: link a local checkout

```bash
just build && herdr plugin link "$PWD"
```

`<path>` is a positional argument (a plugin directory containing
`herdr-plugin.toml`, or a direct manifest path) — not `--path`. herdr
registers the plugin globally for the current user and every session sees it
immediately, without restarting anything.

**`link` does not build, and that is the one thing to remember about it.**
herdr runs `[[build]]` from `install` only —
[`run_plugin_build_commands` has a single call site, inside
`plugin_install`](https://github.com/herdrdev/herdr/blob/v0.9.0/src/cli/plugin.rs#L210).
Linking a tree with no `bin/herdr-draft` in it registers an action that
launches nothing, which is why the command above builds first.

The two routes do not stack: `install` refuses outright while a local link
with the same plugin id is registered — *"plugin zvibaratz.draft is already
linked from a local path; uninstall/unlink it before installing from
GitHub"* (`herdr:src/cli/plugin.rs`, `ensure_replacement_allowed`). Moving
from a development link to a released install means removing the link
first.

## Keybinding

Plugin actions don't appear in herdr's context/global menus in plugin v1 —
keybinding and CLI are the only discovery surfaces (see
[Troubleshooting](#troubleshooting)). Bind a key in herdr's own
`config.toml`:

```toml
[[keys.command]]
key = "prefix+n"
type = "plugin_action"
command = "zvibaratz.draft.open"
description = "new session"
```

`zvibaratz.draft.open` is herdr's qualified id for herdr-draft's one
action: plugin id `zvibaratz.draft` and action id `open`, both from
`herdr-plugin.toml`. herdr matches a `plugin_action` keybind against the
bare action id *or* the qualified one, and the qualified form is the one
worth binding — `open` on its own is generic enough that another plugin
could answer to it. You can also invoke it directly from a shell:

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
`--reap` / `--no-reap`, `--placement`, `--workspace`, `--agent`, `--model`,
`--effort`, `--permission-mode`, `--account`, `--issue`, `--json`,
`--dry-run`, `--on-failure keep|clean`. `herdr-draft create --help` lists
them.

`--model`, `--effort` and `--permission-mode` are the `options` row's flags.
They apply only to an agent kind that declares them — `claude`, today — and
are refused with exit 2 for one that does not. `inherit` sends no flag of
its own even when `config.toml` sets a default, though
`[agents.extra_args]` can still pass one:

```bash
herdr-draft create --title "design the cache" --effort max --permission-mode plan
herdr-draft create --title "quick fix" --model sonnet --effort inherit
```

A title holds no control characters and at most 32 runes, which is what
the form's `title` row holds, and `create` keeps a `--title`, or the title
`--issue` takes from Linear, to the same. A tab, CR or LF becomes a space,
any other control character or invalid UTF-8 is dropped, and what remains
is cut to 32 runes. A line on stderr says so whenever that changes the
title, and when a `--title` that cleans to nothing gives way to the
issue's.

**Anything you don't pass resolves exactly the way the form resolves it** —
see [Where defaults come from](#where-defaults-come-from); the command and
the form go through the same resolver, and a test drives both over the same
files to keep them from drifting. The project directory defaults to the
working directory. A successful create records what it used, so the form's
next open defaults to it too.

`--base HEAD` and `--base @`, like any other spelling of HEAD itself
(`HEAD^0`, `@{0}`), mean what leaving `--base` off means when nothing else
chooses a base: the form's `HEAD` row, which is no base at all. With a
worktree, a `--base` that names no commit in the project is refused before
anything is created, rather than handed to herdr to fail at the worktree
step. Without one, nothing reads `--base`, and it is not checked.

**Inside a linked worktree** (another session's, say) the project is that
checkout. herdr will not create a worktree *from* a linked checkout, so a
worktree session is created from the repository's primary checkout
instead. Its base, whether chosen or not, is resolved in the linked
checkout first, so `HEAD` is the commit you are on there. With no base
chosen, `--json` reports that commit as `base`. The commit is never
remembered. Without a worktree, the session runs in the checkout itself.
The popup does the same. A bare repository has no primary checkout to
create from, and herdr's own refusal stands.

Progress goes to stderr, one line per step; the result to stdout, or a
single JSON object with `--json` (which also carries a prompt the dialog
guard withheld, since a headless caller has no pane to recover it from, and
a `provenance` map naming the tier each value came from — `flag` for the
ones you passed, and `checkout` for a `base` that is the commit a linked
worktree is on). A `mark_ready: true` means the prompt carried
pane-reaper's instruction. `agent_options` holds the session options chosen
for the agent, and `launch_options` what its command line actually carried
for each: the chosen ones, plus whatever `[agents.extra_args]` passes for
the rest, which `provenance` names `extra_args`. `agent_args` is the whole
argument list the agent is started with, so an argument
`[agents.extra_args]` passes besides those three flags, one that skips its
permission prompts included, is reported too. It is what `create` passes
after the agent's command: a `[clauth] launcher`, or a shell function
standing in for that command, can still change what arrives. A dry run's
object also carries `account_usage`: the account the session would bill
(`profile`, clauth's active one when nothing is pinned) and each of its
usage `windows` as clauth reports them, with
`label`, `utilization_pct` and `resets_at`. It is absent whenever that
cannot be said: another agent kind, clauth switched off, not installed or
failing, a status whose schema it does not know, or a profile it does not
report. It never prompts. Exit codes:

| Code | Meaning |
|---|---|
| 0 | created |
| 1 | the plan started and failed, and part of the session may exist (`--on-failure` applied) |
| 2 | bad usage, or a request that cannot be resolved |
| 3 | herdr is unreachable — found before the plan starts |
| 4 | the plan started, and its first step failed before making anything: nothing exists |
| 5 | a check did not answer in time: nothing was created |

**Exit 5 is the refusal that is not the caller's to fix.** Every question
the pre-flight asks the filesystem or git — does this directory exist, is
it a repository, what is its root, does this branch already exist, what
does this base name — gives up after thirty seconds instead of waiting for
good. Without that, a `create` on a stalled network mount sat there with no
output and no exit code for as long as the mount stayed stalled, which is
fine for a person who can press `⌃C` and useless for the script or agent
that is the usual caller. The line on stderr names which check ran out, and
nothing was created.

It is not exit 2: that one means "fix the command and re-run", and there is
nothing in the command to fix. It is not exit 3 either, which would send
you to look at a herdr that is fine. The popup makes the same distinction
on screen, with a shorter budget, since there is someone holding the key.

The budget is not configurable, by design. It is a safety bound rather than
a tuning knob — nothing you could set it to would make a hung mount answer
— and `[timeouts]` values are not validated, so one typo would turn it into
"give up immediately" instead.

Exit 4 needs evidence, not just a failed first step: herdr refused that
step before acting, or it was never asked. A worktree step that herdr
fails *after* git has run can leave the branch, its checkout and even a
workspace on it, and that is exit 1, with no space reported for
`--on-failure clean` to remove — `clean_refused` says so. With `--json`,
exit 4 still prints the object — `ok: false`, `failed_step` and `error` —
with no `workspace_id`/`space_*` ids, since there is nothing for them to
name.

**`--dry-run` shows what a create would make, and makes nothing.** It
runs every check a create runs before it starts, with the same exit 2,
exit 3 and exit 5 and the same reasons, then prints what it would create
and stops.
There is no workspace, no branch and nothing remembered. With `--json` it
prints the object a create would, with `dry_run: true` and without the ids
or `prompt_status`, which only a run can know. `--account auto` asks your
picker with the picker's own `--dry-run`, so a preview spends no account,
though the real pick can come out differently. The `/spawn` skill runs one
before it shows you a command to approve, so the confirmation can say what
the session will run with.

**A prompt has three fates, not two.** `prompt_status` names which:
`sent`, `unsent`, or `unconfirmed`. The third means delivery is unknown,
and it is reached five ways:

- **the wait gave up.** `herdr agent prompt --wait` returned before the
  agent's status changed, which is *not* proof the prompt failed to
  arrive — herdr 0.9.0 completes that wait only on observed
  working-or-blocked activity, so a large prompt into an agent slow to
  paint its transition times out while running perfectly well.
- **the send stalled twice.** A *stall* is herdr seeing no working and no
  blocked activity at all after a send — positive evidence the agent
  processed nothing, which is why herdr-draft pauses a couple of seconds
  and sends once more. If that stalls too, it stops. herdr writes the
  prompt text and Enter before it starts watching, so two sends later
  "it never arrived" is not something this command knows, and the pane
  may hold two copies.
- **something failed after the text had already gone out.** Once herdr has
  typed the prompt, nothing later in the same step can put delivery back to
  "never arrived". In practice this is the retry above meeting a dialog: the
  first send stalls because the agent's screen had not finished painting,
  and by the time the retry looks, the dialog is up — so herdr-draft
  correctly refuses to type into it, having already typed once.
- **the send went out and was not seen to land.** herdr-draft reads the
  pane straight after every send herdr accepts. If it finds a dialog with
  none of the prompt on it, the prompt's Enter may have answered that
  dialog. If the pane stops answering, or herdr's own wait reports the
  agent no longer running, the agent most likely exited. Either way the
  text and Enter went into the pane, which is the rule above applied to a
  first send.
- **herdr failed the send itself.** herdr answers `agent_prompt_failed`
  both when it refused the send before typing anything and when the pane's
  terminal failed partway through, and it says nothing that tells the two
  apart — so the pane may hold none, some or all of the prompt. It is the
  one shape that is not evidence the text went out; it is here because
  "never arrived" is not something this command knows either. Its likeliest
  cause is the pane closing under the send, so there may be no pane left
  to read. If there is none, the text is yours to reuse.

Read `prompt_status` rather than inferring from the other fields:

| `prompt_status` | `prompt_sent` | the text comes back as | what to do |
|---|---|---|---|
| `sent` | `true` | — | nothing |
| `unsent` | `false` | `unsent_prompt` | **read the pane first**, then resend |
| `unconfirmed` | *absent* | `unconfirmed_prompt` | **read the pane. Never resend.** |

**`unsent` is not permission to resend blind.** It means herdr never
accepted the text, not that the pane is ready for it, and it covers two
shapes:

- **the guard refused to send**, because the pane was showing a blocking
  dialog or had not painted yet. Resending types the prompt into that
  dialog, where the trailing Enter answers whichever option is highlighted.
  That is the failure #116 exists to prevent, arriving through the front
  door.
- **the plan stopped before the prompt step**, so nothing was typed — and
  there may be no pane to read at all, if it failed before one was made.

So read the pane, clear whatever it is showing or hand it to someone who
can, and only then resend. If there is no pane, there is nothing to clear
and the text is simply yours to reuse.

`prompt_sent` is absent for `unconfirmed` because neither `true` nor
`false` is a statement this command can make. The two keys carry different
instructions, which is why the text comes back under one or the other:
`unsent_prompt` is text to put back in front of the agent once you have
looked at the pane, while `unconfirmed_prompt` may **already** be in front
of it. Resending that one is how an agent that is working gets its
instructions twice.

`--on-failure clean` is **refused** for an `unconfirmed` prompt, with the
reason in `clean_refused` — which names the evidence for whichever shape it
was. After a wait that gave up, the session may have an agent working in
it right now and cleaning up would kill it mid-turn. After two stalls, a
send that was not seen to land, a send herdr failed partway, or any
failure that followed a send, what the pane holds is unknown and worth
reading before anything is removed.
The ids are still reported in every case, so nothing is stranded without
a way back to it.

**The reported ids name the agent, not the space.**
`workspace_id`/`tab_id`/`pane_id` — and `workspace=`/`tab=`/`pane=` on the
plain line — are where the agent ended up, which is what a script sends its
next keystroke to. `--json` also carries `space_workspace_id`/`space_tab_id`/
`space_pane_id`: the space the run established, and the one a failure
message names. The two differ in one case. When herdr answers `worktree
create` with a workspace that was already open for that checkout, the agent
gets a fresh tab in it rather than whatever pane herdr handed back. Both
triples are always present when known, so comparing them tells you whether
the agent is where the space's own pane is. `--on-failure clean` removes
only what the run actually created: the tab for `tab-here` or `tab-in`, the
pane for `split-here`, and the workspace or the worktree otherwise.

**A cleaned worktree takes its branch with it**, so a retry with the same
title is not refused over a branch the failed run left behind. herdr's
`worktree remove` keeps the branch by design, so herdr-draft deletes it
itself, with `git branch -D` once the checkout is gone, and only when all
three of these hold:

- **the run made it.** herdr checks an existing branch out rather than
  refusing it, and answers the same either way, so herdr-draft looks just
  before the create, and goes by the branch herdr's reply says it used. A
  branch that was already there, or that it could not check for, is kept.
- **it holds nothing its base does not**, counted from the commit the
  worktree was cut from. The clean is already refused for a checkout with
  commits of its own. The branch is counted again just before anything is
  removed, and one that has gained a commit since stops the whole clean,
  with the reason in `clean_refused`.
- **nothing else holds its commits.** The agent keeps running while herdr
  removes its checkout, so a commit can still land then. Once the checkout
  is gone the branch is looked at once more, and one holding a commit no
  other branch does is kept.

`deleted_branch` names the branch when it went, and `deleted_branch_tip`
the commit it pointed at: `git branch <deleted_branch> <deleted_branch_tip>`
puts it back. A branch the clean left in place is `kept_branch`, with
`kept_branch_reason` saying why. The clean still happened, so `cleaned` is
`true`, but a retry that derives the same name is refused until the branch
is gone. The popup's remove does the same. Its line above the buttons says
whether the branch goes, and a remove that had to keep it after all says so
before it closes. Neither closes the repository's own workspace, which
herdr opens alongside a repository's first worktree; close that yourself if
you do not want it.

**A worktree on a branch it did not ask for is a failed create.** herdr's
reply names the branch git actually checked out, and herdr-draft holds it
to the one requested. git 2.53, handed a base that names a branch only a
remote has by its bare name, makes a local branch named after the base
instead, and herdr reports success. The base list offers such a branch as
`origin/<name>` and `--base` refuses a name that resolves to nothing, so
this check is for any way past both. It fails the worktree step after
herdr has made the checkout, so `--on-failure` and the popup's keep or
remove apply as they do to any later failure, and the remove keeps the
branch git made: nothing shows this run asked for it.

**A session's branch does not
track the remote branch it was cut from.** herdr cuts the worktree without
`--no-track`, so under git's default `branch.autoSetupMerge` a base such as
`origin/develop` becomes the new branch's upstream, and a plain `git push`
from the session would be refused (`push.default=simple`) or would push onto
`develop` itself (`push.default=upstream`). herdr-draft removes that upstream
once the worktree exists. It does so only on a branch this create made, only
when the upstream is the remote-tracking branch the base names, and not when
you set `branch.autoSetupMerge = always`, which asks for tracking everywhere.
A local base such as `main` is left alone: under the default it sets no
upstream, and under `inherit` the branch takes `main`'s own. The branch gets
its own upstream on its first `git push -u`, or on a plain `git push` under
`push.autoSetupRemote`. If the removal fails, the create still succeeds and
`create`'s worktree line says so, with the command that finishes it (`git
branch --unset-upstream <branch>`). The popup marks its worktree row `!` and,
once the session is created, stays open with the whole reason until you close
it with `esc` or `enter`. It does that for any step that finished with a
warning (#230); with none, it closes by itself as before.

**A worktree session runs in the worktree's own space**, which herdr's
sidebar groups under the repository's. `--placement` therefore applies only
with `--no-worktree`. With a worktree, `--placement` other than `new-space`,
and `--workspace`, are refused (exit 2) rather than silently dropped, and a
remembered or configured placement simply does not apply.

`--placement tab-here` and `split-here` need to know where "here" is, and
read `HERDR_WORKSPACE_ID` / `HERDR_TAB_ID` / `HERDR_PANE_ID`, which herdr
sets in every pane's shell. `new-space` needs none of them, and neither does
`tab-in`, which names its workspace outright: the one already holding the
project's checkout, or whatever `--workspace <id>` says. A missing one is
named exactly.

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

If the thing running `create` is an *agent* rather than you, it will not
find this section on its own. Install [the `/spawn` skill](#the-spawn-skill),
which carries these three exports as its second paragraph.

## The `/spawn` skill

`herdr-draft skill` prints one `SKILL.md` to stdout. Installed into your
personal skill directory, it makes `/spawn` invocable in every Claude Code
session on the machine — and, more to the point, makes an agent that forms
the intent to *hand work off* reach for `herdr-draft create` instead of
writing a handoff document and stopping, or improvising a topology.

```bash
root=$(herdr plugin list --json --plugin zvibaratz.draft | jq -r '.result.plugins[0].plugin_root // empty')
mkdir -p ~/.claude/skills/spawn
"$root/bin/herdr-draft" skill > ~/.claude/skills/spawn/SKILL.md
```

The directory name has to match the frontmatter `name`; both are `spawn`,
and a test holds this section, the emitted document and the frontmatter to
that one value.

**The lookup is not decoration.** `herdr-draft` is not on `PATH`: the
binary is built into `bin/` inside the plugin's install root, and herdr
owns where that root is. `herdr plugin list --json` reports it as
`plugin_root`, for a GitHub install and a `herdr plugin link` checkout
alike, and works without a running herdr server. That is the one answer
to rely on, in a script as much as here. Do not search
`~/.config/herdr/plugins` for the binary instead: more than one copy can
be there, and the directory layout is herdr's to change. The emitted
document names its own absolute path so that no agent has to repeat this
lookup.

It covers what the sections above cover, aimed at an agent rather than at
you: the `HERDR_PLUGIN_*` exports and what resolving without them costs,
whether the session gets a worktree and what does not travel into one,
which placement to pick, choosing the model, effort and permission mode for
the task rather than inheriting them, how to write and pipe in a prompt,
dry-running the command and confirming it with the user once, showing each
choice with its reason and what it resolves to, and reading `prompt_status`
and the pane afterwards rather than trusting the exit code.

**The emitted file is machine-specific and is not meant to be committed.**
It names the absolute path of the binary that printed it, so nothing has
to go on `PATH` and no agent has to go looking. herdr puts a GitHub
install in a directory named for the plugin id, so upgrading with
`herdr plugin install … --ref` keeps that path. Switching between a
GitHub install and `herdr plugin link` moves it. **Regenerate the copy
after any upgrade anyway**, with the same three lines: what it says about
the command changes with the version.

The file carries the version it was generated from, in its last section.
If that does not match `herdr-draft version`, the copy is stale.

`herdr-draft skill` writes nothing anywhere: it prints, and you redirect.
If it cannot work out its own path it still prints a usable document —
naming the plain command `herdr-draft` — and says so on **stderr**, so a
redirect never puts a warning inside the file.

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
  and it may not start with `-`: herdr passes the branch to `git worktree
  add -b`, which accepts it but hands it on to a `git branch` child that
  reads it as an option. Nor may it start with whitespace, even a no-break
  space git would accept: herdr trims the branch name it is handed, so the
  branch made would not be the one derived. An unusable value is ignored
  and the default takes over; it never stops herdr-draft from opening. The
  popup shows the reason on the `worktree` row's panel, and `herdr-draft
  create` prints it on stderr — except in a repository whose
  `.herdr-draft.toml` sets its own `branch_prefix`, which would have
  overridden this key even if it were valid.
- `default_worktree` (default: `true`) — whether the worktree row starts on
  or off for a git target.
- `default_placement` (default: `"new-space"`) — where a session without a
  worktree lands: `new-space`, `tab-here`, `split-here`, or `tab-in`. With
  the worktree row on it does not apply: a worktree session always runs in
  the worktree's own space, which herdr groups under the repository's.

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
Two keys decide how a pinned account is launched, and they are **one
decision**: `launch` picks the mechanism, `launcher` configures one of them.
Read them together.

| you want | set |
|---|---|
| the default — `clauth start <profile> --` | nothing |
| your own account-switching wrapper, by name | `launcher = ["claude-as", "{account}"]` |
| bare `claude` under a picker's credential dir | `launch = "wrapper"` |

`launcher` configures the `launch = "start"` mechanism only. Under
`launch = "wrapper"` there is no argv template to configure, so a `launcher`
set alongside it is ignored.

**You are told when one of them loses.** A `launcher` ignored under
`"wrapper"`, a malformed `launcher` and an unrecognised `launch` each produce
a warning that says which key was ignored, why, and what was used instead.
`herdr-draft create` prints each one whole on stderr. The popup puts each on
one line of the `account` row's panel, cut to the panel's width — the reason
comes first so that it survives a long `launcher` — whenever that row lets you
pin an account, which is the only time these keys are used. A malformed
`launcher` set beside `"wrapper"` gets both warnings: that it is malformed,
and that wrapper mode would not have used it anyway. The popup also shows the
launch itself: its progress row names the program and account it launched
through, and says so when wrapper mode fell back for want of a `config_dir`.

- `launcher` (default: `["clauth", "start", "{account}", "--"]`) — the argv
  that starts a pinned account, with `{account}` standing for the profile
  name. Used when `launch` is `"start"`, which is the default. It is a
  whole-argv **template**, not a prefix: `clauth start` needs its trailing
  `--` and another launcher may not want one, so nothing is appended on your
  behalf except the agent's own extra args.

  Exactly one `{account}` is required. Any other *shape* — none, two, an
  empty list, a blank element — is ignored and the default used instead
  (reported on the `account` row's panel and on `create`'s stderr), because a
  template with no `{account}` would launch on whichever profile clauth has
  live and quietly spend the wrong budget.

  A wrong *type* is not that case and does not degrade: `launcher = "clauth
  start {account} --"` — a string, which is what a command line looks like —
  fails at parse time and the plugin refuses to open, naming the line and the
  key. That is how this file treats a wrong type on every key, but `launcher`
  is the first list-typed one, so it is worth stating: it takes a **list of
  arguments**, not a command line.

  Your agent's `[agents.extra_args]` and its session options are appended
  *after* the whole template, so a launcher must be a program or function
  that accepts them. An `sh -c "…"` wrapper is accepted by the validator but
  will not receive them — they land as the inner shell's `$0` and `$1` and
  never reach the agent, while the launch step still reports success.

  The reason to change it: `clauth start` costs the session its herdmates
  team lead, since it bypasses the shell function that sets
  `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS` and launches through herdmates.
  If you have a wrapper of your own that keeps both, name it here — e.g.
  `launcher = ["claude-as", "{account}"]`. herdr *types* this argv into the
  pane's shell rather than exec'ing it, so a shell function works, but only
  in a pane whose shell defines it; that is why the shareable default stays
  `clauth start`.

- `picker` (default: unset, meaning "no picker") — an executable implementing
  the [account picker protocol](#account-picker-protocol), used by the
  `account` row's `auto` selection and by `create --account auto`. A command
  name resolved on `PATH`, or an absolute path.

  **It must be named here.** herdr-draft never goes looking for a picker, and
  it will not adopt one it happens to find on `PATH` — finding a program with
  the expected name is not the same as finding the program you meant, and a
  plugin that silently routes account credentials through a same-named
  stranger is worse than a plugin with no picker. Even a named one is probed
  once at startup (one `--json --dry-run`) and is used only if the answer
  carries the documented keys; if it does not, the `account` row says so
  rather than quietly going without.

- `launch` (default: `"start"`) — how a pinned account is launched.

  - `"start"` types `clauth start <profile> -- <extra args>` into the pane.
    clauth builds the per-profile `CLAUDE_CONFIG_DIR` and execs Claude under
    it. This means the same thing on every machine that has clauth, which is
    why it is the default.
  - `"wrapper"` types `CLAUDE_CONFIG_DIR=<dir> claude <extra args>` instead,
    leaving your pane's own login shell to resolve `claude`.

  **`"wrapper"` is only worth setting if your shell defines `claude` as a
  function.** That is the entire reason the mode exists: a wrapper function
  can register a session holder, launch through a team-lead helper, or
  anything else it likes, and `clauth start` cannot call it. If `claude` is
  the plain binary on your machine — which it is unless you have arranged
  otherwise — this mode still isolates the credential and silently does none
  of the rest. herdr-draft cannot detect the difference from outside the pane,
  so it will not guess: you opt in.

  `"wrapper"` also needs a `config_dir`, and only a picker supplies one. A
  hand-pinned profile under `launch = "wrapper"` falls back to the argv
  mechanism — `clauth start`, since wrapper mode resets any `launcher` set
  beside it — because a bare `claude` with nothing isolating it would launch
  under whatever credential is machine-global, the opposite of the pin you
  asked for. The launch step names which of the two it actually typed, and
  says when the downgrade happened.

  An unrecognised value is ignored rather than refused, and `"start"` is
  used. A `launcher` set alongside `"wrapper"` is ignored too — the two keys
  are one decision and only one mechanism can be in effect. Both are
  reported, on the `account` row's panel and on `create`'s stderr (see above).

  **Why this is not one key.** The two mechanisms cannot share a
  representation. `launcher` conveys the account as a *word in a command line*
  and requires exactly one `{account}` in the template. Wrapper mode conveys
  it as a *credential directory in the environment*, so its argv is the bare
  word `claude` with the account named nowhere — which the `launcher`
  validator rejects, correctly, since on the argv path a template with no
  `{account}` would spend the wrong budget. Nor can the environment half move
  into the template: herdr *types* this argv into a shell and every element is
  quoted, and a quoted `NAME=value` is not an assignment to any POSIX shell —
  it is a command name. Hence two keys, ordered, with exactly one in effect.

The `account` row only renders when clauth is configured **and** at least
two profiles exist (a static, startup-time check). The `auto` row appears
inside it only when `picker` is set and its probe succeeded.

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

  **Write each argument literally, with no shell quoting of your own.**
  Values go through as they are written, so a model id containing glob
  characters is spelled plainly:

  ```toml
  [agents.extra_args]
  claude = ["--model", "claude-opus-5[1m]", "--effort", "xhigh"]
  ```

  One of the two launch paths does type its command into a pane's shell
  (spec §9 Path B, a pinned clauth account), and herdr-draft quotes for
  that shell itself. Adding your own quotes breaks the other path, which
  hands the same values to the agent as an argv vector with no shell
  involved — the agent then receives a value with the quote marks still
  attached. If you are carrying a `["--model", "'…'"]` workaround from
  before this was fixed, remove the inner quotes (#72).

  For the model, effort and permission mode, prefer `[agents.options]`
  below: those can be changed per session, in the popup or with a `create`
  flag, and extra args cannot. The `options` panel shows what extra args
  append, read-only.

### `[agents.options.<kind>]`

The default session options for one agent kind: what the `options` row
opens on, and what `create` uses for a flag you leave off. Every key is
optional. An unset one sends no flag of its own, so whatever
`[agents.extra_args]` passes decides, and failing that the agent's own
settings. Only `claude` declares options today.

```toml
[agents.options.claude]
model = "claude-opus-5[1m]"   # an alias (fable, opus, sonnet, haiku) or a full id
effort = "xhigh"              # low | medium | high | xhigh | max
permission_mode = "plan"      # manual | plan | acceptEdits | auto
```

- `inherit` is the same as leaving a key out.
- `bypassPermissions` and `dontAsk` are not accepted. The first opens with
  a confirmation dialog that the session's prompt would be typed into, and
  the second never asks. Pass either through `[agents.extra_args]` if you
  mean it.
- A value the kind does not offer, a key it does not declare, a kind that
  declares nothing, and a value that is not a string are each reported —
  on the `options` panel and on `create`'s stderr — and skipped. None of
  them stops the popup opening.
- An option you set, here or in the popup, **replaces** the same flag in
  `[agents.extra_args]` rather than following it, in either spelling
  (`--model opus` or `--model=opus`). An option left on `inherit` leaves
  extra args alone, and the row shows the value they pass, dimmed.
- It is never remembered from a submit: a session you started in `plan`
  mode does not make the next one plan. Change the default here.

### `[timeouts]`

- `detection_ms` (default: `30000`) — how long herdr-draft polls
  `herdr agent get` after launch before giving up on detecting the agent.
- `prompt_wait_ms` (default: `120000`) — timeout passed to
  `herdr agent prompt --wait` for step 3 of the submit pipeline.
- `trust_wait_ms` (default: `300000`, five minutes) — how long the popup
  waits for **you** to answer a blocking dialog, in practice Claude Code's
  first-run trust prompt in a fresh worktree. The step shows `…`, the
  footer says `answer the dialog in the pane`, and the pipeline carries on
  by itself the moment you do; if the budget runs out, or if the agent
  stops running because you declined, it falls back to the failure screen
  with your prompt saved.

  It covers the dialog wherever it stops the run: at the launch step, when
  herdr refuses to call the agent ready, and at the prompt step, when
  herdr-draft's own guard sees the dialog on a pane herdr already called
  ready.

  A separate number from `detection_ms` on purpose: that one is tuned for
  a machine painting a status transition in tens of seconds, and raising
  it to a person's timescale would make every ordinary detection failure
  take five minutes to report. Set it to `0` to switch the wait off and
  fail immediately instead.

  Only the popup waits for **you**. Headless `create` has nobody at the
  keyboard, so it keeps failing fast with the reason, and there is no flag
  for it. This budget covers that wait alone: the short pause before a
  stalled prompt is sent again waits for a terminal to finish painting,
  not for a person, so it happens on both paths and setting this to `0`
  does not switch it off.

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

### `[reaper]`

- `mark_ready` (default: `false`) — start the prompt panel's toggle on
  `reap`, and make `create` behave as if `--reap` were passed. A reaped
  session's prompt ends with one fixed sentence asking the agent to run
  `herdr pane report-metadata "$HERDR_PANE_ID" --source pane-reaper --token pane_reaper=ready`
  as the last command of its final turn, once its work is saved.

  That token is read by **pane-reaper**, a separate herdr plugin that closes
  a pane its agent has marked ready once it goes idle. Without pane-reaper
  running, the sentence costs one harmless command and nothing closes. The
  sentence is never added to an empty prompt or to one starting with `/`,
  because a slash command would receive it as arguments.

  Closing the pane is all pane-reaper does: a worktree session's checkout
  and the branch it created survive that close and are yours to remove.

  Leave it off if you start orchestrator or `/loop` sessions from the
  popup: those must never close themselves, and a default is easy to forget
  about. `⌃X` in the prompt, or `--reap` / `--no-reap`, decides one session.

  It is `config.toml`-only. A repository cannot set it, and neither memory
  file remembers it: one reaped session would otherwise make every later
  one reap.

  pane-reaper lives in the public
  [quantivly/dotfiles](https://github.com/quantivly/dotfiles/tree/main/config/herdr/plugins/local/pane-reaper)
  repository; install it with `herdr plugin link <clone>/config/herdr/plugins/local/pane-reaper`.

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
resolved through the same tiers. **Highest first:**

| Tier | What it is |
|---|---|
| `herdr workspace list` | the workspace already holding this project — placement only, see below |
| `projects.json` | your last choice in *this* project |
| `.herdr-draft.toml` | the repository's committed default |
| `last-used.json` | your last choice anywhere |
| `config.toml` | your own configuration |
| built-in | what herdr-draft ships with |

A team's committed default beats whatever you last did in some *other*
repository, and loses to what you last did in this one. `⌃R ⌃R` clears back
to the repository default.

One tier is not a file. When the project's checkout already has a workspace
open, `new space` from the built-in default, `config.toml`,
`last-used.json` and `projects.json` yields to `tab in <that space>`, so the repository's own space collects its
sessions as tabs instead of a new top-level space appearing per task; a
remembered `tab in` falls back to `new space` once the space is gone. A
`here` placement stands (it is a choice about this pane), a
`.herdr-draft.toml` that says `new-space` stands (it is a team's statement),
and `--placement` or the chip row always wins. `create --json` attributes the
value to `herdr workspace list`.

None of that applies to a worktree session. It runs in the worktree's own
space, which herdr's sidebar already nests under the repository's, so it
gets the grouping a `tab in` would have given it without leaving an empty
space behind. The placement row goes inert, keeping its value for when the
worktree is turned off, and `create --json` attributes the placement to
`worktree`.

Not every tier can supply every value. `.herdr-draft.toml` never chooses
your agent (that is a machine decision, not a repository one), and
`projects.json` and `last-used.json` only remember things a submit
actually settled — the worktree toggle, the placement, the agent kind, and
the base ref for the project you were in. A selected Linear issue overrides
title, branch and prompt on top of all of it, unless you have already typed
over them. The prompt's `keep · reap` toggle and the `options` row come
from `config.toml` or the built-in alone.

Per-project memory re-applies when you change the project row, for every
field you have not touched yourself. It is keyed by the **git repository
root**, so a linked worktree and its origin share one memory rather than
accumulating one entry each.

A remembered or configured **base** is checked against the repository
before it is used, the same way by the form and by `create`:

- One that names a commit is kept. The worktree panel's base list holds
  only the 50 most recently committed branches, so a base it does not name
  — an older branch, a tag, `HEAD~1` — is offered right after the `HEAD`
  row.
- `HEAD` and `@` are the `HEAD` row, and so is any other spelling that
  names HEAD's own commit (`HEAD^0`, `@{0}`). `HEAD~1` is another commit,
  and is kept.
- One that names no commit falls back to `HEAD`, and the worktree panel
  says which base was dropped and where it came from: `ignoring base
  "old-feature" from projects.json: no such commit here; using HEAD`.
  `create` prints the same on stderr. A branch deleted since is the usual
  case. A branch you have only on a remote, remembered or configured by its
  bare name, is another: git resolves it only as `origin/<name>`, which is
  how the base list offers it.

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
- `default_worktree`, `default_placement`, `default_base` — as above. A
  `default_base` has to name a commit in each clone that uses it, or it
  falls back to `HEAD` there with a note (see
  [Where defaults come from](#where-defaults-come-from)). The `"develop"`
  above works in a clone with a local `develop`. For a team branch a fresh
  clone has only as `origin/develop`, write `"origin/develop"`.
- `linear_branch_name` (default: `true`) — whether a selected Linear
  issue's own `branchName` owns the branch. Set it to `false` in a
  repository with its own branch naming: the branch is then derived from
  the title and `branch_prefix` exactly as in manual mode, while the issue
  still seeds the title and the prompt.

**Everything else in the file is ignored and reported**, including every
other key `config.toml` accepts — `[agents.extra_args]` and
`[agents.options]` (each becomes part of a launched agent's command line), `[agents] favorites`/`default` (a
repository doesn't choose which agent runs on your machine), `[linear]
prompt_template` (it would become the agent's first instruction),
`[reaper]` (it would add an instruction to your agent's prompt),
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
pin a profile for the launched Claude session (by default
`clauth start <profile> --`, routed through `herdr pane run`; see
[`[clauth]`](#clauth) to change the launcher). Pinning is optional — leaving the row on
`active` lets clauth use whatever profile is currently live, with no wrapper
involved. The row carries each profile's own state: `<name> · <plan> · 5h N%
· 7d N%`, with a rate-limited profile's percentage in the warning color.
`ok` is the state that needs no words, so the auth status appears only when
it is not `ok`, and then as one of three things. A profile clauth reports
`expired` — an access token between refreshes, which the launch heals by
itself — reads `expired` in the warning color and is launchable; so does
`expiring`, which is what clauth wrote before 0.15.0 for the same state. One
it reports `broken` reads `sign in again` in red and is refused at submit,
because `clauth login` really is what clears that one. Any value a later
clauth adds reads in the warning color, in clauth's own spelling, and is
launchable — the value set is not fixed, and nothing here will call an
unfamiliar word a failure.

**Tested with clauth 0.14.1+.** This is the empirically-confirmed floor
from this plugin's own live validation (`clauth status --json` schema `1`
parsing, schema `2` from clauth 0.15.2, and `clauth start <profile> --`
launching correctly under `herdr pane run`) — no older clauth 0.x release
was available to test, so treat it as "known to work at 0.14.1," not a
verified absolute minimum.

**Known gap, accepted for v1**: on herdr session restore, herdr resumes
`claude --resume <id>` without the clauth wrapper, dropping the account
binding. Upstream discussion #3228 proposes per-agent resume templates
(`clauth info <id>` already prints the exact resume command). Documented
in README; nothing herdr-draft can fix locally.

## Account picker protocol

herdr-draft can delegate *"which Claude account should this session use?"* to
an external program. It does not ship one, and it is not written against any
particular one: what follows is the whole contract, and **any executable
satisfying it is a picker.**

This is deliberate. The picker this was designed against is not a standalone
tool — it sources a shell framework and a per-machine tenant table — so
depending on it by name would have made a private toolchain a hidden
prerequisite of a public plugin. A documented interface costs one section and
works for everybody.

Like clauth itself, it is **entirely optional**: without `[clauth] picker`,
herdr-draft works exactly as it did before this existed.

### Invocation

```
<picker> --dir <project path> --json [--strict] [--dry-run]
```

- `--dir` is the project directory the session is being created in.
- `--json` is always passed.
- `--strict` asks for headless semantics — no interactive fallback, no waiting
  for a person. `herdr-draft create` passes it; the popup does not.
- `--dry-run` asks the picker **not** to write whatever ledger it keeps and
  **not** to build an account directory, so `config_dir` comes back `null`.
  The popup's live preview passes it, on every project change; the one
  commit-time call, at submit, does not.

### Answer

One JSON object on stdout:

```json
{
  "tenant": "alpha", "tenant_state": "default",
  "profile": "alpha-1", "tier": "Max",
  "config_dir": "/home/you/.local/state/account-dirs/alpha-1",
  "score": 8066, "class": "eligible", "reason": null, "overflow": false,
  "usage":     { "five_hour": 3, "weekly": 11, "cache_age_s": 18 },
  "resets_at": { "five_hour": "2026-09-10T22:49:59Z", "weekly": "2026-09-13T01:59:59Z" },
  "machine":   { "load1": 0.9, "ncpu": 8, "swap_used_pct": 2 },
  "warnings": [], "skipped": [ { "profile": "alpha-0", "why": "rate limited" } ]
}
```

Every field may be `null` when the picker could not determine it, and a picker
may emit keys not listed here — they are ignored. `reason` is the picker's own
one-line explanation, and it is what herdr-draft shows you when the picker says
no, in preference to anything herdr-draft could compose itself.

Two optional keys are shown rather than acted on. `warnings` appear on the
`account` panel in the warning colour, one line each, and `herdr-draft
create` prints them to stderr. `machine` appears on the panel as one dim
line — `machine  load 4.4 on 8 cpus · swap 32%`. A load or swap figure the
picker sent as `null` is written `unmeasured` rather than as zero; the cpu
count is shown when it was sent and left off when it was not. Each warning is
flattened to one line. Neither ever refuses a
session: refusing is the picker's job, through its exit codes.

Six of these keys are **required** — `profile`, `config_dir`, `reason`, and
all three of `usage.five_hour`, `usage.weekly`, `usage.cache_age_s`. They must
be *present*; `null` is a fine value for any of them. That is what the startup
probe checks, and it is deliberately a check on the answer's shape rather than
on the answer.

### Exit codes

| code | meaning |
|---|---|
| `0` | picked — `profile` names the account |
| `2` | refused: the pool is exhausted |
| `3` | refused: the machine is under too much load |
| `4` | refused: no usable account table |
| `5` | refused: no profiles registered |
| `64` | usage error — the picker did not accept herdr-draft's invocation |

Any other exit code is treated as a malfunction, not a refusal — on *every*
call, the startup probe and the commit-time pick alike — and the picker is
reported as unusable rather than obeyed. Exit `0` with a `null` profile is
treated the same way: `0` means picked, so an answer that picked nothing has
broken the contract, and herdr-draft will not quietly launch unpinned instead.

`64` is the odd one out among the documented codes, and worth reading as what
it is. Every other code is the picker's judgement about *your* request; `64`
says the picker and herdr-draft disagree about the **invocation** — a wiring
fault, shown in the row where a decision normally goes. If you see it, compare
your picker's flags against the invocation above rather than looking for a
reason it said no.

**Every call is bounded.** A picker gets 30 seconds to answer, after which the
invocation is abandoned and reported as a malfunction — never as a refusal,
since nothing refused anything. The budget is deliberately generous (a cold
usage cache may be several network round-trips) because reporting a working
picker as broken is the worse error; it exists so that a picker which hangs
cannot stop the dialog from opening.

### What herdr-draft does with it

- **At startup**, once: `--json --dry-run` against the current project. The
  answer's *shape* is checked — the six required keys, one of the six
  documented exit codes — never the answer. A machine whose pool is exhausted
  still has a working picker, and it would be perverse to disable the feature
  on the day it most needs to explain itself. If the probe fails, the `account`
  row says so and the `auto` selection does not appear.
- **On every project change**, debounced: `--json --dry-run`, to fill the
  `auto` row with what the picker *would* choose.
- **Once per submit**, after every blocking check and before anything is
  created: `--json` (plus `--strict` for `create`). This is the call whose
  answer becomes the pin. A refusal here refuses the submit and moves focus to
  the manual account rows — it never falls through to an unpinned launch.

`herdr-draft create` skips the startup probe: the very next thing it does is
the real call, and a failure there is reported in full to whoever is reading
stderr. `create --account auto` with no `[clauth] picker` set is a usage
error (exit 2), not a quiet fallback.

### Writing one

A picker can be a shell script. The minimum viable one:

```sh
#!/bin/sh
# Always picks the same account. The one flag it must not ignore is --dry-run:
# a dry run builds no account directory, so config_dir comes back null. The
# startup probe and every preview pass it, so a picker that answers a real
# config_dir there is claiming, several times a session, to have built
# something it did not.
config_dir="\"$HOME/.claude-my-account\""
for arg in "$@"; do
	[ "$arg" = --dry-run ] && config_dir=null
done
cat <<JSON
{"profile":"my-account","tier":"Max","config_dir":$config_dir,
 "reason":null,"usage":{"five_hour":null,"weekly":null,"cache_age_s":null},
 "resets_at":{"five_hour":null,"weekly":null},"warnings":[],"skipped":[]}
JSON
```

This exact block is extracted from this file and run by
`go test ./internal/picker/`, so the document cannot drift away from the
contract it is documenting.

Point `[clauth] picker` at it, reopen the dialog, and the `account` row grows
an `auto` selection reading `auto → my-account`.

## Known limitations

These are real, live-verified rough edges, not deferred features — read
them before filing a bug against something documented here:

- **A brand-new worktree's first launch pauses on Claude Code's trust
  prompt.** Claude Code asks "Is this a project you created or one you
  trust?" the first time it runs in any directory, and every worktree
  herdr-draft creates is a directory it has never seen. herdr 0.9.0
  recognizes that screen and reports the agent as `blocked`, so `herdr agent
  start` refuses to call the launch a success — even though the agent is
  running fine and is one keystroke from ready.

  In the popup this is a pause, not a failure. A step goes to `…` and the
  footer reads *answer the dialog in the pane*; herdr has already moved you
  to that pane, so answer it there and the pipeline sends your prompt and
  closes by itself. You press nothing in the popup. It happens once per
  directory. How long it will wait for you is
  [`trust_wait_ms`](#timeouts).

  Which step pauses depends on whether herdr recognised the dialog before
  or after it called the agent ready — the launch step if before, the
  prompt step if after. Both are the same pause.

  If the wait runs out, or you answer "No, exit", you get the failure
  screen instead: press `k` to keep the session, and any prompt you had
  composed is saved to a file named on that screen so you can paste it in.

  **Headless `create` still fails immediately** on the same dialog, with
  the reason and exit code 1 — a script has nobody at the keyboard.

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
- **`[clauth] launch = "wrapper"` cannot be verified from outside the pane.**
  herdr-draft types `CLAUDE_CONFIG_DIR=<dir> claude` and the pane's own shell
  resolves `claude`. Whether that reaches a wrapper function or the plain
  binary is a fact about your shell that no herdr API reports, so herdr-draft
  does not claim either way — the launch step says which command line it
  typed, and what happened to it is between that line and your `.zshrc`. This
  is why `"start"`, not `"wrapper"`, is the default.

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
started, but the prompt was never typed: the agent was showing a dialog, or
its screen had not painted yet. The failure screen says
`prompt not sent — saved for manual paste:` with the full path on the line
under it — `unsent-prompt.txt` in the plugin state directory — so you can
paste it into the agent by hand once the pane is clear. The session itself
is fine: press `k` (keep it).

**"delivery unconfirmed" after a submit.** The prompt *was* typed into the
pane, or may have been, but nothing confirmed it landed: the wait for the
agent timed out, a send stalled twice, the pane afterwards showed a dialog
with none of the prompt on it or stopped answering, or herdr failed the
send partway (`agent_prompt_failed`) without saying how much of it it had
typed. Read the pane before anything else. The agent may be working on the
prompt already, or its input box may hold part of it, so pasting the saved
copy without looking can send it twice. `remove` is unavailable here for the
same reason; press `k`, and remove the session yourself once the pane shows
it is safe to.

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
that were explicitly declined.

[`CHANGELOG.md`](CHANGELOG.md) records what a release contains, and
CONTRIBUTING's [Releases](CONTRIBUTING.md#releases) section states the four
places a version lives — the manifest, `herdrc.Version`, the changelog
heading and the git tag — and the order they move in. A changelog heading
that reads `unreleased` means the version is settled and the release is
being held.

herdr does not sandbox plugin code, so
[`SECURITY.md`](SECURITY.md) states this plugin's actual surface (the
command it runs for you, the credential it handles, and the trust boundary
around a repository-committed `.herdr-draft.toml`) and how to report
something privately.

## License & provenance

herdr-draft is MIT licensed (see `LICENSE`). It ports and adapts UI code
from [Atrium](https://github.com/ZviBaratz/atrium) (AGPL-3.0) under a
license grant from Atrium's own author, and translates visual conventions
and default palette values from herdr (Apache-2.0, whose text ships in
`third_party/licenses/Apache-2.0.txt`). Full attribution and
the audited file-by-file provenance are in `NOTICE` and the v1 design
spec's §14.
