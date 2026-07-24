package memory

import (
	"testing"
	"time"

	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	memoryv1 "github.com/pluggableharness/agent/pkg/memory/proto/v1"
)

// This file white-box tests convert.go's unexported translation functions
// directly, rather than only through the RPC boundary server_test.go
// exercises — both give confidence, but a direct table test is the more
// natural way to hit every enum branch (including the UNSPECIFIED/default
// zero-value case a real request would never send).

func TestMemoryTypeConversion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		domain Type
		wire   memoryv1.MemoryType
	}{
		{TypeUnspecified, memoryv1.MemoryType_MEMORY_TYPE_UNSPECIFIED},
		{TypeUser, memoryv1.MemoryType_MEMORY_TYPE_USER},
		{TypeFeedback, memoryv1.MemoryType_MEMORY_TYPE_FEEDBACK},
		{TypeProject, memoryv1.MemoryType_MEMORY_TYPE_PROJECT},
		{TypeReference, memoryv1.MemoryType_MEMORY_TYPE_REFERENCE},
	}

	for _, tt := range tests {
		t.Run(tt.domain.String(), func(t *testing.T) {
			t.Parallel()
			if got := toProtoMemoryType(tt.domain); got != tt.wire {
				t.Errorf("toProtoMemoryType(%v) = %v, want %v", tt.domain, got, tt.wire)
			}
			if got := fromProtoMemoryType(tt.wire); got != tt.domain {
				t.Errorf("fromProtoMemoryType(%v) = %v, want %v", tt.wire, got, tt.domain)
			}
		})
	}

	if got := fromProtoMemoryType(memoryv1.MemoryType(99)); got != TypeUnspecified {
		t.Errorf("fromProtoMemoryType(99) = %v, want TypeUnspecified", got)
	}
	if got := toProtoMemoryType(Type(99)); got != memoryv1.MemoryType_MEMORY_TYPE_UNSPECIFIED {
		t.Errorf("toProtoMemoryType(99) = %v, want MEMORY_TYPE_UNSPECIFIED", got)
	}
}

func TestMemoryScopeConversion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		domain Scope
		wire   memoryv1.MemoryScope
	}{
		{ScopeUnspecified, memoryv1.MemoryScope_MEMORY_SCOPE_UNSPECIFIED},
		{ScopeSession, memoryv1.MemoryScope_MEMORY_SCOPE_SESSION},
		{ScopeProject, memoryv1.MemoryScope_MEMORY_SCOPE_PROJECT},
		{ScopeGlobal, memoryv1.MemoryScope_MEMORY_SCOPE_GLOBAL},
	}

	for _, tt := range tests {
		t.Run(tt.domain.String(), func(t *testing.T) {
			t.Parallel()
			if got := toProtoMemoryScope(tt.domain); got != tt.wire {
				t.Errorf("toProtoMemoryScope(%v) = %v, want %v", tt.domain, got, tt.wire)
			}
			if got := fromProtoMemoryScope(tt.wire); got != tt.domain {
				t.Errorf("fromProtoMemoryScope(%v) = %v, want %v", tt.wire, got, tt.domain)
			}
		})
	}

	if got := fromProtoMemoryScope(memoryv1.MemoryScope(99)); got != ScopeUnspecified {
		t.Errorf("fromProtoMemoryScope(99) = %v, want ScopeUnspecified", got)
	}
	if got := toProtoMemoryScope(Scope(99)); got != memoryv1.MemoryScope_MEMORY_SCOPE_UNSPECIFIED {
		t.Errorf("toProtoMemoryScope(99) = %v, want MEMORY_SCOPE_UNSPECIFIED", got)
	}
}

func TestRecordStatusConversion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		domain RecordStatus
		wire   memoryv1.RecordStatus
	}{
		{RecordStatusUnspecified, memoryv1.RecordStatus_RECORD_STATUS_UNSPECIFIED},
		{RecordStatusCanonical, memoryv1.RecordStatus_RECORD_STATUS_CANONICAL},
		{RecordStatusPending, memoryv1.RecordStatus_RECORD_STATUS_PENDING},
	}

	for _, tt := range tests {
		t.Run(tt.domain.String(), func(t *testing.T) {
			t.Parallel()
			if got := toProtoRecordStatus(tt.domain); got != tt.wire {
				t.Errorf("toProtoRecordStatus(%v) = %v, want %v", tt.domain, got, tt.wire)
			}
			if got := fromProtoRecordStatus(tt.wire); got != tt.domain {
				t.Errorf("fromProtoRecordStatus(%v) = %v, want %v", tt.wire, got, tt.domain)
			}
		})
	}

	if got := fromProtoRecordStatus(memoryv1.RecordStatus(99)); got != RecordStatusUnspecified {
		t.Errorf("fromProtoRecordStatus(99) = %v, want RecordStatusUnspecified", got)
	}
}

