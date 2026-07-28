package kernel

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"time"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"

	"github.com/pluggableharness/agent/internal/config"
	"github.com/pluggableharness/agent/internal/eventbus"
	"github.com/pluggableharness/agent/internal/hookdispatch"
	"github.com/pluggableharness/agent/internal/kernelcallback"
	"github.com/pluggableharness/agent/internal/log"
	"github.com/pluggableharness/agent/internal/metadata"
	"github.com/pluggableharness/agent/internal/pending"
	"github.com/pluggableharness/agent/internal/plugincache"
	"github.com/pluggableharness/agent/internal/pluginhost"
	catalogplugin "github.com/pluggableharness/agent/internal/providercatalog/drivers/plugin"
	"github.com/pluggableharness/agent/internal/providerresolve"
	"github.com/pluggableharness/agent/internal/registry"
	"github.com/pluggableharness/agent/internal/sessionscope"
	"github.com/pluggableharness/agent/internal/sessionstate"
	"github.com/pluggableharness/agent/internal/statebackend"
	"github.com/pluggableharness/agent/internal/telemetry"
	telemetrydrivers "github.com/pluggableharness/agent/internal/telemetry/drivers"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/noop"
	"github.com/pluggableharness/agent/internal/telemetryrelay"
	"github.com/pluggableharness/agent/internal/tokencount"
	"github.com/pluggableharness/agent/internal/xdg"
)

// bringUp constructs every process-wide dependency in dependency order.
//
// It always returns a non-nil *kernel, populated as far as it got, so the
// caller can run the same phased shutdown over a partial bring-up as over
// a complete one. The first failure stops the sequence.
func bringUp(ctx context.Context, opts Options) (*kernel, error) {
	k := &kernel{
		opts:   opts,
		logger: slog.Default(),
		sink:   &sessionSink{},
	}

	paths, err := xdg.Resolve(opts.WorkingDirectory)
	if err != nil {
		return k, fmt.Errorf("kernel: resolve paths: %w", err)
	}
	k.paths = paths

	if err := k.loadConfig(ctx); err != nil {
		return k, err
	}
	if err := k.startTelemetry(ctx); err != nil {
		return k, err
	}
	if err := k.startLogging(); err != nil {
		return k, err
	}
	if err := k.openStores(ctx); err != nil {
		return k, err
	}
	if err := k.startPlugins(ctx); err != nil {
		return k, err
	}
	if err := k.buildHooks(ctx); err != nil {
		return k, err
	}
	if err := k.startFrontends(ctx); err != nil {
		return k, err
	}
	return k, nil
}

// isFrontend is the category predicate that splits bring-up into its two
// Configure passes.
func isFrontend(c commonv1.Category) bool {
	return c == commonv1.Category_CATEGORY_FRONTEND
}

// startFrontends closes the loop bringUp's phasing exists for: build the
// session runner, install the frontend host behind it, and only then let
// frontend plugins run their Configure handlers.
//
// A frontend calls CreateSession from inside Configure, so every
// collaborator that call reaches — the catalog, the hook chains, the
// runner, the host slot — has to be live before this point. Everything
// above in bringUp is that prerequisite, in dependency order.
//
// In -prompt mode the frontend pass is skipped entirely. A frontend's
// Configure is what makes it seize the terminal and start a session of
// its own, and a non-interactive run has neither a terminal to give away
// nor anything for a second session to do. Frontends are still prepared,
// so they appear in the registry and the catalog exactly as any other
// provider — only the step with side effects is withheld.
func (k *kernel) startFrontends(ctx context.Context) error {
	runner, err := k.newRunner(ctx)
	if err != nil {
		return err
	}
	k.runner = runner
	k.hostSlot.Set(newFrontendHost(k, runner, k.plans, k.inter))

	if k.opts.Prompt != "" {
		k.logger.DebugContext(ctx, "kernel: non-interactive run, frontends left unconfigured")
		return nil
	}
	if err := k.supervisor.Configure(ctx, isFrontend); err != nil {
		return fmt.Errorf("kernel: start frontends: %w", err)
	}
	return nil
}

