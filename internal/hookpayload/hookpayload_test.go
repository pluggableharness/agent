package hookpayload

import (
	"errors"
	"fmt"
	"testing"

	"google.golang.org/protobuf/proto"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	hookv1 "github.com/pluggableharness/agent/pkg/hook/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
	planv1 "github.com/pluggableharness/agent/pkg/plan/proto/v1"
	sessionv1 "github.com/pluggableharness/agent/pkg/session/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
)

func TestPointMappings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload *hookv1.HookPayload
		want    commonv1.HookPoint
		wantOk  bool
	}{
		{
			name: "SessionStartPayload",
			payload: &hookv1.HookPayload{
				Payload: &hookv1.HookPayload_SessionStart{
					SessionStart: &hookv1.SessionStartPayload{
						SessionId: "session-123",
						Profile:   "default",
					},
				},
			},
			want:   commonv1.HookPoint_HOOK_POINT_SESSION_START,
			wantOk: true,
		},
		{
			name: "PreModelCallPayload",
			payload: &hookv1.HookPayload{
				Payload: &hookv1.HookPayload_PreModelCall{
					PreModelCall: &hookv1.PreModelCallPayload{
						Messages: []*contentv1.Message{},
						Model: &modelv1.ModelRef{
							Provider: "anthropic",
							Id:       "claude-3-sonnet",
						},
					},
				},
			},
			want:   commonv1.HookPoint_HOOK_POINT_PRE_MODEL_CALL,
			wantOk: true,
		},
		{
			name: "PostModelResponsePayload",
			payload: &hookv1.HookPayload{
				Payload: &hookv1.HookPayload_PostModelResponse{
					PostModelResponse: &hookv1.PostModelResponsePayload{
						Message: &contentv1.Message{},
						Model: &commonv1.ProducerRef{
							Name: "anthropic",
						},
						Usage:   &modelv1.Usage{},
						CostUsd: 0.01,
					},
				},
			},
			want:   commonv1.HookPoint_HOOK_POINT_POST_MODEL_RESPONSE,
			wantOk: true,
		},
		{
			name: "PreToolCallPayload",
			payload: &hookv1.HookPayload{
				Payload: &hookv1.HookPayload_PreToolCall{
					PreToolCall: &hookv1.PreToolCallPayload{
						Call:     &toolv1.ToolCall{},
						PlanItem: &planv1.PlanItem{},
					},
				},
			},
			want:   commonv1.HookPoint_HOOK_POINT_PRE_TOOL_CALL,
			wantOk: true,
		},
		{
			name: "PlanReadyPayload",
			payload: &hookv1.HookPayload{
				Payload: &hookv1.HookPayload_PlanReady{
					PlanReady: &hookv1.PlanReadyPayload{
						Plan: &planv1.Plan{},
					},
				},
			},
			want:   commonv1.HookPoint_HOOK_POINT_PLAN_READY,
			wantOk: true,
		},
		{
			name: "PostToolCallPayload",
			payload: &hookv1.HookPayload{
				Payload: &hookv1.HookPayload_PostToolCall{
					PostToolCall: &hookv1.PostToolCallPayload{
						Call: &toolv1.ToolCall{},
						Outcome: &hookv1.PostToolCallPayload_Result{
							Result: &toolv1.ToolResult{},
						},
					},
				},
			},
			want:   commonv1.HookPoint_HOOK_POINT_POST_TOOL_CALL,
			wantOk: true,
		},
		{
			name: "PostApplyPayload",
			payload: &hookv1.HookPayload{
				Payload: &hookv1.HookPayload_PostApply{
					PostApply: &hookv1.PostApplyPayload{
						Apply: &planv1.ApplyResult{},
					},
				},
			},
			want:   commonv1.HookPoint_HOOK_POINT_POST_APPLY,
			wantOk: true,
		},
		{
			name: "SessionEndPayload",
			payload: &hookv1.HookPayload{
				Payload: &hookv1.HookPayload_SessionEnd{
					SessionEnd: &hookv1.SessionEndPayload{
						SessionId: "session-123",
						Status:    sessionv1.SessionStatus_SESSION_STATUS_COMPLETED,
					},
				},
			},
			want:   commonv1.HookPoint_HOOK_POINT_SESSION_END,
			wantOk: true,
		},
		{
			name:    "nil payload",
			payload: nil,
			want:    commonv1.HookPoint_HOOK_POINT_UNSPECIFIED,
			wantOk:  false,
		},
		{
			name:    "empty payload (no variant set)",
			payload: &hookv1.HookPayload{},
			want:    commonv1.HookPoint_HOOK_POINT_UNSPECIFIED,
			wantOk:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, gotOk := Point(tt.payload)
			if got != tt.want || gotOk != tt.wantOk {
				t.Errorf("Point(%v) = (%v, %v), want (%v, %v)",
					tt.payload, got, gotOk, tt.want, tt.wantOk)
			}
		})
	}
}

func TestMutableFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		point commonv1.HookPoint
		want  []string
	}{
		{
			name:  "SessionStart is immutable",
			point: commonv1.HookPoint_HOOK_POINT_SESSION_START,
			want:  nil,
		},
		{
			name:  "PreModelCall has messages mutable",
			point: commonv1.HookPoint_HOOK_POINT_PRE_MODEL_CALL,
			want:  []string{"messages"},
		},
		{
			name:  "PostModelResponse is immutable",
			point: commonv1.HookPoint_HOOK_POINT_POST_MODEL_RESPONSE,
			want:  nil,
		},
		{
			name:  "PreToolCall is immutable",
			point: commonv1.HookPoint_HOOK_POINT_PRE_TOOL_CALL,
			want:  nil,
		},
		{
			name:  "PlanReady is immutable",
			point: commonv1.HookPoint_HOOK_POINT_PLAN_READY,
			want:  nil,
		},
		{
			name:  "PostToolCall is immutable",
			point: commonv1.HookPoint_HOOK_POINT_POST_TOOL_CALL,
			want:  nil,
		},
		{
			name:  "PostApply is immutable",
			point: commonv1.HookPoint_HOOK_POINT_POST_APPLY,
			want:  nil,
		},
		{
			name:  "SessionEnd is immutable",
			point: commonv1.HookPoint_HOOK_POINT_SESSION_END,
			want:  nil,
		},
		{
			name:  "Unspecified point",
			point: commonv1.HookPoint_HOOK_POINT_UNSPECIFIED,
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Mutable(tt.point)
			if len(got) == 0 && len(tt.want) == 0 {
				return // Both empty/nil is OK
			}
			if !sliceEqual(got, tt.want) {
				t.Errorf("Mutable(%v) = %v, want %v", tt.point, got, tt.want)
			}
		})
	}
}