func TestContentConversion(t *testing.T) {
	t.Parallel()

	t.Run("empty text yields nil blocks", func(t *testing.T) {
		t.Parallel()
		if got := contentToProto(""); got != nil {
			t.Errorf("contentToProto(\"\") = %v, want nil", got)
		}
	})

	t.Run("round trip", func(t *testing.T) {
		t.Parallel()
		blocks := contentToProto("hello world")
		text, err := contentFromProto(blocks)
		if err != nil {
			t.Fatalf("contentFromProto() error = %v, want nil", err)
		}
		if text != "hello world" {
			t.Errorf("contentFromProto() = %q, want %q", text, "hello world")
		}
	})

	t.Run("empty blocks yield empty text", func(t *testing.T) {
		t.Parallel()
		text, err := contentFromProto(nil)
		if err != nil {
			t.Fatalf("contentFromProto(nil) error = %v, want nil", err)
		}
		if text != "" {
			t.Errorf("contentFromProto(nil) = %q, want \"\"", text)
		}
	})

	t.Run("non-text block is rejected", func(t *testing.T) {
		t.Parallel()
		blocks := []*contentv1.ContentBlock{{
			Block: &contentv1.ContentBlock_Image{Image: &contentv1.ImageBlock{Data: []byte("x"), MediaType: "image/png"}},
		}}
		if _, err := contentFromProto(blocks); err == nil {
			t.Error("contentFromProto() error = nil, want a text-only-in-v1 rejection")
		}
	})
}

func TestProvenanceConversion(t *testing.T) {
	t.Parallel()

	t.Run("with turn id", func(t *testing.T) {
		t.Parallel()
		in := Provenance{SourceSessionID: "s1", SourceTurnID: "t1", RecordedBy: "memory.remember"}
		out := provenanceFromProto(provenanceToProto(in))
		if out != in {
			t.Errorf("round trip = %+v, want %+v", out, in)
		}
	})

	t.Run("without turn id", func(t *testing.T) {
		t.Parallel()
		in := Provenance{SourceSessionID: "s1", RecordedBy: "memory.remember"}
		pb := provenanceToProto(in)
		if pb.SourceTurnId != nil {
			t.Errorf("SourceTurnId = %q, want unset", pb.GetSourceTurnId())
		}
		out := provenanceFromProto(pb)
		if out != in {
			t.Errorf("round trip = %+v, want %+v", out, in)
		}
	})

	t.Run("nil proto", func(t *testing.T) {
		t.Parallel()
		out := provenanceFromProto(nil)
		if out != (Provenance{}) {
			t.Errorf("provenanceFromProto(nil) = %+v, want zero value", out)
		}
	})
}

func TestRecordConversion(t *testing.T) {
	t.Parallel()

	t.Run("round trip", func(t *testing.T) {
		t.Parallel()
		now := time.Now().UTC().Truncate(time.Second)
		score := 0.5
		in := Record{
			ID:             "r1",
			Type:           TypeFeedback,
			Scope:          ScopeSession,
			Title:          "t",
			Content:        "c",
			Tokens:         3,
			Status:         RecordStatusCanonical,
			Links:          []string{"a", "b"},
			CreatedAt:      now,
			UpdatedAt:      now,
			Provenance:     Provenance{SourceSessionID: "s1", RecordedBy: "x"},
			RelevanceScore: &score,
		}
		pb, err := recordToProto(in)
		if err != nil {
			t.Fatalf("recordToProto() error = %v, want nil", err)
		}
		out, err := recordFromProto(pb)
		if err != nil {
			t.Fatalf("recordFromProto() error = %v, want nil", err)
		}
		if out.ID != in.ID || out.Type != in.Type || out.Scope != in.Scope || out.Content != in.Content {
			t.Errorf("round trip = %+v, want %+v", out, in)
		}
		if out.RelevanceScore == nil || *out.RelevanceScore != score {
			t.Errorf("RelevanceScore = %v, want %v", out.RelevanceScore, score)
		}
		if !out.CreatedAt.Equal(now) {
			t.Errorf("CreatedAt = %v, want %v", out.CreatedAt, now)
		}
	})

	t.Run("relevance score out of range is rejected", func(t *testing.T) {
		t.Parallel()
		tooLow := -0.1
		_, err := recordToProto(Record{RelevanceScore: &tooLow})
		if err == nil {
			t.Error("recordToProto() error = nil, want an out-of-range rejection")
		}
	})

	t.Run("non-text content is rejected on the way back", func(t *testing.T) {
		t.Parallel()
		pb := &memoryv1.MemoryRecord{Content: []*contentv1.ContentBlock{{
			Block: &contentv1.ContentBlock_Image{Image: &contentv1.ImageBlock{}},
		}}}
		if _, err := recordFromProto(pb); err == nil {
			t.Error("recordFromProto() error = nil, want a text-only-in-v1 rejection")
		}
	})
}

