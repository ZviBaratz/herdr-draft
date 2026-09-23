# Contributing

Patches are welcome. This file exists because several of this project's
conventions are non-obvious and will be got wrong by default — not because
the process is heavy.

`CLAUDE.md` covers much of the same ground in more depth. It is written for
an agent working in the repository; this file is the same material for a
person, plus the parts a contributor needs and an agent does not.

Questions, and ideas you would like to talk through before writing code, go
to [Discussions](https://github.com/ZviBaratz/herdr-draft/discussions).
Bugs go to the [issue tracker](https://github.com/ZviBaratz/herdr-draft/issues/new/choose),
and anything exploitable goes through [SECURITY.md](SECURITY.md) instead.
Everyone taking part is expected to follow the
[code of conduct](CODE_OF_CONDUCT.md).

## Prerequisites

- **Go 1.25 or later.** `go.mod` says `go 1.25.0`, and that line is also the
  floor for everyone who installs the plugin, since herdr builds it on their
  machine. Do not raise it without a reason worth that cost.
- **[just](https://github.com/casey/just)**, which runs every recipe below.
- **staticcheck**, at the version the justfile pins. `just unused` prints
  the exact `go install` line (and a `go run` alternative) when it is
  missing.
- **Python 3.11 or later**, only for `just live`, `just live-selftest`,
  `just screenshots` and `just frames-selftest`. The two `live` recipes
  install their one dependency into a scratch virtualenv on first use; the
  two frame recipes need only the standard library.
- **herdr 0.9.0 or later**, to run the plugin for real.

## Quickstart

```bash
git clone https://github.com/ZviBaratz/herdr-draft
cd herdr-draft
just build                   # go build, stamped with git describe, into bin/
herdr plugin link "$PWD"     # register this checkout with herdr
just check                   # the gate, below
```

**`herdr plugin link` does not build.** herdr runs the manifest's
`[[build]]` only from `install`
([`run_plugin_build_commands` has a single call site, inside
`plugin_install`](https://github.com/herdrdev/herdr/blob/v0.9.0/src/cli/plugin.rs#L210)),
so a linked tree with no `bin/herdr-draft` in it registers an action that
launches nothing. Run `just build` after every change you want to see in the
popup. herdr registers a link for the current user and every session sees it
at once, with no restart. `<path>` is positional, a plugin directory or a
manifest path, not `--path`.

A link and an install with the same plugin id do not stack: `install`
refuses while a link is registered (*"plugin zvibaratz.draft is already
linked from a local path; uninstall/unlink it before installing from
GitHub"*, `herdr:src/cli/plugin.rs` at v0.9.0, `ensure_replacement_allowed`).
Unlink with `herdr plugin unlink zvibaratz.draft` before moving from a
development link to a released install.

To look at the form without herdr at all, `just live` runs the real binary
under a pseudo-terminal with every input stubbed; see
[Manual smoke](#manual-smoke) below.

## The gate is `just check`, and it is four steps

```bash
just check
```

```
test -z "$(gofmt -l .)"
go vet ./...
just unused          # staticcheck -checks U1000 -tests=false ./...
go test ./...
```

CI runs exactly this recipe on every push and pull request
([`.github/workflows/check.yml`](.github/workflows/check.yml)), on Linux and
macOS, so a green run locally is a green run there. `go test ./...` covers
the unit tests and the golden-frame tests below, with no real I/O.

**A green `go test ./...` is not enough.** The step that will reject a
patch unexpectedly is `just unused`, because `go vet` does not detect
unused unexported code *at all*. That blind spot has shipped two defects
here: a whole cascade of drop-line handling survived the deletion of the
only path that called it — still drawing a focus affordance the redesign
had replaced — and half of a filter-count feature sat in a green tree
defined and called by nothing.

`-tests=false` is the load-bearing flag. Without it, a symbol kept alive
only by the test that exists to call it counts as used, which is the exact
shape both defects had. If a symbol is *deliberately* test-only, keep it
with a `//lint:ignore U1000 <reason>` as the last line of its doc comment,
and make the reason say why the symbol is a statement rather than a
leftover. That directive is the only accepted suppression — there is no
allow-list file — because the reason has to sit where the next sweep will
read it.

staticcheck is the one dev tool not in `go.mod`. Its version is pinned in
the justfile as `staticcheck_version`, which the workflow reads too, and
`just unused` run without it names the exact install line. It is not a
`go.mod` tool directive on purpose: that would drag this module's own `go`
line up to whatever the linter needs, and herdr builds the plugin with
`go build` on the *user's* machine, so a dev tool must not narrow who can
install.

## Golden frames

`internal/*/testdata/frames/` holds byte-exact renderings of real screens.
They are the most precise description of the UI in the repository — read
one before trusting any prose about the interface, this file included.

**Regenerate per package. `go test ./... -update` fails.**

```bash
go test ./internal/form/ -update
go test ./internal/app/ -update
```

The flag is registered per test binary, in those two packages only, so a
repo-wide run errors on every other package.

**The screenshots in `docs/images/` are drawn from frames too**: the
`showcase-*` frames in `internal/app/testdata/frames/`. A change that moves
one needs `go test ./internal/app/ -update` and then `just screenshots`,
which regenerates the SVGs; otherwise `TestShowcaseImagesAreCurrent` fails.
`just frames-selftest` checks the frame-to-SVG renderer on its own.

**A frame proves only the state someone thought to fixture.** This project
shipped a defect in the form's *opening* state — the first thing every user
sees — through fifteen green commits, because every fixture had a title
already typed. If your change moves a state no frame pins, add the frame.
And a frame records bytes, not perceptibility: anything that marks a region
needs a *measured* contrast floor against the background it is actually
drawn on, in `internal/theme/contrast_test.go`.

## Tests

Every package that touches herdr, git, Linear or clauth is tested against a
small local interface plus a fake — never the real subprocess or network.
Keep it that way; a new dependency on real I/O in a unit test will be asked
about.

Make a test able to fail. A fake whose canned output does not match what
the real tool produces will pass while the code is broken against reality:
`CLIRunner.PaneRun` failed *every* real invocation while its own unit test
passed, because the fake printed a JSON envelope that `herdr pane run`
never emits.

## Spec citations

herdr-draft is a standalone Go and Bubble Tea binary that drives herdr only
through its public CLI (`$HERDR_BIN_PATH`, which reaches the server over the
socket at `$HERDR_SOCKET_PATH`), never the raw socket API. `docs/specs/`
holds the design records it was built from;
[`docs/README.md`](docs/README.md#design-records) lists them with their
status and a reading order. `docs/specs/2026-08-31-herdr-draft-design.md` is
the original; `docs/specs/2026-09-02-herdr-draft-v2-design.md` supersedes its
§6 (the form) and §7 (skin and mouse); and
`docs/specs/2026-09-02-herdr-draft-v3-design.md` supersedes v2's §4, §7 and
§9 with the dialog that ships. The placement spec then replaces parts of all
three, and each later spec amends the sentences its feature changes. Each
one's own header itemises what it replaced.

Eleven documents, nine of them cited by section:

| citation | document (`docs/specs/`) |
|---|---|
| bare `spec §N` | `2026-08-31-herdr-draft-design.md` (v1) |
| `v2 spec §N` | `2026-09-02-herdr-draft-v2-design.md` |
| `v3 spec §N` | `2026-09-02-herdr-draft-v3-design.md` |
| `placement spec §N` | `2026-09-03-placement-and-pane-ownership-design.md` |
| `spawn-skill spec §N` | `2026-09-16-spawn-skill-design.md` |
| `reap spec §N` | `2026-09-17-pane-reaper-ready-design.md` |
| `agent-options spec §N` | `2026-09-18-agent-options-design.md` |
| `draw-first spec §N` | `2026-09-21-draw-first-design.md` |
| `host-colours spec §N` | `2026-09-23-host-colours-design.md` |

The other two are named by filename, because nothing cites them by
section: `2026-09-07-herdr-membership-assessment.md`, and
`2026-09-08-herdr-8.1-8.2-correction.md`, which retracts the placement
spec's §2.4 and §8.1.

Each supersedes only the sections it names; everything else stays
authoritative, which is why plenty of live code cites v2 correctly. v3 §13,
the placement spec's §12, and §12 of the reap, agent-options and draw-first
specs each itemise what that document replaced. The draw-first spec
describes intended behaviour until it is implemented, so a citation of it
names a plan, not the shipped code.

**A bare `spec §N` means v1, and the check that keeps it honest is opening
v1 §N** — not avoiding numbers that v2 reused. See CLAUDE.md's version of
this section for why (#287): §10, §11 and §13 each name a different subject
in v1 and in v2, and those three are where every wrong citation was.

Most non-trivial doc comments cite a spec section or a live-checkpoint
finding. **Read them before changing the code they sit on** — they usually
record a non-obvious constraint rather than restating the code.

Citations into herdr's own source name a pinned ref, never a local path.
Two spellings: `herdr:src/cli.rs at <pin>` for a passing reference, or a
full `https://github.com/herdrdev/herdr/blob/<pin>/...#L123` URL where a
reader will want to go look. The pin is always the herdr release this
plugin's floor names (`min_herdr_version` in `herdr-plugin.toml`), so it
moves when the floor does. That key holds a bare version and herdr's tags
carry a `v`, so the pin is tagged `v<version>` — a bare `blob/X.Y.Z/` URL
is a 404.

A citation anchored to **any ref other than the current pin** is
**legacy**: still a valid statement about *that* ref, not necessarily
about the herdr this plugin runs against. The first and most common
example is the first pin, `b1ff4582e9688f52ffb943cfa8bee4871ae122e4`
(`herdr:src/cli.rs` with no ref, or `blob/b1ff4582/`), which differs from
the pin that replaced it in the prompt-wait semantics several comments
depend on. Every citation of the current pin joins it the moment the floor
moves. Migrate a legacy citation to the current pin, re-reading the cited
lines at the new ref, when you touch the code around it; do not write a
new one. A citation that resolves only on one machine is not a citation.

**The current pin is the `v0.9.0` tag**
(`b99002ac99b09e00b4ca692436cb15a6b0d676f1`), 59 commits ahead of
`b1ff4582`. This paragraph is the only place the pin is written down —
`CLAUDE.md` states the same rule and points here — so bumping the floor
means editing this paragraph and nothing else about the convention.

## Commits and pull requests

Branch, open a PR, squash merge. Conventional-commit prefixes, lowercase
imperative subjects.

Commit bodies here are long, with `##` headers, and they explain
**rationale** — why this shape and not the obvious alternative, what was
tried and rejected, what a reader would otherwise have to rediscover. A
subject line with no body is fine for a typo and unusual for anything else.
Add `Closes #N.` where it applies.

If you disagree with something a comment asserts, say so in the PR rather
than deleting the comment. Several of them record findings that cost a live
debugging session to obtain.

## Releases

**Four things carry the version, and they move together.**

| Where | Why it exists |
|---|---|
| `herdr-plugin.toml` `version` | what herdr reads at install time and displays for the installed plugin |
| `herdrc.Version` | what the binary reports as itself, in `herdr-draft version` |
| `CHANGELOG.md`'s newest release heading | what a human reads to find out what changed |
| the git tag `vX.Y.Z` | what `herdr plugin install --ref` pins, and what `just build` stamps |

The first three are a gate: `TestVersionMatchesManifest` holds the manifest
and the constant to each other, and `TestChangelogMatchesVersion` holds the
changelog's newest *released* heading to the constant (a bare
`## Unreleased` section is skipped, so work can accumulate under one). Get
any of the three wrong and `just check` fails.

**The tag is the fourth and no test can reach it.** A correct checkout can
have no tags at all — a tarball, a shallow clone, a fresh worktree — so
nothing may assert one exists. That makes it the member to forget, which is
the reason this section exists rather than a comment somewhere.

It is load-bearing rather than ceremony. herdr has no `plugin update`
command — its verbs are `install`, `uninstall`, `link`, `list`,
`config-dir`, `unlink`, `enable`, `disable`, `action`, `log` and `pane`
([`herdr:src/cli/plugin.rs`](https://github.com/herdrdev/herdr/blob/v0.9.0/src/cli/plugin.rs#L25-L36))
— so `herdr plugin install owner/repo --ref <tag>` is the documented way to
pin or refresh an install, and it needs something to point at. And
`just build` stamps `git describe --tags --always --dirty` into the binary:
with no tag in reach that degrades to a bare hash, and `herdr-draft
version`'s build line stops naming a version at all.

### Cutting one

The date is the awkward part: the heading should carry the release date, the
tag should name a commit that already contains the changelog, and you do not
know the date until you are ready. So the date goes on **with** the tag, in
its own commit, and never earlier:

1. **One PR** bumps `herdr-plugin.toml`'s `version` and `herdrc.Version`,
   and adds the `CHANGELOG.md` section headed `## X.Y.Z — unreleased`.
   `just check`, review, squash merge. A release whose section still says
   `unreleased` on `main` is a normal state — it means the version is
   settled and the release is held, usually pending a verification.
2. **When you are actually releasing**, on `main`: replace `unreleased` with
   the date, commit it alone (`release: date X.Y.Z`), and tag *that* commit
   `vX.Y.Z`. Push the commit and the tag. The tagged tree then carries the
   right date, which it cannot if you date it before the tag exists or amend
   it after.
3. **Cut the GitHub release** from the tag: what the thing is, the install
   command with `--ref vX.Y.Z`, the `min_herdr_version` and platform
   constraints, and a pointer to `CHANGELOG.md`. Not a commit list — the
   changelog is the commit list, edited.
4. **Open the next `## Unreleased`** in the changelog when the first change
   after a release lands, not before. An empty one is noise.

Do not tag a commit that is not on `main`, and do not move a published tag.
A wrong release is a new patch version, not a rewritten one — someone may
already have installed `--ref` against it.

## What not to propose

Some features were considered and explicitly declined, and re-proposing one
is a regression rather than an idea. The full lists are in v1 §16, v2 §16,
v3 §14 and the placement spec §13 — among them variant fan-out, draft
persistence, writes back to Linear, prompt-history reuse, a command bar,
session recipes, row grouping bands, and git ahead/behind in the header.

Model, effort and permission mode used to be on this list. They came off it
the way this section says anything should: the recorded reasoning was argued
against, in `docs/specs/2026-09-18-agent-options-design.md` §2, and the owner
accepted the argument.

If you think one deserves revisiting, open an issue arguing against the
recorded reasoning rather than a PR implementing it.

## The install route

`herdr plugin install` clones the repository into a temporary checkout,
prints a preview, asks for confirmation, and only then runs the manifest's
`[[build]]` (`go build -o bin/herdr-draft ./cmd/herdr-draft`, with the plugin
root as its working directory) before moving the checkout into place. The
build runs on the **user's** machine, which is why nothing here may raise the
Go floor. A failed build fails the install with nothing registered, and a
build that edits `herdr-plugin.toml` on its way past is refused, so the
manifest the user confirmed is the manifest they get. `-y` / `--yes` is
required rather than optional when stdin is not a terminal: a scripted
install without it exits 2 instead of hanging.

**This route has been run from clean.** On 2026-09-09, on a freshly
provisioned Ubuntu 22.04 x86_64 machine that had never had herdr or this
plugin on it: git, then herdr 0.9.0's own release binary and Go 1.25.14, and
nothing else. `herdr plugin install ZviBaratz/herdr-draft -y` printed the
preview, ran `[[build]]`, registered the plugin and left a working binary:
`herdr plugin list` reported `zvibaratz.draft (herdr-draft) enabled`, and the
installed `herdr-draft version` reported `0.1.0` with the resolved commit.
The build wants no dev tooling: it is a plain `go build`, and it ran with no
`staticcheck` anywhere on the machine.

What that run did not settle:

- **The popup was not exercised on that machine**, since it needs an
  attached TUI client, and no session was created end to end there. The real
  popup's end-to-end run is Cell 1 of
  [`docs/manual-smoke.md`](docs/manual-smoke.md), recorded on the author's
  own machine the same day.
- **It was the author's own cloud instance, not a stranger's.** It proves the
  install works on a machine nobody prepared for it; a third-party
  first-install report is still welcome, because `[[build]]` is exactly the
  step the development route never runs.
- **An installed plugin's `herdr-draft version` prints no `build` line**, and
  that is correct rather than a stamping failure: herdr runs `[[build]]` as a
  plain argv with no shell, so there is nowhere for `git describe` to run. A
  `commit` line is still there, from the Go toolchain's own VCS stamp, which
  a build from a git *worktree* does not produce.

## Manual smoke

`docs/manual-smoke.md` is the pre-release matrix and is deliberately not in
CI: every scenario creates real herdr sessions. **Read its boxed warning
before running `herdr-draft create` anywhere** — herdr exports the pane
variables into every shell, so a `create` in your own session creates a
real session there.

`just smoke <fresh-repo>` runs the real form in an ordinary pane, which is
the half of the matrix you can drive yourself. The popup path needs a human:
the real popup has no pane id of its own, so nothing can send keys to it.

`just live` is the cheaper cousin and not part of the matrix at all: the same
binary under a pty, every input stubbed under a scratch `HOME`, screen to
stdout. It creates nothing and needs no herdr, so it is the fast way to answer
"what does this look like at that width" before reaching for a real session.
See [`hack/live/README.md`](hack/live/README.md). It needs Python and installs
`pyte` into a scratch venv on first use; nothing about it is on the `[[build]]`
path, and the version is pinned beside staticcheck's in the justfile.

## Security

See [`SECURITY.md`](SECURITY.md). Report anything exploitable privately
rather than in an issue.
