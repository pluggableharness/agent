package hookdispatch

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/pluggableharness/agent/internal/config"
	"github.com/pluggableharness/agent/internal/statebackend"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	hookv1 "github.com/pluggableharness/agent/pkg/hook/proto/v1"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
)

// sub describes one subscription for buildRegistry.
type sub struct {
	provider  string
	mode      string
	client    *fakeClient
	timeoutMS *int
}

// buildRegistry wires subs into a Registry at point, positioned in the
// order given (one file, ascending byte offsets).
func buildRegistry(t *testing.T, point commonv1.HookPoint, subs ...sub) *Registry {
	t.Helper()

	text, ok := PointText(point)
	if !ok {
		t.Fatalf("PointText(%v) reported not ok", point)
	}

	entries := make([]catalogEntry, 0, len(subs))
	hooks := make([]config.Hook, 0, len(subs))
	for i, s := range subs {
		entries = append(entries, catalogEntry{provider: s.provider, points: []commonv1.HookPoint{point}, client: s.client})
		hooks = append(hooks, config.Hook{
			Point:     text,
			Provider:  s.provider,
			Mode:      s.mode,
			TimeoutMS: s.timeoutMS,
			Range:     rangeAt("agent.hcl", (i+1)*100),
		})
	}

	reg, err := NewRegistry(newCatalog(t, entries...), nil, hooks, nil, testDefaultTimeout)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return reg
}

func TestDispatchNoSubscribers(t *testing.T) {
	t.Parallel()

	reg := buildRegistry(t, commonv1.HookPoint_HOOK_POINT_SESSION_START)
	sink := &recordingSink{}
	d := newDispatcher(t, reg, sink, Options{})

	in := sessionStart()
	out, err := d.Dispatch(context.Background(), in)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if out.Decision != hookv1.HookDecision_HOOK_DECISION_ALLOW {
		t.Errorf("decision = %v, want ALLOW", out.Decision)
	}
	if out.Payload != in {
		t.Error("Dispatch returned a different payload than it was given")
	}
	if len(sink.snapshot()) != 0 {
		t.Errorf("sink recorded %d events, want 0", len(sink.snapshot()))
	}
}

func TestDispatchNoHookPoint(t *testing.T) {
	t.Parallel()

	reg := buildRegistry(t, commonv1.HookPoint_HOOK_POINT_SESSION_START)
	d := newDispatcher(t, reg, &recordingSink{}, Options{})

	if _, err := d.Dispatch(context.Background(), &hookv1.HookPayload{}); !errors.Is(err, ErrNoHookPoint) {
		t.Fatalf("Dispatch error = %v, want ErrNoHookPoint", err)
	}
	if _, err := d.Dispatch(context.Background(), nil); !errors.Is(err, ErrNoHookPoint) {
		t.Fatalf("Dispatch(nil) error = %v, want ErrNoHookPoint", err)
	}
}

func TestDispatchObserveFailureContinuesChain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		respond   func(ctx context.Context, req *hookv1.DispatchHookRequest) (*hookv1.DispatchHookResponse, error)
		timeoutMS *int
		wantCat   hookv1.HookErrorCategory
	}{
		{
			name: "rpc error",
			respond: func(context.Context, *hookv1.DispatchHookRequest) (*hookv1.DispatchHookResponse, error) {
				return nil, errBoom
			},
			wantCat: hookv1.HookErrorCategory_HOOK_ERROR_CATEGORY_UNKNOWN,
		},
		{
			name: "timeout",
			respond: func(ctx context.Context, _ *hookv1.DispatchHookRequest) (*hookv1.DispatchHookResponse, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
			timeoutMS: msPtr(20),
			wantCat:   hookv1.HookErrorCategory_HOOK_ERROR_CATEGORY_TIMEOUT,
		},
		{
			name: "invalid response shape",
			respond: func(context.Context, *hookv1.DispatchHookRequest) (*hookv1.DispatchHookResponse, error) {
				// An observe subscriber returning a VetoResult is
				// HOOK_ERROR_CATEGORY_INVALID_RESPONSE.
				return vetoResponse(hookv1.HookDecision_HOOK_DECISION_DENY), nil
			},
			wantCat: hookv1.HookErrorCategory_HOOK_ERROR_CATEGORY_INVALID_RESPONSE,
		},
		{
			name: "plugin crash",
			respond: func(context.Context, *hookv1.DispatchHookRequest) (*hookv1.DispatchHookResponse, error) {
				return nil, status.Error(codes.Unavailable, "plugin exited")
			},
			wantCat: hookv1.HookErrorCategory_HOOK_ERROR_CATEGORY_PROCESS_CRASHED,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			broken := &fakeClient{respond: tt.respond}
			healthy := &fakeClient{}
			reg := buildRegistry(t, commonv1.HookPoint_HOOK_POINT_POST_TOOL_CALL,
				sub{provider: "broken", mode: "observe", client: broken, timeoutMS: tt.timeoutMS},
				sub{provider: "healthy", mode: "observe", client: healthy},
			)
			sink := &recordingSink{}
			d := newDispatcher(t, reg, sink, Options{})

			in := &hookv1.HookPayload{Payload: &hookv1.HookPayload_PostToolCall{
				PostToolCall: &hookv1.PostToolCallPayload{},
			}}
			out, err := d.Dispatch(context.Background(), in)
			if err != nil {
				t.Fatalf("Dispatch: %v", err)
			}

			// A broken logger must not break the loop
			// (hook-dispatch.md#subscriber-error-handling).
			if healthy.callCount() != 1 {
				t.Errorf("healthy subscriber calls = %d, want 1 (chain must continue)", healthy.callCount())
			}
			if out.Decision != hookv1.HookDecision_HOOK_DECISION_ALLOW {
				t.Errorf("decision = %v, want ALLOW", out.Decision)
			}
			if out.Payload != in {
				t.Error("observe mode altered the payload")
			}

			events := sink.snapshot()
			if len(events) != 1 {
				t.Fatalf("persisted %d hook_error events, want 1", len(events))
			}
			assertHookError(t, events[0], hookErrorWant{
				producer: "broken-plugin",
				point:    commonv1.HookPoint_HOOK_POINT_POST_TOOL_CALL,
				mode:     hookv1.HookMode_HOOK_MODE_OBSERVE,
				category: tt.wantCat,
			})
		})
	}
}

