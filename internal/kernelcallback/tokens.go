package kernelcallback

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/pluggableharness/agent/internal/telemetry"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
)

// CountTokens implements the CountTokens RPC (kernel-callbacks.md's
// CountTokens): plugin-scoped, not session-scoped — the request carries no
// session_id — so this delegates directly to s.tokens, which resolves an
// exact count via a loaded model provider's own CountTokens RPC when
// req.ModelRef names one, falling back to the single documented heuristic
// otherwise (kernel-callbacks.md#the-fallback-heuristic).
func (s *Server) CountTokens(ctx context.Context, req *kernelv1.CountTokensRequest) (*kernelv1.CountTokensResult, error) {
	ctx, span := s.telemetry.StartKernelCallbackCountTokens(ctx, s.producer)
	var err error
	defer func() { telemetry.EndSpan(span, err) }()

	s.logger.DebugContext(ctx, "kernelcallback: count_tokens", "block_count", len(req.GetContent()))

	if len(req.GetContent()) == 0 {
		err = status.Error(codes.InvalidArgument, "kernelcallback: count_tokens: content is required and must be non-empty")
		s.logger.WarnContext(ctx, "kernelcallback: count_tokens: rejected", "err", err)
		return nil, err
	}

	count, exact := s.tokens.Count(ctx, req.GetContent(), req.GetModelRef())
	return &kernelv1.CountTokensResult{Count: count, Exact: exact}, nil
}
