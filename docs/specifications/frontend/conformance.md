# Frontend & widget conformance

## Summary matrix

| Requirement | Level | Reference |
|---|---|---|
| FrontendService is GetCapabilities / Configure / Describe only | MUST | [frontend-protocol.md](frontend-protocol.md#transport) |
| No Attach stream on frontend or widget | MUST | [README.md](README.md#transport) |
| Four surfaces: input, state, metadata, transcript | MUST | [README.md](README.md#four-surfaces) |
| SessionState fixed schema, per-session | MUST | [frontend-protocol.md](frontend-protocol.md#state) |
| SubmitInput returns turn_id | MUST | [frontend-protocol.md](frontend-protocol.md#input) |
| MetadataBlock closed body oneof; Tone token scale; never delete | MUST | [frontend-protocol.md](frontend-protocol.md#metadata) |
| StreamDeltas out-of-band re: bus; kernel does not batch | MUST | [frontend-protocol.md](frontend-protocol.md#token-fast-path) |
| RenderTree transcript only; no Region/PlacedContent | MUST | [render-tree.md](render-tree.md) |
| Every RenderNode variant graceful fallback | MUST | [render-tree.md](render-tree.md) |
| Multi-attach; first-response-wins on plan/interactive | MUST | [frontend-protocol.md](frontend-protocol.md#multi-attach-arbitration) |
| Widget screen presence via PublishMetadata | MUST | [widget-protocol.md](widget-protocol.md) |
| Structured FrontendError / WidgetError taxonomies | MUST | this file |

## Error taxonomy

Frontend and widget errors use structured category enums on gRPC status details (`.claude/rules/grpc.md`). Region-unsupported categories are retired (reserved field numbers on the wire).

## Acceptance criterion

The design is only proven when a **second frontend** — even a deliberately minimal HTTP one — renders the same `SessionState` and the same metadata blocks with the same functionality. Unit tests cannot substitute for that. Shipping a second frontend is not required for wire/spec completion of this revision, but the contracts MUST make it possible without a TUI region vocabulary.
