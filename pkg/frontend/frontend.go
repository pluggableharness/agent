package frontend

import (
	"context"

	"google.golang.org/protobuf/types/known/structpb"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	frontendv1 "github.com/pluggableharness/agent/pkg/frontend/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
	planv1 "github.com/pluggableharness/agent/pkg/plan/proto/v1"
	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
	sessionv1 "github.com/pluggableharness/agent/pkg/session/proto/v1"
	slashcommandv1 "github.com/pluggableharness/agent/pkg/slashcommand/proto/v1"
)

// Capabilities is this frontend's static self-description, returned by
// GetCapabilities (frontend-protocol.md's "Transport" section). It MUST be
// cheaply re-derivable and MUST NOT require a network call — Provider.
// Capabilities should build it from data already resident in the plugin
// process, never fetch it remotely.
type Capabilities struct {
	// SlashCommands are the prompt-expansion commands this frontend
	// itself contributes. A direct-invoke command is declared exclusively
	// by a slashcommand.v1 provider instead, never here.
	SlashCommands []*commonv1.PromptExpansionSpec
	// ConfigSchema is this provider's agent.hcl configuration schema. See
	// NewCapabilities and github.com/pluggableharness/agent/pkg/config's
	// Schema/Attribute builders.
	ConfigSchema *configv1.ConfigSchema
	// SupportedRegions are the Regions this frontend proactively declares
	// it can render into — a complement to, not a replacement for, the
	// reactive FRONTEND_ERROR_CATEGORY_REGION_UNSUPPORTED error a
	// placement it can't honor still produces.
	SupportedRegions []renderv1.Region
	// SupportedHookPoints are the hook points this frontend can subscribe
	// to, so a mis-declared agent.hcl hook{} block naming an unsupported
	// point is rejected at config-load time.
	SupportedHookPoints []commonv1.HookPoint
}

// Provider is the interface a frontend plugin author implements. NewService
// adapts a Provider into the generated frontendv1.FrontendServiceServer.
type Provider interface {
	// Capabilities returns this frontend's static self-description. MUST
	// be cheaply re-queryable and MUST NOT require a network call.
	Capabilities(ctx context.Context) (*Capabilities, error)
	// Configure applies this provider's agent.hcl configuration, already
	// validated by the kernel against the ConfigSchema Capabilities
	// returned. A returned *Error becomes the structured detail
	// of the resulting gRPC status (see Error.StatusErr); any
	// other error is wrapped as FRONTEND_ERROR_CATEGORY_UNKNOWN.
	Configure(ctx context.Context, config *structpb.Struct) error
	// HandleEvent is invoked once per ClientEvent this connection's
	// Attach adapter receives, in arrival order, on the connection's
	// single dispatch goroutine (doc.go's "Attach is one stream per
	// connection, not one per session"). Implementations reply through
	// emit, which is valid for the remainder of the connection's
	// lifetime — including from another goroutine, after this call
	// returns, e.g. to push an unsolicited render triggered by activity
	// elsewhere in the plugin process — not just while HandleEvent is
	// executing.
	//
	// A returned error surfaces in-band as ServerEvent.error and keeps
	// the stream open — the path this method should reach for by default.
	// Wrap it with Fatal only for a genuinely fatal condition that must
	// close the stream with a gRPC status (doc.go's "Error handling is
	// two distinct paths, not one").
	HandleEvent(ctx context.Context, event ClientEvent, emit Emitter) error
}

// Emitter sends one ServerEvent at a time to the kernel over the Attach
// connection that produced the ClientEvent currently (or most recently)
// being handled. Safe for concurrent use.
type Emitter interface {
	// Emit sends event. Returns a non-nil error only when the underlying
	// stream itself has failed — never for an application-level condition,
	// which a Provider instead reports by returning an error from
	// HandleEvent (or by constructing its own ErrorEvent payload and
	// calling Emit directly, for finer control over SessionID/RequestID).
	Emit(event ServerEvent) error
}

