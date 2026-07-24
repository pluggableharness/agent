package tool

import (
	"errors"
	"testing"

	schemav1 "github.com/pluggableharness/agent/pkg/schema/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
)

func validSchema() *ToolSchema {
	return &ToolSchema{
		Name:         "read_file",
		Kind:         ToolKindDataSource,
		Risk:         RiskClassReadOnly,
		Description:  "reads a file",
		InputSchema:  &schemav1.Schema{Type: schemav1.SchemaType_SCHEMA_TYPE_OBJECT},
		OutputSchema: &schemav1.Schema{Type: schemav1.SchemaType_SCHEMA_TYPE_OBJECT},
		Streaming:    false,
		Concurrency:  &ConcurrencySpec{Safe: true},
		Idempotent:   true,
	}
}

func TestToProtoToolSchemaValid(t *testing.T) {
	t.Parallel()

	s := validSchema()
	ps, err := toProtoToolSchema(s)
	if err != nil {
		t.Fatalf("toProtoToolSchema: unexpected error: %v", err)
	}
	if ps.GetName() != "read_file" {
		t.Errorf("Name = %q, want %q", ps.GetName(), "read_file")
	}
	if ps.GetKind() != toolv1.ToolKind_TOOL_KIND_DATA_SOURCE {
		t.Errorf("Kind = %v, want %v", ps.GetKind(), toolv1.ToolKind_TOOL_KIND_DATA_SOURCE)
	}
	if ps.GetRisk() != toolv1.RiskClass_RISK_CLASS_READ_ONLY {
		t.Errorf("Risk = %v, want %v", ps.GetRisk(), toolv1.RiskClass_RISK_CLASS_READ_ONLY)
	}
	if !ps.GetConcurrency().GetSafe() {
		t.Errorf("Concurrency.Safe = false, want true")
	}
}

func TestToProtoToolSchemaInvariants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*ToolSchema)
		wantErr error
	}{
		{"nil schema", nil, ErrNilToolSchema},
		{"empty name", func(s *ToolSchema) { s.Name = "" }, ErrEmptyName},
		{"unspecified kind", func(s *ToolSchema) { s.Kind = ToolKindUnspecified }, ErrUnspecifiedKind},
		{"empty description", func(s *ToolSchema) { s.Description = "" }, ErrEmptyDescription},
		{"nil input schema", func(s *ToolSchema) { s.InputSchema = nil }, ErrNilInputSchema},
		{"nil output schema", func(s *ToolSchema) { s.OutputSchema = nil }, ErrNilOutputSchema},
		{"data_source with non-read_only risk", func(s *ToolSchema) { s.Risk = RiskClassLow }, ErrInvalidRiskForKind},
		{"interactive with non-read_only risk", func(s *ToolSchema) {
			s.Kind = ToolKindInteractive
			s.Risk = RiskClassLow
			s.Concurrency = nil
		}, ErrInvalidRiskForKind},
		{"resource with read_only risk", func(s *ToolSchema) {
			s.Kind = ToolKindResource
			s.Risk = RiskClassReadOnly
		}, ErrInvalidRiskForKind},
		{"resource with unspecified risk", func(s *ToolSchema) {
			s.Kind = ToolKindResource
			s.Risk = RiskClassUnspecified
		}, ErrInvalidRiskForKind},
		{"interactive with concurrency declared", func(s *ToolSchema) {
			s.Kind = ToolKindInteractive
			s.Risk = RiskClassReadOnly
			s.Concurrency = &ConcurrencySpec{Safe: true}
		}, ErrConcurrencyForbiddenForInteractive},
		{"resource missing concurrency", func(s *ToolSchema) {
			s.Kind = ToolKindResource
			s.Risk = RiskClassLow
			s.Concurrency = nil
		}, ErrConcurrencyRequired},
		{"data_source missing concurrency", func(s *ToolSchema) {
			s.Concurrency = nil
		}, ErrConcurrencyRequired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var s *ToolSchema
			if tt.mutate != nil {
				s = validSchema()
				tt.mutate(s)
			}

			_, err := toProtoToolSchema(s)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("toProtoToolSchema() error = %v, want wrapping %v", err, tt.wantErr)
			}
		})
	}
}

func TestToProtoToolSchemaDefaultTimeout(t *testing.T) {
	t.Parallel()

	s := validSchema()
	s.DefaultTimeout = 30_000_000_000 // 30s, expressed in ns to avoid importing time here.
	ps, err := toProtoToolSchema(s)
	if err != nil {
		t.Fatalf("toProtoToolSchema: %v", err)
	}
	if ps.GetDefaultTimeout() == nil {
		t.Fatal("DefaultTimeout not set on proto schema")
	}
	if got := ps.GetDefaultTimeout().AsDuration().Seconds(); got != 30 {
		t.Errorf("DefaultTimeout = %vs, want 30s", got)
	}

	s2 := validSchema()
	ps2, err := toProtoToolSchema(s2)
	if err != nil {
		t.Fatalf("toProtoToolSchema: %v", err)
	}
	if ps2.GetDefaultTimeout() != nil {
		t.Errorf("DefaultTimeout = %v, want nil (unset)", ps2.GetDefaultTimeout())
	}
}

