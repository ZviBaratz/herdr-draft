# Security

## Reporting a vulnerability

Report privately through [GitHub's security advisory
form](https://github.com/ZviBaratz/herdr-draft/security/advisories/new),
which is visible only to the maintainer until an advisory is published.
Please do not open a public issue for anything exploitable.

Include what a reproduction needs: the output of `herdr-draft version`,
your herdr version, and the smallest configuration or repository that
triggers it. There is no bounty and no SLA — this is a single-maintainer
plugin — but a report will be read and answered.

## What you are installing

herdr's own plugin documentation is explicit: **herdr does not review or
sandbox plugin code.** A plugin installed from the marketplace runs with
your full privileges, and that is true of this one. The rest of this file
is the surface, stated so you can audit it before deciding.

### It runs commands you configure

Three keys in `config.toml` name something to run, and they are not run the
same way.

- `[linear] api_key_cmd` is argv that herdr-draft executes to obtain your
  Linear API key — the `pass show ...` shape is the intended use. It is run
  directly, with no shell, so nothing in it is word-split or glob-expanded,
  and it carries a `//nolint:gosec` at the call site precisely because
  running it is the feature rather than an oversight
  (`internal/linear/client.go`).
- `[clauth] picker` is an executable implementing the [account picker
  protocol](README.md#account-picker-protocol), and it runs more often than
  the other two. Opening the popup runs it twice — a probe, then a
  `--dry-run` preview for the opening project — and every project change
  runs another preview, whether or not the `account` row is on `auto`.
  Submitting runs it once more, for real, when the row *is* on `auto`; and
  `herdr-draft create --account auto` runs it once. It is argv with no shell
  either, and every call is bounded by a 30-second deadline. herdr-draft
  never goes looking for a picker, so one that merely happens to sit on your
  `PATH` under the expected name is not run: it does nothing unless this key
  names it (`internal/picker/cli.go`).
- `[clauth] launcher` is the one that is **typed into a shell** rather than
  executed. See the section on `herdr pane run` below.

**A repository cannot set any of them.** See the trust boundary below: the
five keys a `.herdr-draft.toml` may set are named there, and none of these
is among them.

### It handles a credential

An inline `[linear] api_key` is supported and discouraged. herdr-draft
refuses to read one if `config.toml` is group- or world-readable, naming
the file and its actual mode rather than failing vaguely
(`checkConfigPerm`). `api_key_cmd` avoids the question entirely by keeping
the secret out of the file.

The key is sent only to Linear's own GraphQL endpoint, and the issues it
returns are cached in your plugin state directory.

### It passes your values to subprocesses as argv

herdr-draft drives `herdr`, `git` and `clauth` as child processes, and
several arguments derive from what you type — a branch name from a title,
a base ref, a project path. Every one of those invocations is argv, with no
shell anywhere. Two paths do involve a shell; neither changes that claim for
the values you type, and each has its own section below.

The hardening is at the boundary rather than sprinkled over call sites:
`appendFlag` refuses any value beginning with `-`, and it returns an error
rather than dropping the argument, so a caller that ignored it cannot
silently run a command with different meaning. What that refusal protects
is **git**, not herdr. herdr's own parsers take the next argv element as a
flag's value whatever it starts with
([`src/cli/worktree.rs`](https://github.com/herdrdev/herdr/blob/v0.9.0/src/cli/worktree.rs#L104-L119)),
so a leading `-` is no danger there. The danger is the command herdr builds
from those two values, `git worktree add -b <branch> <path> <base>`
([`src/worktree.rs`](https://github.com/herdrdev/herdr/blob/v0.9.0/src/worktree.rs#L238-L256)),
which a `-`-leading value reaches by one of two routes (measured on
git 2.53.0):

- `<base>` is a positional with **no `--` terminator** before it, so
  `git worktree add` itself reads a `-`-leading base as an option.
- `<branch>` is safe from that parser as `-b`'s argument, but `git worktree
  add` hands it on to a `git branch` child, which reads it as an option. A
  `--` would not close this route.

That is the argument-injection surface, and `--base` and `--branch` are why
the refusal is kept. It is also why `branch_prefix` is validated wherever it
comes from — it reaches `herdr worktree create --branch <value>` as argv.

### Except one path, which is typed into your shell

`herdr pane run` does not exec. It joins its argv with single spaces and
sends the resulting string to the pane as input followed by Enter, so the
argv structure is gone before anything runs and the pane's own interactive
shell re-parses it. Launching a pinned Claude account is the only thing
herdr-draft does that way, and three configured values travel that route:
`[clauth] launcher`'s template, the `CLAUDE_CONFIG_DIR=<dir>` assignment
that `[clauth] launch = "wrapper"` prepends, and your agent's
`[agents] extra_args`.

The mitigation is that `CLIRunner.PaneRun` shell-quotes every element
before handing it to herdr, so a value that is one word in the argv is
still one word after the pane's shell has read it
(`internal/herdrc/runner.go`). The variable *name* in an assignment prefix
is the half that cannot be quoted — quote it and it stops being an
assignment — so it is held to `[A-Za-z_][A-Za-z0-9_]*` instead. Every name
herdr-draft passes today is a compile-time constant; the check is for the
caller that comes later.

This is also why the same values must **not** be quoted on the other path:
`herdr agent start` takes `extra_args` as an argv vector with no shell
anywhere, and quoting them there would corrupt them.

### And one string that git hands to a shell

Every `git` herdr-draft runs that could reach a remote is started with
`GIT_SSH_COMMAND` set, so that a fetch meeting a passphrase-protected key
fails instead of asking for the passphrase on the terminal the form is
drawn on. git does not exec that variable; it runs it through a shell
(`internal/gitx/repo.go`, `nonInteractiveEnv`).

**Nothing you type into the form reaches that string.** It is built from
exactly two things: an ssh command taken from your own environment or git
configuration, and a fixed `-oBatchMode=yes` appended to it. git's own
precedence order for that command is your `GIT_SSH_COMMAND`, then
`core.sshCommand`, then your `GIT_SSH`, then plain `ssh`, and those are the
only sources `effectiveSSHCommand` reads. Two of the three are shell-parsed
by git already, so passing them on changes nothing about what runs; git
would have handed your `core.sshCommand` to a shell with or without this
plugin. `GIT_SSH` is the one git treats as a bare program path, so it is
shell-quoted before it is folded in, and a path with a space in it stays
one word.

The project path you pick decides only *which* repository's configuration
that read consults, never what it says. `git clone` does not copy
`.git/config`, so a cloned repository cannot supply `core.sshCommand`; a
repository whose `.git/` directory you copied from elsewhere — a tarball, an
`rsync`, an unpacked CI artefact — can, exactly as it could for a
`git fetch` you ran yourself, except that the popup starts this fetch on
its own as soon as you pick the project.

### It reads a file that arrives with `git clone`

A repository may commit a `.herdr-draft.toml`. **This is the part worth
reading if you are auditing.**

The rule is that a file arriving with a clone may only choose among values
you could already have picked in the form yourself. It may never name a
command to run, a path outside the repository, or a credential. Five keys
are allowed — `branch_prefix`, `default_worktree`, `default_placement`,
`default_base`, `linear_branch_name` — and everything else is ignored and
*reported*, with the reason, in the project row's panel.

Three things make that more than an intention:

- The allow-list is the only read path. There is no struct with `toml`
  tags for this file, and the extraction helpers panic on a key that is
  not on the list, so adding a field cannot quietly accept anything.
- The allowed surface is flat by construction, so any table header in the
  file is refused structurally. `[worktree] trust_repository` — which
  waives a git ownership check — is rejected by that mechanism rather than
  by anyone remembering to exclude it, because the answer to "is this
  repository safe?" can never come from the repository.
- An `init()` panics if the allow-list and the deny-list ever overlap, so
  a future change that promotes a forbidden key fails on the first test
  run instead of shipping.

### The headless verb creates without asking

`herdr-draft create` builds real topology — a worktree, a workspace, a
tab, an agent — with no confirmation step, because a headless verb that
prompted would not be headless. herdr exports `HERDR_WORKSPACE_ID`,
`HERDR_TAB_ID` and `HERDR_PANE_ID` into **every** pane's shell, so running
it inside a real session creates a real session there.

This is documented in a boxed warning at the top of
[`docs/manual-smoke.md`](docs/manual-smoke.md) because it caught this
project's own reviewer in a live session. Unsetting the three ids is not
sufficient — a `new-space` placement needs none of them — so to run it
safely, point `HERDR_BIN_PATH` at a nonexistent path and check for exit
code 3.

### What it does not do

- No network access of its own except Linear's GraphQL API, and only when
  you have configured a key. It does, separately, make **git** talk to the
  network on your behalf: opening the form on a git repository fires a
  background `git fetch --prune` there — once per repository per form open,
  with no user action — which contacts that repository's default remote (the
  current branch's upstream, otherwise `origin`; all of them if `fetch.all`
  is set) with whatever credentials git would use, and prunes
  remote-tracking refs.
  That fetch runs with terminal prompts and ssh interaction disabled
  (`GIT_TERMINAL_PROMPT=0`, `ssh -oBatchMode=yes`) and under a 30-second
  deadline, so it can neither prompt for credentials on the popup's own
  terminal nor hold the popup open waiting for a remote that never answers.
- No telemetry, no analytics, no crash reporting.
- No writes outside its own plugin state directory and the git/herdr
  operations you submit. An unset state directory means *there is no state
  directory*, not the current one.
- It never writes to Linear — the integration is read-only by design.

## Supported versions

The latest release only. This is a single-maintainer plugin with no
backport process; fixes land on `main` and go out in the next tag.
