package contextassembly

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"go.opentelemetry.io/otel/metric"
	"google.golang.org/protobuf/proto"

	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	contextv1 "github.com/pluggableharness/agent/pkg/context/proto/v1"
	eventv1 "github.com/pluggableharness/agent/pkg/event/proto/v1"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"

	"github.com/pluggableharness/agent/internal/providercatalog"
	"github.com/pluggableharness/agent/internal/statebackend"
	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/tokencount"
)

// contextContributionSchemaVersion is the events.schema_version this
// package stamps on every context_contribution event it persists —
// event.v1's ContextContributionEvent, schema generation "1"
// (state-backend.md#the-kind-enum).
const contextContributionSchemaVersion = "1"

// EventSink is how Assembler persists a context_contribution event per
// contributing provider (state-backend.md#the-kind-enum). Satisfied by
// *statebackend.Session. Declared here, per go-layout.md's "define the
// interface where it's consumed" rule, rather than this package importing
// a concrete session type as its dependency.
type EventSink interface {
	AppendEvent(ctx context.Context, ev statebackend.Event) (int64, error)
}

// Config is New's constructor argument.
type Config struct {
	// Tokens resolves every ContextSection's tokens field
	// (kernel-callbacks.md#counttokens) — this package never estimates a
	// count itself. Required.
	Tokens *tokencount.Counter
	// Events persists one context_contribution event per contributing
	// provider. Required.
	Events EventSink
	// Telemetry is this Assembler's tracing/metrics provider. Required —
	// see this package's CLAUDE.md for why a nil Telemetry is a
	// programming error, not a supported "telemetry off" mode.
	Telemetry *telemetry.Provider
	// Logger receives this package's structured log output. Required.
	Logger *slog.Logger
}

// TurnInputs bundles the ContextRequest fields
// (context/data-types.md#contextrequest) a context-assemble firing
// carries identically to every provider in its chain, beyond what a
// resolved providercatalog.ContextHandle already supplies (its own
// per-provider TokenBudget) and what Assemble computes fresh each call
// (PriorSections, HistoryTokens). See this package's CLAUDE.md, "Why
// TurnInputs, not a bare budgetCeiling int64", for why this replaces the
// originally-sketched Assemble(..., budgetCeiling int64) parameter.
type TurnInputs struct {
	// SessionID is the current session's identifier.
	SessionID string
	// ParentSessionID is the parent session's identifier, when this
	// session is a sub-agent session. Empty for a top-level session.
	ParentSessionID string
	// TurnID identifies which turn this firing is for, a ULID string
	// standardized across the whole protocol.
	TurnID string
	// ModelTarget is the model this contribution is being assembled for
	// — id, context_window, and effective_ceiling
	// (model/data-types.md#modeltarget). MUST be set; Assemble returns
	// ErrMissingModelTarget otherwise.
	ModelTarget *modelv1.ModelTarget
	// FilesTouched is the paths touched so far this session. MAY be
	// empty, e.g. at turn 0 / session start.
	FilesTouched []string
	// WorkingDirectory is the session's current working directory.
	WorkingDirectory string
	// AssembledTokensLastTurn is the total assembled context size of the
	// PREVIOUS turn's Assemble call (data-types.md#compactor-timing-signals).
	// This package computes its own HistoryTokens fresh every call, but
	// has no session-durable memory of a previous call's assembled
	// total, so the caller threads the previous call's
	// Result.AssembledTokensLastTurn back in here for the next turn. Zero
	// on a session's first firing.
	AssembledTokensLastTurn int64
}

// Result is one context-assemble firing's outcome.
type Result struct {
	// Sections is the full assembled chain, in declaration order, after
	// every provider's Contribute call, token-budget enforcement, and
	// scope-violation handling.
	Sections []*contentv1.ContextSection
	// HistoryTokens is this call's conversation-history token total,
	// computed via Tokens.Count over the history parameter — the same
	// value threaded into every provider's ContextRequest.HistoryTokens
	// this firing (data-types.md#compactor-timing-signals).
	HistoryTokens int64
	// AssembledTokensLastTurn is THIS call's total assembled context size
	// (the sum of every final section's Tokens) — named to match
	// ContextRequest.assembled_tokens_last_turn because that is exactly
	// what it becomes: the caller passes this value back in as the NEXT
	// Assemble call's TurnInputs.AssembledTokensLastTurn.
	AssembledTokensLastTurn int64
	// RewrittenHistory is the compactor-rewritten conversation history,
	// if any compactor in this chain returned one. MAY be nil.
	// protocol.md#session-wide-conversation-compaction: when non-nil,
	// the kernel MUST replace the turn's conversation history with this
	// before the next model call.
	RewrittenHistory []*contentv1.Message
}

