package hookdispatch

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/hcl/v2"
	"google.golang.org/grpc"

	"github.com/pluggableharness/agent/internal/providercatalog"
	catalogfake "github.com/pluggableharness/agent/internal/providercatalog/drivers/fake"
	"github.com/pluggableharness/agent/internal/statebackend"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	hookv1 "github.com/pluggableharness/agent/pkg/hook/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

// fakeClient is a scripted hookv1.HookSubscriberServiceClient: one
// respond func drives every call, and every call is recorded. A fake, not
// a mock — no expectation recording, no generated call verification
// (go-testing.md).
type fakeClient struct {
	// respond returns this call's response, or an error. It receives the
	// call's ctx so a scenario can honor cancellation or sleep past its
	// own deadline. Nil responds with a mode-appropriate success.
	respond func(ctx context.Context, req *hookv1.DispatchHookRequest) (*hookv1.DispatchHookResponse, error)

	mu    sync.Mutex
	calls []*hookv1.DispatchHookRequest
}

// DispatchHook implements hookv1.HookSubscriberServiceClient.
func (c *fakeClient) DispatchHook(ctx context.Context, req *hookv1.DispatchHookRequest, _ ...grpc.CallOption) (*hookv1.DispatchHookResponse, error) {
	c.mu.Lock()
	c.calls = append(c.calls, req)
	c.mu.Unlock()

	if c.respond != nil {
		return c.respond(ctx, req)
	}
	return okResponse(req.GetMode(), req.GetPayload()), nil
}

// callCount reports how many DispatchHook calls this client has seen.
func (c *fakeClient) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

// okResponse builds the trivially-successful response for mode: an
// ObserveAck, an identity TransformResult, or an ALLOW VetoResult.
func okResponse(mode hookv1.HookMode, p *hookv1.HookPayload) *hookv1.DispatchHookResponse {
	switch mode {
	case hookv1.HookMode_HOOK_MODE_TRANSFORM:
		return &hookv1.DispatchHookResponse{Outcome: &hookv1.DispatchHookResponse_Transform{
			Transform: &hookv1.DispatchHookResponse_TransformResult{Payload: p},
		}}
	case hookv1.HookMode_HOOK_MODE_VETO:
		return &hookv1.DispatchHookResponse{Outcome: &hookv1.DispatchHookResponse_Veto{
			Veto: &hookv1.DispatchHookResponse_VetoResult{Decision: hookv1.HookDecision_HOOK_DECISION_ALLOW},
		}}
	default:
		return &hookv1.DispatchHookResponse{Outcome: &hookv1.DispatchHookResponse_Observe{
			Observe: &hookv1.DispatchHookResponse_ObserveAck{},
		}}
	}
}

// vetoResponse builds a VetoResult carrying decision.
func vetoResponse(decision hookv1.HookDecision) *hookv1.DispatchHookResponse {
	return &hookv1.DispatchHookResponse{Outcome: &hookv1.DispatchHookResponse_Veto{
		Veto: &hookv1.DispatchHookResponse_VetoResult{Decision: decision},
	}}
}

// transformResponse builds a TransformResult carrying p.
func transformResponse(p *hookv1.HookPayload) *hookv1.DispatchHookResponse {
	return &hookv1.DispatchHookResponse{Outcome: &hookv1.DispatchHookResponse_Transform{
		Transform: &hookv1.DispatchHookResponse_TransformResult{Payload: p},
	}}
}

// recordingSink is an in-memory EventSink capturing every appended event
// in append order.
type recordingSink struct {
	mu     sync.Mutex
	events []statebackend.Event
	// err, when non-nil, is returned instead of appending.
	err error
}

// AppendEvent implements EventSink.
func (s *recordingSink) AppendEvent(_ context.Context, ev statebackend.Event) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return 0, s.err
	}
	s.events = append(s.events, ev)
	return int64(len(s.events)), nil
}

// snapshot returns a copy of every event appended so far.
func (s *recordingSink) snapshot() []statebackend.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]statebackend.Event(nil), s.events...)
}

// fakeVeto is a scripted KernelVeto standing in for the policy engine.
type fakeVeto struct {
	name     string
	decision hookv1.HookDecision
	err      error
	// block, when non-nil, is waited on (against ctx) before returning,
	// so a scenario can exercise the kernel veto's own deadline.
	block chan struct{}
}

// Name implements KernelVeto.
func (v *fakeVeto) Name() string { return v.name }

