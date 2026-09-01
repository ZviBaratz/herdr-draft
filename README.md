# herdr-draft

New-session creation dialog for [herdr](https://github.com/herdrdev/herdr).

<!-- screenshot: the form open in a herdr popup, Linear issue picker focused,
     rendered in the user's herdr theme -->

## What it is

herdr-draft is a herdr plugin that reproduces the "new session" creation
form from [Atrium](https://github.com/ZviBaratz/atrium) (the author's
previous TUI): a single-screen form, opened in a herdr popup, that creates a
fully configured agent session in one submit — Linear issue, project, git
worktree (branch + base), placement (space / tab / split), agent kind,
Claude account (via [clauth](https://github.com/clauth/clauth)), and an
initial prompt.

It is a standalone Go + Bubble Tea binary. herdr neither knows nor cares
about the implementation language — the plugin drives herdr exclusively
through the public CLI (`$HERDR_BIN_PATH`, itself reaching the herdr server
over the socket at `$HERDR_SOCKET_PATH`).

One keybinding opens the form; one submit produces a running, briefed agent
in the right place. See `docs/specs/2026-08-31-herdr-draft-design.md` for
the full design.

### The Project field

The Project field has two modes, and switches between them on what you
type:

- **Fragment** (anything not starting with `/`, `~` or `.`) fuzzy-filters
  a fixed pool of candidates: the current space's repo root first, then
  the current workspace cwd, then every open herdr workspace's own
  worktree root, then your recents.
- **Path** (`/`, `~` or `.`) browses the filesystem — the subdirectories
  of the parent you have typed so far, re-read only when that parent
  changes. `Tab` completes to the longest common prefix, shell-style, and
  deliberately stops at an exact directory rather than diving into it;
  typing `/` is how you descend. The fully typed path is always the last
  row, so a directory that does not exist yet (or one buried past the
  500-entry listing cap) stays selectable.

Browsed rows are absolute — `~/Projects/x` resolves at browse time, since
`herdr workspace create --cwd` would otherwise resolve a relative path
against the *server's* working directory. An inline marker rates whatever
is selected: `(invalid)` for a path that does not exist, `(direct)` for a
directory that is not a git repository (which is allowed — it just means
no worktree).

## Requirements

- herdr ≥ 0.8.2 (`min_herdr_version` in `herdr-plugin.toml`).
- [clauth](https://github.com/clauth/clauth) ≥ 0.14.1 for account-pinned
  (Path B) launches — see [clauth integration](#clauth-integration) below.
  clauth is entirely optional: without it, herdr-draft still works for
  manual/Linear-seeded session creation (Path A), it just has no Account
  field to pin a profile.
- Go 1.25+ to build from source (`herdr-plugin.toml`'s `[[build]]` runs
  `go build -o bin/herdr-draft ./cmd/herdr-draft` for you).

## Install

Not yet published to a registry. For now, link a local checkout:

```bash
herdr plugin link <path-to-this-repo>
```

`<path-to-this-repo>` is a positional argument (a plugin directory
containing `herdr-plugin.toml`, or a direct manifest path) — not `--path`.
herdr runs the manifest's build command and registers the plugin globally
for the current user; every herdr session sees it immediately.

Once published, the intended route is:

```bash
herdr plugin install ZviBaratz/herdr-draft
```

(this is aspirational — herdr-draft has not been published yet; see the
design spec §16, "marketplace publication," for why it's deferred until the
tool has proven itself in daily use).

## Keybinding

Plugin actions don't appear in herdr's context/global menus in plugin v1 —
keybinding and CLI are the only discovery surfaces (see
[Troubleshooting](#troubleshooting)). Bind a key in herdr's own
`config.toml`:

```toml
[[keys.command]]
key = "prefix+n"
type = "plugin_action"
command = "draft.open"
description = "new session"
```

`draft.open` is herdr's qualified id for herdr-draft's one action (plugin
id `draft`, action id `open`, from `herdr-plugin.toml`). You can also
invoke it directly from a shell:

```bash
herdr plugin pane open --plugin draft --entrypoint open
```

## Configuration

herdr-draft reads `$HERDR_PLUGIN_CONFIG_DIR/config.toml`. Every key is
optional; a missing file or omitted key falls back to the default shown
below. Unknown keys are ignored, so an older herdr-draft binary tolerates a
newer config file.

Get the config directory for your herdr install with:

```bash
herdr plugin config-dir draft
```

### Top level

- `branch_prefix` (default: your lowercased OS username plus `/`, e.g.
  `zvi/`, or `user/` if the username can't be determined) — prefix
  prepended to the sanitized title when herdr-draft derives a branch name
  in manual mode.
- `default_worktree` (default: `true`) — whether the Worktree chip starts
  On or Off for a git target.
- `default_placement` (default: `"new-space"`) — where a non-worktree
  session lands: `new-space`, `tab-here`, or `split-here`. Only relevant
  when Worktree is Off; a worktree is always its own linked-worktree space.

### `[linear]`

- `api_key_cmd` — argv (no shell) whose stdout is your Linear API key, e.g.
  `["pass", "show", "linear-api-key"]`. Checked before `LINEAR_API_KEY` and
  before `api_key` below.
- `api_key` — the API key given directly in the config file. Discouraged;
  herdr-draft checks the file's permissions (0600) before trusting a value
  read this way.
- `prompt_template` (default: empty, meaning the built-in template) —
  overrides the prompt seeded from a selected Linear issue. The built-in
  default is:

  ```
  Work on {identifier}: {title}

  {url}

  {description}
  ```

If no Linear API key resolves from any of the three sources, the Linear
issue field is simply not rendered — manual mode is unaffected.

If a key source is *configured but fails* — `api_key_cmd` exits non-zero or
isn't on `$PATH`, or `api_key` sits in a `config.toml` readable by anyone
but you — the issue field is rendered, marked `unavailable`, with the
reason on the line beneath it. It can't be focused, and everything else in
the form still works.

### `[clauth]`

- `enabled` (default: auto-detected) — explicitly force clauth integration
  on or off. Omit to let herdr-draft detect it.
- `default` (default: unset, meaning "don't pin") — which clauth profile to
  pre-select on the Account field: a profile name, or the sentinel
  `"active"` — unset and `"active"` behave identically ("don't pin, use
  whichever profile clauth currently has live").

The Account field itself only renders when clauth is configured **and** at
least two profiles exist (a static, startup-time check).

### `[agents]`

- `favorites` (default: `["claude"]`) — the chip row of agent kinds shown
  up front. herdr's full kind list (23 as of this writing) is reachable
  behind the row regardless of what's listed here.
- `default` — which kind the Agent field starts on. It may name a kind
  outside `favorites` (it will be selected in the "more…" list); an
  unknown kind is ignored rather than guessed at. Three layers decide the
  starting kind, each overriding the one before: `favorites[0]`, then
  `default`, then the kind of your last successful launch
  (`last-used.json`).
- `[agents.extra_args]` — a sub-table mapping agent kind to extra CLI args
  appended at launch, e.g. `claude = ["--model", "sonnet"]`. Empty by
  default for every kind.

### `[timeouts]`

- `detection_ms` (default: `30000`) — how long herdr-draft polls
  `herdr agent get` after launch before giving up on detecting the agent.
- `prompt_wait_ms` (default: `120000`) — timeout passed to
  `herdr agent prompt --wait` for step 3 of the submit pipeline.

### `[palette]`

Optional escape hatch when herdr's own theme can't be read correctly (see
the design spec §7 for why: `auto_switch` and `name = "terminal"` aren't
resolvable from a static config file, so herdr-draft falls back to the
configured dark variant for both). Override individual fields, e.g.:

```toml
[palette]
accent = "#89b4fa"
panel_bg = "#1e1e2e"
```

Recognized keys mirror `internal/theme.Palette`'s seven fields: `accent`,
`panel_bg`, `text`, `dim_text`, `danger`, `success`, `border` (matching
case-insensitively, underscore-optional). Values must be a strict `#RGB` or
`#RRGGBB` hex literal — anything else (a named color, an ANSI code, a
malformed string) is silently ignored and that field keeps its previous
value.

## clauth integration

When clauth is configured and ≥2 profiles exist, the Account field lets you
pin a profile for the launched Claude session (`clauth start <profile> --`,
routed through `herdr pane run`). Pinning is optional — leaving Account on
"Active" lets clauth use whatever profile is currently live, with no
wrapper involved.

**Tested with clauth 0.14.1+.** This is the empirically-confirmed floor
from this plugin's own live validation (`clauth status --json` schema `1`
parsing, and `clauth start <profile> --` launching correctly under
`herdr pane run`) — no older clauth 0.x release was available to test, so
treat it as "known to work at 0.14.1," not a verified absolute minimum.

**Known gap, accepted for v1**: on herdr session restore, herdr resumes
`claude --resume <id>` without the clauth wrapper, dropping the account
binding. Upstream discussion #3228 proposes per-agent resume templates
(`clauth info <id>` already prints the exact resume command). Documented
in README; nothing herdr-draft can fix locally.

## Known limitations

These are real, live-verified rough edges, not deferred features — read
them before filing a bug against something documented here:

- **Prompt-delivery dialog guard.** When a freshly launched agent shows a
  confirmation dialog — most commonly Claude Code's first-run "Accessing
  workspace" trust prompt in a brand-new worktree — herdr's own agent
  detection reports it as idle/ready-for-input, indistinguishable from the
  agent actually being ready. herdr-draft deliberately does **not** send
  the queued prompt in that case: it recognizes a small, explicit list of
  known dialog signatures (`internal/plan/dialog.go`) and, on a match,
  stops before sending anything, surfacing the prompt text in the popup for
  you to paste manually instead. The alternative — trusting herdr's "idle"
  signal and sending the prompt anyway — used to silently answer the
  dialog's default ("No, exit") and kill the agent with no error shown
  anywhere. The root cause is upstream (herdr's detection manifest doesn't
  yet distinguish a blocking confirmation dialog from a truly idle
  terminal), not something this plugin can fix; this guard is a defensive
  workaround that stays in place until herdr's detection improves.
- **`[worktree] trust_repository` is blocked upstream, not just
  unimplemented.** The design spec's submit pipeline mentions
  `--trust-repository per config`, and herdr does have that flag — on
  `master`, added in commit `095f1337` (#3344, 2026-08-28), which is in no
  release yet. herdr 0.8.2, this plugin's minimum, answers
  `unknown option: --trust-repository`, so passing it would break worktree
  creation. The config key is deliberately absent rather than present and
  inert: a key that silently does nothing is worse than no key. Until a
  herdr release carries that commit, trust the repository yourself
  (`git config --global --add safe.directory <path>`) before or after
  creation.
- **Popup panes are only reachable via keybinding or CLI.** Plugin actions
  do not appear in herdr's own context/global menus in plugin v1 — see
  [Keybinding](#keybinding) for both routes.

## Troubleshooting

**Popup won't open / `ui_busy` error.** herdr's own API returns `ui_busy`
("popup panes can only open from the normal workspace view") when another
herdr modal is already open. Close whatever herdr dialog is currently up
and try again.

**"herdr-draft: ..." plain-text error, nothing renders.** herdr-draft
refuses to open the form at all — rather than opening it broken — when it
can't reach the herdr socket, when `$HERDR_PLUGIN_CONTEXT_JSON` is missing
or malformed, or when `config.toml` fails to parse. Check
`$HERDR_SOCKET_PATH` is set and the herdr server behind it is actually
running; check `config.toml` for a syntax error if you've recently edited
it.

**Plugin actions not appearing in herdr's menus.** Expected in plugin v1 —
see [Known limitations](#known-limitations). Use the keybinding or the CLI
route from [Keybinding](#keybinding).

**Account field isn't showing up.** It only renders when clauth is
configured *and* at least two profiles exist, checked once at form
startup. Fewer than two profiles, or clauth not detected at all, means the
field is simply absent — this is a static, by-design check, not a bug.

**Linear field says `unavailable`.** Your key source is configured but
failed; the reason is on the line beneath the field. See
[`[linear]`](#linear).

**"prompt not sent" after a submit.** The session was created and the agent
started, but the prompt couldn't be delivered (the agent was showing a
dialog, or `agent prompt --wait` timed out). Your prompt text is saved to
`unsent-prompt.txt` in the plugin state directory — the failure screen
shows the full path — so you can paste it into the agent by hand. The
session itself is fine; choose *keep*.

## State

herdr-draft keeps a small amount of loss-tolerant state in
`$HERDR_PLUGIN_STATE_DIR`, all of it safe to delete:

- `recents.json` — recently used project directories, offered as Project
  candidates. Written after each successful submit.
- `last-used.json` — the agent kind, placement, and worktree toggle from
  your last successful submit; the form defaults to them next time.
- `linear-cache.json` — the last Linear issue list, rendered instantly at
  form-open while a fresh one loads.
- `unsent-prompt.txt` — a prompt that couldn't be delivered, kept for
  manual paste (see Troubleshooting above). Overwritten by the next one.

## Testing

```bash
go test ./...        # unit + golden-frame tests, no I/O
gofmt -l .            # formatting check
go vet ./...
```

`just check` runs all three. Manual release smoke (throwaway herdr session,
Path A/B × worktree on/off) is documented separately in
[`docs/manual-smoke.md`](docs/manual-smoke.md) — not part of CI.

## License & provenance

herdr-draft is MIT licensed (see `LICENSE`). It ports and adapts UI code
from [Atrium](https://github.com/ZviBaratz/atrium) (AGPL-3.0) under a
license grant from Atrium's own author, and translates visual conventions
and default palette values from herdr (Apache-2.0). Full attribution and
the audited file-by-file provenance are in `NOTICE` and the design spec's
§14.
