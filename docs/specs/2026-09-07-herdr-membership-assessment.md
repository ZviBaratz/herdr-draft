<!--
Committed 2026-09-08, unchanged from the working document written
2026-09-07. It is the evidence base for
`2026-09-08-herdr-8.1-8.2-correction.md`, which retracts two claims the
placement spec published as herdr bugs -- and a retraction whose evidence
lives only on one laptop is not much of a retraction.

Kept verbatim rather than summarised: the reproductions are the part that
makes the correction checkable, and they do not survive compression. Its
herdr line citations are relative to herdr at `b1ff4582`
(b1ff4582e9688f52ffb943cfa8bee4871ae122e4), the same commit the rest of
this repository pins, so they resolve at
https://github.com/herdrdev/herdr/blob/b1ff4582/ -- the two absolute paths
in its Context section describe where the work was done, not where a
reader should look.
-->

# herdr §8.1 / §8.2 assessment — recommendation

## Context

`herdr-draft` (`/home/zvi/Projects/herdr-draft`) wrote up four proposed herdr-side
changes in `docs/specs/2026-09-03-placement-and-pane-ownership-design.md` §8, two of
them claimed as bugs in herdr independent of herdr-draft. This session evaluated
those claims against `/home/zvi/Projects/herdr` at HEAD `b1ff4582` (tag
`preview-2026-08-31-b1ff4582e968`, `Cargo.toml` version `0.8.2`), reproduced them
live, and checked them against herdr's own tests, docs, and issue history.

**Verdict: report nothing.** Neither §8.1 nor §8.2 survives as a filable bug, and
§8.3/§8.4 are enhancements this account is barred from filing as issues. Details
and the reasoning per finding are below; this document is the deliverable.


---

## What I verified, and how

### Environment and fidelity of the test

- Checkout HEAD `b1ff4582`; `git status` clean before and after.
- **I could not build HEAD.** `cargo build --release` fails at `build.rs:80` —
  `failed to execute zig build for vendored libghostty-vt: NotFound`. No `zig` on
  this machine, and herdr pins Zig 0.15.2. I did not install a toolchain.
- So the live reproduction ran against the **installed `herdr 0.8.2` binary**, on a
  disposable headless session (`herdr --session probe-wt-… server`), never the
  user's live session.
- Fidelity of that substitution, established by diff rather than assumed:
  - `src/app/api/panes.rs` is **byte-identical between `v0.8.2` and HEAD**
    (`git diff --numstat v0.8.2..HEAD` returns nothing for it). Every §8.1
    behaviour I observed is HEAD behaviour.
  - `src/app/api/worktrees/deferred.rs` and `src/app/api/worktrees.rs` differ from
    v0.8.2 **only** by `trust_repository` plumbing and rustfmt churn. The reuse
    branch (`deferred.rs:406-435`) and `open_workspace_idx_for_checkout`
    (`worktrees.rs:596-624`) are semantically unchanged. §8.2 observations carry.
  - **One path does not carry:** `workspace close`'s `workspace_group_close_required`
    guard landed in `d79fd746` *after* v0.8.2. The installed binary cascade-closes a
    group on `workspace close <parent>` with no error; HEAD refuses. I used HEAD
    source, not the binary, for anything involving that guard.

### §8.1 — `pane move` and worktree membership

Confirmed at HEAD, as source:

- `handle_pane_move` removes an emptied cross-workspace source outright with no
  guard — `src/app/api/panes.rs:844-865`.
- `previous_worktree_space` is snapshotted at `panes.rs:662` and restored only in
  `recover_failed_pane_move` (`panes.rs:1070`). A successful move drops it.
- `close_pane`'s guard exists — `panes.rs:1562-1570`, error `confirmation_required`,
  "closing this pane would close a worktree group".
- The pinning test is real and deliberate:
  `api_pane_move_to_new_workspace_closes_empty_source_workspace`
  (`panes.rs:2983-3060`), whose helper `app_with_linked_worktree`
  (`panes.rs:2254-2273`) sets `worktree_space` with `is_linked_worktree: true`
  specifically so the assertion covers a worktree workspace.

