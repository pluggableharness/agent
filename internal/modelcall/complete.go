package modelcall

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	eventv1 "github.com/pluggableharness/agent/pkg/event/proto/v1"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"

	"github.com/pluggableharness/agent/internal/cost"
	"github.com/pluggableharness/agent/internal/retrypolicy"
	"github.com/pluggableharness/agent/internal/statebackend"
	"github.com/pluggableharness/agent/internal/streamaccum"
	"github.com/pluggableharness/agent/internal/telemetry"
)

// messageEventSchemaVersion is the event.v1.MessageEvent payload schema
// version this package writes, per
// docs/specifications/state-backend.md#events' "schema_version = \"1\""
// convention (also documented on statebackend.KernelProducer). A future
// breaking change to MessageEvent's shape ships as event.v2 plus this
// constant becoming "2" — never as a silent edit to the v1 payload.
const messageEventSchemaVersion = "1"

// Complete performs steps 3-4 of the RunTurn algorithm
// (docs/specifications/agent-loop/turn-algorithm.md#the-runturn-algorithm):
// invoke req.Model.Client.StreamCompletion, accumulate the resulting
// stream, and react to a classified failure per
// docs/specifications/agent-loop/error-recovery.md#model-provider-errors.
//
//   - ReactionRetry (rate_limited/overloaded): retried with exponential
//     backoff+jitter (retrypolicy.Delay), honoring the model error's own
//     retry_after verbatim when present, up to cfg.Retry.MaxRetries
//     attempts AND while SessionRetriesRemaining() > 0. Exhausting either
//     cap ends the call with a classified *Error.
//   - ReactionReduceContext (context_length_exceeded), ReactionFail
//     (auth_error/invalid_request), and ReactionSurface
//     (content_filtered): never retried — Complete returns a classified
//     *Error after exactly one attempt.
//
// Cancellation — ctx.Done() firing during the stream or during a backoff
// sleep — is normal control flow (.claude/rules/grpc.md): Complete
// returns ctx.Err() directly, never wrapped in *Error, and never logs it
// as a failure.
//
// On success, Complete computes cost_usd (cost.ResolveTier at the
// completion's receipt time against req.Model.Spec.Pricing, then
// cost.Compute) and persists the message plus its cost ledger entry via
// cfg.Events.AppendMessage in one call.
func (c *Caller) Complete(ctx context.Context, req Request) (Response, error) {
	modelID := req.Model.Ref.ID
	ctx, span := c.cfg.Telemetry.StartModelCall(ctx, modelID, req.Model.Producer)
	var spanErr error
	defer func() { telemetry.EndSpan(span, spanErr) }()
	c.cfg.Logger.DebugContext(ctx, "modelcall: starting completion", "model_id", modelID, "message_id", req.MessageID)

	for attemptNum := 1; ; attemptNum++ {
		done, modelErr, attemptErr := c.doAttempt(ctx, req, attemptNum)
		message, usage, stop := done.message, done.usage, done.stop
		if attemptErr != nil {
			if isCancellation(attemptErr) {
				c.cfg.Logger.DebugContext(ctx, "modelcall: canceled", "model_id", modelID, "attempt", attemptNum)
				if ctxErr := ctx.Err(); ctxErr != nil {
					return Response{}, ctxErr
				}
				return Response{}, attemptErr
			}
			spanErr = attemptErr
			c.cfg.Logger.ErrorContext(ctx, "modelcall: failed to accumulate completion stream", "model_id", modelID, "attempt", attemptNum, "err", attemptErr)
			return Response{}, attemptErr
		}

		if modelErr == nil {
			costUSD, persistErr := c.persist(ctx, req, message, usage)
			if persistErr != nil {
				spanErr = persistErr
				c.cfg.Logger.ErrorContext(ctx, "modelcall: failed to persist completion", "model_id", modelID, "attempt", attemptNum, "err", persistErr)
				return Response{}, persistErr
			}
			c.cfg.Telemetry.RecordUsage(ctx, span, telemetry.Usage{
				InputTokens:      usage.GetInputTokens(),
				OutputTokens:     usage.GetOutputTokens(),
				CacheReadTokens:  usage.GetCacheReadTokens(),
				CacheWriteTokens: usage.GetCacheWriteTokens(),
				CostUSD:          costUSD,
				ModelID:          modelID,
			})
			c.cfg.Logger.DebugContext(ctx, "modelcall: completion succeeded", "model_id", modelID, "attempt", attemptNum, "cost_usd", costUSD)
			return Response{
				Message:     message,
				Usage:       usage,
				CostUSD:     costUSD,
				Stop:        stop,
				Attempts:    attemptNum,
				ActualModel: done.metadata.GetActualModel(),
			}, nil
		}

		category := modelErr.GetCategory()
		reaction := retrypolicy.Classify(category)
		if reaction != retrypolicy.ReactionRetry {
			classified := &Error{Category: category, Attempts: attemptNum, Err: modelErrToErr(modelErr)}
			spanErr = classified
			c.cfg.Logger.WarnContext(ctx, "modelcall: non-retryable model error", "model_id", modelID, "category", category, "attempt", attemptNum)
			return Response{}, classified
		}

		if attemptNum > c.cfg.Retry.MaxRetries || c.SessionRetriesRemaining() <= 0 {
			classified := &Error{
				Category: category,
				Attempts: attemptNum,
				Err:      fmt.Errorf("modelcall: retries exhausted: %w", modelErrToErr(modelErr)),
			}
			spanErr = classified
			c.cfg.Logger.WarnContext(ctx, "modelcall: retries exhausted", "model_id", modelID, "category", category, "attempt", attemptNum, "session_retries_remaining", c.SessionRetriesRemaining())
			return Response{}, classified
		}

		c.sessionRetriesUsed.Add(1)

		var retryAfter *time.Duration
		if d := modelErr.GetRetryAfter(); d != nil {
			dur := d.AsDuration()
			retryAfter = &dur
		}
		delay := retrypolicy.Delay(c.cfg.Retry, attemptNum, retryAfter, c.cfg.Jitter())
		c.cfg.Logger.WarnContext(ctx, "modelcall: retrying after model error", "model_id", modelID, "category", category, "attempt", attemptNum, "delay", delay)

		if sleepErr := c.cfg.Sleep(ctx, delay); sleepErr != nil {
			c.cfg.Logger.DebugContext(ctx, "modelcall: canceled during backoff", "model_id", modelID, "attempt", attemptNum)
			if ctxErr := ctx.Err(); ctxErr != nil {
				return Response{}, ctxErr
			}
			return Response{}, sleepErr
		}
	}
}

