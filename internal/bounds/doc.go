// Package bounds implements the kernel's three independent loop-bound
// dimensions — max_turns, max_cost_usd, max_wall_clock_s — and the running
// cost accumulator that backs them, per
// docs/specifications/agent-loop/turn-algorithm.md#independent-bound-dimensions,
// #cost-accounting, and #limit-reached-behavior. Each dimension is tracked
// and checked independently; this package only reports which one (if any)
// fired for a given session at step 17 of the turn algorithm. Routing a
// fired bound through the graceful-degradation "one more tool-free turn,
// then end the session" path is the caller's responsibility, not this
// package's — see #limit-reached-behavior.
//
// # Root-sessions-only scope, and the parent-link seam
//
// This build of the kernel is root-sessions-only: there is no sub-agent
// spawning yet, and in practice no session tree exists. Every production
// caller constructs a Tracker with parent == nil. Despite that, Tracker is
// built with full parent-chain plumbing from day one —
// NewTracker(limits, parent) and Debit/RemainingCostUSD walking to the
// root — because #cost-accounting requires every usage event's cost_usd to
// be "atomically subtracted from remaining_cost_budget_usd at every session
// on the path from where the spend occurred up to the root," and the same
// reasoning docs/specifications/agent-loop/subagents.md#depth-limits
// already establishes for max_depth's min-over-ancestors resolution applies
// identically to cost. Building the seam now, and testing it thoroughly
// with a synthetic multi-level parent chain even though production never
// exercises it yet, means nothing in this package needs to change when
// session-tree support lands. This is a deliberate, tracked, scoped
// non-conformance with "no session tree exists" — not a shortcut that
// silently skips the requirement.
//
// # Pure domain, no instrumentation
//
// This package is pure domain logic — deterministic, I/O-free, safe for
// concurrent use via an internal mutex, and MUST NOT import log/slog or
// internal/telemetry (.claude/rules/logging-telemetry.md's pure-domain
// exemption). A caller performing I/O or crossing a process boundary logs
// or spans around a call into this package; this package itself never does.
package bounds
