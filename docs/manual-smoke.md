# Manual smoke matrix

Not part of CI (v2 spec §14: "manual release smoke stays in
`docs/manual-smoke.md`"). Run it once per release, against the herdr
version(s) you intend to support, before publishing. It exercises the real
popup, the real herdr CLI, and (for Path B) a real clauth profile — nothing
here is mocked.

Seven cells: the original four (Path A/B × worktree on/off), plus three that
cover what v2 added — the headless `create`, the repo-level
`.herdr-draft.toml`, and per-project memory.

---

> ## ⚠ `create` creates. Really.
>
> herdr exports `HERDR_WORKSPACE_ID` / `HERDR_TAB_ID` / `HERDR_PANE_ID` into
> **every** pane's shell, and `$HERDR_BIN_PATH` reaches a live server. So
> `herdr-draft create` typed into any pane of your *normal* session builds
> real topology *in that session* — a worktree, a workspace, a running
> agent — with no confirmation step, because a headless verb never prompts.
>
> Before any `create` you do not intend to land, unset all three **and**
> point `HERDR_BIN_PATH` at a path that does not exist:
>
> ```sh
> unset HERDR_WORKSPACE_ID HERDR_TAB_ID HERDR_PANE_ID
> HERDR_BIN_PATH=/nonexistent/herdr herdr-draft create --title probe --no-worktree
> # herdr-draft create: herdr unreachable: ... : no such file or directory
> # exit 3
> ```
>
> Exit 3 with nothing created is the proof the guard held. Unsetting the
> three ids alone is not enough (a `new-space` placement needs none of them);
> the unreachable binary is what makes the exercise inert. This caught the v2
> program's own reviewer, in a live session, which is why it is a box and not
> a footnote.
>
> The same trick does **not** work for the form: `Bootstrap` probes herdr
> with `workspace list` and refuses to open at all when it fails (v1 spec
> §9). To exercise the form without creating anything, open it against a
> real herdr and leave by `esc`.

---

## Before you start

- Build the binary you're testing: `just build` from a clean `git status` (or
  note exactly which uncommitted changes you're testing).
- Confirm `herdr --version` and, for Path B, `clauth --version`. Record both
  in your smoke notes — this matrix's whole point is pinning "what actually
  works against what."
- Each scenario needs its own throwaway git repo under `/var/tmp` (one commit
  is enough; `git init && git commit --allow-empty -m init` in a fresh
  directory). Do not reuse a scenario's repo for the next one — branch and
  `projects.json` state from a previous run will change what you are actually
  testing.
- For Path B, pick a real clauth profile you're willing to spend a launch on:
  `clauth status --json`, then choose (e.g. the lowest-utilization one).

### Somewhere isolated to run

The disposable-session route this document used to prescribe does not work as
written. From inside a herdr pane:

```
$ herdr --session repro-draft-smoke-1
error: nested herdr is disabled by default.
see configuration if you want to enable it.
```

herdr blocks it whenever `HERDR_ENV` marks the shell as herdr-managed
(`herdr:src/main.rs`'s `should_block_nested`). Two ways forward. Route B is
usually the faster one, and it is the only one that gives the form under test
a pane id of its own.

**Route A — enable nesting, then use a disposable session.** In herdr's own
`config.toml`:

```toml
[experimental]
allow_nested = true
```

Then, from your normal session, split off an outer pane with a scratch cwd
(e.g. `/var/tmp/herdr-draft-smoke`) and record its pane id — this is the only
pane cleanup may close. Start the disposable session in it, clearing the
inherited socket/session environment first:

```bash
env -u HERDR_SOCKET_PATH -u HERDR_CLIENT_SOCKET_PATH -u HERDR_SESSION \
    -u HERDR_WORKSPACE_ID -u HERDR_TAB_ID -u HERDR_PANE_ID \
    herdr --session repro-draft-smoke-$(date +%s)
```

Drive every command below with that session name pinned and every inherited
override cleared:

```bash
env -u HERDR_SOCKET_PATH -u HERDR_CLIENT_SOCKET_PATH \
    -u HERDR_WORKSPACE_ID -u HERDR_TAB_ID -u HERDR_PANE_ID \
    HERDR_SESSION=<your-session-name> herdr <command>
```

(elided below as `herdr[S] <command>` — substitute the full prefix.) Plugin
install/link state is global, so a plugin linked from your normal session is
already visible in the disposable one; confirm with `herdr[S] plugin list`.

**Route B — run the binary directly, with no popup.** herdr hands a launched
plugin four variables. Set them yourself in a scratch pane and the same form
runs against the same real data, in an ordinary pane you can address:

```bash
export HERDR_BIN_PATH="$(command -v herdr)"
export HERDR_PLUGIN_CONFIG_DIR="$(herdr plugin config-dir draft)"
export HERDR_PLUGIN_STATE_DIR="${XDG_STATE_HOME:-$HOME/.local/state}/herdr/plugins/draft"
export HERDR_PLUGIN_CONTEXT_JSON="$(printf \
  '{"workspace_id":"%s","workspace_cwd":"%s","tab_id":"%s","focused_pane_id":"%s","focused_pane_cwd":"%s"}' \
  "$HERDR_WORKSPACE_ID" "$PWD" "$HERDR_TAB_ID" "$HERDR_PANE_ID" "$PWD")"
./bin/herdr-draft
```

The context JSON's shape is `internal/herdrc/context.go`'s `Context`; every
field on it is optional, so the five above are enough to open the form with a
real invoking workspace and pane. Everything the form *reads* — Linear,
clauth, git, your config and state directories — is real, and a submit is a
real submit, so leave by `esc` unless you mean it.

Route B is the right one for anything about the form's own content (row
values, panels, resolution, `.herdr-draft.toml` notes, per-project memory).
Route A is what proves the popup itself still works, which is cell 1's job.

## Driving the popup

**The popup has no independent pane id.** There is nothing to send keys to
and nothing to read from. Drive it through the **outer host pane** — the
pane the popup is drawn over:

```bash
herdr[S] plugin pane open --plugin draft --entrypoint open
herdr[S] pane send-text <outer-pane-id> "<text>"
herdr[S] pane send-keys <outer-pane-id> <key>
herdr[S] pane read --source visible <outer-pane-id>
```

This is the single most time-wasting thing not to know.

**Read with `--source visible`, not `recent-unwrapped`.** herdr-draft is a
full-screen Bubble Tea program and runs on the terminal's alternate screen,
which never writes to scrollback — so `--source recent-unwrapped`, the
default advice for reading a pane, comes back with nothing at all for this
TUI. `--source visible` reads the screen as drawn, which is what you want
here. (The advice is fine for the shell panes around it; the exception is any
alternate-screen program, this one included.)

### What you are looking at

At the 80×24 floor, fully configured:

```
new session                                                  herdr-draft · main
───────────────────────────────────────────────────────────────────────────────
  issue      none
  title      fix login redirect loop
  prompt     Work on ENG-101: Fix login redirect loop
  project    ~/Projects/herdr-draft
  worktree   on · zvi/fix-login-redirect-loop ← main
  placement  worktree opens as its own space
  agent      claude
  account    active · max · 12%
───────────────────────────────────────────────────────────────────────────────
  Work on ENG-101: Fix login redirect loop

⌃J newline · ⇥ move · ⌃R clear                          ↵ create    esc cancel
```

- **Eight rows, one line each**, in this order: `issue`, `title`, `prompt`,
  `project`, `worktree`, `placement`, `agent`, `account`. Branch and base are
  not rows — they are parts of the worktree row's panel. A row never moves;
  if one does, that is a regression, not a rendering quirk.
- `issue` is absent unless Linear is configured, and `account` unless clauth
  is configured with ≥2 profiles. With neither, the stack is six rows.
- **The focused row is painted**, full width, in `ActiveRowBG`. There is no
  gutter column and no `▎` marker.
- **The panel below the second rule is the only chooser** — candidate lists,
  the prompt editor, verdicts, notes. It belongs to the focused row and to no
  other.
- **Create is on the footer**, right-aligned (`↵ create`, `esc cancel`),
  not a row in the stack. It is still the ring's last focus stop: `⇥` past
  `account` lands on it.
- The header's right half is live context for the **selected** project —
  its directory name and the branch checked out there — not the invoking
  workspace. Watch it change when you set the project row.

### The walk

Focus opens on **title** (v2 spec §8), not on the first row. The steps in
each cell below assume this walk:

| Key | Where it goes |
|---|---|
| `⇧⇥` from title | `issue` (one step up, deliberately) |
| `⇥` | next row: title → prompt → project → worktree → placement → agent → account → create |
| `↑↓` | move within the focused row's panel |
| `←→` | chips (worktree on/off, placement, agent favorites) |
| `⇥` in a picker | complete, then advance |
| `↵` | create, from a non-empty title, from the prompt, or from the create button; advance from anywhere else |
| `⌃S` | create, from anywhere |
| `⌃J` | newline in the prompt (`↵` there creates) |
| `⌃R ⌃R` | clear the form back to its resolved defaults |
| `esc` | cancel |

## The matrix

Four cells: Path A (no account pin, or a non-claude kind) × Path B (pinned
clauth profile, claude only), each × worktree on/off. Run all four every
release.

### Cell 1 — Path A, worktree on

**Setup:** fresh throwaway repo, e.g. `/var/tmp/herdr-draft-smoke-a-wt`. Run
this cell through the real popup (Route A) — it is the one that proves the
popup path.

**Steps:**

1. Open the popup. Confirm focus is on the **title** row (it is painted) and
   the footer reads `name it to create · ⇥ for the prompt`. Confirm the
   opening stack: `title untitled`, `prompt —`, and — this is the state that
   shipped broken once — `worktree  on · from main`, with no invented branch
   name in it.
2. `⇧⇥` to **issue**. Leave it on `none`, or pick a real issue if Linear is
   configured and you want to smoke that path too. A chosen issue seeds the
   title, the branch and the prompt; confirm the `title`, `prompt` and
   `worktree` rows all change to match it.
3. `⇥` back to **title**. Type a short distinctive title (e.g.
   `smoke a wt`). The panel reads `branch will be <branch_prefix><slug>`.
4. `⇥` to **prompt**. Optionally type one; `⌃J` for a newline. The row shows
   the first line plus a dim ` +N more`.
5. `⇥` to **project**. Type the throwaway repo path; `⇥` completes. Confirm
   the header's right half becomes `<repo-dir-name> · <branch>`.
6. `⇥` to **worktree**. The panel is the three-part editor —
   `worktree  off · on`, `branch`, `base` — with `↑↓` between parts and `←→`
   on the chips. Confirm **on**, a seeded branch, and a base list carrying
   `HEAD (<branch>)` first. While the base list is on screen the `base` part
   line is empty: the list is what says which base is selected.
7. `⇥` to **placement**. With the worktree on it reads
   `worktree opens as its own space` and its panel says
   `turn the worktree off to choose`. It cannot be changed; that is correct.
8. `⇥` to **agent**. Leave the default favorite (`claude` unless your config
   changes it). `←→` walks the favorites, `↑↓` the full kind list.
9. `⇥` to **account**. Leave it on `active` — **do not pin a profile**. This
   is what makes it Path A even when clauth is configured.
10. `⌃S` to submit (or `⇥` to the create button and `↵`).

**Expected:** the screen replaces the row stack with the progress stack —
same header, same rule, same label column — and the footer becomes a step
counter:

```
✓ worktree   <branch> from <base>
✓ workspace  smoke a wt
› claude     smoke-a-wt
  prompt     queued
...
step 3 of 4
```

`✓` done, `›` running, `✗` failed, blank pending; a step with nothing of its
own to say prints the state word (`queued`, `working…`, `done`). The label
column is the agent *kind*; the agent step's value is the herdr agent name,
a slug of the title. Then the popup closes.

`herdr[S] workspace list` shows a new workspace grouped with the throwaway
repo's origin workspace (both are created by `worktree create` — see
teardown). `herdr[S] pane list` shows the new pane's agent as `claude` (or
whatever kind you picked) once detection completes
(`herdr[S] agent get <pane-id>`).

