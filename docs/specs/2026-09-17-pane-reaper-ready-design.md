# herdr-draft — asking the agent to mark its pane ready for pane-reaper

- **Date:** 2026-09-17
- **Status:** approved by the owner 2026-09-17 (§11 resolved). Not yet implemented.
- **Amends:** v2 spec §6 (the `prompt` row's value cell), v2 spec §8 (the key
  grammar gains `⌃X` in the prompt), v2 spec §10 (one more resolved value,
  with a short chain), v2 spec §11 (one more forbidden table), v2 spec §13
  (two more flags), and v1 spec §12 (one more `config.toml` table). §12
  itemises every sentence that stops being true. Nothing is superseded
  outright: every section named here stays authoritative apart from the
  sentences §12 lists.
- **Does not amend:** v3 spec §4's row set and order. The toggle is a line
  inside the `prompt` row's panel, not a row. This was the owner's decision,
  and §3 records it.
- **Citations.** herdr line numbers are for **v0.9.0**, the release
  `herdr-plugin.toml` pins as `min_herdr_version` (see `CONTRIBUTING.md`'s
  "Spec citations"). The pane-reaper contract is quoted from its own handoff,
  `~/quantivly/handoffs/2026-09-17-pane-reaper-contract-for-rabota.md`, and
  the instruction text from `_hspawn_reap_instruction` in the dotfiles'
  `zsh/zshrc.herdr`. Neither file is in this repository. §11 explains why
  that matters.

---

## 1. Summary

pane-reaper is a local herdr plugin that closes an agent pane **only** after
the worker in it says it is finished. The worker says so by running one
command as the last step of its final turn:

```bash
herdr pane report-metadata "$HERDR_PANE_ID" --source pane-reaper --token pane_reaper=ready
```

The agent only runs that command if it was told to. The dotfiles' `hspawn`
tells it by appending one fixed sentence to the prompt. herdr-draft creates
the same kind of session and has no way to do this.

This adds an **opt-in** way to append the sentence, **off by default** on
both paths:

- **The form:** a `keep · reap` toggle on the first line of the `prompt`
  panel, flipped with `⌃X` or a click.
- **`create`:** `--reap` / `--no-reap`.
- **`config.toml`:** `[reaper] mark_ready = true` turns the default on for
  both paths.

The sentence is appended only when the prompt is non-empty and is not a slash
command. `plan.Build` does the appending, from a boolean that arrives on
`plan.Input`, so the form and `create` cannot append it differently.

## 2. Background

### 2.1 The contract

From the handoff, and binding on anything that asks an agent to mark itself:

- The mark is a metadata token on the pane. After the pane goes done/idle and
  a grace period passes, pane-reaper closes it only if all of these still
  hold: the same terminal and state, still `ready`, no Claude Code Bash-tool
  child alive, the pane unfocused, and not the last pane of a primary
  workspace.
- **Never mark while still waiting** on a background job, a Monitor, a
  teammate or a reply. The wake-up disarms the pane, so it would linger.
- **Orchestrator and `/loop` sessions must never mark themselves.**
- An unmarked pane is never touched.