func TestApplyTransformPreModelCall(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		req         *hookv1.HookPayload
		resp        *hookv1.HookPayload
		wantErr     bool
		errContains string
	}{
		{
			name: "valid transform with messages changed",
			req: &hookv1.HookPayload{
				Payload: &hookv1.HookPayload_PreModelCall{
					PreModelCall: &hookv1.PreModelCallPayload{
						Messages: []*contentv1.Message{},
						Model: &modelv1.ModelRef{
							Provider: "anthropic",
							Id:       "claude-3-sonnet",
						},
					},
				},
			},
			resp: &hookv1.HookPayload{
				Payload: &hookv1.HookPayload_PreModelCall{
					PreModelCall: &hookv1.PreModelCallPayload{
						Messages: []*contentv1.Message{
							{Role: contentv1.Role_ROLE_USER},
						},
						Model: &modelv1.ModelRef{
							Provider: "anthropic",
							Id:       "claude-3-sonnet",
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid transform changing model field",
			req: &hookv1.HookPayload{
				Payload: &hookv1.HookPayload_PreModelCall{
					PreModelCall: &hookv1.PreModelCallPayload{
						Messages: []*contentv1.Message{},
						Model: &modelv1.ModelRef{
							Provider: "anthropic",
							Id:       "claude-3-sonnet",
						},
					},
				},
			},
			resp: &hookv1.HookPayload{
				Payload: &hookv1.HookPayload_PreModelCall{
					PreModelCall: &hookv1.PreModelCallPayload{
						Messages: []*contentv1.Message{
							{Role: contentv1.Role_ROLE_USER},
						},
						Model: &modelv1.ModelRef{
							Provider: "anthropic",
							Id:       "claude-3-opus",
						},
					},
				},
			},
			wantErr:     true,
			errContains: "immutable",
		},
		{
			name: "nil request payload",
			req:  nil,
			resp: &hookv1.HookPayload{
				Payload: &hookv1.HookPayload_PreModelCall{
					PreModelCall: &hookv1.PreModelCallPayload{},
				},
			},
			wantErr:     true,
			errContains: "nil",
		},
		{
			name: "nil response payload",
			req: &hookv1.HookPayload{
				Payload: &hookv1.HookPayload_PreModelCall{
					PreModelCall: &hookv1.PreModelCallPayload{},
				},
			},
			resp:        nil,
			wantErr:     true,
			errContains: "nil",
		},
		{
			name: "request with no variant set",
			req:  &hookv1.HookPayload{},
			resp: &hookv1.HookPayload{
				Payload: &hookv1.HookPayload_PreModelCall{
					PreModelCall: &hookv1.PreModelCallPayload{},
				},
			},
			wantErr:     true,
			errContains: "no variant",
		},
		{
			name: "response with no variant set",
			req: &hookv1.HookPayload{
				Payload: &hookv1.HookPayload_PreModelCall{
					PreModelCall: &hookv1.PreModelCallPayload{},
				},
			},
			resp:        &hookv1.HookPayload{},
			wantErr:     true,
			errContains: "no variant",
		},
		{
			name: "variant mismatch",
			req: &hookv1.HookPayload{
				Payload: &hookv1.HookPayload_PreModelCall{
					PreModelCall: &hookv1.PreModelCallPayload{},
				},
			},
			resp: &hookv1.HookPayload{
				Payload: &hookv1.HookPayload_SessionStart{
					SessionStart: &hookv1.SessionStartPayload{},
				},
			},
			wantErr:     true,
			errContains: "variant",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := ApplyTransform(tt.req, tt.resp)
			if (err != nil) != tt.wantErr {
				t.Errorf("ApplyTransform() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && err != nil && !errors.Is(err, ErrInvalidResponse) {
				t.Errorf("ApplyTransform() error = %v, should wrap ErrInvalidResponse", err)
			}
			if tt.wantErr && tt.errContains != "" && err != nil {
				if !contains(err.Error(), tt.errContains) {
					t.Errorf("ApplyTransform() error = %v, should contain %q", err, tt.errContains)
				}
			}
		})
	}
}

func TestApplyTransformImmutablePoints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  *hookv1.HookPayload
		resp *hookv1.HookPayload
	}{
		{
			name: "SessionStart unchanged",
			req: &hookv1.HookPayload{
				Payload: &hookv1.HookPayload_SessionStart{
					SessionStart: &hookv1.SessionStartPayload{
						SessionId: "session-123",
						Profile:   "default",
					},
				},
			},
			resp: &hookv1.HookPayload{
				Payload: &hookv1.HookPayload_SessionStart{
					SessionStart: &hookv1.SessionStartPayload{
						SessionId: "session-123",
						Profile:   "default",
					},
				},
			},
		},
		{
			name: "PostModelResponse unchanged",
			req: &hookv1.HookPayload{
				Payload: &hookv1.HookPayload_PostModelResponse{
					PostModelResponse: &hookv1.PostModelResponsePayload{
						Message: &contentv1.Message{},
						Model: &commonv1.ProducerRef{
							Name: "anthropic",
						},
						Usage:   &modelv1.Usage{},
						CostUsd: 0.01,
					},
				},
			},
			resp: &hookv1.HookPayload{
				Payload: &hookv1.HookPayload_PostModelResponse{
					PostModelResponse: &hookv1.PostModelResponsePayload{
						Message: &contentv1.Message{},
						Model: &commonv1.ProducerRef{
							Name: "anthropic",
						},
						Usage:   &modelv1.Usage{},
						CostUsd: 0.01,
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result, err := ApplyTransform(tt.req, tt.resp)
			if err != nil {
				t.Fatalf("ApplyTransform() error = %v, want nil", err)
			}
			if !proto.Equal(result, tt.req) {
				t.Errorf("ApplyTransform() returned payload differs from request")
			}
		})
	}
}

func TestApplyTransformImmutablePointsMutated(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  *hookv1.HookPayload
		resp *hookv1.HookPayload
	}{
		{
			name: "SessionStart changed",
			req: &hookv1.HookPayload{
				Payload: &hookv1.HookPayload_SessionStart{
					SessionStart: &hookv1.SessionStartPayload{
						SessionId: "session-123",
						Profile:   "default",
					},
				},
			},
			resp: &hookv1.HookPayload{
				Payload: &hookv1.HookPayload_SessionStart{
					SessionStart: &hookv1.SessionStartPayload{
						SessionId: "session-456",
						Profile:   "default",
					},
				},
			},
		},
		{
			name: "PostModelResponse changed",
			req: &hookv1.HookPayload{
				Payload: &hookv1.HookPayload_PostModelResponse{
					PostModelResponse: &hookv1.PostModelResponsePayload{
						Message: &contentv1.Message{},
						Model: &commonv1.ProducerRef{
							Name: "anthropic",
						},
						Usage:   &modelv1.Usage{},
						CostUsd: 0.01,
					},
				},
			},
			resp: &hookv1.HookPayload{
				Payload: &hookv1.HookPayload_PostModelResponse{
					PostModelResponse: &hookv1.PostModelResponsePayload{
						Message: &contentv1.Message{},
						Model: &commonv1.ProducerRef{
							Name: "anthropic",
						},
						Usage:   &modelv1.Usage{},
						CostUsd: 0.02,
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := ApplyTransform(tt.req, tt.resp)
			if err == nil {
				t.Fatalf("ApplyTransform() error = nil, want error")
			}
			if !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("ApplyTransform() error = %v, should wrap ErrInvalidResponse", err)
			}
		})
	}
}

func TestValidateShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mode    hookv1.HookMode
		resp    *hookv1.DispatchHookResponse
		wantErr bool
	}{
		{
			name: "observe mode with ObserveAck",
			mode: hookv1.HookMode_HOOK_MODE_OBSERVE,
			resp: &hookv1.DispatchHookResponse{
				Outcome: &hookv1.DispatchHookResponse_Observe{
					Observe: &hookv1.DispatchHookResponse_ObserveAck{},
				},
			},
			wantErr: false,
		},
		{
			name: "transform mode with TransformResult",
			mode: hookv1.HookMode_HOOK_MODE_TRANSFORM,
			resp: &hookv1.DispatchHookResponse{
				Outcome: &hookv1.DispatchHookResponse_Transform{
					Transform: &hookv1.DispatchHookResponse_TransformResult{
						Payload: &hookv1.HookPayload{},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "veto mode with VetoResult ALLOW",
			mode: hookv1.HookMode_HOOK_MODE_VETO,
			resp: &hookv1.DispatchHookResponse{
				Outcome: &hookv1.DispatchHookResponse_Veto{
					Veto: &hookv1.DispatchHookResponse_VetoResult{
						Decision: hookv1.HookDecision_HOOK_DECISION_ALLOW,
					},
				},
			},
			wantErr: false,
		},
		{
			name: "veto mode with VetoResult DENY",
			mode: hookv1.HookMode_HOOK_MODE_VETO,
			resp: &hookv1.DispatchHookResponse{
				Outcome: &hookv1.DispatchHookResponse_Veto{
					Veto: &hookv1.DispatchHookResponse_VetoResult{
						Decision: hookv1.HookDecision_HOOK_DECISION_DENY,
					},
				},
			},
			wantErr: false,
		},
		{
			name: "veto mode with VetoResult UNSPECIFIED",
			mode: hookv1.HookMode_HOOK_MODE_VETO,
			resp: &hookv1.DispatchHookResponse{
				Outcome: &hookv1.DispatchHookResponse_Veto{
					Veto: &hookv1.DispatchHookResponse_VetoResult{
						Decision: hookv1.HookDecision_HOOK_DECISION_UNSPECIFIED,
					},
				},
			},
			wantErr: true,
		},
		{
			name: "observe mode with TransformResult (mismatch)",
			mode: hookv1.HookMode_HOOK_MODE_OBSERVE,
			resp: &hookv1.DispatchHookResponse{
				Outcome: &hookv1.DispatchHookResponse_Transform{
					Transform: &hookv1.DispatchHookResponse_TransformResult{},
				},
			},
			wantErr: true,
		},
		{
			name: "transform mode with ObserveAck (mismatch)",
			mode: hookv1.HookMode_HOOK_MODE_TRANSFORM,
			resp: &hookv1.DispatchHookResponse{
				Outcome: &hookv1.DispatchHookResponse_Observe{
					Observe: &hookv1.DispatchHookResponse_ObserveAck{},
				},
			},
			wantErr: true,
		},
		{
			name: "transform mode with VetoResult (mismatch)",
			mode: hookv1.HookMode_HOOK_MODE_TRANSFORM,
			resp: &hookv1.DispatchHookResponse{
				Outcome: &hookv1.DispatchHookResponse_Veto{
					Veto: &hookv1.DispatchHookResponse_VetoResult{
						Decision: hookv1.HookDecision_HOOK_DECISION_ALLOW,
					},
				},
			},
			wantErr: true,
		},
		{
			name: "veto mode with ObserveAck (mismatch)",
			mode: hookv1.HookMode_HOOK_MODE_VETO,
			resp: &hookv1.DispatchHookResponse{
				Outcome: &hookv1.DispatchHookResponse_Observe{
					Observe: &hookv1.DispatchHookResponse_ObserveAck{},
				},
			},
			wantErr: true,
		},
		{
			name:    "nil response",
			mode:    hookv1.HookMode_HOOK_MODE_OBSERVE,
			resp:    nil,
			wantErr: true,
		},
		{
			name: "unspecified mode",
			mode: hookv1.HookMode_HOOK_MODE_UNSPECIFIED,
			resp: &hookv1.DispatchHookResponse{
				Outcome: &hookv1.DispatchHookResponse_Observe{
					Observe: &hookv1.DispatchHookResponse_ObserveAck{},
				},
			},
			wantErr: true,
		},
		{
			name: "veto mode with nil VetoResult",
			mode: hookv1.HookMode_HOOK_MODE_VETO,
			resp: &hookv1.DispatchHookResponse{
				Outcome: &hookv1.DispatchHookResponse_Veto{
					Veto: nil,
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateShape(tt.mode, tt.resp)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateShape() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && err != nil && !errors.Is(err, ErrInvalidResponse) {
				t.Errorf("ValidateShape() error = %v, should wrap ErrInvalidResponse", err)
			}
		})
	}
}

func TestCategory(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mode hookv1.HookMode
		err  error
		want hookv1.HookErrorCategory
	}{
		{
			name: "ErrInvalidResponse with observe mode",
			mode: hookv1.HookMode_HOOK_MODE_OBSERVE,
			err:  ErrInvalidResponse,
			want: hookv1.HookErrorCategory_HOOK_ERROR_CATEGORY_INVALID_RESPONSE,
		},
		{
			name: "ErrInvalidResponse with transform mode",
			mode: hookv1.HookMode_HOOK_MODE_TRANSFORM,
			err:  ErrInvalidResponse,
			want: hookv1.HookErrorCategory_HOOK_ERROR_CATEGORY_INVALID_RESPONSE,
		},
		{
			name: "ErrInvalidResponse with veto mode",
			mode: hookv1.HookMode_HOOK_MODE_VETO,
			err:  ErrInvalidResponse,
			want: hookv1.HookErrorCategory_HOOK_ERROR_CATEGORY_INVALID_RESPONSE,
		},
		{
			name: "wrapped ErrInvalidResponse",
			mode: hookv1.HookMode_HOOK_MODE_TRANSFORM,
			err:  fmt.Errorf("outer: %w", ErrInvalidResponse),
			want: hookv1.HookErrorCategory_HOOK_ERROR_CATEGORY_INVALID_RESPONSE,
		},
		{
			name: "transform mode with other error",
			mode: hookv1.HookMode_HOOK_MODE_TRANSFORM,
			err:  errors.New("some error"),
			want: hookv1.HookErrorCategory_HOOK_ERROR_CATEGORY_TRANSFORM_FAILED,
		},
		{
			name: "observe mode with other error",
			mode: hookv1.HookMode_HOOK_MODE_OBSERVE,
			err:  errors.New("some error"),
			want: hookv1.HookErrorCategory_HOOK_ERROR_CATEGORY_UNKNOWN,
		},
		{
			name: "veto mode with other error",
			mode: hookv1.HookMode_HOOK_MODE_VETO,
			err:  errors.New("some error"),
			want: hookv1.HookErrorCategory_HOOK_ERROR_CATEGORY_VETO_FAILED,
		},
		{
			name: "unspecified mode with other error",
			mode: hookv1.HookMode_HOOK_MODE_UNSPECIFIED,
			err:  errors.New("some error"),
			want: hookv1.HookErrorCategory_HOOK_ERROR_CATEGORY_UNKNOWN,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Category(tt.mode, tt.err)
			if got != tt.want {
				t.Errorf("Category(%v, %v) = %v, want %v", tt.mode, tt.err, got, tt.want)
			}
		})
	}
}

func TestErrInvalidResponseSentinel(t *testing.T) {
	t.Parallel()

	if ErrInvalidResponse == nil {
		t.Fatalf("ErrInvalidResponse is nil")
	}

	err := fmt.Errorf("wrapping: %w", ErrInvalidResponse)
	if !errors.Is(err, ErrInvalidResponse) {
		t.Errorf("errors.Is(wrapped error, ErrInvalidResponse) = false, want true")
	}
}

// Helper functions for tests

func sliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contains(s, substr string) bool {
	for i := 0; i < len(s)-len(substr)+1; i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
