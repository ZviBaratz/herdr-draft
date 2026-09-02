# herdr-draft v3 — fill the card, and make the structure visible

- **Date:** 2026-09-02
- **Status:** in progress
- **Supersedes:** §4 (the screen), §7 (skin, palette, mouse) and §9
  (degradation) of `docs/specs/2026-09-02-herdr-draft-v2-design.md`, plus
  §6's `account` row. Every other v2 section, and every v1 section v2 did not
  already replace, stays authoritative and its citations keep resolving.
  New comments cite this document as `v3 spec §N`.
- **Itemised list of superseded sentences:** §13, so a reader who lands on a
  v2 sentence can find out in one place whether it still holds.

## 1. Summary

v2 shipped and was judged underwhelming on first sight in a real popup. The
author named three things: dead space on both sides, an interface that "feels
very minimal", and a clauth integration that answers none of the questions
actually asked of it.

Two of the three share one root cause, and it is not taste. **herdr draws
every plugin popup pane as an accent-bordered card filled with `panel_bg`** —
`render_popup_pane` (`herdr:src/ui/panes.rs:467`) is the same
`render_panel_shell(accent, panel_bg)` that herdr's own modals get
(`herdr:src/ui/widgets.rs:51-60`). v2 adopted herdr's *colour* conventions and
skipped nearly all of its *structural* ones; and the two structural devices v2
did build — the horizontal rules and the focused-row fill — are drawn in
colours with a contrast ratio of **1.07:1**, so neither can be seen.

v3 fills the card herdr already frames, makes the structure visible on every
builtin theme, turns panels into tables, and spends the clauth data that is
already being fetched and discarded.

No layer below the view changes. `plan.Build`, `plan.Execute`, `internal/
create`, `internal/defaults`, `internal/config`, `internal/herdrc`,
`internal/clauth` and `internal/gitx` are all untouched.

## 2. What v2 got wrong

**1. The chrome is invisible.** `theme.Palette` maps `Border` from herdr's
`surface_dim` — a colour herdr documents as a *fill* — and `dividerLine`
(`form.go:955-963`) uses it as a *stroke*. In **7 of 18** builtin palettes
`Border == PanelBG` byte-for-byte (`tokyo-night`, `dracula`, `nord`,
`gruvbox`, `one-dark`, `solarized`, `kanagawa`); on the default `catppuccin`
it is `#1e1e2e` on `#181825`. `ActiveRowBG` carries the identical value and
the identical defect — and per `form.go:859-861` that fill is the **only**
difference between a focused row and an unfocused one, v2 having deliberately
removed the `▎` gutter bar in its favour.

A full golden-frame suite could not catch this, because **frames record bytes,
not perceptibility**: a 1.07:1 rule and a 4:1 rule produce equally green
fixtures. §5 adds the test that can.

**2. The card is not filled.** `maxContentWidth = 88` (`rowlayout.go:38`),
centred by `contentBox` (`:75-88`). At 190 columns, 100 of 190 are dead.
Vertically, `layoutFrame` dumps all leftover height into `Region`
(`:252-253`) and `renderPanelRegion` blank-pads it (`form.go:881-895`) —
twenty-two empty rows at 120×40.

**3. Almost none of herdr's visual vocabulary was adopted.** No filled input
(herdr fills its own with `surface0`, `herdr:src/ui/dialogs.rs:48-76`); no
filled selected row (herdr uses four simultaneous signals, `:487-500`); no
right-flush status badge (`:504-518`); no scrollbar, on three scrollable
surfaces (herdr has one, `herdr:src/ui/scrollbar.rs:135-162`); no filter count
(`:576-600`); no marker distinguishing the current value from the cursor
(`herdr:src/ui/widgets.rs:224`).

**4. The clauth data is fetched and thrown away.** §10.

## 3. Design language

v2 §3's five rules stand unchanged and are not restated here. v3 adds one:

6. **Every structural device must be visible on every builtin theme.** A rule,
   a fill or a marker that cannot be distinguished from its background is not
   a subtle design choice; it is an absent one. Contrast is a testable
   property and §5 tests it.

## 4. The screen

Supersedes v2 §4. At the shipped size — a 104×32 popup, so 101×30 of terminal
(§6) — with focus on `title`, where the form opens.

```
┌ New session ─────────────────────────────────────────────────────────────────────────────┐
│ new session                                                          herdr-draft · main   │
│ ───────────────────────────────────────────────────────────────────────────────────────  │
│   issue      none                                                                         │
│ ▌ title      fix login redirect loop▏                                          required   │
│   prompt     —                                                                            │
│   project    ~/Projects/herdr-draft                                       invoking pane   │
│   worktree   on · zvi/fix-login-redirect-loop ← main                     remembered here  │
│   placement  worktree opens as its own space                        implied by worktree   │
│   agent      claude                                                         config.toml   │
│   account    personal · Max 20x · 5h 0% · 7d 14%                                 pinned   │
│ ───────────────────────────────────────────────────────────────────────────────────────  │
│   branch will be zvi/fix-login-redirect-loop                                              │
│                                                                                           │
│   sessions that already exist                                            3/3 workspaces   │
│   herdr-draft        idle      2 panes   zvi/v3-palette                                   │
│   report-studio      working   4 panes                                                    │
│   qspace-tls         blocked   1 pane    do/init-tls                                      │
│                                                                                           │
│ ───────────────────────────────────────────────────────────────────────────────────────  │
│ ⌃S create now · ⇥ for the prompt                            ▐ ↵ create ▌     esc cancel   │
└───────────────────────────────────────────────────────────────────────────────────────────┘
```

