package memory

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
	"github.com/pluggableharness/agent/pkg/content"
	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
	"github.com/pluggableharness/agent/pkg/plugin"
	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
)

// Type is this category's fixed record taxonomy
// (docs/specifications/memory/taxonomy.md) — user/feedback/project/
// reference, never provider-defined. A Record MUST declare exactly one
// Type, immutable after creation.
type Type int

// The fixed Type taxonomy. TypeUnspecified is never valid on a
// real record — its presence means a caller forgot to set the field, same
// as the generated MemoryType_MEMORY_TYPE_UNSPECIFIED zero value it mirrors.
const (
	TypeUnspecified Type = iota
	TypeUser
	TypeFeedback
	TypeProject
	TypeReference
)

// String returns t's taxonomy name, e.g. "user".
func (t Type) String() string {
	switch t {
	case TypeUser:
		return "user"
	case TypeFeedback:
		return "feedback"
	case TypeProject:
		return "project"
	case TypeReference:
		return "reference"
	default:
		return "unspecified"
	}
}

// Scope is this category's fixed visibility taxonomy
// (docs/specifications/memory/data-types.md#memoryscope) —
// session/project/global. MUST be set on every Record and is immutable
// after creation, the same as Type.
type Scope int

// The fixed Scope taxonomy.
const (
	ScopeUnspecified Scope = iota
	ScopeSession
	ScopeProject
	ScopeGlobal
)

// String returns s's taxonomy name, e.g. "project".
func (s Scope) String() string {
	switch s {
	case ScopeSession:
		return "session"
	case ScopeProject:
		return "project"
	case ScopeGlobal:
		return "global"
	default:
		return "unspecified"
	}
}

// RecordStatus distinguishes a fully-persisted record from one awaiting
// review under the optional ratification pattern
// (docs/specifications/memory/protocol.md#ratification-optional). A
// provider with Capabilities.RatificationSupported == false MUST NEVER
// return RecordStatusPending.
type RecordStatus int

// The fixed RecordStatus values.
const (
	RecordStatusUnspecified RecordStatus = iota
	RecordStatusCanonical
	RecordStatusPending
)

// String returns s's status name, e.g. "pending".
func (s RecordStatus) String() string {
	switch s {
	case RecordStatusCanonical:
		return "canonical"
	case RecordStatusPending:
		return "pending"
	default:
		return "unspecified"
	}
}

// Provenance records where a Record came from and who wrote it. It is
// kernel-populated at Record time and immutable thereafter — never
// provider-supplied, never mutated by UpdateRecord
// (docs/specifications/memory/data-types.md#provenance). A Provider
// returning a Record from Recall/ListRecords/GetRecord MAY leave this at
// its zero value; the kernel does not read a plugin-supplied value here as
// authoritative.
type Provenance struct {
	// SourceSessionID is the session that produced this record.
	SourceSessionID string
	// SourceTurnID is the turn (ULID) within SourceSessionID that produced
	// this record, when known. Empty when not known (e.g. a
	// backfill/import).
	SourceTurnID string
	// RecordedBy is the producing plugin's declared name, or the reference
	// tool path that wrote it (e.g. "memory.remember").
	RecordedBy string
}

