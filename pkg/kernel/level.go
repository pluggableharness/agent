package kernel

import (
	"log/slog"

	logv1 "github.com/pluggableharness/agent/pkg/log/proto/v1"
)

// levelToWire and wireToLevel translate between log/slog's level model and
// the wire LogLevel enum, using the exact TRACE-below-Debug/FATAL-above-Error
// boundaries specifications/kernel-callbacks.md#log documents
// ("the kernel MUST translate LOG_LEVEL_TRACE to a custom slog.Level
// below slog.LevelDebug and LOG_LEVEL_FATAL to one above slog.LevelError").
//
// These boundaries are duplicated here rather than imported from
// internal/log's LevelTrace/LevelFatal constants: pkg/ is the
// plugin-author-consumable surface and internal/ is kernel-only
// (go-layout.md's package boundary) — pkg/kernel must not depend on
// internal/log just to share two arithmetic constants. If this project's
// canonical level-boundary arithmetic ever changes, both copies need
// updating together; there is no way around that duplication without
// crossing the pkg//internal boundary this package deliberately doesn't.

// levelToWire converts an slog.Level to the wire LogLevel enum.
func levelToWire(level slog.Level) logv1.LogLevel {
	switch {
	case level < slog.LevelDebug:
		return logv1.LogLevel_LOG_LEVEL_TRACE
	case level < slog.LevelInfo:
		return logv1.LogLevel_LOG_LEVEL_DEBUG
	case level < slog.LevelWarn:
		return logv1.LogLevel_LOG_LEVEL_INFO
	case level < slog.LevelError:
		return logv1.LogLevel_LOG_LEVEL_WARN
	case level <= slog.LevelError:
		return logv1.LogLevel_LOG_LEVEL_ERROR
	default:
		return logv1.LogLevel_LOG_LEVEL_FATAL
	}
}

// wireToLevel converts the wire LogLevel enum to an slog.Level — the
// inverse of levelToWire, used to translate GetTelemetryConfig's reported
// floor into a level a caller's own slog.Handler.Enabled check can
// compare against. LOG_LEVEL_UNSPECIFIED (never valid on the wire) and any
// unrecognized value fall back to slog.LevelInfo.
func wireToLevel(level logv1.LogLevel) slog.Level {
	switch level {
	case logv1.LogLevel_LOG_LEVEL_TRACE:
		return slog.LevelDebug - 4
	case logv1.LogLevel_LOG_LEVEL_DEBUG:
		return slog.LevelDebug
	case logv1.LogLevel_LOG_LEVEL_INFO:
		return slog.LevelInfo
	case logv1.LogLevel_LOG_LEVEL_WARN:
		return slog.LevelWarn
	case logv1.LogLevel_LOG_LEVEL_ERROR:
		return slog.LevelError
	case logv1.LogLevel_LOG_LEVEL_FATAL:
		return slog.LevelError + 4
	default:
		return slog.LevelInfo
	}
}
