package turn

import (
	"context"
	"errors"
	"log/slog"
	"time"

	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	hookv1 "github.com/pluggableharness/agent/pkg/hook/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
	planv1 "github.com/pluggableharness/agent/pkg/plan/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"

	"github.com/pluggableharness/agent/internal/contextassembly"
	"github.com/pluggableharness/agent/internal/hookdispatch"
	"github.com/pluggableharness/agent/internal/modelcall"
	"github.com/pluggableharness/agent/internal/plangate"
	"github.com/pluggableharness/agent/internal/providercatalog"
	"github.com/pluggableharness/agent/internal/statebackend"
	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/noop"
	"github.com/pluggableharness/agent/internal/tooldispatch"
)

// HookDispatcher is the narrow view of hook dispatch this package consumes:
// one call that runs a whole chain and reports its outcome. The hook point
// is not a parameter — HookPayload is a oneof, and the variant set on it IS
// the point.
//
// It is declared here rather than satisfied by importing
// *hookdispatch.Dispatcher as a concrete field, for the same reason
// internal/plangate declares its own: the turn driver needs one method, and
// keeping it an interface is what lets the conformance test below run the
// whole 18-step sequence against hand-written fakes. hookdispatch.Outcome
// is used verbatim — a second Go representation of a dispatcher's result
// would be exactly the parallel type go-layout.md forbids.
//
// Two contract details this package relies on, both documented on
// hookdispatch.Dispatch: a veto subscriber that errors or times out fails
// CLOSED (surfacing as HOOK_DECISION_DENY with a nil error), and a returned
// error is a dispatcher-level failure rather than an implicit verdict —
// Outcome.Decision MUST NOT be read when err is non-nil.
type HookDispatcher interface {
	Dispatch(ctx context.Context, payload *hookv1.HookPayload) (hookdispatch.Outcome, error)
}

// ContextAssembler is the narrow view of internal/contextassembly this
// package consumes: step 1's whole provider chain in one call.
// *contextassembly.Assembler satisfies it as written.
type ContextAssembler interface {
	Assemble(ctx context.Context, providers []providercatalog.ContextHandle, history []*contentv1.Message, in contextassembly.TurnInputs) (contextassembly.Result, error)
}

// ModelCaller is the narrow view of internal/modelcall this package
// consumes: steps 3-4 (StreamCompletion plus accumulation) in one call.
// *modelcall.Caller satisfies it as written.
//
// Complete also persists the message and its cost ledger row internally,
// which is why this package dispatches post-model-response after it rather
// than before — see this package's CLAUDE.md for the full reasoning.
type ModelCaller interface {
	Complete(ctx context.Context, req modelcall.Request) (modelcall.Response, error)
}

// PlanGate is the narrow view of internal/plangate this package consumes:
// the precheck for step 9/9b, plan construction for step 10, the plan-ready
// decision for step 11, the denial blocks a denied item's model-visible
// tool_result carries, and the apply result for step 14. *plangate.Gate
// satisfies it as written.
type PlanGate interface {
	Build(ctx context.Context, req plangate.BuildRequest) (*planv1.Plan, error)
	Precheck(ctx context.Context, calls []plangate.PrecheckCall) []plangate.PrecheckResult
	Decide(ctx context.Context, plan *planv1.Plan) (plangate.Decisions, error)
	DenialBlocks(d plangate.Decisions) []*contentv1.ContentBlock
	Result(ctx context.Context, turnID string, d plangate.Decisions, out []plangate.ApplyOutcome) (*planv1.ApplyResult, error)
}

// ToolScheduler is the narrow view of internal/tooldispatch this package
// consumes: the concurrent path for step 9's data_source group and step
// 12's approved resource group — "one mechanism for both, not two separate
// rules" — plus the strictly sequential path step 9b's interactive group
// requires. *tooldispatch.Scheduler satisfies it as written.
type ToolScheduler interface {
	Execute(ctx context.Context, calls []tooldispatch.Call) ([]tooldispatch.Outcome, error)
	ExecuteInteractive(ctx context.Context, calls []tooldispatch.Call) ([]tooldispatch.Outcome, error)
}

