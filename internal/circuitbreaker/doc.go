// Package circuitbreaker implements the shared per-provider "stop trying"
// tripping logic described in two places:
//
//   - [docs/specifications/agent-loop/plan-apply-gate.md#circuit-breaker-on-repeated-denials]:
//     N consecutive `deny` decisions, or M denials within a sliding window,
//     within one session SHOULD trip the same graceful-degradation path as
//     a bound.
//   - [docs/specifications/agent-loop/error-recovery.md#tool-provider-plugin-crashes]:
//     repeated crashes from the same tool-provider plugin within a session
//     SHOULD trip the same circuit-breaker mechanism described for
//     denials, since an infinite crash-retry loop is the failure-mode
//     analog of a denial storm.
//
// # Shared signal design decision
//
// A denial and a crash for the same provider increment the SAME
// per-provider counters, rather than being tracked as two independent
// signals that each need their own threshold crossed. Both spec sections
// describe crash-handling as reusing "the same circuit-breaker mechanism"
// as denials, and error-recovery.md is explicit that a crash is "the
// failure-mode analog of a denial storm" — both are read here as one
// underlying signal ("this provider is repeatedly failing to do useful
// work"), not two. A provider that is denied twice and then crashes once
// trips a ConsecutiveThreshold of 3 exactly as if all three events had been
// denials. This is the more faithful reading of the spec text, but it is a
// judgment call the spec does not fully resolve — see this package's
// CLAUDE.md for the fuller reasoning and what would justify revisiting it.
//
// This package is pure domain logic: no I/O, no logging (it MUST NOT
// import log/slog or internal/telemetry), no knowledge of what "tripped"
// means to a caller. It only counts events per provider and reports when a
// configured threshold is crossed; routing a tripped provider through the
// limit-reached graceful-degradation path is the caller's job — the
// plan/apply gate for denials, the tool scheduler for crashes.
package circuitbreaker
