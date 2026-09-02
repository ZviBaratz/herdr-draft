# herdr-draft v2 — new-session dialog redesign

- **Date:** 2026-09-02
- **Status:** implemented
- **Errata:** seven corrections found during implementation are marked
  **ERRATUM** in place, below. Each replaces the sentence it follows; the
  original wording is left standing so a comment citing it still resolves.
- **Supersedes:** §6 (the form) and §7 (skin & mouse) of
  `docs/specs/2026-08-31-herdr-draft-design.md`. Every other section of the
  v1 spec stays authoritative and its `spec §N` citations keep resolving.
  Comments citing v1 §6 or §7 are updated as their code migrates; new
  comments cite this document as `v2 spec §N`.

## 1. Summary

v1 reproduced Atrium's creation form. It works, and it reads as a port. This
redesign keeps every non-view layer untouched and rebuilds the form around a
**stack of one-line rows with a single detail panel**, adds a title-first
fast path, and adopts herdr's own dialog grammar. It also adds four
capabilities: per-project defaults memory, a repo-level shared config,
redesigned submit/failure screens, and a headless `create` command.

Target: the common session is *open, type a title, Enter*, with every other
decision visible and already correct.

This spec is the normative document. Two companion plans carry the
step-by-step implementation detail — the view rewrite's six-step bridged
sequencing, in which only one commit changes a rendered pixel, and the
resolver extraction that must precede every new defaults tier. They live in
`docs/superpowers/plans/`, which this project gitignores on purpose: plans
are process records, not published content. **They exist only in the author's
working tree**, so anything a contributor needs in order to do the work
belongs here or in the GitHub issue, not there.

The load-bearing conclusions from both plans are already folded into the
sections below.

## 2. What v1 got wrong

Two defects, named by the author, and both structural rather than cosmetic.

**Every session costs the same.** Eleven focus stops presented as a flat,
equally-weighted list. The fast path exists — Enter from a non-empty Title
submits — but focus opens on Issue or Project, the correct defaults are not
visibly *decided*, and nothing signals that nine fields are safe to skip. So
the user walks them every time.

**The layout and visual language are weak.** Chip rows render unlabeled
(` Off · On`, ` claude · codex · pi · more…`). `allocateHeights` spends one
global row budget across all eleven sections, so moving focus reflows the
whole form — a trade v1 documented and accepted — and at 120×40 fourteen of
forty rows are blank reserved picker rows, because "blank when unfocused" is
a per-field choice rather than a framework rule. The entire visual grammar is
a `▎` bar in a two-cell gutter: no header, no grouping, no hierarchy. The
degradation ladder's heading stage has been dead since it was written, and
two hint setters (`DirField.Hint`, `IssueField.Hint`) have no callers at all,
so their reserved rows are permanently blank.

## 3. Design language

Five rules. The rest of this document follows from them.

1. **Rows state consequences, not settings.** A row says what will exist, not
   which knob is set. The form reads as a description of the session about to
   be created.
2. **One line per field, always** — focused or not, enabled or inert. Row
   positions are fixed; the eye learns the map once.
3. **The panel is the only chooser.** Candidate lists, the prompt editor and
   per-field notes live in one region. Nothing else grows or shrinks.
4. **The footer teaches the focused field**, then states the constants.
   Per-field hint rows disappear.
5. **Copy is plain, lowercase and active.** `none`, not
   `None (manual entry)`. Failures say what happened and what to do.

The deliberate risk is rule 1: collapsing setting-and-value into one
consequence line. If it fails, it fails by being too terse, and the panel is
the escape valve.

## 4. The screen

Resting, fully configured, at the 80×24 floor:

```
 new session                                    herdr-draft · main
 ──────────────────────────────────────────────────────────────────
   issue      none
 ▎ title      fix login redirect loop▏
   prompt     —
   project    ~/Projects/herdr-draft
   worktree   on · zvi/fix-login-redirect-loop ← main
   placement  worktree opens as its own space
   agent      claude
   account    active · max · 12%
 ──────────────────────────────────────────────────────────────────
   branch will be zvi/fix-login-redirect-loop
 ⌃S create now · ⇥ for the prompt         ↵ create · esc cancel
```