// ClientEvent is the domain form of frontendv1.ClientEvent — one event this
// connection's Attach adapter received. SessionID is REQUIRED (non-empty)
// for every session-scoped Payload variant (UserMessage, SlashCommand,
// PlanDecision, InteractiveResponse, ActionTrigger, Interrupt) and empty
// for the six connection-level control variants (Hello, CreateSession,
// AttachSession, ResumeSession, DetachSession, ListSessions), which either
// have no session yet or operate across sessions.
type ClientEvent struct {
	SessionID string
	Payload   ClientEventPayload
}

// ClientEventPayload is the oneof of every ClientEvent variant. Exactly
// one concrete type — UserMessage, SlashCommand, PlanDecision,
// InteractiveResponse, ActionTrigger, Interrupt, Hello, CreateSession,
// AttachSession, ResumeSession, DetachSession, or ListSessions — is ever
// assigned to ClientEvent.Payload.
type ClientEventPayload interface {
	isClientEventPayload()
}

// UserMessage is ordinary chat input. Content MUST contain at least one
// block; see github.com/pluggableharness/agent/pkg/content's Text, Image,
// Document, and other builders — never a bare string (doc.go's
// "ContentBlocks, not a bare string").
type UserMessage struct {
	Content []*contentv1.ContentBlock
}

func (UserMessage) isClientEventPayload() {}

// SlashCommand is a dispatched slash command invocation, resolved by the
// frontend against the kernel-supplied SlashCommandRegistry (see
// SlashCommandRegistry below) before being sent — resolution itself is
// author-side logic this package does not implement.
type SlashCommand struct {
	// Name is the command name, without its leading slash.
	Name string
	// Args is the raw argument string following the command name.
	Args string
}

func (SlashCommand) isClientEventPayload() {}

// PlanScope is the domain form of frontendv1.PlanDecisionScope, reordered
// so its Go zero value is PlanScopeOnce — the default a frontend SHOULD
// send absent explicit operator intent (frontend-protocol.md's
// "plan_decision.corrected_input" section) — rather than the generated
// enum's own zero value, PLAN_DECISION_SCOPE_UNSPECIFIED, which is never a
// valid decision.
type PlanScope int32

const (
	// PlanScopeOnce applies the decision to the named plan item only. The
	// zero value, and the spec-mandated default.
	PlanScopeOnce PlanScope = iota
	// PlanScopeSession applies the decision to the rest of the current
	// session for matching calls.
	PlanScopeSession
	// PlanScopeAlways asks the kernel to persist the decision as policy,
	// outliving the session. The kernel MUST reject this distinctly if it
	// cannot persist policy, never silently downgrading it.
	PlanScopeAlways
)

// String returns a human-readable name for s, for logging.
func (s PlanScope) String() string {
	switch s {
	case PlanScopeOnce:
		return "once"
	case PlanScopeSession:
		return "session"
	case PlanScopeAlways:
		return "always"
	default:
		return "unknown"
	}
}

// PlanDecision resolves a pending PermissionRequest (see PermissionRequest
// below). CorrectedInput, when present, is an opencode-style corrected-
// argument redirect the kernel MUST re-validate against the tool's
// input_schema before treating the item as allowed.
type PlanDecision struct {
	PlanItemID string
	Decision   frontendv1.ClientDecision
	// CorrectedInput, when non-nil, replaces the plan item's tool input
	// rather than a plain allow/deny.
	CorrectedInput *structpb.Struct
	// Scope says how durably this decision applies beyond the named item.
	// The zero value is PlanScopeOnce.
	Scope PlanScope
}

func (PlanDecision) isClientEventPayload() {}

// InteractiveResponse resolves a pending InteractiveRequest (see
// InteractiveRequest below), correlated by CallID.
type InteractiveResponse struct {
	CallID   string
	Response *structpb.Struct
}

func (InteractiveResponse) isClientEventPayload() {}

// ActionTrigger is what a frontend dispatches when the operator activates
// a RenderTree's ActionNode. NodeID, ToolName, Args, and Provider MUST be
// echoed unchanged from the originating ActionNode — never rewritten
// (render-tree.md#interactive-content-the-action-node) — this is
// author-side UI discipline this package documents but cannot enforce.
type ActionTrigger struct {
	NodeID   string
	ToolName string
	Args     *structpb.Struct
	// Provider is the declared name of the tool provider plugin ToolName
	// belongs to — ToolName is only unique per provider.
	Provider string
}

func (ActionTrigger) isClientEventPayload() {}

