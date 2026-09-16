# herdr-draft — the `/spawn` skill: making agents aware of herdr-draft

- **Date:** 2026-09-16
- **Status:** design approved 2026-09-16; implemented 2026-09-16 (PR #133).
  Scheduled into **v0.1.0**, holding the tag for one more PR (owner's
  decision, 2026-09-16, over a recommendation to defer to 0.2.0).
  Two implementation departures from what is written below, both
  deliberate: §3 says the verb needs no injected function, and `dispatch`
  takes one anyway so the routing stays testable the way its other two
  verbs are; and §8.1's "every `create` flag" is enforced against
  `parseArgs`' own `flag.FlagSet` rather than against `createUsage`'s
  prose, which is stricter than this document asked for.
- **Supersedes:** nothing. This is new surface. `herdr-draft help`'s usage
  block gains a verb and `README.md` gains a section; neither is a
  correction.
- **Citations.** herdr line numbers and behaviour are for **v0.9.0**, the
  version `herdr-plugin.toml` pins as `min_herdr_version` and the version
  installed here (`herdr --version` → `herdr 0.9.0`). Do not cite
  `b1ff4582`: it is 59 commits behind v0.9.0 and differs in the prompt-wait
  semantics §7 depends on.

---

## 1. Summary

An agent working in a herdr pane, asked to hand work off to another agent,
does not reach for herdr-draft. It writes a handoff document and stops; or,
told explicitly to use herdr-draft, it improvises the topology and gets a
different answer each time.

This adds a third verb, `herdr-draft skill`, which prints one embedded
`SKILL.md` to stdout. Installed once into the user's personal skill
directory, it makes `/spawn` an invocable skill in every Claude Code
session on the machine, and — more importantly — makes an agent that forms
the *intent* to hand work off reach for `herdr-draft create` instead of
improvising.

The verb is the cheapest possible one: it follows `version`, not `create`.
No `Deps`, no `Runner`, no I/O beyond writing an embedded string to stdout.

**The skill's highest-value content is not "run `create`".** It is the
environment contract (§4) without which `create`, run from an ordinary
agent pane, silently resolves from built-in defaults and ignores the user's
entire configuration. That is measured in §2 and is the likeliest cause of
the "varying results" this design exists to fix.

---

## 2. Evidence

Everything in this section was measured in a live herdr 0.9.0 pane on
2026-09-16, not inferred.

### 2.1 The plugin environment in an ordinary pane names a different plugin

In an ordinary agent pane (not a plugin popup pane):

| variable | value |
|---|---|
| `HERDR_ENV` | `1` |
| `HERDR_BIN_PATH` | `/home/zvi/.local/bin/herdr` |
| `HERDR_WORKSPACE_ID` / `HERDR_TAB_ID` / `HERDR_PANE_ID` | set |
| `HERDR_PLUGIN_ID` | **unset** |
| `HERDR_PLUGIN_CONFIG_DIR` | `…/plugins/config/caioniehues.herdmates` |
| `HERDR_PLUGIN_STATE_DIR` | `…/herdr/plugins/caioniehues.herdmates` |
| `HERDR_PLUGIN_CONTEXT_JSON` | unset |

The two directory variables are set **and point at a different plugin**.
#91's guard is what makes this safe: with `HERDR_PLUGIN_ID` unset, nothing
says whose those directories are, so `usablePluginEnv`
(`internal/create/resolve.go:314-326`) drops all four of `PluginID`,
`ConfigDir`, `StateDir` and `ContextJSON` together — "they are one
environment, not four variables". `HERDR_BIN_PATH` is not among them and
is unaffected.

**This is not hypothetical.** The guard's own doc comment records the
pre-#91 leak, observed live with the directories pointing at another
plugin: three of herdr-draft's state files written into that plugin's state
directory, its `projects.json` read back into a resolution and reported
truthfully as `"placement": "projects.json"`, and its empty `config.toml`
standing in for the user's own — "which silently dropped
`[agents.extra_args]` and launched on the wrong model". #91 turned that
from a silent wrong answer into a disclosed built-in-defaults answer. It
did not make the answer the user wanted; only the exports do that.

**The consequence for `create`.** A hand-run `herdr-draft create` from an
agent pane therefore resolves with no `config.toml`, no `last-used.json`
and no `projects.json`: `TierUserConfig`, `TierGlobalMemory` and
`TierProjectMemory` are all absent, leaving `TierBuiltin` and — when the
repository has one — `TierRepoConfig`. No pinned clauth account, no
remembered placement, no Linear key, no `[agents] extra_args`, no
`[timeouts]`.

`create` already says so, on its first stderr line
(`internal/create/resolve.go:319-322`):

> `herdr-draft create: the HERDR_PLUGIN_* environment here does not say
> whose it is -- HERDR_PLUGIN_ID is not set, so it is not demonstrably
> "zvibaratz.draft"'s -- ignoring it and resolving from built-in defaults;
> export HERDR_PLUGIN_ID=… together with HERDR_PLUGIN_CONFIG_DIR=$(herdr
> plugin config-dir …) and HERDR_PLUGIN_STATE_DIR to resolve exactly what
> the form resolves`

The recipe is three exports, already documented at `README.md:373-389` and
`docs/manual-smoke.md:226-228` — in sections about driving the form by hand
for testing, which is not where an agent looks:

```bash
export HERDR_PLUGIN_ID="zvibaratz.draft"
export HERDR_PLUGIN_CONFIG_DIR="$(herdr plugin config-dir zvibaratz.draft)"
export HERDR_PLUGIN_STATE_DIR="${XDG_STATE_HOME:-$HOME/.local/state}/herdr/plugins/zvibaratz.draft"
```

Note the asymmetry, which §5 returns to: the config directory has a CLI
that prints it, and the state directory does not — it is spelled out as an
XDG path, which is the repository guessing at a layout herdr owns.

So this is not a missing capability. It is a correct, well-guarded
behaviour whose instruction reaches nobody at the moment they need it.

### 2.2 The binary is not reachable by name

`command -v herdr-draft` finds nothing. The binary is built by
`herdr-plugin.toml`'s `[[build]]` into `bin/herdr-draft` **inside the
plugin's install root**, and herdr invokes it by a path relative to that
root.

The root is not discoverable from the CLI in the general case:

- `herdr plugin list` prints **text, not JSON** (`10 plugins installed:` and
  one line per plugin), and for a GitHub install shows only
  `[github:owner/repo@<sha>]` — not the path.
- GitHub plugins unpack to
  `~/.config/herdr/plugins/github/<plugin_id>-<hash>/`. The hash is not
  derivable from the plugin id.
- `herdr plugin config-dir <id>` prints the **config** directory, which is a
  sibling of nothing useful.
- `herdr plugin action invoke <ACTION_ID>` accepts **no user arguments**, so
  `create`'s flags cannot travel through the action surface.

A locally linked install (`herdr plugin link`) does show its path, but that
is the developer's case, not a user's.

### 2.3 herdr's own skill occupies the niche and steers away

`herdr --skill` — installed at `~/.claude/skills/herdr/SKILL.md` — says:

> Default to a sibling pane in the current tab and the current working
> directory. **Do not create a workspace, tab, worktree, or different cwd
> unless the user explicitly requests that topology or location.**

An agent holding that skill, asked to spawn a session, correctly makes a
sibling pane and hand-rolls `herdr agent start` + `herdr agent prompt`.
This is not ignorance of herdr-draft; it is a competing instruction that is
already right about its own subject. Any skill added here has to say which
one wins, and when.

### 2.4 The mechanism exists and has precedent on both sides

- **herdr's pattern:** print the SKILL.md to stdout (`herdr --skill`), user
  installs it. That is how `~/.claude/skills/herdr/` got there.
- **Atrium's pattern:** materialise a two-file plugin under the data dir and
  pass `claude --plugin-dir <path>` at launch
  (`atrium:session/tmux/spawnskill.go`). Its own doc comment rules out the
  two channels it already had — status hooks point inward; the SessionStart
  brief is re-delivered on every `/clear` and is rune-budgeted — because
  neither can be **invocable**.
- **herdr's plugin manifest has no skill or context surface.** At v0.9.0 the
  schema is `build` / `startup` / `actions` / `event_hooks` / `panes` /
  `link_handlers` (`herdr:src/api/schema/plugins.rs`). herdr-draft cannot
  ask herdr to inject anything into an agent; it must use a moment it owns.
- At least one installed herdr plugin (`caioniehues.herdmates`) already
  ships a `skills/` directory in its tree. herdr does not wire it up.

---

## 3. The verb

```
herdr-draft skill
```

Prints the embedded `SKILL.md` to stdout and exits 0. Nothing else: no
flags, no `--install`, no `--check`.

It routes in `dispatch` (`cmd/herdr-draft/main.go:146-167`) beside
`version`, not beside `create`: a `case "skill"` that writes to `stdout`
and returns 0. No injected function is needed, because there is no I/O to
fake — which also means `dispatch`'s existing test shape covers it.

### 3.1 Two values are interpolated at print time

The embedded document is a template in exactly two places.

1. **The absolute path of the binary doing the printing**, from
   `os.Executable()`. This is the answer to §2.2: the emitted skill names
   the binary to run, so no agent has to discover it and nothing has to go
   on `PATH`. If `os.Executable()` fails, the verb prints the document with
   the plain name `herdr-draft` and a note on **stderr** saying the path
   could not be resolved and the reader should put the binary on `PATH`
   themselves — stdout stays a clean document, so the redirect in §3.2 is
   never corrupted.
2. **`herdrc.Version`**, stamped into the body. A stale installed copy is
   then visible to the person who opens it and to any agent that reads it,
   comparable against `herdr-draft version`.

Consequence, stated so it is a decision rather than a surprise: **the
emitted file is machine-specific and is not meant to be committed
anywhere.** It is generated, installed, and regenerated after an upgrade or
a reinstall that moves the binary.

### 3.2 Installation

```bash
mkdir -p ~/.claude/skills/spawn
herdr-draft skill > ~/.claude/skills/spawn/SKILL.md
```

Documented in `README.md` and in the skill's own body (so an agent reading
an installed copy can tell the user how to refresh it).