// modelErrToErr renders a *modelv1.ModelError as a plain error, folding
// in raw_detail when the provider supplied it — debugging context per
// model.md §8, never surfaced to a caller that only checks Category.
func modelErrToErr(modelErr *modelv1.ModelError) error {
	msg := modelErr.GetMessage()
	if raw := modelErr.GetRawDetail(); raw != "" {
		msg = fmt.Sprintf("%s (%s)", msg, raw)
	}
	return errors.New(msg)
}

// completion is one successful attempt's payload.
//
// Bundled rather than returned as a widening tuple: doAttempt already
// reported four values plus an error, and threading vendor metadata
// through as a sixth positional result would make every call site a
// puzzle. The struct also keeps the three failure returns uniform —
// completion{} says "nothing was produced" once, instead of repeating a
// nil/nil/UNSPECIFIED prefix at each one.
type completion struct {
	message  *contentv1.Message
	usage    *modelv1.Usage
	stop     modelv1.StopReason
	metadata *modelv1.StreamEvent_StreamMetadata
}

// doAttempt performs exactly one StreamCompletion invocation: dial the
// RPC, accumulate every event into a fresh streamaccum.Accumulator (never
// reused across attempts, so a retried attempt never inherits a failed
// prior attempt's partial state), and report the outcome as exactly one
// of: a successful completion, a classified modelErr (either the
// accumulator's own decoded ModelError, or a fallback classification of a
// badly-behaved transport-level failure — see classifyTransportErr), or
// an unclassified err for a structurally invalid stream or a
// cancellation.
func (c *Caller) doAttempt(ctx context.Context, req Request, attemptNum int) (out completion, modelErr *modelv1.ModelError, err error) {
	modelID := req.Model.Ref.ID
	ctx, span := c.cfg.Telemetry.StartModelAttempt(ctx, modelID, req.Model.Producer, attemptNum)
	var spanErr error
	defer func() { telemetry.EndSpan(span, spanErr) }()
	c.cfg.Logger.DebugContext(ctx, "modelcall: attempt", "model_id", modelID, "attempt", attemptNum)

	stream, dialErr := req.Model.Client.StreamCompletion(ctx, req.Request)
	if dialErr != nil {
		if isCancellation(dialErr) {
			return completion{}, nil, dialErr
		}
		modelErr = classifyTransportErr(dialErr)
		spanErr = dialErr
		c.cfg.Logger.WarnContext(ctx, "modelcall: transport failure establishing stream, applying fallback classification", "model_id", modelID, "attempt", attemptNum, "category", modelErr.GetCategory(), "err", dialErr)
		return completion{}, modelErr, nil
	}

	acc := streamaccum.New()
	for {
		ev, recvErr := stream.Recv()
		if recvErr != nil {
			if errors.Is(recvErr, io.EOF) {
				break
			}
			if isCancellation(recvErr) {
				return completion{}, nil, recvErr
			}
			modelErr = classifyTransportErr(recvErr)
			spanErr = recvErr
			c.cfg.Logger.WarnContext(ctx, "modelcall: transport failure mid-stream, applying fallback classification", "model_id", modelID, "attempt", attemptNum, "category", modelErr.GetCategory(), "err", recvErr)
			return completion{}, modelErr, nil
		}
		if obsErr := acc.Observe(ev); obsErr != nil {
			err = fmt.Errorf("modelcall: observe stream event: %w", obsErr)
			spanErr = err
			return completion{}, nil, err
		}
		if c.cfg.OnDelta != nil {
			if td := ev.GetTextDelta(); td != nil && td.GetText() != "" {
				c.cfg.OnDelta(req.SessionID, req.MessageID, td.GetText(), kernelv1.DeltaKind_DELTA_KIND_UNSPECIFIED)
			}
			// Thinking rides the same fast path, tagged. The channel a
			// vendor put it on is deliberately not forwarded: a status
			// bar needs to know reasoning is happening, and the finer
			// summary-vs-content split survives on the durable message
			// for a reader that can act on it.
			if td := ev.GetThinkingDelta(); td != nil && td.GetText() != "" {
				c.cfg.OnDelta(req.SessionID, req.MessageID, td.GetText(), kernelv1.DeltaKind_DELTA_KIND_THINKING)
			}
		}
	}

	msg, u, stopReason, ok := acc.Result()
	if !ok {
		err = errors.New("modelcall: stream ended before a terminal event")
		spanErr = err
		return completion{}, nil, err
	}
	done := completion{message: msg, usage: u, stop: stopReason, metadata: acc.Metadata()}
	if accErr := acc.Err(); accErr != nil {
		spanErr = modelErrToErr(accErr)
		return done, accErr, nil
	}
	return done, nil, nil
}