// Interrupt signals that the operator wants to interrupt the current turn.
// It carries no fields.
type Interrupt struct{}

func (Interrupt) isClientEventPayload() {}

// Hello MAY be sent first on a newly opened Attach connection, asserting
// only the protocol version — it does not bind any session.
type Hello struct {
	ProtocolVersion uint32
}

func (Hello) isClientEventPayload() {}

// CreateSession creates a new session under Profile (or the kernel's
// default profile, when nil) and auto-attaches this connection to it.
// Answered by SessionCreated, correlated by RequestID.
type CreateSession struct {
	RequestID string
	// Profile is the agent.hcl profile to create the session under. Nil
	// means the kernel's configured default profile.
	Profile *string
	// InitialPrompt, when non-nil, seeds the session's first turn.
	InitialPrompt *string
	// WorkingDirectory, when non-nil, overrides the kernel's own working
	// directory at creation time.
	WorkingDirectory *string
}

func (CreateSession) isClientEventPayload() {}

// AttachSession subscribes an existing (live or terminal) session onto
// this connection, triggering a backfill replay. Answered by
// SessionAttached, bracketing a batch closed by BackfillComplete.
type AttachSession struct {
	RequestID string
	SessionID string
}

func (AttachSession) isClientEventPayload() {}

// ResumeSession attaches a historical session for continuation or replay —
// see frontend-protocol.md's "Resume and re-open semantics". Answered
// identically to AttachSession.
type ResumeSession struct {
	RequestID string
	SessionID string
}

func (ResumeSession) isClientEventPayload() {}

// DetachSession unsubscribes a session from this connection without
// affecting the session itself or any other connection attached to it.
// Answered by SessionDetached.
type DetachSession struct {
	RequestID string
	SessionID string
}

func (DetachSession) isClientEventPayload() {}

// ListSessions requests the connection-scoped session summary list.
// Answered by SessionList. There is no DeleteSession — see
// frontend-protocol.md's "No session deletion".
type ListSessions struct {
	RequestID string
	// Status, when non-nil, restricts the result to sessions in this
	// status.
	Status *sessionv1.SessionStatus
	// ParentSessionID, when non-nil, restricts the result to children of
	// this session.
	ParentSessionID *string
	// RootsOnly restricts the result to sessions with no
	// parent_session_id, ignored when false.
	RootsOnly bool
}

func (ListSessions) isClientEventPayload() {}

// ServerEvent is the domain form of frontendv1.ServerEvent — one event
// this connection's Attach adapter sends. SessionID is set for every
// session-scoped Payload variant; empty only for the one connection-level
// variant, SessionList. RequestID, when non-nil, correlates this event
// back to the ClientEvent control message that triggered it (set on
// SessionCreated, SessionAttached, BackfillComplete, SessionDetached,
// SessionList, and on ErrorEvent when it answers a specific control
// request; nil for an ordinary live session event not triggered by a
// specific request).
type ServerEvent struct {
	SessionID string
	RequestID *string
	Payload   ServerEventPayload
}

// ServerEventPayload is the oneof of every ServerEvent variant.
type ServerEventPayload interface {
	isServerEventPayload()
}

// StreamDelta is the fast path for incremental text display, skipping a
// full Render round trip — live-only, never used for replayed/backfilled
// text (doc.go's "Fast path vs. full render"). TargetID correlates
// consecutive deltas into one growing piece of text.
type StreamDelta struct {
	TargetID string
	Text     string
}

func (StreamDelta) isServerEventPayload() {}

// Render carries one placed RenderTree to paint — a finished unit, live or
// replayed, never a partial delta (doc.go's "Fast path vs. full render").
// A frontend MUST render every RenderNode type gracefully, including a
// variant added after this package shipped — see FallbackText.
type Render struct {
	Content *renderv1.PlacedContent
}

func (Render) isServerEventPayload() {}

// PermissionRequest asks the operator to resolve a pending plan-apply-gate
// "ask" decision: the kernel blocks that plan item's apply until a
// matching PlanDecision resolves it.
type PermissionRequest struct {
	PlanItem *planv1.PlanItem
}

func (PermissionRequest) isServerEventPayload() {}

