// Command providerconform checks a built model-provider plugin against
// the conformance suite and reports what it found.
//
//	providerconform [flags] <plugin-binary>
//
// It launches the binary the way the kernel does — a real handshake, a
// real subprocess, a real dispense — so it exercises the plugin's own
// main() wiring, and so it works on a plugin written in any language:
// it speaks nothing but the wire protocol.
//
// The assertions are pkg/model/modeltest's, identical to the ones a Go
// author gets from modeltest.Run in their own test. This binary exists
// for the two cases that cannot reach those: a plugin not written in Go,
// and an operator who wants to check a binary they did not build.
//
// Exit codes are meant to be scripted against:
//
//	0  no violations (skips may still be reported)
//	1  at least one violation
//	2  the binary could not be launched or checked at all
//
// 1 and 2 are deliberately distinct. A binary that will not start has not
// failed the suite, it has failed to be tested, and a CI job that
// conflates the two reports a conformance regression when the real
// problem is a bad path or a missing execute bit.
//
// Everything here is flag parsing and wiring, per
// .claude/rules/go-layout.md; the checking itself lives in
// pkg/model/modeltest so a Go author and this binary can never drift
// apart in what they assert.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/pluggableharness/agent/pkg/model/modeltest"
)

// Exit codes, named so the doc comment above and the code cannot drift.
const (
	exitOK       = 0
	exitViolated = 1
	exitUnusable = 2
)

// errUsage reports a command line the tool cannot act on.
var errUsage = errors.New("usage: providerconform [flags] <plugin-binary>")

// errFlagsReported marks a flag-parsing failure the flag package has
// already written to stderr. main returns it without printing, so the
// operator sees one explanation rather than two.
var errFlagsReported = errors.New("flag parsing failed")

func main() {
	// signal.NotifyContext so an interrupted run tears the plugin
	// subprocess down rather than orphaning it. Released explicitly rather
	// than deferred, because os.Exit below would skip a defer.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	code, err := run(ctx, os.Args[1:], os.Stdout)
	if err != nil && !errors.Is(err, errFlagsReported) {
		fmt.Fprintln(os.Stderr, "providerconform:", err)
	}

	stop()
	os.Exit(code)
}

// run parses args, runs the suite, and writes the report to out.
//
// Split from main so every path returns rather than calling os.Exit,
// which would skip deferred cleanup (go-style.md).
func run(ctx context.Context, args []string, out io.Writer) (int, error) {
	fs := flag.NewFlagSet("providerconform", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	configPath := fs.String("config", "",
		"path to a JSON file passed to the plugin's Configure RPC. Point the provider at a recorded transcript or a local test server here — a conformance run must not make billed vendor calls.")
	modelID := fs.String("model", "",
		"which advertised model to exercise. Defaults to the first the plugin advertises.")
	timeout := fs.Duration("timeout", modeltest.DefaultCallTimeout,
		"per-RPC timeout. Lower it against a fake, where any real delay means the plugin is wedged.")

	if err := fs.Parse(args); err != nil {
		return exitUnusable, errFlagsReported
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return exitUnusable, errUsage
	}
	binary := fs.Arg(0)

	opts := []modeltest.Option{modeltest.WithCallTimeout(*timeout)}
	if *modelID != "" {
		opts = append(opts, modeltest.WithModelID(*modelID))
	}
	if *configPath != "" {
		cfg, err := loadConfig(*configPath)
		if err != nil {
			return exitUnusable, err
		}
		opts = append(opts, modeltest.WithConfig(cfg))
	}

	report, err := modeltest.CheckBinary(ctx, binary, opts...)
	if err != nil {
		return exitUnusable, err
	}

	if _, err := io.WriteString(out, formatReport(binary, report)); err != nil {
		return exitUnusable, fmt.Errorf("writing the report: %w", err)
	}
	if !report.OK() {
		return exitViolated, nil
	}
	return exitOK, nil
}

// loadConfig reads a JSON object into the Struct Configure expects.
func loadConfig(path string) (*structpb.Struct, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- the operator names this file; reading it is the flag's purpose
	if err != nil {
		return nil, fmt.Errorf("reading -config: %w", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing -config as a JSON object: %w", err)
	}
	cfg, err := structpb.NewStruct(raw)
	if err != nil {
		return nil, fmt.Errorf("converting -config: %w", err)
	}
	return cfg, nil
}

// formatReport renders the findings and a one-line summary.
//
// Built as a string and written once rather than printed piecemeal, so
// the single write is the only place that can fail and its error is
// actually checked.
//
// Skips appear as prominently as failures on purpose: a skip means a
// requirement was not reached, and the whole point of reporting them is
// that an unexercised check must never read as a pass.
func formatReport(binary string, report modeltest.Report) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "conformance: %s\n\n", binary)
	if body := report.String(); body != "" {
		sb.WriteString(body)
		sb.WriteString("\n")
	}

	failures, skips := len(report.Failures()), len(report.Skips())
	switch {
	case failures == 0 && skips == 0:
		sb.WriteString("PASS — every check satisfied\n")
	case failures == 0:
		fmt.Fprintf(&sb, "PASS — no violations, %d check(s) not reached\n", skips)
	default:
		fmt.Fprintf(&sb, "FAIL — %d violation(s), %d check(s) not reached\n", failures, skips)
	}
	return sb.String()
}
