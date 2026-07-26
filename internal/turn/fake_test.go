package turn

import (
	"context"
	"strconv"
	"sync"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	hookv1 "github.com/pluggableharness/agent/pkg/hook/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
	planv1 "github.com/pluggableharness/agent/pkg/plan/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"

	"github.com/pluggableharness/agent/internal/agentprofile"
	"github.com/pluggableharness/agent/internal/contextassembly"
	"github.com/pluggableharness/agent/internal/hookdispatch"
	"github.com/pluggableharness/agent/internal/modelcall"
	"github.com/pluggableharness/agent/internal/plangate"
	"github.com/pluggableharness/agent/internal/providercatalog"
	catalogfake "github.com/pluggableharness/agent/internal/providercatalog/drivers/fake"
	"github.com/pluggableharness/agent/internal/tooldispatch"
)

// recorder is the shared, ordered call log every fake below appends to. It
// is what makes the conformance test possible: the algorithm's order is a
// property of the sequence of collaborator calls, not of any one of them.
type recorder struct {
	mu    sync.Mutex
	calls []string
}

// add appends one entry.
func (r *recorder) add(entry string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, entry)
}

// snapshot returns a copy of the log so far.
func (r *recorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

// pointLabel names the hook point a payload's set oneof variant implies —
// the same "the variant IS the point" rule internal/hookdispatch applies.
func pointLabel(t *testing.T, p *hookv1.HookPayload) string {
	t.Helper()
	switch p.GetPayload().(type) {
	case *hookv1.HookPayload_SessionStart:
		return "hooks:session-start"
	case *hookv1.HookPayload_PreModelCall:
		return "hooks:pre-model-call"
	case *hookv1.HookPayload_PostModelResponse:
		return "hooks:post-model-response"
	case *hookv1.HookPayload_PreToolCall:
		return "hooks:pre-tool-call"
	case *hookv1.HookPayload_PlanReady:
		return "hooks:plan-ready"
	case *hookv1.HookPayload_PostToolCall:
		return "hooks:post-tool-call"
	case *hookv1.HookPayload_PostApply:
		return "hooks:post-apply"
	case *hookv1.HookPayload_SessionEnd:
		return "hooks:session-end"
	default:
		t.Fatalf("pointLabel: payload has no oneof variant set: %v", p)
		return ""
	}
}

// fakeHooks is a hand-written HookDispatcher. It echoes the payload back
// unchanged (the no-transform-subscriber case) unless a script says
// otherwise.
type fakeHooks struct {
	t   *testing.T
	rec *recorder

	// vetoPreToolCall maps a tool_use block id to the subscriber name that
	// denies it at pre-tool-call.
	vetoPreToolCall map[string]string
	// transformMessages, when non-nil, is what the pre-model-call chain
	// leaves behind as the transformed messages.
	transformMessages []*contentv1.Message
	// errAt maps a point label to an error the dispatch fails with.
	errAt map[string]error
	// onDispatch runs before anything else, for a test that needs to act
	// mid-chain (cancel a context, say).
	onDispatch func(label string)

	mu       sync.Mutex
	preModel []*hookv1.PreModelCallPayload
	preTool  []*hookv1.PreToolCallPayload
	postTool []*hookv1.PostToolCallPayload
	postAppl []*hookv1.PostApplyPayload
}

// Dispatch implements HookDispatcher.
func (f *fakeHooks) Dispatch(_ context.Context, p *hookv1.HookPayload) (hookdispatch.Outcome, error) {
	label := pointLabel(f.t, p)
	f.rec.add(label)
	if f.onDispatch != nil {
		f.onDispatch(label)
	}
	if err, ok := f.errAt[label]; ok {
		return hookdispatch.Outcome{}, err
	}

	out := hookdispatch.Outcome{Payload: p, Decision: hookv1.HookDecision_HOOK_DECISION_ALLOW}

	f.mu.Lock()
	defer f.mu.Unlock()
	switch v := p.GetPayload().(type) {
	case *hookv1.HookPayload_PreModelCall:
		f.preModel = append(f.preModel, v.PreModelCall)
		if f.transformMessages != nil {
			out.Payload = &hookv1.HookPayload{Payload: &hookv1.HookPayload_PreModelCall{
				PreModelCall: &hookv1.PreModelCallPayload{Messages: f.transformMessages, Model: v.PreModelCall.GetModel()},
			}}
		}
	case *hookv1.HookPayload_PreToolCall:
		f.preTool = append(f.preTool, v.PreToolCall)
		if by, denied := f.vetoPreToolCall[v.PreToolCall.GetCall().GetId()]; denied {
			out.Decision = hookv1.HookDecision_HOOK_DECISION_DENY
			out.DeniedBy = by
		}
	case *hookv1.HookPayload_PostToolCall:
		f.postTool = append(f.postTool, v.PostToolCall)
	case *hookv1.HookPayload_PostApply:
		f.postAppl = append(f.postAppl, v.PostApply)
	}
	return out, nil
}

// fakeContext is a hand-written ContextAssembler.
type fakeContext struct {
	rec *recorder
	res contextassembly.Result
	err error

	mu      sync.Mutex
	inputs  []contextassembly.TurnInputs
	history [][]*contentv1.Message
}

// Assemble implements ContextAssembler.
func (f *fakeContext) Assemble(_ context.Context, _ []providercatalog.ContextHandle, history []*contentv1.Message, in contextassembly.TurnInputs) (contextassembly.Result, error) {
	f.rec.add("context:assemble")
	f.mu.Lock()
	f.inputs = append(f.inputs, in)
	f.history = append(f.history, history)
	f.mu.Unlock()
	if f.err != nil {
		return contextassembly.Result{}, f.err
	}
	return f.res, nil
}

// fakeModel is a hand-written ModelCaller. Responses are consumed in order,
// one per Complete call, so one fake serves a multi-turn scenario.
type fakeModel struct {
	t         *testing.T
	rec       *recorder
	responses []modelcall.Response
	err       error

	mu       sync.Mutex
	requests []modelcall.Request
	next     int
}

// Complete implements ModelCaller.
func (f *fakeModel) Complete(_ context.Context, req modelcall.Request) (modelcall.Response, error) {
	f.rec.add("model:complete")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req)
	if f.err != nil {
		return modelcall.Response{}, f.err
	}
	if f.next >= len(f.responses) {
		f.t.Fatalf("fakeModel: Complete called %d times, only %d responses scripted", f.next+1, len(f.responses))
	}
	resp := f.responses[f.next]
	f.next++
	return resp, nil
}

