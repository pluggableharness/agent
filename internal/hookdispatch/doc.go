// Package hookdispatch implements the kernel's ordered, declaration-order
// hook dispatcher — the mechanics layer specified by
// docs/specifications/agent-loop/hook-dispatch.md.
//
// The package owns two things. Registry resolves every hook subscription
// (implicit, category-derived ones and explicit agent.hcl hook{} blocks
// alike) into one ordered chain per hook point, validating at
// construction time what the spec requires to be a config-load error
// rather than a mid-turn surprise. Dispatcher walks one such chain for a
// single hook point, honoring the three subscriber modes' distinct
// failure semantics: an observe subscriber can never alter the payload or
// abort the chain, a transform failure aborts the chain outright rather
// than silently falling back to the pre-transform payload, and a veto
// failure fails closed to HOOK_DECISION_DENY.
//
// Payload shape validation and transform merging are not this package's
// job — internal/hookpayload owns them, and this package composes it.
// What lives here is everything hookpayload deliberately is not: I/O,
// ordering, timeouts, telemetry, and hook_error persistence.
package hookdispatch