func TestToProtoToolResult(t *testing.T) {
	t.Parallel()

	t.Run("nil", func(t *testing.T) {
		t.Parallel()
		if _, err := toProtoToolResult(nil); !errors.Is(err, ErrNilToolResult) {
			t.Errorf("error = %v, want wrapping %v", err, ErrNilToolResult)
		}
	})

	t.Run("with payload", func(t *testing.T) {
		t.Parallel()
		pr, err := toProtoToolResult(&ToolResult{Payload: map[string]any{"ok": true}})
		if err != nil {
			t.Fatalf("toProtoToolResult: %v", err)
		}
		if pr.GetPayload().AsMap()["ok"] != true {
			t.Errorf("Payload = %v, want ok=true", pr.GetPayload().AsMap())
		}
	})

	t.Run("empty payload", func(t *testing.T) {
		t.Parallel()
		pr, err := toProtoToolResult(&ToolResult{})
		if err != nil {
			t.Fatalf("toProtoToolResult: %v", err)
		}
		if pr.GetPayload() != nil {
			t.Errorf("Payload = %v, want nil", pr.GetPayload())
		}
	})
}

func TestToProtoToolError(t *testing.T) {
	t.Parallel()

	t.Run("nil", func(t *testing.T) {
		t.Parallel()
		if _, err := toProtoToolError(nil); !errors.Is(err, ErrNilToolError) {
			t.Errorf("error = %v, want wrapping %v", err, ErrNilToolError)
		}
	})

	t.Run("process_crashed rejected", func(t *testing.T) {
		t.Parallel()
		e := &ToolError{Category: toolErrorCategoryProcessCrashed, Message: "died"}
		if _, err := toProtoToolError(e); !errors.Is(err, ErrProcessCrashedCategory) {
			t.Errorf("error = %v, want wrapping %v", err, ErrProcessCrashedCategory)
		}
	})

	t.Run("valid with details", func(t *testing.T) {
		t.Parallel()
		e := &ToolError{Category: ToolErrorCategoryUnknown, Message: "boom", Retryable: false, Details: map[string]any{"raw": "panic: x"}}
		pe, err := toProtoToolError(e)
		if err != nil {
			t.Fatalf("toProtoToolError: %v", err)
		}
		if pe.GetCategory() != toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_UNKNOWN {
			t.Errorf("Category = %v, want UNKNOWN", pe.GetCategory())
		}
		if pe.GetDetails().AsMap()["raw"] != "panic: x" {
			t.Errorf("Details = %v", pe.GetDetails().AsMap())
		}
	})
}

func TestToProtoToolEvent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		event   *ToolEvent
		wantErr error
	}{
		{"nil event", nil, ErrNilEvent},
		{"no fields set", &ToolEvent{}, ErrEventFieldCount},
		{"two fields set", &ToolEvent{OutputChunk: &OutputChunkEvent{}, Progress: &ProgressEvent{}}, ErrEventFieldCount},
		{"output_chunk", NewOutputChunkEvent(OutputStreamStdout, []byte("hi")), nil},
		{"progress", NewProgressEvent("working", nil), nil},
		{"partial_result", NewPartialResultEvent(map[string]any{"n": 1.0}), nil},
		{"exit_status", NewExitStatusEvent(0, nil), nil},
		{"result", NewResultEvent(map[string]any{"ok": true}), nil},
		{"error", NewErrorEvent(&ToolError{Category: ToolErrorCategoryTimeout, Message: "slow"}), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := toProtoToolEvent(tt.event)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("toProtoToolEvent() error = %v, want wrapping %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("toProtoToolEvent() unexpected error: %v", err)
			}
		})
	}
}

func TestFromProtoToolCall(t *testing.T) {
	t.Parallel()

	t.Run("nil", func(t *testing.T) {
		t.Parallel()
		if _, err := fromProtoToolCall(nil); !errors.Is(err, ErrNilToolCall) {
			t.Errorf("error = %v, want wrapping %v", err, ErrNilToolCall)
		}
	})

	t.Run("valid", func(t *testing.T) {
		t.Parallel()
		args, err := mapToStruct(map[string]any{"path": "a.go"})
		if err != nil {
			t.Fatalf("mapToStruct: %v", err)
		}
		pc := &toolv1.ToolCall{Id: "call-1", ToolName: "read_file", Arguments: args}
		c, err := fromProtoToolCall(pc)
		if err != nil {
			t.Fatalf("fromProtoToolCall: %v", err)
		}
		if c.ID != "call-1" || c.ToolName != "read_file" {
			t.Errorf("got %+v", c)
		}
		if c.Arguments["path"] != "a.go" {
			t.Errorf("Arguments = %v", c.Arguments)
		}
	})
}

