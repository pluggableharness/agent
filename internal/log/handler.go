package log

import (
	"context"
	"log/slog"

	"github.com/pluggableharness/agent/internal/producer"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Server implements the Log method of pluggableharness.kernel.v1's
// KernelCallbackServiceServer. It does not implement RunSession,
// CountTokens, or Emit — see the package doc comment.
type Server struct {
	logger *slog.Logger
}

// NewServer returns a Server that routes Log calls through logger. A nil
// logger defaults to slog.Default().
func NewServer(logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{logger: logger}
}

// Log implements the Log RPC (kernel-callbacks.md §5): it validates the
// incoming entry, converts it to a slog.Record, attaches session and
// producer attribution when present, and hands it to the configured
// logger's Handler. A malformed entry (missing a MUST field) is rejected
// with codes.InvalidArgument rather than logged with defaults filled in.
// LOG_LEVEL_FATAL is not special-cased beyond routing at that severity —
// per kernel-callbacks.md §5, it MUST NOT terminate the plugin or the
// kernel.
func (s *Server) Log(ctx context.Context, req *kernelv1.LogRequest) (*kernelv1.LogResult, error) {
	entry := req.GetEntry()
	if entry == nil {
		// Log-and-return is the sanctioned gRPC-handler exception
		// (internal/CLAUDE.md) to go-style.md's "error or log it, never
		// both": the InvalidArgument status crosses the wire to the
		// remote plugin caller, which never sees this WARN, so there's
		// no in-process double-log.
		s.warnInvalidEntry(ctx, "log: entry is required")
		return nil, status.Error(codes.InvalidArgument, "log: entry is required")
	}

	record, err := RecordFromEntry(entry)
	if err != nil {
		// Same log-and-return exception as above.
		s.warnInvalidEntry(ctx, err.Error())
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// req.SessionId is a proto3 `optional string` (*string): checked via
	// the pointer, not the zero-value getter, so an omitted session_id and
	// an explicitly-empty one stay distinguishable.
	if req.SessionId != nil {
		record.AddAttrs(slog.String("session_id", *req.SessionId))
	}

	if p, ok := producer.FromContext(ctx); ok && p != nil {
		record.AddAttrs(
			slog.String("producer_category", p.GetCategory().String()),
			slog.String("producer_name", p.GetName()),
			slog.String("producer_version", p.GetVersion()),
		)
	}

	// Handler.Enabled must be checked before Handle, per slog's documented
	// pattern for custom callers driving a Handler directly (the "Wrapping
	// output methods" guidance in the log/slog package doc) — Handle
	// itself may perform I/O we want to skip entirely when filtered out.
	if !s.logger.Handler().Enabled(ctx, record.Level) {
		return &kernelv1.LogResult{}, nil
	}
	if err := s.logger.Handler().Handle(ctx, record); err != nil {
		// Deliberately unlogged: this is the same Handler that just
		// failed, so logging through it here would be self-defeating,
		// and this package has no second logger to fall back to. The
		// status.Errorf below is the only signal this failure produces.
		return nil, status.Errorf(codes.Internal, "log: handle: %v", err)
	}

	return &kernelv1.LogResult{}, nil
}

// warnInvalidEntry logs, at WARN, that Log is rejecting an invalid
// LogRequest — a plugin-caused failure, distinct from the internal
// Handle() error path in Log, which stays unlogged (see the comment
// there). Producer attribution is attached when the calling plugin's
// identity is available on ctx, using the same field-building logic as
// the success path below.
func (s *Server) warnInvalidEntry(ctx context.Context, reason string) {
	attrs := []any{slog.String("err", reason)}
	if p, ok := producer.FromContext(ctx); ok && p != nil {
		attrs = append(attrs,
			slog.String("producer_category", p.GetCategory().String()),
			slog.String("producer_name", p.GetName()),
			slog.String("producer_version", p.GetVersion()),
		)
	}
	s.logger.WarnContext(ctx, "log: rejecting invalid entry", attrs...)
}
