// Package modeltest is a conformance suite for model-provider plugins.
//
// A provider author calls Run from their own test to check their plugin
// against the MUST/SHOULD matrix in
// docs/specifications/model/conformance.md, mechanically, rather than by
// re-reading the spec:
//
//	func TestConformance(t *testing.T) {
//		modeltest.Run(t, myprovider.New(), modeltest.WithConfig(cfg))
//	}
//
// # Two drive modes
//
// Run exercises a model.Provider in-process, over a real gRPC round trip
// on an in-memory listener. That is deliberately not a direct method call:
// most of what this suite checks lives in the pkg/model service adapter
// and the wire types — terminal-event bookkeeping, error-to-status
// mapping, the conversion layer — none of which a direct call would
// touch.
//
// RunBinary exercises an already-built plugin binary as the kernel does,
// through a real handshake and subprocess. It belongs in the integration
// tier (.claude/rules/go-testing.md), and it is the only mode that proves
// the plugin's own main() wiring is correct. It also works on a plugin
// written in any language, since it speaks only the wire protocol.
//
// # What it can and cannot check
//
// The declarative checks — capability invariants, pricing tier coverage,
// identity — are complete: they read what the provider advertises and
// need no vendor behind it.
//
// The behavioral checks can only assert what they can drive. A suite
// cannot make an arbitrary vendor emit an encrypted-reasoning block or a
// rate-limit header, so those are checked opportunistically: if the
// provider produces one during the run, its handling is asserted. A
// provider whose vendor emits such content SHOULD supply a request that
// triggers it via WithStreamRequest, so the check has something to bite
// on. What is never done is passing a check by not exercising it — a
// skipped check is reported as skipped.
//
// # Hermetic by construction
//
// This suite makes no network calls of its own. A provider under test
// that reaches a real vendor makes the run non-hermetic and billed; point
// it at a recorded transcript or a local test server instead, which is
// what WithConfig is for.
package modeltest