`▎` marks the focused row in these mockups only because plain text cannot
show a background fill. On screen the whole row is painted in `ActiveRowBG`
and there is no gutter column.

The header carries the form name and live context for the **selected**
project: repository name and its current branch, not the invoking workspace.

With focus on the project row, nothing above the rule moves:

```
   issue      none
   title      fix login redirect loop
   prompt     —
 ▎ project    ~/Projects/her▏
   worktree   on · zvi/fix-login-redirect-loop ← main
   placement  worktree opens as its own space
   agent      claude
   account    active · max · 12%
 ──────────────────────────────────────────────────────────────────
   ~/Projects/herdr
 ▸ ~/Projects/herdr-draft
   ~/Projects/herdr-draft-2
 ⇥ complete · ↑↓ choose                   ↵ create · esc cancel
```

With focus on the worktree row, the panel becomes a three-part editor whose
own label column aligns with the stack above it:

```
 ▎ worktree   on · zvi/fix-login-redirect-loop ← main
   ...
 ──────────────────────────────────────────────────────────────────
     worktree   off · [on]
   ▸ branch     zvi/fix-login-redirect-loop
     base
                  HEAD (main)
                  main
                  release/1.4
 ↑↓ part · ←→ toggle                      ↵ create · esc cancel
```

> **ERRATUM, the placement row's inert copy.** These mockups originally read
> `placement  —  a worktree opens its own space`, against §6's table's
> `worktree opens as its own space`. Two wordings for one state, and the
> `—` is a second unset marker beside a sentence that already explains
> itself. The code follows the table; the mockups above have been corrected
> to match it.
>
> **ERRATUM, the worktree panel's base list.** The mockup originally carried
> `HEAD (main)` on the `base` part line *and* omitted it from the list
> below. That list shape is not reachable: dropping the `HEAD` row from the
> picker would remove the only way back to it. What ships is the shape above
> — while the base list is on screen it is what says which base is selected,
> so the part line yields to it rather than repeating its selection. The
> line fills in again when the list is not being shown.

## 5. The `Section` interface and layout arithmetic

v1's `Section` asks each field how tall it wants to be and hands it a height
to fill exactly. v2 asks for a row and a panel:

```go
type Section interface {
    ID() string
    Label() string            // lowercase; leading spaces indent a child row
    Enabled() bool
    Focus() tea.Cmd
    Blur()
    Update(tea.Msg) tea.Cmd

    // Row renders the value half of this section's single row into at
    // most w cells. Exactly one line, always, focused or not.
    Row(w int) string

    // Panel renders this section's chooser or editor into exactly h
    // lines at width w, including any verdict or note line of its own.
    // Called only while the section is focused.
    Panel(w, h int) string

    // PanelRows is how many panel lines this section would like. Zero
    // means it has no panel; the region collapses for that field.
    PanelRows() int
}
```

`titleValuer`, `completer` and `newliner` survive unchanged. One optional
interface is added:

```go
// footerHinter supplies the focused section's own key rungs, widest
// first. The form appends the constant tail and picks a rung that fits.
footerHinter interface{ FooterRungs() []string }
```

Indentation needs no interface: the worktree children return `"  branch"` and
`"  base"` as labels and the label column pads them.

With `n` sections, fixed cost is the header, two rules and the footer:

```
available = h - 4 - n
panel     = min(focused.PanelRows(), available)
```

> **ERRATUM, the fixed-cost formula.** Neither line describes what
> `layoutFrame` does. Both were written before §9's degradation order was
> settled, and they contradict it two sections later.
>
> The fixed cost is not always 4. The header and the two rules are
> *droppable*, in a definite order, so on a short window it is 3, 2 or 1 —
> the footer alone is never dropped. Nor are the `n` rows fixed: once the
> whole stack and the panel floor no longer both fit above the footer, the
> stack scrolls instead, down to the focused row alone.
>
> The panel is not `min(PanelRows(), available)`. Taking the minimum would
> let a field with little to show shrink the region, which moves the rule
> above it and the footer below it as focus travels — precisely the thing
> the fixed-row design exists to prevent. Slack lands in the panel and
> nowhere else, whatever the focused field asked for.
>
> The real rule is §9's ladder, read as a survival order rather than a drop
> order: the footer first, then the `n` rows, then the panel's first three
> rows, then rule 1, then the header, then rule 2, and every line still
> unspent after that grows the panel. `PanelRows()` is consulted only
> *inside* the region the ladder already fixed — `compose` renders
> `Panel(w, min(PanelRows(), region))` and blank-fills the remainder itself.
> See §9 for the ladder; `rowlayout.go`'s `layoutFrame` carries the worked
> heights and the monotonicity argument.