// Veto implements KernelVeto.
func (v *fakeVeto) Veto(ctx context.Context, _ *hookv1.HookPayload) (hookv1.HookDecision, error) {
	if v.block != nil {
		select {
		case <-v.block:
		case <-ctx.Done():
			return hookv1.HookDecision_HOOK_DECISION_UNSPECIFIED, ctx.Err()
		}
	}
	if v.err != nil {
		return hookv1.HookDecision_HOOK_DECISION_UNSPECIFIED, v.err
	}
	return v.decision, nil
}

// producerFor builds a distinguishable ProducerRef for a named provider.
func producerFor(name string) *commonv1.ProducerRef {
	return &commonv1.ProducerRef{
		Name:     name + "-plugin",
		Version:  "1.0.0",
		Category: commonv1.Category_CATEGORY_TOOL,
	}
}

// rangeAt builds an hcl.Range at a byte offset in a named file — the only
// two fields Position reads.
func rangeAt(filename string, byteStart int) hcl.Range {
	return hcl.Range{
		Filename: filename,
		Start:    hcl.Pos{Byte: byteStart},
		End:      hcl.Pos{Byte: byteStart + 1},
	}
}

// catalogEntry describes one provider to register in a fake catalog.
type catalogEntry struct {
	provider string
	points   []commonv1.HookPoint
	client   hookv1.HookSubscriberServiceClient
}

// newCatalog builds a fake providercatalog.Catalog from entries, giving
// any entry without an explicit client a default always-succeeding one.
func newCatalog(t *testing.T, entries ...catalogEntry) providercatalog.Catalog {
	t.Helper()

	cat := catalogfake.New()
	for _, e := range entries {
		client := e.client
		if client == nil {
			client = &fakeClient{}
		}
		cat.AddHook(e.provider, providercatalog.HookHandle{
			Producer:        producerFor(e.provider),
			Client:          client,
			SupportedPoints: e.points,
		})
	}
	return cat
}

// preModelCall builds a pre-model-call payload carrying one text message
// per body — the one point with a transform-mutable field.
func preModelCall(bodies ...string) *hookv1.HookPayload {
	msgs := make([]*contentv1.Message, 0, len(bodies))
	for _, body := range bodies {
		msgs = append(msgs, &contentv1.Message{
			Role: contentv1.Role_ROLE_USER,
			Content: []*contentv1.ContentBlock{{
				Block: &contentv1.ContentBlock_Text{Text: &contentv1.TextBlock{Text: body}},
			}},
		})
	}
	return &hookv1.HookPayload{Payload: &hookv1.HookPayload_PreModelCall{
		PreModelCall: &hookv1.PreModelCallPayload{
			Messages: msgs,
			Model:    &modelv1.ModelRef{Provider: "anthropic", Id: "claude-opus-4"},
		},
	}}
}

// messageTexts extracts the text of every message in a pre-model-call
// payload, for asserting what a transform chain produced.
func messageTexts(t *testing.T, p *hookv1.HookPayload) []string {
	t.Helper()

	msgs := p.GetPreModelCall().GetMessages()
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		for _, b := range m.GetContent() {
			out = append(out, b.GetText().GetText())
		}
	}
	return out
}

// planReady builds a plan-ready payload — a veto-bearing point.
func planReady() *hookv1.HookPayload {
	return &hookv1.HookPayload{Payload: &hookv1.HookPayload_PlanReady{
		PlanReady: &hookv1.PlanReadyPayload{},
	}}
}

// sessionStart builds a session-start payload — a non-veto-bearing point
// with no transform-mutable field.
func sessionStart() *hookv1.HookPayload {
	return &hookv1.HookPayload{Payload: &hookv1.HookPayload_SessionStart{
		SessionStart: &hookv1.SessionStartPayload{SessionId: "session-01"},
	}}
}

// fixedClock returns a clock func yielding a fixed instant, so a test
// never depends on wall-clock time (determinism.md).
func fixedClock() func() time.Time {
	at := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return at }
}

// errBoom is the generic subscriber failure tests script.
var errBoom = errors.New("boom")

// newDispatcher builds a Dispatcher over reg with a recording sink and
// quiet telemetry/logging defaults.
func newDispatcher(t *testing.T, reg *Registry, sink EventSink, opt Options) *Dispatcher {
	t.Helper()

	if opt.Clock == nil {
		opt.Clock = fixedClock()
	}
	return New(reg, sink, nil, nil, opt)
}
