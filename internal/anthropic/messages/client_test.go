package messages

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	"github.com/pluggableharness/agent/pkg/model"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

// countReq builds the request-shaped CountTokensRequest the RPC now takes,
// carrying loose text as one user message.
func countReq(text, modelID string) *modelv1.CountTokensRequest {
	return &modelv1.CountTokensRequest{
		ModelId: modelID,
		Messages: []*contentv1.Message{{
			Role: contentv1.Role_ROLE_USER,
			Content: []*contentv1.ContentBlock{{
				Block: &contentv1.ContentBlock_Text{Text: &contentv1.TextBlock{Text: text}},
			}},
		}},
	}
}

// specFor is the minimal model.Spec CountTokens needs to translate a
// request: enough to accept text blocks, nothing more.
func specFor(id string) model.Spec {
	return model.Spec{ID: id, MaxOutputTokens: 4096}
}

// roundTripFunc adapts a function to http.RoundTripper, the standard
// fake-transport seam for testing an *http.Client without a real network
// call.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func newResponse(status int, body string, header http.Header) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     header,
	}
}

// sseFromEvents marshals each event to JSON and frames it as one SSE
// message per docs/specifications/model — a real event body round-trips
// through the same StreamEvent type Scanner decodes into.
func sseFromEvents(t *testing.T, events ...StreamEvent) string {
	t.Helper()
	var b strings.Builder
	for _, ev := range events {
		raw, err := json.Marshal(ev)
		if err != nil {
			t.Fatalf("marshal fixture event: %v", err)
		}
		b.WriteString("data: ")
		b.Write(raw)
		b.WriteString("\n\n")
	}
	return b.String()
}

func testRequest() *Request {
	return &Request{
		Model:     "claude-opus-5",
		MaxTokens: 1024,
		Messages:  []Message{{Role: roleUser, Content: []Block{{Type: blockText, Text: "hi"}}}},
		Stream:    true,
	}
}

func newTestClient(transport http.RoundTripper) *Client {
	var buf bytes.Buffer
	return NewClient(ClientConfig{
		BaseURL:   "http://anthropic.test",
		APIKey:    "sk-ant-test-key",
		Timeout:   5 * time.Second,
		Transport: transport,
		Logger:    slog.New(slog.NewTextHandler(&buf, nil)),
	})
}

func TestClient_Stream_success(t *testing.T) {
	t.Parallel()

	var captured *http.Request
	var capturedBody []byte
	body := sseFromEvents(t,
		StreamEvent{Type: eventMessageStart, Message: &StreamMessage{Usage: &Usage{InputTokens: i64p(10)}}},
		StreamEvent{Type: eventContentBlockDelta, Delta: &StreamDelta{Type: deltaText, Text: "hi"}},
		StreamEvent{Type: eventMessageDelta, Usage: &Usage{OutputTokens: i64p(3)}, Delta: &StreamDelta{StopReason: stopEndTurn}},
		StreamEvent{Type: eventMessageStop},
	)

	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		captured = r
		capturedBody, _ = io.ReadAll(r.Body)
		return newResponse(http.StatusOK, body, nil), nil
	})

	client := newTestClient(transport)
	sink := newFakeSink()

	if err := client.Stream(context.Background(), testRequest(), sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	if captured.Method != http.MethodPost {
		t.Errorf("method = %s, want POST", captured.Method)
	}
	if captured.URL.Path != messagesPath {
		t.Errorf("path = %s, want %s", captured.URL.Path, messagesPath)
	}
	if got := captured.Header.Get("x-api-key"); got != "sk-ant-test-key" {
		t.Errorf("x-api-key = %q", got)
	}
	if got := captured.Header.Get("anthropic-version"); got != anthropicVersion {
		t.Errorf("anthropic-version = %q, want %q", got, anthropicVersion)
	}
	if got := captured.Header.Get("content-type"); got != "application/json" {
		t.Errorf("content-type = %q, want application/json", got)
	}
	if got := captured.Header.Get("anthropic-beta"); got != "" {
		t.Errorf("anthropic-beta header set to %q, want no beta header", got)
	}
	if !bytes.Contains(capturedBody, []byte(`"model":"claude-opus-5"`)) {
		t.Errorf("request body missing model field: %s", capturedBody)
	}

	want := []sinkCall{
		{method: "TextDelta", args: []any{"hi"}},
		{method: "Usage", args: []any{model.Usage{InputTokens: 10, OutputTokens: 3}}},
		{method: "Stop", args: []any{modelv1.StopReason_STOP_REASON_END_TURN, ""}},
	}
	assertCalls(t, sink.calls, want)
}

