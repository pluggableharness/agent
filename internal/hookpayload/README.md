# hookpayload

This package implements the `HookPayload`↔`HookPoint` mapping and the per-point transform-mutable-field enforcement the hook dispatcher needs, per `docs/specifications/agent-loop/hook-dispatch.md`.

## Overview

`HookPayload` is a protobuf `oneof` message; the set variant *is* the point being dispatched — there is no separate `HookPoint` field to read. This package provides:

- **Point**: Maps from a payload's set oneof variant to its corresponding `HookPoint` enum value.
- **Mutable**: Returns the list of transform-mutable field names for a given hook point. Only `HOOK_POINT_PRE_MODEL_CALL` has mutable fields (`["messages"]`); all others are immutable.
- **ApplyTransform**: Validates and merges a transform subscriber's response, enforcing that only mutable fields have changed.
- **ValidateShape**: Checks that a response's oneof variant matches the request's declared `HookMode` (observe→ObserveAck, transform→TransformResult, veto→VetoResult).
- **Category**: Maps a dispatch error to the appropriate `HookErrorCategory` for wire reporting.

## Key invariants (from the spec)

- Only `pre-model-call`'s `messages` field is transform-mutable in v1. A transform subscriber at any other point must return the payload byte-identical to the request.
- A response's oneof variant must match the request's declared mode. A mismatch is `HOOK_ERROR_CATEGORY_INVALID_RESPONSE`.
- A veto response's decision must not be `HOOK_DECISION_UNSPECIFIED`.
- The eight dispatchable hook points are session-start, pre-model-call, post-model-response, pre-tool-call, plan-ready, post-tool-call, post-apply, and session-end. `context-assemble` is not a hook.v1 dispatch point.

## Usage

```go
// Map a payload to its hook point
point, ok := hookpayload.Point(payload)
if !ok {
    // Payload has no variant set
}

// Get mutable fields for a point
mutable := hookpayload.Mutable(point)  // ["messages"] for pre-model-call, nil otherwise

// Validate and apply a transform response
merged, err := hookpayload.ApplyTransform(request, response)
if err != nil {
    // Response violates mutable-field constraints
}

// Validate response shape against mode
err := hookpayload.ValidateShape(mode, response)
if err != nil {
    // Response variant doesn't match mode
}

// Map error to category for event reporting
category := hookpayload.Category(mode, err)
```

## Testing

The package targets ~95% coverage with table-driven tests covering all eight hook points, all mode/response combinations, and all error cases. Coverage is verified with `go test -cover ./internal/hookpayload/...`.