**Create stays a focus stop but moves onto the footer line.** It remains the
last Section in the ring and keeps its `button:create` zone; only its
rendering changes, from a centered full-width row to an intrinsic-width
button right-aligned on the footer. This is deliberately less disruptive than
removing it from the ring: `clipKeeping`'s "never drop the last line" needs
no change and now protects the key ladder and the button with one rule, the
ring keeps its guaranteed-enabled stop, and `mouse_test.go` passes as
written. `compose` keeps its existing `body, last := sections[:lastIdx],
sections[lastIdx]` split — `last` simply contributes to the footer rather
than to the stack, so the row indexing is over `sections[:len-1]` exactly as
today's body loop already is.

The footer's key rungs are fitted into `inner - width(button) - 2`. The
button is never traded away for hint text.

Deleted from `sizes.go`: `allocateHeights`, `pickerRowsAt`, `promptRowsAt`,
`formChromeRows`, `footerRows`. Kept: `paintLine`, `dropLinesToFit`,
`clipKeeping`, `fitToHeight`, `innerWidth`. `fitToHeight`'s heading stage
becomes live for the first time.

> **ERRATUM, what was kept from `sizes.go`.** Of the five, only `paintLine`
> survives. The heading stage never became live either: `fitToHeight` was
> deleted having rendered nothing in v2.
>
> `innerWidth` went first. It was kept only so that unifying it with
> `contentBox` would not move the two submit golden frames — and §12, seven
> sections later, requires the submit screen to share the form's own label
> column, which moves those frames on purpose. The spec's own later decision
> overtook the reason for keeping it, so when the v1 compose path went it
> lost its last caller and went with it.
>
> `fitToHeight`, `dropLinesToFit` and `clipKeeping` — Atrium's post-hoc
> drop-lines cascade — went next, for the reason the erratum above gives:
> `layoutFrame`'s components sum to exactly the height they were asked for
> *by construction*, so no render is ever over budget and there is nothing
> left to degrade after the fact. That retires this section's argument that
> `clipKeeping`'s "never drop the last line" needs no change in order to
> protect the key ladder and the button — the conclusion holds, but the
> arithmetic is what holds it, not the clip.
>
> The cascade outlived its last caller by most of the v2 program — from the
> commit that gave the submit pipeline the form's own chrome to the end —
> which is worth recording rather than quietly tidying: it was still
> documenting v1's `▎` gutter bar (`decorateFocus`, unreachable alongside
> it) long after §7 had replaced that affordance with a full-width
> `ActiveRowBG` fill. Dead code that draws the old design is not neutral;
> it tells the next reader the screen still looks that way.

## 6. Fields — rows, panels, states

Row order, top to bottom: `issue`, `title`, `prompt`, `project`, `worktree`,
`placement`, `agent`, `account` — eight rows. Issue sits directly above title
so one `⇧⇥` reaches it. This order is declared in exactly one place,
`internal/app`'s section slice.

| Row | Value when set | Unset | Inert |
|---|---|---|---|
| `issue` | `ENG-101 · fix login redirect loop` | `none` | `unavailable  <reason>` |
| `title` | the text, cursor inline | dim `untitled` | — |
| `prompt` | first line truncated, plus dim ` +N more` | `—` | — |
| `project` | path, `~`-shortened, head-truncated | — | `invalid` / `not a repository` |
| `worktree` | `on · <branch> ← <base>`, or `on · from <base>` with no branch yet | `off` | `not a git repository` |
| `placement` | `new space` / `tab here` / `split here` | — | `worktree opens as its own space` |
| `agent` | `claude` | — | — |
| `account` | `personal · max · ok`, or `active · max · 12%` | — | `account pinning only applies to claude` |

