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
	// ErrNilSchema is returned when converting a nil *Schema.
	ErrNilSchema = errors.New("tool: tool schema must not be nil")
	// ErrNilResult is returned when converting a nil *Result.
	ErrNilResult = errors.New("tool: tool result must not be nil")
	// ErrNilError is returned when converting a nil *Error.
	ErrNilError = errors.New("tool: tool error must not be nil")
	// ErrNilCall is returned when converting a nil *toolv1.ToolCall.
	ErrNilCall = errors.New("tool: call must not be nil")
	// ErrNilEvent is returned by Stream.Send and toProtoEvent for a
	// nil *Event.
	ErrNilEvent = errors.New("tool: event must not be nil")
	// ErrEventFieldCount is returned when a Event does not have
	// exactly one of its six fields set.
	ErrEventFieldCount = errors.New("tool: event must set exactly one field")

	// ErrEmptyName is returned when a Schema's Name is empty.
	ErrEmptyName = errors.New("tool: name must not be empty")
	// ErrUnspecifiedKind is returned when a Schema's Kind is
	// KindUnspecified.
	ErrUnspecifiedKind = errors.New("tool: kind must not be unspecified")
	// ErrEmptyDescription is returned when a Schema's Description is
	// empty.
	ErrEmptyDescription = errors.New("tool: description must not be empty")
	// ErrNilInputSchema is returned when a Schema's InputSchema is
	// nil.
	ErrNilInputSchema = errors.New("tool: input_schema must not be nil")
	// ErrNilOutputSchema is returned when a Schema's OutputSchema is
	// nil.
	ErrNilOutputSchema = errors.New("tool: output_schema must not be nil")
	// ErrInvalidRiskForKind is returned when a Schema's Risk does not
	// match what its Kind requires — RiskClassReadOnly for
	// KindDataSource/KindInteractive, one of
	// low/moderate/high/critical for KindResource.
	ErrInvalidRiskForKind = errors.New("tool: risk does not match kind's required risk classification")
	// ErrConcurrencyRequired is returned when a Schema's Concurrency
	// is nil for a kind other than KindInteractive.
	ErrConcurrencyRequired = errors.New("tool: concurrency must be set except for kind interactive")
	// ErrConcurrencyForbiddenForInteractive is returned when a
	// KindInteractive Schema declares a non-nil Concurrency.
	// docs/specifications/tool/data-types.md#concurrencyspec says the
	// kernel MUST ignore a declared ConcurrencySpec for an interactive
	// operation and enforce sequential execution unconditionally; this
	// package's judgment call is to reject the construction outright
	// instead of silently stripping it, surfacing the author's mistake
	// immediately rather than papering over it — see doc.go and the
	// package report for this call's rationale.
	ErrConcurrencyForbiddenForInteractive = errors.New("tool: concurrency must not be declared for kind interactive")
)

// toProtoKind converts k to its wire representation.
func toProtoKind(k Kind) toolv1.ToolKind {
	switch k {
	case KindResource:
		return toolv1.ToolKind_TOOL_KIND_RESOURCE
	case KindDataSource:
		return toolv1.ToolKind_TOOL_KIND_DATA_SOURCE
	case KindInteractive:
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

// toProtoErrorCategory converts c to its wire representation.
func toProtoErrorCategory(c ErrorCategory) toolv1.ToolErrorCategory {
	switch c {
	case ErrorCategoryInvalidArguments:
		return toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_INVALID_ARGUMENTS
	case ErrorCategoryNotFound:
		return toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_NOT_FOUND
	case ErrorCategoryPermissionDenied:
		return toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_PERMISSION_DENIED
	case ErrorCategoryExecutionFailed:
		return toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_EXECUTION_FAILED
	case ErrorCategoryTimeout:
		return toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_TIMEOUT
	case ErrorCategoryConcurrencyConflict:
		return toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_CONCURRENCY_CONFLICT
	case ErrorCategoryCancelled:
		return toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_CANCELLED
	case toolErrorCategoryProcessCrashed:
		return toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_PROCESS_CRASHED
	case ErrorCategoryUnknown:
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

// validateSchema checks the MUST-level invariants
// docs/specifications/tool/protocol.md#getschema and
// docs/specifications/tool/data-types.md#riskclass place on a Schema.
func validateSchema(s *Schema) error {
	if s.Name == "" {
		return ErrEmptyName
	}
	if s.Kind == KindUnspecified {
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
	case KindDataSource, KindInteractive:
		if s.Risk != RiskClassReadOnly {
			return fmt.Errorf("%w: %s requires read_only, got %s", ErrInvalidRiskForKind, s.Kind, s.Risk)
		}
	case KindResource:
		switch s.Risk {
		case RiskClassLow, RiskClassModerate, RiskClassHigh, RiskClassCritical:
		default:
			return fmt.Errorf("%w: resource requires one of low/moderate/high/critical, got %s", ErrInvalidRiskForKind, s.Risk)
		}
	}

	if s.Kind == KindInteractive {
		if s.Concurrency != nil {
			return ErrConcurrencyForbiddenForInteractive
		}
	} else if s.Concurrency == nil {
		return ErrConcurrencyRequired
	}

	return nil
}

// toProtoSchema validates s and converts it to its wire
// representation.
func toProtoSchema(s *Schema) (*toolv1.ToolSchema, error) {
	if s == nil {
		return nil, ErrNilSchema
	}
	if err := validateSchema(s); err != nil {
		return nil, fmt.Errorf("tool: tool schema %q: %w", s.Name, err)
	}

	ps := &toolv1.ToolSchema{
		Name:         s.Name,
		Kind:         toProtoKind(s.Kind),
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

// toProtoResult converts r to its wire representation.
func toProtoResult(r *Result) (*toolv1.ToolResult, error) {
	if r == nil {
		return nil, ErrNilResult
	}
	payload, err := mapToStruct(r.Payload)
	if err != nil {
		return nil, fmt.Errorf("tool: tool result: %w", err)
	}
	return &toolv1.ToolResult{Payload: payload}, nil
}

// toProtoError validates e's category and converts it to its wire
// representation.
func toProtoError(e *Error) (*toolv1.ToolError, error) {
	if e == nil {
		return nil, ErrNilError
	}
	if err := validateErrorCategory(e.Category); err != nil {
		return nil, fmt.Errorf("tool: tool error: %w", err)
	}

	pe := &toolv1.ToolError{
		Category:  toProtoErrorCategory(e.Category),
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

// toProtoEvent converts e to its wire representation, rejecting a nil
// event or one that does not set exactly one field — the same "exactly
// one of result/error closes the stream, everything else is optional but
// still exactly-one-of-six-per-message" shape
// docs/specifications/tool/data-types.md#toolcall--toolevent--toolresult
// describes for the underlying oneof.
func toProtoEvent(e *Event) (*toolv1.ToolEvent, error) {
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
		pr, err := toProtoResult(e.Result)
		if err != nil {
			return nil, fmt.Errorf("tool: tool event: %w", err)
		}
		return &toolv1.ToolEvent{Event: &toolv1.ToolEvent_Result{Result: pr}}, nil
	default: // e.Error != nil, guaranteed by the exactly-one-field check above.
		pe, err := toProtoError(e.Error)
		if err != nil {
			return nil, fmt.Errorf("tool: tool event: %w", err)
		}
		return &toolv1.ToolEvent{Event: &toolv1.ToolEvent_Error{Error: pe}}, nil
	}
}

// fromProtoCall converts c from its wire representation.
func fromProtoCall(c *toolv1.ToolCall) (*Call, error) {
	if c == nil {
		return nil, ErrNilCall
	}
	return &Call{
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
