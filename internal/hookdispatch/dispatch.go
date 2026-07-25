package hookdispatch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/otel/metric"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/pluggableharness/agent/internal/hookpayload"
	"github.com/pluggableharness/agent/internal/statebackend"
	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/noop"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	hookv1 "github.com/pluggableharness/agent/pkg/hook/proto/v1"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
)

// ErrTransformFailed is returned by Dispatch when a transform-mode
// subscriber failed — an RPC error, a timeout, an invalid response shape,
// or a response mutating a field the point's mutable-field table does not
// list. Per hook-dispatch.md#subscriber-error-handling the chain aborts
// and the kernel MUST NOT fall back to the pre-transform payload, so the
// Outcome returned alongside this error carries no usable payload.
var ErrTransformFailed = errors.New("hookdispatch: transform subscriber failed")

// ErrNoHookPoint is returned by Dispatch for a payload with no oneof
// variant set — there is no point to dispatch, since the set variant is
// the point (hook-dispatch.md#hook-points).
var ErrNoHookPoint = errors.New("hookdispatch: payload has no hook point variant set")

// hookErrorSchemaVersion versions the HookError payload shape a
// hook_error event carries. It tracks the event.v1 payload generation,
// never a kernel release, for the same reason
// statebackend.KernelProducer's version does: a session's persisted
// payload shape must not churn on every kernel upgrade.
const hookErrorSchemaVersion = "1"

// EventSink persists one kernel-synthesized event. It is
// statebackend.Session.AppendEvent's shape, narrowed to the single method
// this package needs so a test can record appends without a sqlite file.
type EventSink interface {
	// AppendEvent appends ev and returns its assigned sequence.
	AppendEvent(ctx context.Context, ev statebackend.Event) (int64, error)
}

// Options are Dispatcher's optional behaviors.
type Options struct {
	// ConcurrentObserve enables
	// hook-dispatch.md#parallelism-within-one-hook-point's MAY: a maximal
	// run of consecutive observe-mode subscribers may execute
	// concurrently with each other. It never reorders around a
	// neighboring transform or veto subscriber, which stay strictly
	// sequential.
	//
	// Default false. Sequential dispatch keeps hook_error persistence
	// order deterministic (determinism.md); with this on, a concurrent
	// run's hook_error events are still persisted in declaration order,
	// but the subscriber calls themselves interleave.
	ConcurrentObserve bool

	// Clock supplies a hook_error event's display-only timestamp and its
	// ULID event id. Defaults to time.Now. Never an ordering authority —
	// sequence is (determinism.md).
	Clock func() time.Time
}

// Dispatcher walks one hook point's ordered subscriber chain, per
// hook-dispatch.md#dispatch-order-and-payload-flow. Construct with New;
// the zero value is not usable.
//
// A Dispatcher is safe for concurrent use: it holds no per-dispatch
// state, and its Registry is read-only once built.
type Dispatcher struct {
	reg    *Registry
	events EventSink
	telem  *telemetry.Provider
	logger *slog.Logger
	clock  func() time.Time
	opt    Options
}

// defaultTelemetryProvider builds the Provider a Dispatcher falls back to
// when New is called with a nil telem — every signal disabled, matching
// internal/sessionstate's and internal/eventbus's own fallback so a
// caller that doesn't care about telemetry doesn't have to construct a
// Provider just to satisfy this constructor.
func defaultTelemetryProvider() (*telemetry.Provider, error) {
	return telemetry.New(context.Background(), telemetry.Config{}, noop.New(), nil)
}

