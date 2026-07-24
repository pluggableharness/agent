package memory

import (
	"context"
	"errors"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	memoryv1 "github.com/pluggableharness/agent/pkg/memory/proto/v1"
	"github.com/pluggableharness/agent/pkg/plugin"
)

// Service adapts a Provider into the generated memoryv1.MemoryServiceServer
// and satisfies plugin.Service, so a plugin author's main() can pass it
// straight to plugin.Config.Services. Construct one with NewService.
type Service struct {
	memoryv1.UnimplementedMemoryServiceServer

	provider Provider
	identity plugin.Identity
	callback *plugin.Callback

	// ratifier is non-nil iff provider satisfies RatificationProvider —
	// see NewService's doc comment for why this, not
	// Provider.Capabilities' own RatificationSupported field, is the
	// authoritative signal this Service acts on.
	ratifier RatificationProvider
	// renderer is non-nil iff provider satisfies Renderer.
	renderer Renderer
}

// NewService builds a Service wrapping provider. identity is this plugin
// build's own self-reported identity, used to answer Describe directly
// (docs/specifications/memory/protocol.md#describe) without involving
// provider. callback is the lazily-dialed kernel callback handle a
// Provider implementation typically closes over to call CountTokens; it is
// held here only so a future RPC that needs it can reach it through
// Service, not because Service itself calls it today.
//
// # Both-or-neither ratification, enforced structurally
//
// NewService type-asserts provider against RatificationProvider exactly
// once. Because Go interface satisfaction is all-or-nothing, this
// assertion can only succeed if provider implements BOTH ApproveRecord and
// RejectRecord — there is no Go-level way to "implement only one" and have
// it succeed. The result of that single assertion is what GetCapabilities
// reports as RatificationSupported (overriding whatever
// Provider.Capabilities itself returns for that field) and what
// ApproveRecord/RejectRecord's handlers gate on — never
// Provider.Capabilities' self-reported value. This means a Provider whose
// Capabilities method claims RatificationSupported: true but which does
// not actually implement RatificationProvider in full is corrected to
// false on the wire, rather than allowed to advertise a capability
// server.go cannot actually route to.
//
// The same reasoning applies to renderer/Renderer for the optional Render
// RPC, though Render has no "both-or-neither" pairing to enforce — it is a
// single optional method.
func NewService(provider Provider, identity plugin.Identity, callback *plugin.Callback) *Service {
	svc := &Service{provider: provider, identity: identity, callback: callback}
	if r, ok := provider.(RatificationProvider); ok {
		svc.ratifier = r
	}
	if r, ok := provider.(Renderer); ok {
		svc.renderer = r
	}
	return svc
}

// Register registers this Service's MemoryServiceServer on s, satisfying
// plugin.Service.
func (s *Service) Register(g *grpc.Server) {
	memoryv1.RegisterMemoryServiceServer(g, s)
}

var _ plugin.Service = (*Service)(nil)
var _ memoryv1.MemoryServiceServer = (*Service)(nil)

