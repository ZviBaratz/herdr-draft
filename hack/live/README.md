# `hack/live` — the form, under a pty, on demand

`docs/manual-smoke.md`'s **Route B** runs the real form in an ordinary pane.
This automates as much of it as can be automated: the same binary, launched
the way herdr launches it, with every input it reads replaced by a stub under
a scratch `HOME`.

```bash
just live --size 101x30                                    # the popup's own size
just live --size 57x18 --keys 'tab tab tab' --row -1       # one row's footer, narrow
just live --size 44x12 --click 4,6                         # reach a row Tab skips
```

`just live` builds the binary, makes a venv with the pinned `pyte` (the pin is
the justfile's `pyte_version`, and lives nowhere else), and runs `drive.py`.
Nothing is installed into the repository or onto your machine; the venv sits
under `$TMPDIR`. `hack/` is not a Go package and nothing here is on the
`[[build]]` path that runs on a user's machine at install time.

It talks to **no real herdr**, spends no account quota and creates nothing, so
it is safe to run in a loop while you read a screen. What it cannot do is the
other half of the smoke matrix: the popup (which has no pane id, so nothing
can send keys to it) and a real submit. Those still need `docs/manual-smoke.md`.

## The stub `herdr`'s envelope

This is the part that costs an afternoon to rediscover, and the reason the
driver is committed rather than rebuilt.

`internal/herdrc`'s `runJSON` unmarshals **`{"result": ...}`** — not `data`,
not a bare value — and `Bootstrap`'s first call is `workspace list`, whose
result decodes into `{"workspaces": [...]}`. The whole stub is four lines:

```sh
#!/bin/sh
case "$1 $2" in
  "workspace list") echo '{"result":{"workspaces":[]}}' ;;
  *)                echo '{"result":{}}' ;;
esac
```

Get it wrong and the plugin refuses to open at all, with an error that reads
like the binary was not found rather than like the payload was the wrong
shape. Measured 2026-09-20, with `{"data":{...}}` in place of `{"result":...}`:

```
herdr-draft: herdr unreachable: herdr workspace list: parse response: unexpected end of JSON input
```

`drive.py` carries that stub inline and says so when the binary exits before
anything is sent. To answer more subcommands — or to reproduce the failure
above — pass your own with `--herdr FILE`.

## The stub `clauth`, and why `HOME` has to be a scratch directory

`internal/clauth`'s `Load` prefers a **fresh** `~/.clauth/status.json` and only
falls back to running the CLI when there is not one. Inherit a real `HOME` and
the form reads your real account state, the stub is never called, and nothing
you see about the account row is reproducible. The driver therefore points
`HOME`, `XDG_CONFIG_HOME` and `XDG_STATE_HOME` at its own tree and drops every
`HERDR_*` variable it inherited — it is usually run from inside a herdr pane,
and an inherited `HERDR_SOCKET_PATH` would reach the real server behind the
stub.

The stub answers `status --json` with two profiles, which is what puts
`active · Max 20x · 5h 12%` on the account row.

## Why `pyte` and not a regex

A Bubble Tea program repaints **partially**. Stripping ANSI out of the raw
stream and grepping the result produces confident nonsense: lines that were
overwritten half a frame ago, cursor jumps read as text order. `pyte` is a
terminal emulator, so what it holds is what a person would see. That is the
same reason `docs/manual-smoke.md` warns that a `pane read` is one keystroke
stale.

## Four things that will mislead you anyway

**A keystroke count is not a row count.** Tab skips inert rows, and which rows
are inert depends on the form's own state — with the worktree on, the
placement row is inert, so four tabs from the opening screen reach the *agent*
row, not the placement row. Read the focus bar (`▌`) rather than counting.

**The last screen row is not always the footer.** The assembled form pads
vertically, so at some heights the bottom row is blank — `assembled-opening-101x30`
is blank on its last line too, and the live screen matches it. `--row -1` is
the footer at 12 rows and a blank line at 30. Look before you rely on it.

**Settle time is a real parameter.** `--settle` (after each action, default
0.25s) and `--open-settle` (after launch, default 1.5s) are how long the driver
waits for a repaint. A debounced check — the project row's, the base settle —
takes longer than either; give it seconds, not milliseconds.

**State carries over unless you let it go.** The plugin's state directory holds
`recents.json`, `last-used.json` and `projects.json`, so a second run would
otherwise open on what the first left behind. The driver wipes it on every run;
`--keep-state` is how you test memory on purpose, and `--fresh` throws the whole
tree away (and refuses to do that to a directory it did not make).

## Worked example: reading a footer cliff

`internal/form`'s `narrowFloorCases` records six terminal widths where the key
ladder falls to its constant tail. Two of them, read live on 2026-09-20 against
this branch's binary:

```console
$ just live --size 35x12 --size 36x12 --keys 'tab*3' --row -1
=== 35x12
 ⇥ move   ⌃S create    esc cancel
=== 36x12
 ↑↓ part   ⌃S create    esc cancel

$ just live --size 40x12 --size 41x12 --keys 'tab*4' --row -1
=== 40x12
 ⇥ move        ⌃S create    esc cancel
=== 41x12
 ↑↓ all kinds   ⌃S create    esc cancel
```

Which is what the golden frames say, arrived at through a real terminal
instead of `ViewAt` — and that agreement is the point. A golden-frame suite
proves only the states someone thought to fixture.
