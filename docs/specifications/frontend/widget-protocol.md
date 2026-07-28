# Widget provider protocol

A widget contributes typed metadata (or other observe-mode work) without owning the frontend. It is a genuine plugin category.

## Transport

`WidgetService` exposes only:

| RPC | Shape |
|---|---|
| `GetCapabilities` | unary |
| `Configure` | unary |
| `Describe` | unary |

There is **no** `Attach` stream. A widget that wants screen presence calls `KernelCallbackService.PublishMetadata` on the callback channel — the same path a tool provider uses for a status block. Observe-mode hooks remain available via `HookSubscriberService` for deriving what to publish.

Capabilities: config schema and supported hook points. There is no region list.

## Screen presence

Publish `MetadataBlock` values (`metadata.v1`) with a stable `id` per logical contribution. Upsert on change; call `RetractMetadata` (or rely on process-exit disconnect) when the contribution should go gray/disappear — the frontend maps `Liveness.DISCONNECTED`.

## Interactive content

Clickable transcript content remains `ActionNode` in a `RenderTree` (transcript surface). Activation is `TriggerAction` on the callback channel, not a widget stream. A widget that also needs an operator action SHOULD also be a tool provider (or emit an `ActionNode` via a tool/model render path), not invent a second action channel.

## Error taxonomy

`WidgetError` / `WidgetErrorCategory` on gRPC status details for Configure (and any future unary). `REGION_UNSUPPORTED` is retired. Remaining: `RENDER_FAILED`, `UNKNOWN`.