// New builds a Dispatcher over reg's chains. events persists hook_error
// events and may be nil, in which case failures are logged and counted
// but not persisted — a caller with no live session (config validation,
// a dry run) has nowhere to append to. telem defaults to a Provider with
// every signal disabled and logger to slog.Default() when nil, the same
// fallback convention internal/sessionstate uses.
func New(reg *Registry, events EventSink, telem *telemetry.Provider, logger *slog.Logger, opt Options) *Dispatcher {
	if logger == nil {
		logger = slog.Default()
	}
	if telem == nil {
		// Unreachable in practice: defaultTelemetryProvider's
		// telemetry.Config{} is a fixed, valid zero value this package
		// controls end to end — the same reasoning internal/eventbus.New
		// and internal/sessionstate.NewLive give for panicking here
		// rather than threading an error through a constructor callers
		// expect to be infallible.
		prov, err := defaultTelemetryProvider()
		if err != nil {
			panic(err)
		}
		telem = prov
	}
	if opt.Clock == nil {
		opt.Clock = time.Now
	}

	return &Dispatcher{
		reg:    reg,
		events: events,
		telem:  telem,
		logger: logger,
		clock:  opt.Clock,
		opt:    opt,
	}
}

// Outcome is one hook point dispatch's result.
type Outcome struct {
	// Payload is the payload as transformed by every transform subscriber
	// that ran. It is the input payload unchanged when no transform
	// subscriber altered it, and is nil when Dispatch returns
	// ErrTransformFailed — an aborted chain has no payload the kernel may
	// continue with.
	Payload *hookv1.HookPayload

	// Decision is ALLOW unless a veto subscriber denied. It is only
	// meaningful at a veto-bearing hook point (points.go's
	// vetoBearingPoints); everywhere else no veto subscription can exist,
	// so it is always ALLOW.
	Decision hookv1.HookDecision

	// DeniedBy names whoever produced a non-ALLOW Decision — a plugin's
	// agent.hcl local name, or a pinned KernelVeto's Name(). Empty when
	// Decision is ALLOW.
	DeniedBy string
}

// Dispatch runs the ordered chain for the hook point p's set oneof
// variant implies, per
// hook-dispatch.md#dispatch-order-and-payload-flow's pseudocode:
//
//   - observe: errors and timeouts are logged and persisted as a
//     hook_error event, and the chain continues with payload and decision
//     unaffected;
//   - transform: the response is validated and merged via
//     internal/hookpayload; any failure aborts the chain, persists a
//     hook_error, and returns ErrTransformFailed — never a silent
//     fallback to the pre-transform payload;
//   - veto: an error or timeout fails closed to DENY, and any explicit
//     non-ALLOW decision short-circuits the remaining subscribers.
//
// The kernel-privileged veto pinned at this point, if any, runs ahead of
// every plugin subscriber (Registry.Pin).
//
// Each subscriber's deadline is transport-level — a context.WithTimeout
// on the DispatchHook call itself, never a request field
// (hook-dispatch.md#per-subscriber-timeout). A subscriber's own deadline
// firing is a subscriber failure and fails closed at a veto point;
// ctx being canceled by the caller is not. When the parent ctx is done,
// Dispatch abandons the chain and returns that cancellation, because
// manufacturing a DENY for a turn that is already being torn down would
// persist a decision that never really happened.
func (d *Dispatcher) Dispatch(ctx context.Context, p *hookv1.HookPayload) (Outcome, error) {
	point, ok := hookpayload.Point(p)
	if !ok {
		return Outcome{}, ErrNoHookPoint
	}
	pointText, ok := PointText(point)
	if !ok {
		return Outcome{}, fmt.Errorf("hookdispatch: dispatch: %w: %v", ErrUnknownPoint, point)
	}

	ctx, span := d.telem.StartHookDispatch(ctx, pointText)
	started := d.clock()

	out, err := d.run(ctx, point, pointText, p)

	d.telem.Instruments().HookDuration.Record(ctx, d.clock().Sub(started).Seconds(),
		metric.WithAttributes(telemetry.HookPointKey.String(pointText)))
	telemetry.EndSpan(span, err)
	return out, err
}

