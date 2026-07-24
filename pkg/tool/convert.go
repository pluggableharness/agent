package tool

import (
	"errors"
	"fmt"

	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"

	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
)

// Sentinel errors returned by the domain<->proto conversions in this file.
var (
	// ErrNilToolSchema is returned when converting a nil *ToolSchema.
	ErrNilToolSchema = errors.New("tool: tool schema must not be nil")
	// ErrNilToolResult is returned when converting a nil *ToolResult.
	ErrNilToolResult = errors.New("tool: tool result must not be nil")
	// ErrNilToolError is returned when converting a nil *ToolError.
	ErrNilToolError = errors.New("tool: tool error must not be nil")
	// ErrNilToolCall is returned when converting a nil *toolv1.ToolCall.
	ErrNilToolCall = errors.New("tool: call must not be nil")
	// ErrNilEvent is returned by Stream.Send and toProtoToolEvent for a
	// nil *ToolEvent.
	ErrNilEvent = errors.New("tool: event must not be nil")
	// ErrEventFieldCount is returned when a ToolEvent does not have
	// exactly one of its six fields set.
	ErrEventFieldCount = errors.New("tool: event must set exactly one field")

	// ErrEmptyName is returned when a ToolSchema's Name is empty.
	ErrEmptyName = errors.New("tool: name must not be empty")
	// ErrUnspecifiedKind is returned when a ToolSchema's Kind is
	// ToolKindUnspecified.
	ErrUnspecifiedKind = errors.New("tool: kind must not be unspecified")
	// ErrEmptyDescription is returned when a ToolSchema's Description is
	// empty.
	ErrEmptyDescription = errors.New("tool: description must not be empty")
	// ErrNilInputSchema is returned when a ToolSchema's InputSchema is
	// nil.
	ErrNilInputSchema = errors.New("tool: input_schema must not be nil")
	// ErrNilOutputSchema is returned when a ToolSchema's OutputSchema is
	// nil.
	ErrNilOutputSchema = errors.New("tool: output_schema must not be nil")
	// ErrInvalidRiskForKind is returned when a ToolSchema's Risk does not
	// match what its Kind requires — RiskClassReadOnly for
	// ToolKindDataSource/ToolKindInteractive, one of
	// low/moderate/high/critical for ToolKindResource.
	ErrInvalidRiskForKind = errors.New("tool: risk does not match kind's required risk classification")
	// ErrConcurrencyRequired is returned when a ToolSchema's Concurrency
	// is nil for a kind other than ToolKindInteractive.
	ErrConcurrencyRequired = errors.New("tool: concurrency must be set except for kind interactive")
	// ErrConcurrencyForbiddenForInteractive is returned when a
	// ToolKindInteractive ToolSchema declares a non-nil Concurrency.
	// docs/specifications/tool/data-types.md#concurrencyspec says the
	// kernel MUST ignore a declared ConcurrencySpec for an interactive
	// operation and enforce sequential execution unconditionally; this
	// package's judgment call is to reject the construction outright
	// instead of silently stripping it, surfacing the author's mistake
	// immediately rather than papering over it — see doc.go and the
	// package report for this call's rationale.
	ErrConcurrencyForbiddenForInteractive = errors.New("tool: concurrency must not be declared for kind interactive")
)

// toProtoToolKind converts k to its wire representation.
func toProtoToolKind(k ToolKind) toolv1.ToolKind {
	switch k {
	case ToolKindResource:
		return toolv1.ToolKind_TOOL_KIND_RESOURCE
	case ToolKindDataSource:
		return toolv1.ToolKind_TOOL_KIND_DATA_SOURCE
	case ToolKindInteractive:
		return toolv1.ToolKind_TOOL_KIND_INTERACTIVE
	default:
		return toolv1.ToolKind_TOOL_KIND_UNSPECIFIED
	}
}

// toProtoRiskClass converts r to its wire representation.
func toProtoRiskClass(r RiskClass) toolv1.RiskClass {
	switch r {
	case RiskClassReadOnly:
		return toolv1.RiskClass_RISK_CLASS_READ_ONLY
	case RiskClassLow:
		return toolv1.RiskClass_RISK_CLASS_LOW
	case RiskClassModerate:
		return toolv1.RiskClass_RISK_CLASS_MODERATE
	case RiskClassHigh:
		return toolv1.RiskClass_RISK_CLASS_HIGH
	case RiskClassCritical:
		return toolv1.RiskClass_RISK_CLASS_CRITICAL
	default:
		return toolv1.RiskClass_RISK_CLASS_UNSPECIFIED
	}
}