func TestDispatchObserveResponsePayloadDiscarded(t *testing.T) {
	t.Parallel()

	// An observe subscriber's payload is discarded even when one comes
	// back — observe mode can never alter the chain.
	noisy := &fakeClient{respond: func(context.Context, *hookv1.DispatchHookRequest) (*hookv1.DispatchHookResponse, error) {
		return okResponse(hookv1.HookMode_HOOK_MODE_OBSERVE, preModelCall("rewritten")), nil
	}}
	reg := buildRegistry(t, commonv1.HookPoint_HOOK_POINT_PRE_MODEL_CALL,
		sub{provider: "noisy", mode: "observe", client: noisy})
	d := newDispatcher(t, reg, &recordingSink{}, Options{})

	out, err := d.Dispatch(context.Background(), preModelCall("original"))
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if got := messageTexts(t, out.Payload); !reflect.DeepEqual(got, []string{"original"}) {
		t.Errorf("messages = %v, want [original]", got)
	}
}

func TestDispatchTransformMutatesMessages(t *testing.T) {
	t.Parallel()

	// pre-model-call's messages is the one transform-mutable field in v1
	// (hook-dispatch.md#per-point-transform-mutable-fields). Two transform
	// subscribers chain: each sees the prior one's output.
	appender := func(text string) *fakeClient {
		return &fakeClient{respond: func(_ context.Context, req *hookv1.DispatchHookRequest) (*hookv1.DispatchHookResponse, error) {
			in := req.GetPayload().GetPreModelCall()
			bodies := make([]string, 0, len(in.GetMessages())+1)
			for _, m := range in.GetMessages() {
				bodies = append(bodies, m.GetContent()[0].GetText().GetText())
			}
			bodies = append(bodies, text)
			return transformResponse(preModelCall(bodies...)), nil
		}}
	}

	last := &fakeClient{}
	reg := buildRegistry(t, commonv1.HookPoint_HOOK_POINT_PRE_MODEL_CALL,
		sub{provider: "first", mode: "transform", client: appender("from-first")},
		sub{provider: "second", mode: "transform", client: appender("from-second")},
		sub{provider: "watcher", mode: "observe", client: last},
	)
	sink := &recordingSink{}
	d := newDispatcher(t, reg, sink, Options{})

	out, err := d.Dispatch(context.Background(), preModelCall("original"))
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	want := []string{"original", "from-first", "from-second"}
	if got := messageTexts(t, out.Payload); !reflect.DeepEqual(got, want) {
		t.Errorf("messages = %v, want %v", got, want)
	}
	// The trailing observe subscriber sees the fully-transformed payload.
	if got := messageTexts(t, last.calls[0].GetPayload()); !reflect.DeepEqual(got, want) {
		t.Errorf("observe subscriber saw %v, want %v", got, want)
	}
	if len(sink.snapshot()) != 0 {
		t.Errorf("persisted %d hook_error events, want 0", len(sink.snapshot()))
	}
}

