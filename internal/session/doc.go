// Package session implements the kernel's session driver: the outer loop
// that surrounds internal/turn's RunTurn, per
// docs/specifications/agent-loop/turn-algorithm.md steps 16-18 and
// docs/specifications/agent-loop/README.md#scope-and-definitions' session
// definition.
//
// One Runner.Run call is one whole session — one RunSession invocation in
// the specification's vocabulary. It resolves the agent profile
// (docs/specifications/configuration/agent-profiles.md), routes the model
// chain and expands the tool scope against the providers actually loaded
// this session, creates the session in the state backend, takes one
// session-lifetime callback grant per resolved plugin, dispatches
// session-start, runs turns until a termination condition fires,
// dispatches session-end, and persists the terminal status.
//
// The three loop-termination mechanisms this package owns —
// docs/specifications/agent-loop/turn-algorithm.md#independent-bound-dimensions'
// three bounds (via internal/bounds), #doom-loop-detection (via
// internal/doomloop), and
// docs/specifications/agent-loop/plan-apply-gate.md#circuit-breaker-on-repeated-denials'
// repeated-denial breaker (surfaced back through turn.Result) — all route
// through one graceful-degradation path: exactly one more turn with tool
// specs withheld and a synthetic instruction naming what fired
// (#limit-reached-behavior), never a raw error.
//
// This build is root-sessions-only. Every session it creates has no
// parent: the bounds tracker's parent link is nil, SessionStartPayload's
// parent_session_id is unset, and the depth budget is computed once via
// agentprofile.RootRemainingDepth and never threaded to a child. The
// sub-agent seams this package leans on (bounds.Tracker's parent chain,
// sessionstate.NewLive's parentBudget, turn.Request.ParentSessionID) are
// all already in place for
// docs/specifications/agent-loop/subagents.md#context-isolation-default-fresh
// to land against without revisiting this package's shape.
package session
