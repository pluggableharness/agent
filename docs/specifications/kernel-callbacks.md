# Kernel callback service

This formalizes the **plugin-to-kernel** direction of communication — the reverse of every other protocol in this series, which covers kernel-to-plugin RPCs (`GetCapabilities`, `Configure`, `StreamCompletion`, `Invoke`, `Attach`, and so on). Four primitives live here:

- **`RunSession`** — runs a full agent session on a plugin's behalf. Used for sub-agent spawns today, and reserved for a future non-interactive pipeline mode. Full turn-by-turn semantics live in [`agent-loop/subagents.md`](agent-loop/subagents.md); this document defines only the wire-level calling mechanism.
- **`CountTokens`** — resolves an exact-if-possible token count for a string, the shared primitive every other category's `tokens` field routes through.
- **`Emit`** — how a plugin persists anything into the session's state backend.
- **`Log`** — carries a plugin's own log output into the kernel's centralized logging, so it doesn't vanish into an unread subprocess stderr.

See [`glossary.md`](glossary.md) for how these four terms fit the wider vocabulary, and [`architecture.md`](architecture.md) for the surrounding system (transport, hook dispatch, plan/apply, state backend).

## The callback channel

`hashicorp/go-plugin` natively supports bidirectional plugins — a plugin subprocess can be handed a gRPC client connection back to the host process, not just the reverse. This is exactly the mechanism `RunSession` needs, and nothing new has to be invented at the transport layer: every plugin subprocess, for every category defined in this series, MUST be given this callback connection at handshake time, unconditionally. A plugin that never calls back simply never uses it — the channel's presence isn't category-gated. A context provider needing `CountTokens` is just as valid a caller as a tool provider needing `RunSession`.

```protobuf
KernelCallbackService {
  RunSession(RunSessionRequest) -> RunSessionResult    // agent-loop/subagents.md —
                                                        // full semantics defined
                                                        // there, not repeated here
  CountTokens(CountTokensRequest) -> CountTokensResult // see "CountTokens" below
  Emit(EmitRequest) -> EmitResult                      // see "Emit" below
  Log(LogRequest) -> LogResult                         // see "Log" below
}
```

This channel and the frontend provider's `Attach` RPC are the **only** two genuinely bidirectional RPCs in the whole system — every other category RPC is server-streaming or unary. A new primitive added to this service does not get to default to bidi "just in case"; the shape here is a consequence of `hashicorp/go-plugin`'s native plugin→kernel channel, not a free design choice repeated per RPC.

