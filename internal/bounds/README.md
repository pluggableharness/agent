# internal/bounds

The kernel's loop-bound tracking from [`docs/specifications/agent-loop/turn-algorithm.md`](../../docs/specifications/agent-loop/turn-algorithm.md#independent-bound-dimensions) — three independent dimensions (`max_turns`, `max_cost_usd`, `max_wall_clock_s`) checked at step 17 of every turn, plus the running-cost accumulator [`#cost-accounting`](../../docs/specifications/agent-loop/turn-algorithm.md#cost-accounting) requires.

## What this package does

- `bounds.go` — `Limits` (the three configured bounds), `Fired` (which dimension tripped, if any) with its `Status()` mapping to a terminal `sessionv1.SessionStatus`, and `Tracker` — one session's turn counter, cost accumulator, and bound-checking logic.
- This package is pure domain logic: no I/O, no clock reads, no logging or telemetry (see `.claude/rules/logging-telemetry.md`'s pure-domain exemption). `Check` takes the elapsed wall-clock duration as a parameter rather than reading a clock itself, so the whole package stays a deterministic function of its inputs.
- Routing a fired bound through the graceful-degradation path (one more tool-free turn, then end the session with the mapped status) is the caller's job — this package only reports which bound fired.

## Root-sessions-only scope

This build of the kernel has no sub-agent spawning yet, so no session tree exists in production — every real caller constructs a `Tracker` with `parent == nil`. `Tracker` is nonetheless built with full parent-chain plumbing (`NewTracker(limits, parent)`, `Debit`/`RemainingCostUSD` walking to the root) from the start, because [`#cost-accounting`](../../docs/specifications/agent-loop/turn-algorithm.md#cost-accounting) requires cost to roll up the whole session tree once one exists — the same reasoning [`subagents.md#depth-limits`](../../docs/specifications/agent-loop/subagents.md#depth-limits) already establishes for `max_depth`'s min-over-ancestors resolution. Building and testing this seam now means nothing here needs to change when session-tree support lands; see `doc.go` and `CLAUDE.md` for more.

## Public API sketch

```go
tr := bounds.NewTracker(bounds.Limits{MaxTurns: 50, MaxCostUSD: 5.00, MaxWallClock: 10 * time.Minute}, nil)

tr.ObserveTurn()
tr.Debit(usageEvent.CostUSD)

if fired := tr.Check(time.Since(sessionStart)); fired != bounds.FiredNone {
	// route through the graceful-degradation path; fired.Status() gives
	// the terminal sessionv1.SessionStatus to persist.
	status := fired.Status()
}
```

A child session's tracker, once session-tree support exists:

```go
child := bounds.NewTracker(childLimits, parentTracker)
child.Debit(usd) // rolls up: parentTracker's (and its ancestors') cost accumulators are debited too
```

## Zero-value convention

`Limits{}` (Go's zero value) means every dimension is unbounded — a zero `MaxTurns`/`MaxCostUSD`/`MaxWallClock` never fires, regardless of how many turns run or how much is spent or how much time elapses. This resolves an ambiguity the spec doesn't spell out literally: a literal zero bound would otherwise fire before the first turn, which is never what an omitted HCL field defaulting to Go's zero value is meant to express.

## Testing notes

- Unit tests are the only tier here (`.claude/rules/go-testing.md`) — pure domain logic, no fakes, no external dependencies, targeting ~95% coverage.
- `TestConcurrentAccess` exercises `ObserveTurn`/`Debit`/`Check` from many goroutines under `go test -race` — this package's contract is safe-for-concurrent-use, matching turn-algorithm.md's concurrent data-source tool calls within one turn.
- `TestParentChainRollup` and `TestRemainingCostUSDReflectsAncestorsTighterBudget` exercise the currently-unused-in-production parent-chain seam directly, with a synthetic 3-level chain, since production never builds one yet.