func TestDispatchTransformFailureAbortsChain(t *testing.T) {
	t.Parallel()

	immutableModel := func(_ context.Context, _ *hookv1.DispatchHookRequest) (*hookv1.DispatchHookResponse, error) {
		p := preModelCall("original")
		// model is immutable at pre-model-call — a subscriber does not
		// get to silently reroute the turn.
		p.GetPreModelCall().Model.Id = "some-cheaper-model"
		return transformResponse(p), nil
	}

	tests := []struct {
		name      string
		respond   func(ctx context.Context, req *hookv1.DispatchHookRequest) (*hookv1.DispatchHookResponse, error)
		timeoutMS *int
		wantCat   hookv1.HookErrorCategory
	}{
		{
			name: "rpc error",
			respond: func(context.Context, *hookv1.DispatchHookRequest) (*hookv1.DispatchHookResponse, error) {
				return nil, errBoom
			},
			wantCat: hookv1.HookErrorCategory_HOOK_ERROR_CATEGORY_TRANSFORM_FAILED,
		},
		{
			name: "timeout",
			respond: func(ctx context.Context, _ *hookv1.DispatchHookRequest) (*hookv1.DispatchHookResponse, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
			timeoutMS: msPtr(20),
			wantCat:   hookv1.HookErrorCategory_HOOK_ERROR_CATEGORY_TIMEOUT,
		},
		{
			name:    "mutating an immutable field",
			respond: immutableModel,
			wantCat: hookv1.HookErrorCategory_HOOK_ERROR_CATEGORY_INVALID_RESPONSE,
		},
		{
			name: "wrong response variant",
			respond: func(context.Context, *hookv1.DispatchHookRequest) (*hookv1.DispatchHookResponse, error) {
				return okResponse(hookv1.HookMode_HOOK_MODE_OBSERVE, nil), nil
			},
			wantCat: hookv1.HookErrorCategory_HOOK_ERROR_CATEGORY_INVALID_RESPONSE,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			downstream := &fakeClient{}
			reg := buildRegistry(t, commonv1.HookPoint_HOOK_POINT_PRE_MODEL_CALL,
				sub{provider: "broken", mode: "transform", client: &fakeClient{respond: tt.respond}, timeoutMS: tt.timeoutMS},
				sub{provider: "downstream", mode: "observe", client: downstream},
			)
			sink := &recordingSink{}
			d := newDispatcher(t, reg, sink, Options{})

			out, err := d.Dispatch(context.Background(), preModelCall("original"))
			if !errors.Is(err, ErrTransformFailed) {
				t.Fatalf("Dispatch error = %v, want ErrTransformFailed", err)
			}
			// The kernel MUST NOT fall back to the pre-transform payload
			// (hook-dispatch.md#subscriber-error-handling).
			if out.Payload != nil {
				t.Error("Dispatch returned a payload alongside ErrTransformFailed")
			}
			if downstream.callCount() != 0 {
				t.Errorf("downstream subscriber calls = %d, want 0 (chain must abort)", downstream.callCount())
			}

			events := sink.snapshot()
			if len(events) != 1 {
				t.Fatalf("persisted %d hook_error events, want 1", len(events))
			}
			assertHookError(t, events[0], hookErrorWant{
				producer: "broken-plugin",
				point:    commonv1.HookPoint_HOOK_POINT_PRE_MODEL_CALL,
				mode:     hookv1.HookMode_HOOK_MODE_TRANSFORM,
				category: tt.wantCat,
			})
		})
	}
}

func TestDispatchTransformAtImmutablePoint(t *testing.T) {
	t.Parallel()

	// At a point with no mutable field, a transform subscriber MUST return
	// the payload byte-identical; an identity response succeeds and any
	// diff is rejected.
	identity := &fakeClient{}
	reg := buildRegistry(t, commonv1.HookPoint_HOOK_POINT_SESSION_START,
		sub{provider: "identity", mode: "transform", client: identity})
	d := newDispatcher(t, reg, &recordingSink{}, Options{})

	if _, err := d.Dispatch(context.Background(), sessionStart()); err != nil {
		t.Fatalf("identity transform at an immutable point: %v", err)
	}

	mutating := &fakeClient{respond: func(context.Context, *hookv1.DispatchHookRequest) (*hookv1.DispatchHookResponse, error) {
		return transformResponse(&hookv1.HookPayload{Payload: &hookv1.HookPayload_SessionStart{
			SessionStart: &hookv1.SessionStartPayload{SessionId: "session-99"},
		}}), nil
	}}
	reg = buildRegistry(t, commonv1.HookPoint_HOOK_POINT_SESSION_START,
		sub{provider: "mutating", mode: "transform", client: mutating})
	d = newDispatcher(t, reg, &recordingSink{}, Options{})

	if _, err := d.Dispatch(context.Background(), sessionStart()); !errors.Is(err, ErrTransformFailed) {
		t.Fatalf("Dispatch error = %v, want ErrTransformFailed", err)
	}
}

