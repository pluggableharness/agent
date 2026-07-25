package modelcall

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"sync/atomic"
	"time"

	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"

	"github.com/pluggableharness/agent/internal/providercatalog"
	"github.com/pluggableharness/agent/internal/retrypolicy"
	"github.com/pluggableharness/agent/internal/statebackend"
	"github.com/pluggableharness/agent/internal/telemetry"
)

// MessageSink persists a completed model turn's message plus its cost
// ledger entry in one call, per docs/specifications/state-backend.md's
// requirement that cost_ledger be populated "at the same time as the
// message event that produced it." statebackend.Session.AppendMessage
// already makes this one transaction; MessageSink names exactly that
// method's signature so a *statebackend.Session satisfies it directly,
// with no adapter.
type MessageSink interface {
	AppendMessage(ctx context.Context, ev statebackend.Event, cost statebackend.CostEntry) (int64, error)
}

// var _ MessageSink = (*statebackend.Session)(nil) is deliberately not a
// package-level compile-time anchor here: statebackend.Session's zero
// value cannot be safely constructed outside that package (its fields are
// unexported, and there is no exported zero-value constructor). The
// interface match is exercised for real instead, by
// modelcall_test.go's TestMessageSink_realStatebackendSession, against an
// actual *statebackend.Session opened over a t.TempDir() file.

// Config wires a Caller's dependencies and policy. Every field is
// required except Jitter, Clock, and Sleep, which default to
// production-appropriate implementations (math/rand jitter, time.Now,
// and a context-aware time.Sleep) when left zero-valued — a test supplies
// its own to stay deterministic.
type Config struct {
	// Retry is the backoff policy and the per-attempt/session-wide retry
	// caps, per docs/specifications/agent-loop/error-recovery.md#model-provider-errors.
	Retry retrypolicy.Settings
	// Events persists a successful completion's message and cost ledger
	// entry.
	Events MessageSink
	// Jitter returns a value in [0, 1) for retrypolicy.Delay's jitter
	// term. Defaults to math/rand.Float64 in production; tests pin it to
	// a fixed value for deterministic backoff assertions.
	Jitter func() float64
	// Clock returns the current time, used as the completion's receipt
	// time for cost.ResolveTier and as the persisted event's timestamp.
	// Defaults to time.Now.
	Clock func() time.Time
	// Sleep is a context-aware sleep used for the backoff delay between
	// retries. It MUST honor ctx cancellation, returning ctx.Err() (or an
	// equivalent error) if canceled before the duration elapses. Defaults
	// to a time.Timer-based implementation.
	Sleep func(ctx context.Context, d time.Duration) error
	// Telemetry provides the model.call/model.attempt spans and the
	// usage/cost metrics, per internal/telemetry/span.go's StartModelCall/
	// StartModelAttempt and internal/telemetry/usage.go's RecordUsage.
	Telemetry *telemetry.Provider
	// Logger is this Caller's structured logger.
	Logger *slog.Logger
}

// Request is one StreamCompletion invocation: which model to call, the
// already-built wire request (assembled by a future modelrequest-
// consuming caller from context/history/tools/params), and the message
// id the kernel has already assigned this completion — MUST be set by
// the caller before Complete is invoked, per determinism.md's rule that a
// plugin never assigns its own message id. Complete stamps this id onto
// both the accumulated contentv1.Message and the persisted
// statebackend.Event.
type Request struct {
	// Model is the resolved model handle to call — its Client is what
	// Complete invokes StreamCompletion on.
	Model providercatalog.ModelHandle
	// MessageID is the kernel-assigned id for the message this call will
	// produce, used as both contentv1.Message.Id and the persisted
	// statebackend.Event.ID.
	MessageID string
	// Request is the already-assembled wire request.
	Request *modelv1.StreamCompletionRequest
}

