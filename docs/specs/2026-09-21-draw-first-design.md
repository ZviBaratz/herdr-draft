# herdr-draft — draw the form first

- **Date:** 2026-09-21
- **Status:** approved by the owner 2026-09-21, one section at a time (§2
  records the rulings). **Not implemented.** Implementation is a separate
  decision, and #293 is unmilestoned on purpose. Until it lands, every
  sentence below describes intended behaviour, and each amendment block this
  document adds to an earlier spec says so.
- **Issues:** #293, which closes with the implementation, and #292, which
  closes with it (decision 4).
- **Amends:** v1 spec §6 (the static-precondition sentence, field 1 and
  field 7), §8 (the clauth and Linear rows), §9 (the account verdict), §10
  (the absent key, and the cache) and §13 (inert with a reason); v2 spec §6.1
  (loading, and absent by design) and v2 spec §13 as #141 amended it (the
  popup's degradation); v3 spec §12 (the opening state at every size). §12
  itemises every sentence that stops being true. Every section named here
  stays authoritative apart from the sentences §12 lists.
- **Does not amend:** v2 spec §14's "rows never move", which is about focus
  moving rather than about the row set; the row set is still fixed before the
  first frame (§4). Nor the pre-open refusal: v1 spec §9 names an unreachable
  herdr, and `Bootstrap`'s doc comment lists the three conditions it refuses
  on. The same three still refuse, and still before anything is drawn.
- **Citations.** Code is cited by symbol at `2b6c32d`, not by line: the
  implementation moves every line in `internal/app`. The design was written
  and reviewed at `d8d5ada`. Between the two, nothing it cites changed
  except `DimText`'s contrast floor (#310), which §6 now relies on. The
  names this document introduces (`Peek`, `Cached`, `resolveKeyCmd`,
  `linearKeyMsg`, `handleLinearKey`, `accountWait`, `Options.Probe` and the
  three `Setup` fields) are proposals; the implementation may rename them,
  and what is specified is the behaviour.

---

## 1. Summary

`app.Bootstrap` returns a fully built `Model`, and `cmd/herdr-draft` reaches
`tea.NewProgram` only after it does. Before it returns, it runs three
subprocesses besides herdr's own: the Linear `api_key_cmd`, bounded at 60s;
`clauth status --json` when clauth's status file is stale, 30s; and a
configured account picker's probe, 30s. While any of them waits, the popup
pane is blank and says nothing. #141 bounded the waits, and its own decision
record says the bound "only decides when to give up". A person whose
`op read` takes fifteen seconds to approve still watches an empty pane for
fifteen seconds, on every open.

This design draws the form first:

- **Before the draw, no subprocess but `herdr workspace list`.** Everything
  else the first frame needs is a local read (§3).
- **The three slow calls run from `Init`**, on the popup's `Lifetime`, and
  land through delivery paths that already exist (§5).
- **The row set is still decided before the first frame.** Only what a row
  says changes afterwards (§4). The `issue` row exists when a key source is
  configured; the `account` row when clauth's status file answers with two or
  more profiles, or when it is stale and clauth is on `PATH` to be asked.
