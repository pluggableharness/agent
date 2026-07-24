// Package pluginruntime launches, dials, and shuts down one
// hashicorp/go-plugin subprocess for one of the seven plugin categories
// (provider, tool, context, memory, frontend, widget, slashcommand), and serves the
// reverse KernelCallbackService channel back to it. See doc.go for the
// package-level overview and README.md/CLAUDE.md for the fuller design
// rationale.
package pluginruntime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"

	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/pkg/common"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
)

// Sentinel errors for an invalid Config, checked by Launch before
// anything is spawned.
var (
	ErrMissingBinaryPath = errors.New("pluginruntime: config: binary path is required")
	ErrMissingProducer   = errors.New("pluginruntime: config: producer is required")
	ErrMissingCallback   = errors.New("pluginruntime: config: callback server is required")
	ErrMissingTelemetry  = errors.New("pluginruntime: config: telemetry provider is required")
)

// VersionMismatchError reports that a plugin's go-plugin protocol version
// — either declared up front (the launch step 1 pre-flight check) or
// negotiated at handshake (the launch step 6 authoritative check) — does
// not match this kernel's own common.ProtocolVersion. This is always a
// startup error, never a runtime error discovered on first RPC
// (plugin-runtime.md).
type VersionMismatchError struct {
	// Declared is the plugin's protocol version, as reported by whichever
	// check constructed this error.
	Declared int
	// Kernel is this kernel build's common.ProtocolVersion.
	Kernel int
}

// Error implements the error interface.
func (e *VersionMismatchError) Error() string {
	return fmt.Sprintf("pluginruntime: protocol version mismatch: plugin=%d kernel=%d", e.Declared, e.Kernel)
}

// Config configures one Launch call — one subprocess, one category.
type Config struct {
	// BinaryPath is the already-resolved local plugin binary to exec.
	// Resolving a registry source address to this path is out of scope
	// for this package — the caller (a future registry-aware launcher)
	// does that resolution and hands Launch the result, the same way
	// DevOverrides already does today.
	BinaryPath string

	// Producer identifies the plugin being launched: category (selects
	// the one plugin.GRPCPlugin adapter registered for this launch),
	// name, version, and — once something populates it — the protocol
	// version used by the step 1 pre-flight check. Required.
	Producer *commonv1.ProducerRef

	// Callback is this launch's already-constructed
	// KernelCallbackServiceServer: one internal/kernelcallback.Server per
	// launched plugin, built by the caller with this plugin's producer
	// identity already baked in (kernel-callbacks.md §4/§5's
	// server-derived-identity requirement). This package only serves it
	// on the fixed callback broker — it never constructs one itself.
	// Required.
	Callback kernelv1.KernelCallbackServiceServer

	// Telemetry supplies the ClientHandler/ServerHandler stats handlers
	// wired onto both halves of the gRPC boundary, and the
	// StartPluginLaunch span this launch runs inside. Required.
	Telemetry *telemetry.Provider

	// Logger receives this launch's own DEBUG/INFO log lines and backs
	// the hclog.Logger shim handed to go-plugin for its own subprocess
	// diagnostics (hcloglogger.go — not plugin application logs, see
	// CLAUDE.md). Defaults to slog.Default() if nil.
	Logger *slog.Logger

	// ExtraEnv is additional "KEY=VALUE" environment entries appended
	// after the minimal allowlist (PATH/HOME/TMPDIR) and the
	// OTEL_RESOURCE_ATTRIBUTES stamp — never a substitute for either, and
	// never a path to inheriting the kernel's full os.Environ() (see this
	// package's CLAUDE.md on the env-allowlist decision).
	ExtraEnv []string
}

// validate checks the fields Launch cannot proceed without. It
// deliberately does not check ExtraEnv or Logger — both are optional.
func (c Config) validate() error {
	if c.BinaryPath == "" {
		return ErrMissingBinaryPath
	}
	if c.Producer == nil {
		return ErrMissingProducer
	}
	if c.Callback == nil {
		return ErrMissingCallback
	}
	if c.Telemetry == nil {
		return ErrMissingTelemetry
	}
	return nil
}

