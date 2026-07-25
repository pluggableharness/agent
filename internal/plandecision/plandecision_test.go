package plandecision_test

import (
	"errors"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/pluggableharness/agent/internal/plandecision"
	"github.com/pluggableharness/agent/internal/schemavalidate"
	planv1 "github.com/pluggableharness/agent/pkg/plan/proto/v1"
	schemav1 "github.com/pluggableharness/agent/pkg/schema/proto/v1"
)

// pathSchema is an object schema requiring a string "path" property — the
// shape a file-writing resource operation would declare.
func pathSchema() *schemav1.Schema {
	return &schemav1.Schema{
		Type: schemav1.SchemaType_SCHEMA_TYPE_OBJECT,
		Properties: map[string]*schemav1.Schema{
			"path": {Type: schemav1.SchemaType_SCHEMA_TYPE_STRING},
		},
		Required: []string{"path"},
	}
}

func mustStruct(t *testing.T, m map[string]any) *structpb.Struct {
	t.Helper()

	s, err := structpb.NewStruct(m)
	if err != nil {
		t.Fatalf("structpb.NewStruct(%v): %v", m, err)
	}
	return s
}

func TestRequestValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  plandecision.Request
		want error
	}{
		{
			name: "nil item",
			req:  plandecision.Request{SessionID: "s-1"},
			want: plandecision.ErrNilItem,
		},
		{
			name: "item present",
			req:  plandecision.Request{Item: &planv1.PlanItem{Id: "pi-1"}},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if err := tt.req.Validate(); !errors.Is(err, tt.want) {
				t.Fatalf("Validate() = %v, want errors.Is %v", err, tt.want)
			}
		})
	}
}

func TestValidateDecision(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		schema  *schemav1.Schema
		dec     plandecision.Decision
		wantErr error
	}{
		{
			name: "allow is terminal",
			dec:  plandecision.Decision{Decision: planv1.PlanDecision_PLAN_DECISION_ALLOW},
		},
		{
			name: "deny is terminal",
			dec:  plandecision.Decision{Decision: planv1.PlanDecision_PLAN_DECISION_DENY},
		},
		{
			name:    "unspecified is not terminal",
			dec:     plandecision.Decision{Decision: planv1.PlanDecision_PLAN_DECISION_UNSPECIFIED},
			wantErr: plandecision.ErrNonTerminalDecision,
		},
		{
			name:    "pending is not terminal",
			dec:     plandecision.Decision{Decision: planv1.PlanDecision_PLAN_DECISION_PENDING},
			wantErr: plandecision.ErrNonTerminalDecision,
		},
		{
			name:    "ask is not terminal",
			dec:     plandecision.Decision{Decision: planv1.PlanDecision_PLAN_DECISION_ASK},
			wantErr: plandecision.ErrNonTerminalDecision,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := plandecision.Request{Item: &planv1.PlanItem{Id: "pi-1"}, InputSchema: tt.schema}
			err := plandecision.ValidateDecision(req, tt.dec)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("ValidateDecision() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ValidateDecision() = %v, want errors.Is %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateDecision_correctedInput(t *testing.T) {
	t.Parallel()

	t.Run("valid correction passes", func(t *testing.T) {
		t.Parallel()

		req := plandecision.Request{Item: &planv1.PlanItem{Id: "pi-1"}, InputSchema: pathSchema()}
		dec := plandecision.Decision{
			Decision:       planv1.PlanDecision_PLAN_DECISION_ALLOW,
			CorrectedInput: mustStruct(t, map[string]any{"path": "/tmp/safe"}),
		}
		if err := plandecision.ValidateDecision(req, dec); err != nil {
			t.Fatalf("ValidateDecision() = %v, want nil", err)
		}
	})

	t.Run("invalid correction is a distinct error, not a downgrade", func(t *testing.T) {
		t.Parallel()

		req := plandecision.Request{Item: &planv1.PlanItem{Id: "pi-1"}, InputSchema: pathSchema()}
		dec := plandecision.Decision{
			Decision:       planv1.PlanDecision_PLAN_DECISION_ALLOW,
			CorrectedInput: mustStruct(t, map[string]any{"wrong": "key"}),
		}
		err := plandecision.ValidateDecision(req, dec)
		if !errors.Is(err, schemavalidate.ErrValidation) {
			t.Fatalf("ValidateDecision() = %v, want errors.Is schemavalidate.ErrValidation", err)
		}
	})

	t.Run("no schema declared skips re-validation", func(t *testing.T) {
		t.Parallel()

		req := plandecision.Request{Item: &planv1.PlanItem{Id: "pi-1"}}
		dec := plandecision.Decision{
			Decision:       planv1.PlanDecision_PLAN_DECISION_ALLOW,
			CorrectedInput: mustStruct(t, map[string]any{"anything": "goes"}),
		}
		if err := plandecision.ValidateDecision(req, dec); err != nil {
			t.Fatalf("ValidateDecision() = %v, want nil", err)
		}
	})

	t.Run("nil correction with a schema is not a violation", func(t *testing.T) {
		t.Parallel()

		req := plandecision.Request{Item: &planv1.PlanItem{Id: "pi-1"}, InputSchema: pathSchema()}
		dec := plandecision.Decision{Decision: planv1.PlanDecision_PLAN_DECISION_ALLOW}
		if err := plandecision.ValidateDecision(req, dec); err != nil {
			t.Fatalf("ValidateDecision() = %v, want nil", err)
		}
	})
}

func TestErrPolicyPersistenceUnavailable_isDistinct(t *testing.T) {
	t.Parallel()

	// The spec requires this be surfaced as its own error rather than
	// collapsing into any other failure mode, so a caller can tell an
	// unpersistable ALWAYS apart from a malformed verdict.
	if errors.Is(plandecision.ErrPolicyPersistenceUnavailable, plandecision.ErrNonTerminalDecision) {
		t.Fatal("ErrPolicyPersistenceUnavailable must not alias ErrNonTerminalDecision")
	}
	if errors.Is(plandecision.ErrPolicyPersistenceUnavailable, plandecision.ErrNilItem) {
		t.Fatal("ErrPolicyPersistenceUnavailable must not alias ErrNilItem")
	}
}
