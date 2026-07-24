// Package agentprofile implements the sub-agent profile semantics described
// in specifications/configuration.md §8: the agent_profile{} block's model
// routing, depth-budget inheritance, and tool-scoping resolution.
//
// This package owns three independent pieces of domain logic, each pure and
// I/O-free:
//
//   - Depth-budget arithmetic (configuration.md §8.4): the two distinct
//     remaining_depth formulas — one for the root session, one for a child
//     spawned from a parent — that make max_depth an inherited, only-ever-
//     shrinking budget rather than a static per-profile ceiling.
//   - Capability-aware model fallback (configuration.md §8.2, cross-referencing
//     model.md §9's required-capability matrix): walking a profile's
//     model{} block's primary-then-fallback chain and picking the first
//     candidate whose ModelSpec actually satisfies a turn's requirements.
//   - Tool-scoping resolution (configuration.md §8.3): expanding a profile's
//     flat tools list (concrete "<provider>.<tool_name>" entries and
//     "<provider>.*" wildcards) against a session's actually-loaded provider
//     tool schemas into the concrete allowed set.
//
// This package does not parse agent.hcl, does not talk to model or tool
// providers, and does not know about sessions or the kernel loop — it is
// handed already-decoded AgentProfile values (and, where needed, caller-
// supplied lookup maps) and returns pure computations. Wiring this into HCL
// decoding, provider discovery, and the agent-loop kernel is out of scope
// here.
package agentprofile
