package tokencount

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"go.opentelemetry.io/otel/metric"

	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"

	"github.com/pluggableharness/agent/internal/telemetry"
)

// Fallback is the single canonical fallback heuristic:
// ceil(total_utf8_byte_length(text_of(content)) / 4)
// (kernel-callbacks.md#the-fallback-heuristic). Non-text blocks MUST NOT
// contribute. Go string length (len(s)) is already a UTF-8 byte count —
// use that, not a rune count; this exact byte-vs-rune distinction is the
// determinism hazard determinism.md calls out by name.
//
// There is exactly one fallback formula in this system — do not add a
// second, content-type-aware variant, even as an optimization.
func Fallback(blocks []*contentv1.ContentBlock) int64 {
	var totalBytes int64
	for _, block := range blocks {
		if text := block.GetText(); text != nil {
			totalBytes += int64(len(text.GetText()))
		}
	}
	// Integer ceiling division: (n + divisor - 1) / divisor, valid here
	// since totalBytes is always >= 0.
	return (totalBytes + 3) / 4
}

// joinedText concatenates every text block's content, in order, into the
// single string a model provider's own CountTokensRequest.Text expects —
// the same "text_of(content)" Fallback sums the byte length of, so the two
// counts (exact vendor tokenizer vs. fallback heuristic) are always
// computed over identical text. Non-text blocks contribute nothing, same
// as Fallback.
func joinedText(blocks []*contentv1.ContentBlock) string {
	var sb strings.Builder
	for _, block := range blocks {
		if text := block.GetText(); text != nil {
			sb.WriteString(text.GetText())
		}
	}
	return sb.String()
}

// ModelLookup is how a Counter reaches a model provider's own optional
// CountTokens RPC by the provider's agent.hcl LOCAL NAME (not its
// self-reported producer name — a caller resolves ModelRef.Provider
// against whatever local-name-keyed registry it has; this package
// declares only the one-method interface it needs, per go-layout.md's
// "define the interface where it's consumed" rule — do NOT import
// internal/pluginhost or any concrete plugin registry here, a future
// phase's registry type will satisfy this interface structurally).
type ModelLookup interface {
	ModelClientByLocalName(name string) (modelv1.ModelServiceClient, bool)
}

// Counter resolves CountTokens per kernel-callbacks.md's algorithm:
// exact when a model provider's own CountTokens RPC is reachable and
// implemented, the single documented fallback otherwise. Safe for
// concurrent use.
type Counter struct {
	lookup    ModelLookup
	telemetry *telemetry.Provider
	logger    *slog.Logger

	mu sync.Mutex
	// unimplemented memoizes provider local names whose CountTokens RPC
	// answered codes.Unimplemented, so a provider that doesn't implement
	// the optional RPC isn't re-probed on every subsequent Count call
	// within this Counter's lifetime.
	unimplemented map[string]bool
	// warnedErr throttles the "any other error" WARN log to once per
	// provider per unresolved streak: set on the first generic error for
	// a provider, cleared the moment that provider next succeeds or is
	// memoized unimplemented, so a provider stuck erroring on every call
	// doesn't flood the log, but a resolved provider warns again if it
	// starts erroring again later. This is a logging throttle only — it
	// never affects resolution, so an errored provider is still retried
	// (never memoized) on every call, per the resolution algorithm.
	warnedErr map[string]bool
}

// NewCounter returns a Counter backed by lookup.
func NewCounter(lookup ModelLookup, prov *telemetry.Provider, logger *slog.Logger) *Counter {
	return &Counter{
		lookup:        lookup,
		telemetry:     prov,
		logger:        logger,
		unimplemented: make(map[string]bool),
		warnedErr:     make(map[string]bool),
	}
}