// Response is a successful Complete call's result.
type Response struct {
	// Message is the accumulated canonical message, with Id and the
	// produced_by_model_id/produced_by_provider attribution fields set.
	Message *contentv1.Message
	// Usage is the completion's token accounting, as reported by the
	// model provider.
	Usage *modelv1.Usage
	// CostUSD is the kernel-computed cost of this completion
	// (docs/specifications/model/protocol.md#cost-computation).
	CostUSD float64
	// Stop is the reason generation stopped.
	Stop modelv1.StopReason
	// Attempts is the total number of StreamCompletion invocations this
	// call made, including the one that finally succeeded (1 if the
	// first attempt succeeded).
	Attempts int
}

// Error carries a classified, non-retried (or retries-exhausted) model
// failure, per docs/specifications/agent-loop/error-recovery.md#model-provider-errors.
// A future internal/session caller inspects Category to decide what
// happens next — notably MODEL_ERROR_CATEGORY_CONTEXT_LENGTH_EXCEEDED,
// which triggers the NEXT turn's context reduction rather than anything
// this call itself does.
type Error struct {
	// Category is the classified failure category.
	Category modelv1.ModelErrorCategory
	// Attempts is the total number of StreamCompletion invocations made
	// before giving up (1 if the very first attempt was non-retryable).
	Attempts int
	// Err is the underlying error: either the model provider's own
	// ModelError.message/raw_detail, or an internal failure description
	// for the retries-exhausted case.
	Err error
}

// Error implements the error interface.
func (e *Error) Error() string {
	return fmt.Sprintf("modelcall: %s: %v (attempts=%d)", e.Category, e.Err, e.Attempts)
}

// Unwrap supports errors.Is/errors.As against the wrapped underlying
// error.
func (e *Error) Unwrap() error {
	return e.Err
}

// Caller invokes StreamCompletion with the retry loop
// docs/specifications/agent-loop/error-recovery.md#model-provider-errors
// requires. One Caller lives for one session: SessionRetriesRemaining's
// budget is never reset between Complete calls on the same instance.
type Caller struct {
	cfg Config

	// sessionRetriesUsed is the running count of retries spent across
	// every Complete call this Caller has ever made, atomic so
	// SessionRetriesRemaining can be read from any goroutine without a
	// separate lock.
	sessionRetriesUsed atomic.Int64
}

// New returns a ready Caller. Jitter, Clock, and Sleep in cfg default to
// production implementations when left nil; every other field is the
// caller's responsibility to supply.
func New(cfg Config) *Caller {
	if cfg.Jitter == nil {
		cfg.Jitter = defaultJitter
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.Sleep == nil {
		cfg.Sleep = defaultSleep
	}
	return &Caller{cfg: cfg}
}

// SessionRetriesRemaining reports how much of this Caller's session-wide
// retry budget (cfg.Retry.SessionMaxRetries) is left, per
// error-recovery.md's requirement that per-attempt and session-wide
// retry caps be tracked separately. Complete decrements this on every
// retry attempt across its whole lifetime; a session-wide cap reaching
// zero stops further retries even when the per-attempt cap
// (cfg.Retry.MaxRetries) isn't hit yet.
func (c *Caller) SessionRetriesRemaining() int {
	remaining := c.cfg.Retry.SessionMaxRetries - int(c.sessionRetriesUsed.Load())
	if remaining < 0 {
		return 0
	}
	return remaining
}

// defaultJitter is Config.Jitter's production default: a uniform value in
// [0, 1) from the package-level math/rand source. Retry jitter is not
// security-sensitive (go-architecture.md's crypto/rand rule covers
// tokens/session-ids/nonces, not backoff timing), so math/rand is the
// right tool here, exactly as this package's own doc comment on Config.Jitter
// specifies.
func defaultJitter() float64 {
	return rand.Float64() // #nosec G404 -- backoff jitter timing, not security-sensitive (go-architecture.md's crypto/rand rule covers tokens/session-ids/nonces, not this)
}

// defaultSleep is Config.Sleep's production default: a context-aware
// sleep that returns ctx.Err() if canceled before d elapses.
func defaultSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
