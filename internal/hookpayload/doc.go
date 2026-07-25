// Package hookpayload implements the HookPayload↔HookPoint mapping and the
// per-point transform-mutable-field enforcement the hook dispatcher needs,
// per docs/specifications/agent-loop/hook-dispatch.md#hook-points,
// #per-point-transform-mutable-fields, and #invalid_response-handling.
//
// HookPayload is a oneof; the set variant *is* the point being dispatched —
// there is no separate HookPoint field to read. Point maps from the payload's
// set oneof variant to the corresponding HookPoint enum value. Mutable
// returns the transform-mutable field names for a given point — only
// pre-model-call's "messages" field is mutable in v1; every other point is
// immutable. ApplyTransform validates and merges a transform subscriber's
// response, enforcing that only mutable fields change. ValidateShape checks
// that a response's oneof variant matches the request's declared mode.
// Category maps a dispatch error to the appropriate HookErrorCategory for
// wire reporting.
//
// # Pure domain, no instrumentation
//
// This package is pure domain logic — deterministic, I/O-free, safe for
// concurrent use, and MUST NOT import log/slog or internal/telemetry
// (.claude/rules/logging-telemetry.md's pure-domain exemption). A caller
// performing I/O or crossing a process boundary logs or spans around a call
// into this package; this package itself never does.
package hookpayload