func TestClient_Stream_nonRetryableClassification(t *testing.T) {
	t.Parallel()

	errBody, err := json.Marshal(APIError{Error: APIErrorBody{Type: errAuthentication, Message: "invalid api key"}})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	transport := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return newResponse(http.StatusUnauthorized, string(errBody), nil), nil
	})

	client := newTestClient(transport)
	err = client.Stream(context.Background(), testRequest(), newFakeSink())

	var modelErr *model.Error
	if !errors.As(err, &modelErr) {
		t.Fatalf("err = %v, want a *model.Error", err)
	}
	if modelErr.Category != modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_AUTH_ERROR {
		t.Errorf("Category = %v, want AUTH_ERROR", modelErr.Category)
	}
	if modelErr.Retryable {
		t.Errorf("Retryable = true, want false")
	}
}

func TestClient_Stream_retryableClassificationWithRetryAfter(t *testing.T) {
	t.Parallel()

	errBody, err := json.Marshal(APIError{Error: APIErrorBody{Type: errRateLimit, Message: "slow down"}})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	transport := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		header := http.Header{}
		header.Set("retry-after", "7")
		return newResponse(http.StatusTooManyRequests, string(errBody), header), nil
	})

	client := newTestClient(transport)
	err = client.Stream(context.Background(), testRequest(), newFakeSink())

	var modelErr *model.Error
	if !errors.As(err, &modelErr) {
		t.Fatalf("err = %v, want a *model.Error", err)
	}
	if modelErr.Category != modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_RATE_LIMITED {
		t.Errorf("Category = %v, want RATE_LIMITED", modelErr.Category)
	}
	if !modelErr.Retryable {
		t.Errorf("Retryable = false, want true")
	}
	if modelErr.RetryAfter != 7*time.Second {
		t.Errorf("RetryAfter = %v, want 7s", modelErr.RetryAfter)
	}
}

func TestClient_Stream_truncatedStream(t *testing.T) {
	t.Parallel()

	// The stream ends after a text delta with no message_stop.
	body := sseFromEvents(t,
		StreamEvent{Type: eventMessageStart},
		StreamEvent{Type: eventContentBlockDelta, Delta: &StreamDelta{Type: deltaText, Text: "partial"}},
	)
	transport := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return newResponse(http.StatusOK, body, nil), nil
	})

	client := newTestClient(transport)
	sink := newFakeSink()

	if err := client.Stream(context.Background(), testRequest(), sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	if len(sink.calls) == 0 {
		t.Fatalf("no sink calls recorded")
	}
	last := sink.calls[len(sink.calls)-1]
	if last.method != "Error" {
		t.Fatalf("last call = %+v, want an Error call for the truncated stream", last)
	}
	modelErr, ok := last.args[0].(*model.Error)
	if !ok {
		t.Fatalf("Error arg = %v, want *model.Error", last.args[0])
	}
	if modelErr.Category != modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_UNKNOWN {
		t.Errorf("Category = %v, want UNKNOWN", modelErr.Category)
	}
	if modelErr.Retryable {
		t.Errorf("Retryable = true, want false")
	}
}

func TestClient_Stream_malformedSSEIsClassifiedAsError(t *testing.T) {
	t.Parallel()

	transport := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return newResponse(http.StatusOK, "data: {not valid json\n\n", nil), nil
	})
	client := newTestClient(transport)

	err := client.Stream(context.Background(), testRequest(), newFakeSink())
	if err == nil {
		t.Fatalf("expected an error")
	}
	if errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want a decode error, not cancellation", err)
	}
}

// eofCancelReader cancels ctx the instant its wrapped reader reports EOF,
// letting a test land a real cancellation exactly at the point drive()
// checks ctx.Err() after a clean-but-empty scan loop — narrower than
// canceling before Stream is even called, which the pre-flight check
// would catch first.
type eofCancelReader struct {
	r      io.Reader
	cancel context.CancelFunc
}

func (e *eofCancelReader) Read(p []byte) (int, error) {
	n, err := e.r.Read(p)
	if err == io.EOF {
		e.cancel()
	}
	return n, err
}

