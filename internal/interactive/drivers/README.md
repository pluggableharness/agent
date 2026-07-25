# internal/interactive/drivers

The driver selector for [`internal/interactive`](../) — the sole place in the kernel that switches on an interactive-resolver driver name (`go-layout.md`'s driver pattern).

```go
func New(name string, logger *slog.Logger, telem *telemetry.Provider) (interactive.Resolver, error)
```

| Name | Driver | Notes |
|---|---|---|
| `unattended` | [`unattended`](unattended/) | The tracked deviation: refuses every interactive call with `interactive.ErrNoFrontend`, because no frontend attach path exists yet |
| *(anything else, including `""`)* | — | `ErrUnknownDriver` |

Two deliberate omissions:

- **No default.** An empty name is an error, not a fallback to `unattended`. Refusing every interactive call is a defensible behavior to select on purpose and an indefensible one to fall into by forgetting to name a driver.
- **`fake` is not registered.** [`drivers/fake`](fake/) is scripted per-test with a `Response` and an error that this signature cannot carry, so tests construct it directly. (`internal/telemetry/drivers` registers *its* fake because that one takes no scripting — the difference is intentional, not an inconsistency.)

The spec-correct driver — emitting an `interactive_request` `ServerEvent` and blocking on the matching `ClientEvent.interactive_response`, per [`docs/specifications/frontend/frontend-protocol.md`](../../../docs/specifications/frontend/frontend-protocol.md) — is not built. Adding it means a new sub-package here plus one line in `New`'s switch.