The outer box is herdr's, not ours. `▌` is the focused row's accent edge (§5);
the fill across the rest of that row cannot be drawn in plain text.

**The bar sits in the two-cell gutter, one column in from the border — not
flush against it.** An earlier draft of this mockup drew `│▌`, which is wrong
for a concrete reason: herdr paints the popup border in **`accent`**
(`herdr:src/ui/panes.rs:468`) and the bar is `accent` too, so flush the two
merge into a single thick stroke and the bar stops reading as a row marker.
The `sideMargin` column of §6.2 is what keeps them apart.

Focus on `issue`. Nothing above the second rule moves:

```
│ ───────────────────────────────────────────────────────────────────────────────────────  │
│   filter: qspace                                                            2/47 issues   │
│ ▸ ENG-1954   Prune QSPACE compose backups so they stop accumulating         In Progress   │
│   ENG-1930   Verify the save-point actually exists before generating               Todo  ▕│
│   DO-470     init-tls.sh never generates derived files for signed/          In Review   █│
```

Aligned columns, a right-flush badge, the filter match highlighted, and a
scrollbar in the last column — present only while the list outgrows the
window (§8).

## 5. Palette, contrast, and focus

Supersedes v2 §7's palette table and its focus-indication paragraph.

### 5.1 What was measured first

Contrast against `panel_bg`, computed for every candidate herdr field across
all seventeen RGB builtins. `terminal` has a named `panel_bg` (`Color::Reset`,
"inherit the terminal background") and so has no known value to compare
against; it is exempt throughout this section.

| candidate | worst case | on |
|---|---|---|
| `surface_dim` — today's `Border` | **1.00:1** | seven themes, byte-identical |
| `surface1` | **1.05:1** | rose-pine-dawn |
| `selection_bg` | **1.10:1** | rose-pine-dawn |
| `overlay0` | **1.69:1** | nord |
| `overlay1` | 2.44:1 | nord |
| `subtext0` | 2.93:1 | solarized-light |

This table is the whole design. **`overlay0` is the only single herdr field
legible as a stroke on every builtin.** An earlier draft of this spec chose
`surface1`, on the grounds that herdr draws its own separator with it
(`herdr:src/ui/dialogs.rs:473`) — true, but only of one dialog, where it
happens to work for catppuccin. Across the theme set it is invisible in nine
of eighteen. Where source fidelity and legibility conflict, legibility wins;
that is design rule 6.

### 5.2 The rule moves to a new field; `Border` does not move

**`Border` stays `surface_dim`, unchanged.** It is the right value for a
scrollbar *track*, and herdr's own scrollbar uses `surface_dim` for exactly
that (`herdr:src/ui/scrollbar.rs:155-157`).

**One new field:**

| Field | herdr source | catppuccin | Used for |
|---|---|---|---|
| `Overlay0` | `overlay0` | `#6c7086`, 3.59:1 | the horizontal rules, the scrollbar thumb, panel column headings, right-flush badges |

`dividerLine` (`form.go:955-963`) points at `Overlay0` instead of `Border`.
That is the entire rule fix, and it mirrors herdr's scrollbar exactly: track
`surface_dim`, thumb `overlay0`.

`Overlay0` is structural, not decorative. The palette today has **no middle
text tier** — it jumps from `Surface` at 1.40:1 to `DimText` at 7.89:1, which
is why a panel row reads as one flat wall of same-weight text. herdr uses
`text` / `subtext0` / `overlay0` inside a *single* list row
(`herdr:src/ui/dialogs.rs:487-500`).

It needs an entry in all 18 builtins (`palette.go:118-235`), a key in
`applyOverrideKey`, and decoding in `herdrThemeCustom`.

A `Faint` field was specified in an earlier draft, to preserve the
`surface_dim` value once `Border` vacated it. `Border` no longer vacates it,
so `Faint` would duplicate `Border`. Dropped.

### 5.3 `ActiveRowBG` is remapped, then floored

`ActiveRowBG` moves from `active_row_bg` to **`selection_bg`**, the more
faithful mapping: `active_row_bg` marks herdr's *active workspace* row against
`sidebar_bg`, while our focused row is a **keyboard cursor**, which herdr
paints with `selection_bg` — "the Navigate-mode cursor row in the sidebar"
(`herdr:src/app/state.rs:78`).

But no single field clears the floor: `selection_bg` fails on gruvbox-light
(1.21), kanagawa-lotus (1.24), rose-pine-dawn (1.10) and vesper (1.11). So the
translated value is **clamped up to the floor where it falls short**, by mixing
`panel_bg` toward `text`. Twenty percent of the way clears 1.25 on every theme
(worst 1.26) and tops out at 1.90, so it never glares.

```go
// ensureContrast returns fg when it already meets floor against bg, and
// otherwise bg mixed toward `toward` until it does -- so an explicit
// active_row_bg from a herdr theme or a user override is honoured wherever it
// is legible, and raised only where it is not.
func ensureContrast(bg, fg, toward Color, floor float64) Color
```

Applied at **load** time, so a user's own override goes through the same
floor. The builtin table holds the straight `selection_bg` translation; the
clamp is a load-time step, not a hand-edited table. A named or `NoColor` input
passes through untouched rather than being mixed.

Deriving beats hand-picking four values for one specific reason: a table cannot
cover a **custom herdr theme or a user override**, and those take the same code
path.

### 5.3a The contrast test

