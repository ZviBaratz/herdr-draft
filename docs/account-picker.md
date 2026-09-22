# Claude accounts

If you use more than one Claude account, herdr-draft can start each session
under the one you choose. It does this through
[clauth](https://github.com/uwuclxdy/clauth), which manages several accounts
on one machine, and optionally through an **account picker**: a program of
your own that chooses the account for you.

Both are optional. Without clauth, the form has no `account` row, and every
session starts under whatever account `claude` would use anyway.

## The account row

The row appears when clauth is installed and has at least two profiles. It
is checked once, when the popup opens. clauth 0.14.1 is the oldest version
tested.

The row starts on `active`, which pins nothing: the session uses whichever
profile clauth has live. Pinning a profile starts the session under that
account, by default with `clauth start <profile> --`. To launch through a
wrapper of your own instead, see
[`launch` and `launcher`](configuration.md#launch-and-launcher). Pinning only
applies to `claude`; for any other agent the row reads
`account pinning only applies to claude`.

Each profile carries its own state, as `<name> · <plan> · 5h 12% · 7d 40%`:
the share of its five-hour and seven-day usage already spent. A
rate-limited profile's figure is in the warning colour. A profile that is
fine, or that clauth has no reading for yet, says nothing more. Otherwise it
shows one of these:

| The row says | What clauth reports | Can you launch on it? |
|---|---|---|
| `expired`, in the warning colour | an access token due for a refresh. The launch refreshes it. clauth before 0.15.0 called this `expiring` | yes |
| `sign in again`, in red | a credential whose last refresh was rejected as revoked or invalid. Run `clauth login` for that profile | no: the create is refused |
| anything else, in the warning colour | a state a newer clauth added, in clauth's own words | yes |

**When the row says `unavailable`**, clauth is installed but herdr-draft
could not read it: it exited with an error, its output did not parse, or it
did not answer within thirty seconds. The reason is on the row. Focusing the
row, with `⇥` or a click, asks clauth again without closing the form; a
reload that finds two or more profiles makes the row work as if clauth had
answered at the start. A reload that fails replaces the reason with the new
one, so the row always reports the latest attempt. The row's panel shows a
reason too long for one line in full.

When clauth reports a status format this version of herdr-draft does not
know, the row lists profile names only, since the rest of each entry cannot
be trusted.

**A known gap:** when herdr restores a session after a restart, it resumes
`claude --resume <id>` directly, without clauth, so the restored session
loses its pinned account. herdr-draft cannot fix this from outside herdr.
`clauth info <id>` prints the resume command with the right account, if you
need to resume one by hand.

## The account picker protocol

herdr-draft can hand the question "which Claude account should this session
use?" to an external program. It does not ship one and is not written
against any particular one. What follows is the whole contract: **any
executable that satisfies it is a picker.**

Name yours in `config.toml`:

```toml
[clauth]
picker = "my-picker"   # a command on PATH, or an absolute path
```

herdr-draft never goes looking for a picker on its own. With one configured,
the `account` row gains an `auto` entry (`auto → alpha`), and `create`
accepts `--account auto`.

### Invocation

```
<picker> --dir <project path> --json [--strict] [--dry-run]
```

- `--dir` is the project directory the session is being created in.
- `--json` is always passed.
- `--strict` asks for headless behaviour: no interactive fallback, no
  waiting for a person. `herdr-draft create` passes it; the popup does not.
- `--dry-run` asks the picker **not** to record the pick in whatever ledger
  it keeps, and **not** to build an account directory, so `config_dir` comes
  back `null`. The popup's preview passes it, on every project change. The
  one call that picks for real, when you create, does not.

### Answer

One JSON object on stdout:

```json
{
  "tenant": "alpha", "tenant_state": "default",
  "profile": "alpha-1", "tier": "Max",
  "config_dir": "/home/you/.local/state/account-dirs/alpha-1",
  "score": 8066, "class": "eligible", "reason": null, "overflow": false,
  "usage":     { "five_hour": 3, "weekly": 11, "cache_age_s": 18 },
  "resets_at": { "five_hour": "2026-09-10T22:49:59Z", "weekly": "2026-09-13T01:59:59Z" },
  "machine":   { "load1": 0.9, "ncpu": 8, "swap_used_pct": 2 },
  "warnings": [], "skipped": [ { "profile": "alpha-0", "why": "rate limited" } ]
}
```

**Six keys are required**: `profile`, `config_dir`, `reason`, and all three
of `usage.five_hour`, `usage.weekly` and `usage.cache_age_s`. They must be
present, and `null` is a fine value for any of them. Every other key is
optional, any field may be `null` when the picker could not work it out, and
keys not listed here are ignored.

`reason` is the picker's own one-line explanation, and it is what
herdr-draft shows you when the picker says no.

Two optional keys are shown rather than acted on. `warnings` appear on the
`account` panel in the warning colour, one line each, and `create` prints
them on stderr. `machine` appears on the panel as one dim line, such as
`machine  load 4.4 on 8 cpus · swap 32%`; a figure sent as `null` reads
`unmeasured` rather than zero. Neither ever refuses a session: refusing is
the picker's job, through its exit code.

### Exit codes

| Code | Meaning |
|---|---|
| `0` | picked: `profile` names the account |
| `2` | refused: the pool is exhausted |
| `3` | refused: the machine is under too much load |
| `4` | refused: no usable account table |
| `5` | refused: no profiles registered |
| `64` | usage error: the picker did not accept herdr-draft's invocation |

Any other exit code is treated as a malfunction, not a refusal, and the
picker is reported as unusable rather than obeyed. So is exit `0` with a
`null` profile: `0` means picked, and herdr-draft will not quietly launch
unpinned instead.

`64` is the odd one out. Every other code is the picker's judgement about
your request; `64` means the picker and herdr-draft disagree about how it is
called. If you see it, compare your picker's flags with the invocation above.

**Every call gets 30 seconds.** A picker that has not answered by then is
abandoned and reported as a malfunction, never as a refusal. The limit is
generous because a picker may need several network round trips, and it
exists so that a picker that hangs cannot stop the popup opening.

### What herdr-draft does with it

- **When the popup opens**, once: `--json --dry-run` against the current
  project. Only the answer's **shape** is checked (the six required keys,
  and one of the documented exit codes), never the answer itself: a picker
  whose pool is exhausted today is still a working picker. If the check
  fails, the `account` row says so and has no `auto` entry.
- **On every project change**: `--json --dry-run` again, so the `auto` entry
  shows what the picker would choose here.
- **Once per create**, after every other check has passed and before
  anything is created: `--json`, plus `--strict` from `create`. This is the
  call whose answer becomes the pin. A refusal here refuses the create and,
  in the popup, moves focus to the account list. It never falls back to an
  unpinned launch.

`herdr-draft create` skips the startup check, since the next thing it does is
the real call. `create --account auto` with no picker configured is a usage
error (exit 2), not a quiet fallback.

Closing the popup, or interrupting `create`, stops a pick that is still
running. A pick that has already answered cannot be taken back: the protocol
has no way to return an account.

With `[clauth] launch = "wrapper"`, the pin is launched as
`CLAUDE_CONFIG_DIR=<config_dir> claude`, which is why a real pick must
answer a `config_dir`. See [`[clauth]`](configuration.md#clauth).

### Writing one

A picker can be a shell script. The smallest one that meets the contract:

```sh
#!/bin/sh
# Always picks the same account. The one flag it must not ignore is --dry-run:
# a dry run builds no account directory, so config_dir comes back null. The
# startup probe and every preview pass it, so a picker that answers a real
# config_dir there is claiming, several times a session, to have built
# something it did not.
config_dir="\"$HOME/.claude-my-account\""
for arg in "$@"; do
	[ "$arg" = --dry-run ] && config_dir=null
done
cat <<JSON
{"profile":"my-account","tier":"Max","config_dir":$config_dir,
 "reason":null,"usage":{"five_hour":null,"weekly":null,"cache_age_s":null},
 "resets_at":{"five_hour":null,"weekly":null},"warnings":[],"skipped":[]}
JSON
```

This exact script is extracted from this page and run by the test suite, so
the example cannot drift from the contract it documents.

Point `[clauth] picker` at it, reopen the popup, and the `account` row gains
an `auto` entry reading `auto → my-account`.
