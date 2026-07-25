package kernel

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	sessionv1 "github.com/pluggableharness/agent/pkg/session/proto/v1"

	"github.com/pluggableharness/agent/internal/bounds"
	"github.com/pluggableharness/agent/internal/config"
	"github.com/pluggableharness/agent/internal/eventbus"
	"github.com/pluggableharness/agent/internal/sessionstate"
	"github.com/pluggableharness/agent/internal/statebackend"
	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/noop"
	"github.com/pluggableharness/agent/internal/turn"
)

// newTestSession opens a real session file under t.TempDir. The sink
// forwards to *statebackend.Session's own append methods, so a fake would
// only prove the forwarding compiles — this proves it persists.
func newTestSession(t *testing.T) *statebackend.Session {
	t.Helper()

	store, err := statebackend.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	now := time.Now()
	sess, err := store.Create(context.Background(), statebackend.SessionMeta{
		SessionID: statebackend.NewSessionID(now),
		Profile:   "default",
		Status:    sessionv1.SessionStatus_SESSION_STATUS_RUNNING,
		StartedAt: now,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

// newTestLiveSession wraps a real session file as the *sessionstate.Live
// the sink actually binds. The sink takes a Live rather than the raw
// handle so that every kernel-originated event republishes onto
// kernel.event.{kind}; see sessionSink's own doc comment.
func newTestLiveSession(t *testing.T) *sessionstate.Live {
	t.Helper()

	bus := eventbus.New()
	t.Cleanup(func() { _ = bus.Close() })
	return sessionstate.NewLive(newTestSession(t), bus, bounds.Limits{}, nil, nil, nil, nil)
}

// testEvent returns an event a plugin-shaped producer could have emitted.
func testEvent(now time.Time) statebackend.Event {
	return statebackend.Event{
		ID:            statebackend.NewEventID(now),
		Timestamp:     now,
		Kind:          kernelv1.EventKind_EVENT_KIND_HOOK_ERROR,
		SchemaVersion: "1",
		Payload:       []byte(`{}`),
		Producer: &commonv1.ProducerRef{
			Category: commonv1.Category_CATEGORY_TOOL,
			Name:     "test-tool",
			Version:  "1",
		},
	}
}

func TestSessionSink_unboundRefusesEveryAppend(t *testing.T) {
	t.Parallel()

	var sink sessionSink
	ctx, ev := context.Background(), testEvent(time.Now())

	if _, err := sink.AppendEvent(ctx, ev); !errors.Is(err, ErrNoLiveSession) {
		t.Errorf("AppendEvent unbound = %v, want ErrNoLiveSession", err)
	}
	if _, err := sink.AppendMessage(ctx, ev, statebackend.CostEntry{}); !errors.Is(err, ErrNoLiveSession) {
		t.Errorf("AppendMessage unbound = %v, want ErrNoLiveSession", err)
	}
	if _, err := sink.AppendPlan(ctx, ev, nil); !errors.Is(err, ErrNoLiveSession) {
		t.Errorf("AppendPlan unbound = %v, want ErrNoLiveSession", err)
	}
}

func TestSessionSink_boundForwardsToTheSession(t *testing.T) {
	t.Parallel()

	var sink sessionSink
	sink.bind(newTestLiveSession(t))

	seq, err := sink.AppendEvent(context.Background(), testEvent(time.Now()))
	if err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	if seq <= 0 {
		t.Errorf("AppendEvent returned sequence %d, want the session's own assigned sequence", seq)
	}
}

// TestSessionSink_rebindRetargets asserts the atomic swap actually takes
// effect — the property the whole late-binding seam depends on.
func TestSessionSink_rebindRetargets(t *testing.T) {
	t.Parallel()

	first, second := newTestLiveSession(t), newTestLiveSession(t)
	var sink sessionSink

	sink.bind(first)
	if _, err := sink.AppendEvent(context.Background(), testEvent(time.Now())); err != nil {
		t.Fatalf("AppendEvent on first: %v", err)
	}
	sink.bind(second)
	seq, err := sink.AppendEvent(context.Background(), testEvent(time.Now()))
	if err != nil {
		t.Fatalf("AppendEvent on second: %v", err)
	}
	if seq != 1 {
		t.Errorf("first append against the rebound session got sequence %d, want 1", seq)
	}
}

// newStackKernel returns the minimum kernel a turnStack needs: a live
// session table, a logger, a fully-disabled telemetry Provider, and the
// settings the per-session collaborators read. Catalog is deliberately
// left nil — turn.New is what rejects it, which is exactly the branch the
// tests below want to land on after the sink has already been bound.
func newStackKernel(t *testing.T) *kernel {
	t.Helper()

	prov, err := telemetry.New(context.Background(), telemetry.Config{}, noop.New(), nil)
	if err != nil {
		t.Fatalf("telemetry.New: %v", err)
	}
	t.Cleanup(func() { _ = prov.Shutdown(context.Background()) })

	return &kernel{
		logger:   slog.New(slog.DiscardHandler),
		telem:    prov,
		sessions: sessionstate.NewTable(),
		sink:     &sessionSink{},
		cfg: &config.Config{Settings: config.Settings{
			Retry:                config.DefaultRetrySettings,
			DoomLoop:             config.DefaultDoomLoopSettings,
			EventBus:             config.DefaultEventBus,
			DefaultHookTimeoutMS: config.DefaultHookTimeoutMS,
			DefaultToolTimeoutMS: config.DefaultToolTimeoutMS,
		}},
	}
}

func TestTurnStack_unknownSessionIsAnError(t *testing.T) {
	t.Parallel()

	stack := newTurnStack(newStackKernel(t))
	_, err := stack.RunTurn(context.Background(), turn.Request{SessionID: "sess-nope"})
	if err == nil || !strings.Contains(err.Error(), "not in the live-session table") {
		t.Fatalf("RunTurn for an unregistered session = %v, want a live-session-table error", err)
	}
}

// TestTurnStack_refusesASecondSession locks in the one-root-session-per-
// process assumption: silently rebuilding would rebind the shared sink out
// from under the first session.
func TestTurnStack_refusesASecondSession(t *testing.T) {
	t.Parallel()

	k := newStackKernel(t)
	stack := newTurnStack(k)
	stack.sessionID = "sess-first"
	stack.driver = failingDriver{}

	if _, err := stack.RunTurn(context.Background(), turn.Request{SessionID: "sess-first"}); !errors.Is(err, errFakeDriver) {
		t.Fatalf("RunTurn for the bound session = %v, want it to reach the built driver", err)
	}
	_, err := stack.RunTurn(context.Background(), turn.Request{SessionID: "sess-second"})
	if err == nil || !strings.Contains(err.Error(), "one root session per process") {
		t.Fatalf("RunTurn for a second session = %v, want a refusal", err)
	}
}

var errFakeDriver = errors.New("fake driver reached")

// failingDriver stands in for a built *turn.Driver, proving delegation
// happened without needing a live plugin behind it.
type failingDriver struct{}

func (failingDriver) RunTurn(context.Context, turn.Request) (turn.Result, error) {
	return turn.Result{}, errFakeDriver
}

// TestTurnStack_bindsTheSinkOnFirstTurn is the seam's core contract: the
// sink is unusable until a turn names its session, and usable after.
func TestTurnStack_bindsTheSinkOnFirstTurn(t *testing.T) {
	t.Parallel()

	k := newStackKernel(t)
	sess := newTestSession(t)
	bus := eventbus.New()
	t.Cleanup(func() { _ = bus.Close() })
	live := sessionstate.NewLive(sess, bus, bounds.Limits{}, nil, nil, nil, nil)
	k.sessions.Put(sess.ID(), live)

	if _, err := k.sink.AppendEvent(context.Background(), testEvent(time.Now())); !errors.Is(err, ErrNoLiveSession) {
		t.Fatalf("sink before the first turn = %v, want ErrNoLiveSession", err)
	}

	// newTurnDriver fails past the bind (there is no catalog here), which
	// is fine: the bind happens first, deliberately, so every collaborator
	// built after it already has a live sink.
	if _, err := k.newTurnDriver(context.Background(), sess.ID()); err == nil {
		t.Fatal("newTurnDriver with no catalog succeeded, want an error")
	}
	if _, err := k.sink.AppendEvent(context.Background(), testEvent(time.Now())); err != nil {
		t.Fatalf("sink after the bind = %v, want it to persist", err)
	}
}
