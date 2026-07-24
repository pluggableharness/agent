package slashcommand

import (
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	schemav1 "github.com/pluggableharness/agent/pkg/schema/proto/v1"
	slashcommandv1 "github.com/pluggableharness/agent/pkg/slashcommand/proto/v1"
	"github.com/pluggableharness/agent/pkg/tool"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
)

func validTestSpec(name string) *Spec {
	return &Spec{
		Name:        name,
		Description: "a test command",
		InputSchema: &schemav1.Schema{Type: schemav1.SchemaType_SCHEMA_TYPE_OBJECT},
		Kind:        tool.KindDataSource,
		Risk:        tool.RiskClassReadOnly,
		Concurrency: &tool.ConcurrencySpec{Safe: true},
	}
}

func TestToProtoKind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   tool.Kind
		want toolv1.ToolKind
	}{
		{tool.KindResource, toolv1.ToolKind_TOOL_KIND_RESOURCE},
		{tool.KindDataSource, toolv1.ToolKind_TOOL_KIND_DATA_SOURCE},
		{tool.KindInteractive, toolv1.ToolKind_TOOL_KIND_INTERACTIVE},
		{tool.KindUnspecified, toolv1.ToolKind_TOOL_KIND_UNSPECIFIED},
		{tool.Kind(99), toolv1.ToolKind_TOOL_KIND_UNSPECIFIED},
	}
	for _, tt := range tests {
		if got := toProtoKind(tt.in); got != tt.want {
			t.Errorf("toProtoKind(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestToProtoRiskClass(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   tool.RiskClass
		want toolv1.RiskClass
	}{
		{tool.RiskClassReadOnly, toolv1.RiskClass_RISK_CLASS_READ_ONLY},
		{tool.RiskClassLow, toolv1.RiskClass_RISK_CLASS_LOW},
		{tool.RiskClassModerate, toolv1.RiskClass_RISK_CLASS_MODERATE},
		{tool.RiskClassHigh, toolv1.RiskClass_RISK_CLASS_HIGH},
		{tool.RiskClassCritical, toolv1.RiskClass_RISK_CLASS_CRITICAL},
		{tool.RiskClassUnspecified, toolv1.RiskClass_RISK_CLASS_UNSPECIFIED},
		{tool.RiskClass(99), toolv1.RiskClass_RISK_CLASS_UNSPECIFIED},
	}
	for _, tt := range tests {
		if got := toProtoRiskClass(tt.in); got != tt.want {
			t.Errorf("toProtoRiskClass(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestToProtoOutputStream(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   tool.OutputStream
		want toolv1.OutputStream
	}{
		{tool.OutputStreamStdout, toolv1.OutputStream_OUTPUT_STREAM_STDOUT},
		{tool.OutputStreamStderr, toolv1.OutputStream_OUTPUT_STREAM_STDERR},
		{tool.OutputStreamUnspecified, toolv1.OutputStream_OUTPUT_STREAM_UNSPECIFIED},
		{tool.OutputStream(99), toolv1.OutputStream_OUTPUT_STREAM_UNSPECIFIED},
	}
	for _, tt := range tests {
		if got := toProtoOutputStream(tt.in); got != tt.want {
			t.Errorf("toProtoOutputStream(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestToProtoConcurrencySpec(t *testing.T) {
	t.Parallel()

	if got := toProtoConcurrencySpec(nil); got != nil {
		t.Errorf("toProtoConcurrencySpec(nil) = %v, want nil", got)
	}

	got := toProtoConcurrencySpec(&tool.ConcurrencySpec{Safe: true, KeyFields: []string{"path"}})
	if !got.GetSafe() || len(got.GetKeyFields()) != 1 || got.GetKeyFields()[0] != "path" {
		t.Errorf("toProtoConcurrencySpec(...) = %v", got)
	}
}

func TestValidateSpec(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*Spec)
		wantErr error
	}{
		{"valid data_source", func(*Spec) {}, nil},
		{"empty name", func(s *Spec) { s.Name = "" }, ErrEmptyName},
		{"unspecified kind", func(s *Spec) { s.Kind = tool.KindUnspecified }, ErrUnspecifiedKind},
		{"empty description", func(s *Spec) { s.Description = "" }, ErrEmptyDescription},
		{"nil input schema", func(s *Spec) { s.InputSchema = nil }, ErrNilInputSchema},
		{"data_source wrong risk", func(s *Spec) { s.Risk = tool.RiskClassLow }, ErrInvalidRiskForKind},
		{
			"resource wrong risk",
			func(s *Spec) {
				s.Kind = tool.KindResource
				s.Risk = tool.RiskClassReadOnly
			},
			ErrInvalidRiskForKind,
		},
		{
			"resource missing concurrency",
			func(s *Spec) {
				s.Kind = tool.KindResource
				s.Risk = tool.RiskClassHigh
				s.Concurrency = nil
			},
			ErrConcurrencyRequired,
		},
		{
			"interactive with concurrency",
			func(s *Spec) {
				s.Kind = tool.KindInteractive
				s.Risk = tool.RiskClassReadOnly
			},
			ErrConcurrencyForbiddenForInteractive,
		},
		{
			"interactive without concurrency ok",
			func(s *Spec) {
				s.Kind = tool.KindInteractive
				s.Risk = tool.RiskClassReadOnly
				s.Concurrency = nil
			},
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := validTestSpec("cmd")
			tt.mutate(s)
			err := validateSpec(s)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("validateSpec() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("validateSpec() = %v, want wrapping %v", err, tt.wantErr)
			}
		})
	}
}

func TestToProtoSpec(t *testing.T) {
	t.Parallel()

	if _, err := toProtoSpec(nil); !errors.Is(err, ErrNilSpec) {
		t.Fatalf("toProtoSpec(nil) error = %v, want wrapping %v", err, ErrNilSpec)
	}

	s := validTestSpec("deploy")
	s.Streaming = true
	s.Idempotent = true
	s.DefaultTimeout = 5 * time.Second

	got, err := toProtoSpec(s)
	if err != nil {
		t.Fatalf("toProtoSpec: %v", err)
	}
	if got.GetName() != "deploy" || !got.GetStreaming() || !got.GetIdempotent() {
		t.Errorf("toProtoSpec(...) = %v", got)
	}
	if got.GetDefaultTimeout().AsDuration() != 5*time.Second {
		t.Errorf("DefaultTimeout = %v, want 5s", got.GetDefaultTimeout().AsDuration())
	}
	if got.GetKind() != toolv1.ToolKind_TOOL_KIND_DATA_SOURCE {
		t.Errorf("Kind = %v, want TOOL_KIND_DATA_SOURCE", got.GetKind())
	}

	invalid := &Spec{}
	if _, err := toProtoSpec(invalid); err == nil {
		t.Error("toProtoSpec(invalid) = nil error, want error")
	}
}

func TestToProtoSpecNoDefaultTimeoutWhenZero(t *testing.T) {
	t.Parallel()

	s := validTestSpec("deploy")
	got, err := toProtoSpec(s)
	if err != nil {
		t.Fatalf("toProtoSpec: %v", err)
	}
	if got.GetDefaultTimeout() != nil {
		t.Errorf("DefaultTimeout = %v, want nil (unset DefaultTimeout)", got.GetDefaultTimeout())
	}
}

func TestToProtoToolResult(t *testing.T) {
	t.Parallel()

	if _, err := toProtoToolResult(nil); !errors.Is(err, tool.ErrNilResult) {
		t.Fatalf("toProtoToolResult(nil) error = %v, want wrapping %v", err, tool.ErrNilResult)
	}

	got, err := toProtoToolResult(&tool.Result{Payload: map[string]any{"ok": true}})
	if err != nil {
		t.Fatalf("toProtoToolResult: %v", err)
	}
	if !got.GetPayload().AsMap()["ok"].(bool) {
		t.Errorf("Payload = %v", got.GetPayload())
	}

	empty, err := toProtoToolResult(&tool.Result{})
	if err != nil {
		t.Fatalf("toProtoToolResult(empty): %v", err)
	}
	if empty.GetPayload() != nil {
		t.Errorf("Payload = %v, want nil for an empty result", empty.GetPayload())
	}
}

func TestToProtoToolError(t *testing.T) {
	t.Parallel()

	if _, err := toProtoToolError(nil); !errors.Is(err, tool.ErrNilError) {
		t.Fatalf("toProtoToolError(nil) error = %v, want wrapping %v", err, tool.ErrNilError)
	}

	got, err := toProtoToolError(&tool.Error{Category: tool.ErrorCategoryNotFound, Message: "missing", Retryable: true, Details: map[string]any{"path": "/x"}})
	if err != nil {
		t.Fatalf("toProtoToolError: %v", err)
	}
	if got.GetCategory() != toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_NOT_FOUND || got.GetMessage() != "missing" || !got.GetRetryable() {
		t.Errorf("toProtoToolError(...) = %v", got)
	}
	if got.GetDetails().AsMap()["path"] != "/x" {
		t.Errorf("Details = %v", got.GetDetails())
	}

	// Invalid category (unspecified) is rejected by the round trip
	// through tool.NewError.
	if _, err := toProtoToolError(&tool.Error{Category: tool.ErrorCategoryUnspecified, Message: "x"}); err == nil {
		t.Error("toProtoToolError(unspecified category) = nil error, want error")
	}
	// Empty message is likewise rejected.
	if _, err := toProtoToolError(&tool.Error{Category: tool.ErrorCategoryUnknown, Message: ""}); err == nil {
		t.Error("toProtoToolError(empty message) = nil error, want error")
	}
}

func TestToProtoEvent(t *testing.T) {
	t.Parallel()

	if _, err := toProtoEvent(nil); !errors.Is(err, ErrNilEvent) {
		t.Fatalf("toProtoEvent(nil) error = %v, want wrapping %v", err, ErrNilEvent)
	}

	if _, err := toProtoEvent(&Event{}); !errors.Is(err, ErrEventFieldCount) {
		t.Fatalf("toProtoEvent(zero-value) error = %v, want wrapping %v", err, ErrEventFieldCount)
	}

	twoSet := NewResultEvent(nil)
	twoSet.Error = &tool.Error{Category: tool.ErrorCategoryUnknown, Message: "x"}
	if _, err := toProtoEvent(twoSet); !errors.Is(err, ErrEventFieldCount) {
		t.Fatalf("toProtoEvent(two fields set) error = %v, want wrapping %v", err, ErrEventFieldCount)
	}

	half := 0.5
	tests := []struct {
		name  string
		event *Event
		check func(t *testing.T, got *slashcommandv1.SlashCommandEvent)
	}{
		{
			"output_chunk",
			NewOutputChunkEvent(tool.OutputStreamStdout, []byte("hi")),
			func(t *testing.T, got *slashcommandv1.SlashCommandEvent) {
				t.Helper()
				if got.GetOutputChunk() == nil || string(got.GetOutputChunk().GetData()) != "hi" {
					t.Errorf("output_chunk = %v", got.GetOutputChunk())
				}
			},
		},
		{
			"progress",
			NewProgressEvent("working", &half),
			func(t *testing.T, got *slashcommandv1.SlashCommandEvent) {
				t.Helper()
				if got.GetProgress() == nil || got.GetProgress().GetFractionComplete() != 0.5 {
					t.Errorf("progress = %v", got.GetProgress())
				}
			},
		},
		{
			"partial_result",
			NewPartialResultEvent(map[string]any{"n": 1.0}),
			func(t *testing.T, got *slashcommandv1.SlashCommandEvent) {
				t.Helper()
				if got.GetPartialResult() == nil || got.GetPartialResult().GetPayload().AsMap()["n"] != 1.0 {
					t.Errorf("partial_result = %v", got.GetPartialResult())
				}
			},
		},
		{
			"exit_status",
			NewExitStatusEvent(1, nil),
			func(t *testing.T, got *slashcommandv1.SlashCommandEvent) {
				t.Helper()
				if got.GetExitStatus() == nil || got.GetExitStatus().GetExitCode() != 1 {
					t.Errorf("exit_status = %v", got.GetExitStatus())
				}
			},
		},
		{
			"result",
			NewResultEvent(map[string]any{"ok": true}),
			func(t *testing.T, got *slashcommandv1.SlashCommandEvent) {
				t.Helper()
				if got.GetResult() == nil || !got.GetResult().GetPayload().AsMap()["ok"].(bool) {
					t.Errorf("result = %v", got.GetResult())
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := toProtoEvent(tt.event)
			if err != nil {
				t.Fatalf("toProtoEvent: %v", err)
			}
			tt.check(t, got)
		})
	}

	t.Run("error", func(t *testing.T) {
		t.Parallel()
		te, err := tool.NewError(tool.ErrorCategoryNotFound, "gone", false, nil)
		if err != nil {
			t.Fatalf("tool.NewError: %v", err)
		}
		got, err := toProtoEvent(NewErrorEvent(te))
		if err != nil {
			t.Fatalf("toProtoEvent: %v", err)
		}
		if got.GetError() == nil || got.GetError().GetMessage() != "gone" {
			t.Errorf("error = %v", got.GetError())
		}
	})

	t.Run("partial_result conversion failure propagates", func(t *testing.T) {
		t.Parallel()
		_, err := toProtoEvent(NewPartialResultEvent(map[string]any{"bad": make(chan int)}))
		if err == nil {
			t.Error("toProtoEvent(partial_result with unencodable payload) = nil error, want error")
		}
	})

	t.Run("result conversion failure propagates", func(t *testing.T) {
		t.Parallel()
		_, err := toProtoEvent(NewResultEvent(map[string]any{"bad": make(chan int)}))
		if err == nil {
			t.Error("toProtoEvent(result with unencodable payload) = nil error, want error")
		}
	})

	t.Run("error conversion failure propagates", func(t *testing.T) {
		t.Parallel()
		_, err := toProtoEvent(NewErrorEvent(&tool.Error{Category: tool.ErrorCategoryUnspecified, Message: "x"}))
		if err == nil {
			t.Error("toProtoEvent(invalid error) = nil error, want error")
		}
	})
}

func TestFromProtoCall(t *testing.T) {
	t.Parallel()

	if _, err := fromProtoCall(nil); !errors.Is(err, ErrNilCall) {
		t.Fatalf("fromProtoCall(nil) error = %v, want wrapping %v", err, ErrNilCall)
	}

	args, err := structpb.NewStruct(map[string]any{"env": "prod"})
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}
	wireCall := &slashcommandv1.SlashCommandCall{
		Id:          "c1",
		Name:        "deploy",
		Arguments:   args,
		CallContext: &commonv1.CallContext{SessionId: "s1", TurnId: "t1", WorkingDirectory: "/work"},
	}

	got, err := fromProtoCall(wireCall)
	if err != nil {
		t.Fatalf("fromProtoCall: %v", err)
	}
	if got.ID != "c1" || got.Name != "deploy" || got.Arguments["env"] != "prod" {
		t.Errorf("fromProtoCall(...) = %+v", got)
	}
	if got.CallContext.GetSessionId() != "s1" {
		t.Errorf("CallContext = %v", got.CallContext)
	}
}

func TestStructToMapNil(t *testing.T) {
	t.Parallel()
	if got := structToMap(nil); got != nil {
		t.Errorf("structToMap(nil) = %v, want nil", got)
	}
}

func TestMapToStructEmpty(t *testing.T) {
	t.Parallel()
	got, err := mapToStruct(nil)
	if err != nil {
		t.Fatalf("mapToStruct(nil): %v", err)
	}
	if got != nil {
		t.Errorf("mapToStruct(nil) = %v, want nil", got)
	}
}

func TestMapToStructError(t *testing.T) {
	t.Parallel()
	if _, err := mapToStruct(map[string]any{"bad": make(chan int)}); err == nil {
		t.Error("mapToStruct(unencodable) = nil error, want error")
	}
}
