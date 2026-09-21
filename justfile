# staticcheck_version is the ONE machine-readable pin for this repo's only
# dev-tool dependency outside go.mod. Both the `unused` recipe below and
# .github/workflows/check.yml read it from here -- CI with
# `just --evaluate staticcheck_version` -- so the version cannot drift
# between what a contributor runs and what the gate runs.
#
# It deliberately does NOT live in go.mod as a `tool` directive, which is
# otherwise the modern way to pin one. staticcheck 2026.2.1 requires Go
# 1.26, so `go get -tool` rewrites this module's own `go` line 1.25 -> 1.26
# -- and herdr runs [[build]]'s `go build` on the USER's machine at install
# time, so that would raise the floor for everyone installing the plugin to
# pin a linter they never run. A dev tool must not narrow who can install.
staticcheck_version := "2026.2.1"

# pyte_version pins the terminal emulator hack/live/drive.py reads the
# screen with, on exactly the terms staticcheck_version is pinned on: one
# machine-readable place, and nothing else in the repo names the number.
#
# It is not a dependency of this project in any sense a user meets. It is
# installed on demand into a venv under a scratch directory, by the
# `_live-venv` recipe below and nowhere else, for `live` and
# `live-selftest`; `hack/` is not a Go package, [[build]]
# builds ./cmd/herdr-draft, and `just check`'s four steps never reach
# either. Nothing here may narrow who can install the plugin -- which is
# the same rule the staticcheck note above states, and the reason a second
# language in the tree is acceptable while a second go.mod requirement
# would need arguing.
pyte_version := "0.8.2"

# live_venv is that scratch venv. A backtick rather than just's own
# env_var_or_default, so an empty TMPDIR falls back exactly as the shell's
# ${TMPDIR:-/var/tmp} does in the recipes that share the directory.
live_venv := `echo "${TMPDIR:-/var/tmp}/herdr-draft-live-venv"`

test:
    go test ./...

# unused is the gate `go vet` cannot be: vet does not report unused
# unexported code at all, and that blind spot is how the whole v1
# drop-lines cascade and then half of #25's filter-count work sat in a
# green tree (#16 item 1). `-tests=false` is the load-bearing flag --
# staticcheck counts a test caller as usage by default, so without it a
# symbol kept alive only by the test that exists to call it looks used,
# which is the exact shape both of those defects had. A symbol that is
# deliberately test-only carries a `//lint:ignore U1000 <reason>` at its
# declaration instead of being deleted; see rowvalues.go's rowEllipsis
# and rowlayout.go's frame.lines for the two the project keeps and why.
#
# staticcheck is a dev-tool dependency, not a module one: it is not in
# go.mod and nothing else in the repo installs it, hence the guard. The
# U1000 baseline was set clean against the pinned version above; a newer
# release refining `unused` may report symbols this tree does not, which
# is a finding to read rather than a break to work around.
unused:
    #!/usr/bin/env bash
    set -euo pipefail
    if ! command -v staticcheck >/dev/null 2>&1; then
        echo "just unused needs staticcheck {{staticcheck_version}}:" >&2
        echo "    go install honnef.co/go/tools/cmd/staticcheck@{{staticcheck_version}}" >&2
        echo "or run it without installing:" >&2
        echo "    go run honnef.co/go/tools/cmd/staticcheck@{{staticcheck_version}} -checks U1000 -tests=false ./..." >&2
        exit 1
    fi
    # A mismatch is reported, not enforced. The U1000 baseline was set
    # clean against the pinned version; a newer release refining `unused`
    # may report symbols this tree does not, and the comment above says
    # that is a finding to read rather than a break to work around. Failing
    # here would turn every upgrade into a blocked checkout.
    have="$(staticcheck -version 2>/dev/null | grep -oE '[0-9]{4}\.[0-9]+(\.[0-9]+)?' | head -1 || true)"
    if [[ -n "$have" && "$have" != "{{staticcheck_version}}" ]]; then
        echo "note: staticcheck $have installed, {{staticcheck_version}} pinned -- findings may differ from CI's" >&2
    fi
    # Echoed because a shebang recipe runs as one script, so just no
    # longer prints the line the way it did for the plain recipe -- and
    # `just check`'s value is partly that you can see the four gates go by.
    echo "staticcheck -checks U1000 -tests=false ./..."
    staticcheck -checks U1000 -tests=false ./...

check:
    test -z "$(gofmt -l .)"
    go vet ./...
    just unused
    go test ./...

# build stamps `main.build` with `git describe`, so a binary built from a
# working tree can say exactly which commit produced it. That is a separate
# line from the version, which always comes from herdr-plugin.toml: before
# any tag exists `git describe --always` returns a bare hash, and folding
# the two together meant the version line stopped naming a version at all.
#
# herdr's own [[build]] in herdr-plugin.toml deliberately does NOT do this
# and cannot: it runs a plain argv with no shell, so there is nowhere to run
# `git describe`. An installed plugin falls back to the manifest-declared
# version, which is the right answer there anyway -- it is the value herdr
# itself displays for the install, so the two agree.
build:
    go build -ldflags "-X main.build=$(git describe --tags --always --dirty 2>/dev/null || echo '')" -o bin/herdr-draft ./cmd/herdr-draft

