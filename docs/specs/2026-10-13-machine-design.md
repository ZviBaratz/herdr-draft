# herdr-draft — start a session on another herdr machine

- **Date:** 2026-10-13
- **Status:** approved by the owner 2026-10-13. The four decisions of §2 were
  answered one by one, and the plan they produced was approved whole.
  Implemented on the branch `zvi/b2-herdr-draft-machine`.
- **Why:** step B2 of the owner's compute migration. Work sessions move to a
  second herdr host, `dev`, while personal sessions stay on the laptop.
  Before this design, every way of starting a session (the popup, `create`,
  the spawn skill) landed on the local herdr server. Reaching another
  machine took four manual steps, and "complexity is a cost" was the
  owner's rule for judging any replacement. The end state this design is
  measured against: **the popup opened in a work project starts the session
  on `dev` with no extra steps, and a personal project still opens locally.**
- **Amends, on approval:**
  - v2 spec §10 gains a field whose chain stops at `config.toml`.
  - v2 spec §11 forbids `[machine]`.
  - v2 spec §13 gains `--machine`.
  - Placement spec §14's "a worktree session runs in its own space" gains a
    second case: a session on another machine.
  - v3 spec §7.2's recipe takes a conditional tenth row without re-deriving.
  - The citation rule in CLAUDE.md and CONTRIBUTING.md gains one scoped
    exception (§11).

  §15 itemises all of these. The draw-first spec is **not** amended: nothing
  here runs before the first frame (§9.2).
- **Citations.** This design rests on a herdr feature that does not exist at
  the current pin, `v0.9.0`. herdr behaviour that 0.9.0 also has is cited at
  the pin. Machine forwarding is cited at **`v0.9.1`**
  (`https://github.com/herdrdev/herdr/blob/v0.9.1/...`), under the exception
  §11 defines.
- **Evidence.** Every probe in §3 was run on 2026-10-13 from the owner's
  laptop (herdr 0.9.1) against the saved machine `dev (EC2)`. Every probe
  was read-only.

---

## 1. Summary

A session gains one more dimension: **the herdr machine it runs on**. Its
value is `local` (this machine, and the builtin) or a saved herdr machine.
`config.toml` maps project directories to machines:

```toml
[machine]
default = "local"            # optional; the builtin is local
[machine.paths]              # the longest whole-component prefix of the project key wins
"~/quantivly" = "dev"
"~/quantivly/browser-repo" = "local"
```

So a work project opens on `dev` and everything else on the laptop, without
anyone choosing. A one-off exception is the popup's `machine` row, cycled to
`local`, or `create --machine local`. Chrome-driving sessions and adb device
checks are the cases that need it. The exception is **never remembered**
(§2, decision 2).

herdr drives a remote session through its own `--machine` prefix (herdr
0.9.1). Git on the remote clone runs over ssh to the same target herdr
itself uses (§8.2). A session on another machine has no account pick, since
that host has its own login. It opens in a new space there, and its base
must be a branch the remote can fetch. When the machine cannot be reached,
the create is refused with the reason, and nothing falls back to the laptop
on its own.

## 2. Decisions

The owner settled four questions on 2026-10-13. Each one is a choice against
a stated alternative.