func TestRecordResultAndDeleteResultConversion(t *testing.T) {
	t.Parallel()

	rr := recordResultToProto(RecordResult{ID: "r1", Status: RecordStatusPending})
	if rr.GetId() != "r1" || rr.GetStatus() != memoryv1.RecordStatus_RECORD_STATUS_PENDING {
		t.Errorf("recordResultToProto() = %+v, want id r1 / PENDING", rr)
	}

	dr := deleteResultToProto(DeleteResult{Deleted: true})
	if !dr.GetDeleted() {
		t.Error("deleteResultToProto().Deleted = false, want true")
	}
}

func TestRecordRequestFromProto(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		id := "r1"
		pb := &memoryv1.RecordRequest{
			Type:    memoryv1.MemoryType_MEMORY_TYPE_USER,
			Scope:   memoryv1.MemoryScope_MEMORY_SCOPE_GLOBAL,
			Id:      &id,
			Title:   "t",
			Content: contentToProto("c"),
		}
		out, err := recordRequestFromProto(pb)
		if err != nil {
			t.Fatalf("recordRequestFromProto() error = %v, want nil", err)
		}
		if out.ID != "r1" || out.Type != TypeUser || out.Scope != ScopeGlobal || out.Content != "c" {
			t.Errorf("recordRequestFromProto() = %+v", out)
		}
	})

	t.Run("non-text content is rejected", func(t *testing.T) {
		t.Parallel()
		pb := &memoryv1.RecordRequest{Content: []*contentv1.ContentBlock{{
			Block: &contentv1.ContentBlock_Image{Image: &contentv1.ImageBlock{}},
		}}}
		if _, err := recordRequestFromProto(pb); err == nil {
			t.Error("recordRequestFromProto() error = nil, want a rejection")
		}
	})
}

func TestUpdateRecordRequestFromProto_NonTextRejected(t *testing.T) {
	t.Parallel()

	pb := &memoryv1.UpdateRecordRequest{Content: []*contentv1.ContentBlock{{
		Block: &contentv1.ContentBlock_Image{Image: &contentv1.ImageBlock{}},
	}}}
	if _, err := updateRecordRequestFromProto(pb); err == nil {
		t.Error("updateRecordRequestFromProto() error = nil, want a rejection")
	}
}

func TestListRecordsRequestFromProto_NoStatusFilter(t *testing.T) {
	t.Parallel()

	out := listRecordsRequestFromProto(&memoryv1.ListRecordsRequest{PageSize: 10, PageToken: "tok"})
	if out.StatusFilter != nil {
		t.Errorf("StatusFilter = %v, want nil", out.StatusFilter)
	}
	if out.PageSize != 10 || out.PageToken != "tok" {
		t.Errorf("ListRecordsRequest = %+v", out)
	}
}

func TestListRecordsResultToProto_PropagatesConversionError(t *testing.T) {
	t.Parallel()

	tooHigh := 2.0
	_, err := listRecordsResultToProto(ListRecordsResult{Records: []Record{{RelevanceScore: &tooHigh}}})
	if err == nil {
		t.Error("listRecordsResultToProto() error = nil, want the out-of-range rejection to propagate")
	}
}

func TestCapabilitiesToProto(t *testing.T) {
	t.Parallel()

	caps := Capabilities{
		DefaultTokenBudget:    1000,
		SupportedTypes:        []Type{TypeUser, TypeProject},
		SupportedScopes:       []Scope{ScopeGlobal},
		RatificationSupported: true,
		SupportedHookPoints:   nil,
	}
	pb := capabilitiesToProto(caps)
	if pb.GetDefaultTokenBudget() != 1000 {
		t.Errorf("DefaultTokenBudget = %d, want 1000", pb.GetDefaultTokenBudget())
	}
	if len(pb.GetSupportedTypes()) != 2 {
		t.Errorf("SupportedTypes = %v, want 2 entries", pb.GetSupportedTypes())
	}
	if len(pb.GetSupportedScopes()) != 1 {
		t.Errorf("SupportedScopes = %v, want 1 entry", pb.GetSupportedScopes())
	}
	if !pb.GetRatificationSupported() {
		t.Error("RatificationSupported = false, want true")
	}
}
