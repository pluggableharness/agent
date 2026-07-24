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
// directly by Describe (via plugin.Identity.ProducerRef) rather than a
// lock-file row. callback is accepted for parity with this plugin
// process's other muxed services and exposed via Callback, so a Provider
// implementation can reach the kernel callback channel (structured
// logging, tracing, the event bus) from within its own methods —
// WidgetService's own RPCs never call back into the kernel themselves.
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

// Describe reports this plugin build's own identity, obtained directly
// from the Identity passed to NewService rather than a lock-file row
// (docs/specifications/frontend/widget-protocol.md#transport).
func (s *Service) Describe(context.Context, *widgetv1.DescribeRequest) (*widgetv1.DescribeResponse, error) {
	return &widgetv1.DescribeResponse{
		Producer: s.identity.ProducerRef(commonv1.Category_CATEGORY_WIDGET),
	}, nil
}

// GetCapabilities delegates to the Provider and converts its result to
// the wire representation. MUST be cheap and MUST NOT require a network
// call (docs/specifications/frontend/widget-protocol.md#transport) — that
// guarantee is the Provider implementation's responsibility, not this
// adapter's.
func (s *Service) GetCapabilities(ctx context.Context, _ *widgetv1.GetCapabilitiesRequest) (*widgetv1.GetCapabilitiesResponse, error) {
	caps, err := s.provider.GetCapabilities(ctx)
	if err != nil {
		return nil, toGRPCStatus(err)
	}
	return &widgetv1.GetCapabilitiesResponse{Capabilities: toProtoCapabilities(caps)}, nil
}

// Configure delegates to the Provider. A rejection surfaces as a gRPC
// status carrying an Error in its structured detail, per
// docs/specifications/frontend/widget-protocol.md#error-taxonomy — never
// an in-band field on ConfigureResponse, and never echoing a received
// secret back out.
func (s *Service) Configure(ctx context.Context, req *widgetv1.ConfigureRequest) (*widgetv1.ConfigureResponse, error) {
	if err := s.provider.Configure(ctx, req.GetConfig()); err != nil {
		return nil, toGRPCStatus(err)
	}
	return &widgetv1.ConfigureResponse{}, nil
}

// Attach serves one session's update feed by delegating to the Provider,
// which pushes updates through an UpdateSender built from stream. Per
// docs/specifications/frontend/widget-protocol.md#transport, this Attach
// is server-streaming only and session-scoped — one call per session,
// never multiplexed across sessions on one connection — a genuinely
// different shape from the frontend protocol's bidirectional,
// connection-scoped Attach despite sharing the RPC name (see doc.go).
// Cancellation (the kernel closing the stream) surfaces as
// codes.Canceled, never as an application error.
func (s *Service) Attach(req *widgetv1.AttachRequest, stream widgetv1.WidgetService_AttachServer) error {
	ctx := stream.Context()
	sender := newUpdateSender(ctx, stream)

	if err := s.provider.Attach(ctx, fromProtoAttachRequest(req), sender); err != nil {
		return toGRPCStatus(err)
	}
	return nil
}

// toGRPCStatus maps err to a gRPC status. Cancellation always maps to
// codes.Canceled, never to an application error. An *Error maps per its
// own category (errors.go); any other error is treated as
// WIDGET_ERROR_CATEGORY_UNKNOWN, mapping to codes.Internal — never
// codes.Unknown — per
// docs/specifications/frontend/widget-protocol.md#error-taxonomy.
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
