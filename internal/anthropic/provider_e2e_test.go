//go:build e2e

// The e2e tier makes one real, billed call to Anthropic. It exists to
// catch the single class of bug every other tier is structurally blind
// to: our idea of the wire format having drifted from the vendor's. A
// recorded transcript can only ever confirm we agree with our own past
// reading of the docs.
//
// It is double-gated on ANTHROPIC_API_KEY *and* AGENT_E2E_LIVE=1. One
// gate would not be enough — a key is present in a lot of developer
// environments for unrelated reasons, and a test that silently spends
// money whenever it finds a credential is a test people learn to distrust.
// The second gate has to be set deliberately.
//
// Not part of the required CI checks.
package anthropic_test

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pluggableharness/agent/internal/anthropic/catalog"
	"github.com/pluggableharness/agent/internal/anthropic/messages"
	"github.com/pluggableharness/agent/pkg/model"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"

	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
)

// liveModelID is the cheapest model in the roster ($1/$5 per MTok). The
// point of this tier is wire-format agreement, which every model shares,
// so paying Opus rates for it would be spending money on nothing.
const liveModelID = "claude-haiku-4-5"

// liveMaxOutputTokens is deliberately tiny. A handful of tokens is enough
// to prove the stream parses; anything more is just cost.
const liveMaxOutputTokens = 16

// requireLive skips unless both gates are set, and returns the key.
func requireLive(t *testing.T) string {
	t.Helper()

	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		t.Skip("e2e: ANTHROPIC_API_KEY is not set")
	}
	if os.Getenv("AGENT_E2E_LIVE") != "1" {
		t.Skip("e2e: AGENT_E2E_LIVE=1 is not set — refusing to spend money without an explicit opt-in")
	}
	return key
}

// liveClient builds a client against the real endpoint.
func liveClient(t *testing.T) *messages.Client {
	t.Helper()
	return messages.NewClient(messages.ClientConfig{
		BaseURL: "https://api.anthropic.com",
		APIKey:  requireLive(t),
		Timeout: 60 * time.Second,
	})
}

// liveSpec returns the roster entry for liveModelID.
func liveSpec(t *testing.T) model.Spec {
	t.Helper()
	for _, spec := range catalog.Models() {
		if spec.ID == liveModelID {
			return spec
		}
	}
	t.Fatalf("roster has no %q", liveModelID)
	return model.Spec{}
}

// TestLive_streamCompletion runs one real completion and asserts the
// stream produced text, a usage event with a plausible token count, and a
// terminal stop — i.e. that every stage of the wire format still parses
// against the live vendor.
func TestLive_streamCompletion(t *testing.T) {
	client := liveClient(t)
	spec := liveSpec(t)

	req := &modelv1.StreamCompletionRequest{
		ModelId: liveModelID,
		Params:  &modelv1.GenerationParams{MaxOutputTokens: ptr[int64](liveMaxOutputTokens)},
		Messages: []*contentv1.Message{{
			Id:   "01JE2E",
			Role: contentv1.Role_ROLE_USER,
			Content: []*contentv1.ContentBlock{{
				Block: &contentv1.ContentBlock_Text{
					Text: &contentv1.TextBlock{Text: "Reply with exactly the word: pong"},
				},
			}},
		}},
	}

	vendorReq, err := messages.BuildRequest(req, spec)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}

	sink := &liveSink{}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := client.Stream(ctx, vendorReq, sink); err != nil {
		t.Fatalf("Stream against the live API: %v", err)
	}

	if got := sink.text(); strings.TrimSpace(got) == "" {
		t.Error("the live stream produced no text")
	}
	usage, ok := sink.lastUsage()
	if !ok {
		t.Fatal("the live stream produced no usage event — the kernel would have no cost to persist")
	}
	if usage.InputTokens <= 0 {
		t.Errorf("input tokens = %d, want a positive count", usage.InputTokens)
	}
	if usage.OutputTokens <= 0 {
		t.Errorf("output tokens = %d, want a positive count", usage.OutputTokens)
	}
	if !sink.stopped() {
		t.Error("the live stream never reached a terminal stop")
	}
	if err := sink.streamError(); err != nil {
		t.Errorf("the live stream reported an in-band error: %v", err)
	}
	// Only the live tier can prove the vendor actually publishes the
	// header stream_start is built on — a recorded transcript would only
	// confirm we agree with our own past reading of the docs.
	if sink.providerRequestID() == "" {
		t.Error("the live stream produced no provider request id — nothing to correlate a failure against")
	}
}

// TestLive_countTokens proves the tokenizer endpoint still answers in the
// shape we parse. This is the RPC that decides whether the kernel treats
// a count as exact or falls back to its ceil(bytes/4) heuristic, so a
// silent break here degrades every context-budget decision.
func TestLive_countTokens(t *testing.T) {
	client := liveClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req := &modelv1.CountTokensRequest{
		ModelId: liveModelID,
		Messages: []*contentv1.Message{{
			Role: contentv1.Role_ROLE_USER,
			Content: []*contentv1.ContentBlock{{
				Block: &contentv1.ContentBlock_Text{
					Text: &contentv1.TextBlock{Text: "The quick brown fox jumps over the lazy dog."},
				},
			}},
		}},
	}

	count, err := client.CountTokens(ctx, req, liveSpec(t))
	if err != nil {
		t.Fatalf("CountTokens against the live API: %v", err)
	}
	if count <= 0 {
		t.Errorf("count = %d, want a positive count", count)
	}
}

// liveSink records what the live stream produced. Hand-written rather
// than generated, and mutex-guarded because nothing promises the client
// drives it from the calling goroutine.
type liveSink struct {
	mu        sync.Mutex
	textBuf   strings.Builder
	usage     *model.Usage
	stop      bool
	err       *model.Error
	requestID string
}

var _ messages.EventSink = (*liveSink)(nil)

// StreamStart records the vendor's request id, which a live-run failure
// report can quote when asking Anthropic about a specific request.
func (s *liveSink) StreamStart(providerRequestID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requestID = providerRequestID
	return nil
}

func (s *liveSink) TextDelta(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.textBuf.WriteString(text)
	return nil
}

func (s *liveSink) ThinkingDelta(string) error      { return nil }
func (s *liveSink) ThinkingSignature([]byte) error  { return nil }
func (s *liveSink) RedactedThinking([]byte) error   { return nil }
func (s *liveSink) ToolCallStart(_, _ string) error { return nil }
func (s *liveSink) ToolCallDelta(_, _ string) error { return nil }
func (s *liveSink) ToolCallDone(string) error       { return nil }

func (s *liveSink) Usage(u model.Usage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.usage = &u
	return nil
}

func (s *liveSink) Stop(modelv1.StopReason, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stop = true
	return nil
}

func (s *liveSink) Error(modelErr *model.Error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = modelErr
	return nil
}

func (s *liveSink) providerRequestID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requestID
}

func (s *liveSink) text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.textBuf.String()
}

func (s *liveSink) lastUsage() (model.Usage, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.usage == nil {
		return model.Usage{}, false
	}
	return *s.usage, true
}

func (s *liveSink) stopped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stop
}

func (s *liveSink) streamError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err == nil {
		return nil
	}
	return s.err
}

// ptr returns a pointer to v, for the optional proto scalars.
func ptr[T any](v T) *T { return &v }
