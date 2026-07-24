package slashcommand

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	"github.com/pluggableharness/agent/pkg/plugin"
	slashcommandv1 "github.com/pluggableharness/agent/pkg/slashcommand/proto/v1"
)

// callbackCtxKey is the unexported context key ContextWithCallback and
// CallbackFromContext use, per .claude/rules/go-architecture.md's "Context
// keys are an unexported type" rule. Deliberately a distinct type from
// pkg/tool's own callbackCtxKey — context keys are per-package by
// convention, not shared across category SDKs.
type callbackCtxKey struct{}

// ContextWithCallback returns a copy of ctx carrying cb, retrievable with
// CallbackFromContext. Service attaches its own *plugin.Callback to the
// context it passes into every Provider method, so an implementation that
// needs to call back into the kernel (Emit a progress update outside the
// Invoke stream, RunSession for a spawn_subagent-shaped command, ...) can
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

// Service adapts a Provider onto the generated
// slashcommandv1.SlashCommandServiceServer, implementing plugin.Service so
// it can be passed to plugin.Config.Services.
type Service struct {
	slashcommandv1.UnimplementedSlashCommandServiceServer

	identity plugin.Identity
	callback *plugin.Callback
	impl     Provider
}

var _ plugin.Service = (*Service)(nil)
var _ slashcommandv1.SlashCommandServiceServer = (*Service)(nil)

// NewService builds a *Service adapting p onto SlashCommandServiceServer.
// identity is this plugin build's own self-reported identity, returned
// verbatim by Describe; callback is the lazily-dialed kernel-callback
// handle attached to every context Service passes into p (see
// ContextWithCallback).
func NewService(p Provider, identity plugin.Identity, callback *plugin.Callback) *Service {
	return &Service{identity: identity, callback: callback, impl: p}
}

// Register registers SlashCommandService on g, satisfying plugin.Service.
func (s *Service) Register(g *grpc.Server) {
	slashcommandv1.RegisterSlashCommandServiceServer(g, s)
}

// ctx returns base with this Service's callback attached, for handing to
// the wrapped Provider.
func (s *Service) ctx(base context.Context) context.Context {
	return ContextWithCallback(base, s.callback)
}

// GetCapabilities implements slashcommandv1.SlashCommandServiceServer.
func (s *Service) GetCapabilities(ctx context.Context, _ *slashcommandv1.GetCapabilitiesRequest) (*slashcommandv1.GetCapabilitiesResponse, error) {
	resp, err := BuildGetCapabilitiesResponse(s.ctx(ctx), s.impl)
	if err != nil {
		return nil, unknownStatusError(err)
	}
	return resp, nil
}

// Configure implements slashcommandv1.SlashCommandServiceServer.
func (s *Service) Configure(ctx context.Context, req *slashcommandv1.ConfigureRequest) (*slashcommandv1.ConfigureResponse, error) {
	cfg := structToMap(req.GetConfig())
	if err := s.impl.Configure(s.ctx(ctx), cfg); err != nil {
		return nil, configureStatusError(err)
	}
	return &slashcommandv1.ConfigureResponse{}, nil
}

// Invoke implements slashcommandv1.SlashCommandServiceServer.
// Server-streaming: it decodes the request's Call, hands it and a *Stream
// to the wrapped Provider, and treats a cancelled context as normal
// control flow rather than a failed RPC, per
// docs/specifications/slashcommand/README.md#transport--lifecycle.
func (s *Service) Invoke(req *slashcommandv1.InvokeRequest, grpcStream slashcommandv1.SlashCommandService_InvokeServer) error {
	call, err := fromProtoCall(req.GetCall())
	if err != nil {
		return invalidArgumentStatusError("slashcommand: invoke: %v", err)
	}

	st := newStream(grpcStream)
	invokeErr := s.impl.Invoke(s.ctx(grpcStream.Context()), call, st)

	switch {
	case invokeErr == nil:
		if !st.closedTerminal() {
			return fmt.Errorf("slashcommand: invoke: %s: provider returned without sending a terminal result or error event", call.Name)
		}
		return nil
	case errors.Is(invokeErr, context.Canceled), status.Code(invokeErr) == codes.Canceled:
		// Cancellation is normal control flow (README.md#transport--lifecycle),
		// never surfaced as an application error.
		return nil
	default:
		return unknownStatusError(invokeErr)
	}
}

// Render implements slashcommandv1.SlashCommandServiceServer. Returns
// codes.Unimplemented if the wrapped Provider does not additionally
// implement Renderer, per
// docs/specifications/slashcommand/protocol.md#render's "MAY be
// implemented".
func (s *Service) Render(ctx context.Context, req *slashcommandv1.RenderRequest) (*slashcommandv1.RenderResponse, error) {
	r, ok := s.impl.(Renderer)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "slashcommand: render not implemented by this provider")
	}
	tree, err := r.Render(s.ctx(ctx), req.GetPayload(), req.GetSchemaVersion())
	if err != nil {
		return nil, unknownStatusError(err)
	}
	return &slashcommandv1.RenderResponse{Tree: tree}, nil
}

// Preview implements slashcommandv1.SlashCommandServiceServer. Returns
// codes.Unimplemented if the wrapped Provider does not additionally
// implement Previewer, per
// docs/specifications/slashcommand/protocol.md#preview's "MAY be
// implemented".
func (s *Service) Preview(ctx context.Context, req *slashcommandv1.PreviewRequest) (*slashcommandv1.PreviewResponse, error) {
	p, ok := s.impl.(Previewer)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "slashcommand: preview not implemented by this provider")
	}
	call, err := fromProtoCall(req.GetCall())
	if err != nil {
		return nil, invalidArgumentStatusError("slashcommand: preview: %v", err)
	}
	tree, err := p.Preview(s.ctx(ctx), call)
	if err != nil {
		return nil, unknownStatusError(err)
	}
	return &slashcommandv1.PreviewResponse{Preview: tree}, nil
}

// Describe implements slashcommandv1.SlashCommandServiceServer directly
// from s.identity, per
// docs/specifications/slashcommand/protocol.md#describe.
func (s *Service) Describe(context.Context, *slashcommandv1.DescribeRequest) (*slashcommandv1.DescribeResponse, error) {
	return &slashcommandv1.DescribeResponse{Producer: s.identity.ProducerRef(commonv1.Category_CATEGORY_SLASHCOMMAND)}, nil
}
