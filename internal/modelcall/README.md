# internal/modelcall

Implements steps 3-4 of the kernel's `RunTurn` algorithm — [`docs/specifications/agent-loop/turn-algorithm.md#the-runturn-algorithm`](../../docs/specifications/agent-loop/turn-algorithm.md#the-runturn-algorithm): invoke a resolved model provider's `StreamCompletion` RPC, accumulate the resulting event stream into the canonical `content.v1.Message`, and react to a classified failure exactly as [`docs/specifications/agent-loop/error-recovery.md#model-provider-errors`](../../docs/specifications/agent-loop/error-recovery.md#model-provider-errors) requires.

## What this package owns

- **The retry loop.** `rate_limited`/`overloaded` errors are retried with exponential backoff and jitter ([`internal/retrypolicy`](../retrypolicy)), honoring a provider-supplied `retry_after` verbatim when present, bounded by two independently tracked caps: a per-attempt-chain cap (`Config.Retry.MaxRetries`) and a session-wide cap (`Caller.SessionRetriesRemaining`) that persists across every `Complete` call one `Caller` ever makes.
- **Immediate, distinct failure for non-retryable categories.** `context_length_exceeded`, `auth_error`/`invalid_request`, and `content_filtered` all return a classified `*Error` after exactly one attempt — never retried, never silently falling back to another model (this package only ever calls the one `Model` it's given).
- **The StreamCompletion receive loop.** Each attempt gets a fresh [`internal/streamaccum`](../streamaccum) `Accumulator` — a retried attempt never inherits a failed prior attempt's partial state.
- **A defensive fallback classification** for a badly-behaved transport failure that never carried a structured `ModelError` inside a `StreamEvent` — mapping the raw gRPC code back to a `ModelErrorCategory` per `.claude/rules/grpc.md`'s taxonomy table, read in reverse.
- **Persisting a successful completion.** Cost computation ([`internal/cost`](../cost)) and the message-plus-cost-ledger write (`MessageSink.AppendMessage`, satisfied in production by `statebackend.Session.AppendMessage`) in one call.

## What this package does not own

- Context assembly, hook dispatch, tool execution, or anything else in `RunTurn`'s other 16 steps — those belong to a future `internal/session`.
- Building the wire `StreamCompletionRequest` — that's `internal/modelrequest`'s job; `Request.Request` arrives already built.
- Deciding *what happens next* after a classified `*Error` (e.g. triggering context reduction for `context_length_exceeded`) — that's the calling turn driver's job, one layer up.

## Layout

- `modelcall.go` — `Config`, `MessageSink`, `Request`, `Response`, `Error`, `Caller`, `New`.
- `complete.go` — `Caller.Complete` (the retry loop), the per-attempt receive loop, the transport-error fallback classifier, and the persist step.
