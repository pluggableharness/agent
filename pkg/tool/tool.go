package tool

import (
	"context"
	"time"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
	schemav1 "github.com/pluggableharness/agent/pkg/schema/proto/v1"
)

// ToolKind classifies whether an operation is gated behind the plan/apply
// approval gate, executes freely, or blocks the current turn for human
// input — see docs/specifications/tool/protocol.md#getschema and
// docs/specifications/tool/protocol.md#kind-interactive. One of the six
// types pkg/slashcommand reuses verbatim; see doc.go.
type ToolKind int

const (
	// ToolKindUnspecified is the zero value. Never valid for a real
	// operation — its presence means an author forgot to set Kind.
	ToolKindUnspecified ToolKind = iota
	// ToolKindResource is a mutating operation, gated behind the
	// plan/apply approval gate.
	ToolKindResource
	// ToolKindDataSource is a read-only operation. Executes freely,
	// subject only to the policy precheck.
	ToolKindDataSource
	// ToolKindInteractive blocks the current turn for human input and
	// produces no state mutation of its own — the human's answer becomes
	// the result.
	ToolKindInteractive
)

// String returns k's wire-name-derived lowercase form, e.g. "data_source".
func (k ToolKind) String() string {
	switch k {
	case ToolKindUnspecified:
		return "unspecified"
	case ToolKindResource:
		return "resource"
	case ToolKindDataSource:
		return "data_source"
	case ToolKindInteractive:
		return "interactive"
	default:
		return "unknown"
	}
}

// RiskClass classifies an operation's blast radius, orthogonal to
// ToolKind: kind determines whether the plan/apply gate applies at all,
// risk determines how significant the gated (or inherently ungated)
// action is — see docs/specifications/tool/data-types.md#riskclass. One
// of the six types pkg/slashcommand reuses verbatim; see doc.go.
type RiskClass int

const (
	// RiskClassUnspecified is the zero value. Never valid for a real
	// operation.
	RiskClassUnspecified RiskClass = iota
	// RiskClassReadOnly is inherently unable to mutate anything the
	// plugin controls. MUST be used for ToolKindDataSource and
	// ToolKindInteractive alike.
	RiskClassReadOnly
	// RiskClassLow is a resource operation with narrow, easily-reversible
	// blast radius, e.g. a write to a scratch path.
	RiskClassLow
	// RiskClassModerate is a resource operation with real but bounded
	// blast radius, e.g. editing a tracked source file.
	RiskClassModerate
	// RiskClassHigh is a resource operation with broad or
	// hard-to-predict blast radius, e.g. arbitrary shell execution.
	RiskClassHigh
	// RiskClassCritical is a resource operation capable of irreversible
	// or wide-blast-radius action, e.g. `rm -rf`, a force-push, or
	// spawning a sub-agent with further unattended write access.
	RiskClassCritical
)

