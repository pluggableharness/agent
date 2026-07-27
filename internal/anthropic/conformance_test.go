package anthropic_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/pluggableharness/agent/internal/anthropic"
	"github.com/pluggableharness/agent/pkg/model/modeltest"
)

// TestConformance runs the shared conformance suite against the real
// Anthropic provider, pointed at a canned in-process vendor rather than
// the network.
//
// This is what keeps the suite honest in both directions: a protocol
// change that the suite does not understand fails here, and a suite
// assertion that no real provider could satisfy fails here too. The
// declarative half — every capability and pricing invariant across the
// whole roster — is exercised regardless of what the fake vendor returns.
func TestConformance(t *testing.T) {
	t.Parallel()

	p := anthropic.New(anthropic.WithTransport(cannedVendor{}))

	cfg, err := structpb.NewStruct(map[string]any{
		"api_key": "sk-ant-conformance-fixture",
		// Loopback http is permitted precisely so a test can point the
		// provider at a fake vendor; see internal/anthropic/CLAUDE.md.
		"base_url": "http://127.0.0.1:1",
	})
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}

	modeltest.Run(t, p, modeltest.WithConfig(cfg))
}

// cannedVendor answers every request with a minimal, well-formed
// Anthropic SSE stream, so the behavioral checks exercise the real
// translation path with no network.
type cannedVendor struct{}

func (cannedVendor) RoundTrip(req *http.Request) (*http.Response, error) {
	const stream = `event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":8,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"pong"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}

event: message_stop
data: {"type":"message_stop"}

`
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"text/event-stream"},
			// Exercises the stream_start path: the adapter reads this from
			// headers before any content arrives.
			"Request-Id": []string{"req_conformance_fixture"},
		},
		Body:    io.NopCloser(strings.NewReader(stream)),
		Request: req,
	}, nil
}
