package anthropic

import (
	"context"
	"log/slog"
	"net/http"
	"sync"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/pluggableharness/agent/internal/anthropic/catalog"
	"github.com/pluggableharness/agent/internal/anthropic/messages"
	"github.com/pluggableharness/agent/pkg/model"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

// Provider implements model.Provider against Anthropic's Messages API.
//
// The zero value is not usable; construct one with New. A Provider is
// safe for concurrent use: the kernel may issue GetCapabilities and
// StreamCompletion calls from several goroutines, and Configure races
// with neither because the guarded state is swapped under a lock.
type Provider struct {
	// mu guards settings/client, which Configure replaces wholesale and
	// every RPC reads. A RWMutex rather than an atomic.Pointer because
	// the pair must change together — a client built from one settings
	// value and a settings value from another configure call would be a
	// silently inconsistent provider.
	mu       sync.RWMutex
	settings settings
	client   *messages.Client

	logger *slog.Logger
	// transport is injected by tests so the unit tier can drive a fake
	// vendor without a network. Nil means http.DefaultTransport.
	transport http.RoundTripper
}

// Compile-time proof this type serves the three MUST RPCs and the SHOULD
// one. Render (MAY) is deliberately not implemented — the kernel's
// generic fallback renders this provider's payloads (plain text, tool
// calls, usage) perfectly well, and
// docs/specifications/model/protocol.md#render says as much.
var (
	_ model.Provider     = (*Provider)(nil)
	_ model.TokenCounter = (*Provider)(nil)
)

// Option configures a Provider built by New.
type Option func(*Provider)

// WithLogger sets the logger this provider and its HTTP client write to.
// Defaults to slog.Default().
func WithLogger(logger *slog.Logger) Option {
	return func(p *Provider) { p.logger = logger }
}

// WithTransport overrides the HTTP transport the vendor client dials
// through. It exists for the unit tier, which drives a fake vendor via an
// injected http.RoundTripper rather than a real network — there is no
// agent.hcl attribute for it, deliberately, because an operator has no
// reason to replace the transport and every reason not to.
func WithTransport(rt http.RoundTripper) Option {
	return func(p *Provider) { p.transport = rt }
}

// New returns a Provider that is not yet configured. The kernel calls
// Configure before any completion, so construction takes no credentials.
func New(opts ...Option) *Provider {
	p := &Provider{logger: slog.Default()}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Capabilities returns the compiled-in model roster and this provider's
// config schema, per docs/specifications/model/protocol.md#getcapabilities.
//
// No vendor call and no lock: the roster is pure data and the schema is
// rebuilt per call, so this stays cheap enough for the kernel to invoke
// before every routing decision, which the spec requires.
func (p *Provider) Capabilities(context.Context) (*model.Capabilities, error) {
	schema, err := ConfigSchema()
	if err != nil {
		return nil, err
	}
	// No slash commands and no hook points: this provider contributes
	// neither. Both fields are MAY-be-empty.
	return model.NewCapabilities(catalog.Models(), schema)
}

// Configure decodes and validates the provider's agent.hcl block and
// builds the vendor client from it.
//
// It fails immediately on a bad config rather than deferring to the first
// completion (docs/specifications/model/protocol.md#configure), and it
// never logs or echoes the API key — the DEBUG line below deliberately
// records only the endpoint and the timeout.
func (p *Provider) Configure(ctx context.Context, cfg *structpb.Struct) error {
	s, err := decodeSettings(cfg)
	if err != nil {
		return err
	}

	client := messages.NewClient(messages.ClientConfig{
		BaseURL:   s.baseURL,
		APIKey:    s.apiKey,
		Timeout:   s.requestTimeout,
		Transport: p.transport,
		Logger:    p.logger,
	})

	p.mu.Lock()
	p.settings = s
	p.client = client
	p.mu.Unlock()

	p.logger.DebugContext(ctx, "anthropic: configured",
		"base_url", s.baseURL, "request_timeout", s.requestTimeout)
	return nil
}

// StreamCompletion translates the kernel's request into Anthropic's wire
// format, streams the vendor's response, and writes every event to sink.
//
// Cancellation is normal control flow, not a failure: the returned
// context.Canceled travels up to pkg/model's statusFromErr, which turns
// it into a bare codes.Canceled rather than an application error.
func (p *Provider) StreamCompletion(ctx context.Context, req *modelv1.StreamCompletionRequest, sink *model.Sink) error {
	client, err := p.readyClient("stream completion")
	if err != nil {
		return err
	}

	spec, err := specByID(req.GetModelId())
	if err != nil {
		return err
	}

	vendorReq, err := messages.BuildRequest(req, spec)
	if err != nil {
		return err
	}

	p.logger.DebugContext(ctx, "anthropic: stream completion: starting",
		"model_id", req.GetModelId(),
		"messages", len(req.GetMessages()),
		"tools", len(req.GetTools()))

	return client.Stream(ctx, vendorReq, sink)
}

// CountTokens satisfies model.TokenCounter using Anthropic's real
// tokenizer endpoint, so the kernel marks these counts exact instead of
// falling back to its ceil(bytes/4) heuristic
// (docs/specifications/kernel-callbacks.md#the-fallback-heuristic).
//
// docs/specifications/model/protocol.md#counttokens calls the fallback a
// genuine last resort rather than a normal operating path, which is why
// this is implemented even though it is only a SHOULD: Anthropic exposes
// exact counting over a cheap endpoint, so declining to use it would be
// choosing a worse number for no reason.
func (p *Provider) CountTokens(ctx context.Context, req *modelv1.CountTokensRequest) (int64, error) {
	client, err := p.readyClient("count tokens")
	if err != nil {
		return 0, err
	}
	spec, err := specByID(req.GetModelId())
	if err != nil {
		return 0, err
	}
	return client.CountTokens(ctx, req, spec)
}

// readyClient returns the configured vendor client, or the structured
// error the kernel gets when it calls an RPC before Configure succeeded.
func (p *Provider) readyClient(rpc string) (*messages.Client, error) {
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()

	if client == nil {
		return nil, notConfiguredError(rpc)
	}
	return client, nil
}

// specByID resolves a model_id against the catalog.
//
// The kernel resolves a model against GetCapabilities before dispatching,
// so a miss here means the kernel's view and the catalog's have diverged
// — a kernel/adapter bug, which is what invalid_request classifies.
func specByID(id string) (model.Spec, error) {
	for _, spec := range catalog.Models() {
		if spec.ID == id {
			return spec, nil
		}
	}
	return model.Spec{}, unknownModelError("resolve model", id)
}
