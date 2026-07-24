package hook_test

import (
	"context"
	"errors"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	"github.com/pluggableharness/agent/pkg/hook"
	hookv1 "github.com/pluggableharness/agent/pkg/hook/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
	planv1 "github.com/pluggableharness/agent/pkg/plan/proto/v1"
)

// newTestClient starts svc on an in-memory bufconn listener and returns a
// hookv1.HookSubscriberServiceClient dialed against it — a real gRPC round
// trip, mirroring pkg/kernel/helpers_test.go's newTestClient, so these
// tests exercise the actual wire marshaling hook.Service's adapter
// produces rather than calling its methods directly in-process.
func newTestClient(t *testing.T, svc *hook.Service) hookv1.HookSubscriberServiceClient {
	t.Helper()

	const bufSize = 1 << 20
	lis := bufconn.Listen(bufSize)

	gs := grpc.NewServer()
	svc.Register(gs)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	dialer := func(context.Context, string) (net.Conn, error) { return lis.Dial() }
	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithContextDialer(dialer), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return hookv1.NewHookSubscriberServiceClient(conn)
}

// fakeSubscriber is a hand-written Observer/Transformer/Vetoer fake
// (go-testing.md: fakes, not mocking frameworks). Each facet's behavior is
// controlled by a caller-set func field; a nil field means "call should
// not reach this facet" for that test.
type fakeSubscriber struct {
	observeFunc   func(ctx context.Context, payload *hook.Payload) error
	transformFunc func(ctx context.Context, payload *hook.Payload) (*hook.Payload, error)
	vetoFunc      func(ctx context.Context, payload *hook.Payload) (hook.Decision, error)
}

func (f *fakeSubscriber) Observe(ctx context.Context, payload *hook.Payload) error {
	return f.observeFunc(ctx, payload)
}

func (f *fakeSubscriber) Transform(ctx context.Context, payload *hook.Payload) (*hook.Payload, error) {
	return f.transformFunc(ctx, payload)
}

func (f *fakeSubscriber) Veto(ctx context.Context, payload *hook.Payload) (hook.Decision, error) {
	return f.vetoFunc(ctx, payload)
}

var (
	_ hook.Observer    = (*fakeSubscriber)(nil)
	_ hook.Transformer = (*fakeSubscriber)(nil)
	_ hook.Vetoer      = (*fakeSubscriber)(nil)
)

// observerOnly implements only hook.Observer, to exercise NewService's
// per-facet type assertion in isolation.
type observerOnly struct {
	observeFunc func(ctx context.Context, payload *hook.Payload) error
}

func (o *observerOnly) Observe(ctx context.Context, payload *hook.Payload) error {
	return o.observeFunc(ctx, payload)
}

var _ hook.Observer = (*observerOnly)(nil)

// vetoerOnly implements only hook.Vetoer, to exercise NewService's
// per-facet type assertion in isolation — unlike fakeSubscriber, which
// always implements all three facets regardless of which func fields are
// set, this type genuinely lacks an Observe method.
type vetoerOnly struct {
	vetoFunc func(ctx context.Context, payload *hook.Payload) (hook.Decision, error)
}

func (v *vetoerOnly) Veto(ctx context.Context, payload *hook.Payload) (hook.Decision, error) {
	return v.vetoFunc(ctx, payload)
}

var _ hook.Vetoer = (*vetoerOnly)(nil)

func sessionStartRequest(mode hookv1.HookMode) *hookv1.DispatchHookRequest {
	return &hookv1.DispatchHookRequest{
		Payload: &hookv1.HookPayload{Payload: &hookv1.HookPayload_SessionStart{
			SessionStart: &hookv1.SessionStartPayload{SessionId: "session-1", Profile: "default", WorkingDirectory: "/work"},
		}},
		Mode: mode,
	}
}

