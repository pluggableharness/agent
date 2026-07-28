package kernelcallback

import (
	"context"
	"log/slog"

	"sync"

	"github.com/pluggableharness/agent/internal/eventbus"
	"github.com/pluggableharness/agent/internal/log"
	"github.com/pluggableharness/agent/internal/metadata"
	"github.com/pluggableharness/agent/internal/producer"
	"github.com/pluggableharness/agent/internal/sessionscope"
	"github.com/pluggableharness/agent/internal/sessionstate"
	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetryrelay"
	"github.com/pluggableharness/agent/internal/tokencount"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	logv1 "github.com/pluggableharness/agent/pkg/log/proto/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

// Config bundles every dependency NewServer needs. Every field here is
// fixed once at construction, for the same reason Producer already was
// (see the package doc comment and CLAUDE.md's "one Server per plugin
// instance" note, now extended to cover every dependency added since —
// none of these are shared, mutable, or read from an untrusted request).
type Config struct {
	// Log is the wrapped internal/log.Server the Log RPC delegates to.
	// MUST be set.
	Log *log.Server

	// Producer is this Server's fixed, server-derived producer identity —
	// the plugin this instance is dedicated to. MUST be set.
	Producer *commonv1.ProducerRef

	// Telemetry is this plugin's telemetry.Provider, used for
	// GetTelemetryConfig's reported signal state and for
	// RecordMetrics' dynamic per-name instrument recording. MUST be set.
	Telemetry *telemetry.Provider

	// TelemetryRelay uploads ExportSpans' relayed batches to the
	// configured collector (observability.md#the-relay-model). MUST be
	// set.
	TelemetryRelay *telemetryrelay.Relay

	// Bus is the event bus Publish/Subscribe operate against
	// (event-bus.md). MUST be set.
	Bus *eventbus.Bus

	// BusSubscribeQueueBound is the per-Subscribe-stream backpressure
	// bound (event-bus.md#backpressure, configuration/blocks-reference.md's
	// event_bus.subscribe_queue_bound). A value <= 0 falls back to
	// defaultBusSubscribeQueueBound.
	BusSubscribeQueueBound int

	// ResolvedConfig is this plugin's already-decoded agent.hcl
	// configuration, identical in shape to what its own ConfigureRequest.config
	// carried — GetConfig's result. MAY be nil until whatever resolves and
	// caches a plugin's config alongside its launch (agent-loop.md, not
	// yet built) exists to supply it; GetConfig returns an empty Struct
	// rather than erroring when nil, since "no config" and "empty config"
	// are indistinguishable to a caller either way.
	ResolvedConfig *structpb.Struct

	// LogLevel is the floor GetTelemetryConfig reports — the operator's
	// configured settings.log_level (configuration/blocks-reference.md),
	// translated to the wire LogLevel enum by whatever loads that config.
	// Defaults to LOG_LEVEL_INFO if left LOG_LEVEL_UNSPECIFIED, matching
	// blocks-reference.md's own documented default.
	LogLevel logv1.LogLevel

	// Logger is this Server's own kernel-native logger, for the
	// entry/error instrumentation .claude/rules/logging-telemetry.md
	// requires of every gRPC handler — distinct from Log (which relays a
	// *plugin's* log output, not this package's own). A nil Logger
	// defaults to slog.Default(), matching log.NewServer's own fallback.
	Logger *slog.Logger

	// Scopes is the process-wide session-authorization registry Emit,
	// ReadEvents, and GetSession consult before honoring a session_id a
	// plugin supplied — kernel-callbacks.md's "the kernel MUST reject a
	// call naming any session other than the one the calling plugin was
	// actually invoked for" (see sessions.go's authorizedSession). MUST
	// be set.
	Scopes *sessionscope.Registry

	// Sessions is the process-wide table of currently-live sessions Emit,
	// ReadEvents, and GetSession look an authorized session_id up in,
	// once Scopes has confirmed the calling plugin holds a grant for it.
	// MUST be set.
	Sessions *sessionstate.Table

	// Tokens resolves CountTokens calls per
	// kernel-callbacks.md#the-fallback-heuristic's algorithm: exact when
	// a model provider's own CountTokens RPC is reachable, the single
	// documented fallback heuristic otherwise. MUST be set.
	Tokens *tokencount.Counter

	// Metadata is the process-wide MetadataBlock collection behind
	// PublishMetadata/RetractMetadata/ListMetadata. MAY be nil; when nil
	// those RPCs return FailedPrecondition (or empty List). Prefer
	// wiring a shared *metadata.Store from cmd/ wiring.
	Metadata *metadata.Store

	// Deltas is the live TokenDelta fan-out for StreamDeltas. MAY be nil;
	// when nil StreamDeltas stays open until the client cancels with no
	// deltas delivered.
	Deltas *DeltaHub

	// HostSlot is the late-bound agent-loop host shared across plugins.
	// MAY be nil; frontend RPCs then return Unimplemented until Set.
	HostSlot *HostSlot
}

// defaultBusSubscribeQueueBound is the fallback per-Subscribe-stream
// backpressure bound when Config.BusSubscribeQueueBound is <= 0, matching
// configuration/blocks-reference.md's event_bus.subscribe_queue_bound
// default.
const defaultBusSubscribeQueueBound = 1024

// Server is the composed kernelv1.KernelCallbackServiceServer handed to
// every plugin subprocess over the callback broker (kernel-callbacks.md
// §1). One Server instance exists per launched plugin, constructed with
// that plugin's already-resolved dependencies baked in — producer
// attribution is a property of which plugin's broker connection a call
// arrived on, established at handshake, and MUST be server-derived, never
// a client-supplied request field (kernel-callbacks.md §4, §5) — the same
// binding-at-construction shape now covers every other dependency added
// since (telemetry, the event bus, resolved config).
type Server struct {
	kernelv1.UnimplementedKernelCallbackServiceServer

	log                    *log.Server
	producer               *commonv1.ProducerRef
	telemetry              *telemetry.Provider
	relay                  *telemetryrelay.Relay
	bus                    *eventbus.Bus
	busSubscribeQueueBound int
	resolvedConfig         *structpb.Struct
	logLevel               logv1.LogLevel
	logger                 *slog.Logger
	scopes                 *sessionscope.Registry
	sessions               *sessionstate.Table
	tokens                 *tokencount.Counter
	metadata               *metadata.Store
	deltas                 *DeltaHub
	hostSlot               *HostSlot

	// attachReleases tracks Grant release funcs from AttachSession so
	// DetachSession can drop exactly one grant per call.
	attachMu       sync.Mutex
	attachReleases map[string][]func()
}

// NewServer returns a Server bound to cfg — see Config's field comments
// for what each dependency is used for.
func NewServer(cfg Config) *Server {
	bound := cfg.BusSubscribeQueueBound
	if bound <= 0 {
		bound = defaultBusSubscribeQueueBound
	}
	logLevel := cfg.LogLevel
	if logLevel == logv1.LogLevel_LOG_LEVEL_UNSPECIFIED {
		logLevel = logv1.LogLevel_LOG_LEVEL_INFO
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		log:                    cfg.Log,
		producer:               cfg.Producer,
		telemetry:              cfg.Telemetry,
		relay:                  cfg.TelemetryRelay,
		bus:                    cfg.Bus,
		busSubscribeQueueBound: bound,
		resolvedConfig:         cfg.ResolvedConfig,
		logLevel:               logLevel,
		logger:                 logger,
		scopes:                 cfg.Scopes,
		sessions:               cfg.Sessions,
		tokens:                 cfg.Tokens,
		metadata:               cfg.Metadata,
		deltas:                 cfg.Deltas,
		hostSlot:               cfg.HostSlot,
		attachReleases:         make(map[string][]func()),
	}
}

// host returns the late-bound FrontendHost, if any.
func (s *Server) host() FrontendHost {
	if s.hostSlot == nil {
		return nil
	}
	return s.hostSlot.Get()
}

// Log implements the Log RPC by injecting this Server's fixed producer
// identity into ctx and delegating to the wrapped log.Server — internal/log
// itself never changes: it already reads producer identity via
// producer.FromContext.
func (s *Server) Log(ctx context.Context, req *kernelv1.LogRequest) (*kernelv1.LogResult, error) {
	ctx = producer.WithProducer(ctx, s.producer)
	return s.log.Log(ctx, req)
}

// RunSession is not yet implemented — tracked future work
// (agent-loop.md §7 defines the semantics this will eventually carry out).
func (s *Server) RunSession(_ context.Context, _ *kernelv1.RunSessionRequest) (*kernelv1.RunSessionResult, error) {
	return nil, status.Error(codes.Unimplemented, "kernelcallback: RunSession not implemented")
}

// CountTokens is implemented in tokens.go. Emit is implemented in emit.go.
// ReadEvents is implemented in events.go. GetSession is implemented in
// sessions.go, alongside the shared authorizedSession helper all three
// session-scoped RPCs (Emit, ReadEvents, GetSession) go through.