// run walks the chain. It is separated from Dispatch purely so the
// dispatch span and duration metric wrap every return path without a
// named-return defer.
func (d *Dispatcher) run(ctx context.Context, point commonv1.HookPoint, pointText string, p *hookv1.HookPayload) (Outcome, error) {
	d.logger.DebugContext(ctx, "hook dispatch start",
		slog.String("hook_point", pointText),
		slog.Int("subscribers", len(d.reg.Subscribers(point))))

	out := Outcome{Payload: p, Decision: hookv1.HookDecision_HOOK_DECISION_ALLOW}

	if v, ok := d.reg.PinnedVeto(point); ok {
		denied, err := d.runKernelVeto(ctx, pointText, v, out.Payload)
		if err != nil {
			return Outcome{Payload: out.Payload}, err
		}
		if denied {
			out.Decision = hookv1.HookDecision_HOOK_DECISION_DENY
			out.DeniedBy = v.Name()
			return out, nil
		}
	}

	chain := d.reg.Subscribers(point)
	for i := 0; i < len(chain); {
		if err := ctx.Err(); err != nil {
			return Outcome{Payload: out.Payload}, fmt.Errorf("hookdispatch: dispatch %s: %w", pointText, err)
		}

		if d.opt.ConcurrentObserve && chain[i].Mode == hookv1.HookMode_HOOK_MODE_OBSERVE {
			end := i
			for end < len(chain) && chain[end].Mode == hookv1.HookMode_HOOK_MODE_OBSERVE {
				end++
			}
			if err := d.runObserveRun(ctx, point, pointText, chain[i:end], out.Payload); err != nil {
				return Outcome{Payload: out.Payload}, err
			}
			i = end
			continue
		}

		sub := chain[i]
		i++

		resp, callErr := d.invoke(ctx, sub, out.Payload)
		if err := ctx.Err(); err != nil {
			return Outcome{Payload: out.Payload}, fmt.Errorf("hookdispatch: dispatch %s: %w", pointText, err)
		}

		switch sub.Mode {
		case hookv1.HookMode_HOOK_MODE_OBSERVE:
			// An observe subscriber can never alter the payload or abort
			// the chain — "a broken logger MUST NOT be able to break the
			// loop" (hook-dispatch.md#subscriber-error-handling).
			if callErr != nil {
				d.recordFailure(ctx, point, pointText, sub, callErr)
			}

		case hookv1.HookMode_HOOK_MODE_TRANSFORM:
			merged, err := transformed(sub, resp, out.Payload, callErr)
			if err != nil {
				d.recordFailure(ctx, point, pointText, sub, err)
				return Outcome{}, fmt.Errorf("hookdispatch: dispatch %s: provider %q: %w: %w", pointText, sub.Provider, ErrTransformFailed, err)
			}
			out.Payload = merged

		case hookv1.HookMode_HOOK_MODE_VETO:
			decision, err := vetoed(resp, callErr)
			if err != nil {
				// Fail closed: a failing veto subscriber can only ever
				// make the kernel more conservative
				// (hook-dispatch.md#timeout-behavior).
				d.recordFailure(ctx, point, pointText, sub, err)
				d.logger.WarnContext(ctx, "veto subscriber failed, failing closed to deny",
					slog.String("hook_point", pointText),
					slog.String("provider", sub.Provider),
					slog.String("error", err.Error()))
				out.Decision = hookv1.HookDecision_HOOK_DECISION_DENY
				out.DeniedBy = sub.Provider
				return out, nil
			}
			if decision != hookv1.HookDecision_HOOK_DECISION_ALLOW {
				out.Decision = decision
				out.DeniedBy = sub.Provider
				return out, nil
			}

		default:
			// Unreachable: NewRegistry rejects any mode outside the three.
			return Outcome{}, fmt.Errorf("hookdispatch: dispatch %s: provider %q: %w: %v", pointText, sub.Provider, ErrUnknownMode, sub.Mode)
		}
	}

	return out, nil
}

// invoke makes one DispatchHook call under its own transport-level
// deadline (hook-dispatch.md#per-subscriber-timeout) and validates the
// response's shape against the declared mode.
func (d *Dispatcher) invoke(ctx context.Context, sub Subscriber, p *hookv1.HookPayload) (*hookv1.DispatchHookResponse, error) {
	modeText, _ := ModeText(sub.Mode)
	ctx, span := d.telem.StartHookSubscriber(ctx, modeText, sub.Producer)

	callCtx, cancel := context.WithTimeout(ctx, sub.Timeout)
	defer cancel()

	resp, err := sub.Client.DispatchHook(callCtx, &hookv1.DispatchHookRequest{Payload: p, Mode: sub.Mode})
	if err != nil {
		err = fmt.Errorf("hookdispatch: dispatch hook: %w", err)
	} else if verr := hookpayload.ValidateShape(sub.Mode, resp); verr != nil {
		err = verr
	}

	telemetry.EndSpan(span, err)
	return resp, err
}

