# Frontend & widget provider protocols

Covers **two** plugin categories concerned with what the operator sees and does, neither owning the agent loop itself:

- **Frontend provider** ([`frontend-protocol.md`](frontend-protocol.md)) — owns how the operator sees and types (terminal, window, voice channel, HTTP surface). The process that paints state and turns operator input into kernel-callback RPCs.
- **Widget provider** ([`widget-protocol.md`](widget-protocol.md)) — contributes typed metadata (or other plugin-side work) without owning the frontend. A genuine category, not merely an extension of the other six (see [`architecture.md`](../architecture.md#the-seven-provider-categories)).

## Four surfaces

The operator-facing model is four **kernel-held surfaces**, not a set of plugin-writable screen regions:

| Surface | What it is | Mechanism |
|---|---|---|
| **Input** | A capability, not a place — TUI prompt, web form, CLI, voice | Unary RPCs on `KernelCallbackService` (frontend → kernel) |
| **State** | "Where am I" — fixed schema, kernel-owned | `GetSessionState` snapshot + `kernel.state` bus deltas |
| **Metadata** | Keyed collection of typed blocks, plugin-owned | `PublishMetadata` / `RetractMetadata` / `ListMetadata` + `kernel.metadata` |
| **Transcript** | The conversation stream | `ReadEvents` backfill + `kernel.event.*` live; **the one place `RenderTree` remains** |

`RenderTree` survives only in the transcript because a tool result's presentation really is producer-specific (a diff, a scrollback pane, a collapsible sub-agent node — [`tool/protocol.md#render`](../tool/protocol.md#render)). The other three carry typed data and let the frontend decide presentation entirely.

## Transport

There is **no** frontend or widget `Attach` stream. Under `hashicorp/go-plugin` the plugin is the gRPC server, so the only direction that lets the kernel push streams into a plugin is the **callback channel**, where the plugin is the client ([`kernel-callbacks.md`](../kernel-callbacks.md)). That channel is given to every category unconditionally.

- **Kernel → frontend:** `Subscribe` (topic-filtered bus), `StreamDeltas` (token fast path, out-of-band re: bus), `ReadEvents` (durable backfill).
- **Frontend → kernel:** unary RPCs on the same channel (`SubmitInput`, session lifecycle, plan/interactive resolution, metadata publish/list, …).
- **`FrontendService` / `WidgetService`:** only `GetCapabilities`, `Configure`, `Describe` — the same triple every other category has.

The callback channel is the **only** genuinely bidirectional transport surface in the protocol series (application RPCs on it are unary or server-streaming). See the repository's gRPC rule, `.claude/rules/grpc.md`.

## Session scope — multi-attach

**Multiple frontends MAY subscribe to the same session concurrently** — a TUI and a web tail both watching one live session — each via their own callback connection and their own `Subscribe` / `StreamDeltas` / `ReadEvents` calls.

- State, metadata, and transcript events for a session broadcast identically to every frontend that has attached that session.
- Resolving decisions (`ResolvePlanDecision`, `ResolveInteractive`) is first-response-wins per pending item; a late second response is rejected with a distinct error.
- Attaching a session already in progress backfills history via `ReadEvents` (and metadata via `ListMetadata`); anything missed after snapshot-then-subscribe is recoverable because `Emit` commits to sqlite before republishing onto `kernel.event.{kind}` ([`event-bus.md`](../event-bus.md)).

## Category structure

- [`render-tree.md`](render-tree.md) — the `RenderTree` IR (transcript only). No placement regions.
- [`frontend-protocol.md`](frontend-protocol.md) — frontend provider protocol and how a frontend consumes the four surfaces.
- [`widget-protocol.md`](widget-protocol.md) — widget provider protocol; screen presence is `PublishMetadata`.
- [`examples.md`](examples.md) — worked sequences.
- [`conformance.md`](conformance.md) — MUST/SHOULD/MAY matrix and the acceptance criterion (a second frontend).
