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
  supported_tool_choice_modes []ToolChoiceMode // SHOULD — which GenerationParams.tool_choice.mode
                                     // values this model accepts (see below); empty means
                                     // this model can't constrain tool choice at all
  supports_documents bool  // MUST — whether this model accepts a DocumentBlock content block
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
  input_tokens_from        int64?   // MAY — smallest accumulated-input-token count this
                                     // tier applies to, inclusive; omitted means unbounded
                                     // below
  input_tokens_until       int64?   // MAY — input-token count this tier stops applying to,
                                     // exclusive; omitted means unbounded above
}
```

This shape expands beyond a flat current-rate snapshot to model time-bounded (promotional/expiring), tiered (realtime vs. batch), and input-size-bounded rates — an Anthropic-style intro-pricing window, a Gemini-style batch discount, and a vendor charging a distinct, higher rate once a request's accumulated input exceeds 200k tokens are all realistic, concrete examples of what this needs to represent, not hypothetical. `input_tokens_from`/`input_tokens_until` add that third dimension alongside the two time bounds; both pairs are independently half-open (an omitted bound is unbounded on that side).

**Kernel resolution**: given a `(timestamp, input_token_count)` pair — the timestamp is the moment `usage` was received ([`protocol.md#cost-computation`](protocol.md#cost-computation)); the input token count is that same `usage` event's `input_tokens` — the kernel selects the tier where `effective_from <= timestamp < effective_until` AND `input_tokens_from <= input_token_count < input_tokens_until` (each bound unbounded on its own side when omitted); exactly one tier MUST match at any given `(timestamp, input_token_count)` pair — a plugin author publishing overlapping or gapped tiers, in either dimension, has published an invalid `Pricing` value, and the kernel MUST reject it at capability-load time, not silently pick one. A plugin declaring only the time dimension (every tier's `input_tokens_from`/`input_tokens_until` both omitted) is a degenerate one-tier-per-input-size case and resolves exactly as it did before this field existed. Whether an overlapping-tier rejection should instead be a softer warning is an open question — see [`conformance.md`](conformance.md#open-questions).

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
  usage                 { input_tokens, output_tokens, cache_read_tokens?, cache_write_tokens?, reasoning_tokens? }
  stop                   { reason: StopReason, matched_stop_sequence?: string }
  error                  ModelError                        // see conformance.md#error-taxonomy
}

StopReason = enum {
  end_turn           // the model completed its turn normally
  tool_use           // the model stopped to request one or more tool invocations
  max_tokens         // the model hit its output token limit before completing its turn
  content_filtered   // the vendor's content filter stopped generation
  cancelled          // the kernel cancelled the stream (user interrupt, timeout, turn
                     // abort) — MUST be treated by the plugin as normal control flow,
                     // never as an error
  refusal             // the model or vendor refused to continue generating — distinct
                     // from content_filtered, which is the vendor's automated content
                     // filter stopping generation; refusal is the model itself
                     // declining (e.g. a safety-trained refusal message)
  stop_sequence       // the model stopped because it generated one of
                     // GenerationParams.stop_sequences; `matched_stop_sequence` carries
                     // which one and MUST be set iff reason == stop_sequence
}
```

`usage.reasoning_tokens` is set only when the vendor reports thinking/reasoning tokens as a distinct count (`ThinkingSpec.supported` models only) and is never also counted in `output_tokens` — a vendor that folds reasoning tokens into its reported `output_tokens` has no separate figure to report, so this stays unset rather than being derived or subtracted. It's billed at `PricingTier.output_per_mtok` unless a future `Pricing` revision declares a distinct reasoning rate; there is none as of this revision.

A plugin MUST classify every terminal failure via a `stop` event's `content_filtered` reason or an `error` event carrying a `ModelError` ([`conformance.md#error-taxonomy`](conformance.md#error-taxonomy)) — the in-band `error` variant is how a plugin reports a classified failure *within* an otherwise-open stream, distinct from the stream simply being torn down at the transport level (a gRPC-level status, or the kernel closing the stream on cancellation). A plugin whose backend fails outright before producing any events MAY end the stream with just an `error` event and no preceding `stop`.

## Canonical message & content-block schema

