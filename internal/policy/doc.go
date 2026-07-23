// Package policy implements the policy engine described in
// specifications/configuration.md §7 — matching a `policy "<name>" { ... }`
// block's `match` criteria against a real tool call, resolving conflicts
// between rules by specificity (§7.2, including the refinement recorded
// there as "Correction (found during internal/policy implementation)"), and
// evaluating the winning rule's action against a call per §7.3's
// evaluate_policy algorithm.
//
// This package is pure domain logic: no I/O, no logging, no HCL parsing. A
// caller (internal/config, not built here) is responsible for decoding
// `policy` blocks out of agent.hcl into Rule values, calling ValidateRules
// at config-load time, and logging IsEmptyMatch / the ask-downgrade signal
// that Evaluate reports back.
package policy