# smoke runs the real form in an ordinary pane with no popup --
# docs/manual-smoke.md's "Route B", which was a copy-paste block until a
# release pass got tired of pasting it. Everything the form reads is real
# (herdr, git, Linear, clauth, your own config and state directories) and a
# submit is a real submit -- it creates a worktree, a workspace, a tab and
# an agent -- so leave by `esc` unless you mean it.
#
# The four exports are what herdr itself hands a launched plugin.
# workspace_cwd and focused_pane_cwd are what the project row resolves
# from, which is why this cd's into the target repo before building the
# context JSON: without that the form would open on THIS repo and the walk
# would test the wrong tree.
#
# The repo argument is deliberately required rather than defaulted. The
# manual-smoke doc is explicit that each scenario needs its own fresh
# throwaway repo, because branch and projects.json state from a previous
# run changes what the next one is actually testing:
#     mkdir -p /var/tmp/hd-smoke-1 && cd /var/tmp/hd-smoke-1 \
#         && git init -q -b main && git commit -q --allow-empty -m init
#
# This covers the form's own content and its submit pipeline. It does NOT
# exercise the popup wrapper -- only Route A does, and the popup has no
# pane id of its own to send keys to, so that half stays a human's job.

# Route B: the real form in an ordinary pane. Needs a fresh throwaway repo.
smoke repo:
    #!/usr/bin/env bash
    set -euo pipefail
    : "${HERDR_WORKSPACE_ID:?not inside a herdr pane -- open one and re-run}"
    : "${HERDR_TAB_ID:?not inside a herdr pane -- open one and re-run}"
    : "${HERDR_PANE_ID:?not inside a herdr pane -- open one and re-run}"
    bin="$PWD/bin/herdr-draft"
    [[ -x "$bin" ]] || { echo "no binary at $bin -- run 'just build' first" >&2; exit 1; }
    [[ -d "{{repo}}/.git" ]] || { echo "{{repo}} is not a git repo -- see the recipe's comment for the one-liner" >&2; exit 1; }
    cd "{{repo}}"
    export HERDR_BIN_PATH="$(command -v herdr)"
    export HERDR_PLUGIN_ID="zvibaratz.draft"
    export HERDR_PLUGIN_CONFIG_DIR="$(herdr plugin config-dir zvibaratz.draft)"
    export HERDR_PLUGIN_STATE_DIR="${XDG_STATE_HOME:-$HOME/.local/state}/herdr/plugins/zvibaratz.draft"
    export HERDR_PLUGIN_CONTEXT_JSON="$(printf \
        '{"workspace_id":"%s","workspace_cwd":"%s","tab_id":"%s","focused_pane_id":"%s","focused_pane_cwd":"%s"}' \
        "$HERDR_WORKSPACE_ID" "$PWD" "$HERDR_TAB_ID" "$HERDR_PANE_ID" "$PWD")"
    echo "repo:     $PWD"
    echo "invoking: workspace $HERDR_WORKSPACE_ID  tab $HERDR_TAB_ID  pane $HERDR_PANE_ID"
    echo "esc leaves without creating"
    echo
    exec "$bin"

