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
"{{HERDR_DRAFT_BIN}}" create [flags]
```

`"{{HERDR_DRAFT_BIN}}" create --help` is the authority on the flags. This
document is the authority on *which* flags to reach for, what has to be
true before you run it, and what the result actually tells you.

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

It is also not the only answer. If the user asked for a handoff *document*
— something to paste into a session they will start later, or somewhere
else, or by hand — then write the file and create nothing. Section 7 says
how to tell the two apart without guessing.

The precondition:

```bash
test "${HERDR_ENV:-}" = 1
```

If that fails you are not inside herdr, nothing below applies, and you
should say so rather than improvise.

## 2. Export the plugin environment, in the same command

**This is the step that goes wrong.** herdr gives the three
`HERDR_PLUGIN_*` variables only to a plugin it launched itself. Your pane
is not that, so they are either unset, or set and pointing at a *different*
plugin's directories.

```bash
export HERDR_PLUGIN_ID="zvibaratz.draft"
export HERDR_PLUGIN_CONFIG_DIR="$(herdr plugin config-dir zvibaratz.draft)"
export HERDR_PLUGIN_STATE_DIR="${XDG_STATE_HOME:-$HOME/.local/state}/herdr/plugins/zvibaratz.draft"
"{{HERDR_DRAFT_BIN}}" create --title "fix the login redirect loop" --worktree
```

**Run that whole block as one command.** Your shell does not survive
between tool calls — environment variables set in one call are gone by the
next, even though the working directory persists. Exporting in one call and
running the create in the next gets you exactly the failure the exports
exist to prevent, quietly enough that you would report success. Every
example further down is the last line of this block; keep them together.

`HERDR_PLUGIN_ID` is the load-bearing one. It is the only variable that
says *whose* the other two directories are, and without it herdr-draft
refuses to trust them — a directory inherited from another plugin's pane
would otherwise have that plugin's configuration read as this one's. A
missing id and a wrong id are treated identically, on purpose.

**What skipping it costs.** No `config.toml`, no `last-used.json`, no
`projects.json`: no pinned account, no remembered placement for this
project, no Linear key, no configured agent arguments, no configured
timeouts. The session is still made — from built-in defaults, which are
almost certainly not what the user configured.

You are told, on the first line of stderr, but the wording depends on which
variables were wrong, so match on the shared part rather than on a whole
sentence. The line begins `herdr-draft create:` and says it is **resolving
from built-in defaults** or **resolving without your config.toml and
remembered defaults**, then names what to export. If you see it, the
session you just made ignored the user's configuration — say so.

## 3. What the session starts from

By default the new agent works in a **git worktree**: a fresh checkout on
a new branch, in a directory of its own, so it cannot collide with what
you are doing right now.

- `--worktree` / `--no-worktree` choose explicitly. Leave both off and the
  user's own default applies.
- `--title TEXT` names the session: its agent, and the space or tab it
  opens. A title over 32 runes is cut to 32, as the form cuts it, and one
  line on stderr gives the title used, so write one that fits.
- `--branch NAME` names the branch. Left off, it is derived from the title.
- `--base REF` is what the branch is cut from. Left off it resolves like
  every other unset value — usually `HEAD`, which is *your current commit*,
  though a repository's own `.herdr-draft.toml` can set a different
  default. If you are on a feature branch and the new work should start
  from `main`, pass it rather than assume.
- `--project DIR` is the repository. Left off, it resolves to the working
  directory.

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

**With a worktree there is nothing to choose.** A worktree session runs in
the worktree's own space, which herdr's sidebar groups under the
repository's. `--placement` other than `new-space`, and `--workspace`, are
refused with a worktree (exit 2), so leave both off.

Without one (`--no-worktree`), `--placement` decides where the session's
pane lands.

| value | where it lands | when it is right |
|---|---|---|
| `tab-in` | a new tab in the workspace already holding the project's checkout | the default whenever the repository has a space open: work that belongs with the repo, not beside you |
| `new-space` | a new top-level workspace | long-running, independent work the user will come back to, in a repository with no space open |
| `tab-here` | a new tab in your workspace | related work they want beside this one |
| `split-here` | a split beside your pane | short work they want to watch |

`tab-here` and `split-here` mean *your* workspace, read from the
`HERDR_WORKSPACE_ID` / `HERDR_TAB_ID` / `HERDR_PANE_ID` that herdr sets in
every pane. `new-space` and `tab-in` need none of them.

**Leave `--placement` off unless you mean it.** With nothing passed and no
worktree, `create` picks `tab-in` when `herdr workspace list` already shows a
workspace on the project's checkout, and `new-space` when it does not — so
the repository's own space collects its sessions as tabs instead of a new
top-level space appearing per task. Pass `--placement new-space` to insist
on a space of its own.

**Any other existing workspace can be named outright:** `--workspace <id>`
(ids from `herdr workspace list`) puts the tab in that workspace and implies
`tab-in`. A script running in one space can therefore start a session in
another without pretending to be a pane there.

## 5. Agent and account

- `--agent KIND` picks which agent to start — `claude`, or whatever else
  the user has configured. Left off, their default applies.
- `--account NAME` pins a clauth account, and applies to `claude` only.
  Left off, or given as `active`, whatever account is active is used.
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

For a sentence or two, a **quoted** heredoc — `<<'EOF'`, not `<<EOF`, so
that a `$`, a backtick or a quote in the text reaches the agent instead of
being expanded by your own shell:

```bash
# with section 2's three exports above it, in the same command
"{{HERDR_DRAFT_BIN}}" create --title "triage the flaky CI job" --no-worktree --placement split-here --prompt - <<'EOF'
Look at the last three failures of the nightly job on main and tell me
whether they share a cause.
EOF
```

For a real handoff, **write it to a durable file first and pipe that in**:

```bash
# with section 2's three exports above it, in the same command
"{{HERDR_DRAFT_BIN}}" create --title "fix the login redirect loop" --worktree --base main --prompt - < ~/handoffs/login-redirect.md
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
The issue's title is cut to 32 runes like any other. The prompt it seeds
is not cut.

