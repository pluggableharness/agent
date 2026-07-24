package kernel_test

import (
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
)

func TestClient_GetConfig(t *testing.T) {
	t.Parallel()

	want, err := structpb.NewStruct(map[string]any{"timeout_ms": float64(5000)})
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}
	srv := &fakeServer{
		getConfigFunc: func(*kernelv1.GetConfigRequest) (*kernelv1.GetConfigResult, error) {
			return &kernelv1.GetConfigResult{Config: want}, nil
		},
	}
	c := newTestClient(t, srv)

	got, err := c.GetConfig(t.Context())
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if got.GetFields()["timeout_ms"].GetNumberValue() != 5000 {
		t.Errorf("GetConfig() = %v, want timeout_ms=5000", got)
	}
}