// Plugin is one successfully launched and dispensed plugin subprocess.
type Plugin struct {
	client       *plugin.Client
	dispensed    any
	producer     *commonv1.ProducerRef
	cancelLaunch context.CancelFunc
}

// Dispensed returns the raw generated category-service client for this
// plugin (e.g. modelv1.ModelServiceClient for a model plugin,
// per launch step 8). Callers type-assert to the category's generated
// client interface — this package returns it as any because it has no
// category-specific knowledge of its own.
func (p *Plugin) Dispensed() any {
	return p.dispensed
}

// Producer returns the identity this plugin was launched with.
func (p *Plugin) Producer() *commonv1.ProducerRef {
	return p.producer
}

// preflightVersionCheck implements launch step 1: a no-op today, since
// nothing populates a real protocol version anywhere yet (no registry/
// lockfile field carries one) — operator decision #2. Once something
// does, this is the first gate a mismatched build fails, before any
// subprocess is spawned; step 6's post-handshake NegotiatedVersion()
// check remains the sole *authoritative* gate regardless.
func preflightVersionCheck(producer *commonv1.ProducerRef) error {
	declared := producer.GetProtocolVersion()
	if declared == 0 {
		return nil
	}
	if declared != uint32(common.ProtocolVersion) {
		return &VersionMismatchError{Declared: int(declared), Kernel: int(common.ProtocolVersion)}
	}
	return nil
}

// buildEnv assembles a launched subprocess's environment from the
// minimal allowlist (PATH/HOME/TMPDIR, only if set in the kernel's own
// environment) plus the OTEL_RESOURCE_ATTRIBUTES producer-identity stamp
// plus extra — never os.Environ() (operator decision #1: ambient
// inheritance would leak every env var the kernel process holds,
// including secrets meant for other plugins, into every subprocess).
func buildEnv(category commonv1.Category, name, version string, extra []string) []string {
	env := make([]string, 0, 4+len(extra))
	for _, key := range []string{"PATH", "HOME", "TMPDIR"} {
		if v, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+v)
		}
	}
	env = append(env, "OTEL_RESOURCE_ATTRIBUTES="+telemetry.ResourceEnv(common.PluginKey(category), name, version))
	env = append(env, extra...)
	return env
}

// buildClient implements launch steps 2-4: the one-entry plugin map, the
// subprocess exec.Cmd built under the minimal env allowlist, and the
// plugin.Client construction with crash-classifying dial options. It does
// not start the subprocess or dial anything — plugin.NewClient only
// builds a struct — so this is the boundary Launch's own unit tests stop
// at (go-testing.md's unit tier must not spawn a real subprocess; the
// actual spawn+handshake is client.Client(), called only by Launch,
// exercised by launch_integration_test.go instead).
//
// The returned context.CancelFunc cancels the launchCtx the returned
// client's subprocess command was built with — Launch releases it on any
// failure path, and a successful launch hands it to the returned *Plugin
// for Close's shutdown escalation (shutdown.go).
func buildClient(ctx context.Context, cfg Config, logger *slog.Logger) (*plugin.Client, context.CancelFunc) {
	category := cfg.Producer.GetCategory()
	name := cfg.Producer.GetName()
	version := cfg.Producer.GetVersion()

	plugins := pluginMap(category, cfg.Callback, cfg.Telemetry)

	launchCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(launchCtx, cfg.BinaryPath) // #nosec G204 -- launching the operator-configured, checksum-verified plugin binary is this package's entire purpose, not attacker-controlled input
	cmd.Env = buildEnv(category, name, version, cfg.ExtraEnv)

	// holder resolves the crash interceptors' chicken-and-egg dependency
	// on the *plugin.Client that doesn't exist until plugin.NewClient
	// returns (crash.go).
	holder := &clientHolder{}
	client := plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig:  common.Handshake,
		Plugins:          plugins,
		Cmd:              cmd,
		AllowedProtocols: []plugin.Protocol{plugin.ProtocolGRPC},
		Logger:           newHCLogger(logger, "pluginruntime"),
		GRPCDialOptions: []grpc.DialOption{
			grpc.WithStatsHandler(cfg.Telemetry.ClientHandler()),
			grpc.WithChainUnaryInterceptor(crashUnaryInterceptor(holder.exited)),
			grpc.WithChainStreamInterceptor(crashStreamInterceptor(holder.exited)),
		},
	})
	holder.client.Store(client)

	return client, cancel
}