func preModelCallRequest() *hookv1.DispatchHookRequest {
	return &hookv1.DispatchHookRequest{
		Payload: &hookv1.HookPayload{Payload: &hookv1.HookPayload_PreModelCall{
			PreModelCall: &hookv1.PreModelCallPayload{
				Messages: []*contentv1.Message{{Role: contentv1.Role_ROLE_USER}},
				Model:    &modelv1.ModelRef{Provider: "anthropic", Id: "claude"},
			},
		}},
		Mode: hookv1.HookMode_HOOK_MODE_TRANSFORM,
	}
}

func planReadyRequest() *hookv1.DispatchHookRequest {
	return &hookv1.DispatchHookRequest{
		Payload: &hookv1.HookPayload{Payload: &hookv1.HookPayload_PlanReady{
			PlanReady: &hookv1.PlanReadyPayload{Plan: &planv1.Plan{TurnId: "turn-1"}},
		}},
		Mode: hookv1.HookMode_HOOK_MODE_VETO,
	}
}

func TestDispatchHook_ObserveSuccess(t *testing.T) {
	t.Parallel()

	var gotPoint commonv1.HookPoint
	sub := &fakeSubscriber{observeFunc: func(_ context.Context, payload *hook.Payload) error {
		gotPoint = payload.Point
		return nil
	}}
	client := newTestClient(t, hook.NewService(sub))

	resp, err := client.DispatchHook(t.Context(), sessionStartRequest(hookv1.HookMode_HOOK_MODE_OBSERVE))
	if err != nil {
		t.Fatalf("DispatchHook() unexpected error: %v", err)
	}
	if resp.GetObserve() == nil {
		t.Errorf("DispatchHook().GetObserve() = nil, want a non-nil ObserveAck")
	}
	if gotPoint != commonv1.HookPoint_HOOK_POINT_SESSION_START {
		t.Errorf("Observe saw Point = %v, want HOOK_POINT_SESSION_START", gotPoint)
	}
}

// TestDispatchHook_ObserveError_ReportedNotSwallowed asserts this
// package's documented boundary: an Observer error is reported to the
// caller as a real gRPC error, not swallowed into a fabricated ObserveAck
// success. The "never fatal to the chain" guarantee is the kernel dispatch
// loop's job one layer up (server.go's DispatchHook doc comment); this
// test only proves this layer doesn't get in the way of the kernel finding
// out about the failure.
func TestDispatchHook_ObserveError_ReportedNotSwallowed(t *testing.T) {
	t.Parallel()

	wantCause := errors.New("logger backend unreachable")
	sub := &fakeSubscriber{observeFunc: func(context.Context, *hook.Payload) error { return wantCause }}
	client := newTestClient(t, hook.NewService(sub))

	resp, err := client.DispatchHook(t.Context(), sessionStartRequest(hookv1.HookMode_HOOK_MODE_OBSERVE))
	if err == nil {
		t.Fatalf("DispatchHook() = %v, nil error; want a reported error, not a swallowed one", resp)
	}
	if status.Code(err) != codes.Internal {
		t.Errorf("DispatchHook() code = %v, want codes.Internal", status.Code(err))
	}
}

// TestDispatchHook_ObserveCanceledMapsToCanceled proves an Observer that
// returns context.Canceled (e.g. by propagating a canceled ctx.Err() it
// observed further downstream) is reported as codes.Canceled, not
// codes.Internal — cancellation is normal control flow, never an
// application error (.claude/rules/grpc.md), even inside this SDK's
// error-reporting path for observe mode. The call itself uses a live,
// uncanceled context so this exercises dispatchObserve's own
// mapContextErr(err) branch on the author's returned error, not gRPC's
// unrelated client-side short-circuit on an already-canceled ctx.
func TestDispatchHook_ObserveCanceledMapsToCanceled(t *testing.T) {
	t.Parallel()

	sub := &fakeSubscriber{observeFunc: func(context.Context, *hook.Payload) error { return context.Canceled }}
	client := newTestClient(t, hook.NewService(sub))

	_, err := client.DispatchHook(t.Context(), sessionStartRequest(hookv1.HookMode_HOOK_MODE_OBSERVE))
	if status.Code(err) != codes.Canceled {
		t.Errorf("DispatchHook() code = %v, want codes.Canceled", status.Code(err))
	}
}

