// Package modelcall implements steps 3-4 of the RunTurn algorithm
// (docs/specifications/agent-loop/turn-algorithm.md#the-runturn-algorithm):
// invoking a model provider's StreamCompletion RPC, accumulating its
// stream into the canonical Message, and reacting to a classified failure
// exactly as docs/specifications/agent-loop/error-recovery.md#model-provider-errors
// requires — retry with backoff for rate_limited/overloaded, immediate
// distinct failure for context_length_exceeded/auth_error/invalid_request/
// content_filtered, and separately tracked per-attempt and session-wide
// retry caps.
//
// Caller does no context assembly (step 1), no hook dispatch (step 2/5),
// and no tool execution (steps 6+) — those are a future internal/session's
// job. This package's whole surface is Complete: one model call, retried
// according to policy, its successful result persisted via MessageSink
// (statebackend.Session.AppendMessage in production).
package modelcall
