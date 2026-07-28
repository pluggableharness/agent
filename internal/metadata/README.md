# metadata

Kernel-side collection of `MetadataBlock` values for the frontend **Metadata** surface.

- **Publish** upserts a block, stamping server-derived producer identity and `liveness=LIVE`.
- **Retract** / producer disconnect flips `liveness=DISCONNECTED` and keeps the row — the kernel never deletes.
- **List** is the snapshot half of snapshot-then-subscribe; live updates go on the event bus topic `kernel.metadata`.

Wired into `internal/kernelcallback` as the implementation behind `PublishMetadata`, `RetractMetadata`, and `ListMetadata`.
