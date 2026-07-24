package slashcommand

import (
	"errors"
	"fmt"

	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"

	slashcommandv1 "github.com/pluggableharness/agent/pkg/slashcommand/proto/v1"
	"github.com/pluggableharness/agent/pkg/tool"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
)

// Sentinel errors returned by the domain<->proto conversions in this file.
var (
	// ErrNilSpec is returned when converting a nil *Spec.
	ErrNilSpec = errors.New("slashcommand: spec must not be nil")
	// ErrNilCall is returned when converting a nil *slashcommandv1.SlashCommandCall.
	ErrNilCall = errors.New("slashcommand: call must not be nil")
	// ErrNilEvent is returned by Stream.Send and toProtoEvent for a nil
	// *Event.
	ErrNilEvent = errors.New("slashcommand: event must not be nil")
	// ErrEventFieldCount is returned when an Event does not have exactly
	// one of its six fields set.
	ErrEventFieldCount = errors.New("slashcommand: event must set exactly one field")

	// ErrEmptyName is returned when a Spec's Name is empty.
	ErrEmptyName = errors.New("slashcommand: name must not be empty")
	// ErrUnspecifiedKind is returned when a Spec's Kind is
	// tool.KindUnspecified.
	ErrUnspecifiedKind = errors.New("slashcommand: kind must not be unspecified")
	// ErrEmptyDescription is returned when a Spec's Description is
	// empty.
	ErrEmptyDescription = errors.New("slashcommand: description must not be empty")
	// ErrNilInputSchema is returned when a Spec's InputSchema is nil.
	ErrNilInputSchema = errors.New("slashcommand: input_schema must not be nil")
	// ErrInvalidRiskForKind is returned when a Spec's Risk does not
	// match what its Kind requires — tool.RiskClassReadOnly for
	// tool.KindDataSource/tool.KindInteractive, one of
	// low/moderate/high/critical for tool.KindResource.
	ErrInvalidRiskForKind = errors.New("slashcommand: risk does not match kind's required risk classification")
	// ErrConcurrencyRequired is returned when a Spec's Concurrency is
	// nil for a kind other than tool.KindInteractive.
	ErrConcurrencyRequired = errors.New("slashcommand: concurrency must be set except for kind interactive")
	// ErrConcurrencyForbiddenForInteractive is returned when a
	// tool.KindInteractive Spec declares a non-nil Concurrency, per
	// docs/specifications/slashcommand/protocol.md#getcapabilities and
	// tool/protocol.md#kind-interactive's "MUST NOT declare a
	// ConcurrencySpec" rule. This package's judgment call, matching
	// pkg/tool's own, is to reject the construction outright rather
	// than silently stripping it.
	ErrConcurrencyForbiddenForInteractive = errors.New("slashcommand: concurrency must not be declared for kind interactive")
)

// toProtoKind converts k to its wire representation — the literal
// pluggableharness.tool.v1.ToolKind, reused verbatim per
// docs/specifications/slashcommand/data-types.md#reused-toolv1-types.
// pkg/tool's own equivalent is unexported, so this package carries its own
// copy of the conversion logic.
func toProtoKind(k tool.Kind) toolv1.ToolKind {
	switch k {
	case tool.KindResource:
		return toolv1.ToolKind_TOOL_KIND_RESOURCE
	case tool.KindDataSource:
		return toolv1.ToolKind_TOOL_KIND_DATA_SOURCE
	case tool.KindInteractive:
		return toolv1.ToolKind_TOOL_KIND_INTERACTIVE
	default:
		return toolv1.ToolKind_TOOL_KIND_UNSPECIFIED
	}
}

// toProtoRiskClass converts r to its wire representation — the literal
// pluggableharness.tool.v1.RiskClass, reused verbatim.
func toProtoRiskClass(r tool.RiskClass) toolv1.RiskClass {
	switch r {
	case tool.RiskClassReadOnly:
		return toolv1.RiskClass_RISK_CLASS_READ_ONLY
	case tool.RiskClassLow:
		return toolv1.RiskClass_RISK_CLASS_LOW
	case tool.RiskClassModerate:
		return toolv1.RiskClass_RISK_CLASS_MODERATE
	case tool.RiskClassHigh:
		return toolv1.RiskClass_RISK_CLASS_HIGH
	case tool.RiskClassCritical:
		return toolv1.RiskClass_RISK_CLASS_CRITICAL
	default:
		return toolv1.RiskClass_RISK_CLASS_UNSPECIFIED
	}
}