The directory name must equal the frontmatter `name`. Both are `spawn`;
§8.1 holds them together with a test.

### 3.3 Rejected: `--install` and `--check`

Writing into `~/.claude/` is a new trust statement. `SECURITY.md` claims
*"No writes outside its own plugin state directory and the git/herdr
operations you submit"*, and v0.1.0 is already correcting three other
claims in that file. Atrium's own comment records avoiding exactly this
("nothing into their claude config directory — a global side effect
outliving Atrium").

Reconsider if the version stamp shows staleness is a real problem.

### 3.4 Rejected: `--plugin-dir` injection

herdr-draft could append `--plugin-dir` to the agent argv it already
builds, so every session it creates inherits the skill with no install.
Elegant, and it is what Atrium does — but it bootstraps nothing. The
session that needs the skill is the one asking for the handoff, and that
one was not created by herdr-draft. Adding it later is cheap and does not
conflict with this design.

### 3.5 Rejected: a Claude Code marketplace plugin

A second distribution channel for a plugin already distributed through
herdr's marketplace, doing nothing the single skill does until there is
more than one skill in it.

---

## 4. The skill's content

The document is prose, reviewed as prose, and embedded rather than
generated (`//go:embed`) so it reads like the rest of the repository's
documentation and so the prose tests in §8 can scan it.

### 4.1 Frontmatter

`name: spawn`. The invocable name is the **intent**, not the product —
`/spawn` is what an agent and a user both reach for. Provenance is not lost
because the `description` names herdr-draft in its first clause.

The `description` must trigger on the intent, not on the product name.
That is the whole point: the observed failure is an agent writing a handoff
document and stopping. It should fire on handing work off, spawning a
follow-up, delegating a task, running work in parallel, and writing a
handoff prompt — **without herdr-draft being named** — and it must state
the `HERDR_ENV=1` precondition, as herdr's own skill does.

### 4.2 Body

Eight sections. The first and the last are the ones that fix the two
observed failure modes.

1. **What this is, and what it is not.** A herdr *session*: its own pane,
   its own agent process, usually its own git worktree, outliving the
   conversation that asked for it. **Not** the Agent tool's subagents,
   which share the caller's process and die with the turn. This paragraph
   exists to keep §4.1's deliberately broad trigger from firing on "spawn a
   subagent", and it comes first because that is the misfire that would
   discredit the skill fastest.

2. **The environment contract, before anything else.** §2.1's three
   exports, why `HERDR_PLUGIN_ID` is the one that makes the other two
   trusted, and the exact stderr line `create` prints when they are missing
   — quoted, so an agent recognises it rather than reading past it. This is
   the section the skill exists for.

3. **What the session starts from.** Worktree on or off, and what that
   decides; `--base`; and that uncommitted work in the invoking tree does
   **not** travel, so the right move is to offer to commit first rather
   than let the next agent discover the hole.

4. **Placement, as a table.** `new-space` / `tab-here` / `split-here` and
   when each is right. Both `here` values need herdr's pane variables,
   which herdr sets in every pane; a new space needs none of them. States
   the #128 limit: `create` cannot place a session in a workspace other
   than its own.

5. **Agent and account.** `--agent`; `--account` (claude only); `auto`,
   what it asks, and that a pick is spent when it runs.

6. **The prompt.** `--prompt -` reads stdin. A heredoc for a line or two;
   for a real brief, write it to a durable file and pipe it — reviewable
   before the session is created, and it survives a failed create. Then
   what a handoff must contain: what was done, where to start, what
   "finished" means in this repository, and what not to touch.

7. **Confirm once, with `AskUserQuestion`.** The chosen command first, two
   nearest alternatives beside it, each shown as the exact command it would
   run so the choice is reviewable rather than a label. Then honour the
   answer.

8. **Read the result; the exit code is not the whole story.** Exit codes
   0/1/2/3; that under `--json`, exits 2 and 3 print nothing on stdout, so
   a caller piping into `jq` gets empty input; that
   `workspace_id`/`tab_id`/`pane_id` name the **agent** while `space_*`
   name the **space**; `prompt_status` and what to do with each value —
   `unconfirmed` means **read the pane, never resend**. `--on-failure keep`
   is the default and is the right one for an agent, because a half-built
   session a human can look at is worth more than a tidy machine. Finish by
   reading the pane (`herdr agent read <pane> --source detection`) rather
   than trusting the exit status.

Plus a short **precedence** note: when the ask is *hand this work to
another agent*, this supersedes herdr's skill's sibling-pane default
(§2.3); `herdr agent start` by hand remains right for a pane that already
exists.

### 4.3 What the skill must not contain

- The popup. It is for a person; it has no pane id of its own, so nothing
  can send keys to it, and an agent cannot drive it.
- Anything about `herdr-draft` internals. The skill teaches a CLI, and
  `herdr-draft create --help` is named as the authority — the same posture
  herdr's own skill takes toward `herdr --help`.

---

## 5. Why not fix the environment contract instead of teaching it

Raised at review: a skill whose second section is three exports is a skill
describing a wart.

It is deliberate, for v0.1.0. The three variables are a **trust boundary**,
not an ergonomic gap: `HERDR_PLUGIN_ID` is the only thing that says whose
the other two directories are, and §2.1 shows a live pane where they belong
to a different plugin. Anything that makes `create` guess its own config
directory — deriving it from `PluginID` and an XDG path, say — would be
herdr-draft asserting an answer herdr owns, and would read another plugin's
directories on exactly the machine state measured in §2.1 if it got the
rule slightly wrong. That is #98's shape.

Teaching it costs a paragraph. Fixing it properly means asking herdr for a
way to resolve a plugin's own state directory by id (`herdr plugin
state-dir <id>`, beside the existing `config-dir`), which is an upstream
change. **Proposed follow-up issue,** filed against herdr, with this
section as the argument.

---

## 6. Release integration

v0.1.0, as a fifth PR, branch `zvi/spawn-skill`, based on `main`.

**It merges after the S1 prompt-posture PR.** §4.2's section 8 describes
`prompt_status`, and S1 changes exactly that: a surviving stall stops being
`unsent` and becomes `unconfirmed`. Writing this skill against pre-S1
behaviour would ship prose that is wrong the day it lands.

Merge order for the release becomes:

```
01 B1  →  02 S1  →  05 spawn skill  →  03 fetch  →  04 docs
```

03 and 04 remain independent and may land whenever they are reviewed.

Files this PR owns, none of which collide with the other four tasks'
regions: `cmd/herdr-draft/main.go` (the `dispatch` case and the `usage`
block), a new `internal/skill` package, a new `README.md` section, and a
new bullet in `CHANGELOG.md`'s `0.1.0` entry under an existing heading.

`herdr-plugin.toml` does not change. `herdrc.Version` does not change:
0.1.0 is still 0.1.0, it now contains this.

---

## 7. Scope

**In:** the `skill` verb, the embedded document, the tests in §8, the
README section, the changelog bullet, the usage line.

**Out:**

- `--install` / `--check` (§3.3).
- `--plugin-dir` injection into created sessions (§3.4).
- Any skill for a non-Claude agent kind. herdr-draft starts codex, gemini
  and others; none has an invocable-skill concept this shape fits, and
  guessing at `AGENTS.md` conventions for each is a separate design.
- Changes to herdr's own skill (§2.3). Proposed as an upstream issue, not
  depended on.
- `herdr plugin state-dir` (§5). Proposed as an upstream issue.

---

## 8. Testing

The risk this feature carries is **prose drifting from the binary**. A
skill that names a flag `create` does not have is worse than no skill,
because an agent will run it and report the failure as the user's problem.
So the tests are prose-to-code tests, which is an idiom this repository
already has (`readme_test.go` executes the README's example picker).

### 8.1 Held to code

- Every `create` flag the document cites exists in `create`'s real flag
  set, and every flag in that set that the document *should* cover is
  covered. The second half is what stops the document quietly falling
  behind a new flag.
- Every exit code the document names matches `internal/create`'s constants.
- Every `prompt_status` value the document names matches
  `internal/create/report.go`'s constants.
- The frontmatter `name` is `spawn`, and equals the install directory the
  README and the document itself tell the user to create.
- The version stamp in the rendered output equals `herdrc.Version`.
- The rendered output's frontmatter parses, and `description` is non-empty
  — it is the only thing that makes the skill trigger at all.

### 8.2 Held to the verb

- `dispatch` routes `skill` to stdout and returns 0, and an unknown verb
  still returns 2.
- The rendered output contains no `%!`-shaped formatting error and no
  unsubstituted placeholder.
- With `os.Executable()` failing, stdout is still a clean document and the
  note goes to **stderr** (§3.1), so `herdr-draft skill > FILE` never
  writes a warning into the file.

### 8.3 Not tested, and why

Whether the skill actually triggers. That is a property of Claude Code's
skill matching and the `description`'s wording, and nothing in this
repository can assert it. It is a manual-smoke cell: install it, open a
session, ask for a handoff in words that never say "herdr-draft", and see
whether `/spawn` fires. Added to `docs/manual-smoke.md`.

---

## 9. Out of scope, recorded so it is not re-proposed

- **A `/spawn` that drives the popup.** The popup has no pane id; nothing
  can send keys to it.
- **Teaching agents the raw three-command topology** (`herdr worktree
  create` + `agent start` + `agent prompt`). That is what they already
  improvise, and it is what this design exists to replace. herdr's own
  skill covers it correctly for the case where it *is* right — a pane that
  already exists.
- **A herdr-draft-side fix for the plugin environment** (§5).
