// Package sessionscope implements the session-authorization mechanism
// backing docs/specifications/kernel-callbacks.md#the-callback-channel's
// rule for every session-scoped RPC (Emit, ReadEvents, GetSession, and
// friends): "the kernel MUST reject a call naming any session other than
// the one the calling plugin was actually invoked for." internal/
// kernelcallback's gRPC handlers are the eventual caller of this package
// — they consult Registry.Authorized before honoring a session_id a
// plugin supplied, and log/trace the rejection themselves; this package
// never does either, by design (see below).
//
// # Why a refcounted multiset, not a single "current session" slot
//
// Three constraints, all present in kernel-callbacks.md's own model of
// how a plugin subprocess is invoked, rule out anything simpler than a
// refcounted multiset of (plugin key, session id) grants:
//
//  1. A plugin subprocess is long-lived and may be invoked by more than
//     one session concurrently — parallel data_source calls within one
//     turn today, nested RunSession sub-agent sessions in a future
//     phase. A single mutable "current session" field would have the
//     second invocation's grant clobber the first's.
//  2. The same plugin may be invoked twice concurrently for the *same*
//     session — two parallel tool calls in one turn. A boolean
//     authorized/not-authorized flag per (key, session) would have
//     whichever call finishes first revoke the still-in-flight second
//     call's authorization. Grants must nest: N grants require N
//     releases before authorization actually withdraws.
//  3. Nested sessions are a named future phase, not a hypothetical.
//     The mechanism is generic over "plugin key" and "session id" alone
//     — it assumes nothing about whether a session tree exists, so
//     extending to nested RunSession children costs zero redesign here:
//     a child session is just another session id some (possibly the
//     same) key can independently hold grants for.
//
// A refcounted map[Key]map[string]int satisfies all three at once: each
// Grant call increments the count for (key, sessionID) and returns a
// release closure bound to that one increment; Authorized is simply
// "count > 0". No caller ever needs to know how many other grants exist
// for the same pair, and no caller can accidentally revoke a grant it
// didn't take.
//
// # No logging, no telemetry
//
// This package MUST NOT import log/slog or internal/telemetry, and does
// not. It is pure in-memory bookkeeping — a rejected Authorized check is
// exactly as significant as an accepted one until whatever RPC handler
// consults it decides otherwise; that handler is where the log line and
// the span belong, not here. Keeping this package silent also keeps it
// trivially testable without a logger fake and keeps its API honest:
// Sessions exists for a caller's own diagnostics, never for this package
// to make a logging decision on anyone's behalf.
package sessionscope
