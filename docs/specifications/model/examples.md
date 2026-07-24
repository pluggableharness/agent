# Model provider — examples

## A provider block in `agent.hcl`

```hcl
required_providers {
  anthropic = {
    source  = "github.com/agentco/provider-anthropic"
    version = "~> 1.2.3"
  }
}

provider "anthropic" {
  api_key  = env("ANTHROPIC_API_KEY")
  base_url = "https://api.anthropic.com"
}
```

`api_key = env("ANTHROPIC_API_KEY")` is resolved by the kernel's HCL/`cty` bridge before `Configure` is ever called — the plugin receives the literal resolved value, never the `env(...)` expression. The `env(...)` argument MUST be a literal string, syntax-validated at config-load time (whether the named variable is actually *set* is a separate, evaluation-time check) — `api_key = env(some_var_name)` (a bare identifier rather than a quoted string) MUST be rejected. See [`configuration/blocks-reference.md`](../configuration/blocks-reference.md).

## The wire protocol

The wire shape is (trimmed to the service declaration and `ModelSpec`):

```protobuf
service ModelService {
  rpc GetCapabilities(GetCapabilitiesRequest) returns (GetCapabilitiesResponse);
  rpc Configure(ConfigureRequest) returns (ConfigureResponse);
  // buf:lint:ignore RPC_RESPONSE_STANDARD_NAME
  rpc StreamCompletion(StreamCompletionRequest) returns (stream StreamEvent);
  rpc CountTokens(CountTokensRequest) returns (CountTokensResponse);
  rpc Render(RenderRequest) returns (RenderResponse);
  rpc Describe(DescribeRequest) returns (DescribeResponse);
}

message ModelSpec {
  string id = 1;
  int64 context_window = 2;
  int64 max_output_tokens = 3;
  bool supports_tool_use = 4;
  bool supports_vision = 5;
  bool supports_streaming = 6;
  optional bool supports_parallel_tool_calls = 7;
  ThinkingSpec thinking = 8;
  CachingSpec caching = 9;
  Pricing pricing = 10;
  repeated ToolChoiceMode supported_tool_choice_modes = 11;
  bool supports_documents = 12;
}
```

