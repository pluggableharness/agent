package kernel

import (
	"log/slog"
	"testing"

	logv1 "github.com/pluggableharness/agent/pkg/log/proto/v1"
)

func TestLevelToWire(t *testing.T) {
	t.Parallel()

	tests := []struct {
		level slog.Level
		want  logv1.LogLevel
	}{
		{slog.LevelDebug - 4, logv1.LogLevel_LOG_LEVEL_TRACE},
		{slog.LevelDebug, logv1.LogLevel_LOG_LEVEL_DEBUG},
		{slog.LevelInfo, logv1.LogLevel_LOG_LEVEL_INFO},
		{slog.LevelWarn, logv1.LogLevel_LOG_LEVEL_WARN},
		{slog.LevelError, logv1.LogLevel_LOG_LEVEL_ERROR},
		{slog.LevelError + 4, logv1.LogLevel_LOG_LEVEL_FATAL},
	}
	for _, tt := range tests {
		if got := levelToWire(tt.level); got != tt.want {
			t.Errorf("levelToWire(%v) = %v, want %v", tt.level, got, tt.want)
		}
	}
}

func TestWireToLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		level logv1.LogLevel
		want  slog.Level
	}{
		{logv1.LogLevel_LOG_LEVEL_TRACE, slog.LevelDebug - 4},
		{logv1.LogLevel_LOG_LEVEL_DEBUG, slog.LevelDebug},
		{logv1.LogLevel_LOG_LEVEL_INFO, slog.LevelInfo},
		{logv1.LogLevel_LOG_LEVEL_WARN, slog.LevelWarn},
		{logv1.LogLevel_LOG_LEVEL_ERROR, slog.LevelError},
		{logv1.LogLevel_LOG_LEVEL_FATAL, slog.LevelError + 4},
		{logv1.LogLevel_LOG_LEVEL_UNSPECIFIED, slog.LevelInfo},
	}
	for _, tt := range tests {
		if got := wireToLevel(tt.level); got != tt.want {
			t.Errorf("wireToLevel(%v) = %v, want %v", tt.level, got, tt.want)
		}
	}
}

func TestLevelRoundTrip(t *testing.T) {
	t.Parallel()

	for _, level := range []logv1.LogLevel{
		logv1.LogLevel_LOG_LEVEL_TRACE,
		logv1.LogLevel_LOG_LEVEL_DEBUG,
		logv1.LogLevel_LOG_LEVEL_INFO,
		logv1.LogLevel_LOG_LEVEL_WARN,
		logv1.LogLevel_LOG_LEVEL_ERROR,
		logv1.LogLevel_LOG_LEVEL_FATAL,
	} {
		if got := levelToWire(wireToLevel(level)); got != level {
			t.Errorf("levelToWire(wireToLevel(%v)) = %v, want %v", level, got, level)
		}
	}
}