// fakeGate is a hand-written PlanGate. By default it allows everything;
// denyPrecheck and denyPlan name call ids to deny at each stage.
type fakeGate struct {
	rec *recorder

	// denyPrecheck names call ids the policy precheck denies.
	denyPrecheck map[string]bool
	// trippedPrecheck names call ids whose precheck denial trips the
	// provider's circuit breaker.
	trippedPrecheck map[string]bool
	// denyPlan names call ids the plan gate denies.
	denyPlan map[string]bool
	// errBuild, errDecide, and errResult fail their respective calls.
	errBuild, errDecide, errResult error

	mu         sync.Mutex
	built      []plangate.BuildRequest
	prechecked [][]plangate.PrecheckCall
	applied    [][]plangate.ApplyOutcome
}

// Build implements PlanGate.
func (f *fakeGate) Build(_ context.Context, req plangate.BuildRequest) (*planv1.Plan, error) {
	f.rec.add("gate:build")
	f.mu.Lock()
	f.built = append(f.built, req)
	f.mu.Unlock()
	if f.errBuild != nil {
		return nil, f.errBuild
	}
	items := make([]*planv1.PlanItem, 0, len(req.Items))
	for _, prov := range req.Items {
		items = append(items, prov.Item)
	}
	return &planv1.Plan{TurnId: req.TurnID, Items: items}, nil
}

