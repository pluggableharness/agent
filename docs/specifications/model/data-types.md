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

Rationale for the shape of `ThinkingSpec`/`CachingSpec` below: a boolean `supports_thinking`/`supports_caching` flag would lose information the kernel actually needs to build a correct request, because real vendors differ in *how* the capability is controlled, not only in whether it exists — sometimes across models from the same vendor.

**Both are sets of independent axes, not one-of-N modes.** This is the correction to an earlier design that modeled each as a single mutually-exclusive enum. Real models occupy more than one position at once, and a single-valued enum forces a provider to declare a half-truth:

- A model MAY reason adaptively *and* expose a named effort ladder simultaneously. Anthropic's Opus 4.8 and Sonnet 5 are exactly this — omitting thinking config runs adaptive reasoning, and `output_config.effort` selects a level on top of it. An adapter for such a model sends both controls in one request.
- A model MAY accept a named effort level *and* an explicit token budget, with the budget deprecated but still functional. Anthropic's Opus 4.6 and Sonnet 4.6 are this; Haiku 4.5 accepts only the budget and errors on effort; Opus 4.7 and later reject the budget with a 400. The status is per-model, never per-vendor, and a spec shape that cannot say so pushes the distinction into adapter code where no caller can see it.
- Whether reasoning can be turned off is not always a yes or no. Anthropic's Opus 5 accepts an explicit disable at effort `high` or below and rejects it at `xhigh` or `max` — so both `true` and `false` are wrong, and the honest answer needs a third value.

Each axis below is therefore declared on its own. A model declares every control it actually accepts, and the kernel validates a requested parameter against the specific control that governs it rather than against a mode that stands in for a whole model family.

### `ThinkingSpec`

```protobuf
ThinkingSpec {
  supported bool                    // MUST — whether this model reasons at all
  effort    EffortControl?          // present iff the model accepts a named effort level
  budget    BudgetControl?          // present iff the model accepts an explicit token budget
  adaptive_by_default bool          // MUST — whether omitting every thinking control still reasons
  disable   enum { unspecified, never, always, conditional }  // MUST when supported
}

EffortControl {
  levels  []string   // MUST be non-empty, e.g. ["low","medium","high","xhigh","max"]
  default string     // MUST — the level the vendor applies when a request omits effort
}

BudgetControl {
  range      {min, max}  // MUST — the accepted token-budget range, inclusive
  default    int64?      // MAY — the budget the vendor applies when a request omits one;
                          // omitted means the vendor reasons zero tokens by default
  deprecated bool        // MUST — the vendor still honors this control but steers callers
                          // to effort/adaptive instead, and MAY remove it in a later model
}
```

`supported == false` means the model has no reasoning capability: `effort` and `budget` MUST both be absent and `adaptive_by_default` MUST be false. `disable` is meaningless in that case — there is nothing to disable — so both `unspecified` and `never` are accepted and mean the same thing, and a reader MUST treat them identically. This keeps the all-zero `ThinkingSpec` a valid declaration for a model that does not reason, which is the common case; only a positive claim that reasoning *can* be turned off (`always` or `conditional`) contradicts `supported == false` and MUST be rejected. Every other combination is a real, declarable position:

