package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/hashicorp/hcl/v2"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/pluggableharness/agent/internal/config"
	"github.com/pluggableharness/agent/internal/eventbus"
	"github.com/pluggableharness/agent/internal/kernelcallback"
	"github.com/pluggableharness/agent/internal/log"
	"github.com/pluggableharness/agent/internal/pluginruntime"
	"github.com/pluggableharness/agent/internal/providerresolve"
	"github.com/pluggableharness/agent/internal/registry"
	"github.com/pluggableharness/agent/internal/sessionscope"
	"github.com/pluggableharness/agent/internal/sessionstate"
	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetryrelay"
	"github.com/pluggableharness/agent/internal/tokencount"
	"github.com/pluggableharness/agent/pkg/common"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
)

// Sentinel errors for an invalid Config, checked by NewSupervisor before
// anything is launched.
var (
	ErrMissingRegistry  = errors.New("pluginhost: config: registry is required")
	ErrMissingBus       = errors.New("pluginhost: config: event bus is required")
	ErrMissingTelemetry = errors.New("pluginhost: config: telemetry provider is required")
	ErrMissingRelay     = errors.New("pluginhost: config: telemetry relay is required")
	ErrMissingLog       = errors.New("pluginhost: config: log server is required")
	ErrMissingScopes    = errors.New("pluginhost: config: session-grant registry is required")
	ErrMissingSessions  = errors.New("pluginhost: config: live-session table is required")
	ErrMissingTokens    = errors.New("pluginhost: config: token counter is required")
)

// ErrIdentityMismatch reports a plugin whose Describe response
// contradicts the lock-file row it was resolved from. The lock file is
// the source of truth for what is allowed to run (configuration.md §11),
// so a binary claiming to be something else is a hard startup error, not
// a warning.
var ErrIdentityMismatch = errors.New("pluginhost: describe contradicts the lock file")

// ErrCategoryProbeFailed reports a dev-override binary that answered
// Describe on none of the seven categories.
var ErrCategoryProbeFailed = errors.New("pluginhost: dev override answered Describe on no category")

// defaultShutdownTimeout bounds the whole teardown pass. Each plugin's
// own drain window is internal/pluginruntime's concern (a hardcoded 5s
// there); this is the outer bound across all of them, generous enough
// that a handful of plugins each draining fully still finishes inside it
// rather than being truncated by the very deadline meant to protect
// against a single hung subprocess.
const defaultShutdownTimeout = 30 * time.Second

// Config bundles everything a Supervisor needs. The telemetry, event
// bus, log, and relay dependencies are process-wide singletons shared by
// every plugin's kernel-callback server; only identity and resolved
// config are per-plugin, and those this package derives itself (see
// callbackSlot).
type Config struct {
	// Resolved is internal/providerresolve.Resolve's output, already in
	// the declaration order launches must follow. MAY be empty.
	Resolved []providerresolve.Resolved

	// Registry receives every successfully brought-up plugin. MUST be
	// set. Supplied by the caller rather than created here so the caller
	// can hold the read side without holding the supervisor.
	Registry *Registry

	// Bus is the process-wide event bus every plugin's Publish/Subscribe
	// callbacks operate against (event-bus.md). MUST be set.
	Bus *eventbus.Bus

	// Telemetry is the kernel's telemetry provider: the bring-up span
	// per provider, and the per-plugin kernel-callback server's own
	// instrumentation. MUST be set.
	Telemetry *telemetry.Provider

	// TelemetryRelay uploads plugin-relayed span batches
	// (observability.md#the-relay-model). MUST be set.
	TelemetryRelay *telemetryrelay.Relay

	// Log is the wrapped internal/log.Server every plugin's Log callback
	// delegates to. MUST be set.
	Log *log.Server

	// Scopes is the process-wide session-grant registry
	// (internal/sessionscope), wired into every plugin's kernel-callback
	// server so its session-scoped RPCs (Emit, ReadEvents, GetSession) can
	// authorize a call against the session it names. MUST be set.
	Scopes *sessionscope.Registry

	// Sessions is the process-wide live-session table
	// (internal/sessionstate), wired into every plugin's kernel-callback
	// server alongside Scopes — authorization alone isn't enough to serve
	// Emit/ReadEvents/GetSession; the RPC also needs the live session
	// object a granted call is authorized against. MUST be set.
	Sessions *sessionstate.Table

	// Tokens is the kernel's single token-counting primitive
	// (internal/tokencount), wired into every plugin's kernel-callback
	// server for CountTokens. MUST be set.
	Tokens *tokencount.Counter

	// ProviderBodies is config.Config.ProviderBodies — each provider{}
	// block's raw, undecoded HCL body, keyed by local name. A local name
	// with no entry is configured with an empty body, which is the
	// ordinary case for a provider that takes no config.
	ProviderBodies map[string]hcl.Body

	// BusSubscribeQueueBound is the per-Subscribe-stream backpressure
	// bound passed through to every plugin's kernel-callback server
	// (configuration/blocks-reference.md#event_bus). A value <= 0 leaves
	// internal/kernelcallback's own default in force.
	BusSubscribeQueueBound int

	// Logger receives this package's own DEBUG/INFO/ERROR lines and is
	// handed to every launched plugin's runtime and callback server.
	// Defaults to slog.Default() when nil.
	Logger *slog.Logger
}

