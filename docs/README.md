# Documentation

## Using herdr-draft

Read these in order the first time; after that, go straight to the page you
need.

1. [The project README](../README.md): what herdr-draft is, installing it,
   and the fast path.
2. [The form](the-form.md): every row, panel and key, and what happens after
   you create.
3. [Configuration](configuration.md): `config.toml`, a repository's
   `.herdr-draft.toml`, and where each default comes from.
4. [Headless `create`](create.md): creating sessions from scripts and
   agents, with the flags, JSON output and exit codes.
5. [The `/spawn` skill](spawn-skill.md): teaching Claude Code agents to hand
   work off through `create`.
6. [Claude accounts](account-picker.md): the `account` row, clauth, and the
   account picker protocol.
7. [Troubleshooting](troubleshooting.md): problems and fixes, known
   limitations, and the state herdr-draft keeps.

[SECURITY.md](../SECURITY.md) states what the plugin runs, reads and sends,
and how to report a vulnerability. [CHANGELOG.md](../CHANGELOG.md) says what
each release contains.

## Contributing

- [CONTRIBUTING.md](../CONTRIBUTING.md): building, the `just check` gate,
  golden frames, conventions, and cutting a release.
- [manual-smoke.md](manual-smoke.md): the pre-release matrix, run by hand
  against a real herdr. Read its warning before running `herdr-draft create`
  anywhere.
- [hack/live/README.md](../hack/live/README.md): `just live`, which runs the
  real form under a pseudo-terminal with every input stubbed.

## Design records

`specs/` holds the design documents the code was built from. They are
records, not user documentation: each describes intended behaviour at the
time it was written, and later documents replace parts of earlier ones.
Where they disagree with the user guide above, the user guide describes what
ships.

Read them in this order. The first four form a chain, each replacing parts
of the ones before it. The rest each add one feature, and amend the earlier
sentences that feature changes. Every document's header says what it
replaces or amends, and itemises the sentences that stop being true.

| Document | What it covers | Status |
|---|---|---|
| [v1 design](specs/2026-08-31-herdr-draft-design.md) | the original design: architecture, the submit pipeline, config and state, licensing and provenance | implemented; its form (§6) and skin (§7) superseded by v2 |
| [v2 design](specs/2026-09-02-herdr-draft-v2-design.md) | the redesign: one line per row, the key grammar, layered defaults, `.herdr-draft.toml`, headless `create` | implemented; its screen (§4), skin (§7), degradation (§9) and `account` row superseded by v3 |
| [v3 design](specs/2026-09-02-herdr-draft-v3-design.md) | filling the popup, palette and contrast, panels as tables, the clauth `account` row, provenance | implemented |
| [placement and pane ownership](specs/2026-09-03-placement-and-pane-ownership-design.md) | where a session lands with a worktree, and which pane the agent owns | implemented; its own §14 reverses §5.3 and §6.1, and §2.4 and §8.1 are retracted by the correction below |
| [herdr membership assessment](specs/2026-09-07-herdr-membership-assessment.md) | the reproductions behind the correction | evidence, kept verbatim |
| [herdr §8.1/§8.2 correction](specs/2026-09-08-herdr-8.1-8.2-correction.md) | retracts two claims the placement design made about herdr | correction note |
| [`/spawn` skill](specs/2026-09-16-spawn-skill-design.md) | the agent-facing skill and `herdr-draft skill` | implemented |
| [pane-reaper ready](specs/2026-09-17-pane-reaper-ready-design.md) | the prompt's `keep · reap` toggle and `[reaper]` | implemented |
| [agent options](specs/2026-09-18-agent-options-design.md) | the `options` row: model, effort and permission mode | implemented |
| [draw first](specs/2026-09-21-draw-first-design.md) | drawing the form at once, before the Linear key, clauth and the account picker have answered | approved, **not implemented** |

[CONTRIBUTING.md's "Spec citations"](../CONTRIBUTING.md#spec-citations)
explains how code comments cite these documents.
