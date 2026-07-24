package kernelcallback

import (
	"context"

	"github.com/pluggableharness/agent/internal/telemetry"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"

	"google.golang.org/protobuf/types/known/structpb"
)

// GetConfig implements the GetConfig RPC (kernel-callbacks.md's GetConfig):
// returns the calling plugin's own already-decoded agent.hcl configuration
// — s.resolvedConfig, fixed at construction like every other dependency on
// this Server. A nil s.resolvedConfig (no caller has supplied one yet —
// see Config.ResolvedConfig's doc comment) returns an empty Struct rather
// than an error, since "no config" and "empty config" are indistinguishable
// to a caller either way.
//
// This handler's own logging deliberately never includes req or the
// returned config's contents — GetConfig is a second channel a sensitive
// config value can cross (kernel-callbacks.md's GetConfig: "a plugin MUST
// NOT echo any value from config into Emit, Publish, Render, a log line,
// or an error message"), and logging the value here would defeat that
// rule before the plugin ever gets the chance to violate it itself.
func (s *Server) GetConfig(ctx context.Context, _ *kernelv1.GetConfigRequest) (*kernelv1.GetConfigResult, error) {
	ctx, span := s.telemetry.StartKernelCallbackGetConfig(ctx, s.producer)
	defer func() { telemetry.EndSpan(span, nil) }()

	s.logger.DebugContext(ctx, "kernelcallback: get_config")

	cfg := s.resolvedConfig
	if cfg == nil {
		cfg = &structpb.Struct{}
	}
	return &kernelv1.GetConfigResult{Config: cfg}, nil
}
