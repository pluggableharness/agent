package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/pluggableharness/agent/internal/pluginruntime"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
	contextv1 "github.com/pluggableharness/agent/pkg/context/proto/v1"
	frontendv1 "github.com/pluggableharness/agent/pkg/frontend/proto/v1"
	hookv1 "github.com/pluggableharness/agent/pkg/hook/proto/v1"
	memoryv1 "github.com/pluggableharness/agent/pkg/memory/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
	slashcommandv1 "github.com/pluggableharness/agent/pkg/slashcommand/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
	widgetv1 "github.com/pluggableharness/agent/pkg/widget/proto/v1"
)

// ErrDuplicateKey reports two loaded plugins claiming the same
// {category, name} identity. v1 supports exactly one instance per
// required_providers entry and has no aliasing mechanism
// (configuration/blocks-reference.md#required_providers), so a collision
// is a hard startup error rather than a last-one-wins overwrite.
var ErrDuplicateKey = errors.New("pluginhost: duplicate plugin key")

// Key identifies a loaded plugin by the identity it reported for itself
// through Describe — not by its agent.hcl local name, which is the
// operator's label for it. Version is deliberately absent: v1 forbids two
// concurrently-loaded builds of the same category+name, matching
// sessionscope.Key's own shape and reasoning.
type Key struct {
	Category commonv1.Category
	Name     string
}

// Live is one launched, described, configured, and registered plugin.
type Live struct {
	// LocalName is the agent.hcl required_providers local name this
	// plugin was declared under — the left half of a "<provider>.<tool>"
	// scoping entry, and what a provider{} block and an agent_profile
	// reference. It is not Producer.Name.
	LocalName string

	// Producer is the identity the plugin reported through Describe,
	// reconciled against the lock file before registration.
	Producer *commonv1.ProducerRef

	// Client is the raw generated category service client — a
	// modelv1.ModelServiceClient for a model plugin, and so on. Callers
	// use the typed accessors below rather than asserting on this
	// directly.
	Client any

	// Capabilities is the whole capability advertisement this plugin
	// answered with: a *modelv1.GetCapabilitiesResponse, a
	// *toolv1.GetSchemaResponse, and so on. Retained because it is the
	// only place a later consumer can read a category's per-model specs,
	// per-tool schemas, or subscribed hook points from without a second
	// round trip; ConfigSchema below is the one field this package
	// itself needs.
	Capabilities any

	// ConfigSchema is the provider's declared agent.hcl config schema,
	// as fetched from GetCapabilities/GetSchema and used to decode its
	// provider{} block.
	ConfigSchema *configv1.ConfigSchema

	// LaunchIndex is this plugin's position in launch order — the
	// providerresolve.Order sequence. Shutdown tears down in reverse
	// LaunchIndex order.
	LaunchIndex int

	// plugin is the underlying subprocess handle. Unexported: its
	// lifecycle belongs to Supervisor, and handing it out would let a
	// consumer close a plugin the supervisor still believes is running.
	plugin *pluginruntime.Plugin

	// closeFn tears this plugin's subprocess down. It is
	// plugin.Close for every real launch, held as a function value
	// rather than called through plugin directly so Shutdown's
	// ordering and error-aggregation behavior is unit-testable without
	// a real subprocess — the same factor-for-testability move
	// internal/pluginruntime's own closeWithKill makes.
	closeFn func(context.Context) error
}

// Exited reports whether this plugin's subprocess has terminated. It
// forwards to the underlying handle rather than exposing it, keeping the
// "its lifecycle belongs to Supervisor" rule above intact: a caller can
// observe that a plugin is gone without being able to end one.
//
// A Live built by a test with no real subprocess reports false — there is
// nothing that could have exited.
func (l *Live) Exited() bool {
	if l.plugin == nil {
		return false
	}
	return l.plugin.Exited()
}

// ModelClient returns this plugin's ModelService client, or ok=false if
// it is not a model plugin.
func (l *Live) ModelClient() (modelv1.ModelServiceClient, bool) {
	c, ok := l.Client.(modelv1.ModelServiceClient)
	return c, ok
}

// ToolClient returns this plugin's ToolService client, or ok=false if it
// is not a tool plugin.
func (l *Live) ToolClient() (toolv1.ToolServiceClient, bool) {
	c, ok := l.Client.(toolv1.ToolServiceClient)
	return c, ok
}

// ContextClient returns this plugin's ContextService client, or ok=false
// if it is not a context plugin.
func (l *Live) ContextClient() (contextv1.ContextServiceClient, bool) {
	c, ok := l.Client.(contextv1.ContextServiceClient)
	return c, ok
}