// Record is one persisted unit of memory
// (docs/specifications/memory/data-types.md#recallrequest--memoryrecord).
// Content is text-only in v1, matching the context provider's
// ContextSection content constraint — convert.go collapses the wire
// []ContentBlock to this single string and back.
type Record struct {
	// ID is a slug, unique within this provider — kernel-enforced, not
	// provider-enforced (docs/specifications/memory/protocol.md#record-updaterecord-deleterecord-the-write-side).
	ID string
	// Type is this record's fixed taxonomy classification. Immutable after
	// creation.
	Type Type
	// Scope is this record's visibility scope. Immutable after creation,
	// like Type.
	Scope Scope
	// Title is a human-readable title.
	Title string
	// Content is the record's text content.
	Content string
	// Tokens is this record's size, computed via CountTokens — never a
	// provider-local heuristic.
	Tokens int32
	// Status reports whether this record is fully persisted or awaiting
	// ratification.
	Status RecordStatus
	// Links are record IDs this record references, kernel-parsed from
	// "[[name]]" syntax in Content at Record/UpdateRecord time — not
	// provider-populated.
	Links []string
	// CreatedAt is when this record was first created.
	CreatedAt time.Time
	// UpdatedAt is when this record was last modified.
	UpdatedAt time.Time
	// Provenance records where this record came from. See the Provenance
	// doc comment: kernel-populated, not meaningfully provider-supplied.
	Provenance Provenance
	// RelevanceScore is this record's recall-time relevance, in [0, 1].
	// Set ONLY on Recall/ListRecords results — nil on every other path,
	// and MUST NOT be persisted alongside the record itself. A Provider
	// that doesn't compute a meaningful relevance figure SHOULD leave this
	// nil rather than fabricating a value
	// (docs/specifications/memory/data-types.md#relevance_score).
	RelevanceScore *float64
}

// Capabilities is this provider's capability advertisement, returned by
// GetCapabilities (docs/specifications/memory/data-types.md#memorycapabilities).
// Build one with the helpers in capabilities.go.
type Capabilities struct {
	// DefaultTokenBudget is the token budget this provider requests for
	// its Recall contributions, absent any override. MUST be set.
	DefaultTokenBudget int64
	// SupportedTypes are the MemoryTypes this provider handles. MUST be
	// set; MAY be a subset of the full taxonomy.
	SupportedTypes []Type
	// SupportedScopes are the MemoryScopes this provider handles. MUST be
	// set; MAY be a subset (e.g. project-only).
	SupportedScopes []Scope
	// RatificationSupported reports whether this provider implements the
	// ApproveRecord/RejectRecord pattern. NewService overrides whatever
	// value a Provider.Capabilities implementation sets here with the
	// authoritative, structurally-derived answer — see server.go.
	RatificationSupported bool
	// SlashCommands are static template-expansion commands this provider
	// contributes. MAY be empty.
	SlashCommands []*commonv1.PromptExpansionSpec
	// ConfigSchema is this provider's agent.hcl config schema. MUST be
	// set — build it with pkg/config's Attribute/Schema helpers.
	ConfigSchema *configv1.ConfigSchema
	// SupportedHookPoints are the hook points this provider declares
	// HookSubscriberService.DispatchHook subscriptions for, beyond the
	// implicit post_model_response/session_end write triggers. MAY be
	// empty.
	SupportedHookPoints []commonv1.HookPoint
}

// RecallRequest is the read-side query, issued at context-assemble time
// (docs/specifications/memory/protocol.md#recall-the-read-side).
type RecallRequest struct {
	// SessionID is the requesting session's id.
	SessionID string
	// TurnID is the current turn within that session, a ULID.
	TurnID string
	// TokenBudget is the budget this Recall call MUST self-truncate its
	// returned records to.
	TokenBudget int64
	// ModelTarget is the model this recall is being assembled for. MUST be
	// set.
	ModelTarget *modelv1.ModelTarget
	// FilesTouched are paths touched so far this turn. MAY be empty.
	FilesTouched []string
	// WorkingDirectory is the session's current working directory.
	WorkingDirectory string
	// TypeFilter restricts results to these MemoryTypes. Empty means every
	// type this provider supports.
	TypeFilter []Type
	// ScopeFilter restricts results to these MemoryScopes. Empty means
	// every scope this provider supports.
	ScopeFilter []Scope
	// IncludePending reports whether PENDING-status records may be
	// included. Defaults to false at the wire boundary — a PENDING record
	// MUST NOT surface through ordinary recall unless this is true.
	IncludePending bool
}

// RecallResult carries the records a Recall call judged relevant.
type RecallResult struct {
	// Records are the recalled records, in this provider's own relevance
	// order.
	Records []Record
}