// Assembler runs the context-assemble RPC chain described in
// context/protocol.md#contribute-the-context-assemble-rpc.
type Assembler struct {
	tokens    *tokencount.Counter
	events    EventSink
	telemetry *telemetry.Provider
	logger    *slog.Logger
}

// New returns an Assembler backed by cfg.
func New(cfg Config) *Assembler {
	return &Assembler{
		tokens:    cfg.Tokens,
		events:    cfg.Events,
		telemetry: cfg.Telemetry,
		logger:    cfg.Logger,
	}
}

// Assemble runs every provider in providers' Contribute RPC, in
// agent.hcl declaration order (providers[i].Position — this function
// sorts a copy rather than trusting caller order), building the
// accumulated ContextSection chain described in
// context/data-types.md#ordering--chaining. history is the session's
// current conversation history, visible only to a provider whose
// ContextCapabilities.Compactor is true (protocol.md#session-wide-conversation-compaction).
//
// Per provider, in order:
//
//   - Builds a ContextRequest carrying that provider's own resolved
//     TokenBudget, in's shared session/turn/model fields, the
//     accumulated chain so far as PriorSections, and — for a compactor
//     only — the current conversation history.
//   - Calls Contribute. A transport-level RPC error aborts the REST of
//     the chain and is returned to the caller, mirroring
//     hook-dispatch.md's transform-mode failure handling: an
//     unintended context state reaching the model is a correctness
//     issue serious enough to surface, not swallow. This is distinct
//     from the two isolated-per-provider conditions below, which the
//     kernel handles without failing the turn.
//   - Unless the provider is a compactor, verifies its response did not
//     mutate, reorder, or drop a section it doesn't own
//     (data-types.md#ordering--chaining). A violation discards the
//     provider's entire response for this turn, restores the chain to
//     what it was before this call, and is logged — never failing the
//     turn.
//   - Recomputes each of the provider's own returned section(s)' tokens
//     via Tokens.Count (never trusting a provider-reported count) and
//     rejects — drops — any section exceeding that provider's
//     TokenBudget, or containing a non-text content block
//     (data-types.md#contextsection's v1 text-only rule). Either
//     rejection is per-section, never a whole-response discard.
//   - Persists one context_contribution event for the provider, if at
//     least one of its own sections survived validation.
//   - If the provider is a compactor and returned rewritten_history,
//     records it as the chain's current conversation history for any
//     later compactor in the same firing, and as Result.RewrittenHistory.
func (a *Assembler) Assemble(ctx context.Context, providers []providercatalog.ContextHandle, history []*contentv1.Message, in TurnInputs) (_ Result, err error) {
	if in.ModelTarget == nil {
		return Result{}, ErrMissingModelTarget
	}

	ctx, span := a.telemetry.StartContextAssemble(ctx, in.TurnID)
	defer func() { telemetry.EndSpan(span, err) }()

	ordered := slices.Clone(providers)
	slices.SortStableFunc(ordered, func(x, y providercatalog.ContextHandle) int {
		return cmp.Compare(x.Position, y.Position)
	})

	modelRef := modelRefFromTarget(in.ModelTarget)
	historyTokens, _ := a.tokens.Count(ctx, flattenMessages(history), modelRef)
	currentHistory := history
	var rewrittenHistory []*contentv1.Message

	chain := make([]*contentv1.ContextSection, 0, len(ordered))
	for _, handle := range ordered {
		isCompactor := handle.Capabilities.GetCompactor()

		req := &contextv1.ContextRequest{
			SessionId:               in.SessionID,
			ParentSessionId:         in.ParentSessionID,
			TurnId:                  in.TurnID,
			TokenBudget:             handle.TokenBudget,
			ModelTarget:             in.ModelTarget,
			FilesTouched:            in.FilesTouched,
			WorkingDirectory:        in.WorkingDirectory,
			PriorSections:           chain,
			HistoryTokens:           historyTokens,
			AssembledTokensLastTurn: in.AssembledTokensLastTurn,
		}
		if isCompactor {
			req.ConversationHistory = currentHistory
		}

		a.logger.DebugContext(ctx, "contextassembly: calling Contribute", "provider", handle.Provider, "turn_id", in.TurnID, "compactor", isCompactor)
		pctx, pspan := a.telemetry.StartContextProviderContribute(ctx, handle.Producer)
		resp, callErr := handle.Client.Contribute(pctx, req)
		telemetry.EndSpan(pspan, callErr)
		if callErr != nil {
			a.logger.ErrorContext(ctx, "contextassembly: Contribute RPC failed, aborting context-assemble", "provider", handle.Provider, "turn_id", in.TurnID, "error", callErr)
			return Result{}, fmt.Errorf("contextassembly: provider %q Contribute: %w", handle.Provider, callErr)
		}

		newChain := resp.GetSections()

		if !isCompactor && violatesScope(chain, newChain, handle.Provider) {
			a.logger.WarnContext(ctx, "contextassembly: non-compactor provider mutated a section it does not own, discarding its response", "provider", handle.Provider, "turn_id", in.TurnID)
			a.recordViolation(ctx, telemetry.ContextViolationReasonScope)
			continue
		}

		finalChain, ownContent, ownTokens := a.validateOwnSections(ctx, handle, newChain, modelRef, in.TurnID)
		chain = finalChain

		if isCompactor {
			if rh := resp.GetRewrittenHistory(); len(rh) > 0 {
				rewrittenHistory = rh
				currentHistory = rh
			}
		}

		if len(ownContent) > 0 {
			a.persistContribution(ctx, handle, ownContent, ownTokens, in.ModelTarget)
		}
	}

	return Result{
		Sections:                chain,
		HistoryTokens:           historyTokens,
		AssembledTokensLastTurn: sumTokens(chain),
		RewrittenHistory:        rewrittenHistory,
	}, nil
}

