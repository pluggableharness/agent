# internal/anthropic/messages — agent notes

Two of the rules below look like obvious simplifications. Both would reintroduce real, silent bugs. Read them before touching anything in this package.

## 1. Never use `protojson`. Ever.

Two fields in this package start life as protobuf and end up as bytes on Anthropic's wire:

- `ToolUseBlock.arguments` (a `structpb.Struct`) → a `tool_use` block's `input`
- `schema.v1.Schema` (containing a `map<string, Schema>`) → a tool's `input_schema`

Both MUST be serialized by converting to native Go values first — `(*structpb.Struct).AsMap()`, or a hand-built `map[string]any` tree — and then `encoding/json.Marshal`. **Never `protojson.Marshal`.**

`protojson` deliberately injects non-deterministic whitespace into its output. That is not a bug; it is an explicit design decision by the protobuf authors to discourage anyone from byte-comparing its output. Here, byte-comparison is exactly what happens — just not by us.

**Why it matters concretely:** Anthropic's prompt cache is a **byte-exact prefix match**. If a tool call's arguments serialize differently on turn N+1 than they did on turn N, every byte after that point is a cache miss. So a single `protojson.Marshal` here silently and permanently disables prompt caching for the entire remainder of any conversation that contains a tool call — and there is no error, no warning, and no log line anywhere. The only symptom is a bill that is several times larger than it should be, discovered weeks later.

`encoding/json` sorts map keys. That is what makes the `properties` map deterministic despite Go map iteration order being randomized, and it is why `.claude/rules/determinism.md`'s "sort the keys" rule is satisfied without an explicit sort here. Do not replace it with a "faster" marshaler that does not sort.

There is a regression test — 100 marshals of the same input asserted byte-identical — specifically so a future edit that reaches for `protojson` fails loudly instead of costing money quietly. If it ever fails, the number is not the problem; the marshaler is.

## 2. Thinking signatures and redacted-thinking bytes are opaque. Pass them through untouched.

On Anthropic's wire, `thinking.signature` and `redacted_thinking.data` are base64 **strings**. On our side they are `[]byte` holding **the literal ASCII bytes of that base64 text** — not the decoded payload.

- Receiving: `[]byte(theBase64String)`. Do **not** `base64.Decode`.
- Sending: `string(theBytes)`. Do **not** `base64.Encode`.

It looks wrong. `[]byte` alongside base64 reads like an invitation to decode. Resist it.

**Why:** these values carry a vendor integrity check. A decode-then-re-encode round trip is not guaranteed to reproduce the vendor's exact output — padding and alphabet choices differ between encoders — and any deviation makes the block fail the vendor's check on the next turn. Anthropic's documented behavior there is to reject **the whole conversation**, not just the offending block. So the failure mode is "every multi-turn thinking conversation breaks on turn two", which is both severe and easy to miss in a single-turn test.

Note the contrast with `ImageBlock.data` and `DocumentBlock.data`: those genuinely are raw binary and genuinely do need `base64.StdEncoding.EncodeToString`. Two neighbouring fields, opposite handling. That is the trap.

## 3. The vendor JSON structs are not a second representation of a PluggableHarness message

[`go-layout.md`](../../../.claude/rules/go-layout.md) forbids `internal/` from defining a parallel Go type for a wire message that already has a generated one. `types.go` is not that.

`types.go` describes **Anthropic's own wire format** — a foreign schema this repository does not own and cannot regenerate. [`architecture.md`](../../../docs/specifications/architecture.md#canonical-message--tool-schema-format) explicitly assigns each model-provider adapter the job of translating between the canonical schema and its vendor's, and that translation needs both shapes present in Go. The rule that would be violated is the opposite one: importing `pkg/content` types *into* the vendor structs, so that one struct tried to be both the canonical message and the Anthropic message at once.

So: do not "simplify" `types.go` by embedding `contentv1` types in it, and do not delete it in favour of building `map[string]any` literals inline. The rule it appears to break is not the rule it is governed by.

## 4. No vendor SDK, and the reason is not just dependency weight

`github.com/anthropics/anthropic-sdk-go` is deliberately absent from `go.mod`, and adding it would be a regression on two independent counts:

- **Dependency tax.** This module is the plugin-author SDK every third party imports. Two endpoints (`POST /v1/messages`, `POST /v1/messages/count_tokens`) are used by exactly one plugin; putting a vendor SDK in the root dependency graph makes every downstream plugin author carry it.
- **Retry conflict.** The official SDK retries by default. [`grpc.md`](../../../.claude/rules/grpc.md) is explicit: *"a provider does not invent its own retry policy inside the plugin; it returns the right code and lets the kernel decide."* The kernel's `internal/modelcall` owns retry and backoff. An SDK retrying underneath us would multiply the kernel's retry budget by its own, invisibly.

If a future change needs a third endpoint, hand-roll it. The threshold for reconsidering is a lot of endpoints, not one more.

## 5. No retries here. Classify and return.

Set `Category`, `Retryable`, and `RetryAfter` on a `*model.Error`, then return it. Do not sleep, do not loop, do not back off. The kernel decides.

## 6. Cancellation is not an error

A canceled context returns `ctx.Err()` unwrapped so `errors.Is(err, context.Canceled)` works upstream, is **not** converted into a `*model.Error`, and is **not** logged at ERROR. `pkg/model`'s `statusFromErr` maps it to a bare `codes.Canceled` before it crosses the plugin boundary. A cancellation logged as a failure trains operators to ignore real failures.

## 7. Context-length detection is message-sniffing, and that is knowingly fragile

Anthropic has no distinct error type for an over-long prompt: it is a `400 invalid_request_error` whose *message* says the prompt is too long. `classify.go` substring-matches that message to upgrade the category to `context_length_exceeded`.

This will silently stop working if Anthropic rewords the message. That was accepted rather than avoided because the failure direction is safe: the classification degrades to `invalid_request`, which the kernel treats as non-retryable — so a context overflow becomes a clean failure rather than a retry loop against a request that can never succeed. The alternative (not detecting it at all) loses the kernel's ability to shrink context and retry, which is the whole reason the category exists.

If you find the sniff has broken, fix the substrings — do not remove the mechanism, and do not make the fallback retryable.
