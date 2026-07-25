// Package turn implements the kernel's RunTurn driver — steps 1 through 15
// of the numbered algorithm in
// docs/specifications/agent-loop/turn-algorithm.md#the-runturn-algorithm.
//
// This package owns no algorithm of its own. Every step delegates to a
// collaborator built and tested in its own package — internal/contextassembly
// (step 1), internal/hookdispatch (steps 2, 5, 7, 11 via the gate, 13, 14),
// internal/modelrequest and internal/modelcall (steps 2-4),
// internal/plangate (steps 9/9b, 10-12, 14) and internal/tooldispatch
// (steps 9/9b, 12). What this package contributes is the documented order,
// the declaration-order bookkeeping that keeps every tool_result paired with
// its tool_use block, and the small adapters that let plangate stay
// decoupled from hookdispatch and tooldispatch.
//
// Steps 16 through 18 — doom-loop detection, bounds checking, and the outer
// loop — are deliberately NOT here. They belong to the session driver that
// calls RunTurn in a loop; Result carries the tool-call hashes, the spend,
// and the done status that driver needs to make those checks itself.
// session-start and session-end are likewise the session driver's.
package turn
