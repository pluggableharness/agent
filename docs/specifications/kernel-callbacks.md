# Kernel callback service

This formalizes the **plugin-to-kernel** direction of communication — the reverse of every other protocol in this series, which covers kernel-to-plugin RPCs (`GetCapabilities`, `Configure`, `StreamCompletion`, `Invoke`, `Attach`, and so on). Twelve primitives live here, grouped by concern:

- **`RunSession`** — runs a full agent session on a plugin's behalf. Used for sub-agent spawns today, and reserved for a future non-interactive pipeline mode. Full turn-by-turn semantics live in [`agent-loop/subagents.md`](agent-loop/subagents.md); this document defines only the wire-level calling mechanism. `RunSessionResult` carries, alongside `final_message` and `status`, three aggregate usage fields — `total_cost_usd`, `total_input_tokens`, `total_output_tokens` — summed across every turn the child session ran, including any of its own descendant sub-agent sessions. These are deliberately flat fields, not a reference to a single completion's `Usage` shape: `RunSessionResult` reports a whole-session rollup (the same `cost_ledger` SUM [`state-backend.md`](state-backend.md) already computes), a different thing from one model call's per-call token counts. The rollup lets a calling plugin do budget-aware fan-out — checking a just-finished child's actual spend before deciding whether to spawn another — without separately re-summing the child's event history itself.
- **`CountTokens`** — resolves an exact-if-possible token count for a string, the shared primitive every other category's `tokens` field routes through.
- **`Emit`** — how a plugin persists anything into the session's state backend.
- **`Log`** — carries a plugin's own log output into the kernel's centralized logging, so it doesn't vanish into an unread subprocess stderr.
- **`ExportSpans`** / **`RecordMetrics`** / **`GetTelemetryConfig`** — the observability relay: a plugin's own tracing and metrics flow through the kernel rather than exporting off-process directly. See [`observability.md`](observability.md) for why and for the tracing/metrics asymmetry.
- **`GetConfig`** — returns the calling plugin's own resolved `agent.hcl` configuration, the same already-decoded shape `Configure` received.
- **`Publish`** / **`Subscribe`** — the event bus: ephemeral, best-effort, cross-plugin pub/sub, distinct from `Emit`'s durable per-session log and from hook dispatch's synchronous, `agent.hcl`-declared subscriber chain. See [`event-bus.md`](event-bus.md).
- **`ReadEvents`** / **`GetSession`** — read-back primitives over the calling plugin's own session: its persisted event log, and its metadata plus live budget rollups.

See [`glossary.md`](glossary.md) for how these terms fit the wider vocabulary, and [`architecture.md`](architecture.md) for the surrounding system (transport, hook dispatch, plan/apply, state backend).

## The callback channel

`hashicorp/go-plugin` natively supports bidirectional plugins — a plugin subprocess can be handed a gRPC client connection back to the host process, not just the reverse. This is exactly the mechanism `RunSession` needs, and nothing new has to be invented at the transport layer: every plugin subprocess, for every category defined in this series, MUST be given this callback connection at handshake time, unconditionally. A plugin that never calls back simply never uses it — the channel's presence isn't category-gated. A context provider needing `CountTokens` is just as valid a caller as a tool provider needing `RunSession`.

```protobuf
KernelCallbackService {
  RunSession(RunSessionRequest) -> RunSessionResult             // agent-loop/subagents.md —
                                                                 // full semantics defined
                                                                 // there, not repeated here
  CountTokens(CountTokensRequest) -> CountTokensResult          // see "CountTokens" below
  Emit(EmitRequest) -> EmitResult                               // see "Emit" below
  Log(LogRequest) -> LogResult                                  // see "Log" below
  ExportSpans(ExportSpansRequest) -> ExportSpansResult          // see "ExportSpans" below
  RecordMetrics(RecordMetricsRequest) -> RecordMetricsResult    // see "RecordMetrics" below
  GetTelemetryConfig(GetTelemetryConfigRequest) -> GetTelemetryConfigResult  // see "GetTelemetryConfig" below
  GetConfig(GetConfigRequest) -> GetConfigResult                // see "GetConfig" below
  Publish(PublishRequest) -> PublishResult                      // see "Publish" below
  Subscribe(SubscribeRequest) -> stream BusEvent                // see "Subscribe" below
  ReadEvents(ReadEventsRequest) -> stream StoredEvent           // see "ReadEvents" below
  GetSession(GetSessionRequest) -> GetSessionResult             // see "GetSession" below
}
```

