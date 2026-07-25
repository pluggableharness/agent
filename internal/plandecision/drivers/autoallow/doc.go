// Package autoallow implements a plandecision.Resolver that approves
// every `ask`-decision plan item without ever asking a human.
//
// # This is a deliberate, tracked deviation from a spec MUST
//
// [docs/specifications/agent-loop/plan-apply-gate.md#decision-semantics]
// requires that an `ask` decision emit a `permission-request` state event
// and block that item's apply until a frontend returns a client decision.
// No frontend attach path exists anywhere in this codebase yet, so the
// current build stage cannot satisfy that MUST. This package is the
// operator-approved stand-in for that stage, approved on the explicit
// condition that it be impossible to mistake for the real, spec-correct
// behavior. Every apparently redundant guard below exists to enforce that
// condition:
//
//   - [Config.AcknowledgeUnsafeAutoAllow] MUST be true or [New] returns
//     [ErrNotAcknowledged]. There is deliberately no usable zero value —
//     nobody constructs this by accident, and the acknowledgement is
//     visible in code at the call site, not buried in a config file.
//   - [DecidedBy] is stamped verbatim onto every verdict, so the
//     `plan_items.decided_by` audit rows of a session run this way say, per
//     item, that no human ever approved it.
//   - Every resolution logs one WARN, so a live session is noisy about it
//     rather than silently permissive.
//   - Every verdict is scoped ONCE, never SESSION or ALWAYS, so this
//     resolver leaves zero durable state behind for the real frontend
//     resolver to later discover and reconcile.
//
// The real implementation is a future `drivers/frontend` in this same
// directory: it emits the `permission-request` `ServerEvent` and blocks on
// the matching `ClientEvent.plan_decision`
// ([docs/specifications/frontend/frontend-protocol.md]). When it lands,
// this driver stops being the default anything.
//
// Do not soften, simplify, or "clean up" the guards above. Read this
// package's CLAUDE.md — which restates each behavioral requirement with
// its rationale — before changing anything here.
package autoallow
