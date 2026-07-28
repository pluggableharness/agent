package turn

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	hookv1 "github.com/pluggableharness/agent/pkg/hook/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
	planv1 "github.com/pluggableharness/agent/pkg/plan/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"

	"github.com/pluggableharness/agent/internal/callhash"
	"github.com/pluggableharness/agent/internal/contextassembly"
	"github.com/pluggableharness/agent/internal/modelcall"
	"github.com/pluggableharness/agent/internal/modelrequest"
	"github.com/pluggableharness/agent/internal/plangate"
	"github.com/pluggableharness/agent/internal/providercatalog"
	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/tooldispatch"
)

// finalAnswerInstruction is the synthetic message
// turn-algorithm.md#limit-reached-behavior requires alongside withheld tool
// specs: the model is told the limit was reached and asked to produce a
// final answer from what it already has. Withholding the tools is what
// makes a tool call impossible; this is what tells the model why.
const finalAnswerInstruction = "The session limit was reached (%s). No tools are available for this turn. Produce your final answer from what you already have."

// pending is one tool_use block and everything the turn accumulates about
// it, held in the block's own declaration order. Every step from 7 through
// 15 reads and writes this: it is what keeps a tool_result paired with the
// tool_use block it answers no matter which kind group executed it or which
// call finished first.
type pending struct {
	// block is the model's original tool_use block.
	block *contentv1.ToolUseBlock
	// scopedName is the "<provider>.<tool>" name the model used, which is
	// also the doom-loop hash's tool name.
	scopedName string
	// handle is the resolved operation, zero when scopedName was out of
	// scope.
	handle providercatalog.ToolHandle
	// call is the kernel-built ToolCall, nil when scopedName was out of
	// scope.
	call *toolv1.ToolCall
	// item is the provisional plan item minted at step 7 so
	// PreToolCallPayload.plan_item is populated before a Plan exists. A
	// resource item is carried forward into the plan BY IDENTITY at step
	// 10 — never re-minted — which is what lets plangate stamp a decision
	// onto the same pointer this turn already handed to a hook.
	item *planv1.PlanItem
	// result and toolErr are this call's terminal outcome; exactly one is
	// set once resolved is true.
	result  *toolv1.ToolResult
	toolErr *toolv1.ToolError
	// block15 overrides the tool_result content block built for step 15.
	// Set only for a plan-gate denial, where plangate.DenialBlocks already
	// produced the model-visible block and reusing it verbatim keeps one
	// denial vocabulary.
	block15 *contentv1.ContentBlock
	// resolved reports that this call has a terminal outcome and must not
	// reach a scheduler.
	resolved bool
}

// kind reports the operation's declared kind, or TOOL_KIND_UNSPECIFIED for
// an out-of-scope call (which therefore joins no group at step 8).
func (p *pending) kind() toolv1.ToolKind {
	return p.handle.Schema.GetKind()
}

// resolve records a terminal error outcome.
func (p *pending) resolve(err *toolv1.ToolError) {
	p.toolErr = err
	p.resolved = true
}

// run is one RunTurn call's mutable state. It exists so Driver can stay
// immutable (and therefore safe for concurrent RunTurn calls) while the
// per-step helpers still share the turn's accumulating bookkeeping.
type run struct {
	d       *Driver
	req     Request
	logger  *slog.Logger
	tripped []string
}

// RunTurn executes steps 1 through 15 of
// turn-algorithm.md#the-runturn-algorithm, in that order, and returns
// everything the session driver needs for steps 16 through 18.
//
// Cancellation is normal control flow (.claude/rules/grpc.md): a canceled
// ctx surfaces as a bare ctx.Err(), never wrapped, never logged as a
// failure, and never recorded as a failed turn span.
func (d *Driver) RunTurn(ctx context.Context, req Request) (Result, error) {
	if err := validate(req); err != nil {
		return Result{}, err
	}

	ctx, span := d.telem.StartTurn(ctx, req.TurnIndex)
	var spanErr error
	defer func() { telemetry.EndSpan(span, spanErr) }()

	r := &run{
		d:      d,
		req:    req,
		logger: d.logger.With(slog.String("session_id", req.SessionID), slog.String("turn_id", req.TurnID)),
	}

	res, err := r.execute(ctx)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			r.logger.DebugContext(ctx, "turn: canceled")
			return Result{}, ctxErr
		}
		spanErr = err
		return Result{}, err
	}
	return res, nil
}