Per [`architecture.md`](../architecture.md#canonical-message--tool-schema-format), the canonical form is content-block messages: `text`, `tool_use`, `tool_result`, `image`, `thinking`, `redacted_thinking`, `document`. This is the state backend's source of truth, independent of any one vendor's wire format surviving.

- `text` — MUST be supported by every plugin, both directions.
- `image` — MUST be supported by every plugin for a model where `ModelSpec.supports_vision == true`; MUST be rejected with a clear `invalid_request` error (not silently dropped) if sent to a model where it's `false`.
- `document` — inline non-image document content (e.g. a PDF), carrying `data: bytes`, `media_type: string`, and an optional `filename`. MUST be supported by every plugin for a model where `ModelSpec.supports_documents == true`; MUST be rejected with a clear `invalid_request` error (not silently dropped) if sent to a model where it's `false` — the same rule `image`/`supports_vision` already establishes, applied to a second, independent capability flag.
- `tool_use` / `tool_result` — MUST be supported wherever `supports_tool_use == true`.
- `thinking` / `redacted_thinking` — only relevant where `ThinkingSpec.supported == true`. **A `thinking` block MAY carry an opaque, vendor-specific integrity token** (e.g. a cryptographic signature) that the plugin must store verbatim and echo back unmodified on the next turn, or the vendor API will reject the request. The kernel and state backend MUST treat this token as an opaque blob — never inspected, re-derived, or reformatted, just round-tripped. On the wire, this is [`StreamEvent`](#streamevent)'s `thinking_signature` variant (`bytes`) — see [`examples.md`](examples.md).

Each model-provider adapter owns its own lossy translation between this canonical form and its vendor's wire format (e.g. OpenAI has no `thinking` block equivalent — an adapter targeting OpenAI simply never emits one).

### `Message` identity and model attribution

Every `Message` carries, beyond `role` and `content`:

```protobuf
Message {
  role                       Role
  content                    []ContentBlock
  id                         string   // MUST — kernel-assigned ULID, stable across replay
  produced_by_model_id       string?  // assistant messages only — the producing model's ModelSpec.id
  produced_by_provider       string?  // assistant messages only — the producing provider's declared name
}
```

`id` is the correlation anchor for deltas and forking (e.g. a frontend edit-and-resubmit that forks history at a given message) — the kernel assigns it once, at persist time, and it never changes, even when the same conversation is replayed against a newer plugin version. A plugin never generates this value itself.

`produced_by_model_id`/`produced_by_provider` record which model and provider actually produced a `ROLE_ASSISTANT` message. Both are plain strings, not a `model.v1.ModelRef` or `model.v1.ModelTarget` — `content.v1` MUST NOT import `model.v1`, because `model.v1` already imports `content.v1` (for `Message`/`ContentBlock`/`ContextSection`) and the reverse import would be a cyclic file dependency `buf` rejects at build time. Both fields are omitted for a `ROLE_USER` message, or when the producing model is otherwise unknown. Because the kernel's routing/fallback chain ([`protocol.md#generation-parameter-validation-and-capability-aware-routing`](protocol.md#generation-parameter-validation-and-capability-aware-routing)) may serve adjacent turns in the same session from different providers or different models, two consecutive `ROLE_ASSISTANT` messages in one conversation MAY carry different `produced_by_model_id`/`produced_by_provider` values — this is expected, not an anomaly, and MUST be preserved verbatim on replay.

## `StreamCompletionRequest`

`StreamCompletionRequest` is `StreamCompletion`'s request — the full canonical conversation, available tools, and generation params for one completion:

```protobuf
StreamCompletionRequest {
  messages             []Message           // MUST — canonical conversation history, in emission order
  model_id              string              // MUST — selects which of this provider's ModelSpec.id to use
  tools                []ToolDeclaration    // MAY be empty
  params                GenerationParams?   // omitted means every param takes its model-specific default
  assembled_context     []ContextSection     // MUST — the kernel-assembled context chain, see below
  call_context           CallContext          // MUST — session/turn/working-directory attribution, see below
  cache_breakpoints     []CacheBreakpoint     // MAY be empty — see below
}
```

### `assembled_context`

`assembled_context` is the kernel-assembled context chain: the accumulated output of every context provider's `Contribute` call plus memory recall ([`context/protocol.md#contribute-the-context-assemble-rpc`](../context/protocol.md#contribute-the-context-assemble-rpc)), carried as `content.v1.ContextSection` — the same type `context/protocol.md`'s `Contribute` RPC produces and consumes — **not** `context.v1.ContextContribution`. `ContextSection` lives in `content.v1` specifically so `model.v1` can reference it without importing `context.v1` (which itself imports `model.v1` for `ModelTarget`, so the reverse import would be a cyclic file dependency).

Ordering is **chain order**: the same order `context/protocol.md`'s `ContextRequest.prior_sections`/`Contribute` response chain uses, where each provider appends its own section after the sections it received. This is the tools → system → static-project-context → conversation-tail prefix ordering `context/data-types.md#ordering--chaining` establishes for prompt-cache reuse.

`assembled_context` is distinct from `messages`: it is system-level/preamble content, never a conversational turn — which is exactly why it's a separate field rather than a synthetic message. **`content.v1.Role` deliberately has no `SYSTEM` value** ([`data-types.md`](#canonical-message--content-block-schema)'s `Message`/`Role` definitions; `content.proto`'s `Role` comment): system-level content is always assembled as a `ContextSection` chain, never carried as a message with a role. Each model-provider adapter maps `assembled_context` to its own vendor's system/preamble mechanism — a top-level `system` string, a leading system-role message, or whatever else that vendor's API expects — and that mapping is adapter-internal, not part of this wire contract.

### `call_context`

`call_context` is a `common.v1.CallContext { session_id, turn_id, working_directory }`. The kernel MUST set it on every `StreamCompletionRequest`. It's what the plugin passes back on its own `KernelCallbackService.Emit`/`Log` calls ([`kernel-callbacks.md#emit`](../kernel-callbacks.md#emit)) for session/turn attribution, without the adapter having to separately thread `session_id`/`turn_id` through its own call sites by hand.

### `cache_breakpoints` and cache-breakpoint placement policy

`cache_breakpoints` is `[]CacheBreakpoint`, wire-level and **request-scoped** — deliberately not carried on the persisted `content.v1.ContentBlock`, because a breakpoint's placement is a per-request optimization decision, not a durable property of the conversation history itself:

```protobuf
CacheBreakpoint {
  position oneof {
    after_assembled_context   // an empty marker — after the whole assembled_context chain
    after_tools                // an empty marker — after the tools declaration list
    after_message_index        int64   // after the message at this zero-based index in `messages`
  }
}
```

`cache_breakpoints` is meaningful only when the target model's `CachingSpec.mode == CACHING_MODE_EXPLICIT_MARKERS`; a model-provider adapter targeting a model whose `CachingSpec.mode` is `CACHING_MODE_IMPLICIT_AUTOMATIC` or `CACHING_MODE_NONE` MUST ignore this field rather than error on it. The adapter maps each breakpoint to its vendor's own cache-control mechanism (e.g. an Anthropic `cache_control` block on the targeted content).

**Breakpoint placement is a kernel decision, not the plugin's.** The kernel knows each `assembled_context` section's `Stability` (`content.proto`'s `Stability` enum: `STABILITY_STATIC` vs. `STABILITY_DYNAMIC`) and each message's position in the conversation, so it places breakpoints at natural stable-prefix boundaries — the same tools → system → static-project-context → conversation-tail ordering that governs `assembled_context`'s own chain order. In practice this means: a breakpoint after `after_tools` when the tool declaration list is stable turn to turn, and a breakpoint after `after_assembled_context` when the whole chain's leading sections are `STABILITY_STATIC` — since that's usually the longest stable prefix a vendor's prompt cache can actually reuse. A plugin never invents its own placement; it only translates the breakpoints the kernel already decided into vendor-native markers.

## `GenerationParams`

`GenerationParams` carries per-request overrides of otherwise model-default generation behavior:

```protobuf
GenerationParams {
  thinking_effort          string?          // one of ThinkingSpec.effort_levels; THINKING_MODE_DISCRETE_EFFORT only
  thinking_budget_tokens    int64?           // within ThinkingSpec.budget_range; THINKING_MODE_CONTINUOUS_BUDGET only
  max_output_tokens         int64?           // per-request override of ModelSpec.max_output_tokens
  temperature                double?          // sampling temperature; vendor-specific range/semantics, passed through as-is
  stop_sequences            []string          // sequences that MUST stop generation before they're produced
  tool_choice                ToolChoice?      // constrains whether/how the model must use a tool this turn
}

ToolChoice {
  mode         ToolChoiceMode   // MUST be set
  tool_name    string?          // MUST be set iff mode == SPECIFIC; MUST be omitted otherwise
}

ToolChoiceMode = enum { UNSPECIFIED, AUTO, ANY, NONE, SPECIFIC }
```

`stop_sequences`: when a vendor honors one of these, the plugin MUST report it back via `StreamEvent.Stop.matched_stop_sequence` with `StopReason.STOP_SEQUENCE` (see [`#streamevent`](#streamevent)).

`tool_choice`: `AUTO` (the model decides freely — equivalent to omitting `tool_choice` entirely), `ANY` (the model MUST call some tool this turn, but may pick which), `NONE` (the model MUST NOT call any tool this turn, even if tools were declared), `SPECIFIC` (the model MUST call the exact tool named in `tool_name`, which MUST name a tool present in `StreamCompletionRequest.tools`).

**Validation mirrors the existing thinking-params rule.** Just as `thinking_effort`/`thinking_budget_tokens` MUST be validated against the resolved model's `ThinkingSpec` before dispatch ([`protocol.md#generation-parameter-validation-and-capability-aware-routing`](protocol.md#generation-parameter-validation-and-capability-aware-routing)), `tool_choice.mode` MUST be validated against the resolved model's `ModelSpec.supported_tool_choice_modes`: a mode absent from that list is a kernel-level reject-or-fallback, never something forwarded to the vendor and left to surface as a raw API error. `ModelSpec.supported_tool_choice_modes` declares precisely which subset of `AUTO`/`ANY`/`NONE`/`SPECIFIC` a vendor supports, mirroring `ThinkingSpec`/`CachingSpec`'s "declare precisely, don't collapse to a bool" rationale — real vendors differ in which modes they expose. An empty list means the model can't constrain tool choice at all.

## `Capabilities.supported_hook_points`

`Capabilities` (`GetCapabilities`'s response payload) additionally carries `supported_hook_points: []common.v1.HookPoint` — which of the eight dispatchable hook points ([`agent-loop/hook-dispatch.md`](../agent-loop/hook-dispatch.md)) this plugin can serve via `HookSubscriberService.DispatchHook`. The kernel MUST reject an `agent.hcl` `hook{}` block naming a point absent from this list, at config-load time.

This is typed as `common.v1.HookPoint`, not `hook.v1.HookPoint`: `hook.proto` imports `model.proto` (for `ModelRef`/`Usage` on its pre-model-call/post-model-response hook payloads), so `model.proto` importing `hook.proto` for this field would be a cyclic file dependency `buf build` rejects outright. `HookPoint` itself is declared in `common.v1` for exactly this reason, alongside `CallContext` and `ProducerRef`.

## `Describe`

`ModelService` gains a `Describe(DescribeRequest) -> DescribeResponse { producer: common.v1.ProducerRef }` RPC, identical in shape across all six category protocols in this protocol revision. It reports this plugin build's own identity — `{name, version, source, category, protocol_version}` — directly from the running process. This matters specifically for a `dev_overrides` binary ([`configuration/settings-and-global.md#dev_overrides`](../configuration/settings-and-global.md#dev_overrides)), which bypasses the registry/lock-file resolution path entirely and so has no `provider "<name>" { ... }` lock entry for the kernel to read identity from; see [`configuration/lock-file.md`](../configuration/lock-file.md#dev_overrides-and-identity-without-a-lock-entry)'s `dev_overrides` note for the canonical explanation.

## Tool schema

Tool resources (declared by tool providers, not model providers — see [`tool/`](../tool/README.md)) are described once in a common JSON Schema subset, and each model-provider adapter translates that into its vendor's tool-definition wire format.

MUST be supported by every adapter: `type`, `properties`, `required`, `enum`, `items` (array element schema), `description`.

MUST NOT be relied upon by tool authors, and adapters are not required to support: `oneOf`/`anyOf`/`allOf`, `$ref`, `pattern` (regex constraints), `format`, non-trivial `additionalProperties` schemas. Real vendors differ in how they represent the *call* itself, independent of this schema question — notably:

- Anthropic, Google Gemini, Ollama: tool-call arguments arrive as an **already-parsed object**.
- OpenAI, Mistral: tool-call arguments arrive as a **JSON-encoded string** that must be parsed.
- xAI: OpenAI-compatible per vendor docs, presumed string-encoded — needs verification before an adapter ships.

The kernel's internal `ToolCall`/`ToolResult` representation MUST store arguments as already-parsed JSON; each adapter is responsible for serializing to/from its vendor's actual shape (string vs. object) at the translation boundary.
