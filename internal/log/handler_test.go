package log

import (
	"log/slog"
	"testing"

	"github.com/pluggableharness/agent/internal/producer"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	logv1 "github.com/pluggableharness/agent/pkg/log/proto/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func newTestServer(h *fakeHandler) *Server {
	return NewServer(slog.New(h))
}

func TestServer_Log_valid(t *testing.T) {
	t.Parallel()
	h := &fakeHandler{minLevel: LevelTrace}
	s := newTestServer(h)

	req := &kernelv1.LogRequest{Entries: []*logv1.LogEntry{validEntry(t)}}
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
	if h.records[0].Message != "retrying request" {
		t.Fatalf("captured message = %q, want %q", h.records[0].Message, "retrying request")
	}
}

func TestServer_Log_batch(t *testing.T) {
	t.Parallel()
	h := &fakeHandler{minLevel: LevelTrace}
	s := newTestServer(h)

	entry1 := validEntry(t)
	entry1.Message = "first"
	entry2 := validEntry(t)
	entry2.Message = "second"

	req := &kernelv1.LogRequest{Entries: []*logv1.LogEntry{entry1, entry2}}
	_, err := s.Log(t.Context(), req)
	if err != nil {
		t.Fatalf("Log: unexpected error: %v", err)
	}
	if len(h.records) != 2 {
		t.Fatalf("handler captured %d records, want 2", len(h.records))
	}
	if h.records[0].Message != "first" || h.records[1].Message != "second" {
		t.Fatalf("captured messages = [%q, %q], want [first, second] in order", h.records[0].Message, h.records[1].Message)
	}
}

func TestServer_Log_emptyBatch(t *testing.T) {
	t.Parallel()
	h := &fakeHandler{minLevel: LevelTrace}
	s := newTestServer(h)

	_, err := s.Log(t.Context(), &kernelv1.LogRequest{Entries: nil})
	assertInvalidArgument(t, err)
	if len(h.records) != 1 {
		t.Fatalf("handler captured %d records, want 1 (the rejection WARN)", len(h.records))
	}
}

func TestServer_Log_malformedEntrySkippedNotFailed(t *testing.T) {
	t.Parallel()
	h := &fakeHandler{minLevel: LevelTrace}
	s := newTestServer(h)

	good := validEntry(t)
	good.Message = "good entry"
	bad := validEntry(t)
	bad.Message = ""

	req := &kernelv1.LogRequest{Entries: []*logv1.LogEntry{bad, good}}
	_, err := s.Log(t.Context(), req)
	if err != nil {
		t.Fatalf("Log: unexpected error for a batch with one malformed entry alongside a valid one: %v", err)
	}
	if len(h.records) != 2 {
		t.Fatalf("handler captured %d records, want 2 (1 rejection WARN + 1 accepted entry)", len(h.records))
	}
	if h.records[0].Level != slog.LevelWarn {
		t.Fatalf("first record level = %v, want WARN (the malformed-entry rejection)", h.records[0].Level)
	}
	if h.records[1].Message != "good entry" {
		t.Fatalf("second record message = %q, want %q", h.records[1].Message, "good entry")
	}
}

func TestServer_Log_allEntriesMalformedFailsBatch(t *testing.T) {
	t.Parallel()
	h := &fakeHandler{minLevel: LevelTrace}
	s := newTestServer(h)

	bad1 := validEntry(t)
	bad1.Message = ""
	bad2 := validEntry(t)
	bad2.Level = 0 // LOG_LEVEL_UNSPECIFIED, never valid on the wire

	req := &kernelv1.LogRequest{Entries: []*logv1.LogEntry{bad1, bad2}}
	_, err := s.Log(t.Context(), req)
	assertInvalidArgument(t, err)
	if len(h.records) != 2 {
		t.Fatalf("handler captured %d records, want 2 (one rejection WARN per malformed entry)", len(h.records))
	}
}

func TestServer_Log_nilEntryInBatch(t *testing.T) {
	t.Parallel()
	h := &fakeHandler{minLevel: LevelTrace}
	s := newTestServer(h)

	_, err := s.Log(t.Context(), &kernelv1.LogRequest{Entries: []*logv1.LogEntry{nil}})
	assertInvalidArgument(t, err)
	if len(h.records) != 1 {
		t.Fatalf("handler captured %d records, want 1 (the rejection WARN)", len(h.records))
	}
}

func TestServer_Log_sessionID(t *testing.T) {
	t.Parallel()

	t.Run("present", func(t *testing.T) {
		t.Parallel()
		h := &fakeHandler{minLevel: LevelTrace}
		s := newTestServer(h)
		sessionID := "sess-123"
		_, err := s.Log(t.Context(), &kernelv1.LogRequest{Entries: []*logv1.LogEntry{validEntry(t)}, SessionId: &sessionID})
		if err != nil {
			t.Fatalf("Log: unexpected error: %v", err)
		}
		attrs := collectAttrs(h.records[0])
		if attrs["session_id"] != "sess-123" {
			t.Fatalf("attrs[session_id] = %v, want sess-123", attrs["session_id"])
		}
	})

	t.Run("absent", func(t *testing.T) {
		t.Parallel()
		h := &fakeHandler{minLevel: LevelTrace}
		s := newTestServer(h)
		_, err := s.Log(t.Context(), &kernelv1.LogRequest{Entries: []*logv1.LogEntry{validEntry(t)}})
		if err != nil {
			t.Fatalf("Log: unexpected error: %v", err)
		}
		attrs := collectAttrs(h.records[0])
		if _, ok := attrs["session_id"]; ok {
			t.Fatal("attrs contains \"session_id\" when SessionId was not set")
		}
	})
}

