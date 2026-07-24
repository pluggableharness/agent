// Package frontend is the hand-written, ergonomic SDK layer over the
// generated pluggableharness.frontend.v1 types in ./proto/v1, for a plugin
// author implementing a frontend provider — the plugin that owns the
// terminal (or window, or voice channel) and mediates between an operator
// and the kernel. The protocol this package implements is specified in
// docs/specifications/frontend/frontend-protocol.md,
// docs/specifications/frontend/render-tree.md (the RenderTree a frontend
// MUST be able to paint, including a node variant added after this
// package's own build — see FallbackText), and
// docs/specifications/frontend/conformance.md.
//
// # Shape
//
// A plugin author implements [Provider] — GetCapabilities, Configure, and
// event handling for the one connection-scoped, bidirectional Attach
// stream — and passes it to [NewService], which adapts it into the
// generated frontendv1.FrontendServiceServer and satisfies
// github.com/pluggableharness/agent/pkg/plugin's Service interface for use
// with plugin.Config.Services.
//
// # Attach is one stream per connection, not one per session
//
// Per frontend-protocol.md's "Transport" section, a frontend opens exactly
// one Attach stream for its connection's whole lifetime; individual
// sessions are subscribed and unsubscribed onto that single stream via the
// six session-control ClientEvent variants (hello, create_session,
// attach_session, resume_session, detach_session, list_sessions),
// correlated to their ServerEvent acknowledgments by a client-generated
// request_id. Every event on the wire, both directions, carries a
// top-level session_id that multiplexes which session it belongs to —
// [ClientEvent] and [ServerEvent] both carry SessionID as a first-class
// field for exactly this reason. This package's Attach adapter
// (attach.go) is a single per-connection dispatch loop, never one
// goroutine per session's own stream, and never fans a session's backfill
// batch out to any connection other than the one that requested it.
//
// # Wire direction: this package receives ClientEvent, sends ServerEvent
//
// The generated frontendv1.FrontendServiceServer.Attach signature —
// Attach(grpc.BidiStreamingServer[ClientEvent, ServerEvent]) error — fixes
// the mechanical wire direction for whichever process implements it: per
// architecture.md's "Transport" section ("category-client construction" is
// a kernel-side launch step for every plugin category, no exception for
// frontend) and github.com/pluggableharness/agent/pkg/plugin's
// Serve/Config (a plugin subprocess only ever runs as a gRPC server —
// its GRPCClient path is unconditionally unsupported), the kernel is the
// FrontendServiceClient that calls Attach, and this package's Service,
// registered as a plugin.Service on the frontend subprocess's own
// grpc.Server, is the FrontendServiceServer. Mechanically that means this
// package's Attach implementation RECEIVES *ClientEvent via stream.Recv()
// and SENDS *ServerEvent via stream.Send() — the reverse of
// frontend-protocol.md's plain-English framing ("the plugin ... sends
// operator input to the kernel as ClientEvents ... and receives ...
// ServerEvents"), which describes the logical origin/destination of each
// event's content rather than which side of this specific bidirectional
// RPC calls Send versus Recv. [Provider.HandleEvent] and [Emitter] are
// built around the actual, compiling mechanical direction: HandleEvent is
// invoked once per ClientEvent this adapter receives, and a Provider
// answers via the Emitter's Emit method, which sends a ServerEvent.
//
// # Fast path vs. full render
//
// [StreamDelta] and [Render] are deliberately distinct Go types (not a
// single "text update" type with a live/replayed flag) per
// frontend-protocol.md's "Fast path vs. full render" section: live
// token-by-token text streaming arrives only as StreamDelta and is never
// replayed as one on backfill, while a finished render — live or replayed
// — always arrives as Render, never as a sequence of deltas. Keeping them
// structurally separate in the [ServerEventPayload] oneof means an
// author's dispatch code cannot accidentally treat a backfilled Render as
// a live StreamDelta or vice versa.
//
// # PlanDecisionScope defaults to ONCE
//
// The generated PlanDecisionScope enum's zero value is
// PLAN_DECISION_SCOPE_UNSPECIFIED, a wire state that means "the sender
// forgot to set this." Per frontend-protocol.md's
// "plan_decision.corrected_input" section, PLAN_DECISION_SCOPE_ONCE is the
// default a frontend SHOULD send absent explicit operator intent — so this
// package's own [PlanScope] domain type reorders the values so its Go
// zero value is PlanScopeOnce, letting a zero-value [PlanDecision] already
// carry the spec-mandated default rather than an invalid UNSPECIFIED.
//
// # Error handling is two distinct paths, not one
//
// A Configure-time error surfaces as a gRPC status carrying a
// [Error] in structured detail, built via
// github.com/pluggableharness/agent/pkg/plugin's StatusError — see
// [Error.StatusErr]. An error encountered mid-Attach (a bad
// render, a malformed ClientEvent, a Provider.HandleEvent failure) instead
// surfaces in-band as ServerEvent.error, keeping the long-lived stream
// open, since tearing down the whole connection over one recoverable
// error would be far more disruptive than the single event it invalidated
// — this is the path an author's HandleEvent naturally reaches by simply
// returning an ordinary error. Only a genuinely fatal condition — the
// plugin process itself failing — legitimately closes the stream with a
// gRPC status, and doing so requires the deliberate, differently-named
// [Fatal] wrapper (see attach.go and errors.go). Conflating these two
// paths — closing the stream over an ordinary recoverable error, or
// silently swallowing a fatal one in-band — is the single most common way
// to get this package's contract wrong.
//
// # ContentBlocks, not a bare string
//
// [UserMessage] carries the same repeated content.v1.ContentBlock
// vocabulary as everywhere else in this protocol series, never a plain
// string — see github.com/pluggableharness/agent/pkg/content's Text,
// Image, Document, and other builders. Field 1 of the generated
// ClientEvent_UserMessage (the protocol's original bare text field) is
// reserved and MUST NOT be reused.
//
// # Author-side UI discipline this package cannot enforce in Go
//
// Two MUST-level rules bind a conforming frontend's own UI code, not
// anything this SDK can check at compile time or runtime, so they are
// documented prominently here instead: an [InteractiveRequest]'s Prompt
// MUST be rendered in the REGION_OVERLAY region, the same visual treatment
// as an ordinary plan-apply-gate "ask" prompt
// (render-tree.md#placement--regions); and a rendered ActionNode MUST be
// made interactive, dispatching that node's tool_name/args/provider
// unchanged as an [ActionTrigger] on activation, never rewritten
// (render-tree.md#interactive-content-the-action-node).
package frontend