Over every builtin, asserting minimum WCAG contrast against `PanelBG`:

- **`Overlay0` >= 1.6:1** — the rule stroke. Every builtin clears it by
  construction; the assertion is what stops a future theme addition from
  reintroducing the bug.
- **`ActiveRowBG` >= 1.25:1**, on the *loaded* palette, so it guards that
  §5.3's clamp is wired in and correct.

Relative luminance is about fifteen lines of Go. `terminal` is the only
exemption, and it is exempt for a stated reason rather than a waiver.

Because the `ActiveRowBG` assertion passes by construction, it needs a direct
unit test on `ensureContrast` beside it: a value already above the floor is
returned unchanged; one below is raised to just above it; the floor is met for
every builtin's `(panel_bg, selection_bg, text)` triple; and a named colour is
passed through rather than mixed.

This is the most valuable test in v3. Without it the defect it guards is
invisible to every other form of verification the project has — a 1.07:1 rule
and a 4:1 rule produce equally green golden frames.


### 5.4 Focus indication

The focused row carries **three** signals, not one: the `ActiveRowBG` fill
across the full width, an **accent `▌` in the left gutter**, and **bold** on
the value.

This reverses v2's deliberate removal of the `▎` gutter bar (`form.go:745`,
`rowlayout.go:66`, `sizes.go:24`, pinned by `form_test.go:1006`). The reversal
is intentional and the reason is on the record: v2 removed the bar *because
the full-width fill was going to replace it*, and the fill turned out to be
invisible. herdr uses four simultaneous signals for a selected row; one is not
enough, and a 1.40:1 fill on its own is still not enough.

The bar costs nothing: v2 removed the glyph but kept the two-cell
`gutterWidth` it lived in.

### 5.5 The primary button

`↵ create` is filled — `Background(Accent)`, `Foreground(panel-contrast)`,
bold — which is herdr's recipe (`herdr:src/ui/dialogs.rs:324-343`). v2
implemented the weaker half: accent *foreground* on a `Surface` fill.

**Known cost, accepted:** v2 used fill-versus-text to show whether the focus
ring was *on* Create. One unconditional face gives that up, and herdr is no
guide here — its dialogs have no focus ring that includes buttons. Two signals
remain: no row in the stack carries the §5.4 bar, and `zoneRungs(ZoneCreate)`
swaps the whole footer ladder to `⇧⇥ back to the form`. Judged sufficient,
but it is a real loss and it is recorded rather than discovered later.

## 6. Layout — horizontal

Supersedes v2 §7's *"width is capped and the content centred horizontally, so
a 200-column terminal does not stretch rows into ribbons."*

### 6.1 The manifest

```toml
[[panes]]
id = "open"
width  = 104
height = 32
```

**Unquoted.** `PopupSize` deserializes a bare integer to `Cells`
(`herdr:src/popup_size.rs:124,133`) and rejects a quoted number — `"104"` is a
deserialization error, asserted at `:192`.

`PopupSize::Cells` exists in herdr v0.8.2, our declared `min_herdr_version`.
`resolve_popup_geometry` clamps with `.min(area.width)` / `.min(area.height)`,
so a small terminal gets a full-screen popup rather than an overflow. herdr
hands the pane `outer − 2` rows and `outer − 3` columns (`:73-85`), so the
form receives **101 × 30**, and **77 × 22** on an 80×24 terminal.

A fixed-width card is herdr's own convention: its modals are a fixed 96 cells
(`centered_popup_rect(area, 96, h)`).

**64×19 is no longer a reachable size.** The long-standing `64x19` / `120x40`
fixture pair corresponds to nothing a user can produce; §12 replaces it.

### 6.2 Fill the pane

`maxContentWidth` is deleted. `rightMargin` becomes a symmetric
`sideMargin = 1`, so the card has one column of air on both sides — today the
header, both rules and the footer sit flush against herdr's `│` glyph on the
left. `gutterWidth = 2` is unchanged: it is the row-stack indent and the panel
cursor column (`rowvalues.go:98-108`), not a margin.

```go
const sideMargin = 1

func contentBox(w int) (padLeft, inner int) {
	inner = w - gutterWidth - 2*sideMargin
	if inner < 1 {
		return 0, 1 // shed the margins before the content; this is also
	}               // what keeps inner nondecreasing in w
	return sideMargin, inner
}
```

`labelCol` is unchanged — its shrink-the-label-first rule is orthogonal and
still correct.

| w | v2 `padLeft`/`inner`/valueW | dead | v3 `padLeft`/`inner`/valueW | dead |
|---|---|---|---|---|
| 40 | 0 / 37 / 26 | 1 | 1 / 36 / 25 | 2 |
| 77 | 0 / 74 / 63 | 1 | 1 / 73 / 62 | 2 |
| **101** | 5 / 88 / 77 | **11** | 1 / 97 / 86 | 2 |
| 190 | 49 / 88 / 77 | **100** | 1 / 186 / 175 | 2 |

Why no cap at all: the pane width is now fixed by our own manifest, so any cap
is either dead code at 101 or reintroduces dead columns at the one size that
ships. And the ribbon worry assumed a naked line of text — once the focused
fill is visible (§5.4) a row is a full-width band with its text at the left.
The panel is the surface that genuinely wanted columns; it was truncating
issue titles mid-word with eighty columns empty on either side.

### 6.3 What the cap was silently protecting

`PromptField.Panel` wraps its textarea at `panelInner(w)`
(`field_prompt.go:249-255`). Clamp the measure **in the field, not the
kernel**: `promptPanelMaxWidth = 100`, inert at the shipped 101.

