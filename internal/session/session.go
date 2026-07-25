package session

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"time"

	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	hookv1 "github.com/pluggableharness/agent/pkg/hook/proto/v1"
	sessionv1 "github.com/pluggableharness/agent/pkg/session/proto/v1"

	"github.com/pluggableharness/agent/internal/agentprofile"
	"github.com/pluggableharness/agent/internal/doomloop"
	"github.com/pluggableharness/agent/internal/eventbus"
	"github.com/pluggableharness/agent/internal/hookdispatch"
	"github.com/pluggableharness/agent/internal/providercatalog"
	"github.com/pluggableharness/agent/internal/sessionscope"
	"github.com/pluggableharness/agent/internal/sessionstate"
	"github.com/pluggableharness/agent/internal/statebackend"
	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/noop"
	"github.com/pluggableharness/agent/internal/turn"
)

// TurnDriver is the narrow view of internal/turn this package consumes:
// one whole turn (turn-algorithm.md steps 1-15) in one call. *turn.Driver
// satisfies it structurally with no adapter — this is its exact concrete
// method signature.
//
// It is declared here rather than taking *turn.Driver as a field, for the
// same reason internal/plangate declares its own HookDispatcher and
// internal/turn declares its own five collaborator interfaces: the session
// driver needs exactly one method, and keeping it an interface is what
// lets the whole turn loop — bounds, doom loop, breaker trips,
// cancellation, the limit-reached final-answer turn — be tested against a
// scripted fake instead of a real model provider.
//
// turn.Request and turn.Result are used verbatim rather than re-declared:
// they are data, not behavior, and a second Go representation of a
// collaborator's request/result is exactly the parallel type
// go-layout.md forbids in internal/.
type TurnDriver interface {
	RunTurn(ctx context.Context, req turn.Request) (turn.Result, error)
}

// HookDispatcher is the narrow view of hook dispatch this package
// consumes. The hook point is not a parameter — HookPayload is a oneof and
// the variant set on it IS the point. *hookdispatch.Dispatcher satisfies
// it as written.
//
// This package dispatches exactly two points, each exactly once per
// session: session-start before the first turn and session-end after the
// loop exits (agent-loop/README.md#scope-and-definitions). Neither is
// veto-bearing and neither has a transform-mutable field, so Outcome is
// discarded — see this package's CLAUDE.md for why a dispatch error is
// logged and swallowed rather than failing the session.
type HookDispatcher interface {
	Dispatch(ctx context.Context, payload *hookv1.HookPayload) (hookdispatch.Outcome, error)
}

// DefaultProfileName is the profile the root session uses when a caller
// names none — configuration/agent-profiles.md#the-implicit-root-profile's
// "the kernel uses the profile named default for the root session unless
// the CLI is told otherwise".
const DefaultProfileName = "default"

// defaultKernelMaxDepth is Config.KernelDefaultMaxDepth's fallback: the
// same "effectively unbounded" sentinel internal/kernelcallback's
// GetSession already reports as RemainingDepth
// (rootSessionRemainingDepth), deliberately reused rather than picking a
// second, disagreeing number for the same idea.
//
// This build is root-sessions-only and no loaded tool advertises a
// spawn-capable operation, so the resolved depth budget is recorded and
// logged but never actually excludes anything from a session's tool
// registry (agent-profiles.md#depth-budget's "when a session's remaining
// depth reaches zero or below, the kernel MUST exclude every spawn-capable
// tool"). A future phase that lands sub-agent spawning replaces this
// constant with the operator's settings.max_depth and wires the exclusion.
const defaultKernelMaxDepth = math.MaxInt32

// effectiveCeilingPercent is the share of a model's context_window this
// package reports as ModelTarget.effective_ceiling — the usable portion
// after reserving space for expected output, tool schemas, and other fixed
// per-turn overhead (model/data-types.md's ModelTarget). Resolving an
// effective ceiling is the session driver's policy, which is exactly why
// internal/turn refuses to invent one (turn.ErrNoModelTarget); 80% is this
// build's documented, deliberately conservative choice, integer arithmetic
// so it is bit-identical on every platform (determinism.md).
const effectiveCeilingPercent = 80

