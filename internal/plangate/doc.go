// Package plangate implements the kernel's plan/apply gate —
// [docs/specifications/agent-loop/plan-apply-gate.md] in full: plan
// construction with provider previews, per-item policy evaluation, the
// plan-ready veto chain, ask resolution, the data_source/interactive
// precheck, PlanDecisionScope handling, denial synthesis, and the
// terminal plan/apply audit writes.
//
// One Gate is scoped to one session. That scoping is load-bearing rather
// than incidental: PLAN_DECISION_SCOPE_SESSION verdicts live in memory on
// the Gate itself, so a new Gate per session is what makes those verdicts
// lapse at session end without any explicit expiry logic
// ([plan-apply-gate.md#plandecisionscope-semantics]).
//
// # What this package deliberately does not import
//
// The hook chain and tool execution reach this package through interfaces
// declared HERE, not through imports of internal/hookdispatch or
// internal/tooldispatch:
//
//   - [HookDispatcher] is the entire view this package has of hook
//     dispatch. It calls the plan-ready chain once and trusts that chain's
//     own ordering guarantee (policy pinned ahead of every plugin
//     subscriber) rather than re-implementing pinning here.
//   - [ApplyOutcome] is this package's own apply-result shape. A caller
//     converts whatever its tool scheduler produced into this shape before
//     calling [Gate.Result].
//
// This is the "define the interface where it is consumed" rule from
// .claude/rules/go-style.md, applied for the same reason
// internal/providercatalog applies it against internal/pluginhost: the
// gate needs a plan-ready verdict and a set of per-call outcomes, not a
// dispatcher's or a scheduler's whole surface. A future editor MUST NOT
// "simplify" these into direct imports of those packages once they exist —
// the narrowness is the point, and the adaptation belongs in the caller
// that owns both sides.
//
// # The decided_by vocabulary
//
// Every persisted plan_items row carries a decided_by string
// ([docs/specifications/state-backend.md#plan_items]) in one of five
// forms:
//
//	policy:<rule-name>                 a policy rule decided outright
//	policy:default                     no rule matched; the kind default applied
//	policy:<rule>+resolver:<name>      an ask escalated to the plan-decision resolver
//	policy:<rule>+session:<name>       an ask satisfied by a remembered SESSION-scope verdict
//	hook-veto:<provider>               a third-party plan-ready veto denied the whole plan
//
// The first four use "policy:default" in place of "policy:<rule>" when no
// rule matched. The fifth is plan-wide: a veto denies every item, whatever
// each item's own policy decision was.
//
// # Instrumentation
//
// This package orchestrates I/O (a Preview RPC per resource item, a hook
// chain dispatch, a resolver round trip, two sqlite appends), so it is
// squarely inside .claude/rules/logging-telemetry.md's mandatory scope —
// unlike internal/policy and internal/plandecision, the pure-domain
// packages it composes, which stay uninstrumented.
package plangate
