---
paths:
  - "**/*.go"
---

# Go coding standard

## Formatting

- `gofmt -s` and `goimports` are non-negotiable; no code merges unformatted.
- Tabs for indentation (gofmt default) — never spaces.
- Import groups, blank-line separated, in this order: stdlib, third-party,
  `github.com/pluggableharness/agent/...` internal. `goimports` enforces this; don't hand-order.
- Import aliases only to resolve a genuine collision (two packages named
  `context`-adjacent things) — never to shorten a name you find long.

## Naming

- Constructors: `New<Type>(...) (*Type, error)` — or a plain `New<Type>(...) *Type`
  when construction cannot fail. No `NewXxxImpl` or `MakeXxx`.
- Interfaces are named for the role they play (`Recaller`, `TokenCounter`),
  never prefixed `I` and never suffixed `Interface`.
- No package stutter: `memory.Store`, not `memory.MemoryStore`. Callers write
  `memory.Store`, which already says what it is.
- Acronyms keep uniform case: `TraceID`, `URL`, `parseHCL` — never `Id`, `Url`, `Hcl`.
- Short receiver names (1-2 letters, first letter(s) of the type), consistent
  across every method on that type.
- Exported identifiers: `PascalCase`. Unexported: `camelCase`. No exceptions
  for "it's just a constant."

## Documentation

- Every exported identifier (type, func, const, var) has a doc comment that
  starts with the identifier's own name, per godoc convention:
  `// Recall queries the memory backend for records matching req.`
- Every `internal/<feature>/` package has a `doc.go` with a package-level
  comment explaining what the package owns and, if applicable, which spec
  section it implements (e.g. `// Package memory implements the memory
  provider contract described in docs/specifications/memory/README.md.`).
- Comments explain *why*, not what the code already says. `// retry because
  the vendor API is eventually consistent`, not `// loop 3 times`.
- American spelling throughout (`serialize`, not `serialise`).

## Errors

- Wrap with context using `%w`: `fmt.Errorf("memory: recall: %w", err)`. The
  prefix is `<package>: <operation>:` — enough to locate the failure from the
  message alone.
- Sentinel errors: `var ErrNotFound = errors.New("memory: record not found")`.
  Structured errors needing data: `type NotFoundError struct { ID string }`
  implementing `error`. Callers use `errors.Is` / `errors.As`, never string
  matching on `Error()`.
- Never `return nil, nil` as a "no result, no error" convention — return a
  sentinel or a typed error, or a zero-value result with an explicit ok bool,
  and document which.
- A function returns an error or logs it, never both. Logging-and-returning
  produces duplicate log lines when the caller also logs.
- No silent fallthrough on an unexpected error branch (`nilerr` pattern) —
  every non-nil error either returns, wraps, or is explicitly and visibly
  discarded with a comment saying why it's safe to ignore.

## Idioms

- `func init()` is banned. Explicit construction and explicit wiring in
  `cmd/`, always. If package-level state seems to need init-time setup,
  it's a sign the state should be a constructor argument instead.
- No global mutable state. The one exception is a package-level
  `slog.Default()`-style logger handle, and only if it is safe to leave unset.
- Functional options (`func WithTimeout(d time.Duration) Option`) for
  constructors with more than ~2 optional parameters — not config structs
  with a dozen zero-value-means-default fields.
- `context.Context` is always the first parameter, named `ctx`, and is never
  stored on a struct. If a type needs a context across multiple calls,
  that's a sign the context should be passed per-call instead.
- Use stdlib symbolic constants (`http.MethodGet`, not `"GET"`).
- `for i := range n` (Go 1.22+ range-over-int) instead of
  `for i := 0; i < n; i++` where the loop only needs the index.
- Functions stay small: soft cap ~120 lines / ~80 statements, cyclomatic
  complexity soft cap 20. Past that, extract a helper — don't argue the
  exception, just split it.
- Preallocate slices/maps when the final size is known (`make([]T, 0, n)`).

## Linting

`golangci-lint` (v2, maximal enabled set) runs clean before any change is
considered done, once the toolchain exists in this repo. Do not claim a
`golangci-lint run` or `go vet ./...` passed unless it was actually run —
this repo has no build/test commands wired up yet (see project `CLAUDE.md`).
