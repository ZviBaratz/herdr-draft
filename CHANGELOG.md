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

## Unreleased

### Changed

- **The popup draws before its slow reads answer** (#293). `Bootstrap` used
  to resolve the Linear API key, read clauth and probe a configured account
  picker before the first frame, so the pane stayed blank for up to sixty
  seconds while a credential helper waited for you to approve a read. Those
  three now run once the form is on screen, and the rows say what they are
  waiting for: `waiting for api_key_cmd…`, `fetching assigned issues…` and
  `reading clauth profiles…` on the panel, or a dim `loading…` on a row
  with nothing to pick yet. The form is usable throughout. `⌃S` while
  clauth is still being read waits five seconds and then creates anyway,
  under what `config.toml` names; `⌃S` resting on `auto` while the picker
  is still being checked waits the same five seconds and then refuses,
  rather than launch under an account nobody chose.
- **A failed Linear key keeps a cached issue list pickable** (#292). The
  list on disk used to be discarded whenever the key could not be
  resolved — a transient `op read` timeout cost you the whole `issue` row
  for the session. It now stays selectable with the reason on the panel,
  which is what a failed *refresh* already did.
- **An `api_key_cmd` that exits 0 and prints nothing** is reported rather
  than treated as "Linear is not configured", when nothing else supplies a
  key. `create --issue` said "Linear is not configured (set [linear]
  api_key_cmd in config.toml)" to users whose `config.toml` named the
  command it had just run; it now says `api_key_cmd printed nothing`, on
  the same exit code.
- **The `terminal` palette draws in your terminal's own colours** (#304).
  Nine of its twelve colours were xterm's default RGB values, so a terminal
  that redefines red still got xterm's red. They are now ANSI palette
  entries, as herdr's own `terminal` theme uses, and the focused row is
  filled with your terminal's bright black instead of a fixed grey. The
  contrast floors never applied to this palette, and still don't: its
  colours are whatever your terminal says they are. herdr's
  `name = "terminal"` on its own still draws in your dark theme; add
  `dark_name = "terminal"` beside it to reach this palette.

## 0.1.0 — 2026-09-22

The first release. herdr-draft is a [herdr](https://github.com/herdrdev/herdr)
plugin: a one-screen dialog, opened in a herdr popup, that creates a fully
configured agent session in one submit, and a headless `create` command that
makes the same session from a script or an agent.

**Requirements:** herdr 0.9.0 or later; Go 1.25 or later on the machine you
install on, since herdr builds the plugin from source; Linux or macOS.
Optional: a Linear API key, [clauth](https://github.com/uwuclxdy/clauth)
0.14.1 or later, and the pane-reaper plugin.

**Install:** `herdr plugin install ZviBaratz/herdr-draft --ref v0.1.0`, then
bind `zvibaratz.draft.open` to a key as the [README](README.md#bind-a-key)
shows.

### The dialog

- Nine one-line rows that never move: `issue`, `title`, `prompt`,
  `project`, `worktree`, `placement`, `agent`, `options` and `account`, over
  one panel that belongs to the focused row. `issue` appears with Linear and
  `account` with two or more clauth profiles, so most people see seven.
- **Worktrees:** a branch derived from the title; a base from `HEAD` or the
  50 most recent branches, remote-only ones as `origin/<name>`; an existing
  or invalid branch refused before anything is created; no upstream tracking
  of the remote branch a session was cut from.
- **Placement:** a new space, a tab or split beside the current pane, or a
  tab in the project's open space, which is the default when there is one. A
  worktree session always runs in the worktree's own space.
- **Options:** the agent's model, effort and permission mode, per session.
  The panel shows what `[agents.extra_args]` passes and what each choice
  sends. Only `claude` declares options.
- **Linear:** a selected issue seeds the title, branch and prompt. Read-only.
- **Accounts:** pin a clauth profile, with each profile's plan and five-hour
  and seven-day usage, or let an account picker choose.
- **Prompt:** `⌃J` for new lines, and a `keep · reap` toggle that asks the
  agent to mark its pane ready for pane-reaper once its work is saved.
- Every check a create waits for is bounded, and one that cannot answer
  refuses the create rather than guessing.

### Headless `create`

- The same session, resolved through the same defaults as the form, for
  scripts and agents. Flags for every row, including `--model`, `--effort`
  and `--permission-mode`.
- `--json` output with ids, provenance for every value, the agent's launch
  arguments, and the prompt's fate: `sent`, `unsent` or `unconfirmed`.
- `--dry-run` runs every check and creates nothing. Exit codes 0 to 5
  separate bad usage, an unreachable herdr, a first step that made nothing,
  and a check that timed out.
- `--on-failure keep|clean`. A clean removes only what the run made,
  including a worktree branch it created that holds no work.
- Sessions open in the background: `create` never moves your view to the
  session it makes. The popup does, since there you asked for the session.

### Defaults and configuration

- Defaults resolve through one chain for the form and `create`: this
  project's memory, the repository's `.herdr-draft.toml`, your last choices,
  `config.toml`, then built-in defaults.
- `.herdr-draft.toml` lets a team share five creation defaults. Its
  allow-list is a trust boundary: a cloned repository can never name a
  command, a path outside itself or a credential, and anything else in the
  file is reported and ignored.
- `config.toml` covers Linear, clauth and the account launcher, agent
  favorites and arguments, per-agent session options, timeouts, worktree
  trust, pane-reaper, and colour overrides.
- Colours follow your herdr theme, with minimum contrast enforced for every
  colour that marks a region or carries a word, across all builtin themes.

### Agents and accounts

- `herdr-draft skill` prints a `/spawn` skill that teaches Claude Code
  agents to hand work off through `create`. `skill --check` tells you when an
  installed copy is stale.
- clauth integration, with a configurable launcher and an opt-in wrapper
  mode for a shell-function `claude`.
- A documented account picker protocol: any executable that follows it can
  choose the account per session. None is bundled or discovered.

### Reliability

- The first prompt is never typed into a dialog. Claude Code's trust prompt
  in a new worktree is a pause in the popup, not a failure.
- A prompt whose delivery cannot be confirmed is reported as unconfirmed,
  with a copy saved, and is never resent blind.
- A broken Linear key or an unreadable clauth degrades one row; only an
  unreachable herdr, a missing plugin context or a broken `config.toml`
  stops the popup opening.

### Known limitations

Each is explained in [Troubleshooting](docs/troubleshooting.md#known-limitations).

- `create` fails at once on Claude Code's trust prompt, since nobody is
  there to answer it.
- The popup stays blank while it resolves the Linear key, reads clauth and
  probes an account picker, each within its own time limit.
- A session herdr restores after a restart loses its pinned account.
- The popup has no menu entry, only a key binding or the command line.
- After `herdr update`, `create` exits 3 from panes the old server opens,
  until the server restarts.
