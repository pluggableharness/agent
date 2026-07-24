// Package kernelcallback composes the full twelve-method
// kernelv1.KernelCallbackServiceServer described in
// specifications/kernel-callbacks.md (RunSession, CountTokens, Emit, Log,
// ExportSpans, RecordMetrics, GetTelemetryConfig, GetConfig, Publish,
// Subscribe, ReadEvents, GetSession) — the plugin-to-kernel callback
// channel every plugin subprocess is handed at handshake, regardless of
// category.
//
// Server delegates Log to internal/log.Server, which already implements
// that one RPC, and implements ExportSpans/RecordMetrics/GetTelemetryConfig
// (telemetry.go), GetConfig (config.go), and Publish/Subscribe
// (eventbus.go) directly against internal/telemetry, internal/telemetryrelay,
// and internal/eventbus. RunSession and CountTokens are not yet
// implemented; they return codes.Unimplemented until the packages that
// carry out their semantics (agent-loop.md §7 for RunSession,
// kernel-callbacks.md §2/§3 for CountTokens) exist. Emit, ReadEvents, and
// GetSession are likewise stubbed — not for a missing data path (Emit's
// target, internal/statebackend, and ReadEvents/GetSession's
// Store.Open-based read path both already exist) but because nothing
// anywhere in this codebase yet tracks which session(s) a given plugin
// instance is authorized to touch, and kernel-callbacks.md's own MUST —
// "the kernel MUST reject a call naming any session other than the one
// the calling plugin was actually invoked for" — has no enforcement
// mechanism to call into without it. Implementing any of the three
// without that check would be silently insecure, not merely incomplete.
//
// Every Server instance is dedicated to exactly one launched plugin, with
// that plugin's producer identity — and, as of this revision, every other
// per-plugin dependency (telemetry, the event bus, resolved config) —
// fixed in at construction time via Config. kernel-callbacks.md requires
// producer attribution to be server-derived — a property of which
// plugin's broker connection a call arrived on, established at handshake
// — never a client-supplied request field. Binding every dependency per
// Server instance, rather than reading identity from an untrusted request
// or a shared mutable field, is how this package upholds that requirement
// uniformly across all twelve RPCs, not just the ones that touch identity
// directly.
package kernelcallback
