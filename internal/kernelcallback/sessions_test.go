package kernelcallback

import (
	"context"
	"testing"
	"time"

	"github.com/pluggableharness/agent/internal/bounds"
	"github.com/pluggableharness/agent/internal/sessionscope"
	"github.com/pluggableharness/agent/internal/sessionstate"
	"github.com/pluggableharness/agent/internal/statebackend"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	sessionv1 "github.com/pluggableharness/agent/pkg/session/proto/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// newLiveSession builds a fresh *sessionstate.Live over a real
// *statebackend.Session in a t.TempDir() store and f's real *eventbus.Bus,
// registers it in f.sessions under a fresh session id, and grants f's
// scopes registry a grant for f.server's own bound producer — the full
// state a session-scoped RPC (Emit/ReadEvents/GetSession) needs to
// succeed. Returns the session id and the grant's release func, so a test
// can simulate "authorized but no longer live" by calling release and
// removing the session without ending the grant.
func newLiveSession(t *testing.T, f *testFixture, limits bounds.Limits) (sessionID string, release func()) {
	t.Helper()

	st, err := statebackend.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sessionID = statebackend.NewSessionID(time.Now())
	sess, err := st.Create(context.Background(), statebackend.SessionMeta{
		SessionID: sessionID,
		Profile:   "default",
		Status:    sessionv1.SessionStatus_SESSION_STATUS_RUNNING,
		StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })

	live := sessionstate.NewLive(sess, f.bus, limits, nil, nil, nil, nil)
	f.sessions.Put(sessionID, live)
	t.Cleanup(func() { f.sessions.Remove(sessionID) })

	key := sessionscope.KeyFor(f.server.producer)
	release = f.scopes.Grant(key, sessionID)
	return sessionID, release
}

func TestServer_authorizedSession_emptySessionID(t *testing.T) {
	t.Parallel()
	f := newTestServer(t, testProducer())

	_, err := f.server.authorizedSession(t.Context(), "")
	assertCode(t, err, codes.PermissionDenied)
}

func TestServer_authorizedSession_neverGranted(t *testing.T) {
	t.Parallel()
	f := newTestServer(t, testProducer())

	_, err := f.server.authorizedSession(t.Context(), "sess-never-granted")
	assertCode(t, err, codes.PermissionDenied)
}

func TestServer_authorizedSession_grantedButNotLive(t *testing.T) {
	t.Parallel()
	f := newTestServer(t, testProducer())

	// The session was live and granted, then ended (removed from the live
	// table) while the grant was still outstanding — e.g. the session
	// ended between the grant being taken and this call arriving. Must
	// fail identically to the never-granted case, not surface as though
	// the session simply doesn't exist.
	sessionID, release := newLiveSession(t, f, bounds.Limits{})
	t.Cleanup(release)
	f.sessions.Remove(sessionID)

	_, err := f.server.authorizedSession(t.Context(), sessionID)
	assertCode(t, err, codes.PermissionDenied)
}

func TestServer_authorizedSession_indistinguishable(t *testing.T) {
	t.Parallel()
	f := newTestServer(t, testProducer())

	_, neverGrantedErr := f.server.authorizedSession(t.Context(), "sess-a")

	key := sessionscope.KeyFor(f.server.producer)
	releaseGrant := f.scopes.Grant(key, "sess-b")
	t.Cleanup(releaseGrant)
	_, grantedNotLiveErr := f.server.authorizedSession(t.Context(), "sess-b")

	stNever, ok := status.FromError(neverGrantedErr)
	if !ok {
		t.Fatalf("never-granted error %v is not a gRPC status error", neverGrantedErr)
	}
	stGrantedNotLive, ok := status.FromError(grantedNotLiveErr)
	if !ok {
		t.Fatalf("granted-not-live error %v is not a gRPC status error", grantedNotLiveErr)
	}
	if stNever.Code() != stGrantedNotLive.Code() {
		t.Errorf("codes differ: never-granted = %v, granted-not-live = %v, want identical", stNever.Code(), stGrantedNotLive.Code())
	}
	if stNever.Message() != stGrantedNotLive.Message() {
		t.Errorf("messages differ: never-granted = %q, granted-not-live = %q, want identical", stNever.Message(), stGrantedNotLive.Message())
	}
	if stNever.Code() != codes.PermissionDenied {
		t.Errorf("code = %v, want codes.PermissionDenied", stNever.Code())
	}
}

func TestServer_authorizedSession_success(t *testing.T) {
	t.Parallel()
	f := newTestServer(t, testProducer())
	sessionID, release := newLiveSession(t, f, bounds.Limits{})
	t.Cleanup(release)

	live, err := f.server.authorizedSession(t.Context(), sessionID)
	if err != nil {
		t.Fatalf("authorizedSession: unexpected error: %v", err)
	}
	if live == nil {
		t.Fatal("authorizedSession returned a nil *sessionstate.Live")
	}
}

func TestServer_GetSession_authorizationFailure(t *testing.T) {
	t.Parallel()
	f := newTestServer(t, testProducer())

	_, err := f.server.GetSession(t.Context(), &kernelv1.GetSessionRequest{SessionId: "no-such-session"})
	assertCode(t, err, codes.PermissionDenied)
}

func TestServer_GetSession_returnsPersistedAndLiveHalves(t *testing.T) {
	t.Parallel()
	f := newTestServer(t, testProducer())
	sessionID, release := newLiveSession(t, f, bounds.Limits{MaxCostUSD: 10})
	t.Cleanup(release)

	live, ok := f.sessions.Get(sessionID)
	if !ok {
		t.Fatalf("test setup: session %q not registered live", sessionID)
	}
	cost := statebackend.CostEntry{ProviderName: "anthropic", ModelID: "claude", CostUSD: 2.5}
	now := time.Unix(1700000000, 0).UTC()
	if _, err := live.AppendMessage(t.Context(), statebackend.Event{
		ID:            statebackend.NewEventID(now),
		Timestamp:     now,
		Kind:          kernelv1.EventKind_EVENT_KIND_MESSAGE,
		Producer:      testProducer(),
		SchemaVersion: "1",
		Payload:       []byte("hi"),
	}, cost); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	// The debit is deliberately separate from the append: the session
	// driver owns it (internal/session's absorb, once per turn), so
	// AppendMessage persists the cost_ledger row and moves no tracker.
	// This test asserts GetSession reports both halves — the persisted
	// rollup and the live tracker — so it has to set the live half up the
	// same way production does.
	live.Budget().Debit(cost.CostUSD)

	result, err := f.server.GetSession(t.Context(), &kernelv1.GetSessionRequest{SessionId: sessionID})
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}

	if result.GetInfo().GetSessionId() != sessionID {
		t.Errorf("Info.SessionId = %q, want %q", result.GetInfo().GetSessionId(), sessionID)
	}
	if result.GetInfo().GetProfile() != "default" {
		t.Errorf("Info.Profile = %q, want %q", result.GetInfo().GetProfile(), "default")
	}
	if result.GetInfo().GetStatus() != sessionv1.SessionStatus_SESSION_STATUS_RUNNING {
		t.Errorf("Info.Status = %v, want SESSION_STATUS_RUNNING", result.GetInfo().GetStatus())
	}
	if result.GetInfo().GetCostUsd() != cost.CostUSD {
		t.Errorf("Info.CostUsd = %v, want %v (persisted rollup)", result.GetInfo().GetCostUsd(), cost.CostUSD)
	}
	wantRemaining := 10 - cost.CostUSD
	if result.GetRemainingCostBudgetUsd() != wantRemaining {
		t.Errorf("RemainingCostBudgetUsd = %v, want %v (live budget tracker)", result.GetRemainingCostBudgetUsd(), wantRemaining)
	}
	if result.GetRemainingDepth() != rootSessionRemainingDepth {
		t.Errorf("RemainingDepth = %v, want the root-sessions-only placeholder %v", result.GetRemainingDepth(), rootSessionRemainingDepth)
	}
}

