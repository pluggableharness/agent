# internal/anthropic

The Anthropic model provider — this repository's reference implementation of the [`ModelService`](../../docs/specifications/model/README.md) contract, served as a `hashicorp/go-plugin` subprocess by [`cmd/anthropic`](../../cmd/anthropic).

It is the first plugin in the tree that talks to a real vendor, and it is built the way a third party would build one: against [`pkg/model`](../../pkg/model), [`pkg/plugin`](../../pkg/plugin), [`pkg/config`](../../pkg/config), and [`pkg/content`](../../pkg/content) alone, plus the standard library. That constraint is enforced by a `depguard` rule rather than left to discipline — see [`CLAUDE.md`](CLAUDE.md).

## Layout

| Package | Owns |
|---|---|
| `internal/anthropic` | The `model.Provider` implementation, the `agent.hcl` config schema and its decoding, and secret-safe error construction |
| [`catalog/`](catalog/) | The model roster and its pricing — pure data, no I/O |
| [`messages/`](messages/) | Everything vendor-shaped: Anthropic's JSON types, canonical↔vendor translation, the SSE reader, the HTTP client, and error classification |

## Configuration

```hcl
required_providers {
  anthropic = {
    source  = "github.com/pluggableharness/agent-provider-anthropic"
    version = "~> 1.0"
  }
}

provider "anthropic" {
  api_key = env("ANTHROPIC_API_KEY")
}
```

| Attribute | Required | Default | Notes |
|---|---|---|---|
| `api_key` | yes | — | Sensitive, so `agent.hcl` may only reach it through `env(...)`. The kernel resolves the indirection; the plugin receives the literal value. |
| `base_url` | no | `https://api.anthropic.com` | For a gateway or a proxy. Plain `http://` is accepted only for a loopback host. |
| `request_timeout_seconds` | no | `600` | Ceiling on one request, not a target. A high-effort completion legitimately runs for minutes. |

`Configure` validates all three and fails immediately on a bad one, rather than deferring the failure to the first completion ([`protocol.md#configure`](../../docs/specifications/model/protocol.md#configure)).

## What it deliberately does not do

- **No vendor SDK.** Two endpoints are hand-rolled on `net/http`. Adding `anthropic-sdk-go` to `go.mod` would tax every downstream plugin author with a dependency only this plugin needs, and the SDK's built-in retry behavior directly conflicts with the kernel owning retry. See [`messages/CLAUDE.md`](messages/CLAUDE.md).
- **No retries.** Every failure is classified into a `model.Error` with `Retryable`/`RetryAfter` set; `internal/modelcall` decides what happens next.
- **No cost arithmetic.** The plugin reports token counts and declares pricing; the kernel multiplies and persists.
- **No cache-breakpoint placement.** The kernel decides where breakpoints go — it is the side that knows each context section's `Stability`. The adapter only translates the breakpoints it is handed into vendor `cache_control` markers.

## Tests

Three tiers, per [`go-testing.md`](../../.claude/rules/go-testing.md):

```sh
go test ./internal/anthropic/...                      # unit — fully offline
go test -tags=integration ./internal/anthropic/...    # launches the real binary against an httptest server
AGENT_E2E_LIVE=1 ANTHROPIC_API_KEY=... \
  go test -tags=e2e ./internal/anthropic/...          # one real, billed call
```

The e2e tier is double-gated on both `ANTHROPIC_API_KEY` **and** `AGENT_E2E_LIVE=1`, so a key present for unrelated reasons never silently spends money. It is not part of the required CI checks.
