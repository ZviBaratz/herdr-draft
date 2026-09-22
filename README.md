# herdr-draft

[![check](https://github.com/ZviBaratz/herdr-draft/actions/workflows/check.yml/badge.svg)](https://github.com/ZviBaratz/herdr-draft/actions/workflows/check.yml)
[![Latest release](https://img.shields.io/github/v/release/ZviBaratz/herdr-draft)](https://github.com/ZviBaratz/herdr-draft/releases)
[![Go version](https://img.shields.io/github/go-mod/go-version/ZviBaratz/herdr-draft)](go.mod)
[![License: MIT](https://img.shields.io/github/license/ZviBaratz/herdr-draft)](LICENSE)

A new-session dialog for [herdr](https://github.com/herdrdev/herdr), the
terminal workspace manager for coding agents.

Starting an agent on a piece of work takes a handful of steps: cut a git
worktree on a new branch, decide where the session goes in herdr, start the
agent with the right model, effort and permission mode, choose which Claude
account it runs under, and type the first prompt. herdr-draft puts all of
that on one screen, fills every field with a sensible default, and does it
in one submit. The same binary creates sessions without the screen too, for
scripts and for agents handing work to other agents.

<!-- Hero image: a real capture of the popup goes here, as docs/images/popup.png. -->

![The herdr-draft form as it opens: nine one-line rows (issue, title, prompt,
project, worktree, placement, agent, options, account) with the title row
focused, and the sessions already open listed in the panel
below](docs/images/opening.svg)

## What you get

- **One screen, one submit.** Title, prompt, project, git worktree and
  branch, where the session opens, agent, model, effort, permission mode and
  Claude account, each on one line with a default already filled in.
- **Defaults that remember.** The form opens on what you last chose in this
  repository, then on a team's committed `.herdr-draft.toml`, then on what
  you last chose anywhere, then on your own configuration.
- **Worktrees.** A branch derived from the title, a base picked from recent
  branches (remote ones included), and a duplicate branch refused before
  anything is created.
- **Linear issues** (optional) seed the title, branch and prompt.
- **Claude accounts** (optional, through
  [clauth](https://github.com/uwuclxdy/clauth)): pin a session to an account,
  see each account's usage, or let a picker program choose.
- **Headless `create`**, with the same defaults, JSON output and documented
  exit codes, and a `/spawn` skill that teaches Claude Code agents to use it.
- **Honest about the first prompt.** It never types into a dialog, and when
  it cannot confirm the agent took the prompt, it says so and keeps a copy.

## Requirements

- **herdr 0.9.0 or later.** herdr refuses to install the plugin on an older
  version.
- **Go 1.25 or later** on the machine you install on. herdr builds the plugin
  from source when you install it.
- **Linux or macOS.** Windows is not supported.

Optional:

- A **Linear API key**, for the `issue` row.
- **[clauth](https://github.com/uwuclxdy/clauth) 0.14.1 or later** with two
  or more profiles, for the `account` row.
- **pane-reaper**, a separate herdr plugin, to close a session's pane when
  its agent finishes (see [`[reaper]`](docs/configuration.md#reaper)).
- **[jq](https://jqlang.org/)**, for the one-line lookups in these docs.

## Install

```bash
herdr plugin install ZviBaratz/herdr-draft --ref v0.1.0
```

herdr clones the repository, shows you what it is about to install and asks
you to confirm. It then builds the binary with `go build` on your machine and
registers the plugin as `zvibaratz.draft`. If the build fails, nothing is
registered. Add `-y` to skip the confirmation, which a script without a
terminal must do.

### Bind a key

Plugins do not appear in herdr's menus, so give the dialog a key. In herdr's
own `config.toml`:

```toml
[[keys.command]]
key = "prefix+d"
type = "plugin_action"
command = "zvibaratz.draft.open"
description = "new session"
```

`zvibaratz.draft.open` is the plugin's `open` action, qualified with its
plugin id so that no other plugin's `open` can answer to it. Check the file
with `herdr config check` and load it with `herdr server reload-config`.

Pick a key herdr does not already use. `prefix+d` is free in herdr's
defaults, but `prefix+n` is not: it is the next tab, and `config check`
accepts a binding that collides with it without a word.

You can also open the dialog from any shell inside herdr:

```bash
herdr plugin pane open --plugin zvibaratz.draft --entrypoint open
```

## The fast path

Open it, type a title, press `↵`.

Focus opens on the title, and every other row is already resolved. From a
named title, `↵` creates the session: a worktree on a branch derived from
what you typed, its own space in herdr's sidebar, and your agent running in
it. Nothing below the title needs your attention unless you want to change
it.

![The form after typing "fix flaky checkout test": the worktree row now
names the branch you/fix-flaky-checkout-test, and the footer offers ↵ to
create](docs/images/titled.svg)

## The rows at a glance

Most people see **seven rows**. `issue` appears only when Linear is set up,
and `account` only when clauth has two or more profiles.

| Row | What it sets |
|---|---|
| `issue` | a Linear issue to seed the title, branch and prompt |
| `title` | the session's name, and the source of its branch name |
| `prompt` | the first message the agent receives |
| `project` | the directory, usually a repository, the session is created in |
| `worktree` | whether the session gets its own git worktree, on which branch, from which base |
| `placement` | where a session without a worktree opens: a new space, a tab, a split, or a tab in the project's open space |
| `agent` | which agent to start |
| `options` | the agent's model, effort and permission mode, for this session only |
| `account` | which Claude account the session runs under |

herdr's sidebar lists **spaces**; a space holds tabs, and a tab holds panes.
A **worktree** is a second checkout of the repository on its own branch, so a
session can work without touching your main checkout. A worktree session
always gets its own space, nested under the repository's.

[The form](docs/the-form.md) goes through every row and panel.

## Keys

| Key | What it does |
|---|---|
| `⇥` / `⇧⇥` | next / previous row |
| `↑` `↓` | move within the focused row's panel |
| `←` `→` | chips: worktree on or off, placement, agent favorites, an option's value |
| `↵` | create, from a named title or the prompt; pin the account or choose the base under the cursor; otherwise next row |
| `⌃S` | create, from anywhere |
| `⌃J` | new line in the prompt (`⇧↵` and `⌥↵` too) |
| `⌃X` | in the prompt: end it with pane-reaper's instruction, or not |
| `⌃R` `⌃R` | clear back to the resolved defaults, including the repository's `.herdr-draft.toml` |
| `esc` / `⌃C` | cancel |

The mouse works too: click a row, click a line in the panel, scroll with the
wheel. [The full key reference](docs/the-form.md#keys) covers the details.

## Configure

Everything is optional. herdr-draft reads `config.toml` from the plugin's
config directory (`herdr plugin config-dir zvibaratz.draft`):

```toml
branch_prefix = "you/"             # branch = prefix + the title, slugified
default_worktree = true            # start the worktree row on

[linear]
api_key_cmd = ["pass", "show", "linear-api-key"]   # adds the issue row

[clauth]
default = "personal"               # the account to pin by default

[agents.options.claude]            # this agent's default session options
model = "opus"
effort = "high"
permission_mode = "plan"

[agents.extra_args]
claude = ["--verbose"]             # passed to every claude session

[worktree]
trust_repository = true            # if git reports "dubious ownership"
```

A repository can commit a `.herdr-draft.toml` with five shared defaults,
such as a team branch prefix and a default base. It can never name a command
or a credential. [Configuration](docs/configuration.md) documents every key
and the order in which defaults apply.

## Headless `create`

`herdr-draft create` makes the same session without the popup, resolving
everything you leave out the way the form does:

```bash
herdr-draft create --title "fix login redirect loop" --worktree
herdr-draft create --title "triage" --no-worktree --placement split-here
git log -1 --format=%B | herdr-draft create --title "revert" --prompt - --json
```

It creates for real, with no confirmation, so try `--dry-run` first.
`herdr-draft` is not on your `PATH`; [Headless `create`](docs/create.md)
shows how to find it, the environment it needs, every flag, the JSON output
and the exit codes.

## Agents and the `/spawn` skill

`herdr-draft skill` prints a Claude Code skill that teaches an agent to hand
work off by creating a real session with `create`, rather than writing a
handoff document and stopping. It dry-runs the command and asks you to
confirm it once. Install it into `~/.claude/skills/spawn/`, and regenerate it
after every upgrade: see [The `/spawn` skill](docs/spawn-skill.md).

## Upgrade and uninstall

herdr has no `plugin update` command. To upgrade, install again with the
tag of a newer [release](https://github.com/ZviBaratz/herdr-draft/releases),
and herdr replaces the existing install:

```bash
herdr plugin install ZviBaratz/herdr-draft --ref vX.Y.Z
```

Then [regenerate the `/spawn` skill](docs/spawn-skill.md#keep-it-current) if
you installed it. The [changelog](CHANGELOG.md) says what each release
changed.

To uninstall:

```bash
herdr plugin uninstall zvibaratz.draft
```

That removes the plugin itself. Your `config.toml`, the plugin's
[state directory](docs/troubleshooting.md#state), the key binding in herdr's
configuration and `~/.claude/skills/spawn/` stay until you delete them.

## Support

- **Something is wrong:** check [Troubleshooting](docs/troubleshooting.md)
  first, then [open a bug report](https://github.com/ZviBaratz/herdr-draft/issues/new/choose).
  Include the full output of `herdr-draft version`, the output of
  `herdr status`, your OS and terminal, what you configured, and the smallest
  steps that show the problem.
- **A question or an idea:** ask in
  [Discussions](https://github.com/ZviBaratz/herdr-draft/discussions).
- **A security problem:** report it privately, as described in
  [SECURITY.md](SECURITY.md). That page also lists exactly what the plugin
  runs, reads and sends, since herdr does not sandbox plugins.

Everyone taking part is expected to follow the
[code of conduct](CODE_OF_CONDUCT.md).

## Documentation

- [The form](docs/the-form.md): every row, panel and key.
- [Configuration](docs/configuration.md): `config.toml`, `.herdr-draft.toml`,
  and where defaults come from.
- [Headless `create`](docs/create.md): flags, JSON output, exit codes.
- [The `/spawn` skill](docs/spawn-skill.md): installing and updating it.
- [Claude accounts](docs/account-picker.md): clauth and the account picker
  protocol.
- [Troubleshooting](docs/troubleshooting.md): problems, known limitations,
  and the state herdr-draft keeps.
- [Contributing](CONTRIBUTING.md): building, testing and releasing.
- [All documentation](docs/README.md), including the design records.

## License and provenance

herdr-draft is [MIT licensed](LICENSE). Its form code is ported and adapted
from [Atrium](https://github.com/ZviBaratz/atrium), the author's earlier
terminal UI (AGPL-3.0), under a license grant from Atrium's author. Its
visual conventions and default colours follow herdr (Apache-2.0, whose text
ships in [`third_party/licenses/Apache-2.0.txt`](third_party/licenses/Apache-2.0.txt)).
[`NOTICE`](NOTICE) has the file-by-file attribution.