This channel and the frontend provider's `Attach` RPC are the **only** two genuinely bidirectional RPCs in the whole system — every other category RPC, and every RPC on this service, is server-streaming or unary. `Subscribe` and `ReadEvents` are server-streaming; the other ten are unary. A new primitive added to this service does not get to default to bidi "just in case"; the shape here is a consequence of `hashicorp/go-plugin`'s native plugin→kernel channel existing at all, not a free design choice repeated per RPC — and even that channel's own two RPCs (`RunSession`, `CountTokens`) are, at the application level, simple request/response calls riding a connection that happens to be bidirectional at the transport layer, not calls that themselves stream both ways.

The callback channel uses a **fixed, well-known broker ID**, not a wire-negotiated one — safe because the kernel is the only party that ever accepts this broker connection, so no collision is possible. Producer identity (`{category, name, version}`) is a property of *which broker connection a call arrived on*, established at handshake, and is server-derived, never client-supplied (see "Emit" and "Log" below) — a plugin cannot declare a producer identity other than its own.

**Every RPC on this service falls into one of two shapes with respect to session scope**, and this determines whether a `session_id` field appears on its request at all:

- **Plugin-scoped** — about the calling plugin itself, true regardless of which (if any) session is currently invoking it: `CountTokens`, `GetConfig`, `GetTelemetryConfig`, `Publish`, `Subscribe`. These carry no `session_id` field.
- **Session-scoped** — about one specific session the plugin is participating in: `RunSession` (the *parent*, via `parent_session_id`), `Emit`, `Log` (optionally), `ExportSpans`/`RecordMetrics` (optionally, same reasoning as `Log`), `ReadEvents`, `GetSession`. These carry an explicit `session_id` field, because a single long-lived plugin subprocess can be invoked by more than one session over its lifetime (concurrent parallel data-source calls in one turn, or nested `RunSession` spawns) — the kernel has no other way to know which session a given call pertains to. Every mandatory-`session_id` RPC follows `Emit`'s rule: the kernel MUST reject a call naming any session other than the one the calling plugin was actually invoked for.

## `CountTokens`

```protobuf
CountTokensRequest {
  content     []ContentBlock   // MUST — text-only in v1, matching context/
                                // and memory/'s existing content-type
                                // constraint
  model_ref   { provider: string, id: string }?  // MAY be omitted
}

CountTokensResult {
  count   int
  exact   bool   // MUST — true if a real vendor tokenizer produced this
                 // count, false if the kernel's fallback heuristic (see
                 // below) did
}
```

### Resolution algorithm

```go
CountTokens(req):
  if req.model_ref is set and that model provider implements the optional
     CountTokens RPC (model/protocol.md#counttokens):
    return (that provider's count, exact: true)
  else:
    return (fallback_heuristic(req.content), exact: false)
```

