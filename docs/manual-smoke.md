# Manual smoke matrix

Not part of CI (v2 spec §14: "manual release smoke stays in
`docs/manual-smoke.md`"). Run it once per release, against the herdr
version(s) you intend to support, before publishing. It exercises the real
popup, the real herdr CLI, and (for Path B) a real clauth profile — nothing
here is mocked.

Fourteen cells and a 10b: the original four (Path A/B × worktree on/off), plus
three that cover what v2 added — the headless `create`, the repo-level
`.herdr-draft.toml`, and per-project memory — plus two that cover what the
placement spec added: a worktree session keeping its own space (placement
spec §14, which reversed the placement honored there before), and the reuse
path. The tenth is the one the 0.9.0 floor exists for, a large multi-line
prompt (#73), and 10b asks the same question from the other side (#108).
Cell 10 spends real model quota on its payload, so it is the one to decide
about deliberately rather than by default. The last two cover how a pinned
account is launched: the account picker with the wrapper launch, and a
`[clauth] launcher` of your own.
The thirteenth asks whether the spawn skill actually fires, and the
fourteenth is the placement that puts a session in the repository's own
space as a tab (#128).

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

### Claude Code's trust prompt is a precondition, not a surprise

On 0.9.0 this shapes half the matrix, so read it once here rather than
rediscovering it per cell.

Claude Code asks "Is this a project you created or one you trust?" the first
time it runs in any directory, and **herdr 0.9.0 detects that screen** and
reports the agent `blocked`. So any cell that expects a run with no pause in
it has to be pointed at a directory Claude Code has already been trusted in.
Otherwise `agent start` refuses (Path A) or the detection wait stops (Path
B), which is correct behaviour and not a defect — see #90 and #94.

**Since #115 the popup does not fail on that; it waits.** The launch step
goes to `…`, the footer says `answer the dialog in the pane`, and the moment
you answer it the pipeline sends the prompt and closes itself — no keypress
in the popup at all. So for a popup cell the trust prompt is now a *pause to
resolve* rather than a stop to confirm, and the pass condition moved with it:
the cell passes when the prompt lands, not when the failure screen appears.
The old screen is still reachable two ways and both are worth knowing: let
`[timeouts] trust_wait_ms` (five minutes) run out, or answer **"No, exit"**.

**Headless `create` still fails immediately** — decision 2 of #115, since a
script has nobody at the keyboard — so every Route A0 cell keeps its old
expected result unchanged.

Two things make this less obvious than it sounds:

- **Trust is per clauth account, not per directory.** `clauth start <profile>`
  runs Claude Code against that profile's own isolated config dir
  (`~/.local/state/claude-account-dirs/<profile>`), which carries its own
  trust list. Pre-trusting by running plain `claude` does nothing for a Path B
  cell. Pre-trust under the profile the cell will actually use:

  ```bash
  herdr[S] pane run <pane> clauth start <profile> --
  # answer the dialog, then /exit
  ```

- **A worktree cell cannot be pre-trusted at all.** The checkout does not
  exist until the create makes it, so there is no directory to trust in
  advance. Cells 1, 3 and 8 will meet the trust prompt **every time**, on
  every machine — through the popup as a pause you answer, headlessly as a
  failure. That is their expected result, not a defect; each says so.

### Somewhere isolated to run

The disposable-session route this document used to prescribe does not work as
written. From inside a herdr pane:

```
$ herdr --session repro-draft-smoke-1
error: nested herdr is disabled by default.
see configuration if you want to enable it.
```

herdr blocks it whenever `HERDR_ENV` marks the shell as herdr-managed
(`herdr:src/main.rs`'s `should_block_nested`). Three ways forward. Route B is
usually the faster one, and it is the only one that gives the form under test
a pane id of its own.

**Route A0 — a headless disposable server, no config change.** The block above
is on the **TUI**. `herdr server` returns from
[`main.rs`](https://github.com/herdrdev/herdr/blob/v0.9.0/src/main.rs#L545-L546)
into `server::headless::run_server()` *before* `exit_if_nested_disabled` is
ever reached, so a headless session starts from inside a herdr pane with
nothing enabled and nothing edited:

```bash
env -u HERDR_SOCKET_PATH -u HERDR_CLIENT_SOCKET_PATH -u HERDR_ENV \
    -u HERDR_WORKSPACE_ID -u HERDR_TAB_ID -u HERDR_PANE_ID \
    HERDR_SESSION=probe-$(date +%s) herdr server &
```

It daemonises and returns 0. Drive it exactly as Route A does, with
`HERDR_SESSION` pinned on every call. For `herdr-draft create`, point
`HERDR_BIN_PATH` at a one-line wrapper carrying that prefix —
`internal/herdrc`'s `CLIRunner` execs `$HERDR_BIN_PATH` directly and there is
no way to inject a flag:

```sh
#!/bin/sh
exec env -u HERDR_SOCKET_PATH -u HERDR_CLIENT_SOCKET_PATH -u HERDR_ENV \
  -u HERDR_WORKSPACE_ID -u HERDR_TAB_ID -u HERDR_PANE_ID \
  HERDR_SESSION=<your-session-name> herdr "$@"
```

Prefer this to Route A. It costs nothing, touches no configuration you would
then have to remember to undo, and it runs the **real form** — `pane run` the
Route B block below into one of its panes, then `pane send-keys` and
`pane read --source visible` to drive and read it.

> **Reading a TUI you are driving: two traps, both of which produce confident
> nonsense.** Both cost a false reading on the 0.9.0 pass.
>
> `pane read` returns whatever is on screen *now*, and the form has not
> necessarily repainted since your keystroke, so a naive send-then-read is
> **one keystroke stale**. Three `Right`s on the placement chips read back as
> `new space → new space → tab here`, which looks exactly like a row reverting
> on its own. Settle ~0.4s before reading, and remember the chip rows **wrap**.
>
> `pane wait-output` is not a "has changed" primitive: it searches the
> selected snapshot **immediately, including existing output**, then polls. So
> waiting for a string already on screen returns at once — waiting for
> `split here` after pressing `→` matches the chip row that always contains
> it, and waiting for `Quick safety check` before launching a *second* agent
> matches the *previous* agent's dialog still in the buffer. Wait on something
> that can only appear after the change (a per-choice hint, say), or `clear`
> the pane first.
>
> Answering a dialog needs the same care for a different reason: Claude Code's
> trust prompt paints before its key handling is wired, so a `Down` sent the
> instant the text appears can be dropped — and since **"No, exit" is the
> preselected row**, the following `Enter` then declines. Read back the `❯`
> marker and confirm it moved before pressing `Enter`. The one thing it cannot do
is the popup: `plugin pane open` spawns the plugin process, but the popup gets
no entry in `pane list` at all, because it is composited by an attached TUI
client and a headless server has none. That is the precise reason Cell 1
needs a human, and it is worth knowing before you go looking for a pane id
that does not exist.

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
plugin five variables. Set them yourself in a scratch pane and the same form
runs against the same real data, in an ordinary pane you can address:

```bash
export HERDR_BIN_PATH="$(command -v herdr)"
export HERDR_PLUGIN_ID="zvibaratz.draft"
export HERDR_PLUGIN_CONFIG_DIR="$(herdr plugin config-dir zvibaratz.draft)"
export HERDR_PLUGIN_STATE_DIR="${XDG_STATE_HOME:-$HOME/.local/state}/herdr/plugins/zvibaratz.draft"
export HERDR_PLUGIN_CONTEXT_JSON="$(printf \
  '{"workspace_id":"%s","workspace_cwd":"%s","tab_id":"%s","focused_pane_id":"%s","focused_pane_cwd":"%s"}' \
  "$HERDR_WORKSPACE_ID" "$PWD" "$HERDR_TAB_ID" "$HERDR_PANE_ID" "$PWD")"
./bin/herdr-draft
```

Or `just smoke <repo>`, which is exactly the block above with the guards a
release pass wants: it refuses outside a herdr pane, refuses without a built
binary, refuses a path that is not a git repo, and `cd`s into the repo before
building the context JSON — that last one matters, because `workspace_cwd`
and `focused_pane_cwd` are what the project row resolves from, so running the
block from *this* repo opens the form on this tree rather than the throwaway.
The repo argument is required rather than defaulted, for the fresh-repo
reason in "Before you start".

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
herdr[S] plugin pane open --plugin zvibaratz.draft --entrypoint open
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

**The popup is a fixed 104×32 cells** (v3 spec §6.1), which after herdr's
own chrome hands the form **101×30**. herdr clamps that down to the
terminal and never up, so an 80×24 terminal gives **77×22** and a 60×20 one
**57×18** — those three are the sizes a user can produce, and the golden
frames pin all three plus one deliberately oversized **150×44** whose only
job is the margins and the footer's full-width reach. If the popup does not
look 104 cells wide, herdr is
still holding the old manifest in memory: `herdr plugin list --plugin zvibaratz.draft
--json` to check, then `herdr plugin disable draft && herdr plugin enable
draft`.

At the shipped 101×30, fully configured (blank panel rows elided):

```

 new session                                                                      herdr-draft · main
 ───────────────────────────────────────────────────────────────────────────────────────────────────
   issue      none
   title      fix login redirect loop
 ▌ prompt     Work on ENG-101: Fix login redirect loop
   project    ~/Projects/herdr-draft
   worktree   on · zvi/fix-login-redirect-loop ← main
   placement  new space
   agent      claude
   account    active · Max 20x · 5h 12% · 7d 40%
 ───────────────────────────────────────────────────────────────────────────────────────────────────
   Work on ENG-101: Fix login redirect loop

 ───────────────────────────────────────────────────────────────────────────────────────────────────
 ⌃J newline · ⇥ move · ⌃R clear                                              ↵ create    esc cancel

```

The blank first and last lines are the card's margins (v3 spec §7.1), and the
rule above the footer is rule 3 closing it. Both are real output, not
formatting here: at 101×30 the frame is
`1 / header / rule / 8 rows / rule / 15 / rule / footer / 1`.

- **Eight rows, one line each**, in this order: `issue`, `title`, `prompt`,
  `project`, `worktree`, `placement`, `agent`, `account`. Branch and base are
  not rows — they are parts of the worktree row's panel. A row never moves;
  if one does, that is a regression, not a rendering quirk.
- `issue` is absent unless Linear is configured, and `account` unless clauth
  is configured with ≥2 profiles. With neither, the stack is six rows.
- **The focused row carries three signals** (v3 spec §5.4): the full-width
  `ActiveRowBG` paint, an accent `▌` in the two-cell gutter, and a bold
  value. All three, on the same row, or it is a regression — v2 shipped the
  paint alone and it was invisible.
- **The panel below the second rule is the only chooser** — candidate lists,
  the prompt editor, verdicts, notes. It belongs to the focused row and to no
  other. It is capped at fifteen rows (`panelCapRows`, v3 spec §7.2) and
  everything past that becomes the margin above and below the card.
- **A focused text input is filled** (v3 spec §8.7): the title, issue filter,
  project filter, branch editor and prompt draw on their own background, one
  step off whatever they sit on. An input that looks exactly like the space
  around it is the defect that fill exists to remove, so check it on a light
  theme too — the fill is derived per theme, not a fixed colour.
- **Create is on the footer**, right-aligned (`↵ create`, `esc cancel`),
  not a row in the stack. It is still the ring's last focus stop: `⇥` past
  `account` lands on it — the button looks the same there (it is filled with
  the accent color in every state, v3 spec §5.5), and what changes is the
  key ladder beside it, to `⇧⇥ back to the form`.
- **The card fills the pane**, one blank column on each side (v3 spec §6.2).
  Rules and footer reaching only two thirds of the way across is the v2
  width cap, and it is gone.
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

1. Open the popup. Confirm focus is on the **title** row (painted, barred and
   bold — v3 spec §5.4; before v3 the paint was 1.07:1 and this check could
   not actually be performed) and
   the footer reads `name it to create · ⇥ for the prompt · ⌃R clear`. Confirm the
   opening stack: `title untitled`, `prompt —`, and — this is the state that
   shipped broken once — `worktree  on · from main`, with no invented branch
   name in it.

   The panel below is **not empty**: it carries the sessions that were open
   when the form opened (v3 spec §9), one row each, with a count on the
   heading. It is informational — no `▸`, no selection, and `↑↓` do nothing
   there. The workspace you opened the popup from is deliberately absent.
   An empty region here means either a herdr holding one workspace (correct)
   or a regression (not).
2. `⇧⇥` to **issue**. Leave it on `none`, or pick a real issue if Linear is
   configured and you want to smoke that path too. A chosen issue seeds the
   title, the branch and the prompt; confirm the `title`, `prompt` and
   `worktree` rows all change to match it.

   With Linear configured this is the panel that exercises three of v3's
   devices at once, and a real queue is the only place they meet: the list
   is a **table** (identifiers in their own column, titles aligned, status
   right-flushed, v3 spec §8.1); the last column of each row is a
   **scrollbar** whenever the list outgrows the window (§8.5) — walk `↑↓`
   and watch the thumb move rather than the track; and the status line
   right-flushes a **count**, `50 issues` at rest and `4/50 issues` once you
   type. Type a few letters and the **matched run is accented** in each row
   (§8.4). It accents the match WINDOW, not the individual characters, so a
   fuzzy query lights up a contiguous stretch — that is correct, not a bug.
3. `⇥` back to **title**. Type a short distinctive title (e.g.
   `smoke a wt`). The panel reads `branch will be <branch_prefix><slug>`,
   and the session list is still under it. Now type the exact label of one
   of the sessions in that list: the row is marked `!` on the keystroke,
   ahead of the `label in use` verdict a debounce later. Backspace one
   character and the mark clears.
4. `⇥` to **prompt**. Optionally type one; `⌃J` for a newline. The row shows
   the first line plus a dim ` +N more`.
5. `⇥` to **project**. Type the throwaway repo path; `⇥` completes. Confirm
   the header's right half becomes `<repo-dir-name> · <branch>`.
6. `⇥` to **worktree**. The panel is the three-part editor —
   `worktree  off · on`, `branch`, `base` — with `↑↓` between parts and `←→`
   on the chips. Confirm **on**, a seeded branch, and a base list carrying
   `HEAD (<branch>)` first. While the base list is on screen the `base` part
   line is empty: the list is what says which base is selected.
7. **placement** is not a stop with the worktree on: the `⇥` from step 6
   lands on **agent**, and the row reads `the worktree's own space`
   (placement spec §14). Clicking it shows `turn the worktree off to choose`
   and the footer `nothing to set here`. To see its chips, turn the worktree
   off in step 6 and back on again. The row keeps whatever chip was chosen
   while the worktree was off.
8. `⇥` to **agent**. Leave the default favorite (`claude` unless your config
   changes it). `←→` walks the favorites, `↑↓` the full kind list.
9. `⇥` to **account**. Walk the list with `↑↓` if you like, but **do not
   press `↵`** — browsing is not pinning (v3 spec §10.3), and leaving the
   pin unset is what makes this Path A even when clauth is configured. The
   `✓` stays on the `active` row throughout; if it follows your cursor, that
   is the defect #29 fixed coming back, and it is not cosmetic — `Pin()`
   feeds `plan.Input.AccountPin` and the session really would launch under
   whatever row you stopped on.

   While you are here, confirm the panel answers the three questions it
   exists for: a gauge and a percentage for BOTH windows, `●` on whichever
   profile clauth reports as live, and a relative reset (`in 2h11m`, or
   `in 4d14h` for a weekly window). A profile at or past 95% reads
   `rate limited`; at 100% only, it is the pre-v3 threshold and a
   regression.
10. `⌃S` to submit (or `⇥` to the create button and `↵`).

**Expected:** the screen replaces the row stack with the progress stack —
same header, same rule, same label column — and the footer becomes a step
counter:

```
✓ worktree   <branch> from <base>
✓ tab        smoke a wt
› claude     smoke-a-wt
  prompt     queued
...
step 3 of 4
```

`✓` done, `›` running, `✗` failed, `…` waiting on you, `!` failed without
stopping the run (only the `tab` row can: a tab that kept herdr's `1`),
blank pending; a step with nothing of its own to say prints the state word
(`queued`, `working…`, `done`). The label
column is the agent *kind*; the agent step's value is the herdr agent name,
a slug of the title. Then the popup closes.

`herdr[S] workspace list` shows a new workspace grouped with the throwaway
repo's origin workspace (both are created by `worktree create` — see
teardown). `herdr[S] pane list` shows the new pane's agent as `claude` (or
whatever kind you picked) once detection completes
(`herdr[S] agent get <pane-id>`).

**Expected on a fresh worktree, and this is the cell's real point (#115):**
a brand-new worktree is a directory Claude Code has never been trusted in,
so it shows its first-run trust dialog. The popup does **not** fail on that.
A row goes to `…` and the footer replaces the step counter with an
instruction:

```
✓ worktree   <branch> from <base>
✓ workspace  smoke a wt
✓ claude     smoke-a-wt
… prompt     waiting on you…
───────────────────────────────────────────────────────────────────────────────
answer the dialog in the pane — your prompt goes out as soon as you do
```

**Which row waits is not fixed, and both are correct.** herdr may report the
agent `blocked` and refuse `agent start`, in which case the **launch** row
waits and the prompt row is still `queued`; or `agent start` may return ok
with the dialog still up, in which case the launch row goes green and the
**prompt** row waits, refused by herdr-draft's own dialog guard. On the
2026-09-09 pass the second happened every time through the popup and the
first only through headless probes, so expect the shape above — but a `…` on
the launch row is the same feature, not a different result.

herdr has already moved you to the agent's pane (the worktree's space is
created with `Focus: true`), so the dialog is in front of you. Answer it (`↓`, `↵` for "Yes, I trust this
folder"). **Press nothing in the popup** — that is the whole of #115.

**Then read the pane, not the report.** `herdr[S] pane read <pane-id>
--source visible` must show your prompt as a **submitted turn above the `❯`
with an empty input buffer**, with the agent's answer following it.
`agent get` cannot answer this in either direction: a short successful turn
is already `idle` by the time you look (Cell 10, learned by running it). The
popup closes itself once the prompt is away.

The last row of the footer hint is prompt-dependent: with no prompt typed it
reads `answer the dialog in the pane — this carries on by itself once you
do`, which is the honest wording for a plan that has nothing to deliver.

**The old failure screen is still reachable, and worth seeing once.** Set
`trust_wait_ms = 0` in `config.toml` (or answer **"No, exit"**, or just walk
away for five minutes) and the run lands where it used to:

```
✗ claude     claude started; answer the dialog in the pane, then keep this ses…
...
───────────────────────────────────────────────────────────────────────────────
  prompt not sent — saved for manual paste:
  <state dir>/unsent-prompt.txt
  remove deletes the worktree, its branch and its workspace
                                                      k keep it    c remove it
```

Press `k`, confirm the agent process is still alive
(`herdr[S] pane process-info --pane <pane-id>` should still show `claude`)
and move on. Confirm the file the screen named actually holds your prompt.
Answering "No, exit" instead gives a different message — *the agent is no
longer running* — and no instruction to answer a dialog that is gone.

> **On the fallback path, answer the gate BEFORE you go and look at the
> pane.** This no longer applies to the ordinary run — since #115 the popup
> waits and closes itself, and there is no gate to answer — but it still
> applies whenever the wait ends in the failure screen. The space the agent
> runs in is created with `Focus: true` (`topologyOp`, build.go) —
> deliberately, since that is where the agent lands — so herdr moves you to
> the new pane while the popup is still up with its keep-or-remove choice
> unanswered. On the 2026-09-09 run that
> produced a genuinely confusing artifact: a `k` pressed while looking at
> the pane went to the **agent**, whose startup banner came out `kclaude:`,
> and the gate was answered only by a second `k` after switching back.
>
> Nothing was harmed and nothing leaked, but two things follow. A stray
> character typed at an agent sitting on a confirmation dialog is input to
> a live selector — the trust prompt in that run showed `❯ Yes, I trust
> this folder` rather than the preselected `No, exit`, which is what `k`
> does to a vim-style two-row list (up, wrapping), so a following Enter
> would have answered the row the stray key chose. And reading that pane's
> scrollback afterwards makes it look exactly like the popup leaked a
> keystroke, which is a false finding this document briefly recorded before
> the person who ran it said what they had actually pressed. A report is
> not evidence; the person at the keyboard is.

Worth exercising deliberately at least once: with a **dirty** checkout,
`remove` must go unavailable rather than silently refusing. The reason
replaces the rationale line and the button loses its key glyph:

```
  remove unavailable  uncommitted changes
                                                        k keep it    remove it
```

And once with a **pristine** one, pressing `c`: the branch goes with the
checkout (#173). `git -C <repo> branch` no longer lists it, and the same
title submitted again passes the title row's duplicate check instead of
being blocked on `branch exists`. The repository's own workspace that
`worktree create` opened alongside stays open; the line never claimed it.

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

9. **account**: `↑↓` to a specific profile, then **`↵` to pin it** — the
   cursor alone does nothing (v3 spec §10.3), which is the whole of #29.
   Confirm the `✓` moves onto that row and stays there when you `↑↓` away
   again. This is what makes it Path B.

   The stack row then reads `<name> · <plan> · 5h N% · 7d N%`. Note what it
   does NOT say: `ok`. v3 spec §10.1 drops the auth status from the resting
   row entirely — "`ok` is the state that needs no words" — so it appears
   only when it is not ok, as `sign in again` in red. A rate-limited
   profile's percentage is in the warning color.
10. Submit.

**Expected:** the agent step reads `claude   under clauth <the profile you
picked>`, followed by a `detection  waiting for the agent` step.

**The detection step is expected to PAUSE here, and answering it is the
pass condition** — a worktree checkout is new, so it cannot have been
pre-trusted (see "Claude Code's trust prompt is a precondition"). Since #115
the row goes to `…` and the footer carries the instruction, exactly as Cell
1 describes; Path B reaches that state through herdrc's own blocked verdict
rather than through `agent start`'s refusal, and the whole point is that the
difference does not reach the screen. **Check that it does not**: this row
must look and read the same as Cell 1's.

Do the three checks below **while the dialog is still up** — the popup is
waiting, so there is no hurry and nothing to answer in it first. Then answer
the dialog in the pane and confirm the prompt lands as a submitted turn
(`herdr[S] pane read <pane-id> --source visible`), which is Cell 1's own
verification.

To see the old failure text instead, set `trust_wait_ms = 0` and re-run; the
step must then say the instruction, not a timeout and not a raw error code:

```
claude started; answer the dialog in the pane, then keep this session
-- it is showing "Quick safety check": ...
```

The three things this cell exists for, all of which hold with the agent
still sitting on its dialog:

- **The agent is alive.** `herdr[S] agent get <pane-id>` reports
  `agent_status: "blocked"`, not a dead pane. Until #94 this cell destroyed
  the agent — the prompt was typed into the dialog and its Enter answered
  "No, exit" — so a pane back at a shell prompt is a regression of that fix
  and the single most important thing on this page to notice.
- **The prompt survived.** On the waiting path it is still queued and goes
  out when you answer. On the fallback path the failure screen names the
  file it was saved to (or `--json` carries `prompt_sent: false` and
  `unsent_prompt`).
- **The launch really went through clauth.** `herdr[S] pane list` shows
  `agent: "claude"` and `tokens.clauth: "<the profile you picked>"`, and
  `herdr[S] pane process-info --pane <pane-id>` shows **both** `clauth`
  (parent) and `claude` (child).

Finally, answer the dialog in the pane (`↓`, `↵`) and confirm the advice the
message gave is true: `agent get` should move to `idle`, the queued prompt
should appear in the pane as a submitted turn, and the session should be
usable. A cell that ends here with a working session that has the prompt in
it has passed.

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
| `herdr-draft create --title x --placement nowhere` | `unknown --placement "nowhere": expected new-space, tab-here, split-here or tab-in`, then the usage block → **exit 2** |
| `herdr-draft create --title x --no-worktree --placement tab-here` | `HERDR_WORKSPACE_ID is not set: --placement tab-here creates the tab in the workspace this pane belongs to` → **exit 2** (the missing variable is named exactly) |
| `herdr-draft create --title x --no-worktree --placement new-space` | `herdr unreachable: herdr workspace list: ...` → **exit 3** |
| `herdr-draft bogus` | the top-level usage block → **exit 2**; `herdr-draft help` prints the same at **exit 0** |

Also confirm, with `HERDR_PLUGIN_CONFIG_DIR` / `HERDR_PLUGIN_STATE_DIR`
unset, that the first stderr line is the warning that `create` is resolving
without your config and remembered defaults. A headless caller that silently
resolved from built-in defaults would be the worst failure mode this command
has.

Then the other half of that guard (#91), which is worth a live check because
it is the one that used to lose data: export the two directories **without**
`HERDR_PLUGIN_ID` and confirm the first stderr line refuses them by name —
`HERDR_PLUGIN_ID is not set, so it is not demonstrably "zvibaratz.draft"'s`
— and that the run leaves *nothing* behind in the directory it was pointed
at. Point it at a scratch directory rather than another plugin's, and check
it is still empty afterwards:

```bash
scratch="$(mktemp -d)"
env -u HERDR_PLUGIN_ID HERDR_PLUGIN_CONFIG_DIR="$scratch" HERDR_PLUGIN_STATE_DIR="$scratch" \
    herdr-draft create --title "id guard" --no-worktree --placement new-space --json
ls -A "$scratch"   # must print nothing
```

Then, **in a disposable session only** (Route A), with a real
`HERDR_BIN_PATH`, run one that lands:

```bash
herdr[S] pane send-text <scratch-pane-id> \
  'herdr-draft create --title "smoke headless" --worktree --json'
```

Expect exit 0, one JSON object on stdout with `"ok": true`, the created
`workspace_id` / `tab_id` / `pane_id` / `checkout_path`, and a `provenance`
map naming the tier each unspecified value came from (`flag` for the ones
you passed). Progress lines go to stderr, one per step, and must not
contaminate the JSON.

The three ids name **where the agent is**, and `space_workspace_id` /
`space_tab_id` / `space_pane_id` beside them name the space `--on-failure
clean` acts on (#99). This cell runs `--worktree`, where the two are the
same triple twice over — Cell 9's reuse path is the only one that separates
them since placement spec §14, and the one to read this against.
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

## What the placement spec added

### Cell 8 — worktree on: the session keeps its own space

**Why this cell exists.** Before placement spec §14, a worktree combined
with a placement put the agent's tab outside the worktree's space and left
that space holding an idle shell. Once #128 made `tab in <repo>` the
default, that happened to every worktree session after the first in a
repository. Measured on herdr 0.9.0 it also set up herdr's own `worktree
open` to take over the repository's space ([herdrdev/herdr#4293](https://github.com/herdrdev/herdr/issues/4293)).
This cell checks that the default path no longer does any of it.

**Setup:** fresh throwaway repo with no workspace open on it, invoked from a
pane in a different workspace.

**Steps:**

1. Create once through the form, worktree on (the default), everything else
   left alone. herdr opens **two** workspaces on this first worktree create
   in the repository: the repository's own (an idle shell,
   `ensure_source_parent_membership`'s side effect — design doc §2.7;
   `plan.Clean` deliberately leaves it) and the session's space, nested
   under it.
2. Open the form again on the same repository. The `placement` row reads
   `the worktree's own space` before you touch anything. `⇥` goes from
   `worktree` straight to `agent`. Clicking the row shows `turn the worktree
   off to choose` in the panel and `nothing to set here` in the footer.
3. Turn the worktree off. The row now reads `tab in <repo>`: #128's
   default, since step 1 made the repository's space one herdr recognises.
   Turn it back on, type a title and submit.

**Expected:** exactly **one** new workspace, nested under the repository's,
with the agent's pane in it. The progress stack has no `creating tab` step,
and neither the repository's space nor the one you invoked from gains a tab.

**Headless** (plugin env exported as Cell 5 does):

```bash
herdr-draft create --project <repo> --title "cell 8b" --worktree --json
herdr-draft create --project <repo> --title "cell 8c" --worktree --placement tab-here
```

The first reports `"placement": "new-space"`, `provenance.placement` as
`worktree`, and `workspace_id` equal to `space_workspace_id`. The second
exits 2 naming `--no-worktree`, and creates nothing.

**Count panes with other plugins in mind.** Plugin install/link state is
global, so a Route A0 session inherits every plugin installed for your normal
one — and three of them subscribe to `worktree.created`. One,
`persiyanov.reviewr`, answers it by *opening a pane*
(`[[events]] on = "worktree.created"`, `command = ["bash", "herdr/pane.sh",
"auto-open"]`), so the worktree's own workspace can hold reviewr's pane
beside the agent's. Confirmed independent of herdr-draft: a bare `herdr
worktree create` reproduces it, and the plan only ever issues the calls its
progress stack shows. Check `herdr[S] plugin list` before treating a
surplus pane or container as a finding, and count what changed rather than
the absolute total.

Run this cell **headlessly** (Route A0) and expect step 2 (starting the
agent) to fail with `agent_not_ready` **every time you run this cell**, on
every machine — not just the first. #115 changed the popup and deliberately
left `create` alone (its decision 2), so this cell's expected result is
unchanged; the popup's version of the same moment is Cell 1's pause. If you
walk this cell through the popup instead, expect Cell 1's waiting row here
too, and check that the agent is in the worktree's own space before you
answer the dialog. The
worktree checkout is created by the run itself, so it is always a path
Claude Code has never seen and there is nothing to pre-trust in advance
(see "Claude Code's trust prompt is a precondition"). Its first-run trust
prompt blocks it, and herdr's own `agent start` refuses outright rather
than herdr-draft sending a prompt into it — the same class of blocking
dialog `internal/plan/dialog.go` exists to catch, just refused one layer
higher up here. That failure does not invalidate the cell: confirm
instead that the one new workspace above exists and that the agent's pane
is inside it, on the worktree checkout.

**Read the failure message itself — it is part of what this cell
checks** (#90). herdr's own text is "blocked during startup", which
describes a healthy session as a broken one; herdr-draft is supposed to
read the pane and replace it. Expect stderr to carry, in this order, the
instruction, the dialog it matched, and herdr's own error:

```
answer the dialog in the pane, then keep this session -- it is showing
"Quick safety check": ... {"error":{"code":"agent_not_ready", ...
```

A bare `agent_not_ready` with no instruction means the pane read did not
happen or did not match, and is a finding. In the popup (Route A) the
same message appears on the failed step row, truncated to the column —
check the *instruction* survives there, since that is the half the user
acts on. Answering the prompt in the pane and pressing `k` should leave
a working session; a prompt composed in the form should be named on the
failure screen as saved for manual paste, not lost.

### Cell 9 — the reuse path

**Setup:** a throwaway repo with a session already created once via Cell
1 or Cell 8, so a worktree checkout for some branch already exists AND
a pane is still sitting in it (do not close the earlier session's
workspace).

**Steps:** create a SECOND session against the SAME title/branch as the
first — same throwaway repo, same typed title, worktree on, any
placement.

**Expected:** the second create must NOT land the agent in the first
session's own pane. Confirm a NEW pane was created for the second
agent — if the second agent appears to be running in the exact pane the
FIRST session's shell was sitting in, that is the reuse defect placement
spec §5.2 exists to prevent, and it is a real regression, not a rendering
quirk. If the submit later fails and offers keep/clean, confirm "clean"
is DISABLED with a reason naming the reused workspace, if (and only if)
this scenario actually triggered reuse -- check `herdr[S] workspace list`
first to confirm whether it did, since reuse depends on herdr's own
`open_workspace_idx_for_checkout` match rules (design doc §2.1), which
this manual step cannot force with certainty every time.

**This cell is not runnable as written, and here is the recipe that does
work.** "Same typed title, same branch" is refused twice over:

- Through the form, the duplicate check stops the submit. Retyping Cell 8's
  title puts `branch & label in use` in the title panel, and a duplicate
  verdict is the one thing that can block a create (`internal/app/async.go`'s
  `titleNote`). Since reuse *requires* an existing checkout, and an existing
  checkout is exactly `branch exists`, no same-branch create can be submitted
  from the form at all.
- Headlessly, `herdr worktree create` fails before herdr ever considers
  reuse: `fatal: '<checkout>' already exists`, surfaced as
  `worktree_create_failed`.

Reuse needs a **stale workspace**: one herdr still holds open for a checkout
that is no longer on disk. That is reachable, and it is the shape §5.2 was
written for:

```bash
# after Cell 8, with its worktree workspace still open
rm -rf /home/zvi/.herdr/worktrees/<repo>/<branch-slug>
git -C <repo> worktree prune
# then create the SAME branch again
herdr-draft create --project <repo> --title "<a different title>" \
    --branch <same branch> --worktree --json
```

No `--placement` is needed. Since placement spec §14 a worktree session has
no placement op, so the worktree op is always the agent's pane and the
correction fires whenever herdr reuses a workspace.

**Passed on 0.9.0.** herdr reused the open workspace, and the correction
claimed a fresh **tab** in it — the agent landed in a new pane, and the first
session's two shells were untouched:

```
w3:p1  w3:t1  unknown  …/zvi-smoke-b-wt (deleted)   <- first session, untouched
w3:p2  w3:t1  unknown  …/zvi-smoke-b-wt (deleted)
w3:p3  w3:t2  blocked  …/zvi-smoke-b-wt             <- the agent, in the claimed tab
```

The `--json` report was wrong about where the agent is (`pane_id` named
`w3:p1`, the first session's stale shell): **#99**, since fixed. A re-run
should now report `pane_id` = the claimed tab's own pane and carry the
reused workspace's original ids under `space_*`.

## What the 0.9.0 floor added

### Cell 10 — a large multi-line prompt

**Why this cell exists.** Every other cell in this matrix sends a short
prompt, and that is exactly why #73 was never caught by one. On herdr
0.8.2 a ~5 KB multi-line payload was **typed but not submitted**: all four
steps reported ok, `--json` carried `"prompt_sent": true, "ok": true`, and
the whole text sat in Claude Code's input buffer below the `❯` with
`agent_status: "idle"`. A single `pane send-keys <pane> enter` submitted it
and the agent went to `working` at once. Nothing was written to
`unsent-prompt.txt`, because as far as herdr-draft knew nothing had failed.

herdr 0.9.0 claims to fix precisely this (#3506/#3685: `agent prompt` sends
the prompt and Enter *before* reporting success, and `--wait` against a
non-working agent now requires observed working or blocked activity). This
cell is how that claim gets checked, and how a regression in it gets caught
next time.

**This cell costs real model quota** — a ~5 KB prompt is delivered to a real
agent on a real account. That is the one thing that cannot be faked here:
the defect is in the delivery of a payload of that size, so a short prompt
proves nothing. Budget for it, or skip the cell and say you skipped it.

**Setup:** Route A0's disposable session, a payload of roughly the right
size, and — because this cell needs the agent to actually *run* — a project
directory Claude Code has **already been trusted in**, under the account the
cell will use ("Claude Code's trust prompt is a precondition"). A fresh
throwaway repo is the wrong choice here for once: it stops at the trust
dialog and the prompt never gets sent, which tests nothing. This repo's own
checkout does fine. The last line is what keeps the *agent's* own turn
cheap while the payload stays honest — what is under test is the paste, not
the answer:

```bash
{ sed -n '1,120p' docs/specs/2026-08-31-herdr-draft-design.md
  printf '\n\nDo not act on any of the above. Reply with the single word OK and stop.\n'
} > /tmp/smoke-prompt.txt
wc -c /tmp/smoke-prompt.txt    # aim for ~5000; adjust the line count to suit
```

Use a payload with **blank lines and indented lines** in it, which the
excerpt above has. A single 5 KB paragraph is a weaker test: a newline is
what Claude Code's input treats as a submit, so the multi-line shape is the
part that can go wrong.

**Steps, headless** — the reproduction #73 was filed from:

```bash
herdr[S] pane send-text <scratch-pane-id> \
  'herdr-draft create --title "smoke big prompt" --no-worktree \
     --placement new-space --prompt - --json < /tmp/smoke-prompt.txt'
```

**Steps, through the form** — the same payload one layer up, since the popup
composes the prompt rather than reading a file. Paste it into the prompt row
(the row accepts `⌃J` for a newline; a paste carries its own), then submit.
Confirm the row's `+N more` count matches the payload's line count before
submitting, so a truncated paste is not mistaken for a delivery failure.

**Expected:** exit 0, `"ok": true`, `"prompt_sent": true` — and then, because
that is exactly what was untrue on 0.8.2, **verify it in the pane rather than
believing the report**:

```bash
PANE=$(... | jq -r .pane_id)          # the AGENT's pane (#99) -- not space_pane_id
herdr[S] pane read "$PANE" --source visible | tail -30
```

The pass condition is the payload appearing **above** the `❯` as a submitted
turn, with the agent's own answer after it and the input buffer below the `❯`
**empty**. The failure signature is the exact inverse, and reads like a pass
from the outside: the full text sitting **below** the `❯`, and a report that
says `prompt_sent: true`. Recover with
`herdr[S] pane send-keys "$PANE" enter`, then file it against #73.

**`agent get` cannot answer this, in either direction.** An agent typed into
but never submitted is genuinely `idle`; so is one that submitted, answered
and finished — which a one-word answer does in a few seconds, so a pass is
usually `idle` by the time you look. `working` is a *transient*, not the
pass condition. Only the pane distinguishes the two, which is the whole
reason this cell reads it.

**If it does reproduce on 0.9.0**, #73 item 2 is the fix: `plan.Execute`
already reads the pane *before* sending (`internal/plan/dialog.go`) and can
read after as well, falling back to the `unsent-prompt.txt` path rather than
reporting success.

**A wording trap, either way** (#43): herdr's `agent_prompt_stalled` means
the text **was typed** and the submission did not complete. "Prompt not sent
— saved for manual paste" is subtly wrong for that code, and a user who
believes it pastes the prompt a second time.

**Retired as of #132** — kept here because it is what the trap looked like,
and because the reasoning is what to re-check if the wording ever drifts
back. A stall is now sent once more after a short pause, on both the popup
and headless `create`; one that survives that retry is reported as
**unconfirmed**, not unsent, so neither path shows that line any more. What
you should see instead is "delivery unconfirmed — read the pane" in the
popup and `prompt_status: "unconfirmed"` from `create`. A run that still
says "not sent" for `agent_prompt_stalled` is a regression, not a trap.

### Cell 10b — the same question from the other side (#108)

#73 is a report of success for a prompt that never landed. **#108 is the
inverse**, found live on 0.9.0 with a 3005-byte prompt into a `split-here`
pane: `agent prompt --wait` returned the `timeout` code, `create` reported
exit 1 and `prompt_sent: false` with the whole prompt back under
`unsent_prompt` — and the agent was already several tool calls into it.

`--wait` completes only on *observed* working-or-blocked activity (herdr's
own note for #3506/#3685), so an agent slow to paint its status transition
exceeds the prompt timeout while being perfectly healthy. The wait timing
out says nothing about delivery.

**How to provoke it.** Raise the odds rather than engineer it: a large
prompt into a slow-to-paint agent, with the prompt timeout cut short.

```bash
# from the scratch pane, with [timeouts] prompt_ms lowered in config.toml
herdr-draft create --title "smoke unconfirmed" --no-worktree   --placement split-here --prompt - --json < /tmp/smoke-prompt.txt
```

**Expected on 0.1.0 and later:** exit **1** (something did go wrong — the
confirmation), and then

- `"prompt_status": "unconfirmed"`,
- **no** `prompt_sent` key at all — neither value is knowable,
- the text under `"unconfirmed_prompt"`, never `"unsent_prompt"`,
- and if `--on-failure clean` was passed: `"cleaned"` absent, with
  `"clean_refused"` explaining that an agent may be working in there.

The stderr line must not say "the prompt was not sent", and must tell you
to read the pane.

**Then read the pane**, exactly as Cell 10 does — `pane read --source
visible` — and confirm which of the two it actually was. That is the point
of the cell: the report deliberately does not decide, so this step is the
only thing that does.

**The failure signature is the old behaviour**: exit 1 with
`prompt_sent: false` and `unsent_prompt` carrying text the pane shows
already submitted. Doing the documented thing with that field sends the
agent its instructions twice, and `--on-failure clean` would have removed
the session mid-turn.

**Unrun.** The fix is unit- and mutation-tested; provoking a real
`timeout` from herdr needs an agent that genuinely paints slowly, so this
cell is written from the code and the one live capture in #108 — a
hypothesis until someone runs it.

**Passed on 0.9.0**, headlessly, with 6311 bytes over 120 lines and 22 blank
lines — a superset of the payload that failed on 0.8.2. The whole thing
arrived above the `❯` as one submitted turn and the agent answered it; the
input buffer was empty. #73's silent failure does not survive the floor bump.
The form half of the cell is still unrun.

## What the account picker added

### Cell 11 — the account picker protocol, and the wrapper launch

**Needs a picker.** Any executable satisfying the [account picker
protocol](../README.md#account-picker-protocol) will do; the cell below was run
against one that exists on the author's machine, named through
`[clauth] picker` in the plugin's own `config.toml`. Without one, herdr-draft
has no `auto` row and this cell has nothing to run.

`[clauth] launch = "wrapper"` is the other half, and it is **only meaningful
where the pane's login shell defines `claude` as a FUNCTION**. On a machine
where `claude` is the plain binary the mode still isolates the credential and
silently does nothing else, so a pass there proves the launch and not the
wrapper. Say which you had.

1. **The protocol, against the real picker.** Not a stub:

   ```bash
   HERDR_DRAFT_PICKER=$(command -v <your-picker>) go test ./internal/picker/ -run Real -v
   ```

   It probes, then picks with `--dry-run`, and asserts `config_dir` came back
   null. Skipped, not failed, when the variable is unset — no particular
   picker may become a prerequisite for a green tree.

2. **The same invocation `create` makes**, without the ledger write:

   ```bash
   <your-picker> --dir "$PWD" --json --strict --dry-run | jq -c '{profile,config_dir,class,usage}'
   ```

3. **The form's OPENING state**, via Route B. This is the step worth the
   trouble: the preview fires on a project change, and the form's first
   project has not changed from anything, so the opening state is its own
   case. The `account` row must already name a profile.

4. **The panel**, six `tab`s from the title row. The `✓` belongs on the `auto`
   row, not on the profile it named — they are two different rows, and that
   distinction is what the whole `auto` design rests on.

5. **A real launch**, and read all three facts off the pane rather than
   inferring any of them:

   ```bash
   herdr pane list      # tokens.acct on the new pane
   herdr pane process-info --pane <id>
   ```

**Passed on 0.9.0, 2026-09-10**, on a machine whose shell does define
`claude` as a function.

The profile and tenant this run picked are written below as `<profile>` and
`<tenant>`, the same placeholders this cell already uses for `<your-picker>`.
This is a public repository and the evidence here is its *shape* — a probe that
passed, a `config_dir` that was null under `--dry-run`, a row that rendered,
a `tokens.acct` that was set — none of which one machine's account names carry.
Redacting them is deliberate and is the same reasoning that genericised the Go
fixtures; the date, the herdr version and the numbers are untouched.

- Step 1: probe ok; picked `<profile>` (tenant `<tenant>`, tier `Max`),
  `config_dir` null under `--dry-run`.
- Step 2: exit 0, `{"profile":"<profile>","tenant":"<tenant>",`
  `"config_dir":null,"class":"eligible","usage":{"five_hour":8,"weekly":12,`
  `"cache_age_s":66}}`.
- Step 3: the row opened reading
  `account  auto → <profile> · Max · 5h 9% · 7d 12%`. **It did not on the
  first attempt** — the opening state showed a bare `auto`, which is the
  defect this step exists to catch and was fixed before the run was recorded.
- Step 4: `✓ auto  picker  [gauges]  → <profile>` first, `active` second,
  then five profiles; legend `auto  let the account picker choose, per
  project`. Four of the five profiles were at `7d 100%` and marked
  `rate limited`; the picker chose the one that was not.
- Step 5: `tokens.acct = "<profile>"` on the launched pane, and
  `process-info` reported
  `claude --settings {"teammateMode":"tmux"} --model claude-opus-5[1m] --effort xhigh`.
  The `--settings` argument is the whole point of wrapper mode: `clauth start`
  has never produced it, because only the shell function can.

**Read this before repeating step 5.** The launch that produced the evidence
above was **accidental**: a `herdr pane run` aimed at a pane that still had
the form open typed the command into the title field and submitted it, which
created a worktree and a session named after a temp-file path. Everything the
step needed was in that session, and it was torn down immediately
(`worktree remove --workspace`, then `git branch -D`), but it is worth knowing
that a `pane run` into a pane running a TUI is a submit, not a shell command.
Send `esc` and confirm the form is gone before running anything into that
pane.

## What the configurable launcher added

### Cell 12 — a `[clauth] launcher` of your own

**Why this cell exists.** `[clauth] launcher` (#121) is the command line
herdr-draft *types* into the agent's pane on every pinned launch, and typing is
where #72 hid: a launch step reporting ok over a shell that had refused the
command. Cells 3 and 4 exercise the default template. Nothing else here runs
one of your own.

**Step 1 costs nothing. Step 2 costs one real launch** on the profile you pin;
it sends no prompt, so the agent never takes a turn.

**Setup:** Route A0's disposable session, and two scratch config directories —
never your real plugin config. The first holds the launcher under test, with
**no `launch` key**, because `launch = "wrapper"` ignores `launcher` entirely:

```toml
[clauth]
launcher = ["env", "HERDR_DRAFT_SMOKE=launcher", "clauth", "start", "{account}", "--"]

[agents.extra_args]
claude = ["--model", "<a model id your account can use, with [brackets] in it>"]
```

The template is chosen so that it needs no wrapper of your own: `env` exists
everywhere. It still puts words before the account, the account mid-argv, and
a `NAME=value` element that has to reach `env` as a plain argument once the
pane's shell has parsed the typed line. The bracketed model id is #72's own
shape: typed unquoted, the shell globs it and the launch dies.

The second directory holds a launcher that is refused twice over:

```toml
[clauth]
launch = "wrapper"
launcher = ["claude-as"]
```

1. **Free: the refusals, with nothing launched.** `create` reports its config
   before it looks for herdr, so with `HERDR_BIN_PATH` pointing nowhere the
   run stops at exit 3 having said everything this step needs:

   ```bash
   state="$(mktemp -d)"
   env -u HERDR_WORKSPACE_ID -u HERDR_TAB_ID -u HERDR_PANE_ID \
       HERDR_BIN_PATH=/nonexistent/herdr HERDR_PLUGIN_ID=zvibaratz.draft \
       HERDR_PLUGIN_CONFIG_DIR=<refused-config-dir> HERDR_PLUGIN_STATE_DIR="$state" \
       ./bin/herdr-draft create --title "launcher smoke" --no-worktree \
         --placement new-space --account <profile>
   ls -A "$state"   # must print nothing
   ```

   **Expected**, both facts about the one launcher, the ignored one first:

   ```
   herdr-draft create: ignoring [clauth] launcher: [clauth] launch = "wrapper" launches claude under an isolated credential directory rather than through an argv template; using [clauth start {account} --] instead of [claude-as]
   herdr-draft create: ignoring [clauth] launcher: it contains {account} 0 times, want exactly 1; using [clauth start {account} --] instead of [claude-as]
   herdr-draft create: herdr unreachable: herdr workspace list: fork/exec /nonexistent/herdr: no such file or directory
   ```

   The same command against the first directory must print no `ignoring`
   line at all. And if clauth has at least two profiles, opening the form on
   the second directory via Route B puts the same two lines on the `account`
   row's panel.

2. **The real launch**, from a pane of the disposable session, into a
   directory already trusted under `<profile>` ("Claude Code's trust prompt
   is a precondition"; this repo's checkout does fine):

   ```bash
   herdr[S] pane send-text <scratch-pane-id> \
     'HERDR_PLUGIN_ID=zvibaratz.draft HERDR_PLUGIN_CONFIG_DIR=<launcher-config-dir> \
      HERDR_PLUGIN_STATE_DIR="$(mktemp -d)" herdr-draft create --title "launcher smoke" \
      --no-worktree --placement new-space --account <profile> --json'
   ```

   **Expected:** exit 0 and `"ok": true`, with stderr's launch rows reading
   `typing the clauth launch` and then `waiting for agent detection`. Neither
   row is the evidence — the first covers the typing and not the command,
   which is the whole of #72 — so read the facts off the agent's pane
   (`.pane_id` from the JSON, not `space_pane_id`):

   - **It ran, under clauth.** `herdr[S] pane list` shows `agent: "claude"`
     and `tokens.clauth: "<profile>"`, and
     `herdr[S] pane process-info --pane <pane-id>` shows `clauth` (parent)
     and `claude` (child). A pane back at a shell prompt is the #72 failure.
   - **The extra args followed the whole template, and the brackets arrived
     as data.** The same `process-info` shows `claude` carrying your model id
     with its brackets intact — not a glob error, and not with quote
     characters left in it.
   - **The words before the account ran.** `HERDR_DRAFT_SMOKE=launcher` is in
     the agent's environment, which a template dropped or mangled on the way
     into the pane could not have put there:

     ```bash
     # Linux
     for f in $(command grep -l HERDR_DRAFT_SMOKE=launcher /proc/[0-9]*/environ 2>/dev/null); do
       tr '\0' ' ' < "${f%environ}cmdline"; echo; done
     # macOS
     ps eww -Ao pid,command | command grep '[H]ERDR_DRAFT_SMOKE=launcher'
     ```

     Expect `clauth` and `claude` among the matches, plus anything `claude`
     has started since, which inherits it. `command grep` is not a flourish:
     a shell function named `grep` made this print nothing on the first run,
     with the variable present in both processes.
   - **What was typed.** Not on screen: by the time `create` returns, Claude
     Code has started and replaced the shell's echo, so `pane read` shows its
     banner instead. A shell that records history as each command runs has
     the line, and it should read `env HERDR_DRAFT_SMOKE=launcher clauth start
     <profile> -- --model '<model id>' …` — the account filled in, the extra
     args after the whole template, and only the bracketed element quoted.

**Teardown:** close the new workspace (`herdr[S] workspace close`), as Cell
2; nothing else was created. Delete both scratch directories.

**Passed on herdr 0.9.0 and clauth 0.15.1, 2026-09-15**, with the binary
built at `4d5fdeb`, through Route A0 — `create` run from the host shell with
`HERDR_BIN_PATH` at the Route A0 wrapper, rather than typed into one of the
session's panes. The profile is written as `<profile>`, for the reason Cell
11 gives.

- Step 1: the three expected lines exactly, exit 3, and an empty state
  directory; the first directory printed no `ignoring` line. The Route B
  half — the same lines on the `account` panel — was not run live;
  `assembled-config-warnings-clauth-101x30` pins it.
- Step 2: exit 0 and `"ok": true`, with stderr reading
  `[1/3] creating workspace ... ok`, `[2/3] typing the clauth launch ... ok`,
  `[3/3] waiting for agent detection ... ok`. `pane list` showed
  `agent: "claude"`, `agent_status: "idle"` and `tokens.clauth: "<profile>"`.
  `process-info` showed `clauth start <profile> -- --model claude-opus-5[1m]
  --effort xhigh` with `claude --model claude-opus-5[1m] --effort xhigh`
  beneath it: the brackets intact, the extra args after the template.
  `HERDR_DRAFT_SMOKE` was in the environment of `clauth`, whose parent was the
  pane's `zsh` (so `env` ran and exec'd into it), and of `claude`.
- The run corrected two things in this cell before it was recorded: the
  Linux check now says `command grep`, and the typed line is no longer
  promised on screen. Shell history had it as
  `env HERDR_DRAFT_SMOKE=launcher clauth start <profile> -- --model
  'claude-opus-5[1m]' --effort xhigh`.

## What the spawn skill added

### Cell 13 — does `/spawn` actually fire?

**Why this cell exists.** Everything else about the skill is held to the
binary by tests: the flags it names exist, the exit codes match, the
frontmatter parses. Not one of them can tell you whether Claude Code
*picks the skill up* when an agent forms the intent the description was
written for. That is a property of Claude Code's skill matching and of one
paragraph of English, and nothing in this repository can assert it.

**Step 1 — install it.** Into your real personal skill directory; this is
the one cell that deliberately touches it.

```bash
root=$(herdr plugin list --json --plugin zvibaratz.draft | jq -r '.result.plugins[0].plugin_root // empty')
draft="$root/bin/herdr-draft"
echo "$draft"                              # a GitHub install or a linked checkout, both answer here
"$draft" skill > /tmp/spawn-SKILL.md       # inspect it first
head -5 /tmp/spawn-SKILL.md                # must start with the `---` fence
mkdir -p ~/.claude/skills/spawn
cp /tmp/spawn-SKILL.md ~/.claude/skills/spawn/SKILL.md
```

**The lookup is half the cell.** `herdr-draft` is not on `PATH`, so
locating it is the first thing any user has to do, and it is the step the
README can get wrong without any test noticing. Record what it printed.
Until #167 this cell used a `find` over `~/.config/herdr/plugins`, on the
premise that herdr reported no install path; `herdr plugin list --json`
does, as `plugin_root`.

Check the path it baked in is the one you expect — `grep -m1 create
~/.claude/skills/spawn/SKILL.md` — and that the version in its last
section matches what `"$draft" version` prints.

**Step 2 — the trigger.** Open a *fresh* Claude Code session in a herdr
pane (a skill added mid-session is not seen). Ask for a handoff in words
that never say "herdr-draft", "spawn", "plugin" or "skill". Something
like:

> I'm done with the auth refactor here. Start another agent on the
> migration piece so it can run while I review this.

**Pass:** the session announces it is using the `spawn` skill, and what it
proposes is a `herdr-draft create` command — not a handoff document, and
not a hand-rolled `herdr agent start`.

**Also worth recording, because it is the likeliest way this goes wrong:**
ask something that should *not* fire it — "spawn a subagent to read these
forty files and summarise them" — and confirm it does not. An over-trigger
onto the Agent tool's in-process subagents is the misfire that would
discredit the skill fastest, and section 1 of the document exists to
prevent it.

**Step 3 — the environment half.** If it did fire, watch whether the three
`HERDR_PLUGIN_*` exports and the `create` went out in **one** tool call.
Exports in a separate call do not count and are the failure to look for:
Claude Code's Bash tool keeps the working directory between calls but not
environment variables, so an agent that exports in one call and creates in
the next resolves from built-in defaults while looking like it did
everything right. The tell is on `create`'s first stderr line. That window
is the whole reason the skill's second section says "in the same command".

**Step 4 — the session's options (#209).** In the same kind of fresh
session, ask for two handoffs. First, one whose shape is obvious: a
one-line wording fix with the change spelled out. Then one whose first
decisions are yours, such as "the issue lists decisions for me; have
someone plan it". Neither needs to be created. The confirmation is what is
under test, so answer it with "Other: don't create" once you have read it.

**Pass:**

- Before it asks, the session runs the command with `--dry-run --json`
  twice, with the exports in the same call each time: first with none of
  the three option flags, which reads the user's defaults, then with its
  choices. A single run that already carries the flags is a fail. It
  cannot know what the choices replace (the skill's section 7).
- Every command it offers carries `--model`, `--effort` and
  `--permission-mode`.
- Each option's preview names the account, the branch and base (or the
  placement), and whether the session reaps itself. Its description gives
  a reason for the options, and the default each one replaces.
- With an argument besides the three option flags in `[agents.extra_args]`
  (#219), every preview lists it word for word. If it changes how the
  agent asks for permission, the question itself says so. The owner's
  `extra_args` passes only `--model` and `--effort`, so this needs a
  scratch config to see.
- With a window of the session's account at or above 95% (#215), the
  question names the window, how full it is and when it resets, and one
  option is a cheaper configuration. Without one, nothing about usage is
  asked.
- The wording fix gets a small configuration: `sonnet` at `low` or
  `medium`.
- The second gets `--permission-mode plan`, and `--no-reap` when
  `[reaper] mark_ready` is on.
- No mode offered is more permissive than the session's own.

**Run on 2026-09-19** (Recorded runs), twice. The first pass covered step
2's positive half and steps 3 and 4. The second, on the numbered section 7
(#226), repeated step 4's miss case and ran step 2's negative half. Step 1
was the owner's own install.

**`herdr agent prompt` sends its text as a bracketed paste.** One session
read that paste as text the user had pasted rather than asked for, and
asked whether to act on it. Answer the question if you see it. A prompt
the session only asks about has not tested the trigger.

The confirmation cannot be read in full from a short pane, and asking
the session to write out what it did can trip the model's safeguards. The
session's own transcript is the reliable record. It holds every Bash call
and the exact `AskUserQuestion` input. It lives at
`<config dir>/projects/<cwd>/<session>.jsonl`, where:

- `<config dir>` is the *watched* session's `CLAUDE_CONFIG_DIR`, or
  `~/.claude` when it has none. Under clauth that is per account, so it is
  not necessarily your own shell's.
- `<cwd>` is its working directory with every character but letters and
  digits turned into `-`, so `/home/me/.herdr/x` becomes
  `-home-me--herdr-x`.
- `<session>` is `herdr agent get <pane>`'s `agent_session.value`, which is
  there only when herdr's claude hook is installed.

## What tab-in added

### Cell 14 — a tab in the repository's own space (#128)

**Why this cell exists.** Everything the resolver decides is pinned by
tests against a fake `workspace list`: which workspace matches, which
tiers yield, what the JSON says. None of them can tell you whether the
tab herdr actually creates lands in the space the form named, whether the
sidebar shows no new top-level space, or what the form reads at the
keyboard. Those three are this cell.

**Setup:** a throwaway repo, and a workspace already open on its PRIMARY
checkout that herdr counts as the repository's. A plain `workspace create
--cwd` is **not** enough on its own. herdr's `workspace list` carries a
`worktree` object only for a workspace with worktree membership, and
`plan.FindSpace` matches on that, so `tab in` is neither offered nor
accepted for it. Mark it with `worktree open` on the primary checkout, which
answers `already_open: true` and keeps the label:

```bash
herdr[S] workspace create --cwd /var/tmp/hd-smoke-14 --label smoke14 --no-focus
herdr[S] worktree open --cwd /var/tmp/hd-smoke-14 --path /var/tmp/hd-smoke-14 --no-focus
```

A first `worktree create` in the repository marks one too. Note its id. Then open the form from a pane in a DIFFERENT
workspace (the disposable session's root workspace is fine), so that
"here" and "the repo's space" are not the same place.

**Steps, form:** `project` → the throwaway repo; worktree **off**. Read the
`placement` row before touching it.

**Expected:** the row reads `tab in smoke14` without any input from you,
and its panel says the space already holds the repository. `←`/`→` still
reaches `new space`, `tab here` and `split here`; the chip count is four.
Submit. `herdr[S] workspace list --json` must show the SAME number of
workspaces as before the submit, `smoke14` with `tab_count` one higher, and
the agent's pane inside it — not a new top-level space, and not a tab in
the pane you submitted from.

**Steps, headless (from any pane, plugin env exported as Cell 5 does):**

```bash
herdr-draft create --project /var/tmp/hd-smoke-14 --title "probe 14" --no-worktree --json
```

**Expected:** `"placement": "tab-in"`, `"provenance": {"placement": "herdr
workspace list", …}`, and `workspace_id` equal to `smoke14`'s id. Then the
two ways round it:

```bash
herdr-draft create --project /var/tmp/hd-smoke-14 --title "probe 14b" --no-worktree --placement new-space --json   # a new space, as asked
herdr-draft create --project /var/tmp/hd-smoke-14 --title "probe 14c" --no-worktree --workspace <root-ws-id> --json  # a tab in a space YOU named
```

The second must put its tab in the workspace you named (`workspace_id`
equals it; `provenance.placement` is `flag`), and it must work with
`HERDR_WORKSPACE_ID`/`HERDR_TAB_ID`/`HERDR_PANE_ID` all unset — `tab-in`
reads none of them, which is the whole of #128.

**Worktree on.** Repeat the form walk with the worktree **on**. The row
reads `the worktree's own space`, not `tab in smoke14`. The submit opens
one new space nested under `smoke14` with the agent in it, and adds no tab
to `smoke14` (placement spec §14). Headlessly, `create --worktree --json`
reports equal agent and `space_*` triples.

**Also record:** close `smoke14`, re-open the form on the same repo, and
turn the worktree off (the worktree-on submit above left it remembered on).
The row must read `new space` and the chip count must be three. The memory
files still remember `tab-in` — a worktree submit keeps the placement it
found (placement spec §14.2) — so this is the resolver dropping it once the
space is gone, rather than leaving the cursor on a chip that is not there.

**Partly run.** The setup's `worktree open` step, the headless `tab-in`
default, and the headless worktree-on result were observed on 2026-09-17
(Recorded runs). The form walk, the two `create` variants, and "Also
record" were not. Do not write a result you did not see.

## What the options row added

### Cell 15 — the agent's model, effort and permission mode

**Why this cell exists.** The tests pin every argv the plan builds for both
launch paths, and every state of the row and its panel. None of them can
tell you whether the claude that starts in the pane is running the model,
effort and mode the row claimed. That is decided by claude, from a
command line the tests only ever compare with an expectation. Nor can they
tell you whether the declaration still matches the claude you have
installed (agent-options spec §9).

**Drift first, before you open anything.** Compare the installed claude
against `internal/agentopts`' declaration:

```bash
claude --version
claude --help | grep -A2 -- '--effort'
claude --help | grep -A3 -- '--permission-mode'
```

**Expected:** `--effort` lists exactly `low, medium, high, xhigh, max`, and
`--permission-mode`'s choices include `manual`, `plan`, `acceptEdits` and
`auto`. A value the declaration offers that claude no longer lists is a
defect to fix before release. A choice claude lists that the declaration
does not is only a gap: it is never offered, so nothing breaks.

**Setup:** a throwaway repo, and a `config.toml` in the redirected config
directory carrying:

```toml
[agents.extra_args]
claude = ["--model", "sonnet"]

[agents.options.claude]
effort = "high"
```

**Steps, form, unpinned (Path A):** `project` → the throwaway repo, worktree
off, and an account row left on `active` (or clauth absent). Read the
`options` row before touching it.

**Expected at rest:** `sonnet · effort high`, with `sonnet` dim, and the
panel's last lines `[agents.extra_args] adds: --model sonnet` and `from
config.toml`. Then walk the panel:

1. `↑`/`↓` move between `model`, `effort` and `mode`, and `←`/`→` move the
   chips of the line under `▸`.
2. On `model`, choose `other`: a `name` line opens under it. Type
   `claude-opus-5[1m]`. Paste `--foo` into it, and nothing lands.
3. `mode` → `plan`. The row now reads `claude-opus-5[1m] · effort high ·
   plan mode`, and the hint on the model line says the model replaces
   extra args' `sonnet`.
4. Move the `agent` row to `codex` and back to `claude`. On codex the row
   reads `none for codex` and `⇥` skips it. Back on claude, your choices are
   all still there.

Submit with a prompt. **Expected:** the progress screen's agent step names
the options. `herdr[S] pane read <agent-pane>` shows claude's banner naming
the 1M-context model at high effort, and claude's footer shows plan mode.
The pane's command must not contain `--model sonnet`: the option replaced
it.

**Steps, form, pinned (Path B):** the same walk with an account pinned on
the `account` row. **Expected:** the same banner and footer. The typed
command in the pane's scrollback reads `clauth start <account> --
--effort high ...`, with the bracketed model id quoted for the shell.

**Steps, headless:**

```bash
herdr-draft create --project /var/tmp/hd-smoke-15 --title "probe 15" --no-worktree \
  --model opus --permission-mode plan --json
herdr-draft create --project /var/tmp/hd-smoke-15 --title "probe 15b" --no-worktree \
  --effort inherit --json
herdr-draft create --project /var/tmp/hd-smoke-15 --title "x" --agent codex --effort high; echo "exit $?"
herdr-draft create --project /var/tmp/hd-smoke-15 --title "x" --permission-mode bypassPermissions; echo "exit $?"
```

**Expected:**
- The first reports `"agent_options": {"model": "opus", "effort": "high",
  "permission_mode": "plan"}` and provenance `option.model: flag`,
  `option.effort: config.toml`.
- The second has no `agent_options` at all, and its pane runs with extra
  args' `--model sonnet` and no `--effort`.
- The last two exit 2 before anything is created. The codex one names the
  option codex does not declare, and the bypass one gives its reason.

**Also record, the model error.** Set `model = "no-such-model"` through the
`other` line and submit with a prompt. Record verbatim what claude shows in
the pane, and what the progress screen and `create --json` reported.
Agent-options spec §10 leaves a wrong model id to claude. If claude shows a
stable blocking screen that the prompt was typed into, that is the evidence
for adding it to `dialog.go`'s signatures; if it shows an ordinary error,
record that nothing is needed. **On claude 2.1.277 it is an ordinary
error** (Recorded runs, 2026-09-18): claude starts, its banner names the
unknown id, and the prompt is delivered and answered in the transcript with
`There's an issue with the selected model (no-such-model)`. Re-check it when
claude's major version moves.

**Run on 2026-09-18** (Recorded runs), in three passes: the form, its panel
and Path B's typed command without an agent; real launches on both paths;
and once through the real popup, with the owner's own config, after the
merge. The headless refusals were covered by tests, not against a live
herdr. Do not write a result you did not see.

**Reading what a launch was given.** claude draws on the terminal's
alternate screen, so the shell line that started it is not in the pane's
buffer once it is up, whatever `pane read --source` asks for. The process
still carries it: `herdr pane process-info --pane <id>` lists the
foreground process's `argv`, which is the one record of which flags
actually reached claude, and so of whether an option replaced an extra
arg or merely followed it.

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
  leave it on. Route A0 needs no such decision, which is most of why it is
  the one to prefer.
- Check for an orphaned plugin process: **`pgrep -x herdr-draft`**. A
  `plugin pane open` in a headless session (Route A0) spawns the binary with
  no client to attach it to, so it outlives the session that started it and
  has to be killed by hand. Use `-x` (match the process *name*), not `-f`:
  `pgrep -f herdr-draft` matches every command line containing the string,
  including the shell running the `pgrep` itself, so it reports phantom
  orphans — it answered 5 on a machine whose real count was 0.
- Record the herdr and clauth versions tested, which cells passed as
  expected, and anything that deviated from "Expected" above (including which
  known-limitation caveats you actually hit) — in **Recorded runs** below, and
  alongside the release.

## Recorded runs

### `/spawn` again: the numbered section 7, and the negative trigger (Cell 13, #226) — 2026-09-19

herdr 0.9.0, Claude Code 2.1.278 on Opus 5 at `high` in auto mode, with
the owner's account and config. herdr-draft `main` at `5d4d1dc`, installed,
with its skill regenerated by the owner. Each case ran in a fresh session
in a sibling pane of this repository's worktree. The evidence is each
session's transcript, not its screen.

| Case | Result |
|---|---|
| The first pass's miss case again: "…get another agent started on that in a fresh session of its own" (a one-line wording fix) | **Passed.** Its own brief could not overwrite the first pass's, which was still in `~/Projects/handoffs/`, so it offered that one and said so. Two dry runs, each in one call with the exports: plain first, then with `--model sonnet --effort medium --permission-mode auto`. The recommended option's description read "This replaces your extra_args default of claude-opus-5[1m] at xhigh", which the first pass got wrong. The third option carried the owner's own id, quoted. No reap flag was passed, which is right with `mark_ready` off. The question was dismissed with Esc, and no session was created. |
| Step 2's negative half: "Spawn a subagent to read the files in internal/form and summarise what each one is for." | **Passed, on the second prompt.** The first was read as pasted text, and the session asked whether it was a request at all. Answered "Yes, that was my own request. Use a subagent, as it says.", its first and only tool call was the Agent tool (`general-purpose`), and it never loaded the skill. It was interrupted about a second after that call, before the subagent had read anything. |

The session that ran this pass then used the skill for real, handing
#219 and #215 to a new session. The steps held there too: a plain dry run
that read the defaults, a dry run of every option offered, one
confirmation, and a create whose `launch_options` matched the approved
`claude-opus-5[1m]` · `xhigh` · `plan`.

Teardown: both test sessions exited and their pane was closed.

### the account row on clauth's schema 2 (#238) — 2026-09-19

herdr 0.9.0, clauth 0.15.2, whose status feed is `schema: 2`. Before:
`main` at `5d4d1dc`, built from `git archive`. After:
`zvi/238-clauth-schema-2` at `56cba11`, not merged. Route A0 with its own
`XDG_CONFIG_HOME` and `XDG_STATE_HOME` under `/var/tmp`, a throwaway
repository, and a scratch plugin config (`[agents] favorites =
["claude"]`, nothing else) and state dir. Route B's five variables opened
the real form in the session's one pane. It read the owner's real
`~/.clauth/status.json`, which it only reads. Six profiles, none pinned, no
picker configured. Each pass pressed `⇥` until the account row had focus,
read the screen, and left by `esc`.

| Read | before | after |
|---|---|---|
| the account row | `account    active` | `account    active · Max 20x · 5h 5% · 7d 62%` |
| the account panel | the six names, and `clauth status degraded — showing names only` | each profile's plan, its `5h` and `7d` gauges and reset time, `●` on the live one, and `! … rate limited` on the two at `7d 100%` |

Nothing was submitted. The scratch state dir gained only the Linear cache.
Teardown: I stopped and deleted the disposable session and removed the
tree. No process from the pass was left.

### `agent_args`, the whole argument list (#219) — 2026-09-19

herdr 0.9.0. `zvi/219-agent-args` at `33e418f`, not merged, built with
`go build`. Route A0 with its own `XDG_CONFIG_HOME` and `XDG_STATE_HOME`
under `/var/tmp`, `onboarding = false`, `[worktrees] directory` under the
same path, a throwaway repository with one commit on `main`, and a
scratch plugin state dir and this plugin config:

```toml
[agents]
favorites = ["claude"]
[agents.extra_args]
claude = ["--verbose", "--model", "claude-opus-5[1m]", "--effort=xhigh"]
[clauth]
default = "smoke"
launcher = ["<scratch>/fake-claude", "{account}", "--"]
[timeouts]
detection_ms = 3000
```

`--verbose` stands in for any argument that is not one of the three option
flags, and `--effort=xhigh` is there to be displaced in its `=` spelling.
`fake-claude` printed its argv and slept, so no claude started and the
pass spent no account quota. The account is pinned so the real run takes
the launcher: unpinned, `herdr agent start` would have started the real
claude. Each `create` ran headlessly with `--no-worktree --placement
new-space`, the three plugin variables and no pane ids. Around each dry
run I took a snapshot of the workspace list, the repository's branches,
the worktree directory and the plugin state dir.

| Case | Result |
|---|---|
| `--dry-run --json` | exit 0. `agent_args`: `--verbose`, `--model`, `claude-opus-5[1m]`, `--effort=xhigh`, as `extra_args` has them. `launch_options` held the model and `xhigh`. None of the four changed. |
| the same with `--effort high --permission-mode plan` | exit 0. `agent_args`: `--verbose`, `--model`, `claude-opus-5[1m]`, `--effort`, `high`, `--permission-mode`, `plan`, with `--effort=xhigh` gone. None of the four changed. |
| the same for real | It typed the launch and stopped at detection, as it must with a stub (exit 1, kept), with the same `agent_args`. The pane showed the stub's argv as `[smoke] [--] [--verbose] [--model] [claude-opus-5[1m]] [--effort] [high] [--permission-mode] [plan]`: the launcher's two words, then `agent_args` element for element. |

Teardown: I stopped and deleted the disposable session and removed the
tree. No process from the pass was left, and none had a cwd under the
scratch tree.

### does `/spawn` fire, and does it choose the options? (Cell 13, #209) — 2026-09-19

herdr 0.9.0, Claude Code 2.1.278 on Opus 5 at `high` in auto mode, with
the owner's account and config. herdr-draft `main` at `4734e67`, installed
and its skill regenerated by the owner. Each scenario ran in a fresh
session in a sibling pane of this repository's worktree. Both prompts
avoided the words "herdr-draft", "spawn", "plugin" and "skill". Each
confirmation was dismissed with Esc rather than answered "Other: don't
create", as the cell asks, which the session took as a refusal. No session
was created, though the two briefs they wrote remain.

| Case | Result |
|---|---|
| "…get another agent started on that in a fresh session of its own" (a one-line wording fix) | **The skill fired** (step 2). The exports and the create went out in **one** call (step 3), with no stderr warning. Every option carried all three flags: `sonnet` at `medium` in `auto` first, then `sonnet` at `low` and `opus` at `medium`, all within the session's own `auto`. Each preview named the account, the branch and base, and the reap. **One miss:** it ran a single dry run, with the option flags already set, so it never read the defaults. Its description then said the choice replaced "claude's own settings (no configured defaults)", when `[agents.extra_args]` sets `claude-opus-5[1m]` at `xhigh`. |
| "Hand issue 215 … off to someone new … it should come back to me with a plan first" | **Passed in full.** The skill fired, and each create went out in one call with the exports. It dry-ran twice: plainly first, which read `claude-opus-5[1m]` and `xhigh` with provenance `extra_args`, then with its choice. Every option was `plan`. The two opus options carried the user's own id, quoted (`--model 'claude-opus-5[1m]'`), at `high` (recommended) and `xhigh`, and the third was `sonnet` at `high`. The recommended option's description read "Your default is xhigh". It passed `--no-reap` though `mark_ready` was off, which is harmless. |

The miss is the reason the skill's section 7 now numbers its three steps
and says why the first dry run cannot be folded into the second. Of the
two sessions that read the two-run text, one skipped the plain run. The
#209 tabletop read an earlier text that asked for the plain run alone, so
it does not count. The numbered text held in a real session in the
second pass, above.

Teardown: both sessions exited and their pane closed. The two briefs they
wrote are still in `~/Projects/handoffs/`.

### an empty branch (#199's open question) — 2026-09-19

herdr 0.9.0, git 2.53.0. `main` at `21813c3`, built from `git archive`, and
`zvi/refuse-empty-branch` at `192dc50`, since rebased unchanged as
`719dd7d`, not merged. Route A0 and Route B,
isolated as #199's run below: its own `XDG_*` dirs, `onboarding = false`,
`[worktrees] directory` under the scratch tree, and a scratch plugin config
with `branch_prefix = "zvi/"`, `default_worktree = true` and `[agents]
favorites = ["nosuchkind"]`, so no claude was started. In the form, the
title `empty check` derived `zvi/empty-check`, and `⇥ ⇥ ⇥ ↓` and twenty
backspaces cleared it.

| Case | `main` | the fix |
|---|---|---|
| `create --worktree --branch "" --on-failure clean` | **the defect.** herdr invented `worktree/silver-valley-bb3f`. The create failed at `starting agent`, and the clean removed the session but said `not its branch worktree/silver-valley-bb3f: nothing shows this run made it`. The branch was left behind. | **as expected.** `--branch is empty; leave it off to use the derived branch "zvi/fix-empty"`, **exit 2**, nothing created. |
| the form: the worktree row focused in the opening state, before a title | no verdict | no verdict |
| the form: the branch input cleared | no verdict | no verdict |
| the form: the same, `⌃S` | **the defect.** `✓ worktree  from HEAD`, naming no branch. `c remove it` removed the session and left `worktree/silver-meadow-372f` behind. | **as expected.** Refused: `branch name required` under the branch input, which kept the cursor. Nothing created. |

The independent review then reworded `create`'s refusal. It now names where
the branch it offers comes from (`leave it off to use the branch derived from
the title, "zvi/fix-empty"`), and offers none when that one would be refused
too. That change is held by `TestCreateRefusesAnEmptyBranch` and was not run
live again.

Teardown: both leftover branches deleted, the disposable session stopped and
deleted, no process with a cwd under the scratch tree, the tree removed, and
none of the pass's binaries left running.

### a branch git cannot hold (#199) — 2026-09-19

herdr 0.9.0, git 2.53.0. `main` at `5cead7a`, built from `git archive`, and
`zvi/fix-199-branch-name-validity` at `fce2882`, since rebased as `50c3687`
with the refusal unchanged, not merged. Route A0 with
its own `XDG_CONFIG_HOME` and `XDG_STATE_HOME` under `/var/tmp`,
`onboarding = false`, `[worktrees] directory` under the same path, and a
scratch plugin config and state dir with `branch_prefix = "zvi/"`,
`default_worktree = true` and `[agents] favorites = ["nosuchkind"]`, so no
claude was started. The throwaway repository had a branch `zvi/old` at
`cd7690e` ("old work"), one commit behind `main` at `7cab5a8`. The headless
cases ran `create --project <repo> --worktree --branch <name>`. The form ran
by Route B in the session's one pane. There the title `old` gave the branch
`zvi/old`, `⇥ ⇥ ⇥ ↓` reached the branch input, one space was typed after it,
and then `⌃S`.

| Case | `main` | the fix |
|---|---|---|
| `create --branch "zvi/old "` | **the defect.** A worktree on the existing `zvi/old` at `cd7690e`, not cut from `main`. Exit 1 at `starting agent`. | **as expected.** `--branch "zvi/old " cannot be used as a branch name: ends with a space`, **exit 2**, nothing created. |
| `create --branch "zvi/old<U+00A0>"`, a no-break space, which git accepts | **the defect.** herdr trimmed it: the same worktree on `zvi/old` at `cd7690e`. | **as expected.** `ends with whitespace (U+00A0)`, exit 2, nothing created. |
| `create --branch zvi/a..b` | failed inside herdr, `fatal: 'zvi/a..b' is not a valid branch name`, exit 1, and `herdr may have made part of it`. | **as expected.** `contains ".."`, exit 2, nothing created. |
| `create --branch zvi/new-work`, the control | — | a worktree on a new `zvi/new-work` cut from `7cab5a8` |
| the form: `zvi/old ` typed, `⌃S` | **the defect.** `✓ worktree  zvi/old  from HEAD`, but git put it on the existing `zvi/old` at `cd7690e`. | **as expected.** The worktree panel read `invalid branch name  ends with a space` under the parts. `⌃S` changed nothing: the cursor stayed in the branch input. |
| the form: the same, `⇥` to the agent row, `⌃S` | — | refused, and focus came back to the worktree row with the cursor in the branch input |
| the form: the space and `⌃S` sent 12 ms apart, inside the debounce | — | held for the check, then refused as above; nothing created |

**Second pass, after the independent review** (`3032541`, the same isolation
and a fresh scratch tree). The review moved the verdict to the line under the
branch part, and kept it at the panel's three-row floor, where it had been
dropped while the submit was still refused. It also stopped `create`
refusing the branch where no worktree can be made. For the 14-row case,
`stty rows 14 cols 104` ran in the pane before the form started.

| Case | the fix |
|---|---|
| the form: `zvi/old ` typed, `⇥` to the agent row, `⌃S` | refused; the panel read `branch  zvi/old`, then `invalid branch name  ends with a space`, then `base` |
| the same in a 14-row window | refused; the panel's three rows were the chips, the branch and the verdict, the base part giving way |
| `create --worktree --branch "zvi/a b"` in a directory that is not a repository | `worktree creation requires a git repository at "<plain>"`, exit 2: the more basic refusal first |

Teardown for each pass: the disposable session stopped and deleted, no process
with a cwd under the scratch tree, the tree removed, and none of the pass's
binaries left running.

### `create --dry-run` and `launch_options` (#209) — 2026-09-19

herdr 0.9.0. `zvi/fix-209-spawn-skill-session-options` at `8dc7393`, not
merged, built with `go build`. Route A0 with its own `XDG_CONFIG_HOME` and
`XDG_STATE_HOME` under `/var/tmp`, `onboarding = false`, `[worktrees]
directory` under the same path, a throwaway repository with one commit on
`main`, and a scratch plugin state dir and this plugin config:

```toml
[agents]
favorites = ["claude"]
[agents.extra_args]
claude = ["--model", "claude-opus-5[1m]", "--effort", "xhigh"]
[clauth]
default = "smoke"
launcher = ["<scratch>/fake-claude", "{account}", "--"]
picker = "<scratch>/picker"
[timeouts]
detection_ms = 3000
```

`fake-claude` printed its argv and slept, so no claude started and the
pass spent no account quota. `picker` logged its argv and answered with
the protocol's recorded object. Each `create` ran headlessly with the three
plugin variables and no pane ids. Around every dry run I took a snapshot
of five things: `herdr workspace list`, the repository's branches, the
worktree directory, the plugin state dir, and the picker's log.

| Case | Result |
|---|---|
| `--worktree --base main --dry-run --json` | exit 0. `dry_run: true`, `branch: zvi/smoke-dry`, `account: smoke`. `launch_options` held `claude-opus-5[1m]` and `xhigh`, with provenance `extra_args` for both and `built-in` for `permission_mode`. No ids and no `prompt_status`. None of the five changed. |
| the same with `--account auto` | exit 0, `account: picked-1`. The picker's argv was `--dir <repo> --json --strict --dry-run`; nothing else changed. |
| the same with `--effort high`, dry and then for real | The dry run exited 0. The real run made the worktree and typed the launch, then stopped at detection, as it must with a stub (exit 1, kept). Its `--json`, without the ids and the failure fields, was identical to the dry run's. The pane showed the stub's argv as `smoke -- --model claude-opus-5[1m] --effort high`: `launch_options` exactly, with `extra_args`' `xhigh` displaced. |
| the same title again, dry and then for real | Both exited 2 with `branch "zvi/smoke-dry" already exists, locally or on a remote`. Nothing changed. |

Teardown: I stopped and deleted the disposable session and removed the
tree. No process from the pass was left, and none had a cwd under the
scratch tree.

### the popup's dead end, with and without evidence (#208) — 2026-09-19

herdr 0.9.0, git 2.53.0. `main` at `930f9ad`, built from `git archive`, and
`zvi/fix-208-popup-dead-end` at `4af9d4a`, not merged. That was before the
branch was rebased onto #203 and #210, which change when a submit starts
and not what its failure screen says, and before the review's fixes, which
change neither case below. Route A0 with its own `XDG_CONFIG_HOME` and
`XDG_STATE_HOME` under `/var/tmp`, `onboarding = false`, `[worktrees]
directory` under the same path, and a scratch plugin config and state dir
with `[agents] favorites = ["nosuchkind"]`. No create got past its first
step, so the pass spent no account quota. The form ran by Route B in each
case's own workspace pane, about 118 cells wide. Each run typed a title,
sent `⌃S`, read the screen and left by `esc`.

| Case | `main` | the fix |
|---|---|---|
| worktree on, branch `zvi/collide`, where herdr's checkout path `<worktrees>/plain/zvi-collide` already held a file | **the defect.** `✗ worktree`, herdr `worktree_create_failed`: `Preparing worktree (new branch 'zvi/collide')`, `fatal: ... already exists`. The screen said `nothing was created — there is nothing to keep or remove`, but `git branch` listed `zvi/collide`. | **as expected.** The same failure. The screen said `herdr may have made part of it before failing — any of the branch zvi/collide, its checkout and a workspace for it; look before retrying`, wrapped at a space onto two lines. `git branch` listed `zvi/collide`. |
| worktree on, from a lane of a `--separate-git-dir` repository | `nothing was created — there is nothing to keep or remove`. That is correct here: herdr `linked_worktree_source`, and afterwards there was no branch, checkout or workspace. | **as expected.** The same line. |

In all four runs the footer offered only `esc close`, and `esc` closed the
form. The step row truncates herdr's error, so the code in each case was
read by running the same `worktree create` by hand. In the first case that
probe made the branch again, and it was deleted before the next run.

Teardown: the disposable session stopped and deleted, no process with a cwd
under the scratch tree, the tree removed, `pgrep -x herdr-draft` 0.

### the form frozen during the `auto` pick (#136) — 2026-09-19

herdr 0.9.0. Before: `zvi/fix-137-submit-waits-for-title-check` at
`ee14d8c`, which has no freeze. After: `b9faba0`, the freeze on top of it,
since rebased as `7839db0` with the freeze unchanged. Route A0 with the
isolation of the #195 run below, and a throwaway repository. To stretch
the `auto` pick into something a person can press keys during, the plugin
config named a stub picker that answers a `--dry-run` at once and makes a
real pick sleep 3 s and then refuse (exit 2). It logs every call. It also
set `[agents] favorites = ["claude"]`, `[clauth] default = "auto"`, and
`[clauth] launcher = ["echo", "LAUNCHED", "{account}"]` as a guard. A
refused pick creates nothing, and a pick that somehow went through would
have launched `echo` rather than claude. No session was created, and no
account was spent. The account row read `auto → personal-0` from the
stub's dry run.

Each pass typed the title `freeze check`, waited 1 s for its checks, and
pressed `⌃S`. During the pick it then typed ` typed` and pressed `⌃S`
again, read the screen, then pressed `⌃R ⌃R`, and read it once more after
the refusal.

| Read | before | after |
|---|---|---|
| during the pick | `title  freeze check typed`, `account  auto · asking the picker…` | `title  freeze check`, `account  auto · asking the picker…` |
| real picks in the stub's log | 2: the second `⌃S` started another | 1 |
| after the refusal | the form `⌃R ⌃R` had rebuilt under the pick: `title  untitled`, `worktree  off`, and the refusal on the account row | the form as validated: `title  freeze check`, `worktree  on · zvi/freeze-check ← main`, and the refusal on the account row |

`esc` then closed the form in both. Teardown: the disposable session
stopped and deleted, no process left with a cwd under the scratch tree, the
scratch tree removed, `pgrep -x herdr-draft` 0.

### a submit inside the title check's debounce (#137) — 2026-09-19

herdr 0.9.0. `main` at `7399894`, built from `git archive`, and
`zvi/fix-137-submit-waits-for-title-check` at `46c2179`, not merged. Route
A0 with the isolation of the #195 run below: its own `XDG_*` dirs,
`onboarding = false`, `[worktrees] directory` under the scratch tree, and a
scratch plugin config with `[agents] favorites = ["nosuchkind"]`, so no
claude was started. The form ran by Route B in the host workspace's pane, on
a throwaway repository with the worktree on. A second workspace labelled
`busy` was open, and the repository already had a branch `zvi/existing`.
The form opens on the title, so each pass sent the title and `⌃S` in
back-to-back `send-text` / `send-keys` calls, 11–24 ms apart.

| Case | `main` | the fix |
|---|---|---|
| `busy`, the label of an open workspace, then `⌃S` | **the defect.** `✓ worktree  zvi/busy`, and a second workspace labelled `busy`. | **as expected.** No submit: the title panel read `label in use` and marked the open `busy` with `!`. Nothing was created. |
| `existing`, whose branch `zvi/existing` exists, then `⌃S` | **the defect.** `✓ worktree  zvi/existing from HEAD`: herdr checked the existing branch out into a new worktree. `c` removed the worktree and kept the branch. | **as expected.** No submit: the title panel read `branch exists`. |
| `busy`, its `label in use` landed, then `2` and `⌃S` | **the defect.** No submit and no reason: the panel read `branch will be zvi/busy2`, the verdict for `busy` having refused it. | **as expected.** It waited for `busy2`'s check and went on: `✓ worktree  zvi/busy2`, `✓ tab  busy2`. `c` removed it. |

Teardown: the disposable session stopped and deleted, no process left with
a cwd under the scratch tree, the scratch tree removed, `pgrep -x
herdr-draft` 0.

### a create whose first step made nothing (#192) — 2026-09-19

herdr 0.9.0, git 2.53.0. `main` at `7399894`, built from `git archive`, and
`zvi/fix-192-exit-code-nothing-created` at `5482772`, not merged. Route A0
with its own `XDG_CONFIG_HOME` and `XDG_STATE_HOME` under `/var/tmp`,
`onboarding = false`, `[worktrees] directory` under the same path, and a
scratch plugin config and state dir with `[agents] favorites =
["nosuchkind"]`. No create got past its first step, so the pass spent no
account quota. Each `create` ran headlessly with the three plugin variables
and no pane ids.

| Case | `main` | the fix |
|---|---|---|
| `--worktree`, from a lane of a `--separate-git-dir` repository | **the defect.** herdr `linked_worktree_source`, `nothing was created`, **exit 1**. | **as expected.** The same refusal and line, **exit 4**. `--on-failure clean` attempted nothing. `--json`: `ok: false`, `failed_step: creating worktree`, no `workspace_id` or `space_*` ids. Afterwards: no workspace, no `zvi/*` branch, nothing new under the worktree directory. |
| `--worktree --branch zvi/collide`, where herdr's checkout path `<worktrees>/plain/zvi-collide` already held a file | **a second defect.** herdr `worktree_create_failed`: `Preparing worktree (new branch 'zvi/collide')`, `fatal: ... already exists`. stderr said `nothing was created`, exit 1, but `git branch` listed `zvi/collide`: git makes the branch before it refuses the path. | **as expected.** The same failure, **exit 1**, and `herdr may have made part of it before failing -- the worktree's checkout, or its branch zvi/collide; look before retrying`. The branch was there. Retried unchanged: exit 2, `branch "zvi/collide" already exists`. |

The second case is why exit 4 needs evidence rather than a space missing
from the reply. Under that simpler rule it would have been exit 4, telling a
caller nothing existed when a branch did, and the retry that exit invites is
the one refused above.

Teardown: the disposable session stopped and deleted, no process with a cwd
under the scratch tree, the tree removed, `pgrep -x herdr-draft` 0.

### the clean gate's base, on the recorded path (#193) — 2026-09-19

herdr 0.9.0, git 2.53.0. `main` at `7399894`, built from `git archive`, and
`zvi/fix-193-clean-gate-head-base` at `931a1ff`, not merged. Route A0 with
its own `XDG_CONFIG_HOME` and `XDG_STATE_HOME` under `/var/tmp`,
`onboarding = false`, `[worktrees] directory` under the same path, and a
scratch plugin config and state dir with `[agents] favorites =
["nosuchkind"]`, so every create stopped at `starting agent` and the pass
spent no account quota. Each case had its own repository of three commits,
`HEAD~1` at `78803a0`, and ran `create --worktree --base HEAD~1
--on-failure clean --json`.

**What this pass cannot show is the fix itself.** #193's remaining defect
is in the clean's fallback, which runs only when the create could not
record the commit it cut from: a `rev-parse` that fails just before a
`worktree create` that succeeds. A live run cannot arrange that, so
`TestCleanGateCountsFromTheCommitTheBaseNamed` carries it. The pass is here
because the same change makes `gitx.Disposable` refuse anything but a full
commit id, and the ordinary path goes through `Disposable` too.

"One commit" is a worktree that a `post-checkout` hook committed into as
`git worktree add` made it, standing in for an agent that committed before
the failure. The repository's own `core.hooksPath` has to name the hook by
absolute path: a global `core.hooksPath` beats `.git/hooks`, and a
relative one resolves against the linked checkout.

| Case | `main` | the fix |
|---|---|---|
| pristine | `cleaned`, `deleted_branch` at tip `78803a0` | the same |
| one commit in the worktree | `clean_refused: worktree has 1 commit(s) not on 78803a0…`, the checkout and branch kept | the same |

Teardown: the disposable session stopped and deleted, no process with a
cwd under the scratch tree, the tree removed, `pgrep -x herdr-draft` 0.

### a submit inside the project row's debounce (#195) — 2026-09-19

herdr 0.9.0. `main` at `905f328`, built from `git archive`, and
`zvi/fix-195-submit-waits-for-dir-check` at `d11cedb`, not merged. Route A0
with its own `XDG_CONFIG_HOME` and `XDG_STATE_HOME` under `/var/tmp`,
`onboarding = false`, `[worktrees] directory` under the same path, and a
scratch plugin config and state dir with `[agents] favorites =
["nosuchkind"]`. No claude was started, so the pass spent no account quota.
The form ran by Route B in the host workspace's pane, whose cwd was a
throwaway repository, so each pass opened on `project  <repo>` and
`worktree  on · from main`. Each pass typed a title, `⇥ ⇥` to the project
row, and then sent the second path and `⌃S` in back-to-back `pane
send-text` / `send-keys` calls, 9–18 ms apart, well inside the 150 ms
debounce. `<repo>`, `<plain>` and `<nowhere>` sit side by side in the
scratch tree, and `<plain>` is a directory that is not a repository.

| Case | `main` | the fix |
|---|---|---|
| `<plain>`, `⌃S` | **the defect.** `✗ worktree  herdr worktree create --cwd <plain> …`, herdr `not_git_worktree`: the submit used the repository's answers. | **as expected.** `✓ workspace`, `✓ tab`, `✗ nosuchkind`: the submit waited, then went on without a worktree, in a new space whose pane's cwd was `<plain>`. One `⌃S`. `c` removed it. |
| `<nowhere>`, a path that does not exist, `⌃S` | **the defect.** `✗ worktree  herdr worktree create --cwd <nowhere> …`. | **as expected.** No submit: the project row read `<nowhere>  invalid`, the worktree row `not a git repository`, focus on the project row. Nothing was created. |
| `<pla>`, `⌃S`, then `in` | **the defect**, and a second finding: it submitted `<plai>`, not `<pla>`. `⌃S` reaches the app as a `SubmitMsg` through a command round trip, so a key already queued behind it lands first. | **as expected.** The submit arrived at `<plai>`, was held while `n` came in, and went on once the check of `<plain>` landed: the session's pane cwd was `<plain>`. |

**Second pass, rebased onto #197 (#194) and #190** (`9d9bb8f`, against
`main` at `ca64e46`, same isolation and a fresh scratch tree). #197 holds a
submit for its base check too, and the dir check that releases #195's hold
is what schedules that check. `<repo2>` is a second repository whose
`projects.json` entry remembers `worktree: true` and `base: "old-branch"`,
one commit behind its `main`: `old-branch` at `4b176bd`, `main` at
`f244bae`.

| Case | `main` at `ca64e46` | the fix |
|---|---|---|
| `<repo2>`, `⌃S` | **the defect, a quieter shape.** `✓ worktree  zvi/live-check from HEAD`, cut at `f244bae`: the submit was built before `<repo2>`'s memory landed, so its remembered base never applied. | **as expected.** `✓ worktree  zvi/live-check from old-branch`, cut at `4b176bd`: the submit waited for the dir check, then for the base check it started, then went on. One `⌃S`. |
| `<plain>`, `⌃S` | the first pass's defect, unchanged | the first pass's result, unchanged |

Each create was removed with `c`, which took the worktree's branch too
(#190). `projects.json` was unchanged afterwards, since no create succeeded.

Teardown: the disposable session stopped and deleted, no process left with
a cwd under the scratch tree, the scratch tree removed, `pgrep -x
herdr-draft` 0.

### a remembered base the branch list does not name (#194) — 2026-09-19

herdr 0.9.0. `zvi/fix-194-unlisted-base` at `75c9836`, not merged, against
`main` at `905f328`, both built into `/var/tmp/h194`. Route A0 with its own
`XDG_CONFIG_HOME` and `XDG_STATE_HOME`, `[worktrees] directory` under the
same path, and a scratch plugin config and state dir with `[agents]
favorites = ["nosuchkind"]`, so every create stopped at `starting agent`
and the pass spent no account quota. The repository had 51 branches, each
with its own committer date: `b1`…`b49`, then `main` (`8e8f645`), then
`old-branch` (`492faeb`), so the picker's 50 hold `main` and not
`old-branch`. `projects.json` in the scratch state dir remembered one base
per case. The form ran through Route B in a pane of the same session.

| Case | `main` | the fix |
|---|---|---|
| `create`, remembered `HEAD` | `base: HEAD` from `projects.json`, cut from `8e8f645` | `base` omitted, still `projects.json`, cut from `8e8f645` |
| `create`, remembered `gone-branch` | **exit 1 at `creating worktree`**, herdr `worktree_create_failed: fatal: invalid reference: gone-branch` | `herdr-draft create: ignoring base "gone-branch" from projects.json: no such commit here; using HEAD`, `provenance.base` `built-in`, cut from `8e8f645` |
| `create`, remembered `old-branch` | cut from `492faeb` | cut from `492faeb` |
| `create --base HEAD` | `base: HEAD` from `flag` | `base` omitted, from `flag`, cut from `8e8f645` |
| `create --base gone-branch` | exit 1 at `creating worktree`, as above | **exit 2**, `--base "gone-branch" names no commit in /var/tmp/h194/repo`, nothing created |
| the form, remembered `HEAD` | `on · from main`, the `HEAD (main)` row | the same |
| the form, remembered `gone-branch` | `on · from main`, **nothing said** | `on · from main`, and the panel's note line: `ignoring base "gone-branch" from projects.json: no such commit here; using HEAD`. A submit cut from `8e8f645`. |
| the form, remembered `old-branch` | `on · from main`, **nothing said**. A submit read `✓ worktree  smoke/form-main-old from HEAD` and cut from `8e8f645`, where `create` cut from `492faeb`: the issue's drift, measured. | `on · from old-branch`, with `old-branch` listed right after `HEAD (main)` and selected. A submit cut from `492faeb`, as `create` did. |
| `create` from a lane (`git worktree add`, one commit, `c130cd1`), remembered `gone-branch` | exit 2, #171's refusal: `... gone-branch could not be ... -- pass --base to choose another` | the note on stderr, `base` `c130cd1…` from `checkout`, cut from `c130cd1` |

**Not checked live:** what each path writes back to `projects.json`, because
a create that fails at `starting agent` writes no memory.
`TestTierBase_HeadIsTheHeadRowAndIsRememberedAsTheFormRemembersIt` and
`TestFormAndCommandProduceTheSamePlan` cover it, since both paths remember
the `plan.Input`'s own `BaseRef`.

**Second pass, after the independent review** (`10558fc`, the same session
and repository). The review found that the popup would now keep a
remembered `HEAD^0`, a spelling of HEAD itself that #193's clean gate
treats as a base with no commits beyond it.

| Case | Result |
|---|---|
| `create`, remembered `HEAD^0` | `base` omitted, still `projects.json`, cut from `8e8f645` |
| `create --base HEAD^0` | `base` omitted, from `flag`, cut from `8e8f645` |
| `create`, remembered `HEAD~1` | kept: `base: HEAD~1`, cut from `3b6fa8b` |
| `create --no-worktree --base gone-branch` | not refused: on to `starting agent`, as before this branch |
| the form, remembered `HEAD^0` | `on · from main`, the `HEAD (main)` row, no note, which is what `main`'s binary shows too |

Not checked live: a base the user chose surviving a project change (the
review's first finding), which needs the project row driven by keystroke.
`TestPopup_AChosenBaseSurvivesAProjectChange` reproduces it through
`Update`.

Teardown: the disposable session stopped and deleted, no process with a
cwd under `/var/tmp/h194`, the tree removed, `pgrep -x herdr-draft` 0.

### `create` and the popup from inside a lane (#171) — 2026-09-18

herdr 0.9.0. `zvi/fix-create-from-a-worktree-lane` at `c2240c0`, not
merged, against `main` at `52d9750`, both built into a scratch directory.
Route A0 with its own `XDG_CONFIG_HOME` and `XDG_STATE_HOME`, `[worktrees]
directory` under `/var/tmp`, and a scratch plugin config and state dir with
`[agents] favorites = ["nosuchkind"]`. Every create stopped at `starting
agent`, and the pass spent no account quota. The setup was the issue's: a
throwaway repo with a parent workspace `w1`, and a lane made by `herdr
worktree create --branch lane/one` (`w2`), with one commit on it: `main` at
`de2f1b4`, the lane at `4486c1e`. Every `create` ran from the lane's
checkout, with `w2`'s pane ids and the three plugin variables. herdr's own
`workspace list` gave `w2` a `repo_root` of the primary checkout and a
`checkout_path` of the lane, which is what the popup's new default reads.

| Case | Result |
|---|---|
| `main`, the issue's five rows | **the defect, reproduced.** `--worktree`, and no worktree flag: exit 1, `linked_worktree_source`, `nothing was created`. `--no-worktree`: a tab in `w2`, in the lane. `--worktree --project <repo>`: cut from `de2f1b4`, `main`'s commit. Adding `--base lane/one`: cut from `4486c1e`. |
| the fix, the same rows | **as expected.** `--worktree`, and no flag: exit 1 at `starting agent`, each checkout at `4486c1e`. `--no-worktree`: a tab in `w2`, cwd the lane. `--worktree --project <repo>`: `de2f1b4`, unchanged, since the primary checkout is not a lane. `--worktree --base lane/one`, with no `--project`: `4486c1e`. |
| `--worktree --json` | **as expected.** `project_dir` the lane, `base` `4486c1e2e7a654264b758096fe10617f6cca9fad`, `provenance.base` `checkout`. |
| a detached lane | **as expected.** A commit on a detached HEAD, `c47b30a`: the new checkout was cut from it. |
| the popup, opened in the lane's pane (Route B) | **as expected.** The context was `w2`'s entry from `workspace list`. The form opened on `project  /var/tmp/h171/wt/repo/lane-one`, `worktree  on · from lane/one`, with the lane then the repo root as the project row's first two candidates. A submit read `✓ worktree  zvi/form-lane from 4486c1e`, and the checkout was at `4486c1e`. `c` on the failure screen removed it. |
| the popup, a lane picked in the project row | **the defect, then the fix.** Opened from `w1`, `lane-one` typed into the project row, a title, `⌃S`. `main`'s binary: `✗ worktree  herdr worktree create --cwd <lane> …`. The fix: `✓ worktree  zvi/picked-lane from 4486c1e`. The issue had read this case from the code; this is its first measurement. |

**Not checked live:** that the lane's commit is never remembered, because a
create that fails at `starting agent` writes no memory. `TestLane_TheCommitIsNotRemembered`
and `TestSubmit_FromALaneTheCommitIsNotRemembered` cover both paths.

**Second pass, after the independent review** (`34b51ca`, same isolation, a
fresh setup: `main` at `3b2a5c1`, the lane at `8f42d06`). The review found
two defects in `c2240c0`, and both reproduced live on that code first:

| Case | `c2240c0` | `34b51ca` |
|---|---|---|
| `--worktree --base HEAD`, from the lane | cut from `3b2a5c1`, the **primary's** commit: herdr resolved `HEAD` in the source | cut from `8f42d06`, the lane's: every base is now resolved in the lane first |
| a lane of a `--separate-git-dir` repository | `--cwd <dir holding the git directory>`, herdr `not_git_worktree` | no substitute: `--cwd <lane>`, herdr's own `linked_worktree_source` |

With `34b51ca`, no base still gave `8f42d06`. `--base main --json` gave
`base: main` from `flag`, cut from `3b2a5c1`. Teardown as above.

Teardown: the disposable session stopped and deleted, no process with a cwd
under the scratch tree, the scratch tree removed, `pgrep -x herdr-draft` 0.

### the options row through the real popup — 2026-09-18

herdr 0.9.0, claude 2.1.277, the owner's live install at `52d9750` (#181
and #187 merged), the owner's own `config.toml`, and the real popup opened
by its keybinding. The owner drove it. The config's `[agents.extra_args]`
passes `--model claude-opus-5[1m] --effort xhigh`, and `[clauth] launcher`
is `claude-as`. One launch, prompt `hi`.

| Case | Result |
|---|---|
| the form, before submitting (Route B against the same config, read-only) | **as expected.** The row read `claude-opus-5[1m] · effort xhigh`, dimmed, and the panel said `inherit sends nothing of its own; [agents.extra_args] passes --model claude-opus-5[1m]` over `[agents.extra_args] adds: --model claude-opus-5[1m] --effort xhigh`. That is extra_args shown for the first time, not options. |
| worktree off, `effort low`, `plan`, model left on `inherit`, `⌃S` | **as expected.** `herdr pane process-info` gave the agent's argv as `claude --settings {"teammateMode":"tmux"} --model claude-opus-5[1m] --effort low --permission-mode plan`. There is one `--effort`, so `low` replaced extra_args' `xhigh`. The model came from extra_args, since that option inherited. The `--settings` element is `claude-as`'s own, so the pinned launch went through the owner's wrapper and the options followed it. The banner read `Opus 5 (1M context) with low effort`, claude said it was in plan mode, and the tab was named `options demo`. |
| where it landed | **as designed, and not what was predicted.** Opened from a pane in a linked worktree's space, the project row defaulted to the repository's main checkout, so `tab in` put the session in the repository's own space rather than the worktree's. |

Teardown: the demo pane closed, and its claude process gone.

"Where it landed" describes the default at `52d9750`. #171 changed it: the
popup opened in a linked worktree's space now opens on that worktree's
checkout, as `create` run there does, so the same submit would put the
session in a tab in the worktree's own space.

### the options row, launching claude (agent-options spec) — 2026-09-18

herdr 0.9.0, claude **2.1.277** (it updated itself during the day; the drift
check passed again on it, with the same `--effort` levels and
`--permission-mode` choices), `zvi/session-configuration` at `3749c82`
(#181), not merged. Route A0 with isolated `XDG_*` dirs, a throwaway repo,
and a scratch plugin config and state dir. Three launches on `personal-0`,
two one-word prompts: the pass cost the account about 1% of its 5h window.

| Case | Result |
|---|---|
| Path A, the form (Route B) | **as expected.** Typing `claude-opus-5[1m]` onto the model chips jumped to `other`; `effort high`, `plan`; `⌃S`. The progress screen read `… claude  options-smoke · claude-opus-5[1m] · effort high · plan mode` and the footer `answer the dialog in the pane — your prompt goes out as soon as you do`, over the trust dialog. herdr typed `claude --model 'claude-opus-5[1m]' --effort high --permission-mode plan`. After `Down`, a check that `❯` sat on "Yes, I trust this folder", and `Enter`, the prompt went out and the form closed. claude's banner read `Opus 5 (1M context) with high effort`, its footer `⏸ plan mode on`, and it answered `hi` in plan mode. |
| Path B, headless, pinned | **as expected.** `--account personal-0 --model sonnet --effort low --permission-mode auto --prompt hi`: the typed launch was `clauth start personal-0 -- --model sonnet --effort low --permission-mode auto`. Trust is kept per launch mechanism, so the dialog came up again and `create` failed fast at detection, as headless `create` must: exit 1, `prompt_status: unsent`, and `agent_options` all three. After answering the dialog: `Sonnet 5 with low effort` and `⏵⏵ auto mode on`, with **no** first-use dialog for `auto`. That was the one thing that could have put a screen between the launch and the prompt. |
| unknown model id | **an ordinary error, not a dialog.** Path A, headless, `--model no-such-model --prompt hi`: exit 0, `prompt_status: sent`. The banner read `no-such-model with high effort`, and the prompt was answered with `There's an issue with the selected model (no-such-model). It may not exist or you may not have access to it. Run /model to pick a different model.` Nothing belongs in `dialog.go`. The create is honest about delivery; that the session cannot work is claude's to say, and it does, in the pane. |

Teardown: the disposable session stopped and deleted, no process left with
a cwd under the scratch tree, the scratch tree removed, `pgrep -x
herdr-draft` 0. claude recorded its trust in `personal-0`'s own config for
the throwaway path, which is left as it is.

### the options row (agent-options spec) — 2026-09-18

herdr 0.9.0, claude 2.1.276, `zvi/session-configuration` as it stood before
the independent review's fixes, not merged. Route A0 with its own `XDG_CONFIG_HOME` and `XDG_STATE_HOME`, a
scratch plugin config and state dir, and `[clauth] enabled = false` for the
form, so no account row. No claude was started: the pass spent no account
quota and wrote nothing of the user's.

| Case | Result |
|---|---|
| drift | **as expected.** `--effort` listed `low, medium, high, xhigh, max`, and `--permission-mode` listed `acceptEdits, auto, bypassPermissions, manual, dontAsk, plan`. |
| form at rest | **as expected.** With `[agents.extra_args] claude = ["--model", "sonnet"]` and `[agents.options.claude] effort = "high"` plus a refused `permission_mode = "bypassPermissions"`: the row read `sonnet · effort high` (`sonnet` dim). The panel read `inherit sends nothing of its own; [agents.extra_args] passes --model sonnet`, `[agents.extra_args] adds: --model sonnet`, the refusal warning, and `from config.toml`. The footer rung was `↑↓ option · ←→ value`. |
| form walk | **as expected.** `←` on model reached `other` and opened `name`. `claude-opus-5[1m]` typed in. A following space was refused, and the row read `… · effort xhigh · plan mode` after `→` on effort and `→ →` on mode. The rest of `send-text ' --dangerous'` landed after the refused space, as `claude-opus-5[1m]--dangerous`. That is a valid id, because the rule only forbids a leading dash, so it is one argument to `--model` and not a flag. |
| codex round trip | **as expected.** Agent `→` to codex: `options  none for codex`. `←` back: every choice restored. |
| Path B, headless | **as expected.** `[clauth] launcher = ["echo", "LAUNCHED", "{account}"]`, extra args `["--model", "sonnet", "--verbose"]`, `--model 'claude-opus-5[1m]' --permission-mode plan --account personal-1`. The pane was typed `echo LAUNCHED personal-1 --verbose --model 'claude-opus-5[1m]' --effort high --permission-mode plan`, and echo printed the id unquoted. Extra args' `--model sonnet` was replaced and `--verbose` kept. `--json` reported all three options, with provenance `option.effort: config.toml` and the other two `flag`. Exit 1 at detection, as an echo launcher must. |

Teardown: the disposable session stopped and deleted, the scratch tree
removed, `pgrep -x herdr-draft` 0.

### control characters in a `create` title (#178) — 2026-09-18

herdr 0.9.0. First `main` at `e558782`, then `zvi/sanitize-create-titles`
built from the uncommitted tree on `e558782` that became this branch's
commit, not merged. Route A0 with the §15.1 run's isolation, and
`[agents] favorites = ["nosuchkind"]`, so no account quota was spent. For
the first pass a second disposable server hosted a TUI client attached to
the first, and `pane read --source visible` gave herdr's own sidebar and
tab bar. `--issue` was not run live, because it needs a real Linear key;
`TestFormAndCommandProduceTheSamePlan` and
`TestTitleIsKeptToWhatTheFormKeeps` cover it.

| Case | Result |
|---|---|
| `main`, titles with a tab, a line break, a BEL and an ESC sequence | **the defect.** herdr stored all four labels verbatim, control characters and all, for the space and the tab. Its TUI drops them when drawing: the sidebar read `tabhere`, `line oneline two`, `bell here` and `esc [7mreverse[0m h…`, and the tab bar read `line oneline two`. Nothing reached the terminal as a live code, but a tab or a line break runs the words together. |
| the fix, the same four titles, `--worktree` | **as expected.** One stderr line each, e.g. `herdr-draft create: --title has characters the form's title row does not keep (tabs and line breaks become spaces, the rest are dropped); using "line one line two"`. The labels were `tab here`, `line one line two`, `bell here` and `esc [7mreverse[0m here`, for the space and the tab, and the branches were derived from them (`zvi/line-one-line-two`). |

Teardown: both sessions stopped and deleted, the scratch tree removed,
`herdr session list` without either, `pgrep -x herdr-draft` 0.

The stderr line quoted above is the wording the run saw. Review tightened it
in the branch's next commit to `(a tab, CR or LF becomes a space, and the
rest are dropped)`, because a VT, FF or NEL is dropped, not spaced.

### a long `create` title is cut to 32 runes (#176) — 2026-09-18

herdr 0.9.0, `zvi/cap-create-titles-at-32-runes`, built from the
uncommitted tree on `9141b77` that became this branch's commit, and not
merged. For contrast, `main` at `9141b77` was built from `git archive`.
Route A0, with the §15.1 run's isolation: its own `XDG_CONFIG_HOME`,
`XDG_STATE_HOME` and `[worktrees] directory` under `/var/tmp`, a scratch
plugin state dir, and `[agents] favorites = ["nosuchkind"]` with
`branch_prefix = "zvi/"`. Every create stopped at `starting agent`, and the
pass spent no account quota. `--issue` was not run live, because it needs a
real Linear key. `TestFormAndCommandProduceTheSamePlan` and
`TestTitleIsKeptToWhatTheFormKeeps` (then named
`TestLongTitleIsCutAndSaysSo`) cover it.

| Case | Result |
|---|---|
| 47-rune `--title`, `--no-worktree --json` | **as expected.** The first stderr line was `herdr-draft create: --title is 47 runes and a title is capped at 32, as in the form; using "fix login redirect loop when the"`. `workspace list` gave `w1 'fix login redirect loop when the'`, `tab list` gave `w1:t1` with the same label, and `--json`'s `title` was the cut one. |
| the same title again | **as expected.** The cut line, then exit 2 with `workspace w1 is already labelled "fix login redirect loop when the"`: the title-in-use refusal compares the cut title. |
| 43-rune `--title`, `--worktree` | **as expected.** `using "add a CSV export button to the r"`. The branch was `zvi/add-a-csv-export-button-to-the-r`, derived from the cut title, and `w2` and `w2:t1` carried the cut title. |
| 43-rune `--title` from `main`'s binary | **the defect, for contrast.** No line, and `w3` and `w3:t1` read `'wire the exporter to the nightly job runner'` whole. |

Teardown: the session stopped and deleted, the scratch tree removed,
`herdr session list` without it, `pgrep -x herdr-draft` 0.

### every tab a session opens carries its title (placement spec §15.1) — 2026-09-18

herdr 0.9.0, `zvi/name-created-tabs`, built from the tree that became
`ca51eed` (it differed from that commit in doc comments only), and not
merged. Route A0, with its own `XDG_CONFIG_HOME` and
`XDG_STATE_HOME` so no installed plugin loaded, and `[worktrees] directory`
under `/var/tmp`. `[agents] favorites = ["nosuchkind"]` and a scratch plugin
state dir, so every create stops at `starting agent`, and the pass spent no
account quota and wrote nothing of the user's. A second disposable server
hosted a TUI client attached to the first, and `pane read --source visible`
from it gave herdr's own tab bar as text. The client needed
`onboarding = false` in the first session's config: the onboarding card
ignores `esc` and offers only `↵ continue`.

| Case | Result |
|---|---|
| `new space`, headless | **as expected.** Three steps, `[2/3] naming tab ... ok`; `tab list` gave `w1:t1 'Fix login redirect loop'`, and so did the tab bar. The same create from `main`'s binary (`5a17647`) left its tab `'1'`. |
| worktree, headless | **as expected.** `w3:t1 'Add export button'`. |
| §5.2 reuse, headless | **as expected.** Cell 9's stale-workspace recipe, plus `git branch -D` of the branch, since #147 refuses an existing one. herdr reused `w3`; the claim `w3:t2` was named `'Export button, take two'`, and `w3:t1` kept `'Add export button'`. The tab bar read `Add export button │ Export button, take two`. |
| `tab here`, `split here`, headless | **as expected.** Two steps each, no rename. `tab here` made `w2:t2 'Tab here session'` at creation. `split here` added a pane to the invoking tab, `w2:t1`, whose label stayed `'1'`. |
| a refused rename | **as expected.** A `HERDR_BIN_PATH` shim failed `tab rename` alone with a `tab_not_found` envelope. `[2/3] naming tab ... failed, continuing: … tab_not_found …`, the run went on, and the failure line named `starting agent`. |
| form, worktree on | **as expected.** A typed title and `⌃S`: `✓ worktree`, `✓ tab  Wire the CSV exporter`, `✗ nosuchkind`. `tab list` agreed. |

Teardown: both disposable sessions stopped and deleted, the scratch tree
removed, `herdr session list` without either, `pgrep -x herdr-draft` 0.

### a worktree session keeps its own space (placement spec §14) — 2026-09-17

herdr 0.9.0, `zvi/worktree-owns-its-space` at `66c3070` plus the `create
--help` wording of the commit that records this run, built into a scratch
directory. Route A0 (a headless disposable
server), with the form run inside one of its panes (Route B's variables set
by a launcher script) and a scratch state dir. `[agents] favorites =
["nosuchkind"]`: every create stops at `starting agent` with herdr's
`unsupported_agent_kind`, so this checks topology and form, not agents, and
spent no account quota. No trust prompt was reached.

| Cell | Result |
|---|---|
| 8, headless | **as expected.** The first `create --worktree` in a fresh repo opened the parent (idle shell) and the session's own nested space. The second opened exactly one nested space with the agent's pane in it, `placement` `new-space` from `worktree`, and the agent and space triples equal. `--worktree --placement tab-here` exited 2 with `--placement tab-here needs --no-worktree: a worktree session runs in the worktree's own space, which herdr groups under the repository's`. `--no-worktree` with the parent open still went `tab-in` the parent. `--on-failure clean` removed the worktree's space. |
| 8, form | **as expected, one gap.** The row read `the worktree's own space` on open. `⇥` from `worktree` landed on `agent`. Turning the worktree off made the row read `tab in hdux-repo`, and turning it back on restored the inert row. A submit showed two steps (`worktree`, then the failed agent), with no tab step, and added exactly one workspace, nested, holding the agent's pane, while the parent and invoking workspaces were unchanged. **Not checked:** the click-focused panel and footer, which a headless pane cannot click. `field_placement_test.go` and the `placement-panel-worktree-80x24` frame pin them. |
| 14, setup | **the corrected setup works.** A plain `workspace create --cwd … --label smoke14` had `worktree: null`. `worktree open --cwd <repo> --path <repo> --no-focus` answered `already_open: true` for it, label kept, and after that `--no-worktree` defaulted `tab-in` `smoke14`, and `--worktree` opened one space nested under it. |

Teardown: every workspace, worktree, branch and both throwaway repos
removed; `herdr session list` back to `default`; `pgrep -x herdr-draft` 0.

### placement tab-in (#128) — 2026-09-16

herdr 0.9.0, `zvi/placement-existing-workspace` at `afab907`, built from a local
worktree and not merged. Route B (the real form) against the real
`~/Projects/herdr-draft` checkout, which workspace `wG` already held; the form
was opened from a pane in a different workspace, so "here" and "the repo's
space" were not the same place.

| Cell | Result |
|---|---|
| 14 — a tab in the repository's own space | **partial: the form's default is confirmed, the submit path is not.** With `project` = the repo and worktree off, the `placement` row read `tab in <space>` with no input at all — the resolver's answer reaching the keyboard, which is the one thing the `tabin_test.go` fakes cannot show. The form was then dismissed with esc. Everything after the submit is therefore **unexercised**: the workspace count staying level, `tab_count` going up by one, and the pane landing inside the existing space. Cell 14's own setup (a throwaway repo and a dedicated `smoke14` workspace) was not used either — this was the live repo. |

The unexercised half is the half that touches herdr rather than the form, so
treat #128's sidebar behaviour as **untested at the keyboard** until a run
follows the cell as written.

### herdr 0.9.0 — 2026-09-08/09

herdr 0.9.0, clauth 0.15.1, plugin at `d15255f`. Run via Route A0 (headless
disposable sessions) plus Route B for the form. Three sittings: cells 3–6 on
2026-09-08 at `0bef09e`, cells 2/7/8/9 on 2026-09-09 at `d15255f`, and cell
10 plus #99's re-check on 2026-09-09 at `e9961e1` (the #101+#99 stack, built
locally and not yet merged).

| Cell | Result |
|---|---|
| 1 — Path A, worktree on | **pass, with a finding, and two sub-checks not exercised** — run in the real popup on 2026-09-09 at `87d2059`. The popup opened as a **card** with the terminal visible around it (not a wall); the submit painted the progress stack inside it with the same header, rule and label column; the settled failure screen carried the keep/remove buttons; `k` kept it and the agent survived. The failed step truncated to `claude started; answer the dialog in the pane, then keep this session -- it is showin…`, i.e. **the whole instruction survived and only the trailing rationale was cut** — byte-identical to `submit-blocked-start-101x30.txt`, which #112 added for exactly this. The trust-dialog stop at step 2 was the expected pass condition. Not exercised: the opening state live (it is pinned at the shipped size by `assembled-opening-101x30.txt`), and the popup's unsent-prompt recovery line, because no prompt was typed. The run resolved placement to **`tab here`** from `last-used.json`, not the cell's nominal `new space`, so it also re-confirmed #99's divergence: the worktree's own space held a bare shell and the agent landed in the invoking workspace's new tab. See the boxed caution in the cell for the sequencing trap this exposed |
| 2 — Path A, worktree off | **pass, with a finding** — `split here` produced exactly one new pane in the invoking tab, the progress stack's first row read `pane`, and the multi-line prompt really was delivered. But the agent could not run: `[agents.extra_args]`' quoted model id reaches Path A's argv exec unstripped, so `--model` was the literal `'claude-opus-5[1m]'`. All three steps reported success. **#72** |
| 3 — Path B, worktree on | **pass**, after #94 — the blocked-detection path above. Reproduced the pre-fix failure first, then confirmed the agent survives and the dialog answers cleanly |
| 4 — Path B, worktree off | **pass** — all four steps, prompt delivered and answered in the pane, `tokens.clauth` and `process-info` both as expected |
| 5 — headless `create` | **pass** — all six inert probes, the no-config warning, and a live create returning `ok: true` with its provenance map |
| 6 — forbidden repo-config key | **pass except one line** — value applied, both forbidden keys rejected with reasons, `branch_prefix` falling back to the user's own. The `from .herdr-draft.toml` provenance line does not render: **#93** |
| 7 — per-project defaults | **pass, both halves** — with `last-used.json` saying `claude`/`new-space` and `projects.json[X]` saying `codex`/`tab-here`, switching the project to X brought back X's values (tier 1 over tier 3), and the account row degraded to `account pinning only applies to claude`. Then, on a **second** project change, a touched `placement` survived while the untouched `agent` re-applied — the case CLAUDE.md's shared-snapshot hazard would have broken |
| 8 — placement tab here | **pass** — two new workspaces (the worktree's own plus the origin repo's) and one new tab in the invoking workspace, agent pane in **that** workspace with its cwd on the checkout, `agent_status: blocked` rather than dead. #90's message was exactly right: instruction first, then the matched `"Quick safety check"`, then herdr's own error, with the keep/clean gate offered. See the pane-count caution in the cell — another plugin adds one |
| 9 — the reuse path | **pass, via a rewritten recipe** — not runnable as documented (refused by the form's duplicate check and by `worktree create` alike); reached through a stale workspace, see the cell. §5.2's correction claimed a fresh tab and left the first session's panes alone. The `--json` report named the wrong pane: **#99** |
| 10 — a large multi-line prompt | **pass** — 6311 bytes, 120 lines, 22 blank lines, headless `--prompt -` into an already-trusted directory. Delivered **and submitted**: the whole payload above the `❯` as one turn, the agent's answer after it, the input buffer empty. #73's 0.8.2 silent failure does not reproduce on 0.9.0, so the floor bump is the fix. The form half of the cell is still unrun |

**2026-09-09, second sitting — #115's own verification, at `437629d`.** Run
via Route B (the real form in an addressable pane), state dir redirected to a
temp directory so the user's own `recents.json`/`projects.json` were not
written; teardown left no workspace, checkout or process behind. Four launches
into fresh worktrees of one throwaway repo.

- **The mechanism #115 rests on is confirmed, end to end.** An agent sitting
  on the trust dialog reports `agent_status: "blocked"`; the instant the
  dialog is answered `agent get` reports `done` with `interactive_ready:
  true` — which is exactly the verdict `classifyAgent` maps to ready — on the
  *same* agent, with no re-launch. A prompt sent at that point lands as a
  **submitted turn above the `❯` with an empty input buffer** (`❯ Reply with
  exactly one word: pineapple`, then `● pineapple`). That is the issue's own
  pass condition, met.
- **But #115's wait never engaged through the popup, because `agent start`
  did not refuse.** In all three form submits the launch row went `›` → `✓`
  with no `…` in between: herdr returned success while the pane was showing
  the trust dialog. The `agent_not_ready` refusal #115 waits on DID occur,
  twice, but only from probes launching into an ordinary split pane
  (headless `create`, and a hand-driven `herdr agent start`). So on this
  machine the popup's failure lands one step later, on the **prompt** op,
  refused by herdr-draft's own `promptIfReady` guard. #115's first
  implementation did not wait on that path at all; it now waits on both, and
  the prompt-step wait polls the SCREEN rather than herdr's status, because
  herdr is the thing that was wrong about this pane.
- **Worse: that guard is racy, and when it loses, the agent dies and the run
  reports success.** In two of the three form submits the guard did not fire
  — the dialog painted after its read — so `agent prompt` sent the text and
  its trailing Enter answered the **preselected `❯ No, exit`**. The agent
  exited, and the pipeline reported a clean create: state persisted, no
  `unsent-prompt.txt`, no failure screen. This is #94's failure mode
  returning through a timing window rather than through a classifier gap, and
  it is independent of #115 (the wait is not in that path). Read the pane
  before believing a success. **#116**
- **And the wait was not enough on its own.** With the prompt-step wait in
  place a fourth submit reached `… claude`, the footer read `answer the
  dialog in the pane — your prompt goes out as soon as you do`, and the wait
  resolved the moment the dialog was answered — then the prompt failed
  anyway. Reproduced by hand: 0.5s after the dialog clears `agent get`
  reports `idle` with `interactive_ready: true`, but Claude Code is not yet
  accepting input, so herdr types, observes nothing, and returns
  `agent_prompt_stalled` — leaving the pane with an **empty input buffer and
  no turn**. A stalled send is now retried once, after a short settle and
  through the same guard; a send whose confirmation merely timed out is
  still never retried, since that one means the agent was working.
- **Fifth submit, with everything in place: pass, and it is Cell 1's own
  pass condition.** `… claude  final-check` with the footer instruction; the
  dialog answered in the pane and **nothing pressed in the popup**; the
  launch row went `✓`, the prompt row `working…`, the popup persisted its
  state and closed itself. The pane then held `❯ Reply with exactly one
  word: pineapple` as a **submitted turn**, `● pineapple` under it, and an
  **empty input buffer** — one copy of the prompt, not two. That is #115
  met, read off the pane rather than the report.
- Not a finding: `agent read --source detection` on a blocked pane does carry
  both `dialog.go` signatures verbatim, so the guard's *matching* is sound.
  What it cannot do is match a screen that has not painted yet.

**#116, fixed on 2026-09-10 — and the fix has two rules because the defect
has two halves.** The sitting above is the whole of its evidence, so it is
recorded here rather than in a new entry. Before the send, an empty read is
now a refusal in its own right (`errPaneUnpainted`): the guard's rule used
to be negative — "no signature matched, therefore safe" — and a screen with
nothing on it answered it the wrong way. In the popup that refusal enters
#115's prompt-step wait, which had the same hole and no longer treats a
blank poll as a cleared one; headless `create`, with nobody to wait for,
refuses outright with the prompt saved. After the send, the pane is read
again and the create is only reported clean once it says the prompt landed
— an agent that has stopped answering, or a dialog still up with no trace of
the prompt on it, is a failure. **It never resends** (#108): the second read
decides what to tell the user, never whether to type again.

Prevention alone would not have been enough, and this is the reasoning worth
keeping: narrowing the window the guard misses does not make the report
honest when it is missed anyway, and only the post-send read catches a race
nobody predicted. The reverse is also true — the post-send read cannot stop
the agent from dying, only stop the popup from calling that a success.

Two limits to know before trusting it. The trace test is skipped for a
prompt over ~400 characters or 10 lines, because Cell 10 already measured
that a large prompt is wrapped and scrolled by the agent's own TUI and
appears verbatim nowhere; those fall back to the "agent stopped answering"
verdict alone. And "Enter to confirm" is Claude Code's own permission-prompt
footer, so a *delivered* prompt whose agent immediately asks to act on it
sits on a matching screen — which is why the check looks for the prompt
rather than for the absence of a dialog.

**2026-09-10, third sitting — #116's own verification, at `08b02b0`.** Route
B, state dir redirected to `/var/tmp` (the user's real `recents.json` /
`projects.json` / `last-used.json` were confirmed untouched afterwards, by
mtime). Two form submits and four hand-driven probe launches into one
throwaway repo plus a second untrusted one. Teardown left no workspace,
checkout, or process behind.

- **Both form submits pass, read off the pane.** The first met the trust
  dialog: the launch row went `…` with the footer instruction, the dialog
  was answered *in the agent's pane with nothing pressed in the popup*, and
  the pane then held `❯ Reply with exactly one word: pineapple` as a
  submitted turn, `● pineapple` under it, and an **empty input buffer** —
  one copy, not two. The second ran with no dialog (Claude Code trusts a
  sibling worktree of a repo it already trusts, which is worth knowing when
  planning a cell) and closed in 19s, also clean. So the new pre-send
  refusal does not block a good send and the post-send read does not invent
  a failure — which was the fix's own biggest risk.
- **The startup window was measured, and it is NOT blank.** This is the
  finding, and it corrects the assumption the pre-send rule was built on.
  Polling `agent read --source detection` as fast as it answers, through
  four launches into an untrusted directory:

  | after `agent start` | read | screen |
  |---|---|---|
  | 0–300/500 ms | **fails** | — (already refused before #116) |
  | ~590 ms | ok, 20 non-space | `\x1b[200~claude\x1b[201~` — the shell's echo of the command herdr typed |
  | ~630 ms | ok, 29 non-space | that, plus `❯ claude` |
  | ~1889 ms | ok, 138 non-space | plus clauth's account banner |
  | ~2 s | ok, 542 non-space | the dialog, both signatures present |

  So there is a **~1.4 s window where the read succeeds, carries real text,
  and matches no signature**. `errPaneUnpainted` does not close it — only a
  strictly empty read is refused, and this is not one. What covers that
  window is the post-send check, which is why taking both directions
  mattered for a reason that was not visible when the choice was made.
  `TestPromptGuardOnTheMeasuredStartupWindow` pins these exact bytes so the
  hole stays documented rather than assumed closed.
- **The post-send check's strongest evidence is confirmed.** A pane whose
  agent has exited answers `agent read` with `agent_not_found` — checked
  directly by cancelling a probe agent's dialog and reading its pane. That
  is the verdict `confirmPromptLanded` draws, and it needs no heuristic.
- **A detection screen can carry the dialog's tail without its heading.**
  Reading a blocked probe pane returned the options and `Enter to confirm`
  with `Quick safety check` scrolled out of the region entirely. Both
  `dialog.go` signatures earn their keep; a guard matching only the heading
  would have missed that pane.
- Not reproduced this sitting: `agent start` returning **ok** with the trust
  dialog pending. It refused (`agent_not_ready`) in all five untrusted
  launches, where the 2026-09-09 sitting saw it return ok in two of three.
  The race is real and recorded, but which side it lands on varies by
  machine or load, so a run that does not reproduce it is not evidence it
  is fixed.

Four findings from the first sitting, all live-only against a green
`just check`:

- **#72** (fixed) — the quoted-`extra_args` workaround this repo recommended
  *broke* Path A, because `agent start` execs an argv vector while `pane run`
  types into a shell. No single config value works on both paths, so the
  quoting moved into `CLIRunner.PaneRun`. Confirmed live during cell 10: with
  the inner quotes removed from `config.toml`, a Path A launch reported
  `✻ Opus 5 1M` and `eff_xhigh` in the pane's own status line rather than
  refusing the model id.
- **#99** (fixed, filed here) — `create`'s reported `pane_id`/`pane=` named
  the space's pane, not the pane the agent is in, whenever a placement op or
  a reuse correction moved it. The report now reads `ExecResult.AgentAt`,
  widened from a pane id to the whole workspace/tab/pane because a placement
  op moves all three; the space keeps its own `space_*` keys, and the
  keep-or-clean line still names the SPACE (Cell 8). **Re-checked live** on
  the divergent combination (worktree on + `tab here`, which is the only one
  that separates all three): the report named `w1`/`w1:t2`/`w1:p2` — the
  invoking workspace's new tab, matching the `--pane w1:p2` in herdr's own
  `agent start` error — with the worktree's own `w3`/`w3:t1`/`w3:p1` under
  `space_*`. Before the fix it named `w3:p1`.
- **#93** (open) — the missing `from .herdr-draft.toml` provenance line.
- **#94** (fixed) — Path B killed the agent it had just launched.

Two non-findings worth not rediscovering: the surplus pane in a worktree's
workspace is `persiyanov.reviewr`'s `worktree.created` hook, not ours; and
`pgrep -f herdr-draft` self-matches its own shell. Both are now written into
the cells above.

Also confirmed incidentally: `esc` cancels without creating, `⌃S` submits from
any row, a failed create writes **no** state (Cell 8's failure left
`last-used.json` untouched), and the `label in use` / `branch & label in use`
verdicts fire live against a real session list.

Teardown was clean — no stray workspaces, no orphaned process
(`pgrep -x herdr-draft` → 0), checkouts and their per-repo parents removed,
probe session deleted. The run's own litter in the user's state
(`last-used.json`, and three paths each in `projects.json`/`recents.json`) was
reverted by hand afterwards; a smoke pass writes real state, so budget for
that or accept it.

### herdr 0.9.0 — 2026-09-15

**Cell 12's first run, at `4d5fdeb`** — herdr 0.9.0, clauth 0.15.1, Route A0.
Both steps passed. The detail, and the two sentences the run corrected in the
cell, are recorded in Cell 12 itself. Teardown was clean — workspace closed,
session stopped and deleted, no process left carrying the marker, no orphaned
`herdr-draft` — and because the state directory was a scratch one, the run
left nothing in the real plugin state to revert.
