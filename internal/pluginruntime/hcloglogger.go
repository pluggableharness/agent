package pluginruntime

import (
	"context"
	"io"
	"log"
	"log/slog"
	"strings"
	"sync/atomic"

	"github.com/hashicorp/go-hclog"

	internallog "github.com/pluggableharness/agent/internal/log"
)

// hclogAdapter adapts a *slog.Logger to hclog.Logger so that
// hashicorp/go-plugin's own subprocess-management diagnostics — handshake
// negotiation, broker bring-up, process-exit bookkeeping — flow through
// this codebase's single log/slog sink instead of a second, uncorrelated
// log stream. This is deliberately NOT the channel plugin *application*
// logs travel through: those cross the KernelCallbackService.Log RPC and
// land in internal/kernelcallback.Server / internal/log.Server instead
// (see CLAUDE.md's "hclog shim is for go-plugin's own diagnostics" note —
// don't repurpose this adapter to carry application logs).
type hclogAdapter struct {
	logger      *slog.Logger
	name        string
	impliedArgs []any

	// level backs SetLevel/GetLevel. hclog's own doc for SetLevel says an
	// implementation that "cannot update the level on the fly" should
	// no-op; storing it here satisfies GetLevel's round-trip contract
	// without pretending to change the underlying slog.Handler's actual
	// filtering threshold, which is fixed at Handler-construction time.
	level atomic.Int32
}

var _ hclog.Logger = (*hclogAdapter)(nil)

// newHCLogger returns an hclog.Logger backed by logger, identified by
// name in hclog's Name()/Named() sense.
func newHCLogger(logger *slog.Logger, name string) *hclogAdapter {
	h := &hclogAdapter{logger: logger, name: name}
	h.level.Store(int32(hclog.Info))
	return h
}

// slogLevelFor maps an hclog.Level to the nearest slog.Level, per this
// package's CLAUDE.md: hclog's five real severities (Trace/Debug/Info/
// Warn/Error) map onto internal/log's TRACE..ERROR range one-to-one;
// NoLevel and Off (hclog has no Fatal) fall back to slog.LevelInfo, since
// neither carries a meaningful severity of its own to translate.
func slogLevelFor(l hclog.Level) slog.Level {
	switch l {
	case hclog.Trace:
		return internallog.LevelTrace
	case hclog.Debug:
		return slog.LevelDebug
	case hclog.Warn:
		return slog.LevelWarn
	case hclog.Error:
		return slog.LevelError
	default: // hclog.Info, hclog.NoLevel, hclog.Off
		return slog.LevelInfo
	}
}

// Log emits msg/args at level, translated per slogLevelFor. context.Background
// is used deliberately: hclog.Logger's interface carries no context
// parameter, so this is an ingress boundary (go-style.md's exception for
// "at ingress" context construction), not a mid-call fabrication.
func (h *hclogAdapter) Log(level hclog.Level, msg string, args ...any) {
	h.logger.Log(context.Background(), slogLevelFor(level), msg, args...)
}

// Trace emits msg/args at hclog's TRACE level.
func (h *hclogAdapter) Trace(msg string, args ...any) { h.Log(hclog.Trace, msg, args...) }

// Debug emits msg/args at hclog's DEBUG level.
func (h *hclogAdapter) Debug(msg string, args ...any) { h.Log(hclog.Debug, msg, args...) }

// Info emits msg/args at hclog's INFO level.
func (h *hclogAdapter) Info(msg string, args ...any) { h.Log(hclog.Info, msg, args...) }

// Warn emits msg/args at hclog's WARN level.
func (h *hclogAdapter) Warn(msg string, args ...any) { h.Log(hclog.Warn, msg, args...) }

// Error emits msg/args at hclog's ERROR level.
func (h *hclogAdapter) Error(msg string, args ...any) { h.Log(hclog.Error, msg, args...) }

// IsTrace reports whether a TRACE-level Log call would actually be
// handled, per the underlying slog.Handler's own Enabled check.
func (h *hclogAdapter) IsTrace() bool {
	return h.logger.Enabled(context.Background(), internallog.LevelTrace)
}

// IsDebug reports whether a DEBUG-level Log call would actually be handled.
func (h *hclogAdapter) IsDebug() bool {
	return h.logger.Enabled(context.Background(), slog.LevelDebug)
}

