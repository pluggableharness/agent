---
applyTo: "**"
---

# Repository context

PluggableHarness Agent is an AI coding harness built as a Go microkernel: the kernel owns plugin lifecycle, the plan/apply policy gate, configuration, and the session log; everything opinionated (models, tools, context, memory, frontends, widgets) runs as an out-of-process gRPC plugin.

## Source of truth

`docs/specifications/` is authoritative for everything it covers. RFC 2119 keywords (MUST/SHOULD/MAY) are load-bearing conformance language, not style. Where code and spec disagree, the spec wins — the code is the bug. Any pull request that changes observable behavior MUST update the relevant spec in the same PR.

## Layout

| Path | Contents |
|---|---|
| `api/` | `.proto` sources; buf module root (`buf.yaml`, `buf.gen.yaml`) |
| `internal/` | All real logic; never imported outside this module; interface-plus-drivers pattern |
| `pkg/` | Plugin-author surface: hand-written SDK per category plus buf-generated stubs under `pkg/<category>/proto/v1/` |
| `docs/specifications/` | Protocol and kernel contracts — the authoritative spec tree |
| `docs/first-party/` | First-party tool and provider catalog; not part of the protocol spec |
| `cmd/` | Thin entrypoints only — flag parsing and wiring, no business logic |

## Hard rules

- Never hand-edit anything under `pkg/*/proto/v1/` — it is 100% `buf generate` output, committed, and CI fails on drift. Fix the `.proto` under `api/` and regenerate.
- Compiled artifacts go to the gitignored `bin/` directory only — never the repo root, never `/tmp`. GoReleaser's `dist/` is the sole exception.
- Never create, move, or delete `v*` tags; they are admin-only and drive releases.

## Validation gate

Every change must pass the same checks CI runs:

```sh
go mod tidy        # must be a no-op (git diff --exit-code go.mod go.sum)
go build ./...
go vet ./...
gofmt -l -s .      # must print nothing
go test -race -shuffle=on -covermode=atomic ./...
buf lint
buf format --diff --exit-code
golangci-lint run
govulncheck ./...
```
