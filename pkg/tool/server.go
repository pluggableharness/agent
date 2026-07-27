package tool

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	"github.com/pluggableharness/agent/pkg/plugin"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
)

// callbackCtxKey is the unexported context key ContextWithCallback and
// CallbackFromContext use, per .claude/rules/go-architecture.md's "Context
// keys are an unexported type" rule.
type callbackCtxKey struct{}

// ContextWithCallback returns a copy of ctx carrying cb, retrievable with
// CallbackFromContext. Service attaches its own *plugin.Callback to the
// context it passes into every Provider method, so an implementation that
// needs to call back into the kernel (Emit a progress update outside the
// Invoke stream, RunSession for a spawn_subagent-shaped tool, ...) can
// reach it without every Provider method signature threading a
// *plugin.Callback through by hand.
func ContextWithCallback(ctx context.Context, cb *plugin.Callback) context.Context {
	return context.WithValue(ctx, callbackCtxKey{}, cb)
}

// CallbackFromContext retrieves the *plugin.Callback ContextWithCallback
// attached to ctx, if any.
func CallbackFromContext(ctx context.Context) (*plugin.Callback, bool) {
	cb, ok := ctx.Value(callbackCtxKey{}).(*plugin.Callback)
	return cb, ok
}

// Service adapts a Provider onto the generated toolv1.ToolServiceServer,
// implementing plugin.Service so it can be passed to plugin.Config.Services.
// It owns the name-keyed dispatch from an incoming Call to the Tool that
// serves it, so no Provider or Tool implementation switches on
// Call.ToolName itself.
type Service struct {
	toolv1.UnimplementedToolServiceServer

	identity plugin.Identity
	callback *plugin.Callback
	impl     Provider
	// tools maps Schema.Name to the Tool serving it, built once by
	// NewService.
	tools map[string]Tool
	// schema is the capability advertisement, built once by NewService
	// and returned verbatim by every GetSchema. Safe to cache because
	// Provider.Tools is static by contract — see its documentation.
	schema *toolv1.GetSchemaResponse
}

var _ plugin.Service = (*Service)(nil)
var _ toolv1.ToolServiceServer = (*Service)(nil)

// NewService builds a *Service adapting p onto ToolServiceServer. identity
// is this plugin build's own self-reported identity, returned verbatim by
// Describe; callback is the lazily-dialed kernel-callback handle attached
// to every context Service passes into p and its Tools (see
// ContextWithCallback).
//
// Every one of p's Tools is resolved here, at construction: a nil Tool, an
// unnamed or duplicate Schema.Name, or a Schema that fails validation is an
// error now rather than a malformed advertisement the kernel discovers on
// its first GetSchema, or an ambiguous dispatch it discovers mid-turn.
func NewService(p Provider, identity plugin.Identity, callback *plugin.Callback) (*Service, error) {
	tools, schema, err := resolveTools(p)
	if err != nil {
		return nil, fmt.Errorf("tool: new service: %w", err)
	}
	return &Service{identity: identity, callback: callback, impl: p, tools: tools, schema: schema}, nil
}

// tool resolves the Tool serving name, or an *Error the caller returns
// straight to the kernel. A call naming an operation this provider does
// not expose never reaches a Tool.
func (s *Service) tool(name string) (Tool, error) {
	t, ok := s.tools[name]
	if !ok {
		return nil, ToStatusError(&Error{
			Category:  ErrorCategoryInvalidArguments,
			Message:   fmt.Sprintf("tool: %q: %v", name, ErrUnknownTool),
			Retryable: false,
		})
	}
	return t, nil
}

// Register registers ToolService on s, satisfying plugin.Service.
func (s *Service) Register(g *grpc.Server) {
	toolv1.RegisterToolServiceServer(g, s)
}

// ctx returns base with this Service's callback attached, for handing to
// the wrapped Provider.
func (s *Service) ctx(base context.Context) context.Context {
	return ContextWithCallback(base, s.callback)
}

// GetSchema implements toolv1.ToolServiceServer, returning the
// advertisement NewService built. Cheaply re-queryable and free of any
// network call, per docs/specifications/tool/protocol.md#getschema, because
// there is no work left to do at request time.
func (s *Service) GetSchema(context.Context, *toolv1.GetSchemaRequest) (*toolv1.GetSchemaResponse, error) {
	return s.schema, nil
}