// toProtoOutputStream converts s to its wire representation.
func toProtoOutputStream(s OutputStream) toolv1.OutputStream {
	switch s {
	case OutputStreamStdout:
		return toolv1.OutputStream_OUTPUT_STREAM_STDOUT
	case OutputStreamStderr:
		return toolv1.OutputStream_OUTPUT_STREAM_STDERR
	default:
		return toolv1.OutputStream_OUTPUT_STREAM_UNSPECIFIED
	}
}

// toProtoToolErrorCategory converts c to its wire representation.
func toProtoToolErrorCategory(c ToolErrorCategory) toolv1.ToolErrorCategory {
	switch c {
	case ToolErrorCategoryInvalidArguments:
		return toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_INVALID_ARGUMENTS
	case ToolErrorCategoryNotFound:
		return toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_NOT_FOUND
	case ToolErrorCategoryPermissionDenied:
		return toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_PERMISSION_DENIED
	case ToolErrorCategoryExecutionFailed:
		return toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_EXECUTION_FAILED
	case ToolErrorCategoryTimeout:
		return toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_TIMEOUT
	case ToolErrorCategoryConcurrencyConflict:
		return toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_CONCURRENCY_CONFLICT
	case ToolErrorCategoryCancelled:
		return toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_CANCELLED
	case toolErrorCategoryProcessCrashed:
		return toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_PROCESS_CRASHED
	case ToolErrorCategoryUnknown:
		return toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_UNKNOWN
	default:
		return toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_UNSPECIFIED
	}
}

// toProtoConcurrencySpec converts c to its wire representation. A nil c
// converts to nil.
func toProtoConcurrencySpec(c *ConcurrencySpec) *toolv1.ConcurrencySpec {
	if c == nil {
		return nil
	}
	return &toolv1.ConcurrencySpec{Safe: c.Safe, KeyFields: c.KeyFields}
}

// validateToolSchema checks the MUST-level invariants
// docs/specifications/tool/protocol.md#getschema and
// docs/specifications/tool/data-types.md#riskclass place on a ToolSchema.
func validateToolSchema(s *ToolSchema) error {
	if s.Name == "" {
		return ErrEmptyName
	}
	if s.Kind == ToolKindUnspecified {
		return ErrUnspecifiedKind
	}
	if s.Description == "" {
		return ErrEmptyDescription
	}
	if s.InputSchema == nil {
		return ErrNilInputSchema
	}
	if s.OutputSchema == nil {
		return ErrNilOutputSchema
	}

	switch s.Kind {
	case ToolKindDataSource, ToolKindInteractive:
		if s.Risk != RiskClassReadOnly {
			return fmt.Errorf("%w: %s requires read_only, got %s", ErrInvalidRiskForKind, s.Kind, s.Risk)
		}
	case ToolKindResource:
		switch s.Risk {
		case RiskClassLow, RiskClassModerate, RiskClassHigh, RiskClassCritical:
		default:
			return fmt.Errorf("%w: resource requires one of low/moderate/high/critical, got %s", ErrInvalidRiskForKind, s.Risk)
		}
	}

	if s.Kind == ToolKindInteractive {
		if s.Concurrency != nil {
			return ErrConcurrencyForbiddenForInteractive
		}
	} else if s.Concurrency == nil {
		return ErrConcurrencyRequired
	}

	return nil
}

// toProtoToolSchema validates s and converts it to its wire
// representation.
func toProtoToolSchema(s *ToolSchema) (*toolv1.ToolSchema, error) {
	if s == nil {
		return nil, ErrNilToolSchema
	}
	if err := validateToolSchema(s); err != nil {
		return nil, fmt.Errorf("tool: tool schema %q: %w", s.Name, err)
	}

	ps := &toolv1.ToolSchema{
		Name:         s.Name,
		Kind:         toProtoToolKind(s.Kind),
		Risk:         toProtoRiskClass(s.Risk),
		Description:  s.Description,
		InputSchema:  s.InputSchema,
		OutputSchema: s.OutputSchema,
		Streaming:    s.Streaming,
		Concurrency:  toProtoConcurrencySpec(s.Concurrency),
		Idempotent:   s.Idempotent,
	}
	if s.DefaultTimeout > 0 {
		ps.DefaultTimeout = durationpb.New(s.DefaultTimeout)
	}
	return ps, nil
}

// toProtoToolResult converts r to its wire representation.
func toProtoToolResult(r *ToolResult) (*toolv1.ToolResult, error) {
	if r == nil {
		return nil, ErrNilToolResult
	}
	payload, err := mapToStruct(r.Payload)
	if err != nil {
		return nil, fmt.Errorf("tool: tool result: %w", err)
	}
	return &toolv1.ToolResult{Payload: payload}, nil
}