// IDMinter mints the kernel-assigned identifiers a turn needs: the message
// id for the completion this turn produces, and one plan-item id per
// resource call. It is injected so a test can pin them; production wiring
// passes an IDMinter over statebackend.NewEventID, the house ULID scheme
// every other kernel-assigned id already uses (determinism.md forbids a
// second id scheme, and forbids a plugin ever assigning one).
type IDMinter interface {
	New() string
}

// Config is a Driver's collaborators. Hooks, Context, Model, Gate, Tools,
// and Catalog are required; New returns an error naming any that is
// missing. IDs, Clock, Telemetry, and Logger default to production
// implementations when left nil.
//
// This is a dependency struct, not the zero-value-means-default config
// struct go-style.md warns against — every field is a collaborator, not a
// tunable.
type Config struct {
	// Hooks dispatches every hook point a turn fires.
	Hooks HookDispatcher
	// Context runs step 1's context-assemble chain.
	Context ContextAssembler
	// Model runs steps 3-4.
	Model ModelCaller
	// Gate runs the policy precheck and the plan/apply gate.
	Gate PlanGate
	// Tools schedules and executes tool calls.
	Tools ToolScheduler
	// Catalog resolves the session's live context providers. It is this
	// package's only coupling to plugin lifecycle, and a read-only one.
	Catalog providercatalog.Catalog
	// IDs mints message and plan-item identifiers. Defaults to a minter
	// over statebackend.NewEventID and Clock.
	IDs IDMinter
	// Clock supplies the wall clock the default IDMinter stamps into a
	// ULID. Display-only and never an ordering authority
	// (determinism.md); defaults to time.Now.
	Clock func() time.Time
	// Telemetry provides the turn span. Defaults to a Provider with every
	// signal disabled, matching internal/plangate's and
	// internal/tooldispatch's own fallback convention.
	Telemetry *telemetry.Provider
	// Logger receives this package's structured output. Defaults to
	// slog.Default().
	Logger *slog.Logger
}

// Request is one turn's inputs. A session driver builds it; this package
// never invents a field of it.
type Request struct {
	// SessionID is the session this turn belongs to. MUST NOT be empty.
	SessionID string
	// ParentSessionID is the parent session's identifier when this is a
	// sub-agent session. Empty for a top-level session.
	ParentSessionID string
	// TurnID is this turn's identifier, a ULID. MUST NOT be empty.
	TurnID string
	// TurnIndex is this turn's zero-based position in the session, used
	// only as the turn span's bounded attribute.
	TurnIndex int
	// WorkingDirectory is the session's working directory, threaded into
	// every context request and every tool call's CallContext.
	WorkingDirectory string
	// Model is the resolved model handle this turn calls. MUST be set.
	Model providercatalog.ModelHandle
	// ModelTarget is the id/context_window/effective_ceiling triple every
	// context provider budgets against. MUST be set — resolving an
	// effective ceiling is the session driver's policy, not this
	// package's, so a missing one is an error rather than a guess.
	ModelTarget *modelv1.ModelTarget
	// Params are the caller's requested generation overrides, resolved
	// against the model's own ModelSpec by modelrequest.ValidateParams
	// before reaching the wire. MAY be nil.
	Params *modelv1.GenerationParams
	// History is the conversation history this turn starts from, in
	// emission order.
	History []*contentv1.Message
	// ScopedTools are the operations in scope for this turn, keyed by the
	// "<provider>.<tool>" name the model sees in a ToolUseBlock — exactly
	// agentprofile.ResolveTools's resolved key. The handle's own
	// Schema.Name is what reaches the provider as ToolCall.tool_name.
	ScopedTools map[string]providercatalog.ToolHandle
	// FilesTouched are the paths touched so far this session, threaded to
	// every context provider.
	FilesTouched []string
	// AssembledTokensLastTurn is the previous turn's Result.AssembledTokens.
	// Zero on a session's first turn.
	AssembledTokensLastTurn int64
	// PlanMode restricts this turn to data_source-kind calls by removing
	// every TOOL_KIND_RESOURCE operation from the tool specs sent to the
	// model — plan-apply-gate.md#decision-semantics' schema-removal
	// mechanism, never a runtime interception.
	PlanMode bool
	// FinalAnswer marks this as the limit-reached final-answer turn
	// (turn-algorithm.md#limit-reached-behavior): ALL tool specs are
	// withheld, not just resource ones, and a synthetic instruction
	// naming FinalAnswerReason is appended to the history so the model is
	// steered to a text-only answer.
	FinalAnswer bool
	// FinalAnswerReason names the bound that fired, rendered into the
	// synthetic instruction. Read only when FinalAnswer is set.
	FinalAnswerReason string
}

