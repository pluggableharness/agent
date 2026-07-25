package statebackend

import (
	"fmt"
	"time"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	planv1 "github.com/pluggableharness/agent/pkg/plan/proto/v1"
)

// Event mirrors the events table's columns
// (docs/specifications/state-backend.md#events) — the kernel's event
// envelope, verbatim, as an append-only row. Sequence is assigned by
// AppendEvent/AppendMessage/AppendPlan (session.go) and is meaningless on
// an Event passed into one of those calls; it's populated on an Event read
// back from storage (Stage 3's query.go).
type Event struct {
	// Sequence is the row's assigned INTEGER PRIMARY KEY AUTOINCREMENT
	// value. Ignored on append; only meaningful on a read-back Event.
	Sequence int64
	// ID is the stable event identifier, independent of storage — UNIQUE
	// within a session's file; a repeat on append returns
	// ErrDuplicateEventID.
	ID string
	// Timestamp is wall-clock, display-only, never ordering-authoritative
	// (docs/specifications/state-backend.md#ordering--concurrency).
	Timestamp time.Time
	// Kind identifies the event envelope's payload shape
	// (docs/specifications/state-backend.md#the-kind-enum).
	// EVENT_KIND_UNSPECIFIED is rejected on append with ErrInvalidKind.
	Kind kernelv1.EventKind
	// Producer identifies which plugin produced this event. Required
	// (producer_category/name/version are all NOT NULL columns).
	Producer *commonv1.ProducerRef
	// SchemaVersion is the producer's payload schema version.
	SchemaVersion string
	// Payload is the opaque event body — the kernel never inspects this
	// (docs/specifications/state-backend.md#events).
	Payload []byte
}

// CostEntry mirrors the cost_ledger table's columns
// (docs/specifications/state-backend.md#cost_ledger) — structured spend,
// appended once per completed model turn alongside its message event
// (AppendMessage, session.go). CostUSD and the token counters are stored
// exactly as the caller computed them; this package never recomputes a
// cost/token figure itself (determinism.md).
type CostEntry struct {
	ProviderName     string
	ModelID          string
	InputTokens      int64
	OutputTokens     int64
	CacheWriteTokens int64
	CacheReadTokens  int64
	CostUSD          float64
}

// PlanItem mirrors the plan_items table's columns
// (docs/specifications/state-backend.md#plan_items) — one row per plan
// item, appended alongside its plan event (AppendPlan, session.go).
type PlanItem struct {
	TurnID       string
	ToolCallID   string
	ProviderName string
	ToolName     string
	// Decision is one of PLAN_DECISION_ALLOW/ASK/DENY.
	// PLAN_DECISION_UNSPECIFIED and PLAN_DECISION_PENDING are rejected on
	// append with ErrInvalidDecision — the spec's decision column only
	// ever holds a made decision ("allow | ask | deny"), never a pending
	// one (docs/specifications/state-backend.md#plan_items).
	Decision  planv1.PlanDecision
	DecidedBy string
}

// eventKindText maps EventKind to the exact lowercase snake_case text
// docs/specifications/state-backend.md#the-kind-enum documents — the wire
// enum's own SCREAMING_SNAKE_CASE String() is not what gets stored.
// EVENT_KIND_UNSPECIFIED is deliberately absent: like SessionStatus's zero
// value (statebackend.go), it MUST NOT ever be persisted.
var eventKindText = map[kernelv1.EventKind]string{
	kernelv1.EventKind_EVENT_KIND_MESSAGE:              "message",
	kernelv1.EventKind_EVENT_KIND_TOOL_CALL:            "tool_call",
	kernelv1.EventKind_EVENT_KIND_TOOL_RESULT:          "tool_result",
	kernelv1.EventKind_EVENT_KIND_PLAN:                 "plan",
	kernelv1.EventKind_EVENT_KIND_APPLY:                "apply",
	kernelv1.EventKind_EVENT_KIND_CONTEXT_CONTRIBUTION: "context_contribution",
	kernelv1.EventKind_EVENT_KIND_MEMORY_WRITE:         "memory_write",
	kernelv1.EventKind_EVENT_KIND_MEMORY_UPDATE:        "memory_update",
	kernelv1.EventKind_EVENT_KIND_MEMORY_DELETE:        "memory_delete",
	kernelv1.EventKind_EVENT_KIND_HOOK_ERROR:           "hook_error",
}