// Reasons named in turn.Request.FinalAnswerReason, and carried back on
// Result.FinalAnswerReason. The three bound reasons mirror
// turn-algorithm.md#limit-reached-behavior's three status subtypes; the
// other two name the mechanisms that have no status subtype of their own
// (see Result.Status).
const (
	// ReasonMaxTurns names a fired max_turns bound.
	ReasonMaxTurns = "max_turns"
	// ReasonMaxCostUSD names a fired max_cost_usd bound.
	ReasonMaxCostUSD = "max_cost_usd"
	// ReasonMaxWallClock names a fired max_wall_clock_s bound.
	ReasonMaxWallClock = "max_wall_clock_s"
	// ReasonDoomLoop names a tripped doom-loop detector
	// (turn-algorithm.md#doom-loop-detection).
	ReasonDoomLoop = "doom_loop"
	// ReasonCircuitBreaker names a tripped repeated-denial circuit breaker
	// (plan-apply-gate.md#circuit-breaker-on-repeated-denials).
	ReasonCircuitBreaker = "circuit_breaker"
)

// BuiltinDefaultProfile returns the kernel-builtin profile used when no
// agent_profile "default" block exists at all
// (configuration/agent-profiles.md#the-implicit-root-profile: "kernel-builtin
// defaults apply for every field below"). It is a function rather than a
// package-level var because AgentProfile carries slices and a mutable
// global would let one caller's edit leak into every later session
// (go-style.md's no-global-mutable-state rule).
//
// The three loop bounds are agent-profiles.md's own agent_profile
// "default" example verbatim — 200 turns, $5.00, one hour — rather than
// numbers invented here. Tools and SlashCommands stay empty: §8.3's strict
// default ("a profile that omits tools entirely inherits no tools") is a
// posture, not an artifact of a declared block, so a kernel with no
// profile configured at all gets a text-only session rather than the full
// loaded capability set. MaxDepth stays nil so RootRemainingDepth falls
// through to Config.KernelDefaultMaxDepth, and Model stays empty so
// resolution falls through to the sole-loaded-model rule documented on
// ErrNoDefaultModel.
func BuiltinDefaultProfile() agentprofile.AgentProfile {
	return agentprofile.AgentProfile{
		Name:          DefaultProfileName,
		MaxTurns:      200,
		MaxCostUSD:    5.00,
		MaxWallClockS: 3600,
	}
}

