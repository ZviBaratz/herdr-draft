# Troubleshooting

Start with the version, then look for your symptom under
[Problems and fixes](#problems-and-fixes). Some rough edges are known and
by design; they are listed under [Known limitations](#known-limitations).

## Which version am I running?

```bash
herdr-draft version     # --version and -V work too
```

```
herdr-draft 0.1.0
  plugin id  zvibaratz.draft
  skill      42e8d3ecbc4f
  commit     a33304d53459
  built with go1.25.14
```

- The first line is the version herdr shows for the install.
- `plugin id` is the id this binary answers to.
- `skill` is the digest of the [`/spawn` skill](spawn-skill.md) this binary
  prints, which an installed copy of the skill should match.
- `commit` is the commit it was built from, when Go recorded one.
- A `build` line (a `git describe` of the tree) appears only for a binary
  built with `just build` from a clone. herdr's own install does not add it.

`herdr-draft` is not on your `PATH`; see
[Finding the binary](create.md#finding-the-binary). Quote the whole output
in a bug report, along with `herdr status`, which shows both the herdr client
and server versions.

## Problems and fixes

### The popup will not open, or herdr reports `ui_busy`

herdr opens a popup only from its normal view, and answers `ui_busy`
("popup panes can only open from the normal workspace view") while another
herdr dialog or mode is up. Close it and try again.

The popup has no entry in herdr's menus: plugins cannot add menu entries
yet. Open it with the key you bound, or from a shell:

```bash
herdr plugin pane open --plugin zvibaratz.draft --entrypoint open
```

### A plain-text `herdr-draft: ...` error instead of the form

herdr-draft refuses to open, rather than open broken, in three cases: it
cannot reach herdr, the invocation context herdr passes it is missing or
malformed, or `config.toml` does not parse. The message says which, and the
config one names the file and the line. Everything else degrades one row
instead of refusing.

### `$HERDR_PLUGIN_CONTEXT_JSON is not set` when you run the binary yourself

Expected. herdr sets that variable when it launches the plugin, and running
the binary from a shell gives it no space, tab or pane to work from. Open the
popup, or use [`herdr-draft create`](create.md); the message names both.

### `create` exits 3 right after a `herdr update`

```
herdr-draft create: herdr unreachable: herdr workspace list: fork/exec /home/you/.local/bin/herdr (deleted): no such file or directory
```

`herdr update` replaces the herdr binary and leaves the old server running.
Until that server restarts, the panes it opens are given the old binary's
path in `HERDR_BIN_PATH`, which no longer exists, so `create` cannot reach
herdr from them. `herdr status` shows the mismatch: a newer client than
server.

Restart the herdr server when it suits you. Until then, give `create` the
current binary in the same command:

```bash
HERDR_BIN_PATH="$(command -v herdr)" herdr-draft create --title "..."
```

### `create` ignores my configuration

`create` needs the plugin's config and state directories, which herdr does
not put in ordinary panes. Without them it resolves from built-in defaults
and says so on stderr. Export them as shown in
[Export the plugin environment](create.md#export-the-plugin-environment).

### A row shows a value you did not choose

Something above your own `config.toml` supplied it: the repository's
`.herdr-draft.toml`, or what you last chose in this project or anywhere.
Look for `from .herdr-draft.toml` in the row's panel, and see
[Where defaults come from](configuration.md#where-defaults-come-from).
Pressing `⌃R` twice clears the form back to the resolved defaults, including
the repository's file.

### There is no `issue` row, or no `account` row

Both are optional, and each appears only with its integration, checked once
when the popup opens. `issue` needs a Linear API key (see
[`[linear]`](configuration.md#linear)). `account` needs clauth with at least
two profiles (see [Claude accounts](account-picker.md)). Without them the
form has seven rows, which is by design.

### The `issue` row says `unavailable`

A key source is configured but failed: `api_key_cmd` exited with an error,
is not on `PATH` or did not answer within sixty seconds, or `api_key` sits in
a `config.toml` others can read. The reason is on the row and, in full, in
its panel. See [`[linear]`](configuration.md#linear).

### The `account` row says `unavailable`

clauth is installed, but herdr-draft could not read it. Focus the row, with
`⇥` or a click, to ask clauth again without closing the form. See
[The account row](account-picker.md#the-account-row).

### The `project` row says `check timed out`, or the title panel says `couldn't check`

A check on the project did not answer within five seconds. It is almost
always a network drive that has stopped responding. herdr-draft refuses to
create rather than guess; the refusal lifts as soon as the check answers or
you change the value. `create` gives the same checks thirty seconds and
exits 5; see [Exit codes](create.md#exit-codes).

### Worktree creation fails with git's "dubious ownership"

git refuses to work in a repository owned by another user. Set
[`trust_repository = true`](configuration.md#worktree) under `[worktree]` to
trust the repository for each worktree request, rather than adding it to
`safe.directory` for the whole machine.

### "prompt not sent" after a submit

The session was created and the agent started, but the prompt was never
typed: the agent was showing a dialog, or had not finished drawing its
screen. The failure screen reads `prompt not sent — saved for manual paste:`
and names the file it saved the prompt to, `unsent-prompt.txt` in the
[state directory](#state). Press `k` to keep the session, clear whatever the
pane shows, and paste the prompt in.

### "delivery unconfirmed" after a submit

The prompt was typed into the pane, or may have been, but nothing confirmed
that the agent took it: the wait for the agent ran out, a send stalled twice,
the pane afterwards showed a dialog without the prompt or stopped answering,
or herdr failed the send partway through. The agent may already be working on
it, or its input box may hold part of it.

**Read the pane before doing anything else.** Pasting the saved copy without
looking can send the prompt twice. `remove` is unavailable for the same
reason: press `k`, and remove the session yourself once the pane shows it is
safe.

### Installing fails with "already linked from a local path"

herdr will not install over a linked development checkout with the same
plugin id. Run `herdr plugin unlink zvibaratz.draft`, then install again.

## Known limitations

These are known and understood. Please read them before filing a bug.

- **A new worktree's first launch pauses on Claude Code's trust prompt.**
  Claude Code asks whether you trust a folder the first time it runs there,
  and every new worktree is a folder it has never seen. In the popup this is
  a pause, not a failure: the step shows `…`, the footer reads
  `answer the dialog in the pane`, and once you answer it in the pane the
  popup sends your prompt and closes. It happens once per folder, and the
  popup waits up to [`trust_wait_ms`](configuration.md#timeouts). If the wait
  runs out, or you answer "No, exit", you get the failure screen with your
  prompt saved.

  **Headless `create` fails at once on the same dialog**, with the reason and
  exit 1, because a script has nobody at the keyboard to answer it.

- **herdr-draft never types a prompt into a pane it has not checked.** Before
  sending, it reads the pane and compares it with a list of known dialogs; on
  a match, or when the pane cannot be read, it stops and saves the prompt.
  herdr recognises Claude Code's trust prompt, but not every dialog an agent
  can show, so this check stays even where herdr would have called the agent
  ready.

- **The popup can open blank for a while.** Before it draws anything, it
  resolves your Linear key, reads clauth's status when clauth's own status
  file is out of date, and probes a configured account picker. While any of
  those is waiting, the pane is empty: up to sixty seconds for an
  `api_key_cmd` that is waiting for your approval, and up to thirty seconds
  each for clauth and the picker.

- **The popup is reachable only by a key binding or from the command line.**
  herdr does not yet let plugins add entries to its menus.

- **Wrapper launch cannot be verified from outside the pane.** With
  `[clauth] launch = "wrapper"`, herdr-draft types
  `CLAUDE_CONFIG_DIR=<dir> claude`, and your pane's shell decides what
  `claude` is. Whether that reaches a wrapper function or the plain binary is
  a fact about your shell that herdr cannot report, so the progress screen
  says only what was typed. This is why `"start"` is the default.

- **A restored session loses its pinned account.** When herdr restores
  sessions after a restart, it resumes `claude --resume <id>` without clauth.
  See [The account row](account-picker.md#the-account-row).

- **Closing a reaped pane leaves its worktree.** pane-reaper closes the pane
  and nothing else: a worktree session's checkout and branch stay, and are
  yours to remove.

- **The `terminal` theme and `auto_switch` are drawn in your dark theme.**
  One takes its colours from your terminal and the other follows its light
  or dark appearance, and neither can be read from herdr's config file. Use
  [`[palette]`](configuration.md#palette) if the result reads wrong.

## State

herdr-draft keeps a little state in its state directory,
`${XDG_STATE_HOME:-$HOME/.local/state}/herdr/plugins/zvibaratz.draft`. All of
it is safe to delete: you lose your remembered defaults, the cached issue
list and any saved prompt, and nothing else.

| File | What it holds |
|---|---|
| `recents.json` | project directories you have used recently, offered on the `project` row. Written after each successful create |
| `last-used.json` | the agent kind, placement and worktree toggle from your last successful create anywhere |
| `projects.json` | the same, plus the base, for each repository, keyed by its root so a linked worktree shares its repository's entry. Keeps the 50 most recently used |
| `linear-cache.json` | the last Linear issue list, drawn at once while a fresh one loads |
| `unsent-prompt.txt` | the last prompt that could not be delivered, kept for pasting by hand. Replaced by the next one |