// Precheck implements PlanGate.
func (f *fakeGate) Precheck(_ context.Context, calls []plangate.PrecheckCall) []plangate.PrecheckResult {
	f.rec.add("gate:precheck")
	f.mu.Lock()
	f.prechecked = append(f.prechecked, calls)
	f.mu.Unlock()

	results := make([]plangate.PrecheckResult, 0, len(calls))
	for _, c := range calls {
		id := c.Call.GetId()
		if f.denyPrecheck[id] {
			results = append(results, plangate.PrecheckResult{
				Call:    c.Call,
				Allowed: false,
				Tripped: f.trippedPrecheck[id],
				Denial: &toolv1.ToolError{
					Category: toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_PERMISSION_DENIED,
					Message:  "policy denied " + c.Provider + "." + c.Call.GetToolName() + " (policy:default); this call was not executed",
				},
			})
			continue
		}
		results = append(results, plangate.PrecheckResult{Call: c.Call, Allowed: true})
	}
	return results
}

// Decide implements PlanGate.
func (f *fakeGate) Decide(_ context.Context, plan *planv1.Plan) (plangate.Decisions, error) {
	f.rec.add("gate:decide")
	if f.errDecide != nil {
		return plangate.Decisions{}, f.errDecide
	}
	d := plangate.Decisions{Plan: plan}
	for _, item := range plan.GetItems() {
		if f.denyPlan[item.GetCallId()] {
			item.Decision = planv1.PlanDecision_PLAN_DECISION_DENY
			item.DecidedBy = "policy:default"
			reason := item.GetProvider() + "." + item.GetOperationName() + " was denied (policy:default); this call was not executed"
			d.Denied = append(d.Denied, plangate.DeniedItem{
				Item:   item,
				Reason: reason,
				Error: &toolv1.ToolError{
					Category: toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_PERMISSION_DENIED,
					Message:  reason,
				},
			})
			continue
		}
		item.Decision = planv1.PlanDecision_PLAN_DECISION_ALLOW
		item.DecidedBy = "policy:default"
		d.Allowed = append(d.Allowed, item)
	}
	return d, nil
}

// DenialBlocks implements PlanGate, matching the real gate's block shape.
func (f *fakeGate) DenialBlocks(d plangate.Decisions) []*contentv1.ContentBlock {
	blocks := make([]*contentv1.ContentBlock, 0, len(d.Denied))
	for _, di := range d.Denied {
		blocks = append(blocks, toolResultBlock(di.Item.GetCallId(), di.Reason, true))
	}
	return blocks
}

// Result implements PlanGate.
func (f *fakeGate) Result(_ context.Context, turnID string, _ plangate.Decisions, out []plangate.ApplyOutcome) (*planv1.ApplyResult, error) {
	f.rec.add("gate:result")
	f.mu.Lock()
	f.applied = append(f.applied, out)
	f.mu.Unlock()
	if f.errResult != nil {
		return nil, f.errResult
	}
	return &planv1.ApplyResult{TurnId: turnID}, nil
}

// fakeTools is a hand-written ToolScheduler. Every call succeeds with a
// payload naming its tool unless failCalls says otherwise.
type fakeTools struct {
	rec *recorder

	// failCalls names call ids whose outcome carries a ToolError.
	failCalls map[string]bool
	// crashTripCalls names call ids whose outcome carries the
	// process_crashed ToolError a tripped circuit breaker produces —
	// internal/tooldispatch stamps the trip into Error.Details rather than
	// onto Outcome itself, so this fake reproduces that exact shape.
	crashTripCalls map[string]bool
	// mislabeledTripCalls names call ids whose outcome carries the
	// BreakerTrippedDetail key on a category that is NOT process_crashed.
	// internal/tooldispatch never produces this shape today — recordBreaker
	// stamps the key only under its crashed branch — so this exists purely
	// to prove the reader's category guard is load-bearing rather than
	// decorative, and would catch a future writer widening the key's use.
	mislabeledTripCalls map[string]bool
	// errExecute fails the whole Execute batch.
	errExecute error
	// shortOutcomes drops the last outcome, to exercise the
	// contract-violation guard.
	shortOutcomes bool

	mu         sync.Mutex
	executed   [][]tooldispatch.Call
	interacted [][]tooldispatch.Call
}

// Execute implements ToolScheduler.
func (f *fakeTools) Execute(_ context.Context, calls []tooldispatch.Call) ([]tooldispatch.Outcome, error) {
	f.rec.add("tools:execute")
	f.mu.Lock()
	f.executed = append(f.executed, calls)
	f.mu.Unlock()
	return f.outcomes(calls)
}