// RecordRequest creates a new record
// (docs/specifications/memory/protocol.md#record-updaterecord-deleterecord-the-write-side).
type RecordRequest struct {
	// Type is this record's fixed taxonomy classification. MUST be set.
	Type Type
	// Scope is this record's visibility scope. MUST be set.
	Scope Scope
	// ID is an author-suggested slug. Empty means the provider derives one
	// from content; the kernel disambiguates collisions with a numeric
	// suffix rather than overwriting or rejecting.
	ID string
	// Title is a human-readable title.
	Title string
	// Content is the record's text content.
	Content string
}

// RecordResult is the shared outcome shape for Record, UpdateRecord, and
// ApproveRecord.
type RecordResult struct {
	// ID is the final assigned slug. MUST be set.
	ID string
	// Status reports whether the write is fully persisted or awaiting
	// ratification.
	Status RecordStatus
}

// UpdateRecordRequest replaces an existing record's title/content
// wholesale (docs/specifications/memory/data-types.md#the-write-side).
// Deliberately carries no Type or Scope field: both are immutable after
// creation, so this type does not even offer a way to attempt changing
// them — recategorizing a record means DeleteRecord followed by a new
// Record call, never UpdateRecord.
type UpdateRecordRequest struct {
	// ID MUST match an existing record, or the call fails with a
	// structured *Error{Category: ErrorCategoryNotFound}.
	ID string
	// Title is the record's new title. Nil leaves the existing title
	// unchanged.
	Title *string
	// Content is the record's new content, replacing the existing content
	// wholesale — NOT a patch. A Provider implementation MUST NOT merge
	// this with the record's prior content.
	Content string
}

// DeleteResult is the shared outcome shape for DeleteRecord and
// RejectRecord.
type DeleteResult struct {
	// Deleted is true if a record was actually removed.
	Deleted bool
}

// ListRecordsRequest is the enumeration/audit query: paginated browsing of
// this provider's records, filterable by type/scope/status
// (docs/specifications/memory/protocol.md#listrecords--getrecord). Unlike
// RecallRequest, there is no IncludePending gate — PENDING records ARE
// listable here.
type ListRecordsRequest struct {
	// TypeFilter restricts results to these MemoryTypes. Empty means every
	// type this provider supports.
	TypeFilter []Type
	// ScopeFilter restricts results to these MemoryScopes. Empty means
	// every scope this provider supports.
	ScopeFilter []Scope
	// StatusFilter restricts results to this RecordStatus. Nil means both
	// CANONICAL and PENDING records are eligible.
	StatusFilter *RecordStatus
	// PageSize is the maximum number of records to return in one page.
	PageSize int32
	// PageToken is an opaque continuation token from a prior
	// ListRecordsResult.NextPageToken. Empty on the first page.
	PageToken string
}

// ListRecordsResult carries one page of matching records.
type ListRecordsResult struct {
	// Records are this page's records.
	Records []Record
	// NextPageToken is the opaque continuation token for the next page.
	// Empty when this is the last page.
	NextPageToken string
}

