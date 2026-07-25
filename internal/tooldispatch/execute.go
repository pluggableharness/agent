package tooldispatch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/metric"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/pluggableharness/agent/internal/schemavalidate"
	"github.com/pluggableharness/agent/internal/statebackend"
	"github.com/pluggableharness/agent/internal/telemetry"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	eventv1 "github.com/pluggableharness/agent/pkg/event/proto/v1"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	schemav1 "github.com/pluggableharness/agent/pkg/schema/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
)

// Execute runs calls honoring ConcurrencySpec keying
// (turn-algorithm.md#turn-level-tool-call-concurrency): one
// semaphore.Weighted per provider, capacity maxWeight;
// safe:false/undeclared acquires the full capacity (exclusive); safe:true
// acquires weight 1 (shared) plus, if key_fields is declared, an
// additional per-key semaphore.Weighted(1) keyed by
// internal/callhash.Fields(args, keyFields). See Scheduler's "Lock
// ordering" doc comment for the one rule that keeps this deadlock-free.
//
// Persists one tool_call event before Invoke and one tool_result event
// after, per call (producer = the tool provider from Call.Handle).
// Outcomes are returned in INPUT order regardless of completion order —
// state-backend sequence values are naturally assigned in commit order,
// which turn-algorithm.md's concurrency section permits to differ from
// input order.
//
// Enforces ToolSchema.output_schema strictly on a returned ToolResult's
// payload: a non-conforming payload is rejected as an "unknown"-category
// ToolError, never passed through to history.
// SCHEMA_TYPE_UNSPECIFIED on output_schema means "no constraint
// declared" and is logged at DEBUG once per (provider, tool), never
// failed.
//
// Applies ToolSchema.default_timeout as the per-call Invoke deadline,
// falling back to cfg.DefaultTimeout when the schema doesn't declare one.
//
// A crashed plugin process (detected via the grpc/codes.Unavailable
// status the Invoke stream returns, per grpc.md's process_crashed
// mapping) is surfaced as a TOOL_ERROR_CATEGORY_PROCESS_CRASHED
// tool_result error and increments cfg.Breaker.RecordCrash(provider). A
// resulting circuit-breaker trip is reported back to the caller via the
// returned Outcome.Error.Details' "breaker_tripped" boolean field — see
// Outcome's doc comment and CLAUDE.md — since routing a tripped provider
// through the limit-reached path is the future internal/turn's job, not
// this package's.
//
// If cfg.SerializeAll is set, every call in calls runs strictly
// sequentially in input order, ignoring ConcurrencySpec entirely — for a
// model whose ModelSpec.supports_parallel_tool_calls is false.
func (s *Scheduler) Execute(ctx context.Context, calls []Call) ([]Outcome, error) {
	if len(calls) == 0 {
		return nil, nil
	}
	if s.cfg.SerializeAll {
		return s.executeSequential(ctx, calls)
	}
	return s.executeConcurrent(ctx, calls)
}