func TestDispatchVetoExplicitDenyShortCircuits(t *testing.T) {
	t.Parallel()

	denier := &fakeClient{respond: func(context.Context, *hookv1.DispatchHookRequest) (*hookv1.DispatchHookResponse, error) {
		return vetoResponse(hookv1.HookDecision_HOOK_DECISION_DENY), nil
	}}
	downstreamVeto := &fakeClient{}
	downstreamObserve := &fakeClient{}

	reg := buildRegistry(t, commonv1.HookPoint_HOOK_POINT_PLAN_READY,
		sub{provider: "allower", mode: "veto", client: &fakeClient{}},
		sub{provider: "denier", mode: "veto", client: denier},
		sub{provider: "expensive", mode: "veto", client: downstreamVeto},
		sub{provider: "watcher", mode: "observe", client: downstreamObserve},
	)
	sink := &recordingSink{}
	d := newDispatcher(t, reg, sink, Options{})

	out, err := d.Dispatch(context.Background(), planReady())
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if out.Decision != hookv1.HookDecision_HOOK_DECISION_DENY {
		t.Errorf("decision = %v, want DENY", out.Decision)
	}
	if out.DeniedBy != "denier" {
		t.Errorf("DeniedBy = %q, want %q", out.DeniedBy, "denier")
	}
	if downstreamVeto.callCount() != 0 || downstreamObserve.callCount() != 0 {
		t.Error("an explicit non-allow decision did not short-circuit the remaining subscribers")
	}
	// An explicit deny is a considered verdict, not a failure — nothing to
	// persist.
	if len(sink.snapshot()) != 0 {
		t.Errorf("persisted %d hook_error events, want 0", len(sink.snapshot()))
	}
}

func TestDispatchVetoFailureFailsClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		respond   func(ctx context.Context, req *hookv1.DispatchHookRequest) (*hookv1.DispatchHookResponse, error)
		timeoutMS *int
		wantCat   hookv1.HookErrorCategory
	}{
		{
			name: "rpc error",
			respond: func(context.Context, *hookv1.DispatchHookRequest) (*hookv1.DispatchHookResponse, error) {
				return nil, errBoom
			},
			wantCat: hookv1.HookErrorCategory_HOOK_ERROR_CATEGORY_VETO_FAILED,
		},
		{
			name: "timeout",
			respond: func(ctx context.Context, _ *hookv1.DispatchHookRequest) (*hookv1.DispatchHookResponse, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
			timeoutMS: msPtr(20),
			wantCat:   hookv1.HookErrorCategory_HOOK_ERROR_CATEGORY_TIMEOUT,
		},
		{
			name: "unspecified decision",
			respond: func(context.Context, *hookv1.DispatchHookRequest) (*hookv1.DispatchHookResponse, error) {
				// Not an implicit allow or deny — an invalid response.
				return vetoResponse(hookv1.HookDecision_HOOK_DECISION_UNSPECIFIED), nil
			},
			wantCat: hookv1.HookErrorCategory_HOOK_ERROR_CATEGORY_INVALID_RESPONSE,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			downstream := &fakeClient{}
			reg := buildRegistry(t, commonv1.HookPoint_HOOK_POINT_PLAN_READY,
				sub{provider: "broken", mode: "veto", client: &fakeClient{respond: tt.respond}, timeoutMS: tt.timeoutMS},
				sub{provider: "downstream", mode: "observe", client: downstream},
			)
			sink := &recordingSink{}
			d := newDispatcher(t, reg, sink, Options{})

			out, err := d.Dispatch(context.Background(), planReady())
			if err != nil {
				t.Fatalf("Dispatch: %v", err)
			}
			if out.Decision != hookv1.HookDecision_HOOK_DECISION_DENY {
				t.Errorf("decision = %v, want DENY (fail-closed)", out.Decision)
			}
			if out.DeniedBy != "broken" {
				t.Errorf("DeniedBy = %q, want %q", out.DeniedBy, "broken")
			}
			if downstream.callCount() != 0 {
				t.Errorf("downstream subscriber calls = %d, want 0", downstream.callCount())
			}

			events := sink.snapshot()
			if len(events) != 1 {
				t.Fatalf("persisted %d hook_error events, want 1", len(events))
			}
			assertHookError(t, events[0], hookErrorWant{
				producer: "broken-plugin",
				point:    commonv1.HookPoint_HOOK_POINT_PLAN_READY,
				mode:     hookv1.HookMode_HOOK_MODE_VETO,
				category: tt.wantCat,
			})
		})
	}
}