// Config is a Runner's collaborators. Store, Sessions, Scopes, Bus, Turn,
// Hooks, and Catalog are required; New returns an error naming the first
// one missing. Profiles may be nil — a nil map resolves the implicit
// default profile via BuiltinDefaultProfile. DoomLoop, KernelDefaultMaxDepth,
// Clock, Telemetry, and Logger all default when left at their zero value.
//
// This is a dependency struct, not the zero-value-means-default config
// struct go-style.md warns against: every required field is a
// collaborator, and the four optional ones are documented defaults, not
// tunables a caller is expected to reason about.
//
// There is deliberately no *circuitbreaker.Breaker field. One Breaker is
// scoped to one session and is consumed by internal/plangate
// (plangate.Config.Breaker) and internal/tooldispatch
// (tooldispatch.Config.Breaker), both of which sit *below* the TurnDriver
// seam — a Runner receives an already-constructed turn driver and cannot
// reach into it. The composition root constructs the per-session Breaker
// and wires it into those two Configs; what reaches this package is the
// trip signal both of them surface back through turn.Result.TrippedProviders.
type Config struct {
	// Store creates the session's sqlite file
	// (docs/specifications/state-backend.md#file-layout).
	Store *statebackend.Store
	// Sessions is the process-wide live-session registry this session
	// registers itself in for the duration of its run, so
	// internal/kernelcallback can resolve an authorized plugin's
	// Emit/GetSession/ReadEvents call to it.
	Sessions *sessionstate.Table
	// Scopes is the callback-grant table. One session-lifetime grant per
	// resolved plugin is taken at session start and released at session
	// end, on every exit path.
	Scopes *sessionscope.Registry
	// Bus is the event bus a session's persisted events republish onto,
	// threaded into the sessionstate.Live this package constructs.
	Bus *eventbus.Bus
	// Turn runs one turn. Production wiring passes a *turn.Driver.
	Turn TurnDriver
	// Hooks dispatches session-start and session-end.
	Hooks HookDispatcher
	// Catalog resolves the profile's model chain and tool scoping against
	// the providers actually loaded this session.
	Catalog providercatalog.Catalog
	// Profiles are the decoded agent_profile blocks, keyed by name —
	// exactly config.Config.AgentProfiles. A nil or missing "default"
	// entry falls back to BuiltinDefaultProfile.
	Profiles map[string]agentprofile.AgentProfile
	// KernelDefaultMaxDepth is the kernel's configured default root-session
	// depth ceiling (settings.max_depth), the kernelDefault argument
	// agentprofile.RootRemainingDepth resolves an unset profile MaxDepth
	// against. Zero or negative means unset and resolves to
	// defaultKernelMaxDepth.
	KernelDefaultMaxDepth int
	// DoomLoop is the detector's window/threshold. The zero value resolves
	// to doomloop.DefaultConfig.
	DoomLoop doomloop.Config
	// Clock supplies session start/end timestamps, the elapsed figure the
	// wall-clock bound is checked against, and the ULID timestamps for the
	// session and its turn ids. Defaults to time.Now. Display-only and
	// never an ordering authority (determinism.md) — with the single
	// exception of the wall-clock bound, which is a duration measurement
	// rather than an ordering decision.
	Clock func() time.Time
	// Telemetry provides the session span and the session/turn metrics.
	// Defaults to a Provider with every signal disabled, matching the
	// fallback convention internal/turn, internal/plangate, and
	// internal/tooldispatch already use.
	Telemetry *telemetry.Provider
	// Logger receives this package's structured output. Defaults to
	// slog.Default().
	Logger *slog.Logger
}

// Spec is one session's inputs — everything a caller supplies that isn't
// already resolved from configuration.
type Spec struct {
	// Profile names the agent_profile to run under. Empty resolves to
	// DefaultProfileName.
	Profile string
	// Prompt is the session's initial user message. It is the ONLY thing
	// that crosses into a session's history at start
	// (subagents.md#context-isolation-default-fresh's fresh-context
	// default), everything else being contributed by the profile's own
	// context-assemble chain.
	Prompt string
	// WorkingDirectory is threaded into every context request and every
	// tool call's CallContext, and reported to session-start subscribers.
	WorkingDirectory string
	// PlanMode restricts every turn of this session to data_source-kind
	// calls, by removing TOOL_KIND_RESOURCE operations from the specs sent
	// to the model (plan-apply-gate.md#decision-semantics' schema-removal
	// mechanism). It is a per-session setting here because nothing yet
	// toggles it mid-session.
	PlanMode bool
}

// Result is one whole session's outcome, mirroring
// subagents.md#data-types' RunSessionResult: the terminal status, the one
// message that crosses a session boundary, and the aggregate spend and
// token counts an orchestrator does budget-aware fan-out on.
type Result struct {
	// SessionID is the created session's id. Set even on most failure
	// paths, so a caller can find the partial session's file.
	SessionID string
	// Status is the terminal status persisted to session_meta.
	Status sessionv1.SessionStatus
	// FinalMessage is the last assistant message the session produced, or
	// nil if it never produced one.
	FinalMessage *contentv1.Message
	// TotalCostUSD is the session's aggregate spend, summed across every
	// turn (and, once a session tree exists, every descendant).
	TotalCostUSD float64
	// TotalInputTokens is the aggregate input-token count across every
	// turn's usage event.
	TotalInputTokens int64
	// TotalOutputTokens is the aggregate output-token count across every
	// turn's usage event.
	TotalOutputTokens int64
	// FinalAnswerReason names what routed this session through the
	// limit-reached path (one of the Reason* constants), or is empty when
	// no limit-reached turn ran. It is the only place a doom-loop or
	// circuit-breaker termination is distinguishable from an ordinary
	// completion, since neither has a SessionStatus of its own.
	FinalAnswerReason string
}

