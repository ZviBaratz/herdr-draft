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
# U1000 baseline was set clean against staticcheck 2026.2.1; a newer
# release refining `unused` may report symbols this tree does not, which
# is a finding to read rather than a break to work around.
unused:
    @command -v staticcheck >/dev/null 2>&1 || (echo "just unused needs staticcheck: go install honnef.co/go/tools/cmd/staticcheck@2026.2.1  (or run it without installing: go run honnef.co/go/tools/cmd/staticcheck@2026.2.1 -checks U1000 -tests=false ./...)" >&2; exit 1)
    staticcheck -checks U1000 -tests=false ./...

check:
    test -z "$(gofmt -l .)"
    go vet ./...
    just unused
    go test ./...

build:
    go build -o bin/herdr-draft ./cmd/herdr-draft

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