**Expected on a fresh worktree with a prompt (normal, not a bug):** if you
typed a prompt and this is Claude Code's first run in a brand-new worktree
directory, Claude Code shows its own first-run trust dialog. herdr-draft's
prompt-delivery dialog guard recognizes it and refuses to send the prompt.
The pipeline stops on the failure screen: the stack stays, the failed row
carries the reason in red, and under a second rule the keep/remove choice
appears as buttons on the footer.

```
✓ worktree   <branch> from <base>
✓ workspace  smoke a wt
✗ prompt     agent is waiting on a dialog ("<signature>") -- prompt not sent
...
───────────────────────────────────────────────────────────────────────────────
  prompt not sent — saved for manual paste:
  <state dir>/unsent-prompt.txt
  remove undoes everything this create made
                                                      k keep it    c remove it
```

This is documented behavior (README's "Known limitations"), not a smoke
failure — press `k`, confirm the agent process is still alive
(`herdr[S] pane process-info --pane <pane-id>` should still show `claude`),
and move on. Confirm the file the screen named actually holds your prompt.

Worth exercising deliberately at least once: with a **dirty** checkout,
`remove` must go unavailable rather than silently refusing. The reason
replaces the rationale line and the button loses its key glyph:

```
  remove unavailable  uncommitted changes
                                                        k keep it    remove it
```

**Teardown:** `herdr[S] worktree remove --workspace <worktree-workspace-id>`
(only removes cleanly when the checkout has no commits beyond base — true
here since nothing was committed), then `herdr[S] workspace close
<origin-workspace-id>` for the second, non-worktree workspace `worktree
create` always opens alongside it. Delete the throwaway repo directory.

### Cell 2 — Path A, worktree off

**Setup:** fresh throwaway repo, e.g. `/var/tmp/herdr-draft-smoke-a-nowt`.

**Steps:** same walk as Cell 1, except:

6. **worktree**: `←→` to **off**. The row collapses to `off`, and the branch
   and base parts stop mattering.
7. **placement**: now live. Its panel is the chip row with a per-choice
   explanation underneath. Exercise at least one of `new space` / `tab here`
   / `split here`; rotate which one across releases so all three get covered
   eventually.

**Expected:** the progress stack's first row matches the placement you
picked — `workspace`, `tab` or `pane` — then the agent row and the prompt
row as in Cell 1. No worktree and no second origin-repo workspace:
`herdr[S] workspace list` / `tab list` / `pane list` should show exactly one
new container.

**Teardown:** close whatever was created (`herdr[S] workspace close`,
`tab close`, or the pane simply exits) — no `worktree remove` needed. Delete
the throwaway repo directory.

### Cell 3 — Path B, worktree on

**Setup:** fresh throwaway repo, e.g. `/var/tmp/herdr-draft-smoke-b-wt`.
Requires clauth configured with ≥2 profiles — otherwise there is no
**account** row at all (see README's config reference), and the cell cannot
be run.

**Steps:** Cell 1's walk through step 8 (agent must be `claude` — Path B is
claude-only), then:

9. **account**: `↑↓` to a specific profile, not `active`. This is what makes
   it Path B. Confirm the row picks up the profile's own state word —
   `<name> · <plan> · ok`, with a rate-limited profile's percentage in the
   warning color and an expired one reading `sign in again` in red.
10. Submit.

**Expected:** the agent step reads `claude   under clauth <the profile you
picked>`, followed by a `detection  waiting for the agent` step, then the
prompt row as in Cell 1 (including the same first-run-dialog caveat).
`herdr[S] pane list` on the launched pane should show `agent: "claude"` and
`tokens.clauth: "<the profile you picked>"`.
`herdr[S] pane process-info --pane <pane-id>` should show **both** `clauth`
(parent) and `claude` (child).

**Teardown:** same as Cell 1.

### Cell 4 — Path B, worktree off

**Setup:** fresh throwaway repo, e.g. `/var/tmp/herdr-draft-smoke-b-nowt`.

**Steps:** combine Cell 2 (worktree off, a placement) with Cell 3 (agent
`claude`, account pinned).

**Expected:** the placement step from Cell 2, then the clauth agent step and
detection step from Cell 3, then the prompt row. Same `pane list` and
`process-info` checks as Cell 3.

**Teardown:** same as Cell 2.

## What v2 added

### Cell 5 — headless `create` and its exit codes

`create` shares the form's resolver, so this cell is about the command
surface, not about resolution. With the two lines below in effect every probe
in the table is inert. **Re-read the warning at the top of this file before
the live run that follows it.**

```bash
unset HERDR_WORKSPACE_ID HERDR_TAB_ID HERDR_PANE_ID
export HERDR_BIN_PATH=/nonexistent/herdr
```

| Probe | Expected |
|---|---|
| `herdr-draft create --no-worktree` | `a title is required: pass --title, or --issue to take one from Linear` → **exit 2** |
| `herdr-draft create --title x --placement nowhere` | `unknown --placement "nowhere": expected new-space, tab-here or split-here`, then the usage block → **exit 2** |
| `herdr-draft create --title x --no-worktree --placement tab-here` | `HERDR_WORKSPACE_ID is not set: --placement tab-here creates the tab in the workspace this pane belongs to` → **exit 2** (the missing variable is named exactly) |
| `herdr-draft create --title x --no-worktree --placement new-space` | `herdr unreachable: herdr workspace list: ...` → **exit 3** |
| `herdr-draft bogus` | the top-level usage block → **exit 2**; `herdr-draft help` prints the same at **exit 0** |

Also confirm, with `HERDR_PLUGIN_CONFIG_DIR` / `HERDR_PLUGIN_STATE_DIR`
unset, that the first stderr line is the warning that `create` is resolving
without your config and remembered defaults. A headless caller that silently
resolved from built-in defaults would be the worst failure mode this command
has.

Then, **in a disposable session only** (Route A), with a real
`HERDR_BIN_PATH`, run one that lands:

```bash
herdr[S] pane send-text <scratch-pane-id> \
  'herdr-draft create --title "smoke headless" --worktree --json'
```

Expect exit 0, one JSON object on stdout with `"ok": true`, the created
`workspace_id` / `pane_id` / `checkout_path`, and a `provenance` map naming
the tier each unspecified value came from (`flag` for the ones you passed).
Progress lines go to stderr, one per step, and must not contaminate the JSON.
Tear the created topology down as in Cell 1.

### Cell 6 — a `.herdr-draft.toml` with a forbidden key

**Setup:** a throwaway repo with a `develop` branch and a committed
`.herdr-draft.toml` mixing one allowed key, one forbidden one, and one
allowed key with an unusable value:

```toml
default_base = "develop"
branch_prefix = "a b/"

[agents.extra_args]
claude = ["--dangerously-skip-permissions"]
```

**Steps:** open the form (Route B is easiest), set **project** to that repo,
and read the **project** row's panel.

**Expected:** below the candidate list, one note per rejected key, each with
its reason, truncated to the panel width:

```
  ignoring agents.extra_args: it becomes part of a launched agent's command li…
  ignoring branch_prefix "a b/": contains a space; using your own configured p…
```

The report is on the **project** panel — it is a property of the file, and
the project row is what decides which file applies. Then check the
**worktree** row: it should read `on · <branch> ← develop`, and `⇥` onto it
should show `develop` selected in the base list with `from .herdr-draft.toml`
on its own line above it. The extra args must not appear anywhere, and
`branch_prefix` must have fallen back to *your* configured prefix, not to the
built-in default.

Confirm too that a malformed file (`echo 'nonsense =' > .herdr-draft.toml`)
is ignored without blocking the form.

### Cell 7 — per-project defaults on a second visit

The point of this cell is that `projects.json` (tier 1) beats
`last-used.json` (tier 3), which is invisible unless the two disagree. It
needs two real creates, so run it in the disposable session (Route A).

1. In throwaway repo **X**, create a session with a deliberately unusual
   combination — worktree **off**, placement **tab here**, agent something
   other than your default.
2. In throwaway repo **Y**, create another with worktree **off**, placement
   **new space**. `last-used.json` now says `new-space`.
3. Reopen the form and set **project** to **X**.

**Expected:** the worktree, placement and agent rows come back with step 1's
values, not step 2's. `$HERDR_PLUGIN_STATE_DIR/projects.json` has an entry
per repo, keyed by the **repository root** — so a linked worktree of X and X
itself share one entry, which is worth checking directly if you have one to
hand. No row is marked `from .herdr-draft.toml`: the form attributes only the
repo-config tier, and this one came from your own history.

Then touch a field before switching projects and confirm memory does **not**
overwrite it: per-project defaults re-apply on a project change only for
fields the user has not touched.

## After the matrix

- Confirm no stray panes, workspaces, tabs, or worktree checkouts remain:
  `herdr[S] workspace list` should be back to just the disposable session's
  root workspace.
- Stop and delete the disposable session; confirm it's gone from
  `herdr session list`.
- Confirm the outer host pane is back at a bare shell prompt, then close only
  that pane.
- Delete every throwaway repo directory under `/var/tmp` this run created,
  and any now-empty parent directories left under `~/.herdr/worktrees/`
  (`worktree remove` only deletes the per-branch checkout, not the per-repo
  parent directory).
- Delete the `projects.json` entries the run wrote, or accept them — the file
  is capped at 50 entries and evicts least-recently-seen, so throwaway paths
  age out on their own.
- If you enabled `[experimental] allow_nested` for Route A, decide whether to
  leave it on.
- Record the herdr and clauth versions tested, which cells passed as
  expected, and anything that deviated from "Expected" above (including which
  known-limitation caveats you actually hit) alongside the release.
