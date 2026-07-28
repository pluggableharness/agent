package model

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
	"github.com/pluggableharness/agent/pkg/plugin"
)

// Service adapts a Provider into the generated modelv1.ModelServiceServer
// and satisfies pkg/plugin.Service, so it can be passed directly to
// plugin.Config.Services. Construct one with NewService.
type Service struct {
	modelv1.UnimplementedModelServiceServer

	provider Provider
	identity plugin.Identity
	callback *plugin.Callback
}

var (
	_ plugin.Service             = (*Service)(nil)
	_ modelv1.ModelServiceServer = (*Service)(nil)
)

// NewService builds a Service wrapping p. identity is this plugin build's
// own self-reported identity, used to answer Describe
// (docs/specifications/model/protocol.md#describe) — implemented here
// directly from identity.ProducerRef, no author code required. callback is
// the lazily-dialed handle to the kernel callback channel
// (pkg/plugin.NewCallback); Service does not itself call into it — it is
// threaded through so a future RPC handler needing kernel-callback access
// (e.g. a CountTokens fallback path) has it available without changing
// this constructor's signature.
//
// CountTokens and Render are optional per
// docs/specifications/model/conformance.md's summary matrix (SHOULD, MAY
// respectively). Whether p supports them is detected with a type
// assertion against TokenCounter/Renderer at call time, once per RPC —
// the standard library's optional-interface pattern (io.ReaderFrom,
// io.WriterTo) — rather than a boolean flag on Provider itself that an
// author could forget to keep in sync with their own method set, or a
// second required constructor parameter that would force every author to
// write "false"/nil for RPCs their backend doesn't support.
func NewService(p Provider, identity plugin.Identity, callback *plugin.Callback) *Service {
	return &Service{provider: p, identity: identity, callback: callback}
}

// Register registers ModelService on s, satisfying pkg/plugin.Service.
func (svc *Service) Register(s *grpc.Server) {
	modelv1.RegisterModelServiceServer(s, svc)
}

// Describe reports this plugin build's own identity, per
// docs/specifications/model/protocol.md#describe. Implemented directly
// from svc.identity — no Provider method is involved.
func (svc *Service) Describe(_ context.Context, _ *modelv1.DescribeRequest) (*modelv1.DescribeResponse, error) {
	return &modelv1.DescribeResponse{
		Producer: svc.identity.ProducerRef(commonv1.Category_CATEGORY_MODEL, ProtocolVersion),
	}, nil
}

// GetCapabilities delegates to svc.provider.Capabilities and converts the
// result to the wire type.
func (svc *Service) GetCapabilities(ctx context.Context, _ *modelv1.GetCapabilitiesRequest) (*modelv1.GetCapabilitiesResponse, error) {
	caps, err := svc.provider.Capabilities(ctx)
	if err != nil {
		return nil, statusFromErr(err)
	}
	return &modelv1.GetCapabilitiesResponse{Capabilities: capabilitiesToProto(caps)}, nil
}

// Configure delegates to svc.provider.Configure.
func (svc *Service) Configure(ctx context.Context, req *modelv1.ConfigureRequest) (*modelv1.ConfigureResponse, error) {
	if err := svc.provider.Configure(ctx, req.GetConfig()); err != nil {
		return nil, statusFromErr(err)
	}
	return &modelv1.ConfigureResponse{}, nil
}

// StreamCompletion delegates to svc.provider.StreamCompletion, handing it
// a Sink wrapping stream. Cancellation — the kernel closing the gRPC
// stream — is treated as normal control flow: a returned context.Canceled
// (or any error already carrying codes.Canceled, e.g. one Sink itself
// returned) becomes a bare codes.Canceled status, never routed through
// Error's category-based mapping.
func (svc *Service) StreamCompletion(req *modelv1.StreamCompletionRequest, stream modelv1.ModelService_StreamCompletionServer) error {
	sink := newSink(stream)
	if err := svc.provider.StreamCompletion(stream.Context(), req, sink); err != nil {
		return statusFromErr(err)
	}
	return nil
}

// CountTokens delegates to svc.provider when it implements TokenCounter,
// per docs/specifications/model/protocol.md#counttokens (SHOULD). A
// Provider that doesn't implement TokenCounter returns codes.Unimplemented
// here, and the kernel falls back to its documented heuristic.
func (svc *Service) CountTokens(ctx context.Context, req *modelv1.CountTokensRequest) (*modelv1.CountTokensResponse, error) {
	tc, ok := svc.provider.(TokenCounter)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "model: CountTokens not implemented by this provider")
	}
	count, err := tc.CountTokens(ctx, req)
	if err != nil {
		return nil, statusFromErr(err)
	}
	return &modelv1.CountTokensResponse{Count: count}, nil
}

// Render delegates to svc.provider when it implements Renderer, per
// docs/specifications/model/protocol.md#render (MAY). A Provider that
// doesn't implement Renderer returns codes.Unimplemented here, and the
// kernel falls back to its generic default rendering.
func (svc *Service) Render(ctx context.Context, req *modelv1.RenderRequest) (*modelv1.RenderResponse, error) {
	r, ok := svc.provider.(Renderer)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "model: Render not implemented by this provider")
	}
	tree, err := r.Render(ctx, req.GetPayload(), req.GetSchemaVersion())
	if err != nil {
		return nil, statusFromErr(err)
	}
	return &modelv1.RenderResponse{Tree: tree}, nil
}

// GetAccount delegates to svc.provider when it implements Accounter, per
// docs/specifications/model/protocol.md#getaccount (MAY). A Provider that
// doesn't implement Accounter returns codes.Unimplemented here, which the
// kernel treats as "this provider has no account state to report" rather
// than as a failure.
func (svc *Service) GetAccount(ctx context.Context, _ *modelv1.GetAccountRequest) (*modelv1.GetAccountResponse, error) {
	a, ok := svc.provider.(Accounter)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "model: GetAccount not implemented by this provider")
	}
	snap, err := a.GetAccount(ctx)
	if err != nil {
		return nil, statusFromErr(err)
	}
	return &modelv1.GetAccountResponse{Account: accountToProto(snap)}, nil
}