## 7. Layout — vertical and degradation

Supersedes v2 §9.

**There are two kinds of leftover height and only one can become margin.**

1. **`(h, n)`-determined** — what remains after the rows and chrome.
   Focus-independent. Today all of it is dumped into `Region`
   (`rowlayout.go:252-253`). This can become margin.
2. **Focus-determined** — `Region − PanelRows(focused)`, blank-filled by
   `renderPanelRegion` (`form.go:891-893`). At 101×30 that is fourteen rows
   with `title` focused. This **cannot** become margin without moving the
   footer. Only §9 addresses it.

### 7.1 The ladder

Unchanged in order — footer, rows, the panel's first `panelFloor`, rule 1,
header, rule 2 — with two rungs appended: rule 3, then the capped region, then
the margin.

```
 1. the footer                     never dropped
 2. the n stack rows               scrolled, focused row kept visible
 3. the panel's first panelFloor rows
 4. rule 1   (under the header)
 5. the header
 6. rule 2   (above the panel)
 7. rule 3   (below the panel)                          new
 8. the region, grown to at most panelCapRows           new
 9. the remainder: PadBottom = rem/2, PadTop = rem-rem/2  new
```

### 7.2 `panelCapRows = 15`

A **package constant, derived from the manifest.** The popup is 30 rows; eight
stack rows and six chrome lines leave sixteen; hold one back top and bottom as
the card's inset and the panel gets fifteen. Re-derive it if the manifest
height changes, and say so in its doc comment.

Two things this constant is deliberately *not*:

- **It is not `max(PanelRows())`.** That is 24 (`issuePanelMaxRows`), and the
  natural region at h=30 is 17 — a cap of 24 would never bind, making the
  whole change a no-op at the only size that ships.
- **It is not a parameter to `layoutFrame`.** `layoutFrame(h, n) frame` keeps
  its signature. `PromptField.PanelRows()` grows with the text you type
  (`field_prompt.go:266-273`) and `IssueField.PanelRows()` shrinks as you
  filter (`:423-428`), so any `max()`-derived cap is data-dependent — **the
  footer would jump while the user types.** A constant cannot do that.

Only the two widgets that already window their own content are clipped by 15:
issue (24) and prompt (20). Every other field's panel fits whole.

### 7.3 Worked table, n = 8

| h | pad | hdr | r1 | rows | r2 | region | r3 | ftr | pad |
|---|---|---|---|---|---|---|---|---|---|
| 40 | 6 | 1 | 1 | 8 | 1 | 15 | 1 | 1 | 6 |
| **30** | 1 | 1 | 1 | 8 | 1 | **15** | 1 | 1 | 1 |
| 29 | 1 | 1 | 1 | 8 | 1 | 15 | 1 | 1 | 0 |
| 28 | 0 | 1 | 1 | 8 | 1 | 15 | 1 | 1 | 0 |
| **22** | 0 | 1 | 1 | 8 | 1 | 9 | 1 | 1 | 0 |
| 19 | 0 | 1 | 1 | 8 | 1 | 6 | 1 | 1 | 0 |
| 16 | 0 | 1 | 1 | 8 | 1 | 3 | 1 | 1 | 0 |
| 15 | 0 | 1 | 1 | 8 | 1 | 3 | 0 | 1 | 0 |
| 14 | 0 | 0 | 1 | 8 | 0 | 3 | 0 | 1 | 0 |
| 13 | 0 | 0 | 1 | 8 | 0 | 3 | 0 | 1 | 0 |
| 12 | 0 | 0 | 0 | 8 | 0 | 3 | 0 | 1 | 0 |
| 11 | 0 | 0 | 0 | 7 | 0 | 3 | 0 | 1 | 0 |

30 is the popup; 22 is the popup on an 80×24 terminal. **Everything at h ≤ 15
is byte-identical to v2** — the change lives entirely at h ≥ 16.

Monotonic throughout: `Region` is a `min` of a nondecreasing quantity, and
both pads are nondecreasing in `rem`. Two further invariants, free and worth
pinning: pads exist only once the region is full
(`PadTop > 0 || PadBottom > 0 ⇒ Region == panelCapRows`), and the top is
favoured on an odd remainder (`0 <= PadTop − PadBottom <= 1`).

`frame` gains `PadTop`, `PadBottom` and `Rule3`; `lines()` sums them.
**`TestLayoutFrame_IsMonotone` enumerates components by hand
(`rowlayout_test.go:180-190`) — omitting the new three silently stops checking
them.**

### 7.4 Rule 3

A divider between the region and the footer, ranked **last** in the ladder:
dropped before rule 2, because rule 2 separates two kinds of content while
rule 3 only closes the card.

The blank region reads as a *void* because it is bounded above by a rule and
below by nothing. Enclose it and the same rows read as an empty panel inside a
card, which is what they are.

The zero-row alternative — painting the footer line in `Surface` to make it a
bar — is rejected on a hard fact: **`cancelButton` (`form.go:1097`) is itself
filled with `Surface`**, so it would vanish into the bar.

(This paragraph originally cited the unfocused `createButton` as well. §5.5
made Create unconditionally accent-filled, so only cancel is a `Surface` face
now. The conclusion is unchanged — one disappearing button is enough — but do
not cite the second half.)

### 7.5 The footer, and `submitview`

`renderFooter` and `spreadLine` need **no change**. The footer's reach is
entirely a function of the `boxWidth` `composeRows:822` hands it; fixing
`contentBox` moves it from `[5, 94]` to `[1, 99]` at w=101.

