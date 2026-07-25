package statebackend

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/structpb"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	eventv1 "github.com/pluggableharness/agent/pkg/event/proto/v1"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	planv1 "github.com/pluggableharness/agent/pkg/plan/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
)

func TestEventKind_roundTrip(t *testing.T) {
	t.Parallel()

	for kind := range eventKindText {
		text, err := encodeEventKind(kind)
		if err != nil {
			t.Fatalf("encodeEventKind(%v): %v", kind, err)
		}
		got, err := decodeEventKind(text)
		if err != nil {
			t.Fatalf("decodeEventKind(%q): %v", text, err)
		}
		if got != kind {
			t.Errorf("round trip %v -> %q -> %v, want %v", kind, text, got, kind)
		}
	}
}

func TestEncodeEventKind_unspecifiedRejected(t *testing.T) {
	t.Parallel()
	if _, err := encodeEventKind(kernelv1.EventKind_EVENT_KIND_UNSPECIFIED); !errors.Is(err, ErrInvalidKind) {
		t.Fatalf("encodeEventKind(UNSPECIFIED) err = %v, want ErrInvalidKind", err)
	}
}

func TestDecodeEventKind_unrecognized(t *testing.T) {
	t.Parallel()
	if _, err := decodeEventKind("not_a_kind"); !errors.Is(err, ErrInvalidKind) {
		t.Fatalf("decodeEventKind(garbage) err = %v, want ErrInvalidKind", err)
	}
}

func TestEncodeEventKind_hookError(t *testing.T) {
	t.Parallel()
	// EVENT_KIND_HOOK_ERROR is kernel-synthesized (never emitted by a
	// plugin's own Emit call, per docs/specifications/state-backend.md#the-kind-enum)
	// but round-trips through the same eventKindText table as every other
	// kind; this pins the exact stored text against the kind enum's
	// dedicated table entry, on top of the generic TestEventKind_roundTrip
	// coverage above.
	got, err := encodeEventKind(kernelv1.EventKind_EVENT_KIND_HOOK_ERROR)
	if err != nil {
		t.Fatalf("encodeEventKind(EVENT_KIND_HOOK_ERROR): %v", err)
	}
	if got != "hook_error" {
		t.Errorf("encodeEventKind(EVENT_KIND_HOOK_ERROR) = %q, want %q", got, "hook_error")
	}
}