// Count resolves a token count for blocks, optionally against ref (the
// model to count exactly against, if reachable). Resolution order,
// exactly matching kernel-callbacks.md#resolution-algorithm:
//  1. ref == nil or ref.GetProvider() == "" -> Fallback, exact=false.
//  2. ref.Provider previously memoized as codes.Unimplemented ->
//     Fallback, exact=false, no round trip, logged at DEBUG.
//  3. lookup.ModelClientByLocalName(ref.GetProvider()) misses (provider
//     not loaded this session) -> Fallback, exact=false, logged at DEBUG.
//  4. Call the model client's CountTokens RPC:
//     - success -> (result, exact=true).
//     - codes.Unimplemented -> memoize + Fallback, exact=false, DEBUG.
//     - codes.Canceled/DeadlineExceeded -> propagate the caller's
//     cancellation as a Fallback result (never logged as a failure —
//     grpc.md: cancellation is normal control flow) and let the
//     caller's own ctx handling take over.
//     - any other error -> Fallback, exact=false, throttled WARN naming
//     the provider (local name only — never log content) and the
//     code. NOT memoized (a transient error must not permanently
//     downgrade a provider that might succeed next time).
//
// Never returns an error itself — a counting primitive that can fail
// would turn every context/memory provider's `tokens` field computation
// into a failure path, which kernel-callbacks.md's design deliberately
// avoids by always having a fallback.
func (c *Counter) Count(ctx context.Context, blocks []*contentv1.ContentBlock, ref *modelv1.ModelRef) (count int64, exact bool) {
	c.logger.DebugContext(ctx, "tokencount: resolving CountTokens", "block_count", len(blocks), "has_model_ref", ref != nil)

	if ref == nil || ref.GetProvider() == "" {
		c.recordFallback(ctx, telemetry.FallbackReasonNoModelRef)
		return Fallback(blocks), false
	}
	provider := ref.GetProvider()

	if c.isMemoizedUnimplemented(provider) {
		c.logger.DebugContext(ctx, "tokencount: provider memoized unimplemented, skipping round trip", "provider", provider)
		c.recordFallback(ctx, telemetry.FallbackReasonUnimplemented)
		return Fallback(blocks), false
	}

	client, ok := c.lookup.ModelClientByLocalName(provider)
	if !ok {
		c.logger.DebugContext(ctx, "tokencount: model provider not loaded this session", "provider", provider)
		c.recordFallback(ctx, telemetry.FallbackReasonProviderAbsent)
		return Fallback(blocks), false
	}

	resp, err := client.CountTokens(ctx, &modelv1.CountTokensRequest{
		Text:    joinedText(blocks),
		ModelId: ref.GetId(),
	})
	if err != nil {
		return c.resolveError(ctx, blocks, provider, err)
	}

	c.clearWarned(provider)
	return resp.GetCount(), true
}

// resolveError classifies err from a CountTokens round trip and returns
// the appropriate fallback result, per Count's documented resolution
// order for case 4's error branches.
func (c *Counter) resolveError(ctx context.Context, blocks []*contentv1.ContentBlock, provider string, err error) (int64, bool) {
	code := status.Code(err)
	switch code {
	case codes.Unimplemented:
		c.memoizeUnimplemented(provider)
		c.clearWarned(provider)
		c.logger.DebugContext(ctx, "tokencount: model provider does not implement CountTokens, memoizing", "provider", provider)
		c.recordFallback(ctx, telemetry.FallbackReasonUnimplemented)
		return Fallback(blocks), false
	case codes.Canceled, codes.DeadlineExceeded:
		// Cancellation is normal control flow (.claude/rules/grpc.md) —
		// never logged as a failure. The caller's own ctx handling takes
		// over from here; this is not counted against any of the four
		// bounded fallback reasons, since it isn't a provider-side
		// condition at all.
		c.logger.DebugContext(ctx, "tokencount: CountTokens call canceled", "provider", provider)
		return Fallback(blocks), false
	default:
		c.warnError(ctx, provider, code, err)
		c.recordFallback(ctx, telemetry.FallbackReasonError)
		return Fallback(blocks), false
	}
}

// isMemoizedUnimplemented reports whether provider was previously marked
// as answering codes.Unimplemented.
func (c *Counter) isMemoizedUnimplemented(provider string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.unimplemented[provider]
}

// memoizeUnimplemented records that provider's CountTokens RPC answered
// codes.Unimplemented, so future calls skip the round trip entirely.
func (c *Counter) memoizeUnimplemented(provider string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unimplemented[provider] = true
}

// clearWarned resets provider's error-warning throttle, called whenever
// provider produces a non-error outcome (success or a fresh Unimplemented
// memoization) so a later, new error streak warns again instead of
// staying silently throttled forever — mirroring
// internal/eventbus.Subscription's own warn-once-per-streak pattern.
func (c *Counter) clearWarned(provider string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.warnedErr, provider)
}

// warnError logs a throttled WARN for a generic (non-Unimplemented,
// non-cancellation) CountTokens error — once per unresolved streak for
// provider, not once per call, naming only the provider's local name and
// the gRPC code, never any request content.
func (c *Counter) warnError(ctx context.Context, provider string, code codes.Code, err error) {
	c.mu.Lock()
	alreadyWarned := c.warnedErr[provider]
	c.warnedErr[provider] = true
	c.mu.Unlock()

	if alreadyWarned {
		return
	}
	c.logger.WarnContext(ctx, "tokencount: model provider CountTokens failed, falling back to heuristic",
		"provider", provider, "code", code.String(), "error", err)
}

// recordFallback increments the TokenCountFallbacks metric for reason, if
// this Counter has telemetry wired.
func (c *Counter) recordFallback(ctx context.Context, reason string) {
	c.telemetry.Instruments().TokenCountFallbacks.Add(ctx, 1, metric.WithAttributes(telemetry.TokenCountFallbackReasonKey.String(reason)))
}
