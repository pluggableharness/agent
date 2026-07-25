//go:build integration

// Package anthropic_test's integration tier launches the real
// cmd/anthropic binary as a go-plugin subprocess and drives it through
// the generated ModelService client, exactly as the kernel does — against
// an httptest.Server replaying a hand-written Anthropic SSE transcript
// rather than the live vendor.
//
// This is the tier that proves the parts unit tests structurally cannot:
// that the binary handshakes, that Describe and GetCapabilities
// round-trip over the wire, that Configure's decoded Struct survives the
// schema-to-cty bridge's shape, that a full stream reaches a real
// *model.Sink, that a vendor error becomes the right grpc code, and that
// a mid-stream cancellation tears down cleanly.
package anthropic_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/pluggableharness/agent/internal/eventbus"
	"github.com/pluggableharness/agent/internal/kernelcallback"
	"github.com/pluggableharness/agent/internal/log"
	"github.com/pluggableharness/agent/internal/pluginruntime"
	"github.com/pluggableharness/agent/internal/telemetry"
	telemetryfake "github.com/pluggableharness/agent/internal/telemetry/drivers/fake"
	"github.com/pluggableharness/agent/internal/telemetryrelay"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

// The model this tier drives. A real roster entry rather than a fixture
// id, because GetCapabilities is served by the real catalog.
const testModelID = "claude-opus-5"

// pluginBinary is the built cmd/anthropic every test here launches.
var pluginBinary string

func TestMain(m *testing.M) { os.Exit(run(m)) }

// run builds the plugin once and delegates to m.Run, so every cleanup
// happens before os.Exit — which skips deferred calls (go-style.md).
func run(m *testing.M) int {
	// bin/ is the only sanctioned output path for a compiled artifact in
	// this repo, test fixtures included — the project CLAUDE.md's
	// "Build output — bin/ only, no exceptions".
	binDir, err := filepath.Abs(filepath.Join("..", "..", "bin"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "anthropic: integration: resolve bin/:", err)
		return 1
	}
	if err := os.MkdirAll(binDir, 0o750); err != nil {
		fmt.Fprintln(os.Stderr, "anthropic: integration: mkdir bin/:", err)
		return 1
	}
	pluginBinary = filepath.Join(binDir, "anthropic-integration")

	cmd := exec.CommandContext(context.Background(), "go", "build", "-o", pluginBinary, "./../../cmd/anthropic")
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "anthropic: integration: build plugin: %v\n%s", err, out)
		return 1
	}
	defer func() { _ = os.Remove(pluginBinary) }()

	return m.Run()
}

// transcriptToolUse is the worked event sequence from
// docs/specifications/model/examples.md#a-full-streamcompletion-event-sequence,
// written in Anthropic's own SSE format: text, then one tool call, then
// usage, then a tool_use stop.
const transcriptToolUse = `event: message_start
data: {"type":"message_start","message":{"id":"msg_int_1","type":"message","role":"assistant","model":"claude-opus-5","content":[],"stop_reason":null,"usage":{"input_tokens":412,"cache_read_input_tokens":128,"cache_creation_input_tokens":64,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: ping
data: {"type":"ping"}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Let me check "}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"that file."}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"tc_1","name":"read_file","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"main.go\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":28}}

event: message_stop
data: {"type":"message_stop"}

`