func TestEventKindText(t *testing.T) {
	t.Parallel()

	// One case per real EventKind, pinning the exact stored text against
	// docs/specifications/state-backend.md#the-kind-enum's vocabulary — the
	// same strings kernel-callbacks.md#emit builds `kernel.event.{kind}`
	// bus topics from, so a typo here is a wire-visible break.
	want := map[kernelv1.EventKind]string{
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

	for kind, wantText := range want {
		t.Run(kind.String(), func(t *testing.T) {
			t.Parallel()
			got, err := EventKindText(kind)
			if err != nil {
				t.Fatalf("EventKindText(%v): %v", kind, err)
			}
			if got != wantText {
				t.Errorf("EventKindText(%v) = %q, want %q", kind, got, wantText)
			}
		})
	}

	// The exported wrapper must cover exactly the internal table, no more:
	// a kind added to eventKindText without a case here is a gap.
	if len(want) != len(eventKindText) {
		t.Errorf("test table covers %d kinds, eventKindText has %d", len(want), len(eventKindText))
	}
}

func TestEventKindText_unspecifiedRejected(t *testing.T) {
	t.Parallel()
	if _, err := EventKindText(kernelv1.EventKind_EVENT_KIND_UNSPECIFIED); !errors.Is(err, ErrInvalidKind) {
		t.Fatalf("EventKindText(UNSPECIFIED) err = %v, want ErrInvalidKind", err)
	}
}

func TestEventKindText_matchesEncodeEventKind(t *testing.T) {
	t.Parallel()
	// The exported wrapper must be the same function, not a second switch.
	for kind := range eventKindText {
		want, err := encodeEventKind(kind)
		if err != nil {
			t.Fatalf("encodeEventKind(%v): %v", kind, err)
		}
		got, err := EventKindText(kind)
		if err != nil {
			t.Fatalf("EventKindText(%v): %v", kind, err)
		}
		if got != want {
			t.Errorf("EventKindText(%v) = %q, encodeEventKind = %q", kind, got, want)
		}
	}
}

func TestEventPayloadType(t *testing.T) {
	t.Parallel()

	// Transcribed from docs/specifications/state-backend.md#the-kind-enum's
	// kind -> event.v1 message table. The wants are asserted against the
	// generated descriptors' own FullName rather than repeated string
	// literals, so a renamed or missing message fails the test rather than
	// silently agreeing with a stale constant.
	tests := []struct {
		kind kernelv1.EventKind
		want protoreflect.FullName
	}{
		{kernelv1.EventKind_EVENT_KIND_MESSAGE, (&eventv1.MessageEvent{}).ProtoReflect().Descriptor().FullName()},
		{kernelv1.EventKind_EVENT_KIND_TOOL_CALL, (&eventv1.ToolCallEvent{}).ProtoReflect().Descriptor().FullName()},
		{kernelv1.EventKind_EVENT_KIND_TOOL_RESULT, (&eventv1.ToolResultEvent{}).ProtoReflect().Descriptor().FullName()},
		{kernelv1.EventKind_EVENT_KIND_PLAN, (&eventv1.PlanEvent{}).ProtoReflect().Descriptor().FullName()},
		{kernelv1.EventKind_EVENT_KIND_APPLY, (&eventv1.ApplyEvent{}).ProtoReflect().Descriptor().FullName()},
		{kernelv1.EventKind_EVENT_KIND_CONTEXT_CONTRIBUTION, (&eventv1.ContextContributionEvent{}).ProtoReflect().Descriptor().FullName()},
		// One message for all three memory kinds — the mutating verb is the
		// kind itself, not a payload field.
		{kernelv1.EventKind_EVENT_KIND_MEMORY_WRITE, (&eventv1.MemoryMutationEvent{}).ProtoReflect().Descriptor().FullName()},
		{kernelv1.EventKind_EVENT_KIND_MEMORY_UPDATE, (&eventv1.MemoryMutationEvent{}).ProtoReflect().Descriptor().FullName()},
		{kernelv1.EventKind_EVENT_KIND_MEMORY_DELETE, (&eventv1.MemoryMutationEvent{}).ProtoReflect().Descriptor().FullName()},
		{kernelv1.EventKind_EVENT_KIND_HOOK_ERROR, (&eventv1.HookErrorEvent{}).ProtoReflect().Descriptor().FullName()},
	}

	for _, tt := range tests {
		t.Run(tt.kind.String(), func(t *testing.T) {
			t.Parallel()
			got, err := EventPayloadType(tt.kind)
			if err != nil {
				t.Fatalf("EventPayloadType(%v): %v", tt.kind, err)
			}
			if got != string(tt.want) {
				t.Errorf("EventPayloadType(%v) = %q, want %q", tt.kind, got, tt.want)
			}
			if !strings.HasPrefix(got, "pluggableharness.event.v1.") {
				t.Errorf("EventPayloadType(%v) = %q, want a pluggableharness.event.v1 message name", tt.kind, got)
			}
		})
	}

	// Every kind that has a stored text must also have a payload type —
	// the two tables describe the same enum and must never drift apart.
	if len(eventPayloadType) != len(eventKindText) {
		t.Errorf("eventPayloadType covers %d kinds, eventKindText %d", len(eventPayloadType), len(eventKindText))
	}
	for kind := range eventKindText {
		if _, ok := eventPayloadType[kind]; !ok {
			t.Errorf("kind %v has a stored text but no payload type", kind)
		}
	}
}

func TestEventPayloadType_unspecifiedRejected(t *testing.T) {
	t.Parallel()
	if _, err := EventPayloadType(kernelv1.EventKind_EVENT_KIND_UNSPECIFIED); !errors.Is(err, ErrInvalidKind) {
		t.Fatalf("EventPayloadType(UNSPECIFIED) err = %v, want ErrInvalidKind", err)
	}
	if _, err := EventPayloadType(kernelv1.EventKind(9999)); !errors.Is(err, ErrInvalidKind) {
		t.Fatalf("EventPayloadType(9999) err = %v, want ErrInvalidKind", err)
	}
}

func TestProducerCategory_roundTrip(t *testing.T) {
	t.Parallel()

	for category := range producerCategoryText {
		text, err := encodeProducerCategory(category)
		if err != nil {
			t.Fatalf("encodeProducerCategory(%v): %v", category, err)
		}
		got, err := decodeProducerCategory(text)
		if err != nil {
			t.Fatalf("decodeProducerCategory(%q): %v", text, err)
		}
		if got != category {
			t.Errorf("round trip %v -> %q -> %v, want %v", category, text, got, category)
		}
	}
}

func TestEncodeProducerCategory_unspecifiedRejected(t *testing.T) {
	t.Parallel()
	if _, err := encodeProducerCategory(commonv1.Category_CATEGORY_UNSPECIFIED); err == nil {
		t.Fatal("encodeProducerCategory(UNSPECIFIED) = nil error, want error")
	}
}

func TestDecodeProducerCategory_unrecognized(t *testing.T) {
	t.Parallel()
	if _, err := decodeProducerCategory("not_a_category"); err == nil {
		t.Fatal("decodeProducerCategory(garbage) = nil error, want error")
	}
}

func TestKernelProducer_fixedIdentity(t *testing.T) {
	t.Parallel()

	p := KernelProducer()
	if p.GetName() != "kernel" {
		t.Errorf("Name = %q, want %q", p.GetName(), "kernel")
	}
	if p.GetVersion() != "1" {
		t.Errorf("Version = %q, want %q", p.GetVersion(), "1")
	}
	if p.GetCategory() != commonv1.Category_CATEGORY_UNSPECIFIED {
		t.Errorf("Category = %v, want CATEGORY_UNSPECIFIED", p.GetCategory())
	}
	if p.GetSource() != "" {
		t.Errorf("Source = %q, want empty", p.GetSource())
	}
	if !IsKernelProducer(p) {
		t.Error("IsKernelProducer(KernelProducer()) = false, want true")
	}

	// A fresh value per call: mutating one caller's ref must not corrupt
	// the next caller's.
	other := KernelProducer()
	if p == other {
		t.Error("KernelProducer returned the same pointer twice, want a fresh value per call")
	}
}

func TestIsKernelProducer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		producer *commonv1.ProducerRef
		want     bool
	}{
		{"kernel producer", KernelProducer(), true},
		{"kernel name, future payload generation", &commonv1.ProducerRef{Name: "kernel", Version: "2"}, true},
		{"nil producer", nil, false},
		{"real plugin", testProducer(), false},
		{"plugin named kernel with a real category", &commonv1.ProducerRef{Name: "kernel", Version: "1", Category: commonv1.Category_CATEGORY_TOOL}, false},
		{"unspecified category, other name", &commonv1.ProducerRef{Name: "rogue", Version: "1"}, false},
		{"unspecified category, empty name", &commonv1.ProducerRef{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsKernelProducer(tt.producer); got != tt.want {
				t.Errorf("IsKernelProducer(%+v) = %v, want %v", tt.producer, got, tt.want)
			}
		})
	}
}

