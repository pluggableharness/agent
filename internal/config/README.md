# internal/config

Top-level `agent.hcl` parsing (`specifications/configuration.md` §1-3),
`required_providers`/`provider`/`settings`/`hook` decoding (§5, §6, §9,
§8.6), and the schema-to-cty bridge (§4) that turns a provider's advertised
`ConfigSchema` into decoded JSON for its `Configure` RPC.

## What this package does

- `load.go` — `LoadFile`: parses the file, enforces the closed six-block
  schema (any other top-level block is a load-time error), dispatches each
  block to its decoder, validates policy rules before returning.
- `provider.go`, `policy.go`, `profile.go`, `settings.go`, `hook.go` — one
  decoder per block type, translating `agent.hcl` syntax into typed Go
  values (composing `internal/policy` and `internal/agentprofile`'s types
  for the blocks that belong to those packages' domains).
- `bridge.go` — `DecodeProviderConfig`, the schema-to-cty bridge itself.
  The only place a `cty.Value` exists anywhere in this package.

## Logging and telemetry

`LoadFile` performs file I/O, so per `internal/CLAUDE.md` it takes a
`context.Context` and an `*internal/telemetry.Provider`:

```go
func LoadFile(ctx context.Context, prov *telemetry.Provider, path string) (*Config, error)
```

It wraps the file read in the span opened by `prov.StartConfigLoad(ctx, path)`
(`internal/telemetry/span.go`), ended via `telemetry.EndSpan` with the call's
error, and logs a single `DEBUG` entry (`"config: loading file"`) carrying
only `path` — never decoded config content or a secret value. `decode` and
`bridge.go`'s `DecodeProviderConfig` remain deliberately uninstrumented:
both are in-memory only, and `DecodeProviderConfig` additionally evaluates
`env(...)`-sourced secrets, the same exemption `internal/hclsecret` documents
for its own functions (see that package's `CLAUDE.md`).

## How it fits in

A `provider{}` block's body is captured **raw** (`Config.ProviderBodies`,
an undecoded `hcl.Body`) — never eagerly decoded. A provider's
`ConfigSchema` only exists once its plugin subprocess is loaded and
queried, which is outside this package's job; call `DecodeProviderConfig`
once a real schema is available, from whatever loads plugins (doesn't
exist yet).