// validate checks the dependencies Start cannot proceed without.
func (c Config) validate() error {
	switch {
	case c.Registry == nil:
		return ErrMissingRegistry
	case c.Bus == nil:
		return ErrMissingBus
	case c.Telemetry == nil:
		return ErrMissingTelemetry
	case c.TelemetryRelay == nil:
		return ErrMissingRelay
	case c.Log == nil:
		return ErrMissingLog
	case c.Scopes == nil:
		return ErrMissingScopes
	case c.Sessions == nil:
		return ErrMissingSessions
	case c.Tokens == nil:
		return ErrMissingTokens
	default:
		return nil
	}
}

// Supervisor owns the lifecycle of every launched plugin subprocess:
// Start brings them all up in order, Shutdown tears them all down in
// reverse. It is driven by one goroutine; Registry, not Supervisor, is
// the concurrent read side.
type Supervisor struct {
	cfg    Config
	logger *slog.Logger

	// launch spawns one provider's subprocess. It is spawnSubprocess for
	// every real Supervisor, held as a field so the rest of the bring-up
	// sequence — Describe, reconcile, schema fetch, config decode, the
	// slot install, Configure, register, and every failure path through
	// them — is unit-testable against an in-process gRPC client instead
	// of requiring a real subprocess for each case. Same
	// factor-for-testability move internal/pluginruntime's own
	// closeWithKill makes.
	launch launchFunc

	// mu guards launched and shutDown, which Shutdown may be called
	// against concurrently with — or twice after — a Start that failed.
	mu       sync.Mutex
	launched []*Live
	shutDown bool
}

// launched is one successfully launched subprocess, in the terms the
// rest of the bring-up sequence needs it: the dispensed category client,
// the runtime handle a Live keeps for its hook client, and the teardown
// function.
type launchedPlugin struct {
	client  any
	runtime *pluginruntime.Plugin
	close   func(context.Context) error
}

// launchFunc spawns one provider's subprocess, serving slot on its
// callback broker.
type launchFunc func(ctx context.Context, resolved providerresolve.Resolved, slot *callbackSlot) (*launchedPlugin, error)

// NewSupervisor validates cfg and returns a Supervisor ready to Start.
func NewSupervisor(cfg Config) (*Supervisor, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	s := &Supervisor{cfg: cfg, logger: logger}
	s.launch = s.spawnSubprocess
	return s, nil
}

// Start brings up every resolved provider, in order, all-or-nothing.
//
// Each provider runs the same sequence: build its late-bound callback
// slot, launch the subprocess, Describe it and reconcile that identity
// against the lock file, verify the binary's checksum, fetch its
// capability advertisement, decode its provider{} block against the
// ConfigSchema that advertisement carried, install the real identity and
// decoded config into the slot, Configure, and register.
//
// A failure anywhere tears down every provider already launched, in
// reverse order, before returning — a half-started kernel is never
// handed to a session.
func (s *Supervisor) Start(ctx context.Context) error {
	for i, resolved := range s.cfg.Resolved {
		if err := s.startOne(ctx, i, resolved); err != nil {
			// The teardown error is deliberately swallowed rather than
			// joined: the caller needs to act on why startup failed, and
			// go-style.md forbids logging and returning the same error,
			// so the one that IS swallowed is the one logged.
			if teardownErr := s.Shutdown(ctx); teardownErr != nil {
				s.logger.ErrorContext(ctx, "pluginhost: teardown after failed start",
					"provider", resolved.LocalName, "error", teardownErr)
			}
			return err
		}
	}
	s.logger.InfoContext(ctx, "pluginhost: all providers started", "count", len(s.cfg.Resolved))
	return nil
}

