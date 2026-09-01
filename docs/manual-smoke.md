# Manual smoke matrix

Not part of CI (spec §15: "Manual smoke ... throwaway named herdr session;
run the full matrix of Path A/B × worktree on/off once per release"). Run
this once per release, against the herdr version(s) you intend to support,
before publishing. It exercises the real popup, the real herdr CLI, and (for
Path B) a real clauth profile — nothing here is mocked.

## Before you start

- Build the binary you're testing: `go build -o bin/herdr-draft
  ./cmd/herdr-draft` from a clean `git status` (or note exactly which
  uncommitted changes you're testing).
- Confirm `herdr --version` and, for Path B, `clauth --version`. Record both
  in your smoke notes — this matrix's whole point is pinning "what actually
  works against what."
- Use the project's `herdr-throwaway-repro` skill/method throughout: every
  scenario below runs inside a **disposable named herdr session**, never
  the default session. If you're driving this by hand rather than through
  an agent, replicate the same isolation:

  1. From your normal herdr session, split off a new outer pane with a
     scratch cwd (e.g. `/var/tmp/herdr-draft-smoke`). Record its pane id —
     this is the only pane cleanup may close.
  2. Start a disposable session in it, clearing inherited socket/session
     env first:

     ```bash
     env -u HERDR_SOCKET_PATH -u HERDR_CLIENT_SOCKET_PATH -u HERDR_SESSION \
         -u HERDR_WORKSPACE_ID -u HERDR_TAB_ID -u HERDR_PANE_ID \
         herdr --session repro-draft-smoke-$(date +%s)
     ```

  3. Drive every command below with that session name pinned and every
     inherited override cleared:

     ```bash
     env -u HERDR_SOCKET_PATH -u HERDR_CLIENT_SOCKET_PATH \
         -u HERDR_WORKSPACE_ID -u HERDR_TAB_ID -u HERDR_PANE_ID \
         HERDR_SESSION=<your-session-name> herdr <command>
     ```

     (elided below as `herdr[S] <command>` for brevity — substitute the
     full prefix above.)
  4. Make sure the `draft` plugin is linked and visible in the disposable
     session before starting: `herdr[S] plugin list` — plugin
     install/link state is global, so a plugin linked from your normal
     session is already visible here.

- Each scenario needs its own throwaway git repo under `/var/tmp` (one
  commit is enough; `git init && git commit --allow-empty -m init` in a
  fresh directory). Do not reuse a scenario's repo for the next one —
  branch/label collisions from a previous run will change what you're
  actually testing.
- For Path B, pick a real clauth profile you're willing to spend a launch
  on: `clauth status --json` and choose (e.g. the lowest-utilization one).

## The matrix

Four cells: Path A (no account pin, or non-claude kind) × Path B (pinned
clauth profile, claude only), each × worktree on/off. Run all four every
release.

Drive the popup by opening it for real and sending input into the **outer
host pane** (the popup itself has no independent pane id to target
directly):

```bash
herdr[S] plugin pane open --plugin draft --entrypoint open
herdr[S] pane send-text <outer-pane-id> "<text>"
herdr[S] pane send-keys <outer-pane-id> <key>
herdr[S] pane read <outer-pane-id>          # to confirm state before proceeding
```

### Cell 1 — Path A, worktree on

**Setup:** fresh throwaway repo, e.g. `/var/tmp/herdr-draft-smoke-a-wt`.

**Steps:**

1. Open the popup. Leave Linear issue on `None (manual entry)` (or pick a
   real issue if Linear is configured and you want to smoke that path too).
2. Project: type the throwaway repo path, Tab. Confirm it's recognized as a
   git repo (Base picker populates with `HEAD (<branch>)`).
3. Title: type a short distinctive title (e.g. `smoke a wt`), Tab. Confirm
   Branch auto-seeds as `<branch_prefix><sanitized-title>`.
4. Worktree: confirm **On** (default, unless your config overrides it —
   toggle it on explicitly if it isn't). Leave Branch/Base at their seeded
   defaults.
5. Placement: confirm it's inert (worktree on always makes its own space —
   spec §6 field 5).
6. Agent: leave on the default favorite (`claude` unless your config
   changes it).
7. Account: leave on `Active` — **do not pin a profile**. This is what
   makes it Path A even when clauth is configured.
8. Prompt: optionally type a short prompt.
9. Advance to Create, submit.

**Expected:** progress lines in order: `creating worktree… ✓` →
`starting agent… ✓` → (if you typed a prompt) `sending prompt…`, then the
popup closes. `herdr[S] workspace list` shows a new workspace grouped with
the throwaway repo's origin workspace (both are created by `worktree
create` — see teardown). `herdr[S] pane list` shows the new pane's agent as
`claude` (or whatever kind you picked) once detection completes
(`herdr[S] agent get <pane-id>`).

**Expected on a fresh worktree with a prompt (normal, not a bug):** if you
typed a prompt and this is Claude Code's first run in a brand-new worktree
directory, Claude Code shows its own first-run trust dialog. herdr-draft's
prompt-delivery dialog guard recognizes this and refuses to send the
prompt automatically — you'll see `sending prompt… ✗ agent is waiting on a
dialog ("..." ) -- prompt not sent`, then `Step failed — choose how to
proceed: k keep · c clean` with the prompt text shown for manual paste.
This is documented behavior (README's "Known limitations"), not a smoke
failure — press `k` to keep the session, confirm the agent process is
still alive (`herdr[S] pane process-info --pane <pane-id>`, should still
show `claude`), and move on.

**Teardown:** `herdr[S] worktree remove --workspace <worktree-workspace-id>`
(only removes cleanly when the checkout has no commits beyond base — true
here since nothing was committed), then `herdr[S] workspace close
<origin-workspace-id>` for the second, non-worktree workspace `worktree
create` always opens alongside it. Delete the throwaway repo directory.

### Cell 2 — Path A, worktree off

**Setup:** fresh throwaway repo, e.g. `/var/tmp/herdr-draft-smoke-a-nowt`.

**Steps:** same as Cell 1, except:

4. Worktree: toggle **Off**.
5. Placement: now active (no longer inert) — exercise at least one of
   `new space` / `tab here` / `split here`; rotate which one across
   releases so all three get covered eventually.
6–9. Same as Cell 1 (Agent default, Account unpinned, optional prompt,
   submit).

**Expected:** progress lines: `creating workspace… ✓` (or `creating tab…
✓` / `splitting pane… ✓`, matching whichever placement you picked) →
`starting agent… ✓` → prompt line as in Cell 1. No worktree, no second
origin-repo workspace — `herdr[S] workspace list` /
`herdr[S] tab list` / `herdr[S] pane list` should show exactly one new
container matching the placement you chose.

**Teardown:** close whatever was created (`herdr[S] workspace close`,
`tab close`, or the pane simply exits) — no `worktree remove` needed since
there's no worktree. Delete the throwaway repo directory.

### Cell 3 — Path B, worktree on

**Setup:** fresh throwaway repo, e.g. `/var/tmp/herdr-draft-smoke-b-wt`.
Requires clauth configured with ≥2 profiles (otherwise the Account field
won't render at all — see README's config reference).

**Steps:** same as Cell 1 through step 6 (Agent must be `claude` — Path B
is claude-only), then:

7. Account: pick a specific profile from the list (not `Active`). This is
   what makes it Path B.
8–9. Same as Cell 1.

**Expected:** progress lines: `creating worktree… ✓` →
`launching claude via clauth… ✓` → `waiting for agent detection… ✓` →
prompt line as in Cell 1 (including the same first-run-dialog caveat).
`herdr[S] pane list` on the launched pane should show `agent: "claude"`
and `tokens.clauth: "<the profile you picked>"`.
`herdr[S] pane process-info --pane <pane-id>` should show **both**
`clauth` (parent) and `claude` (child) processes running.

**Teardown:** same as Cell 1.

### Cell 4 — Path B, worktree off

**Setup:** fresh throwaway repo, e.g. `/var/tmp/herdr-draft-smoke-b-nowt`.

**Steps:** combine Cell 2 (worktree off, placement) with Cell 3 (Agent
`claude`, Account pinned to a specific profile).

**Expected:** progress lines: placement-creation line (as Cell 2) →
`launching claude via clauth… ✓` → `waiting for agent detection… ✓` →
prompt line. Same `pane list`/`process-info` checks as Cell 3.

**Teardown:** same as Cell 2.

## After the matrix

- Confirm no stray panes, workspaces, tabs, or worktree checkouts remain:
  `herdr[S] workspace list` should be back to just the disposable
  session's root workspace.
- Stop and delete the disposable session; confirm it's gone from
  `herdr session list`.
- Confirm the outer host pane is back at a bare shell prompt, then close
  only that pane.
- Delete every throwaway repo directory under `/var/tmp` this run created,
  and any now-empty parent directories left under `~/.herdr/worktrees/`
  (`worktree remove` only deletes the per-branch checkout, not the
  per-repo parent directory).
- Record the herdr and clauth versions tested, which cells passed as
  expected, and anything that deviated from "Expected" above (including
  which known-limitation caveats you actually hit) alongside the release.
