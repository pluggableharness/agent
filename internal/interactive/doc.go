// Package interactive owns the kernel-side seam through which a
// `kind: interactive` tool call gets its answer.
//
// docs/specifications/tool/protocol.md#kind-interactive defines
// `interactive` as a genuine third `ToolKind` alongside `resource` and
// `data_source`: a call that neither mutates state nor performs a pure
// read, but blocks the current turn on a human response, whose answer
// becomes the tool's own result. `ask_user` is the canonical example.
// docs/specifications/agent-loop/plan-apply-gate.md#data-source-and-interactive-calls
// specifies how such a call reaches a human once policy has allowed it:
// execution surfaces as
// docs/specifications/frontend/frontend-protocol.md's
// `interactive_request`/`interactive_response` `ServerEvent`/`ClientEvent`
// pair, correlated by call id and executed strictly sequentially.
//
// This package holds the Resolver interface that stands where that round
// trip happens, so the (not-yet-built) tool scheduler can be written
// against the real contract before any frontend attach path exists. See
// README.md for the seam's shape and CLAUDE.md for the tracked deviation
// the drivers/unattended driver represents.
package interactive
