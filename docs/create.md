# Headless `create`

`herdr-draft create` makes the same session the popup makes, without the
popup: for scripts, and for agents that hand work to a new session. It
resolves everything you leave out exactly the way the form does.

> **`create` creates for real, with no confirmation.** herdr exports
> `HERDR_WORKSPACE_ID`, `HERDR_TAB_ID` and `HERDR_PANE_ID` into every pane's
> shell, so running `create` in any pane of your normal herdr session builds
> a real worktree, space and agent there. Use `--dry-run` to see what a
> command would do first.

`create` does not move your view. The space, tab or split it opens appears
in herdr's sidebar, and you stay where you were, so a script or an agent
can make sessions while you keep working. The popup is different: there you
asked for the session yourself, so herdr takes you to it.

## Finding the binary

`herdr-draft` is not on your `PATH`. herdr builds it into `bin/` inside the
plugin's install directory, and reports that directory as `plugin_root`:

```bash
root=$(herdr plugin list --json --plugin zvibaratz.draft | jq -r '.result.plugins[0].plugin_root // empty')
"$root/bin/herdr-draft" create --help
```

The examples below write plain `herdr-draft`. The function under
[Export the plugin environment](#export-the-plugin-environment) finds the
binary this way and sets what it needs, so with it in your shell's startup
file they work as written.

## Examples

```bash
# A worktree session on a branch derived from the title
herdr-draft create --title "fix login redirect loop" --worktree

# No worktree, in a pane beside this one
herdr-draft create --title "triage" --no-worktree --placement split-here

# A prompt from stdin, and this session's options
git log -1 --format=%B | herdr-draft create --title "revert" --prompt - \
  --model opus --effort high --permission-mode plan

# See what a create would make, and make nothing
herdr-draft create --title "design the cache" --dry-run --json
```

## Export the plugin environment

herdr gives a plugin it launches two directories, `HERDR_PLUGIN_CONFIG_DIR`
and `HERDR_PLUGIN_STATE_DIR`, but does not put them in ordinary panes.
Without them, `create` cannot see your `config.toml` or your remembered
choices, resolves from built-in defaults instead, and says so on stderr.

Set them for `herdr-draft` alone rather than exporting them from your shell's
startup file. Other plugins read the same variable names, so a global export
would hand them herdr-draft's directories, or leave herdr-draft reading
theirs. A small function does it per call:

```bash
herdr-draft() {
  local root
  root=$(herdr plugin list --json --plugin zvibaratz.draft | jq -r '.result.plugins[0].plugin_root // empty')
  HERDR_PLUGIN_ID="zvibaratz.draft" \
  HERDR_PLUGIN_CONFIG_DIR="$(herdr plugin config-dir zvibaratz.draft)" \
  HERDR_PLUGIN_STATE_DIR="${XDG_STATE_HOME:-$HOME/.local/state}/herdr/plugins/zvibaratz.draft" \
    "$root/bin/herdr-draft" "$@"
}
```

It works in zsh and bash. These are the same directories the popup uses, so the popup and `create`
share one configuration and one memory.

An agent that runs `create` does not read this page; the
[`/spawn` skill](spawn-skill.md) carries these exports for it.

`HERDR_PLUGIN_ID` says whose the other variables are. A shell started inside
another plugin's pane can inherit that plugin's `HERDR_PLUGIN_*` variables,
so `create` uses them only when the id is `zvibaratz.draft`. With a
different id, or none, it ignores all of them, rather than read another
plugin's configuration or write into its state, and tells you what to
export.

## Flags

`herdr-draft create --help` prints the same list.

| Flag | Meaning |
|---|---|
| `--project DIR` | project directory (default: the working directory) |
| `--title TEXT` | session title; required unless `--issue` supplies one |
| `--prompt TEXT` | initial prompt; `-` reads it from stdin |
| `--branch NAME` | worktree branch (default: derived from the title) |
| `--base REF` | the commit a worktree starts from (default: `HEAD`) |
| `--worktree` / `--no-worktree` | create a git worktree, or not |
| `--reap` / `--no-reap` | end the prompt with pane-reaper's instruction, or not (default: off, or `[reaper] mark_ready`) |
| `--placement WHERE` | `new-space`, `tab-here`, `split-here` or `tab-in`: where a session without a worktree lands |
| `--workspace ID` | the space `tab-in` opens its tab in, instead of the one holding the project (implies `--placement tab-in`) |
| `--agent KIND` | agent kind to start, such as `claude` |
| `--model NAME` | an alias (`fable`, `opus`, `sonnet`, `haiku`) or a full model id such as `claude-opus-5[1m]` |
| `--effort LEVEL` | `low`, `medium`, `high`, `xhigh` or `max` |
| `--permission-mode MODE` | `manual`, `plan`, `acceptEdits` or `auto` |
| `--account NAME` | clauth account to pin (claude only); `auto` asks your [account picker](account-picker.md) |
| `--issue ID` | seed the title, branch and prompt from a Linear issue |
| `--json` | print one JSON object instead of a line |
| `--dry-run` | check everything a create would, report what it would make, and stop |
| `--on-failure WHAT` | `keep` or `clean`: what to do with a session that failed partway (default: `keep`) |

`--model`, `--effort` and `--permission-mode` are the form's `options` row.
They apply only to an agent kind that declares them, which today is
`claude`, and are refused with exit 2 for any other. `inherit` sends no flag
of its own, even when `config.toml` sets a default for that option, though
`[agents.extra_args]` can still pass one:

```bash
herdr-draft create --title "quick fix" --model sonnet --effort inherit
```

## How unset values resolve

Anything you do not pass resolves the way the form resolves it: this
project's memory, then the repository's `.herdr-draft.toml`, then your last
choices anywhere, then `config.toml`, then the built-in default. See
[Where defaults come from](configuration.md#where-defaults-come-from). The
project directory defaults to the working directory.

A successful `create` records what it used, exactly as a submit from the
popup does, so the popup's next open defaults to it too.

`create` refuses what the form refuses, before anything is created (exit 2):
a worktree branch that already exists, a title an open space already
carries, a branch name git cannot use, an empty `--branch`, and a pinned
account whose credential clauth reports as revoked. Keys a repository's
`.herdr-draft.toml` is not allowed to set are listed on stderr.

### Titles and prompts

A title is kept to what the form's `title` row holds: a tab, CR or LF
becomes a space, other control characters and invalid UTF-8 are dropped, and
the result is cut to 32 characters. That applies to a `--title` and to the
title `--issue` takes from Linear. A line on stderr says so whenever it
changes the title.

A prompt keeps its newlines and tabs. Other control characters are removed,
and Windows (CRLF) and lone CR line endings become plain newlines. The same
applies to a prompt seeded from a Linear issue.

### Worktrees and bases

`--base HEAD`, `--base @` and any other spelling of HEAD's own commit mean
no base: the worktree starts from the commit the project is on. With a
worktree, a `--base` that names no commit in the project is refused (exit 2)
before anything is created.

**Inside a linked worktree**, such as another session's, the project is that
checkout. herdr cannot create a worktree from a linked checkout, so a new
worktree is created from the repository's main checkout, with its base
resolved in the linked one: `HEAD` means the commit you are on there. With
no base chosen, `--json` reports that commit as `base`, and it is never
remembered. Without a worktree, the session runs in the linked checkout
itself. The popup behaves the same way. A bare repository has no main
checkout to create from, and herdr's refusal stands.

A session's branch does not track the remote branch it was cut from.
`create --base origin/develop` gives a branch with no upstream until its
first `git push -u`. If removing the upstream fails, the create still
succeeds, and its worktree line on stderr gives the command that finishes
the job (`git branch --unset-upstream <branch>`).

### Placement

A worktree session always runs in the worktree's own space. `--placement`
therefore applies only with `--no-worktree`: with a worktree, any
`--placement` other than `new-space`, and `--workspace`, are refused
(exit 2), and a remembered or configured placement does not apply.

`tab-here` and `split-here` need to know where "here" is, and read
`HERDR_WORKSPACE_ID`, `HERDR_TAB_ID` and `HERDR_PANE_ID`, which herdr sets in
every pane's shell. `new-space` needs none of them, and neither does
`tab-in`, which names its space outright: the one already open on the
project, or the one `--workspace` gives. A missing variable is named in the
error.

## Output

Progress goes to **stderr**, one line per step. The result goes to
**stdout**, and nothing else does, so `$(herdr-draft create ...)` is either a
session or empty:

```
created workspace=w3 tab=w3:t2 pane=w3:p3 checkout=/path/to/checkout agent=claude branch=you/fix-login-redirect-loop
```

The ids name where the **agent** is, which is where you send its next
keystroke. `--json` also reports the space the run created, as `space_*`.
The two differ in one case: when herdr answers with a space that was already
open on the same checkout, the agent gets a fresh tab in it rather than that
space's existing pane.

### `--json`

One object on stdout. Keys that do not apply are left out, so test for
presence.

| Key | Meaning |
|---|---|
| `ok` | whether every step ran |
| `dry_run` | `true` for a `--dry-run` report |
| `error`, `failed_step` | what failed, and at which step |
| `title`, `project_dir`, `agent_kind`, `placement`, `worktree`, `branch`, `base` | what the session is |
| `account` | the pinned clauth profile, when one is pinned |
| `workspace_id`, `tab_id`, `pane_id` | where the agent is |
| `space_workspace_id`, `space_tab_id`, `space_pane_id` | the space the run created, which `--on-failure clean` acts on |
| `checkout_path` | the worktree's directory |
| `prompt_status` | `sent`, `unsent` or `unconfirmed`, whenever there was a prompt; see [Prompt delivery](#prompt-delivery) |
| `prompt_sent` | `true` or `false`; absent when the status is `unconfirmed`, or there was no prompt |
| `unsent_prompt`, `unconfirmed_prompt` | the prompt text, returned when it was not confirmed delivered |
| `mark_ready` | `true` when the prompt carried pane-reaper's instruction |
| `agent_options` | the session options you chose, such as `{"effort": "high"}` |
| `launch_options` | what the agent's command line carries for each option, including values `[agents.extra_args]` passes |
| `agent_args` | the whole argument list passed after the agent's command |
| `account_usage` | dry run only: the billed account's `profile` and its usage `windows` (`label`, `utilization_pct`, `resets_at`) |
| `on_failure`, `cleaned`, `clean_refused` | what happened to a failed session, and why a clean was refused |
| `deleted_branch`, `deleted_branch_tip` | a branch the clean deleted, and the commit it pointed at |
| `kept_branch`, `kept_branch_reason` | a branch the clean left in place, and why |
| `provenance` | where each value came from |

**`provenance`** maps each value to its source: `flag` for one you passed,
a file name (`projects.json`, `.herdr-draft.toml`, `last-used.json`,
`config.toml`), `built-in`, `herdr workspace list` for a `tab-in` chosen
because the project's space is open, `worktree` for the placement a worktree
decides, `checkout` for a base that is a linked worktree's current commit,
and `extra_args` for an option `[agents.extra_args]` supplies. Options
appear under `option.<name>`, such as `option.effort`.

**`agent_args`** is what `create` passes after the agent's command. The pane's
shell resolves that command, so a shell function named after the agent, or a
[`[clauth] launcher`](configuration.md#launch-and-launcher), can still change
what arrives.

**`account_usage`** is absent whenever it cannot be known: another agent
kind, clauth off, missing or failing, or a profile clauth does not report.
Absent means unknown, not "has room".

**With `--json`, exits 2, 3 and 5 print nothing on stdout.** The reason is on
stderr, as it is without `--json`. A caller piping into `jq` gets empty input
for those exits, so check the exit code first.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | created; with `--dry-run`, would create |
| 1 | the plan started and failed, and part of the session may exist (`--on-failure` applied) |
| 2 | bad usage, or a request that cannot be resolved; nothing was created |
| 3 | herdr is unreachable, found before anything was created |
| 4 | the plan's first step failed before making anything: nothing exists |
| 5 | a check did not answer in time; nothing was created |

**Exit 4 needs evidence.** It means herdr refused the first step before
acting, or was never asked. A worktree step herdr fails after git has run
can leave the branch, its checkout and even a space behind, so that is exit
1, and `clean_refused` says there is no space for `--on-failure clean` to
remove. Under `--json`, exit 4 still prints the object, with `ok: false`,
`failed_step` and `error`, and no ids.

**Exit 5 is the one that is not yours to fix.** Every question `create` asks
git before it starts (does the directory exist, is it a repository, what are
its root and main checkout, does the branch exist, what commit does the base
name) gives up after **thirty seconds** instead of waiting forever on a
stalled network drive. With `--issue`, two more questions leave the machine:
the `api_key_cmd` that reads your Linear key gets **sixty seconds**, since a
password manager may be waiting for your approval, and the Linear request
itself gets thirty. The line on stderr says which question ran out.

Read that line before retrying. A stalled mount answers a retry exactly as it
answered the first attempt, so report it to whoever owns the mount. Linear,
or a password manager waiting on an approval, may well answer next time. A
different flag helps in neither case.

Reading clauth's status, for `--account` or a `--dry-run --json` report, is
bounded at thirty seconds too, but a timeout there does not refuse: the create carries on
without it, as it does for any clauth failure. Calls to herdr itself are not
bounded. None of these limits is configurable.

## `--dry-run`

A dry run makes every check a create makes, with the same exit 2, 3 and 5
and the same reasons, then prints what it would create and stops. Nothing is
created and nothing is remembered.

```console
$ herdr-draft create --title "design the cache" --effort max --permission-mode plan --dry-run
would create title="design the cache" agent=claude branch=you/design-the-cache base=HEAD effort=max permission_mode=plan
```

With `--json`, it prints the object a create would, with `dry_run: true`, no
ids and no `prompt_status`, plus `account_usage`. `--account auto` asks your
picker with the picker's own `--dry-run`, so a preview uses up no account,
though the real pick can come out differently.

## Prompt delivery

`prompt_status` says what became of the prompt, and it has three values
rather than two:

| `prompt_status` | `prompt_sent` | The text comes back as | What to do |
|---|---|---|---|
| `sent` | `true` | — | nothing |
| `unsent` | `false` | `unsent_prompt` | read the pane, then resend |
| `unconfirmed` | absent | `unconfirmed_prompt` | read the pane; **never resend blind** |

**`unsent`** means herdr never accepted the text. Either herdr-draft refused
to type into the pane, because it was showing a dialog or had not finished
drawing, or the create stopped before the prompt step. It is not permission
to resend blind: typing into a dialog lets the prompt's Enter answer it.
Clear whatever the pane shows first. If there is no pane, the text is simply
yours to reuse.

**`unconfirmed`** means the text may already be in front of the agent.
Delivery is unknown when:

- herdr's wait for the agent to take the prompt ran out. A large prompt into
  an agent that is slow to redraw can time out while working perfectly well.
- the send stalled twice. herdr-draft sends once more after a stall, and
  herdr types the text before it starts watching, so the pane may hold two
  copies.
- the send went out, and the pane afterwards showed a dialog with none of the
  prompt on it, or stopped answering, which usually means the agent exited.
- herdr failed the send partway through, and cannot say how much it typed.
- anything failed after the text had gone out.

Resending an unconfirmed prompt is how a working agent gets its instructions
twice. Read the pane first.

## When a create fails

`--on-failure keep` (the default) leaves whatever the run made, and reports
its ids. `--on-failure clean` removes what this run created: the tab for
`tab-here` or `tab-in`, the pane for `split-here`, and the space or the
worktree otherwise. It never closes the repository's own space, which herdr
opens alongside a repository's first worktree.

`clean` is **refused**, with the reason in `clean_refused`, when the prompt
is `unconfirmed`: an agent may be working in there right now. The ids are
reported either way, so nothing is left without a way back to it.

**A cleaned worktree takes its branch with it**, so a retry with the same
title is not refused over a branch the failure left behind. The branch is
deleted only when all of these hold:

- this run made it. A branch that already existed, or that could not be
  checked, is kept.
- it holds no commit its base does not. A checkout with commits of its own
  refuses the whole clean.
- nothing else holds its commits once the checkout is gone. The agent keeps
  running while herdr removes its checkout, so a commit can still land.

`deleted_branch` and `deleted_branch_tip` name what went:
`git branch <deleted_branch> <deleted_branch_tip>` puts it back. A branch
left in place is `kept_branch`, with `kept_branch_reason`; the clean still
happened, so `cleaned` is `true`, but a retry that derives the same branch is
refused until you remove it.

**A worktree on a branch it did not ask for is a failed create.** herdr
reports the branch git checked out, and herdr-draft holds it to the one it
asked for. `--on-failure` then applies as it does to any later failure, and a
clean keeps the branch git made, since nothing shows this run asked for it.