func TestEncodeProducer_kernel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		producer *commonv1.ProducerRef
		kind     kernelv1.EventKind
		want     string
		wantErr  bool
	}{
		{"kernel on plan", KernelProducer(), kernelv1.EventKind_EVENT_KIND_PLAN, "kernel", false},
		{"kernel on apply", KernelProducer(), kernelv1.EventKind_EVENT_KIND_APPLY, "kernel", false},
		{"kernel on tool_call", KernelProducer(), kernelv1.EventKind_EVENT_KIND_TOOL_CALL, "", true},
		{"kernel on message", KernelProducer(), kernelv1.EventKind_EVENT_KIND_MESSAGE, "", true},
		// hook_error is kernel-synthesized but carries the failing
		// subscriber's identity, never the kernel's
		// (state-backend.md#the-kind-enum).
		{"kernel on hook_error", KernelProducer(), kernelv1.EventKind_EVENT_KIND_HOOK_ERROR, "", true},
		// The reserved identity is not a general "unknown producer" escape
		// hatch: an unspecified category with any other name stays as
		// rejected as it has always been, even on a plan event.
		{"unspecified category, other name, on plan", &commonv1.ProducerRef{Name: "rogue", Version: "1"}, kernelv1.EventKind_EVENT_KIND_PLAN, "", true},
		{"real plugin on plan", testProducer(), kernelv1.EventKind_EVENT_KIND_PLAN, "tool", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := encodeProducer(tt.producer, tt.kind)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidProducer) {
					t.Fatalf("encodeProducer err = %v, want ErrInvalidProducer", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("encodeProducer: %v", err)
			}
			if got != tt.want {
				t.Errorf("encodeProducer = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEncodeProducer_everyRealCategoryUnaffected(t *testing.T) {
	t.Parallel()

	// Every real category must encode exactly as encodeProducerCategory
	// already does, for every kind — the kernel-producer branch must be
	// invisible to a legitimate plugin producer.
	for category := range producerCategoryText {
		want, err := encodeProducerCategory(category)
		if err != nil {
			t.Fatalf("encodeProducerCategory(%v): %v", category, err)
		}
		for kind := range eventKindText {
			got, err := encodeProducer(&commonv1.ProducerRef{Category: category, Name: "p", Version: "1"}, kind)
			if err != nil {
				t.Fatalf("encodeProducer(%v, %v): %v", category, kind, err)
			}
			if got != want {
				t.Errorf("encodeProducer(%v, %v) = %q, want %q", category, kind, got, want)
			}
		}
	}
}

func TestDecodeProducer_kernelPairing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		categoryText string
		producerName string
		want         commonv1.Category
		wantErr      bool
	}{
		{"kernel pair", "kernel", "kernel", commonv1.Category_CATEGORY_UNSPECIFIED, false},
		// The reserved category text is only ever meaningful paired with
		// the reserved name — never a general route back to UNSPECIFIED.
		{"kernel category, plugin name", "kernel", "some-plugin", commonv1.Category_CATEGORY_UNSPECIFIED, true},
		{"kernel category, empty name", "kernel", "", commonv1.Category_CATEGORY_UNSPECIFIED, true},
		{"real category, kernel name", "tool", "kernel", commonv1.Category_CATEGORY_TOOL, false},
		{"real category", "memory", "recall", commonv1.Category_CATEGORY_MEMORY, false},
		{"garbage", "not_a_category", "p", commonv1.Category_CATEGORY_UNSPECIFIED, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := decodeProducer(tt.categoryText, tt.producerName)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidProducer) {
					t.Fatalf("decodeProducer(%q, %q) err = %v, want ErrInvalidProducer", tt.categoryText, tt.producerName, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("decodeProducer(%q, %q): %v", tt.categoryText, tt.producerName, err)
			}
			if got != tt.want {
				t.Errorf("decodeProducer(%q, %q) = %v, want %v", tt.categoryText, tt.producerName, got, tt.want)
			}
		})
	}
}

