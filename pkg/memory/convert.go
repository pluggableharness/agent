package memory

import (
	"fmt"
	"math"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/pluggableharness/agent/pkg/content"
	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	memoryv1 "github.com/pluggableharness/agent/pkg/memory/proto/v1"
)

// clampInt32 converts v to int32, clamping to [math.MinInt32, math.MaxInt32]
// rather than an unchecked narrowing conversion (gosec G115). A wire int64
// token count is never expected to exceed that range in practice, but a
// clamp is cheap insurance against silently wrapping into a negative or
// nonsensical value if it ever did.
func clampInt32(v int64) int32 {
	switch {
	case v > math.MaxInt32:
		return math.MaxInt32
	case v < math.MinInt32:
		return math.MinInt32
	default:
		return int32(v)
	}
}

// toProtoMemoryType converts a domain Type to its wire enum value.
func toProtoMemoryType(t Type) memoryv1.MemoryType {
	switch t {
	case TypeUser:
		return memoryv1.MemoryType_MEMORY_TYPE_USER
	case TypeFeedback:
		return memoryv1.MemoryType_MEMORY_TYPE_FEEDBACK
	case TypeProject:
		return memoryv1.MemoryType_MEMORY_TYPE_PROJECT
	case TypeReference:
		return memoryv1.MemoryType_MEMORY_TYPE_REFERENCE
	default:
		return memoryv1.MemoryType_MEMORY_TYPE_UNSPECIFIED
	}
}

// fromProtoMemoryType converts a wire Type enum value to its domain
// equivalent.
func fromProtoMemoryType(t memoryv1.MemoryType) Type {
	switch t {
	case memoryv1.MemoryType_MEMORY_TYPE_USER:
		return TypeUser
	case memoryv1.MemoryType_MEMORY_TYPE_FEEDBACK:
		return TypeFeedback
	case memoryv1.MemoryType_MEMORY_TYPE_PROJECT:
		return TypeProject
	case memoryv1.MemoryType_MEMORY_TYPE_REFERENCE:
		return TypeReference
	default:
		return TypeUnspecified
	}
}

// toProtoMemoryScope converts a domain Scope to its wire enum value.
func toProtoMemoryScope(s Scope) memoryv1.MemoryScope {
	switch s {
	case ScopeSession:
		return memoryv1.MemoryScope_MEMORY_SCOPE_SESSION
	case ScopeProject:
		return memoryv1.MemoryScope_MEMORY_SCOPE_PROJECT
	case ScopeGlobal:
		return memoryv1.MemoryScope_MEMORY_SCOPE_GLOBAL
	default:
		return memoryv1.MemoryScope_MEMORY_SCOPE_UNSPECIFIED
	}
}

// fromProtoMemoryScope converts a wire Scope enum value to its domain
// equivalent.
func fromProtoMemoryScope(s memoryv1.MemoryScope) Scope {
	switch s {
	case memoryv1.MemoryScope_MEMORY_SCOPE_SESSION:
		return ScopeSession
	case memoryv1.MemoryScope_MEMORY_SCOPE_PROJECT:
		return ScopeProject
	case memoryv1.MemoryScope_MEMORY_SCOPE_GLOBAL:
		return ScopeGlobal
	default:
		return ScopeUnspecified
	}
}

// toProtoRecordStatus converts a domain RecordStatus to its wire enum
// value.
func toProtoRecordStatus(s RecordStatus) memoryv1.RecordStatus {
	switch s {
	case RecordStatusCanonical:
		return memoryv1.RecordStatus_RECORD_STATUS_CANONICAL
	case RecordStatusPending:
		return memoryv1.RecordStatus_RECORD_STATUS_PENDING
	default:
		return memoryv1.RecordStatus_RECORD_STATUS_UNSPECIFIED
	}
}

// fromProtoRecordStatus converts a wire RecordStatus enum value to its
// domain equivalent.
func fromProtoRecordStatus(s memoryv1.RecordStatus) RecordStatus {
	switch s {
	case memoryv1.RecordStatus_RECORD_STATUS_CANONICAL:
		return RecordStatusCanonical
	case memoryv1.RecordStatus_RECORD_STATUS_PENDING:
		return RecordStatusPending
	default:
		return RecordStatusUnspecified
	}
}