`herdr pane report-metadata` exists at this plugin's floor
([`src/cli/pane.rs#L42`](https://github.com/herdrdev/herdr/blob/v0.9.0/src/cli/pane.rs#L42),
[`#L1478`](https://github.com/herdrdev/herdr/blob/v0.9.0/src/cli/pane.rs#L1478)),
so no floor bump is needed. herdr exports `HERDR_PANE_ID` into every pane's
shell, which is what the agent's own shell tool inherits. `CLAUDE.md`'s note
on `herdr:src/pane.rs`'s `apply_pane_launch_env` is the existing citation.

### 2.2 The sentence

Verified live on 2026-09-17 with
`zsh -c "source ~/.dotfiles/zsh/zshrc.herdr >/dev/null 2>&1; _hspawn_reap_instruction"`:

> When your task is completely finished and its results are saved (committed,
> pushed, or written to a file), run this as the last command of your final
> turn so this pane closes itself: herdr pane report-metadata "$HERDR_PANE_ID"
> --source pane-reaper --token pane_reaper=ready — never while you are still
> waiting on a background job, a review, or a reply.

This is one line in the source, wrapped above for reading. It is 352 runes,
including one em dash (U+2014).

`hspawn` appends it after a single space, and not to a prompt starting with
`/`. A slash command takes the rest of its line as arguments, so the
sentence would reach `/loop` as arguments.

### 2.3 Why herdr-draft needs it

The spawn skill (spawn spec §4) makes `herdr-draft create` the thing an agent
reaches for when it hands work off. That handoff is exactly the
fire-and-steer-once worker pane-reaper exists to close. With no flag, an agent
that wants the pane reaped has to paste the sentence into `--prompt` by hand.
Hand-copied, the sentence drifts from hspawn's and is added to slash commands
too.

## 3. Decisions

Made by the owner on 2026-09-17, and not reopened here:

1. **Opt-in, default off on both paths.**
2. **Equivalence holds.** The form and `create` produce the same `plan.Input`
   from the same inputs (v2 spec §13; `internal/create/equivalence_test.go`).
   Every `create` flag has a form control behind it.
3. **`create --reap` sets it headlessly.** The popup sets it with **a toggle
   inside the `prompt` row's detail panel, not a new row.** The row set and
   order are fixed by v3 spec §4.
4. **A config key may flip the default for both paths.** It is
   `[reaper] mark_ready` (§6.2).
5. **The sentence is appended only when the prompt is non-empty and does not
   start with `/`.**
6. **`plan.Build` stays pure.** The resolved boolean arrives on `plan.Input`.

## 4. The sentence lives in one place

### 4.1 `internal/plan/reap.go`

```go
// ReapInstruction is the sentence that asks an agent to mark its own pane
// ready for pane-reaper. It is byte-identical to the dotfiles'
// _hspawn_reap_instruction.
const ReapInstruction = `When your task is completely finished ... a review, or a reply.`

// ReapApplies reports whether a prompt can carry ReapInstruction.
func ReapApplies(prompt string) bool

// PromptText is the text OpAgentPrompt sends: in.Prompt, with the
// instruction appended when in.MarkReady is set and ReapApplies.
func PromptText(in Input) string
```

It sits in `internal/plan` for three reasons:

- `Build` composes the text, and `Build` must not import anything that does
  I/O.
- `internal/form` already imports `internal/plan` (`submitview.go`,
  `mouse_test.go`), so the row and panel can ask `plan.ReapApplies` instead of
  keeping a second copy of the rule.
- `internal/create` needs the same predicate for its `--json` report and its
  stderr note.

One predicate in one package means the panel's "applies" and the plan's
"appended" cannot disagree.

### 4.2 The composition rule

- **Applies when:** `strings.TrimSpace(prompt) != ""` and
  `!strings.HasPrefix(strings.TrimSpace(prompt), "/")`.
- **Leading whitespace is trimmed before the `/` test.** hspawn tests the raw
  string. herdr-draft trims for two reasons. A pasted prompt often carries
  stray leading whitespace. And the two ways of being wrong cost different
  amounts: wrongly skipping loses a reap, while wrongly appending sends a
  paragraph of arguments to a slash command. Being conservative is the
  cheaper mistake.
- **Composed as:** `strings.TrimRight(prompt, " \t\r\n") + "\n\n" + ReapInstruction`.
  hspawn joins with a single space because its prompts are single-line words
  joined by spaces. herdr-draft's prompts are typically multi-paragraph
  handoffs (spawn spec §4.2 says to pipe a file). An instruction glued onto
  a handoff's last line reads as part of that line's point, and a paragraph
  of its own does not. The user's trailing whitespace is trimmed only when
  the sentence is appended, so the blank line is exactly one.
- **No dedupe.** A prompt that already contains `pane_reaper=ready` and is
  sent with the toggle on carries the sentence twice. That is harmless, and
  the alternative adds a third panel state (§5.3) for a case only a
  hand-pasted prompt produces. `--reap` is the documented way to ask.
- **A whitespace-only prompt** is still sent exactly as before (`Build`'s
  existing `in.Prompt != ""` gate), without the sentence.

### 4.3 What pins it

`internal/plan/reap_test.go`:

- `TestReapInstruction_IsHspawnsSentence` asserts the constant equals the
  literal of §2.2, byte for byte, so an edit has to change the test and the
  reviewer sees both. It also asserts the constant contains
  `herdr pane report-metadata "$HERDR_PANE_ID" --source pane-reaper --token pane_reaper=ready`
  exactly. That substring is the part of the contract pane-reaper reads.
- `TestReapApplies` is a table: empty, whitespace, `/loop 5m`, `  /loop`, a
  normal prompt, and a prompt with `/` mid-text.
- `TestBuild_MarkReadyAppendsToThePromptOp` and
  `TestBuild_MarkReadyNeverAppendsToASlashCommand` check the op's `Text`.

The test cannot reach the dotfiles, so it holds the literal rather than
reading `_hspawn_reap_instruction`. When hspawn's text changes, this constant
and its test change by hand. §11 records that as a known cost.

## 5. The form

### 5.1 Where the toggle is

It is the **first** line of the `prompt` panel. The textarea moves down one
line. With the toggle on, at 80 columns (herdr's popup border omitted, as in
v2 spec §4's mockups):

```
 ▌ prompt     Work on ENG-101: Fix login redirect loop +1 more · reap when done
   project    ~/Projects/herdr-draft
   ...
 ──────────────────────────────────────────────────────────────────────────────
    keep · reap   the prompt ends with pane-reaper's instruction to mark this…
   Work on ENG-101: Fix login redirect loop

   Start with the cookie the callback sets.

 ──────────────────────────────────────────────────────────────────────────────
 ⌃J newline · ⌃X keep or reap · ⇥ move                ↵ create    esc cancel
```

`reap` is the selected chip, drawn in the accent colour, which plain text
cannot show.

It goes first and not last for the reason the worktree panel's chips go first
(v2 spec §6). `PromptField.PanelRows()` grows with the text (v3 spec §7.2), so
a line pinned under the textarea would move down the screen while the user
types newlines. The top line never moves.

The line is a `widgets.ChipRow` of two chips, `keep` and `reap`, followed by
a one-line dim hint. The chips reuse the chip vocabulary and styling the
worktree and placement panels already use, so no new widget and no new
contrast surface is needed (`CLAUDE.md`, "Frames record bytes, not
perceptibility"). `keep` is the word hspawn's own opt-out flag uses
(`--keep`).

### 5.2 Keys and mouse

- **`⌃X` toggles, from anywhere in the prompt zone.** The textarea owns every
  printable key, `←→`, `↑↓`, and most control chords. A part cursor like the
  worktree panel's would take `↑↓` away from multi-line editing, so a chord
  is the only key that can reach the toggle without taking one away from the
  text. The binding is checked against each owner of chords:
  - bubbles v2.1.1's textarea `DefaultKeyMap` binds `ctrl+` `f b n p w k u
    m h d a e v t`, and not `x`.
  - This plugin's grammar (`keys.go`) binds `⌃S`, `⌃R`, `⌃J` and `⌃C`.
  - herdr's default keys are prefix-mode apart from `remote_image_paste`
    (`ctrl+v`), and the default prefix is `ctrl+b`
    ([`src/config/model.rs#L1084-L1109`](https://github.com/herdrdev/herdr/blob/v0.9.0/src/config/model.rs#L1084-L1109)).

  `⌃X` is free in all three. It is the familiar "exit" chord, which suits "the
  pane exits when done".
- It is a grammar action, the way `⌃J` is. `MapKey` returns a new
  `ActionToggle` for `ctrl+x` in `ZonePrompt` only (`ActionNone` elsewhere),
  and `handleKey` hands it to a new optional capability,
  `toggler interface{ Toggle() }`. This follows the precedent `newliner`
  set, and it keeps `⌃X` from ever reaching the textarea as input.
- **Click** on either chip selects it. The chips are marked
  `chip:prompt:keep` / `chip:prompt:reap`. `form.go`'s `zonePanel` branch
  already forwards a click inside the panel to the focused section, and
  `PromptField.Update` tries `ChipRow.SelectAt` before forwarding to the
  textarea.
- **The footer** teaches it. `zoneRungs(ZonePrompt)` becomes
  `{"⌃J newline · ⌃X keep or reap", "⌃J newline"}`. The narrow rung drops the
  toggle before the newline, because `⌃J` is the one that stops `↵` from
  creating the session by mistake.
- **`⌃R ⌃R`** rebuilds the form through `New`, which reseeds the toggle from
  the resolved default (§6). No extra code is needed.
- There is no touched-versus-preselected rule. Nothing re-seeds the toggle
  after `New`, because its only tiers do not change with the project row
  (§6.1), so `applyProjectDefaults` never touches it.

### 5.3 What the row and the hint say

v2 spec §3 rule 1, which v3 §3 keeps unchanged, decides the row: "rows state
consequences, not settings". The README and CHANGELOG put it as "a row
states what the session will be". So the row shows the reap **only when the
sentence will actually be sent**:

| toggle | prompt | row | panel hint (dim) |
|---|---|---|---|
| keep | any | unchanged from v2 spec §6 | `reap asks the agent to mark this pane ready for pane-reaper once it is done` |
| reap | empty / whitespace | `—` | `no prompt, so there is nothing to add pane-reaper's instruction to` |
| reap | starts with `/` | `/loop 5m …` (unchanged) | `a slash command takes no instruction -- it would arrive as its arguments` |
| reap | anything else | first line, ` +N more`, then dim ` · reap when done` | `the prompt ends with pane-reaper's instruction to mark this pane ready` |

The row suffix goes after ` +N more`, and like it, its width is taken out
**before** the first line is elided (`PromptField.Row`'s existing rule), so
the summary shrinks and the statement survives. The suffix is dim, the same
style as ` +N more`: it qualifies the value rather than being a second value.

The row never shows the instruction text itself, and neither does the
textarea. The textarea holds what the user typed. Putting 352 runes of
generated text into an editable buffer would make "turn it off" mean
"find and delete a paragraph".

### 5.4 Degradation

This covers v3 spec §7, the vertical ladder that replaced v2 §9. v3 §9, the
resting panel, is untouched.

- The panel still gets `min(PanelRows(), Region)` rows.
  `PanelRows()` becomes `max(promptPanelMinRows, lines + 2)` capped at
  `promptPanelMaxRows`: one row to type into and one for the toggle.
  `promptPanelMinRows` (6) and `promptPanelMaxRows` (20) do not change, so
  the textarea's ceiling drops by one row. `panelCapRows = 15` (v3 §7.2)
  already clips the prompt panel at the shipped size, so the rows the user
  actually sees at 101×30 are 1 toggle + 14 text, against 15 text before.
- **With a panel of `h == 1`** the toggle line is dropped and the textarea
  keeps the one row. It is where the user types, and the row still states
  the reap. `⌃X` still works, and the footer still teaches it wherever the
  footer fits.
- The line is fitted to the panel width. At narrow widths the hint elides at
  its tail, and the chips go only when the panel is under 16 cells, which no
  shipped popup produces.

### 5.5 Golden frames that change by design

Only frames with the prompt focused change: `internal/form`'s
`prompt-panel-80x24`, and `internal/app`'s `assembled-full-*` and
`assembled-minimal-*`. The diff for each must be exactly three things: the
toggle line inserted under rule 2, the textarea shifted down one line, and
the footer rung. A frame whose row stack moves is a defect. The row suffix
appears in no existing frame, because every existing fixture resolves the
toggle to `keep`. One new form frame pins the on state:
`prompt-panel-reap-80x24`.

## 6. Resolution

### 6.1 The chain: built-in, then `config.toml`, and nothing above

`defaults.Resolved.MarkReady`, attributed under `FieldMarkReady = "mark_ready"`:

| tier | supplies it? | why |
|---|---|---|
| built-in | `false` | decision 1 |
| `config.toml` `[reaper] mark_ready` | yes | decision 4 |
| `last-used.json` | **no** | see below |
| `.herdr-draft.toml` | **no, and refused with a note** | §6.3 |
| `projects.json` | **no** | see below |
| `herdr workspace list` | no | it decides placement only |

This is the second value with a deliberately short chain, after
`TrustRepository`, and the two stop short for different reasons.
`trust_repository` skips memory because *no control offers it*. This value
has a control. It skips memory anyway, for these reasons:

1. **The owner's default would not hold.** v2 spec §10 writes memory "only
   after a successful submit", on both paths (`create` remembers too). One
   `--reap` from one script, or one `⌃X` in one popup, would make every later
   session reap by default, anywhere (`last-used.json`) or in that
   repository (`projects.json`). "Default off" would last until first use.
2. **The mark is a property of a task, not of a place.** What v2 §10
   remembers (worktree, placement, agent kind, base) describes how you work
   *in a repository*. Whether a worker should close itself depends on whether
   anyone will steer it afterwards, and the contract names sessions that must
   **never** mark themselves: orchestrators and `/loop`. A remembered `reap`
   would silently extend to the next orchestrator started in the same
   repository. Its pane would then close under it.
3. **It belongs to the prompt, and the prompt is not remembered.** Nothing in
   v2 §10 remembers prompt content. The toggle lives in the prompt's panel
   because it changes what the prompt says.

A user who wants reaping by default says so once, in `config.toml`, which is
decision 4's mechanism. `⌃R ⌃R` returns to that.

### 6.2 The config key

```toml
[reaper]
mark_ready = true   # default false: ask agents to mark their panes ready for pane-reaper
```

- ``config.ReaperConfig{ MarkReady *bool `toml:"mark_ready"` }``, on
  `Config.Reaper`.
- A **pointer**, for `WorktreeConfig.TrustRepository`'s reason: a file with
  no `[reaper]` table must attribute the `false` to `built-in`, not to
  `config.toml` (`defaults.go`'s note beside that key).
- The table is named `[reaper]` rather than `[pane_reaper]`, so that a later
  key about the reaper, such as a per-pane grace
  (`pane_reaper_min`, contract §"What a lane needs to do"), has a home. That
  key is out of scope (§10).

### 6.3 `.herdr-draft.toml` refuses it, with a reason

`repoDeniedKeys` gains:

```go
{"reaper", "it would add an instruction to your agent's prompt"},
```

The allow-list is flat, so the table is already rejected structurally (v2 §11,
`repo.go`). The entry exists for the **note**. It is the same trust argument
as `linear.prompt_template` ("it would become the agent's first instruction"):
a file that arrives with `git clone` must not decide what an agent is told.
A committed `mark_ready = true` would also close panes on the machine of
everyone who clones the repository.

## 7. `create`

### 7.1 Flags

`--reap` and `--no-reap`, as one tri-state on `request.reap *bool`. This is
exactly the `--worktree` / `--no-worktree` shape (`flags.go`):

- Neither given: the resolved default (§6) applies.
- Both given: exit 2, `--reap and --no-reap contradict each other`.
- `--reap=false` is accepted, because Go's `flag` package accepts it for any
  boolean. It means off, and `--no-reap=false` means on. This is the same
  inversion `--no-worktree=false` already has, for the same reason: each
  flag is the other's mirror. It is not documented. `--no-reap` is the
  spelling to teach.

A pair and not a single `--reap` because decision 4 can turn the default
**on**. A script then needs an off spelling that does not rely on knowing
Go's `=false` syntax. The pair is also the one boolean shape this command
already teaches.

The two booleans are registered onto fields of `request` (`reapOn`,
`reapOff`) rather than as new parameters of `registerFlags`. That keeps the
signature `spawnskill_contract_test.go` calls unchanged. It departs slightly
from how the worktree pair is wired, which is noted in the plan.

### 7.2 What it sets

`plan.Input.MarkReady`, and `provenance["mark_ready"] = "flag"` when either
flag is given. Otherwise the tier name (`built-in` / `config.toml`).

`plan.Input.MarkReady` carries the **toggle position**, not whether the
sentence was appended, and the form carries the same. The equivalence test
compares positions, and `plan.Build` decides "appended" from the position
plus the prompt, once, for both.

### 7.3 A flag that does nothing says so

`create` refuses what the form refuses (#145–147), and the form does not
refuse a `reap` toggle over an empty prompt, so `create` does not refuse it
either. A **flag given outright** that will not apply prints one stderr line
and carries on:

- `herdr-draft create: --reap has no effect: there is no prompt to add pane-reaper's instruction to`
- `herdr-draft create: --reap has no effect: a prompt starting with / is a slash command, and the instruction would reach it as arguments`

A reap that is on only because of `config.toml` prints nothing. Nobody asked
for it on this command line, so its not applying is not news. This follows
the rule `buildInput` already uses for a remembered placement under a
worktree.

### 7.4 `--json`

`jsonReport` gains ``MarkReady bool `json:"mark_ready,omitempty"` ``, true
exactly when the sentence was **appended** (`in.MarkReady && plan.ReapApplies(in.Prompt)`).
A consumer asks "will this pane close itself?", and the toggle position alone
does not answer that. The position is still visible through `provenance`.

`unsent_prompt` and `unconfirmed_prompt` return the **composed** text, since
it is what `OpAgentPrompt` carried (`unsentPromptText` reads the op). That is
correct: a caller who pastes it by hand should give the agent the same
instruction the send would have.

### 7.5 `create --help` and the spawn skill

`createUsage` gains:

```
  --reap             end the prompt with pane-reaper's instruction, so the
                     agent marks its own pane ready to close when finished
                     (default: off, or [reaper] mark_ready)
  --no-reap          do not, even if [reaper] mark_ready is on
```

`internal/skill/spawn_skill.md` §6 has to name both flags, or
`TestSkillNamesEveryCreateFlag` fails, and that is right: a flag the skill
does not name is a flag no agent uses. The prose carries the contract's two
rules an agent can get wrong. Pass `--reap` only for a worker nobody will
steer afterwards, and never for an orchestrator or `/loop` session. It works
only where the user runs pane-reaper. Spawn spec §4.3 still holds: the
paragraph says nothing about herdr-draft internals.

## 8. Linear `prompt_template`

A selected issue seeds the prompt through the user's template (`app.RenderPromptTemplate`,
spec §10; `create.promptText`). The sentence is appended **after** that, to
whatever the prompt holds at submit. It is never part of the template:

- The template renders into the textarea, which the user edits (touched rules
  unchanged). The sentence is added in `plan.Build`, after editing is over.
- Toggling does not re-seed or re-render anything.
- A template that renders to a `/`-prefixed prompt gets no sentence, by §4.2.
- `[linear] prompt_template` stays on the deny-list. §6.3's `reaper` entry
  is its sibling, and the README section explaining one explains both.

## 9. The submit view, and delivery

### 9.1 What the progress screen shows

The prompt step's label stays `prompt`. Its detail, currently `""` so that
the state word shows (`submitStepDetail`), stays `""` whether or not the
sentence was appended. Every existing progress and failure golden frame
therefore stays byte-identical. The reap was already stated on the row the
user submitted from. A step detail would replace the state word
(`queued`/`working…`/`done`), which is the one thing that row exists to show
while it runs.

The unsent-prompt recovery surface (`SubmitView.SetUnsentPrompt`, the saved
`unsent-prompt.txt`) shows and saves the composed text, for §7.4's reason.

### 9.2 Delivery sees the whole text

`plan.Execute` needs no change, and must not get one. Every guard reads
`OpAgentPrompt`'s own `Prompt.Text`: `promptIfReady`, the stall retry,
`confirmPromptLanded`, `promptIsVerifiable`, `promptOnScreen`, and
`unsentPromptText`. `Build` has already composed that text, so each guard
sees exactly what `herdr agent prompt` receives (argv, no shell, so
`"$HERDR_PANE_ID"` reaches the agent literally, `runner.go`'s `AgentPrompt`).
Composing anywhere later, in `Execute` or in `CLIRunner`, would leave
the guards judging text that is not the text sent.

**Accepted cost:** `promptIsVerifiable` bounds the swallowed-prompt verdict to
`promptTraceMaxRunes = 400` runes AND `promptTraceMaxLines = 10` newlines.
With 354 runes appended (`"\n\n"` plus the sentence), a marked prompt longer
than about 46 runes is past the rune bound; because the appended `"\n\n"`
alone spends 2 of the 10 lines, a short prompt with 8–9 newlines of its own
is past the line bound first. For
marked prompts, the "dialog ate it" verdict therefore effectively applies
only to very short prompts. The size-independent "the agent is gone" verdict
still applies to all of them. That is the bound working as designed: a
400-rune turn is reflowed by the agent's TUI and does not appear verbatim on
the screen next to a dialog. The probe still finds the user's first 24 runes,
because the sentence goes at the end. Re-tuning the bound is out of scope
(§10).

## 10. Out of scope

- **A per-pane grace** (`pane_reaper_min`). It is a separate `report-metadata`
  call the spawner makes, not something the agent is told. It would need its
  own control, and nobody has asked for one.
- **Closing panes, or checking pane-reaper is installed.** herdr-draft only
  asks the agent. If pane-reaper is not running, the agent sets a token no
  one reads, which harms nothing (contract: unmarked panes are never touched,
  and marked panes are touched only by pane-reaper).
- **Making the sentence configurable.** A `config.toml` template for it would
  let the text drift from pane-reaper's token without any test noticing.
  The constant is the contract.
- **Deduplicating a prompt that already carries the token** (§4.2).
- **Remembering the toggle** (§6.1).
- **Re-tuning `promptTraceMaxRunes`** (§9.2).
- **Tearing down a reaped lane's worktree.** The contract leaves that to the
  spawner, and so does this.

## 11. Owner rulings (2026-09-17)

1. **Name pane-reaper, and say where it comes from.** An earlier draft of this
   section said nobody else could install pane-reaper. That was wrong:
   `quantivly/dotfiles` is a public repository, and the plugin is
   `config/herdr/plugins/local/pane-reaper` there. It installs with
   `herdr plugin link` from a clone. So the user-facing copy names
   pane-reaper, and the README links to it. The dependency is soft: without
   pane-reaper the agent sets a token nothing reads (§10). The account-picker
   precedent (CHANGELOG 0.1.0) is about a *private* toolchain, which this is
   not.
2. **`CLAUDE.md` reads "every flag there has a form control behind it".**
   Confirmed.
3. **§9.2's cost is accepted.** A marked prompt past about 46 runes loses the
   swallowed-prompt verdict.
4. **The constant is synced with hspawn by hand** (§4.3). The test pins the
   literal.

## 12. Amended text, itemised

| where | sentence | status |
|---|---|---|
| v1 spec §12 | the `config.toml` listing | extended by `[reaper] mark_ready` (§6.2) |
| v2 spec §6, table | `prompt` row: "first line truncated, plus dim ` +N more`" | extended: plus dim ` · reap when done` when the sentence will be sent (§5.3) |
| v2 spec §6, prose | "`prompt` shows the textarea" | now "a `keep · reap` line over the textarea" (§5.1) |
| v2 spec §8 | the key list | extended: `⌃X` toggles keep/reap in the prompt (§5.2) |
| v2 spec §10 | the precedence list | holds as written. `mark_ready` has a two-tier chain (§6.1) |
| v2 spec §11 | "Forbidden and ignored with a visible note" list | extended by `[reaper]` (§6.3) |
| v2 spec §13 | the flag list | extended by `--reap` / `--no-reap` (§7.1) |
| `internal/form/footer.go` | ZonePrompt: "One key, because one key is what is surprising here" | now two, and the comment says why (§5.2) |
| `CLAUDE.md` | "every flag there has a form row behind it" | reads "form control" (§11 item 2, confirmed) |