// startOne runs one provider's whole bring-up sequence.
func (s *Supervisor) startOne(ctx context.Context, index int, resolved providerresolve.Resolved) (err error) {
	ctx, span := s.cfg.Telemetry.StartProviderBringUp(ctx, resolved.LocalName, categoryText(resolved.Category))
	defer func() { telemetry.EndSpan(span, err) }()

	s.logger.DebugContext(ctx, "pluginhost: starting provider",
		"provider", resolved.LocalName, "binary", resolved.BinaryPath, "dev_override", resolved.ViaDevOverride)

	// Step 1: the late-bound identity/config slot, serving a provisional
	// server until Describe and the config decode supply the real one.
	slot := newCallbackSlot(s.newCallbackServer(provisionalProducer(resolved), nil))

	// Step 2: launch. A resolved category launches directly; an unknown
	// one (always a dev override) is probed.
	plugin, err := s.launch(ctx, resolved, slot)
	if err != nil {
		return err
	}
	client := plugin.client
	defer func() {
		if err != nil {
			s.closePlugin(ctx, resolved.LocalName, plugin.close)
		}
	}()

	// Step 3: Describe, reconciled against the lock file.
	producer, err := describeProducer(ctx, client)
	if err != nil {
		return fmt.Errorf("pluginhost: %s: %w", resolved.LocalName, err)
	}
	if err = reconcile(resolved, producer); err != nil {
		return fmt.Errorf("pluginhost: %s: %w", resolved.LocalName, err)
	}

	// Step 4: checksum verification, skipped for a dev override — which
	// has no lock row to verify against by design
	// (settings-and-global.md#dev_overrides).
	if resolved.Locked != nil {
		if err = registry.VerifyChecksum(ctx, s.cfg.Telemetry, resolved.BinaryPath, resolved.Platform, *resolved.Locked); err != nil {
			return fmt.Errorf("pluginhost: %s: %w", resolved.LocalName, err)
		}
	}

	// Step 5: the capability advertisement, and with it the ConfigSchema.
	capabilities, schema, err := fetchCapabilities(ctx, client)
	if err != nil {
		return fmt.Errorf("pluginhost: %s: %w", resolved.LocalName, err)
	}

	// Step 6: decode this provider's own provider{} block against that
	// schema — the deferred half of internal/config's chicken-and-egg
	// (a ConfigSchema only exists once the plugin is running).
	decoded, err := config.DecodeProviderConfig(s.providerBody(resolved.LocalName), schema)
	if err != nil {
		return fmt.Errorf("pluginhost: %s: %w", resolved.LocalName, err)
	}

	// Step 7: install the real identity and config BEFORE Configure.
	// kernel-callbacks.md permits a plugin to call GetConfig or Log from
	// inside its own Configure handler, so this ordering is load-bearing,
	// not tidiness.
	slot.set(s.newCallbackServer(producer, decoded))

	// Step 8: Configure, then register.
	if err = configurePlugin(ctx, client, decoded); err != nil {
		return fmt.Errorf("pluginhost: %s: %w", resolved.LocalName, err)
	}

	live := &Live{
		LocalName:    resolved.LocalName,
		Producer:     producer,
		Client:       client,
		Capabilities: capabilities,
		ConfigSchema: schema,
		LaunchIndex:  index,
		plugin:       plugin.runtime,
		closeFn:      plugin.close,
	}
	if err = s.cfg.Registry.Add(live); err != nil {
		return err
	}

	s.mu.Lock()
	s.launched = append(s.launched, live)
	s.mu.Unlock()

	s.logger.InfoContext(ctx, "pluginhost: provider started",
		"provider", resolved.LocalName,
		"producer_category", producer.GetCategory().String(),
		"producer_name", producer.GetName(),
		"producer_version", producer.GetVersion(),
		"launch_index", index)
	return nil
}

// spawnSubprocess is the real launchFunc: a provider whose category the
// lock file already recorded launches once; a dev override, whose
// category is only knowable from a live Describe, is probed.
func (s *Supervisor) spawnSubprocess(ctx context.Context, resolved providerresolve.Resolved, slot *callbackSlot) (*launchedPlugin, error) {
	if resolved.Category != commonv1.Category_CATEGORY_UNSPECIFIED {
		plugin, err := pluginruntime.Launch(ctx, s.launchConfig(resolved, resolved.Category, slot))
		if err != nil {
			return nil, fmt.Errorf("pluginhost: %s: launch: %w", resolved.LocalName, err)
		}
		return newLaunchedPlugin(plugin), nil
	}
	return s.probe(ctx, resolved, slot)
}

