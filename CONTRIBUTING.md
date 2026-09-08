# Contributing

Patches are welcome. This file exists because several of this project's
conventions are non-obvious and will be got wrong by default — not because
the process is heavy.

`CLAUDE.md` covers much of the same ground in more depth. It is written for
an agent working in the repository; this file is the same material for a
person, plus the parts a contributor needs and an agent does not.

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

CI runs exactly this recipe, on Linux and macOS, so a green run locally is
a green run there.

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

staticcheck is the one dev tool not in `go.mod`. Run `just unused` without
it and the error names the pinned install line.

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

Four design documents, and the citation convention distinguishes them:

| citation | document |
|---|---|
| bare `spec §N` | `docs/specs/2026-08-31-herdr-draft-design.md` (v1) |
| `v2 spec §N` | `docs/specs/2026-09-02-herdr-draft-v2-design.md` |
| `v3 spec §N` | `docs/specs/2026-09-02-herdr-draft-v3-design.md` |
| `placement spec §N` | `docs/specs/2026-09-03-placement-and-pane-ownership-design.md` |

Each supersedes only the sections it names; everything else stays
authoritative, which is why plenty of live code cites v2 correctly. v3 §13
itemises every superseded sentence.

Most non-trivial doc comments cite a spec section or a live-checkpoint
finding. **Read them before changing the code they sit on** — they usually
record a non-obvious constraint rather than restating the code.

Citations into herdr's own source name a pinned commit
(`b1ff4582e9688f52ffb943cfa8bee4871ae122e4`), never a local path. A
citation that resolves only on one machine is not a citation.

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

## What not to propose

Some features were considered and explicitly declined, and re-proposing one
is a regression rather than an idea. The full lists are in v1 §16, v2 §16,
v3 §14 and the placement spec §13 — among them variant fan-out, draft
persistence, model/effort/permission-mode chip fields, writes back to
Linear, prompt-history reuse, a command bar, session recipes, row grouping
bands, and git ahead/behind in the header.

If you think one deserves revisiting, open an issue arguing against the
recorded reasoning rather than a PR implementing it.

## Manual smoke

`docs/manual-smoke.md` is the pre-release matrix and is deliberately not in
CI: every scenario creates real herdr sessions. **Read its boxed warning
before running `herdr-draft create` anywhere** — herdr exports the pane
variables into every shell, so a `create` in your own session creates a
real session there.

`just smoke <fresh-repo>` runs the real form in an ordinary pane, which is
the half of the matrix you can drive yourself. The popup path needs a human:
the real popup has no pane id of its own, so nothing can send keys to it.

## Security

See [`SECURITY.md`](SECURITY.md). Report anything exploitable privately
rather than in an issue.