// ExecuteInteractive implements ToolScheduler.
func (f *fakeTools) ExecuteInteractive(_ context.Context, calls []tooldispatch.Call) ([]tooldispatch.Outcome, error) {
	f.rec.add("tools:execute-interactive")
	f.mu.Lock()
	f.interacted = append(f.interacted, calls)
	f.mu.Unlock()
	return f.outcomes(calls)
}

// outcomes builds one outcome per call.
func (f *fakeTools) outcomes(calls []tooldispatch.Call) ([]tooldispatch.Outcome, error) {
	if f.errExecute != nil {
		return nil, f.errExecute
	}
	out := make([]tooldispatch.Outcome, 0, len(calls))
	for _, c := range calls {
		if f.crashTripCalls[c.Call.GetId()] {
			out = append(out, tooldispatch.Outcome{
				Call: c.Call,
				Error: &toolv1.ToolError{
					Category:  toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_PROCESS_CRASHED,
					Message:   c.Call.GetToolName() + " crashed",
					Retryable: true,
					Details: mustStruct(map[string]any{
						tooldispatch.BreakerTrippedDetail:  true,
						tooldispatch.BreakerProviderDetail: c.Handle.Provider,
					}),
				},
			})
			continue
		}
		if f.mislabeledTripCalls[c.Call.GetId()] {
			out = append(out, tooldispatch.Outcome{
				Call: c.Call,
				Error: &toolv1.ToolError{
					Category:  toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_TIMEOUT,
					Message:   c.Call.GetToolName() + " timed out",
					Retryable: true,
					Details: mustStruct(map[string]any{
						tooldispatch.BreakerTrippedDetail:  true,
						tooldispatch.BreakerProviderDetail: c.Handle.Provider,
					}),
				},
			})
			continue
		}
		if f.failCalls[c.Call.GetId()] {
			out = append(out, tooldispatch.Outcome{
				Call: c.Call,
				Error: &toolv1.ToolError{
					Category: toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_UNKNOWN,
					Message:  c.Call.GetToolName() + " failed",
				},
			})
			continue
		}
		out = append(out, tooldispatch.Outcome{
			Call:   c.Call,
			Result: &toolv1.ToolResult{Payload: mustStruct(map[string]any{"ok": c.Call.GetToolName()})},
		})
	}
	if f.shortOutcomes && len(out) > 0 {
		out = out[:len(out)-1]
	}
	return out, nil
}

// seqMinter is a deterministic IDMinter: id-1, id-2, ... in call order.
type seqMinter struct {
	mu sync.Mutex
	n  int
}

// New implements IDMinter.
func (m *seqMinter) New() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.n++
	return "id-" + strconv.Itoa(m.n)
}

// harness bundles a Driver with every fake behind it, so a test can script
// one fake and assert on another without rebuilding the wiring.
type harness struct {
	driver  *Driver
	rec     *recorder
	hooks   *fakeHooks
	context *fakeContext
	model   *fakeModel
	gate    *fakeGate
	tools   *fakeTools
}

// newHarness wires a Driver over fresh fakes. responses are the model
// completions to serve, one per turn.
func newHarness(t *testing.T, responses ...modelcall.Response) *harness {
	t.Helper()
	rec := &recorder{}
	h := &harness{
		rec:     rec,
		hooks:   &fakeHooks{t: t, rec: rec},
		context: &fakeContext{rec: rec},
		model:   &fakeModel{t: t, rec: rec, responses: responses},
		gate:    &fakeGate{rec: rec},
		tools:   &fakeTools{rec: rec},
	}
	d, err := New(Config{
		Hooks:   h.hooks,
		Context: h.context,
		Model:   h.model,
		Gate:    h.gate,
		Tools:   h.tools,
		Catalog: catalogfake.New(),
		IDs:     &seqMinter{},
	})
	if err != nil {
		t.Fatalf("New: unexpected error: %v", err)
	}
	h.driver = d
	return h
}

