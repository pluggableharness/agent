// Package contextassembly implements step 1 of RunTurn
// (docs/specifications/agent-loop/turn-algorithm.md): the
// ContextService.Contribute chain that assembles a turn's prompt context
// before each model call
// (docs/specifications/context/protocol.md#contribute-the-context-assemble-rpc,
// docs/specifications/context/data-types.md).
//
// context-assemble is explicitly NOT a hook.v1 HookSubscriberService
// dispatch (docs/specifications/agent-loop/hook-dispatch.md#hook-points):
// it stays on the context category's own ContextService.Contribute RPC,
// which already carries the full accumulated ContextSection chain as a
// first-class typed request/response — routing it through the generic
// HookPayload oneof would just be a second, redundant path to the same
// effect with weaker typing. This package therefore never imports
// internal/hookdispatch, and never will: the two chains are structurally
// separate, even though both implement the same "ordered transform chain
// in agent.hcl declaration order" shape described in
// docs/specifications/architecture.md's hook-dispatch semantics.
//
// Assembler.Assemble runs every loaded context provider's Contribute RPC,
// in agent.hcl declaration order, building the accumulated ContextSection
// chain: validating each provider's own section(s) against its token
// budget (dropping an over-budget section, never failing the turn),
// enforcing the own-section-only scope rule for non-compactor providers
// (discarding a violating provider's entire response and restoring the
// prior chain), and threading a compactor's rewritten_history through to
// the caller. See this package's CLAUDE.md for the exact mechanics this
// doc comment summarizes and the deviations from the sketched API shape.
package contextassembly
