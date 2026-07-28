package session

import (
	"context"
	"testing"

	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	sessionv1 "github.com/pluggableharness/agent/pkg/session/proto/v1"

	"github.com/pluggableharness/agent/internal/agentprofile"
)

func TestHandle_OpenSubmitStaysRunning(t *testing.T) {
	t.Parallel()

	h := newHarness(t, profileWith(func(p *agentprofile.AgentProfile) {
		p.Tools = []string{"filesystem.*"}
	}), []step{done(0.10)}, true)

	handle, err := h.runner.Open(context.Background(), Spec{
		Prompt:           "",
		WorkingDirectory: "/work",
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close(context.Background()) })

	if _, ok := h.table.Get(handle.SessionID()); !ok {
		t.Fatal("session not registered in live table")
	}

	turnID, err := handle.Submit(context.Background(), []*contentv1.ContentBlock{
		{Block: &contentv1.ContentBlock_Text{Text: &contentv1.TextBlock{Text: "hi"}}},
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if turnID == "" {
		t.Fatal("turn_id empty")
	}

	state, err := handle.State(context.Background())
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if state.GetInfo().GetStatus() != sessionv1.SessionStatus_SESSION_STATUS_RUNNING {
		t.Fatalf("status after submit = %v, want RUNNING", state.GetInfo().GetStatus())
	}
	if state.GetWorkingDirectory() != "/work" {
		t.Fatalf("working_directory = %q", state.GetWorkingDirectory())
	}

	// Second submit should also work (session stays open; scripted turn
	// repeats its last done step).
	if _, err := handle.Submit(context.Background(), []*contentv1.ContentBlock{
		{Block: &contentv1.ContentBlock_Text{Text: &contentv1.TextBlock{Text: "again"}}},
	}); err != nil {
		t.Fatalf("second Submit: %v", err)
	}
}