// toProtoToolError validates e's category and converts it to its wire
// representation.
func toProtoToolError(e *ToolError) (*toolv1.ToolError, error) {
	if e == nil {
		return nil, ErrNilToolError
	}
	if err := validateErrorCategory(e.Category); err != nil {
		return nil, fmt.Errorf("tool: tool error: %w", err)
	}

	pe := &toolv1.ToolError{
		Category:  toProtoToolErrorCategory(e.Category),
		Message:   e.Message,
		Retryable: e.Retryable,
	}
	if len(e.Details) > 0 {
		details, err := mapToStruct(e.Details)
		if err != nil {
			return nil, fmt.Errorf("tool: tool error: details: %w", err)
		}
		pe.Details = details
	}
	return pe, nil
}

// toProtoToolEvent converts e to its wire representation, rejecting a nil
// event or one that does not set exactly one field — the same "exactly
// one of result/error closes the stream, everything else is optional but
// still exactly-one-of-six-per-message" shape
// docs/specifications/tool/data-types.md#toolcall--toolevent--toolresult
// describes for the underlying oneof.
func toProtoToolEvent(e *ToolEvent) (*toolv1.ToolEvent, error) {
	if e == nil {
		return nil, ErrNilEvent
	}

	set := 0
	for _, isSet := range []bool{e.OutputChunk != nil, e.Progress != nil, e.PartialResult != nil, e.ExitStatus != nil, e.Result != nil, e.Error != nil} {
		if isSet {
			set++
		}
	}
	if set != 1 {
		return nil, fmt.Errorf("tool: tool event: %w: got %d fields set", ErrEventFieldCount, set)
	}

	switch {
	case e.OutputChunk != nil:
		return &toolv1.ToolEvent{Event: &toolv1.ToolEvent_OutputChunk_{OutputChunk: &toolv1.ToolEvent_OutputChunk{
			Stream: toProtoOutputStream(e.OutputChunk.Stream),
			Data:   e.OutputChunk.Data,
		}}}, nil
	case e.Progress != nil:
		return &toolv1.ToolEvent{Event: &toolv1.ToolEvent_Progress_{Progress: &toolv1.ToolEvent_Progress{
			Message:          e.Progress.Message,
			FractionComplete: e.Progress.FractionComplete,
		}}}, nil
	case e.PartialResult != nil:
		payload, err := mapToStruct(e.PartialResult.Payload)
		if err != nil {
			return nil, fmt.Errorf("tool: tool event: partial_result: %w", err)
		}
		return &toolv1.ToolEvent{Event: &toolv1.ToolEvent_PartialResult_{PartialResult: &toolv1.ToolEvent_PartialResult{Payload: payload}}}, nil
	case e.ExitStatus != nil:
		return &toolv1.ToolEvent{Event: &toolv1.ToolEvent_ExitStatus_{ExitStatus: &toolv1.ToolEvent_ExitStatus{
			ExitCode: e.ExitStatus.ExitCode,
			Signal:   e.ExitStatus.Signal,
		}}}, nil
	case e.Result != nil:
		pr, err := toProtoToolResult(e.Result)
		if err != nil {
			return nil, fmt.Errorf("tool: tool event: %w", err)
		}
		return &toolv1.ToolEvent{Event: &toolv1.ToolEvent_Result{Result: pr}}, nil
	default: // e.Error != nil, guaranteed by the exactly-one-field check above.
		pe, err := toProtoToolError(e.Error)
		if err != nil {
			return nil, fmt.Errorf("tool: tool event: %w", err)
		}
		return &toolv1.ToolEvent{Event: &toolv1.ToolEvent_Error{Error: pe}}, nil
	}
}

// fromProtoToolCall converts c from its wire representation.
func fromProtoToolCall(c *toolv1.ToolCall) (*ToolCall, error) {
	if c == nil {
		return nil, ErrNilToolCall
	}
	return &ToolCall{
		ID:          c.GetId(),
		ToolName:    c.GetToolName(),
		Arguments:   structToMap(c.GetArguments()),
		CallContext: c.GetCallContext(),
	}, nil
}

// structToMap converts s to a plain map, or nil if s is nil.
// structpb.Struct.AsMap never errors.
func structToMap(s *structpb.Struct) map[string]any {
	if s == nil {
		return nil
	}
	return s.AsMap()
}

// mapToStruct converts m to a *structpb.Struct, or nil if m is empty.
func mapToStruct(m map[string]any) (*structpb.Struct, error) {
	if len(m) == 0 {
		return nil, nil //nolint:nilnil // absence of a payload is a meaningful, documented zero value on the wire (an unset embedded message field), not an ambiguous "no result, no error".
	}
	s, err := structpb.NewStruct(m)
	if err != nil {
		return nil, fmt.Errorf("tool: encode struct: %w", err)
	}
	return s, nil
}