// Runner drives whole sessions. Construct with New; the zero value is not
// usable.
//
// A Runner holds only immutable collaborators — every mutable scrap of a
// session's state lives on the per-call value in run.go — so it is safe
// for concurrent Run calls, one per session.
type Runner struct {
	store    *statebackend.Store
	sessions *sessionstate.Table
	scopes   *sessionscope.Registry
	bus      *eventbus.Bus
	turn     TurnDriver
	hooks    HookDispatcher
	catalog  providercatalog.Catalog
	profiles map[string]agentprofile.AgentProfile

	kernelDefaultMaxDepth int
	doomLoop              doomloop.Config
	clock                 func() time.Time
	telem                 *telemetry.Provider
	logger                *slog.Logger
}

// New returns a Runner over cfg's collaborators, or an error naming the
// first required one that is missing (or the first invalid optional one —
// an out-of-range doom-loop threshold fails here rather than at the first
// turn).
func New(cfg Config) (*Runner, error) {
	if err := requireCollaborators(cfg); err != nil {
		return nil, err
	}

	loop := cfg.DoomLoop
	if loop == (doomloop.Config{}) {
		loop = doomloop.DefaultConfig
	}
	if _, err := doomloop.New(loop); err != nil {
		return nil, err
	}

	depth := cfg.KernelDefaultMaxDepth
	if depth <= 0 {
		depth = defaultKernelMaxDepth
	}
	clock := cfg.Clock
	if clock == nil {
		clock = time.Now
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

	return &Runner{
		store:                 cfg.Store,
		sessions:              cfg.Sessions,
		scopes:                cfg.Scopes,
		bus:                   cfg.Bus,
		turn:                  cfg.Turn,
		hooks:                 cfg.Hooks,
		catalog:               cfg.Catalog,
		profiles:              cfg.Profiles,
		kernelDefaultMaxDepth: depth,
		doomLoop:              loop,
		clock:                 clock,
		telem:                 telem,
		logger:                logger,
	}, nil
}

// requireCollaborators reports the first required Config field left unset.
func requireCollaborators(cfg Config) error {
	switch {
	case cfg.Store == nil:
		return missing("Store")
	case cfg.Sessions == nil:
		return missing("Sessions")
	case cfg.Scopes == nil:
		return missing("Scopes")
	case cfg.Bus == nil:
		return missing("Bus")
	case cfg.Turn == nil:
		return missing("Turn")
	case cfg.Hooks == nil:
		return missing("Hooks")
	case cfg.Catalog == nil:
		return missing("Catalog")
	}
	return nil
}

// Errors this package returns. A fired bound, a tripped doom loop, and a
// tripped circuit breaker are all statuses on the returned Result, never
// errors — turn-algorithm.md#limit-reached-behavior is explicit that the
// kernel MUST NOT raise an unrecoverable error as the default behavior. An
// error here means the session could not be run, or a turn failed outright.
var (
	// ErrMissingCollaborator is returned by New when Config omits a
	// required collaborator.
	ErrMissingCollaborator = errors.New("session: config is missing a required collaborator")

	// ErrUnknownProfile is returned by Run when Spec.Profile names an
	// agent_profile that isn't configured. It is deliberately NOT returned
	// for the profile named "default", which falls back to
	// BuiltinDefaultProfile instead.
	ErrUnknownProfile = errors.New("session: no such agent profile")

	// ErrNoDefaultModel is returned by Run when the resolved profile
	// declares no model{} block and the loaded provider set does not
	// contain exactly one model to fall back to. Exactly one is the only
	// unambiguous case: picking among several would be an arbitrary
	// choice this package refuses to make on an operator's behalf, and
	// picking among none is impossible.
	ErrNoDefaultModel = errors.New("session: profile declares no model and no sole loaded model to default to")
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
	return "session: new: Config." + e.Field + " is required"
}

// Unwrap makes errors.Is(err, ErrMissingCollaborator) succeed.
func (e *missingError) Unwrap() error {
	return ErrMissingCollaborator
}