// transformed resolves one transform subscriber's response into the
// payload the rest of the chain sees, or an error the caller turns into
// ErrTransformFailed.
func transformed(sub Subscriber, resp *hookv1.DispatchHookResponse, current *hookv1.HookPayload, callErr error) (*hookv1.HookPayload, error) {
	if callErr != nil {
		return nil, callErr
	}
	merged, err := hookpayload.ApplyTransform(current, resp.GetTransform().GetPayload())
	if err != nil {
		return nil, fmt.Errorf("hookdispatch: provider %q: %w", sub.Provider, err)
	}
	return merged, nil
}

// vetoed resolves one veto subscriber's response into a decision, or an
// error the caller fails closed on. ValidateShape has already rejected an
// UNSPECIFIED decision by the time this runs.
func vetoed(resp *hookv1.DispatchHookResponse, callErr error) (hookv1.HookDecision, error) {
	if callErr != nil {
		return hookv1.HookDecision_HOOK_DECISION_DENY, callErr
	}
	return resp.GetVeto().GetDecision(), nil
}

// runObserveRun dispatches a maximal run of consecutive observe-mode
// subscribers concurrently, then persists their failures in declaration
// order so hook_error sequence stays deterministic regardless of which
// call finished first (determinism.md).
func (d *Dispatcher) runObserveRun(ctx context.Context, point commonv1.HookPoint, pointText string, run []Subscriber, p *hookv1.HookPayload) error {
	errs := make([]error, len(run))

	var wg sync.WaitGroup
	wg.Add(len(run))
	for i, sub := range run {
		go func() {
			defer wg.Done()
			_, errs[i] = d.invoke(ctx, sub, p)
		}()
	}
	wg.Wait()

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("hookdispatch: dispatch %s: %w", pointText, err)
	}
	for i, err := range errs {
		if err != nil {
			d.recordFailure(ctx, point, pointText, run[i], err)
		}
	}
	return nil
}

// runKernelVeto evaluates the pinned kernel veto ahead of every plugin
// subscriber. It reports whether the veto denied. A parent-cancellation
// is returned as an error rather than being manufactured into a deny, the
// same distinction the plugin path draws.
//
// A kernel veto's failure is deliberately not persisted as a hook_error:
// state-backend.md#the-kind-enum attributes a hook_error to the failing
// *subscriber*, and the policy engine is not a plugin — it has no
// ProducerRef, and statebackend rejects the reserved kernel producer on
// any kind but plan and apply. The failure is logged and counted instead.
func (d *Dispatcher) runKernelVeto(ctx context.Context, pointText string, v KernelVeto, p *hookv1.HookPayload) (bool, error) {
	ctx, span := d.telem.StartHookSubscriber(ctx, telemetry.SubscriberModeVeto, nil)

	callCtx, cancel := context.WithTimeout(ctx, d.reg.defaultTimeout)
	defer cancel()

	decision, err := v.Veto(callCtx, p)
	telemetry.EndSpan(span, err)

	if parentErr := ctx.Err(); parentErr != nil {
		return false, fmt.Errorf("hookdispatch: dispatch %s: %w", pointText, parentErr)
	}
	if err != nil {
		d.countFailure(ctx, pointText, telemetry.SubscriberModeVeto)
		d.logger.WarnContext(ctx, "kernel veto failed, failing closed to deny",
			slog.String("hook_point", pointText),
			slog.String("kernel_veto", v.Name()),
			slog.String("error", err.Error()))
		return true, nil
	}
	return decision != hookv1.HookDecision_HOOK_DECISION_ALLOW, nil
}