// newLaunchedPlugin adapts a runtime handle to the shape the bring-up
// sequence consumes.
func newLaunchedPlugin(plugin *pluginruntime.Plugin) *launchedPlugin {
	return &launchedPlugin{client: plugin.Dispensed(), runtime: plugin, close: plugin.Close}
}

// probe finds a dev-override binary's real category by launching it once
// per candidate category, in probeCategories' fixed order, and keeping
// the first launch whose Describe answers.
//
// This costs up to seven subprocess launches, and only ever for a
// dev-override provider — see this package's CLAUDE.md for why it is a
// sequence of single-category launches rather than one launch keyed by
// all seven categories at once.
func (s *Supervisor) probe(ctx context.Context, resolved providerresolve.Resolved, slot *callbackSlot) (*launchedPlugin, error) {
	for _, category := range probeCategories {
		plugin, err := pluginruntime.Launch(ctx, s.launchConfig(resolved, category, slot))
		if err != nil {
			s.logger.DebugContext(ctx, "pluginhost: category probe: launch failed",
				"provider", resolved.LocalName, "category", common.PluginKey(category), "error", err)
			continue
		}
		if _, err := describeProducer(ctx, plugin.Dispensed()); err != nil {
			s.logger.DebugContext(ctx, "pluginhost: category probe: not this category",
				"provider", resolved.LocalName, "category", common.PluginKey(category), "error", err)
			s.closePlugin(ctx, resolved.LocalName, plugin.Close)
			continue
		}
		s.logger.DebugContext(ctx, "pluginhost: category probe: matched",
			"provider", resolved.LocalName, "category", common.PluginKey(category))
		return newLaunchedPlugin(plugin), nil
	}
	return nil, fmt.Errorf("pluginhost: %s: %w: %s", resolved.LocalName, ErrCategoryProbeFailed, resolved.BinaryPath)
}

// launchConfig builds the internal/pluginruntime.Config for one launch of
// resolved under category, serving slot on the callback broker.
func (s *Supervisor) launchConfig(resolved providerresolve.Resolved, category commonv1.Category, slot *callbackSlot) pluginruntime.Config {
	producer := provisionalProducer(resolved)
	producer.Category = category
	return pluginruntime.Config{
		BinaryPath: resolved.BinaryPath,
		Producer:   producer,
		Callback:   slot,
		Telemetry:  s.cfg.Telemetry,
		Logger:     s.logger,
	}
}

// newCallbackServer builds one plugin's kernel-callback server, binding
// the process-wide singletons alongside that plugin's own identity and
// resolved config (internal/kernelcallback's "one Server per plugin
// instance" design). Scopes/Sessions/Tokens are the same process-wide
// singletons shared across every launched plugin's server — only
// Producer/ResolvedConfig are per-plugin.
func (s *Supervisor) newCallbackServer(producer *commonv1.ProducerRef, resolvedConfig *structpb.Struct) *kernelcallback.Server {
	return kernelcallback.NewServer(kernelcallback.Config{
		Log:                    s.cfg.Log,
		Producer:               producer,
		Telemetry:              s.cfg.Telemetry,
		TelemetryRelay:         s.cfg.TelemetryRelay,
		Bus:                    s.cfg.Bus,
		BusSubscribeQueueBound: s.cfg.BusSubscribeQueueBound,
		ResolvedConfig:         resolvedConfig,
		Scopes:                 s.cfg.Scopes,
		Sessions:               s.cfg.Sessions,
		Tokens:                 s.cfg.Tokens,
		Logger:                 s.logger,
	})
}

// providerBody returns the raw provider{} body declared for name, or an
// empty body when none was — a provider that takes no config is the
// ordinary case, not an error (blocks-reference.md's provider{} block is
// optional).
func (s *Supervisor) providerBody(name string) hcl.Body {
	if body, ok := s.cfg.ProviderBodies[name]; ok && body != nil {
		return body
	}
	return hcl.EmptyBody()
}