// baseRequest is a minimally valid Request for the harness above.
func baseRequest(tools map[string]providercatalog.ToolHandle) Request {
	return Request{
		SessionID:        "sess-1",
		TurnID:           "turn-1",
		WorkingDirectory: "/work",
		Model: providercatalog.ModelHandle{
			Ref:      agentprofile.ModelRef{Provider: "anthropic", ID: "test-model"},
			Producer: &commonv1.ProducerRef{Name: "anthropic"},
			Spec:     &modelv1.ModelSpec{Id: "test-model"},
		},
		ModelTarget: &modelv1.ModelTarget{Id: "test-model", ContextWindow: 1000, EffectiveCeiling: 800},
		History:     []*contentv1.Message{userMessage("hello")},
		ScopedTools: tools,
	}
}

// toolHandle builds a resolved ToolHandle for a "<provider>.<name>"
// operation of the given kind.
func toolHandle(provider, name string, kind toolv1.ToolKind) providercatalog.ToolHandle {
	return providercatalog.ToolHandle{
		Provider: provider,
		Producer: &commonv1.ProducerRef{Name: provider},
		Schema: &toolv1.ToolSchema{
			Name:        name,
			Kind:        kind,
			Risk:        toolv1.RiskClass_RISK_CLASS_LOW,
			Description: name + " description",
		},
	}
}

// terminal marks a handle as one whose successful call ends the turn.
func terminal(h providercatalog.ToolHandle) providercatalog.ToolHandle {
	h.TerminatesTurn = true
	return h
}

// userMessage builds a one-text-block user message.
func userMessage(text string) *contentv1.Message {
	return &contentv1.Message{
		Role:    contentv1.Role_ROLE_USER,
		Content: []*contentv1.ContentBlock{{Block: &contentv1.ContentBlock_Text{Text: &contentv1.TextBlock{Text: text}}}},
	}
}

// assistantMessage builds an assistant message from raw blocks.
func assistantMessage(id string, blocks ...*contentv1.ContentBlock) *contentv1.Message {
	return &contentv1.Message{Role: contentv1.Role_ROLE_ASSISTANT, Id: id, Content: blocks}
}

// textBlock builds a text content block.
func textBlock(text string) *contentv1.ContentBlock {
	return &contentv1.ContentBlock{Block: &contentv1.ContentBlock_Text{Text: &contentv1.TextBlock{Text: text}}}
}

// useBlock builds a tool_use content block. name is the scoped
// "<provider>.<tool>" name the model calls.
func useBlock(id, name string, args map[string]any) *contentv1.ContentBlock {
	return &contentv1.ContentBlock{Block: &contentv1.ContentBlock_ToolUse{
		ToolUse: &contentv1.ToolUseBlock{Id: id, Name: name, Arguments: mustStruct(args)},
	}}
}

// response builds a model completion carrying msg.
func response(msg *contentv1.Message) modelcall.Response {
	return modelcall.Response{
		Message:  msg,
		Usage:    &modelv1.Usage{InputTokens: 10, OutputTokens: 5},
		CostUSD:  0.25,
		Stop:     modelv1.StopReason_STOP_REASON_END_TURN,
		Attempts: 1,
	}
}

// mustStruct builds a structpb.Struct, panicking on a malformed literal —
// acceptable in a test helper where the input is a compile-time constant.
func mustStruct(fields map[string]any) *structpb.Struct {
	s, err := structpb.NewStruct(fields)
	if err != nil {
		panic(err)
	}
	return s
}

// toolResultTexts extracts every tool_result block's (tool_use_id, text,
// is_error) triple from a message, in order.
func toolResultTexts(msg *contentv1.Message) []toolResultView {
	var out []toolResultView
	for _, block := range msg.GetContent() {
		if tr := block.GetToolResult(); tr != nil {
			text := ""
			if len(tr.GetContent()) > 0 {
				text = tr.GetContent()[0].GetText().GetText()
			}
			out = append(out, toolResultView{ToolUseID: tr.GetToolUseId(), Text: text, IsError: tr.GetIsError()})
		}
	}
	return out
}

// toolResultView is one flattened tool_result block, for assertions.
type toolResultView struct {
	ToolUseID string
	Text      string
	IsError   bool
}

// equalStrings reports whether got and want hold the same entries in the
// same order.
func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
