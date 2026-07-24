package slashcommand

import (
	"context"
	"time"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
	schemav1 "github.com/pluggableharness/agent/pkg/schema/proto/v1"
	"github.com/pluggableharness/agent/pkg/tool"
)

// Spec declares one directly-invocable command a provider exposes, per
// docs/specifications/slashcommand/data-types.md#slashcommandspec. Kind,
// Risk, and Concurrency are tool.Kind/tool.RiskClass/tool.ConcurrencySpec —
// reused verbatim from pkg/tool, never redeclared here; see doc.go. Unlike
// tool.Schema, Spec has no OutputSchema field at all: a direct-invoke
// command is never presented to the model as a callable tool — it
// dispatches without a model turn — so there is no LLM-facing
// structured-output contract to validate its result against.
type Spec struct {
	// Name MUST be set — the command's name, without the leading "/".
	// MUST be unique across every direct-invoke command declared by
	// every provider in the session; a name collision at config-load
	// time is a hard error the kernel enforces, not this package.
	Name string
	// Description MUST be set — shown in the frontend's hotkey_hints
	// region and wherever else the frontend surfaces available
	// commands.
	Description string
	// InputSchema MUST be set — the common JSON-Schema subset (built
	// with pkg/schema) describing Call.Arguments's shape for this
	// command.
	InputSchema *schemav1.Schema
	// Kind MUST be set — drives the plan/apply gate identically to
	// tool.Schema.Kind. Reused verbatim from pkg/tool.
	Kind tool.Kind
	// Risk MUST be set — see tool.RiskClass. MUST be
	// tool.RiskClassReadOnly for tool.KindDataSource/tool.KindInteractive;
	// MUST be one of low/moderate/high/critical for tool.KindResource.
	// Reused verbatim from pkg/tool.
	Risk tool.RiskClass
	// Concurrency MUST be set for every Kind except tool.KindInteractive,
	// for which it MUST be nil. Reused verbatim from pkg/tool.
	Concurrency *tool.ConcurrencySpec
	// Streaming MUST be set — true if Invoke may emit intermediate
	// events (output_chunk, progress, partial_result) before the
	// terminal event; false if Invoke always emits exactly one
	// terminal event with no lead-up.
	Streaming bool
	// DefaultTimeout SHOULD be set — the deadline the kernel applies to
	// Invoke for this command absent an agent.hcl override. The zero
	// value means unset: the kernel's own global default applies
	// instead.
	DefaultTimeout time.Duration
	// Idempotent MUST be set — true iff re-running this command with
	// identical arguments cannot produce a different end state than
	// running it once. Gates whether the kernel MAY auto-retry a
	// retryable tool.Error for a tool.KindResource command — see
	// docs/specifications/tool/conformance.md#the-idempotent--retry-interaction,
	// reused verbatim here per
	// docs/specifications/slashcommand/conformance.md.
	Idempotent bool
}

// Call is one request to execute a direct-invoke command, per
// docs/specifications/slashcommand/data-types.md#slashcommandcall--slashcommandevent.
// Distinct from tool.Call: a Call names a Spec.Name from this same
// provider's own Capabilities, never another provider's tool operation.
type Call struct {
	// ID is kernel-assigned; echoed in every Event for this call.
	ID string
	// Name matches a Spec.Name from this provider's Capabilities
	// response.
	Name string
	// Arguments is already-parsed JSON conforming to that Spec's
	// InputSchema.
	Arguments map[string]any
	// CallContext is always set by the kernel. Its WorkingDirectory is
	// the cwd this call MUST resolve any relative-path argument
	// against; its SessionId/TurnId are what a Provider echoes back on
	// its own kernel-callback Emit/Log calls for correlation. See
	// docs/specifications/slashcommand/protocol.md#invoke.
	CallContext *commonv1.CallContext
}

// OutputChunkEvent carries one slice of raw stdout/stderr-shaped output
// from a process-backed command. Stream is tool.OutputStream, reused
// verbatim from pkg/tool.
type OutputChunkEvent struct {
	Stream tool.OutputStream
	Data   []byte
}

// ProgressEvent carries a human-readable status update for a long-running
// call.
type ProgressEvent struct {
	Message string
	// FractionComplete is how far through the operation this call is,
	// in [0.0, 1.0]. nil means the provider cannot estimate completion
	// fraction.
	FractionComplete *float64
}

// PartialResultEvent carries incremental structured output before the
// terminal result, e.g. progress lines as emitted.
type PartialResultEvent struct {
	Payload map[string]any
}

// ExitStatusEvent carries a process-backed command's child process exit
// information. Process-backed commands only — a Provider for a
// non-process-backed command MUST NOT emit this. At most one per Invoke
// stream.
type ExitStatusEvent struct {
	ExitCode int32
	// Signal is the signal that terminated the child process, if any.
	// nil means the process exited normally.
	Signal *string
}