// recordFailure counts, logs, and persists one subscriber failure as a
// hook_error event.
func (d *Dispatcher) recordFailure(ctx context.Context, point commonv1.HookPoint, pointText string, sub Subscriber, cause error) {
	modeText, _ := ModeText(sub.Mode)
	d.countFailure(ctx, pointText, modeText)

	if sub.Mode == hookv1.HookMode_HOOK_MODE_OBSERVE {
		// The observe path swallows the error, so this is the only place
		// it surfaces in logs. The transform and veto paths log or return
		// their own error at the call site instead — go-style.md's "a
		// function returns an error or logs it, never both".
		d.logger.WarnContext(ctx, "observe subscriber failed, continuing chain",
			slog.String("hook_point", pointText),
			slog.String("provider", sub.Provider),
			slog.String("error", cause.Error()))
	}

	if d.events == nil {
		return
	}
	if err := d.persistHookError(ctx, point, sub, cause); err != nil {
		// Nothing above this can act on a failed append — the dispatch
		// outcome is already decided — so it is logged and dropped here
		// rather than returned.
		d.logger.ErrorContext(ctx, "persisting hook_error failed",
			slog.String("hook_point", pointText),
			slog.String("provider", sub.Provider),
			slog.String("error", err.Error()))
	}
}

// countFailure increments the hook-error counter. Its attributes are the
// two bounded dimensions only — the hook point and the subscriber mode —
// never a producer or session identifier (telemetry's cardinality rule).
func (d *Dispatcher) countFailure(ctx context.Context, pointText, modeText string) {
	d.telem.Instruments().HookErrors.Add(ctx, 1, metric.WithAttributes(
		telemetry.HookPointKey.String(pointText),
		telemetry.SubscriberModeKey.String(modeText),
	))
}

// persistHookError appends the hook_error event for one failed dispatch.
// The event's producer is the failing subscriber, never the kernel:
// state-backend.md#the-kind-enum is explicit that hook_error, though
// kernel-synthesized, carries the failing subscriber's identity, and
// statebackend's own encodeProducer rejects the reserved kernel producer
// on this kind for exactly that reason.
func (d *Dispatcher) persistHookError(ctx context.Context, point commonv1.HookPoint, sub Subscriber, cause error) error {
	detail := &hookv1.HookError{
		Point:      point,
		Subscriber: sub.Producer,
		Mode:       sub.Mode,
		Category:   errorCategory(sub.Mode, cause),
		Message:    cause.Error(),
	}
	body, err := proto.Marshal(detail)
	if err != nil {
		return fmt.Errorf("hookdispatch: marshal hook error: %w", err)
	}

	now := d.clock()
	if _, err := d.events.AppendEvent(ctx, statebackend.Event{
		ID:            statebackend.NewEventID(now),
		Timestamp:     now,
		Kind:          kernelv1.EventKind_EVENT_KIND_HOOK_ERROR,
		Producer:      sub.Producer,
		SchemaVersion: hookErrorSchemaVersion,
		Payload:       body,
	}); err != nil {
		return fmt.Errorf("hookdispatch: append hook_error: %w", err)
	}
	return nil
}

// errorCategory classifies cause for the persisted HookError. The two
// transport-shaped categories are resolved here rather than in
// internal/hookpayload, which is pure domain and never sees a gRPC status
// or a context deadline: a subscriber's own deadline firing is TIMEOUT,
// and codes.Unavailable is the plugin-crash mapping grpc.md already
// assigns. Everything else defers to hookpayload.Category's
// mode-appropriate mapping.
func errorCategory(mode hookv1.HookMode, cause error) hookv1.HookErrorCategory {
	if errors.Is(cause, context.DeadlineExceeded) || status.Code(cause) == codes.DeadlineExceeded {
		return hookv1.HookErrorCategory_HOOK_ERROR_CATEGORY_TIMEOUT
	}
	if status.Code(cause) == codes.Unavailable {
		return hookv1.HookErrorCategory_HOOK_ERROR_CATEGORY_PROCESS_CRASHED
	}
	return hookpayload.Category(mode, cause)
}