// executeConcurrent is Execute's concurrent fan-out path: one goroutine
// per call under errgroup.WithContext, always waited for in full before
// returning (cancellation.md's "no orphan goroutine writing a
// tool_result after the caller considers the turn done" — errgroup.Wait
// blocks until every launched goroutine has returned).
func (s *Scheduler) executeConcurrent(ctx context.Context, calls []Call) ([]Outcome, error) {
	outcomes := make([]Outcome, len(calls))
	g, gctx := errgroup.WithContext(ctx)
	for i, call := range calls {
		g.Go(func() error {
			outcome, err := s.runOne(gctx, call)
			if err != nil {
				return err
			}
			outcomes[i] = outcome
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return outcomes, nil
}

// executeSequential runs every call in calls one at a time, in input
// order, without launching any goroutine — Execute's cfg.SerializeAll
// path. Locking is a no-op here (Scheduler.acquireLocks skips it when
// SerializeAll is set) since a single caller can never contend with
// itself.
func (s *Scheduler) executeSequential(ctx context.Context, calls []Call) ([]Outcome, error) {
	outcomes := make([]Outcome, len(calls))
	for i, call := range calls {
		outcome, err := s.runOne(ctx, call)
		if err != nil {
			return nil, err
		}
		outcomes[i] = outcome
	}
	return outcomes, nil
}

// runOne executes one call end to end: persist tool_call, apply
// ConcurrencySpec locking (or the SerializeAll no-op) and the per-call
// timeout, Invoke, validate output_schema, record breaker
// crash/success, persist tool_result. Returns a non-nil error only for a
// genuine scheduler-level failure (an EventSink.AppendEvent write
// failing) — every other failure mode (cancellation, timeout, a crashed
// plugin, an invalid output payload) is captured inside the returned
// Outcome instead, so one call's business failure never aborts sibling
// calls sharing the same errgroup.
//
// ctx governs locking and the Invoke call itself, so it responds to the
// caller's own cancellation/timeout promptly. Event persistence
// deliberately uses context.WithoutCancel(ctx) instead: per
// go-architecture.md's "a goroutine that outlives its request derives
// its context deliberately," the tool_call/tool_result audit rows MUST
// still be written even when ctx is already canceled — cancellation.md's
// "no orphan goroutine" guarantee is what makes this durable write safe
// to wait for synchronously rather than abandoning it.
func (s *Scheduler) runOne(ctx context.Context, call Call) (Outcome, error) {
	toolCall := call.Call
	handle := call.Handle
	schema := handle.Schema
	persistCtx := context.WithoutCancel(ctx)

	ctx, span := s.cfg.Telemetry.StartToolExecute(ctx, toolCall.GetToolName(), toolKindAttr(schema.GetKind()), handle.Producer)
	var spanErr error
	defer func() { telemetry.EndSpan(span, spanErr) }()

	logger := s.cfg.Logger.With(
		slog.String("provider", handle.Provider),
		slog.String("tool_name", toolCall.GetToolName()),
		slog.String("call_id", toolCall.GetId()),
	)
	logger.DebugContext(ctx, "tooldispatch: call entry")

	if err := s.persistToolCall(persistCtx, toolCall, handle.Producer); err != nil {
		spanErr = err
		logger.ErrorContext(ctx, "tooldispatch: persist tool_call failed", "err", err)
		return Outcome{}, fmt.Errorf("tooldispatch: persist tool_call: %w", err)
	}

	safe, key, hasKey := concurrencyKey(handle.Provider, toolCall.GetToolName(), toolCall.GetArguments(), schema.GetConcurrency())

	var result *toolv1.ToolResult
	var toolErr *toolv1.ToolError
	var exitCode *int32
	var crashed bool

	// Locks are acquired under the caller's own ctx, NOT under the
	// per-call deadline: ToolSchema.default_timeout is documented as the
	// Invoke deadline, and starting its clock while a call is still queued
	// behind an exclusive (safe:false) sibling would report a TIMEOUT for
	// an operation that was never invoked at all. The per-call deadline is
	// therefore derived below, after the locks are held, so it measures
	// only the provider's own execution.
	release, lockErr := s.acquireLocks(ctx, handle.Provider, safe, key, hasKey)
	if lockErr != nil {
		toolErr = buildToolError(classifyCtxErr(lockErr), lockErr)
	} else {
		defer release()

		invokeCtx := ctx
		if timeout := s.callTimeout(schema); timeout > 0 {
			var cancel context.CancelFunc
			invokeCtx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}

		start := s.cfg.Clock()
		result, toolErr, exitCode, crashed = s.invoke(invokeCtx, handle.Client, toolCall)
		s.recordToolDuration(ctx, toolCall.GetToolName(), s.cfg.Clock().Sub(start), toolErr == nil)
	}

	s.recordBreaker(handle.Provider, crashed, toolErr)

	if result != nil {
		if verr := s.validateOutput(ctx, handle.Provider, toolCall.GetToolName(), schema.GetOutputSchema(), result.GetPayload()); verr != nil {
			result = nil
			toolErr = verr
		}
	}

	if toolErr != nil {
		logger.DebugContext(ctx, "tooldispatch: call terminal error", "category", toolErr.GetCategory().String())
	} else {
		logger.DebugContext(ctx, "tooldispatch: call terminal result")
	}

	seq, err := s.persistToolResult(persistCtx, toolCall.GetId(), result, toolErr, handle.Producer)
	if err != nil {
		spanErr = err
		logger.ErrorContext(ctx, "tooldispatch: persist tool_result failed", "err", err)
		return Outcome{}, fmt.Errorf("tooldispatch: persist tool_result: %w", err)
	}

	return Outcome{
		Call:     toolCall,
		Result:   result,
		Error:    toolErr,
		ExitCode: exitCode,
		Sequence: seq,
	}, nil
}

// invoke calls client.Invoke(call) and consumes its event stream through
// to the terminal result/error event, per
// tool/protocol.md#invoke: output_chunk/progress/partial_result MAY each
// appear zero or more times and are consumed but not otherwise acted on
// by this package; exit_status MAY appear at most once and is captured
// into the returned exitCode. crashed reports whether the failure was
// classified as TOOL_ERROR_CATEGORY_PROCESS_CRASHED.
func (s *Scheduler) invoke(ctx context.Context, client toolv1.ToolServiceClient, call *toolv1.ToolCall) (result *toolv1.ToolResult, toolErr *toolv1.ToolError, exitCode *int32, crashed bool) {
	stream, err := client.Invoke(ctx, &toolv1.InvokeRequest{Call: call})
	if err != nil {
		cat := classifyInvokeErr(err)
		return nil, buildToolError(cat, err), nil, cat == toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_PROCESS_CRASHED
	}

	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil, buildToolError(toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_UNKNOWN,
				errors.New("tooldispatch: invoke stream closed without a terminal result or error event")), exitCode, false
		}
		if err != nil {
			cat := classifyInvokeErr(err)
			return nil, buildToolError(cat, err), exitCode, cat == toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_PROCESS_CRASHED
		}

		ev := resp.GetEvent()
		switch {
		case ev.GetResult() != nil:
			return ev.GetResult(), nil, exitCode, false
		case ev.GetError() != nil:
			return nil, ev.GetError(), exitCode, false
		case ev.GetExitStatus() != nil:
			ec := ev.GetExitStatus().GetExitCode()
			exitCode = &ec
		default:
			// output_chunk / progress / partial_result: non-terminal,
			// keep reading. This package has no rendering/accumulation
			// concern of its own (internal/streamaccum owns that, for a
			// future consumer that needs the intermediate content).
		}
	}
}

// classifyInvokeErr maps a failed Invoke call/Recv error to a
// ToolErrorCategory, per grpc.md's error-taxonomy table: codes.Canceled
// -> cancelled (normal control flow, never a crash), codes.DeadlineExceeded
// -> timeout, codes.Unavailable -> process_crashed (the mapping
// grpc.md's table specifies for a crashed plugin subprocess), anything
// else -> unknown.
func classifyInvokeErr(err error) toolv1.ToolErrorCategory {
	switch {
	case errors.Is(err, context.Canceled) || status.Code(err) == codes.Canceled:
		return toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_CANCELLED
	case errors.Is(err, context.DeadlineExceeded) || status.Code(err) == codes.DeadlineExceeded:
		return toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_TIMEOUT
	case status.Code(err) == codes.Unavailable:
		return toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_PROCESS_CRASHED
	default:
		return toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_UNKNOWN
	}
}

// classifyCtxErr maps a semaphore.Acquire failure (which can only be a
// ctx error — see golang.org/x/sync/semaphore's contract) to a
// ToolErrorCategory: a locally-expired per-call deadline is timeout,
// anything else (including the caller's own ctx or an errgroup sibling's
// failure canceling the shared context) is cancelled.
func classifyCtxErr(err error) toolv1.ToolErrorCategory {
	if errors.Is(err, context.DeadlineExceeded) {
		return toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_TIMEOUT
	}
	return toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_CANCELLED
}

// buildToolError builds a *toolv1.ToolError for cat/err. Retryable per
// conformance.md#error-taxonomy's reaction table: timeout and
// process_crashed are "retryable at kernel's discretion" (this package's
// discretion is "yes" — a transient unavailability is worth a caller-level
// retry); cancelled and unknown are not. TOOL_ERROR_CATEGORY_UNKNOWN MUST
// include the raw underlying error in Details, per errors.pb.go's own
// doc comment on that category.
func buildToolError(cat toolv1.ToolErrorCategory, err error) *toolv1.ToolError {
	te := &toolv1.ToolError{
		Category: cat,
		Message:  err.Error(),
		Retryable: cat == toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_TIMEOUT ||
			cat == toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_PROCESS_CRASHED,
	}
	if cat == toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_UNKNOWN {
		te.Details = &structpb.Struct{Fields: map[string]*structpb.Value{
			"error": structpb.NewStringValue(err.Error()),
		}}
	}
	return te
}

// recordBreaker feeds one call's outcome into cfg.Breaker: a crash
// increments the crash counter (and, if that trip cfg.Breaker, stamps
// "breaker_tripped": true onto toolErr.Details so the caller can inspect
// it — see Outcome's doc comment); anything else counts as a success,
// resetting the provider's consecutive-bad-event streak. A nil
// cfg.Breaker disables tracking entirely.
func (s *Scheduler) recordBreaker(provider string, crashed bool, toolErr *toolv1.ToolError) {
	if s.cfg.Breaker == nil {
		return
	}
	if crashed {
		if s.cfg.Breaker.RecordCrash(provider) && toolErr != nil {
			if toolErr.Details == nil {
				toolErr.Details = &structpb.Struct{Fields: make(map[string]*structpb.Value)}
			}
			toolErr.Details.Fields["breaker_tripped"] = structpb.NewBoolValue(true)
			toolErr.Details.Fields["provider"] = structpb.NewStringValue(provider)
		}
		return
	}
	s.cfg.Breaker.RecordSuccess(provider)
}

// validateOutput enforces schema strictly against payload, per
// tool/protocol.md#invoke: a non-conforming payload MUST be rejected and
// re-surfaced as an "unknown"-category ToolError, never passed through
// to history. schema.Type == SCHEMA_TYPE_UNSPECIFIED (including a nil
// schema) means "no constraint declared" — logged once per (provider,
// tool) at DEBUG and otherwise accepted unconditionally, never failed.
func (s *Scheduler) validateOutput(ctx context.Context, provider, tool string, schema *schemav1.Schema, payload *structpb.Struct) *toolv1.ToolError {
	if schema.GetType() == schemav1.SchemaType_SCHEMA_TYPE_UNSPECIFIED {
		s.logUnspecifiedOnce(ctx, provider, tool)
		return nil
	}

	if payload == nil {
		payload = &structpb.Struct{Fields: make(map[string]*structpb.Value)}
	}
	value := &structpb.Value{Kind: &structpb.Value_StructValue{StructValue: payload}}
	if err := schemavalidate.Validate(value, schema); err != nil {
		return buildToolError(toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_UNKNOWN,
			fmt.Errorf("tooldispatch: result payload failed output_schema validation: %w", err))
	}
	return nil
}

// logUnspecifiedOnce logs one DEBUG line the first time (provider, tool)
// is seen with an unconstrained output_schema, and is silent on every
// later call — this is a static per-operation fact, not something worth
// repeating once per invocation.
func (s *Scheduler) logUnspecifiedOnce(ctx context.Context, provider, tool string) {
	key := provider + "\x00" + tool
	s.loggedMu.Lock()
	_, already := s.loggedUnspecOut[key]
	if !already {
		s.loggedUnspecOut[key] = struct{}{}
	}
	s.loggedMu.Unlock()
	if !already {
		s.cfg.Logger.DebugContext(ctx, "tooldispatch: operation declares no output_schema constraint",
			slog.String("provider", provider), slog.String("tool_name", tool))
	}
}

// persistToolCall marshals call as a ToolCallEvent and appends an
// EVENT_KIND_TOOL_CALL event via cfg.Events, per state-backend.md's
// kind -> event.v1 message table. Unlike persistToolResult, no caller
// needs the assigned sequence for a tool_call row (Outcome.Sequence is
// documented as the tool_result event's sequence specifically), so this
// returns only an error.
func (s *Scheduler) persistToolCall(ctx context.Context, call *toolv1.ToolCall, producer *commonv1.ProducerRef) error {
	// MarshalPayload, never a bare proto.Marshal: ToolCall.arguments is a
	// structpb.Struct, whose proto map marshals in randomized order unless
	// ordering is pinned (.claude/rules/determinism.md).
	payload, err := statebackend.MarshalPayload(&eventv1.ToolCallEvent{Call: call})
	if err != nil {
		return fmt.Errorf("tooldispatch: marshal ToolCallEvent: %w", err)
	}
	now := s.cfg.Clock()
	ev := statebackend.Event{
		ID:            statebackend.NewEventID(now),
		Timestamp:     now,
		Kind:          kernelv1.EventKind_EVENT_KIND_TOOL_CALL,
		Producer:      producer,
		SchemaVersion: eventSchemaVersion,
		Payload:       payload,
	}
	_, err = s.cfg.Events.AppendEvent(ctx, ev)
	return err
}

// persistToolResult marshals exactly one of result/toolErr as a
// ToolResultEvent and appends an EVENT_KIND_TOOL_RESULT event via
// cfg.Events, per state-backend.md's kind -> event.v1 message table.
func (s *Scheduler) persistToolResult(ctx context.Context, toolCallID string, result *toolv1.ToolResult, toolErr *toolv1.ToolError, producer *commonv1.ProducerRef) (int64, error) {
	re := &eventv1.ToolResultEvent{ToolCallId: toolCallID}
	if toolErr != nil {
		re.Outcome = &eventv1.ToolResultEvent_Error{Error: toolErr}
	} else {
		re.Outcome = &eventv1.ToolResultEvent_Result{Result: result}
	}

	// MarshalPayload, never a bare proto.Marshal: ToolResult.payload and
	// ToolError.details are both structpb.Struct, whose proto map marshals
	// in randomized order unless ordering is pinned
	// (.claude/rules/determinism.md).
	payload, err := statebackend.MarshalPayload(re)
	if err != nil {
		return 0, fmt.Errorf("tooldispatch: marshal ToolResultEvent: %w", err)
	}
	now := s.cfg.Clock()
	ev := statebackend.Event{
		ID:            statebackend.NewEventID(now),
		Timestamp:     now,
		Kind:          kernelv1.EventKind_EVENT_KIND_TOOL_RESULT,
		Producer:      producer,
		SchemaVersion: eventSchemaVersion,
		Payload:       payload,
	}
	return s.cfg.Events.AppendEvent(ctx, ev)
}

// recordToolDuration records the Invoke call's wall-clock duration
// against internal/telemetry's shared pluggableharness.tool.duration
// histogram and increments pluggableharness.tool.calls, both bounded by
// ToolNameKey/OutcomeKey only, per attributes.go's cardinality rule.
func (s *Scheduler) recordToolDuration(ctx context.Context, toolName string, d time.Duration, ok bool) {
	outcome := telemetry.OutcomeOK
	if !ok {
		outcome = telemetry.OutcomeError
	}
	attrs := metric.WithAttributes(telemetry.ToolNameKey.String(toolName), telemetry.OutcomeKey.String(outcome))
	s.cfg.Telemetry.Instruments().ToolDuration.Record(ctx, d.Seconds(), attrs)
	s.cfg.Telemetry.Instruments().ToolCalls.Add(ctx, 1, attrs)
}

// toolKindAttr renders kind as the lowercase vocabulary
// internal/telemetry.ToolKindKey expects (tool.md's ToolKind
// vocabulary), matching telemetry.ToolKindResource/DataSource/Interactive
// rather than proto's SCREAMING_SNAKE_CASE String().
func toolKindAttr(kind toolv1.ToolKind) string {
	switch kind {
	case toolv1.ToolKind_TOOL_KIND_RESOURCE:
		return telemetry.ToolKindResource
	case toolv1.ToolKind_TOOL_KIND_DATA_SOURCE:
		return telemetry.ToolKindDataSource
	case toolv1.ToolKind_TOOL_KIND_INTERACTIVE:
		return telemetry.ToolKindInteractive
	default:
		return "unspecified"
	}
}
