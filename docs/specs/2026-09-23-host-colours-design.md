# herdr-draft — learn the host terminal's real colours

- **Date:** 2026-09-23
- **Status:** approved by the owner 2026-09-23 (all six of §2, decision 5
  with a request to explain it, which §4.2 now does at more length),
  **implemented the same day**, and then **independently reviewed** — a
  blocker, eight should-fixes and ten nits, all fixed. §5.2, §6.1 and §9.2
  are corrected from what was approved, where building it or reviewing it
  found the design wrong; every correction is marked where it sits rather
  than silently applied. The review is
  `~/Projects/handoffs/herdr-draft-348-review.md`.
- **Issue:** #347, which closes with the implementation.
- **Amends, on approval:** v3 spec §5.3a's "`terminal` is the only
  exemption", which becomes "`terminal` is exempt until the host answers";
  and v3 spec §5.4's three focus signals, which on that palette have been two
  since #346 and become three again wherever the background is known. §13
  itemises. It amends **nothing** in the draw-first spec: §3.1's rule is that
  `Bootstrap` runs no subprocess but `herdr workspace list` before the draw,
  and this design adds nothing before the draw (§4.1).
- **Does not amend:** v3 spec §5.3's clamp, which is reused unchanged and is
  the whole of the arithmetic here; or v1 spec §7's palette bullet, whose
  `name = "terminal"` sentence ("unknowable from config alone") was already
  superseded by v2 §7 and retired in practice by #345. This design is the
  reason that sentence is now false in both halves: the palette is knowable,
  and not from config.
- **Citations.** herdr is cited at the current pin, `v0.9.0`
  (`b99002ac99b09e00b4ca692436cb15a6b0d676f1`). Go dependencies are cited at
  the versions in `go.mod`: `charm.land/bubbletea/v2 v2.0.9`,
  `github.com/charmbracelet/x/ansi v0.11.8`, and
  `github.com/charmbracelet/ultraviolet v0.0.0-20260811164956-006e29f97886`,
  which is `// indirect` today and would stop being (§10).
- **Evidence.** Every measurement in §3 was taken on **2026-09-23**, in a
  real herdr pane on the author's machine — herdr 0.9.0, host terminal
  alacritty, background `#181818`, foreground `#d8d8d8`. §5.4's table was
  produced by running the **existing** `floorContrast` over the 43 schemes in
  `/home/zvi/Projects/handoffs/herdr-draft-304-scheme-measurement/schemes`,
  the same evidence base #304 and #345 used.

---

## 1. Summary

A program inside a herdr pane can ask the terminal what colour it is
actually drawing, and get the **host's** answer. Measured, not inferred: OSC
11, OSC 10 and OSC 4 for every index this palette uses all came back, all
carried the host's values, and all reached a Bubble Tea `Update` as messages
(§3). The premise #347 is built on holds.

So the `terminal` palette stops being the one palette this process cannot
measure. The design is three steps and one new pure function:

- **Query from `Init`** (§4). Nothing moves ahead of the first frame. The
  answers landed 18.8–23.3 ms after process start across five runs, which is
  ~18–22 ms after the first `View()` — one repaint, inside the first frame's
  own budget.
- **Use the answers as measurements, not as emissions** (§5). The learned
  background and foreground are what the floors are computed *against*; they
  are never sent. An index that clears its floor stays an index and keeps
  following the user's terminal, which is the whole of what #304 bought. An
  index that does not clear it is replaced by the RGB the existing clamp
  produces, because an index that is measurably illegible is not worth
  preserving.
- **The focused row gets its fill back** (§5.2). `ActiveRowBG` and `Surface`
  are fills whose only job is to differ from the background, and an inherited
  fill cannot differ. With a real background they are seeded from it and
  raised by the same `ensureContrast` every other theme uses. Over the 43
  schemes that lands between **1.254:1 and 1.431:1**, floor 1.25.
- **No answer changes nothing** (§6). Headless `create`, an older herdr, a
  host that never answered herdr's own probe, a terminal that is not herdr:
  today's screen, unchanged, and the five existing guards still pin it.

Over the 43 schemes, **13 keep every index untouched** and gain only the
band. The rest have between one and five words raised, and **no clamp gives
up** — every raise clears its floor.

## 2. Decisions

These are the rulings the design needs. Each has a recommendation and the
reason it could go the other way.

1. **Ship it at all**, given §3 confirms the premise but §3.4 verifies the
   *popup* pane in herdr's source rather than on screen.
   **Recommendation: yes**, and §12 names the one experiment that would
   close §3.4 if the owner wants it closed first.
