package kernelcallback

import (
	"context"

	"github.com/pluggableharness/agent/internal/log"
	"github.com/pluggableharness/agent/internal/producer"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Server is the composed kernelv1.KernelCallbackServiceServer handed to
// every plugin subprocess over the callback broker (kernel-callbacks.md
// §1). One Server instance exists per launched plugin, constructed with
// that plugin's already-resolved producer identity baked in — producer
// attribution is a property of which plugin's broker connection a call
// arrived on, established at handshake, and MUST be server-derived, never
// a client-supplied request field (kernel-callbacks.md §4, §5).
type Server struct {
	kernelv1.UnimplementedKernelCallbackServiceServer

	log      *log.Server
	producer *commonv1.ProducerRef
}

// NewServer returns a Server delegating Log to logServer, with every call's
// server-derived producer identity fixed to producerRef — the plugin this
// Server instance is dedicated to.
func NewServer(logServer *log.Server, producerRef *commonv1.ProducerRef) *Server {
	return &Server{log: logServer, producer: producerRef}
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

// CountTokens is not yet implemented — tracked future work
// (kernel-callbacks.md §2/§3 defines the semantics this will eventually
// carry out).
func (s *Server) CountTokens(_ context.Context, _ *kernelv1.CountTokensRequest) (*kernelv1.CountTokensResult, error) {
	return nil, status.Error(codes.Unimplemented, "kernelcallback: CountTokens not implemented")
}

// Emit is not yet implemented — tracked future work (kernel-callbacks.md
// §4 defines the semantics, including the same server-derived-identity
// requirement this package already applies to Log).
func (s *Server) Emit(_ context.Context, _ *kernelv1.EmitRequest) (*kernelv1.EmitResult, error) {
	return nil, status.Error(codes.Unimplemented, "kernelcallback: Emit not implemented")
}
