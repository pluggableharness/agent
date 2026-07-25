// Package modelrequest implements the kernel-side validation and
// cache-breakpoint placement rules a StreamCompletionRequest MUST satisfy
// before it is ever dispatched to a model provider plugin, per
// docs/specifications/model/protocol.md#generation-parameter-validation-and-capability-aware-routing,
// docs/specifications/model/protocol.md#cache-breakpoint-placement-policy,
// and the corresponding data shapes in
// docs/specifications/model/data-types.md#streamcompletionrequest,
// docs/specifications/model/data-types.md#generationparams, and
// docs/specifications/model/data-types.md#cache_breakpoints-and-cache-breakpoint-placement-policy.
//
// Three independent concerns live here, one per file:
//
//   - params.go: ValidateParams resolves a caller's GenerationParams
//     against a resolved model's ModelSpec (specifically its ThinkingSpec
//     and supported_tool_choice_modes), falling back to safe defaults
//     rather than ever forwarding an invalid combination to the plugin.
//   - content.go: ValidateContent rejects a message list containing an
//     ImageBlock or DocumentBlock the resolved model's capability flags
//     don't declare support for — a hard reject, never a silent drop, per
//     data-types.md's "MUST be rejected with a clear invalid_request
//     error" rule for both content-block kinds.
//   - cache.go: PlaceCacheBreakpoints computes where the kernel should
//     mark StreamCompletionRequest.cache_breakpoints, meaningful only when
//     the resolved model's CachingSpec.mode ==
//     CACHING_MODE_EXPLICIT_MARKERS.
//
// This is pure domain logic: no I/O, no logging, no clock reads, single
// goroutine, deterministic given its inputs. Per
// .claude/rules/logging-telemetry.md's pure-domain exemption, this
// package MUST NOT import log/slog or internal/telemetry — a caller logs
// around it (e.g. logging FellBackThinking/FellBackToolChoice at the
// turn-loop call site), nothing in here does.
package modelrequest