// MemoryClient returns this plugin's MemoryService client, or ok=false if
// it is not a memory plugin.
func (l *Live) MemoryClient() (memoryv1.MemoryServiceClient, bool) {
	c, ok := l.Client.(memoryv1.MemoryServiceClient)
	return c, ok
}

// FrontendClient returns this plugin's FrontendService client, or
// ok=false if it is not a frontend plugin.
func (l *Live) FrontendClient() (frontendv1.FrontendServiceClient, bool) {
	c, ok := l.Client.(frontendv1.FrontendServiceClient)
	return c, ok
}

// WidgetClient returns this plugin's WidgetService client, or ok=false if
// it is not a widget plugin.
func (l *Live) WidgetClient() (widgetv1.WidgetServiceClient, bool) {
	c, ok := l.Client.(widgetv1.WidgetServiceClient)
	return c, ok
}

// SlashCommandClient returns this plugin's SlashCommandService client, or
// ok=false if it is not a slashcommand plugin.
func (l *Live) SlashCommandClient() (slashcommandv1.SlashCommandServiceClient, bool) {
	c, ok := l.Client.(slashcommandv1.SlashCommandServiceClient)
	return c, ok
}

// HookClient returns this plugin's HookSubscriberService client, dialed
// over the very connection its category client came from
// (agent-loop/hook-dispatch.md#wire-contract--pluggableharnesshookv1), by
// delegating to the underlying pluginruntime.Plugin. ok is false only for
// a Live that never came from a real launch.
func (l *Live) HookClient() (hookv1.HookSubscriberServiceClient, bool) {
	if l.plugin == nil {
		return nil, false
	}
	return l.plugin.HookClient()
}

// Registry is the live plugin table every later lookup goes through.
// Construct with NewRegistry; the zero value is not usable. Safe for
// concurrent use.
//
// A sync.RWMutex guards it because the read side is the hot path — a
// turn resolves handles for every model call, tool call, and context
// contribution — while the write side happens only at startup, in one
// goroutine, from Supervisor.Start.
type Registry struct {
	mu      sync.RWMutex
	byKey   map[Key]*Live
	byLocal map[string]*Live
	order   []*Live
}

// NewRegistry returns an empty, ready-to-use Registry.
func NewRegistry() *Registry {
	return &Registry{
		byKey:   make(map[Key]*Live),
		byLocal: make(map[string]*Live),
	}
}

// Add registers l under both its {category, name} key and its agent.hcl
// local name, appending it to launch order. A key or local name already
// present is ErrDuplicateKey — never a silent overwrite, since the second
// registration would otherwise make the first plugin permanently
// unreachable while its subprocess kept running.
func (r *Registry) Add(l *Live) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := Key{Category: l.Producer.GetCategory(), Name: l.Producer.GetName()}
	if existing, ok := r.byKey[key]; ok {
		return fmt.Errorf("%w: %s/%s declared as both %q and %q",
			ErrDuplicateKey, key.Category, key.Name, existing.LocalName, l.LocalName)
	}
	if _, ok := r.byLocal[l.LocalName]; ok {
		return fmt.Errorf("%w: local name %q registered twice", ErrDuplicateKey, l.LocalName)
	}

	r.byKey[key] = l
	r.byLocal[l.LocalName] = l
	r.order = append(r.order, l)
	return nil
}

// ByKey resolves a plugin by its self-reported {category, name} identity.
func (r *Registry) ByKey(k Key) (*Live, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	l, ok := r.byKey[k]
	return l, ok
}

// ByLocalName resolves a plugin by its agent.hcl required_providers local
// name — the name a provider{} block, an agent_profile's model{}/tools,
// and a hook{} block all use.
func (r *Registry) ByLocalName(name string) (*Live, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	l, ok := r.byLocal[name]
	return l, ok
}

// ByCategory returns every loaded plugin of category c, in launch order.
// Returns an empty (non-nil) slice when none are loaded.
func (r *Registry) ByCategory(c commonv1.Category) []*Live {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]*Live, 0, len(r.order))
	for _, l := range r.order {
		if l.Producer.GetCategory() == c {
			out = append(out, l)
		}
	}
	return out
}

// All returns every loaded plugin in launch order — the same
// providerresolve.Order sequence hook dispatch walks forward and
// shutdown walks backward. The returned slice is a copy, so a caller
// cannot reorder the registry by sorting it.
func (r *Registry) All() []*Live {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]*Live, len(r.order))
	copy(out, r.order)
	return out
}