// DoneReason names why a turn ended the session's turn loop. It is
// meaningful only when Result.Done is true.
type DoneReason int

const (
	// DoneNone means the turn did not end the loop: the model asked for
	// tool calls and none of them terminated the turn.
	DoneNone DoneReason = iota
	// DoneNoToolCalls is turn-algorithm.md#done-detection's implicit,
	// MUST-support baseline: the model's message carried no tool_use
	// blocks.
	DoneNoToolCalls
	// DoneTerminalTool is the opt-in explicit path: a successfully
	// executed operation whose ToolSchema.terminates_turn is set.
	DoneTerminalTool
)

// String implements fmt.Stringer.
func (r DoneReason) String() string {
	switch r {
	case DoneNone:
		return "none"
	case DoneNoToolCalls:
		return "no_tool_calls"
	case DoneTerminalTool:
		return "terminal_tool"
	default:
		return "unknown"
	}
}

// Result is one turn's outcome — everything the session driver needs to
// run steps 16-18 itself.
type Result struct {
	// Message is the raw assistant message this turn produced, with its
	// kernel-assigned id and model attribution already stamped on by
	// modelcall.
	Message *contentv1.Message
	// History is history ++ message ++ the turn's tool_result blocks,
	// ready to carry into the next turn. The tool_result blocks ride in a
	// single ROLE_USER message, in the same declaration order their
	// tool_use blocks appeared in.
	History []*contentv1.Message
	// Usage is the completion's token accounting as the provider reported
	// it.
	Usage *modelv1.Usage
	// CostUSD is the kernel-computed cost of this turn's completion,
	// already persisted to the cost ledger by modelcall.
	CostUSD float64
	// AssembledTokens is this turn's total assembled context size — the
	// value the session driver threads back in as the next turn's
	// Request.AssembledTokensLastTurn.
	AssembledTokens int64
	// CallHashes are this turn's resource and data_source calls' hashes,
	// in declaration order, for the caller's step-16 doom-loop check.
	// Interactive calls are excluded: turn-algorithm.md#doom-loop-detection
	// scopes the check to "the most recent threshold resource/data-source
	// calls".
	CallHashes []string
	// TrippedProviders are the providers whose repeated-denial circuit
	// breaker tripped during this turn, sorted and deduplicated. The
	// caller routes a trip through the same graceful-degradation path a
	// bound uses; this package reports it and never acts on it.
	TrippedProviders []string
	// Done reports that this turn ended the session's turn loop.
	Done bool
	// DoneReason names why. Meaningful only when Done is true.
	DoneReason DoneReason
}

// Driver runs one turn. Construct with New; the zero value is not usable.
//
// A Driver holds only immutable collaborators, so it is safe for concurrent
// use — every mutable scrap of a turn's state lives on the per-call run
// value in runturn.go.
type Driver struct {
	hooks   HookDispatcher
	context ContextAssembler
	model   ModelCaller
	gate    PlanGate
	tools   ToolScheduler
	catalog providercatalog.Catalog
	ids     IDMinter
	clock   func() time.Time
	telem   *telemetry.Provider
	logger  *slog.Logger
}

