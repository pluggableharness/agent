package log

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"

	logv1 "github.com/pluggableharness/agent/pkg/log/proto/v1"
)

// ErrInvalidLevel is returned when a wire LogLevel or a config-supplied
// level string doesn't match a known level.
var ErrInvalidLevel = errors.New("log: invalid level")

// LevelTrace and LevelFatal extend log/slog's four built-in levels
// (Debug/Info/Warn/Error) to cover pluggableharness.agent.log.v1.LogLevel's full range,
// per kernel-callbacks.md §5: the kernel MUST translate LOG_LEVEL_TRACE to
// a custom slog.Level below slog.LevelDebug and LOG_LEVEL_FATAL to one
// above slog.LevelError. The +-4 deltas mirror log/slog's own documented
// custom-level example.
const (
	LevelTrace slog.Level = slog.LevelDebug - 4
	LevelFatal slog.Level = slog.LevelError + 4
)

// levelFromProto converts a wire LogLevel into the corresponding
// slog.Level. LOG_LEVEL_UNSPECIFIED and any unrecognized value are errors,
// never silently coerced to a default level.
func levelFromProto(l logv1.LogLevel) (slog.Level, error) {
	switch l {
	case logv1.LogLevel_LOG_LEVEL_TRACE:
		return LevelTrace, nil
	case logv1.LogLevel_LOG_LEVEL_DEBUG:
		return slog.LevelDebug, nil
	case logv1.LogLevel_LOG_LEVEL_INFO:
		return slog.LevelInfo, nil
	case logv1.LogLevel_LOG_LEVEL_WARN:
		return slog.LevelWarn, nil
	case logv1.LogLevel_LOG_LEVEL_ERROR:
		return slog.LevelError, nil
	case logv1.LogLevel_LOG_LEVEL_FATAL:
		return LevelFatal, nil
	default:
		return 0, fmt.Errorf("log: level: %w: %v", ErrInvalidLevel, l)
	}
}

// ParseLevel parses one of configuration.md §9's settings.log_level
// threshold values ("trace", "debug", "info", "warn", "error" — case
// insensitive) into a slog.Level. There is no "fatal" threshold: a
// threshold means "this level and anything more severe," and FATAL is
// always more severe than ERROR, so an "error" threshold already includes
// FATAL for free via ordinary Level comparison.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "trace":
		return LevelTrace, nil
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("log: parse level: %w: %q", ErrInvalidLevel, s)
	}
}

// HandlerOptions returns slog.HandlerOptions configured with the given
// level threshold and a ReplaceAttr hook that renders LevelTrace/LevelFatal
// as "TRACE"/"FATAL" instead of slog's default "DEBUG-4"/"ERROR+4".
func HandlerOptions(level slog.Level) *slog.HandlerOptions {
	return &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && a.Key == slog.LevelKey {
				if lvl, ok := a.Value.Any().(slog.Level); ok {
					a.Value = slog.StringValue(levelName(lvl))
				}
			}
			return a
		},
	}
}

// levelName renders a slog.Level as a name spanning the full
// TRACE/DEBUG/INFO/WARN/ERROR/FATAL range, generalizing correctly for any
// level value within (or beyond) that range, not just the exact
// LevelTrace/LevelFatal constants.
func levelName(l slog.Level) string {
	switch {
	case l < slog.LevelDebug:
		return "TRACE"
	case l < slog.LevelInfo:
		return "DEBUG"
	case l < slog.LevelWarn:
		return "INFO"
	case l < slog.LevelError:
		return "WARN"
	case l < LevelFatal:
		return "ERROR"
	default:
		return "FATAL"
	}
}
