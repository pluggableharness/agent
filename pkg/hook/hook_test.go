package hook_test

import (
	"testing"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	"github.com/pluggableharness/agent/pkg/hook"
	hookv1 "github.com/pluggableharness/agent/pkg/hook/proto/v1"
)

func TestHookPayload_ProtoNilReceiver(t *testing.T) {
	t.Parallel()

	var nilPayload *hook.Payload
	if got := nilPayload.Proto(); got != nil {
		t.Errorf("(*Payload)(nil).Proto() = %v, want nil", got)
	}
}

func TestNewHookPayload(t *testing.T) {
	t.Parallel()

	proto := &hookv1.HookPayload{Payload: &hookv1.HookPayload_SessionEnd{
		SessionEnd: &hookv1.SessionEndPayload{SessionId: "session-1"},
	}}
	payload, err := hook.NewPayload(proto)
	if err != nil {
		t.Fatalf("NewPayload() unexpected error: %v", err)
	}
	if payload.Point != commonv1.HookPoint_HOOK_POINT_SESSION_END {
		t.Errorf("NewPayload().Point = %v, want HOOK_POINT_SESSION_END", payload.Point)
	}
	if payload.Proto() != proto {
		t.Error("NewPayload().Proto() did not return the wrapped message")
	}
}

func TestNewHookPayload_InvalidVariant(t *testing.T) {
	t.Parallel()

	if _, err := hook.NewPayload(&hookv1.HookPayload{}); err == nil {
		t.Error("NewPayload(empty) = nil error, want ErrPayloadVariantUnset")
	}
}