func TestServer_Log_producerAttribution(t *testing.T) {
	t.Parallel()

	t.Run("present", func(t *testing.T) {
		t.Parallel()
		h := &fakeHandler{minLevel: LevelTrace}
		s := newTestServer(h)
		p := &commonv1.ProducerRef{
			Category: commonv1.Category_CATEGORY_TOOL,
			Name:     "ripgrep",
			Version:  "1.2.3",
		}
		ctx := producer.WithProducer(t.Context(), p)
		_, err := s.Log(ctx, &kernelv1.LogRequest{Entries: []*logv1.LogEntry{validEntry(t)}})
		if err != nil {
			t.Fatalf("Log: unexpected error: %v", err)
		}
		attrs := collectAttrs(h.records[0])
		if attrs["producer_name"] != "ripgrep" {
			t.Fatalf("attrs[producer_name] = %v, want ripgrep", attrs["producer_name"])
		}
		if attrs["producer_version"] != "1.2.3" {
			t.Fatalf("attrs[producer_version] = %v, want 1.2.3", attrs["producer_version"])
		}
	})

	t.Run("absent", func(t *testing.T) {
		t.Parallel()
		h := &fakeHandler{minLevel: LevelTrace}
		s := newTestServer(h)
		_, err := s.Log(t.Context(), &kernelv1.LogRequest{Entries: []*logv1.LogEntry{validEntry(t)}})
		if err != nil {
			t.Fatalf("Log: unexpected error: %v", err)
		}
		attrs := collectAttrs(h.records[0])
		if _, ok := attrs["producer_name"]; ok {
			t.Fatal("attrs contains \"producer_name\" when no producer was set on the context")
		}
	})
}

func TestServer_Log_invalidEntryWarns(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		entry func(t *testing.T) *kernelv1.LogRequest
	}{
		{
			name: "empty batch",
			entry: func(t *testing.T) *kernelv1.LogRequest {
				t.Helper()
				return &kernelv1.LogRequest{Entries: nil}
			},
		},
		{
			name: "malformed entry",
			entry: func(t *testing.T) *kernelv1.LogRequest {
				t.Helper()
				entry := validEntry(t)
				entry.Message = ""
				return &kernelv1.LogRequest{Entries: []*logv1.LogEntry{entry}}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := &fakeHandler{minLevel: LevelTrace}
			s := newTestServer(h)

			_, err := s.Log(t.Context(), tt.entry(t))
			assertInvalidArgument(t, err)

			if len(h.records) != 1 {
				t.Fatalf("handler captured %d records, want exactly 1 WARN", len(h.records))
			}
			record := h.records[0]
			if record.Level != slog.LevelWarn {
				t.Fatalf("record level = %v, want %v", record.Level, slog.LevelWarn)
			}
			attrs := collectAttrs(record)
			if _, ok := attrs["err"]; !ok {
				t.Fatal("attrs missing \"err\"")
			}
			for _, key := range []string{"producer_category", "producer_name", "producer_version"} {
				if _, ok := attrs[key]; ok {
					t.Fatalf("attrs contains %q when no producer was set on the context", key)
				}
			}
		})
	}
}

func TestServer_Log_invalidEntryWarnsWithProducer(t *testing.T) {
	t.Parallel()
	h := &fakeHandler{minLevel: LevelTrace}
	s := newTestServer(h)
	p := &commonv1.ProducerRef{
		Category: commonv1.Category_CATEGORY_TOOL,
		Name:     "ripgrep",
		Version:  "1.2.3",
	}
	ctx := producer.WithProducer(t.Context(), p)

	_, err := s.Log(ctx, &kernelv1.LogRequest{Entries: []*logv1.LogEntry{nil}})
	assertInvalidArgument(t, err)

	if len(h.records) != 1 {
		t.Fatalf("handler captured %d records, want exactly 1 WARN", len(h.records))
	}
	attrs := collectAttrs(h.records[0])
	if attrs["producer_category"] != commonv1.Category_CATEGORY_TOOL.String() {
		t.Fatalf("attrs[producer_category] = %v, want %v", attrs["producer_category"], commonv1.Category_CATEGORY_TOOL.String())
	}
	if attrs["producer_name"] != "ripgrep" {
		t.Fatalf("attrs[producer_name] = %v, want ripgrep", attrs["producer_name"])
	}
	if attrs["producer_version"] != "1.2.3" {
		t.Fatalf("attrs[producer_version] = %v, want 1.2.3", attrs["producer_version"])
	}
}

func TestServer_Log_belowThresholdSkipsHandle(t *testing.T) {
	t.Parallel()
	h := &fakeHandler{minLevel: slog.LevelError}
	s := newTestServer(h)

	entry := validEntry(t) // LOG_LEVEL_INFO, below the ERROR threshold
	_, err := s.Log(t.Context(), &kernelv1.LogRequest{Entries: []*logv1.LogEntry{entry}})
	if err != nil {
		t.Fatalf("Log: unexpected error: %v", err)
	}
	if len(h.records) != 0 {
		t.Fatalf("handler captured %d records for a below-threshold entry, want 0", len(h.records))
	}
}

func TestNewServer_nilLoggerDefaults(t *testing.T) {
	t.Parallel()
	s := NewServer(nil)
	if s.logger == nil {
		t.Fatal("NewServer(nil).logger is nil, want slog.Default()")
	}
}

func assertInvalidArgument(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("error %v is not a gRPC status error", err)
	}
	if st.Code() != codes.InvalidArgument {
		t.Fatalf("status code = %v, want %v", st.Code(), codes.InvalidArgument)
	}
}