func TestDecodeProducerCategory_rejectsKernelText(t *testing.T) {
	t.Parallel()
	// The general category decoder must never resolve the reserved text:
	// CATEGORY_UNSPECIFIED stays unreachable without the paired name.
	if _, err := decodeProducerCategory("kernel"); !errors.Is(err, ErrInvalidProducer) {
		t.Fatalf("decodeProducerCategory(%q) err = %v, want ErrInvalidProducer", "kernel", err)
	}
}

func TestPlanDecision_roundTrip(t *testing.T) {
	t.Parallel()

	for decision := range planDecisionText {
		text, err := encodePlanDecision(decision)
		if err != nil {
			t.Fatalf("encodePlanDecision(%v): %v", decision, err)
		}
		got, err := decodePlanDecision(text)
		if err != nil {
			t.Fatalf("decodePlanDecision(%q): %v", text, err)
		}
		if got != decision {
			t.Errorf("round trip %v -> %q -> %v, want %v", decision, text, got, decision)
		}
	}
}

func TestEncodePlanDecision_unspecifiedRejected(t *testing.T) {
	t.Parallel()
	if _, err := encodePlanDecision(planv1.PlanDecision_PLAN_DECISION_UNSPECIFIED); !errors.Is(err, ErrInvalidDecision) {
		t.Fatalf("encodePlanDecision(UNSPECIFIED) err = %v, want ErrInvalidDecision", err)
	}
}

func TestEncodePlanDecision_pendingRejected(t *testing.T) {
	t.Parallel()
	// PENDING is a real, valid PlanDecision value elsewhere in the system
	// (a plan item awaiting a decision) but the plan_items table only ever
	// holds a *made* decision — state-backend.md's decision column is
	// documented as "allow | ask | deny" with no "pending" value.
	if _, err := encodePlanDecision(planv1.PlanDecision_PLAN_DECISION_PENDING); !errors.Is(err, ErrInvalidDecision) {
		t.Fatalf("encodePlanDecision(PENDING) err = %v, want ErrInvalidDecision", err)
	}
}

func TestDecodePlanDecision_unrecognized(t *testing.T) {
	t.Parallel()
	if _, err := decodePlanDecision("not_a_decision"); !errors.Is(err, ErrInvalidDecision) {
		t.Fatalf("decodePlanDecision(garbage) err = %v, want ErrInvalidDecision", err)
	}
}

// TestMarshalPayload_isDeterministicAcrossRemarshals is the regression
// test for the defect MarshalPayload exists to prevent. structpb.Struct's
// fields are a proto map, and protobuf-go randomizes map ordering on every
// marshal unless ordering is pinned — so a bare proto.Marshal produces
// different bytes for the identical event on every run, which
// .claude/rules/determinism.md forbids for any persisted payload.
//
// The struct below is deliberately wide: one or two keys can collide into
// the same order by chance often enough to let a broken implementation
// pass intermittently.
func TestMarshalPayload_isDeterministicAcrossRemarshals(t *testing.T) {
	t.Parallel()

	fields := make(map[string]*structpb.Value, 24)
	for i := range 24 {
		fields[fmt.Sprintf("key_%02d", i)] = structpb.NewStringValue(fmt.Sprintf("value-%02d", i))
	}
	ev := &eventv1.ToolCallEvent{
		Call: &toolv1.ToolCall{
			Id:        "call-1",
			ToolName:  "search",
			Arguments: &structpb.Struct{Fields: fields},
		},
	}

	want, err := MarshalPayload(ev)
	if err != nil {
		t.Fatalf("MarshalPayload: %v", err)
	}
	for i := range 100 {
		got, err := MarshalPayload(ev)
		if err != nil {
			t.Fatalf("MarshalPayload (remarshal %d): %v", i, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("MarshalPayload produced different bytes on remarshal %d: a persisted payload must not depend on Go map iteration order", i)
		}
	}
}