**Where the write-up is wrong.** It says `close_pane` "refuses exactly this". It
does not. The guard runs through `confirm_implicit_worktree_group_close`
(`src/app/actions.rs:2015-2019`) → `workspace_close_would_close_worktree_group`
(`:1990`) → `workspace_close_indices` (`:1970`), and `workspace_close_indices`
**filters on `!space.is_linked_worktree`** (`actions.rs:1974`). The guard therefore
fires only when the target is the **primary repo workspace of a group with ≥2
members** — i.e. only when the close would *cascade* into sibling workspaces. It
never fires for a linked worktree workspace, which is the workspace herdr-draft
actually cares about.

Reproduced live (herdr 0.8.2, `panes.rs` identical at HEAD), disposable session:

```
workspace create --cwd <repo> --label parent      -> w6 (worktree, is_linked=false)
worktree  create --cwd <repo> --branch probe/c    -> w7 (worktree, is_linked=true)

pane close w6:p1   (primary's only pane)  -> ERROR confirmation_required
                                             "closing this pane would close a worktree group"
pane close w7:p2   (linked child's only pane) -> {"type":"ok"}, w7 gone, NO confirmation
```

and, on an equivalent group:

```
pane move w3:p1 --new-workspace --label moved -> ok, closed_workspace_id: "w3"
   w3 (primary) gone with its worktree provenance; w5 created with none;
   w4 (linked child) SURVIVES — pane move removes exactly one workspace, no cascade.
```

So the true, narrow statement is: **`pane move` is the one close-ish path not covered
by the confirmation that `pane close` / `tab close` / (at HEAD) `workspace close` apply
to a primary repo workspace.** For a linked worktree workspace — the case §8.1 is
written about — `pane close` gives no protection either, so there is no asymmetry
there at all.

Consequence of the primary case, measured rather than argued: `worktree list` still
resolves correctly afterwards, because it enumerates from git
(`worktrees.rs:61-71` → `list_existing_worktrees`) and re-derives `open_workspace_id`
from `open_workspace_idx_for_checkout`. In my run, `worktree list --workspace w4`
(the orphan) still reported the repo root and correctly named the moved pane's new
workspace `w5` as the primary's `open_workspace_id`. Nothing is destroyed on disk
and no process dies. What is actually lost is the sidebar grouping: the parent row
requires a group member with `!is_linked_worktree` (`src/ui/sidebar.rs:348-360`,
`:395-406`), so the children render flat until some repo-root workspace re-acquires
provenance.

**That exact downstream symptom is a closed issue.** herdrdev/herdr#3620,
"Worktrees render flat when the repo-root workspace is opened after them"
(2026-09-04) — closed `not_planned`, relabelled `feature-req`, with:

> "Worktree grouping is explicit provenance attached by `worktree create` or
> `worktree open`. … Automatically joining generic or duplicate-cwd workspaces to an
> existing group would change workspace/group behavior, so this is a feature request
> rather than a failure of the current contract."

**And the framing that does survive is already an open issue.** herdrdev/herdr#1750,
"Close confirmation is inconsistent across close paths" (open, 3 👍, untouched since
2026-07-22) reports precisely this: one path closes a workspace with a prompt,
another with the same effect closes it silently. §8.1 is one more path on that list,
not a new defect.

### §8.2 — `worktree create` stamping a workspace it did not open

Confirmed at HEAD, as source and live:

- Reuse branch and unconditional stamping: `deferred.rs:406-435`
  (`mark_worktree_membership(..., !created_workspace)`), `set_worktree_membership`
  writing `workspace.worktree_space` at `worktrees.rs:474-496`.
- Matching is as wide as claimed, and wider: `open_workspace_idx_for_checkout`
  (`worktrees.rs:596-624`) matches on stored `worktree_space.checkout_path`,
  `git_space().checkout_key`, the resolved identity cwd's git space, or the
  identity cwd itself.