// Launch runs the full launch sequence (plugin-runtime.md's "Handshake"
// and "Subprocess lifecycle" sections; see this package's CLAUDE.md for
// the numbered step-by-step this implements): pre-flight version check,
// plugin-map construction, subprocess spawn under a minimal env
// allowlist, client construction with crash-classifying dial options,
// handshake, the authoritative post-handshake version gate, and dispense.
//
// ctx governs the subprocess's entire lifetime: canceling it tears the
// subprocess tree down (exec.CommandContext), and it is also the parent
// of the internal launchCtx a later Close(ctx) escalates to if go-plugin's
// own Kill() doesn't finish inside the drain window (shutdown.go).
func Launch(ctx context.Context, cfg Config) (*Plugin, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	category := cfg.Producer.GetCategory()
	name := cfg.Producer.GetName()
	version := cfg.Producer.GetVersion()
	categoryKey := common.PluginKey(category)

	logger.DebugContext(ctx, "pluginruntime: launch: starting",
		"category", categoryKey, "name", name, "version", version, "binary", cfg.BinaryPath)

	// Step 1: pre-flight version check, before anything is spawned.
	if err := preflightVersionCheck(cfg.Producer); err != nil {
		return nil, err
	}

	ctx, span := cfg.Telemetry.StartPluginLaunch(ctx, categoryKey, name, version)
	var err error
	defer func() { telemetry.EndSpan(span, err) }()

	// Steps 2-4: plugin map, subprocess command, and *plugin.Client
	// construction — split into buildClient because none of it actually
	// starts anything (plugin.NewClient only builds a struct; go-plugin
	// confirmed via direct source read), which is what lets that part be
	// unit-tested without spawning a real subprocess. cancel is released
	// here on any failure path below; ownership passes to the returned
	// *Plugin only on success.
	client, cancel := buildClient(ctx, cfg, logger)
	launchOK := false
	defer func() {
		if !launchOK {
			cancel()
		}
	}()

	// Step 5: spawn + handshake.
	rpcClient, dialErr := client.Client()
	if dialErr != nil {
		client.Kill()
		err = fmt.Errorf("pluginruntime: launch: %w", dialErr)
		return nil, err
	}

	// Step 6: the sole authoritative version gate, before any category
	// RPC is issued.
	negotiated := client.NegotiatedVersion()
	if negotiated != int(common.ProtocolVersion) {
		client.Kill()
		err = &VersionMismatchError{Declared: negotiated, Kernel: int(common.ProtocolVersion)}
		return nil, err
	}

	// Step 7: dispense — triggers categoryPlugin.GRPCClient, which
	// registers the callback broker and returns the raw category client.
	raw, dispenseErr := rpcClient.Dispense(categoryKey)
	if dispenseErr != nil {
		client.Kill()
		err = fmt.Errorf("pluginruntime: launch: dispense: %w", dispenseErr)
		return nil, err
	}

	logger.DebugContext(ctx, "pluginruntime: launch: complete",
		"category", categoryKey, "name", name, "version", version)

	launchOK = true
	// Step 8.
	return &Plugin{client: client, dispensed: raw, producer: cfg.Producer, cancelLaunch: cancel}, nil
}
