package telemetry

import (
	"log/slog"

	"go.opentelemetry.io/contrib/bridges/otelslog"
)

// SlogHandler returns a slog.Handler backed by p's OTel LoggerProvider, via
// the otelslog bridge. This is the seam that lets internal/log.NewServer(...)
// — and any of the kernel's own log/slog output, per internal/CLAUDE.md's
// logging/telemetry integration rule — emit through OTel with zero changes
// to internal/log itself: internal/log only ever needs a *slog.Logger, and
// slog.New(p.SlogHandler(scopeName)) is one.
//
// scopeName identifies the calling component (e.g. "pluggableharness-agent-kernel" for the
// kernel's own logger, or a plugin's name for a plugin-scoped one) and
// becomes the emitted records' instrumentation scope.
//
// Verified empirically (see sloghandler_test.go): otelslog's default
// slog.Level-to-log.Severity conversion happens to map
// internal/log.LevelTrace and internal/log.LevelFatal onto exactly
// log.SeverityTrace1 and log.SeverityFatal1 respectively — because both
// libraries independently use a 4-wide step between named levels
// (log/slog's documented "+-4 per level" convention and OTel's four
// sub-severities per named band). No remapping wrapper is needed.
//
// Trace correlation is automatic: the OTel Logs spec requires Logger.Emit
// to resolve trace context from the ctx passed to the slog call, so a log
// emitted while a span from this same Provider is active in ctx carries
// that span's trace_id/span_id without any extra code here.
func (p *Provider) SlogHandler(scopeName string) slog.Handler {
	return otelslog.NewHandler(scopeName, otelslog.WithLoggerProvider(p.loggerProvider))
}