// Provider is the required RPC surface every memory provider MUST
// implement — GetCapabilities, Configure, Recall, Record, UpdateRecord,
// DeleteRecord, ListRecords, and GetRecord
// (docs/specifications/memory/conformance.md's summary matrix). Describe is
// handled by server.go directly from the Identity a plugin author supplies
// to NewService, so it is not part of this interface.
type Provider interface {
	// Capabilities reports what this provider supports. Called from the
	// GetCapabilities RPC handler.
	Capabilities(ctx context.Context) (Capabilities, error)
	// Configure decodes this provider's agent.hcl config block, already
	// decoded from HCL/cty per the schema this provider advertised in
	// Capabilities.ConfigSchema.
	Configure(ctx context.Context, cfg *structpb.Struct) error
	// Recall returns the records this provider judges relevant to req,
	// self-truncated to req.TokenBudget. A candidate set that still
	// exceeds TokenBudget after this provider's own truncation MUST fail
	// with a *Error{Category: ErrorCategoryBudgetExceeded} — see
	// errors.go's BudgetExceeded.
	Recall(ctx context.Context, req RecallRequest) (RecallResult, error)
	// Record creates a new record.
	Record(ctx context.Context, req RecordRequest) (RecordResult, error)
	// UpdateRecord replaces an existing record's title/content wholesale —
	// NOT a patch. MUST fail with a *Error{Category: ErrorCategoryNotFound}
	// if req.ID doesn't match an existing record, rather than silently
	// no-op'ing.
	UpdateRecord(ctx context.Context, req UpdateRecordRequest) (RecordResult, error)
	// DeleteRecord removes an existing record. MUST fail with a
	// *Error{Category: ErrorCategoryNotFound} if id doesn't match an
	// existing record, rather than silently no-op'ing.
	DeleteRecord(ctx context.Context, id string) (DeleteResult, error)
	// ListRecords is the enumeration/audit path: paginated browsing,
	// filterable by type/scope/status, with PENDING records listable
	// without any IncludePending-style gate.
	ListRecords(ctx context.Context, req ListRecordsRequest) (ListRecordsResult, error)
	// GetRecord fetches exactly one record by id. MUST fail with a
	// *Error{Category: ErrorCategoryNotFound} for an unknown id, rather
	// than returning an empty result.
	GetRecord(ctx context.Context, id string) (Record, error)
}

// RatificationProvider is the optional ratification pattern
// (docs/specifications/memory/protocol.md#ratification-optional): a
// Provider MAY additionally implement ApproveRecord (transitions PENDING →
// CANONICAL) and RejectRecord (discards a pending draft entirely, NOT a
// soft-delete). Both MUST be implemented together — see server.go's
// NewService doc comment for how this package enforces that structurally
// rather than at runtime.
type RatificationProvider interface {
	Provider

	// ApproveRecord transitions a PENDING record to CANONICAL. MUST fail
	// with a *Error{Category: ErrorCategoryNotFound} if id doesn't match an
	// existing record.
	ApproveRecord(ctx context.Context, id string) (RecordResult, error)
	// RejectRecord discards a pending draft entirely — NOT a soft-delete.
	// MUST fail with a *Error{Category: ErrorCategoryNotFound} if id
	// doesn't match an existing record.
	RejectRecord(ctx context.Context, id string) (DeleteResult, error)
}

// Renderer is the optional Render RPC
// (docs/specifications/memory/protocol.md#render): a Provider MAY
// additionally implement this to return its own RenderTree for a payload
// (e.g. a review-inbox view for a PENDING record) in place of the kernel's
// generic fallback.
type Renderer interface {
	Provider

	// Render returns a RenderTree for payload, emitted against
	// schemaVersion.
	Render(ctx context.Context, payload []byte, schemaVersion string) (*renderv1.RenderTree, error)
}

// CountTokens computes text's token count against modelTarget (a
// ModelTarget.Id, or empty to let the kernel fall back to its documented
// heuristic) via the kernel's CountTokens callback
// (docs/specifications/kernel-callbacks.md#counttokens). Memory providers
// MUST route Record.Tokens computation through this call rather than an
// arbitrary provider-local heuristic
// (docs/specifications/kernel-callbacks.md#why-a-kernel-primitive-not-a-provider-local-heuristic).
func CountTokens(ctx context.Context, cb *plugin.Callback, modelTarget, text string) (int32, error) {
	client, err := cb.Client(ctx)
	if err != nil {
		return 0, fmt.Errorf("memory: count tokens: %w", err)
	}

	req := &kernelv1.CountTokensRequest{
		Content: []*contentv1.ContentBlock{content.Text(text)},
	}
	if modelTarget != "" {
		req.ModelRef = &modelv1.ModelRef{Id: modelTarget}
	}

	result, err := client.CountTokens(ctx, req)
	if err != nil {
		return 0, fmt.Errorf("memory: count tokens: %w", err)
	}
	return clampInt32(result.GetCount()), nil
}