// isCancellation reports whether err represents the kernel canceling the
// stream (a user interrupt, timeout, or turn abort) — normal control flow
// per .claude/rules/grpc.md, never an application error.
func isCancellation(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, context.Canceled) || status.Code(err) == codes.Canceled
}

// classifyTransportErr maps a badly-behaved transport-level gRPC failure
// — one that never carried a structured *modelv1.ModelError inside a
// StreamEvent — back to a ModelErrorCategory, per .claude/rules/grpc.md's
// error-taxonomy table read in reverse. This is a defensive fallback for
// a non-conformant provider plugin, never the primary classification
// path: the primary path is streamaccum.Accumulator.Err(), which decodes
// the structured ModelError a conformant plugin sends as its terminal
// stream event.
//
// codes.ResourceExhausted is inherently ambiguous in reverse: both
// rate_limited and context_length_exceeded forward-map to it per the
// grpc.md table. This function resolves the ambiguity toward
// RATE_LIMITED deliberately: misclassifying a genuine context-length
// failure as rate_limited costs at most cfg.Retry.MaxRetries wasted
// attempts before Complete gives up and returns a classified *Error
// anyway (still bounded, still safe); misclassifying a genuine rate
// limit as context_length_exceeded would mean the kernel never retries a
// request that would plainly have succeeded on a second try. The
// asymmetry in what a wrong guess costs is what breaks the tie.
func classifyTransportErr(err error) *modelv1.ModelError {
	st, ok := status.FromError(err)
	if !ok {
		return &modelv1.ModelError{
			Category: modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_UNKNOWN,
			Message:  err.Error(),
		}
	}

	category := modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_UNKNOWN
	switch st.Code() {
	case codes.ResourceExhausted:
		category = modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_RATE_LIMITED
	case codes.Unavailable:
		category = modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_OVERLOADED
	case codes.Unauthenticated:
		category = modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_AUTH_ERROR
	case codes.InvalidArgument:
		category = modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST
	case codes.FailedPrecondition:
		category = modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_CONTENT_FILTERED
	}

	return &modelv1.ModelError{
		Category: category,
		Message:  st.Message(),
	}
}

