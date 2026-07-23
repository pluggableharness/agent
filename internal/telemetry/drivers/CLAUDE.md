# internal/telemetry/drivers — agent notes

- **This is the only package that imports every driver.** A driver
  sub-package (`otlpgrpc/`, etc.) must never import a sibling driver or
  this package — that direction is one-way, per `go-layout.md`.
- **`cfg telemetry.Config` is passed to every driver's `New`, even ones
  that ignore most of it** (`noop`, `fake`, `stdout` take no meaningful
  config today). This keeps the selector's signature uniform regardless of
  which driver ends up selected — don't special-case the signature per
  driver.
- **`ErrUnknownDriver` lives here, not in the parent `internal/telemetry`
  package**, because "unknown driver name" is inherently a concept of the
  selector, not of the `Backend` interface itself.
