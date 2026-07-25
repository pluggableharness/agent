// Package plandecision defines the seam through which the kernel resolves
// a single `ask`-decision plan item to a terminal `allow`/`deny` verdict.
//
// [docs/specifications/agent-loop/plan-apply-gate.md#decision-semantics]
// states the contract this seam exists to serve: an `ask` decision means
// the kernel MUST emit a `permission-request` state event and block that
// item's apply until a frontend returns a client decision. Everything
// about how that verdict is obtained — which frontend is attached, how the
// operator is prompted, how long the block lasts — is deliberately behind
// the [Resolver] interface, so the plan/apply gate itself contains no
// frontend knowledge at all.
//
// # Who implements Resolver
//
// The spec-correct implementation is a future `drivers/frontend`: it emits
// the `permission-request` `ServerEvent` and blocks on the matching
// `ClientEvent.plan_decision`
// ([docs/specifications/frontend/frontend-protocol.md]). It does not exist
// yet, because no frontend attach path exists anywhere in this codebase
// yet.
//
// Until it does, the only shipping driver is
// `drivers/autoallow` — a deliberate, tracked, operator-approved deviation
// from the MUST above that resolves every item to `allow` without ever
// asking a human. It is built to be impossible to mistake for the real
// thing: it cannot be constructed without an explicit in-code
// acknowledgement, and it stamps a shouty
// [github.com/pluggableharness/agent/internal/plandecision/drivers/autoallow.DecidedBy]
// marker onto every verdict so an audited session shows per item that no
// human ever approved it. Read that package's CLAUDE.md before touching
// it.
//
// # Purity
//
// This package is pure domain logic: interface, request/verdict types, and
// the validation the spec states as MUST for a verdict a Resolver hands
// back. It performs no I/O and MUST NOT import log/slog or
// internal/telemetry (.claude/rules/logging-telemetry.md's pure-domain
// exemption) — the drivers instrument, the seam does not.
package plandecision