One latent bug to fix while here: `form.go:925`'s `both < width` is strict, so
when the two buttons fit *exactly* it drops cancel — contradicting the
documented priority at `:912-915`.

`submitview.go:279-342` shares this kernel and must emit the pads and handle
`Rule3`. Do **not** copy its trick of folding `Rule2` into the region as the
region's own top border (`:320-326`); render `Rule3` as a plain divider, or
`TestSubmitView_RuleMatchesTheForm` (`:190-198`) needs a story.

## 8. Panels are tables

### 8.1 The item type

```go
type PickerItem struct {
    ID        string
    Cells     []string     // PLAIN text, left to right
    Badge     string       // right-flush status word, own aligned column
    BadgeTone Tone
    Marker    string       // "!" — a FIXED 2-cell column, before cell 0
    Current   bool         // the field's current VALUE, not the cursor
    Match     PickerMatch  // {Col, Start, End}; Col < 0 == nothing to paint
                           // End is HALF-OPEN -- see below
}

type PickerColumn struct{ Min, Max int; Flex bool; Tone Tone; Elide ElideMode }
func (p *Picker) SetColumns(cols ...PickerColumn)
```

Cells must be plain text: the picker measures them for alignment, truncates
them per column, and paints a match span inside one — none of which it can do
to a string a caller has already styled.

Column widths are measured over the **whole filtered set, never the visible
window**, or they jitter as you scroll. Invalidate in `SetItems` and
`SetQuery`, the two places `p.filtered` is rebuilt.

`Tone` is a closed enum of palette *roles*. v2 §7 declined a per-item style
hook and that judgement stands in spirit — no caller-supplied
`lipgloss.Style`; the picker resolves every colour from its own injected
palette. Its stated *reason* was factually wrong and does not: "rows are
rendered by the field, not by the picker" is false — `Picker.renderRow`
(`picker.go:415-433`) renders them. Only the account *stack* row is rendered
by its field.

`lipgloss/v2/table` was considered and loses on a hard constraint: it **wraps**
cells, so a row is not one line, breaking both `Panel(w, h)`'s exactly-h-lines
contract and `Picker.View`'s exactly-height-rows contract at the first long
issue title. Its column sizing is also a median-of-non-whitespace heuristic,
so widths would shift per keystroke.

### 8.2 The selected row

herdr's four signals, and the fourth already exists:

| herdr | here |
|---|---|
| `bg(surface0)` | `Background(Surface)`, full row width including padding |
| `BOLD` | `.Bold(true)` on the cursor row's cells |
| brighter fg | `Foreground(Text)` — **changed from `Accent`** |
| `›` marker | the existing `▸` panel gutter glyph, unchanged |

No second cursor glyph inside the row; `picker.go:236-245` documents why it
lives in the gutter. And drop `Accent` from the cursor row's foreground — with
an accent gutter glyph and an accent match span, three accents on one row is
noise. herdr uses plain `text` on `surface0` and reserves accent for the
search field.

**`Current` is not the cursor, and it fixes a live bug.** In `AgentField`,
`←`/`→` move the chip row and set `lastConfirmed` (`:161-166`, `:218-222`)
without touching the picker cursor, which is only re-seeded by `SetKind`
(`:195-206`) — so today the agent panel highlights kind X while the row above
reads kind Y. `Current: k == f.lastConfirmed` repairs it.

**Only `AgentField` sets it.** An earlier draft said the other four pickers
"set it anyway, cheaply and honestly". That is wrong on every word but the
last. In those four the value is *derived from* the cursor, so there is no
stored value to compare against: setting `Current` means either a flag that
goes stale on every `↑↓` or re-feeding the whole item list per keystroke — to
draw a `✓` immediately beside a `▸` that already says the same thing, and to
force a two-cell mark column onto three pickers (dir, worktree-base, issue)
that otherwise need none. `AgentField` is precisely where cursor and value
genuinely diverge, which is why it is the one with a bug to fix.

`AccountField` will want `Current` once §10.3 makes `Pin()` a deliberate
commit rather than a cursor position. Today `Pin()` *is* the cursor, so it
would be the same redundancy.

The mark column is therefore reserved only when the filtered set actually
uses it.

**One copy relocation columnisation forced.** The `active` sentinel's hint
(`use whatever profile is live`) is prose, and prose does not survive being a
cell: it sets the *plan* column's width for every profile row beneath it, and
moving it to the badge strands the explanation twenty-eight cells from its
label. It moves to the panel's status line — which `AccountField` already
reserves and which is empty in exactly that state, and which is the same place
§10.2 puts the `●` legend.

`✓` (current) and `!` (marker) share one two-cell mark column in priority
order: marker wins. A profile that is both current and auth-failed must shout
the failure, and its currency is already stated on the stack row above.

The mark column is **fixed-width**. Today `Marker` is a bare prefix
(`:423-426`), so a marked row shifts two cells right and stops lining up;
`account-panel-80x24.txt` currently pins that as correct.

### 8.3 The fill hazard

`Style.Render`'s trailing reset is unconditional, so an outer
`Background(Surface)` around a row containing an accent match span loses the
fill after that span — precisely the hazard `paintLine` documents
(`sizes.go:110-138`).

So: hoist `paintLine`'s body to `widgets.PaintLine` (form already imports
widgets; the reverse would cycle), leave `form.paintLine` as a delegator, and
have the picker reassert its own background after every embedded reset.