func TestServer_GetSession_metaQueryFailure(t *testing.T) {
	t.Parallel()
	f := newTestServer(t, testProducer())
	sessionID, release := newLiveSession(t, f, bounds.Limits{})
	t.Cleanup(release)

	live, ok := f.sessions.Get(sessionID)
	if !ok {
		t.Fatalf("test setup: session %q not registered live", sessionID)
	}
	// As in TestServer_Emit_liveWriteFailure: Close the underlying session
	// directly, leaving it in the live table and still granted, to
	// exercise GetSession's own meta-query-failure branch (codes.Internal).
	if err := live.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, err := f.server.GetSession(t.Context(), &kernelv1.GetSessionRequest{SessionId: sessionID})
	assertCode(t, err, codes.Internal)
}

func TestServer_GetSession_noSpendOmitsCostUsd(t *testing.T) {
	t.Parallel()
	f := newTestServer(t, testProducer())
	sessionID, release := newLiveSession(t, f, bounds.Limits{})
	t.Cleanup(release)

	result, err := f.server.GetSession(t.Context(), &kernelv1.GetSessionRequest{SessionId: sessionID})
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if result.GetInfo().CostUsd != nil {
		t.Errorf("Info.CostUsd = %v, want nil (no spend yet)", result.GetInfo().GetCostUsd())
	}
}
