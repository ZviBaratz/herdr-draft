# `hack/live` — the form, under a pty, on demand

`docs/manual-smoke.md`'s **Route B** runs the real form in an ordinary pane.
This automates as much of it as can be automated: the same binary, launched
the way herdr launches it, with every input it reads replaced by a stub under
a scratch `HOME`.

```bash
just live --size 101x30                                 # the popup's own size
just live --size 57x18 --keys tab,tab,tab --row -1      # one row's footer, narrow
just live --size 44x12 --click 4,4                      # reach a row Tab skips
```

**Commas, not spaces, in `--keys` through `just`.** The recipe's `{{ARGS}}` is a
space-joined string the shell re-splits, so a quoted `--keys 'tab tab tab'`
arrives as three arguments and argparse refuses the last two. `drive.py` splits
key tokens on either separator, so commas work through both callers; run it
directly (`python hack/live/drive.py …`) when a flag's value has to contain a
space. The recipe also sets `set -f`, because without it `tab*3` is globbed
into a filename that happens to match.

A row number in an example is only as good as the rows on screen — the issue
row is absent here (see below), which moves everything above `placement` up by
one. Dump the screen before clicking.

`just live` builds the binary, makes a venv with the pinned `pyte` (the pin is
the justfile's `pyte_version`, and lives nowhere else), and runs `drive.py`.
Nothing is installed into the repository or onto your machine; the venv sits
under `$TMPDIR`. The `python3` it is made from must be 3.11 or later, for
`tomllib` (see the refusals below). `hack/` is not a Go package and nothing here is on the
`[[build]]` path that runs on a user's machine at install time.

It reaches **no real herdr, no real Linear and no real clauth**, and creates
nothing outside its own scratch tree, so it is safe to run in a loop while you
read a screen. What it cannot do is the other half of the smoke matrix: the
popup (which has no pane id, so nothing can send keys to it) and a submit that
creates anything real. Those still need `docs/manual-smoke.md`.

**The child's environment is an allow-list**, which is the only reason any of
that is true. It inherits `LANG`/`LC_*`, `TZ` and `TMPDIR` and nothing else;
everything the form reads comes from the scratch tree. The first version of
this driver subtracted `HERDR_*` and the two `XDG_*` names and called that
"nothing of yours" — and still handed over the Linear API key, which
`internal/linear` reads straight from the environment, so the issue row was
live against a real workspace and 163KB of it landed in the scratch state
directory on every run. A screen that depends on whose key is in the shell is
not evidence, and a screen dump that carries real issue titles is worse than
useless in a PR.

## The stub Linear, and why it needs its own binary

Linear is the one input a scratch `HOME` cannot stub: the endpoint is compiled
in. So `just live` does not drive `bin/herdr-draft`. It links **its own copy**
with `-X` pointing `internal/linear`'s `defaultEndpoint` at a loopback port
(#302), and `stub_linear.py` serves `linear-issues.json` there. The issue row
then renders five obviously fake `LIV-` issues — a filterable picker, three
columns, an elided title and a count line — from a file committed next to this
one, not from anyone's workspace.

Three things follow, and all three are on purpose:

- **The driven binary is not `just build`'s.** It differs by that one string,
  and it says so: `go version -m` on it prints the `-ldflags` it was linked
  with. Each run links its own copy into a fresh directory under
  `$TMPDIR/herdr-draft-live-bin/` and deletes it on the way out — one shared
  path let a second `just live` relink the first run's binary between its link
  and its exec, and the first form then dialled the second run's port. An installed plugin is built by
  `herdr-plugin.toml`'s `[[build]]`, which sets no `-X`; `internal/linear`'s
  `TestNoInstalledBinaryCanBeRedirected` reads the real manifest to keep it so.
- **It lives under `$TMPDIR`, never in `bin/`.** `just smoke` runs
  `bin/herdr-draft` against the REAL Linear and must never pick this one up —
  and this one sends whatever Linear key it holds to a loopback port. That is
  right for the stub's fake key and wrong for anyone's real one. **Do not run it
  by hand.**
- **The only key it ever holds is the stub's, and that takes two refusals as
  well as the allow-list.** `internal/linear`'s `ResolveAPIKey` prefers a
  `[linear] api_key_cmd` to the environment, and the environment to an inline
  `api_key`. The allow-list means nothing is inherited and the driver sets
  `LINEAR_API_KEY` to the stub's own obviously fake key, which beats any inline
  key — but a `--config` carrying `api_key_cmd` would beat the stub's, and
  handed a real `config.toml` that is a real key sent to a loopback port. So
  with the stub on, the driver **refuses** a config that sets `api_key_cmd`,
  before anything is written. It also refuses `--linear-port` without an
  explicit `--binary`, because the default `bin/herdr-draft` is not linked to
  the stub and would send the stub's key to the real Linear.
- **"Sets `api_key_cmd`" means what `internal/config` reads, so the config is
  parsed, not grepped** (#314). The first version matched lines starting
  `api_key_cmd =`, and let through the three other spellings TOML has for the
  same key: an inline table (`linear = { api_key_cmd = … }`), a dotted key
  (`linear.api_key_cmd = …`) and a quoted one (`"api_key_cmd" = …`). Each
  delivered its command's output to the loopback port. Two properties of
  BurntSushi/toml shape the check that replaced it. It matches a key to a field
  with `strings.EqualFold` when no exact match exists, so `[LINEAR]
  API_KEY_CMD` counts, and the check compares with `casefold()`. And it reads
  TOML 1.1, which `tomllib` does not. A newline inside an inline table is valid
  to the binary and an error to the driver. So a config `tomllib` cannot parse
  is **refused** too, since the driver cannot tell what it sets. A comment
  or a string that merely mentions `api_key_cmd` is not refused, and neither
  is a `[linear]` that is not a table, which the binary refuses to load.
  `just live-selftest` holds every one of those cases.
- **The config is read once, and the bytes checked are the bytes installed.**
  A run can wait on another run's lock between the check and building the
  tree, for as long as that run lasts. Reading the path a second time would
  install whatever the file said by then. A file that is not UTF-8 is refused
  like any other the driver cannot parse, and `--linear-port` accepts
  1–65535 only: a `0` used to read as "no stub" and skip both refusals.
- **The `api_key_cmd` refusal holds without the stub too.** Without the stub
  such a config hands the default binary a real key for the real Linear, or
  hands a stub-linked binary run by hand a real key for a loopback port. An
  inline `api_key` is refused on a stub-off run for the same reason (#318),
  since only the stub's `LINEAR_API_KEY` outranks it. The `--binary`
  requirement is the stub's alone.
- **The stub refuses what should never reach it anyway.** Any key but its own,
  and any query but the one `internal/linear` sends, gets a GraphQL error the
  form shows as the row's reason. With the refusals above, no route through the
  driver can deliver another key — this is the second line, not the first —
  and the key itself is never printed: the stub logs nothing, and neither its
  refusal nor `internal/linear`'s errors carry the `Authorization` header.

Run `drive.py` directly without `--linear-port` and there is no stub and no
key, so the issue row is absent — the honest state for a run with no Linear.
A `--config` carrying an inline `api_key` is refused on such a run (#318):
with no `LINEAR_API_KEY` set, nothing outranks it, and the default binary
would use it against the real Linear. Through `just live` the same config is
fine, because the stub's key wins.

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

**The empty array is why the title row's panel is blank**, and it is the first
thing that looks broken and is not. That panel lists the sessions open when the
form opened, fed by the *same* `workspace list` call. Hand `--herdr` a stub
that returns two workspaces and it fills in exactly as the committed fixture
`internal/app/testdata/frames/assembled-opening-101x30.txt` has it:

```sh
#!/bin/sh
case "$1 $2" in
  "workspace list") cat <<'JSON'
{"result":{"workspaces":[
  {"workspace_id":"ws-1","number":1,"label":"report-studio","focused":false,
   "pane_count":4,"tab_count":2,"active_tab_id":"t1","agent_status":"idle","worktree":null},
  {"workspace_id":"ws-2","number":2,"label":"qspace-tls","focused":false,
   "pane_count":1,"tab_count":1,"active_tab_id":"t2","agent_status":"blocked","worktree":null}
]}}
JSON
  ;;
  *) echo '{"result":{}}' ;;
esac
```

```console
$ just live --herdr /tmp/rich-herdr.sh --size 101x30
15    sessions open when this form did                     2 sessions
16    report-studio  idle     4 panes
17    qspace-tls     blocked  1 pane
```

`WorkspaceInfo` in `internal/herdrc/runner.go` is the field list.

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

## Every reading checks itself

pyte 0.8.2 has **no SU or SD** (`CSI Ps S`, `CSI Ps T`): its CSI table maps
neither, so both fall to a silent no-op. Bubble Tea's renderer uses SU to take
lines out of the middle of a screen — it scrolls the region and repaints only
what differs from the scrolled result — so under pyte the scroll never
happened and the lines that should have gone stayed. The first list here to
shrink mid-screen was #302's issue picker: filtering five issues down to two
left the old frame's last two rows on screen, and dropped `est 2` from a row
that belonged there. This driver printed all of it as if the form had drawn
it. The form was right. `drive.py`'s `emulator` adds both scrolls, written
straight onto pyte's buffer, because pyte's own `delete_lines` has a second
bug on the same path (a never-written blank line moving up leaves the old row
in place) — and routes DL through the same code for that reason. IL goes
through it too, for symmetry rather than repair: pyte's `insert_lines` pops
every row it passes, bottom up, so it has no such bug (#312), and
`just live-selftest` checks that on plain pyte. No walk has emitted either yet; Bubble Tea's renderer uses DL only
for a scroll region that reaches the bottom row, which the form's footer never
lets happen. A driver that is wrong only on a path nobody has walked is still
wrong.

One more thing pyte gets wrong, and it is the opposite failure: it does not
know a CSI that opens with `=`, abandons it, and **draws the rest as text**.
The form's exit sequence sets kitty keyboard flags with `CSI = 0;1 u`, and the
last frame read `▌ title      un0;1ued`. Those sequences only set, push or pop
keyboard flags, so the emulator removes them before pyte sees them — including
one cut in half by the pty read. Every other sequence the form was seen to
emit that pyte does not handle was fed to it alone and left the screen
unchanged: modifyOtherKeys (`CSI > 4 m`), the kitty keyboard query
(`CSI ? u`), a mode query (`CSI ? 2026 $ p`), a tab-stop reset (`CSI ? 5 W`),
and the private modes for the alternate screen, mouse and bracketed paste.

That bug was invisible for the whole of #290, so **every reading is now
compared with a full repaint of the same state.** A resize away and back makes
the program draw every line from scratch, which is ground truth that owes
nothing to how well the emulator applied the increments. If the two disagree
the driver still prints what was painted, names the rows that differ on
stderr, and exits 1. Measured on the emulator as #290 shipped it, one of
twelve walks — the picker filter at 101x30 — disagrees, at exactly the rows
above; with SU and SD, none do. It costs 1.5–2s per size (measured 1.5s
here and about 2.0s on another run of the same machine); `--no-check-repaint`
skips it. A disagreement is a finding either way: this emulator misread a
sequence, or the program's own incremental repaint is wrong. One false alarm
is possible — a picker that re-scrolls differently at the resize's
intermediate height — so read the rows before concluding. A walk that ends the
form — `esc`, `⌃C`, a submit — has nothing left to repaint, so the check is
skipped with a note rather than run: resizing pyte alone drops its top line and
reports every row as different, which the first version did.

That check covers only what a walk reaches, and no walk emits DL or IL or
chooses where a pty read ends. **`just live-selftest`** covers the rest: it
feeds `emulator()` byte strings directly, with no binary, pty or stub, and
compares each screen with what a terminal shows. The cases are SU and SD with
and without margins, DL and IL, the kitty sequences and the ignored ones cut
at every byte, and 2,000 seeded scrolls against a plain list of lines. It also
holds the two Linear refusals above to every config case they name, since no
walk exercises a refusal either. It takes well under a second, so run it after
touching `emulator()`, `refuse_real_linear` or `pyte_version`. It is not part
of `just check`, which must not need Python.

Two runs at once used to corrupt each other through the shared scratch tree
as well as the shared binary. A run now holds a lock beside `--root` for its
whole length; a second one says it is waiting and then runs.

## Why `pyte` and not a regex

A Bubble Tea program repaints **partially**. Stripping ANSI out of the raw
stream and grepping the result produces confident nonsense: lines that were
overwritten half a frame ago, cursor jumps read as text order. `pyte` is a
terminal emulator, so what it holds is what a person would see. That is the
same reason `docs/manual-smoke.md` warns that a `pane read` is one keystroke
stale.

## Things that will mislead you anyway

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

**`--repo` points the form at a real repository, and the form fetches.** The
project row's open does one `git fetch --prune` against that repo's real
remote. It is the one flag that reaches the network — no credentials travel
with it, since `SSH_AUTH_SOCK` is not inherited, so a private remote simply
fails — and the throwaway repo the driver makes has no remote at all.

**The config is per run, like the state.** `--config FILE` installs `FILE`
for that run only. It used to persist, so one run's config silently applied
to every run after it — a config handing the binary a wrong Linear key went
on putting the stub's refusal on screen for runs that never asked for it.

**State carries over unless you let it go.** The plugin's state directory holds
`recents.json`, `last-used.json` and `projects.json`, so a second run would
otherwise open on what the first left behind. The driver wipes it on every run;
`--keep-state` is how you test memory on purpose, and `--fresh` throws the whole
tree away (and refuses to do that to a directory it did not make).

## Worked example: reading a footer cliff

The key ladder falls to its constant tail at six measured terminal widths, one
per zone, and steps back up one column wider. Two of them, read live on
2026-09-20 against this branch's binary:

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

Which is what `internal/form`'s own narrow-band fixtures say, arrived at
through a real terminal instead of `ViewAt` — and that agreement is the point.
A golden-frame suite proves only the states someone thought to fixture.

(The widths are deliberately not cited to a symbol here. They belong to a test
table in `internal/form`, and a pointer from `hack/` into an unexported test
variable is the kind of citation that resolves on one branch and nowhere else.)
