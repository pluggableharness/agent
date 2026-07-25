# internal/interactive/drivers/unattended — agent notes

- **`New` has no "acknowledge unsafe" construction gate, unlike the sibling `internal/plandecision/drivers/autoallow`. That is deliberate, not an omission — do not add one.** `autoallow` auto-*approves* mutating calls, which is genuinely dangerous and must be opted into loudly. This driver auto-*refuses*, which is the safe default: an interactive call's whole payload is a human's answer, so there is no safe value to fabricate and no flag that would make fabricating one acceptable. `README.md` and `doc.go` both carry the full rationale because a reader comparing the two tracked deviations will otherwise read the asymmetry as a bug.

- **Construction logs INFO, per-call refusal logs WARN, and the split is intentional.** INFO because selecting this driver in a frontend-less build is expected and safe; WARN per call because *repeated* refusals in one session are the actionable signal ("attach a frontend"). Don't collapse them to one level, and don't promote the WARN to ERROR — the error is returned to the caller, which handles it.

- **`Resolve` both logs and returns the same condition**, which `go-style.md` normally forbids. Deliberate: the WARN is the operator-facing session signal, not a duplicate of the caller's error handling. The exception is documented at the call site too.

- **`ctx.Err()` is checked first and returned bare, not wrapped.** Cancellation is normal control flow (`grpc.md`), and returning it unwrapped keeps a canceled turn from being misreported as a missing-frontend refusal. `unattended_test.go` locks this precedence in for both `context.Canceled` and `context.DeadlineExceeded`. Don't reorder the check below the logging, and don't wrap it with a package prefix.

- **A zero-value `Resolver` is usable** — nil `Logger` falls back to `slog.Default()` per call, nil `Telemetry` skips the span and counter. Both guards exist for hand-assembled values in tests; `drivers.New` always wires both. Don't read the nil-telemetry branch as license to treat instrumentation as optional in wired code.

- **`Provider.StartInteractiveResolve` predates this package.** It was added to `internal/telemetry/span.go` in anticipation of this seam (alongside `StartPlanDecisionResolve` for the sibling deviation), so use it rather than hand-rolling a `tracer.Start`. The `Instruments.InteractiveResolutions` counter, by contrast, was added *by* this package's work.

- **`CallID` is unbounded — log and span attribute only, never a metric attribute** (`internal/telemetry/attributes.go`'s cardinality rule). `ToolName` is bounded by the operator's configured tool set, so it is safe on both.

- **This driver never validates `Request.Arguments` or the (absent) `Response.Payload` against an `output_schema`.** That's the caller's job, per the parent package's contract. A refusal has no payload to validate anyway.
