// Package producer carries server-derived plugin-identity attribution
// (pluggableharness.agent.common.v1.ProducerRef) across a context.Context.
//
// Producer identity MUST be set only by trusted, kernel-side code —
// specifications/kernel-callbacks.md §4 and §5 both require it to be
// server-derived, never accepted from an untrusted request field. This
// package is intentionally minimal: two functions and an unexported
// context-key type, with no dependents beyond the act of passing a
// *commonv1.ProducerRef through a context.
package producer
