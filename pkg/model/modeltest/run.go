package modeltest

import (
	"context"
	"fmt"
	"net"
	"testing"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/pluggableharness/agent/pkg/common"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	"github.com/pluggableharness/agent/pkg/model"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
	"github.com/pluggableharness/agent/pkg/plugin"
)

// bufSize is the in-memory listener's buffer. Generous enough that a
// large frame never blocks the writer during a test.
const bufSize = 1 << 20

// Run checks p against the conformance suite and fails t for every
// violation. This is the entry point a provider author calls from their
// own test.
//
// Skipped checks are logged rather than failed: a skip means the run
// could not reach that requirement, which is worth seeing but is not a
// violation.
func Run(t *testing.T, p model.Provider, opts ...Option) {
	t.Helper()
	report(t, Check(t.Context(), p, opts...))
}

// RunBinary checks an already-built plugin binary and fails t for every
// violation.
//
// It spawns a subprocess, so it belongs in the integration tier
// (.claude/rules/go-testing.md), never the unit tier.
func RunBinary(t *testing.T, binaryPath string, opts ...Option) {
	t.Helper()

	rep, err := CheckBinary(t.Context(), binaryPath, opts...)
	if err != nil {
		t.Fatalf("modeltest: %v", err)
	}
	report(t, rep)
}

// report fails t for each violation and logs each skip.
func report(t *testing.T, rep Report) {
	t.Helper()

	for _, f := range rep.Skips() {
		t.Logf("SKIP %s: %s", f.Check, f.Message)
	}
	for _, f := range rep.Failures() {
		t.Errorf("%s: %s", f.Check, f.Message)
	}
}

// Check runs the conformance suite against p in-process, over a real gRPC
// round trip on an in-memory listener, and returns what it found.
//
// The round trip is the point: most of what this suite checks lives in
// the pkg/model service adapter and the generated wire types — terminal
// event bookkeeping, error-to-status mapping, the conversion layer — and
// a direct method call on p would exercise none of it.
//
// Returning a Report rather than driving *testing.T is what lets a
// non-test binary reuse these assertions, and what lets the suite's own
// tests prove it rejects a bad provider. A conformance suite that cannot
// be shown to fail is worth very little.
func Check(ctx context.Context, p model.Provider, opts ...Option) Report {
	cfg := resolve(opts)
	cfg.inProcess = true

	// A fixed identity, deliberately NOT the caller's expectation: in
	// this mode modeltest supplies the identity itself, so serving the
	// expected one would make WithExpectedIdentity a check that compares
	// a value against itself and can never fail.
	identity := plugin.Identity{
		Name:    "modeltest",
		Version: "0.0.0",
		Source:  "github.com/pluggableharness/agent/pkg/model/modeltest",
	}

	lis := bufconn.Listen(bufSize)
	svc := model.NewService(p, identity, plugin.NewCallback())

	gs := grpc.NewServer()
	svc.Register(gs)
	go func() { _ = gs.Serve(lis) }()
	defer gs.Stop()

	dialer := func(context.Context, string) (net.Conn, error) { return lis.Dial() }
	conn, err := grpc.NewClient("passthrough:///modeltest",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return Report{Findings: []Finding{{
			Check:    "dial",
			Severity: SeverityFail,
			Message:  fmt.Sprintf("could not dial the in-process listener: %v", err),
		}}}
	}
	defer func() { _ = conn.Close() }()

	return runSuite(ctx, modelv1.NewModelServiceClient(conn), cfg)
}

// CheckBinary runs the conformance suite against an already-built plugin
// binary, launching it the way the kernel does: a real handshake, a real
// subprocess, a real dispense.
//
// This is the only mode that exercises the plugin's own main() wiring —
// its handshake config, its Serve call, its identity stamping — and the
// only one that works on a plugin written in a language other than Go,
// since it speaks nothing but the wire protocol.
//
// The returned error is for a failure to launch at all, which is distinct
// from a conformance violation: a binary that will not start has not
// failed the suite, it has failed to be tested.
func CheckBinary(ctx context.Context, binaryPath string, opts ...Option) (Report, error) {
	cfg := resolve(opts)
	client, cleanup, err := launch(ctx, binaryPath)
	if err != nil {
		return Report{}, err
	}
	defer cleanup()
	return runSuite(ctx, client, cfg), nil
}

// launch starts binaryPath as a go-plugin subprocess and returns a client
// for its ModelService plus a teardown func.
func launch(ctx context.Context, binaryPath string) (modelv1.ModelServiceClient, func(), error) {
	// The subprocess is bound to a context derived from the caller's, so
	// an abandoned run tears the process down rather than leaking it.
	ctx, cancel := context.WithCancel(ctx)

	categoryKey := common.PluginKey(commonv1.Category_CATEGORY_MODEL)
	client := goplugin.NewClient(&goplugin.ClientConfig{
		HandshakeConfig:  common.Handshake,
		Plugins:          goplugin.PluginSet{categoryKey: &clientPlugin{}},
		Cmd:              commandContext(ctx, binaryPath),
		AllowedProtocols: []goplugin.Protocol{goplugin.ProtocolGRPC},
		Logger:           discardLogger(),
	})
	cleanup := func() {
		client.Kill()
		cancel()
	}

	rpc, err := client.Client()
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("modeltest: handshake with %s: %w", binaryPath, err)
	}

	// The negotiated version gate is checked here for the same reason the
	// kernel checks it before the first category RPC: a version mismatch
	// is a startup error, not a mystery failure on some later call.
	if got := client.NegotiatedVersion(); got != int(common.ProtocolVersion) {
		cleanup()
		return nil, nil, fmt.Errorf("modeltest: protocol version mismatch: plugin=%d, this SDK=%d", got, common.ProtocolVersion)
	}

	raw, err := rpc.Dispense(categoryKey)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("modeltest: dispense %q: %w", categoryKey, err)
	}
	got, ok := raw.(modelv1.ModelServiceClient)
	if !ok {
		cleanup()
		return nil, nil, fmt.Errorf("modeltest: dispensed %T, want a ModelServiceClient — is this binary a model provider?", raw)
	}
	return got, cleanup, nil
}

// clientPlugin adapts the dispensed connection into a ModelServiceClient.
// It never serves, so GRPCServer is not reachable in this direction.
type clientPlugin struct {
	goplugin.Plugin
}

var _ goplugin.GRPCPlugin = (*clientPlugin)(nil)

// GRPCServer is never called: this adapter only ever runs kernel-side.
func (*clientPlugin) GRPCServer(*goplugin.GRPCBroker, *grpc.Server) error {
	return errServeUnsupported
}

// GRPCClient returns the generated client over the dispensed connection.
func (*clientPlugin) GRPCClient(_ context.Context, _ *goplugin.GRPCBroker, conn *grpc.ClientConn) (any, error) {
	return modelv1.NewModelServiceClient(conn), nil
}