// closePlugin tears one subprocess down on a failure path, logging
// rather than returning any teardown error — the caller is already
// returning the failure that made teardown necessary.
func (s *Supervisor) closePlugin(ctx context.Context, localName string, closeFn func(context.Context) error) {
	if closeFn == nil {
		return
	}
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultShutdownTimeout)
	defer cancel()
	if err := closeFn(closeCtx); err != nil {
		s.logger.ErrorContext(ctx, "pluginhost: closing plugin after a failed bring-up",
			"provider", localName, "error", err)
	}
}

// Shutdown tears every launched plugin down in reverse LaunchIndex
// order — the reverse of hook-dispatch order, so a plugin never
// outlives one that may still call into it.
//
// The whole pass runs under its own deadline over
// context.WithoutCancel(ctx), because shutdown is normally reached
// precisely because ctx was already canceled; inheriting that
// cancellation would turn every graceful drain into an immediate kill.
//
// One plugin failing to close does not abort the rest: every teardown is
// attempted and the failures are returned joined. Safe after a partially
// failed Start, and safe to call twice — the second call is a no-op.
func (s *Supervisor) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	if s.shutDown {
		s.mu.Unlock()
		return nil
	}
	s.shutDown = true
	launched := s.launched
	s.launched = nil
	s.mu.Unlock()

	if len(launched) == 0 {
		return nil
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultShutdownTimeout)
	defer cancel()

	s.logger.DebugContext(shutdownCtx, "pluginhost: shutting down", "count", len(launched))

	var errs []error
	for i := len(launched) - 1; i >= 0; i-- {
		live := launched[i]
		if live.closeFn == nil {
			continue
		}
		if err := live.closeFn(shutdownCtx); err != nil {
			// Logged as well as collected because this error is the one
			// case where continuing past a failure is the point: the
			// joined return value is what the caller sees, but the
			// per-plugin attribution only exists here.
			s.logger.ErrorContext(shutdownCtx, "pluginhost: plugin shutdown failed",
				"provider", live.LocalName, "launch_index", live.LaunchIndex, "error", err)
			errs = append(errs, fmt.Errorf("pluginhost: %s: shutdown: %w", live.LocalName, err))
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	s.logger.InfoContext(shutdownCtx, "pluginhost: shutdown complete", "count", len(launched))
	return nil
}

// provisionalProducer builds the identity a plugin is launched under,
// before its own Describe has spoken. Name is the agent.hcl local name
// because nothing else is known yet: required_providers records a source
// and a version constraint, and the lock file records a source, version,
// and category — none of them the plugin's own published name. Describe
// replaces this wholesale.
func provisionalProducer(resolved providerresolve.Resolved) *commonv1.ProducerRef {
	return &commonv1.ProducerRef{
		Name:     resolved.LocalName,
		Version:  resolved.Version,
		Source:   resolved.Source,
		Category: resolved.Category,
	}
}

// reconcile checks a plugin's self-reported identity against the lock
// row it was resolved from. Source, version, and category are each
// checked only when the lock file actually records one — category is an
// optional field, and a dev override has no lock row at all, in which
// case Describe is the sole authority by design.
//
// The lock file records no published name, so there is deliberately
// nothing to compare Producer.Name against: required_providers' local
// name is the operator's label, explicitly permitted to differ from the
// plugin's own name (blocks-reference.md#required_providers).
func reconcile(resolved providerresolve.Resolved, producer *commonv1.ProducerRef) error {
	if resolved.Locked == nil {
		return nil
	}
	locked := *resolved.Locked
	if got := producer.GetSource(); got != "" && locked.Source != "" && got != locked.Source {
		return fmt.Errorf("%w: source: describe says %q, lock file says %q", ErrIdentityMismatch, got, locked.Source)
	}
	if got := producer.GetVersion(); got != "" && locked.Version != "" && got != locked.Version {
		return fmt.Errorf("%w: version: describe says %q, lock file says %q", ErrIdentityMismatch, got, locked.Version)
	}
	if resolved.Category != commonv1.Category_CATEGORY_UNSPECIFIED && producer.GetCategory() != resolved.Category {
		return fmt.Errorf("%w: category: describe says %q, lock file says %q",
			ErrIdentityMismatch, common.PluginKey(producer.GetCategory()), common.PluginKey(resolved.Category))
	}
	return nil
}

// categoryText renders a category for a span attribute, returning an
// empty string for CATEGORY_UNSPECIFIED so an unknown category reads as
// absent rather than as a literal "unspecified" value.
func categoryText(c commonv1.Category) string {
	if c == commonv1.Category_CATEGORY_UNSPECIFIED {
		return ""
	}
	return common.PluginKey(c)
}
