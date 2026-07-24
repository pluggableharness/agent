package kernel_test

import (
	"errors"
	"testing"

	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
)

func TestClient_Emit(t *testing.T) {
	t.Parallel()

	var gotReq *kernelv1.EmitRequest
	srv := &fakeServer{
		emitFunc: func(req *kernelv1.EmitRequest) (*kernelv1.EmitResult, error) {
			gotReq = req
			return &kernelv1.EmitResult{Id: "evt-01", Sequence: 7}, nil
		},
	}
	c := newTestClient(t, srv)

	result, err := c.Emit(t.Context(), "session-01", kernelv1.EventKind_EVENT_KIND_MESSAGE, "1", []byte("payload"))
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if result.GetId() != "evt-01" || result.GetSequence() != 7 {
		t.Errorf("Emit() = %+v, want id=evt-01 sequence=7", result)
	}
	if gotReq.GetSessionId() != "session-01" ||
		gotReq.GetKind() != kernelv1.EventKind_EVENT_KIND_MESSAGE ||
		gotReq.GetSchemaVersion() != "1" ||
		string(gotReq.GetPayload()) != "payload" {
		t.Errorf("server received %+v, want session_id=session-01 kind=MESSAGE schema_version=1 payload=payload", gotReq)
	}
}

func TestClient_Emit_error(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	srv := &fakeServer{
		emitFunc: func(*kernelv1.EmitRequest) (*kernelv1.EmitResult, error) {
			return nil, wantErr
		},
	}
	c := newTestClient(t, srv)

	if _, err := c.Emit(t.Context(), "session-01", kernelv1.EventKind_EVENT_KIND_MESSAGE, "1", nil); err == nil {
		t.Fatal("Emit: want error, got nil")
	}
}