// toProtoOutputStream converts s to its wire representation — the literal
// pluggableharness.tool.v1.OutputStream, reused verbatim.
func toProtoOutputStream(s tool.OutputStream) toolv1.OutputStream {
	switch s {
	case tool.OutputStreamStdout:
		return toolv1.OutputStream_OUTPUT_STREAM_STDOUT
	case tool.OutputStreamStderr:
		return toolv1.OutputStream_OUTPUT_STREAM_STDERR
	default:
		return toolv1.OutputStream_OUTPUT_STREAM_UNSPECIFIED
	}
}

// toProtoConcurrencySpec converts c to its wire representation — the
// literal pluggableharness.tool.v1.ConcurrencySpec, reused verbatim. A nil
// c converts to nil.
func toProtoConcurrencySpec(c *tool.ConcurrencySpec) *toolv1.ConcurrencySpec {
	if c == nil {
		return nil
	}
	return &toolv1.ConcurrencySpec{Safe: c.Safe, KeyFields: c.KeyFields}
}

// validateSpec checks the MUST-level invariants
// docs/specifications/slashcommand/protocol.md#getcapabilities and
// docs/specifications/slashcommand/data-types.md#slashcommandspec place on
// a Spec — the same kind/risk/concurrency rules tool.Schema carries,
// applied verbatim per data-types.md, minus the OutputSchema check (this
// category declares no output_schema at all).
func validateSpec(s *Spec) error {
	if s.Name == "" {
		return ErrEmptyName
	}
	if s.Kind == tool.KindUnspecified {
		return ErrUnspecifiedKind
	}
	if s.Description == "" {
		return ErrEmptyDescription
	}
	if s.InputSchema == nil {
		return ErrNilInputSchema
	}

	switch s.Kind {
	case tool.KindDataSource, tool.KindInteractive:
		if s.Risk != tool.RiskClassReadOnly {
			return fmt.Errorf("%w: %s requires read_only, got %s", ErrInvalidRiskForKind, s.Kind, s.Risk)
		}
	case tool.KindResource:
		switch s.Risk {
		case tool.RiskClassLow, tool.RiskClassModerate, tool.RiskClassHigh, tool.RiskClassCritical:
		default:
			return fmt.Errorf("%w: resource requires one of low/moderate/high/critical, got %s", ErrInvalidRiskForKind, s.Risk)
		}
	}

	if s.Kind == tool.KindInteractive {
		if s.Concurrency != nil {
			return ErrConcurrencyForbiddenForInteractive
		}
	} else if s.Concurrency == nil {
		return ErrConcurrencyRequired
	}

	return nil
}

// toProtoSpec validates s and converts it to its wire representation.
func toProtoSpec(s *Spec) (*slashcommandv1.SlashCommandSpec, error) {
	if s == nil {
		return nil, ErrNilSpec
	}
	if err := validateSpec(s); err != nil {
		return nil, fmt.Errorf("slashcommand: spec %q: %w", s.Name, err)
	}

	ps := &slashcommandv1.SlashCommandSpec{
		Name:        s.Name,
		Description: s.Description,
		InputSchema: s.InputSchema,
		Kind:        toProtoKind(s.Kind),
		Risk:        toProtoRiskClass(s.Risk),
		Concurrency: toProtoConcurrencySpec(s.Concurrency),
		Streaming:   s.Streaming,
		Idempotent:  s.Idempotent,
	}
	if s.DefaultTimeout > 0 {
		ps.DefaultTimeout = durationpb.New(s.DefaultTimeout)
	}
	return ps, nil
}

// toProtoToolResult converts r to its wire representation — the literal
// pluggableharness.tool.v1.ToolResult, reused verbatim as
// SlashCommandEvent.result.
func toProtoToolResult(r *tool.Result) (*toolv1.ToolResult, error) {
	if r == nil {
		return nil, tool.ErrNilResult
	}
	payload, err := mapToStruct(r.Payload)
	if err != nil {
		return nil, fmt.Errorf("slashcommand: tool result: %w", err)
	}
	return &toolv1.ToolResult{Payload: payload}, nil
}

// toProtoToolError converts e to its wire representation — the literal
// pluggableharness.tool.v1.ToolError, reused verbatim as
// SlashCommandEvent.error. Re-validates e's fields by round-tripping
// through tool.NewError rather than duplicating pkg/tool's own (unexported)
// category-validation logic — the one piece of tool.go's error-construction
// behavior this package genuinely reuses at the call site instead of
// re-implementing.
func toProtoToolError(e *tool.Error) (*toolv1.ToolError, error) {
	if e == nil {
		return nil, tool.ErrNilError
	}
	if _, err := tool.NewError(e.Category, e.Message, e.Retryable, e.Details); err != nil {
		return nil, fmt.Errorf("slashcommand: tool error: %w", err)
	}

	pe := &toolv1.ToolError{
		Category:  toProtoErrorCategory(e.Category),
		Message:   e.Message,
		Retryable: e.Retryable,
	}
	if len(e.Details) > 0 {
		details, err := mapToStruct(e.Details)
		if err != nil {
			return nil, fmt.Errorf("slashcommand: tool error: details: %w", err)
		}
		pe.Details = details
	}
	return pe, nil
}

