---
name: spawn
description: Use when handing work off to a new agent session - spawning a follow-up agent, delegating a task to run in parallel, continuing this work in a fresh session, or writing a handoff prompt for someone else to pick up. Creates a real herdr session with its own pane, agent and git worktree via herdr-draft; NOT for the Agent tool's in-process subagents. Covers the environment contract, choosing the worktree and placement, writing the prompt, and verifying what was actually created. Requires HERDR_ENV=1.
---

# Spawning a herdr session for another agent

You are being asked to hand work to another agent. Do not write a handoff
document and stop. Create the session, put the handoff in it, and tell the
user where it is.

One command does all of it:

```bash
{{HERDR_DRAFT_BIN}} create [flags]
```

`{{HERDR_DRAFT_BIN}} create --help` is the authority on the flags. This
document is the authority on *which* flags to reach for and what has to be
true before you run it.

## 1. What this is, and what it is not

A **herdr session** is a pane of its own, running an agent process of its
own, usually in a git worktree of its own. It outlives the conversation
that asked for it: the user can look at it tomorrow, type into it, and
close it when they are done. That is what this skill makes.

It is **not** the Agent tool's subagents. Those run inside your own
process, share your context, and die when the turn ends. If the ask is
"go read these forty files and summarise", that is a subagent and this
skill is the wrong tool. If the ask is "start someone on this", "pick this
up in a fresh session", "run this in parallel while I do the other thing",
or "write the handoff for the next agent" — that is a session, and this is
how to make one.

The precondition:

```bash
test "${HERDR_ENV:-}" = 1
```

If that fails you are not inside herdr, nothing below applies, and you
should say so rather than improvise.

## 2. Export the plugin environment first

**Do this before anything else.** herdr gives the three `HERDR_PLUGIN_*`
variables only to a plugin it launched itself. Your pane is not that, so
they are either unset or — measured live — set and pointing at a
*different* plugin that happens to have launched something in your
lineage.

```bash
export HERDR_PLUGIN_ID="zvibaratz.draft"
export HERDR_PLUGIN_CONFIG_DIR="$(herdr plugin config-dir zvibaratz.draft)"
export HERDR_PLUGIN_STATE_DIR="${XDG_STATE_HOME:-$HOME/.local/state}/herdr/plugins/zvibaratz.draft"
```

`HERDR_PLUGIN_ID` is the load-bearing one. It is the only variable that
says *whose* the other two directories are, and without it herdr-draft
refuses to trust them — a directory inherited from another plugin's pane
would otherwise have that plugin's configuration read as this one's. A
missing id and a wrong id are treated identically, on purpose.

When they are missing you get this on the first line of stderr, and it is
worth recognising rather than reading past:

> `herdr-draft create: the HERDR_PLUGIN_* environment here does not say
> whose it is -- HERDR_PLUGIN_ID is not set, so it is not demonstrably
> "zvibaratz.draft"'s -- ignoring it and resolving from built-in defaults;
> export HERDR_PLUGIN_ID=… together with HERDR_PLUGIN_CONFIG_DIR=$(herdr
> plugin config-dir …) and HERDR_PLUGIN_STATE_DIR to resolve exactly what
> the form resolves`

That is not a warning you can skip. It means no `config.toml`, no
`last-used.json`, no `projects.json`: **no pinned account, no remembered
placement for this project, no Linear key, no configured agent arguments,
no configured timeouts.** The session still gets made — from built-in
defaults, which are almost certainly not what the user configured. If you
have already created one without exporting these, say so.

## 3. What the session starts from

By default the new agent works in a **git worktree**: a fresh checkout on
a new branch, in a directory of its own, so it cannot collide with what
you are doing right now.

- `--worktree` / `--no-worktree` choose explicitly. Leave both off and the
  user's own default applies.
- `--branch NAME` names the branch. Left off, it is derived from the title.
- `--base REF` is what the branch is cut from. Left off, it is `HEAD` —
  which is *your current commit*, not the repository's default branch. If
  you are on a feature branch and the new work should start from `main`,
  pass it.
- `--project DIR` is the repository. Left off, it is the working directory.

**Uncommitted work does not travel.** A worktree is cut from a commit, so
anything you have edited and not committed is invisible to the new agent.
If your tree is dirty and the handoff depends on those edits, offer to
commit them first — do not let the next agent discover the hole.

`--no-worktree` puts the new agent in the same checkout you are in. That
is right for read-only work (a review, a summary, a question about the
code) and wrong for anything that will edit files, because two agents
writing to one checkout is exactly the collision the worktree exists to
prevent.

## 4. Where the pane goes

`--placement` decides where the agent's pane lands.

| value | where it lands | when it is right |
|---|---|---|
| `new-space` | a new top-level workspace | long-running, independent work the user will come back to |
| `tab-here` | a new tab in your workspace | related work they want beside this one |
| `split-here` | a split beside your pane | short work they want to watch |

`tab-here` and `split-here` mean *your* workspace, read from the
`HERDR_WORKSPACE_ID` / `HERDR_TAB_ID` / `HERDR_PANE_ID` that herdr sets in
every pane. `new-space` needs none of them.

