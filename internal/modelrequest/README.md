# internal/modelrequest

Kernel-side `StreamCompletionRequest` building and validation against a resolved model's declared `ModelSpec`, per [`docs/specifications/model/protocol.md#generation-parameter-validation-and-capability-aware-routing`](../../docs/specifications/model/protocol.md#generation-parameter-validation-and-capability-aware-routing) and [`docs/specifications/model/protocol.md#cache-breakpoint-placement-policy`](../../docs/specifications/model/protocol.md#cache-breakpoint-placement-policy).

## What this package does

Three independent checks the kernel MUST run before a `StreamCompletionRequest` is ever dispatched to a model provider plugin:

- **`ValidateParams`** — resolves a caller's `*modelv1.GenerationParams` against the resolved model's `*modelv1.ModelSpec`. `thinking_effort`/`thinking_budget_tokens` outside the model's declared `ThinkingSpec`, or a `tool_choice.mode` outside `ModelSpec.supported_tool_choice_modes`, is never forwarded to the plugin — it is cleared (fallback to the model's default thinking behavior, or to `TOOL_CHOICE_MODE_AUTO`) and reported back via `Params.FellBackThinking`/`Params.FellBackToolChoice` so a caller can log or, if it wants a stricter policy, fail the turn itself.
- **`ValidateContent`** — rejects a message list containing an `ImageBlock` against a model where `ModelSpec.supports_vision` is false, or a `DocumentBlock` against a model where `ModelSpec.supports_documents` is false. This is a hard reject (`ErrUnsupportedContent`), never a silent drop — [`docs/specifications/frontend/frontend-protocol.md#usermessage-carries-contentblocks`](../../docs/specifications/frontend/frontend-protocol.md#usermessage-carries-contentblocks) requires the kernel surface this conflict rather than swallow it.
- **`PlaceCacheBreakpoints`** — computes where the kernel should mark `StreamCompletionRequest.cache_breakpoints`, meaningful only when the resolved model's `CachingSpec.mode == CACHING_MODE_EXPLICIT_MARKERS`. Placement is a kernel decision, never the plugin's: a model-provider adapter only translates the breakpoints it's given into vendor-native cache-control markers.

## How it fits in

The kernel calls all three whenever it is about to build a `StreamCompletionRequest` for a turn, after routing has already resolved which model/`ModelSpec` will serve it:

1. `ValidateParams(callerParams, resolvedSpec)` — use the returned `Params.Resolved` as `StreamCompletionRequest.params`.
2. `ValidateContent(messages, resolvedSpec)` — if it returns a non-nil error, the turn fails with that error rather than proceeding; the kernel never strips the offending block and retries silently.
3. `PlaceCacheBreakpoints(assembledContext, messages, resolvedSpec)` — use the returned slice as `StreamCompletionRequest.cache_breakpoints` verbatim (it is already `[]*modelv1.CacheBreakpoint`, the exact wire type).

This is pure domain logic — no I/O, no logging, single goroutine, deterministic given its inputs — per [`.claude/rules/logging-telemetry.md`](../../.claude/rules/logging-telemetry.md)'s pure-domain exemption. A caller logs `FellBackThinking`/`FellBackToolChoice` and any `ValidateContent` rejection around this package; nothing in here does.

## `CacheBreakpoint`

`CacheBreakpoint` is a plain alias of `*modelv1.CacheBreakpoint`, not a second Go representation — the generated wire type's three-variant `oneof` (`after_assembled_context` / `after_tools` / `after_message_index`) is already a clean fit for what this package computes, so per [`.claude/rules/go-layout.md`](../../.claude/rules/go-layout.md)'s "internal/ MUST consume the generated types directly" rule, there is no domain wrapper to convert at a boundary.

`PlaceCacheBreakpoints` currently only ever computes an `after_assembled_context` breakpoint (placed when `assembled_context`'s leading section is `STABILITY_STATIC`), not `after_tools` — see `cache.go`'s doc comment for why: the function's fixed inputs (`sections`, `messages`, `spec`) carry no tool-declaration list or turn-to-turn history to judge whether the tools list is actually stable, and inventing that judgment without the data behind it would not be a real kernel decision.