// contentToProto wraps text into the single-element []*ContentBlock the
// wire types carry, per this category's text-only-in-v1 constraint
// (docs/specifications/memory/data-types.md#recallrequest--memoryrecord).
func contentToProto(text string) []*contentv1.ContentBlock {
	if text == "" {
		return nil
	}
	return []*contentv1.ContentBlock{content.Text(text)}
}

// contentFromProto collapses blocks back to a single string, enforcing the
// text-only-in-v1 constraint: any non-text block is a protocol violation
// this SDK rejects rather than silently drops.
func contentFromProto(blocks []*contentv1.ContentBlock) (string, error) {
	var text string
	for _, b := range blocks {
		tb := b.GetText()
		if tb == nil {
			return "", fmt.Errorf("memory: convert: content block is not text-only, which this category requires in v1")
		}
		text += tb.GetText()
	}
	return text, nil
}

// provenanceToProto converts a domain Provenance to its wire equivalent.
func provenanceToProto(p Provenance) *memoryv1.Provenance {
	pb := &memoryv1.Provenance{
		SourceSessionId: p.SourceSessionID,
		RecordedBy:      p.RecordedBy,
	}
	if p.SourceTurnID != "" {
		turnID := p.SourceTurnID
		pb.SourceTurnId = &turnID
	}
	return pb
}

// provenanceFromProto converts a wire Provenance to its domain equivalent.
func provenanceFromProto(p *memoryv1.Provenance) Provenance {
	return Provenance{
		SourceSessionID: p.GetSourceSessionId(),
		SourceTurnID:    p.GetSourceTurnId(),
		RecordedBy:      p.GetRecordedBy(),
	}
}

// recordToProto converts a domain Record to the wire MemoryRecord Recall,
// ListRecords, and GetRecord return.
func recordToProto(r Record) (*memoryv1.MemoryRecord, error) {
	pb := &memoryv1.MemoryRecord{
		Id:         r.ID,
		Type:       toProtoMemoryType(r.Type),
		Scope:      toProtoMemoryScope(r.Scope),
		Title:      r.Title,
		Content:    contentToProto(r.Content),
		Tokens:     int64(r.Tokens),
		Status:     toProtoRecordStatus(r.Status),
		Links:      r.Links,
		CreatedAt:  timestamppb.New(r.CreatedAt),
		UpdatedAt:  timestamppb.New(r.UpdatedAt),
		Provenance: provenanceToProto(r.Provenance),
	}
	if r.RelevanceScore != nil {
		score := *r.RelevanceScore
		if score < 0 || score > 1 {
			return nil, fmt.Errorf("memory: convert: relevance_score %v is outside the required [0, 1] range", score)
		}
		pb.RelevanceScore = &score
	}
	return pb, nil
}

// recordFromProto converts a wire MemoryRecord to its domain equivalent.
func recordFromProto(pb *memoryv1.MemoryRecord) (Record, error) {
	text, err := contentFromProto(pb.GetContent())
	if err != nil {
		return Record{}, err
	}

	r := Record{
		ID:         pb.GetId(),
		Type:       fromProtoMemoryType(pb.GetType()),
		Scope:      fromProtoMemoryScope(pb.GetScope()),
		Title:      pb.GetTitle(),
		Content:    text,
		Tokens:     clampInt32(pb.GetTokens()),
		Status:     fromProtoRecordStatus(pb.GetStatus()),
		Links:      pb.GetLinks(),
		CreatedAt:  pb.GetCreatedAt().AsTime(),
		UpdatedAt:  pb.GetUpdatedAt().AsTime(),
		Provenance: provenanceFromProto(pb.GetProvenance()),
	}
	if pb.RelevanceScore != nil {
		score := pb.GetRelevanceScore()
		r.RelevanceScore = &score
	}
	return r, nil
}

// recordResultToProto converts a domain RecordResult to its wire
// equivalent.
func recordResultToProto(r RecordResult) *memoryv1.RecordResult {
	return &memoryv1.RecordResult{
		Id:     r.ID,
		Status: toProtoRecordStatus(r.Status),
	}
}

