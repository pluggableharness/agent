# Frontend provider protocol

A frontend owns how the operator sees and types. It does **not** own the agent loop, policy, or the state backend. Wire traffic with the kernel uses the **kernel callback channel** exclusively for session lifecycle, input, state, metadata, transcript, and token deltas; the category service is only the standard triple.

## Transport

`FrontendService` exposes:

| RPC | Shape | Purpose |
|---|---|---|
| `GetCapabilities` | unary | Slash commands, config schema, supported hook points |
| `Configure` | unary | Apply `agent.hcl` provider config |
| `Describe` | unary | Plugin identity from the running process |

There is no `Attach`. Capabilities MUST be cheaply re-queryable and MUST NOT require a network call.

### Callback-channel RPCs a frontend uses

Documented fully in [`kernel-callbacks.md`](../kernel-callbacks.md); listed here as the frontend-facing contract:

| Concern | RPCs |
|---|---|
| Session lifecycle | `CreateSession`, `AttachSession`, `ResumeSession`, `DetachSession`, `ListSessions` |
| Input | `SubmitInput` (returns `turn_id`), `InvokeSlashCommand`, `TriggerAction`, `Interrupt` |
| Plan / interactive | `ResolvePlanDecision`, `ResolveInteractive` |
| State | `GetSessionState` |
| Metadata | `ListMetadata`, and (for frontend-owned blocks) `PublishMetadata` / `RetractMetadata` |
| Transcript | `ReadEvents` |
| Live updates | `Subscribe` with topics `kernel.event.*`, `kernel.state`, `kernel.metadata` |
| Token fast path | `StreamDeltas` (server-streaming; not on the bus) |

## Four surfaces

### Input

Operator input is a **capability**, not a region. The frontend collects content (text, pasted images as `ContentBlock`s) and calls `SubmitInput`. Correlation uses the returned `turn_id` plus the event stream. Slash commands and `ActionNode` activations are separate unaries (`InvokeSlashCommand`, `TriggerAction`) so they keep the no-model-turn plan/apply path.

### State

`SessionState` (`session.v1`) is a **fixed schema**: `SessionInfo` plus working directory, VCS summary, model, thinking/effort, context pressure, turn count, elapsed, total tokens. No extension point — that is what makes it renderable by a status bar, HTTP header, stdout line, or spoken sentence.

**Per-session:** every snapshot and bus payload names exactly one `session_id`. A frontend attached to several sessions holds one state per session.

Startup sequence: `GetSessionState` then `Subscribe` on topic `kernel.state` (payload carries `session_id`). Snapshot-then-subscribe cannot drop updates that were committed after the snapshot if the frontend also re-reads on mismatch; the kernel republishes on `kernel.state` whenever a watched field changes.

### Metadata

Formerly "sidebar": a keyed collection of `MetadataBlock` (`metadata.v1`) with a closed body oneof (`KeyValue`, `Progress`, `Status`, `ItemList`, `Timer`), `Tone` token scale (`neutral`/`info`/`success`/`warning`/`danger`), and `Liveness` (`LIVE` / `DISCONNECTED`).

- Plugin authors compose blocks via `pkg/metadata` builders; frontends map `Tone` to their own vocabulary (never a wire color).
- `PublishMetadata` upserts; producer is **server-derived**.
- Retraction and publisher exit flip `liveness` to `DISCONNECTED` and republish — the kernel **never deletes** a block.
- Snapshot: `ListMetadata`; live: topic `kernel.metadata` (payload is the block, including `session_id`).

### Transcript

Conversation content travels as durable events (`ReadEvents` / `kernel.event.{kind}`) with optional `Render` payloads decoded to `RenderTree` ([`render-tree.md`](render-tree.md)). There is no placement region: transcript is the conversation stream; chrome is state/metadata.

### Token fast path

`StreamDeltas` delivers live `TokenDelta`s (session_id, target_id, text) on a dedicated server-streaming RPC on the callback channel — **not** the event bus (no topic matching, no shared queue). The kernel forwards each delta promptly and does **not** batch; coalescing is the frontend's decision. Deltas are live-only: replayed text arrives as finished renders, never as deltas. Per-stream FIFO only; outside `determinism.md` replay guarantees by construction.

## Session lifecycle

- **CreateSession** — new session; auto-attaches the caller. Optional profile, working_directory, initial_prompt.
- **AttachSession** — subscribe to an existing (possibly live) session; backfill via `ReadEvents` + `ListMetadata`.
- **ResumeSession** — attach a historical session. `COMPLETED`/`CANCELLED` MAY re-open to `RUNNING` for new turns; bound-exhausted or `FAILED` attaches **replay-only** — subsequent `SubmitInput` is rejected with `FRONTEND_ERROR_CATEGORY_SESSION_REPLAY_ONLY`.
- **DetachSession** — drop this frontend's subscription; other frontends and the session itself are unaffected.
- **ListSessions** — filtered summary list.

No frontend-triggered session deletion. Pruning is a kernel CLI / operator action only.

## Plan and interactive resolution

When policy evaluates a plan item as `ASK`, or an interactive-kind tool blocks for input, the frontend learns via bus/hooks/render (plan-ready / interactive prompt content) and answers with `ResolvePlanDecision` or `ResolveInteractive`.

- **First-response-wins** across multi-attach frontends for a given pending id.
- `ClientDecision` is allow/deny; `PlanDecisionScope` is once/session/always (`plan.v1`). ALWAYS that cannot be persisted MUST be rejected, never silently downgraded.
- Corrected input on a plan decision MUST be re-validated against the tool's input schema.

## Multi-attach arbitration

`ClientEvent`-era connection multiplexing is gone. Multiple frontends each hold their own callback connection. Input and decisions are processed in kernel arrival order per session; the only single-winner rule is first-response-wins on pending plan/interactive ids.

## Error taxonomy

Structured `FrontendError` / `FrontendErrorCategory` on gRPC status details for Configure and residual frontend-local failures. Session and input errors from callback RPCs use the same category vocabulary where applicable (`SESSION_NOT_FOUND`, `SESSION_CREATE_FAILED`, `SESSION_REPLAY_ONLY`, `INVALID_REQUEST`, …). Region-unsupported is retired with placement regions.

## Slash commands

Unchanged aggregation model: direct-invoke from `slashcommand.v1` providers; prompt-expansion from any category's capability response. Registry delivery is via bus/session attach side channel as specified in the slashcommand docs — not via a bidi Attach stream.
