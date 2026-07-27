// Package widget is the plugin-author-facing SDK for the widget provider
// category described in docs/specifications/frontend/widget-protocol.md —
// a plugin that contributes content into whichever frontend is attached,
// without owning the terminal/window/voice channel itself (a git-status
// panel, a context-budget indicator — content that isn't naturally "a
// tool" or "a context provider," it just wants to put something on
// screen). Despite living under docs/specifications/frontend/ alongside
// the frontend provider protocol, the widget protocol is generated as its
// own pkg/widget/proto/v1 package, distinct from pkg/frontend.
//
// # The Attach name collision — read this before touching Attach
//
// This package's Attach RPC shares its name with pkg/frontend's Attach
// but has a genuinely different shape, and conflating the two is the
// single easiest mistake to make when building on this SDK
// (docs/specifications/frontend/widget-protocol.md#transport):
//
//   - frontend Attach is BIDIRECTIONAL and CONNECTION-scoped: one stream
//     multiplexes every session a frontend has subscribed to, correlated
//     by session_id on each message it carries.
//   - widget Attach — this package's Attach — is SERVER-STREAMING ONLY
//     and SESSION-scoped: one call per session, identified once by
//     AttachRequest.SessionID, never multiplexed across sessions on a
//     single connection. A widget instance serving three attached
//     sessions gets three separate Attach calls, not one call carrying
//     three sessions' worth of updates.
//
// Do not port a connection-multiplexing design onto this Attach — it is
// structurally simpler than the frontend protocol's Attach, not a variant
// of it. Service.Attach (server.go) and UpdateSender (stream.go) are
// built around exactly one call, one session, one Provider.Attach
// invocation.
//
// # Package shape
//
// Provider (widget.go) is the interface a widget plugin author
// implements: GetCapabilities, Configure, and Attach. NewService
// (server.go) adapts a Provider to widgetv1.WidgetServiceServer for
// registration via plugin.Config.Services (pkg/plugin); Describe is
// implemented directly from the plugin.Identity passed to NewService,
// per widget-protocol.md#transport's dev_overrides-identity discussion,
// rather than delegated to the Provider — every category protocol gains
// this identical RPC specifically so a dev_overrides-resolved binary,
// which has no provider {} lock-file entry, can still report its own
// identity.
//
// # Passive in v1 — no action-triggering mechanism here
//
// Widgets are display-only in this protocol revision
// (docs/specifications/frontend/widget-protocol.md#interactive-widgets).
// A WidgetUpdate's RenderTree MAY include an ActionNode the same way any
// other producer's render tree can, but a widget wanting to trigger
// something does so by also implementing SlashCommandService in the same
// plugin process (pkg/slashcommand, a sibling package — not built here).
// This package deliberately has no action-dispatch or action-receiving
// machinery of its own; don't invent one.
//
// # Deriving display state
//
// A widget gets no special session-state API. It derives whatever it
// wants to display by subscribing to hook points in observe mode (rides
// HookSubscriberService, pkg/hook — a sibling package) and pushes the
// result out through Attach's WidgetUpdate stream
// (docs/specifications/frontend/widget-protocol.md#deriving-display-state--no-new-data-feed).
// Attach's stream is purely one-directional: it never receives anything
// back — there is no frontend-protocol-style ClientEvent equivalent here,
// and this package has no receive-side code to match one.
//
// # Errors
//
// Widget Attach has no in-band error channel at all — unlike pkg/frontend's
// Attach, which can report a recoverable error in-band via
// ServerEvent.error, this Attach is server-streaming only with no return
// channel besides the stream itself. Error (errors.go) is therefore
// always carried in the structured detail of a gRPC status returned from
// Configure or Attach, never as an in-band WidgetUpdate field — see
// docs/specifications/frontend/widget-protocol.md#error-taxonomy and
// docs/specifications/frontend/conformance.md#error-taxonomy.
package widget

// ProtocolVersion is the version of the widget category's own protocol this
// SDK implements — the "v1" in pluggableharness.widget.v1.
//
// Deliberately NOT pkg/common.ProtocolVersion, which versions the
// go-plugin runtime contract shared by every category. The two move
// independently: see that constant's documentation for why.
const ProtocolVersion uint32 = 1