func TestDispatchHook_ObserveNotImplemented(t *testing.T) {
	t.Parallel()

	// A Vetoer-only subscriber has no Observer facet.
	sub := &vetoerOnly{vetoFunc: func(context.Context, *hook.Payload) (hook.Decision, error) {
		return hook.DecisionAllow, nil
	}}
	client := newTestClient(t, hook.NewService(sub))

	_, err := client.DispatchHook(t.Context(), sessionStartRequest(hookv1.HookMode_HOOK_MODE_OBSERVE))
	if status.Code(err) != codes.Unimplemented {
		t.Errorf("DispatchHook() code = %v, want codes.Unimplemented", status.Code(err))
	}
}

func TestDispatchHook_TransformMutableFieldAllowed(t *testing.T) {
	t.Parallel()

	sub := &fakeSubscriber{transformFunc: func(_ context.Context, payload *hook.Payload) (*hook.Payload, error) {
		// Redact the messages — pre-model-call's one transform-mutable
		// field (agent-loop/hook-dispatch.md#per-point-transform-mutable-fields).
		redacted := &hookv1.HookPayload{Payload: &hookv1.HookPayload_PreModelCall{
			PreModelCall: &hookv1.PreModelCallPayload{
				Messages: nil,
				Model:    payload.Proto().GetPreModelCall().GetModel(),
			},
		}}
		domain, err := hook.NewPayload(redacted)
		if err != nil {
			t.Fatalf("NewPayload: %v", err)
		}
		return domain, nil
	}}
	client := newTestClient(t, hook.NewService(sub))

	resp, err := client.DispatchHook(t.Context(), preModelCallRequest())
	if err != nil {
		t.Fatalf("DispatchHook() unexpected error: %v", err)
	}
	if got := resp.GetTransform().GetPayload().GetPreModelCall().GetMessages(); len(got) != 0 {
		t.Errorf("DispatchHook() returned %d messages, want the redaction to have taken effect (0)", len(got))
	}
}

// TestDispatchHook_TransformInPlaceMutationValidatedAgainstSnapshot
// exercises the other valid Transformer authoring style NewPayload's
// doc comment describes — mutating payload.Proto() in place and returning
// the same *Payload — against both an allowed and a rejected mutation,
// proving dispatchTransform's pre-call snapshot (not a live reqProto
// reference) is what the comparison actually runs against.
func TestDispatchHook_TransformInPlaceMutationValidatedAgainstSnapshot(t *testing.T) {
	t.Parallel()

	t.Run("mutable field in place", func(t *testing.T) {
		t.Parallel()
		sub := &fakeSubscriber{transformFunc: func(_ context.Context, payload *hook.Payload) (*hook.Payload, error) {
			payload.Proto().GetPreModelCall().Messages = nil
			return payload, nil
		}}
		client := newTestClient(t, hook.NewService(sub))

		resp, err := client.DispatchHook(t.Context(), preModelCallRequest())
		if err != nil {
			t.Fatalf("DispatchHook() unexpected error: %v", err)
		}
		if got := resp.GetTransform().GetPayload().GetPreModelCall().GetMessages(); len(got) != 0 {
			t.Errorf("DispatchHook() returned %d messages, want the in-place redaction to have taken effect (0)", len(got))
		}
	})

	t.Run("immutable field in place is still rejected", func(t *testing.T) {
		t.Parallel()
		sub := &fakeSubscriber{transformFunc: func(_ context.Context, payload *hook.Payload) (*hook.Payload, error) {
			payload.Proto().GetPreModelCall().Model.Provider = "mutated-in-place"
			return payload, nil
		}}
		client := newTestClient(t, hook.NewService(sub))

		_, err := client.DispatchHook(t.Context(), preModelCallRequest())
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("DispatchHook() code = %v, want codes.InvalidArgument — an in-place mutation of an immutable field must still be caught", status.Code(err))
		}
	})
}

