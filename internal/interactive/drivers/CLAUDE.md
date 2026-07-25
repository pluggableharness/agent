# internal/interactive/drivers — agent notes

- **This is the only package that imports every driver.** A driver sub-package must never import a sibling driver or this package — that direction is one-way, per `go-layout.md`.
- **`ErrUnknownDriver` lives here, not in the parent `internal/interactive` package**, because "unknown driver name" is inherently a concept of the selector, not of the `Resolver` interface — the same placement `internal/telemetry/drivers` uses.
- **No default case that falls back to `unattended`.** An empty or unrecognized name is an error. See the parent `CLAUDE.md` for why: silently defaulting to "refuse every interactive call" is exactly the failure mode this whole tracked deviation exists to make impossible to stumble into.
- **`fake` is intentionally absent from the switch, and `drivers_test.go` asserts that.** Don't add it for symmetry with `internal/telemetry/drivers`; the fake needs per-test scripting this signature can't carry.
- **`logger`/`telem` are passed to every driver's constructor uniformly, even where a driver ignores one** — same convention as `internal/telemetry/drivers` passing `cfg` to `noop`. Don't special-case the signature per driver.
