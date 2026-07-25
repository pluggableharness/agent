# internal/interactive/drivers/fake

A scripted [`interactive.Resolver`](../../) for tests.

Pre-program a `Response` (the "a human answered" path) or an error (the "no frontend" path, or any other failure), and every `Resolve` call returns it. Every request handed to `Resolve` is recorded in call order and readable via `Requests()`, so a consumer can assert *what* it asked the human, not just that it asked.

```go
r := fake.New(interactive.Response{Payload: answer}, nil)          // human answered
r := fake.New(interactive.Response{}, interactive.ErrNoFrontend)   // nothing attached
var r fake.Resolver                                                // zero value: empty answer, no error
```

This exists so the future tool scheduler can exercise both paths without depending on [`drivers/unattended`](../unattended/) or on a real frontend — scripting `interactive.ErrNoFrontend` here reproduces the unattended path without importing it.

Per `go-testing.md` it is hand-written rather than generated: a fake is a small real implementation, not a mock with `.EXPECT()` recording. It is concurrency-safe (one mutex) even though interactive calls execute sequentially by spec — a fake that races under `go test -race` would be a worse debugging experience than the lock costs. It honors `ctx` cancellation like any real `Resolver` must: an already-done context returns `ctx.Err()` and records nothing.

It is deliberately **not** registered in [`drivers/drivers.go`](../drivers.go)'s selector — its scripting can't be expressed through that signature, so tests construct it directly.