func TestDispatchHook_TransformImmutableFieldRejected(t *testing.T) {
	t.Parallel()

	sub := &fakeSubscriber{transformFunc: func(_ context.Context, payload *hook.Payload) (*hook.Payload, error) {
		// Illegally rewrite `model`, which pre-model-call's mutable-field
		// table does not list.
		mutated := &hookv1.HookPayload{Payload: &hookv1.HookPayload_PreModelCall{
			PreModelCall: &hookv1.PreModelCallPayload{
				Messages: payload.Proto().GetPreModelCall().GetMessages(),
				Model:    &modelv1.ModelRef{Provider: "different-provider", Id: "claude"},
			},
		}}
		domain, err := hook.NewPayload(mutated)
		if err != nil {
			t.Fatalf("NewPayload: %v", err)
		}
		return domain, nil
	}}
	client := newTestClient(t, hook.NewService(sub))

	_, err := client.DispatchHook(t.Context(), preModelCallRequest())
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("DispatchHook() code = %v, want codes.InvalidArgument (HOOK_ERROR_CATEGORY_INVALID_RESPONSE)", status.Code(err))
	}
}

func TestDispatchHook_TransformWrongVariantRejected(t *testing.T) {
	t.Parallel()

	sub := &fakeSubscriber{transformFunc: func(context.Context, *hook.Payload) (*hook.Payload, error) {
		wrong := &hookv1.HookPayload{Payload: &hookv1.HookPayload_SessionStart{
			SessionStart: &hookv1.SessionStartPayload{SessionId: "session-1"},
		}}
		domain, err := hook.NewPayload(wrong)
		if err != nil {
			t.Fatalf("NewPayload: %v", err)
		}
		return domain, nil
	}}
	client := newTestClient(t, hook.NewService(sub))

	_, err := client.DispatchHook(t.Context(), preModelCallRequest())
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("DispatchHook() code = %v, want codes.InvalidArgument (HOOK_ERROR_CATEGORY_INVALID_RESPONSE)", status.Code(err))
	}
}

func TestDispatchHook_TransformNilPayloadRejected(t *testing.T) {
	t.Parallel()

	sub := &fakeSubscriber{transformFunc: func(context.Context, *hook.Payload) (*hook.Payload, error) {
		return &hook.Payload{}, nil
	}}
	client := newTestClient(t, hook.NewService(sub))

	_, err := client.DispatchHook(t.Context(), preModelCallRequest())
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("DispatchHook() code = %v, want codes.InvalidArgument", status.Code(err))
	}
}

func TestDispatchHook_TransformSubscriberError(t *testing.T) {
	t.Parallel()

	sub := &fakeSubscriber{transformFunc: func(context.Context, *hook.Payload) (*hook.Payload, error) {
		return nil, errors.New("redaction backend down")
	}}
	client := newTestClient(t, hook.NewService(sub))

	_, err := client.DispatchHook(t.Context(), preModelCallRequest())
	if status.Code(err) != codes.Internal {
		t.Errorf("DispatchHook() code = %v, want codes.Internal", status.Code(err))
	}
}

func TestDispatchHook_VetoAllowAndDeny(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		decision hook.Decision
	}{
		{"allow", hook.DecisionAllow},
		{"deny", hook.DecisionDeny},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sub := &fakeSubscriber{vetoFunc: func(context.Context, *hook.Payload) (hook.Decision, error) { return tt.decision, nil }}
			client := newTestClient(t, hook.NewService(sub))

			resp, err := client.DispatchHook(t.Context(), planReadyRequest())
			if err != nil {
				t.Fatalf("DispatchHook() unexpected error: %v", err)
			}
			if got := resp.GetVeto().GetDecision(); got != tt.decision {
				t.Errorf("DispatchHook().GetVeto().GetDecision() = %v, want %v", got, tt.decision)
			}
		})
	}
}

