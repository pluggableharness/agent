# internal/plandecision/drivers

The driver selector for [`internal/plandecision`](../) ([`go-layout.md`](../../../.claude/rules/go-layout.md)'s driver pattern): the one place that maps a resolver name to a constructor, so nothing else in the kernel switches on a driver name.

```go
r, err := drivers.New(drivers.NameAutoAllowUnsafe, drivers.Config{
    AcknowledgeUnsafeAutoAllow: true,
    Logger:                     logger,
    Telemetry:                  prov,
})
```

## Registered names

| Name | Driver | Notes |
|---|---|---|
| `auto-allow-unsafe` | [`autoallow`](autoallow/) | Approves every `ask` item without asking a human — a tracked deviation from [`plan-apply-gate.md#decision-semantics`](../../../docs/specifications/agent-loop/plan-apply-gate.md#decision-semantics). Naming it here is not enough: `Config.AcknowledgeUnsafeAutoAllow` must also be true, or construction fails with `autoallow.ErrNotAcknowledged`. |
| `frontend` | *(reserved, unimplemented)* | The spec-correct resolver: emits a `permission-request` `ServerEvent`, blocks on the matching `ClientEvent.plan_decision`. Deliberately not stubbed — until it exists, the name is a construction error, because an unimplemented resolver must fail loudly rather than return something that pretends to ask. |

[`fake`](fake/) is deliberately **not** registered: scripting it needs per-call responses that have no representation in `Config`, and a selectable fake would be one more route to a resolver that never asks a human. Tests construct it directly.

## No default

`New("")` returns `ErrUnknownDriver`, exactly like a misspelled name. This is the point of the package: with no default, no build can end up on the auto-allow resolver by omission — it has to be named, and then acknowledged.
