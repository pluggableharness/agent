package tooldispatch

import (
	"context"
	"log/slog"
	"testing"

	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
)

// testLogger returns a *slog.Logger that discards output — tests assert
// on return values and recorded fakes, never on log lines, but every
// Scheduler still needs a non-nil Logger to exercise its logging calls
// under -race.
func testLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(testDiscard{}, nil))
}

type testDiscard struct{}

func (testDiscard) Write(p []byte) (int, error) { return len(p), nil }

func TestNew_DefaultsLoggerAndTelemetry(t *testing.T) {
	t.Parallel()
	s := New(Config{Events: &fakeEvents{}})
	if s.cfg.Logger == nil {
		t.Fatal("New: Logger not defaulted")
	}
	if s.cfg.Telemetry == nil {
		t.Fatal("New: Telemetry not defaulted")
	}
}

func TestNew_HonorsExplicitConfig(t *testing.T) {
	t.Parallel()
	logger := testLogger(t)
	s := New(Config{Events: &fakeEvents{}, Logger: logger, SerializeAll: true})
	if s.cfg.Logger != logger {
		t.Fatal("New: explicit Logger overwritten")
	}
	if !s.cfg.SerializeAll {
		t.Fatal("New: SerializeAll not carried through")
	}
}

func TestConcurrencyKey(t *testing.T) {
	t.Parallel()
	args := mustStruct(t, map[string]any{"path": "a.go", "other": "x"})

	tests := []struct {
		name       string
		spec       *toolv1.ConcurrencySpec
		wantSafe   bool
		wantHasKey bool
	}{
		{
			name:     "nil spec is unsafe (conservative default)",
			spec:     nil,
			wantSafe: false,
		},
		{
			name:     "safe false is unsafe regardless of key_fields",
			spec:     &toolv1.ConcurrencySpec{Safe: false, KeyFields: []string{"path"}},
			wantSafe: false,
		},
		{
			name:       "safe true, no key_fields: no per-key lock",
			spec:       &toolv1.ConcurrencySpec{Safe: true},
			wantSafe:   true,
			wantHasKey: false,
		},
		{
			name:       "safe true with key_fields: per-key lock",
			spec:       &toolv1.ConcurrencySpec{Safe: true, KeyFields: []string{"path"}},
			wantSafe:   true,
			wantHasKey: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			safe, key, hasKey := concurrencyKey("fs", "write_file", args, tt.spec)
			if safe != tt.wantSafe {
				t.Errorf("safe = %v, want %v", safe, tt.wantSafe)
			}
			if hasKey != tt.wantHasKey {
				t.Errorf("hasKey = %v, want %v", hasKey, tt.wantHasKey)
			}
			if hasKey && key == "" {
				t.Error("hasKey true but key is empty")
			}
		})
	}
}

func TestConcurrencyKey_DistinctArgsProduceDistinctKeys(t *testing.T) {
	t.Parallel()
	spec := &toolv1.ConcurrencySpec{Safe: true, KeyFields: []string{"path"}}
	a := mustStruct(t, map[string]any{"path": "a.go"})
	b := mustStruct(t, map[string]any{"path": "b.go"})

	_, keyA, _ := concurrencyKey("fs", "write_file", a, spec)
	_, keyB, _ := concurrencyKey("fs", "write_file", b, spec)
	if keyA == keyB {
		t.Fatalf("distinct key_fields values produced the same key %q", keyA)
	}

	_, keyA2, _ := concurrencyKey("fs", "write_file", a, spec)
	if keyA != keyA2 {
		t.Fatalf("identical key_fields values produced different keys: %q vs %q", keyA, keyA2)
	}
}

func TestAcquireLocks_SerializeAllSkipsLocking(t *testing.T) {
	t.Parallel()
	s := New(Config{Events: &fakeEvents{}, Logger: testLogger(t), SerializeAll: true})
	release, err := s.acquireLocks(context.Background(), "p", false, "", false)
	if err != nil {
		t.Fatalf("acquireLocks: %v", err)
	}
	release()
	if len(s.providerSems) != 0 {
		t.Fatalf("SerializeAll: provider semaphore map should stay empty, got %d entries", len(s.providerSems))
	}
}

func TestAcquireLocks_ExclusiveExcludesShared(t *testing.T) {
	t.Parallel()
	s := New(Config{Events: &fakeEvents{}, Logger: testLogger(t)})

	// Take the exclusive (unsafe) lock first.
	releaseExclusive, err := s.acquireLocks(context.Background(), "p", false, "", false)
	if err != nil {
		t.Fatalf("acquireLocks (exclusive): %v", err)
	}

	// A shared acquire on the same provider must not succeed while the
	// exclusive holder is still active.
	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()
	<-ctx.Done() // already expired: acquire must fail immediately, not block
	if _, err := s.acquireLocks(ctx, "p", true, "", false); err == nil {
		t.Fatal("acquireLocks: shared acquire unexpectedly succeeded while exclusive lock held")
	}

	releaseExclusive()

	release2, err := s.acquireLocks(context.Background(), "p", true, "", false)
	if err != nil {
		t.Fatalf("acquireLocks (shared, after release): %v", err)
	}
	release2()
}