# live drives the real form under a pty and prints what a terminal would
# show -- docs/manual-smoke.md's Route B, automated as far as it goes.
#
# Unlike `smoke` this needs no herdr, no pane and no repository of yours:
# every input the form reads is a stub under a scratch HOME, so it creates
# nothing, spends no account quota and cannot submit anything real. That is
# what makes it safe to run in a loop while reading a screen.
#
# It drives its OWN binary, not bin/herdr-draft (#302). Linear is the one
# input a scratch HOME cannot stub, because the endpoint is compiled in:
# so this links a copy with `-X` pointing internal/linear's defaultEndpoint
# at a loopback port, and drive.py serves hack/live/linear-issues.json
# there. That copy differs from `just build`'s by that one string -- and
# says so, since `go version -m` prints the -ldflags it was linked with.
# It goes under $TMPDIR, never bin/, for two reasons: `just smoke` runs
# bin/herdr-draft against the REAL Linear and must never pick this one up,
# and this one sends whatever Linear key it holds to a loopback port, which
# is fine for the stub's fake key and wrong for anyone's real one. Do not
# run it by hand -- which the recipe makes hard to do by accident, since it
# deletes each run's copy on the way out.
#
# A fresh free port on every run, which costs a relink -- measured at
# 0.4s with a warm cache -- rather than a fixed port that a second run, or
# anything else on the machine, could already hold. The port is freed
# between being picked and drive.py binding it, and drive.py says so
# plainly if something took it in that window.
#
# And a fresh BINARY on every run, for the same reason one layer down.
# The first version linked every run to one path, so a second `just live`
# started a moment after the first relinked it between the first run's
# link and its exec: the first form then dialled the second run's port,
# found nothing, and printed an unavailable issue row with exit 0 -- three
# trials in three. Each run now links into its own directory and deletes
# it on the way out, which is why this runs drive.py rather than exec'ing
# it. The scratch tree under --root is shared too; drive.py takes a lock on
# it for the length of the run.
#
# The venv lives OUTSIDE the tree drive.py manages, because `--fresh`
# deletes that tree and rebuilding the venv on every pass is the slowest
# thing here by an order of magnitude. The linked binary lives outside it
# for the same reason.
#
#     just live --size 101x30
#     just live --size 57x18 --keys tab,tab,tab --row -1
#
# COMMAS, not spaces, in --keys through this recipe. `{{ARGS}}` is a
# space-joined string that the shell then re-splits, so a quoted
# `--keys 'tab tab tab'` arrives as three arguments and argparse refuses
# the last two. drive.py splits key tokens on either separator, so the
# comma form is the one that survives both callers; `python3
# hack/live/drive.py --keys 'tab tab tab'` is fine directly. The same
# limit applies to any other flag whose value has a space in it, which is
# why --type and --line are worth running directly when they do.
#
# `set -f` is not decoration either. Without it the shell globs {{ARGS}}
# on the way past, and a key token like `tab*3` silently becomes a
# filename that happens to match -- the same shape as #72's unquoted
# model id and #209's `[1m]`.
#
# A leading `--` is swallowed rather than passed on: just does not need
# one before a recipe's own flags, but typing it is the reflex, and
# argparse's answer to a stray `--` here is an unrecognised-argument
# error that names the flags you got right.

# Route B under a pty: the real form, stubbed inputs, screen to stdout.
live *ARGS: _live-venv
    #!/usr/bin/env bash
    set -euo pipefail
    venv="{{live_venv}}"
    mkdir -p "${TMPDIR:-/var/tmp}/herdr-draft-live-bin"
    livebin="$(mktemp -d "${TMPDIR:-/var/tmp}/herdr-draft-live-bin/run.XXXXXX")"
    trap 'rm -rf "$livebin"' EXIT
    port="$("$venv/bin/python" -c 'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()')"
    go build -ldflags "-X main.build=$(git describe --tags --always --dirty 2>/dev/null || echo '') -X github.com/ZviBaratz/herdr-draft/internal/linear.defaultEndpoint=http://127.0.0.1:${port}/graphql" \
        -o "$livebin/herdr-draft" ./cmd/herdr-draft
    set -f
    set -- {{ARGS}}
    [[ "${1:-}" == "--" ]] && shift
    "$venv/bin/python" hack/live/drive.py --binary "$livebin/herdr-draft" --linear-port "$port" "$@"

# live-selftest is drive.py's terminal emulator on its own: the fixes #305
# made to pyte (SU and SD, DL and IL, the kitty keyboard sequences it would
# draw as text, a sequence cut in two by a read) fed byte strings and
# compared with what a terminal shows. No binary, no pty, no stub, well
# under a second. `just live` checks every reading against a full repaint,
# but only on the paths a walk reaches -- and no walk emits DL or IL, or
# can choose where a read ends. Run it after touching drive.py's
# `emulator()`, or after moving pyte_version.
#
# Like `live` it is not part of `just check`, which must not need Python.
#
# `-B` because the way to prove a case pins something is to mutate
# emulator() and run this again, and a .pyc is trusted by source mtime,
# in whole seconds, and size: two one-byte mutations inside the same
# second ran the FIRST one's bytecode and reported the second as
# surviving. Nothing else imports drive.py, so with this there is never a
# cached copy of it to go stale.

# The emulator's own tests, fed bytes directly. Fast; needs python3.
live-selftest: _live-venv
    "{{live_venv}}/bin/python" -B hack/live/selftest.py

# _live-venv makes that venv with the pinned pyte, and repairs it.
#
# Probed by IMPORTING pyte, not by the interpreter existing: a first
# run interrupted between `venv` and `pip install` leaves an
# executable python with nothing in it, and a guard that only checks
# for the binary never repairs it -- it just tells you to run the
# command that failed.
_live-venv:
    #!/usr/bin/env bash
    set -euo pipefail
    venv="{{live_venv}}"
    if ! "$venv/bin/python" -c 'import pyte' >/dev/null 2>&1; then
        command -v python3 >/dev/null 2>&1 || { echo "just live and just live-selftest need python3" >&2; exit 1; }
        rm -rf "$venv"
        echo "creating $venv with pyte {{pyte_version}}"
        python3 -m venv "$venv" || {
            echo "could not create a venv -- on Debian/Ubuntu that is the python3-venv package" >&2
            exit 1
        }
        "$venv/bin/pip" install --quiet "pyte=={{pyte_version}}" || {
            echo "could not install pyte {{pyte_version}} -- the half-built venv has been left at $venv" >&2
            exit 1
        }
    fi
