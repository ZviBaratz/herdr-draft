# herdr-draft — placement under a worktree, and who owns the agent's pane

- **Date:** 2026-09-03
- **Status:** design approved 2026-09-03 (§5 through §8); implementation
  not started. §11's taste-call items (1-5, 7) carry recommendations and are
  not blockers; item 6 (the incident's cause) is resolved.
- **Supersedes:**
  - `internal/plan/build.go:19-21` — the `Placement` type's doc sentence
    "It is ignored when `Input.UseWorktree` is set -- worktree creation
    always opens a new workspace regardless of Placement."
  - `internal/plan/build.go:159-162` — `topologyOp`'s doc sentence
    "Worktree creation always wins over Placement (spec §9): a worktree is
    always a new workspace, regardless of where the form said to place it."
  - v1 spec §6 field 5 (`docs/specs/2026-08-31-herdr-draft-design.md:213-215`)
    — "Active only when Worktree is off … inert with hint `worktree opens as
    its own space` otherwise." (v2 §6's field table restated the same rule
    for the row; both go.)
  - v1 spec §9 step 1 (`…-design.md:301-303`) — "Worktree on: `herdr worktree
    create …` → new workspace id + its initial tab/pane", which asserts an
    ownership the response does not carry.
  - v2 spec §13 (`docs/specs/2026-09-02-herdr-draft-v2-design.md:626-627`) —
    "Only the tab-here and split-here placements need it; a new space or a
    worktree needs neither."
  - `internal/create/resolve.go:358-374` (the headless mirror of the same
    rule) and `internal/create/resolve.go:562-565` (`requireContext`'s
    `UseWorktree` early return).
- **Itemised list of superseded sentences:** §12.
- **Citations.** herdr line numbers are for **v0.8.2** — the version
  `herdr-plugin.toml:4` pins as `min_herdr_version` *and* the version
  installed here (`herdr --version` → `herdr 0.8.2`). The sibling checkout
  `/home/zvi/Projects/herdr` sits at `b1ff4582`, a few commits past the tag,
  which shifts some numbers; where it does, the HEAD line is given in
  parentheses. New comments cite this document as `placement spec §N`.

## 1. Summary

Two reported defects. They look unrelated and share one cause.

1. **There is no way to choose where a session's pane lands.** The
   `placement` row exists but is inert whenever the worktree toggle is on
   (`field_placement.go:92`), and `default_worktree` is `true`
   (`config/config.go:108`), so in every repository that has not said
   otherwise the row is dead on arrival. Real state file, written by the
   incident create itself: `{"kind":"claude","placement":"new-space",
   "worktree":true}`.
2. **A created session landed in a pane that already had a `git diff` on
   screen.** herdr's `worktree create` does not always open a new
   workspace: when any existing workspace already resolves to the target
   checkout path it **reuses** that workspace and returns the root pane of
   whichever tab was active there
   (`app/api/worktrees/deferred.rs:396-414`, `:457-467`; v0.8.2). The
   response carries no flag saying so, so herdr-draft starts the agent in
   a stranger's pane.

**Read §2.6 before §5.2.** herdr's own log refutes that second paragraph as
an account of the reported incident: the create at issue made a **fresh**
workspace and a **freshly spawned** pane, and so did every other one of the
fifteen `worktree create` calls in seven days of log. The reuse branch is
real, reachable and dangerous — it would hand `plan.Clean` a workspace the
user owns, and `Clean` would close it (§5.4) — but it is **latent, not
observed**. §5.2 is retained as a hazard closed for ~20 lines on top of
machinery §5.3 needs anyway, not as the fix for the report.

**The reported symptom's actual cause is now identified, and it is
`tdi.worktree-setup`'s companion pane (§2.7), not herdr-draft or herdr.**
Confirmed with the author: the pane was a distinct one, in the same
workspace as the agent's, that outlived closing the agent's pane and closed
only when the workspace did — which is `pane 101`'s own shape and no other
candidate's. No code change in this document addresses it, and none is
needed; see §2.7's closing paragraph.

The shared cause: **`plan.Build` fuses two decisions into one op.**
`topologyOp` (`build.go:163`) answers both *"make the checkout exist and be
known to herdr"* and *"which pane does the agent run in"*. With
`UseWorktree` set, the first answer eats the second — which is why
Placement has nothing to say (defect 1) and why herdr-draft accepts
whatever pane comes back without checking that it is ours (defect 2).

This document splits them. A plan gains an explicit **agent pane**,
distinct from the **space**, with two reasons to differ: the user asked for
a placement, or the space turned out not to be ours. One mechanism, two
triggers, and the second one is also a safety fix — see §5.4, where the
current keep-or-clean gate would close a workspace the user owns.

Nothing in `internal/defaults` changes. No new config key, no new tier, no
new state file.

## 2. Evidence

Everything below was read from source, log or state file, not inferred. Where
an earlier investigation's characterisation was wrong, §2.5 says so.

§2.1-§2.4 establish what herdr's `worktree create` *can* do. **§2.7
establishes that it did not do it here** — not in the reported incident and
not in any of the fifteen creates on record — so read §2.5 and §2.7 before
treating §2.1 as an account of the reported bug.

### 2.1 The reuse branch, and how it is reached

`handle_api_worktree_add_finished`
(`app/api/worktrees/deferred.rs:343`; HEAD `:354`) runs after the
`git worktree add` subprocess returns:

```rust
let (ws_idx, created_workspace) =
    if let Some(ws_idx) = self.open_workspace_idx_for_checkout(&result.path) {
        if api.focus { self.state.switch_workspace(ws_idx); }
        (ws_idx, false)
    } else {
        match self.create_workspace_with_options(result.path.clone(), api.focus) { … }
    };
```
(v0.8.2 `:396-414`; HEAD `:406-424`.)

`open_workspace_idx_for_checkout` (`app/api/worktrees.rs:569`; HEAD `:596`)
matches on **four** independent rules, any one of which is enough:

1. `ws.worktree_space().checkout_path` canonicalises to the target —
   a workspace herdr already tracks as owning that checkout. This value is
   **persisted in the session file**, so it outlives a restart.
2. `ws.git_space().checkout_key` equals the target.
3. the resolved identity cwd's `git_space_metadata().checkout_key` equals
   the target.
4. `ws.resolved_identity_cwd_from(…)` canonicalises to the target — and
   that function reads **`self.tabs.first()`**'s root pane cwd, falling back
   to the workspace's stored `identity_cwd` (`workspace.rs:1101-1110`), where
   the pane cwd itself falls back to the terminal's *launch* cwd
   (`workspace/tab.rs:562-576`).

Rule 4 is the wide one: a plain workspace with no worktree involvement
whatsoever matches if its first tab's root pane merely sits in that
directory.

**The branch is reachable.** For the git step to have succeeded the path
must be absent or empty, which sounds like it should exclude a match — it
does not. Probe (throwaway, deleted):

```
$ git worktree add <existing EMPTY dir> -b b1
Preparing worktree (new branch 'b1')
HEAD is now at 6466aa3 init
exit=0
$ git worktree add <existing NON-EMPTY dir> -b b2
fatal: '…/wt-nonempty' already exists
exit=128
```

So `git worktree add` succeeds into an existing empty directory, and of
course into an absent one. A workspace left open on a checkout that was
later removed from git — or a pane simply sitting in the directory — is
enough. That is not an exotic state: it is what iterating on the same
session produces.

**Two cheap pre-conditions are already blocked, which narrows the trigger
set precisely.** `runTitleCheck` (`app/async.go:515-529`) computes
`branchExists` via `git.BranchExists` and `labelTaken` via
`workspaceLabelTaken` (`:535`), and `titleDupBlocked = branchExists ||
labelTaken` (`:569`) **blocks submit** (`app/app.go:1028`). So the incident
cannot have been "the branch already existed" or "a workspace already had
that label" — herdr-draft refuses both. What is left is exactly the residual
set: rules 1-4 firing while the branch does not exist and no workspace
carries the title. §6.2 turns on this.

### 2.2 The response carries no signal

`ResponseResult::WorktreeCreated` (`api/schema/response.rs:69-74`, identical
in v0.8.2 and HEAD):

```rust
WorktreeCreated { workspace: WorkspaceInfo, tab: TabInfo,
                  root_pane: PaneInfo, worktree: WorktreeInfo },
```

Its sibling four lines down (`:75-81`) does carry one:

```rust
WorktreeOpened { workspace, tab, root_pane, worktree, already_open: bool },
```

and `handle_api_worktree_open` fills it from the same helper
(`app/api/worktrees.rs:101`, `:160-168`; HEAD `:113`, `:172-181`), as does
`EventData::WorktreeOpened` (`api/schema/events.rs:459-463`).

The boolean the create path needs **already exists in the same function**:
`created_workspace`, bound in the snippet above and used twice
(`deferred.rs:423`'s `!created_workspace`, `:445`'s `if created_workspace`).
Because the create path has exactly two branches, `already_open ==
!created_workspace` is exact, not approximate. §8.3 is a two-line diff.

`WorktreeInfo.open_workspace_id` is not a substitute: on the create path it
is populated unconditionally with the resolved workspace's id
(`worktree_info_for_workspace` → `worktree_info_for_membership(…,
Some(public_workspace_id(ws_idx)))`, `app/api/worktrees.rs:599-602` calling
`:550`), so it is `Some` whether or not the workspace was reused.

### 2.3 What the reused pane actually is

The match is made on `tabs.first()`'s root pane (rule 4 above). The pane
handed back is

```rust
let tab_idx = self.state.workspaces[ws_idx].active_tab;
…root_pane: self.root_pane_info(ws_idx, tab_idx)…
```
(`deferred.rs:457-467`; HEAD `:468-478`), and `root_pane_info`
(`app/creation.rs:411`) returns `tab.root_pane` — the **root pane of the
active tab**.

These are two different tabs. herdr can match on tab 1 and hand back tab
7's root pane. It is also not the *focused* pane: in a split tab, the root
pane is the first leaf of the layout tree, not the one with the cursor. So
the pane herdr-draft types an agent into may be neither the pane that caused
the match nor the pane the user is looking at.

### 2.4 Membership is workspace-scoped, and `pane move` destroys it

`set_worktree_membership` (`app/api/worktrees.rs:449`; HEAD `:475`) writes
`workspace.worktree_space = Some(membership)` — one `Option` per
**workspace**. There is no per-tab or per-pane worktree identity anywhere in
the model. §7.2 shows why that single fact rules out an entire family of
otherwise-attractive designs.

`handle_pane_move` (`app/api/panes.rs:624`) is worse than "not helpful". On
a cross-workspace move that empties the source:

```rust
if source_workspace_empty && cross_workspace {
    self.state.workspaces.remove(source_ws_idx);
    …
}
```
(`:844-845`) — the workspace is removed outright, taking `worktree_space`
with it. The move *does* snapshot `previous_worktree_space`
(`:662`), but only `recover_failed_pane_move` restores it (`:1070`); on a
**successful** move nothing carries it to the destination.

And `close_pane` refuses exactly this:

```rust
if self.state.close_pane_would_close_workspace(ws_idx, pane_id)
    && self.state.confirm_implicit_worktree_group_close(ws_idx) {
    return Err(encode_error(id, "confirmation_required",
        "closing this pane would close a worktree group"));
}
```
(`:1562-1570`). The guard is absent from `handle_pane_move`, and
`api_pane_move_to_new_workspace_closes_empty_source_workspace`
(`panes.rs:2984`) pins the unguarded behaviour as intended. `herdr pane
close` has no `--force` (`cli/pane.rs:1014`), so the asymmetry cannot be
worked around from the client either.

Note the guard is also conditional: `confirm_implicit_worktree_group_close`
(`app/actions.rs:2004`; HEAD `:2015`) is
`self.confirm_close && workspace_close_would_close_worktree_group(ws_idx) &&
begin_workspace_close_confirmation(ws_idx)`, and the middle predicate is
`workspace_close_indices(ws_idx).len() >= 2` (`:1987`; HEAD `:1990`). It
depends on a user setting and on the group having a second member. A design
must not rely on it.

### 2.5 Corrections to, and gaps in, the earlier investigation

The last two rows are not corrections — nothing was claimed about them. They
are the two facts that change the shape of the fix, which is why this section
lists them rather than leaving them to be rediscovered in §5.

| Earlier claim | Verdict |
|---|---|
| reuse logic at `worktrees.rs:~596`, handler at `deferred.rs:~354-360` | **Right on master**, and the handler starts at `:354`; the reuse branch itself is `:406-424` on HEAD and `:396-414` at v0.8.2, where the handler starts at `:343`. The v0.8.2 numbers are the ones that matter. |
| "reuses that workspace (and whatever pane is its active one)" | **Imprecise.** It is the root pane of the active *tab*, and the match is made against a *different* tab (`tabs.first()`). §2.3. |
| `WorktreeCreated` carries no reuse boolean; `WorktreeOpened` does | **Confirmed**, at v0.8.2 and HEAD, `response.rs:69-81`. |
| membership attaches to the workspace, `worktrees.rs:~462-492` | **Confirmed.** v0.8.2 `:437-467`; HEAD `:462-494`. |
| `pane move` would orphan tracking; `pane close` has a guard `pane move` may lack | **Confirmed and worse than stated.** No guard at all on move, the snapshot is failure-only, and the pinning test exists. §2.4. |
| workspace reuse is what put a `git diff` in the reported session's pane | **Refuted for this incident and for every create on record.** `workspace.create workspace_id="wS" pane_id=100` fires 53 ms into the create, so `created_workspace` was `true` and the pane was brand new; all fifteen `worktree create` calls in seven days of `herdr-server.log` behave the same way. §2.7. The mechanism is real and worth closing anyway — §2.7's last paragraph — but the reported symptom's actual cause is `tdi.worktree-setup`'s companion pane, resolved with the author, §2.7/§11 item 6. |
| the fix is to add `already_open` to the response and have the executor react | **Right diagnosis of the mechanism, wrong dependency, and not the reported bug.** The reaction is right; making it *wait on herdr* is not. Installed herdr is 0.8.2, `min_herdr_version` is 0.8.2, and this repo already has a documented scar about depending on a post-0.8.2 flag (`--trust-repository`, CLAUDE.md, `app/app.go:1165-1174`). §3 shows the signal is obtainable today, exactly. |
| park Placement-under-worktree as out of scope | **Disagreed.** §5.3 delivers it now. §7 says what it costs and §8.4 says what would make it free. |
| — (not reached) | **Missing, and the most serious consequence.** `plan.Clean` is `r.WorktreeRemove(ctx, created.WorkspaceID)` (`exec.go:420-431`), and on a reused space that id names a workspace the *user* owns — which `worktree remove` closes outright, with every tab in it. The reuse defect is therefore not only "the agent starts in a busy pane"; it is "the escape hatch offered right afterwards destroys unrelated work". §5.4, and herdr's own half of it in §8.2. |
| — (not reached) | **Missing, and it would have broken the fix.** `Execute` overwrites `created` on *every* topology-producing op (`exec.go:321-325`), so any design that appends a second topology op silently clobbers `CheckoutPath` and disables the "no commits beyond base" half of the clean gate. §5.1 is why the space and the agent pane have to be separate fields rather than a second write to one. §9 pins it. |
| do not replicate herdr's checkout-path formula client-side for a pre-flight warning | **Right conclusion, and now for a stated reason** — §6.2. The two pre-flight cases worth catching are already caught *and already block submit*; the residual ones are precisely the ones the formula would be needed for. |

### 2.6 The incident itself

`~/.local/state/herdr/plugins/draft/last-used.json`, mtime `3 Sep 09:28`:

```json
{"kind":"claude","placement":"new-space","worktree":true}
```

and `projects.json` for `/home/zvi/Projects/herdr-draft`: `worktree:true,
placement:"new-space", seen:"2026-09-03T06:28:44Z"`. So the create ran with
**worktree on and placement new-space** — the inert default. The sibling
plugin `tdi.worktree-setup` logged its steps at `06:28:39Z`, five seconds
earlier, in that worktree; none of its three default steps is a `git diff`
(`~/.config/herdr/plugins/config/tdi.worktree-setup/config.toml`), so the
diff on screen was not put there by the create.

The checkout still exists — `herdr worktree list` reports
`{"branch":"zvi/test","is_linked_worktree":true,
"path":"/home/zvi/.herdr/worktrees/herdr-draft/zvi-test"}` with **no
`open_workspace_id`** — so the workspace that held it has since been closed.

### 2.7 The reuse branch did not fire, and has never fired here

`~/.config/herdr/herdr-server.log` settles it. The incident, verbatim:

```
06:28:12.953  api.request.start   request_id="cli:plugin" method="plugin.pane.open"
06:28:12.957  pane.spawn.start    pane_id=98  rows=30 cols=101
…
06:28:39.259  api.request.start   request_id="cli:worktree:create" method="worktree.create"
06:28:39.308  pane.spawn.start    pane_id=100 rows=43 cols=170
06:28:39.311  pane.spawned        pane_id=100 pid=2723888
06:28:39.312  workspace.create    workspace_id="wS" pane_id=100
06:28:39.312  workspace.focus     workspace_id="wS"
06:28:39.320  api.request.complete request_id="cli:worktree:create" outcome="ok"
06:28:39.339  api.request.start   request_id="cli:agent:start" method="agent.start"
06:28:39.477  api.request.start   request_id="cli:plugin" method="plugin.pane.open"
06:28:39.479  pane.spawn.start    pane_id=101 rows=43 cols=170
06:28:40.869  agent changed pane=100 previous_agent=None agent=Some(Claude) pgid=Some(2724760)
```

(Pane 98 at `rows=30 cols=101` is the herdr-draft popup itself — v3 §6's
shipped 104×32 popup is exactly 101×30 of terminal.)

`workspace.create … workspace_id="wS" pane_id=100` is emitted only by
`create_workspace_with_options`, the **else** branch of §2.1's `if let` — so
`created_workspace` was `true` and **the workspace was not reused**. The pane
the agent got was spawned 3.7 ms before the workspace record existed. The
persisted workspace count goes `workspaces=3` (06:28:35) →
`workspaces=4` (06:28:49), one net new.

And this is not one lucky create. Every `cli:worktree:create` in the log —
**fifteen of them, 2026-08-28 through 2026-09-03** — is followed within
milliseconds by at least one `workspace.create`, and seven are followed by
*two* (`w3`+`w4`, `w5`+`w6`, `w8`+`w9`, `wB`+`wC`, `wJ`+`wK`, `wM`+`wN`,
`wR`+`wS`), which is `ensure_source_parent_membership` opening the
origin-repo workspace as a side effect — the same side effect `plan.Clean`'s
doc comment already records from task 2b's live probing (`exec.go:413-419`).
There is not one create in seven days without a fresh workspace.

**So §5.2 does not fix the reported symptom, because the reported symptom was
not workspace reuse.** Three candidates the log named, resolved by asking the
author rather than by more log reading (§11 item 6 asked exactly this):

- **Pane 101, confirmed.** The author: a distinct pane, present after the
  agent's own pane was closed, gone only when the workspace was — which is
  pane 101's shape and neither of the other two candidates'. `plugin.pane.open`
  spawned it into the *same workspace* 165 ms after the create
  (`tdi.worktree-setup`, `herdr plugin list`).
- **Pane 99, ruled out.** A pane in a *different* workspace (`wG`) cannot be
  "closed when I closed the agent's pane, reopened when I quit" — its
  lifecycle has no dependency on this create's workspace at all.
  Coincidental timing, unrelated session.
- **The shell in pane 100, ruled out.** The author was describing a second
  pane, not the agent's own — this candidate required the diff to be *in*
  pane 100.

**What is in pane 101 is not, itself, a `git diff`.** `herdr plugin log
list --plugin tdi.worktree-setup` returns the exact captured stdout for this
run (`log_id: plugin-log-1700`, `started_unix_ms`/`finished_unix_ms` 175 ms
apart, matching the config read in §2.6): three `$ if [ -f … ]; then …; fi`
lines and three `[exit 0]`s, no diff, because herdr-draft carries none of
the three files the plugin's `[default]` steps guard on. But the pane's own
`pane.exit` did not fire until **06:29:09.95**, ~30 s after that 175 ms
script finished (`pane.spawn.start` `06:28:39.479` → `pane.exit`
`06:29:09.95`, `code: 0, signal: None` — a clean exit, not a kill from a
workspace close, which supports the pane surviving as a plain shell for that
window rather than dying with the script). Something in that ~30 s window —
most plausibly the author running `git diff` themselves to look around a
fresh checkout, or a shell prompt/hook printing repository state on landing
in a new directory — put the diff there. This document does not settle
which, and does not need to: **pane 101 belongs to `tdi.worktree-setup`, a
plugin herdr-draft neither invokes nor is aware of, that opens a companion
pane into every worktree workspace it sees created** (`herdr-plugin.toml`'s
event subscription, not read here) **— on a purely per-project, already
user-configurable basis**
(`~/.config/herdr/plugins/config/tdi.worktree-setup/config.toml`'s
`[[project]]` table, §2.6). Nothing in this design changes what that pane
is, does, or shows; a reader who wants it gone for this project gives it a
`[[project]] path = "~/Projects/herdr-draft"` entry with `steps = []`, the
same file, no herdr-draft or herdr change.

**What §2.1-§2.4 do still establish**, independent of the incident: the
branch exists, it is reachable (the empty-directory probe), its trigger set
is broad and its two cheap members are already blocked at submit, the
response gives herdr-draft no way to notice — and, the part that decides the
matter, if it ever does fire, the keep-or-clean gate's "clean" button closes
a workspace the user owns (§5.4). That is a hazard worth closing on its own
terms, at the cost of one CLI call, on top of a space/agent-pane separation
§5.3 needs regardless.

## 3. The reuse signal, obtained today

herdr allocates workspace ids from a monotonic counter and documents them as
stable:

```rust
static NEXT_WORKSPACE_ID: AtomicU64 = AtomicU64::new(1);
pub(crate) fn generate_workspace_id() -> String {
    let counter = NEXT_WORKSPACE_ID.fetch_add(1, Ordering::Relaxed);
    format!("w{}", encode_public_number(counter as usize))
}
```
(`workspace.rs:106-112`), and `Workspace.id` is `/// Stable public workspace
identity, independent of display order.` (`:179`). `reserve_workspace_ids`
(`:153-174`) advances the counter past the highest id in a restored session,
so ids are never recycled.

Therefore: **the set of workspace ids that exist immediately before the
create, intersected with the id the create returns, is an exact reuse
test.** No path formula, no schema change, no version floor.

`herdr workspace list` is already wired, and — the part that matters for
§5.2 — it is already on the **`Runner` interface**
(`herdrc/runner.go:96`), which is what `plan.Execute` takes. So every test
fake already implements it and `internal/plan` needs no new dependency to
make the call. `CLIRunner.WorkspaceList` (`:300`) returns `[]WorkspaceInfo`
(`:29-39`), carrying `WorkspaceID` and `Label`; `app.Bootstrap` already calls
it (`app/app.go:241`). Live, right now:

```
$ herdr workspace list
… "workspace_id":"w3" … "workspace_id":"wG" … "workspace_id":"w4" …
```

The only false reading is a workspace created by a *third party* between the
list and the create *and then reused by it*, which requires two coincidences
in a sub-second window; and the failure is benign, producing one extra tab in
a workspace herdr had just made. A workspace closed in that window cannot
produce a false positive, because ids are not recycled.

**The list call goes immediately before the create, not at Bootstrap.** The
Bootstrap list is minutes stale by submit time, and a workspace opened in
between would read as reuse. Placing the call adjacent to the create also
makes the failure policy free — §5.2.

## 4. What herdr can and cannot do, at v0.8.2

Exhaustively, because §7's rejections depend on it.

`WorktreeCreateParams` (`api/schema/worktrees.rs:12-27`, v0.8.2):
`{workspace_id, cwd, branch, base, path, label, focus}`. **There is no
destination parameter.** `workspace_id` and `cwd` select the *source* repo —
`resolve_worktree_source` (`app/api/worktrees.rs:186`) reads them as "which
checkout am I branching from", and supplying both is an `invalid_request`.
The CLI agrees: `usage: herdr worktree create [--workspace ID | --cwd PATH]
[--branch NAME] [--base REF] [--path PATH] [--label TEXT] [--focus]
[--no-focus]` (`cli/worktree.rs:143`). Note `--no-focus` and `--path` are
both available at v0.8.2; `--trust-repository` is not, confirming CLAUDE.md.

`herdr pane move` does support both placement shapes —

```
usage: herdr pane move <pane_id> --tab <tab_id> --split right|down [--target-pane ID] [--ratio FLOAT] [--focus|--no-focus]
       herdr pane move <pane_id> --new-tab [--workspace ID] [--label TEXT] [--focus|--no-focus]
       herdr pane move <pane_id> --new-workspace [--label TEXT] [--tab-label TEXT] [--focus|--no-focus]
```
(`cli/pane.rs:919`) — and §2.4 is why it is not usable here.

`herdr tab create --workspace <id> --cwd <path>` and `herdr pane split
<pane> --direction right --cwd <path>` both exist and both already have
`Runner` methods and request types (`herdrc/runner.go:394`, `:430`;
`TabCreateReq`, `PaneSplitReq`), because `topologyOp` already uses them for
the no-worktree placements.

**The consequence.** With v0.8.2 there is exactly one safe way to put an
agent pane on a fresh worktree checkout somewhere other than the worktree's
own workspace: create the worktree (`--no-focus`), then create a tab or split
*separately*, with `--cwd <checkout>`. Every alternative either moves the
worktree's root pane (destroying membership, §2.4) or asks herdr for a
destination it has no parameter for. The cost of the safe way is one idle
shell left in the worktree's workspace. §7.1 enumerates the six variants
tried and shows the cost is irreducible; §8.4 is the herdr change that
removes it.

## 5. The design: the space, and the agent's pane

### 5.1 The split

A plan currently produces one topology and threads its pane everywhere
(`exec.go:321-325`, then `created.PaneID` at each launch op). It will produce
two facts:

- **the space** — what `Clean` acts on: the worktree workspace, or the plain
  workspace/tab/pane that `topologyOp` made. Carries `CheckoutPath`.
- **the agent pane** — the pane the launch ops target.

They are the same pane in the common case and `ExecResult` says which:

```go
type ExecResult struct {
	// Created is the SPACE the plan established -- what Clean acts on.
	// Its PaneID is no longer necessarily where the agent runs.
	Created *herdrc.CreatedTopology

	// AgentPane is the pane the launch ops targeted. Equal to
	// Created.PaneID unless a placement op or a reuse correction moved it.
	AgentPane string

	// SpaceReused reports that the space op landed on a workspace that
	// was already open before the plan ran (§3). SpaceLabel names it, for
	// CleanCheck's refusal message.
	SpaceReused bool
	SpaceLabel  string

	FailedIndex int
	PromptText  string
}
```

`Op` gains one field, and its "exactly one request field per Kind" doc
comment (`build.go:99-106`) grows a modifier clause:

```go
// CwdFromCheckout tells the executor to fill this op's Cwd from the SPACE
// op's CheckoutPath, which Build cannot know: only OpTabCreate and
// OpPaneSplit set it, and only as §5.3's placement op under a worktree.
CwdFromCheckout bool
```

`Execute` precomputes the index of the **last** topology-producing op in
`ops`. That op's pane is the agent pane; every earlier topology op is the
space. This is a two-line precomputation over `ops` and it is what lets the
rest of the executor stay as it is.

`Build` stays pure. It performs no I/O, consults no `Runner`, and learns
nothing new (`in.Placement`, `in.UseWorktree` and `in.Ctx` are already
there). CLAUDE.md's rule holds unchanged.

### 5.2 Reuse correction — a runtime reaction inside step 1

This closes a **latent** hazard, not the reported one: §2.7 shows the reuse
branch has never fired on this machine, and §5.4 is what makes it worth
closing anyway. Everything it needs — the space/agent-pane separation, the
`Runner` call, the op kind — §5.3 already requires.

Reuse is a runtime fact. `Build` cannot model it, and inventing a
"conditional op" so that it could would be worse than the problem: `app`
seeds the staged checklist from `ops` **before** execution
(`app/app.go:1188` calling `submitSteps`, `app/async.go:844`), so an op that
may or may not run would either miscount the checklist or show a step that
never happens.

`Execute` therefore does the correction *inside* the `OpWorktreeCreate`
step, which is what its label ("creating worktree") already describes and
what keeps `len(ops)`, `Progress.Index` and `Progress.Total` untouched.
There is a precedent for exactly this shape: `startAgentWithDedupe`
(`exec.go:144-160`) is a runtime reaction to `agent_name_taken` that lives
inside the `OpAgentStart` step and that `Build` knows nothing about.

```
OpWorktreeCreate:
  1. ids := set of WorkspaceID from r.WorkspaceList(ctx)      # §3
  2. topo := r.WorktreeCreate(ctx, *op.Worktree)
  3. reused := ids.has(topo.WorkspaceID)
  4. if reused AND this op is the agent-pane op:
         claim := r.TabCreate(ctx, TabCreateReq{
             Workspace: topo.WorkspaceID,
             Cwd:       topo.CheckoutPath,
             Label:     op.Worktree.Label,   # the title, already here
             Focus:     true,
         })
         agentPane = claim.PaneID
     else:
         agentPane = topo.PaneID
```

Four things about that, each deliberate:

**The list call precedes the create, and its failure fails the step.** If
herdr-draft cannot establish who owns the pane it is about to type an agent
into, it must not type. Failing *before* `WorktreeCreate` runs means nothing
is half-built and the keep-or-clean gate is never reached — the step fails
with `Created == nil`, which `handleSubmitDone` already treats as "nothing to
clean" (`app/async.go:970`). This is the same principle as `promptIfReady`
(`exec.go:212-220`): never act into a state that has not been positively
confirmed. A `workspace list` that fails while `worktree create` would have
succeeded is nearly inconceivable — same socket, same server — so the policy
costs nothing in practice and is the honest one when it does fire.

**The correction claims a fresh tab, not a split.** A split changes the
geometry of a tab the user owns. A new tab adds one entry to their tab bar
and changes nothing they were looking at. When we are already in the wrong
workspace, the least invasive footprint is the right one.

**It fires only when the worktree op is the agent-pane op.** If §5.3's
placement op follows, the agent is going somewhere else entirely and a claim
in the reused workspace would be pure litter.

**It is unconditional, not a user choice.** §7.4.

### 5.3 Placement under a worktree — a build-time op

`topologyOp` keeps its four shapes. `Build` appends, after it:

| `UseWorktree` | `Placement` | ops |
|---|---|---|
| false | any | `topologyOp` only, exactly as today |
| true | `new space` | `OpWorktreeCreate` only (`Focus: true`), exactly as today |
| true | `tab here` | `OpWorktreeCreate` (`Focus: false`), then `OpTabCreate{Workspace: Ctx.WorkspaceID, Label: Title, Focus: true, CwdFromCheckout: true}` |
| true | `split here` | `OpWorktreeCreate` (`Focus: false`), then `OpPaneSplit{PaneID: Ctx.FocusedPaneID, Direction: "right", Focus: true, CwdFromCheckout: true}` |

`Execute` fills the placement op's `Cwd` from the space op's `CheckoutPath`
— which `CreatedTopology` already carries for exactly this class of need
(`herdrc/runner.go:19-24`; it exists today so `CleanCheck` can reach the
checkout). The path arrives absolute from herdr, so `pathx.ExpandTilde` is
not needed and must not be applied; `herdr pane split --cwd` does not expand
`~` server-side (`app/app.go:1145-1152`) and would silently create a pane in
a literal `~` directory if it were.

`--no-focus` on the worktree create is what stops the screen jumping to the
worktree's workspace and back. It is available at v0.8.2
(`cli/worktree.rs:143`) and `focusFlag` already emits it
(`herdrc/runner.go:232-237`).

Worked example. Focus is in workspace `wG` ("herdr-draft"), tab `wG:t1`,
pane `wG:p2`. Title `fix login redirect loop`, worktree on, branch
`zvi/fix-login-redirect-loop`, placement `split here`:

```
1  creating worktree      herdr worktree create --cwd ~/Projects/herdr-draft
                            --branch zvi/fix-login-redirect-loop --base main
                            --label "fix login redirect loop" --no-focus
                          → workspace wH, pane wH:p1,
                            checkout ~/.herdr/worktrees/herdr-draft/zvi-fix-login-redirect-loop
2  splitting this pane    herdr pane split wG:p2 --direction right
                            --cwd ~/.herdr/worktrees/herdr-draft/zvi-fix-login-redirect-loop
                            --focus
                          → pane wG:p3            ← the agent pane
3  starting agent         herdr agent start fix-login-redirect-loop
                            --kind claude --pane wG:p3
4  sending prompt         herdr agent prompt wG:p3 … --wait
```

The user's pane splits; claude runs on the checkout, beside their work.
Workspace `wH` exists, holds one idle shell in the checkout, and is what
carries `worktree_space` — so herdr's sidebar grouping, `herdr worktree
remove` and the keep-or-clean gate all keep working. That idle shell is the
disclosed cost of §4's constraint; §6.1 says so on screen and §8.4 is what
removes it.

**A second-order consequence, stated because it is the very mechanism this
document is about.** Pane `wG:p3` now sits in the new checkout, inside
workspace `wG`. A *later* `worktree create` for the same checkout could
therefore match `wG` under §2.1's rule 4 — if `wG`'s first tab's root pane
were the one in the checkout, which it is not here, but the shape is real.
The design is closed under repetition: that later create would be detected as
reuse by §5.2 and would claim its own fresh tab rather than land on `wG:p3`.

### 5.4 The keep-or-clean gate

This is the half the earlier investigation did not reach, and it is the more
serious one.

`Clean` today is `r.WorktreeRemove(ctx, created.WorkspaceID)`
(`exec.go:420-431`). On a reused space, `created.WorkspaceID` is **a
workspace the user owns**, and `worktree remove` closes it:
`handle_api_worktree_remove_finished` calls
`close_removed_linked_worktree_workspace(ws_idx)` and emits
`WorkspaceClosed` (`app/api/worktrees/deferred.rs:541-560`; HEAD
`:552-571`). herdr will do it, too, because the create *stamped*
`worktree_space` onto that workspace on the way past
(`mark_worktree_membership(&source, ws_idx, …, !created_workspace)`,
`deferred.rs:418-424`). So the current code path is: land in a stranger's
workspace, then offer the user a button that closes it, with every tab and
pane in it. That is §8.2's herdr bug and this section's client-side guard.

`CleanCheck` and `Clean` take the plan's outcome rather than a bare
`CreatedTopology`, and gain two rules:

- **`SpaceReused` refuses, with the label.** `CleanDecision{Allowed: false,
  Reason: "this session joined \"<SpaceLabel>\", a workspace that was already
  open -- removing the worktree would close it and everything in it. The
  checkout at <path> is left in place."}` The refusal names the path so the
  user can act, and warns rather than acting, because the only two automatic
  options are both worse: closing their workspace, or removing the checkout
  behind herdr's back with `gitx` and leaving a stale `worktree_space`
  pointing at nothing — which is precisely the state §2.1's rule 1 turns into
  the next collision.

  **The refusal is total**, including the pane §5.2 claimed: a "clean" that
  closed our tab and silently left a git worktree and a branch on disk would
  be a clean in name only, and for a worktree the expensive belief to hold
  wrongly is "nothing remains". The gate already has the right affordance for
  this — v1 spec §9's "the clean option is disabled with the reason shown" —
  so no new UI is needed, and the two buttons never both appear enabled
  doing the same thing. §11 asks whether the reason should carry the exact
  `git worktree remove` command.
- **A claimed agent pane is closed first.** When `AgentPane !=
  Created.PaneID`, `Clean` runs `r.PaneClose(ctx, AgentPane)` before the
  worktree/workspace removal, so nothing is left running an agent in a
  directory about to be deleted. `PaneClose` is a new `Runner` method over
  `herdr pane close <pane_id>` (`cli/pane.rs:1012-1023`).

  CLAUDE.md's two-success-shapes rule applies here and the answer was
  checked rather than assumed, because guessing it wrong is a bug this
  codebase has already shipped once (`PaneRun` via `runJSON`). `pane close`
  routes through `runtime::pane_close` → `print_method_response`
  (`cli/runtime.rs:124-126`), the same helper every JSON subcommand uses, and
  `handle_pane_close` answers `encode_success(id, ResponseResult::Ok {})`
  (`app/api/panes.rs:1545-1550`). So `PaneClose` is **`runJSON`**, like
  `WorktreeRemove` and `WorkspaceClose` (`herdrc/runner.go:605-621`), not
  `runOK` — which in this file has exactly one user, `PaneRun`
  (`runner.go:590`).

`CleanCheck`'s existing worktree logic is untouched: it still resolves the
base ref and defers to `gitx.Disposable` on `Created.CheckoutPath`
(`exec.go:342-376`), which keeps working precisely because §5.1 stops the
placement op from overwriting the space topology. Under today's single-
topology threading (`exec.go:321-325`) a second topology op would clobber
`CheckoutPath` and silently disable the "no commits beyond base" half of the
gate — the same class of defect `resolveBaseRef`'s doc comment
(`exec.go:378-409`) was written about. §9 pins it with a test.

## 6. The form

### 6.1 The `placement` row stops being inert

`PlacementField.Enabled()` becomes `true` unconditionally, `SetWorktreeOn`
stops snapping the selection back to `new`, and `FooterRungs`' `"nothing to
set here"` rung goes. `syncDerivedInertness` (`app/app.go:1626`) keeps
pushing the worktree state in, because the field still needs it — not to go
inert, but to say a different thing.

The chips keep their labels and IDs (`new` / `tab-here` / `split-here`,
unified with `config.toml`'s vocabulary in task 21). `Chip.FocusHint` becomes
worktree-dependent, since the same chip now has two different consequences:

| chip | worktree off | worktree on |
|---|---|---|
| `new space` | opens a new workspace of its own | the worktree opens as its own space |
| `tab here` | opens a tab beside this pane's tab | the agent opens a tab here, on the worktree's checkout |
| `split here` | splits this pane in two | the agent splits this pane, onto the worktree's checkout |

and, under worktree, for the two non-default chips only, the panel's hint
line is followed by the disclosure: `the worktree also keeps a space of its
own`.

**The row itself shows only the chip label**, as it does with worktree off.
It does not restate the cost. The reason is v3 §3 rule 1's own arithmetic:
the row states *its* consequence, and "a worktree exists" is already stated
one line up, by the `worktree` row's `on · <branch> ← <base>`. Two rows
saying the same thing three lines apart is what v2 §3 rule 5's copy pass
exists to delete. The extra space is a fact about the worktree, and the
worktree row owns it. §11 records the alternative.

The provenance column's `implied by worktree` value disappears with the
inert state, and so does `internal/create`'s matching `provenanceWorktree`
constant (`create/resolve.go:587`) — it existed for "the one value the plan
itself decides", and the plan no longer decides it.

### 6.2 No new pre-flight warning

Decided against, for a reason worth writing down because the obvious
intuition points the other way.

The v3 §9 session list is built for exactly this shape of problem: the app
already computes `labelTaken` from the cached `WorkspaceList` and
`refreshSessions` marks the colliding row (`field_title.go:381-408`), showing
a collision coming rather than reporting it afterwards. Extending it to
"a workspace already holds the checkout you are about to create" is the
natural next thought.

It should not be built, and the argument is §2.1's: **the two pre-flight
conditions worth catching are already caught, and already block submit.**
`branchExists` (real `git.BranchExists`) and `labelTaken` both feed
`titleDupBlocked` (`app/async.go:569`), which gates submit outright
(`app/app.go:1028`). Whatever reuse survives that gate is, by construction,
reuse that neither the branch name nor the workspace label reveals — a
workspace whose *checkout path* matches while its branch and label do not.
Detecting that before the create means knowing the target checkout path
before herdr computes it, which means either re-implementing herdr's
path formula in Go (a two-language duplication that must never drift, for a
value herdr hands over for free one call later) or asking herdr for it (a
schema change, §8, and a version floor herdr-draft cannot meet today).

Meanwhile §5.2's correction is not a partial mitigation of the symptom — it
removes it entirely, for every one of §2.1's four rules, with no formula and
no version floor. A pre-flight warning would buy the user a sentence before
a problem they will no longer have.

`WorkspaceInfo.Worktree.CheckoutPath` is available in the cached list
(`herdrc/runner.go:29-39`) and stays unused, deliberately. If §8.3 ever
lands, the reuse fact arrives in the create response and this section does
not change.

### 6.3 The headless verb

`internal/create` owns no precedence of its own, so both of its
worktree/placement special cases go:

- `resolve.go:358-374` — the block that forces `PlacementNewSpace` under a
  worktree and prints `--placement … ignored: a worktree always opens a new
  space`. Deleted. `--placement split-here --worktree` is now a request
  herdr-draft can honour.
- `resolve.go:562-565` — `requireContext`'s `if in.UseWorktree { return nil }`
  early return. Deleted: `tab-here` needs `HERDR_WORKSPACE_ID`/`HERDR_TAB_ID`
  and `split-here` needs `HERDR_PANE_ID` whether or not a worktree is
  involved. The existing per-placement messages (`:566-578`) are already
  right and need no wording change.

`equivalence_test.go` is the test that keeps the two paths honest and it
needs no new case to *notice* this: with both overrides gone, a form and a
command given the same remembered `placement` produce the same
`plan.Input.Placement`, which is what it already asserts field for field.

### 6.4 Nothing migrates

`default_placement` now applies under a worktree too. That reads like a
silent behaviour change for anyone with a remembered `tab-here`, and it is
not, because **no such stored value can exist**: `PlacementField` snapped
back to `new` whenever a worktree was on (`field_placement.go:140-146`) and
`internal/create` forced the same, so every value ever persisted by a
worktree user is `new-space`. The live state file says so:
`{"kind":"claude","placement":"new-space","worktree":true}`. A user who had
`default_placement = "tab-here"` in `config.toml` while working
worktree-first was already getting `new space`; after this change they get
what their config asked for, on their next create, once.

## 7. Rejected alternatives

### 7.1 Moving the worktree's own pane

`worktree create` then `pane move --tab <invoking> --split right`. §2.4:
the source workspace empties, herdr removes it, `worktree_space` goes with
it, and there is no guard. `herdr worktree remove` then has no workspace to
address, `plan.Clean` calls it with a dead id, and the checkout is orphaned
from herdr's model — the exact state §2.1's rule 1 converts into the next
collision.

The obvious repair — split the worktree workspace first so the move does not
empty it, *then* move the second pane out — works, and lands in exactly the
same place: one idle shell left behind, reached by three more calls and three
more failure modes than §5.3's composition. Six variants were enumerated
(move the root pane; pre-split then move; split the invoking pane then `cd`
via `pane run`; `worktree open` after a client-side `git worktree add`;
`worktree create --path` to a chosen location then move; `tab create` then
move) and every one that preserves membership leaves exactly one pane
behind. The cost is a property of §4's API surface, not of the composition
chosen.

### 7.2 Asking `worktree create` for a destination

Not merely unimplemented — **unrepresentable**. `worktree_space` is one
`Option` per workspace (`app/api/worktrees.rs:449`; §2.4), so "a worktree's
pane inside a workspace that is not the worktree's" has nowhere to be
recorded. Attaching it anyway would either overwrite the destination
workspace's own membership or claim a user's general-purpose workspace as a
worktree space, which is §8.2's bug on purpose. Making it representable
means per-tab or per-pane membership, and with it new answers for `worktree
remove`, the sidebar's grouping, session persistence and
`confirm_implicit_worktree_group_close` — a herdr redesign, not a parameter.
§8.4 is the small change that gets the same user-visible result without it.

### 7.3 Creating the checkout ourselves

herdr-draft has `internal/gitx` and could run `git worktree add` directly,
then place a pane with `--cwd`. It would need no herdr change and leave no
extra pane. It also throws away every worktree-aware behaviour herdr has —
grouping, `worktree remove`, `worktree list`'s `open_workspace_id`, the
group-close confirmation — and puts herdr-draft in the business of choosing
checkout paths, which §6.2 just argued against for a weaker reason. CLAUDE.md
frames the whole plugin as driving herdr through its public CLI; creating
herdr's own concepts behind its back is the same violation as reaching for
the socket.

### 7.4 A "reuse the existing pane anyway" opt-in

Asked for explicitly in the brief, and it should not be built.

A chip is a promise the form can keep. This one cannot: §6.2 establishes that
reuse is undetectable before the create for every case that survives the
existing submit gate, so the control would ask the user to decide about a
condition that is invisible at the moment of deciding and, in the
overwhelming majority of creates, will not occur at all. v3 §9 made the same
call about the session list — "on a list nothing can act on, [a cursor] is a
promise the form cannot keep" — and refused a cursor for it.

It is also not a choice anyone wants once stated plainly. Reusing the pane
means `herdr agent start` types a command line into a live shell that may
have a foreground process in it; this codebase's own hardest-won rule
(`promptIfReady`, `dialog.go`, CLAUDE.md's "screen detection is
evidence-based") is that herdr-draft does not send input into a state it has
not confirmed. And the intent the opt-in would express — *put this session in
a workspace that already exists* — is what §5.3 now provides properly, in a
pane herdr-draft created, chosen before submit, on a row built for it.

### 7.5 Waiting for `already_open`

The right signal, the wrong dependency. Installed herdr is 0.8.2;
`min_herdr_version` is 0.8.2; and this repo has a scar exactly here —
`--trust-repository` is *deliberately* unwired because 0.8.2 rejects the flag
(CLAUDE.md; `app/app.go:1165-1174`). §3's id-set test is exact today, on
0.8.2, with a `Runner` method that already exists. §8.3 stays worth asking
for, as a simplification that deletes one call, not as a prerequisite.

## 8. What herdr should change

Separated because none of it is required for §5 to ship. Items 1 and 2 are
bugs in herdr that exist whether or not herdr-draft does anything; 3 is a
two-line convenience; 4 is the one that would make §5.3 free. All line
numbers are v0.8.2.

### 8.1 `pane move` silently destroys worktree membership

`handle_pane_move` (`app/api/panes.rs:624`) removes an emptied source
workspace (`:844-845`) with none of the protection `close_pane` gives the
same situation (`:1562-1570`, error `confirmation_required`, "closing this
pane would close a worktree group"). `previous_worktree_space` is snapshotted
(`:662`) but restored only by `recover_failed_pane_move` (`:1070`); a
*successful* move drops it. `api_pane_move_to_new_workspace_closes_empty_
source_workspace` (`:2984`) pins the behaviour, so this is a decision to
revisit, not an oversight to patch blindly.

Either of two fixes is coherent, and the choice is herdr's:

```rust
// (a) refuse, symmetrically with close_pane
if source_workspace_empty
    && cross_workspace
    && self.state.workspaces[source_ws_idx].worktree_space.is_some()
{
    return encode_error(
        id, "confirmation_required",
        "moving this pane would close a worktree group",
    );
}

// (b) carry the membership to the destination
if source_workspace_empty && cross_workspace {
    let carried = self.state.workspaces[source_ws_idx].worktree_space.clone();
    self.state.workspaces.remove(source_ws_idx);
    if let Some(membership) = carried {
        // target_ws_idx is resolved below; set after the insert, and only
        // when the destination has no membership of its own.
        …
    }
}
```

(b) is the better behaviour and the larger change: `worktree_space` being
one-per-workspace (§7.2) means it can only be carried into a destination
that has none, so (b) needs a documented answer for the case where it does.
(a) is small, honest, and matches an existing precedent.

### 8.2 `worktree create` stamps membership onto a workspace it did not open

On the reuse branch, `mark_worktree_membership(&source, ws_idx, result.path,
true, !created_workspace)` (`app/api/worktrees/deferred.rs:418-424`) sets
`worktree_space` on a **pre-existing, user-owned** workspace. That workspace
is then removable by `herdr worktree remove --workspace <it>`, which closes
it entirely (`:534-556`) — a general-purpose workspace with unrelated tabs
becomes destroyable by a worktree command, because something once ran a
`worktree create` for a checkout one of its panes happened to be sitting in.

The narrow fix is to leave `worktree_space` alone when the workspace was
reused rather than created (`emit_update` already distinguishes the cases via
`!created_workspace`); the trade is that `worktree list`'s
`open_workspace_id` then stops reporting that checkout as open. The right
answer depends on which of those herdr considers the invariant, which is why
this is a report rather than a patch.

### 8.3 `already_open` on `WorktreeCreated`

The smallest item, and purely additive: a new field with
`#[serde(default)]`-compatible semantics on an existing variant, mirroring
`WorktreeOpened`, which has carried it since before 0.8.2.

```diff
--- a/src/api/schema/response.rs
+++ b/src/api/schema/response.rs
@@
     WorktreeCreated {
         workspace: WorkspaceInfo,
         tab: TabInfo,
         root_pane: PaneInfo,
         worktree: WorktreeInfo,
+        /// True when the checkout was already open in a workspace and that
+        /// workspace was reused rather than a new one created -- the same
+        /// fact `WorktreeOpened::already_open` reports for `worktree open`.
+        already_open: bool,
     },
```

```diff
--- a/src/app/api/worktrees/deferred.rs
+++ b/src/app/api/worktrees/deferred.rs
@@ ResponseResult::WorktreeCreated {
                 root_pane: self
                     .root_pane_info(ws_idx, tab_idx)
                     .expect("created worktree workspace should have an active root pane"),
                 worktree,
+                already_open: !created_workspace,
```

`created_workspace` is already in scope, bound by the same `if let` that
chooses between reuse and creation, and the create path has exactly two
branches — so `!created_workspace` is exactly
`open_workspace_idx_for_checkout(&result.path).is_some()`, not an
approximation. `EventData::WorktreeCreated` (`api/schema/events.rs:455-458`)
carries `{workspace, worktree}` and could take the same field for symmetry
with `EventData::WorktreeOpened` (`:459-463`); herdr-draft does not consume
events and does not need it.

What it buys herdr-draft: §3's pre-create `workspace list` call disappears,
and with it §5.2's fail-closed policy. Gated on `min_herdr_version` moving
past the release that carries it, exactly as `--trust-repository` is.

### 8.4 `worktree create --no-open`, and a path-addressed `worktree remove`

The change that makes §5.3 free. Today `worktree create` always resolves a
workspace for the checkout (create or reuse, §2.1), so a caller that wants
the checkout but intends to place the pane itself gets a workspace and an
idle shell it did not ask for.

```
herdr worktree create … --no-open
  → { "type": "worktree_created", "worktree": { … }, "workspace": null }
```

`--no-open` runs `git worktree add`, records whatever herdr needs to keep
knowing the checkout is a worktree of that repo, opens **no** workspace,
reuses none, and returns the `WorktreeInfo` alone. It composes with the
existing `--path`.

It needs a companion, because `WorktreeRemoveParams` is
`{workspace_id, force}` (`api/schema/worktrees.rs:46-50`) and a checkout with
no workspace has no id to name:

```
herdr worktree remove (--workspace ID | --cwd PATH --path PATH) [--force]
```

With both, herdr-draft's `tab here` / `split here` under a worktree become
`worktree create --no-open` followed by one `tab create`/`pane split`, with
no leftover pane, and §5.4's `PaneClose` step becomes the whole of `Clean`'s
extra work. §11 asks whether the author would rather wait for this than ship
§5.3's disclosed cost.

## 9. Testing

Unit, in `internal/plan`:

- `Build` emits the placement op for `worktree × {tab-here, split-here}` and
  does not for `worktree × new-space` or for any `!UseWorktree` case; the
  placement op carries `CwdFromCheckout` and an empty `Cwd`; the worktree op
  carries `Focus: false` in exactly the two cases that have a placement op.
- `Execute` with a fake `Runner`:
  - **not reused** — `WorkspaceList` returns ids not containing the created
    one; the recorded call sequence is byte-identical to today's, `AgentPane
    == Created.PaneID`, `SpaceReused` false.
  - **reused** — the created id is in the list; one extra `TabCreate` on the
    returned workspace with `Cwd == CheckoutPath`; the agent starts on the
    claimed pane; `SpaceReused` true and `SpaceLabel` set from the list.
  - **reused with a placement op** — no extra `TabCreate`; the agent starts
    on the placement op's pane.
  - **`WorkspaceList` fails** — the step fails, `WorktreeCreate` call count
    is `0`, `Created` is nil.
  - **`CheckoutPath` survives a placement op** — `Created.CheckoutPath` is
    still the worktree's after `OpPaneSplit` has produced a topology. This is
    the regression test for the clobber §5.4 describes, and it is the one
    that would fail against a naive `created = topo` on every topology op.
- `CleanCheck` refuses on `SpaceReused` with `SpaceLabel` in the reason;
  `Clean` calls `PaneClose` before `WorktreeRemove` when `AgentPane !=
  Created.PaneID`, and not at all when they are equal.

Equivalence: `internal/create`'s `equivalence_test.go` needs no new case
(§6.3) but must be run — with both overrides deleted, a divergence would show
up there first.

Golden frames, and CLAUDE.md's warning is the reason this list is explicit
rather than "add frames for the change". The v2 form shipped a defect in its
**opening** state through fifteen green commits because every fixture had a
title typed. The opening state is precisely what this change moves:

- `placement` focused, worktree **on**, each of the three chips — the panel's
  hint and, for two of them, the disclosure line.
- `placement` unfocused, worktree on, each chip — the row is the chip label
  and nothing else (§6.1).
- the **opening** frame with worktree on: `placement` is now a live row with
  a live provenance cell where it used to read `worktree opens as its own
  space` / `implied by worktree`.
- the focus ring with `placement` enabled under worktree — it is a real stop
  again, so every frame that walks the ring changes.

And build it and run it. `docs/manual-smoke.md`'s Route B runs the real form
in an ordinary pane. The smoke matrix grows the four cells this change
creates — `{tab here, split here} × worktree on`, form and `create` — and the
reuse path is worth provoking by hand at least once: open a worktree session,
close its *branch* and checkout from a shell while leaving the workspace
open, then create the same session again.

## 10. Scope

In: `internal/plan` (`build.go`, `exec.go`), `internal/herdrc` (one new
`Runner` method), `internal/form/field_placement.go`, `internal/app`
(`syncDerivedInertness`, the submit/clean wiring, `submitResult`'s type),
`internal/create` (two deletions), tests and frames.

Out: `internal/defaults` (no new key, no new tier, no reordering),
`internal/config`, `internal/linear`, `internal/clauth`, `internal/gitx`,
`internal/theme`, `internal/pathx`. The row order (`app.New`'s `sections`)
does not change. `--trust-repository` stays unwired.

## 11. Open questions for review

Recorded rather than guessed at, and each carries this document's own
recommendation so no *implementation* is blocked on an answer. **Item 6 is
the exception**: it is not a taste call, it is the unanswered diagnostic
question §2.7 opens, and problem 2 should not be called fixed until someone
answers it.

1. **Is the leftover idle shell acceptable?** §4 proves it is irreducible at
   v0.8.2 and §8.4 removes it. The alternative is to keep `placement` inert
   under a worktree until herdr grows `--no-open`, which is the earlier
   investigation's position. *Recommendation: ship §5.3 now.* The pane is not
   junk — it is the worktree's own workspace, where one would run tests and
   git in that checkout, and it is what carries the membership herdr needs.
   Waiting means the row stays dead in the default configuration for an
   unbounded time, which is the reported defect.
2. **Should the row state the extra space, or only the panel?** §6.1 puts the
   chip label on the row and the disclosure in the panel, arguing the
   `worktree` row one line up already says a worktree exists. A reader who
   never focuses `placement` therefore never sees the disclosure.
   *Recommendation: as specified.* If review disagrees, `tab here · plus its
   own space` fits the value column and is the smaller change.
3. **`split here`'s direction.** `defaultSplitDirection` is `"right"`
   (`build.go:121-125`), inherited from task 2/2b's live checkpoints. Under a
   worktree the split is a *long-lived agent pane* rather than a scratch
   pane, and `down` may read better in a wide pane. *Recommendation: leave it
   at `right`.* One default, one place, and no evidence either way; a
   `split_direction` config key is a new key for a taste question and §10
   keeps `internal/config` out.
4. **Does the reuse refusal in `CleanCheck` need an escape hatch?** §5.4
   refuses to clean a reused space and names the checkout so the user can act
   — but `herdr worktree remove` on that workspace will close it (§8.2), so
   the honest instruction is `git worktree remove <path>` from the origin
   repo. *Recommendation: put that exact command in the reason string.* It is
   longer than any reason the gate currently shows and worth the width.
5. **Is §5.2 worth building at all, given §2.7?** The reuse branch has never
   fired here, so §5.2 closes a latent hazard rather than a live one. It is
   cheap — one CLI call and ~20 lines on top of the space/agent-pane
   separation §5.3 needs regardless — and what it prevents is `Clean`
   closing a workspace the user owns. *Recommendation: build it, and change
   nothing else about the plan if the author would rather not.* The
   space/agent-pane split (§5.1) is load-bearing for §5.3 on its own and
   must land either way; §5.2 is the twenty lines that ride on it. If it is
   dropped, §8.2 becomes the only defence and it is a herdr change.
6. **RESOLVED, 2026-09-03, by asking the author.** *Was the diff in a
   distinct pane, or in the agent's own?* A distinct one — it outlived
   closing the agent's pane and closed only when the workspace did. That
   fact alone identifies it as pane 101, `tdi.worktree-setup`'s companion
   pane (§2.7): the only candidate whose lifecycle is tied to *this*
   workspace and is not the agent's own pane. **Not a herdr-draft bug, not
   a herdr bug — a side effect of a separate, already-installed plugin that
   reacts to every worktree creation regardless of who created it, already
   configurable per-project in its own config file.** No item in §5 through
   §8 addresses it or needs to. §2.7's closing paragraph has the full
   account and the config line that turns the pane off for this project, if
   wanted.
7. **Is `w`-prefixed id-set comparison too clever?** §3 rests on herdr's
   workspace ids being stable and never recycled, which is documented
   (`workspace.rs:179`) and enforced (`reserve_workspace_ids`) but is not a
   guarantee herdr's API contract states in so many words. *Recommendation:
   proceed, and cite `workspace.rs:179` at the call site* — the failure mode
   if it ever changed is one extra tab, and §8.3 retires the technique.

## 12. Superseded text, itemised

| where | status |
|---|---|
| `plan/build.go:19-21`, `Placement` "is ignored when `Input.UseWorktree` is set" | replaced by §5.3: it is honoured, as the agent pane's location |
| `plan/build.go:159-162`, `topologyOp` "worktree creation always wins over Placement" | replaced by §5.1/§5.3; the op is the *space*, and no longer decides the agent's pane |
| v1 spec §6 field 5 (`:213-215`), "Active only when Worktree is off … inert with hint" | replaced by §6.1; the row is always a focus stop |
| v1 spec §9 step 1 (`:301-303`), worktree create "→ new workspace id + its initial tab/pane" | wrong as stated: it may return an existing workspace and a pane herdr-draft did not create. Replaced by §2.1-§2.3 |
| v2 spec §6's field table, `placement` row's Inert cell `worktree opens as its own space` | replaced by §6.1's two-column hint table; the sentence survives only as `new space`'s worktree-on hint |
| v2 spec §13 (`:626-627`), "Only the tab-here and split-here placements need [the pane env]; a new space or a worktree needs neither" | replaced by §6.3: a worktree with `tab-here`/`split-here` needs it too |
| `create/resolve.go:358-374`, the `--placement … ignored` override | deleted, §6.3 |
| `create/resolve.go:562-565`, `requireContext`'s `UseWorktree` early return | deleted, §6.3 |
| `create/resolve.go:585-588`, `provenanceWorktree` | deleted, §6.1; the plan no longer decides placement |
| `form/field_placement.go:24`, `placementInertHint` and `:26-31` `placementInertPanelHint` | replaced by §6.1's hints; there is no inert state to explain |
| v3 spec §4's mockup, the `placement  worktree opens as its own space` / `implied by worktree` row | replaced by §6.1; §9 requires a new frame for the opening state |

Unchanged and still true: `plan.Build` performs no I/O and never touches a
`Runner` (§5.1); `Row(w)` takes no height (§6.1 changes what a row says, not
how it measures); `--trust-repository` stays unwired; the five-key
`.herdr-draft.toml` allow-list is untouched.

## 13. Out of scope

- **A `placement` value meaning "a new space, but do not focus it".** Focus
  is a separate axis from topology and every op already takes `Focus`; mixing
  the two into one chip row would make a four-chip row answer two questions.
- **Per-tab or per-pane worktree membership in herdr.** §7.2. Named so the
  next reader knows it was considered and how large it is.
- **A pre-flight collision warning.** §6.2, with the argument.
- **Closing the leftover shell.** `pane close` on it can hit
  `confirmation_required` (§2.4) depending on a *user setting*, so the
  behaviour would differ per configuration; consistent behaviour with one
  disclosed pane beats inconsistent behaviour with sometimes none. §8.4 is
  the real fix.
