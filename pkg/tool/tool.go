package tool

import (
	"context"
	"time"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
	schemav1 "github.com/pluggableharness/agent/pkg/schema/proto/v1"
)

// Kind classifies whether an operation is gated behind the plan/apply
// approval gate, executes freely, or blocks the current turn for human
// input — see docs/specifications/tool/protocol.md#getschema and
// docs/specifications/tool/protocol.md#kind-interactive. One of the six
// types pkg/slashcommand reuses verbatim; see doc.go.
type Kind int

const (
	// KindUnspecified is the zero value. Never valid for a real
	// operation — its presence means an author forgot to set Kind.
	KindUnspecified Kind = iota
	// KindResource is a mutating operation, gated behind the
	// plan/apply approval gate.
	KindResource
	// KindDataSource is a read-only operation. Executes freely,
	// subject only to the policy precheck.
	KindDataSource
	// KindInteractive blocks the current turn for human input and
	// produces no state mutation of its own — the human's answer becomes
	// the result.
	KindInteractive
)

// String returns k's wire-name-derived lowercase form, e.g. "data_source".
func (k Kind) String() string {
	switch k {
	case KindUnspecified:
		return "unspecified"
	case KindResource:
		return "resource"
	case KindDataSource:
		return "data_source"
	case KindInteractive:
		return "interactive"
	default:
		return "unknown"
	}
}

// RiskClass classifies an operation's blast radius, orthogonal to
// Kind: kind determines whether the plan/apply gate applies at all,
// risk determines how significant the gated (or inherently ungated)
// action is — see docs/specifications/tool/data-types.md#riskclass. One
// of the six types pkg/slashcommand reuses verbatim; see doc.go.
type RiskClass int

const (
	// RiskClassUnspecified is the zero value. Never valid for a real
	// operation.
	RiskClassUnspecified RiskClass = iota
	// RiskClassReadOnly is inherently unable to mutate anything the
	// plugin controls. MUST be used for KindDataSource and
	// KindInteractive alike.
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
	// Safe is MUST-set for every operation except KindInteractive.
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

// Result is the terminal, successful outcome of an Invoke call, per
// docs/specifications/tool/data-types.md#toolcall--toolevent--toolresult.
// Payload MUST conform to the operation's declared Schema.OutputSchema
// — the kernel validates this strictly and rejects a non-conforming
// payload rather than passing it through to history. One of the six types
// pkg/slashcommand reuses verbatim; see doc.go. Deliberately holds nothing
// Call-specific (no call ID, no tool name) so it reuses cleanly for a
// slash command's own direct-invoke result.
type Result struct {
	// Payload is the already-decoded JSON result payload.
	Payload map[string]any
}

// Schema declares one operation a Provider exposes, per
// docs/specifications/tool/protocol.md#getschema.
type Schema struct {
	// Name MUST be unique within this provider's namespace, e.g.
	// "read_file".
	Name string
	// Kind MUST be set — drives the plan/apply gate.
	Kind Kind
	// Risk MUST be set — see RiskClass. MUST be RiskClassReadOnly for
	// KindDataSource and KindInteractive alike; MUST be one of
	// low/moderate/high/critical for KindResource.
	Risk RiskClass
	// Description MUST be set — shown to the model for tool selection
	// and in plan diffs.
	Description string
	// InputSchema MUST be set — the common JSON-Schema subset (built with
	// pkg/schema) describing Call.Arguments's shape for this
	// operation.
	InputSchema *schemav1.Schema
	// OutputSchema MUST be set — the common JSON-Schema subset describing
	// Result.Payload's shape for this operation.
	OutputSchema *schemav1.Schema
	// Streaming MUST be set — true if Invoke may emit intermediate
	// events (output_chunk, progress, partial_result) before the
	// terminal event; false if Invoke always emits exactly one terminal
	// event with no lead-up.
	Streaming bool
	// Concurrency MUST be set for every kind except KindInteractive,
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
	// Error for a KindResource operation —
	// docs/specifications/tool/conformance.md's retry interaction.
	// KindDataSource operations are implicitly safe to retry
	// regardless of this field.
	Idempotent bool
}

// Call is one request to execute an operation, per
// docs/specifications/tool/data-types.md#toolcall--toolevent--toolresult.
type Call struct {
	// ID is kernel-assigned; echoed in every Event for this call.
	ID string
	// ToolName matches a Schema.Name from this provider's Schema.
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

// Event is one message a Provider's Invoke sends via *Stream, per
// docs/specifications/tool/data-types.md#toolcall--toolevent--toolresult.
// Exactly one field is set; construct one with NewOutputChunkEvent,
// NewProgressEvent, NewPartialResultEvent, NewExitStatusEvent,
// NewResultEvent, or NewErrorEvent rather than a struct literal — see
// stream.go for the ordering, cardinality, and terminal-event contract
// *Stream.Send enforces.
type Event struct {
	OutputChunk   *OutputChunkEvent
	Progress      *ProgressEvent
	PartialResult *PartialResultEvent
	ExitStatus    *ExitStatusEvent
	Result        *Result
	Error         *Error
}

// NewOutputChunkEvent builds a Event carrying one output chunk.
func NewOutputChunkEvent(stream OutputStream, data []byte) *Event {
	return &Event{OutputChunk: &OutputChunkEvent{Stream: stream, Data: data}}
}

// NewProgressEvent builds a Event carrying a progress update.
// fractionComplete may be nil.
func NewProgressEvent(message string, fractionComplete *float64) *Event {
	return &Event{Progress: &ProgressEvent{Message: message, FractionComplete: fractionComplete}}
}

// NewPartialResultEvent builds a Event carrying incremental structured
// output.
func NewPartialResultEvent(payload map[string]any) *Event {
	return &Event{PartialResult: &PartialResultEvent{Payload: payload}}
}

// NewExitStatusEvent builds a Event carrying a child process's exit
// status. signal may be nil.
func NewExitStatusEvent(exitCode int32, signal *string) *Event {
	return &Event{ExitStatus: &ExitStatusEvent{ExitCode: exitCode, Signal: signal}}
}

// NewResultEvent builds a Event carrying the terminal, successful
// result.
func NewResultEvent(payload map[string]any) *Event {
	return &Event{Result: &Result{Payload: payload}}
}

// NewErrorEvent builds a Event carrying the terminal, failed result.
func NewErrorEvent(err *Error) *Event {
	return &Event{Error: err}
}

// Provider is the interface a tool plugin author implements; NewService
// adapts it onto the generated toolv1.ToolServiceServer.
type Provider interface {
	// Schema returns the Schema for every operation this plugin
	// exposes, per docs/specifications/tool/protocol.md#getschema. MUST
	// be cheaply re-queryable and MUST NOT make a network call.
	Schema(ctx context.Context) ([]*Schema, error)
	// Configure decodes and validates this provider's agent.hcl block,
	// already decoded from JSON into config. MUST reject with an error
	// on a missing required field rather than deferring failure to the
	// first Invoke. A returned *Error is surfaced with its own
	// category/message; any other error defaults to
	// ErrorCategoryInvalidArguments.
	Configure(ctx context.Context, config map[string]any) error
	// Invoke executes call, sending zero or more non-terminal events and
	// exactly one terminal event (built with NewResultEvent or
	// NewErrorEvent) via stream before returning. Returning a nil error
	// without having sent a terminal event is a Provider bug the adapter
	// surfaces as a failed RPC. See stream.go for the full contract.
	Invoke(ctx context.Context, call *Call, stream *Stream) error
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
	Preview(ctx context.Context, call *Call) (*renderv1.RenderTree, error)
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
