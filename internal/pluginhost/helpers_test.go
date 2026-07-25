package pluginhost

import (
	"context"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/fake"
)

// parseHCLBody parses src as one provider{} block's body.
func parseHCLBody(t *testing.T, src string) hcl.Body {
	t.Helper()

	file, diags := hclparse.NewParser().ParseHCL([]byte(src), "agent.hcl")
	if diags.HasErrors() {
		t.Fatalf("parse %q: %v", src, diags)
	}
	return file.Body
}

// mustTelemetry builds a *telemetry.Provider wired to the in-memory fake
// backend, shut down at test cleanup — mirroring the same helper in
// internal/config and internal/registry.
func mustTelemetry(t *testing.T) *telemetry.Provider {
	t.Helper()

	prov, err := telemetry.New(context.Background(), telemetry.DefaultConfig, fake.New(), nil)
	if err != nil {
		t.Fatalf("telemetry.New: %v", err)
	}
	t.Cleanup(func() {
		if err := prov.Shutdown(context.Background()); err != nil {
			t.Errorf("telemetry.Shutdown: %v", err)
		}
	})
	return prov
}

// mustStruct builds a *structpb.Struct from a plain map, failing the test
// rather than returning an error a caller would have to handle inline.
func mustStruct(t *testing.T, m map[string]any) *structpb.Struct {
	t.Helper()

	s, err := structpb.NewStruct(m)
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}
	return s
}
