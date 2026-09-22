# herdr-draft — per-agent session options: model, effort, permission mode

- **Date:** 2026-09-18
- **Status:** approved by the owner 2026-09-18 (§3 records the rulings).
- **Reverses:** v1 spec §16 item 3 and v2 spec §16's "model/effort/permission
  chip fields". Both are recorded as declined. §2 says why the reasoning
  behind that no longer holds, and it is the part of this document to read
  first.
- **Amends:** v3 spec §4 (the row set gains `options`), v3 spec §7.2
  (`panelCapRows` is re-derived for nine rows), v2 spec §8 (the key grammar
  gains the `options` zone), v2 spec §11 (one more forbidden table), v2 spec
  §13 (three more flags), and v1 spec §12 (one more `config.toml` table). §12
  itemises every sentence that stops being true. Every section named here
  stays authoritative apart from the sentences §12 lists.
- **Does not amend:** v2 spec §10's memory tiers. Nothing here is remembered
  (§6.1).
- **Citations.** Claude Code's flag vocabulary is quoted from `claude --help`
  of **claude 2.1.276**, run 2026-09-18. It is not in this repository and can
  drift. §9 says how that is checked.

---

## 1. Summary

Atrium let you choose Claude's model, effort level and permission mode for
each session: three chip rows, plus `atrium new --model/--effort/--permission-mode`.
herdr-draft can do neither. The only mechanism is `[agents.extra_args]` in
`config.toml`, one static argv per agent kind, which applies to every session
and is never shown.

This adds:

- **A per-kind declaration.** `internal/agentopts` states, for each agent
  kind, the session options it takes: their names, values and flags. Claude
  is the only kind declared. Adding another is a change to that one file.
- **The form:** a new `options` row under `agent`. The row states the
  outcome, e.g. `opus · effort xhigh · plan mode`. Its panel holds one chip
  line per option. A kind with nothing declared gets one dim line,
  `none for codex`, and no focus stop.
- **`create`:** `--model`, `--effort` and `--permission-mode`, registered
  from the declaration.
- **`config.toml`:** `[agents.options.<kind>]` sets the default for both
  paths.
- **Visibility:** the panel shows what `[agents.extra_args]` appends, read
  only. It is the one setting that changes every launch and has never been on
  screen.

`inherit`, the first chip on every line, sends no flag, so the agent's own
settings decide. It is the default for every option.

> **Amended (2026-09-22; approved by the owner): the first chip is labelled
> with its outcome.** The first chip still sends no flag and is still the
> default, but it no longer reads `inherit`. Its label says what sending no
> flag leads to, as far as herdr-draft itself knows: the value
> `[agents.extra_args]` passes for that flag, followed by a dim `•`
> (`xhigh•`, or a typed model id such as `claude-opus-5[1m]•`), or
> `claude's` when `[agents.extra_args]` passes none, because claude's own
> settings then decide. What those settings hold is not read. They differ
> between account directories, some defaults depend on the plan, the
> environment can override them, and with an `auto` account picker the
> directory is not known until the submit, so a resolver would be guessing.
> `inherit` stays the word a person types: `create --effort inherit`,
> `config.toml` and the spawn skill are unchanged. §7.2's amendment gives
> the panel.

## 2. Why the recorded decision is reversed

v1 §16 item 3 reads: *"Model / effort / permission-mode chip fields — covered
by per-kind extra args; Atrium's own retrospective flagged the 'Claude-shaped
form' as a mistake (adapter-declared schemas were its intended fix). Do not
re-ship that mistake."* That is two claims, and neither survives.

1. **"Covered by per-kind extra args."** Extra args are one argv per agent
   kind, fixed in a file. They cannot differ between two sessions of the same
   kind, which is the whole of the request. Nothing displays them either, so
   a user who set `--model` there has no way to see, from the popup, what a
   session will run.