Note the `buf:lint:ignore RPC_RESPONSE_STANDARD_NAME` on `StreamCompletion`: the streamed element type is the bare `StreamEvent`, naming the streamed domain concept per [`protocol.md`](protocol.md#streamcompletion) rather than a `StreamCompletionResponse` wrapper — an intentional, annotated deviation from buf's default naming lint, not an oversight.

## A full `StreamCompletion` event sequence

A single turn where the model answers with text, then requests one tool call, expressed as the `oneof StreamEvent.event` variants:

```text
→ StreamCompletionRequest{
    messages: [ {role: user, content: [{text: "What's in main.go?"}], id: "01J..."} ],
    model_id: "claude-opus-5",
    tools: [ {name: "read_file", input_schema: {...}} ],
    assembled_context: [
      {provider: "project-context", label: "CLAUDE.md", content: [...], tokens: 812, stability: STABILITY_STATIC},
    ],
    call_context: {session_id: "01J...", turn_id: "01J...", working_directory: "/repo"},
    cache_breakpoints: [ {after_assembled_context: {}} ],
  }

← StreamEvent{text_delta:        {text: "Let me check "}}
← StreamEvent{text_delta:        {text: "that file."}}
← StreamEvent{tool_call_start:   {id: "tc_1", name: "read_file"}}
← StreamEvent{tool_call_delta:   {id: "tc_1", arguments_fragment: "{\"path\":"}}
← StreamEvent{tool_call_delta:   {id: "tc_1", arguments_fragment: "\"main.go\"}"}}
← StreamEvent{tool_call_done:    {id: "tc_1"}}
← StreamEvent{usage:             {input_tokens: 412, output_tokens: 28}}
← StreamEvent{stop:              {reason: STOP_REASON_TOOL_USE}}
```

The `assembled_context` entry above is what the adapter maps to Anthropic's `system` parameter (or an equivalent leading system-role message for a vendor without a dedicated system slot) — it never appears in `messages` itself, per `content.v1.Role` having no `SYSTEM` value. The single `cache_breakpoints` entry tells the adapter to place a cache-control marker immediately after that assembled-context content, since it's the turn's `STABILITY_STATIC`, longest-stable-prefix content — the adapter translates this to Anthropic's `cache_control: {type: "ephemeral"}` on the corresponding system block.

A turn where the model declines to answer, having been constrained to a specific tool it chose not to use:

```text
→ StreamCompletionRequest{
    messages: [ ... ],
    model_id: "claude-opus-5",
    tools: [ {name: "delete_repo", input_schema: {...}} ],
    params: { tool_choice: {mode: TOOL_CHOICE_MODE_NONE} },
    ...
  }

← StreamEvent{text_delta: {text: "I won't do that — it looks destructive and unconfirmed."}}
← StreamEvent{usage:      {input_tokens: 201, output_tokens: 19, reasoning_tokens: 143}}
← StreamEvent{stop:       {reason: STOP_REASON_REFUSAL}}
```

`reasoning_tokens: 143` here is billed at `pricing.output_per_mtok`, per [`protocol.md#cost-computation`](protocol.md#cost-computation), and is never folded into the reported `output_tokens: 19`.

A turn stopped by a caller-supplied stop sequence:

```text
→ StreamCompletionRequest{ ..., params: { stop_sequences: ["</answer>"] } }

← StreamEvent{text_delta: {text: "42"}}
← StreamEvent{stop:       {reason: STOP_REASON_STOP_SEQUENCE, matched_stop_sequence: "</answer>"}}
```

The kernel accumulates `tool_call_delta` fragments by `id` into the final parsed-JSON arguments before dispatching to the tool provider — per [`data-types.md#tool-schema`](data-types.md#tool-schema), arguments are always stored as already-parsed JSON in the kernel's internal representation, regardless of whether the vendor sent a JSON-encoded string (OpenAI/Mistral) or an already-parsed object (Anthropic/Gemini/Ollama) on its own wire.

If the kernel cancels the stream mid-flight (user hit Ctrl-C), the plugin sees `context.Canceled`, stops generating, and the stream simply ends — there is no `StreamEvent{stop: {reason: STOP_REASON_CANCELLED}}` guaranteed from every vendor backend, since the cancellation is a transport-level gRPC operation, not a vendor-API-level one for every vendor. A plugin MAY still emit a `STOP_REASON_CANCELLED` stop event on a best-effort basis where its vendor SDK makes that easy.

## Cost-computation worked example

Given `pricing` from a `ModelSpec` and a received `usage` event:

```text
pricing = {
  currency: "USD", free: false,
  tiers: [{ input_per_mtok: 3.00, output_per_mtok: 15.00,
            cache_read_per_mtok: 0.30, cache_write_per_mtok: 3.75 }]
}
usage = { input_tokens: 412, output_tokens: 28, cache_read_tokens: 0, cache_write_tokens: 0 }

cost_usd = 412 * 3.00 / 1e6 + 28 * 15.00 / 1e6 + 0 + 0
         = 0.001236 + 0.00042
         = 0.001656
```

The kernel persists `0.001656` into the state backend's `cost_ledger` table ([`state-backend.md#cost_ledger`](../state-backend.md#cost_ledger)) at the moment the `usage` event is received — using whichever `PricingTier` matches that timestamp, per [`data-types.md#pricing`](data-types.md#pricing)'s resolution rule.

Had this `usage` event carried `reasoning_tokens`, the worked sum above would gain one more term, `reasoning_tokens * output_per_mtok / 1e6`, using the same `output_per_mtok` rate — see [`protocol.md#cost-computation`](protocol.md#cost-computation)'s full five-term formula. Had `pricing.tiers` declared an `input_tokens_from`/`input_tokens_until` bound (e.g. a higher rate above 200k input tokens), the kernel would first resolve which tier matches `(usage_timestamp, input_tokens)` before doing this arithmetic — the calculation itself is unchanged once the right tier is selected.

**This dollar figure is distinct from telemetry.** Telemetry instrumentation separately *observes* the same usage numbers for dashboards and tracing, but never recomputes or owns the persisted figure — a second computation path for the same number would be exactly the kind of divergence that undermines replay fidelity. Usage instrumentation takes the already-computed cost figure as input and mirrors it onto metrics and trace spans; it runs only after the kernel's cost-ledger write has computed `cost_usd`, never before, which keeps it a pure observability mirror rather than a second source of truth.
