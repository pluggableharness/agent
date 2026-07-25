# internal/plandecision/drivers/fake

The hand-written `plandecision.Resolver` test double ([`go-testing.md`](../../../../.claude/rules/go-testing.md): fakes, not mocking frameworks). It lets a plan-gate consumer be tested against every `Decision` shape without a frontend, a real resolver, or generated mock machinery.

```go
// One scripted response per call, in order.
r := fake.New(
    fake.Response{Decision: plandecision.Decision{Decision: planv1.PlanDecision_PLAN_DECISION_ALLOW}},
    fake.Response{Decision: plandecision.Decision{Decision: planv1.PlanDecision_PLAN_DECISION_DENY}},
    fake.Response{Err: errors.New("frontend detached")},
)

// Or the same response for every call, when the verdict isn't what's under test.
r := fake.NewAlways(fake.Response{Decision: plandecision.Decision{
    Decision: planv1.PlanDecision_PLAN_DECISION_ALLOW,
}})
```

- `Calls()` returns every `Request` passed to `Resolve`, in order, including calls that returned an error — for asserting what the consumer actually asked about.
- `Reset()` clears the recorded calls and rewinds the queue.
- Running past the end of a scripted queue returns `ErrExhausted` rather than silently repeating the last response: overrunning the script is a test-setup mistake, and it should say so.
- An already-cancelled `ctx` returns the cancellation error before consulting the script, so a consumer's generic cancellation test behaves the same here as against a real resolver.

This driver is deliberately **not** registered in the [selector](../drivers.go) — see that package's `CLAUDE.md`.