// TestPlugin_describeAndCapabilities proves the handshake, the Describe
// identity a dev_overrides binary depends on, and the real roster
// crossing the wire.
func TestPlugin_describeAndCapabilities(t *testing.T) {
	client, _ := launchPlugin(t, staticTranscript(transcriptToolUse))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	describe, err := client.Describe(ctx, &modelv1.DescribeRequest{})
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	producer := describe.GetProducer()
	if producer.GetName() != "anthropic" {
		t.Errorf("Describe name = %q, want %q", producer.GetName(), "anthropic")
	}
	if producer.GetCategory() != commonv1.Category_CATEGORY_MODEL {
		t.Errorf("Describe category = %v, want CATEGORY_MODEL", producer.GetCategory())
	}

	caps, err := client.GetCapabilities(ctx, &modelv1.GetCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("GetCapabilities: %v", err)
	}

	var found *modelv1.ModelSpec
	for _, m := range caps.GetCapabilities().GetModels() {
		if m.GetId() == testModelID {
			found = m
		}
	}
	if found == nil {
		t.Fatalf("roster has no %q", testModelID)
	}
	if found.GetContextWindow() != 1_000_000 {
		t.Errorf("context window = %d, want 1000000", found.GetContextWindow())
	}
	if !found.GetSupportsToolUse() {
		t.Error("model must declare tool-use support")
	}
	if got := found.GetPricing().GetTiers(); len(got) == 0 {
		t.Error("pricing must carry at least one tier — the kernel bills from it")
	}

	// The config schema rides along on GetCapabilities so the kernel
	// knows what Configure expects before ever calling it.
	var sawAPIKey bool
	for _, attr := range caps.GetCapabilities().GetConfigSchema().GetAttributes() {
		if attr.GetName() == "api_key" {
			sawAPIKey = true
			if !attr.GetSensitive() {
				t.Error("api_key must be declared sensitive across the wire")
			}
		}
	}
	if !sawAPIKey {
		t.Error("config schema did not reach the kernel")
	}
}

// TestPlugin_streamCompletionDeliversEveryEvent drives the worked
// transcript end to end and asserts the events a real *model.Sink
// produced, in order.
func TestPlugin_streamCompletionDeliversEveryEvent(t *testing.T) {
	client, server := launchPlugin(t, staticTranscript(transcriptToolUse))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	configure(t, ctx, client, server.URL)

	stream, err := client.StreamCompletion(ctx, sampleRequest())
	if err != nil {
		t.Fatalf("StreamCompletion: %v", err)
	}

	var (
		text      strings.Builder
		arguments strings.Builder
		usage     *modelv1.Usage
		stop      *modelv1.StreamEvent_Stop
		toolID    string
		toolName  string
		toolDone  bool
	)
	for {
		ev, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		switch {
		case ev.GetTextDelta() != nil:
			text.WriteString(ev.GetTextDelta().GetText())
		case ev.GetToolCallStart() != nil:
			toolID = ev.GetToolCallStart().GetId()
			toolName = ev.GetToolCallStart().GetName()
		case ev.GetToolCallDelta() != nil:
			arguments.WriteString(ev.GetToolCallDelta().GetArgumentsFragment())
		case ev.GetToolCallDone() != nil:
			toolDone = true
		case ev.GetUsage() != nil:
			usage = ev.GetUsage()
		case ev.GetStop() != nil:
			stop = ev.GetStop()
		}
	}

	if got, want := text.String(), "Let me check that file."; got != want {
		t.Errorf("text = %q, want %q", got, want)
	}
	if toolID != "tc_1" || toolName != "read_file" {
		t.Errorf("tool call = (%q, %q), want (tc_1, read_file)", toolID, toolName)
	}
	if got, want := arguments.String(), `{"path":"main.go"}`; got != want {
		t.Errorf("accumulated arguments = %q, want %q", got, want)
	}
	if !toolDone {
		t.Error("no tool_call_done — the kernel would never dispatch the call")
	}
	if stop == nil || stop.GetReason() != modelv1.StopReason_STOP_REASON_TOOL_USE {
		t.Errorf("stop = %v, want STOP_REASON_TOOL_USE", stop)
	}

	if usage == nil {
		t.Fatal("no usage event — the kernel would have no cost to persist")
	}
	if usage.GetInputTokens() != 412 {
		t.Errorf("input tokens = %d, want 412", usage.GetInputTokens())
	}
	// Anthropic's message_delta usage is cumulative, so the adapter must
	// merge rather than emit twice: input/cache counts come from
	// message_start, output tokens from the last message_delta.
	if usage.GetOutputTokens() != 28 {
		t.Errorf("output tokens = %d, want 28", usage.GetOutputTokens())
	}
	if usage.GetCacheReadTokens() != 128 {
		t.Errorf("cache read tokens = %d, want 128", usage.GetCacheReadTokens())
	}
	if usage.GetCacheWriteTokens() != 64 {
		t.Errorf("cache write tokens = %d, want 64", usage.GetCacheWriteTokens())
	}
}

