---
applyTo: "**/*.go"
---

# Go conventions

The full rules live in `.claude/rules/go-style.md`, `go-layout.md`, `go-testing.md`, `determinism.md`, `logging-telemetry.md`, and `grpc.md`; these are the ones that matter most in a pull request.

## Style

- `gofmt -s` and goimports formatting; every exported symbol has a godoc comment starting with its name, American spelling.
- Constructors are `New<Type>`; no `I` prefixes, `Interface` suffixes, or package-name stutter; initialisms cased as `TraceID`, `URL`.
- Wrap errors with `%w` using `pkg: op:` prefixes; use sentinel or typed errors with `errors.Is`/`errors.As`; never `return nil, nil`; never both log an error and return it.
- No `init()` functions, no global mutable state; functional options for configuration; `context.Context` is the first parameter and is never stored in a struct.

## Layout

- `cmd/` stays thin — parse config, construct via `internal/` constructors, call `Run`.
- Pluggable concerns put the interface at `internal/<feature>/` and each implementation in a leaf package under `drivers/<name>/`; only `drivers/drivers.go` knows the full driver set; no driver imports another driver.
- No junk-drawer packages (`util`, `common`, `helpers`).
- A new `internal/` package ships its `README.md` and `CLAUDE.md` in the same commit that creates it.

## Testing

- Standard library `testing` only — no testify or mock frameworks; hand-written fakes implement the package's own interfaces.
- Table-driven tests with `t.Parallel()`, `t.Helper()`, `t.Cleanup()`; coverage floor is 80% per `internal/` package (higher for pure-domain packages).
- Test tiers are separated by build tags: untagged unit, `integration`, `e2e` — one tier per file.

## Determinism, telemetry, and gRPC

- The persisted `sequence` counter is the only ordering authority — never `time.Now()` for ordering, and no serialized output may depend on Go map iteration order (sort keys first).
- Code that does I/O or crosses a process boundary uses `log/slog` plus `internal/telemetry`; pure-domain packages (`internal/policy`, `internal/agentprofile`) import neither. Never log secrets.
- gRPC handlers map failures to specific `google.golang.org/grpc/codes` values, never `Unknown`; context cancellation (`codes.Canceled`) is normal control flow, not an error to report.
