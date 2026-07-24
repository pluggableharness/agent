package context

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/types/known/structpb"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	"github.com/pluggableharness/agent/pkg/kernel"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
	"github.com/pluggableharness/agent/pkg/plugin"
	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
)

// Stability is a Section's or Capabilities' turn-to-turn
// change hint (data-types.md#stability-hint--cache-prefix-ordering):
// StabilityStatic for content unchanged for the session (e.g. a repo
// convention file), StabilityDynamic for content that may differ turn to
// turn (e.g. git status or a file tree). Static-stability content SHOULD
// be declared by providers earlier in agent.hcl declaration order than
// dynamic-stability content, to preserve prompt-cache prefix reuse.
type Stability int32

const (
	// StabilityUnspecified is the zero value. Never valid on a value a
	// provider actually returns; its presence means the author forgot to
	// set Stability.
	StabilityUnspecified Stability = iota
	// StabilityStatic marks content unchanged for the whole session.
	StabilityStatic
	// StabilityDynamic marks content that may differ turn to turn.
	StabilityDynamic
)

// String returns s's spec vocabulary name ("static", "dynamic",
// "unspecified"), for logging and error messages.
func (s Stability) String() string {
	switch s {
	case StabilityStatic:
		return "static"
	case StabilityDynamic:
		return "dynamic"
	default:
		return "unspecified"
	}
}

// Capabilities reports a context provider's static properties —
// the author-facing analogue of contextv1.ContextCapabilities
// (protocol.md#getcapabilities). DefaultTokenBudget and Stability MUST be
// set by every provider; Stability and Compactor MUST be re-queryable
// cheaply and MUST NOT depend on a live read of the content source. Build
// one with NewCapabilities rather than a struct literal.
type Capabilities struct {
	// DefaultTokenBudget is the token cap this provider requests if
	// agent.hcl does not override it. MUST be set.
	DefaultTokenBudget int64
	// Stability declares whether this provider's contributed content
	// changes turn to turn. MUST be set.
	Stability Stability
	// Compactor declares whether this provider MAY rewrite, merge, or
	// drop other providers' sections in the chain it receives, and MAY
	// receive ConversationHistory on Request and return
	// RewrittenHistory. Defaults to false — most providers are not
	// compactors. See data-types.md#ordering--chaining.
	Compactor bool
	// SlashCommands are prompt-expansion slash commands this provider
	// contributes. MAY be empty.
	SlashCommands []*commonv1.PromptExpansionSpec
	// ConfigSchema is this provider's agent.hcl config schema, built via
	// pkg/config. MUST be set (MAY be an empty schema for a provider
	// with no configuration).
	ConfigSchema *configv1.ConfigSchema
	// SupportedHookPoints are the hook points (beyond context-assemble
	// itself) this provider subscribes HookSubscriberService.DispatchHook
	// to. MAY be empty.
	SupportedHookPoints []commonv1.HookPoint
}

// Section is one provider's contribution to the assembled prompt
// context — the author-facing analogue of contentv1.Section
// (data-types.md#contextsection). Content is a plain string because v1 is
// text-only: a non-text content block MUST be rejected, not silently
// dropped, so this type simply has no way to express one.
type Section struct {
	// Provider is the producing plugin's declared name — the identity
	// key a provider uses to re-find and replace its own prior
	// section(s) in the chain. MUST match the plugin's own agent.hcl
	// declared name.
	Provider string
	// Label MUST be set — the kernel wraps every section in a clearly
	// delimited boundary using it when concatenating the chain into the
	// final prompt (data-types.md#labeling).
	Label string
	// Content is this section's text. MUST be computed via CountTokens
	// (below) for the Tokens field, and MUST fit within the
	// Request.TokenBudget this provider was given — the provider
	// itself, not the kernel, performs any needed reduction
	// (data-types.md#budget-mechanics).
	Content string
	// Tokens MUST be computed via the kernel's CountTokens callback
	// primitive — see CountTokens below — never a provider-local
	// heuristic estimate.
	Tokens int64
	// Stability MUST be set.
	Stability Stability
	// Truncated records whether this section was cut down to fit its
	// budget. Setting it true is NOT itself sufficient to satisfy the
	// budget constraint — Content itself must actually fit.
	Truncated bool
}