// TestPlugin_vendorErrorMapsToStatusCode proves the taxonomy survives the
// plugin boundary: a vendor 429 must arrive as codes.ResourceExhausted,
// which is what tells internal/modelcall to back off rather than fail the
// turn (.claude/rules/grpc.md's mapping table).
func TestPlugin_vendorErrorMapsToStatusCode(t *testing.T) {
	handler := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.Header().Set("retry-after", "7")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"rate_limit_error","message":"rate limit exceeded"},"request_id":"req_int_429"}`)
	}
	client, server := launchPlugin(t, handler)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	configure(t, ctx, client, server.URL)

	stream, err := client.StreamCompletion(ctx, sampleRequest())
	if err != nil {
		t.Fatalf("StreamCompletion: %v", err)
	}

	// The failure may surface either as a terminal in-band Error event or
	// as the stream's status, depending on whether the adapter had
	// already opened the stream. Both are legal per
	// docs/specifications/model/data-types.md#streamevent; assert
	// whichever arrives carries the right classification.
	var inBand *modelv1.ModelError
	for {
		ev, recvErr := stream.Recv()
		if recvErr != nil {
			if errors.Is(recvErr, io.EOF) {
				break
			}
			if got, want := status.Code(recvErr), codes.ResourceExhausted; got != want {
				t.Fatalf("stream status code = %v, want %v (err: %v)", got, want, recvErr)
			}
			return
		}
		if e := ev.GetError(); e != nil {
			inBand = e.GetError()
		}
	}

	if inBand == nil {
		t.Fatal("a 429 produced neither an error status nor an error event")
	}
	if inBand.GetCategory() != modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_RATE_LIMITED {
		t.Errorf("category = %v, want RATE_LIMITED", inBand.GetCategory())
	}
	if !inBand.GetRetryable() {
		t.Error("a rate limit must be retryable — the kernel backs off and retries")
	}
	if got := inBand.GetRetryAfter().AsDuration(); got != 7*time.Second {
		t.Errorf("retry_after = %v, want 7s (from the retry-after header)", got)
	}
}

// TestPlugin_cancellationIsCleanShutdown cancels mid-stream and asserts
// the plugin treats it as normal control flow: the stream ends promptly,
// the subprocess stays healthy enough to close cleanly, and nothing is
// logged at ERROR for the cancellation itself
// (docs/specifications/model/README.md#transport--lifecycle,
// .claude/rules/grpc.md).
func TestPlugin_cancellationIsCleanShutdown(t *testing.T) {
	released := make(chan struct{})
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("httptest response writer does not flush")
			return
		}
		// Enough of a stream to prove the plugin is mid-flight, then hold
		// it open until the client goes away.
		_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"usage\":{\"input_tokens\":5}}}\n\n")
		_, _ = io.WriteString(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		_, _ = io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"thinking\"}}\n\n")
		flusher.Flush()
		<-r.Context().Done()
		close(released)
	}

	client, server, logs := launchPluginWithLogs(t, handler)
	configureCtx, configureCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer configureCancel()
	configure(t, configureCtx, client, server.URL)

	streamCtx, cancel := context.WithCancel(context.Background())
	stream, err := client.StreamCompletion(streamCtx, sampleRequest())
	if err != nil {
		cancel()
		t.Fatalf("StreamCompletion: %v", err)
	}

	// Read until the first real delta so the cancel genuinely lands
	// mid-stream rather than before the request was even issued.
	for {
		ev, recvErr := stream.Recv()
		if recvErr != nil {
			cancel()
			t.Fatalf("Recv before cancel: %v", recvErr)
		}
		if ev.GetTextDelta() != nil {
			break
		}
	}

	cancel()

	// The stream must end, and it must end as a cancellation rather than
	// as an application error.
	_, recvErr := stream.Recv()
	if recvErr == nil {
		t.Fatal("stream did not end after cancellation")
	}
	if code := status.Code(recvErr); code != codes.Canceled && !errors.Is(recvErr, context.Canceled) {
		t.Errorf("post-cancel status = %v, want Canceled", code)
	}

	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Error("the plugin did not release the upstream HTTP request after cancellation")
	}

	for _, rec := range logs.records() {
		if rec.level >= slog.LevelError {
			t.Errorf("cancellation produced an ERROR log, which trains operators to ignore real failures: %q", rec.msg)
		}
	}
}

// sampleRequest is the canonical request every streaming test sends —
// the worked example's shape from
// docs/specifications/model/examples.md.
func sampleRequest() *modelv1.StreamCompletionRequest {
	return &modelv1.StreamCompletionRequest{
		ModelId: testModelID,
		Messages: []*contentv1.Message{{
			Id:   "01JINTEGRATION",
			Role: contentv1.Role_ROLE_USER,
			Content: []*contentv1.ContentBlock{{
				Block: &contentv1.ContentBlock_Text{
					Text: &contentv1.TextBlock{Text: "What's in main.go?"},
				},
			}},
		}},
		CallContext: &commonv1.CallContext{
			SessionId: "01JSESSION",
			TurnId:    "01JTURN",
		},
	}
}

// configure calls Configure with a fake key and the test server's URL.
func configure(t *testing.T, ctx context.Context, client modelv1.ModelServiceClient, baseURL string) {
	t.Helper()

	cfg, err := structpb.NewStruct(map[string]any{
		"api_key": "sk-ant-integration-not-a-real-key",
		// The loopback carve-out in validateBaseURL is what makes this
		// legal — an httptest.Server is plain http on 127.0.0.1.
		"base_url": baseURL,
	})
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}
	if _, err := client.Configure(ctx, &modelv1.ConfigureRequest{Config: cfg}); err != nil {
		t.Fatalf("Configure: %v", err)
	}
}

// staticTranscript serves body as an SSE stream for any request.
func staticTranscript(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, body)
	}
}

// launchPlugin builds the launch and discards the captured logs.
func launchPlugin(t *testing.T, handler http.HandlerFunc) (modelv1.ModelServiceClient, *httptest.Server) {
	t.Helper()
	client, server, _ := launchPluginWithLogs(t, handler)
	return client, server
}

// launchPluginWithLogs starts the fake vendor, launches the real plugin
// binary through internal/pluginruntime exactly as the kernel does, and
// returns the dispensed category client alongside the captured kernel-side
// log records.
func launchPluginWithLogs(t *testing.T, handler http.HandlerFunc) (modelv1.ModelServiceClient, *httptest.Server, *logCapture) {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	capture := &logCapture{}
	logger := slog.New(capture)

	producer := &commonv1.ProducerRef{
		Category: commonv1.Category_CATEGORY_MODEL,
		Name:     "anthropic",
		Version:  "0.0.0",
	}

	backend := telemetryfake.New()
	prov, err := telemetry.New(context.Background(), telemetry.DefaultConfig, backend, nil)
	if err != nil {
		t.Fatalf("telemetry.New: %v", err)
	}
	t.Cleanup(func() {
		if err := prov.Shutdown(context.Background()); err != nil {
			t.Errorf("telemetry.Shutdown: %v", err)
		}
	})

	bus := eventbus.New()
	t.Cleanup(func() { _ = bus.Close() })

	callback := kernelcallback.NewServer(kernelcallback.Config{
		Log:            log.NewServer(logger),
		Producer:       producer,
		Telemetry:      prov,
		TelemetryRelay: telemetryrelay.New(backend.RelayedSpans),
		Bus:            bus,
		Logger:         logger,
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	pl, err := pluginruntime.Launch(ctx, pluginruntime.Config{
		BinaryPath: pluginBinary,
		Producer:   producer,
		Callback:   callback,
		Telemetry:  prov,
		Logger:     logger,
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		if err := pl.Close(closeCtx); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	client, ok := pl.Dispensed().(modelv1.ModelServiceClient)
	if !ok {
		t.Fatalf("Dispensed() is %T, want modelv1.ModelServiceClient", pl.Dispensed())
	}
	return client, server, capture
}

// logRecord is the flattened slog record the cancellation test inspects.
type logRecord struct {
	level slog.Level
	msg   string
}

// logCapture is a concurrency-safe slog.Handler fake — the plugin's own
// log callbacks arrive on a background goroutine, concurrently with the
// test's assertions (go-testing.md: fakes, not mocking frameworks).
type logCapture struct {
	mu   sync.Mutex
	recs []logRecord
}

func (c *logCapture) Enabled(context.Context, slog.Level) bool { return true }

func (c *logCapture) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.recs = append(c.recs, logRecord{level: r.Level, msg: r.Message})
	return nil
}

func (c *logCapture) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *logCapture) WithGroup(string) slog.Handler      { return c }

func (c *logCapture) records() []logRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]logRecord(nil), c.recs...)
}
