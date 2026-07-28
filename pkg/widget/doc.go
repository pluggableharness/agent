// Package widget implements the hand-written, plugin-author-facing Go SDK
// for the widget provider category — plugins that contribute typed
// metadata (or other side work) without owning the frontend
// (docs/specifications/frontend/widget-protocol.md).
//
// WidgetService exposes GetCapabilities, Configure, and Describe only.
// There is no Attach stream. Screen presence is KernelCallbackService.
// PublishMetadata on the callback channel, the same path any tool
// provider uses for a status block.
package widget

// ProtocolVersion is the version of the widget category's own protocol
// this SDK implements — the "v1" in pluggableharness.widget.v1.
const ProtocolVersion uint32 = 1