// Request is Contribute's request — the author-facing analogue of
// contextv1.ContextRequest (data-types.md#contextrequest), delivered once
// per context-assemble firing (at least once per turn, before each model
// call).
type Request struct {
	SessionID       string
	ParentSessionID string
	// TurnID identifies which turn this firing is for, a ULID string.
	TurnID string
	// TokenBudget is the kernel-computed allocation for this provider's
	// own contribution this call — agent.hcl's declared override if
	// present, else this provider's own Capabilities.DefaultTokenBudget.
	TokenBudget int64
	// ModelTarget describes the model this context is being assembled
	// for. Note it carries no provider name (only id, context_window,
	// effective_ceiling) — CountTokens below cannot derive a fully
	// qualified model.v1.ModelRef from it alone, so it routes through
	// the kernel's documented fallback heuristic unless the caller
	// supplies its own ModelRef via the package-level CountTokens
	// function.
	ModelTarget *modelv1.ModelTarget
	// FilesTouched MAY be empty (e.g. turn 0 / session-start). A
	// provider MAY react to it for JIT-scoped, subdirectory-narrowed
	// contributions (README.md#firing-cadence--jit-loading).
	FilesTouched     []string
	WorkingDirectory string
	// PriorSections is the accumulated output of earlier providers in
	// this hook's declaration-order chain. A non-compactor provider MUST
	// only append or edit its OWN section(s) (matched by Provider name)
	// in the chain it returns from Contribute — see
	// Contribution.Sections and CheckOwnSectionOnly below.
	PriorSections []*Section
	// ConversationHistory arrives populated ONLY when this provider's
	// own Capabilities.Compactor == true; for every other
	// provider it is nil, indistinguishable from "not provided".
	ConversationHistory []*contentv1.Message
	// HistoryTokens is the kernel-computed current conversation-history
	// token total, carried on every firing (not just compactor-directed
	// ones) — see data-types.md#compactor-timing-signals.
	HistoryTokens int64
	// AssembledTokensLastTurn is the kernel-computed total assembled
	// context size of the previous turn.
	AssembledTokensLastTurn int64

	// CountTokens is bound by Service.Contribute to the kernel's
	// CountTokens callback (kernel-callbacks.md#counttokens) — call this
	// to compute a Section.Tokens value rather than inventing a
	// local heuristic. Always non-nil when Contribute is invoked through
	// a *Service; a hand-rolled unit test that constructs a
	// *Request directly MUST set it (e.g. to a fake, or to a
	// closure built from the package-level CountTokens function against
	// a bufconn kernel-callback test server).
	CountTokens func(ctx context.Context, text string) (int64, error)
}

// Contribution is Contribute's response — the author-facing
// analogue of contextv1.ContextContribution
// (data-types.md#contextcontribution).
type Contribution struct {
	// Sections MUST be the FULL accumulated chain, in declaration order,
	// including this provider's own new/updated section(s) — this
	// provider's own section appended to (or edited within)
	// Request.PriorSections, NEVER a delta. A non-compactor
	// provider mutating a section it doesn't own is a scope_violation:
	// the kernel discards the entire response for the turn. See
	// CheckOwnSectionOnly.
	Sections []*Section
	// RewrittenHistory MAY be included by a compactor provider (only)
	// alongside its section contribution. When present, the kernel
	// replaces the turn's conversation history with this value before
	// the next model call.
	RewrittenHistory []*contentv1.Message
}

// Provider is the interface a context provider plugin author implements.
// NewService adapts a Provider into a real
// pluggableharness.context.v1.ContextService gRPC server.
type Provider interface {
	// GetCapabilities reports this provider's static properties. MUST be
	// cheap and side-effect-free — see Capabilities.Stability and
	// Capabilities.Compactor.
	GetCapabilities(ctx context.Context) (*Capabilities, error)
	// Configure delivers this provider's agent.hcl config block, already
	// decoded via the schema-to-cty bridge into config. MUST reject with
	// a structured error (see errors.go) if a declared source path/glob
	// cannot be resolved to anything on disk, rather than deferring to a
	// silent-empty Contribute at first call.
	Configure(ctx context.Context, config *structpb.Struct) error
	// Contribute is the context-assemble RPC's author-facing entry
	// point, invoked at least once per turn, before each model call.
	//
	// req.PriorSections is the accumulated chain from every earlier
	// provider in this hook's declaration-order chain.
	// Contribution.Sections in the return value MUST be the FULL
	// accumulated chain — req.PriorSections plus (or with) this
	// provider's own section(s) — NEVER just this provider's own
	// addition. A non-compactor implementation MUST only append or edit
	// sections whose Provider field matches this plugin's own declared
	// name; see CheckOwnSectionOnly for a helper to verify that
	// contract in this provider's own tests.
	Contribute(ctx context.Context, req *Request) (*Contribution, error)
}

// Renderer is the optional interface a Provider MAY additionally
// implement to support the Render RPC (protocol.md#render) — e.g. to
// render an injected CLAUDE.md section collapsed by default in a
// transcript view. A Provider that doesn't implement Renderer causes
// Service.Render to return codes.Unimplemented, and the kernel falls back
// to its generic default rendering.
type Renderer interface {
	Render(ctx context.Context, req *RenderRequest) (*renderv1.RenderTree, error)
}

