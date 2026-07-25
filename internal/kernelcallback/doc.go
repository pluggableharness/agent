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
// (telemetry.go), GetConfig (config.go), Publish/Subscribe (eventbus.go),
// CountTokens (tokens.go), Emit (emit.go), ReadEvents (events.go), and
// GetSession (sessions.go) directly against internal/telemetry,
// internal/telemetryrelay, internal/eventbus, internal/tokencount,
// internal/sessionscope, and internal/sessionstate. RunSession is the one
// remaining stub, returning codes.Unimplemented until agent-loop.md §7's
// session-tree semantics exist — this build is root-sessions-only.
//
// Emit, ReadEvents, and GetSession are session-scoped: each authorizes its
// request's session_id via sessions.go's shared authorizedSession helper
// before touching a session's data, per kernel-callbacks.md's own MUST —
// "the kernel MUST reject a call naming any session other than the one
// the calling plugin was actually invoked for." authorizedSession returns
// the identical codes.PermissionDenied error whether the calling plugin
// was never granted the named session or was granted it but the session
// is no longer live — a deliberate security property (never
// codes.NotFound, never a distinguishable message), not an oversight; see
// CLAUDE.md.
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