- `worktree remove --workspace <id>` closes the whole workspace:
  `deferred.rs:562` → `close_removed_linked_worktree_workspace`
  (`src/app/worktrees.rs:976-1003`) → `close_selected_workspace`.

Reproduced live, end to end:

```
workspace create --cwd <empty dir> --label my-notes   -> w8, "worktree": null
tab create --workspace w8 --cwd $HOME --label unrelated -> w8:t2   (2 tabs)

worktree create --workspace w6 --branch probe/d --path <that same empty dir>
   -> reuses w8 (no new workspace), returns root_pane w8:p1,
      w8 now carries worktree provenance is_linked_worktree: true

worktree remove --workspace w8
   -> ok; w8 and BOTH tabs are gone, including the unrelated one
```

So the mechanism and the consequence are both real.

**But it is herdr's stated contract, not a bug.** Three independent confirmations,
all in this repo or its issue tracker:

1. **A test pins it.** `api_worktree_open_reuses_already_open_checkout_from_subdirectory`
   (`src/app/api/worktrees.rs:1410-1485`) builds a **plain workspace with no
   `worktree_space`**, whose identity cwd is merely a *subdirectory* of the checkout,
   and asserts at `:1462-1467` that after the call it carries
   `worktree_space().is_linked_worktree == true`. Stamping a pre-existing,
   user-owned, previously-unmarked workspace on the reuse branch is the asserted
   behaviour.
2. **The docs state it.** `docs/next/website/src/content/docs/socket-api.mdx:411`:
   "Worktree commands can emit `workspace.updated` when an existing workspace gains
   or changes worktree provenance." That sentence exists to describe this branch.
   `configuration.mdx:105`: "Choosing an already-open checkout focuses it."
3. **A maintainer said it, three days ago.** The #3620 comment quoted above:
   "Worktree grouping is explicit provenance attached by `worktree create` or
   `worktree open`."

The write-up asks which of two things herdr treats as the invariant — provenance on
the reused workspace, or `worktree list`'s `open_workspace_id` reporting the checkout
as open. herdr has answered: provenance attachment is the contract, and
`open_workspace_id` is derived from it (`worktree_info_for_entry`,
`worktrees.rs:570-572`). Leaving `worktree_space` alone on the reuse branch would
break both halves.

Note also that the create path is not special here: `handle_worktree_open` performs
the identical stamping on its own reuse branch (`worktrees.rs:139-145`), which is
where the pinning test lives. A report that treats this as a `worktree create`
oversight would be describing half of a symmetric, deliberate design.

**And the hazard it points at is already an open issue.** herdrdev/herdr#3214,
"worktree actions resolve a workspace to a different repository" (open, labelled
`maintainer-needed`) is the user-facing form of "a workspace ends up carrying
worktree provenance it should not have."

### §2.7's reachability claim — accepted, and it matters

I did not re-audit the herdr-draft server log, so I take §2.7 on trust: the reuse
branch never fired in fifteen `worktree create` calls over seven days on that
machine, and two of the four trigger rules are blocked upstream by herdr-draft's own
submit gate. My live reproduction of §8.2 required passing `--path` at the exact
directory a workspace was sitting in — a user-directed action, not an accident.
Without `--path`, herdr picks `<worktrees.directory>/<repo>/<branch-slug>`, and the
reuse branch fires only if a workspace happens to sit exactly there.

That is the difference between a latent mechanism and an observed failure, and it is
load-bearing for the filing decision.

---

## What I could not verify

- **HEAD binary behaviour.** No Zig toolchain; the build fails. Everything live was
  0.8.2, with the diff evidence above establishing where that is and is not a
  faithful proxy. The one place it is not — `workspace close`'s group guard — I
  handled from source only.
- **The TUI rendering claim.** I asserted flat-rendering from
  `src/ui/sidebar.rs:348-406` and from #3620's independent report, not from a
  screenshot; I drove herdr headlessly.
- **herdr-draft's server log.** Not re-read; §2.7 accepted as written.
- **`§8.1` fix (b)'s open question.** The write-up's own note — that carrying
  membership to the destination needs a documented answer when the destination
  already has one — I did not attempt to resolve. It is herdr's call and moot
  under this recommendation.

