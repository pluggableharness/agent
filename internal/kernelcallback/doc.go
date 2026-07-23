// Package kernelcallback composes the full four-method
// kernelv1.KernelCallbackServiceServer described in
// specifications/kernel-callbacks.md §1 (RunSession, CountTokens, Emit,
// Log) — the plugin-to-kernel callback channel every plugin subprocess is
// handed at handshake, regardless of category.
//
// Server delegates Log to internal/log.Server, which already implements
// that one RPC. RunSession, CountTokens, and Emit are not yet implemented;
// they return codes.Unimplemented until the packages that carry out their
// semantics (agent-loop.md §7 for RunSession, kernel-callbacks.md §2/§3 for
// CountTokens, kernel-callbacks.md §4 for Emit) exist.
//
// Every Server instance is dedicated to exactly one launched plugin, with
// that plugin's producer identity fixed in at construction time.
// kernel-callbacks.md §4 and §5 both require producer attribution to be
// server-derived — a property of which plugin's broker connection a call
// arrived on, established at handshake — never a client-supplied request
// field. Binding the identity per Server instance, rather than reading it
// from an untrusted request or a shared mutable field, is how this package
// upholds that requirement.
package kernelcallback
