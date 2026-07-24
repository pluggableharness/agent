package statebackend

import (
	"errors"
	"testing"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	planv1 "github.com/pluggableharness/agent/pkg/plan/proto/v1"
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