// PlanReady announces a complete plan for display, e.g. before execution
// begins or after a replan.
type PlanReady struct {
	Plan *planv1.Plan
}

func (PlanReady) isServerEventPayload() {}

// InteractiveRequest carries a kind:interactive tool call's own prompt
// content across the frontend boundary, correlated by CallID with the
// eventual InteractiveResponse. A conforming frontend MUST render Prompt
// in the REGION_OVERLAY region, the same visual treatment as an ordinary
// "ask" prompt (render-tree.md#placement--regions) — author-side UI
// discipline this package documents but cannot enforce.
type InteractiveRequest struct {
	CallID   string
	ToolName string
	Prompt   *renderv1.RenderTree
}

func (InteractiveRequest) isServerEventPayload() {}

// SessionTreeUpdate reports a CHILD session's status change (e.g. a
// RunSession-spawned sub-agent), so a frontend can keep a SubSessionNode's
// displayed status current. For the attached session's OWN status, see
// SessionStatusUpdate below, a deliberately distinct variant.
type SessionTreeUpdate struct {
	ParentSessionID string
	ChildSessionID  string
	Status          sessionv1.SessionStatus
}

func (SessionTreeUpdate) isServerEventPayload() {}

// ErrorEvent carries a structured, non-fatal Error for display —
// the in-band error path (doc.go's "Error handling is two distinct paths,
// not one").
type ErrorEvent struct {
	Err *Error
}

func (ErrorEvent) isServerEventPayload() {}

// SessionCreated acknowledges a successful CreateSession, carrying the new
// session's info.
type SessionCreated struct {
	Info *sessionv1.SessionInfo
}

func (SessionCreated) isServerEventPayload() {}

// SessionAttached acknowledges a successful AttachSession or
// ResumeSession, carrying the session's current info and opening its
// backfill batch — the replayed Render events that follow, bracketed by
// the eventual BackfillComplete.
type SessionAttached struct {
	Info *sessionv1.SessionInfo
}

func (SessionAttached) isServerEventPayload() {}

// BackfillComplete is the done-marker closing a backfill batch opened by
// SessionAttached. Live events with sequence > LastSequence follow. Per
// frontend-protocol.md's "Backfill" section, a backfill batch is unicast
// to the attaching connection only, never broadcast to any other frontend
// subscribed to the same session.
type BackfillComplete struct {
	LastSequence int64
}

func (BackfillComplete) isServerEventPayload() {}

// SessionDetached acknowledges a successful DetachSession. It carries no
// fields.
type SessionDetached struct{}

func (SessionDetached) isServerEventPayload() {}

// SessionList answers a ListSessions request, most-recently-started first.
// This is the one connection-level ServerEvent variant — ServerEvent's own
// SessionID is empty for it.
type SessionList struct {
	Sessions []*sessionv1.SessionInfo
}

func (SessionList) isServerEventPayload() {}

// SlashCommandRegistry is the profile-scoped aggregate of every loaded
// provider's declared slash commands, sent on session attach and again
// whenever the registry changes. DirectInvokeCommands is declared
// exclusively by slashcommand.v1 providers; PromptExpansionCommands is
// shared vocabulary any category MAY declare. A command name MUST be
// unique jointly across both lists — the kernel enforces this at
// config-load time, not this package.
type SlashCommandRegistry struct {
	DirectInvokeCommands    []*slashcommandv1.SlashCommandSpec
	PromptExpansionCommands []*commonv1.PromptExpansionSpec
}

func (SlashCommandRegistry) isServerEventPayload() {}

// UsageUpdate carries one turn's token/cost accounting and the session's
// running totals, for a context-budget indicator or similar.
type UsageUpdate struct {
	Turn              *modelv1.Usage
	CumulativeCostUSD float64
	UsedTokens        int64
	EffectiveCeiling  int64
}

func (UsageUpdate) isServerEventPayload() {}

// SessionStatusUpdate reports the attached session's OWN lifecycle status
// transition (e.g. RUNNING -> COMPLETED, or a bound-exhausted re-open).
// Deliberately distinct from SessionTreeUpdate above, which reports a
// CHILD session's status.
type SessionStatusUpdate struct {
	Status sessionv1.SessionStatus
}

func (SessionStatusUpdate) isServerEventPayload() {}
