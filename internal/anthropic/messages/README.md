# internal/anthropic/messages

Everything Anthropic-shaped. This package is the only place in the repository that knows what Anthropic's wire format looks like; the rest of [`internal/anthropic`](..) deals in `pkg/model` domain types.

## What it owns

| File | Concern |
|---|---|
| `types.go` | Anthropic's own JSON schema — request body, content blocks, tools, streamed events, error envelopes — plus every wire string literal as a named constant |
| `schema.go` | The restricted [`schema.v1`](../../../api/pluggableharness/schema/v1/types.proto) subset → JSON Schema, deterministically |
| `request.go` | Canonical `StreamCompletionRequest` → Anthropic request body: messages, content blocks, system content, tools, tool choice, generation params, cache breakpoints |
| `sse.go` | The server-sent-event reader |
| `events.go` | Anthropic stream events → `model.Sink` calls, behind a small interface seam |
| `client.go` | The `net/http` client for `POST /v1/messages` and `POST /v1/messages/count_tokens` |
| `classify.go` | HTTP status and vendor error type → `model.Error` category, retryability, and retry-after |

## The two directions

**Outbound** (`request.go`, `schema.go`): the kernel hands over a canonical conversation, a tool list, generation params, and a set of cache breakpoints it has already decided the placement of. This package translates each into Anthropic's equivalent — `assembled_context` sections become the top-level `system` array, cache breakpoints become `cache_control` markers, the restricted JSON-Schema subset becomes a tool's `input_schema`.

**Inbound** (`sse.go`, `events.go`): Anthropic's SSE events become `model.Sink` calls. `text_delta` → `TextDelta`, `input_json_delta` → `ToolCallDelta`, `signature_delta` → `ThinkingSignature`, and so on, with the vendor's cumulative `usage` merged across `message_start` and `message_delta` and emitted exactly once.

## The testability seam

`events.go` defines an `EventSink` interface covering the subset of `*model.Sink` the translator uses, with a compile-time anchor:

```go
var _ EventSink = (*model.Sink)(nil)
```

`*model.Sink` can only be constructed by `pkg/model`'s own gRPC handler, so without this seam the translator would be untestable without a live stream. With it, a hand-written recording fake asserts exact call sequences offline. The anchor is what stops the seam drifting away from the real type.

## Determinism is load-bearing here

Two serialization paths in this package feed Anthropic's prompt cache, which is a byte-exact prefix match. Both are pinned to `encoding/json` over native Go values, never `protojson`, and both have a 100-iteration byte-identity regression test.

Separately, thinking signatures and redacted-thinking payloads pass through as opaque bytes and are never decoded or re-encoded.

Both rules look like things a future editor would "clean up". [`CLAUDE.md`](CLAUDE.md) explains what breaks if they do — read it first.

## What this package will not do

No retries, no backoff, no cost arithmetic, and no cache-breakpoint placement. It classifies, translates, and returns; the kernel decides everything else.
