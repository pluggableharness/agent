// Package pending holds the in-process waiter registries that bridge
// operator-facing KernelCallbackService RPCs
// (ResolvePlanDecision, ResolveInteractive) to the turn loop's blocking
// Resolver interfaces (plandecision.Resolver, interactive.Resolver).
//
// A Resolve call parks on a channel keyed by session+id; the matching
// Answer/Complete call unblocks it. First-response-wins: a second Answer
// for an already-resolved id returns ErrAlreadyResolved.
package pending