| Axis | Meaning when present/true |
|---|---|
| `effort` | The caller MAY select one of `levels`. Absent means this model has no effort ladder — sending an effort level is a kernel-level reject, not something forwarded. |
| `budget` | The caller MAY select a token budget inside `range`. Absent means this model has no budget control; a model that once had one and had it removed declares it absent, not `deprecated`. |
| `adaptive_by_default` | Omitting both controls still produces reasoning. False means an unconfigured request reasons zero tokens. This is what makes the vendor's actual default behavior auditable through `GetCapabilities` rather than hidden in adapter code — a kernel wanting deterministic behavior reads it and sends an explicit override instead of guessing what "unspecified" means. |
| `disable = never` | Reasoning cannot be turned off at all (a Grok model defaulting reasoning on with no off switch; Anthropic's Fable 5, where an explicit disable is a 400). |
| `disable = always` | An explicit disable is accepted in every configuration. |
| `disable = conditional` | An explicit disable is accepted in some configurations and rejected in others — Anthropic's Opus 5 accepts it at effort `high` or below and returns a 400 at `xhigh` or `max`. The protocol deliberately does not model *which* configurations: the condition is vendor-specific and would need a general constraint language to express. What `conditional` buys the kernel is the knowledge that a disable attempt MAY legitimately fail, so such a failure is a vendor policy response and not an adapter bug. |

`EffortControl.default` and `BudgetControl.default` replace a single `default` string that had to encode either kind of value. Each now sits on the control it belongs to, in that control's own type, which also settles the question of what a per-model budget default means separately from the range's bounds.

### `CachingSpec`

```protobuf
CachingSpec {
  supported            bool   // MUST — whether this model caches prompts at all
  explicit_markers     bool   // MUST — the caller MAY place cache breakpoints on content blocks
  implicit_automatic   bool   // MUST — the vendor caches transparently above a token threshold
  keepalive_supported  bool   // MUST, default false — see the keepalive note below
}
```

Like [`ThinkingSpec`](#thinkingspec), these are independent axes rather than one-of-N modes, and for the same reason: a real model occupies both positions at once. Google's Gemini 2.5 and later run implicit automatic caching by default *and* offer explicit manual declaration concurrently, at a deeper discount. An enum could name only one, which forced an adapter either to under-declare the model or to route the second mechanism outside this protocol entirely.

`supported == false` means the model does not cache: `explicit_markers` and `implicit_automatic` MUST both be false. `supported == true` requires at least one of them to be true — a model that caches by some mechanism the protocol cannot name is not declarable, and silently declaring neither would read as "no caching" to every caller.

| Axis | Meaning when true |
|---|---|
| `explicit_markers` | The caller places cache breakpoints and the adapter translates them into vendor-native markers (Anthropic's `cache_control`, Mistral's `prompt_cache_key`). This is the axis [`cache_breakpoints`](#cache_breakpoints-and-cache-breakpoint-placement-policy) is gated on. |
| `implicit_automatic` | The vendor caches transparently above some token threshold with no caller action. Declaring this does not require the kernel to do anything; it exists so cost and cache-hit behavior are explicable rather than surprising. |

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
  stream_start         { provider_request_id: string }  // MAY — see below
  text_delta          { text: string }
  thinking_delta       { text: string }                    // only when ThinkingSpec.supported
  thinking_signature   { signature: bytes }                // see "Canonical message" below —
                                                             // MUST be emitted if the vendor's
                                                             // thinking blocks carry an integrity
                                                             // signature
  redacted_thinking    { data: bytes }                     // a complete vendor-encrypted
                                                             // reasoning block, not a fragment;
                                                             // only when ThinkingSpec.supported
  tool_call_start      { id: string, name: string }
  tool_call_delta       { id: string, arguments_fragment: string }  // partial-JSON accumulation
  tool_call_done        { id: string }
  usage                 { input_tokens, output_tokens, cache_read_tokens?, cache_write_tokens?, reasoning_tokens?, rate_limits[],
                          vendor_cost?, vendor_total_tokens?, components[], reasoning_already_counted? }
  stop                   { reason: StopReason, matched_stop_sequence?: string, model_affirmed?: bool }
  error                  ModelError                        // see conformance.md#error-taxonomy
  metadata               StreamMetadata                    // non-content facts; see #stream-metadata
  safety_notice          { kind: SafetyKind, message?: string, attrs{} }
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

### `stream_start` and vendor request correlation

`stream_start` carries the vendor's own identifier for this request — an Anthropic `request-id` header, an OpenAI `x-request-id` — as soon as the adapter learns it, normally from response headers before any content streams. It exists so a failure can be correlated with the vendor's own logs when asking them what went wrong. A plugin whose vendor publishes no such id MAY omit the event entirely.

It is a separate early event rather than a field on `stop` deliberately: an id that arrives only on successful completion is absent in exactly the case it is needed. The kernel treats the value as opaque — logged and surfaced, never parsed.

### `usage.rate_limits`

`rate_limits` reports the vendor's own rate-limit budgets as of this completion. It MAY be empty; a vendor that publishes nothing has nothing to declare, and an adapter MUST NOT synthesize a snapshot from its own bookkeeping.

It is repeated because vendors publish several budgets at once and they exhaust independently — OpenAI and xAI return separate request and token headers, Anthropic reports input and output separately. Naming *which* budget is close to empty is the entire value: "you have 2% left" is unactionable without saying 2% of what, and a user whose session stops mid-task without being told which ceiling they hit cannot act on it. Every numeric field on a snapshot is optional, so an adapter reports the subset its vendor actually returned rather than inventing the rest.

A snapshot also carries `limit_id`, `limit_name`, `window_role` (`primary` | `secondary`), `used_percent`, and `window_seconds`. These exist for subscription products, which meter several windows at once and frequently publish only a percentage. Before them, an adapter facing such a vendor had to pick a `RateLimitKind` per window and fake `limit = 100`, `remaining = 100 - percent` to fit the absolute fields — a mapping that reads as authoritative and is not. **An adapter MUST NOT derive one form from the other**: a vendor publishing real counts sets `remaining`/`limit`; a vendor publishing a percentage sets `used_percent`; neither is computed from the other, and a budget the vendor did not report is absent rather than zero.

### `usage.vendor_cost` — reported, never authoritative

Some vendors return their own price for a completion (xAI's `cost_in_usd_ticks`). `vendor_cost` carries it as `{ amount, unit, currency? }`, where `amount` is an **exact decimal string** — not a double, because these reconcile against invoices and binary floating point cannot represent them exactly — and `unit` names the vendor's own denomination (`"usd"`, `"xai_ticks_1e10"`).

**This does not change who computes cost.** The kernel still derives and persists `cost_usd` from the token counts plus the matching [`PricingTier`](#pricing) at the completion's receipt time, and every rollup, budget check, and replay reads that computed figure. `vendor_cost` is persisted beside it so an operator can see that list price and actual bill disagree, and so a subscription session — where computed cost is structurally `0.00` under `free: true` — has something truthful to show. Making the vendor figure authoritative would put two costs in the ledger with no deterministic rule for which one a replay reproduces, which the repository's replay-determinism rule (`.claude/rules/determinism.md`) treats as a correctness bug. The kernel MUST NOT convert between units: a conversion it invented would be one more unaudited number in the ledger.

`vendor_total_tokens` and `components[]` follow the same reporting-not-deriving rule. `vendor_total_tokens` is recorded only when the vendor publishes a total that is *not* the sum of the parts — the disagreement is the point, so the kernel MUST NOT fill it in by addition. `components[]` carries vendor-defined counters with no first-class field (`{ name, value }`): per-modality input tokens, accepted/rejected prediction tokens, hosted-tool source counts. They are opaque — stored and surfaced, never interpreted — and **the kernel MUST sort them by `name` before persisting**, or the event log would inherit the adapter's own map iteration order.

`reasoning_already_counted` is set only on an explicit vendor signal (OpenAI's `X-Reasoning-Included`) that `reasoning_tokens` is already inside `output_tokens`. Absent means "not stated", and the kernel applies the default above: a distinct count. It exists to stop the kernel double-counting reasoning in its own estimates, so guessing at it defeats the purpose.

### `stream_metadata` — how the vendor is serving this request

`metadata` carries non-content facts: `actual_model`, `system_fingerprint`, `service_tier`, `rate_limits[]`, `live_context_window`, `live_max_output_tokens`, `catalog_etag`, `sticky_turn_token`, and an `attrs{}` escape hatch.

It is separate from `stream_start` because the two have different schedules. `stream_start` fires once when the vendor accepts the request; metadata may not be knowable until headers land, may change mid-stream, and **MAY be emitted more than once** — a later event supersedes an earlier one *field by field*, and an absent field means "no new information", never "cleared".

`actual_model` is the load-bearing field. Vendors remap for safety routing, capacity, and deprecation (`grok-4` resolving to `grok-4.3`), and dropping that fact has two costs: a silent quality change becomes unattributable, and the kernel's own `cost_usd` cites pricing for a model that never ran.

**Neither `metadata` nor `safety_notice` is a content-block boundary.** Both carry no content and both may arrive mid-stream, so a kernel accumulating a message MUST NOT close an open `text` or `thinking` block on receiving one — doing so splits a run of deltas around a header revision or a moderation notice.

`safety_notice` reports the vendor interposing: `buffering` (output held for review, so a stall is expected and is not a hang), `moderation`, or `verification_required` (the account must complete a challenge the kernel cannot satisfy itself). A kernel that does not recognize a `kind` MUST ignore the event rather than failing the turn — an unexplained stall is strictly worse than an unrecognized notice.

A plugin MUST classify every terminal failure via a `stop` event's `content_filtered` reason or an `error` event carrying a `ModelError` ([`conformance.md#error-taxonomy`](conformance.md#error-taxonomy)) — the in-band `error` variant is how a plugin reports a classified failure *within* an otherwise-open stream, distinct from the stream simply being torn down at the transport level (a gRPC-level status, or the kernel closing the stream on cancellation). A plugin whose backend fails outright before producing any events MAY end the stream with just an `error` event and no preceding `stop`.

## Canonical message & content-block schema

Per [`architecture.md`](../architecture.md#canonical-message--tool-schema-format), the canonical form is content-block messages: `text`, `tool_use`, `tool_result`, `image`, `thinking`, `redacted_thinking`, `document`. This is the state backend's source of truth, independent of any one vendor's wire format surviving.

- `text` — MUST be supported by every plugin, both directions.
- `image` — MUST be supported by every plugin for a model where `ModelSpec.supports_vision == true`; MUST be rejected with a clear `invalid_request` error (not silently dropped) if sent to a model where it's `false`.
- `document` — inline non-image document content (e.g. a PDF), carrying `data: bytes`, `media_type: string`, and an optional `filename`. MUST be supported by every plugin for a model where `ModelSpec.supports_documents == true`; MUST be rejected with a clear `invalid_request` error (not silently dropped) if sent to a model where it's `false` — the same rule `image`/`supports_vision` already establishes, applied to a second, independent capability flag.
- `tool_use` / `tool_result` — MUST be supported wherever `supports_tool_use == true`.
- `thinking` / `redacted_thinking` — only relevant where `ThinkingSpec.supported == true`. **A `thinking` block MAY carry an opaque, vendor-specific integrity token** (e.g. a cryptographic signature) that the plugin must store verbatim and echo back unmodified on the next turn, or the vendor API will reject the request. The kernel and state backend MUST treat this token as an opaque blob — never inspected, re-derived, or reformatted, just round-tripped. On the wire, this is [`StreamEvent`](#streamevent)'s `thinking_signature` variant (`bytes`) — see [`examples.md`](examples.md).
- A `redacted_thinking` block is the whole-block analogue of that rule: reasoning content the vendor encrypts outright, opaque even as text, that the plugin MUST still store verbatim and echo back unmodified on a later turn or the vendor rejects the entire conversation — not just the affected block. On the wire it arrives as [`StreamEvent`](#streamevent)'s `redacted_thinking` variant carrying `data: bytes`, and unlike `thinking_delta` it is never fragmented across events: there is nothing for the kernel to accumulate, so the vendor emits the block whole and the plugin forwards it whole into `ContentBlock`'s `redacted_thinking`. A plugin serving a vendor that produces such blocks MUST emit this variant rather than dropping the content or flattening it into a `thinking` block.

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
  provider_options       Struct?              // MAY — vendor-specific pass-through, see below
}
```

### `assembled_context`

`assembled_context` is the kernel-assembled context chain: the accumulated output of every context provider's `Contribute` call plus memory recall ([`context/protocol.md#contribute-the-context-assemble-rpc`](../context/protocol.md#contribute-the-context-assemble-rpc)), carried as `content.v1.ContextSection` — the same type `context/protocol.md`'s `Contribute` RPC produces and consumes — **not** `context.v1.ContextContribution`. `ContextSection` lives in `content.v1` specifically so `model.v1` can reference it without importing `context.v1` (which itself imports `model.v1` for `ModelTarget`, so the reverse import would be a cyclic file dependency).

Ordering is **chain order**: the same order `context/protocol.md`'s `ContextRequest.prior_sections`/`Contribute` response chain uses, where each provider appends its own section after the sections it received. This is the tools → system → static-project-context → conversation-tail prefix ordering `context/data-types.md#ordering--chaining` establishes for prompt-cache reuse.

`assembled_context` is distinct from `messages`: it is system-level/preamble content, never a conversational turn — which is exactly why it's a separate field rather than a synthetic message. **`content.v1.Role` deliberately has no `SYSTEM` value** ([`data-types.md`](#canonical-message--content-block-schema)'s `Message`/`Role` definitions; `content/v1/types.proto`'s `Role` comment): system-level content is always assembled as a `ContextSection` chain, never carried as a message with a role. Each model-provider adapter maps `assembled_context` to its own vendor's system/preamble mechanism — a top-level `system` string, a leading system-role message, or whatever else that vendor's API expects — and that mapping is adapter-internal, not part of this wire contract.

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

`cache_breakpoints` is meaningful only when the target model declares `CachingSpec.explicit_markers`; an adapter targeting a model that does not MUST ignore this field rather than error on it. The adapter maps each breakpoint to its vendor's own cache-control mechanism (e.g. an Anthropic `cache_control` block on the targeted content).

Gating on `explicit_markers` alone — rather than on a mode that could name only one mechanism — is what lets a model declaring *both* caching axes still honor breakpoints. Under the earlier enum, a model like Gemini 2.5 that declared implicit caching (the accurate description of its default behavior) was thereby required to discard breakpoints it could in fact have honored through its explicit pathway.

**Breakpoint placement is a kernel decision, not the plugin's.** The kernel knows each `assembled_context` section's `Stability` (`content/v1/types.proto`'s `Stability` enum: `STABILITY_STATIC` vs. `STABILITY_DYNAMIC`) and each message's position in the conversation, so it places breakpoints at natural stable-prefix boundaries — the same tools → system → static-project-context → conversation-tail ordering that governs `assembled_context`'s own chain order. In practice this means: a breakpoint after `after_tools` when the tool declaration list is stable turn to turn, and a breakpoint after `after_assembled_context` when the whole chain's leading sections are `STABILITY_STATIC` — since that's usually the longest stable prefix a vendor's prompt cache can actually reuse. A plugin never invents its own placement; it only translates the breakpoints the kernel already decided into vendor-native markers.

### `provider_options`

`provider_options` is an optional `google.protobuf.Struct` carrying vendor-specific request knobs the kernel has no semantics for. It is the escape hatch that lets a third-party provider ship a vendor feature without a change to this protocol — a vendor's `service_tier`, a sampling `seed`, a beta-feature header flag, a conversation-retention id, an incremental-response id. The kernel passes it through untouched: it never reads a key, never validates one, and never assigns meaning to one.

Values originate the same way every other provider-specific value does — from the provider's own `ConfigSchema` and the operator's `provider "<name>" { ... }` block ([`configuration/blocks-reference.md`](../configuration/blocks-reference.md)) — and the provider documents its own accepted keys. Two providers MAY use the same key name for unrelated things; nothing in this protocol coordinates them.

**The rule that keeps this honest: a field the kernel reads MUST NOT live here.** `provider_options` is pass-through by construction, so anything the kernel acts on — a value affecting routing, capability validation, cost computation, or replay — is a typed field on this protocol or it does not work at all. A provider that smuggles such a value through `provider_options` gets no kernel behavior from it, only silence. Concretely: prompt-cache TTL selection cannot live here (the kernel computes `cost_usd` and the TTL changes the rate), a thinking-effort level cannot live here (the kernel validates it against [`ThinkingSpec`](#thinkingspec) and routes fallbacks on it), and a token count cannot live here. When a knob turns out to need kernel behavior, the fix is to promote it to a typed field in a later protocol revision — not to teach the kernel to read `provider_options`.

This is a `Struct` for the same reason `ConfigureRequest.config` is one — the shape is the provider's schema, not the kernel's to name — applied per-request rather than once at configure time. It is deliberately *not* one of the opaque `bytes` payloads (Emit→Render→Paint, event-bus publish), which exist so a producer's payload format can evolve independently of the kernel; `provider_options` exists because the kernel has no opinion about the values at all. See the repository's protobuf rule (`.claude/rules/proto.md`)'s strong-typing section, whose pass-through rule this section restates from the protocol side.

## `GenerationParams`

`GenerationParams` carries per-request overrides of otherwise model-default generation behavior:

```protobuf
GenerationParams {
  thinking_effort          string?          // one of ThinkingSpec.effort.levels; requires effort to be present
  thinking_budget_tokens    int64?           // within ThinkingSpec.budget.range; requires budget to be present
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

This is typed as `common.v1.HookPoint`, not `hook.v1.HookPoint`: `hook/v1/events.proto` imports `model/v1/types.proto` (for `ModelRef`/`Usage` on its pre-model-call/post-model-response hook payloads), so `model/v1/types.proto` importing anything from `hook.v1` for this field would be a cyclic file dependency `buf build` rejects outright. `HookPoint` itself is declared in `common.v1` for exactly this reason, alongside `CallContext` and `ProducerRef`.

## `Describe`

`ModelService` gains a `Describe(DescribeRequest) -> DescribeResponse { producer: common.v1.ProducerRef }` RPC, identical in shape across all seven category protocols in this protocol revision. It reports this plugin build's own identity — `{name, version, source, category, protocol_version}` — directly from the running process. This matters specifically for a `dev_overrides` binary ([`configuration/settings-and-global.md#dev_overrides`](../configuration/settings-and-global.md#dev_overrides)), which bypasses the registry/lock-file resolution path entirely and so has no `provider "<name>" { ... }` lock entry for the kernel to read identity from; see [`configuration/lock-file.md`](../configuration/lock-file.md#dev_overrides-and-identity-without-a-lock-entry)'s `dev_overrides` note for the canonical explanation.

## Tool schema

Tool resources (declared by tool providers, not model providers — see [`tool/`](../tool/README.md)) are described once in a common JSON Schema subset, and each model-provider adapter translates that into its vendor's tool-definition wire format.

MUST be supported by every adapter: `type`, `properties`, `required`, `enum`, `items` (array element schema), `description`.

MUST NOT be relied upon by tool authors, and adapters are not required to support: `oneOf`/`anyOf`/`allOf`, `$ref`, `pattern` (regex constraints), `format`, non-trivial `additionalProperties` schemas. Real vendors differ in how they represent the *call* itself, independent of this schema question — notably:

- Anthropic, Google Gemini, Ollama: tool-call arguments arrive as an **already-parsed object**.
- OpenAI, Mistral: tool-call arguments arrive as a **JSON-encoded string** that must be parsed.
- xAI: OpenAI-compatible per vendor docs, presumed string-encoded — needs verification before an adapter ships.

The kernel's internal `ToolCall`/`ToolResult` representation MUST store arguments as already-parsed JSON; each adapter is responsible for serializing to/from its vendor's actual shape (string vs. object) at the translation boundary.
