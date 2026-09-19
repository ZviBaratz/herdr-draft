# Changelog

Notable changes to herdr-draft, newest first. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the versions
are [semantic](https://semver.org/spec/v2.0.0.html).

Four things carry the version and move together: `herdr-plugin.toml`'s
`version`, `herdrc.Version`, this file's newest release heading, and the
`v`-prefixed git tag. The first three are held to each other by tests; the
tag is the one no test can reach. See
[CONTRIBUTING.md](CONTRIBUTING.md#releases) for the order they move in, and
for why a heading here may read `unreleased`.

## 0.1.0 — unreleased

First release. There is no predecessor to diff against, so this entry says
what 0.1.0 *is* rather than what changed.

herdr-draft is a [herdr](https://github.com/herdrdev/herdr) plugin: a
single-screen dialog, opened in a herdr popup, that creates a fully
configured agent session in one submit. The same binary also creates one
without the popup. It drives herdr exclusively through the public CLI
(`$HERDR_BIN_PATH`), never the raw socket API.

### The dialog

- **Nine rows, one line each**, in a fixed order — `issue`, `title`,
  `prompt`, `project`, `worktree`, `placement`, `agent`, `options`,
  `account` — over a single detail panel belonging to the focused row. A row states what the
  session will be rather than which knob is set, and its position does not
  move with focus or window height.
- `issue` is present only when Linear is configured, and `account` only when
  clauth has at least two profiles. With neither, the form is seven rows.
- **`placement` offers `tab in <space>` and defaults to it** whenever the
  project's checkout already has a workspace open (#128), so a repository's
  own space collects its sessions as tabs instead of a new top-level space
  appearing per task. `new space` stays for a repository with none.
- **A worktree session always runs in the worktree's own space**, which
  herdr's sidebar nests under the repository's. While the worktree is on,
  the `placement` row is inert and reads `the worktree's own space`. It
  keeps its value for when the worktree is turned off. Placing a worktree
  session's agent elsewhere left the worktree's space holding only an idle
  shell, and once `tab in` became the default it did so for every worktree
  session after the first in a repository.
- **Opened in a linked worktree's own space, the form defaults to that
  checkout** (#171): another session's worktree, say, with the repository's
  root as the next candidate. It used to open on the repository root, so a
  worktree from there was cut from the primary checkout's commit rather
  than the one you were on, and a session without one ran in the primary
  checkout, where `create` run in the same place runs in the lane.
- **Every tab a session opens carries its title**, from the popup and from
  `create` alike. herdr's `workspace create` and `worktree create` label
  only the space, which left its first tab called `1`, so a `new space` or
  worktree session now renames that tab. The rename is cosmetic, and a
  failed one never fails the create. `create` reports the step on stderr as
  `failed, continuing` and the run goes on. The popup marks the row `!`,
  but a successful submit still closes the popup at once, so the row stays
  on screen only when a later step fails. `split here` renames nothing,
  because the tab it lands in is yours.
- **The `options` row chooses the agent's model, effort and permission
  mode** for this session, which `[agents.extra_args]` could only fix for
  every session of a kind. It reads `opus · effort xhigh · plan mode`, and
  its panel has one chip line per option, each starting at `inherit` (send
  no flag). `other` takes a typed model id such as `claude-opus-5[1m]`. The
  options are **declared per agent kind** rather than shaped around Claude,
  so a kind that declares none — every kind but `claude`, today — costs one
  dim `none for <kind>` line and no focus stop. `bypassPermissions` and
  `dontAsk` are not offered. The panel also shows what
  `[agents.extra_args]` appends, which was never on screen before. This
  reverses the v1 spec's decision to leave these fields out; the
  agent-options spec says why.
- **A selected Linear issue seeds title, branch and prompt**, unless you
  have already typed over them.
- **The prompt panel can end the prompt with pane-reaper's instruction.**
  A `keep · reap` line over the textarea, flipped with `⌃X` or a click,
  asks the agent to mark its own pane ready once its work is saved, so the
  pane-reaper plugin can close it. Off by default. It is never added to an
  empty prompt or a slash command, and the row reads `· reap when done`
  only when it will really be sent.
- Candidate lists are tables: aligned columns, a right-flush status word, a
  live match count, a scrollbar once the list outgrows the window, and the
  matched run drawn in the accent colour.
- The `title` panel shows the sessions that were open when the form opened
  and marks the one a colliding title would collide with.
- The `worktree` panel is a three-part `off · on` / `branch` / `base`
  editor, so a form with a worktree is the same height as one without.
- **The base list offers a branch only the remote has as `origin/<name>`**
  (#198), the name git resolves as written; one you also have locally is
  still one row, under its local name. It used to offer the bare name,
  which is no ref in a fresh clone, where every branch but the default one
  is remote-only. git 2.53 handed that name as a worktree's base checks out
  a new local branch of the same name instead of the one asked for, and
  the create reported success. The list also no longer offers `origin`,
  which is the remote's `HEAD` rather than a branch (#148), or a detached
  `HEAD`'s `(HEAD detached at …)` line.
- **A session's branch no longer tracks the remote branch it was cut
  from** (#221), from the popup or from `create --base origin/<name>`.
  herdr cuts the worktree without `--no-track`, so under git's default
  `branch.autoSetupMerge` the new branch took its base as its upstream: a
  plain `git push` from the session was refused under
  `push.default=simple`, and under `push.default=upstream` it pushed the
  session's commits onto the shared branch. The upstream is now removed
  once the worktree exists — only on a branch the create made, only when
  it is the remote-tracking branch the base names, not when the branch has
  the same name as that remote branch (#229), and not when
  `branch.autoSetupMerge` is `always`. The branch then tracks nothing until
  its first `git push -u` (or `push.autoSetupRemote`). A removal that fails
  does not fail the create: `create`'s worktree line reports it, with the
  command that finishes it; the popup's row shows it only until a
  successful submit closes the popup.
- **Create is a real focus stop**, the ring's last, on the footer rather
  than in the row stack. `⌃S` submits from anywhere; `⌃R ⌃R` clears back to
  the resolved defaults.
- **A submit waits for the project row's check** (#195). The row's path is
  checked 150 ms after the last edit: whether it exists, whether it is a
  repository or a linked worktree, its `.herdr-draft.toml` and its
  remembered choices. A `⌃S` sent sooner was built from the previous
  project's answers, so a worktree could be sent to a repository the form
  had already left. It now waits for the answer about the path the row
  holds — the one you end on, if you keep typing in the row meanwhile —
  and for the check of the base that project remembers, when it remembers
  one (#194), then validates and goes on by itself. It waits for the title's
  duplicate check the same way (#137), so a duplicate title typed and
  submitted at once no longer gets past the refusal, and a duplicate fixed
  at once is no longer refused with no warning on screen.
- **A submit that has passed validation builds what validation saw**
  (#136). When it must first pick an `auto` account or read a lane's commit,
  the form is frozen until it has: a second `⌃S` used to spend a second
  pick, a `⌃R ⌃R` had the pick submit the rebuilt form, and an edit reached
  the session unchecked. `esc` and the Cancel button still cancel.
- **A branch git cannot hold is refused where the branch is shown** (#199).
  The `worktree` panel names what is wrong with it on the line under the
  branch, and a submit lands the cursor in the branch input to fix it. The
  duplicate check could not see these names, because git calls a branch it
  could never hold "absent". So `zvi/old ` got past it, and herdr, which
  trims the name, started the session on your existing `zvi/old`. The same
  goes for whitespace git would take but herdr trims, such as a trailing
  no-break space. Other invalid names failed inside herdr, at the create's
  first step. A branch derived from the title is always one git can hold.
- Mouse: click a row to focus it, click a panel line to select it, wheel to
  scroll the panel.
- The popup is a fixed **104×32 cells**, a manifest value rather than a
  proportion of the terminal. herdr clamps it down to the terminal and
  never up.

### Headless `create`

- `herdr-draft create` builds the same session without the popup, for
  scripts and for agents already running inside one.
- Flags mirror the form's fields: `--project`, `--title`, `--prompt` (`-`
  reads stdin), `--branch`, `--base`, `--worktree` / `--no-worktree`,
  `--reap` / `--no-reap`, `--placement`, `--workspace`, `--agent`,
  `--model`, `--effort`, `--permission-mode`, `--account`, `--issue`,
  `--json`, `--dry-run`, `--on-failure keep|clean`.
- **`--model`, `--effort` and `--permission-mode`** are the `options` row's
  flags, registered from the same per-kind declaration. `inherit` clears a
  configured default for one session. An option the agent kind does not
  declare, like `--effort` with `--agent codex`, is refused rather than
  ignored. `--json` reports the chosen options as `agent_options`, and as
  `launch_options` what the agent's command line actually carried for
  each, including what `[agents.extra_args]` passed for an option left on
  `inherit`. For those, provenance says `extra_args`, not `built-in`
  (#209).
- **`--dry-run` shows what a create would make, and makes nothing.** It
  runs the whole pre-flight, with the real run's exit 2 and 3, then
  reports and stops. It creates nothing and remembers nothing, and an
  `auto` account goes to the picker's own `--dry-run`, so no pick is spent.
  Under `--json` it prints the create's object with `dry_run: true` and no
  ids (#209).
- **`--placement tab-in` and `--workspace <id>`** place the agent's tab in a
  workspace other than the invoking one — the one already holding the
  project (the default when there is one), or any open workspace by id —
  with none of the three pane variables required (#128). They need
  `--no-worktree`: with a worktree, `--workspace` and any `--placement` but
  `new-space` are refused, and a remembered placement does not apply.
- **Anything left unset resolves exactly as the form resolves it** — one
  resolver, and a test drives a real form and a real `create` request over
  the same files to keep the two from drifting.
- **A title is kept to what the form's `title` row holds**, whether it
  came from `--title` or from `--issue`'s Linear issue: a tab, CR or LF
  becomes a space, other control characters and invalid UTF-8 are dropped,
  and it is cut to 32 runes. A line on stderr gives the title used. Before, one title could
  build two different sessions: the space and tab labels and a
  title-derived branch differed from the popup's, and so could the agent's
  name (#176, #178).
- **Run inside a linked worktree, it can create a worktree** (#171).
  `--worktree`, and a create with no worktree flag at all, handed herdr the
  linked checkout as the source, which herdr refuses, so nothing was
  created. That was the spawn skill's first example, run by an agent in any
  worktree lane. The new worktree is now created from the repository's
  primary checkout, with its base resolved in the lane, so an unset base,
  or `HEAD`, is the commit the lane is on. With no base chosen, `--json`
  reports that commit as `base`, with provenance `checkout`, and it is
  never remembered. The project stays the lane, so `--no-worktree` still
  runs there. The popup does the same, from a lane's space or with one
  picked in the `project` row.
- **`--base HEAD` and `--base @` mean no base**, as the form's `HEAD` row
  does, and so does any other spelling of HEAD itself (`HEAD^0`). Their
  provenance stays `flag`. `--json` omits `base`, or from a lane reports
  the lane's commit, as it does for no base at all. With a worktree, a
  `--base` that names no commit is refused before anything is created
  (exit `2`) rather than reaching herdr and failing at the worktree step
  (#194).
- Progress goes to stderr one line per step; the result to stdout, or a
  single JSON object under `--json` carrying a `provenance` map naming the
  tier each value came from.
- Exit codes: `0` created, `1` the plan started and failed, and part of
  the session may exist (`--on-failure` applied), `2` bad usage or an
  unresolvable request, `3` herdr unreachable, `4` the plan started and its
  first step failed before anything existed (#192). `4` is given only with
  evidence: herdr refused the step before acting, or was never asked. A
  worktree step herdr fails after git has run is `1`, and so is any other
  first-step failure that shows neither: `create` no longer says "nothing
  was created" about one, and says instead what it may have left. With no
  space reported, `--on-failure clean` has nothing to remove, and
  `clean_refused` says so. The popup's failure screen follows the same
  rule (#208). When the first step fails, `esc close` is its only key,
  and it says "nothing was created" only on the same evidence. Otherwise
  it says what may be left, as `create` does.
- **It refuses what the form refuses**, before anything is created (exit
  `2`): a branch that already exists when a worktree would create it, a
  title an open workspace already carries, and a pinned account clauth
  reports as signed out (#147). The branch check matters most: herdr checks
  an existing branch out rather than refusing it, and ignores `--base` when
  it does. `--account auto`'s real pick runs after every other check, so a
  refused request spends no pick (#145), and `--account active` means no
  pin, as it does in `config.toml` (#146). A branch git cannot hold, from
  `--branch` or the Linear issue, is refused the same way and names where it
  came from (#199). That check runs before herdr is asked anything.
- **Cleaning a failed worktree session deletes the branch it made** (#173),
  so a retry with the same title is no longer refused over a branch the
  failure left behind; herdr's `worktree remove` keeps branches by design.
  The branch goes only if this run made it — checked just before the
  create, since herdr checks an existing branch out and answers the same
  — only if it holds no commit its base lacks, counted again just before
  anything is removed, and only if, once the checkout is gone, nothing
  else is left holding a commit of its own: the agent keeps running while
  herdr removes the checkout. `--json` reports `deleted_branch` with its
  tip, or `kept_branch` and why. The popup's remove does the same, and its
  line says what remove deletes rather than "everything this create made",
  which was never true of the repository workspace herdr opens beside a
  first worktree.
- **A prompt has three fates, not two**: `prompt_status` is `sent`,
  `unsent` or `unconfirmed`. The third is `agent prompt --wait` giving up
  before the agent's status changed, which is not proof the prompt failed
  to arrive — so the text comes back as `unconfirmed_prompt` rather than
  `unsent_prompt`, and `--on-failure clean` is refused, because the session
  may have an agent working in it right now. It is also every other
  failure after herdr has accepted the send, including a first send that
  the read straight afterwards finds did not land, or that herdr's own
  wait reports the agent gone after (#154): `unsent` means herdr never
  took the text.
- **The reported ids name the agent, not the space.** `--json` carries the
  space's triple alongside the agent's. The two differ when herdr answers a
  worktree create with a workspace that was already open, and the agent is
  given a fresh tab in it instead of whatever pane herdr handed back.
- `create` reads the plugin environment only when `HERDR_PLUGIN_ID` says it
  is ours, so a shell inside another plugin's pane cannot have its config
  read as this plugin's or its state written into.
- **`herdr-draft skill` prints an agent skill for driving all of the
  above.** Redirect it into `~/.claude/skills/spawn/SKILL.md` — the README
  has the recipe, including the `herdr plugin list --json` lookup that
  finds the binary, which is not on `PATH` (#167) — and
  an agent asked to hand work off creates a session instead of writing a
  handoff document and stopping. It carries the `HERDR_PLUGIN_*` exports
  and why they must share one command, the placement choice, the prompt,
  and how to read `prompt_status` and the pane afterwards. Unless the user
  names them, the agent chooses the model, effort and permission mode for
  the task and passes them, never a mode more permissive than its own. It
  dry-runs the command, and the confirmation it asks for shows each choice
  with its reason and what the session will run with (#209). It also carries
  the absolute path of the binary that printed it, which is why the emitted
  file is machine-specific and is regenerated after an upgrade.

### Where values come from

- Five tiers, applied lowest-first so the last writer wins: built-in →
  `config.toml` → `last-used.json` → `.herdr-draft.toml` →
  `projects.json`. A team's committed default beats what you last did in
  some *other* repository and loses to what you last did in this one.
- One more tier is a fact about the machine, not a file: `herdr workspace
  list`. It decides placement only, for a session without a worktree:
  `new-space` from the three memory and user-config tiers yields to
  `tab-in` when the project's space is open, a remembered `tab-in` falls
  back once it is gone, and a `here` placement, a `.herdr-draft.toml`, and
  `--placement` all stand.
- Per-project memory re-applies when you change the project row, for every
  field you have not touched yourself, and is keyed by the **git repository
  root** so a linked worktree and its origin share one memory.
- **A remembered or configured base is kept, mapped or reported, the same
  on both paths** (#194). One that names a commit is kept, and the form's
  base list offers it right after the `HEAD` row even when it is not among
  the 50 most recently committed branches the list holds. `HEAD` and `@`
  are the `HEAD` row, whose value is no base at all, and so is any other
  spelling that names HEAD's own commit (`HEAD^0`, `@{0}`). One that names no
  commit — a branch deleted since, or one you have only on a remote under
  its bare name — falls back to `HEAD`, and the worktree panel, or a
  `herdr-draft create:` line on stderr, says which base was dropped.
  Before, the form fell back to `HEAD` silently for any base its list did
  not name, while `create` passed the same base to herdr as it was, so one
  project built two different sessions and wrote two different bases back
  to `projects.json`.
- The resolver is pure and is the only place the precedence chain exists.

### Repository-level config (`.herdr-draft.toml`)

- A repository can commit creation defaults so a team shares them:
  `branch_prefix`, `default_worktree`, `default_placement`, `default_base`,
  `linear_branch_name`.
- **That five-key allow-list is a trust boundary, not a convenience.** The
  file arrives with `git clone`, so it may only choose among values you
  could already have picked in the form yourself, and may never name a
  command, a path outside the repository, or a credential. Anything
  rejected is reported rather than silently dropped. `[reaper]` is refused
  with its own reason: it would add an instruction to your agent's prompt.
  So is `[agents.options]`: it becomes part of a launched agent's command
  line.
- Read from the *origin* repository root and re-read when the project row
  changes.

### Configuration and appearance

- `config.toml`, all keys optional and unknown keys ignored, with
  `[linear]`, `[clauth]`, `[agents]`, `[timeouts]`, `[worktree]`, `[reaper]`
  and `[palette]` sections.
- `[worktree] trust_repository` is deliberately `config.toml`-only: it
  waives git's ownership check for one request, so a cloned repository must
  not be able to assert its own trustworthiness.
- `[agents.options.<kind>]` sets a kind's default `model`, `effort` and
  `permission_mode` for the popup and `create` alike. It is
  `config.toml`-only and never remembered from a submit, for `mark_ready`'s
  reason: how hard a session should think, or whether it should plan
  first, describes the task rather than the repository. A value the kind
  does not offer, of any type, is reported and skipped rather than refusing
  to open. An option you set replaces the same flag in
  `[agents.extra_args]` instead of following it.
- `[reaper] mark_ready` turns the prompt's `reap` on by default for the
  popup and `create` alike. It is `config.toml`-only, and never remembered
  from a submit: one reaped session must not make the next one reap, and
  orchestrator sessions must never close themselves.
- An optional **account picker protocol**: herdr-draft can hand "which Claude
  account should this session use?" to any executable implementing a small
  documented contract (`--dir <path> --json [--strict] [--dry-run]`, one JSON
  object out, six documented exit codes). It ships no picker and hard-codes
  none, which is what keeps this feature from making a public plugin depend
  on one machine's private toolchain. `[clauth] picker` names one —
  explicitly; a picker is never discovered on `PATH` — and it is probed once
  before it is trusted. Every call is bounded by a 30-second deadline, a
  refusal is always shown rather than silently downgraded to an unpinned
  launch, and an exit code outside the documented set is reported as a
  malfunction rather than obeyed as a decision. With it configured, the
  `account` row grows an `auto` selection and `create` accepts
  `--account auto`. The picker's `warnings` and `machine` figures (load,
  cpus, swap) are shown on the `account` panel, with an unmeasured figure
  written as such rather than as zero, and `create` prints the warnings to
  stderr; neither ever refuses a session (#165).
- `[clauth] launch` selects how a pinned account is launched, defaulting to
  `clauth start <profile> --`. The opt-in `"wrapper"` mode types
  `CLAUDE_CONFIG_DIR=<dir> claude` instead, which is only worth setting on a
  machine whose shell defines `claude` as a function; everywhere else it
  silently means less, which is why it is not the default.

  It pairs with `[clauth] launcher` as **one decision**: `launch` picks the
  mechanism and `launcher` configures the argv one. The two cannot be a single
  key — `launcher` conveys the account as a word in a command line and
  requires an `{account}` placeholder, while wrapper mode conveys it as a
  credential directory in the environment and names the account nowhere in its
  argv, and a quoted `NAME=value` is not a shell assignment, so the
  environment half cannot travel inside the template.

  `launcher` takes exactly one `{account}`, standing for the profile name;
  any other shape falls back to the default rather than launching on an
  unintended account. The reason to set it at all is that `clauth start`
  costs the session its herdmates team lead, so a wrapper of your own that
  keeps both — `["claude-as", "{account}"]` — goes here.

  Only one mechanism is ever in effect: a `launcher` set alongside
  `launch = "wrapper"` is ignored. That, a malformed `launcher` and an
  unrecognised `launch` are each reported, on the `account` row's panel in
  the popup and on stderr by `herdr-draft create`; a malformed `launcher`
  beside `"wrapper"` is reported as both malformed and ignored. The popup
  also reports the launch it performed, including when wrapper mode fell
  back for want of a `config_dir`.
- `[palette]` overrides individual theme fields when herdr's own theme
  cannot be resolved from a static config file. Every region that marks
  something — rules, the focused row's fill, input backgrounds — has a
  **measured** contrast floor against the ground it is actually drawn on,
  over all eighteen builtin herdr themes.

### Robustness

- **Degradation over refusal.** Only an unparseable plugin context, an
  unreachable herdr or a broken `config.toml` refuse to open. A broken
  Linear key or an unloadable clauth degrades that one row to "unavailable,
  with a reason".
- **A report is not evidence; the pane is.** Neither direction of the
  prompt-delivery question is answered by what herdr reports: a wait that
  times out has not proved a failure, and an agent's `idle` status has not
  proved one either way. Where the answer cannot be known, the output says
  so and names the pane to look at, rather than asserting the tidier
  answer.
- **A project directory means that repository, whatever the environment
  says.** git lets `GIT_DIR`, `GIT_WORK_TREE` and the rest of its
  local-repository variables override the directory a command runs in, and
  a git hook exports them, as does `git rebase --exec` in a linked worktree.
  So a `create` run from either used to ask *another* repository whether
  the project was a repository, which branches it had, where its root was,
  and whether a failed session's worktree was safe to remove. herdr-draft
  now clears exactly the variables git itself clears before acting on a
  different repository (`git rev-parse --local-env-vars`). `GIT_SSH`,
  `GIT_SSH_COMMAND` and your other git settings still apply.
- **A failed worktree is judged against a commit, however its base was
  spelled.** The clean counts the checkout's commits from the commit it
  was cut from, recorded just before the create (#173). When that could not
  be read, the clean resolves the base itself, in the repository the
  worktree was cut from, and never inside the checkout: there `HEAD` or `@`
  names the worktree's own tip, so a checkout holding commits read as
  holding none and was removed, and `HEAD~1` names its own parent, so a
  pristine one read as a commit ahead and was kept (#193). A create whose
  reply named no checkout is kept too, rather than judged by whichever
  repository `create` was run from.
- **A worktree on a branch it did not ask for fails the create** (#198).
  herdr's reply names the branch git checked out, and one that names
  another branch than the request fails the worktree step, naming both.
  herdr has made the checkout by then, so the popup offers keep or remove
  and `--on-failure` applies, and the remove keeps the branch git made. A
  reply with no branch, which is how herdr describes a detached checkout,
  is not held against the request.
- **herdr's errors are read by their code, never by their text** (#144). A
  failed herdr call's message carries its command line, and so the prompt,
  title or branch you typed. The busy-pane retry, the taken-name suffix and
  the blocked-launch explanation used to find herdr's codes anywhere in that
  message, so a prompt that merely mentioned `agent_pane_busy` could be sent
  again into an agent already working on it, and a branch naming it could
  re-run a worktree step whose checkout then dropped out of the keep-or-remove
  offer. Each now matches only the code in herdr's own error reply.
- **Screen detection is evidence-based.** Before sending a queued prompt
  the executor reads the pane and checks it against known blocking-dialog
  signatures rather than trusting an "idle" report, and turns herdr's
  "blocked during startup" into an instruction the user can act on.
- **A pane with nothing on it is not a ready one, and a send that reported
  success is not a delivery.** The read before a prompt has to come back
  with something on it: a screen that has not painted matches no dialog
  signature, and treating that as safe is how a prompt's own Enter used to
  answer a trust dialog that drew a moment later — killing the agent while
  the run reported a clean create. A pane can also carry a shell prompt and
  nothing else for a second or so before an agent's own screen appears,
  which no signature will ever match, so the pane is read again *after* the
  send: a create is only called clean once the pane says the prompt landed.
  An agent that has stopped answering, or a dialog still up with no trace
  of the prompt on it, is a failure with the prompt saved, and its delivery
  is **unconfirmed**, not unsent: the text and its Enter went into the pane,
  so the clean is refused and both the popup and `create` say to read the
  pane first (#154). This check never resends: text has already gone out,
  so a second copy is the injury it exists to report. The one failure that does get another send is a
  *stall* — see below — and it is the only one.
- **A first-run trust dialog is a pause, not a failure.** Every worktree is
  a directory the agent has never been trusted in, so its first launch there
  meets a confirmation screen and herdr refuses to call the launch ready.
  The popup waits through it — the launch row shows `…`, the footer says
  what to answer and where, and the pipeline delivers the queued prompt and
  closes by itself once the dialog clears. No keypress in the popup. How
  long it waits is `[timeouts] trust_wait_ms`, five minutes by default; when
  that runs out, or when the agent stops running because the dialog was
  declined, it falls back to the failure screen with the prompt saved.
  Headless `create` deliberately keeps failing fast, because a script has
  nobody at the keyboard. That budget covers waiting on a *person* and
  nothing else.

  The wait covers the dialog wherever it stops the run. herdr may recognise
  the screen and refuse the launch, or it may call the agent ready and leave
  the dialog for herdr-draft's own prompt guard to catch one step later;
  live, the second is what the popup meets. The launch-step wait polls
  herdr's status, the prompt-step wait polls the pane's own screen — herdr
  being wrong about that pane is the reason the second wait exists, so it
  does not ask herdr again.

  A prompt that herdr reports as *stalled* — it saw the agent do nothing at
  all — is sent once more after a short pause, through the same dialog
  guard. That is the one prompt failure which is positive evidence nothing
  was *processed*, and for about a second after a trust dialog clears it is
  the ordinary outcome: herdr calls the agent ready while its interface is
  still coming up. The pause waits for a terminal to paint rather than for
  a person, so this retry is not on `trust_wait_ms` and both the popup and
  headless `create` take it. It is deliberately not the same as a prompt
  whose confirmation merely timed out, which means the agent *was* working
  and is never resent.

  A stall that survives the retry is reported as **unconfirmed**, not
  unsent: herdr writes the prompt text and Enter before it starts watching,
  so after two sends the pane may hold two copies and "it never arrived" is
  not something this command knows. `--on-failure clean` is refused, and
  both the popup and `create` say to read the pane before resending.

  That posture is **sticky**. Once the text has gone out, nothing later in
  the same step reports the prompt as unsent or permits the clean — which
  matters most when the retry itself is refused, the first send having
  stalled against a screen that had not painted and the dialog being up by
  the time the retry looks. "Gone out" is recorded the moment herdr
  accepts the send, or read from one of the herdr errors that only come
  after it has typed. It is no longer inferred from whichever error ended
  the step, which is how a first send found not to have landed used to
  escape it (#154).
- `herdr pane run` types its argv into a shell rather than exec'ing it, so
  the runner shell-quotes every element — and the argv path that has no
  shell deliberately does not.
- **Besides Linear — which a configured key is your consent for — one
  thing reaches the network without being asked, and it is bounded.**
  Landing the form on a git repository runs a background `git fetch
  --prune` there — once per repository per form open — so the base picker
  knows about remote branches that arrived since you last fetched. It
  contacts that repository's default remote with the credentials git would
  normally use, *including* a `core.sshCommand` or `GIT_SSH` of your own,
  which it resolves and appends to rather than overrides. Every git call
  that can reach a remote runs with terminal prompts and ssh interaction
  disabled, because git asks for a password on `/dev/tty` rather than on
  the stdin it was handed, and inside the popup that is the terminal the
  form is drawn on. The fetch is additionally bounded at 30 seconds, since
  disabling prompts does not stop a configured askpass helper from opening
  a dialog that never answers. [`SECURITY.md`](SECURITY.md) states the same
  at greater length.

### Packaging

- Requires **herdr ≥ 0.9.0**; `platforms = ["linux", "macos"]`, declared
  rather than omitted, because an absent list is an active claim to run on
  Windows, which this cannot.
- `herdr-draft version` prints the version, the plugin id it answers to,
  and the build it was compiled from when the toolchain stamped one.
- The plugin id is namespaced: `zvibaratz.draft`.
- MIT licensed. The form's UI code is ported and adapted from
  [Atrium](https://github.com/ZviBaratz/atrium) — see `NOTICE`.
