# Model provider — data types

## `ModelSpec`

```protobuf
ModelSpec {
  id                 string   // MUST — vendor's exact model identifier
  context_window     int      // MUST — input token budget
  max_output_tokens  int      // MUST
  supports_tool_use          bool  // MUST
  supports_vision             bool  // MUST
  supports_streaming          bool  // MUST — UX hint only, see protocol.md; RPC shape is always streaming
  supports_parallel_tool_calls bool // SHOULD — several vendors allow multiple
                                     // tool_use blocks in one turn; a false/absent value
                                     // means the kernel must serialize tool calls for this model
  thinking   ThinkingSpec  // MUST be present; use { supported: false } if none
  caching    CachingSpec   // MUST be present; use { supported: false } if none
  pricing    Pricing       // MUST be present — see below
}
```

Rationale for the sum-type shape of `ThinkingSpec`/`CachingSpec` below: all three thinking modes and both caching modes are in active use across real vendors, sometimes multiple modes on different models *from the same vendor* (Anthropic's newer models use adaptive thinking; Haiku 4.5 only supports the older discrete `budget_tokens` style). A boolean `supports_thinking` flag would lose information the kernel actually needs to build a correct request.

### `ThinkingSpec`

```protobuf
ThinkingSpec {
  supported bool
  mode      enum { none, always_on_adaptive, discrete_effort, continuous_budget }
  effort_levels   []string   // required if mode == discrete_effort, e.g. ["low","medium","high","xhigh","max"]
  budget_range    {min, max} // required if mode == continuous_budget (token count range)
  can_disable     bool       // MUST — some vendors' reasoning cannot be turned off (e.g. a
                              // Grok model defaulting reasoning on with no off switch)
  default         string?    // MUST when mode != none — the effort level (discrete_effort)
                              // or budget-token value (continuous_budget, as a string) the
                              // vendor applies when a request omits thinking config entirely.
                              // Makes the actual default behavior visible/auditable in
                              // GetCapabilities instead of hidden in adapter code — a kernel
                              // wanting deterministic behavior can read this and always send
                              // an explicit override rather than guessing what "unspecified"
                              // means for a given model.
}
```

### `CachingSpec`

```protobuf
CachingSpec {
  supported bool
  mode      enum { none, explicit_markers, implicit_automatic }
  // explicit_markers: caller must place cache breakpoints on content blocks (Anthropic/Mistral-style)
  // implicit_automatic: vendor applies caching transparently above a token threshold, no caller action
  keepalive_supported  bool  // MUST, default false — see the keepalive note below
}
```

**Cache keepalive is a provider-owned behavior, not a kernel mechanism.** A dedicated keepalive daemon — re-pinging every 5 minutes so a long tool-execution gap doesn't let a prompt-cache TTL expire — is a real-world pattern (as used, for example, by Aider) and a real cost concern given `cache_read_per_mtok` (below) is typically far cheaper than `input_per_mtok`. This is deliberately **not** a kernel-loop responsibility: cache TTL mechanics are vendor-specific (5m/1h TTLs differ per vendor), and the adapter that already understands its own vendor's `CachingSpec.mode` is the natural owner of keeping that cache warm — not a kernel that would need to learn every vendor's TTL semantics to orchestrate a generic loop. A model provider MAY implement its own internal keepalive (e.g. a background goroutine within the plugin subprocess watching elapsed time since the last real call) and declares this via `keepalive_supported` so the kernel/operator can tell whether a given provider implements the optimization, without the kernel ever driving the loop itself.

## `Pricing`

```protobuf
Pricing {
  currency    string          // MUST — "USD" for v1 (see conformance.md's open questions);
                               // reserved for future multi-currency support, not acted on yet
  free        bool            // MUST — true for local/free-to-run models (e.g. an
                               // Ollama-served model); when true, tiers MAY be omitted
                               // entirely (or a single zero-rate tier supplied)
  tiers       []PricingTier   // MUST have at least one entry unless free == true
}

PricingTier {
  effective_from          string?   // ISO 8601 date/timestamp; MAY be omitted, meaning
                                     // "since this plugin version was published"
  effective_until         string?   // ISO 8601; MAY be omitted, meaning "still current" —
                                     // an omitted effective_until marks the currently
                                     // active tier
  input_per_mtok           float64  // MUST — cost per million input tokens, realtime rate
  output_per_mtok          float64  // MUST — cost per million output tokens, realtime rate
  cache_write_per_mtok     float64? // MUST be present iff caching.supported
  cache_read_per_mtok      float64? // MUST be present iff caching.supported — typically
                                     // far cheaper than input_per_mtok, the entire point
                                     // of caching
  batch_input_per_mtok     float64? // MAY — a vendor's discounted batch/async rate, where
                                     // one exists (e.g. a Gemini-style batch tier)
  batch_output_per_mtok    float64? // MAY, paired with batch_input_per_mtok
}
```

This shape expands beyond a flat current-rate snapshot to model both time-bounded (promotional/expiring) and tiered (realtime vs. batch) rates — an Anthropic-style intro-pricing window and a Gemini-style batch discount are both realistic, concrete examples of what this needs to represent, not hypothetical.

**Kernel resolution**: given a timestamp (the moment `usage` was received, [`protocol.md#cost-computation`](protocol.md#cost-computation)), the kernel selects the tier where `effective_from <= timestamp < effective_until` (treating an omitted bound as unbounded on that side); exactly one tier MUST match at any given timestamp — a plugin author publishing overlapping or gapped tiers has published an invalid `Pricing` value, and the kernel MUST reject it at capability-load time, not silently pick one. Whether an overlapping-tier rejection should instead be a softer warning is an open question — see [`conformance.md`](conformance.md#open-questions).

`Pricing` MUST be present on every `ModelSpec`, even a free one — it is a required message field on the wire, not optional, for exactly that reason.

## `StreamEvent`

The full shape of what `StreamCompletion` streams back — see [`protocol.md#streamcompletion`](protocol.md#streamcompletion) and [`examples.md`](examples.md) for a worked sequence.

```protobuf
StreamEvent = oneof {
  text_delta          { text: string }
  thinking_delta       { text: string }                    // only when ThinkingSpec.supported
  thinking_signature   { signature: bytes }                // see "Canonical message" below —
                                                             // MUST be emitted if the vendor's
                                                             // thinking blocks carry an integrity
                                                             // signature
  tool_call_start      { id: string, name: string }
  tool_call_delta       { id: string, arguments_fragment: string }  // partial-JSON accumulation
  tool_call_done        { id: string }
  usage                 { input_tokens, output_tokens, cache_read_tokens?, cache_write_tokens? }
  stop                   { reason: StopReason }
  error                  ProviderError                     // see conformance.md#error-taxonomy
}

StopReason = enum {
  end_turn           // the model completed its turn normally
  tool_use           // the model stopped to request one or more tool invocations
  max_tokens         // the model hit its output token limit before completing its turn
  content_filtered   // the vendor's content filter stopped generation
  cancelled          // the kernel cancelled the stream (user interrupt, timeout, turn
                     // abort) — MUST be treated by the plugin as normal control flow,
                     // never as an error
}
```

A plugin MUST classify every terminal failure via a `stop` event's `content_filtered` reason or an `error` event carrying a `ProviderError` ([`conformance.md#error-taxonomy`](conformance.md#error-taxonomy)) — the in-band `error` variant is how a plugin reports a classified failure *within* an otherwise-open stream, distinct from the stream simply being torn down at the transport level (a gRPC-level status, or the kernel closing the stream on cancellation). A plugin whose backend fails outright before producing any events MAY end the stream with just an `error` event and no preceding `stop`.

## Canonical message & content-block schema

Per [`architecture.md`](../architecture.md#canonical-message--tool-schema-format), the canonical form is content-block messages: `text`, `tool_use`, `tool_result`, `image`, `thinking`, `redacted_thinking`. This is the state backend's source of truth, independent of any one vendor's wire format surviving.

- `text` — MUST be supported by every plugin, both directions.
- `image` — MUST be supported by every plugin for a model where `ModelSpec.supports_vision == true`; MUST be rejected with a clear `invalid_request` error (not silently dropped) if sent to a model where it's `false`.
- `tool_use` / `tool_result` — MUST be supported wherever `supports_tool_use == true`.
- `thinking` / `redacted_thinking` — only relevant where `ThinkingSpec.supported == true`. **A `thinking` block MAY carry an opaque, vendor-specific integrity token** (e.g. a cryptographic signature) that the plugin must store verbatim and echo back unmodified on the next turn, or the vendor API will reject the request. The kernel and state backend MUST treat this token as an opaque blob — never inspected, re-derived, or reformatted, just round-tripped. On the wire, this is [`StreamEvent`](#streamevent)'s `thinking_signature` variant (`bytes`) — see [`examples.md`](examples.md).

Each model-provider adapter owns its own lossy translation between this canonical form and its vendor's wire format (e.g. OpenAI has no `thinking` block equivalent — an adapter targeting OpenAI simply never emits one).

## Tool schema

Tool resources (declared by tool providers, not model providers — see [`tool/`](../tool/README.md)) are described once in a common JSON Schema subset, and each model-provider adapter translates that into its vendor's tool-definition wire format.

MUST be supported by every adapter: `type`, `properties`, `required`, `enum`, `items` (array element schema), `description`.

MUST NOT be relied upon by tool authors, and adapters are not required to support: `oneOf`/`anyOf`/`allOf`, `$ref`, `pattern` (regex constraints), `format`, non-trivial `additionalProperties` schemas. Real vendors differ in how they represent the *call* itself, independent of this schema question — notably:

- Anthropic, Google Gemini, Ollama: tool-call arguments arrive as an **already-parsed object**.
- OpenAI, Mistral: tool-call arguments arrive as a **JSON-encoded string** that must be parsed.
- xAI: OpenAI-compatible per vendor docs, presumed string-encoded — needs verification before an adapter ships.

The kernel's internal `ToolCall`/`ToolResult` representation MUST store arguments as already-parsed JSON; each adapter is responsible for serializing to/from its vendor's actual shape (string vs. object) at the translation boundary.
