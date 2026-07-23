// Package telemetry implements PluggableHarness Agent's OTel-native tracing and metrics
// module: distributed spans across the kernel's turn loop and the
// hashicorp/go-plugin subprocess boundary, plus a fixed set of metric
// instruments mirroring the kernel's own cost/bounds bookkeeping
// (state-backend.md §4.3, agent-loop.md §3.1).
//
// This package is kernel-internal instrumentation, not a hook subscriber:
// the RunTurn loop (not yet built) calls the Start* helpers in span.go
// directly at its 18 numbered steps and around each of the 9 hook-point
// dispatches (agent-loop.md §2, §4), so a span can carry the live ctx into
// downstream provider/tool RPCs and across the plugin gRPC boundary
// (grpchooks.go). An observe-mode hook subscriber, by contrast, receives a
// payload and has its return discarded (agent-loop.md §4.1) — it cannot
// thread a span through the kernel's real call chain, which is why tracing
// is not implemented as one.
//
// Telemetry is strictly side-band: it never writes to the events,
// cost_ledger, or plan_items tables, and never recomputes a value the
// kernel already computed elsewhere (see determinism.md and this package's
// CLAUDE.md). A replayed session MUST use the noop driver (drivers/noop).
//
// The exporter backend is the swappable concern (go-layout.md's driver
// pattern): Backend in telemetry.go is the interface, drivers/<name> holds
// one implementation each, and drivers/drivers.go is the sole
// name-to-constructor selector.
package telemetry
