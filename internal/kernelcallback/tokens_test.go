package kernelcallback

import (
	"testing"

	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"

	"google.golang.org/grpc/codes"
)

func TestServer_CountTokens_validation(t *testing.T) {
	t.Parallel()
	f := newTestServer(t, testProducer())

	_, err := f.server.CountTokens(t.Context(), &kernelv1.CountTokensRequest{})
	assertCode(t, err, codes.InvalidArgument)
}

func TestServer_CountTokens_delegatesToCounter(t *testing.T) {
	t.Parallel()
	f := newTestServer(t, testProducer())

	// No ModelRef is supplied, and fakeModelLookup never has a provider
	// loaded either way — this exercises the fallback heuristic path,
	// ceil(utf8_byte_length/4), through the real *tokencount.Counter this
	// package's Server was constructed with.
	blocks := []*contentv1.ContentBlock{
		{Block: &contentv1.ContentBlock_Text{Text: &contentv1.TextBlock{Text: "12345678"}}},
	}
	result, err := f.server.CountTokens(t.Context(), &kernelv1.CountTokensRequest{Content: blocks})
	if err != nil {
		t.Fatalf("CountTokens: %v", err)
	}
	if result.GetExact() {
		t.Error("Exact = true, want false (no model provider loaded)")
	}
	if result.GetCount() != 2 {
		t.Errorf("Count = %d, want 2 (ceil(8/4))", result.GetCount())
	}
}
