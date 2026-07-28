package widget

import (
	"context"
	"errors"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	"github.com/pluggableharness/agent/pkg/plugin"
	widgetv1 "github.com/pluggableharness/agent/pkg/widget/proto/v1"
)

// Service adapts a Provider to widgetv1.WidgetServiceServer and satisfies
// plugin.Service, so it can be passed to plugin.Config.Services. Construct
// one with NewService; the zero value is not usable.
type Service struct {
	widgetv1.UnimplementedWidgetServiceServer

	provider Provider
	identity plugin.Identity
	callback *plugin.Callback
}

var (
	_ plugin.Service               = (*Service)(nil)
	_ widgetv1.WidgetServiceServer = (*Service)(nil)
)

// NewService builds a Service adapting p to widgetv1.WidgetServiceServer.
// identity is this plugin build's own self-reported identity, used
// directly by Describe. callback is the kernel callback handle a Provider
// uses for PublishMetadata and other kernel RPCs.
func NewService(p Provider, identity plugin.Identity, callback *plugin.Callback) *Service {
	return &Service{provider: p, identity: identity, callback: callback}
}

// Callback returns the kernel callback handle passed to NewService.
func (s *Service) Callback() *plugin.Callback {
	return s.callback
}

// Register registers this Service's WidgetServiceServer handler on gs,
// satisfying plugin.Service.
func (s *Service) Register(gs *grpc.Server) {
	widgetv1.RegisterWidgetServiceServer(gs, s)
}

// Describe reports this plugin build's own identity.
func (s *Service) Describe(context.Context, *widgetv1.DescribeRequest) (*widgetv1.DescribeResponse, error) {
	return &widgetv1.DescribeResponse{
		Producer: s.identity.ProducerRef(commonv1.Category_CATEGORY_WIDGET, ProtocolVersion),
	}, nil
}

// GetCapabilities delegates to the Provider and converts its result to
// the wire representation.
func (s *Service) GetCapabilities(ctx context.Context, _ *widgetv1.GetCapabilitiesRequest) (*widgetv1.GetCapabilitiesResponse, error) {
	caps, err := s.provider.GetCapabilities(ctx)
	if err != nil {
		return nil, toGRPCStatus(err)
	}
	return &widgetv1.GetCapabilitiesResponse{Capabilities: toProtoCapabilities(caps)}, nil
}

// Configure delegates to the Provider.
func (s *Service) Configure(ctx context.Context, req *widgetv1.ConfigureRequest) (*widgetv1.ConfigureResponse, error) {
	if err := s.provider.Configure(ctx, req.GetConfig()); err != nil {
		return nil, toGRPCStatus(err)
	}
	return &widgetv1.ConfigureResponse{}, nil
}

// toGRPCStatus maps err to a gRPC status. An *Error maps per its own
// category; any other error is treated as WIDGET_ERROR_CATEGORY_UNKNOWN.
func toGRPCStatus(err error) error {
	if errors.Is(err, context.Canceled) {
		return status.Error(codes.Canceled, err.Error())
	}
	var werr *Error
	if !errors.As(err, &werr) {
		werr = Unknown(err.Error())
	}
	return werr.toStatus()
}
