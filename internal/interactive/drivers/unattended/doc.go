// Package unattended is the tracked-deviation interactive.Resolver for a
// kernel build with no frontend attach path: it refuses every
// interactive-kind call rather than fabricating an answer.
//
// docs/specifications/agent-loop/plan-apply-gate.md#data-source-and-interactive-calls
// and docs/specifications/frontend/frontend-protocol.md#fast-path-vs-full-render
// require an allowed interactive call's execution to surface as an
// `interactive_request`/`interactive_response` round trip with a human
// over an attached frontend. No frontend attach path exists in this
// codebase yet, so this stage cannot ask a human anything. That gap is
// deliberate and operator-approved; this driver exists to make it
// structurally impossible to mistake for the real behavior.
//
// # The auto-refuse / auto-allow asymmetry
//
// The sibling tracked deviation, internal/plandecision's autoallow
// driver, stands in for the same missing frontend at the plan/apply
// gate's `ask` decision — and it auto-APPROVES. This driver
// auto-REFUSES. That is not an inconsistency, and it is not an
// oversight:
//
//   - An `ask`-decision plan item has a defensible (if unsafe) default:
//     the call the model already proposed, executed as proposed. The
//     danger there is real enough that autoallow gates its own
//     construction behind an explicit "I acknowledge this is unsafe"
//     argument.
//   - An interactive call has no such default. Its entire payload is a
//     human's answer — the tool's result *is* whatever the human said.
//     There is no safe value to invent: any synthetic answer is a lie
//     told to the model in its own history, and no acknowledgment flag
//     makes fabricating one acceptable.
//
// So this driver has no acknowledgment gate, and its absence is
// deliberate. Refusing is the safe default, not a risk being taken —
// there is no "auto-allow" equivalent for a call whose whole point is
// asking a human something.
//
// A refusal surfaces as interactive.ErrNoFrontend. The caller (the
// future tool scheduler, not built here) converts it into a
// TOOL_ERROR_CATEGORY_PERMISSION_DENIED ToolError
// (pkg/tool/proto/v1.ToolErrorCategory), so the model observes the
// denial in its own history and can adapt on a later turn — the same
// "denial surfaces as tool-result text, not an out-of-band channel"
// rule the plan/apply gate's own deny path follows.
package unattended
