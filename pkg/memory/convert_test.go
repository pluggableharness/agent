package memory_test

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/pluggableharness/agent/pkg/memory"
	memoryv1 "github.com/pluggableharness/agent/pkg/memory/proto/v1"
	"github.com/pluggableharness/agent/pkg/plugin"
)

// exerciseRecordRoundTrip drives a Record through the wire boundary (via a
// Recall call) and returns what came back, black-box — convert.go's
// recordToProto/recordFromProto aren't exported, so this package tests them
// through the public RPC surface, same as every other _test.go in this
// package.
func exerciseRecordRoundTrip(t *testing.T, in memory.Record) *memoryv1.MemoryRecord {
	t.Helper()

	provider := &fakeProvider{recallFunc: func(_ context.Context, _ memory.RecallRequest) (memory.RecallResult, error) {
		return memory.RecallResult{Records: []memory.Record{in}}, nil
	}}
	client := newTestClient(t, memory.NewService(provider, testIdentity(), plugin.NewCallback()))
	resp, err := client.Recall(t.Context(), &memoryv1.RecallRequest{})
	if err != nil {
		t.Fatalf("Recall() error = %v, want nil", err)
	}
	if len(resp.GetRecords()) != 1 {
		t.Fatalf("Recall() returned %d records, want 1", len(resp.GetRecords()))
	}
	return resp.GetRecords()[0]
}

func TestConvert_RecordRoundTrip(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Second)
	score := 0.75
	in := memory.Record{
		ID:             "user-role",
		Type:           memory.TypeUser,
		Scope:          memory.ScopeGlobal,
		Title:          "Role",
		Content:        "Backend engineer.",
		Tokens:         42,
		Status:         memory.RecordStatusCanonical,
		Links:          []string{"other-record"},
		CreatedAt:      now,
		UpdatedAt:      now,
		Provenance:     memory.Provenance{SourceSessionID: "sess-1", SourceTurnID: "turn-1", RecordedBy: "memory.remember"},
		RelevanceScore: &score,
	}

	out := exerciseRecordRoundTrip(t, in)

	if out.GetId() != in.ID {
		t.Errorf("Id = %q, want %q", out.GetId(), in.ID)
	}
	if out.GetType() != memoryv1.MemoryType_MEMORY_TYPE_USER {
		t.Errorf("Type = %v, want MEMORY_TYPE_USER", out.GetType())
	}
	if out.GetScope() != memoryv1.MemoryScope_MEMORY_SCOPE_GLOBAL {
		t.Errorf("Scope = %v, want MEMORY_SCOPE_GLOBAL", out.GetScope())
	}
	if out.GetTitle() != in.Title {
		t.Errorf("Title = %q, want %q", out.GetTitle(), in.Title)
	}
	if got := contentText(t, out); got != in.Content {
		t.Errorf("Content = %q, want %q", got, in.Content)
	}
	if out.GetTokens() != int64(in.Tokens) {
		t.Errorf("Tokens = %d, want %d", out.GetTokens(), in.Tokens)
	}
	if out.GetStatus() != memoryv1.RecordStatus_RECORD_STATUS_CANONICAL {
		t.Errorf("Status = %v, want RECORD_STATUS_CANONICAL", out.GetStatus())
	}
	if len(out.GetLinks()) != 1 || out.GetLinks()[0] != "other-record" {
		t.Errorf("Links = %v, want [other-record]", out.GetLinks())
	}
	if !out.GetCreatedAt().AsTime().Equal(now) {
		t.Errorf("CreatedAt = %v, want %v", out.GetCreatedAt().AsTime(), now)
	}
	if out.GetProvenance().GetSourceSessionId() != "sess-1" {
		t.Errorf("Provenance.SourceSessionId = %q, want %q", out.GetProvenance().GetSourceSessionId(), "sess-1")
	}
	if out.GetProvenance().GetSourceTurnId() != "turn-1" {
		t.Errorf("Provenance.SourceTurnId = %q, want %q", out.GetProvenance().GetSourceTurnId(), "turn-1")
	}
	if out.GetRelevanceScore() != 0.75 {
		t.Errorf("RelevanceScore = %v, want 0.75", out.GetRelevanceScore())
	}
}

func TestConvert_RecordRoundTrip_NoRelevanceScoreOrTurnID(t *testing.T) {
	t.Parallel()

	in := memory.Record{ID: "r1", Type: memory.TypeProject, Scope: memory.ScopeProject, Content: "note"}
	out := exerciseRecordRoundTrip(t, in)

	if out.RelevanceScore != nil {
		t.Errorf("RelevanceScore = %v, want nil", out.GetRelevanceScore())
	}
	if out.GetProvenance().SourceTurnId != nil {
		t.Errorf("Provenance.SourceTurnId = %q, want unset", out.GetProvenance().GetSourceTurnId())
	}
}

func TestConvert_RelevanceScoreOutOfRangeRejected(t *testing.T) {
	t.Parallel()

	tooHigh := 1.5
	provider := &fakeProvider{recallFunc: func(context.Context, memory.RecallRequest) (memory.RecallResult, error) {
		return memory.RecallResult{Records: []memory.Record{{ID: "r1", RelevanceScore: &tooHigh}}}, nil
	}}
	client := newTestClient(t, memory.NewService(provider, testIdentity(), plugin.NewCallback()))
	_, err := client.Recall(t.Context(), &memoryv1.RecallRequest{})
	assertCode(t, err, codes.Internal)
}