> **ERRATUM, the worktree row with no branch yet.** The table originally had
> no cell for the state the form *opens* in: worktree on, in a repository,
> with no title typed and therefore nothing to derive a branch from. Only
> `on · <branch> ← <base>` was specified, so the row rendered the branch
> editor's own placeholder into it and named a branch called "branch name".
> Fifteen green commits shipped past it, because every golden frame typed a
> title first. The row now changes *shape* rather than filling in a name:
> `on · from <base>`. The `←` is a relation between a branch and the ref it
> forks from, and with nothing on its left there is no relation to draw.
>
> The general rule the gap illustrates: a row table needs a cell for every
> state a value can be in, and "unset" is a state a *derived* value reaches
> too, not only a typed one.

Panels: `issue`, `project`, `agent`, `account` show their candidate list;
`prompt` shows the textarea; `title` shows its verdict line; `placement`
shows its chips with a per-choice explanation; `worktree` shows a three-part
editor; `create` shows a one-line confirmation.

**The worktree is one row with a three-part panel.** An earlier draft made it
three rows to avoid a second focus level, where `↑↓` would have to mean both
"move between the toggle, branch and base" and "move the base list". That
concern is answered by precedent: `AgentField` already resolves exactly this
ambiguity, by letting `↑` at the top of its list move the outer cursor back
(`field_agent.go:161-170`). The worktree panel uses the same pattern with a
`worktreePart` sub-cursor over `{chips, branch, base}`. One row also matches
the layout the author chose, and keeps the stack at eight rows.

Every public setter and getter on `WorktreeField` keeps its signature, so
`internal/app`'s reads and writes are untouched apart from the section list.

Markers: v1's bare `!` prefix becomes a colored state word. A rate-limited
profile reads `gamma · team · 100%` with the percentage in the warning color;
an auth failure reads `beta · max · sign in again` in the danger color. Only
the account *row* needs this, and a row is not a picker item, so
`widgets.Picker` needs no per-item style hook and is reused unchanged.

Verdicts render in the panel, never appended to the row, so a recomputing
verdict cannot shift text under the cursor. This retires
`titleVerdictMaxCells = 21` (`field_title.go:26`): the verdict now owns a
full-width line, so the clamp that kept it from colliding with typed text is
obsolete. Today `branch: zvi/fix-login-redirect-loop` is silently cut at 21
cells.

`DirField.Hint` and `IssueField.Hint` are deleted rather than wired.

### 6.1 Empty and loading states

- **Loading.** The row reads `loading…` in dim; the panel says what is being
  read (`reading branches…`, `fetching assigned issues…`). Nothing blocks.
- **Unavailable with a reason.** `unavailable  no API key`, with the panel
  naming the config key that fixes it.
- **Absent by design.** Linear unconfigured, or fewer than two clauth
  profiles, remain static startup checks that omit the row entirely. With
  neither configured the form is eight rows; that is the shape most adopters
  will see and it must look deliberate.

  > **ERRATUM.** Six, not eight. Eight is the *full* count, with both
  > configured. Dropping `issue` and `account` leaves `title`, `prompt`,
  > `project`, `worktree`, `placement`, `agent` — six rows in the stack,
  > plus Create on the footer. `assembled-minimal-80x24` is the frame that
  > pins it.
- **Nothing to choose.** An empty panel list speaks in the field's own terms
  (`no branches yet`, `no assigned issues`), never a bare `no matches`.

## 7. Skin, palette, mouse

herdr's `render_new_linked_worktree_overlay` (`herdr:src/ui/dialogs.rs:259`)
is the house grammar: lowercase terse labels in a dim color, values brighter,
no `Label:` colons, a lowercase modal header, status in dim, errors in red,
and action buttons carrying a key glyph plus a verb — the primary filled with
the accent color, the secondary on a surface background.

Adopt all of it. The one deviation: herdr puts each label on its own line
above its value, which would cost twenty rows for ten fields. Keep the
dim-label / bright-value pairing, set as an aligned two-column row.

**Four palette fields are added.** `theme.Palette` has seven; herdr's has
nineteen (`herdr:src/app/state.rs:69`).

