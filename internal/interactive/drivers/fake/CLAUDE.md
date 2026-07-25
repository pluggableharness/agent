# internal/interactive/drivers/fake — agent notes

- **Not registered in the `drivers` selector, unlike `internal/telemetry/drivers/fake`.** Its scripted `Response`/`Err` can't travel through `drivers.New(name, logger, telem)`, so tests construct it directly. `drivers_test.go` asserts `New("fake", …)` is `ErrUnknownDriver` — don't "fix" that by adding a case.

- **`Err` beats `Response` when both are set**, and the zero value answers every call with an empty `Response` and a nil error. Both behaviors are tested; changing either silently changes what every consumer's test means.

- **`Requests()` returns a copy.** A caller mutating the returned slice must not corrupt the fake's record — `fake_test.go` asserts this. Don't return the internal slice to avoid an allocation.

- **A canceled `ctx` records nothing and returns `ctx.Err()`** — the fake models the same cancellation precedence every real `Resolver` owes, so a consumer's cancellation test behaves identically against the fake and against `drivers/unattended`.

- **The mutex is intentional even though interactive calls are sequential by spec.** It costs nothing and keeps `go test -race` quiet for a consumer that resolves from more than one goroutine while testing something unrelated.
