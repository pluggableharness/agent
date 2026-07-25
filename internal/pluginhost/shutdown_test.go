package pluginhost

// Unit tier: Shutdown's ordering and error-aggregation behavior, plus
// the two Config-to-value builders, all driven through Live.closeFn
// rather than a real subprocess. The launch sequence those values feed
// is integration-tier.

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/pluggableharness/agent/internal/providerresolve"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
)

// recordingCloser builds a Live whose teardown appends its name to
// order, optionally failing with err.
func recordingCloser(name string, index int, mu *sync.Mutex, order *[]string, err error) *Live {
	return &Live{
		LocalName:   name,
		LaunchIndex: index,
		Producer:    &commonv1.ProducerRef{Name: name, Category: commonv1.Category_CATEGORY_TOOL},
		closeFn: func(context.Context) error {
			mu.Lock()
			*order = append(*order, name)
			mu.Unlock()
			return err
		},
	}
}

func TestShutdown_reverseLaunchOrder(t *testing.T) {
	var (
		mu    sync.Mutex
		order []string
	)

	s, err := NewSupervisor(testDeps(t))
	if err != nil {
		t.Fatalf("NewSupervisor: %v", err)
	}
	s.launched = []*Live{
		recordingCloser("first", 0, &mu, &order, nil),
		recordingCloser("second", 1, &mu, &order, nil),
		recordingCloser("third", 2, &mu, &order, nil),
	}

	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if want := []string{"third", "second", "first"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("teardown order = %v, want %v (the reverse of launch order)", order, want)
	}
}

// TestShutdown_continuesPastAFailure is the contract that separates
// Shutdown from Start: one plugin failing to close must not leave the
// rest running, and every failure must still surface.
func TestShutdown_continuesPastAFailure(t *testing.T) {
	var (
		mu    sync.Mutex
		order []string
	)
	boom := errors.New("boom")
	worse := errors.New("worse")

	s, err := NewSupervisor(testDeps(t))
	if err != nil {
		t.Fatalf("NewSupervisor: %v", err)
	}
	s.launched = []*Live{
		recordingCloser("first", 0, &mu, &order, boom),
		recordingCloser("second", 1, &mu, &order, nil),
		recordingCloser("third", 2, &mu, &order, worse),
	}

	shutdownErr := s.Shutdown(context.Background())
	if shutdownErr == nil {
		t.Fatal("Shutdown = nil error, want the joined teardown failures")
	}
	if !errors.Is(shutdownErr, boom) || !errors.Is(shutdownErr, worse) {
		t.Errorf("Shutdown = %v, want both teardown failures joined", shutdownErr)
	}
	if want := []string{"third", "second", "first"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("teardown order = %v, want %v — a failure must not abort the rest", order, want)
	}

	// A second call is a no-op even after a failing first one.
	if err := s.Shutdown(context.Background()); err != nil {
		t.Errorf("second Shutdown = %v, want nil", err)
	}
	if len(order) != 3 {
		t.Errorf("teardown ran %d times across two Shutdown calls, want 3", len(order))
	}
}

// TestShutdown_skipsLivesWithNoSubprocess covers the defensive path for
// a Live that never came from a real launch.
func TestShutdown_skipsLivesWithNoSubprocess(t *testing.T) {
	s, err := NewSupervisor(testDeps(t))
	if err != nil {
		t.Fatalf("NewSupervisor: %v", err)
	}
	s.launched = []*Live{{LocalName: "no-subprocess", Producer: &commonv1.ProducerRef{Name: "x"}}}

	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestLaunchConfig(t *testing.T) {
	s, err := NewSupervisor(testDeps(t))
	if err != nil {
		t.Fatalf("NewSupervisor: %v", err)
	}

	resolved := providerresolve.Resolved{
		LocalName:  "anthropic",
		Source:     "github.com/agentco/provider-anthropic",
		Version:    "1.2.3",
		Category:   commonv1.Category_CATEGORY_UNSPECIFIED,
		BinaryPath: "/cache/anthropic",
	}
	slot := newCallbackSlot(s.newCallbackServer(provisionalProducer(resolved), nil))

	// A probe launches the same resolved provider under a category the
	// resolution did not know, so launchConfig must take the category it
	// is given rather than the one on Resolved.
	cfg := s.launchConfig(resolved, commonv1.Category_CATEGORY_MODEL, slot)
	if cfg.BinaryPath != resolved.BinaryPath {
		t.Errorf("BinaryPath = %q, want %q", cfg.BinaryPath, resolved.BinaryPath)
	}
	if cfg.Producer.GetCategory() != commonv1.Category_CATEGORY_MODEL {
		t.Errorf("Producer.Category = %v, want the category launchConfig was given", cfg.Producer.GetCategory())
	}
	if cfg.Callback != slot {
		t.Error("Callback is not the slot it was given")
	}
	if cfg.Telemetry == nil || cfg.Logger == nil {
		t.Error("launchConfig did not carry the supervisor's telemetry provider and logger through")
	}
}

func TestNewCallbackServer(t *testing.T) {
	cfg := testDeps(t)
	cfg.BusSubscribeQueueBound = 7
	s, err := NewSupervisor(cfg)
	if err != nil {
		t.Fatalf("NewSupervisor: %v", err)
	}

	producer := &commonv1.ProducerRef{Name: "p", Category: commonv1.Category_CATEGORY_TOOL}
	resolved := mustStruct(t, map[string]any{"api_key": "value"})

	srv := s.newCallbackServer(producer, resolved)
	if srv == nil {
		t.Fatal("newCallbackServer returned nil")
	}

	// The resolved config is what a plugin's own GetConfig sees — the
	// one binding this package is responsible for getting right.
	got, err := srv.GetConfig(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if got.GetConfig().GetFields()["api_key"].GetStringValue() != "value" {
		t.Errorf("GetConfig = %v, want the resolved config it was constructed with", got.GetConfig())
	}
}