// Configure implements toolv1.ToolServiceServer.
func (s *Service) Configure(ctx context.Context, req *toolv1.ConfigureRequest) (*toolv1.ConfigureResponse, error) {
	cfg := structToMap(req.GetConfig())
	if err := s.impl.Configure(s.ctx(ctx), cfg); err != nil {
		var te *Error
		if errors.As(err, &te) {
			return nil, ToStatusError(te)
		}
		return nil, ToStatusError(&Error{Category: ErrorCategoryInvalidArguments, Message: err.Error(), Retryable: false})
	}
	return &toolv1.ConfigureResponse{}, nil
}

// Invoke implements toolv1.ToolServiceServer. Server-streaming: it decodes
// the request's Call, resolves the Tool its ToolName names, hands the call
// and a *Stream to that Tool, and treats a cancelled context as normal
// control flow rather than a failed RPC, per
// docs/specifications/tool/README.md#transport--lifecycle.
func (s *Service) Invoke(req *toolv1.InvokeRequest, grpcStream toolv1.ToolService_InvokeServer) error {
	call, err := fromProtoCall(req.GetCall())
	if err != nil {
		return ToStatusError(&Error{Category: ErrorCategoryInvalidArguments, Message: fmt.Sprintf("tool: invoke: %v", err), Retryable: false})
	}

	t, err := s.tool(call.ToolName)
	if err != nil {
		return err
	}

	st := newStream(grpcStream)
	invokeErr := t.Invoke(s.ctx(grpcStream.Context()), call, st)

	switch {
	case invokeErr == nil:
		if !st.closedTerminal() {
			return fmt.Errorf("tool: invoke: %s: tool returned without sending a terminal result or error event", call.ToolName)
		}
		return nil
	case errors.Is(invokeErr, context.Canceled), status.Code(invokeErr) == codes.Canceled:
		// Cancellation is normal control flow (README.md#transport--lifecycle),
		// never surfaced as an application error.
		return nil
	default:
		return ToStatusError(&Error{Category: ErrorCategoryUnknown, Message: invokeErr.Error(), Retryable: false})
	}
}

// Render implements toolv1.ToolServiceServer. Returns codes.Unimplemented
// if the wrapped Provider does not additionally implement Renderer, per
// docs/specifications/tool/protocol.md#render's "MAY be implemented".
func (s *Service) Render(ctx context.Context, req *toolv1.RenderRequest) (*toolv1.RenderResponse, error) {
	r, ok := s.impl.(Renderer)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "tool: render not implemented by this provider")
	}
	tree, err := r.Render(s.ctx(ctx), req.GetPayload(), req.GetSchemaVersion())
	if err != nil {
		return nil, ToStatusError(&Error{Category: ErrorCategoryUnknown, Message: err.Error(), Retryable: false})
	}
	return &toolv1.RenderResponse{Tree: tree}, nil
}

// Preview implements toolv1.ToolServiceServer, dispatching on the call's
// ToolName exactly as Invoke does. Returns codes.Unimplemented if the
// addressed Tool does not implement Previewer, per
// docs/specifications/tool/protocol.md#preview's "MAY be implemented" —
// which is per operation, so one Tool previewing and its sibling not is a
// supported, expected shape.
func (s *Service) Preview(ctx context.Context, req *toolv1.PreviewRequest) (*toolv1.PreviewResponse, error) {
	call, err := fromProtoCall(req.GetCall())
	if err != nil {
		return nil, ToStatusError(&Error{Category: ErrorCategoryInvalidArguments, Message: fmt.Sprintf("tool: preview: %v", err), Retryable: false})
	}

	t, err := s.tool(call.ToolName)
	if err != nil {
		return nil, err
	}
	p, ok := t.(Previewer)
	if !ok {
		return nil, status.Error(codes.Unimplemented, fmt.Sprintf("tool: preview not implemented by tool %q", call.ToolName))
	}

	tree, err := p.Preview(s.ctx(ctx), call)
	if err != nil {
		return nil, ToStatusError(&Error{Category: ErrorCategoryUnknown, Message: err.Error(), Retryable: false})
	}
	return &toolv1.PreviewResponse{Preview: tree}, nil
}

// Describe implements toolv1.ToolServiceServer directly from s.identity,
// per docs/specifications/tool/protocol.md#describe.
func (s *Service) Describe(context.Context, *toolv1.DescribeRequest) (*toolv1.DescribeResponse, error) {
	return &toolv1.DescribeResponse{Producer: s.identity.ProducerRef(commonv1.Category_CATEGORY_TOOL, ProtocolVersion)}, nil
}