func TestDispatchParentCancellationIsNotADeny(t *testing.T) {
	t.Parallel()

	// A subscriber's OWN deadline firing fails closed to DENY. The parent
	// ctx being canceled — the whole turn being torn down — must not be
	// manufactured into a DENY, which would persist a decision for a turn
	// that is being abandoned anyway.
	entered := make(chan struct{})
	blocking := &fakeClient{respond: func(ctx context.Context, _ *hookv1.DispatchHookRequest) (*hookv1.DispatchHookResponse, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}}

	// A generous per-subscriber timeout, so the only thing that can end
	// the call is the parent cancellation.
	reg := buildRegistry(t, commonv1.HookPoint_HOOK_POINT_PLAN_READY,
		sub{provider: "slow", mode: "veto", client: blocking, timeoutMS: msPtr(60_000)})
	sink := &recordingSink{}
	d := newDispatcher(t, reg, sink, Options{})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	type result struct {
		out Outcome
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := d.Dispatch(ctx, planReady())
		done <- result{out: out, err: err}
	}()

	<-entered
	cancel()

	select {
	case got := <-done:
		if !errors.Is(got.err, context.Canceled) {
			t.Fatalf("Dispatch error = %v, want errors.Is context.Canceled", got.err)
		}
		if got.out.Decision == hookv1.HookDecision_HOOK_DECISION_DENY {
			t.Error("a parent-canceled dispatch was manufactured into a DENY")
		}
		if got.out.DeniedBy != "" {
			t.Errorf("DeniedBy = %q, want empty", got.out.DeniedBy)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Dispatch did not return after the parent context was canceled")
	}

	// Nothing is persisted for an abandoned turn.
	if len(sink.snapshot()) != 0 {
		t.Errorf("persisted %d hook_error events, want 0", len(sink.snapshot()))
	}
}

func TestDispatchAlreadyCanceledParent(t *testing.T) {
	t.Parallel()

	client := &fakeClient{}
	reg := buildRegistry(t, commonv1.HookPoint_HOOK_POINT_PLAN_READY,
		sub{provider: "gate", mode: "veto", client: client})
	d := newDispatcher(t, reg, &recordingSink{}, Options{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	out, err := d.Dispatch(ctx, planReady())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Dispatch error = %v, want errors.Is context.Canceled", err)
	}
	if out.Decision == hookv1.HookDecision_HOOK_DECISION_DENY {
		t.Error("an already-canceled dispatch was manufactured into a DENY")
	}
	if client.callCount() != 0 {
		t.Errorf("subscriber calls = %d, want 0", client.callCount())
	}
}

func TestDispatchKernelVetoRunsFirst(t *testing.T) {
	t.Parallel()

	plugin := &fakeClient{}
	reg := buildRegistry(t, commonv1.HookPoint_HOOK_POINT_PLAN_READY,
		sub{provider: "third-party", mode: "veto", client: plugin})
	reg.Pin(commonv1.HookPoint_HOOK_POINT_PLAN_READY, &fakeVeto{
		name:     "policy",
		decision: hookv1.HookDecision_HOOK_DECISION_DENY,
	})
	sink := &recordingSink{}
	d := newDispatcher(t, reg, sink, Options{})

	out, err := d.Dispatch(context.Background(), planReady())
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if out.Decision != hookv1.HookDecision_HOOK_DECISION_DENY {
		t.Errorf("decision = %v, want DENY", out.Decision)
	}
	if out.DeniedBy != "policy" {
		t.Errorf("DeniedBy = %q, want %q", out.DeniedBy, "policy")
	}
	// The trust model's guarantee: a third-party veto subscriber cannot
	// override a DENY policy has already produced, because policy ran
	// first and short-circuited the chain.
	if plugin.callCount() != 0 {
		t.Errorf("third-party veto calls = %d, want 0", plugin.callCount())
	}
}

func TestDispatchKernelVetoAllowsChainToContinue(t *testing.T) {
	t.Parallel()

	plugin := &fakeClient{}
	reg := buildRegistry(t, commonv1.HookPoint_HOOK_POINT_PLAN_READY,
		sub{provider: "third-party", mode: "veto", client: plugin})
	reg.Pin(commonv1.HookPoint_HOOK_POINT_PLAN_READY, &fakeVeto{
		name:     "policy",
		decision: hookv1.HookDecision_HOOK_DECISION_ALLOW,
	})
	d := newDispatcher(t, reg, &recordingSink{}, Options{})

	out, err := d.Dispatch(context.Background(), planReady())
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if out.Decision != hookv1.HookDecision_HOOK_DECISION_ALLOW {
		t.Errorf("decision = %v, want ALLOW", out.Decision)
	}
	if plugin.callCount() != 1 {
		t.Errorf("third-party veto calls = %d, want 1", plugin.callCount())
	}
}

func TestDispatchKernelVetoFailsClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		veto *fakeVeto
	}{
		{name: "error", veto: &fakeVeto{name: "policy", err: errBoom}},
		{name: "timeout", veto: &fakeVeto{name: "policy", block: make(chan struct{})}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			plugin := &fakeClient{}
			cat := newCatalog(t, catalogEntry{
				provider: "third-party",
				points:   []commonv1.HookPoint{commonv1.HookPoint_HOOK_POINT_PLAN_READY},
				client:   plugin,
			})
			hooks := []config.Hook{{Point: "plan-ready", Provider: "third-party", Mode: "veto", Range: rangeAt("agent.hcl", 10)}}

			// A short default timeout so the blocking veto's own deadline
			// fires quickly.
			reg, err := NewRegistry(cat, nil, hooks, nil, 20*time.Millisecond)
			if err != nil {
				t.Fatalf("NewRegistry: %v", err)
			}
			reg.Pin(commonv1.HookPoint_HOOK_POINT_PLAN_READY, tt.veto)

			sink := &recordingSink{}
			d := newDispatcher(t, reg, sink, Options{})

			out, err := d.Dispatch(context.Background(), planReady())
			if err != nil {
				t.Fatalf("Dispatch: %v", err)
			}
			if out.Decision != hookv1.HookDecision_HOOK_DECISION_DENY {
				t.Errorf("decision = %v, want DENY (fail-closed)", out.Decision)
			}
			if out.DeniedBy != "policy" {
				t.Errorf("DeniedBy = %q, want %q", out.DeniedBy, "policy")
			}
			if plugin.callCount() != 0 {
				t.Errorf("third-party veto calls = %d, want 0", plugin.callCount())
			}
			// A kernel veto is not a plugin: it has no ProducerRef, and
			// state-backend.md attributes hook_error to the failing
			// subscriber — so nothing is persisted for it.
			if len(sink.snapshot()) != 0 {
				t.Errorf("persisted %d hook_error events for a kernel veto, want 0", len(sink.snapshot()))
			}
		})
	}
}