// TestDispatchHook_VetoUnspecifiedFailsClosedAsError proves the crux of
// the fail-closed guarantee this package resolved by judgment call: a
// Vetoer returning DecisionUnspecified produces a gRPC error
// (HOOK_ERROR_CATEGORY_INVALID_RESPONSE / codes.InvalidArgument), never an
// in-band VetoResult{Decision: DecisionDeny}. rpc_response.pb.go's
// VetoResult doc comment places the actual fail-closed conversion at "the
// gRPC-status level" — i.e. in the kernel's dispatch loop, once it
// observes this error — not in this SDK.
func TestDispatchHook_VetoUnspecifiedFailsClosedAsError(t *testing.T) {
	t.Parallel()

	sub := &fakeSubscriber{vetoFunc: func(context.Context, *hook.Payload) (hook.Decision, error) {
		return hook.DecisionUnspecified, nil
	}}
	client := newTestClient(t, hook.NewService(sub))

	resp, err := client.DispatchHook(t.Context(), planReadyRequest())
	if err == nil {
		t.Fatalf("DispatchHook() = %v, nil error; want an error, not an in-band decision", resp)
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("DispatchHook() code = %v, want codes.InvalidArgument", status.Code(err))
	}
	if resp != nil {
		t.Errorf("DispatchHook() response = %v, want nil alongside the error", resp)
	}
}

func TestDispatchHook_VetoSubscriberError(t *testing.T) {
	t.Parallel()

	sub := &fakeSubscriber{vetoFunc: func(context.Context, *hook.Payload) (hook.Decision, error) {
		return hook.DecisionUnspecified, errors.New("policy engine down")
	}}
	client := newTestClient(t, hook.NewService(sub))

	_, err := client.DispatchHook(t.Context(), planReadyRequest())
	if status.Code(err) != codes.Internal {
		t.Errorf("DispatchHook() code = %v, want codes.Internal", status.Code(err))
	}
}

func TestDispatchHook_UnspecifiedMode(t *testing.T) {
	t.Parallel()

	sub := &fakeSubscriber{}
	client := newTestClient(t, hook.NewService(sub))

	_, err := client.DispatchHook(t.Context(), sessionStartRequest(hookv1.HookMode_HOOK_MODE_UNSPECIFIED))
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("DispatchHook() code = %v, want codes.InvalidArgument", status.Code(err))
	}
}

func TestDispatchHook_PayloadUnset(t *testing.T) {
	t.Parallel()

	sub := &fakeSubscriber{}
	client := newTestClient(t, hook.NewService(sub))

	_, err := client.DispatchHook(t.Context(), &hookv1.DispatchHookRequest{Mode: hookv1.HookMode_HOOK_MODE_OBSERVE})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("DispatchHook() code = %v, want codes.InvalidArgument", status.Code(err))
	}
}

func TestNewService_PartialFacets(t *testing.T) {
	t.Parallel()

	observed := false
	sub := &observerOnly{observeFunc: func(context.Context, *hook.Payload) error {
		observed = true
		return nil
	}}
	client := newTestClient(t, hook.NewService(sub))

	if _, err := client.DispatchHook(t.Context(), sessionStartRequest(hookv1.HookMode_HOOK_MODE_OBSERVE)); err != nil {
		t.Fatalf("DispatchHook(observe) unexpected error: %v", err)
	}
	if !observed {
		t.Error("Observe was never called")
	}

	// observerOnly implements no Vetoer facet — must fail closed with
	// Unimplemented, never silently allow.
	_, err := client.DispatchHook(t.Context(), planReadyRequest())
	if status.Code(err) != codes.Unimplemented {
		t.Errorf("DispatchHook(veto) code = %v, want codes.Unimplemented", status.Code(err))
	}
}