// validate rejects a request this package cannot run at all. Everything it
// checks is a value only the session driver can supply.
func validate(req Request) error {
	switch {
	case req.SessionID == "":
		return ErrNoSessionID
	case req.TurnID == "":
		return ErrNoTurnID
	case req.ModelTarget == nil:
		return ErrNoModelTarget
	}
	return nil
}

// execute walks the numbered algorithm. Each step group is one helper; this
// function is the order itself, which is the whole of what
// conformance.md's "turn algorithm executes steps in the documented order"
// MUST asks of this package.
func (r *run) execute(ctx context.Context) (Result, error) {
	// Step 1 — context-assemble.
	assembled, err := r.assembleContext(ctx)
	if err != nil {
		return Result{}, err
	}

	// Step 2 — pre-model-call, then the real wire request.
	history := r.baseHistory(assembled)
	mreq, err := r.buildModelRequest(ctx, history, assembled.Sections)
	if err != nil {
		return Result{}, err
	}

	// Steps 3-5 — StreamCompletion, accumulate, post-model-response.
	resp, err := r.callModel(ctx, mreq)
	if err != nil {
		return Result{}, err
	}

	out := Result{
		Message:         resp.Message,
		Usage:           resp.Usage,
		CostUSD:         resp.CostUSD,
		AssembledTokens: assembled.AssembledTokensLastTurn,
		ActualModel:     resp.ActualModel,
	}

	// Step 6 — the implicit DoneCheck. No tool_use blocks ends the turn
	// here, skipping steps 7 through 14 entirely.
	uses := toolUseBlocks(resp.Message)
	if len(uses) == 0 {
		r.logger.DebugContext(ctx, "turn: done, model requested no tool calls")
		out.Done = true
		out.DoneReason = DoneNoToolCalls
		out.History = appendHistory(history, resp.Message, nil)
		return out, nil
	}

	// Step 7 — pre-tool-call, one dispatch per block in declaration order.
	pendings, err := r.dispatchPreToolCalls(ctx, uses)
	if err != nil {
		return Result{}, err
	}

	// Step 8 — split by kind. Pure Go, no collaborator.
	dataSource, resource, interactive := splitByKind(pendings)

	// Steps 9 and 9b — precheck then execute, concurrently for
	// data_source and strictly sequentially for interactive.
	if err := r.runPrecheckedCalls(ctx, dataSource, false); err != nil {
		return Result{}, err
	}
	if err := r.runPrecheckedCalls(ctx, interactive, true); err != nil {
		return Result{}, err
	}

	// Steps 10-12 — build the plan, decide it, apply what it allowed.
	decisions, applied, err := r.buildDecideApply(ctx, resource)
	if err != nil {
		return Result{}, err
	}

	// Step 13 — post-tool-call for every outcome, in declaration order,
	// plus the terminates_turn check.
	done, reason, err := r.dispatchPostToolCalls(ctx, pendings)
	if err != nil {
		return Result{}, err
	}
	out.Done, out.DoneReason = done, reason

	// Step 14 — post-apply.
	if len(resource) > 0 {
		if err := r.postApply(ctx, decisions, applied); err != nil {
			return Result{}, err
		}
	}

	// Step 15 — history append.
	out.History = appendHistory(history, resp.Message, toolResultBlocks(pendings))
	out.CallHashes = callHashes(pendings)
	out.TrippedProviders = dedupeSorted(r.tripped)
	return out, nil
}

// assembleContext runs step 1. internal/contextassembly persists its own
// context_contribution event per contributing provider, so nothing is
// persisted here.
func (r *run) assembleContext(ctx context.Context) (contextassembly.Result, error) {
	res, err := r.d.context.Assemble(ctx, r.d.catalog.Contexts(), r.req.History, contextassembly.TurnInputs{
		SessionID:               r.req.SessionID,
		ParentSessionID:         r.req.ParentSessionID,
		TurnID:                  r.req.TurnID,
		ModelTarget:             r.req.ModelTarget,
		FilesTouched:            r.req.FilesTouched,
		WorkingDirectory:        r.req.WorkingDirectory,
		AssembledTokensLastTurn: r.req.AssembledTokensLastTurn,
	})
	if err != nil {
		return contextassembly.Result{}, fmt.Errorf("turn: context-assemble: %w", err)
	}
	r.logger.DebugContext(ctx, "turn: context assembled",
		slog.Int("sections", len(res.Sections)), slog.Int64("tokens", res.AssembledTokensLastTurn))
	return res, nil
}

