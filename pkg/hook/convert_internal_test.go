package hook

import (
	"errors"
	"testing"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	hookv1 "github.com/pluggableharness/agent/pkg/hook/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
	planv1 "github.com/pluggableharness/agent/pkg/plan/proto/v1"
	sessionv1 "github.com/pluggableharness/agent/pkg/session/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
)

func sessionStartPayload() *hookv1.HookPayload {
	return &hookv1.HookPayload{Payload: &hookv1.HookPayload_SessionStart{
		SessionStart: &hookv1.SessionStartPayload{SessionId: "session-1", Profile: "default", WorkingDirectory: "/work"},
	}}
}

func preModelCallPayload(messages ...*contentv1.Message) *hookv1.HookPayload {
	return &hookv1.HookPayload{Payload: &hookv1.HookPayload_PreModelCall{
		PreModelCall: &hookv1.PreModelCallPayload{
			Messages: messages,
			Model:    &modelv1.ModelRef{Provider: "anthropic", Id: "claude"},
		},
	}}
}

func postModelResponsePayload() *hookv1.HookPayload {
	return &hookv1.HookPayload{Payload: &hookv1.HookPayload_PostModelResponse{
		PostModelResponse: &hookv1.PostModelResponsePayload{
			Message: &contentv1.Message{Role: contentv1.Role_ROLE_ASSISTANT},
		},
	}}
}

func preToolCallPayload() *hookv1.HookPayload {
	return &hookv1.HookPayload{Payload: &hookv1.HookPayload_PreToolCall{
		PreToolCall: &hookv1.PreToolCallPayload{
			Call:     &toolv1.ToolCall{Id: "call-1", ToolName: "grep"},
			PlanItem: &planv1.PlanItem{Id: "item-1"},
		},
	}}
}

func planReadyPayload() *hookv1.HookPayload {
	return &hookv1.HookPayload{Payload: &hookv1.HookPayload_PlanReady{
		PlanReady: &hookv1.PlanReadyPayload{Plan: &planv1.Plan{TurnId: "turn-1"}},
	}}
}

func postToolCallPayload() *hookv1.HookPayload {
	return &hookv1.HookPayload{Payload: &hookv1.HookPayload_PostToolCall{
		PostToolCall: &hookv1.PostToolCallPayload{
			Call:    &toolv1.ToolCall{Id: "call-1", ToolName: "grep"},
			Outcome: &hookv1.PostToolCallPayload_Result{Result: &toolv1.ToolResult{}},
		},
	}}
}

func postApplyPayload() *hookv1.HookPayload {
	return &hookv1.HookPayload{Payload: &hookv1.HookPayload_PostApply{
		PostApply: &hookv1.PostApplyPayload{Apply: &planv1.ApplyResult{TurnId: "turn-1"}},
	}}
}

func sessionEndPayload() *hookv1.HookPayload {
	return &hookv1.HookPayload{Payload: &hookv1.HookPayload_SessionEnd{
		SessionEnd: &hookv1.SessionEndPayload{SessionId: "session-1", Status: sessionv1.SessionStatus_SESSION_STATUS_COMPLETED},
	}}
}

