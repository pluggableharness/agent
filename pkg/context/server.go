package context

import (
	"context"
	"fmt"
	"log/slog"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	contextv1 "github.com/pluggableharness/agent/pkg/context/proto/v1"
	"github.com/pluggableharness/agent/pkg/kernel"
	"github.com/pluggableharness/agent/pkg/plugin"
)

// Service adapts a Provider into a real
// pluggableharness.context.v1.ContextService gRPC server, satisfying both
// plugin.Service (for plugin.Config.Services) and
// contextv1.ContextServiceServer. Build one with NewService.
type Service struct {
	contextv1.UnimplementedContextServiceServer

	provider Provider
	identity plugin.Identity
	callback *plugin.Callback
}

var (
	_ plugin.Service                 = (*Service)(nil)
	_ contextv1.ContextServiceServer = (*Service)(nil)
)

// NewService returns a *Service that dispatches every ContextService RPC
// to provider, self-reporting identity via Describe and dialing callback
// for the kernel's CountTokens primitive on every Contribute call.
func NewService(provider Provider, identity plugin.Identity, callback *plugin.Callback) *Service {
	return &Service{provider: provider, identity: identity, callback: callback}
}

// Register registers this Service's ContextService handler on s,
// satisfying plugin.Service.
func (s *Service) Register(g *grpc.Server) {
	contextv1.RegisterContextServiceServer(g, s)
}

// GetCapabilities implements contextv1.ContextServiceServer by delegating
// to s.provider.GetCapabilities.
func (s *Service) GetCapabilities(ctx context.Context, _ *contextv1.GetCapabilitiesRequest) (*contextv1.GetCapabilitiesResponse, error) {
	caps, err := s.provider.GetCapabilities(ctx)
	if err != nil {
		return nil, toStatusError(err)
	}
	return &contextv1.GetCapabilitiesResponse{Capabilities: capabilitiesToProto(caps)}, nil
}

// Configure implements contextv1.ContextServiceServer by delegating to
// s.provider.Configure.
func (s *Service) Configure(ctx context.Context, req *contextv1.ConfigureRequest) (*contextv1.ConfigureResponse, error) {
	if err := s.provider.Configure(ctx, req.GetConfig()); err != nil {
		return nil, toStatusError(err)
	}
	return &contextv1.ConfigureResponse{}, nil
}

// Contribute implements contextv1.ContextServiceServer: it converts req
// to its domain representation, dials the kernel callback client, and
// delegates the rest to contribute. Splitting the callback dial out of
// contribute is what makes contribute unit-testable with a fake
// *kernel.Client built over bufconn — plugin.Callback's own dial cannot
// be driven from outside pkg/plugin in a test (see
// pkg/plugin/callback_internal_test.go's identical documented
// limitation), so this seam is the only way to exercise the rest of
// Contribute's logic without a real hashicorp/go-plugin broker.
func (s *Service) Contribute(ctx context.Context, req *contextv1.ContextRequest) (*contextv1.ContextContribution, error) {
	domainReq, err := requestFromProto(req)
	if err != nil {
		return nil, plugin.StatusError(codes.InvalidArgument, errorDomain, ErrorCategoryInvalidRequest.reason(), err.Error(), nil)
	}

	client, err := s.callback.Client(ctx)
	if err != nil {
		return nil, plugin.StatusError(codes.Internal, errorDomain, ErrorCategoryUnknown.reason(), fmt.Sprintf("context: dial kernel callback: %v", err), nil)
	}

	return s.contribute(ctx, req, domainReq, client)
}

// contribute wires domainReq.CountTokens to client, delegates to
// s.provider.Contribute, converts the result back to wire form, and runs
// two best-effort, log-only defensive checks before returning — a
// provider budget or scope violation is the KERNEL's to enforce (it MUST
// discard the response), not this SDK's, but a loud log gives a provider
// author a fighting chance of noticing their own bug locally rather than
// only discovering it via a silently-discarded production response.
func (s *Service) contribute(ctx context.Context, req *contextv1.ContextRequest, domainReq *Request, client *kernel.Client) (*contextv1.ContextContribution, error) {
	domainReq.CountTokens = func(ctx context.Context, text string) (int64, error) {
		return countTokens(ctx, client, nil, text)
	}

	contribution, err := s.provider.Contribute(ctx, domainReq)
	if err != nil {
		return nil, toStatusError(err)
	}

	s.checkContribution(ctx, domainReq.PriorSections, contribution, req.GetTokenBudget())

	return contributionToProto(contribution), nil
}

// checkContribution logs (never fails the RPC) if contribution appears to
// violate the own-section-only rule (data-types.md#ordering--chaining) or
// its token budget (data-types.md#budget-mechanics). GetCapabilities MUST
// be cheap and side-effect-free per protocol.md, so calling it here to
// learn Compactor is within the spec's own stated cost expectation.
func (s *Service) checkContribution(ctx context.Context, prior []*Section, contribution *Contribution, tokenBudget int64) {
	if contribution == nil {
		return
	}

	caps, capErr := s.provider.GetCapabilities(ctx)
	compactor := capErr == nil && caps != nil && caps.Compactor

	if violation := CheckOwnSectionOnly(prior, contribution.Sections, s.identity.Name, compactor); violation != nil {
		slog.Default().WarnContext(ctx, "context: provider scope violation", "provider", s.identity.Name, "error", violation)
	}

	var ownTokens int64
	for _, sec := range contribution.Sections {
		if compactor || sec.Provider == s.identity.Name {
			ownTokens += sec.Tokens
		}
	}
	if tokenBudget > 0 && ownTokens > tokenBudget {
		slog.Default().WarnContext(ctx, "context: section(s) exceed allocated token budget", "provider", s.identity.Name, "tokens", ownTokens, "budget", tokenBudget)
	}
}

// Render implements contextv1.ContextServiceServer. If s.provider also
// implements Renderer, Render delegates to it; otherwise it reports
// codes.Unimplemented, which is protocol.md#render's documented signal
// for the kernel to fall back to its generic default rendering.
func (s *Service) Render(ctx context.Context, req *contextv1.RenderRequest) (*contextv1.RenderResponse, error) {
	renderer, ok := s.provider.(Renderer)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "context: provider does not implement Render")
	}
	tree, err := renderer.Render(ctx, &RenderRequest{Payload: req.GetPayload(), SchemaVersion: req.GetSchemaVersion()})
	if err != nil {
		return nil, toStatusError(err)
	}
	return &contextv1.RenderResponse{Tree: tree}, nil
}

// Describe implements contextv1.ContextServiceServer directly from
// s.identity, per configuration/lock-file.md's dev_overrides identity
// mechanism.
func (s *Service) Describe(context.Context, *contextv1.DescribeRequest) (*contextv1.DescribeResponse, error) {
	return &contextv1.DescribeResponse{Producer: s.identity.ProducerRef(commonv1.Category_CATEGORY_CONTEXT, ProtocolVersion)}, nil
}