// toProtoErrorCategory converts c to its wire representation. pkg/tool's
// own equivalent is unexported, so this package carries its own copy —
// same reasoning as toProtoKind above.
func toProtoErrorCategory(c tool.ErrorCategory) toolv1.ToolErrorCategory {
	switch c {
	case tool.ErrorCategoryInvalidArguments:
		return toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_INVALID_ARGUMENTS
	case tool.ErrorCategoryNotFound:
		return toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_NOT_FOUND
	case tool.ErrorCategoryPermissionDenied:
		return toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_PERMISSION_DENIED
	case tool.ErrorCategoryExecutionFailed:
		return toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_EXECUTION_FAILED
	case tool.ErrorCategoryTimeout:
		return toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_TIMEOUT
	case tool.ErrorCategoryConcurrencyConflict:
		return toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_CONCURRENCY_CONFLICT
	case tool.ErrorCategoryCancelled:
		return toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_CANCELLED
	case tool.ErrorCategoryUnknown:
		return toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_UNKNOWN
	default:
		return toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_UNSPECIFIED
	}
}

// toProtoEvent converts e to its wire representation, rejecting a nil
// event or one that does not set exactly one field — the same
// "exactly-one-of-six" shape
// docs/specifications/slashcommand/data-types.md#slashcommandcall--slashcommandevent
// describes for the underlying oneof, reused verbatim from
// tool/data-types.md#toolcall--toolevent--toolresult.
func toProtoEvent(e *Event) (*slashcommandv1.SlashCommandEvent, error) {
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
		return nil, fmt.Errorf("slashcommand: event: %w: got %d fields set", ErrEventFieldCount, set)
	}

	switch {
	case e.OutputChunk != nil:
		return &slashcommandv1.SlashCommandEvent{Event: &slashcommandv1.SlashCommandEvent_OutputChunk_{OutputChunk: &slashcommandv1.SlashCommandEvent_OutputChunk{
			Stream: toProtoOutputStream(e.OutputChunk.Stream),
			Data:   e.OutputChunk.Data,
		}}}, nil
	case e.Progress != nil:
		return &slashcommandv1.SlashCommandEvent{Event: &slashcommandv1.SlashCommandEvent_Progress_{Progress: &slashcommandv1.SlashCommandEvent_Progress{
			Message:          e.Progress.Message,
			FractionComplete: e.Progress.FractionComplete,
		}}}, nil
	case e.PartialResult != nil:
		payload, err := mapToStruct(e.PartialResult.Payload)
		if err != nil {
			return nil, fmt.Errorf("slashcommand: event: partial_result: %w", err)
		}
		return &slashcommandv1.SlashCommandEvent{Event: &slashcommandv1.SlashCommandEvent_PartialResult_{PartialResult: &slashcommandv1.SlashCommandEvent_PartialResult{Payload: payload}}}, nil
	case e.ExitStatus != nil:
		return &slashcommandv1.SlashCommandEvent{Event: &slashcommandv1.SlashCommandEvent_ExitStatus_{ExitStatus: &slashcommandv1.SlashCommandEvent_ExitStatus{
			ExitCode: e.ExitStatus.ExitCode,
			Signal:   e.ExitStatus.Signal,
		}}}, nil
	case e.Result != nil:
		pr, err := toProtoToolResult(e.Result)
		if err != nil {
			return nil, fmt.Errorf("slashcommand: event: %w", err)
		}
		return &slashcommandv1.SlashCommandEvent{Event: &slashcommandv1.SlashCommandEvent_Result{Result: pr}}, nil
	default: // e.Error != nil, guaranteed by the exactly-one-field check above.
		pe, err := toProtoToolError(e.Error)
		if err != nil {
			return nil, fmt.Errorf("slashcommand: event: %w", err)
		}
		return &slashcommandv1.SlashCommandEvent{Event: &slashcommandv1.SlashCommandEvent_Error{Error: pe}}, nil
	}
}

// fromProtoCall converts c from its wire representation.
func fromProtoCall(c *slashcommandv1.SlashCommandCall) (*Call, error) {
	if c == nil {
		return nil, ErrNilCall
	}
	return &Call{
		ID:          c.GetId(),
		Name:        c.GetName(),
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
		return nil, fmt.Errorf("slashcommand: encode struct: %w", err)
	}
	return s, nil
}
