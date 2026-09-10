# Manual smoke matrix

Not part of CI (v2 spec §14: "manual release smoke stays in
`docs/manual-smoke.md`"). Run it once per release, against the herdr
version(s) you intend to support, before publishing. It exercises the real
popup, the real herdr CLI, and (for Path B) a real clauth profile — nothing
here is mocked.

Ten cells: the original four (Path A/B × worktree on/off), plus three that
cover what v2 added — the headless `create`, the repo-level
`.herdr-draft.toml`, and per-project memory — plus two that cover what the
placement spec added: placement honored under a worktree, and the reuse
path. The tenth is the one the 0.9.0 floor exists for, a large multi-line
prompt (#73); it is also the only cell that spends real model quota, so it
is the one to decide about deliberately rather than by default.

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
[`main.rs`](https://github.com/herdrdev/herdr/blob/b1ff4582/src/main.rs#L588)
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
7. `⇥` to **placement**. It is now LIVE even with the worktree on
   (placement spec §6.1) — `←→` through all three chips. Confirm each
   chip's row label changes (`new space` / `tab here` / `split here`) and
   that the panel's hint follows it (`the worktree opens as its own
   space` / `the agent opens a tab here, on the worktree's checkout` /
   `the agent splits this pane, onto the worktree's checkout`), with `the
   worktree also keeps a space of its own` appearing under the panel's
   hint for the two non-default chips only. Leave it on whichever chip you
   want to actually submit with — this is no longer a read-only row.
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

With a `here` placement herdr has already moved you to the agent's pane, so
the dialog is in front of you. Answer it (`↓`, `↵` for "Yes, I trust this
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
  remove undoes everything this create made
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
> applies whenever the wait ends in the failure screen. With a `here`
> placement the agent's tab or split is created with `Focus: true`
> (`placementOp`, build.go) — deliberately, since that is where the agent
> lands — so herdr moves you to the new pane while the popup is still up
> with its keep-or-remove choice unanswered. On the 2026-09-09 run that
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
| `herdr-draft create --title x --placement nowhere` | `unknown --placement "nowhere": expected new-space, tab-here or split-here`, then the usage block → **exit 2** |
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
clean` acts on (#99). This cell runs `--worktree` with the default
placement, where the two are the same triple twice over — Cell 8's
combination is the one that separates them, and the one to read this
against.
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

### Cell 8 — worktree on, placement tab here

**Setup:** fresh throwaway repo, worktree on (the default), placement
`tab here`.

**Steps:** same walk as Cell 1 through step 6 (worktree on), then step 7
(placement) left on `tab here`, then submit.

**Expected:** TWO new containers, not one — the worktree's own new
workspace (an idle shell, no agent) and a new TAB in the workspace you
were ALREADY in, with the agent running there instead. `herdr[S]
workspace list` should show one more workspace for the worktree itself,
**plus** one more still for the origin repo if it was not already open
when you started this cell: `worktree create` opens it too, as
`ensure_source_parent_membership`'s side effect (design doc §2.7;
`plan.Clean`'s doc comment records the same thing, and deliberately
leaves that workspace alone). Against the fresh throwaway repo this
cell's own setup calls for, that means **two** new workspaces, not one —
reading it as one would mistake this cell's own setup for a failure.

**Count panes with other plugins in mind.** Plugin install/link state is
global, so a Route A0 session inherits every plugin installed for your normal
one — and three of them subscribe to `worktree.created`. One,
`persiyanov.reviewr`, answers it by *opening a pane*
(`[[events]] on = "worktree.created"`, `command = ["bash", "herdr/pane.sh",
"auto-open"]`), so the worktree's own workspace holds **two** idle shells
rather than the one this cell describes. Confirmed independent of
herdr-draft: a bare `herdr worktree create` reproduces it, and the plan only
ever issues the two calls its progress stack shows. Check
`herdr[S] plugin list` before treating a surplus container as a finding, and
count what changed rather than the absolute total.
`herdr[S] tab list --workspace <your original workspace>` should show one
more tab than before, and that tab's pane is running the agent. This is
placement spec §5.3's disclosed cost — the idle shell in the worktree's
own workspace is not a bug.

Run this cell **headlessly** (Route A0) and expect step 3 (starting the
agent) to fail with `agent_not_ready` **every time you run this cell**, on
every machine — not just the first. #115 changed the popup and deliberately
left `create` alone (its decision 2), so this cell's expected result is
unchanged; the popup's version of the same moment is Cell 1's pause. If you
walk this cell through the popup instead, expect Cell 1's waiting row here
too, and check that the agent lands in the invoking workspace rather than
the worktree's before you answer the dialog. The
worktree checkout is created by the run itself, so it is always a path
Claude Code has never seen and there is nothing to pre-trust in advance
(see "Claude Code's trust prompt is a precondition"). Its first-run trust
prompt blocks it, and herdr's own `agent start` refuses outright rather
than herdr-draft sending a prompt into it — the same class of blocking
dialog `internal/plan/dialog.go` exists to catch, just refused one layer
higher up here. That failure does not invalidate the cell: confirm
instead that the two containers above exist, that the agent's pane is in
the invoking workspace rather than the worktree's, and that its cwd is
the worktree checkout; `--on-failure keep`'s report naming the SPACE
(not an agent pane) is the same evidence read a different way.

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
# then create the SAME branch again, placement new-space so the worktree op
# is itself the agent's pane -- the only shape in which the correction fires
herdr-draft create --project <repo> --title "<a different title>" \
    --branch <same branch> --worktree --placement new-space --json
```

`--placement new-space` is load-bearing: with `tab here` or `split here` a
placement op follows and *it* decides where the agent goes, so `plan.Execute`
deliberately skips the claim (`exec.go`, "a claim in the reused workspace
would be litter").

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
- Not a finding: `agent read --source detection` on a blocked pane does carry
  both `dialog.go` signatures verbatim, so the guard's *matching* is sound.
  What it cannot do is match a screen that has not painted yet.

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
