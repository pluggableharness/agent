package log

import (
	"errors"
	"log/slog"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	logv1 "github.com/pluggableharness/agent/pkg/log/proto/v1"
)

func TestAttrsFromStruct(t *testing.T) {
	t.Parallel()

	t.Run("nil struct", func(t *testing.T) {
		t.Parallel()
		if got := AttrsFromStruct(nil); got != nil {
			t.Fatalf("AttrsFromStruct(nil) = %v, want nil", got)
		}
	})

	t.Run("empty struct", func(t *testing.T) {
		t.Parallel()
		s, err := structpb.NewStruct(map[string]any{})
		if err != nil {
			t.Fatalf("structpb.NewStruct: %v", err)
		}
		if got := AttrsFromStruct(s); len(got) != 0 {
			t.Fatalf("AttrsFromStruct(empty) = %v, want empty", got)
		}
	})

	t.Run("flat struct", func(t *testing.T) {
		t.Parallel()
		s, err := structpb.NewStruct(map[string]any{
			"retries": 3.0,
			"ok":      true,
			"host":    "example.com",
		})
		if err != nil {
			t.Fatalf("structpb.NewStruct: %v", err)
		}
		attrs := AttrsFromStruct(s)
		got := make(map[string]any, len(attrs))
		for _, a := range attrs {
			got[a.Key] = a.Value.Any()
		}
		want := map[string]any{"retries": 3.0, "ok": true, "host": "example.com"}
		for k, v := range want {
			if got[k] != v {
				t.Fatalf("attr %q = %v, want %v", k, got[k], v)
			}
		}
	})

	t.Run("nested struct", func(t *testing.T) {
		t.Parallel()
		s, err := structpb.NewStruct(map[string]any{
			"request": map[string]any{"method": "GET", "path": "/health"},
		})
		if err != nil {
			t.Fatalf("structpb.NewStruct: %v", err)
		}
		attrs := AttrsFromStruct(s)
		if len(attrs) != 1 {
			t.Fatalf("len(attrs) = %d, want 1", len(attrs))
		}
		nested, ok := attrs[0].Value.Any().(map[string]any)
		if !ok {
			t.Fatalf("attrs[0].Value.Any() = %T, want map[string]any", attrs[0].Value.Any())
		}
		if nested["method"] != "GET" {
			t.Fatalf("nested[method] = %v, want GET", nested["method"])
		}
	})
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

func TestRecordFromEntry(t *testing.T) {
	t.Parallel()

	t.Run("valid entry", func(t *testing.T) {
		t.Parallel()
		entry := validEntry(t)
		record, err := RecordFromEntry(entry)
		if err != nil {
			t.Fatalf("RecordFromEntry: unexpected error: %v", err)
		}
		if record.Message != "retrying request" {
			t.Fatalf("record.Message = %q, want %q", record.Message, "retrying request")
		}
		if record.Level != levelMustFromProto(t, logv1.LogLevel_LOG_LEVEL_INFO) {
			t.Fatalf("record.Level = %v, want INFO", record.Level)
		}
		if !record.Time.Equal(entry.GetTime().AsTime()) {
			t.Fatalf("record.Time = %v, want %v", record.Time, entry.GetTime().AsTime())
		}
		attrs := collectAttrs(record)
		if attrs["logger"] != "anthropic.retry" {
			t.Fatalf("attrs[logger] = %v, want anthropic.retry", attrs["logger"])
		}
	})

	t.Run("empty logger omits the logger attr", func(t *testing.T) {
		t.Parallel()
		entry := validEntry(t)
		entry.Logger = ""
		record, err := RecordFromEntry(entry)
		if err != nil {
			t.Fatalf("RecordFromEntry: unexpected error: %v", err)
		}
		attrs := collectAttrs(record)
		if _, ok := attrs["logger"]; ok {
			t.Fatalf("attrs contains \"logger\" for an entry with an empty logger name")
		}
	})

	t.Run("nil fields does not error", func(t *testing.T) {
		t.Parallel()
		entry := validEntry(t)
		entry.Fields = nil
		if _, err := RecordFromEntry(entry); err != nil {
			t.Fatalf("RecordFromEntry: unexpected error for nil Fields: %v", err)
		}
	})

	t.Run("fields become attrs", func(t *testing.T) {
		t.Parallel()
		entry := validEntry(t)
		s, err := structpb.NewStruct(map[string]any{"attempt": 2.0})
		if err != nil {
			t.Fatalf("structpb.NewStruct: %v", err)
		}
		entry.Fields = s
		record, err := RecordFromEntry(entry)
		if err != nil {
			t.Fatalf("RecordFromEntry: unexpected error: %v", err)
		}
		attrs := collectAttrs(record)
		if attrs["attempt"] != 2.0 {
			t.Fatalf("attrs[attempt] = %v, want 2.0", attrs["attempt"])
		}
	})

	missingFieldTests := []struct {
		name   string
		mutate func(*logv1.LogEntry)
	}{
		{"missing level", func(e *logv1.LogEntry) { e.Level = logv1.LogLevel_LOG_LEVEL_UNSPECIFIED }},
		{"missing message", func(e *logv1.LogEntry) { e.Message = "" }},
		{"missing time", func(e *logv1.LogEntry) { e.Time = nil }},
	}
	for _, tt := range missingFieldTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			entry := validEntry(t)
			tt.mutate(entry)
			_, err := RecordFromEntry(entry)
			if err == nil {
				t.Fatal("RecordFromEntry: want error, got nil")
			}
			if !errors.Is(err, ErrMissingField) {
				t.Fatalf("RecordFromEntry error = %v, want wrapping ErrMissingField", err)
			}
		})
	}
}

// levelMustFromProto is a test-only helper wrapping levelFromProto for
// cases where the input is known-valid and an error would indicate a test
// bug, not a case under test.
func levelMustFromProto(t *testing.T, l logv1.LogLevel) slog.Level {
	t.Helper()
	got, err := levelFromProto(l)
	if err != nil {
		t.Fatalf("levelFromProto(%v): unexpected error: %v", l, err)
	}
	return got
}
