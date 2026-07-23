package log

import (
	"errors"
	"fmt"
	"log/slog"

	"google.golang.org/protobuf/types/known/structpb"

	logv1 "github.com/pluggableharness/agent/pkg/log/proto/v1"
)

// ErrMissingField is returned when a wire LogEntry is missing one of its
// MUST fields (kernel-callbacks.md §5: level, message, time).
var ErrMissingField = errors.New("log: missing required field")

// AttrsFromStruct converts a google.protobuf.Struct into slog attributes,
// mirroring slog.Attr's key/value model. Nested objects and lists convert
// via structpb's own AsMap, so nested shapes come through as plain Go
// map[string]any / []any values rather than being hand-unpacked here.
func AttrsFromStruct(s *structpb.Struct) []slog.Attr {
	if s == nil {
		return nil
	}
	m := s.AsMap()
	attrs := make([]slog.Attr, 0, len(m))
	for k, v := range m {
		attrs = append(attrs, slog.Any(k, v))
	}
	return attrs
}

// RecordFromEntry converts a wire LogEntry into a slog.Record ready to
// hand to a Handler. It validates the entry's MUST fields
// (kernel-callbacks.md §5: level, message, time) and returns
// ErrMissingField wrapped with which field was absent if any are missing —
// a malformed entry is rejected, never silently patched with a default.
func RecordFromEntry(entry *logv1.LogEntry) (slog.Record, error) {
	if entry.GetLevel() == logv1.LogLevel_LOG_LEVEL_UNSPECIFIED {
		return slog.Record{}, fmt.Errorf("log: entry: %w: level", ErrMissingField)
	}
	if entry.GetMessage() == "" {
		return slog.Record{}, fmt.Errorf("log: entry: %w: message", ErrMissingField)
	}
	if entry.GetTime() == nil {
		return slog.Record{}, fmt.Errorf("log: entry: %w: time", ErrMissingField)
	}

	level, err := levelFromProto(entry.GetLevel())
	if err != nil {
		return slog.Record{}, fmt.Errorf("log: entry: %w", err)
	}

	// pc=0: this record didn't originate from a Go call site the runtime
	// can attribute to a source line — it arrived over the wire from a
	// plugin subprocess. slog handles pc=0 by omitting source attribution.
	record := slog.NewRecord(entry.GetTime().AsTime(), level, entry.GetMessage(), 0)

	if logger := entry.GetLogger(); logger != "" {
		record.AddAttrs(slog.String("logger", logger))
	}
	record.AddAttrs(AttrsFromStruct(entry.GetFields())...)

	return record, nil
}
