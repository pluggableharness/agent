package kernel_test

import (
	"errors"
	"testing"

	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
)

func TestClient_RunSession(t *testing.T) {
	t.Parallel()

	var gotReq *kernelv1.RunSessionRequest
	srv := &fakeServer{
		runSessionFunc: func(req *kernelv1.RunSessionRequest) (*kernelv1.RunSessionResult, error) {
			gotReq = req
			return &kernelv1.RunSessionResult{
				SessionId:    "child-01",
				TotalCostUsd: 0.5,
			}, nil
		},
	}
	c := newTestClient(t, srv)

	req := &kernelv1.RunSessionRequest{
		Profile:                "reviewer",
		Prompt:                 "review this diff",
		ParentSessionId:        "parent-01",
		RemainingDepth:         3,
		RemainingCostBudgetUsd: 10,
	}
	result, err := c.RunSession(t.Context(), req)
	if err != nil {
		t.Fatalf("RunSession: %v", err)
	}
	if result.GetSessionId() != "child-01" || result.GetTotalCostUsd() != 0.5 {
		t.Errorf("RunSession() = %+v, want session_id=child-01 total_cost_usd=0.5", result)
	}
	if gotReq.GetProfile() != "reviewer" || gotReq.GetParentSessionId() != "parent-01" {
		t.Errorf("server received %+v, want profile=reviewer parent_session_id=parent-01", gotReq)
	}
}

func TestClient_RunSession_error(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	srv := &fakeServer{
		runSessionFunc: func(*kernelv1.RunSessionRequest) (*kernelv1.RunSessionResult, error) {
			return nil, wantErr
		},
	}
	c := newTestClient(t, srv)

	if _, err := c.RunSession(t.Context(), &kernelv1.RunSessionRequest{}); err == nil {
		t.Fatal("RunSession: want error, got nil")
	}
}

func TestClient_GetSession(t *testing.T) {
	t.Parallel()

	var gotReq *kernelv1.GetSessionRequest
	srv := &fakeServer{
		getSessionFunc: func(req *kernelv1.GetSessionRequest) (*kernelv1.GetSessionResult, error) {
			gotReq = req
			return &kernelv1.GetSessionResult{
				RemainingDepth:         2,
				RemainingCostBudgetUsd: 3.25,
			}, nil
		},
	}
	c := newTestClient(t, srv)

	result, err := c.GetSession(t.Context(), "session-01")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if result.GetRemainingDepth() != 2 || result.GetRemainingCostBudgetUsd() != 3.25 {
		t.Errorf("GetSession() = %+v, want remaining_depth=2 remaining_cost_budget_usd=3.25", result)
	}
	if gotReq.GetSessionId() != "session-01" {
		t.Errorf("server received %+v, want session_id=session-01", gotReq)
	}
}

func TestClient_GetSession_error(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	srv := &fakeServer{
		getSessionFunc: func(*kernelv1.GetSessionRequest) (*kernelv1.GetSessionResult, error) {
			return nil, wantErr
		},
	}
	c := newTestClient(t, srv)

	if _, err := c.GetSession(t.Context(), "session-01"); err == nil {
		t.Fatal("GetSession: want error, got nil")
	}
}
