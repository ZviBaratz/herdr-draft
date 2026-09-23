# Configuration

herdr-draft works with no configuration at all. Everything on this page is
optional: a missing file, a missing table or a missing key falls back to the
default shown here.

There are two files:

- **`config.toml`** is yours. It lives in the plugin's config directory and
  applies to every session you create.
- **`.herdr-draft.toml`** is a repository's. A team commits it at the root of
  a repository to share a handful of creation defaults. It can set only five
  keys, for the reasons in [Repository defaults](#repository-defaults-herdr-drafttoml).

Find your config directory with:

```bash
herdr plugin config-dir zvibaratz.draft
```

## A complete example

Every key `config.toml` accepts, with its default. The lines commented out
have no default worth writing down, or are examples.

```toml
# Top level
branch_prefix = "you/"             # default: your lowercased username + "/"
default_worktree = true            # does the worktree row start on?
default_placement = "new-space"    # new-space | tab-here | split-here | tab-in

[linear]                           # the issue row appears once a key resolves
# api_key_cmd = ["pass", "show", "linear-api-key"]   # argv, no shell
# api_key = "lin_api_..."          # discouraged; the file must be 0600
# prompt_template = "..."          # default shown under [linear] below

[clauth]                           # the account row needs clauth and 2+ profiles
# enabled = true                   # default: detected
# default = "personal"             # default: pin nothing
# picker = "my-picker"             # an account picker, named explicitly
launch = "start"                   # "start" or "wrapper"
launcher = ["clauth", "start", "{account}", "--"]

[agents]
favorites = ["claude"]             # chips at the top of the agent row
# default = "claude"               # default: favorites[0]

[agents.extra_args]                # extra arguments per agent kind
# claude = ["--verbose"]

[agents.options.claude]            # default session options for one kind
# model = "opus"                   # fable | opus | sonnet | haiku | a full id
# effort = "high"                  # low | medium | high | xhigh | max
# permission_mode = "plan"         # manual | plan | acceptEdits | auto

[timeouts]
detection_ms = 30000               # wait for herdr to detect the agent
prompt_wait_ms = 120000            # wait for the agent to take the prompt
trust_wait_ms = 300000             # wait for you to answer a dialog (popup only)

[worktree]
trust_repository = false           # waive git's ownership check per request

[reaper]
mark_ready = false                 # start every prompt on "reap"

[palette]                          # colour overrides, when the theme reads wrong
# accent = "#89b4fa"
```

Unknown keys are ignored, so an older herdr-draft reads a newer file without
complaint. A key with the wrong **type**, such as a string where a list
belongs, is different: the popup refuses to open, naming the file, the
line and the key, and `create` exits 2 with the same message. So does a file
that fails to parse as TOML.

## Top level

**`branch_prefix`** (default: your lowercased OS username plus `/`, such as
`zvi/`, or `user/` when the username cannot be read). herdr-draft derives a
worktree's branch from the title and puts this in front of it:
`fix login redirect loop` becomes `zvi/fix-login-redirect-loop`.

It has to be usable as the start of a git branch name. A trailing `/` is
fine, and `branch_prefix = ""` means no prefix at all. It may not start with
`-` or with whitespace, including a no-break space. A value that fails any of
these is ignored and the default takes over, with the reason on the
`worktree` row's panel in the popup and on stderr from `create`. It never
stops the popup opening.

**`default_worktree`** (default: `true`). Whether the `worktree` row starts
on for a git repository.

**`default_placement`** (default: `"new-space"`). Where a session **without**
a worktree lands: `new-space`, `tab-here`, `split-here` or `tab-in`. It does
not apply to a worktree session, which always runs in the worktree's own
space.

These three are defaults, and something above `config.toml` can override
them. See [Where defaults come from](#where-defaults-come-from).

## `[linear]`

Linear is optional. With no key, the `issue` row is not drawn and everything
else works the same.

The key is looked for in this order, and the first one found wins:

1. **`api_key_cmd`**: an argv (a list, run with no shell) whose stdout is
   your API key, for example `["pass", "show", "linear-api-key"]` or
   `["op", "read", "op://Private/Linear/credential"]`. It gets **sixty
   seconds** to answer, longer than anything else herdr-draft waits for,
   because a password manager may be waiting for you to approve the read.
2. **`LINEAR_API_KEY`** in the environment.
3. **`api_key`**: the key written into `config.toml`. This is discouraged,
   and herdr-draft refuses to read it unless the file is readable by you
   alone (mode `0600`).

A source that is configured but fails does not hide the row. With a cached
issue list on disk the list stays pickable and the panel carries the
reason; with nothing to pick the row reads `unavailable` with the reason
beside it. That covers an `api_key_cmd` that exits non-zero, is not on
`PATH`, does not answer within its sixty seconds, or exits 0 having printed
nothing with no other source behind it, and an `api_key` in a file others
can read.

**The popup does not wait for the key.** It draws first, and the `issue`
row is live while `api_key_cmd` runs: the cached list is pickable, and the
panel reads `waiting for api_key_cmd…`. With no cache yet the row reads
`loading…`. So a helper that waits for you to approve a read costs you
nothing but that row — you can type a title, pick a project and press ⌃S
while it runs. (`herdr-draft create --issue` has no screen to draw, so it
does wait.)

The issue list is the issues assigned to you that are in a to-do or
in-progress state, the 50 most recently updated. The popup draws the last
list it fetched right away and refreshes it in the background. A refresh
that fails or takes longer than **thirty seconds** leaves the cached list
pickable and says so on the panel. herdr-draft never writes to Linear.

**`prompt_template`** (default: empty, meaning the built-in template). The
prompt a selected issue seeds. The built-in one is:

```toml
prompt_template = """
Work on {identifier}: {title}

{url}

{description}"""
```

The four placeholders are `{identifier}`, `{title}`, `{url}` and
`{description}`. Control characters other than newline and tab are removed
from the issue's text before it reaches the prompt, and Windows line endings
become plain newlines.

## `[clauth]`

[clauth](https://github.com/uwuclxdy/clauth) manages several Claude
accounts on one machine. With clauth installed and at least two profiles,
the form grows an `account` row that pins the session to one of them.
Without it, there is no row and sessions start under whatever account
`claude` would use anyway. See [Claude accounts](account-picker.md) for how
the row behaves.

**`enabled`** (default: detected). Force the integration on or off. Leave it
out to let herdr-draft look for clauth.

**`default`** (default: unset). The profile the `account` row starts on.
Unset and `"active"` mean the same thing: pin nothing, and use whichever
profile clauth has live.

**`picker`** (default: unset). An executable implementing the
[account picker protocol](account-picker.md#the-account-picker-protocol),
which adds an `auto` choice to the `account` row and `--account auto` to
`create`. A command name looked up on `PATH`, or an absolute path.

herdr-draft never goes looking for a picker. A program on `PATH` with a
plausible name is not used unless this key names it, because finding a
program with the expected name is not the same as finding the one you meant.
A named picker is also probed once, and is used only if its answer has the
documented shape. The probe rides on the first preview the popup makes, so
it costs no extra run: the `auto` row is offered from the first frame and
removed, with the reason on the panel, if that answer turns out not to
implement the protocol. A ⌃S made before the probe has answered waits for
it, and is refused rather than launched unpinned if it never does.

### `launch` and `launcher`

These two keys are one decision. `launch` picks how a pinned account starts,
and `launcher` configures one of the two ways.

| You want | Set |
|---|---|
| the default: `clauth start <profile> --` | nothing |
| your own account-switching command | `launcher = ["claude-as", "{account}"]` |
| a bare `claude` under a picker's credential directory | `launch = "wrapper"` |

**`launch`** (default: `"start"`).

- `"start"` types the `launcher` argv, followed by the agent's arguments,
  into the new pane. With the default launcher that is
  `clauth start <profile> -- <arguments>`: clauth sets up the profile's
  `CLAUDE_CONFIG_DIR` and runs Claude under it. It behaves the same on every
  machine that has clauth, which is why it is the default.
- `"wrapper"` types `CLAUDE_CONFIG_DIR=<dir> claude <arguments>` instead and
  leaves your pane's shell to resolve `claude`. It is only worth setting if
  your shell defines `claude` as a function, such as one that registers the
  session somewhere or starts a team lead: `clauth start` cannot call a shell
  function. If `claude` is the plain binary on your machine, this mode still
  isolates the credential and does nothing else.

  Wrapper mode needs a credential directory, and only a picker supplies one.
  A profile you pinned by hand falls back to `"start"` with the default
  launcher, because a bare `claude` with nothing isolating it would run under
  the machine's default credential, which is the opposite of the pin you
  asked for. The progress screen says when that happened.

**`launcher`** (default: `["clauth", "start", "{account}", "--"]`). The argv
`"start"` types, with `{account}` standing for the profile name. It is the
whole command, not a prefix: nothing is added except the agent's own
arguments, which come after it. The reason to change it is a wrapper of your
own, for example one that keeps a team lead that `clauth start` would bypass.
herdr types the argv into the pane's shell rather than running it directly,
so a shell function works, as long as the pane's shell defines it.

- It must contain exactly one `{account}`. A template with none, two, an
  empty list or a blank element is ignored and the default used, because a
  template with no `{account}` would launch whichever profile clauth has live
  and bill the wrong account.
- It is a **list of arguments**, not a command line. A string such as
  `launcher = "clauth start {account} --"` is a type error, and the popup
  refuses to open.
- The agent's arguments are appended after the template, so a launcher must
  accept them. An `sh -c "..."` wrapper passes the validator but loses them:
  they become the inner shell's `$0` and `$1` and never reach the agent,
  while the launch still reports success.

**You are told when one of them does not apply.** A `launcher` set beside
`launch = "wrapper"` is ignored, a malformed `launcher` is replaced by the
default, and an unrecognised `launch` value falls back to `"start"`. Each
produces a warning naming the key, the reason and what was used instead:
whole on stderr from `create`, and one line each on the `account` row's panel
in the popup.

## `[agents]`

**`favorites`** (default: `["claude"]`). The chips at the top of the `agent`
row's panel. Every agent kind herdr knows is listed under them either way.

**`default`** (default: the first favorite). The kind the `agent` row starts
on. It may name a kind outside `favorites`. An unknown kind is ignored.

**`[agents.extra_args]`** (default: empty). Extra arguments per agent kind,
appended every time that kind starts:

```toml
[agents.extra_args]
claude = ["--model", "claude-opus-5[1m]", "--verbose"]
```

Write each argument literally, with no shell quoting of your own. Each
value is quoted for the shell wherever a shell reads the command, so one
with `[` or `*` in it is safe as written, and quotes you add yourself reach
the agent as part of the value.

For the model, effort and permission mode, prefer `[agents.options]`: those
can be changed per session, in the popup or with a `create` flag. The
`options` row's panel shows what `extra_args` adds, read-only.

## `[agents.options.<kind>]`

The session options a kind starts with: what the `options` row opens on,
and what `create` uses when you leave the flag off. Only `claude` declares
options today.

```toml
[agents.options.claude]
model = "claude-opus-5[1m]"   # an alias (fable, opus, sonnet, haiku) or a full model id
effort = "xhigh"              # low | medium | high | xhigh | max
permission_mode = "plan"      # manual | plan | acceptEdits | auto
```

- A key left out, or set to `"inherit"`, sends no flag of its own. What
  `[agents.extra_args]` passes then decides, and failing that, Claude's own
  settings.
- An option you set, here or in the popup, **replaces** the same flag in
  `[agents.extra_args]` rather than adding a second one, in either spelling
  (`--model opus` or `--model=opus`).
- `bypassPermissions` and `dontAsk` are not accepted. The first opens with a
  confirmation dialog that the session's prompt would be typed into, and the
  second refuses every action that is not already allowed, which suits a
  script rather than a pane. Pass either through `[agents.extra_args]` if you
  mean it.
- A value the kind does not offer, a key it does not declare, a kind with no
  options, and a value that is not a string are each reported, on the
  `options` panel and on `create`'s stderr, and skipped. None of them stops
  the popup opening.
- Options are never remembered from a submit. A session you started in plan
  mode does not make the next one plan; change the default here.

## `[timeouts]`

**`detection_ms`** (default: `30000`). How long to wait for herdr to detect
the agent after it starts.

**`prompt_wait_ms`** (default: `120000`). How long to wait for the agent to
take the first prompt. A wait that runs out does not mean the prompt was
lost: the agent may have been busy painting its screen. See
[Troubleshooting](troubleshooting.md#delivery-unconfirmed-after-a-submit).

**`trust_wait_ms`** (default: `300000`, five minutes). How long the popup
waits for **you** to answer a blocking dialog in the new pane, in practice
Claude Code's "do you trust this folder" prompt in a fresh worktree. The
step shows `…`, the footer says `answer the dialog in the pane`, and the
popup carries on by itself once you do. If the time runs out, or the agent
exits because you declined, you get the failure screen with your prompt
saved. `0` turns the wait off. Headless `create` never waits for a person:
it fails straight away with the reason, whatever this is set to.

This covers waiting for you and nothing else. When a prompt stalls, which
happens for about a second after a trust dialog clears, herdr-draft pauses
briefly and sends it once more. That pause waits for the screen, not for a
person, so both the popup and `create` take it, and `0` does not turn it
off.

## `[worktree]`

**`trust_repository`** (default: `false`). Adds `--trust-repository` to
herdr's worktree creation, which trusts the repository for that one request.
Turn it on if creating a worktree fails with git's "dubious ownership" error,
usually a repository owned by a different user. The alternative is the
permanent, machine-wide `git config --global --add safe.directory <path>`.

Only `config.toml` can set this. A repository cannot vouch for itself, and
neither memory file records it, because they record what you chose in the
form and no row offers it.

## `[reaper]`

**`mark_ready`** (default: `false`). Starts the prompt panel's toggle on
`reap`, and makes `create` behave as if `--reap` were passed.

A reaped session's prompt ends with one fixed sentence asking the agent to
run this as the last command of its final turn, once its work is saved:

```bash
herdr pane report-metadata "$HERDR_PANE_ID" --source pane-reaper --token pane_reaper=ready
```

That token is read by **pane-reaper**, a separate herdr plugin that closes a
pane its agent has marked ready once the pane goes idle. Without pane-reaper
the sentence costs one harmless command and nothing closes. It is never
added to an empty prompt, or to one that starts with `/`, since a slash
command would take it as arguments. Closing the pane is all pane-reaper does:
a worktree session's checkout and branch stay, and are yours to remove.

Leave `mark_ready` off if you start long-running orchestrator or `/loop`
sessions from the popup, which must never close themselves. `⌃X` in the
prompt, or `--reap` / `--no-reap`, decides for one session. Only
`config.toml` can set it, and it is never remembered: one reaped session
would otherwise make every later one reap.

pane-reaper lives in the
[quantivly/dotfiles](https://github.com/quantivly/dotfiles/tree/main/config/herdr/plugins/local/pane-reaper)
repository. Install it with
`herdr plugin link <clone>/config/herdr/plugins/local/pane-reaper`.

## `[palette]`

herdr-draft draws in herdr's colours. It reads herdr's own `config.toml`:
the theme `[theme] name` selects, then any colours in herdr's
`[theme.custom]`. `auto_switch` cannot be resolved from a file, because it
follows the live terminal's light or dark appearance, so herdr-draft uses
your configured dark theme (`dark_name`, or catppuccin when that is unset).

`name = "terminal"` sends your terminal's own palette entries, the way
herdr's `terminal` theme does: red is your terminal's red, and the
background, the text and the input fields are left as your terminal draws
them. The focused row has no fill of its own on this theme, so its words
stay on your terminal's background; it is marked by the `▌` in the left
margin and a bold value. herdr-draft cannot see what any of those colours
are, so the contrast adjustments described below do not apply to it. If a
colour is hard to read there, change it in your terminal's palette or
override it in `[palette]`.

`[palette]` overrides individual colours on top of all that, for when the
theme reads wrong:

```toml
[palette]
accent = "#89b4fa"
active_row_bg = "#45475a"
```

| Key | What it colours |
|---|---|
| `accent` | the create button, the active chip, the characters a filter matched |
| `panel_bg` | the background |
| `text` | values |
| `dim_text` | row labels, hints and other secondary text |
| `overlay0` | the rules between sections, column headings, the scrollbar thumb |
| `danger` | failures and refusals |
| `success` | success marks, the picker badge |
| `warning` | rate-limited, expired and degraded states |
| `branch` | branch and base names |
| `border` | the scrollbar track |
| `surface` | the secondary button, a picker's highlighted row, input backgrounds |
| `active_row_bg` | the focused row's fill (`selection_bg` is accepted as another name for it) |

Keys match case-insensitively, and the underscore is optional
(`panelbg` works). A value must be a `#RGB` or `#RRGGBB` hex colour. Anything
else is ignored and that colour keeps its previous value.

After every override has applied, herdr-draft checks each colour against the
backgrounds it is drawn on. The focused row's fill, and every colour that
carries a word, are raised to a minimum contrast when they fall short, so a
colour you set may be drawn a little lighter or darker than written. The
focused row is also marked with a `▌` in the left margin, so it stays visible
whatever the fill looks like.

## Where defaults come from

Every row the form opens on, and every flag you leave off `create`, is
resolved the same way. **Highest first:**

| Source | What it is |
|---|---|
| `herdr workspace list` | a space already open for this project (placement only, see below) |
| `projects.json` | what you last chose in *this* project |
| `.herdr-draft.toml` | the repository's committed default |
| `last-used.json` | what you last chose anywhere |
| `config.toml` | your own configuration |
| built-in | herdr-draft's default |

A team's committed default beats what you last did in some *other*
repository, and loses to what you last did in this one.

Pressing `⌃R` twice clears the form back to these defaults, including the
repository's `.herdr-draft.toml`. The one thing it leaves out is this
project's own memory, so you land on the repository's default rather than
on what you last did here. Move to another project and back, and that
memory applies again.

Not every source can supply every value:

- `projects.json` and `last-used.json` remember only what a successful
  submit settled: the worktree toggle, the placement, the agent kind, and the
  base for the project you were in.
- `.herdr-draft.toml` never chooses your agent. That is a decision about
  your machine, not the repository.
- The prompt's `keep · reap` toggle and the `options` row come from
  `config.toml` or the built-in default only.
- A selected Linear issue sets the title, branch and prompt on top of all of
  it, unless you have already typed over them.

**Per-project memory re-applies when you change the project row**, for every
field you have not touched yourself. It is keyed by the repository's root,
so a linked worktree and the repository it belongs to share one memory.

**The one source that is not a file.** When a space is already open on the
project's checkout, a `new space` placement that came from the built-in
default, `config.toml` or either memory file becomes `tab in <that space>`.
The repository's space then collects its sessions as tabs, rather than a new
space appearing for every task. A remembered `tab in` falls back to
`new space` once that space is gone. A `here` placement stands, since it is a
choice about the pane you are in. So does a `.herdr-draft.toml` that says
`new-space`, and so does anything you choose in the form or pass with
`--placement`.

None of this applies to a worktree session. It always runs in the
worktree's own space, which herdr's sidebar already nests under the
repository's. The `placement` row goes inert and keeps its value for when
you turn the worktree off.

**A remembered or configured base is checked before it is used**, the same
way by the form and by `create`:

- One that names a commit is kept. The base list holds only the 50 most
  recently committed branches, so a base it does not name, such as an older
  branch, a tag or `HEAD~1`, is offered right after the `HEAD` row.
- `HEAD`, `@`, and any other spelling of HEAD's own commit (`HEAD^0`,
  `@{0}`) mean the `HEAD` row: no base, so the worktree starts from the
  commit the project is on.
- One that names no commit falls back to `HEAD`, and the worktree panel
  says which base was dropped and where it came from, for example
  `ignoring base "old-feature" from projects.json: no such commit here; using HEAD`.
  `create` prints the same on stderr. A deleted branch is the usual cause.
  Another is a branch that exists only on the remote, written by its bare
  name: git knows it only as `origin/<name>`, which is how the base list
  offers it.

**Seeing where a value came from.** `create --json` carries a `provenance`
map naming the source of each value. In the form, only the repository's file
is credited: `from .herdr-draft.toml`, on its own line in the panel of the
row it set. The mark goes away once you change that value yourself.

## Repository defaults (`.herdr-draft.toml`)

A repository can commit a `.herdr-draft.toml` at its root so everyone
working in it gets the same creation defaults. It is read from the main
repository root, so a linked worktree sees the same file, and it is re-read
whenever you change the project row.

```toml
branch_prefix = "team/"
default_worktree = true
default_placement = "new-space"
default_base = "origin/develop"
linear_branch_name = false
```

Those five keys are the whole list:

- **`branch_prefix`**: the same rules as in `config.toml`. An unusable value
  is ignored with a reason, and your own configured prefix applies instead.
- **`default_worktree`** and **`default_placement`**: as in `config.toml`.
- **`default_base`**: the base new worktrees start from. It has to name a
  commit in each clone that uses it, or that clone falls back to `HEAD` with
  a note. A fresh clone has most branches only as `origin/<name>`, so write
  `"origin/develop"` rather than `"develop"` unless everyone has a local
  `develop`.
- **`linear_branch_name`** (default: `true`): whether a selected Linear
  issue's own branch name is used. Set it to `false` in a repository with its
  own branch naming. The branch is then derived from the title and
  `branch_prefix`, while the issue still seeds the title and prompt.

**This file is a trust boundary.** It arrives with `git clone`, so it may
only choose among values you could have picked in the form yourself. It may
never name a command, a path outside the repository or a credential; if it
could, cloning a repository would be enough to run code on your machine.

So everything else in it is ignored and reported, with the reason. That
includes every other key `config.toml` accepts: `[agents.extra_args]` and
`[agents.options]` (they become part of the agent's command line),
`[agents] favorites` and `default` (a repository does not choose which
agent runs on your machine), `[linear] prompt_template` (it would become the
agent's first instruction), `[reaper]` (it would add an instruction to the
prompt), `[linear] api_key` and `api_key_cmd`, and all of `[clauth]`,
`[timeouts]`, `[palette]` and `[worktree]`. The last matters most:
`trust_repository` waives a git safety check, and whether a repository is
safe can never be decided by the repository. A misspelled key and a file that
does not parse are ignored the same way, and never block the form.

The report is on the `project` row's panel, one line per ignored key,
because the project row decides which repository's file applies:

```
  ignoring agents.extra_args: it becomes part of a launched agent's command li…
  ignoring linear.prompt_template: it would become the agent's first instructi…
```

`create` prints the same lines on stderr.
