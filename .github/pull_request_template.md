<!--
Thanks for the patch. CONTRIBUTING.md has the conventions; the short version is below.
The commit body carries the rationale (why this shape, what was rejected), so the
description here can point at it rather than repeat it.
-->

## What and why

<!-- What changes for someone using herdr-draft, and why. `Closes #N` where it applies. -->

## Checklist

- [ ] `just check` is green locally (gofmt, go vet, `just unused`, go test).
- [ ] Golden frames regenerated **per package** where the screen moved (`go test ./internal/form/ -update`, then `./internal/app/ -update`), and I read the frame diffs.
- [ ] A change to the form has its counterpart in `herdr-draft create` (and the reverse), or says why not.
- [ ] Docs updated where behaviour changed (README, `docs/`, `herdr-draft create --help`).
