# internal/hookpayload — agent notes

- **Pure domain, no exceptions.** This package is exempt from `.claude/rules/logging-telemetry.md`'s instrumentation requirements. Do not add `log/slog` or `internal/telemetry` imports here; a caller logs/spans around a call into this package instead. Same exemption as `internal/bounds` and `internal/policy`.

- **Point mapping is mechanically derived from the proto oneof.** The eight dispatchable points and their payload-variant names come directly from `api/pluggableharness/hook/v1/events.proto`'s `HookPayload.oneof payload` block. Verify against the generated `pkg/hook/proto/v1/events.pb.go` if the mapping needs updating.

- **Mutable fields per hook-dispatch.md#per-point-transform-mutable-fields.** Only `HOOK_POINT_PRE_MODEL_CALL` has transform-mutable fields (`["messages"]`); every other point returns `nil`. This constraint is load-bearing: the hook dispatcher uses it to reject any response that changes an immutable field, so never add additional mutable points without updating the spec first.

- **ApplyTransform's cloning and comparison strategy.** The function uses `proto.Clone` and `proto.Equal` from `google.golang.org/protobuf/proto` — the same package the project already depends on directly. Do not hand-roll field-by-field comparison or a second reflection-based equality check; the protobuf library's implementations are the single source of truth for message equality.

- **ValidateShape is mode-specific.** Each mode has exactly one valid response variant: OBSERVE→ObserveAck, TRANSFORM→TransformResult, VETO→VetoResult. Any other combination is `HOOK_ERROR_CATEGORY_INVALID_RESPONSE`. Additionally, a veto response's decision field must never be `HOOK_DECISION_UNSPECIFIED` — it's an error, not an implicit allow or deny.

- **Category mapping is exhaustive over error types.** An `ErrInvalidResponse` error always maps to `HOOK_ERROR_CATEGORY_INVALID_RESPONSE` regardless of mode. Other errors map mode-appropriately: TRANSFORM→`HOOK_ERROR_CATEGORY_TRANSFORM_FAILED`, VETO→`HOOK_ERROR_CATEGORY_VETO_FAILED`, OBSERVE→`HOOK_ERROR_CATEGORY_UNKNOWN` (observe errors don't block the chain, so they're less categorized). Do not add special cases for non-`ErrInvalidResponse` errors without checking `docs/specifications/agent-loop/hook-dispatch.md#invalid_response-handling` first.

- **Coverage target is ~95%** — the package is pure domain, deterministic, I/O-free, and safe for concurrent use, so there are no integration tests or fakes. Unit tests at the API boundary should cover every hook point, every mode, every response variant, and the main error paths. Measure with `go test -cover ./internal/hookpayload/...`.
