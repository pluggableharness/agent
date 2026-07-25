// Command agent is the PluggableHarness kernel binary.
//
// This build runs exactly one non-interactive session: it loads agent.hcl,
// launches every resolved provider plugin, runs -prompt to completion,
// prints the session's final message to stdout, and exits. The interactive
// command docs/specifications/architecture.md#cli-shape describes arrives
// with the frontend plugin category; there is no REPL here yet.
//
// Everything below is wiring, per .claude/rules/go-layout.md: flags in,
// one internal/kernel.Run call, an exit code out.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/pluggableharness/agent/internal/kernel"
)

// Exit codes. 130 for a SIGINT follows the shell convention (128 + SIGINT),
// so a piped invocation can tell an operator's Ctrl-C apart from a real
// failure; 2 for a usage error matches flag's own convention.
const (
	exitOK       = 0
	exitFailure  = 1
	exitUsage    = 2
	exitCanceled = 130
)

// version is overridden at release time via -ldflags; a `go build` from a
// checkout reports whatever the module's own build info knows.
var version = ""

func main() { os.Exit(run()) }

// run parses flags, runs one session, and maps the outcome to an exit
// code. It exists separately from main so every path returns rather than
// calling os.Exit from inside a nested scope, which would skip deferred
// cleanup.
func run() int {
	fs := flag.NewFlagSet("agent", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var (
		configPath  = fs.String("config", kernel.DefaultConfigFile, "path to the agent.hcl config file")
		profile     = fs.String("profile", "", `agent_profile block to run under (default "default")`)
		prompt      = fs.String("prompt", "", "the prompt to run (required: this build has no interactive mode)")
		logLevel    = fs.String("log-level", "", "override settings.log_level (trace|debug|info|warn|error)")
		showVersion = fs.Bool("version", false, "print the version and exit")
	)

	if err := fs.Parse(os.Args[1:]); err != nil {
		// flag already wrote the message and the usage text.
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if *showVersion {
		_, _ = fmt.Fprintln(os.Stdout, buildVersion())
		return exitOK
	}
	if *prompt == "" {
		_, _ = fmt.Fprintln(os.Stderr, "agent: -prompt is required")
		fs.Usage()
		return exitUsage
	}

	// The one cancellation root: everything below derives from it, so a
	// signal reaches the model stream, the tool calls, and the plugin
	// subprocesses through the same context internal/kernel already
	// threads everywhere.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := kernel.Run(ctx, kernel.Options{
		ConfigPath: *configPath,
		Profile:    *profile,
		Prompt:     *prompt,
		LogLevel:   *logLevel,
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
	})
	switch {
	case err == nil:
		return exitOK
	case errors.Is(err, context.Canceled):
		// A real SIGINT is a normal exit, not a failure to report: the
		// kernel already persisted the session as cancelled and logged
		// why.
		return exitCanceled
	default:
		// The one sanctioned non-slog write in the tree: a config-load
		// or path-resolution failure happens before logging is wired at
		// all, so slog.Default() would still be stdlib's.
		_, _ = fmt.Fprintln(os.Stderr, "agent:", err)
		return exitFailure
	}
}

// buildVersion reports the release version when one was stamped in, and
// falls back to the module's own recorded build info otherwise.
func buildVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "(devel)"
}