The callback channel uses a **fixed, well-known broker ID**, not a wire-negotiated one — safe because the kernel is the only party that ever accepts this broker connection, so no collision is possible. Producer identity (`{category, name, version}`) is a property of *which broker connection a call arrived on*, established at handshake, and is server-derived, never client-supplied (see "Emit" and "Log" below) — a plugin cannot declare a producer identity other than its own.

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
     CountTokens RPC (provider/protocol.md#counttokens):
    return (that provider's count, exact: true)
  else:
    return (fallback_heuristic(req.content), exact: false)
```

A model provider's own `CountTokens` (when implemented) uses its real vendor tokenizer — some vendors expose a dedicated counting endpoint, others require a bundled tokenizer library; this document doesn't mandate which, only the RPC shape. [`provider/protocol.md#counttokens`](provider/protocol.md#counttokens) declares this a SHOULD for model providers: the fallback formula below is deliberately kept simple and single-purpose rather than made smarter, on the reasoning that accuracy should come from providers actually implementing real tokenizers, not from the kernel guessing better. Still not a MUST there, since not every vendor makes exact counting cheap or even possible without a network round-trip — but `exact: false` results should be the exception in practice, not the norm.

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

`producer_category`/`producer_name`/`producer_version` are deliberately **not** `EmitRequest` fields: the kernel already knows which plugin is calling — it's a property of the already-authenticated callback connection established at handshake (see "The callback channel" above) — and fills them in server-side. A plugin cannot declare a producer identity other than its own; there's no field to spoof. The same rule applies to `Log` below.

`EventKind` is `state-backend.md`'s authoritative enum, restated here only because it's the wire-level type `Emit` actually carries — this document does not own its definition, and `state-backend.md` remains authoritative. Like every enum in this system, `EventKind`'s zero value, `EVENT_KIND_UNSPECIFIED`, is never valid on the wire — a caller that forgets to set `kind` produces a detectable, named "unspecified" error rather than something that silently looks like a real event kind. Usage/cost, `Render` output, and `session_start`/`session_end` deliberately don't get their own `EventKind` at all — see [`state-backend.md#the-kind-enum`](state-backend.md#the-kind-enum) for why.

## Log

`Log` carries a plugin's own log output into the kernel's centralized logging, so it doesn't vanish into an unread subprocess stderr — plugin logs and the kernel's own logs end up in one place instead of two. Unlike `Emit`, a `Log` call is not tied to an active session: a plugin MAY call `Log` before any session exists (process startup, or from within `Configure`) or after one has ended (during shutdown), so `session_id` is optional here where it is mandatory for `Emit`.

```protobuf
LogRequest {
  session_id  string?    // MAY be omitted — set when the log line is
                          // attributable to a specific session (the common
                          // case), omitted for startup/shutdown/Configure-time
                          // logging that predates or outlives any session
  entry       LogEntry   // MUST — see below
}

LogResult {}   // empty; a Log call either succeeds or the RPC itself errors
```

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

The six-level vocabulary (`TRACE`, `DEBUG`, `INFO`, `WARN`, `ERROR`, `FATAL`) is the canonical logging vocabulary for the whole project, not just this RPC: kernel-native code, not just forwarded plugin logs, uses these same six levels. `LogLevel`'s zero value, `LOG_LEVEL_UNSPECIFIED`, is never valid on the wire, the same convention every enum in this system follows — a `Log` call that omits `level` is a malformed request, not a silently-defaulted one.

`LOG_LEVEL_TRACE` and `LOG_LEVEL_FATAL` do not map directly onto `log/slog`'s four built-in levels (Debug/Info/Warn/Error — the kernel's own logging is slog-only). The kernel MUST translate `LOG_LEVEL_TRACE` to a custom `slog.Level` below `slog.LevelDebug` and `LOG_LEVEL_FATAL` to one above `slog.LevelError`; `slog.Level` is an ordinary `int8` and custom levels are a native, documented pattern, not a workaround. The exact numeric offsets are a kernel implementation detail, not part of this wire protocol.

**`LOG_LEVEL_FATAL` is a severity label only.** Logging at `FATAL` does not itself terminate the plugin or the kernel, and the kernel **MUST NOT** treat a `Log` call carrying it as a request to do anything beyond routing the entry through at that severity. This is worth stating unambiguously because "FATAL" naturally reads as "the process should die" to anyone unfamiliar with this design — it isn't, here. A plugin that logs FATAL and then crashes is still detected and categorized through the ordinary process-crash path (`process_crashed`, per [`tool/conformance.md`](tool/conformance.md) and the parallel handling in other categories), never inferred from having received a FATAL log line.

## Required vs. optional support

| Capability | Level | Notes |
|---|---|---|
| Bidirectional callback channel, unconditional per plugin | MUST | "The callback channel" |
| `RunSession` callable via this channel | MUST | semantics in [`agent-loop/subagents.md`](agent-loop/subagents.md) |
| `CountTokens` callable via this channel | MUST | "CountTokens" |
| Model-provider's own `CountTokens` RPC | SHOULD, per model provider | [`provider/protocol.md#counttokens`](provider/protocol.md#counttokens) |
| Exact-vs-fallback resolution algorithm | MUST | "CountTokens" |
| Single documented fallback formula, no per-caller variation | MUST | "The fallback heuristic" |
| Context/memory providers computing `tokens` via this primitive, not their own heuristic | MUST | "Why a kernel primitive, not a provider-local heuristic" |
| `Emit` callable via this channel | MUST | "Emit" |
| Kernel derives producer identity server-side, never from a client-supplied field | MUST | "Emit", "Log" |
| `Log` callable via this channel | MUST | "Log" |
| `session_id` optional on `Log` (unlike `Emit`, where it's mandatory) | MUST | "Log" |
| Kernel translates `LOG_LEVEL_TRACE`/`LOG_LEVEL_FATAL` onto custom `slog.Level` values outside slog's 4 built-in levels | MUST | "Log" |
| `LOG_LEVEL_FATAL` triggers plugin/kernel termination | MUST NOT | "Log" |

## Open questions

- Whether the kernel should cache `CountTokens` results — the same static content (e.g. a `stability: static` context section, per [`context/README.md`](context/README.md)) being re-counted every turn is wasted work. Not addressed here; a plausible follow-up optimization once a reference implementation exists.
- Whether other future kernel primitives (beyond `RunSession`/`CountTokens`/ `Emit`/`Log`) belong on this same `KernelCallbackService`, or whether some warrant their own dedicated channel — no candidate has surfaced yet, noted for whenever one does.
