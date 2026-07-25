package kernelcallback

import (
	"bytes"
	"testing"

	"github.com/pluggableharness/agent/internal/bounds"
	"github.com/pluggableharness/agent/internal/statebackend"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"

	"google.golang.org/grpc/codes"
)

func TestServer_Emit_authorizationFailure(t *testing.T) {
	t.Parallel()
	f := newTestServer(t, testProducer())

	_, err := f.server.Emit(t.Context(), &kernelv1.EmitRequest{
		SessionId:     "no-such-session",
		Kind:          kernelv1.EventKind_EVENT_KIND_TOOL_CALL,
		SchemaVersion: "1",
		Payload:       []byte("x"),
	})
	assertCode(t, err, codes.PermissionDenied)
}

func TestServer_Emit_rejectsKernelOwnedKinds(t *testing.T) {
	t.Parallel()
	f := newTestServer(t, testProducer())
	sessionID, release := newLiveSession(t, f, bounds.Limits{})
	t.Cleanup(release)

	tests := []struct {
		name string
		kind kernelv1.EventKind
	}{
		{"message", kernelv1.EventKind_EVENT_KIND_MESSAGE},
		{"plan", kernelv1.EventKind_EVENT_KIND_PLAN},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := f.server.Emit(t.Context(), &kernelv1.EmitRequest{
				SessionId:     sessionID,
				Kind:          tt.kind,
				SchemaVersion: "1",
				Payload:       []byte("x"),
			})
			assertCode(t, err, codes.PermissionDenied)
		})
	}
}

func TestServer_Emit_validation(t *testing.T) {
	t.Parallel()
	f := newTestServer(t, testProducer())
	sessionID, release := newLiveSession(t, f, bounds.Limits{})
	t.Cleanup(release)

	tests := []struct {
		name string
		req  *kernelv1.EmitRequest
	}{
		{"unspecified kind", &kernelv1.EmitRequest{SessionId: sessionID, SchemaVersion: "1", Payload: []byte("x")}},
		{"empty schema_version", &kernelv1.EmitRequest{SessionId: sessionID, Kind: kernelv1.EventKind_EVENT_KIND_TOOL_CALL, Payload: []byte("x")}},
		{"nil payload", &kernelv1.EmitRequest{SessionId: sessionID, Kind: kernelv1.EventKind_EVENT_KIND_TOOL_CALL, SchemaVersion: "1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := f.server.Emit(t.Context(), tt.req)
			assertCode(t, err, codes.InvalidArgument)
		})
	}
}

func TestServer_Emit_roundTrip(t *testing.T) {
	t.Parallel()
	f := newTestServer(t, testProducer())
	sessionID, release := newLiveSession(t, f, bounds.Limits{})
	t.Cleanup(release)

	result, err := f.server.Emit(t.Context(), &kernelv1.EmitRequest{
		SessionId:     sessionID,
		Kind:          kernelv1.EventKind_EVENT_KIND_TOOL_CALL,
		SchemaVersion: "1",
		Payload:       []byte("payload-bytes"),
	})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if result.GetId() == "" {
		t.Error("EmitResult.Id is empty")
	}
	if result.GetSequence() != 1 {
		t.Errorf("EmitResult.Sequence = %d, want 1", result.GetSequence())
	}

	live, ok := f.sessions.Get(sessionID)
	if !ok {
		t.Fatalf("test setup: session %q not registered live", sessionID)
	}
	var found bool
	for ev, evErr := range live.Events(t.Context(), statebackend.EventQuery{}) {
		if evErr != nil {
			t.Fatalf("Events: %v", evErr)
		}
		if ev.ID == result.GetId() && bytes.Equal(ev.Payload, []byte("payload-bytes")) {
			found = true
		}
	}
	if !found {
		t.Error("Emit did not persist the expected event")
	}
}

func TestServer_Emit_liveWriteFailure(t *testing.T) {
	t.Parallel()
	f := newTestServer(t, testProducer())
	sessionID, release := newLiveSession(t, f, bounds.Limits{})
	t.Cleanup(release)

	live, ok := f.sessions.Get(sessionID)
	if !ok {
		t.Fatalf("test setup: session %q not registered live", sessionID)
	}
	// Authorization only checks the scope grant and live-table membership
	// (authorizedSession), not whether the underlying session is still
	// writable — Close it directly to exercise Emit's own live.Emit
	// failure branch (codes.Internal) without removing it from the live
	// table.
	if err := live.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, err := f.server.Emit(t.Context(), &kernelv1.EmitRequest{
		SessionId:     sessionID,
		Kind:          kernelv1.EventKind_EVENT_KIND_TOOL_CALL,
		SchemaVersion: "1",
		Payload:       []byte("x"),
	})
	assertCode(t, err, codes.Internal)
}
