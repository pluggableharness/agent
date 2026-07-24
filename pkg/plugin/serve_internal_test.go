package plugin

import (
	"errors"
	"testing"

	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	"github.com/pluggableharness/agent/pkg/common"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
)

// This file is a white-box (package plugin, not plugin_test) test,
// deliberately, for the same reason callback_internal_test.go is: it needs
// grpcPlugin and pluginSet/serveConfig, which are unexported by design
// (Serve itself blocks forever in real operation and is not something a
// unit test can call directly — only its constituent, non-blocking pieces
// are testable here, following the same split-for-testability reasoning
// internal/pluginruntime/launch.go's buildClient uses).

// fakeService records the *grpc.Server it was registered on, for
// asserting grpcPlugin.GRPCServer actually plumbed it through.
type fakeService struct {
	registered *grpc.Server
}

func (f *fakeService) Register(s *grpc.Server) {
	f.registered = s
}

func TestGRPCPlugin_GRPCServer_registersServicesAndBroker(t *testing.T) {
	t.Parallel()

	svc1 := &fakeService{}
	svc2 := &fakeService{}
	cb := NewCallback()
	p := &grpcPlugin{cfg: Config{
		Identity: Identity{Name: "fixture"},
		Category: commonv1.Category_CATEGORY_TOOL,
		Callback: cb,
		Services: []Service{svc1, svc2},
	}}

	s := grpc.NewServer()
	broker := &plugin.GRPCBroker{}

	if err := p.GRPCServer(broker, s); err != nil {
		t.Fatalf("GRPCServer() error = %v, want nil", err)
	}

	if svc1.registered != s {
		t.Errorf("svc1.registered = %v, want %v", svc1.registered, s)
	}
	if svc2.registered != s {
		t.Errorf("svc2.registered = %v, want %v", svc2.registered, s)
	}

	cb.mu.Lock()
	gotBroker := cb.broker
	cb.mu.Unlock()
	if gotBroker != broker {
		t.Errorf("Callback.broker = %v, want %v", gotBroker, broker)
	}
}

func TestGRPCPlugin_GRPCServer_nilCallback(t *testing.T) {
	t.Parallel()

	p := &grpcPlugin{cfg: Config{Services: []Service{&fakeService{}}}}

	if err := p.GRPCServer(&plugin.GRPCBroker{}, grpc.NewServer()); err != nil {
		t.Fatalf("GRPCServer() error = %v, want nil", err)
	}
}

func TestGRPCPlugin_GRPCClient(t *testing.T) {
	t.Parallel()

	p := &grpcPlugin{}

	_, err := p.GRPCClient(t.Context(), nil, nil)
	if !errors.Is(err, errGRPCClientUnsupported) {
		t.Fatalf("GRPCClient() error = %v, want errGRPCClientUnsupported", err)
	}
}

func TestPluginSet(t *testing.T) {
	t.Parallel()

	cfg := Config{Category: commonv1.Category_CATEGORY_MODEL}
	set := pluginSet(cfg)

	if got, want := len(set), 1; got != want {
		t.Fatalf("len(pluginSet(cfg)) = %d, want %d", got, want)
	}
	key := common.PluginKey(cfg.Category)
	if _, ok := set[key]; !ok {
		t.Errorf("pluginSet(cfg) missing key %q, got keys %v", key, mapKeys(set))
	}
}

func TestServeConfig(t *testing.T) {
	t.Parallel()

	cfg := Config{Category: commonv1.Category_CATEGORY_MODEL}
	sc := serveConfig(cfg)

	if sc.HandshakeConfig != common.Handshake {
		t.Errorf("HandshakeConfig = %+v, want %+v", sc.HandshakeConfig, common.Handshake)
	}
	if sc.GRPCServer == nil {
		t.Error("GRPCServer is nil, want plugin.DefaultGRPCServer")
	}
	if got, want := len(sc.Plugins), 1; got != want {
		t.Errorf("len(Plugins) = %d, want %d", got, want)
	}
}

func mapKeys(m plugin.PluginSet) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