2. **"The Claude-shaped form."** Atrium's defect was specific, and its issue
   tracker names it: #690 is a non-Claude form spending 45% of its height at
   80×24 on three rows that each read "n/a — the program is not Claude
   Code". Atrium's own fix, #816, is "adapters declare their session-config
   fields; the create form renders from the declaration". That is this
   design. There is one row, not three. A kind that declares nothing costs one
   dim line that the focus ring skips, and adding a kind's options changes a
   declaration rather than the form.

`CONTRIBUTING.md`'s "What not to propose" list loses the item and points
here.

## 3. Decisions

Owner rulings, 2026-09-18:

1. **A new `options` row under `agent`**, rather than lines inside the agent
   row's panel. The agent panel's `↑↓` already drives a 23-kind list, and
   options there would be invisible until that row is focused.
2. **Defaults come from `config.toml` only.** Nothing is remembered between
   sessions (§6.1).
3. **Only Claude is declared.** Other kinds show `none for <kind>` and keep
   using `[agents.extra_args]`.

Decided in the design and not reopened:

4. **`bypassPermissions` and `dontAsk` are not offered**, on either surface
   (§4.3).
5. **An option that is set displaces the same flag in `[agents.extra_args]`**
   rather than relying on the agent taking the last of two (§5.2).
6. **Equivalence holds.** The form and `create` produce the same
   `plan.Input` from the same inputs, and every `create` flag has a form
   control behind it.
7. **`plan.Build` stays pure.** The chosen values arrive on
   `plan.Input.AgentOptions`, and Build turns them into argv.

## 4. The declaration

### 4.1 `internal/agentopts`

A pure package that imports nothing from this module. `plan`, `config`,
`defaults`, `app` and `create` import it. `form` does not: the app turns the
declaration into the form's own `OptionSpec` values and pushes them in,
which is how every other candidate list reaches the form.

An `Option` is a name (`model`, the `config.toml` key and the `--json` key),
a panel label, the agent's own flag (`--model`), the offered values, whether
a typed value is accepted, and how a chosen value reads in the row. `Values`
is a map from option name to value, nil when nothing is set.

The package's whole API is: look an option up, normalise a value (including
`inherit`), validate a set of values, build the argv, and find which declared
flags an argv already passes.

### 4.2 Claude's options

From `claude --help`, 2.1.276:

| option | label | flag | offered | row |
|---|---|---|---|---|
| `model` | model | `--model` | `fable`, `opus`, `sonnet`, `haiku`, or a typed id | the value |
| `effort` | effort | `--effort` | `low`, `medium`, `high`, `xhigh`, `max` | `effort xhigh` |
| `permission_mode` | mode | `--permission-mode` | `manual`, `plan`, `acceptEdits`, `auto` | `plan mode` |

A typed model id must match `^[A-Za-z0-9][A-Za-z0-9._:/\[\]-]{0,63}$`:

- **The leading character is alphanumeric.** A value can therefore never be
  read as a flag of its own.
- **Brackets are allowed.** The 1M-context ids are spelled
  `claude-opus-5[1m]`, and this user's own `extra_args` carries one. Atrium
  refused them because it ran the program through `sh -c`. Here Path A hands
  the value to herdr as an argv element with no shell, and Path B's
  `PaneRun` quotes every element for the pane's shell (#72), so no shell
  ever sees a bare `[`.

No model is validated against a list, as in Atrium. No headless channel
lists the models an account may use, so the running agent is the validator.

### 4.3 What is not offered

- **`bypassPermissions`.** It opens with a confirmation dialog. That dialog
  is the exact screen the prompt guard exists to catch, so offering the mode
  as a chip would make "create with a prompt" fail by design. It is also the
  escalation #166 refused to let an agent calling `create` hand a child.
- **`dontAsk`.** It denies anything not already allowed and never asks,
  which suits a script driving claude, not a person at a pane. Atrium left it
  out for the same reason.

Both remain one line away in `[agents.extra_args]`, which only the user
writes. Asking for either through `create` or `config.toml` is refused, and
the message says so. It does not just call the value unknown.

## 5. The launch

### 5.1 Build