// validateOwnSections walks newChain — the full chain handle.Client.Contribute
// just returned — recomputing and enforcing handle's own TokenBudget on
// every section handle.Provider owns (data-types.md#budget-mechanics) and
// rejecting any owned section carrying a non-text content block
// (data-types.md#contextsection). A foreign-owned section (present because
// handle is a compactor, or simply untouched) passes through unvalidated —
// scope enforcement already happened in Assemble before this is called.
// Returns the resulting chain, the concatenated content blocks of every
// surviving owned section (for the context_contribution event), and their
// summed, kernel-recomputed token total.
func (a *Assembler) validateOwnSections(ctx context.Context, handle providercatalog.ContextHandle, newChain []*contentv1.ContextSection, modelRef *modelv1.ModelRef, turnID string) ([]*contentv1.ContextSection, []*contentv1.ContentBlock, int64) {
	finalChain := make([]*contentv1.ContextSection, 0, len(newChain))
	var ownContent []*contentv1.ContentBlock
	var ownTokens int64

	for _, sec := range newChain {
		if sec.GetProvider() != handle.Provider {
			finalChain = append(finalChain, sec)
			continue
		}

		if hasNonTextBlock(sec.GetContent()) {
			a.logger.ErrorContext(ctx, "contextassembly: provider section contains a non-text content block, dropping section", "provider", handle.Provider, "turn_id", turnID, "label", sec.GetLabel())
			a.recordViolation(ctx, telemetry.ContextViolationReasonNonText)
			continue
		}

		tokens, _ := a.tokens.Count(ctx, sec.GetContent(), modelRef)
		if tokens > handle.TokenBudget {
			a.logger.WarnContext(ctx, "contextassembly: provider section exceeded its token budget, dropping section", "provider", handle.Provider, "turn_id", turnID, "label", sec.GetLabel(), "tokens", tokens, "token_budget", handle.TokenBudget)
			a.recordViolation(ctx, telemetry.ContextViolationReasonBudget)
			continue
		}

		sec.Tokens = tokens // kernel-authoritative recount, never the provider's own.
		finalChain = append(finalChain, sec)
		ownContent = append(ownContent, sec.GetContent()...)
		ownTokens += tokens
	}

	return finalChain, ownContent, ownTokens
}