The frame-level interaction resolves in our favour — the picker's `bgSurface`
is inserted after each reset *before* `composeRows:830` inserts `bgPanel`, so
Surface is last and wins — but that is a byte-level argument, not a proof.
Render a real example and read the bytes, as this codebase already does at
`sizes.go:110-138` and `picker.go:457-468`, and pin it with a test.

### 8.4 Match highlighting

> **Two span types, two conventions. Read this before writing the renderer.**
> `widgets.PickerMatch.End` is **half-open** `[Start, End)`; `fuzzySpan.End`
> is **inclusive**. That is deliberate, not an oversight: `PickerMatch`'s zero
> value has to be inert, because all five fields leave `Match` unset on most
> items, and an inclusive `{0,0,0}` would paint the first character of column
> zero in four fields at once. Inclusive coordinates have no inert zero.
> **A caller bridging the two adds one to `End` at the boundary.**

`fuzzyMatch` (`fuzzy.go:41`) already returns `(ok, start, end)`; `fuzzyRank`
(`:150-154`) discards both. Add `fuzzyRankSpans` returning
`{Text, Start, End}` and re-express `fuzzyRank` over it — unchanged signature,
so `fuzzy_test.go` does not move.

**Match the string that will be DISPLAYED, not the string that was ranked.**
`DirField` ranks full paths but displays `collapseHome(it)` (`:630`), and in
path mode ranks basenames but displays full paths (`:675-705`). Re-running
`fuzzyMatch` against the display text is O(n) and needs no coordinate
arithmetic through two transforms.

Ownership, since two callers fill one field: `applyFilter` computes `Match`
for every item it keeps whenever `p.query != ""`; when the query is empty the
caller's own `Match` is preserved verbatim, which is how a field that ranks
its own items supplies spans itself.

### 8.5 Scrollbar and filter count

**The scrollbar costs a column, not a line** — the last cell of each row,
reserved only while the list outgrows the window. Track `▕` in `Border`, thumb
`█` in `Overlay0`, geometry a pure sibling of `scrollOffset` (`:438-450`)
ported from `herdr:src/ui/scrollbar.rs:36-69` and table-tested the same way.

**The filter count costs a line already paid for.** `IssueField` (`:407`),
`DirField` (`:590`) and `AccountField` (`:535`) each already reserve a status
line that is usually empty, so the count is right-flushed onto it via the
existing `spreadLine`. Agent and worktree-base get none: neither filters,
neither usually scrolls. **No `PanelRows()` grows anywhere**, by scoping.

The count does not go on the row: `IssueField.Row` and `DirField.Row` return
the live input while focused, and a count there competes with the text being
typed. v2 §3 rule 1 — rows stay quiet.

### 8.6 Gauges

`gaugeBar(fraction float64, width int) string` in `rowvalues.go`, unexported:
`█` filled, `░` empty, ten cells, whole blocks. Eighths render inconsistently
across fonts and buy nothing at this size.

It returns plain text into a `Cell`, so the picker tones and aligns it like
any other column. Colour the *word* — the `Badge` in `ToneWarning` /
`ToneDanger` — not the bar.

**Not `bubbles/v2/progress`**, and the first reason settles it: it imports
`github.com/charmbracelet/harmonica`, which has zero entries in `go.sum`, so
it is not the free dependency it appears to be. It is also a `tea.Model` built
for animated spring transitions, against a widgets package whose doc opens
with "no I/O, no owned `bubbletea.Program`, no global renderer state".

### 8.7 Filled inputs

`lineInputStyles` (`lineinput.go:79-97`) and `paletteStyles`
(`widgets/textarea.go:126-152`) both set no background, citing "backgrounds
are the form root's job". Right for a full-bleed panel, wrong for an *input*:
herdr fills its own with `surface0`, and an empty input here is currently
indistinguishable from empty space.

One call: `lineInput.View` returns `paintLine(l.ti.View(), width, l.fill)`.
`paintLine` already pads to exactly width, reasserts the background after
every reset — `textinput.View` emits several — and **no-ops for
`lipgloss.NoColor{}`**, so the `terminal` theme, whose `Surface` *is*
`NoColor{}` (`palette.go:203-205`), correctly gets no fill.

`widgets/textarea.go` takes the same via `PaintLine`, but a four-row filled
block is much heavier than a one-row one — make it a `SetFill` the field opts
into, so the decision is visible at the call site.

### 8.7a The fill is DERIVED, not `Surface`

This paragraph was added after the section shipped, because the section did
not say what `l.fill` is and issue #27, which did, was wrong. Recorded here
rather than left in the code alone, since §8.7 as written would mislead the
next reader into the exact defect v3 exists to fix.

**An input is only ever rendered while its field is focused** — `TitleField.
Row`, `IssueField.Row`, `DirField.Row` all gate on it — and a focused stack
row is filled `ActiveRowBG` edge to edge before the input's own fill is
composited into it (§5.4). So the pair that decides whether an input is
visible at all is `Surface` against `ActiveRowBG`, not `Surface` against
`PanelBG`. Measured across all seventeen RGB builtins:

| theme | `Surface` vs `ActiveRowBG` |
|---|---|
| **catppuccin** (the default) | **1.000:1** — the two fields are the byte-identical `#313244` |
| tokyo-night-day | 1.002:1 |
| catppuccin-latte | 1.007:1 |
| gruvbox-light / kanagawa-lotus / dracula | 1.07:1 |

A flat `Surface` fill moves every golden frame and zero pixels on the theme
most users see. That is §2's founding defect, one field over, and it is
exactly what §3 rule 6 exists to forbid.

