// Package providercatalog defines the read-only lookup interface every
// agent-loop package uses to reach a live plugin, and is the only place
// those packages couple to plugin lifecycle at all.
//
// # Why this package exists
//
// The turn driver, hook dispatcher, plan/apply gate, tool scheduler,
// model caller, and context assembler all need the same four things: a
// dialed model client for a given agentprofile.ModelRef, a dialed tool
// client for a given "<provider>.<tool>" pair, the context providers in
// declaration order, and a plugin's HookSubscriberService client. None
// of them needs — or should be able to express — launching a
// subprocess, running a handshake, applying agent.hcl configuration, or
// tearing a plugin down. Routing all six through one narrow read-only
// interface keeps that asymmetry structural rather than merely
// conventional: an agent-loop package cannot start a plugin, because
// nothing it can reach exposes a way to.
//
// The direct payoff is testability. Every agent-loop package's unit
// tests construct the scripted in-memory Catalog in drivers/fake,
// declare the handles a scenario needs, and run the real turn logic
// with zero subprocesses, zero gRPC dialing, and zero plugin binaries
// on disk — the unit tier's "in-memory fakes only" budget in
// .claude/rules/go-testing.md, met by construction.
//
// # What a Catalog is and isn't
//
// A Catalog is a view of already-resolved state. Every method is a pure
// lookup: it never launches, configures, dials, retries, or tears down
// anything, and never blocks on I/O. The handles it returns carry a
// live gRPC client the caller invokes directly, plus the static
// metadata (spec, schema, capabilities) the kernel already learned at
// load time — so a consumer needs no second round trip to decide
// whether a model satisfies a turn's requirements or whether a tool
// terminates the turn.
//
// Lifecycle — resolution order, dependency graph, Configure calls,
// failure and restart policy — belongs to whatever future component
// builds a Catalog, never to a Catalog itself.
//
// # Drivers
//
// drivers/fake is the scripted in-memory implementation described
// above. A drivers/plugin implementation wrapping the real plugin
// registry is deliberately not built yet: no such registry exists to
// wrap, and writing a placeholder would fix this interface's shape
// against an imagined lifecycle API rather than a real one. It is a
// later phase's job, and it lands without changing this package — that
// is the point of the split.
package providercatalog