2. **RGB versus indices** (#347's "which colour wins"). **Recommendation:
   keep the index wherever it clears its floor; emit RGB only where the
   clamp moved it** (§5.3). The alternative — send RGB for everything once
   the colours are known — is simpler to describe and throws away #304 on
   13 of 43 schemes that did not need it.
3. **`ActiveRowBG` and `Surface` become RGB whenever the background is
   known**, not only where a clamp moved them (§5.2). This is the one place
   the rule in decision 2 does not apply, and it is not an exception so much
   as the same rule reaching a different answer: a fill that equals the
   background is not a fill. It is also what un-does #346 on this palette,
   so it is the decision most visible on screen.

   > **Qualified by §5.2, which is where the detail lives.** "Whenever the
   > background is known" is the approved wording and it is one condition
   > short: both fills also need the *foreground*, because it is what the
   > clamp walks toward. With a background and no foreground the row stays
   > unfilled, which is the right answer rather than a gap — a fill painted
   > a flat copy of its own background is not one. Noted here because this
   > is the text that was approved (#348 review, N7).
4. **`Overlay0` is left following ANSI 7, unfloored** (§5.5), which means on
   a light host the labels walk dark (`DimText`, floored) while the rules
   stay light (`Overlay0`, not floored) — two fields, one source index,
   diverging. **Recommendation: accept it.** `floorContrast` floors
   `Overlay0` on no theme today; adding it would move a value seventeen
   other palettes measure against, which is the hazard CLAUDE.md's "a change
   that moves a shared value" bullet is about. But it is a visible
   divergence and the owner may prefer the consistency.
5. **The budget is 250 ms** (§4.2), after which a late answer is dropped
   rather than applied. Ten times the worst measurement, and under the floor
   of how fast a person can react to the popup appearing, so a repaint can
   never land mid-sentence.
6. **Drift is out of scope** (§8): a host theme changed while the popup is
   open is not followed. #347 says this "probably doesn't need to" and the
   popup's median lifetime is seconds.

## 3. The premise, measured

### 3.1 What a herdr pane answers

A program that puts `/dev/tty` in raw mode and writes `OSC 11 ?`, `OSC 10 ?`
and `OSC 4;n;?` for n in 0–15, run in a pane created by `herdr tab create`,
got **eighteen replies for eighteen queries**, plus the DA1 control:

```
\x1b]11;rgb:1818/1818/1818\x1b\\
\x1b]10;rgb:d8d8/d8d8/d8d8\x1b\\
\x1b]4;0;rgb:1818/1818/1818\x1b\\     \x1b]4;1;rgb:acac/4242/4242\x1b\\
\x1b]4;2;rgb:9090/a9a9/5959\x1b\\     \x1b]4;3;rgb:f4f4/bfbf/7575\x1b\\
\x1b]4;4;rgb:6a6a/9f9f/b5b5\x1b\\     \x1b]4;7;rgb:d8d8/d8d8/d8d8\x1b\\
\x1b]4;8;rgb:6b6b/6b6b/6b6b\x1b\\     \x1b]4;9;rgb:c5c5/5555/5555\x1b\\
```

The shape is herdr's `osc_rgb_response`
([`src/pane/terminal.rs#L3312`](https://github.com/herdrdev/herdr/blob/v0.9.0/src/pane/terminal.rs#L3312)):
`\x1b]{cmd};rgb:{r:04x}/{g:04x}/{b:04x}\x1b\\`, 16-bit channels at
`8-bit × 257`, ST-terminated.

### 3.2 It is the host's palette, not ghostty's

Two independent reasons, and the second is the one that settles it.

**herdr only answers OSC 10 and 11 from a host theme it actually has.**
`default_color_query_response`
([`terminal.rs#L3253`](https://github.com/herdrdev/herdr/blob/v0.9.0/src/pane/terminal.rs#L3253))
reads `core.host_terminal_theme.foreground` / `.background`, which are
`Option` and default to `None`, and `?`-returns — **no reply at all** — when
they are unset. A reply is therefore proof that herdr's own probe of the host
succeeded and that this is its result. The palette rides on the same object:
`apply_host_terminal_theme`
([`terminal.rs#L1177`](https://github.com/herdrdev/herdr/blob/v0.9.0/src/pane/terminal.rs#L1177))
seeds the pane's ghostty palette from `theme.palette`, and on Linux outside
WSL herdr does ask for all 256 entries
(`should_query_host_terminal_palette`,
[`src/platform/linux.rs#L45`](https://github.com/herdrdev/herdr/blob/v0.9.0/src/platform/linux.rs#L45),
reached from
[`src/client/terminal_geometry.rs#L199`](https://github.com/herdrdev/herdr/blob/v0.9.0/src/client/terminal_geometry.rs#L199)).

**And every one of the sixteen differs from ghostty's default.** Where the
host probe supplies nothing, `apply_host_terminal_theme` leaves
`ghostty::default_palette()` in place, so ghostty's defaults are exactly what
a failure would look like. Ghostty's defaults are the Tomorrow Night table
(`ghostty-org/ghostty`, `src/terminal/color.zig`: `.red => 0xCC6666`,
`.green => 0xB5BD68`, `.blue => 0x81A2BE`, …). The pane answered `#ac4242`,
`#90a959`, `#6a9fb5`. **16 of 16 entries differ.** There is no partial
explanation: the palette came from the host.

### 3.3 Bubble Tea surfaces all three

The question #347 raises — "it is not yet known whether Bubble Tea surfaces
the reply to the program" — is answered, and the answer is yes, by two
different routes.

- **OSC 10/11** are parsed by ultraviolet into `ForegroundColorEvent` /
  `BackgroundColorEvent` (`decoder.go`, `parseOsc`, `case 10:`/`case 11:`)
  and re-typed by `translateInputEvent` (`bubbletea/v2@v2.0.9/input.go:13-15`)
  into `tea.ForegroundColorMsg` / `tea.BackgroundColorMsg`.
- **OSC 4** falls through `parseOsc`'s switch to
  `UnknownOscEvent(b[:i])` — the raw bytes as a string — and
  `translateInputEvent`'s final `return e` hands it to `Update` unchanged.
  It is **not swallowed**; it simply arrives untyped.

And the send has a supported path: `tea.Raw(string)` (`raw.go`) reaches
`p.execute` (`tea.go:866`), the same output buffer under the same `p.mu` that
bubbletea's own `RequestBackgroundColor` uses. There is no need to write to
the tty behind the renderer's back, and no need to take the terminal out of
bubbletea's hands before the program starts — which matters, because the one
thing that route risks is leaving half a reply in the input buffer for
bubbletea to parse as keystrokes.

Verified end to end by a real `tea.Program` in a real pane:

```
BackgroundColorMsg #181818
ForegroundColorMsg #d8d8d8
uv.UnknownOscEvent "\x1b]4;1;rgb:acac/4242/4242\x1b\\"      (… 2, 3, 4, 7, 8, 9)
```

### 3.4 The popup pane: verified in source, then measured

This was the one gap, and #347 names it: the measurement above was taken in
an ordinary pane, not in a plugin popup, and a popup cannot be exercised
without a registered plugin — which the owner's live install is, and which
this work may not touch.

**It is now measured too** (2026-09-23, after the implementation landed).
The reproduction is
`~/Projects/handoffs/herdr-draft-347-popup-verification/`, and the trick is
to impersonate the host rather than to borrow one: an isolated herdr
(`--session hcprobe`, scratch `XDG_CONFIG_HOME`) started under a pty the
harness owns, which answers that client's host-terminal probe with
**sentinel** colours — background `#012345`, foreground `#fedcba`, palette
index N as `#{N:02x}abcd`. A scratch plugin with `placement = "popup"` is
linked into that session and opened with `herdr plugin pane open`.

Every sentinel came back:

```
\x1b]11;rgb:0101/2323/4545\x1b\      -> #012345
\x1b]10;rgb:fefe/dcdc/baba\x1b\      -> #fedcba
\x1b]4;4;rgb:0404/abab/cdcd\x1b\     -> #04abcd, and so for 0..15
```

Those values exist nowhere but in the harness, so the popup pane served the
palette of the terminal the client was attached to. Together with §3.1 —
a *real* host, measured in an ordinary pane — the chain is complete at both
ends. Two incidental confirmations: the popup's process had a real
controlling tty, and `HERDR_PANE_ID` was unset in it, which is
CONTRIBUTING.md's "the real popup has no pane id of its own" seen from the
inside.

The source reading that stood in for this is below, and it is kept rather
than deleted: it is what said where to look, and it is the part that
generalises to a herdr this experiment did not run against.

What the source says, at the pin:

- The plugin popup is spawned by `spawn_popup_argv_command`
  ([`src/app/popup.rs#L67`](https://github.com/herdrdev/herdr/blob/v0.9.0/src/app/popup.rs#L67)),
  which passes `app.state.host_terminal_theme`
  ([`#L88`](https://github.com/herdrdev/herdr/blob/v0.9.0/src/app/popup.rs#L88))
  — the same field, by value, that every other pane gets.
- It reaches `TerminalRuntime::spawn_argv_command`
  ([`src/pane.rs#L2026`](https://github.com/herdrdev/herdr/blob/v0.9.0/src/pane.rs#L2026)),
  which delegates to `spawn_command_builder`
  ([`#L2247`](https://github.com/herdrdev/herdr/blob/v0.9.0/src/pane.rs#L2247)).
  So does `spawn_shell_command`
  ([`#L1988`](https://github.com/herdrdev/herdr/blob/v0.9.0/src/pane.rs#L1988)),
  which is what made the pane §3.1 was measured in.
- `spawn_command_builder` calls `apply_host_terminal_theme(host_terminal_theme)`
  ([`#L2273`](https://github.com/herdrdev/herdr/blob/v0.9.0/src/pane.rs#L2273))
  and wires the response writer that carries an OSC reply back to the child.

The popup and the measured pane therefore share the constructor, the
argument and the responder. What differs is `argv` versus a shell,
`AgentDetection::Disabled`, and overlay rendering — none of which touches the
colour path. **Treat it as an open risk anyway**: it is verified by reading,
not by seeing, and this design's whole premise rests on it. §12 says what
would close it and what it costs if it turns out false (nothing: the feature
never activates, and §6's fallback is today's screen).

One related case worth recording while it is in view, also read and not run:
`from_handoff_fd`
([`src/pane.rs#L2072`](https://github.com/herdrdev/herdr/blob/v0.9.0/src/pane.rs#L2072))
drops the response receiver (`let (response_tx, _response_rx) = …`), so a
pane restored across a herdr handoff may answer nothing. The popup is always
freshly spawned and never takes that path, so this is a note about §6's
fallback being reachable in practice, not a defect in the plan.

### 3.5 Timing

Five runs of the Bubble Tea probe, in a pane, timestamps relative to process
start:

| | first `View()` | background | last OSC 4 reply |
|---|---|---|---|
| run 1 | 0.947 ms | 18.906 ms | 19.107 ms |
| run 2 | 1.026 ms | 21.211 ms | 23.274 ms |
| run 3 | 0.895 ms | 22.017 ms | 22.062 ms |
| run 4 | 0.874 ms | 19.400 ms | 19.436 ms |
| run 5 | 0.847 ms | 18.778 ms | 18.770 ms |

Two things to read out of it. The nine answers arrive **within 4.5 ms of each
other** in the worst run, so there is no partial-answer window worth designing
for. And the ~18 ms is not herdr: `Init` ran at ~0.9 ms and the replies
arrived within 0.2 ms of the frame that flushed the queries, so the round trip
through herdr's emulator is sub-millisecond and the 18 ms is Bubble Tea's own
first flush. A slower machine moves the frame and the answer together.

## 4. Timing: query from `Init`, apply once

### 4.1 The rule

**Nothing moves ahead of the first frame.** `Bootstrap` still runs no
subprocess but `herdr workspace list` (draw-first spec §3.1), and
`theme.LoadHerdrPalette` stays where it is, a local file read, producing
exactly today's palette. The queries are `tea.Cmd`s returned from the Model's
`Init`, beside the three #293 already moved there.

The alternative considered and rejected was probing in `cmd/herdr-draft`
before `tea.NewProgram`: raw-mode the tty ourselves, write, read with a
deadline, restore. It would remove the repaint entirely, because the palette
would be known before `form.New`. It is rejected for a specific failure mode
rather than for draw-first purity: when the deadline fires we have consumed
some prefix of the terminal's reply and cannot push the rest back, so Bubble
Tea inherits a fragment like `rgb:c5c5/5555/5555\x1b\\` and parses it as
**keystrokes typed into the title field**. A one-in-a-thousand race that types
into the user's form is worse than a repaint at 20 ms.

### 4.2 The budget

**250 ms from `Init`.** An answer that lands inside it is applied; one that
lands outside it is dropped, and the popup keeps today's palette for its
lifetime. There is no retry and no second query.

Ten times §3.5's worst measurement, and chosen against the other end too:
the fastest a person reacts to the popup appearing is on the order of 300 ms,
so a repaint inside 250 ms cannot land on someone mid-keystroke. The bound
exists for the case where it is not met — a host that answers slowly, or one
that answers OSC 11 and nothing else — not for the case measured.

It is a plain timer, not `awaitCheck`: there is no call to hand it, the
answer is already in flight as a message, and it holds no submit. That is the
same shape draw-first spec §7.1 gives its two account holds ("ruling 15"),
and for the same reason, so it is a precedent rather than a new pattern.
Armed **once**, from `Init`, for the reason that convention gives: a timer
re-armed by every arrival never spends its budget.

### 4.3 The repaint

One repaint, ~20 ms after the first frame, of a form nobody has typed into
yet. It has to be judged on screen before this ships (§9.3), and the owner's
own terminal is the easy case: §5.4 shows that on alacritty at `#181818`
**every index clears its floor**, so the only visible change is the focused
row gaining a `#2b2b2b` fill. A theme where six words change colour at once
is the case to look at, and `just live` plus a `[palette]` override can stage
one without touching the owner's herdr config (see the
`live-look-at-a-theme-via-palette-override` note).

## 5. What the answers are used for

### 5.1 Measurements, not emissions

The resolution is a pure function in `internal/theme` — three steps, and the
middle one is the existing code unchanged:

1. **Seed.** Replace each unmeasurable field with the colour the host would
   have drawn for it: `PanelBG`, `Surface` and `ActiveRowBG` ← the background;
   `Text` ← the foreground; `Accent` ← ANSI 4's RGB, `Success` ← 2,
   `Warning` ← 3, `DimText`/`Overlay0`/`Branch` ← 7, `Border` ← 8,
   `Danger` ← 9.
2. **Floor.** Run `floorContrast` exactly as it stands. Every clamp,
   constant, ground list and ordering rule in v3 spec §5.3 and §5.3a applies
   untouched — including the load-bearing one, that `ActiveRowBG` is raised
   *before* it is used as a ground for the words.
3. **Restore.** Every field the floor did **not** move goes back to the
   unmeasurable value it started as. `PanelBG` and `Text` always go back, no
   matter what: they are the two "inherit the terminal's own" fields, they
   are only ever used as grounds, and emitting them as RGB would replace an
   inherited background — possibly a transparent one — with a flat colour.

Step 3 is what keeps #304's guarantee, and it is necessary rather than tidy.
Without it, a seeded `PanelBG` that no clamp touched would be emitted as
truecolor, and the form would paint over the user's terminal background on
every theme that needed no raise at all.

### 5.2 The two fills, and why #346 reverses

`ActiveRowBG` and `Surface` become RGB whenever the background is known,
even though step 3 would otherwise restore them. They are the two **fills
whose entire job is to differ from the ground they sit on**, and an
inherited fill cannot differ.

They get there by two different routes, and the design said they got there
by one. **This section is corrected from what was approved**; the
implementation found the difference and the correction is recorded rather
than quietly applied.

- **`ActiveRowBG` needs no help.** Seeded from the background it sits at
  1.00:1 against it, `floorContrast` raises it, and step 3 keeps the raised
  value. "The floor did not move it" is a state it cannot be in — except
  with no foreground, where `ensureContrast` has no `toward`, returns its
  input, and step 3 restores `NoColor`. The row then stays unfilled, which
  is right: a fill painted a flat copy of the background it sits on is not
  a fill.
- **`Surface` is the one field the general rule cannot reach.**
  `floorContrast` never writes it. The painted value is derived per call
  site by `InputFill` or `SurfaceFill` from the ground it is composited
  onto, and both derive by calling `ensureContrast` with `Text` as the
  target — which step 3 restores to `NoColor`. So at render time they can
  derive nothing and hand their input straight back, and whatever is in the
  field **is** the fill, at every call site.

So `Surface` is computed in `ResolveHost`, once, as `SurfaceFill` of the
background. That is the same expression `floorContrast` used as the third
ground a word is measured on, so the fill a picker paints its cursor row
with and the fill the words were floored against are the same value **by
construction** rather than by coincidence — which is #149's rule, and this
is the one place on this palette where it has to be asserted rather than
derived.

**What one value cannot do is serve two grounds**, and the cost lands in one
place: an input rendered on a *focused row*. Every other theme paints that
with `InputFill(ActiveRowBG)`, raised away from the band; here it comes out
as the same `Surface`, raised away from the panel by the same walk and the
same 1.25 floor that raised `ActiveRowBG` — so the two are equal and the
input does not read as a chip on the band. That is **not a regression**: on
this palette an input has had no fill at all since #304, and this leaves
that one region exactly where it was while fixing the cursor row and the
cancel button.

Raising `Surface` against `ActiveRowBG` instead was tried, and it is worth
recording why it lost rather than only that it did. It fixes the input, and
it makes the cursor fill brighter than any theme ships — which moves the
third ground every word is measured against. Measured over the 43 schemes,
that pushed solarized-light's dim tier to 2.483:1 with nowhere left to walk,
because `raiseDimText`'s ceiling is the theme's own `Text`. One region
unfilled beats one theme illegible.

What all of this buys is #346 in reverse, on this palette only. The focused
row regains the first of v3 spec §5.4's three signals, and `SurfaceFill`
regains the picker's cursor band and the cancel button's face — two of the
regions #149 found shipping invisible on twelve builtins. The golden frames
`terminal-theme-fallback` and `terminal-theme-resolved` differ on exactly
those two lines and nowhere else.

#276's reasoning is not wrong and is not deleted; it becomes the
**fallback's** reasoning, which is where it belongs: with no fill, every
word on the focused row sits on the terminal's own background, and that is
still the right screen when nobody answered.

### 5.3 RGB versus indices

**An index that clears its floor stays an index.** That is #304's guarantee
and it costs nothing to keep: the colour is legible on this host by
measurement, and leaving it as an index means it keeps following the user's
terminal — including through the theme change §8 declines to track.

**An index that does not clear its floor is replaced by the RGB the clamp
produced.** The argument is short: the measurement says the user cannot read
that word on their own background, so "it follows your terminal" is a
property of an illegible word. There is no third option — an index cannot be
raised, only replaced.

Two consequences worth stating plainly, because they are the cost.

- The replacement is **not** the terminal's colour any more. `raiseSemanticText`
  walks toward black or white (`farthestEnd`), so ANSI 7 on a light host
  becomes a near-black grey, up to 195 sRGB units from where it started
  (§5.4). That is a large change, and it is a large change applied to a
  colour that measured under 3:1 — the "label tier under 3:1 on 11 of 43, all
  of them light schemes" #347 opens with.
- **The band creates some of the work it then does.** Against the background
  alone, `Danger` (ANSI 9) is under 3:1 on 4 of 43 schemes. Against the three
  grounds — which include the focused-row fill that only exists because §5.2
  brought it back — it is under 3:1 on **18 of 43**. Restoring the fill is
  what puts fourteen more schemes in the clamp's way. This is not an argument
  against the fill; it is the honest accounting, and it is why §5.4 measures
  both columns.

### 5.4 What returns, measured

Run over the 43 schemes with the real `floorContrast`:

| field | under floor on the background alone | under floor on the three grounds |
|---|---|---|
| `Accent` (ANSI 4) | 6 of 43 | 16 of 43 |
| `Success` (ANSI 2) | 8 of 43 | 14 of 43 |
| `Warning` (ANSI 3) | 13 of 43 | 15 of 43 |
| `DimText` (ANSI 7) | 11 of 43 | 12 of 43 |
| `Danger` (ANSI 9) | 4 of 43 | 18 of 43 |

The first column reproduces #347's own figures for ANSI 4, 7 and 9 exactly
(6, 11, 4), which is the cross-check that the simulation is measuring what
the issue measured.

Outcomes:

- **The focused-row fill lands between 1.254:1 and 1.431:1** on all 43,
  median 1.331. Floor 1.25. It never glares, the same way v3 spec §5.3
  reported for the builtins.
- **13 of 43 schemes keep every index untouched** and gain only the band.
- **No clamp gives up.** Every raised field clears its floor on all three
  grounds; `bestAlong`'s and `raiseDimText`'s give-up paths are never
  reached. (They stay, for the override case they were written for.)
- Largest walks are on light schemes' ANSI 7: 195, 194, 192, 189 sRGB units.
  Smallest are 15–20.

On the owner's own terminal — alacritty, `#181818` on `#d8d8d8` — the result
is the quiet end:

```
band #2b2b2b at 1.254:1, surface fill #2b2b2b
accent 4  #6a9fb5 worst 4.88:1 -> kept as the index
danger 9  #c55555 worst 3.24:1 -> kept as the index
warning 3 #f4bf75 worst 8.47:1 -> kept as the index
success 2 #90a959 worst 5.40:1 -> kept as the index
dim 7     #d8d8d8 worst 9.93:1 -> kept as the index
branch 7  #d8d8d8 worst 9.93:1 -> kept as the index
```

Every word stays the terminal's own; the row gets its band. That is the
screen §9.3 will be looking at, and it is worth knowing in advance that it is
the mild one.

### 5.5 What is left alone

- **`Overlay0`** keeps ANSI 7, unfloored. `floorContrast` floors it on no
  theme — v3 spec §5.3a asserts 1.6:1 against the *table* instead, which a
  host palette has no equivalent of. Decision 4 asks whether to accept the
  resulting divergence (labels walk dark on a light host, rules stay light).
- **`Border`** keeps ANSI 8. It is the deliberately near-invisible fill, has
  no floor on any theme, and gaining one here would make the scrollbar track
  louder than every other palette draws it.
- **Indices 0, 5, 6, 10–15** are never queried. The palette uses 2, 3, 4, 7,
  8 and 9 and nothing else, so the query is eight sequences: OSC 11, OSC 10
  and six OSC 4s.
- **Every other builtin.** The query runs only when the loaded palette's
  `PanelBG` is unmeasurable, which by construction is the `terminal` entry
  alone: `parseHexColor` accepts hex and nothing else, so no override and no
  custom herdr theme can produce an index. Seventeen palettes are untouched
  and no golden frame outside the terminal fixtures can move.

## 6. No answer

The fallback is today's behaviour, and today's behaviour is already correct.
The reachable cases:

| case | what happens |
|---|---|
| headless `create` | never queries: no `tea.Program`, no draw, no tty to ask. `plan.Input` is unchanged, so `equivalence_test.go` is untouched |
| a herdr older than the OSC responder | the queries go out, nothing comes back, the budget expires |
| herdr's own host probe never succeeded | OSC 10/11 `?`-return with no reply (§3.2); the background is missing, so **nothing is applied at all** |
| the popup opened before herdr learned the host theme | same as above; `theme_sync` would push the theme to the pane later, but the budget is spent |
| a pane restored across a herdr handoff | §3.4's dropped receiver; no reply |
| not herdr at all — `just live`, a plain terminal, a pty with nobody on the far end | no reply, budget expires, 250 ms later than otherwise |
| the terminal answers OSC 11 but not OSC 4 | the background is known, so the fills and the floors apply; each missing index stays an index, unmeasured and unfloored, exactly as today. **Inside herdr this does not happen as written — see §6.1** |

**The background is the one required answer.** Without it there is no ground,
so there is nothing to measure against and nothing honest to raise anything
to — the same sentence `ensureContrast`'s doc comment already makes about an
unmeasurable `bg`. A missing *foreground* is not fatal and needs no special
case: `ensureContrast` and `raiseDimText` both return their input untouched
when `toward`/`text` is unmeasurable, so the band and the dim tier stay as
they are while the semantic clamps — which walk toward black or white and
need only the grounds — still apply. That falls out of the existing code
rather than being arranged.

### 6.1 Inside herdr, OSC 4 is never unanswered — it answers ghostty's

**Corrected after implementation**, from the independent review of #348 (its
S1), and it is the one place where reading herdr's source changed the design
rather than confirming it.

A herdr pane **always** answers OSC 4. `palette_color_query_response` replies
from the pane's own palette, and `apply_host_terminal_theme`
([`terminal.rs#L1177`](https://github.com/herdrdev/herdr/blob/v0.9.0/src/pane/terminal.rs#L1177))
builds that palette by starting from `ghostty::default_palette()` and
overwriting only the entries the host actually supplied. On **WSL** the host
palette is never probed at all — `should_query_host_terminal_palette()` is
`!running_inside_wsl()`
([`linux.rs#L45`](https://github.com/herdrdev/herdr/blob/v0.9.0/src/platform/linux.rs#L45))
— so OSC 10 and 11 carry the host's real foreground and background while
OSC 4 carries ghostty's Tomorrow Night values. The same holds anywhere the
host answered herdr's 256-entry probe only partially.

The table row above says such an index "stays an index, exactly as today".
It would not have: the code would have seen a complete set and floored the
palette against colours the screen never draws — and, worse, **replaced** a
word on the strength of measuring ghostty's stand-in. That is a regression
against today's behaviour on those hosts, not a missed improvement.

So an OSC 4 answer equal to libghostty's default for its index is dropped,
and that index falls back to staying an index — which is what the row
promised, now actually reachable. §3.2's own argument is what makes this
sound: ghostty's defaults are exactly what a failed probe leaves behind, so
recognising them is recognising the failure.

The cost is a false positive — a user whose scheme genuinely uses one of
those sixteen values loses the floor on that entry. Measured over the 43
schemes, that is **zero of 43** on any of the six indices this palette
draws with, and a false positive errs toward today's screen. The
maintenance hazard is the other way: if ghostty changes its defaults the
table stops matching and the WSL case returns silently. The test that
states the values makes a deliberate change visible; it cannot detect
ghostty drifting, and does not claim to.

**The obvious alternative was detecting WSL ourselves**, the same way herdr
does (`/proc/sys/kernel/osrelease`, `/proc/version`, the WSL env vars,
`/run/WSL`), and skipping OSC 4 there. It vendors nobody's constants and
has no drift hazard, and it was rejected on coverage. WSL is not the only
way to reach this: a host terminal that does not implement OSC 4 **at all**
leaves every entry at ghostty's default on any platform, and that is the
more likely case of the two — plenty of terminals answer OSC 10 and 11 and
not OSC 4. Recognising the stand-in catches both, because it tests the
thing that actually went wrong rather than inferring it from the operating
system. It also keeps `internal/theme` pure, where a `/proc` read would
not. (The owner delegated this choice on 2026-09-23; this paragraph is the
reasoning they delegated.)

## 7. Where the code goes

### 7.1 `internal/theme` — pure, as everything here is

One exported function and one exported predicate:

```go
// HostColors is what the terminal answered. Every field is optional;
// Background missing means there is nothing to resolve.
type HostColors struct {
    Background Color
    Foreground Color
    Palette    map[uint8]Color
}

// ResolveHost applies §5.1's seed/floor/restore to p. It performs no I/O,
// and returns p unchanged when p needs nothing (NeedsHostColors) or when
// host supplies no background.
func ResolveHost(p Palette, host HostColors) Palette

// NeedsHostColors reports whether p has an unmeasurable PanelBG -- the
// terminal palette, and nothing else this package can produce.
func NeedsHostColors(p Palette) bool
```

`Builtin`, `Default`, `Resolve` and `LoadHerdrPalette` are **not touched**,
which is what keeps the four existing theme guards green and meaningful
(§9.1). `floorContrast` is called, not modified.

The OSC 4 reply parser lives here too, as a pure function on a string:
strip `\x1b]4;`, split the index off at the first `;`, strip either
terminator (`\x07` or `\x1b\\`), and hand the remainder to
`ansi.XParseColor`, which already understands `rgb:rrrr/gggg/bbbb`
(`x/ansi@v0.11.8/util.go:58`). The result is normalised to an explicit
8-bit `color.RGBA` before it enters a `Palette`, because `rgb8` requires
each byte replicated and only `color.RGBA` guarantees it — `XParseColor`'s
`#`-prefixed branch returns a `colorful.Color`, which does not.

### 7.2 `internal/app`

- `Init` gains `tea.RequestBackgroundColor`, `tea.RequestForegroundColor`,
  six `tea.Raw("\x1b]4;n;?\x1b\\")` and the 250 ms timer — but **only when
  `theme.NeedsHostColors(m.palette)`**, so on seventeen palettes `Init` is
  byte-identical to today's.
- Three message handlers accumulate into a `HostColors`:
  `tea.BackgroundColorMsg`, `tea.ForegroundColorMsg`, and
  `uv.UnknownOscEvent` (parsed by §7.1's function; anything that is not an
  OSC 4 reply for an index we asked about is ignored).
- When the last expected answer lands, or the timer fires with a background
  in hand, `ResolveHost` runs once and the result is pushed into the form.
  A version counter retires the timer if the answers won, the way
  `reqVersions` already retires a superseded check.
- It goes on `context.Background()`, not the `Lifetime`: there is no
  subprocess and no child to kill, which is the distinction that bullet
  draws.

### 7.3 `internal/form` — `SetPalette`

The form is a dumb view and stays one. `form.Model` gains
`SetPalette(theme.Palette)`, which stores the palette and forwards to each
section that implements a new optional interface:

```go
// paletteSetter is the fifth optional interface beside titleValuer,
// completer, newliner and footerHinter -- checked by type assertion, and
// deliberately not on Section.
type paletteSetter interface{ SetPalette(theme.Palette) }
```

This is smaller than it looks, and that is the point of having checked. The
fifteen constructors that take a palette almost all **store** it and build
styles at render time from the stored value; `renderStackRow`, `dividerLine`,
`dimText`, `createButton` and the rest already take `p theme.Palette` as a
parameter. Only **two** places bake a palette-derived value into a field and
need to re-bake:

- `lineinput.go:86` — `ti.SetStyles(lineInputStyles(palette))` and
  `fill: palette.InputFill(ground)`.
- `widgets/textarea.go` — `paletteStyles(palette)`, same shape one level up.

So each `SetPalette` is one or two lines, and the two input widgets get a
third. No state is copied, nothing is reconstructed, and no value the user
has typed is at risk — which is the whole reason this is preferred to
re-running `New(Setup{…})` the way `handleClearRequested` does. That path
exists and would have been the obvious reuse; it is rejected because it
discards the form, and a palette landing must not.

## 8. Drift

Out of scope, per decision 6 and #347's own "this probably doesn't need to".
A host theme changed while the popup is open is not followed: there is no
second query, no subscription, and no re-resolve. herdr tracks it and pushes
the new theme to its panes (`apply_host_terminal_theme_to_panes`,
[`src/app/theme_sync.rs#L53`](https://github.com/herdrdev/herdr/blob/v0.9.0/src/app/theme_sync.rs#L53)),
so the *indices* this palette still emits follow it for free. Only the
clamped fields and the two fills freeze, and only for the seconds the popup
is open.

## 9. Testing

Every package that touches I/O here is tested against a small interface and a
fake, as everything in this repository is. The good news is that almost none
of this touches I/O.

### 9.1 The five existing guards stay green, and gain a meaning

None of them needs replacing, because `Builtin("terminal")` never changes:

| guard | why it still passes |
|---|---|
| `TestTerminalPalette_EmitsTheTerminalsOwnColours` | reads `builtinPalettes["terminal"]`, which is untouched |
| `TestBuiltinPalettes_OnlyTerminalEmitsIndexedColours` | same table |
| `TestClamps_HandAnIndexBackAsAnIndex` | the clamps are unmodified; `ResolveHost` hands them RGB, never an index |
| `TestLoadHerdrPalette_HexOverridesOnTheTerminalPalette` | `LoadHerdrPalette` is unmodified |
| `TestTerminalTheme_TheScreenSendsOnlyTheTerminalsOwnColours` (form) | builds its screen from `theme.Builtin("terminal")`, i.e. the fallback |

Each of them is now a statement about **the fallback screen**, which is
better than what it was a statement about before, and §13 records that
change of meaning so the next reader does not mistake one for a claim about
the resolved palette.

### 9.2 New unit tests

**Corrected from what was approved.** Every guard below was
mutation-checked once — break the thing it names, watch it go red, restore
from a byte-identical backup. Five of the first thirteen mutations
*survived*, and each survivor was a real finding rather than a
mis-aimed mutation:

- Two `hostDone` checks, one in each collector and one in
  `finishHostColors`, **alibied each other**: breaking either left every
  test green. Collapsed to the single check in `finishHostColors`, which is
  the only place a decision is made, and the mutation is caught now.
- `ResolveHost`'s `out.PanelBG = p.PanelBG` is **unreachable belt and
  braces** — the restore loop already covers it, because `floorContrast`
  never writes that field. The line stays, and
  `TestFloorContrast_DoesNotMoveTheInheritedFields` now pins the premise it
  rests on, so a future floor that did write `PanelBG` goes red here
  instead of silently emitting the user's background.
- The `NeedsHostColors` gate was masked by the `len(indices) == 0` check
  beside it, because catppuccin has no ANSI index either way. The
  discriminating fixture is a terminal palette with a `[palette] panel_bg`
  override: indices *and* a known background.
- Which ground `Surface` is derived from had no guard at all;
  `TestResolveHost_SurfaceIsThePanelFill` is it.
- And one mutation exposed a wrong assertion rather than a weak guard:
  "a background alone is enough to resolve" is false. The words are
  floored (they need only grounds) and the band is not (it needs the
  foreground as its walk target), and the test asserted the opposite while
  passing, because `lipgloss.NoColor` is neither nil nor an index and the
  check was on the field's shape rather than on the screen.

- **`ResolveHost`, table-driven.** Seed/floor/restore on hand-written
  `HostColors`: a palette that needs nothing comes back byte-identical
  including its indices; one that needs a raise comes back with RGB in
  exactly the fields that moved and indices in the rest; a missing background
  returns `p` unchanged; a missing foreground leaves the band and `DimText`
  alone and still floors the semantics; a non-terminal palette is returned
  untouched.
- **The floor actually holds**, over the 43 schemes, as a real test rather
  than the throwaway that produced §5.4: for each scheme, assert the band
  clears `ActiveRowContrastFloor` and every raised word clears its floor on
  all three grounds. The scheme files are external, so the test ships a small
  committed fixture of the measured values rather than reading that
  directory — a test that skips when a path on one laptop is missing is not a
  test.
- **The reply parser**, on real bytes: both terminators; a reply for an index
  we did not ask for; `rgb:` with two-digit channels; a truncated reply; an
  OSC 4 *set* rather than a reply; an empty string. Each returns "not a
  reply" rather than a wrong colour.
- **The library seam, pinned against an upgrade.** A zero-value
  `uv.EventDecoder` decodes offline, verified:
  `d.Decode([]byte("\x1b]4;9;rgb:c5c5/5555/5555\x1b\\"))` returns
  `uv.UnknownOscEvent` carrying those bytes, and the OSC 11 form returns
  `uv.BackgroundColorEvent{#181818}`. A test that asserts both is what makes
  this feature **able to fail**: the day a bubbletea or ultraviolet bump
  starts typing OSC 4 into an event of its own, our `UnknownOscEvent` branch
  would silently stop matching and the feature would quietly die. This test
  goes red instead.
- **`Init` asks for what it needs, and only when it needs it.** Run the
  `Init` batch and assert the `RawMsg` payloads are the six OSC 4 queries on
  the terminal palette, and that they are absent on catppuccin.
- **`SetPalette` reaches everything.** A test that constructs every section,
  calls `SetPalette` with a palette whose every field is a distinct sentinel,
  renders, and asserts no byte of the old palette survives — including inside
  the two input widgets, which are the ones that bake.

### 9.3 Golden frames and the screen

- A new terminal-theme frame **with** host colours resolved, beside the
  existing one without. Two fixtures, one difference, which is the shape that
  makes a frame readable.
- Regenerate per package (`go test ./internal/form/ -update`, then
  `./internal/app/ -update`), never `./... -update`. No `showcase-*` frame is
  expected to move, since the showcase is not on the terminal theme — if one
  does, `just screenshots` as well.
- **And then look at it**, in a real herdr pane under `name = "terminal"`,
  with the bytes shown. A frame records bytes, not perceptibility, and this
  change is about perceptibility twice over: the band has to be visible and
  the repaint has to be invisible. §4.3 says which theme to stage.

## 10. Cost

Proportionate, and worth naming the parts that are not free.

- **One new dependency edge.** `github.com/charmbracelet/ultraviolet` is
  `// indirect` today and becomes direct, because `uv.UnknownOscEvent` has no
  bubbletea alias — `translateInputEvent` re-types twenty-one event kinds and
  falls through for this one. That is a module whose version is a bare
  pseudo-version and whose API has no compatibility promise. §9.2's decoder
  test is the mitigation, and it is a real one.
- **Roughly 15 small methods** in `internal/form`, two of which do work.
- **One pure function** and one parser in `internal/theme`, plus their tests.
- **No change to `plan`, `create`, `defaults`, `config` or `herdrc`**, and no
  new flag — so nothing to keep in step in `equivalence_test.go` and nothing
  new in the skill document.

If decision 1 goes the other way, the closing argument for #347 is §3 itself:
the premise is true, the cost is the dependency edge and the repaint, and the
benefit is bounded by §5.4's table — 13 of 43 schemes gain only a band.

## 11. Out of scope

- **Drift** (§8).
- **Querying the other 250 palette entries.** Nothing draws them.
- **A `[theme] host_colors = false` escape hatch.** A user who dislikes the
  result already has `[palette]`, which wins over everything and is where the
  existing answer to "I cannot read this" lives.
- **Flooring `Overlay0` for every theme** (decision 4). If the owner wants
  the divergence closed, it is its own issue, because it moves a value
  seventeen other palettes measure against.
- **Giving the focused-row input chip a fill of its own**, which §5.2 says
  this palette does without. The review (N3) is right that §5.2's
  measurement rules out a band-derived *cursor fill* — the third ground a
  word is measured on — and says nothing against a separate value for the
  input, which `InputFill` alone would use and which is a floor ground on
  no theme. The obstacle is only that `Text` goes back to `NoColor`, so the
  render-time derivation has nothing to walk toward; a non-emitted
  derivation target would remove it. That is a follow-up, not a defect
  here.
- **Anything about herdr's own rendering.** herdr already picks its selection
  colour from the host background (`automatic_selection_bg`); this design
  does not ask herdr for anything it does not already offer.

## 12. Risks, and what would settle them

| risk | standing | what would settle it |
|---|---|---|
| ~~the **popup** pane does not answer~~ | **closed 2026-09-23** (§3.4): the experiment described here was run, and every sentinel came back | — |
| the repaint is visible and annoying | unmeasured; §3.5 says ~20 ms | §9.3, on screen, on a theme where several words move at once |
| a `uv` bump retypes OSC 4 | unguarded today | §9.2's decoder test |
| `raiseSemanticText` walks a word 195 units and it looks wrong | measured, not judged | §9.3, on a light host |

## 13. Amended text, itemised

Sentences that stop being true when this ships. Everything else in the
documents named stays authoritative.

1. **v3 spec §5.3a**, "`terminal` is the only exemption, and it is exempt for
   a stated reason rather than a waiver." Still true when the host does not
   answer. Becomes: `terminal` is exempt **until the host answers**, and the
   stated reason becomes the fallback's reason.
2. **v3 spec §5.4**, "The focused row carries **three** signals." True again
   on the terminal palette whenever the background is known; #346 made it two
   there, and this restores the fill as the first of the three.
3. **The `terminal` entry's comment in `internal/theme/palette.go`**, "None
   of this is measurable, and nothing here pretends otherwise" and
   "ActiveRowBG is selection_bg's Color::Reset … and so the focused row has
   NO fill on this theme (#276)". Both become statements about the
   unresolved table and the fallback, and the comment has to say so — the
   table itself does not change.
4. **`resolveBuiltinFromConfig`'s comment**, "there is nothing left to know —
   the terminal supplies every colour". There was something left to know, and
   §3 is it. The sentence's conclusion (route `name = "terminal"` to the
   terminal palette) is unchanged and correct.
5. **`widgets.PromptArea`'s doc comment**, "no SetPalette setter exists on
   any of the three widgets", and its conclusion that a form wanting to
   react to a palette change "would need to reconstruct the widget". All
   three have one now, and reconstruction is what they exist to avoid: a
   `PromptArea` holds the prompt the user has typed.
6. **v1 spec §16 item 8**, "Reading herdr theme changes live (v1 reads at
   startup only)", is NOT amended, and the distinction is the one a future
   reader is most likely to lose. Nothing here watches herdr's config, and
   a user who switches herdr themes while the popup is open still sees the
   palette it opened with — decision 6 keeps that out of scope. What
   changed is that the palette read *at startup* can now finish arriving a
   few milliseconds after the first frame. `paletteSetter`'s own doc says
   so, next to the code that would otherwise look like the thing §16 item 8
   rules out.
7. **v1 spec §7's** "`name = \"terminal\"` [is] unknowable from config
   alone: resolve to the configured dark variant and document the
   limitation." Already superseded text (v2 §7 replaced §7, and #345 retired
   the behaviour). Recorded here only because this design is what makes both
   halves of it false, and a reader coming from #304 will look for it.
