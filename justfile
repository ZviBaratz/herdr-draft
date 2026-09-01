test:
    go test ./...

check:
    test -z "$(gofmt -l .)"
    go vet ./...
    go test ./...

build:
    go build -o bin/herdr-draft ./cmd/herdr-draft
