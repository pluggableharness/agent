package hook

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	hookv1 "github.com/pluggableharness/agent/pkg/hook/proto/v1"
)

// errorInfo extracts the google.rpc.ErrorInfo detail plugin.StatusError
// attaches, failing the test if err carries none.
func errorInfo(t *testing.T, err error) *errdetails.ErrorInfo {
	t.Helper()
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("status.FromError(%v) ok = false, want a *status.Status-backed error", err)
	}
	for _, d := range st.Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok {
			return info
		}
	}
	t.Fatalf("error %v carries no google.rpc.ErrorInfo detail", err)
	return nil
}

func TestErrorMetadata(t *testing.T) {
	t.Parallel()

	got := errorMetadata(commonv1.HookPoint_HOOK_POINT_PLAN_READY, hookv1.HookMode_HOOK_MODE_VETO, hookv1.HookErrorCategory_HOOK_ERROR_CATEGORY_VETO_FAILED)
	want := map[string]string{
		"hook_point": "HOOK_POINT_PLAN_READY",
		"mode":       "HOOK_MODE_VETO",
		"category":   "HOOK_ERROR_CATEGORY_VETO_FAILED",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("errorMetadata()[%q] = %q, want %q", k, got[k], v)
		}
	}
}

func TestErrInvalidResponse(t *testing.T) {
	t.Parallel()

	err := errInvalidResponse(commonv1.HookPoint_HOOK_POINT_PLAN_READY, hookv1.HookMode_HOOK_MODE_VETO, "veto_decision_unspecified", "hook: veto: response decision is unspecified")
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument {
		t.Errorf("errInvalidResponse() code = %v (ok=%v), want codes.InvalidArgument", st.Code(), ok)
	}
	info := errorInfo(t, err)
	if info.GetReason() != "veto_decision_unspecified" {
		t.Errorf("errInvalidResponse() reason = %q, want %q", info.GetReason(), "veto_decision_unspecified")
	}
	if info.GetDomain() != errorDomain {
		t.Errorf("errInvalidResponse() domain = %q, want %q", info.GetDomain(), errorDomain)
	}
	if info.GetMetadata()["category"] != "HOOK_ERROR_CATEGORY_INVALID_RESPONSE" {
		t.Errorf("errInvalidResponse() category = %q, want HOOK_ERROR_CATEGORY_INVALID_RESPONSE", info.GetMetadata()["category"])
	}
}

func TestErrTransformFailed(t *testing.T) {
	t.Parallel()

	cause := errors.New("boom")
	err := errTransformFailed(commonv1.HookPoint_HOOK_POINT_PRE_MODEL_CALL, cause)
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.Internal {
		t.Errorf("errTransformFailed() code = %v (ok=%v), want codes.Internal", st.Code(), ok)
	}
	if info := errorInfo(t, err); info.GetMetadata()["category"] != "HOOK_ERROR_CATEGORY_TRANSFORM_FAILED" {
		t.Errorf("errTransformFailed() category = %q, want HOOK_ERROR_CATEGORY_TRANSFORM_FAILED", info.GetMetadata()["category"])
	}
}

func TestErrVetoFailed(t *testing.T) {
	t.Parallel()

	err := errVetoFailed(commonv1.HookPoint_HOOK_POINT_PLAN_READY, errors.New("boom"))
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.Internal {
		t.Errorf("errVetoFailed() code = %v (ok=%v), want codes.Internal", st.Code(), ok)
	}
	if info := errorInfo(t, err); info.GetMetadata()["category"] != "HOOK_ERROR_CATEGORY_VETO_FAILED" {
		t.Errorf("errVetoFailed() category = %q, want HOOK_ERROR_CATEGORY_VETO_FAILED", info.GetMetadata()["category"])
	}
}

func TestErrObserveFailed(t *testing.T) {
	t.Parallel()

	err := errObserveFailed(commonv1.HookPoint_HOOK_POINT_SESSION_START, errors.New("boom"))
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.Internal {
		t.Errorf("errObserveFailed() code = %v (ok=%v), want codes.Internal", st.Code(), ok)
	}
	if info := errorInfo(t, err); info.GetMetadata()["category"] != "HOOK_ERROR_CATEGORY_UNKNOWN" {
		t.Errorf("errObserveFailed() category = %q, want HOOK_ERROR_CATEGORY_UNKNOWN", info.GetMetadata()["category"])
	}
}

func TestErrNotImplemented(t *testing.T) {
	t.Parallel()

	err := errNotImplemented(commonv1.HookPoint_HOOK_POINT_SESSION_END, hookv1.HookMode_HOOK_MODE_OBSERVE)
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.Unimplemented {
		t.Errorf("errNotImplemented() code = %v (ok=%v), want codes.Unimplemented", st.Code(), ok)
	}
	if !strings.Contains(st.Message(), ErrSubscriberNotImplemented.Error()) {
		t.Errorf("errNotImplemented() message = %q, want it to contain %q", st.Message(), ErrSubscriberNotImplemented.Error())
	}
}

func TestErrInvalidRequest(t *testing.T) {
	t.Parallel()

	err := errInvalidRequest("mode_unset", "hook: dispatch: request mode is unspecified")
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument {
		t.Errorf("errInvalidRequest() code = %v (ok=%v), want codes.InvalidArgument", st.Code(), ok)
	}
	if info := errorInfo(t, err); info.GetReason() != "mode_unset" {
		t.Errorf("errInvalidRequest() reason = %q, want %q", info.GetReason(), "mode_unset")
	}
}

func TestMapContextErr(t *testing.T) {
	t.Parallel()

	if got := mapContextErr(nil); got != nil {
		t.Errorf("mapContextErr(nil) = %v, want nil", got)
	}
	if got := mapContextErr(errors.New("boom")); got != nil {
		t.Errorf("mapContextErr(non-context error) = %v, want nil", got)
	}

	if got := mapContextErr(context.Canceled); status.Code(got) != codes.Canceled {
		t.Errorf("mapContextErr(context.Canceled) code = %v, want codes.Canceled", status.Code(got))
	}
	if got := mapContextErr(context.DeadlineExceeded); status.Code(got) != codes.DeadlineExceeded {
		t.Errorf("mapContextErr(context.DeadlineExceeded) code = %v, want codes.DeadlineExceeded", status.Code(got))
	}

	wrapped := fmt.Errorf("hook: dispatch: %w", context.Canceled)
	if got := mapContextErr(wrapped); status.Code(got) != codes.Canceled {
		t.Errorf("mapContextErr(wrapped context.Canceled) code = %v, want codes.Canceled", status.Code(got))
	}
}