// persistContribution writes one context_contribution event
// (state-backend.md#the-kind-enum) for handle's surviving contribution
// this firing.
func (a *Assembler) persistContribution(ctx context.Context, handle providercatalog.ContextHandle, content []*contentv1.ContentBlock, tokens int64, target *modelv1.ModelTarget) {
	// MarshalPayload, never a bare proto.Marshal: a contributed content
	// block may carry a structpb.Struct, whose proto map marshals in
	// randomized order unless ordering is pinned
	// (.claude/rules/determinism.md).
	payload, err := statebackend.MarshalPayload(&eventv1.ContextContributionEvent{
		Content: content,
		Tokens:  tokens,
		Target:  target,
	})
	if err != nil {
		a.logger.ErrorContext(ctx, "contextassembly: failed to marshal context_contribution payload", "provider", handle.Provider, "error", err)
		return
	}

	now := time.Now()
	ev := statebackend.Event{
		ID:            statebackend.NewEventID(now),
		Timestamp:     now,
		Kind:          kernelv1.EventKind_EVENT_KIND_CONTEXT_CONTRIBUTION,
		Producer:      handle.Producer,
		SchemaVersion: contextContributionSchemaVersion,
		Payload:       payload,
	}
	if _, err := a.events.AppendEvent(ctx, ev); err != nil {
		a.logger.ErrorContext(ctx, "contextassembly: failed to persist context_contribution event", "provider", handle.Provider, "error", err)
	}
}

// recordViolation increments the ContextContributionViolations metric for
// reason.
func (a *Assembler) recordViolation(ctx context.Context, reason string) {
	a.telemetry.Instruments().ContextContributionViolations.Add(ctx, 1, metric.WithAttributes(telemetry.ContextViolationReasonKey.String(reason)))
}

// violatesScope reports whether newChain, returned by a non-compactor
// provider named providerName, illegally touched a section it does not
// own (data-types.md#ordering--chaining). A provider appears exactly once
// per declaration-order chain, so prior — the chain built from providers
// 1..N-1 before providerName was ever called — consists entirely of
// sections providerName does not own. A compliant non-compactor response
// therefore leaves every foreign-owned section in newChain exactly as it
// was in prior, in the same order: dropping, reordering, inserting, or
// mutating any of them is exactly what this checks for.
func violatesScope(prior, newChain []*contentv1.ContextSection, providerName string) bool {
	foreign := make([]*contentv1.ContextSection, 0, len(newChain))
	for _, sec := range newChain {
		if sec.GetProvider() != providerName {
			foreign = append(foreign, sec)
		}
	}
	if len(foreign) != len(prior) {
		return true
	}
	for i, sec := range foreign {
		if !proto.Equal(sec, prior[i]) {
			return true
		}
	}
	return false
}

// hasNonTextBlock reports whether content contains any block that is not
// a text block — including a nil block — per
// data-types.md#contextsection's "text-only in v1" MUST.
func hasNonTextBlock(content []*contentv1.ContentBlock) bool {
	for _, block := range content {
		if block.GetText() == nil {
			return true
		}
	}
	return false
}

// flattenMessages concatenates every message's content blocks, in order,
// into the single slice Tokens.Count expects.
func flattenMessages(msgs []*contentv1.Message) []*contentv1.ContentBlock {
	var blocks []*contentv1.ContentBlock
	for _, m := range msgs {
		blocks = append(blocks, m.GetContent()...)
	}
	return blocks
}

// sumTokens adds up every section's Tokens field.
func sumTokens(sections []*contentv1.ContextSection) int64 {
	var total int64
	for _, sec := range sections {
		total += sec.GetTokens()
	}
	return total
}

// modelRefFromTarget derives the *modelv1.ModelRef tokencount.Counter.Count
// needs from in.ModelTarget. ModelTarget (model/data-types.md#modeltarget)
// carries only the target model's id, context_window, and
// effective_ceiling — never the agent.hcl LOCAL NAME of the model
// provider plugin serving it, which is what ModelRef.Provider (and
// therefore exact, routed CountTokens resolution) requires. Resolving
// that local name is the model-routing layer's job, several layers above
// this package's boundary; until a future caller threads it through,
// Provider is left empty here, which per tokencount.Counter's documented
// resolution order (step 1: "ref.GetProvider() == \"\" -> Fallback")
// deterministically falls back to the one canonical heuristic rather than
// erroring — never a second, ad hoc fallback path.
func modelRefFromTarget(target *modelv1.ModelTarget) *modelv1.ModelRef {
	return &modelv1.ModelRef{Id: target.GetId()}
}
