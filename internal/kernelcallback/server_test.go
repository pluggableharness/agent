package kernelcallback

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/pluggableharness/agent/internal/log"
	"github.com/pluggableharness/agent/internal/producer"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	logv1 "github.com/pluggableharness/agent/pkg/log/proto/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// fakeHandler is a hand-written slog.Handler fake (per go-testing.md: fakes,
// not mocking frameworks) that captures every Record it receives instead of
// writing it anywhere, so a test can assert directly on the Record's
// attributes — mirroring internal/log's own fakeHandler.
type fakeHandler struct {
	records []slog.Record
}

func (h *fakeHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *fakeHandler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r)
	return nil
}

func (h *fakeHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *fakeHandler) WithGroup(_ string) slog.Handler      { return h }

// collectAttrs flattens a slog.Record's attributes into a map, for
// assertions in tests.
func collectAttrs(r slog.Record) map[string]any {
	attrs := make(map[string]any, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.Any()
		return true
	})
	return attrs
}

func validEntry(t *testing.T) *logv1.LogEntry {
	t.Helper()
	return &logv1.LogEntry{
		Level:   logv1.LogLevel_LOG_LEVEL_INFO,
		Logger:  "anthropic.retry",
		Message: "retrying request",
		Time:    timestamppb.New(time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)),
	}
}

func TestServer_Log_delegatesWithServerDerivedProducer(t *testing.T) {
	t.Parallel()

	h := &fakeHandler{}
	logServer := log.NewServer(slog.New(h))
	want := &commonv1.ProducerRef{
		Category: commonv1.Category_CATEGORY_TOOL,
		Name:     "ripgrep",
		Version:  "1.2.3",
	}
	s := NewServer(logServer, want)

	// Deliberately no producer.WithProducer on the incoming ctx: Server.Log
	// must derive attribution from its own baked-in producer, not from
	// anything already on ctx.
	req := &kernelv1.LogRequest{Entry: validEntry(t)}
	result, err := s.Log(t.Context(), req)
	if err != nil {
		t.Fatalf("Log: unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("Log: result is nil")
	}
	if len(h.records) != 1 {
		t.Fatalf("handler captured %d records, want 1", len(h.records))
	}

	attrs := collectAttrs(h.records[0])
	if attrs["producer_category"] != want.GetCategory().String() {
		t.Fatalf("attrs[producer_category] = %v, want %v", attrs["producer_category"], want.GetCategory().String())
	}
	if attrs["producer_name"] != want.GetName() {
		t.Fatalf("attrs[producer_name] = %v, want %v", attrs["producer_name"], want.GetName())
	}
	if attrs["producer_version"] != want.GetVersion() {
		t.Fatalf("attrs[producer_version] = %v, want %v", attrs["producer_version"], want.GetVersion())
	}
}

func TestServer_Log_ignoresContextProducer(t *testing.T) {
	t.Parallel()

	h := &fakeHandler{}
	logServer := log.NewServer(slog.New(h))
	baked := &commonv1.ProducerRef{
		Category: commonv1.Category_CATEGORY_TOOL,
		Name:     "baked-in",
		Version:  "1.0.0",
	}
	s := NewServer(logServer, baked)

	// A different producer already on the incoming ctx MUST be overridden
	// by the Server's own baked-in identity — attribution is a property of
	// this Server instance, never a client- or caller-supplied value.
	spoofed := &commonv1.ProducerRef{
		Category: commonv1.Category_CATEGORY_PROVIDER,
		Name:     "spoofed",
		Version:  "9.9.9",
	}
	ctx := producer.WithProducer(t.Context(), spoofed)

	_, err := s.Log(ctx, &kernelv1.LogRequest{Entry: validEntry(t)})
	if err != nil {
		t.Fatalf("Log: unexpected error: %v", err)
	}
	attrs := collectAttrs(h.records[0])
	if attrs["producer_name"] != "baked-in" {
		t.Fatalf("attrs[producer_name] = %v, want baked-in (server-derived, not ctx-derived)", attrs["producer_name"])
	}
}

func TestServer_unimplementedMethods(t *testing.T) {
	t.Parallel()

	s := NewServer(log.NewServer(slog.New(&fakeHandler{})), &commonv1.ProducerRef{Name: "x"})

	t.Run("RunSession", func(t *testing.T) {
		t.Parallel()
		_, err := s.RunSession(t.Context(), &kernelv1.RunSessionRequest{})
		assertUnimplemented(t, err)
	})

	t.Run("CountTokens", func(t *testing.T) {
		t.Parallel()
		_, err := s.CountTokens(t.Context(), &kernelv1.CountTokensRequest{})
		assertUnimplemented(t, err)
	})

	t.Run("Emit", func(t *testing.T) {
		t.Parallel()
		_, err := s.Emit(t.Context(), &kernelv1.EmitRequest{})
		assertUnimplemented(t, err)
	})
}

func assertUnimplemented(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("error %v is not a gRPC status error", err)
	}
	if st.Code() != codes.Unimplemented {
		t.Fatalf("status code = %v, want %v", st.Code(), codes.Unimplemented)
	}
}
