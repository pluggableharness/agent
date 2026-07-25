package kernel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"

	"github.com/pluggableharness/agent/internal/config"
	"github.com/pluggableharness/agent/internal/eventbus"
	"github.com/pluggableharness/agent/internal/hookdispatch"
	"github.com/pluggableharness/agent/internal/log"
	"github.com/pluggableharness/agent/internal/pluginhost"
	"github.com/pluggableharness/agent/internal/providercatalog"
	"github.com/pluggableharness/agent/internal/session"
	"github.com/pluggableharness/agent/internal/sessionscope"
	"github.com/pluggableharness/agent/internal/sessionstate"
	"github.com/pluggableharness/agent/internal/statebackend"
	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetryrelay"
	"github.com/pluggableharness/agent/internal/tokencount"
	"github.com/pluggableharness/agent/internal/xdg"
)

// DefaultConfigFile is the project-root config file name Options.ConfigPath
// defaults to (docs/specifications/architecture.md#xdg-layout).
const DefaultConfigFile = "agent.hcl"

// DefaultLogLevel is the level used when neither Options.LogLevel nor
// settings.log_level names one.
const DefaultLogLevel = "info"

// shutdownTimeout bounds the whole phased teardown. Shutdown normally runs
// because ctx was already canceled (a SIGINT, or a bring-up failure), so it
// runs on a fresh deadline over context.WithoutCancel — but it must still
// be bounded, or one wedged plugin subprocess hangs the process forever.
//
// Fifteen seconds is a project judgment call, not a spec-mandated number:
// it is comfortably more than pluginhost.Supervisor's own per-plugin
// drain-then-kill sequence needs across a handful of plugins, and short
// enough that an operator who hit Ctrl-C twice does not reach for kill -9.
const shutdownTimeout = 15 * time.Second

// Options is everything cmd/agent supplies to one Run call.
type Options struct {
	// ConfigPath is the agent.hcl to load. Empty resolves to
	// DefaultConfigFile inside WorkingDirectory.
	ConfigPath string

	// Profile names the agent_profile block to run under. Empty resolves
	// to "default" inside internal/session, which falls back to
	// BuiltinDefaultProfile when no such block is declared.
	Profile string

	// Prompt is the single non-interactive prompt this session runs.
	// Required — this build has no frontend and therefore no way to ask
	// for one.
	Prompt string

	// LogLevel overrides settings.log_level when non-empty. Accepts the
	// six-level vocabulary internal/log.ParseLevel defines.
	LogLevel string

	// WorkingDirectory is the project directory: the root
	// internal/xdg.Resolve computes ./agent.hcl and ./.agent/ against,
	// and the directory tool calls run in. Empty resolves to os.Getwd.
	WorkingDirectory string

	// Stdout receives the session's final message, and nothing else.
	// Nil defaults to os.Stdout.
	Stdout io.Writer

	// Stderr receives this process's own log output. Nil defaults to
	// os.Stderr.
	Stderr io.Writer
}

// ErrNoPrompt reports an Options with no Prompt. This build runs exactly
// one non-interactive session, so there is nowhere else a prompt could
// come from.
var ErrNoPrompt = errors.New("kernel: a prompt is required")

// normalize fills Options' defaults, returning the resolved copy.
func (o Options) normalize() (Options, error) {
	if o.Prompt == "" {
		return Options{}, ErrNoPrompt
	}
	if o.WorkingDirectory == "" {
		wd, err := os.Getwd()
		if err != nil {
			return Options{}, fmt.Errorf("kernel: working directory: %w", err)
		}
		o.WorkingDirectory = wd
	}
	if o.Stdout == nil {
		o.Stdout = os.Stdout
	}
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
	return o, nil
}

// kernel is one Run call's assembled process-wide state. Every field is
// populated by bringUp, in the order the fields are declared, and torn
// down by shutdown in reverse. A partially-populated kernel is normal —
// bringUp returns one alongside its error precisely so shutdown can close
// whatever did come up.
type kernel struct {
	opts   Options
	paths  xdg.Paths
	cfg    *config.Config
	logger *slog.Logger

	// bootTelem is the throwaway, fully-disabled Provider config loading
	// runs under, before the real one can be built from that same config.
	// Shut down as soon as telem replaces it, not at teardown.
	bootTelem *telemetry.Provider

	telem *telemetry.Provider
	relay *telemetryrelay.Relay
	bus   *eventbus.Bus
	store *statebackend.Store

	scopes   *sessionscope.Registry
	sessions *sessionstate.Table
	plugins  *pluginhost.Registry
	tokens   *tokencount.Counter
	logSrv   *log.Server

	supervisor *pluginhost.Supervisor
	catalog    providercatalog.Catalog
	hookReg    *hookdispatch.Registry
	hooks      *hookdispatch.Dispatcher

	// sink is the late-binding bridge between the turn-stack collaborators
	// built here and the per-session *statebackend.Session internal/session
	// creates for itself. See turnstack.go.
	sink *sessionSink
}