- **The form is usable during the wait**, not merely visible. The cached
  issue list is pickable while the key resolves, and stays pickable if it
  fails (#292).
- **`create` does not change shape.** It has no draw. The one contract it
  shares with the popup, `linear.ResolveAPIKey`, now reports an `api_key_cmd`
  that printed nothing instead of treating it as unconfigured (§8).

## 2. Decisions

The owner's decisions, recorded on #293 ("Design decisions, 2026-09-21")
before this design was written:

1. **Goal: usable during the wait**, not merely visible.
2. **`account` row: the fast half decides, the slow half loads.** A fresh
   status file decides the row exactly as today. A stale one shows
   `loading…` while the CLI is asked. Fewer than two profiles then leave the
   row in place with #200's reason (fixed in #255) rather than removing it.
3. **An `api_key_cmd` that prints nothing**, with no `LINEAR_API_KEY` and no
   inline key, is unavailable with a reason rather than absent. The
   fall-through to the other two sources is kept.
4. **`issue` row: cache now, keep it on failure.** `linear-cache.json` is
   loaded before the draw and shown pickable while the key resolves. On
   failure it stays pickable and its note becomes the reason. This is #292.
5. **⌃S during an account load: hold, then proceed.** "Held through #202's
   helpers at the popup's 5s budget." On timeout the create goes ahead without
   the dead-credential check (#243/#245). Unlike #202's held checks, this
   timeout does not refuse. Ruling 12 narrows it for one case: when the
   account would be `auto` and the picker has not answered its probe, the
   create cannot go ahead and refuses (§7.3).

Approach 1 was chosen with them: move the slow calls into `Init`, reuse the
existing delivery paths, and give the two fields an explicit loading state.
Two alternatives were rejected. Modelling loading as `unavailable` fails
because an unavailable row is inert and ignores input (#191), which breaks
decision 4, and a submit could not tell loading from failed. A placeholder
screen in front of all of `Bootstrap` makes the wait visible but not usable,
which decision 1 rules out.

Rulings made while this design was presented, 2026-09-21:

6. **A `PATH` check joins decision 2's fast half** (§3.2). Without it,
   everyone without clauth, which is most people, would see a `loading…` row
   that could only vanish or say "clauth not found".
7. **The picker's probe moves too, merged into the opening preview** (§3.3,
   §5.4). What that costs a ⌃S is §7.1.
8. **A loading `account` row is inert and not a focus stop** (§4.2).
9. **A refresh keeps the issue the user picked** (§5.2).
10. **`fetching assigned issues…` applies to today's ordinary open as well**
    (§6).
11. **When ⌃S outlasts a clauth load, the account is what the row would have
    selected, from config alone** (§7.2).
12. **A probe still pending at the deadline refuses the submit** (§7.3).
13. **`DimText`'s contrast is not this design's question** (§6). The ruling
    was to leave it to a separate issue. That issue already existed as #299,
    and #310 had closed it by flooring `DimText` at 3:1; #316 duplicates it.
14. **Every amendment block this document adds to an earlier spec says it is
    approved and not yet implemented.** The implementing change removes that
    clause (§11).
15. **The account hold takes one of #202's two helpers, not both** (§7.1). Its
    bound is `checkBudget()`'s, but the wait is a timer rather than
    `awaitCheck`, because the answer is already in flight as a message and
    there is no call to hand it. Approved with §7 on 2026-09-21. It is an
    exception to CLAUDE.md's rule that anything new holding a submit "gets
    the same bound, from the same two helpers" (§11).

**Revised after an independent review, 2026-09-21.** A reviewer in a fresh
session checked this document against the code at `d8d5ada`. Its findings
changed five things in the approved sections, none of them a ruling:

- a probe that fails while ⌃S waits on it refuses the submit rather than
  releasing it unpinned (§5.4, §7.3);
- §7.2's account from config is recorded on the Model, where `accountPin`
  and the pick read it (§7.2);
- the account holds apply only when the agent is `claude` (§7.1);
- the two account holds sit where the account is read, after every
  validation that does not need it, rather than before all of them (§7.1);
- the merged probe keeps exactly today's verdict, and checks the keys on
  every documented exit code (§5.4).

## 3. Before the draw

### 3.1 The rule

Before the draw, `Bootstrap` runs no subprocess but `herdr workspace list`.
Everything else the first frame needs is a local read.

| stays before the draw | why |
|---|---|
| `herdrc.ParseContext` | a refusal (`Bootstrap`'s doc comment lists the three) |
| `runner.WorkspaceList` | the refusal for an unreachable herdr, and the data behind the project candidates, the title panel's sessions and the open-workspace defaults tier. It has no deadline; §10 says why that is out of scope |
| `config.Load`, `config.LoadState`, `config.LoadProjects`, `theme.LoadHerdrPalette`, `pathx.Home` | local files. A `config.toml` that fails to parse still refuses |
| Linear: whether a key source is configured | pure: `api_key_cmd` is set, `$LINEAR_API_KEY` is non-empty once trimmed (as `ResolveAPIKey` reads it), or an inline `api_key` is set |
| Linear: `linear.LoadCache` | a local file, now read whenever a key source is configured (decision 4) rather than only after a key resolved |
| Linear: the whole resolve, **when `api_key_cmd` is empty** | `$LINEAR_API_KEY`, or the inline key plus one `stat` of `config.toml`; no process. Those users' first frame is today's, apart from ruling 10's `fetching…` phase |
| clauth: the status file and `exec.LookPath` | §3.2 |

| moves into `Init` | how |
|---|---|
| `api_key_cmd` | `resolveKeyCmd`, appended by `Bootstrap` after `New` returns (§4.4, §5.1) |
| `clauth status --json`, when the status file is not fresh | the existing `reloadClauthCmd` (§5.3) |
| the picker's probe | merged into the opening `--dry-run` preview (§3.3, §5.4) |

`Bootstrap` still refuses on the same three conditions, and still before
anything is drawn.

### 3.2 clauth's fast half

`clauth.Load` already runs in two halves: the status file, then the CLI. The
fast half becomes its own call, and it gives one of three answers:

| status file | CLI on `PATH` | answer | row |
|---|---|---|---|
| fresh | not asked | the status | as today: live with two or more profiles, absent with fewer |
| not fresh | yes | ask the CLI | `loading…` (§4.2) |
| not fresh | no | not installed | absent, as today |

The `PATH` check is ruling 6. Today's CLI run already performs it:
`exec.Command` resolves the name before it starts anything, and
`exec.ErrNotFound` is how `Bootstrap` tells "not installed" (no row) from
"installed and broken" (a row with a reason). `LookPath` asks the same
question before the draw and classifies the same way. Not found means absent.
Any other `LookPath` error means the row, whose CLI run then fails with that
error on it. `exec.Command` resolves a bare name through `LookPath`, and
production's `CLIBin` is the bare `clauth`. An empty `CLIBin`, which only
tests build, answers "ask the CLI", so `Status` fails with `Load`'s own
error and the row is unavailable, as it is today.

`internal/clauth` gains the call, `Peek`, returning the status and a
three-valued `Cached`, and `app.clauthSource` gains the method. `create`'s
own one-method `ClauthSource` is untouched, and `create` keeps calling
`Status`.

### 3.3 The picker

A named picker is no longer probed by `Bootstrap`. `Deps.Picker` is set to
`picker.CLI{Bin}` unprobed, and `Setup.PickerProbePending` says so (§4.1).
The probe's `--json --dry-run` is the same invocation the opening preview
makes, against the same directory, so the two become one call: the first
preview also applies the probe's shape check (§5.4).

With a live `account` row, the first frame is therefore today's
`auto · asking the picker…`, because that row already opens with its preview
pending. With no `account` row at all, no probe runs. Today one runs anyway,
and its verdict is shown nowhere.

## 4. The row set and `New`

### 4.1 `Setup`

Three fields, each mirrored on `Model` the way `LinearUnavailable`,
`ClauthUnavailable` and `PickerUnavailable` already are, so a ⌃R⌃R rebuild
reconstructs the same state (§7.4):

- `LinearResolving`: `api_key_cmd` is configured, so the key lands after the
  draw. The command is the first source `ResolveAPIKey` tries, so a
  configured command makes the resolve asynchronous whatever else is set.
- `ClauthLoading`: the status file was not fresh and the CLI is on `PATH`.
- `PickerProbePending`: `Deps.Picker` is the named executable, not yet
  vetted.

**Zero means "as before"**, `plan.ExecOpts`' convention, with one
exception: ruling 10's phase. A `Setup` with `Deps.Linear` set now shows
`fetching assigned issues…` while its opening fetch is out, and with no cache
its `issue` row reads `loading…`. §9.1 lists the frames this moves. Nothing
else an existing test builds changes, and no `plan.Input` changes, so
`internal/create`'s equivalence test holds (§8.3).

### 4.2 The rows

`issue`, first match wins:

| `Setup` | row |
|---|---|
| `Deps.Linear != nil` | as today: the cached list, with a refresh scheduled; and ruling 10's `fetching…` phase (§6) |
| `LinearResolving` | live; the cached list pickable; a loading note (§6) |
| `LinearUnavailable`, with a cache | live; the cached list pickable; the reason as its note (#292) |
| `LinearUnavailable`, no cache | as today: inert, `unavailable  <reason>` |

The #292 reason goes where a failed refresh's reason already goes,
`IssueField.SetRefreshError`, because it is the same situation: a cached list
that could not be refreshed. `IssueField` gains one setter, for the loading
phase. It also ignores input while unavailable, as #191 made `AccountField`
do, because a row can now turn inert while it is focused.

`account`:

| `Setup` | row |
|---|---|
| `ClauthUnavailable` | as today: inert, with the reason |
| `ClauthLoading` | inert, `loading…`, not a focus stop |
| clauth enabled, with two or more profiles | as today: live |

`AccountField` gains a loading state beside its unavailable one, since
`Enabled`, `RetryOnFocus`, `Row`, `Panel` and `FooterRungs` all branch on
state. Loading takes precedence over the "not claude" hint, as unavailable
does today.

A loading row is inert because there is nothing to choose yet, and because a
choice made there would not survive. Its only entry is `active`, committing
`active` leaves the pin empty, and when the profiles land
`SetPickerAvailable` makes `auto` the selection over an empty pin, silently
overriding the choice. It is not a focus stop, unlike an unavailable row
(`RetryOnFocus`, #200), because there is nothing to retry: the read is
already out. For the same reason the focus-triggered reload in
`reactToChanges` skips a loading row. A second read would retire the first.

### 4.3 The interfaces

- `app.clauthSource` gains `Peek() (clauth.Status, clauth.Cached)` (§3.2).
- `picker.Options` gains `Probe`, which makes `Pick` also apply the probe's
  check that the answer carries the documented keys. `CLI.Probe` folds into
  it. `pickerSource` itself is unchanged.
- The key gets no interface. `Bootstrap` builds the resolver as a closure
  over `linear.ResolveAPIKey`, which it calls directly today, and hands it to
  `resolveKeyCmd`, which takes it as a parameter so that its tests can pass a
  fake.

### 4.4 `initCmds`

- `New` adds `reloadClauthCmd` when `ClauthLoading`, so a rebuild asks clauth
  again (§7.4).
- The opening preview carries the probe while `PickerProbePending`. `New`
  schedules the preview for any `account` row that exists, a loading one
  included, exactly as it schedules one today for an unavailable one. The
  answer is stored on the field and shows once the row is live.
- **`resolveKeyCmd` is not `New`'s.** `Bootstrap` appends it after `New`
  returns. A ⌃R⌃R rebuild goes through `New` and not `Bootstrap`, so it never
  runs `api_key_cmd` again: a second `op read` is a second approval prompt.

## 5. Delivery and cancellation

### 5.1 The key

`resolveKeyCmd` runs `linear.ResolveAPIKey` on `lt.Begin()` and returns a
`linearKeyMsg{src, err}`. The message is **unversioned**: there is one per
popup, and it lands on whichever form is current. `handleLinearKey` clears
`linearResolving`, then:

- **on success**, sets `m.deps.Linear = src`, sets the phase to "fetching"
  and returns the existing `refreshLinearCmd`;
- **on failure**, records `m.linearUnavailable` and clears the phase. With a
  list on screen it calls `SetRefreshError(reason)` and the list stays
  pickable (#292). With nothing to pick it calls `SetUnavailable(reason)`,
  today's inert state.

A submit never waits for any of this (§7.1).

### 5.2 The refresh keeps the pick

`handleLinearResult` increments `issueItemsVersion` before `SetIssues`, and a
higher version resets the picker's cursor to `none`. That contradicts
`SetIssues`' own doc, which says a refresh over the cache-rendered list lands
at an unchanged version and keeps the selection by issue identifier. No
`IssueChosenMsg` fires either, so the row reads `none` while the title,
branch and prompt the pick seeded stay. Today that loses only a pick made
before the first refresh lands. With this design, picking from the cache
while `op read` waits is the intended flow, so every such pick would be lost.

So:

- the refresh lands at the **same** version, and the selection is preserved
  by identifier;
- if the refresh dropped the issue the user chose, the field keeps that issue
  in its list. Without this, a same-version `SetItems` falls back to the old
  *index*, which silently shows a different issue as the chosen one.

`linear.SaveCache` and `m.linearIssues` still take exactly what Linear
returned.

### 5.3 clauth

The stale case is the existing `reloadClauthCmd`, fired from `Init`.
`handleClauthResult` routes to `recoverAccountRow` when
`clauthLoading || clauthUnavailable != ""`, and `recoverAccountRow` clears
`clauthLoading` on every outcome. Its outcomes are #200's, unchanged:

- a failure: unavailable, with the reason;
- fewer than two profiles: unavailable, with `accountTooFewProfilesReason`
  (decision 2's "the row stays");
- otherwise: live, through `populateAccountRow`.

`accountTooFewProfilesReason`'s comment calls this "the one state a reload
can produce that opening the popup never does". That stops being true, and
the comment changes with the code.

### 5.4 The probe's verdict

While `pickerProbePending` is set, every preview passes `Options.Probe`, and
`pickerPreviewMsg` records that it did. **The first probed answer to land
decides, ahead of both of `handlePickerPreview`'s guards**: whatever its
version, and whether or not a submit is out on a round trip
(`submitResolving`). The verdict is about the executable, not the directory
or the moment, so neither may discard it. A dropped verdict would leave
`pickerProbePending` set for the rest of the popup, and every `auto` submit
refused (§7.3).

`Options.Probe` checks exactly what `CLI.Probe` checks today, and nothing
more: the executable ran and answered; its exit code is one the protocol
documents; and its stdout carries the documented keys. The key check runs on
every documented exit code, before `Pick` builds a `*picker.RefusalError`,
so a same-named program that exits 2 fails it. A failure of those three
comes back as its own error type (`ProbeError`). `Pick`'s other plain errors,
such as exit 0 with no profile or a payload that does not decode, are not
verdicts. They reach the row as a preview's do today.

- **Pass:** anything but a `ProbeError`. The pending flag clears.
- **Fail:** a `ProbeError`. The pending flag clears. `m.pickerUnavailable`
  takes the flattened reason, `m.deps.Picker` becomes nil, and
  `SetPickerAvailable(false)` removes `auto`. On a live row the notes are
  rebuilt, through a helper factored out of `populateAccountRow`. A loading
  or unavailable row carries no notes (`populateAccountRow`: only a live row
  does) and gets them when it goes live. The row then reads what today's
  failed probe produces, arrived at after the first frame instead of before
  it.
- **A Fail while a submit waits on the probe refuses that submit** (§7.3).
  Removing `auto` would otherwise release the hold, and the submit would
  launch unpinned under whatever account is live, which `handlePickerCommit`
  exists to prevent.

The versioned half, applying the answer as the preview, keeps both guards.

### 5.5 The `Lifetime`

- `resolveKeyCmd` runs on `lt.Begin()`, so `esc` during an approval kills
  the `op read` process rather than leaving it waiting for a popup that is
  gone. An approval dialog that another process put on screen may outlive
  it; closing that is the helper's business. `Lifetime.Shutdown`'s grace is
  sized for the kill (#211) and covers it.
- `reloadClauthCmd` moves onto it too. The open-time read and the focus
  reload are one function, and CLAUDE.md puts every new shell-out from a
  `tea.Cmd` on the `Lifetime`.
- `refreshLinearCmd` stays on `context.Background()`. It is an in-process
  HTTP request, and nothing of it outlives the process.

CLAUDE.md's "Quitting the popup kills the work it started" lists clauth and
Linear among the "reads nobody has needed cancelled". This design moves
clauth and the Linear key off that list, and leaves the Linear fetch on it
with the reason above. The bullet changes with the code (§11).

### 5.6 The budgets

The 60s, 30s and 30s budgets are unchanged. The comments that justify them by
the moment before the draw describe a wait that is now a note on a drawn
form, and change with the code: `linear.keyCmdTimeout`'s "the popup's blank
pane is the price", and the comments on `clauth.cliTimeout` and
`picker.pickerTimeout` that place their calls before the popup is drawn.
`pickerTimeout`'s own principle, that a hang before the draw is "neither
visible nor recoverable", is what this design finally meets for all three
calls.

## 6. What the states say

**The row changes only when there is nothing to pick.** Otherwise the row
shows the value and the phase goes on the panel's status line. That follows
v2 spec §6's "verdicts render in the panel, never appended to the row", and
keeps a cached pick visible while the key resolves. Every word below is in
`DimText` (`dimText` and `dimHint`), the colour `unavailable  <reason>` and
the dim `none` already use. #310 floors `DimText` at `DimTextContrastFloor`,
3:1, on the three grounds a word is drawn on: the panel, the focused row
and a picker's cursor row. The design introduces no new colour, so it needs
no new floor. Every row is one line built from state alone, so `Row(w)`
keeps its contract.

| state | row | panel status line |
|---|---|---|
| `issue`: key out, a cache | the value (`none` or the chosen issue) | `waiting for api_key_cmd…`, with the count |
| `issue`: key out, no cache | `loading…` | `waiting for api_key_cmd…` |
| `issue`: fetching, a cache | the value | `fetching assigned issues…`, with the count |
| `issue`: fetching, no cache | `loading…` | `fetching assigned issues…` |
| `issue`: key failed, a cache (#292) | the value | `<reason>`, with the count: today's failed-refresh shape |
| `issue`: key failed, no cache | `unavailable  <reason>`, inert (today) | `<reason>` |
| `account`: loading | `loading…` | `reading clauth profiles…`, no list, and the footer's `nothing to set here` |

- **`waiting for api_key_cmd…`** names the key the user wrote, which v2 spec
  §6.1 asks of a reason. "Waiting" is also honest: the helper may be waiting
  on a person.
- **"Fetching" changes today's ordinary open too** (ruling 10). v2 spec §6.1
  specified `fetching assigned issues…` and nothing implemented it. Without
  it, a first open with no cache says `no assigned issues` while the fetch is
  still out: the statement `IssueField.panelStatus`' own comment calls false,
  because "nobody looked at the queue".
- **Where the phase is set and cleared.** `waiting for api_key_cmd…` is set
  by `New` while `LinearResolving`, and replaced by `handleLinearKey` on
  either outcome (§5.1). `fetching assigned issues…` is set wherever a fetch
  is scheduled, in `New` and in §5.1, and `handleLinearResult` clears it on
  either outcome.
- **Precedence on the status line** keeps `panelStatus`' order and adds the
  phase to it. An unavailable field's reason comes first. Then, with no
  issues at all, the phase while one is set, and otherwise what it says
  today (a failed refresh's reason, or `no assigned issues`). Then a filter
  matching nothing, `no matching issues`. Then the phase while one is set.
  Then a failed refresh's reason. The phase and a failure's reason are never
  set together: every landing that records a reason clears the phase.
- **A ⌃R⌃R during a fetch** leaves the discarded form's fetch to land on the
  rebuilt one, since `handleLinearResult` carries no version. Its list is as
  fresh as the one being fetched, so it is applied, as today, and it clears
  the phase. The rebuilt form's own fetch then lands over it.
- **`linearUnavailableReason` flattens**, as `clauthUnavailableReason`
  already does. Today a multi-line `api_key_cmd` stderr reaches a one-line
  row unflattened.

## 7. ⌃S and ⌃R⌃R during a load

### 7.1 The holds

Two holds, each at the point where the account is read, and neither ahead of
a validation that does not need the account:

- **The clauth hold** sits in `checkSubmitValidation`, immediately before
  `accountAuthBlocked` (#243), after every other validation. It holds while
  `clauthLoading` is set.
- **The probe hold** sits in `continueSubmit`, before the commit pick. It
  holds while `pickerProbePending` is set and the selection is `auto`,
  including §7.2's `auto`. A commit pick writes the picker's ledger, and must
  never run an executable that has not answered the probe. Because it sits
  after validation, it freezes the form as the commit pick's own round trip
  does (`submitResolving`, #136): an edit made during the wait would
  otherwise reach a plan that validation never saw.

Both apply only when the agent is `claude`, the only kind an account is
pinned or picked for (`accountPin`, `ResolveAccount`). A codex submit never
waits on clauth or a picker. A required title, a duplicate, or a branch git
cannot use is refused at once, as today, and never after a wait for an
account that could not have changed the answer. #202's three holds stay
where they are, ahead of validation, because validation reads their
verdicts.

**One budget, armed once per ⌃S.** The bound is `checkBudget()`'s deadline:
`blockingCheckDeadline`, 5s, with its test override. The first time either
account hold holds for a given ⌃S, it arms a timer message that carries a
version, `accountWait` in `reqVersions`. Re-entries from #202's landings,
and the other account hold, do not re-arm it, so nothing pushes the deadline
back. When it fires, the Model records `accountWaitExpired` and re-enters
the step that is holding. The submit goes on holding for any of #202's
checks that is still out, but no longer for the account. The next ⌃S (a new
`form.SubmitMsg`, not a re-entry) clears the flag and may arm a new timer. A
clear starts from a fresh Model, and `superseded()` retires a pending timer
with the other counters (#201).
`TestClear_EveryCounterStartsPastTheDiscardedForms` lists those counters by
hand, and gains `accountWait`.

The wait is a timer rather than `awaitCheck` (ruling 15). The clauth
landing, the probed preview and the timer each re-enter the step that is
holding while `submitHeld` is set, as `handleDirResult` and its siblings
already re-enter `handleSubmit`.

**Linear never holds a submit.** The `issue` row is optional, and a pick from
the cache is already a complete choice.

### 7.2 clauth at the deadline: proceed

Decision 5: the create goes ahead without the #243 dead-credential check,
since there is no status to check against. That is clauth's advisory
posture, which `create` has taken since #141.

**Which account (ruling 11).** The row has no profile list, so
`AccountField.SetPin`, which pins only a profile its list contains, cannot
apply `[clauth] default`, and the field's own `Pin()` is empty. The create
launches under **what the row would have selected, from config alone**:

1. a `[clauth] default` naming a profile, pinned without verification, as
   `create` pins it;
2. otherwise, with a picker configured whose probe has not failed, `auto`:
   the commit pick runs once the probe has passed (§7.1's probe hold), and a
   probe still pending when the budget is spent is §7.3's refusal;
3. otherwise, unpinned.

This is the live row's own precedence: `SetPin`'s comment has a named
profile beat `auto`, and `auto` is the resting selection over `active`. One
divergence is disclosed: a mistyped default. The live row drops it ("never
guess"). This path types `clauth start <typo>` into the pane, which is what
`create` does with it.

**Where the answer is kept.** It is recorded on the Model when the spent
budget lets the submit past the clauth hold, in a field beside `autoPick`
(`accountFromConfig`, following `WithAccount`'s pattern). `accountPin`,
`continueSubmit` and `ResolveAccount` read it ahead of the field. That keeps
`buildPlanInput` a pure read of state. The next ⌃S, or a clear, discards it.

A clauth answer that lands after that point, while the submit is out on a
round trip, updates the row and not the submit in flight. `updateResolving`
lets `clauthResultMsg` through, but the recorded answer stands, so a profile
list that arrives later cannot change the account of a create that
validation has already passed.

### 7.3 The probe: refuse

The probe hold refuses the submit in two cases: its budget runs out while
the probe is still pending, or the probe fails while the submit waits on it
(§5.4). Both follow #202's held checks, where an unknown is not a pass, and
`handlePickerCommit`, which refuses rather than launch unpinned. Focus moves
to the `account` row, and its panel's status line gives the reason:
`picker has not answered yet` for the first case, in #202's
could-not-check register, and the probe's own reason for the second. On a
loading row the refusal takes the status line ahead of
`reading clauth profiles…` until the next ⌃S, and the panel still draws no
list. After a pending probe the next ⌃S holds again. After a failed one,
`auto` is gone and the next ⌃S goes ahead under the selection the row now
shows.

### 7.4 ⌃R⌃R

`handleClearRequested` rebuilds the form through `New` from the Model's own
state. `recoverAccountRow`'s rule applies to every new state: each transition
writes the Model's mirror first, and the field is derived from it, so a
rebuild cannot put back a state that has since moved on.

- **The key:** `LinearResolving` is carried. Its message is `Bootstrap`'s
  and unversioned, so it lands on the rebuilt form, and `api_key_cmd` never
  runs twice (§4.4).
- **clauth:** `ClauthLoading` is carried, and the rebuilt `New` asks again.
  `superseded()` has already moved `reqs.clauth` past the discarded form's
  read, so that answer is dropped (#201). The cost is a second
  `clauth status --json`, which nobody has to approve.
- **The probe:** `PickerProbePending` is carried. The verdict is unversioned
  (§5.4), so an answer still in flight from the discarded form decides it.
- **A held ⌃S does not survive a clear**, as today.

## 8. `create` and `ResolveAPIKey`

### 8.1 `create` does not change shape

`create` has no draw, so its pre-flight stays synchronous. It resolves the
key only under `--issue`, and reads clauth only to check a named pin. There
is no new flag, no new exit code, and no change to its own `ClauthSource`. It
never probes a picker (`accountPicker`'s comment says why), so
`Options.Probe` does not reach it.

### 8.2 Decision 3

`linear.ResolveAPIKey`: an `api_key_cmd` that exits 0 and prints nothing
still falls through to `$LINEAR_API_KEY`, then to the inline `api_key`. Only
when neither supplies a key does it return an error,
`resolve linear api key: api_key_cmd printed nothing`, where today it returns
`("", nil)`. With no `api_key_cmd` configured, `("", nil)` still means "not
configured". An inline `api_key` in a `config.toml` wider than 0600 still
returns the permissions error, not this one.

- **The popup** treats it as any other failure (§4.2): the #292 state with a
  cache, `unavailable  api_key_cmd printed nothing` without one.
- **`create --issue`** says
  `--issue ENG-1: resolve linear api key: api_key_cmd printed nothing` and
  exits **2**. That is the code "Linear is not configured" already has, now
  with the true reason: the old message told the user to set a key that was
  set. `TestIssueWithoutLinear` configures no key source at all and keeps its
  message. `create` without `--issue` never resolves a key.

### 8.3 `equivalence_test.go` holds by construction

- It builds the form with `app.New` and a `Setup` whose three new fields are
  zero, which §4.1 defines as today's form.
- It injects `Deps.Linear`, so neither side ever reaches `ResolveAPIKey`.
- Its `Deps.Clauth` is the production `app.NewClauthSource(clauth.LoadOpts{})`,
  which gains `Peek` and still satisfies the interface.

No scenario changes, and none is added. §7.2's rule agrees with `create`'s
wherever both apply: a profile default is pinned without verification. Where
they differ, a picker with no default, they already differ on the live row:
the popup rests on `auto`, and `create` without `--account` does not pick.

### 8.4 The skill document

Unchanged. It names neither message, and its exit table already files an
unresolvable request under 2.

## 9. Testing

### 9.1 Golden frames

A golden-frame suite proves only the states someone fixtured, so the new
opening state is pinned at every size, as v3 spec §12 requires of the
existing one.

New in `internal/app`:

- `assembled-opening-loading-{57x18,77x22,101x30,150x44}`: the key out with a
  cache, clauth loading and the picker probing. This is the first frame
  someone with an `op read` helper and no clauth daemon sees: `title`
  focused, `issue` reading `none` and `account` reading `loading…`.
- `assembled-issue-waiting-101x30` and `assembled-issue-waiting-empty-101x30`:
  the key out, with and without a cache.
- `assembled-issue-fetching-empty-101x30`: a first run, with the key
  resolved and no cache.
- `assembled-linear-key-failed-cached-101x30`: #292's state.
- `assembled-account-loading-101x30`: focused through `FocusByID`, since the
  ring skips that row.
- `assembled-account-loading-refused-101x30`: §7.3's refusal on a loading
  row, the one panel whose order is new.

Frames that move: any frame that draws the `issue` panel while the opening
fetch is out gains `fetching assigned issues…` on its status line (§6).
**Each such diff is exactly that line**; anything more is a defect.

In `internal/form`: the `issue` panel waiting, with and without a cache, and
the `account` row and panel loading. `field_rows_test.go`'s check that `Row`
renders the same at two heights is extended to the new states; it renders
only the states it lists.

### 9.2 Unit tests

In `internal/app`, one test per cause, each proven by mutating that cause
alone:

- `Bootstrap` runs no subprocess but `WorkspaceList`. With `api_key_cmd`
  set, a stale fake clauth and a picker configured, it never calls `Status`,
  never resolves the key and never probes. The three `Setup` fields are set,
  and `Init` carries both loads.
- The key landing's three outcomes. It lands on a ⌃R⌃R rebuild, and the
  rebuilt `Init` carries no second resolve.
- The refresh keeps the pick, whether the new list contains the chosen issue
  or not, and emits no `IssueChosenMsg`.
- clauth from `loading…`: its three outcomes. The focus reload is suppressed
  while loading, a rebuild asks again, and the old answer is dropped.
- The probe: the first probed answer decides even across a version bump. A
  failure removes `auto`, adds the note and clears `Deps.Picker`.
- The holds:
  - a landing releases each;
  - the timer applies §7.2's three cases with no #243 check, and records the
    answer where `accountPin` and the pick read it;
  - a clauth answer landing during the round trip after that does not change
    the account;
  - a pending probe refuses at the deadline;
  - a probe that fails during the hold refuses rather than launching
    unpinned;
  - a probed answer landing while `submitResolving` still decides;
  - a codex submit never holds;
  - a required title is refused at once while clauth loads;
  - the timer is armed once per ⌃S, and re-entries do not re-arm it;
  - a stale timer never releases a later hold, and
    `TestClear_EveryCounterStartsPastTheDiscardedForms` includes
    `accountWait`.
- `Lifetime.Shutdown` cancels both the key resolve and the clauth read.
- `linearUnavailableReason` flattens multi-line text.
- The existing test that `Bootstrap` degrades on a broken `api_key_cmd`
  (finding I5) moves: the reason now appears after the `Init` command runs.

Elsewhere:

- `internal/linear`: printed nothing with no fallback returns the error;
  printed nothing with `$LINEAR_API_KEY` set still falls through; and a truth
  table for "a key source is configured".
- `internal/clauth`: `Peek`'s three answers, with `PATH` set by `t.Setenv`
  and nothing executed.
- `internal/picker`: `Options.Probe`'s verdict is today's `Probe` verdict.
  Exit 0 with no profile still passes, and a program that exits 2 without the
  documented keys fails.
- `internal/create`: the equivalence test unchanged and green.

### 9.3 Manual smoke

A recorded run in `docs/manual-smoke.md` at implementation, on #141's
harness: `$HERDR_BIN_PATH` a stub that answers only `workspace list`, a
scratch `$HOME`, `api_key_cmd = ["sleep", "600"]`, and a `clauth` stub first
on `PATH`. Two things differ from #141's run, because they decide whether
the run can fail:

- **The stubs `exec` their sleep** (`#!/bin/sh` then `exec sleep 600`), so
  the process the `Lifetime` kills is the sleep itself, not a shell whose
  child outlives it.
- **The binary runs with `SIGHUP` ignored**, which its children inherit.
  Under `pty.fork()` the popup leads the pty's session, and its exit sends
  `SIGHUP` to its process group, which would kill every `sleep` whether or
  not the `Lifetime` did. The alternative is to assert each child dead while
  `herdr-draft` is still alive.

Then:

- **The first byte is painted in under a second.** #141 measured 60.1s on
  the same harness.
- **`esc` with both loads out leaves no `sleep` alive** within the grace.
- **⌃S while clauth sleeps holds for 5s, timed, and then goes ahead.** The
  stub herdr refusing step 1 is proof enough that it did.
- **With a seeded `linear-cache.json`, `$LINEAR_API_KEY` unset, no inline
  key, and `api_key_cmd = ["sh", "-c", "sleep 15"]`**: the cached list is
  pickable during the wait. The panel then reads
  `api_key_cmd printed nothing`, and the pick stays. No network is involved.

## 10. Out of scope

- **A deadline on `herdr workspace list`.** It is the refusal for an
  unreachable herdr, and herdr is the process that draws the popup, so one
  that cannot answer `workspace list` is unlikely to draw it either.
  Bounding it is a separate question.
- **Hiding a cached list by age.** `linear.LoadCache` returns a timestamp
  that nothing reads, which #292 raised. A cached issue only seeds editable
  text, and decision 4 keeps the list on failure without a limit.
- **Changing the budgets.** #141's decision 4 set the same budget on both
  paths, and nothing here reopens it.
- **Probing in `create`.** `accountPicker`'s reasons stand.

## 11. What moves with the implementation

These describe the code as it is, so they change with the code and not
before:

- **CLAUDE.md**:
  - the `internal/app` architecture bullet (`Bootstrap`'s pre-open work);
  - "Quitting the popup kills the work it started" (§5.5);
  - "A check that holds a submit is bounded": the account holds, their
    clauth timeout that proceeds (§7), and ruling 15's exception to "the
    same bound, from the same two helpers".
- **README**:
  - the note under the row table (`issue` absent unless Linear is
    configured, and `account` a static startup check);
  - `[linear]`: "simply not rendered"; "configured but fails … It can't be
    focused"; "The popup resolves the key before it draws";
  - `[clauth] picker`: "probed once at startup (one `--json --dry-run`)";
  - the paragraph after `[clauth]`'s keys: "a static, startup-time check",
    and the `auto` row appearing "only when `picker` is set and its probe
    succeeded";
  - the Account picker protocol section's passages on "the startup probe"
    and on what happens "At startup, once";
  - Troubleshooting's "There is no `account` row", and "The `account` row
    says `unavailable`", which can now mean clauth reports one profile;
  - "The `issue` row says `unavailable`";
  - the `linear-cache.json` entry in the state-file list.
- **Code comments** whose claims this design falsifies. They include:
  - `Bootstrap`, `New`'s Linear and account comments, `Init`, and
    `Model.issue`/`account`;
  - `Setup.LinearUnavailable`, `Setup.ClauthUnavailable`, `Deps.Linear`,
    `Deps.Picker` and `linearSource`;
  - `linear.keyCmdTimeout`, `clauth.cliTimeout`, `picker.pickerTimeout`,
    `linear.ResolveAPIKey` and `linear.runKeyCmd`;
  - `reloadClauthCmd`, `handleClauthResult`, `recoverAccountRow`,
    `populateAccountRow` ("two callers") and `accountTooFewProfilesReason`;
  - `submitHeld` and `blockingCheckDeadline`, which count three holds;
  - `linearRefreshReason`, whose flattening its "sibling does not need";
  - `AccountField`'s type doc, `SetUnavailable`, `SetPickerAvailable`
    ("has been probed") and `pickerAvailable`;
  - `IssueField.Enabled` and `IssueField.refreshErr`;
  - `picker.CLI`, `create`'s `accountPicker`, and `main.go`'s file header.

  The list is where to start, not proof that it is complete. The greps that
  find the rest are `static precondition`, `>= 2 profiles`,
  `BEFORE the popup`, `before the popup is drawn` and `probed`.
- **`docs/manual-smoke.md`**: §9.3's recorded run.
- **`CHANGELOG.md`**.
- **The amendment blocks** this document adds to earlier specs lose their
  "approved, not yet implemented" clause (ruling 14).

## 12. Amended text, itemised

| where | sentence | status |
|---|---|---|
| v1 spec §6 | "Fields whose precondition is static at startup (Linear unconfigured, fewer than two clauth profiles) are simply not rendered." | Linear's precondition is static: a key source is configured. clauth's is static only when its status file is fresh. Otherwise the row is drawn `loading…`, and fewer than two profiles leave it with a reason (§3.2, §4.2) |
| v1 spec §6, field 1 | "rendered only when Linear is configured" | "configured" means a key source is set, not that a key resolved (§3.1) |
| v1 spec §6, field 7 | "rendered only when clauth is configured and ≥2 profiles exist (static)" | as the first row (§3.2) |
| v1 spec §8 | the clauth row's "form-open + on account focus" | the form-open read is split: the status file before the draw, and the CLI after it when the file is stale (§3.2, §5.3) |
| v1 spec §8 | the Linear row's "form-open, async; cache-first" | the key is async too, when it comes from `api_key_cmd` (§5.1) |
| v1 spec §9, as #243 amended | a pinned account whose credential clauth reports dead blocks the submit | not checked when ⌃S outlasts a clauth load; the pin then comes from config (§7.2) |
| v1 spec §10 | "Absent → the Linear field is not rendered" | an `api_key_cmd` that prints nothing, with nothing to fall through to, is a failure with a reason (§8.2) |
| v1 spec §10 | "Cache: last response in state dir, rendered instantly at form-open with an async refresh" | also kept, pickable, when the key fails (§4.2) |
| v1 spec §13 | "Linear/clauth/network failures degrade the respective field to inert with a reason" | a key failure with a cache degrades to a pickable list with the reason, as a failed refresh already did (§4.2) |
| v2 spec §6.1 | "Loading. The row reads `loading…` in dim; the panel says what is being read" | specified here for the first time; the row reads `loading…` only when there is nothing to pick (§6) |
| v2 spec §6.1 | "Absent by design. Linear unconfigured, or fewer than two clauth profiles, remain static startup checks that omit the row entirely." | as v1 spec §6 above |
| v2 spec §13, as #141 amended | "In the **popup** none of the three refuses. Each degrades the row that needed it, with the reason on the row … a shorter one before the draw would report a working credential helper as broken" | none of the three waits before the draw any more (§3). A key failure with a cache puts its reason on the panel, and the list stays pickable (§4.2, §6). The budgets are unchanged (§5.6) |
| v3 spec §12 | "`TestAssembledForm_OpeningState` must loop over sizes" | extended to the loading opening state (§9.1) |
