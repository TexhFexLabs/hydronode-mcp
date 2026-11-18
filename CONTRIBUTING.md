# Contributing

Thanks for helping improve `hydronode-mcp`. Small, focused pull requests are easiest to review.

## Setup

Requires Go 1.24+.

```sh
go test -race ./...
go run honnef.co/go/tools/cmd/staticcheck@v0.8.2 ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
```

## Rules

- Test first: write a failing test against `httptest.Server`, see it fail, then implement the change.
- Never include real hosts, real tokens or personal data in tests, comments or commits. Use
  `example.com` and obviously fake tokens (`hoa_test_…`).
- Keep tool responses condensed: history tools return at most 48 buckets, list tools at most 20 items.
- Never log request bodies, tool arguments, backend payloads or raw bearer tokens.

## Before you push

```sh
gofmt -s -w .
go vet ./...
go test -race ./...
```