**The limit worth knowing (#128):** "here" is fixed by the pane you are
running in, so the only two destinations are a brand-new workspace and the
one you are already in. A third — some *other* workspace that already
exists — cannot be named at all. If that is where the work belongs, say so
rather than quietly putting it somewhere else.

With a worktree and a `here` placement, the checkout gets a workspace of
its own *as well*, and the agent runs in your workspace, not in it. That
is why §8's two id triples are not the same thing.

## 5. Agent and account

- `--agent KIND` picks which agent to start — `claude`, or whatever else
  the user has configured. Left off, their default applies.
- `--account NAME` pins a clauth account, and applies to `claude` only.
  Left off, whatever account is active is used.
- `--account auto` asks the user's configured account picker to choose one
  for this project. It is a real call with real consequences: a picker
  that hands out accounts from a pool **spends one** when it runs. Do not
  pass it speculatively, and do not pass it at all unless the user has a
  picker configured — without one it is a usage error, not a quiet
  fallback.

If the user has not said anything about accounts, pass neither flag.

## 6. Write the prompt

`--prompt` is the first thing the new agent is told. `--prompt -` reads it
from stdin, which is how anything longer than a sentence gets in.

Keep the whole invocation on one line. For a sentence or two, pass it
inline:

```bash
{{HERDR_DRAFT_BIN}} create --title "triage the flaky CI job" --no-worktree --placement split-here --prompt "Look at the last three failures of the nightly job on main and tell me whether they share a cause."
```

For a real handoff, **write it to a durable file first and pipe that in**:

```bash
{{HERDR_DRAFT_BIN}} create --title "fix the login redirect loop" --worktree --base main --placement new-space --prompt - < ~/handoffs/login-redirect.md
```

Two reasons, both practical: the user can read the brief before the
session exists, and if the create fails the brief is still on disk instead
of only in a shell history.

A handoff prompt earns its length by answering four things the next agent
cannot find out on its own:

1. **What has already been done**, and where — the branch, the commits, the
   files.
2. **Where to start.** One concrete first action, not a goal.
3. **What "finished" means here** — the repository's own gate. If
   `CLAUDE.md` or a `justfile` names one, quote it.
4. **What not to touch.** Files another session owns, work in flight,
   anything mid-review.

`--issue ID` seeds the title, branch and prompt from a Linear issue, if
the user has Linear configured and the work has an issue. It is a
starting point for the prompt, not a substitute for the four things above.

## 7. Confirm once, then do it

Building the command is your job. Choosing among plausible ones is the
user's. Before running anything, ask **once**, with `AskUserQuestion`, and
show the **exact command** for each option rather than a label — a
placement is a word, but a command is reviewable.

Put the one you would run first, and beside it the two nearest
alternatives: usually the other worktree answer and the other placement.
Then honour whichever they pick. Do not ask again, and do not ask a
second question about a flag they did not raise.

If they gave you the whole topology already ("split it beside me, no
worktree"), they have answered; run it.

## 8. Read the result

**The exit code is a report about the command, not about the agent.**

| exit | meaning | what to do |
|---|---|---|
| 0 | created | report where it is |
| 1 | the plan started and failed | the session may half-exist; look at it |
| 2 | bad usage, or a request that cannot be resolved | fix the command and re-run |
| 3 | herdr is unreachable | nothing was created; stop |

`--json` prints one object instead of a human line, and is the right
choice whenever you are going to act on the result. Note that on exit 2
and exit 3 it prints **nothing** on stdout, so a pipe into `jq` gets empty
input — read the exit status before you read the JSON.

**The ids name the agent, not the worktree.** `workspace_id`, `tab_id` and
`pane_id` are where the agent actually is, which is what you address next.
`space_workspace_id`, `space_tab_id` and `space_pane_id` are the space the
checkout got, which is a different place whenever a worktree was combined
with a `here` placement.

**`prompt_status` is the field that matters**, and it has three values, not
two:

| value | means | what to do |
|---|---|---|
| `sent` | the agent acknowledged it | nothing |
| `unsent` | it never arrived | resend it; the text comes back as `unsent_prompt` |
| `unconfirmed` | delivery could not be confirmed | **read the pane. Never resend.** The text is under `unconfirmed_prompt` |

`unconfirmed` is not a failure. It means the confirmation timed out while
the agent was demonstrably busy — very often because the agent was already
several tool calls into the prompt and slow to paint its status. Resending
is how a working agent gets its instructions twice.

`--on-failure keep` is the default and is the right one for you. A
half-built session a human can open and look at is worth more than a tidy
machine, and `--on-failure clean` on an `unconfirmed` prompt would kill an
agent mid-turn — herdr-draft refuses that combination for exactly that
reason.

**Finish by looking at the pane**, not by trusting the exit status:

```bash
herdr agent read w12:p3 --source detection --format text
```

A launch that reported success can still be sitting on a first-run trust
dialog, a login chooser, or a permission prompt — the agent asking a
question nobody is there to answer. If it is, tell the user which pane and
what it is asking. That, and the pane id, is the whole report the user
needs from you.

## Precedence over herdr's own skill

herdr's skill says to default to a sibling pane in the current tab and the
current directory, and not to create a workspace, tab or worktree unless
asked. That is right for the case it is about: putting a command or an
agent into the space you are already in.

**When the ask is "hand this work to another agent", this skill wins.** A
handoff is the case where a worktree and a topology of its own are the
point, not an unrequested flourish. `herdr agent start` by hand stays
correct for a pane that already exists.

---

Generated from herdr-draft {{VERSION}}. Compare that against what
`{{HERDR_DRAFT_BIN}} version` prints, and if they differ this file is
stale — regenerate it:

```bash
mkdir -p ~/.claude/skills/spawn
{{HERDR_DRAFT_BIN}} skill > ~/.claude/skills/spawn/SKILL.md
```

The path above is this machine's. Reinstalling or upgrading the plugin can
move the binary, which is why the file is regenerated rather than copied.
