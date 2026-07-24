package kernelcallback

import (
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
)

func TestServer_GetConfig_returnsResolvedConfig(t *testing.T) {
	t.Parallel()

	want, err := structpb.NewStruct(map[string]any{"api_key_ref": "resolved", "timeout_ms": float64(5000)})
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}
	f := newTestServer(t, testProducer(), func(cfg *Config) {
		cfg.ResolvedConfig = want
	})

	got, err := f.server.GetConfig(t.Context(), &kernelv1.GetConfigRequest{})
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if got.GetConfig().GetFields()["timeout_ms"].GetNumberValue() != 5000 {
		t.Errorf("GetConfig().Config = %v, want the fixture's resolved config", got.GetConfig())
	}
}

func TestServer_GetConfig_nilResolvedConfigReturnsEmptyStruct(t *testing.T) {
	t.Parallel()
	f := newTestServer(t, testProducer())

	got, err := f.server.GetConfig(t.Context(), &kernelv1.GetConfigRequest{})
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if got.GetConfig() == nil {
		t.Fatal("GetConfig().Config is nil, want an empty (non-nil) Struct")
	}
	if len(got.GetConfig().GetFields()) != 0 {
		t.Errorf("GetConfig().Config = %v, want empty", got.GetConfig())
	}
}