func TestClient_Stream_cancellationRacesTruncatedStream(t *testing.T) {
	t.Parallel()

	body := sseFromEvents(t, StreamEvent{Type: eventContentBlockDelta, Delta: &StreamDelta{Type: deltaText, Text: "x"}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	transport := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(&eofCancelReader{r: strings.NewReader(body), cancel: cancel}),
			Header:     http.Header{},
		}, nil
	})
	client := newTestClient(transport)
	sink := newFakeSink()

	err := client.Stream(ctx, testRequest(), sink)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	for _, c := range sink.calls {
		if c.method == "Error" {
			t.Fatalf("unexpected Error call %+v — cancellation must win over the truncated-stream classification", c)
		}
	}
}

func TestClient_Stream_cancellationBeforeRequest(t *testing.T) {
	t.Parallel()

	// A custom RoundTripper is not skipped by net/http for an
	// already-canceled context — only http.Transport's own connection
	// logic short-circuits on context cancellation — so this fake checks
	// the request's context itself and returns ctx.Err(), exactly as
	// http.Transport would.
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := r.Context().Err(); err != nil {
			return nil, err
		}
		t.Fatalf("request context was not canceled")
		return nil, nil
	})
	client := newTestClient(transport)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := client.Stream(ctx, testRequest(), newFakeSink())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	var modelErr *model.Error
	if errors.As(err, &modelErr) {
		t.Fatalf("cancellation was converted into a *model.Error: %+v", modelErr)
	}
}

func TestClient_Stream_cancellationMidStream(t *testing.T) {
	t.Parallel()

	body := sseFromEvents(t,
		StreamEvent{Type: eventContentBlockDelta, Delta: &StreamDelta{Type: deltaText, Text: "x"}},
		StreamEvent{Type: eventMessageStop},
	)
	transport := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return newResponse(http.StatusOK, body, nil), nil
	})
	client := newTestClient(transport)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := newFakeSink()
	// Simulates the kernel closing the gRPC stream mid-turn: the real
	// *model.Sink detects this via its own stream context and returns
	// ctx.Err() from the first send after cancellation
	// (pkg/model/stream.go's send). Canceling ctx itself from inside the
	// sink call — not just returning context.Canceled as a value — is
	// what exercises drive()'s real ctx.Err() check rather than merely
	// passing through an error that happens to equal context.Canceled.
	sink.before["TextDelta"] = cancel
	sink.failAt["TextDelta"] = context.Canceled

	err := client.Stream(ctx, testRequest(), sink)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	var modelErr *model.Error
	if errors.As(err, &modelErr) {
		t.Fatalf("cancellation was converted into a *model.Error: %+v", modelErr)
	}
}

func TestClient_Stream_transportErrorIsClassifiedNotCancellation(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("connection refused")
	transport := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return nil, wantErr
	})
	client := newTestClient(transport)

	err := client.Stream(context.Background(), testRequest(), newFakeSink())
	if err == nil || errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want a wrapped transport error, not cancellation", err)
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("err = %v, want it to mention the underlying transport failure", err)
	}
}

func TestClient_CountTokens_success(t *testing.T) {
	t.Parallel()

	var capturedBody []byte
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		capturedBody, _ = io.ReadAll(r.Body)
		if r.URL.Path != countTokensPath {
			t.Errorf("path = %s, want %s", r.URL.Path, countTokensPath)
		}
		return newResponse(http.StatusOK, `{"input_tokens": 42}`, nil), nil
	})

	client := newTestClient(transport)
	got, err := client.CountTokens(context.Background(), countReq("hello world", "claude-opus-5"), specFor("claude-opus-5"))
	if err != nil {
		t.Fatalf("CountTokens: %v", err)
	}
	if got != 42 {
		t.Errorf("got %d, want 42", got)
	}

	want := `{"model":"claude-opus-5","messages":[{"role":"user","content":[{"type":"text","text":"hello world"}]}]}`
	if string(capturedBody) != want {
		t.Errorf("request body = %s, want %s", capturedBody, want)
	}
}

func TestClient_CountTokens_malformedResponseBody(t *testing.T) {
	t.Parallel()

	transport := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return newResponse(http.StatusOK, "{not valid json", nil), nil
	})
	client := newTestClient(transport)

	got, err := client.CountTokens(context.Background(), countReq("hi", "claude-opus-5"), specFor("claude-opus-5"))
	if err == nil {
		t.Fatalf("expected a decode error")
	}
	if got != 0 {
		t.Errorf("got %d, want 0 on error", got)
	}
}

