package log

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	logv1 "github.com/pluggableharness/agent/pkg/log/proto/v1"
)

func TestLevelFromProto(t *testing.T) {
	tests := []struct {
		name    string
		in      logv1.LogLevel
		want    slog.Level
		wantErr bool
	}{
		{"trace", logv1.LogLevel_LOG_LEVEL_TRACE, LevelTrace, false},
		{"debug", logv1.LogLevel_LOG_LEVEL_DEBUG, slog.LevelDebug, false},
		{"info", logv1.LogLevel_LOG_LEVEL_INFO, slog.LevelInfo, false},
		{"warn", logv1.LogLevel_LOG_LEVEL_WARN, slog.LevelWarn, false},
		{"error", logv1.LogLevel_LOG_LEVEL_ERROR, slog.LevelError, false},
		{"fatal", logv1.LogLevel_LOG_LEVEL_FATAL, LevelFatal, false},
		{"unspecified", logv1.LogLevel_LOG_LEVEL_UNSPECIFIED, 0, true},
		{"unknown value", logv1.LogLevel(99), 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := levelFromProto(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("levelFromProto(%v) = nil error, want error", tt.in)
				}
				if !errors.Is(err, ErrInvalidLevel) {
					t.Fatalf("levelFromProto(%v) error = %v, want wrapping ErrInvalidLevel", tt.in, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("levelFromProto(%v) unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("levelFromProto(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    slog.Level
		wantErr bool
	}{
		{"trace", "trace", LevelTrace, false},
		{"debug", "debug", slog.LevelDebug, false},
		{"info", "info", slog.LevelInfo, false},
		{"warn", "warn", slog.LevelWarn, false},
		{"error", "error", slog.LevelError, false},
		{"uppercase", "ERROR", slog.LevelError, false},
		{"mixed case", "WaRn", slog.LevelWarn, false},
		{"fatal is not a valid threshold", "fatal", 0, true},
		{"empty string", "", 0, true},
		{"garbage", "not-a-level", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseLevel(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseLevel(%q) = nil error, want error", tt.in)
				}
				if !errors.Is(err, ErrInvalidLevel) {
					t.Fatalf("ParseLevel(%q) error = %v, want wrapping ErrInvalidLevel", tt.in, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseLevel(%q) unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("ParseLevel(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// TestLevelOrdering confirms the full TRACE < DEBUG < INFO < WARN < ERROR <
// FATAL ordering holds, including the two custom levels this package adds.
func TestLevelOrdering(t *testing.T) {
	t.Parallel()
	levels := []slog.Level{
		LevelTrace,
		slog.LevelDebug,
		slog.LevelInfo,
		slog.LevelWarn,
		slog.LevelError,
		LevelFatal,
	}
	for i := 1; i < len(levels); i++ {
		if levels[i-1] >= levels[i] {
			t.Fatalf("levels out of order: %v (%d) >= %v (%d)",
				levels[i-1], levels[i-1], levels[i], levels[i])
		}
	}
}

// TestFatalPassesErrorThreshold is a named regression test for a subtle
// point: a threshold means "this level and anything more severe," so an
// "error" threshold must still let FATAL entries through — it's easy to
// get this backwards and assume FATAL needs an explicit, wider threshold.
func TestFatalPassesErrorThreshold(t *testing.T) {
	t.Parallel()
	threshold, err := ParseLevel("error")
	if err != nil {
		t.Fatalf("ParseLevel(\"error\") unexpected error: %v", err)
	}
	if LevelFatal < threshold {
		t.Fatalf("LevelFatal (%d) does not pass an \"error\" threshold (%d)", LevelFatal, threshold)
	}

	opts := HandlerOptions(threshold)
	handler := slog.NewTextHandler(io.Discard, opts)
	if !handler.Enabled(t.Context(), LevelFatal) {
		t.Fatal("handler with \"error\" threshold does not enable FATAL-level records")
	}
}

// TestHandlerOptionsRendersCustomLevelNames confirms LevelTrace/LevelFatal
// print as "TRACE"/"FATAL" rather than slog's default "DEBUG-4"/"ERROR+4".
func TestHandlerOptionsRendersCustomLevelNames(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		level slog.Level
		want  string
	}{
		{"trace", LevelTrace, "TRACE"},
		{"debug", slog.LevelDebug, "DEBUG"},
		{"info", slog.LevelInfo, "INFO"},
		{"warn", slog.LevelWarn, "WARN"},
		{"error", slog.LevelError, "ERROR"},
		{"fatal", LevelFatal, "FATAL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			handler := slog.NewTextHandler(&buf, HandlerOptions(LevelTrace))
			logger := slog.New(handler)
			logger.Log(t.Context(), tt.level, "msg")

			if got := buf.String(); !strings.Contains(got, "level="+tt.want) {
				t.Fatalf("output %q does not contain level=%s", got, tt.want)
			}
		})
	}
}
