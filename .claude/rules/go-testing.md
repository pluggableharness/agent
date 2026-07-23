---
paths:
  - "**/*.go"
---

# Testing standard

## Every `.go` file requires tests

Any `.go` file containing logic (i.e. not a pure type-alias or constant-only
file) gets a corresponding `_test.go` file. A PR/commit that adds or changes
behavior in a `.go` file without a matching test change is incomplete. The
only exemptions are `cmd/` thin-wiring mains and generated code under any
`pkg/<category>/proto/` directory.

## Test tiers

Three tiers, distinguished by build tag and by what they're allowed to touch.
One tier per file — a single `_test.go` file does not mix tiers.

| Tier | Build tag | Scope | May touch | Speed budget |
|---|---|---|---|---|
| **Unit** | *(none — default)* | One function/type in isolation | In-memory fakes only (see `go-layout.md`'s driver pattern — test against the interface with a fake driver, never a real backend) | ≤ 100ms per test |
| **Integration** | `//go:build integration` | One `internal/<feature>` package against a real dependency of that one feature (e.g. `internal/memory/drivers/sqlite` against a real sqlite file, a real go-plugin subprocess for one provider) | One real backend at a time; no network calls to third-party services | ≤ 5s per test |
| **E2E** | `//go:build e2e` | A full kernel session: config load, plugin handshake, a turn end-to-end | Everything — real plugins, real state backend | ≤ 60s per test |

Naming convention: `<name>_test.go` for unit, `<name>_integration_test.go` for
integration, `<name>_e2e_test.go` for e2e, so the tier is visible from the
filename alone even before reading the build tag.

## Coverage

- **≥80% statement coverage is the floor for every `internal/` package.**
  A package below 80% is not done.
- `internal/*/​` packages implementing pure domain logic (state machines,
  policy evaluation, cost/depth-budget arithmetic) target ~95% — they are
  I/O-free and deterministic, so there's no excuse for gaps.
- Generated code (`pkg/<category>/proto/`) and thin `cmd/` mains are excluded
  from the floor — there is no meaningful logic to cover.
- Coverage is measured with unit tests as the baseline (`go test -cover`);
  integration/e2e tests may raise it further but unit coverage alone must
  clear 80% per package.

## Framework and style

- Standard library `testing` only. No `testify`, no `assert`/`require`
  packages — write the `if got != want { t.Fatalf(...) }` by hand. This keeps
  test failures readable without a third-party diff formatter and keeps the
  plugin SDK's dependency footprint minimal for third-party plugin authors.
- Fakes, not mocking frameworks: implement the relevant `internal/<feature>`
  interface by hand under `drivers/fake/` (per `go-layout.md`). A fake is a
  real, small, readable implementation — not a generated mock with
  `.EXPECT()` call recording.
- `t.Parallel()` on every test that has no shared mutable state.
- `t.Helper()` on every test helper function so failures report the caller's
  line, not the helper's.
- `t.Cleanup()` for teardown, not deferred cleanup at the end of the test body
  — it composes correctly with helpers that also register cleanup.
- Table-driven tests for anything with more than 2-3 input/output cases.
- Golden files live under `testdata/` per package; regenerate them behind an
  explicit `UPDATE_SNAPSHOTS` (or equivalent) env-var gate, never silently.
- Concurrency-sensitive code (anything touching the plan/apply gate, the
  turn-level concurrency described in `docs/specifications/agent-loop/turn-algorithm.md`, or the sqlite state
  backend) runs under `go test -race` as a hard requirement, not a nice-to-have.

## Replay determinism in tests

Tests that exercise anything replay-adjacent (state backend, event ordering)
must assert against `sequence`, never wall-clock time — see `determinism.md`.
A test that depends on `time.Now()` ordering two events is a bug in the test,
not an acceptable flake.