func TestClient_CountTokens_nonRetryableClassification(t *testing.T) {
	t.Parallel()

	errBody, err := json.Marshal(APIError{Error: APIErrorBody{Type: errNotFound, Message: "no such model"}})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	transport := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return newResponse(http.StatusNotFound, string(errBody), nil), nil
	})

	client := newTestClient(transport)
	got, err := client.CountTokens(context.Background(), countReq("hi", "unknown-model"), specFor("unknown-model"))
	if got != 0 {
		t.Errorf("got %d, want 0 on error", got)
	}
	var modelErr *model.Error
	if !errors.As(err, &modelErr) {
		t.Fatalf("err = %v, want a *model.Error", err)
	}
	if modelErr.Category != modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST {
		t.Errorf("Category = %v, want INVALID_REQUEST", modelErr.Category)
	}
}

func TestClient_CountTokens_cancellation(t *testing.T) {
	t.Parallel()

	// See TestClient_Stream_cancellationBeforeRequest's comment: a fake
	// RoundTripper must check context cancellation itself.
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := r.Context().Err(); err != nil {
			return nil, err
		}
		t.Fatalf("request context was not canceled")
		return nil, nil
	})
	client := newTestClient(transport)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.CountTokens(ctx, countReq("hi", "claude-opus-5"), specFor("claude-opus-5"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// TestClient_apiKeyNeverLeaks exercises several distinct failure paths
// with a distinctive API key and asserts it never appears in a returned
// error's message or in anything logged — internal/anthropic/CLAUDE.md's
// secrets rule, and the one property this package cannot regress on
// silently.
func TestClient_apiKeyNeverLeaks(t *testing.T) {
	t.Parallel()

	const secretKey = "sk-ant-api03-do-not-leak-this-canary-value"

	var logBuf bytes.Buffer
	cfg := ClientConfig{
		BaseURL: "http://anthropic.test",
		APIKey:  secretKey,
		Logger:  slog.New(slog.NewTextHandler(&logBuf, nil)),
	}

	t.Run("transport failure", func(t *testing.T) {
		// The transport's own error is deliberately key-free. The
		// property under test is that *this client* never adds the
		// credential to an error it builds or wraps — it cannot scrub a
		// secret that an arbitrary RoundTripper chose to put in its own
		// message, and pretending otherwise would be testing the fake
		// rather than the code.
		//
		// The request-header path is asserted separately below: the key
		// travels in x-api-key and must never reach a URL, a body, or a
		// log line.
		cfg := cfg
		var sawKeyHeader bool
		cfg.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			sawKeyHeader = r.Header.Get("x-api-key") == secretKey
			if strings.Contains(r.URL.String(), secretKey) {
				t.Error("the API key reached the request URL")
			}
			return nil, errors.New("dial tcp: connection refused")
		})
		client := NewClient(cfg)
		err := client.Stream(context.Background(), testRequest(), newFakeSink())
		if err == nil {
			t.Fatalf("expected an error")
		}
		if !sawKeyHeader {
			t.Error("the API key never reached the x-api-key header, so this test proves nothing")
		}
		if strings.Contains(err.Error(), secretKey) {
			t.Fatalf("error contains the API key: %v", err)
		}
	})

	t.Run("classified HTTP failure", func(t *testing.T) {
		cfg := cfg
		cfg.Transport = roundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return newResponse(http.StatusUnauthorized, `{"error":{"type":"authentication_error","message":"invalid x-api-key"}}`, nil), nil
		})
		client := NewClient(cfg)
		err := client.Stream(context.Background(), testRequest(), newFakeSink())
		if err == nil {
			t.Fatalf("expected an error")
		}
		if strings.Contains(err.Error(), secretKey) {
			t.Fatalf("error contains the API key: %v", err)
		}
	})

	if strings.Contains(logBuf.String(), secretKey) {
		t.Fatalf("log output contains the API key: %s", logBuf.String())
	}
}

func TestNewClient_defaults(t *testing.T) {
	t.Parallel()

	client := NewClient(ClientConfig{BaseURL: "http://anthropic.test", APIKey: "k"})
	if client.http.Transport != http.DefaultTransport {
		t.Errorf("Transport = %v, want http.DefaultTransport", client.http.Transport)
	}
	if client.logger == nil {
		t.Errorf("logger = nil, want slog.Default()")
	}
}
