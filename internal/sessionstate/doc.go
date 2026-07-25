// Package sessionstate is the kernel's live-session table and the
// sole-writer object for one session's persisted event log
// (docs/specifications/state-backend.md#ordering--concurrency: "the
// kernel is the sole writer to any given session's file"). A *Live wraps
// exactly one already-created/opened *statebackend.Session, serializing
// every Emit/Append* call through one mutex so appends and
// their same-transaction accompanying rows (cost_ledger, plan_items) are
// never interleaved, and republishes each successfully-persisted event
// onto the event bus's reserved kernel.event.{kind} topic
// (docs/specifications/kernel-callbacks.md#emit,
// docs/specifications/event-bus.md#the-kernel-namespace) — write-then-republish,
// never the reverse, so a bus subscriber never observes an event the sqlite
// file doesn't already durably hold.
//
// Table is the process-wide registry of currently-live sessions, keyed by
// session id — the lookup a future turn/session driver uses to find the
// *Live for a session it already knows the id of.
//
// This package is the primitive a later phase's internal/kernelcallback
// Emit/ReadEvents/GetSession implementation sits on top of: it is not
// itself an RPC handler, does no session-scope authorization (that's
// internal/sessionscope's job, called by the future RPC handler before it
// ever reaches this package), and MUST NOT be imported by
// internal/kernelcallback — that connection is wired in a later phase, not
// this one.
package sessionstate
