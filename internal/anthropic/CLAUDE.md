# internal/anthropic — agent notes

## This package's whole point is that it has no privileges

`internal/anthropic` is the reference model provider. It exists to answer a question the rest of the repository cannot answer about itself: **is `pkg/` actually sufficient to write a real plugin against?** That answer is only worth anything if this package is held to exactly what a third party gets — `pkg/...` plus the standard library, nothing else.

So the import rule is not a style preference:

> `internal/anthropic/**` (non-test files) MUST NOT import any other `internal/...` package.

A `depguard` rule in [`.golangci.yml`](../../.golangci.yml) (`anthropic-plugin-isolation`) enforces it mechanically. If you find yourself wanting something from `internal/`, that is the signal that the thing belongs in `pkg/` — move it there and let every plugin author have it. Do not widen the depguard rule, and do not route around it with an indirection.

Test files are exempt, deliberately: the integration tier imports `internal/pluginruntime` to launch the built binary the way the kernel does. That is the kernel-launches-plugin direction, not a dependency of the plugin on the kernel.

## The telemetry rule does not apply here, and that is a real deviation

[`logging-telemetry.md`](../../.claude/rules/logging-telemetry.md) makes `internal/telemetry` mandatory for any `internal/` package that does I/O, and this package does plenty. It is not wired up, because it cannot be: `internal/telemetry` is an `internal/` package, and importing it would break the isolation property above — the property this package exists to demonstrate.

`log/slog` **is** used (stdlib, so no conflict) and carries the DEBUG/WARN/ERROR obligations the rule describes. What is missing is spans and metrics.

This is a gap in the plugin-author surface, not a shortcut taken here: a third-party plugin has no way to emit a span either. The protocol already anticipates the answer — `observability.md`'s relay model has plugins export finished spans through the kernel callback channel — but no `pkg/` package exposes it yet. When one does, wire it up here first; this package is the natural proving ground for it.

## No retries. None. Anywhere.

Classify the failure, set `Retryable` and `RetryAfter`, return. The kernel's `internal/modelcall` owns retry and backoff, and [`grpc.md`](../../.claude/rules/grpc.md) states it plainly: *"a provider does not invent its own retry policy inside the plugin; it returns the right code and lets the kernel decide."*

This is also why the vendor SDK is not a dependency — see [`messages/CLAUDE.md`](messages/CLAUDE.md).

## No cost arithmetic either

The plugin reports token counts through `Sink.Usage` and declares `Pricing` in [`catalog/`](catalog/). The kernel multiplies them and persists the result ([`protocol.md#cost-computation`](../../docs/specifications/model/protocol.md#cost-computation)). If a diff in this package ever computes a dollar figure, that is a second source of truth for a number that is supposed to have exactly one.

## Secrets: the API key arrives exactly once, through Configure

It comes in `ConfigureRequest.config`, already resolved from `env(...)` by the kernel's HCL bridge. It **never** comes from `os.Getenv` — `internal/pluginruntime`'s `buildEnv` gives a launched subprocess only `PATH`/`HOME`/`TMPDIR` plus an OTel resource stamp, so the kernel's own environment is not visible here at all. A plugin reaching for `os.Getenv("ANTHROPIC_API_KEY")` would work only by accident on a developer's machine and fail under the real launcher.

It must not reach a log line, an error message, an emitted event, or a `Render` output ([`protocol.md#configure`](../../docs/specifications/model/protocol.md#configure)). `config_test.go`'s `TestDecodeSettings_neverEchoesTheKey` runs every rejection path with a real-looking key present specifically so a future edit that interpolated the whole config into a message fails there rather than in production.

## base_url allows plain http, but only to loopback

`validateBaseURL` rejects `http://` to any remote host, because the API key rides in a request header. Loopback is exempt so the integration tier can point the plugin at an `httptest.Server`. The loopback check parses the host as an IP (or matches `localhost` exactly) — never a string prefix, because `127.0.0.1.evil.example.com` is a remote hostname and a prefix check would accept it. There is a test for exactly that.

## Where things live

| Concern | Home |
|---|---|
| `model.Provider` implementation, the RPC surface | `provider.go` |
| `agent.hcl` schema, decoding, validation | `config.go` |
| Secret-safe `*model.Error` construction | `errors.go` |
| Model roster and pricing | [`catalog/`](catalog/) |
| Everything Anthropic-shaped: JSON types, translation, SSE, HTTP, classification | [`messages/`](messages/) |

Read `messages/CLAUDE.md` before touching anything under `messages/` — two of the rules there look like obvious simplifications and are not.