A model provider's own `CountTokens` (when implemented) uses its real vendor tokenizer — some vendors expose a dedicated counting endpoint, others require a bundled tokenizer library; this document doesn't mandate which, only the RPC shape. [`model/protocol.md#counttokens`](model/protocol.md#counttokens) declares this a SHOULD for model providers: the fallback formula below is deliberately kept simple and single-purpose rather than made smarter, on the reasoning that accuracy should come from providers actually implementing real tokenizers, not from the kernel guessing better. Still not a MUST there, since not every vendor makes exact counting cheap or even possible without a network round-trip — but `exact: false` results should be the exception in practice, not the norm.

### Why a kernel primitive, not a provider-local heuristic

The alternative — every context/memory provider picks its own estimation formula — is what created the gap this primitive closes: two providers' `tokens: 4000` could represent genuinely different amounts of actual context-window consumption, silently corrupting the budget system's allocation arithmetic. Routing every estimate through one kernel-owned implementation (exact when possible, a single documented fallback otherwise) guarantees `tokens` fields stay mutually comparable and additive regardless of which provider produced them — the property the budget system actually needs, one that "provider's own estimate" alone never guaranteed. Context providers and memory providers MUST compute their `tokens` fields via this primitive, not an arbitrary provider-local heuristic — see [`context/README.md`](context/README.md) and [`memory/README.md`](memory/README.md).

This resolution algorithm is the single canonical implementation: the kernel MUST NOT grow a second, competing fallback formula elsewhere.

## The fallback heuristic

When no model-specific tokenizer is available (`exact: false`), the kernel MUST use exactly one documented, deterministic formula — not "any reasonable heuristic," since the whole point is byte-identical results regardless of caller:

```text
fallback_heuristic(content) = ceil(total_utf8_byte_length(text_of(content)) / 4)
```

Chosen for precedent, not novelty: this exact ~4-bytes-per-token approximation is already the dominant cheap heuristic in production use across other coding harnesses (e.g., Continue's `estimateTokenCount = length/4`). Non-text content blocks (when a future revision allows them) MUST NOT contribute to this calculation in v1, consistent with context and memory providers both being text-only for now.

This formula is deliberately **not content-type-aware**. A code-vs-prose variant formula — weighting differently for content that's mostly code versus mostly prose — is deliberately not used: the fix for accuracy is upstream (more providers implementing real `CountTokens`, the SHOULD above), not a smarter fallback formula. There is exactly one fallback formula in the system — a second, content-type-specific variant MUST NOT be introduced, even as an optimization.

## Emit

`Emit` is how a plugin persists anything into the session's state backend. The kernel is the state backend's sole writer ([`state-backend.md`](state-backend.md)) — a plugin never opens or writes the sqlite file directly, it calls `Emit` and the kernel performs the actual write, assigning the ordering-authoritative `sequence` and the stable `id` itself.

```protobuf
EmitRequest {
  session_id      string      // MUST — the calling session's id; the kernel
                               // MUST reject an Emit naming any other
                               // session (a plugin cannot write into a
                               // session it wasn't invoked for)
  kind            EventKind   // MUST — mirrors state-backend.md's event-kind
                               // enum exactly
  schema_version  string      // MUST — versions the shape of `payload`, so a
                               // future kernel can still interpret an old
                               // event correctly (the "supersedes" mechanism,
                               // see architecture.md#versioning--schema-drift--supersedes)
  payload         bytes       // MUST — opaque to the kernel by design; the
                               // kernel never inspects this. Structure is
                               // defined by whichever spec owns this `kind`
}

EmitResult {
  id        string   // the assigned, storage-independent event id
  sequence  int64    // the assigned ordering-authoritative sequence number
}
```

`producer_category`/`producer_name`/`producer_version` are deliberately **not** `EmitRequest` fields: the kernel already knows which plugin is calling — it's a property of the already-authenticated callback connection established at handshake (see "The callback channel" above) — and fills them in server-side. A plugin cannot declare a producer identity other than its own; there's no field to spoof. The same rule applies to `Log` and every other RPC on this service.

`EventKind` is `state-backend.md`'s authoritative enum, restated here only because it's the wire-level type `Emit` actually carries — this document does not own its definition, and `state-backend.md` remains authoritative. Like every enum in this system, `EventKind`'s zero value, `EVENT_KIND_UNSPECIFIED`, is never valid on the wire — a caller that forgets to set `kind` produces a detectable, named "unspecified" error rather than something that silently looks like a real event kind. Usage/cost, `Render` output, and `session_start`/`session_end` deliberately don't get their own `EventKind` at all — see [`state-backend.md#the-kind-enum`](state-backend.md#the-kind-enum) for why.

`payload` is always the `pluggableharness.event.v1` message that matches `kind`, marshaled to bytes — [`state-backend.md#the-kind-enum`](state-backend.md#the-kind-enum) carries the authoritative kind → message table. `schema_version` names the `event` package version that message belongs to: `"1"` for `event.v1`, and a future breaking payload change ships as `event.v2` with `schema_version = "2"`, never a silent edit to the `event.v1` shape. This does not change the opacity of `payload` itself — the kernel marshals/unmarshals nothing at `Emit` time for a plugin-supplied payload and never inspects the bytes; `schema_version` only tells a future reader (replay, a newer kernel) which package's generated type to decode with.

`Emit` accepts `EVENT_KIND_HOOK_ERROR` like any other kind, with one difference: the kernel is the one calling it, on a failing hook subscriber's behalf, rather than a plugin calling `Emit` for itself — see [`state-backend.md#the-kind-enum`](state-backend.md#the-kind-enum) for why this kind is kernel-synthesized and [`agent-loop/hook-dispatch.md#subscriber-error-handling`](agent-loop/hook-dispatch.md#subscriber-error-handling) for when it fires.

A successful `Emit` MUST also republish the same event onto the event bus (see [`event-bus.md#the-kernel-namespace`](event-bus.md#the-kernel-namespace)) on the reserved topic `kernel.event.{kind}`, where `{kind}` is `EventKind`'s lowercase text form (`state-backend.md#the-kind-enum`'s own vocabulary, e.g. `kernel.event.tool_call`). This is the first, and so far only, kernel-side bus publisher — it lets any plugin observe the durable event stream live, without polling `ReadEvents`, while the durability guarantee stays exactly where it already lives: a bus subscriber that never connects, or disconnects mid-stream, loses nothing durable, because the sqlite row was already committed before the republish.

## Log

`Log` carries a plugin's own log output into the kernel's centralized logging, so it doesn't vanish into an unread subprocess stderr — plugin logs and the kernel's own logs end up in one place instead of two. Unlike `Emit`, a `Log` call is not tied to an active session: a plugin MAY call `Log` before any session exists (process startup, or from within `Configure`) or after one has ended (during shutdown), so `session_id` is optional here where it is mandatory for `Emit`.

`Log` carries a **batch** of entries, not a single one — a plugin logging at `TRACE` would otherwise pay one unary round-trip per line, which is untenable. Batching is a transport concern only; it does not change `Log`'s session-optionality or any other per-entry semantics below.

```protobuf
LogRequest {
  session_id  string?      // MAY be omitted — set when the log line is
                            // attributable to a specific session (the common
                            // case), omitted for startup/shutdown/Configure-time
                            // logging that predates or outlives any session
  entries     []LogEntry   // MUST — non-empty; one or more entries, in the
                            // order the plugin produced them
}

LogResult {}   // empty; a Log call either succeeds or the RPC itself errors
```

A malformed entry within an otherwise-valid batch (missing `level`, missing `message`, missing `time`) is skipped and warned about individually — the kernel MUST NOT fail the whole batch for one bad entry, since that would silently discard every well-formed entry alongside it. `LogRequest` carrying zero entries, or an `entries` list where every single entry is malformed, is itself a malformed request and fails the RPC.

```protobuf
LogEntry {
  level    LogLevel                    // MUST
  logger   string                       // MAY be empty; a dotted sub-component
                                         // name within the plugin (e.g.
                                         // "anthropic.retry"), mirroring Go's
                                         // log/slog logger-naming convention
  message  string                       // MUST — human-readable
  fields   Struct                       // MAY be empty; structured attributes,
                                         // mirroring slog.Attr's key/value model
  time     Timestamp                    // MUST — when the event occurred at
                                         // the plugin, not when the kernel
                                         // received it (meaningful if a plugin
                                         // buffers log output before flushing)
}
```

The six-level vocabulary (`TRACE`, `DEBUG`, `INFO`, `WARN`, `ERROR`, `FATAL`) is the canonical logging vocabulary for the whole project, not just this RPC: kernel-native code, not just forwarded plugin logs, uses these same six levels. `LogLevel`'s zero value, `LOG_LEVEL_UNSPECIFIED`, is never valid on the wire, the same convention every enum in this system follows — an entry that omits `level` is malformed (see above), not silently defaulted.

`LOG_LEVEL_TRACE` and `LOG_LEVEL_FATAL` do not map directly onto `log/slog`'s four built-in levels (Debug/Info/Warn/Error — the kernel's own logging is slog-only). The kernel MUST translate `LOG_LEVEL_TRACE` to a custom `slog.Level` below `slog.LevelDebug` and `LOG_LEVEL_FATAL` to one above `slog.LevelError`; `slog.Level` is an ordinary `int8` and custom levels are a native, documented pattern, not a workaround. The exact numeric offsets are a kernel implementation detail, not part of this wire protocol.

**`LOG_LEVEL_FATAL` is a severity label only.** Logging at `FATAL` does not itself terminate the plugin or the kernel, and the kernel **MUST NOT** treat a `Log` call carrying it as a request to do anything beyond routing the entry through at that severity. This is worth stating unambiguously because "FATAL" naturally reads as "the process should die" to anyone unfamiliar with this design — it isn't, here. A plugin that logs FATAL and then crashes is still detected and categorized through the ordinary process-crash path (`process_crashed`, per [`tool/conformance.md`](tool/conformance.md) and the parallel handling in other categories), never inferred from having received a FATAL log line.

A plugin SHOULD consult `GetTelemetryConfig`'s `log_level` before constructing an entry whose `fields` are expensive to compute, and MAY skip the call entirely for a level below the reported floor — the entry would be logged through and then discarded kernel-side either way, so this is purely an optimization, never a correctness requirement.

## `ExportSpans`

`ExportSpans` relays a batch of a plugin's own completed trace spans to the kernel, which is the single place that holds collector configuration and exports to it. See [`observability.md#the-relay-model`](observability.md#the-relay-model) for the full rationale (this reverses an earlier direct-export design) and for why this RPC has no metrics counterpart.

```protobuf
ExportSpansRequest {
  session_id  string?         // MAY be omitted — same session-optional rule
                               // as Log; a plugin may produce spans outside
                               // any session context (Configure, startup)
  spans       []trace.v1.Span // MUST — non-empty
}

ExportSpansResult {}   // empty; an ExportSpans call either succeeds or the
                       // RPC itself errors
```

The kernel MUST NOT re-parent, re-time, or otherwise alter a relayed span's `trace_id`/`span_id`/`parent_span_id`/timestamps before forwarding it to a collector — it is a transparent relay, not a re-emission through its own tracer. `producer` attribution is added to the exported resource, server-derived exactly as with `Emit`/`Log`, never read from the span itself.

## `RecordMetrics`

`RecordMetrics` relays a batch of metric observations. Unlike `ExportSpans`, this is **not** a transparent relay: see [`observability.md#the-tracingmetrics-asymmetry`](observability.md#the-tracingmetrics-asymmetry) for why plugin-supplied metric attributes MUST be bounded by the kernel before they reach any exporter.

```protobuf
RecordMetricsRequest {
  session_id  string?              // MAY be omitted — same rule as Log
  metrics     []metric.v1.MetricRecord  // MUST — non-empty
}

RecordMetricsResult {}   // empty; same shape as ExportSpansResult
```

## `GetTelemetryConfig`

`GetTelemetryConfig` answers "is tracing/metrics/logging on, and at what level" without a plugin needing to guess from its own environment. A plugin SHOULD call this once at startup and cache the result for its process lifetime rather than calling it per operation — see [`observability.md#gettelemetryconfig-caching`](observability.md#gettelemetryconfig-caching).

```protobuf
GetTelemetryConfigRequest {}   // empty; identity comes from the callback
                                // connection, not a request field

GetTelemetryConfigResult {
  traces_enabled   bool     // MUST
  metrics_enabled  bool     // MUST
  logs_enabled     bool     // MUST
  log_level        LogLevel // MUST — the floor below which a Log entry is
                             // accepted but immediately discarded; see "Log"
  sampling_ratio   double   // MUST — the ParentBased(TraceIDRatioBased)
                             // ratio configured for traces, meaningful only
                             // when traces_enabled
}
```

## `GetConfig`

`GetConfig` returns the calling plugin's own already-decoded `agent.hcl` configuration — the same shape `Configure` received, secrets already resolved through the schema-to-cty bridge (`configuration/blocks-reference.md`). This closes a real gap: before this RPC existed, a plugin that needed its config outside the one `Configure` call it happened to receive it in had no channel to ask for it again.

```protobuf
GetConfigRequest {}   // empty; identity comes from the callback connection

GetConfigResult {
  config  Struct   // MUST — identical shape to ConfigureRequest.config
}
```

**A plugin MUST NOT echo any value from `config` into `Emit`, `Publish`, `Render`, a log line, or an error message** if that value came from a `sensitive` config attribute — the same rule [`model/protocol.md`](model/protocol.md) and [`tool/protocol.md`](tool/protocol.md) already impose on a received secret, restated here because `GetConfig` is a second channel a secret now crosses. Handling this RPC is on the kernel's deliberately-unlogged path ([`configuration/blocks-reference.md`](configuration/blocks-reference.md)'s secret-resolution rule) — the kernel itself MUST NOT log `config`'s contents at any level, including `TRACE`, when serving this call.

## Publish

`Publish` emits one event onto the ephemeral, in-process event bus for other plugins (and the kernel) to observe. See [`event-bus.md`](event-bus.md) for the full topic grammar, delivery semantics, and the boundary against `Emit`/hook dispatch/frontend broadcast — this section gives only the wire shape.

```protobuf
PublishRequest {
  event_type      string  // MUST — a single dot-free, wildcard-free segment
                           // naming this occurrence within the plugin's own
                           // namespace, e.g. "file_changed"
  payload         bytes   // MAY be empty; opaque to the kernel — see
                           // event-bus.md's carve-out
  payload_type    string  // MUST — identifies payload's shape for a
                           // subscriber: a fully-qualified proto message
                           // name (preferred) or a media type
  schema_version  string  // MUST — versions payload_type the same way
                           // EmitRequest.schema_version versions Emit's
                           // payload
}

PublishResult {
  topic  string  // the fully-resolved topic this event was published on:
                 // "plugin.{category}.{name}.{event_type}"
}
```

**A plugin never supplies its own topic.** The kernel constructs `plugin.{category}.{name}.{event_type}` from the authenticated callback connection's producer identity, exactly as it derives `producer_category`/`producer_name`/`producer_version` for `Emit` and `Log` — a plugin cannot publish under another plugin's identity, and cannot publish onto the reserved `kernel.*` namespace (see [`event-bus.md#the-kernel-namespace`](event-bus.md#the-kernel-namespace)), because it never has the ability to name a topic outside its own constructed one.

## Subscribe

`Subscribe` is server-streaming: the plugin sends one request and receives a live stream of `BusEvent`s matching its filters until it closes the stream or the kernel closes it (see [`event-bus.md#backpressure`](event-bus.md#backpressure) for the one condition under which the kernel closes it unilaterally).

```protobuf
SubscribeRequest {
  topic_filters  []string  // MUST — non-empty; each entry is either an
                            // exact topic or a topic prefix ending in "*"
                            // (event-bus.md's filter grammar)
}

// streamed:
BusEvent {
  topic           string     // the event's fully-resolved topic
  payload         bytes      // opaque; see Publish
  payload_type    string     // see Publish
  schema_version  string     // see Publish
  time            Timestamp  // when the kernel received the Publish this
                              // event fans out from
}
```

## `ReadEvents`

`ReadEvents` is server-streaming: it reads back the calling plugin's own session's persisted event log, ordered by `sequence` — never by `time`, per `.claude/rules/determinism.md`'s ordering rule, restated here because this is the one RPC on this service that reads the ordering-authoritative column back out.

```protobuf
ReadEventsRequest {
  session_id     string        // MUST — same one-session-only rule as Emit
  kinds          []EventKind   // MAY be empty, meaning "every kind"
  from_sequence  int64?        // MAY be omitted, meaning "from the start
                                // of the session's log"
  limit          int32?        // MAY be omitted, meaning "no limit"
}

// streamed, ordered by sequence ascending:
StoredEvent {
  sequence        int64
  id              string
  time            Timestamp    // display-only, mirrors state-backend.md's
                                 // events.timestamp column — never used to
                                 // reorder anything
  kind            EventKind
  producer        ProducerRef  // who originally Emit'd this event — read
                                 // back, not server-derived for this call
  schema_version  string
  payload         bytes        // opaque, exactly as Emit wrote it
}
```

## `GetSession`

`GetSession` returns the calling plugin's own session's metadata plus its live, in-memory budget rollups — the same [`state-backend.md`](state-backend.md)-backed `SessionInfo` the frontend protocol already uses, extended with two fields that state backend deliberately does not persist ([`state-backend.md#live-vs-post-hoc-tree-walking`](state-backend.md#live-vs-post-hoc-tree-walking)): a session's remaining depth and cost budget are in-memory kernel state, recomputed at spawn time and spent down at each `RunSession` hop, never written to sqlite.

```protobuf
GetSessionRequest {
  session_id  string  // MUST — same one-session-only rule as Emit
}

GetSessionResult {
  info                       session.v1.SessionInfo  // MUST
  remaining_depth            int32                    // MUST
  remaining_cost_budget_usd  double                   // MUST
}
```

`info.cost_usd` is the persisted `cost_ledger` SUM (a rollup computed and stored at usage-event time, per `.claude/rules/determinism.md`'s cost-rollup rule) — `GetSession` reads it, it never re-walks and re-sums the event log itself. `remaining_cost_budget_usd` and `remaining_depth`, by contrast, are the live in-memory figures — the two mechanisms answer genuinely different questions, and this RPC deliberately surfaces both rather than picking one.

## Required vs. optional support

| Capability | Level | Notes |
|---|---|---|
| Bidirectional callback channel, unconditional per plugin | MUST | "The callback channel" |
| `RunSession` callable via this channel | MUST | semantics in [`agent-loop/subagents.md`](agent-loop/subagents.md) |
| `CountTokens` callable via this channel | MUST | "CountTokens" |
| Model-provider's own `CountTokens` RPC | SHOULD, per model provider | [`model/protocol.md#counttokens`](model/protocol.md#counttokens) |
| Exact-vs-fallback resolution algorithm | MUST | "CountTokens" |
| Single documented fallback formula, no per-caller variation | MUST | "The fallback heuristic" |
| Context/memory providers computing `tokens` via this primitive, not their own heuristic | MUST | "Why a kernel primitive, not a provider-local heuristic" |
| `Emit` callable via this channel | MUST | "Emit" |
| Kernel derives producer identity server-side, never from a client-supplied field | MUST | "Emit", "Log", and every other RPC on this service |
| `Emit` republishes onto `kernel.event.{kind}` on success | MUST | "Emit" |
| `Log` callable via this channel | MUST | "Log" |
| `session_id` optional on `Log`/`ExportSpans`/`RecordMetrics` (unlike `Emit`/`ReadEvents`/`GetSession`, where it's mandatory) | MUST | "Log", "ExportSpans", "RecordMetrics" |
| `Log` accepts a batch and skips-and-warns a malformed entry rather than failing the whole batch | MUST | "Log" |
| Kernel translates `LOG_LEVEL_TRACE`/`LOG_LEVEL_FATAL` onto custom `slog.Level` values outside slog's 4 built-in levels | MUST | "Log" |
| `LOG_LEVEL_FATAL` triggers plugin/kernel termination | MUST NOT | "Log" |
| `ExportSpans` relays without altering span identity/timing | MUST | "ExportSpans" |
| `RecordMetrics` attribute keys bounded kernel-side | MUST | [`observability.md#the-tracingmetrics-asymmetry`](observability.md#the-tracingmetrics-asymmetry) |
| `GetTelemetryConfig` callable via this channel | MUST | "GetTelemetryConfig" |
| `GetConfig` callable via this channel | MUST | "GetConfig" |
| Secrets in `GetConfig`'s result echoed into `Emit`/`Publish`/`Render`/logs/errors | MUST NOT | "GetConfig" |
| `Publish` callable via this channel | MUST | "Publish" |
| Plugin-supplied topic on `Publish` | MUST NOT (kernel constructs it) | "Publish" |
| `Subscribe` callable via this channel | MUST | "Subscribe" |
| `ReadEvents` callable via this channel, ordered by `sequence` | MUST | "ReadEvents" |
| `GetSession` callable via this channel | MUST | "GetSession" |

## Open questions

- Whether the kernel should cache `CountTokens` results — the same static content (e.g. a `stability: static` context section, per [`context/README.md`](context/README.md)) being re-counted every turn is wasted work. Not addressed here; a plausible follow-up optimization once a reference implementation exists.
- Whether other future kernel primitives belong on this same `KernelCallbackService`, or whether some warrant their own dedicated channel. **Resolved in this revision: one service.** The handshake already guarantees every plugin subprocess exactly one callback connection (see "The callback channel"); a second channel would need its own broker ID and its own handshake step for no benefit over adding an RPC here. Should a genuinely different transport shape ever be needed (a primitive that must be bidirectional, for instance), that would be the trigger to revisit this, not RPC count alone.
- `event-bus.md#authorization` tracks whether `Subscribe` needs an `agent.hcl`-declared allowlist, since v1 lets any plugin subscribe to any non-`kernel.*` topic including another plugin's.