`--reap` ends the prompt with an instruction to mark the new pane ready
for **pane-reaper**, which closes it once the agent has saved its work and
gone idle. Pass it only when the user runs pane-reaper, and only for a
worker nobody will steer afterwards — never for an orchestrator or a
`/loop` session, which must not close themselves. It adds nothing to an
empty prompt or a slash command. `--no-reap` turns it off where the user's
configuration turned it on. With neither, leave the choice to that
configuration.

## 7. Confirm once, then do it

Building the command is your job. Choosing among plausible ones is the
user's. Before running anything, ask **once**, with `AskUserQuestion`, and
show the **exact command** for each option rather than a label — a
placement is a word, but a command is reviewable.

Put the one you would run first, and beside it the two nearest
alternatives: usually the other worktree answer and, for a session without
a worktree, the other placement.

**Offer "write the brief to a file and create nothing" as one of the
options whenever the user's own words asked for a document** — "write a
handoff", "draft a prompt I can paste", "write this up for the next
session". They may want the file and no session, and putting it on the
list is the only way to find out without guessing. Show the path you would
write.

Then honour whichever they pick. Do not ask again, and do not ask a second
question about a flag they did not raise. If they already gave you the
whole topology ("split it beside me, no worktree"), they have answered;
run it.

## 8. Read the result

**The exit code is a report about the command, not about the agent.**

| exit | meaning | what to do |
|---|---|---|
| 0 | created | report where it is |
| 1 | the plan started and failed | the session may half-exist; look at it |
| 2 | bad usage, or a request that cannot be resolved — including a branch or title already in use, and a pinned account clauth reports as signed out | fix the command and re-run |
| 3 | herdr is unreachable | nothing was created; stop |

`--json` prints one object instead of a human line, and is the right
choice whenever you are going to act on the result. Note that on exit 2
and exit 3 it prints **nothing** on stdout, so a pipe into `jq` gets empty
input — read the exit status before you read the JSON.

**The ids name the agent, not the space.** `workspace_id`, `tab_id` and
`pane_id` are where the agent actually is, which is what you address next.
`space_workspace_id`, `space_tab_id` and `space_pane_id` are the space the
run established. They differ only when herdr answered with a workspace
that was already open for the checkout, and the agent was given a fresh
tab in it.

**`prompt_status` is the field that matters**, and it has three values, not
two. Each is a statement about what you *know*, so act on the last column
rather than reasoning backwards from what you assume went wrong:

| value | what it tells you | what to do |
|---|---|---|
| `sent` | the prompt reached the agent | nothing |
| `unsent` | the text is not in front of the agent — which is not the same as never typed | **read the pane first**, then resend `unsent_prompt` |
| `unconfirmed` | delivery is **unknown**; it may have arrived in full | **read the pane. Never resend.** The text is under `unconfirmed_prompt` |

`unsent` is not permission to resend blind. It covers three shapes, and
only one of them is the simple one:

- **a guard refused to send**, because the pane was showing a blocking
  dialog or had not finished painting. Resending types your prompt into
  that dialog, where the trailing Enter answers whichever option is
  highlighted.
- **the send went out and left no trace** on the screen afterwards. There
  may be no agent left to receive a second copy.
- **the run stopped before the prompt step**, so nothing was typed. Here
  there may be no pane to read at all, if it failed before one was made.

So read the pane, clear whatever it is showing or hand that to the user,
and only then resend. If there is no pane, there is nothing to clear and
the text is simply yours to reuse.

`unconfirmed` is not a failure. It means nothing observed confirms delivery
and nothing observed rules it out. Resending on that is how an agent that
is already working gets its instructions twice — and note that the text may
have gone out **more than once** already, so a pane showing two copies of
the prompt is a thing this status covers rather than a second bug to chase.

`--on-failure keep` is the default and is the right one for you: a
half-built session a human can open and look at is worth more than a tidy
machine. Passing `--on-failure clean` does not override that for an
`unconfirmed` prompt — the clean is refused at failure time and the session
kept, with the reason under `clean_refused`.

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
current directory, and not to make a workspace, tab or worktree unless
asked. That is right for the case it is about: putting a command or an
agent into the space you are already in.

**When the ask is "hand this work to another agent", this skill wins.** A
handoff is the case where a worktree and a topology of its own are the
point, not an unrequested flourish.

Two things stay with herdr's rule. `herdr agent start` by hand is still
correct for a pane that already exists. And an explicit request for a
sibling pane in the current directory is honoured as asked — either
through herdr's own skill, or through this one with `--no-worktree
--placement split-here`, which builds the same topology.

---

Generated from herdr-draft {{VERSION}}. Compare that against what
`"{{HERDR_DRAFT_BIN}}" version` prints; if they differ, this file is stale.
To regenerate it:

```bash
mkdir -p ~/.claude/skills/spawn
"{{HERDR_DRAFT_BIN}}" skill > ~/.claude/skills/spawn/SKILL.md
```

The path above is this machine's. Reinstalling or upgrading the plugin can
move the binary, which is why the file is regenerated rather than copied.
