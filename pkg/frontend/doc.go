// Package frontend implements the hand-written, plugin-author-facing Go
// SDK for the frontend provider category — the process that owns how the
// operator sees and types (TUI, web, CLI, voice), without owning the
// agent loop (docs/specifications/frontend/).
//
// # The category triple only
//
// FrontendService exposes GetCapabilities, Configure, and Describe — the
// same three RPCs every other category has. There is no Attach stream:
// under go-plugin the plugin is the gRPC server, so the only direction
// that lets the kernel push streams into a frontend is the kernel
// callback channel (docs/specifications/kernel-callbacks.md), where the
// plugin is the client.
//
// # Four surfaces, one callback channel
//
// A frontend consumes four kernel-held surfaces over that channel:
//
//   - Input — SubmitInput, ResolvePlanDecision, ResolveInteractive,
//     Interrupt, InvokeSlashCommand, TriggerAction (unary)
//   - State — GetSessionState snapshot + Subscribe on topic kernel.state
//   - Metadata — ListMetadata snapshot, PublishMetadata/RetractMetadata,
//     and Subscribe on topic kernel.metadata
//   - Transcript — ReadEvents backfill + Subscribe on kernel.event.*;
//     StreamDeltas for the live token fast path (not on the bus)
//
// Session lifecycle (CreateSession/AttachSession/ResumeSession/
// DetachSession/ListSessions) is also on the callback channel.
//
// See docs/specifications/frontend/frontend-protocol.md for the full
// contract and docs/specifications/frontend/render-tree.md for the
// transcript-only RenderTree IR.
package frontend

// ProtocolVersion is the version of the frontend category's own protocol
// this SDK implements — the "v1" in pluggableharness.frontend.v1.
//
// Deliberately NOT pkg/common.ProtocolVersion, which versions the
// go-plugin runtime contract shared by every category. The two move
// independently: see that constant's documentation for why.
const ProtocolVersion uint32 = 1
