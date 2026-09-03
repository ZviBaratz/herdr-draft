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