func TestDispatchKernelVetoParentCancellation(t *testing.T) {
	t.Parallel()

	reg := buildRegistry(t, commonv1.HookPoint_HOOK_POINT_PLAN_READY)
	blocked := make(chan struct{})
	t.Cleanup(func() { close(blocked) })
	reg.Pin(commonv1.HookPoint_HOOK_POINT_PLAN_READY, &fakeVeto{name: "policy", block: blocked})
	d := newDispatcher(t, reg, &recordingSink{}, Options{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	out, err := d.Dispatch(ctx, planReady())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Dispatch error = %v, want errors.Is context.Canceled", err)
	}
	if out.Decision == hookv1.HookDecision_HOOK_DECISION_DENY {
		t.Error("a parent-canceled kernel veto was manufactured into a DENY")
	}
}

func TestDispatchConcurrentObserveOverlaps(t *testing.T) {
	t.Parallel()

	const observers = 3

	arrived := make(chan struct{}, observers)
	release := make(chan struct{})
	blockingObserver := func() *fakeClient {
		return &fakeClient{respond: func(ctx context.Context, req *hookv1.DispatchHookRequest) (*hookv1.DispatchHookResponse, error) {
			arrived <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return okResponse(req.GetMode(), req.GetPayload()), nil
		}}
	}

	reg := buildRegistry(t, commonv1.HookPoint_HOOK_POINT_POST_TOOL_CALL,
		sub{provider: "one", mode: "observe", client: blockingObserver()},
		sub{provider: "two", mode: "observe", client: blockingObserver()},
		sub{provider: "three", mode: "observe", client: blockingObserver()},
	)
	d := newDispatcher(t, reg, &recordingSink{}, Options{ConcurrentObserve: true})

	done := make(chan error, 1)
	go func() {
		_, err := d.Dispatch(context.Background(), &hookv1.HookPayload{Payload: &hookv1.HookPayload_PostToolCall{
			PostToolCall: &hookv1.PostToolCallPayload{},
		}})
		done <- err
	}()

	// All three must be in flight simultaneously: none of them can return
	// until release is closed, so a sequential dispatcher would never
	// deliver the second arrival.
	for i := range observers {
		select {
		case <-arrived:
		case <-time.After(5 * time.Second):
			close(release)
			t.Fatalf("only %d of %d observe subscribers ran concurrently", i, observers)
		}
	}
	close(release)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Dispatch: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Dispatch did not return after the observe run was released")
	}
}