---

## Recommendation

### §8.1 — **report nothing.**

Three reasons, in order of weight:

1. **It is a duplicate of an open issue.** #1750 "Close confirmation is inconsistent
   across close paths" is exactly the surviving framing, open and unaddressed with
   three upvotes. herdr's `CONTRIBUTING.md` / `.github/ISSUE_TEMPLATE/bug.yml` and
   this repo's `CLAUDE.md` both forbid an agent filing a duplicate.
2. **The write-up's stated defect is not the real one.** "close_pane refuses exactly
   this" is false for the linked-worktree case that motivated it — I reproduced
   `pane close` closing a linked worktree workspace silently. The residual true
   claim is narrower (primary repo workspace only) and materially less severe than
   what the guard protects against: `pane move` removes one workspace, it does not
   cascade into siblings, so the harm behind #2874/#3206 does not arise.
3. **The behaviour is pinned as intended.** `api_pane_move_to_new_workspace_closes_empty_source_workspace`
   uses a worktree workspace on purpose. A patch is a design change to herdr's close
   semantics, not a fix — and this account cannot open one anyway (below).

If the trade ever matters to herdr-draft in practice, the right channel is a comment
on **#1750** adding `pane move` to the list of paths, or a Discussions post — not a
new issue. I did not post either; that is a decision for the human.

### §8.2 — **report nothing.**

The behaviour is documented (`socket-api.mdx:411`), tested
(`worktrees.rs:1462-1467`), symmetric across `worktree create` and `worktree open`,
and stated as contract by a maintainer three days ago on #3620. That makes it a
design disagreement, which the bug template explicitly routes to Discussions. Its
user-visible hazard is already tracked on the open #3214.

### §8.3 / §8.4 — **cannot be filed as issues, and should not be.**

Both are enhancements (`already_open` on `WorktreeCreated`; `worktree create
--no-open` plus a path-addressed `worktree remove`). herdr's issue template routes
feature requests to Discussions and closes them automatically as issues; §8.4 is the
same shape as #3533 and #3620, both closed `feature-req`. §8.4 would genuinely remove
the leftover-pane cost herdr-draft currently discloses, and §8.3 is a two-line exact
signal — so they are worth raising, but in **Discussions**, by the human, in their
own voice.

### Standing constraint that makes "offer a PR" unavailable regardless

`gh auth status` reports the acting account as **`ZviBaratz`**. It is not in
`.github/MAINTAINERS` (`ogulcancelik`, `Pimpmuckl`, `kangal-bot`) and not in
`.github/APPROVED_CONTRIBUTORS`. Under `CLAUDE.md`'s external contributor guardrail
and `CONTRIBUTING.md`, this account may not open an implementation pull request —
unsolicited ones are closed automatically — and may file an issue **only** for a
verified, reproducible, non-duplicate bug, with no root-cause analysis, proposed fix,
or diff in the body. Even had §8.1 or §8.2 survived on the merits, "file an issue and
offer a PR" was never an available option here. (herdr's own `build.rs` prints this
policy as a build warning.)

---

## Draft issue text

**None.** Both bug claims resolve to "do not file", so drafting issue text would be
drafting something that should not be sent. If the human decides to raise §8.1 on
#1750 or §8.3/§8.4 in Discussions, I can draft that text on request — as a comment
or discussion post, in their voice, not as an issue.

---

## Reproduction hygiene

All probing ran in a disposable headless session, never the live one. Afterwards:
`herdr server stop`, `herdr session delete probe-wt-…`, `git worktree remove --force`
on both probe checkouts, probe branches deleted, `git worktree prune`, scratch repo
removed, `~/.herdr/worktrees/repo/` removed. `herdr session list` shows only
`default`; `git status` in `/home/zvi/Projects/herdr` is clean. The failed
`cargo build` left only gitignored `target/` artifacts. Nothing was pushed, filed, or
commented anywhere.
