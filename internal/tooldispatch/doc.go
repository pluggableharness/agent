// Package tooldispatch implements the ConcurrencySpec scheduler and
// Invoke client shared by both halves of RunTurn's tool execution, per
// [docs/specifications/agent-loop/turn-algorithm.md#turn-level-tool-call-concurrency]
// and [docs/specifications/tool/protocol.md#invoke]:
//
//   - step 9 — data_source calls, which execute freely once past their
//     policy precheck;
//   - step 12 — resource calls, which execute only after plan approval.
//
// Both groups share exactly one Scheduler and one scheduling mechanism —
// turn-algorithm.md is explicit that this is "one mechanism for both,
// not two separate rules." What differs between step 9 and step 12 is
// approval gating, decided upstream by internal/plangate (not built by
// this package, and never imported by it — see the "Structural
// boundaries" section below); by the time a Call reaches
// Scheduler.Execute, that decision has already been made.
//
// # Two structurally separate call paths
//
// Execute schedules resource/data_source calls concurrently, honoring
// each call's declared ConcurrencySpec (tool/data-types.md#concurrencyspec).
// ExecuteInteractive runs interactive-kind calls strictly sequentially,
// via internal/interactive.Resolver, never consulting ConcurrencySpec at
// all — tool/protocol.md#kind-interactive requires this unconditionally,
// regardless of what an interactive operation's (invalid, if present)
// ConcurrencySpec might say. These are two separate exported methods, not
// one method with an if/else on kind, because the turn algorithm's own
// step 8 (splitting tool_use blocks by kind, a future internal/turn's
// job) already separates interactive calls out before either method is
// ever invoked — see CLAUDE.md for the fuller reasoning.
//
// # Lock ordering
//
// Execute's concurrency scheme is deadlock-free by one invariant:
// every call acquires its provider-wide semaphore before its per-key
// semaphore (never the reverse), and releases in the opposite order.
// See Scheduler's doc comment in tooldispatch.go for the full argument.
//
// # Structural boundaries
//
// This package MUST NOT import internal/plangate. A future
// internal/turn (not built yet) is the only package that calls both
// plangate and this package and glues their outputs together — Call and
// Outcome are declared here, independently of plangate's own types, for
// exactly that reason.
package tooldispatch