| New field | herdr source | Used for |
|---|---|---|
| `Surface` | `surface0` | secondary button fill, selected panel row |
| `ActiveRowBG` | `active_row_bg` | the focused row's background |
| `Warning` | `peach` | rate-limited and degraded markers |
| `Branch` | `mauve` | branch names, which herdr colors distinctly |

Each needs an entry in every builtin palette and an override key.

Focus indication is the full-width `ActiveRowBG` fill, not a gutter bar.
`paintLine` already reasserts a background after every embedded ANSI reset,
which is exactly the hazard a full-width row highlight hits.

Zones: `row:<id>` per row (click focuses), `panel:<id>:<n>` per panel list
line (click selects), plus `button:create` and `button:cancel`. The wheel
scrolls the panel unconditionally — there is now exactly one scrollable
region, so routing by focus is no longer meaningful.

All four widgets are reused unchanged. `widgets.Picker` needs no per-item
style hook: the one styled case is an account row, and rows are rendered by
the field, not by the picker. The only widget-adjacent change is populating
`Chip.FocusHint` on the placement chips, which `ChipRow.MarkedView` already
renders on a second line (`chiprow.go:225-229`) — plain data, no new code.

Width is capped and the content centered horizontally, so a 200-column
terminal does not stretch rows into ribbons. Row values truncate with a
visible marker via `ansi.Truncate` / `ansi.TruncateLeft` — already an
indirect dependency — keeping the informative end: the tail for paths, the
head for titles and branches. This is a deliberate, disclosed departure from
v1's silent-clip-no-ellipsis rule, which was about not adding a reflow
dependency; for a one-line value row a marked truncation beats a silent one.

## 8. Key grammar

Mostly unchanged, so muscle memory survives.

- **Focus opens on `title`**, not on the first enabled section.
- `↵` from a non-empty title submits. Unchanged, but now the opening state.
- `↵` from the prompt submits rather than advancing. Nothing used it for a
  newline; `⌃J`, `⇧↵` and `⌥↵` keep that job.
- `⇥` / `⇧⇥` move between rows; `↑↓` drive the panel.
- `⌃S` submits from anywhere, `esc` / `⌃C` cancel, `⌃R ⌃R` clears.
- Paste stays routed away from the key grammar.

`ZoneCreate` is removed with the Create section.

> **ERRATUM.** It is not. This line predates §5's own decision — and issue
> #3's authoritative Revision — to keep Create as a Section rendered on the
> footer line rather than remove it from the ring. The section stayed, so
> `ZoneCreate` stayed with it: it is still the ring's last stop, still the
> zone `↵` submits from, and still carries `button:create`.

## 9. Degradation

Fixed cost is header, rule, rows, rule, panel, footer. As height shrinks:

1. Panel shrinks to three rows, the useful floor for a picker.
2. Drop the second rule.
3. Drop the header.
4. Drop the first rule.
5. Scroll the row stack, keeping the focused row visible.

The footer and its buttons are never dropped. Panel content never falls below
three rows while a picker is focused.

## 10. Defaults resolution

One resolver in a new pure package `internal/defaults`, called by both the
form and the headless command, so the two cannot disagree. It performs no
I/O: every tier is passed in already loaded.

**Precedence, highest first:**

1. `projects.json` — your last choice in *this* project
2. `.herdr-draft.toml` — the repository's committed default
3. `last-used.json` — your last choice anywhere
4. `config.toml` — your own configuration
5. the built-in default

A Linear issue selection overrides title, branch and prompt on top of all of
it, under the existing touched-versus-preselected rule.

Tiers 1 and 2 sit that way round because your last choice *in this
repository* is both deliberate and recent, while the repo default is what a
*new* checkout should start from. Tier 2 beats tier 3 because a team's
committed default should outrank whatever you last happened to do in some
other repository. `⌃R ⌃R` clears back to the repository default.

The resolver reports which tier supplied each value, which is what lets the
panel attribute a value (`from .herdr-draft.toml`) and `create --json` print
its provenance.

**Per-project memory re-applies when the project row changes**, unless the
user has already touched that field — the same touched-versus-preselected
rule the Linear seeding uses.

Extracting this resolver is the first piece of work, before any new tier
exists, and its acceptance criterion is that the four `assembled-*` golden
frames stay byte-identical. That identity is the proof the extraction changed
no behavior.

