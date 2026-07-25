# internal/streamaccum

Accumulates a model provider's `StreamCompletion` event stream into the kernel's canonical content-block `Message` — steps 3-4 of [`docs/specifications/agent-loop/turn-algorithm.md#the-runturn-algorithm`](../../docs/specifications/agent-loop/turn-algorithm.md#the-runturn-algorithm)'s `message := accumulate(stream)`.

## What this package does

A `StreamCompletion` RPC streams back a sequence of `StreamEvent` values — incremental text/thinking fragments, tool-call start/delta/done triples, a whole-block `redacted_thinking`, a final `usage`, and a terminal `stop` or `error` — per [`docs/specifications/model/data-types.md#streamevent`](../../docs/specifications/model/data-types.md#streamevent). `Accumulator` turns that sequence into the one `Message` the rest of the turn algorithm operates on:

- `New()` returns an empty `Accumulator`.
- `Observe(ev)` feeds one `StreamEvent`, in the order the provider emitted it.
- `Result()` returns the accumulated `Message`, the final `Usage`, and the `StopReason` once a terminal event has been seen (`ok == false` before that).
- `Err()` returns the terminal `ModelError` if the stream ended in an `error` variant, `nil` otherwise.

## Why there's no content-block index

The wire format has none. Reading the generated `StreamEvent`/`ContentBlock` types (and the source `.proto` doc comments) confirms `text_delta`, `thinking_delta`, and `thinking_signature` carry no index or id at all — a `tool_use` block is the only kind correlated by an explicit id (`ToolCallStart.id`, echoed on its matching deltas and its `tool_call_done`). So block boundaries for text/thinking content are implicit in the event sequence itself: a run of same-kind deltas is one block, and any other event (a different delta kind, a tool call event, `usage`, `stop`, `error`) closes it. `redacted_thinking` needs no such tracking — it arrives as one complete block per event, never fragmented.

## How it fits in

The kernel's `RunTurn` loop (see the turn-algorithm doc above) calls a model provider's `StreamCompletion`, feeds every event it returns to one `Accumulator` via `Observe`, and once `Result()` reports `ok == true`, passes the resulting `Message` into step 5's `post-model-response` hook dispatch. `Usage` and `StopReason` feed the cost-computation (`internal/cost`) and bounds-tracking (`internal/bounds`) paths respectively — this package produces the raw materials for both but computes neither cost nor bound state itself.

## Relationship to `pkg/model`

`pkg/model/stream.go`'s `Sink` is the plugin-author-facing side of the identical event vocabulary — a `Provider` implementation calls `Sink.TextDelta`/`ToolCallStart`/etc. to *produce* the stream this package *consumes*. `Sink` is unexported-constructor and lives in `pkg/model`; this package never imports it, only the same `pkg/model/proto/v1` wire types both sides share.