// eventTextKind is eventKindText inverted, built once from eventKindText
// itself so the two can never drift.
var eventTextKind = func() map[string]kernelv1.EventKind {
	m := make(map[string]kernelv1.EventKind, len(eventKindText))
	for kind, text := range eventKindText {
		m[text] = kind
	}
	return m
}()

// eventPayloadType maps EventKind to the fully-qualified
// pluggableharness.event.v1 message name that kind's payload is marshaled
// as, transcribed from docs/specifications/state-backend.md#the-kind-enum's
// kind -> event.v1 message table. The three memory kinds deliberately share
// one message (MemoryMutationEvent) — the mutating verb is the kind itself,
// not a payload field. EVENT_KIND_UNSPECIFIED is absent for the same reason
// it is absent from eventKindText: it is never valid on the wire.
var eventPayloadType = map[kernelv1.EventKind]string{
	kernelv1.EventKind_EVENT_KIND_MESSAGE:              "pluggableharness.event.v1.MessageEvent",
	kernelv1.EventKind_EVENT_KIND_TOOL_CALL:            "pluggableharness.event.v1.ToolCallEvent",
	kernelv1.EventKind_EVENT_KIND_TOOL_RESULT:          "pluggableharness.event.v1.ToolResultEvent",
	kernelv1.EventKind_EVENT_KIND_PLAN:                 "pluggableharness.event.v1.PlanEvent",
	kernelv1.EventKind_EVENT_KIND_APPLY:                "pluggableharness.event.v1.ApplyEvent",
	kernelv1.EventKind_EVENT_KIND_CONTEXT_CONTRIBUTION: "pluggableharness.event.v1.ContextContributionEvent",
	kernelv1.EventKind_EVENT_KIND_MEMORY_WRITE:         "pluggableharness.event.v1.MemoryMutationEvent",
	kernelv1.EventKind_EVENT_KIND_MEMORY_UPDATE:        "pluggableharness.event.v1.MemoryMutationEvent",
	kernelv1.EventKind_EVENT_KIND_MEMORY_DELETE:        "pluggableharness.event.v1.MemoryMutationEvent",
	kernelv1.EventKind_EVENT_KIND_HOOK_ERROR:           "pluggableharness.event.v1.HookErrorEvent",
}

// EventKindText returns kind's stable lowercase TEXT encoding — the exact
// value this package stores in events.kind, and the same vocabulary
// kernel-callbacks.md#emit's reserved bus topic `kernel.event.{kind}` is
// built from. EVENT_KIND_UNSPECIFIED and any unrecognized value return
// ErrInvalidKind. This is a thin exported wrapper over the package-internal
// encodeEventKind rather than a second switch: the stored vocabulary has
// exactly one definition (eventKindText), and callers outside this package
// get it from here.
func EventKindText(kind kernelv1.EventKind) (string, error) {
	return encodeEventKind(kind)
}

// EventPayloadType returns the fully-qualified pluggableharness.event.v1
// message name that kind's payload MUST be marshaled as, per
// docs/specifications/state-backend.md#the-kind-enum — the value a
// kernel.v1.BusEvent.payload_type field carries when the kernel republishes
// a persisted event onto the bus. EVENT_KIND_UNSPECIFIED and any
// unrecognized value return ErrInvalidKind, matching EventKindText.
//
// This is a name, not a decode: this package never unmarshals a payload
// (events.payload is opaque to the kernel, per the spec's events table).
func EventPayloadType(kind kernelv1.EventKind) (string, error) {
	name, ok := eventPayloadType[kind]
	if !ok {
		return "", fmt.Errorf("statebackend: %w: %v", ErrInvalidKind, kind)
	}
	return name, nil
}

// encodeEventKind renders kind as its stored TEXT representation.
// EVENT_KIND_UNSPECIFIED and any unrecognized value return ErrInvalidKind.
func encodeEventKind(kind kernelv1.EventKind) (string, error) {
	text, ok := eventKindText[kind]
	if !ok {
		return "", fmt.Errorf("statebackend: %w: %v", ErrInvalidKind, kind)
	}
	return text, nil
}

// decodeEventKind is the inverse of encodeEventKind, used when reading an
// events row back (Stage 3's query.go).
func decodeEventKind(text string) (kernelv1.EventKind, error) {
	kind, ok := eventTextKind[text]
	if !ok {
		return kernelv1.EventKind_EVENT_KIND_UNSPECIFIED, fmt.Errorf("statebackend: %w: %q", ErrInvalidKind, text)
	}
	return kind, nil
}

