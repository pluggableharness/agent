package interactive

import (
	"context"
	"errors"

	"google.golang.org/protobuf/types/known/structpb"

	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
)

// Request is one interactive-kind tool call awaiting a human answer.
type Request struct {
	// CallID is the originating ToolCall's call id — the correlation key
	// a frontend's interactive_response echoes back
	// (docs/specifications/frontend/frontend-protocol.md).
	CallID string

	// ToolName is the operation the model invoked, used for attribution
	// in logs, spans, and whatever prompt a frontend renders.
	ToolName string

	// Arguments is the call's parsed arguments, the kernel's canonical
	// ToolCall input representation
	// (docs/specifications/tool/data-types.md).
	Arguments *structpb.Struct

	// Prompt is the RenderTree a frontend would show a human, built from
	// the originating provider's Preview RPC if it implements one — MAY
	// be nil when the provider has no Preview.
	Prompt *renderv1.RenderTree
}

// Response is a human's (or, for the tracked-deviation driver, a
// synthetic) answer to an interactive call.
type Response struct {
	// Payload becomes the interactive call's ToolResult.payload —
	// MUST conform to the originating operation's declared output_schema
	// (validated by the caller, not this package).
	Payload *structpb.Struct
}

// ErrNoFrontend is what the tracked-deviation "unattended" driver
// returns for every call — see drivers/unattended. A caller (the future
// tool scheduler, not built here) converts this into a
// TOOL_ERROR_CATEGORY_PERMISSION_DENIED ToolError so the model observes
// the refusal in its own history and can adapt on a later turn, rather
// than the call silently vanishing.
var ErrNoFrontend = errors.New("interactive: no frontend attached to answer an interactive call")

// Resolver resolves one interactive call to a human's answer. The
// spec-correct implementation (a future drivers/frontend, NOT built
// here) emits an interactive_request ServerEvent and blocks on the
// matching ClientEvent.interactive_response, correlated by CallID. Every
// implementation MUST honor ctx cancellation promptly.
type Resolver interface {
	Resolve(ctx context.Context, req Request) (Response, error)
}