func TestConvert_UpdateRecordRequest_TitleOptional(t *testing.T) {
	t.Parallel()

	t.Run("unset title leaves Title nil", func(t *testing.T) {
		t.Parallel()
		var gotTitle *string
		provider := &fakeProvider{updateRecordFunc: func(_ context.Context, req memory.UpdateRecordRequest) (memory.RecordResult, error) {
			gotTitle = req.Title
			return memory.RecordResult{ID: req.ID}, nil
		}}
		client := newTestClient(t, memory.NewService(provider, testIdentity(), plugin.NewCallback()))
		_, err := client.UpdateRecord(t.Context(), &memoryv1.UpdateRecordRequest{Id: "r1", Content: textBlocks("x")})
		if err != nil {
			t.Fatalf("UpdateRecord() error = %v, want nil", err)
		}
		if gotTitle != nil {
			t.Errorf("Title = %q, want nil", *gotTitle)
		}
	})

	t.Run("set title round-trips", func(t *testing.T) {
		t.Parallel()
		var gotTitle *string
		provider := &fakeProvider{updateRecordFunc: func(_ context.Context, req memory.UpdateRecordRequest) (memory.RecordResult, error) {
			gotTitle = req.Title
			return memory.RecordResult{ID: req.ID}, nil
		}}
		newTitle := "New Title"
		client := newTestClient(t, memory.NewService(provider, testIdentity(), plugin.NewCallback()))
		_, err := client.UpdateRecord(t.Context(), &memoryv1.UpdateRecordRequest{Id: "r1", Title: &newTitle, Content: textBlocks("x")})
		if err != nil {
			t.Fatalf("UpdateRecord() error = %v, want nil", err)
		}
		if gotTitle == nil || *gotTitle != newTitle {
			t.Errorf("Title = %v, want %q", gotTitle, newTitle)
		}
	})
}

func TestConvert_ListRecordsRequest_StatusFilter(t *testing.T) {
	t.Parallel()

	var gotFilter *memory.RecordStatus
	provider := &fakeProvider{listRecordsFunc: func(_ context.Context, req memory.ListRecordsRequest) (memory.ListRecordsResult, error) {
		gotFilter = req.StatusFilter
		return memory.ListRecordsResult{}, nil
	}}
	client := newTestClient(t, memory.NewService(provider, testIdentity(), plugin.NewCallback()))
	pending := memoryv1.RecordStatus_RECORD_STATUS_PENDING
	_, err := client.ListRecords(t.Context(), &memoryv1.ListRecordsRequest{StatusFilter: &pending})
	if err != nil {
		t.Fatalf("ListRecords() error = %v, want nil", err)
	}
	if gotFilter == nil || *gotFilter != memory.RecordStatusPending {
		t.Errorf("StatusFilter = %v, want RecordStatusPending", gotFilter)
	}
}

func TestConvert_RecallRequest_Filters(t *testing.T) {
	t.Parallel()

	var got memory.RecallRequest
	provider := &fakeProvider{recallFunc: func(_ context.Context, req memory.RecallRequest) (memory.RecallResult, error) {
		got = req
		return memory.RecallResult{}, nil
	}}
	client := newTestClient(t, memory.NewService(provider, testIdentity(), plugin.NewCallback()))
	_, err := client.Recall(t.Context(), &memoryv1.RecallRequest{
		SessionId:      "sess-1",
		TurnId:         "turn-1",
		TokenBudget:    500,
		TypeFilter:     []memoryv1.MemoryType{memoryv1.MemoryType_MEMORY_TYPE_PROJECT},
		ScopeFilter:    []memoryv1.MemoryScope{memoryv1.MemoryScope_MEMORY_SCOPE_PROJECT},
		IncludePending: true,
	})
	if err != nil {
		t.Fatalf("Recall() error = %v, want nil", err)
	}
	if got.SessionID != "sess-1" || got.TurnID != "turn-1" || got.TokenBudget != 500 {
		t.Errorf("RecallRequest = %+v, want session/turn/budget to round-trip", got)
	}
	if len(got.TypeFilter) != 1 || got.TypeFilter[0] != memory.TypeProject {
		t.Errorf("TypeFilter = %v, want [TypeProject]", got.TypeFilter)
	}
	if len(got.ScopeFilter) != 1 || got.ScopeFilter[0] != memory.ScopeProject {
		t.Errorf("ScopeFilter = %v, want [ScopeProject]", got.ScopeFilter)
	}
	if !got.IncludePending {
		t.Error("IncludePending = false, want true")
	}
}

// contentText extracts the plain text of r's content, failing t if it
// isn't exactly one text block.
func contentText(t *testing.T, r *memoryv1.MemoryRecord) string {
	t.Helper()
	blocks := r.GetContent()
	if len(blocks) != 1 || blocks[0].GetText() == nil {
		t.Fatalf("content = %v, want exactly one text block", blocks)
	}
	return blocks[0].GetText().GetText()
}
