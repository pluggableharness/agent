---
paths:
  - "**/*.go"
  - "**/*.proto"
---

# gRPC service design

This project's plugin boundary is entirely gRPC (over `hashicorp/go-plugin`
subprocess transport — see `plugin-runtime.md` for the handshake/lifecycle
half of this). The RPC *shape* per category is not a free choice: it's
dictated by the specs and MUST match exactly.

## Streaming shapes — per spec, not per preference

| Category | RPC | Shape | Source |
|---|---|---|---|
| Model | `StreamCompletion` | server-streaming + cancellation | `docs/specifications/model/README.md#transport--lifecycle` (explicitly *not* bidi) |
| Tool | `Invoke` | server-streaming | `docs/specifications/tool/protocol.md` |
| Frontend | `Attach` | **bidirectional** streaming | `docs/specifications/frontend/frontend-protocol.md` |
| Widget | `Attach` | server-streaming only | `docs/specifications/frontend/widget-protocol.md` |
| Kernel callback | `RunSession`, `CountTokens` | bidirectional (go-plugin's native plugin→kernel channel) | `docs/specifications/kernel-callbacks.md` |

Frontend `Attach` and the kernel-callback channel are the **only** two
genuinely bidirectional RPCs in the whole protocol. Do not default a new RPC
to bidi streaming because it "might need it later" — pick the narrowest shape
the spec calls for.

- **A backend that has no real streaming to do still implements the
  streaming RPC shape** and emits exactly one terminal message. Do not add a
  parallel non-streaming RPC as a shortcut — `docs/specifications/model/`
  and `docs/specifications/tool/` both make the streaming signature MUST
  regardless of whether the underlying vendor API streams.
- **Cancellation is normal control flow, not an error.** When the kernel
  closes a `StreamCompletion` or `Invoke` stream (user interrupt, timeout,
  turn abort), the provider MUST treat `context.Canceled` /
  `codes.Canceled` as expected and clean up without logging it as a failure.

## Error taxonomy → `codes`

Every RPC error crossing the plugin boundary maps to both a `grpc/codes.Code`
(for transport-level handling: retry, backoff, surfaced-to-user) and the
category's own structured error enum (for kernel/policy-level handling).
Do not return a bare `status.Error(codes.Unknown, "...")` — always the most
specific code, with the category error enum in the message's structured
detail (`google.rpc.ErrorInfo` or an equivalent typed field on the response).

Canonical mapping (extend per spec, don't invent parallel categories):

| Spec category | `grpc/codes` |
|---|---|
| `context_length_exceeded` (`docs/specifications/model/conformance.md`) | `codes.ResourceExhausted` |
| `rate_limited` (`docs/specifications/model/conformance.md`) | `codes.ResourceExhausted` (distinguished by structured detail, not code alone) |
| `overloaded` (`docs/specifications/model/conformance.md`) | `codes.Unavailable` |
| `auth_error` (`docs/specifications/model/conformance.md`) | `codes.Unauthenticated` |
| `invalid_request` (`docs/specifications/model/conformance.md`) | `codes.InvalidArgument` |
| `content_filtered` (`docs/specifications/model/conformance.md`) | `codes.FailedPrecondition` |
| `process_crashed` (`docs/specifications/tool/conformance.md`'s `ToolErrorCategory`) | `codes.Unavailable` |
| cancellation | `codes.Canceled` — never treated as an application error |
| unmapped/unexpected | `codes.Internal`, never `codes.Unknown` |

Retry/backoff on the kernel side follows the canonical defaults in
`docs/specifications/configuration/settings-and-global.md` (`base_delay_ms=500`,
`backoff_factor=2`, `max_retries=5`) — a provider does not invent its own
retry policy inside the plugin; it returns the right code and lets the
kernel decide.

## Context and deadlines

- Every RPC handler takes `ctx context.Context` as its first parameter (per
  `go-style.md`) and propagates it through to any downstream call
  (vendor HTTP client, subprocess, sqlite) — never `context.Background()`
  inside a handler.
- The kernel sets a deadline on every outbound call; a plugin honoring
  cancellation promptly (previous section) is what makes that deadline
  actually bound wall-clock time instead of leaking a goroutine.

## The strong-typing rule and its one carve-out

`proto.md` bans `Any`/untyped `bytes`/loose maps as a general rule. The
Emit→Render→Paint payload (`docs/specifications/model/protocol.md#render`,
`docs/specifications/frontend/render-tree.md`) is the one deliberate exception: it is
opaque *by design* so a producer's payload format can evolve independently
of the kernel. Do not "fix" this by giving it a concrete message type — that
would defeat the plugin-independence the specs are built around. Every other
field stays strongly typed.

Similarly, `docs/specifications/configuration/`'s two type systems — HCL/`cty` for
provider config, a restricted JSON-Schema subset for tool I/O — are
deliberately not unified into one proto type. Don't propose collapsing them.
