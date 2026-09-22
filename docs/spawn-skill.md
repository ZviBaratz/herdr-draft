# The `/spawn` skill

herdr-draft ships a Claude Code skill that teaches an agent to hand work
off by creating a real herdr session with [`herdr-draft create`](create.md),
instead of writing a handoff document and stopping. Installed, it also makes `/spawn` a command you can type in any
Claude Code session on the machine.

`herdr-draft skill` prints the skill as one `SKILL.md` on stdout. It writes
nothing itself: you redirect it where Claude Code looks for skills.

## Install

```bash
root=$(herdr plugin list --json --plugin zvibaratz.draft | jq -r '.result.plugins[0].plugin_root // empty')
mkdir -p ~/.claude/skills/spawn
"$root/bin/herdr-draft" skill > ~/.claude/skills/spawn/SKILL.md
```

The first line finds the binary. `herdr-draft` is not on your `PATH`: herdr
builds it into `bin/` inside the plugin's install directory, and
`herdr plugin list --json` reports that directory as `plugin_root`, for an
install from GitHub and a linked checkout alike. It works without a running
herdr server. Do not search `~/.config/herdr/plugins` for the binary
instead; there can be more than one copy, and the layout is herdr's to
change. The snippet needs [`jq`](https://jqlang.org/).

The directory name has to be `spawn`, the skill's own name. Claude Code
takes a skill's identity from the `name` in its header and does not load one
whose directory disagrees, and it says nothing when that happens.

## What it covers

The skill is written for the agent, not for you. It covers:

- the `HERDR_PLUGIN_*` exports `create` needs, and what resolving without
  them costs (see [Export the plugin environment](create.md#export-the-plugin-environment));
- whether the new session gets a worktree, and what does not travel into one;
- which placement to pick;
- choosing the model, effort and permission mode for the task, and never a
  permission mode looser than its own;
- writing the prompt and piping it in;
- dry-running the command, then asking you to confirm it once, showing each
  choice with its reason and what it resolves to;
- reading `prompt_status` and the new pane afterwards, rather than trusting
  the exit code.

## Keep it current

**The installed file is specific to your machine**, and is not meant to be
committed anywhere. It names the absolute path of the binary that printed
it, so no agent has to look for one. An install from GitHub keeps the same
path when you upgrade it. Switching between an install and a linked checkout
moves it.

**Regenerate it after every upgrade**, with the same three lines: what it
says about `create` changes with the version.

The file ends with the version it was generated from and a digest of the
skill's text. `herdr-draft version` prints both, and the digest changes
whenever the text does, even within one version. To check an installed copy:

```bash
"$root/bin/herdr-draft" skill --check ~/.claude/skills/spawn/SKILL.md
```

| Exit | Meaning |
|---|---|
| 0 | the file is exactly what this binary would print |
| 1 | it is stale: the message says what moved (the text, the version, the binary's path, or an edit) and prints the command that regenerates it |
| 2 | no verdict: bad arguments, a file it could not read, or a binary that could not work out its own path |

The skill runs that check on itself before it spawns anything, and tells you
when your copy is stale. It does not regenerate the file.

If `herdr-draft skill` cannot work out its own path, it still prints a usable
document that names the plain command `herdr-draft`, and says so on
**stderr**, so a warning never ends up inside the redirected file.