func TestPointFromPayload(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload *hookv1.HookPayload
		want    commonv1.HookPoint
	}{
		{"session-start", sessionStartPayload(), commonv1.HookPoint_HOOK_POINT_SESSION_START},
		{"pre-model-call", preModelCallPayload(), commonv1.HookPoint_HOOK_POINT_PRE_MODEL_CALL},
		{"post-model-response", postModelResponsePayload(), commonv1.HookPoint_HOOK_POINT_POST_MODEL_RESPONSE},
		{"pre-tool-call", preToolCallPayload(), commonv1.HookPoint_HOOK_POINT_PRE_TOOL_CALL},
		{"plan-ready", planReadyPayload(), commonv1.HookPoint_HOOK_POINT_PLAN_READY},
		{"post-tool-call", postToolCallPayload(), commonv1.HookPoint_HOOK_POINT_POST_TOOL_CALL},
		{"post-apply", postApplyPayload(), commonv1.HookPoint_HOOK_POINT_POST_APPLY},
		{"session-end", sessionEndPayload(), commonv1.HookPoint_HOOK_POINT_SESSION_END},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := pointFromPayload(tt.payload)
			if err != nil {
				t.Fatalf("pointFromPayload(%s) unexpected error: %v", tt.name, err)
			}
			if got != tt.want {
				t.Errorf("pointFromPayload(%s) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestPointFromPayload_UnsetVariant(t *testing.T) {
	t.Parallel()

	_, err := pointFromPayload(&hookv1.HookPayload{})
	if !errors.Is(err, ErrPayloadVariantUnset) {
		t.Errorf("pointFromPayload(empty) error = %v, want wrapping ErrPayloadVariantUnset", err)
	}
}

func TestPayloadToDomain(t *testing.T) {
	t.Parallel()

	proto := sessionStartPayload()
	domain, err := payloadToDomain(proto, "sub-1")
	if err != nil {
		t.Fatalf("payloadToDomain() unexpected error: %v", err)
	}
	if domain.Point != commonv1.HookPoint_HOOK_POINT_SESSION_START {
		t.Errorf("payloadToDomain().Point = %v, want HOOK_POINT_SESSION_START", domain.Point)
	}
	if domain.SubscriptionID != "sub-1" {
		t.Errorf("payloadToDomain().SubscriptionID = %q, want %q", domain.SubscriptionID, "sub-1")
	}
	if domain.Proto() != proto {
		t.Error("payloadToDomain().Proto() did not return the same wire message it wrapped")
	}
}

func TestPayloadToDomain_InvalidVariant(t *testing.T) {
	t.Parallel()

	if _, err := payloadToDomain(&hookv1.HookPayload{}, ""); err == nil {
		t.Error("payloadToDomain(empty) = nil error, want ErrPayloadVariantUnset")
	}
}

func TestCloneHookPayload(t *testing.T) {
	t.Parallel()

	if got := cloneHookPayload(nil); got != nil {
		t.Errorf("cloneHookPayload(nil) = %v, want nil", got)
	}

	original := preModelCallPayload(&contentv1.Message{Role: contentv1.Role_ROLE_USER})
	cloned := cloneHookPayload(original)
	if cloned == original {
		t.Error("cloneHookPayload returned the same pointer, want a deep copy")
	}
	if cloned.GetPreModelCall().GetModel().GetProvider() != "anthropic" {
		t.Errorf("cloneHookPayload().GetPreModelCall().GetModel().GetProvider() = %q, want %q",
			cloned.GetPreModelCall().GetModel().GetProvider(), "anthropic")
	}

	// Mutating the clone MUST NOT affect the original.
	cloned.GetPreModelCall().Model.Provider = "mutated"
	if original.GetPreModelCall().GetModel().GetProvider() != "anthropic" {
		t.Error("mutating the clone changed the original — clone is not deep")
	}
}

func TestClearPreModelCallMessages(t *testing.T) {
	t.Parallel()

	// Nil-safe for any other variant.
	other := sessionStartPayload()
	clearPreModelCallMessages(other)

	p := preModelCallPayload(&contentv1.Message{Role: contentv1.Role_ROLE_USER})
	clearPreModelCallMessages(p)
	if len(p.GetPreModelCall().GetMessages()) != 0 {
		t.Errorf("clearPreModelCallMessages left %d messages, want 0", len(p.GetPreModelCall().GetMessages()))
	}
}

func TestPayloadsEqualExceptMutable(t *testing.T) {
	t.Parallel()

	msg := &contentv1.Message{Role: contentv1.Role_ROLE_USER}

	tests := []struct {
		name  string
		point commonv1.HookPoint
		req   *hookv1.HookPayload
		resp  *hookv1.HookPayload
		want  bool
	}{
		{
			name:  "identical payloads at a non-mutable point",
			point: commonv1.HookPoint_HOOK_POINT_SESSION_START,
			req:   sessionStartPayload(),
			resp:  sessionStartPayload(),
			want:  true,
		},
		{
			name:  "pre-model-call messages changed is allowed",
			point: commonv1.HookPoint_HOOK_POINT_PRE_MODEL_CALL,
			req:   preModelCallPayload(msg),
			resp:  preModelCallPayload(),
			want:  true,
		},
		{
			name:  "pre-model-call model field changed is rejected",
			point: commonv1.HookPoint_HOOK_POINT_PRE_MODEL_CALL,
			req:   preModelCallPayload(msg),
			resp: &hookv1.HookPayload{Payload: &hookv1.HookPayload_PreModelCall{
				PreModelCall: &hookv1.PreModelCallPayload{Model: &modelv1.ModelRef{Provider: "different", Id: "claude"}},
			}},
			want: false,
		},
		{
			name:  "non-mutable point with any field changed is rejected",
			point: commonv1.HookPoint_HOOK_POINT_SESSION_START,
			req:   sessionStartPayload(),
			resp: &hookv1.HookPayload{Payload: &hookv1.HookPayload_SessionStart{
				SessionStart: &hookv1.SessionStartPayload{SessionId: "different", Profile: "default", WorkingDirectory: "/work"},
			}},
			want: false,
		},
		{
			name:  "wrong oneof variant is rejected",
			point: commonv1.HookPoint_HOOK_POINT_SESSION_START,
			req:   sessionStartPayload(),
			resp:  sessionEndPayload(),
			want:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := payloadsEqualExceptMutable(tt.point, tt.req, tt.resp); got != tt.want {
				t.Errorf("payloadsEqualExceptMutable(%s) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}