// loadConfig parses agent.hcl under a throwaway, fully-disabled telemetry
// Provider: config.LoadFile requires one, and the real Provider's own
// configuration is inside the file being loaded.
func (k *kernel) loadConfig(ctx context.Context) error {
	boot, err := telemetry.New(ctx, telemetry.Config{}, noop.New(), nil)
	if err != nil {
		return fmt.Errorf("kernel: bootstrap telemetry: %w", err)
	}
	k.bootTelem = boot

	path := k.opts.ConfigPath
	if path == "" {
		path = k.paths.ProjectConfig
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("kernel: no config file at %s: an agent.hcl is required to declare a model provider and an agent profile", path)
		}
		return fmt.Errorf("kernel: config file %s: %w", path, err)
	}

	cfg, err := config.LoadFile(ctx, boot, path)
	if err != nil {
		return fmt.Errorf("kernel: load config: %w", err)
	}
	k.cfg = cfg
	return nil
}

// startTelemetry replaces the bootstrap Provider with the real one the
// loaded config describes, then shuts the bootstrap one down.
//
// The settings.telemetry = false switch is not re-implemented here:
// config.TelemetryConfig already forces the noop driver in that case
// (configuration/settings-and-global.md#the-telemetry-switch), so exactly
// one place in the tree decides it.
func (k *kernel) startTelemetry(ctx context.Context) error {
	cfg := config.TelemetryConfig(k.cfg.Settings)

	backend, err := telemetrydrivers.New(cfg.Backend, cfg)
	if err != nil {
		return fmt.Errorf("kernel: telemetry backend: %w", err)
	}
	prov, err := telemetry.New(ctx, cfg, backend, statebackend.KernelProducer())
	if err != nil {
		return fmt.Errorf("kernel: telemetry: %w", err)
	}
	k.telem = prov

	// The bootstrap Provider has done its one job. It is discarded here
	// rather than in shutdown so a long session is not holding two
	// Providers open for its whole life.
	if err := k.bootTelem.Shutdown(ctx); err != nil {
		return fmt.Errorf("kernel: bootstrap telemetry shutdown: %w", err)
	}
	k.bootTelem = nil

	uploader, err := backend.TraceUploader(ctx)
	if err != nil {
		return fmt.Errorf("kernel: telemetry relay: %w", err)
	}
	k.relay = telemetryrelay.New(uploader)
	return nil
}

// startLogging builds this process's slog handler and installs it as the
// default.
//
// slog.SetDefault is a global mutation, and this is the one sanctioned
// place for it (.claude/rules/go-style.md): every internal/ package below
// falls back to slog.Default() when constructed without an explicit
// logger, so installing it here is what makes those fallbacks land on the
// operator's configured level and destination rather than on stdlib's.
func (k *kernel) startLogging() error {
	name := k.opts.LogLevel
	if name == "" {
		name = k.cfg.Settings.LogLevel
	}
	if name == "" {
		name = DefaultLogLevel
	}
	level, err := log.ParseLevel(name)
	if err != nil {
		return fmt.Errorf("kernel: log level %q: %w", name, err)
	}

	handler := newFanoutHandler(
		slog.NewTextHandler(k.opts.Stderr, log.HandlerOptions(level)),
		k.otelLogHandler(),
	)
	k.logger = slog.New(handler)
	slog.SetDefault(k.logger)
	return nil
}

// otelLogHandler returns the OTel logs-bridge handler when the operator
// enabled the logs signal, or nil when they did not. A nil handler is
// dropped by newFanoutHandler, so a telemetry-off kernel logs to stderr
// and nowhere else.
func (k *kernel) otelLogHandler() slog.Handler {
	cfg := k.telem.Config()
	if !cfg.Enabled || !cfg.LogsEnabled {
		return nil
	}
	return k.telem.SlogHandler("github.com/pluggableharness/agent/internal/kernel")
}