// GetCapabilities reports s.provider's capabilities, with
// RatificationSupported forced to reflect whether s.ratifier is set — see
// NewService's doc comment.
func (s *Service) GetCapabilities(ctx context.Context, _ *memoryv1.GetCapabilitiesRequest) (*memoryv1.GetCapabilitiesResponse, error) {
	caps, err := s.provider.Capabilities(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	caps.RatificationSupported = s.ratifier != nil

	return &memoryv1.GetCapabilitiesResponse{Capabilities: capabilitiesToProto(caps)}, nil
}

// Configure decodes s.provider's agent.hcl config block.
func (s *Service) Configure(ctx context.Context, req *memoryv1.ConfigureRequest) (*memoryv1.ConfigureResponse, error) {
	if err := s.provider.Configure(ctx, req.GetConfig()); err != nil {
		return nil, toStatus(err)
	}
	return &memoryv1.ConfigureResponse{}, nil
}

// Recall is the read side: returns the records s.provider judges relevant
// to req.
func (s *Service) Recall(ctx context.Context, req *memoryv1.RecallRequest) (*memoryv1.RecallResponse, error) {
	result, err := s.provider.Recall(ctx, recallRequestFromProto(req))
	if err != nil {
		return nil, toStatus(err)
	}

	records := make([]*memoryv1.MemoryRecord, 0, len(result.Records))
	for _, r := range result.Records {
		pb, err := recordToProto(r)
		if err != nil {
			return nil, toStatus(err)
		}
		records = append(records, pb)
	}
	return &memoryv1.RecallResponse{Records: records}, nil
}

// Record is the write side: creates a new record.
func (s *Service) Record(ctx context.Context, req *memoryv1.RecordRequest) (*memoryv1.RecordResponse, error) {
	domainReq, err := recordRequestFromProto(req)
	if err != nil {
		return nil, toStatus(err)
	}

	result, err := s.provider.Record(ctx, domainReq)
	if err != nil {
		return nil, toStatus(err)
	}
	if err := s.checkPendingAllowed(result.Status); err != nil {
		return nil, toStatus(err)
	}
	return &memoryv1.RecordResponse{Result: recordResultToProto(result)}, nil
}

// UpdateRecord replaces an existing record's title/content wholesale.
func (s *Service) UpdateRecord(ctx context.Context, req *memoryv1.UpdateRecordRequest) (*memoryv1.UpdateRecordResponse, error) {
	if req.GetId() == "" {
		return nil, toStatus(NotFound("id is required"))
	}
	domainReq, err := updateRecordRequestFromProto(req)
	if err != nil {
		return nil, toStatus(err)
	}

	result, err := s.provider.UpdateRecord(ctx, domainReq)
	if err != nil {
		return nil, toStatus(err)
	}
	if err := s.checkPendingAllowed(result.Status); err != nil {
		return nil, toStatus(err)
	}
	return &memoryv1.UpdateRecordResponse{Result: recordResultToProto(result)}, nil
}

// DeleteRecord removes an existing record.
func (s *Service) DeleteRecord(ctx context.Context, req *memoryv1.DeleteRecordRequest) (*memoryv1.DeleteRecordResponse, error) {
	if req.GetId() == "" {
		return nil, toStatus(NotFound("id is required"))
	}
	result, err := s.provider.DeleteRecord(ctx, req.GetId())
	if err != nil {
		return nil, toStatus(err)
	}
	return &memoryv1.DeleteRecordResponse{Result: deleteResultToProto(result)}, nil
}

// ApproveRecord transitions a PENDING record to CANONICAL. Fails with
// ErrorCategoryRatificationUnsupported unless s.ratifier is set — see
// NewService's doc comment for why that gate is structural, not
// Capabilities-driven.
func (s *Service) ApproveRecord(ctx context.Context, req *memoryv1.ApproveRecordRequest) (*memoryv1.ApproveRecordResponse, error) {
	if s.ratifier == nil {
		return nil, toStatus(RatificationUnsupported("this provider does not implement ApproveRecord/RejectRecord"))
	}
	if req.GetId() == "" {
		return nil, toStatus(NotFound("id is required"))
	}

	result, err := s.ratifier.ApproveRecord(ctx, req.GetId())
	if err != nil {
		return nil, toStatus(err)
	}
	return &memoryv1.ApproveRecordResponse{Result: recordResultToProto(result)}, nil
}

// RejectRecord discards a pending draft entirely. Fails with
// ErrorCategoryRatificationUnsupported unless s.ratifier is set.
func (s *Service) RejectRecord(ctx context.Context, req *memoryv1.RejectRecordRequest) (*memoryv1.RejectRecordResponse, error) {
	if s.ratifier == nil {
		return nil, toStatus(RatificationUnsupported("this provider does not implement ApproveRecord/RejectRecord"))
	}
	if req.GetId() == "" {
		return nil, toStatus(NotFound("id is required"))
	}

	result, err := s.ratifier.RejectRecord(ctx, req.GetId())
	if err != nil {
		return nil, toStatus(err)
	}
	return &memoryv1.RejectRecordResponse{Result: deleteResultToProto(result)}, nil
}

// Render returns s.renderer's RenderTree for req. Returns codes.Unimplemented
// if s.provider does not implement Renderer, so the kernel falls back to
// its generic default rendering.
func (s *Service) Render(ctx context.Context, req *memoryv1.RenderRequest) (*memoryv1.RenderResponse, error) {
	if s.renderer == nil {
		return nil, status.Error(codes.Unimplemented, "memory: this provider does not implement Render")
	}
	tree, err := s.renderer.Render(ctx, req.GetPayload(), req.GetSchemaVersion())
	if err != nil {
		return nil, toStatus(err)
	}
	return &memoryv1.RenderResponse{Tree: tree}, nil
}

// ListRecords is the enumeration/audit path: paginated browsing, with
// PENDING records listable without any include_pending-style gate.
func (s *Service) ListRecords(ctx context.Context, req *memoryv1.ListRecordsRequest) (*memoryv1.ListRecordsResponse, error) {
	result, err := s.provider.ListRecords(ctx, listRecordsRequestFromProto(req))
	if err != nil {
		return nil, toStatus(err)
	}
	resp, err := listRecordsResultToProto(result)
	if err != nil {
		return nil, toStatus(err)
	}
	return resp, nil
}

// GetRecord fetches exactly one record by id.
func (s *Service) GetRecord(ctx context.Context, req *memoryv1.GetRecordRequest) (*memoryv1.GetRecordResponse, error) {
	if req.GetId() == "" {
		return nil, toStatus(NotFound("id is required"))
	}
	record, err := s.provider.GetRecord(ctx, req.GetId())
	if err != nil {
		return nil, toStatus(err)
	}
	pb, err := recordToProto(record)
	if err != nil {
		return nil, toStatus(err)
	}
	return &memoryv1.GetRecordResponse{Record: pb}, nil
}

// Describe reports this plugin build's own identity, independent of
// s.provider (docs/specifications/memory/protocol.md#describe).
func (s *Service) Describe(context.Context, *memoryv1.DescribeRequest) (*memoryv1.DescribeResponse, error) {
	return &memoryv1.DescribeResponse{
		Producer: s.identity.ProducerRef(commonv1.Category_CATEGORY_MEMORY),
	}, nil
}

// checkPendingAllowed rejects a RecordStatusPending result from a provider
// that isn't ratification-capable — docs/specifications/memory/protocol.md#ratification-optional's
// "A provider with ratification_supported: false MUST NOT ever return
// status: pending", enforced defensively at the adapter boundary rather
// than trusted to every Provider implementation.
func (s *Service) checkPendingAllowed(status RecordStatus) error {
	if status == RecordStatusPending && s.ratifier == nil {
		return Unknown("provider returned status pending but does not implement ratification")
	}
	return nil
}

// toStatus converts err into the gRPC status error crossing the plugin
// boundary. A cancelled context is reported as codes.Canceled — normal
// control flow, never an application error
// (.claude/rules/grpc.md#context-and-deadlines). An *Error is converted via
// its own grpcStatus. Anything else is reported as ErrorCategoryUnknown /
// codes.Internal, never a bare codes.Unknown.
func toStatus(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return status.Error(codes.Canceled, err.Error())
	}

	var memErr *Error
	if errors.As(err, &memErr) {
		return memErr.grpcStatus()
	}
	return Unknown(err.Error()).grpcStatus()
}