**Per-project memory** lives in a new `projects.json` in the state directory,
alongside the existing files:

```json
{"version": 1, "entries": {
  "/home/zvi/Projects/herdr-draft": {
    "kind": "claude", "worktree": true, "placement": "new-space",
    "base": "main", "seen": "2026-09-02T10:14:00Z"}}}
```

Keyed by the git repository root when the project is a repo, so a linked
worktree and its origin share one memory, and by the canonical absolute path
otherwise. Capped at 50 entries, evicting least-recently-seen. Written only
after a successful submit. `last-used.json` is unchanged and becomes tier 3,
so there is no migration and no data loss on upgrade.

Two new helpers, and the detail in each matters:

- `gitx.RepoRoot(ctx, dir)` must derive the root from `--git-common-dir`'s
  parent, **not** `--show-toplevel`. A linked worktree's `--show-toplevel`
  names its own checkout, which would give every worktree of one repository a
  separate memory entry. Fall back to `--show-toplevel` on git older than
  2.31. Return `""` and no error when `dir` is not a repository.
- `pathx.CanonicalKey(path)` normalizes the non-repo key: tilde-expanded,
  absolute, symlinks resolved, cleaned, no trailing separator. An
  unresolvable path is returned expanded and cleaned rather than dropped.

Two traps in the app-layer wiring, neither of which any existing test would
catch:

- `worktreeDefaultApplied` must be **replaced** by the touched flag, not left
  beside it. Two mechanisms deciding whether a default may still apply is how
  a field ends up honoring neither.
- Touched-detection needs its **own** snapshot field, because
  `syncDerivedInertness` resyncs `lastWorktreeOn` on every call. Sharing that
  snapshot silently kills memory re-application after the first project
  change.

## 11. Repo-level shared config

`.herdr-draft.toml` at the repository root, committed, so a team shares
defaults.

**Trust model: a file that arrives with `git clone` may only choose among
values the user could already have picked in the form. It may never name a
command to run, a path outside the repository, or a credential.** Anything
else makes checkout a code-execution vector.

Allowed: `branch_prefix` (validated, see below), `default_worktree`,
`default_placement`, `default_base`, and `linear_branch_name`, a new key
controlling whether a Linear issue's own `branchName` owns the branch.

Forbidden and ignored with a visible note:

- `[agents.extra_args]` — appended to a launched agent's argv, so it is
  execution.
- **`[linear] prompt_template`** — a repo-controlled template becomes the
  agent's first instruction, which is a prompt-injection surface rather than
  a preference. An earlier draft of this spec allowed it; that was wrong.
- `[agents] favorites` and `default` — a repository has no business choosing
  which agent runs on your machine.
- `[linear] api_key` and `api_key_cmd` — a credential, and a command.
- All of `[clauth]`, `[timeouts]` and `[palette]`.

**`branch_prefix` must be validated wherever it comes from.** `gitx.BranchSlug`
prepends it raw and the result reaches `herdr worktree create --branch <v>`
as argv. That is a latent argument-injection surface in the *existing*
user-config path, before any repo config exists, so the validation is worth
landing on its own schedule.

Read at form open and again whenever the project row changes, through the
same debounced-and-versioned pattern the dir and base checks use. A malformed
file is ignored and reported in the panel; it never blocks the form.
Provenance appears in the focused row's panel (`from .herdr-draft.toml`), not
in the row.

## 12. Submit and failure

Same header, rule, label column and button row as the form, so the pipeline
does not read as a different program.

```
 new session                                    herdr-draft · main
 ──────────────────────────────────────────────────────────────────
   ✓ worktree   zvi/fix-login-redirect-loop from main
   ✓ workspace  fix login redirect loop
   › claude     starting under clauth quantivly-2
     prompt     queued
```

On failure the stack stays, the failed row carries the reason, and the choice
becomes buttons:

```
   ✗ claude     agent_pane_busy after 5s

   the worktree exists and has no commits of its own
                              [ k keep it ]  [ c remove it ]
```

Clean stays disabled with its reason shown when the checkout is dirty or
ahead of base. The unsent-prompt path keeps its own row.