`plan.Input.AgentOptions` carries the chosen values. `Build` refuses any
option that the kind does not declare, the way it refuses an account pin on
a non-claude kind. It then computes the agent's arguments once, and both
launch paths use them:

```
agentopts.Launch(kind, ExtraArgs, AgentOptions)
  = ExtraArgs with every set option's own flag removed
  + the set options' flags, in declaration order
```

- **Path A**, `herdr agent start ... -- <args>`: an argv vector. Nothing is
  quoted.
- **Path B**, the pinned-account launcher or wrapper: `<launcher> <args>`,
  quoted by `PaneRun`.

The existing caveat still applies to a `[clauth] launcher` of the form
`sh -c "…"`. Arguments appended after it become the inner shell's `$0` and
`$1`, so they never reach claude. That is already true of `extra_args`, and
`resolveLauncher`'s comment says so. The options inherit it unchanged.

### 5.2 Displacing `extra_args`

With `extra_args = ["--model", "opus"]` and `fable` chosen in the form, the
command line is `--model fable`, not `--model opus --model fable`. Both
spellings are removed: `--model opus` as two elements, and `--model=opus` as
one. Nothing depends on how claude treats a repeated flag.

An option left on `inherit` does not touch `extra_args`, and the form says
what `extra_args` will pass instead (§7.2).

## 6. Resolution

### 6.1 The chain: built-in, then `config.toml`

| tier | supplies it? |
|---|---|
| built-in | `inherit` (no flag) |
| `config.toml` `[agents.options.<kind>]` | yes |
| `last-used.json`, `projects.json` | **no** (ruling 2) |
| `.herdr-draft.toml` | **no, and refused with a note** (§6.3) |

`defaults.AgentOptions(cfg, kind)` is the only place the chain exists. Both
paths call it with their final kind. The form calls it again whenever the
agent row moves to a kind it has not seen in this popup.

This is the third value with a short chain, after `trust_repository` and
`mark_ready`. The reason is `mark_ready`'s (reap spec §6.1): whether a
session should plan first or think hard describes the task, not the
repository. A remembered `plan mode` would quietly carry into the next
session.

### 6.2 The config table

```toml
[agents.options.claude]
model = "claude-opus-5[1m]"
effort = "xhigh"
permission_mode = "plan"
```

`config.Load` validates it against the declaration. Nothing it rejects stops
the popup opening. Each rejection becomes a warning, which the popup shows
on the `options` panel and `create` prints to stderr:

- a kind with no declaration, e.g. `[agents.options.codex]`;
- a key the kind does not declare;
- a value it does not offer, including the two in §4.3, with their own
  reason;
- a value that is not a string.

`inherit` is accepted and means unset.

### 6.3 `.herdr-draft.toml` refuses it

`repoDeniedKeys` gains
`{"agents.options", "it becomes part of a launched agent's command line"}`.
The `agents` table is already rejected structurally, because the allow-list
is flat. The entry exists to give the note its reason, which is
`extra_args`'s reason.

## 7. The form

### 7.1 The row

`options` sits between `agent` and `account`. Its label is seven cells, so
`labelColWidth` does not change.

| state | row |
|---|---|
| nothing set, nothing in `extra_args` | dim `claude's own settings` |
| some set | `opus · effort xhigh · plan mode`, with inherited options left out |
| inherited, but `extra_args` passes the flag | that value, dim (`opus` in dim) |
| a kind with nothing declared | dim `none for codex` |