So: `Palette.InputFill(ground) Color` returns `Surface` where `Surface`
already clears `InputFillContrastFloor` (1.25, the same number and the same
reasoning as `ActiveRowContrastFloor`) against that ground — herdr's own
pairing, kept wherever it works — and otherwise the **ground** raised away
by `ensureContrast`, the same walk §5.3 uses, so the package has one clamp
and not two. Worst case after: **1.256:1** (rose-pine-dawn), nothing above
1.556:1, every builtin clearing on both grounds.

`ground` is a **required argument at every call site**, not a default,
because getting it wrong is invisible by construction. Three inputs sit on
`ActiveRowBG` (title, issue, dir); two sit on `PanelBG` (the worktree branch
input and the prompt textarea, both drawn inside the detail panel).

The raise mixes the ground toward `Text` rather than toward the panel, so
the input is a slightly *raised chip* and not an inset well. A well would be
`PanelBG`, and a `PanelBG` rectangle inside a focused row lines up
vertically with the unfocused rows above and below it — the band would read
as having a hole punched through it rather than as carrying an input.

Two further prescriptions in issue #27 are **declined with evidence**, not
followed: a `Background` on `Focused.Text` / `Blurred.Text` / `Placeholder`,
and one on the textarea's `CursorLine` / `EndOfBuffer`. Those styles set a
Foreground only, so they emit no background SGR to overwrite `PaintLine`'s
reasserted fill, and the fill is already unbroken. That is pinned cell by
cell over a real four-row block rather than argued — which is also where a
future `bubbles` release that starts emitting one gets caught.

## 9. The resting panel

`TitleField.PanelRows()` returns **1** (`field_title.go:190`) while
`issuePanelMaxRows` is **24**. Because the region is fixed so the rule and
footer do not move as focus travels, **no popup height serves both**: size it
for the picker and the opening screen has a twenty-row hole; size it for the
opening screen and the picker scrolls at ten issues.