func TestDispatchConcurrentObserveDoesNotReorder(t *testing.T) {
	t.Parallel()

	// An observe subscriber declared between two transform subscribers
	// still sees exactly the payload state as of that point in the chain,
	// and the transform subscribers on either side are unaffected.
	var mu sync.Mutex
	var order []string
	record := func(name string) {
		mu.Lock()
		order = append(order, name)
		mu.Unlock()
	}

	observer := func(name string) *fakeClient {
		return &fakeClient{respond: func(_ context.Context, req *hookv1.DispatchHookRequest) (*hookv1.DispatchHookResponse, error) {
			record(name + ":" + lastText(req.GetPayload()))
			return okResponse(req.GetMode(), req.GetPayload()), nil
		}}
	}
	transformer := func(name, add string) *fakeClient {
		return &fakeClient{respond: func(_ context.Context, req *hookv1.DispatchHookRequest) (*hookv1.DispatchHookResponse, error) {
			record(name)
			in := req.GetPayload().GetPreModelCall()
			bodies := make([]string, 0, len(in.GetMessages())+1)
			for _, m := range in.GetMessages() {
				bodies = append(bodies, m.GetContent()[0].GetText().GetText())
			}
			bodies = append(bodies, add)
			return transformResponse(preModelCall(bodies...)), nil
		}}
	}

	reg := buildRegistry(t, commonv1.HookPoint_HOOK_POINT_PRE_MODEL_CALL,
		sub{provider: "obs-a", mode: "observe", client: observer("obs-a")},
		sub{provider: "xf-1", mode: "transform", client: transformer("xf-1", "one")},
		sub{provider: "obs-b", mode: "observe", client: observer("obs-b")},
		sub{provider: "xf-2", mode: "transform", client: transformer("xf-2", "two")},
		sub{provider: "obs-c", mode: "observe", client: observer("obs-c")},
	)
	d := newDispatcher(t, reg, &recordingSink{}, Options{ConcurrentObserve: true})

	out, err := d.Dispatch(context.Background(), preModelCall("original"))
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	want := []string{"obs-a:original", "xf-1", "obs-b:one", "xf-2", "obs-c:two"}
	mu.Lock()
	got := append([]string(nil), order...)
	mu.Unlock()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("call order = %v, want %v", got, want)
	}

	wantMessages := []string{"original", "one", "two"}
	if msgs := messageTexts(t, out.Payload); !reflect.DeepEqual(msgs, wantMessages) {
		t.Errorf("messages = %v, want %v", msgs, wantMessages)
	}
}

