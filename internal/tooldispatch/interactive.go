package tooldispatch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/pluggableharness/agent/internal/interactive"
	"github.com/pluggableharness/agent/internal/telemetry"

	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
)

// ExecuteInteractive runs calls STRICTLY sequentially, in declaration
// order, via cfg.Interactive.Resolve — NEVER consulting ConcurrencySpec
// at all, per tool/protocol.md#kind-interactive: "the kernel MUST ignore
// any declared spec and enforce sequential execution unconditionally."
// This is a structurally separate method from Execute, not a runtime
// branch inside it — see CLAUDE.md for why, and turn-algorithm.md's step
// 8 (a future internal/turn's job) for where the interactive/concurrent
// split happens before either method is ever called.
//
// Unlike Execute, an interactive call never reaches a tool provider's
// Invoke RPC — there is nothing to crash, so cfg.Breaker is never
// consulted here, and no per-call Invoke timeout is applied: a human
// answering a question has no meaningful deadline for this package to
// impose. ctx cancellation (a turn abort) is still honored, mapped to
// TOOL_ERROR_CATEGORY_CANCELLED/TIMEOUT exactly as classifyCtxErr does
// for Execute's lock-wait path.
//
// interactive.ErrNoFrontend is converted to a
// TOOL_ERROR_CATEGORY_PERMISSION_DENIED ToolError, per
// internal/interactive's own doc comment on that sentinel — this is the
// "future tool scheduler" that comment anticipates.
//
// output_schema is still enforced strictly on a successful Response —
// interactive.Response's own doc comment on Payload defers that
// validation to "the caller," which is this method.
func (s *Scheduler) ExecuteInteractive(ctx context.Context, calls []Call) ([]Outcome, error) {
	if len(calls) == 0 {
		return nil, nil
	}
	outcomes := make([]Outcome, len(calls))
	for i, call := range calls {
		outcome, err := s.runOneInteractive(ctx, call)
		if err != nil {
			return nil, err
		}
		outcomes[i] = outcome
	}
	return outcomes, nil
}

// runOneInteractive resolves one interactive-kind call. Like runOne, it
// returns a non-nil error only for a genuine EventSink write failure —
// every other failure (no frontend attached, cancellation, an
// out-of-schema answer) is captured inside the returned Outcome.
func (s *Scheduler) runOneInteractive(ctx context.Context, call Call) (Outcome, error) {
	toolCall := call.Call
	handle := call.Handle
	persistCtx := context.WithoutCancel(ctx)

	ctx, span := s.cfg.Telemetry.StartToolExecute(ctx, toolCall.GetToolName(), telemetry.ToolKindInteractive, handle.Producer)
	defer func() { telemetry.EndSpan(span, nil) }()

	logger := s.cfg.Logger.With(
		slog.String("provider", handle.Provider),
		slog.String("tool_name", toolCall.GetToolName()),
		slog.String("call_id", toolCall.GetId()),
	)
	logger.DebugContext(ctx, "tooldispatch: interactive call entry")

	if err := s.persistToolCall(persistCtx, toolCall, handle.Producer); err != nil {
		logger.ErrorContext(ctx, "tooldispatch: persist tool_call failed", "err", err)
		return Outcome{}, fmt.Errorf("tooldispatch: persist tool_call: %w", err)
	}

	req := interactive.Request{
		CallID:    toolCall.GetId(),
		ToolName:  toolCall.GetToolName(),
		Arguments: toolCall.GetArguments(),
	}
	resp, resolveErr := s.cfg.Interactive.Resolve(ctx, req)

	var result *toolv1.ToolResult
	var toolErr *toolv1.ToolError
	switch {
	case resolveErr == nil:
		result = &toolv1.ToolResult{Payload: resp.Payload}
		if verr := s.validateOutput(ctx, handle.Provider, toolCall.GetToolName(), handle.Schema.GetOutputSchema(), result.GetPayload()); verr != nil {
			result = nil
			toolErr = verr
		}
	case errors.Is(resolveErr, interactive.ErrNoFrontend):
		toolErr = &toolv1.ToolError{
			Category:  toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_PERMISSION_DENIED,
			Message:   resolveErr.Error(),
			Retryable: false,
		}
	case errors.Is(resolveErr, context.Canceled) || errors.Is(resolveErr, context.DeadlineExceeded):
		toolErr = buildToolError(classifyCtxErr(resolveErr), resolveErr)
	default:
		toolErr = buildToolError(toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_UNKNOWN, resolveErr)
	}

	if toolErr != nil {
		logger.DebugContext(ctx, "tooldispatch: interactive call terminal error", "category", toolErr.GetCategory().String())
	} else {
		logger.DebugContext(ctx, "tooldispatch: interactive call terminal result")
	}

	seq, err := s.persistToolResult(persistCtx, toolCall.GetId(), result, toolErr, handle.Producer)
	if err != nil {
		logger.ErrorContext(ctx, "tooldispatch: persist tool_result failed", "err", err)
		return Outcome{}, fmt.Errorf("tooldispatch: persist tool_result: %w", err)
	}

	return Outcome{
		Call:     toolCall,
		Result:   result,
		Error:    toolErr,
		Sequence: seq,
	}, nil
}