// openStores brings up the process-wide state and messaging layer, plus
// the three registries the plugin supervisor and its per-plugin callback
// servers read and write.
//
// The contextcheck suppressions below are on constructors that take no
// context at all by design. The linter reaches them through each one's
// *nil-telemetry fallback*, which builds a throwaway Provider over
// context.Background — a branch none of these calls can take, because
// every one is passed a live k.telem. Nothing drops ctx here.
func (k *kernel) openStores(ctx context.Context) error {
	store, err := statebackend.NewStore(k.paths.SessionsDir, //nolint:contextcheck // see this function's note on the constructors' nil-telemetry fallback
		statebackend.WithLogger(k.logger),
		statebackend.WithTelemetry(k.telem))
	if err != nil {
		return fmt.Errorf("kernel: state backend: %w", err)
	}
	k.store = store

	k.bus = eventbus.New(eventbus.WithLogger(k.logger), eventbus.WithTelemetry(k.telem)) //nolint:contextcheck // same
	k.logSrv = log.NewServer(k.logger)
	k.scopes = sessionscope.NewRegistry()
	k.sessions = sessionstate.NewTable()
	k.plugins = pluginhost.NewRegistry()
	k.tokens = tokencount.NewCounter(k.plugins, k.telem, k.logger)
	k.metadata = metadata.NewStore()
	k.deltas = kernelcallback.NewDeltaHub()
	k.hostSlot = &kernelcallback.HostSlot{}
	k.plans = pending.NewPlanBridge()
	k.inter = pending.NewInteractiveBridge()

	k.logger.DebugContext(ctx, "kernel: stores open",
		"sessions_dir", k.paths.SessionsDir,
		"plugin_cache_dir", k.paths.PluginCacheDir)
	return nil
}

// startPlugins resolves every required_providers entry to a launchable
// binary and brings the whole set up.
//
// There is deliberately no download or install path here: this build
// resolves through dev_overrides or an existing lock file plus a cached
// binary, and reports everything missing in one actionable error rather
// than reaching for the network. Installing what providerresolve reports
// is a separate, later phase.
func (k *kernel) startPlugins(ctx context.Context) error {
	global, err := k.loadGlobalConfig(ctx)
	if err != nil {
		return err
	}
	lock, err := k.loadLockFile(ctx)
	if err != nil {
		return err
	}

	resolved, err := providerresolve.Resolve(ctx, providerresolve.Input{
		Config:   k.cfg,
		Lock:     lock,
		Global:   global,
		CacheDir: k.paths.PluginCacheDir,
		Platform: plugincache.Platform(),
		Logger:   k.logger,
	})
	if err != nil {
		// A *providerresolve.MissingError already names every
		// unresolvable provider, its source, its constraint, and why it
		// could not be resolved. Wrapping preserves errors.As for a
		// caller that wants the structured list.
		return fmt.Errorf("kernel: resolve providers: %w", err)
	}

	sup, err := pluginhost.NewSupervisor(pluginhost.Config{
		Resolved:               resolved,
		Registry:               k.plugins,
		Bus:                    k.bus,
		Telemetry:              k.telem,
		TelemetryRelay:         k.relay,
		Log:                    k.logSrv,
		Scopes:                 k.scopes,
		Sessions:               k.sessions,
		Tokens:                 k.tokens,
		Metadata:               k.metadata,
		Deltas:                 k.deltas,
		HostSlot:               k.hostSlot,
		ProviderBodies:         k.cfg.ProviderBodies,
		ProviderEnv:            k.cfg.ProviderEnv,
		BusSubscribeQueueBound: k.cfg.Settings.EventBus.SubscribeQueueBound,
		Logger:                 k.logger,
	})
	if err != nil {
		return fmt.Errorf("kernel: plugin supervisor: %w", err)
	}
	// Recorded before Start so shutdown tears down a partially-started
	// set: Supervisor.Start is what launches subprocesses, and a failure
	// halfway through it leaves earlier plugins running.
	k.supervisor = sup

	// Two phases, deliberately. Prepare launches, describes, and
	// registers every provider — which is what makes each one's real
	// category known, including a dev override's, whose lock file has
	// none. Configure then runs for everything except frontends, so the
	// catalog below is built over configured tool and model plugins
	// while frontends are still holding at their Configure handler. The
	// frontend pass runs from startFrontends, once the host they call
	// back into exists.
	if err := sup.Prepare(ctx); err != nil {
		return fmt.Errorf("kernel: start plugins: %w", err)
	}
	if err := sup.Configure(ctx, func(c commonv1.Category) bool { return !isFrontend(c) }); err != nil {
		return fmt.Errorf("kernel: start plugins: %w", err)
	}

	k.catalog = catalogplugin.New(ctx, catalogplugin.Config{
		Registry:  k.plugins,
		Telemetry: k.telem,
		Logger:    k.logger,
	})
	return nil
}