// Event is one message a Provider's Invoke sends via *Stream, per
// docs/specifications/slashcommand/data-types.md#slashcommandcall--slashcommandevent.
// Exactly one field is set; construct one with NewOutputChunkEvent,
// NewProgressEvent, NewPartialResultEvent, NewExitStatusEvent,
// NewResultEvent, or NewErrorEvent rather than a struct literal — see
// stream.go for the ordering, cardinality, and terminal-event contract
// *Stream.Send enforces. Result and Error are tool.Result/tool.Error,
// reused verbatim from pkg/tool per data-types.md#reused-toolv1-types —
// this type does not redeclare them.
type Event struct {
	OutputChunk   *OutputChunkEvent
	Progress      *ProgressEvent
	PartialResult *PartialResultEvent
	ExitStatus    *ExitStatusEvent
	Result        *tool.Result
	Error         *tool.Error
}

// NewOutputChunkEvent builds an Event carrying one output chunk.
func NewOutputChunkEvent(stream tool.OutputStream, data []byte) *Event {
	return &Event{OutputChunk: &OutputChunkEvent{Stream: stream, Data: data}}
}

// NewProgressEvent builds an Event carrying a progress update.
// fractionComplete may be nil.
func NewProgressEvent(message string, fractionComplete *float64) *Event {
	return &Event{Progress: &ProgressEvent{Message: message, FractionComplete: fractionComplete}}
}

// NewPartialResultEvent builds an Event carrying incremental structured
// output.
func NewPartialResultEvent(payload map[string]any) *Event {
	return &Event{PartialResult: &PartialResultEvent{Payload: payload}}
}

// NewExitStatusEvent builds an Event carrying a child process's exit
// status. signal may be nil.
func NewExitStatusEvent(exitCode int32, signal *string) *Event {
	return &Event{ExitStatus: &ExitStatusEvent{ExitCode: exitCode, Signal: signal}}
}

// NewResultEvent builds an Event carrying the terminal, successful result,
// wrapping payload in a tool.Result — reused verbatim from pkg/tool.
func NewResultEvent(payload map[string]any) *Event {
	return &Event{Result: &tool.Result{Payload: payload}}
}

// NewErrorEvent builds an Event carrying the terminal, failed result. err
// is a *tool.Error — reused verbatim from pkg/tool; construct one with
// tool.NewError.
func NewErrorEvent(err *tool.Error) *Event {
	return &Event{Error: err}
}

// Provider is the interface a slash-command plugin author implements;
// NewService adapts it onto the generated
// slashcommandv1.SlashCommandServiceServer.
type Provider interface {
	// Capabilities returns the Spec for every direct-invoke command this
	// plugin exposes, per
	// docs/specifications/slashcommand/protocol.md#getcapabilities. MUST
	// be cheaply re-queryable and MUST NOT make a network call.
	Capabilities(ctx context.Context) ([]*Spec, error)
	// Configure decodes and validates this provider's agent.hcl block,
	// already decoded from JSON into config. MUST reject with an error
	// on a missing required field rather than deferring failure to the
	// first Invoke. A returned *tool.Error is surfaced with its own
	// category/message; any other error defaults to
	// tool.ErrorCategoryInvalidArguments.
	Configure(ctx context.Context, config map[string]any) error
	// Invoke executes call, sending zero or more non-terminal events and
	// exactly one terminal event (built with NewResultEvent or
	// NewErrorEvent) via stream before returning. Returning a nil error
	// without having sent a terminal event is a Provider bug the
	// adapter surfaces as a failed RPC. See stream.go for the full
	// contract.
	Invoke(ctx context.Context, call *Call, stream *Stream) error
}

// Renderer is an optional interface a Provider MAY additionally implement
// to render a previously-emitted opaque payload as a RenderTree, per
// docs/specifications/slashcommand/protocol.md#render. If a Provider does
// not implement Renderer, the kernel falls back to its generic default
// (pretty-printed JSON payload).
type Renderer interface {
	Render(ctx context.Context, payload []byte, schemaVersion string) (*renderv1.RenderTree, error)
}

// Previewer is an optional interface a Provider MAY additionally implement
// to describe, without executing, what Invoke(call) would do, per
// docs/specifications/slashcommand/protocol.md#preview. Producing a
// preview MUST NOT mutate anything and MUST be side-effect-free; a
// Provider unable to satisfy that for a given command MUST NOT implement
// Previewer for it. If a Provider does not implement Previewer, a kernel
// falls back to showing the call's raw arguments in the plan/apply gate's
// permission UI.
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

// HookPointProvider is an optional interface a Provider MAY implement to
// advertise which of the eight dispatchable hook points its
// HookSubscriberService subscribes to, per
// docs/specifications/slashcommand/protocol.md#getcapabilities.
type HookPointProvider interface {
	SupportedHookPoints() []commonv1.HookPoint
}