// New returns a Driver over cfg's collaborators, or an error naming the
// first required one that is missing.
func New(cfg Config) (*Driver, error) {
	switch {
	case cfg.Hooks == nil:
		return nil, missing("Hooks")
	case cfg.Context == nil:
		return nil, missing("Context")
	case cfg.Model == nil:
		return nil, missing("Model")
	case cfg.Gate == nil:
		return nil, missing("Gate")
	case cfg.Tools == nil:
		return nil, missing("Tools")
	case cfg.Catalog == nil:
		return nil, missing("Catalog")
	}

	clock := cfg.Clock
	if clock == nil {
		clock = time.Now
	}
	ids := cfg.IDs
	if ids == nil {
		ids = ulidMinter{clock: clock}
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	telem := cfg.Telemetry
	if telem == nil {
		prov, err := telemetry.New(context.Background(), telemetry.Config{}, noop.New(), nil)
		if err != nil {
			return nil, err
		}
		telem = prov
	}

	return &Driver{
		hooks:   cfg.Hooks,
		context: cfg.Context,
		model:   cfg.Model,
		gate:    cfg.Gate,
		tools:   cfg.Tools,
		catalog: cfg.Catalog,
		ids:     ids,
		clock:   clock,
		telem:   telem,
		logger:  logger,
	}, nil
}

// ulidMinter is Config.IDs' production default: the same ULID generator
// every other kernel-assigned identifier already comes from.
type ulidMinter struct {
	clock func() time.Time
}

// New mints one identifier.
func (m ulidMinter) New() string {
	return statebackend.NewEventID(m.clock())
}

// Errors this package returns. A denied call, a failed tool, and a
// non-conformant model response are all data on the returned Result or on
// a synthesized tool_result block — never an error. An error here means the
// turn could not be run at all.
var (
	// ErrMissingCollaborator is returned by New when Config omits a
	// required collaborator.
	ErrMissingCollaborator = errors.New("turn: config is missing a required collaborator")

	// ErrNoSessionID is returned by RunTurn for a request with no session
	// id.
	ErrNoSessionID = errors.New("turn: request has no session id")

	// ErrNoTurnID is returned by RunTurn for a request with no turn id.
	ErrNoTurnID = errors.New("turn: request has no turn id")

	// ErrNoModelTarget is returned by RunTurn for a request with no model
	// target. Resolving an effective ceiling is the session driver's
	// policy decision; this package will not invent one.
	ErrNoModelTarget = errors.New("turn: request has no model target")

	// ErrOutcomeCount is returned when the scheduler returns a different
	// number of outcomes than the number of calls it was given. Reaching
	// it means a scheduler broke its own contract, and continuing would
	// pair a result with the wrong tool_use block.
	ErrOutcomeCount = errors.New("turn: scheduler returned a different number of outcomes than calls")
)

// missing renders ErrMissingCollaborator for one named field.
func missing(field string) error {
	return &missingError{Field: field}
}

// missingError names which Config field New found unset.
type missingError struct {
	// Field is the Config field name.
	Field string
}

// Error implements the error interface.
func (e *missingError) Error() string {
	return "turn: new: Config." + e.Field + " is required"
}

// Unwrap makes errors.Is(err, ErrMissingCollaborator) succeed.
func (e *missingError) Unwrap() error {
	return ErrMissingCollaborator
}

// unknownToolError synthesizes the ToolError a tool_use block naming an
// operation outside this turn's scope resolves to. The model asked for
// something it was never offered, which is a permission-shaped outcome,
// not a provider failure — and never retryable, since re-issuing the same
// call would miss the same scope.
func unknownToolError(name string) *toolv1.ToolError {
	return &toolv1.ToolError{
		Category:  toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_PERMISSION_DENIED,
		Message:   name + " is not in scope for this turn; this call was not executed",
		Retryable: false,
	}
}

// vetoDenialError synthesizes the ToolError a pre-tool-call veto's DENY
// resolves to. The wording deliberately mirrors internal/plangate's own
// denial text ("<provider>.<op> was denied (<decided_by>); this call was
// not executed") so a model sees one denial vocabulary regardless of which
// gate produced it, and it deliberately does not claim a subscriber
// examined the call: a veto that errored or timed out fails closed to the
// same DENY, and nothing this package receives can tell the two apart.
func vetoDenialError(provider, operation, deniedBy string) *toolv1.ToolError {
	return &toolv1.ToolError{
		Category:  toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_PERMISSION_DENIED,
		Message:   provider + "." + operation + " was denied (hook-veto:" + deniedBy + "); this call was not executed",
		Retryable: false,
	}
}
