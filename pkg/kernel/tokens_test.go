package kernel_test

import (
	"errors"
	"testing"

	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
)

func TestClient_CountTokens(t *testing.T) {
	t.Parallel()

	var gotReq *kernelv1.CountTokensRequest
	srv := &fakeServer{
		countTokensFunc: func(req *kernelv1.CountTokensRequest) (*kernelv1.CountTokensResult, error) {
			gotReq = req
			return &kernelv1.CountTokensResult{Count: 42, Exact: true}, nil
		},
	}
	c := newTestClient(t, srv)

	req := &kernelv1.CountTokensRequest{
		Content: []*contentv1.ContentBlock{{}},
	}
	result, err := c.CountTokens(t.Context(), req)
	if err != nil {
		t.Fatalf("CountTokens: %v", err)
	}
	if result.GetCount() != 42 || !result.GetExact() {
		t.Errorf("CountTokens() = %+v, want count=42 exact=true", result)
	}
	if len(gotReq.GetContent()) != 1 {
		t.Errorf("server received %+v, want one content block", gotReq)
	}
}

func TestClient_CountTokens_error(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	srv := &fakeServer{
		countTokensFunc: func(*kernelv1.CountTokensRequest) (*kernelv1.CountTokensResult, error) {
			return nil, wantErr
		},
	}
	c := newTestClient(t, srv)

	if _, err := c.CountTokens(t.Context(), &kernelv1.CountTokensRequest{}); err == nil {
		t.Fatal("CountTokens: want error, got nil")
	}
}
