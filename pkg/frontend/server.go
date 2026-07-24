package frontend

import (
	"context"

	"google.golang.org/grpc"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	frontendv1 "github.com/pluggableharness/agent/pkg/frontend/proto/v1"
	"github.com/pluggableharness/agent/pkg/plugin"
)

// Service adapts a Provider into the generated frontendv1.FrontendServiceServer
// and satisfies github.com/pluggableharness/agent/pkg/plugin's Service
// interface, for registration via plugin.Config.Services. Construct one
// with NewService.
type Service struct {
	frontendv1.UnimplementedFrontendServiceServer

	provider Provider
	identity plugin.Identity
	callback *plugin.Callback
}

var (
	_ plugin.Service                   = (*Service)(nil)
	_ frontendv1.FrontendServiceServer = (*Service)(nil)
)

// NewService returns a Service wrapping p. identity is this plugin build's
// own self-reported identity — Describe reports it directly, per
// frontend-protocol.md's "Transport" section, rather than the kernel
// inferring it from a lock-file row. callback is this plugin process's
// lazily-dialed handle to the kernel callback channel
// (github.com/pluggableharness/agent/pkg/plugin's Callback); p's own
// methods may dial it via callback.Client to make kernel-callback calls
// (Log, Emit, GetConfig, ...) as part of handling a request.
func NewService(p Provider, identity plugin.Identity, callback *plugin.Callback) *Service {
	return &Service{provider: p, identity: identity, callback: callback}
}

// Register registers the FrontendService on s, satisfying
// github.com/pluggableharness/agent/pkg/plugin's Service interface.
func (svc *Service) Register(s *grpc.Server) {
	frontendv1.RegisterFrontendServiceServer(s, svc)
}

// GetCapabilities returns this frontend's slash commands, config schema,
// supported regions, and supported hook points. Unary.
func (svc *Service) GetCapabilities(ctx context.Context, _ *frontendv1.GetCapabilitiesRequest) (*frontendv1.GetCapabilitiesResponse, error) {
	caps, err := svc.provider.Capabilities(ctx)
	if err != nil {
		return nil, statusErr(err)
	}
	return &frontendv1.GetCapabilitiesResponse{Capabilities: capabilitiesToProto(caps)}, nil
}

// Configure applies this provider's agent.hcl configuration. Unary. A
// returned error surfaces as a gRPC status carrying a Error in its
// structured detail, never as an in-band field on ConfigureResponse
// (doc.go's "Error handling is two distinct paths, not one").
func (svc *Service) Configure(ctx context.Context, req *frontendv1.ConfigureRequest) (*frontendv1.ConfigureResponse, error) {
	if err := svc.provider.Configure(ctx, req.GetConfig()); err != nil {
		return nil, statusErr(err)
	}
	return &frontendv1.ConfigureResponse{}, nil
}

// Describe reports this plugin build's own identity, obtained directly
// from svc.identity rather than a lock-file row — see NewService.
func (svc *Service) Describe(context.Context, *frontendv1.DescribeRequest) (*frontendv1.DescribeResponse, error) {
	return &frontendv1.DescribeResponse{
		Producer: svc.identity.ProducerRef(commonv1.Category_CATEGORY_FRONTEND),
	}, nil
}
