# The form

The popup is one screen: a header, a stack of rows, one panel, and a footer.
This page goes through each part, then the keys, then what happens after you
create.

![The form as it opens, with Linear and clauth both set up: nine one-line
rows with the title row focused, and the sessions already open listed in
the panel below](images/opening.svg)

A few herdr words, since the form uses them throughout. herdr's sidebar
lists **spaces** (herdr's API calls them workspaces). A space holds
**tabs**, and a tab holds **panes**, each running a shell or an agent. A git
**worktree** is a second checkout of a repository, in its own directory and
on its own branch, so a session can work without disturbing your main
checkout.

## Layout

- **The header** reads `new session` on the left and, on the right, the
  project the session will be created in and the branch checked out there.
  It follows the `project` row, not the space you opened the popup from.
- **The rows** each take exactly one line and never move. A row says what the
  session will be, not which setting is on. The focused row is filled with
  the theme's selection colour and marked with `▌` in the left margin.
- **The panel**, under the rows, belongs to the focused row: a list to pick
  from, a text box, a line of chips, or an explanation of why the row cannot
  be changed. Lists are tables with aligned columns, a count such as
  `3/24 issues`, and a scrollbar once they outgrow the panel; the characters
  your filter matched are drawn in the accent colour.
- **The footer** teaches the focused row's keys on the left and carries the
  create and cancel buttons on the right.

Text that comes from elsewhere, such as an issue title, a space's name, a
directory or an error message, is drawn with its control characters
removed and its line breaks shown as spaces, so it cannot disturb the
screen.

The popup is a fixed 104×32 cells. herdr shrinks it to fit a smaller
terminal and never grows it, so on an 80×24 terminal the popup fills the
screen.

## The rows

There are up to nine rows. Two appear only with their integration: `issue`
needs Linear, and `account` needs clauth with at least two profiles. Without
either, the form has seven rows, and that is the shape most people see.

| Row | Reads | When it cannot be set |
|---|---|---|
| `issue` | `ENG-101 · fix login redirect loop`, or `none` | `unavailable` and the reason |
| `title` | what you typed, or a dim `untitled` | — |
| `prompt` | the first line, a dim ` +N more`, and ` · reap when done` when the prompt will end with pane-reaper's instruction | — |
| `project` | the path, with `~` for your home directory | `invalid`, `not a repository`, `check timed out` |
| `worktree` | `on · <branch> ← <base>`, `on · from <base>` before there is a title, or `off` | `not a git repository` |
| `placement` | `new space`, `tab here`, `split here` or `tab in <space>` | `the worktree's own space`, whenever the worktree is on |
| `agent` | `claude`, or another agent kind | — |
| `options` | `opus · effort xhigh · plan mode`, or `claude's own settings` | `none for <kind>`, for an agent with no options |
| `account` | `active · max · 5h 12% · 7d 40%`, or the pinned profile | `account pinning only applies to claude` |

### While something is still loading

The form draws immediately — before your Linear key has resolved, before
clauth has been asked for its profiles, and before a configured account
picker has been checked. None of that holds up the screen, and none of it
holds up a create either, with one exception noted below.

A row says so in one of two ways. **If there is something to pick, the row
keeps showing it** and the panel's bottom line names what is still coming:

| Row | Panel says | While |
|---|---|---|
| `issue` | `waiting for api_key_cmd…` | your credential helper runs — it may be waiting for you to approve a read |
| `issue` | `fetching assigned issues…` | the issue list loads |
| `account` | `reading clauth profiles…` | `clauth status --json` runs, because clauth's status file was out of date |

**If there is nothing to pick, the row itself reads `loading…`** in dim —
an `issue` row with no cached list from a previous run, and the `account`
row, which has no profiles until clauth answers. A `loading…` `account` row
is skipped by `⇥`: there is nothing there to choose or retry yet.

The one wait that can hold a create is the account. `⌃S` while clauth is
still being read waits up to five seconds and then goes ahead anyway, under
whatever your `config.toml` names. `⌃S` resting on `auto` while the picker
is still being checked waits the same five seconds and then *refuses*,
rather than launch under an account nobody chose.

### issue

Pick one of your Linear issues to seed the session. The panel lists the
issues assigned to you, with their state and estimate, and filters as you
type. Choosing one fills in the title, the branch (from the issue's own
branch name) and the prompt, unless you have already typed over them. `none`
at the top clears the choice.

The row appears whenever a Linear key *source* is configured — whether or
not a key resolves from it. A cached list from a previous run is pickable
straight away, while the key resolves and while the list refreshes, and
stays pickable if either fails, with the reason on the panel. See
[`[linear]`](configuration.md#linear) for setting one up and for what
`unavailable` means.

![The issue row focused, with the Linear issue picker open in the
panel](images/issue-picker.svg)

### title

The session's name: the label of its space and tab, and, with a worktree,
the source of its branch name. It holds up to 32 characters. The panel says
what the title will produce (`branch will be you/fix-flaky-checkout-test`)
and lists the sessions that were already open when the popup opened.

A title that collides with something is flagged before you create:
`branch exists` when the worktree's branch is already there,
`label in use` when an open space already has that name, and
`branch & label in use` for both. A duplicate stops the create, because herdr
would check out the existing branch instead of making a new one. If git
does not answer within five seconds, the panel reads `couldn't check`, and
the create is refused until it does or you change the title: nothing is
created on a guess.

### prompt

The first message the agent receives. `↵` here creates the session; use
`⌃J` (or `⇧↵` or `⌥↵`, where your terminal sends them) for a new line.
`⌃←` `⌃→` (or `⌥←` `⌥→`) move by word, and `⌃⌫` `⌃⌦` delete one. The row
shows the first line and how many more there are.

Above the text box, `keep · reap` decides whether the prompt ends with a
sentence asking the agent to mark its pane ready for pane-reaper once its
work is saved. `⌃X` or a click flips it. It is never added to an empty
prompt or to a slash command. See
[`[reaper]`](configuration.md#reaper) for what that sentence does.

### project

The directory the session is created in. The row has two modes, and switches
between them on what you type:

- **Search** (anything not starting with `/`, `~` or `.`) filters a fixed
  list: the current space's repository first, then the current space's
  directory, then every open space's repository, then directories you have
  used recently. Opened from a linked worktree's own space, the list starts
  with that worktree's checkout, with the main repository next.
- **Browse** (starting with `/`, `~` or `.`) lists the subdirectories of
  the directory you have typed so far. `⇥` completes to the longest common
  prefix, the way a shell does, and stops at an exact directory rather than
  entering it; type `/` to go in. The path you typed is always the last
  entry, so a directory that does not exist yet stays selectable.

A directory that is not a git repository is allowed. The row says
`not a repository` and the `worktree` row turns off, and that is the only
consequence.

Changing the project re-reads that repository: its branches, its
[`.herdr-draft.toml`](configuration.md#repository-defaults-herdr-drafttoml),
and what you last chose there. Anything the repository's file refused is
listed in this row's panel.

**The first time the form lands on a repository, it runs `git fetch --prune`
there in the background**, once per repository each time the popup opens,
so the base list includes branches pushed since you last fetched. It
contacts the repository's default remote (the current branch's upstream, or
`origin`) with the credentials git would normally use, including your own
`core.sshCommand` or `GIT_SSH`. It runs with password prompts and ssh
questions turned off and gives up after 30 seconds, so a remote that wants a
password fails quietly instead of prompting over the form.

### worktree

Whether the session gets its own git worktree, and on which branch. The
panel has three parts: `off · on`, the **branch**, and the **base** it
starts from.

- **The branch** is derived from the title and your
  [`branch_prefix`](configuration.md#top-level) until you edit it yourself. A
  name git cannot use is refused on the line under it, and an emptied branch
  is refused when you create.
- **The base** list starts with `HEAD` (the commit the project is on), then
  the 50 most recently committed branches. A branch that only the remote has
  is listed as `origin/<name>`, the name git knows it by. `↵` chooses the
  base under the cursor.

A worktree session runs in the worktree's own space, which herdr nests
under the repository's space in the sidebar.

Its branch does not track the remote branch it was cut from. With git's
default settings, a worktree cut from `origin/develop` would take
`origin/develop` as its upstream, and a plain `git push` from the session
would be refused or would push onto `develop` itself. herdr-draft removes
that upstream once the worktree exists; the branch gets its own on its first
`git push -u`. If you set git's `branch.autoSetupMerge = always`, it is left
as you asked.

### placement

Where a session **without** a worktree goes:

- `new space`: a new space of its own.
- `tab here`: a new tab next to the pane you opened the popup from.
- `split here`: a new pane beside that pane.
- `tab in <space>`: a new tab in the space already open on this project.
  This is the default whenever such a space exists, so a repository's space
  collects its sessions as tabs.

With the worktree on, the row reads `the worktree's own space` and cannot be
changed. It keeps your choice for when you turn the worktree off.

### agent

Which agent to start. Your [favorites](configuration.md#agents) are chips at
the top of the panel (`←` `→`), and every kind herdr knows is listed under
them (`↑` `↓`).

### options

The agent's model, effort and permission mode for this session. The panel
has one line of chips per option:

- **model**: `fable`, `opus`, `sonnet`, `haiku`, or `other` to type a full
  model id such as `claude-opus-5[1m]`.
- **effort**: `low`, `medium`, `high`, `xhigh`, `max`.
- **mode**: `manual`, `plan`, `accept-edits` (`acceptEdits` in
  `config.toml` and on the command line), `auto`.

The first chip on each line sends no flag at all, and is labelled with what
the session gets instead. When your
[`[agents.extra_args]`](configuration.md#agents) passes that flag, the chip
shows the value it passes, with a dim `•`. Otherwise it reads `claude's`:
Claude's own settings decide. With
`claude = ["--model", "claude-opus-5[1m]", "--effort", "xhigh"]` in
`[agents.extra_args]`, the panel reads:

```
▸ model      claude-opus-5[1m]• · fable · opus · sonnet · haiku · other
  effort     xhigh• · low · medium · high · xhigh · max
  mode       claude's · manual · plan · accept-edits · auto
  sends no --model of its own; [agents.extra_args] passes claude-opus-5[1m]
  [agents.extra_args] adds: --model claude-opus-5[1m] --effort xhigh
```

Pick another chip to send that value instead; it replaces the flag
`[agents.extra_args]` would have passed. The line under the chips says what
the option under the cursor sends (`sends --effort high`, or
`sends no --effort, so claude's own settings decide`), and
`from config.toml` marks a value that is your configured default.

The row sums it up: `opus · effort xhigh · plan mode` for what you chose, a
dim value for one `[agents.extra_args]` passes, and `claude's own settings`
when nothing is set anywhere.

Only `claude` has options today. For any other agent the row reads
`none for <kind>` and focus skips it. Set defaults per agent in
[`[agents.options.<kind>]`](configuration.md#agentsoptionskind). Options are
never remembered between sessions.

![The options row focused, with a line of chips each for model, effort and
permission mode](images/options.svg)

### account

Which Claude account the session runs under, when
[clauth](https://github.com/uwuclxdy/clauth) manages more than one. The row
starts on `active`, which pins nothing and uses whichever profile clauth has
live. The panel lists every profile with its plan and how much of its
five-hour and seven-day usage is spent; `↵` pins the one under the cursor.

With an [account picker](account-picker.md#the-account-picker-protocol)
configured, an `auto` entry lets the picker choose per project, and shows
what it would choose (`auto → alpha`).

![The account row focused, listing two clauth profiles with their usage
gauges](images/account.svg)

The row appears with clauth on `PATH` and at least two profiles — or while
clauth is still being asked, which is the `loading…` state above. If it
then reports fewer than two profiles, the row stays and says so rather than
disappearing from under you. See [Claude accounts](account-picker.md) for
what each state on it means.

## Keys

| Key | What it does |
|---|---|
| `⇥` / `⇧⇥` | next / previous row. The ring wraps: `⇧⇥` from the first row reaches the create button |
| `↑` `↓` | move within the focused row's panel |
| `←` `→` | chips: worktree on or off, placement, agent favorites, an option's value |
| `⇥` in a list | complete, then move to the next row |
| `↵` | create, from a named title, from the prompt, or on the create button. Pin the profile under the cursor on the `account` row, and choose the base under the cursor in the base list. Move to the next row everywhere else |
| `⌃S` | create, from anywhere |
| `⌃J` (or `⇧↵`, `⌥↵`) | new line in the prompt |
| `⌃X` | in the prompt: switch between `keep` and `reap` |
| `⌃R` `⌃R` | clear back to the resolved defaults, including the repository's `.herdr-draft.toml` |
| `esc` / `⌃C` | cancel |

The first row is `issue` when Linear is set up, and `title` otherwise, so
`⇧⇥` from the title reaches `issue` only when that row exists and is
available.

The create button is the last stop in the ring, reached by `⇥` past the last
row. It names the key that creates from where you are: `↵ create` on a named
title, in the prompt and on the button itself, `⌃S create` everywhere else,
and no key at all while the title is empty, since nothing creates until you
name the session.

The mouse works too: click a row to focus it, click a line in the panel to
select it, and scroll the panel with the wheel.

## Creating the session

Once you create, the rows give way to the steps the session takes: the
worktree, the space or tab, the agent, and the prompt. Each is ticked off as
it finishes. A successful create closes the popup by itself.

**A pause for a dialog.** Claude Code asks whether you trust a folder the
first time it runs in one, and every new worktree is a folder it has never
seen. When that happens the step shows `…` and the footer reads
`answer the dialog in the pane`. herdr has already moved you to that pane:
answer it there, and the popup sends your prompt and closes. You press
nothing in the popup. It waits up to
[`trust_wait_ms`](configuration.md#timeouts), five minutes by default.

**A warning.** If a step finished but something about it needs your
attention, such as a branch whose upstream could not be removed, the step is
marked `!`, and the popup stays open with the full message until you close
it with `esc` or `↵`.

**A failure.** A step that fails is marked `✗` with herdr's reason, and the
popup offers what to do with whatever the create already made:

- `k` keeps it, so you can look at it or finish it by hand.
- `c` removes it, and the line above the buttons says exactly what goes: the
  tab, the pane, or the worktree with its space and its branch. The branch is
  deleted only when this create made it and it holds no commits of its own;
  otherwise it is kept, and the popup says so.
- When the very first step failed, `esc` is the only key. The screen says
  nothing was created when herdr refused before acting, and otherwise names
  what may have been left behind, for you to look at before retrying.

A prompt that could not be delivered is never lost: it is saved to a file
the failure screen names. See
[Troubleshooting](troubleshooting.md#prompt-not-sent-after-a-submit) for
what "prompt not sent" and "delivery unconfirmed" mean and what to do about
each.