// deleteResultToProto converts a domain DeleteResult to its wire
// equivalent.
func deleteResultToProto(r DeleteResult) *memoryv1.DeleteResult {
	return &memoryv1.DeleteResult{Deleted: r.Deleted}
}

// recallRequestFromProto converts a wire RecallRequest to its domain
// equivalent.
func recallRequestFromProto(req *memoryv1.RecallRequest) RecallRequest {
	typeFilter := make([]Type, 0, len(req.GetTypeFilter()))
	for _, t := range req.GetTypeFilter() {
		typeFilter = append(typeFilter, fromProtoMemoryType(t))
	}
	scopeFilter := make([]Scope, 0, len(req.GetScopeFilter()))
	for _, s := range req.GetScopeFilter() {
		scopeFilter = append(scopeFilter, fromProtoMemoryScope(s))
	}

	return RecallRequest{
		SessionID:        req.GetSessionId(),
		TurnID:           req.GetTurnId(),
		TokenBudget:      req.GetTokenBudget(),
		ModelTarget:      req.GetModelTarget(),
		FilesTouched:     req.GetFilesTouched(),
		WorkingDirectory: req.GetWorkingDirectory(),
		TypeFilter:       typeFilter,
		ScopeFilter:      scopeFilter,
		IncludePending:   req.GetIncludePending(),
	}
}

// recordRequestFromProto converts a wire RecordRequest to its domain
// equivalent.
func recordRequestFromProto(req *memoryv1.RecordRequest) (RecordRequest, error) {
	text, err := contentFromProto(req.GetContent())
	if err != nil {
		return RecordRequest{}, err
	}
	return RecordRequest{
		Type:    fromProtoMemoryType(req.GetType()),
		Scope:   fromProtoMemoryScope(req.GetScope()),
		ID:      req.GetId(),
		Title:   req.GetTitle(),
		Content: text,
	}, nil
}

// updateRecordRequestFromProto converts a wire UpdateRecordRequest to its
// domain equivalent. The wire message itself carries no type/scope field
// (memory.go's UpdateRecordRequest doc comment explains why), so there is
// nothing to strip here — the immutability guarantee is structural, not
// something this conversion enforces.
func updateRecordRequestFromProto(req *memoryv1.UpdateRecordRequest) (UpdateRecordRequest, error) {
	text, err := contentFromProto(req.GetContent())
	if err != nil {
		return UpdateRecordRequest{}, err
	}

	out := UpdateRecordRequest{
		ID:      req.GetId(),
		Content: text,
	}
	if req.Title != nil {
		title := req.GetTitle()
		out.Title = &title
	}
	return out, nil
}

// listRecordsRequestFromProto converts a wire ListRecordsRequest to its
// domain equivalent.
func listRecordsRequestFromProto(req *memoryv1.ListRecordsRequest) ListRecordsRequest {
	typeFilter := make([]Type, 0, len(req.GetTypeFilter()))
	for _, t := range req.GetTypeFilter() {
		typeFilter = append(typeFilter, fromProtoMemoryType(t))
	}
	scopeFilter := make([]Scope, 0, len(req.GetScopeFilter()))
	for _, s := range req.GetScopeFilter() {
		scopeFilter = append(scopeFilter, fromProtoMemoryScope(s))
	}

	out := ListRecordsRequest{
		TypeFilter:  typeFilter,
		ScopeFilter: scopeFilter,
		PageSize:    req.GetPageSize(),
		PageToken:   req.GetPageToken(),
	}
	if req.StatusFilter != nil {
		status := fromProtoRecordStatus(req.GetStatusFilter())
		out.StatusFilter = &status
	}
	return out
}

// listRecordsResultToProto converts a domain ListRecordsResult to its wire
// equivalent.
func listRecordsResultToProto(r ListRecordsResult) (*memoryv1.ListRecordsResponse, error) {
	records := make([]*memoryv1.MemoryRecord, 0, len(r.Records))
	for _, rec := range r.Records {
		pb, err := recordToProto(rec)
		if err != nil {
			return nil, err
		}
		records = append(records, pb)
	}
	return &memoryv1.ListRecordsResponse{
		Records:       records,
		NextPageToken: r.NextPageToken,
	}, nil
}