// loadGlobalConfig reads $XDG_CONFIG_HOME/agent/config.hcl, tolerating its
// absence: dev_overrides and registry mirrors are per-user opt-ins, and a
// machine with neither has no file at all.
func (k *kernel) loadGlobalConfig(ctx context.Context) (*registry.GlobalConfig, error) {
	if !fileExists(k.paths.GlobalConfig) {
		k.logger.DebugContext(ctx, "kernel: no global config", "path", k.paths.GlobalConfig)
		return nil, nil //nolint:nilnil // providerresolve.Input.Global documents nil as "no global config", not an error
	}
	global, err := registry.LoadGlobalConfig(ctx, k.telem, k.paths.GlobalConfig)
	if err != nil {
		return nil, fmt.Errorf("kernel: load global config: %w", err)
	}
	return global, nil
}

// loadLockFile reads .agent/agent.lock.hcl, tolerating its absence: a
// fresh checkout has no lock file, and providerresolve reports every
// provider that consequently cannot resolve.
func (k *kernel) loadLockFile(ctx context.Context) (*registry.LockFile, error) {
	if !fileExists(k.paths.LockFile) {
		k.logger.DebugContext(ctx, "kernel: no lock file", "path", k.paths.LockFile)
		return nil, nil //nolint:nilnil // providerresolve.Input.Lock documents nil as "no lock file", not an error
	}
	lock, err := registry.LoadLockFile(ctx, k.telem, k.paths.LockFile)
	if err != nil {
		return nil, fmt.Errorf("kernel: load lock file: %w", err)
	}
	return lock, nil
}

// fileExists reports whether path names an existing file. A stat error
// other than "does not exist" (a permission problem, a broken symlink) is
// deliberately treated as absent here: the loader that follows opens the
// path itself and reports the real reason, rather than this helper
// guessing at one.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// buildHooks assembles the hook-subscription chains and the one Dispatcher
// every hook point in the process dispatches through.
//
// implicit is empty, deliberately. No category-to-hook-point derivation
// table exists anywhere in this codebase or in any spec table that could
// be cited, and internal/hookdispatch's own Implicit doc comment refuses
// to invent one. Only explicit hook{} blocks from agent.hcl subscribe in
// this build — see this package's CLAUDE.md.
func (k *kernel) buildHooks(ctx context.Context) error {
	reg, err := hookdispatch.NewRegistry(
		k.catalog,
		nil,
		k.cfg.Hooks,
		k.cfg.ProviderRanges,
		hookTimeout(k.cfg.Settings.DefaultHookTimeoutMS),
	)
	if err != nil {
		return fmt.Errorf("kernel: hook registry: %w", err)
	}
	k.hookReg = reg
	k.hooks = hookdispatch.New(reg, k.sink, k.telem, k.logger, hookdispatch.Options{}) //nolint:contextcheck // see openStores

	k.logger.DebugContext(ctx, "kernel: hook chains built", "explicit_subscriptions", len(k.cfg.Hooks))
	return nil
}

// hookTimeout converts settings.default_hook_timeout_ms into the Duration
// hookdispatch.NewRegistry takes, falling back to config's own canonical
// default for a hand-built Settings that never went through LoadFile.
func hookTimeout(ms int) time.Duration {
	if ms <= 0 {
		ms = config.DefaultHookTimeoutMS
	}
	return time.Duration(ms) * time.Millisecond
}

// toolTimeout converts settings.default_tool_timeout_ms the same way.
func toolTimeout(ms int) time.Duration {
	if ms <= 0 {
		ms = config.DefaultToolTimeoutMS
	}
	return time.Duration(ms) * time.Millisecond
}
