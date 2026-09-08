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

### It runs a command you configure

`[linear] api_key_cmd` is argv that herdr-draft executes to obtain your
Linear API key — the `pass show ...` shape is the intended use. It is run
directly, with no shell, so nothing in it is word-split or glob-expanded,
and it carries a `//nolint:gosec` at the call site precisely because
running it is the feature rather than an oversight
(`internal/linear/client.go`).

**A repository cannot set it.** See the trust boundary below.

### It handles a credential

An inline `[linear] api_key` is supported and discouraged. herdr-draft
refuses to read one if `config.toml` is group- or world-readable, naming
the file and its actual mode rather than failing vaguely
(`checkConfigPerm`). `api_key_cmd` avoids the question entirely by keeping
the secret out of the file.

The key is sent only to Linear's own GraphQL endpoint, and the issues it
returns are cached in your plugin state directory.

### It passes your values to subprocesses as argv

herdr-draft drives `herdr`, `git` and `clauth` as child processes, never a
shell, and several arguments derive from what you type — a branch name
from a title, a base ref, a project path.

The hardening is at the boundary rather than sprinkled over call sites:
`appendFlag` refuses any value beginning with `-`, which the herdr CLI
would otherwise read as another flag, and it returns an error rather than
dropping the argument, so a caller that ignored it cannot silently run a
command with different meaning. `branch_prefix` is validated wherever it
comes from — it reaches `herdr worktree create --branch <value>` as argv.

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

- No network access except Linear's GraphQL API, and only when you have
  configured a key.
- No telemetry, no analytics, no crash reporting.
- No writes outside its own plugin state directory and the git/herdr
  operations you submit. An unset state directory means *there is no state
  directory*, not the current one.
- It never writes to Linear — the integration is read-only by design.

## Supported versions

The latest release only. This is a single-maintainer plugin with no
backport process; fixes land on `main` and go out in the next tag.
