## What & why

<!-- What does this PR change, and what problem does it solve? Link issues with "Fixes #123". -->

## Checklist

- [ ] Local gate passes: `go mod tidy` is a no-op, `go build ./...`, `go vet ./...`, `gofmt -l -s .` prints nothing, `go test -race -covermode=atomic ./...`, `golangci-lint run`
- [ ] Observable behavior changes update the matching `docs/specifications/` document in this same PR
- [ ] No hand edits under `pkg/*/proto/v1/` — `.proto` changes made in `api/` and regenerated with `buf generate`
- [ ] Doc cross-references are path + heading anchor (never section numbers); renamed headings were grepped for inbound anchors
- [ ] New `internal/` packages include `README.md` + `CLAUDE.md` in the same commit
- [ ] No compiled artifacts outside `bin/`

## Notes for reviewers

<!-- Anything unusual: tradeoffs, follow-ups deferred, spec sections that deserve close reading. Delete if empty. -->
