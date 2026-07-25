package unattended

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/pluggableharness/agent/internal/interactive"
	"github.com/pluggableharness/agent/internal/telemetry"
)

// Resolver always returns interactive.ErrNoFrontend — there is no
// frontend attached to answer anything. See this package's doc.go for
// why it auto-refuses where the sibling internal/plandecision autoallow
// deviation auto-approves, and why the absence of an "acknowledge
// unsafe" construction gate here is deliberate rather than an omission.
type Resolver struct {
	// Logger is where construction and every refusal are logged. Never
	// nil on a Resolver built by New; a zero-value Resolver falls back to
	// slog.Default() per call.
	Logger *slog.Logger

	// Telemetry provides the interactive.resolve span and the
	// InteractiveResolutions counter. New leaves it exactly as passed,
	// including nil — a nil Provider skips instrumentation rather than
	// panicking, so a hand-assembled Resolver stays usable.
	Telemetry *telemetry.Provider
}

// Compile-time anchor: this driver implements the parent seam.
var _ interactive.Resolver = (*Resolver)(nil)

// New constructs the unattended resolver. Unlike the sibling
// internal/plandecision autoallow driver's New, this has no
// acknowledgment gate that can refuse construction — it always succeeds,
// since always-refuse is a safe default with no hidden risk to flag. A
// nil logger falls back to slog.Default(); a nil telem leaves
// instrumentation off.
//
// Construction logs one INFO — not WARN, because a build with no
// frontend refusing interactive calls is that build's expected, safe
// behavior, not a risk being taken.
func New(logger *slog.Logger, telem *telemetry.Provider) *Resolver {
	if logger == nil {
		logger = slog.Default()
	}
	logger.Info("interactive resolver: unattended driver in use; every interactive-kind call will be refused because no frontend is attached to answer one",
		slog.String("driver", driverName))
	return &Resolver{Logger: logger, Telemetry: telem}
}

// driverName is the value logged and spanned as this driver's identity,
// matching the name the drivers selector registers it under.
const driverName = "unattended"

// Resolve always returns interactive.ErrNoFrontend, logging one WARN per
// call naming the refused tool: repeated interactive refusals within a
// session are a distinct signal worth surfacing, since a session that
// keeps hitting interactive calls with nothing able to answer them is
// one that would benefit from a frontend being attached. (This is the
// one place the driver deliberately both logs and returns — the WARN is
// the operator-facing session signal, not a duplicate of the error the
// caller already handles.)
//
// Cancellation is checked first and wins over the refusal: an
// already-done ctx returns ctx.Err() rather than ErrNoFrontend, so a
// caller unwinding a canceled turn is never told the reason was a
// missing frontend.
func (r *Resolver) Resolve(ctx context.Context, req interactive.Request) (interactive.Response, error) {
	if err := ctx.Err(); err != nil {
		return interactive.Response{}, err
	}

	logger := r.logger()
	logger.Debug("interactive resolve: entry",
		slog.String("driver", driverName),
		slog.String("call_id", req.CallID),
		slog.String("tool_name", req.ToolName))

	if r.Telemetry != nil {
		var span trace.Span
		ctx, span = r.Telemetry.StartInteractiveResolve(ctx, req.ToolName)
		defer func() {
			telemetry.EndSpan(span, interactive.ErrNoFrontend)
			r.Telemetry.Instruments().InteractiveResolutions.Add(ctx, 1, metric.WithAttributes(
				telemetry.ToolNameKey.String(req.ToolName),
				telemetry.OutcomeKey.String(telemetry.OutcomeError),
			))
		}()
	}

	logger.Warn("interactive call refused: no frontend is attached to answer it",
		slog.String("driver", driverName),
		slog.String("call_id", req.CallID),
		slog.String("tool_name", req.ToolName))

	return interactive.Response{}, interactive.ErrNoFrontend
}

// logger returns the Logger to use, tolerating a zero-value Resolver
// assembled by hand rather than via New.
func (r *Resolver) logger() *slog.Logger {
	if r.Logger == nil {
		return slog.Default()
	}
	return r.Logger
}