1. **The default comes from path rules in `config.toml`, not from the
   account picker's tenant.** The picker already answers
   `"tenant": "quantivly"` for `~/quantivly/hub` (§3.5). Deriving the machine
   from it would make the machine wait on an account pick that a remote
   session then throws away, since that session has no account (§6.3). It
   would also make the feature depend on a picker being configured at all. A
   path rule is pure: it runs no subprocess, so the form and `create` resolve
   it identically (v2 spec §10's whole point). The cost is that
   `~/quantivly` is written in two places, here and in the picker's tenant
   table.
2. **The machine is not remembered.** Its chain is builtin, then
   `config.toml`, the same shape as `[worktree] trust_repository`
   (CLAUDE.md). Under placement's rules instead, one local session for a
   Chrome check would silently turn the next popup in that repository local,
   which is exactly the extra step this design exists to remove. A
   repository that must always stay local gets its own rule.
3. **Git on the remote clone runs over `ssh -o BatchMode=yes <target>`.**
   herdr's API cannot run git (§3.3). Without remote git, a worktree would be
   cut from whatever the remote last fetched (`dev`'s clones: 2026-09-16), the
   base would remain the new branch's upstream (#221's hazard), and the clean
   gate would have nothing safe to run. The alternative accepted all three
   and was turned down.
4. **An unreachable machine is refused with its reason, and there is no
   fallback.** The popup's row shows herdr's or ssh's own words and ⌃S
   refuses, with `local` one keystroke away on the same row. `create` exits 3
   and names `--machine local`. An opt-in `fallback = "local"` key was
   offered and turned down: it would let work sessions drift back onto the
   laptop quietly, which undoes the migration this serves.

## 3. Evidence

### 3.1 herdr 0.9.1's machine prefix

`herdr --machine <selector> <command>` routes one CLI command to a saved SSH
machine's server
([`src/cli/target.rs`](https://github.com/herdrdev/herdr/blob/v0.9.1/src/cli/target.rs)).
What it does:

- **Forwarded commands:** `workspace`, `worktree`, `tab`, `pane` and
  `notification`; `agent` except `attach` and a local `explain --file`;
  `api snapshot`, `status server`, a list of `plugin` subcommands, and four
  `server` subcommands (`validate_machine_command`).
- **The selector** is an enabled profile's id or a unique, case-sensitive
  label (`resolve_machine`). The ssh target is **not** accepted. A bad
  selector is a usage error, exit 2.
- **The caller's pane:** `caller_pane_id()` returns `None` when the command
  is remote, so `--current` cannot name the caller's local pane. Local
  workspace, tab and pane ids mean nothing on the other server; ids such as
  `w3` exist independently on each.
- **Transport failures print no JSON envelope.** They are a Rust `Debug` of
  an `io::Error` on stderr, exit 1 (§3.4).

### 3.2 Paths

- `worktree create --cwd` passes `~/...` and absolute paths through
  unchanged when remote
  ([`src/cli/worktree.rs` `normalize_path_arg`](https://github.com/herdrdev/herdr/blob/v0.9.1/src/cli/worktree.rs)),
  and the server expands `~` (`absolute_user_path`,
  [`src/app/api/worktrees.rs`](https://github.com/herdrdev/herdr/blob/v0.9.1/src/app/api/worktrees.rs)).
- `workspace create --cwd` is taken as `PathBuf::from(cwd)` with no
  expansion ([`src/app/api/workspaces.rs`](https://github.com/herdrdev/herdr/blob/v0.9.1/src/app/api/workspaces.rs)),
  so it needs an absolute path that is right **on the remote**.

The laptop's `$HOME` is `/home/zvi`; `dev`'s is `/home/ubuntu`. A laptop path
is therefore meaningless there, and §6.1 maps it.

### 3.3 `worktree create` does not fetch

The server runs `git worktree add -b <branch> <path> <base>`, with no fetch
before it (`build_worktree_add_new_branch_command`, `src/worktree.rs` at both
v0.9.0 and v0.9.1). `dev`'s clones were last fetched on 2026-09-16. Nothing
in herdr's forwarded API runs an arbitrary command and returns its output.

### 3.4 `dev` today

- `herdr machine list --json` (local, about 3 ms) prints a bare JSON array:

  ```json
  [{"id":"af0915ce314d4345e77e3d31e22e8679","label":"dev (EC2)","target":"dev",
    "session":"default","enabled":true,"selected":false},
   {"id":"fb473b9d019c8b09c2ec6da8f4acdaa0","label":"nanoclaw","target":"nanoclaw",
    "session":"default","enabled":true,"selected":false}]
  ```

  That is not the `{"result": ...}` envelope `runJSON` parses, which makes
  it the third success shape this codebase meets after `runJSON` and
  `runOK`. `nanoclaw` is the owner's personal production host; a session
  landing there by accident is a defect (§4.2).
- `herdr --machine af0915ce… status server` answered in 2.3 s with exit 1
  and this on stderr:

  ```
  Error: Custom { kind: Unsupported, error: "machine 'dev (EC2)': remote Herdr does not support machine API forwarding; update Herdr on this machine" }
  ```

  `dev` runs herdr 0.9.0. **Until its server is 0.9.1 or later, every remote
  create is refused**, correctly (§14).
- A read-only `ssh dev` found the following: `HOME=/home/ubuntu`;
  `~/quantivly/{hub,platform,handoffs}` present; `claude` in
  `~/.local/bin`; no `clauth` and no `claude-as`; and git at `/usr/bin/git`,
  which a non-interactive ssh `PATH` reaches.
- **ssh to `dev`** goes through an SSM `ProxyCommand` with
  `ControlMaster auto` and `ControlPersist 600`. A new connection takes
  4–5 s, a multiplexed one about 0.3 s. Each `herdr --machine` call opens
  about three ssh sessions to find and start the remote bridge
  (`src/remote/attach.rs`, `src/remote/saved.rs` at v0.9.1).

### 3.5 What herdr-draft assumed

Before this change, every one of the following was local:

- Every `gitx` call. That includes `untrackBase`'s write after a worktree is
  cut, and `Clean`'s `DeleteBranch`, which under a remote session would have
  deleted a **laptop** branch of the same name.
- `tab here` and `split here`, built from the invoking pane's ids.
- `tab in`, which reads the local workspace list (`FindSpace`).
- The account path: clauth's status file, the picker, and the launcher
  template (`claude-as` is a laptop dotfiles function).
- The `Clean` guard that refuses to close "the workspace this create was
  invoked from". It compares ids, which on a remote session come from two
  servers.

The picker's `tenant` field was decoded (`internal/picker`) and never read.

## 4. Vocabulary

### 4.1 Selectors

A **selector** names a machine, both in `config.toml` and in `create
--machine`. It is resolved against `herdr machine list --json`, in this
order:

1. `local` is always this machine. A profile labelled `local` must be named
   by its id.
2. A profile **id**.
3. An exact profile **label**.
4. A profile's ssh **target**, only when exactly one profile has it.

A label match and a target match that point at different profiles are
refused as ambiguous. A disabled profile is refused. Whatever matched, herdr
is always handed the **profile id**, so a renamed label never breaks a rule,
and herdr's own label lookup is never what decides. Step 4 goes further than
herdr's own selector, and it is what lets the owner write `dev` rather than
`dev (EC2)`.

### 4.2 What is offered

A machine is **offered** if it is `local`, or if `config.toml` names it in
`[machine] default` or anywhere in `[machine.paths]`. The popup's row offers
exactly that set, and `create --machine` **accepts exactly that set**, since
every `create` flag has a form control behind it (CLAUDE.md;
`equivalence_test.go`). It accepts any of an offered machine's names --
the selector `config.toml` wrote, or the label or profile id `--json` hands
back -- so that an agent passing back what a dry run reported reaches the
same machine (`machine.SelectOffered`). A saved machine the configuration never names, such
as `nanoclaw`, cannot be reached by either front end. That is deliberate: a
production host must not be one ← away from a work session.

With no `[machine]` table at all, only `local` exists. The popup has no
`machine` row, and nothing changes for anyone who has not opted in.

## 5. Configuration and resolution

### 5.1 The table

- `default` is a selector, used when no rule matches. It is optional; the
  builtin is `local`.
- `[machine.paths]` maps a directory to a selector. Keys are expanded
  (`~`) and canonicalised when `config.toml` loads.
- A relative key, an empty selector, or two keys that canonicalise to one
  directory are **reported**, and the offending entry is dropped. No
  entry is ever dropped silently (the `[agents.options]` warning path).

### 5.2 Matching

A rule matches when its directory is the **project key** or one of its
ancestors, compared by whole path components: `~/quantivly-old` does not
match `~/quantivly`. The longest match wins.

The project key is v2 spec §10's memory key: the repository root from
`--git-common-dir`'s parent, or the canonical path for a directory that is
not a repository. A lane (a worktree under `~/.herdr/worktrees/...`)
therefore resolves to its repository's rule. That matters because lanes are
where the spawn skill starts most sessions.

### 5.3 The chain

`defaults.Machine(cfg, key)` returns the selector, the tier that supplied it
(`TierBuiltin` or `TierUserConfig`), and the rule that matched. It is a
function of its own, shaped like `defaults.AgentOptions`, so the chain's stop
at `config.toml` is a property of the signature rather than of a test.
`last-used.json`, `projects.json` and `.herdr-draft.toml` cannot supply a
machine, because nothing reads them for one.

`.herdr-draft.toml` **refuses** a `[machine]` table, with a visible note
(v2 spec §11). A repository arriving with `git clone` must not choose which
host runs your code: that is a choice about where commands execute, which
the trust boundary exists to keep out of a repository's hands.

## 6. What a remote session is

### 6.1 Its directory

A remote session's directory has two parts:

- **The relative path:** the project key's path relative to the laptop's
  `$HOME`, both canonicalised.
- **The remote `$HOME`:** learned from the probe (§7).

A key outside the laptop's `$HOME` is refused for a remote session; there is
no relative path to carry across. A lane maps to its repository's
**primary checkout** on the remote. The lane's own checkout exists only on
the laptop. `Linked` (the lane's commit as a base) is therefore zero for a
remote session.

### 6.2 Its placement

A remote session opens in **a new space on that machine**.
`EffectivePlacement(useWorktree, remote, chosen)` returns `new-space` when
`remote` is set, as it already did under a worktree (placement spec §14):

- `tab here` and `split here` would need the caller's pane on the remote
  server, which does not exist there (§3.1).
- `tab in` would need the remote's workspace list at resolve time. It is
  out of scope (§13).

`create` refuses `--placement tab-here|split-here|tab-in` and `--workspace`
together with a remote machine (exit 2). The popup's placement row goes
inert with `a new space on <label>`. A remembered placement is left as it
was (`RememberedPlacement`), so a remote session never rewrites what the
local default is.

### 6.3 No account

`dev` uses its own `/login`; the laptop's clauth profiles, picker and
launcher are not there. A remote session therefore launches through Path A
(`herdr agent start`) with the configured agent arguments and options. The
picker preview, the clauth hold and the probe hold are all skipped, and the
account row goes inert with `<label> uses its own login`. `create
--account` together with a remote machine is a usage error (exit 2), and
`[clauth] default` is ignored for a remote session.

### 6.4 Its base

The worktree is cut **on the remote, after a fetch there** (§8.3), so the
base must be a ref the remote can have. `RemoteBase`, shared by the popup
and `create` like `SettleBase`, maps the resolved base using the laptop's
clone:

| resolved base | remote base |
|---|---|
| `""` (HEAD) | the target of the laptop's `origin/HEAD`, e.g. `origin/main` |
| `origin/X` | unchanged |
| a local `X` with an `origin/X` | `origin/X` |
| anything else | refused: "dev has none of this laptop's local branches — push it, or use local" |

The stale-base note (#360) gives way to `cut from origin/main on <label>,
after a fetch there`. Memory records the base the form resolved, **before**
this mapping.

### 6.5 What is never stored

The machine itself is never stored, in either memory file. The placement and
base written after a remote session are what they would have been had it
been local (§6.2, §6.4).

## 7. The probe

A remote machine is checked **before** anything is created, by two probes
that run in parallel under one **30 s** deadline. That is
`preflightCheckDeadline`'s size, not `blockingCheckDeadline`'s 5 s: one
`herdr --machine` call is about three ssh sessions, and a new SSM
connection alone takes 4–5 s (§3.4).

1. **Forwarding:** `herdr --machine <id> workspace list`. Its success means
   the machine is reachable, its herdr supports forwarding, and the client
   and server agree. Its workspace labels feed the duplicate-title check.
   Its failure text is herdr's own, and is shown unwrapped from the
   `Custom { … }` debug form. The unwrapping is for display only and never
   classifies anything (#144).
2. **The clone:** one `ssh -o BatchMode=yes -T <target>` that answers the
   remote `$HOME`, whether `"$HOME"/'<rel>'` exists, whether it is a git
   repository, origin's URL, and the clone's local branch names.

**Three refusals** come out of the clone probe:

- **No clone:** the directory is missing, or is not a repository when a
  worktree was asked for. The message is `~/quantivly/x is not on dev`.
- **The wrong repository:** origin differs from the laptop clone's. Both
  URLs are normalised (scheme, credentials, port, `:`/`/` and `.git`) and
  compared by owner and repository alone, because one repository arrives
  under several hosts -- `dev` rewrites ssh remotes to https, and an ssh host
  alias names github.com otherwise. The refusal shows the normalised URLs,
  so a token in either never reaches the screen.
- **A duplicate branch:** the branch already exists in that snapshot.
  herdr would check out an existing local branch and ignore the base, so
  this is the remote half of the title check. The laptop's `origin/*`
  answers the other half.

**A timeout is unknown, not invalid**: it refuses with "the check did not
answer", not with "unreachable". The front ends map each outcome as follows:

| outcome | `create` exit | popup |
|---|---|---|
| unreachable, or forwarding refused | 3, naming `--machine local` | the row shows the reason; ⌃S refuses |
| no clone, or the wrong repository | 2 | the same |
| duplicate branch | 2 | the title row's existing refusal |
| timed out | 5 | the row says so; ⌃S refuses |

## 8. Execution

### 8.1 The runner

`herdrc.CLIRunner` gains `Machine` (a profile id). One argv helper serves all
four exec sites (`runJSON`, `runOK`, `runText`, and `pollDetection`'s own
`agent get`), and it puts `--machine <id>` first. `Runner` gains `On(id)
Runner`, and **`plan` scopes the runner itself**: `Execute`, `CleanCheck`
and `Clean` call `r.On(machine.ID)`, so no caller can hand a remote plan a
local runner.

`plan.Machine{ID, Label, Target, Dir}` sits on `plan.Input`, and `Build`
stamps it on every op. Its zero value means local. It is not in `ExecOpts`,
which holds only what differs between the popup and `create` (CLAUDE.md):
the machine is part of *what* is built, like `AccountConfigDir`.

**`refusedWhole` does not see transport failures.** A transport failure has
no envelope, so `herdrEnvelopeCode` finds no code and the create is **not**
marked "nothing created". `create` therefore exits 1 rather than 4, which is
the honest answer: a bridge that dropped after sending may have made the
space.

### 8.2 Git, over ssh

`gitx.Host` is the seam:

- **The two values:** `gitx.Here`, its zero value, and `gitx.Over(target)`.
- **What becomes a method:** the functions `plan` needs become `Host`
  methods: `LocalBranchExists`, `BranchExists`, `ResolveRef`, `Upstream`,
  `FullRefName`, `AutoSetupMerge`, `UnsetUpstream`, `Disposable`,
  `CommitsAhead`, `CommitsOnlyOn`, `DeleteBranch` and `FetchPrune`.
- **No call site moves:** the package functions stay as wrappers over
  `Here`, so no caller outside `gitx` changes.
- **The remote command:** `ssh -o BatchMode=yes -o ConnectTimeout=15 -o
  ServerAliveInterval=15 -o ServerAliveCountMax=4 -T -- <target> exec /bin/sh
  -c '<script>'`. The bounds are herdr's own reason for its bridge: these calls
  mostly have no deadline of their own, and an SSM link that stalls without
  an EOF would hang a multiplexed client for good. `/bin/sh`, because OpenSSH
  hands the command to the remote user's login shell, which may be fish or
  csh. The script unsets git's repository environment variables and runs
  `GIT_TERMINAL_PROMPT=0 git -C <dir> <args>`, with every word
  shell-quoted. It does **not** set `GIT_SSH_COMMAND`: the remote's own
  `core.sshCommand` must win there, which is #130's lesson from the other
  side.
- **Exit codes:** ssh exits 255 on its own failure, and that is an error,
  never "absent". Only git's 1 means a ref was not found.
- **`ErrWaitDelay` after a zero exit is success.** A `ControlPersist`
  master started by this command can inherit its stderr and outlive it.
- **What stays local:** `IsGitRepo` and the `configValue` behind the
  laptop's own ssh.

### 8.3 The worktree step, remote

In order, on the remote, with nothing in herdr asked until the third step:

1. `FetchPrune`, bounded at 30 s (`fetchPruneTimeout`'s size). **A failed
   fetch is fatal**: a base that might be a month stale is the failure this
   step exists to prevent. It is retried once, a second later and inside
   the same bound, because sessions spawned together into one clone fetch
   at once and git refuses the later fetch while the earlier holds a ref
   lock.
2. Resolve the base there (`commitAt`). It is fatal too.
3. `herdr --machine <id> worktree create --cwd <dir>`, where `<dir>` is the
   absolute directory the clone probe reported. The server would expand
   `~/<rel>` too (§3.2), but one absolute directory serves both this call
   and `workspace create`, which would not.
4. `untrackBase` there (#221).

A failure in step 1 or 2 means herdr was never asked, so `create` exits 4.
For a non-worktree session, `workspace create --cwd` gets the absolute
remote directory.

### 8.4 Clean

`CleanCheck` and `Clean` run their git through the same host. The guard
"refusing to close the workspace this create was invoked from" is skipped
for a remote session, whose workspace can never be the caller's. The test
that matters: two repositories, "laptop" and "remote", each with a branch
`x`; a remote `Clean` deletes `x` **only** in "remote".

### 8.5 Messages

These name the machine:

- the first step's label (`creating worktree on dev (EC2)`), which names
  where the session's space is going; the steps after it are in that
  space;
- `explainBlockedStart`;
- the trust-wait hint, which tells the reader to switch herdr to that
  machine to answer, because the dialog is on a screen the laptop is not
  showing.

The pane tail a failed launch quotes is left as it is: it is the pane's own
text, and the step label above it already says where the pane is.

## 9. The form

### 9.1 The row

The section order becomes issue, title, prompt, project, **machine**,
worktree, placement, agent, options, account. The row is a chip row,
**present only when `config.toml` has a `[machine]` table**. Its chips are
the offered set (§4.2), with `local` first, and the label fits the 11-cell
column.

The panel shows:

- each chip's profile label;
- the provenance, e.g. `from config.toml: ~/quantivly → dev`;
- the probe's state: `checking dev (EC2)…`, `ready: ~/quantivly/hub on dev
  (EC2)`, or the refusal in herdr's or ssh's own words.

### 9.2 Timing

- **Before the first frame:** the row set is fixed by a pure check (does
  the table exist?), and the rules are matched **lexically** against the
  project directory. That reads no file and runs nothing, so the
  draw-first spec's §3.1 rule holds unchanged.
- **`Init`:** runs `herdr machine list --json`.
- **When the dir check lands:** its key re-resolves the rules, which is
  how a lane follows its repository, the same second stage
  `applyProjectDefaults` already runs. The probe then starts, versioned
  like the other checks, on the `Lifetime`'s context so that `esc` kills
  the ssh.

### 9.3 Touched versus preselected

The row has its own `appliedMachine` snapshot. A re-resolution never
overrides a machine the user cycled to, under the same rule every other
field follows (v2 spec §10). `syncDerivedInertness` drives the placement
and account rows' inert state from it.

### 9.4 ⌃S

A probe still out joins `handleSubmit`'s hold. A refusal, or a timeout,
refuses the submit and focuses the machine row, where `local` is the first
chip.

### 9.5 Success

A remote create posts a local `herdr notification show "<title> opened on
<label>"`, best-effort. herdr 0.9.1 has no command that switches the
client's view to another machine, so without a toast the popup closing
would look like nothing happened.

### 9.6 Height

`panelCapRows` stays 14. v3 spec §7.2's recipe would give 13 for ten rows,
and the agent-options spec (§7.4) re-derived it for its ninth. The tenth row
here is **conditional**, though. Re-deriving would move every capped frame,
the showcase images included, for users who never see the row. With the
row present, at 30 rows the natural region is exactly 14 (30 − 10 − 6), so
the row costs the card's one row of inset, which `layoutFrame` already gives
up first. Nothing clips. A golden frame pins that state.

## 10. `create` and the spawn skill

### 10.1 The flag

`--machine NAME` takes a selector from the offered set (§4.2), or `local`.
Left off, the path rules decide.

- **Refused with a remote machine (exit 2):** `--account`, `--placement
  tab-here|split-here|tab-in`, `--workspace`, and a base that §6.4
  refuses.
- **`--json`:** gains `"machine": {"selector", "id", "label"}`, and the
  provenance map gains `machine` (`flag`, `config.toml` or `built-in`).
- **`--dry-run`:** runs both probes and reports what would be created and
  where.

### 10.2 The skill

`spawn_skill.md` gains a "which machine" part beside placement:

- The default comes from the owner's rules.
- `--machine local` is for sessions that drive the local browser or a USB
  device.
- A remote session sees only what is pushed, and its base is an `origin/`
  branch.
- The "here" placements do not exist on a remote session.

The accounts part says there is no `--account` on another machine. The exit
table names exits 3 and 5 for a machine. The contract tests already require
every flag to appear in both the skill and `createUsage`.

## 11. Citations: the `v0.9.1` exception

The rule (CLAUDE.md; CONTRIBUTING.md "Spec citations") is that a citation
into herdr names the pin, `v0.9.0`, and any other ref is legacy. Machine
forwarding does not exist at the pin. The exception:

> **A citation of herdr's machine forwarding** (`--machine`,
> `machine list`, the remote bridge) **names `v0.9.1`**, the first release
> that has it. Everything else still cites the pin. When the floor reaches
> 0.9.1, these citations become ordinary pin citations and the exception is
> deleted.

The floor itself stays at **0.9.0**. Machines need 0.9.1 on **both** ends,
and the README says so. On a 0.9.0 laptop the probe shows herdr's own
refusal of the unknown option. Raising the floor would refuse the plugin to
every 0.9.0 user for a feature most of them will not configure.

## 12. Testing

- **`gitx`:** a fake `ssh` on `PATH` that drops its options and runs its
  last argument with `sh -c` in a temporary repository. It covers hostile
  quoting (a space, a `'`, a `$`), 255 against 1, a background child holding
  stderr (`ErrWaitDelay`), and the environment prefix.
- **`herdrc`:**
  - the `--machine` argv at all four exec sites, through the fake-binary
    harness;
  - `MachineList` against the bare array captured in §3.4;
  - `refusedWhole` with a non-envelope transport failure against a
    `protocol_mismatch` envelope.
- **`config` and `defaults`:**
  - whole-component matching and the longest-rule rule;
  - the memory and repo tiers never supply a machine;
  - `.herdr-draft.toml` refuses the table.
- **`plan`:**
  - `Build`'s refusals and placement;
  - runner scoping;
  - git through the host;
  - the `Clean` regression (§8.4).
- **`create`:**
  - the flag and its combinations;
  - each probe outcome's exit code;
  - `--json`;
  - memory left alone.
- **`equivalence_test.go`:**
  - a rule picks `dev`;
  - the longest rule wins;
  - `[machine] default`;
  - `--machine local` against the row cycled to `local`;
  - a lane maps to its primary checkout;
  - a remembered `tab-here` with an open space still gives a new space;
  - a remembered `main` becomes `origin/main`;
  - a configured default account is dropped.
- **Golden frames:**
  - the row local with provenance;
  - checking;
  - ready, with placement and account inert;
  - unreachable, in herdr's words;
  - no clone;
  - timed out;
  - a refused ⌃S;
  - the 30-row frame with ten rows.

  Every frame without `[machine]` stays byte-identical.
- **Live:** `docs/manual-smoke.md`'s machine cell. It is runnable read-only
  today (the refusal of §3.4), and end to end once `dev` runs 0.9.1.

## 13. Out of scope

- **A fallback to local**, which decision 4 rejects.
- **`tab in` an existing space on the remote.** It would need the remote
  workspace list at resolve time.
- **An account on the remote,** beyond its own login.
- **Switching the herdr client's view to the remote machine.** herdr 0.9.1
  has no command for it.
- **A project outside `$HOME`.**
- **Moving a running session between machines.**

## 14. Prerequisites and rollout

1. **`dev`'s herdr server must be 0.9.1 or later** (§3.4). Restarting it
   stops its panes, so it is the owner's step, with the dev-sync work (B1).
2. **Check once, live:** whether Claude on `dev` shows its trust prompt for
   each new worktree path. If it does, every remote session waits on a
   dialog the laptop is not showing. §8.5's hint says where it is; the fix is
   `dev`'s own configuration.
3. **Switching on** is one line in `config.toml`, `"~/quantivly" = "dev"`.
   **Rolling back** is removing it, and sessions already on `dev` keep
   running.

## 15. Amended text, itemised

| document | section | amendment |
|---|---|---|
| v2 spec | §10 | Adds a field, the machine, whose chain is builtin then `config.toml`, resolved by its own function (§5.3). The precedence list is unchanged for every other field. |
| v2 spec | §11 | `[machine]` joins the forbidden list: a repository does not choose which host runs your code. |
| v2 spec | §13 | `create` gains `--machine`, its refusals (§10.1), exit 3 for an unreachable machine, and exit 5 for a probe that timed out. |
| placement spec | §14 | "A worktree session runs in its own space" gains a second case: a session on another machine runs in a new space there (§6.2). |
| v3 spec | §7.2 | The recipe re-derives the cap for an **unconditional** row. A conditional row may instead take the card's inset (§9.6). |
| CLAUDE.md, CONTRIBUTING.md | spec citations | The `v0.9.1` exception (§11). |