// RenderRequest is Render's request — the same opaque payload/schema_version
// shape as every other category's Render RPC
// (frontend/render-tree.md#schema-versioning). Kept as a thin alias over
// the generated fields rather than a domain type: the payload is opaque by
// design (.claude/rules/grpc.md's Emit->Render->Paint carve-out), so there
// is nothing to translate.
type RenderRequest struct {
	// Payload is the opaque, previously-Emit'd bytes to render.
	Payload []byte
	// SchemaVersion identifies which shape Payload was encoded with. A
	// Renderer MUST branch on this rather than sniffing Payload's shape,
	// and MUST keep decoding every schema_version it has ever emitted.
	SchemaVersion string
}

// CheckOwnSectionOnly reports a scope_violation-shaped *Error if
// chain diverges from prior anywhere other than the sections owned by
// providerName, unless compactor is true. This mirrors
// data-types.md#ordering--chaining's own-section-only rule structurally:
// a non-compactor context provider MUST only append or edit its own
// section(s). Not invoked automatically as part of the RPC contract (the
// kernel is the actual enforcement authority — a plugin cannot make the
// kernel accept or reject its own response), but useful as a check in a
// provider's own Contribute tests, and Service.Contribute calls it
// defensively (log-only) as well.
func CheckOwnSectionOnly(prior, chain []*Section, providerName string, compactor bool) error {
	if compactor {
		return nil
	}
	if len(chain) < len(prior) {
		return &Error{
			Category: ErrorCategoryScopeViolation,
			Message:  fmt.Sprintf("context: returned chain has %d section(s), fewer than the %d it was given, without compactor capability", len(chain), len(prior)),
		}
	}
	for i, p := range prior {
		if p.Provider == providerName {
			continue
		}
		c := chain[i]
		if !sectionEqual(p, c) {
			return &Error{
				Category: ErrorCategoryScopeViolation,
				Message:  fmt.Sprintf("context: section %d (provider %q) was mutated by non-owning provider %q", i, p.Provider, providerName),
			}
		}
	}
	for i := len(prior); i < len(chain); i++ {
		if chain[i].Provider != providerName {
			return &Error{
				Category: ErrorCategoryScopeViolation,
				Message:  fmt.Sprintf("context: appended section %d has provider %q, want %q", i, chain[i].Provider, providerName),
			}
		}
	}
	return nil
}

// sectionEqual reports whether a and b carry identical field values.
func sectionEqual(a, b *Section) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Provider == b.Provider &&
		a.Label == b.Label &&
		a.Content == b.Content &&
		a.Tokens == b.Tokens &&
		a.Stability == b.Stability &&
		a.Truncated == b.Truncated
}

// CountTokens resolves text's token count via the kernel's CountTokens
// callback primitive (kernel-callbacks.md#counttokens) — the ONLY
// sanctioned way a Section.Tokens value may be produced
// (data-types.md#contextsection: "never a provider-local heuristic
// estimate"). modelRef MAY be nil, in which case the kernel's single
// documented fallback heuristic applies
// (kernel-callbacks.md#the-fallback-heuristic:
// ceil(utf8_byte_length/4)) rather than a real vendor tokenizer. This is
// the function Request.CountTokens is built from; call it directly
// only when a provider needs a model.v1.ModelRef more specific than what
// Request.ModelTarget alone can supply (ModelTarget carries no
// provider name).
func CountTokens(ctx context.Context, cb *plugin.Callback, modelRef *modelv1.ModelRef, text string) (int64, error) {
	client, err := cb.Client(ctx)
	if err != nil {
		return 0, fmt.Errorf("context: count tokens: %w", err)
	}
	return countTokens(ctx, client, modelRef, text)
}

// countTokens is CountTokens' shared implementation over an
// already-dialed *kernel.Client, reused by Service.Contribute so it
// doesn't need to re-dial the callback broker for every provider it
// wires a Request.CountTokens closure for.
func countTokens(ctx context.Context, client *kernel.Client, modelRef *modelv1.ModelRef, text string) (int64, error) {
	result, err := client.CountTokens(ctx, &kernelv1.CountTokensRequest{
		Content: []*contentv1.ContentBlock{
			{Block: &contentv1.ContentBlock_Text{Text: &contentv1.TextBlock{Text: text}}},
		},
		ModelRef: modelRef,
	})
	if err != nil {
		return 0, fmt.Errorf("context: count tokens: %w", err)
	}
	return result.GetCount(), nil
}