// baseHistory is the conversation history this turn carries forward: the
// request's own, replaced wholesale by a compactor's rewrite when one fired
// (context/protocol.md#session-wide-conversation-compaction makes that
// replacement a MUST), plus the limit-reached turn's synthetic instruction.
//
// Both edits are durable on purpose, and the pre-model-call hook's own
// transform deliberately is NOT — see this package's CLAUDE.md.
func (r *run) baseHistory(assembled contextassembly.Result) []*contentv1.Message {
	history := r.req.History
	if len(assembled.RewrittenHistory) > 0 {
		history = assembled.RewrittenHistory
	}
	if !r.req.FinalAnswer {
		return history
	}
	instruction := &contentv1.Message{
		Role: contentv1.Role_ROLE_USER,
		Content: []*contentv1.ContentBlock{{
			Block: &contentv1.ContentBlock_Text{
				Text: &contentv1.TextBlock{Text: fmt.Sprintf(finalAnswerInstruction, r.req.FinalAnswerReason)},
			},
		}},
	}
	out := make([]*contentv1.Message, 0, len(history)+1)
	out = append(out, history...)
	return append(out, instruction)
}

// buildModelRequest runs step 2: dispatch pre-model-call over the messages
// about to be sent, then assemble the real StreamCompletionRequest from
// whatever the chain left behind.
//
// Tool specs are computed BEFORE the dispatch and are not part of the
// payload — hook.v1's PreModelCallPayload carries only messages (the one
// transform-mutable field in v1) and the immutable model ref. Plan mode and
// the limit-reached turn are both implemented right here, by removing tool
// schemas from the request rather than intercepting a call at runtime.
func (r *run) buildModelRequest(ctx context.Context, history []*contentv1.Message, sections []*contentv1.ContextSection) (*modelv1.StreamCompletionRequest, error) {
	out, err := r.d.hooks.Dispatch(ctx, &hookv1.HookPayload{
		Payload: &hookv1.HookPayload_PreModelCall{
			PreModelCall: &hookv1.PreModelCallPayload{
				Messages: history,
				Model:    &modelv1.ModelRef{Provider: r.req.Model.Ref.Provider, Id: r.req.Model.Ref.ID},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("turn: pre-model-call: %w", err)
	}
	messages := out.Payload.GetPreModelCall().GetMessages()

	spec := r.req.Model.Spec
	if err := modelrequest.ValidateContent(messages, spec); err != nil {
		return nil, fmt.Errorf("turn: pre-model-call: %w", err)
	}
	params := modelrequest.ValidateParams(r.req.Params, spec)
	if params.FellBackThinking || params.FellBackToolChoice {
		r.logger.WarnContext(ctx, "turn: generation params fell back to model defaults",
			slog.Bool("thinking", params.FellBackThinking), slog.Bool("tool_choice", params.FellBackToolChoice))
	}

	return &modelv1.StreamCompletionRequest{
		Messages:         messages,
		ModelId:          r.req.Model.Ref.ID,
		Tools:            r.toolDeclarations(),
		Params:           params.Resolved,
		AssembledContext: sections,
		CallContext:      r.callContext(),
		CacheBreakpoints: modelrequest.PlaceCacheBreakpoints(sections, messages, spec),
	}, nil
}

// toolDeclarations renders this turn's in-scope operations as the tool
// specs the model sees, in sorted name order so a request's bytes never
// depend on Go map iteration order (determinism.md).
//
// FinalAnswer withholds every declaration; PlanMode withholds only the
// TOOL_KIND_RESOURCE ones, which is exactly
// plan-apply-gate.md#decision-semantics' "removing a tool from the schema
// entirely is the cleanest implementation available: the model literally
// cannot attempt the call".
func (r *run) toolDeclarations() []*modelv1.ToolDeclaration {
	if r.req.FinalAnswer || len(r.req.ScopedTools) == 0 {
		return nil
	}
	names := make([]string, 0, len(r.req.ScopedTools))
	for name := range r.req.ScopedTools {
		names = append(names, name)
	}
	sort.Strings(names)

	decls := make([]*modelv1.ToolDeclaration, 0, len(names))
	for _, name := range names {
		handle := r.req.ScopedTools[name]
		if r.req.PlanMode && handle.Schema.GetKind() == toolv1.ToolKind_TOOL_KIND_RESOURCE {
			continue
		}
		decls = append(decls, &modelv1.ToolDeclaration{
			Name:        name,
			Description: handle.Schema.GetDescription(),
			InputSchema: handle.Schema.GetInputSchema(),
		})
	}
	return decls
}

// callContext is the session/turn/working-directory attribution every
// outbound call carries.
func (r *run) callContext() *commonv1.CallContext {
	return &commonv1.CallContext{
		SessionId:        r.req.SessionID,
		TurnId:           r.req.TurnID,
		WorkingDirectory: r.req.WorkingDirectory,
	}
}

// callModel runs steps 3-4 and then step 5.
//
// Complete owns steps 3 and 4 together, and persists the message plus its
// cost ledger row as part of computing its result. post-model-response is
// therefore dispatched after it returns rather than before — an inert
// reordering, since that point has no transform-mutable field and bears no
// veto. See this package's CLAUDE.md for the full argument.
func (r *run) callModel(ctx context.Context, mreq *modelv1.StreamCompletionRequest) (modelcall.Response, error) {
	resp, err := r.d.model.Complete(ctx, modelcall.Request{
		Model:     r.req.Model,
		SessionID: r.req.SessionID,
		MessageID: r.d.ids.New(),
		Request:   mreq,
	})
	if err != nil {
		return modelcall.Response{}, fmt.Errorf("turn: model call: %w", err)
	}

	if _, err := r.d.hooks.Dispatch(ctx, &hookv1.HookPayload{
		Payload: &hookv1.HookPayload_PostModelResponse{
			PostModelResponse: &hookv1.PostModelResponsePayload{
				Message: resp.Message,
				Model:   r.req.Model.Producer,
				Usage:   resp.Usage,
				CostUsd: resp.CostUSD,
			},
		},
	}); err != nil {
		return modelcall.Response{}, fmt.Errorf("turn: post-model-response: %w", err)
	}
	return resp, nil
}

// dispatchPreToolCalls runs step 7: for each tool_use block in declaration
// order, mint a provisional PENDING plan item carrying the snapshot fields
// plan-apply-gate.md#snapshot-rationale requires, then dispatch
// pre-tool-call with it.
//
// pre-tool-call is veto-bearing, so a DENY removes the call from
// consideration entirely and synthesizes the denial tool_result the model
// will see. A block naming an operation outside this turn's scope never
// reaches a hook at all — there is no handle to snapshot a plan item from —
// and resolves straight to an out-of-scope error, still in its own
// declaration slot so its tool_result pairs correctly.
func (r *run) dispatchPreToolCalls(ctx context.Context, uses []*contentv1.ToolUseBlock) ([]*pending, error) {
	pendings := make([]*pending, 0, len(uses))
	for _, block := range uses {
		p := &pending{block: block, scopedName: block.GetName()}
		pendings = append(pendings, p)

		handle, ok := r.req.ScopedTools[p.scopedName]
		if !ok {
			r.logger.WarnContext(ctx, "turn: model called an out-of-scope tool",
				slog.String("tool_name", p.scopedName))
			p.resolve(unknownToolError(p.scopedName))
			continue
		}
		p.handle = handle
		p.call = &toolv1.ToolCall{
			Id:          block.GetId(),
			ToolName:    handle.Schema.GetName(),
			Arguments:   block.GetArguments(),
			CallContext: r.callContext(),
		}
		p.item = r.provisionalItem(p)

		out, err := r.d.hooks.Dispatch(ctx, &hookv1.HookPayload{
			Payload: &hookv1.HookPayload_PreToolCall{
				PreToolCall: &hookv1.PreToolCallPayload{Call: p.call, PlanItem: p.item},
			},
		})
		if err != nil {
			return nil, fmt.Errorf("turn: pre-tool-call: %w", err)
		}
		if out.Decision != hookv1.HookDecision_HOOK_DECISION_ALLOW {
			r.logger.WarnContext(ctx, "turn: pre-tool-call veto denied a call",
				slog.String("provider", handle.Provider),
				slog.String("operation", handle.Schema.GetName()),
				slog.String("denied_by", out.DeniedBy))
			p.resolve(vetoDenialError(handle.Provider, handle.Schema.GetName(), out.DeniedBy))
		}
	}
	return pendings, nil
}

// provisionalItem mints the PENDING plan item step 7 needs so
// PreToolCallPayload.plan_item can be populated before a real Plan exists.
// Its kind/risk/description are read from the resolved handle's schema at
// this moment, which is the plan-construction-time snapshot
// plan-apply-gate.md#snapshot-rationale requires — never re-resolved later.
// preview stays absent here: plangate.Build owns the Preview RPC, and a
// populated preview on a non-resource item is an error it rejects.
func (r *run) provisionalItem(p *pending) *planv1.PlanItem {
	return &planv1.PlanItem{
		Id:               r.d.ids.New(),
		CallId:           p.call.GetId(),
		Provider:         p.handle.Provider,
		OperationName:    p.handle.Schema.GetName(),
		Input:            p.call.GetArguments(),
		Decision:         planv1.PlanDecision_PLAN_DECISION_PENDING,
		Kind:             p.handle.Schema.GetKind(),
		Risk:             p.handle.Schema.GetRisk(),
		Description:      p.handle.Schema.GetDescription(),
		ProducerCategory: commonv1.Category_CATEGORY_TOOL,
	}
}

// splitByKind runs step 8. An already-resolved call (out of scope, or
// vetoed at step 7) joins no group: it has a terminal outcome and must
// never reach a precheck or a scheduler.
func splitByKind(pendings []*pending) (dataSource, resource, interactive []*pending) {
	for _, p := range pendings {
		if p.resolved {
			continue
		}
		switch p.kind() {
		case toolv1.ToolKind_TOOL_KIND_DATA_SOURCE:
			dataSource = append(dataSource, p)
		case toolv1.ToolKind_TOOL_KIND_RESOURCE:
			resource = append(resource, p)
		case toolv1.ToolKind_TOOL_KIND_INTERACTIVE:
			interactive = append(interactive, p)
		default:
			// An operation whose schema declares no kind cannot be
			// gated: plan-apply-gate.md's whole decision structure is
			// kind-driven, so executing it would mean executing
			// something policy could not classify. Deny it rather than
			// guess a kind for it.
			p.resolve(unknownToolError(p.scopedName))
		}
	}
	return dataSource, resource, interactive
}

// runPrecheckedCalls runs step 9 (sequential false) or step 9b (sequential
// true). Both kinds get the identical policy precheck — only scheduling
// differs, which is plan-apply-gate.md#data-source-and-interactive-calls'
// entire distinction between them.
//
// A precheck denial synthesizes its own tool_result and never reaches the
// scheduler.
func (r *run) runPrecheckedCalls(ctx context.Context, group []*pending, sequential bool) error {
	if len(group) == 0 {
		return nil
	}

	checks := make([]plangate.PrecheckCall, 0, len(group))
	for _, p := range group {
		checks = append(checks, plangate.PrecheckCall{Call: p.call, Provider: p.handle.Provider, Schema: p.handle.Schema})
	}

	allowed := make([]*pending, 0, len(group))
	for i, res := range r.d.gate.Precheck(ctx, checks) {
		p := group[i]
		if res.Allowed {
			allowed = append(allowed, p)
			continue
		}
		r.logger.WarnContext(ctx, "turn: policy precheck denied a call",
			slog.String("provider", p.handle.Provider),
			slog.String("operation", p.handle.Schema.GetName()),
			slog.Bool("downgraded_from_ask", res.Downgraded))
		p.resolve(res.Denial)
		if res.Tripped {
			r.tripped = append(r.tripped, p.handle.Provider)
		}
	}

	return r.schedule(ctx, allowed, sequential)
}

// schedule hands allowed's calls to the scheduler and records each outcome
// against its own pending. The two scheduler paths are separate methods on
// purpose, per internal/tooldispatch: interactive calls MUST run
// sequentially regardless of any declared ConcurrencySpec.
func (r *run) schedule(ctx context.Context, allowed []*pending, sequential bool) error {
	if len(allowed) == 0 {
		return nil
	}
	calls := make([]tooldispatch.Call, 0, len(allowed))
	for _, p := range allowed {
		calls = append(calls, tooldispatch.Call{Call: p.call, Handle: p.handle})
	}

	var outcomes []tooldispatch.Outcome
	var err error
	if sequential {
		outcomes, err = r.d.tools.ExecuteInteractive(ctx, calls)
	} else {
		outcomes, err = r.d.tools.Execute(ctx, calls)
	}
	if err != nil {
		return fmt.Errorf("turn: execute tool calls: %w", err)
	}
	if len(outcomes) != len(allowed) {
		return fmt.Errorf("turn: execute tool calls: %w: %d calls, %d outcomes", ErrOutcomeCount, len(allowed), len(outcomes))
	}

	for i, o := range outcomes {
		r.record(ctx, allowed[i], o)
	}
	return nil
}

// record stores one scheduler outcome on its pending and routes a
// crash-driven circuit-breaker trip into this turn's tripped set.
// tooldispatch guarantees exactly one of Result/Error is set.
//
// The trip arrives inside Error.Details' tooldispatch.BreakerTrippedDetail
// boolean rather than as a field on Outcome — internal/tooldispatch's
// CLAUDE.md records why it rides there — so this is the one place that
// reads it. Without this the scheduler's half of the shared per-session
// breaker would be debited and stamped but never acted on, leaving a
// repeatedly-crashing provider to be re-called every turn until an
// ordinary bound fired: exactly the wall the breaker exists to stop the
// loop hitting. The gate's denial half already routes through
// runPrecheckedCalls and recordDenials; this is the crash half.
//
// p.handle.Provider is the name recorded, not the Details' own "provider"
// field, so all three writers of r.tripped agree on the agent.hcl local
// name a caller's limit-reached path will report.
func (r *run) record(ctx context.Context, p *pending, o tooldispatch.Outcome) {
	p.result, p.toolErr, p.resolved = o.Result, o.Error, true
	// Both halves are required: the category, because the contract this
	// reads is specifically "a PROCESS_CRASHED error whose crash tripped
	// the breaker" (tooldispatch.Outcome.Error's doc comment), and the
	// Details flag, because only the crash that actually crossed a
	// threshold sets it. Checking the flag alone would silently widen if a
	// future writer ever reused the key on another category.
	if o.Error.GetCategory() == toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_PROCESS_CRASHED &&
		o.Error.GetDetails().GetFields()[tooldispatch.BreakerTrippedDetail].GetBoolValue() {
		r.logger.WarnContext(ctx, "turn: tool provider circuit breaker tripped",
			slog.String("provider", p.handle.Provider),
			slog.String("operation", p.handle.Schema.GetName()))
		r.tripped = append(r.tripped, p.handle.Provider)
	}
}

// buildDecideApply runs steps 10, 11, and 12.
//
// The plan phase is skipped entirely when this turn made no resource calls:
// plangate.Decide persists a plan event and one plan_items row per item in
// one transaction, and an empty plan would write an audit row per turn
// describing nothing. A plan-ready chain over a plan with no items likewise
// gives a veto subscriber nothing to veto. See this package's CLAUDE.md.
func (r *run) buildDecideApply(ctx context.Context, resource []*pending) (plangate.Decisions, []plangate.ApplyOutcome, error) {
	if len(resource) == 0 {
		return plangate.Decisions{}, nil, nil
	}

	byCall := make(map[string]*pending, len(resource))
	items := make([]plangate.ProvisionalItem, 0, len(resource))
	for _, p := range resource {
		byCall[p.call.GetId()] = p
		// Carried forward BY IDENTITY: the same *planv1.PlanItem step 7
		// already showed a hook is the one Decide stamps a decision onto.
		items = append(items, plangate.ProvisionalItem{Item: p.item, Provider: p.handle.Provider, Handle: p.handle})
	}

	plan, err := r.d.gate.Build(ctx, plangate.BuildRequest{TurnID: r.req.TurnID, Items: items})
	if err != nil {
		return plangate.Decisions{}, nil, fmt.Errorf("turn: build plan: %w", err)
	}

	decisions, err := r.d.gate.Decide(ctx, plan)
	if err != nil {
		return plangate.Decisions{}, nil, fmt.Errorf("turn: plan-ready: %w", err)
	}
	r.recordDenials(ctx, decisions, byCall)

	applied, err := r.applyAllowed(ctx, decisions, byCall)
	if err != nil {
		return plangate.Decisions{}, nil, err
	}
	return decisions, applied, nil
}

// recordDenials resolves every denied plan item against its pending,
// reusing plangate.DenialBlocks' own block verbatim so the denial the model
// reads is the gate's wording, not a second rendering of it.
func (r *run) recordDenials(ctx context.Context, decisions plangate.Decisions, byCall map[string]*pending) {
	blocks := make(map[string]*contentv1.ContentBlock, len(decisions.Denied))
	for _, block := range r.d.gate.DenialBlocks(decisions) {
		blocks[block.GetToolResult().GetToolUseId()] = block
	}

	for _, denied := range decisions.Denied {
		p, ok := byCall[denied.Item.GetCallId()]
		if !ok {
			continue
		}
		p.resolve(denied.Error)
		p.block15 = blocks[denied.Item.GetCallId()]
		if denied.Tripped {
			r.tripped = append(r.tripped, denied.Item.GetProvider())
		}
	}
	if len(decisions.Denied) > 0 {
		r.logger.WarnContext(ctx, "turn: plan gate denied resource calls",
			slog.Int("denied", len(decisions.Denied)),
			slog.String("vetoed_by", decisions.VetoedBy))
	}
}

// applyAllowed runs step 12 — the same scheduler step 9 used, per the
// spec's "one mechanism for both, not two separate rules" — and converts
// each outcome for the gate's step-14 ApplyResult.
func (r *run) applyAllowed(ctx context.Context, decisions plangate.Decisions, byCall map[string]*pending) ([]plangate.ApplyOutcome, error) {
	allowed := make([]*pending, 0, len(decisions.Allowed))
	for _, item := range decisions.Allowed {
		if p, ok := byCall[item.GetCallId()]; ok {
			allowed = append(allowed, p)
		}
	}
	if err := r.schedule(ctx, allowed, false); err != nil {
		return nil, err
	}

	applied := make([]plangate.ApplyOutcome, 0, len(allowed))
	for _, p := range allowed {
		applied = append(applied, applyOutcome(tooldispatch.Outcome{Call: p.call, Result: p.result, Error: p.toolErr}))
	}
	return applied, nil
}

// dispatchPostToolCalls runs step 13: one post-tool-call dispatch per
// outcome, in the declaration order the tool_use blocks appeared in — not
// grouped by kind, not completion order. That ordering is what makes the
// step-15 tool_result blocks pair correctly with their tool_use blocks for
// every vendor API.
//
// It also carries turn-algorithm.md#done-detection's opt-in explicit
// termination: immediately after a call's own dispatch, a successful call
// of an operation declaring terminates_turn ends the turn, independent of
// whether other tool_use blocks were present in the same message. The
// remaining dispatches still run — their calls have already executed by
// now, and dropping their hooks would drop their tool_result blocks and
// break the pairing the same step exists to protect.
func (r *run) dispatchPostToolCalls(ctx context.Context, pendings []*pending) (bool, DoneReason, error) {
	done, reason := false, DoneNone
	for _, p := range pendings {
		if p.call == nil {
			// Never a real call: no ToolCall was ever built for an
			// out-of-scope block, so there is nothing to report to a
			// hook whose payload requires one.
			continue
		}

		payload := &hookv1.PostToolCallPayload{Call: p.call}
		if p.result != nil {
			payload.Outcome = &hookv1.PostToolCallPayload_Result{Result: p.result}
		} else {
			payload.Outcome = &hookv1.PostToolCallPayload_Error{Error: p.toolErr}
		}
		if _, err := r.d.hooks.Dispatch(ctx, &hookv1.HookPayload{
			Payload: &hookv1.HookPayload_PostToolCall{PostToolCall: payload},
		}); err != nil {
			return false, DoneNone, fmt.Errorf("turn: post-tool-call: %w", err)
		}

		// A denied or failed terminal tool does not end the turn:
		// providercatalog.ToolHandle.TerminatesTurn is documented as "a
		// SUCCESSFUL call of this operation is an immediate DoneCheck
		// once its post-tool-call hook has fired".
		if !done && p.handle.TerminatesTurn && p.result != nil {
			r.logger.DebugContext(ctx, "turn: done, terminal tool called",
				slog.String("provider", p.handle.Provider),
				slog.String("operation", p.handle.Schema.GetName()))
			done, reason = true, DoneTerminalTool
		}
	}
	return done, reason, nil
}

// postApply runs step 14: the gate builds and persists the ApplyResult,
// then post-apply fires with it.
func (r *run) postApply(ctx context.Context, decisions plangate.Decisions, applied []plangate.ApplyOutcome) error {
	result, err := r.d.gate.Result(ctx, r.req.TurnID, decisions, applied)
	if err != nil {
		return fmt.Errorf("turn: apply result: %w", err)
	}
	if _, err := r.d.hooks.Dispatch(ctx, &hookv1.HookPayload{
		Payload: &hookv1.HookPayload_PostApply{PostApply: &hookv1.PostApplyPayload{Apply: result}},
	}); err != nil {
		return fmt.Errorf("turn: post-apply: %w", err)
	}
	return nil
}

// toolUseBlocks extracts a message's tool_use blocks in declaration order.
func toolUseBlocks(msg *contentv1.Message) []*contentv1.ToolUseBlock {
	var uses []*contentv1.ToolUseBlock
	for _, block := range msg.GetContent() {
		if use := block.GetToolUse(); use != nil {
			uses = append(uses, use)
		}
	}
	return uses
}

// toolResultBlocks renders every pending's outcome as its model-visible
// tool_result block, in declaration order. A plan-gate denial reuses the
// block plangate already produced; everything else is rendered here in the
// identical shape.
func toolResultBlocks(pendings []*pending) []*contentv1.ContentBlock {
	blocks := make([]*contentv1.ContentBlock, 0, len(pendings))
	for _, p := range pendings {
		switch {
		case p.block15 != nil:
			blocks = append(blocks, p.block15)
		case p.result != nil:
			blocks = append(blocks, resultBlock(p.block.GetId(), p.result))
		case p.toolErr != nil:
			blocks = append(blocks, errorBlock(p.block.GetId(), p.toolErr))
		default:
			// Unreachable while every path above resolves its own
			// pending, but a missing tool_result for a tool_use block is
			// a hard error at several vendor APIs — so synthesize one
			// rather than emit an unpaired block.
			blocks = append(blocks, errorBlock(p.block.GetId(), unknownToolError(p.scopedName)))
		}
	}
	return blocks
}

// appendHistory runs step 15's concatenation: history ++ message ++ the
// turn's tool_result blocks. The results ride in one ROLE_USER message
// because content.v1.Role has no tool role — a tool result is something the
// caller hands back to the model, which is what every vendor API models it
// as too.
func appendHistory(history []*contentv1.Message, message *contentv1.Message, results []*contentv1.ContentBlock) []*contentv1.Message {
	out := make([]*contentv1.Message, 0, len(history)+2)
	out = append(out, history...)
	out = append(out, message)
	if len(results) > 0 {
		out = append(out, &contentv1.Message{Role: contentv1.Role_ROLE_USER, Content: results})
	}
	return out
}

// callHashes computes the doom-loop hash of every resource and data_source
// call, in declaration order, for the caller's step-16 check.
// turn-algorithm.md#doom-loop-detection scopes the window to
// "resource/data-source calls", so interactive calls and out-of-scope
// blocks contribute nothing.
//
// The hashed name is the "<provider>.<tool>" name the model used, not the
// provider-local operation name that reaches the wire: two providers can
// advertise the same operation name, and hashing the unscoped one would
// make two unrelated calls collide into a false doom loop.
func callHashes(pendings []*pending) []string {
	hashes := make([]string, 0, len(pendings))
	for _, p := range pendings {
		switch p.kind() {
		case toolv1.ToolKind_TOOL_KIND_RESOURCE, toolv1.ToolKind_TOOL_KIND_DATA_SOURCE:
			hashes = append(hashes, callhash.Call(p.scopedName, p.block.GetArguments()))
		default:
		}
	}
	if len(hashes) == 0 {
		return nil
	}
	return hashes
}

// dedupeSorted returns names sorted and deduplicated, or nil when empty.
// Sorted because a caller may log or persist it and Go map order must not
// leak into either (determinism.md).
func dedupeSorted(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, name := range names {
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