// IsInfo reports whether an INFO-level Log call would actually be handled.
func (h *hclogAdapter) IsInfo() bool {
	return h.logger.Enabled(context.Background(), slog.LevelInfo)
}

// IsWarn reports whether a WARN-level Log call would actually be handled.
func (h *hclogAdapter) IsWarn() bool {
	return h.logger.Enabled(context.Background(), slog.LevelWarn)
}

// IsError reports whether an ERROR-level Log call would actually be
// handled.
func (h *hclogAdapter) IsError() bool {
	return h.logger.Enabled(context.Background(), slog.LevelError)
}

// ImpliedArgs returns the key/value pairs accumulated via With.
func (h *hclogAdapter) ImpliedArgs() []any {
	return h.impliedArgs
}

// With returns a sublogger that always includes args, per hclog's
// sublogger contract.
func (h *hclogAdapter) With(args ...any) hclog.Logger {
	n := &hclogAdapter{
		logger:      h.logger.With(args...),
		name:        h.name,
		impliedArgs: append(append([]any{}, h.impliedArgs...), args...),
	}
	n.level.Store(h.level.Load())
	return n
}

// Name returns this logger's current name.
func (h *hclogAdapter) Name() string {
	return h.name
}

// Named returns a sublogger whose name is name appended to this logger's
// existing name (dot-joined), per hclog's Named contract — distinct from
// ResetNamed, which replaces the name outright.
func (h *hclogAdapter) Named(name string) hclog.Logger {
	full := name
	if h.name != "" {
		full = h.name + "." + name
	}
	return h.resetNamed(full)
}

// ResetNamed returns a sublogger whose name is set to name directly,
// discarding this logger's existing name.
func (h *hclogAdapter) ResetNamed(name string) hclog.Logger {
	return h.resetNamed(name)
}

func (h *hclogAdapter) resetNamed(name string) hclog.Logger {
	n := &hclogAdapter{
		logger:      h.logger,
		name:        name,
		impliedArgs: append([]any{}, h.impliedArgs...),
	}
	n.level.Store(h.level.Load())
	return n
}

// SetLevel updates the level GetLevel reports. It does not alter the
// underlying slog.Handler's own filtering threshold — that's fixed when
// the Handler is constructed — matching hclog's documented allowance for
// implementations that "cannot update the level on the fly."
func (h *hclogAdapter) SetLevel(level hclog.Level) {
	h.level.Store(int32(level))
}

// GetLevel returns the level most recently set via SetLevel, or
// hclog.Info if SetLevel has never been called.
func (h *hclogAdapter) GetLevel() hclog.Level {
	return hclog.Level(h.level.Load())
}

// StandardLogger returns a stdlib *log.Logger that writes through to h.
func (h *hclogAdapter) StandardLogger(opts *hclog.StandardLoggerOptions) *log.Logger {
	return log.New(h.StandardWriter(opts), "", 0)
}

// StandardWriter returns an io.Writer that forwards each written line to h
// at opts.ForceLevel, or hclog.Info if ForceLevel is unset (hclog.NoLevel).
// InferLevels-based per-line level detection is not implemented: this
// adapter exists solely for go-plugin's own rare stdlib-logger bridge
// points, not as a general-purpose hclog replacement.
func (h *hclogAdapter) StandardWriter(opts *hclog.StandardLoggerOptions) io.Writer {
	level := hclog.Info
	if opts != nil && opts.ForceLevel != hclog.NoLevel {
		level = opts.ForceLevel
	}
	return &standardLogWriter{adapter: h, level: level}
}

// standardLogWriter is the io.Writer StandardWriter returns.
type standardLogWriter struct {
	adapter *hclogAdapter
	level   hclog.Level
}

// Write logs the trimmed line in p at w.level and reports len(p), never
// less, so a caller using this as a log.Logger's output never sees a
// short-write error for what is, from its perspective, a successful log
// line.
func (w *standardLogWriter) Write(p []byte) (int, error) {
	if msg := strings.TrimRight(string(p), "\n"); msg != "" {
		w.adapter.Log(w.level, msg)
	}
	return len(p), nil
}