The last state is not `Enabled()`, so the focus ring skips it. `Update`
ignores input there, following `PlacementField`, because a click can still
focus an inert row (`form.go`'s `FocusByID`).

### 7.2 The panel

```
 ▸ model    inherit · fable · opus · sonnet · haiku · other
   name     claude-opus-5[1m]▏
   effort   inherit · low · medium · high · xhigh · max
   mode     inherit · manual · plan · accept-edits · auto
   sends --model claude-opus-5[1m]
   config.toml [agents.extra_args] adds: --verbose
```

> **Amended (2026-09-22; approved by the owner): the first chip is labelled
> with its outcome** (§1's amendment says why). With
> `[agents.extra_args] claude = ["--model", "claude-opus-5[1m]", "--effort", "xhigh"]`
> and nothing chosen, the panel reads:
>
> ```
>  ▸ model    claude-opus-5[1m]• · fable · opus · sonnet · haiku · other
>    effort   xhigh• · low · medium · high · xhigh · max
>    mode     claude's · manual · plan · accept-edits · auto
>    sends no --model of its own; [agents.extra_args] passes claude-opus-5[1m]
>    [agents.extra_args] adds: --model claude-opus-5[1m] --effort xhigh
> ```
>
> - The first chip's label is the value `[agents.extra_args]` passes, with a
>   dim `•` after it, when it passes one. Otherwise it is `<kind>'s`. An
>   offered value is spelled as its own chip is (`accept-edits•`). A typed
>   id needs no extra chip, because the first chip's label already names it.
> - Only the label changes. The chip still sends no flag, it keeps its
>   place and its internal ID, and `Values` still omits it. The explicit
>   chips are unchanged, so choosing the plain `xhigh` above still sends
>   `--effort xhigh` and displaces `extra_args` (§5.2). That is what keeps
>   a `config.toml` seed of `xhigh` meaning the same on both paths.
> - The `•` is drawn in the dim text colour on every chip, the cursor's
>   included, and counts toward the chip's width everywhere the chip row
>   measures one: the scrolled window, the cut markers and the click zone.
> - The two hint lines that named `inherit` become
>   `sends no --effort, so claude's own settings decide` and
>   `sends no --effort of its own; [agents.extra_args] passes high`.
>   The others are unchanged.
> - Wherever else this section says `inherit` for the chip, read "the first
>   chip". The row (§7.1) does not change. The panel above is the golden
>   frame `options-pinned-80x24` (§11).

- One part per option, in declaration order. `name` appears only while
  `other` is selected. It is a line input, like the worktree panel's branch.
- `↑↓` moves between parts, and `←→` moves the chips of a chip part. On
  `name`, every key but `↑↓` goes to the input. This is the worktree panel's
  grammar (`field_worktree.go`). Clicking a chip selects it and moves the
  part cursor there.
- **The hint line** says what the focused part will do:
  - `sends --effort xhigh`
  - `inherit sends no --effort, so claude's own settings decide`
  - when `extra_args` passes the flag: `inherit sends nothing of its own; [agents.extra_args] passes --effort high`
  - when a set option displaces `extra_args` (§5.2): `sends --model fable instead of [agents.extra_args]'s opus`

  > **Amended (2026-09-22; approved by the owner).** The second and third
  > now read `sends no --effort, so claude's own settings decide` and
  > `sends no --effort of its own; [agents.extra_args] passes high`. The
  > first chip no longer says `inherit`, so the hint does not either (see
  > the amendment under the panel above).

- The `extra_args` line shows the raw config value, and only when there is
  one.
- Warnings about `[agents.options]` (§6.2) come next, then a provenance line
  (`from config.toml`) while the values shown are the config file's own.
- **Shortening when rows are scarce:** the provenance line goes first, then
  the notes, then the `extra_args` line, then the hint. The parts go last,
  and when even they outnumber the rows (four parts with `other` open, at
  the three-row floor), the panel keeps a window of them around the cursor,
  so the part being changed is never the one cut.
- **The name input** refuses any edit that would make an invalid id, so
  pasting `--foo` does nothing. `other` with an empty name sends no flag,
  like `inherit`. A key typed on the model's chips selects `other` and
  starts a fresh name with that key, when the key is a valid start; any
  other key leaves the chips alone.
- **A configured value is matched against the offered values only.**
  `model = "other"` is a model id, sent as `--model other` by both paths,
  not a selection of the `other` chip.

A kind's values persist for the life of the popup. Going claude → codex →
claude brings back what you chose. `⌃R ⌃R` rebuilds the form and reseeds
every kind from `config.toml`.

### 7.3 Keys and footer

`ZoneOptions` is a plain zone: Tab and Enter advance, as on `worktree`. The
footer rungs are:

- on a chip part: `↑↓ option · ←→ value`
- on `name`: `type a model id · ↑↓ option`
- inert: `nothing to set here`

### 7.4 Height

A ninth row re-derives `panelCapRows` from 15 to 14 (v3 §7.2's recipe:
30 − 9 rows − 6 chrome lines − 1 inset top and bottom). The issue and prompt
panels, the only two it clips, show one line fewer at 101×30. The options
panel is at most eight lines with every extra showing.

## 8. `create`

### 8.1 Flags

`--model`, `--effort` and `--permission-mode` are registered from the
declaration. Each flag's name is the option's name with `_` spelled `-`.
The rules:

- A flag not given leaves the `config.toml` default, or `inherit`.
- `--effort inherit` sends no flag, even if `config.toml` sets one.
- A value the kind does not offer exits 2, with the offered list.
- **An option the kind does not declare exits 2.** For example,
  `--effort does not apply to agent kind codex: it declares no effort option`.
  The form cannot produce that request, so refusing it follows #145–147's
  rule.

### 8.2 Provenance and `--json`

- **Provenance:** `option.<name>` is `flag`, `config.toml` or `built-in`,
  for every option the kind declares.
- **`--json`:** gains `"agent_options": {"model": "opus", ...}`, omitted when
  nothing is set.

> **Amended, #209 (2026-09-19): what the agent actually runs with.**
> `agent_options` stays the chosen values, as above. `--json` also gains
> `launch_options`: the value the agent's command line carries for each
> declared option, which is `agentopts.Launch` read back through
> `agentopts.Pinned`. So an option left on `inherit` that
> `[agents.extra_args]` passes is listed too, and its provenance is
> `extra_args`, not `built-in`. That holds after an explicit `inherit`,
> which sends no flag of its own and cannot take `extra_args`'s away. The
> #208 session's report said `built-in` for a model and an effort that
> `extra_args` had set. It reads the declared flags only. Any other argument
> in `extra_args` reaches the agent unreported, a permission-skipping one
> included, and an `sh -c` launcher's loss of the arguments (§5.1) is
> invisible to it.

> **Amended, #219 (2026-09-19): the whole argument list.** `--json`, real
> or `--dry-run`, also gains `agent_args`: the argument list both launch
> paths hand the agent's command, which is §5.1's `agentopts.Launch`
> (`plan.AgentArgs`). It is a JSON array, not a shell string. Both paths
> are typed into the pane's shell in the end, but herdr and `PaneRun` each
> quote the list for that shell themselves, and a string here would be one
> shell's spelling of it. So the argument the paragraph above says goes
> unreported is now reported, and the spawn skill's confirmation shows
> every element beyond the three option flags before the user approves,
> even when the user named every choice and no question was otherwise
> asked. An argument that changes how the agent asks for permission is
> shown, not refused: it is the user's own configuration. `agent_args` is
> what this plugin passes after the agent's command, and the pane's shell
> resolves that command, so a function of that name can still change what
> arrives. So can a pinned account's launcher, which can add arguments of
> its own, and an `sh -c` one drops these. Recognising an `sh -c` launcher
> would take a heuristic that is wrong in both directions, and none is
> attempted.

### 8.3 The spawn skill

Section 5 of `spawn_skill.md` names the three flags, which
`TestSkillNamesEveryCreateFlag` requires. It also tells an agent to leave
them unset unless the task asks, and never to pass a more permissive
`--permission-mode` than the one it runs under.

> **Amended, #209 (2026-09-19): reversed.** Under "leave them unset", every
> spawned session ran on a configuration chosen for no task, and nobody saw
> it before the session started. The #208 session, a wording fix, ran on
> Opus 5 at `xhigh` in auto mode, from `[agents.extra_args]` and claude's
> own settings, and nobody had chosen that. The skill now tells an agent to
> choose all three for the task, unless the user named them, and to pass
> them. It gives:
>
> - a task-shape table for the model and the effort;
> - a rule for the mode: `plan` when the first decisions are the user's;
> - the ceiling as an order: `plan` and `manual` always; `acceptEdits`
>   only under `acceptEdits` or `auto`; `auto` only under `auto`; and
>   `manual` assumed when the spawner cannot tell its own.
>
> Its section 7 confirmation shows each choice with its reason, and what
> the command resolves to, read from `create --dry-run` (v2 spec §13). The
> ceiling and the two refused modes are unchanged.

## 9. Drift

The offered values are copied from `claude --help` and can go stale. Tests
never run the real CLI (`CLAUDE.md`), so the check is manual:
`docs/manual-smoke.md` gains a cell that compares `claude --help`'s
`--effort` and `--permission-mode` choices against the declaration.

When claude drops a value, the failure is loud:

- an unknown `--permission-mode` is rejected while claude parses its
  arguments, so the start fails;
- an unknown `--effort` produces a warning in the pane.

When claude adds a value, it is simply not offered until the declaration
catches up.

## 10. Out of scope

- **Remembering choices** (ruling 2).
- **Declarations for codex and the other kinds.** Each one is a
  declaration-only change, whenever someone wants it.
- **`bypassPermissions` and `dontAsk`** (§4.3).
- **Claude's `-n/--name`, set from the title.** It would be derived rather
  than chosen, so it is not a control. herdr already labels the tab.
- **`--fallback-model`.** Nobody has asked for it.
- **`--resume`**, which is #166's: a narrow flag with no form control.
- **Checking that the running agent honoured the flags.** §9's loud failures
  cover a stale declaration. A wrong model id is the agent's to report, in
  its own pane, and on claude 2.1.277 it does: the session starts, and the
  first prompt is answered with `There's an issue with the selected model`.
  It is an ordinary transcript error, not a blocking screen, so the prompt
  guard's signature list has nothing to learn from it
  (`docs/manual-smoke.md`, Cell 15's recorded run).

## 11. Testing

- `agentopts`: validation (a bracketed id, a leading `-`, and the refused
  modes), argv order, both displacement spellings, and an unknown kind.
- `plan`: both launch paths carry the options, displacement applies to
  both, and an undeclared option is refused.
- `form`: row states, panel frames (`options-panel`, `options-panel-other`,
  `options-inert`), the part grammar, name-input refusal, and inert input
  ignored.
- `app`: the section order, per-kind restore, and the config seed. The
  assembled goldens change by exactly one row, plus a panel region one line
  shorter.
- `create`: the table-driven equivalence cases (config default, codex with
  none, a flag over config), a keystroke-driven equivalence test on the
  `mark_ready` pattern, a flag for every declared option, and the exit-2
  refusals.

## 12. Amended text, itemised

| where | sentence | status |
|---|---|---|
| v1 spec §3 | "Non-goals (v1): … model/effort/permission chip fields" | reversed by this spec |
| v1 spec §16 item 3 | "Model / effort / permission-mode chip fields … Do not re-ship that mistake." | reversed (§2) |
| v2 spec §16 | "Carried forward … model/effort/permission chip fields" | reversed (§2) |
| v1 spec §12 | the `config.toml` listing | extended by `[agents.options.<kind>]` (§6.2) |
| v2 spec §8 | the key list | extended by the `options` zone (§7.3) |
| v2 spec §11 | "Forbidden and ignored with a visible note" list | extended by `agents.options` (§6.3) |
| v2 spec §13 | the flag list | extended by `--model`, `--effort`, `--permission-mode` (§8.1) |
| v3 spec §4 | the row set | `options` between `agent` and `account` (§7.1) |
| v3 spec §7.2 | "`panelCapRows = 15`" … "eight stack rows" | 14, nine rows (§7.4) |
| `CONTRIBUTING.md` | "What not to propose: … model/effort/permission-mode chip fields" | removed, pointing here |
| `README.md` | "Eight rows, one line each" | nine |
| `internal/form/keys.go` | "Atrium's claude-shaped fields … have no equivalent zone here at all" | `ZoneOptions` is the declared equivalent |