func TestDispatchConcurrentObserveFailuresPersistInDeclarationOrder(t *testing.T) {
	t.Parallel()

	// A concurrent run's calls interleave, but their hook_error events are
	// persisted in declaration order so replay stays deterministic
	// (determinism.md).
	failing := func(delay time.Duration) *fakeClient {
		return &fakeClient{respond: func(context.Context, *hookv1.DispatchHookRequest) (*hookv1.DispatchHookResponse, error) {
			time.Sleep(delay)
			return nil, errBoom
		}}
	}

	reg := buildRegistry(t, commonv1.HookPoint_HOOK_POINT_POST_TOOL_CALL,
		// The first-declared subscriber finishes last.
		sub{provider: "first", mode: "observe", client: failing(30 * time.Millisecond)},
		sub{provider: "second", mode: "observe", client: failing(0)},
	)
	sink := &recordingSink{}
	d := newDispatcher(t, reg, sink, Options{ConcurrentObserve: true})

	if _, err := d.Dispatch(context.Background(), &hookv1.HookPayload{Payload: &hookv1.HookPayload_PostToolCall{
		PostToolCall: &hookv1.PostToolCallPayload{},
	}}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	events := sink.snapshot()
	if len(events) != 2 {
		t.Fatalf("persisted %d hook_error events, want 2", len(events))
	}
	want := []string{"first-plugin", "second-plugin"}
	for i, name := range want {
		if got := events[i].Producer.GetName(); got != name {
			t.Errorf("event[%d] producer = %q, want %q", i, got, name)
		}
	}
}

func TestDispatchWithoutEventSink(t *testing.T) {
	t.Parallel()

	// A caller with no live session has nowhere to append to; failures are
	// still counted and logged, and the chain behaves identically.
	broken := &fakeClient{respond: func(context.Context, *hookv1.DispatchHookRequest) (*hookv1.DispatchHookResponse, error) {
		return nil, errBoom
	}}
	healthy := &fakeClient{}
	reg := buildRegistry(t, commonv1.HookPoint_HOOK_POINT_POST_TOOL_CALL,
		sub{provider: "broken", mode: "observe", client: broken},
		sub{provider: "healthy", mode: "observe", client: healthy},
	)
	d := newDispatcher(t, reg, nil, Options{})

	if _, err := d.Dispatch(context.Background(), &hookv1.HookPayload{Payload: &hookv1.HookPayload_PostToolCall{
		PostToolCall: &hookv1.PostToolCallPayload{},
	}}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if healthy.callCount() != 1 {
		t.Errorf("healthy subscriber calls = %d, want 1", healthy.callCount())
	}
}

func TestDispatchEventSinkFailureIsNotFatal(t *testing.T) {
	t.Parallel()

	// A failed hook_error append cannot change the dispatch outcome — the
	// outcome is already decided by the time it is persisted.
	broken := &fakeClient{respond: func(context.Context, *hookv1.DispatchHookRequest) (*hookv1.DispatchHookResponse, error) {
		return nil, errBoom
	}}
	healthy := &fakeClient{}
	reg := buildRegistry(t, commonv1.HookPoint_HOOK_POINT_POST_TOOL_CALL,
		sub{provider: "broken", mode: "observe", client: broken},
		sub{provider: "healthy", mode: "observe", client: healthy},
	)
	d := newDispatcher(t, reg, &recordingSink{err: errBoom}, Options{})

	if _, err := d.Dispatch(context.Background(), &hookv1.HookPayload{Payload: &hookv1.HookPayload_PostToolCall{
		PostToolCall: &hookv1.PostToolCallPayload{},
	}}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if healthy.callCount() != 1 {
		t.Errorf("healthy subscriber calls = %d, want 1", healthy.callCount())
	}
}

func TestDispatchHookErrorIsNotAttributedToTheKernel(t *testing.T) {
	t.Parallel()

	broken := &fakeClient{respond: func(context.Context, *hookv1.DispatchHookRequest) (*hookv1.DispatchHookResponse, error) {
		return nil, errBoom
	}}
	reg := buildRegistry(t, commonv1.HookPoint_HOOK_POINT_POST_TOOL_CALL,
		sub{provider: "broken", mode: "observe", client: broken})
	sink := &recordingSink{}
	d := newDispatcher(t, reg, sink, Options{})

	if _, err := d.Dispatch(context.Background(), &hookv1.HookPayload{Payload: &hookv1.HookPayload_PostToolCall{
		PostToolCall: &hookv1.PostToolCallPayload{},
	}}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("persisted %d events, want 1", len(events))
	}
	// state-backend.md#the-kind-enum: hook_error, though
	// kernel-synthesized, attributes the FAILING SUBSCRIBER as producer —
	// statebackend's own encodeProducer rejects the reserved kernel
	// producer on this kind.
	if statebackend.IsKernelProducer(events[0].Producer) {
		t.Error("hook_error was attributed to the kernel producer")
	}
	if events[0].Kind != kernelv1.EventKind_EVENT_KIND_HOOK_ERROR {
		t.Errorf("event kind = %v, want EVENT_KIND_HOOK_ERROR", events[0].Kind)
	}
	if events[0].ID == "" {
		t.Error("hook_error event has no id")
	}
	if events[0].SchemaVersion != hookErrorSchemaVersion {
		t.Errorf("schema version = %q, want %q", events[0].SchemaVersion, hookErrorSchemaVersion)
	}
}

// hookErrorWant is the expected content of one persisted hook_error.
type hookErrorWant struct {
	producer string
	point    commonv1.HookPoint
	mode     hookv1.HookMode
	category hookv1.HookErrorCategory
}

// assertHookError checks one persisted event against want, decoding its
// HookError payload.
func assertHookError(t *testing.T, ev statebackend.Event, want hookErrorWant) {
	t.Helper()

	if ev.Kind != kernelv1.EventKind_EVENT_KIND_HOOK_ERROR {
		t.Errorf("event kind = %v, want EVENT_KIND_HOOK_ERROR", ev.Kind)
	}
	if got := ev.Producer.GetName(); got != want.producer {
		t.Errorf("event producer = %q, want %q (the failing subscriber)", got, want.producer)
	}

	var detail hookv1.HookError
	if err := proto.Unmarshal(ev.Payload, &detail); err != nil {
		t.Fatalf("unmarshaling HookError payload: %v", err)
	}
	if detail.GetPoint() != want.point {
		t.Errorf("HookError point = %v, want %v", detail.GetPoint(), want.point)
	}
	if detail.GetMode() != want.mode {
		t.Errorf("HookError mode = %v, want %v", detail.GetMode(), want.mode)
	}
	if detail.GetCategory() != want.category {
		t.Errorf("HookError category = %v, want %v", detail.GetCategory(), want.category)
	}
	if detail.GetSubscriber().GetName() != want.producer {
		t.Errorf("HookError subscriber = %q, want %q", detail.GetSubscriber().GetName(), want.producer)
	}
	if detail.GetMessage() == "" {
		t.Error("HookError carries no message")
	}
}

// lastText returns the text of the last message in a pre-model-call
// payload — a compact way for a test observer to record what payload
// state it saw.
func lastText(p *hookv1.HookPayload) string {
	msgs := p.GetPreModelCall().GetMessages()
	if len(msgs) == 0 {
		return ""
	}
	return msgs[len(msgs)-1].GetContent()[0].GetText().GetText()
}

// msPtr returns a pointer to ms, for config.Hook's *int TimeoutMS.
func msPtr(ms int) *int { return &ms }