// producerCategoryText maps a plugin Category to the lowercase text this
// package stores in events.producer_category and producers.category.
// state-backend.md's DDL leaves the column's exact text vocabulary
// undocumented (unlike session_meta.status and events.kind, which the spec
// enumerates literally) — this uses the same lowercase category names the
// specifications/ tree itself uses as directory names (model/, tool/,
// context/, memory/, frontend/, widget/, slashcommand/), for consistency
// with every other lowercase-text enum this package stores.
// CATEGORY_UNSPECIFIED is deliberately absent — a producer's category MUST
// NOT ever be unspecified (kernel-callbacks.md's server-derived producer
// identity is always a real category).
var producerCategoryText = map[commonv1.Category]string{
	commonv1.Category_CATEGORY_MODEL:        "model",
	commonv1.Category_CATEGORY_TOOL:         "tool",
	commonv1.Category_CATEGORY_CONTEXT:      "context",
	commonv1.Category_CATEGORY_MEMORY:       "memory",
	commonv1.Category_CATEGORY_FRONTEND:     "frontend",
	commonv1.Category_CATEGORY_WIDGET:       "widget",
	commonv1.Category_CATEGORY_SLASHCOMMAND: "slashcommand",
}

// producerTextCategory is producerCategoryText inverted, built once from
// producerCategoryText itself so the two can never drift.
var producerTextCategory = func() map[string]commonv1.Category {
	m := make(map[string]commonv1.Category, len(producerCategoryText))
	for category, text := range producerCategoryText {
		m[text] = category
	}
	return m
}()

// encodeProducerCategory renders category as its stored TEXT
// representation. CATEGORY_UNSPECIFIED and any unrecognized value are
// rejected.
func encodeProducerCategory(category commonv1.Category) (string, error) {
	text, ok := producerCategoryText[category]
	if !ok {
		return "", fmt.Errorf("statebackend: producer category %v has no stored representation", category)
	}
	return text, nil
}

// decodeProducerCategory is the inverse of encodeProducerCategory, used
// when reading an events or producers row back (Stage 3's query.go).
func decodeProducerCategory(text string) (commonv1.Category, error) {
	category, ok := producerTextCategory[text]
	if !ok {
		return commonv1.Category_CATEGORY_UNSPECIFIED, fmt.Errorf("statebackend: unrecognized producer category %q", text)
	}
	return category, nil
}

// planDecisionText maps a PlanDecision to the exact lowercase text
// docs/specifications/state-backend.md#plan_items documents
// ("allow | ask | deny"). PLAN_DECISION_UNSPECIFIED and
// PLAN_DECISION_PENDING are deliberately absent — the plan_items table
// only ever holds a made decision.
var planDecisionText = map[planv1.PlanDecision]string{
	planv1.PlanDecision_PLAN_DECISION_ALLOW: "allow",
	planv1.PlanDecision_PLAN_DECISION_ASK:   "ask",
	planv1.PlanDecision_PLAN_DECISION_DENY:  "deny",
}

// planTextDecision is planDecisionText inverted, built once from
// planDecisionText itself so the two can never drift.
var planTextDecision = func() map[string]planv1.PlanDecision {
	m := make(map[string]planv1.PlanDecision, len(planDecisionText))
	for decision, text := range planDecisionText {
		m[text] = decision
	}
	return m
}()

// encodePlanDecision renders decision as its stored TEXT representation.
// PLAN_DECISION_UNSPECIFIED, PLAN_DECISION_PENDING, and any unrecognized
// value return ErrInvalidDecision.
func encodePlanDecision(decision planv1.PlanDecision) (string, error) {
	text, ok := planDecisionText[decision]
	if !ok {
		return "", fmt.Errorf("statebackend: %w: %v", ErrInvalidDecision, decision)
	}
	return text, nil
}

// decodePlanDecision is the inverse of encodePlanDecision, used when
// reading a plan_items row back (Stage 3's query.go).
func decodePlanDecision(text string) (planv1.PlanDecision, error) {
	decision, ok := planTextDecision[text]
	if !ok {
		return planv1.PlanDecision_PLAN_DECISION_UNSPECIFIED, fmt.Errorf("statebackend: %w: %q", ErrInvalidDecision, text)
	}
	return decision, nil
}