No layout change fixes this (§7's two kinds of leftover). The region gets
content instead.

**The source is `herdr workspace list`, already fetched and already in
memory.** `Bootstrap` calls `runner.WorkspaceList` once at open
(`internal/app/app.go:225`) and retains it, never re-fetching (`:338`).
`herdrc.WorkspaceInfo` (`runner.go:29-39`) carries `Label`, `AgentStatus`,
`PaneCount`, `TabCount`, `Worktree` and `Focused`.

```
   branch will be fix-login-redirect-loop

   sessions that already exist
   herdr-draft        idle      2 panes   zvi/v3-palette
   report-studio      working   4 panes
   qspace-tls         blocked   1 pane    do/init-tls
```

No new I/O, no new subprocess, no new state file, nothing new collected about
the user. It also makes an existing check visible: the app already computes
`labelTaken` (`async.go:569`) from this same data, but that only surfaces
*after* you have typed a colliding title.

Constraints:

- `TitleField.PanelRows()` becomes `capRows(2 + len(sessions), …)`.
- The data is fetched **once**, at Bootstrap, and goes stale if a session is
  created elsewhere while the form is open. Acceptable — the panel is
  informational and the authoritative duplicate check still runs at submit —
  but the panel must not imply it is live.
- `internal/form` stays a dumb view: the app pushes the list through a setter
  on `*TitleField`'s concrete type, as every other candidate list is pushed.
- It is a list in a panel, so it inherits §8 wholesale. It is not a new widget.

## 10. clauth

Supersedes v2 §6's `account` row.

`internal/clauth` needs **no change**: `Profile.Windows` is already a full
slice of `{Label, UtilizationPct, ResetsAt}`, and clauth reports three windows
per profile (`5h`, `7d`, `7d fable`). What discards them is one constant,
`accountWindowLabel = "5h"` (`field_account.go:71`), filtered on by both
`accountWindowHint` (`:338`) and `accountUtilization` (`:500`). Nine values
per profile arrive; one is rendered.

### 10.1 The row

```
  account    personal · Max 20x · 5h 0% · 7d 14%
```

v2 renders `personal · Max 20x · ok` because `accountRowState` (`:463-478`)
returns the literal `AuthStatus` when a profile is pinned and utilization only
when none is. That is backwards: **`ok` is the state that needs no words.**
Auth status appears only when it is *not* `ok`, as `sign in again` in
`Danger`.

Read windows by label from an ordered list rather than hard-coding `"5h"`, so
the third window is dropped by an explicit decision rather than by a constant
nobody remembers.

### 10.2 The panel

```
   profile        plan      5h              7d            resets
 ▸ personal       Max 20x   ░░░░░░░░░░   0%  █░░░░░░░░░  14%  in 2h11m
   quantivly-3 ●  Team      █████░░░░░  57%  ██░░░░░░░░  21%  in 2h11m
   quantivly-2    Team      ██████████  98%  █░░░░░░░░░  13%  in 1h40m

 ● live in clauth · the pinned profile is what this session launches under
```

`●` marks clauth's **`ActiveProfile`**, which the app currently reads only as
a lookup key (`:433`) and never displays. *"What is the active account"* was
one of the three questions asked, and today the screen cannot answer it.

Reset times are relative. `internal/form` does no I/O, so `now` is supplied by
the app alongside the status; clauth already reloads on account-field focus
(`async.go:695-709`), which bounds the staleness. If that plumbing proves
awkward, fall back to absolute (`resets 15:29`) rather than reaching for a
clock inside the view.

**The warning threshold drops from 100% to 95%**, matching clauth's own
default auto-switch trip point. Today `:467` fires only at `>= 100`, so a
profile at 98% — a real live value — warns nowhere, in either surface.

### 10.3 `Pin()` must mean a deliberate choice

`AccountField.Pin()` (`:395-401`) returns **the picker's current cursor
position**, translating only the `active` sentinel back to `""`. Nothing
distinguishes "the user chose this profile" from "the cursor is resting here".
So visiting the account row and pressing `↓` once permanently pins an
account — and `Pin()` feeds `plan.Input.AccountPin`, so the session really
does launch under `clauth <that profile>`.

This is a correctness bug, not a cosmetic one. Pinning becomes an explicit
selection commit.

Also: `accountRow` (`:312`) and `Row` (`:445`) build the same `·`-joined
string by hand with different rules, and the panel emits a dangling `· ` when
`AuthStatus` is empty.

## 11. The provenance column

The row stack's right half is empty painted background at any real width. It
carries **why each value is what it is** — which `defaults.Resolved.From`
already records per field, and which is exactly the "defaults are visibly
decided" goal v2 set itself and then buried in a panel.

```
  worktree   on · zvi/fix-login-redirect-loop ← main      remembered here
  agent      claude                                       config.toml
  placement  worktree opens as its own space              implied by worktree
```

Rendered only above a width threshold, and only for rows whose value came from
a non-builtin tier.

**This is the one element of v3 the author did not explicitly select.** It is
deliberately isolated — no dependants — so it can be dropped without
disturbing anything else.

## 12. Testing

Everything in v2 §14 still applies. v3 adds and changes:

- **The contrast test of §5.3.** The only net that catches the class of defect
  v3 exists to fix.
- **Regenerate golden frames per package** — `go test ./internal/form/ -update`
  then `go test ./internal/app/ -update`. A bare `go test ./... -update`
  **fails**, because the flag is registered only in `form_test.go:21` and
  `app/frames_test.go:40`. v2 §14 line 621 states this incorrectly.
- **Retire the `64x19` / `120x40` fixture pair** (§6.1). New sizes: **101×30**
  (the popup), **77×22** (the popup on an 80×24 terminal), **57×18** (a 60×20
  terminal, in the band where rule 3 is gone but rule 2 is not), and one
  oversized **150×44** whose only job is to pin the pads and the footer's
  full-width reach.

  **This applies to the whole-form frames only.** `internal/form`'s
  single-field fixtures (`title-panel-80x24` and its siblings) render a
  synthetic one-section form that no popup ever shows, so their width is an
  arbitrary measuring stick rather than a claim about a real terminal. They
  keep 80×24; renaming them would churn every fixture in the package to say
  nothing new.
- **Four states nobody currently fixtures**, each made load-bearing by v3: a
  small panel in a large region on the *assembled* form (`title` focused,
  `PanelRows() == 1` — the exact screen §7 is judged by, and it does not
  exist); a capped panel with the picker scrolling; the rule-3 boundary at
  h=15 vs h=16; and the pad boundary at h=29 vs h=30.
- **`TestAssembledForm_OpeningState` must loop over sizes.** The v2 defect
  shipped through fifteen green commits because every fixture had a title
  typed; the same trap is set one axis over, with the opening state pinned at
  exactly one size. Opening-state × degradation is a product of two axes and
  one cell of it is covered.
- **Tests that break by design** are listed in the implementation plan; the
  four that index frame lines by hand — `form_test.go:918`,
  `field_rows_test.go:181-183`, `form_test.go:930-961`, `sizes_test.go:31-33`
  — must derive their offsets from the frame rather than assume 2. The first
  two are what mechanically enforce `Row(w)`'s no-height contract.

**Manual verification is not optional.** `go test ./...` passing is not
evidence the screen is right — that is the lesson v2 paid for twice, once in
the opening state and once in a rule nobody could see. Build it, open the real
popup, and look.

`docs/manual-smoke.md:227` currently instructs the reader to *"confirm focus
is on the title row (it is painted)"* — a check that cannot presently be
performed. It becomes meaningful for the first time.

## 13. Superseded v2 text, itemised

| v2 spec | status |
|---|---|
| §4, the mockups | replaced by §4 |
| §6, the `account` row's value table | replaced by §10.1 |
| §7, the palette table | extended by §5.2–5.3: one new field (`Overlay0`), one remapping (`ActiveRowBG`), and a load-time contrast floor. `Border` is unchanged |
| §7, "focus indication is the full-width `ActiveRowBG` fill, not a gutter bar" | replaced by §5.4; the bar returns, with reasons |
| §7, "width is capped and the content centred" | replaced by §6.2; no cap |
| §7, "`widgets.Picker` needs no per-item style hook" | spirit stands, stated reason was wrong; see §8.1 |
| §9, the five-step ladder | replaced by §7.1's nine rungs |
| §14 line 621, `go test ./... -update` | wrong; see §12 |
| §15 line 652, "the plugin manifest does not change" | replaced by §6.1 |

## 14. Out of scope

Offered to the author and declined; do not build them:

- **Row grouping bands** (what / where / how).
- **Git ahead/behind status in the header.**
- **clauth's auto-switch chain** — `fallback` position, threshold, armed.
  The data exists in `clauth status --json` and is dropped at our JSON layer;
  leave it dropped.

Unchanged from v2 and still true: `--trust-repository` stays unwired (herdr
0.8.2 rejects the flag), and `internal/plan/dialog.go` stays (Claude Code's
first-run trust prompt is still undetected).