// Run loads config, launches every resolved plugin, runs exactly one
// non-interactive session with opts.Prompt, prints the final message to
// opts.Stdout, and shuts everything down in reverse order — even when a
// failure happened partway through bring-up.
//
// The returned error is non-nil for any failure: a missing or invalid
// config, an unresolvable provider (a *providerresolve.MissingError,
// naming every one), a plugin that would not launch or configure, or the
// session itself failing. cmd/agent maps it to a process exit code.
func Run(ctx context.Context, opts Options) error {
	opts, err := opts.normalize()
	if err != nil {
		return err
	}

	k, upErr := bringUp(ctx, opts)
	if upErr != nil {
		// k is non-nil and partially built: tear down whatever came up
		// before returning the bring-up failure, which is the error that
		// matters.
		return errors.Join(upErr, k.shutdown(ctx))
	}

	runErr := k.runSession(ctx)
	return errors.Join(runErr, k.shutdown(ctx))
}

// runSession builds the session driver over the process-wide collaborators
// and runs exactly one session.
//
// The Runner (and the turn stack under it) is constructed per session, not
// per process: internal/plangate and internal/tooldispatch share one
// *circuitbreaker.Breaker scoped to a single session, and the gate needs
// that session's id at construction. See internal/session's CLAUDE.md.
func (k *kernel) runSession(ctx context.Context) error {
	stack := newTurnStack(k)

	runner, err := session.New(session.Config{ //nolint:contextcheck // session.New takes no context by design; nothing is dropped
		Store:                 k.store,
		Sessions:              k.sessions,
		Scopes:                k.scopes,
		Bus:                   k.bus,
		Turn:                  stack,
		Hooks:                 k.hooks,
		Catalog:               k.catalog,
		Profiles:              k.cfg.AgentProfiles,
		KernelDefaultMaxDepth: maxDepth(k.cfg.Settings.MaxDepth),
		DoomLoop:              doomLoopConfig(k.cfg.Settings.DoomLoop),
		Telemetry:             k.telem,
		Logger:                k.logger,
	})
	if err != nil {
		return fmt.Errorf("kernel: session driver: %w", err)
	}

	result, err := runner.Run(ctx, session.Spec{
		Profile:          k.opts.Profile,
		Prompt:           k.opts.Prompt,
		WorkingDirectory: k.opts.WorkingDirectory,
	})
	if err != nil {
		return fmt.Errorf("kernel: session %s: %w", result.SessionID, err)
	}

	k.logger.InfoContext(ctx, "kernel: session finished",
		"session_id", result.SessionID,
		"status", result.Status.String(),
		"final_answer_reason", result.FinalAnswerReason,
		"total_cost_usd", result.TotalCostUSD,
		"input_tokens", result.TotalInputTokens,
		"output_tokens", result.TotalOutputTokens)

	if _, err := io.WriteString(k.opts.Stdout, finalText(result.FinalMessage)); err != nil {
		return fmt.Errorf("kernel: write final message: %w", err)
	}
	return nil
}

// finalText renders a session's final message as the plain text a
// non-interactive caller reads on stdout: every text block, in emission
// order, newline-terminated.
//
// Only text blocks are rendered. A tool_use/tool_result/thinking block in
// a *final* message has no meaning to a pipeline consumer, and the real
// answer to "how should this be displayed" is the Emit -> Render -> Paint
// pipeline (architecture.md#emit--render--paint-pipeline) that arrives
// with the frontend category — not a second, competing renderer here.
func finalText(msg *contentv1.Message) string {
	if msg == nil {
		return ""
	}
	var out string
	for _, block := range msg.GetContent() {
		if text := block.GetText(); text != nil {
			out += text.GetText()
		}
	}
	if out == "" {
		return ""
	}
	return out + "\n"
}