// persist computes cost_usd for message/usage and writes the message
// event plus its cost ledger entry via cfg.Events.AppendMessage, in one
// call. It stamps req.MessageID onto message (Id and the
// produced_by_model_id/produced_by_provider attribution fields) before
// either persisting or building the MessageEvent payload — the kernel,
// never the plugin, assigns a message its id (.claude/rules/determinism.md).
func (c *Caller) persist(ctx context.Context, req Request, message *contentv1.Message, usage *modelv1.Usage) (float64, error) {
	modelID := req.Model.Ref.ID
	providerName := req.Model.Ref.Provider

	message.Id = req.MessageID
	message.ProducedByModelId = &modelID
	message.ProducedByProvider = &providerName

	receivedAt := c.cfg.Clock()

	// A free model declared with no tiers bills at zero without any tier
	// resolution. cost.ValidatePricing accepts exactly that shape, so
	// resolving unconditionally would fail every completion from a
	// legally-declared free provider — see cost.IsFree.
	var costUSD float64
	pricing := req.Model.Spec.GetPricing()
	if !cost.IsFree(pricing) {
		tier, err := cost.ResolveTier(pricing, receivedAt, usage.GetInputTokens())
		if err != nil {
			return 0, fmt.Errorf("modelcall: resolve pricing tier: %w", err)
		}
		costUSD = cost.Compute(tier, usage)
	}

	// MarshalPayload, never a bare proto.Marshal: every ToolUseBlock in
	// message carries its arguments as a structpb.Struct, whose proto map
	// marshals in randomized order unless ordering is pinned
	// (.claude/rules/determinism.md).
	payload, err := statebackend.MarshalPayload(&eventv1.MessageEvent{
		Message: message,
		Model:   req.Model.Producer,
		Usage:   usage,
		CostUsd: costUSD,
	})
	if err != nil {
		return 0, fmt.Errorf("modelcall: marshal message event: %w", err)
	}

	ev := statebackend.Event{
		ID:            req.MessageID,
		Timestamp:     receivedAt,
		Kind:          kernelv1.EventKind_EVENT_KIND_MESSAGE,
		Producer:      req.Model.Producer,
		SchemaVersion: messageEventSchemaVersion,
		Payload:       payload,
	}
	entry := statebackend.CostEntry{
		ProviderName:     providerName,
		ModelID:          modelID,
		InputTokens:      usage.GetInputTokens(),
		OutputTokens:     usage.GetOutputTokens(),
		CacheWriteTokens: usage.GetCacheWriteTokens(),
		CacheReadTokens:  usage.GetCacheReadTokens(),
		CostUSD:          costUSD,
	}

	if _, err := c.cfg.Events.AppendMessage(ctx, ev, entry); err != nil {
		return 0, fmt.Errorf("modelcall: persist message: %w", err)
	}
	return costUSD, nil
}