func TestToProtoEnumConverters(t *testing.T) {
	t.Parallel()

	kindTests := []struct {
		in   ToolKind
		want toolv1.ToolKind
	}{
		{ToolKindResource, toolv1.ToolKind_TOOL_KIND_RESOURCE},
		{ToolKindDataSource, toolv1.ToolKind_TOOL_KIND_DATA_SOURCE},
		{ToolKindInteractive, toolv1.ToolKind_TOOL_KIND_INTERACTIVE},
		{ToolKindUnspecified, toolv1.ToolKind_TOOL_KIND_UNSPECIFIED},
		{ToolKind(99), toolv1.ToolKind_TOOL_KIND_UNSPECIFIED},
	}
	for _, tt := range kindTests {
		if got := toProtoToolKind(tt.in); got != tt.want {
			t.Errorf("toProtoToolKind(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}

	riskTests := []struct {
		in   RiskClass
		want toolv1.RiskClass
	}{
		{RiskClassReadOnly, toolv1.RiskClass_RISK_CLASS_READ_ONLY},
		{RiskClassLow, toolv1.RiskClass_RISK_CLASS_LOW},
		{RiskClassModerate, toolv1.RiskClass_RISK_CLASS_MODERATE},
		{RiskClassHigh, toolv1.RiskClass_RISK_CLASS_HIGH},
		{RiskClassCritical, toolv1.RiskClass_RISK_CLASS_CRITICAL},
		{RiskClassUnspecified, toolv1.RiskClass_RISK_CLASS_UNSPECIFIED},
		{RiskClass(99), toolv1.RiskClass_RISK_CLASS_UNSPECIFIED},
	}
	for _, tt := range riskTests {
		if got := toProtoRiskClass(tt.in); got != tt.want {
			t.Errorf("toProtoRiskClass(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}

	streamTests := []struct {
		in   OutputStream
		want toolv1.OutputStream
	}{
		{OutputStreamStdout, toolv1.OutputStream_OUTPUT_STREAM_STDOUT},
		{OutputStreamStderr, toolv1.OutputStream_OUTPUT_STREAM_STDERR},
		{OutputStreamUnspecified, toolv1.OutputStream_OUTPUT_STREAM_UNSPECIFIED},
		{OutputStream(99), toolv1.OutputStream_OUTPUT_STREAM_UNSPECIFIED},
	}
	for _, tt := range streamTests {
		if got := toProtoOutputStream(tt.in); got != tt.want {
			t.Errorf("toProtoOutputStream(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}

	categoryTests := []struct {
		in   ToolErrorCategory
		want toolv1.ToolErrorCategory
	}{
		{ToolErrorCategoryInvalidArguments, toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_INVALID_ARGUMENTS},
		{ToolErrorCategoryNotFound, toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_NOT_FOUND},
		{ToolErrorCategoryPermissionDenied, toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_PERMISSION_DENIED},
		{ToolErrorCategoryExecutionFailed, toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_EXECUTION_FAILED},
		{ToolErrorCategoryTimeout, toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_TIMEOUT},
		{ToolErrorCategoryConcurrencyConflict, toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_CONCURRENCY_CONFLICT},
		{ToolErrorCategoryCancelled, toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_CANCELLED},
		{toolErrorCategoryProcessCrashed, toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_PROCESS_CRASHED},
		{ToolErrorCategoryUnknown, toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_UNKNOWN},
		{ToolErrorCategoryUnspecified, toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_UNSPECIFIED},
		{ToolErrorCategory(99), toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_UNSPECIFIED},
	}
	for _, tt := range categoryTests {
		if got := toProtoToolErrorCategory(tt.in); got != tt.want {
			t.Errorf("toProtoToolErrorCategory(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}

	if got := toProtoConcurrencySpec(nil); got != nil {
		t.Errorf("toProtoConcurrencySpec(nil) = %v, want nil", got)
	}
}

func TestStructRoundTrip(t *testing.T) {
	t.Parallel()

	if got := structToMap(nil); got != nil {
		t.Errorf("structToMap(nil) = %v, want nil", got)
	}

	s, err := mapToStruct(nil)
	if err != nil || s != nil {
		t.Errorf("mapToStruct(nil) = (%v, %v), want (nil, nil)", s, err)
	}

	m := map[string]any{"a": 1.0, "b": "x"}
	ps, err := mapToStruct(m)
	if err != nil {
		t.Fatalf("mapToStruct: %v", err)
	}
	got := structToMap(ps)
	if got["a"] != 1.0 || got["b"] != "x" {
		t.Errorf("round trip = %v, want %v", got, m)
	}
}