Unchanged safety property: Esc and Ctrl+C must not quit mid-pipeline, only in
the step-one dead end. Quitting strands `plan.Execute` on an unbuffered
channel send.

## 13. Headless `create`

`herdr-draft create`, with flags mirroring the form: `--project`, `--title`,
`--prompt` (`-` reads stdin), `--branch`, `--base`, `--worktree` /
`--no-worktree`, `--placement`, `--agent`, `--account`, `--issue`, `--json`,
`--on-failure keep|clean`. Unset flags resolve through §10's resolver, so the
command and the form produce the same session from the same inputs.

Run from a plain shell inside a herdr pane there is no
`HERDR_PLUGIN_CONTEXT_JSON`, so context comes from `HERDR_WORKSPACE_ID` /
`HERDR_TAB_ID` / `HERDR_PANE_ID`. Only the tab-here and split-here placements
need it; a new space or a worktree needs neither. Require it lazily and name
the missing variable exactly.

Progress to stderr, one line per step; result to stdout, or a single object
with `--json`. Exit codes: 0 created, 1 failed after the topology was created
(with `--on-failure` applied), 2 bad usage, 3 herdr unreachable. Never
prompts.

`main.go` dispatches on `os.Args[1]`: absent means the popup, exactly as
today; an unknown verb prints usage and exits 2.

## 14. Testing

Two new invariant tests carry the design, because all fifteen existing golden
frames are invalidated by it:

- **Rows never move.** Render the assembled form at a fixed size once per
  focusable row, normalize the focus highlight, and assert the row-stack
  region is byte-identical across all of them.
- **Every row reachable and legible at the floor.** Sweep heights from 40
  downward, asserting the focused row, the panel and the button row render at
  each one.

A third test earns its place because the current suite has no equivalent:
**`Row` must render identically at two different window heights.** That is
what makes "rows never move" a contract rather than a comment, since `Row`
takes no height parameter by design.

Golden frames regenerated with `go test ./... -update`. In `internal/form`:
`empty-80x24` and `empty-120x40` keep their names; `title-verdict`,
`dir-browse`, `issue-picker` and `account` become `*-panel-*`; new
`prompt-panel-80x24` and `worktree-panel-80x24`; `degraded-80x20` becomes
`degraded-64x12` plus `degraded-40x8`. `progress-80x24` and
`failure-clean-denied-80x24` must **not** move — `submitview.go` keeps using
`innerWidth`, and the form's new `contentBox` must not be unified with it.

In `internal/app`, the four `assembled-*` frames are regenerated, and three
are added. The most valuable is **`assembled-full-64x19`**: the real popup
interior on an 80×24 terminal, the exact size this whole redesign is
justified by, and a size nothing currently pins.

`internal/app/frames_test.go`'s `sectionMarkers` map, which hard-fails on an
unknown section ID, loses `branch` and `base` and takes the new row labels.
Its `len(ids) < 9` guard becomes `< 8`.

Fakes only: no test touches herdr, git, Linear or clauth for real. Manual
release smoke stays in `docs/manual-smoke.md` and is updated for the new
screens.

## 15. Unchanged constraints

- `internal/form` performs no I/O and knows nothing of herdr, git, Linear or
  clauth. Values arrive through setters on each field's concrete type.
- `plan.Build` stays pure.
- `--trust-repository` stays unwired. herdr 0.8.2 rejects the flag outright;
  the removal condition in v1 §9 is unchanged.
- `internal/plan/dialog.go` stays. Claude Code's first-run trust dialog is
  still undetected on herdr master, and `agent prompt`'s `agent_blocked` gate
  reads cached state rather than the screen, so the guard is load-bearing.
- The plugin manifest does not change. At rest the form is roughly seventeen
  rows, which an 80%-height popup holds from 80×24 upward.

## 16. Out of scope

Carried forward from v1 §16 and still out: variant fan-out, draft persistence
across popup close, model/effort/permission chip fields, Linear writes,
resume/account binding across herdr restore (upstream #3228), marketplace
publication, prompt-history reuse, live theme reload, and the account
exhausted-confirm modal.

Newly considered and declined: a command-bar/omnibox parser, a two-column
plan-preview layout (it collapses to the row stack below ~90 columns and the
author did not report the outcome as opaque), and session recipes or
templates.
