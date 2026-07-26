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
| **Unit** | *(none — default)* | One function/type in isolation | In-memory fakes only (see `go-layout.md`'s driver pattern — test against the interface with a fake driver, never a real backend) | ≤ 100ms per test, ≈0ms for most — see [Unit-test speed](#unit-test-speed) |
| **Integration** | `//go:build integration` | One `internal/<feature>` package against a real dependency of that one feature (e.g. `internal/memory/drivers/sqlite` against a real sqlite file, a real go-plugin subprocess for one provider) | One real backend at a time; no network calls to third-party services | ≤ 5s per test |
| **E2E** | `//go:build e2e` | A full kernel session: config load, plugin handshake, a turn end-to-end | Everything — real plugins, real state backend | ≤ 60s per test |

Naming convention: `<name>_test.go` for unit, `<name>_integration_test.go` for
integration, `<name>_e2e_test.go` for e2e, so the tier is visible from the
filename alone even before reading the build tag.

## Unit-test speed

Unit tests are the pre-commit inner loop — run constantly, pre-emptively,
on every save. They MUST be near-instant so that running them is never a
reason to hesitate. The budget is not aspirational:

- **≤ 100ms per unit test, and the overwhelming majority land at
  effectively 0ms** — that near-zero common case is the real target; the
  100ms figure is a ceiling, not a license to spend it. A test that can't
  hit the ceiling is almost always misclassified: it is reaching a real
  *external* dependency — the network, a real subprocess, a separate server
  — which makes it an integration test (`//go:build integration`,
  `_integration_test.go`), not a unit test. Move it; do not slow the unit
  tier to accommodate it. An embedded, in-process store opened under
  `t.TempDir()` (the sqlite state backend, via modernc's pure-Go driver, is
  the one in-tree case) stays unit-tier, but it is the *heavy* end of the
  budget and the only place approaching the ceiling is acceptable — keep its
  per-test setup minimal (open one fresh DB per test, run no migrations you
  can avoid) rather than treating the 100ms as headroom to fill.
- **The whole unit suite's actual test time stays comfortably sub-second.**
  Measure the test time the toolchain reports (`ok <pkg> 0.0xs`), not the
  wall clock: wall clock also includes compiling ~one test binary per package
  and, under `-race`, the race runtime's own fixed per-binary startup (on the
  order of a second per package on this host, independent of what the tests
  do). Neither is test-authored cost; don't chase them by weakening a test.

### No fixed-duration sleeps on the happy path

The dominant way a fast test turns slow is a hard-coded wait — most often a
**negative assertion** that sleeps to "wait and see" that something does NOT
happen:

```go
// WRONG — burns the full 100ms on every passing run, and is still only
// probabilistic (a slow enough machine delivers the event at 101ms).
select {
case ev := <-got:
    t.Fatalf("unexpected delivery: %+v", ev)
case <-time.After(100 * time.Millisecond):
}
```

A fixed sleep is the worst of both worlds: it is *both* slow on every green
run *and* flaky, because no constant is simultaneously short enough to be
fast and long enough to be certain. Replace it with a deterministic check —
one of these two, never a `time.After`/`time.Sleep` guess:

- **Provable quiescence, then a non-blocking check.** When the producer is
  provably stopped — e.g. a `Close`/cancel that synchronously joins the
  producer goroutine has returned — nothing can still be in flight, so assert
  the channel is empty *instantly* with a `select { case v := <-ch: fail;
  default: }`. `internal/eventbus`'s `assertNoPending` is the reference
  helper, with a doc comment stating exactly the precondition that makes it
  sound.
- **Sentinel flush for a still-live producer.** When the producer is still
  running (a subscriber that should have filtered an event out, say), you
  can't check instantly — but you also don't need to sleep. Push a *sentinel*
  through the same ordered path and wait for it with the normal positive-wait
  helper: once the sentinel arrives, FIFO ordering guarantees anything that
  was supposed to precede it already has, so its absence is proven, not
  guessed. `internal/eventbus`'s filter tests are the reference pattern.

A **positive** wait — blocking on a channel for something that SHOULD arrive,
guarded by a generous `time.After` that only fires on failure — is fine and
encouraged: it returns the instant the value arrives and only spends its
timeout when the test is already failing. Keep those timeouts generous (a few
seconds) so a loaded CI box never trips them; they cost nothing on green runs.
`recvOrTimeout` in `internal/eventbus` is the reference shape.

### `-race` is the gate, not the inner loop

`go test -race` remains mandatory for concurrency-sensitive code and is the
CI gate (project `CLAUDE.md`). But the race runtime's fixed per-binary
startup dominates a fast package's wall time, so the tight edit-save-test
loop can run the plain `go test ./<pkg>/` for instant feedback and reserve
`-race` (and the full `./...` sweep) for before a change is considered done.
Speed is achieved by removing real work from the tests, never by dropping
`-race` from the gate.

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
