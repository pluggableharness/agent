package providercatalog

import (
	"errors"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	contextv1 "github.com/pluggableharness/agent/pkg/context/proto/v1"
	hookv1 "github.com/pluggableharness/agent/pkg/hook/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"

	"github.com/pluggableharness/agent/internal/agentprofile"
)

// ErrNotFound is returned by any Catalog lookup for a name or ref this
// catalog does not have — including one whose provider simply is not
// loaded in this session. Callers match it with errors.Is, never on the
// error string; implementations wrap it with the name that missed.
var ErrNotFound = errors.New("providercatalog: provider not found")

// ModelHandle is a resolved, live handle to one model provider's one
// model.
type ModelHandle struct {
	// Ref is the {provider local name, vendor model id} pair this handle
	// resolves, matching the agent_profile model{} block that named it.
	Ref agentprofile.ModelRef
	// Producer identifies the plugin build serving this model, as
	// reported by its Describe RPC or the lock file.
	Producer *commonv1.ProducerRef
	// Spec is the model's declared capabilities and pricing, learned at
	// load time. It is what agentprofile.SelectModel's eligibility check
	// reads, so a caller never needs a round trip to route a turn.
	Spec *modelv1.ModelSpec
	// Client is the dialed ModelService client. Calling it is the
	// caller's job; this package never invokes an RPC on it.
	Client modelv1.ModelServiceClient
}

// ToolHandle is a resolved, live handle to one tool provider's one
// operation.
type ToolHandle struct {
	// Provider is the tool provider's agent.hcl local name — the left
	// half of a "<provider>.<tool>" scoping entry, not the plugin's
	// published name (which lives on Producer).
	Provider string
	// Producer identifies the plugin build serving this operation.
	Producer *commonv1.ProducerRef
	// Schema is the operation's declared schema, learned at load time
	// from GetSchema: kind and risk for the plan/apply gate, input and
	// output schemas, concurrency, timeout.
	Schema *toolv1.ToolSchema
	// Client is the dialed ToolService client.
	Client toolv1.ToolServiceClient
	// SupportsPreview reports whether this plugin actually implements
	// the optional Preview RPC. There is no ToolSchema field for it —
	// the tool protocol makes Preview a MAY and requires the kernel to
	// tolerate its absence — so the component that builds the catalog
	// resolves it once at load time and records it here, rather than
	// having the plan/apply gate discover it from an Unimplemented
	// status mid-turn.
	SupportsPreview bool
	// TerminatesTurn mirrors ToolSchema.terminates_turn: a successful
	// call of this operation is an immediate DoneCheck once its
	// post-tool-call hook has fired. Lifted out of Schema because the
	// turn driver's done-detection consults it on every tool result and
	// has no other reason to hold the whole schema.
	TerminatesTurn bool
}

// ContextHandle is a resolved, live handle to one context provider.
type ContextHandle struct {
	// Provider is the context provider's agent.hcl local name.
	Provider string
	// Producer identifies the plugin build serving this provider.
	Producer *commonv1.ProducerRef
	// Capabilities is the provider's declared static properties,
	// learned at load time: default token budget, stability, whether it
	// is a compactor, its slash commands, and its subscribed hook
	// points.
	Capabilities *contextv1.ContextCapabilities
	// Client is the dialed ContextService client.
	Client contextv1.ContextServiceClient
	// Position is this provider's declaration order in agent.hcl,
	// determined when the catalog was built. Contexts returns handles
	// already ordered by it; it is carried here so a caller that
	// filters or regroups handles can still recover the ordering
	// without recomputing it from config.
	Position int
	// TokenBudget is the effective per-turn token cap for this
	// provider: the agent.hcl override if one was declared, otherwise
	// Capabilities.DefaultTokenBudget. Resolving that precedence is the
	// catalog builder's job, not its consumers'.
	TokenBudget int64
}

// HookHandle is a resolved, live handle to one plugin's
// HookSubscriberService, dialed over the same connection as its primary
// category service.
type HookHandle struct {
	// Producer identifies the plugin build serving these hooks.
	Producer *commonv1.ProducerRef
	// Client is the dialed HookSubscriberService client.
	Client hookv1.HookSubscriberServiceClient
	// SupportedPoints are the hook points this plugin declared
	// subscriptions for, advertised alongside its category
	// capabilities. A dispatcher checks membership here before spending
	// an RPC on a point the plugin never subscribed to.
	SupportedPoints []commonv1.HookPoint
}

// Catalog is the read-only view of every live, resolved provider a
// session's turn loop needs — its only coupling to plugin lifecycle.
// Every method is a pure lookup against already-resolved state; nothing
// here launches, configures, or dials a plugin.
//
// Implementations are safe for concurrent use by multiple goroutines: a
// turn runs tool calls in parallel, and each of them resolves its own
// handle.
type Catalog interface {
	// Model resolves ref to a live handle, or ErrNotFound if that
	// provider or model id is not loaded.
	Model(ref agentprofile.ModelRef) (ModelHandle, error)

	// ModelSpecs returns every currently-loaded model's declared spec,
	// keyed by ref. The shape is exactly agentprofile.SelectModel's
	// specs parameter, so capability-aware routing is a direct call
	// with no adaptation: a ref absent from the map is a candidate
	// SelectModel skips rather than an error.
	ModelSpecs() map[agentprofile.ModelRef]*modelv1.ModelSpec

	// Tool resolves a provider local name and operation name to a live
	// handle, or ErrNotFound if that provider is not loaded or does not
	// advertise that operation.
	Tool(provider, tool string) (ToolHandle, error)

	// ToolNames returns every loaded tool provider's advertised
	// operation names, keyed by local name. The shape is exactly
	// agentprofile.ResolveTools's available parameter, so expanding a
	// profile's tool scoping is a direct call with no adaptation.
	ToolNames() map[string][]string

	// Contexts returns every loaded context provider's handle, ordered
	// by the Position each handle carries — agent.hcl declaration
	// order, decided when the catalog was built and never recomputed
	// here.
	Contexts() []ContextHandle

	// Hook resolves a loaded plugin's HookSubscriberService by its
	// agent.hcl local name, or ErrNotFound if that plugin is not loaded
	// or serves no hooks. The name is the local one from any category —
	// a hook subscription rides the same connection as the plugin's
	// primary category service.
	Hook(provider string) (HookHandle, error)
}