// String returns r's wire-name-derived lowercase form, e.g. "read_only".
func (r RiskClass) String() string {
	switch r {
	case RiskClassUnspecified:
		return "unspecified"
	case RiskClassReadOnly:
		return "read_only"
	case RiskClassLow:
		return "low"
	case RiskClassModerate:
		return "moderate"
	case RiskClassHigh:
		return "high"
	case RiskClassCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// ConcurrencySpec declares whether this operation's Invoke calls may run
// concurrently against the same provider process, per
// docs/specifications/tool/data-types.md#concurrencyspec. One of the six
// types pkg/slashcommand reuses verbatim; see doc.go.
type ConcurrencySpec struct {
	// Safe is MUST-set for every operation except ToolKindInteractive.
	// false (the zero value) means the kernel MUST NOT run any other
	// Invoke call against this provider process concurrently with this
	// one — a coarse, provider-wide lock. true means concurrent Invoke
	// calls against this provider are generally safe.
	Safe bool
	// KeyFields is MAY, only meaningful when Safe is true. Names of this
	// operation's input_schema fields whose value(s) form a
	// serialization key; the kernel serializes calls sharing an
	// identical key while freely parallelizing calls with distinct keys.
	// Omitting KeyFields under Safe == true asserts that no two calls to
	// this operation can ever conflict — a strong claim, true for e.g.
	// web_search, false for e.g. write_file.
	KeyFields []string
}

// OutputStream distinguishes which underlying stream an output chunk came
// from. One of the six types pkg/slashcommand reuses verbatim; see doc.go.
type OutputStream int

const (
	// OutputStreamUnspecified is the zero value. Never valid for a real
	// chunk.
	OutputStreamUnspecified OutputStream = iota
	// OutputStreamStdout is standard output.
	OutputStreamStdout
	// OutputStreamStderr is standard error.
	OutputStreamStderr
)

// String returns s's wire-name-derived lowercase form, e.g. "stdout".
func (s OutputStream) String() string {
	switch s {
	case OutputStreamUnspecified:
		return "unspecified"
	case OutputStreamStdout:
		return "stdout"
	case OutputStreamStderr:
		return "stderr"
	default:
		return "unknown"
	}
}

// ToolResult is the terminal, successful outcome of an Invoke call, per
// docs/specifications/tool/data-types.md#toolcall--toolevent--toolresult.
// Payload MUST conform to the operation's declared ToolSchema.OutputSchema
// — the kernel validates this strictly and rejects a non-conforming
// payload rather than passing it through to history. One of the six types
// pkg/slashcommand reuses verbatim; see doc.go. Deliberately holds nothing
// ToolCall-specific (no call ID, no tool name) so it reuses cleanly for a
// slash command's own direct-invoke result.
type ToolResult struct {
	// Payload is the already-decoded JSON result payload.
	Payload map[string]any
}

// ToolSchema declares one operation a Provider exposes, per
// docs/specifications/tool/protocol.md#getschema.
type ToolSchema struct {
	// Name MUST be unique within this provider's namespace, e.g.
	// "read_file".
	Name string
	// Kind MUST be set — drives the plan/apply gate.
	Kind ToolKind
	// Risk MUST be set — see RiskClass. MUST be RiskClassReadOnly for
	// ToolKindDataSource and ToolKindInteractive alike; MUST be one of
	// low/moderate/high/critical for ToolKindResource.
	Risk RiskClass
	// Description MUST be set — shown to the model for tool selection
	// and in plan diffs.
	Description string
	// InputSchema MUST be set — the common JSON-Schema subset (built with
	// pkg/schema) describing ToolCall.Arguments's shape for this
	// operation.
	InputSchema *schemav1.Schema
	// OutputSchema MUST be set — the common JSON-Schema subset describing
	// ToolResult.Payload's shape for this operation.
	OutputSchema *schemav1.Schema
	// Streaming MUST be set — true if Invoke may emit intermediate
	// events (output_chunk, progress, partial_result) before the
	// terminal event; false if Invoke always emits exactly one terminal
	// event with no lead-up.
	Streaming bool
	// Concurrency MUST be set for every kind except ToolKindInteractive,
	// for which it MUST be nil — see ConcurrencySpec.
	Concurrency *ConcurrencySpec
	// DefaultTimeout SHOULD be set — the deadline the kernel applies to
	// Invoke for this operation absent an agent.hcl override. The zero
	// value means unset: the kernel's own global default applies
	// instead.
	DefaultTimeout time.Duration
	// Idempotent is true iff re-running this operation with identical
	// arguments cannot produce a different end state than running it
	// once. Gates whether the kernel MAY auto-retry a retryable
	// ToolError for a ToolKindResource operation —
	// docs/specifications/tool/conformance.md's retry interaction.
	// ToolKindDataSource operations are implicitly safe to retry
	// regardless of this field.
	Idempotent bool
}

// ToolCall is one request to execute an operation, per
// docs/specifications/tool/data-types.md#toolcall--toolevent--toolresult.
type ToolCall struct {
	// ID is kernel-assigned; echoed in every ToolEvent for this call.
	ID string
	// ToolName matches a ToolSchema.Name from this provider's Schema.
	ToolName string
	// Arguments is already-parsed JSON conforming to that operation's
	// InputSchema.
	Arguments map[string]any
	// CallContext is always set by the kernel. Its WorkingDirectory is
	// the cwd a process-backed operation (exec/bash, read_file, and
	// similarly-shaped tools) MUST resolve any relative-path argument
	// against — without it, those tools have no defined cwd and are
	// unusable. Its SessionId/TurnId are what a Provider echoes back on
	// its own kernel-callback Emit/Log calls for correlation. See
	// docs/specifications/tool/protocol.md#invoke.
	CallContext *commonv1.CallContext
}

// OutputChunkEvent carries one slice of raw stdout/stderr-shaped output
// from a process-backed operation.
type OutputChunkEvent struct {
	Stream OutputStream
	Data   []byte
}

// ProgressEvent carries a human-readable status update for a long-running
// call.
type ProgressEvent struct {
	Message string
	// FractionComplete is how far through the operation this call is, in
	// [0.0, 1.0]. nil means the provider cannot estimate completion
	// fraction.
	FractionComplete *float64
}

// PartialResultEvent carries incremental structured output before the
// terminal result, e.g. search hits as they're found.
type PartialResultEvent struct {
	Payload map[string]any
}

// ExitStatusEvent carries a process-backed operation's child process exit
// information. exec-family tools only — a Provider for a non-process-
// backed tool (file read, grep, web fetch) MUST NOT emit this. At most one
// per Invoke stream.
type ExitStatusEvent struct {
	ExitCode int32
	// Signal is the signal that terminated the child process, if any.
	// nil means the process exited normally.
	Signal *string
}

// ToolEvent is one message a Provider's Invoke sends via *Stream, per
// docs/specifications/tool/data-types.md#toolcall--toolevent--toolresult.
// Exactly one field is set; construct one with NewOutputChunkEvent,
// NewProgressEvent, NewPartialResultEvent, NewExitStatusEvent,
// NewResultEvent, or NewErrorEvent rather than a struct literal — see
// stream.go for the ordering, cardinality, and terminal-event contract
// *Stream.Send enforces.
type ToolEvent struct {
	OutputChunk   *OutputChunkEvent
	Progress      *ProgressEvent
	PartialResult *PartialResultEvent
	ExitStatus    *ExitStatusEvent
	Result        *ToolResult
	Error         *ToolError
}

// NewOutputChunkEvent builds a ToolEvent carrying one output chunk.
func NewOutputChunkEvent(stream OutputStream, data []byte) *ToolEvent {
	return &ToolEvent{OutputChunk: &OutputChunkEvent{Stream: stream, Data: data}}
}

// NewProgressEvent builds a ToolEvent carrying a progress update.
// fractionComplete may be nil.
func NewProgressEvent(message string, fractionComplete *float64) *ToolEvent {
	return &ToolEvent{Progress: &ProgressEvent{Message: message, FractionComplete: fractionComplete}}
}

// NewPartialResultEvent builds a ToolEvent carrying incremental structured
// output.
func NewPartialResultEvent(payload map[string]any) *ToolEvent {
	return &ToolEvent{PartialResult: &PartialResultEvent{Payload: payload}}
}

// NewExitStatusEvent builds a ToolEvent carrying a child process's exit
// status. signal may be nil.
func NewExitStatusEvent(exitCode int32, signal *string) *ToolEvent {
	return &ToolEvent{ExitStatus: &ExitStatusEvent{ExitCode: exitCode, Signal: signal}}
}

// NewResultEvent builds a ToolEvent carrying the terminal, successful
// result.
func NewResultEvent(payload map[string]any) *ToolEvent {
	return &ToolEvent{Result: &ToolResult{Payload: payload}}
}

// NewErrorEvent builds a ToolEvent carrying the terminal, failed result.
func NewErrorEvent(err *ToolError) *ToolEvent {
	return &ToolEvent{Error: err}
}

// Provider is the interface a tool plugin author implements; NewService
// adapts it onto the generated toolv1.ToolServiceServer.
type Provider interface {
	// Schema returns the ToolSchema for every operation this plugin
	// exposes, per docs/specifications/tool/protocol.md#getschema. MUST
	// be cheaply re-queryable and MUST NOT make a network call.
	Schema(ctx context.Context) ([]*ToolSchema, error)
	// Configure decodes and validates this provider's agent.hcl block,
	// already decoded from JSON into config. MUST reject with an error
	// on a missing required field rather than deferring failure to the
	// first Invoke. A returned *ToolError is surfaced with its own
	// category/message; any other error defaults to
	// ToolErrorCategoryInvalidArguments.
	Configure(ctx context.Context, config map[string]any) error
	// Invoke executes call, sending zero or more non-terminal events and
	// exactly one terminal event (built with NewResultEvent or
	// NewErrorEvent) via stream before returning. Returning a nil error
	// without having sent a terminal event is a Provider bug the adapter
	// surfaces as a failed RPC. See stream.go for the full contract.
	Invoke(ctx context.Context, call *ToolCall, stream *Stream) error
}

// Renderer is an optional interface a Provider MAY additionally implement
// to render a previously-emitted opaque payload as a RenderTree, per
// docs/specifications/tool/protocol.md#render. If a Provider does not
// implement Renderer, the kernel falls back to its generic default
// (pretty-printed JSON payload).
type Renderer interface {
	Render(ctx context.Context, payload []byte, schemaVersion string) (*renderv1.RenderTree, error)
}

// Previewer is an optional interface a Provider MAY additionally implement
// to describe, without executing, what Invoke(call) would do, per
// docs/specifications/tool/protocol.md#preview. Producing a preview MUST
// NOT mutate anything and MUST be side-effect-free; a Provider unable to
// satisfy that for a given operation MUST NOT implement Previewer for it.
// If a Provider does not implement Previewer, a kernel falls back to
// showing the call's raw arguments in the plan/apply gate's permission UI.
type Previewer interface {
	Preview(ctx context.Context, call *ToolCall) (*renderv1.RenderTree, error)
}

// ConfigSchemaProvider is an optional interface a Provider MAY implement
// to advertise the ConfigSchema (built with pkg/config) the kernel decodes
// its agent.hcl provider block against before ever calling Configure. A
// Provider that takes no configuration simply does not implement this
// interface.
type ConfigSchemaProvider interface {
	ConfigSchema() (*configv1.ConfigSchema, error)
}

// SlashCommandProvider is an optional interface a Provider MAY implement
// to contribute prompt-expansion slash commands to its GetSchema response,
// per docs/specifications/tool/protocol.md#getschema.
type SlashCommandProvider interface {
	SlashCommands() []*commonv1.PromptExpansionSpec
}

// HookPointProvider is an optional interface a Provider MAY implement to
// advertise which of the eight dispatchable hook points its
// HookSubscriberService subscribes to, per
// docs/specifications/tool/protocol.md#getschema.
type HookPointProvider interface {
	SupportedHookPoints() []commonv1.HookPoint
}
